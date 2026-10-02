// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/runner"
)

// A branch left for a human is the one thing on the fallback screen that has
// to be acted on outside it, and the fallback is all a reader in a small
// terminal ever sees. It named the branches but not how many, and nothing on
// it said the line is cut: past the bound a conflict was a branch a person
// could not find.
func TestMinimalScreenCountsTheUnmergedBranches(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 50, 10, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	got := stripANSI(m.renderMinimal())
	if !strings.Contains(got, "unmerged: 1,") {
		t.Fatalf("the fallback does not count the branches:\n%s", got)
	}
	// The review and its branch, which is the pair a person merges from.
	if !strings.Contains(got, "sec (gauntlet/x/sec-review)") {
		t.Fatalf("the fallback does not say which branch to merge:\n%s", got)
	}
	total := maxConflicts + 3
	for i := range total {
		m.apply(runner.Event{Kind: runner.EvMerge, Review: fmt.Sprintf("review-%02d", i),
			Branch: fmt.Sprintf("gauntlet/x/review-%02d", i), Status: runner.StatusConflict})
	}
	short := stripANSI(m.renderMinimal())
	if !strings.Contains(short, fmt.Sprintf("unmerged: %d,", total+1)) {
		t.Fatalf("the fallback undercounts the branches:\n%s", short)
	}
	if !strings.Contains(short, "older") {
		t.Fatalf("the fallback does not say the list is cut short:\n%s", short)
	}
}
