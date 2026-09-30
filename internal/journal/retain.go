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
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
)

// PruneResult is what one Prune did to the history: Moved runs left the listing for
// the quarantine and are still restorable, Evicted runs were unlinked from it
// and are gone from the machine. The two are reported apart because they are
// not the same loss, and only the second one is permanent.
type PruneResult struct {
	Moved   int
	Evicted int
}

// Prune drops the run journals and index rows older than the newest keep, and
// removes any shard directory left empty. It reports what it did to the
// history, so a run can say what the bound cost rather than leaving the
// operator to find out.
//
// A dropped journal is renamed into pruned/, not unlinked, and the quarantine
// is bounded by the same keep, so a run stays recoverable until keep newer
// runs have replaced it. Restore puts one back. Lowering the bound is the one
// way to destroy history the quarantine still held, and the runs that fell
// outside it come back in Evicted.
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
func Prune(keep int) (PruneResult, error) {
	if keep <= 0 {
		return PruneResult{}, nil
	}
	var res PruneResult
	err := withIndexLock(func() error {
		var err error
		res, err = pruneLocked(keep)
		return err
	})
	return res, err
}

// journalIdle reports whether a journal can be moved out of the listing now,
// which is the same question as whether anyone still has it open for writing.
// A writer holds a shared lock on the stream it appends to, so an exclusive
// lock that cannot be taken names a run in progress.
//
// A journal that cannot be opened is not evidence of anything except a
// failure to open it. A run another gauntlet is appending to is exactly what
// an EACCES or an exhausted descriptor table looks like here, and moving it
// out from under that writer costs a run. Only a journal that is already gone
// is idle, because then there is nothing left to disturb; every other open
// failure keeps the journal and is reported.
func journalIdle(path string) (bool, error) {
	if holdsStream(path) {
		return false, nil
	}
	f, err := openRead(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	defer f.Close()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil, nil
}

// pruneLocked does the work under the index lock, so a concurrent Close cannot
// append a row between the read that decides what to keep and the write that
// keeps it. The caller holds the lock.
func pruneLocked(keep int) (PruneResult, error) {
	all, err := listJournals()
	if err != nil {
		return PruneResult{}, err
	}
	if len(all) <= keep {
		return PruneResult{}, nil
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
	//
	// Every failure the probe meets is kept, not just the first, so the count
	// returned below is what moved and the error is everything that did not:
	// a journal kept because it could not be opened, and a journal kept
	// because the move failed, are both a run the operator has to hear about.
	var errs []error
	note := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	movable := make([]namedJournal, 0, len(stale))
	for _, j := range stale {
		idle, err := journalIdle(j.path)
		if err != nil {
			note(fmt.Errorf("cannot tell whether %s is in use: %w", j.id, err))
		}
		if idle {
			movable = append(movable, j)
			continue
		}
		keepIDs[j.id] = struct{}{}
	}

	// The index goes first. A row naming a journal that is about to leave
	// runs/ would outlive the file it points at, and the listing is read off
	// the journals under runs/: an orphan row drops out of it, so the run
	// disappears from the listing the moment its journal does. Prune is the
	// only thing that brings it back, from pruned/.
	rows, err := readAllIndex()
	if err != nil {
		return PruneResult{}, err
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
			return PruneResult{}, err
		}
	}

	touched := make(map[string]struct{}, 4)
	res := PruneResult{}
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
		res.Moved++
		touched[filepath.Dir(j.path)] = struct{}{}
	}
	// Moving the journals is what empties a shard, and os.Remove on a
	// directory succeeds only when nothing is left in it, so a shard a
	// retained run or a running one still writes into is left where it is
	// rather than named for deletion. A run filed straight into runs/ (an id
	// naming no shard) makes the root itself the emptied directory; the shared
	// helper stops there rather than syncing a parent that still holds it.
	note(removeEmptyDirs(touched, runsDir()))
	// The quarantine is bounded by the same keep, so a run stays recoverable
	// until keep newer runs have pushed it out, and the state tree does not
	// grow a second unbounded history beside the one Prune exists to bound.
	// What falls outside it is unlinked, so the count is carried out with the
	// rest: an evicted journal is the one run no rename put aside.
	evicted, err := trimQuarantine(keep)
	res.Evicted = evicted
	if err != nil {
		note(err)
	}
	if len(errs) > 0 {
		// The runs that did move are gone from runs/ whatever else failed,
		// so the count is what moved and the error is what did not.
		return res, errors.Join(errs...)
	}
	// Every movable run is out of runs/ on the way out of here: a journal that
	// was already gone is the outcome the rename wanted, and one whose rename
	// failed is an error the caller already gets. A run left behind for still
	// being written is not a removal and is not counted as one.
	return res, nil
}

// readAllIndex parses the whole index, oldest first, skipping the lines it
// cannot read. Unlike readIndex it is unbounded, because compacting the index
// is the one operation that has to see all of it: the file it reads is the one
// this package is about to bound, and reading a bounded tail would drop the
// rows in between.
func readAllIndex() ([]Summary, error) {
	// openRead, not os.Open, for the reason indexNamesRun and summarizeFile
	// use it: a planted FIFO at this path blocks every reader forever, and a
	// planted symlink is followed.
	f, err := openRead(indexPath())
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
