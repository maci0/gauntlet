// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gitx runs git safely against a possibly hostile repository and
// measures how much a review changed the working tree.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
// deadline. A var only so tests can shrink it; production always sees the
// default.
var waitGrace = 10 * time.Second

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

// gitPath resolves git once per PATH. The memo is keyed by the PATH it was
// built from for the same reason the agent resolver's is: a cache that
// outlives its input answers for a machine that no longer exists.
var (
	gitMu       sync.Mutex
	gitPathSeen string
	gitPathFor  string
	gitPathOnce bool
)

func gitPath() string {
	path := os.Getenv("PATH")
	gitMu.Lock()
	defer gitMu.Unlock()
	if gitPathOnce && gitPathSeen == path {
		return gitPathFor
	}
	gitPathSeen, gitPathFor, gitPathOnce = path, runx.LookPath("git"), true
	return gitPathFor
}

// Available reports whether git itself was found. Found is not the whole
// question: the calls below separate options with `--end-of-options` (git
// 2.24) and create branches with `git switch` (2.23), so git older than 2.24
// answers "unknown option" where a broken repository would have answered
// something else. README states that floor.
func Available() bool { return gitPath() != "" }

// errGitUnavailable is what every entry point returns when git is missing, so
// a caller can tell "no git" from "git refused" with errors.Is.
var errGitUnavailable = errors.New("git is not available")

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

	mu       sync.Mutex
	baseline string
	baseOnce sync.Once
	lastAt   time.Time
	lastVal  Stats
	haveLast bool

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

// Stats are cumulative worktree line changes against a baseline commit.
type Stats struct {
	Ins, Del int
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
	return r.baseline != ""
}

