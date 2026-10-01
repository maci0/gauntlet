// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	ctx := t.Context()
	cmd := exec.CommandContext(ctx, "sh", "-c", backupRecipeFor(t, state, archive))
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

// TestBackupRecipeKeepsTheLastGoodArchive pins the write the recipe used to
// do. `tar -czf "$archive"` opens the destination for writing, so the archive
// it is about to replace is truncated before a byte of the new one lands: a
// run cut by a full disk, a crash, or a `kill` leaves a `.tgz` that opens and
// stops, in the one place the previous good copy was. The `tar -tzf` check
// came after the damage, so it verified a file the job had already ruined.
//
// What the destination may hold is the previous archive or the new one, never
// half of one. That is what building into a temporary beside it and renaming
// after it reads back buys, and this points the recipe at a `tar` that writes
// a cut archive and fails, which is what an interrupted job leaves behind.
func TestBackupRecipeKeepsTheLastGoodArchive(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	// Random bytes, not a repeated line: gzip compresses a repetition down to
	// a fraction of its size, and a second archive that is only slightly
	// larger is written before a kill can land in the middle of it.
	blob := make([]byte, 1<<20)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	makeBlob(t, state, filepath.Join("runs", "2026-08-25", "20260825T131500Z-a91f.jsonl"), blob)

	// A first, good archive at the destination: what an operator restores
	// from today.
	if out, err := runBackupRecipe(t, state); err != nil {
		t.Fatalf("the first run of the recipe failed: %v\n%s", err, out)
	}
	archive := filepath.Join(dir, "gauntlet-state.tgz")
	good, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("the recipe wrote no archive: %v", err)
	}
	listed, err := exec.Command("tar", "-tzf", archive).Output()
	if err != nil {
		t.Fatalf("the first archive does not read back: %v", err)
	}
	if !strings.Contains(string(listed), "20260825T131500Z-a91f.jsonl") {
		t.Fatalf("the first archive does not hold the journal it archived:\n%s", listed)
	}

	// A state tree the next run cannot archive: the second job writes half an
	// archive and fails, which is what a destination that filled up does.
	makeBlob(t, state, filepath.Join("runs", "2026-08-25", "20260825T140000Z-b33f.jsonl"),
		make([]byte, 8<<20))

	cut := t.TempDir()
	stub := filepath.Join(cut, "tar")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"+
		"# tar, standing in for a destination that filled up: it writes a cut\n"+
		"# archive and fails, which is what an interrupted job leaves behind.\n"+
		"out=\n"+
		"while [ \"$#\" -gt 0 ]; do\n"+
		"  case $1 in -czf|-cf|-f) out=$2; shift 2; continue;; esac\n"+
		"  shift\n"+
		"done\n"+
		"cat >/dev/null\n"+
		"printf 'not a gzip stream' >\"$out\"\n"+
		"exit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := backupRecipeFor(t, state, archive)
	cmd := exec.CommandContext(t.Context(), "sh", "-c", recipe)
	// The stub has to come first on PATH, and a shell reads the first PATH in
	// its environment rather than the last, so the inherited one is dropped
	// rather than appended after.
	cmd.Env = append(without(os.Environ(), "PATH="), "GAUNTLET_HOME="+state, "PATH="+cut+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the failing archive job reported success, so nothing checked it:\n%s", out)
	}

	after, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("the failed run removed the archive at the destination: %v", err)
	}
	if !bytes.Equal(after, good) {
		t.Fatalf("the failed run replaced a good archive with %d bytes of a new one, "+
			"so the copy that read back a moment ago is gone", len(after))
	}
	// Nothing half-written is left where a later run could mistake it for a
	// second copy worth restoring.
	if left := partialsBeside(t, archive); len(left) != 0 {
		t.Errorf("the failed run left %v beside the archive", left)
	}
	if _, err := exec.Command("tar", "-tzf", archive).Output(); err != nil {
		t.Fatalf("the archive at the destination does not read back: %v", err)
	}
}

// backupRecipeFor is the recipe as the page prints it with its placeholders
// pointed at scratch: the block names its own output path, so nothing reaches
// the path the documentation shows as one, and the placeholders a reader
// substitutes are the page's job to explain rather than paths a test honours.
func backupRecipeFor(t *testing.T, state, archive string) string {
	t.Helper()
	recipe := strings.ReplaceAll(backupRecipe(t), "/path/on/other-storage/gauntlet-state.tgz", archive)
	recipe = strings.ReplaceAll(recipe, "/path/to/empty-restore", filepath.Join(filepath.Dir(state), "restored"))
	recipe = strings.ReplaceAll(recipe, "/path/to/repo", state)
	return recipe
}

// makeBlob writes one journal-shaped file of exactly the given bytes at a
// relative path under the state tree, which the archive is taken over.
func makeBlob(t *testing.T, state, rel string, body []byte) {
	t.Helper()
	path := filepath.Join(state, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// without drops every environment entry whose name matches prefix, so a caller
// can set one of its own and have the shell read that one rather than the
// first copy it was handed.
func without(env []string, prefix string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// partialsBeside lists the temporaries the recipe left next to an archive,
// which is the shape a glob for one temporary per job sees.
func partialsBeside(t *testing.T, archive string) []string {
	t.Helper()
	pattern := archive + "." + "*" + ".partial"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// TestBackupRecipeGivesOverlappingJobsTheirOwnTemporary pins the process id in
// the recipe's temporary name. Two jobs that overlap on one schedule - a timer
// that fired late and the next one starting on time - would share a temporary
// named after the archive, and the first to finish renames away a file the
// second is still appending to: the destination then holds an archive another
// job is still writing, which is the same cut copy the atomic write was added
// to prevent.
//
// The two jobs start together over a state tree big enough that both are still
// writing when the directory is read, and the read looks for two temporaries.
// A shared name cannot show two at once: the first job's rename takes the only
// one away.
func TestBackupRecipeGivesOverlappingJobsTheirOwnTemporary(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	// Random bytes, so compressing 48 MiB of it takes long enough to be caught
	// mid-write rather than over before the directory is read.
	makeBlob(t, state, filepath.Join("runs", "2026-08-25", "20260825T131500Z-a91f.jsonl"),
		make([]byte, 48<<20))

	archive := filepath.Join(dir, "gauntlet-state.tgz")
	recipe := backupRecipeFor(t, state, archive)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.CommandContext(t.Context(), "sh", "-c", recipe)
			cmd.Env = append(os.Environ(), "GAUNTLET_HOME="+state)
			cmd.CombinedOutput()
		}()
	}

	// Read the directory repeatedly until both jobs have written their
	// temporary or the deadline passes, which is the moment one job renames
	// away the other's file.
	deadline := time.Now().Add(5 * time.Second)
	peak := 0
	for time.Now().Before(deadline) {
		if n := len(partialsBeside(t, archive)); n > peak {
			peak = n
		}
		if peak == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	wg.Wait()

	if peak != 2 {
		t.Errorf("%d temporaries beside the archive while two jobs ran, want 2: "+
			"the recipe writes through one name, so one job renames away the other's copy",
			peak)
	}
	// Whatever the overlap did, one whole archive is at the destination.
	if _, err := exec.Command("tar", "-tzf", archive).Output(); err != nil {
		t.Fatalf("the archive at the destination does not read back: %v", err)
	}
	if left := partialsBeside(t, archive); len(left) != 0 {
		t.Errorf("%v left beside the archive after both jobs finished", left)
	}
}
