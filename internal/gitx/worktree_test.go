// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The finish path runs under retries, hot reloads, and loop after loop, so
// each step must survive being executed twice: a second pass leaves the same
// state the first one did. These tests pin that, because the property is what
// lets the runner treat "did this already happen?" as a question git answers.

func TestCommitAllTwiceCommitsOnce(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-00", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	if err := os.WriteFile(filepath.Join(wt.Dir, "fix.go"),
		[]byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := wt.CommitAll(ctx, "sec-review: automated review fixes")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the first commit must report that something was committed")
	}
	one, err := r.Tip(ctx, wt.Branch)
	if err != nil {
		t.Fatal(err)
	}

	changed, err = wt.CommitAll(ctx, "sec-review: automated review fixes")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("a second commit with nothing staged must report false")
	}
	two, err := r.Tip(ctx, wt.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("a repeated CommitAll moved the branch: %s != %s", one, two)
	}
}

func TestResetToBaseRestoresAndConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-00", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	// What a failed attempt leaves behind: an edited tracked file, a staged
	// tracked file, and an untracked scratch file.
	if err := os.WriteFile(filepath.Join(wt.Dir, "fix.go"),
		[]byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := &Repo{Dir: wt.Dir}
	if _, err := sub.run(ctx, 30*time.Second, "add", "-A"); err != nil {
		t.Fatal(err)
	}

	if err := wt.ResetToBase(ctx); err != nil {
		t.Fatal(err)
	}
	assertWorktreeMatchesBase(t, ctx, r, wt, base)

	// A second reset over an already-restored checkout must be a no-op, not
	// an error: the retry path calls it before every attempt.
	if err := wt.ResetToBase(ctx); err != nil {
		t.Fatalf("a repeated ResetToBase must succeed: %v", err)
	}
	assertWorktreeMatchesBase(t, ctx, r, wt, base)
}

func TestStartBranchTwiceConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "lane-0", "run-l1-lane0", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	branch := "gauntlet/run-l1-lane0-00/sec-review"
	if err := wt.StartBranch(ctx, branch, base); err != nil {
		t.Fatal(err)
	}
	if wt.Branch != branch {
		t.Fatalf("StartBranch left Branch=%q, want %s", wt.Branch, branch)
	}
	one, err := r.Tip(ctx, branch)
	if err != nil {
		t.Fatal(err)
	}
	if one != base {
		t.Fatalf("new branch is at %s, want base %s", one, base)
	}

	if err := wt.StartBranch(ctx, branch, base); err != nil {
		t.Fatalf("a repeated StartBranch on the same empty branch must succeed: %v", err)
	}
	two, err := r.Tip(ctx, branch)
	if err != nil {
		t.Fatal(err)
	}
	if two != one {
		t.Fatalf("a repeated StartBranch moved the branch: %s != %s", two, one)
	}

	// Detached, the branch still exists at base: a killed attempt leaves that,
	// and the next StartBranch must check it out rather than fail on switch -c.
	if err := wt.Advance(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := wt.StartBranch(ctx, branch, base); err != nil {
		t.Fatalf("StartBranch on a leftover empty branch must succeed: %v", err)
	}
	if wt.Branch != branch {
		t.Fatalf("recovered StartBranch left Branch=%q, want %s", wt.Branch, branch)
	}
	three, err := r.Tip(ctx, branch)
	if err != nil {
		t.Fatal(err)
	}
	if three != base {
		t.Fatalf("recovered branch moved: %s != %s", three, base)
	}
}

func TestStartBranchRejectsExistingWork(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "lane-0", "run-l1-lane0", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	branch := "gauntlet/run-l1-lane0-00/sec-review"
	if err := wt.StartBranch(ctx, branch, base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Dir, "fix.go"),
		[]byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.CommitAll(ctx, "sec-review: automated review fixes"); err != nil {
		t.Fatal(err)
	}
	kept, err := r.Tip(ctx, branch)
	if err != nil {
		t.Fatal(err)
	}

	if err := wt.StartBranch(ctx, branch, base); err == nil {
		t.Fatal("StartBranch over a branch with commits must fail")
	} else if !strings.Contains(err.Error(), "already exists at") {
		t.Fatalf("leftover-work error = %v, want it to name both tips", err)
	}
	if got, err := r.Tip(ctx, branch); err != nil || got != kept {
		t.Fatalf("the kept branch was modified: %s != %s (%v)", got, kept, err)
	}
}

