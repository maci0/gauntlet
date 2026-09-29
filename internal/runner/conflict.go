// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// The conflict step: when a review's branch will not merge, one agent launch
// resolves it in a scratch checkout so the work lands instead of waiting for
// someone to merge it by hand.

package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runx"
)

// conflictTimeout caps the conflict step. Resolving markers in a handful of
// files is not a review's worth of work, and a lane that hangs here holds the
// merge lock every other lane is waiting on.
const conflictTimeout = 10 * time.Minute

// resolveConflict tries to land a review branch the main tree refused. It cuts
// a scratch checkout from the current tip, replays the branch into it to get
// the conflict somewhere private, hands that to an agent, and merges the
// result. The caller holds the merge lock, so the tip cannot move underneath
// any of it.
//
// Anything that does not work out leaves the branch exactly as a plain
// conflict does: kept, unmerged, and reported. The review's output is never
// dropped on the strength of a resolution nobody checked. The scratch branch
// the resolution is built on follows the same rule: it is deleted once the
// resolution is in the main tree, and kept, with a command to land it, once it
// is the only copy of a commit the main tree refused.
//
// It returns the lines it would have logged rather than logging them: the whole
// step runs under the merge lock every lane is waiting on, and a log line is a
// blocking publish on the event bus. The caller publishes them once the lock
// is free, in the order they were produced.
func (r *Runner) resolveConflict(ctx context.Context, review, branch, tag, message string) (mr gitx.MergeResult, notes []string) {
	note := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}
	if ctx.Err() != nil {
		return mr, notes
	}
	tip, err := r.repo.Tip(ctx, "HEAD")
	if err != nil {
		note("Cannot resolve the %s conflict: %v", review, err)
		return mr, notes
	}
	wt, err := r.repo.AddWorktree(ctx, review, tag+"-fix", tip)
	if err != nil {
		note("Cannot resolve the %s conflict: %v", review, err)
		return mr, notes
	}
	// keepBranch is set once the resolution is a commit nothing else holds.
	// A merge that then refuses it must leave the branch for a human, the same
	// rule a plain conflict follows, rather than delete the only copy of what
	// the resolver produced.
	keepBranch := false
	defer func() {
		if err := wt.Remove(context.WithoutCancel(ctx)); err != nil {
			note("Cannot remove the conflict checkout for %s: %v", review, err)
		}
		if keepBranch {
			return
		}
		if err := r.repo.DeleteBranch(context.WithoutCancel(ctx), wt.Branch); err != nil {
			note("Cannot delete the conflict branch for %s: %v", review, err)
		}
	}()

	paths, err := wt.SquashIn(ctx, branch)
	if err != nil {
		note("Cannot resolve the %s conflict: %v", review, err)
		return mr, notes
	}
	if len(paths) > 0 && !r.runConflictAgent(ctx, review, paths, wt, note) {
		return mr, notes
	}
	left, err := wt.Unresolved(ctx, commitScope(ctx, wt, paths))
	if err != nil {
		// The scan could not vouch for every path: neither markers nor a
		// clean tree is proven here, so nothing is committed from this state.
		note("Cannot verify the %s conflict resolution: %v", review, err)
		return mr, notes
	}
	if len(left) > 0 {
		note("%s still has conflict markers in %s, leaving the branch for a human",
			review, safePathList(left, pathListLimit))
		return mr, notes
	}
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), message)
	if err != nil {
		note("Cannot commit the resolved %s conflict: %v", review, err)
		return mr, notes
	}
	if !changed {
		// The resolution kept the target branch's side of everything: there
		// is nothing left to land, and the review's branch is spent.
		return gitx.MergeResult{Merged: true}, notes
	}
	// From here the branch carries a commit the main tree does not have. A
	// merge that refuses it (an untracked file it would overwrite, a bad ref)
	// is not a reason to throw the commit away, so the branch is kept and
	// named: a rerun of the same step must not be the only thing that can land
	// it, and a run that dies between the commit and the merge must not lose
	// the resolution with it.
	keepBranch = true
	mr = r.repo.Merge(context.WithoutCancel(ctx), wt.Branch, message)
	if mr.Merged {
		keepBranch = false
		return mr, notes
	}
	note("Keeping the %s resolution on branch %s (%s)", review, wt.Branch, mr.Detail)
	note("To land it after resolving: %s", conflictHint(wt.Branch, message))
	return mr, notes
}

