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

// NFC returns s in Unicode Normalization Form C.
func NFC(s string) string {
	if IsASCII(s) {
		return s
	}
	return norm.NFC.String(s)
}

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
	wantNorm := NFC(want)
	var wantArr [32]rune
	wantRunes := foldRunesInto(wantNorm, wantArr[:0])
	best, bestD := "", distance+1
	var prevArr, curArr [32]int
	prev, cur := prevArr[:], curArr[:]
	var candArr [32]rune
	candBuf := candArr[:0]
	for _, c := range candidates {
		candNorm := NFC(c)
		candLen := len(c)
		if !IsASCII(c) {
			candLen = utf8.RuneCountInString(candNorm)
		}
		if candLen-len(wantRunes) >= bestD || len(wantRunes)-candLen >= bestD {
			continue
		}
		candBuf = foldRunesInto(candNorm, candBuf[:0])
		var d int
		d, prev, cur = editDistanceFolded(wantRunes, candBuf, prev, cur)
		if d < bestD {
			best, bestD = c, d
			if bestD == 0 {
				return best
			}
		}
	}
	return best
}

// IsASCII reports whether s is all bytes below 0x80.
func IsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
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
	if r < 128 {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	lo := r
	for t := unicode.SimpleFold(r); t != r; t = unicode.SimpleFold(t) {
		if t < lo {
			lo = t
		}
	}
	return lo
}

func foldRunesInto(s string, dst []rune) []rune {
	for _, r := range s {
		dst = append(dst, foldRune(r))
	}
	return dst
}

// distance is how far a typo may stray and still earn a hint.
const distance = 3

func editDistanceFolded(ar, br []rune, prev, cur []int) (int, []int, []int) {
	if len(br) > len(ar) {
		ar, br = br, ar
	}
	needed := len(br) + 1
	if cap(prev) < needed {
		prev = make([]int, needed)
		cur = make([]int, needed)
	} else {
		prev = prev[:needed]
		cur = cur[:needed]
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
	return prev[len(br)], prev, cur
}
