// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"strings"
	"testing"
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
	if strings.ContainsRune(notes[2].Note, '‮') || strings.ContainsRune(notes[2].Note, '\t') {
		t.Fatalf("control and format characters survived: %q", notes[2].Note)
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
		if len(n.Note) > 4*fileNoteMax || len(n.Path) > 4*filePathMax {
			t.Fatalf("unbounded note or path: %d/%d", len(n.Note), len(n.Path))
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
