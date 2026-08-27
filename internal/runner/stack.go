// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/maci0/gauntlet/internal/ghx"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
)

// prepareStackMode validates every local and remote precondition before an
// agent starts. In particular, the dry-run push proves new stack branches can
// be published without creating a probe ref on the remote.
func (r *Runner) prepareStackMode(ctx context.Context) error {
	if !gitx.Available() {
		return errors.New("--stacked-prs needs git")
	}
	if !r.repo.HasBaseline() {
		return fmt.Errorf("--stacked-prs needs a git repository with at least one commit: %s", r.cfg.Dir)
	}
	changes, err := r.repo.Status(ctx, r.cfg.OwnArtifacts)
	if err != nil {
		return fmt.Errorf("cannot read git status in %s: %w", r.cfg.Dir, err)
	}
	if len(changes.Tracked) > 0 {
		return fmt.Errorf("%w: commit or stash your changes before building a PR stack (%s)",
			ErrDirtyTree, humanize.List(safePaths(changes.Tracked), 3))
	}
	if n := len(changes.Untracked); n > 0 {
		r.log("%d untracked file(s) stay put and are not reviewed: %s",
			n, humanize.List(changes.Untracked, 3))
	}

	base := r.cfg.PRBase
	if base == "" {
		base = r.repo.CurrentBranch(ctx)
	}
	if base == "" {
		return errors.New("--stacked-prs needs --pr-base when HEAD is detached")
	}
	if err := r.repo.ValidateBranchName(ctx, base); err != nil {
		return fmt.Errorf("--pr-base: %w", err)
	}
	baseTip, err := r.repo.Tip(ctx, "refs/heads/"+base)
	if err != nil {
		return fmt.Errorf("--pr-base %s is not a local branch", base)
	}
	remoteURL, err := r.repo.RemoteURL(ctx, r.cfg.PushRemote)
	if err != nil {
		return err
	}
	remoteTip, found, err := r.repo.RemoteBranchTip(ctx, r.cfg.PushRemote, base)
	if err != nil {
		return fmt.Errorf("cannot read %s/%s: %w", r.cfg.PushRemote, base, err)
	}
	if !found {
		return fmt.Errorf("--pr-base %s does not exist on remote %s", base, r.cfg.PushRemote)
	}
	if remoteTip != baseTip {
		return fmt.Errorf("local %s and %s/%s differ; synchronize the base before building a stack",
			base, r.cfg.PushRemote, base)
	}

	repoName, host := r.cfg.PRRepo, r.cfg.PRHost
	if repoName == "" {
		repoName, host, err = ghx.ParseRemote(remoteURL)
		if err != nil {
			return fmt.Errorf("cannot infer the GitHub repository from %s: %w", r.cfg.PushRemote, err)
		}
	}
	if host == "" {
		host = "github.com"
	}
	r.gh = ghx.Client{Dir: r.cfg.Dir, Repo: repoName, Host: host}
	if err := r.gh.Preflight(ctx); err != nil {
		return err
	}
	probe := fmt.Sprintf("gauntlet/preflight/%s-%s", shortTip(baseTip), safeTag(r.cfg.RunID))
	if err := r.repo.CanPushBranch(ctx, r.cfg.PushRemote, baseTip, probe); err != nil {
		return fmt.Errorf("cannot push stack branches to %s: %w", r.cfg.PushRemote, err)
	}

	r.cfg.PRBase = base
	r.stackBase, r.stackBaseTip = base, baseTip
	r.repo.PruneWorktrees(ctx)
	return nil
}

func shortTip(tip string) string {
	if len(tip) > 12 {
		return tip[:12]
	}
	return tip
}

func safeTag(tag string) string {
	var b strings.Builder
	for _, r := range tag {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "run"
	}
	return b.String()
}

