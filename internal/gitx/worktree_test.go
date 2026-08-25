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
