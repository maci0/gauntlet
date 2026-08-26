// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runner schedules reviews onto agents and reports what happened.
package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
)

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

	// Retries is how many times a review is rerun on the same agent after a
	// launch failure or a nonzero exit, with a growing delay between tries.
	// The failures worth waiting out are transient: a rate limit, a dropped
	// connection, a CLI that died before it started. Zero keeps only the
	// fallback to a different agent.
	Retries int
	// RetryDelay is the first wait between retries; it doubles from there.
	// Zero means retryBaseDelay, which is what a run wants; tests set it low
	// so a retry path costs milliseconds.
	RetryDelay time.Duration

	Commit bool
	Push   bool
	// MergeInto is a branch this run's work is merged into after each loop,
	// once the commit step has left the tree clean. Empty leaves the work
	// where the reviews put it, on the branch that was checked out.
	MergeInto string
	Yolo      bool
	Raw       bool
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

// Runner executes Config until it is stopped, the loop limit is reached, or
// the runtime budget runs out.
type Runner struct {
	cfg Config
	bus *Bus
	st  *Stats

	repo *gitx.Repo

	mu             sync.Mutex // guards sessionStarted
	seed           uint64     // effective seed: cfg.Seed, or clock-derived when zero
	sessionStarted map[agent.Spec]bool
	// tools is where this machine's helper binaries resolved to, probed once
	// at startup rather than per review; SplitTools treats an empty or
	// absent entry as missing.
	tools   map[string]string
	mergeMu sync.Mutex // serializes merges into the main tree

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
	// finish is the graceful quit: like soft, no new review starts, but what
	// is in flight is drained and its work is committed, pushed, and merged
	// before the run ends. A reload hands its unfinished reviews to a
	// successor; a graceful quit has no successor, so it must not leave the
	// loop's output uncommitted.
	finish atomic.Bool
}

// RequestStop asks the runner to finish the reviews already in flight and then
// return. Unlike canceling the context, it never kills a running agent: the
// reviews now running finish normally, including their commit and merge.
func (r *Runner) RequestStop() { r.soft.Store(true) }

// RequestFinish asks the runner to stop starting reviews and end the run once
// the ones already running are done, their results committed, pushed, and
// merged as the flags ask. Reviews not yet started are dropped, not deferred:
// nothing follows this run.
func (r *Runner) RequestFinish() { r.finish.Store(true) }

// finishing reports whether a graceful quit is under way, for a screen that
// should say so.
func (r *Runner) finishing() bool { return r.finish.Load() }

// dropPending clears the unstarted queue, for a run that is ending on purpose
// and has no successor to hand it to.
func (r *Runner) dropPending() {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.pending = nil
}

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

// ErrDirtyTree is the one worktree precondition a caller can do something
// about: the work is there, it simply is not committed. Callers that can ask
// a person offer the commit step rather than stopping.
var ErrDirtyTree = errors.New("--jobs > 1 needs a clean working tree")

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
		sessionStarted: map[agent.Spec]bool{},
		resume:         append([]string(nil), cfg.ResumeQueue...),
		tools:          resolveTools(cfg.Reviews),
	}
	r.seed = seed
	// Whatever this run does, it writes a lock in the reviewed tree and may
	// write worktrees under it. Neither is the project's, so neither should
	// ever show up in its git status: the exclusion is local to the clone,
	// which is the right place for one tool's scratch.
	r.repo.ExcludeOwnArtifacts(ctx)
	if cfg.Jobs > 1 {
		if err := r.prepareWorktreeMode(ctx); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// resolveTools probes, in one parallel pass, every helper binary the
// scheduled reviews might reach for. The answer goes into each prompt, so an
// agent knows what is here before it starts guessing.
func resolveTools(reviews []string) map[string]string {
	var entries []string
	for _, review := range reviews {
		entries = append(entries, agent.ToolsFor(review)...)
	}
	return agent.ResolveMany(agent.ToolBins(entries))
}

// toolsFor splits one review's helpers into what this machine has and what it
// does not, from the paths resolveTools probed once for the whole schedule.
func (r *Runner) toolsFor(review string) prompt.Tools {
	have, missing := agent.SplitTools(agent.ToolsFor(review), r.tools)
	return prompt.Tools{Have: have, Missing: missing}
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

// safePaths renders worktree paths for an error or log line. They come from
// git status against a possibly hostile tree: a file name may carry escape,
// control, or bidi characters that survive unquoteC's decoding, so anything
// headed for a message that is not sanitized downstream (a returned error the
// caller prints raw) is stripped here. Matching and own-artifact comparison
// still see the exact paths; only display text passes through this.
func safePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = normalize.Sanitize(p)
	}
	return out
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
	// Only tracked modifications block: a review works from a commit, so
	// uncommitted edits to files git knows about would be invisible to it and
	// then collide with its merge. An untracked file is in nobody's way; it is
	// simply not reviewed, which is worth saying once rather than refusing to
	// run over.
	changes, err := r.repo.Status(ctx, r.cfg.OwnArtifacts)
	if err != nil {
		return fmt.Errorf("cannot read git status in %s: %w", r.cfg.Dir, err)
	}
	if len(changes.Tracked) > 0 {
		// The paths are named in an error the caller may print raw, so they
		// are sanitized here rather than left to every consumer.
		return fmt.Errorf("%w: commit or stash your changes first, "+
			"or run without --jobs to review the tree in place (%s)",
			ErrDirtyTree, humanize.List(safePaths(changes.Tracked), 3))
	}
	if n := len(changes.Untracked); n > 0 {
		r.log("%d untracked file(s) stay put and are not reviewed: %s",
			n, humanize.List(changes.Untracked, 3))
	}
	r.repo.PruneWorktrees(ctx)
	return nil
}

