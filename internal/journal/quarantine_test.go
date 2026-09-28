// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A prune is unattended and the keep is a flag, so what it drops has to stay
// recoverable: the journal is moved to pruned/ and Restore puts it back with
// its index row.
func TestPruneQuarantinesAndRestore(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	ids := []string{
		"20260825T090000Z-0001", "20260825T100000Z-0002", "20260825T110000Z-0003",
		"20260825T120000Z-0004",
	}
	for i, id := range ids {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
	}

	if removed, err := Prune(2); err != nil || removed != 2 {
		t.Fatalf("Prune(2) = %d, %v; want 2, nil", removed, err)
	}

	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 || held[0] != ids[1] || held[1] != ids[0] {
		t.Fatalf("quarantine is %v, want %v newest first", held, ids[:2])
	}
	if _, err := os.Stat(filepath.Join(Home(), "runs", "2026-08-25", ids[0]+".jsonl")); err == nil {
		t.Error("a pruned journal is still under runs/")
	}
	if _, err := os.Stat(quarantinePath(ids[0])); err != nil {
		t.Errorf("a pruned journal is not in the quarantine: %v", err)
	}

	if err := Restore(ids[0]); err != nil {
		t.Fatalf("Restore(%s): %v", ids[0], err)
	}
	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("listing has %d runs after the restore, want 3", len(rows))
	}
	if !readIndexFile(t)[ids[0]] {
		t.Error("the restored run has no index row")
	}
	held, err = Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0] != ids[1] {
		t.Errorf("quarantine after the restore is %v, want just %s", held, ids[1])
	}
}

// The quarantine is bounded by the same keep as the listing, or it is a second
// unbounded history beside the one Prune exists to bound.
func TestQuarantineIsBoundedByKeep(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	// A run in progress, so each Prune has a newest journal it cannot take.
	live, err := Open("20260825T120000Z-00ff", base.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	live.Write(map[string]string{"ev": "run_start"})
	live.Flush()

	for i := range 6 {
		record(t, "20260825T09000"+string(rune('0'+i))+"-000"+string(rune('1'+i)), base.Add(time.Duration(i)*time.Hour))
		if _, err := Prune(1); err != nil {
			t.Fatalf("Prune(1) at run %d: %v", i, err)
		}
	}
	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) > 1 {
		t.Errorf("quarantine holds %d runs (%v), want at most the keep of 1", len(held), held)
	}
}

// A restore that quietly did nothing is how a lost run stays lost, so the
// cases where there is nothing to move say so.
func TestRestoreRejectsWhatItCannotRestore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)

	err := Restore("20260825T100000Z-0002")
	if !errors.Is(err, ErrNotPruned) {
		t.Errorf("restoring an unknown run: %v, want ErrNotPruned", err)
	}
	if err := Restore("../escape"); !errors.Is(err, ErrInvalidRunID) {
		t.Errorf("restoring a malformed run id: %v, want ErrInvalidRunID", err)
	}
	if err := Restore("20260825T090000Z-0001"); !errors.Is(err, ErrAlreadyListed) {
		t.Errorf("restoring a run already in the listing: %v, want ErrAlreadyListed", err)
	}
}

// A name this package would not open must not sit in the quarantine either.
// trimQuarantine unlinks whatever falls outside the keep window, so a planted
// stem that counted toward that window would push a real quarantined run out.
func TestQuarantineIgnoresUnvalidatedRunIDs(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	if err := quarantine("20260825T090000Z-0001", journalPath("20260825T090000Z-0001")); err != nil {
		t.Fatal(err)
	}
	// A stem the run-id rules refuse, sitting beside the real quarantine.
	planted := filepath.Join(prunedDir(), "2026-08-25", "not a run id!.jsonl")
	if err := os.MkdirAll(filepath.Dir(planted), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0] != "20260825T090000Z-0001" {
		t.Fatalf("quarantine is %v, want only the real run", held)
	}
	// A keep of one covers the real run. The planted stem sorts above it, so
	// counting it would spend the window and unlink the real one.
	if err := trimQuarantine(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantinePath("20260825T090000Z-0001")); err != nil {
		t.Errorf("the real quarantined run was evicted: %v", err)
	}
}

