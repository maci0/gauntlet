// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/ghx"
	"github.com/maci0/gauntlet/internal/gitx"
)

// StackPrep is what stacked mode proves before any agent starts, the
// suggestion agent included: the resolved base branch, the exact base commit
// the whole stack is pinned to, and a gh client already past authentication,
// repository access, and a dry-run new-branch push.
type StackPrep struct {
	Base    string
	BaseTip string
	GH      ghx.Client
	// ReadRemote is what ls-remote and fetch are pointed at to inspect stack
	// branches: the raw push URL when it differs from the fetch URL (a fork
	// workflow pushes there, and git's read operations follow the fetch URL),
	// the remote name otherwise.
	ReadRemote string
}

// PrepareStack validates every local and remote precondition of a stacked
// run and pins its base commit. It runs before any agent starts, and its
// order is deliberate: local checks and the dirty-checkout boundary come
// first (returning StackDirtyError before consent), the remote and its URL
// are validated next, and only then does anything touch the network. The
// dry-run push proves new stack branches can be published without creating a
// probe ref on the remote.
func PrepareStack(ctx context.Context, cfg Config) (*StackPrep, error) {
	if cfg.PushRemote == "" {
		cfg.PushRemote = "origin"
	}
	if !gitx.Available() {
		return nil, errors.New("--stacked-prs needs git")
	}
	repo := gitx.Open(cfg.Dir)
	if !repo.HasBaseline() {
		return nil, fmt.Errorf("--stacked-prs needs a git repository with at least one commit: %s", cfg.Dir)
	}
	base := cfg.PRBase
	if base == "" {
		cur, err := repo.CurrentBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot read the current branch: %w", err)
		}
		base = cur
	}
	if base == "" {
		return nil, errors.New("--stacked-prs needs --pr-base when HEAD is detached")
	}
	if err := repo.ValidateBranchName(ctx, base); err != nil {
		return nil, fmt.Errorf("--pr-base: %w", err)
	}
	remoteURL, err := repo.RemoteURL(ctx, cfg.PushRemote)
	if err != nil {
		return nil, err
	}
	// The dirty boundary surfaces before the fetch: consent is about what a
	// run is going to do, so nothing has happened yet when the user is asked.
	changes, err := repo.Status(ctx, cfg.OwnArtifacts)
	if err != nil {
		return nil, fmt.Errorf("cannot read git status in %s: %w", cfg.Dir, err)
	}
	if len(changes.Tracked)+len(changes.Untracked) > 0 && !cfg.AllowDirtyStack {
		return nil, &StackDirtyError{Dir: cfg.Dir, Remote: cfg.PushRemote, Base: base,
			Tracked: changes.Tracked, Untracked: changes.Untracked}
	}

	// The base repository comes from where fetches read; the PR head owner
	// from where pushes actually land. Both URLs are validated here, before
	// the first network operation, so a remote gh cannot address is refused
	// instead of half-used.
	repoName, host := cfg.PRRepo, cfg.PRHost
	headOwner := ""
	// Stack branches are pushed to the remote's push URL, while ls-remote and
	// fetch follow its fetch URL. When the two differ, recovery must read the
	// branches from where the pushes actually landed.
	readRemote := cfg.PushRemote
	pushURL, pushErr := repo.RemotePushURL(ctx, cfg.PushRemote)
	if pushErr == nil && pushURL != remoteURL {
		readRemote = pushURL
	}
	if repoName == "" {
		repoName, host, err = ghx.ParseRemote(remoteURL)
		if err != nil {
			return nil, fmt.Errorf("cannot infer the GitHub repository from %s: %w", cfg.PushRemote, err)
		}
		if pushErr != nil {
			return nil, pushErr
		}
		if pushURL != remoteURL {
			headRepo, _, err := ghx.ParseRemote(pushURL)
			if err != nil {
				return nil, fmt.Errorf("cannot infer the PR head repository from the push URL of %s: %w",
					cfg.PushRemote, err)
			}
			headOwner, _, _ = strings.Cut(headRepo, "/")
			if baseOwner, _, _ := strings.Cut(repoName, "/"); headOwner == baseOwner {
				headOwner = ""
			}
		}
	}
	if host == "" {
		host = "github.com"
	}

	// Fetch, rather than pull: the remote base object enters the shared Git
	// object store, while the branch and files checked out by the user do not
	// move. The isolated worktree is cut directly from this commit. A
	// hot-reload successor keeps the tip its predecessor pinned instead of
	// fetching again: a remote base that advances mid-run would rename every
	// layer and split the resumed run into a new stack.
	var baseTip string
	if cfg.ResumeStackTip != "" {
		has, err := repo.HasCommit(ctx, cfg.ResumeStackTip)
		if err != nil {
			return nil, fmt.Errorf("cannot verify pinned stack base %s: %w", shortTip(cfg.ResumeStackTip), err)
		}
		if has {
			baseTip = cfg.ResumeStackTip
		}
	}
	if baseTip == "" {
		baseTip, err = repo.FetchRemoteBranchTip(ctx, cfg.PushRemote, base)
		if err != nil {
			return nil, fmt.Errorf("cannot fetch %s/%s: %w", cfg.PushRemote, base, err)
		}
	}

	gh := ghx.Client{Dir: cfg.Dir, Repo: repoName, Host: host, HeadOwner: headOwner}
	if err := gh.Preflight(ctx); err != nil {
		return nil, err
	}
	probe := fmt.Sprintf("review/preflight-%s-%s", shortTip(baseTip), safeTag(cfg.RunID))
	if err := repo.CanPushBranch(ctx, cfg.PushRemote, baseTip, probe); err != nil {
		return nil, fmt.Errorf("cannot push stack branches to %s: %w", cfg.PushRemote, err)
	}
	repo.PruneWorktrees(ctx)
	return &StackPrep{Base: base, BaseTip: baseTip, GH: gh, ReadRemote: readRemote}, nil
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
	// Layer numbers count what was actually published, not schedule position:
	// a review that changed nothing leaves no branch, so the two diverge as
	// soon as one does, and a body claiming to be layer 5 of a three-branch
	// chain sends its reader looking for branches that do not exist.
	published := 0

	// A hot-reload successor receives only the unfinished suffix. Walk the
	// completed prefix to recover the last published branch; an absent branch
	// there was a no-change or failed review and correctly leaves the parent.
	for i := range start {
		var recovered bool
		var err error
		previous := parent
		parent, parentTip, recovered, err = r.recoverStackLayer(
			ctx, loopNo, i, r.cfg.Reviews[i], parent, parentTip, stackRecoverPrefix, published+1)
		if parent != previous {
			published++
		}
		if err != nil {
			r.recordStackFailure(ctx, loopNo, r.cfg.Reviews[i],
				gitx.StackProvisionalBranch(r.stackBaseTip, i, r.cfg.Reviews[i]), parent,
				"recover completed stack", err)
			return false
		}
		_ = recovered // every completed-prefix layer is recovered or collapsed
	}

	r.setPending(r.cfg.Reviews[start:])
	var wt *gitx.Worktree
	var err error
	defer func() {
		// A hard cancel has no successor, so whatever is still queued is
		// recorded rather than dropped from the stats, summary, and journal,
		// the same rule the sequential and parallel loops follow. Every exit
		// runs through here, so a cancel that lands mid-publication counts its
		// stranded reviews too; abandonQueue drains the queue, so reaching
		// this twice is a no-op. A soft stop leaves ctx alone and hands its
		// queue to the successor instead.
		if ctx.Err() != nil {
			r.abandonQueue(loopNo)
		}
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
		r.checkUsageLimit(ctx)
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
		previous := parent
		parent, parentTip, handled, err = r.recoverStackLayer(
			ctx, loopNo, i, review, parent, parentTip, stackRecoverCurrent, published+1)
		if parent != previous {
			published++
		}
		if err != nil {
			r.recordStackFailure(ctx, loopNo, review,
				gitx.StackProvisionalBranch(r.stackBaseTip, i, review), parent, "recover stack layer", err)
			return false
		}
		if handled {
			continue
		}
		// The layer starts under a deterministic provisional name and takes
		// its topic name only once its commit subject exists.
		branch := gitx.StackProvisionalBranch(r.stackBaseTip, i, review)
		if wt == nil {
			wt, err = r.repo.AddStackWorktree(ctx, branch, r.cfg.RunID, parentTip)
		} else {
			err = wt.StartBranch(ctx, branch, parentTip)
		}
		if err != nil {
			r.recordStackFailure(ctx, loopNo, review, branch, parent, "create stack branch", err)
			return false
		}

		res := r.runReview(ctx, review, loopNo, wt)
		res.Branch, res.Base = branch, parent
		if res.Status != StatusOK {
			// Recorded before anything that can fail below it: a discard that
			// breaks must not also erase the failure that led to it, or the
			// run reports no failed review and exits 0.
			r.st.Add(res)
			if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
				r.publishStackFailure(loopNo, review, branch, parent,
					fmt.Errorf("discard failed layer: %w", err))
				return false
			}
			if res.Status == StatusInterrupted {
				return false // the defer records what the cancel stranded
			}
			continue
		}

		title := commitSubject(res.Subject, treeChanges(context.WithoutCancel(ctx), wt.Dir))
		changed, commitErr := wt.CommitAll(context.WithoutCancel(ctx), title)
		if commitErr != nil {
			res.Status = StatusFail
			res.Detail = commitErr.Error()
			r.st.Add(res)
			r.publishStackFailure(loopNo, review, branch, parent, commitErr)
			if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
				r.log("Cannot discard failed stack layer %s: %v", review, err)
			}
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
		// The commit exists, so its subject can name the branch. A rename that
		// cannot happen (no usable topic, or the name is taken locally or on
		// the remote) keeps the provisional name, which is unique by
		// construction; the stack stays publishable either way.
		if final := r.stackFinalBranch(ctx, i, review, title, branch); final != "" {
			if err := wt.RenameBranch(ctx, final); err != nil {
				r.log("Keeping provisional stack branch %s: %v", branch, err)
			} else {
				branch = final
				res.Branch = final
			}
		}
		body := r.stackBody(ctx, review, title, wt.Dir, parentTip, "HEAD", parent, published+1, res.FileNotes)
		// The layer's own commit range is the exact measurement, so it replaces
		// whatever the shared-tree sample estimated -- but only when git
		// answered. An unreadable range leaves the estimate standing rather
		// than reporting the change as zero lines.
		if body.HaveLines {
			res.Ins, res.Del, res.HaveLines = body.Ins, body.Del, true
		}
		if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			res.Status = StatusFail
			res.Detail = "push: " + err.Error()
			r.st.Add(res)
			r.publishStackFailure(loopNo, review, branch, parent, fmt.Errorf("push: %w", err))
			return false
		}
		prURL, err := r.ensurePullRequest(ctx, branch, parent, body)
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
		parent, published = branch, published+1
		parentTip, err = r.repo.Tip(ctx, "refs/heads/"+branch)
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

