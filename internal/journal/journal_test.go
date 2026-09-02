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
	"sync"
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

// Journal's contract is concurrent writers: parallel review lanes publish
// from their own goroutines while the loop end flushes from the consumer.
// Under that interleave every event must still land whole — one JSON object
// per line, none torn or interleaved mid-line by a racing encoder — and the
// close must write exactly one index row.
func TestJournalSurvivesConcurrentWriters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}

	const (
		lanes   = 8
		perLane = 200
	)
	type ev struct {
		Kind string `json:"ev"`
		Lane int    `json:"lane"`
		Seq  int    `json:"seq"`
		Pad  string `json:"pad"`
	}
	// Pad enough bytes that a lane's write crosses the bufio boundary: a torn
	// encode would land here as an unparseable line and surface as a miss.
	pad := strings.Repeat("x", 512)

	var wg sync.WaitGroup
	for lane := range lanes {
		wg.Go(func() {
			for seq := range perLane {
				j.Write(ev{Kind: "review_end", Lane: lane, Seq: seq, Pad: pad})
			}
		})
	}
	wg.Go(func() {
		for range 20 {
			j.Flush()
			time.Sleep(time.Millisecond)
		}
	})
	wg.Wait()

	if err := j.Close(Summary{
		Version: "test", Start: now, End: now.Add(time.Minute),
		Reviews: lanes * perLane, OK: lanes * perLane,
	}); err != nil {
		t.Fatal(err)
	}

	events, err := collect(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != lanes*perLane {
		t.Fatalf("lost events: got %d of %d", len(events), lanes*perLane)
	}
	seqs := make([][]int, lanes)
	for _, e := range events {
		if e["ev"] != "review_end" || e["pad"] != pad {
			t.Fatalf("torn event: %+v", e)
		}
		lane := int(e["lane"].(float64))
		seqs[lane] = append(seqs[lane], int(e["seq"].(float64)))
	}
	for lane, want := range seqs {
		if len(want) != perLane {
			t.Fatalf("lane %d recorded %d of %d events", lane, len(want), perLane)
		}
		for seq, n := range want {
			if n != seq {
				t.Fatalf("lane %d order broken at %d: %d", lane, seq, n)
			}
		}
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Reviews != lanes*perLane {
		t.Fatalf("index wrong after concurrent writers: %+v", runs)
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

func TestShardFromRunID(t *testing.T) {
	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	if got := shardFromRunID(id); got != "2026-08-25" {
		t.Fatalf("generated id %q: shard %q, want 2026-08-25", id, got)
	}
	for _, id := range []string{
		"", "broken-run", "20260825", "20260825T131500",
		"20260825X131500Z-1", "abcdefghT131500Z-1", "20260825TxxxxxxZ-1",
	} {
		if got := shardFromRunID(id); got != "" {
			t.Errorf("%q: got shard %q, want empty so locateRun scans", id, got)
		}
	}
}

// A generated id must resolve by opening its own date shard, not by listing
// every day directory: History and `gauntlet show` would otherwise pay one
// probe per day the install has ever run.
func TestLocateRunUsesTheDateInAGeneratedID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]string{"ev": "run_start"})
	if err := j.Close(Summary{Start: now, End: now}); err != nil {
		t.Fatal(err)
	}

	// Decoy shards newer and older, including one that would win a newest-first
	// scan if the dated path were skipped. They stay empty so a wrong lookup
	// is a miss, not a silent hit on the wrong file.
	for _, day := range []string{"2020-01-01", "2026-08-24", "2026-08-26", "2026-12-31"} {
		if err := os.MkdirAll(filepath.Join(home, "runs", day), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	p, ok, err := locateRun(id)
	if err != nil || !ok {
		t.Fatalf("generated id missed: ok=%v err=%v", ok, err)
	}
	want := filepath.Join(home, "runs", "2026-08-25", id+".jsonl")
	if p != want {
		t.Fatalf("located %q, want the dated shard %q", p, want)
	}
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

// History is what a directory's own past runs said about each review: how
// often it ran there, and how often it left lines behind. It is per directory,
// so a run over several trees does not credit one with another's work.
func TestHistoryCountsPerDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	ins, del := 12, 3
	events := []map[string]any{
		{"ev": "review_end", "dir": "/w/one", "review": "sec-review", "ins": ins, "del": del},
		{"ev": "review_end", "dir": "/w/one", "review": "sec-review"},
		{"ev": "review_end", "dir": "/w/one", "review": "doc-review"},
		{"ev": "review_end", "dir": "/w/two", "review": "sec-review", "ins": ins},
		{"ev": "loop_end", "dir": "/w/one"},
	}
	for _, e := range events {
		j.Write(e)
	}
	if err := j.Close(Summary{Dirs: []string{"/w/one", "/w/two"}}); err != nil {
		t.Fatal(err)
	}

	got, err := History("/w/one")
	if err != nil {
		t.Fatal(err)
	}
	if h := got["sec-review"]; h.Runs != 2 || h.Changed != 1 {
		t.Fatalf("sec-review in /w/one = %+v, want 2 runs and 1 that changed something", h)
	}
	if h := got["doc-review"]; h.Runs != 1 || h.Changed != 0 {
		t.Fatalf("doc-review in /w/one = %+v, want one run that changed nothing", h)
	}
	if _, ok := got["loop_end"]; ok {
		t.Fatal("History counted an event that is not a review")
	}
	other, err := History("/w/two")
	if err != nil {
		t.Fatal(err)
	}
	if h := other["sec-review"]; h.Runs != 1 || h.Changed != 1 {
		t.Fatalf("sec-review in /w/two = %+v, want only that directory's own run", h)
	}
	if none, err := History("/w/three"); err != nil || len(none) != 0 {
		t.Fatalf("a directory with no runs got %v, %v", none, err)
	}
}

// A worktree-mode review journals its lines on the merge event, not on
// review_end: the review_end publishes before the review's own commit exists.
// History must read the counts where the run actually wrote them, or every
// isolated review looks like it never changed a line.
func TestHistoryCountsAMergedReviewsLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		// An isolated review: review_end without counts, then the merge that
		// carried its work into the main tree.
		{"ev": "review_end", "dir": "/w/iso", "review": "sec-review", "status": "ok"},
		{"ev": "merge", "dir": "/w/iso", "review": "sec-review", "status": "ok",
			"branch": "gauntlet/r/sec-review", "ins": 7, "del": 2},
		// A review whose branch did not merge: no counts, nothing changed.
		{"ev": "review_end", "dir": "/w/iso", "review": "perf-review", "status": "ok"},
		{"ev": "merge", "dir": "/w/iso", "review": "perf-review", "status": "conflict",
			"branch": "gauntlet/r/perf-review"},
		// The loop-step merge into --merge-into names a branch and carries no
		// counts; it is not a review.
		{"ev": "merge", "dir": "/w/iso", "review": "main", "status": "ok"},
	}
	for _, e := range events {
		j.Write(e)
	}
	if err := j.Close(Summary{Dirs: []string{"/w/iso"}}); err != nil {
		t.Fatal(err)
	}

	got, err := History("/w/iso")
	if err != nil {
		t.Fatal(err)
	}
	if h := got["sec-review"]; h.Runs != 1 || h.Changed != 1 {
		t.Fatalf("sec-review in /w/iso = %+v, want one run whose merge landed lines", h)
	}
	if h := got["perf-review"]; h.Runs != 1 || h.Changed != 0 {
		t.Fatalf("perf-review in /w/iso = %+v, want one run that never merged", h)
	}
	if h, ok := got["main"]; ok {
		t.Fatalf("the loop-step merge was counted as a review: %+v", h)
	}
}

