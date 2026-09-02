// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/gitx"
)

// subjectMax is the conventional 72-character cap for a generated subject.
// An agent's own SUBJECT: line is bounded separately (agent.subjectMax) and
// is used as-is: it already went through that sanitizer.
const subjectMax = 72

// commitSubject is what the history will say about a review's change: what
// the agent called it, or a summary of the files it actually touched. Neither
// mentions this tool, a review, a run id, or a model: the commit is the
// project's, not the machinery's.
func commitSubject(fromAgent string, ch gitx.Changes) string {
	if s := strings.TrimSpace(fromAgent); s != "" {
		return s
	}
	return subjectFromChanges(ch)
}

func treeChanges(ctx context.Context, dir string) gitx.Changes {
	ch, err := gitx.Open(dir).Status(ctx, nil)
	if err != nil {
		return gitx.Changes{}
	}
	return ch
}

func subjectFromChanges(ch gitx.Changes) string {
	files := append(append([]string{}, ch.Tracked...), ch.Untracked...)
	if len(files) == 0 {
		return "chore: update files"
	}
	sort.Strings(files)
	typ := subjectType(files)
	what := subjectWhat(files, len(ch.Tracked) == 0)
	if scope := subjectScope(files); scope != "" {
		return clipSubject(typ + "(" + scope + "): " + what)
	}
	return clipSubject(typ + ": " + what)
}

func subjectType(files []string) string {
	docs, tests, ci := 0, 0, 0
	for _, f := range files {
		slash := filepath.ToSlash(f)
		base := path.Base(slash)
		switch {
		case isTestPath(slash, base):
			tests++
		case isDocPath(slash, base):
			docs++
		case isCIPath(slash):
			ci++
		}
	}
	n := len(files)
	switch {
	case tests == n:
		return "test"
	case docs == n:
		return "docs"
	case ci == n:
		return "ci"
	default:
		return "chore"
	}
}

func isTestPath(slash, base string) bool {
	if strings.Contains(slash, "/testdata/") || strings.HasPrefix(slash, "testdata/") {
		return true
	}
	for _, suf := range []string{"_test.go", "_test.py", "_test.rb", "_test.ts", "_test.js", ".test.ts", ".test.js", ".spec.ts", ".spec.js"} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return strings.HasPrefix(base, "test_")
}

func isDocPath(slash, base string) bool {
	if strings.HasPrefix(slash, "docs/") || strings.HasPrefix(slash, "doc/") {
		return true
	}
	switch base {
	case "README.md", "README", "CHANGELOG.md", "CONTRIBUTING.md", "LICENSE", "COPYING":
		return true
	}
	return strings.HasSuffix(base, ".md")
}

func isCIPath(slash string) bool {
	return strings.HasPrefix(slash, ".github/") ||
		strings.HasPrefix(slash, ".gitlab-ci") ||
		strings.HasPrefix(slash, ".circleci/")
}

// boringScopes are directory names that name a layout convention, not an
// area of the project. Using one as a conventional-commit scope would read
// as "the src package" rather than what changed.
var boringScopes = map[string]bool{
	"src": true, "lib": true, "pkg": true, "internal": true, "app": true,
	"cmd": true, "testdata": true, "vendor": true, "scripts": true,
	"workflows": true, "github": true, "circleci": true,
}

func subjectScope(files []string) string {
	var scope string
	for _, f := range files {
		dir := path.Base(path.Dir(filepath.ToSlash(f)))
		if dir == "." || dir == "" || dir == "/" || strings.HasPrefix(dir, ".") || boringScopes[dir] {
			return ""
		}
		dir = commitToken(dir)
		if dir == "" {
			return ""
		}
		if scope == "" {
			scope = dir
			continue
		}
		if scope != dir {
			return ""
		}
	}
	return scope
}

func subjectWhat(files []string, allNew bool) string {
	verb := "update"
	if allNew {
		verb = "add"
	}
	names := displayNames(files)
	switch len(names) {
	case 1:
		return verb + " " + names[0]
	case 2:
		return verb + " " + names[0] + " and " + names[1]
	default:
		return verb + " " + names[0] + " and " + strconv.Itoa(len(names)-1) + " other files"
	}
}

func displayNames(files []string) []string {
	bases := make(map[string]int, len(files))
	out := make([]string, len(files))
	for _, f := range files {
		bases[path.Base(filepath.ToSlash(f))]++
	}
	for i, f := range files {
		slash := filepath.ToSlash(f)
		base := path.Base(slash)
		name := base
		if bases[base] > 1 {
			name = slash
		}
		name = commitToken(name)
		if name == "" {
			name = "file"
		}
		out[i] = name
	}
	return out
}

func commitToken(s string) string {
	// Filenames arrive in whatever form the filesystem handed git (macOS
	// writes NFD); compose first so the subject stores the same spelling
	// discovery and signal matching already use, and so a combining mark
	// cannot sit on the 72-rune cut by itself.
	s = norm.NFC.String(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func clipSubject(s string) string {
	if utf8.RuneCountInString(s) <= subjectMax {
		return s
	}
	n := 0
	for i := range s {
		if n == subjectMax {
			return strings.TrimSpace(s[:i])
		}
		n++
	}
	return s
}
