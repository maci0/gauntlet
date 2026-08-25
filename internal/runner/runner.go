// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runner schedules reviews onto agents and reports what happened.
package runner

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
)

// commitTimeout caps the commit and push step; a review's own --timeout may be
// much longer, but writing a commit message is not a long job.
const commitTimeout = 5 * time.Minute

// outputRateLimit bounds how many lines one agent may contribute per second.
// Above this, output is summarized instead of echoed: no dashboard and no log
// file is improved by 10k lines/s of narration.
const outputRateLimit = 200

// Config describes one review loop over one directory.
type Config struct {
	Dir     string // absolute path of the tree under review
	Set     prompt.Set
	Reviews []string // scheduled review names, in order, repeats meaning weight
	Agents  []agent.Spec
	Bin     map[string]string // agent -> executable override

	Timeout  time.Duration
	Jobs     int // 1: sequential, in place. >1: one worktree per review, then merge
	MaxLoops int
	Runtime  time.Duration

	Commit bool
	Push   bool
	Yolo   bool
	Raw    bool
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
	// review shuffle and agent sampling. Zero derives one from the clock, as
	// runs always have; a nonzero seed replays those choices exactly, and the
	// effective seed is published on the run-start event so any journal can be
	// reproduced from what it records.
	Seed uint64

	// ResumeQueue is the unfinished part of a loop interrupted by a hot
	// reload. When set, it is the first loop's schedule.
	ResumeQueue []string

	RunID        string
	Version      string
	OwnArtifacts map[string]bool // real paths the runner itself created
}

// Runner executes Config until it is stopped, the loop limit is reached, or
// the runtime budget runs out.
type Runner struct {
	cfg Config
	bus *Bus
	st  *Stats

	repo *gitx.Repo

	mu             sync.Mutex
	rng            *rand.Rand
	seed           uint64 // effective seed: cfg.Seed, or clock-derived when zero
	sessionStarted map[agent.Spec]bool
	mergeMu        sync.Mutex // serializes merges into the main tree

	loopMu    sync.Mutex
	loopCount int

	// pending is what the current loop has not started yet. A soft stop hands
	// it to the successor, so a reload never re-runs reviews that already ran
	// in the interrupted loop.
	pendingMu sync.Mutex
	pending   []string
	// resume is the queue handed over by a previous process; it replaces the
	// first loop's schedule.
	resume []string

	// soft is set when the run should end at the next quiescent point: a hot
	// reload waiting for in-flight reviews, or a runtime budget that expired.
	soft atomic.Bool
}

// RequestStop asks the runner to finish the reviews already in flight and then
// return. Unlike canceling the context, it never kills a running agent: the
// reviews now running finish normally, including their commit and merge.
func (r *Runner) RequestStop() { r.soft.Store(true) }

// Pending is what the current loop had not started when the runner stopped.
// Handing it to a successor lets a hot reload finish the loop rather than
// starting it over.
func (r *Runner) Pending() []string {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return append([]string(nil), r.pending...)
}

// setPending records the not-yet-started reviews of the current loop.
func (r *Runner) setPending(names []string) {
	r.pendingMu.Lock()
	r.pending = append(r.pending[:0], names...)
	r.pendingMu.Unlock()
}

// takeNext pops the next review from the pending queue.
func (r *Runner) takeNext() (string, bool) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if len(r.pending) == 0 {
		return "", false
	}
	next := r.pending[0]
	r.pending = r.pending[1:]
	return next, true
}

