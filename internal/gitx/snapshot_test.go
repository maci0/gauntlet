// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Restore must put the checkout back to the snapshot even when the "agent"
// committed, staged, edited tracked files, and left untracked scratch: that
// is the in-place retry path, and a second restore over an already-restored
// tree must be a no-op.
func TestSnapshotRestoreConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	main := filepath.Join(r.Dir, "main.go")
	wip := filepath.Join(r.Dir, "wip.go")
	staged := filepath.Join(r.Dir, "staged.go")
	if err := os.WriteFile(main, []byte("package main\n// dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wip, []byte("package wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("package staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", "staged.go")

	snap, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Valid() {
		t.Fatal("snapshot of a committed repo with dirty files must be valid")
	}

	head := gitOut(t, r.Dir, "rev-parse", "HEAD")
	wantMain := readFile(t, main)
	wantWIP := readFile(t, wip)
	wantStaged := readFile(t, staged)
	wantIndex := gitOut(t, r.Dir, "show", ":staged.go")

	if err := os.WriteFile(main, []byte("package broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wip, []byte("package wip\n// clobbered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "scratch.go"),
		[]byte("package scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", "-A")
	gitIn(t, r.Dir, "commit", "-qm", "agent commit")

	if err := r.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}
	assertRestored(t, r, head, main, wip, staged, wantMain, wantWIP, wantStaged, wantIndex)

	if err := r.Restore(ctx, snap); err != nil {
		t.Fatalf("a repeated Restore must succeed: %v", err)
	}
	assertRestored(t, r, head, main, wip, staged, wantMain, wantWIP, wantStaged, wantIndex)
}

func assertRestored(t *testing.T, r *Repo, head, main, wip, staged string, wantMain, wantWIP, wantStaged, wantIndex string) {
	t.Helper()
	if got := gitOut(t, r.Dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved: %s != %s", got, head)
	}
	if got := readFile(t, main); got != wantMain {
		t.Fatalf("main.go = %q, want %q", got, wantMain)
	}
	if got := readFile(t, wip); got != wantWIP {
		t.Fatalf("wip.go = %q, want %q", got, wantWIP)
	}
	if got := readFile(t, staged); got != wantStaged {
		t.Fatalf("staged.go = %q, want %q", got, wantStaged)
	}
	if got := gitOut(t, r.Dir, "show", ":staged.go"); got != wantIndex {
		t.Fatalf("index staged.go = %q, want %q", got, wantIndex)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "scratch.go")); err == nil {
		t.Fatal("the attempt's scratch.go is still in the tree")
	}
	ch, err := r.Status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Untracked) != 1 || ch.Untracked[0] != "wip.go" {
		t.Fatalf("untracked = %v, want [wip.go]", ch.Untracked)
	}
}

// A snapshot's trees are unreachable, so a gc that prunes them leaves a
// snapshot nothing can be restored from. The reset that would throw away the
// attempt's worktree runs first in the old order, so the operator learned
// about it after their own files were gone; the check is reported before any
// step touches the tree.
func TestRestoreReportsAPrunedSnapshotBeforeTouchingTheTree(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	main := filepath.Join(r.Dir, "main.go")
	wip := filepath.Join(r.Dir, "wip.go")
	if err := os.WriteFile(wip, []byte("package wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The attempt's work, as a restore would find it.
	gitIn(t, r.Dir, "add", "-A")
	gitIn(t, r.Dir, "commit", "-qm", "agent commit")
	agentMain := readFile(t, main)
	head := gitOut(t, r.Dir, "rev-parse", "HEAD")

	gitDir, err := r.gitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(gitDir, "objects", snap.fullTree[:2], snap.fullTree[2:])
	if err := os.Remove(loose); err != nil {
		t.Skipf("the snapshot tree is not a loose object here: %v", err)
	}

	err = r.Restore(ctx, snap)
	if err == nil {
		t.Fatal("restoring a snapshot the object store has pruned must fail")
	}
	if !strings.Contains(err.Error(), snap.fullTree) {
		t.Errorf("error does not name the missing tree: %v", err)
	}
	if got := gitOut(t, r.Dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved: %s != %s", got, head)
	}
	if got := readFile(t, main); got != agentMain {
		t.Errorf("the tree was reset before the snapshot was checked: %q != %q", got, agentMain)
	}
	if _, err := os.Stat(wip); err != nil {
		t.Errorf("the untracked file the snapshot held is gone: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotCleansStaleIndices(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(gitDir, "gauntlet-snap-old")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory and a symlink under the prefix are not a snapshot index, and
	// the sweep says so from the stat rather than from the type the directory
	// entry carries, so a git directory on a mount that reports no entry type
	// still sweeps the same set this one does.
	keep := map[string]string{
		"gauntlet-snap-dir":  "",
		"gauntlet-snap-link": stale,
	}
	if err := os.Mkdir(filepath.Join(gitDir, "gauntlet-snap-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range keep {
		if target == "" {
			continue
		}
		if err := os.Symlink(target, filepath.Join(gitDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	for name := range keep {
		if err := os.Chtimes(filepath.Join(gitDir, name), oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale snapshot file %s still exists", stale)
	}
	for name := range keep {
		if _, err := os.Lstat(filepath.Join(gitDir, name)); err != nil {
			t.Errorf("%s is not a snapshot index and must survive: %v", name, err)
		}
	}
}
