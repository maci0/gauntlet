// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/gitx"
)

// What --push does after a review merges: publish it, say so on the bus, and
// keep going when the push itself fails. The push is a step of its own with
// its own failure mode, and the line it publishes is the only record the
// operator has that the work left the machine, so all three halves are pinned
// here rather than left to the run smoke tests, which never set the flag.

// A review that merges under --push reaches the remote and says so. A review
// that merges and never announces the push reads as unpublished work sitting
// in a branch, which is what an operator goes looking for by hand.
func TestMergedReviewPushesAndSaysSo(t *testing.T) {
	origin := t.TempDir()
	runGit(t, origin, "init", "-q", "-b", "main")
	runGit(t, origin, "config", "user.email", "test@example.invalid")
	runGit(t, origin, "config", "user.name", "test")
	runGit(t, origin, "config", "receive.denyCurrentBranch", "ignore")
	writeIn(t, origin, "main.go", "package main\n")
	runGit(t, origin, "add", "-A")
	runGit(t, origin, "commit", "-qm", "init")

	dir := t.TempDir()
	runGit(t, dir, "clone", origin, ".")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "test")

	repo := gitx.Open(dir)
	if err := repo.Push(t.Context()); err != nil {
		t.Fatalf("seeding the remote: %v", err)
	}
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "adding the guard"
cat > guard.go <<'EOF'
package main

func guard() {}
EOF
echo "RESULT: changed=1"`)
	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	cfg.Push, cfg.Jobs = true, 2

	r, events := runRecorded(t, cfg)
	assertMerged(t, events, gitx.LaneBranch("test-l1-lane0-00", "a-review"))
	if !sawLog(events, "Pushed a-review") {
		t.Fatalf("a merged review under --push did not report the push:\n%s", logText(events))
	}
	if r.Stats().CommitFails() != 0 {
		t.Fatalf("counts: %+v, want no failed step", r.Stats().Counts())
	}
	// The remote holds the work. The subject is the runner's, written from
	// the files the review touched rather than taken from the agent.
	if body := runGit(t, origin, "log", "-1", "--format=%B"); !strings.Contains(body, "guard.go") {
		t.Fatalf("the review merged but never reached the remote:\n%s", body)
	}
	if _, found, err := repo.RemoteBranchTip(t.Context(), "origin", "main"); err != nil || !found {
		t.Fatalf("the remote has no main after --push: %v", err)
	}
}

// A push that fails is counted and the run carries on: the work is already
// committed and merged, so refusing to publish it would leave the operator
// with a review they asked for sitting in a local branch. The line has to name
// the failure, because the next push (or the commit step) is what carries
// this commit, and an operator who read "Merged a-review" and nothing else
// has no reason to go looking for it.
func TestFailedPushAfterAMergeIsCountedAndReported(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
cat > guard.go <<'EOF'
package main

func guard() {}
EOF
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	// A repository with no remote at all: git push fails outright, which is
	// what a revoked credential, a deleted remote, or an offline machine
	// looks like from here.
	cfg.Push, cfg.Jobs = true, 2

	r, events := runRecorded(t, cfg)
	assertMerged(t, events, gitx.LaneBranch("test-l1-lane0-00", "a-review"))
	if !sawLog(events, "Push after a-review failed") {
		t.Fatalf("a push that failed after the merge was not reported:\n%s", logText(events))
	}
	if r.Stats().CommitFails() != 1 {
		t.Fatalf("counts: %+v, want the failed push counted", r.Stats().Counts())
	}
	// The review still counts as landed: it merged, and losing that would
	// report a success as a failure.
	if c := r.Stats().Counts(); c.OK != 1 {
		t.Fatalf("counts: %+v, want the merged review to count as ok", c)
	}
	// The work is still here, which is the whole point of not treating this
	// as fatal.
	if body := runGit(t, dir, "log", "-1", "--format=%s"); !strings.Contains(body, "guard.go") {
		t.Fatalf("a failed push lost the commit:\n%s", body)
	}
}

// --push off is the default and has to stay quiet: a run nobody asked to
// publish must not announce a push it never made, and must not fail one it
// never attempted. testRepo has no remote, so a push here would fail loudly.
func TestNoPushIsAttemptedWithoutTheFlag(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
cat > guard.go <<'EOF'
package main

func guard() {}
EOF
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	cfg.Jobs = 2
	r, events := runRecorded(t, cfg)

	assertMerged(t, events, gitx.LaneBranch("test-l1-lane0-00", "a-review"))
	if got := logText(events); strings.Contains(got, "Pushed ") || strings.Contains(got, "Push after ") {
		t.Fatalf("a run without --push reported a push:\n%s", got)
	}
	if r.Stats().CommitFails() != 0 {
		t.Fatalf("counts: %+v, want no failed step", r.Stats().Counts())
	}
}

// logText joins the run's log lines, for a failure that has to show what the
// run did say rather than only what it did not.
func logText(events []Event) string {
	var out []string
	for _, ev := range events {
		if ev.Kind == EvLog {
			out = append(out, ev.Text)
		}
	}
	return strings.Join(out, "\n")
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
