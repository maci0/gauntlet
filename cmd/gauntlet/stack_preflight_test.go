// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/prompt"
)

// preflightRepo builds a repo whose origin is configured as a GitHub URL but
// whose transport is redirected to a local bare via url.insteadOf, so the
// preflight's URL validation and its fetch both work offline.
func preflightRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
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
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	run("remote", "add", "origin", "https://github.com/owner/repo.git")
	run("config", "url."+bare+".insteadOf", "https://github.com/owner/repo.git")
	run("push", "-q", "origin", "main")
	return dir
}

// preflightGH puts a fake gh on the PATH that passes authentication and
// repository access.
func preflightGH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "auth status") exit 0 ;;
  "repo view") echo '{"nameWithOwner":"owner/repo"}'; exit 0 ;;
  "pr list") echo '[]'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// The dirty-checkout consent is the first gate: refusing it (or lacking a
// terminal without --yes) must happen before gauntlet touches the network,
// and the prompt promises the fetch, not a fetch that already happened.
func TestStackPreflightConsentComesBeforeAnyFetch(t *testing.T) {
	repo := preflightRepo(t)
	preflightGH(t)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fetchHead := filepath.Join(repo, ".git", "FETCH_HEAD")
	opts := &options{pushRemote: "origin", prBase: "main"}
	d := &dirRun{dir: repo}
	var out bytes.Buffer

	err := stackPreflight(context.Background(), d, opts, dirHandoff{}, false, "test-run",
		nil, bufio.NewReader(strings.NewReader("")), false, &out)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive dirty preflight = %v, want a --yes refusal", err)
	}
	if _, statErr := os.Stat(fetchHead); !os.IsNotExist(statErr) {
		t.Fatal("the remote was fetched before consent")
	}

	opts.yes = true
	out.Reset()
	if err := stackPreflight(context.Background(), d, opts, dirHandoff{}, false, "test-run",
		nil, bufio.NewReader(strings.NewReader("")), false, &out); err != nil {
		t.Fatalf("consented preflight: %v", err)
	}
	defer cleanupSnapshots([]*dirRun{d})
	if !strings.Contains(out.String(), "After confirmation, gauntlet fetches this remote branch") {
		t.Fatalf("prompt does not promise the fetch:\n%s", out.String())
	}
	if _, statErr := os.Stat(fetchHead); statErr != nil {
		t.Fatalf("consented preflight never fetched: %v", statErr)
	}
	if d.prep == nil || d.snapshot == nil {
		t.Fatal("preflight left no prep or snapshot for the run")
	}
}

// A remote that is not a GitHub repository is refused during validation,
// before any network operation runs against it.
func TestStackPreflightValidatesTheRemoteBeforeNetwork(t *testing.T) {
	repo := preflightRepo(t)
	preflightGH(t)
	mustGit(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "elsewhere.git"))
	fetchHead := filepath.Join(repo, ".git", "FETCH_HEAD")

	err := stackPreflight(context.Background(), &dirRun{dir: repo},
		&options{pushRemote: "origin", prBase: "main", yes: true}, dirHandoff{}, false,
		"test-run", nil, bufio.NewReader(strings.NewReader("")), false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cannot infer the GitHub repository") {
		t.Fatalf("non-GitHub remote preflight = %v", err)
	}
	if _, statErr := os.Stat(fetchHead); !os.IsNotExist(statErr) {
		t.Fatal("an unvalidated remote was fetched")
	}
}

