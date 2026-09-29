// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// run records one finished run the way a real run does: a journal file and the
// index row that Close appends for it.
func record(t *testing.T, id string, start time.Time) {
	t.Helper()
	j, err := Open(id, start)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if err := j.Close(Summary{
		Version: "test", Start: start, End: start.Add(time.Minute),
		ExitCode: new(int),
	}); err != nil {
		t.Fatal(err)
	}
}

// Prune is the bound on the history: past the newest keep, neither the journal
// nor the index row survives, and both copies have to agree or the listing
// names a run whose file is gone.
func TestPruneDropsBothCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	// Two days of runs, so a keep that cuts mid-shard leaves a shard behind
	// with journals still in it.
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	ids := []string{
		"20260825T090000Z-0001", "20260825T100000Z-0002", "20260825T110000Z-0003",
		"20260826T090000Z-0004", "20260826T100000Z-0005", "20260826T110000Z-0006",
	}
	for i, id := range ids {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
	}

	removed, err := Prune(3)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("pruned %d runs, want 3", removed)
	}

	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("listing has %d runs, want the 3 newest: %v", len(rows), ids)
	}
	for i, want := range []string{"20260826T110000Z-0006", "20260826T100000Z-0005", "20260826T090000Z-0004"} {
		if rows[i].RunID != want {
			t.Errorf("listing[%d] is %s, want %s", i, rows[i].RunID, want)
		}
	}

	// The index is compacted, not just read: a row that outlived its journal
	// would be reconstructed on the next listing and reappear.
	indexed := readIndexFile(t)
	for i, id := range ids {
		retained := i >= len(ids)-3
		if _, onDisk := indexed[id]; onDisk != retained {
			t.Errorf("index row for %s: present=%v, want %v", id, onDisk, retained)
		}
		_, err := os.Stat(journalPath(id))
		if retained && err != nil {
			t.Errorf("journal for a retained run is gone: %v", err)
		} else if !retained && err == nil {
			t.Errorf("journal for %s survived the prune", id)
		}
	}
	// The shard that still holds retained runs stays; the emptied one goes.
	if _, err := os.Stat(filepath.Join(home, "runs", "2026-08-25")); err == nil {
		t.Error("the emptied shard directory survived")
	}
	if _, err := os.Stat(filepath.Join(home, "runs", "2026-08-26")); err != nil {
		t.Errorf("the retained shard directory is gone: %v", err)
	}
}

// A keep the history already satisfies is a no-op, and a keep of zero keeps
// everything: a caller with no configured bound must not delete history.
func TestPruneKeepsWhatItIsToldTo(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{
		"20260825T090000Z-0001", "20260825T100000Z-0002", "20260825T110000Z-0003",
	} {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
	}

	for _, keep := range []int{0, -1, 3, 10} {
		removed, err := Prune(keep)
		if err != nil {
			t.Fatalf("Prune(%d): %v", keep, err)
		}
		if removed != 0 {
			t.Errorf("Prune(%d) removed %d runs, want none", keep, removed)
		}
	}
	if rows, err := Recent(10); err != nil || len(rows) != 3 {
		t.Errorf("listing has %d runs after a no-op prune (err %v), want 3", len(rows), err)
	}
}

// A journal a run has open is the newest one, so pruning cannot take the file
// out from under a run in progress.
func TestPruneLeavesTheRunInProgress(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	record(t, "20260825T100000Z-0002", base.Add(time.Hour))

	live, err := Open("20260825T110000Z-0003", base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	live.Write(map[string]string{"ev": "run_start"})
	live.Flush()

	if _, err := Prune(1); err != nil {
		t.Fatal(err)
	}
	live.Write(map[string]string{"ev": "loop_end"})
	if err := live.Close(Summary{Version: "test", Start: base.Add(2 * time.Hour)}); err != nil {
		t.Fatalf("the run in progress could not finish its journal: %v", err)
	}
	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RunID != "20260825T110000Z-0003" {
		t.Fatalf("listing after the prune is %v, want only the run in progress", rows)
	}
}

// A run another gauntlet still has open is not stale whatever its id says. A
// run that began before the one that is pruning still has its journal open
// whenever it is long, so the keep window must not move it: the move would file
// a live event stream under pruned/, and the row its Close appends would name a
// file the listing no longer holds.
func TestPruneLeavesAnOlderRunInProgress(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	// One run in progress, opened before the run that follows it.
	older, err := Open("20260825T090000Z-00aa", base)
	if err != nil {
		t.Fatal(err)
	}
	older.Write(map[string]string{"ev": "run_start"})
	older.Flush()
	record(t, "20260825T100000Z-00bb", base.Add(time.Hour))

	removed, err := Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("Prune(1) removed %d runs, want none while a run is still open", removed)
	}
	if _, err := os.Stat(journalPath("20260825T090000Z-00aa")); err != nil {
		t.Errorf("the run in progress lost its journal: %v", err)
	}
	if held, err := Quarantined(); err != nil || len(held) != 0 {
		t.Errorf("quarantine is %v (err %v), want empty", held, err)
	}
	// Its row survives the same pass, so the run still lists when it finishes.
	if err := older.Close(Summary{Version: "test", Start: base, End: base}); err != nil {
		t.Fatalf("the run in progress could not finish its journal: %v", err)
	}
	rows, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("listing has %d runs, want both: %v", len(rows), rows)
	}
	if st, err := Inspect(); err != nil || st.Disagreed != 0 {
		t.Errorf("state tree after the run finished is %+v (err %v), want consistent", st, err)
	}
}

func readIndexFile(t *testing.T) map[string]bool {
	t.Helper()
	rows, err := readAllIndex()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(rows))
	for _, s := range rows {
		out[s.RunID] = true
	}
	return out
}

// A stream this process has open is not idle, and the kernel cannot be asked
// the question on both platforms: Linux ties a flock to the open file
// description, so the probe's descriptor conflicts with the writer's shared
// lock, while macOS ties it to the process and the probe converts this
// process's own shared lock and succeeds. Without the in-process record, a
// prune on macOS would call a stream it is still appending to idle and move it
// under a live run.
func TestJournalThisProcessHoldsIsNotIdle(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	live, err := Open("20260825T090000Z-0001", base)
	if err != nil {
		t.Fatal(err)
	}
	live.Write(map[string]string{"ev": "run_start"})
	live.Flush()
	path := journalPath("20260825T090000Z-0001")

	if journalIdle(path) {
		t.Error("a journal this process has open reads as idle")
	}
	if err := live.Close(Summary{Version: "test", Start: base, End: base}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !journalIdle(path) {
		t.Error("a journal this process has closed still reads as in progress")
	}
}
