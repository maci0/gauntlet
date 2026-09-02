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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
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
	gitPathSeen, gitPathFor, gitPathOnce = path, resolveGit(path), true
	return gitPathFor
}

func resolveGit(rawPath string) string {
	// Resolve on an absolute-only PATH so a planted ./git cannot run.
	for _, dir := range filepath.SplitList(rawPath) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, "git")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// Available reports whether git itself was found.
func Available() bool { return gitPath() != "" }

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
	lastAt   time.Time
	lastVal  Stats
	haveLast bool

	// lineCounts caches untracked-file line counts across samples, guarded by
	// mu. Sampling repeats for the life of a loop, and re-reading every
	// untracked file each time turns one dashboard number into a background
	// disk scan that grows as reviews add files. An entry is trusted only
	// while size and mtime still match, so an edited file recomputes; files
	// that vanish leave their entries behind. The table stops admitting new
	// keys at lineCountCacheMax rather than dropping the working set.
	lineCounts map[string]lineCount

	// extraSafe is the per-repo -c overlay on top of safeConfig: attr.tree
	// pointed at the empty tree (so in-tree .gitattributes cannot select a
	// smudge filter or merge driver) and local filter/merge/diff commands
	// blanked, for git versions that ignore attr.tree. Computed once; a
	// hostile config is a property of the clone, not of one call.
	safeMu    sync.Mutex
	extraSafe []string
	safeReady bool
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

// Stats are cumulative worktree line changes against a baseline commit.
type Stats struct {
	Ins, Del int
}

// Open prepares a repo handle and records the baseline commit that line stats
// are measured against. Outside a repository (or without git) every stat call
// reports "unknown" and the runner silently omits line counts.
func Open(dir string) *Repo {
	r := &Repo{Dir: dir}
	if !Available() {
		return r
	}
	if out, err := r.run(context.Background(), gitQuick, "rev-parse", "HEAD"); err == nil {
		r.baseline = strings.TrimSpace(string(out))
	}
	return r
}

// HasBaseline reports whether line stats are measurable here.
func (r *Repo) HasBaseline() bool { return r != nil && r.baseline != "" }

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

// extraSafeConfig returns the per-repo -c flags, computing them once.
func (r *Repo) extraSafeConfig() []string {
	if r == nil {
		return nil
	}
	r.safeMu.Lock()
	defer r.safeMu.Unlock()
	if r.safeReady {
		return r.extraSafe
	}
	r.extraSafe = r.buildExtraSafe()
	r.safeReady = true
	return r.extraSafe
}

// buildExtraSafe must run with safeMu held and must not call extraSafeConfig:
// it bootstraps through execGit with only the static safeConfig.
func (r *Repo) buildExtraSafe() []string {
	var extra []string
	out, err := r.execGit(context.Background(), bytes.NewReader(nil), gitQuick,
		append(append([]string{}, safeConfig...), "hash-object", "-t", "tree", "--stdin")...)
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
		append(append([]string{}, safeConfig...), "config", "--local", "--list")...)
	if err == nil {
		extra = append(extra, disableLocalDrivers(string(list))...)
	}
	if extra == nil {
		return []string{}
	}
	return extra
}

