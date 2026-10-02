// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maci0/gauntlet/internal/normalize"
)

// Narrowing the feed to a run that has produced nothing yet must say so. The
// filter is cached, so a view taken over a feed that has since filled with
// lines this filter drops reads as a cache of matches: the panel then reports
// a match over a line the filter is there to hide, and points the reader at
// f for a panel showing exactly what it hides.
func TestFeedFilterDoesNotShowLinesItHides(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 40, true
	m.filter = feedSignal
	m.feed = []feedLine{{text: "reading main.go", kind: normalize.Plain}}
	if got := m.visibleFeed(); len(got) != 0 {
		t.Fatalf("the filter kept narration: %+v", got)
	}
	if got := stripANSI(m.renderFeed(90, 6)); !strings.Contains(got, "nothing matches") {
		t.Fatalf("a feed with nothing under the filter drew no notice:\n%s", got)
	}
	// A second line the filter also drops leaves the notice standing: the one
	// moment a cached view could still be showing the old result.
	m.pushFeed(feedLine{text: "still reading", kind: normalize.Plain})
	if got := stripANSI(m.renderFeed(90, 6)); !strings.Contains(got, "nothing matches") {
		t.Fatalf("a stale view drew a line the filter hides:\n%s", got)
	}
	// A line the filter keeps is a match, and the notice has to go.
	m.pushFeed(feedLine{text: "RESULT: done", kind: normalize.Result})
	got := stripANSI(m.renderFeed(90, 6))
	if strings.Contains(got, "nothing matches") || !strings.Contains(got, "RESULT: done") {
		t.Fatalf("a matching line is not drawn:\n%s", got)
	}
	// Pressing the key that the notice names brings the dropped lines back.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if got := stripANSI(m.renderFeed(90, 6)); strings.Contains(got, "nothing matches") {
		t.Fatalf("widening left the notice up:\n%s", got)
	}
}
