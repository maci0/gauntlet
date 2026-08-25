// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/prompt"
)

// fakeAgent writes an executable that behaves like an agent CLI: it takes the
// prompt as its last argument, prints some output, and edits the tree.
func fakeAgent(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// testRepo makes a git repository with one commit.
func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for runner tests")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

// promptSet builds a small review set on disk.
func promptSet(t *testing.T, names ...string) (prompt.Set, string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		body := "Your goal is to test " + n + ".\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := Discover(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	return set, dir
}

// Discover is a thin test helper around prompt.Discover.
func Discover(t *testing.T, dir string) (prompt.Set, []string, error) {
	t.Helper()
	return prompt.Discover(context.Background(), dir, dir)
}

// drain subscribes now and discards events in the background, so a test that
// does not inspect the stream still cannot block the runner.
func drain(bus *Bus) {
	ch := bus.Subscribe(256)
	go func() {
		for range ch {
		}
	}()
}

// collect drains a bus subscription into a slice until it closes.
func collect(ch <-chan Event, done chan<- []Event) {
	var got []Event
	for ev := range ch {
		got = append(got, ev)
	}
	done <- got
}

func baseConfig(t *testing.T, repo string, set prompt.Set, reviews []string, bin string) Config {
	t.Helper()
	return Config{
		Dir: repo, Set: set, Reviews: reviews,
		Agents:  []agent.Spec{{Tool: "claude"}},
		Bin:     map[string]string{"claude": bin},
		Timeout: 30 * time.Second, Jobs: 1, MaxLoops: 1,
		RunID: "test", Version: "test", Quiet: false,
	}
}

// runQuiet runs cfg to completion with the event stream drained, for tests
// that judge the outcome from the stats alone.
func runQuiet(t *testing.T, cfg Config) *Runner {
	t.Helper()
	bus := NewBus()
	drain(bus)
	return runOn(t, cfg, bus)
}

// runRecorded runs cfg to completion and returns the runner and every event
// it published.
func runRecorded(t *testing.T, cfg Config) (*Runner, []Event) {
	t.Helper()
	bus := NewBus()
	events := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(events, done)
	r := runOn(t, cfg, bus)
	return r, <-done
}

// runOn is the shared body of runQuiet and runRecorded.
func runOn(t *testing.T, cfg Config, bus *Bus) *Runner {
	t.Helper()
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()
	return r
}

func TestSequentialRunEditsTreeAndCountsLines(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review", "doc-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "Reading main.go"
echo "// touched" >> main.go
echo '{"usage":{"output_tokens":1200}}'
echo "RESULT: changed=1"`)

	r, got := runRecorded(t, baseConfig(t, repo, set, []string{"sec-review", "doc-review"}, bin))

	c := r.Stats().Counts()
	if c.OK != 2 || c.Failures() != 0 {
		t.Fatalf("counts: %+v", c)
	}
	body, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(body), "// touched") != 2 {
		t.Fatalf("both reviews should have edited the tree:\n%s", body)
	}
	for _, res := range r.Stats().Results() {
		if !res.HaveLines || res.Ins != 1 {
			t.Errorf("%s: line stats wrong: %+v", res.Review, res)
		}
		if res.Tokens != 1200 {
			t.Errorf("%s: tokens not parsed: %d", res.Review, res.Tokens)
		}
	}
	if countKind(got, EvReviewEnd) != 2 || countKind(got, EvLoopEnd) != 1 {
		t.Fatalf("event stream is missing results: %d ends", countKind(got, EvReviewEnd))
	}
}

func TestFailingAgentIsRetriedOnAnother(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	binDir := t.TempDir()
	bad := fakeAgent(t, binDir, "claude", `echo "boom" >&2; exit 3`)
	good := fakeAgent(t, binDir, "codex", `echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bad)
	cfg.Agents = []agent.Spec{{Tool: "claude"}, {Tool: "codex"}}
	cfg.Bin = map[string]string{"claude": bad, "codex": good}

	// This test's subject is the retry, which only runs when the failing
	// agent is sampled first. Which one goes first is a seeded choice keyed
	// by review name, so search for a seed that orders it that way: with the
	// clock's default seed the assertions hold either way, and retry would go
	// untested about half the time while the suite stayed green.
	seed := uint64(0)
	for s := uint64(1); s < 1000 && seed == 0; s++ {
		probeCfg := cfg
		probeCfg.Seed = s
		probe, err := New(context.Background(), probeCfg, NewBus())
		if err != nil {
			t.Fatal(err)
		}
		if probe.pickAgent("sec-review", nil).Tool == "claude" {
			seed = s
		}
	}
	if seed == 0 {
		t.Fatal("no seed in 1000 samples the failing agent first")
	}
	cfg.Seed = seed

	r := runQuiet(t, cfg)

	results := r.Stats().Results()
	if len(results) != 1 {
		t.Fatalf("a retry must produce one result, got %d: %+v", len(results), results)
	}
	// Whichever agent was picked first, the review must end up recorded as a
	// success on the working agent: the retry replaces the failed attempt
	// entirely, so neither a failure nor the bad agent may survive.
	if results[0].Status != StatusOK || results[0].Agent.Tool != "codex" {
		t.Fatalf("unexpected outcome: %+v", results[0])
	}
}

