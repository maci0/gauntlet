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

// commitFix writes fix.go into the worktree and commits it, the one review
// change every finish-path test needs before it has anything to merge, reset,
// or leave behind. It reports whether the commit had anything to record.
func commitFix(t *testing.T, ctx context.Context, wt *Worktree) bool {
	t.Helper()
	if err := os.WriteFile(filepath.Join(wt.Dir, "fix.go"),
		[]byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := wt.CommitAll(ctx, "sec-review: automated review fixes")
	if err != nil {
		t.Fatal(err)
	}
	return changed
}

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

	if !commitFix(t, ctx, wt) {
		t.Fatal("the first commit must report that something was committed")
	}
	one, err := r.Tip(ctx, wt.Branch)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := wt.CommitAll(ctx, "sec-review: automated review fixes")
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

func TestAdvanceDiscardsFailedEdits(t *testing.T) {
	for _, staged := range []bool{false, true} {
		name := "unstaged"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			r := newRepo(t)
			ctx := context.Background()
			base, err := r.Tip(ctx, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			wt, err := r.AddWorktree(ctx, "lane-0", "advance", base)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()
			branch := wt.Branch
			for _, path := range []string{"main.go", "scratch.go"} {
				if err := os.WriteFile(filepath.Join(wt.Dir, path), []byte("failed edit\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			sub := &Repo{Dir: wt.Dir}
			if staged {
				if _, err := sub.run(ctx, gitNormal, "add", "-A"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := wt.Advance(ctx, base); err != nil {
					t.Fatal(err)
				}
				changes, err := sub.Status(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(changes.Tracked) != 0 || len(changes.Untracked) != 0 {
					t.Fatalf("failed edits survived advancement: %+v", changes)
				}
				if wt.Branch != "" || wt.Base() != base {
					t.Fatalf("advanced worktree: branch=%q base=%q", wt.Branch, wt.Base())
				}
				if tip, err := sub.Tip(ctx, "HEAD"); err != nil || tip != base {
					t.Fatalf("HEAD=%q, want %q: %v", tip, base, err)
				}
				if current, err := sub.CurrentBranch(ctx); err != nil || current != "" {
					t.Fatalf("checkout is not detached: %q: %v", current, err)
				}
				if tip, err := r.Tip(ctx, branch); err != nil || tip != base {
					t.Fatalf("original branch moved: %q, want %q: %v", tip, base, err)
				}
			}
		})
	}
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

func TestRenameBranchTwiceConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "layer-0", "run-l1-stack", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	provisional := "review/01-sec-review-wip-abc123"
	if err := wt.StartBranch(ctx, provisional, base); err != nil {
		t.Fatal(err)
	}
	final := "review/01-sec-reviewer-topic"
	if err := wt.RenameBranch(ctx, final); err != nil {
		t.Fatal(err)
	}
	if wt.Branch != final {
		t.Fatalf("rename left Branch=%q, want %s", wt.Branch, final)
	}

	// A killed run that already renamed replays the rename: the worktree's
	// handle still carries the provisional name, and the second call has to
	// land on the state the first produced instead of failing on a missing
	// source branch.
	wt.Branch = provisional
	if err := wt.RenameBranch(ctx, final); err != nil {
		t.Fatalf("a repeated rename of the same pair must succeed: %v", err)
	}
	if wt.Branch != final {
		t.Fatalf("repeated rename left Branch=%q, want %s", wt.Branch, final)
	}
	if tip, err := r.Tip(ctx, final); err != nil || tip != base {
		t.Fatalf("repeated rename moved the branch: %q, %v", tip, err)
	}
	if _, err := r.Tip(ctx, "refs/heads/"+provisional); err == nil {
		t.Fatal("provisional branch survived the rename")
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
	commitFix(t, ctx, wt)
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

// CommitAll stages the whole checkout, so the scan a caller runs over "what
// will be committed" has to include the files the resolver touched outside the
// conflict, and the scope is what tells it those paths exist.
func TestCommitScopeCoversUnrelatedEdits(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(wt.Dir, "notes.md"),
		[]byte("<<<<<<< HEAD\nhalf-resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Dir, "scratch.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scope, err := wt.CommitScope(ctx)
	if err != nil {
		t.Fatalf("CommitScope: %v", err)
	}
	slices.Sort(scope)
	if want := []string{"notes.md", "scratch.md"}; !slices.Equal(scope, want) {
		t.Fatalf("CommitScope = %q, want %q", scope, want)
	}
	left, err := wt.Unresolved(ctx, scope)
	if err != nil {
		t.Fatalf("Unresolved: %v", err)
	}
	if !slices.Equal(left, []string{"notes.md"}) {
		t.Fatalf("Unresolved = %q, want the marked file in the commit scope", left)
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
	defer func() { _ = w.Remove(context.WithoutCancel(ctx)) }()
	if _, err := w.SquashIn(ctx, "refs/heads/does-not-exist"); err == nil {
		t.Fatal("merging a branch that does not exist should fail, not report a conflict")
	}
}

func TestCurrentBranchDistinguishesDetachedFromError(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	got, err := r.CurrentBranch(ctx)
	if err != nil || got != "main" {
		t.Fatalf("on a branch: got %q, %v; want main, nil", got, err)
	}

	gitIn(t, r.Dir, "checkout", "-q", "--detach")
	got, err = r.CurrentBranch(ctx)
	if err != nil || got != "" {
		t.Fatalf("detached HEAD: got %q, %v; want empty, nil", got, err)
	}

	outside := Open(t.TempDir())
	if _, err := outside.CurrentBranch(ctx); err == nil {
		t.Fatal("outside a repository CurrentBranch must fail, not report detached HEAD")
	}
}

func TestHasCommitDistinguishesMissingFromError(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	head, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	has, err := r.HasCommit(ctx, head)
	if err != nil || !has {
		t.Fatalf("HEAD should be present: has=%v err=%v", has, err)
	}

	missing := strings.Repeat("b", 40)
	has, err = r.HasCommit(ctx, missing)
	if err != nil || has {
		t.Fatalf("a missing object is false, nil; got has=%v err=%v", has, err)
	}

	outside := Open(t.TempDir())
	if _, err := outside.HasCommit(ctx, missing); err == nil {
		t.Fatal("outside a repository HasCommit must fail, not report the object missing")
	}
}

func TestDeleteBranchesMatching(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	gitIn(t, r.Dir, "branch", "gauntlet/run-lane-0")
	gitIn(t, r.Dir, "branch", "gauntlet/run-lane-1")
	gitIn(t, r.Dir, "branch", "other-branch")

	r.DeleteBranchesMatching(ctx, "gauntlet/run-lane*")

	branches := gitOut(t, r.Dir, "branch", "--list", "--format=%(refname:short)")
	if strings.Contains(branches, "gauntlet/run-lane") {
		t.Fatalf("matching branches should be deleted, got:\n%s", branches)
	}
	if !strings.Contains(branches, "other-branch") {
		t.Fatalf("unrelated branch should remain, got:\n%s", branches)
	}
}

// A sweep that deletes everything it listed reports success; one whose
// listing failed reports the failure instead of reporting a clean sweep that
// deleted nothing.
func TestDeleteBranchesMatchingReportsFailure(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	gitIn(t, r.Dir, "branch", "gauntlet/run-lane-0")

	if err := r.DeleteBranchesMatching(ctx, "gauntlet/run-lane*"); err != nil {
		t.Fatalf("a sweep that deleted every match must not report an error: %v", err)
	}

	// A directory that is not a repository cannot list its branches at all, so
	// the sweep deleted nothing: that has to reach the caller rather than
	// reading as a sweep that found nothing to do.
	notRepo := Open(filepath.Join(t.TempDir(), "not-a-repo"))
	err := notRepo.DeleteBranchesMatching(ctx, "gauntlet/run-lane*")
	if err == nil {
		t.Fatal("a sweep that could not list its pattern must report the failure")
	}
	if msg := err.Error(); !strings.Contains(msg, "list branches matching") {
		t.Fatalf("the failure must name the operation, got: %s", msg)
	}
}

func TestRemoveTwiceConverges(t *testing.T) {
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
	if err := wt.Remove(ctx); err != nil {
		t.Fatalf("first Remove failed: %v", err)
	}
	if err := wt.Remove(ctx); err != nil {
		t.Fatalf("second Remove on already-removed worktree failed: %v", err)
	}
}

func TestRemoveMissingDirConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-01", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(wt.Dir); err != nil {
		t.Fatal(err)
	}
	if err := wt.Remove(ctx); err != nil {
		t.Fatalf("Remove on deleted worktree dir failed: %v", err)
	}
}

func TestAddWorktreeConvergesOnOrphanedDir(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an unmanaged/orphaned directory left by a crashed run
	// where git metadata was pruned or never created.
	tag := "run-l1-02"
	slug := BranchSlug("sec-review")
	orphanedDir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), tag+"-"+slug)
	if err := os.MkdirAll(orphanedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanedDir, "leftover.txt"), []byte("debris\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt, err := r.AddWorktree(ctx, "sec-review", tag, base)
	if err != nil {
		t.Fatalf("AddWorktree failed on orphaned directory: %v", err)
	}
	defer func() { _ = wt.Remove(ctx) }()

	// The worktree checkout must be functional and clean.
	if _, err := os.Stat(filepath.Join(wt.Dir, "leftover.txt")); !os.IsNotExist(err) {
		t.Fatal("leftover file from crashed run should have been cleaned up")
	}
}

func TestRemovePrunedMetadataConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-03", base)
	if err != nil {
		t.Fatal(err)
	}
	dir := wt.Dir

	// Simulate worktree prune while dir remains (e.g. metadata removed manually or pruned).
	// We prune git's metadata by temporarily renaming dir, running prune, and renaming back.
	tmpDir := dir + "-tmp"
	if err := os.Rename(dir, tmpDir); err != nil {
		t.Fatal(err)
	}
	r.PruneWorktrees(ctx)
	if err := os.Rename(tmpDir, dir); err != nil {
		t.Fatal(err)
	}

	// wt.Remove must converge: remove the directory and succeed even though
	// git's worktree metadata is no longer present.
	if err := wt.Remove(ctx); err != nil {
		t.Fatalf("Remove on pruned metadata failed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir %s should be removed", dir)
	}
}

func TestRemovedWorktreeMethodsAreSafe(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-04", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Remove(ctx); err != nil {
		t.Fatal(err)
	}

	// All methods on removed worktree (with empty Dir) must return safe errors or no-op,
	// never touching the parent repository.
	if err := wt.ResetToBase(ctx); err != nil {
		t.Fatalf("ResetToBase on removed worktree failed: %v", err)
	}
	if err := wt.DiscardCurrent(ctx); err != nil {
		t.Fatalf("DiscardCurrent on removed worktree failed: %v", err)
	}
	if _, err := wt.CommitAll(ctx, "msg"); err == nil {
		t.Fatal("CommitAll on removed worktree should return error")
	}
	if err := wt.Advance(ctx, base); err == nil {
		t.Fatal("Advance on removed worktree should return error")
	}
	if _, err := wt.SquashIn(ctx, "branch"); err == nil {
		t.Fatal("SquashIn on removed worktree should return error")
	}
	if err := wt.StartBranch(ctx, "branch", base); err == nil {
		t.Fatal("StartBranch on removed worktree should return error")
	}
	if err := wt.RenameBranch(ctx, "new-name"); err == nil {
		t.Fatal("RenameBranch on removed worktree should return error")
	}
}

func TestCleanWorktreeRoot(t *testing.T) {
	r := newRepo(t)
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))

	// CleanWorktreeRoot when worktreeRoot does not exist is a safe no-op: the
	// call must not create the root or the .gauntlet parent it would remove.
	r.CleanWorktreeRoot()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("CleanWorktreeRoot created the missing root %s: %v", root, err)
	}
	if _, err := os.Stat(filepath.Dir(root)); !os.IsNotExist(err) {
		t.Fatalf("CleanWorktreeRoot created the missing parent %s: %v", filepath.Dir(root), err)
	}

	// When worktreeRoot is empty, CleanWorktreeRoot removes it and its parent .gauntlet
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	r.CleanWorktreeRoot()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("CleanWorktreeRoot failed to remove empty root %s", root)
	}
	gauntletDir := filepath.Dir(root)
	if _, err := os.Stat(gauntletDir); !os.IsNotExist(err) {
		t.Fatalf("CleanWorktreeRoot failed to remove empty parent %s", gauntletDir)
	}

	// When worktreeRoot contains remaining files, os.Remove fails safely and preserves them
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(root, "remaining.txt")
	if err := os.WriteFile(keepFile, []byte("preserve me"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.CleanWorktreeRoot()
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("CleanWorktreeRoot deleted non-empty root content: %v", err)
	}
}

// A killed run leaves its checkouts on disk, and CleanWorktreeRoot cannot
// remove them: os.Remove fails on a non-empty root, so one interrupted run
// would pin every checkout it left, each a full copy of the tree, for every
// run after it. The startup sweep is what reclaims them.
func TestSweepWorktreeRootReclaimsKilledRun(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Two checkouts from two loops, as a --jobs run cut before it was killed
	// would leave them, plus their branches still registered with git.
	for _, name := range []string{"run-l1-lane-0", "run-l2-lane-0"} {
		if _, err := r.AddWorktree(ctx, name, "tag", base); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 2 {
		t.Fatalf("test setup: want 2 checkouts under %s, got %d", root, len(dirs))
	}

	// CleanWorktreeRoot cannot: the root is not empty. This is the state a
	// killed run leaves, and the reason the sweep exists.
	r.CleanWorktreeRoot()
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("CleanWorktreeRoot removed a root holding checkouts: %v", err)
	}

	r.SweepWorktreeRoot(ctx)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(root)
		t.Fatalf("SweepWorktreeRoot left %d entries under %s: %v", len(entries), root, err)
	}
	if _, err := os.Stat(filepath.Dir(root)); !os.IsNotExist(err) {
		t.Fatalf("SweepWorktreeRoot left the %s parent behind: %v", filepath.Dir(root), err)
	}
	// The branches the checkouts were on are swept separately, by
	// DeleteBranchesMatching; the sweep only owns the disk.
	if out, err := r.run(ctx, gitNormal, "worktree", "list"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(out), worktreeRoot) {
		t.Fatalf("git still lists a checkout under %s: %s", worktreeRoot, out)
	}
}

