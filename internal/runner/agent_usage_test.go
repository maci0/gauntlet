// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
)

// This file answers one question per supported agent: can gauntlet compute a
// tokens-per-second figure for it, end to end, through the real runner?
//
// Each case drives the actual pipeline (spawn, parse, publish) with a fake
// binary that emits exactly what the real agent emits. The shapes are not
// invented: they were read from the agents' own transcripts, their `--help`,
// their packages, or `strings` on their binaries, and the same fixtures appear
// in internal/streamjson and in toktop's agentusage.
//
// Transcript cases need the optional reader, so they run only under
// `-tags toktop`; the stream cases run in every build.
//
// A rate needs two things: a growing count and the time between the readings.
// So every case asserts at least two usage events with increasing totals, and
// that the last one matches what the agent reported.

// needTranscripts skips a case that only a build with transcript reading can
// satisfy. Without it those agents report nothing, which is the intended
// behavior, not a failure.
func needTranscripts(t *testing.T) {
	t.Helper()
	if transcriptSource == "" {
		t.Skip("transcript reading is off in this build; use -tags toktop")
	}
}

// usageCase is one agent's route to live token counts.
type usageCase struct {
	name string
	// tool is the agent name gauntlet knows it by.
	tool string
	// stream launches the agent in its machine-readable mode, which is how
	// the counts reach stdout, and for dsh also what makes it write a
	// readable session log.
	stream bool
	// transcript says the counts come from a session file rather than
	// stdout, so the case needs a build with transcript reading.
	transcript bool
	// script is the fake agent: it prints the stream, or writes the transcript.
	script string
	// wantFinal is the last cumulative output-token count expected.
	wantFinal int
	// wantThinking is the reasoning share, 0 when the agent reports none.
	wantThinking int
}

