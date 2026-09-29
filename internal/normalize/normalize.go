// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package normalize turns chatty, terminal-oriented agent output into a
// bounded stream of informative lines.
//
// Agent CLIs paint spinners, repaint lines with carriage returns, and narrate
// every step. Echoing that verbatim buries the few lines that matter and, in a
// dashboard, corrupts the screen. Normalization is pure and per-stream: one
// Normalizer per agent process, fed one raw line at a time.
package normalize

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Kind classifies a surviving line so a UI can color it and a summary can find
// results without a second parse.
type Kind uint8

const (
	Plain    Kind = iota // ordinary narration
	Tool                 // the agent invoking a tool or touching a file
	Error                // something the agent reports as broken
	Result               // the run's own protocol lines: RESULT:, PATH:, RELEVANT:, COMMIT:, SUBJECT:
	Progress             // "reading…", "searching…": kept, but collapsed by verb
	DiffAdd              // an added line in a unified diff
	DiffDel              // a removed line in a unified diff
	DiffMeta             // a diff header or hunk marker
	Thinking             // the model reasoning, which agents report separately
)

// Line is one normalized output line.
type Line struct {
	Text   string
	Kind   Kind
	Repeat int // >1 when identical consecutive lines were collapsed into this one
}

var (
	// CSI, OSC (BEL- or ST-terminated), single-char escapes, and DEC private
	// sequences. Anything left is stripped as a control rune below.
	ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]" +
		"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" +
		"|\x1b[@-Z\\\\-_]")

	// A line made only of spinner glyphs, block/braille noise, or bullets
	// carries no information once the animation is gone.
	spinnerRe = regexp.MustCompile(`^[\s\p{Z}⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣾⣽⣻⢿⡿⣟⣯⣷◐◓◑◒⠁⠂⠄⡀⢀⠠⠐⠈|/\-\\●○◎◌⣿▁▂▃▄▅▆▇█▏▎▍▌▋▊▉·．]*$`)

	// Box drawing only: agent CLIs frame their panels, which a dashboard
	// re-frames anyway.
	boxRe = regexp.MustCompile(`^[\s\p{Z}─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╭╮╰╯═║╔╗╚╝╠╣╦╩╬▔▁]*$`)

	// Agent CLIs draw a gutter down the left of tool output: opencode uses
	// "|", claude uses "⏺" and "⎿", codex uses "•". The gutter is decoration;
	// what follows it is the line.
	gutterRe = regexp.MustCompile(`^[\s\p{Z}]*(?:[|│┃⎿⏺•▌]+[\s\p{Z}]*)+`)

	// After the gutter is gone, a line of nothing but punctuation carries no
	// information. Bare "\x1b[0m" resets, which opencode prints between
	// blocks, reduce to empty here.
	punctOnlyRe = regexp.MustCompile(`^[\s\p{Z}|│┃⎿⏺•▌·:;,.\-_=+~^*]*$`)

	// opencode's session header: "> build · ox-alpha-free" (mode and model).
	opencodeHeaderRe = regexp.MustCompile(`^>\s+\S+\s+·\s+\S+\s*$`)

	progressRe = regexp.MustCompile(`(?i)^[\s\p{Z}]*(reading|searching|scanning|thinking|indexing|analyzing|analysing|processing|loading|writing|compiling|running|checking|fetching|downloading|uploading|applying|saving|generating|querying|watching|waiting)\b`)

	toolRe = regexp.MustCompile(`(?i)^[\s\p{Z}]*(?:[✓✔✗✘⏺⎿·•>]+[\s\p{Z}]*)?(bash|read|edit|write|grep|glob|list|search|patch|apply_patch|str_replace|multiedit|todowrite|todoread|webfetch|websearch|task|shell|exec)\b[\s\p{Z}(:]`)

	errorRe = regexp.MustCompile(`(?i)\b(error|failed|failure|exception|traceback|panic|fatal|refused|denied|timed out)\b`)

	resultRe = regexp.MustCompile(`^[\s\p{Z}]*(RESULT|PATH|RELEVANT|COMMIT|SUBJECT):`)

	// Diff recognition. Agents paste unified diffs constantly, and a diff read
	// as prose is unreadable: the sign at the start of the line is the whole
	// meaning. These match the shapes that can only be a diff.
	diffStartRe = regexp.MustCompile(`^(diff --git |index [0-9a-f]{4,}|--- (a/|/dev/null)|\+\+\+ (b/|/dev/null)|@@ .* @@|=== modified file)`)
)

