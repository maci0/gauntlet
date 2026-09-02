// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ghx creates and discovers GitHub pull requests through the gh CLI.
package ghx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const commandTimeout = 2 * time.Minute

// waitGrace is how long Run may outlive its process before the output pipes
// are closed out from under whoever still holds them, the same bound gitx
// uses. A grandchild gh spawned (a helper, a pager) inherits those pipes.
const waitGrace = 10 * time.Second

// Client is a GitHub repository reached through gh.
type Client struct {
	Dir  string
	Repo string // OWNER/REPO of the base repository (the fetch destination)
	Host string
	// HeadOwner is the account the head branches actually land in, inferred
	// from the remote's push URL. Empty means the base repository itself;
	// set, it qualifies every PR head as OWNER:BRANCH, which is how GitHub
	// addresses a cross-fork head.
	HeadOwner string
}

// head qualifies a branch the way the GitHub API expects it: bare within the
// base repository, OWNER:BRANCH when the pushes go to a fork.
func (c Client) head(branch string) string {
	if c.HeadOwner == "" {
		return branch
	}
	return c.HeadOwner + ":" + branch
}

// ParseRemote turns a GitHub HTTPS or SSH remote into the repository and
// hostname gh expects. Remotes without an OWNER/REPO shape (local paths,
// nested forge paths) are rejected here; a well-formed URL on a non-GitHub
// host parses but then fails Preflight's gh authentication check. Either way
// the repository is always passed to gh explicitly, never inferred from cwd.
func ParseRemote(raw string) (repo, host string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("empty remote URL")
	}
	// Errors quote the remote so the operator can see what was refused;
	// userinfo is stripped first so a stored https://user:pass@host/... does
	// not land in the terminal or the run journal.
	shown := redactUserinfo(raw)
	if after, ok := strings.CutPrefix(raw, "git@"); ok {
		left, path, ok := strings.Cut(after, ":")
		if !ok {
			return "", "", fmt.Errorf("unsupported Git remote URL %q", shown)
		}
		host, repo = left, path
	} else {
		u, parseErr := url.Parse(raw)
		if parseErr != nil || u.Hostname() == "" {
			return "", "", fmt.Errorf("unsupported Git remote URL %q", shown)
		}
		host, repo = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}
	// A trailing slash is a legal spelling of the same remote -- it is what a
	// browser address bar copies -- and it reaches remote.<name>.url exactly
	// as it was typed, since RemoteURL deliberately reports the raw value.
	// Cut it before .git so both orders parse: left in place it makes an
	// empty last path segment, the OWNER/REPO shape check counts two slashes,
	// and a stacked run refuses a remote every other git command accepts.
	repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	owner, name, ok := strings.Cut(repo, "/")
	// Count==1 is not enough: `owner/` and `/repo` each have one slash and
	// are not a GitHub repository. Both sides have to be a name.
	if host == "" || !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("remote %q is not a GitHub OWNER/REPO URL", shown)
	}
	return repo, host, nil
}

// Available reports whether gh resolves from an absolute PATH entry. An empty
// PATH component never means cwd: a reviewed repository may contain a planted
// executable named gh.
func Available() bool { return binary() != "" }

func binary() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, "gh")
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// Preflight proves gh is authenticated and can see the selected repository.
func (c Client) Preflight(ctx context.Context) error {
	if !Available() {
		return errors.New("stacked PRs need the gh CLI")
	}
	if _, err := c.run(ctx, "auth", "status", "--hostname", c.Host); err != nil {
		return fmt.Errorf("gh is not authenticated for %s: %w", c.Host, err)
	}
	if _, err := c.run(ctx, "repo", "view", c.selector(), "--json", "nameWithOwner"); err != nil {
		return fmt.Errorf("cannot access GitHub repository %s: %w", c.Repo, err)
	}
	return nil
}

