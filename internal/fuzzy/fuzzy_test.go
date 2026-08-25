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

func TestClosestNonASCII(t *testing.T) {
	candidates := []string{"codex🚀", "sécurity-review", "日本語-review"}
	tests := []struct {
		want, got string
	}{
		// One emoji is one edit, not four byte edits.
		{"codex", "codex🚀"},
		{"CODEX", "codex🚀"},
		// One accented rune is one edit, not two.
		{"security-review", "sécurity-review"},
		{"securty-review", "sécurity-review"},
		// Each CJK rune is one edit, not three bytes.
		{"日本-review", "日本語-review"},
	}
	for _, tt := range tests {
		if got := Closest(tt.want, candidates); got != tt.got {
			t.Errorf("Closest(%q) = %q, want %q", tt.want, got, tt.got)
		}
	}
	// Two substitutions stay under the threshold and still earn a hint;
	// four do not.
	if got := Closest("中国語-review", candidates); got != "日本語-review" {
		t.Errorf("Closest(中国語-review) = %q, want 日本語-review", got)
	}
	if got := Closest("abcdefg-review", candidates); got != "" {
		t.Errorf("Closest(abcdefg-review) = %q, want none", got)
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
		// Multibyte runes compare as single units.
		{"é", "e", 1},
		{"café", "cafe", 1},
		{"日本語", "日本", 1},
	}
	for _, tt := range tests {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
