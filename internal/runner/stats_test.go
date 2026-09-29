// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

func TestSeedPrependsCarriedOverResults(t *testing.T) {
	// A hot reload continues one run: the successor's report must read in run
	// order, with the previous process's results first, not its own.
	st := &Stats{Start: time.Now()}
	st.Add(Result{Review: "b-review", Agent: agent.Spec{Tool: "codex"}, Status: StatusOK})
	st.Seed([]Result{
		{Review: "a-review", Agent: agent.Spec{Tool: "claude"}, Status: StatusOK},
	}, 1, 1)

	got := st.Results()
	if len(got) != 2 || got[0].Review != "a-review" || got[1].Review != "b-review" {
		t.Fatalf("seeded results must come first: %+v", got)
	}
	if st.CommitRuns() != 1 || st.CommitFails() != 1 {
		t.Fatalf("carried-over counters lost: runs=%d fails=%d",
			st.CommitRuns(), st.CommitFails())
	}
}

func TestDetailCapKeepsTheMostRecentAndSeedPrependsToThem(t *testing.T) {
	// Results sorts by review name, so the ring's own order is read through the
	// accessor that keeps it.
	detail := func(s *Stats) []Result {
		s.mu.Lock()
		defer s.mu.Unlock()
		return append([]Result(nil), s.detail()...)
	}

	st := &Stats{}
	for i := range MaxDetailResults + 50 {
		st.Add(Result{Review: fmt.Sprintf("review-%d", i), Status: StatusOK})
	}
	got := detail(st)
	if len(got) != MaxDetailResults {
		t.Fatalf("detail beyond the cap: %d rows", len(got))
	}
	if first := got[0].Review; first != "review-50" {
		t.Fatalf("the cap must drop the oldest rows: first=%s", first)
	}
	if dropped := st.DetailDropped(); dropped != 50 {
		t.Fatalf("dropped count: %d", dropped)
	}

	// A seed lands in front of the detail this run already holds, and what the
	// ring had dropped stays dropped: a dropped row listed and counted twice.
	// The cap keeps the most recent end of the run, so a carried-over result
	// first goes when the ring is already full.
	st.Seed([]Result{{Review: "carried", Status: StatusOK}}, 0, 0)
	got = detail(st)
	if len(got) != MaxDetailResults {
		t.Fatalf("seeded detail beyond the cap: %d rows", len(got))
	}
	if first, last := got[0].Review, got[len(got)-1].Review; first != "review-50" || last != "review-2049" {
		t.Fatalf("seeded detail must be the run's own most recent rows: first=%s last=%s", first, last)
	}
	if dropped := st.DetailDropped(); dropped != 51 {
		t.Fatalf("dropped count after the seed: %d", dropped)
	}
}

