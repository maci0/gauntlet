// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	workflowUses = regexp.MustCompile(`^\s+- uses:\s+(\S+)`)
	pinnedAction = regexp.MustCompile(`^[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
)

// Workflows are the supply-chain and release path. DESIGN.md pins actions by
// commit SHA and runner images by name; a tag or a -latest image sneaking
// back in would only be noticed after it had already run.
func TestWorkflowsPinSupplyChain(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var files int
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yml") {
			continue
		}
		files++
		path := filepath.Join(dir, ent.Name())
		text := readRepoFile(t, path)
		checkWorkflow(t, ent.Name(), text)
	}
	if files == 0 {
		t.Fatal("no workflow files under .github/workflows")
	}
}

func checkWorkflow(t *testing.T, name, text string) {
	t.Helper()
	for _, img := range []string{"ubuntu-latest", "macos-latest", "windows-latest"} {
		if strings.Contains(text, img) {
			t.Errorf("%s: runner image %s is unpinned; pin ubuntu-24.04 / macos-15 so an image rollout cannot turn a green tree red", name, img)
		}
	}
	if strings.Contains(text, "secrets.") {
		t.Errorf("%s: references repository secrets; this project's workflows use github.token only", name)
	}

	var uses, checkouts, persistFalse, timeouts, runsOn int
	for raw := range strings.SplitSeq(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		code := line
		if i := strings.Index(code, "#"); i >= 0 {
			code = strings.TrimSpace(code[:i])
		}
		switch {
		case strings.HasPrefix(code, "timeout-minutes:"):
			timeouts++
		case strings.HasPrefix(code, "runs-on:"):
			runsOn++
		case strings.HasPrefix(code, "persist-credentials:"):
			if !strings.Contains(code, "false") {
				t.Errorf("%s: persist-credentials must be false; git credentials are not needed after checkout", name)
			}
			persistFalse++
		}
		m := workflowUses.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		uses++
		ref := m[1]
		if !pinnedAction.MatchString(ref) {
			t.Errorf("%s: action %q is not pinned to a 40-character commit SHA", name, ref)
		}
		if strings.HasPrefix(ref, "actions/checkout@") {
			checkouts++
		}
	}
	if uses == 0 {
		t.Errorf("%s: no actions to pin", name)
	}
	if checkouts != persistFalse {
		t.Errorf("%s: %d checkout(s) but %d persist-credentials: false", name, checkouts, persistFalse)
	}
	if runsOn == 0 || timeouts != runsOn {
		t.Errorf("%s: %d runs-on and %d timeout-minutes; every job needs a timeout", name, runsOn, timeouts)
	}
}

func TestReleaseWriteTokenIsPublishOnly(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	if !strings.Contains(text, "GITHUB_TOKEN: \"\"") || !strings.Contains(text, "GH_TOKEN: \"\"") {
		t.Fatal("release job must clear GITHUB_TOKEN and GH_TOKEN so make release and the smoke test cannot use the write token")
	}
	publish := strings.Index(text, "name: Publish")
	if publish < 0 {
		t.Fatal("release.yml has no Publish step")
	}
	before, after := text[:publish], text[publish:]
	if strings.Contains(before, "github.token") {
		t.Fatal("github.token must not appear before the Publish step")
	}
	if !strings.Contains(after, "GH_TOKEN: ${{ github.token }}") {
		t.Fatal("Publish must set GH_TOKEN from github.token")
	}
}

func TestDistJobSmokeTestsHostBinary(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml"))
	if !strings.Contains(text, "gauntlet_ci_linux_amd64 version") {
		t.Fatal("dist job must run the host binary it just built, not only link it")
	}
}

func TestVulnscanRunsOnMainGoModPush(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "vulnscan.yml"))
	if !strings.Contains(text, "push:") || !strings.Contains(text, "branches: [main]") {
		t.Fatal("vulnscan must run on push to main of go.mod/go.sum, not only on pull requests and the weekly schedule")
	}
}
