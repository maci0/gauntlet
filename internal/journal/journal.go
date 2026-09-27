// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package journal records every run under ~/.gauntlet as JSONL.
//
// Layout:
//
//	~/.gauntlet/
//	  runs/2026-08-25/<run-id>.jsonl   one file per run: the full event stream
//	  index.jsonl                      one summary line per finished run
//	  .index.lock                      serializes index rebuilds and Close
//	  state/<run-id>.json              hot-reload handoff, deleted after pickup
//
// Date sharding keeps any single directory listing small, and the flat index
// makes "what did I run last week" a tail, not a tree walk. The journals are
// the source of truth: a missing or empty index is rebuilt from them, and a
// stale one has every unindexed journal in the listing window appended, so a
// crash that flushed the event stream but never wrote the summary row still
// lists, including one that sits behind a later Close, and two such crashes
// in a row do not hide the older one. Nothing here is load-bearing for a run
// in progress: a journal that cannot be written degrades to a warning, never
// a failed run.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// Home is the root of the state tree, resolved by gauntlethome.Dir:
// GAUNTLET_HOME if set, else $HOME/.gauntlet. With no usable HOME it degrades
// to ".gauntlet" beside the working directory: nothing here is load-bearing,
// and a degraded location beats refusing to run.
//
// agent.CustomFilePath resolves the same root for agents.json but refuses a
// working-directory fallback, because a definitions file picked up from there
// could be planted by the reviewed tree.
func Home() string {
	root, _ := gauntlethome.Dir()
	return root
}

// StateDir holds hot-reload handoff files.
func StateDir() string { return filepath.Join(Home(), "state") }

// NewRunID returns a sortable, collision-resistant id for one run.
func NewRunID(now time.Time) string {
	return fmt.Sprintf("%s-%04x", now.UTC().Format("20060102T150405Z"), os.Getpid()&0xffff)
}

