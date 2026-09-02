// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
