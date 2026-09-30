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
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/ghx"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
)

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
		e.PathList())
}

// DisplayPaths returns sanitized tracked paths followed by sanitized
// untracked paths, ready for terminal output. The exact paths remain in the
// fields for programmatic handling; only their display form is altered.
func (e *StackDirtyError) DisplayPaths() []string {
	if e == nil {
		return nil
	}
	paths := slices.Concat(e.Tracked, e.Untracked)
	return safePaths(paths)
}

// PathList is DisplayPaths rendered for a one-line message, which names a few
// and counts the rest. It is what Error formats with, and an error is printed
// once per log line, so it reads the first names off the two lists rather than
// joining and sanitizing all of them.
func (e *StackDirtyError) PathList() string {
	if e == nil {
		return ""
	}
	shown := make([]string, 0, pathListLimit)
	for _, group := range [...][]string{e.Tracked, e.Untracked} {
		for _, p := range group {
			if len(shown) == pathListLimit {
				break
			}
			shown = append(shown, normalize.Sanitize(p))
		}
	}
	return humanize.ListOf(shown, pathListLimit, len(e.Tracked)+len(e.Untracked))
}

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
	// An unreadable push URL is refused whatever named the repository. A
	// --pr-repo run pushes to that URL while recovery and the taken-name
	// probe read the fetch URL, so continuing here has the stack look absent
	// and get pushed a second time.
	if pushErr != nil {
		return nil, fmt.Errorf("cannot read the push URL of %s: %w", cfg.PushRemote, pushErr)
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
			return nil, fmt.Errorf("cannot verify pinned stack base %s: %w", normalize.Clip(cfg.ResumeStackTip, shortTipLen), err)
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
	probe := fmt.Sprintf("review/preflight-%s-%s", normalize.Clip(baseTip, shortTipLen), safeTag(cfg.RunID))
	if err := repo.CanPushBranch(ctx, cfg.PushRemote, baseTip, probe); err != nil {
		return nil, fmt.Errorf("cannot push stack branches to %s: %w", cfg.PushRemote, err)
	}
	// The run lock is held for this directory by the time a stack is prepared,
	// so the worktree root holds nothing a live run is using: whatever is left
	// in it is scratch a killed process never removed.
	repo.SweepWorktreeRoot(ctx)
	return &StackPrep{Base: base, BaseTip: baseTip, GH: gh, ReadRemote: readRemote}, nil
}

