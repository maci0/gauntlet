// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gitx runs git safely against a possibly hostile repository and
// measures how much a review changed the working tree.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/runx"
)

// RealPath returns p as an absolute path with existing symlinks resolved.
func RealPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// gitPath resolves git once per PATH. The memo is keyed by the PATH it was
// built from for the same reason the agent resolver's is: a cache that
// outlives its input answers for a machine that no longer exists, and a cache
// filled from a PATH other than its key answers for one that never was.
var (
	gitMu       sync.Mutex
	gitPathSeen string
	gitPathFor  string
	gitPathOnce bool
)

func gitPath() string {
	path := runx.AbsPATH()
	gitMu.Lock()
	defer gitMu.Unlock()
	if gitPathOnce && gitPathSeen == path {
		return gitPathFor
	}
	gitPathSeen, gitPathFor, gitPathOnce = path, runx.LookPathIn(path, "git"), true
	return gitPathFor
}

// Available reports whether git itself was found. Found is not the whole
// question: the calls that take a ref or path the reviewed repository supplied
// separate it from the options with `--end-of-options` (git 2.24), and
// branches are created with `git switch` (2.23), so git older than 2.24
// answers "unknown option" where a broken repository would have answered
// something else. README states that floor.
func Available() bool { return gitPath() != "" }

// errGitUnavailable is what every entry point returns when git is missing, so
// a caller can tell "no git" from "git refused" with errors.Is.
var errGitUnavailable = errors.New("git is not available")

// ErrNotRepository is what a call against a directory git does not manage
// reports, so a caller can tell "this tree has no branch" from "git broke
// here" with errors.Is. The two look identical from the outside — no branch
// name, no merge targets, nothing staged — and a caller that renders them
// alike tells an operator their repository is clean when nothing was read.
var ErrNotRepository = errors.New("not a git repository")

// IsNotRepository reports whether err says the tree is not one git manages,
// rather than that the query failed on one it does.
func IsNotRepository(err error) bool { return errors.Is(err, ErrNotRepository) }

// classifyNotRepo maps git's "fatal: not a git repository" onto
// ErrNotRepository, so the answer travels with the tree it describes instead of
// living only in the message. git says it with exit status 128, which it also
// uses for a repository whose HEAD cannot be read, so the status alone cannot
// separate them and git's own wording is the only signal there is.
//
// The wording is matched against the error's message rather than
// ExitError.Stderr, because execGitEnv folds stderr into the message after
// redacting it (status.go's exitsWith unwraps to the *exec.ExitError for the
// same reason), which leaves the exit error's own capture empty. A caller that
// treats an unreadable HEAD alike is no worse off than before; the case this
// exists for is a git that failed some other way, which keeps its own error.
func classifyNotRepo(err error) error {
	if err == nil {
		return nil
	}
	ee, ok := errors.AsType[*exec.ExitError](err)
	if !ok || ee.ExitCode() != 128 {
		return err
	}
	if strings.Contains(err.Error(), "not a git repository") {
		return fmt.Errorf("%w: %w", ErrNotRepository, err)
	}
	return err
}

// Repo is a working tree git commands run against.
type Repo struct {
	Dir string

	// wtMu serializes worktree bookkeeping. Git validates every registered
	// worktree while adding or removing one, so two of those running at once
	// can trip over each other's half-deleted metadata:
	//
	//	fatal: failed to read .git/worktrees/<other>/commondir
	//
	// The operations take milliseconds, and getting one wrong strands a
	// branch or a checkout in the reviewed repo.
	wtMu sync.Mutex

	// baseMu guards the baseline and the flag that retires the probe. The
	// flag is set only by a probe that read a commit, so a probe that failed
	// (git not on PATH yet, a rev-parse that did not answer) is retried by
	// the next caller instead of pinning the handle to "no measurement" for
	// the life of the run.
	baseMu   sync.Mutex
	baseSet  bool
	baseline string

	mu       sync.Mutex
	lastAt   time.Time
	lastVal  Stats
	haveLast bool
	// lastOwn is the own-artifact set lastVal was measured under. The
	// debounced value is a function of that set as much as of the tree, so the
	// cache is only answered to a caller holding the same one: serving it to a
	// caller with a different set would report the previous set's artifacts as
	// the tree's lines. The set is one log file and one lock file per
	// directory, so comparing it is cheaper than a git walk and far cheaper
	// than a wrong number.
	lastOwn map[string]bool

	// lineCounts caches untracked-file line counts across samples, guarded by
	// mu. Sampling repeats for the life of a loop, and re-reading every
	// untracked file each time turns one dashboard number into a background
	// disk scan that grows as reviews add files. An entry is trusted only
	// while size and mtime still match, so an edited file recomputes. Files
	// that vanish or leave the untracked set are dropped so the cap is the
	// live working set. The table stops admitting new keys at
	// lineCountCacheMax rather than dropping that set.
	lineCounts map[string]lineCount

	// extraSafe is the per-repo -c overlay on top of safeConfig: attr.tree
	// pointed at the empty tree (so in-tree .gitattributes cannot select a
	// smudge filter or merge driver) and local filter/merge/diff commands
	// blanked, for git versions that ignore attr.tree.
	//
	// safeConfig names the local config file the overlay was derived from and
	// safeStamp is that file's size and mtime when it was. A review runs with
	// its permissions bypassed and can write .git/config itself, so a hostile
	// config is not only a property of the clone as unpacked: a driver planted
	// after the first git call would be executed by the next checkout or merge
	// with nothing blanking it. The overlay is therefore rebuilt when the file
	// it was read from changes, which is a stat per git call rather than the
	// two subprocesses a rebuild costs.
	//
	// safeWatched is the same check for the config files outside the
	// repository the overlay copied values from (the operator's own credential
	// helpers, read at system and global scope). The local config alone does
	// not cover them, and a helper the operator removed has to stop being
	// asserted rather than pinned into every later git call.
	safeMu      sync.Mutex
	extraSafe   []string
	safeReady   bool
	safeConfig  string
	safeStamp   configStamp
	safeWatched []watchedConfig

	// Now is the clock Sample debounces its cache against and stamps it
	// with. nil means time.Now. The debounce decides whether a sample is a
	// fresh walk or a cached value, so a run whose only injected clock is
	// wall time attributes lines to whichever review happened to cross the
	// interval. One handle per run, set once by the owner.
	Now func() time.Time
}

