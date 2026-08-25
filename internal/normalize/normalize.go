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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Kind classifies a surviving line so a UI can color it and a summary can find
// results without a second parse.
type Kind uint8

const (
	Plain    Kind = iota // ordinary narration
	Tool                 // the agent invoking a tool or touching a file
	Error                // something the agent reports as broken
	Result               // the run's own protocol lines: RESULT:, PATH:, RELEVANT:
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
	spinnerRe = regexp.MustCompile(`^[\s⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣾⣽⣻⢿⡿⣟⣯⣷◐◓◑◒⠁⠂⠄⡀⢀⠠⠐⠈|/\-\\●○◎◌⣿▁▂▃▄▅▆▇█▏▎▍▌▋▊▉·．]*$`)

	// Box drawing only: agent CLIs frame their panels, which a dashboard
	// re-frames anyway.
	boxRe = regexp.MustCompile(`^[\s─│┌┐└┘├┤┬┴┼━┃┏┓┗┛┣┫┳┻╋╭╮╰╯═║╔╗╚╝╠╣╦╩╬▔▁]*$`)

	// Agent CLIs draw a gutter down the left of tool output: opencode uses
	// "|", claude uses "⏺" and "⎿", codex uses "•". The gutter is decoration;
	// what follows it is the line.
	gutterRe = regexp.MustCompile(`^\s*(?:[|│┃⎿⏺•▌]+\s*)+`)

	// After the gutter is gone, a line of nothing but punctuation carries no
	// information. Bare "\x1b[0m" resets, which opencode prints between
	// blocks, reduce to empty here.
	punctOnlyRe = regexp.MustCompile(`^[\s|│┃⎿⏺•▌·:;,.\-_=+~^*]*$`)

	// opencode's session header: "> build · ox-alpha-free" (mode and model).
	opencodeHeaderRe = regexp.MustCompile(`^>\s+\S+\s+·\s+\S+\s*$`)

	progressRe = regexp.MustCompile(`(?i)^\s*(reading|searching|scanning|thinking|indexing|analyzing|analysing|processing|loading|writing|compiling|running|checking|fetching|downloading|uploading|applying|saving|generating|querying|watching|waiting)\b`)

	toolRe = regexp.MustCompile(`(?i)^\s*(?:[✓✔✗✘⏺⎿·•>]+\s*)?(bash|read|edit|write|grep|glob|list|search|patch|apply_patch|str_replace|multiedit|todowrite|todoread|webfetch|websearch|task|shell|exec)\b[\s(:]`)

	errorRe = regexp.MustCompile(`(?i)\b(error|failed|failure|exception|traceback|panic|fatal|refused|denied|timed out)\b`)

	resultRe = regexp.MustCompile(`^\s*(RESULT|PATH|RELEVANT|COMMIT):`)

	// Diff recognition. Agents paste unified diffs constantly, and a diff read
	// as prose is unreadable: the sign at the start of the line is the whole
	// meaning. These match the shapes that can only be a diff.
	diffStartRe = regexp.MustCompile(`^(diff --git |index [0-9a-f]{4,}|--- (a/|/dev/null)|\+\+\+ (b/|/dev/null)|@@ .* @@|=== modified file)`)
	hunkRe      = regexp.MustCompile(`^@@ .* @@`)
)

// Config tunes one Normalizer. The zero value is usable: no rate limiting.
type Config struct {
	// MaxLinesPerSec caps the lines one stream may emit; a burst beyond the
	// cap is dropped and reported once via Suppressed. 0 disables the cap.
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
	if now.Sub(n.windowFrom) >= time.Second {
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
	s = strings.TrimRight(s, " \t")
	if s == "" || punctOnlyRe.MatchString(s) || spinnerRe.MatchString(s) || boxRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// stripControl removes C0/C1 controls and Unicode formatting characters
// (bidi overrides included) that can drive or spoof a terminal. Tabs become
// spaces so alignment survives.
func stripControl(s string) string {
	if !strings.ContainsFunc(s, isControl) {
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

func isControl(r rune) bool {
	return r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
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
	case errorRe.MatchString(s):
		return Error
	default:
		return Plain
	}
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
	if hunkRe.MatchString(s) {
		return DiffMeta, true
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
// text (file names, prompt descriptions, agent output shown outside the feed).
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

// Truncate cuts s to at most w visible cells, without splitting a UTF-8
// sequence, marking the cut with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 1 {
		return s
	}
	n := 0
	for i := range s {
		if n == w {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
