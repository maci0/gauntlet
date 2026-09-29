// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package journal records every run under ~/.gauntlet as JSONL.
//
// Layout:
//
//	~/.gauntlet/
//	  runs/2026-08-25/<run-id>.jsonl   one file per run: the full event stream
//	  index.jsonl                      one summary line per finished run
//	  pruned/2026-08-25/<run-id>.jsonl journals Prune moved out of the listing
//	  .index.lock                      serializes index rebuilds and Close
//	  state/<run-id>.json              hot-reload handoff, deleted after pickup
//
// agents.json sits in the same root and is the one file there this package
// never writes: it holds the user's custom agent definitions, so it is theirs
// to back up (see the backup and restore section of docs/RUNS.md).
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
//
// Nothing here removes a run, so the tree would otherwise grow one file per run
// for the life of the install. Prune is that bound, and it is what --keep-runs
// drives.
package journal

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/safefile"
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

// NewRunID returns a sortable id for one run: the UTC start instant, so ids
// order by age, then the pid that minted it. The order is read back with
// runIDOrder, not with a text compare: the stamp is fixed width and orders
// itself, but the pid is hex of a variable width, so two runs minted in the
// same second do not sort by pid as text.
//
// The whole pid, not a prefix of it, because the id is the key the index
// dedupes on and the journal file is opened by: two runs whose ids match share
// one event stream, and the second one's ending replaces the first one's row.
// Truncating the pid to its low 16 bits made that a question of a shared
// second and a wrapped pid rather than of two live processes, and two live
// processes never share a pid.
func NewRunID(now time.Time) string { return runIDFor(now, os.Getpid()) }

// ErrInvalidRunID marks a run id this package will not open: a caller passing
// one has the argument wrong, not the tree.
var ErrInvalidRunID = errors.New("invalid run id")

// runIDFor is NewRunID for a stated pid, so the one thing the id has to
// guarantee can be tested against pids no live pair could be.
func runIDFor(now time.Time, pid int) string {
	return fmt.Sprintf("%s-%x", now.UTC().Format("20060102T150405Z"), pid)
}

// runIDStamp is the layout runIDFor writes the instant in, and the length of
// that instant's field.
const (
	runIDStampLayout = "20060102T150405Z"
	runIDStampLen    = 16
)

