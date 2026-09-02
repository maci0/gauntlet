// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// seedConfig builds a runner config whose stochastic choices are fully
// observable: schedule order and agent picks, with no agent ever launched.
func seedConfig(t *testing.T, reviews []string, agents []agent.Spec, seed uint64) Config {
	t.Helper()
	set, _ := promptSet(t, reviews...)
	return Config{
		Dir: t.TempDir(), Set: set, Reviews: reviews,
		Agents: agents, Timeout: time.Second, Jobs: 1,
		RunID: "test", Version: "test", Seed: seed,
	}
}

// TestSeedReplaysScheduleAndPicks pins what the journal's recorded seed is
// good for: two runners with one seed shuffle reviews and sample agents
// identically; a different seed orders them differently.
func TestSeedReplaysScheduleAndPicks(t *testing.T) {
	reviews := []string{
		"aa-review", "ab-review", "ac-review", "ad-review",
		"ae-review", "af-review", "ag-review", "ah-review", "ai-review",
	}
	agents := []agent.Spec{{Tool: "claude"}, {Tool: "codex"}, {Tool: "gemini"}}

	schedule := func(seed uint64) []string {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t, reviews, agents, seed), NewBus())
		if err != nil {
			t.Fatal(err)
		}
		return r.schedule(1)
	}
	picks := func(seed uint64, n int) []agent.Spec {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t, reviews, agents, seed), NewBus())
		if err != nil {
			t.Fatal(err)
		}
		out := make([]agent.Spec, n)
		for i := range out {
			out[i] = r.pickAgent(reviews[i%len(reviews)], nil)
		}
		return out
	}

	if a, b := schedule(42), schedule(42); !slices.Equal(a, b) {
		t.Fatalf("same seed produced different schedules:\n%v\n%v", a, b)
	}
	if a, b := picks(42, 12), picks(42, 12); !slices.Equal(a, b) {
		t.Fatalf("same seed produced different agent picks:\n%v\n%v", a, b)
	}
	if slices.Equal(schedule(42), schedule(43)) {
		t.Fatal("different seeds produced identical schedules")
	}
	if slices.Equal(picks(42, 12), picks(43, 12)) {
		t.Fatal("different seeds produced identical agent picks")
	}
}

// TestPicksDoNotDependOnDrawOrder pins the property that lets a --jobs > 1
// run replay: sampling review B must not disturb what review A samples, no
// matter how the lanes interleave. A shared random stream would fail this on
// the first swapped call, because the OS scheduler would own the draw order.
func TestPicksDoNotDependOnDrawOrder(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review"}
	agents := []agent.Spec{{Tool: "claude"}, {Tool: "codex"}, {Tool: "gemini"}}

	build := func() *Runner {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t, reviews, agents, 7), NewBus())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r := build()
	aFirst := r.pickAgent(reviews[0], nil)
	bAfterA := r.pickAgent(reviews[1], nil)

	r = build()
	bFirst := r.pickAgent(reviews[1], nil)
	aAfterB := r.pickAgent(reviews[0], nil)

	if aFirst != aAfterB || bFirst != bAfterA {
		t.Fatalf("pick order changed the picks: a %v then b %v, vs b %v then a %v",
			aFirst.Tool, bAfterA.Tool, bFirst.Tool, aAfterB.Tool)
	}

	// An agent excluded for a failed attempt must stay excluded even though
	// the draw key is unchanged.
	exclude := map[agent.Spec]bool{aFirst: true}
	for range 20 {
		if spec := r.pickAgent(reviews[0], exclude); spec == aFirst {
			t.Fatalf("excluded %s was sampled again", aFirst.Tool)
		}
	}
}

// TestZeroSeedDerivesFromClock checks the production default: no configured
// seed still resolves to a nonzero effective seed, which the run-start event
// then records.
func TestZeroSeedDerivesFromClock(t *testing.T) {
	r, err := New(context.Background(), seedConfig(t,
		[]string{"aa-review"}, []agent.Spec{{Tool: "claude"}}, 0), NewBus())
	if err != nil {
		t.Fatal(err)
	}
	if r.seed == 0 {
		t.Fatal("clock-derived seed is zero")
	}
}

// TestZeroSeedUsesInjectedClock pins the clock seam for an unset seed: two
// runners that share one frozen Now derive the same seed, and Unix epoch
// (UnixNano 0) still yields a nonzero seed so 0 keeps meaning "unset".
func TestZeroSeedUsesInjectedClock(t *testing.T) {
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 123, time.UTC)
	bus := NewBus()
	bus.Now = func() time.Time { return stamp }

	build := func() *Runner {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t,
			[]string{"aa-review"}, []agent.Spec{{Tool: "claude"}}, 0), bus)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := build(), build()
	want := uint64(stamp.UnixNano())
	if a.seed != want || b.seed != want {
		t.Fatalf("injected clock seed: %d and %d, want %d", a.seed, b.seed, want)
	}

	epoch := NewBus()
	epoch.Now = func() time.Time { return time.Unix(0, 0).UTC() }
	r, err := New(context.Background(), seedConfig(t,
		[]string{"aa-review"}, []agent.Spec{{Tool: "claude"}}, 0), epoch)
	if err != nil {
		t.Fatal(err)
	}
	if r.seed == 0 {
		t.Fatal("epoch clock derived seed 0, which means unset")
	}
}

