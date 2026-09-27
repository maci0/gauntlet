// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// status.go: what the state tree holds and whether the two copies of the run
// history agree. A listing repairs a disagreement on its own, so nothing here
// writes: this is the report an operator reads after a restore, when the
// question is not "can a run list" but "is what survived complete".

package journal

// Status is the state tree read as a whole: the journals, the index built
// from them, and the pruned journals still recoverable.
type Status struct {
	// Journals is how many event streams are under runs/. It is the
	// irreplaceable count: the index is derived from these files.
	Journals int
	// Rows is how many summaries index.jsonl can answer with, whether or not
	// they name a journal still on disk.
	Rows int
	// Disagreed is how many runs the two copies tell apart: a journal the
	// index does not name (a crash between the last Flush and Close, or a
	// listing that has not repaired it yet) plus a row whose journal is gone.
	// The next `gauntlet runs` appends the first kind; nothing reconstructs
	// the second, which is why it counts.
	Disagreed int
	// Pruned is how many journals the retention bound moved to pruned/ and
	// can still restore.
	Pruned int
}

// Consistent reports whether the two copies of the history tell the same
// story, the state a completed run leaves behind.
func (s Status) Consistent() bool { return s.Disagreed == 0 }

// Inspect reads the state tree and reports what it holds. A missing tree is
// zero runs, not an error: an install that has never run has nothing wrong
// with it. A walk error is returned, since a tree that cannot be read is a
// finding in itself.
func Inspect() (Status, error) {
	journals, err := listJournals()
	if err != nil {
		return Status{}, err
	}
	rows, err := readAllIndex()
	if err != nil {
		return Status{}, err
	}
	held, err := listQuarantined()
	if err != nil {
		return Status{}, err
	}

	present := make(map[string]struct{}, len(journals))
	for _, j := range journals {
		present[j.id] = struct{}{}
	}
	var st Status
	for _, r := range rows {
		if r.RunID == "" {
			// A row with no id cannot be matched to a journal either way;
			// it is a line the writer could not close, not a disagreement.
			continue
		}
		if _, ok := present[r.RunID]; !ok {
			st.Disagreed++
		}
	}
	named := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r.RunID != "" {
			named[r.RunID] = struct{}{}
		}
	}
	for _, j := range journals {
		if _, ok := named[j.id]; !ok {
			st.Disagreed++
		}
	}
	st.Journals, st.Rows, st.Pruned = len(journals), len(rows), len(held)
	return st, nil
}
