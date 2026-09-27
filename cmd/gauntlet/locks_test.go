// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/runner"
)

// lockNote runs one directory's worth of events through the lock notifier and
// returns what the lock file says afterwards. The event stream ends before the
// file is read, so the note is read at a known point rather than raced.
func lockNote(t *testing.T, events ...runner.Event) string {
	t.Helper()
	dir := t.TempDir()
	lock, err := runner.Acquire(runner.LockPath(dir))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Release()
	runs := []*dirRun{{dir: dir, lock: lock}}
	ch := make(chan runner.Event)
	done := make(chan struct{})
	go func() {
		defer close(done)
		noteLocks(runs, "run-1", ch)
	}()
	for _, ev := range events {
		ev.Dir = dir
		ch <- ev
	}
	close(ch)
	<-done
	raw, err := os.ReadFile(runner.LockPath(dir))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	return string(raw)
}

// A review scheduled twice is weight, not two rows: under --jobs both
// instances run at once, in different lanes and on different branches. The
// note has to name both, and the one that ends first must not take the other's
// entry with it and leave a running review described as idle.
func TestLockNoteFollowsARepeatedReviewInTwoLanes(t *testing.T) {
	const (
		laneA = "gauntlet/run-1-l1-lane0-00/security"
		laneB = "gauntlet/run-1-l1-lane1-01/security"
	)
	startA := runner.Event{Kind: runner.EvReviewStart, Review: "security", Loop: 1,
		Branch: laneA, Agent: "claude"}
	startB := runner.Event{Kind: runner.EvReviewStart, Review: "security", Loop: 1,
		Branch: laneB, Agent: "codex"}
	endA := runner.Event{Kind: runner.EvReviewEnd, Review: "security", Loop: 1,
		Branch: laneA, Agent: "claude"}
	endB := runner.Event{Kind: runner.EvReviewEnd, Review: "security", Loop: 1,
		Branch: laneB, Agent: "codex"}

	both := lockNote(t, startA, startB)
	for _, want := range []string{"security (claude)", "security (codex)"} {
		if !strings.Contains(both, want) {
			t.Errorf("note with both lanes running is %q, want it to name %q", both, want)
		}
	}

	one := lockNote(t, startA, startB, endA)
	if !strings.Contains(one, "security (codex)") {
		t.Errorf("note after lane 0 finished is %q, want the still-running lane named", one)
	}
	if strings.Contains(one, "claude") {
		t.Errorf("note after lane 0 finished is %q, want the finished lane dropped", one)
	}

	if drained := lockNote(t, startA, startB, endA, endB); !strings.Contains(drained, "idle") {
		t.Errorf("note after both lanes finished is %q, want it to read idle", drained)
	}

	none := lockNote(t, startA, endA)
	if !strings.Contains(none, "idle") {
		t.Errorf("note with nothing running is %q, want it to read idle", none)
	}
}

// The same review twice on one agent is still one line to wait for: a repeated
// line reads as two reviews rather than the one the reader is blocked on.
func TestLockNoteNamesARepeatedReviewOnce(t *testing.T) {
	note := lockNote(t,
		runner.Event{Kind: runner.EvReviewStart, Review: "security", Loop: 1,
			Branch: "gauntlet/run-1-l1-lane0-00/security", Agent: "claude"},
		runner.Event{Kind: runner.EvReviewStart, Review: "security", Loop: 1,
			Branch: "gauntlet/run-1-l1-lane1-01/security", Agent: "claude"},
	)
	if got := strings.Count(note, "security (claude)"); got != 1 {
		t.Errorf("note names the repeated review %d times, want 1: %q", got, note)
	}
}
