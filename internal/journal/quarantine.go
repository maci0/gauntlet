// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// quarantine.go: what a prune puts aside instead of unlinking. Prune is the
// only path in the package that deletes a run, and it fires unattended at the
// end of every run: one mistyped --keep-runs and a year of history is gone
// with nothing to restore from. So a pruned journal is renamed into pruned/
// and the quarantine is bounded by the same keep, which gives a pruned run
// the same lifetime in runs terms as a retained one has until the next prune
// pushes it out. Restoring is a rename back to where the run's id files it,
// the shard it names or the top of runs/, and the index row is rewritten from
// the journal right there rather than left to the next listing.

package journal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// prunedDir holds the journals a prune took out of runs/. The name says what
// the files are: pruned, still readable, one shard per day like runs/.
func prunedDir() string { return filepath.Join(Home(), "pruned") }

// quarantinePath is where the journal of runID waits after a prune.
func quarantinePath(runID string) string {
	return filepath.Join(prunedDir(), shardFromRunID(runID), runID+".jsonl")
}

// journalPath is where a run's journal lives while it is in the listing.
//
// A run id names the shard it is filed under, but Open falls back to the run's
// own start date for an id that names none, so the derived path is not where
// such a run sits. The tree is asked instead, and the derived path is what a
// run that is not written yet gets.
func journalPath(runID string) string {
	if p, ok, err := locateRun(runID); err == nil && ok {
		return p
	}
	return filepath.Join(runsDir(), shardFromRunID(runID), runID+".jsonl")
}

