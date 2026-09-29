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
	if got := Closest("", []string{"a", "b", "c"}); got != "" {
		t.Errorf("Closest with empty query returned %q, want empty", got)
	}
	if got := Closest("a", []string{"", "something-long"}); got != "" {
		t.Errorf("Closest with empty candidate returned %q, want empty", got)
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

func foldRunes(s string) []rune {
	return foldRunesInto(s, make([]rune, 0, len(s)))
}

func editDistance(a, b string) int {
	var prevArr, curArr [32]int
	d, _, _ := editDistanceFolded(foldRunes(a), foldRunes(b), prevArr[:], curArr[:])
	return d
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

func TestNFC(t *testing.T) {
	nfd := "cafe\u0301"
	want := "caf\u00e9"
	if got := NFC(nfd); got != want {
		t.Fatalf("NFC(%q) = %q, want %q", nfd, got, want)
	}
	ascii := "hello world"
	if got := NFC(ascii); got != ascii {
		t.Fatalf("NFC(%q) = %q, want %q", ascii, got, ascii)
	}
}

// TestSort pins the order a reader sees in every list of names the tool
// prints. Byte order puts "Zebra" before "apple" and every non-ASCII name
// after every ASCII one; the list is read by a person scanning for the name
// they want, and the same names are already matched case-insensitively by the
// picker filter and by Closest.
func TestSort(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "one",
			in:   []string{"only"},
			want: []string{"only"},
		},
		{
			// The built-in names are lowercase ASCII letters, digits, and
			// dashes, and a list of those is already in this order, so
			// nothing the tool ships moves.
			name: "builtin name shapes hold",
			in:   []string{"sec-review", "a11y-review", "code-review", "error-review"},
			want: []string{"a11y-review", "code-review", "error-review", "sec-review"},
		},
		{
			// Case and punctuation are ordered by collation weight, not by
			// code point. Byte order had every capital and every underscore
			// ahead of the lowercase letters.
			name: "case and punctuation by weight",
			in:   []string{"zeta", "alpha", "Beta", "a11y", "_under"},
			want: []string{"_under", "a11y", "alpha", "Beta", "zeta"},
		},
		{
			name: "case is not order",
			in:   []string{"zebra", "Apple", "mango"},
			want: []string{"Apple", "mango", "zebra"},
		},
		{
			// U+00C4 sorts after "Z" by code point, so byte order files an
			// accented name after every plain one.
			name: "accented beside its letter",
			in:   []string{"Zebra", "Ätna", "Apple"},
			want: []string{"Apple", "Ätna", "Zebra"},
		},
		{
			name: "cjk among the latin names",
			in:   []string{"zeta", "日本語-review", "alpha"},
			want: []string{"alpha", "zeta", "日本語-review"},
		},
		{
			name: "cyrillic and greek",
			in:   []string{"zeta", "Код", "alpha", "Ασφάλεια"},
			want: []string{"alpha", "zeta", "Ασφάλεια", "Код"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Clone(tt.in)
			Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Sort(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestSortIsPermutation drives Sort over arbitrary names: it must reorder
// nothing away, drop nothing, and panic on nothing, which is what every
// caller of a name list depends on. The name lists come from a reviewed
// repository and from ~/.gauntlet/agents.json, so they are arbitrary bytes
// as far as this package is concerned.
func TestSortIsPermutation(t *testing.T) {
	seeds := [][]string{
		{},
		{"a"},
		{"a", "a", "a"},
		{"", "a", "", "b"},
		{"日本語", "α", "Код", "a"},
		{"\x00\x01", "\xff\xfe", "a"},
		{"codex🚀", "codex", "codex👨‍👩‍👧"},
		{strings.Repeat("a", 500), "b"},
		{"İstanbul", "istanbul", "Istanbul"},
	}
	for _, seed := range seeds {
		got := slices.Clone(seed)
		Sort(got)
		if len(got) != len(seed) {
			t.Fatalf("Sort(%q) changed the length to %d", seed, len(got))
		}
		for _, n := range seed {
			if !slices.Contains(got, n) {
				t.Errorf("Sort(%q) dropped %q: %q", seed, n, got)
			}
		}
	}
}

func TestIsASCII(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"hello", true},
		{"a-z A-Z 0-9 !@#$%^&*()", true},
		{"\x00\x1f\x7f", true},
		{"\x80", false},
		{"hello\x80world", false},
		{"café", false},
		{"日本語", false},
		{"codex🚀", false},
	}
	for _, c := range cases {
		if got := IsASCII(c.in); got != c.want {
			t.Errorf("IsASCII(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
