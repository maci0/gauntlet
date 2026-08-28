// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
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

// onFirstEvent subscribes and calls fire when an event of kind kind flows,
// then keeps draining so later publishes never block. Tests interrupt a run
// through this instead of a timed sleep: a sleep races process startup and
// fires before any review began, where the after-effects it claims to judge
// hold vacuously.
func onFirstEvent(bus *Bus, kind Kind, fire func()) {
	events := bus.Subscribe(256)
	go func() {
		fired := false
		defer func() {
			for range events {
			}
		}()
		for ev := range events {
			if !fired && ev.Kind == kind {
				fired = true
				fire()
				continue
			}
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
	cfg.ResolveConflicts = false // the branch is the human's to merge

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

// TestParallelModeResolvesAConflict pins the conflict step: two reviews
// rewrite the same line, and the branch that will not merge is handed to an
// agent, resolved in a scratch checkout, and landed. Nothing is left for a
// human to merge by hand.
func TestParallelModeResolvesAConflict(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	// One script, three jobs: each review rewrites the same line its own way,
	// and the conflict prompt is answered by dropping the marker lines, which
	// keeps both sides.
	bin := fakeAgent(t, t.TempDir(), "claude", `
case "$*" in
*"Conflicted files:"*)
	echo ran >> "$GAUNTLET_TEST_CONFLICTS"
	grep -v -e '^<<<<<<<' -e '^=======$' -e '^>>>>>>>' main.go > merged.tmp
	mv merged.tmp main.go
	echo "RESOLVE: done" ;;
*a-review*)
	printf 'package main\n\nfunc main() { a() }\n' > main.go
	echo "RESULT: changed=1" ;;
*)
	printf 'package main\n\nfunc main() { b() }\n' > main.go
	echo "RESULT: changed=1" ;;
esac`)

	ran := filepath.Join(t.TempDir(), "conflicts")
	t.Setenv("GAUNTLET_TEST_CONFLICTS", ran)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review"}, bin)
	cfg.Jobs = 2
	cfg.ResolveConflicts = true

	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.OK != 2 || c.Conflict != 0 {
		t.Fatalf("both reviews should have landed, got %+v", c)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("no conflict to resolve: the test proved nothing")
	}
	body := readTree(t, repo, "main.go")
	if !strings.Contains(body, "a()") || !strings.Contains(body, "b()") {
		t.Fatalf("the resolution dropped a side:\n%s", body)
	}
	if strings.Contains(body, "<<<<<<<") {
		t.Fatalf("conflict markers reached the tree:\n%s", body)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out != "" {
		t.Fatalf("a resolved conflict should leave no branch behind:\n%s", out)
	}
	if out := gitOut(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("the tree should be clean after the resolution:\n%s", out)
	}
}

// TestParallelModeKeepsWhatTheAgentDidNotResolve: an agent that leaves the
// markers in place must not have them committed. The branch stays for a human.
func TestParallelModeKeepsWhatTheAgentDidNotResolve(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
case "$*" in
*"Conflicted files:"*)
	echo "RESOLVE: I could not tell which side was right" ;;
*a-review*)
	printf 'package main\n\nfunc main() { a() }\n' > main.go
	echo "RESULT: changed=1" ;;
*)
	printf 'package main\n\nfunc main() { b() }\n' > main.go
	echo "RESULT: changed=1" ;;
esac`)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review"}, bin)
	cfg.Jobs = 2
	cfg.ResolveConflicts = true

	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.Conflict != 1 || c.OK != 1 {
		t.Fatalf("want one merge and one unresolved conflict, got %+v", c)
	}
	if body := readTree(t, repo, "main.go"); strings.Contains(body, "<<<<<<<") {
		t.Fatalf("an unresolved conflict was committed:\n%s", body)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*-fix/*"); out != "" {
		t.Fatalf("the scratch branch of a failed resolution should be gone:\n%s", out)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out == "" {
		t.Fatal("the unresolved review's branch was deleted, losing its work")
	}
}

// TestParallelModeConflictPromptOmitsControlPaths: git happily carries
// control characters in a filename, and the conflict step names the conflicted
// paths in an agent prompt. A path with an embedded newline could forge
// instruction lines there, so it must never be named; the marker scan still
// sees it, so the resolution stays incomplete and the branch is kept for a
// human, exactly as for a conflict the agent could not finish.
func TestParallelModeConflictPromptOmitsControlPaths(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	// The hostile name needs its newline built from a substitution: a plain
	// "\n" inside the sh string would be literal backslash-n. "a\nb" keeps
	// its interior newline where command substitution strips only trailing
	// ones.
	bin := fakeAgent(t, t.TempDir(), "claude", `
hostile="hostile$(printf 'a\nb')mark.md"
case "$*" in
*"Conflicted files:"*)
	printf '%s\n' "$*" > "$GAUNTLET_TEST_PROMPT"
	grep -v -e '^<<<<<<<' -e '^=======$' -e '^>>>>>>>' main.go > merged.tmp
	mv merged.tmp main.go
	echo "RESOLVE: done" ;;
*a-review*)
	printf 'package main\n\nfunc main() { a() }\n' > main.go
	printf 'hostile a\n' > "$hostile"
	echo "RESULT: changed=1" ;;
