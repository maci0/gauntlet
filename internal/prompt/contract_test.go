// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package prompt

import (
	"fmt"
	"os"
	"path/filepath"
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
	"container-review", "db-review", "ddd-review", "deps-review", "design-review",
	"doc-review", "dr-review", "dst-review", "dx-review", "error-review",
	"functionality-review", "fuzz-review", "gitops-review", "helm-review",
	"i18n-review", "idempotency-review", "infra-review", "k8s-review",
	"lint-review", "llm-review",
	"minimalism-review", "mobile-review", "numerics-review",
	"o11y-review", "perf-review", "perfectionism-review",
	"pkg-review", "privacy-review",
	"prompt-review", "release-review", "resource-review", "sdk-review",
	"sec-review", "skills-review", "slop-review", "specs-review",
	"test-review", "threat-review", "time-review", "uislop-review",
	"unicode-review", "ux-review", "webperf-review",
}

// goldenSetNames is the set surface: the named sets plus the two computed
// ones (--reviews all / --reviews project).
var goldenSetNames = []string{
	"agents", "all", "backend", "frontend", "gitops", "project", "quick",
	"security", "shipping", "standard",
}

func TestBundledReviewNamesMatchTheContract(t *testing.T) {
	assertSurfaceUnchanged(t, "bundled review name", goldenReviewNames, BundledNames())
}

func TestSetNamesMatchTheContract(t *testing.T) {
	assertSurfaceUnchanged(t, "review set name", goldenSetNames, SetNames())
}

// A set member naming a review that no longer exists is dropped silently when
// the set expands, so renaming a review would quietly shrink every set it
// belongs to instead of failing loudly. Members must always resolve.
func TestEverySetMemberIsABundledReview(t *testing.T) {
	bundled := make(map[string]bool, len(goldenReviewNames))
	for _, n := range BundledNames() {
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

// The README's review grid is the front page's only complete list of what
// gauntlet ships, so a prompt added, renamed, or resummarized without it goes
// out as a README that undersells or lies. Nothing generates the table at
// build time; this rebuilds it and diffs, and the failure prints the block to
// paste back between the markers.
func TestReadmeGridMatchesBundled(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	const begin, end = "<!-- BEGIN REVIEWS", "<!-- END REVIEWS -->"
	_, rest, ok := strings.Cut(string(readme), begin)
	if !ok {
		t.Fatalf("README.md has no %q marker", begin)
	}
	if _, rest, ok = strings.Cut(rest, "\n"); !ok {
		t.Fatalf("README.md %q marker has no line after it", begin)
	}
	got, _, ok := strings.Cut(rest, end)
	if !ok {
		t.Fatalf("README.md has no %q marker", end)
	}

	set, _, err := Discover(t.Context(), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteString("| Review | Finds |\n| --- | --- |\n")
	for _, name := range BundledNames() {
		r, found := set.Get(name)
		if !found {
			t.Fatalf("bundled review %q is not in the discovered set", name)
		}
		fmt.Fprintf(&want, "| `%s` | %s |\n", name, r.Summary())
	}
	if strings.TrimSpace(got) != strings.TrimSpace(want.String()) {
		t.Errorf("README.md review grid is stale; replace it between the markers with:\n\n%s", want.String())
	}
}

// The install snippet resolves a tag, then must fetch that tag's asset. Using
// releases/latest/download after the resolve can pair a newly published tag
// name with the previous binary if a release lands between the two curls.
//
// The assertion is on the download base, not on one line of the snippet: the
// script assigns that base to a variable so the binary and checksums.txt come
// from the same directory, and a test that demanded the whole URL spelled out
// inline would fail on a script that is right.
func TestReadmeInstallPinsTheResolvedTag(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(readme)
	if strings.Contains(text, "releases/latest/download/") {
		t.Fatal("README install fetches from releases/latest/download/; use releases/download/v${ver} so the binary matches the tag just resolved")
	}
	// The block may spell the URL through a base variable, so require the tag
	// to appear in it and the asset to be fetched from that base. Demanding one
	// exact line would fail a correct snippet that composes the URL in two
	// steps, which is how the install block is written. The base is matched
	// without a trailing slash: the snippet builds one base URL and joins the
	// asset name onto it, so ${ver} has to be interpolated into the path.
	if !strings.Contains(text, "releases/download/v${ver}") {
		t.Fatal("README install must fetch from releases/download/v${ver} so the binary matches the tag just resolved")
	}
	// Pinning the base only helps if every download goes through it.
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "curl") && strings.Contains(line, "github.com/maci0/gauntlet/releases") {
			t.Errorf("README install fetches a hardcoded release URL, bypassing the resolved tag: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(text, `"$base/$asset"`) {
		t.Fatal("README install must download the asset from the resolved tag's base URL")
	}
}
