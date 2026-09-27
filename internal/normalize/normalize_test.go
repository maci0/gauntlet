// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package normalize

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// push feeds lines and returns every emitted text, flushing at the end.
func push(n *Normalizer, lines ...string) []Line {
	var out []Line
	for _, l := range lines {
		out = append(out, n.Push(l)...)
	}
	return append(out, n.Flush()...)
}

func texts(lines []Line) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Text)
	}
	return out
}

func TestDropsNoise(t *testing.T) {
	cases := map[string]string{
		"empty":                     "",
		"spaces":                    "   ",
		"spinner":                   "⠋",
		"spinner run":               "  ⠙ ⠹ ⠸ ",
		"box drawing":               "╭──────────╮",
		"box drawing unicode space": "╭───\u00a0──────╮",
		"unicode spaces":            "\u00a0\u3000   ",
		"spinner unicode space":     "  ⠙\u00a0⠹ ⠸ ",
		"bullets":                   "· · ·",
		"ansi only":                 "\x1b[2K\x1b[1G",
		"osc title":                 "\x1b]0;claude\x07",
		"block ramp":                "▁▂▃▄▅▆▇█",
		"control only":              "\x00\x01\x02",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got := push(New(Config{}), in); len(got) != 0 {
				t.Fatalf("expected %q to be dropped, got %q", in, texts(got))
			}
		})
	}
}

func TestStripsEscapesKeepsText(t *testing.T) {
	got := push(New(Config{}), "\x1b[32m✓\x1b[0m Read \x1b[1mmain.go\x1b[0m")
	if len(got) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(got), texts(got))
	}
	if want := "✓ Read main.go"; got[0].Text != want {
		t.Fatalf("got %q, want %q", got[0].Text, want)
	}
	if got[0].Kind != Tool {
		t.Fatalf("got kind %v, want Tool", got[0].Kind)
	}
}

func TestCarriageReturnKeepsLastSegment(t *testing.T) {
	// A progress line rewritten in place: only what the terminal would show
	// survives.
	got := push(New(Config{}), "downloading 10%\rdownloading 55%\rdownloading 100%")
	if len(got) != 1 || got[0].Text != "downloading 100%" {
		t.Fatalf("got %q, want [downloading 100%%]", texts(got))
	}
}

func TestCollapsesConsecutiveDuplicates(t *testing.T) {
	got := push(New(Config{}), "same", "same", "same", "different")
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %q", texts(got))
	}
	if got[0].Repeat != 3 {
		t.Fatalf("want repeat 3, got %d", got[0].Repeat)
	}
	if got[1].Text != "different" || got[1].Repeat != 1 {
		t.Fatalf("unexpected second line: %+v", got[1])
	}
}

func TestCollapsesProgressByVerb(t *testing.T) {
	got := push(New(Config{}),
		"Reading src/a.go", "Reading src/b.go", "Reading src/c.go",
		"Searching for callers", "Reading src/d.go")
	want := []string{"Reading src/a.go", "Searching for callers", "Reading src/d.go"}
	if strings.Join(texts(got), "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", texts(got), want)
	}
}

func TestRateLimitSummarizes(t *testing.T) {
	now := time.Unix(0, 0)
	n := New(Config{MaxLinesPerSec: 2, Now: func() time.Time { return now }})
	var out []Line
	for i := range 10 {
		out = append(out, n.Push(strings.Repeat("x", i+1))...)
	}
	out = append(out, n.Flush()...)
	// Two lines pass the window, the rest are summarized in one note.
	if len(out) != 3 {
		t.Fatalf("want 2 lines plus a summary, got %q", texts(out))
	}
	last := out[len(out)-1].Text
	if !strings.Contains(last, "8 lines suppressed") {
		t.Fatalf("want a suppression note, got %q", last)
	}
}

