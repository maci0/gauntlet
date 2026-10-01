// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// A stack checkout nested under .gauntlet is this run's scratch. Moving it
// must not look like an edit of the launch checkout, and a file beside it must.
func TestLaunchTreeIgnoresScratch(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	before, err := r.LaunchTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(r.Dir, ".gauntlet", "worktrees", "stack")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "edited.go"), []byte("package edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, ".gauntlet.lock"), []byte("lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterScratch, err := r.LaunchTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterScratch != before {
		t.Fatalf("scratch changed the launch tree: %s != %s", afterScratch, before)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "leaked.go"), []byte("package leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	afterEdit, err := r.LaunchTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if afterEdit == before {
		t.Fatal("an edit outside scratch left the launch tree unchanged")
	}
}

// The private index a snapshot writes is a descriptor this function owns on
// every exit. A tree whose real index cannot be opened took the branch that
// returned with the descriptor still held, and one snapshot is taken per
// review of an in-place run, so that was one leaked descriptor per review for
// as long as the run lasted. A FIFO planted at .git/index is the refusal that
// reaches it: safefile.OpenRead answers "not a regular file", which is not the
// ErrNotExist the empty-index branch is for.
func TestSnapshotReleasesItsIndexWhenTheRealOneIsRefused(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	gitDir, err := r.gitDir(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(gitDir, "index")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(gitDir, "index"), 0o600); err != nil {
		t.Skipf("cannot plant a FIFO: %v", err)
	}
	before, ok := openFDs()
	if !ok {
		t.Skip("no /proc or /dev/fd to count descriptors")
	}
	// One call would see one descriptor; several make the growth a rate, and
	// the per-review rate is what the bound is for. worktreeTree is the entry
	// that reaches snapshotTree with the private index open: Snapshot reads
	// the real index through git first, so a FIFO there fails before the
	// private one is ever created.
	for range 5 {
		if _, err := r.worktreeTree(ctx); err == nil {
			t.Fatal("a FIFO in place of the index should fail the worktree tree")
		}
	}
	after, ok := openFDs()
	if !ok {
		t.Skip("no /proc or /dev/fd to count descriptors")
	}
	if after > before {
		t.Fatalf("five refused worktree trees leaked %d file descriptors", after-before)
	}
}

// openFDs counts this process's open descriptors. Linux exposes them under
// /proc/self/fd and macOS under /dev/fd; a kernel with neither is skipped
// rather than failed, since the count is not portable.
func openFDs() (int, bool) {
	for _, dir := range []string{"/proc/self/fd", "/dev/fd"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		// ReadDir holds the directory it is listing, but only the difference
		// across two calls is read, so that one cancels.
		return len(entries), true
	}
	return 0, false
}
