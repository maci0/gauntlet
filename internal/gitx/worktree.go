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
	return r.addBranchWorktree(ctx, dir, branch, base)
}

// AddStackWorktree creates the one checkout a stacked-PR run advances through
// its review branches. Unlike AddWorktree, the caller supplies the complete,
// deterministic branch name so a later invocation can recover the same stack.
func (r *Repo) AddStackWorktree(ctx context.Context, branch, tag, base string) (*Worktree, error) {
	if !Available() {
		return nil, errors.New("git is required for stacked PRs")
	}
	if _, err := r.run(ctx, gitQuick, "check-ref-format", "--branch", branch); err != nil {
		return nil, fmt.Errorf("invalid stack branch %q: %w", branch, err)
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	if err := r.ensureWorktreeRoot(); err != nil {
		return nil, err
	}

	dir := filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot), "stack-"+BranchSlug(tag))
	return r.addBranchWorktree(ctx, dir, branch, base)
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
	if err := r.prepareWorktreeDir(ctx, dir); err != nil {
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

// prepareWorktreeDir clears a leftover checkout at dir and creates its parent.
// Callers hold wtMu.
func (r *Repo) prepareWorktreeDir(ctx context.Context, dir string) error {
	_, _ = r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	return os.MkdirAll(filepath.Dir(dir), 0o755)
}

// reclaimEmptyBranch deletes branch when it still points at base, which means
// it carries no committed work. A leftover branch would fail worktree add: a
// hot reload continues the same run id while the successor's loop numbering
// restarts, so tags and their branches recur. One pointing anywhere else holds
// real output, and the same rule that keeps conflicted branches applies: fail
// rather than destroy.
func (r *Repo) reclaimEmptyBranch(ctx context.Context, branch, base string) error {
	tip, err := r.Tip(ctx, "refs/heads/"+branch)
	if err != nil {
		return nil
	}
	if tip != base {
		return fmt.Errorf("branch %s already exists at %s, not base %s; merge or delete it first",
			branch, shortSHA(tip), shortSHA(base))
	}
	// A failure here is not swallowed for long: the worktree add then fails
	// on the branch that is still there, with git's own words.
	_, _ = r.run(ctx, gitNormal, "branch", "-D", branch)
	return nil
}

func shortSHA(s string) string {
	return s[:min(12, len(s))]
}

// addBranchWorktree creates a checkout of base on a fresh branch at dir.
// Callers hold wtMu and have already proved the worktree root.
func (r *Repo) addBranchWorktree(ctx context.Context, dir, branch, base string) (*Worktree, error) {
	if err := r.prepareWorktreeDir(ctx, dir); err != nil {
		return nil, err
	}
	if err := r.reclaimEmptyBranch(ctx, branch, base); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", "-b", branch, dir, base); err != nil {
		r.abortWorktreeAdd(ctx, dir, branch)
		return nil, fmt.Errorf("git worktree add: %w", err)
	}
	return &Worktree{Dir: dir, Branch: branch, base: base, repo: r}, nil
}

