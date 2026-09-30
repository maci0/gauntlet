// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// One review's attempt: picking the agent, composing the prompt, running it
// under the worktree isolation the mode asked for, and deciding whether the
// result is worth another try.

package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runx"
)

// outputRateLimit bounds how many lines one normalizer admits per second.
// Above this, output is summarized instead of echoed: no dashboard and no log
// file is improved by 10k lines/s of narration. Each of an agent's two output
// streams carries its own normalizer, so the ceiling is per stream, and the
// reasoning lines of a machine-readable event are emitted around it rather
// than through it.
const outputRateLimit = 200

// runLane takes the reviews the schedule assigned to this lane and runs them
// sequentially in one persistent worktree. The directory path stays constant
// across reviews, so the agent's system prompt prefix is byte-identical and
// the provider's prompt cache hits after the first review in this lane. Which
// reviews those are is fixed by the seed, not by the order the lanes finish.
func (r *Runner) runLane(ctx context.Context, wt *gitx.Worktree, loopNo, laneIdx int) {
	for reviewIdx := 0; ; reviewIdx++ {
		if ctx.Err() != nil || r.soft.Load() || r.finish.Load() {
			return
		}
		if r.budgetExhausted() != "" {
			return
		}
		review, ok := r.takeNextFor(laneIdx)
		if !ok {
			return
		}
		// One probe per attempt this lane actually starts, and none on the way
		// out: asked first, a lane that is about to stop waits out the probe
		// anyway, so --jobs N paid for N concurrent probes on every loop's
		// final turn and again on a cancel, to learn there was nothing to stop.
		r.checkUsageLimit(ctx)
		if r.finish.Load() {
			// The probe just spent the window: this review, already taken
			// off the queue, and the rest of it must not start.
			r.dropPending()
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

	if ctx.Err() != nil {
		res := r.interrupted(review)
		r.publishReviewEnd(res, loopNo, "", "", 1)
		return res
	}

	cleanCtx := context.WithoutCancel(ctx)
	tag := fmt.Sprintf("%s-l%d-lane%d-%02d", r.cfg.RunID, loopNo, laneIdx, reviewIdx)

	// Start from the latest HEAD so the review sees work merged by other
	// lanes, not the stale tip from when the loop (or the last review) began.
	base := wt.Base()
	if tip, err := r.repo.Tip(cleanCtx, "HEAD"); err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot read HEAD before %s, using the lane's previous base: %v", review, err)
	} else if tip != "" {
		base = tip
	}

	// Switch the lane to a review-specific branch from the current tip.
	oldBranch := wt.Branch
	branch := gitx.LaneBranch(tag, gitx.BranchSlug(review))
	if err := wt.StartBranch(cleanCtx, branch, base); err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot start branch for %s in lane %d: %v", review, laneIdx, err)
		res := Result{Review: review, Agent: r.pickAgent(review, nil),
			ExitCode: -1, Status: StatusSkipped, Detail: err.Error()}
		r.publishReviewEnd(res, loopNo, "", "", 1)
		return res
	}
	if oldBranch != "" && oldBranch != branch {
		if err := r.repo.DeleteBranch(cleanCtx, oldBranch); err != nil {
			r.logReview(loopNo, laneIdx, review, "Cannot delete lane branch %s before %s: %v", oldBranch, review, err)
		}
	}

	// advance resets the lane to the current HEAD so it is ready for the next
	// review. Called on every exit path after the review branch is no longer
	// needed in the worktree (merged, discarded, or conflict-kept).
	advance := func(deleteBranch bool) {
		branchToDelete := wt.Branch
		tip := base
		if t, err := r.repo.Tip(cleanCtx, "HEAD"); err != nil {
			r.logReview(loopNo, laneIdx, review, "Cannot read HEAD after %s, advancing lane %d to its previous base: %v",
				review, laneIdx, err)
		} else if t != "" {
			tip = t
		}
		if err := wt.Advance(cleanCtx, tip); err != nil {
			r.logReview(loopNo, laneIdx, review, "Cannot advance lane %d after %s: %v", laneIdx, review, err)
		}
		if deleteBranch && branchToDelete != "" {
			if err := r.repo.DeleteBranch(cleanCtx, branchToDelete); err != nil {
				r.logReview(loopNo, laneIdx, review, "Cannot delete review branch %s for %s: %v", branchToDelete, review, err)
			}
		}
	}

	res := r.runReview(ctx, review, loopNo, laneIdx, wt)
	res.Branch = wt.Branch

	if res.Status != StatusOK {
		advance(true)
		res.Branch = ""
		return res
	}

	changes, chErr := treeChanges(context.WithoutCancel(ctx), wt.Dir)
	if chErr != nil {
		r.log("Cannot read the status of the %s worktree, so the commit subject falls back to a generic one: %v",
			review, chErr)
	}
	msg := commitSubject(res.Subject, changes)
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), msg)
	if err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot commit %s worktree: %v", review, err)
		res.Status = StatusFail
		r.bus.Publish(Event{
			Kind: EvMerge, Dir: r.cfg.Dir, Review: review, Loop: loopNo,
			Branch: wt.Branch, Status: StatusFail, Text: err.Error(),
		})
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
	// A log line is a blocking publish on the event bus, and every lane merges
	// here, so the lines the merge step produces are held back and published
	// once the lock is free. A subscriber that stalls must not park the merge
	// lock with it.
	var held []string
	if !mr.Merged && mr.Conflict && r.cfg.ResolveConflicts && ctx.Err() == nil {
		fixed, notes := r.resolveConflict(ctx, review, wt.Branch, tag, msg)
		if fixed.Merged {
			mr, resolved = fixed, true
		}
		held = append(held, notes...)
	}
	if mr.Merged && r.cfg.Push {
		held = append(held, r.pushLanded(ctx, review))
	}
	r.mergeMu.Unlock()
	for _, line := range held {
		if line != "" {
			r.bus.Publish(Event{Kind: EvLog, Dir: r.cfg.Dir, Text: line})
		}
	}

	switch {
	case mr.Merged:
		if resolved {
			r.logReview(loopNo, laneIdx, review, "Merged %s after resolving a conflict%s", review, linesNote(res))
		} else {
			r.logReview(loopNo, laneIdx, review, "Merged %s%s", review, linesNote(res))
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
			r.logReview(loopNo, laneIdx, review, "MERGE CONFLICT: %s kept on branch %s (%s)", review, wt.Branch, mr.Detail)
			r.logReview(loopNo, laneIdx, review, "To land it after resolving: %s", conflictHint(wt.Branch, msg))
		} else {
			res.Status = StatusFail
			r.logReview(loopNo, laneIdx, review, "MERGE FAILED: %s kept on branch %s (%s)", review, wt.Branch, mr.Detail)
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
func (r *Runner) runReview(ctx context.Context, review string, loopNo, laneIdx int, wt *gitx.Worktree) Result {
	if wt == nil && r.repo != nil && r.mayRetry() && gitx.Available() {
		if snap, err := r.repo.Snapshot(ctx); err != nil {
			r.logReview(loopNo, laneIdx, review, "Warning: cannot snapshot the tree before %s: %v", review, err)
		} else {
			r.retrySnap = snap
			defer func() { r.retrySnap = gitx.Snapshot{} }()
		}
	}
	return r.runReviewExcluding(ctx, review, loopNo, laneIdx, wt, map[agent.Spec]bool{}, 1, 0)
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

// runReviewExcluding launches review, skipping the agents in exclude.
// firstAttempt is the 1-based attempt number this call begins at, and attempt
// counts the retries already made on this agent. They differ once a failed
// agent is set aside: the next attempt is the next one in the run's sequence,
// not a restart of it, and both the event stream and the log have to say so.
func (r *Runner) runReviewExcluding(ctx context.Context, review string, loopNo, laneIdx int,
	wt *gitx.Worktree, exclude map[agent.Spec]bool, firstAttempt, attempt int) Result {

	spec := r.pickAgent(review, exclude)
	res := Result{Review: review, Agent: spec, ExitCode: -1}
	// The attempt this launch publishes as, and the one its outcome closes.
	number := firstAttempt + attempt

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
		r.logReview(loopNo, laneIdx, review, "No such review: %s", review)
		return r.skipped(res, loopNo, lane, "unknown name")
	}
	body, err := rev.Body()
	if err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot read prompt for %s (%v), skipping", review, err)
		return r.skipped(res, loopNo, lane, err.Error())
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
		Timeout:  r.cfg.Timeout,
		Dir:      dir,
		Now:      r.bus.Clock(),
	})
	if err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot build command for %s: %v", spec.Label(), err)
		res.Status = StatusFail
		res.Detail = err.Error()
		r.forgetSession(spec)
		if retry, ok := r.retryWithDifferentAgent(ctx, review, loopNo, laneIdx, wt, exclude, spec, firstAttempt, attempt); ok {
			return retry
		}
		r.publishReviewEnd(res, loopNo, promptSHA, lane, number)
		return res
	}

	origin := ""
	if rev.IsProject() {
		origin = " [project]"
	}
	r.logReview(loopNo, laneIdx, review, "Running %s%s with %s (timeout %s)", review, origin, spec.Label(),
		humanize.Duration(r.cfg.Timeout))
	r.bus.Publish(Event{
		Kind: EvReviewStart, Dir: r.cfg.Dir, Review: review,
		Agent: spec.Label(), Loop: loopNo, Attempt: number,
		PromptSHA: promptSHA, Branch: lane,
	})

	before, haveBefore := gitx.Stats{}, false
	if wt == nil {
		r.repo.Invalidate()
		before, haveBefore = r.sample(ctx)
	}

	start := r.now()

	// The run's token budget is read between reviews, so a launch that never
	// stops on its own is the one shape of spend no ceiling catches. A review
	// that reaches the whole budget by itself is stopped here, from the
	// provider's own counters, and starts no successor: the loop's own budget
	// check has already been passed by the time the figure arrives.
	reviewCtx, stopReview := context.WithCancel(ctx)
	defer stopReview()
	cap := newReviewCap(r.cfg.TokenBudget, stopReview)

	usage := r.watchUsage(reviewCtx, spec, dir, review, loopNo, start, cap.reading)
	defer usage.halt()

	pr := runProc(reviewCtx, procOpts{
		Argv:           argv,
		Dir:            dir,
		Timeout:        r.cfg.Timeout,
		Raw:            r.cfg.Raw,
		MaxLinesPerSec: outputRateLimit,
		Now:            r.now,
		Sink:           r.outputSink(review, spec.Label()),
		// Cumulative for this review: the dashboard turns successive values
		// into a rate, and a review that never reports usage sends nothing.
		Usage:    func(u agent.Usage) { usage.report(u.Reported(), max(u.Thinking, 0)) },
		Envelope: func(u agent.Usage) { cap.reading(u.Reported()) },
		Stream:   r.cfg.Stream,
	})
	tokens, thinking := usage.settle()
	res.Elapsed = max(r.now().Sub(start), 0)
	res.ExitCode = pr.ExitCode
	res.Subject = pr.Subject
	res.FileNotes = pr.FileNotes
	res.Tokens = max(pr.Usage.Reported(), tokens)
	res.Thinking = max(max(pr.Usage.Thinking, 0), thinking)

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

	spentTokens, overBudget := cap.spent()
	switch {
	case overBudget:
		// A runaway is not a failure of the review and not an interruption of
		// the run, and it is not retried: every attempt at the same launch
		// costs what the last one did. The reading that stopped it is the
		// ceiling, not a sum the caller has to add up.
		r.logReview(loopNo, laneIdx, review, "OVER BUDGET: %s with %s after %s: one review spent %d tokens, "+
			"the run's whole budget", review, spec.Label(),
			humanize.Duration(res.Elapsed), spentTokens)
		res.Status = StatusFail
		res.Detail = fmt.Sprintf("stopped at %d tokens, the run's token budget", spentTokens)
		r.forgetSession(spec)
	case pr.Canceled:
		r.logReview(loopNo, laneIdx, review, "Interrupted: %s (%s) after %s", review, spec.Label(), humanize.Duration(res.Elapsed))
		res.Status = StatusInterrupted
		r.forgetSession(spec)
	case pr.TimedOut:
		r.logReview(loopNo, laneIdx, review, "TIMEOUT: %s (%s) after %s", review, spec.Label(), humanize.Duration(r.cfg.Timeout))
		res.Status = StatusTimeout
		r.forgetSession(spec)
	case pr.Err != nil:
		r.logReview(loopNo, laneIdx, review, "FAILED to launch %s for %s: %v", spec.Label(), review, pr.Err)
		res.Status = StatusFail
		res.Detail = pr.Err.Error()
		r.forgetSession(spec)
		if retry, ok := r.retry(ctx, review, pr.Note, loopNo, laneIdx, wt, exclude, spec, firstAttempt, attempt); ok {
			return retry
		}
		interruptedOnCancel(ctx, &res)
	case pr.StreamErr != nil:
		// The agent ran to its own end but its output was cut short, so the
		// subject and note were parsed from a partial stream. Retrying is
		// worth a try, and saying so beats filing a cut transcript as a
		// finished review. This sits below the cancel and timeout arms
		// because those close the pipes on purpose, which a reader sees as
		// the same broken pipe.
		r.logReview(loopNo, laneIdx, review, "LOST OUTPUT: %s (%s) after %s: %v", review, spec.Label(),
			humanize.Duration(res.Elapsed), pr.StreamErr)
		res.Status = StatusFail
		res.Detail = withNote("output stream ended early", pr.StreamErr.Error())
		r.forgetSession(spec)
		if retry, ok := r.retry(ctx, review, pr.Note, loopNo, laneIdx, wt, exclude, spec, firstAttempt, attempt); ok {
			return retry
		}
		interruptedOnCancel(ctx, &res)
	case pr.ExitCode != 0:
		r.logReview(loopNo, laneIdx, review, "FAILED: %s (%s) after %s, exit %d", review, spec.Label(),
			humanize.Duration(res.Elapsed), pr.ExitCode)
		res.Status = StatusFail
		res.Detail = withNote(fmt.Sprintf("%s exited %d", spec.Label(), pr.ExitCode), pr.Note)
		r.forgetSession(spec)
		if retry, ok := r.retry(ctx, review, pr.Note, loopNo, laneIdx, wt, exclude, spec, firstAttempt, attempt); ok {
			return retry
		}
		interruptedOnCancel(ctx, &res)
	default:
		res.Status = StatusOK
		r.logReview(loopNo, laneIdx, review, "Done: %s (%s) in %s%s", review, spec.Label(),
			humanize.Duration(res.Elapsed), linesNote(res))
	}

	r.publishReviewEnd(res, loopNo, promptSHA, lane, number)
	return res
}