// runLoopStack builds a linear chain in one worktree. A layer becomes the
// next layer's base only after its branch is pushed and its PR exists.
func (r *Runner) runLoopStack(ctx context.Context, loopNo int) bool {
	start := r.stackResumeIndex()
	parent, parentTip := r.stackBase, r.stackBaseTip

	// A hot-reload successor receives only the unfinished suffix. Walk the
	// completed prefix to recover the last published branch; an absent branch
	// there was a no-change or failed review and correctly leaves the parent.
	for i := range start {
		var recovered bool
		var err error
		parent, parentTip, recovered, err = r.recoverStackLayer(
			ctx, i, r.cfg.Reviews[i], parent, parentTip, false, true)
		if err != nil {
			r.recordStackFailure(loopNo, r.cfg.Reviews[i],
				gitx.StackBranchName(r.stackBaseTip, i, r.cfg.Reviews[i]), parent,
				"recover completed stack", err)
			return false
		}
		_ = recovered // every completed-prefix layer is recovered or collapsed
	}

	r.setPending(r.cfg.Reviews[start:])
	var wt *gitx.Worktree
	var err error
	defer func() {
		if wt != nil {
			if err := wt.Remove(context.WithoutCancel(ctx)); err != nil {
				r.log("Cannot remove stack worktree: %v", err)
			}
		}
	}()

	for i := start; i < len(r.cfg.Reviews); i++ {
		if ctx.Err() != nil || r.soft.Load() {
			return false
		}
		if r.finish.Load() {
			r.dropPending()
			return true
		}
		if r.budgetExhausted() {
			r.log("Runtime budget exhausted, finishing up")
			return false
		}
		review, ok := r.takeNext()
		if !ok {
			break
		}

		var handled bool
		parent, parentTip, handled, err = r.recoverStackLayer(
			ctx, i, review, parent, parentTip, true, false)
		if err != nil {
			r.recordStackFailure(loopNo, review,
				gitx.StackBranchName(r.stackBaseTip, i, review), parent, "recover stack layer", err)
			return false
		}
		if handled {
			continue
		}
		branch := gitx.StackBranchName(r.stackBaseTip, i, review)
		if wt == nil {
			wt, err = r.repo.AddStackWorktree(ctx, branch, r.cfg.RunID, parentTip)
		} else {
			err = wt.StartBranch(ctx, branch, parentTip)
		}
		if err != nil {
			r.recordStackFailure(loopNo, review, branch, parent, "create stack branch", err)
			return false
		}

		res := r.runReview(ctx, review, loopNo, wt)
		res.Branch, res.Base = branch, parent
		if res.Status != StatusOK {
			if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
				r.log("Cannot discard failed %s layer: %v", review, err)
				return false
			}
			r.st.Add(res)
			if res.Status == StatusInterrupted {
				return false
			}
			continue
		}

		title := commitSubject(res.Subject, review)
		changed, commitErr := wt.CommitAll(context.WithoutCancel(ctx), title)
		if commitErr != nil {
			res.Status = StatusFail
			res.Detail = commitErr.Error()
			r.st.Add(res)
			r.publishStackFailure(loopNo, review, branch, parent, commitErr)
			_ = wt.DiscardCurrent(context.WithoutCancel(ctx))
			return false
		}
		if !changed {
			res.Ins, res.Del, res.HaveLines = 0, 0, true
			if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
				res.Status = StatusFail
				res.Detail = err.Error()
				r.publishStackFailure(loopNo, review, branch, parent, err)
				r.st.Add(res)
				return false
			}
			r.st.Add(res)
			continue
		}
		if ins, del, ok := r.repo.DiffStat(ctx, wt.Dir, parentTip, "HEAD"); ok {
			res.Ins, res.Del, res.HaveLines = ins, del, true
		}
		if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			res.Status = StatusFail
			res.Detail = "push: " + err.Error()
			r.st.Add(res)
			r.publishStackFailure(loopNo, review, branch, parent, fmt.Errorf("push: %w", err))
			return false
		}
		prURL, err := r.ensurePullRequest(ctx, review, branch, parent, title)
		if err != nil {
			res.Status = StatusFail
			res.Detail = err.Error()
			r.st.Add(res)
			r.publishStackFailure(loopNo, review, branch, parent, err)
			return false
		}
		res.URL = prURL
		r.st.Add(res)
		r.publishPullRequest(loopNo, review, branch, parent, prURL, false)
		parent = branch
		parentTip, err = r.repo.Tip(ctx, branch)
		if err != nil {
			r.st.addCommitFail()
			r.publishStackFailure(loopNo, review, branch, res.Base, err)
			return false
		}
	}
	return ctx.Err() == nil
}

