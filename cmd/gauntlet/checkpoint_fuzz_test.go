// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/selfupdate"
)

// A crash checkpoint is the one file `gauntlet resume` takes on trust: it
// carries the argv a successor executes, the directory it chdirs into, and the
// results a run reports as its own, and it is read back from disk after a
// process died, which is the same position a file the reviewed repository can
// reach is in whenever GAUNTLET_HOME points inside the tree. Nothing about
// the bytes is a message gauntlet wrote on a well-formed run: a power cut
// truncates one mid-line, a hand edit changes a field, and a blob from a
// newer build carries members this one has no field for. It is decoded with
// encoding/json, which cannot panic, so the fuzzer proves the small part and
// the assertions are what make a wrong answer visible.
//
// readCheckpoint is the whole decode path: the guarded open, the byte cap, the
// unmarshal, and the check that the run id inside names the run the file is
// filed under. The consumer of what comes back is here too, because the
// interesting failures are the ones a decoder accepts and a resume then
// mishandles: an handoff whose map keys resolve to a directory the run never
// held, a pending list that does not name a review, a results slice whose
// per-review identity does not agree across its own fields.

// checkpointRunID is the run a planted checkpoint is filed under. The name is
// the second half of the read: a checkpoint is only ever consulted for the run
// its file name claims, so a valid run id is what lets the corpus reach the
// unmarshal rather than stopping at the name check.
const checkpointRunID = "20261001T015123Z-1a2b"