// A review that never finished is not a run the suggester should learn from.
// Counting skips, failures, timeouts, and interrupts as "finished without
// changing a line" would demote reviews the operator cancelled or that never
// launched. Older journals with no status still count, as they did.
func TestHistoryIgnoresUnfinishedReviews(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		{"ev": "review_end", "dir": "/w", "review": "ok-review", "status": "ok"},
		{"ev": "review_end", "dir": "/w", "review": "skip-review", "status": "skipped"},
		{"ev": "review_end", "dir": "/w", "review": "fail-review", "status": "fail"},
		{"ev": "review_end", "dir": "/w", "review": "timeout-review", "status": "timeout"},
		{"ev": "review_end", "dir": "/w", "review": "interrupt-review", "status": "interrupted"},
		{"ev": "review_end", "dir": "/w", "review": "legacy-review"},
	}
	for _, e := range events {
		j.Write(e)
	}
	if err := j.Close(Summary{Dirs: []string{"/w"}}); err != nil {
		t.Fatal(err)
	}

	got, err := History("/w")
	if err != nil {
		t.Fatal(err)
	}
	if h := got["ok-review"]; h.Runs != 1 || h.Changed != 0 {
		t.Fatalf("ok-review = %+v, want one finished run", h)
	}
	if h := got["legacy-review"]; h.Runs != 1 || h.Changed != 0 {
		t.Fatalf("legacy-review = %+v, want one finished run (no status)", h)
	}
	for _, name := range []string{"skip-review", "fail-review", "timeout-review", "interrupt-review"} {
		if h, ok := got[name]; ok {
			t.Fatalf("%s was counted as a finished run: %+v", name, h)
		}
	}
}

