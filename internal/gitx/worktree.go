// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Worktree is a private checkout: its own directory and branch, cut from a
// known commit. Concurrent reviews never share a tree, so they cannot
// overwrite each other's edits or observe each other's half-done work. In
// --jobs mode it is a persistent lane reused across reviews; stacked-PR mode
// advances one through the stack. Results reach the main tree only through
// Merge.
type Worktree struct {
	Dir    string // absolute path of the checkout
	Branch string
	base   string // the commit the checkout was cut from
	repo   *Repo
}

// Base returns the commit this worktree was cut from (or advanced to).
func (w *Worktree) Base() string { return w.base }

// LockName is the run lock gauntlet writes in the directory it reviews. It
// lives here so the ignore rules and the runner agree on one spelling.
const LockName = ".gauntlet.lock"

// worktreeRoot is where per-review checkouts live, relative to the repo.
// Keeping them inside the repo means one .git object store and no cross-device
// rename; keeping them under one directory means one line in info/exclude.
const worktreeRoot = ".gauntlet/worktrees"

// conflictMarker opens a conflict region. git writes seven characters and then
// the branch name, so the marker alone is what a search looks for.
const conflictMarker = "<<<<<<<"

// IsClean reports whether the working tree has no uncommitted changes beyond
// the runner's own artifacts, tracked or untracked. Worktree isolation only
// refuses tracked modifications (see Status); untracked files do not block
// --jobs.
func (r *Repo) IsClean(ctx context.Context, ownArtifacts map[string]bool) (bool, error) {
	dirty, err := r.DirtyPaths(ctx, ownArtifacts)
	if err != nil {
		return false, err
	}
	return len(dirty) == 0, nil
}

