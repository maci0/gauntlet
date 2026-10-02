// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The tag guard compares the tag against every release this repository has
// published, and `gh release list` returns the 30 most recent ones unless the
// command says otherwise. A repository shipping faster than that outruns the
// default within a couple of releases, at which point the listing stops
// holding the newest published tag and the guard's `tail -1` names an older
// one: the new tag is then compared against a stale ceiling, which passes for
// a tag that is in fact older than what is already out there, and the
// immutability rule below refuses to put it right. So the ceiling is named
// rather than left at the tool's default, and read here rather than remembered.
//
// It lives in its own file because it is about the release workflow's own
// limits rather than about any one of its steps, and it is the one case in
// ci_test.go that a `gh` upgrade can invalidate silently.
func TestReleaseTagGuardReadsTheWholePublishedSet(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	i := strings.Index(text, "gh release list")
	if i < 0 {
		t.Fatal("release.yml has no gh release list; the guard reads no published set to compare the tag against")
	}
	line := text[i:]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	limit := regexp.MustCompile(`--limit[= ]+(\d+)`).FindStringSubmatch(line)
	if limit == nil {
		t.Fatalf("gh release list carries no --limit, so it reads gh's default of 30 releases:\n%s", line)
	}
	// Twice what this repository has published (80 `## <version>` headings in
	// CHANGELOG.md), and far under the API's 1000-per-page ceiling, so the
	// bound is headroom rather than a guess at today's count. Raise it as the
	// count grows; never lower it to what the count happens to be now.
	const published = 80
	n, err := strconv.Atoi(limit[1])
	if err != nil {
		t.Fatalf("--limit %q is not a number: %v", limit[1], err)
	}
	if n < published*2 {
		t.Errorf("gh release list reads %d releases; the guard compares the tag against the whole published set, and the tree names %d", n, published)
	}
}
