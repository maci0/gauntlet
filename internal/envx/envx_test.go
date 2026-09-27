// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package envx

import "testing"

// The list of values that mean off is what docs/CLI.md promises the operator,
// so it is pinned here rather than left to whichever call site reads it. A
// value that means on and a value nobody thought of both read as on: the
// operator set the variable deliberately, and ignoring it would leave a
// setting that looks applied and is not.
func TestOn(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true},
		{"on", true},
		{"disabled", true},
		{"anything else", true},
		{"", false},
		{" ", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{" False ", false},
		{"no", false},
		{"NO", false},
		{"off", false},
		{"OFF", false},
		{" off\t", false},
	}
	for _, c := range cases {
		if got := On(c.value); got != c.want {
			t.Errorf("On(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}
