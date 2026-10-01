// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

// A lock this process cannot take is not evidence that nobody holds it. The
// reviewed tree decides where the lock file lands, so it can be unreadable, a
// directory, or a symlink, and a resume that read any of those as "idle"
// would exec a second gauntlet into a tree the first still owns. The two
// failures want different sentences, so they are told apart: held by a run, or
// unreadable.
func TestResumeRefusesWhenTheLockCannotBeRead(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	// A directory in place of the lock file: the plant the lock's own
	// regular-file check refuses, and one a reviewed repository can commit.
	dir := t.TempDir()
	if err := os.MkdirAll(runner.LockPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	cp := checkpoint{
		Handoff: handoff{RunID: "20261001T015123Z-1a2b", Dirs: map[string]dirHandoff{handoffKey(dir): {}}},
		Cwd:     dir, Updated: time.Now(),
	}
	if err := saveCheckpoint(cp); err != nil {
		t.Fatal(err)
	}

	reexec = func(string, string, []string) error {
		t.Fatal("an unreadable lock must not reach the exec")
		return nil
	}
	t.Cleanup(func() { reexec = selfupdate.Reexec })

	code, stderr := captureFD(t, &os.Stderr, func() int { return run([]string{"resume", "20261001T015123Z-1a2b"}) })
	if code != exitLocked || !strings.Contains(stderr, "cannot tell whether") {
		t.Fatalf("exit %d, want %d and a reason:\n%s", code, exitLocked, stderr)
	}

	// The listing reports the same state rather than calling the run ready,
	// since a resume would refuse it.
	code, out := captureFD(t, &os.Stdout, func() int { return run([]string{"resume"}) })
	if code != exitOK || !strings.Contains(out, "unknown") {
		t.Fatalf("exit %d, listing:\n%s", code, out)
	}
}

// The directory the refusal names has to be the one whose lock failed, not the
// first entry of a map walked in map order: an operator reading "a gauntlet
// is running in <path>" has to know which tree to go and look at.
func TestResumeNamesTheDirectoryWhoseLockFailed(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	locked, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	free := t.TempDir()
	// Both spellings, so the sort order and the failure order differ.
	if err := os.MkdirAll(runner.LockPath(locked), 0o755); err != nil {
		t.Fatal(err)
	}
	cp := checkpoint{
		Handoff: handoff{RunID: "20261001T015123Z-1a2b", Dirs: map[string]dirHandoff{
			handoffKey(locked): {}, handoffKey(free): {},
		}},
		Cwd: free, Updated: time.Now(),
	}
	if err := saveCheckpoint(cp); err != nil {
		t.Fatal(err)
	}
	reexec = func(string, string, []string) error {
		t.Fatal("an unreadable lock must not reach the exec")
		return nil
	}
	t.Cleanup(func() { reexec = selfupdate.Reexec })

	_, stderr := captureFD(t, &os.Stderr, func() int { return run([]string{"resume", "20261001T015123Z-1a2b"}) })
	if !strings.Contains(stderr, filepath.Base(locked)) {
		t.Fatalf("the refusal does not name the directory whose lock failed:\n%s", stderr)
	}
}
