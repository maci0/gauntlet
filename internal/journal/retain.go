// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// retain.go: the bound on the run history. A run files a journal and an index
// row, and nothing else in the package ever deletes either, so an install that
// reviews daily keeps a file per run for the life of the machine. Prune is the
// counterpart: it drops the runs past the newest keep, from both copies, so the
// two stay in agreement and the listing never names a run whose journal is gone.

package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// Prune drops the run journals and index rows older than the newest keep, and
// removes any shard directory left empty. It returns how many runs it dropped.
//
// A dropped journal is renamed into pruned/, not unlinked, and the quarantine
// is bounded by the same keep, so a run stays recoverable until keep newer
// runs have replaced it. Restore puts one back.
//
// A keep of zero or less keeps everything and does nothing, so a caller that
// has no configured bound never deletes history by accident.
//
// The walk orders by run id, which embeds a UTC start time, so a run in
// progress usually sorts above every run it can evict. A journal that is still
// open is never evicted whatever its id says, and keeps its index row with it:
// the run holding it open is another gauntlet on the same state tree that
// began earlier and is still working. Moving that journal would file a live
// event stream under pruned/ and leave the row its Close appends naming a file
// that is no longer in the listing, which is a finished run that never lists
// and a Status that reports a disagreement. A run whose clock sits behind the
// run that started after it is the one case the ordering mis-orders, and that
// is the ordering walkJournals already trusts to mean "latest".
func Prune(keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	var removed int
	err := withIndexLock(func() error {
		n, err := pruneLocked(keep)
		removed = n
		return err
	})
	return removed, err
}

// journalIdle reports whether a journal can be moved out of the listing now,
// which is the same question as whether anyone still has it open for writing.
// A writer holds a shared lock on the stream it appends to, so an exclusive
// lock that cannot be taken names a run in progress.
//
// A journal that cannot be opened at all is reported idle: a file the prune
// cannot read is one the rename is about to move, and a read failure here is
// not a reason to leave a run the keep window named.
func journalIdle(path string) bool {
	if holdsStream(path) {
		return false
	}
	f, err := openRead(path)
	if err != nil {
		return true
	}
	defer f.Close()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// pruneLocked does the work under the index lock, so a concurrent Close cannot
// append a row between the read that decides what to keep and the write that
// keeps it. The caller holds the lock.
func pruneLocked(keep int) (int, error) {
	all, err := listJournals()
	if err != nil {
		return 0, err
	}
	if len(all) <= keep {
		return 0, nil
	}
	// listJournals is newest first, so everything past the window is the tail.
	stale := all[keep:]
	keepIDs := make(map[string]struct{}, keep)
	for _, j := range all[:keep] {
		keepIDs[j.id] = struct{}{}
	}
	// A run another gauntlet still has open is not stale, whatever the keep
	// window says, and it is decided here rather than at the move: the index
	// below is written from keepIDs, so a run that keeps its journal has to
	// keep its row in the same pass.
	movable := make([]namedJournal, 0, len(stale))
	for _, j := range stale {
		if journalIdle(j.path) {
			movable = append(movable, j)
			continue
		}
		keepIDs[j.id] = struct{}{}
	}

	// The index goes first. A journal removed while its row survived would
	// leave a listing entry pointing at a file that is gone, and Recent would
	// print that run as a run. The reverse, a row whose journal is gone, is
	// what the tail already tolerates: it reconstructs what it can and skips
	// a run it cannot read.
	rows, err := readAllIndex()
	if err != nil {
		return 0, err
	}
	kept := rows[:0:0]
	for _, s := range rows {
		// A row with no run id cannot be matched to a journal, and the tail
		// passes it through. Keep it: this is a bound, not a repair.
		if s.RunID == "" {
			kept = append(kept, s)
			continue
		}
		if _, ok := keepIDs[s.RunID]; ok {
			kept = append(kept, s)
		}
	}
	if len(kept) != len(rows) {
		if err := writeIndex(kept); err != nil {
			return 0, err
		}
	}

	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	touched := make(map[string]struct{}, 4)
	for _, j := range movable {
		// The journal is renamed into pruned/ rather than unlinked, so a
		// keep the user got wrong is a move and not a loss. A journal that
		// is already gone is the outcome the rename wanted, so it is not
		// an error; anything else leaves the run where it was and is
		// reported with the rest.
		if err := quarantine(j.id, j.path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				note(err)
			}
			continue
		}
		touched[filepath.Dir(j.path)] = struct{}{}
	}
	// Moving the journals is what empties a shard, and os.Remove on a
	// directory succeeds only when nothing is left in it, so a shard a
	// retained run or a running one still writes into is left where it is
	// rather than named for deletion.
	emptied := false
	for _, dir := range sortedKeys(touched) {
		switch err := os.Remove(dir); {
		case err == nil:
			emptied = true
		case errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		default:
			note(err)
		}
	}
	// A removed shard is only gone for good once its parent records the
	// removal, the same requirement as the one Open has for a new file.
	if emptied {
		note(gauntlethome.SyncDir(runsDir()))
	}
	// The quarantine is bounded by the same keep, so a run stays recoverable
	// until keep newer runs have pushed it out, and the state tree does not
	// grow a second unbounded history beside the one Prune exists to bound.
	if err := trimQuarantine(keep); err != nil {
		note(err)
	}
	if firstErr != nil {
		return 0, firstErr
	}
	// Every movable run is out of runs/ on the way out of here: a journal that
	// was already gone is the outcome the rename wanted, and one whose rename
	// failed is an error the caller already gets. A run left behind for still
	// being written is not a removal and is not counted as one.
	return len(movable), nil
}

// readAllIndex parses the whole index, oldest first, skipping the lines it
// cannot read. Unlike readIndex it is unbounded, because compacting the index
// is the one operation that has to see all of it: the file it reads is the one
// this package is about to bound, and reading a bounded tail would drop the
// rows in between.
func readAllIndex() ([]Summary, error) {
	f, err := os.Open(indexPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var rows []Summary
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), indexLineMax)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var s Summary
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			continue
		}
		rows = append(rows, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// indexLineMax bounds one index line. A row carries the argv that produced the
// run, so the line grows with the command, but not without limit: a
// multi-megabyte row is a corrupt file, and Scanner must stop there rather than
// allocate whatever the file asks for.
const indexLineMax = 4 << 20