// TestElapsedUsesInjectedClock pins that review and loop elapsed, event
// timestamps, and Stats.Start all read Bus.Now, so a frozen clock makes a
// real agent launch report zero elapsed instead of wall time.
func TestElapsedUsesInjectedClock(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)
	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Seed = 1
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	bus := NewBus()
	bus.Now = func() time.Time { return stamp }
	events := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(events, done)
	r := runOn(t, cfg, bus)

	if !r.Stats().Start.Equal(stamp) {
		t.Fatalf("Stats.Start %v, want injected %v", r.Stats().Start, stamp)
	}
	results := r.Stats().Results()
	if len(results) != 1 {
		t.Fatalf("results: %+v", results)
	}
	if results[0].Elapsed != 0 {
		t.Fatalf("result elapsed %s, want 0 under a frozen clock", results[0].Elapsed)
	}
	for _, ev := range <-done {
		if !ev.Time.Equal(stamp) {
			t.Fatalf("%s timestamp %v, want %v", ev.Kind, ev.Time, stamp)
		}
		if (ev.Kind == EvReviewEnd || ev.Kind == EvLoopEnd) && ev.Elapsed != 0 {
			t.Fatalf("%s elapsed %v, want 0 under a frozen clock", ev.Kind, ev.Elapsed)
		}
	}
}

// TestBudgetExhaustedUsesInjectedClock pins that --runtime compares against
// Bus.Now, not wall time: a clock already past the budget must not start a
// review, even though the process has only just launched.
func TestBudgetExhaustedUsesInjectedClock(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)
	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Seed = 1
	// Far future so a wall-clock time.Since(Started) is negative and would
	// not exhaust the budget: this test only passes if the check reads Now.
	stamp := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg.Started = stamp
	cfg.Runtime = time.Second

	bus := NewBus()
	bus.Now = func() time.Time { return stamp.Add(2 * time.Second) }
	events := bus.Subscribe(256)
	done := make(chan []Event, 1)
	go collect(events, done)
	r := runOn(t, cfg, bus)

	if got := r.Stats().Results(); len(got) != 0 {
		t.Fatalf("budget should prevent reviews, got %+v", got)
	}
	if countKind(<-done, EvReviewStart) != 0 {
		t.Fatal("a review started after the injected clock had spent the budget")
	}
}

// TestScheduleReplaysAcrossAReload pins the property a hot reload needs: a
// successor built from the recorded seed schedules every later loop exactly as
// the uninterrupted run would have. This only holds because each loop's order
// is a keyed draw rather than the next bite of a random stream; a stream would
// restart from its beginning in the successor and diverge from the lineage it
// took over.
func TestScheduleReplaysAcrossAReload(t *testing.T) {
	reviews := []string{
		"aa-review", "ab-review", "ac-review", "ad-review", "ae-review", "af-review",
	}
	agents := []agent.Spec{{Tool: "claude"}, {Tool: "codex"}, {Tool: "gemini"}}

	// The uninterrupted run: three whole loops in one process.
	r, err := New(context.Background(), seedConfig(t, reviews, agents, 7), NewBus())
	if err != nil {
		t.Fatal(err)
	}
	lineage := [][]string{r.schedule(1), r.schedule(2), r.schedule(3)}

	// The successor takes over mid-loop: loop 1 finishes from the handed-over
	// queue, then its loops 2 and 3 must match the lineage's, not restart the
	// sequence.
	cfg := seedConfig(t, reviews, agents, 7)
	cfg.ResumeQueue = []string{"ad-review"}
	succ, err := New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}
	if got := succ.schedule(1); !slices.Equal(got, []string{"ad-review"}) {
		t.Fatalf("resume queue was reordered: %v", got)
	}
	for _, l := range []int{2, 3} {
		if got := succ.schedule(l); !slices.Equal(got, lineage[l-1]) {
			t.Fatalf("loop %d: lineage %v, successor %v", l, lineage[l-1], got)
		}
	}
}

// TestRunStartCarriesTheSeed pins where replayability lives: the run-start
// event records the effective seed, so a journal describes its own rerun.
func TestRunStartCarriesTheSeed(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo ok`)

	bus := NewBus()
	events := bus.Subscribe(64)
	done := make(chan []Event, 1)
	go collect(events, done)

	cfg := baseConfig(t, repo, set, []string{"sec-review"}, bin)
	cfg.Seed = 99
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	for _, ev := range <-done {
		if ev.Kind != EvRunStart {
			continue
		}
		if ev.Seed != 99 {
			t.Fatalf("run start recorded seed %d, want 99", ev.Seed)
		}
		return
	}
	t.Fatal("no run_start event published")
}
