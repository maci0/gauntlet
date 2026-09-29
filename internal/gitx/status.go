// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

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
