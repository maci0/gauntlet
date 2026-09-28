// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package humanize converts and formats durations and counts for display. One
// implementation so the prompt text, the logs, and the dashboard never
// disagree about what "1h05m" means.
package humanize

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Seconds decodes the elapsed seconds a journal and an event stream persist as
// a JSON number, and reports whether there is one. It is the only reader of
// that field: the seconds are elapsed, not wall time, and the field is JSON
// seconds rather than a time.Duration's nanoseconds, so the multiply is the
// conversion and every reader has to make it.
//
// A value at or below zero, a NaN or an infinity, and a magnitude past the
// nanosecond range are all refused rather than clamped. json.Unmarshal hands
// back whatever the bytes held, and a corrupt or hostile journal is a file the
// tool reads: a negative duration renders as a run that never ended, and a
// past-the-range one wraps to a negative int64 and does the same.
func Seconds(secs float64) (time.Duration, bool) {
	if secs <= 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return 0, false
	}
	if secs > float64(math.MaxInt64)/float64(time.Second) {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

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

// Clock renders an instant as local wall clock with its zone offset: the
// prefix a log line and a journal replay carry.
//
// The offset is not decoration. Local wall clock alone names one instant per
// day unambiguously, so a run long enough to cross local midnight prints
// 23:59:59 and then 00:00:01 as if time had run backwards, and on each DST
// transition the same wall-clock hour is either absent or repeated: a run
// logging 02:30:00+02:00 and an hour later 02:30:00+01:00 in Europe/Warsaw
// prints the identical stamp for two events an hour apart. The offset is what
// tells those readings apart, and it names the zone they are in besides.
//
// 13 characters, the width the `gauntlet show` timestamp column is already
// padded to.
func Clock(t time.Time) string {
	return t.Local().Format("15:04:05-0700")
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

// Plural counts a thing in words, taking both spellings so a caller with an
// irregular noun does not have to append an "s" to get it wrong. The count is
// grouped, so a long figure reads as one number rather than as digits.
func Plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return Count(n) + " " + many
}

// Share is part as a whole-number percentage of whole, truncated, and held
// inside 0-100. whole <= 0 is no measurement and reports 0 rather than
// dividing by it, and a part above the whole (an agent whose disclosed split
// does not add up to the total it also reported) reads as 100 rather than
// past it. The arithmetic is int64 so a 32-bit int cannot wrap the multiply.
func Share(part, whole int) int {
	if whole <= 0 {
		return 0
	}
	return min(100, max(0, int(int64(part)*100/int64(whole))))
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