// Stacked discovery reads the snapshot of the fetched remote base: an
// uncommitted prompt file, or one committed only locally, must not enter the
// run's review set.
func TestStackDiscoveryReadsTheBaseSnapshotNotTheCheckout(t *testing.T) {
	repo := preflightRepo(t)
	preflightGH(t)
	if err := os.WriteFile(filepath.Join(repo, "good-review.md"),
		[]byte("Your goal is to test the good review.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "good-review.md")
	mustGit(t, repo, "commit", "-qm", "add project prompt")
	mustGit(t, repo, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(repo, "local-review.md"),
		[]byte("Your goal is to test the local-only review.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", "local-review.md")
	mustGit(t, repo, "commit", "-qm", "local only, never pushed")
	if err := os.WriteFile(filepath.Join(repo, "evil-review.md"),
		[]byte("Your goal is to test the planted review.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &dirRun{dir: repo}
	if err := stackPreflight(context.Background(), d,
		&options{pushRemote: "origin", prBase: "main", yes: true}, dirHandoff{}, false,
		"test-run", nil, bufio.NewReader(strings.NewReader("")), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	defer cleanupSnapshots([]*dirRun{d})
	set, _, err := prompt.Discover(context.Background(), "", d.scanDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set.Get("good-review"); !ok {
		t.Fatal("pushed project prompt missing from stacked discovery")
	}
	for _, name := range []string{"evil-review", "local-review"} {
		if _, ok := set.Get(name); ok {
			t.Fatalf("%s reached stacked discovery from the checkout", name)
		}
	}
}

// Several directories can be dirty at once; each gets its own consent prompt
// on one shared input stream.
func TestStackPreflightPromptsPerDirtyDirectory(t *testing.T) {
	preflightGH(t)
	var runs []*dirRun
	for range 2 {
		repo := preflightRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, &dirRun{dir: repo})
	}
	defer cleanupSnapshots(runs)
	stdin := bufio.NewReader(strings.NewReader("y\ny\n"))
	var out bytes.Buffer
	for _, d := range runs {
		if err := stackPreflight(context.Background(), d,
			&options{pushRemote: "origin", prBase: "main"}, dirHandoff{}, false,
			"test-run", nil, stdin, true, &out); err != nil {
			t.Fatalf("preflight for %s: %v", d.dir, err)
		}
	}
	if got := strings.Count(out.String(), "UNCOMMITTED FILES"); got != 2 {
		t.Fatalf("dirty prompts shown %d times, want one per directory:\n%s", got, out.String())
	}
}

// A resumed run keeps the base commit its predecessor pinned, even when the
// remote branch has advanced since; a fresh run takes the new tip.
func TestStackPreflightKeepsThePinnedBaseOnResume(t *testing.T) {
	repo := preflightRepo(t)
	preflightGH(t)
	pinned := mustGit(t, repo, "rev-parse", "main")
	tree := mustGit(t, repo, "rev-parse", "main^{tree}")
	advanced := mustGit(t, repo, "commit-tree", "-p", "main", "-m", "advance", tree)
	mustGit(t, repo, "push", "-q", "origin", advanced+":refs/heads/main")

	opts := &options{pushRemote: "origin", yes: true}
	resumedRun := &dirRun{dir: repo}
	if err := stackPreflight(context.Background(), resumedRun, opts,
		dirHandoff{StackBase: "main", StackBaseTip: pinned}, true, "test-run",
		nil, bufio.NewReader(strings.NewReader("")), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	cleanupSnapshots([]*dirRun{resumedRun})
	if resumedRun.prep.BaseTip != pinned {
		t.Fatalf("resumed base tip = %s, want the pinned %s", resumedRun.prep.BaseTip, pinned)
	}

	freshRun := &dirRun{dir: repo}
	if err := stackPreflight(context.Background(), freshRun, opts,
		dirHandoff{}, false, "test-run",
		nil, bufio.NewReader(strings.NewReader("")), false, io.Discard); err != nil {
		t.Fatal(err)
	}
	defer cleanupSnapshots([]*dirRun{freshRun})
	if freshRun.prep.BaseTip != advanced {
		t.Fatalf("fresh base tip = %s, want the advanced %s", freshRun.prep.BaseTip, advanced)
	}
}

// /dev/null is a character device and a pipe passes a Stat check; neither is
// a terminal, and both used to fool the old ModeCharDevice test on the null
// device. Interactive prompts must see a real tty or nothing.
func TestIsTerminalRejectsDevNullAndPipes(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Fatal("/dev/null passed the terminal check")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) || isTerminal(w) {
		t.Fatal("a pipe passed the terminal check")
	}
}
