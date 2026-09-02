// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"regexp"
	"strings"
)

// FileNote is one "PATH: <file>: <what was done>" line the review protocol
// asks an agent to print per changed file. The note is the only per-file
// description of the change that exists anywhere: the diff shows what moved,
// this says what it was for.
type FileNote struct {
	Path string
	Note string
}

// fileNoteRe finds the per-file report lines. The payload after "PATH:" is
// "<file>: <note>"; a line without the second separator names a file and
// describes nothing, and is dropped rather than guessed at.
var fileNoteRe = regexp.MustCompile(`(?im)^\s*PATH:\s*(.+?)\s*$`)

// fileNoteMax bounds one note and filePathMax one path. Both are untrusted
// agent output headed for a PR body, which bounds them again for display;
// these caps only keep a hostile line from bloating what the run carries.
const (
	fileNoteMax  = 300
	filePathMax  = 500
	fileNotesMax = 64
)

// ParseFileNotes returns the per-file descriptions a review printed, in first
// appearance order, the last note winning when a file is reported twice: an
// agent that revises itself means the later line. The count is capped; a
// review that touched more files than that has a diff too big for its notes
// to orient anyone anyway.
func ParseFileNotes(tail []byte) []FileNote {
	var notes []FileNote
	index := make(map[string]int)
	for _, m := range fileNoteRe.FindAllStringSubmatch(string(tail), -1) {
		path, note, ok := strings.Cut(m[1], ": ")
		if !ok {
			continue
		}
		path = cleanReportedLine(path, filePathMax)
		note = cleanReportedLine(note, fileNoteMax)
		if path == "" || note == "" {
			continue
		}
		if at, seen := index[path]; seen {
			notes[at].Note = note
			continue
		}
		if len(notes) >= fileNotesMax {
			continue
		}
		index[path] = len(notes)
		notes = append(notes, FileNote{Path: path, Note: note})
	}
	return notes
}
