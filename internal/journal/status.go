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
	// The index tail repair appends a journal left at the end of the file;
	// a hole behind a later Close is filled for the listing only and never
	// appended, and nothing reconstructs a row whose journal is gone, which
	// is why that counts.
	Disagreed int
	// Pruned is how many journals the retention bound moved to pruned/ and
	// can still restore.
	Pruned int
	// Truncated is how many journals, under runs/ or in the quarantine, end
	// without the newline every recorded event ends with: a file cut mid-line
	// by a power cut, or copied while a run was still writing it. Nothing
	// else reports such a file, because the half-line it ends on is not JSON
	// and every reader drops it, so the run replays as a shorter run that
	// looks complete. A restore verified only on the counts above cannot see
	// it; this count is what tells a whole archive from a short one.
	Truncated int
}

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
	for _, j := range journals {
		if truncated(j.path) {
			st.Truncated++
		}
	}
	for _, q := range held {
		if truncated(q.path) {
			st.Truncated++
		}
	}
	return st, nil
}

// truncated reports whether the JSONL file at path ends mid-line: its last
// byte is not the newline a recorded event always ends with.
//
// A journal is only ever written whole lines (json.Encoder terminates every
// value it writes), so a file that ends on anything else was cut: a power cut
// part way through a write, a filesystem that lost the tail, or an archive job
// that copied the tree with a run still writing to it. The half-line reads as
// nothing to every reader here, so the run it belongs to lists, replays, and
// summarizes as a complete run that is missing its last events, and a restore
// checked only on how many runs survived cannot tell. An empty file is not
// truncated: it is a run that has not recorded anything yet, or one whose
// events are all still in the write buffer.
//
// One byte is read, at the end, through openRead: a file that cannot be opened
// as a regular journal is left to the walk that already reported it, rather
// than counted as a short one.
func truncated(path string) bool {
	f, err := openRead(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], fi.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}
