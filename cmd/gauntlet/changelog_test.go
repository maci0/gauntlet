// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
				t.Fatalf("CHANGELOG.md:%d: %q is not a SemVer heading without a v prefix (prerelease and build metadata are allowed)",
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

// A patch release must contain only fixes (or security fixes): new features
// require a minor bump, and breaking changes require a major bump. A minor
// release may add features but must not remove or break documented contract
// surface. Only a major release may contain removals.
func TestChangelogSemVerBumps(t *testing.T) {
	text := readChangelog(t)
	for _, err := range validateChangelogSemVer(text) {
		t.Errorf("CHANGELOG.md:%s", err)
	}
}

func TestValidateChangelogSemVerCases(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "valid patch and minor bumps",
			content: "## Unreleased\n\n### Added\n- Feature\n\n" +
				"## 1.23.0\n\n### Added\n- New feature\n\n" +
				"## 1.22.1\n\n### Fixed\n- Bug fix\n\n" +
				"## 1.22.0\n\n### Fixed\n- Earlier fix\n",
			wantErr: "",
		},
		{
			name: "unreleased contains removed",
			content: "## Unreleased\n\n### Removed\n- Dropped flag\n\n" +
				"## 1.22.1\n\n### Fixed\n- Bug fix\n",
			wantErr: "Unreleased section contains ### Removed",
		},
		{
			name: "patch release contains added",
			content: "## Unreleased\n\n" +
				"## 1.22.2\n\n### Added\n- New feature\n\n" +
				"## 1.22.1\n\n### Fixed\n- Bug fix\n",
			wantErr: "patch release 1.22.2 contains ### Added",
		},
		{
			name: "patch release contains deprecated",
			content: "## Unreleased\n\n" +
				"## 1.22.2\n\n### Deprecated\n- Old feature\n\n" +
				"## 1.22.1\n\n### Fixed\n- Bug fix\n",
			wantErr: "patch release 1.22.2 contains ### Deprecated",
		},
		{
			name: "minor release contains removed",
			content: "## Unreleased\n\n" +
				"## 1.23.0\n\n### Removed\n- Old command\n\n" +
				"## 1.22.1\n\n### Fixed\n- Bug fix\n",
			wantErr: "minor release 1.23.0 contains ### Removed",
		},
		{
			name: "minor release fails to reset patch",
			content: "## Unreleased\n\n" +
				"## 1.23.1\n\n### Added\n- New feature\n\n" +
				"## 1.22.0\n\n### Fixed\n- Bug fix\n",
			wantErr: "patch release 1.23.1 contains ### Added",
		},
		{
			name: "minor bump does not reset patch when only fixed",
			content: "## Unreleased\n\n" +
				"## 1.23.1\n\n### Fixed\n- Fix\n\n" +
				"## 1.22.0\n\n### Fixed\n- Bug fix\n",
			wantErr: "minor release 1.23.1 must reset patch to 0",
		},
		{
			name: "major bump does not reset minor or patch",
			content: "## Unreleased\n\n" +
				"## 2.1.0\n\n### Removed\n- Old command\n\n" +
				"## 1.22.0\n\n### Fixed\n- Bug fix\n",
			wantErr: "major release 2.1.0 must reset minor and patch to 0",
		},
		{
			name: "major bump resets minor but does not reset patch",
			content: "## Unreleased\n\n" +
				"## 2.0.1\n\n### Removed\n- Old command\n\n" +
				"## 1.22.0\n\n### Fixed\n- Bug fix\n",
			wantErr: "major release 2.0.1 must reset minor and patch to 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validateChangelogSemVer(tc.content)
			if tc.wantErr == "" {
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
			} else {
				found := false
				for _, e := range errs {
					if strings.Contains(e, tc.wantErr) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErr, errs)
				}
			}
		})
	}
}