// quarantine moves one pruned journal out of the listing and into pruned/.
//
// A rename, not a copy: it is the same filesystem, the listing loses the run
// at once, and a crash leaves the journal in one directory or the other
// rather than half of it. Both directories are synced, since neither the
// removal nor the arrival is recorded anywhere else.
func quarantine(runID, path string) error {
	dst := quarantinePath(runID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(path, dst); err != nil {
		return err
	}
	if err := gauntlethome.SyncDir(filepath.Dir(dst)); err != nil {
		return err
	}
	return gauntlethome.SyncDir(filepath.Dir(path))
}

// trimQuarantine deletes the quarantines a keep no longer covers, oldest
// first, so the directory is bounded by the same number the listing is. The
// caller holds the index lock.
//
// Each unlink is followed by a sync of the directory that held the file, so an
// evicted run stays evicted: a removal the filesystem has not recorded comes
// back after a power cut, and a run the bound was supposed to drop reappearing
// in the quarantine is a bound that does not hold.
func trimQuarantine(keep int) error {
	held, err := listQuarantined()
	if err != nil {
		return err
	}
	if len(held) <= keep {
		return nil
	}
	touched := make(map[string]struct{}, 4)
	for _, q := range slices.Backward(held[keep:]) {
		if err := os.Remove(q.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		dir := filepath.Dir(q.path)
		touched[dir] = struct{}{}
		if err := gauntlethome.SyncDir(dir); err != nil {
			return err
		}
	}
	return removeEmptyDirs(touched)
}

// Quarantined reports the runs a prune has put aside and can still be
// recovered, newest first. It is what a user is told after a prune, and what
// a restore checks before moving a journal back.
func Quarantined() ([]string, error) {
	held, err := listQuarantined()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(held))
	for i, q := range held {
		out[i] = q.id
	}
	return out, nil
}

// Pruned reports whether runID's journal is waiting in pruned/, which is what
// makes it restorable. It is the one question a caller that found no journal
// under runs/ has left: the run is either recoverable or it never happened, and
// the two want different sentences.
func Pruned(runID string) bool {
	if !validRunID(runID) {
		return false
	}
	_, err := os.Stat(quarantinePath(runID))
	return err == nil
}

// quarantined is one journal waiting in pruned/.
type quarantined struct {
	id   string
	path string
}

// listQuarantined walks pruned/ newest first, by the same run id ordering and
// the same one-entry-per-id rule listJournals uses, so a restore names runs in
// the order they happened.
//
// pruned/ itself is walked as well as its shards: a journal whose run id names
// no shard, which a hand-named run or a rearranged tree produces, is filed at
// the top of the directory, and a quarantine that could not see it would leave
// it there past every keep, which is the unbounded history Prune exists to
// stop, and would tell a user after a prune that a run they can still restore
// is not quarantined.
func listQuarantined() ([]quarantined, error) {
	root := prunedDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []quarantined
	// pruned/ itself, then each shard under it, so a journal filed at the top
	// is read once rather than once per entry.
	top, err := quarantinedInDir(root)
	if err != nil {
		return nil, err
	}
	out = top
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		batch, err := quarantinedInDir(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	// os.ReadDir is sorted by name and the sort below is stable, so copies of
	// one id that the preference cannot separate keep the order they were read
	// in and the choice is the same on every machine.
	slices.SortStableFunc(out, func(a, b quarantined) int {
		if c := runIDOrder(b.id, a.id); c != 0 {
			return c
		}
		return quarantinePreference(a) - quarantinePreference(b)
	})
	// An id is held once however many files carry it, the rule allJournals
	// applies to runs/ and for the same reason: the keep window trims by
	// position, so a second copy spends a slot and can push a real quarantined
	// run out, and a user is told about a run they can restore once.
	deduped := out[:0]
	for i, q := range out {
		if i > 0 && q.id == out[i-1].id {
			continue
		}
		deduped = append(deduped, q)
	}
	return deduped, nil
}

// quarantinePreference ranks the copies of one run id: the one in the shard
// its id names, which is where quarantine and Restore file a run, ahead of a
// stray filed beside it. The twin of journalPreference.
func quarantinePreference(q quarantined) int {
	if shard := shardFromRunID(q.id); shard != "" &&
		filepath.Base(filepath.Dir(q.path)) == shard {
		return 0
	}
	return 1
}

// quarantinedInDir returns the quarantined journals one directory holds.
func quarantinedInDir(dir string) ([]quarantined, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []quarantined
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		// The same validRunID the runs/ walk applies, and for the same
		// reason: trimQuarantine unlinks whatever falls outside the keep
		// window, so a name this package would not open must not count
		// toward that window either. An unvalidated stem here would take a
		// keep slot and push a real quarantined run out.
		if !validRunID(id) {
			continue
		}
		out = append(out, quarantined{id: id, path: filepath.Join(dir, name)})
	}
	return out, nil
}

// The two ways a restore has nothing to do. Both are an argument the caller
// got wrong rather than a failure of the machine: a restore that silently
// did nothing is how a lost run stays lost.
var (
	ErrNotPruned     = errors.New("run is not in the quarantine")
	ErrAlreadyListed = errors.New("run is already in the listing")
)

// Restore moves a quarantined journal back under runs/, where the next
// listing rebuilds its index row from the journal itself. A run that is not
// quarantined, or is already in the listing, is an error.
func Restore(runID string) error {
	if !validRunID(runID) {
		return fmt.Errorf("%w: %q", ErrInvalidRunID, runID)
	}
	return withIndexLock(func() error {
		// The listing is checked first: a run already in it is the more
		// useful thing to say than a miss in the quarantine, and the two
		// orderings disagree exactly when a run was restored twice. The
		// lookup is the same one Open follows rather than a stat of the
		// derived path, so a run whose journal sits in a shard its id does
		// not name is still found: restoring it would put a second file
		// under the same run id, and the listing would show the run twice.
		if _, listed, err := locateRun(runID); err != nil {
			return err
		} else if listed {
			return fmt.Errorf("%w: %s", ErrAlreadyListed, runID)
		}
		src := quarantinePath(runID)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s under %s", ErrNotPruned, runID, prunedDir())
			}
			return err
		}
		dst := journalPath(runID)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		if err := gauntlethome.SyncDir(filepath.Dir(dst)); err != nil {
			return err
		}
		if err := gauntlethome.SyncDir(filepath.Dir(src)); err != nil {
			return err
		}
		// The move is what empties the shard it came out of, so the directory
		// goes with it, the way the prune's own move does. Left behind, one
		// empty directory per restore is the unbounded growth the quarantine
		// bound exists to stop, and no later trim can remove it: trimQuarantine
		// only unlinks files and the shard no longer holds any.
		if err := removeEmptyDirs(map[string]struct{}{filepath.Dir(src): {}}); err != nil {
			return err
		}
		// A row left over from before the prune would be reconstructed from
		// the journal on the next listing anyway, but writing it here makes
		// the restore visible to a reader that has the index cached.
		s, err := summarizeFile(runID, dst)
		if err != nil {
			return fmt.Errorf("cannot read the restored journal %s: %w", runID, err)
		}
		return appendIndexLocked(s)
	})
}

// removeEmptyDirs deletes the directories a set of removals emptied, and
// syncs the parent of each one that goes, since a directory stays until its
// parent records the removal.
func removeEmptyDirs(touched map[string]struct{}) error {
	root := prunedDir()
	emptied, removedRoot := false, false
	// A sorted walk, not a map range: several shards can fail to remove at
	// once, and the error the caller sees would otherwise be whichever one the
	// map happened to reach first, which differs between two runs of the same
	// prune over the same tree.
	dirs := make([]string, 0, len(touched))
	for dir := range touched {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		switch err := os.Remove(dir); {
		case err == nil:
			emptied = true
			removedRoot = removedRoot || dir == root
		case errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		default:
			return err
		}
	}
	if !emptied || removedRoot {
		// pruned/ itself is the tree the bound applies to, so there is no
		// parent here to sync its removal, and quarantine recreates it.
		return nil
	}
	return gauntlethome.SyncDir(root)
}
