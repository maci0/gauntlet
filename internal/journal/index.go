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

// runsDir holds the dated shards under which every run journal is filed.
func runsDir() string { return filepath.Join(Home(), "runs") }

func indexLockPath() string { return filepath.Join(Home(), ".index.lock") }

// indexLockWait bounds how long a mutation waits for the cross-process index
// lock. The holder walks the whole journal tree on a rebuild or a prune, so on
// a long history or a network home the wait is not instant. It is bounded
// anyway: an unbounded LOCK_EX parks `gauntlet runs`, `gauntlet show`, and
// the exit-time Prune behind a peer that may never release it, and a wait that
// ends is reportable where a hang is not.
const indexLockWait = 30 * time.Second

// indexLockPoll is how often a blocked acquisition retries the lock.
const indexLockPoll = 50 * time.Millisecond

// lockIndex takes the cross-process index lock, giving up after wait.
// LOCK_EX alone would block forever, so LOCK_NB is retried until the deadline
// and the failure names the holder's lock file.
//
// now and sleep are the wait loop's clock and pause, nil meaning time.Now and
// time.Sleep. They are the two reads that decide when a contended lock gives
// up, so a caller that owns a clock supplies both and the bound is reached on
// that clock instead of on however long the retries happened to take.
func lockIndex(fd int, wait time.Duration, now func() time.Time, sleep func(time.Duration)) error {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	deadline := now().Add(wait)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if now().After(deadline) {
			return fmt.Errorf("index lock %s is held by another gauntlet: %w",
				indexLockPath(), err)
		}
		sleep(indexLockPoll)
	}
}

// lockWriter marks an open journal as still being written, for as long as the
// handle lives: the kernel drops the lock when the file closes, so nothing has
// to release it.
//
// It is a shared lock, because two handles on one stream are two writers of the
// same run, not two runs: a hot reload's successor appends to the journal its
// predecessor closed. Prune asks the other half of the question with
// journalIdle, which needs an exclusive lock, so one writer never blocks
// another and a prune still sees every one of them.
//
// A filesystem that does not implement flock takes no lock. Prune then moves
// whatever the keep window names, which is what it did before the lock existed,
// so the journal is never lost to a lock this kernel cannot hold.
func lockWriter(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
}

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
	if err := lockIndex(fd, indexLockWait, nil, nil); err != nil {
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
	drop, err := indexNamesRun(s.RunID)
	if err != nil {
		return err
	}
	if drop {
		return rewriteIndexDropping(s.RunID, line)
	}
	f, created, err := openIndexForAppend()
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
	if err := f.Sync(); err != nil {
		return err
	}
	if !created {
		return nil
	}
	// The row is durable once the file is synced, but a file this call
	// created has no directory entry until the state root records it: a power
	// cut between the two loses the row of the install's first run, which the
	// caller is told was written. The same requirement Open has for a new
	// journal, and the one commitIndex meets for a rename.
	return gauntlethome.SyncDir(Home())
}