func TestCountsTotalsAndFailures(t *testing.T) {
	st := &Stats{}
	st.Add(Result{Review: "ok", Status: StatusOK, Ins: 5, Del: 2, HaveLines: true,
		Tokens: 100, Elapsed: 2 * time.Second, Agent: agent.Spec{Tool: "claude", Model: "opus"}})
	st.Add(Result{Review: "broken", Status: StatusFail, Agent: agent.Spec{Tool: "codex"}})
	st.Add(Result{Review: "slow", Status: StatusTimeout, Ins: 9, HaveLines: false,
		Elapsed: time.Second, Agent: agent.Spec{Tool: "claude", Model: "opus"}})
	st.Add(Result{Review: "clash", Status: StatusConflict, Agent: agent.Spec{Tool: "codex"}})
	st.Add(Result{Review: "ghost", Status: StatusSkipped, ExitCode: -1,
		Agent: agent.Spec{Tool: "codex"}})
	st.Add(Result{Review: "halted", Status: StatusInterrupted,
		Agent: agent.Spec{Tool: "claude"}})

	c := st.Counts()
	if c.Total() != 6 {
		t.Fatalf("total: %+v", c)
	}
	if c.Interrupted != 1 {
		t.Fatalf("interrupted: %+v", c)
	}
	// A failure, a timeout, a conflict, and a skip fail the run, matching
	// exit code 1's documented meaning; an interruption does not, because it
	// says nothing about the review's outcome.
	if c.Failures() != 4 {
		t.Fatalf("failures: %+v", c)
	}
	failed := st.Failures()
	if len(failed) != 4 ||
		failed[0].Review != "broken" || failed[1].Review != "clash" ||
		failed[2].Review != "ghost" || failed[3].Review != "slow" {
		t.Fatalf("failure list wrong: %+v", failed)
	}
	for _, res := range failed {
		if res.Review == "halted" || res.Status == StatusInterrupted {
			t.Fatalf("an interruption must not appear in the failure list: %+v", failed)
		}
	}

	ins, del, tokens, agentTime, timed, haveLines := st.Totals()
	if ins != 5 || del != 2 {
		t.Errorf("lines without HaveLines must not be summed: +%d/-%d", ins, del)
	}
	if tokens != 100 {
		t.Errorf("tokens: %d", tokens)
	}
	if agentTime != 3*time.Second || timed != 2 {
		t.Errorf("agent time: %s over %d reviews", agentTime, timed)
	}
	if !haveLines {
		t.Error("haveLines should be set when any result measured lines")
	}
	// Empty stats should report zero totals and false haveLines.
	empty := &Stats{}
	if empty.Counts().Total() != 0 {
		t.Fatalf("empty stats total = %d, want 0", empty.Counts().Total())
	}
	if len(empty.Failures()) != 0 {
		t.Fatalf("empty stats has failures: %+v", empty.Failures())
	}
	eIns, eDel, eTokens, eTime, eTimed, eHaveLines := empty.Totals()
	if eIns != 0 || eDel != 0 || eTokens != 0 || eTime != 0 || eTimed != 0 || eHaveLines {
		t.Fatalf("empty Totals returned non-zero values: ins=%d del=%d tok=%d time=%v timed=%d haveLines=%v",
			eIns, eDel, eTokens, eTime, eTimed, eHaveLines)
	}

	// Results with empty status (publication metadata recovered without agent)
	// must be ignored by Counts and Failures.
	st.Add(Result{Review: "metadata-only", Status: ""})
	if st.Counts().Total() != 6 {
		t.Fatalf("empty status must not increment total counts: got %d, want 6", st.Counts().Total())
	}
	if len(st.Failures()) != 4 {
		t.Fatalf("empty status must not appear in failures: %+v", st.Failures())
	}

	// When no results have lines, haveLines must report false.
	noLines := &Stats{}
	noLines.Add(Result{Review: "x", Status: StatusOK, Ins: 10, Del: 5, HaveLines: false})
	_, _, _, _, _, nlHaveLines := noLines.Totals()
	if nlHaveLines {
		t.Error("haveLines must be false when no result measured lines")
	}
}

func TestCountsAdd(t *testing.T) {
	a := Counts{OK: 1, Fail: 2, Timeout: 3, Skipped: 4, Interrupted: 5, Conflict: 6}
	b := Counts{OK: 10, Fail: 20, Timeout: 30, Skipped: 40, Interrupted: 50, Conflict: 60}
	a.Add(b)
	want := Counts{OK: 11, Fail: 22, Timeout: 33, Skipped: 44, Interrupted: 55, Conflict: 66}
	if a != want {
		t.Fatalf("Counts.Add got %+v, want %+v", a, want)
	}
	if a.Total() != 231 {
		t.Fatalf("Total = %d, want 231", a.Total())
	}
	if a.Failures() != 165 {
		t.Fatalf("Failures = %d, want 165", a.Failures())
	}
}

// A status this build does not name reaches it through a reload handoff
// written by a newer binary. It has to land in a bucket: the journal row the
// run closes must add up (journal.Summary counts Reviews against its own
// buckets), and the reporter's total has to match the dashboard's, which
// tallies by raw status string and never loses one.
func TestCountsTallyUnrecognizedStatus(t *testing.T) {
	st := &Stats{}
	st.Add(Result{Review: "future", Status: Status("deferred")})
	st.Seed([]Result{{Review: "seeded-future", Status: Status("deferred")}}, 0, 0)
	c := st.Counts()
	if c.Other != 2 {
		t.Fatalf("Other = %d, want 2: %+v", c.Other, c)
	}
	if c.Total() != 2 {
		t.Fatalf("Total = %d, want 2: %+v", c.Total(), c)
	}
	// Not a failure: this build cannot tell what the newer one meant by it.
	if c.Failures() != 0 || len(st.Failures()) != 0 {
		t.Fatalf("an unrecognized status must not fail the run: %+v", c)
	}
	var attributed int
	for _, a := range st.ByAgent() {
		attributed += a.Counts.Other
	}
	if attributed != 2 {
		t.Fatalf("per-agent breakdown attributes %d unrecognized results, want 2", attributed)
	}
}

