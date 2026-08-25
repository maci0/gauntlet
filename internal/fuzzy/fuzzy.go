// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fuzzy matches user-typed names against the accepted set, for
// "did you mean" hints on typos.
package fuzzy

import "strings"

// Closest returns the candidate nearest want within a small edit distance,
// compared case-insensitively, or "" when nothing is close enough.
func Closest(want string, candidates []string) string {
	want = strings.ToLower(want)
	best, bestD := "", distance+1
	for _, c := range candidates {
		if d := editDistance(want, strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

// distance is how far a typo may stray and still earn a hint.
const distance = 3

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
