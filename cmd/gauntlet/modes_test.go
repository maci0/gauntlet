// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
)

// promptPair builds a two-review set like a discovered project would have.
func promptPair(t *testing.T) prompt.Set {
	t.Helper()
	dir := t.TempDir()
	for _, n := range []string{"sec-review", "doc-review"} {
		body := "Your goal is to test " + n + ".\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestConfirmStackIsolationExplainsExcludedCheckout(t *testing.T) {
	dirty := &runner.StackDirtyError{Dir: "/repo", Remote: "origin", Base: "main",
		Tracked: []string{"changed.go"}, Untracked: []string{"notes.txt"}}

	reader := func(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
	var out bytes.Buffer
	ok, err := confirmStackIsolationWith(&out, reader("yes\n"), true, &options{}, dirty)
	if err != nil || !ok {
		t.Fatalf("interactive yes = %v, %v", ok, err)
	}
	for _, want := range []string{
		"UNCOMMITTED FILES (2)", "  changed.go\n", "  notes.txt\n",
		"will not be reviewed or included", "STACK BASE",
		"  remote  origin\n", "  branch  main\n",
		"After confirmation, gauntlet fetches this remote branch",
		"isolated worktree",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("confirmation is missing %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if ok, err := confirmStackIsolationWith(&out, reader("n\n"), true, &options{}, dirty); err != nil || ok {
		t.Fatalf("interactive no = %v, %v", ok, err)
	}
	if ok, err := confirmStackIsolationWith(io.Discard, reader(""), false, &options{}, dirty); err == nil || ok || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("unattended confirmation = %v, %v", ok, err)
	}
	if ok, err := confirmStackIsolationWith(io.Discard, reader(""), false, &options{yes: true}, dirty); err != nil || !ok {
		t.Fatalf("--yes confirmation = %v, %v", ok, err)
	}
}

// TestScheduleForTurnsFlagsIntoASchedule pins the decision of what runs:
// no explicit --reviews means everything discovered, an explicit list wins,
// exclusions apply to both, and a schedule that filters down to nothing is
// refused rather than run as an empty loop.
func TestScheduleForTurnsFlagsIntoASchedule(t *testing.T) {
	set := promptPair(t)
	d := &dirRun{dir: t.TempDir(), set: set}

	got, err := scheduleFor(d, &options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Discovery order follows the filesystem, so compare as a set.
	if !slices.Equal(slices.Sorted(slices.Values(got)),
		[]string{"doc-review", "sec-review"}) {
		t.Fatalf("default schedule %v, want every discovered review", got)
	}

	got, err = scheduleFor(d, &options{reviewsSet: true, reviews: "doc"}, nil)
	if err != nil || strings.Join(got, ",") != "doc-review" {
		t.Fatalf("explicit schedule %v (%v), want the named short name expanded", got, err)
	}

	if got, err := scheduleFor(d, &options{reviewsSet: true, reviews: "sec"}, map[string]bool{"sec-review": true}); err == nil {
		t.Fatalf("exclusion left %v, want a refusal", got)
	} else if !strings.Contains(err.Error(), "no reviews remain") {
		t.Fatalf("empty schedule error should say so: %v", err)
	}
}

// TestShowPromptPrintsTheExactAgentText pins --show-prompt: a known review
// prints the composed prompt (containment rules and substituted timeout
// included), a short name resolves to its -review file, and an unknown name
// is a usage error naming what exists.
func TestShowPromptPrintsTheExactAgentText(t *testing.T) {
	set := promptPair(t)
	opts := &options{showPrompt: "sec", timeout: 30 * time.Minute}

	var out bytes.Buffer
	if code := cmdShowPrompt(&out, set, opts); code != exitOK {
		t.Fatalf("exit %d for a known review", code)
	}
	got := out.String()
	for _, want := range []string{
		"Your goal is to test sec-review.",
		"Git is read-only for you",
		"30m00s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q:\n%s", want, got)
		}
	}

	stderr := captureStderr(t, func() int {
		return cmdShowPrompt(io.Discard, set, &options{showPrompt: "nope"})
	})
	for _, want := range []string{"Unknown review: nope", "sec-review"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("unknown-review error is missing %q:\n%s", want, stderr)
		}
	}
}

// TestShowPromptStripsTerminalEscapes pins the display boundary on
// --show-prompt: a planted prompt's escape sequences and bidi controls must
// not reach the terminal (an OSC 52 sequence overwrites the clipboard in many
// emulators), while the visible text around them survives so the prompt stays
// readable.
func TestShowPromptStripsTerminalEscapes(t *testing.T) {
	dir := t.TempDir()
	body := "Your goal is to test evil.\n\x1b]52;c;aGVsbG8=\x07" +
		"\x1b[31mred\x1b[0m \u202Espoof\u202C tail\n"
	if err := os.WriteFile(filepath.Join(dir, "evil-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := cmdShowPrompt(&out, set, &options{showPrompt: "evil"}); code != exitOK {
		t.Fatalf("exit %d for the planted review", code)
	}
	got := out.String()
	for _, want := range []string{"Your goal is to test evil.", "red spoof tail"} {
		if !strings.Contains(got, want) {
			t.Errorf("display form is missing visible text %q:\n%s", want, got)
		}
	}
	for _, bad := range []rune{'\x1b', '\x07', '\u202E'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("display form carries hostile character %#x:\n%s", bad, got)
		}
	}
}