// The journals are the durable copy. Deleting the derived index must not hide
// a finished run: Recent rebuilds the listing from the event stream.
func TestRecentRebuildsMissingIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	ins, del := 4, 1
	j.Write(map[string]any{
		"ev": "run_start", "ts": now, "dir": "/repo", "version": "test",
		"agents": []string{"claude"},
	})
	j.Write(map[string]any{
		"ev": "review_end", "ts": now.Add(time.Minute), "dir": "/repo",
		"review": "sec-review", "status": "ok", "ins": ins, "del": del,
		"tokens": 12, "loop": 1,
	})
	if err := j.Close(Summary{
		Version: "test", Dirs: []string{"/repo"}, Agents: []string{"claude"},
		Args: []string{"--once"}, Start: now, End: now.Add(time.Minute),
		Loops: 1, Reviews: 1, OK: 1, Ins: ins, Del: del, Tokens: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath()); err != nil {
		t.Fatal(err)
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != id {
		t.Fatalf("deleted index should rebuild from the journal: %+v", runs)
	}
	got := runs[0]
	if got.OK != 1 || got.Reviews != 1 || got.Ins != ins || got.Del != del || got.Tokens != 12 {
		t.Fatalf("rebuilt summary counts wrong: %+v", got)
	}
	if got.Version != "test" || !slices.Equal(got.Dirs, []string{"/repo"}) ||
		!slices.Equal(got.Agents, []string{"claude"}) {
		t.Fatalf("rebuilt summary lost run_start fields: %+v", got)
	}
	if len(got.Args) != 0 {
		t.Fatalf("a reconstructed row has no args: %+v", got)
	}
	if _, err := os.Stat(indexPath()); err != nil {
		t.Fatalf("rebuild should write the index back: %v", err)
	}
}

// Close writes Args onto the index only. Rebuilding a healthy index would
// drop them, so Recent must leave a matching index alone.
func TestRecentKeepsIndexArgsWhenHealthy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]any{"ev": "run_start", "ts": now, "dir": "/repo"})
	if err := j.Close(Summary{
		Dirs: []string{"/repo"}, Args: []string{"--once", "--tui"},
		Start: now, End: now, ExitCode: 1,
	}); err != nil {
		t.Fatal(err)
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != id {
		t.Fatalf("healthy index should list the closed run: %+v", runs)
	}
	if !slices.Equal(runs[0].Args, []string{"--once", "--tui"}) || runs[0].ExitCode != 1 {
		t.Fatalf("a matching index must not be rebuilt: %+v", runs[0])
	}
}

// A process that flushed the journal then died before Close leaves a file
// with no index row. The next listing must pick that run up without
// rewriting the rows Close already wrote.
func TestRecentIndexesAJournalNeverClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	first := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	idA := NewRunID(first)
	jA, err := Open(idA, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := jA.Close(Summary{
		Dirs: []string{"/a"}, Start: first, End: first, Args: []string{"--once"},
	}); err != nil {
		t.Fatal(err)
	}

	second := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	idB := NewRunID(second)
	jB, err := Open(idB, second)
	if err != nil {
		t.Fatal(err)
	}
	jB.Write(map[string]any{
		"ev": "run_start", "ts": second, "dir": "/b", "version": "test",
	})
	jB.Write(map[string]any{
		"ev": "review_end", "ts": second.Add(time.Minute), "dir": "/b",
		"review": "doc-review", "status": "ok", "loop": 1,
	})
	jB.Flush()
	jB.CloseQuiet()

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("the unindexed journal should list: %+v", runs)
	}
	if runs[0].RunID != idB || runs[1].RunID != idA {
		t.Fatalf("want the orphan newest, then the closed run: %+v", runs)
	}
	if runs[0].OK != 1 || len(runs[0].Dirs) != 1 || runs[0].Dirs[0] != "/b" {
		t.Fatalf("orphan summary wrong: %+v", runs[0])
	}
	if !slices.Equal(runs[1].Args, []string{"--once"}) {
		t.Fatalf("appending an orphan must not rebuild the healthy rows: %+v", runs[1])
	}
}

