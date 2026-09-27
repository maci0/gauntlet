// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import "github.com/maci0/gauntlet/internal/normalize"

// safePaths renders worktree paths for an error or log line. They come from
// git status against a possibly hostile tree: a file name may carry escape,
// control, or bidi characters that survive unquoteC's decoding, so anything
// headed for a message that is not sanitized downstream (a returned error the
// caller prints raw) is stripped here. Matching and own-artifact comparison
// still see the exact paths; only display text passes through this.
func safePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = normalize.Sanitize(p)
	}
	return out
}
