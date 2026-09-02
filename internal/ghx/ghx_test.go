// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ghx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for _, c := range []struct {
		raw, repo, host string
	}{
		{"https://github.com/owner/project.git", "owner/project", "github.com"},
		{"https://user:pass@github.com/owner/project.git", "owner/project", "github.com"},
		{"git@github.com:owner/project.git", "owner/project", "github.com"},
		{"ssh://git@git.example.com/owner/project.git", "owner/project", "git.example.com"},
		// A trailing slash is the spelling a browser address bar hands over,
		// and git stores a remote URL exactly as it was typed. It used to
		// leave an empty last path segment, so the OWNER/REPO check counted
		// two slashes and a stacked run refused the remote outright.
		{"https://github.com/owner/project", "owner/project", "github.com"},
		{"https://github.com/owner/project/", "owner/project", "github.com"},
		{"https://github.com/owner/project.git/", "owner/project", "github.com"},
		{"git@github.com:owner/project/", "owner/project", "github.com"},
	} {
		repo, host, err := ParseRemote(c.raw)
		if err != nil || repo != c.repo || host != c.host {
			t.Errorf("ParseRemote(%q) = %q, %q, %v", c.raw, repo, host, err)
		}
	}
	for _, raw := range []string{"", "/tmp/repo.git", "https://github.com/only-owner",
		"https://github.com/only-owner/", "https://github.com/owner/group/project",
		// One slash is not OWNER/REPO when either side is empty. `.git` as the
		// repo name strips to a trailing slash; a double slash leaves a
		// leading one. Both used to pass the count==1 check.
		"https://github.com/owner/.git", "https://github.com//repo"} {
		if _, _, err := ParseRemote(raw); err == nil {
			t.Errorf("ParseRemote(%q) accepted a non-GitHub repository", raw)
		}
	}
}

func TestParseRemoteErrorRedactsUserinfo(t *testing.T) {
	cases := []string{
		"https://alice:s3cret@github.com/owner/group/project",
		"https://alice:s3cret@",
		"https://alice:s3cret@github.com/only-owner",
	}
	for _, raw := range cases {
		_, _, err := ParseRemote(raw)
		if err == nil {
			t.Errorf("ParseRemote(%q) accepted", raw)
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, "s3cret") || strings.Contains(msg, "alice:") ||
			strings.Contains(msg, "alice@") {
			t.Errorf("ParseRemote(%q) error leaked userinfo: %q", raw, msg)
		}
	}
}

// FuzzParseRemote drives the Git remote URL parser with arbitrary strings
// from a reviewed repository's config. The result is passed to gh as --repo
// and --hostname, so whatever is accepted must be exactly one non-empty owner
// and one non-empty name on a non-empty host, and parsing must be
// deterministic.
func FuzzParseRemote(f *testing.F) {
	seeds := []string{
		"https://github.com/owner/project.git",
		"git@github.com:owner/project.git",
		"ssh://git@git.example.com/owner/project.git",
		"https://github.com/owner/project",
		"https://github.com/owner/project/",
		"https://github.com/owner/project.git/",
		"git@github.com:owner/project/",
		"https://user:pass@github.com/owner/project.git",
		"https://github.com:443/owner/project",
		"https://github.com/owner/project.git?x=1",
		"https://github.com/owner/project.git#frag",
		"https://github.com/owner/.git",
		"https://github.com//repo",
		"https://github.com/only-owner",
		"https://github.com/owner/group/project",
		"git@github.com:owner/project",
		"git@github.com/owner/project.git",
		"ssh://git@github.com/owner/project.git/",
		"file:///tmp/repo.git",
		"ext::sh -c evil",
		"/tmp/repo.git",
		"",
		"   ",
		"git@",
		"https://github.com/owner/project.git.git",
		"https://[::1]/owner/project.git",
		"git@github.com:owner/project.git/",
		"HTTPS://GITHUB.COM/Owner/Project.GIT",
		"https://github.com/ow ner/repo",
		"javascript:alert(1)",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		repo, host, err := ParseRemote(raw)
		repo2, host2, err2 := ParseRemote(raw)
		if (err == nil) != (err2 == nil) || repo != repo2 || host != host2 {
			t.Fatalf("ParseRemote(%q) is not deterministic: (%q, %q, %v) vs (%q, %q, %v)",
				raw, repo, host, err, repo2, host2, err2)
		}
		if strings.TrimSpace(raw) == "" {
			if err == nil {
				t.Fatalf("empty remote was accepted as %q %q", repo, host)
			}
			return
		}
		if err != nil {
			if repo != "" || host != "" {
				t.Fatalf("rejected remote still returned %q %q", repo, host)
			}
			return
		}
		if host == "" {
			t.Fatalf("accepted %q with an empty host", raw)
		}
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			t.Fatalf("accepted %q as repo %q, which is not OWNER/REPO", raw, repo)
		}
	})
}

