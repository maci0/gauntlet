// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runner schedules reviews onto agents and reports what happened.
package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/ghx"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
)

// Config describes one review loop over one directory.
type Config struct {
	NoSandbox    bool
	SandboxWrite []string
	Dir          string // absolute path of the tree under review
	Set          prompt.Set
	Reviews      []string // scheduled review names, in order, repeats meaning weight
	Agents       []agent.Spec
	Bin          map[string]string // agent -> executable override

	Timeout  time.Duration
	Jobs     int // 1: sequential, in place. >1: N persistent lane worktrees, then merge
	MaxLoops int
	// MaxReviews caps how many reviews one loop runs. The cut happens after
	// the seeded per-loop shuffle, so a seeded run replays exactly which
	// reviews made the cut, and every loop's draws match an uncapped run's.
	// A review scheduled twice holds two of the slots when both land inside
	// the cut. Zero means unlimited. A resume queue is never re-capped: it is
	// the already-truncated remainder of an interrupted loop.
	MaxReviews int
	Runtime    time.Duration

	// TokenBudget caps the tokens the run may report before it stops starting
	// reviews, across every loop, lane, and agent. Wall clock bounds a run on
	// a machine that is slow; it does not bound what the provider charges, and
	// an agent that stalls can spend a full timeout's worth of tokens in
	// seconds. Zero means unlimited.
	//
	// It counts the tokens the reviews report. The commit and conflict steps
	// launch agents too and are not counted, so the ceiling is on the review
	// schedule, which is where the multiplier (--jobs, --max-loops, a repeated
	// review) lives.
	//
	// The ceiling is one for the whole run. A process running several
	// directories builds one Runner per directory, each with its own Stats, so
	// RunTokens is the tally they share: a budget read from a directory's own
	// total would be a ceiling per directory, and a three-directory run under
	// --token-budget would spend up to three times it. nil for a single
	// directory, where the local total is the whole run.
	TokenBudget int
	RunTokens   *Tokens

	// UsageCmd is a command whose stdout is the percentage of the provider's
	// usage window already spent, and UsageLimit is the percentage at which
	// the run stops starting reviews. Both are needed for either to apply.
	// The runner cannot read that figure itself: it lives in the provider's
	// API response headers, and no agent CLI reports it to a headless launch.
	UsageCmd   []string
	UsageLimit float64

	// Retries is how many times a review is rerun on the same agent after a
	// launch failure or a nonzero exit, with a growing delay between tries.
	// The failures worth waiting out are transient: a rate limit, a dropped
	// connection, a CLI that died before it started. Zero keeps only the
	// fallback to a different agent.
	Retries int

	Commit bool
	Push   bool
	// StackedPRs runs the configured review order in an isolated worktree,
	// publishing each changed review as a child PR of the previous one.
	// MaxLoops (default 1) is how many of those passes to run: each pass
	// gets a fresh worktree cut from the previous pass's last published tip,
	// so later rounds see earlier fixes instead of reopening them.
	StackedPRs bool
	PRBase     string // initial remote base branch; empty means current branch name
	PushRemote string // remote receiving stack branches; empty means origin
	// AllowDirtyStack confirms that changes in the original checkout may be
	// excluded. Stack mode never reads them: its worktree starts at the fetched
	// remote base. The CLI sets this only after explicit consent (or on resume).
	AllowDirtyStack bool
	// PRRepo and PRHost are normally inferred from PushRemote. They exist so
	// tests can pair a local fake Git remote with a fake gh endpoint.
	PRRepo string
	PRHost string
	// ResumeStackTip pins a resumed stacked run to the base commit its
	// predecessor fetched. PrepareStack keeps it when the object is still in
	// the store, so a remote base that advanced during the reload cannot
	// rename the layers and split the run into a new stack.
	ResumeStackTip string
	// ResumeLoops is how many stacked (or sequential) loops a predecessor
	// already finished. Stacked branch names include the 1-based pass, so a
	// successor that continues loop 2 must not name its layers as loop 1.
	ResumeLoops int
	// ResumeStackHead and ResumeStackHeadTip are the last published layer
	// when a stacked run is interrupted. The next pass cuts from this tip
	// rather than from the original --pr-base, so already-applied fixes stay
	// in the tree. Empty means the original base is still the head.
	ResumeStackHead      string
	ResumeStackHeadTip   string
	ResumeStackPublished int
	// StackPrep carries a preflight the CLI already ran (before the suggest
	// step and the dirty-checkout consent it fronts). Nil makes New run
	// PrepareStack itself.
	StackPrep *StackPrep
	// MergeInto is a branch this run's work is merged into after each loop,
	// once the commit step has left the tree clean. Empty leaves the work
	// where the reviews put it, on the branch that was checked out.
	MergeInto string
	// ResolveConflicts hands a review branch that will not merge to an agent,
	// which resolves it in a scratch checkout so the work lands. Off leaves
	// the branch for a human, which is what a run does when the tree it
	// merges into is not one an agent should be editing blind.
	ResolveConflicts bool
	Yolo             bool
	// Paths is the operator's --paths scope: files, directories, or globs,
	// relative to Dir, that review prompts tell the agent to confine findings
	// and edits to. The agent still works from the whole tree; the scope is
	// prompt-enforced, not mechanical. Empty means unscoped, and only review
	// prompts carry it: suggest, commit, and conflict prompts are unchanged.
	Paths []string
	Raw   bool
	// Stream asks agents that support it for machine-readable output, which
	// carries token usage and separates reasoning from visible text.
	Stream           bool
	Quiet            bool
	ContinueSessions bool

	// Started is when the run began, which may predate this process: a hot
	// reload hands the original start time to its successor so the runtime
	// budget covers the whole run, not just the latest binary.
	Started time.Time

	// Seed drives every stochastic choice the runner makes: the per-loop
	// review shuffle, agent sampling, and backoff jitter. Each choice is a
	// pure function of this seed and inputs a journal already records (loop
	// number, review name, attempt number), so no choice depends on the order
	// goroutines run in or on how many choices ran before it, and a nonzero
	// seed replays a parallel run as exactly as a sequential one, across a hot
	// reload too. Zero derives one from the clock, as runs always have; the
	// effective seed is published on the run-start event so any journal can
	// be reproduced from what it records.
	Seed uint64

	// ResumeQueue is the unfinished part of a loop interrupted by a hot
	// reload. When set, it is the first loop's schedule.
	ResumeQueue []string

	RunID        string
	Version      string
	OwnArtifacts map[string]bool // real paths the runner itself created
}

