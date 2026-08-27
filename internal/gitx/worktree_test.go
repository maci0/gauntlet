// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = r.Dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
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
	afterFirst := git("rev-parse", "HEAD")
	commits := git("rev-list", "--count", base+"..HEAD")

	mr = r.Merge(ctx, wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("merging an already-landed branch must succeed as a no-op, got: %+v", mr)
	}
	if got := git("rev-parse", "HEAD"); got != afterFirst {
		t.Fatalf("a repeated merge added commits: %s != %s", got, afterFirst)
	}
	if got := git("rev-list", "--count", base+"..HEAD"); got != commits {
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
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = r.Dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("branch", "main-line", base)

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
	afterFirst := git("rev-parse", "refs/heads/main-line")
	commits := git("rev-list", "--count", base+"..refs/heads/main-line")

	mr = r.MergeInto(ctx, "main-line", wt.Branch, "Merge sec-review from gauntlet run run")
	if !mr.Merged {
		t.Fatalf("a repeat merge of unchanged work must succeed as a no-op, got: %+v", mr)
	}
	if got := git("rev-parse", "refs/heads/main-line"); got != afterFirst {
		t.Fatalf("a repeated merge moved main-line: %s != %s", got, afterFirst)
	}
	if got := git("rev-list", "--count", base+"..refs/heads/main-line"); got != commits {
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

	paths, err := second.SquashIn(ctx, first.Branch)
	if err != nil {
		t.Fatalf("SquashIn: %v", err)
	}
	if len(paths) != 1 || paths[0] != "main.go" {
		t.Fatalf("conflicted paths = %q, want [main.go]", paths)
	}
	left, err := second.Unresolved(ctx, paths)
	if err != nil {
		t.Fatalf("Unresolved: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("Unresolved = %q, want the file that still has markers", left)
	}

	// Resolving means the markers are gone, whoever removed them.
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
