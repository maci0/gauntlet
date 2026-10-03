// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/gitx"
)

// The git half of docs/RUNS.md was documented and never run, and it is the
// half that cannot be checked by anything else on the page. The state-tree
// archive beside it is executed by backuprecipe_test.go against every shape a
// state root takes, but a review left on an unmerged lane branch is not state
// tree state: it is a commit in the reviewed repository, it is what
// `gauntlet doctor` names as the one output no copy of GAUNTLET_HOME holds,
// and the two-line block that keeps it sat in a markdown fence. A fence cannot
// fail, so the one backup protecting irreplaceable work was the one backup
// nothing ran.
//
// Running it exposed the failure the prose reads as covered. `git clone
// <bundle>` materializes every ref it fetched under refs/remotes/origin/, and
// the refs an unlanded review needs are read back from local
// refs/heads/gauntlet/ (gitx.LaneBranches). So the documented restore brought
// the commits back as remote-tracking refs: present in the repository, named by
// no local branch, invisible to doctor, and unreachable by the next run's
// merge step. The work was recovered and still unusable, which is the outcome
// the section exists to prevent, and the only way an operator learns the
// difference is by doing the restore during an incident.
//
// So the block is executed here, and the assertion is the shape a restored
// repository has to have: the unlanded review on a local branch under
// refs/heads/gauntlet/, with its commit reachable and its content intact.

// bundleRecipe extracts the ```sh block that takes the git bundle. The
// section opens with this block, before the state-tree archive, so it is found
// by the line that creates the bundle rather than by the first fence.
func bundleRecipe(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "RUNS.md"))
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(data), "### Backup and restore")
	if at < 0 {
		t.Fatal("docs/RUNS.md has no backup and restore section")
	}
	rest := string(data)[at:]
	before, _, ok := strings.Cut(rest, "git -C /path/to/repo bundle create")
	if !ok {
		t.Fatal("the backup section documents no git bundle recipe")
	}
	start := strings.LastIndex(before, "```sh")
	if start < 0 {
		t.Fatal("the git bundle recipe is not inside a shell block")
	}
	body := rest[start+len("```sh"):]
	block, _, ok := strings.Cut(body, "```")
	if !ok {
		t.Fatal("the git bundle block is not closed")
	}
	return strings.TrimSpace(block)
}

// TestBundleRecipeRestoresUnlandedReviewsAsLocalBranches runs the documented
// block against a repository holding an unlanded review, and requires the
// restored repository to hold it the way the runner reads a review back.
func TestBundleRecipeRestoresUnlandedReviewsAsLocalBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	dir := t.TempDir()
	repo, run := gitRepo(t, "package main\n")
	// A review a run kept after a merge that did not land: the commit is on a
	// lane branch and nowhere else, which is the state the section says no
	// copy of the state tree holds.
	branch := gitx.LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance")
	run("checkout", "-qb", branch)
	if err := os.WriteFile(filepath.Join(repo, "review.go"),
		[]byte("package main\n\n// the review that did not land\nfunc reviewed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "perf: the review the merge did not carry")
	head := strings.TrimSpace(run2(t, repo, "rev-parse", "HEAD"))
	run("checkout", "-q", "main")
	// The branch is a name and a commit; nothing on main carries the work, so a
	// restore that loses the branch loses the review itself.
	if strings.Contains(run2(t, repo, "cat-file", "-p", "main"), "reviewed") {
		t.Fatal("the fixture put the review on main, so losing the branch loses nothing")
	}

	bundle := filepath.Join(dir, "repo.bundle")
	restored := filepath.Join(dir, "restored-repo")
	recipe := bundleRecipeFor(t, repo, bundle, restored)
	if out, err := exec.CommandContext(t.Context(), "sh", "-c", recipe).CombinedOutput(); err != nil {
		t.Fatalf("the documented bundle recipe failed: %v\n%s%s", err, out, recipe)
	}

	// The bundle has to hold the lane branch at all. Without this the failure
	// below reads as a restore problem when it is a create problem.
	if out := run2(t, dir, "bundle", "list-heads", bundle); !strings.Contains(out, "refs/heads/"+branch) {
		t.Fatalf("the bundle does not hold the unlanded review branch %s:\n%s", branch, out)
	}

	// And the restored repository has to hold it where the runner looks for it:
	// a local branch, not a remote-tracking ref, because LaneBranches and the
	// merge step both read refs/heads/gauntlet/.
	names := run2(t, restored, "for-each-ref", "--format=%(refname)", "refs/heads")
	if !strings.Contains(names, "refs/heads/"+branch) {
		// Fatal rather than reported and continued: the two assertions below
		// read that same branch, so continuing turns one finding into three
		// failures that all name the same missing ref.
		t.Fatalf("the restored repository has no local branch for the unlanded review %s;\n"+
			"the commits came back as remote-tracking refs, so doctor cannot name them "+
			"and the next run cannot merge them:\n%s", branch, names)
	}
	if got := strings.TrimSpace(run2(t, restored, "rev-parse", "refs/heads/"+branch)); got != head {
		t.Errorf("the restored branch is at %s, the review commit was %s", got, head)
	}
	// The content, not just the ref: a branch pointing at a commit whose tree
	// lost the file is a restore that read back and recovered nothing.
	if !strings.Contains(run2(t, restored, "cat-file", "-p", "refs/heads/"+branch+":review.go"),
		"the review that did not land") {
		t.Errorf("the restored review branch does not carry the review's content")
	}
}

// TestBundleRecipeRunsTwiceOverTheSamePaths is the rerun an on-schedule job
// does every night. The bundle is written to one destination twice, which is
// what the job does, and each drill clones into a directory of its own as the
// page tells an operator to: a block that failed on the second take would pass
// a drill taken once and fail the first scheduled copy after it.
func TestBundleRecipeRunsTwiceOverTheSamePaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	dir := t.TempDir()
	repo, run := gitRepo(t, "package main\n")
	branch := gitx.LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security")
	run("checkout", "-qb", branch)
	if err := os.WriteFile(filepath.Join(repo, "review.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "sec: a review that did not land")
	run("checkout", "-q", "main")

	bundle := filepath.Join(dir, "repo.bundle")
	for i := range 2 {
		recipe := bundleRecipeFor(t, repo, bundle, filepath.Join(dir, "restored-"+strconv.Itoa(i)))
		if out, err := exec.CommandContext(t.Context(), "sh", "-c", recipe).CombinedOutput(); err != nil {
			t.Fatalf("the documented bundle recipe failed on run %d: %v\n%s%s", i+1, err, out, recipe)
		}
	}
}

// bundleRecipeFor is the recipe as the page prints it with its placeholders
// pointed at scratch: the repository is one this test built, the bundle and
// the restored clone are under the test's own temporary directory, and nothing
// is left pointing at the paths the documentation shows as examples.
func bundleRecipeFor(t *testing.T, repo, bundle, restored string) string {
	t.Helper()
	recipe := bundleRecipe(t)
	recipe = strings.ReplaceAll(recipe, "/path/to/repo", repo)
	recipe = strings.ReplaceAll(recipe, "/path/on/other-storage/repo.bundle", bundle)
	recipe = strings.ReplaceAll(recipe, "/path/to/restored-repo", restored)
	if strings.Contains(recipe, "/path/") {
		t.Fatalf("the bundle block names a path this drill did not substitute:\n%s", recipe)
	}
	return recipe
}

// run2 runs one git command in a directory and returns its output, failing the
// test when it does not succeed. gitRepo's runner takes no directory, and the
// restored repository is a different tree from the one under test.
func run2(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}
