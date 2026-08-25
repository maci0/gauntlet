// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Worktree is one review's private checkout: its own directory, its own
// branch, cut from a known commit. Concurrent reviews never share a tree, so
// they cannot overwrite each other's edits or observe each other's half-done
// work. Their results reach the main tree only through Merge.
type Worktree struct {
	Dir    string // absolute path of the checkout
	Branch string
	base   string // the commit the checkout was cut from
	repo   *Repo
}

// worktreeRoot is where per-review checkouts live, relative to the repo.
// Keeping them inside the repo means one .git object store and no cross-device
// rename; keeping them under one directory means one line in info/exclude.
const worktreeRoot = ".gauntlet/worktrees"

// IsClean reports whether the working tree has no uncommitted changes beyond
// the runner's own artifacts. Worktree mode requires a clean tree: a branch is
// cut from a commit, so uncommitted work would be invisible to every review
// and then collide with the merges.
func (r *Repo) IsClean(ctx context.Context, ownArtifacts map[string]bool) (bool, error) {
	dirty, err := r.DirtyPaths(ctx, ownArtifacts)
	if err != nil {
		return false, err
	}
	return len(dirty) == 0, nil
}

// CurrentBranch returns the checked-out branch name, or "" on a detached HEAD.
func (r *Repo) CurrentBranch(ctx context.Context) string {
	out, err := r.run(ctx, 10*time.Second, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Branches lists local branch names, excluding gauntlet's own review
// branches: those are a run's scratch space, never a merge target.
func (r *Repo) Branches(ctx context.Context) []string {
	out, err := r.run(ctx, 10*time.Second, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
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
	out, err := r.run(ctx, 10*time.Second, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ExcludeWorktreeRoot adds the worktree directory to .git/info/exclude, so the
// checkouts never show up as untracked files in the repo being reviewed. It is
// idempotent and best effort: a failure only means noisier git status output.
func (r *Repo) ExcludeWorktreeRoot(ctx context.Context) {
	out, err := r.run(ctx, 10*time.Second, "rev-parse", "--git-common-dir")
	if err != nil {
		return
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(r.Dir, gitDir)
	}
	path := filepath.Join(gitDir, "info", "exclude")
	body, _ := os.ReadFile(path)
	entry := "/" + worktreeRoot + "/"
	if strings.Contains(string(body), entry) {
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
	fmt.Fprintf(f, "%s# gauntlet per-review worktrees\n%s\n", prefix, entry)
}

// AddWorktree creates a checkout of base on a fresh branch. The name is the
// review it belongs to; the tag (run id plus loop and lane) keeps concurrent
// and repeated runs from colliding on branch names.
func (r *Repo) AddWorktree(ctx context.Context, name, tag, base string) (*Worktree, error) {
	if !Available() {
		return nil, errors.New("git is required for parallel reviews")
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()

	slug := branchSlug(name)
	branch := fmt.Sprintf("gauntlet/%s/%s", tag, slug)
	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), tag+"-"+slug)
	// A leftover checkout from a killed run would fail the add; clear it first.
	_, _ = r.run(ctx, 30*time.Second, "worktree", "remove", "--force", dir)
	// A leftover branch would fail the add too: a hot reload continues the
	// same run id while the successor's loop numbering restarts, so tags and
	// their branches recur. One still pointing at base carries no committed
	// work (a review commits before its branch matters), so dropping it lets a
	// rerun converge. One pointing anywhere else holds real output, and the
	// same rule that keeps conflicted branches applies: fail rather than destroy.
	if tip, err := r.Tip(ctx, branch); err == nil {
		if tip != base {
			return nil, fmt.Errorf("branch %s already exists at %s, not base %s; merge or delete it first",
				branch, tip[:min(12, len(tip))], base[:min(12, len(base))])
		}
		_, _ = r.run(ctx, 30*time.Second, "branch", "-D", branch)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, 120*time.Second, "worktree", "add", "--quiet", "-b", branch, dir, base); err != nil {
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, Branch: branch, base: base, repo: r}, nil
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
	if _, err := sub.run(ctx, 60*time.Second, "add", "-A"); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}
	// diff --cached --quiet exits 1 when something is staged.
	if _, err := sub.run(ctx, 30*time.Second, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	if _, err := sub.run(ctx, 60*time.Second,
		"-c", "user.name=gauntlet",
		"-c", "user.email=gauntlet@localhost",
		"commit", "--no-verify", "--quiet", "-m", message); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}
	return true, nil
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
	if _, err := sub.run(ctx, 60*time.Second, "reset", "--hard", w.base); err != nil {
		return fmt.Errorf("git reset --hard: %w", err)
	}
	if _, err := sub.run(ctx, 60*time.Second, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean -fd: %w", err)
	}
	return nil
}

// MergeResult says what happened when a review's branch met the main tree.
type MergeResult struct {
	Merged   bool
	Conflict bool
	Detail   string
}

// Merge integrates a review branch into the current branch of the main tree.
// Callers must serialize merges: git allows one at a time per worktree, and a
// conflicting merge must be aborted before the next one starts.
//
// A branch that is already fully merged succeeds as a no-op rather than
// adding a second merge commit: a rerun that reaches an already-landed review
// (a crash between merge and branch cleanup) leaves the tree exactly as the
// first pass did.
//
// On conflict the merge is aborted and the branch is kept, so the work can be
// inspected or merged by hand. Dropping it silently would lose a review's
// entire output.
func (r *Repo) Merge(ctx context.Context, branch, message string) MergeResult {
	out, err := r.run(ctx, 120*time.Second,
		"-c", "user.name=gauntlet",
		"-c", "user.email=gauntlet@localhost",
		"merge", "--no-ff", "--no-verify", "-m", message, branch)
	if err == nil {
		return MergeResult{Merged: true}
	}
	// A conflicted merge narrates on stdout; other failures explain on
	// stderr, which run folds into the error. Either way keep the cause.
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	// Abort whatever state the failed merge left behind, so the next review
	// does not inherit it.
	_, _ = r.run(ctx, 60*time.Second, "merge", "--abort")
	return MergeResult{Conflict: true, Detail: firstLine(detail)}
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

	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "merge-"+branchSlug(target))
	_, _ = r.run(ctx, 30*time.Second, "worktree", "remove", "--force", dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return MergeResult{Detail: err.Error()}
	}
	if _, err := r.run(ctx, 120*time.Second, "worktree", "add", "--quiet", dir, target); err != nil {
		// A target already checked out elsewhere is the common cause, and it
		// is the user's own checkout: say so rather than the raw git error.
		return MergeResult{Detail: fmt.Sprintf("cannot check out %s to merge into: %v", target, err)}
	}
	defer func() {
		_, _ = r.run(context.WithoutCancel(ctx), 30*time.Second, "worktree", "remove", "--force", dir)
	}()

	sub := &Repo{Dir: dir}
	out, err := sub.run(ctx, 120*time.Second,
		"-c", "user.name=gauntlet",
		"-c", "user.email=gauntlet@localhost",
		"merge", "--no-ff", "--no-verify", "-m", message, branch)
	if err == nil {
		return MergeResult{Merged: true}
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	_, _ = sub.run(context.WithoutCancel(ctx), 60*time.Second, "merge", "--abort")
	return MergeResult{Conflict: true, Detail: detail}
}

// Remove deletes the checkout. The branch is left alone: Merge decides
// whether it is still needed.
func (w *Worktree) Remove(ctx context.Context) error {
	if w == nil || w.repo == nil {
		return nil
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	if _, err := w.repo.run(ctx, 60*time.Second, "worktree", "remove", "--force", w.Dir); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}

// DeleteBranch removes a merged review branch. Unmerged branches need -D and
// are kept instead, so nothing is destroyed by accident.
func (r *Repo) DeleteBranch(ctx context.Context, branch string) {
	_, _ = r.run(ctx, 30*time.Second, "branch", "-d", branch)
}

// PruneWorktrees clears bookkeeping for checkouts that no longer exist, which
// is what a killed run leaves behind.
func (r *Repo) PruneWorktrees(ctx context.Context) {
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	_, _ = r.run(ctx, 60*time.Second, "worktree", "prune")
}

// CleanWorktreeRoot removes the per-review checkout directory when nothing is
// left in it, so a finished run leaves no trace in the reviewed tree.
// os.Remove on a non-empty directory fails, which is the intended guard.
func (r *Repo) CleanWorktreeRoot() {
	root := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
	_ = os.Remove(root)
	_ = os.Remove(filepath.Dir(root))
}

// branchSlug keeps a review name safe for a git ref.
func branchSlug(s string) string {
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

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}
