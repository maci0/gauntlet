// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fuzzy matches user-typed names against the accepted set, for
// "did you mean" hints on typos.
package fuzzy

import (
	"strings"
	"unicode"
	"unicode/utf8"

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
	wantRunes := foldRunes(norm.NFC.String(want))
	best, bestD := "", distance+1
	var prev, cur []int
	for _, c := range candidates {
		candNorm := norm.NFC.String(c)
		candLen := utf8.RuneCountInString(candNorm)
		if candLen-len(wantRunes) >= bestD || len(wantRunes)-candLen >= bestD {
			continue
		}
		candRunes := foldRunes(candNorm)
		if d := editDistanceFolded(wantRunes, candRunes, &prev, &cur); d < bestD {
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
	return strings.Map(foldRune, s)
}

func foldRune(r rune) rune {
	lo := r
	for t := unicode.SimpleFold(r); t != r; t = unicode.SimpleFold(t) {
		if t < lo {
			lo = t
		}
	}
	return lo
}

func foldRunes(s string) []rune {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = foldRune(r)
	}
	return runes
}

// distance is how far a typo may stray and still earn a hint.
const distance = 3

// editDistance is Levenshtein distance over runes. Byte-based comparison
// would charge one edit per UTF-8 continuation byte, so a single differing
// character outside ASCII costs up to three edits and two names sharing
// prefix bytes can score spuriously close.
func editDistance(a, b string) int {
	return editDistanceFolded(foldRunes(a), foldRunes(b), nil, nil)
}

func editDistanceFolded(ar, br []rune, prevBuf, curBuf *[]int) int {
	needed := len(br) + 1
	var prev, cur []int
	if prevBuf != nil && cap(*prevBuf) >= needed {
		prev = (*prevBuf)[:needed]
		cur = (*curBuf)[:needed]
	} else {
		prev = make([]int, needed)
		cur = make([]int, needed)
		if prevBuf != nil {
			*prevBuf = prev
			*curBuf = cur
		}
	}
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}