// runIDOrder compares two run ids oldest first, which is the order the listing,
// the prune window, and the quarantine window all read them in.
//
// A generated id is a fixed-width UTC stamp, a dash, and the pid in hex. The
// stamp orders across seconds on its own, because it is the same width every
// time and digits compare as digits. The pid does not: %x drops leading zeros,
// so "1f4" (500) is a prefix of "1f400" (128000) and sorts below it as text,
// and an order read off the text files the newer of two same-second runs as
// the older one. Which pid is later is a number, so the tail is compared as
// one.
//
// An id that is not in the generated form (a hand-named journal, a fixture) is
// not reordered against a generated one, because the two have no common shape
// to order by and the text compare the directory walk was already giving them
// is as good an answer as any.
func runIDOrder(a, b string) int {
	sa, ta, oka := splitRunID(a)
	sb, tb, okb := splitRunID(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	if sa != sb {
		return strings.Compare(sa, sb)
	}
	pa, oka := hexPID(ta)
	pb, okb := hexPID(tb)
	if oka && okb && pa != pb {
		return cmp.Compare(pa, pb)
	}
	return strings.Compare(ta, tb)
}

// splitRunID returns the instant a generated id carries and the pid after it.
// false says the id is not in the generated form, so it orders as text.
func splitRunID(id string) (stamp, tail string, ok bool) {
	if len(id) > runIDStampLen+1 && id[runIDStampLen] == '-' {
		if t, err := time.Parse(runIDStampLayout, id[:runIDStampLen]); err == nil && t.UTC().Format(runIDStampLayout) == id[:runIDStampLen] {
			return id[:runIDStampLen], id[runIDStampLen+1:], true
		}
	}
	return "", "", false
}

// hexPID reads a run id's tail as the pid it is written from. false for a tail
// that is not hex, which then orders as text.
func hexPID(tail string) (uint64, bool) {
	if tail == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(tail, 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
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
	if d, ok := humanize.Seconds(s.Elapsed); ok {
		return d, true
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
	w   *lineBuffer
	enc *json.Encoder
	err error
	// dropped counts the events Write turned away after err was set. The
	// error itself names the failure once; the count is what says how much
	// of the run the journal never recorded, so a summary that reads as
	// complete is not taken at face value.
	dropped int

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
		f, err = openNoFollow(path, os.O_WRONLY|os.O_APPEND)
		if err != nil {
			return nil, err
		}
	}
	// A shared lock for the life of the handle says this stream is still being
	// written: Prune probes the journals it is about to quarantine with an
	// exclusive lock and leaves the ones it cannot take alone, so a second
	// gauntlet sharing the state tree cannot have a run moved out from under
	// it mid-run. A filesystem with no flock takes no lock, and the journal is
	// written exactly as it was before this lock existed.
	lockWriter(f)
	holdStream(path)
	if created {
		// The journal is the source of truth the index is rebuilt from, so a
		// machine that lost power must not lose the file that carries it. A
		// new file's name lives in its directory until that directory is
		// synced; the contents are synced at Close.
		if err := gauntlethome.SyncDir(dir); err != nil {
			// The stream is dropped with the handle: no Journal is returned, so
			// nothing will ever call closeFileLocked to release it, and a
			// process that keeps failing here would leave one entry per failed
			// open in the map for the rest of its life.
			forgetStream(path)
			f.Close()
			return nil, err
		}
	}
	w := newLineBuffer(f)
	return &Journal{runID: runID, path: path, f: f, w: w, enc: json.NewEncoder(w)}, nil
}

// journalBufferBytes is how much a journal holds before it writes. It trades
// syscalls against how much a killed run loses, at loop boundaries.
const journalBufferBytes = 32 << 10

// lineBuffer batches whole JSONL lines before writing them, so a flush never
// lands mid-line. bufio.Writer writes whatever fills its buffer, which with a
// 32 KiB journal buffer cuts a long event in half and leaves the file ending
// on something other than a newline: the run then reports as a journal cut
// mid-line while it is still writing, and doctor sends the operator to re-take
// an archive that was never short.
type lineBuffer struct {
	f   *os.File
	buf []byte
}

func newLineBuffer(f *os.File) *lineBuffer {
	return &lineBuffer{f: f, buf: make([]byte, 0, journalBufferBytes)}
}

// Write takes the bytes json.Encoder produced, a whole value plus its
// newline, and holds them until there is a full buffer's worth or Flush asks
// for them. Nothing reaches the file until a line is complete.
func (l *lineBuffer) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	if len(l.buf) >= journalBufferBytes {
		return len(p), l.writeThrough(-1)
	}
	return len(p), nil
}

// Flush writes everything held, whole lines and all.
func (l *lineBuffer) Flush() error { return l.writeThrough(len(l.buf)) }

// writeThrough writes the first n bytes of the buffer and keeps the rest. A
// negative n holds every complete line, which is what the size-triggered write
// wants: the tail belongs to a line the encoder has not finished.
func (l *lineBuffer) writeThrough(n int) error {
	if n < 0 {
		n = bytes.LastIndexByte(l.buf, '\n') + 1
	}
	if n == 0 {
		return nil
	}
	if _, err := l.f.Write(l.buf[:n]); err != nil {
		return err
	}
	l.buf = l.buf[:copy(l.buf, l.buf[n:])]
	return nil
}

// openNoFollow opens an existing file for writing, refusing a symlink in its
// place. os.OpenFile has no O_NOFOLLOW, and the two appends that reach an
// already-existing file here are the ones with no O_EXCL to prove the create:
// the journal for a run id that is already on disk, and the index row. The
// state root is 0700, but a GAUNTLET_HOME that resolves beside the working
// directory (no usable HOME) puts it inside the reviewed tree, where a
// committed .gauntlet/runs/<shard>/<id>.jsonl link would otherwise receive
// the run's paths, prompt names, and agent output. The lock file and the
// journals prune probes already carry the flag; these two were the gap.
func openNoFollow(path string, flag int) (*os.File, error) {
	fd, err := syscall.Open(path, flag|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openRead opens an existing journal for reading, refusing a symlink and a
// non-regular file in its place. The write side's openNoFollow covers a link
// planted at the path; the read side needs the same refusal plus a regular-file
// check, because a directory entry is not the file it points at: a FIFO named
// after a run id lists as a journal, and opening one for reading blocks until
// a writer appears, which for a planted node is never.
func openRead(path string) (*os.File, error) {
	f, _, err := safefile.OpenRead(path)
	return f, err
}

// Write appends one event. A nil Journal is a no-op, so callers never branch.
//
// Once an event has failed to write, later events are turned away rather than
// each retried into the same failure, and the ones turned away are counted so
// Close can report the gap. Silently dropping them would leave a journal that
// ends mid-run and a summary that reads as though the run ended there.
func (j *Journal) Write(v any) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.err != nil {
		j.dropped++
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
	// The summary describes the whole run, so a run whose journal is missing
	// a tail has to say how much is missing before the count of what it did
	// record is read as the count of what happened.
	journalErr := j.err
	if journalErr != nil && j.dropped > 0 {
		journalErr = fmt.Errorf("%w (%d further events were not recorded)", journalErr, j.dropped)
	}
	if err := appendIndex(s); err != nil {
		// An index failure is not sticky: a later Close retries the append.
		// A journal write error that already landed in j.err still wins,
		// but both are preserved so neither is swallowed.
		if journalErr != nil {
			return errors.Join(journalErr, fmt.Errorf("append index: %w", err))
		}
		return fmt.Errorf("append index: %w", err)
	}
	j.indexed = true
	return journalErr
}
