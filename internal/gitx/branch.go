// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// CurrentBranch returns the checked-out branch name. A detached HEAD is
// ("", nil): that is a state, not a failure. Any other git error is returned
// so callers do not treat a broken repository as detached.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, gitQuick, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		if exitsWith(err, 1) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Branches lists local branch names, excluding gauntlet's own review
// branches — a run's lane scratch space under gauntlet/ and stacked-PR layers
// under review/ — since neither is ever a merge target.
func (r *Repo) Branches(ctx context.Context) []string {
	out, err := r.run(ctx, gitQuick, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, "gauntlet/") && !strings.HasPrefix(name, "review/") {
			names = append(names, name)
		}
	}
	return names
}

// Tip returns the commit the given ref points at.
func (r *Repo) Tip(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, gitQuick, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// MergeResult says what happened when a review's branch met the main tree.
type MergeResult struct {
	Merged   bool
	Conflict bool
	Detail   string
}

// Merge lands a review branch on the current branch of the main tree as one
// commit. The branch is squashed rather than merged: a loop of forty reviews
// should leave forty commits in the project's history, not forty of them
// threaded through forty merge nodes.
//
// Callers must serialize merges: git allows one at a time per worktree, and a
// conflicting merge must be aborted before the next one starts.
//
// A branch that carries nothing new succeeds as a no-op rather than adding an
// empty commit: a rerun that reaches an already-landed review (a crash
// between merge and branch cleanup) leaves the tree exactly as the first pass
// did.
//
// On conflict nothing lands and the branch is kept, so the work can be
// inspected or merged by hand. Dropping it silently would lose a review's
// entire output.
//
// A merge failure that leaves nothing unmerged is reported as neither merged
// nor conflicted: a bad ref or a refused checkout cannot be edited past, and
// labeling it a conflict would send a resolver to fix what no edit fixes and
// count the review as MERGE CONFLICT instead of failed.
func (r *Repo) Merge(ctx context.Context, branch, message string) MergeResult {
	out, err := r.run(ctx, gitSlow, "merge", "--squash", "--no-verify", branch)
	if err != nil {
		// A conflicted merge narrates on stdout and leaves unmerged entries in
		// the index; other failures explain on stderr and leave the index
		// alone. Classification asks git which it was (as SquashIn does), so
		// the answer comes from the index rather than from parsing narration.
		// Either way keep the cause's words; the check must run before abort,
		// which throws away exactly this state.
		paths, uErr := r.unmergedPaths(ctx)
		r.abortMerge(ctx)
		return classifyMergeFailure(out, err, paths, uErr)
	}
	// A squash stages; it never commits. Nothing staged means the branch held
	// nothing this tree does not already have.
	if _, clean := r.run(ctx, gitNormal, "diff", "--cached", "--quiet"); clean == nil {
		return MergeResult{Merged: true}
	}
	out, err = r.run(ctx, gitNormal,
		"commit", "--no-verify", "--quiet", "-m", message)
	if err != nil {
		// The squash applied cleanly, so whatever failed is bookkeeping and
		// not a conflict for anyone to resolve.
		r.abortMerge(ctx)
		return MergeResult{Detail: mergeNarration(out, err)}
	}
	return MergeResult{Merged: true}
}

// abortMerge clears whatever a failed merge left behind, so the next review
// does not inherit it. A squash leaves staged changes rather than a merge in
// progress, which `merge --abort` does not know about: the hard reset is what
// covers both.
func (r *Repo) abortMerge(ctx context.Context) {
	_, _ = r.run(ctx, gitNormal, "merge", "--abort")
	_, _ = r.run(ctx, gitNormal, "reset", "--hard", "HEAD")
}

// MergeInto merges branch into target, in a scratch checkout of target rather
// than in the tree the user is working in: gauntlet never switches the branch
// under someone's editor, and a run that merges is still a run they can watch.
//
// Like Merge, a repeat over an already-landed branch succeeds as a no-op, so
// the next loop's merge step cannot stack a second merge commit onto work
// that never changed.
//
// The target branch must exist locally. On conflict the merge is aborted and
// nothing moves, exactly as a review branch that will not merge is kept for a
// human.
func (r *Repo) MergeInto(ctx context.Context, target, branch, message string) MergeResult {
	if !Available() {
		return MergeResult{Detail: "git is not available"}
	}
	if _, err := r.Tip(ctx, "refs/heads/"+target); err != nil {
		return MergeResult{Detail: fmt.Sprintf("no local branch %s to merge into: %v", target, err)}
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return MergeResult{Detail: err.Error()}
	}

	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "merge-"+BranchSlug(target))
	// Clearing a checkout a killed run left here; if it fails, the add below
	// reports it rather than this line.
	if err := r.prepareWorktreeDir(ctx, dir); err != nil {
		return MergeResult{Detail: err.Error()}
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", dir, target); err != nil {
		r.abortWorktreeAdd(ctx, dir, "")
		// A target already checked out elsewhere is the common cause, and it
		// is the user's own checkout: say so rather than the raw git error.
		return MergeResult{Detail: fmt.Sprintf("cannot check out %s to merge into: %v", target, err)}
	}
	defer func() {
		_, _ = r.run(context.WithoutCancel(ctx), gitNormal, "worktree", "remove", "--force", dir)
	}()

	sub := &Repo{Dir: dir}
	out, err := sub.run(ctx, gitSlow,
		"merge", "--no-ff", "--no-verify", "-m", message, branch)
	if err == nil {
		return MergeResult{Merged: true}
	}
	// Classify the same way Merge does, by what the index still holds, before
	// the abort throws that state away: runMergeStep reports a genuine
	// conflict differently from a merge git could not even start.
	paths, uErr := sub.unmergedPaths(ctx)
	_, _ = sub.run(context.WithoutCancel(ctx), gitNormal, "merge", "--abort")
	return classifyMergeFailure(out, err, paths, uErr)
}