func TestRateLimitWindowStartsAtFirstLine(t *testing.T) {
	for _, start := range []time.Time{
		{},
		time.Time{}.Add(500 * time.Millisecond),
		time.Unix(0, 0),
	} {
		t.Run(start.String(), func(t *testing.T) {
			now := start
			n := New(Config{MaxLinesPerSec: 1, Now: func() time.Time { return now }})
			var out []Line
			out = append(out, n.Push("first")...)
			now = start.Add(750 * time.Millisecond)
			out = append(out, n.Push("suppressed")...)
			now = start.Add(time.Second)
			out = append(out, n.Push("next window")...)
			out = append(out, n.Flush()...)
			want := "first|next window|… 1 lines suppressed (rate limit)"
			if got := strings.Join(texts(out), "|"); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestRateLimitClockStepBackward(t *testing.T) {
	start := time.Unix(100, 0)
	now := start
	n := New(Config{MaxLinesPerSec: 1, Now: func() time.Time { return now }})
	var out []Line
	out = append(out, n.Push("line 1")...)
	// Clock steps backward (NTP step or manual clock change)
	now = start.Add(-10 * time.Second)
	out = append(out, n.Push("line 2")...)
	out = append(out, n.Flush()...)
	want := "line 1|line 2"
	if got := strings.Join(texts(out), "|"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		{"RESULT: changed=3", Result},
		{"PATH: internal/x.go: fixed the leak", Result},
		{"RELEVANT: sec-review: has auth code", Result},
		{"Bash(go test ./...)", Tool},
		{"Error: cannot open file", Error},
		{"the build failed", Error},
		{"Analyzing the module graph", Progress},
		{"just some narration", Plain},
		{"terror haunts the pantry: terror", Plain},
		{"a timeout builds the tower: timeout", Plain},
	}
	for _, c := range cases {
		got := push(New(Config{}), c.in)
		if len(got) != 1 {
			t.Fatalf("%q was dropped", c.in)
		}
		if got[0].Kind != c.want {
			t.Errorf("%q: got kind %v, want %v", c.in, got[0].Kind, c.want)
		}
	}
}

// TestMaybeErrorCoversErrorWords pins the prefilter contract: every word the
// error regex matches must trip maybeError, or classify silently downgrades
// Error lines to Plain. This is a work counter, not a timing gate: it holds
// on a loaded machine.
func TestMaybeErrorCoversErrorWords(t *testing.T) {
	words := []string{"error", "failed", "failure", "exception", "traceback", "panic", "fatal", "refused", "denied", "timed out"}
	for _, w := range words {
		for _, s := range []string{w, "x " + w + " y", strings.ToUpper(w)} {
			if !maybeError(s) {
				t.Errorf("maybeError(%q) = false, errorRe matches", s)
			}
			if got := push(New(Config{}), "msg "+s); len(got) != 1 || got[0].Kind != Error {
				t.Errorf("classify(%q) = %+v, want Error", s, got)
			}
		}
	}
}

func TestSanitizeStripsBidiAndControls(t *testing.T) {
	// U+202E reverses everything after it on a terminal, and BEL rings it:
	// both must vanish while the visible text and the tab's spacing survive.
	in := "safe‮codename\ttab"
	if got, want := Sanitize(in), "safecodename tab"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A file name off a Unix filesystem may hold bytes that are not UTF-8 at all.
// They are repaired the same way whether or not a control character sits
// beside them: the answer must not depend on the rest of the string, and JSON
// rewrites them to U+FFFD wherever a run keeps one.
func TestSanitizeRepairsInvalidUTF8(t *testing.T) {
	const bad = string(utf8.RuneError)
	cases := map[string]string{
		"a\xffb":     "a" + bad + "b",
		"caf\xe9.md": "caf" + bad + ".md",
		// One replacement character per bad byte: that is what decoding the
		// string yields, not one per run of them.
		"\xff\xfe\xfd": bad + bad + bad,
	}
	for in, want := range cases {
		got := Sanitize(in)
		if got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	// The same bytes beside a control character: both are dealt with, and the
	// answer for the invalid bytes does not change.
	if got, want := Sanitize("a\xff\x01b"), "a�b"; got != want {
		t.Errorf("Sanitize = %q, want %q", got, want)
	}
	if got := Display("a\xffb\x1b[31m"); got != "a�b" {
		t.Errorf("Display = %q, want %q", got, "a�b")
	}
}

func TestTruncatesVeryLongLines(t *testing.T) {
	got := push(New(Config{MaxWidth: 10}), strings.Repeat("a", 500))
	if len(got) != 1 {
		t.Fatal("line dropped")
	}
	if len([]rune(got[0].Text)) != 11 { // 10 plus the ellipsis
		t.Fatalf("got %d runes: %q", len([]rune(got[0].Text)), got[0].Text)
	}
}

// Real output shapes seen from the agent CLIs, kept as a table so a new agent
// can be added by pasting a line rather than reading the regexes.
func TestAgentSpecificNoise(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means the line should be dropped
		kind Kind
	}{
		// opencode prints bare resets between blocks, and a session header.
		{"opencode reset", "\x1b[0m", "", Plain},
		{"opencode header", "> build · ox-alpha-free", "> build · ox-alpha-free", Progress},
		{"opencode gutter tool", "|  Read  internal/app.go", "Read  internal/app.go", Tool},
		{"opencode gutter text", "|  found three callers", "found three callers", Plain},
		{"opencode empty gutter", "|", "", Plain},
		{"opencode gutter rule", "|  ----------", "", Plain},
		// claude marks tool calls with a bullet and results with a corner.
		{"claude tool", "⏺ Bash(go test ./...)", "Bash(go test ./...)", Tool},
		{"claude result gutter", "⎿  ok  github.com/x/y  0.2s", "ok  github.com/x/y  0.2s", Plain},
		// codex indents with a middle dot.
		{"codex bullet", "• Read src/main.rs", "Read src/main.rs", Tool},
		// The protocol lines every agent must end with survive untouched.
		{"result line", "RESULT: changed=2", "RESULT: changed=2", Result},
		// Tabs become spaces even when no other control character would
		// otherwise send the line through the rewrite.
		{"tab alignment", "a\tb", "a    b", Plain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := push(New(Config{}), c.in)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("expected %q to be dropped, got %q", c.in, texts(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected one line from %q, got %q", c.in, texts(got))
			}
			if got[0].Text != c.want {
				t.Fatalf("got %q, want %q", got[0].Text, c.want)
			}
			if got[0].Kind != c.kind {
				t.Fatalf("%q: got kind %v, want %v", c.in, got[0].Kind, c.kind)
			}
		})
	}
}

func TestRepeatedSessionHeaderCollapses(t *testing.T) {
	got := push(New(Config{}),
		"> build · ox-alpha-free",
		"> build · ox-alpha-free",
		"doing work",
		"> build · ox-alpha-free")
	if len(got) != 3 {
		t.Fatalf("want header, work, header: %q", texts(got))
	}
}

func TestGutterDoesNotEatRealContent(t *testing.T) {
	// A pipe inside a command is data, not a gutter.
	got := push(New(Config{}), "Bash(rg foo | head -3)")
	if len(got) != 1 || got[0].Text != "Bash(rg foo | head -3)" {
		t.Fatalf("mangled a real pipe: %q", texts(got))
	}
}

func TestUnifiedDiffIsClassifiedBySign(t *testing.T) {
	n := New(Config{})
	lines := []string{
		"Here is the patch:",
		"diff --git a/main.go b/main.go",
		"index 1234567..89abcde 100644",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -10,7 +10,7 @@ func main() {",
		" unchanged context",
		"-\tfmt.Println(\"old\")",
		"+\tfmt.Println(\"new\")",
		"\\ No newline at end of file",
	}
	var got []Line
	for _, l := range lines {
		got = append(got, n.Push(l)...)
	}
	got = append(got, n.Flush()...)

	want := []Kind{Plain, DiffMeta, DiffMeta, DiffDel, DiffAdd, DiffMeta, Plain, DiffDel, DiffAdd, DiffMeta}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %q", len(got), len(want), texts(got))
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("line %d (%q): got kind %v, want %v", i, got[i].Text, got[i].Kind, want[i])
		}
	}
}

func TestProseIsNotMistakenForADiff(t *testing.T) {
	// Bullet lists and CLI flags start with a dash but are not deletions.
	// Push holds a line back until the next one arrives (duplicate
	// collapsing), so the whole batch is fed and then flushed.
	got := push(New(Config{}),
		"- checked the config loader",
		"-v enables verbose output",
		"+1 to that approach")
	if len(got) != 3 {
		t.Fatalf("want three lines, got %q", texts(got))
	}
	for _, l := range got {
		if l.Kind == DiffAdd || l.Kind == DiffDel {
			t.Errorf("%q was read as a diff line (%v)", l.Text, l.Kind)
		}
	}
}

func TestDiffModeEndsAtProse(t *testing.T) {
	n := New(Config{})
	feed := []string{
		"@@ -1,2 +1,2 @@",
		"-old line",
		"+new line",
		"That fixes the leak.", // leaves diff mode
		"- and here is a bullet",
	}
	var got []Line
	for _, l := range feed {
		got = append(got, n.Push(l)...)
	}
	got = append(got, n.Flush()...)
	last := got[len(got)-1]
	if last.Kind == DiffDel {
		t.Fatalf("diff mode leaked into prose: %q", last.Text)
	}
}

func TestDisplayStripsTerminalDrivingBytes(t *testing.T) {
	cases := map[string]string{
		"plain text":          "RESULT: changed=3",
		"csi colors":          "\x1b[32mok\x1b[0m",
		"cursor moves":        "\x1b[2K\x1b[1G\x1b[31;1mx",
		"osc title":           "\x1b]0;pwned\x07after",
		"osc st":              "\x1b]8;;http://x\x1b\\link",
		"controls":            "a\x00\x01\x07b",
		"bidi override":       "user\u202Eevil",
		"zero widths":         "hidden\u200binvisible\uFEFF!",
		"tab":                 "a\tb",
		"unterminated":        "\x1b[31mno reset",
		"lone esc":            "before\x1bafter",
		"c1 controls":         "a\x9bb",
		"carriage return":     "left\rright",
		"line separator":      "line\u2028split",
		"paragraph separator": "para\u2029split",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := Display(in)
			for _, r := range got {
				if r == 0x1b || (unicode.IsControl(r) && r != ' ') || unicode.Is(unicode.Cf, r) ||
					unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
					t.Fatalf("Display(%q) = %q still carries terminal-driving %q", in, got, r)
				}
			}
			if name == "plain text" && got != in {
				t.Fatalf("plain text changed: got %q", got)
			}
			if name == "csi colors" && got != "ok" {
				t.Fatalf("got %q, want \"ok\"", got)
			}
			if name == "bidi override" && got != "userevil" {
				t.Fatalf("got %q, want \"userevil\"", got)
			}
			if name == "line separator" && got != "linesplit" {
				t.Fatalf("got %q, want \"linesplit\"", got)
			}
			if name == "paragraph separator" && got != "parasplit" {
				t.Fatalf("got %q, want \"parasplit\"", got)
			}
		})
	}
}

func TestClipKeepsGraphemesIntact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"accent", "abe\u0301xyz", 3, "ab"},
		{"flag", "ab🇵🇱xyz", 3, "ab"},
		{"variation selector", "ab\u2708\ufe0fxyz", 3, "ab"},
		{"skin tone", "ab👍🏽xyz", 3, "ab"},
		{"joined emoji", "ab👩\u200d💻xyz", 4, "ab"},
		{"cluster fits", "abe\u0301xyz", 4, "abe\u0301"},
		{"exact fit", "abe\u0301", 4, "abe\u0301"},
		{"oversized cluster", "e\u0301\u0302x", 2, ""},
		{"zero width", "abc", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clip(tc.input, tc.width); got != tc.want {
				t.Fatalf("Clip(%q, %d) = %q, want %q", tc.input, tc.width, got, tc.want)
			}
		})
	}
}

