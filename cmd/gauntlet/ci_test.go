// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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

// Everything `make artifacts` writes into dist/ has to be uploaded, or the
// file it built for a release never reaches one. checksums.txt is what
// `gauntlet update` verifies an asset against, sbom.json is the inventory of
// what shipped, and LICENSE is the text of the grant the binaries are offered
// under, which a consumer who installs the binary alone has nowhere else to
// read. The two upload sites (a draft being repaired, a first publish) have to
// carry the same set, so the list is read out of both.
func TestReleaseUploadsEveryArtifact(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	var uploads int
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "dist/gauntlet_*") {
			continue
		}
		uploads++
		for _, want := range []string{"dist/checksums.txt", "dist/sbom.json", "dist/LICENSE"} {
			if !strings.Contains(line, want) {
				t.Errorf("release upload is missing %s: %s", want, strings.TrimSpace(line))
			}
		}
	}
	if uploads != 2 {
		t.Errorf("release.yml has %d upload lines, want 2 (repairing a draft, and the first publish)", uploads)
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
	// One read-only step runs before the build and asks the API which versions
	// are already published; it needs the token to do that. The build steps
	// between the two must not see it, so the token is granted to the guard
	// and to Publish, and to nothing in between.
	guard := strings.Index(text, "name: Refuse a tag that is not a new release")
	build := strings.Index(text, "name: Build every platform")
	if guard < 0 || build < 0 {
		t.Fatal("release.yml must refuse a tag that is not a new release before it builds anything")
	}
	before, after := text[:guard], text[build:publish]
	if strings.Contains(before, "github.token") {
		t.Fatal("github.token must not appear before the step that reads the published versions")
	}
	if !strings.Contains(text[guard:build], "GH_TOKEN: ${{ github.token }}") {
		t.Fatal("the guard step must set GH_TOKEN from github.token; gh reads the published releases with it")
	}
	if strings.Contains(after, "github.token") {
		t.Fatal("github.token must not appear between the build and the Publish step")
	}
	if !strings.Contains(text[publish:], "GH_TOKEN: ${{ github.token }}") {
		t.Fatal("Publish must set GH_TOKEN from github.token")
	}
}

// Every job in every workflow runs code that inherits its environment: the
// test suite spawns the agent CLIs a contributor has installed, the scripts
// job resolves and executes four tools from PyPI, vulnscan runs govulncheck
// from the module proxy, and the dist jobs build and run what they built. No
// step outside the release job's Publish needs a token (checkout persists
// none, and everything else reaches the module proxy, PyPI, or the advisory
// database), so every job clears both names and the write-capable token stays
// confined to the step that publishes. A job added later is covered by this
// reading every file rather than by remembering to extend a list.
func TestEveryJobClearsTheTokenItsProcessesInherit(t *testing.T) {
	root := moduleRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var jobs int
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yml") {
			continue
		}
		name := ent.Name()
		for job, block := range workflowJobs(readRepoFile(t, filepath.Join(dir, name))) {
			jobs++
			for _, want := range []string{`GITHUB_TOKEN: ""`, `GH_TOKEN: ""`} {
				if !strings.Contains(block, want) {
					t.Errorf("%s: job %s must carry %s; every step in it runs code that inherits this environment", name, job, want)
				}
			}
		}
	}
	if jobs == 0 {
		t.Fatal("no workflow jobs to check")
	}
}

var jobName = regexp.MustCompile(`^[a-z][a-z0-9_-]*:$`)

// workflowJobs maps each job name in a workflow to its block. A job key sits
// at exactly two spaces of indent and nothing else, which is what keeps a
// shell keyword inside a `run: |` body from reading as one.
func workflowJobs(text string) map[string]string {
	lines := strings.Split(text, "\n")
	jobs := map[string]string{}
	inJobs, name := false, ""
	for _, line := range lines {
		switch {
		case line == "jobs:":
			inJobs = true
		case !inJobs:
			continue
		case line == "" || line[0] != ' ':
			// The next top-level key ends the section.
			if line != "" {
				inJobs = false
			}
		case jobName.MatchString(strings.TrimPrefix(line, "  ")) && !strings.HasPrefix(line, "   "):
			name = strings.TrimSuffix(strings.TrimPrefix(line, "  "), ":")
			jobs[name] = ""
		case name != "":
			jobs[name] += line + "\n"
		}
	}
	return jobs
}

