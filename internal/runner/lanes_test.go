// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentLanesRunEveryReviewOnce drives the parallel loop, where every
// lane is a goroutine taking reviews off one shared queue and adding its
// result to one shared tally while the others do the same. Nothing here
// checks what a review found: the assertion is that a fan-out of N lanes over
// a queue of M reviews runs each of them exactly once, and that what the
// shared state reports afterwards accounts for all of them. A lane that lost
// the queue, a result the ring dropped, or a publish that raced the run's
// ending shows up as a missing or doubled name.
//
// The fake agent sleeps, so the lanes overlap instead of finishing in turn,
// which is what makes the queue, the pending mutex, the stats ring, and the
// event bus contended rather than merely concurrent. Run under -race.
func TestConcurrentLanesRunEveryReviewOnce(t *testing.T) {
	repo := testRepo(t)
	reviews := []string{
		"sec-review", "doc-review", "perf-review", "test-review",
		"build-review", "api-review", "deps-review", "ui-review",
	}
	set, _ := promptSet(t, reviews...)
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "Reading main.go"
sleep 0.05
echo "RESULT: changed=0"`)

	cfg := baseConfig(t, repo, set, reviews, bin)
	const jobs = 4
	cfg.Jobs = jobs
	// One loop: a second would rerun every review and the counting below could
	// no longer tell a doubled result from a review that simply ran twice.
	cfg.MaxLoops = 1

	r, events := runRecorded(t, cfg)

	if got := r.Loops(); got != 1 {
		t.Fatalf("completed loops = %d, want 1", got)
	}
	if left := r.Pending(); len(left) != 0 {
		t.Fatalf("queue still holds %v after the loop, want it empty", left)
	}

	results := r.Stats().Results()
	if len(results) != len(reviews) {
		t.Fatalf("results = %d, want %d: %v", len(results), len(reviews), names(results))
	}
	if got := r.Stats().Counts().Total(); got != len(reviews) {
		t.Fatalf("counted results = %d, want %d", got, len(reviews))
	}
	if got := countByReview(results); !equalNames(got, reviews) {
		t.Fatalf("recorded reviews = %v, want each of %v exactly once", got, reviews)
	}

	// One review_end per review, each on a branch of its own. A run that fell
	// back to the sequential in-place loop reports a review with no branch at
	// all, so the branch is also what says the lanes really ran: two lanes
	// sharing a review would still report the right count if a result were
	// also missing.
	ends := map[string]int{}
	branches := map[string]string{}
	for _, ev := range events {
		if ev.Kind != EvReviewEnd {
			continue
		}
		ends[ev.Review]++
		if ev.Review != "" {
			branches[ev.Review] = ev.Branch
			if ev.Branch == "" {
				t.Fatalf("%s ended with no branch: the run did not use lane worktrees", ev.Review)
			}
		}
	}
	if !equalNames(ends, reviews) {
		t.Fatalf("review_end events = %v, want one for each of %v", ends, reviews)
	}
	seen := map[string]string{}
	for review, branch := range branches {
		if other, dup := seen[branch]; dup {
			t.Fatalf("lanes %s and %s shared branch %q", other, review, branch)
		}
		seen[branch] = review
	}
}

// names is the review names of results, in the order recorded, for a failure
// message that has to show which review is missing.
func names(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Review)
	}
	return out
}

// countByReview tallies results per review name, so a review recorded twice
// reads as a count of two rather than as a name that looks right.
func countByReview(results []Result) map[string]int {
	out := make(map[string]int, len(results))
	for _, r := range results {
		out[r.Review]++
	}
	return out
}

// equalNames reports whether got holds every want exactly once, whatever order
// the two are in.
func equalNames(got map[string]int, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, name := range want {
		if got[name] != 1 {
			return false
		}
	}
	return true
}

// TestPendingQueueIsDrainedOncePerLane checks the queue the lanes take from
// directly, with no git, no agent, and no tree: every review must be handed
// out exactly once, and only to the lane it was scheduled for, whichever
// goroutine asks first. A lane that took another's review would run it in the
// wrong worktree, and a queue entry taken twice would run a review twice in
// one tree.
func TestPendingQueueIsDrainedOncePerLane(t *testing.T) {
	const jobs, reviews = 4, 40
	r := &Runner{cfg: Config{Jobs: jobs}}
	scheduled := make([]string, 0, reviews)
	for i := range reviews {
		scheduled = append(scheduled, fmt.Sprintf("review-%02d", i))
	}
	r.setPending(scheduled)

	var mu sync.Mutex
	taken := make(map[string]int, reviews)
	var wg sync.WaitGroup
	for lane := range jobs {
		wg.Go(func() {
			for {
				name, ok := r.takeNextFor(lane)
				if !ok {
					return
				}
				mu.Lock()
				taken[name]++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if len(taken) != len(scheduled) {
		t.Fatalf("%d reviews handed out, want %d", len(taken), len(scheduled))
	}
	for _, name := range scheduled {
		if n := taken[name]; n != 1 {
			t.Fatalf("%s handed out %d times, want once", name, n)
		}
	}
	if left := r.Pending(); len(left) != 0 {
		t.Fatalf("queue still holds %d entries, want it empty", len(left))
	}
}
