// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/runx"
)

func TestParseFileNotes(t *testing.T) {
	tail := []byte(strings.Join([]string{
		"noise before",
		"PATH: internal/cache/store.go: drop the stale entry before the refill",
		"  path: b.go: lowercase prefix still counts",
		"PATH: no-note-here",
		"PATH: internal/cache/store.go: the later line wins",
		"PATH: evil.go: text‮with a bidi override\tand controls",
		"SUBJECT: fix: unrelated",
	}, "\n"))
	notes := ParseFileNotes(tail)
	if len(notes) != 3 {
		t.Fatalf("notes = %+v", notes)
	}
	if notes[0].Path != "internal/cache/store.go" || notes[0].Note != "the later line wins" {
		t.Fatalf("last note for a repeated path must win in place: %+v", notes[0])
	}
	if notes[1].Path != "b.go" || notes[1].Note != "lowercase prefix still counts" {
		t.Fatalf("notes[1] = %+v", notes[1])
	}
	if notes[2].Path != "evil.go" || notes[2].Note != "textwith a bidi overrideand controls" {
		t.Fatalf("notes[2] = %+v, want evil.go sanitized note", notes[2])
	}
	if strings.ContainsRune(notes[2].Note, '‮') || strings.ContainsRune(notes[2].Note, '\t') {
		t.Fatalf("control and format characters survived: %q", notes[2].Note)
	}
}

func TestParseFileNotesLineBoundaries(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, empty := range []string{"PATH:", "PATH: \t"} {
			for _, next := range []string{"SUBJECT: fix: unrelated", "file.go: ordinary prose"} {
				input := empty + newline + next + newline
				if got := ParseFileNotes([]byte(input)); len(got) != 0 {
					t.Errorf("ParseFileNotes(%q) = %+v, want no notes", input, got)
				}
			}
			input := empty + newline + "\tPATH:\tfile.go: keep this\t" + newline
			got := ParseFileNotes([]byte(input))
			if len(got) != 1 || got[0] != (FileNote{Path: "file.go", Note: "keep this"}) {
				t.Errorf("ParseFileNotes(%q) = %+v, want the valid note", input, got)
			}
		}
	}
}

func TestParseFileNotesBounds(t *testing.T) {
	var sb strings.Builder
	for i := range fileNotesMax + 10 {
		sb.WriteString("PATH: file")
		sb.WriteString(strings.Repeat("x", i+1))
		sb.WriteString(".go: a note\n")
	}
	sb.WriteString("PATH: filex.go: " + strings.Repeat("y", 2000) + "\n")
	notes := ParseFileNotes([]byte(sb.String()))
	if len(notes) != fileNotesMax {
		t.Fatalf("count = %d, want the cap %d", len(notes), fileNotesMax)
	}
	for _, n := range notes {
		if utf8.RuneCountInString(n.Note) > fileNoteMax {
			t.Fatalf("note rune count %d exceeds max %d", utf8.RuneCountInString(n.Note), fileNoteMax)
		}
		if utf8.RuneCountInString(n.Path) > filePathMax {
			t.Fatalf("path rune count %d exceeds max %d", utf8.RuneCountInString(n.Path), filePathMax)
		}
	}
}

func TestParseFileNotesEmpty(t *testing.T) {
	for _, tail := range []string{"", "PATH: x\nRESULT: changed=1", "PATH: a.go:", "PATH: : note"} {
		if notes := ParseFileNotes([]byte(tail)); len(notes) != 0 {
			t.Fatalf("ParseFileNotes(%q) = %+v, want none", tail, notes)
		}
	}
}