// CurrentBranch returns the checked-out branch name, or "" on a detached HEAD.
func (r *Repo) CurrentBranch(ctx context.Context) string {
	out, err := r.run(ctx, gitQuick, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Branches lists local branch names, excluding gauntlet's own review
// branches: those are a run's scratch space, never a merge target.
func (r *Repo) Branches(ctx context.Context) []string {
	out, err := r.run(ctx, gitQuick, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, "gauntlet/") {
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

// ExcludeOwnArtifacts adds what a run writes into the reviewed tree, the
// per-review checkouts and the run lock, to .git/info/exclude, so neither ever
// shows up as an untracked file in the repository being reviewed. It is
// idempotent and best effort: a failure only means noisier git status output.
func (r *Repo) ExcludeOwnArtifacts(ctx context.Context) {
	out, err := r.run(ctx, gitQuick, "rev-parse", "--git-common-dir")
	if err != nil {
		return
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(r.Dir, gitDir)
	}
	path := filepath.Join(gitDir, "info", "exclude")
	body, _ := os.ReadFile(path)
	var missing []string
	for _, entry := range []string{"/" + worktreeRoot + "/", "/" + LockName} {
		if !strings.Contains(string(body), entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	prefix := ""
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		prefix = "\n"
	}
	fmt.Fprintf(f, "%s# gauntlet's own scratch: per-review worktrees and the run lock\n%s\n",
		prefix, strings.Join(missing, "\n"))
}

// ensureWorktreeRoot proves the scratch directory is the repository's own
// before anything is created or force-removed under it. A hostile tree can
// plant `.gauntlet` (or `worktrees` below it) as a symlink pointing anywhere;
// following it would hand `git worktree add` and `worktree remove --force` a
// path outside the repository. Each existing component must therefore be a
// real directory, and the resolved root must stay inside the resolved repo.
// Callers hold wtMu, like every other worktree-bookkeeping step.
func (r *Repo) ensureWorktreeRoot() error {
	repoReal, err := filepath.EvalSymlinks(r.Dir)
	if err != nil {
		return err
	}
	dir := repoReal
	for part := range strings.SplitSeq(worktreeRoot, "/") {
		dir = filepath.Join(dir, part)
		fi, err := os.Lstat(dir)
		if os.IsNotExist(err) {
			continue // MkdirAll creates the rest as real directories
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%s is not a real directory inside the repository; "+
				"refusing to write gauntlet worktrees through it", dir)
		}
	}
	return nil
}

// AddWorktree creates a checkout of base on a fresh branch. The name identifies
// the checkout (a persistent lane, or a one-shot conflict resolver); the tag
// (run id plus loop and lane) keeps concurrent and repeated runs from colliding
// on branch names.
func (r *Repo) AddWorktree(ctx context.Context, name, tag, base string) (*Worktree, error) {
	if !Available() {
		return nil, errors.New("git is required for parallel reviews")
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return nil, err
	}

	slug := BranchSlug(name)
	branch := fmt.Sprintf("gauntlet/%s/%s", tag, slug)
	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), tag+"-"+slug)
	// A leftover checkout from a killed run would fail the add; clear it first.
	_, _ = r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	// A leftover branch would fail the add too: a hot reload continues the
	// same run id while the successor's loop numbering restarts, so tags and
	// their branches recur. One still pointing at base carries no committed
	// work (a review commits before its branch matters), so dropping it lets a
	// rerun converge. One pointing anywhere else holds real output, and the
	// same rule that keeps conflicted branches applies: fail rather than destroy.
	if tip, err := r.Tip(ctx, "refs/heads/"+branch); err == nil {
		if tip != base {
			return nil, fmt.Errorf("branch %s already exists at %s, not base %s; merge or delete it first",
				branch, tip[:min(12, len(tip))], base[:min(12, len(base))])
		}
		// A failure here is not swallowed for long: the worktree add below
		// then fails on the branch that is still there, with git's own words.
		_, _ = r.run(ctx, gitNormal, "branch", "-D", branch)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", "-b", branch, dir, base); err != nil {
		r.abortWorktreeAdd(ctx, dir, branch)
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, Branch: branch, base: base, repo: r}, nil
}

// AddStackWorktree creates the one checkout a stacked-PR run advances through
// its review branches. Unlike AddWorktree, the caller supplies the complete,
// deterministic branch name so a later invocation can recover the same stack.
func (r *Repo) AddStackWorktree(ctx context.Context, branch, tag, base string) (*Worktree, error) {
	if !Available() {
		return nil, errors.New("git is required for stacked PRs")
	}
	if _, err := r.run(ctx, gitQuick, "check-ref-format", "--branch", branch); err != nil {
		return nil, fmt.Errorf("invalid stack branch %q", branch)
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return nil, err
	}

	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "stack-"+BranchSlug(tag))
	_, _ = r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	if tip, err := r.Tip(ctx, "refs/heads/"+branch); err == nil {
		if tip != base {
			return nil, fmt.Errorf("branch %s already carries unpublished work", branch)
		}
		_, _ = r.run(ctx, gitNormal, "branch", "-D", branch)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", "-b", branch, dir, base); err != nil {
		r.abortWorktreeAdd(ctx, dir, branch)
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, Branch: branch, base: base, repo: r}, nil
}

// AddSnapshotWorktree cuts a read-only view of one commit, detached so no
// branch is created or moved. Stacked runs discover project prompts and
// compute suggestions from this snapshot of the fetched remote base, never
// from the user's checkout: an uncommitted or local-only *-review.md must not
// steer a run that publishes only remote-based work.
func (r *Repo) AddSnapshotWorktree(ctx context.Context, tag, base string) (*Worktree, error) {
	if !Available() {
		return nil, errors.New("git is required for stacked PRs")
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return nil, err
	}
	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "base-"+BranchSlug(tag))
	// A leftover snapshot from a killed run sits at this same deterministic
	// path; it is gauntlet's own and carries nothing, so it is replaced.
	_, _ = r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", "--detach", dir, base); err != nil {
		r.abortWorktreeAdd(ctx, dir, "")
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, base: base, repo: r}, nil
}

// abortWorktreeAdd drops a half-created checkout and, when named, its branch.
// A cancel or kill can land after git has registered the worktree and created
// the branch; both have to go. The caller still holds wtMu, and the context
// is kept alive so a cancelled add does not skip the cleanup.
func (r *Repo) abortWorktreeAdd(ctx context.Context, dir, branch string) {
	cleanCtx := context.WithoutCancel(ctx)
	_, _ = r.run(cleanCtx, gitQuick, "worktree", "unlock", dir)
	_, _ = r.run(cleanCtx, gitNormal, "worktree", "remove", "--force", dir)
	if branch != "" {
		_, _ = r.run(cleanCtx, gitNormal, "branch", "-D", branch)
	}
}