// A tag is the only review the bytes behind it can get: the release job runs
// the suite but not the pull-request gates, and `update` and the README install
// both serve the tag. CONTRIBUTING says to cut it from a commit main carries,
// so the workflow has to be what makes that true.
func TestReleaseRefusesATagThatIsNotOnMain(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	if !strings.Contains(text, "fetch-depth: 0") {
		t.Error("release checkout must fetch the full history; the ancestry check reads main")
	}
	if !strings.Contains(text, "git merge-base --is-ancestor HEAD origin/main") {
		t.Error("release job must refuse a tag whose commit origin/main does not carry")
	}
	build := strings.Index(text, "name: Build every platform")
	check := strings.Index(text, "name: Refuse a tag that is not a new release")
	if check < 0 {
		t.Fatal("release.yml has no guard step")
	}
	if check > build {
		t.Error("check the tag before building; the build is the expensive half")
	}
	if !strings.Contains(text, "make release VERSION=") {
		t.Error("release job must build through make release, which runs check and the suite")
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

// A tag is the whole release process, and two mistakes in one reach the
// publish step with a valid CHANGELOG section beside them. An older tag than
// the newest published becomes `releases/latest`, which is what both
// `gauntlet update` and the README install resolve, so every consumer is
// handed an older version, and the immutability rule then refuses to put it
// back. A tag on a commit main does not carry publishes code the next release
// from main reverts. The guard is what reads both facts.
func TestReleaseRefusesAStaleOrUnmergedTag(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	_, step, ok := strings.Cut(text, "- name: Refuse a tag that is not a new release\n")
	if !ok {
		t.Fatal("release.yml has no step refusing a tag that is not a new release")
	}
	_, body, ok := strings.Cut(step, "        run: |\n")
	if !ok {
		t.Fatal("the guard step has no shell body")
	}
	var guard strings.Builder
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		code, ok := strings.CutPrefix(line, "          ")
		if !ok {
			break
		}
		guard.WriteString(code + "\n")
	}

	const (
		onMain    = "a tag main carries, newer than what is published"
		stale     = "a tag main carries, older than what is published"
		offMain   = "a tag newer than what is published, on a commit main lacks"
		candidate = "a release candidate, which sorts below its final"
	)
	cases := []struct {
		name    string
		tag     string
		side    bool
		wantErr string
	}{
		{onMain, "v1.4.0", false, ""},
		{stale, "v1.2.0", false, "older than the published v1.3.0"},
		{offMain, "v1.4.0", true, "origin/main does not carry"},
		{candidate, "v1.4.0-rc.1", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, sha := releaseGuardRepo(t, tc.side)
			// gh is the only part of the guard that leaves the machine, and
			// the answer it would give is the whole question: it prints the
			// published tags the way the real --jq filter does.
			var script strings.Builder
			script.WriteString("gh() { printf '%s\\n' \"$PUBLISHED\"; }\n")
			script.WriteString(guard.String())
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", script.String())
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"GITHUB_REF_NAME="+tc.tag,
				"GITHUB_SHA="+sha,
				"PUBLISHED=v1.3.0\nv1.2.0",
			)
			out, err := cmd.CombinedOutput()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("guard refused a publishable tag: err=%v, output=%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("guard must fail with %q: err=%v, output=%s", tc.wantErr, err, out)
			}
		})
	}
}

