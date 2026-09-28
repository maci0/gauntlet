// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// quarantine.go: what a prune puts aside instead of unlinking. Prune is the
// only path in the package that deletes a run, and it fires unattended at the
// end of every run: one mistyped --keep-runs and a year of history is gone
// with nothing to restore from. So a pruned journal is renamed into pruned/
// and the quarantine is bounded by the same keep, which gives a pruned run
// the same lifetime in runs terms as a retained one has until the next prune
// pushes it out. Restoring is a rename back into runs/<shard>/, after which
// the next listing rebuilds the index row from the journal.

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
func journalPath(runID string) string {
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
		touched[filepath.Dir(q.path)] = struct{}{}
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

// quarantined is one journal waiting in pruned/.
type quarantined struct {
	id   string
	path string
}

// listQuarantined walks pruned/ newest first, by the same run id ordering
// listJournals uses, so a restore names runs in the order they happened.
func listQuarantined() ([]quarantined, error) {
	root := prunedDir()
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	shards, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []quarantined
	for _, sh := range shards {
		if !sh.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, sh.Name()))
		if err != nil {
			return nil, err
		}
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
			// reason: trimQuarantine unlinks whatever falls outside the
			// keep window, so a name this package would not open must not
			// count toward that window either. An unvalidated stem here
			// would take a keep slot and push a real quarantined run out.
			if !validRunID(id) {
				continue
			}
			out = append(out, quarantined{
				id:   id,
				path: filepath.Join(root, sh.Name(), name),
			})
		}
	}
	slices.SortFunc(out, func(a, b quarantined) int {
		return runIDOrder(b.id, a.id)
	})
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
		// orderings disagree exactly when a run was restored twice.
		dst := journalPath(runID)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("%w: %s", ErrAlreadyListed, runID)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		src := quarantinePath(runID)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s under %s", ErrNotPruned, runID, prunedDir())
			}
			return err
		}
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
	emptied := false
	for dir := range touched {
		switch err := os.Remove(dir); {
		case err == nil:
			emptied = true
		case errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, syscall.EEXIST):
		default:
			return err
		}
	}
	if !emptied {
		return nil
	}
	return gauntlethome.SyncDir(prunedDir())
}