// ensureBaseline records HEAD once. Sample is the only caller that needs it;
// ListFiles, Status, and CheckIgnore share the handle and must not each
// spawn a git process for a commit they never compare against.
func (r *Repo) ensureBaseline() {
	r.baseOnce.Do(func() {
		if !Available() {
			return
		}
		if out, err := r.run(context.Background(), gitQuick, "rev-parse", "HEAD"); err == nil {
			r.baseline = strings.TrimSpace(string(out))
		}
	})
}

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
// args (tests that re-enable a hook) still wins, matching the previous
// "last -c wins" contract.
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
// helpers back behind the blank.
func disableLocalDrivers(listing string) []string {
	var extra []string
	for line := range strings.SplitSeq(listing, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" {
			continue
		}
		switch {
		case isFilterCommand(key), isMergeDriver(key), isDiffHelper(key),
			strings.EqualFold(key, "credential.helper"),
			strings.EqualFold(key, "core.gitproxy"),
			strings.EqualFold(key, "interactive.difffilter"),
			strings.EqualFold(key, "core.editor"),
			strings.EqualFold(key, "sequence.editor"),
			strings.EqualFold(key, "core.askpass"):
			extra = append(extra, "-c", key+"=")
		case strings.HasPrefix(key, "filter.") && strings.HasSuffix(key, ".required"):
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
		// a bare exit status that says nothing about the cause. Userinfo is
		// stripped first: a remote stored as https://user:pass@host/... is
		// otherwise echoed verbatim into the error the runner prints and
		// journals.
		if msg := runx.RedactUserinfo(strings.TrimSpace(errBuf.String())); msg != "" {
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

var (
	insRe   = regexp.MustCompile(`(\d+) insertion`)
	delRe   = regexp.MustCompile(`(\d+) deletion`)
	nulByte = []byte{0}
	nlByte  = []byte{'\n'}
)

// maxPlausibleCount bounds a line count read out of git output. A diff of
// this many lines does not exist; the bound is what keeps a misparse a
// misparse. See parseCount.
const maxPlausibleCount = 1 << 40

// parseCount reads one line count out of git's output. A count that does not
// fit, or that clears maxPlausibleCount, is no count at all: the caller adds
// these to one another and writes the sum to the journal, and strconv.Atoi
// hands back the clamped maximum next to its range error rather than zero, so
// a 20-digit figure would arrive as 2^63-1, wrap negative the moment the
// untracked files' own counts are added to it, and be reported as a review
// that deleted nine quintillion lines.
func parseCount(b []byte) int {
	n, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil || n > maxPlausibleCount || n > uint64(math.MaxInt) {
		return 0
	}
	return int(n)
}

// parseShortstat reads the counts out of a `git diff --shortstat` output.
func parseShortstat(out []byte) Stats {
	var st Stats
	if m := insRe.FindSubmatch(out); m != nil {
		st.Ins = parseCount(m[1])
	}
	if m := delRe.FindSubmatch(out); m != nil {
		st.Del = parseCount(m[1])
	}
	return st
}

// untrackedLineCap bounds the per-file line count of untracked files. This
// stat runs repeatedly for the life of the loop, so one huge untracked file
// must not make every sample re-read gigabytes.
const untrackedLineCap = 8 << 20

// countReadBytes is the size of one read chunk.
const countReadBytes = 1 << 20

// binarySniffBytes is how much of a file's head is checked for a NUL. git's
// own binary heuristic looks at a prefix, not the whole file; a NUL later
// than this still leaves a line count, which is the same tradeoff.
const binarySniffBytes = 64 << 10

// lineBufs recycles read buffers across the untracked-file walk. Sample runs
// every few hundred milliseconds and touches every untracked file each time,
// so a fresh 1 MiB per file would hand the GC tens of megabytes of garbage
// per sample while reviews accumulate new files.
var lineBufs = sync.Pool{
	New: func() any {
		buf := make([]byte, countReadBytes)
		return &buf
	},
}

// minSampleInterval debounces sampling. Two lanes finishing together, or a
// review that ends in under a second, must not each pay for a full git walk.
const minSampleInterval = 750 * time.Millisecond

// Sample returns cumulative (insertions, deletions) since the baseline, and
// whether the measurement is available at all. Results are cached briefly and
// shared across callers.
//
// git diff never sees untracked files, but reviews are told to add tests (new
// files), so their lines are counted as insertions.
func (r *Repo) Sample(ctx context.Context, ownArtifacts map[string]bool) (Stats, bool) {
	if !r.HasBaseline() {
		return Stats{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The >= 0 half matters: a clock stepped backwards leaves lastAt in the
	// future, and a sample from a future cache entry would report a tree the
	// reviews have not produced yet.
	if r.haveLast {
		if since := r.now().Sub(r.lastAt); since >= 0 && since < minSampleInterval {
			return r.lastVal, true
		}
	}

	// The two queries are independent reads of the same tree: running them
	// together cuts sample latency in half, and they only hold r.mu because
	// the cached result they fill is shared. extraSafeConfig and gitPath
	// serialize themselves.
	var diff, untracked []byte
	var diffErr, lsErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		diff, diffErr = r.run(ctx, gitQuick, "diff", "--shortstat", r.baseline, "--")
	})
	wg.Go(func() {
		untracked, lsErr = r.run(ctx, gitQuick, "ls-files", "--others", "--exclude-standard", "-z")
	})
	wg.Wait()
	if diffErr != nil || lsErr != nil {
		return Stats{}, false
	}

	st := parseShortstat(diff)
	var live []string
	for name := range bytes.SplitSeq(untracked, nulByte) {
		if len(name) == 0 {
			continue
		}
		p := filepath.Join(r.Dir, string(name))
		if isOwnArtifact(ownArtifacts, p) {
			continue
		}
		live = append(live, p)
	}
	r.pruneLineCounts(live)
	for _, p := range live {
		st.Ins += r.countLinesCached(p)
	}

	r.lastVal, r.lastAt, r.haveLast = st, r.now(), true
	return st, true
}

// Invalidate drops the cached sample so the next call measures fresh. Called
// right before and after a review, and around a merge or a rebase, where an
// up-to-date number matters more than the saved walk.
func (r *Repo) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.haveLast = false
	r.mu.Unlock()
}

// openRegular opens path read-only, refusing symlinks at open time. A planted
// symlink (to a FIFO, device, or out-of-tree file) must not be followed, and
// opening a writer-less FIFO would block forever. O_NONBLOCK is cleared once
// the descriptor is known to be a regular file. O_CLOEXEC keeps the descriptor
// out of a child that forks while the read is in flight.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("not a regular file")
	}
	// O_NONBLOCK was only to refuse a planted FIFO; the line count reads
	// through this descriptor and must not get EAGAIN. There is nothing to
	// do if the kernel refuses.
	_ = syscall.SetNonblock(fd, false)
	return f, fi, nil
}