func TestByAgentGroupsAndSorts(t *testing.T) {
	st := &Stats{}
	st.Add(Result{Status: StatusOK, Tokens: 50, Elapsed: 10 * time.Second,
		Agent: agent.Spec{Tool: "codex", Model: "gpt-5"}})
	st.Add(Result{Status: StatusOK, Tokens: 70, Elapsed: 30 * time.Second,
		Agent: agent.Spec{Tool: "codex", Model: "gpt-5"}})
	st.Add(Result{Status: StatusFail, Elapsed: time.Second, Agent: agent.Spec{Tool: "claude"}})

	got := st.ByAgent()
	if len(got) != 2 || got[0].Label != "claude" || got[1].Label != "codex:gpt-5" {
		t.Fatalf("agents out of order: %+v", got)
	}
	if got[1].Counts.OK != 2 || got[1].Tokens != 120 || got[1].Elapsed != 40*time.Second {
		t.Fatalf("codex summary wrong: %+v", got[1])
	}
	if got[0].Counts.Fail != 1 {
		t.Fatalf("claude summary wrong: %+v", got[0])
	}
}

// TestListsOrderNonASCIINamesByCollation pins that the run's own lists are
// read the way every other list of names in the tool is. A review name and an
// agent name are the reviewed tree's and the operator's own, so they carry
// accents and scripts: ordered by byte value both landed after every ASCII
// name whatever letter they started with, and the summary read as if the
// non-ASCII runs were the tail of the run rather than rows in it.
func TestListsOrderNonASCIINamesByCollation(t *testing.T) {
	st := &Stats{}
	for _, name := range []string{"Zebra-review", "Ähre-review", "日本語-review"} {
		st.Add(Result{Review: name, Status: StatusFail, Agent: agent.Spec{Tool: name}})
	}

	want := []string{"Ähre-review", "Zebra-review", "日本語-review"}
	got := make([]string, 0, len(want))
	for _, r := range st.Results() {
		got = append(got, r.Review)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Results() = %q, want %q", got, want)
	}
	got = got[:0]
	for _, r := range st.Failures() {
		got = append(got, r.Review)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Failures() = %q, want %q", got, want)
	}
	got = got[:0]
	for _, a := range st.ByAgent() {
		got = append(got, a.Label)
	}
	if !slices.Equal(got, want) {
		t.Errorf("ByAgent() = %q, want %q", got, want)
	}
}

func TestByAgentIgnoresNegativeElapsed(t *testing.T) {
	st := &Stats{}
	st.Add(Result{Status: StatusOK, Elapsed: 10 * time.Second, Agent: agent.Spec{Tool: "claude"}})
	st.Add(Result{Status: StatusFail, Elapsed: -5 * time.Second, Agent: agent.Spec{Tool: "claude"}})

	got := st.ByAgent()
	if len(got) != 1 || got[0].Elapsed != 10*time.Second {
		t.Fatalf("negative elapsed should not reduce agent elapsed, got %+v", got)
	}
}

