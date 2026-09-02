// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/gitx"
)

func TestCommitSubjectPrefersTheAgent(t *testing.T) {
	ch := gitx.Changes{Tracked: []string{"internal/foo.go"}}
	got := commitSubject("fix: guard the nil map write", ch)
	if got != "fix: guard the nil map write" {
		t.Fatalf("got %q", got)
	}
	got = commitSubject("  \n", ch)
	if got != "chore: update foo.go" {
		t.Fatalf("blank agent subject must fall through to the files: %q", got)
	}
}

func TestSubjectFromChanges(t *testing.T) {
	cases := []struct {
		name string
		ch   gitx.Changes
		want string
	}{
		{"nothing listed", gitx.Changes{}, "chore: update files"},
		{"one file at the root", gitx.Changes{Tracked: []string{"parse.go"}}, "chore: update parse.go"},
		{"new file", gitx.Changes{Untracked: []string{"parse.go"}}, "chore: add parse.go"},
		{"scope from a shared directory", gitx.Changes{Tracked: []string{"internal/parser/parse.go"}},
			"chore(parser): update parse.go"},
		{"boring parent is not a scope", gitx.Changes{Tracked: []string{"internal/lock.go"}},
			"chore: update lock.go"},
		{"two files", gitx.Changes{Tracked: []string{"b.go", "a.go"}},
			"chore: update a.go and b.go"},
		{"more than two", gitx.Changes{Tracked: []string{"c.go", "a.go", "b.go"}},
			"chore: update a.go and 2 other files"},
		{"tests only", gitx.Changes{Tracked: []string{"foo_test.go", "bar_test.go"}},
			"test: update bar_test.go and foo_test.go"},
		{"docs only", gitx.Changes{Tracked: []string{"README.md", "docs/CLI.md"}},
			"docs: update README.md and CLI.md"},
		{"ci only", gitx.Changes{Tracked: []string{".github/workflows/ci.yml"}},
			"ci: update ci.yml"},
		{"mixed types stay chore", gitx.Changes{Tracked: []string{"foo.go", "foo_test.go"}},
			"chore: update foo.go and foo_test.go"},
		{"same basename keeps the path", gitx.Changes{Tracked: []string{"a/foo.go", "b/foo.go"}},
			"chore: update a/foo.go and b/foo.go"},
		{"different directories drop the scope", gitx.Changes{Tracked: []string{
			"internal/parser/parse.go", "internal/lexer/lex.go",
		}}, "chore: update lex.go and parse.go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := subjectFromChanges(c.ch); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSubjectFromChangesStripsControlsAndFits(t *testing.T) {
	got := subjectFromChanges(gitx.Changes{Tracked: []string{"ok\x00.go"}})
	if strings.ContainsRune(got, 0) {
		t.Fatalf("control byte reached the subject: %q", got)
	}
	if got != "chore: update ok.go" {
		t.Fatalf("visible name did not survive sanitizing: %q", got)
	}
	long := strings.Repeat("x", 80) + ".go"
	got = subjectFromChanges(gitx.Changes{Tracked: []string{long}})
	if n := len([]rune(got)); n > subjectMax {
		t.Fatalf("subject is %d runes, want at most %d: %q", n, subjectMax, got)
	}
	if !strings.HasPrefix(got, "chore: update x") {
		t.Fatalf("truncated subject lost the type and name: %q", got)
	}
}

// A macOS tree hands git NFD filenames. The subject is permanent history, so
// the same word must land in the composed form used everywhere else rather
// than as a combining sequence that a later rune-counted cut can split.
func TestSubjectFromChangesComposesFilenames(t *testing.T) {
	nfd := "cafe\u0301.go"
	if nfd == norm.NFC.String(nfd) {
		t.Fatal("fixture is not decomposed")
	}
	got := subjectFromChanges(gitx.Changes{Tracked: []string{nfd}})
	if want := "chore: update café.go"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