// New prepares a runner. It opens the repository (if any) and validates the
// preconditions for the requested concurrency.
func New(ctx context.Context, cfg Config, bus *Bus) (*Runner, error) {
	if len(cfg.Agents) == 0 {
		return nil, errors.New("no agents to run reviews with")
	}
	if len(cfg.Reviews) == 0 {
		return nil, errors.New("no reviews scheduled")
	}
	if cfg.Jobs < 1 {
		cfg.Jobs = 1
	}
	start := cfg.Started
	if start.IsZero() {
		start = time.Now()
	}
	seed := seedOrClock(cfg.Seed)
	r := &Runner{
		cfg:            cfg,
		bus:            bus,
		st:             &Stats{Start: start},
		repo:           gitx.Open(cfg.Dir),
		rng:            rand.New(rand.NewSource(int64(seed))),
		sessionStarted: map[agent.Spec]bool{},
		resume:         append([]string(nil), cfg.ResumeQueue...),
	}
	r.seed = seed
	if cfg.Jobs > 1 {
		if err := r.prepareWorktreeMode(ctx); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Stats exposes the accumulated results.
func (r *Runner) Stats() *Stats { return r.st }

// seedOrClock returns the configured seed, or one derived from the clock when
// unset, so production keeps its random shuffle while a seeded run replays it.
func seedOrClock(seed uint64) uint64 {
	if seed != 0 {
		return seed
	}
	return uint64(time.Now().UnixNano())
}

// Loops is the number of completed loops.
func (r *Runner) Loops() int {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	return r.loopCount
}

// prepareWorktreeMode enforces what isolated parallel reviews require: a git
// repository and a clean tree. Concurrent agents in one working tree corrupt
// each other, and a worktree is cut from a commit, so uncommitted work would
// be invisible to every review and then collide with the merges.
func (r *Runner) prepareWorktreeMode(ctx context.Context) error {
	if !gitx.Available() {
		return errors.New("--jobs > 1 needs git: each review runs in its own worktree")
	}
	if !r.repo.HasBaseline() {
		return fmt.Errorf("--jobs > 1 needs a git repository with at least one commit: %s", r.cfg.Dir)
	}
	clean, err := r.repo.IsClean(ctx, r.cfg.OwnArtifacts)
	if err != nil {
		return fmt.Errorf("cannot read git status in %s: %w", r.cfg.Dir, err)
	}
	if !clean {
		return errors.New("--jobs > 1 needs a clean working tree: commit or stash your changes first, " +
			"or run without --jobs to review the tree in place")
	}
	r.repo.PruneWorktrees(ctx)
	r.repo.ExcludeWorktreeRoot(ctx)
	return nil
}

func (r *Runner) log(format string, args ...any) {
	r.bus.Publish(Event{Kind: EvLog, Dir: r.cfg.Dir, Text: fmt.Sprintf(format, args...)})
}

// pickAgent samples an agent from the pool, skipping any the caller excluded
// (a previous attempt at the same review).
func (r *Runner) pickAgent(exclude map[agent.Spec]bool) agent.Spec {
	pool := make([]agent.Spec, 0, len(r.cfg.Agents))
	for _, a := range r.cfg.Agents {
		if !exclude[a] {
			pool = append(pool, a)
		}
	}
	if len(pool) == 0 {
		pool = r.cfg.Agents
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return pool[r.rng.Intn(len(pool))]
}

// schedule returns this loop's review order and arms the pending queue. The
// first call consumes a resume queue handed over by a previous process.
func (r *Runner) schedule() []string {
	if len(r.resume) > 0 {
		order := r.resume
		r.resume = nil
		r.setPending(order)
		return order
	}
	order := append([]string(nil), r.cfg.Reviews...)
	r.mu.Lock()
	r.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	r.mu.Unlock()
	r.setPending(order)
	return order
}

// Run executes loops until the context is canceled or a limit is reached.
func (r *Runner) Run(ctx context.Context) {
	r.bus.Publish(Event{
		Kind: EvRunStart, Dir: r.cfg.Dir, Version: r.cfg.Version,
		Agents: AgentLabels(r.cfg.Agents), Total: len(r.cfg.Reviews),
		Seed: r.seed,
	})
	defer r.bus.Publish(Event{Kind: EvRunEnd, Dir: r.cfg.Dir, Loop: r.Loops()})
	if r.cfg.Jobs > 1 {
		defer r.repo.CleanWorktreeRoot()
	}

	for {
		if ctx.Err() != nil || r.soft.Load() {
			return
		}
		if r.budgetExhausted() {
			r.log("Runtime budget exhausted, finishing up")
			return
		}
		loopNo := r.Loops() + 1
		r.bus.Publish(Event{Kind: EvLoopStart, Dir: r.cfg.Dir, Loop: loopNo, Total: len(r.cfg.Reviews)})

		start := time.Now()
		before, haveBefore := r.sample(ctx)

		var completed bool
		if r.cfg.Jobs > 1 {
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
			Elapsed: time.Since(start).Seconds(),
		}
		if after, ok := r.sample(ctx); ok && haveBefore {
			ins, del := delta(before, after)
			ev.Ins, ev.Del = new(ins), new(del)
		}
		r.bus.Publish(ev)

		if r.cfg.MaxLoops > 0 && loops >= r.cfg.MaxLoops {
			return
		}
	}
}

func (r *Runner) budgetExhausted() bool {
	return r.cfg.Runtime > 0 && time.Since(r.st.Start) >= r.cfg.Runtime
}

// runLoopSequential reviews the working tree in place, one review at a time.
// This is the original's behavior, and the only mode that can review
// uncommitted work.
func (r *Runner) runLoopSequential(ctx context.Context, loopNo int) bool {
	r.schedule()
	for {
		if ctx.Err() != nil || r.soft.Load() {
			return false
		}
		if r.budgetExhausted() {
			r.log("Runtime budget exhausted, finishing up")
			return false
		}
		review, ok := r.takeNext()
		if !ok {
			break
		}
		res := r.runReview(ctx, review, loopNo, nil)
		r.st.Add(res)
		if ctx.Err() != nil && res.Status == StatusInterrupted {
			return false
		}
		if r.cfg.Commit || r.cfg.Push {
			r.runCommitStep(ctx)
		}
	}
	return true
}

// runLoopParallel runs up to Jobs reviews at once, each in its own worktree on
// its own branch, then merges them back one at a time.
func (r *Runner) runLoopParallel(ctx context.Context, loopNo int) bool {
	base, err := r.repo.Tip(ctx, "HEAD")
	if err != nil {
		r.log("Cannot read HEAD, falling back to sequential: %v", err)
		return r.runLoopSequential(ctx, loopNo)
	}
	branch := r.repo.CurrentBranch(ctx)
	if branch == "" {
		r.log("Detached HEAD: merges would be lost, falling back to sequential")
		return r.runLoopSequential(ctx, loopNo)
	}

	sem := make(chan struct{}, r.cfg.Jobs)
	var wg sync.WaitGroup
	r.schedule()
	for i := 0; ; i++ {
		if ctx.Err() != nil || r.soft.Load() || r.budgetExhausted() {
			break
		}
		review, ok := r.takeNext()
		if !ok {
			break
		}
		wg.Add(1)
		go func(i int, review string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			r.st.Add(r.runIsolated(ctx, review, loopNo, i, base))
		}(i, review)
	}
	wg.Wait()

	if len(r.Pending()) > 0 {
		return false // stopped early: this loop is unfinished
	}
	if r.cfg.Commit || r.cfg.Push {
		// All lanes have drained and merged: the tree is quiescent, which is
		// the only safe moment to hand it to a commit agent.
		r.runCommitStep(ctx)
	}
	return ctx.Err() == nil
}

// runIsolated runs one review in a private worktree and merges its commit.
func (r *Runner) runIsolated(ctx context.Context, review string, loopNo, idx int, base string) Result {
	tag := fmt.Sprintf("%s-l%d-%02d", r.cfg.RunID, loopNo, idx)
	wt, err := r.repo.AddWorktree(ctx, review, tag, base)
	if err != nil {
		r.log("Cannot create worktree for %s: %v", review, err)
		return Result{Review: review, Agent: r.pickAgent(nil), Status: StatusSkipped}
	}
	removed := false
	// The checkout is disposable once its commit exists; the branch is what
	// carries the work. Removing it early also frees the branch, which git
	// refuses to delete while a worktree has it checked out.
	release := func() {
		if removed {
			return
		}
		removed = true
		if err := wt.Remove(context.WithoutCancel(ctx)); err != nil {
			r.log("Cannot remove worktree for %s: %v", review, err)
		}
	}
	defer release()

	res := r.runReview(ctx, review, loopNo, wt)
	res.Branch = wt.Branch
	// A worktree that produced nothing keeps no branch: it still points at
	// base, and leaving it behind would litter the repo with one empty branch
	// per failed review. The checkout must go first; git refuses to delete a
	// branch its worktree still has checked out.
	discard := func() {
		release()
		r.repo.DeleteBranch(context.WithoutCancel(ctx), wt.Branch)
		res.Branch = ""
	}
	if res.Status != StatusOK {
		discard()
		return res
	}

	msg := fmt.Sprintf("%s: automated review fixes\n\nRun %s, agent %s.",
		review, r.cfg.RunID, res.Agent.Label())
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), msg)
	if err != nil {
		r.log("Cannot commit %s worktree: %v", review, err)
		res.Status = StatusFail
		discard()
		return res
	}
	if !changed {
		res.Ins, res.Del, res.HaveLines = 0, 0, true
		discard()
		return res
	}
	if ins, del, ok := r.repo.DiffStat(ctx, wt.Dir, base, "HEAD"); ok {
		res.Ins, res.Del, res.HaveLines = ins, del, true
	}
	release()

	// One merge at a time: git allows one per worktree, and a conflicting
	// merge must be aborted before the next one starts.
	r.mergeMu.Lock()
	mr := r.repo.Merge(context.WithoutCancel(ctx), wt.Branch,
		fmt.Sprintf("Merge %s from gauntlet run %s", review, r.cfg.RunID))
	r.mergeMu.Unlock()

	switch {
	case mr.Merged:
		r.repo.DeleteBranch(context.WithoutCancel(ctx), wt.Branch)
		r.log("Merged %s%s", review, linesNote(res))
		r.bus.Publish(Event{
			Kind: EvMerge, Dir: r.cfg.Dir, Review: review, Loop: loopNo,
			Branch: wt.Branch, Status: StatusOK,
			Ins: new(res.Ins), Del: new(res.Del),
		})
	default:
		// The branch survives on purpose: dropping it would throw away the
		// entire review, silently.
		res.Status = StatusConflict
		r.log("MERGE CONFLICT: %s kept on branch %s (%s)", review, wt.Branch, mr.Detail)
		r.bus.Publish(Event{
			Kind: EvMerge, Dir: r.cfg.Dir, Review: review, Loop: loopNo,
			Branch: wt.Branch, Status: StatusConflict, Text: mr.Detail,
		})
	}
	return res
}

// runReview dispatches one review to one agent. wt is nil for in-place runs.
func (r *Runner) runReview(ctx context.Context, review string, loopNo int, wt *gitx.Worktree) Result {
	return r.runReviewExcluding(ctx, review, loopNo, wt, map[agent.Spec]bool{})
}

// shouldResume reports whether this launch may resume the spec's previous
// session, and records that the spec has now started one.
func (r *Runner) shouldResume(spec agent.Spec, wt *gitx.Worktree) bool {
	if !r.cfg.ContinueSessions || wt != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sameTool := 0
	for _, s := range r.cfg.Agents {
		if s.Tool == spec.Tool {
			sameTool++
		}
	}
	resume := r.sessionStarted[spec] && sameTool == 1
	r.sessionStarted[spec] = true
	return resume
}

func (r *Runner) runReviewExcluding(ctx context.Context, review string, loopNo int,
	wt *gitx.Worktree, exclude map[agent.Spec]bool) Result {

	spec := r.pickAgent(exclude)
	res := Result{Review: review, Agent: spec, ExitCode: -1}

	rev, ok := r.cfg.Set.Get(review)
	if !ok {
		r.log("No such review: %s", review)
		res.Status = StatusSkipped
		return res
	}
	body, err := rev.Body()
	if err != nil {
		r.log("Cannot read prompt for %s (%v), skipping", review, err)
		res.Status = StatusSkipped
		return res
	}

	dir := r.cfg.Dir
	if wt != nil {
		dir = wt.Dir
	}

	// Session resume targets an agent CLI's most recent session in a
	// directory, not a model id. Skip it when two models of one CLI are in the
	// pool (their sessions would mix), and in worktree mode, where every
	// review runs in a different directory anyway.
	resume := r.shouldResume(spec, wt)

	text := prompt.Compose(body, r.cfg.Timeout, review, r.cfg.Yolo)
	argv, err := agent.BuildCmd(spec, text, agent.BuildOpts{
		Continue: resume,
		Binary:   r.cfg.Bin[spec.Tool],
		Stream:   r.cfg.Stream,
	})
	if err != nil {
		r.log("Cannot build command for %s: %v", spec.Label(), err)
		res.Status = StatusFail
		return res
	}

	origin := ""
	if rev.IsProject() {
		origin = " [project]"
	}
	r.log("Running %s%s with %s (timeout %s)", review, origin, spec.Label(),
		humanize.Duration(r.cfg.Timeout))
	r.bus.Publish(Event{
		Kind: EvReviewStart, Dir: r.cfg.Dir, Review: review,
		Agent: spec.Label(), Loop: loopNo,
	})

	before, haveBefore := gitx.Stats{}, false
	if wt == nil {
		r.repo.Invalidate()
		before, haveBefore = r.sample(ctx)
	}

	start := time.Now()

	// Two independent sources of live usage: what the agent prints, and what
	// it writes to its own session transcript. Whichever reports more is the
	// truth; agents that do neither report nothing at all.
	usageMu := sync.Mutex{}
	best, bestThink := 0, 0
	publishUsage := func(tokens, thinking int) {
		usageMu.Lock()
		if tokens <= best && thinking <= bestThink {
			usageMu.Unlock()
			return
		}
		best = max(best, tokens)
		bestThink = max(bestThink, thinking)
		tokens, thinking = best, bestThink
		usageMu.Unlock()
		r.bus.Publish(Event{
			Kind: EvUsage, Dir: r.cfg.Dir, Review: review,
			Agent: spec.Label(), Loop: loopNo, Tokens: tokens, Thinking: thinking,
		})
	}

	// The transcript watcher lives exactly as long as the agent does. Its
	// context is derived from the review's, so an interrupt stops it too.
	watchCtx, stopWatch := context.WithCancel(ctx)
	watcher := watchTranscript(spec.Tool, dir, start)
	go watcher.Run(watchCtx, publishUsage)

	pr := runProc(ctx, procOpts{
		Argv:           argv,
		Dir:            dir,
		Timeout:        r.cfg.Timeout,
		Raw:            r.cfg.Raw,
		MaxLinesPerSec: outputRateLimit,
		Sink:           r.outputSink(review, spec.Label()),
		// Cumulative for this review: the dashboard turns successive values
		// into a rate, and a review that never reports usage sends nothing.
		Usage:  func(u agent.Usage) { publishUsage(u.Reported(), max(u.Thinking, 0)) },
		Stream: r.cfg.Stream,
	})
	stopWatch()
	// The agent's last records are written as it exits, so the final read
	// happens here rather than on a tick that already passed.
	if out, think := watcher.Final(); out > 0 || think > 0 {
		publishUsage(out, think)
	}
	res.Elapsed = time.Since(start)
	res.ExitCode = pr.ExitCode
	usageMu.Lock()
	res.Tokens = max(pr.Usage.Reported(), best)
	res.Thinking = max(max(pr.Usage.Thinking, 0), bestThink)
	usageMu.Unlock()

	if wt == nil {
		r.repo.Invalidate()
		if after, ok := r.sample(ctx); ok && haveBefore {
			// Attribution only holds when this review was the only writer.
			if r.cfg.Jobs == 1 {
				res.Ins, res.Del = delta(before, after)
				res.HaveLines = true
			}
		}
	}

	switch {
	case pr.Err != nil:
		r.log("FAILED to launch %s for %s: %v", spec.Label(), review, pr.Err)
		res.Status = StatusFail
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec); ok {
			return retry
		}
	case pr.TimedOut:
		r.log("TIMEOUT: %s (%s) after %s", review, spec.Label(), humanize.Duration(r.cfg.Timeout))
		res.Status = StatusTimeout
	case pr.Canceled:
		r.log("Interrupted: %s (%s) after %s", review, spec.Label(), humanize.Duration(res.Elapsed))
		res.Status = StatusInterrupted
	case pr.ExitCode != 0:
		r.log("FAILED: %s (%s) after %s, exit %d", review, spec.Label(),
			humanize.Duration(res.Elapsed), pr.ExitCode)
		res.Status = StatusFail
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec); ok {
			return retry
		}
	default:
		res.Status = StatusOK
		r.log("Done: %s (%s) in %s%s", review, spec.Label(),
			humanize.Duration(res.Elapsed), linesNote(res))
	}

	ev := Event{
		Kind: EvReviewEnd, Dir: r.cfg.Dir, Review: review, Agent: spec.Label(),
		Loop: loopNo, Status: res.Status, ExitCode: new(res.ExitCode),
		Elapsed: res.Elapsed.Seconds(), Tokens: res.Tokens, Thinking: res.Thinking,
	}
	if res.HaveLines {
		ev.Ins, ev.Del = new(res.Ins), new(res.Del)
	}
	r.bus.Publish(ev)
	return res
}

