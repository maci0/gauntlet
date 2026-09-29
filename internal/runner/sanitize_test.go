// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"testing"

	"github.com/maci0/gauntlet/internal/humanize"
)

// Paths from git status against a hostile tree can carry escape sequences,
// control bytes, and bidi overrides once unquoteC has decoded their C-style
// quoting. They end up inside errors the caller prints raw, so safePaths must
// strip everything able to drive or spoof a terminal while leaving visible
// text alone.
func TestSafePathsStripsHostileBytes(t *testing.T) {
	got := safePaths([]string{
		"normal/file.go",
		"evil\x1b]0;pwned\x07.md",
		"a\u202eb\u200dc.md",
	})
	want := []string{
		"normal/file.go",
		"evil]0;pwned.md",
		"abc.md",
	}
	if len(got) != len(want) {
		t.Fatalf("safePaths returned %d paths, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("safePaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// safePathList names a few paths and counts the rest. It has to read exactly
// as the full sanitize followed by humanize.List did, including the count: a
// short list, an empty one, and a list longer than the limit with a hostile
// name in the part that only the count covers.
func TestSafePathListMatchesFullSanitize(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		limit int
	}{
		{"empty", nil, pathListLimit},
		{"under limit", []string{"a.go", "b.go"}, pathListLimit},
		{"at limit", []string{"a.go", "b.go", "c.go"}, pathListLimit},
		{
			"over limit",
			[]string{"a.go", "b\x1b]0;pwned\x07.go", "c.go", "d\u202eb.go", "e.go"},
			pathListLimit,
		},
		{"limit below one", []string{"a.go"}, 0},
	}
	for _, c := range cases {
		want := humanize.List(safePaths(c.paths), c.limit)
		if got := safePathList(c.paths, c.limit); got != want {
			t.Errorf("%s: safePathList = %q, want %q", c.name, got, want)
		}
	}
}

// PathList names the first few of a dirty tree across both its lists and
// counts the rest, so it has to read the same as DisplayPaths rendered for one
// line. The split point falls inside the untracked half here, which is the
// case a bounded read of the tracked half alone would get wrong.
func TestStackDirtyPathListMatchesDisplayPaths(t *testing.T) {
	dirty := &StackDirtyError{
		Dir:       "wt",
		Tracked:   []string{"a.go", "b.go", "c.go"},
		Untracked: []string{"d.go", "e\u202ef.go", "f.go", "g.go"},
	}
	want := humanize.List(dirty.DisplayPaths(), pathListLimit)
	if got := dirty.PathList(); got != want {
		t.Errorf("PathList = %q, want %q", got, want)
	}
}