func TestTruncateKeepsUTF8Intact(t *testing.T) {
	in := "héllo wörld"
	got := Truncate(in, 6)
	if want := "héllo …"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if Truncate(in, 100) != in {
		t.Fatal("short string was modified")
	}
	if Truncate(in, 1) != in {
		t.Fatal("degenerate width must be a no-op")
	}
}

func TestTruncateKeepsGraphemesIntact(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"accent", "abe\u0301xyz", 3, "ab…"},
		{"flag", "ab🇵🇱xyz", 3, "ab…"},
		{"variation selector", "ab\u2708\ufe0fxyz", 3, "ab…"},
		{"skin tone", "ab👍🏽xyz", 3, "ab…"},
		{"joined emoji", "ab👩\u200d💻xyz", 4, "ab…"},
		{"cluster fits", "abe\u0301xyz", 4, "abe\u0301…"},
		{"exact fit", "abe\u0301", 4, "abe\u0301"},
		{"oversized cluster", "e\u0301\u0302x", 2, "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.input, tc.width); got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.input, tc.width, got, tc.want)
			}
		})
	}
}

func TestNormalizerTruncatesWholeGraphemes(t *testing.T) {
	n := New(Config{MaxWidth: 3})
	got := append(n.Push("abe\u0301xyz"), n.Flush()...)
	if len(got) != 1 || got[0].Text != "ab…" {
		t.Fatalf("normalized lines = %v, want [ab…]", texts(got))
	}
}

