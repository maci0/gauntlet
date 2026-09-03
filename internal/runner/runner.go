// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runner schedules reviews onto agents and reports what happened.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/ghx"
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
	// RetryDelay is the first wait between retries; it doubles from there.
	// Zero means retryBaseDelay, which is what a run wants; tests set it low
	// so a retry path costs milliseconds.
	RetryDelay time.Duration

	Commit bool
	Push   bool
	// StackedPRs runs the configured review order in one isolated worktree,
	// publishing each changed review as a child PR of the previous one.
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
	// stackReadRemote is where stack branches are read back from (ls-remote,
	// fetch): the push URL when it differs from the fetch URL, else the
	// remote name. Pushes keep using the remote name.
	stackReadRemote string

	mu             sync.Mutex // guards sessionStarted
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
	// is in flight is drained and its work is committed, published, or merged
	// before the run ends. A reload hands its unfinished reviews to a
	// successor; a graceful quit has no successor, so it must not leave the
	// loop's output uncommitted.
	finish atomic.Bool
	// usageProbeFailed keeps a broken usage probe from narrating once per
	// review. The first failure is worth a line; the rest are the same line.
	usageProbeFailed atomic.Bool
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

// StackDirtyError asks the caller to surface the isolation boundary before a
// stacked run proceeds. Unlike parallel worktree mode, dirty files are not a
// technical blocker: they stay in the original checkout and the stack starts
// from the selected remote branch. They are still important enough that an
// interactive CLI must not silently omit them from review.
type StackDirtyError struct {
	Dir       string
	Remote    string
	Base      string
	Tracked   []string
	Untracked []string
}

func (e *StackDirtyError) Error() string {
	if e == nil {
		return "stacked PR confirmation required"
	}
	return fmt.Sprintf("%s has %d uncommitted file(s) that stacked PRs would exclude (%s)",
		normalize.Sanitize(e.Dir), len(e.Tracked)+len(e.Untracked),
		humanize.List(e.DisplayPaths(), 3))
}

