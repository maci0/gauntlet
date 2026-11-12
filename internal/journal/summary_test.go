// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import "testing"

// A row written before timeouts was its own field counted deadlines inside
// failed and carries no key to say so. decodeSummary is the one place that
// reads an index line, and it has to mark that row: a reader that added the
// new field on top of the old failed count would count each deadline twice,
// and one that trusted a zero timeouts would report a deadline as an agent
// failure. A row this build writes names the count, zero included, and is
// not marked.
func TestDecodeSummaryMarksARowThatFoldedTimeouts(t *testing.T) {
	old := []byte(`{"run_id":"20260101T000000Z-abcd","reviews":2,"ok":1,"failed":1}`)
	got, ok := decodeSummary(old)
	if !ok {
		t.Fatal("an old row did not decode")
	}
	if !got.Folded || got.Timeouts != 0 || got.Failed != 1 {
		t.Fatalf("old row read as split: %+v", got)
	}
	if n, known := got.TimeoutsCounted(); known || n != 0 {
		t.Fatalf("TimeoutsCounted = %d, %t, want 0, false", n, known)
	}

	fresh := []byte(`{"run_id":"20260101T000000Z-abcd","reviews":2,"ok":1,"failed":0,"timeouts":1}`)
	got, ok = decodeSummary(fresh)
	if !ok {
		t.Fatal("a fresh row did not decode")
	}
	if got.Folded || got.Timeouts != 1 || got.Failed != 0 {
		t.Fatalf("fresh row read as folded: %+v", got)
	}
	if n, known := got.TimeoutsCounted(); !known || n != 1 {
		t.Fatalf("TimeoutsCounted = %d, %t, want 1, true", n, known)
	}

	// Zero is a count, not an absence: a run that passed everything still
	// names the field, and must not be read as a row that hid its deadlines.
	none := []byte(`{"run_id":"20260101T000000Z-abcd","reviews":1,"ok":1,"failed":0,"timeouts":0}`)
	got, ok = decodeSummary(none)
	if !ok || got.Folded {
		t.Fatalf("a row that recorded no timeouts was marked folded: %+v", got)
	}
	if _, ok = decodeSummary([]byte(`{"run_id":`)); ok {
		t.Fatal("a torn line decoded")
	}
}