// stackRecoverPass says which walk is asking about a layer.
type stackRecoverPass int

const (
	// stackRecoverPrefix walks layers a predecessor already finished.
	// An absent or empty branch is handled; a recovered PR is not recorded again.
	stackRecoverPrefix stackRecoverPass = iota
	// stackRecoverCurrent is the live schedule. An absent branch means the
	// agent must run; a recovered layer is recorded as a result.
	stackRecoverCurrent
)

// recoverStackLayer finishes or reuses a layer left by an earlier process. It
// returns handled=false only when the agent must run.
//
// A published layer's name carries a topic taken from a commit subject that
// does not exist yet when this runs, so the name cannot be recomputed. What
// can be is its deterministic prefix: candidates are every local and remote
// branch under it, and the commit graph — not the name — decides which one is
// this stack's layer. The layer is by construction a one-commit child of the
// previous layer's tip, which descends from the pinned base commit; a stale
// same-prefixed branch from an older stack hangs off some other parent and is
// rejected by that ancestry check.
func (r *Runner) recoverStackLayer(ctx context.Context, loopNo, scheduleIndex int, review, parent, parentTip string,
	pass stackRecoverPass, layer int) (next, nextTip string, handled bool, recoverErr error) {

	prefix := gitx.StackBranchPrefix(scheduleIndex, review)
	provisional := gitx.StackProvisionalBranch(r.stackBaseTip, scheduleIndex, review)
	locals, err := r.repo.LocalBranchesWithPrefix(ctx, prefix)
	if err != nil {
		return parent, parentTip, false, err
	}
	remotes, err := r.repo.RemoteBranchesWithPrefix(ctx, r.stackReadRemote, prefix)
	if err != nil {
		return parent, parentTip, false, err
	}
	names := slices.Clone(locals)
	for name := range remotes {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var branch, branchTip string
	for _, name := range names {
		localTip, localErr := r.repo.Tip(ctx, "refs/heads/"+name)
		remoteTip, remoteFound := remotes[name]
		if localErr != nil && remoteFound {
			if err := r.repo.FetchBranch(ctx, r.stackReadRemote, name); err != nil {
				return parent, parentTip, false, err
			}
			localTip, localErr = r.repo.Tip(ctx, "refs/heads/"+name)
		}
		if localErr != nil {
			continue
		}
		if localTip == parentTip {
			// A provisional branch whose review never committed. Only this
			// stack's own local leftover is reclaimed; any other branch
			// sitting at the parent is stale and merely ignored.
			if name == provisional && !remoteFound {
				r.repo.DeleteBranch(context.WithoutCancel(ctx), name)
			}
			continue
		}
		if actualParent, err := r.repo.ParentTip(ctx, "refs/heads/"+name); err != nil || actualParent != parentTip {
			continue // ancestry rejects it: not a one-commit child of this stack's previous layer
		}
		if remoteFound && remoteTip != localTip {
			return parent, parentTip, false, fmt.Errorf("local and remote stack branch %s differ", name)
		}
		if branch != "" {
			return parent, parentTip, false, fmt.Errorf(
				"both %s and %s look like this stack's layer %d; delete the stale one", branch, name, layer)
		}
		branch, branchTip = name, localTip
	}
	if branch == "" {
		return parent, parentTip, pass == stackRecoverPrefix, nil
	}

	title, err := r.repo.CommitSubject(ctx, "refs/heads/"+branch)
	if err != nil {
		return parent, parentTip, false, err
	}
	_, remoteFound := remotes[branch]
	// A killed run can stop between commit and rename. Finish the rename here,
	// but never for a name the remote already knows: the remote must stay
	// self-describing, and renaming under an open PR would strand its head.
	if branch == provisional && !remoteFound {
		if final := r.stackFinalBranch(ctx, scheduleIndex, review, title, branch); final != "" {
			if err := r.repo.RenameBranch(ctx, branch, final); err != nil {
				r.log("Keeping provisional stack branch %s: %v", branch, err)
			} else {
				branch = final
			}
		}
	}
	if !remoteFound {
		if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			return parent, parentTip, false, fmt.Errorf("push: %w", err)
		}
	}
	prURL, err := r.gh.Find(ctx, branch, parent)
	if err != nil {
		return parent, parentTip, false, err
	}
	if prURL == "" {
		body := r.stackBody(ctx, review, title, r.cfg.Dir, parentTip, branchTip, parent, layer, nil)
		prURL, err = r.ensurePullRequest(ctx, branch, parent, body)
		if err != nil {
			return parent, parentTip, false, err
		}
	}
	if pass == stackRecoverCurrent {
		r.st.Add(Result{Review: review, Branch: branch, Base: parent, URL: prURL})
	}
	r.publishPullRequest(loopNo, review, branch, parent, prURL, true)
	return branch, branchTip, true, nil
}

