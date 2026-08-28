// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/gitx"
)

func stackRepo(t *testing.T) (repo, remote string) {
	t.Helper()
	repo = testRepo(t)
	remote = filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare remote: %v: %s", err, out)
	}
	gitOut(t, repo, "remote", "add", "origin", remote)
	gitOut(t, repo, "push", "-u", "origin", "main")
	return repo, remote
}

func fakeGH(t *testing.T) (logPath, statePath string) {
	t.Helper()
	dir := t.TempDir()
	logPath, statePath = filepath.Join(dir, "gh.log"), filepath.Join(dir, "prs")
	if err := os.WriteFile(statePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	body := `
printf '%s\n' "$*" >> "$GAUNTLET_GH_LOG"
case "$1 $2" in
  "auth status")
    if [ "${GAUNTLET_GH_FAIL_AUTH:-}" = 1 ]; then echo logged-out >&2; exit 1; fi
    exit 0 ;;
  "repo view") printf '{"nameWithOwner":"owner/repo"}\n'; exit 0 ;;
  "pr list")
    head="" base=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --head) head="$2"; shift 2 ;;
        --base) base="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    # Real gh documents that --head does not support OWNER:BRANCH, and
    # matches nothing when given it. Refusing it here is what keeps a
    # cross-fork run from silently failing to find its own PRs.
    case "$head" in
      *:*) echo 'the "<owner>:<branch>" syntax is not supported for --head' >&2; exit 1 ;;
    esac
    found="" owner=""
    while IFS='|' read -r h b u o; do
      if [ "$h" = "$head" ] && [ "$b" = "$base" ]; then found="$u"; owner="$o"; fi
    done < "$GAUNTLET_GH_STATE"
    if [ -n "$found" ]; then
      printf '[{"url":"%s","headRefName":"%s","baseRefName":"%s","headRepositoryOwner":{"login":"%s"}}]\n' \
        "$found" "$head" "$base" "$owner"
    else
      printf '[]\n'
    fi
    exit 0 ;;
  "pr create")
    head="" base=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --head) head="$2"; shift 2 ;;
        --base) base="$2"; shift 2 ;;
        *) shift ;;
      esac
    done
    if [ "${GAUNTLET_GH_FAIL_CREATE:-}" = 1 ]; then echo refused >&2; exit 1; fi
    # gh pr create does accept OWNER:BRANCH; the PR records the bare branch
    # name and the owner of the repository the head lives in.
    owner="owner"
    case "$head" in
      *:*) owner="${head%%:*}" ;;
    esac
    head="${head##*:}"
    url="https://github.com/owner/repo/pull/$(($(wc -l < "$GAUNTLET_GH_STATE") + 1))"
    printf '%s|%s|%s|%s\n' "$head" "$base" "$url" "$owner" >> "$GAUNTLET_GH_STATE"
    printf '%s\n' "$url"
    exit 0 ;;
esac
echo "unexpected gh invocation: $*" >&2
exit 2`
	fakeAgent(t, dir, "gh", body)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GAUNTLET_GH_LOG", logPath)
	t.Setenv("GAUNTLET_GH_STATE", statePath)
	return logPath, statePath
}

func stackConfig(t *testing.T, repo string, reviews []string, body string) Config {
	t.Helper()
	set, _ := promptSet(t, reviews...)
	bin := fakeAgent(t, t.TempDir(), "claude", body)
	cfg := baseConfig(t, repo, set, reviews, bin)
	cfg.StackedPRs = true
	cfg.PRBase = "main"
	cfg.PushRemote = "origin"
	cfg.PRRepo = "owner/repo"
	cfg.PRHost = "github.com"
	return cfg
}

func TestStackedPRsBuildLinearRemoteGraphWithoutTouchingCheckout(t *testing.T) {
	repo, _ := stackRepo(t)
	logPath, statePath := fakeGH(t)
	base := gitOut(t, repo, "rev-parse", "main")
	b1 := gitx.StackBranchName(base, 0, "first-review")
	b2 := gitx.StackBranchName(base, 1, "second-review")
	// A tag carrying a stack branch's exact name must not shadow the branch:
	// bare-ref resolution prefers tags, which would re-parent the stack.
	gitOut(t, repo, "tag", b1)
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
case "$*" in
  *first-review*) printf 'first\n' > first.txt; echo 'SUBJECT: fix: add first layer' ;;
  *second-review*) printf 'second\n' > second.txt; echo 'SUBJECT: fix: add second layer' ;;