// openAppendNoFollow opens path for appending, creating it at perm if it does
// not exist, and refusing a symlink at the final path component. os.OpenFile
// has no O_NOFOLLOW, and a component the reviewed repository picks can be a
// link: without the flag the append lands in whatever the link points at.
func openAppendNoFollow(path string, perm os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		uint32(perm))
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	// A hardlink is a real regular file and a legitimate way to share one
	// exclude between worktrees, so only a non-regular descriptor is refused.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}

// pruneLineCounts drops entries that are not in this sample's untracked
// set. Reviews commit or delete files they created; without this those
// paths occupy the cap forever and later untracked files are never cached.
func (r *Repo) pruneLineCounts(live []string) {
	if len(r.lineCounts) == 0 {
		return
	}
	keep := make(map[string]struct{}, len(live))
	for _, p := range live {
		keep[p] = struct{}{}
	}
	for p := range r.lineCounts {
		if _, ok := keep[p]; !ok {
			delete(r.lineCounts, p)
		}
	}
}

// countLinesCached counts the newlines in a regular file through the repo's
// sample cache: an unchanged file (same size and mtime) returns its
// remembered count instead of being read again. Sample calls this for every
// untracked file every sample, so the cache is what keeps repeated sampling
// at stat cost.
func (r *Repo) countLinesCached(path string) int {
	f, fi, err := openRegular(path)
	if err != nil {
		delete(r.lineCounts, path)
		return 0
	}
	defer f.Close()
	size, mod := fi.Size(), fi.ModTime()
	if e, ok := r.lineCounts[path]; ok && e.size == size && e.modTime.Equal(mod) {
		return e.lines
	}
	n := countLinesFrom(f)
	if r.lineCounts == nil {
		r.lineCounts = make(map[string]lineCount)
	}
	if _, exists := r.lineCounts[path]; exists || len(r.lineCounts) < lineCountCacheMax {
		r.lineCounts[path] = lineCount{size: size, modTime: mod, lines: n}
	}
	return n
}