type pull struct {
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	HeadOwner   struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

// Find returns the URL of an existing pull request with the exact head and
// base, including a closed one. This makes publication idempotent across a
// retry, hot reload, or repeated invocation.
//
// The head filter is the bare branch name even for a fork: `gh pr list --head`
// documents that `<owner>:<branch>` is not supported, and passing the
// qualified form matches nothing, which would make every cross-fork run
// believe its PR does not exist and try to open a second one. The owner is
// confirmed from each candidate's head repository instead.
func (c Client) Find(ctx context.Context, head, base string) (string, error) {
	out, err := c.run(ctx, "pr", "list", "--repo", c.selector(), "--state", "all",
		"--head", head, "--base", base, "--limit", "100",
		"--json", "url,headRefName,baseRefName,headRepositoryOwner")
	if err != nil {
		return "", fmt.Errorf("find PR %s -> %s: %w", head, base, err)
	}
	var prs []pull
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", fmt.Errorf("decode gh PR list: %w", err)
	}
	for _, pr := range prs {
		if pr.HeadRefName == head && pr.BaseRefName == base && pr.URL != "" &&
			c.ownsHead(pr) {
			return c.validateURL(pr.URL)
		}
	}
	return "", nil
}

// ownsHead reports whether a candidate PR's head branch lives where this
// client's pushes land: the fork for a cross-fork client, the base repository
// itself otherwise. Filtering by a bare branch name returns matches from every
// fork that happens to carry it, and stack branch names are derived from a
// public base commit, so without this check anyone could open a PR from their
// own fork under a name a run is about to use and have it taken for that run's
// own layer. A candidate whose head repository is gone reports no owner and
// stays eligible: the branch checks that follow are what reject it.
func (c Client) ownsHead(p pull) bool {
	owner := c.HeadOwner
	if owner == "" {
		owner, _, _ = strings.Cut(c.Repo, "/")
	}
	if owner == "" || p.HeadOwner.Login == "" {
		return true
	}
	return strings.EqualFold(p.HeadOwner.Login, owner)
}

// Create opens a pull request and returns its URL. Every value is an argv
// element; repository text is never evaluated by a shell.
//
// Head and base are the idempotency key. A create that times out after GitHub
// accepted it, or that fails because the PR already exists, is recovered by
// Find: a retry returns the existing URL instead of opening a second PR or
// failing a layer that is already published. Stdout is trusted when it is
// already a valid PR URL, which is what a kill after gh printed the URL looks
// like.
func (c Client) Create(ctx context.Context, head, base, title, body string) (string, error) {
	out, err := c.run(ctx, "pr", "create", "--repo", c.selector(), "--head", c.head(head),
		"--base", base, "--title", title, "--body", body)
	if url, ok := c.createdURL(out); ok {
		return url, nil
	}
	if url, findErr := c.Find(ctx, head, base); findErr == nil && url != "" {
		return url, nil
	}
	if err != nil {
		return "", fmt.Errorf("create PR %s -> %s: %w", head, base, err)
	}
	prURL := strings.TrimSpace(string(out))
	if prURL == "" {
		return "", errors.New("gh pr create returned no URL")
	}
	return c.validateURL(prURL)
}

// createdURL reports a PR URL gh printed on stdout, even when the process
// then failed. Anything that is not a valid URL for this host is ignored so
// an error page or usage text cannot be taken for a successful create.
func (c Client) createdURL(out []byte) (string, bool) {
	raw := firstLine(strings.TrimSpace(string(out)))
	if raw == "" {
		return "", false
	}
	u, err := c.validateURL(raw)
	if err != nil {
		return "", false
	}
	return u, true
}

func (c Client) selector() string {
	if c.Host == "" || c.Host == "github.com" {
		return c.Repo
	}
	return c.Host + "/" + c.Repo
}

func (c Client) validateURL(raw string) (string, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), c.Host) {
		return "", fmt.Errorf("gh returned an invalid PR URL for %s", c.Host)
	}
	return raw, nil
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	bin := binary()
	if bin == "" {
		return nil, exec.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.Dir
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// Like gitx: a child that escapes the process group must not keep Run
	// blocked on the output pipes past the kill.
	cmd.WaitDelay = waitGrace
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errOut.String())
		if detail != "" {
			return out.Bytes(), fmt.Errorf("%w: %s", err, firstLine(detail))
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return redactUserinfo(line)
}

// userinfoRe matches the userinfo of a URL (the "alice:token@" in
// https://alice:token@host/...). git@host:path SSH syntax has no "://", so
// it is left alone.
var userinfoRe = regexp.MustCompile(`(?i)((?:https?|ssh|git|ftps?)://)[^/@\s'"]+@`)

// redactUserinfo strips URL userinfo from s so a credential-bearing remote
// does not land in an error string. Idempotent; strings with no "://" are
// returned unchanged.
func redactUserinfo(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	return userinfoRe.ReplaceAllString(s, "$1")
}