// Lengths of the abbreviated commit ids a generated branch or probe name
// carries.
const (
	shortTipLen           = 12
	shortDisambiguatorLen = 6
)

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
// next layer's base only after its branch is pushed. A pull request the
// head/base lookup cannot see still leaves that commit in the chain, and
// the pass keeps scheduling: one layer that cannot publish does not end it.
func (r *Runner) runLoopStack(ctx context.Context, loopNo int) bool {
	start := r.stackResumeIndex()
	parent, parentTip := r.StackHead()
	// Layer numbers count what was actually published, not schedule position:
	// a review that changed nothing leaves no branch, so the two diverge as
	// soon as one does, and a body claiming to be layer 5 of a three-branch
	// chain sends its reader looking for branches that do not exist. They
	// continue across --max-loops passes so a later round's first PR does not
	// read as the first layer of a new stack.
	published := r.StackPublished()
	// pushed is a layer this pass put on the remote. dropWorktree is set
	// only when the pass finishes with every such layer's pull request
	// confirmed. Anything else — nothing pushed, a commit that never left,
	// a pull request the lookup cannot see — keeps the checkout, and a
	// later pass of this run reuses it instead of deleting it. Deleting it
	// is how a run that published nothing throws away the only directory
	// the operator can still open.
	pushed := false
	dropWorktree := false
	var wt *gitx.Worktree
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
		if wt == nil {
			return
		}
		if !dropWorktree {
			r.log("Keeping stack worktree at %s", wt.Dir)
			return
		}
		if err := wt.Remove(context.WithoutCancel(ctx)); err != nil {
			r.log("Cannot remove stack worktree: %v", err)
		}
	}()

	// A hot-reload successor receives only the unfinished suffix. Walk the
	// completed prefix to recover the last published branch; an absent branch
	// there was a no-change or failed review and correctly leaves the parent.
	// When the predecessor persisted the head, that walk would start from the
	// last published tip rather than this pass's base and find nothing.
	if r.cfg.ResumeStackHeadTip == "" {
		for i := range start {
			var err error
			previous := parent
			// handled carries nothing here: a completed-prefix layer is
			// recovered or collapsed, never left for an agent to run.
			parent, parentTip, _, err = r.recoverStackLayer(
				ctx, loopNo, i, r.cfg.Reviews[i], parent, parentTip, stackRecoverPrefix, published+1)
			if parent != previous {
				published++
				pushed = true
				r.rememberStackHead(parent, parentTip, published)
			}
			if err != nil {
				r.recordStackFailure(ctx, loopNo, r.cfg.Reviews[i],
					gitx.StackLoopProvisionalBranch(r.stackBaseTip, r.stackPass(loopNo), i, r.cfg.Reviews[i]), parent,
					"recover completed stack", err)
				if r.stackFailureStops(err) {
					return false
				}
			}
		}
	}

	r.setPending(r.cfg.Reviews[start:])
	var err error

	for i := start; i < len(r.cfg.Reviews); i++ {
		if ctx.Err() != nil || r.soft.Load() {
			return false
		}
		r.checkUsageLimit(ctx)
		if r.finish.Load() {
			r.dropPending()
			if pushed && !r.holdStackCheckout {
				dropWorktree = true
			}
			return true
		}
		if why := r.budgetExhausted(); why != "" {
			r.log("%s budget exhausted, finishing up", why)
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
			pushed = true
			r.rememberStackHead(parent, parentTip, published)
		}
		if err != nil {
			r.recordStackFailure(ctx, loopNo, review,
				gitx.StackLoopProvisionalBranch(r.stackBaseTip, r.stackPass(loopNo), i, review), parent, "recover stack layer", err)
			if r.stackFailureStops(err) {
				return false
			}
		}
		if handled {
			continue
		}
		// The layer starts under a deterministic provisional name and takes
		// its topic name only once its commit subject exists.
		branch := gitx.StackLoopProvisionalBranch(r.stackBaseTip, r.stackPass(loopNo), i, review)
		if wt == nil {
			wt, err = r.openStackWorktree(ctx, branch, parentTip)
		} else {
			err = wt.StartBranch(ctx, branch, parentTip)
		}
		if err != nil {
			r.recordStackFailure(ctx, loopNo, review, branch, parent, "create stack branch", err)
			return false
		}
		// Taken after the checkout exists, so the scratch directory itself
		// is the baseline. A later reading that differs is a write into the
		// launch checkout, which this pass does not commit and must not
		// report as a review that changed nothing.
		launchBefore, err := r.repo.LaunchTree(ctx)
		if err != nil {
			r.recordStackFailure(ctx, loopNo, review, branch, parent, "read the launch checkout", err)
			return false
		}

		res := r.runReview(ctx, review, loopNo, 0, wt)
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

		next, nextTip, done := r.publishStackLayer(ctx, loopNo, i, review, branch, wt, &res,
			parent, parentTip, published+1, launchBefore)
		if !done {
			return false
		}
		if next == "" {
			continue // a layer that changed nothing leaves the parent where it is
		}
		parent, parentTip, published = next, nextTip, published+1
		pushed = true
		r.rememberStackHead(parent, parentTip, published)
	}
	r.rememberStackHead(parent, parentTip, published)
	if pushed && !r.holdStackCheckout && ctx.Err() == nil {
		dropWorktree = true
	}
	return ctx.Err() == nil
}