// DisplayPaths returns sanitized tracked paths followed by sanitized
// untracked paths, ready for terminal output. The exact paths remain in the
// fields for programmatic handling; only their display form is altered.
func (e *StackDirtyError) DisplayPaths() []string {
	if e == nil {
		return nil
	}
	paths := append(append([]string(nil), e.Tracked...), e.Untracked...)
	return safePaths(paths)
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
		cfg.MaxLoops = 1
		if cfg.PushRemote == "" {
			cfg.PushRemote = "origin"
		}
		// Stack mode never shuffles: its one pass walks cfg.Reviews in
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
	seed := seedOrClock(cfg.Seed, bus.now)
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

// now is the runner's clock: the bus's injected Now, or wall time.
func (r *Runner) now() time.Time { return r.bus.now() }

// seedOrClock returns the configured seed, or one derived from the clock when
// unset, so production keeps its random shuffle while a seeded run replays it.
// now nil means time.Now. A derived seed is never 0: 0 means "unset" and would
// be re-derived on replay instead of reproducing the original draws.
func seedOrClock(seed uint64, now func() time.Time) uint64 {
	if seed != 0 {
		return seed
	}
	if now == nil {
		now = time.Now
	}
	n := uint64(now().UnixNano())
	if n == 0 {
		n = 1
	}
	return n
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
	// The cap cuts after the shuffle, which always draws over the full list:
	// capping first would change the draw keys and break seeded replay, and
	// cutting here is what makes different loops sample different reviews.
	if n := r.cfg.MaxReviews; n > 0 && n < len(order) {
		order = order[:n]
	}
	r.setPending(order)
	return order
}

// perLoop is how many reviews one loop schedules: the full list, or the
// --max-reviews cap when it is smaller.
func (r *Runner) perLoop() int {
	if n := r.cfg.MaxReviews; n > 0 && n < len(r.cfg.Reviews) {
		return n
	}
	return len(r.cfg.Reviews)
}

// Run executes loops until the context is canceled or a limit is reached.
func (r *Runner) Run(ctx context.Context) {
	r.bus.Publish(Event{
		Kind: EvRunStart, Dir: r.cfg.Dir, Version: r.cfg.Version,
		Agents: agent.Labels(r.cfg.Agents), Total: r.perLoop(),
		Seed: r.seed,
	})
	defer r.bus.Publish(Event{Kind: EvRunEnd, Dir: r.cfg.Dir, Loop: r.Loops()})
	if r.cfg.Jobs > 1 || r.cfg.StackedPRs {
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
		r.bus.Publish(Event{Kind: EvLoopStart, Dir: r.cfg.Dir, Loop: loopNo, Total: r.perLoop()})

		start := r.now()
		before, haveBefore := r.sample(ctx)
		beforeIns, beforeDel := 0, 0
		if r.cfg.StackedPRs {
			beforeIns, beforeDel, _, _, _, _ = r.st.Totals()
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
			Elapsed: r.now().Sub(start).Seconds(),
		}
		if r.cfg.StackedPRs {
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
		if r.cfg.MaxLoops > 0 && loops >= r.cfg.MaxLoops {
			return
		}
	}
}

func (r *Runner) budgetExhausted() bool {
	return r.cfg.Runtime > 0 && r.now().Sub(r.st.Start) >= r.cfg.Runtime
}

// runLoopSequential reviews the working tree in place, one review at a time.
// This is the original's behavior, and the only mode that can review
// uncommitted work.
func (r *Runner) runLoopSequential(ctx context.Context, loopNo int) bool {
	r.schedule(loopNo)
	for {
		if r.soft.Load() {
			return false
		}
		if ctx.Err() != nil {
			// A hard cancel strands whatever never started: no successor will
			// ever start it, so each is recorded as interrupted rather than
			// let vanish from the stats, the summary, and the journal, exactly
			// as the parallel loop records its stranded lanes.
			r.abandonQueue(loopNo)
			return false
		}
		r.checkUsageLimit(ctx)
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
		if ctx.Err() != nil {
			// The cancel landed while this review ran or right after it; the
			// rest of the loop's queue dies with this process, so it is
			// recorded, not dropped.
			r.abandonQueue(loopNo)
			return false
		}
		if r.cfg.Commit || r.cfg.Push {
			r.runCommitStep(ctx)
		}
	}
	r.runMergeStep(ctx, loopNo)
	return true
}

// runLoopParallel runs up to Jobs reviews at once using persistent lane
// worktrees. Each lane is a stable directory reused across reviews, so agent
// system prompts (which embed the working directory) share a prefix and hit
// the provider's prompt cache after the first review in each lane.
func (r *Runner) runLoopParallel(ctx context.Context, loopNo int) bool {
	base, err := r.repo.Tip(ctx, "HEAD")
	if err != nil {
		r.log("Cannot read HEAD, falling back to sequential: %v", err)
		return r.runLoopSequential(ctx, loopNo)
	}
	branch, err := r.repo.CurrentBranch(ctx)
	if err != nil {
		r.log("Cannot read the current branch, falling back to sequential: %v", err)
		return r.runLoopSequential(ctx, loopNo)
	}
	if branch == "" {
		r.log("Detached HEAD: merges would be lost, falling back to sequential")
		return r.runLoopSequential(ctx, loopNo)
	}

	tag := fmt.Sprintf("%s-l%d", r.cfg.RunID, loopNo)
	lanes := make([]*gitx.Worktree, r.cfg.Jobs)
	for i := range lanes {
		wt, err := r.repo.AddWorktree(ctx, fmt.Sprintf("lane-%d", i), tag, base)
		if err != nil {
			r.log("Cannot create lane %d: %v", i, err)
			for j := range i {
				if lanes[j] != nil {
					if err := lanes[j].Remove(context.WithoutCancel(ctx)); err != nil {
						r.log("Cannot remove lane %d: %v", j, err)
					}
					r.repo.DeleteBranch(context.WithoutCancel(ctx), lanes[j].Branch)
				}
			}
			return r.runLoopSequential(ctx, loopNo)
		}
		lanes[i] = wt
	}
	defer func() {
		cleanCtx := context.WithoutCancel(ctx)
		for _, wt := range lanes {
			if wt == nil {
				continue
			}
			if err := wt.Remove(cleanCtx); err != nil {
				r.log("Cannot remove lane worktree %s: %v", wt.Dir, err)
			}
			if wt.Branch != "" {
				r.repo.DeleteBranch(cleanCtx, wt.Branch)
			}
		}
		if ctx.Err() != nil {
			// A cancel can race advance(), leaving review branches that
			// no lane cleaned up. Conflict branches are not worth
			// preserving from a cancelled run.
			r.repo.DeleteBranchesMatching(cleanCtx, "gauntlet/"+tag+"-lane*")
		}
	}()

	r.schedule(loopNo)
	var wg sync.WaitGroup
	for i, wt := range lanes {
		wg.Go(func() {
			r.runLane(ctx, wt, loopNo, i)
		})
	}
	wg.Wait()

	if ctx.Err() != nil {
		r.abandonQueue(loopNo)
	}

	if len(r.Pending()) > 0 && !r.finish.Load() {
		return false
	}
	if r.finish.Load() {
		r.dropPending()
	}
	if r.cfg.Commit || r.cfg.Push {
		r.runCommitStep(ctx)
	}
	r.runMergeStep(ctx, loopNo)
	return ctx.Err() == nil
}

// abandonQueue records every queued review a hard cancel will never start,
// in either loop. There is no successor to hand them to, so letting them
// vanish would drop them from the stats, the summary, and the journal alike.
// Soft stops never call this: their queue is the handoff.
func (r *Runner) abandonQueue(loopNo int) {
	for {
		review, ok := r.takeNext()
		if !ok {
			return
		}
		res := Result{Review: review, Agent: r.pickAgent(review, nil), ExitCode: -1,
			Status: StatusInterrupted}
		r.st.Add(res)
		r.publishReviewEnd(res, loopNo, "", "")
	}
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

// runLane pulls reviews from the shared queue and runs them sequentially in
// one persistent worktree. The directory path stays constant across reviews,
// so the agent's system prompt prefix is byte-identical and the provider's
// prompt cache hits after the first review in this lane.
func (r *Runner) runLane(ctx context.Context, wt *gitx.Worktree, loopNo, laneIdx int) {
	for reviewIdx := 0; ; reviewIdx++ {
		// Each lane asks before it starts another review, so a limit reached
		// mid-loop is noticed by whichever lane frees up first rather than
		// waiting for the whole loop to drain. Once the answer has tripped the
		// graceful quit, the probe stops running.
		r.checkUsageLimit(ctx)
		if ctx.Err() != nil || r.soft.Load() || r.finish.Load() {
			return
		}
		if r.budgetExhausted() {
			return
		}
		review, ok := r.takeNext()
		if !ok {
			return
		}
		r.st.Add(r.runLaneReview(ctx, wt, review, loopNo, laneIdx, reviewIdx))
	}
}

// runLaneReview runs one review in a persistent lane worktree, commits the
// result, and merges it into the main tree. Between reviews the lane advances
// to the current HEAD so the next review sees all prior work.
func (r *Runner) runLaneReview(ctx context.Context, wt *gitx.Worktree, review string,
	loopNo, laneIdx, reviewIdx int) Result {

	tag := fmt.Sprintf("%s-l%d-lane%d-%02d", r.cfg.RunID, loopNo, laneIdx, reviewIdx)

	// Start from the latest HEAD so the review sees work merged by other
	// lanes, not the stale tip from when the loop (or the last review) began.
	base := wt.Base()
	if tip, err := r.repo.Tip(ctx, "HEAD"); err != nil {
		r.log("Cannot read HEAD before %s, using the lane's previous base: %v", review, err)
	} else if tip != "" {
		base = tip
	}

	// Switch the lane to a review-specific branch from the current tip.
	oldBranch := wt.Branch
	branch := fmt.Sprintf("gauntlet/%s/%s", tag, gitx.BranchSlug(review))
	if err := wt.StartBranch(ctx, branch, base); err != nil {
		r.log("Cannot start branch for %s in lane %d: %v", review, laneIdx, err)
		res := Result{Review: review, Agent: r.pickAgent(review, nil),
			ExitCode: -1, Status: StatusSkipped, Detail: err.Error()}
		r.publishReviewEnd(res, loopNo, "", "")
		return res
	}
	if oldBranch != "" && oldBranch != branch {
		r.repo.DeleteBranch(context.WithoutCancel(ctx), oldBranch)
	}

	// advance resets the lane to the current HEAD so it is ready for the next
	// review. Called on every exit path after the review branch is no longer
	// needed in the worktree (merged, discarded, or conflict-kept).
	advance := func(deleteBranch bool) {
		branchToDelete := wt.Branch
		tip := base
		if t, err := r.repo.Tip(ctx, "HEAD"); err != nil {
			r.log("Cannot read HEAD after %s, advancing lane %d to its previous base: %v",
				review, laneIdx, err)
		} else if t != "" {
			tip = t
		}
		if err := wt.Advance(context.WithoutCancel(ctx), tip); err != nil {
			r.log("Cannot advance lane %d after %s: %v", laneIdx, review, err)
		}
		if deleteBranch && branchToDelete != "" {
			r.repo.DeleteBranch(context.WithoutCancel(ctx), branchToDelete)
		}
	}

	res := r.runReview(ctx, review, loopNo, wt)
	res.Branch = wt.Branch

	if res.Status != StatusOK {
		advance(true)
		res.Branch = ""
		return res
	}

	msg := commitSubject(res.Subject, treeChanges(context.WithoutCancel(ctx), wt.Dir))
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), msg)
	if err != nil {
		r.log("Cannot commit %s worktree: %v", review, err)
		res.Status = StatusFail
		advance(true)
		res.Branch = ""
		return res
	}
	if !changed {
		res.Ins, res.Del, res.HaveLines = 0, 0, true
		advance(true)
		res.Branch = ""
		return res
	}
	if ins, del, ok := r.repo.DiffStat(ctx, wt.Dir, base, "HEAD"); ok {
		res.Ins, res.Del, res.HaveLines = ins, del, true
	}

	r.mergeMu.Lock()
	mr := r.repo.Merge(context.WithoutCancel(ctx), wt.Branch, msg)
	resolved := false
	if !mr.Merged && mr.Conflict && r.cfg.ResolveConflicts && ctx.Err() == nil {
		if fixed := r.resolveConflict(ctx, review, wt.Branch, tag, msg); fixed.Merged {
			mr, resolved = fixed, true
		}
	}
	if mr.Merged && r.cfg.Push {
		r.pushLanded(ctx, review)
	}
	r.mergeMu.Unlock()

	switch {
	case mr.Merged:
		if resolved {
			r.log("Merged %s after resolving a conflict%s", review, linesNote(res))
		} else {
			r.log("Merged %s%s", review, linesNote(res))
		}
		ev := Event{
			Kind: EvMerge, Dir: r.cfg.Dir, Review: review, Loop: loopNo,
			Branch: wt.Branch, Status: StatusOK,
		}
		if res.HaveLines {
			ev.Ins, ev.Del = new(res.Ins), new(res.Del)
		}
		r.bus.Publish(ev)
		advance(true)
	default:
		if mr.Conflict {
			res.Status = StatusConflict
			r.log("MERGE CONFLICT: %s kept on branch %s (%s)", review, wt.Branch, mr.Detail)
			r.log("To land it after resolving: %s", conflictHint(wt.Branch, msg))
		} else {
			res.Status = StatusFail
			r.log("MERGE FAILED: %s kept on branch %s (%s)", review, wt.Branch, mr.Detail)
		}
		r.bus.Publish(Event{
			Kind: EvMerge, Dir: r.cfg.Dir, Review: review, Loop: loopNo,
			Branch: wt.Branch, Status: res.Status, Text: mr.Detail,
		})
		// Branch kept for human inspection; advance the lane without deleting it.
		advance(false)
	}
	return res
}

// runReview dispatches one review to one agent. wt is nil for in-place runs.
func (r *Runner) runReview(ctx context.Context, review string, loopNo int, wt *gitx.Worktree) Result {
	if wt == nil && r.repo != nil && r.mayRetry() && gitx.Available() {
		if snap, err := r.repo.Snapshot(ctx); err != nil {
			r.log("Warning: cannot snapshot the tree before %s: %v", review, err)
		} else {
			r.retrySnap = snap
			defer func() { r.retrySnap = gitx.Snapshot{} }()
		}
	}
	return r.runReviewExcluding(ctx, review, loopNo, wt, map[agent.Spec]bool{}, 0)
}

// mayRetry reports whether a failed launch or nonzero exit can run again:
// either another attempt on the same agent, or a different agent in the pool.
func (r *Runner) mayRetry() bool {
	return r.cfg.Retries > 0 || len(r.cfg.Agents) > 1
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

	dir := r.cfg.Dir
	lane := ""
	if wt != nil {
		dir = wt.Dir
		// The lane's review branch is the journal's record of which worktree
		// this review ran in: with --jobs > 1 the lane a review lands in is
		// whichever goroutine won the queue race, so only the branch captured
		// here tells a replay which working directory the agent saw.
		lane = wt.Branch
	}

	rev, ok := r.cfg.Set.Get(review)
	if !ok {
		r.log("No such review: %s", review)
		res.Status = StatusSkipped
		res.Detail = "unknown name"
		r.publishReviewEnd(res, loopNo, "", lane)
		return res
	}
	body, err := rev.Body()
	if err != nil {
		r.log("Cannot read prompt for %s (%v), skipping", review, err)
		res.Status = StatusSkipped
		res.Detail = err.Error()
		r.publishReviewEnd(res, loopNo, "", lane)
		return res
	}
	// Recorded on both of this attempt's events: a prompt edited mid-run makes
	// its later attempts carry a different fingerprint, and the journal says so.
	promptSHA := prompt.Fingerprint(body)

	// Session resume targets an agent CLI's most recent session in a
	// directory, not a model id. Skip it when two models of one CLI are in the
	// pool (their sessions would mix), and in worktree mode, where every
	// review runs in a different directory anyway.
	resume := r.shouldResume(spec, wt)

	text := prompt.Compose(body, r.cfg.Timeout, review, r.cfg.Yolo, r.toolsFor(review), r.cfg.Paths)
	argv, err := agent.BuildCmd(spec, text, agent.BuildOpts{
		Continue: resume,
		Binary:   r.cfg.Bin[spec.Tool],
		Stream:   r.cfg.Stream,
	})
	if err != nil {
		r.log("Cannot build command for %s: %v", spec.Label(), err)
		res.Status = StatusFail
		res.Detail = err.Error()
		r.publishReviewEnd(res, loopNo, promptSHA, lane)
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
		PromptSHA: promptSHA, Branch: lane,
	})

	before, haveBefore := gitx.Stats{}, false
	if wt == nil {
		r.repo.Invalidate()
		before, haveBefore = r.sample(ctx)
	}

	start := r.now()

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
	// Run is joined before review_end: the lane key is the agent, not the
	// review, so a tick published after this review has ended is attributed
	// to whatever that agent starts next.
	watchCtx, stopWatch := context.WithCancel(ctx)
	watcher := watchTranscript(spec.Tool, dir, start)
	var watchDone sync.WaitGroup
	watchDone.Go(func() { watcher.Run(watchCtx, publishUsage) })

	pr := runProc(ctx, procOpts{
		Argv:           argv,
		Dir:            dir,
		Timeout:        r.cfg.Timeout,
		Raw:            r.cfg.Raw,
		MaxLinesPerSec: outputRateLimit,
		Now:            r.now,
		Sink:           r.outputSink(review, spec.Label()),
		// Cumulative for this review: the dashboard turns successive values
		// into a rate, and a review that never reports usage sends nothing.
		Usage:  func(u agent.Usage) { publishUsage(u.Reported(), max(u.Thinking, 0)) },
		Stream: r.cfg.Stream,
	})
	stopWatch()
	watchDone.Wait()
	// The agent's last records are written as it exits, so the final read
	// happens here rather than on a tick that already passed.
	if out, think := watcher.Final(); out > 0 || think > 0 {
		publishUsage(out, think)
	}
	res.Elapsed = r.now().Sub(start)
	res.ExitCode = pr.ExitCode
	res.Subject = pr.Subject
	res.FileNotes = pr.FileNotes
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
		res.Detail = pr.Err.Error()
		r.forgetSession(spec)
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec, attempt); ok {
			return retry
		}
	case pr.TimedOut:
		r.log("TIMEOUT: %s (%s) after %s", review, spec.Label(), humanize.Duration(r.cfg.Timeout))
		res.Status = StatusTimeout
		r.forgetSession(spec)
	case pr.Canceled:
		r.log("Interrupted: %s (%s) after %s", review, spec.Label(), humanize.Duration(res.Elapsed))
		res.Status = StatusInterrupted
		r.forgetSession(spec)
	case pr.ExitCode != 0:
		r.log("FAILED: %s (%s) after %s, exit %d", review, spec.Label(),
			humanize.Duration(res.Elapsed), pr.ExitCode)
		res.Status = StatusFail
		r.forgetSession(spec)
		if retry, ok := r.retry(ctx, review, loopNo, wt, exclude, spec, attempt); ok {
			return retry
		}
	default:
		res.Status = StatusOK
		r.log("Done: %s (%s) in %s%s", review, spec.Label(),
			humanize.Duration(res.Elapsed), linesNote(res))
	}

	r.publishReviewEnd(res, loopNo, promptSHA, lane)
	return res
}