// releaseGuardRepo builds what the guard reads: a clone whose origin is a bare
// repository, a commit main carries, and a tagged commit either on main or on a
// branch of it. It returns the checkout and the tagged commit, the two things
// the guard resolves.
func releaseGuardRepo(t *testing.T, offMain bool) (dir, sha string) {
	t.Helper()
	root := t.TempDir()
	upstream := filepath.Join(root, "upstream.git")
	run := func(dir string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(root, "init", "--quiet", "--bare", "--initial-branch=main", upstream)
	work := filepath.Join(root, "work")
	run(root, "clone", "--quiet", upstream, work)
	run(work, "config", "user.email", "release@example.com")
	run(work, "config", "user.name", "Release Test")
	run(work, "commit", "--quiet", "--allow-empty", "-m", "on main")
	run(work, "push", "--quiet", "origin", "main")
	if offMain {
		run(work, "checkout", "--quiet", "-b", "side")
	}
	run(work, "commit", "--quiet", "--allow-empty", "-m", "tagged")
	if !offMain {
		run(work, "push", "--quiet", "origin", "main")
	}
	tagged := strings.TrimSpace(runCapture(t, work, "git", "rev-parse", "HEAD"))
	return work, tagged
}

func runCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), err)
	}
	return string(out)
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
			got := readRepoFile(t, filepath.Join(dir, "dist", "notes.md"))
			if got != tc.want {
				t.Fatalf("notes = %q, want %q", got, tc.want)
			}
		})
	}
}

// `artifacts` depends on `dist` and both targets are phony, so a `make dist`
// step of its own ahead of `make artifacts` cross-compiles every platform a
// second time, cold, on every push. One build per job is the difference
// between the job fitting its timeout and not.
func TestDistJobCrossCompilesEveryPlatformOnce(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml"))
	if !strings.Contains(text, "run: make artifacts VERSION=ci") {
		t.Fatal("dist job must build the platform binaries through `make artifacts`, which depends on dist")
	}
	if strings.Contains(text, "run: make dist VERSION=ci") {
		t.Fatal("dist job must not run `make dist` before `make artifacts`; both are phony, so that cross-compiles every platform twice")
	}
}

// The dist job must run the host binary it just built, not only link it.
// Asking the Makefile for that check is what keeps the job from carrying a
// second copy of it: `make smoke` resolves the asset through host-artifact and
// runs it. A job that inlines the comparison again is the drift this pins.
func TestDistJobSmokeTestsHostBinary(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml"))
	if !strings.Contains(text, "make smoke VERSION=ci") {
		t.Fatal("dist job must run `make smoke VERSION=ci`, the check that runs the binary dist built")
	}
	if strings.Contains(text, `"$binary" version`) {
		t.Fatal("dist job must not inline the version check; make smoke owns it")
	}
}

// A manual run and the push it repeats resolve to the same ref, so a workflow
// that can be dispatched by hand and cancels superseded runs needs the event
// in its group: without it, repeating a failed run stops the run it was asked
// to repeat. Read over every workflow, so the next one added is covered.
func TestManualDispatchIsNotCancelledByTheRunItRepeats(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yml") {
			continue
		}
		name := ent.Name()
		text := readRepoFile(t, filepath.Join(dir, name))
		if !strings.Contains(text, "workflow_dispatch:") {
			continue
		}
		if !strings.Contains(text, "cancel-in-progress: true") {
			continue
		}
		if !strings.Contains(text, "github.event_name") {
			t.Errorf("%s: a workflow that can be dispatched by hand and cancels superseded runs must put github.event_name in its concurrency group", name)
		}
	}
}