func TestEveryAgentYieldsATokenRate(t *testing.T) {
	cases := []usageCase{
		// Anthropic-shaped stream: claude, and the CLIs that copy its
		// envelope (kimi, cursor-agent).
		{
			name: "claude stream-json", tool: "claude", stream: true, wantFinal: 460, wantThinking: 90,
			script: `
echo '{"type":"system","subtype":"init"}'
echo '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"checking callers"}],"usage":{"input_tokens":900,"output_tokens":120,"output_tokens_details":{"thinking_tokens":90}}}}'
sleep 0.3
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"fixed"}],"usage":{"input_tokens":900,"output_tokens":460,"output_tokens_details":{"thinking_tokens":90}}}}'
echo "RESULT: no-changes"`,
		},
		{
			name: "cursor-agent stream-json", tool: "cursor-agent", stream: true, wantFinal: 410,
			script: `
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"looking"}],"usage":{"input_tokens":9000,"output_tokens":120}}}'
sleep 0.3
echo '{"type":"result","subtype":"success","usage":{"input_tokens":9000,"output_tokens":410}}'
echo "RESULT: no-changes"`,
		},
		{
			name: "kimi stream-json", tool: "kimi", stream: true, wantFinal: 700,
			script: `
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"a"}],"usage":{"output_tokens":300}}}'
sleep 0.3
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"b"}],"usage":{"output_tokens":700}}}'
echo "RESULT: no-changes"`,
		},
		// Gemini-shaped stream: gemini and qwen.
		{
			name: "gemini stream-json", tool: "gemini", stream: true, wantFinal: 210, wantThinking: 60,
			script: `
echo '{"type":"assistant","content":{"parts":[{"text":"scanning"}]},"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":80,"thoughtsTokenCount":20,"totalTokenCount":150}}'
sleep 0.3
echo '{"type":"assistant","content":{"parts":[{"text":"done"}]},"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":210,"thoughtsTokenCount":60,"totalTokenCount":320}}'
echo "RESULT: no-changes"`,
		},
		// grok's own event envelope, read out of its binary.
		{
			name: "grok streaming-messages-json", tool: "grok", stream: true, wantFinal: 240, wantThinking: 55,
			script: `
echo '{"type":"stream_event","event":{"type":"reasoning_delta","text":"weighing"},"usage":{"input_tokens":4096,"output_tokens":90,"reasoning_tokens":30}}'
sleep 0.3
echo '{"type":"stream_event","event":{"type":"text_delta","text":"patched"},"usage":{"input_tokens":4096,"output_tokens":240,"reasoning_tokens":55}}'
echo "RESULT: no-changes"`,
		},
		// Transcript agents: the counts never reach stdout at all.
		{
			name: "claude transcript", transcript: true, tool: "claude", wantFinal: 350, wantThinking: 140,
			script: `
d="$HOME/.claude/projects/proj"; mkdir -p "$d"
printf '{"type":"assistant","cwd":"%s","message":{"usage":{"input_tokens":5,"output_tokens":100,"output_tokens_details":{"thinking_tokens":40}}}}\n' "$PWD" >> "$d/s.jsonl"
sleep 0.4
printf '{"type":"assistant","cwd":"%s","message":{"usage":{"input_tokens":5,"output_tokens":250,"output_tokens_details":{"thinking_tokens":100}}}}\n' "$PWD" >> "$d/s.jsonl"
echo "RESULT: no-changes"`,
		},
		{
			name: "codex rollout", transcript: true, tool: "codex", wantFinal: 1300, wantThinking: 400,
			script: `
d="$HOME/.codex/sessions/2026/08/25"; mkdir -p "$d"; f="$d/rollout-x.jsonl"
printf '{"type":"session_meta","payload":{"id":"x","cwd":"%s"}}\n' "$PWD" >> "$f"
printf '{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":400,"reasoning_output_tokens":100,"total_tokens":5000}}}}\n' >> "$f"
sleep 0.4
printf '{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":1300,"reasoning_output_tokens":400,"total_tokens":9000}}}}\n' >> "$f"
echo "RESULT: no-changes"`,
		},
		{
			name: "qwen chat transcript", transcript: true, tool: "qwen", wantFinal: 305, wantThinking: 87,
			script: `
d="$HOME/.qwen/projects/p/chats"; mkdir -p "$d"; f="$d/c.jsonl"
printf '{"type":"assistant","cwd":"%s","usageMetadata":{"candidatesTokenCount":205,"thoughtsTokenCount":82,"totalTokenCount":387}}\n' "$PWD" >> "$f"
sleep 0.4
printf '{"type":"assistant","cwd":"%s","usageMetadata":{"candidatesTokenCount":13,"thoughtsTokenCount":5,"totalTokenCount":400}}\n' "$PWD" >> "$f"
echo "RESULT: no-changes"`,
		},
		{
			name: "dsh session log", tool: "dsh", stream: true, transcript: true, wantFinal: 260, wantThinking: 90,
			script: `
d="$HOME/.dsh/sessions/--proj--/sess"; mkdir -p "$d"; f="$d/session.jsonl"
printf '{"type":"session","version":1,"id":"s","cwd":"%s"}\n' "$PWD" >> "$f"
printf '{"type":"assistant-message","usage":{"prompt_tokens":1200,"completion_tokens":110,"reasoning_tokens":40}}\n' >> "$f"
sleep 0.4
printf '{"type":"assistant-message","usage":{"prompt_tokens":1200,"completion_tokens":150,"reasoning_tokens":50}}\n' >> "$f"
echo "RESULT: no-changes"`,
		},
		{
			name: "clanker token log", transcript: true, tool: "clanker", wantFinal: 2000,
			script: `
mkdir -p state
printf '{"ts":1,"provider":"p","model":"m","prompt_tokens":900,"completion_tokens":1663,"total_tokens":2563,"ok":true}\n' >> state/token_stats.jsonl
sleep 0.4
printf '{"ts":2,"provider":"p","model":"m","prompt_tokens":10,"completion_tokens":337,"total_tokens":347,"ok":true}\n' >> state/token_stats.jsonl
echo "RESULT: no-changes"`,
		},
		// The pi family, which ships as definitions rather than code.
		{
			name: "pi session", transcript: true, tool: "pi", wantFinal: 480, wantThinking: 130,
			script: `
d="$HOME/.pi/agent/sessions"; mkdir -p "$d"; f="$d/s.jsonl"
printf '{"type":"message","cwd":"%s","message":{"usage":{"output":180,"reasoning":50,"totalTokens":900}}}\n' "$PWD" >> "$f"
sleep 0.4
printf '{"type":"message","cwd":"%s","message":{"usage":{"output":300,"reasoning":80,"totalTokens":1400}}}\n' "$PWD" >> "$f"
echo "RESULT: no-changes"`,
		},
		{
			name: "prime-agent session", transcript: true, tool: "prime-agent", wantFinal: 260,
			script: `
d="$HOME/.prime/agent/sessions"; mkdir -p "$d"; f="$d/s.jsonl"
printf '{"type":"session","cwd":"%s"}\n' "$PWD" >> "$f"
printf '{"type":"message","cwd":"%s","message":{"usage":{"input":100,"output":110,"totalTokens":5360}}}\n' "$PWD" >> "$f"
sleep 0.4
printf '{"type":"message","cwd":"%s","message":{"usage":{"input":100,"output":150,"totalTokens":5569}}}\n' "$PWD" >> "$f"
echo "RESULT: no-changes"`,
		},
		{
			name: "feynman session", transcript: true, tool: "feynman", wantFinal: 220, wantThinking: 70,
			script: `
d="$HOME/.feynman/sessions"; mkdir -p "$d"; f="$d/s.jsonl"
printf '{"type":"message","cwd":"%s","message":{"usage":{"output":100,"reasoning":30,"totalTokens":800}}}\n' "$PWD" >> "$f"
sleep 0.4
printf '{"type":"message","cwd":"%s","message":{"usage":{"output":120,"reasoning":40,"totalTokens":900}}}\n' "$PWD" >> "$f"
echo "RESULT: no-changes"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.transcript {
				needTranscripts(t)
			}
			// A private HOME so the fake agent's transcript is the only one the
			// watcher can see, and the real ~/.claude is never read.
			home := t.TempDir()
			t.Setenv("HOME", home)

			repo := testRepo(t)
			set, _ := promptSet(t, "a-review")
			bin := fakeAgent(t, t.TempDir(), c.tool, c.script)

			cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
			cfg.Agents = []agent.Spec{{Tool: c.tool}}
			cfg.Bin = map[string]string{c.tool: bin}
			cfg.Stream = c.stream
			cfg.Timeout = 30 * time.Second

			bus := NewBus()
			events := bus.Subscribe(512)
			done := make(chan []Event, 1)
			go collect(events, done)

			r, err := New(context.Background(), cfg, bus)
			if err != nil {
				t.Fatal(err)
			}
			r.Run(context.Background())
			bus.Close()

			var usage []Event
			for _, ev := range <-done {
				if ev.Kind == EvUsage {
					usage = append(usage, ev)
				}
			}
			if len(usage) < 2 {
				t.Fatalf("a rate needs at least two readings; got %d: %+v", len(usage), usage)
			}
			for i := 1; i < len(usage); i++ {
				if usage[i].Tokens < usage[i-1].Tokens {
					t.Fatalf("usage went backwards, so a rate would be negative: %+v", usage)
				}
			}
			last := usage[len(usage)-1]
			if last.Tokens != c.wantFinal {
				t.Errorf("final tokens %d, want %d", last.Tokens, c.wantFinal)
			}
			if last.Thinking != c.wantThinking {
				t.Errorf("reasoning tokens %d, want %d", last.Thinking, c.wantThinking)
			}

			// The rate itself: tokens divided by the span between readings.
			span := usage[len(usage)-1].Time.Sub(usage[0].Time)
			if span <= 0 {
				t.Fatalf("readings carry no time span, so no rate is computable")
			}
			if rate := float64(last.Tokens-usage[0].Tokens) / span.Seconds(); rate <= 0 {
				t.Fatalf("computed rate is %.2f tok/s", rate)
			}

			// And the result carries the same total the events did.
			res := r.Stats().Results()
			if len(res) != 1 || res[0].Tokens != c.wantFinal {
				t.Fatalf("result tokens %+v, want %d", res, c.wantFinal)
			}
		})
	}
}

// TestAgentsWithoutASourceReportNothing pins the other half of the contract:
// an agent that says nothing about tokens must produce no rate at all, rather
// than a zero that looks like a measurement.
func TestAgentsWithoutASourceReportNothing(t *testing.T) {
	for _, tool := range []string{"opencode", "agy"} {
		t.Run(tool, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			repo := testRepo(t)
			set, _ := promptSet(t, "a-review")
			bin := fakeAgent(t, t.TempDir(), tool, `echo "working"; echo "RESULT: no-changes"`)

			cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
			cfg.Agents = []agent.Spec{{Tool: tool}}
			cfg.Bin = map[string]string{tool: bin}

			bus := NewBus()
			events := bus.Subscribe(256)
			done := make(chan []Event, 1)
			go collect(events, done)

			r, err := New(context.Background(), cfg, bus)
			if err != nil {
				t.Fatal(err)
			}
			r.Run(context.Background())
			bus.Close()

			for _, ev := range <-done {
				if ev.Kind == EvUsage {
					t.Fatalf("invented usage for a silent agent: %+v", ev)
				}
			}
			if got := r.Stats().Results()[0].Tokens; got != 0 {
				t.Fatalf("tokens %d, want 0", got)
			}
		})
	}
}

// TestTranscriptAgentsAreNotConfusedByOtherProjects guards the attribution
// that makes per-agent rates trustworthy when several reviews run at once.
func TestTranscriptAgentsAreNotConfusedByOtherProjects(t *testing.T) {
	needTranscripts(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	other := t.TempDir()

	// A transcript from a different project, written before the review starts.
	dir := filepath.Join(home, ".claude", "projects", "elsewhere")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","cwd":"` + other + `","message":{"usage":{"output_tokens":99999}}}`
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `
d="$HOME/.claude/projects/mine"; mkdir -p "$d"
printf '{"type":"assistant","cwd":"%s","message":{"usage":{"output_tokens":42}}}\n' "$PWD" >> "$d/s.jsonl"
echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	if got := r.Stats().Results()[0].Tokens; got != 42 {
		t.Fatalf("tokens %d, want 42: another project's usage leaked in", got)
	}
}

// TestStreamAndTranscriptAgreeOnTheHigherNumber covers an agent that reports
// through both routes at once, which claude and the pi family do.
func TestStreamAndTranscriptAgreeOnTheHigherNumber(t *testing.T) {
	needTranscripts(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := testRepo(t)
	set, _ := promptSet(t, "a-review")
	// stdout says 100; the transcript says 500. The larger is the truth,
	// since both are cumulative for this review.
	bin := fakeAgent(t, t.TempDir(), "claude", `
d="$HOME/.claude/projects/p"; mkdir -p "$d"
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"x"}],"usage":{"output_tokens":100}}}'
printf '{"type":"assistant","cwd":"%s","message":{"usage":{"output_tokens":500}}}\n' "$PWD" >> "$d/s.jsonl"
sleep 0.3
echo "RESULT: no-changes"`)

	cfg := baseConfig(t, repo, set, []string{"a-review"}, bin)
	cfg.Stream = true
	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(context.Background())
	bus.Close()

	if got := r.Stats().Results()[0].Tokens; got != 500 {
		t.Fatalf("tokens %d, want 500 (the higher of the two sources)", got)
	}
	if strings.TrimSpace(os.Getenv("HOME")) == "" {
		t.Fatal("HOME was not isolated")
	}
}
