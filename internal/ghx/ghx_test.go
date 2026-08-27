// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ghx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for _, c := range []struct {
		raw, repo, host string
	}{
		{"https://github.com/owner/project.git", "owner/project", "github.com"},
		{"git@github.com:owner/project.git", "owner/project", "github.com"},
		{"ssh://git@git.example.com/owner/project.git", "owner/project", "git.example.com"},
	} {
		repo, host, err := ParseRemote(c.raw)
		if err != nil || repo != c.repo || host != c.host {
			t.Errorf("ParseRemote(%q) = %q, %q, %v", c.raw, repo, host, err)
		}
	}
	for _, raw := range []string{"", "/tmp/repo.git", "https://github.com/only-owner"} {
		if _, _, err := ParseRemote(raw); err == nil {
			t.Errorf("ParseRemote(%q) accepted a non-GitHub repository", raw)
		}
	}
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