// stackFinalBranch picks the published name of a committed layer: the
// deterministic prefix plus a topic cut from the commit subject. A name
// already taken by an unrelated branch — locally or on the remote — gets the
// stack's short base tip appended at the end, where nobody reads it; if even
// that is taken, "" says to keep the provisional name, which is unique by
// construction.
func (r *Runner) stackFinalBranch(ctx context.Context, index int, review, subject, current string) string {
	final := gitx.StackFinalBranch(index, review, subject)
	if final == "" || final == current {
		return ""
	}
	if !r.stackNameTaken(ctx, final) {
		return final
	}
	final += "-" + shortDisambiguator(r.stackBaseTip)
	if final == current || r.stackNameTaken(ctx, final) {
		return ""
	}
	return final
}

// stackNameTaken reports whether a branch name is already in use locally or
// on the remote. An unreadable remote counts as taken: renaming onto a name
// that cannot be checked risks a rejected push over what is only cosmetics.
func (r *Runner) stackNameTaken(ctx context.Context, name string) bool {
	if _, err := r.repo.Tip(ctx, "refs/heads/"+name); err == nil {
		return true
	}
	_, found, err := r.repo.RemoteBranchTip(ctx, r.stackReadRemote, name)
	return err != nil || found
}

func shortDisambiguator(tip string) string {
	if len(tip) > 6 {
		return tip[:6]
	}
	return tip
}