// FuzzNormalizerStream drives the stateful pipeline (clean, diff tracking,
// duplicate collapse, truncation) with an arbitrary line sequence, the same
// path every raw agent output line takes. Pinned contracts: whatever the
// state machine does, nothing terminal-driving is ever emitted, a width cap
// bounds every line, repeats count real emissions, a second Flush is silent,
// and replaying the same stream through a fresh Normalizer reproduces the
// exact output (state never leaks between streams).
func FuzzNormalizerStream(f *testing.F) {
	seeds := []string{
		"reading files…\nreading tests…\nRESULT: changed=2",
		"diff --git a/pool.go b/pool.go\n--- a/pool.go\n+++ b/pool.go\n@@ -1,2 +1,2 @@\n-old\n+new\n context",
		"| continuation\n⏺ tool use\n•another gutter",
		"\x1b[32m✓\x1b[0m done\r\x1b[2K\x1b[1Grepainted\n\x1b]0;title\x07",
		"same line\nsame line\nsame line\nother\nsame line",
		"error: failed\npanic: boom\nplain narration",
		"⠋\n╭───╮\n│ box │\n╰───╯\n· · ·",
		"a\tb\nc\rd",
		strings.Repeat("x", 500),
	}
	for _, s := range seeds {
		f.Add([]byte(s), 80)
	}
	f.Fuzz(func(t *testing.T, data []byte, maxWidth int) {
		if maxWidth < 0 {
			maxWidth = 0
		} else if maxWidth > 4096 {
			maxWidth = 4096
		}
		cfg := Config{MaxWidth: maxWidth}
		run := func() (out, tail []Line) {
			n := New(cfg)
			for line := range strings.SplitSeq(string(data), "\n") {
				out = append(out, n.Push(line)...)
			}
			out = append(out, n.Flush()...)
			return out, n.Flush()
		}
		got, extra := run()
		if len(extra) != 0 {
			t.Fatalf("the Flush after Flush emitted %v", texts(extra))
		}
		for _, l := range got {
			if l.Repeat < 1 {
				t.Fatalf("emitted line with Repeat %d: %q", l.Repeat, l.Text)
			}
			for _, r := range l.Text {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Fatalf("stream emitted terminal-driving %q in %q", r, l.Text)
				}
			}
			if maxWidth > 1 && utf8.RuneCountInString(l.Text) > maxWidth+1 {
				t.Fatalf("width %d exceeded: %q", maxWidth, l.Text)
			}
		}
		// A normalizer that has seen nothing must have nothing to say.
		if n := New(cfg); len(n.Flush()) != 0 {
			t.Fatal("Flush on a fresh normalizer emitted a line")
		}
		// Replay determinism: one stream's state must not shape another's.
		again, againTail := run()
		if len(againTail) != 0 || fmt.Sprint(again) != fmt.Sprint(got) {
			t.Fatalf("replay diverged:\n got %v (tail %v)\nwant %v", again, againTail, got)
		}
	})
}

