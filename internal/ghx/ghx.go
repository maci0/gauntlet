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
	"strings"
	"syscall"
	"time"
)

const commandTimeout = 2 * time.Minute

// Client is a GitHub repository reached through gh.
type Client struct {
	Dir  string
	Repo string // OWNER/REPO
	Host string
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
	if after, ok := strings.CutPrefix(raw, "git@"); ok {
		left, path, ok := strings.Cut(after, ":")
		if !ok {
			return "", "", fmt.Errorf("unsupported Git remote URL %q", raw)
		}
		host, repo = left, path
	} else {
		u, parseErr := url.Parse(raw)
		if parseErr != nil || u.Hostname() == "" {
			return "", "", fmt.Errorf("unsupported Git remote URL %q", raw)
		}
		host, repo = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}
	repo = strings.TrimSuffix(repo, ".git")
	if host == "" || strings.Count(repo, "/") != 1 {
		return "", "", fmt.Errorf("remote %q is not a GitHub OWNER/REPO URL", raw)
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
}

// Find returns the URL of an existing pull request with the exact head and
// base, including a closed one. This makes publication idempotent across a
// retry, hot reload, or repeated invocation.
func (c Client) Find(ctx context.Context, head, base string) (string, error) {
	out, err := c.run(ctx, "pr", "list", "--repo", c.selector(), "--state", "all",
		"--head", head, "--base", base, "--limit", "100",
		"--json", "url,headRefName,baseRefName")
	if err != nil {
		return "", fmt.Errorf("find PR %s -> %s: %w", head, base, err)
	}
	var prs []pull
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", fmt.Errorf("decode gh PR list: %w", err)
	}
	for _, pr := range prs {
		if pr.HeadRefName == head && pr.BaseRefName == base && pr.URL != "" {
			return c.validateURL(pr.URL)
		}
	}
	return "", nil
}

// Create opens a pull request and returns its URL. Every value is an argv
// element; repository text is never evaluated by a shell.
func (c Client) Create(ctx context.Context, head, base, title, body string) (string, error) {
	out, err := c.run(ctx, "pr", "create", "--repo", c.selector(), "--head", head,
		"--base", base, "--title", title, "--body", body)
	if err != nil {
		return "", fmt.Errorf("create PR %s -> %s: %w", head, base, err)
	}
	prURL := strings.TrimSpace(string(out))
	if prURL == "" {
		return "", errors.New("gh pr create returned no URL")
	}
	return c.validateURL(prURL)
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
	cmd.WaitDelay = 10 * time.Second
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
	return line
}