esac
echo 'RESULT: changed=1'`)

	r, events := runRecorded(t, cfg)
	if got := r.Stats().Counts(); got.OK != 2 || got.Failures() != 0 {
		t.Fatalf("counts: %+v", got)
	}
	if got := gitOut(t, repo, "rev-parse", "main"); got != base {
		t.Fatalf("original branch moved: %s != %s", got, base)
	}
	for _, name := range []string{"first.txt", "second.txt"} {
		if _, err := os.Stat(filepath.Join(repo, name)); !os.IsNotExist(err) {
			t.Fatalf("%s reached the original checkout", name)
		}
	}
	if got := gitOut(t, repo, "rev-parse", b2+"^"); got != gitOut(t, repo, "rev-parse", "refs/heads/"+b1) {
		t.Fatalf("second layer parent = %s, want first layer", got)
	}
	if got := gitOut(t, repo, "diff", "--name-only", "refs/heads/"+b1+"..refs/heads/"+b2); got != "second.txt" {
		t.Fatalf("second PR incremental diff = %q", got)
	}
	for _, branch := range []string{b1, b2} {
		gitOut(t, repo, "show-ref", "--verify", "refs/remotes/origin/"+branch)
	}
	state, _ := os.ReadFile(statePath)
	wantState := b1 + "|main|https://github.com/owner/repo/pull/1|owner\n" +
		b2 + "|" + b1 + "|https://github.com/owner/repo/pull/2|owner\n"
	if string(state) != wantState {
		t.Fatalf("PR stack:\n%s\nwant:\n%s", state, wantState)
	}
	if countKind(events, EvPullRequest) != 2 {
		t.Fatalf("pull request events = %d", countKind(events, EvPullRequest))
	}
	var published []Event
	for _, ev := range events {
		if ev.Kind == EvPullRequest {
			published = append(published, ev)
		}
	}
	if published[0].Branch != b1 || published[0].Base != "main" || published[0].URL == "" ||
		published[1].Branch != b2 || published[1].Base != b1 || published[1].URL == "" {
		t.Fatalf("pull request event fields: %+v", published)
	}
	if list := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("stack worktree survived:\n%s", list)
	}
	logBody, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBody), "pr create") {
		t.Fatal("gh never created the PRs")
	}
}

func TestStackedPRsCollapseNoChangeLayer(t *testing.T) {
	repo, _ := stackRepo(t)
	_, statePath := fakeGH(t)
	base := gitOut(t, repo, "rev-parse", "main")
	cfg := stackConfig(t, repo, []string{"first-review", "empty-review", "third-review"}, `
case "$*" in
  *first-review*) echo first > first.txt; echo 'RESULT: changed=1' ;;
  *empty-review*) echo 'RESULT: no-changes' ;;
  *third-review*) echo third > third.txt; echo 'RESULT: changed=1' ;;
esac`)

	runQuiet(t, cfg)
	b1 := gitx.StackBranchName(base, 0, "first-review")
	empty := gitx.StackBranchName(base, 1, "empty-review")
	b3 := gitx.StackBranchName(base, 2, "third-review")
	if got := gitOut(t, repo, "rev-parse", b3+"^"); got != gitOut(t, repo, "rev-parse", b1) {
		t.Fatalf("third layer did not collapse onto first: %s", got)
	}
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+empty)
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatal("no-change branch survived")
	}
	state, _ := os.ReadFile(statePath)
	if strings.Contains(string(state), empty) || !strings.Contains(string(state), b3+"|"+b1+"|") {
		t.Fatalf("no-change layer leaked into PR stack:\n%s", state)
	}
}

func TestStackedPRsRetryStartsFromCleanLayer(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	marker := filepath.Join(t.TempDir(), "attempts")
	cfg := stackConfig(t, repo, []string{"retry-review"}, `
