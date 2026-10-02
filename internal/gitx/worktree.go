// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/runx"
	"github.com/maci0/gauntlet/internal/safefile"
)

// Worktree is a private checkout: its own directory and branch, cut from a
// known commit. Concurrent reviews never share a tree, so they cannot
// overwrite each other's edits or observe each other's half-done work. In
// --jobs mode it is a persistent lane reused across reviews; stacked-PR mode
// advances one through the stack. Results reach the main tree only through
// Merge.
//
// A Worktree belongs to the one goroutine that created it, one lane each, and
// its fields carry no lock of their own: repo.wtMu serializes git's worktree
// bookkeeping, not this struct. Sharing one across goroutines is not supported.
type Worktree struct {
	Dir    string // absolute path of the checkout
	Branch string
	base   string // the commit the checkout was cut from
	repo   *Repo
	sub    *Repo
}

// newWorktree builds the handle and the repository that drives the checkout.
// The sub-handle is made here rather than on first use, so no field of a
// published Worktree is ever lazily initialized: that check-then-act is the
// shape a race takes when a second goroutine ever reaches the same checkout.
func newWorktree(r *Repo, dir, branch, base string) *Worktree {
	w := &Worktree{Dir: dir, Branch: branch, base: base, repo: r}
	if dir == "" {
		return w
	}
	w.sub = &Repo{Dir: dir}
	if r != nil {
		// A linked worktree reads the same local config as the repository
		// it was cut from, so it adopts the parent's overlay and the
		// config identity that retires it, rather than resolving its own.
		w.sub.adoptSafeConfig(r)
	}
	return w
}