func validateChangelogSemVer(text string) []string {
	var (
		errs       []string
		currentVer *changelogVersion
		prevVer    *changelogVersion
		groups     []string
		lineNum    int
	)
	check := func(ver *changelogVersion, groups []string, line int) {
		if ver == nil {
			if slices.Contains(groups, "Removed") {
				errs = append(errs, fmt.Sprintf("line %d: Unreleased section contains ### Removed; removals are breaking and require a major release", line))
			}
			return
		}
		// 1.0.1 and 1.0.2 are documented historical exceptions that shipped before contract guards.
		if *ver == "1.0.1" || *ver == "1.0.2" {
			prevVer = ver
			return
		}
		maj, min, pat := ver.numbers()
		if maj < 1 {
			prevVer = ver
			return // 0.x releases allowed breaking changes in minor
		}
		if pat > 0 {
			if slices.Contains(groups, "Added") {
				errs = append(errs, fmt.Sprintf("line %d: patch release %s contains ### Added; additions require a minor release", line, *ver))
			}
			if slices.Contains(groups, "Deprecated") {
				errs = append(errs, fmt.Sprintf("line %d: patch release %s contains ### Deprecated; deprecations require a minor release", line, *ver))
			}
			if slices.Contains(groups, "Removed") {
				errs = append(errs, fmt.Sprintf("line %d: patch release %s contains ### Removed; removals are breaking and require a major release", line, *ver))
			}
		} else if min > 0 {
			if slices.Contains(groups, "Removed") {
				errs = append(errs, fmt.Sprintf("line %d: minor release %s contains ### Removed; removals are breaking and require a major release", line, *ver))
			}
		}
		if prevVer != nil {
			pMaj, pMin, pPat := prevVer.numbers()
			if maj >= 1 && pMaj >= 1 {
				if pMaj > maj {
					if pMin != 0 || pPat != 0 {
						errs = append(errs, fmt.Sprintf("line %d: major release %s must reset minor and patch to 0, got .%d.%d",
							line, *prevVer, pMin, pPat))
					}
				} else if pMin > min {
					if pPat != 0 {
						errs = append(errs, fmt.Sprintf("line %d: minor release %s must reset patch to 0, got patch %d",
							line, *prevVer, pPat))
					}
				}
			}
		}
		prevVer = ver
	}
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		switch {
		case strings.HasPrefix(line, "### "):
			groups = append(groups, strings.TrimPrefix(line, "### "))
		case strings.HasPrefix(line, "## "):
			title := strings.TrimPrefix(line, "## ")
			check(currentVer, groups, lineNum)
			groups = nil
			lineNum = n
			if title == "Unreleased" {
				currentVer = nil
				continue
			}
			ver, ok := parseChangelogVersion(title)
			if ok {
				currentVer = &ver
			} else {
				currentVer = nil
			}
		}
	}
	check(currentVer, groups, lineNum)
	return errs
}

