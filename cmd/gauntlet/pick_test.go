// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/term"
)

// The launcher draws on the alt screen and reads keys from stdin, so a pick
// without a terminal on both streams is refused before any tree is touched.
// This is the same gate --tui applies and TestTUINeedsBothTerminals pins
// there: a dashboard launched anyway quits on its first tick with nothing
// said, and the refusal is the only thing standing between the two.
func TestPickNeedsBothTerminals(t *testing.T) {
	// The test binary's stdout is a pipe under `go test`, so the gate trips
	// whether or not the developer's own terminal is attached.
	if term.IsTerminal(int(os.Stdout.Fd())) && stdinIsTerminal() {
		t.Skip("both streams are terminals here, so the refusal cannot be reached")
	}
	got := captureStderr(t, func() int { return run([]string{"pick"}) })
	if !strings.Contains(got, "pick needs a terminal on stdin and stdout") {
		t.Fatalf("the refusal should name both streams, got %q", got)
	}
}

func TestTreeStateUntrackedDoesNotCountAsDirty(t *testing.T) {
	dir, _ := gitRepo(t, "package main\n")

	ctx := context.Background()
	if branch, _, dirty := treeState(ctx, dir); dirty {
		t.Fatal("a clean tree must not look dirty to the launcher")
	} else if branch != "main" {
		t.Fatalf("branch = %q, want %q", branch, "main")
	}

	if err := os.WriteFile(filepath.Join(dir, "scratch.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, dirty := treeState(ctx, dir); dirty {
		t.Fatal("an untracked file must not block --jobs in the launcher")
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, dirty := treeState(ctx, dir); !dirty {
		t.Fatal("an uncommitted tracked edit must still block --jobs in the launcher")
	}
}

func TestTreeStateTargetsAndNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	ctx := context.Background()

	// Non-git directory returns empty branch and no dirty flag.
	nonGit := t.TempDir()
	if b, targets, dirty := treeState(ctx, nonGit); b != "" || len(targets) != 0 || dirty {
		t.Fatalf("treeState on non-git dir: got (%q, %v, %v), want (\"\", nil, false)", b, targets, dirty)
	}

	dir, run := gitRepo(t, "package main\n")
	run("branch", "feature-1")
	run("branch", "feature-2")

	branch, targets, dirty := treeState(ctx, dir)
	if branch != "main" || dirty {
		t.Fatalf("treeState on clean repo: got (%q, %v, %v), want branch main, dirty false", branch, targets, dirty)
	}
	slices.Sort(targets)
	wantTargets := []string{"feature-1", "feature-2"}
	if !slices.Equal(targets, wantTargets) {
		t.Fatalf("targets = %v, want %v", targets, wantTargets)
	}
}
