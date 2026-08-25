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
