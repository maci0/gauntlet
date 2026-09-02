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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
	path := filepath.Join(Home(), "runs", now.UTC().Format("2006-01-02"), runID+".jsonl")
	// A hot reload continues the same run id in a new process, and a reload
	// that crosses UTC midnight derives a different shard from the successor's
	// clock. That would split one run's event stream over two files, and a
	// replay by id would find only the newer half, so follow the file the run
	// already has when there is one.
	if prev, ok, _ := locateRun(runID); ok {
		path = prev
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
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
// Close after CloseQuiet skips straight to the index append, and a repeated
// Close never writes a second row: one run, one summary, whatever order the
// endings arrive in.
func (j *Journal) Close(s Summary) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.indexed {
		return j.err
	}
	if !j.closed {
		// The same keep-the-first-error rule Flush and CloseQuiet follow: a
		// mid-run write failure must not be eclipsed by a later flush result.
		if err := j.w.Flush(); err != nil && j.err == nil {
			j.err = err
		}
		if err := j.f.Close(); err != nil && j.err == nil {
			j.err = err
		}
		j.closed = true
	}
	s.RunID, s.Path = j.runID, j.path
	j.indexed = true
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
	line, err := json.Marshal(s)
	if err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// CloseQuiet flushes and closes the journal without writing an index entry.
// A hot reload uses it: the successor continues the same run and writes the
// one summary row that covers all of it. If the exec then fails, a later
// Close still writes that row; nothing is lost by closing quietly first.
func (j *Journal) CloseQuiet() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return
	}
	j.closed = true
	if err := j.w.Flush(); err != nil && j.err == nil {
		j.err = err
	}
	if err := j.f.Close(); err != nil && j.err == nil {
		j.err = err
	}
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
	if n <= 0 {
		return nil, false // up to zero entries is no entries
	}
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

// Events replays one run's journal, handing each decodable event to visit in
// file order. Used by `gauntlet show <run-id>`. Visiting streams rather than
// collects: a long run's journal is bounded by disk, not by RAM, and every
// consumer prints one line at a time anyway. Lines that do not parse are
// skipped, matching the index reader's tolerance for a killed process.
func Events(runID string, visit func(map[string]any)) error {
	return events(runID, nil, visit)
}

// events replays a journal like Events, but when gate is non-nil it is applied
// to each raw line first and only passing lines are decoded. A gate is a
// conservative prefilter, not an authority: a line that slips past it is
// decoded and decided normally.
func events(runID string, gate func([]byte) bool, visit func(map[string]any)) error {
	path, err := findRun(runID)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if gate != nil && !gate(line) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err == nil {
			visit(m)
		}
	}
	return sc.Err()
}

// ErrNoJournal marks a lookup miss: the run id is not in the tree. Callers
// use it to report a bad command-line argument (usage) rather than a general
// failure.
var ErrNoJournal = errors.New("no journal for run")

// maxRunIDLen bounds a run id: NewRunID produces 23 bytes, and a cap keeps a
// hostile or fat-fingered argument from building absurd paths.
const maxRunIDLen = 128