// subRepo is the handle that runs git inside the checkout. It is nil once the
// checkout is gone, which Remove reports by clearing Dir.
func (w *Worktree) subRepo() *Repo {
	if w == nil || w.Dir == "" {
		return nil
	}
	return w.sub
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
// idempotent, and nothing else in a run depends on it, so a failure is
// reported and the run continues: the cost of a skipped exclude is noisier git
// status output. A short write is a failure, though, not a success: the next
// run's entry check would not match a truncated line and would append the
// same entry again on every run.
func (r *Repo) ExcludeOwnArtifacts(ctx context.Context) error {
	out, err := r.run(ctx, gitQuick, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(r.Dir, gitDir)
	}
	path := filepath.Join(gitDir, "info", "exclude")
	// The exclude file itself is as plantable as its directory, so the read
	// goes through the one guarded open every repository-planted path in this
	// tree uses: safefile.OpenRead refuses a symlink, and following one would
	// read an arbitrary file's contents into the substring check below. A
	// missing file is the normal first-run case, and so is a refused symlink,
	// so the error is dropped and the text is read as empty either way. The
	// read is capped: an exclude file belongs in a repository that does not
	// ship one, and an oversized one is not read whole to look for two short
	// strings.
	var text string
	if f, fi, err := safefile.OpenRead(path); err == nil {
		if fi.Size() <= maxExcludeBytes {
			body, _ := io.ReadAll(io.LimitReader(f, maxExcludeBytes))
			text = string(body)
		}
		_ = f.Close()
	}
	var missing []string
	present := excludedEntries(text)
	for _, entry := range []string{"/" + worktreeRoot + "/", "/" + LockName} {
		if !present[entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// MkdirAll and the open both follow a symlink at any component, and the
	// reviewed tree picks gitDir: `.git` can be a symlink or a gitfile whose
	// path lands elsewhere, `.git/info` can be planted outright, and
	// `exclude` itself can be a link to any file on the machine. Appending
	// through any of them writes a line the operator did not ask for, into a
	// file outside the repository. The directory is re-checked, and
	// safefile.Append carries O_NOFOLLOW so the last component cannot be the
	// link either. A hardlink is a real regular file and a legitimate way to
	// share one exclude between worktrees, so only a non-regular descriptor
	// is refused.
	if !realDir(filepath.Dir(path)) {
		return fmt.Errorf("git exclude directory %s is not a real directory", filepath.Dir(path))
	}
	f, _, err := safefile.Append(path, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		prefix = "\n"
	}
	_, writeErr := fmt.Fprintf(f, "%s# gauntlet's own scratch: per-review worktrees and the run lock\n%s\n",
		prefix, strings.Join(missing, "\n"))
	// The close is where a full disk surfaces on an append that buffered, so
	// both failures are reported rather than the first one alone.
	return errors.Join(writeErr, f.Close())
}

// excludedEntries is the set of patterns an exclude file actually applies.
//
// git reads one pattern per line, so membership is a line that equals the
// entry. A substring search over the whole file is not the same question: a
// commented-out entry, a trailing comment on another pattern, or a longer
// pattern that happens to contain the entry all read as "already there", and
// the entry is then never written. Nothing appends it later either, since every
// later run makes the same wrong answer, so the scratch directory stays
// untracked for the life of the clone.
func excludedEntries(text string) map[string]bool {
	entries := map[string]bool{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if entry := strings.TrimSpace(line); entry != "" {
			entries[entry] = true
		}
	}
	return entries
}

// realDir reports whether path is an existing real directory. Lstat never
// reports a symlink as a directory, so a planted link fails the check. Callers
// use it before writing through a path whose components came out of the
// reviewed tree.
func realDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
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

// beginWorktreeAdd proves git is present, serializes worktree bookkeeping, and
// proves the scratch root, in that order, so no two runs shape the metadata at
// once. wtMu stays held afterwards: the caller unlocks it.
func (r *Repo) beginWorktreeAdd(withoutGit string) error {
	if !Available() {
		return errors.New(withoutGit)
	}
	r.wtMu.Lock()
	if err := r.ensureWorktreeRoot(); err != nil {
		r.wtMu.Unlock()
		return err
	}
	return nil
}

// worktreeRootDir is where per-review checkouts live in this repo.
func (r *Repo) worktreeRootDir() string {
	return filepath.Join(r.Dir, filepath.FromSlash(worktreeRoot))
}

// worktreeDir is the checkout named by its leaf under worktreeRootDir.
//
// The leaf is slugged here rather than trusted from the caller, so a name
// carrying "..", a separator, or a leading dash cannot place a checkout
// outside the scratch root or hand it to git as an option. Every caller in
// this package passes an already-slugged fragment, so the slug is a no-op for
// all of them; it is here because the guarantee belongs to the one function
// that turns a name into a path, not to each caller's discipline.
func (r *Repo) worktreeDir(name string) string {
	return filepath.Join(r.worktreeRootDir(), BranchSlug(name))
}

// AddWorktree creates a checkout of base on a fresh branch. The name identifies
// the checkout (a persistent lane, or a one-shot conflict resolver); the tag
// (run id plus loop and lane) keeps concurrent and repeated runs from colliding
// on branch names.
func (r *Repo) AddWorktree(ctx context.Context, name, tag, base string) (*Worktree, error) {
	if err := r.beginWorktreeAdd("git is required for parallel reviews"); err != nil {
		return nil, err
	}
	defer r.wtMu.Unlock()

	slug := BranchSlug(name)
	branch := LaneBranch(tag, slug)
	return r.addBranchWorktree(ctx, r.worktreeDir(tag+"-"+slug), branch, base)
}

// AddStackWorktree creates the one checkout a stacked-PR run advances through
// its review branches. Unlike AddWorktree, the caller supplies the complete,
// deterministic branch name so a later invocation can recover the same stack.
func (r *Repo) AddStackWorktree(ctx context.Context, branch, tag, base string) (*Worktree, error) {
	if err := r.beginWorktreeAdd("git is required for stacked PRs"); err != nil {
		return nil, err
	}
	defer r.wtMu.Unlock()
	if err := r.ValidateBranchName(ctx, branch); err != nil {
		return nil, err
	}
	return r.addBranchWorktree(ctx, r.worktreeDir("stack-"+BranchSlug(tag)), branch, base)
}

// AdoptStackWorktree reopens the scratch checkout a stacked run left in
// place. It returns nil when that directory is not there. A directory that
// is there but is not a registered worktree is an error: the caller must
// not replace it, because replacing it deletes the checkout.
func (r *Repo) AdoptStackWorktree(ctx context.Context, tag string) (*Worktree, error) {
	if err := r.beginWorktreeAdd("git is required for stacked PRs"); err != nil {
		return nil, err
	}
	defer r.wtMu.Unlock()
	dir := r.worktreeDir("stack-" + BranchSlug(tag))
	fi, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("stack worktree %s is not a directory", dir)
	}
	out, err := r.run(ctx, gitQuick, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if !worktreeListed(string(out), dir) {
		return nil, fmt.Errorf("stack worktree %s is not registered", dir)
	}
	return newWorktree(r, dir, "", ""), nil
}

// worktreeListed reports whether porcelain names dir. Git prints the path
// after resolving symlinks, and quotes it when the spelling needs quoting,
// so comparing the path this process built misses a checkout that is the
// same directory.
func worktreeListed(porcelain, dir string) bool {
	want := RealPath(dir)
	for line := range strings.SplitSeq(porcelain, "\n") {
		rest, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if RealPath(unquoteC(rest)) == want {
			return true
		}
	}
	return false
}

// AddSnapshotWorktree cuts a read-only view of one commit, detached so no
// branch is created or moved. Stacked runs discover project prompts and
// compute suggestions from this snapshot of the fetched remote base, never
// from the user's checkout: an uncommitted or local-only *-review.md must not
// steer a run that publishes only remote-based work.
func (r *Repo) AddSnapshotWorktree(ctx context.Context, tag, base string) (*Worktree, error) {
	if err := r.beginWorktreeAdd("git is required for a snapshot checkout"); err != nil {
		return nil, err
	}
	defer r.wtMu.Unlock()

	dir := r.worktreeDir("base-" + BranchSlug(tag))
	// A leftover snapshot from a killed run sits at this same deterministic
	// path; it is gauntlet's own and carries nothing, so it is replaced.
	if err := r.prepareWorktreeDir(ctx, dir); err != nil {
		return nil, err
	}
	if _, err := r.run(ctx, gitSlow, "worktree", "add", "--quiet", "--detach", dir, base); err != nil {
		return nil, errors.Join(fmt.Errorf("git worktree add: %w", err), r.abortWorktreeAdd(ctx, dir, ""))
	}
	if err := tightenCheckout(dir); err != nil {
		return nil, errors.Join(err, r.abortWorktreeAdd(ctx, dir, ""))
	}
	return newWorktree(r, dir, "", base), nil
}

// removeWorktreeDir removes a worktree checkout and its bookkeeping. It is
// idempotent: an already-removed checkout, a missing directory, or an orphaned
// checkout directory whose git metadata has already been pruned all converge
// to a clean state.
// Callers hold wtMu.
func (r *Repo) removeWorktreeDir(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	rel, err := filepath.Rel(r.worktreeRootDir(), dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove worktree path outside %s: %s", worktreeRoot, dir)
	}
	// A cancel during "git worktree add" can leave the entry locked;
	// unlock it and try removing before falling back to manual cleanup.
	_, _ = r.run(ctx, gitQuick, "worktree", "unlock", dir)
	// The directory goes whenever git's remove did not take it away. Only a
	// stat that says it is gone is proof of that: a stat that fails for any
	// other reason says nothing about the checkout, and reading it as gone
	// leaves a full copy of the repository on disk with nothing left naming
	// it. RemoveAll on an absent path is a no-op, so the stat only has to
	// rule that case out.
	_, removeErr := r.run(ctx, gitNormal, "worktree", "remove", "--force", dir)
	_, statErr := os.Stat(dir)
	if removeErr != nil || !errors.Is(statErr, fs.ErrNotExist) {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove worktree dir: %w", err)
		}
		_, _ = r.run(ctx, gitNormal, "worktree", "prune")
	}
	return nil
}

// abortWorktreeAdd drops a half-created checkout and, when named, its branch.
// A cancel or kill can land after git has registered the worktree and created
// the branch; both have to go. The caller still holds wtMu, and the context
// is kept alive so a cancelled add does not skip the cleanup.
//
// The failure is returned rather than dropped. A checkout of a possibly
// private tree that cannot be removed stays in the reviewed repository, and a
// branch that cannot be deleted stays in its ref list: the caller reports the
// add that failed, so it is the only place left to say what was left behind.
func (r *Repo) abortWorktreeAdd(ctx context.Context, dir, branch string) error {
	cleanCtx := context.WithoutCancel(ctx)
	var errs []error
	if err := r.removeWorktreeDir(cleanCtx, dir); err != nil {
		errs = append(errs, fmt.Errorf("cannot remove the partial checkout %s: %w", dir, err))
	}
	if branch != "" {
		if _, err := r.run(cleanCtx, gitNormal, "branch", "-D", "--", branch); err != nil {
			errs = append(errs, fmt.Errorf("cannot delete the branch %s left by the failed checkout: %w", branch, err))
		}
	}
	return errors.Join(errs...)
}

// ownerOnly is the mode a scratch directory this package creates under the
// reviewed repository is left at. The reviewed repository may be private, and
// a checkout of it is a second copy of that data on a machine that may have
// more than one local account: a directory left at 0755 hands every other user
// on the machine a readable copy of the whole tree. A directory created inside
// git's own layout, such as the .git/info holding the exclude file, is left
// at the mode git uses, because it holds no copy of the tree.
const ownerOnly = 0o700

// prepareWorktreeDir clears a leftover checkout at dir and creates its parent.
// Callers hold wtMu.
func (r *Repo) prepareWorktreeDir(ctx context.Context, dir string) error {
	if err := r.removeWorktreeDir(ctx, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), ownerOnly); err != nil {
		return err
	}
	// MkdirAll leaves a directory an earlier run already made at the mode it
	// was made with, so the components are narrowed rather than assumed.
	tightenWorktreeRoot(r.Dir)
	return nil
}

// tightenWorktreeRoot narrows the scratch directories under repo to their
// owner. Best effort: a component this account does not own cannot be chmod'ed,
// and best-effort is also what the run lock does with the file it finds
// already there.
func tightenWorktreeRoot(repo string) {
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return
	}
	dir := root
	for part := range strings.SplitSeq(worktreeRoot, "/") {
		dir = filepath.Join(dir, part)
		_ = os.Chmod(dir, ownerOnly)
	}
}