// publishStackLayer commits a reviewed layer, names it after its commit
// subject, pushes it, and opens its PR. It returns the published branch and
// its tip, or an empty branch when the layer changed nothing or its push
// did not land. done is false only when the checkout cannot take another
// layer. A recorded failure with done true leaves the pass running.
func (r *Runner) publishStackLayer(ctx context.Context, loopNo, scheduleIndex int, review, branch string,
	wt *gitx.Worktree, res *Result, parent, parentTip string, layer int, launchBefore string) (string, string, bool) {

	changes, chErr := treeChanges(context.WithoutCancel(ctx), wt.Dir)
	if chErr != nil {
		r.log("Cannot read the status of the %s layer worktree, so the commit subject falls back to a generic one: %v",
			review, chErr)
	}
	title := commitSubject(res.Subject, changes)
	changed, err := wt.CommitAll(context.WithoutCancel(ctx), title)
	if err != nil {
		r.failStackLayer(res, loopNo, review, branch, parent, err)
		if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
			r.log("Cannot discard failed stack layer %s: %v", review, err)
			r.holdStackCheckout = true
			return "", "", false
		}
		return "", "", true
	}
	// CommitAll only sees the stack checkout. A review that exited 0 after
	// writing the launch tree, or after naming files it did not commit, is
	// not a layer that changed nothing: recording it as one is how a pass
	// reports every review passed and opens no pull request.
	if err := r.launchCheckoutChanged(ctx, launchBefore); err != nil {
		r.failStackLayer(res, loopNo, review, branch, parent, err)
		if !changed {
			r.discardEmptyStackLayer(ctx, wt, review)
			return "", "", true
		}
		r.holdStackCheckout = true
		return "", "", true
	}
	if !changed {
		// A subject with no diff is an idempotent re-run: the file was
		// already in that state. A per-file note is not. The protocol prints
		// one only for a file the review changed, and a note with nothing
		// committed means that edit landed somewhere this pass will not publish.
		if len(res.FileNotes) > 0 {
			r.failStackLayer(res, loopNo, review, branch, parent,
				errors.New("the review reported edits but the stack worktree has nothing to commit"))
			r.discardEmptyStackLayer(ctx, wt, review)
			return "", "", true
		}
		res.Ins, res.Del, res.HaveLines = 0, 0, true
		if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
			r.failStackLayer(res, loopNo, review, branch, parent, err)
			return "", "", false
		}
		r.st.Add(*res)
		return "", "", true
	}
	// The commit exists, so its subject can name the branch. A rename that
	// cannot happen (no usable topic, or the name is taken locally or on
	// the remote) keeps the provisional name, which is unique by
	// construction; the stack stays publishable either way.
	if final := r.stackFinalBranch(ctx, loopNo, scheduleIndex, review, title, branch); final != "" {
		if err := wt.RenameBranch(ctx, final); err != nil {
			r.log("Keeping provisional stack branch %s: %v", branch, err)
		} else {
			branch = final
			res.Branch = final
		}
	}
	body := r.stackBody(ctx, review, title, wt.Dir, parentTip, "HEAD", parent, layer, res.FileNotes)
	// The layer's own commit range is the exact measurement, so it replaces
	// whatever the shared-tree sample estimated -- but only when git
	// answered. An unreadable range leaves the estimate standing rather
	// than reporting the change as zero lines.
	if body.HaveLines {
		res.Ins, res.Del, res.HaveLines = body.Ins, body.Del, true
	}
	if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
		r.failStackLayer(res, loopNo, review, branch, parent, fmt.Errorf("push: %w", err))
		r.holdStackCheckout = true
		return "", "", true
	}
	prURL, err := r.ensurePullRequest(ctx, branch, parent, body)
	if err != nil {
		r.failStackLayer(res, loopNo, review, branch, parent, err)
		r.holdStackCheckout = true
		// The commit is on the remote, so later reviews stack on it. The
		// missing pull request stays a failed layer until a lookup sees it.
		tip, tipErr := r.repo.Tip(ctx, "refs/heads/"+branch)
		if tipErr != nil {
			return "", "", true
		}
		return branch, tip, true
	}
	res.URL = prURL
	r.st.Add(*res)
	r.publishPullRequest(loopNo, review, branch, parent, prURL, false, *res)
	branchTip, err := r.repo.Tip(ctx, "refs/heads/"+branch)
	if err != nil {
		r.st.addCommitFail()
		r.publishStackFailure(loopNo, review, branch, parent, err)
		return "", "", false
	}
	return branch, branchTip, true
}

