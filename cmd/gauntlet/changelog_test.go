// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/prompt"
)

// changelogGroups is Keep a Changelog's impact headings, the vocabulary
// CHANGELOG.md actually uses. The release workflow dumps a version section
// verbatim as GitHub notes, so a repeated ### Fixed ships as duplicate
// headings instead of one list.
var changelogGroups = []string{
	"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security",
}

func TestChangelogSectionsAreWellFormed(t *testing.T) {
	text := readChangelog(t)
	var (
		section   string
		seenGroup = map[string]int{}
		prevVer   *changelogVersion
		sawFirst  bool
	)
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		switch {
		case strings.HasPrefix(line, "### "):
			if section == "" {
				t.Fatalf("CHANGELOG.md:%d: %q before any ## version heading", n, line)
			}
			group := strings.TrimPrefix(line, "### ")
			if !isChangelogGroup(group) {
				t.Fatalf("CHANGELOG.md:%d: unknown heading %q; want one of %s",
					n, line, strings.Join(changelogGroups, ", "))
			}
			if prev, ok := seenGroup[group]; ok {
				t.Fatalf("CHANGELOG.md:%d: %q repeats in %s (first at line %d); add another bullet, not another heading",
					n, line, section, prev)
			}
			seenGroup[group] = n
		case strings.HasPrefix(line, "## "):
			title := strings.TrimPrefix(line, "## ")
			if !sawFirst {
				if title != "Unreleased" {
					t.Fatalf("CHANGELOG.md:%d: first version heading is %q, want ## Unreleased", n, line)
				}
				sawFirst = true
				section = line
				seenGroup = map[string]int{}
				continue
			}
			ver, ok := parseChangelogVersion(title)
			if !ok {
				t.Fatalf("CHANGELOG.md:%d: %q is not ## Unreleased or ## X.Y.Z; the release workflow matches those exact forms",
					n, line)
			}
			if prevVer != nil && !ver.less(*prevVer) {
				t.Fatalf("CHANGELOG.md:%d: versions must descend: %s follows %s",
					n, title, prevVer)
			}
			prevVer = &ver
			section = line
			seenGroup = map[string]int{}
		}
	}
	if !sawFirst {
		t.Fatal("CHANGELOG.md has no ## Unreleased heading")
	}
}

// A name on the environment-variable contract is something a consumer can
// set. If it is not in CHANGELOG.md, the addition or rename shipped without
// notes, which is how GH_TOKEN would have gone out undocumented.
func TestChangelogMentionsEveryContractEnvVar(t *testing.T) {
	text := readChangelog(t)
	for _, name := range goldenEnvVars {
		if !strings.Contains(text, name) {
			t.Errorf("CHANGELOG.md does not mention %s; environment names are API and land in Unreleased in the same change as the snapshot", name)
		}
	}
}

// Long flag names are the consumer-facing spellings. Short aliases are the
// same flags; requiring "-j" in the notes would match every hyphenated word.
func TestChangelogMentionsEveryContractFlag(t *testing.T) {
	text := readChangelog(t)
	for _, name := range goldenFlagNames {
		if len(name) < 2 {
			continue
		}
		needle := "--" + name
		if !strings.Contains(text, needle) {
			t.Errorf("CHANGELOG.md does not mention %s; flag names are API and land in Unreleased in the same change as the snapshot", needle)
		}
	}
}

func TestChangelogMentionsEveryContractCommand(t *testing.T) {
	text := readChangelog(t)
	for _, cmd := range goldenCommands {
		word := cmd
		if i := strings.Index(word, " ["); i >= 0 {
			word = word[:i]
		}
		if i := strings.Index(word, " <"); i >= 0 {
			word = word[:i]
		}
		if word == "gauntlet" {
			continue
		}
		if !strings.Contains(text, word) {
			t.Errorf("CHANGELOG.md does not mention %q; command names are API and land in Unreleased in the same change as the snapshot", word)
		}
	}
}

func TestChangelogMentionsEveryContractReview(t *testing.T) {
	text := readChangelog(t)
	for _, name := range prompt.BundledNames() {
		stem := strings.TrimSuffix(name, "-review")
		if strings.Contains(text, name) || strings.Contains(text, "`"+stem+"`") {
			continue
		}
		t.Errorf("CHANGELOG.md does not mention %s; review names are API and land in Unreleased in the same change as the snapshot", name)
	}
}

func TestChangelogMentionsEveryContractSet(t *testing.T) {
	text := readChangelog(t)
	for _, name := range prompt.SetNames() {
		if !strings.Contains(text, "`"+name+"`") {
			t.Errorf("CHANGELOG.md does not mention `%s`; set names are API and land in Unreleased in the same change as the snapshot", name)
		}
	}
}

func readChangelog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func isChangelogGroup(name string) bool {
	return slices.Contains(changelogGroups, name)
}

type changelogVersion struct{ major, minor, patch int }

func (v changelogVersion) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor) + "." + strconv.Itoa(v.patch)
}

func (v changelogVersion) less(o changelogVersion) bool {
	switch {
	case v.major != o.major:
		return v.major < o.major
	case v.minor != o.minor:
		return v.minor < o.minor
	default:
		return v.patch < o.patch
	}
}

func parseChangelogVersion(s string) (changelogVersion, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return changelogVersion{}, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return changelogVersion{}, false
	}
	if parts[0] != strconv.Itoa(maj) || parts[1] != strconv.Itoa(min) || parts[2] != strconv.Itoa(pat) {
		return changelogVersion{}, false
	}
	return changelogVersion{maj, min, pat}, true
}
