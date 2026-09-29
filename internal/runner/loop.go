// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// The loop: what one pass over the schedule does, in the three shapes it takes.
// Sequential runs review in place, --jobs > 1 gives each lane a persistent
// worktree, and the merge step folds what a loop committed back into the tree
// the caller named.

package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
)

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
			ErrDirtyTree, safePathList(changes.Tracked, pathListLimit))
	}
	if n := len(changes.Untracked); n > 0 {
		r.log("%d untracked file(s) stay put and are not reviewed: %s",
			n, safePathList(changes.Untracked, pathListLimit))
	}
	// The run lock on this directory is held, so nothing under the worktree
	// root belongs to a live run: whatever is in it is scratch a previous
	// process was killed before it could remove, and each entry is a full
	// copy of the tree.
	r.repo.SweepWorktreeRoot(ctx)
	return nil
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
		key := fmt.Sprintf("shuffle\x00%d\x00%d", r.cfg.ResumeLoops+loopNo, i)
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

// queued is one unstarted review with the lane that will run it.
type queued struct {
	review string
	lane   int
}

// takeNext pops the head of the pending queue, whichever lane it belongs to.
// The single-actor loops (sequential, stacked) and the cancel drain run one
// review at a time and own the whole queue.
func (r *Runner) takeNext() (string, bool) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if len(r.pending) == 0 {
		return "", false
	}
	next := r.pending[0]
	r.pending = r.pending[1:]
	return next.review, true
}

// takeNextFor pops the first queued review assigned to lane. Lane assignment
// is a pure function of the seeded schedule (see setPending), so a run's lane
// order replays from its seed the way the schedule itself does, rather than
// following which lane's goroutine the scheduler happened to wake.
func (r *Runner) takeNextFor(lane int) (string, bool) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	for i, q := range r.pending {
		if q.lane == lane {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			return q.review, true
		}
	}
	return "", false
}

// setPending records the not-yet-started reviews of the current loop, in
// scheduled order, each tagged with the lane that will run it: position i
// goes to lane i%Jobs. Splitting the queue up front rather than letting
// whichever lane finishes first grab the next review is what makes a --jobs
// run replayable from its seed, since the review's lane names its branch and
// worktree. The cost is a lane with one slow review cannot steal the tail of
// another lane's list, so the loop ends when the slowest lane's last review
// does rather than when the last review anywhere does.
func (r *Runner) setPending(names []string) {
	jobs := max(r.cfg.Jobs, 1)
	r.pendingMu.Lock()
	r.pending = slices.Grow(r.pending[:0], len(names))[:0]
	for i, name := range names {
		r.pending = append(r.pending, queued{review: name, lane: i % jobs})
	}
	r.pendingMu.Unlock()
}

// dropPending clears the unstarted queue, for a run that is ending on purpose
// and has no successor to hand it to.
func (r *Runner) dropPending() {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.pending = nil
}

func (r *Runner) rememberStackHead(branch, tip string, published int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stackHead = branch
	r.stackHeadTip = tip
	r.stackPublished = published
}

// stackPass is the 1-based stacked pass a loop number belongs to, counting
// loops a predecessor already finished so branch names stay stable across a
// hot reload.
func (r *Runner) stackPass(loopNo int) int { return r.cfg.ResumeLoops + loopNo }

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