// retry reruns a review on a different agent after a launch failure or a
// nonzero exit. Timeouts are deliberately not retried: the next agent would
// most likely spend the same budget for the same reason.
func (r *Runner) retry(ctx context.Context, review string, loopNo int, wt *gitx.Worktree,
	exclude map[agent.Spec]bool, failed agent.Spec) (Result, bool) {

	if ctx.Err() != nil {
		return Result{}, false
	}
	next := map[agent.Spec]bool{failed: true}
	for k := range exclude {
		next[k] = true
	}
	if len(next) >= len(r.cfg.Agents) {
		return Result{}, false
	}
	r.log("Retrying %s with another agent after %s failed", review, failed.Label())
	return r.runReviewExcluding(ctx, review, loopNo, wt, next), true
}

// outputSink forwards normalized agent lines onto the bus. --quiet drops them
// at the source, so a chatty agent costs nothing.
func (r *Runner) outputSink(review, agentLabel string) func(normalize.Line) {
	if r.cfg.Quiet {
		return nil
	}
	return func(l normalize.Line) {
		r.bus.Publish(Event{
			Kind: EvOutput, Dir: r.cfg.Dir, Review: review, Agent: agentLabel,
			Text: l.Text, LineKind: l.Kind, Repeat: l.Repeat,
		})
	}
}