if [ ! -e "`+marker+`" ]; then
  touch "`+marker+`"
  echo partial > partial.txt
  exit 7
fi
if [ -e partial.txt ]; then exit 9; fi
echo clean > clean.txt
echo 'RESULT: changed=1'`)
	cfg.Retries = 1
	cfg.RetryDelay = time.Millisecond

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.OK != 1 || got.Failures() != 0 {
		t.Fatalf("retry result: %+v", got)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	branch := gitx.StackBranchName(base, 0, "retry-review")
	if files := gitOut(t, repo, "diff", "--name-only", "main.."+branch); files != "clean.txt" {
		t.Fatalf("failed attempt leaked into commit: %q", files)
	}
}

func TestStackedPRsPublicationFailureStopsBeforeNextReview(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	t.Setenv("GAUNTLET_GH_FAIL_CREATE", "1")
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
echo changed > change.txt
echo 'RESULT: changed=1'`)

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.Fail != 1 {
		t.Fatalf("publication failure counts: %+v", got)
	}
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 1 {
		t.Fatalf("reviews started after publication failed: %q", started)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	b1 := gitx.StackBranchName(base, 0, "first-review")
	gitOut(t, repo, "show-ref", "--verify", "refs/heads/"+b1)
	if got := gitOut(t, repo, "rev-parse", "main"); got != base {
		t.Fatal("publication failure moved the original branch")
	}
}

func TestStackedPRsRepeatedRunReusesPRsWithoutAgents(t *testing.T) {
	repo, _ := stackRepo(t)
	logPath, _ := fakeGH(t)
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
case "$*" in
  *first-review*) echo first > first.txt ;;
  *second-review*) echo second > second.txt ;;
esac
echo 'RESULT: changed=1'`)

	runQuiet(t, cfg)
	runQuiet(t, cfg)
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 2 {
		t.Fatalf("repeated run launched agents again: %q", started)
	}
	logBody, _ := os.ReadFile(logPath)
	if strings.Count(string(logBody), "pr create") != 2 {
		t.Fatalf("repeated run duplicated PRs:\n%s", logBody)
	}
}

func TestStackedPRsResumePublishesCommittedBranchWithoutRerunningAgent(t *testing.T) {
	repo, _ := stackRepo(t)
	_, statePath := fakeGH(t)
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review"}, `
echo x >> "`+marker+`"
echo first > first.txt
echo 'SUBJECT: fix: publish recovered layer'
echo 'RESULT: changed=1'`)
	t.Setenv("GAUNTLET_GH_FAIL_CREATE", "1")
	runQuiet(t, cfg)
	t.Setenv("GAUNTLET_GH_FAIL_CREATE", "")
	runQuiet(t, cfg)

	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 1 {
		t.Fatalf("resume reran the committed review: %q", started)
	}
	state, _ := os.ReadFile(statePath)
	if strings.Count(string(state), "https://github.com/owner/repo/pull/") != 1 {
		t.Fatalf("resume did not publish exactly one PR:\n%s", state)
	}
}

func TestStackedPRsCancellationCleansWorktreeAndPublishesNothing(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	cfg := stackConfig(t, repo, []string{"slow-review"}, `
echo partial > partial.txt
sleep 10`)
	cfg.Timeout = 30 * time.Second
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	bus.Close()
	if list := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 1 {
		t.Fatalf("canceled stack worktree survived:\n%s", list)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	branch := gitx.StackBranchName(base, 0, "slow-review")
	if _, found, err := r.repo.RemoteBranchTip(context.Background(), "origin", branch); err != nil || found {
		t.Fatalf("canceled layer was published: found=%v err=%v", found, err)
	}
}

func TestStackedPRsFailedReviewIsDiscardedBeforeNextLayer(t *testing.T) {
	repo, _ := stackRepo(t)
	_, statePath := fakeGH(t)
	cfg := stackConfig(t, repo, []string{"bad-review", "good-review"}, `
case "$*" in
  *bad-review*) echo partial > partial.txt; exit 7 ;;
  *good-review*)
    if [ -e partial.txt ]; then exit 9; fi
    echo good > good.txt
    echo 'RESULT: changed=1' ;;