// Config tunes one Normalizer. The zero value is usable: no rate limiting.
type Config struct {
	// MaxLinesPerSec caps the lines one stream may emit; a burst beyond the
	// cap is dropped and counted, and Flush reports the count once as a
	// synthetic line so the gap is visible rather than silent. 0 disables
	// the cap.
	MaxLinesPerSec int
	// MaxWidth truncates very long lines (minified files, base64 blobs) to
	// keep a single line from blowing up a frame. 0 disables truncation.
	MaxWidth int
	// Now is the clock, injectable for tests. nil means time.Now.
	Now func() time.Time
}

// Normalizer filters one agent's output stream. It is not safe for concurrent
// use: give each stream its own.
type Normalizer struct {
	cfg Config

	// inDiff tracks whether the stream is inside a unified diff, so a bare
	// "-foo" is read as a removed line there and as prose everywhere else.
	inDiff bool

	lastVerb   string // last progress verb echoed, to collapse repeats
	lastText   string // last emitted line, to collapse exact duplicates
	pendKind   Kind
	pendCount  int
	windowFrom time.Time
	inWindow   int
	suppressed int
}

// New returns a Normalizer for one stream.
func New(cfg Config) *Normalizer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Normalizer{cfg: cfg}
}

// Push feeds one raw line (with or without its trailing newline) and returns
// the lines to emit. Duplicate collapsing means a line can be held back until
// the next non-matching line arrives, so callers must also call Flush at EOF.
func (n *Normalizer) Push(raw string) []Line {
	text, ok := clean(raw)
	if !ok {
		return nil
	}
	if n.cfg.MaxWidth > 0 {
		text = Truncate(text, n.cfg.MaxWidth)
	}

	kind := n.classify(text)
	if kind == Progress {
		verb := "session"
		if m := progressRe.FindStringSubmatch(text); m != nil {
			verb = strings.ToLower(m[1])
		}
		if n.lastVerb == verb {
			return nil // same activity, still going: one line is enough
		}
		n.lastVerb = verb
	} else {
		n.lastVerb = ""
	}

	// Collapse exact consecutive duplicates into one line with a count.
	if n.pendCount > 0 && text == n.lastText {
		n.pendCount++
		return nil
	}

	out := n.flushPending()
	if !n.allow() {
		n.suppressed++
		return out
	}
	n.lastText, n.pendKind, n.pendCount = text, kind, 1
	return out
}

// Flush emits any line held back for duplicate collapsing, plus a note about
// rate-limited lines. Call it when the stream ends.
func (n *Normalizer) Flush() []Line {
	out := n.flushPending()
	if n.suppressed > 0 {
		out = append(out, Line{
			Text: "… " + strconv.Itoa(n.suppressed) + " lines suppressed (rate limit)",
			Kind: Plain,
		})
		n.suppressed = 0
	}
	return out
}

func (n *Normalizer) flushPending() []Line {
	if n.pendCount == 0 {
		return nil
	}
	l := Line{Text: n.lastText, Kind: n.pendKind, Repeat: n.pendCount}
	n.pendCount = 0
	return []Line{l}
}

// allow implements a fixed-window rate limit. A fixed window is enough here:
// the goal is to stop a runaway stream from flooding a feed, not to shape
// traffic precisely.
func (n *Normalizer) allow() bool {
	if n.cfg.MaxLinesPerSec <= 0 {
		return true
	}
	now := n.cfg.Now()
	if n.inWindow == 0 || now.Sub(n.windowFrom) >= time.Second || now.Sub(n.windowFrom) < 0 {
		n.windowFrom, n.inWindow = now, 0
		// Drops are not reported here: they accumulate in suppressed until
		// Flush emits one summary line.
	}
	if n.inWindow >= n.cfg.MaxLinesPerSec {
		return false
	}
	n.inWindow++
	return true
}

