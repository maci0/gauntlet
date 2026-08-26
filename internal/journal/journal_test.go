// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// collect replays runID's journal through Events and returns what it visited,
// for tests that assert on whole streams.
func collect(runID string) (out []map[string]any, err error) {
	err = Events(runID, func(m map[string]any) { out = append(out, m) })
	return out, err
}

// A journal that cannot be written must not still look complete: the first
// write error survives to Close, which is where the run reports it.
func TestWriteErrorSurvivesToClose(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	now := time.Now()
	j, err := Open("broken-run", now)
	if err != nil {
		t.Fatal(err)
	}
	// Close the file under the writer: every later flush fails the way a full
	// disk does, without needing one.
	if err := j.f.Close(); err != nil {
		t.Fatal(err)
	}
	for range 4096 {
		j.Write(map[string]string{"ev": "log", "text": "a line long enough to fill the buffer"})
	}
	j.Flush()
	if closeErr := j.Close(Summary{Version: "test", Start: now, End: now}); closeErr == nil {
		t.Fatal("a journal that failed to write reported a clean close")
	}
}

func TestHomeHonorsOverride(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", "/somewhere/else")
	if got := Home(); got != "/somewhere/else" {
		t.Fatalf("GAUNTLET_HOME ignored: %q", got)
	}
	t.Setenv("GAUNTLET_HOME", "")
	if got := Home(); !strings.HasSuffix(got, ".gauntlet") {
		t.Fatalf("default home should be ~/.gauntlet, got %q", got)
	}
}

func TestWriteAndReplay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	type ev struct {
		Kind   string    `json:"ev"`
		Time   time.Time `json:"ts"`
		Review string    `json:"review,omitempty"`
	}
	j.Write(ev{Kind: "run_start", Time: now})
	j.Write(ev{Kind: "review_end", Time: now, Review: "sec-review"})
	j.Flush()

	if err := j.Close(Summary{
		Version: "test", Dirs: []string{"/repo"}, Start: now,
		End: now.Add(time.Minute), Loops: 1, Reviews: 1, OK: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Date-sharded path, one file per run.
	want := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("journal not at %s: %v", want, err)
	}

	events, err := collect(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1]["review"] != "sec-review" {
		t.Fatalf("replay wrong: %+v", events)
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != id || runs[0].OK != 1 {
		t.Fatalf("index wrong: %+v", runs)
	}
	if runs[0].Path != want {
		t.Fatalf("index should point at the journal: %q", runs[0].Path)
	}
}

func TestOpenShardsByUTCNotLocalZone(t *testing.T) {
	// The shard must agree with the UTC date embedded in the run id no matter
	// which zone the host runs in. This instant is 2026-08-26 local and
	// 2026-08-25 UTC, so a local-dated shard would land one day off.
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	loc := time.FixedZone("test+2", 2*60*60)
	now := time.Date(2026, 8, 26, 0, 30, 0, 0, loc)
	id := NewRunID(now)
	if !strings.HasPrefix(id, "20260825T") {
		t.Fatalf("run id should carry the UTC date, got %q", id)
	}
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Close(Summary{Start: now.UTC(), End: now.UTC()})

	want := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("journal not at %s: %v", want, err)
	}
}

func TestOpenResumesTheSameFileAcrossUTCMidnight(t *testing.T) {
	// A hot reload continues the same run in a new process. If the exec lands
	// past UTC midnight, deriving the shard from the successor's clock would
	// split one run's stream over two files and a replay by id would find only
	// the newer half. The run's existing file must be followed instead.
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	type ev struct{ Kind string }
	first := time.Date(2026, 8, 25, 23, 59, 40, 0, time.UTC)
	id := NewRunID(first)

	j, err := Open(id, first)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(ev{"run_start"})
	j.Flush()
	j.CloseQuiet()

	second := time.Date(2026, 8, 26, 0, 0, 10, 0, time.UTC)
	j2, err := Open(id, second)
	if err != nil {
		t.Fatal(err)
	}
	j2.Write(ev{"loop_end"})
	j2.Flush()
	want := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if err := j2.Close(Summary{Version: "test", Start: first.UTC(), End: second.UTC()}); err != nil {
		t.Fatal(err)
	}

	events, err := collect(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0]["Kind"] != "run_start" || events[1]["Kind"] != "loop_end" {
		t.Fatalf("replay lost or reordered events across the reload: %+v", events)
	}
	if _, err := os.Stat(filepath.Join(home, "runs", "2026-08-26")); err == nil {
		t.Fatal("a second shard must not be created for one run")
	}
	runs, err := Recent(1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("index should hold exactly one row: %v %v", runs, err)
	}
	if runs[0].Path != want {
		t.Fatalf("index should point at the one journal file: %q", runs[0].Path)
	}
}