// runCommitStep asks an agent to commit (and optionally push) whatever the
// reviews changed.
func (r *Runner) runCommitStep(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	dirty, err := r.repo.DirtyPaths(ctx, r.cfg.OwnArtifacts)
	if err == nil && len(dirty) == 0 {
		return // nothing to commit
	}
	if err != nil {
		r.log("Warning: could not check git status before the commit step")
	}

	spec := r.pickAgent(nil)
	action := "commit"
	if r.cfg.Push {
		action = "commit+push"
	}
	argv, err := agent.BuildCmd(spec, prompt.CommitPrompt(r.cfg.Push, r.cfg.Yolo),
		agent.BuildOpts{Binary: r.cfg.Bin[spec.Tool]})
	if err != nil {
		r.log("Cannot build %s command for %s: %v", action, spec.Label(), err)
		r.st.addCommitFail()
		return
	}

	// This launch becomes the CLI's most recent session in this directory,
	// which is what the resume flags target. Resuming it from the next review
	// would continue the commit conversation, so start that review fresh.
	r.mu.Lock()
	delete(r.sessionStarted, spec)
	r.mu.Unlock()

	r.log("Running %s step with %s", action, spec.Label())
	r.st.addCommitRun()
	timeout := min(r.cfg.Timeout, commitTimeout)
	pr := runProc(ctx, procOpts{
		Argv: argv, Dir: r.cfg.Dir, Timeout: timeout,
		Raw: r.cfg.Raw, MaxLinesPerSec: outputRateLimit,
		Sink: r.outputSink("commit", spec.Label()),
	})

	status := StatusOK
	switch {
	case pr.Err != nil:
		r.log("%s step FAILED to launch (%s): %v", action, spec.Label(), pr.Err)
		status = StatusFail
	case pr.TimedOut:
		r.log("TIMEOUT: %s step (%s) after %s", action, spec.Label(), humanize.Duration(timeout))
		status = StatusTimeout
	case pr.Canceled:
		status = StatusInterrupted
	case pr.ExitCode != 0:
		r.log("%s step FAILED (%s), exit %d", action, spec.Label(), pr.ExitCode)
		status = StatusFail
	default:
		r.log("%s step done (%s)", action, spec.Label())
	}
	if status == StatusFail || status == StatusTimeout {
		r.st.addCommitFail()
	}
	r.bus.Publish(Event{
		Kind: EvCommit, Dir: r.cfg.Dir, Agent: spec.Label(),
		Status: status, Text: action,
	})
}

