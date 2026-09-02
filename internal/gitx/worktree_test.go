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

func TestMergeTwiceLandsOnce(t *testing.T) {
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
	if _, err := wt.CommitAll(ctx, "sec-review: automated review fixes"); err != nil {
		t.Fatal(err)
	}

	mr := r.Merge(ctx, wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("first merge failed: %+v", mr)
	}
	afterFirst := gitOut(t, r.Dir, "rev-parse", "HEAD")
	commits := gitOut(t, r.Dir, "rev-list", "--count", base+"..HEAD")

	mr = r.Merge(ctx, wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("merging an already-landed branch must succeed as a no-op, got: %+v", mr)
	}
	if got := gitOut(t, r.Dir, "rev-parse", "HEAD"); got != afterFirst {
		t.Fatalf("a repeated merge added commits: %s != %s", got, afterFirst)
	}
	if got := gitOut(t, r.Dir, "rev-list", "--count", base+"..HEAD"); got != commits {
		t.Fatalf("a repeated merge changed the history length: %s != %s", got, commits)
	}
}

func TestMergeIntoTwiceConverges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "branch", "main-line", base)

	wt, err := r.AddWorktree(ctx, "sec-review", "run-l1-00", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()

	if err := os.WriteFile(filepath.Join(wt.Dir, "fix.go"),
		[]byte("package fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.CommitAll(ctx, "sec-review: automated review fixes"); err != nil {
		t.Fatal(err)
	}

	mr := r.MergeInto(ctx, "main-line", wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("first merge into main-line failed: %+v", mr)
	}
	afterFirst := gitOut(t, r.Dir, "rev-parse", "refs/heads/main-line")
	commits := gitOut(t, r.Dir, "rev-list", "--count", base+"..refs/heads/main-line")

	mr = r.MergeInto(ctx, "main-line", wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("a repeat merge of unchanged work must succeed as a no-op, got: %+v", mr)
	}
	if got := gitOut(t, r.Dir, "rev-parse", "refs/heads/main-line"); got != afterFirst {
		t.Fatalf("a repeated merge moved main-line: %s != %s", got, afterFirst)
	}
	if got := gitOut(t, r.Dir, "rev-list", "--count", base+"..refs/heads/main-line"); got != commits {
		t.Fatalf("a repeated merge stacked commits on main-line: %s != %s", got, commits)
	}
}

// SquashIn is the conflict step's first move: it must land what merges and
// name what does not, without either state escaping into the main tree.
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

func TestMergeConflictDetailNamesTheConflict(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "branch", "main-line", base)

	write := func(name, body string) *Worktree {
		t.Helper()
		wt, err := r.AddWorktree(ctx, name, name, base)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = wt.Remove(context.WithoutCancel(ctx)) })
		if err := os.WriteFile(filepath.Join(wt.Dir, "main.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.CommitAll(ctx, "change"); err != nil {
			t.Fatal(err)
		}
		return wt
	}
	first := write("a-review", "package main\n\nfunc main() { a() }\n")
	second := write("b-review", "package main\n\nfunc main() { b() }\n")

	if mr := r.Merge(ctx, first.Branch, "land a"); !mr.Merged {
		t.Fatalf("first merge failed: %+v", mr)
	}
	got := r.Merge(ctx, second.Branch, "land b")
	if !got.Conflict || got.Merged {
		t.Fatalf("second merge = %+v, want a conflict", got)
	}
	if got.Detail == "" || strings.Contains(got.Detail, "\n") {
		t.Fatalf("Merge Detail = %q, want one line", got.Detail)
	}
	if !strings.HasPrefix(got.Detail, "CONFLICT") {
		t.Fatalf("Merge Detail = %q, want the CONFLICT line, not git's Auto-merging prefix", got.Detail)
	}

	if mr := r.MergeInto(ctx, "main-line", first.Branch, "land a"); !mr.Merged {
		t.Fatalf("first merge into main-line failed: %+v", mr)
	}
	into := r.MergeInto(ctx, "main-line", second.Branch, "land b")
	if !into.Conflict || into.Merged {
		t.Fatalf("MergeInto = %+v, want a conflict", into)
	}
	if into.Detail == "" || strings.Contains(into.Detail, "\n") {
		t.Fatalf("MergeInto Detail = %q, want one line", into.Detail)
	}
	if !strings.HasPrefix(into.Detail, "CONFLICT") {
		t.Fatalf("MergeInto Detail = %q, want the CONFLICT line", into.Detail)
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