func TestRecentIsNewestFirstAndSkipsGarbage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	for i := range 3 {
		now := time.Date(2026, 8, 25, 13, i, 0, 0, time.UTC)
		j, err := Open(NewRunID(now)+string(rune('a'+i)), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.Close(Summary{Start: now, End: now, Loops: i}); err != nil {
			t.Fatal(err)
		}
	}
	// A truncated line (a killed process) must not break the listing.
	f, err := os.OpenFile(filepath.Join(home, "index.jsonl"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"run_id\": trunca\n")
	f.Close()

	runs, err := Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("want 2 entries, got %d", len(runs))
	}
	if runs[0].Loops != 2 || runs[1].Loops != 1 {
		t.Fatalf("not newest first: %+v", runs)
	}
}

func TestRecentOnEmptyHome(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	runs, err := Recent(5)
	if err != nil || len(runs) != 0 {
		t.Fatalf("a fresh home should be empty, got %v %v", runs, err)
	}
}

func TestRecentReadsPastTheFirstChunk(t *testing.T) {
	// An index bigger than one backward read must still yield the right tail,
	// including when n reaches across the slice boundary.
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	const total = 20000 // ~800 KB of index: forces the backward reads to widen
	var b strings.Builder
	for i := range total {
		b.WriteString(fmt.Sprintf(`{"run_id":"r%d","loops":%d,"dirs":["/d"]}`, i, i))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(home, "index.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{1, 10, total} {
		runs, err := Recent(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) != n {
			t.Fatalf("n=%d: got %d entries", n, len(runs))
		}
		if runs[0].RunID != fmt.Sprintf("r%d", total-1) || runs[0].Loops != total-1 {
			t.Fatalf("n=%d: newest entry wrong: %+v", n, runs[0])
		}
		if runs[n-1].Loops != total-n {
			t.Fatalf("n=%d: oldest entry wrong: %+v", n, runs[n-1])
		}
	}
}

func TestCloseAfterCloseQuietStillIndexesOnce(t *testing.T) {
	// A hot reload closes the journal quietly and execs into the new binary,
	// which was to write the one summary row. When that exec fails, the dying
	// process must finish its own run: exactly one index row, whatever was
	// closed before it, and no second row from a repeated Close. Without this
	// a failed swap strands the run unindexed and gauntlet runs never shows
	// it happened.
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]string{"ev": "run_start"})
	j.CloseQuiet()

	want := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if err := j.Close(Summary{Version: "test", Start: now, End: now}); err != nil {
		t.Fatalf("closing after a quiet close should still index the run: %v", err)
	}
	if err := j.Close(Summary{Version: "test", Start: now, End: now}); err != nil {
		t.Fatalf("repeated close should be a no-op: %v", err)
	}

	events, err := collect(id)
	if err != nil || len(events) != 1 {
		t.Fatalf("events must survive the quiet close: %v %+v", err, events)
	}
	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != id || runs[0].Path != want {
		t.Fatalf("index must hold exactly one row pointing at the journal: %+v (want %s)", runs, want)
	}
}

func TestNilJournalIsUsable(t *testing.T) {
	// Journaling is never load-bearing: a run whose journal could not be
	// opened must still work.
	var j *Journal
	j.Write(struct{}{})
	j.Flush()
	j.CloseQuiet()
	if err := j.Close(Summary{}); err != nil {
		t.Fatalf("closing a nil journal should be a no-op: %v", err)
	}
}