// FuzzParseFileNotes feeds arbitrary agent output into ParseFileNotes and pins
// its extraction contract: at most fileNotesMax notes come back, every path is
// unique with later appearances winning in place, paths and notes are bounded
// by filePathMax and fileNoteMax, formatting controls, bidi overrides, and
// line breaks are stripped, and parsing is deterministic.
func FuzzParseFileNotes(f *testing.F) {
	seeds := []string{
		"PATH: internal/cache/store.go: drop the stale entry before the refill",
		"  path: b.go: lowercase prefix still counts",
		"PATH: no-note-here",
		"PATH: internal/cache/store.go: the later line wins",
		"PATH: evil.go: text\u202Ewith a bidi override\tand controls",
		"SUBJECT: fix: unrelated",
		"PATH: file.go: " + strings.Repeat("x", 1000),
		"PATH: " + strings.Repeat("d/", 300) + "file.go: note",
		"   PATH:   spaces.go   :   spaced note   \n",
		"PATH: a.go: note 1\nPATH: b.go: note 2\nPATH: a.go: revised note 1",
		"noise before\nPATH: ok.go: note\nnoise after",
		"",
		"PATH: : note with empty path",
		"PATH: a.go:\n",
		"PATH: \x00\x1b[31mbad.go: \x07evil\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	var sb strings.Builder
	for i := range fileNotesMax + 10 {
		sb.WriteString(fmt.Sprintf("PATH: file_%d.go: note_%d\n", i, i))
	}
	f.Add([]byte(sb.String()))

	f.Fuzz(func(t *testing.T, tail []byte) {
		notes := ParseFileNotes(tail)
		if len(notes) > fileNotesMax {
			t.Fatalf("ParseFileNotes returned %d notes, exceeds max %d", len(notes), fileNotesMax)
		}

		again := ParseFileNotes(tail)
		if len(notes) != len(again) {
			t.Fatalf("ParseFileNotes non-deterministic in count: %d vs %d", len(notes), len(again))
		}
		for i := range notes {
			if notes[i] != again[i] {
				t.Fatalf("ParseFileNotes non-deterministic at index %d: %+v vs %+v", i, notes[i], again[i])
			}
		}

		seen := make(map[string]bool, len(notes))
		for _, n := range notes {
			if n.Path == "" || n.Note == "" {
				t.Fatalf("empty path or note in result: %+v", n)
			}
			if seen[n.Path] {
				t.Fatalf("duplicate path in parsed notes: %q", n.Path)
			}
			seen[n.Path] = true

			if runes := utf8.RuneCountInString(n.Path); runes > filePathMax {
				t.Fatalf("path exceeds filePathMax (%d > %d): %q", runes, filePathMax, n.Path)
			}
			if runes := utf8.RuneCountInString(n.Note); runes > fileNoteMax {
				t.Fatalf("note exceeds fileNoteMax (%d > %d): %q", runes, fileNoteMax, n.Note)
			}

			if strings.ContainsAny(n.Path, "\n\r") {
				t.Fatalf("newlines in path: %q", n.Path)
			}
			if strings.ContainsAny(n.Note, "\n\r") {
				t.Fatalf("newlines in note: %q", n.Note)
			}

			if n.Path != strings.TrimSpace(n.Path) {
				t.Fatalf("path has outer whitespace: %q", n.Path)
			}
			if n.Note != strings.TrimSpace(n.Note) {
				t.Fatalf("note has outer whitespace: %q", n.Note)
			}

			for _, r := range n.Path + n.Note {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
					unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
					t.Fatalf("control/format rune %q survived into note: %+v", r, n)
				}
			}
		}
	})
}

// An agent launched with the operator's environment prints a rejected key when
// it reports the failure, and a note an agent prints can carry one it read
// through the CLI. These two lines are the ones that outlive the run, as the
// commit message and as the PR title and body, so a credential in one is
// republished to everyone who can see the repository.
func TestReportedLinesRedactCredentials(t *testing.T) {
	tail := []byte(strings.Join([]string{
		"SUBJECT: the push failed: GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz012345",
		"PATH: internal/cache/store.go: retry with ANTHROPIC_API_KEY=sk-ant-0123456789abcdefXYZ",
	}, "\n"))
	if got := ParseSubject(tail); strings.Contains(got, "ghp_") || strings.Contains(got, "012345") {
		t.Errorf("ParseSubject kept a credential: %q", got)
	} else if !strings.Contains(got, runx.Redacted) {
		t.Errorf("ParseSubject = %q, want the credential replaced", got)
	}
	notes := ParseFileNotes(tail)
	if len(notes) != 1 {
		t.Fatalf("ParseFileNotes returned %d notes, want 1", len(notes))
	}
	if strings.Contains(notes[0].Note, "sk-ant-") || strings.Contains(notes[0].Note, "abcdefXYZ") {
		t.Errorf("ParseFileNotes kept a credential: %q", notes[0].Note)
	} else if !strings.Contains(notes[0].Note, runx.Redacted) {
		t.Errorf("ParseFileNotes note = %q, want the credential replaced", notes[0].Note)
	}
}