func TestClientPreflightFindAndCreate(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	script := `#!/bin/sh
printf '<%s>\n' "$@" >> "$GH_TEST_LOG"
case "$1 $2" in
  "auth status") exit 0 ;;
  "repo view") echo '{"nameWithOwner":"owner/repo"}'; exit 0 ;;
  "pr list") echo '[{"url":"https://github.com/owner/repo/pull/7","headRefName":"child","baseRefName":"base"}]'; exit 0 ;;
  "pr create") echo 'https://github.com/owner/repo/pull/8'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GH_TEST_LOG", logPath)
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com"}
	if err := c.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Find(context.Background(), "child", "base"); err != nil || got != "https://github.com/owner/repo/pull/7" {
		t.Fatalf("Find = %q, %v", got, err)
	}
	marker := filepath.Join(dir, "must-not-exist")
	title := "fix: literal $(touch " + marker + ")"
	if got, err := c.Create(context.Background(), "child", "base", title, "body"); err != nil || got != "https://github.com/owner/repo/pull/8" {
		t.Fatalf("Create = %q, %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("PR title was evaluated by a shell")
	}
}

func TestClientReportsAuthenticationFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\necho logged-out >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := (Client{Dir: dir, Repo: "owner/repo", Host: "github.com"}).Preflight(context.Background())
	if err == nil {
		t.Fatal("logged-out gh passed preflight")
	}
}

func TestClientRejectsForeignOrNonHTTPSPRURL(t *testing.T) {
	c := Client{Host: "github.com"}
	for _, raw := range []string{"http://github.com/owner/repo/pull/1", "https://evil.example/pr/1", "\x1b]52;bad"} {
		if _, err := c.validateURL(raw); err == nil {
			t.Errorf("accepted PR URL %q", raw)
		}
	}
}

// A fork workflow pushes heads into another account. Both PR discovery and
// creation must qualify the head as OWNER:BRANCH, or GitHub looks for the
// branch in the base repository and never finds it.
func TestClientQualifiesCrossForkHead(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	// gh accepts OWNER:BRANCH for `pr create --head` and documents that
	// `pr list --head` does not support it, answering nothing when given it.
	// This fake enforces both, so sending the qualified form to the filter
	// fails the test instead of silently finding no PR.
	script := `#!/bin/sh
printf '<%s>\n' "$@" >> "$GH_TEST_LOG"
case "$1 $2" in
  "pr list")
    for a in "$@"; do
      case "$a" in *:*) echo 'owner:branch not supported for --head' >&2; exit 1 ;; esac
    done
    printf '[{"url":"https://github.com/owner/repo/pull/7","headRefName":"child","baseRefName":"base","headRepositoryOwner":{"login":"%s"}}]\n' "$GH_TEST_HEAD_OWNER"
    exit 0 ;;
  "pr create") echo 'https://github.com/owner/repo/pull/9'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GH_TEST_LOG", logPath)
	t.Setenv("GH_TEST_HEAD_OWNER", "fork")
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com", HeadOwner: "fork"}

	got, err := c.Find(context.Background(), "child", "base")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/owner/repo/pull/7" {
		t.Fatalf("cross-fork Find = %q, want the fork's own PR", got)
	}
	if _, err := c.Create(context.Background(), "child", "base", "t", "b"); err != nil {
		t.Fatal(err)
	}
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBody), "<pr>\n<list>") ||
		!strings.Contains(string(logBody), "<--head>\n<child>") {
		t.Fatalf("the head filter must be the bare branch name:\n%s", logBody)
	}
	if !strings.Contains(string(logBody), "<--head>\n<fork:child>") {
		t.Fatalf("pr create must qualify the head with the push owner:\n%s", logBody)
	}

	// Another fork carrying the same branch name against the same base is a
	// different pull request; reusing it would publish into someone else's.
	t.Setenv("GH_TEST_HEAD_OWNER", "stranger")
	if got, err := c.Find(context.Background(), "child", "base"); err != nil || got != "" {
		t.Fatalf("Find matched a foreign fork's PR: %q, %v", got, err)
	}
}

