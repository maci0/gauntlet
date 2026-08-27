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

// gitIn runs one git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
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