// ErrDirtyTree is the one worktree precondition a caller can do something
// about: the work is there, it simply is not committed. Callers that can ask
// a person offer the commit step rather than stopping.
var ErrDirtyTree = errors.New("--jobs > 1 needs a clean working tree")

// Runner executes Config until it is stopped, the loop limit is reached, or
// the runtime budget runs out.
type Runner struct {
	cfg Config
	bus *Bus
	st  *Stats

	repo *gitx.Repo
	gh   ghx.Client

	stackBase    string
	stackBaseTip string
	// stackHead / stackHeadTip are the last published layer (branch name and
	// commit), or the original base when nothing has published yet. Each
	// stacked --max-loops pass cuts its worktree from this tip.
	stackHead      string
	stackHeadTip   string
	stackPublished int
	// stackReadRemote is where stack branches are read back from (ls-remote,
	// fetch): the push URL when it differs from the fetch URL, else the
	// remote name. Pushes keep using the remote name.
	stackReadRemote string

	mu             sync.Mutex // guards sessionStarted, stackHead, stackPublished
	seed           uint64     // effective seed: cfg.Seed, or clock-derived when zero
	sessionStarted map[agent.Spec]bool
	// tools is where this machine's helper binaries resolved to, probed once
	// at startup rather than per review; SplitTools treats an empty or
	// absent entry as missing.
	tools   map[string]string
	mergeMu sync.Mutex // serializes merges into the main tree

	// retrySnap is the in-place tree as it was before the current review.
	// Isolated reviews rewind a worktree to its base commit instead.
	retrySnap gitx.Snapshot

	loopMu    sync.Mutex
	loopCount int

	// pending is what the current loop has not started yet, in scheduled
	// order. A soft stop hands it to the successor, so a reload never re-runs
	// reviews that already ran in the interrupted loop.
	pendingMu sync.Mutex
	pending   []queued
	// resume is the queue handed over by a previous process; it replaces the
	// first loop's schedule.
	resume []string

	// soft is set when the run should end at the next quiescent point: a hot
	// reload waiting for in-flight reviews, or a runtime budget that expired.
	soft atomic.Bool
	// finish is the graceful quit: like soft, no new review starts, but what
	// is in flight is drained and its work is committed, published, or merged
	// before the run ends. A reload hands its unfinished reviews to a
	// successor; a graceful quit has no successor, so it must not leave the
	// loop's output uncommitted.
	finish atomic.Bool
	// usageProbeFailed keeps a broken usage probe from narrating once per
	// review. The first failure is worth a line; the rest are the same line.
	usageProbeFailed atomic.Bool
	// budgetStop is set when a run budget stopped the loop from starting
	// another review. Like finish it ends the run once the loop's commit and
	// merge steps have run, which is what a review that ran to completion owes
	// the tree.
	budgetStop atomic.Bool
}