// A same-repo run filters by a bare branch name too, and stack branch names
// are derived from a public base commit. A PR opened from a stranger's fork
// under such a name must not be taken for the run's own layer.
func TestClientIgnoresForeignHeadWithoutAFork(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "pr list")
    printf '[{"url":"https://github.com/owner/repo/pull/3","headRefName":"child","baseRefName":"base","headRepositoryOwner":{"login":"%s"}}]\n' "$GH_TEST_HEAD_OWNER"
    exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com"} // pushes to the base repo

	t.Setenv("GH_TEST_HEAD_OWNER", "stranger")
	if got, err := c.Find(context.Background(), "child", "base"); err != nil || got != "" {
		t.Fatalf("Find adopted a stranger's fork PR: %q, %v", got, err)
	}
	t.Setenv("GH_TEST_HEAD_OWNER", "owner")
	if got, err := c.Find(context.Background(), "child", "base"); err != nil ||
		got != "https://github.com/owner/repo/pull/3" {
		t.Fatalf("Find missed the run's own PR: %q, %v", got, err)
	}
	// A deleted head repository reports no owner; the branch checks that
	// follow decide, so Find must not filter it out here.
	t.Setenv("GH_TEST_HEAD_OWNER", "")
	if got, err := c.Find(context.Background(), "child", "base"); err != nil ||
		got != "https://github.com/owner/repo/pull/3" {
		t.Fatalf("Find dropped an owner-less candidate: %q, %v", got, err)
	}
}

// A create that fails after GitHub accepted it (timeout, "already exists")
// must return the existing PR rather than error, or a stacked retry opens a
// second PR or stops a layer that is already published.
func TestCreateReusesExistingPRWhenCreateFails(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "pr create") echo 'a pull request already exists' >&2; exit 1 ;;
  "pr list")
    echo '[{"url":"https://github.com/owner/repo/pull/7","headRefName":"child","baseRefName":"base","headRepositoryOwner":{"login":"owner"}}]'
    exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com"}
	got, err := c.Create(context.Background(), "child", "base", "t", "b")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/owner/repo/pull/7" {
		t.Fatalf("Create = %q, want the existing PR", got)
	}
}

// gh prints the URL then dies: the process fails, stdout still names the PR.
func TestCreateKeepsURLWhenProcessFailsAfterPrintingIt(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "pr create") echo 'https://github.com/owner/repo/pull/8'; exit 1 ;;
  "pr list") echo '[]'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com"}
	got, err := c.Create(context.Background(), "child", "base", "t", "b")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/owner/repo/pull/8" {
		t.Fatalf("Create = %q, want the URL gh printed before failing", got)
	}
}

func TestCreateStillFailsWhenNoPRExists(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1 $2" in
  "pr create") echo denied >&2; exit 1 ;;
  "pr list") echo '[]'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	c := Client{Dir: dir, Repo: "owner/repo", Host: "github.com"}
	if _, err := c.Create(context.Background(), "child", "base", "t", "b"); err == nil {
		t.Fatal("Create succeeded with no PR and a failed gh")
	}
}
