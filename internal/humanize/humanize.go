// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package humanize formats durations and counts for display. One
// implementation so the prompt text, the logs, and the dashboard never
// disagree about what "1h05m" means.
package humanize

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration renders a wall-clock span compactly: 45s, 3m07s, 2h05m.
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int64(d / time.Second)
	switch {
	case secs >= 3600:
		return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
	case secs >= 60:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// Count renders an integer with thousands separators.
func Count(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// List names a few items and counts the rest, for a message that has to fit
// on one line: "a.go, b.go, c.go and 4 more".
func List(items []string, limit int) string {
	switch {
	case len(items) == 0:
		return ""
	case limit < 1:
		limit = 1
	}
	shown := items
	rest := 0
	if len(items) > limit {
		shown, rest = items[:limit], len(items)-limit
	}
	out := strings.Join(shown, ", ")
	if rest > 0 {
		out += fmt.Sprintf(" and %d more", rest)
	}
	return out
}
