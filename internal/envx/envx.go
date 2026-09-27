// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package envx reads the boolean environment variables gauntlet documents.
//
// Five consumer-facing variables mean "on" or "off" by their value rather
// than by their presence: CLICOLOR_FORCE, FORCE_COLOR, GAUNTLET_NO_ANIMATION,
// NO_MOTION, and REDUCED_MOTION. They are read from two packages, the plain
// reporter in cmd/gauntlet and the dashboard in internal/ui, and each held its
// own copy of the list of values that mean off. docs/CLI.md states that list
// once for all of them, so two copies of the rule are two places to edit when
// it changes, and one of them answers differently the moment somebody forgets.
// Both read it here.
package envx

import "strings"

// offValues are the values that mean "off" for a documented boolean variable.
// Anything else, including a value that is not a boolean at all, means "on":
// the variable is an opt-in the operator sets deliberately, and refusing to
// read it would silently ignore a setting someone typed.
func off(value string) bool {
	switch value {
	case "", "0", "false", "no", "off":
		return true
	}
	return false
}

// On reports whether a documented boolean variable's value means on.
//
// The value is trimmed and case-folded first: a value that arrived in another
// case, or with a trailing space, is the same answer, and every one of these
// variables is read in whatever environment a wrapper script or a CI runner
// set up. NO_COLOR is not one of them and is not routed here: it counts at any
// value, empty included (no-color.org).
func On(value string) bool {
	return !off(strings.ToLower(strings.TrimSpace(value)))
}
