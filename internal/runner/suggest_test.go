// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// TestSuggestTriesTheNextAgentAfterAFailure pins the triage contract Suggest's
// doc comment states: a nonzero exit, and an exit of 0 with unusable output,
// are both failures, and the next sampled agent is tried rather than giving
// up. Which agent is asked first is a seeded shuffle, so the seed search makes
// the fall-through deterministic instead of leaving it to luck.
func TestSuggestTriesTheNextAgentAfterAFailure(t *testing.T) {
	set, _ := promptSet(t, "sec-review")
	binDir := t.TempDir()
	bin := map[string]string{
		"claude": fakeAgent(t, binDir, "claude", `echo boom >&2; exit 7`),
		"codex":  fakeAgent(t, binDir, "codex", `echo "I would run everything"`),
		"kimi":   fakeAgent(t, binDir, "kimi", `echo "RELEVANT: sec-review: handles secrets"`),
	}

	var logs strings.Builder
	cfg := SuggestConfig{
		Dir:     t.TempDir(),
		Set:     set,
		Pool:    []string{"sec-review"},
		Agents:  []agent.Spec{{Tool: "claude"}, {Tool: "codex"}, {Tool: "kimi"}},
		Bin:     bin,
		Timeout: 30 * time.Second,
		Log:     func(f string, a ...any) { fmt.Fprintf(&logs, f, a...); logs.WriteByte('\n') },
	}

	sawFallback := false
	for seed := uint64(1); seed < 40 && !sawFallback; seed++ {
		logs.Reset()
		cfg.Seed = seed
		picked, spec, err := Suggest(context.Background(), cfg)
		if err != nil {
			t.Fatalf("seed %d: the usable agent must end the triage with picks: %v\n%s",
				seed, err, logs.String())
		}
		if len(picked) != 1 || picked[0].Name != "sec-review" {
			t.Fatalf("seed %d: picks %+v, want sec-review", seed, picked)
		}
		if spec.Tool != "kimi" {
			t.Fatalf("seed %d: answered by %s, want kimi", seed, spec.Tool)
		}
		if logs.String() != "" && strings.Contains(logs.String(), "claude failed") &&
			strings.Contains(logs.String(), "codex printed no usable") {
			sawFallback = true
		}
	}
	if !sawFallback {
		t.Fatal("no seed in 40 asked a failing or silent agent before kimi; fall-through untested")
	}
}

// TestSuggestZeroSeedDerivesFromTheInjectedClock pins the one nondeterminism
// the triage step had left: a caller with no run seed used to read the wall
// clock here, beside every other clock in the run. Two calls under one
// injected Now must ask the agents in the same order.
func TestSuggestZeroSeedDerivesFromTheInjectedClock(t *testing.T) {
	set, _ := promptSet(t, "sec-review")
	binDir := t.TempDir()
	agents := []agent.Spec{{Tool: "claude"}, {Tool: "codex"}, {Tool: "kimi"}}
	bin := map[string]string{
		"claude": fakeAgent(t, binDir, "claude", `echo boom >&2; exit 7`),
		"codex":  fakeAgent(t, binDir, "codex", `echo "RELEVANT: sec-review: handles secrets"`),
		"kimi":   fakeAgent(t, binDir, "kimi", `echo "RELEVANT: sec-review: handles secrets"`),
	}
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	firstAsked := func() string {
		t.Helper()
		var asked strings.Builder
		cfg := SuggestConfig{
			Dir: t.TempDir(), Set: set, Pool: []string{"sec-review"},
			Agents: agents, Bin: bin, Timeout: 30 * time.Second,
			Now: func() time.Time { return stamp },
			Log: func(f string, a ...any) { fmt.Fprintf(&asked, f, a...) },
		}
		if _, _, err := Suggest(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		// The first "Asking" line names the agent the shuffle put first.
		line := asked.String()
		_, after, ok := strings.Cut(line, "Asking ")
		if !ok {
			t.Fatalf("no agent was asked:\n%s", line)
		}
		rest := after
		return rest[:strings.Index(rest, " ")]
	}
	if a, b := firstAsked(), firstAsked(); a != b {
		t.Fatalf("two calls under one clock asked %q then %q", a, b)
	}
}

// TestSuggestRefusesAnEmptyPoolBeforeLaunchingAnything pins the guard that
// keeps a fully filtered catalog from becoming a launch: with nothing left to
// pick, triage must fail before any agent is asked.
func TestSuggestRefusesAnEmptyPoolBeforeLaunchingAnything(t *testing.T) {
	set, _ := promptSet(t, "sec-review")
	var launches int
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RELEVANT: sec-review: x"`)
	cfg := SuggestConfig{
		Dir: t.TempDir(), Set: set,
		Pool:    nil,
		Agents:  []agent.Spec{{Tool: "claude"}},
		Bin:     map[string]string{"claude": bin},
		Timeout: 30 * time.Second,
		Log: func(f string, a ...any) {
			launches++
			t.Logf(f, a...)
		},
	}
	if _, _, err := Suggest(context.Background(), cfg); err == nil {
		t.Fatal("an empty pool must be refused")
	}
	if launches != 0 {
		t.Fatal("an empty pool reached an agent launch")
	}
}

func TestSuggestRejectsMalformedNames(t *testing.T) {
	set, _ := promptSet(t, "sec-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RELEVANT: sec-review.json: malformed name"`)
	picked, _, err := Suggest(t.Context(), SuggestConfig{
		Dir:     t.TempDir(),
		Set:     set,
		Pool:    []string{"sec-review"},
		Only:    &agent.Spec{Tool: "claude"},
		Bin:     map[string]string{"claude": bin},
		Timeout: 5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "no usable 'RELEVANT:' lines") {
		t.Fatalf("malformed output did not fail triage: picks %+v, error %v", picked, err)
	}
	if len(picked) != 0 {
		t.Fatalf("malformed output scheduled reviews: %+v", picked)
	}
}