func (r *Runner) log(format string, args ...any) {
	r.bus.Publish(Event{Kind: EvLog, Dir: r.cfg.Dir, Text: fmt.Sprintf(format, args...)})
}

// pickAgent samples an agent from the pool, skipping any the caller excluded
// (a previous attempt at the same review). The sample is keyed by review
// name, so lanes running concurrently cannot change each other's draws and a
// seeded run replays regardless of scheduling.
func (r *Runner) pickAgent(review string, exclude map[agent.Spec]bool) agent.Spec {
	pool := make([]agent.Spec, 0, len(r.cfg.Agents))
	for _, a := range r.cfg.Agents {
		if !exclude[a] {
			pool = append(pool, a)
		}
	}
	if len(pool) == 0 {
		pool = r.cfg.Agents
	}
	return pool[drawIndex(r.seed, "agent\x00"+review, len(pool))]
}

// schedule returns loop loopNo's review order and arms the pending queue. The
// first call consumes a resume queue handed over by a previous process.
//
// The shuffle is a keyed draw per Fisher-Yates step, not a random stream: loop
// 3's order is the same pure function of the seed whether this process has
// scheduled two loops before it or inherited the run from a hot reload, which
// is what lets the successor continue the interrupted schedule exactly.
func (r *Runner) schedule(loopNo int) []string {
	if len(r.resume) > 0 {
		order := r.resume
		r.resume = nil
		r.setPending(order)
		return order
	}
	order := append([]string(nil), r.cfg.Reviews...)
	for i := len(order) - 1; i > 0; i-- {
		key := fmt.Sprintf("shuffle\x00%d\x00%d", loopNo, i)
		j := drawIndex(r.seed, key, i+1)
		order[i], order[j] = order[j], order[i]
	}
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

		if r.finish.Load() {
			return // the graceful quit's last loop is done
		}
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
	r.schedule(loopNo)
	for {
		if ctx.Err() != nil || r.soft.Load() {
			return false
		}
		if r.finish.Load() {
			// No new review starts, and what was never started is dropped:
			// there is no successor to hand it to. What this loop did still
			// commits and merges below.
			r.dropPending()
			break
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
	r.runMergeStep(ctx, loopNo)
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
	r.schedule(loopNo)
	// A lane is claimed before a review comes off the queue. Popping first
	// and waiting for a lane second would empty the queue at dispatch speed
	// while every review still waited minutes for its turn, and a stop could
	// no longer tell never-started work from in-flight work: a hot reload
	// would report nothing pending and lose the loop's remainder, and a
	// graceful quit or an expired budget would keep launching everything
	// already popped. Claiming first leaves the unstarted half on the queue,
	// where the stop handling below already knows what to do with it.
loop:
	for i := 0; ; i++ {
		if ctx.Err() != nil || r.soft.Load() || r.finish.Load() {
			break
		}
		if r.budgetExhausted() {
			r.log("Runtime budget exhausted, finishing up")
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		if ctx.Err() != nil {
			// Cancel landed while both select cases were ready: give the lane
			// back rather than dispatch into a dead context.
			<-sem
			break loop
		}
		review, ok := r.takeNext()
		if !ok {
			<-sem
			break
		}
		wg.Add(1)
		go func(i int, review string) {
			defer wg.Done()
			defer func() { <-sem }()
			r.st.Add(r.runIsolated(ctx, review, loopNo, i, base))
		}(i, review)
	}
	wg.Wait()

	// A hard cancel strands whatever never got a lane: no successor will
	// ever start it, so each is recorded as interrupted rather than let
	// vanish from the stats, the summary, and the journal. Soft stops and
	// budget stops leave the queue alone instead: the handoff or the caller
	// decides, exactly as the sequential loop does.
	if ctx.Err() != nil {
		r.abandonQueue(loopNo)
	}

	if len(r.Pending()) > 0 && !r.finish.Load() {
		return false // stopped early: this loop is unfinished
	}
	if r.finish.Load() {
		// A graceful quit drops what it never started: there is no successor
		// to hand the queue to, and the work that did run must still land.
		r.dropPending()
	}
	if r.cfg.Commit || r.cfg.Push {
		// All lanes have drained and merged: the tree is quiescent, which is
		// the only safe moment to hand it to a commit agent.
		r.runCommitStep(ctx)
	}
	r.runMergeStep(ctx, loopNo)
	return ctx.Err() == nil
}

// abandonQueue records every queued review a hard cancel will never start.
// There is no successor to hand them to, so letting them vanish would drop
// them from the stats, the summary, and the journal alike. Soft stops never
// call this: their queue is the handoff.
func (r *Runner) abandonQueue(loopNo int) {
	for {
		review, ok := r.takeNext()
		if !ok {
			return
		}
		res := Result{Review: review, Agent: r.pickAgent(review, nil), ExitCode: -1,
			Status: StatusInterrupted}
		r.st.Add(res)
		r.bus.Publish(Event{
			Kind: EvReviewEnd, Dir: r.cfg.Dir, Review: review,
			Agent: res.Agent.Label(), Loop: loopNo, Status: StatusInterrupted,
			ExitCode: new(res.ExitCode),
		})
	}
}

// commitSubject is what the history will say about a review's change: what
// the agent called it, or a plain fallback naming the review's subject area.
// Neither mentions this tool, a run id, or a model: the commit is the
// project's, not the machinery's.
func commitSubject(fromAgent, review string) string {
	if fromAgent != "" {
		return fromAgent
	}
	return fmt.Sprintf("chore(%s): apply review findings", strings.TrimSuffix(review, "-review"))
}

// pushLanded publishes what just landed on this branch. A failure is logged
// and counted, never fatal: the work is committed, and the next review's push
// (or the commit step) carries it.
func (r *Runner) pushLanded(ctx context.Context, review string) {
	if err := r.repo.Push(context.WithoutCancel(ctx)); err != nil {
		r.log("Push after %s failed: %v", review, err)
		r.st.addCommitFail()
		return
	}
	r.log("Pushed %s", review)
}

// runIsolated runs one review in a private worktree and merges its commit.
func (r *Runner) runIsolated(ctx context.Context, review string, loopNo, idx int, base string) Result {
	tag := fmt.Sprintf("%s-l%d-%02d", r.cfg.RunID, loopNo, idx)
	wt, err := r.repo.AddWorktree(ctx, review, tag, base)
	if err != nil {
		r.log("Cannot create worktree for %s: %v", review, err)
		return Result{Review: review, Agent: r.pickAgent(review, nil), Status: StatusSkipped}
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

	msg := commitSubject(res.Subject, review)
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
	mr := r.repo.Merge(context.WithoutCancel(ctx), wt.Branch, commitSubject(res.Subject, review))
	if mr.Merged && r.cfg.Push {
		// Push while the merge lock is held: two pushes racing on one branch
		// is one rejection and one wasted round trip. A run of forty reviews
		// publishes as it goes rather than holding everything to the end.
		r.pushLanded(ctx, review)
	}
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
	return r.runReviewExcluding(ctx, review, loopNo, wt, map[agent.Spec]bool{}, 0)
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
	wt *gitx.Worktree, exclude map[agent.Spec]bool, attempt int) Result {

	spec := r.pickAgent(review, exclude)
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

	text := prompt.Compose(body, r.cfg.Timeout, review, r.cfg.Yolo, r.toolsFor(review))
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
		Agent: spec.Label(), Loop: loopNo, Attempt: attempt + 1,
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
	res.Subject = pr.Subject
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
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec, attempt); ok {
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
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec, attempt); ok {
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

// Retry delays. A rate limit or a dropped connection clears in seconds, so the
// wait starts long enough to matter and doubles from there.
const (
	retryBaseDelay = 5 * time.Second
	retryMaxDelay  = 2 * time.Minute
)

// retry reruns a review after a launch failure or a nonzero exit: first on the
// same agent, backing off between tries, then on a different one. Timeouts are
// deliberately not retried: the next attempt would most likely spend the same
// budget for the same reason.
//
// A retried review starts from the same state as the first attempt: in an
// isolated review the worktree is reset to its base commit, so the failed
// attempt's half-applied fixes cannot leak into what the retry sees, commits,
// or merges. Sequential reviews retry in place, where the tree belongs to the
// user and is not the runner's to rewind.
//
// ponytail: the backoff is per review, not per agent. Reviews rate-limited by
// one provider each wait on their own; a shared per-agent gate is the upgrade
// if that turns out to matter.
func (r *Runner) retry(ctx context.Context, review string, loopNo int, wt *gitx.Worktree,
	exclude map[agent.Spec]bool, failed agent.Spec, attempt int) (Result, bool) {

	if ctx.Err() != nil {
		return Result{}, false
	}
	if attempt < r.cfg.Retries {
		delay := r.backoff(review, attempt)
		r.log("Retrying %s with %s in %s (attempt %d of %d)", review, failed.Label(),
			humanize.Duration(delay), attempt+2, r.cfg.Retries+1)
		if !sleepCtx(ctx, delay) {
			return Result{}, false
		}
		if !r.resetForRetry(ctx, review, wt) {
			return Result{}, false
		}
		return r.runReviewExcluding(ctx, review, loopNo, wt, exclude, attempt+1), true
	}
	next := map[agent.Spec]bool{failed: true}
	for k := range exclude {
		next[k] = true
	}
	if len(next) >= len(r.cfg.Agents) {
		return Result{}, false
	}
	r.log("Retrying %s with another agent after %s failed", review, failed.Label())
	if !r.resetForRetry(ctx, review, wt) {
		return Result{}, false
	}
	return r.runReviewExcluding(ctx, review, loopNo, wt, next, 0), true
}

// resetForRetry rewinds an isolated review's checkout to its base before the
// next attempt runs. It reports whether the retry may proceed: a checkout that
// cannot be restored would make every later attempt build on unknown state,
// so the review fails instead of committing something no rerun could produce.
func (r *Runner) resetForRetry(ctx context.Context, review string, wt *gitx.Worktree) bool {
	if wt == nil {
		return true
	}
	if err := wt.ResetToBase(ctx); err != nil {
		r.log("Cannot restore the worktree for %s before the retry: %v", review, err)
		return false
	}
	return true
}

// backoff is the wait before the next attempt: doubling, capped, and jittered
// so reviews that failed together do not come back together. The jitter is
// keyed by review and attempt, so a seeded run replays it exactly no matter
// how many lanes are retrying at once.
func (r *Runner) backoff(review string, attempt int) time.Duration {
	base := r.cfg.RetryDelay
	if base <= 0 {
		base = retryBaseDelay
	}
	d := retryMaxDelay
	if attempt < 32 {
		if grown := base << attempt; grown > 0 && grown < retryMaxDelay {
			d = grown
		}
	}
	jitter := drawIndex(r.seed, fmt.Sprintf("backoff\x00%s\x00%d", review, attempt),
		int(d/2)+1)
	return d/2 + time.Duration(jitter)
}

// sleepCtx waits for d, and reports false if the run is stopping instead.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
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

// runMergeStep merges what this loop produced into --merge-into. It runs
// after the commit step, because only committed work can be merged: anything
// still dirty in the tree is not in the branch, and merging then would report
// a success that moved none of it.
//
// The merge happens in a scratch checkout of the target, so the branch the
// reviews ran on stays checked out and the run stays watchable. A conflict
// aborts and keeps everything where it is: the work is on this branch, and a
// human resolves it.
func (r *Runner) runMergeStep(ctx context.Context, loopNo int) {
	if r.cfg.MergeInto == "" || ctx.Err() != nil || r.repo == nil {
		return
	}
	from := r.repo.CurrentBranch(ctx)
	if from == "" {
		r.log("Not merging into %s: this tree is on a detached HEAD", r.cfg.MergeInto)
		return
	}
	if from == r.cfg.MergeInto {
		return // the work is already there
	}
	if dirty, err := r.repo.DirtyPaths(ctx, r.cfg.OwnArtifacts); err == nil && len(dirty) > 0 {
		r.log("Not merging into %s: %d uncommitted path(s) would be left behind",
			r.cfg.MergeInto, len(dirty))
		r.st.addCommitFail()
		return
	}

	r.mergeMu.Lock()
	mr := r.repo.MergeInto(context.WithoutCancel(ctx), r.cfg.MergeInto, from,
		fmt.Sprintf("Merge %s from gauntlet run %s", from, r.cfg.RunID))
	r.mergeMu.Unlock()

	ev := Event{
		Kind: EvMerge, Dir: r.cfg.Dir, Loop: loopNo,
		Review: from, Branch: r.cfg.MergeInto, Text: mr.Detail,
	}
	switch {
	case mr.Merged:
		r.log("Merged %s into %s", from, r.cfg.MergeInto)
		ev.Status = StatusOK
	case mr.Conflict:
		r.log("MERGE CONFLICT: %s does not merge into %s (%s)", from, r.cfg.MergeInto, mr.Detail)
		ev.Status = StatusConflict
		r.st.addCommitFail()
	default:
		r.log("Cannot merge %s into %s: %s", from, r.cfg.MergeInto, mr.Detail)
		ev.Status = StatusFail
		r.st.addCommitFail()
	}
	r.bus.Publish(ev)
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
func LockPath(dir string) string { return filepath.Join(dir, gitx.LockName) }