// skipped reports a review that never launched: a name the set does not
// carry, or a prompt that would not read. The fingerprint is empty, since no
// body was ever fingerprinted.
func (r *Runner) skipped(res Result, loopNo int, lane, detail string) Result {
	res.Status = StatusSkipped
	res.Detail = detail
	r.publishReviewEnd(res, loopNo, "", lane, 1)
	return res
}

// interruptedOnCancel downgrades a recorded failure to interrupted when the
// context ended the review. retry gives up on a canceled context as well as on
// an exhausted budget, and a cancel is what every other path records, so a
// review killed during its retry backoff is not a failure of the review.
func interruptedOnCancel(ctx context.Context, res *Result) {
	if ctx.Err() != nil {
		res.Status = StatusInterrupted
	}
}

// usageWatch tracks one review's live token usage from the two independent
// sources the runner has: what the agent prints, and what it writes to its own
// session transcript. Whichever reports more is the truth, and an agent that
// does neither reports nothing at all.
//
// The transcript watcher lives exactly as long as the agent does. Its context
// is derived from the review's, so an interrupt stops it too, and settle joins
// it before review_end: the lane key is the agent, not the review, so a tick
// published after this review has ended would be attributed to whatever that
// agent starts next.
type usageWatch struct {
	publish func(tokens, thinking int)

	reader transcriptReader
	stop   context.CancelFunc
	done   sync.WaitGroup

	mu        sync.Mutex
	best      int
	bestThink int
}