esac`)
	cfg.Retries = 0

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.Fail != 1 || got.OK != 1 {
		t.Fatalf("results after failed layer: %+v", got)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	bad := gitx.StackBranchName(base, 0, "bad-review")
	good := gitx.StackBranchName(base, 1, "good-review")
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+bad)
	cmd.Dir = repo
	if cmd.Run() == nil {
		t.Fatal("failed review branch survived")
	}
	if got := gitOut(t, repo, "rev-parse", good+"^"); got != base {
		t.Fatalf("good review based on %s, want main %s", got, base)
	}
	state, _ := os.ReadFile(statePath)
	if strings.Contains(string(state), bad) || !strings.Contains(string(state), good+"|main|") {
		t.Fatalf("failed review entered stack:\n%s", state)
	}
}

func TestStackedPRsHotReloadRecoversPublishedPrefix(t *testing.T) {
	repo, _ := stackRepo(t)
	logPath, _ := fakeGH(t)
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
case "$*" in
  *first-review*) echo first > first.txt ;;
  *second-review*) echo second > second.txt ;;
esac
echo 'RESULT: changed=1'`)

	bus := NewBus()
	events := bus.Subscribe(0)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		stopped := false
		for ev := range events {
			if !stopped && ev.Kind == EvPullRequest {
				stopped = true
				r.RequestStop()
			}
		}
	}()
	go func() { r.Run(context.Background()); close(done) }()
	<-done
	bus.Close()
	if got := r.Pending(); len(got) != 1 || got[0] != "second-review" {
		t.Fatalf("handoff queue = %v", got)
	}

	cfg.ResumeQueue = r.Pending()
	runQuiet(t, cfg)
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 2 {
		t.Fatalf("reload reran the completed prefix: %q", started)
	}
	logBody, _ := os.ReadFile(logPath)
	if strings.Count(string(logBody), "pr create") != 2 {
		t.Fatalf("reload duplicated or missed a PR:\n%s", logBody)
	}
}