// failStackLayer records a layer that could not be published. The result goes
// in before the failure is published, so nothing below the record can erase
// the failure and leave the run reporting no failed review.
func (r *Runner) failStackLayer(res *Result, loop int, review, branch, base string, err error) {
	res.Status = StatusFail
	res.Detail = err.Error()
	r.st.Add(*res)
	r.publishStackFailure(loop, review, branch, base, err)
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
// branch under it, and the commit graph, not the name, decides which one is
// this stack's layer. The layer is by construction a one-commit child of the
// previous layer's tip, which descends from the pinned base commit; a stale
// same-prefixed branch from an older stack hangs off some other parent and is
// rejected by that ancestry check.
func (r *Runner) recoverStackLayer(ctx context.Context, loopNo, scheduleIndex int, review, parent, parentTip string,
	pass stackRecoverPass, layer int) (next, nextTip string, handled bool, recoverErr error) {

	prefix := gitx.StackLoopPrefix(r.stackPass(loopNo), scheduleIndex, review)
	provisional := gitx.StackLoopProvisionalBranch(r.stackBaseTip, r.stackPass(loopNo), scheduleIndex, review)
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
				if err := r.repo.DeleteBranch(context.WithoutCancel(ctx), name); err != nil {
					r.log("Cannot delete the provisional stack branch %s: %v", name, err)
				}
			}
			continue
		}
		actualParent, err := r.repo.ParentTip(ctx, "refs/heads/"+name)
		if err != nil {
			// A layer branch descends from the pinned base, so it always has
			// a parent: a read failure is git failing, not ancestry rejecting
			// the branch. Reading it as a rejection would conclude this
			// stack has no layer and re-run a review whose work is already
			// committed, pushed, and possibly open as a pull request.
			return parent, parentTip, false, fmt.Errorf(
				"cannot read the parent of stack branch %s: %w", name, err)
		}
		if actualParent != parentTip {
			continue // not a one-commit child of this stack's previous layer
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
	// The subject is read back out of history, where an agent that committed
	// on its own, a resolved conflict, or an operator left whatever they
	// wrote. Everything downstream treats the title as display text, so it
	// gets the same clipping and sanitizing a subject written this run would
	// have had.
	title = clipSubject(title)
	_, remoteFound := remotes[branch]
	// A killed run can stop between commit and rename. Finish the rename here,
	// but never for a name the remote already knows: the remote must stay
	// self-describing, and renaming under an open PR would strand its head.
	if branch == provisional && !remoteFound {
		if final := r.stackFinalBranch(ctx, loopNo, scheduleIndex, review, title, branch); final != "" {
			if err := r.repo.RenameBranch(ctx, branch, final); err != nil {
				r.log("Keeping provisional stack branch %s: %v", branch, err)
			} else {
				branch = final
			}
		}
	}
	if !remoteFound {
		if err := r.repo.PushBranch(ctx, r.cfg.PushRemote, branch); err != nil {
			// The commit stays local. Later reviews branch from the last
			// pushed base, and this layer is not run again.
			return parent, parentTip, true, stackKeep{fmt.Errorf("push: %w", err)}
		}
	}
	prURL, err := r.gh.Find(ctx, branch, parent)
	if err != nil {
		return branch, branchTip, true, stackKeep{err}
	}
	if prURL == "" {
		body := r.stackBody(ctx, review, title, r.cfg.Dir, parentTip, branchTip, parent, layer, nil)
		prURL, err = r.ensurePullRequest(ctx, branch, parent, body)
		if err != nil {
			return branch, branchTip, true, stackKeep{err}
		}
	}
	res := Result{
		Review:  review,
		Branch:  branch,
		Base:    parent,
		URL:     prURL,
		Status:  StatusOK,
		Subject: title,
	}
	if ins, del, ok := r.repo.DiffStat(ctx, r.cfg.Dir, parentTip, branchTip); ok {
		res.Ins, res.Del, res.HaveLines = ins, del, true
	}
	if pass == stackRecoverCurrent {
		r.st.Add(res)
		r.publishPullRequest(loopNo, review, branch, parent, prURL, true, res)
	}
	return branch, branchTip, true, nil
}

// stackFinalBranch picks the published name of a committed layer: the
// deterministic prefix plus a topic cut from the commit subject. A name
// already taken by an unrelated branch (locally or on the remote) gets the
// stack's short base tip appended at the end, where nobody reads it; if even
// that is taken, "" says to keep the provisional name, which is unique by
// construction.
func (r *Runner) stackFinalBranch(ctx context.Context, loopNo, index int, review, subject, current string) string {
	final := gitx.StackLoopFinalBranch(r.stackPass(loopNo), index, review, subject)
	if final == "" || final == current {
		return ""
	}
	if !r.stackNameTaken(ctx, final) {
		return final
	}
	final += "-" + normalize.Clip(r.stackBaseTip, shortDisambiguatorLen)
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

// stackKeep is a layer whose commit stays and whose failure must not end
// the pass. Later reviews still run, and the scratch checkout stays.
type stackKeep struct{ error }

// stackFailureStops reports whether err ends the pass. A kept layer does
// not: the checkout is held and the caller schedules the next review.
func (r *Runner) stackFailureStops(err error) bool {
	if _, ok := errors.AsType[stackKeep](err); ok {
		r.holdStackCheckout = true
		return false
	}
	return true
}

// openStackWorktree returns the pass's scratch checkout. A pass that is
// holding one reopens that directory; replacing it would delete the commit
// the operator was told was left on disk.
func (r *Runner) openStackWorktree(ctx context.Context, branch, parentTip string) (*gitx.Worktree, error) {
	if r.holdStackCheckout {
		wt, err := r.repo.AdoptStackWorktree(ctx, r.cfg.RunID)
		if err != nil {
			return nil, err
		}
		if wt != nil {
			if err := wt.StartBranch(ctx, branch, parentTip); err != nil {
				return nil, err
			}
			return wt, nil
		}
	}
	return r.repo.AddStackWorktree(ctx, branch, r.cfg.RunID, parentTip)
}

// ensurePullRequest returns the pull request for branch onto base, creating
// it when the lookup finds none. A URL printed by create is not enough: the
// lookup runs again, and an empty result fails the layer. The commit is
// left where it is; a missing pull request is not a layer that changed nothing.
func (r *Runner) ensurePullRequest(ctx context.Context, branch, base string, body prBody) (string, error) {
	if prURL, err := r.gh.Find(ctx, branch, base); err != nil || prURL != "" {
		return prURL, err
	}
	if _, err := r.gh.Create(ctx, branch, base, body.Title, body.render()); err != nil {
		return "", err
	}
	found, err := r.gh.Find(ctx, branch, base)
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("pull request for %s onto %s was not created", branch, base)
	}
	return found, nil
}

