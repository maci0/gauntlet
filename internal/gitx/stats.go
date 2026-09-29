// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Stats are cumulative worktree line changes against a baseline commit.
type Stats struct {
	Ins, Del int
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
// these to one another and writes the sum to the journal, so one 20-digit
// figure that parsed as a huge positive count would be reported as a review
// that deleted nine quintillion lines. Zero is the miss: it is the value a
// misparse and an absent count already read as.
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

// countReadBytes is the size of one read chunk over an untracked file.
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
// shared across callers holding the same own-artifact set; a caller with
// another set is measured afresh rather than handed the previous set's number.
//
// git diff never sees untracked files, but reviews are told to add tests (new
// files), so their lines are counted as insertions.
func (r *Repo) Sample(ctx context.Context, ownArtifacts map[string]bool) (Stats, bool) {
	if !r.HasBaseline() {
		return Stats{}, false
	}
	// Read the sha before taking r.mu: a baseline is retired exactly once,
	// so the value read here is the one the diff below is taken against.
	base := r.baselineSHA()
	r.mu.Lock()
	defer r.mu.Unlock()
	// The >= 0 half matters: a clock stepped backwards leaves lastAt in the
	// future, and a sample from a future cache entry would report a tree the
	// reviews have not produced yet. The set comparison matters for the same
	// reason from the other side: the cached value was measured under one set
	// of own artifacts, and a caller holding another is owed a measurement of
	// its own.
	if r.haveLast && sameArtifactSet(r.lastOwn, ownArtifacts) {
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
		diff, diffErr = r.run(ctx, gitQuick, "diff", "--shortstat", base, "--")
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
	// A copy, not the caller's map: the run adds a directory's lock file to
	// the one map it shares with every other directory's runner, and a stored
	// reference would then describe a set that never existed.
	r.lastOwn = maps.Clone(ownArtifacts)
	return st, true
}

// sameArtifactSet reports whether two own-artifact sets name the same paths.
// Two empty sets are the same set: an empty one owns nothing, so a measurement
// taken under it counts every untracked file, which is what the next caller
// holding an empty set wants too.
func sameArtifactSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for p, own := range a {
		if b[p] != own {
			return false
		}
	}
	return true
}

// Invalidate drops the cached sample so the next call measures fresh. Called
// right before and after a review, and around a merge or a rebase, where an
// up-to-date number matters more than the saved walk.
func (r *Repo) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.haveLast, r.lastOwn = false, nil
	r.mu.Unlock()
}

// errNotRegular is a descriptor that stat says is not a regular file, which a
// no-follow open still admits when the target itself is a FIFO or a device.
var errNotRegular = errors.New("not a regular file")

// openRegularFD adopts fd and returns it only if it is a regular file. Both
// hardened openers below share it: they differ in their flags, not in what
// they accept afterwards. The descriptor is closed on every error path, so a
// caller never has to.
func openRegularFD(fd int, path string) (*os.File, os.FileInfo, error) {
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errNotRegular
	}
	return f, fi, nil
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
	f, fi, err := openRegularFD(fd, path)
	if err != nil {
		return nil, nil, err
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
	// A hardlink is a real regular file and a legitimate way to share one
	// exclude between worktrees, so only a non-regular descriptor is refused.
	f, _, err := openRegularFD(fd, path)
	if err != nil {
		return nil, err
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
	// Answer from the cache off an Lstat, before opening. A cached file is one
	// the previous sample already read, so this is the whole cost of it: the
	// open is not skipped for a hit, and a no-follow open of an unchanged tree
	// spends three times the syscalls to learn what one stat says. A symlink
	// carries the link's own size and mtime, which never match a cached regular
	// file, so it falls through to the open that refuses it.
	if li, err := os.Lstat(path); err == nil {
		if e, ok := r.lineCounts[path]; ok && li.Mode().IsRegular() &&
			e.size == li.Size() && e.modTime.Equal(li.ModTime()) {
			return e.lines
		}
	}
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