func TestStackedPRsDirtyCheckoutNeedsConsentAndUsesRemoteBase(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	remoteBase := gitOut(t, repo, "rev-parse", "refs/remotes/origin/main")
	if err := os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("committed locally\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, repo, "add", "local-only.txt")
	gitOut(t, repo, "commit", "-qm", "local only")
	localTip := gitOut(t, repo, "rev-parse", "main")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("dirty tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := stackConfig(t, repo, []string{"sec-review"},
		`printf 'reviewed\n' > reviewed.txt; echo 'RESULT: changed=1'`)
	bus := NewBus()
	drain(bus)
	_, err := New(context.Background(), cfg, bus)
	bus.Close()
	dirty, ok := errors.AsType[*StackDirtyError](err)
	if !ok {
		t.Fatalf("dirty stack preflight = %v, want StackDirtyError", err)
	}
	if dirty.Remote != "origin" || dirty.Base != "main" ||
		!strings.Contains(dirty.Error(), "main.go") || !strings.Contains(dirty.Error(), "untracked.txt") {
		t.Fatalf("dirty stack detail = %+v (%v)", dirty, dirty)
	}

	cfg.AllowDirtyStack = true
	r, _ := runRecorded(t, cfg)
	if got := r.Stats().Counts(); got.OK != 1 || got.Failures() != 0 {
		t.Fatalf("counts: %+v", got)
	}
	if got := gitOut(t, repo, "rev-parse", "main"); got != localTip {
		t.Fatalf("original branch moved: %s != %s", got, localTip)
	}
	for path, want := range map[string]string{
		"main.go": "dirty tracked\n", "untracked.txt": "dirty untracked\n",
	} {
		body, readErr := os.ReadFile(filepath.Join(repo, path))
		if readErr != nil || string(body) != want {
			t.Fatalf("original %s = %q, %v", path, body, readErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(repo, "reviewed.txt")); !os.IsNotExist(statErr) {
		t.Fatal("review output reached the original checkout")
	}
	branch := gitx.StackBranchName(remoteBase, 0, "sec-review")
	if got := gitOut(t, repo, "rev-parse", "refs/heads/"+branch+"^"); got != remoteBase {
		t.Fatalf("stack parent = %s, want fetched remote base %s", got, remoteBase)
	}
	if out, cmdErr := exec.Command("git", "-C", repo, "cat-file", "-e",
		"refs/heads/"+branch+":local-only.txt").CombinedOutput(); cmdErr == nil {
		t.Fatalf("local-only commit leaked into remote-based stack: %s", out)
	}
}

func TestStackedPRsPreflightChecksGitHubAuthenticationBeforeAgent(t *testing.T) {
	t.Run("gh authentication before agent", func(t *testing.T) {
		repo, _ := stackRepo(t)
		fakeGH(t)
		t.Setenv("GAUNTLET_GH_FAIL_AUTH", "1")
		marker := filepath.Join(t.TempDir(), "agent-ran")
		cfg := stackConfig(t, repo, []string{"sec-review"}, `touch "`+marker+`"`)
		bus := NewBus()
		drain(bus)
		if _, err := New(context.Background(), cfg, bus); err == nil || !strings.Contains(err.Error(), "not authenticated") {
			t.Fatalf("logged-out stack preflight = %v", err)
		}
		bus.Close()
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("agent started before gh authentication passed")
		}
	})
}

// A hot reload hands its successor the exact base commit the run was pinned
// to. If the remote base advances in the reload window, the successor must
// keep building the same stack from the pinned commit, not start a new one
// named after the moved tip.
func TestStackedPRsReloadKeepsPinnedBaseWhenRemoteAdvances(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
case "$*" in
  *first-review*) echo first > first.txt ;;
  *second-review*) echo second > second.txt ;;
esac
echo 'RESULT: changed=1'`)
	pinned := gitOut(t, repo, "rev-parse", "main")

	bus := NewBus()
	events := bus.Subscribe(0)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		stopped := false
		for ev := range events {
			if !stopped && ev.Kind == EvPullRequest {
				stopped = true
				r.RequestStop()
			}
		}
	}()
	go func() { r.Run(context.Background()); close(done) }()
	<-done
	bus.Close()

	// The remote base advances while no gauntlet is running, without moving
	// the local checkout: a new commit object pushed straight to the remote.
	tree := gitOut(t, repo, "rev-parse", "main^{tree}")
	advanced := gitOut(t, repo, "commit-tree", "-p", "main", "-m", "advance", tree)
	gitOut(t, repo, "push", "-q", "origin", advanced+":refs/heads/main")

	cfg.ResumeQueue = r.Pending()
	cfg.ResumeStackTip = pinned
	runQuiet(t, cfg)

	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 2 {
		t.Fatalf("resume reran or skipped reviews: %q", started)
	}
	b1 := gitx.StackBranchName(pinned, 0, "first-review")
	b2 := gitx.StackBranchName(pinned, 1, "second-review")
	if got := gitOut(t, repo, "rev-parse", b2+"^"); got != gitOut(t, repo, "rev-parse", "refs/heads/"+b1) {
		t.Fatalf("resumed layer parent = %s, want the pinned stack's first layer", got)
	}
	moved := gitx.StackBranchName(advanced, 1, "second-review")
	cmd := exec.Command("git", "show-ref", "--verify", "refs/heads/"+moved)
	cmd.Dir = repo
	if cmd.Run() == nil {
		t.Fatal("resume followed the advanced remote base into a new stack")
	}
}

// A hard-killed process leaves a registered stack worktree and possibly a
// branch behind. The next run reuses or removes only that gauntlet-owned
// state; a user's own worktree and branch survive untouched.
func TestStackedPRsRecoverStaleWorktreeWithoutTouchingUnrelatedState(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	userWt := filepath.Join(t.TempDir(), "user-wt")
	gitOut(t, repo, "worktree", "add", "-q", "-b", "user-branch", userWt, "main")
	staleDir := filepath.Join(repo, ".gauntlet", "worktrees", "stack-test")
	gitOut(t, repo, "worktree", "add", "-q", "--detach", staleDir, "main")

	cfg := stackConfig(t, repo, []string{"sec-review"},
		`echo fixed > fixed.txt; echo 'RESULT: changed=1'`)
	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.OK != 1 || got.Failures() != 0 {
		t.Fatalf("counts after stale recovery: %+v", got)
	}
	gitOut(t, repo, "show-ref", "--verify", "refs/heads/user-branch")
	list := gitOut(t, repo, "worktree", "list", "--porcelain")
	if !strings.Contains(list, userWt) {
		t.Fatalf("unrelated worktree was removed:\n%s", list)
	}
	if strings.Contains(list, staleDir) {
		t.Fatalf("stale gauntlet worktree survived the run:\n%s", list)
	}
}

// A rejected push stops the stack: the failure is recorded, the committed
// branch is kept locally for a human, and no later review starts on a base
// that never became a usable remote PR head.
func TestStackedPRsPushFailureStopsTheStack(t *testing.T) {
	repo, remote := stackRepo(t)
	_, statePath := fakeGH(t)
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"),
		[]byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
echo changed > changed.txt
echo 'RESULT: changed=1'`)

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.Fail != 1 {
		t.Fatalf("push failure counts: %+v", got)
	}
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 1 {
		t.Fatalf("a review started after the push failed: %q", started)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	b1 := gitx.StackBranchName(base, 0, "first-review")
	gitOut(t, repo, "show-ref", "--verify", "refs/heads/"+b1)
	if _, found, err := r.repo.RemoteBranchTip(context.Background(), "origin", b1); err != nil || found {
		t.Fatalf("rejected push left a remote branch: found=%v err=%v", found, err)
	}
	state, _ := os.ReadFile(statePath)
	if strings.Contains(string(state), b1) {
		t.Fatalf("unpushed branch got a PR:\n%s", state)
	}
}