// Recovering an in-flight journal writes a reconstructed row; Close then
// appends the real one. The listing is newest first, so the Close row wins.
func TestRecentPrefersTheCloseRowOverAReconstructedOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]any{"ev": "run_start", "ts": now, "dir": "/repo"})
	j.Flush()
	j.CloseQuiet()

	if _, err := Recent(1); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(Summary{
		Dirs: []string{"/repo"}, Args: []string{"--once"}, Start: now, End: now,
		OK: 3, Reviews: 3, ExitCode: 1,
	}); err != nil {
		t.Fatal(err)
	}
	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("the close row and the reconstructed row are one run: %+v", runs)
	}
	if !slices.Equal(runs[0].Args, []string{"--once"}) || runs[0].OK != 3 || runs[0].ExitCode != 1 {
		t.Fatalf("the close row should win: %+v", runs[0])
	}
}

func TestSummarizeFileTalliesReviewStatuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []map[string]any{
		{"ev": "review_end", "status": "ok"},
		{"ev": "review_end", "status": "fail"},
		{"ev": "review_end", "status": "timeout"},
		{"ev": "review_end", "status": "skipped"},
		{"ev": "review_end", "status": "conflict"},
		{"ev": "review_end", "status": "interrupted"},
		{"ev": "review_end"},
	} {
		j.Write(e)
	}
	j.Flush()
	j.CloseQuiet()

	s, err := summarizeFile(id, j.path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Reviews != 7 || s.OK != 2 || s.Failed != 2 || s.Skipped != 1 || s.Conflicts != 1 {
		t.Fatalf("status tally wrong: %+v", s)
	}
}

// Isolated reviews publish line counts on merge, not review_end. Sequential
// reviews put them on review_end. Summing both would double-count neither
// shape if the other field is absent, and a --merge-into event carries none.
func TestSummarizeFileTakesIsolatedLinesFromMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	ins, del := 7, 2
	j.Write(map[string]any{"ev": "review_end", "dir": "/w", "review": "sec-review", "status": "ok"})
	j.Write(map[string]any{
		"ev": "merge", "dir": "/w", "review": "sec-review", "status": "ok",
		"ins": ins, "del": del,
	})
	j.Write(map[string]any{"ev": "merge", "dir": "/w", "review": "main", "status": "ok"})
	j.Flush()
	j.CloseQuiet()

	s, err := summarizeFile(id, j.path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Reviews != 1 || s.OK != 1 || s.Ins != ins || s.Del != del {
		t.Fatalf("isolated lines should come from merge once: %+v", s)
	}
}

func TestHistorySurvivesADeletedIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	ins, del := 12, 3
	j.Write(map[string]any{
		"ev": "review_end", "dir": "/w/one", "review": "sec-review",
		"status": "ok", "ins": ins, "del": del,
	})
	if err := j.Close(Summary{Dirs: []string{"/w/one"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath()); err != nil {
		t.Fatal(err)
	}

	got, err := History("/w/one")
	if err != nil {
		t.Fatal(err)
	}
	if h := got["sec-review"]; h.Runs != 1 || h.Changed != 1 {
		t.Fatalf("history after a deleted index = %+v", h)
	}
}

// Two processes that flushed then died before Close must both list.
// Recovering only the newest journal would hide the older crash until
// something deleted the index.
func TestRecentIndexesTheWholeUnindexedTail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	first := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	idA := NewRunID(first)
	jA, err := Open(idA, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := jA.Close(Summary{
		Dirs: []string{"/a"}, Start: first, End: first, Args: []string{"--once"},
	}); err != nil {
		t.Fatal(err)
	}

	second := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
	idB := NewRunID(second)
	jB, err := Open(idB, second)
	if err != nil {
		t.Fatal(err)
	}
	jB.Write(map[string]any{
		"ev": "run_start", "ts": second, "dir": "/b", "version": "test",
	})
	jB.Write(map[string]any{
		"ev": "review_end", "ts": second.Add(time.Minute), "dir": "/b",
		"review": "doc-review", "status": "ok", "loop": 1,
	})
	jB.Flush()
	jB.CloseQuiet()

	third := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	idC := NewRunID(third)
	jC, err := Open(idC, third)
	if err != nil {
		t.Fatal(err)
	}
	jC.Write(map[string]any{
		"ev": "run_start", "ts": third, "dir": "/c", "version": "test",
	})
	jC.Write(map[string]any{
		"ev": "review_end", "ts": third.Add(time.Minute), "dir": "/c",
		"review": "sec-review", "status": "ok", "loop": 1,
	})
	jC.Flush()
	jC.CloseQuiet()

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("both unindexed journals should list: %+v", runs)
	}
	if runs[0].RunID != idC || runs[1].RunID != idB || runs[2].RunID != idA {
		t.Fatalf("want newest crash, older crash, then the closed run: %+v", runs)
	}
	if runs[0].OK != 1 || len(runs[0].Dirs) != 1 || runs[0].Dirs[0] != "/c" {
		t.Fatalf("newest orphan summary wrong: %+v", runs[0])
	}
	if runs[1].OK != 1 || len(runs[1].Dirs) != 1 || runs[1].Dirs[0] != "/b" {
		t.Fatalf("older orphan summary wrong: %+v", runs[1])
	}
	if !slices.Equal(runs[2].Args, []string{"--once"}) {
		t.Fatalf("appending the tail must not rebuild the healthy rows: %+v", runs[2])
	}
}

