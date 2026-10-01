// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"slices"
	"testing"
)

// The two branch listings answer different questions and must not be swapped.
// Branches is a merge-target list, so it hides both of the tool's own
// namespaces. LaneBranches is the opposite: it names the lane branches a merge
// refused to land, because those commits exist nowhere else -- the journal
// records the branch name and nothing about what is on it. Listing the wrong
// set here either hides work the operator has to recover by hand or sends
// somebody to bundle up a stack the remote already holds.
func TestLaneBranchesNamesOnlyUnlandedLaneWork(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	// A run that kept two lanes after a merge that did not land.
	gitIn(t, r.Dir, "branch", LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"))
	gitIn(t, r.Dir, "branch", LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"))
	// A stacked layer is published as a pull request, so the remote holds it
	// and it is not work a bundle has to carry.
	gitIn(t, r.Dir, "branch", StackBranchPrefix+"01-a-review")
	// The operator's own branches are not review output at all.
	gitIn(t, r.Dir, "branch", "feature/keep-me")

	got := r.LaneBranches(ctx)
	want := []string{
		LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"),
		LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"),
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("LaneBranches = %q, want exactly the lane branches %q", got, want)
	}

	// A repository whose reviews all landed holds no lane branch, so the list
	// is empty rather than nil-with-an-error: doctor reports on the machine
	// and a clean repository has nothing to say.
	gitIn(t, r.Dir, "branch", "-D", LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"))
	gitIn(t, r.Dir, "branch", "-D", LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"))
	if got := r.LaneBranches(ctx); len(got) != 0 {
		t.Fatalf("LaneBranches after every lane landed = %q, want none", got)
	}

	// A stack layer alone is still not unlanded work.
	gitIn(t, r.Dir, "branch", LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"))
	gitIn(t, r.Dir, "branch", StackBranchPrefix+"02-b-review")
	got = r.LaneBranches(ctx)
	if len(got) != 1 || got[0] != LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security") {
		t.Fatalf("LaneBranches = %q, want only the lane branch", got)
	}
}

// A directory that is not a repository is not a failure here. Doctor reports
// on the machine it was started in, and the listing is a line in a report
// about something else: a broken or absent repository has no branches to
// name, and raising would take the whole report down over it.
func TestLaneBranchesIsQuietWithoutARepository(t *testing.T) {
	if got := Open(t.TempDir()).LaneBranches(context.Background()); got != nil {
		t.Fatalf("LaneBranches outside a repository = %q, want nil", got)
	}
	if got := (*Repo)(nil).LaneBranches(context.Background()); got != nil {
		t.Fatalf("LaneBranches on a nil repo = %q, want nil", got)
	}
}
