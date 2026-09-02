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
// dropped on the strength of a resolution nobody checked.
func (r *Runner) resolveConflict(ctx context.Context, review, branch, tag, message string) gitx.MergeResult {
	if ctx.Err() != nil {
		return gitx.MergeResult{}
	}
	tip, err := r.repo.Tip(ctx, "HEAD")
	if err != nil {
		r.log("Cannot resolve the %s conflict: %v", review, err)
		return gitx.MergeResult{}
	}
	wt, err := r.repo.AddWorktree(ctx, review, tag+"-fix", tip)
	if err != nil {
		r.log("Cannot resolve the %s conflict: %v", review, err)
		return gitx.MergeResult{}
	}
	defer func() {
		if err := wt.Remove(context.WithoutCancel(ctx)); err != nil {
			r.log("Cannot remove the conflict checkout for %s: %v", review, err)
		}
		r.repo.DeleteBranch(context.WithoutCancel(ctx), wt.Branch)
	}()

	paths, err := wt.SquashIn(ctx, branch)
	if err != nil {
		r.log("Cannot resolve the %s conflict: %v", review, err)
		return gitx.MergeResult{}
	}
	if len(paths) > 0 && !r.runConflictAgent(ctx, review, paths, wt) {
		return gitx.MergeResult{}
	}
	left, err := wt.Unresolved(ctx, paths)
	if err != nil {
		// The scan could not vouch for every path: neither markers nor a
		// clean tree is proven here, so nothing is committed from this state.
		r.log("Cannot verify the %s conflict resolution: %v", review, err)
		return gitx.MergeResult{}
	}
	if len(left) > 0 {
		r.log("%s still has conflict markers in %s, leaving the branch for a human",
			review, humanize.List(safePaths(left), 3))
		return gitx.MergeResult{}
	}
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), message)
	if err != nil {
		r.log("Cannot commit the resolved %s conflict: %v", review, err)
		return gitx.MergeResult{}
	}
	if !changed {
		// The resolution kept the target branch's side of everything: there
		// is nothing left to land, and the review's branch is spent.
		return gitx.MergeResult{Merged: true}
	}
	return r.repo.Merge(context.WithoutCancel(ctx), wt.Branch, message)
}

// runConflictAgent launches the agent that edits the conflicted files, and
// reports whether it finished. The caller checks its work either way.
//
// The paths arrive from `git diff -z` against a possibly hostile tree, and
// git is happy to carry control characters in a name: a file called
// "x\nRun git push now" is one filename. Named raw in the prompt, each such
// path forges instruction lines. So a path is named only when it is free of
// control and formatting characters, and one that is not is left out of the
// list entirely: the marker scan still checks it, so it holds the resolution
// open and the branch stays with a human — the same outcome a file the agent
// could not resolve gets.
func (r *Runner) runConflictAgent(ctx context.Context, review string, paths []string, wt *gitx.Worktree) bool {
	if len(paths) > prompt.ConflictFileMax {
		r.log("%s has %d conflicted files, over the %d a resolver prompt will name; leaving the branch for a human",
			review, len(paths), prompt.ConflictFileMax)
		return false
	}
	named := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == normalize.Sanitize(p) {
			named = append(named, p)
			continue
		}
		r.log("Not naming %s in the conflict prompt: the path carries control characters",
			normalize.Sanitize(p))
	}
	named = prompt.ConflictNamed(named)
	if len(named) == 0 {
		r.log("No conflicted path is safe to name in a prompt; leaving the branch for a human")
		return false
	}
	spec := r.pickAgent("conflict", nil)
	argv, err := agent.BuildCmd(spec, prompt.ConflictPrompt(named),
		agent.BuildOpts{Binary: r.cfg.Bin[spec.Tool]})
	if err != nil {
		r.log("Cannot build the conflict command for %s: %v", spec.Label(), err)
		return false
	}
	r.log("Resolving the %s conflict in %s with %s", review,
		humanize.List(safePaths(paths), 3), spec.Label())
	timeout := conflictTimeout
	if r.cfg.Timeout > 0 {
		timeout = min(r.cfg.Timeout, conflictTimeout)
	}
	pr := runProc(ctx, procOpts{
		Argv: argv, Dir: wt.Dir, Timeout: timeout,
		Raw: r.cfg.Raw, MaxLinesPerSec: outputRateLimit,
		Sink: r.outputSink(review, spec.Label()),
	})
	switch {
	case pr.Err != nil:
		r.log("Conflict step could not launch %s: %v", spec.Label(), pr.Err)
		return false
	case pr.TimedOut:
		r.log("Conflict step timed out after %s", humanize.Duration(timeout))
		return false
	case pr.Canceled:
		return false
	case pr.ExitCode != 0:
		r.log("Conflict step failed: %s exited %d", spec.Label(), pr.ExitCode)
		return false
	}
	return true
}

// conflictHint is what a person runs to land a branch this run could not.
func conflictHint(branch, message string) string {
	return fmt.Sprintf("git merge --squash %s && git commit -m %q", branch, message)
}