// The three tag sets and two platforms are a matrix whose cells are selected by
// `if:` conditions on the test and cover steps, and a condition that stops
// matching one of them drops a build configuration off the gate with nothing
// red: the job still runs, on the cells that survived. So the matrix and its
// conditions are read and evaluated here rather than trusted to the comments
// beside them.
func TestTestJobRunsTheSuiteOnEveryMatrixCell(t *testing.T) {
	job := workflowJobs(readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "ci.yml")))["test"]
	if job == "" {
		t.Fatal("ci.yml has no test job")
	}
	oses := matrixAxis(t, job, "os")
	tags := matrixAxis(t, job, "tags")
	if want := []string{"ubuntu-24.04", "macos-15"}; !slices.Equal(oses, want) {
		t.Errorf("test job os axis = %q, want %q", oses, want)
	}
	// The empty entry is the third configuration: transcripts without the
	// database driver. It is the one a reader drops as a typo, and a matrix
	// without it tests a build nothing ships.
	if want := []string{"sqlite", "", "notoktop"}; !slices.Equal(tags, want) {
		t.Errorf("test job tags axis = %q, want %q", tags, want)
	}

	steps := conditionalMakeSteps(job)
	if len(steps) == 0 {
		t.Fatal("test job has no conditional make step")
	}
	for _, os := range oses {
		var checked bool
		for _, tag := range tags {
			cell := map[string]string{"os": os, "tags": tag}
			var suite bool
			for _, step := range steps {
				run, err := evalWorkflowCondition(step.ifExpr, cell)
				if err != nil {
					t.Fatalf("step %q on %s/%q: %v", step.target, os, tag, err)
				}
				if !run {
					continue
				}
				if step.target == "test" || step.target == "cover" {
					suite = true
				}
				if step.target == "check" {
					checked = true
				}
			}
			if !suite {
				t.Errorf("%s/%s: no cell runs the suite; this build configuration is untested and the job is still green", os, tag)
			}
		}
		if !checked {
			t.Errorf("%s: no cell runs make check", os)
		}
	}
}

var (
	matrixAxisList = regexp.MustCompile(`(?m)^\s+` + `(\w+):\s*\[([^\]]*)\]\s*$`)
	stepRun        = regexp.MustCompile(`^\s+- run: make (\w+)`)
)

// matrixAxis reads one `key: [a, b]` list out of a job's matrix.
func matrixAxis(t *testing.T, job, key string) []string {
	t.Helper()
	for _, m := range matrixAxisList.FindAllStringSubmatch(job, -1) {
		if m[1] != key {
			continue
		}
		var out []string
		for item := range strings.SplitSeq(m[2], ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, unquoteWorkflowLiteral(item))
			}
		}
		if len(out) == 0 {
			t.Fatalf("matrix axis %q is empty", key)
		}
		return out
	}
	t.Fatalf("test job matrix has no %q axis", key)
	return nil
}

type makeStep struct{ target, ifExpr string }

// conditionalMakeSteps pairs each `- run: make <target>` with the `if:` on the
// line below it. A step with no condition runs in every cell and carries the
// empty expression, which evaluates to true.
func conditionalMakeSteps(job string) []makeStep {
	var steps []makeStep
	lines := strings.Split(job, "\n")
	for i, raw := range lines {
		m := stepRun.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		step := makeStep{target: m[1], ifExpr: "true"}
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			if after, ok := strings.CutPrefix(trimmed, "if:"); ok {
				step.ifExpr = strings.TrimSpace(after)
				break
			}
			if trimmed == "" {
				break
			}
		}
		steps = append(steps, step)
	}
	return steps
}