func TestTimeoutKillsTheAgent(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo starting; sleep 30`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Timeout = 300 * time.Millisecond

	start := time.Now()
	r := runQuiet(t, cfg)

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("timeout did not kill the agent: took %s", elapsed)
	}
	if got := r.Stats().Counts(); got.Timeout != 1 {
		t.Fatalf("counts: %+v", got)
	}
}

func TestParallelModeRequiresCleanTree(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo ok`)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Jobs = 3

	_, err := New(context.Background(), cfg, NewBus())
	if err == nil || !strings.Contains(err.Error(), "clean working tree") {
		t.Fatalf("want a clean-tree error, got %v", err)
	}
}

func TestParallelModeIsolatesAndMerges(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review")
	// Each review writes its own file, so all three merge cleanly.
	bin := fakeAgent(t, t.TempDir(), "claude", `
name=$(basename "$PWD")
echo "content of $name" > "fix-$name.txt"
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review", "c-review"}, bin)
	cfg.Jobs = 3

	r, got := runRecorded(t, cfg)

	if c := r.Stats().Counts(); c.OK != 3 {
		t.Fatalf("counts: %+v", c)
	}
	if n := countKind(got, EvMerge); n != 3 {
		t.Fatalf("want three merges, got %d", n)
	}
	// Every review's work reached the main tree, and nothing was left behind.
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	fixes := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "fix-") {
			fixes++
		}
		if e.Name() == ".gauntlet" {
			t.Error("worktree root was not cleaned up")
		}
	}
	if fixes != 3 {
		t.Fatalf("want three merged files, found %d", fixes)
	}
	if out := gitOut(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("tree should be clean after merges:\n%s", out)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out != "" {
		t.Fatalf("merged branches should be deleted:\n%s", out)
	}
}

func TestParallelModeKeepsConflictingBranches(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	// Both reviews rewrite the same line differently: one merges, one cannot.
	bin := fakeAgent(t, t.TempDir(), "claude", `
printf 'package main\n\nfunc main() { /* %s */ }\n' "$(basename "$PWD")" > main.go
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review"}, bin)
	cfg.Jobs = 2

	r := runQuiet(t, cfg)

	c := r.Stats().Counts()
	if c.Conflict != 1 || c.OK != 1 {
		t.Fatalf("want one merge and one conflict, got %+v", c)
	}
	// The conflicting work is preserved, not dropped.
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out == "" {
		t.Fatal("the conflicting branch was deleted, losing that review's work")
	}
	// And the tree is left usable, not mid-merge.
	if out := gitOut(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("aborted merge left the tree dirty:\n%s", out)
	}
}

// TestParallelModeWithNoChangesDeletesTheBranch pins the empty-review path:
// a review that changed nothing leaves no branch behind. Its branch points at
// the base commit, so keeping it would litter the repo with dead refs run
// after run.
func TestParallelModeWithNoChangesDeletesTheBranch(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Jobs = 2

	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.OK != 1 {
		t.Fatalf("counts: %+v", c)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out != "" {
		t.Fatalf("a review with no changes must leave no branch:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gauntlet")); !os.IsNotExist(err) {
		t.Error("worktree root was not cleaned up")
	}
}

// TestFailedCommitDeletesTheBranch pins the commit-failure path: when the
// commit step itself fails, the branch still points at the base (nothing was
// committed), so it is deleted like any other failed review's instead of
// surviving as litter.
func TestFailedCommitDeletesTheBranch(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "// touched" >> main.go
echo "RESULT: changed=1"`)

	// Make every git commit fail without a secret key while leaving `git add`
	// working, which is exactly the shape of a real commit-step failure.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Jobs = 2

	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.Fail != 1 {
		t.Fatalf("want one failed review, got %+v", c)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out != "" {
		t.Fatalf("a failed commit must leave no branch:\n%s", out)
	}
	if entries, err := os.ReadDir(repo); err == nil {
		for _, e := range entries {
			if e.Name() == ".gauntlet" {
				t.Error("worktree root survived a failed commit")
			}
		}
	}
	// The agent only ever touched its own checkout, so the main tree is clean.
	if out := gitOut(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("tree left dirty:\n%s", out)
	}
}

// The commit step hands the dirty tree to an agent instead of committing
// itself, so the fakes tell the two invocations apart by the prompt: only the
// commit step's prompt calls the agent a git commit assistant. The prompt is
// not always the first argument, so the match runs over every argument.
const commitStepMarker = "commit assistant"

// TestCommitStepRunsAfterADirtyReview pins the happy path of --commit in
// sequential mode: a review that edits the tree is followed by exactly one
// commit-step launch, recorded in the run's counters and on an EvCommit event.
func TestCommitStepRunsAfterADirtyReview(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
case "$*" in *"`+commitStepMarker+`"*)
  echo "COMMIT: done" > commit-step-ran
  exit 0;;
esac
echo "// touched" >> main.go
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Commit = true

	r, got := runRecorded(t, cfg)

	c := r.Stats().Counts()
	if c.OK != 1 {
		t.Fatalf("review itself must succeed: %+v", c)
	}
	if r.Stats().CommitRuns() != 1 || r.Stats().CommitFails() != 0 {
		t.Fatalf("commit step not run once: runs=%d fails=%d",
			r.Stats().CommitRuns(), r.Stats().CommitFails())
	}
	var commits []Event
	for _, ev := range got {
		if ev.Kind == EvCommit {
			commits = append(commits, ev)
		}
	}
	if len(commits) != 1 || commits[0].Status != StatusOK {
		t.Fatalf("want one successful commit event, got %+v", commits)
	}
	// The commit agent answered as the commit assistant, not by falling
	// through the review branch again.
	if _, err := os.Stat(filepath.Join(repo, "commit-step-ran")); err != nil {
		t.Fatalf("the commit step never ran its own branch: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), "// touched"); n != 1 {
		t.Fatalf("the commit step ran the review branch %d extra times:\n%s", n-1, body)
	}
}

// TestCommitStepSkipsACleanTree pins the early return: a review that changed
// nothing must not spend a commit launch on an empty tree.
func TestCommitStepSkipsACleanTree(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Commit = true

	r := runQuiet(t, cfg)

	if r.Stats().CommitRuns() != 0 {
		t.Fatalf("clean tree still launched a commit step: %+v", r.Stats().Counts())
	}
}

// TestFailedCommitStepIsCountedAsAFailure pins that a failing commit step is
// visible as one: it is the difference between "reviews fixed things" and
// "the fixes are sitting uncommitted in the tree".
func TestFailedCommitStepIsCountedAsAFailure(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
case "$*" in *"`+commitStepMarker+`"*) echo "boom" >&2; exit 7;; esac
echo "// touched" >> main.go
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Commit = true

	r, got := runRecorded(t, cfg)

	if r.Stats().CommitRuns() != 1 || r.Stats().CommitFails() != 1 {
		t.Fatalf("failed step must be one run and one failure: runs=%d fails=%d",
			r.Stats().CommitRuns(), r.Stats().CommitFails())
	}
	for _, ev := range got {
		if ev.Kind != EvCommit {
			continue
		}
		if ev.Status != StatusFail {
			t.Fatalf("commit event reported %v, want fail", ev.Status)
		}
		return
	}
	t.Fatal("no commit event published")
}

func TestCancelStopsTheRun(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `sleep 10`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review", "c-review"}, bin)
	cfg.MaxLoops = 0 // would otherwise loop forever

	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	r.Run(ctx)
	bus.Close()

	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("cancel did not stop the run: took %s", elapsed)
	}
	if c := r.Stats().Counts(); c.Interrupted == 0 {
		t.Fatalf("interrupted review not recorded: %+v", c)
	}
}

func TestRequestStopFinishesInFlightWork(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.MaxLoops = 0

	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		r.RequestStop()
	}()
	r.Run(context.Background())
	bus.Close()

	if !r.soft.Load() {
		t.Fatal("stop was not recorded")
	}
	// A soft stop never kills an agent, so nothing may be interrupted.
	if c := r.Stats().Counts(); c.Interrupted != 0 {
		t.Fatalf("soft stop killed a review: %+v", c)
	}
}

func TestLockKeepsTwoRunsApart(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err == nil {
		t.Fatal("second acquire should fail")
	}
	first.Release()
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	second.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("lock file should be removed on release")
	}
}

// The note tells the next gauntlet what it is waiting for: it survives being
// rewritten shorter, and hostile bytes in it never reach the terminal.
func TestLockNoteReachesTheRunTurnedAway(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	held, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	held.Note("running a-review with claude, and a long tail of other work")
	held.Note("idle\x1b[31m\x07")
	_, err = Acquire(path)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: %v, want ErrLocked", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "idle") {
		t.Fatalf("the note is missing from %q", msg)
	}
	if strings.Contains(msg, "a-review") {
		t.Fatalf("the previous, longer note was left behind: %q", msg)
	}
	if strings.ContainsAny(msg, "\x1b\x07") {
		t.Fatalf("control bytes reached the message: %q", msg)
	}
}

// A review whose agent dies once is rerun on the same agent after a wait, and
// only falls through to another agent once the retries are spent.
func TestFailedReviewIsRetriedOnTheSameAgent(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	marker := filepath.Join(t.TempDir(), "attempts")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo x >> `+marker+`
attempts=$(wc -l < `+marker+`)
if [ "$attempts" -lt 3 ]; then
	echo "overloaded_error" >&2
	exit 1
fi
echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Retries, cfg.RetryDelay = 2, time.Millisecond
	r, got := runRecorded(t, cfg)

	if c := r.Stats().Counts(); c.OK != 1 || c.Failures() != 0 {
		t.Fatalf("the third attempt succeeded, so the review did: %+v", c)
	}
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(body), "x"); lines != 3 {
		t.Fatalf("agent ran %d times, want 3 (first try plus two retries)", lines)
	}
	if n := countKind(got, EvReviewEnd); n != 1 {
		t.Fatalf("%d review_end events, want one: a retried review ends once", n)
	}
}

// Retries are bounded: with none configured, one failure is one failure.
func TestRetriesOffMeansOneAttempt(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	marker := filepath.Join(t.TempDir(), "attempts")
	bin := fakeAgent(t, t.TempDir(), "claude", "echo x >> "+marker+"\nexit 1")

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.Failures() != 1 {
		t.Fatalf("counts: %+v", c)
	}
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(body), "x"); lines != 1 {
		t.Fatalf("agent ran %d times, want 1", lines)
	}
}

// --merge-into moves each loop's commits onto another branch without ever
// checking that branch out in the tree the reviews are running in.
func TestMergeIntoLandsTheWorkOnAnotherBranch(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "branch", "main-line")
	gitRun(t, repo, "checkout", "-q", "-b", "work")
	set, _ := promptSet(t, "sec-review")
	// A fake that commits its own work, which is what the commit step asks a
	// real agent to do.
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "// touched" >> main.go
git add -A
git -c user.name=t -c user.email=t@e commit -qm "review work" >/dev/null 2>&1
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Commit, cfg.MergeInto = true, "main-line"
	_, got := runRecorded(t, cfg)

	if branch := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); branch != "work" {
		t.Fatalf("the tree is on %q, want the branch the reviews ran on", branch)
	}
	if !strings.Contains(gitOut(t, repo, "log", "--oneline", "main-line"), "review work") {
		t.Fatalf("main-line did not take the work:\n%s",
			gitOut(t, repo, "log", "--oneline", "--all"))
	}
	var merged bool
	for _, ev := range got {
		if ev.Kind == EvMerge && ev.Branch == "main-line" && ev.Status == StatusOK {
			merged = true
		}
	}
	if !merged {
		t.Fatal("the merge was not reported on the bus")
	}
}

// The merge is refused rather than faked when the work is still uncommitted:
// merging then would report moving what it did not move.
func TestMergeIntoRefusesADirtyTree(t *testing.T) {
	repo := testRepo(t)
	gitRun(t, repo, "branch", "main-line")
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "// touched" >> main.go
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.MergeInto = "main-line"
	r, got := runRecorded(t, cfg)

	if n := countKind(got, EvMerge); n != 0 {
		t.Fatalf("%d merge events, want none: the tree was dirty", n)
	}
	if r.Stats().CommitFails() == 0 {
		t.Fatal("a refused merge must be counted, not passed over in silence")
	}
	if tip := gitOut(t, repo, "rev-parse", "main-line"); tip != gitOut(t, repo, "rev-parse", "HEAD") {
		t.Fatal("nothing was committed, so main-line must not have moved")
	}
}

// A graceful quit stops starting reviews, lets the ones running finish, and
// still commits what this loop produced: there is no successor to do it, so
// dropping the commit step would leave the work uncommitted on disk.
func TestRequestFinishDrainsAndCommits(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review", "d-review")
	// Each review edits and commits, like the commit step would.
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "// touched" >> main.go
git add -A
git -c user.name=t -c user.email=t@e commit -qm "review work" >/dev/null 2>&1
echo "RESULT: changed=1"
sleep 0.2`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review", "c-review", "d-review"}, bin)
	cfg.Commit, cfg.MaxLoops = true, 0 // loop forever until asked to stop
	bus := NewBus()
	events := bus.Subscribe(512)
	done := make(chan []Event, 1)
	go collect(events, done)

	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		// Let the first review start, then ask for the graceful quit.
		time.Sleep(150 * time.Millisecond)
		r.RequestFinish()
	}()
	r.Run(context.Background())
	bus.Close()
	got := <-done

	if !r.Finishing() {
		t.Fatal("the runner should report that it is finishing")
	}
	started := countKind(got, EvReviewStart)
	ended := countKind(got, EvReviewEnd)
	if started == 0 {
		t.Fatal("nothing ran at all")
	}
	if started != ended {
		t.Fatalf("%d reviews started but %d ended: a graceful quit must drain", started, ended)
	}
	if started == len(cfg.Reviews) {
		t.Fatalf("all %d reviews ran: the quit did not stop the queue", started)
	}
	if n := countKind(got, EvLoopEnd); n == 0 {
		t.Fatal("the loop that was draining never reported its end")
	}
	if pending := r.Pending(); len(pending) != 0 {
		t.Fatalf("%v is still queued: a graceful quit has no successor to hand it to", pending)
	}
}

// Untracked files are not what --jobs objects to: a review works from a
// commit, so a file git never heard of is not work it would miss, and it
// cannot collide with a merge that does not create that path. Refusing to run
// over someone's scratch script was a dead end, since the commit step stages
// tracked files only and would leave it there forever.
func TestUntrackedFilesDoNotBlockWorktreeMode(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "pollbench.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)
	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Jobs = 2

	bus := NewBus()
	drain(bus)
	defer bus.Close()
	if _, err := New(context.Background(), cfg, bus); err != nil {
		t.Fatalf("an untracked file blocked worktree mode: %v", err)
	}

	// A tracked modification still does, because that work would be invisible.
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), cfg, bus); !errors.Is(err, ErrDirtyTree) {
		t.Fatalf("uncommitted tracked work must still block --jobs, got %v", err)
	}
}