// New prepares a runner. It opens the repository (if any) and validates the
// preconditions for the requested concurrency.
func New(ctx context.Context, cfg Config, bus *Bus) (*Runner, error) {
	if len(cfg.Agents) == 0 {
		return nil, errors.New("no agents to run reviews with: install one (see `gauntlet doctor`)")
	}
	if len(cfg.Reviews) == 0 {
		return nil, errors.New("no reviews scheduled")
	}
	if cfg.Jobs < 1 {
		cfg.Jobs = 1
	}
	if cfg.StackedPRs {
		cfg.Jobs = 1
		if cfg.PushRemote == "" {
			cfg.PushRemote = "origin"
		}
		// Stack mode never shuffles: each pass walks cfg.Reviews in
		// configured order, and the resume suffix check indexes into it, so
		// the cap truncates the schedule itself rather than the per-loop draw.
		if n := cfg.MaxReviews; n > 0 && n < len(cfg.Reviews) {
			cfg.Reviews = cfg.Reviews[:n]
		}
	}
	start := cfg.Started
	if start.IsZero() {
		start = bus.now()
	}
	seed := SeedOrClock(cfg.Seed, bus.now)
	repo := gitx.Open(cfg.Dir)
	// The sample debounce is a time decision about a review's line
	// attribution, so it reads the run's clock, not the wall clock: one seed
	// has to attribute the same lines to the same review on every replay.
	repo.Now = bus.now
	r := &Runner{
		cfg:            cfg,
		bus:            bus,
		st:             NewStats(start, cfg.RunTokens),
		repo:           repo,
		sessionStarted: map[agent.Spec]bool{},
		resume:         append([]string(nil), cfg.ResumeQueue...),
		tools:          resolveTools(cfg.Reviews),
	}
	r.seed = seed
	// Whatever this run does, it writes a lock in the reviewed tree and may
	// write worktrees under it. Neither is the project's, so neither should
	// ever show up in its git status: the exclusion is local to the clone,
	// which is the right place for one tool's scratch.
	if err := r.repo.ExcludeOwnArtifacts(ctx); err != nil {
		// Nothing downstream depends on the exclusion, so the run continues.
		// Saying so is what keeps a full disk from reading as a run that
		// simply never created its scratch.
		r.log("Cannot record scratch paths in git exclude: %v", err)
	}
	if cfg.StackedPRs {
		prep := cfg.StackPrep
		if prep == nil {
			var err error
			if prep, err = PrepareStack(ctx, cfg); err != nil {
				return nil, err
			}
		}
		r.cfg.PRBase = prep.Base
		r.gh = prep.GH
		r.stackBase, r.stackBaseTip = prep.Base, prep.BaseTip
		r.stackHead, r.stackHeadTip = prep.Base, prep.BaseTip
		if cfg.ResumeStackHeadTip != "" {
			r.stackHead = cfg.ResumeStackHead
			r.stackHeadTip = cfg.ResumeStackHeadTip
			r.stackPublished = cfg.ResumeStackPublished
		}
		r.stackReadRemote = prep.ReadRemote
		if r.stackReadRemote == "" {
			r.stackReadRemote = r.cfg.PushRemote
		}
	} else if cfg.Jobs > 1 {
		if err := r.prepareWorktreeMode(ctx); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// isolated reports whether reviews run in their own worktrees rather than in
// the main tree: lane worktrees under --jobs, one stack worktree per stacked
// pass. Every mode it names also means the run owns worktree scratch of its
// own and the main tree's line counts are not attributable to a review.
func (r *Runner) isolated() bool { return r.cfg.StackedPRs || r.cfg.Jobs > 1 }

// Run executes loops until the context is canceled or a limit is reached.
func (r *Runner) Run(ctx context.Context) {
	r.bus.Publish(Event{
		Kind: EvRunStart, Dir: r.cfg.Dir, Version: r.cfg.Version,
		Agents: agent.Labels(r.cfg.Agents), Total: r.perLoop(),
		Seed: r.seed,
	})
	defer func() {
		r.bus.Publish(Event{Kind: EvRunEnd, Dir: r.cfg.Dir, Loop: r.Loops()})
	}()
	if r.isolated() {
		defer r.repo.CleanWorktreeRoot()
	}

	for {
		if ctx.Err() != nil || r.soft.Load() {
			return
		}
		if why := r.budgetExhausted(); why != "" {
			r.log("%s budget exhausted, finishing up", why)
			return
		}
		loopNo := r.Loops() + 1
		r.bus.Publish(Event{Kind: EvLoopStart, Dir: r.cfg.Dir, Loop: loopNo, Total: r.perLoop()})

		start := r.now()
		// An isolated loop's line counts come from the run's own totals, not
		// from sampling the main tree: nothing in it is written in place, so a
		// sample there would only be discarded.
		var before gitx.Stats
		var haveBefore bool
		var beforeIns, beforeDel int
		if r.isolated() {
			beforeIns, beforeDel, _, _, _, _ = r.st.Totals()
		} else {
			before, haveBefore = r.sample(ctx)
		}

		var completed bool
		if r.cfg.StackedPRs {
			completed = r.runLoopStack(ctx, loopNo)
		} else if r.cfg.Jobs > 1 {
			completed = r.runLoopParallel(ctx, loopNo)
		} else {
			completed = r.runLoopSequential(ctx, loopNo)
		}
		if !completed {
			return // interrupted: the loop's last review did not finish
		}

		r.loopMu.Lock()
		r.loopCount++
		loops := r.loopCount
		r.loopMu.Unlock()

		ev := Event{
			Kind: EvLoopEnd, Dir: r.cfg.Dir, Loop: loops,
			Elapsed: max(r.now().Sub(start), 0).Seconds(),
		}
		if r.isolated() {
			afterIns, afterDel, _, _, _, haveLines := r.st.Totals()
			if haveLines {
				ins, del := afterIns-beforeIns, afterDel-beforeDel
				ev.Ins, ev.Del = new(ins), new(del)
			}
		} else if after, ok := r.sample(ctx); ok && haveBefore {
			ins, del := delta(before, after)
			ev.Ins, ev.Del = new(ins), new(del)
		}
		r.bus.Publish(ev)

		if r.finish.Load() {
			return // the graceful quit's last loop is done
		}
		if r.budgetStop.Load() {
			return // the budget stopped the last loop, after its commit and merge
		}
		if r.cfg.MaxLoops > 0 && loops >= r.cfg.MaxLoops {
			return
		}
	}
}

// RequestStop asks the runner to finish the reviews already in flight and then
// return. Unlike canceling the context, it never kills a running agent: the
// reviews now running finish normally, including their commit and publication
// or merge work.
func (r *Runner) RequestStop() { r.soft.Store(true) }

// RequestFinish asks the runner to stop starting reviews and end the run once
// the ones already running are done, their results committed, pushed, and
// merged as the flags ask. Reviews not yet started are dropped, not deferred:
// nothing follows this run.
func (r *Runner) RequestFinish() { r.finish.Store(true) }

// Pending is what the current loop had not started when the runner stopped.
// Handing it to a successor lets a hot reload finish the loop rather than
// starting it over.
func (r *Runner) Pending() []string {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	out := make([]string, 0, len(r.pending))
	for _, q := range r.pending {
		out = append(out, q.review)
	}
	return out
}

// StackHead is the last published stacked layer (branch name and tip), or the
// original --pr-base when nothing has published yet. A hot-reload successor
// cuts the next pass from this tip.
func (r *Runner) StackHead() (branch, tip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stackHead, r.stackHeadTip
}

// StackPublished is how many layers this stacked run has opened PRs for.
func (r *Runner) StackPublished() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stackPublished
}

// Stats exposes the accumulated results.
func (r *Runner) Stats() *Stats { return r.st }

// Loops is the number of completed loops.
func (r *Runner) Loops() int {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	return r.loopCount
}