// stackResumeIndex maps the hot-reload queue back onto the stable configured
// order. Stack mode never shuffles, so a legitimate queue is always a suffix.
func (r *Runner) stackResumeIndex() int {
	if len(r.resume) == 0 {
		return 0
	}
	start := len(r.cfg.Reviews) - len(r.resume)
	if start >= 0 && slices.Equal(r.cfg.Reviews[start:], r.resume) {
		r.resume = nil
		return start
	}
	r.resume = nil
	return 0
}

// recoverStackLayer finishes or reuses a deterministic layer left by an
// earlier process. It returns handled=false only when the agent must run.
func (r *Runner) recoverStackLayer(ctx context.Context, index int, review, parent string,
	parentTip string, record, absentDone bool) (next, nextTip string, handled bool, recoverErr error) {

	branch := gitx.StackBranchName(r.stackBaseTip, index, review)
	prURL, err := r.gh.Find(ctx, branch, parent)
	if err != nil {
		return parent, parentTip, false, err
	}
	localTip, localErr := r.repo.Tip(ctx, "refs/heads/"+branch)
	remoteTip, remoteFound, remoteErr := r.repo.RemoteBranchTip(ctx, r.cfg.PushRemote, branch)
	if remoteErr != nil {
		return parent, parentTip, false, remoteErr
	}
	if localErr != nil && remoteFound {
		if err := r.repo.FetchBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			return parent, parentTip, false, err
		}
		localTip, localErr = r.repo.Tip(ctx, "refs/heads/"+branch)
	}
	if localErr != nil {
		if prURL != "" {
			return parent, parentTip, false, errors.New("existing PR has no recoverable head branch")
		}
		return parent, parentTip, absentDone, nil
	}
	if localTip == parentTip {
		if remoteFound {
			return parent, parentTip, false, errors.New("remote stack branch has no review commit")
		}
		r.repo.DeleteBranch(context.WithoutCancel(ctx), branch)
		return parent, parentTip, absentDone, nil
	}
	actualParent, err := r.repo.ParentTip(ctx, branch)
	if err != nil || actualParent != parentTip {
		return parent, parentTip, false,
			fmt.Errorf("existing branch is not a one-commit child of %s", parent)
	}
	if remoteFound && remoteTip != localTip {
		return parent, parentTip, false, errors.New("local and remote stack branches differ")
	}
	if !remoteFound {
		if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			return parent, parentTip, false, fmt.Errorf("push: %w", err)
		}
	}
	if prURL == "" {
		title, err := r.repo.CommitSubject(ctx, branch)
		if err != nil {
			return parent, parentTip, false, err
		}
		prURL, err = r.ensurePullRequest(ctx, review, branch, parent, title)
		if err != nil {
			return parent, parentTip, false, err
		}
	}
	if record {
		r.st.Add(Result{Review: review, Branch: branch, Base: parent, URL: prURL})
	}
	r.publishPullRequest(1, review, branch, parent, prURL, true)
	return branch, localTip, true, nil
}

func (r *Runner) ensurePullRequest(ctx context.Context, review, branch, base, title string) (string, error) {
	if prURL, err := r.gh.Find(ctx, branch, base); err != nil || prURL != "" {
		return prURL, err
	}
	body := "## Summary\n\n" + title
	return r.gh.Create(ctx, branch, base, title, body)
}

func (r *Runner) publishPullRequest(loop int, review, branch, base, prURL string, reused bool) {
	verb := "Opened"
	if reused {
		verb = "Reused"
	}
	r.log("%s PR for %s (%s -> %s): %s", verb, review, branch, base, prURL)
	r.bus.Publish(Event{Kind: EvPullRequest, Dir: r.cfg.Dir, Review: review,
		Loop: loop, Branch: branch, Base: base, URL: prURL, Status: StatusOK})
}

func (r *Runner) publishStackFailure(loop int, review, branch, base string, err error) {
	r.log("STACK STOPPED after %s: %v", review, err)
	r.bus.Publish(Event{Kind: EvPullRequest, Dir: r.cfg.Dir, Review: review,
		Loop: loop, Branch: branch, Base: base, Status: StatusFail, Text: err.Error()})
}

func (r *Runner) recordStackFailure(loop int, review, branch, base, step string, err error) {
	detail := fmt.Errorf("%s: %w", step, err)
	r.st.Add(Result{Review: review, Agent: r.pickAgent(review, nil), Status: StatusFail,
		ExitCode: -1, Branch: branch, Base: base, Detail: detail.Error()})
	r.publishStackFailure(loop, review, branch, base, detail)
}