// openIndexForAppend opens index.jsonl for appending and reports whether this
// call is the one that created it, which is what decides whether the append has
// a directory entry left to record.
//
// O_EXCL proves the create rather than a stat guessing at it: the two writers
// this file has are serialized by the index lock, and an install whose first
// run creates the index and loses power is exactly the case the flag is here
// for. An existing file takes the plain append path, which is the one this
// replaces.
func openIndexForAppend() (*os.File, bool, error) {
	f, err := os.OpenFile(indexPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err == nil {
		return f, true, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, false, err
	}
	f, err = openNoFollow(indexPath(), os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return nil, false, err
	}
	return f, false, nil
}

// indexNamesRun reports whether the index already holds a row for runID. A
// missing index holds none, not an error: the first run of an install has no
// index yet.
//
// The index is append-only and lives for the life of the install, so this
// walks it a line at a time and stops at the first row that matches: nothing
// the size of the file is held, and only a line that already carries the run id
// is decoded at all.
func indexNamesRun(runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	f, err := os.Open(indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	needle := runIDNeedle(runID)
	found := false
	err = indexLines(f, func(line []byte) bool {
		if !namesRun(line, needle, runID) {
			return true
		}
		found = true
		return false
	})
	return found, err
}

// runIDNeedle is how a marshalled row spells the run id, used to skip rows
// that cannot name this run before they are decoded. A generated id passes
// validRunID, and that charset is written verbatim, so the needle is exact.
// An id outside it gets no needle: json would escape the raw line and the
// needle would then be a substring the line does not have.
func runIDNeedle(runID string) []byte {
	if !validRunID(runID) {
		return nil
	}
	return []byte(`"run_id":"` + runID + `"`)
}

// rewriteIndexDropping replaces the index with every row but the ones naming
// runID, and puts newLine at the end. The rows are streamed to the temporary
// file rather than gathered first, so a rewrite costs the size of the index
// once instead of holding it, and a copy of it, in memory. The caller holds
// the index lock.
func rewriteIndexDropping(runID string, newLine []byte) error {
	needle := runIDNeedle(runID)
	return commitIndex(func(w io.Writer) error {
		f, err := os.Open(indexPath())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			defer f.Close()
			var writeErr error
			walkErr := indexLines(f, func(line []byte) bool {
				if namesRun(line, needle, runID) {
					return true // drop this row and keep walking the rest
				}
				_, writeErr = w.Write(append(line, '\n'))
				return writeErr == nil
			})
			if writeErr != nil {
				return writeErr
			}
			if walkErr != nil {
				return walkErr
			}
		}
		_, err = w.Write(newLine)
		return err
	})
}

// namesRun reports whether one raw index line names runID. A line that does
// not decode, or carries no id, names nothing: the index is a convenience
// log, and a mangled row is not this run's to delete. The needle is a
// prefilter, so only a line that could hold the id is decoded.
func namesRun(line, needle []byte, runID string) bool {
	if needle != nil && !bytes.Contains(line, needle) {
		return false
	}
	var row struct {
		RunID string `json:"run_id"`
	}
	return json.Unmarshal(line, &row) == nil && row.RunID == runID
}

// indexBufBytes sizes the buffers the index is streamed through: a summary
// row is a few hundred bytes, so a line this size is already a corrupt file.
const indexBufBytes = 64 << 10

// indexLines walks a file one line at a time, handing each non-empty line to
// visit with its terminator and trailing whitespace trimmed. visit returning
// false stops the walk. The slice is reused between lines, so a visitor that
// keeps one has to copy it.
func indexLines(f *os.File, visit func([]byte) bool) error {
	size := indexBufBytes
	// Most installs hold a handful of runs, so a reader the size of the whole
	// index beats one that is always the ceiling.
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 && fi.Size() < int64(size) {
		size = int(fi.Size())
	}
	r := bufio.NewReaderSize(f, size)
	var long []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line := chunk
		if err == bufio.ErrBufferFull {
			// A row longer than the buffer: gather it, since a visitor may
			// need the whole line and the copy outlives the read.
			long = append(long[:0], chunk...)
			for err == bufio.ErrBufferFull {
				chunk, err = r.ReadSlice('\n')
				long = append(long, chunk...)
			}
			line = long
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		// ReadSlice hands back the terminator, so the line is cut there and
		// then trimmed of the carriage return and blanks a writer may have
		// left, which is what dropping a line and writing it back did.
		if i := bytes.LastIndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if line = bytes.TrimRight(line, "\r \t"); len(line) > 0 && !visit(line) {
			return nil
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
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
	id, indexed, err := indexState()
	if err != nil || id == "" {
		return err
	}
	if indexed == id {
		return nil
	}
	return withIndexLock(recoverIndexLocked)
}

func recoverIndexLocked() error {
	id, indexed, err := indexState()
	if err != nil || id == "" {
		return err
	}
	if indexed == "" {
		_, err := rebuildIndex()
		return err
	}
	if indexed == id {
		return nil
	}
	return recoverIndexTail(indexed)
}

// indexState pairs the newest journal on disk with the run the index's newest
// row names. Either may be "": no journal under runs/, or no row the index can
// answer with. Together they say which of the three recoveries applies, and
// both callers read the pair under the same conditions, so a change to what
// "stale" means cannot reach one path and miss the other.
func indexState() (journalID, indexedID string, err error) {
	journalID, _, ok, err := newestJournal()
	if err != nil || !ok {
		return "", "", err
	}
	newest, err := readIndex(1)
	if err != nil || len(newest) == 0 {
		return journalID, "", err
	}
	return journalID, newest[0].RunID, nil
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

// walkJournals visits every run journal under runs/, newest first by run id.
// visit returning false stops the walk.
func walkJournals(visit func(namedJournal) bool) error {
	all, err := allJournals()
	if err != nil {
		return err
	}
	for _, j := range all {
		if !visit(j) {
			return nil
		}
	}
	return nil
}

// allJournals is every run journal under runs/, newest first, one entry per
// run id.
//
// The order is by run id across the whole tree rather than by shard directory.
// A run id carries the UTC start its shard is named for, so a journal filed
// where the id does not say (a hand-named run, a tree rearranged by hand, a
// backup restored beside itself) would otherwise be listed at the position of
// the directory it happens to sit in, and `gauntlet runs` would print runs out
// of order with no way to say which is the newer one.
//
// An id is listed once however many files carry it. The run id is the key the
// index dedupes on and the file is opened by, so a second copy is one run: the
// listing, the keep window, and Inspect all count it once instead of spending
// a slot on a duplicate and printing the same run twice.
func allJournals() ([]namedJournal, error) {
	root := runsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []namedJournal
	// runs/ itself, then each shard under it: a journal filed at the top of
	// the tree, which a hand-named run id produces, is read once here rather
	// than once per entry.
	top, err := journalsInDir(root)
	if err != nil {
		return nil, err
	}
	out = top
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		batch, err := journalsInDir(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	// os.ReadDir is sorted by name and the sort below is stable, so copies of
	// one id that the preference cannot separate keep the order they were
	// read in and the choice is the same on every machine.
	slices.SortStableFunc(out, func(a, b namedJournal) int {
		if c := runIDOrder(b.id, a.id); c != 0 {
			return c
		}
		return journalPreference(a) - journalPreference(b)
	})
	deduped := out[:0]
	for i, j := range out {
		if i > 0 && j.id == out[i-1].id {
			continue
		}
		deduped = append(deduped, j)
	}
	return deduped, nil
}

// journalPreference ranks the copies of one run id: the one in the shard its
// id names, which is where Open and Restore file a run, ahead of a stray filed
// beside it.
func journalPreference(j namedJournal) int {
	if shard := shardFromRunID(j.id); shard != "" &&
		filepath.Base(filepath.Dir(j.path)) == shard {
		return 0
	}
	return 1
}

// journalsInDir returns one shard's journals, newest first. The order comes
// from runIDOrder rather than from the directory listing's byte order: two
// runs minted in the same second carry pids of different hex widths, and
// ReadDir files the wider one below the narrower one. A read error is the
// caller's to report: a shard that cannot be listed is a finding, and a walk
// that swallowed it would report a tree that is merely short.
func journalsInDir(dir string) ([]namedJournal, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var batch []namedJournal
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".jsonl")
		if !validRunID(id) {
			continue
		}
		// A directory carrying a run id and a .jsonl suffix, which is what a
		// repository volume holding a stray copy of one looks like, is not a
		// journal. The question is settled by the stat rather than by the
		// directory entry type: a readdir reporting no type (some network and
		// FUSE mounts) hands back ModeIrregular, which reads as "not a
		// directory", and the listing would then hand every reader a path it
		// cannot open as a file.
		if fi, err := f.Info(); err != nil || fi.IsDir() {
			continue
		}
		batch = append(batch, namedJournal{id: id, path: filepath.Join(dir, f.Name())})
	}
	slices.SortFunc(batch, func(a, b namedJournal) int {
		return runIDOrder(b.id, a.id)
	})
	return batch, nil
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
	all, err := allJournals()
	if err != nil || n <= 0 || len(all) <= n {
		return all, err
	}
	return all[:n], nil
}

// rebuildIndex rewrites index.jsonl from every run journal, oldest first, so
// a later Close still appends. A journal that cannot be read keeps the row the
// index already has, because Args, ExitCode, and Elapsed are on the row alone:
// dropping it would spend the only copy of what the journal does not carry, and
// nothing reconstructs them afterwards. The caller holds the index lock.
func rebuildIndex() (int, error) {
	journals, err := listJournals()
	if err != nil {
		return 0, err
	}
	held, err := readAllIndex()
	if err != nil {
		return 0, err
	}
	byID := make(map[string]Summary, len(held))
	for _, s := range held {
		if s.RunID != "" {
			byID[s.RunID] = s
		}
	}
	rows := make([]Summary, 0, len(journals))
	for _, journal := range slices.Backward(journals) {
		s, err := summarizeFile(journal.id, journal.path)
		if err != nil {
			if prev, ok := byID[journal.id]; ok {
				prev.Path = journal.path
				rows = append(rows, prev)
			}
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
	return commitIndex(func(w io.Writer) error {
		enc := json.NewEncoder(w)
		for _, s := range rows {
			if err := enc.Encode(s); err != nil {
				return err
			}
		}
		return nil
	})
}

// commitIndex replaces index.jsonl with what write emits, through a temp file
// and a rename, so a reader sees either the whole old index or the whole new
// one. The rows are streamed rather than gathered, so a rewrite holds one
// buffer's worth of index instead of all of it. The caller holds the index
// lock.
func commitIndex(write func(io.Writer) error) error {
	if err := os.MkdirAll(Home(), 0o700); err != nil {
		return err
	}
	tmp, err := gauntlethome.NewTempFile(Home(), ".index.jsonl-", nil)
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
	w := bufio.NewWriterSize(tmp, indexBufBytes)
	if err := write(w); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
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
	Branch  string    `json:"branch"`
	Status  string    `json:"status"`
	Loop    int       `json:"loop"`
	Ins     *int      `json:"ins"`
	Del     *int      `json:"del"`
	Tokens  int       `json:"tokens"`
	Version string    `json:"version"`
	Agents  []string  `json:"agents"`
}

// layerKey names one published layer by what its merge or pull_request event
// carries: the directory, the loop, the review, and the branch the work landed
// on. A branch name is unique to a layer within a run, and a layer's
// publication can reach the journal twice (a hot-reload successor re-recording
// a layer its predecessor already published), so a repeated key is the same
// work counted a second time.
type layerKey struct {
	dir, review, branch string
	loop                int
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
//
// The line totals are the sum over layers, not over publications: a merge or
// pull_request event repeated in one stream counts once, the way History
// counts one run per review.
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
	counted := map[layerKey]bool{}
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
			// A layer's counts are added once, keyed by the branch its work
			// landed on. A publication that reaches the journal twice (a
			// hot-reload successor re-recording a layer its predecessor
			// already published) repeats every field the key is built from,
			// and summing it twice would report one diff as two and leave the
			// summary reconciling against nothing. An event carrying no counts
			// claims none, so it never keeps a counted one out.
			key := layerKey{dir: e.Dir, review: e.Review, branch: e.Branch, loop: e.Loop}
			if counted[key] {
				break
			}
			if e.Ins != nil {
				counted[key] = true
			}
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

// maxTailHint bounds the slice parseTail reserves up front. n is a caller's
// listing limit, and --limit has no upper bound, so reserving n Summary values
// (a few hundred bytes each) made `runs --limit 100000000` reserve gigabytes
// for an index holding a handful of rows. The cap is a hint, not a limit:
// append grows the slice to whatever the index really holds.
const maxTailHint = 1024

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
	out = make([]Summary, 0, min(n, maxTailHint))
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
