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
}

var gitPath = sync.OnceValue(func() string {
	// Resolve on an absolute-only PATH so a planted ./git cannot run.
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, "git")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
})

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
}

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
	if out, err := r.run(context.Background(), 10*time.Second, "rev-parse", "HEAD"); err == nil {
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
	g := gitPath()
	if g == "" {
		return nil, exec.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := append(append([]string{}, safeConfig...), args...)
	cmd := exec.CommandContext(ctx, g, argv...)
	cmd.Dir = r.Dir
	cmd.Stdin = stdin
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	if err != nil {
		// Git explains itself on stderr; dropping it turns every failure into
		// a bare exit status that says nothing about the cause.
		if msg := strings.TrimSpace(errBuf.String()); msg != "" {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out.Bytes(), err
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

	diff, err := r.run(ctx, 10*time.Second, "diff", "--shortstat", r.baseline)
	if err != nil {
		return Stats{}, false
	}
	untracked, err := r.run(ctx, 10*time.Second, "ls-files", "--others", "--exclude-standard", "-z")
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
		st.Ins += countLines(p)
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

// countLines counts newlines in a regular file, refusing symlinks at open
// time. A planted symlink (to a FIFO, device, or out-of-tree file) must not be
// followed, and opening a writer-less FIFO would block forever.
func countLines(path string) int {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	// Clear O_NONBLOCK now that the file is known to be regular.
	_ = syscall.SetNonblock(fd, false)

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
			if first && bytes.IndexByte(buf[:min(c, 65536)], 0) >= 0 {
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
	out, err := sub.run(ctx, 30*time.Second, "diff", "--shortstat", from, to)
	if err != nil {
		return 0, 0, false
	}
	st := parseShortstat(out)
	return st.Ins, st.Del, true
}

// DirtyPaths returns worktree paths with uncommitted changes, excluding the
// runner's own artifacts (matched by real path, so a repo file merely named
// like one is still seen as a real change).
func (r *Repo) DirtyPaths(ctx context.Context, ownArtifacts map[string]bool) ([]string, error) {
	out, err := r.run(ctx, 10*time.Second,
		"-c", "core.quotePath=false",
		"status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var dirty []string
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
		dirty = append(dirty, p)
	}
	return dirty, nil
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
		10*time.Second, "check-ignore", "--stdin", "-z")
	if err != nil {
		var ee *exec.ExitError
		if !(errors.As(err, &ee) && ee.ExitCode() == 1) {
			return out // not a repository, or git broke: ignore nothing
		}
	}
	for p := range strings.SplitSeq(string(data), "\x00") {
		if p != "" {
			out[p] = true
		}
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
