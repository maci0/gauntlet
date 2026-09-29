// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"os"
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

	// The tail a run that lost power mid-write leaves: a complete line, then a
	// second event cut before its JSON finished. The listing still names the
	// run, so only the replay says the last event is gone.
	path := journalPath("20260825T090000Z-0001")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"ev\":\"loop_start\",\"loop\":2}\n{\"ev\":\"review_"); err != nil {
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
	// The run is not hidden, and it is not reported as whole: it replays the
	// one event that landed whole and drops the half-written one behind it.
	// An exact count, not a non-zero one: a replay that dropped the complete
	// line, or that emitted the torn one as a phantom, has restored nothing
	// worth having.
	var events []string
	if err := Events("20260825T090000Z-0001", func(m map[string]any) {
		ev, _ := m["ev"].(string)
		events = append(events, ev)
	}); err != nil {
		t.Fatalf("replaying a cut journal: %v", err)
	}
	if len(events) != 1 || events[0] != "loop_start" {
		t.Fatalf("a cut journal replays %v, want just the loop_start that landed", events)
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
}
