// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"sort"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// Result is the outcome of one review run.
type Result struct {
	Review   string
	Agent    agent.Spec
	Status   Status
	ExitCode int
	Elapsed  time.Duration

	// Ins/Del are only meaningful when HaveLines is set: git may be absent,
	// or concurrent reviews may make attribution impossible.
	Ins, Del  int
	HaveLines bool

	Tokens int
	// Thinking is the reasoning share of Tokens, 0 when the agent does not
	// report one.
	Thinking int
	Branch   string // set in worktree mode
}

// Stats accumulates results across a run. Safe for concurrent use: parallel
// review lanes Add results while the commit step records its runs, and a
// reader may tally at any point in between.
type Stats struct {
	mu      sync.Mutex
	results []Result

	commitRuns  int
	commitFails int

	Start time.Time
}

// CommitRuns is how many commit steps ran.
func (s *Stats) CommitRuns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitRuns
}

// CommitFails is how many commit steps failed, including launches that never
// reached an agent.
func (s *Stats) CommitFails() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitFails
}

// addCommitRun records one commit step that launched.
func (s *Stats) addCommitRun() {
	s.mu.Lock()
	s.commitRuns++
	s.mu.Unlock()
}

// addCommitFail records one commit-step failure, including launches that
// never reached an agent.
func (s *Stats) addCommitFail() {
	s.mu.Lock()
	s.commitFails++
	s.mu.Unlock()
}

// Add records one result.
func (s *Stats) Add(r Result) {
	s.mu.Lock()
	s.results = append(s.results, r)
	s.mu.Unlock()
}

// Seed pre-loads results carried over from an earlier process. A hot reload
// continues the same run, so its successor must report the whole run, not just
// what happened after the swap.
func (s *Stats) Seed(results []Result, commitRuns, commitFails int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(append([]Result(nil), results...), s.results...)
	s.commitRuns += commitRuns
	s.commitFails += commitFails
}

// Results returns a copy of every result so far.
func (s *Stats) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Result(nil), s.results...)
}

// Counts tallies results by status.
type Counts struct {
	OK, Fail, Timeout, Skipped, Interrupted, Conflict int
}

// tally records one result under its status bucket.
func (c *Counts) tally(s Status) {
	switch s {
	case StatusOK:
		c.OK++
	case StatusFail:
		c.Fail++
	case StatusTimeout:
		c.Timeout++
	case StatusSkipped:
		c.Skipped++
	case StatusInterrupted:
		c.Interrupted++
	case StatusConflict:
		c.Conflict++
	}
}

// Add merges another tally into this one.
func (c *Counts) Add(o Counts) {
	c.OK += o.OK
	c.Fail += o.Fail
	c.Timeout += o.Timeout
	c.Skipped += o.Skipped
	c.Interrupted += o.Interrupted
	c.Conflict += o.Conflict
}

// Total is every recorded result.
func (c Counts) Total() int {
	return c.OK + c.Fail + c.Timeout + c.Skipped + c.Interrupted + c.Conflict
}

// Failures counts results that make the run exit nonzero.
func (c Counts) Failures() int { return c.Fail + c.Timeout + c.Skipped + c.Conflict }

// Counts tallies the recorded results.
func (s *Stats) Counts() Counts {
	var c Counts
	for _, r := range s.Results() {
		c.tally(r.Status)
	}
	return c
}

// Totals sums lines changed, tokens reported, and agent wall time.
func (s *Stats) Totals() (ins, del, tokens int, agentTime time.Duration, timed int, haveLines bool) {
	for _, r := range s.Results() {
		if r.HaveLines {
			ins += r.Ins
			del += r.Del
			haveLines = true
		}
		tokens += r.Tokens
		if r.Elapsed > 0 {
			agentTime += r.Elapsed
			timed++
		}
	}
	return ins, del, tokens, agentTime, timed, haveLines
}

// AgentSummary is one agent's slice of the run.
type AgentSummary struct {
	Label   string
	Counts  Counts
	Tokens  int
	Elapsed time.Duration
}

// TokensPerSec is the agent's throughput, or 0 when there is nothing to divide
// by. Sub-second totals make any rate noise, so they report 0.
func (a AgentSummary) TokensPerSec() float64 {
	if a.Tokens == 0 || a.Elapsed < time.Second {
		return 0
	}
	return float64(a.Tokens) / a.Elapsed.Seconds()
}

// ByAgent breaks the run down per tool:model, in label order.
func (s *Stats) ByAgent() []AgentSummary {
	byLabel := map[string]*AgentSummary{}
	for _, r := range s.Results() {
		label := r.Agent.Label()
		a, ok := byLabel[label]
		if !ok {
			a = &AgentSummary{Label: label}
			byLabel[label] = a
		}
		a.Counts.tally(r.Status)
		a.Tokens += r.Tokens
		a.Elapsed += r.Elapsed
	}
	out := make([]AgentSummary, 0, len(byLabel))
	for _, a := range byLabel {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Failures lists every result that makes the run exit nonzero, by name.
// Skipped is included: a review that never ran (unknown name, unreadable
// prompt) is why exit code 1 exists alongside ok and interrupted, and the
// detailed list must account for every nonzero exit the counts report.
func (s *Stats) Failures() []Result {
	var out []Result
	for _, r := range s.Results() {
		if r.Status.Failed() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Review < out[j].Review })
	return out
}
