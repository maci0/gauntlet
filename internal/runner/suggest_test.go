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
