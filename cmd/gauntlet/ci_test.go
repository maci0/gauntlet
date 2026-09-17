// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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

func TestReleaseConcurrencyIsPerTag(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	_, rest, ok := strings.Cut(text, "\nconcurrency:\n")
	if !ok {
		t.Fatal("release.yml has no workflow concurrency group")
	}
	block, _, _ := strings.Cut(rest, "\n\n")
	for _, want := range []string{
		"  group: release-${{ github.ref }}",
		"  cancel-in-progress: false",
	} {
		if !strings.Contains("\n"+block+"\n", "\n"+want+"\n") {
			t.Errorf("release concurrency must serialize each tag without canceling other tags; missing %q", want)
		}
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

func TestReleaseKeepsPublishedVersionsImmutable(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	for _, want := range []string{"--draft", "--json isDraft", `if [ "$draft" = false ]`} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml must publish through a draft and refuse to overwrite a published release; missing %q", want)
		}
	}
}

func TestReleasePublicationClassification(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	_, step, ok := strings.Cut(text, "- name: Publish\n")
	if !ok {
		t.Fatal("release.yml has no Publish step")
	}
	_, body, ok := strings.Cut(step, "        run: |\n")
	if !ok {
		t.Fatal("Publish step has no shell body")
	}
	var script strings.Builder
	script.WriteString(`gh() {
  printf '%s\n' "$*" >> gh.log
  if [ "$1 $2" = "release view" ]; then
    if [ "$RELEASE_STATE" = missing ]; then return 1; fi
    printf '%s\n' "$RELEASE_STATE"
  fi
}
`)
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		code, ok := strings.CutPrefix(line, "          ")
		if !ok {
			break
		}
		script.WriteString(code + "\n")
	}
	for _, tc := range []struct {
		tag        string
		prerelease string
	}{
		{"v1.2.3", "false"},
		{"v1.2.3-rc.1", "true"},
		{"v1.2.3-rc.1+build.4", "true"},
		{"v1.2.3+build-4", "false"},
	} {
		for _, state := range []string{"missing", "true", "false"} {
			t.Run(tc.tag+"/"+state, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "bash", "-e", "-o", "pipefail", "-c", script.String())
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tc.tag, "RELEASE_STATE="+state)
				out, err := cmd.CombinedOutput()
				calls := readRepoFile(t, filepath.Join(dir, "gh.log"))
				if state == "false" {
					if err == nil || !strings.Contains(string(out), "already published") {
						t.Fatalf("published release must be refused: err=%v, output=%s", err, out)
					}
					if strings.Count(calls, "\n") != 1 {
						t.Fatalf("published release was mutated: %s", calls)
					}
					return
				}
				if err != nil {
					t.Fatalf("publish: %v: %s", err, out)
				}
				var published bool
				for call := range strings.SplitSeq(calls, "\n") {
					if strings.Contains(call, "--draft=false") {
						published = true
						if !strings.Contains(call, "--prerelease="+tc.prerelease) {
							t.Errorf("publication must set prerelease=%s: %s", tc.prerelease, call)
						}
					}
				}
				if !published {
					t.Fatalf("release stayed a draft: %s", calls)
				}
			})
		}
	}
}

func TestReleaseRejectsEmptyNotes(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	_, step, ok := strings.Cut(text, "- name: Extract this version's CHANGELOG section\n")
	if !ok {
		t.Fatal("release.yml has no release notes step")
	}
	_, body, ok := strings.Cut(step, "        run: |\n")
	if !ok {
		t.Fatal("release notes step has no shell body")
	}
	var script strings.Builder
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		code, ok := strings.CutPrefix(line, "          ")
		if !ok {
			break
		}
		script.WriteString(code + "\n")
	}
	for _, tc := range []struct {
		name      string
		changelog string
		want      string
	}{
		{"valid", "## Unreleased\n\n## 1.2.3\n\n### Fixed\n\n- Preserve settings.\n\n## 1.2.2\n- Older fix.\n", "\n### Fixed\n\n- Preserve settings.\n\n"},
		{"final section", "## 1.2.3\n- Preserve settings.\n", "- Preserve settings.\n"},
		{"missing", "## 1.2.2\n- Older fix.\n", ""},
		{"empty", "## 1.2.3\n", ""},
		{"headings only", "## 1.2.3\n\n### Added\n\n### Fixed\n\n", ""},
		{"indented heading", "## 1.2.3\n  ### Fixed\n", ""},
		{"prose", "## 1.2.3\nPreserve settings.\n", "Preserve settings.\n"},
		{"blank", "## 1.2.3\n\n\n", ""},
		{"whitespace", "## 1.2.3\n \t \n\t\n", ""},
		{"next section", "## 1.2.3\n\n## 1.2.2\n- Older fix.\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(tc.changelog), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-e", "-o", "pipefail", "-c", script.String())
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GITHUB_REF_NAME=v1.2.3")
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err == nil || !strings.Contains(string(out), "CHANGELOG.md") {
					t.Fatalf("empty release notes must fail with a changelog error: err=%v, output=%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("extract notes: %v: %s", err, out)
			}
			got := readRepoFile(t, filepath.Join(dir, "notes.md"))
			if got != tc.want {
				t.Fatalf("notes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDistJobSmokeTestsHostBinary(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml"))
	if !strings.Contains(text, "gauntlet_ci_linux_amd64 version") {
		t.Fatal("dist job must run the host binary it just built, not only link it")
	}
}

func TestVulnscanUsesLocalTargetAndRunsOnMainGoModPush(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "vulnscan.yml"))
	if !strings.Contains(text, "run: make vuln") {
		t.Fatal("vulnscan must use make vuln so its local and CI invocations stay identical")
	}
	if !strings.Contains(text, "push:") || !strings.Contains(text, "branches: [main]") {
		t.Fatal("vulnscan must run on push to main of go.mod/go.sum, not only on pull requests and the weekly schedule")
	}
}
