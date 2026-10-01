// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// docs/RUNS.md tells an operator to take the state tree's backup with the shell
// block printed there, and to run the restore commands after changing the
// archive job or on a schedule, because a backup that has only been written has
// not been proven restorable. Nothing ran that block: the two drills the page
// names build the tree in Go and never touch the recipe a cron job or a
// systemd timer actually executes, so the failure modes the recipe has are
// the ones no test sees. `tar` exits non-zero on a member that is not there
// and refuses to write an empty archive, so naming `runs` unconditionally
// fails on a fresh machine and on a state root whose history is only in the
// quarantine — the copy of that machine's history that never happens, in a log
// nobody reads.
//
// So this runs the block as written, against every shape a state root takes:
// nothing recorded yet, a listed run, a history the keep bound moved into the
// quarantine, and all three members at once. A change to the page that breaks
// any of them fails here rather than at the first restore.

// backupRecipe extracts the ```sh block that takes the state-tree archive.
// The section opens with the reviewed repository's git, which is a different
// backup and a different destination, so the block is anchored on the line
// that reads the state root rather than on the first fence: the archive is
// the block whose first command resolves GAUNTLET_HOME.
func backupRecipe(t *testing.T) string {
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
	open := strings.Index(rest, "state=${GAUNTLET_HOME")
	if open < 0 {
		t.Fatal("the backup section documents no state-tree archive recipe")
	}
	// The block that holds that line is the one the archive recipe is in.
	start := strings.LastIndex(rest[:open], "```sh")
	if start < 0 {
		t.Fatal("the state-tree recipe is not inside a shell block")
	}
	body := rest[start+len("```sh"):]
	closed := strings.Index(body, "```")
	if closed < 0 {
		t.Fatal("the state-tree block is not closed")
	}
	return strings.TrimSpace(body[:closed])
}

// runBackupRecipe runs the recipe with the two paths it reads pointed at
// scratch: GAUNTLET_HOME at a state tree this test built, and the archive
// itself beside it. The recipe writes nothing else, and the variables are set
// through the environment rather than by editing the text, so what runs is the
// block as the page prints it.
func runBackupRecipe(t *testing.T, state string) (string, error) {
	t.Helper()
	archive := filepath.Join(filepath.Dir(state), "gauntlet-state.tgz")
	// The page's block names its own output path; point that at scratch too,
	// so nothing reaches the path the documentation uses as a placeholder.
	recipe := strings.ReplaceAll(backupRecipe(t), "/path/on/other-storage/gauntlet-state.tgz", archive)
	// Placeholders a reader substitutes are the page's job to explain, not
	// paths this test can honour; the two that name where the copy goes.
	recipe = strings.ReplaceAll(recipe, "/path/to/empty-restore",
		filepath.Join(filepath.Dir(state), "restored"))
	recipe = strings.ReplaceAll(recipe, "/path/to/repo", state)

	ctx := t.Context()
	cmd := exec.CommandContext(ctx, "sh", "-c", recipe)
	cmd.Env = append(os.Environ(), "GAUNTLET_HOME="+state)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestBackupRecipeArchivesEveryShapeOfStateTree(t *testing.T) {
	shard := filepath.Join("runs", "2026-08-25")

	for _, tc := range []struct {
		name  string
		build func(t *testing.T, state string)
		want  []string
	}{
		{
			// A machine whose only history is in the quarantine, and one
			// installed minutes ago with nothing in it: the two shapes a
			// recipe naming `runs` unconditionally cannot take.
			name:  "nothing recorded yet",
			build: func(*testing.T, string) {},
			// An empty state tree has nothing to lose, so there is nothing to
			// archive and nothing to fail.
			want: nil,
		},
		{
			name: "a listed run and no quarantine",
			build: func(t *testing.T, state string) {
				makeJournal(t, state, filepath.Join(shard, "20260825T131500Z-a91f.jsonl"))
			},
			want: []string{filepath.Join("runs", "2026-08-25", "20260825T131500Z-a91f.jsonl")},
		},
		{
			// A run past the --keep-runs bound leaves runs/, and a machine
			// whose whole history is quarantine has no runs/ at all. The
			// journal the bound moved is still recoverable state, so a
			// recipe naming `runs` unconditionally fails here rather than
			// archiving the quarantine.
			name: "history only in the quarantine",
			build: func(t *testing.T, state string) {
				makeJournal(t, state, filepath.Join("pruned", "2026-07-01",
					"20260701T090000Z-b22e.jsonl"))
			},
			want: []string{filepath.Join("pruned", "2026-07-01", "20260701T090000Z-b22e.jsonl")},
		},
		{
			name: "history, quarantine, and custom agents",
			build: func(t *testing.T, state string) {
				makeJournal(t, state, filepath.Join(shard, "20260825T131500Z-a91f.jsonl"))
				makeJournal(t, state, filepath.Join("pruned", "2026-07-01",
					"20260701T090000Z-b22e.jsonl"))
				if err := os.WriteFile(filepath.Join(state, "agents.json"), []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{
				filepath.Join("runs", "2026-08-25", "20260825T131500Z-a91f.jsonl"),
				filepath.Join("pruned", "2026-07-01", "20260701T090000Z-b22e.jsonl"),
				"agents.json",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			state := filepath.Join(dir, "state")
			if err := os.MkdirAll(state, 0o700); err != nil {
				t.Fatal(err)
			}
			tc.build(t, state)

			out, err := runBackupRecipe(t, state)
			if err != nil {
				t.Fatalf("the documented recipe failed on %s: %v\n%s", tc.name, err, out)
			}

			archive := filepath.Join(dir, "gauntlet-state.tgz")
			if tc.want == nil {
				// Nothing recorded is not a failure, and `tar` refuses to
				// write an archive with no members, so the page writes none.
				if _, err := os.Stat(archive); err == nil {
					t.Fatalf("the recipe wrote an archive of an empty state tree: %s", archive)
				}
				return
			}
			if _, err := os.Stat(archive); err != nil {
				t.Fatalf("the recipe wrote no archive: %v", err)
			}
			// The archive has to hold what the state tree holds, read back
			// the way a restore reads it: a listing the recipe never printed
			// is a table of contents, not proof that the members arrived.
			listed, err := exec.Command("tar", "-tzf", archive).Output()
			if err != nil {
				t.Fatalf("the archive does not read back: %v", err)
			}
			for _, member := range tc.want {
				if !strings.Contains(string(listed), member) {
					t.Errorf("the archive does not hold %s:\n%s", member, listed)
				}
			}
		})
	}
}

// makeJournal writes one journal-shaped file at a relative path under the
// state tree, which is what the archive is taken over.
func makeJournal(t *testing.T, state, rel string) {
	t.Helper()
	path := filepath.Join(state, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"ev":"run_start","ts":"2026-08-25T13:15:00Z","dir":"/repo"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}
