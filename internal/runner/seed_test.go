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
		return r.schedule()
	}
	picks := func(seed uint64, n int) []agent.Spec {
		t.Helper()
		r, err := New(context.Background(), seedConfig(t, reviews, agents, seed), NewBus())
		if err != nil {
			t.Fatal(err)
		}
		out := make([]agent.Spec, n)
		for i := range out {
			out[i] = r.pickAgent(nil)
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