*)
	printf 'package main\n\nfunc main() { b() }\n' > main.go
	printf 'hostile b\n' > "$hostile"
	echo "RESULT: changed=1" ;;
esac`)

	promptFile := filepath.Join(t.TempDir(), "conflict-prompt")
	t.Setenv("GAUNTLET_TEST_PROMPT", promptFile)

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review"}, bin)
	cfg.Jobs = 2
	cfg.ResolveConflicts = true

	r := runQuiet(t, cfg)

	// The hostile file was never resolved, so the resolution is incomplete
	// and the branch is kept for a human.
	if c := r.Stats().Counts(); c.Conflict != 1 || c.OK != 1 {
		t.Fatalf("want one merge and one unresolved conflict, got %+v", c)
	}
	// The agent was told about the clean path and never about the hostile one.
	got, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("the conflict agent never ran: %v", err)
	}
	if !strings.Contains(string(got), "main.go") {
		t.Fatalf("the conflict prompt dropped the clean path:\n%s", got)
	}
	if strings.Contains(string(got), "hostile") {
		t.Fatalf("a path with an embedded newline was named in the conflict prompt:\n%s", got)
	}
	if body := readTree(t, repo, "main.go"); strings.Contains(body, "<<<<<<<") {
		t.Fatalf("conflict markers reached the tree:\n%s", body)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*-fix/*"); out != "" {
		t.Fatalf("the scratch branch of an incomplete resolution should be gone:\n%s", out)
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/*"); out == "" {
		t.Fatal("the unresolved review's branch was deleted, losing its work")
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
	defer cancel()
	// Cancel only once a review is genuinely in flight: cancelling from a
	// timer could land before any review started, where "it stopped the run"
	// holds for the wrong reason.
	onFirstEvent(bus, EvReviewStart, cancel)
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
	// The agent has to still be working when the stop request lands, or the
	// test passes without ever exercising a stop during a review.
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "working"; sleep 2; echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.MaxLoops = 0

	bus := NewBus()
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	onFirstEvent(bus, EvReviewStart, r.RequestStop)
	r.Run(context.Background())
	bus.Close()

	if !r.soft.Load() {
		t.Fatal("stop was not recorded")
	}
	// A soft stop never kills an agent, so nothing may be interrupted.
	if c := r.Stats().Counts(); c.Interrupted != 0 {
		t.Fatalf("soft stop killed a review: %+v", c)
	}
	// The in-flight agent ran to its own end and was recorded as done.
	res := r.Stats().Results()
	if len(res) != 1 || res[0].Status != StatusOK {
		t.Fatalf("in-flight work did not finish: %+v", res)
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

// A hot reload releases the locks before the exec, and when that exec fails
// the deferred release runs again on the same locks. The second Release must
// be a no-op: closing an already-closed descriptor can hit a recycled fd.
func TestLockReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release()
	lock.Release() // must not panic, double-close, or touch another fd
	if _, err := Acquire(path); err != nil {
		t.Fatalf("lock not released: %v", err)
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

// Every launch is journaled with the fingerprint of the prompt text it was
// composed from, so a run's output stays attributable to exact words after
// the prompt file has changed or disappeared.
func TestReviewEventsCarryThePromptFingerprint(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review", "doc-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)

	_, got := runRecorded(t, baseConfig(t, repo, set, []string{"sec-review", "doc-review"}, bin))

	want := make(map[string]string)
	for _, name := range []string{"sec-review", "doc-review"} {
		rev, ok := set.Get(name)
		if !ok {
			t.Fatalf("%s not discovered", name)
		}
		body, err := rev.Body()
		if err != nil {
			t.Fatal(err)
		}
		want[name] = prompt.Fingerprint(body)
	}
	starts := 0
	for _, ev := range got {
		if ev.Kind != EvReviewStart && ev.Kind != EvReviewEnd {
			continue
		}
		if ev.PromptSHA != want[ev.Review] {
			t.Errorf("%s for %s: prompt_sha256 = %q, want the body's fingerprint",
				ev.Kind, ev.Review, ev.PromptSHA)
		}
		if ev.Kind == EvReviewStart {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("saw %d review_start events, want one per review", starts)
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

// A retried isolated review must start from the same commit the first attempt
// did: the failed attempt's half-applied fixes are reset out of the worktree,
// so the retry converges to what one successful attempt would have produced.
func TestRetriedWorktreeReviewStartsFromBase(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	counter := filepath.Join(t.TempDir(), "attempts")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo x >> `+counter+`
attempts=$(wc -l < `+counter+`)
if [ "$attempts" -ge 2 ]; then
	if [ -e scratch.go ]; then
		echo "the failed attempt left scratch.go behind" >&2
		exit 4
	fi
	echo "package fixed" > fixed.go
	echo "RESULT: no-changes"
	exit 0
fi
echo "package broken" > scratch.go
exit 1`)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Jobs = 2
	cfg.Retries, cfg.RetryDelay = 2, time.Millisecond
	r := runQuiet(t, cfg)

	if c := r.Stats().Counts(); c.OK != 1 || c.Failures() != 0 {
		t.Fatalf("the retry must succeed against a restored worktree: %+v", c)
	}
	body, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(body), "x"); lines != 2 {
		t.Fatalf("agent ran %d times, want 2 (first try plus one retry)", lines)
	}

	// The landed work is exactly what the successful attempt wrote.
	fixed, err := exec.Command("git", "-C", repo, "show", "HEAD:fixed.go").Output()
	if err != nil {
		t.Fatalf("the retry's own change never landed: %v", err)
	}
	if string(fixed) != "package fixed\n" {
		t.Fatalf("fixed.go holds %q", fixed)
	}
	if err := exec.Command("git", "-C", repo, "cat-file", "-e", "HEAD:scratch.go").Run(); err == nil {
		t.Fatal("the failed attempt's scratch.go was committed")
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
	// Ask for the graceful quit once a review is in flight; a timer could
	// fire before any review started and the drain assertions would hold on
	// an empty run.
	onFirstEvent(bus, EvReviewStart, r.RequestFinish)
	r.Run(context.Background())
	bus.Close()
	got := <-done

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

// What a run writes into a project's history is the project's, not this
// tool's. This is the regression guard for a repository that once carried 125
// commits authored by "gauntlet <gauntlet@localhost>", every one of them
// subjected "X-review: automated review fixes" and threaded through a merge
// node saying which run produced it. None of that may come back.
func TestRunLeavesNoTraceOfItselfInTheHistory(t *testing.T) {
	repo := testRepo(t)
	before := gitOut(t, repo, "rev-parse", "HEAD")
	set, _ := promptSet(t, "sec-review", "doc-review", "perf-review")
	// Each review touches its own file, so all three land, and prints the
	// subject the protocol asks for.
	bin := fakeAgent(t, t.TempDir(), "claude", `
name=$(echo "$*" | grep -o '[a-z]*-review' | head -1)
echo "package x" > "${name}.go"
echo "PATH: ${name}.go: new"
echo "SUBJECT: feat(${name}): add the ${name} helper"
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"sec-review", "doc-review", "perf-review"}, bin)
	cfg.Jobs = 3
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	log := gitOut(t, repo, "log", "--format=%an <%ae>%n%B", before+"..HEAD")
	if log == "" {
		t.Fatal("nothing landed, so this proves nothing")
	}
	for _, banned := range []string{
		"gauntlet", "automated review fixes", "from gauntlet run", "Merge ",
		"apply review findings",
	} {
		if strings.Contains(log, banned) {
			t.Fatalf("%q reached the project's history:\n%s", banned, log)
		}
	}
	// The repository's own identity signs the commits, the same as a commit
	// typed by hand.
	for line := range strings.SplitSeq(gitOut(t, repo, "log", "--format=%an <%ae>", before+"..HEAD"), "\n") {
		if line != "test <test@example.invalid>" {
			t.Fatalf("commit authored by %q, want the repository's configured identity", line)
		}
	}
	// And the subjects are the reviews' own, in conventional form.
	subjects := gitOut(t, repo, "log", "--format=%s", before+"..HEAD")
	for _, want := range []string{"feat(sec-review)", "feat(doc-review)", "feat(perf-review)"} {
		if !strings.Contains(subjects, want) {
			t.Fatalf("missing %q in:\n%s", want, subjects)
		}
	}
	// One commit per review, and no merge nodes at all.
	if n := len(strings.Split(strings.TrimSpace(subjects), "\n")); n != 3 {
		t.Fatalf("%d commits for 3 reviews:\n%s", n, subjects)
	}
	if merges := gitOut(t, repo, "log", "--merges", "--format=%h", before+"..HEAD"); merges != "" {
		t.Fatalf("merge commits reached the history: %s", merges)
	}
}

// A review that skips SUBJECT: still has to leave a commit that names the
// files it touched, not a placeholder about the review itself.
func TestCommitWithoutSubjectNamesTheFiles(t *testing.T) {
	repo := testRepo(t)
	before := gitOut(t, repo, "rev-parse", "HEAD")
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "package x" > helper.go
echo "PATH: helper.go: new"
echo "RESULT: changed=1"`)
	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Jobs = 2
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	subjects := gitOut(t, repo, "log", "--format=%s", before+"..HEAD")
	if !strings.Contains(subjects, "helper.go") {
		t.Fatalf("subject does not name the file:\n%s", subjects)
	}
	if strings.Contains(subjects, "apply review findings") || strings.Contains(subjects, "sec-review") {
		t.Fatalf("the review leaked into the subject:\n%s", subjects)
	}
}

// A killed run leaves its lock file behind, note and all. The flock died with
// it, so the note describes nothing: taking the lock clears it rather than
// letting a dead run keep answering for the directory.
func TestAcquireClearsAStaleNote(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	if err := os.WriteFile(path, []byte("gauntlet dev (pid 1, run old): sec-review (claude)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("a stale lock file must not block a new run: %v", err)
	}
	defer lock.Release()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("the dead run's note survived: %q", body)
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

// softStopHandsOverTheLoop requests a stop partway through a loop and checks
// the handoff: in-flight agents finish undisturbed, exactly what did not run
// stays pending, and a successor seeded with that queue runs it and counts
// the resumed loop as complete.
func softStopHandsOverTheLoop(t *testing.T, jobs int, agentScript string) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review", "d-review")
	bin := fakeAgent(t, t.TempDir(), "claude", agentScript)

	reviews := []string{"a-review", "b-review", "c-review", "d-review"}
	cfg := baseConfig(t, repo, set, reviews, bin)
	cfg.Jobs = jobs
	cfg.MaxLoops = 1

	bus := NewBus()
	drain(bus)
	first, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	// Stop after the first review has ended, so the handover provably carries
	// at least one finished review; a timer races the agents instead.
	onFirstEvent(bus, EvReviewEnd, first.RequestStop)
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

func TestSoftStopHandsOverTheRestOfTheLoop(t *testing.T) {
	softStopHandsOverTheLoop(t, 0, `echo "RESULT: no-changes"; sleep 0.4`)
}

// The parallel twin of TestSoftStopHandsOverTheRestOfTheLoop. With more
// reviews than lanes, whatever never got a lane must still be pending after a
// soft stop: a dispatch loop that empties the queue up front would run the
// whole loop behind the stop and hand the successor nothing.
func TestSoftStopInParallelModeHandsOverTheQueue(t *testing.T) {
	softStopHandsOverTheLoop(t, 2, `echo "RESULT: no-changes"; sleep 1.2`)
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
	defer cancel()
	// Cancel while worktrees are live: a timer could fire before any review
	// began, where the cleanup contract passes over an untouched repo.
	onFirstEvent(bus, EvReviewStart, cancel)
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

// testCancelAccounting cancels mid-flight with the given lane count while an
// agent that never finishes runs, then checks every dispatched review is
// accounted for: the lanes that were running are interrupted, and reviews
// still queued behind the semaphore are recorded as interrupted or skipped
// per wantSkipped rather than vanishing from the stats, the summary, and any
// reload handoff.
func testCancelAccounting(t *testing.T, jobs int, wantSkipped bool) {
	repo := testRepo(t)
	names := []string{"a-review", "b-review", "c-review", "d-review", "e-review"}
	set, _ := promptSet(t, names...)
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "working"; sleep 10`)

	cfg := baseConfig(t, repo, set, names, bin)
	cfg.Jobs = jobs
	cfg.Seed = 1

	bus := NewBus()
	events := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(events, done)

	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel with lanes busy and a queue behind them; firing from a timer
	// could land before dispatch began and leave nothing to account for.
	onFirstEvent(bus, EvReviewStart, cancel)
	r.Run(ctx)
	bus.Close()

	results := r.Stats().Results()
	got := map[string]Result{}
	for _, res := range results {
		if prev, dup := got[res.Review]; dup {
			t.Fatalf("review %s recorded twice (%s and %s)", res.Review, prev.Status, res.Status)
		}
		got[res.Review] = res
		valid := false
		switch res.Status {
		case StatusInterrupted:
			valid = true
		case StatusSkipped:
			valid = wantSkipped
		}
		if !valid {
			statuses := "interrupted"
			if wantSkipped {
				statuses = "interrupted or skipped"
			}
			t.Errorf("review %s recorded as %s after cancel, want %s",
				res.Review, res.Status, statuses)
		}
	}
	for _, name := range names {
		if _, ok := got[name]; !ok {
			t.Errorf("review %s vanished: no result was recorded for it", name)
		}
	}

	published := map[string]bool{}
	for _, ev := range <-done {
		if ev.Kind == EvReviewEnd {
			published[ev.Review] = true
		}
	}
	for _, name := range names {
		if !published[name] {
			t.Errorf("review %s has no review_end event: its outcome was never published", name)
		}
	}
}

func TestCancelRecordsQueuedReviews(t *testing.T) {
	testCancelAccounting(t, 2, true)
}

// TestCancelRecordsQueuedReviewsSequential pins the same accounting for the
// sequential loop, where a queued review may only ever record as interrupted:
// an interrupt strands every review still queued behind the one that was
// running, and each must survive in the stats, the summary, and any journal
// replay.
func TestCancelRecordsQueuedReviewsSequential(t *testing.T) {
	testCancelAccounting(t, 1, false)
}

// TestFailedWorktreeAddReportsAndKeepsRepo pins two contracts for a lane
// whose checkout cannot be cut. In loaded runs that is usually a cancel
// racing the `git worktree add`; any cause must end the same way: the repo is
// left as it was found, and the review's outcome reaches the event stream
// rather than vanishing from it while the stats still carry it.
func TestFailedWorktreeAddReportsAndKeepsRepo(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "working"; sleep 10`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Jobs = 2
	cfg.Seed = 1

	// Squat on the review branch name for every lane so the review is
	// skipped regardless of which lane pulls it from the queue.
	tree := gitOut(t, repo, "rev-parse", "HEAD^{tree}")
	squat := gitOut(t, repo, "commit-tree", tree, "-p", "HEAD", "-m", "squat")
	for i := range cfg.Jobs {
		name := fmt.Sprintf("gauntlet/test-l1-lane%d-00/a-review", i)
		gitOut(t, repo, "branch", name, squat)
	}

	bus := NewBus()
	events := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(events, done)

	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	results := r.Stats().Results()
	if len(results) != 1 || results[0].Status != StatusSkipped {
		t.Fatalf("failed add recorded as %+v, want one skipped result", results)
	}

	var ends []Event
	for _, ev := range <-done {
		if ev.Kind == EvReviewEnd && ev.Review == "a-review" {
			ends = append(ends, ev)
		}
	}
	if len(ends) != 1 || ends[0].Status != StatusSkipped {
		t.Fatalf("a failed add published %d review_end events (%+v), want exactly one skipped",
			len(ends), ends)
	}

	// Every squatting branch keeps its commit: a failed review must not
	// take out a branch it did not create.
	for i := range cfg.Jobs {
		name := fmt.Sprintf("gauntlet/test-l1-lane%d-00/a-review", i)
		if tip := gitOut(t, repo, "rev-parse", name); tip != squat {
			t.Fatalf("squatting branch %s moved: %s, want %s", name, tip, squat)
		}
	}
	if out := gitOut(t, repo, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("checkouts appeared:\n%s", out)
	}
}

// readTree reads one file from a checkout.
func readTree(t *testing.T, dir, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A merged review's line counts ride on the merge event, measured against the
// review's own commit. They must appear exactly when they were measured: the
// journal turns a missing count into "changed nothing", so the event may
// fabricate neither.
func TestMergedEventCarriesTheMeasuredLines(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "one
two
three" > added.txt
echo "RESULT: changed=1"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Jobs = 2

	_, got := runRecorded(t, cfg)

	var merged *Event
	for i := range got {
		if got[i].Kind == EvMerge && got[i].Status == StatusOK {
			merged = &got[i]
		}
	}
	if merged == nil {
		t.Fatal("no successful merge event was published")
	}
	if merged.Ins == nil || merged.Del == nil {
		t.Fatal("a merged review with a measured diff published no line counts")
	}
	if *merged.Ins != 3 || *merged.Del != 0 {
		t.Fatalf("merge event = +%d/-%d, want +3/-0", *merged.Ins, *merged.Del)
	}
}