// FuzzDisplay pins the one contract every caller of Display depends on: no
// input, however crafted, leaves a control, formatting, or escape rune that
// could drive or spoof a terminal.
func FuzzDisplay(f *testing.F) {
	for _, s := range []string{
		"\x1b[32m✓\x1b[0m Read main.go",
		"\x1b]0;title\x07rest",
		"\x1b]0;unterminated",
		strings.Repeat("\x1b[", 100),
		"\u202Ereversed\u202C",
		"a\x00b\x07\x08\x9b",
		"\xc3\xa9plain",
		"tab\tkept as space",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Display(s)
		for _, r := range got {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("Display(%q) = %q still carries terminal-driving %q", s, got, r)
			}
		}
	})
}

// FuzzDisplayWriter drives the writer a child process's raw output goes
// through. FuzzDisplay covers the text transform; this covers the buffering
// around it, which is where the arithmetic lives: drain cuts the held line at
// maxPendingBytes and walks back to a rune boundary, and a byte at a time is
// the worst case for that, since no write boundary may be assumed to line up
// with a newline or with the cut.
//
// Two paths run for every input. The raw one asserts the sink's contracts:
// nothing terminal-driving reaches the terminal, the output is decodable text
// however the held line was split, no newline is invented, and the buffer
// stays bounded no matter how long a line grows. The translated one replaces
// each input byte with an inert rune of varying width, so the content is
// always something Display leaves alone and the writer has to be a transparent
// pass-through: any byte dropped, duplicated, or split mid-character by the
// cut shows up as a diff against the input. That is the only assertion here
// that can fail without a crash, and it is the one the cap path needs.
func FuzzDisplayWriter(f *testing.F) {
	for _, s := range []string{
		"",
		"indexed a/b.go\nwarning: 2 files skipped\n",
		"no trailing newline",
		"\n\n\n",
		"trailing newline\n",
		"héllo 中文 🎯 wide runes at the cut",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, s string, oversize bool) {
		t.Run("raw", func(t *testing.T) {
			var out bytes.Buffer
			w := NewDisplayWriter(&out)
			for i := 0; i < len(s); i++ {
				n, err := w.Write([]byte(s[i : i+1]))
				if err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("Write reported %d bytes for a 1-byte write", n)
				}
				if len(w.held) > maxPendingBytes+utf8.UTFMax {
					t.Fatalf("held buffer grew to %d bytes on a %d-byte line", len(w.held), len(s))
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if !utf8.ValidString(got) {
				t.Fatalf("writer emitted undecodable text: %q", got)
			}
			for _, r := range got {
				// The newline is the writer's own terminator, appended after
				// Display has run, so it is the one control it may emit.
				if r == '\n' {
					continue
				}
				if r == 0x1b {
					t.Fatalf("writer emitted an escape byte: %q", got)
				}
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					t.Fatalf("writer emitted terminal-driving %q in %q", r, got)
				}
			}
			// Every newline the input carried is a line the writer owes the
			// reader, and a final unterminated line owes none: drain appends
			// the terminator, so the counts have to match exactly.
			if n, want := strings.Count(got, "\n"), strings.Count(s, "\n"); n != want {
				t.Fatalf("writer emitted %d newlines for %d in the input", n, want)
			}
			if len(w.held) != 0 {
				t.Fatalf("Flush left %d bytes held: %q", len(w.held), w.held)
			}
		})
		t.Run("transparent", func(t *testing.T) {
			safe := inert(s)
			if oversize {
				// Cross maxPendingBytes so the cut and its walk back to a
				// rune boundary run, with a multi-byte rune deliberately
				// straddling the cut.
				safe = strings.Repeat("中", maxPendingBytes) + safe
			}
			var out bytes.Buffer
			w := NewDisplayWriter(&out)
			for i := 0; i < len(safe); i++ {
				if _, err := w.Write([]byte(safe[i : i+1])); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != safe {
				t.Fatalf("writer changed inert text:\n got %d bytes\nwant %d bytes", len(got), len(safe))
			}
		})
	})
}

