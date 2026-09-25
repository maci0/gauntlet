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

func TestMergeAbortsEvenWhenContextCancelled(t *testing.T) {
	r := newRepo(t)
	base, err := r.Tip(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(context.Background(), "conflicted", "run-1", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = wt.Remove(context.WithoutCancel(context.Background())) })
	if err := os.WriteFile(filepath.Join(wt.Dir, "file.txt"), []byte("from branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.CommitAll(context.Background(), "branch commit"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "file.txt"), []byte("from main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", "file.txt")
	gitIn(t, r.Dir, "commit", "-m", "main commit")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mr := r.Merge(ctx, wt.Branch, "try merge")
	if mr.Merged {
		t.Fatalf("canceled merge reported Merged: %+v", mr)
	}
	if _, err := r.run(context.Background(), gitNormal, "diff", "--cached", "--quiet"); err != nil {
		t.Fatalf("staged changes left behind after canceled merge: %v", err)
	}
	if _, err := r.run(context.Background(), gitNormal, "diff", "--quiet"); err != nil {
		t.Fatalf("unstaged changes left behind after canceled merge: %v", err)
	}
}

func TestValidateBranchName(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	valid := []string{"main", "feature/layer", "patch-1", "review-fix"}
	for _, name := range valid {
		if err := r.ValidateBranchName(ctx, name); err != nil {
			t.Errorf("ValidateBranchName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"-starts-with-dash",
		"head..tail",
		"bad~name",
		"bad^name",
		"bad:name",
		"bad?name",
		"bad*name",
		"bad[name",
		"bad@{name",
		"bad\\name",
		"",
		" ",
		"bad name",
	}
	for _, name := range invalid {
		if err := r.ValidateBranchName(ctx, name); err == nil {
			t.Errorf("ValidateBranchName(%q) accepted invalid branch name", name)
		}
	}
}

func TestCommitSubject(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	subject := "fix: test commit subject publication"
	if err := os.WriteFile(filepath.Join(r.Dir, "file.txt"), []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", "file.txt")
	gitIn(t, r.Dir, "commit", "-m", subject)

	got, err := r.CommitSubject(ctx, "HEAD")
	if err != nil {
		t.Fatalf("CommitSubject failed: %v", err)
	}
	if got != subject {
		t.Fatalf("CommitSubject = %q, want %q", got, subject)
	}

	if _, err := r.CommitSubject(ctx, "nonexistent-ref"); err == nil {
		t.Fatal("CommitSubject on nonexistent ref should return error")
	}
}

func TestParentTip(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	firstTip, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(r.Dir, "file2.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", "file2.txt")
	gitIn(t, r.Dir, "commit", "-m", "second commit")

	parent, err := r.ParentTip(ctx, "HEAD")
	if err != nil {
		t.Fatalf("ParentTip failed: %v", err)
	}
	if parent != firstTip {
		t.Fatalf("ParentTip = %q, want %q", parent, firstTip)
	}

	if _, err := r.ParentTip(ctx, firstTip); err == nil {
		t.Fatal("ParentTip on initial commit should return error")
	}
}

func TestPullRebaseAbortsOnConflict(t *testing.T) {
	origin := newRepo(t)
	cloneDir := t.TempDir()
	gitOut(t, cloneDir, "clone", origin.Dir, ".")
	gitIn(t, cloneDir, "config", "user.email", "test@example.invalid")
	gitIn(t, cloneDir, "config", "user.name", "test")
	r := Open(cloneDir)
	ctx := context.Background()

	// Advance origin with a change to main.go
	if err := os.WriteFile(filepath.Join(origin.Dir, "main.go"), []byte("package main\n\n// origin change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin.Dir, "commit", "-am", "origin edit")

	// Create a conflicting change in clone
	if err := os.WriteFile(filepath.Join(cloneDir, "main.go"), []byte("package main\n\n// clone change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, cloneDir, "commit", "-am", "clone edit")

	err := r.PullRebase(ctx)
	if err == nil {
		t.Fatal("PullRebase on conflicting branches should return error")
	}

	// Verify no rebase is left in progress
	out, statusErr := r.run(ctx, gitQuick, "status")
	if statusErr != nil {
		t.Fatalf("git status failed after rebase abort: %v", statusErr)
	}
	if strings.Contains(string(out), "rebase in progress") {
		t.Fatalf("repository left in rebase state after PullRebase error:\n%s", out)
	}
}

func TestPushBranchCanPushBranchAndFetchBranch(t *testing.T) {
	origin := newRepo(t)
	cloneDir := t.TempDir()
	gitOut(t, cloneDir, "clone", origin.Dir, ".")
	gitIn(t, cloneDir, "config", "user.email", "test@example.invalid")
	gitIn(t, cloneDir, "config", "user.name", "test")
	r := Open(cloneDir)
	ctx := context.Background()

	baseTip, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// CanPushBranch succeeds for valid commit to new branch
	if err := r.CanPushBranch(ctx, "origin", baseTip, "review/01-sec"); err != nil {
		t.Fatalf("CanPushBranch failed: %v", err)
	}
	// CanPushBranch fails for nonexistent commit
	if err := r.CanPushBranch(ctx, "origin", "nonexistent-source-ref", "review/01-sec"); err == nil {
		t.Fatal("CanPushBranch on nonexistent commit should return error")
	}
	// CanPushBranch fails for invalid remote
	if err := r.CanPushBranch(ctx, "bad-remote", baseTip, "review/01-sec"); err == nil {
		t.Fatal("CanPushBranch with invalid remote should return error")
	}

	// Create and push branch
	gitIn(t, cloneDir, "branch", "review/01-sec", baseTip)
	if err := r.PushBranch(ctx, "origin", "review/01-sec"); err != nil {
		t.Fatalf("PushBranch failed: %v", err)
	}

	// Verify remote has the branch
	remoteTip, found, err := r.RemoteBranchTip(ctx, "origin", "review/01-sec")
	if err != nil || !found || remoteTip != baseTip {
		t.Fatalf("RemoteBranchTip = %q, %v, %v; want %q, true, nil", remoteTip, found, err, baseTip)
	}

	// Fetch branch into a second clone that does not have it yet
	secondClone := t.TempDir()
	gitOut(t, secondClone, "clone", origin.Dir, ".")
	r2 := Open(secondClone)
	if err := r2.FetchBranch(ctx, "origin", "review/01-sec"); err != nil {
		t.Fatalf("FetchBranch failed: %v", err)
	}
	fetchedTip, err := r2.Tip(ctx, "refs/heads/review/01-sec")
	if err != nil || fetchedTip != baseTip {
		t.Fatalf("Tip after FetchBranch = %q, %v; want %q", fetchedTip, err, baseTip)
	}
}

func TestPush(t *testing.T) {
	origin := newRepo(t)
	gitIn(t, origin.Dir, "config", "receive.denyCurrentBranch", "ignore")
	cloneDir := t.TempDir()
	gitOut(t, cloneDir, "clone", origin.Dir, ".")
	gitIn(t, cloneDir, "config", "user.email", "test@example.invalid")
	gitIn(t, cloneDir, "config", "user.name", "test")
	r := Open(cloneDir)
	ctx := context.Background()

	// Commit a change in the clone
	if err := os.WriteFile(filepath.Join(cloneDir, "pushed.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, cloneDir, "add", "pushed.txt")
	gitIn(t, cloneDir, "commit", "-m", "push test commit")

	localTip, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Push(ctx); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	originTip := gitOut(t, origin.Dir, "rev-parse", "HEAD")
	if originTip != localTip {
		t.Fatalf("origin tip = %s, want %s after Push", originTip, localTip)
	}

	// Push with no upstream or unpushed branch without upstream fails
	gitIn(t, cloneDir, "checkout", "-b", "no-upstream")
	if err := r.Push(ctx); err == nil {
		t.Fatal("Push on branch without upstream should return error")
	}
}