// The run id is the key the index dedupes on and the file is opened by, so a
// second file carrying one id is the same run. Listed twice, it spends a slot
// of the keep window and prints the same run out of order.
func TestListingCountsARunIDOnce(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	record(t, "20260825T100000Z-0002", base.Add(time.Hour))
	// The first run's journal a second time, in a later shard: a tree
	// rearranged by hand, or a backup restored beside itself.
	stray := filepath.Join(runsDir(), "2026-08-26", "20260825T090000Z-0001.jsonl")
	if err := os.MkdirAll(filepath.Dir(stray), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(journalPath("20260825T090000Z-0001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, body, 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("listing has %d runs, want 2: %v", len(rows), rows)
	}
	// The copy in the shard its id names is the one listed, and the runs are
	// ordered by the start their ids carry, not by the shard they sit in.
	if rows[0].RunID != "20260825T100000Z-0002" || rows[1].RunID != "20260825T090000Z-0001" {
		t.Fatalf("listing is %s then %s, want the newer run first", rows[0].RunID, rows[1].RunID)
	}
	if rows[1].Path != journalPath("20260825T090000Z-0001") {
		t.Errorf("the listed copy is %s, want the one in the shard the id names", rows[1].Path)
	}
	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Journals != 2 || st.Rows != 2 || st.Disagreed != 0 {
		t.Errorf("Inspect reports %+v, want two runs in agreement", st)
	}
}

// A restore must not put a second copy of a run under a run id already in the
// listing, which is what a journal sitting in a shard its id does not name
// would otherwise produce: the derived path is free, and the rename lands
// beside the file that is already there.
func TestRestoreRejectsARunFiledUnderAnotherShard(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	const id = "20260825T090000Z-0001"
	record(t, id, base)
	// A copy of the same run, filed where the id does not name.
	listed := filepath.Join(runsDir(), "2026-08-27", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(listed), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(journalPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listed, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// And a quarantined copy of it, which is what a restore is asked for.
	if err := quarantine(id, listed); err != nil {
		t.Fatal(err)
	}

	if err := Restore(id); !errors.Is(err, ErrAlreadyListed) {
		t.Fatalf("restoring a run listed under another shard: %v, want ErrAlreadyListed", err)
	}
	if _, err := os.Stat(journalPath(id)); err != nil {
		t.Errorf("the listed run lost its journal: %v", err)
	}
}

// A run id that names no shard is filed at the top of runs/ and of pruned/.
// A quarantine walk that reads only the shards cannot see it, so it survives
// every keep (the unbounded history Prune exists to stop) and a user told
// what a prune left behind is not told about a run that is still restorable.
func TestQuarantineSeesARunFiledWithoutAShard(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	// An id in no generated form, so the quarantine files it at the top of
	// pruned/. It reads as the older of the two, an id with no start to
	// compare falling back to its text.
	record(t, "0handrun", base)
	if err := quarantine("0handrun", journalPath("0handrun")); err != nil {
		t.Fatal(err)
	}

	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0] != "0handrun" {
		t.Fatalf("quarantine is %v, want the hand-named run", held)
	}
	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Pruned != 1 {
		t.Errorf("Inspect counts %d pruned runs, want 1", st.Pruned)
	}
	// A keep of one covers it, and a second quarantined run pushes it out.
	record(t, "20260825T100000Z-0001", base.Add(time.Hour))
	if err := quarantine("20260825T100000Z-0001", journalPath("20260825T100000Z-0001")); err != nil {
		t.Fatal(err)
	}
	if err := trimQuarantine(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantinePath("0handrun")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the hand-named quarantine was not bounded by the keep: %v", err)
	}
}

// A restore has to leave the run where the listing looks for it. One whose id
// names no shard is filed at the top of runs/, which the shard walk skipped, so
// a restore that had worked on paper put the run where nothing could read it
// back.
func TestRestoreListsARunFiledWithoutAShard(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "handrun", base)
	if err := quarantine("handrun", journalPath("handrun")); err != nil {
		t.Fatal(err)
	}
	if err := Restore("handrun"); err != nil {
		t.Fatalf("Restore(handrun): %v", err)
	}
	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RunID != "handrun" {
		t.Fatalf("listing after the restore is %v, want the restored run", rows)
	}
	if !readIndexFile(t)["handrun"] {
		t.Error("the restored run has no index row")
	}
}

// A run id filed twice in pruned/ is one run, the rule the listing already
// applies to runs/. A quarantine that counted the copies separately spends two
// slots of the keep on one run, prints it twice, and can push a real
// quarantined run out of the bound.
func TestQuarantineCountsARunIDOnce(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{"20260825T090000Z-0001", "20260825T100000Z-0002"} {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
		if err := quarantine(id, journalPath(id)); err != nil {
			t.Fatal(err)
		}
	}
	// The older run's journal a second time, in a shard its id does not name:
	// a quarantine tree rearranged by hand.
	stray := filepath.Join(prunedDir(), "2026-08-26", "20260825T090000Z-0001.jsonl")
	if err := os.MkdirAll(filepath.Dir(stray), 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(quarantinePath("20260825T090000Z-0001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, body, 0o600); err != nil {
		t.Fatal(err)
	}

	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 || held[0] != "20260825T100000Z-0002" || held[1] != "20260825T090000Z-0001" {
		t.Fatalf("quarantine is %v, want each run once, newest first", held)
	}
	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Pruned != 2 {
		t.Errorf("Inspect counts %d pruned runs, want 2", st.Pruned)
	}
	// A keep of two covers both runs. Counting the stray copy would spend a
	// slot on the duplicate and unlink the older run to stay inside it.
	if err := trimQuarantine(2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantinePath("20260825T090000Z-0001")); err != nil {
		t.Errorf("the older run was pushed out by its own duplicate: %v", err)
	}
}

// A restore is the other move that empties a directory, so the shard it took
// the journal out of goes with it, the way the prune's own move does. No later
// trim can: that one only unlinks files, and the emptied shard holds none.
func TestRestoreRemovesTheShardItEmptied(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	record(t, "20260825T090000Z-0001", time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC))
	if err := quarantine("20260825T090000Z-0001", journalPath("20260825T090000Z-0001")); err != nil {
		t.Fatal(err)
	}
	if err := Restore("20260825T090000Z-0001"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prunedDir(), "2026-08-25")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the emptied quarantine shard is still there: %v", err)
	}
	// The run is back in the listing, which is the half that matters.
	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RunID != "20260825T090000Z-0001" {
		t.Fatalf("listing after the restore is %v, want the restored run", rows)
	}
}

// Inspect is what a restore is checked against, so it counts a row whose
// journal is gone, not just a journal the index never learned of.
func TestInspectCountsBothDisagreements(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	record(t, "20260825T100000Z-0002", base.Add(time.Hour))

	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Journals != 2 || st.Rows != 2 || st.Disagreed != 0 || !st.Consistent() {
		t.Fatalf("a closed tree reports %+v, want two runs in agreement", st)
	}

	// A journal removed by hand, its index row left behind.
	if err := os.Remove(journalPath("20260825T090000Z-0001")); err != nil {
		t.Fatal(err)
	}
	st, err = Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Journals != 1 || st.Rows != 2 || st.Disagreed != 1 || st.Consistent() {
		t.Fatalf("after losing a journal, Inspect reports %+v, want one disagreement", st)
	}
}
