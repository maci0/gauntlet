// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"errors"
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