// inertRunes is every rune inert translates an input byte to: no escape, no
// control or formatting rune, all valid UTF-8, and deliberately of differing
// byte widths so the cut has to find a real rune boundary.
var inertRunes = []rune{'a', 'b', 'Z', '0', '9', '-', '_', '.', ':', ' ', '\n', 'é', '中', '🎯'}

// inert maps s to a string Display returns unchanged, one rune per input
// byte, so the fuzzed content still shapes the line structure and the buffer
// while remaining something the writer must reproduce exactly.
func inert(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := range len(s) {
		b.WriteRune(inertRunes[int(s[i])%len(inertRunes)])
	}
	return b.String()
}

// DisplayWriter is a pass-through for a program that writes plain text: the
// same bytes in, the same bytes out, whatever the write boundaries. Only the
// sequences and control characters Display strips are removed.
func TestDisplayWriterPassesPlainTextThrough(t *testing.T) {
	const text = "indexed a/b.go\nwarning: 2 files skipped\n"
	var got bytes.Buffer
	w := NewDisplayWriter(&got)
	// One byte at a time is the worst case for a line-buffering writer: no
	// write boundary may be assumed to line up with a newline.
	for i := range len(text) {
		n, err := w.Write([]byte(text[i : i+1]))
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("Write reported %d bytes for a 1-byte write", n)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got.String() != text {
		t.Fatalf("plain text was altered:\n got %q\nwant %q", got.String(), text)
	}
}

// A blank line is a line. drain used to return early whenever it held
// nothing, so the terminator of an empty line went out with it and every
// blank line a child wrote vanished.
func TestDisplayWriterKeepsBlankLines(t *testing.T) {
	for _, text := range []string{"\n", "\n\n\n", "a\n\nb\n", "\n\n\n\n\n", "a\n\n\n"} {
		var got bytes.Buffer
		w := NewDisplayWriter(&got)
		if _, err := w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if got.String() != text {
			t.Fatalf("blank lines were dropped:\n got %q\nwant %q", got.String(), text)
		}
	}
}

// Past maxPendingBytes the writer emits the oldest part of an unterminated
// line so the buffer stays bounded, and that cut must land between characters.
// The cap is a byte count and every rune in this line is three bytes wide, so
// the cut falls inside a character on most offsets; a boundary search that
// walks the wrong way never finds one and repairs the tail to U+FFFD instead,
// which loses the character rather than deferring it.
func TestDisplayWriterCutsOnRuneBoundary(t *testing.T) {
	// maxPendingBytes is not a multiple of three, so the cut lands mid-rune on
	// some drains and between runes on others. Both must come out whole.
	for _, pad := range []int{0, 1, 2} {
		line := strings.Repeat("中", pad) + strings.Repeat("x", maxPendingBytes) + strings.Repeat("中", 64)
		var got bytes.Buffer
		w := NewDisplayWriter(&got)
		// One byte at a time: no write boundary may be assumed to line up
		// with the cut, which is the case the cap logic actually runs in.
		for i := 0; i < len(line); i++ {
			if _, err := w.Write([]byte(line[i : i+1])); err != nil {
				t.Fatal(err)
			}
			if len(w.held) > maxPendingBytes+utf8.UTFMax {
				t.Fatalf("held buffer grew to %d bytes, past the cap", len(w.held))
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if got.String() != line {
			t.Fatalf("pad %d: the cut split a character", pad)
		}
	}
}

// runeBoundary answers the question the cut turns on: where does b[:i] stop
// splitting a UTF-8 sequence. The byte before a boundary is a continuation
// byte, so the answer is the nearest rune start at or before the cut, and in a
// line of nothing but multi-byte runes no byte after the first is one.
func TestRuneBoundary(t *testing.T) {
	for _, tc := range []struct {
		in   string
		cut  int
		want int
	}{
		{"abc", 3, 3},
		{"abc", 2, 2},
		{"中中中", 9, 9},
		{"中中中", 8, 6},
		{"中中中", 7, 6},
		{"中中中", 5, 3},
		{"中中中", 1, 0},
		{"", 0, 0},
		{"中", 1, 0},
		// A cut past the end of the buffer clamps rather than reading it.
		{"abc", 99, 3},
	} {
		if got := runeBoundary([]byte(tc.in), tc.cut); got != tc.want {
			t.Errorf("runeBoundary(%q, %d) = %d, want %d", tc.in, tc.cut, got, tc.want)
		}
	}
}

// Whatever a hostile producer puts in a line, only the escape, control, and
// formatting characters come out the far side.
func TestDisplayWriterStripsHostileSequences(t *testing.T) {
	var got bytes.Buffer
	w := NewDisplayWriter(&got)
	hostile := "\x1b[2J\x1b[31mindexed \x1b]0;title\x07a\u202eb.go\u0007\n"
	if _, err := w.Write([]byte(hostile)); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "indexed ab.go\n"
	if got.String() != want {
		t.Fatalf("hostile line was not sanitized:\n got %q\nwant %q", got.String(), want)
	}
}

// A child killed mid-line leaves bytes with no newline. They are the last
// thing it owes the reader, so Flush has to put them out; sanitized, because
// the same attacker controls them.
func TestDisplayWriterFlushesUnterminatedTail(t *testing.T) {
	var got bytes.Buffer
	w := NewDisplayWriter(&got)
	if _, err := w.Write([]byte("done\npartial \x1b[31mline")); err != nil {
		t.Fatal(err)
	}
	if got.String() != "done\n" {
		t.Fatalf("a partial line was written before it was terminated: %q", got.String())
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := "done\npartial line"; got.String() != want {
		t.Fatalf("Flush dropped or altered the tail:\n got %q\nwant %q", got.String(), want)
	}
}

// A child that never emits a newline must not be able to grow the pending
// buffer without bound: past the cap the oldest rune-aligned prefix goes out
// and the rest stays held, so nothing is dropped and no rune is cut in half.
func TestDisplayWriterBoundsThePendingLine(t *testing.T) {
	var got bytes.Buffer
	w := NewDisplayWriter(&got)
	// The 3-byte euro sign starts one byte before the cap, so a cut at the cap
	// would land inside it.
	chunk := strings.Repeat("x", maxPendingBytes-1) + "\u20ac" + "y"
	const chunks = 4
	for range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got.Len() >= len(chunk)*chunks {
		t.Fatalf("the pending line was never bounded: %d bytes held", got.Len())
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := strings.Repeat(chunk, chunks); got.String() != want {
		t.Fatalf("bounding lost or reordered bytes: got %d bytes, want %d", got.Len(), len(want))
	}
	if strings.ContainsRune(got.String(), 0xFFFD) {
		t.Fatal("the bound split a UTF-8 sequence")
	}
}
