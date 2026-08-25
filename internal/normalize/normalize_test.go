// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package normalize

import (
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
		"empty":        "",
		"spaces":       "   ",
		"spinner":      "⠋",
		"spinner run":  "  ⠙ ⠹ ⠸ ",
		"box drawing":  "╭──────────╮",
		"bullets":      "· · ·",
		"ansi only":    "\x1b[2K\x1b[1G",
		"osc title":    "\x1b]0;claude\x07",
		"block ramp":   "▁▂▃▄▅▆▇█",
		"control only": "\x00\x01\x02",
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

func TestSanitizeStripsBidiAndControls(t *testing.T) {
	// U+202E reverses everything after it on a terminal, and BEL rings it:
	// both must vanish while the visible text and the tab's spacing survive.
	in := "safe‮codename\ttab"
	if got, want := Sanitize(in), "safecodename tab"; got != want {
		t.Fatalf("got %q, want %q", got, want)
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
		"plain text":      "RESULT: changed=3",
		"csi colors":      "\x1b[32mok\x1b[0m",
		"cursor moves":    "\x1b[2K\x1b[1G\x1b[31;1mx",
		"osc title":       "\x1b]0;pwned\x07after",
		"osc st":          "\x1b]8;;http://x\x1b\\link",
		"controls":        "a\x00\x01\x07b",
		"bidi override":   "user\u202Eevil",
		"zero widths":     "hidden\u200binvisible\uFEFF!",
		"tab":             "a\tb",
		"unterminated":    "\x1b[31mno reset",
		"lone esc":        "before\x1bafter",
		"c1 controls":     "a\x9bb",
		"carriage return": "left\rright",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := Display(in)
			for _, r := range got {
				if r == 0x1b || (unicode.IsControl(r) && r != ' ') || unicode.Is(unicode.Cf, r) {
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
