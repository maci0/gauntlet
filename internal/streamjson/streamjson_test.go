// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package streamjson

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

// The envelopes below follow the three API dialects the supported agents emit:
// Anthropic-shaped (claude, cursor-agent, kimi), Gemini-shaped (gemini, qwen),
// and OpenAI-shaped. The parser is deliberately envelope-agnostic, so these
// double as a statement of what it must survive.

// usageFound reports whether any counter was found.
func usageFound(u Usage) bool { return u.Output > 0 || u.Total > 0 || u.Thinking > 0 }

func TestAnthropicShapedLine(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"text","text":"Fixed the leak in pool.go"}],
		"usage":{"input_tokens":900,"output_tokens":120,
		"output_tokens_details":{"thinking_tokens":40}}}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Text != "Fixed the leak in pool.go" {
		t.Fatalf("text: %q", ev.Text)
	}
	if ev.Usage.Output != 120 || ev.Usage.Thinking != 40 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
}

func TestGeminiShapedLine(t *testing.T) {
	line := `{"type":"assistant","content":{"parts":[{"text":"done"}]},
		"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":17,
		"thoughtsTokenCount":9,"totalTokenCount":76}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Usage.Output != 17 || ev.Usage.Thinking != 9 || ev.Usage.Total != 76 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
	if !strings.Contains(ev.Text, "done") {
		t.Fatalf("text: %q", ev.Text)
	}
}

func TestOpenAIShapedLine(t *testing.T) {
	line := `{"choices":[{"delta":{"content":"partial output"}}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Usage.Output != 5 || ev.Usage.Total != 15 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
	if ev.Text != "partial output" {
		t.Fatalf("text: %q", ev.Text)
	}
}

func TestThinkingBlocksAreSeparatedFromOutput(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[
		{"type":"thinking","thinking":"the caller already checks nil"},
		{"type":"text","text":"No change needed."}]}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Thinking != "the caller already checks nil" {
		t.Fatalf("thinking: %q", ev.Thinking)
	}
	if ev.Text != "No change needed." {
		t.Fatalf("text: %q", ev.Text)
	}
}

func TestThinkingBlockWithPlainTextField(t *testing.T) {
	// grok emits reasoning deltas as a typed block whose payload is "text".
	line := `{"type":"stream_event","event":{"type":"reasoning_delta","text":"weighing options"}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Thinking != "weighing options" {
		t.Fatalf("thinking: %q, text: %q", ev.Thinking, ev.Text)
	}
	if ev.Text != "" {
		t.Fatalf("reasoning leaked into visible output: %q", ev.Text)
	}
}

func TestPlainTextIsNotJSON(t *testing.T) {
	for _, line := range []string{
		"Reading src/main.go",
		"",
		"[1, 2, 3]",   // an array is not an event envelope
		`{"broken": `, // truncated
		"⏺ Bash(go test ./...)",
	} {
		if _, ok := Parse([]byte(line)); ok {
			t.Errorf("%q should not parse as an event", line)
		}
	}
}

func TestUnknownEnvelopeContributesNothingRatherThanGarbage(t *testing.T) {
	// A shape nobody anticipated must not invent numbers or text.
	line := `{"kind":"progress","phase":"indexing","files":420}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if usageFound(ev.Usage) {
		t.Fatalf("invented usage: %+v", ev.Usage)
	}
	if ev.Text != "" || ev.Thinking != "" || usageFound(ev.Usage) {
		t.Fatalf("invented content: %+v", ev)
	}
}

func TestDeeplyNestedPayloadDoesNotRunAway(t *testing.T) {
	// A tool result can nest arbitrarily; the walk must stop and stay quiet.
	line := `{"type":"tool_result","content":` + strings.Repeat(`{"a":`, 40) + `"deep"` + strings.Repeat(`}`, 40) + `}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if strings.Contains(ev.Text, "deep") {
		t.Fatal("walked past the depth limit")
	}
}

func TestResultLineCarriesFinalUsage(t *testing.T) {
	line := `{"type":"result","subtype":"success","total_cost_usd":0.03,
		"usage":{"input_tokens":1000,"output_tokens":250,"total_tokens":1250}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Usage.Output != 250 || ev.Usage.Total != 1250 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
}

// The two shapes below were read out of the shipped binaries themselves
// (`strings` on grok and cursor-agent), so they record what those agents
// actually emit rather than what their docs claim.

func TestGrokStreamEventShape(t *testing.T) {
	line := `{"type":"stream_event","event":{"type":"text_delta","text":"patching pool.go"},
		"usage":{"input_tokens":4096,"output_tokens":88,"reasoning_tokens":31,"cached_tokens":2048}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Text != "patching pool.go" {
		t.Fatalf("text: %q", ev.Text)
	}
	if ev.Usage.Output != 88 || ev.Usage.Thinking != 31 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
}

func TestCursorAgentResultShape(t *testing.T) {
	line := `{"type":"result","subtype":"success","is_error":false,
		"usage":{"input_tokens":9000,"output_tokens":410}}`
	ev, ok := Parse([]byte(line))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Usage.Output != 410 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
}

func TestDshSessionRecordsCarryUsage(t *testing.T) {
	// dsh writes session records whose usage comes straight from the provider
	// response.
	event := `{"type":"assistant-message","usage":{"prompt_tokens":1200,"completion_tokens":260,` +
		`"reasoning_tokens":90,"cached_tokens":800}}`
	ev, ok := Parse([]byte(event))
	if !ok {
		t.Fatal("valid JSON was rejected")
	}
	if ev.Usage.Output != 260 || ev.Usage.Thinking != 90 {
		t.Fatalf("usage: %+v", ev.Usage)
	}
}

func TestAbsurdCountersReportNothing(t *testing.T) {
	// A float outside the range of int is not a measurement, and what a
	// conversion does with one differs by platform. It must read as absent.
	for _, line := range []string{
		`{"usage":{"output_tokens":1e30}}`,
		`{"usage":{"input_tokens":-5}}`,
		`{"usage":{"total_tokens":9223372036854775807}}`,
		// JSON numbers decode as float64; a fractional counter must not
		// truncate to an integer count (1.9 would have become 1).
		`{"usage":{"output_tokens":1.5}}`,
		`{"usage":{"output_tokens":1.9}}`,
	} {
		ev, ok := Parse([]byte(line))
		if !ok {
			t.Fatalf("%s should parse as JSON", line)
		}
		if usageFound(ev.Usage) {
			t.Fatalf("%s invented usage %+v", line, ev.Usage)
		}
	}
	// Sane large counters still count.
	ev, ok := Parse([]byte(`{"usage":{"output_tokens":1e6}}`))
	if !ok || ev.Usage.Output != 1000000 {
		t.Fatalf("large counter lost: ok=%v usage=%+v", ok, ev.Usage)
	}
}

// asInt's json.Number case is not on Parse's path today -- Parse decodes
// without UseNumber -- so nothing else here would notice it truncating. The
// float64 case spends five lines on exactly this hazard; the sibling case has
// to hold the same line, or enabling UseNumber one day would quietly turn a
// counter that does not fit an int into a small, plausible number.
func TestNumberCountersAreRangeChecked(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
		ok   bool
	}{
		{"120", 120, true},
		{"0", 0, false},
		{"-5", 0, false},
		{"9223372036854775808", 0, false}, // past int64: Int64 itself refuses
		{"1.5", 0, false},                 // not an integer count
		{"1099511627776", 1 << 40, true},  // trillion-token cap, inclusive
		{"1099511627777", 0, false},       // one past the cap
	} {
		got, ok := asInt(json.Number(c.in))
		if ok != c.ok || got != c.want {
			t.Errorf("asInt(json.Number(%q)) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	// The bound is int, not int64, so a 32-bit build refuses what it cannot
	// hold rather than reporting the low half of it. 2^33 sits under the
	// trillion-token cap and above 32-bit MaxInt.
	mid := json.Number(strconv.FormatInt(1<<33, 10))
	got, ok := asInt(mid)
	if want := math.MaxInt > 1<<33; ok != want {
		t.Errorf("asInt(%s) = %d, %v; want ok=%v on this platform", mid, got, ok, want)
	}
}

func TestFloatCountersAreIntegersInRange(t *testing.T) {
	// Parse's path: encoding/json yields float64, never json.Number.
	for _, c := range []struct {
		in   float64
		want int
		ok   bool
	}{
		{120, 120, true},
		{1e6, 1_000_000, true},
		{1 << 40, 1 << 40, true},
		{0, 0, false},
		{-5, 0, false},
		{1.5, 0, false},
		{1.9, 0, false},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		{math.Inf(-1), 0, false},
		{1e30, 0, false},
		{1<<40 + 1, 0, false},
		{1 << 62, 0, false},
	} {
		got, ok := asInt(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("asInt(%v) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The text an agent wrote comes out in the order it wrote it. Decoding into
// map[string]any could not promise that: Go randomizes map iteration, so two
// text-bearing keys, or two sibling blocks each holding text, swapped places
// from one parse to the next.
//
// Parsed many times on purpose. One parse of the old code agreed with the
// document order about nine times in ten, so a single assertion would have
// passed nearly always and failed in someone else's run instead.
func TestTextKeepsTheOrderTheAgentWroteIt(t *testing.T) {
	for _, c := range []struct{ name, line, text, thinking string }{
		{"two text keys in one record", `{"text":"FIRST","content":"SECOND"}`,
			"FIRST\nSECOND", ""},
		{"reversed", `{"content":"FIRST","text":"SECOND"}`, "FIRST\nSECOND", ""},
		{"two sibling records", `{"a":{"text":"FIRST"},"b":{"text":"SECOND"}}`,
			"FIRST\nSECOND", ""},
		{"reasoning beside output", `{"thinking":"THINK","text":"SAY"}`, "SAY", "THINK"},
		// Repeating a key is malformed, and encoding/json would have kept only
		// the last value. Both are text the agent emitted, so both are kept.
		{"a repeated key keeps both", `{"text":"ONE","text":"TWO"}`, "ONE\nTWO", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			for range 200 {
				ev, ok := Parse([]byte(c.line))
				if !ok {
					t.Fatalf("%s should parse as JSON", c.line)
				}
				if ev.Text != c.text || ev.Thinking != c.thinking {
					t.Fatalf("out of order: text=%q thinking=%q, want text=%q thinking=%q",
						ev.Text, ev.Thinking, c.text, c.thinking)
				}
			}
		})
	}
}

// A line is JSON or it is not, and that answer decides whether the caller
// shows it as text. Depth is not part of it: an agent that embeds a deeply
// nested tool result still emitted one JSON line, and the decoder stops
// building past its own bound without changing that answer. Trailing bytes
// are the case that really is not one document.
func TestDecoderAcceptsDepthAndRejectsTrailingBytes(t *testing.T) {
	deep := `{"tool":` + strings.Repeat("[", 300) + strings.Repeat("]", 300) + `,"text":"visible"}`
	ev, ok := Parse([]byte(deep))
	if !ok {
		t.Fatal("a deeply nested payload stopped the line from reading as JSON")
	}
	if ev.Text != "visible" {
		t.Fatalf("text beside the deep payload was lost: %q", ev.Text)
	}
	for _, line := range []string{`{"text":"x"} trailing`, `{"text":"x"}{"text":"y"}`} {
		if _, ok := Parse([]byte(line)); ok {
			t.Errorf("%q is not one JSON document and should not read as one", line)
		}
	}
}

// BenchmarkParse measures the per-line cost every stream-mode output line and
// every transcript record pays.
func BenchmarkParse(b *testing.B) {
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"text","text":"Fixed the leak in pool.go by bounding the ring buffer."}],
		"usage":{"input_tokens":900,"output_tokens":120,
		"output_tokens_details":{"thinking_tokens":40}}}}`)
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := Parse(line); !ok {
			b.Fatal("valid JSON was rejected")
		}
	}
}

// FuzzParse feeds the parser arbitrary agent output and pins the invariants
// every caller relies on: a rejected line contributes nothing, no counter is
// ever negative or platform-dependent, parsing is deterministic, and anything
// encoding/json accepts as an object is accepted as an event.
func FuzzParse(f *testing.F) {
	seeds := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":2}}}`,
		`{"choices":[{"delta":{"content":"partial"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
		`{"content":{"parts":[{"text":"done"}]},"usageMetadata":{"candidatesTokenCount":3}}`,
		`{"type":"tool_result","content":` + strings.Repeat(`{"a":`, 40) + `"deep"` + strings.Repeat(`}`, 40) + `}`,
		`{"usage":{"output_tokens":1e30,"reasoning_tokens":0.5}}`,
		`{"text":"\u0000\u001b[32m","cwd":"/tmp/x","type":"t"}`,
		"{\"broken\": ", ``, `[1,2,3]`, `{"a":`, "null", "42",
		`{"type":"x","message":{"message":{"message":{"text":"nested"}}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		ev, ok := Parse(line)
		if !ok && ev != (Event{}) {
			t.Fatalf("rejected line contributed content: %q -> %+v", line, ev)
		}
		if ev.Usage.Output < 0 || ev.Usage.Thinking < 0 || ev.Usage.Total < 0 {
			t.Fatalf("negative counter: %q -> %+v", line, ev.Usage)
		}
		if ev2, ok2 := Parse(line); ok2 != ok || ev2 != ev {
			t.Fatalf("Parse is not deterministic for %q", line)
		}
		if ok {
			return
		}
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "{") {
			var doc any
			if json.Unmarshal([]byte(trimmed), &doc) == nil {
				t.Fatalf("rejected a JSON object encoding/json accepts: %q", trimmed)
			}
		}
	})
}