// StartBranch advances a stack worktree onto a fresh child of base. The old
// branch remains: an open pull request still needs it locally and remotely.
func (w *Worktree) StartBranch(ctx context.Context, branch, base string) error {
	if w == nil || w.repo == nil {
		return errors.New("nil stack worktree")
	}
	if _, err := w.repo.run(ctx, gitQuick, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid stack branch %q", branch)
	}
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "switch", "--quiet", "-c", branch, base); err != nil {
		return fmt.Errorf("git switch -c %s: %w", branch, err)
	}
	w.Branch, w.base = branch, base
	return nil
}

// DiscardCurrent resets an unpublished layer, detaches the checkout at its
// base, and deletes only that empty gauntlet branch. The next review can then
// start another child without any failed or no-change branch in the stack.
func (w *Worktree) DiscardCurrent(ctx context.Context) error {
	if w == nil || w.repo == nil || w.Branch == "" {
		return nil
	}
	branch, base := w.Branch, w.base
	if err := w.ResetToBase(ctx); err != nil {
		return err
	}
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "switch", "--quiet", "--detach", base); err != nil {
		return fmt.Errorf("git switch --detach: %w", err)
	}
	w.Branch = ""
	w.repo.DeleteBranch(ctx, branch)
	return nil
}

// CommitAll stages everything in the worktree and commits it. It reports
// whether there was anything to commit.
//
// Calling it again with nothing new staged commits nothing and reports false,
// so a retry of the finish step cannot split one review across two commits.
//
// The agent is forbidden to run git itself, so this is the only writer: a
// single commit per review, authored by the runner, with no AI attribution in
// the message (the same rule the commit-step prompt enforces).
func (w *Worktree) CommitAll(ctx context.Context, message string) (bool, error) {
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "add", "-A"); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}
	// diff --cached --quiet exits 1 when something is staged. Any other
	// outcome means the answer was not read off healthy plumbing: committing
	// then would decide on broken state rather than on what is staged.
	_, derr := sub.run(ctx, gitNormal, "diff", "--cached", "--quiet")
	switch {
	case derr == nil:
		return false, nil
	case !exitsWith(derr, 1):
		return false, fmt.Errorf("git diff --cached --quiet: %w", derr)
	}
	if _, err := sub.run(ctx, gitNormal,
		"commit", "--no-verify", "--quiet", "-m", message); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}
	return true, nil
}

// SquashIn stages another branch's work in this checkout without committing
// it, and reports the paths git could not merge on its own. An empty list with
// a nil error means the merge applied cleanly and is staged.
//
// It is how a conflict gets somewhere private to be resolved: the conflicted
// state lives in a scratch checkout, never in the tree the user is working in.
func (w *Worktree) SquashIn(ctx context.Context, branch string) ([]string, error) {
	sub := &Repo{Dir: w.Dir}
	out, err := sub.run(ctx, gitSlow, "merge", "--squash", "--no-verify", branch)
	if err == nil {
		return nil, nil
	}
	// -z names each path on its own NUL-terminated record and leaves bytes
	// like é raw. Without it git quotes non-ASCII into C escapes
	// ("r\303\251sum\303\251.tex"), a string Unresolved cannot open back up,
	// so markers in that file would go unnoticed and get committed.
	unmerged, uErr := sub.run(ctx, gitNormal, "diff", "--name-only", "-z",
		"--diff-filter=U")
	if uErr != nil {
		return nil, fmt.Errorf("git merge --squash %s: %w", branch, err)
	}
	var paths []string
	for p := range strings.SplitSeq(string(unmerged), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		// The merge failed for a reason no editing can fix (a bad ref, a
		// checkout git refused). Report git's own words alongside the cause,
		// so a caller matching on the failure type still sees the narration.
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return nil, fmt.Errorf("git merge --squash %s: %w", branch, err)
		}
		return nil, fmt.Errorf("git merge --squash %s: %w: %s", branch, err, firstLine(detail))
	}
	return paths, nil
}