// A failed commit stops the stack the same way: recorded, published as a
// failure, nothing half-made left in the worktree or on the remote.
func TestStackedPRsCommitFailureStopsTheStack(t *testing.T) {
	repo, _ := stackRepo(t)
	_, statePath := fakeGH(t)
	// Signing is demanded but the signer does not exist, so every commit in
	// this clone (the stack worktree included) fails deterministically.
	gitOut(t, repo, "config", "commit.gpgsign", "true")
	gitOut(t, repo, "config", "gpg.program", filepath.Join(t.TempDir(), "no-such-gpg"))
	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"first-review", "second-review"}, `
echo x >> "`+marker+`"
echo changed > changed.txt
echo 'RESULT: changed=1'`)

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.Fail != 1 {
		t.Fatalf("commit failure counts: %+v", got)
	}
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 1 {
		t.Fatalf("a review started after the commit failed: %q", started)
	}
	state, _ := os.ReadFile(statePath)
	if strings.Contains(string(state), "pull") {
		t.Fatalf("failed commit produced a PR:\n%s", state)
	}
	if got := gitOut(t, repo, "rev-parse", "main"); got != gitOut(t, repo, "rev-parse", "origin/main") {
		t.Fatal("commit failure moved a branch")
	}
}

// A remote with distinct fetch and push URLs is a fork workflow: the base
// repository comes from where fetches read, the PR head owner from where
// pushes land, and the PR is created with the OWNER:BRANCH qualified head.
func TestStackedPRsSeparateFetchAndPushURLsCrossForkHead(t *testing.T) {
	repo := testRepo(t)
	upstream := filepath.Join(t.TempDir(), "upstream.git")
	fork := filepath.Join(t.TempDir(), "fork.git")
	for _, bare := range []string{upstream, fork} {
		if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
			t.Fatalf("init bare: %v: %s", err, out)
		}
	}
	gitOut(t, repo, "remote", "add", "origin", "https://github.com/upstream/proj.git")
	gitOut(t, repo, "config", "remote.origin.pushurl", "https://github.com/fork/proj.git")
	// insteadOf redirects transport to the local bares; the configured URLs
	// stay what PrepareStack reads and infers the repositories from.
	gitOut(t, repo, "config", "url."+upstream+".insteadOf", "https://github.com/upstream/proj.git")
	gitOut(t, repo, "config", "url."+fork+".insteadOf", "https://github.com/fork/proj.git")
	gitOut(t, repo, "push", "-q", upstream, "main")
	logPath, statePath := fakeGH(t)

	marker := filepath.Join(t.TempDir(), "reviews")
	cfg := stackConfig(t, repo, []string{"sec-review"},
		`echo x >> "`+marker+`"
echo fixed > fixed.txt; echo 'RESULT: changed=1'`)
	cfg.PRRepo, cfg.PRHost = "", "" // force inference from the remote URLs

	r := runQuiet(t, cfg)
	if got := r.Stats().Counts(); got.OK != 1 || got.Failures() != 0 {
		t.Fatalf("cross-fork counts: %+v", got)
	}
	base := gitOut(t, repo, "rev-parse", "main")
	branch := gitx.StackBranchName(base, 0, "sec-review")
	if out, err := exec.Command("git", "-C", fork, "rev-parse", "refs/heads/"+branch).CombinedOutput(); err != nil {
		t.Fatalf("stack branch missing from the push destination: %v: %s", err, out)
	}
	if err := exec.Command("git", "-C", upstream, "rev-parse", "--verify", "refs/heads/"+branch).Run(); err == nil {
		t.Fatal("stack branch leaked into the fetch-side repository")
	}
	logBody, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBody), "repo view upstream/proj") {
		t.Fatalf("base repository was not inferred from the fetch URL:\n%s", logBody)
	}
	if !strings.Contains(string(logBody), "--head fork:"+branch) {
		t.Fatalf("PR head was not qualified with the push owner:\n%s", logBody)
	}

	// Recovery must read stack branches from the push side, where they were
	// actually published: with the local branch gone, a rerun fetches the
	// layer back from the fork and reuses its PR without launching the agent.
	gitOut(t, repo, "branch", "-D", branch)
	runQuiet(t, cfg)
	started, _ := os.ReadFile(marker)
	if strings.Count(string(started), "x") != 1 {
		t.Fatalf("fork-side recovery reran the agent: %q", started)
	}
	if got := gitOut(t, repo, "rev-parse", "refs/heads/"+branch+"^"); got != base {
		t.Fatalf("recovered branch parent = %s, want base %s", got, base)
	}
	state, _ := os.ReadFile(statePath)
	if strings.Count(string(state), "https://") != 1 {
		t.Fatalf("fork-side recovery duplicated the PR:\n%s", state)
	}
}

