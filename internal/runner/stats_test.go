// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
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

	c := st.Counts()
	if c.Total() != 5 {
		t.Fatalf("total: %+v", c)
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

func TestTokensPerSec(t *testing.T) {
	if got := (AgentSummary{Tokens: 0, Elapsed: time.Minute}).TokensPerSec(); got != 0 {
		t.Errorf("no tokens means no rate: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: 500 * time.Millisecond}).TokensPerSec(); got != 0 {
		t.Errorf("sub-second totals make any rate noise: %v", got)
	}
	if got := (AgentSummary{Tokens: 100, Elapsed: 20 * time.Second}).TokensPerSec(); got != 5 {
		t.Errorf("got %v, want 5", got)
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