// countLinesFrom counts newlines on an open regular file.
func countLinesFrom(f *os.File) int {
	bp := lineBufs.Get().(*[]byte)
	defer lineBufs.Put(bp)
	buf := *bp
	n, read := 0, 0
	var last byte
	truncated := false
	first := true
	for {
		if read >= untrackedLineCap {
			truncated = true
			break
		}
		c, err := f.Read(buf)
		if c > 0 {
			if first && bytes.IndexByte(buf[:min(c, binarySniffBytes)], 0) >= 0 {
				return 0 // binary: no line count to speak of
			}
			first = false
			n += bytes.Count(buf[:c], nlByte)
			read += c
			last = buf[c-1]
		}
		if err != nil {
			break
		}
	}
	// Count like git: a final line with no trailing newline still counts.
	if !truncated && last != 0 && last != '\n' {
		n++
	}
	return n
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

// DiffStat reports the lines changed between two commits, measured inside
// dir (a worktree of this repo). Unlike a shared-tree sample, this is exact:
// the range covers one review's own commit and nothing else.
func (r *Repo) DiffStat(ctx context.Context, dir, from, to string) (ins, del int, ok bool) {
	sub := r.subRepo(dir)
	out, err := sub.run(ctx, gitNormal, "diff", "--shortstat", "--end-of-options", from, to, "--")
	if err != nil {
		return 0, 0, false
	}
	st := parseShortstat(out)
	return st.Ins, st.Del, true
}

// ChangedFiles lists the paths a commit range touches, measured inside dir (a
// worktree of this repo). DiffStat says how much a layer changed; this says
// where, which is what a reader needs to tell what a change is about before
// opening the diff. Renames are not followed: both names are places someone
// has to look. Output is NUL-separated, so a path git would otherwise quote
// arrives intact.
func (r *Repo) ChangedFiles(ctx context.Context, dir, from, to string) ([]string, error) {
	if !Available() {
		return nil, errGitUnavailable
	}
	sub := r.subRepo(dir)
	out, err := sub.run(ctx, gitNormal, "diff", "--name-only", "--no-renames", "-z", "--end-of-options", from, to, "--")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// Changes splits what git status reports by whether git is tracking the path.
// The distinction decides what may block worktree isolation: a modification
// git tracks is work a review would neither see nor merge, while an untracked
// file simply sits where it is, reviewed by nobody and in nobody's way.
type Changes struct {
	Tracked   []string
	Untracked []string
}

// statusPorcelain runs `git status --porcelain` and hands every nonempty
// entry to visit as (raw line, path), skipping entries this run owns. Both
// readers of git status share it so their parse cannot drift apart.
func (r *Repo) statusPorcelain(ctx context.Context, ownArtifacts map[string]bool,
	visit func(line, path string)) error {

	out, err := r.run(ctx, gitQuick,
		"-c", "core.quotePath=false",
		"status", "--porcelain")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := porcelainPath(line)
		if p == "" {
			continue
		}
		if isOwnArtifact(ownArtifacts, filepath.Join(r.Dir, p)) {
			continue
		}
		visit(line, p)
	}
	return nil
}

// isOwnArtifact reports whether p is one of the run's own artifacts. The real
// path is consulted too, so a symlink pointing into the run's own tree is
// recognized as well as a plain match. An empty set owns nothing.
func isOwnArtifact(ownArtifacts map[string]bool, p string) bool {
	if len(ownArtifacts) == 0 {
		return false
	}
	if ownArtifacts[p] {
		return true
	}
	real, err := filepath.EvalSymlinks(p)
	return err == nil && ownArtifacts[real]
}

// Status reports the working tree's changes, excluding the runner's own
// artifacts, split by whether git tracks them.
func (r *Repo) Status(ctx context.Context, ownArtifacts map[string]bool) (Changes, error) {
	var ch Changes
	err := r.statusPorcelain(ctx, ownArtifacts, func(line, p string) {
		if strings.HasPrefix(line, "??") {
			ch.Untracked = append(ch.Untracked, p)
		} else {
			ch.Tracked = append(ch.Tracked, p)
		}
	})
	if err != nil {
		return Changes{}, err
	}
	return ch, nil
}

// DirtyPaths returns worktree paths with uncommitted changes, excluding the
// runner's own artifacts (matched by real path, so a repo file merely named
// like one is still seen as a real change).
func (r *Repo) DirtyPaths(ctx context.Context, ownArtifacts map[string]bool) ([]string, error) {
	var dirty []string
	err := r.statusPorcelain(ctx, ownArtifacts, func(_, p string) {
		dirty = append(dirty, p)
	})
	return dirty, err
}

// exitsWith reports whether err is git exiting with the given status. The
// wrapper runIn builds still unwraps down to the *exec.ExitError, so the
// status survives the stderr that gets folded into the message.
func exitsWith(err error, code int) bool {
	ee, ok := errors.AsType[*exec.ExitError](err)
	return ok && ee.ExitCode() == code
}

// nothingStaged reads the index and reports whether it holds no change.
// diff --cached --quiet exits 1 when something is staged and 0 when nothing
// is; any other outcome means the answer was not read off healthy plumbing,
// which is an error rather than an answer. Both callers commit on the
// result, so they must agree on which reading means what.
func (r *Repo) nothingStaged(ctx context.Context) (bool, error) {
	_, err := r.run(ctx, gitNormal, "diff", "--cached", "--quiet")
	switch {
	case err == nil:
		return true, nil
	case exitsWith(err, 1):
		return false, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet: %w", err)
}

// CheckIgnore returns the subset of paths git ignores in this tree. Without
// git, or outside a repository, nothing counts as ignored: prompt discovery
// then treats every candidate as legitimate instead of failing the run.
//
// The invocation is the same hardened one every other git call uses: resolved
// on an absolute-only PATH so a planted ./git cannot run, with the repo's own
// config prevented from executing anything.
func (r *Repo) CheckIgnore(ctx context.Context, paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return out
	}
	data, err := r.runIn(ctx, strings.NewReader(strings.Join(paths, "\x00")),
		gitQuick, "check-ignore", "--stdin", "-z")
	if err != nil && !exitsWith(err, 1) {
		return out // not a repository, or git broke: ignore nothing
	}
	for _, p := range splitNUL(data) {
		out[p] = true
	}
	return out
}

// arrow separates a rename's source from its destination in porcelain v1.
const arrow = " -> "

// porcelainPath extracts the worktree path from a `git status --porcelain`
// line (XY <path>, or the destination of a `orig -> dest` rename).
func porcelainPath(line string) string {
	line = strings.TrimRight(line, "\r")
	if len(line) <= 3 {
		return ""
	}
	entry := line[3:]
	// The arrow is a rename/copy marker carried by the index-side status (R or
	// C); an untracked file may legitimately have it in its name.
	if line[0] == 'R' || line[0] == 'C' {
		if i := renameSep(entry); i >= 0 {
			entry = entry[i+len(arrow):]
		}
	}
	return unquoteC(entry)
}

// renameSep returns where the arrow separating a rename's source from its
// destination begins, or -1. A file name may contain the arrow and the space
// around it, so the first one is not always the separator: git renames
// `a -> b.txt` to `c.txt` as "a -> b.txt" -> c.txt. A path holding a space is
// always quoted, so a leading quote is what says the source ends there.
func renameSep(entry string) int {
	if !strings.HasPrefix(entry, `"`) {
		return strings.Index(entry, arrow)
	}
	for i := 1; i < len(entry); i++ {
		switch entry[i] {
		case '\\':
			i++ // an escaped byte cannot be the closing quote
		case '"':
			if !strings.HasPrefix(entry[i+1:], arrow) {
				return -1
			}
			return i + 1
		}
	}
	return -1
}

// unquoteC reverses git's C-style path quoting. The status call runs with
// core.quotePath=false, so UTF-8 bytes arrive raw, but a path holding a
// control character, a quote, or a backslash still arrives wrapped in double
// quotes with C escapes inside. Without decoding, a name would surface as the
// literal text `caf\303\251.md` instead of café.md and never match its real
// path again. An unrecognized escape is kept verbatim rather than invented.
func unquoteC(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(body) {
			b.WriteByte(c)
			break
		}
		i++
		switch e := body[i]; e {
		case 'a':
			b.WriteByte('\a')
			i++
		case 'b':
			b.WriteByte('\b')
			i++
		case 'f':
			b.WriteByte('\f')
			i++
		case 'n':
			b.WriteByte('\n')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'v':
			b.WriteByte('\v')
			i++
		case '\\', '"':
			b.WriteByte(e)
			i++
		case '0', '1', '2', '3':
			if i+2 < len(body) &&
				body[i+1] >= '0' && body[i+1] <= '7' &&
				body[i+2] >= '0' && body[i+2] <= '7' {
				b.WriteByte((e-'0')<<6 | (body[i+1]-'0')<<3 | (body[i+2] - '0'))
				i += 3
			} else {
				b.WriteByte('\\')
				b.WriteByte(e)
				i++
			}
		default:
			b.WriteByte('\\')
			b.WriteByte(e)
			i++
		}
	}
	return b.String()
}