// watchUsage starts following spec's transcript, which only counts what it
// writes from since onward. onProvider is called with each reading the
// transcript itself reports, which is the provider's record rather than
// anything the model printed: the review's token cap acts on it.
func (r *Runner) watchUsage(ctx context.Context, spec agent.Spec, dir, review string,
	loopNo int, since time.Time, onProvider func(tokens int)) *usageWatch {

	w := &usageWatch{
		publish: func(tokens, thinking int) {
			r.bus.Publish(Event{
				Kind: EvUsage, Dir: r.cfg.Dir, Review: review,
				Agent: spec.Label(), Loop: loopNo, Tokens: tokens, Thinking: thinking,
			})
		},
		reader: openTranscript(spec.Tool, dir, since),
	}
	watchCtx, cancel := context.WithCancel(ctx)
	w.stop = cancel
	w.done.Go(func() {
		w.reader.Run(watchCtx, func(tokens, thinking int) {
			w.report(tokens, thinking)
			if onProvider != nil {
				onProvider(tokens)
			}
		})
	})
	return w
}

// report records a reading and publishes it only when it grew, so a repeated
// tick does not republish the same counts.
func (w *usageWatch) report(tokens, thinking int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if tokens <= w.best && thinking <= w.bestThink {
		return
	}
	w.best = max(w.best, tokens)
	w.bestThink = max(w.bestThink, thinking)
	w.publish(w.best, w.bestThink)
}

