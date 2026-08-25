// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/prompt"
)

// Doctor assembles its review catalog from agent.ReviewTools and
// agent.ReviewsWithoutTools, while the reviews themselves are defined by
// internal/prompt's embedded prompts. Nothing in the type system ties the two
// together: a new review prompt that skips the tool table would silently
// vanish from doctor, and a stale name would show a phantom row or advertise
// tools for a review that no longer exists.
func TestDoctorReviewCatalogMatchesTheBundledReviews(t *testing.T) {
	bundled := make(map[string]bool)
	for _, n := range prompt.BundledNames() {
		bundled[n] = true
	}

	cataloged := make(map[string]string) // review -> which list named it
	note := func(name, list string) {
		if !bundled[name] {
			t.Errorf("%s lists %q, which is not a bundled review", list, name)
			return
		}
		if prev := cataloged[name]; prev != "" {
			t.Errorf("%q appears in both %s and %s; each bundled review is listed exactly once", name, prev, list)
		}
		cataloged[name] = list
	}
	for name := range agent.ReviewTools {
		note(name, "agent.ReviewTools")
	}
	for _, name := range agent.ReviewsWithoutTools {
		note(name, "agent.ReviewsWithoutTools")
	}

	var missing []string
	for _, n := range prompt.BundledNames() {
		if cataloged[n] == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("bundled reviews absent from doctor's catalog (add them to agent.ReviewTools or agent.ReviewsWithoutTools): %s",
			strings.Join(missing, ", "))
	}
}