func TestTokensPerSec(t *testing.T) {
	if got := (AgentSummary{Tokens: 0, Elapsed: time.Minute}).TokensPerSec(); got != 0 {
		t.Errorf("no tokens means no rate: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: 500 * time.Millisecond}).TokensPerSec(); got != 0 {
		t.Errorf("sub-second totals make any rate noise: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: 0}).TokensPerSec(); got != 0 {
		t.Errorf("zero elapsed must return 0: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: -time.Second}).TokensPerSec(); got != 0 {
		t.Errorf("negative elapsed must return 0: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: 20 * time.Second}).TokensPerSec(); got != 5 {
		t.Errorf("got %v, want 5", got)
	}
}

// TestResultsOrderIndependentOfCompletionOrder pins that a run reports its
// reviews in name order, not in the order the lanes happened to finish. Two
// stats fed the same reviews in opposite completion orders must read
// identically, and a review that ran in two loops keeps its loop order.
func TestResultsOrderIndependentOfCompletionOrder(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review", "ad-review"}
	forward := &Stats{Start: time.Now()}
	for _, name := range reviews {
		forward.Add(Result{Review: name, Status: StatusOK})
	}
	forward.Add(Result{Review: "aa-review", Status: StatusFail, Detail: "loop two"})

	backward := &Stats{Start: time.Now()}
	for _, review := range slices.Backward(reviews) {
		backward.Add(Result{Review: review, Status: StatusOK})
	}
	backward.Add(Result{Review: "aa-review", Status: StatusFail, Detail: "loop two"})

	got, want := backward.Results(), forward.Results()
	if !slices.EqualFunc(got, want, func(a, b Result) bool {
		return a.Review == b.Review && a.Status == b.Status && a.Detail == b.Detail
	}) {
		t.Fatalf("completion order leaked into the result list:\n%+v\n%+v", got, want)
	}
	if names := []string{got[0].Review, got[1].Review}; names[0] != "aa-review" || names[1] != "aa-review" {
		t.Fatalf("the two loops of one review must stay adjacent and in run order: %+v", got)
	}
	if got[0].Status != StatusOK || got[1].Status != StatusFail {
		t.Fatalf("loop order within a review reversed: %+v", got[:2])
	}
}

// TestFailuresOrderIndependentOfCompletionOrder pins that the failure list
// reads by name and then by loop, the way Results does. A review that failed
// in two loops contributes two rows, each with its own branch and lines, so an
// unstable sort would print one run's failures in either order.
func TestFailuresOrderIndependentOfCompletionOrder(t *testing.T) {
	reviews := []string{"aa-review", "ab-review", "ac-review", "ad-review"}
	forward := &Stats{Start: time.Now()}
	for _, name := range reviews {
		forward.Add(Result{Review: name, Status: StatusFail})
	}
	forward.Add(Result{Review: "aa-review", Status: StatusTimeout, Branch: "loop-two"})

	backward := &Stats{Start: time.Now()}
	for _, review := range slices.Backward(reviews) {
		backward.Add(Result{Review: review, Status: StatusFail})
	}
	backward.Add(Result{Review: "aa-review", Status: StatusTimeout, Branch: "loop-two"})

	got, want := backward.Failures(), forward.Failures()
	if !slices.EqualFunc(got, want, func(a, b Result) bool {
		return a.Review == b.Review && a.Status == b.Status && a.Branch == b.Branch
	}) {
		t.Fatalf("completion order leaked into the failure list:\n%+v\n%+v", got, want)
	}
	if len(got) != 5 {
		t.Fatalf("failure list: %+v", got)
	}
	if got[0].Status != StatusFail || got[1].Status != StatusTimeout {
		t.Fatalf("loop order within a review reversed: %+v", got[:2])
	}
}

// TestStatsSurvivesConcurrentUse pins the documented guarantee under the race
// detector: parallel lanes Add results and record commit steps while readers
// tally, and a hot-reload Seed lands mid-run. Every access must go through the
// mutex; an unsynchronized counter shows up here as a detector report long
// before it shows up in a run.
func TestStatsSurvivesConcurrentUse(t *testing.T) {
	st := &Stats{Start: time.Now()}
	const lanes, perLane = 8, 250

	var wg sync.WaitGroup
	for range lanes {
		wg.Go(func() {
			for range perLane {
				st.Add(Result{Review: "r-review", Status: StatusOK})
				st.Counts()
				st.Totals()
				st.ByAgent()
				_ = st.CommitRuns()
				_ = st.CommitFails()
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			st.Seed(nil, 1, 0)
			st.Results()
			st.Failures()
		}
	})
	wg.Go(func() {
		for range 100 {
			st.addCommitRun()
			st.addCommitFail()
		}
	})
	wg.Wait()

	if got := len(st.Results()); got != lanes*perLane {
		t.Fatalf("results lost under concurrency: %d, want %d", got, lanes*perLane)
	}
	if runs, fails := st.CommitRuns(), st.CommitFails(); runs != 200 || fails != 100 {
		t.Fatalf("counters lost under concurrency: runs=%d fails=%d", runs, fails)
	}
}

func TestTokensCountsCarriedOverResults(t *testing.T) {
	// A hot reload continues the same run, so a token budget read here must
	// include what the predecessor already spent.
	st := &Stats{Start: time.Now()}
	st.Add(Result{Review: "b-review", Tokens: 250})
	st.Seed([]Result{{Review: "a-review", Tokens: 700}}, 0, 0)

	if got := st.Tokens(); got != 950 {
		t.Fatalf("Tokens() = %d, want 950", got)
	}
}

func TestRunningTokenTotalMatchesTheRecordedResults(t *testing.T) {
	// Tokens() reads a running total rather than walking results, so the
	// total has to stay equal to the sum over what was recorded. A run with
	// --max-loops 0 records forever, which is the case that would drift.
	st := &Stats{Start: time.Now()}
	want := 0
	add := func(r Result) {
		st.Add(r)
		want += r.Tokens
	}
	add(Result{Review: "a-review", Tokens: 120, Elapsed: time.Second, Ins: 3, Del: 1, HaveLines: true})
	add(Result{Review: "b-review", Tokens: 0, Status: StatusSkipped})
	seeded := []Result{
		{Review: "a-review", Tokens: 45},
		{Review: "c-review", Tokens: 7, Elapsed: 2 * time.Second, Ins: 2, HaveLines: true},
	}
	st.Seed(seeded, 3, 1)
	for _, r := range seeded {
		want += r.Tokens
	}

	if got := st.Tokens(); got != want {
		t.Fatalf("Tokens() = %d, want %d", got, want)
	}
	_, _, tokens, _, _, _ := st.Totals()
	if tokens != want {
		t.Fatalf("Totals() tokens = %d, want %d", tokens, want)
	}
	sum := 0
	for _, r := range st.Results() {
		sum += r.Tokens
	}
	if sum != want {
		t.Fatalf("sum over Results() = %d, want %d", sum, want)
	}
}

// A run long enough to pass maxDetailResults keeps the newest ones and says
// how many it dropped. An off-by-one in the window either duplicates a row or
// drops a review that is not the oldest, and the count is what a summary
// prints next to a short list, so a wrong number there reads as the whole run.
func TestDetailKeepsTheNewestResultsAndCountsTheRest(t *testing.T) {
	st := &Stats{Start: time.Now()}
	const over = 10
	for i := range maxDetailResults + over {
		st.Add(Result{Review: fmt.Sprintf("r%05d-review", i), Status: StatusOK})
	}
	if got, want := len(st.Results()), maxDetailResults; got != want {
		t.Fatalf("kept %d results, want the cap of %d", got, want)
	}
	if got, want := st.DetailDropped(), over; got != want {
		t.Fatalf("DetailDropped() = %d, want %d", got, want)
	}
	kept := st.Results()
	// Names sort, so the window is read by name rather than by position: what
	// has to hold is that the dropped ten are the first ten of the run, in one
	// name order, and that nothing is listed twice.
	if first, last := kept[0].Review, kept[len(kept)-1].Review; first != "r00010-review" || last != "r02009-review" {
		t.Fatalf("kept %s through %s, want r00010-review through r02009-review", first, last)
	}
	names := make([]string, len(kept))
	for i, r := range kept {
		names[i] = r.Review
	}
	if !slices.IsSorted(names) {
		t.Fatalf("detail is not in name order: %v", names[:4])
	}
	if dup := slices.Compact(slices.Clone(names)); len(dup) != len(names) {
		t.Fatalf("a result is listed twice: %d of %d names are distinct", len(dup), len(names))
	}
}

// A hot reload seeds a predecessor's whole run, which is bounded on its own
// and again against the cap once it is in front of the successor's own work.
func TestSeedBoundsTheCarriedOverResults(t *testing.T) {
	const over = 10
	carried := make([]Result, maxDetailResults+over)
	for i := range carried {
		carried[i] = Result{Review: fmt.Sprintf("r%05d-review", i), Status: StatusOK}
	}
	st := &Stats{Start: time.Now()}
	st.Add(Result{Review: "zz-successor-review", Status: StatusOK})
	st.Seed(carried, 0, 0)

	if got := len(st.Results()); got != maxDetailResults {
		t.Fatalf("kept %d results, want the cap of %d", got, maxDetailResults)
	}
	// The successor's own result takes a slot of the cap, so the seed's own
	// overflow of ten is joined by one more carried row that has nowhere to go:
	// the run recorded 2010 results before the swap and one after, the listing
	// holds the cap, and the count printed beside a short list is the
	// difference rather than the seed's overflow alone, since the successor's
	// result is the row that pushes the first carried one off the front.
	if got, want := st.DetailDropped(), over+1; got != want {
		t.Fatalf("DetailDropped() = %d, want %d", got, want)
	}
	kept := st.Results()
	// The successor's own work is the newest, so it stays listed: a seed that
	// pushed it off the end would drop the result the live run just produced.
	if got := kept[len(kept)-1].Review; got != "zz-successor-review" {
		t.Fatalf("the newest result is %q, want the successor's own", got)
	}
}
