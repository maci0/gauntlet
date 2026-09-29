// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"bytes"
	"context"
	"strings"
	"time"
)

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

// ChangedSince returns the files touched by commits at or after cutoff. It
// says which parts of a tree are alive: a directory nobody has edited in a
// quarter is not where the next review should look.
//
// The window is an instant on the repo's own clock, not a relative phrase.
// git resolves "--since=90 days ago" against its own wall clock, so two
// callers an hour apart, or one caller replayed a seeded run on a later day,
// get different churn over the same tree; the cutoff has to be a value the
// caller computed from the clock the run is already driving everything else
// from. r.now() supplies that clock, and the cutoff is carried in UTC so the
// result does not shift with the machine's zone.
func (r *Repo) ChangedSince(ctx context.Context, cutoff time.Time) ([]string, error) {
	if r == nil || !Available() {
		return nil, errGitUnavailable
	}
	since := cutoff.UTC().Format(time.RFC3339)
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