// A Close that cannot append the index row must not mark the run indexed:
// the next Close is the retry, and without it the run stays off the listing
// until something else recovers the journal.
func TestCloseRetriesAFailedIndexWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]any{"ev": "run_start", "ts": now, "dir": "/repo"})
	j.Flush()

	if err := os.Mkdir(indexPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(Summary{
		Dirs: []string{"/repo"}, Args: []string{"--once"}, Start: now, End: now,
		OK: 1, Reviews: 1, ExitCode: 1,
	}); err == nil {
		t.Fatal("index write should fail while the path is a directory")
	}
	if err := os.Remove(indexPath()); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(Summary{
		Dirs: []string{"/repo"}, Args: []string{"--once"}, Start: now, End: now,
		OK: 1, Reviews: 1, ExitCode: 1,
	}); err != nil {
		t.Fatalf("retry should index the run: %v", err)
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != id {
		t.Fatalf("retried close should list the run: %+v", runs)
	}
	if !slices.Equal(runs[0].Args, []string{"--once"}) || runs[0].ExitCode != 1 {
		t.Fatalf("retried close should keep the close row: %+v", runs[0])
	}
}

// A rebuild of a missing index and a Close racing each other must not let
// the rename overwrite the Close row. Args and ExitCode live only there.
func TestIndexRebuildDoesNotDropAConcurrentClose(t *testing.T) {
	for range 30 {
		home := t.TempDir()
		t.Setenv("GAUNTLET_HOME", home)

		first := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
		idA := NewRunID(first)
		jA, err := Open(idA, first)
		if err != nil {
			t.Fatal(err)
		}
		if err := jA.Close(Summary{
			Dirs: []string{"/a"}, Start: first, End: first, Args: []string{"--once"},
			ExitCode: 0,
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(indexPath()); err != nil {
			t.Fatal(err)
		}

		second := time.Date(2026, 8, 25, 14, 0, 0, 0, time.UTC)
		idB := NewRunID(second)
		jB, err := Open(idB, second)
		if err != nil {
			t.Fatal(err)
		}
		jB.Write(map[string]any{"ev": "run_start", "ts": second, "dir": "/b"})
		jB.Flush()

		var wg sync.WaitGroup
		wg.Go(func() { _, _ = Recent(10) })
		wg.Go(func() {
			_ = jB.Close(Summary{
				Dirs: []string{"/b"}, Start: second, End: second,
				Args: []string{"--jobs", "2"}, ExitCode: 2,
			})
		})
		wg.Wait()

		runs, err := Recent(10)
		if err != nil {
			t.Fatal(err)
		}
		var b Summary
		found := false
		for _, r := range runs {
			if r.RunID == idB {
				b = r
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("closed run missing after rebuild race: %+v", runs)
		}
		if !slices.Equal(b.Args, []string{"--jobs", "2"}) || b.ExitCode != 2 {
			t.Fatalf("Close row lost to rebuild: %+v", b)
		}
	}
}

func TestRebuildIndexSkipsACorruptJournal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	now := time.Date(2026, 8, 25, 13, 15, 0, 0, time.UTC)
	id := NewRunID(now)
	j, err := Open(id, now)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]any{"ev": "review_end", "dir": "/repo", "status": "ok"})
	if err := j.Close(Summary{Dirs: []string{"/repo"}, Start: now, End: now}); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(home, "runs", "2026-08-25", "20260825T120000Z-0001.jsonl")
	if err := os.WriteFile(bad, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath()); err != nil {
		t.Fatal(err)
	}

	runs, err := Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("corrupt journal still lists as a row, good one must remain: %+v", runs)
	}
	var found bool
	for _, r := range runs {
		if r.RunID == id && r.OK == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the readable journal vanished next to a corrupt one: %+v", runs)
	}
}
