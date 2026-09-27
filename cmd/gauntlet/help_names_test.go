// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Reading a help row's flag names is what the drift test does with them, and
// nothing else does at all, so the accessor lives with its one caller.
func (f flagDoc) names() []string {
	out := []string{f.Long}
	if f.Short != "" {
		out = append(out, f.Short)
	}
	return out
}