// validRunID reports whether s may be joined into a journal path. Run ids are
// generated by NewRunID (digits, T, Z, and hex), but the CLI also accepts one
// from the command line, so anything outside the safe charset, and any "..",
// is treated as a lookup miss.
func validRunID(s string) bool {
	if s == "" || len(s) > maxRunIDLen {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// findRun locates a run journal by id, searching the date shards newest first.
func findRun(runID string) (string, error) {
	if p, ok, err := locateRun(runID); err != nil {
		return "", err
	} else if ok {
		return p, nil
	}
	return "", fmt.Errorf("%w %s under %s", ErrNoJournal, runID,
		filepath.Join(Home(), "runs"))
}

// locateRun returns the path already holding runID's journal, if any. Shards
// are searched newest first, so a run that somehow has more than one file
// resolves to the one a replay would read anyway.
//
// The id is validated first: `gauntlet show` hands it over straight from the
// command line, and an unvalidated join would let "../" climb out of the runs
// tree and stat (then read) any *.jsonl the user can reach.
func locateRun(runID string) (string, bool, error) {
	if !validRunID(runID) {
		return "", false, nil
	}
	root := filepath.Join(Home(), "runs")
	days, err := os.ReadDir(root)
	if err != nil {
		// A journal tree that does not exist yet means no such run, not a
		// filesystem error: `gauntlet show <id>` before the first run, or on
		// a fresh GAUNTLET_HOME, must read as a miss, not as ENOENT noise.
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Name() > days[j].Name() })
	for _, d := range days {
		p := filepath.Join(root, d.Name(), runID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p, true, nil
		}
	}
	return "", false, nil
}

// ReviewHistory is what past runs did with one review in one directory.
type ReviewHistory struct {
	Runs    int // times the review finished there
	Changed int // times it left lines behind
}

// How far back History looks: far enough for a repeat pattern to show, near
// enough that it follows a project that has moved on. Journals are read whole,
// so the number of matching runs is capped as well: the answer is a weighting,
// not an audit, and a machine with a year of runs must not pay for all of them.
const (
	historyRuns    = 30
	historyMatches = 8
)

// reviewEndJSON is the byte form of the one event kind History cares about,
// as the journal encoder writes it: quoted, unescaped ASCII. The gate is a
// prefilter, not a parse, so a stray occurrence elsewhere only costs a decode.
var reviewEndJSON = []byte(`"review_end"`)

// mergeJSON gates for merge events the same way. An isolated review (--jobs)
// publishes its review_end before its own commit exists, so the line counts
// it measured against that commit ride on the merge event instead; without
// this gate, every review that ever ran in worktree mode would look like it
// never changed a line, and the history signal would demote exactly the
// reviews that keep landing work.
var mergeJSON = []byte(`"merge"`)

// History reports, per review, how a directory's own past runs went: how often
// each review ran there and how often it actually changed something. It is the
// only signal that improves with use, and it costs one pass over the recent
// journals.
//
// Runs are counted on review_end only when the review finished (status ok, or
// no status on older journals). A skip, timeout, failure, or interrupt is not
// a finished run: counting those as "ran and changed nothing" would demote
// reviews the operator cancelled or that never launched.
// Changed comes from either event that carries the review's measured lines:
// a sequential review's review_end, or the merge event an isolated review's
// work lands through. A merge that did not land (conflict, failure) carries no
// counts and changes nothing, exactly like a review that changed nothing.
//
// Runs that cannot be read are skipped: this is a convenience log, and a
// suggestion is not worth failing over.
func History(dir string) (map[string]ReviewHistory, error) {
	runs, err := Recent(historyRuns)
	if err != nil {
		return nil, err
	}
	out := map[string]ReviewHistory{}
	matched := 0
	for _, run := range runs {
		if !slices.Contains(run.Dirs, dir) {
			continue
		}
		if matched++; matched > historyMatches {
			break
		}
		// A run journal records one line per output event, so it holds an
		// order of magnitude more output than history events. The gate skips
		// their decode; a line without either marker cannot be one of the two
		// events counted here.
		_ = events(run.RunID, func(line []byte) bool {
			return bytes.Contains(line, reviewEndJSON) || bytes.Contains(line, mergeJSON)
		}, func(e map[string]any) {
			kind, _ := e["ev"].(string)
			if d, _ := e["dir"].(string); d != dir {
				return
			}
			name, _ := e["review"].(string)
			if name == "" {
				return
			}
			ins, _ := e["ins"].(float64)
			del, _ := e["del"].(float64)
			switch kind {
			case "review_end":
				if s, _ := e["status"].(string); s != "" && s != "ok" {
					return
				}
				h := out[name]
				h.Runs++
				if ins+del > 0 {
					h.Changed++
				}
				out[name] = h
			case "merge":
				// The run itself is already counted by this review's
				// review_end; the merge only ever adds whether it landed
				// measured work. The loop-step merge into --merge-into
				// carries no counts and names a branch, not a review, so it
				// falls out on both.
				if s, _ := e["status"].(string); s != "ok" {
					return
				}
				if ins+del > 0 {
					h := out[name]
					h.Changed++
					out[name] = h
				}
			}
		})
	}
	return out, nil
}