// Unresolved lists which of the given paths still carry conflict markers. It
// is the check on a resolution: an agent that stopped halfway leaves a file
// that looks edited and still has `<<<<<<<` in it, and committing that would
// put markers into the project's history.
//
// A path that is gone is resolved: deleting the file is a valid answer to a
// delete/modify conflict. A path that cannot be read at all is evidence of
// nothing, and reporting nothing would read as "resolved": a permissions
// problem on a half-edited file must fail the scan rather than authorize the
// commit. The paths inspected so far come back either way, with an error that
// tells the caller the resolution is unverified.
func (w *Worktree) Unresolved(ctx context.Context, paths []string) ([]string, error) {
	var left []string
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return left, fmt.Errorf("conflict-marker scan stopped early: %w", err)
		}
		body, err := os.ReadFile(filepath.Join(w.Dir, filepath.FromSlash(p)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return left, fmt.Errorf("cannot read %s for conflict markers: %w", p, err)
		}
		if bytes.Contains(body, []byte(conflictMarker)) {
			left = append(left, p)
		}
	}
	return left, nil
}

// ResetToBase restores the checkout to the commit it was cut from, undoing
// everything a failed attempt left behind: tracked files go back to base
// (staged or not), and untracked files the attempt created are removed.
//
// It is what makes a retried review converge: attempt N+1 must start from the
// same state attempt N did, or a review that half-applied its fixes before
// exiting nonzero would hand its successor a tree no rerun could reproduce,
// and one failed attempt would change what a later successful one commits.
//
// Like CommitAll, this is the runner writing, never the agent. Running it on
// an already-clean checkout is a no-op, so calling it before every retry is
// safe whatever the previous attempt actually did. Ignored files stay: only
// git-visible debris is the retry's problem.
func (w *Worktree) ResetToBase(ctx context.Context) error {
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "reset", "--hard", w.base); err != nil {
		return fmt.Errorf("git reset --hard: %w", err)
	}
	if _, err := sub.run(ctx, gitNormal, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean -fd: %w", err)
	}
	return nil
}

// Advance moves a persistent lane worktree to a new base, detaching from
// whatever branch the previous review used so it can be deleted by the
// caller. Uncommitted changes and untracked files from the previous review
// are cleaned up. After Advance the worktree is detached at newBase with
// Branch == ""; the next review calls StartBranch to begin its own work.
func (w *Worktree) Advance(ctx context.Context, newBase string) error {
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "checkout", "--quiet", "--detach", newBase); err != nil {
		return fmt.Errorf("git checkout --detach: %w", err)
	}
	if _, err := sub.run(ctx, gitNormal, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean -fd: %w", err)
	}
	w.Branch = ""
	w.base = newBase
	return nil
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
		unmerged, uErr := r.run(ctx, gitNormal, "diff", "--name-only", "--diff-filter=U")
		conflicted := uErr == nil && strings.TrimSpace(string(unmerged)) != ""
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		r.abortMerge(ctx)
		return MergeResult{Conflict: conflicted, Detail: firstLine(detail)}
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
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		r.abortMerge(ctx)
		return MergeResult{Detail: firstLine(detail)}
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
		return MergeResult{Detail: fmt.Sprintf("no local branch %s to merge into", target)}
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return MergeResult{Detail: err.Error()}
	}

	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "merge-"+BranchSlug(target))
	// Clearing a checkout a killed run left here; if it fails, the add below
	// reports it rather than this line.
	_, _ = r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return MergeResult{Detail: err.Error()}
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", dir, target); err != nil {
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
	unmerged, uErr := sub.run(ctx, gitNormal, "diff", "--name-only", "--diff-filter=U")
	conflicted := uErr == nil && strings.TrimSpace(string(unmerged)) != ""
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	_, _ = sub.run(context.WithoutCancel(ctx), gitNormal, "merge", "--abort")
	return MergeResult{Conflict: conflicted, Detail: detail}
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
		return "", fmt.Errorf("no Git remote %s", remote)
	}
	return strings.TrimSpace(string(out)), nil
}

