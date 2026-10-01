// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --semcode names a helper the run cannot do without, so the run says so
// before it takes the locks, the way it already refuses a missing agent CLI. A
// run that finds out after locking, after prompt discovery, and after the
// suggest step answers a flag the reader has to reconnect to the top of a
// message stack; this one answers while the command line is still in view.
func TestSemcodeWithoutTheIndexerIsAUsageError(t *testing.T) {
	dir, _ := gitRepo(t, "package main\n")
	bindir := t.TempDir()
	claude := filepath.Join(bindir, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A PATH the indexer is nowhere in, while the agent CLI named on the
	// command line is: the check under test is about the helper, so the run
	// has to get past every other precondition to reach it.
	t.Setenv("PATH", bindir)
	t.Setenv("GAUNTLET_HOME", t.TempDir())

	code, said := captureStderrFor(t, func() int {
		return run([]string{"--dir", dir, "--semcode", "--agents", "claude", "--once"})
	})
	if code != exitUsage {
		t.Fatalf("a run without %s exited %d, want %d:\n%s", semcodeIndexer, code, exitUsage, said)
	}
	if !strings.Contains(said.String(), semcodeIndexer) {
		t.Fatalf("the missing helper was not named:\n%s", said)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gauntlet.lock")); err == nil {
		t.Error("the run took the lock before reporting the missing helper")
	}
}

// A dry run launches nothing, so it cannot fail on the missing helper; it says
// so on stderr instead, where the schedule it printed stays untouched on
// stdout.
func TestSemcodeDryRunWarnsWithoutTheIndexer(t *testing.T) {
	dir, _ := gitRepo(t, "package main\n")
	bindir := t.TempDir()
	claude := filepath.Join(bindir, "claude")
	if err := os.WriteFile(claude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir)
	t.Setenv("GAUNTLET_HOME", t.TempDir())

	stdout := captureStdout(t, func() {
		code, said := captureStderrFor(t, func() int {
			return run([]string{"--dir", dir, "--semcode", "--agents", "claude", "--dry-run"})
		})
		if code != exitOK {
			t.Fatalf("a dry run without %s exited %d, want %d:\n%s",
				semcodeIndexer, code, exitOK, said)
		}
		if !strings.Contains(said.String(), semcodeIndexer) {
			t.Errorf("the dry run did not mention the missing helper:\n%s", said)
		}
	})
	if !strings.Contains(stdout, "Dry run") {
		t.Fatalf("the schedule is missing from stdout:\n%s", stdout)
	}
}
