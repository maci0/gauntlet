// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package humanize

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                  "0s",
		45 * time.Second:   "45s",
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

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"90":  90 * time.Second,
		"90s": 90 * time.Second,
		"30m": 30 * time.Minute,
		"1h":  time.Hour,
		"2d":  48 * time.Hour,
		"1H":  time.Hour,
		// Largest value each unit can hold before time.Duration overflows.
		"9223372036s": 9223372036 * time.Second,
		"2562047h":    2562047 * time.Hour,
		"106751d":     106751 * 24 * time.Hour,
	}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil {
			t.Errorf("ParseDuration(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDuration(%q) = %v, want %v", in, got, want)
		}
	}
	// Each of these wraps time.Duration into a positive, plausible-looking
	// value (5124096h becomes ~25m26s) and must be rejected outright.
	for _, bad := range []string{"", "0", "-5m", "abc", "10y", "9999999999999999999999d",
		"5124096h", "36893488148s", "177922d"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) should fail", bad)
		}
	}
}

func TestParseDurationAllowZero(t *testing.T) {
	cases := map[string]time.Duration{
		"0":   0,
		"0s":  0,
		"0m":  0,
		"0h":  0,
		"0d":  0,
		"30m": 30 * time.Minute,
	}
	for in, want := range cases {
		got, err := ParseDurationAllowZero(in)
		if err != nil {
			t.Errorf("ParseDurationAllowZero(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDurationAllowZero(%q) = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "-5m", "abc", "10y"} {
		if _, err := ParseDurationAllowZero(bad); err == nil {
			t.Errorf("ParseDurationAllowZero(%q) should fail", bad)
		}
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
