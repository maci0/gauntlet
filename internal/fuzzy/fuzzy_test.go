// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package fuzzy

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

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

// Folding equates case variants that lowercasing misses: the long s, the
// Kelvin sign, and the final sigma all fold to their ordinary counterparts,
// so a typo hint still fires when a name was typed with one of them.
func TestClosestFolding(t *testing.T) {
	candidates := []string{"sec-review", "kode-review", "sigma-review"}
	tests := []struct{ want, got string }{
		{"ſec-review", "sec-review"},        // U+017F LATIN SMALL LETTER LONG S
		{"\u212Aode-review", "kode-review"}, // U+212A KELVIN SIGN
		{"sigma-revie\u03C2", "sigma-review"},
	}
	for _, tt := range tests {
		if got := Closest(tt.want, candidates); got != tt.got {
			t.Errorf("Closest(%q) = %q, want %q", tt.want, got, tt.got)
		}
	}
}

// A decomposed spelling of the same name normalizes to the candidate instead
// of counting as edits: macOS writes NFD filenames while keyboards type NFC.
func TestClosestNormalization(t *testing.T) {
	candidates := []string{"sécurity-review"}
	nfd := norm.NFD.String("sécurity-review")
	if nfd == "sécurity-review" {
		t.Fatal("test fixture is not actually decomposed")
	}
	if got := Closest(nfd, candidates); got != "sécurity-review" {
		t.Errorf("Closest(NFD) = %q, want sécurity-review", got)
	}
}

// Fold maps every rune to its orbit's smallest member, so strings differing
// only by case (including the pairs lowercasing cannot equate, like final and
// ordinary sigma) compare equal. It is idempotent: folding a folded string
// changes nothing.
func TestFold(t *testing.T) {
	tests := []struct {
		a, b string
	}{
		{"quick", "QUICK"},
		{"sigma", "SIGMA"},
		// Lowercasing equates neither of these pairs; folding must.
		{"\u03C3", "\u03C2"},         // ordinary vs final sigma
		{"ſec-review", "sec-review"}, // U+017F LONG S vs s
	}
	for _, tt := range tests {
		if Fold(tt.a) != Fold(tt.b) {
			t.Errorf("Fold(%q) = %q != Fold(%q) = %q", tt.a, Fold(tt.a), tt.b, Fold(tt.b))
		}
	}
	if got := Fold(Fold("AbC")); got != Fold("AbC") {
		t.Errorf("Fold is not idempotent: %q", got)
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

func BenchmarkClosest(b *testing.B) {
	candidates := []string{
		"code-review", "sec-review", "quick", "standard", "a11y-review",
		"error-review", "lint-review", "mobile-review", "privacy-review",
		"api-review", "arch-review", "authz-review", "build-review",
		"cli-review", "compat-review", "concurrency-review", "config-review",
		"container-review", "db-review", "deps-review", "doc-review",
		"fuzz-review", "gitops-review", "helm-review", "i18n-review",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = Closest("secreview", candidates)
		_ = Closest("quck", candidates)
		_ = Closest("zzzzzz", candidates)
	}
}

// FuzzClosest feeds arbitrary query strings to Closest and Fold. It pins their
// robustness and correctness invariants: Closest never panics, returned hints
// always belong to the candidate set and lie within the edit distance limit,
// exact candidate matches always resolve with zero distance, and Fold is
// idempotent and preserves rune count.
func FuzzClosest(f *testing.F) {
	candidates := []string{
		"code-review", "sec-review", "quick", "standard", "a11y-review",
		"error-review", "lint-review", "mobile-review", "privacy-review",
		"api-review", "arch-review", "authz-review", "build-review",
		"cli-review", "compat-review", "concurrency-review", "config-review",
		"container-review", "db-review", "deps-review", "doc-review",
		"fuzz-review", "gitops-review", "helm-review", "i18n-review",
		"claude", "codex", "gemini", "opencode", "crush", "copilot", "amp", "dsh",
		"codex🚀", "sécurity-review", "日本語-review",
	}
	seeds := []string{
		"code-reveiw",
		"CODE-REVIEW",
		"secreview",
		"quck",
		"standart",
		"zzzzzz",
		"",
		"codex",
		"codex🚀",
		"security-review",
		"sécurity-review",
		"日本-review",
		"中国語-review",
		"ſec-review",
		"\u212Aode-review",
		"sigma-revie\u03C2",
		"a11y",
		"fuzz",
		"all",
		"claud",
		"gemini",
		"\x00\x01\x02",
		"review\r\n",
		strings.Repeat("a", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, want string) {
		got := Closest(want, candidates)
		if got != "" {
			if !slices.Contains(candidates, got) {
				t.Fatalf("Closest(%q) returned %q which is not in candidates", want, got)
			}
			d := editDistance(norm.NFC.String(want), norm.NFC.String(got))
			if d > distance {
				t.Fatalf("Closest(%q) = %q has distance %d > limit %d", want, got, d, distance)
			}
		}

		folded := Fold(want)
		if Fold(folded) != folded {
			t.Fatalf("Fold is not idempotent on %q", want)
		}
		if utf8.RuneCountInString(folded) != utf8.RuneCountInString(want) {
			t.Fatalf("Fold changed rune count of %q", want)
		}

		if want != "" && utf8.RuneCountInString(want) <= 64 {
			withSelf := append(candidates[:len(candidates):len(candidates)], want)
			selfMatch := Closest(want, withSelf)
			if selfMatch == "" {
				t.Fatalf("Closest(%q) failed to find itself in candidates", want)
			}
			if d := editDistance(norm.NFC.String(want), norm.NFC.String(selfMatch)); d != 0 {
				t.Fatalf("Closest(%q) self-match has distance %d != 0", want, d)
			}
		}
	})
}
