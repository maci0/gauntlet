// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is HEAD, the index, and the worktree (including untracked files,
// excluding ignored) at one moment. Restore puts the tree back to this state,
// which is what makes an in-place retry start from the same files the first
// attempt saw without discarding the user's own uncommitted work.
type Snapshot struct {
	head      string
	indexTree string
	fullTree  string
}

// Valid reports whether Restore can apply s.
func (s Snapshot) Valid() bool {
	return isHex(s.fullTree) && isHex(s.indexTree) && isHex(s.head)
}

// Snapshot records the current checkout so Restore can put it back. The
// worktree walk uses a private index, so the real index and files are not
// touched; the tree objects sit in the object store until gc.
func (r *Repo) Snapshot(ctx context.Context) (Snapshot, error) {
	if r == nil || !Available() {
		return Snapshot{}, errGitUnavailable
	}
	head, err := r.Tip(ctx, "HEAD")
	if err != nil {
		return Snapshot{}, fmt.Errorf("cannot read HEAD: %w", err)
	}
	if !isHex(head) {
		// Tip returned without an error but not a commit, so there is no err
		// to wrap: naming what came back is the only thing a reader can act on.
		return Snapshot{}, fmt.Errorf("cannot read HEAD: %q is not a commit", head)
	}
	indexOut, err := r.run(ctx, gitQuick, "write-tree")
	if err != nil {
		return Snapshot{}, fmt.Errorf("cannot snapshot the index: %w", err)
	}
	indexTree := strings.TrimSpace(string(indexOut))
	if !isHex(indexTree) {
		return Snapshot{}, errors.New("git write-tree returned no tree")
	}
	fullTree, err := r.worktreeTree(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{head: head, indexTree: indexTree, fullTree: fullTree}, nil
}

// Restore resets HEAD, the index, and the worktree to s. A second call with
// the same snapshot is a no-op: the retry path restores before every attempt.
//
// Files the attempt created that were not in the snapshot are removed
// (`git clean -fd`); ignored files stay, matching ResetToBase.
//
// The snapshot's objects are verified before the first destructive step, not
// after: a snapshot's trees are unreferenced, so a gc that pruned them leaves
// nothing to restore from, and `reset --hard` throws away the working state
// those objects are the only copy of. Reporting it first leaves the tree as
// the attempt left it, which is the state the caller has to fall back on.
func (r *Repo) Restore(ctx context.Context, s Snapshot) error {
	if r == nil || !Available() {
		return errGitUnavailable
	}
	if !s.Valid() {
		return errors.New("invalid snapshot")
	}
	if err := r.verifySnapshot(ctx, s); err != nil {
		return err
	}
	if _, err := r.run(ctx, gitNormal, "reset", "--hard", s.head, "--"); err != nil {
		return fmt.Errorf("git reset --hard: %w", err)
	}
	if _, err := r.run(ctx, gitNormal, "read-tree", "-u", "--reset", s.fullTree); err != nil {
		return fmt.Errorf("git read-tree: %w", err)
	}
	if _, err := r.run(ctx, gitNormal, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean -fd: %w", err)
	}
	if _, err := r.run(ctx, gitNormal, "read-tree", s.indexTree); err != nil {
		return fmt.Errorf("git read-tree (index): %w", err)
	}
	r.Invalidate()
	return nil
}

// verifySnapshot reports whether the object store still holds everything the
// snapshot names. A snapshot records trees no ref points at, so they survive
// only until a prune drops what is unreachable: gc.expire now, a repack, or a
// repository whose own gc settings are aggressive. The caller holds no lock
// and the check is three quick lookups, which is the price of not finding out
// halfway through a restore.
func (r *Repo) verifySnapshot(ctx context.Context, s Snapshot) error {
	for _, o := range []struct{ sha, kind string }{
		{s.head, "commit"},
		{s.indexTree, "tree"},
		{s.fullTree, "tree"},
	} {
		ok, err := r.hasObject(ctx, o.sha, o.kind)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("snapshot %s %s is no longer in the object store", o.kind, o.sha)
		}
	}
	return nil
}