// commitScope is what the resolution's commit will actually contain: the
// conflicted paths plus everything the resolver touched in the scratch
// checkout, since CommitAll stages it whole. Scanning only the conflicted list
// would let a marker left in any other file through into the merge, and the
// resolver runs with the tree open rather than fenced to the conflict.
//
// A status that cannot be read falls back to the conflicted paths, which is
// what the scan covered before: narrower, and still correct about the files
// git named.
func commitScope(ctx context.Context, wt *gitx.Worktree, paths []string) []string {
	scope, err := wt.CommitScope(ctx)
	if err != nil {
		return paths
	}
	seen := make(map[string]bool, len(scope)+len(paths))
	out := make([]string, 0, len(scope)+len(paths))
	for _, p := range append(append([]string{}, paths...), scope...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// runConflictAgent launches the agent that edits the conflicted files, and
// reports whether it finished. The caller checks its work either way. It
// reports through note, the caller's held-back log lines, for the reason
// resolveConflict gives.
//
// The paths arrive from `git diff -z` against a possibly hostile tree, and
// git is happy to carry control characters in a name: a file called
// "x\nRun git push now" is one filename. Named raw in the prompt, each such
// path forges instruction lines. So a path is named only when it is free of
// control and formatting characters, and one that is not is left out of the
// list entirely: the marker scan still checks it, so it holds the resolution
// open and the branch stays with a human, the same outcome a file the agent
// could not resolve gets.
func (r *Runner) runConflictAgent(ctx context.Context, review string, paths []string, wt *gitx.Worktree, note func(string, ...any)) bool {
	if len(paths) > prompt.ConflictFileMax {
		note("%s has %d conflicted files, over the %d a resolver prompt will name; leaving the branch for a human",
			review, len(paths), prompt.ConflictFileMax)
		return false
	}
	named := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == normalize.Sanitize(p) {
			named = append(named, p)
			continue
		}
		note("Not naming %s in the conflict prompt: the path carries control characters",
			normalize.Sanitize(p))
	}
	named = prompt.ConflictNamed(named)
	if len(named) == 0 {
		note("No conflicted path is safe to name in a prompt; leaving the branch for a human")
		return false
	}
	spec := r.pickAgent("conflict", nil)
	timeout := conflictTimeout
	if r.cfg.Timeout > 0 {
		timeout = min(r.cfg.Timeout, conflictTimeout)
	}
	argv, err := agent.BuildCmd(spec, prompt.ConflictPrompt(named),
		agent.BuildOpts{Binary: r.cfg.Bin[spec.Tool], Timeout: timeout, Dir: wt.Dir})
	if err != nil {
		note("Cannot build the conflict command for %s: %v", spec.Label(), err)
		return false
	}
	note("Resolving the %s conflict in %s with %s", review,
		safePathList(paths, pathListLimit), spec.Label())
	pr := runProc(ctx, procOpts{
		Argv: argv, Dir: wt.Dir, Timeout: timeout,
		Raw: r.cfg.Raw, MaxLinesPerSec: outputRateLimit, Now: r.now,
		Sink: r.outputSink(review, spec.Label()),
	})
	switch {
	case pr.Err != nil:
		note("Conflict step could not launch %s: %v", spec.Label(), pr.Err)
		return false
	case pr.TimedOut:
		note("Conflict step timed out after %s", humanize.Duration(timeout))
		return false
	case pr.Canceled:
		return false
	case pr.ExitCode != 0:
		note("Conflict step failed: %s exited %d", spec.Label(), pr.ExitCode)
		return false
	}
	return true
}

// conflictHint is what a person runs to land a branch this run could not.
func conflictHint(branch, message string) string {
	return fmt.Sprintf("git merge --squash %s && git commit -m %s", runx.ShQuote(branch), runx.ShQuote(message))
}
