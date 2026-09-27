// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// index.jsonl: one summary line per finished run, and the recovery that keeps
// it honest. The journals under runs/ are the source of truth; a missing or
// stale index is rebuilt from them, so a process that flushed its events and
// died before Close still lists.

package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

func indexPath() string { return filepath.Join(Home(), "index.jsonl") }

func indexLockPath() string { return filepath.Join(Home(), ".index.lock") }

// withIndexLock serializes index mutations across processes. writeIndex
// replaces index.jsonl by rename, so the lock lives in a sibling file: a
// flock on the index itself would be left on the old inode after the swap.
func withIndexLock(fn func() error) error {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	fd, err := syscall.Open(indexLockPath(),
		syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return fmt.Errorf("index lock path is not a regular file: %s", indexLockPath())
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	return fn()
}

func appendIndex(s Summary) error {
	return withIndexLock(func() error {
		return appendIndexLocked(s)
	})
}

// The run id is the key, and a run can reach an ending twice: the process
// that owns it Closes, while another process reading the history meanwhile
// reconstructs the row from the journal on disk, because a run that has not
// Closed yet has no row. Appending both would leave two rows for one run, and
// the listing would show the right one only because dedupeRunIDs prefers the
// newest. A row already naming this run is therefore dropped and the new one
// written in its place, so the index holds one row per run and the file grows
// by exactly one line per run however often the ending is reached.
func appendIndexLocked(s Summary) (err error) {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if prev, err := readIndexFile(); err != nil {
		return err
	} else if kept, dropped := dropIndexRows(prev, s.RunID); dropped {
		return writeIndexBytes(append(kept, line...))
	}
	f, err := os.OpenFile(indexPath(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err = f.Write(line); err != nil {
		return err
	}
	return f.Sync()
}

// readIndexFile reads the whole index, which is what proving a run id absent
// costs. A missing index is empty content, not an error: the first run of an
// install has none.
func readIndexFile() ([]byte, error) {
	data, err := os.ReadFile(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// dropIndexRows returns the index with every line naming runID removed, and
// whether it removed any. A line that does not decode, or carries no id, is
// kept: the index is a convenience log, and a mangled row is not this run's
// to delete. Each surviving line is re-terminated, so a file that was not
// already one-row-per-line comes out of the rewrite as one.
func dropIndexRows(data []byte, runID string) (kept []byte, dropped bool) {
	out := make([]byte, 0, len(data))
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimRight(line, "\r \t")
		if len(line) == 0 {
			continue
		}
		var row struct {
			RunID string `json:"run_id"`
		}
		if runID != "" && json.Unmarshal(line, &row) == nil && row.RunID == runID {
			dropped = true
			continue
		}
		out = append(append(out, line...), '\n')
	}
	return out, dropped
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
	j.closeFileLocked()
}

// closeFileLocked flushes and closes the journal file under j.mu.
// The same keep-the-first-error rule Flush and CloseQuiet follow: a
// mid-run write failure must not be eclipsed by a later flush result.
//
// The flush is followed by an fsync, once per close, so the event stream
// this package calls the source of truth survives a power cut and not only
// a killed process. The index append is synced for the same reason, and
// syncing only the derived copy would leave a listing that outlives the
// journal it was reconstructed from.
func (j *Journal) closeFileLocked() {
	if j.closed {
		return
	}
	j.closed = true
	if err := j.w.Flush(); err != nil && j.err == nil {
		j.err = err
	}
	if err := j.f.Sync(); err != nil && j.err == nil {
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
//
// The journals under runs/ are the durable copy. If the index is missing,
// empty, or does not yet name the newest journal (a process that flushed
// events then died before Close), Recent reconstructs the missing rows from
// those files and writes them back. A crash that is not at the tail, because
// a later run Closed, is filled from its journal for this listing: appending
// it would make it the newest index row and hide that later Close. A
// reconstructed row has no Args, ExitCode, or Elapsed: only Close records
// those.
func Recent(n int) ([]Summary, error) {
	if n <= 0 {
		return nil, nil
	}
	if err := recoverIndex(); err != nil {
		return nil, err
	}
	out, err := readIndex(n)
	if err != nil {
		return nil, err
	}
	out = dedupeRunIDs(out)
	want, err := listJournalsN(n)
	if err != nil || len(want) == 0 {
		// No journals: the index is the listing, as tests and a
		// hand-written index rely on.
		return out, err
	}
	lookup, err := indexLookup(want, out)
	if err != nil {
		return nil, err
	}
	// A hole behind a later Close cannot be appended: that would make it
	// the newest index row, and recoverIndex would then reconstruct the
	// already-Closed newer run and hide its Args. The listing fills those
	// rows from the journal for this call; the index stays a suffix cache.
	for _, j := range want {
		if _, ok := lookup[j.id]; ok {
			continue
		}
		s, err := summarizeFile(j.id, j.path)
		if err != nil {
			continue
		}
		lookup[j.id] = s
	}
	return summariesFor(want, lookup), nil
}

func readIndex(n int) ([]Summary, error) {
	f, err := os.Open(indexPath())
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

	for chunk := int64(recentChunk); ; {
		start := max(size-chunk, 0)
		data := make([]byte, size-start)
		nRead, err := f.ReadAt(data, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		out, enough := parseTail(data[:nRead], start > 0, n)
		if enough || start == 0 {
			return out, nil
		}
		// Doubling is how the window grows; once it cannot, the next pass
		// has to be the whole file or the loop never reaches start==0.
		if chunk > math.MaxInt64/2 {
			chunk = math.MaxInt64
			continue
		}
		chunk *= 2
	}
}

// recoverIndex restores the listing from the run journals when the index
// cannot answer. A healthy index is left alone: Close writes Args,
// ExitCode, and Elapsed that the journals do not carry, and rebuilding
// would drop them.
//
// A stale index is the tail case: one or more processes flushed then died
// before Close. Appending only the newest journal would hide an older crash
// that is still sitting on disk. The missing tail is reconstructed oldest
// first so the listing stays chronological, without rewriting the rows Close
// already wrote.
func recoverIndex() error {
	id, _, ok, err := newestJournal()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	newest, err := readIndex(1)
	if err != nil {
		return err
	}
	if len(newest) > 0 && newest[0].RunID == id {
		return nil
	}
	return withIndexLock(recoverIndexLocked)
}

func recoverIndexLocked() error {
	id, _, ok, err := newestJournal()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	newest, err := readIndex(1)
	if err != nil {
		return err
	}
	if len(newest) == 0 {
		_, err := rebuildIndex()
		return err
	}
	if newest[0].RunID == id {
		return nil
	}
	return recoverIndexTail(newest[0].RunID)
}

// indexLookup is the newest index row for each of want, newest-first so a
// Close row wins over a reconstruction. listed is the already-read tail;
// the healthy path (every wanted id is there) does not widen the read.
func indexLookup(want []namedJournal, listed []Summary) (map[string]Summary, error) {
	lookup := make(map[string]Summary, len(want))
	for _, s := range listed {
		if s.RunID != "" {
			if _, ok := lookup[s.RunID]; !ok {
				lookup[s.RunID] = s
			}
		}
	}
	need := 0
	for _, j := range want {
		if _, ok := lookup[j.id]; !ok {
			need++
		}
	}
	if need == 0 {
		return lookup, nil
	}
	// A hole sits behind a later Close, so it is older than the tail of
	// n rows. Widen until every wanted id is present or the file ends;
	// first occurrence still wins, because each wider read is newest-first.
	for limit := max(len(listed)*2, 32); need > 0; {
		rows, err := readIndex(limit)
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			if s.RunID == "" {
				continue
			}
			if _, ok := lookup[s.RunID]; !ok {
				lookup[s.RunID] = s
			}
		}
		still := 0
		for _, j := range want {
			if _, ok := lookup[j.id]; !ok {
				still++
			}
		}
		if still == 0 || len(rows) < limit {
			break
		}
		need = still
		if limit > math.MaxInt/2 {
			limit = math.MaxInt
		} else {
			limit *= 2
		}
	}
	return lookup, nil
}

func summariesFor(want []namedJournal, lookup map[string]Summary) []Summary {
	out := make([]Summary, 0, len(want))
	for _, j := range want {
		if s, ok := lookup[j.id]; ok {
			out = append(out, s)
		}
	}
	return out
}

// recoverIndexTail appends reconstructed rows for every journal newer than
// anchorID, oldest first. If the indexed run is not among the files (deleted
// journal, rearranged tree), only the true newest is appended, so a full
// walk cannot duplicate every existing row.
func recoverIndexTail(anchorID string) error {
	var missing []namedJournal
	found := false
	err := walkJournals(func(j namedJournal) bool {
		if j.id == anchorID {
			found = true
			return false
		}
		missing = append(missing, j)
		return true
	})
	if err != nil {
		return err
	}
	if !found {
		if len(missing) == 0 {
			return nil
		}
		missing = missing[:1]
	}
	indexed, err := indexLookup(missing, nil)
	if err != nil {
		return err
	}
	for _, journal := range slices.Backward(missing) {
		if _, ok := indexed[journal.id]; ok {
			continue
		}
		s, err := summarizeFile(journal.id, journal.path)
		if err != nil {
			if !found {
				return fmt.Errorf("cannot reconstruct run %s: %w", journal.id, err)
			}
			continue
		}
		if err := appendIndexLocked(s); err != nil {
			return fmt.Errorf("cannot append reconstructed run %s to the index: %w", journal.id, err)
		}
	}
	return nil
}

// dedupeRunIDs keeps the first occurrence of each run id. Recent is newest
// first, so a Close row appended after a reconstructed one wins.
func dedupeRunIDs(in []Summary) []Summary {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]Summary, 0, len(in))
	for _, s := range in {
		if s.RunID == "" {
			out = append(out, s)
			continue
		}
		if _, ok := seen[s.RunID]; ok {
			continue
		}
		seen[s.RunID] = struct{}{}
		out = append(out, s)
	}
	return out
}

type namedJournal struct {
	id, path string
}

// walkJournals visits every run journal under runs/, shards then ids, both
// newest first. Run ids embed a UTC timestamp, so a lexical max is the
// latest start. visit returning false stops the walk.
func walkJournals(visit func(namedJournal) bool) error {
	root := filepath.Join(Home(), "runs")
	days, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	slices.Reverse(days)
	for _, d := range days {
		if !d.IsDir() {
			continue
		}
		for _, j := range journalsInDir(filepath.Join(root, d.Name())) {
			if !visit(j) {
				return nil
			}
		}
	}
	return nil
}

func journalsInDir(dir string) []namedJournal {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var batch []namedJournal
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".jsonl")
		if !validRunID(id) {
			continue
		}
		batch = append(batch, namedJournal{id: id, path: filepath.Join(dir, f.Name())})
	}
	slices.Reverse(batch)
	return batch
}

// newestJournal is the latest run file under runs/.
func newestJournal() (id, path string, ok bool, err error) {
	err = walkJournals(func(j namedJournal) bool {
		id, path, ok = j.id, j.path, true
		return false
	})
	return id, path, ok, err
}

func listJournals() ([]namedJournal, error) {
	return listJournalsN(0)
}

// listJournalsN returns the newest n journals, newest first. n <= 0 means
// every journal, the walk recoverIndexTail uses.
func listJournalsN(n int) ([]namedJournal, error) {
	var out []namedJournal
	err := walkJournals(func(j namedJournal) bool {
		out = append(out, j)
		return n <= 0 || len(out) < n
	})
	return out, err
}

// rebuildIndex rewrites index.jsonl from every run journal, oldest first, so
// a later Close still appends. Journals that cannot be read are skipped.
// The caller holds the index lock.
func rebuildIndex() (int, error) {
	journals, err := listJournals()
	if err != nil {
		return 0, err
	}
	rows := make([]Summary, 0, len(journals))
	for _, journal := range slices.Backward(journals) {
		s, err := summarizeFile(journal.id, journal.path)
		if err != nil {
			continue
		}
		rows = append(rows, s)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := writeIndex(rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// writeIndex replaces index.jsonl. The caller holds the index lock.
func writeIndex(rows []Summary) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, s := range rows {
		if err := enc.Encode(s); err != nil {
			return err
		}
	}
	return writeIndexBytes(buf.Bytes())
}

// writeIndexBytes replaces index.jsonl with body, through a temp file and a
// rename, so a reader sees either the whole old index or the whole new one.
// The caller holds the index lock.
func writeIndexBytes(body []byte) error {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	gauntlethome.SweepStaleTemps(Home(), ".index.jsonl-", 24*time.Hour)
	tmp, err := os.CreateTemp(Home(), ".index.jsonl-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, indexPath()); err != nil {
		return err
	}
	// The temp file is synced before the rename, which makes its contents
	// durable; the rename itself is not, until the directory holding it is.
	return gauntlethome.SyncDir(Home())
}

// indexEvent is the subset of a journal line summarizeFile and History read.
// Extra fields are ignored, matching Events.
type indexEvent struct {
	Ev      string    `json:"ev"`
	TS      time.Time `json:"ts"`
	Dir     string    `json:"dir"`
	Review  string    `json:"review"`
	Status  string    `json:"status"`
	Loop    int       `json:"loop"`
	Ins     *int      `json:"ins"`
	Del     *int      `json:"del"`
	Tokens  int       `json:"tokens"`
	Version string    `json:"version"`
	Agents  []string  `json:"agents"`
}

// historyEvent holds only the fields History inspects, avoiding timestamp parsing
// and agent list allocations on each event line.
type historyEvent struct {
	Ev     string `json:"ev"`
	Dir    string `json:"dir"`
	Review string `json:"review"`
	Status string `json:"status"`
	Ins    int    `json:"ins"`
	Del    int    `json:"del"`
}

// lines unwraps a counted line delta. An absent field is zero here and
// distinct in LinesMeasured, so "not attributed" never sums as "no change".
func lines(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// summarizeFile rebuilds a Summary from one run's event stream. Args,
// ExitCode, and Elapsed are not on the events, so they stay absent rather
// than zero: only Close knows them.
func summarizeFile(runID, path string) (Summary, error) {
	s := Summary{RunID: runID, Path: path}
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	seenDir := map[string]bool{}
	var lastTS time.Time
	for sc.Scan() {
		var e indexEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if !e.TS.IsZero() {
			if s.Start.IsZero() {
				s.Start = e.TS
			}
			lastTS = e.TS
		}
		if e.Dir != "" && !seenDir[e.Dir] {
			seenDir[e.Dir] = true
			s.Dirs = append(s.Dirs, e.Dir)
		}
		switch e.Ev {
		case "loop_end":
			s.Loops++
		case "run_start":
			if s.Version == "" {
				s.Version = e.Version
			}
			if len(s.Agents) == 0 {
				s.Agents = append([]string(nil), e.Agents...)
			}
		case "review_end":
			s.Reviews++
			switch e.Status {
			case "", "ok":
				s.OK++
			case "fail", "timeout":
				s.Failed++
			case "skipped":
				s.Skipped++
			case "conflict":
				s.Conflicts++
			case "interrupted":
				s.Interrupted++
			default:
				// A status this build does not know. It is not a pass: the
				// switch has to account for every review it counts, or the
				// FAILED column stops explaining the run's exit code.
				s.Other++
			}
			s.Ins += lines(e.Ins)
			s.Del += lines(e.Del)
			s.Tokens += e.Tokens
		case "merge", "pull_request":
			s.Ins += lines(e.Ins)
			s.Del += lines(e.Del)
		}
		s.LinesMeasured = s.LinesMeasured || e.Ins != nil
	}
	if err := sc.Err(); err != nil {
		return Summary{}, err
	}
	s.End = lastTS
	return s, nil
}

// recentChunk is the first slice taken from the end of the index: thousands of
// runs at ~300 bytes each, so most installs never read twice.
const recentChunk = 256 << 10

// parseTail parses newline-delimited summaries out of data, newest last,
// returning up to n of them newest first. dropFirst says whether the head of
// data may cut a line in half (it does whenever the slice starts past byte 0).
//
// Lines are walked from the end of the buffer so a listing of n runs parses
// n of them, not every line the slice happens to hold. The contract matches
// splitting on '\n' and dropping a truncated head line: FuzzParseTail is the
// oracle.
func parseTail(data []byte, dropFirst bool, n int) (out []Summary, enough bool) {
	if n <= 0 {
		return nil, false // up to zero entries is no entries
	}
	out = make([]Summary, 0, n)
	end := len(data)
	for end >= 0 {
		start := 0
		if i := bytes.LastIndexByte(data[:end], '\n'); i >= 0 {
			start = i + 1
		}
		// A slice that did not begin at byte 0 may start inside a line;
		// that first record is incomplete and must not be parsed.
		if !(dropFirst && start == 0) {
			line := bytes.TrimSpace(data[start:end])
			if len(line) > 0 {
				var s Summary
				if err := json.Unmarshal(line, &s); err == nil {
					out = append(out, s)
					if len(out) == n {
						return out, true
					}
				}
			}
		}
		if start == 0 {
			break
		}
		end = start - 1
	}
	return out, false
}
