// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// The commit step: one agent launch that turns whatever the reviews changed
// into a commit, both inside the loop and on its own.

package runner

import (
	"context"
	"errors"
	"fmt"
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

// CommitOpts describes a commit step run on its own, outside a loop: the
// offer gauntlet makes when --jobs needs a clean tree and the only thing in
// the way is uncommitted work.
type CommitOpts struct {
	Dir     string
	Agent   agent.Spec
	Bin     map[string]string
	Push    bool
	Yolo    bool
	Timeout time.Duration
	// Out receives the agent's normalized output, so a caller with a terminal
	// can show the work rather than a silent pause. Nil discards it.
	Out func(string)
}

// CommitNow hands the working tree to one agent to commit, and reports
// whether the tree ended up clean. It is the same prompt and the same
// containment as the commit step inside a loop; what differs is that nothing
// else is running, so the caller waits for it.
func CommitNow(ctx context.Context, o CommitOpts) error {
	argv, err := agent.BuildCmd(o.Agent, prompt.CommitPrompt(o.Push, o.Yolo),
		agent.BuildOpts{Binary: o.Bin[o.Agent.Tool]})
	if err != nil {
		return fmt.Errorf("cannot build the commit command for %s: %w", o.Agent.Label(), err)
	}
	timeout := o.Timeout
	if timeout <= 0 || timeout > commitTimeout {
		timeout = commitTimeout
	}
	var sink func(normalize.Line)
	if o.Out != nil {
		sink = func(l normalize.Line) { o.Out(l.Text) }
	}
	pr := runProc(ctx, procOpts{
		Argv: argv, Dir: o.Dir, Timeout: timeout,
		MaxLinesPerSec: outputRateLimit, Sink: sink,
	})
	switch {
	case pr.Err != nil:
		return fmt.Errorf("commit step could not launch %s: %w", o.Agent.Label(), pr.Err)
	case pr.TimedOut:
		return fmt.Errorf("commit step timed out after %s", humanize.Duration(timeout))
	case pr.Canceled:
		return context.Canceled
	case pr.ExitCode != 0:
		return fmt.Errorf("commit step failed: %s exited %d", o.Agent.Label(), pr.ExitCode)
	}
	return unfinishedCommit(ctx, gitx.Open(o.Dir), nil)
}

// unfinishedCommit reports whether tracked files are still dirty after a
// commit-step agent claimed success. The agent saying so is not the same as
// git saying so: both the in-loop step and the --jobs dirty-tree offer ask
// this, so a liar cannot make --commit look like it landed work.
//
// Untracked files are not the job. The same rule the worktree precondition
// uses: an agent that leaves untracked scratch behind has still committed
// what git tracks.
func unfinishedCommit(ctx context.Context, repo *gitx.Repo, own map[string]bool) error {
	// Open never fails, so git being absent is what the verification
	// actually hits, and saying so beats letting exec.ErrNotFound surface
	// as "cannot read git status".
	if !gitx.Available() {
		return errors.New("commit step finished but git is unavailable, so the commit could not be verified")
	}
	changes, err := repo.Status(ctx, own)
	if err != nil {
		return fmt.Errorf("cannot read git status after the commit step: %w", err)
	}
	if len(changes.Tracked) > 0 {
		return fmt.Errorf("still uncommitted after the commit step: %s",
			humanize.List(safePaths(changes.Tracked), 3))
	}
	return nil
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
		r.log("Warning: could not check git status before the commit step: %v", err)
	}

	// The commit step is one launch per loop, not tied to a review, so it
	// samples under a stable pseudo-name of its own.
	spec := r.pickAgent("commit", nil)
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
		Raw: r.cfg.Raw, MaxLinesPerSec: outputRateLimit, Now: r.now,
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
	if status == StatusOK {
		if err := unfinishedCommit(ctx, r.repo, r.cfg.OwnArtifacts); err != nil {
			r.log("%v", err)
			status = StatusFail
		}
	}
	if status == StatusFail || status == StatusTimeout {
		r.st.addCommitFail()
	}
	r.bus.Publish(Event{
		Kind: EvCommit, Dir: r.cfg.Dir, Agent: spec.Label(),
		Status: status, Text: action,
	})
}
