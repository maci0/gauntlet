// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fuzzy answers the one question a name typed by a person or read off
// a file asks before it is compared to anything: what is it, in the form two
// strings can be compared in? NFC and Fold are those forms, and IsASCII is
// the fast answer that says a value cannot differ from its folded self.
// Closest is the one fuzzy match, the "did you mean" behind a rejected name.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// NFC returns s in Unicode Normalization Form C.
func NFC(s string) string {
	if IsASCII(s) {
		return s
	}
	return norm.NFC.String(s)
}

// Sort orders names the way a reader looks for them.
//
// sort.Strings compares code points, which puts "Zebra" before "apple" and
// "Ätna" after "Zulu". Every list this orders is read by a person choosing
// from it: the agent names an error offers, the review names the picker and
// the dry run show, the set names a mistyped --reviews names back. A name
// outside ASCII then sorts to the end of the list whatever letter it starts
// with, and a name carrying an accent sorts nowhere near the letter a reader
// types to find it. The same names are already matched case-insensitively and
// accent-tolerantly by the picker filter and by Closest, so a list ordered by
// bytes contradicts what the rest of the tool does with those names.
//
// A list of lowercase ASCII letters and digits, with no punctuation, is
// already in this order, so the built-in agent and review names do not move.
// Case and punctuation are ordered by collation weight rather than by code
// point, which is the change: "Beta" files under B and "_under" under _,
// where byte order had put every capital first.
//
// Root collation, not a language: the tool has no locale setting, and
// English, German, and Turkish readers all want "apple" before "Äpple" and
// "apple" before "Zebra". Root is the order all of them agree on, and it
// keeps CJK, Cyrillic, and Arabic names ordered among themselves rather than
// in one block after every Latin one.
func Sort(names []string) {
	if len(names) < 2 {
		return
	}
	cmpNames := Comparator()
	sort.SliceStable(names, func(i, j int) bool {
		return cmpNames(names[i], names[j]) < 0
	})
}

// Comparator returns the three-way comparison Sort orders by, for a caller
// that sorts records of its own by a name field rather than a bare list of
// names: a stat sorted by review name, a breakdown sorted by tool:model label.
// Ordering such a list by byte value is the same defect Sort exists to fix,
// one package away from it.
//
// The collator carries per-comparison state, so the returned function is not
// safe to share across goroutines and builds one collator per call. Build it
// once per sort and reuse it for every comparison the sort makes.
func Comparator() func(a, b string) int {
	return collate.New(language.Und).CompareString
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
	return ClosestWithin(want, candidates, Limit)
}

// ClosestWithin is Closest with the caller's own edit-distance ceiling, for a
// name short enough that the shared limit would be noise: three edits turn
// "limt" into "log", which reads as a guess rather than a correction.
func ClosestWithin(want string, candidates []string, max int) string {
	wantNorm := NFC(want)
	if wantNorm == "" || max < 0 {
		return ""
	}
	var wantArr [32]rune
	wantRunes := foldRunesInto(wantNorm, wantArr[:0])
	best, bestD := "", max+1
	var prevArr, curArr [32]int
	prev, cur := prevArr[:], curArr[:]
	var candArr [32]rune
	candBuf := candArr[:0]
	for _, c := range candidates {
		candNorm := NFC(c)
		if candNorm == "" {
			continue
		}
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

// Limit is how far a typo may stray and still earn a hint. A caller whose
// names are short enough for that to be noise sets its own ceiling through
// ClosestWithin.
const Limit = 3

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