// disableLocalDrivers blanks every local config key that names a program git
// would exec from .gitattributes or during a checkout/merge/diff. attr.tree
// already stops attributes from selecting them on git 2.40+; this is the
// fallback for older git, and covers a driver that config would invoke
// without an attribute (core.editor, gitProxy).
func disableLocalDrivers(listing string) []string {
	var extra []string
	for line := range strings.SplitSeq(listing, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" {
			continue
		}
		switch {
		case isFilterCommand(key), isMergeDriver(key), isDiffHelper(key),
			key == "core.gitproxy", key == "interactive.difffilter",
			key == "core.editor", key == "sequence.editor", key == "core.askpass":
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
	// PATH is the same absolute-only list resolveGit uses: GIT_SSH_COMMAND=ssh
	// looks up ssh on PATH, and a relative entry (notably ".") would pick up
	// a planted executable in the reviewed tree.
	cmd.Env = gitEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	// The deadline kill takes the whole process group down, not just the git
	// pid: git's own children (a hook, a merge driver) must not survive it as
	// orphans. WaitDelay then bounds the wait on the output pipes such a
	// child would still hold open. The same rules runProc and runIndexer
	// enforce on their own children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = waitGrace
	err := cmd.Run()
	if err != nil {
		// Git explains itself on stderr; dropping it turns every failure into
		// a bare exit status that says nothing about the cause. Userinfo is
		// stripped first: a remote stored as https://user:pass@host/... is
		// otherwise echoed verbatim into the error the runner prints and
		// journals.
		if msg := redactUserinfo(strings.TrimSpace(errBuf.String())); msg != "" {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out.Bytes(), err
}

// userinfoRe matches the userinfo of a URL (the "alice:token@" in
// https://alice:token@host/...), including ssh:// and git:// spellings git
// prints. git@host:path SSH syntax has no "://", so it is left alone.
var userinfoRe = regexp.MustCompile(`(?i)((?:https?|ssh|git|ftps?)://)[^/@\s'"]+@`)

// redactUserinfo strips URL userinfo from s so a credential-bearing remote
// does not land in an error string. Idempotent; strings with no "://" are
// returned unchanged.
func redactUserinfo(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	return userinfoRe.ReplaceAllString(s, "$1")
}

// gitEnv is os.Environ with cwd-relative PATH entries dropped and, unless the
// operator already exported one, GIT_SSH_COMMAND=ssh. Git's own helpers (ssh,
// a credential helper, diffie) inherit this, so a planted ./ssh cannot run.
func gitEnv() []string {
	env := os.Environ()
	abs := absPATH()
	out := make([]string, 0, len(env)+2)
	seenPATH := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+abs)
			seenPATH = true
			continue
		}
		out = append(out, kv)
	}
	if !seenPATH {
		out = append(out, "PATH="+abs)
	}
	if _, set := os.LookupEnv("GIT_SSH_COMMAND"); !set {
		out = append(out, "GIT_SSH_COMMAND=ssh")
	}
	return out
}

