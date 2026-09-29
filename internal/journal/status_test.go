// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A journal is only ever written whole lines, so a file that ends on anything
// but a newline was cut: by a power cut, or by an archive job that copied the
// tree with a run still writing. The half-line is not JSON, so every reader
// drops it and the run replays as a shorter run that looks whole. Inspect is
// what a restore is checked against, so it is where the loss is counted.
func TestInspectCountsJournalsCutMidLine(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	record(t, "20260825T100000Z-0002", base.Add(time.Hour))

	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Truncated != 0 {
		t.Fatalf("two closed journals report %d truncated, want 0", st.Truncated)
	}

	// The tail a run that lost power mid-write leaves: a run_start, then a
	// second event that never got its newline. The listing still names the
	// run, so only the count says the last events are gone.
	path := journalPath("20260825T090000Z-0001")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ev":"loop_start","loop":2}`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st, err = Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Truncated != 1 {
		t.Fatalf("after a cut journal, Inspect reports %d truncated, want 1: %+v", st.Truncated, st)
	}
	if st.Journals != 2 || st.Rows != 2 {
		t.Fatalf("a cut journal is still a journal: %+v", st)
	}
	// The run is not hidden, and it is not reported as whole: it replays with
	// the events that did land.
	var events int
	Events("20260825T090000Z-0001", func(map[string]any) { events++ })
	if events == 0 {
		t.Fatal("a cut journal must still replay the events that did land")
	}
}

// The quarantine holds the runs a prune kept recoverable, so a journal cut
// there is the same loss one tree further out, and a restore that only
// restores a half-line has not restored the run.
func TestInspectCountsQuarantinedJournalsCutMidLine(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	record(t, "20260825T090000Z-0001", base)
	record(t, "20260825T100000Z-0002", base.Add(time.Hour))
	if _, err := Prune(1); err != nil {
		t.Fatal(err)
	}
	held, err := Quarantined()
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 {
		t.Fatalf("the quarantine holds %v, want one run", held)
	}
	f, err := os.OpenFile(quarantinePath(held[0]), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ev":"loop_start"`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Truncated != 1 {
		t.Fatalf("Inspect reports %d truncated quarantined journals, want 1: %+v", st.Truncated, st)
	}
}

// An empty journal is a run that has recorded nothing yet, not a short one:
// every event is still in the write buffer, and a run that wrote nothing has
// lost nothing.
func TestInspectDoesNotCountAnEmptyJournalAsTruncated(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	base := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	if _, err := Open("20260825T090000Z-0001", base); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect()
	if err != nil {
		t.Fatal(err)
	}
	if st.Journals != 1 || st.Truncated != 0 {
		t.Fatalf("an unwritten journal reports %+v, want one journal and nothing truncated", st)
	}
	// And the file is left as it was: Inspect reads and does not repair.
	fi, err := os.Stat(journalPath("20260825T090000Z-0001"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 0 {
		t.Fatalf("Inspect wrote to the journal: %d bytes", fi.Size())
	}
	if strings.Contains(string(mustRead(t, journalPath("20260825T090000Z-0001"))), "truncat") {
		t.Fatal("the journal carries the report instead of the run")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
