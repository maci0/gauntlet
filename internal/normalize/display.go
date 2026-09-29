// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package normalize

import (
	"bytes"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Sanitize strips control and formatting characters from untrusted display
// text (file names, prompt descriptions, agent output shown outside the feed),
// and repairs bytes that are not valid UTF-8 (see stripControl). A value that
// survives it unchanged is safe to show and to store as text; one that does
// not carried something a reader could not be shown verbatim.
func Sanitize(s string) string {
	return stripControl(strings.ReplaceAll(s, "\t", " "))
}

// Display makes untrusted text safe to write to a terminal while leaving every
// visible character alone: ANSI/OSC escape sequences, C0/C1 controls, and
// Unicode formatting characters (bidi overrides included) are removed. It is
// for lines that reach the user without passing through a Normalizer -- raw
// echo mode and structured stream events -- where clean() never ran. An
// unterminated escape sequence degrades to inert punctuation: the ESC byte
// itself is always stripped, so no fragment can drive the terminal.
func Display(s string) string {
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRe.ReplaceAllString(s, "")
	}
	return Sanitize(s)
}

// Document is Display for text that is more than one line: every line is
// sanitized as Display would sanitize it, and the line breaks survive. Display
// drops them because its callers read one line at a time and a bare LF there is
// a stray byte; a prompt is a document, and Display's contract would fold the
// whole thing onto a single line.
func Document(s string) string {
	if !strings.Contains(s, "\n") {
		return Display(s)
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = Display(line)
	}
	return strings.Join(lines, "\n")
}

// Repair turns every byte that is not valid UTF-8 into U+FFFD, one per byte,
// leaving every other character alone. It is the repair Sanitize performs,
// on its own, for text that is compared rather than shown: an agent's printed
// path has been through the agent-side sanitizer and is already repaired,
// while the same file name from git still carries the raw bytes the
// filesystem holds, and the two would never match without this.
func Repair(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

// Truncate cuts s to at most w code points, without splitting a UTF-8
// sequence, marking the cut with an ellipsis. A w of 1 or less leaves the
// string whole: the mark is the only thing that could fit, and a line reduced
// to a bare ellipsis tells a reader nothing. Code points are a bound, not a
// layout measurement: wide characters occupy two terminal cells and a
// combining mark zero, so display-width cutting lives with the dashboard,
// which owns the column math.
func Truncate(s string, w int) string {
	if w <= 1 || len(s) <= w {
		return s
	}
	// The mark reports a cut, so it appears only when Clip took one: a string
	// longer in bytes than w but within w code points comes back whole.
	if cut := Clip(s, w); len(cut) < len(s) {
		return cut + "…"
	}
	return s
}

// RedactHome rewrites the operator's home directory to "~" wherever it appears
// in s, so text kept for later (a journal line, a command line) carries no OS
// account name. The path stays the one the operator recognizes and can expand,
// so nothing is lost but the account.
//
// A home of "/", or one this process cannot resolve, is left alone: replacing
// every separator would shorten nothing and mangle the text. An occurrence is
// rewritten only where it is a whole path component, so "/home/alice" does not
// shorten the unrelated "/home/alicia".
func RedactHome(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == string(os.PathSeparator) {
		return s
	}
	home = strings.TrimRight(home, string(os.PathSeparator))
	if home == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		i := strings.Index(s, home)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		rest := s[i+len(home):]
		// A partial component is somebody else's path that merely starts with
		// the same bytes.
		if rest != "" && rest[0] != os.PathSeparator {
			b.WriteString(home[:1])
			s = s[i+1:]
			continue
		}
		b.WriteString("~")
		s = rest
	}
}

// maxPendingBytes caps the partial line a DisplayWriter holds while waiting
// for its newline. A child that streams one endless line would otherwise grow
// the buffer without bound; past the cap the writer emits what it has and
// keeps going, so nothing is dropped.
const maxPendingBytes = 1 << 20

// DisplayWriter filters untrusted bytes on their way to a terminal, for a
// child process whose output is streamed rather than parsed line by line. Give
// each of the child's streams its own: it is not safe for concurrent use.
//
// Every visible character survives, so this changes no output a normal program
// produces. What it removes is the part of the child's output the producer does
// not control: a file name read out of a hostile tree carries escape sequences
// and bidi overrides into the operator's terminal.
type DisplayWriter struct {
	dst  io.Writer
	held []byte
}