func assertWorktreeMatchesBase(t *testing.T, ctx context.Context, r *Repo, wt *Worktree, base string) {
	t.Helper()
	sub := &Repo{Dir: wt.Dir}
	changes, err := sub.Status(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Tracked) > 0 || len(changes.Untracked) > 0 {
		t.Fatalf("the checkout is not back to base: tracked=%v untracked=%v",
			changes.Tracked, changes.Untracked)
	}
	tip, err := r.Tip(ctx, wt.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if tip != base {
		t.Fatalf("the branch moved during the review: %s != %s", tip, base)
	}
}

func TestSquashInNamesTheConflictedPaths(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Two checkouts of the same base rewrite the same line differently.
	write := func(w *Worktree, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(w.Dir, "main.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := w.CommitAll(ctx, "change"); err != nil {
			t.Fatal(err)
		}
	}
	first, err := r.AddWorktree(ctx, "a-review", "t1", base)
	if err != nil {
		t.Fatal(err)
	}
	write(first, "package main\n\nfunc main() { a() }\n")
	second, err := r.AddWorktree(ctx, "b-review", "t2", base)
	if err != nil {
		t.Fatal(err)
	}
	write(second, "package main\n\nfunc main() { b() }\n")

	// A conflicted non-ASCII filename must come back with its bytes intact:
	// git's default core.quotePath turns é into C escapes, a form neither
	// Unresolved nor the conflict prompt can match to the real file.
	cafe := filepath.Join(first.Dir, "café-notes.md")
	if err := os.WriteFile(cafe, []byte("from first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := first.CommitAll(ctx, "café change"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second.Dir, "café-notes.md"), []byte("from second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := second.CommitAll(ctx, "café counterchange"); err != nil {
		t.Fatal(err)
	}

	paths, err := second.SquashIn(ctx, first.Branch)
	if err != nil {
		t.Fatalf("SquashIn: %v", err)
	}
	slices.Sort(paths)
	wantPaths := []string{"café-notes.md", "main.go"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("conflicted paths = %q, want %q", paths, wantPaths)
	}
	left, err := second.Unresolved(ctx, paths)
	if err != nil {
		t.Fatalf("Unresolved: %v", err)
	}
	if len(left) != 2 {
		t.Fatalf("Unresolved = %q, want both files that still carry markers", left)
	}

	// Resolving means the markers are gone, whoever removed them.
	if err := os.WriteFile(filepath.Join(second.Dir, "café-notes.md"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(second, "package main\n\nfunc main() { a(); b() }\n")
	left, err = second.Unresolved(ctx, paths)
	if err != nil {
		t.Fatalf("Unresolved: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("Unresolved = %q after the markers were removed", left)
	}
	// A path that no longer exists counts as resolved: deleting the file is a
	// valid answer to a delete/modify conflict.
	left, err = second.Unresolved(ctx, []string{"gone.go"})
	if err != nil {
		t.Fatalf("Unresolved: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("Unresolved = %q for a file that is not there", left)
	}
}

// A path that cannot be read proves nothing either way. Silently reading it
// as "resolved" is how markers reach history behind a permissions problem, so
// the scan must fail instead and the caller must keep the branch.
func TestUnresolvedFailsOnAnUnreadablePath(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "a-review", "t1", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	// A directory named like a conflicted file is readable by nobody:
	// os.ReadFile fails with something other than not-exist.
	if err := os.Mkdir(filepath.Join(wt.Dir, "blocked.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	left, err := wt.Unresolved(ctx, []string{"blocked.go"})
	if err == nil {
		t.Fatal("Unresolved reported success for a path it could not read")
	}
	if len(left) != 0 {
		t.Fatalf("Unresolved = %q along with the error; want nothing claimed either way", left)
	}
}

func TestSquashInReportsAMergeNobodyCanResolve(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.AddWorktree(ctx, "a-review", "t1", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.SquashIn(ctx, "refs/heads/does-not-exist"); err == nil {
		t.Fatal("merging a branch that does not exist should fail, not report a conflict")
	}
}