// Summary is the one-line record of a finished run, appended to index.jsonl.
type Summary struct {
	RunID       string    `json:"run_id"`
	Path        string    `json:"path"`
	Version     string    `json:"version"`
	Dirs        []string  `json:"dirs"`
	Agents      []string  `json:"agents,omitempty"`
	Args        []string  `json:"args,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Elapsed     float64   `json:"elapsed_s,omitempty"` // monotonic seconds; Duration
	Loops       int       `json:"loops"`
	Reviews     int       `json:"reviews"`
	OK          int       `json:"ok"`
	Failed      int       `json:"failed"`
	Skipped     int       `json:"skipped,omitempty"`
	Conflicts   int       `json:"conflicts,omitempty"`
	Interrupted int       `json:"interrupted,omitempty"`
	// Other counts reviews whose terminal status this build does not
	// recognize. It exists so a journal written by a newer version still
	// reconciles: every review counted is in exactly one bucket.
	Other int `json:"other,omitempty"`
	Ins   int `json:"ins,omitempty"`
	Del   int `json:"del,omitempty"`
	// LinesMeasured says whether Ins and Del were counted. Without it a run
	// whose git was unavailable, or whose reviews shared a tree so no honest
	// attribution was possible, is listed as +0/-0, which reads exactly like a
	// measured run that changed nothing.
	LinesMeasured bool `json:"lines_measured,omitempty"`
	Tokens        int  `json:"tokens,omitempty"`
	// ExitCode is a pointer because 0 is the success code: only Close knows
	// it, and a row rebuilt from a run that died before Close has none. A
	// plain int would serialize that absence as a passing exit.
	ExitCode *int `json:"exit_code,omitempty"`
}

// Duration is how long the run lasted, and whether that span is known.
//
// Close records the monotonic elapsed the process measured (JSON seconds,
// matching event elapsed_s, not a time.Duration's nanoseconds), so an NTP
// step or a manual clock set between Start and End cannot turn a 30-minute
// run into 0s (clock stepped back) or 90 minutes (clock stepped forward).
// Old index rows, and rows rebuilt from the event stream, have only wall
// timestamps: End.Sub(Start) is then the fallback, and a pair that moved
// backwards is missing rather than a negative duration.
func (s Summary) Duration() (time.Duration, bool) {
	if s.Elapsed > 0 && !math.IsNaN(s.Elapsed) && !math.IsInf(s.Elapsed, 0) &&
		s.Elapsed <= float64(math.MaxInt64/int64(time.Second)) {
		return time.Duration(s.Elapsed * float64(time.Second)), true
	}
	if s.Start.IsZero() || s.End.IsZero() || s.End.Before(s.Start) {
		return 0, false
	}
	return s.End.Sub(s.Start), true
}

// Journal is the append-only event log of one run. It is safe for concurrent
// use: parallel workers publish from their own goroutines.
type Journal struct {
	runID string
	path  string

	mu  sync.Mutex
	f   *os.File
	w   *bufio.Writer
	enc *json.Encoder
	err error

	closed  bool // the file is flushed and closed; a later Close only indexes
	indexed bool // the summary row is written; further Closes add nothing
}

// Open creates the journal file for one run.
//
// The shard is dated in UTC to agree with NewRunID, which embeds the UTC
// timestamp: sharding by host-local wall time would file a run started just
// past local midnight under a day that disagrees with its id, so correlating
// an id prefix with a directory misses by one day for every run in that
// window. Rendering stays free to convert to local at display time.
func Open(runID string, now time.Time) (*Journal, error) {
	if !validRunID(runID) {
		return nil, fmt.Errorf("invalid run id: %q", runID)
	}
	shard := shardFromRunID(runID)
	if shard == "" {
		shard = now.UTC().Format("2006-01-02")
	}
	path := filepath.Join(Home(), "runs", shard, runID+".jsonl")
	// A hot reload continues the same run id in a new process, and a reload
	// that crosses UTC midnight derives a different shard from the successor's
	// clock. That would split one run's event stream over two files, and a
	// replay by id would find only the newer half, so follow the file the run
	// already has when there is one.
	if prev, ok, err := locateRun(runID); err != nil {
		return nil, fmt.Errorf("cannot locate existing journal for %s: %w", runID, err)
	} else if ok {
		path = prev
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	created := err == nil
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
	}
	if created {
		// The journal is the source of truth the index is rebuilt from, so a
		// machine that lost power must not lose the file that carries it. A
		// new file's name lives in its directory until that directory is
		// synced; the contents are synced at Close.
		if err := gauntlethome.SyncDir(dir); err != nil {
			f.Close()
			return nil, err
		}
	}
	w := bufio.NewWriterSize(f, 32<<10)
	return &Journal{runID: runID, path: path, f: f, w: w, enc: json.NewEncoder(w)}, nil
}

// Write appends one event. A nil Journal is a no-op, so callers never branch.
func (j *Journal) Write(v any) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != nil {
		return
	}
	if err := j.enc.Encode(v); err != nil {
		j.err = err
	}
}

// Flush pushes buffered lines to disk. Called at loop boundaries so a killed
// run still leaves a useful journal.
func (j *Journal) Flush() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	// A full disk fails here, halfway through a run, and Close is what
	// reports it: keep the first error rather than letting the journal go
	// quiet and still look complete.
	if err := j.w.Flush(); err != nil && j.err == nil {
		j.err = err
	}
}

// Close finishes the journal and appends the run to the index.
//
// It converges: a hot reload closed the file quietly before execing away, and
// when that exec fails the dying process still has to finish its own run. So
// Close after CloseQuiet skips straight to the index append. A successful
// append is sticky so a repeated Close never writes a second row; a failed
// one leaves the journal unindexed so a later Close can retry. One run, one
// summary, whatever order the endings arrive in.
func (j *Journal) Close(s Summary) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.indexed {
		return j.err
	}
	j.closeFileLocked()
	s.RunID, s.Path = j.runID, j.path
	if err := appendIndex(s); err != nil {
		// An index failure is not sticky: a later Close retries the append.
		// A journal write error that already landed in j.err still wins,
		// but both are preserved so neither is swallowed.
		if j.err != nil {
			return errors.Join(j.err, fmt.Errorf("append index: %w", err))
		}
		return fmt.Errorf("append index: %w", err)
	}
	j.indexed = true
	return j.err
}
