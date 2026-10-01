// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"testing"
)

// A tree git does not manage and a git that failed on a tree it does look the
// same from the outside: no branch name, and nothing an operator can act on.
// The classification is what keeps a caller from rendering the second as the
// first — a launcher that showed a failing git as "this checkout" would report
// a clean tree nobody ever read.
func TestCurrentBranchSeparatesNonRepoFromFailure(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	if _, err := r.CurrentBranch(ctx); err != nil {
		t.Fatalf("CurrentBranch on a repository: %v", err)
	}

	// A detached HEAD is a state, not a failure, and keeps its own answer.
	head := gitOut(t, r.Dir, "rev-parse", "HEAD")
	gitIn(t, r.Dir, "checkout", "-q", head)
	if b, err := r.CurrentBranch(ctx); err != nil || b != "" {
		t.Fatalf("detached HEAD = (%q, %v), want an empty branch and no error", b, err)
	}

	// A directory git does not manage is ErrNotRepository, not a bare failure.
	_, err := Open(t.TempDir()).CurrentBranch(ctx)
	if !IsNotRepository(err) {
		t.Fatalf("CurrentBranch outside a repository = %v, want ErrNotRepository", err)
	}
}
