// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
