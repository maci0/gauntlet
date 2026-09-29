// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"cmp"
	"slices"
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
	// Subject is the commit subject the agent gave for its change, when the
	// review printed one. The runner writes the commit in worktree mode, and
	// this is the only description of the change it has.
	Subject string
	// FileNotes are the per-file "what was done" lines the review printed,
	// the only per-file description of the change that exists anywhere: a
	// stacked layer's PR body combines them into an overview paragraph under
	// Summary for the paths its commit touched.
	FileNotes []agent.FileNote
	// Thinking is the reasoning share of Tokens, 0 when the agent does not
	// report one.
	Thinking int
	Branch   string // set in worktree mode
	Base     string // pull request base in stacked-PR mode
	URL      string // pull request URL in stacked-PR mode
	Detail   string // infrastructure failure detail when no agent exit explains it
}

// maxDetailResults bounds the per-result detail a Stats keeps. A run with
// --max-loops 0 records a result per review per loop for as long as it is left
// going, so the detail has no bound the run itself imposes: it grew for the
// life of the process, and every tally walked it, so a stacked run paid
// O(loops^2) under the mutex every lane contends for, and a hot reload
// serialized the whole slice into a handoff past the 16 MiB read cap, which
// its reader treats as a corrupt blob and the successor exits on.
//
// The cap is on detail only. Every number a run reports is folded in as the
// result arrives and stays exact for all of them; past the cap the summary
// stops listing individual rows (the pull-request list, the per-review failure
// list) and keeps counting. detailDropped records how many it stopped
// listing, so a run that hit the cap says so rather than reporting a short
// list as the whole run.
const maxDetailResults = 2000

// MaxDetailResults is the bound maxDetailResults names, exported so the
// summary can print it next to the number of rows it left out.
const MaxDetailResults = maxDetailResults

// Stats accumulates results across a run. Safe for concurrent use: parallel
// review lanes Add results while the commit step records its runs, and a
// reader may tally at any point in between.
type Stats struct {
	mu      sync.Mutex
	results []Result

	// The aggregates below are the running sums of the results, maintained as
	// each one arrives so the readers below cost the same on loop 2 and on
	// loop 2000. They cover every result, including the ones the detail slice
	// has already dropped.
	tokens   int
	counts   Counts
	byAgent  map[string]*AgentSummary
	ins, del int
	thinking int
	// agentTime and timed are summed only over results with a positive
	// Elapsed, and haveLines only over results that reported lines.
	agentTime time.Duration
	timed     int
	haveLines bool

	// detailDropped is how many results the detail slice has discarded.
	detailDropped int

	commitRuns  int
	commitFails int

	// run counts the same tokens as tokens does, for every directory of a
	// multi-directory run. nil when the run is one directory, in which case
	// the local running total is the whole run and the shared one would
	// double it.
	run *Tokens

	Start time.Time
}

// NewStats is a Stats for a run started at start, counting against the run-wide
// tally run. A nil run is a single-directory run, whose own totals are the
// run's.
func NewStats(start time.Time, run *Tokens) *Stats {
	return &Stats{Start: start, run: run}
}

// Tokens is a run-wide token tally, shared by the runners of a
// multi-directory run. One process runs one runner per directory, each with
// its own Stats, so a ceiling read from that Stats alone is a ceiling per
// directory: a three-directory run under --token-budget 1000000 spends up to
// three million. The tally is what the ceiling is measured against.
//
// Safe for concurrent use: every directory's lanes add to it while every
// other directory's budget reads it.
type Tokens struct {
	mu sync.Mutex
	n  int
}

// Add records what one review reported.
func (t *Tokens) Add(n int) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	t.n += n
	t.mu.Unlock()
}

// Total is every token every directory of the run has recorded so far.
func (t *Tokens) Total() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
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

func (s *Stats) addCommitRun() {
	s.mu.Lock()
	s.commitRuns++
	s.mu.Unlock()
}

func (s *Stats) addCommitFail() {
	s.mu.Lock()
	s.commitFails++
	s.mu.Unlock()
}

// fold adds one result to the running aggregates. The caller holds s.mu.
func (s *Stats) fold(r Result) {
	s.tokens += r.Tokens
	s.thinking += r.Thinking
	if r.HaveLines {
		s.ins += r.Ins
		s.del += r.Del
		s.haveLines = true
	}
	if r.Elapsed > 0 {
		s.agentTime += r.Elapsed
		s.timed++
	}
	// An empty status is publication metadata recovered without launching an
	// agent. Counts and ByAgent have always skipped those, and skipping them
	// here too is what keeps the two agreeing with a walk over the slice.
	if r.Status == "" {
		return
	}
	s.counts.tally(r.Status)
	if s.byAgent == nil {
		s.byAgent = map[string]*AgentSummary{}
	}
	label := r.Agent.Label()
	a, ok := s.byAgent[label]
	if !ok {
		a = &AgentSummary{Label: label}
		s.byAgent[label] = a
	}
	a.Counts.tally(r.Status)
	a.Tokens += r.Tokens
	if r.Elapsed > 0 {
		a.Elapsed += r.Elapsed
	}
}

// keepDetail appends to the capped detail slice, dropping the oldest result
// once it is full. The caller holds s.mu.
func (s *Stats) keepDetail(r Result) {
	if len(s.results) < maxDetailResults {
		s.results = append(s.results, r)
		return
	}
	copy(s.results, s.results[1:])
	s.results[len(s.results)-1] = r
	s.detailDropped++
}

// Add records one result.
func (s *Stats) Add(r Result) {
	s.mu.Lock()
	s.fold(r)
	s.keepDetail(r)
	s.mu.Unlock()
	s.run.Add(r.Tokens)
}