// toolsFor splits one review's helpers into what this machine has and what it
// does not, from the paths resolveTools probed once for the whole schedule.
func (r *Runner) toolsFor(review string) prompt.Tools {
	have, missing := agent.SplitTools(agent.ToolsFor(review), r.tools)
	return prompt.Tools{Have: have, Missing: missing}
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

func (r *Runner) log(format string, args ...any) {
	r.bus.Publish(Event{Kind: EvLog, Dir: r.cfg.Dir, Text: fmt.Sprintf(format, args...)})
}

// now is the runner's clock: the bus's injected Now, or wall time.
func (r *Runner) now() time.Time { return r.bus.now() }

// sleep is the runner's wait: the bus's injected Sleep, or a real timer.
func (r *Runner) sleep(ctx context.Context, d time.Duration) bool { return r.bus.sleep(ctx, d) }

// budgetExhausted names the run budget that has run out, or "" while reviews
// may still start. --runtime is the wall clock; --token-budget is the total
// tokens recorded so far, a hot reload's predecessor included, so a ceiling
// survives the exec that reloads the run. Both are checked at the same points:
// before a loop, and before a review is taken.
func (r *Runner) budgetExhausted() string {
	if r.cfg.Runtime > 0 && r.now().Sub(r.st.Start) >= r.cfg.Runtime {
		return "Runtime"
	}
	if r.cfg.TokenBudget > 0 && r.st.Tokens() >= r.cfg.TokenBudget {
		return "Token"
	}
	return ""
}

// runLoopSequential reviews the working tree in place, one review at a time.
// This is the original's behavior, and the only mode that can review
// uncommitted work.
func (r *Runner) runLoopSequential(ctx context.Context, loopNo int) bool {
	for range r.schedule(loopNo) {
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
		if why := r.budgetExhausted(); why != "" {
			// What this loop already ran still commits and merges below, the
			// same as the graceful quit: the budget stops what starts next, not
			// what has already run.
			r.log("%s budget exhausted, finishing up", why)
			r.budgetStop.Store(true)
			r.dropPending()
			break
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
			r.removeLanes(ctx, lanes[:i])
			return r.runLoopSequential(ctx, loopNo)
		}
		lanes[i] = wt
	}
	defer func() {
		r.removeLanes(ctx, lanes)
		if ctx.Err() != nil {
			// A cancel can race advance(), leaving review branches that
			// no lane cleaned up. Conflict branches are not worth
			// preserving from a cancelled run. Lane branches live under
			// gauntlet/<tag>/lane-*; the older gauntlet/<tag>-lane* shape
			// is swept too, since its separator is "-" where the current
			// one is "/".
			cleanCtx := context.WithoutCancel(ctx)
			for _, pattern := range []string{"gauntlet/" + tag + "/lane-*", "gauntlet/" + tag + "-lane*"} {
				if err := r.repo.DeleteBranchesMatching(cleanCtx, pattern); err != nil {
					r.log("Cannot sweep lane branches: %v", err)
				}
			}
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
		if why := r.budgetExhausted(); why == "" {
			return false
		} else {
			// The lanes stopped because a budget ran out, not because they
			// were interrupted: what they ran still commits and merges.
			r.log("%s budget exhausted, finishing up", why)
			r.budgetStop.Store(true)
		}
	}
	if r.finish.Load() || r.budgetStop.Load() {
		r.dropPending()
	}
	if r.cfg.Commit || r.cfg.Push {
		r.runCommitStep(ctx)
	}
	r.runMergeStep(ctx, loopNo)
	return ctx.Err() == nil
}

// removeLanes takes down the lane worktrees it is given and the branches they
// checked out. A nil entry is a lane whose worktree was never created.
func (r *Runner) removeLanes(ctx context.Context, lanes []*gitx.Worktree) {
	cleanCtx := context.WithoutCancel(ctx)
	for _, wt := range lanes {
		if wt == nil {
			continue
		}
		if err := wt.Remove(cleanCtx); err != nil {
			r.log("Cannot remove lane worktree %s: %v", wt.Dir, err)
		}
		if wt.Branch != "" {
			if err := r.repo.DeleteBranch(cleanCtx, wt.Branch); err != nil {
				r.log("Cannot delete lane branch %s: %v", wt.Branch, err)
			}
		}
	}
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
		res := r.interrupted(review)
		r.st.Add(res)
		r.publishReviewEnd(res, loopNo, "", "", 1)
	}
}

// interrupted is the result of a review that was taken from the queue but
// never launched, because the run was canceled first. The agent is drawn the
// same way a real launch would draw it, so a replay names the same one.
func (r *Runner) interrupted(review string) Result {
	return Result{Review: review, Agent: r.pickAgent(review, nil),
		ExitCode: -1, Status: StatusInterrupted}
}

// pushLanded pushes what just landed on this branch and returns the line to
// publish, "" when there is nothing to say. A failure is counted, never fatal:
// the work is committed, and the next review's push (or the commit step)
// carries it. It runs under the merge lock, so the caller publishes the line
// once that lock is free.
func (r *Runner) pushLanded(ctx context.Context, review string) string {
	if err := r.repo.Push(context.WithoutCancel(ctx)); err != nil {
		r.st.addCommitFail()
		return fmt.Sprintf("Push after %s failed: %v", review, err)
	}
	return fmt.Sprintf("Pushed %s", review)
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
//
// Both samples are diffs against the same baseline, so a removal of a
// previously added line moves Ins down and Del up by the same count: those
// two readings are one event. Additions and restorations are independent
// (adding lines and restoring deleted ones both raise Ins), so those add;
// a drop in Ins is not independent of a rise in Del, and Del already
// counts it.
func delta(before, after gitx.Stats) (ins, del int) {
	dIns := after.Ins - before.Ins
	dDel := after.Del - before.Del
	ins = max(dIns, 0) + max(-dDel, 0)
	if dDel > 0 {
		return ins, dDel
	}
	return ins, max(-dIns, 0)
}