// tightenCheckout narrows one finished checkout, which git itself created at
// its own umask. Called only after a successful add: an aborted one is removed
// again, and the umask git ran under is not this package's to decide.
//
// A chmod that fails leaves a full copy of a possibly private repository
// readable by every account on the machine, and nothing else in the run would
// say so, so the failure is returned rather than swallowed.
func tightenCheckout(dir string) error {
	if err := os.Chmod(dir, ownerOnly); err != nil {
		return fmt.Errorf("cannot restrict %s to its owner: %w", dir, err)
	}
	return nil
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
	_, _ = r.run(ctx, gitNormal, "branch", "-D", "--", branch)
	return nil
}

// shortSHALen is how much of a commit id an error message shows. Enough to
// name a commit in `git log` without letting a full hash crowd out the message.
const shortSHALen = 12

func shortSHA(s string) string {
	return s[:min(shortSHALen, len(s))]
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
		return nil, errors.Join(fmt.Errorf("git worktree add: %w", err), r.abortWorktreeAdd(ctx, dir, branch))
	}
	if err := tightenCheckout(dir); err != nil {
		return nil, errors.Join(err, r.abortWorktreeAdd(ctx, dir, branch))
	}
	return newWorktree(r, dir, branch, base), nil
}

// StartBranch advances a stack worktree onto a fresh child of base. The old
// branch remains: an open pull request still needs it locally and remotely.
//
// Calling it again with the same branch still at base checks that branch out
// rather than failing: a retry or a leftover from a killed attempt is the
// same state a first call produces. A branch that already carries commits is
// real output and is refused, matching reclaimEmptyBranch.
func (w *Worktree) StartBranch(ctx context.Context, branch, base string) error {
	if w == nil || w.repo == nil || w.Dir == "" {
		return errors.New("nil stack worktree")
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	if err := w.repo.ValidateBranchName(ctx, branch); err != nil {
		return err
	}
	sub := w.subRepo()
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
			return fmt.Errorf("git switch %s: %w", branch, swErr)
		}
	}
	w.Branch, w.base = branch, base
	return nil
}

