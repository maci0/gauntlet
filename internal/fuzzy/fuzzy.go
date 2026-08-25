// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fuzzy matches user-typed names against the accepted set, for
// "did you mean" hints on typos.
package fuzzy

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Closest returns the candidate nearest want within a small edit distance,
// compared case-insensitively and after Unicode normalization, or "" when
// nothing is close enough.
//
// Case comparison is simple case folding, not lowercasing: folding equates
// characters whose lowercase forms differ (ſ and s, U+212A KELVIN SIGN and k,
// final and ordinary sigma), which ToLower misses on one side only.
// Normalization to NFC first keeps a decomposed spelling of the same name
// from looking like several edits' worth of typos.
func Closest(want string, candidates []string) string {
	want = norm.NFC.String(want)
	best, bestD := "", distance+1
	for _, c := range candidates {
		if d := editDistance(want, norm.NFC.String(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// Fold canonicalizes case: every rune is mapped to the smallest rune of its
// simple-fold orbit. Lowercasing misses the pairs whose lowercase forms
// differ from their fold forms (Greek final and ordinary sigma, long and
// round s), so a query typed with one spelling would not find text spelled
// with the other; folding equates them on both sides.
func Fold(s string) string {
	return strings.Map(func(r rune) rune {
		lo := r
		for t := unicode.SimpleFold(r); t != r; t = unicode.SimpleFold(t) {
			if t < lo {
				lo = t
			}
		}
		return lo
	}, s)
}

// distance is how far a typo may stray and still earn a hint.
const distance = 3

// editDistance is Levenshtein distance over runes. Byte-based comparison
// would charge one edit per UTF-8 continuation byte, so a single differing
// character outside ASCII costs up to three edits and two names sharing
// prefix bytes can score spuriously close.
func editDistance(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if foldEqual(ar[i-1], br[j-1]) {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// foldEqual reports whether two runes are equal under simple case folding.
// The orbit walk terminates: SimpleFold is a permutation over the runes that
// fold together, so it always returns to its starting point.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}
	return false
}
