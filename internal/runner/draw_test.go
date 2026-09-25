// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// TestDrawIndexIsDeterministicAndBounded checks the primitive every keyed
// choice rests on: the same seed and key always give the same index, and the
// index never escapes [0, n).
func TestDrawIndexIsDeterministicAndBounded(t *testing.T) {
	if got := drawIndex(42, "any", 0); got != 0 {
		t.Fatalf("drawIndex(n=0) = %d, want 0", got)
	}
	if got := drawIndex(42, "any", 1); got != 0 {
		t.Fatalf("drawIndex(n=1) = %d, want 0", got)
	}
	if got := drawIndex64(42, "any", 0); got != 0 {
		t.Fatalf("drawIndex64(n=0) = %d, want 0", got)
	}
	if got := drawIndex64(42, "any", 1); got != 0 {
		t.Fatalf("drawIndex64(n=1) = %d, want 0", got)
	}
	big := int64(math.MaxInt32) + 50000
	if got := drawIndex64(42, "big", big); got < 0 || got >= big {
		t.Fatalf("drawIndex64(big) = %d, out of range [0, %d)", got, big)
	}
	for n := 1; n <= 17; n++ {
		for i := range 200 {
			key := fmt.Sprintf("key-%d", i)
			got := drawIndex(42, key, n)
			if got < 0 || got >= n {
				t.Fatalf("drawIndex(%q, %d) = %d, out of range", key, n, got)
			}
			if again := drawIndex(42, key, n); got != again {
				t.Fatalf("drawIndex(%q, %d) not stable: %d then %d", key, n, got, again)
			}
		}
	}
}

// TestBackoffReplaysFromTheSeed pins what a journal's seed is worth for
// retries: two runners with one seed wait exactly the same for any review and
// attempt, within the doubling window's bounds.
func TestBackoffReplaysFromTheSeed(t *testing.T) {
	build := func(seed uint64) *Runner {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t,
			[]string{"aa-review"}, []agent.Spec{{Tool: "claude"}}, seed), NewBus())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r1, r2 := build(11), build(11)
	for _, review := range []string{"aa-review", "zz-review"} {
		for attempt := range 3 {
			d1 := r1.backoff(review, attempt)
			if d1 != r2.backoff(review, attempt) {
				t.Fatalf("backoff(%s, %d) differs between seeded runners: %s",
					review, attempt, d1)
			}
			if d1 < retryBaseDelay/2 || d1 > retryBaseDelay<<attempt {
				t.Fatalf("backoff(%s, %d) = %s outside [%s, %s]",
					review, attempt, d1, retryBaseDelay/2, retryBaseDelay<<attempt)
			}
		}
	}
	if r1.backoff("aa-review", 0) == r1.backoff("zz-review", 0) &&
		r1.backoff("zz-review", 0) == r1.backoff("qq-review", 0) {
		t.Fatal("distinct reviews all drew identical jitter; keys are not mixing")
	}
	if d := r1.backoff("aa-review", -1); d < retryBaseDelay/2 || d > retryBaseDelay {
		t.Fatalf("backoff with negative attempt = %s, want within [%s, %s]",
			d, retryBaseDelay/2, retryBaseDelay)
	}
}

// TestBackoffIgnoresOtherDraws guards the keyed-draw property end to end:
// sampling an agent between two backoff calls must not move the second one,
// which is what lets parallel lanes replay without a global draw order.
func TestBackoffIgnoresOtherDraws(t *testing.T) {
	r, err := New(context.Background(), seedConfig(t,
		[]string{"aa-review"}, []agent.Spec{{Tool: "claude"}, {Tool: "codex"}}, 5), NewBus())
	if err != nil {
		t.Fatal(err)
	}
	before := r.backoff("aa-review", 0)
	_ = r.pickAgent("some-other-review", nil)
	if after := r.backoff("aa-review", 0); before != after {
		t.Fatalf("an unrelated pick moved backoff from %s to %s", before, after)
	}
}

func TestSeedOrClock(t *testing.T) {
	// Explicit seed passes through untouched.
	if got := seedOrClock(42, nil); got != 42 {
		t.Fatalf("seedOrClock with explicit seed = %d, want 42", got)
	}

	// Zero seed derives from clock.
	fixed := time.Unix(1234567890, 987654321)
	clock := func() time.Time { return fixed }
	if got := seedOrClock(0, clock); got != uint64(fixed.UnixNano()) {
		t.Fatalf("seedOrClock from clock = %d, want %d", got, fixed.UnixNano())
	}

	// Zero UnixNano falls back to 1 (seed 0 means unset).
	zeroClock := func() time.Time { return time.Unix(0, 0) }
	if got := seedOrClock(0, zeroClock); got != 1 {
		t.Fatalf("seedOrClock with zero clock = %d, want 1", got)
	}

	// Zero Time falls back to 1 without invoking undefined UnixNano behavior.
	uninitClock := func() time.Time { return time.Time{} }
	if got := seedOrClock(0, uninitClock); got != 1 {
		t.Fatalf("seedOrClock with uninitialized Time = %d, want 1", got)
	}

	// Nil clock uses time.Now and returns non-zero.
	if got := seedOrClock(0, nil); got == 0 {
		t.Fatal("seedOrClock with nil clock returned 0")
	}
}
