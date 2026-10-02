// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The restore half of docs/RUNS.md was documented and never run. The archive
// recipe beside it is executed in backuprecipe_test.go against every shape a
// state root takes, but the commands an operator is told to type after the disk
// is gone — extract into an empty directory, list, replay a run, read the
// counts — sat in a markdown fence. A fence is not evidence: it cannot fail, so
// a recipe and the restore that consumes it drift apart and nothing notices
// until the incident. The archive holds what the tree held and the restore can
// still be the wrong three commands for the archive the recipe writes, and
// only running one against the other proves they agree.
//
// What this adds to the drill the page already promises: both halves run, one
// after the other, against the binary this tree builds rather than against a
// `gauntlet` that happens to be installed on the machine.

// restoreRecipe extracts the ```sh block that restores an archive. The section
// prints the state-tree archive recipe first, so the block is found from the
// prose that introduces the restore rather than from the first fence, which
// belongs to the backup.
func restoreRecipe(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "RUNS.md"))
	if err != nil {
		t.Fatal(err)
	}
	whole := string(data)
	section := strings.Index(whole, "### Backup and restore")
	if section < 0 {
		t.Fatal("docs/RUNS.md has no backup and restore section")
	}
	rest := whole[section:]
	prose := strings.Index(rest, "Restore into an empty directory")
	if prose < 0 {
		t.Fatal("the backup section documents no restore recipe")
	}
	fence := strings.Index(rest[prose:], "```sh")
	if fence < 0 {
		t.Fatal("the restore recipe is not inside a shell block")
	}
	body := rest[prose+fence+len("```sh"):]
	block, _, ok := strings.Cut(body, "```")
	if !ok {
		t.Fatal("the restore block is not closed")
	}
	return strings.TrimSpace(block)
}

// restoreRecipeFor is the restore recipe as the page prints it, with its
// placeholders pointed at scratch: the archive is the one the backup recipe
// wrote, the restore directory is empty, and the run id is one the tree
// actually holds, so the `gauntlet show` in the block replays a journal rather
// than reading a placeholder. The binary the block names is the one under test.
func restoreRecipeFor(t *testing.T, archive, restore, runID string) string {
	t.Helper()
	recipe := strings.ReplaceAll(restoreRecipe(t), "/path/on/other-storage/gauntlet-state.tgz", archive)
	recipe = strings.ReplaceAll(recipe, "/path/to/empty-restore", restore)
	recipe = strings.ReplaceAll(recipe, "run=<run-id from the listing>", "run="+runID)
	recipe = strings.ReplaceAll(recipe, "gauntlet ", gauntletBinary(t)+" ")
	if strings.Contains(recipe, "/path/") {
		t.Fatalf("the restore block names a path this drill did not substitute:\n%s", recipe)
	}
	return recipe
}

// gauntletBinary builds the command under test and returns its path, so the
// restore is proven against the binary this tree builds rather than whatever
// the machine's PATH holds.
func gauntletBinary(t *testing.T) string {
	t.Helper()
	once.Do(func() { buildGauntletBinary(t) })
	if gauntletBuildErr != nil {
		t.Fatalf("cannot build the binary the restore recipe names: %v", gauntletBuildErr)
	}
	if gauntletPath == "" {
		t.Fatal("the binary the restore recipe names was not built")
	}
	return gauntletPath
}

var (
	once             sync.Once
	gauntletPath     string
	gauntletBuildErr error
)

// buildGauntletBinary compiles cmd/gauntlet into a temporary directory. The
// build is not what this drill is about and it is the expensive part, so it
// runs once for the package however many times the block is read back.
func buildGauntletBinary(t *testing.T) {
	dir, err := os.MkdirTemp("", "gauntlet-bin")
	if err != nil {
		gauntletBuildErr = err
		return
	}
	gauntletPath = filepath.Join(dir, "gauntlet")
	// Bounded like every other go command the suite runs: a toolchain that
	// stops answering fails the drill that asked for it instead of hanging
	// the package. The build runs from the module root, because the test's
	// own directory is cmd/gauntlet and a relative package path would be
	// resolved beneath it.
	ctx, cancel := context.WithTimeout(context.Background(), goCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", gauntletPath, "./cmd/gauntlet")
	cmd.Dir = moduleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		gauntletBuildErr = fmt.Errorf("%w: %s", err, out)
		return
	}
	if _, err := os.Stat(gauntletPath); err != nil {
		gauntletBuildErr = fmt.Errorf("the build left no binary: %w", err)
	}
}