// hasObject reports whether the object store holds sha as a kind, with the
// three-way answer HasCommit gives: exit 1 is missing, and a git failure of
// its own is returned rather than read as a missing object, since a store
// that cannot be searched is not evidence the object is gone.
func (r *Repo) hasObject(ctx context.Context, sha, kind string) (bool, error) {
	if !isHex(sha) {
		return false, nil // never a revision expression or an option
	}
	if _, err := r.run(ctx, gitQuick, "rev-parse", "--verify", "--quiet", sha+"^{"+kind+"}"); err == nil {
		return true, nil
	} else if exitsWith(err, 1) {
		return false, nil
	} else {
		return false, fmt.Errorf("cannot read snapshot %s %s: %w", kind, sha, err)
	}
}

// worktreeTree writes a tree of the current worktree, including untracked
// files and excluding ignored ones, without changing the real index.
func (r *Repo) worktreeTree(ctx context.Context) (string, error) {
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return "", err
	}
	// The reviewed repository picks gitDir: `.git` can be a symlink or a
	// gitfile whose path lands elsewhere, and `.git/info` can be planted
	// outright. Every write below lands in it, so it is proven to be a real
	// directory first, the same rule ExcludeOwnArtifacts applies to the
	// exclude file.
	if !realDir(gitDir) {
		return "", fmt.Errorf("git directory %s is not a real directory", gitDir)
	}
	sweepStaleSnapshots(gitDir, r.now())
	tmp, err := os.CreateTemp(gitDir, "gauntlet-snap-")
	if err != nil {
		return "", fmt.Errorf("cannot create a snapshot index: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	// The real index is copied through the descriptor CreateTemp already
	// holds, and read through O_NOFOLLOW. Reopening tmpName by name after a
	// close would throw the O_EXCL away and follow a symlink swapped into
	// the name, and a planted `.git/index` link would be read out of tree.
	if src, _, err := openRegular(filepath.Join(gitDir, "index")); err == nil {
		_, err = io.Copy(tmp, src)
		if cerr := src.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return "", err
		}
		if err := tmp.Close(); err != nil {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	} else {
		if err := tmp.Close(); err != nil {
			return "", err
		}
		// An empty file is not a valid index; git add will create one.
		if err := os.Remove(tmpName); err != nil {
			return "", err
		}
	}
	if _, err := r.runIndex(ctx, tmpName, gitNormal, "add", "-A"); err != nil {
		return "", fmt.Errorf("cannot snapshot the worktree: %w", err)
	}
	out, err := r.runIndex(ctx, tmpName, gitQuick, "write-tree")
	if err != nil {
		return "", fmt.Errorf("cannot snapshot the worktree: %w", err)
	}
	tree := strings.TrimSpace(string(out))
	if !isHex(tree) {
		return "", errors.New("git write-tree returned no tree")
	}
	return tree, nil
}

func (r *Repo) gitDir(ctx context.Context) (string, error) {
	out, err := r.run(ctx, gitQuick, "rev-parse", "--git-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("git rev-parse --git-dir returned nothing")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.Dir, dir)
	}
	return dir, nil
}

func (r *Repo) runIndex(ctx context.Context, index string, timeout time.Duration, args ...string) ([]byte, error) {
	return r.execGitEnv(ctx, nil, []string{"GIT_INDEX_FILE=" + index}, timeout, r.argv(args)...)
}

// staleSnapshotAge is how old a leftover snapshot index must be before the
// next snapshot sweeps it. The window that creates one closes when the process
// does, so anything older belongs to a write that never finished; the age
// keeps a concurrent run's in-flight index safe, the same rule
// gauntlethome.StaleTempAge follows.
const staleSnapshotAge = time.Hour

// sweepStaleSnapshots removes leftover gauntlet-snap-* index files from gitDir
// that were left behind by a killed or crashed process. now is the repo clock:
// the sweep runs only where a snapshot is taken, which is the in-place path,
// so a run under a pinned clock leaves the same git directory a live run does.
func sweepStaleSnapshots(gitDir string, now time.Time) {
	entries, err := os.ReadDir(gitDir)
	if err != nil {
		return
	}
	cutoff := now.Add(-staleSnapshotAge)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "gauntlet-snap-") {
			continue
		}
		// The stat, not the directory entry type, decides what a leftover is.
		// A readdir reporting no type hands back ModeIrregular, which reads as
		// "not a regular file" and would leave every snapshot behind on a
		// repository on a mount that reports it. The lstat Go already needs
		// for the mtime answers it, and it keeps a symlink planted under the
		// prefix out of the sweep.
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(gitDir, name))
	}
}