// unmergedPaths lists index entries git still has not resolved. -z keeps
// non-ASCII names as raw bytes, the same form Unresolved and the conflict
// prompt can open.
func (r *Repo) unmergedPaths(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, gitNormal, "diff", "--name-only", "-z", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// classifyMergeFailure records whether the index still holds unmerged paths
// and keeps a one-line explanation. Git writes "Auto-merging <path>" first
// and the CONFLICT line after it; taking the first line would hide why the
// merge stopped.
func classifyMergeFailure(out []byte, cause error, paths []string, pathErr error) MergeResult {
	return MergeResult{
		Conflict: pathErr == nil && len(paths) > 0,
		Detail:   mergeNarration(out, cause),
	}
}

func mergeNarration(out []byte, cause error) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = cause.Error()
	}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CONFLICT") {
			return line
		}
	}
	return firstLine(text)
}

// Push sends the current branch to its upstream. It is used after a review
// lands, so a long run publishes as it goes instead of holding everything
// until the end, and a failure is reported rather than fatal: the work is
// committed either way, and the next push will carry it.
func (r *Repo) Push(ctx context.Context) error {
	if out, err := r.run(ctx, gitPush, "push"); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("git push: %w", err)
		}
		// The cause stays attached so callers can still match on it; the
		// narration is kept beside it because git splits it across streams.
		return fmt.Errorf("git push: %w: %s", err, firstLine(detail))
	}
	return nil
}