func unquoteWorkflowLiteral(s string) string {
	if len(s) >= 2 && (s[0] == '\'' && s[len(s)-1] == '\'' || s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}

// evalWorkflowCondition evaluates the flat subset of GitHub's expression
// grammar the workflows use: `||`, `&&`, and a `==` or `!=` against
// matrix.tags, matrix.os, or runner.os. Anything else is an error rather than
// a guess, so a condition written in a form this does not model fails the
// suite instead of being read as true.
func evalWorkflowCondition(expr string, cell map[string]string) (bool, error) {
	// Spacing around the operators is the only thing separating a comparison
	// into three fields, so it goes before the expression is split.
	expr = strings.Join(strings.Fields(expr), "")
	for or := range strings.SplitSeq(expr, "||") {
		any := false
		for and := range strings.SplitSeq(or, "&&") {
			all := true
			for factor := range strings.FieldsSeq(and) {
				v, err := evalWorkflowFactor(factor, cell)
				if err != nil {
					return false, err
				}
				all = all && v
			}
			any = any || all
		}
		if any {
			return true, nil
		}
	}
	return false, nil
}

func evalWorkflowFactor(factor string, cell map[string]string) (bool, error) {
	negate := false
	if rest, ok := strings.CutPrefix(factor, "!"); ok {
		negate, factor = true, strings.TrimSpace(rest)
	}
	var value bool
	switch factor {
	case "true":
		value = true
	case "false":
		value = false
	default:
		if operand, known := workflowOperand(factor); known {
			// A bare operand is GitHub's truthiness, and the empty string is
			// false, which is the one matrix value that reads either way.
			value = operand(cell) != "" && operand(cell) != "false"
			return value != negate, nil
		}
		var ok bool
		if value, ok = compareWorkflowOperand(factor, cell); !ok {
			return false, fmt.Errorf("cannot evaluate %q: this reader covers `||`, `&&`, and == / != against matrix.tags, matrix.os, and runner.os", factor)
		}
	}
	return value != negate, nil
}

func compareWorkflowOperand(factor string, cell map[string]string) (bool, bool) {
	for _, op := range []string{"!=", "=="} {
		left, right, found := strings.Cut(factor, op)
		if !found {
			continue
		}
		lhs, ok := workflowOperandValue(strings.TrimSpace(left), cell)
		if !ok {
			return false, false
		}
		rhs, ok := workflowOperandValue(strings.TrimSpace(right), cell)
		if !ok {
			return false, false
		}
		return (lhs == rhs) == (op == "=="), true
	}
	return false, false
}

func workflowOperandValue(operand string, cell map[string]string) (string, bool) {
	if read, ok := workflowOperand(operand); ok {
		return read(cell), true
	}
	if literal, err := strconv.Unquote(operand); err == nil {
		return literal, true
	}
	return unquoteWorkflowLiteral(operand), true
}

// workflowOperand resolves the context references a condition can read. A
// literal resolves to a constant instead, and reports false, so a bare
// `matrix.tags` is not mistaken for the word.
func workflowOperand(operand string) (func(map[string]string) string, bool) {
	switch operand {
	case "matrix.tags":
		return func(cell map[string]string) string { return cell["tags"] }, true
	case "matrix.os":
		return func(cell map[string]string) string { return cell["os"] }, true
	case "runner.os":
		return func(cell map[string]string) string { return runnerOS(cell["os"]) }, true
	}
	return nil, false
}

// runnerOS maps a runner image to the value GitHub gives `runner.os`, which is
// what the workflow's own condition compares against.
func runnerOS(image string) string {
	if strings.HasPrefix(image, "macos") {
		return "Darwin"
	}
	return "Linux"
}

// The reader decides which matrix cells run the suite, so its reading of a
// condition is what the check above rests on. Every form the workflows use is
// pinned here, including the two where GitHub's truthiness and the empty
// matrix value meet.
func TestWorkflowConditionReader(t *testing.T) {
	linux := map[string]string{"os": "ubuntu-24.04", "tags": "sqlite"}
	empty := map[string]string{"os": "ubuntu-24.04", "tags": ""}
	mac := map[string]string{"os": "macos-15", "tags": ""}
	for _, tc := range []struct {
		expr string
		cell map[string]string
		want bool
	}{
		{`matrix.tags == 'sqlite'`, linux, true},
		{`matrix.tags == 'sqlite'`, empty, false},
		{`matrix.tags != 'sqlite' || runner.os != 'Linux'`, mac, true},
		{`matrix.tags != 'sqlite' || runner.os != 'Linux'`, linux, false},
		{`matrix.tags != 'sqlite' && runner.os != 'Linux'`, mac, true},
		{`matrix.tags == 'sqlite' && runner.os == 'Linux'`, linux, true},
		{`matrix.tags == 'sqlite'`, empty, false},
		{`!matrix.tags`, empty, true},
		{`matrix.tags == "sqlite"`, linux, true},
	} {
		got, err := evalWorkflowCondition(tc.expr, tc.cell)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s with %q = %v, want %v", tc.expr, tc.cell, got, tc.want)
		}
	}
	if _, err := evalWorkflowCondition("contains(github.event.pull_request.labels, 'x')", linux); err == nil {
		t.Error("a condition this reader does not model must be an error, not a verdict")
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

// The install script is the only path that puts a release binary on a user's
// machine without going through `gauntlet update`, which verifies against
// checksums.txt. Every release ships that file, so the script must too, and
// must verify before the binary is made executable.
func TestReadmeInstallVerifiesReleaseChecksum(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), "README.md"))
	_, rest, ok := strings.Cut(text, "\n## Install\n")
	if !ok {
		t.Fatal("README.md has no Install section")
	}
	block, _, _ := strings.Cut(rest, "\n## ")
	for _, want := range []string{"checksums.txt", "sha256sum", "shasum -a 256"} {
		if !strings.Contains(block, want) {
			t.Errorf("README install script missing %q; it must verify the download", want)
		}
	}
	verify := strings.Index(block, "sha256sum -c")
	chmod := strings.Index(block, "chmod +x")
	if verify < 0 || chmod < 0 {
		t.Fatalf("install script must verify and then chmod: verify=%d chmod=%d", verify, chmod)
	}
	if verify > chmod {
		t.Error("install script makes the binary executable before verifying it")
	}
	// checksums.txt names the release asset, so the download has to land under
	// that name for `sha256sum -c` to read the bytes it is checking. Saving it
	// straight to gauntlet makes every listed file "could not be read" on both
	// platforms, and the check passes over a file nobody verified.
	if !strings.Contains(block, `-o "$asset"`) {
		t.Error("install script must download to the asset name checksums.txt lists")
	}
	mv := strings.Index(block, `mv "$asset" gauntlet`)
	if mv < 0 {
		t.Fatal("install script must move the verified asset into place under its final name")
	}
	if mv < verify {
		t.Error("install script renames the download before verifying it")
	}
	// A subshell with set -e is what makes the failure visible: a bare `(cd ...
	// && sha256sum -c ...)` discards the verifier's exit status and the chmod
	// after it runs regardless.
	if !strings.Contains(block, "set -e") {
		t.Error("install script must abort on a failed verification, not carry on to chmod")
	}
	// awk matching nothing leaves an empty checksum file, and whether the
	// verifier treats that as success is a property of the local coreutils or
	// Perl shasum, not of this script. Refuse the install outright instead.
	if !strings.Contains(block, "if [ ! -s asset.sha256 ]; then") {
		t.Error("install script must refuse to install when checksums.txt lists no entry for the asset")
	}
}

