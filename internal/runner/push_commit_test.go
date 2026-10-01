// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
)

// git runs one command in dir and returns its combined output, failing the
// test on error. A push that is expected to be refused goes through exec
// directly in the test body.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// A remote that moved under a run is the one push failure an agent used to
// recover from by itself. Without --yolo the commit stays local and the step
// fails: rebasing somebody's branch onto a remote they did not ask to merge
// with is a change to their history, and a step that quietly did it would be
// the tool rewriting a repository nobody consented to. This pins the refusal
// half so the recovery below cannot grow into the default.
func TestPushAfterCommitRefusesADivergentRemoteWithoutYolo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for runner tests")
	}
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "-b", "main")
	runGit(t, origin, "config", "user.email", "test@example.invalid")
	runGit(t, origin, "config", "user.name", "test")
	runGit(t, origin, "config", "receive.denyCurrentBranch", "ignore")
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "init")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", origin, ".")
	runGit(t, cloneDir, "config", "user.email", "test@example.invalid")
	runGit(t, cloneDir, "config", "user.name", "test")
	repo := gitx.Open(cloneDir)
	if err := repo.Push(context.Background()); err != nil {
		t.Fatalf("seeding the remote: %v", err)
	}

	// Somebody else lands a commit on the remote while the run works, so the
	// local commit is a non-fast-forward.
	if err := os.WriteFile(filepath.Join(origin, "upstream.go"), []byte("package upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "upstream moved")

	if err := os.WriteFile(filepath.Join(cloneDir, "local.go"), []byte("package local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, cloneDir, "add", "-A")
	runGit(t, cloneDir, "commit", "-qm", "local work")

	if err := pushAfterCommit(context.Background(), repo, false); err == nil {
		t.Fatal("a divergent remote was pushed over without --yolo")
	}
	// The local history is untouched: the commit is still there, still the
	// tip, and the rebase that would have rewritten it never ran.
	if body := runGit(t, cloneDir, "log", "-1", "--format=%s"); !strings.Contains(body, "local work") {
		t.Fatalf("the refused push moved the local tip: %s", body)
	}
}

// --yolo recovers the divergence the way the agent used to: rebase, strip the
// replayed commit again, push. The rebase rewrites the commit the step just
// made, so StripAITrailers runs in its "clean HEAD whatever it is" mode, and
// that is the load-bearing step in the whole path: the agent's attribution
// lives in the commit the rebase replayed, and a recovery that pushed
// without re-stripping publishes it to the remote where no sweep will take it
// back. This is the regression guard for that, and it also pins that the
// upstream commit survives the rebase rather than being dropped.
func TestPushAfterCommitRecoversADivergentRemoteUnderYolo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for runner tests")
	}
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "-b", "main")
	runGit(t, origin, "config", "user.email", "test@example.invalid")
	runGit(t, origin, "config", "user.name", "test")
	runGit(t, origin, "config", "receive.denyCurrentBranch", "ignore")
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "init")

	cloneDir := t.TempDir()
	runGit(t, cloneDir, "clone", origin, ".")
	runGit(t, cloneDir, "config", "user.email", "test@example.invalid")
	runGit(t, cloneDir, "config", "user.name", "test")
	repo := gitx.Open(cloneDir)
	ctx := context.Background()
	if err := repo.Push(ctx); err != nil {
		t.Fatalf("seeding the remote: %v", err)
	}

	if err := os.WriteFile(filepath.Join(origin, "upstream.go"), []byte("package upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "upstream moved")

	// The commit the step made, carrying the attribution a CLI injects
	// despite the prompt ban. This is what the recovery has to strip.
	if err := os.WriteFile(filepath.Join(cloneDir, "local.go"), []byte("package local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, cloneDir, "add", "-A")
	runGit(t, cloneDir, "commit", "-q", "-m", "fix: the guard",
		"-m", "Co-Authored-By: Cursor <cursoragent@cursor.com>")
	localTip, err := repo.Tip(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := pushAfterCommit(ctx, repo, true); err != nil {
		t.Fatalf("yolo recovery failed: %v", err)
	}

	// The remote now holds the replayed commit, cleaned.
	body := runGit(t, origin, "log", "-1", "--format=%B")
	if strings.Contains(body, "Co-Authored-By") || strings.Contains(body, "cursoragent") {
		t.Fatalf("the recovery pushed the attribution the rebase replayed:\n%s", body)
	}
	if !strings.Contains(body, "fix: the guard") {
		t.Fatalf("the recovery dropped the commit it was pushing:\n%s", body)
	}
	// The rebase replayed onto the upstream commit rather than overwriting
	// it: the remote tip is a new commit, and the upstream work is behind it.
	remoteTip := strings.TrimSpace(runGit(t, origin, "rev-parse", "HEAD"))
	if remoteTip == localTip {
		t.Fatal("the remote still points at the pre-rebase commit")
	}
	if parent := strings.TrimSpace(runGit(t, origin, "rev-parse", "HEAD~1")); !strings.Contains(
		runGit(t, origin, "log", "-1", "--format=%s", parent), "upstream moved") {
		t.Fatalf("the rebased commit does not sit on the upstream one; its parent is %s", parent)
	}
}

// CommitNow pushes when it is told to, and the push it makes is the runner's,
// not the agent's: the agent only commits. This pins that a commit step with
// Push set publishes the commit, which is the path whose recovery the two
// tests above cover and the one a caller wires --commit through.
func TestCommitNowPushesTheStrippedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for runner tests")
	}
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "-b", "main")
	runGit(t, origin, "config", "user.email", "test@example.invalid")
	runGit(t, origin, "config", "user.name", "test")
	runGit(t, origin, "config", "receive.denyCurrentBranch", "ignore")
	if err := os.WriteFile(filepath.Join(origin, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "init")

	repo := t.TempDir()
	runGit(t, repo, "clone", origin, ".")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "test")
	if err := gitx.Open(repo).Push(context.Background()); err != nil {
		t.Fatalf("seeding the remote: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committer := fakeAgent(t, t.TempDir(), "claude", `
echo "committing the write"
git add -A
git commit -q -m "fix: the write" -m "Co-Authored-By: Cursor <cursoragent@cursor.com>"`)

	var out bytes.Buffer
	if err := CommitNow(context.Background(), CommitOpts{
		Dir: repo, Agent: agent.Spec{Tool: "claude"},
		Bin: map[string]string{"claude": committer}, Push: true, Timeout: 30 * time.Second,
		Out: func(line string) { out.WriteString(line + "\n") },
	}); err != nil {
		t.Fatalf("CommitNow with Push: %v", err)
	}
	// Out is what keeps the operator from watching a silent pause, so a
	// launch that streamed nothing never reached the sink at all.
	if !strings.Contains(out.String(), "committing the write") {
		t.Fatalf("CommitNow streamed nothing to its Out sink:\n%q", out.String())
	}

	body := runGit(t, origin, "log", "-1", "--format=%B")
	if !strings.Contains(body, "fix: the write") {
		t.Fatalf("the pushed commit is not the one the agent made:\n%s", body)
	}
	if strings.Contains(body, "Co-Authored-By") {
		t.Fatalf("the push published an attribution trailer:\n%s", body)
	}
}