// The offer gauntlet makes when --jobs meets a dirty tree: hand it to an
// agent, and report whether the tree actually ended up clean, since the agent
// saying so is not the same as git saying so.
func TestCommitNowReportsWhatGitSees(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committer := fakeAgent(t, t.TempDir(), "claude", `
git add -A
git -c user.name=t -c user.email=t@e commit -qm "work" >/dev/null 2>&1`)

	err := CommitNow(context.Background(), CommitOpts{
		Dir: repo, Agent: agent.Spec{Tool: "claude"},
		Bin: map[string]string{"claude": committer}, Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("the tree was committed, so this should succeed: %v", err)
	}

	// An agent that exits happily without committing is a failure, not a
	// success: the run that follows needs the tracked work committed, not the
	// claim that it is.
	if err := os.WriteFile(filepath.Join(repo, "new.go"), []byte("package main // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liar := fakeAgent(t, t.TempDir(), "claude", `echo "committed everything, honest"`)
	err = CommitNow(context.Background(), CommitOpts{
		Dir: repo, Agent: agent.Spec{Tool: "claude"},
		Bin: map[string]string{"claude": liar}, Timeout: 30 * time.Second,
	})
	if err == nil {
		t.Fatal("a tree still dirty after the commit step must be reported as such")
	}
}

func countKind(events []Event, kind Kind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// gitRun runs one git command in a test repository, failing the test if it
// does not succeed.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func TestSoftStopHandsOverTheRestOfTheLoop(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review", "d-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"; sleep 0.4`)

	reviews := []string{"a-review", "b-review", "c-review", "d-review"}
	cfg := baseConfig(t, repo, set, reviews, bin)
	cfg.MaxLoops = 1

	bus := NewBus()
	drain(bus)
	first, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		first.RequestStop() // as a hot reload does
	}()
	first.Run(context.Background())
	bus.Close()

	done := first.Stats().Counts().Total()
	pending := first.Pending()
	if done == 0 || done == len(reviews) {
		t.Fatalf("test needs a partial loop: %d done", done)
	}
	if len(pending) != len(reviews)-done {
		t.Fatalf("pending %v does not match %d finished reviews", pending, done)
	}
	// Nothing was killed: a soft stop lets in-flight agents finish.
	if c := first.Stats().Counts(); c.Interrupted != 0 || c.Failures() != 0 {
		t.Fatalf("soft stop disturbed a review: %+v", c)
	}

	// The successor picks up exactly what was left, and finishes the loop.
	bus2 := NewBus()
	drain(bus2)
	cfg2 := cfg
	cfg2.ResumeQueue = pending
	second, err := New(context.Background(), cfg2, bus2)
	if err != nil {
		t.Fatal(err)
	}
	second.Run(context.Background())
	bus2.Close()

	var ran []string
	for _, r := range second.Stats().Results() {
		ran = append(ran, r.Review)
	}
	sort.Strings(ran)
	want := append([]string(nil), pending...)
	sort.Strings(want)
	if strings.Join(ran, ",") != strings.Join(want, ",") {
		t.Fatalf("successor ran %v, want exactly the pending %v", ran, want)
	}
	if second.Loops() != 1 {
		t.Fatalf("the resumed loop should count as complete, got %d", second.Loops())
	}
}

func TestLiveUsageIsReportedWhileTheAgentRuns(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	// An agent that streams growing cumulative usage, as several CLIs do.
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo 'thinking'
echo '{"usage":{"output_tokens":100}}'
sleep 0.3
echo '{"usage":{"output_tokens":250}}'
sleep 0.3
echo '{"usage":{"output_tokens":900}}'
echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	_, got := runRecorded(t, cfg)

	var usage []int
	var lastEnd int
	for _, ev := range got {
		switch ev.Kind {
		case EvUsage:
			usage = append(usage, ev.Tokens)
		case EvReviewEnd:
			lastEnd = ev.Tokens
		}
	}
	if len(usage) < 3 {
		t.Fatalf("want a usage event per growth step, got %v", usage)
	}
	for i := 1; i < len(usage); i++ {
		if usage[i] <= usage[i-1] {
			t.Fatalf("usage must only grow: %v", usage)
		}
	}
	if usage[len(usage)-1] != 900 || lastEnd != 900 {
		t.Fatalf("final usage %v, review end %d, want 900", usage, lastEnd)
	}
}

func TestNoUsageEventsWhenAgentReportsNone(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "no numbers here"; echo "RESULT: no-changes"`)

	r, got := runRecorded(t, baseConfig(t, repo, set, []string{"a-review"}, bin))

	for _, ev := range got {
		if ev.Kind == EvUsage {
			t.Fatalf("invented usage for an agent that reported none: %+v", ev)
		}
	}
	if got := r.Stats().Results()[0].Tokens; got != 0 {
		t.Fatalf("tokens should stay zero, got %d", got)
	}
}

func TestCancelDuringParallelLeavesNoWorktrees(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review", "d-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "working"; sleep 10`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review", "c-review", "d-review"}, bin)
	cfg.Jobs = 3

	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	bus.Close()

	// Interrupting mid-flight must not leave checkouts, branches, or a
	// half-merged tree behind: the next run has to start from a sane repo.
	if entries, err := os.ReadDir(repo); err == nil {
		for _, e := range entries {
			if e.Name() == ".gauntlet" {
				t.Error("worktree root survived the cancel")
			}
		}
	}
	if out := gitOut(t, repo, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("stray worktrees registered:\n%s", out)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out != "" {
		t.Errorf("stray branches left behind:\n%s", out)
	}
	if out := gitOut(t, repo, "status", "--porcelain"); out != "" {
		t.Errorf("tree left dirty:\n%s", out)
	}
}