// Seed pre-loads results carried over from an earlier process. A hot reload
// continues the same run, so its successor must report the whole run, not just
// what happened after the swap.
func (s *Stats) Seed(results []Result, commitRuns, commitFails int) {
	s.mu.Lock()
	carried := 0
	kept := make([]Result, 0, min(len(results), maxDetailResults))
	// Oldest first, so the detail kept is the most recent work, the same end
	// of the run the cap keeps when results arrive one at a time.
	for _, r := range results {
		s.fold(r)
		carried += r.Tokens
	}
	if len(results) > maxDetailResults {
		s.detailDropped += len(results) - maxDetailResults
		kept = append(kept, results[len(results)-maxDetailResults:]...)
	} else {
		kept = append(kept, results...)
	}
	s.results = append(kept, s.results...)
	if len(s.results) > maxDetailResults {
		s.results = append([]Result(nil), s.results[len(s.results)-maxDetailResults:]...)
	}
	s.commitRuns += commitRuns
	s.commitFails += commitFails
	s.mu.Unlock()
	// Carried tokens go on the shared tally too: the ceiling is a run budget,
	// and a reload's successor must continue the run it inherited rather than
	// hand the run a fresh allowance for the part already spent.
	s.run.Add(carried)
}

// DetailDropped is how many results the detail slice has discarded. It is
// nonzero only on a run long enough to pass maxDetailResults, and it is
// reported so a summary that lists fewer rows than the run ran says why.
func (s *Stats) DetailDropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detailDropped
}

// Results returns a copy of the results still held, in review-name order. The
// slice is bounded at maxDetailResults; a result older than the bound is
// counted everywhere but not listed here, and DetailDropped reports how many.
//
// Lanes finish in whatever order the OS scheduler picks, so insertion order
// would make a --jobs > 1 run report its reviews, and any list built from
// them, differently on every replay of the same seed. The sort is stable: the
// same review can run once per loop, and those are sequential, so keeping
// insertion order within a name keeps the loops in the order they ran.
func (s *Stats) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Result(nil), s.results...)
	slices.SortStableFunc(out, func(a, b Result) int { return cmp.Compare(a.Review, b.Review) })
	return out
}

// Counts tallies results by status.
type Counts struct {
	OK, Fail, Timeout, Skipped, Interrupted, Conflict int
	// Other counts results whose status this build does not recognize. A
	// hot reload continues the same run from a handoff the predecessor
	// wrote, so a status a newer build named reaches an older one, and a
	// status no switch case claims has to land in a bucket: dropped, it
	// makes the journal's row count reviews its own buckets do not add up
	// to, and the reporter's total disagree with the dashboard's. journal
	// carries the same bucket for the same reason.
	Other int
}

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
	default:
		c.Other++
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
	c.Other += o.Other
}

// Total is every recorded result.
func (c Counts) Total() int {
	return c.OK + c.Fail + c.Timeout + c.Skipped + c.Interrupted + c.Conflict + c.Other
}

// Failures counts results that make the run exit nonzero.
func (c Counts) Failures() int { return c.Fail + c.Timeout + c.Skipped + c.Conflict }

// Counts tallies the recorded results. The tally is exact for every result,
// including the ones the detail slice has dropped.
func (s *Stats) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts
}

// Tokens is every token the run has recorded, including results a predecessor
// process seeded. A token budget reads it before every review, so it reads the
// running total rather than walking results, and a hot reload continues the
// same ceiling instead of handing itself a fresh one.
//
// A multi-directory run reads the shared tally instead of this Stats: the
// ceiling is one for the run, and a per-directory total would multiply it by
// the number of directories.
func (s *Stats) Tokens() int {
	if s.run != nil {
		return s.run.Total()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}

// Thinking is every reasoning token the run has recorded, exact for results
// the detail slice has dropped, so a share computed against it does not fall as
// the oldest results are cut.
func (s *Stats) Thinking() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thinking
}

// Totals sums lines changed, tokens reported, and agent wall time. The sums
// are exact for every result, including the ones the detail slice has dropped.
func (s *Stats) Totals() (ins, del, tokens int, agentTime time.Duration, timed int, haveLines bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ins, s.del, s.tokens, s.agentTime, s.timed, s.haveLines
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
	if a.Tokens <= 0 || a.Elapsed < time.Second {
		return 0
	}
	return float64(a.Tokens) / a.Elapsed.Seconds()
}

// ByAgent breaks the run down per tool:model, in label order. The breakdown
// is exact for every result, including the ones the detail slice has dropped.
func (s *Stats) ByAgent() []AgentSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentSummary, 0, len(s.byAgent))
	for _, a := range s.byAgent {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b AgentSummary) int { return cmp.Compare(a.Label, b.Label) })
	return out
}

// Failures lists every result that makes the run exit nonzero, by name.
// Skipped is included: a review that never ran (unknown name, unreadable
// prompt) is why exit code 1 exists alongside ok and interrupted, and the
// detailed list must account for every nonzero exit the counts report.
//
// Sorted like Results, and stable for the same reason: a review that failed
// in two loops contributes two rows, each carrying its own branch, exit
// code, and lines, and an unstable sort would print them in either order.
//
// The list covers the results the detail slice still holds, so a run longer
// than maxDetailResults lists only its recent failures. Every failure is
// still counted in Counts and Failures(), and DetailDropped says how many rows
// the list left out.
//
// A status this build does not recognize is not listed here either: it is
// counted in Other, and claiming a failure it may not be would make the exit
// code a guess.
func (s *Stats) Failures() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Result
	for _, r := range s.results {
		if r.Status.Failed() {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Result) int { return cmp.Compare(a.Review, b.Review) })
	return out
}