// StartBranch advances a stack worktree onto a fresh child of base. The old
// branch remains: an open pull request still needs it locally and remotely.
//
// Calling it again with the same branch still at base checks that branch out
// rather than failing: a retry or a leftover from a killed attempt is the
// same state a first call produces. A branch that already carries commits is
// real output and is refused, matching reclaimEmptyBranch.
func (w *Worktree) StartBranch(ctx context.Context, branch, base string) error {
	if w == nil || w.repo == nil {
		return errors.New("nil stack worktree")
	}
	if _, err := w.repo.run(ctx, gitQuick, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("invalid stack branch %q: %w", branch, err)
	}
	sub := &Repo{Dir: w.Dir}
	if _, err := sub.run(ctx, gitNormal, "switch", "--quiet", "-c", branch, base); err != nil {
		tip, tipErr := w.repo.Tip(ctx, "refs/heads/"+branch)
		baseTip, baseErr := w.repo.Tip(ctx, base)
		if tipErr != nil || baseErr != nil {
			return fmt.Errorf("git switch -c %s: %w", branch, err)
		}
		if tip != baseTip {
			return fmt.Errorf("branch %s already exists at %s, not base %s; merge or delete it first",
				branch, shortSHA(tip), shortSHA(baseTip))
		}
		if _, swErr := sub.run(ctx, gitNormal, "switch", "--quiet", branch); swErr != nil {
			return fmt.Errorf("git switch -c %s: %w", branch, err)
		}
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
	paths, uErr := sub.unmergedPaths(ctx)
	if uErr != nil {
		return nil, fmt.Errorf("git merge --squash %s: %w", branch, err)
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

// StackBranchPrefix is the deterministic head of every branch name one stack
// layer can publish under: the 1-based schedule position keeps merge order
// visible and sortable, and the review stem says which pass wrote it. It is
// computable before the review runs, which is what lets a repeated invocation
// find layers it already published whatever topic they ended up named after.
func StackBranchPrefix(index int, review string) string {
	return fmt.Sprintf("review/%02d-%s", index+1, BranchSlug(review))
}

// StackProvisionalBranch names a layer before its commit exists, when there
// is no subject to take a topic from. The base-tip fragment keeps provisional
// branches of unrelated stacks (same repository, older base) from colliding
// on one name; the -wip- marker is what publication or a recovery pass
// renames away once the commit's subject is known.
func StackProvisionalBranch(baseTip string, index int, review string) string {
	tip := BranchSlug(baseTip)
	if len(tip) > 6 {
		tip = tip[:6]
	}
	return StackBranchPrefix(index, review) + "-wip-" + tip
}

// StackFinalBranch names a published layer after its commit subject, or ""
// when the subject yields no usable topic, in which case the layer keeps its
// provisional name.
func StackFinalBranch(index int, review, subject string) string {
	topic := TopicSlug(subject)
	if topic == "" {
		return ""
	}
	return StackBranchPrefix(index, review) + "-" + topic
}

// topicSlugMax bounds the topic fragment of a branch name. The subject it is
// cut from is capped elsewhere at 100 runes; a ref that long stops being
// something a reviewer can read in a branch list.
const topicSlugMax = 40

// TopicSlug distills a commit subject into the short topic a stack branch
// name carries. The conventional-commit type and scope repeat what the
// review stem already says, so they are dropped; what remains is lowercased
// and reduced to hyphenated words, cut at a word boundary. The output is
// always a valid ref fragment: lowercase alphanumerics and single interior
// hyphens carry none of the sequences check-ref-format refuses.
func TopicSlug(subject string) string {
	if head, rest, ok := strings.Cut(subject, ":"); ok && isConventionalType(head) {
		subject = rest
	}
	var b strings.Builder
	pending := false // a hyphen is owed only between words
	for _, r := range strings.ToLower(subject) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			need := 1
			if pending {
				need = 2
			}
			if b.Len()+need > topicSlugMax {
				return b.String()
			}
			if pending {
				b.WriteByte('-')
				pending = false
			}
			b.WriteRune(r)
		default:
			if b.Len() > 0 {
				pending = true
			}
		}
	}
	return b.String()
}

// isConventionalType recognizes the "type" or "type(scope)!" head of a
// conventional-commit subject, so TopicSlug drops it rather than spending the
// topic's budget repeating what the branch prefix already says.
func isConventionalType(head string) bool {
	head = strings.TrimSpace(head)
	if head == "" || len(head) > 30 {
		return false
	}
	for _, r := range head {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '(', r == ')', r == '-', r == '_', r == '!':
		default:
			return false
		}
	}
	return true
}

// RenameBranch moves the worktree's current branch to a new name, which is
// how a layer sheds its provisional name once its commit subject is known.
// The rename happens in the worktree so the checked-out HEAD follows it.
func (w *Worktree) RenameBranch(ctx context.Context, name string) error {
	if w == nil || w.repo == nil || w.Branch == "" {
		return errors.New("no branch to rename")
	}
	if name == w.Branch {
		return nil
	}
	if _, err := w.repo.run(ctx, gitQuick, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("invalid stack branch %q: %w", name, err)
	}
	sub := &Repo{Dir: w.Dir}
	// -m, never -M: a same-named branch holding real work is kept, and the
	// failure is reported, matching reclaimEmptyBranch's rule.
	if _, err := sub.run(ctx, gitNormal, "branch", "-m", w.Branch, name); err != nil {
		return fmt.Errorf("git branch -m %s %s: %w", w.Branch, name, err)
	}
	w.Branch = name
	return nil
}
