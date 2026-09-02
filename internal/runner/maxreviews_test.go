// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"slices"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
)

// TestMaxReviewsCutsAfterTheShuffle pins the flag's contract with --seed: a
// capped loop runs exactly the first N of the schedule an uncapped run would
// have shuffled, on every loop. Cutting before the shuffle would change the
// draw keys and reorder everything downstream of the cap.
func TestMaxReviewsCutsAfterTheShuffle(t *testing.T) {
	reviews := []string{
		"aa-review", "ab-review", "ac-review", "ad-review",
		"ae-review", "af-review", "ag-review", "ah-review", "ai-review",
	}
	agents := []agent.Spec{{Tool: "claude"}}

	full, err := New(context.Background(), seedConfig(t, reviews, agents, 42), NewBus())
	if err != nil {
		t.Fatal(err)
	}
	cfg := seedConfig(t, reviews, agents, 42)
	cfg.MaxReviews = 3
	capped, err := New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}

	for loop := 1; loop <= 3; loop++ {
		want := full.schedule(loop)[:3]
		got := capped.schedule(loop)
		if !slices.Equal(got, want) {
			t.Fatalf("loop %d: capped schedule %v, want the uncapped prefix %v", loop, got, want)
		}
	}
}

// TestMaxReviewsAtOrAboveScheduleIsANoOp: a cap the schedule never reaches
// changes nothing, including the shuffle itself.
func TestMaxReviewsAtOrAboveScheduleIsANoOp(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review"}
	agents := []agent.Spec{{Tool: "claude"}}

	schedule := func(maxReviews int) []string {
		t.Helper()
		cfg := seedConfig(t, reviews, agents, 7)
		cfg.MaxReviews = maxReviews
		r, err := New(context.Background(), cfg, NewBus())
		if err != nil {
			t.Fatal(err)
		}
		return r.schedule(1)
	}

	want := schedule(0)
	if len(want) != len(reviews) {
		t.Fatalf("uncapped schedule dropped reviews: %v", want)
	}
	for _, n := range []int{len(reviews), len(reviews) + 5} {
		if got := schedule(n); !slices.Equal(got, want) {
			t.Fatalf("cap %d changed the schedule: got %v, want %v", n, got, want)
		}
	}
}

// TestMaxReviewsDoesNotRecapAResumeQueue pins the hot-reload interaction: the
// queue a predecessor hands over is already the capped loop's unfinished
// remainder, and it may legitimately be longer than the cap when the cap was
// raised between processes. Re-applying the cap would silently drop reviews
// the interrupted loop still owed.
func TestMaxReviewsDoesNotRecapAResumeQueue(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review", "ad-review"}
	agents := []agent.Spec{{Tool: "claude"}}

	cfg := seedConfig(t, reviews, agents, 7)
	cfg.MaxReviews = 1
	cfg.ResumeQueue = []string{"ac-review", "aa-review"}
	r, err := New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.schedule(1); !slices.Equal(got, cfg.ResumeQueue) {
		t.Fatalf("resume queue was re-capped or reordered: %v, want %v", got, cfg.ResumeQueue)
	}
	// The loops after the handed-over one are this process's own and honor
	// the cap again.
	if got := r.schedule(2); len(got) != 1 {
		t.Fatalf("loop 2 ignored the cap: %v", got)
	}
}

// TestMaxReviewsTruncatesTheStackPass: stack mode never shuffles, so the cap
// cuts the configured order itself — the single pass runs the first N
// entries, which bounds the PR count at N.
func TestMaxReviewsTruncatesTheStackPass(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review", "ad-review"}
	agents := []agent.Spec{{Tool: "claude"}}

	cfg := seedConfig(t, reviews, agents, 7)
	cfg.StackedPRs = true
	cfg.StackPrep = &StackPrep{Base: "main"}
	cfg.MaxReviews = 2
	r, err := New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.cfg.Reviews; !slices.Equal(got, reviews[:2]) {
		t.Fatalf("stacked schedule %v, want the configured prefix %v", got, reviews[:2])
	}

	// A cap the pass never reaches leaves the configured order whole.
	cfg = seedConfig(t, reviews, agents, 7)
	cfg.StackedPRs = true
	cfg.StackPrep = &StackPrep{Base: "main"}
	cfg.MaxReviews = len(reviews) + 1
	r, err = New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.cfg.Reviews; !slices.Equal(got, reviews) {
		t.Fatalf("oversized cap changed the stacked schedule: %v, want %v", got, reviews)
	}
}