// settle ends the watcher and returns the highest counts either source saw.
// The agent's last records are written as it exits, so the final read happens
// here rather than on a tick that already passed.
func (w *usageWatch) settle() (tokens, thinking int) {
	w.halt()
	if out, think := w.reader.Final(); out > 0 || think > 0 {
		w.report(out, think)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.best, w.bestThink
}

// halt stops the watcher and waits for it. It is safe to call twice, so a
// review that returns early still joins the goroutine it started.
func (w *usageWatch) halt() {
	w.stop()
	w.done.Wait()
}

// publishReviewEnd puts one review's outcome on the bus. Every path that
// decides a status uses it, including a skip that never launched, so the
// journal and the dashboard cannot disagree with the stats.
func (r *Runner) publishReviewEnd(res Result, loopNo int, promptSHA, lane string, attempt int) {
	ev := Event{
		Kind: EvReviewEnd, Dir: r.cfg.Dir, Review: res.Review,
		Agent: res.Agent.Label(), Loop: loopNo, Status: res.Status, Attempt: attempt,
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
// wait starts long enough to matter and doubles from there. retryBaseDelay is
// a var so tests can shrink it; production always sees 5s.
var (
	retryBaseDelay = 5 * time.Second
	retryMaxDelay  = 2 * time.Minute
)

// maxBackoffDoublings is the largest attempt that can be shifted rather than
// taken as the cap: past it, base<<attempt leaves a time.Duration's range and
// the cap is the honest answer.
const maxBackoffDoublings = 32

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
// note is the agent's own last line, and it can end the same-agent attempts
// before they start: a spent quota or a rejected key is a statement about the
// account, and the same command spends its whole budget to hear it again. The
// fallback to another agent still runs, because another CLI may hold another
// account, and the operator's --usage-limit is the one that stops a run whose
// agents share one window.
func (r *Runner) retry(ctx context.Context, review, note string, loopNo, laneIdx int, wt *gitx.Worktree,
	exclude map[agent.Spec]bool, failed agent.Spec, firstAttempt, attempt int) (Result, bool) {

	if ctx.Err() != nil || r.budgetExhausted() != "" {
		return Result{}, false
	}
	if attempt < r.cfg.Retries && terminalAgentFailure(note) {
		// Say it rather than skip silently: an operator reading the log has to
		// be able to tell a decision from a bug.
		r.logReview(loopNo, laneIdx, review, "Not retrying %s with %s: it stopped for a reason another attempt cannot clear (%s)",
			review, failed.Label(), note)
		attempt = r.cfg.Retries
	}
	if attempt < r.cfg.Retries {
		delay := r.backoff(review, attempt)
		r.logReview(loopNo, laneIdx, review, "Retrying %s with %s in %s (attempt %d of %d)", review, failed.Label(),
			humanize.Duration(delay), attempt+2, r.cfg.Retries+1)
		if !r.sleep(ctx, delay) || r.windowSpent(ctx) {
			return Result{}, false
		}
		if !r.resetForRetry(ctx, review, loopNo, laneIdx, wt) {
			return Result{}, false
		}
		return r.runReviewExcluding(ctx, review, loopNo, laneIdx, wt, exclude, firstAttempt, attempt+1), true
	}
	next := map[agent.Spec]bool{failed: true}
	for k := range exclude {
		next[k] = true
	}
	if len(next) >= len(r.cfg.Agents) {
		return Result{}, false
	}
	r.logReview(loopNo, laneIdx, review, "Retrying %s with another agent after %s failed", review, failed.Label())
	if r.windowSpent(ctx) {
		return Result{}, false
	}
	if !r.resetForRetry(ctx, review, loopNo, laneIdx, wt) {
		return Result{}, false
	}
	// The failed agent is set aside, not the attempt sequence: the fallback is
	// the next try, so a reader sees one continuous run of attempts instead of
	// a second sequence starting over at one.
	return r.runReviewExcluding(ctx, review, loopNo, laneIdx, wt, next, firstAttempt+attempt+1, 0), true
}

// retryWithDifferentAgent hands the review straight to another agent, skipping
// the same-agent attempts. It is for a command that would not build: the argv
// is a pure function of the prompt and the spec, so a second build of the same
// pair fails the same way, and only another CLI's flags can change the outcome
// (a prompt over one CLI's argument limit is under another's).
func (r *Runner) retryWithDifferentAgent(ctx context.Context, review string, loopNo, laneIdx int,
	wt *gitx.Worktree, exclude map[agent.Spec]bool, failed agent.Spec, firstAttempt, attempt int) (Result, bool) {
	return r.retry(ctx, review, "", loopNo, laneIdx, wt, exclude, failed, firstAttempt, max(attempt, r.cfg.Retries))
}

// resetForRetry rewinds the checkout to what the first attempt saw before the
// next one runs. Isolated reviews reset to the worktree's base commit; in-place
// reviews restore the pre-review snapshot. It reports whether the retry may
// proceed: a checkout that cannot be restored would make every later attempt
// build on unknown state, so the review fails instead of committing something
// no rerun could produce.
func (r *Runner) resetForRetry(ctx context.Context, review string, loopNo, laneIdx int, wt *gitx.Worktree) bool {
	if wt != nil {
		if err := wt.ResetToBase(ctx); err != nil {
			r.logReview(loopNo, laneIdx, review, "Cannot restore the worktree for %s before the retry: %v", review, err)
			return false
		}
		return true
	}
	if !r.retrySnap.Valid() {
		r.logReview(loopNo, laneIdx, review, "Cannot retry %s: the tree before the first attempt was not captured", review)
		return false
	}
	if err := r.repo.Restore(ctx, r.retrySnap); err != nil {
		r.logReview(loopNo, laneIdx, review, "Cannot restore the tree for %s before the retry: %v", review, err)
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
	base := retryBaseDelay
	d := retryMaxDelay
	if attempt <= 0 {
		d = base
	} else if attempt < maxBackoffDoublings {
		if grown := base << attempt; grown > 0 && grown < retryMaxDelay {
			d = grown
		}
	}
	jitter := drawIndex64(r.seed, fmt.Sprintf("backoff\x00%s\x00%d", review, attempt),
		int64(d/2)+1)
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
//
// Credentials are stripped on the way out. An agent prints what it read: a
// provider that rejects a key says so by naming it, a shell that expands an
// environment variable prints the value, and a line the model assembled from
// the tree can carry either. normalize.Display keeps the visible characters and
// drops the escape sequences, so a line that reached the bus was safe to show
// but not necessarily safe to keep, and every subscriber of EvOutput keeps it:
// the run journal writes it to disk, where it outlives the run, and the TUI
// scrollback outlives the screen. The subject, the file notes, and every error
// string already pass through runx.RedactSecrets for the same reason, and this
// is the one place they are not: the bulk of what the agent said.
func (r *Runner) outputSink(review, agentLabel string) func(normalize.Line) {
	if r.cfg.Quiet {
		return nil
	}
	return func(l normalize.Line) {
		r.bus.Publish(Event{
			Kind: EvOutput, Dir: r.cfg.Dir, Review: review, Agent: agentLabel,
			Text: runx.RedactSecrets(l.Text), LineKind: l.Kind, Repeat: l.Repeat,
		})
	}
}

func linesNote(res Result) string {
	if !res.HaveLines || (res.Ins == 0 && res.Del == 0) {
		return ""
	}
	return fmt.Sprintf(", +%d/-%d lines", res.Ins, res.Del)
}