// DiscardCurrent resets an unpublished layer, detaches the checkout at its
// base, and deletes only that empty gauntlet branch. The next review can then
// start another child without any failed or no-change branch in the stack.
//
// A branch git refuses to delete is reported rather than dropped: the caller
// continues with deeper layers either way, and a leftover branch left silent
// here is one no later cleanup names, because by then the name is gone.
func (w *Worktree) DiscardCurrent(ctx context.Context) error {
	if w == nil || w.repo == nil || w.Branch == "" || w.Dir == "" {
		return nil
	}
	branch, base := w.Branch, w.base
	if err := w.ResetToBase(ctx); err != nil {
		return err
	}
	sub := w.subRepo()
	if _, err := sub.run(ctx, gitNormal, "switch", "--quiet", "--detach", base); err != nil {
		return fmt.Errorf("git switch --detach: %w", err)
	}
	delErr := w.repo.DeleteBranch(ctx, branch)
	// Cleared either way: the layer is gone from the stack's point of view,
	// and the name has to stay available so the next child can take it.
	w.Branch = ""
	return delErr
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
	if w == nil || w.repo == nil || w.Dir == "" {
		return false, errors.New("nil worktree")
	}
	sub := w.subRepo()
	if _, err := sub.run(ctx, gitNormal, "add", "-A"); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}
	// diff --cached --quiet exits 1 when something is staged. Any other
	// outcome means the answer was not read off healthy plumbing: committing
	// then would decide on broken state rather than on what is staged.
	clean, err := sub.nothingStaged(ctx)
	if err != nil {
		return false, err
	}
	if clean {
		return false, nil
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
	if w == nil || w.repo == nil || w.Dir == "" {
		return nil, errors.New("nil worktree")
	}
	sub := w.subRepo()
	out, err := sub.run(ctx, gitSlow, "merge", "--squash", "--no-verify", "--", branch)
	if err == nil {
		return nil, nil
	}
	paths, uErr := sub.unmergedPaths(ctx)
	if uErr != nil {
		return nil, fmt.Errorf("git merge --squash %s: %w (unmerged paths: %v)", branch, err, uErr)
	}
	if len(paths) == 0 {
		// The merge failed for a reason no editing can fix (a bad ref, a
		// checkout git refused). Report git's own words alongside the cause,
		// so a caller matching on the failure type still sees the narration.
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return nil, fmt.Errorf("git merge --squash %s: %w", branch, err)
		}
		return nil, fmt.Errorf("git merge --squash %s: %w: %s", branch, err, runx.FirstLine(detail))
	}
	return paths, nil
}

