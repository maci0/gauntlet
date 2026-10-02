// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"
	"time"
)

// The activity marker's n/a means no rate has been measured yet, and stops
// meaning that the moment an agent prints. A run that finished two reviews,
// whose lanes carry tens of thousands of tokens and whose feed has lines, sat
// next to a flat chart and a marker reading as if it had never run: the one
// reading on the screen that could not be reconciled with the rest of it.
func TestActivityMarkerReadsZeroOnceOutputArrives(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.activityTitle()); !strings.Contains(got, "n/a") {
		t.Fatalf("a run that has printed nothing reads %q, want n/a", got)
	}
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if got := stripANSI(m.activityTitle()); strings.Contains(got, "n/a") {
		t.Fatalf("a run that printed output still claims no measurement: %q", got)
	}
	// Output with no sample in the ring yet: the first tick may not have come,
	// and the first second of a run can hold no line at all. Zero is what
	// both measure, and an empty chart below already says the rest.
	m2 := newModel(demoConfig())
	m2.apply(demoEvents()[3])
	if got := stripANSI(m2.activityTitle()); strings.Contains(got, "n/a") {
		t.Fatalf("output with no sample yet reads %q, want a figure", got)
	}
}

// The grid is the run's roster and the only panel that says a review was
// scheduled at all. Its last row also carries the "+N more" cell, so the
// budget has to hold that row: it does not, and a run whose reviews exactly
// fill the panel drops its last one with no word that any review went missing.
func TestGridKeepsItsLastReviewForTheMoreCell(t *testing.T) {
	cfg := demoConfig()
	cfg.Reviews = append(cfg.Reviews, "ux-review", "i18n-review")
	m := newModel(cfg)
	m.w, m.h, m.ready = 104, 30, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	m.now = m.cfg.Started.Add(90 * time.Second)
	inner := 100
	cellW := m.reviewCellWidth()
	cols := max(inner/cellW, 1)
	rows := (len(m.order) + cols - 1) / cols
	_, _, gridH, _ := m.sectionHeights()
	if gridH < rows {
		t.Fatalf("the grid is budgeted %d rows for %d reviews, and its last row "+
			"also carries the count of whatever does not fit", gridH, rows)
	}
	full := stripANSI(m.renderGrid(inner, rows))
	if strings.Contains(full, "+") {
		t.Fatalf("a panel with room for every review announced a count it has none of:\n%s", full)
	}
	for _, name := range m.order {
		if !strings.Contains(full, reviewShort(name)) {
			t.Fatalf("the roster dropped %s:\n%s", name, full)
		}
	}
	// A cell is the unit the grid draws in, so the panel drops reviews one
	// cell at a time as the terminal narrows. Whatever it drops, the cell
	// that counts them has to be there: a review that leaves the screen with
	// no word that it did is the run's roster going quietly out of date.
	for w := cellW; w <= inner; w++ {
		drawn := stripANSI(m.renderGrid(w, rows))
		dropped := false
		for _, name := range m.order {
			if !strings.Contains(drawn, reviewShort(name)) {
				dropped = true
			}
		}
		if dropped && !strings.Contains(drawn, "more") {
			t.Fatalf("at %d columns the panel dropped reviews and named none:\n%s", w, drawn)
		}
		if strings.Contains(drawn, "more") && !dropped {
			t.Fatalf("at %d columns the panel announced a count it has none of:\n%s", w, drawn)
		}
	}
}