// clean strips escapes and control characters and reports whether anything
// informative is left.
func clean(raw string) (string, bool) {
	s := strings.TrimRight(raw, "\r\n")
	// A carriage return rewrites the line in place: only the last segment is
	// what the terminal would have shown.
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRe.ReplaceAllString(s, "")
	}
	s = stripControl(s)
	// Drop the decorative left gutter, then judge what is left. Doing this
	// before the emptiness checks is what turns opencode's "|" continuation
	// lines into either real content or nothing.
	if g := gutterRe.FindString(s); g != "" && len(g) < len(s) {
		s = s[len(g):]
	}
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	if s == "" {
		return "", false
	}
	// Spinner, box-drawing, and punctuation-only lines have no ASCII letter.
	// Typical agent output does, so the three regexes are skipped for it.
	if !hasASCIILetter(s) && (punctOnlyRe.MatchString(s) || spinnerRe.MatchString(s) || boxRe.MatchString(s)) {
		return "", false
	}
	return s, true
}

func hasASCIILetter(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// stripControl removes C0/C1 controls and Unicode formatting characters
// (bidi overrides included) that can drive or spoof a terminal. Tabs become
// spaces so alignment survives.
//
// A byte sequence that is not valid UTF-8 becomes U+FFFD. The rebuild loop
// already does that (ranging over a string decodes a bad byte as RuneError),
// but the fast path returned such text untouched, so the same byte came back
// repaired in one string and raw in another that happened to hold no control
// character. The filesystem allows those bytes in a file name, and JSON
// rewrites them to U+FFFD wherever a journal or a report keeps one, so leaving
// them raw made a name depend on what else sat beside it.
func stripControl(s string) string {
	if !strings.ContainsFunc(s, needsStripControl) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("    ")
		case isControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsStripControl(r rune) bool {
	if r < 0x80 {
		return r < ' ' || r == 0x7f
	}
	return isControl(r)
}

func isControl(r rune) bool {
	if r < 0x80 {
		return r != '\t' && (r < ' ' || r == 0x7f)
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// classify labels one line, tracking diff state across calls.
func (n *Normalizer) classify(s string) Kind {
	if k, ok := n.classifyDiff(s); ok {
		return k
	}
	switch {
	case opencodeHeaderRe.MatchString(s):
		// Which model an agent picked is worth knowing once; opencode reprints
		// it, so it is treated as progress and collapsed by verb.
		return Progress
	case resultRe.MatchString(s):
		return Result
	case toolRe.MatchString(s):
		return Tool
	case progressRe.MatchString(s):
		return Progress
	case maybeError(s) && errorRe.MatchString(s):
		return Error
	default:
		return Plain
	}
}

// maybeError is a case-insensitive trigram prefilter for errorRe: the regex
// costs ~1.7µs per plain line, the Contains scan ~60ns. Every errorRe word
// carries one of these trigrams; a line without any cannot match.
func maybeError(s string) bool {
	for i := 0; i+3 <= len(s); i++ {
		a, b, c := s[i]|32, s[i+1]|32, s[i+2]|32
		switch [3]byte{a, b, c} {
		case [3]byte{'e', 'r', 'r'}, [3]byte{'f', 'a', 'i'}, [3]byte{'p', 'a', 'n'},
			[3]byte{'f', 'a', 't'}, [3]byte{'d', 'e', 'n'}, [3]byte{'r', 'e', 'f'},
			[3]byte{'t', 'i', 'm'}, [3]byte{'e', 'x', 'c'}, [3]byte{'t', 'r', 'a'}:
			return true
		}
	}
	return false
}

// classifyDiff recognizes unified-diff lines and reports whether the line was
// one. A diff is entered on an unmistakable header and left on the first line
// that cannot belong to one, so ordinary prose starting with "-" is never
// mistaken for a deletion.
func (n *Normalizer) classifyDiff(s string) (Kind, bool) {
	if diffStartRe.MatchString(s) {
		n.inDiff = true
		switch {
		case strings.HasPrefix(s, "+++"):
			return DiffAdd, true
		case strings.HasPrefix(s, "---"):
			return DiffDel, true
		default:
			return DiffMeta, true
		}
	}
	if !n.inDiff {
		return Plain, false
	}
	switch s[0] {
	case '+':
		return DiffAdd, true
	case '-':
		return DiffDel, true
	case ' ':
		return Plain, true // context line: real content, no emphasis
	case '\\':
		return DiffMeta, true // "\ No newline at end of file"
	}
	n.inDiff = false
	return Plain, false
}

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