// A hard cancel has no successor, so reviews the stack never reached are
// recorded as interrupted rather than dropped from the stats, the summary,
// and the journal, which is the rule the sequential and parallel loops
// already follow.
func TestStackedPRsHardCancelRecordsStrandedReviews(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	cfg := stackConfig(t, repo, []string{"slow-review", "never-review", "also-never-review"}, `
echo partial > partial.txt
sleep 10`)
	cfg.Timeout = 30 * time.Second
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	bus.Close()

	counts := r.Stats().Counts()
	if counts.Total() != 3 {
		t.Fatalf("counts = %+v, want all three reviews accounted for", counts)
	}
	if counts.Interrupted != 3 {
		t.Fatalf("interrupted = %d, want the canceled review and both it stranded",
			counts.Interrupted)
	}
	if got := r.Pending(); len(got) != 0 {
		t.Fatalf("queue left behind with no successor to take it: %v", got)
	}
}

// A cancel kills the git and gh commands that build a layer, so the setup step
// reports its own failure. That review was interrupted, not failed: counting it
// as a failure inflates the failure count on a Ctrl-C and publishes a
// pull_request failure for a layer nobody attempted.
func TestStackSetupFailureAfterCancelCountsAsInterrupted(t *testing.T) {
	repo, _ := stackRepo(t)
	fakeGH(t)
	cfg := stackConfig(t, repo, []string{"a-review"}, `echo "RESULT: no-changes"`)
	bus := NewBus()
	sub := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(sub, done)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r.recordStackFailure(canceled, 1, "a-review", "branch", "main",
		"create stack branch", errors.New("signal: killed"))
	bus.Close()

	if got := r.Stats().Counts(); got.Interrupted != 1 || got.Failures() != 0 {
		t.Fatalf("counts = %+v, want the canceled setup recorded as interrupted", got)
	}
	if n := countKind(<-done, EvPullRequest); n != 0 {
		t.Fatalf("published %d pull_request events for a layer nobody attempted", n)
	}
}
