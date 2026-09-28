// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/maci0/gauntlet/internal/runner"
)

// The journal is the file a backup, a sync, or a pasted "gauntlet runs --json"
// carries off the machine, so what lands in it must not name the account. Both
// free-text carriers are checked here: the command line on the index row, and
// the event text, which reaches disk with the absolute path of the reviewed
// tree inside git and path errors.
func TestJournalKeepsTheAccountNameOut(t *testing.T) {
	home := filepath.Join(t.TempDir(), "alice")
	t.Setenv("HOME", home)

	args := journaledArgs([]string{"review", "--dir", home + "/src/gauntlet", "--agent", "agy"})
	if slices.Contains(args, home) {
		t.Fatalf("the index row still carries the home path: %q", args)
	}
	if want := "review"; args[0] != want {
		t.Fatalf("argument %d rewritten: got %q, want %q", 0, args[0], want)
	}
	if want := "~/src/gauntlet"; args[2] != want {
		t.Fatalf("argument 2 = %q, want %q", args[2], want)
	}

	ev := journaledEvent(runner.Event{
		Kind: runner.EvMerge,
		Dir:  home + "/src/gauntlet",
		Text: "CONFLICT (content): Merge conflict in " + home + "/src/gauntlet/a.go",
	})
	if ev.Text == "" {
		t.Fatal("the conflict detail was dropped instead of shortened")
	}
	if want := "CONFLICT (content): Merge conflict in ~/src/gauntlet/a.go"; ev.Text != want {
		t.Fatalf("event text = %q, want %q", ev.Text, want)
	}
	// The dir is a key the run matches its own locks and the index on, so it
	// stays a resolved path: only the free text is shortened.
	if want := home + "/src/gauntlet"; ev.Dir != want {
		t.Fatalf("event dir = %q, want %q", ev.Dir, want)
	}
}

func TestJournaledEventLeavesEmptyTextAlone(t *testing.T) {
	if got := journaledEvent(runner.Event{Kind: runner.EvLoopStart}); got.Text != "" {
		t.Fatalf("text invented for an event that had none: %q", got.Text)
	}
}