func absPATH() string {
	keep := make([]string, 0, 16)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" && filepath.IsAbs(dir) {
			keep = append(keep, dir)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

var (
	insRe = regexp.MustCompile(`(\d+) insertion`)
	delRe = regexp.MustCompile(`(\d+) deletion`)
)

// parseShortstat reads the counts out of a `git diff --shortstat` output.
func parseShortstat(out []byte) Stats {
	var st Stats
	if m := insRe.FindSubmatch(out); m != nil {
		st.Ins, _ = strconv.Atoi(string(m[1]))
	}
	if m := delRe.FindSubmatch(out); m != nil {
		st.Del, _ = strconv.Atoi(string(m[1]))
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
	if r.haveLast && time.Since(r.lastAt) < minSampleInterval {
		return r.lastVal, true
	}

	diff, err := r.run(ctx, gitQuick, "diff", "--shortstat", r.baseline)
	if err != nil {
		return Stats{}, false
	}
	untracked, err := r.run(ctx, gitQuick, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return Stats{}, false
	}

	st := parseShortstat(diff)
	for name := range bytes.SplitSeq(untracked, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		p := filepath.Join(r.Dir, string(name))
		if real, err := filepath.EvalSymlinks(p); err == nil && ownArtifacts[real] {
			continue
		}
		st.Ins += r.countLinesCached(p)
	}

	r.lastVal, r.lastAt, r.haveLast = st, time.Now(), true
	return st, true
}

// Invalidate drops the cached sample so the next call measures fresh. Called
// right before and after a review, where an up-to-date number matters more
// than the saved walk.
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
func openRegular(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	// O_NONBLOCK was only to refuse a planted FIFO; the line count reads
	// through this descriptor and must not get EAGAIN. There is nothing to
	// do if the kernel refuses.
	_ = syscall.SetNonblock(fd, false)
	return f, nil
}

// countLinesCached counts the newlines in a regular file through the repo's
// sample cache: an unchanged file (same size and mtime) returns its
// remembered count instead of being read again. Sample calls this for every
// untracked file every sample, so the cache is what keeps repeated sampling
// at stat cost.
func (r *Repo) countLinesCached(path string) int {
	f, err := openRegular(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
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
			n += bytes.Count(buf[:c], []byte{'\n'})
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

// DiffStat reports the lines changed between two commits, measured inside
// dir (a worktree of this repo). Unlike a shared-tree sample, this is exact:
// the range covers one review's own commit and nothing else.
func (r *Repo) DiffStat(ctx context.Context, dir, from, to string) (ins, del int, ok bool) {
	sub := &Repo{Dir: dir}
	out, err := sub.run(ctx, gitNormal, "diff", "--shortstat", from, to)
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
		return nil, errors.New("git is not available")
	}
	sub := &Repo{Dir: dir}
	out, err := sub.run(ctx, gitNormal, "diff", "--name-only", "--no-renames", "-z", from, to)
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
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := porcelainPath(line)
		if p == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(filepath.Join(r.Dir, p)); err == nil && ownArtifacts[real] {
			continue
		}
		visit(line, p)
	}
	return nil
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

// porcelainPath extracts the worktree path from a `git status --porcelain`
// line (XY <path>, or the destination of a `orig -> dest` rename).
func porcelainPath(line string) string {
	if len(line) <= 3 {
		return ""
	}
	entry := line[3:]
	// The " -> " arrow is a rename/copy marker carried by the index-side
	// status (R or C); an untracked file may legitimately have it in its name.
	if (line[0] == 'R' || line[0] == 'C') && strings.Contains(entry, " -> ") {
		_, entry, _ = strings.Cut(entry, " -> ")
	}
	return unquoteC(strings.TrimSpace(entry))
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
		switch e := body[i]; {
		case e == 'a':
			b.WriteByte('\a')
			i++
		case e == 'b':
			b.WriteByte('\b')
			i++
		case e == 'f':
			b.WriteByte('\f')
			i++
		case e == 'n':
			b.WriteByte('\n')
			i++
		case e == 'r':
			b.WriteByte('\r')
			i++
		case e == 't':
			b.WriteByte('\t')
			i++
		case e == 'v':
			b.WriteByte('\v')
			i++
		case e == '\\' || e == '"':
			b.WriteByte(e)
			i++
		case e >= '0' && e <= '7' && i+2 < len(body) &&
			body[i+1] >= '0' && body[i+1] <= '7' &&
			body[i+2] >= '0' && body[i+2] <= '7':
			b.WriteByte((e-'0')<<6 | (body[i+1]-'0')<<3 | (body[i+2] - '0'))
			i += 3
		default:
			b.WriteByte('\\')
			b.WriteByte(e)
			i++
		}
	}
	return b.String()
}

// ListFiles returns the repository's files, relative to its root and in git's
// own idea of what belongs: tracked files plus untracked ones that are not
// ignored. It is what a tree scan should walk, since the repo already declares
// which directories are build output and which are dependencies.
func (r *Repo) ListFiles(ctx context.Context) ([]string, error) {
	if r == nil || !Available() {
		return nil, errors.New("git is not available")
	}
	out, err := r.run(ctx, gitSlow, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// ChangedSince returns the files touched by commits in the given window, as
// git accepts it for --since ("90 days ago"). It says which parts of a tree
// are alive: a directory nobody has edited in a quarter is not where the next
// review should look.
func (r *Repo) ChangedSince(ctx context.Context, since string) ([]string, error) {
	if r == nil || !Available() {
		return nil, errors.New("git is not available")
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
	var paths []string
	for field := range strings.SplitSeq(string(out), "\x00") {
		if field != "" {
			paths = append(paths, field)
		}
	}
	return paths
}