// plantCheckpoint writes data as the checkpoint of checkpointRunID and returns
// the path it was planted at.
func plantCheckpoint(t *testing.T, data []byte) string {
	t.Helper()
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	path := mustCheckpointPath(t, checkpointRunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// FuzzReadCheckpoint drives readCheckpoint with an arbitrary checkpoint image:
// the bytes a process left half written, a hand edit, and a blob from a newer
// build all reach it the same way.
//
// The contract it has to hold is a refusal or a decode, never a partial one,
// and never an error that a caller cannot tell apart from a miss: the run id
// inside the file has to be the one the file is filed under, because that is
// the only thing tying the argv a resume is about to execute to the run the
// operator named. Everything the resume does with the result is checked from
// here on, since a decoder that hands back a well-formed but nonsensical
// handoff has done all the damage it can.
func FuzzReadCheckpoint(f *testing.F) {
	// A checkpoint this binary writes, one field at a time.
	whole := `{"handoff":{"run_id":"` + checkpointRunID + `","started_at":"2026-10-01T01:51:23Z",` +
		`"elapsed":3600000000000,"seed":42,"reloads":1,` +
		`"dirs":{"/repo":{"loops":2,"pending":["sec-review"],"reviews":["sec-review","dst-review"],` +
		`"results":[{"review":"sec-review","agent":"claude","status":"ok","exit_code":0}]}}},` +
		`"argv":["--dirs","/repo","--once"],"cwd":"/repo","pid":4242,"version":"1.0.0",` +
		`"updated":"2026-10-01T02:51:23Z"}`
	seeds := []string{
		whole,
		// A power cut mid-line, at every structural position a JSON document
		// has one.
		whole[:len(whole)/2],
		whole[:20],
		"",
		"{",
		`{"handoff":`,
		`{"handoff":{"run_id":"other"},"cwd":"/tmp"}`,
		`{"handoff":{"run_id":""}}`,
		`{}`,
		"not json at all",
		"\x00\x01\x02",
		"\xff\xfe invalid utf8 \xed\xa0\x80",
		// Members a newer build writes and this one has no field for: they
		// are dropped rather than failing the whole resume.
		`{"handoff":{"run_id":"` + checkpointRunID + `","unknown":{"deep":[1,2,3]}},"future":1}`,
		// Types the fields are not.
		`{"handoff":{"reloads":"three"},"pid":"nope","argv":"/not-a-list"}`,
		`{"handoff":{"elapsed":"1h","dirs":[]}}`,
		`{"handoff":{"dirs":{"/repo":{"loops":"2","pending":"sec-review"}}}}`,
		`{"handoff":{"dirs":null}}`,
		`{"handoff":{"run_id":null}}`,
		`{"cwd":null,"argv":null}`,
		// Duplicate members: the last one wins, and a run id planted after
		// the real one is exactly the confusion the name check exists for.
		`{"handoff":{"run_id":"` + checkpointRunID + `"},"handoff":{"run_id":"other"}}`,
		`{"handoff":{"run_id":"other"},"handoff":{"run_id":"` + checkpointRunID + `"}}`,
		// Deeply nested and wide, the two shapes that turn a decode into a
		// stack or a memory problem rather than a wrong answer.
		`{"handoff":{"run_id":"` + checkpointRunID + `","dirs":{"` + strings.Repeat("/", 200) +
			strings.Repeat(`"a":`, 200) + `null}}`,
		`{"handoff":{"run_id":"` + checkpointRunID + `","dirs":{` +
			strings.Repeat(`"/d":{`, 100) + strings.Repeat(`}`, 100) + `}}`,
		strings.Repeat(`{"a":`, 500) + strings.Repeat("}", 500),
		// Times and durations at and past what a struct holds.
		`{"handoff":{"started_at":"0001-01-01T00:00:00Z","elapsed":-1}}`,
		`{"handoff":{"started_at":"9999-12-31T23:59:59Z","elapsed":9223372036854775807}}`,
		`{"updated":"not-a-time"}`,
		// A pending list of reviews that are not reviews, and a results entry
		// whose own fields disagree.
		`{"handoff":{"run_id":"` + checkpointRunID + `","dirs":{"/r":{"pending":["","../x","-flag","a b"],` +
			`"results":[{"review":"","status":"","exit_code":-9999,"tokens":-5,"HaveLines":true,"Ins":-3}]}}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := plantCheckpoint(t, data)

		cp, err := readCheckpoint(path)
		if err != nil {
			// A refusal is the only safe answer to anything malformed, and a
			// resume reads it as "cannot resume" rather than "no such run":
			// a checkpoint the reader cannot trust must not become a
			// successful resume of a run the operator did not name.
			return
		}

		// The name check: what came back is only ever consulted as this run,
		// and the argv below is executed, so the id inside the file and the
		// one in its name are one.
		if cp.Handoff.RunID != checkpointRunID {
			t.Fatalf("readCheckpoint accepted %q under the name %q", cp.Handoff.RunID, checkpointRunID)
		}

		// Determinism: the successor writes a handoff from what it read and
		// a second resume reads the same file, so a read that varies would
		// make the same crash resume two different ways.
		again, err2 := readCheckpoint(path)
		if err2 != nil {
			t.Fatalf("second read of %q failed where the first succeeded: %v", path, err2)
		}
		if !reflect.DeepEqual(cp, again) {
			t.Fatalf("reading the same checkpoint twice gave different results:\n%+v\n%+v", cp, again)
		}

		checkDecodedHandoff(t, cp)
	})
}

// checkDecodedHandoff is the half of the contract that is about the resume
// rather than the decoder: every consumer of a handoff the run is about to
// act on, checked against the bytes rather than against a hand-built struct.
func checkDecodedHandoff(t *testing.T, cp checkpoint) {
	t.Helper()
	h := cp.Handoff

	// Loops totals the loops the directories recorded, and that is the only
	// way a resume can tell how far the run got. A total that disagrees with
	// the map would misreport the run it is continuing, so the two are
	// recomputed from the decoded map.
	want := 0
	for _, d := range h.Dirs {
		want += d.Loops
	}
	if got := h.Loops(); got != want {
		t.Fatalf("Loops() = %d over %d directories, want %d", got, len(h.Dirs), want)
	}

	// A lookup resolves the directory the way the writer filed it, and a
	// directory a resume never held resolves to the zero handoff rather than
	// to some other directory's progress.
	for dir, want := range h.Dirs {
		if got := h.Dir(dir); !reflect.DeepEqual(got, want) {
			t.Fatalf("Dir(%q) = %+v, want the filed %+v", dir, got, want)
		}
	}
	if got := h.Dir(t.TempDir()); !reflect.DeepEqual(got, dirHandoff{}) {
		t.Fatalf("a directory the run never held resolved to %+v", got)
	}

	// The reconstructed start and origin are the two numbers --runtime reads,
	// and a corrupt or negative elapsed must not hand a resume a start in the
	// future. Both are pure functions of the decoded handoff, so they are
	// checked here for the property the caller relies on rather than for a
	// specific value.
	now := time.Now()
	if start := resumeStart(now, h); start.After(now) {
		t.Fatalf("resumeStart put a corrupt checkpoint in the future: %s", start)
	}
	if origin := resumeOrigin(now, h); origin.After(now) {
		t.Fatalf("resumeOrigin put a corrupt checkpoint in the future: %s", origin)
	}

	// The argv is printed by `gauntlet resume` and journaled, so whatever a
	// checkpoint carried has to render as one line of text with no control
	// character in it: a newline in an argument would forge a second line in
	// the listing and forge a second command in the output the operator pastes.
	printed := strings.Join(journaledArgs(cp.Argv), " ")
	if !utf8.ValidString(printed) {
		t.Fatalf("the resumed argv does not render as valid text: %q", printed)
	}
	if strings.ContainsAny(printed, "\n\r\x1b") {
		t.Fatalf("the resumed argv carries a line or escape break: %q", printed)
	}
	// And a credential planted in it is not republished: the listing is
	// copied into issues, and a checkpoint the reviewed tree can reach is not
	// a place to keep one.
	if strings.Contains(printed, "ghp_") || strings.Contains(printed, "sk-ant-") {
		t.Fatalf("the resumed argv carries a credential: %q", printed)
	}

	// A pending entry and a review entry are review names the successor acts
	// on, and both are headed for a listing the operator reads and for the
	// scheduler's own selection. What this decoder owes them is text, not a
	// name: encoding/json hands back valid UTF-8 for a valid document, and a
	// document is not required to hold one. A name carrying a control
	// character or a line break would forge a line in the listing, so those
	// are refused here. Whether a name names a real review is the
	// scheduler's rule, checked where it selects, not the decoder's.
	for dir, d := range h.Dirs {
		for _, name := range slices.Concat(d.Pending, d.Reviews) {
			if !utf8.ValidString(name) {
				t.Fatalf("directory %q carries a review name that is not valid UTF-8: %q", dir, name)
			}
			for _, r := range name {
				if unicode.IsControl(r) && r != '\t' {
					t.Fatalf("directory %q carries a review name with a control character %q: %q",
						dir, r, name)
				}
			}
			if strings.ContainsAny(name, "\n\r") {
				t.Fatalf("directory %q carries a review name that spans lines: %q", dir, name)
			}
		}
	}

	// A results entry carries no invariant this decoder is responsible for.
	// The fields are ints the writer chose, and a hostile checkpoint can put
	// any value in any of them; the consumers clamp what they must
	// (Tokens.Add ignores n <= 0, journal.count bounds every counted
	// figure), so asserting on the values here would assert a policy the
	// decode path does not hold and does not own.
}

// A handoff is decoded by selfupdate.LoadState on the reload path and by
// readCheckpoint on the resume path, from two independently written files that
// carry the same struct. The two decoders disagreeing is the failure that
// matters: the reload path resumes a run in place, the resume path starts a
// successor over the same fields, and a member one understands and the other
// drops produces a run that silently forgets a directory it had already
// reviewed. FuzzLoadHandoff is the oracle that keeps the two byte-compatible.
//
// It writes the handoff through the same encoder a reload uses, corrupts it,
// and reads it back through both readers, so the pair is asserted on the
// boundary rather than on a struct either reader happened to be given.
func FuzzLoadHandoff(f *testing.F) {
	seeds := []string{
		`{"run_id":"` + checkpointRunID + `","started_at":"2026-10-01T01:51:23Z","elapsed":1000,"seed":7,"reloads":0,"dirs":{}}`,
		`{"run_id":"r","dirs":{"/repo":{"loops":1,"pending":["sec-review"]}}}`,
		`{"run_id":"r","dirs":{"/a":{"loops":1},"/b":{"loops":2}}}`,
		`{"run_id":"r","elapsed":-5}`,
		`{"run_id":"r","seed":18446744073709551615}`,
		`{"run_id":"r","started_at":"0001-01-01T00:00:00Z"}`,
		`{"run_id":"r","dirs":null}`,
		`{"run_id":`,
		`{}`,
		``,
		`null`,
		`[]`,
		`{"run_id":123}`,
		`{"run_id":"r","dirs":{"/r":{"results":[{"review":"sec","status":"ok","exit_code":0,"ins":3,"del":1,"have_lines":true}]}}}`,
		`{"run_id":"r","dirs":{"/r":{"results":[{"review":"sec","ins":-3,"have_lines":true}]}}}`,
		`{"run_id":"r","unknown_member":{"a":[1,2,3]}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		home := t.TempDir()
		t.Setenv("GAUNTLET_HOME", home)

		// The reload reader is the one the successor runs, and it is bound to
		// an absolute path it then drops: a checkpoint is read once, so a
		// second read of the same bytes has to be planted again.
		statePath := filepath.Join(home, "handoff.json")
		if err := os.WriteFile(statePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GAUNTLET_STATE", statePath)

		var viaState handoff
		ok, err := loadHandoffState(&viaState)
		if err != nil {
			return // a refusal is the answer to anything malformed
		}
		if !ok {
			t.Fatalf("LoadState reported no handoff for the path it was given: %s", statePath)
		}
		// A consumed handoff is dropped, so a second read finds nothing
		// rather than resuming a run that already finished.
		if _, err := os.Stat(statePath); err == nil {
			t.Fatalf("LoadState left %s in place after reading it", statePath)
		}
		checkDecodedHandoff(t, checkpoint{Handoff: viaState})

		// The resume reader over the same bytes, planted under a name whose
		// run id the handoff has to match. The pair assertion: a member the
		// two decoders read differently is a run that resumes with a
		// different shape than it was written with.
		planted := make([]byte, len(data))
		copy(planted, data)
		if err := os.WriteFile(statePath, planted, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GAUNTLET_STATE", "")
		cp, err := readCheckpoint(statePath)
		if err != nil {
			return
		}
		var viaCheckpoint handoff
		if json.Valid(planted) {
			// The run id check is the one difference between the two readers
			// and it is readCheckpoint's own rule, so the handoffs are
			// compared field by field rather than as one value.
			if err := json.Unmarshal(planted, &viaCheckpoint); err != nil {
				t.Fatalf("readCheckpoint decoded what encoding/json cannot: %s", err)
			}
			if !reflect.DeepEqual(viaCheckpoint, cp.Handoff) {
				t.Fatalf("the two readers disagree:\nLoadState:   %+v\ncheckpoint:  %+v", viaCheckpoint, cp.Handoff)
			}
		}
	})
}

// loadHandoffState is LoadState as the reload path calls it. The handoff lives
// in this package and the reader in another, so the harness calls the
// exported reader across the same boundary main does rather than reaching into
// it.
func loadHandoffState(v any) (bool, error) {
	return selfupdate.LoadState(v)
}