// launchCheckoutChanged reports whether the launch checkout moved during the
// review. A read that fails is a change that cannot be ruled out, so the
// layer stops rather than passing with no pull request.
func (r *Runner) launchCheckoutChanged(ctx context.Context, before string) error {
	after, err := r.repo.LaunchTree(ctx)
	if err != nil {
		return fmt.Errorf("cannot read the launch checkout: %w", err)
	}
	if after != before {
		return fmt.Errorf("the review edited %s instead of the stack worktree", r.cfg.Dir)
	}
	return nil
}

// discardEmptyStackLayer drops a provisional branch that never got a commit.
// The checkout directory stays; the caller decides whether the pass removes it.
func (r *Runner) discardEmptyStackLayer(ctx context.Context, wt *gitx.Worktree, review string) {
	if err := wt.DiscardCurrent(context.WithoutCancel(ctx)); err != nil {
		r.log("Cannot discard failed stack layer %s: %v", review, err)
	}
}

// stackBody assembles what a layer's PR says about itself: an overview of
// what the change did, the subject area the review declared, the paths its
// commit touched, how big it is, and where it sits in the chain. Anything git
// will not answer is left out rather than guessed at; a body missing its file
// list still orients a reader, while one naming the wrong files misleads
// them. The overview is built only from notes whose paths the commit touched,
// for the same reason: a note for an untouched path describes the wrong diff,
// or was planted. A recovered layer has no notes (its agent ran in a process
// that is gone) and renders without an overview.
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
		// The notes are model output and the overview is cut to
		// prBodyOverviewMax when it renders, so spending the bound here
		// keeps a review that reported a note per file from building
		// megabytes of joined string to throw all but 400 runes of it away.
		// What fits is the same either way: a shorter overview is one the
		// render would have cut at this character anyway.
		budget := prBodyOverviewMax
		for _, n := range notes {
			if budget <= 0 {
				break
			}
			// A note whose path the commit never touched describes the wrong
			// diff, or was planted; it contributes nothing to the overview.
			if !touched[noteKey(n.Path)] {
				continue
			}
			part := strings.TrimSuffix(strings.TrimSpace(n.Note), ".")
			part = norm.NFC.String(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			parts = append(parts, part)
			// "; " between parts and the closing "." ride on the same bound:
			// they are what the joined string is, not an allowance on top.
			budget -= utf8.RuneCountInString(part) + 3
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
// slashes, no leading "./", NFC normalized, and bytes that are not valid
// UTF-8 repaired. The repair is not cosmetic: a name holding one is legal on
// ext4 and APFS, the agent's line reaches this table already repaired to
// U+FFFD by the parser that read it, and git still hands out the raw bytes.
// Without it the two forms never meet and the note is dropped as if it had
// described a file the commit never touched.
func noteKey(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
	return norm.NFC.String(normalize.Repair(p))
}

func (r *Runner) publishPullRequest(loop int, review, branch, base, prURL string, reused bool, res Result) {
	verb := "Opened"
	if reused {
		verb = "Reused"
	}
	r.log("%s PR for %s (%s -> %s): %s", verb, review, branch, base, prURL)
	ev := Event{Kind: EvPullRequest, Dir: r.cfg.Dir, Review: review,
		Loop: loop, Branch: branch, Base: base, URL: prURL, Status: StatusOK}
	if res.HaveLines {
		ev.Ins, ev.Del = new(res.Ins), new(res.Del)
	}
	r.bus.Publish(ev)
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
