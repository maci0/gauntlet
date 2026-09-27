// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// gitIn runs one git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = gitOut(t, dir, args...)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// A reviewed repository's own .git/config is untrusted, and core.sshCommand
// is a command line git executes for every ssh remote operation. Remote
// inspection and fetching must never run it.
func TestRepoLocalSSHCommandNeverExecutes(t *testing.T) {
	r := newRepo(t)
	// The override must be what production ships, not whatever the test
	// machine happens to export.
	t.Setenv("GIT_SSH_COMMAND", "placeholder")
	os.Unsetenv("GIT_SSH_COMMAND")

	marker := filepath.Join(t.TempDir(), "pwned")
	script := filepath.Join(t.TempDir(), "evil-ssh")
	if err := os.WriteFile(script,
		[]byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "config", "core.sshCommand", script)
	gitIn(t, r.Dir, "remote", "add", "origin", "git@host.invalid:owner/repo.git")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := r.RemoteBranchTip(ctx, "origin", "main"); err == nil {
		t.Fatal("ls-remote against host.invalid somehow succeeded")
	}
	if _, err := r.FetchRemoteBranchTip(ctx, "origin", "main"); err == nil {
		t.Fatal("fetch from host.invalid somehow succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("repository-local core.sshCommand executed during a remote operation")
	}
}

// An ext:: remote URL is itself a command line. No gauntlet operation uses
// external transport helpers, so it is refused outright.
func TestExtTransportRemoteIsRefused(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	gitIn(t, r.Dir, "remote", "add", "evil", "ext::sh -c \"touch "+marker+"\"")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := r.RemoteBranchTip(ctx, "evil", "main"); err == nil {
		t.Fatal("ext:: remote was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("ext:: remote executed its command")
	}
}

// A remote URL may carry userinfo (https://user:pass@host/...). Git repeats
// that URL on stderr when the fetch fails, and the runner journals the error,
// so the account name and password must not survive the wrap.
func TestGitErrorRedactsRemoteUserinfo(t *testing.T) {
	r := newRepo(t)
	gitIn(t, r.Dir, "remote", "add", "origin",
		"https://alice:s3cret@127.0.0.1:1/owner/repo.git")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := r.FetchRemoteBranchTip(ctx, "origin", "main")
	if err == nil {
		t.Fatal("fetch of 127.0.0.1:1 succeeded")
	}
	msg := err.Error()
	if strings.Contains(msg, "s3cret") || strings.Contains(msg, "alice:") ||
		strings.Contains(msg, "alice@") {
		t.Fatalf("git error leaked remote userinfo: %q", msg)
	}
}

// A hostile tree can plant .gauntlet (or worktrees below it) as a symlink
// pointing anywhere. Worktree creation must refuse to follow it rather than
// write or force-remove through it.
func TestWorktreeRootSymlinkEscapeIsRefused(t *testing.T) {
	ctx := context.Background()
	for _, plant := range []string{".gauntlet", ".gauntlet/worktrees"} {
		r := newRepo(t)
		outside := t.TempDir()
		link := filepath.Join(r.Dir, filepath.FromSlash(plant))
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		base, err := r.Tip(ctx, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.AddStackWorktree(ctx, "gauntlet/stack/x/01-a", "tag", base); err == nil {
			t.Fatalf("planted %s symlink was followed by AddStackWorktree", plant)
		}
		if _, err := r.AddWorktree(ctx, "sec-review", "tag-l1-00", base); err == nil {
			t.Fatalf("planted %s symlink was followed by AddWorktree", plant)
		}
		if _, err := r.AddSnapshotWorktree(ctx, "tag", base); err == nil {
			t.Fatalf("planted %s symlink was followed by AddSnapshotWorktree", plant)
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatalf("worktree escaped the repository through %s: %v entries, %v", plant, entries, err)
		}
	}
}

// A hostile clone can put a smudge filter in .git/config and name it from
// .gitattributes. worktree add checks files out, so without the empty
// attr.tree overlay that filter would run in this process before any agent
// starts.
func TestSmudgeFilterDoesNotRunOnWorktreeAdd(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	gitIn(t, r.Dir, "config", "filter.evil.smudge", "touch "+marker)
	gitIn(t, r.Dir, "config", "filter.evil.clean", "cat")
	if err := os.WriteFile(filepath.Join(r.Dir, ".gitattributes"),
		[]byte("* filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", ".gitattributes")
	gitIn(t, r.Dir, "commit", "-qm", "attrs")
	r = Open(r.Dir)

	ctx := context.Background()
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, add := range []struct {
		name string
		fn   func() (*Worktree, error)
	}{
		{"AddWorktree", func() (*Worktree, error) {
			return r.AddWorktree(ctx, "sec-review", "tag-l1-00", base)
		}},
		{"AddStackWorktree", func() (*Worktree, error) {
			return r.AddStackWorktree(ctx, "gauntlet/stack/x/01-a", "tag", base)
		}},
		{"AddSnapshotWorktree", func() (*Worktree, error) {
			return r.AddSnapshotWorktree(ctx, "snap", base)
		}},
	} {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("smudge filter already ran before %s", add.name)
		}
		wt, err := add.fn()
		if err != nil {
			t.Fatalf("%s: %v", add.name, err)
		}
		if _, err := os.Stat(filepath.Join(wt.Dir, "main.go")); err != nil {
			t.Fatalf("%s checked out no files: %v", add.name, err)
		}
		_ = wt.Remove(context.WithoutCancel(ctx))
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("smudge filter executed during %s", add.name)
		}
	}
}

// merge.<driver>.driver is a command line git runs on conflict. A squash
// merge of a review branch must not exec one planted in the clone's config.
func TestMergeDriverDoesNotRunOnMerge(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	gitIn(t, r.Dir, "config", "merge.evil.driver", "touch "+marker+" ; true")
	if err := os.WriteFile(filepath.Join(r.Dir, ".gitattributes"),
		[]byte("* merge=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "add", ".gitattributes")
	gitIn(t, r.Dir, "commit", "-qm", "mergeattr")

	gitIn(t, r.Dir, "checkout", "-qb", "other")
	if err := os.WriteFile(filepath.Join(r.Dir, "main.go"),
		[]byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "commit", "-qam", "other")
	gitIn(t, r.Dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(r.Dir, "main.go"),
		[]byte("package mainline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.Dir, "commit", "-qam", "mainline")
	r = Open(r.Dir)

	mr := r.Merge(context.Background(), "other", "land other")
	if !mr.Conflict {
		t.Fatalf("expected a merge conflict, got %+v", mr)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("merge driver executed during Merge")
	}
}

// GIT_SSH_COMMAND=ssh is looked up on PATH. A relative PATH entry would
// resolve to a planted ssh in the reviewed tree during ls-remote/fetch/push.
func TestPlantedSSHOnRelativePATHDoesNotRun(t *testing.T) {
	r := newRepo(t)
	t.Setenv("GIT_SSH_COMMAND", "placeholder")
	os.Unsetenv("GIT_SSH_COMMAND")

	marker := filepath.Join(t.TempDir(), "pwned")
	script := filepath.Join(r.Dir, "ssh")
	if err := os.WriteFile(script,
		[]byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "."+string(os.PathListSeparator)+os.Getenv("PATH"))
	gitIn(t, r.Dir, "remote", "add", "origin", "git@host.invalid:owner/repo.git")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := r.RemoteBranchTip(ctx, "origin", "main"); err == nil {
		t.Fatal("ls-remote against host.invalid somehow succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("planted ./ssh executed via a relative PATH entry")
	}
}

func TestEmptyGitSSHCommandDefaultsToSSH(t *testing.T) {
	for _, val := range []string{"", "   "} {
		t.Setenv("GIT_SSH_COMMAND", val)
		env := gitEnv()
		found := false
		for _, kv := range env {
			if kv == "GIT_SSH_COMMAND=ssh" {
				found = true
			} else if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
				t.Fatalf("unexpected GIT_SSH_COMMAND entry: %q", kv)
			}
		}
		if !found {
			t.Fatalf("GIT_SSH_COMMAND=%q did not default to GIT_SSH_COMMAND=ssh", val)
		}
	}
}

// Git translates its own output, and that output reaches the journal and the
// error a failed review reports. Pinning the C locale is what keeps a run
// recorded on a translated machine byte-comparable with a replay elsewhere,
// and parseShortstat reads its counts out of English "N insertion(s)" text, so
// a git carrying translated catalogs must not be the one answering either.
func TestGitLocaleIsPinnedToC(t *testing.T) {
	for _, val := range []string{"", "de_DE.UTF-8"} {
		t.Setenv("LC_ALL", val)
		env := gitEnv()
		found := false
		for _, kv := range env {
			if kv == gitLocale {
				found = true
			} else if strings.HasPrefix(kv, "LC_ALL=") {
				t.Fatalf("LC_ALL=%q left an unexpected entry: %q", val, kv)
			}
		}
		if !found {
			t.Fatalf("LC_ALL=%q did not yield %s", val, gitLocale)
		}
	}
}

func TestDisableLocalDriversBlanksExecutableKeys(t *testing.T) {
	listing := strings.Join([]string{
		"filter.evil.smudge=touch pwned",
		"filter.evil.clean=cat",
		"filter.evil.required=true",
		"merge.evil.driver=touch pwned",
		"diff.evil.textconv=cat",
		"core.editor=vim",
		"user.email=test@example.invalid",
		"remote.origin.url=https://github.com/o/r.git",
		"url.https://evil.insteadOf=https://github.com/",
	}, "\n")
	got := disableLocalDrivers(listing)
	want := []string{
		"-c", "filter.evil.smudge=",
		"-c", "filter.evil.clean=",
		"-c", "filter.evil.required=false",
		"-c", "merge.evil.driver=",
		"-c", "diff.evil.textconv=",
		"-c", "core.editor=",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("disableLocalDrivers =\n%q\nwant\n%q", got, want)
	}
}

// FuzzDisableLocalDrivers drives the local-config blanker with arbitrary
// `git config --list` text. That listing is the reviewed repository's own
// config, and the result is spliced into every later git argv as `-c key=`.
// Whatever it emits must be paired `-c` assignments that neutralize a driver
// key from the listing, never invent a value, and stay deterministic.
func FuzzDisableLocalDrivers(f *testing.F) {
	seeds := []string{
		strings.Join([]string{
			"filter.evil.smudge=touch pwned",
			"filter.evil.clean=cat",
			"filter.evil.required=true",
			"merge.evil.driver=touch pwned",
			"diff.evil.textconv=cat",
			"core.editor=vim",
			"user.email=test@example.invalid",
			"remote.origin.url=https://github.com/o/r.git",
		}, "\n"),
		"filter.x.process=foo\ndiff.x.command=bar\ndiff.x.cmd=baz\n",
		"core.gitproxy=x\ninteractive.difffilter=y\nsequence.editor=z\ncore.askpass=w\n",
		"filter.x.required=\n=novalue\nnocolon\n\n\t\n",
		"FILTER.X.SMUDGE=x\nfilter..smudge=x\nmerge..driver=x\n",
		strings.Repeat("filter.x.smudge=y\n", 20),
		"filter.x.smudge=" + strings.Repeat("a", 4096),
		"diff.x.textconv=\ncore.editor=\n",
		"filter.x.smudge=one\nfilter.x.smudge=two\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, listing string) {
		got := disableLocalDrivers(listing)
		if again := disableLocalDrivers(listing); !slices.Equal(got, again) {
			t.Fatalf("disableLocalDrivers(%q) is not deterministic: %q vs %q", listing, got, again)
		}
		if len(got)%2 != 0 {
			t.Fatalf("disableLocalDrivers(%q) = %q, not -c pairs", listing, got)
		}
		want := 0
		for line := range strings.SplitSeq(listing, "\n") {
			key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || key == "" {
				continue
			}
			if driverKey(key) {
				want++
			}
		}
		if len(got)/2 != want {
			t.Fatalf("disableLocalDrivers(%q) neutralized %d keys, listing had %d driver keys",
				listing, len(got)/2, want)
		}
		for i := 0; i < len(got); i += 2 {
			if got[i] != "-c" {
				t.Fatalf("argv %d is %q, want -c (from %q)", i, got[i], listing)
			}
			val := got[i+1]
			key, rhs, ok := strings.Cut(val, "=")
			if !ok || key == "" || !driverKey(key) {
				t.Fatalf("neutralized %q, which is not a driver key (from %q)", val, listing)
			}
			switch rhs {
			case "":
				if strings.HasPrefix(key, "filter.") && strings.HasSuffix(key, ".required") {
					t.Fatalf("blanked required-filter key %q, want false", key)
				}
			case "false":
				if !(strings.HasPrefix(key, "filter.") && strings.HasSuffix(key, ".required")) {
					t.Fatalf("set false on %q", key)
				}
			default:
				t.Fatalf("left a value on %q", val)
			}
		}
	})
}

func driverKey(key string) bool {
	switch {
	case isFilterCommand(key), isMergeDriver(key), isDiffHelper(key),
		key == "core.gitproxy", key == "interactive.difffilter",
		key == "core.editor", key == "sequence.editor", key == "core.askpass":
		return true
	case strings.HasPrefix(key, "filter.") && strings.HasSuffix(key, ".required"):
		return true
	}
	return false
}

// The snapshot worktree is a checkout of one commit: uncommitted files in the
// original tree must not appear in it, which is what lets stacked discovery
// read it instead of the checkout.
func TestSnapshotWorktreeHoldsOnlyTheCommit(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(r.Dir, "evil-review.md"),
		[]byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddSnapshotWorktree(ctx, "run", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wt.Remove(context.WithoutCancel(ctx)) }()
	if _, err := os.Stat(filepath.Join(wt.Dir, "main.go")); err != nil {
		t.Fatalf("committed file missing from snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt.Dir, "evil-review.md")); !os.IsNotExist(err) {
		t.Fatal("uncommitted file reached the snapshot")
	}
}

// Branch operations must separate flags from branch names with "--" so branch
// names or patterns cannot inject options into git branch or merge commands.
func TestBranchOperationsSeparateOptionsWithDashes(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	// Deleting a non-existent branch name formatted like a flag must report
	// branch not found, not fail with an unrecognized option.
	err := r.DeleteBranch(ctx, "--abort")
	if err == nil {
		t.Fatal("deleting a branch that does not exist must report the failure")
	}
	if msg := err.Error(); !strings.Contains(msg, "--abort") ||
		strings.Contains(msg, "unrecognized option") {
		t.Fatalf("DeleteBranch must name the branch and report branch-not-found, got: %s", msg)
	}

	// Deleting branches matching a pattern with dashed prefix. The pattern
	// matches nothing, so the sweep must come back clean; a `git branch -D
	// --pattern*` that reached git as an option would fail instead.
	if err := r.DeleteBranchesMatching(ctx, "--pattern*"); err != nil {
		t.Fatalf("DeleteBranchesMatching on an option-shaped pattern: %v", err)
	}

	// An empty branch name is a no-op, not a git invocation that always fails.
	if err := r.DeleteBranch(ctx, ""); err != nil {
		t.Fatalf("DeleteBranch with an empty name must be a no-op, got: %v", err)
	}

	wt, err := r.AddWorktree(ctx, "lane", "test-tag", "HEAD")
	if err != nil {
		t.Fatalf("AddWorktree failed: %v", err)
	}
	defer wt.Remove(ctx)

	// SquashIn with option-shaped branch name must not trigger git merge options.
	if _, err := wt.SquashIn(ctx, "--abort"); err == nil {
		t.Fatal("SquashIn on non-existent branch --abort should fail")
	} else if strings.Contains(err.Error(), "expects no arguments") {
		t.Fatalf("SquashIn failed with option injection error: %v", err)
	}

	// RenameBranch with option-shaped name must be rejected by ref format check
	// or git branch -m with -- separator.
	if err := wt.RenameBranch(ctx, "--new-branch"); err == nil {
		t.Fatal("RenameBranch on --new-branch should fail")
	}

	// Aborting worktree add or reclaiming empty branch with dashed branch name.
	// The abort reports nothing, so the traces it must leave are the
	// assertions: the half-created checkout is gone and no branch was created
	// for the option-shaped name.
	tempWT := r.worktreeDir("temp-wt")
	if err := os.MkdirAll(filepath.Join(tempWT, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.abortWorktreeAdd(ctx, tempWT, "--abort")
	if _, err := os.Stat(tempWT); !os.IsNotExist(err) {
		t.Fatalf("abortWorktreeAdd left %s behind: %v", tempWT, err)
	}
	if out := gitOut(t, r.Dir, "for-each-ref", "--format=%(refname)", "refs/heads/"); strings.Contains(out, "--abort") {
		t.Fatalf("abortWorktreeAdd created a branch for the option-shaped name: %s", out)
	}
	tip, err := r.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.reclaimEmptyBranch(ctx, "--abort", tip); err != nil {
		t.Fatalf("reclaimEmptyBranch failed: %v", err)
	}

	// A file named HEAD in the repository must not cause git log or diff to fail
	// with "ambiguous argument 'HEAD': both revision and filename".
	headFile := filepath.Join(r.Dir, "HEAD")
	if err := os.WriteFile(headFile, []byte("collision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(headFile)

	subj, err := r.CommitSubject(ctx, "HEAD")
	if err != nil {
		t.Fatalf("CommitSubject failed with HEAD file present: %v", err)
	}
	if subj != "init" {
		t.Fatalf("CommitSubject = %q, want the seed commit subject %q", subj, "init")
	}

	if _, err := r.StripAITrailers(ctx, ""); err != nil {
		t.Fatalf("StripAITrailers failed with HEAD file present: %v", err)
	}

	if _, _, ok := r.DiffStat(ctx, r.Dir, "HEAD", "HEAD"); !ok {
		t.Fatal("DiffStat failed with HEAD file present")
	}

	if _, err := r.ChangedFiles(ctx, r.Dir, "HEAD", "HEAD"); err != nil {
		t.Fatalf("ChangedFiles failed with HEAD file present: %v", err)
	}

	// abortMerge with HEAD file present must reset the tree: a `reset --hard
	// HEAD` that hit "ambiguous argument" would leave the edit in place, and
	// the call itself reports nothing.
	tracked := filepath.Join(r.Dir, "main.go")
	if err := os.WriteFile(tracked, []byte("package main\n\nfunc main() { panic(\"dirty\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.abortMerge(ctx)
	if got := gitOut(t, r.Dir, "diff", "--name-only", "HEAD", "--"); got != "" {
		t.Fatalf("abortMerge did not reset the tree with a HEAD file present, still dirty: %s", got)
	}

	// ResetToBase with HEAD file present must succeed.
	if err := wt.ResetToBase(ctx); err != nil {
		t.Fatalf("ResetToBase failed with HEAD file present: %v", err)
	}

	// CommitSubject with option-shaped ref must not execute git log flags like --output.
	marker := filepath.Join(t.TempDir(), "pwned_log")
	if _, err := r.CommitSubject(ctx, "--output="+marker); err == nil {
		t.Fatal("CommitSubject with --output flag should fail")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("CommitSubject executed --output and created file")
	}

	// DiffStat with option-shaped ref must not execute git diff flags like --output.
	diffMarker := filepath.Join(t.TempDir(), "pwned_diff")
	if _, _, ok := r.DiffStat(ctx, r.Dir, "--output="+diffMarker, "HEAD"); ok {
		t.Fatal("DiffStat with --output flag should not report ok")
	}
	if _, err := os.Stat(diffMarker); !os.IsNotExist(err) {
		t.Fatal("DiffStat executed --output and created file")
	}

	// ChangedFiles with option-shaped ref must not execute git diff flags like --output.
	changedMarker := filepath.Join(t.TempDir(), "pwned_changed")
	if _, err := r.ChangedFiles(ctx, r.Dir, "--output="+changedMarker, "HEAD"); err == nil {
		t.Fatal("ChangedFiles with --output flag should fail")
	}
	if _, err := os.Stat(changedMarker); !os.IsNotExist(err) {
		t.Fatal("ChangedFiles executed --output and created file")
	}

	// Tip with option-shaped ref must be treated as a single ref and fail, not run --branches.
	if _, err := r.Tip(ctx, "--branches"); err == nil {
		t.Fatal("Tip with --branches flag should fail")
	}

	// ParentTip with option-shaped ref must be treated as a single ref and fail.
	if _, err := r.ParentTip(ctx, "--branches"); err == nil {
		t.Fatal("ParentTip with --branches flag should fail")
	}
}

// `git worktree add <dir> <commit-ish>` has no `--`, so a merge target that
// starts with a dash reaches an option position. MergeInto must refuse the
// shape instead of letting `worktree add` read it as a flag.
func TestMergeIntoRefusesOptionShapedTarget(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	for _, target := range []string{"--detach", "-f", "--orphan=evil", "-b"} {
		mr := r.MergeInto(ctx, target, "main", "merge")
		if mr.Merged {
			t.Fatalf("MergeInto(%q) reported a merge; want the shape refused", target)
		}
		if !strings.Contains(mr.Detail, "invalid branch name") {
			t.Fatalf("MergeInto(%q) Detail = %q; want the check-ref-format refusal", target, mr.Detail)
		}
	}
	// The refusal happens before any worktree is created.
	entries, err := os.ReadDir(filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot)))
	if err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "merge-") {
				t.Fatalf("MergeInto created %q for an option-shaped target", e.Name())
			}
		}
	}
}

// The reviewed tree picks gitDir, so a planted symlink at .git/info must not
// redirect the exclude append outside the repository: the run would create a
// file in a directory the operator never pointed gauntlet at.
func TestExcludeOwnArtifactsRefusesASymlinkedInfoDir(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	outside := t.TempDir()
	info := filepath.Join(r.Dir, ".git", "info")
	// The link replaces the directory, so it has to go first: Symlink over an
	// existing path fails and the test would skip instead of testing anything.
	if err := os.RemoveAll(info); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, info); err != nil {
		t.Skipf("cannot plant a symlink: %v", err)
	}

	r.ExcludeOwnArtifacts(ctx)

	planted := filepath.Join(outside, "exclude")
	body, err := os.ReadFile(planted)
	if err == nil {
		t.Fatalf("ExcludeOwnArtifacts wrote %q through a symlinked info dir: %q", planted, body)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("reading the planted exclude: %v", err)
	}
}

// A review runs with its permissions bypassed and can write .git/config
// itself. The per-repo overlay that blanks filter/merge/diff drivers is
// derived from that file, so an overlay computed once and never rechecked
// would hand the next checkout a driver the review planted after the first
// git call, with nothing blanking it.
func TestExtraSafeConfigRebuildsAfterConfigChanges(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	first := r.extraSafeConfig()
	if r.safeConfig == "" {
		t.Fatal("no local config path was recorded for the overlay")
	}
	if slices.Contains(first, "filter.late.smudge=") {
		t.Fatalf("overlay blanks a driver that is not configured yet: %q", first)
	}

	// The config grows, so size alone tells the stamp it changed.
	gitIn(t, r.Dir, "config", "filter.late.smudge", "touch pwned")
	second := r.extraSafeConfig()
	if !slices.Contains(second, "filter.late.smudge=") {
		t.Fatalf("overlay kept a stale answer after .git/config changed: %q", second)
	}

	// And it is still correct when nothing changed: a second call must not
	// rebuild into a different answer, and must not lose the blank either.
	gitIn(t, r.Dir, "config", "filter.later.smudge", "touch pwned")
	third := r.extraSafeConfig()
	if !slices.Contains(third, "filter.late.smudge=") ||
		!slices.Contains(third, "filter.later.smudge=") {
		t.Fatalf("overlay lost a blank on rebuild: %q", third)
	}
	if again := r.extraSafeConfig(); strings.Join(again, "\x00") != strings.Join(third, "\x00") {
		t.Fatalf("an unchanged config produced a different overlay: %q vs %q", again, third)
	}
	if _, err := r.Status(ctx, nil); err != nil {
		t.Fatalf("git status after the rebuilds: %v", err)
	}
}