// A name on the environment-variable contract is something a consumer can
// set. If it is not in CHANGELOG.md, the addition or rename shipped without
// notes, which is how GH_TOKEN would have gone out undocumented.
func TestChangelogMentionsEveryContractEnvVar(t *testing.T) {
	text := readChangelog(t)
	for _, name := range goldenEnvVars {
		needle := "`" + name + "`"
		if !strings.Contains(text, needle) {
			t.Errorf("CHANGELOG.md does not mention %s; environment names are API and land in Unreleased in the same change as the snapshot", needle)
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

func TestChangelogMentionsEveryContractExitCode(t *testing.T) {
	text := readChangelog(t)
	for _, code := range goldenExitCodes {
		re := regexp.MustCompile(`(?i)\bexit(?:s|ing)?(?:\s+code)?\s+` + regexp.QuoteMeta(code) + `\b`)
		if !re.MatchString(text) {
			t.Errorf("CHANGELOG.md does not mention exit code %s; exit codes are API and land in Unreleased in the same change as the snapshot", code)
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

func TestParseChangelogVersion(t *testing.T) {
	for _, text := range []string{
		"0.0.0", "1.21.0", "1.22.0-rc.1", "1.22.0-rc.1+build.4",
		"1.22.0+build-4", "1.22.0+001", "1.22.0-0", "1.22.0-01a",
		"999999999999999999999999999999.0.0",
	} {
		t.Run(text, func(t *testing.T) {
			got, ok := parseChangelogVersion(text)
			if !ok || got.String() != text {
				t.Fatalf("parseChangelogVersion(%q) = %q, %v", text, got, ok)
			}
		})
	}
	for _, text := range []string{
		"", "v1.2.3", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"-1.2.3", "1.-2.3", "1.2.-3", "+1.2.3", "1.2.3-", "1.2.3+",
		"1.2.3-rc..1", "1.2.3-01", "1.2.3-rc.01", "1.2.3+build..4",
		"1.2.3+build+4", "1.2.3-rc_1", "1.2.3-β", "1.2.3\n", " 1.2.3",
	} {
		t.Run(text, func(t *testing.T) {
			if got, ok := parseChangelogVersion(text); ok {
				t.Fatalf("parseChangelogVersion(%q) accepted %q", text, got)
			}
		})
	}
	for _, tc := range []struct {
		text string
		maj  int
		min  int
		pat  int
	}{
		{"1.2.3", 1, 2, 3},
		{"0.10.0", 0, 10, 0},
		{"1.22.1", 1, 22, 1},
		{"2.0.0-rc.1+build.4", 2, 0, 0},
	} {
		v, ok := parseChangelogVersion(tc.text)
		if !ok {
			t.Fatalf("rejected %q", tc.text)
		}
		maj, min, pat := v.numbers()
		if maj != tc.maj || min != tc.min || pat != tc.pat {
			t.Fatalf("%s.numbers() = (%d, %d, %d), want (%d, %d, %d)", tc.text, maj, min, pat, tc.maj, tc.min, tc.pat)
		}
	}
}

func TestChangelogVersionPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.1.0", "2.0.0-rc.9", "2.0.0-rc.10",
		"2.0.0-rc.999999999999999999999999999999", "2.0.0", "10.0.0",
	}
	for i, text := range ordered {
		v, ok := parseChangelogVersion(text)
		if !ok {
			t.Fatalf("rejected %q", text)
		}
		for j, other := range ordered {
			o, ok := parseChangelogVersion(other)
			if !ok {
				t.Fatalf("rejected %q", other)
			}
			if got := v.less(o); got != (i < j) {
				t.Errorf("%s.less(%s) = %v, want %v", text, other, got, i < j)
			}
		}
		withBuild, ok := parseChangelogVersion(text + "+build-4")
		if !ok || withBuild.less(v) || v.less(withBuild) {
			t.Errorf("build metadata changed precedence for %s", text)
		}
	}
}

type changelogVersion string

var changelogSemver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+)*)?(\+([0-9A-Za-z-]+)(\.[0-9A-Za-z-]+)*)?$`)

func (v changelogVersion) String() string {
	return string(v)
}

func (v changelogVersion) numbers() (major, minor, patch int) {
	m := changelogSemver.FindStringSubmatch(string(v))
	if len(m) < 4 {
		return 0, 0, 0
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return maj, min, pat
}

func (v changelogVersion) less(o changelogVersion) bool {
	left, _, _ := strings.Cut(string(v), "+")
	right, _, _ := strings.Cut(string(o), "+")
	leftCore, leftPre, _ := strings.Cut(left, "-")
	rightCore, rightPre, _ := strings.Cut(right, "-")
	leftParts, rightParts := strings.Split(leftCore, "."), strings.Split(rightCore, ".")
	for i, part := range leftParts {
		if part != rightParts[i] {
			return changelogNumberLess(part, rightParts[i])
		}
	}
	if leftPre == "" || rightPre == "" {
		return leftPre != "" && rightPre == ""
	}
	leftParts, rightParts = strings.Split(leftPre, "."), strings.Split(rightPre, ".")
	for i := range min(len(leftParts), len(rightParts)) {
		a, b := leftParts[i], rightParts[i]
		if a == b {
			continue
		}
		aNumeric, bNumeric := changelogNumeric(a), changelogNumeric(b)
		if aNumeric && bNumeric {
			return changelogNumberLess(a, b)
		}
		if aNumeric != bNumeric {
			return aNumeric
		}
		return a < b
	}
	return len(leftParts) < len(rightParts)
}

func changelogNumberLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func changelogNumeric(s string) bool {
	return strings.Trim(s, "0123456789") == ""
}

func parseChangelogVersion(s string) (changelogVersion, bool) {
	if !changelogSemver.MatchString(s) {
		return "", false
	}
	version, _, _ := strings.Cut(s, "+")
	if _, pre, ok := strings.Cut(version, "-"); ok {
		for part := range strings.SplitSeq(pre, ".") {
			if len(part) > 1 && part[0] == '0' && changelogNumeric(part) {
				return "", false
			}
		}
	}
	return changelogVersion(s), true
}
