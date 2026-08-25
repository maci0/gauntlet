// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package prompt

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// CHANGELOG.md states the consumer contract: review names and review set
// names are API. Removing or renaming one is breaking and waits for the next
// major version; additions may land in a minor. These snapshots turn drift
// into a failed test, so every change to the surface is conscious, lands in
// the same commit as its changelog entry, and gets the right bump kind.
//
// The 1.0.1 release removed a public package without either: this file is the
// guard against doing that again.

// goldenReviewNames is the bundled review surface at the time of writing.
var goldenReviewNames = []string{
	"a11y-review", "agentrules-review", "api-review", "arch-review",
	"authz-review", "build-review", "cache-review", "cli-review",
	"code-review", "compat-review", "concurrency-review", "config-review",
	"container-review", "db-review", "deps-review", "design-review",
	"doc-review", "dr-review", "dst-review", "dx-review", "error-review",
	"functionality-review", "fuzz-review", "i18n-review",
	"idempotency-review", "infra-review", "lint-review", "llm-review",
	"minimalism-review", "mobile-review", "numerics-review",
	"o11y-review", "perf-review", "pkg-review", "privacy-review",
	"prompt-review", "release-review", "resource-review", "sdk-review",
	"sec-review", "skills-review", "slop-review", "specs-review",
	"test-review", "threat-review", "time-review", "uislop-review",
	"unicode-review", "ux-review", "webperf-review",
}

// goldenSetNames is the set surface: the named sets plus the two computed
// ones (--reviews all / --reviews project).
var goldenSetNames = []string{
	"agents", "all", "backend", "frontend", "project", "quick",
	"security", "shipping", "standard",
}

func TestBundledReviewNamesMatchTheContract(t *testing.T) {
	assertSurfaceUnchanged(t, "bundled review name", goldenReviewNames, bundledNames())
}

func TestSetNamesMatchTheContract(t *testing.T) {
	assertSurfaceUnchanged(t, "review set name", goldenSetNames, SetNames())
}

// A set member naming a review that no longer exists is dropped silently when
// the set expands, so renaming a review would quietly shrink every set it
// belongs to instead of failing loudly. Members must always resolve.
func TestEverySetMemberIsABundledReview(t *testing.T) {
	bundled := make(map[string]bool, len(goldenReviewNames))
	for _, n := range bundledNames() {
		bundled[n] = true
	}
	for set, members := range Sets {
		for _, m := range members {
			if !bundled[m] {
				t.Errorf("set %q lists %q, which is not a bundled review; "+
					"a renamed or removed review silently shrinks the set "+
					"(fix the set, or treat the rename as breaking per "+
					"CHANGELOG.md)", set, m)
			}
		}
	}
}

// assertSurfaceUnchanged fails with the SemVer consequence spelled out,
// separating removals from additions so the required bump kind is obvious.
func assertSurfaceUnchanged(t *testing.T, what string, want, got []string) {
	t.Helper()
	removed, added := nameDiff(want, got)
	if len(removed) == 0 && len(added) == 0 {
		return
	}
	t.Fatalf("the %s surface changed; CHANGELOG.md's consumer contract makes these names API\n  removed: %s\n  added:   %s\nremovals and renames are breaking and wait for the next major version; additions may land in a minor. Record the change in CHANGELOG.md and update the snapshot in this test in the same commit.",
		what, quoteAll(removed), quoteAll(added))
}

func nameDiff(want, got []string) (missing, extra []string) {
	unseen := make(map[string]bool, len(want))
	for _, n := range want {
		unseen[n] = true
	}
	for _, n := range got {
		if unseen[n] {
			delete(unseen, n)
			continue
		}
		extra = append(extra, n)
	}
	for n := range unseen {
		missing = append(missing, n)
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}
