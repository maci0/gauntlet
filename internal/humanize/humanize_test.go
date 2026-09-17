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