// The root is inside the reviewed tree, so a symlink planted there by that
// tree must not be followed: sweeping it would delete whatever it points at.
func TestSweepWorktreeRootLeavesSymlink(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	canary := filepath.Join(target, "canary.txt")
	if err := os.WriteFile(canary, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "planted")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r.SweepWorktreeRoot(ctx)
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("SweepWorktreeRoot followed a symlink out of the root and deleted %s: %v", canary, err)
	}
}

// The threat model claims a symlinked scratch root is refused. The sweep is
// the one path that lists and deletes everything under that root, so it has to
// make the same proof: a planted `.gauntlet/worktrees` link must not turn a
// sweep into a recursive delete of whatever it points at.
func TestSweepWorktreeRootRefusesSymlinkedRoot(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	canary := filepath.Join(target, "canary.txt")
	if err := os.WriteFile(canary, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory of checkouts the planted root would otherwise expose.
	victim := filepath.Join(target, "checkout")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	r.SweepWorktreeRoot(ctx)
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("SweepWorktreeRoot followed a symlinked root and deleted %s: %v", canary, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("SweepWorktreeRoot followed a symlinked root and deleted %s: %v", victim, err)
	}
}

// A missing root is the normal first run: the sweep must not create one.
func TestSweepWorktreeRootMissingRoot(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	r.SweepWorktreeRoot(ctx)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("SweepWorktreeRoot created the missing root %s: %v", root, err)
	}
	if _, err := os.Stat(filepath.Dir(root)); !os.IsNotExist(err) {
		t.Fatalf("SweepWorktreeRoot created the missing parent %s: %v", filepath.Dir(root), err)
	}
}

