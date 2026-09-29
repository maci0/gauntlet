// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"
	"testing"
	"time"
)

// The token budget is read between reviews, so a single launch that keeps
// extending itself is the one spend no ceiling catches: the provider is billed
// for every turn until the timeout kills it. A review that reaches the whole
// budget on its own is stopped, and nothing starts after it.
func TestReviewCapStopsARunawayLaunch(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	// An envelope counter past the budget, then a run of turns that would
	// otherwise keep the launch alive for the whole timeout.
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo '{"usage":{"output_tokens":900}}'
i=0
while [ $i -lt 300 ]; do
  echo '{"usage":{"output_tokens":900}}'
  i=$((i+1))
done`)

	cfg := baseConfig(t, dir, set, []string{"a-review", "b-review"}, bin)
	cfg.Stream, cfg.TokenBudget, cfg.Timeout = true, 800, 30*time.Second

	start := time.Now()
	r, events := runRecorded(t, cfg)
	elapsed := time.Since(start)

	if n := countKind(events, EvReviewStart); n != 1 {
		t.Fatalf("started %d reviews, want 1: the budget stops what starts next", n)
	}
	if c := r.Stats().Counts(); c.Failures() != 1 {
		t.Fatalf("counts: %+v, want one failure", c)
	}
	if !sawLog(events, "OVER BUDGET") {
		t.Fatal("the run did not say the review went over budget")
	}
	if elapsed > 20*time.Second {
		t.Fatalf("the runaway ran for %s, want it stopped well inside its timeout", elapsed)
	}
}

// The cap stops work, so only the provider's own figure may stop it. A model
// that prints a usage-shaped line, whether from a fixture it read or from its
// own invention, is prose like any other and cannot end the review it is in.
// The schedule still stops on what the run bills, which is the looser figure
// the run has always charged: the review that read it finishes, the next one
// does not start.
func TestReviewCapIgnoresAFigureTheModelPrinted(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo "Total tokens: 900000"
echo "RESULT: changed=0"`)

	cfg := baseConfig(t, dir, set, []string{"a-review", "b-review"}, bin)
	cfg.Stream, cfg.TokenBudget = true, 1000

	r, events := runRecorded(t, cfg)
	if sawLog(events, "OVER BUDGET") {
		t.Fatal("a figure the model printed stopped the review it was printed in")
	}
	if c := r.Stats().Counts(); c.OK != 1 {
		t.Fatalf("counts: %+v, want the one review that started to finish", c)
	}
	if n := countKind(events, EvReviewEnd); n != 1 {
		t.Fatalf("ended %d reviews, want 1", n)
	}
}

// No budget is no cap: the same runaway runs to its own end, which is what
// every run before the cap did.
func TestReviewCapIsOffWithoutABudget(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo '{"usage":{"output_tokens":900}}'`)

	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	cfg.Stream, cfg.TokenBudget = true, 0

	r, events := runRecorded(t, cfg)
	if sawLog(events, "OVER BUDGET") {
		t.Fatal("a run with no token budget stopped a review anyway")
	}
	if c := r.Stats().Counts(); c.OK != 1 {
		t.Fatalf("counts: %+v, want 1 ok", c)
	}
}

// The cap ends the review and the run takes the number with it: the tokens
// spent before the stop are counted, so the budget is reported as spent
// rather than as untouched.
func TestReviewCapCountsWhatItSpent(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
echo '{"usage":{"output_tokens":4000}}'
i=0
while [ $i -lt 300 ]; do
  echo '{"usage":{"output_tokens":4000}}'
  i=$((i+1))
done`)

	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	cfg.Stream, cfg.TokenBudget = true, 1000

	r, _ := runRecorded(t, cfg)
	if got := r.Stats().Tokens(); got < 1000 {
		t.Fatalf("run tally = %d tokens, want the stopped review's spend counted", got)
	}
}

// sawLog reports whether a log line matching sub was published.
func sawLog(events []Event, sub string) bool {
	for _, ev := range events {
		if ev.Kind == EvLog && strings.Contains(ev.Text, sub) {
			return true
		}
	}
	return false
}