// TestRestoreRecipeReadsBackAnArchiveTheBackupRecipeWrote runs the whole
// recovery as the page prints it, both halves and in the order an incident
// takes them: the archive recipe writes an archive of a state tree holding a
// listed run and a quarantined one, the restore recipe extracts that archive
// into an empty directory, and then the binary reads the extracted tree with
// each of the three commands the page tells an operator to run.
//
// The counts are what makes this a restore rather than an extraction. A
// listing, a replay, and a `--json` document all exit zero on a tree with
// nothing in it, so a command's exit status proves nothing here; the history
// object is compared against the tree the copy was taken from, which is what
// distinguishes a restore that worked from one that extracted a file and lost
// the history in it.
func TestRestoreRecipeReadsBackAnArchiveTheBackupRecipeWrote(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	makeJournal(t, state, filepath.Join("runs", "2026-08-25", "20260825T131500Z-a91f.jsonl"))
	makeJournal(t, state, filepath.Join("pruned", "2026-07-01", "20260701T090000Z-b22e.jsonl"))
	// The third archived member, and the one whose loss every count above
	// would read as a tree that never had one: a restored tree without it
	// runs, lists, and reviews with the built-in agents, with nothing saying
	// a definition is gone.
	if err := os.WriteFile(filepath.Join(state, "agents.json"),
		[]byte(`{"myagent": {"argv": ["myagent", "-p", "{prompt}"]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runID := "20260825T131500Z-a91f"

	// Read the live tree first, through the binary, so the expectation is the
	// tree the copy was taken from rather than numbers written beside the
	// fixtures.
	before := historyCounts(t, runGauntlet(t, state, "runs", "--json"))

	if out, err := runBackupRecipe(t, state); err != nil {
		t.Fatalf("the documented backup recipe failed: %v\n%s", err, out)
	}
	archive := filepath.Join(dir, "gauntlet-state.tgz")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("the backup recipe wrote no archive: %v", err)
	}

	restore := filepath.Join(dir, "empty-restore")
	recipe := restoreRecipeFor(t, archive, restore, runID)
	if out, err := exec.CommandContext(t.Context(), "sh", "-c", recipe).CombinedOutput(); err != nil {
		t.Fatalf("the documented restore recipe failed: %v\n%s%s", err, out, recipe)
	}

	after := historyCounts(t, runGauntlet(t, restore, "runs", "--json"))
	if got, want := after, before; got != want {
		t.Errorf("the restored tree reports %s, the tree it was copied from %s", got, want)
	}
	if !strings.Contains(after, "journals=1") {
		t.Errorf("the restored tree does not hold the listed run it archived: %s", after)
	}
	if !strings.Contains(after, "pruned=1") {
		t.Errorf("the restored tree does not hold the quarantined run it archived: %s", after)
	}
	if !strings.Contains(after, "disagreed=0") || !strings.Contains(after, "truncated=0") {
		t.Errorf("the restored tree does not agree with itself: %s", after)
	}
	if !strings.Contains(after, "agents=1") {
		t.Errorf("the restored tree lost the agent definitions it archived: %s", after)
	}

	// `gauntlet show` against the restored tree, read through the binary: the
	// command that replays a journal end to end, and the one that fails
	// loudly on an archive whose runs/ came out empty. Its output is the
	// events the journal recorded, so what it has to show is the event the
	// archived journal carries — an empty replay of an intact journal would
	// still exit zero.
	shown := runGauntlet(t, restore, "show", runID)
	if !strings.Contains(shown, "run_start") || !strings.Contains(shown, "/repo") {
		t.Errorf("the restored tree cannot replay the run it archived:\n%s", shown)
	}

	// A second drill into the same directory is what a restore rehearsed after
	// an upgrade looks like, and a `mkdir` without -p stops it on its first
	// command for a reason that has nothing to do with the archive.
	if out, err := exec.CommandContext(t.Context(), "sh", "-c", recipe).CombinedOutput(); err != nil {
		t.Errorf("the restore recipe does not survive a second drill into the same directory: %v\n%s", err, out)
	}
}

// runGauntlet runs one gauntlet command against a state root and returns what
// it printed. GAUNTLET_HOME is dropped from the inherited environment first:
// TestMain points it at a scratch root for the whole package, and a leftover
// there would aim the command at a tree other than the one under test.
func runGauntlet(t *testing.T, state string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), gauntletBinary(t), args...)
	cmd.Env = append(without(os.Environ(), "GAUNTLET_HOME="), "GAUNTLET_HOME="+state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gauntlet %s against %s: %v\n%s", strings.Join(args, " "), state, err, out)
	}
	return string(out)
}

// historyCounts is the `history` object of a `gauntlet runs --json` document,
// rendered as one line. The object rather than the whole document: what
// survived is the claim, and comparing bytes would make a formatting change
// read as a lost run.
func historyCounts(t *testing.T, document string) string {
	t.Helper()
	var runs struct {
		History struct {
			Journals  int `json:"journals"`
			Rows      int `json:"rows"`
			Disagreed int `json:"disagreed"`
			Pruned    int `json:"pruned"`
			Truncated int `json:"truncated"`
			Agents    int `json:"agents"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(document), &runs); err != nil {
		t.Fatalf("gauntlet runs --json did not print a JSON document: %v\n%s", err, document)
	}
	h := runs.History
	return fmt.Sprintf("journals=%d rows=%d disagreed=%d pruned=%d truncated=%d agents=%d",
		h.Journals, h.Rows, h.Disagreed, h.Pruned, h.Truncated, h.Agents)
}