// A checkout is a second copy of the reviewed repository, and the reviewed
// repository may be private. The machine may have more than one local account,
// so every directory holding one is left readable by its owner only.
func TestWorktreeDirsAreOwnerOnly(t *testing.T) {
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

	snap, err := r.AddSnapshotWorktree(ctx, "run-l1-00", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Remove(context.WithoutCancel(ctx)) }()

	for _, dir := range []string{
		wt.Dir,
		snap.Dir,
		r.worktreeRootDir(),
		filepath.Dir(r.worktreeRootDir()),
	} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != ownerOnly {
			t.Errorf("%s is %#o, want %#o: another local user could read the checkout",
				dir, got, ownerOnly)
		}
	}
}

// The handle that runs git inside a checkout is built with the checkout, not
// on first use: a Worktree is owned by one lane goroutine, and a lazily filled
// field is a check-then-act that a second goroutine would turn into a race.
// Remove retires it with the directory.
func TestSubRepoIsBuiltWithTheWorktree(t *testing.T) {
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
	sub := wt.subRepo()
	if sub == nil {
		t.Fatal("a fresh worktree must have its checkout handle ready")
	}
	if sub != wt.subRepo() {
		t.Fatal("the checkout handle must be the same one on every call")
	}
	if sub.Dir != wt.Dir {
		t.Fatalf("checkout handle runs in %q, want %q", sub.Dir, wt.Dir)
	}
	if err := wt.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if wt.subRepo() != nil {
		t.Fatal("a removed worktree must have no checkout handle")
	}
}