// ListFilesAtMost returns the repository's files, relative to its root and in
// git's own idea of what belongs: tracked files plus untracked ones that are
// not ignored. It is what a tree scan should walk, since the repo already
// declares which directories are build output and which are dependencies.
// The listing stops after n paths: a scan that will only look at the first
// hundred thousand files must not keep the rest of a million-file listing
// alive as substrings of one giant string. n <= 0 lists all of them.
func (r *Repo) ListFilesAtMost(ctx context.Context, n int) ([]string, error) {
	return r.listFiles(ctx, n)
}

// ListFilesMatching is ListFilesAtMost restricted to a git pathspec (a glob
// matched against the basename when it contains no slash). Prompt discovery
// asks for "*-review.md" so a large tree is not walked just to find a handful
// of files. An empty glob lists everything.
func (r *Repo) ListFilesMatching(ctx context.Context, glob string) ([]string, error) {
	if glob == "" {
		return r.listFiles(ctx, 0)
	}
	return r.listFiles(ctx, 0, glob)
}

func (r *Repo) listFiles(ctx context.Context, limit int, pathspec ...string) ([]string, error) {
	if r == nil || !Available() {
		return nil, errGitUnavailable
	}
	args := make([]string, 0, 6+len(pathspec))
	args = append(args, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if len(pathspec) > 0 {
		args = append(args, "--")
		args = append(args, pathspec...)
	}
	out, err := r.run(ctx, gitSlow, args...)
	if err != nil {
		return nil, err
	}
	if limit > 0 {
		return splitNULAtMost(out, limit), nil
	}
	return splitNUL(out), nil
}

// ChangedSince returns the files touched by commits in the given window, as
// git accepts it for --since ("90 days ago"). It says which parts of a tree
// are alive: a directory nobody has edited in a quarter is not where the next
// review should look.
func (r *Repo) ChangedSince(ctx context.Context, since string) ([]string, error) {
	if r == nil || !Available() {
		return nil, errGitUnavailable
	}
	out, err := r.run(ctx, gitSlow, "log", "--since="+since, "--name-only",
		"--no-renames", "--pretty=format:", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// splitNUL splits git's -z output, dropping the empty records its formats
// leave between entries (`git log --pretty=format:` writes one at every
// commit boundary).
//
// A record is taken exactly as git wrote it. -z exists so a path survives
// byte for byte, and git carries a leading or trailing space in a file name
// like any other character: trimming here would turn " notes.md" into
// "notes.md", which names nothing on disk. A stacked PR body would then list
// a file the commit did not touch, and the suggester's tree listing would key
// its file signals on a name the tree does not have.
func splitNUL(out []byte) []string {
	// One backing string for every record: the listing is already in memory,
	// and substrings of it beat a copy per path. Count the NULs so the slice
	// is sized once; git -z usually terminates the last record, but a missing
	// terminator still fits in +1.
	paths := make([]string, 0, bytes.Count(out, nulByte)+1)
	for field := range strings.SplitSeq(string(out), "\x00") {
		if field != "" {
			paths = append(paths, field)
		}
	}
	return paths
}

// splitNULAtMost copies at most n records out of git's -z output. Unlike
// splitNUL it does not alias the input, so the caller can drop the rest of a
// huge listing without the kept paths pinning it.
func splitNULAtMost(out []byte, n int) []string {
	if n <= 0 {
		return nil
	}
	paths := make([]string, 0, n)
	start := 0
	for i := 0; i <= len(out); i++ {
		if i < len(out) && out[i] != 0 {
			continue
		}
		if i > start {
			paths = append(paths, string(out[start:i]))
			if len(paths) == n {
				return paths
			}
		}
		start = i + 1
	}
	return paths
}