func (r *Runner) ensurePullRequest(ctx context.Context, branch, base string, body prBody) (string, error) {
	if prURL, err := r.gh.Find(ctx, branch, base); err != nil || prURL != "" {
		return prURL, err
	}
	return r.gh.Create(ctx, branch, base, body.Title, body.render())
}

// stackBody assembles what a layer's PR says about itself: an overview of
// what the change did, the subject area the review declared, the paths its
// commit touched, how big it is, and where it sits in the chain. Anything git
// will not answer is left out rather than guessed at; a body missing its file
// list still orients a reader, while one naming the wrong files misleads
// them. The overview is built only from notes whose paths the commit touched,
// for the same reason: a note for an untouched path describes the wrong diff,
// or was planted. A recovered layer has no notes — its agent ran in a process
// that is gone — and renders without an overview.
func (r *Runner) stackBody(ctx context.Context, review, title, dir, from, to, base string,
	layer int, notes []agent.FileNote) prBody {
	b := prBody{Title: title, Base: base, Root: r.stackBase, Layer: layer}
	if rev, ok := r.cfg.Set.Get(review); ok {
		b.Scope = rev.Summary()
	}
	if files, err := r.repo.ChangedFiles(ctx, dir, from, to); err == nil {
		b.Files = files
	}
	if len(notes) > 0 && len(b.Files) > 0 {
		touched := make(map[string]bool, len(b.Files))
		for _, f := range b.Files {
			touched[noteKey(f)] = true
		}
		var parts []string
		seen := map[string]bool{}
		for _, n := range notes {
			// A note whose path the commit never touched describes the wrong
			// diff, or was planted; it contributes nothing to the overview.
			if !touched[noteKey(n.Path)] {
				continue
			}
			part := strings.TrimSuffix(strings.TrimSpace(n.Note), ".")
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			b.Overview = strings.Join(parts, "; ") + "."
		}
	}
	if ins, del, ok := r.repo.DiffStat(ctx, dir, from, to); ok {
		b.Ins, b.Del, b.HaveLines = ins, del, true
	}
	return b
}

// noteKey aligns an agent-printed path with a git-reported one: forward
// slashes and no leading "./".
func noteKey(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
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

func (r *Runner) recordStackFailure(ctx context.Context, loop int, review, branch, base, step string, err error) {
	detail := fmt.Errorf("%s: %w", step, err)
	res := Result{Review: review, Agent: r.pickAgent(review, nil),
		ExitCode: -1, Branch: branch, Base: base, Detail: detail.Error()}
	// A cancel kills the git and gh commands that build a layer, so the step
	// reports its own failure. The review was interrupted, not failed: calling
	// it a failure inflates the failure count on a Ctrl-C and publishes a
	// pull_request failure for a layer nobody attempted.
	if ctx.Err() != nil {
		res.Status = StatusInterrupted
		r.st.Add(res)
		return
	}
	res.Status = StatusFail
	r.st.Add(res)
	r.publishStackFailure(loop, review, branch, base, detail)
}
