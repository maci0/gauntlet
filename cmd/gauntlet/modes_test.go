// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
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
