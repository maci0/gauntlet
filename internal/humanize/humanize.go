// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package humanize formats durations and counts for display. One
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

// Duration renders a wall-clock span compactly: 45s, 3m07s, 2h05m.
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	switch {
	case secs >= 3600:
		return fmt.Sprintf("%dh%02dm", secs/3600, (secs%3600)/60)
	case secs >= 60:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// ParseDuration accepts the CLI's duration syntax: a positive integer with an
// optional s/m/h/d suffix (bare digits mean seconds).
func ParseDuration(s string) (time.Duration, error) {
	d, err := parseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive: %q", s)
	}
	return d, nil
}

// ParseDurationAllowZero is ParseDuration with zero allowed, for the flags
// where 0 means "unlimited".
func ParseDurationAllowZero(s string) (time.Duration, error) {
	return parseDuration(s)
}

func parseDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("invalid duration: %q (e.g. 90s, 30m, 1h, 2d)", s)
	}
	unit := time.Second
	digits := trimmed
	switch last := trimmed[len(trimmed)-1]; last {
	case 's', 'S':
		digits = trimmed[:len(trimmed)-1]
	case 'm', 'M':
		unit, digits = time.Minute, trimmed[:len(trimmed)-1]
	case 'h', 'H':
		unit, digits = time.Hour, trimmed[:len(trimmed)-1]
	case 'd', 'D':
		unit, digits = 24*time.Hour, trimmed[:len(trimmed)-1]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration: %q (e.g. 90s, 30m, 1h, 2d)", s)
	}
	// Bound the product before computing it: a wrapped time.Duration can come
	// back positive and small (5124096h wraps to ~25m), and a sign check on
	// the result would silently accept that.
	if int64(n) > math.MaxInt64/int64(unit) {
		return 0, fmt.Errorf("duration is too large: %q", s)
	}
	return time.Duration(n) * unit, nil
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
