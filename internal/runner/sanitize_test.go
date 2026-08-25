// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import "testing"

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
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("safePaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