// sample reads cumulative worktree line stats.
func (r *Runner) sample(ctx context.Context) (gitx.Stats, bool) {
	if r.cfg.Jobs > 1 {
		// In worktree mode the main tree only changes through merges, which
		// are attributed per review from their own commits.
		return gitx.Stats{}, false
	}
	return r.repo.Sample(ctx, r.cfg.OwnArtifacts)
}

// delta converts two cumulative samples into this review's contribution.
// Removing lines a previous review added shows as negative insertions, which
// is this review deleting them (and the reverse for restorations).
func delta(before, after gitx.Stats) (ins, del int) {
	dIns := after.Ins - before.Ins
	dDel := after.Del - before.Del
	return max(dIns, 0) + max(-dDel, 0), max(dDel, 0) + max(-dIns, 0)
}

func linesNote(res Result) string {
	if !res.HaveLines || (res.Ins == 0 && res.Del == 0) {
		return ""
	}
	return fmt.Sprintf(", +%d/-%d lines", res.Ins, res.Del)
}

// AgentLabels renders each spec as its display form.
func AgentLabels(specs []agent.Spec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Label())
	}
	return out
}

// LockPath is the per-directory lock file that keeps two runs apart.
func LockPath(dir string) string { return filepath.Join(dir, ".gauntlet.lock") }