// FuzzParseTail feeds parseTail an arbitrary index image and pins the
// contract Recent relies on to list runs: at most n entries come back, a hit
// returns exactly n, a miss returns every summary the region holds, order is
// newest first regardless of n, and a truncated head line (dropFirst) is
// dropped rather than half-parsed.
func FuzzParseTail(f *testing.F) {
	seeds := []string{
		`{"run_id":"r2","loops":2}` + "\n" + `{"run_id":"r1","loops":1}`,
		`{"run_id":"r0","start":"2026-08-25T12:00:00Z","end":"2026-08-25T12:01:00Z"}`,
		"{\"run_id\": trunca\n",
		"\n\n{}",
		`garbage` + "\n" + `{"run_id":"ok"}` + "\n" + `also garbage`,
		strings.Repeat(`{"run_id":"x"}`+"\n", 50),
		"",
		"\nno trailing newline",
	}
	for _, s := range seeds {
		f.Add([]byte(s), false, 3)
		f.Add([]byte(s), true, 1)
	}
	// Regression: a negative count once panicked in make before the guard.
	f.Add([]byte(`{"run_id":"r"}`), true, -42)
	f.Add([]byte(`{"run_id":"r"}`), false, 0)
	f.Fuzz(func(t *testing.T, data []byte, dropFirst bool, n int) {
		if n <= 0 {
			// Below 1 is outside the contract Recent establishes (n > 0);
			// reaching here at all must merely be safe.
			parseTail(data, dropFirst, n)
			return
		}
		if n > 4096 {
			n = 4096
		}
		got, enough := parseTail(data, dropFirst, n)

		// Independent oracle: the summaries a reader can parse out of the
		// region, oldest last, with an incomplete head line removed. The
		// contract is that got is exactly its tail of length n.
		lines := strings.Split(string(data), "\n")
		first := 0
		if dropFirst && len(lines) > 0 {
			first = 1
		}
		var want []Summary
		for i := first; i < len(lines); i++ {
			var s Summary
			if err := json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &s); err != nil {
				continue
			}
			want = append(want, s)
		}
		slices.Reverse(want)
		take := min(n, len(want))
		if len(got) != take {
			t.Fatalf("got %d entries, want %d", len(got), take)
		}
		if enough != (len(want) >= n) {
			t.Fatalf("enough=%v for %d entries and n=%d", enough, len(want), n)
		}
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Fatalf("entry %d: got %+v, want %+v", i, got[i], want[i])
			}
		}

		// Ordering must not depend on n: a longer read extends the same list.
		longer, _ := parseTail(data, dropFirst, n+1)
		for i := range got {
			if i < len(longer) && !reflect.DeepEqual(longer[i], got[i]) {
				t.Fatalf("order changed with n: [%d] %+v vs %+v", i, got[i], longer[i])
			}
		}
	})
}

func TestEventsUnknownRun(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	err := Events("nope", nil)
	if err == nil {
		t.Fatal("unknown run should error")
	}
	// A fresh home has no runs/ directory yet; the miss must read as a miss,
	// not as the raw ENOENT of an internal path.
	if strings.Contains(err.Error(), "open ") {
		t.Errorf("missing journal tree leaked a raw filesystem error: %v", err)
	}
	if !strings.Contains(err.Error(), "no journal for run nope") {
		t.Errorf("error should name the run and where it looked: %v", err)
	}

	// Same message once the tree exists but the id is still wrong.
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "runs", "2026-08-25"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = Events("nope", nil)
	if err == nil || !strings.Contains(err.Error(), "no journal for run nope") {
		t.Errorf("existing tree, unknown id: got %v", err)
	}
}

func TestEventsRejectsTraversalIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	// A file the lookup must never reach even though the id names it.
	outside := filepath.Join(home, "secret.jsonl")
	if err := os.WriteFile(outside, []byte(`{"ev":"leaked"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "runs", "2026-08-25"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"../secret",                         // climbs out of the shard
		"..%2Fsecret",                       // percent junk is not an id either
		filepath.Join("..", "..", "secret"), // separator-carrying
		"20260825T120000Z-0001/../../../../../secret", // mixed
		"",                                 // empty
		strings.Repeat("a", maxRunIDLen+1), // overlong
	} {
		events, err := collect(id)
		if err == nil || !errors.Is(err, ErrNoJournal) {
			t.Errorf("id %q: want ErrNoJournal, got %v (%+v)", id, err, events)
		}
	}

	// A generated id still resolves.
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]string{"ev": "run_start"})
	if err := j.Close(Summary{Start: now, End: now}); err != nil {
		t.Fatal(err)
	}
	if err := Events(id, func(map[string]any) {}); err != nil {
		t.Errorf("generated id rejected: %v", err)
	}
}
