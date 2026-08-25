// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package fuzzy

import "testing"

func TestClosest(t *testing.T) {
	candidates := []string{"code-review", "sec-review", "quick", "standard"}
	tests := []struct {
		want, got string
	}{
		{"code-reveiw", "code-review"}, // transposition
		{"CODE-REVIEW", "code-review"}, // case-insensitive both ways
		{"secreview", "sec-review"},
		{"quck", "quick"},
		{"standart", "standard"},
		{"zzzzzz", ""}, // nothing close enough
		{"", ""},
	}
	for _, tt := range tests {
		if got := Closest(tt.want, candidates); got != tt.got {
			t.Errorf("Closest(%q) = %q, want %q", tt.want, got, tt.got)
		}
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "ab", 1},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"a", "", 1},
	}
	for _, tt := range tests {
		if got := EditDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("EditDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
