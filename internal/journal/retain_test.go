// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	res, err := Prune(3)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 3 {
		t.Fatalf("moved %d runs, want 3", res.Moved)
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
		res, err := Prune(keep)
		if err != nil {
			t.Fatalf("Prune(%d): %v", keep, err)
		}
		if res.Moved != 0 {
			t.Errorf("Prune(%d) moved %d runs, want none", keep, res.Moved)
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

	res, err := Prune(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 0 {
		t.Errorf("Prune(1) moved %d runs, want none while a run is still open", res.Moved)
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

// Prune fires unattended at the end of every run, so the second execution of
// the same keep is the normal case, not a retry: a run whose keep the history
// already satisfies must leave the tree exactly as it found it, and a run that
// arrived since must cost one journal, one row, and one quarantine slot, with
// the bound still holding.
func TestPruneTwiceChangesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{
		"20260825T090000Z-0001", "20260825T100000Z-0002", "20260825T110000Z-0003",
	} {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
	}

	res, err := Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 {
		t.Fatalf("moved %d runs, want 1", res.Moved)
	}
	first := state(t)
	for round := range 2 {
		res, err := Prune(2)
		if err != nil {
			t.Fatalf("second prune %d: %v", round, err)
		}
		if res.Moved != 0 {
			t.Errorf("second prune %d moved %d runs, want none", round, res.Moved)
		}
		if got := state(t); got != first {
			t.Errorf("second prune %d changed the tree:\n got %q\nwant %q", round, got, first)
		}
	}

	// One more run, then the same prune: the repeat costs exactly the new run.
	record(t, "20260825T120000Z-0004", base.Add(3*time.Hour))
	res, err = Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 {
		t.Fatalf("prune after a new run moved %d runs, want 1", res.Moved)
	}
	// The bound holds on both sides of the repeat: the newest two runs are
	// listed and indexed, and the quarantine is back to its own keep rather
	// than one per prune.
	want := "listed [20260825T120000Z-0004 20260825T110000Z-0003] " +
		"quarantined [20260825T100000Z-0002 20260825T090000Z-0001] " +
		"indexed [20260825T110000Z-0003 20260825T120000Z-0004]"
	if got := state(t); got != want {
		t.Errorf("prune after a new run left:\n got %q\nwant %q", got, want)
	}
}

// A move is recoverable and an eviction is not, so the two come back apart.
// The quarantine is bounded by the same keep the listing is, which is what
// makes lowering the bound the one way a prune destroys history the tree was
// still holding: the runs it moves aside are restorable and the ones the new
// bound no longer covers are unlinked. The count is what a run tells the
// operator it destroyed, and without it the loss is silent.
func TestPruneReportsTheRunsItEvictedFromTheQuarantine(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	for i, id := range []string{
		"20260825T090000Z-0001", "20260825T100000Z-0002", "20260825T110000Z-0003",
		"20260825T120000Z-0004", "20260825T130000Z-0005",
	} {
		record(t, id, base.Add(time.Duration(i)*time.Hour))
	}

	res, err := Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 3 {
		t.Errorf("moved %d runs, want the three outside the bound", res.Moved)
	}
	// Three moved aside, and the bound covers two, so the oldest of them is
	// unlinked: the one journal no rename put anywhere.
	if res.Evicted != 1 {
		t.Errorf("evicted %d runs, want the one the quarantine bound no longer covered", res.Evicted)
	}
	want := "listed [20260825T130000Z-0005 20260825T120000Z-0004] " +
		"quarantined [20260825T110000Z-0003 20260825T100000Z-0002] " +
		"indexed [20260825T120000Z-0004 20260825T130000Z-0005]"
	if got := state(t); got != want {
		t.Errorf("prune(2) over five runs left:\n got %q\nwant %q", got, want)
	}

	// A prune with nothing to move evicts nothing, so the run's own line does
	// not claim a loss on every ordinary run.
	res, err = Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if res != (PruneResult{}) {
		t.Errorf("a prune inside the bound reports %+v, want nothing moved or evicted", res)
	}
}

// state is the whole of what a prune can change: the runs in the listing, the
// index rows, and the quarantines still recoverable.
func state(t *testing.T) string {
	t.Helper()
	rows, err := Recent(100)
	if err != nil {
		t.Fatal(err)
	}
	listed := make([]string, 0, len(rows))
	for _, s := range rows {
		listed = append(listed, s.RunID)
	}
	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	indexed := make([]string, 0, len(rows))
	for id := range readIndexFile(t) {
		indexed = append(indexed, id)
	}
	slices.Sort(indexed)
	return fmt.Sprintf("listed %v quarantined %v indexed %v", listed, held, indexed)
}

// The writes carry O_NOFOLLOW because a GAUNTLET_HOME with no usable HOME
// resolves beside the working directory, inside the reviewed tree, where a
// committed .gauntlet/runs/<shard>/<id>.jsonl link is a file the repository
// chooses. The reads have to refuse the same link: `show` parses what it
// reads and prints it, so a planted one would surface the contents of any file
// on the machine as a run's event stream.
func TestReadsRefuseAPlantedSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	start := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	const id = "20260825T090000Z-0001"
	record(t, id, start)
	real := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if _, err := os.Lstat(real); err != nil {
		t.Fatalf("the recorded journal is not where it should be: %v", err)
	}

	// The target holds what a leak would print: a line that is not an event.
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(`{"api_key":"sk-not-a-real-key"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, real); err != nil {
		t.Fatal(err)
	}

	var got []map[string]any
	if err := Events(id, func(ev map[string]any) { got = append(got, ev) }); err == nil {
		t.Fatal("Events followed a symlink out of the state root")
	}
	if len(got) != 0 {
		t.Fatalf("the planted link was parsed as event stream: %v", got)
	}
	if _, err := os.Lstat(real); err != nil {
		t.Fatal("the planted link should survive the refusal: ", err)
	}
}
