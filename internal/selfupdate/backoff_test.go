// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import "testing"

// TestAssetJitterIsReproducibleFromTheSeed is the property the retry wait
// lost when it was drawn from the process-wide random generator: the same
// seed and the same attempt must give the same wait, twice over. Without it a
// report of what the updater waited cannot be checked, because nothing about
// the sequence survives the process.
func TestAssetJitterIsReproducibleFromTheSeed(t *testing.T) {
	defer func(s uint64) { jitterSeed = s }(jitterSeed)
	jitterSeed = 0x0123456789abcdef
	first := assetJitter(2, 1000)
	if again := assetJitter(2, 1000); again != first {
		t.Errorf("assetJitter(2, 1000) = %d then %d under one seed; want a stable draw", first, again)
	}
	jitterSeed = 0xfedcba9876543210
	if other := assetJitter(2, 1000); other == first {
		t.Errorf("assetJitter(2, 1000) = %d under both seeds; the seed decorrelates nothing", first)
	}
}

// TestAssetJitterStaysInRange covers the bound and the degenerate n, the two
// ways a keyed draw can be out of its contract: a value at or above n would
// make the wait overshoot its cap, and n <= 1 has exactly one answer.
func TestAssetJitterStaysInRange(t *testing.T) {
	for attempt := range 64 {
		for _, n := range []int{1, 2, 3, 7, 1000, 1 << 20} {
			got := assetJitter(attempt, n)
			if n <= 1 {
				if got != 0 {
					t.Errorf("assetJitter(%d, %d) = %d, want 0 for a single-valued range", attempt, n, got)
				}
				continue
			}
			if got < 0 || got >= int64(n) {
				t.Errorf("assetJitter(%d, %d) = %d, outside [0, %d)", attempt, n, got, n)
			}
		}
	}
}

// TestAssetBackoffIsCappedAndVaries is the reason the jitter is there: two
// attempts after one outage must not wait the same length, and neither may
// wait past the cap the doubling would otherwise pass.
func TestAssetBackoffIsCappedAndVaries(t *testing.T) {
	seen := make(map[int]bool, assetAttempts)
	for attempt := range assetAttempts {
		d := assetBackoff(attempt)
		if d < assetRetryBase>>1 {
			t.Errorf("assetBackoff(%d) = %v, under half the base delay", attempt, d)
		}
		if d > assetRetryMax {
			t.Errorf("assetBackoff(%d) = %v, past the %v cap", attempt, d, assetRetryMax)
		}
		seen[int(d)] = true
	}
	if len(seen) < 2 {
		t.Errorf("every attempt waited the same length (%v); the jitter is not reaching the wait", seen)
	}
}
