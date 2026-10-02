// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// A key row too narrow for one of its segments used to fit none of them: the
// reader was left with the fit marker, and a page of help whose keys all work
// and none of which are named reads as a page whose keys are broken.
func TestHelpLegendNamesItsKeysOnANarrowRow(t *testing.T) {
	for _, w := range []int{14, 16, 20, 30} {
		legend := stripANSI(helpLegend(w, ""))
		if got := lipgloss.Width(legend); got > w {
			t.Fatalf("at %d columns the key row is %d wide: %q", w, got, legend)
		}
		if !strings.Contains(legend, "q/esc close") {
			t.Fatalf("at %d columns the key row names no way out: %q", w, legend)
		}
		if !strings.Contains(legend, "…") {
			t.Fatalf("at %d columns the key row is cut without saying so: %q", w, legend)
		}
	}
	// A row with room for exactly one segment keeps it whole and marks the
	// rest; one too narrow for any of them lays the keys out as a single
	// string. Either way the row names keys that work.
	if one := stripANSI(helpLegend(11, "")); one != "q/esc close" {
		t.Fatalf("a row with room for one key names %q", one)
	}
	// Wide enough for the segments, the row stays segmented, and the keys are
	// the same ones either way.
	for _, key := range []string{"j/k scroll", "pgup/pgdn", "space/b", "g/G"} {
		if !strings.Contains(stripANSI(helpLegend(80, "")), key) {
			t.Errorf("the key row does not name %s", key)
		}
	}
}