// A checksum says which bytes shipped, not which workflow built them. The
// attestation is the signed record of the second, and it only exists if the
// release job asks for one: the permissions that sign it, the pinned action,
// and the checksums it covers.
func TestReleaseAttestsBuildProvenance(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	for _, want := range []string{
		"id-token: write",
		"attestations: write",
		"actions/attest-build-provenance@",
		"subject-checksums: dist/checksums.txt",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release job must %q; without it a release ships no signed record of what built it", want)
		}
	}
	attest := strings.Index(text, "actions/attest-build-provenance@")
	if attest < 0 {
		t.Fatal("release job has no provenance attestation step")
	}
	if publish := strings.Index(text, "name: Publish"); attest > publish {
		t.Error("attest the artifacts before publishing them; an attestation written after the release exists describes nothing a consumer downloaded")
	}
	if smoke := strings.Index(text, "name: Smoke-test what would be published"); attest < smoke {
		t.Error("attest what the smoke test verified, not a build that has not been checked")
	}
}

func TestReleaseSmokeTestsSbom(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	if !strings.Contains(text, "test -s dist/sbom.json") {
		t.Fatal("release job smoke test must verify dist/sbom.json is non-empty before publication")
	}
	if !strings.Contains(text, "dist/checksums.txt dist/sbom.json") {
		t.Fatal("release job must publish sbom.json beside the binaries and checksums.txt")
	}
}
