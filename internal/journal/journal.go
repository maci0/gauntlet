// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package journal records every run under ~/.gauntlet as JSONL.
//
// Layout:
//
//	~/.gauntlet/
//	  runs/2026-08-25/<run-id>.jsonl   one file per run: the full event stream
//	  index.jsonl                      one summary line per finished run
//	  state/<run-id>.json              hot-reload handoff, deleted after pickup
//
// Date sharding keeps any single directory listing small, and the flat index
// makes "what did I run last week" a tail, not a tree walk. Nothing here is
// load-bearing: a journal that cannot be written degrades to a warning, never
// a failed run.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Home is the root of the journal tree. GAUNTLET_HOME overrides it.
func Home() string {
	if h := os.Getenv("GAUNTLET_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gauntlet"
	}
	return filepath.Join(home, ".gauntlet")
}

// StateDir holds hot-reload handoff files.
func StateDir() string { return filepath.Join(Home(), "state") }

// NewRunID returns a sortable, collision-resistant id for one run.
func NewRunID(now time.Time) string {
	return fmt.Sprintf("%s-%04x", now.UTC().Format("20060102T150405Z"), os.Getpid()&0xffff)
}

// Summary is the one-line record of a finished run, appended to index.jsonl.
type Summary struct {
	RunID     string    `json:"run_id"`
	Path      string    `json:"path"`
	Version   string    `json:"version"`
	Dirs      []string  `json:"dirs"`
	Agents    []string  `json:"agents,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Loops     int       `json:"loops"`
	Reviews   int       `json:"reviews"`
	OK        int       `json:"ok"`
	Failed    int       `json:"failed"`
	Skipped   int       `json:"skipped,omitempty"`
	Conflicts int       `json:"conflicts,omitempty"`
	Ins       int       `json:"ins,omitempty"`
	Del       int       `json:"del,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
	ExitCode  int       `json:"exit_code"`
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
}

// Open creates the journal file for one run.
//
// The shard is dated in UTC to agree with NewRunID, which embeds the UTC
// timestamp: sharding by host-local wall time would file a run started just
// past local midnight under a day that disagrees with its id, so correlating
// an id prefix with a directory misses by one day for every run in that
// window. Rendering stays free to convert to local at display time.
func Open(runID string, now time.Time) (*Journal, error) {
	dir := filepath.Join(Home(), "runs", now.UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, runID+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
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
	_ = j.w.Flush()
}

// Close finishes the journal and appends the run to the index.
func (j *Journal) Close(s Summary) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.w.Flush(); err != nil {
		j.err = err
	}
	if err := j.f.Close(); err != nil && j.err == nil {
		j.err = err
	}
	s.RunID, s.Path = j.runID, j.path
	if err := appendIndex(s); err != nil && j.err == nil {
		j.err = err
	}
	return j.err
}

func appendIndex(s Summary) error {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Home(), "index.jsonl"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// CloseQuiet flushes and closes the journal without writing an index entry.
// A hot reload uses it: the successor continues the same run and writes the
// one summary row that covers all of it.
func (j *Journal) CloseQuiet() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = j.w.Flush()
	_ = j.f.Close()
}

// Recent returns the last n runs from the index, newest first. The index is
// append-only and grows for the life of the install, so it is read backwards
// from the end in bounded slices instead of being loaded whole: listing runs
// costs what the listing shows, not the size of every run ever recorded.
// A truncated or partly corrupt index yields what could be parsed rather than
// an error: this is a convenience log, not a ledger.
func Recent(n int) ([]Summary, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(filepath.Join(Home(), "index.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()

	for chunk := int64(recentChunk); ; chunk *= 2 {
		start := max(size-chunk, 0)
		data := make([]byte, size-start)
		if _, err := f.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		out, enough := parseTail(data, start > 0, n)
		if enough || start == 0 {
			return out, nil
		}
	}
}

// recentChunk is the first slice taken from the end of the index: thousands of
// runs at ~300 bytes each, so most installs never read twice.
const recentChunk = 256 << 10

// parseTail parses newline-delimited summaries out of data, newest last,
// returning up to n of them newest first. dropFirst says whether the head of
// data may cut a line in half (it does whenever the slice starts past byte 0).
func parseTail(data []byte, dropFirst bool, n int) (out []Summary, enough bool) {
	lines := strings.Split(string(data), "\n")
	first := 0
	if dropFirst && len(lines) > 0 {
		first = 1 // the line the slice landed inside is not complete
	}
	out = make([]Summary, 0, min(n, len(lines)))
	for i := len(lines) - 1; i >= first; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var s Summary
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}
		out = append(out, s)
		if len(out) == n {
			return out, true
		}
	}
	return out, false
}

// Events replays one run's journal. Used by `gauntlet show <run-id>`.
func Events(runID string) ([]map[string]any, error) {
	path, err := findRun(runID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err == nil {
			out = append(out, m)
		}
	}
	return out, sc.Err()
}

// ErrNoJournal marks a lookup miss: the run id is not in the tree. Callers
// use it to report a bad command-line argument (usage) rather than a general
// failure.
var ErrNoJournal = errors.New("no journal for run")

// findRun locates a run journal by id, searching the date shards newest first.
func findRun(runID string) (string, error) {
	root := filepath.Join(Home(), "runs")
	days, err := os.ReadDir(root)
	if err != nil {
		// A journal tree that does not exist yet means no such run, not a
		// filesystem error: `gauntlet show <id>` before the first run, or on
		// a fresh GAUNTLET_HOME, must read as a miss, not as ENOENT noise.
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w %s under %s", ErrNoJournal, runID, root)
		}
		return "", err
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Name() > days[j].Name() })
	for _, d := range days {
		p := filepath.Join(root, d.Name(), runID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w %s under %s", ErrNoJournal, runID, root)
}
