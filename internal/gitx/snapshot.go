// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
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

// Valid reports whether Restore can apply s. An invalid snapshot is a no-op
// for the retry path: better to retry on the live tree than to refuse the
// review because a snapshot could not be taken.
func (s Snapshot) Valid() bool {
	return isHex(s.fullTree) && isHex(s.indexTree) && isHex(s.head)
}

// Snapshot records the current checkout so Restore can put it back. The
// worktree walk uses a private index, so the real index and files are not
// touched; the tree objects sit in the object store until gc.
func (r *Repo) Snapshot(ctx context.Context) (Snapshot, error) {
	if r == nil || !Available() {
		return Snapshot{}, errors.New("git is not available")
	}
	head, err := r.Tip(ctx, "HEAD")
	if err != nil || !isHex(head) {
		return Snapshot{}, fmt.Errorf("cannot read HEAD: %w", err)
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
func (r *Repo) Restore(ctx context.Context, s Snapshot) error {
	if r == nil || !Available() {
		return errors.New("git is not available")
	}
	if !s.Valid() {
		return errors.New("invalid snapshot")
	}
	if _, err := r.run(ctx, gitNormal, "reset", "--hard", s.head); err != nil {
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

// worktreeTree writes a tree of the current worktree, including untracked
// files and excluding ignored ones, without changing the real index.
func (r *Repo) worktreeTree(ctx context.Context) (string, error) {
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(gitDir, "gauntlet-snap-")
	if err != nil {
		return "", fmt.Errorf("cannot create a snapshot index: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if data, err := os.ReadFile(filepath.Join(gitDir, "index")); err == nil {
		if err := os.WriteFile(tmpName, data, 0o600); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else if err := os.Remove(tmpName); err != nil {
		// An empty file is not a valid index; git add will create one.
		return "", err
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