// now is the repo clock: the injected Now, or wall time. Nil-safe so a zero
// Repo can still be asked.
func (r *Repo) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// lineCount is one cached count and the stat it was measured against. mtime
// equality is the whole validation, so on a filesystem with coarse timestamps
// a same-size rewrite inside one tick can show stale counts for a sample;
// the numbers feed display only, which make(1) long ago decided this
// tradeoff is good enough for.
type lineCount struct {
	size    int64
	modTime time.Time
	lines   int
}

// lineCountCacheMax bounds the table. A var so tests can shrink it;
// production always sees 4096.
var lineCountCacheMax = 4096

// configStamp identifies one reading of a config file, local or watched. Size
// and mtime are the same pair countLinesCached trusts, and for the same reason: a
// rewrite inside one filesystem timestamp tick reads as unchanged, and the
// consequence there is one stale line count on a display number rather than a
// driver git executes.
type configStamp struct {
	size    int64
	modTime time.Time
}

// watchedConfig is one config file outside the repository that the cached
// overlay was derived from, and that file's identity when it was.
type watchedConfig struct {
	path  string
	stamp configStamp
}

// stampConfig reads a config file's identity. A file that cannot be read
// stamps as the zero value, so a config that is missing and one that cannot be
// opened do not read differently from a config that was never there.
func stampConfig(path string) configStamp {
	fi, err := os.Lstat(path)
	if err != nil {
		return configStamp{}
	}
	return configStamp{size: fi.Size(), modTime: fi.ModTime()}
}

// Open prepares a repo handle. The baseline commit used for line stats is
// resolved on first use, so callers that only run read-only git (status,
// list, check-ignore, the file-signal scan) do not pay for a rev-parse.
// Outside a repository (or without git) every stat call reports "unknown"
// and the runner silently omits line counts.
func Open(dir string) *Repo {
	return &Repo{Dir: dir}
}

// HasBaseline reports whether line stats are measurable here.
func (r *Repo) HasBaseline() bool {
	if r == nil {
		return false
	}
	r.ensureBaseline()
	r.baseMu.Lock()
	defer r.baseMu.Unlock()
	return r.baseline != ""
}

// ensureBaseline records HEAD once, and only once it has been read. Every
// caller that compares a commit against the baseline needs it; ListFiles,
// Status, and CheckIgnore share the handle and must not each spawn a git
// process for a commit they never compare against.
//
// The memo is keyed on having read a commit, not on having asked. A probe
// that failed describes a moment: git missing from PATH, a worktree whose
// HEAD is not written yet, a rev-parse that lost a race with a checkout.
// Retiring the handle on that answer would report "not measurable" for the
// rest of the run, and the dashboard shows n/a for a number the tree has had
// all along, so a failure is retried and only a sha retires the probe.
func (r *Repo) ensureBaseline() {
	r.baseMu.Lock()
	defer r.baseMu.Unlock()
	if r.baseSet {
		return
	}
	if !Available() {
		return
	}
	out, err := r.run(context.Background(), gitQuick, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return
	}
	r.baseline, r.baseSet = sha, true
}

// baselineSHA returns the commit line stats are measured against, "" when the
// probe has not read one.
func (r *Repo) baselineSHA() string {
	r.baseMu.Lock()
	defer r.baseMu.Unlock()
	return r.baseline
}

func (r *Repo) subRepo(dir string) *Repo {
	sub := &Repo{Dir: dir}
	if r != nil {
		// Adopt the parent's overlay rather than rebuilding it, and with it
		// the config paths and stamps that make the adoption
		// self-invalidating: a sub-repo of the same repository reads the same
		// config files, so sharing all four keeps one rebuild per change,
		// not one per handle.
		sub.adoptSafeConfig(r)
	}
	return sub
}

// adoptSafeConfig copies an already-computed overlay and the config identity it
// was derived from, so a handle that inherits the answer also inherits the
// check that retires it.
func (r *Repo) adoptSafeConfig(parent *Repo) {
	parent.safeMu.Lock()
	defer parent.safeMu.Unlock()
	// The parent's own validation happens in extraSafeConfig, which the
	// callers reach through this; take the same path so an inherited overlay
	// is never one generation staler than a computed one.
	r.extraSafe = parent.extraSafeConfigLocked()
	r.safeReady = true
	r.safeConfig = parent.safeConfig
	r.safeStamp = parent.safeStamp
	r.safeWatched = slices.Clone(parent.safeWatched)
}