// RemotePushURL returns where `git push` sends this remote's branches: the
// configured push URL when one exists, the fetch URL otherwise. Raw like
// RemoteURL, for the same reason.
func (r *Repo) RemotePushURL(ctx context.Context, remote string) (string, error) {
	if out, err := r.run(ctx, gitQuick, "config", "--get", "remote."+remote+".pushurl"); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return r.RemoteURL(ctx, remote)
}

// HasCommit reports whether the object store already holds sha as a commit.
// A hot-reload successor uses it to keep a pinned stack base that its
// predecessor fetched, instead of fetching a tip that may have moved.
func (r *Repo) HasCommit(ctx context.Context, sha string) bool {
	if !isHex(sha) {
		return false // never a revision expression or an option
	}
	_, err := r.run(ctx, gitQuick, "cat-file", "-e", sha+"^{commit}")
	return err == nil
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
	if err != nil || tip == "" {
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
		return fmt.Errorf("invalid branch name %q", branch)
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

// Remove deletes the checkout. The branch is left alone: Merge decides
// whether it is still needed.
func (w *Worktree) Remove(ctx context.Context) error {
	if w == nil || w.repo == nil {
		return nil
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	if _, err := w.repo.run(ctx, gitNormal, "worktree", "remove", "--force", w.Dir); err != nil {
		// A cancel during "git worktree add" can leave the entry locked;
		// unlock it and retry before giving up.
		_, _ = w.repo.run(ctx, gitQuick, "worktree", "unlock", w.Dir)
		if _, err2 := w.repo.run(ctx, gitNormal, "worktree", "remove", "--force", w.Dir); err2 != nil {
			return fmt.Errorf("git worktree remove: %w", err)
		}
	}
	return nil
}

// DeleteBranch removes a merged review branch. Unmerged branches need -D and
// are kept instead, so nothing is destroyed by accident.
//
// It takes wtMu like every other worktree-bookkeeping command: deleting a
// branch walks the registered worktrees (a branch checked out anywhere has to
// survive), and reading that metadata while AddWorktree or PruneWorktrees is
// halfway through its own update fails exactly the way their doc comment
// describes. The failure would be swallowed here, leaving the branch stranded.
func (r *Repo) DeleteBranch(ctx context.Context, branch string) {
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	// -D, not -d: a squashed branch is not "merged" as far as git is
	// concerned, though its content is in the commit that just landed. This
	// is only ever called after that commit succeeded, or for a branch that
	// never left its base.
	_, _ = r.run(ctx, gitNormal, "branch", "-D", branch)
}

// DeleteBranchesMatching deletes every branch matching a glob pattern.
// Used to sweep review branches that a cancelled lane may have left behind.
// Prunes stale worktree registrations first so a branch is not rejected as
// "checked out" in a worktree that was already removed from disk.
func (r *Repo) DeleteBranchesMatching(ctx context.Context, pattern string) {
	r.PruneWorktrees(ctx)
	out, err := r.run(ctx, gitQuick, "branch", "--list", pattern)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			r.DeleteBranch(ctx, name)
		}
	}
}

// PruneWorktrees clears bookkeeping for checkouts that no longer exist, which
// is what a killed run leaves behind.
func (r *Repo) PruneWorktrees(ctx context.Context) {
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	_, _ = r.run(ctx, gitNormal, "worktree", "prune")
}

// CleanWorktreeRoot removes the per-review checkout directory when nothing is
// left in it, so a finished run leaves no trace in the reviewed tree.
// os.Remove on a non-empty directory fails, which is the intended guard.
func (r *Repo) CleanWorktreeRoot() {
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	_ = os.Remove(root)
	_ = os.Remove(filepath.Dir(root))
}

// BranchSlug keeps a review name safe for a git ref.
func BranchSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "review"
	}
	return out
}

// StackBranchName is stable for one base commit, schedule position, and
// review. That stability is how a repeated invocation recognizes layers it
// already published without making unrelated stacks collide.
func StackBranchName(baseTip string, index int, review string) string {
	tip := BranchSlug(baseTip)
	if len(tip) > 12 {
		tip = tip[:12]
	}
	return fmt.Sprintf("gauntlet/stack/%s/%02d-%s", tip, index+1, BranchSlug(review))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
