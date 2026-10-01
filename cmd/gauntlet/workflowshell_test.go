// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `make check-workflow-shell` exists to lint the shell a workflow's `run:`
// steps execute, and the extractor that feeds it read a `run:` only at the
// start of a line. GitHub Actions also spells a step as `- run: |`, with the
// key and the block indicator on one line, and the pattern never matched that:
// the release notes, the tag guard, the smoke test, and the publish step are
// all written that way, so the shell that decides whether a release is
// published was never the shell that got linted. shellcheck reading five
// bodies where the file has nine is silent, so nothing said so.
//
// The two spellings and both body indents are read out of a real workflow
// and lint is run over what came out, rather than the pattern being pinned as
// text: a pattern that matches is a claim about the regex, and the defect was
// a regex that matched everything it was asked about.
func TestWorkflowShellExtractorCoversEveryRunBody(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not on PATH")
	}
	// Three spellings, all valid: the block scalar on its own line, on the
	// step line, and a `with:` key one level deeper. A body two indents deep
	// is the one a fixed cut of ten spaces misses, so it is here.
	workflow := `name: extractor
on: push
jobs:
  j:
    runs-on: ubuntu-24.04
    steps:
      - run: |
          echo scalar-on-own-line
      - name: block on the step line
        run: |
          echo block-on-step-line
      - name: nested under with
        with:
          run: |
            echo nested-body
      - run: make check
`
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "x.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	bodies := extractWorkflowBodies(t, dir, "x.yml")
	if len(bodies) != 3 {
		t.Fatalf("extracted %d bodies, want 3: %v", len(bodies), bodies)
	}
	for _, want := range []string{
		"echo scalar-on-own-line",
		"echo block-on-step-line",
		"echo nested-body",
	} {
		found := false
		for _, body := range bodies {
			if strings.Contains(body, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no extracted body carries %q; the shell in it is never linted", want)
		}
	}
	// A defect in the shell itself has to fail the lint, which is the whole
	// reason the bodies are extracted at all. The body below is one the old
	// pattern never saw, so the command substitution's status and the
	// directory change are exactly the class of defect the target is for.
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "y.yml"),
		[]byte("name: bad\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: |\n          cd $(mktemp -d)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runWorkflowShellLint(t, dir); err == nil {
		t.Error("a masked command substitution in a `run: |` body must fail the lint")
	} else if !strings.Contains(out, "SC2312") {
		t.Errorf("lint failed for the wrong reason: %v\n%s", err, out)
	}
}

// A body the extractor opens and leaves empty is a step whose shell was never
// linted: shellcheck reads a preamble-only script as a clean one, so the
// failure is silent. The target has to name it instead.
func TestWorkflowShellLintRefusesAnEmptyBody(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A block scalar with nothing under it: a real YAML shape, and the one
	// the extractor has no body lines to take.
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "x.yml"),
		[]byte("name: empty\non: push\njobs:\n  j:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: |\n      - run: |\n          echo later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runWorkflowShellLint(t, dir)
	if err == nil {
		t.Fatalf("an empty body must fail the lint: %s", out)
	}
	if !strings.Contains(out, "extracted empty") {
		t.Errorf("the refusal must name the missed body: %s", out)
	}
}

// The two helpers above share the tree the real target builds, so a change to
// the Makefile that moves the recipe reaches them: they read the recipe out of
// the Makefile itself rather than keeping a second awk program here.
func workflowShellRecipe(t *testing.T) string {
	t.Helper()
	var recipe []string
	lines := strings.Split(makefileText(t), "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "check-workflow-shell: ##") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("Makefile has no check-workflow-shell recipe")
	}
	for _, line := range lines[start+1:] {
		if line != "" && !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, strings.TrimPrefix(line, "\t"))
	}
	if len(recipe) == 0 {
		t.Fatal("check-workflow-shell has no recipe")
	}
	return strings.Join(recipe, "\n")
}

// runWorkflowShellLint runs the target's extraction and lint over a tree the
// test owns, with the two variables the recipe reads pointed at scratch paths
// so the test writes nothing outside its own temporary directory.
func runWorkflowShellLint(t *testing.T, dir string) (string, error) {
	t.Helper()
	scratch := filepath.Join(dir, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	// A make recipe reaches the shell with $$ for the shell's own $, and the
	// one variable the recipe reads is $(TMPDIR). Both are undone here, and
	// `sh` runs what is left: the same recipe, with the scratch path the test
	// owns in place of the machine's. `@` is make's "do not echo" rather than
	// the shell's, so it goes with them.
	recipe := strings.ReplaceAll(workflowShellRecipe(t), "$$", "$")
	recipe = strings.ReplaceAll(recipe, "$(TMPDIR)", scratch)
	recipe = strings.TrimPrefix(recipe, "@")
	recipe = strings.ReplaceAll(recipe, "\n@", "\n")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", recipe)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMPDIR="+scratch)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// extractWorkflowBodies returns the shell each extracted body carries, read
// back off disk so the test sees what shellcheck would.
func extractWorkflowBodies(t *testing.T, dir, name string) []string {
	t.Helper()
	if _, err := runWorkflowShellLint(t, dir); err != nil {
		// The lint is allowed to fail on a body it found; the extraction is
		// what this test reads.
		_ = err
	}
	stem := strings.TrimSuffix(name, ".yml")
	entries, err := filepath.Glob(filepath.Join(dir, "scratch", "workflow-shell", stem+"-*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, path := range entries {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}
