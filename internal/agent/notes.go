// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/normalize"
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
var fileNoteRe = regexp.MustCompile(`(?im)^[ \t]*PATH:[ \t]*(.+?)[ \t]*$`)

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
	for _, m := range fileNoteRe.FindAllSubmatch(tail, -1) {
		path, note, ok := strings.Cut(string(m[1]), ": ")
		if !ok {
			continue
		}
		path = cleanReportedLine(path, filePathMax)
		note = cleanReportedLine(note, fileNoteMax)
		if path == "" || note == "" {
			continue
		}
		// The index is keyed on the composed spelling while the note keeps
		// the path as the agent printed it. A macOS checkout hands the agent
		// the decomposed name of a file it created, and the two forms are one
		// file, so keying on the raw bytes would report it twice. Every other
		// site that pairs a printed path with a git-reported one composes both
		// sides for the same reason (see runner's noteKey).
		key := fuzzy.NFC(path)
		if at, seen := index[key]; seen {
			notes[at].Note = note
			continue
		}
		if len(notes) >= fileNotesMax {
			continue
		}
		index[key] = len(notes)
		notes = append(notes, FileNote{Path: path, Note: note})
	}
	return notes
}

// subjectRe finds the commit subject a review prints for the change it made.
// The runner writes the commit in worktree mode, and only the agent knows
// what the change was: without this the history reads "automated fixes" forty
// times over.
var subjectRe = regexp.MustCompile(`(?im)^[ \t]*SUBJECT:[ \t]*(.+?)[ \t]*$`)

// subjectMax bounds a commit subject. Git wraps a longer one badly, and the
// line is agent output, which is untrusted text headed for a file people read.
const subjectMax = 100

// ParseSubject returns the commit subject a review asked for, or "" when it
// printed none. The last one wins: an agent that revises itself means the
// later line.
func ParseSubject(tail []byte) string {
	matches := subjectRe.FindAllSubmatch(tail, -1)
	for _, m := range slices.Backward(matches) {
		if s := cleanReportedLine(string(m[1]), subjectMax); s != "" {
			return s
		}
	}
	return ""
}

// cleanReportedLine sanitizes one line of agent output headed for a file
// people read. Formatting and separators go first, then the trim: dropping a
// hidden character can expose whitespace that was hiding behind it, and the
// result is a line of text, not a line of text with a ragged end. A subject
// becomes a commit message, where a newline would forge a body or an author
// trailer and a bidi override would reverse the log line; a file note lands
// in a PR body with the same exposure. The cut is by code points and lands on
// a grapheme boundary: a commit subject survives verbatim, and a byte count
// would land inside the UTF-8 encoding of anything past ASCII.
func cleanReportedLine(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxRunes {
		s = strings.TrimSpace(normalize.Clip(s, maxRunes))
	}
	return s
}
