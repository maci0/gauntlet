// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package humanize

import (
	"math"
	"testing"
	"time"
)

// The persisted elapsed_s field is JSON seconds, and three readers decode it
// for three surfaces. What they must not do is turn a value the field cannot
// hold into a duration: a negative renders as a run that never ended, and one
// past the nanosecond range wraps to a negative int64 and does the same.
func TestSeconds(t *testing.T) {
	cases := []struct {
		secs float64
		want time.Duration
		ok   bool
	}{
		{0, 0, false},
		{-1, 0, false},
		{0.5, 500 * time.Millisecond, true},
		{1, time.Second, true},
		{1800.25, 30*time.Minute + 250*time.Millisecond, true},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		{math.Inf(-1), 0, false},
		{float64(math.MaxInt64), 0, false},
	}
	for _, c := range cases {
		got, ok := Seconds(c.secs)
		if ok != c.ok || got != c.want {
			t.Errorf("Seconds(%v) = (%v, %v), want (%v, %v)", c.secs, got, ok, c.want, c.ok)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                  "0s",
		45 * time.Second:   "45s",
		60 * time.Second:   "1m00s",
		90 * time.Second:   "1m30s",
		3600 * time.Second: "1h00m",
		7500 * time.Second: "2h05m",
		-5 * time.Second:   "0s",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

// The prefix a log line and a journal replay carry has to name the zone, not
// only the wall clock. A fall-back transition repeats one local hour, so the
// two instants below render as the same 02:30:00 without it, an hour apart in
// real time; the reading also has to fit the 13-column timestamp `gauntlet
// show` pads to.
func TestClockSeparatesRepeatedWallClock(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	// Clock renders in the host zone, so the host is the zone under test
	// here. time.Local is a package var the runtime reads through, not one a
	// TZ change refreshes, which is why this assigns it rather than setting
	// TZ. It is restored before the next test reads it.
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
	// 2026-10-25 02:30 CEST and 2026-10-25 02:30 CET: the hour the clock is
	// put back over, the same wall clock twice.
	summer := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC).In(loc)
	winter := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC).In(loc)
	first, second := Clock(summer), Clock(winter)
	if first == second {
		t.Fatalf("Clock rendered both readings of a repeated hour as %q", first)
	}
	if want := "02:30:00+0200"; first != want {
		t.Errorf("Clock(early) = %q, want %q", first, want)
	}
	if want := "02:30:00+0100"; second != want {
		t.Errorf("Clock(late) = %q, want %q", second, want)
	}
	if n := len(Clock(summer)); n != 13 {
		t.Errorf("Clock width = %d, want 13 to fit the show column", n)
	}
}

func TestCount(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4321: "-4,321"}
	for in, want := range cases {
		if got := Count(in); got != want {
			t.Errorf("Count(%d) = %q, want %q", in, got, want)
		}
	}
}

// The reasoning share is displayed by two callers, so the percentage they
// print has one definition: truncated, clamped, and undefined-safe.
func TestShare(t *testing.T) {
	cases := []struct {
		part, whole, want int
	}{
		{0, 100, 0},
		{1, 3, 33},
		{50, 200, 25},
		{100, 100, 100},
		{150, 100, 100}, // a split that does not add up reads as full
		{-5, 100, 0},
		{5, 0, 0}, // no total is no measurement
		{5, -1, 0},
	}
	for _, c := range cases {
		if got := Share(c.part, c.whole); got != c.want {
			t.Errorf("Share(%d, %d) = %d, want %d", c.part, c.whole, got, c.want)
		}
	}
}

// A one-line list names a few and counts the rest, so a message about 40
// changed files still fits on a terminal line.
func TestList(t *testing.T) {
	cases := []struct {
		items []string
		max   int
		want  string
	}{
		{nil, 3, ""},
		{[]string{"a"}, 3, "a"},
		{[]string{"a", "b", "c"}, 3, "a, b, c"},
		{[]string{"a", "b", "c", "d"}, 3, "a, b, c and 1 more"},
		{[]string{"a", "b"}, 0, "a and 1 more"},
	}
	for _, c := range cases {
		if got := List(c.items, c.max); got != c.want {
			t.Errorf("List(%v, %d) = %q, want %q", c.items, c.max, got, c.want)
		}
	}
}
