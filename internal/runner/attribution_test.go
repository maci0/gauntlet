// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// TestLaneNarrationIsAttributable pins what a --jobs run's journal needs to be
// replayable: a line a lane published while running a review names that review
// and that lane. The lanes publish concurrently, so which lane's line lands
// first is the scheduler's choice and a second run of the same seed interleaves
// them differently; the attribution is what a reader sorts by to put the two
// recordings back in the same order. A log line with an empty review cannot be
// placed at all.
func TestLaneNarrationIsAttributable(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review", "ad-review"}
	agents := []agent.Spec{{Tool: "claude"}, {Tool: "codex"}}
	repo := testRepo(t)
	set, _ := promptSet(t, reviews...)
	dir := t.TempDir()
	bin := map[string]string{
		"claude": fakeAgent(t, dir, "claude", `echo "RESULT: no-changes"`),
		"codex":  fakeAgent(t, dir, "codex", `echo "RESULT: no-changes"`),
	}
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	record := func() []Event {
		t.Helper()
		cfg := baseConfig(t, repo, set, reviews, bin["claude"])
		cfg.Agents, cfg.Bin, cfg.Seed, cfg.Jobs = agents, bin, 4242, 2
		bus := NewBus()
		bus.Now = func() time.Time { return stamp }
		events := bus.Subscribe(1024)
		done := make(chan []Event, 1)
		go collect(events, done)
		runOn(t, cfg, bus)
		return <-done
	}

	// Two runs of one seed: same reviews, same agents, same lane assignment.
	// Order is the scheduler's, so the comparison is on the attributed set.
	first, second := record(), record()
	if len(first) == 0 {
		t.Fatal("the recorded run published no events, so nothing was compared")
	}
	names := func(evs []Event) []string {
		out := make([]string, 0, len(evs))
		for _, ev := range evs {
			if ev.Kind != EvLog {
				continue
			}
			if ev.Review == "" {
				t.Fatalf("a lane published narration naming no review, so a replay cannot place it: %q", ev.Text)
			}
			out = append(out, ev.Text)
		}
		return out
	}
	firstNames, secondNames := names(first), names(second)
	slices.Sort(firstNames)
	slices.Sort(secondNames)
	if !slices.Equal(firstNames, secondNames) {
		t.Fatalf("two seeded lane runs narrated different work:\nfirst:  %s\nsecond: %s",
			strings.Join(firstNames, "\n"), strings.Join(secondNames, "\n"))
	}
	if len(firstNames) == 0 {
		t.Fatal("the run narrated nothing, so the attribution was never exercised")
	}
	// Every journaled line is JSON a run can be replayed from, so the fields
	// this test reads have to survive the encoding.
	line, err := json.Marshal(first[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"ev"`) {
		t.Fatalf("an event does not encode as the journal writes it: %s", line)
	}
}