// PushBranch publishes one stack layer under the same local and remote name.
// It never force-pushes: a divergent remote branch is preserved and reported.
func (r *Repo) PushBranch(ctx context.Context, remote, branch string) error {
	out, err := r.run(ctx, gitPush, "push", "--set-upstream", "--", remote,
		"refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		// Like Push: the cause stays attached so callers can still match on
		// it, with git's narration beside it because git splits the two
		// across streams.
		if detail := firstLine(strings.TrimSpace(string(out))); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

// CanPushBranch checks new-branch permission without changing the remote.
func (r *Repo) CanPushBranch(ctx context.Context, remote, source, branch string) error {
	out, err := r.run(ctx, gitPush, "push", "--dry-run", "--", remote,
		source+":refs/heads/"+branch)
	if err != nil {
		if detail := firstLine(strings.TrimSpace(string(out))); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

// RemoteURL returns the remote's configured fetch URL, as written, before any
// url.<base>.insteadOf rewriting: the rewrite redirects transport, while what
// the user configured is what names the repository.
func (r *Repo) RemoteURL(ctx context.Context, remote string) (string, error) {
	out, err := r.run(ctx, gitQuick, "config", "--get", "remote."+remote+".url")
	if err != nil {
		if exitsWith(err, 1) {
			return "", fmt.Errorf("no Git remote %s", remote)
		}
		return "", fmt.Errorf("git remote %s: %w", remote, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RemotePushURL returns where `git push` sends this remote's branches: the
// configured push URL when one exists, the fetch URL otherwise. Raw like
// RemoteURL, for the same reason.
func (r *Repo) RemotePushURL(ctx context.Context, remote string) (string, error) {
	out, err := r.run(ctx, gitQuick, "config", "--get", "remote."+remote+".pushurl")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	// Exit 1 is "no such key": fall back to the fetch URL. Any other failure
	// (timeout, not a repository) must not be papered over with a different URL.
	if !exitsWith(err, 1) {
		return "", fmt.Errorf("git remote %s push URL: %w", remote, err)
	}
	return r.RemoteURL(ctx, remote)
}

// HasCommit reports whether the object store already holds sha as a commit.
// A hot-reload successor uses it to keep a pinned stack base that its
// predecessor fetched, instead of fetching a tip that may have moved.
//
// Missing is a normal answer (false, nil). A git failure is returned, not
// reported as missing: treating a locked or broken store as "not there"
// would make a resumed stacked run fetch a new base and split the stack.
func (r *Repo) HasCommit(ctx context.Context, sha string) (bool, error) {
	if !isHex(sha) {
		return false, nil // never a revision expression or an option
	}
	// rev-parse --verify --quiet: exit 0 if the commit exists, 1 if it does
	// not, anything else if git itself failed. cat-file -e exits 128 for a
	// missing object, the same status as "not a repository", so it cannot
	// tell those apart.
	_, err := r.run(ctx, gitQuick, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err == nil {
		return true, nil
	}
	if exitsWith(err, 1) {
		return false, nil
	}
	return false, err
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// RemoteBranchTip reports a remote branch's object id without updating local
// refs. Missing is a normal answer used by stack resumption.
func (r *Repo) RemoteBranchTip(ctx context.Context, remote, branch string) (tip string, found bool, err error) {
	out, runErr := r.run(ctx, gitPush, "ls-remote", "--heads", "--", remote, "refs/heads/"+branch)
	if runErr != nil {
		return "", false, runErr
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", false, nil
	}
	return fields[0], true, nil
}

// LocalBranchesWithPrefix lists local branches that are the prefix itself or
// extend it past a hyphen: the shape every name a stack layer can publish
// under takes, provisional or topic-final. A layer's branch name depends on a
// commit subject that does not exist yet at recovery time, so recovery
// matches this deterministic prefix and verifies candidates by commit graph.
func (r *Repo) LocalBranchesWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	out, err := r.run(ctx, gitQuick, "for-each-ref", "--format=%(refname:short)",
		"refs/heads/"+prefix, "refs/heads/"+prefix+"-*")
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// RemoteBranchesWithPrefix is LocalBranchesWithPrefix against the remote,
// read via ls-remote so no local ref moves. It returns branch name to tip.
func (r *Repo) RemoteBranchesWithPrefix(ctx context.Context, remote, prefix string) (map[string]string, error) {
	out, err := r.run(ctx, gitPush, "ls-remote", "--heads", "--", remote,
		"refs/heads/"+prefix, "refs/heads/"+prefix+"-*")
	if err != nil {
		return nil, err
	}
	tips := make(map[string]string)
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if name, ok := strings.CutPrefix(fields[1], "refs/heads/"); ok {
			tips[name] = fields[0]
		}
	}
	return tips, nil
}

// RenameBranch moves a local branch, which is how recovery finishes the
// provisional-to-final rename a killed run left undone. Like
// Worktree.RenameBranch it refuses to overwrite: -m keeps a same-named branch
// holding real work and reports the failure. It takes wtMu like DeleteBranch:
// a rename walks the registered worktrees to follow a checked-out branch.
func (r *Repo) RenameBranch(ctx context.Context, from, to string) error {
	if _, err := r.run(ctx, gitQuick, "check-ref-format", "--branch", to); err != nil {
		return fmt.Errorf("invalid stack branch %q: %w", to, err)
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if _, err := r.run(ctx, gitNormal, "branch", "-m", from, to); err != nil {
		return fmt.Errorf("git branch -m %s %s: %w", from, to, err)
	}
	return nil
}

// FetchRemoteBranchTip downloads the selected remote branch and returns the
// fetched commit without moving any local branch or checkout. FETCH_HEAD is a
// process-local staging ref here: the repository lock prevents another
// gauntlet in the same clone from racing this preparation step.
func (r *Repo) FetchRemoteBranchTip(ctx context.Context, remote, branch string) (string, error) {
	if _, err := r.run(ctx, gitPush, "fetch", "--quiet", "--no-tags", "--", remote,
		"refs/heads/"+branch); err != nil {
		return "", err
	}
	tip, err := r.Tip(ctx, "FETCH_HEAD")
	if err != nil {
		return "", fmt.Errorf("fetch returned no branch tip: %w", err)
	}
	if tip == "" {
		return "", errors.New("fetch returned no branch tip")
	}
	return tip, nil
}

// FetchBranch restores a stack branch into local refs for a resumed run.
func (r *Repo) FetchBranch(ctx context.Context, remote, branch string) error {
	if _, err := r.run(ctx, gitPush, "fetch", "--quiet", "--", remote,
		"refs/heads/"+branch+":refs/heads/"+branch); err != nil {
		return fmt.Errorf("fetch branch %s: %w", branch, err)
	}
	return nil
}

// ValidateBranchName rejects revision expressions and option-shaped input
// before a user-supplied base is joined into refs/heads or a refspec.
func (r *Repo) ValidateBranchName(ctx context.Context, branch string) error {
	if _, err := r.run(ctx, gitQuick, "check-ref-format", "--branch", branch); err != nil {
		if exitsWith(err, 1) {
			return fmt.Errorf("invalid branch name %q", branch)
		}
		return fmt.Errorf("invalid branch name %q: %w", branch, err)
	}
	return nil
}

// CommitSubject returns the subject at ref, for completing publication after
// a previous process committed or pushed but stopped before creating its PR.
func (r *Repo) CommitSubject(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, gitQuick, "log", "-1", "--format=%s", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ParentTip returns a commit's first parent. Stack layers are deliberately one
// commit each, so this proves a recovered branch is based on the expected
// preceding layer rather than merely sharing some older ancestor.
func (r *Repo) ParentTip(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, gitQuick, "rev-parse", ref+"^")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return redactUserinfo(line)
}