// NewDisplayWriter returns a writer that passes each complete line of its
// input through Display before writing it to dst. A trailing partial line is
// held until Flush.
func NewDisplayWriter(dst io.Writer) *DisplayWriter {
	return &DisplayWriter{dst: dst}
}

// Write implements io.Writer, forwarding the line-structured form of whatever
// p carries. It always reports len(p): a line the destination rejects comes
// back as the error, which is where an io.Writer reports it.
func (w *DisplayWriter) Write(p []byte) (int, error) {
	n := len(p)
	for {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.held = append(w.held, p...)
			return n, w.drain("", false)
		}
		w.held = append(w.held, p[:i]...)
		if err := w.drain("\n", false); err != nil {
			return n, err
		}
		p = p[i+1:]
	}
}

// Flush writes back a partial line the child left unterminated, which is the
// last thing a killed or newline-less process owes the reader.
func (w *DisplayWriter) Flush() error {
	return w.drain("", true)
}

// drain emits what is held, followed by end. An empty end means the line is
// still open, so it stays held until it is terminated or, past
// maxPendingBytes, until its oldest rune-aligned prefix goes out, so a child
// streaming one endless line cannot grow the buffer without bound and nothing
// is dropped. A final drain has no later chance to emit, so it emits
// unconditionally.
func (w *DisplayWriter) drain(end string, final bool) error {
	if len(w.held) == 0 {
		if end == "" {
			return nil // nothing held, and no terminator to owe the reader
		}
		// An empty line is still a line. Skipping it would drop a blank line
		// the child wrote, which is a rewrite of its output, not a filter.
		_, err := io.WriteString(w.dst, end)
		return err
	}
	cut := len(w.held)
	if end == "" && !final {
		if cut <= maxPendingBytes {
			return nil // the line may still grow; wait for its newline
		}
		cut = runeBoundary(w.held, maxPendingBytes)
		if cut == 0 {
			// No prefix of the pending line ends on a rune boundary, so no
			// cut is clean. Emitting the whole buffer is what keeps a child
			// streaming invalid bytes from growing it without bound, and
			// Display repairs what goes out.
			cut = len(w.held)
		}
	}
	line := Display(string(w.held[:cut]))
	w.held = append(w.held[:0], w.held[cut:]...)
	_, err := io.WriteString(w.dst, line+end)
	return err
}

// runeBoundary returns an index i at or below cut where b[:i] ends on a UTF-8
// sequence boundary, or 0 when the bytes before cut hold no boundary at all.
//
// b[:i] ends on a boundary exactly when b[i] starts a rune, so the search is
// for the nearest rune start at or before cut. That direction is the whole
// point. The byte *before* a boundary is a continuation byte, so a walk that
// steps back while the byte it lands on is a continuation byte walks straight
// past every boundary in text whose characters are multi-byte. Asking
// FullRune about the single byte before the cut is the same trap wearing a
// different hat: it is false for every multi-byte lead, so it rejects each
// boundary in turn and, in a line of nothing but CJK, ends at the front of the
// buffer having found none. What goes out is then the whole line, its tail
// repaired to U+FFFD, and the rest of the character arrives too late to repair
// back.
//
// The walk is bounded by the longest rune: a cut is at most three bytes past
// the boundary before it. Cutting back rather than forward keeps the split
// character in the buffer, so it is emitted whole once the rest of it arrives.
func runeBoundary(b []byte, cut int) int {
	if cut >= len(b) {
		return len(b) // nothing is being held back past the cut
	}
	for i := cut; i > 0 && cut-i < utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			return i
		}
	}
	return 0
}

// Clip cuts s to at most max code points, without splitting a grapheme
// cluster (combining marks, emoji sequences, flags) or a UTF-8 sequence,
// returning the prefix without an ellipsis.
func Clip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	n := 0
	clusters := uniseg.NewGraphemes(s)
	for clusters.Next() {
		n += utf8.RuneCountInString(clusters.Str())
		if n > max {
			start, _ := clusters.Positions()
			return s[:start]
		}
	}
	return s
}