// CommitScope lists the paths CommitAll would stage in this checkout, tracked
// and untracked alike. It is the set a conflict-marker scan has to cover: the
// resolver edits the files git reported as conflicted, but it runs with the
// whole tree open, and `git add -A` commits whatever else it touched.
func (w *Worktree) CommitScope(ctx context.Context) ([]string, error) {
	if w == nil || w.repo == nil || w.Dir == "" {
		return nil, errors.New("nil worktree")
	}
	ch, err := w.subRepo().Status(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return slices.Concat(ch.Tracked, ch.Untracked), nil
}

// maxScanBytes bounds one file the marker scan reads whole. Everything larger
// is not source the resolver edited, and reading a hostile checkout's largest
// file into memory to search it for seven characters is a bad trade. Such a
// file counts as unresolved: an unread answer is not a clean one.
const maxScanBytes = 8 << 20

// maxExcludeBytes bounds .git/info/exclude when it is read to check for the
// two entries this run needs. The file is normally empty or a handful of
// lines, and a repository that ships a huge one is not read whole to search
// it for two short strings.
const maxExcludeBytes = 1 << 20

// readScanFile reads a file for the marker scan, refusing anything past
// maxScanBytes by size rather than by reading it first.
//
// The paths come from `git diff --name-only --diff-filter=U`, so a
// conflicted name is one the reviewed repository picked, and the agent that
// was asked to resolve it can leave anything at that name. safefile.OpenRead,
// not os.Open: a symlink planted there, or tracked in the checkout, would
// otherwise be read out of the tree, up to the scan limit.
func readScanFile(name string) ([]byte, error) {
	f, fi, err := safefile.OpenRead(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi.Size() > maxScanBytes {
		return nil, fmt.Errorf("file is %d bytes, over the %d-byte scan limit", fi.Size(), maxScanBytes)
	}
	return io.ReadAll(io.LimitReader(f, maxScanBytes))
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
	if w == nil || w.Dir == "" {
		return nil, errors.New("nil worktree")
	}
	var left []string
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return left, fmt.Errorf("conflict-marker scan stopped early: %w", err)
		}
		body, err := readScanFile(filepath.Join(w.Dir, filepath.FromSlash(p)))
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
	if w == nil || w.repo == nil || w.Dir == "" {
		return nil
	}
	sub := w.subRepo()
	if _, err := sub.run(ctx, gitNormal, "reset", "--hard", w.base, "--"); err != nil {
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
	if w == nil || w.repo == nil || w.Dir == "" {
		return errors.New("nil worktree")
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	sub := w.subRepo()
	if _, err := sub.run(ctx, gitNormal, "checkout", "--quiet", "--force", "--detach", newBase, "--"); err != nil {
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
// whether it is still needed. It is idempotent: a second call on an
// already-removed checkout is a safe no-op.
func (w *Worktree) Remove(ctx context.Context) error {
	if w == nil || w.repo == nil {
		return nil
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	dir := w.Dir
	if dir == "" {
		return nil
	}
	// Cleared only once the directory is gone: on a failure the handle is
	// the caller's only record of where the checkout is, and a retry that
	// found Dir empty would report success over a directory still on disk.
	if err := w.repo.removeWorktreeDir(ctx, dir); err != nil {
		return err
	}
	w.Dir = ""
	w.sub = nil
	return nil
}

// DeleteBranch force-removes a review branch. It is only ever called once the
// review's content is in a commit, or for a branch that never left its base,
// so an unmerged branch is deleted rather than kept.
//
// It takes wtMu like every other worktree-bookkeeping command: deleting a
// branch walks the registered worktrees (a branch checked out anywhere has to
// survive), and reading that metadata while AddWorktree or PruneWorktrees is
// halfway through its own update fails exactly the way their doc comment
// describes. The failure would be swallowed here, leaving the branch stranded.
//
// A branch that outlives its review is a leftover the operator has to find and
// delete by hand, so the error is returned rather than dropped: the wrapped
// message names the branch and carries git's own explanation. An empty branch
// name is a no-op, not a git invocation that always fails.
func (r *Repo) DeleteBranch(ctx context.Context, branch string) error {
	if branch == "" || r == nil || !Available() {
		return nil
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	// -D, not -d: a squashed branch is not "merged" as far as git is
	// concerned, though its content is in the commit that just landed. This
	// is only ever called after that commit succeeded, or for a branch that
	// never left its base.
	if _, err := r.run(ctx, gitNormal, "branch", "-D", "--", branch); err != nil {
		return fmt.Errorf("delete branch %s: %w", branch, err)
	}
	return nil
}

// DeleteBranchesMatching deletes every branch matching a glob pattern. It
// sweeps the review branches a cancelled lane may have left behind.
// Prunes stale worktree registrations first so a branch is not rejected as
// "checked out" in a worktree that was already removed from disk.
//
// A sweep that cannot list its own pattern has deleted nothing and says so.
// One that lists branches but fails to delete some still deletes the rest, and
// reports every branch that survived: a partial sweep reported as a clean one
// is how a run leaves a pile of review branches behind with nothing to show
// for it.
func (r *Repo) DeleteBranchesMatching(ctx context.Context, pattern string) error {
	if r == nil || !Available() {
		return nil
	}
	r.PruneWorktrees(ctx)
	names, err := r.listBranchesMatching(ctx, pattern)
	if err != nil {
		return fmt.Errorf("list branches matching %s: %w", pattern, err)
	}

	if len(names) == 0 {
		return nil
	}
	// One process for the whole sweep: a cancelled `--jobs` run leaves a branch
	// per lane, and a fork and an exec per branch is the dominant cost of
	// tidying up after it. `git branch -D` takes every name and deletes all it
	// can.
	r.wtMu.Lock()
	_, err = r.run(ctx, gitNormal, append([]string{"branch", "-D", "--"}, names...)...)
	r.wtMu.Unlock()
	if err == nil {
		return nil
	}
	// A batch that failed does not say which branch, or which of them are still
	// there: `git branch -D` deletes the branches it can and exits nonzero on
	// the rest. Re-listing is what separates a held branch from one the batch
	// already removed, so the report names every survivor and only the
	// survivors. A listing that fails leaves every name a candidate, which is
	// the walk this was before.
	pending := names
	if still, lErr := r.listBranchesMatching(ctx, pattern); lErr == nil {
		pending = nil
		for _, name := range names {
			if slices.Contains(still, name) {
				pending = append(pending, name)
			}
		}
	}
	var failed []error
	for _, name := range pending {
		if err := r.DeleteBranch(ctx, name); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// DeleteMergedBranchesMatching deletes the branches matching pattern that
// HEAD already contains and keeps every other one, returning how many it
// deleted. It is the sweep for branches a killed process left: one still at
// the base it was cut from carries nothing, while one with a commit HEAD lacks
// is work, and `git branch -d` refuses exactly those. A squash-landed branch
// is not merged as far as git is concerned, so it is kept too.
//
// listBranchesMatching, which does the selecting, names the local branches
// the pattern selects, one per line. The `--` it passes is what keeps an
// option-shaped pattern from reaching git as an option, which is why a
// pattern like "--pattern*" lists nothing rather than changing what the
// sweep does.
//
// This reads the branch list with `branch --list` rather than sharing
// localRefNames' for-each-ref, because the two glob differently and the
// sweeps need this one's: a sweep pattern like "gauntlet/<run>-*" has to
// select "gauntlet/<run>-lane0-00/a-review", whose slash lies past the
// star, and for-each-ref's star stops at a slash, so that branch would
// survive every sweep the run makes of it. `branch --list` treats the
// pattern as a path glob and matches through the separator.
func (r *Repo) DeleteMergedBranchesMatching(ctx context.Context, pattern string) (int, error) {
	if r == nil || !Available() {
		return 0, nil
	}
	r.PruneWorktrees(ctx)
	names, err := r.listBranchesMatching(ctx, pattern)
	if err != nil {
		return 0, fmt.Errorf("list branches matching %s: %w", pattern, err)
	}
	if len(names) == 0 {
		return 0, nil
	}
	r.wtMu.Lock()
	// The status is not an error to report: -d exits nonzero for every
	// branch it keeps, and keeping those is the point. The re-list below
	// is what says how many went.
	_, _ = r.run(ctx, gitNormal, append([]string{"branch", "-d", "--"}, names...)...)
	r.wtMu.Unlock()
	left, err := r.listBranchesMatching(ctx, pattern)
	if err != nil {
		return 0, fmt.Errorf("list branches matching %s: %w", pattern, err)
	}
	return len(names) - len(left), nil
}

// listBranchesMatching is `branch --list` over the pattern, one name per
// line with blanks dropped. See DeleteMergedBranchesMatching for why this is
// not localRefNames.
func (r *Repo) listBranchesMatching(ctx context.Context, pattern string) ([]string, error) {
	out, err := r.run(ctx, gitQuick, "branch", "--list", "--format=%(refname:short)", "--", pattern)
	if err != nil {
		return nil, err
	}
	return refNames(out), nil
}

// PruneWorktrees clears bookkeeping for checkouts that no longer exist, which
// is what a killed run leaves behind.
func (r *Repo) PruneWorktrees(ctx context.Context) {
	if r == nil || !Available() {
		return
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	_, _ = r.run(ctx, gitNormal, "worktree", "prune")
}

// SweepWorktreeRoot removes the checkout directories an interrupted run left
// under the worktree root. PruneWorktrees only drops git's bookkeeping for
// checkouts whose directory is already gone, and CleanWorktreeRoot's os.Remove
// refuses a non-empty root, so a single SIGKILL, OOM, or power cut would
// otherwise pin every checkout that run left -- each a full copy of the tree --
// for every run after it.
//
// The root is this package's own scratch, and the caller must hold the run lock
// on this directory: a second run in the same clone takes that lock before it
// cuts a checkout, so nothing under the root is in use here. Call it at
// startup only. DeleteBranchesMatching also prunes, mid-run, while the lanes
// are live, and sweeping there would delete the checkouts in flight.
func (r *Repo) SweepWorktreeRoot(ctx context.Context) {
	if r == nil || !Available() {
		return
	}
	r.wtMu.Lock()
	defer r.wtMu.Unlock()
	// The same proof AddWorktree makes: a planted `.gauntlet` or `worktrees`
	// symlink would otherwise make ReadDir list, and the loop below delete,
	// whatever it points at.
	if err := r.ensureWorktreeRoot(); err != nil {
		return
	}
	root := r.worktreeRootDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return // no root yet, or unreadable: nothing of ours to sweep
	}
	for _, e := range entries {
		// IsDir is false for a symlink, whatever it points at, so a link
		// planted in the reviewed tree is left alone rather than followed.
		if !e.IsDir() {
			continue
		}
		_ = r.removeWorktreeDir(ctx, filepath.Join(root, e.Name()))
	}
	// The root itself goes with its last entry. Failing means something is
	// still in it, which is the same signal CleanWorktreeRoot acts on.
	_ = os.Remove(root)
	_ = os.Remove(filepath.Dir(root))
}

// CleanWorktreeRoot removes the per-review checkout directory when nothing is
// left in it, so a finished run leaves no trace in the reviewed tree.
// os.Remove on a non-empty directory fails, which is the intended guard.
func (r *Repo) CleanWorktreeRoot() {
	if r == nil {
		return
	}
	root := r.worktreeRootDir()
	_ = os.Remove(root)
	_ = os.Remove(filepath.Dir(root))
}

// BranchSlug keeps a review name safe for a git ref.
//
// A git ref is UTF-8, not ASCII: check-ref-format forbids a fixed set of
// ASCII metacharacters and nothing else, so a review name the reviewed
// repository writes in Japanese, Korean, Arabic, Cyrillic, or Greek belongs
// in the ref whole. Mapping every rune outside [a-zA-Z0-9_-] to a hyphen, as
// this did, did not merely transliterate those names: it erased them. A
// review stem "日本語-review" slugged to "review", and so did "тест-review"
// and "مراجعة-review", so three unrelated reviews in one tree took the same
// lane branch, the same worktree directory, and the same merge scratch
// directory. The operator saw one review's work under another review's name.
//
// So the rule is now: keep a rune that is a letter or a digit in any script,
// keep '-' and '_', and replace everything else with a hyphen. Letters and
// digits are the two categories no reader can mistake for a ref separator,
// and the ASCII set is a subset of them, so an ASCII name slugs exactly as
// before and every existing branch and worktree directory is recoverable.
//
// NFC first, for the same reason a subject is composed before it is cut: a
// macOS filesystem hands out the decomposed spelling of a name it created
// that way, and "café" as one code point and as "e" plus a combining accent
// are one review with two refs. ValidateBranchName and check-ref-format are
// left to refuse whatever survives that is still not a legal ref.
func BranchSlug(s string) string {
	var b strings.Builder
	for _, r := range norm.NFC.String(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_',
			unicode.IsLetter(r), unicode.IsDigit(r):
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

// StackLoopPrefix is the deterministic head of every branch name one stack
// layer can publish under: the 1-based schedule position keeps merge order
// visible and sortable, and the review stem says which pass wrote it. It is
// computable before the review runs, which is what lets a repeated invocation
// find layers it already published whatever topic they ended up named after.
// Pass 1 keeps the historical review/<NN>-<review> form so a one-pass run
// and the first round of a multi-pass run recover the same branches. Later
// passes insert the loop number (review/<loop>-<NN>-<review>) so they cannot
// be mistaken for an earlier pass's layer.
func StackLoopPrefix(loop, index int, review string) string {
	if loop < 2 {
		return fmt.Sprintf("review/%02d-%s", index+1, BranchSlug(review))
	}
	return fmt.Sprintf("review/%02d-%02d-%s", loop, index+1, BranchSlug(review))
}

// StackLoopProvisionalBranch names a layer before its commit exists, when
// there is no subject to take a topic from. The base-tip fragment keeps
// provisional branches of unrelated stacks (same repository, older base)
// from colliding on one name; the -wip- marker is what publication or a
// recovery pass renames away once the commit's subject is known.
func StackLoopProvisionalBranch(baseTip string, loop, index int, review string) string {
	// baseTip is a full hash; only this much of it goes into a ref name, where
	// the rest would be noise nobody reads.
	const tipFragment = 6
	tip := BranchSlug(baseTip)
	if len(tip) > tipFragment {
		tip = tip[:tipFragment]
	}
	return StackLoopPrefix(loop, index, review) + "-wip-" + tip
}

// StackLoopFinalBranch names a published layer after its commit subject, or
// "" when the subject yields no usable topic, in which case the layer keeps
// its provisional name.
func StackLoopFinalBranch(loop, index int, review, subject string) string {
	topic := TopicSlug(subject)
	if topic == "" {
		return ""
	}
	return StackLoopPrefix(loop, index, review) + "-" + topic
}

// topicSlugMax bounds the topic fragment of a branch name. The subject a
// stack branch is named from arrives already clipped to 72 runes by the
// runner; a ref longer than that stops being something a reviewer can read in
// a branch list.
const topicSlugMax = 40

// TopicSlug distills a commit subject into the short topic a stack branch
// name carries. The conventional-commit type and scope repeat what the
// review stem already says, so they are dropped; what remains is lowercased
// and reduced to hyphenated words, cut at a word boundary. The output is
// always a valid ref fragment: letters and digits in any script, plus single
// interior hyphens, carry none of the sequences check-ref-format refuses.
//
// Letters and digits rather than [a-z0-9], for the reason BranchSlug gives:
// a repository whose history is in Japanese or Russian writes its subjects
// that way, and a hardcoded ASCII class returned "" for every one of them, so
// the layer shed no provisional name and the branch a reader opened stayed
// the "wip" one the recovery pass is meant to rename away. ToLower is
// Unicode's, not bytes.ToLower's, so it is the real lowercase of a Greek,
// Cyrillic, or Turkish letter.
func TopicSlug(subject string) string {
	if head, rest, ok := strings.Cut(subject, ":"); ok && isConventionalType(head) {
		subject = rest
	}
	var b strings.Builder
	pending := false // a hyphen is owed only between words
	for _, r := range strings.ToLower(norm.NFC.String(subject)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', unicode.IsLetter(r), unicode.IsDigit(r):
			// The budget is on the bytes of the ref fragment, so a rune is
			// measured by what it costs rather than counted as one. Charging
			// every rune one unit let a subject of two-byte Cyrillic overshoot
			// topicSlugMax by the number of such runes, and the fragment is
			// what lands in refs/heads. A rune that does not fit ends the
			// topic, which is what the ASCII-only loop did for every rune past
			// the budget.
			size := utf8.RuneLen(r)
			if size < 1 {
				size = 1
			}
			need := size
			if pending {
				need += 1
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
//
// The character test accepts a letter or digit in any script, for the reason
// TopicSlug does. Restricted to ASCII it read a Japanese or Russian subject
// as a subject with no conventional head, so the type stayed in the topic and
// the branch carried "修正:" where the branch prefix already named the review.
func isConventionalType(head string) bool {
	// conventionalTypeMax bounds what may be read as a type. A longer prefix is
	// prose that happens to precede a colon, and dropping its first word would
	// cut the subject, not a type.
	const conventionalTypeMax = 30
	head = strings.TrimSpace(head)
	if head == "" || utf8.RuneCountInString(head) > conventionalTypeMax {
		return false
	}
	for _, r := range head {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			unicode.IsLetter(r), unicode.IsDigit(r),
			r == '(', r == ')', r == '-', r == '_', r == '!':
		default:
			return false
		}
	}
	return true
}

// RenameBranch moves the worktree's current branch to a new name, which is
// how a layer sheds its provisional name once its commit subject is known.
// The rename happens in the worktree so the checked-out HEAD follows it.
//
// A repeat of a rename that already landed succeeds and the handle follows the
// new name, the same way Repo.RenameBranch converges: from is gone and name is
// there, which is what the first call produced.
func (w *Worktree) RenameBranch(ctx context.Context, name string) error {
	if w == nil || w.repo == nil || w.Branch == "" || w.Dir == "" {
		return errors.New("no branch to rename")
	}
	if name == w.Branch {
		return nil
	}
	if err := w.repo.ValidateBranchName(ctx, name); err != nil {
		return err
	}
	w.repo.wtMu.Lock()
	defer w.repo.wtMu.Unlock()
	if w.repo.renamedAlready(ctx, w.Branch, name) {
		w.Branch = name
		return nil
	}
	sub := w.subRepo()
	// -m, never -M: a same-named branch holding real work is kept, and the
	// failure is reported, matching reclaimEmptyBranch's rule.
	if _, err := sub.run(ctx, gitNormal, "branch", "-m", "--", w.Branch, name); err != nil {
		return fmt.Errorf("git branch -m %s %s: %w", w.Branch, name, err)
	}
	w.Branch = name
	return nil
}

// CommonDir returns the shared metadata directory, including from a linked
// worktree. Agents committing there need this directory writable as well.
func (r *Repo) CommonDir(ctx context.Context) (string, error) {
	out, err := r.run(ctx, gitQuick, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", errors.New("git returned an empty common directory")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Dir, path)
	}
	return filepath.EvalSymlinks(path)
}