// publishReviewEnd puts one review's outcome on the bus. Every path that
// decides a status uses it, including a skip that never launched, so the
// journal and the dashboard cannot disagree with the stats.
func (r *Runner) publishReviewEnd(res Result, loopNo int, promptSHA, lane string) {
	ev := Event{
		Kind: EvReviewEnd, Dir: r.cfg.Dir, Review: res.Review,
		Agent: res.Agent.Label(), Loop: loopNo, Status: res.Status,
		ExitCode: new(res.ExitCode), Elapsed: res.Elapsed.Seconds(),
		Tokens: res.Tokens, Thinking: res.Thinking,
		PromptSHA: promptSHA, Branch: lane, Text: res.Detail,
	}
	if res.HaveLines {
		ev.Ins, ev.Del = new(res.Ins), new(res.Del)
	}
	r.bus.Publish(ev)
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
// isolated review the worktree is reset to its base commit; in place, the
// working tree is restored to the snapshot taken before the first attempt,
// including the user's own uncommitted files. Either way the failed attempt's
// half-applied fixes cannot leak into what the retry sees, commits, or merges.
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

// resetForRetry rewinds the checkout to what the first attempt saw before the
// next one runs. Isolated reviews reset to the worktree's base commit; in-place
// reviews restore the pre-review snapshot. It reports whether the retry may
// proceed: a checkout that cannot be restored would make every later attempt
// build on unknown state, so the review fails instead of committing something
// no rerun could produce.
func (r *Runner) resetForRetry(ctx context.Context, review string, wt *gitx.Worktree) bool {
	if wt != nil {
		if err := wt.ResetToBase(ctx); err != nil {
			r.log("Cannot restore the worktree for %s before the retry: %v", review, err)
			return false
		}
		return true
	}
	if !r.retrySnap.Valid() {
		return true
	}
	if err := r.repo.Restore(ctx, r.retrySnap); err != nil {
		r.log("Cannot restore the tree for %s before the retry: %v", review, err)
		return false
	}
	return true
}

// forgetSession drops spec from the resume set so a later launch of the same
// agent starts a new session. A failed, timed-out, or interrupted attempt must
// not be continued: --continue-sessions is for context between successful
// reviews, and retrying inside a failed conversation would replay its effects.
func (r *Runner) forgetSession(spec agent.Spec) {
	r.mu.Lock()
	delete(r.sessionStarted, spec)
	r.mu.Unlock()
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
// after the commit step, because only committed work can be merged: tracked
// files still dirty are not in the branch, and merging then would report a
// success that moved none of them. Untracked files stay in the original tree
// either way: the merge is a scratch checkout of committed work, the same
// rule --jobs uses when it lets them sit.
//
// The merge happens in a scratch checkout of the target, so the branch the
// reviews ran on stays checked out and the run stays watchable. A conflict
// aborts and keeps everything where it is: the work is on this branch, and a
// human resolves it.
func (r *Runner) runMergeStep(ctx context.Context, loopNo int) {
	if r.cfg.MergeInto == "" || ctx.Err() != nil || r.repo == nil {
		return
	}
	from, err := r.repo.CurrentBranch(ctx)
	if err != nil {
		r.log("Not merging into %s: cannot read the current branch: %v", r.cfg.MergeInto, err)
		r.st.addCommitFail()
		return
	}
	if from == "" {
		r.log("Not merging into %s: this tree is on a detached HEAD", r.cfg.MergeInto)
		r.st.addCommitFail()
		return
	}
	if from == r.cfg.MergeInto {
		return // the work is already there
	}
	changes, err := r.repo.Status(ctx, r.cfg.OwnArtifacts)
	if err != nil {
		r.log("Not merging into %s: cannot read git status: %v", r.cfg.MergeInto, err)
		r.st.addCommitFail()
		return
	}
	if len(changes.Tracked) > 0 {
		r.log("Not merging into %s: %d uncommitted path(s) would be left behind",
			r.cfg.MergeInto, len(changes.Tracked))
		r.st.addCommitFail()
		return
	}

	r.mergeMu.Lock()
	mr := r.repo.MergeInto(context.WithoutCancel(ctx), r.cfg.MergeInto, from,
		fmt.Sprintf("Merge branch '%s'", from))
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
