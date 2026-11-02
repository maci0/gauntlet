// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/normalize"
)

func TestACPSession(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s"}}`,
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`,
		`{"jsonrpc":"2.0","id":"p","method":"session/request_permission","params":{"sessionId":"s","options":[{"optionId":"yes","kind":"allow_once"}]}}`,
		`{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn"}}`,
	}, "\n")
	var out bytes.Buffer
	var text strings.Builder
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, func(s string) { text.WriteString(s) }, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" || !strings.Contains(out.String(), `"optionId":"yes"`) || !strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("text %q, requests %s", text.String(), out.String())
	}
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", false, false, nil, nil, nil, nil); err != nil || !strings.Contains(out.String(), `"outcome":"cancelled"`) {
		t.Fatalf("denied permission: %v, %s", err, out.String())
	}
	if err := acpSession(strings.NewReader(`{"id":1,"result":{"protocolVersion":2}}`), &out, "/work", "review", "", "", "", "", false, false, nil, nil, nil, nil); err == nil {
		t.Fatal("accepted unsupported version")
	}
}

func TestACPDoesNotMixOpenCodeChildTextIntoParent(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s"}}`,
		`{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","_meta":{"opencode/child-session":{"id":"child"}},"content":{"type":"text","text":"child output"}}}}`,
		`{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","title":"child: Read file","_meta":{"opencode/child-session":{"id":"child"}}}}}`,
		`{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"parent output"}}}}`,
		`{"id":5,"result":{"stopReason":"end_turn"}}`,
	}, "\n")
	var requests bytes.Buffer
	var text, progress strings.Builder
	err := acpSession(strings.NewReader(input), &requests, "/work", "review", "", "", "", "", true, false, func(s string) { text.WriteString(s) }, func(s string) { progress.WriteString(s) }, nil, nil)
	if err != nil || text.String() != "parent output" || progress.String() != "child: Read file" {
		t.Fatalf("text %q, progress %q, error %v", text.String(), progress.String(), err)
	}
}

func TestACPPermissionRequiresAnEstablishedSession(t *testing.T) {
	input := strings.Join([]string{
		`{"id":"early","method":"session/request_permission","params":{"sessionId":"","options":[{"optionId":"yes","kind":"allow_once"}]}}`,
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s"}}`,
		`{"id":"wrong","method":"session/request_permission","params":{"sessionId":"other","options":[{"optionId":"yes","kind":"allow_once"}]}}`,
		`{"id":"right","method":"session/request_permission","params":{"sessionId":"s","options":[{"optionId":"yes","kind":"allow_once"}]}}`,
		`{"id":5,"result":{"stopReason":"end_turn"}}`,
	}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"id":"early","jsonrpc":"2.0","result":{"outcome":{"outcome":"cancelled"}}`,
		`"id":"wrong","jsonrpc":"2.0","result":{"outcome":{"outcome":"cancelled"}}`,
		`"id":"right","jsonrpc":"2.0","result":{"outcome":{"optionId":"yes","outcome":"selected"}}`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s in %s", want, out.String())
		}
	}
}

func TestACPProcess(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "agent", `
read -r init
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","title":"Read \u001b[31mfile"}}}'
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"usage_update","used":999999,"size":1000000}}}'
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"SUBJECT: fix it\n"}}}}'
printf '%s\n' '{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn","usage":{"inputTokens":100,"outputTokens":40,"thoughtTokens":7,"totalTokens":171}}}'
while read -r line; do :; done
`)
	prompt := "review"
	var lines []normalize.Line
	var liveLimitCalled bool
	r := runProc(context.Background(), procOpts{Argv: []string{bin}, Dir: t.TempDir(), NoSandbox: true, ACPPrompt: &prompt, Timeout: 5 * time.Second, Sink: func(l normalize.Line) { lines = append(lines, l) }, Envelope: func(agent.Usage) { liveLimitCalled = true }})
	if r.ExitCode != 0 || r.StreamErr != nil || r.TimedOut || r.Subject != "fix it" || len(lines) == 0 || r.Usage.Output != 40 || r.Usage.Thinking != 7 || r.Usage.Total != 171 || liveLimitCalled {
		t.Fatalf("result = %+v, lines = %+v", r, lines)
	}
	tool := false
	for _, line := range lines {
		if line.Kind == normalize.Tool && line.Text == "Read file" {
			tool = true
		}
	}
	if !tool {
		t.Fatalf("sanitized tool progress missing: %+v", lines)
	}
}

func TestCursorACPReceivesPinnedModel(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "cursor-agent", `
if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
[ "$1" = --model ] && [ "$2" = 'model[fast=false]' ] && [ "$3" = acp ] || exit 3
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	o := withACP(context.Background(), procOpts{Argv: []string{"cursor-agent", "--model", "model[fast=false]", "-p", "P"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "cursor-agent", Model: "model[fast=false]"}, "P", false, "")
	r := runProc(context.Background(), o)
	if r.ExitCode != 0 || r.StreamErr != nil || r.Err != nil {
		t.Fatalf("Cursor ACP = %+v", r)
	}
}

func TestACPUnsupportedConfigFallsBackBeforePrompt(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = acp ]; then
  if [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
  read -r session
  printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
  read -r config
  printf '%s\n' '{"id":3,"error":{"code":-32601,"message":"unsupported"}}'
  while read -r line; do exit 42; done
  sleep 30
else
  echo 'RESULT: changed=0'
fi
`)
	o := withACP(context.Background(), procOpts{Argv: []string{bin, "run", "--standalone", "prompt"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "opencode", Model: "provider/model"}, "prompt", false, "")
	out, result := captureProc(context.Background(), o)
	if result.ExitCode != 0 || result.StreamErr != nil || !strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("fallback = %+v, output = %q", result, out)
	}
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":5,"error":{"code":-32601,"message":"unsupported"}}`
	var requests bytes.Buffer
	err := acpSession(strings.NewReader(input), &requests, "/work", "prompt", "", "", "", "", true, false, nil, nil, nil, nil)
	if err == nil || errors.Is(err, errACPConfigUnsupported) {
		t.Fatalf("prompt error was eligible for fallback: %v", err)
	}
}

func TestACPUnsupportedProtocolFallsBackOnlyForNewSessions(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		fallback       bool
	}{
		{"version", `{"id":1,"result":{"protocolVersion":2}}`, true},
		{"initialize missing", `{"id":1,"error":{"code":-32601,"message":"unsupported"}}`, true},
		{"malformed", `{"id":1,"result":{"protocolVersion":"invalid"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = acp ]; then
  if [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  read -r init
  printf '%s\n' '`+tc.response+`'
  while read -r line; do exit 42; done
else
  echo 'RESULT: changed=0'
fi
`)
			for _, continuing := range []bool{false, true} {
				resume := ""
				if continuing {
					resume = "existing-session"
				}
				o := withACP(context.Background(), procOpts{Argv: []string{bin, "run", "--standalone", "prompt"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "opencode"}, "prompt", continuing, resume)
				out, result := captureProc(context.Background(), o)
				if tc.fallback && !continuing {
					if result.ExitCode != 0 || result.StreamErr != nil || !strings.Contains(out, "RESULT: changed=0") {
						t.Fatalf("fallback = %+v, output = %q", result, out)
					}
				} else if result.StreamErr == nil || strings.Contains(out, "RESULT: changed=0") {
					t.Fatalf("unexpected fallback (continuing=%v): %+v, output = %q", continuing, result, out)
				}
			}
		})
	}
}

func TestKimiCodeACPFallsBackBeforePrompt(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "kimi", `
if [ "$1" = acp ]; then
  if [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"Kimi Code CLI","version":"2.1.1"}}}'
  while read -r line; do exit 42; done
else
  echo 'RESULT: changed=0'
fi
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, continuing := range []bool{false, true} {
		resume := ""
		if continuing {
			resume = "s"
		}
		o := withACP(context.Background(), procOpts{Tool: "kimi", Argv: []string{"kimi", "-p", "prompt"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "kimi"}, "prompt", continuing, resume)
		out, result := captureProc(context.Background(), o)
		if !continuing && (result.ExitCode != 0 || result.StreamErr != nil || !strings.Contains(out, "RESULT: changed=0")) {
			t.Fatalf("new session fallback = %+v, output %q", result, out)
		}
		if continuing && (result.StreamErr == nil || strings.Contains(out, "RESULT: changed=0")) {
			t.Fatalf("continued session switched protocols: %+v, output %q", result, out)
		}
	}
}

func TestKimiACPRecognizesVerifiedPythonServers(t *testing.T) {
	for _, tc := range []struct {
		version, capabilities string
		allowed               bool
	}{
		{"1.49.0", `"http":true`, true},
		{"1.50.0", `"http":true,"sse":false`, true},
		{"1.51.0", `"http":true,"sse":false`, true},
		{"1.51.0", `"http":true`, true},
		{"1.51.0", `"http":true,"sse":true`, false},
		{"1.51.0", `"sse":false`, false},
		{"2.1.1", `"http":true,"sse":true`, false},
		{"unknown", `"http":true,"sse":false`, false},
	} {
		input := `{"id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"Kimi Code CLI","version":"` + tc.version + `"},"agentCapabilities":{"mcpCapabilities":{` + tc.capabilities + `}}}}` + "\n" +
			`{"id":2,"result":{"sessionId":"s"}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn"}}`
		var out bytes.Buffer
		err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil, true)
		if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, errACPAgentUnsupported) {
			t.Errorf("version %s, capabilities %s: error = %v", tc.version, tc.capabilities, err)
		}
	}
}

func TestOpenCodeACPReviewCountsFinalUsage(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = run ] && [ "$2" = --help ]; then
  echo 'provider/model#variant'
  exit 0
fi
if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
[ "$1" = acp ] || exit 1
read -r init
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"RESULT: changed=0\n"}}}}'
printf '%s\n' '{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn","usage":{"inputTokens":100,"outputTokens":40,"thoughtTokens":7,"totalTokens":147}}}'
while read -r line; do :; done
`)
	cfg := baseConfig(t, dir, set, []string{"a-review", "b-review"}, bin)
	cfg.Agents = []agent.Spec{{Tool: "opencode"}}
	cfg.Bin = map[string]string{"opencode": bin}
	cfg.NoSandbox, cfg.TokenBudget = true, 45

	r, events := runRecorded(t, cfg)
	if n := countKind(events, EvReviewStart); n != 1 {
		t.Fatalf("started %d reviews, want one; counts = %+v, tokens = %d, events = %+v", n, r.Stats().Counts(), r.Stats().Tokens(), events)
	}
	if c := r.Stats().Counts(); c.OK != 1 || c.Failures() != 0 {
		t.Fatalf("counts = %+v, want one successful review", c)
	}
	if got := r.Stats().Tokens(); got != 47 {
		t.Fatalf("run tally = %d, want 47 output and reasoning tokens", got)
	}
	if !sawLog(events, "Token budget exhausted") {
		t.Fatal("final ACP usage did not stop the next review")
	}
}

func TestOpenCodeACPFallbackContinuesNativeSession(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	logPath := filepath.Join(t.TempDir(), "launches")
	t.Setenv("GAUNTLET_TEST_ACP_LOG", logPath)
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = run ] && [ "$2" = --help ]; then
  echo 'provider/model#variant'
elif [ "$1" = acp ] && [ "$2" = --help ]; then
  echo 'Agent Client Protocol'
elif [ "$1" = acp ]; then
  echo acp >> "$GAUNTLET_TEST_ACP_LOG"
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
  read -r session
  printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
  read -r config
  printf '%s\n' '{"id":3,"error":{"code":-32601,"message":"unsupported"}}'
  while read -r line; do exit 42; done
else
  continued=no
  for arg in "$@"; do [ "$arg" = -c ] && continued=yes; done
  echo "run continued=$continued" >> "$GAUNTLET_TEST_ACP_LOG"
  echo 'RESULT: changed=0'
fi
`)
	cfg := baseConfig(t, dir, set, []string{"a-review", "b-review"}, bin)
	cfg.Agents = []agent.Spec{{Tool: "opencode", Model: "provider/model"}}
	cfg.Bin = map[string]string{"opencode": bin}
	cfg.NoSandbox, cfg.ContinueSessions = true, true
	cfg.ResumeQueue = []string{"a-review", "b-review"}
	r := runQuiet(t, cfg)
	if counts := r.Stats().Counts(); counts.OK != 2 || counts.Failures() != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "acp\nrun continued=no\nrun continued=yes\n" {
		t.Fatalf("launches = %q", got)
	}
}

func TestOpenCodeACPContinuesSessionAndCountsEachTurn(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	logPath := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("GAUNTLET_TEST_ACP_LOG", logPath)
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = run ] && [ "$2" = --help ]; then echo 'provider/model#variant'; exit 0; fi
if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
[ "$1" = acp ] || exit 1
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}}}}'
read -r session
printf '%s\n' "$session" >> "$GAUNTLET_TEST_ACP_LOG"
case "$session" in
  *session/resume*) printf '%s\n' '{"id":2,"result":{"configOptions":[]}}' ;;
  *) printf '%s\n' '{"id":2,"result":{"sessionId":"s","configOptions":[]}}' ;;
esac
read -r prompt
printf '%s\n' '{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"RESULT: changed=0\n"}}}}'
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn","usage":{"inputTokens":100,"outputTokens":40,"thoughtTokens":7,"totalTokens":147}}}'
while read -r line; do :; done
`)
	cfg := baseConfig(t, dir, set, []string{"a-review", "b-review"}, bin)
	cfg.Agents = []agent.Spec{{Tool: "opencode"}}
	cfg.Bin = map[string]string{"opencode": bin}
	cfg.NoSandbox, cfg.ContinueSessions = true, true
	cfg.ResumeQueue = []string{"a-review", "b-review"}
	r := runQuiet(t, cfg)
	if counts := r.Stats().Counts(); counts.OK != 2 || counts.Failures() != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	if tokens := r.Stats().Tokens(); tokens != 94 {
		t.Fatalf("tokens = %d, want 47 for each turn", tokens)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"method":"session/new"`) || !strings.Contains(lines[1], `"method":"session/resume"`) || !strings.Contains(lines[1], `"sessionId":"s"`) {
		t.Fatalf("session requests = %q", got)
	}
}

func TestACPContinuationDoesNotSwitchToNativeWhenProbeFails(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "native-ran")
	t.Setenv("ACP_NATIVE_MARKER", marker)
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = acp ]; then exit 1; fi
echo ran > "$ACP_NATIVE_MARKER"
`)
	base := procOpts{Tool: "opencode", Argv: []string{bin, "run", "--standalone", "-c", "P"}, Dir: dir, NoSandbox: true, Timeout: 5 * time.Second}
	selected := withACP(context.Background(), base, agent.Spec{Tool: "opencode"}, "P", true, "existing")
	result := runProc(context.Background(), selected)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "ACP") {
		t.Fatalf("continuation failure = %+v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("native continuation was launched: %v", err)
	}
	if selected := withACP(context.Background(), base, agent.Spec{Tool: "opencode"}, "P", true, ""); selected.ACPPrompt != nil {
		t.Fatalf("native session could not continue: %+v", selected)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := runProc(ctx, withACP(ctx, base, agent.Spec{Tool: "opencode"}, "P", true, "existing")); !got.Canceled {
		t.Fatalf("cancelled continuation = %+v", got)
	}
}

func TestACPProcessTimeout(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "agent", "sleep 30")
	prompt := "review"
	r := runProc(context.Background(), procOpts{Argv: []string{bin}, Dir: t.TempDir(), NoSandbox: true, ACPPrompt: &prompt, Timeout: 50 * time.Millisecond})
	if !r.TimedOut {
		t.Fatalf("result = %+v, want timeout", r)
	}
}

func TestACPProcessStopsServerAfterResponse(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		wantError    bool
	}{
		{name: "complete", script: `
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
sleep 30`},
		{name: "failed", wantError: true, script: `
read -r init
printf '%s\n' '{"id":1,"error":{"code":-32000,"message":"authentication required"}}'
sleep 30`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := fakeAgent(t, t.TempDir(), "agent", tc.script)
			prompt := "review"
			start := time.Now()
			r := runProc(context.Background(), procOpts{Argv: []string{bin}, Dir: t.TempDir(), NoSandbox: true, ACPPrompt: &prompt, Timeout: 10 * time.Second})
			if r.TimedOut || (r.StreamErr != nil) != tc.wantError || (!tc.wantError && r.ExitCode != 0) || time.Since(start) > 5*time.Second {
				t.Fatalf("result = %+v, elapsed = %s", r, time.Since(start))
			}
		})
	}
}

func TestACPCompletedPromptDoesNotTimeoutDuringShutdown(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "agent", `
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
sleep 30`)
	prompt := "review"
	r := runProc(context.Background(), procOpts{Argv: []string{bin}, Dir: t.TempDir(), NoSandbox: true, ACPPrompt: &prompt, Timeout: 500 * time.Millisecond})
	if r.TimedOut || r.StreamErr != nil || r.ExitCode != 0 {
		t.Fatalf("completed prompt reported as failure: %+v", r)
	}
}

func TestACPModelAndEffort(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s"}}`,
		`{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"provider/model"}]}}`,
		`{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"high"}]}}`,
		`{"id":5,"result":{"stopReason":"end_turn"}}`,
	}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "provider/model", "high", "", "", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"configId":"model"`) || !strings.Contains(out.String(), `"configId":"effort"`) {
		t.Fatalf("missing config requests: %s", out.String())
	}
	wrong := strings.Replace(input, `"currentValue":"high"`, `"currentValue":"low"`, 1)
	if err := acpSession(strings.NewReader(wrong), &out, "/work", "review", "provider/model", "high", "", "", true, false, nil, nil, nil, nil); err == nil {
		t.Fatal("accepted a different effort")
	}
}

func TestACPConfigDoesNotChangeEarlierSelection(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s"}}`,
		`{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"provider/model"}]}}`,
		`{"id":4,"result":{"configOptions":[{"id":"model","currentValue":"other/model"},{"id":"effort","currentValue":"high"}]}}`,
	}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "provider/model", "high", "", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("accepted changed model: %v, %s", err, out.String())
	}
	input = strings.Join([]string{
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s"}}`,
		`{"id":6,"result":{"configOptions":[{"id":"mode","currentValue":"full"}]}}`,
		`{"id":3,"result":{"configOptions":[{"id":"mode","currentValue":"restricted"},{"id":"model","currentValue":"provider/model"}]}}`,
	}, "\n")
	out.Reset()
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "provider/model", "", "", "full", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("accepted changed mode: %v, %s", err, out.String())
	}
}

func TestACPConfigRejectsEarlierSelectionAfterMatchedOption(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, mode, response string
	}{
		{"model", "provider/model", "high", "", `{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"provider/model"}]}}
{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"high"},{"id":"model","currentValue":"other/model"}]}}`},
		{"mode", "provider/model", "", "full", `{"id":6,"result":{"configOptions":[{"id":"mode","currentValue":"full"}]}}
{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"provider/model"},{"id":"mode","currentValue":"restricted"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}` + "\n" + tc.response
			var out bytes.Buffer
			err := acpSession(strings.NewReader(input), &out, "/work", "review", tc.model, tc.effort, "", tc.mode, true, false, nil, nil, nil, nil)
			if err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
				t.Fatalf("accepted changed %s: %v, %s", tc.name, err, out.String())
			}
		})
	}
}

func TestACPConfigUpdateKeepsConfirmedSelections(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}`
	mode := `{"id":6,"result":{"configOptions":[{"id":"mode","currentValue":"full"}]}}`
	model := `{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"pinned"},{"id":"mode","currentValue":"full"}]}}`
	effort := `{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"high"},{"id":"model","currentValue":"pinned"},{"id":"mode","currentValue":"full"}]}}`
	for _, tc := range []struct{ name, prefix, update string }{
		{"mode", mode, `{"id":"mode","currentValue":"restricted"}`},
		{"model", mode + "\n" + model, `{"id":"model","currentValue":"other"}`},
		{"effort", mode + "\n" + model + "\n" + effort, `{"id":"effort","currentValue":"low"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			update := `{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"config_option_update","configOptions":[` + tc.update + `]}}}`
			input := strings.Join([]string{init, tc.prefix, update, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
			var out bytes.Buffer
			err := acpSession(strings.NewReader(input), &out, "/work", "review", "pinned", "high", "", "full", true, false, nil, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("accepted changed %s: %v", tc.name, err)
			}
		})
	}
	update := `{"method":"session/update","params":{"sessionId":"another","update":{"sessionUpdate":"config_option_update","configOptions":[{"id":"model","currentValue":"other"}]}}}`
	input := strings.Join([]string{init, mode, model, effort, update, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "pinned", "high", "", "full", true, false, nil, nil, nil, nil); err != nil {
		t.Fatalf("rejected another session's update: %v", err)
	}
	for _, tc := range []struct{ name, update string }{
		{"model", `{"sessionUpdate":"current_model_update","currentModelId":"other"}`},
		{"mode", `{"sessionUpdate":"current_mode_update","currentModeId":"restricted"}`},
	} {
		t.Run("legacy "+tc.name, func(t *testing.T) {
			update := `{"method":"session/update","params":{"sessionId":"s","update":` + tc.update + `}}`
			input := strings.Join([]string{init, mode, model, effort, update, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
			var out bytes.Buffer
			if err := acpSession(strings.NewReader(input), &out, "/work", "review", "pinned", "high", "", "full", true, false, nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("accepted changed %s: %v", tc.name, err)
			}
		})
	}
}

func TestACPProviderModel(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"[\"provider\",\"model\"]"}]}}` + "\n" +
		`{"id":5,"result":{"stopReason":"end_turn"}}`
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", `["provider","model"]`, "", "", "", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"value":"[\"provider\",\"model\"]"`) {
		t.Fatal(out.String())
	}
}

func TestACPDshRejectsUnknownModelBeforePrompt(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":3,"error":{"code":-32602,"message":"unknown model option"}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", `["deepseek-official","deepseek-flash"]`, "", "", "", true, false, nil, nil, nil, nil)
	if err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("unknown dsh model = %v, requests = %s", err, out.String())
	}
}

func TestACPDshFallsBackForUnknownModel(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "dsh", `
if [ "$1" = --profile ] && [ "$2" = acp ]; then
  if [ "$3" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}}}}'
  read -r session
  printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
  read -r config
  printf '%s\n' '{"id":3,"error":{"code":-32602,"message":"unknown model option"}}'
  while read -r line; do exit 42; done
  sleep 30
else
  [ "$1" = --profile ] && [ "$2" = headless ] && [ "$3" = --patch ] && [ "$4" = expected-patch ] && [ "$5" = review ] || exit 43
  echo 'RESULT: changed=0'
fi
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Tool: "dsh", Argv: []string{"dsh", "--profile", "headless", "--patch", "expected-patch", "review"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}
	spec := agent.Spec{Tool: "dsh", Model: "provider/model"}
	out, result := captureProc(context.Background(), withACP(context.Background(), base, spec, "review", false, ""))
	if result.Err != nil || result.StreamErr != nil || result.ExitCode != 0 || !strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("headless fallback = %+v, output %q", result, out)
	}
	out, result = captureProc(context.Background(), withACP(context.Background(), base, spec, "review", true, "s"))
	if !errors.Is(result.StreamErr, errACPInvalidModel) || strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("continued session fell back = %+v, output %q", result, out)
	}
}

func TestACPDshTimeoutDoesNotFallBack(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "dsh", `
if [ "$1" = --profile ] && [ "$2" = acp ]; then
  if [ "$3" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  sleep 10
else
  echo 'RESULT: changed=0'
fi
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Tool: "dsh", Argv: []string{"dsh", "--profile", "headless", "review"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 100 * time.Millisecond}
	out, result := captureProc(context.Background(), withACP(context.Background(), base, agent.Spec{Tool: "dsh", Model: "provider/model"}, "review", false, ""))
	if !result.TimedOut || strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("timed-out ACP fell back = %+v, output %q", result, out)
	}
}

func TestACPDshPromptErrorDoesNotFallBack(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "dsh", `
if [ "$1" = --profile ] && [ "$2" = acp ]; then
  if [ "$3" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
  read -r session
  printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
  read -r config
  printf '%s\n' '{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"[\"provider\",\"model\"]"}]}}'
  read -r prompt
  printf '%s\n' '{"id":5,"error":{"code":-32602,"message":"invalid prompt"}}'
else
  echo 'RESULT: changed=0'
fi
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Tool: "dsh", Argv: []string{"dsh", "--profile", "headless", "review"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}
	out, result := captureProc(context.Background(), withACP(context.Background(), base, agent.Spec{Tool: "dsh", Model: "provider/model"}, "review", false, ""))
	if !errors.Is(result.StreamErr, errACPPromptFailed) || strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("prompt error triggered fallback = %+v, output %q", result, out)
	}
}

func TestACPResume(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}}}}` + "\n" +
		`{"id":2,"result":{}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn"}}`
	var out bytes.Buffer
	var session string
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "existing", "", true, false, nil, nil, nil, func(id string) { session = id }); err != nil {
		t.Fatal(err)
	}
	if session != "existing" || !strings.Contains(out.String(), `"method":"session/resume"`) || !strings.Contains(out.String(), `"sessionId":"existing"`) {
		t.Fatalf("session %q, requests %s", session, out.String())
	}
	wrong := strings.Replace(input, `{"id":2,"result":{}}`, `{"id":2,"result":{"sessionId":"other"}}`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(wrong), &out, "/work", "review", "", "", "existing", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("accepted a different resumed session: %v, %s", err, out.String())
	}
	missing := strings.Replace(input, `"sessionCapabilities":{"resume":{}}`, `"sessionCapabilities":{}`, 1)
	if err := acpSession(strings.NewReader(missing), &out, "/work", "review", "", "", "existing", "", true, false, nil, nil, nil, nil); err == nil {
		t.Fatal("resumed without capability")
	}
	load := strings.Replace(input, `"sessionCapabilities":{"resume":{}}`, `"loadSession":true`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(load), &out, "/work", "review", "", "", "existing", "", true, false, nil, nil, nil, nil); err != nil || !strings.Contains(out.String(), `"method":"session/load"`) {
		t.Fatalf("load fallback: %v, %s", err, out.String())
	}
}

func TestACPRequiresResumeBeforeStartingSession(t *testing.T) {
	for _, capability := range []string{"", `,"agentCapabilities":{"sessionCapabilities":{"resume":false}}`} {
		input := `{"id":1,"result":{"protocolVersion":1` + capability + `}}`
		var out bytes.Buffer
		err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, true, nil, nil, nil, nil)
		if !errors.Is(err, errACPResumeUnsupported) || strings.Contains(out.String(), `"method":"session/new"`) {
			t.Fatalf("capability %q: err = %v, requests = %s", capability, err, out.String())
		}
	}
	input := `{"id":1,"result":{"protocolVersion":1,"agentCapabilities":{"sessionCapabilities":{"resume":{}}}}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn"}}`
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, true, nil, nil, nil, nil); err != nil || !strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("resumable agent: err = %v, requests = %s", err, out.String())
	}
}

func TestACPErrorReportsRedactedReason(t *testing.T) {
	input := `{"id":1,"error":{"code":-32000,"message":"Authentication required\nAPI_KEY=secret"}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Authentication required") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestACPProviderErrorReportsSafeDetail(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":5,"error":{"code":-32603,"message":"Internal error","data":{"error":"\u001b[31mquota exceeded\u001b[0m\nAPI_KEY=secret"}}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") || strings.ContainsAny(err.Error(), "\x1b\n") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("provider error = %v", err)
	}
}

func TestACPProviderQuotaErrorSkipsSameAgentRetry(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "quota-review")
	marker := filepath.Join(t.TempDir(), "attempts")
	t.Setenv("GAUNTLET_TEST_ACP_ATTEMPTS", marker)
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = run ] && [ "$2" = --help ]; then echo 'provider/model#variant'; exit 0; fi
if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
echo attempt >> "$GAUNTLET_TEST_ACP_ATTEMPTS"
printf '%s\n' '{"id":5,"error":{"code":-32603,"message":"Internal error","data":{"error":"quota exceeded"}}}'
`)
	cfg := baseConfig(t, dir, set, []string{"quota-review"}, bin)
	cfg.Agents, cfg.Bin, cfg.NoSandbox, cfg.Retries = []agent.Spec{{Tool: "opencode"}}, map[string]string{"opencode": bin}, true, 1
	bus := NewBus()
	done := make(chan []Event, 1)
	go collect(bus.Subscribe(256), done)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.Run(ctx)
	bus.Close()
	events := <-done
	if ctx.Err() != nil {
		t.Fatal("waited to retry a terminal provider error")
	}
	got, err := os.ReadFile(marker)
	if err != nil || strings.Count(string(got), "attempt") != 1 {
		t.Fatalf("attempts %q, error %v", got, err)
	}
	if !sawLog(events, "Not retrying") {
		t.Fatalf("missing retry decision: %+v", events)
	}
}

func TestACPAuthenticationErrorSkipsSameAgentRetry(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "auth-review")
	bin := fakeAgent(t, t.TempDir(), "opencode", `
if [ "$1" = run ] && [ "$2" = --help ]; then echo 'provider/model#variant'; exit 0; fi
if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; exit 0; fi
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"error":{"code":-32000,"message":"Authentication required"}}'
`)
	cfg := baseConfig(t, dir, set, []string{"auth-review"}, bin)
	cfg.Agents, cfg.Bin, cfg.NoSandbox, cfg.Retries = []agent.Spec{{Tool: "opencode"}}, map[string]string{"opencode": bin}, true, 1
	bus := NewBus()
	done := make(chan []Event, 1)
	go collect(bus.Subscribe(256), done)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.Run(ctx)
	bus.Close()
	events := <-done
	if ctx.Err() != nil || !sawLog(events, "Not retrying") {
		t.Fatalf("authentication failure was retried: context %v, events %+v", ctx.Err(), events)
	}
}

func TestACPErrorsSanitizeAgentText(t *testing.T) {
	for _, input := range []string{
		`{"id":1,"error":{"code":-32000,"message":"\u001b[31mfailed\u001b[0m"}}`,
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" +
			`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
			`{"id":5,"result":{"stopReason":"\u001b[31mfailed\u001b[0m\nAPI_KEY=secret"}}`,
	} {
		var out bytes.Buffer
		err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
		if err == nil || strings.ContainsAny(err.Error(), "\x1b\n") || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "failed") {
			t.Fatalf("unsafe ACP error: %v", err)
		}
	}
}

func TestACPCachedAuthentication(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"cached_token"}],"_meta":{"defaultAuthMethodId":"cached_token"}}}`
	needAuth := `{"id":2,"error":{"code":-32000,"message":"authentication required"}}`
	input := strings.Join([]string{init, needAuth, `{"id":7,"result":{}}`, `{"id":2,"result":{"sessionId":"s"}}`, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"method":"authenticate"`) || !strings.Contains(out.String(), `"methodId":"cached_token"`) || strings.Count(out.String(), `"method":"session/new"`) != 2 {
		t.Fatal(out.String())
	}
	otherDefault := strings.Replace(init, `"defaultAuthMethodId":"cached_token"`, `"defaultAuthMethodId":"xai.api_key"`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(otherDefault+"\n"+needAuth), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"authenticate"`) {
		t.Fatalf("unexpected cached authentication: %v, %s", err, out.String())
	}
}

func TestACPGrokAPIKeyAuthentication(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"xai.api_key"},{"id":"grok.com"}],"_meta":{"defaultAuthMethodId":"xai.api_key"}}}`
	needAuth := `{"id":2,"error":{"code":-32000,"message":"Authentication required"}}`
	direct := strings.Join([]string{init, `{"id":2,"result":{"sessionId":"s"}}`, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(direct), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err != nil || strings.Contains(out.String(), `"method":"authenticate"`) {
		t.Fatalf("direct API-key session: %v, %s", err, out.String())
	}
	input := strings.Join([]string{init, needAuth, `{"id":7,"result":{}}`, `{"id":2,"result":{"sessionId":"s"}}`, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	out.Reset()
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err != nil || !strings.Contains(out.String(), `"methodId":"xai.api_key"`) {
		t.Fatalf("API-key authentication: %v, %s", err, out.String())
	}
	out.Reset()
	withoutDefault := strings.Replace(init, `"defaultAuthMethodId":"xai.api_key"`, `"defaultAuthMethodId":"grok.com"`, 1)
	if err := acpSession(strings.NewReader(withoutDefault+"\n"+needAuth), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"authenticate"`) {
		t.Fatalf("unexpected authentication: %v, %s", err, out.String())
	}
}

func TestACPGrokRejectsUnknownModelBeforePrompt(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s","models":{"currentModelId":"grok-4.6"}}}` + "\n" +
		`{"id":3,"error":{"code":-32602,"message":"Unknown model"}}`
	var requests bytes.Buffer
	err := acpSession(strings.NewReader(input), &requests, "/work", "review", "invalid-model", "", "", "", true, false, nil, nil, nil, nil)
	if err == nil || !strings.Contains(requests.String(), `"value":"invalid-model"`) || strings.Contains(requests.String(), `"method":"session/prompt"`) {
		t.Fatalf("unknown model = %v, requests = %s", err, requests.String())
	}
}

func TestACPAuthenticationRejectionIsTerminal(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"xai.api_key"}],"_meta":{"defaultAuthMethodId":"xai.api_key"}}}` + "\n" +
		`{"id":2,"error":{"code":-32000,"message":"Authentication required"}}` + "\n" +
		`{"id":7,"error":{"code":-32000,"message":"Authentication required","data":"invalid key\nAPI_KEY=secret"}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
	if !errors.Is(err, errACPAuthenticationRequired) || !strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("authentication rejection = %v", err)
	}
}

func TestACPAuthenticationErrorIncludesAgentInstruction(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1,"authMethods":[{"id":"cursor_login"}]}}` + "\n" +
		`{"id":2,"error":{"code":-32000,"message":"Authentication required","data":{"message":"Please run agent login first.\nAPI_KEY=secret"}}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
	if !errors.Is(err, errACPAuthenticationRequired) || !strings.Contains(err.Error(), "Please run agent login first") || strings.Contains(err.Error(), "secret") || strings.Contains(out.String(), `"method":"authenticate"`) {
		t.Fatalf("authentication error = %v, requests = %s", err, out.String())
	}
}

func TestACPAuthenticationErrorIncludesStringData(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"error":{"code":-32000,"message":"Authentication required","data":"no auth method id provided\nAPI_KEY=secret"}}`
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil)
	if !errors.Is(err, errACPAuthenticationRequired) || !strings.Contains(err.Error(), "no auth method id provided") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("authentication error = %v", err)
	}
}

func TestACPUsageRejectsInvalidCounters(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":-1,"thoughtTokens":1099511627777,"totalTokens":100}}}`
	var out bytes.Buffer
	var got agent.Usage
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "", true, false, nil, nil, func(u agent.Usage) { got = u }, nil); err != nil {
		t.Fatal(err)
	}
	if got.Output != -1 || got.Thinking != -1 || got.Total != 100 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestOpenCodeACPReasoningOnlyCountsAsOutput(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "opencode", `
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn","usage":{"thoughtTokens":7}}}'
`)
	prompt := "review"
	r := runProc(context.Background(), procOpts{Tool: "opencode", Argv: []string{bin}, Dir: t.TempDir(), NoSandbox: true, ACPPrompt: &prompt, Timeout: 5 * time.Second})
	if r.StreamErr != nil || r.Usage.Output != 7 || r.Usage.Thinking != 7 {
		t.Fatalf("reasoning-only usage = %+v, stream error %v", r.Usage, r.StreamErr)
	}
}

func TestACPAdapterMode(t *testing.T) {
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":6,"result":{"configOptions":[{"id":"mode","currentValue":"agent-full-access"}]}}` + "\n" +
		`{"id":5,"result":{"stopReason":"end_turn"}}`
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"configId":"mode"`) {
		t.Fatal(out.String())
	}
	wrong := strings.Replace(input, `"currentValue":"agent-full-access"`, `"currentValue":"read-only"`, 1)
	if err := acpSession(strings.NewReader(wrong), &out, "/work", "review", "", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err == nil {
		t.Fatal("accepted read-only mode")
	}
	withModel := strings.Replace(input, `{"id":5,`, `{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"pinned"}]}}`+"\n"+`{"id":5,`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(withModel), &out, "/work", "review", "pinned", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"configId":"model"`) {
		t.Fatal(out.String())
	}
	withEffort := strings.Replace(withModel, `{"id":5,`, `{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"high"}]}}`+"\n"+`{"id":5,`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(withEffort), &out, "/work", "review", "pinned", "high", "", "agent-full-access", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"configId":"effort"`) {
		t.Fatal(out.String())
	}
}

func TestACPRejectsConflictingSelectionOptions(t *testing.T) {
	for _, tc := range []struct {
		name, model, effort, mode, id, value string
	}{
		{"mode", "", "", "agent-full-access", "6", "agent-full-access"},
		{"model", "pinned", "", "", "3", "pinned"},
		{"effort", "", "high", "", "4", "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
				`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
				`{"id":` + tc.id + `,"result":{"configOptions":[{"id":"` + tc.name + `","currentValue":"` + tc.value + `"},{"id":"` + tc.name + `","currentValue":"other"}]}}`
			var out bytes.Buffer
			err := acpSession(strings.NewReader(input), &out, "/work", "review", tc.model, tc.effort, "", tc.mode, true, false, nil, nil, nil, nil)
			if err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
				t.Fatalf("accepted conflicting %s: %v, requests %s", tc.name, err, out.String())
			}
		})
	}
}

func TestACPLegacyAdvertisedMode(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1}}`
	session := `{"id":2,"result":{"sessionId":"s","modes":{"currentModeId":"read-only","availableModes":[{"id":"agent-full-access"}]}}}`
	unsupported := `{"id":6,"error":{"code":-32601,"message":"not implemented"}}`
	input := strings.Join([]string{init, session, unsupported, `{"id":8,"result":{}}`, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"method":"session/set_mode"`) || !strings.Contains(out.String(), `"modeId":"agent-full-access"`) || !strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("requests = %s", out.String())
	}
	withModel := strings.Replace(input, `{"id":5,"result":{"stopReason":"end_turn"}}`, `{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"pinned"}]}}`+"\n"+`{"id":5,"result":{"stopReason":"end_turn"}}`, 1)
	out.Reset()
	if err := acpSession(strings.NewReader(withModel), &out, "/work", "review", "pinned", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err != nil || !strings.Contains(out.String(), `"configId":"model"`) {
		t.Fatalf("legacy mode and pinned model: err = %v, requests = %s", err, out.String())
	}
	missing := strings.Replace(session, `"agent-full-access"`, `"other"`, 1)
	out.Reset()
	err := acpSession(strings.NewReader(strings.Join([]string{init, missing, unsupported}, "\n")), &out, "/work", "review", "", "", "", "agent-full-access", true, false, nil, nil, nil, nil)
	if !errors.Is(err, errACPConfigUnsupported) || strings.Contains(out.String(), `"method":"session/set_mode"`) {
		t.Fatalf("unadvertised mode: err = %v, requests = %s", err, out.String())
	}
	out.Reset()
	malformed := strings.Replace(input, `{"id":8,"result":{}}`, `{"id":8,"result":null}`, 1)
	if err := acpSession(strings.NewReader(malformed), &out, "/work", "review", "", "", "", "agent-full-access", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("invalid mode response: err = %v, requests = %s", err, out.String())
	}
}

func TestACPLegacyAdvertisedModel(t *testing.T) {
	init := `{"id":1,"result":{"protocolVersion":1}}`
	session := `{"id":2,"result":{"sessionId":"s","models":{"availableModels":[{"modelId":"pinned"}]}}}`
	unsupported := `{"id":3,"error":{"code":-32601,"message":"not implemented"}}`
	input := strings.Join([]string{init, session, unsupported, `{"id":9,"result":{}}`, `{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"high"}]}}`, `{"id":5,"result":{"stopReason":"end_turn"}}`}, "\n")
	var out bytes.Buffer
	if err := acpSession(strings.NewReader(input), &out, "/work", "review", "pinned", "high", "", "", true, false, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"method":"session/set_model"`) || !strings.Contains(out.String(), `"modelId":"pinned"`) || !strings.Contains(out.String(), `"configId":"effort"`) {
		t.Fatalf("requests = %s", out.String())
	}
	missing := strings.Replace(session, `"pinned"`, `"other"`, 1)
	out.Reset()
	err := acpSession(strings.NewReader(strings.Join([]string{init, missing, unsupported}, "\n")), &out, "/work", "review", "pinned", "", "", "", true, false, nil, nil, nil, nil)
	if !errors.Is(err, errACPConfigUnsupported) || strings.Contains(out.String(), `"method":"session/set_model"`) {
		t.Fatalf("unadvertised model: err = %v, requests = %s", err, out.String())
	}
	out.Reset()
	malformed := strings.Replace(input, `{"id":9,"result":{}}`, `{"id":9,"result":null}`, 1)
	if err := acpSession(strings.NewReader(malformed), &out, "/work", "review", "pinned", "high", "", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("invalid model response: err = %v, requests = %s", err, out.String())
	}
	out.Reset()
	mismatch := strings.Replace(input, `{"id":9,"result":{}}`, `{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"current_model_update","currentModelId":"other"}}}`+"\n"+`{"id":9,"result":{}}`, 1)
	if err := acpSession(strings.NewReader(mismatch), &out, "/work", "review", "pinned", "high", "", "", true, false, nil, nil, nil, nil); err == nil || strings.Contains(out.String(), `"method":"session/prompt"`) {
		t.Fatalf("contradictory model update: err = %v, requests = %s", err, out.String())
	}
}

func TestACPLegacyModeAndModel(t *testing.T) {
	input := strings.Join([]string{
		`{"id":1,"result":{"protocolVersion":1}}`,
		`{"id":2,"result":{"sessionId":"s","modes":{"availableModes":[{"id":"agent-full-access"}]},"models":{"availableModels":[{"modelId":"pinned"}]}}}`,
		`{"id":6,"error":{"code":-32601,"message":"unsupported"}}`,
		`{"id":8,"result":{}}`,
		`{"id":3,"error":{"code":-32601,"message":"unsupported"}}`,
		`{"id":9,"result":{}}`,
		`{"id":5,"result":{"stopReason":"end_turn"}}`,
	}, "\n")
	var out bytes.Buffer
	err := acpSession(strings.NewReader(input), &out, "/work", "review", "pinned", "", "", "agent-full-access", true, false, nil, nil, nil, nil)
	if err != nil || !strings.Contains(out.String(), `"method":"session/set_mode"`) || !strings.Contains(out.String(), `"method":"session/set_model"`) {
		t.Fatalf("legacy selections: err = %v, requests = %s", err, out.String())
	}
}

func TestInstalledACPAdapters(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"codex", "codex-acp", "claude", "claude-agent-acp"} {
		fakeAgent(t, dir, name, "exit 0")
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tt := range []struct{ tool, adapter, mode, env string }{
		{"codex", "codex-acp", "agent-full-access", "CODEX_PATH="},
		{"claude", "claude-agent-acp", "bypassPermissions", "CLAUDE_CODE_EXECUTABLE="},
	} {
		o := withACP(context.Background(), procOpts{Argv: []string{tt.tool, "-p", "P"}}, agent.Spec{Tool: tt.tool}, "P", false, "")
		if len(o.Argv) != 1 || o.Argv[0] != filepath.Join(dir, tt.adapter) || o.ACPMode != tt.mode || len(o.ACPEnv) != 1 || o.ACPEnv[0] != tt.env+filepath.Join(dir, tt.tool) {
			t.Errorf("%s: %+v", tt.tool, o)
		}
		o = withACP(context.Background(), procOpts{Argv: []string{tt.tool, "-p", "P"}}, agent.Spec{Tool: tt.tool, Model: "pinned"}, "P", false, "")
		if o.ACPModel != "pinned" || o.ACPMode != tt.mode {
			t.Errorf("%s pinned: %+v", tt.tool, o)
		}
	}
	o := withACP(context.Background(), procOpts{Argv: []string{"claude", "-p", "P"}}, agent.Spec{Tool: "claude", Effort: "high"}, "P", false, "")
	if o.ACPPrompt == nil || o.ACPEffort != "high" {
		t.Fatalf("Claude effort: %+v", o)
	}
	o = withACP(context.Background(), procOpts{Argv: []string{"claude", "-p", "P"}}, agent.Spec{Tool: "claude", Effort: "xhigh"}, "P", false, "")
	if o.ACPPrompt != nil {
		t.Fatal("selected adapter for xhigh effort")
	}
}

func TestCustomACP(t *testing.T) {
	dir := t.TempDir()
	fakeAgent(t, dir, "custom-server", "exit 0")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	def := agent.Custom{Argv: []string{"custom-cli", "-p", "{prompt}"}, ACP: []string{"custom-server", "--stdio"}, Model: []string{"--model", "{model}"}}
	if err := agent.Register("acp-test-custom", def); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Unregister("acp-test-custom") })
	spec := agent.Spec{Tool: "acp-test-custom", Model: "pinned"}
	o := withACP(context.Background(), procOpts{Argv: []string{"custom-cli", "-p", "P"}, Stream: true}, spec, "P", false, "")
	if strings.Join(o.Argv, " ") != "custom-server --stdio" || o.ACPPrompt == nil || o.ACPModel != "pinned" || o.Stream {
		t.Fatalf("ACP options = %+v", o)
	}
	o = withACP(context.Background(), procOpts{Argv: []string{"/override", "-p", "P"}}, spec, "P", false, "")
	if o.ACPPrompt != nil {
		t.Fatal("ignored the executable override")
	}
}

func TestCustomACPMissingServerUsesNativeOnlyForNewSession(t *testing.T) {
	dir := t.TempDir()
	fakeAgent(t, dir, "custom-cli", `echo 'RESULT: changed=0'`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	def := agent.Custom{Argv: []string{"custom-cli", "-p", "{prompt}"}, ACP: []string{filepath.Join(t.TempDir(), "missing-server")}}
	if err := agent.Register("acp-test-missing", def); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Unregister("acp-test-missing") })
	base := procOpts{Argv: []string{"custom-cli", "-p", "P"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}
	spec := agent.Spec{Tool: "acp-test-missing"}
	got := withACP(context.Background(), base, spec, "P", false, "")
	if got.ACPPrompt != nil {
		t.Fatalf("missing server selected for a new session: %+v", got)
	}
	if output, result := captureProc(context.Background(), got); result.Err != nil || result.ExitCode != 0 || !strings.Contains(output, "RESULT: changed=0") {
		t.Fatalf("native fallback = %+v, output %q", result, output)
	}
	if got := withACP(context.Background(), base, spec, "P", true, "existing"); got.ACPPrompt == nil || got.ACPResume != "existing" {
		t.Fatalf("continued session silently switched protocols: %+v", got)
	}
}

func TestCustomACPRelativeServerUsesReviewDirectory(t *testing.T) {
	dir := t.TempDir()
	fakeAgent(t, dir, "server", "exit 0")
	def := agent.Custom{Argv: []string{"custom-cli", "-p", "{prompt}"}, ACP: []string{"./server"}}
	if err := agent.Register("acp-test-relative", def); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Unregister("acp-test-relative") })
	got := withACP(context.Background(), procOpts{Argv: []string{"custom-cli", "-p", "P"}, Dir: dir}, agent.Spec{Tool: "acp-test-relative"}, "P", false, "")
	if got.ACPPrompt == nil || got.Argv[0] != "./server" {
		t.Fatalf("relative server not selected: %+v", got)
	}
}

func TestCustomACPRunsInsideSandbox(t *testing.T) {
	if _, ok, err := SandboxSupport(); !ok {
		t.Skipf("sandbox unavailable: %v", err)
	}
	dir, scratch, outside := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("ACP_DENIED_TARGET", filepath.Join(outside, "escaped"))
	t.Setenv("ACP_ENV_TEST", "preserved")
	bin := fakeAgent(t, t.TempDir(), "custom-acp", `
[ "$ACP_ENV_TEST" = preserved ] || exit 11
[ "$PWD" = "$1" ] || exit 12
if (echo escape > "$ACP_DENIED_TARGET") 2>/dev/null; then exit 13; fi
echo allowed > allowed
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"RESULT: changed=0\n"}}}}'
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
`)
	def := agent.Custom{Argv: []string{"custom-native", "{prompt}"}, ACP: []string{bin, dir}}
	if err := agent.Register("sandbox-acp-test", def); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Unregister("sandbox-acp-test") })
	o := withACP(context.Background(), procOpts{Tool: "sandbox-acp-test", Argv: []string{"custom-native", "P"}, Dir: dir, Timeout: 5 * time.Second}, agent.Spec{Tool: "sandbox-acp-test"}, "P", false, "")
	out, result := captureProc(context.Background(), o)
	if result.ExitCode != 0 || result.Err != nil || result.StreamErr != nil || !strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("sandboxed ACP = %+v, output = %q", result, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "allowed")); err != nil {
		t.Fatalf("worktree write failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(err) {
		t.Fatalf("outside write succeeded: %v", err)
	}
}

func TestOpenCodeACPPreparesReadOnlyConfig(t *testing.T) {
	if _, ok, err := SandboxSupport(); !ok {
		t.Skipf("sandbox unavailable: %v", err)
	}
	dir, scratch, config := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", scratch)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	for _, key := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, t.TempDir())
	}
	bin := fakeAgent(t, t.TempDir(), "opencode", `
mkdir -p "$XDG_CONFIG_HOME/opencode" || exit 11
if (echo changed > "$XDG_CONFIG_HOME/opencode/forbidden") 2>/dev/null; then exit 12; fi
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
`)
	prompt := "review"
	r := runProc(context.Background(), procOpts{Tool: "opencode", Argv: []string{bin}, Dir: dir, ACPPrompt: &prompt, Timeout: 5 * time.Second})
	if r.ExitCode != 0 || r.Err != nil || r.StreamErr != nil {
		t.Fatalf("sandboxed OpenCode startup = %+v", r)
	}
}

func TestOpenCodeACPUsesRelativeXDGDirectories(t *testing.T) {
	if _, ok, err := SandboxSupport(); !ok {
		t.Skipf("sandbox unavailable: %v", err)
	}
	dir, scratch := t.TempDir(), t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", scratch)
	t.Setenv("XDG_DATA_HOME", "relative/data")
	t.Setenv("XDG_STATE_HOME", "relative/state")
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	bin := fakeAgent(t, t.TempDir(), "opencode", `
for root in "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"; do
  mkdir -p "$root/opencode" || exit 11
  echo ok > "$root/opencode/probe" || exit 12
done
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn"}}'
`)
	prompt := "review"
	r := runProc(context.Background(), procOpts{Tool: "opencode", Argv: []string{bin}, Dir: dir, ACPPrompt: &prompt, Timeout: 5 * time.Second})
	if r.ExitCode != 0 || r.Err != nil || r.StreamErr != nil {
		t.Fatalf("relative XDG startup = %+v", r)
	}
	for _, root := range []string{"data", "state", "cache"} {
		if _, err := os.Stat(filepath.Join(dir, "relative", root, "opencode", "probe")); err != nil {
			t.Errorf("%s directory not written: %v", root, err)
		}
	}
}

func TestKimiACPProbe(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "kimi", `if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; fi`)
	if !subcommandACP(context.Background(), bin, t.TempDir()) {
		t.Fatal("did not detect ACP")
	}
	old := fakeAgent(t, t.TempDir(), "kimi", `echo 'unknown command'`)
	if subcommandACP(context.Background(), old, t.TempDir()) {
		t.Fatal("detected ACP on an older CLI")
	}
}

func TestArchivedKimiLauncherDoesNotReportSuccessfulReview(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "prompt-ran")
	t.Setenv("GAUNTLET_TEST_KIMI_PROMPT", marker)
	bin := fakeAgent(t, t.TempDir(), "kimi", `
if [ "$1" = --version ]; then echo 'kimi, version 1.52.0'; echo 'deprecated'; exit 0; fi
if [ "$1" = acp ] || [ "$1" = --help ]; then echo 'deprecated'; exit 0; fi
echo ran > "$GAUNTLET_TEST_KIMI_PROMPT"
exit 0
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, state := range []struct {
		continuing bool
		resume     string
	}{{false, ""}, {true, ""}, {true, "session"}} {
		o := withACP(context.Background(), procOpts{Tool: "kimi", Argv: []string{"kimi", "-p", "review"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "kimi"}, "review", state.continuing, state.resume)
		r := runProc(context.Background(), o)
		if r.Err == nil || !strings.Contains(r.Err.Error(), "1.52.0") {
			t.Fatalf("archived launcher accepted (state=%+v): %+v", state, r)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("prompt command ran: %v", err)
		}
	}
}

func TestArchivedKimiVersionDetection(t *testing.T) {
	for _, version := range []string{"kimi, version 1.51.0", "kimi, version 1.52.0-rc1", "2.1.1"} {
		bin := fakeAgent(t, t.TempDir(), "kimi", "echo '"+version+"'")
		if archivedKimi(context.Background(), bin, t.TempDir()) {
			t.Fatalf("rejected working version %q", version)
		}
	}
}

func TestArchivedKimiSkipsSameAgentRetry(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "kimi-review")
	bin := fakeAgent(t, t.TempDir(), "kimi", `echo 'kimi, version 1.52.0'`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := baseConfig(t, dir, set, []string{"kimi-review"}, bin)
	cfg.Agents, cfg.Bin, cfg.NoSandbox, cfg.Retries = []agent.Spec{{Tool: "kimi"}}, nil, true, 1
	bus := NewBus()
	done := make(chan []Event, 1)
	go collect(bus.Subscribe(256), done)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r.Run(ctx)
	bus.Close()
	events := <-done
	if ctx.Err() != nil || !sawLog(events, "Not retrying") || !sawLog(events, "1.52.0") {
		t.Fatalf("archived launcher was retried: context %v, events %+v", ctx.Err(), events)
	}
}

func TestACPProbesAcceptHelpOnStderr(t *testing.T) {
	dir := t.TempDir()
	bin := fakeAgent(t, t.TempDir(), "agent", `echo 'Agent Client Protocol --acp stdio --no-leader --always-approve' >&2`)
	if !subcommandACP(context.Background(), bin, dir) || !dshACP(context.Background(), bin, dir) || !grokACP(context.Background(), bin, dir) || acpFlag(context.Background(), bin, dir) != "--acp" {
		t.Fatal("did not detect ACP help printed on stderr")
	}
	failed := fakeAgent(t, t.TempDir(), "failed", `echo 'Agent Client Protocol --acp stdio --no-leader --always-approve' >&2; exit 1`)
	if subcommandACP(context.Background(), failed, dir) || dshACP(context.Background(), failed, dir) || grokACP(context.Background(), failed, dir) || acpFlag(context.Background(), failed, dir) != "" {
		t.Fatal("accepted an unsuccessful probe")
	}
}

func TestOMPDoesNotAutomaticallySelectACP(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "omp", `if [ "$1" = acp ] && [ "$2" = --help ]; then echo 'Agent Client Protocol'; fi`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Argv: []string{"omp", "-p", "P"}, Dir: t.TempDir(), Stream: true}
	for _, model := range []string{"", "provider/model", "model"} {
		got := withACP(context.Background(), base, agent.Spec{Tool: "omp", Model: model}, "P", false, "")
		if got.ACPPrompt != nil || !got.Stream {
			t.Errorf("model %q: selected unreliable ACP mode: %+v", model, got)
		}
	}
	base.Argv[0] = bin
	if got := withACP(context.Background(), base, agent.Spec{Tool: "omp"}, "P", false, ""); got.ACPPrompt != nil {
		t.Fatal("selected ACP for a binary override")
	}
}

func TestFeynmanDoesNotAutomaticallySelectPiACP(t *testing.T) {
	dir := t.TempDir()
	fakeAgent(t, dir, "feynman", `echo 0.5.60`)
	fakeAgent(t, dir, "pi-acp", `exit 0`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Tool: "feynman", Argv: []string{"feynman", "--prompt", "P"}, Dir: t.TempDir()}
	if got := withACP(context.Background(), base, agent.Spec{Tool: "feynman"}, "P", false, ""); got.ACPPrompt != nil {
		t.Fatalf("selected an unverified pi-acp bridge: %+v", got)
	}
}

func TestCopilotACP(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "copilot", `if [ "$1" = --help ]; then echo '--acp'; fi`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := procOpts{Argv: []string{"copilot", "--allow-all", "-p", "P"}, Dir: t.TempDir()}
	for _, model := range []string{"", "pinned"} {
		got := withACP(context.Background(), base, agent.Spec{Tool: "copilot", Model: model}, "P", false, "")
		if strings.Join(got.Argv, " ") != "copilot --acp --allow-all" || got.ACPPrompt == nil || got.ACPModel != model {
			t.Errorf("model %q: ACP options = %+v", model, got)
		}
	}
	for _, spec := range []agent.Spec{{Tool: "copilot", Model: "auto"}, {Tool: "copilot", Effort: "high"}} {
		if got := withACP(context.Background(), base, spec, "P", false, ""); got.ACPPrompt != nil {
			t.Errorf("spec %+v should use its CLI invocation", spec)
		}
	}
	base.Argv[0] = bin
	if got := withACP(context.Background(), base, agent.Spec{Tool: "copilot"}, "P", false, ""); got.ACPPrompt != nil {
		t.Fatal("selected ACP for a binary override")
	}
	if err := agent.Register("copilot", agent.Custom{Argv: []string{"wrapper", "{prompt}"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agent.Unregister("copilot") })
	if got := withACP(context.Background(), procOpts{Argv: []string{"wrapper", "P"}}, agent.Spec{Tool: "copilot"}, "P", false, ""); got.ACPPrompt != nil {
		t.Fatal("replaced the user's custom command")
	}
}

func TestCopilotACPAuthenticationFallback(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "copilot", `
if [ "$1" = --help ]; then
  echo --acp
elif [ "$1" = --acp ]; then
  read -r init
  printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
  read -r session
  printf '%s\n' '{"id":2,"error":{"code":-32000,"message":"Authentication required"}}'
  while read -r line; do exit 42; done
  sleep 30
else
  echo 'RESULT: changed=0'
fi
`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	o := withACP(context.Background(), procOpts{Argv: []string{"copilot", "--allow-all", "-p", "review"}, Dir: t.TempDir(), NoSandbox: true, Timeout: 5 * time.Second}, agent.Spec{Tool: "copilot"}, "review", false, "")
	out, result := captureProc(context.Background(), o)
	if result.ExitCode != 0 || result.StreamErr != nil || !strings.Contains(out, "RESULT: changed=0") {
		t.Fatalf("fallback = %+v, output = %q", result, out)
	}
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":5,"error":{"code":-32000,"message":"Authentication required"}}`
	var requests bytes.Buffer
	if err := acpSession(strings.NewReader(input), &requests, "/work", "review", "", "", "", "", true, false, nil, nil, nil, nil); err == nil || errors.Is(err, errACPAuthenticationRequired) {
		t.Fatalf("prompt error was eligible for fallback: %v", err)
	}
}

func TestACPFlagProbe(t *testing.T) {
	for _, tt := range []struct{ help, want string }{
		{"--acp, enable ACP", "--acp"},
		{"--experimental-acp enable ACP", "--experimental-acp"},
		{"--acp-other", ""},
	} {
		bin := fakeAgent(t, t.TempDir(), "agent", "echo '"+tt.help+"'")
		if got := acpFlag(context.Background(), bin, t.TempDir()); got != tt.want {
			t.Errorf("help %q: got %q, want %q", tt.help, got, tt.want)
		}
	}
}

func TestACPCapabilityProbeStopsOnCancellation(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "opencode", "sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	o := withACP(ctx, procOpts{Argv: []string{bin, "run", "--standalone", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "opencode"}, "P", false, "")
	if o.ACPPrompt != nil || ctx.Err() == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("probe did not stop after cancellation: options %+v, error %v, elapsed %s", o, ctx.Err(), time.Since(start))
	}
}

func TestOpenCodeProbeCancellationInterruptsReview(t *testing.T) {
	dir := testRepo(t)
	set, _ := promptSet(t, "a-review")
	marker := filepath.Join(t.TempDir(), "probe-started")
	t.Setenv("GAUNTLET_TEST_PROBE_MARKER", marker)
	bin := fakeAgent(t, t.TempDir(), "opencode", `: > "$GAUNTLET_TEST_PROBE_MARKER"; sleep 30`)
	cfg := baseConfig(t, dir, set, []string{"a-review"}, bin)
	cfg.Agents = []agent.Spec{{Tool: "opencode"}}
	cfg.Bin = map[string]string{"opencode": bin}
	r, err := New(context.Background(), cfg, NewBus())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	r.Run(ctx)
	<-done
	results := r.Stats().Results()
	if len(results) != 1 || results[0].Status != StatusInterrupted {
		t.Fatalf("results = %+v; want one interrupted review", results)
	}
}

func TestWithACP(t *testing.T) {
	bin := fakeAgent(t, t.TempDir(), "opencode", `echo 'Agent Client Protocol'`)
	base := procOpts{Argv: []string{bin, "run", "--standalone", "P"}, Stream: true}
	spec := agent.Spec{Tool: "opencode", Model: "provider/model", Effort: "high"}
	got := withACP(context.Background(), base, spec, "prompt", false, "")
	if strings.Join(got.Argv, " ") != bin+" acp" || got.ACPPrompt == nil || *got.ACPPrompt != "prompt" || got.ACPModel != spec.Model || got.ACPEffort != spec.Effort || got.Stream {
		t.Fatalf("ACP options = %+v", got)
	}
	got = withACP(context.Background(), base, agent.Spec{Tool: "opencode", Model: "provider/nested/model#high"}, "prompt", false, "")
	if got.ACPPrompt == nil || got.ACPModel != "provider/nested/model" || got.ACPEffort != "high" {
		t.Fatalf("model variant ACP options = %+v", got)
	}
	for _, s := range []agent.Spec{{Tool: "opencode", Model: "provider/model#"}, {Tool: "opencode", Model: "provider/model#high#low"}, {Tool: "opencode", Model: "provider/model#high", Effort: "low"}} {
		if selected := withACP(context.Background(), base, s, "prompt", false, ""); selected.ACPPrompt != nil {
			t.Errorf("invalid model selector %+v used ACP", s)
		}
	}
	got = withACP(context.Background(), base, spec, "prompt", true, "")
	if got.ACPPrompt != nil {
		t.Fatal("continued without a known ACP session")
	}
	got = withACP(context.Background(), base, spec, "prompt", true, "session")
	if got.ACPResume != "session" || got.ACPFallbackArgv != nil {
		t.Fatalf("continued ACP options = %+v", got)
	}
	unsupported := base
	unsupported.Argv = []string{fakeAgent(t, t.TempDir(), "opencode", `exit 2`), "run", "--standalone", "P"}
	if got := withACP(context.Background(), unsupported, spec, "prompt", false, ""); got.ACPPrompt != nil {
		t.Fatal("selected ACP for an OpenCode without the subcommand")
	}
	for _, tool := range []string{"gemini", "qwen"} {
		bin := fakeAgent(t, t.TempDir(), tool, `echo --acp`)
		t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
		got = withACP(context.Background(), procOpts{Argv: []string{tool, "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: tool, Model: "pinned"}, "P", false, "")
		if strings.Join(got.Argv, " ") != tool+" --acp -m pinned" || got.ACPModel != "" {
			t.Errorf("%s ACP options = %+v", tool, got)
		}
	}
	bin = fakeAgent(t, t.TempDir(), "kimi", `echo 'Agent Client Protocol'`)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"kimi", "-m", "pinned", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "kimi", Model: "pinned"}, "P", false, "")
	if strings.Join(got.Argv, " ") != "kimi acp" || got.ACPModel != "pinned" {
		t.Errorf("kimi ACP options = %+v", got)
	}
	got = withACP(context.Background(), procOpts{Argv: []string{bin, "-p", "P"}}, agent.Spec{Tool: "kimi"}, "P", false, "")
	if got.ACPPrompt != nil {
		t.Fatal("probed a binary override")
	}
	legacy := fakeAgent(t, t.TempDir(), "kimi", `if [ "$1" = --help ]; then echo --acp; else exit 1; fi`)
	t.Setenv("PATH", filepath.Dir(legacy)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"kimi", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "kimi"}, "P", false, "")
	if strings.Join(got.Argv, " ") != "kimi --acp" {
		t.Errorf("legacy Kimi ACP options = %+v", got)
	}
	cursor := fakeAgent(t, t.TempDir(), "cursor-agent", `echo 'Agent Client Protocol'`)
	t.Setenv("PATH", filepath.Dir(cursor)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"cursor-agent", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "cursor-agent"}, "P", false, "")
	if strings.Join(got.Argv, " ") != "cursor-agent acp" {
		t.Errorf("Cursor ACP options = %+v", got)
	}
	got = withACP(context.Background(), procOpts{Argv: []string{"cursor-agent", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "cursor-agent", Model: "pinned"}, "P", false, "")
	if !slices.Equal(got.Argv, []string{"cursor-agent", "--model", "pinned", "acp"}) || got.ACPPrompt == nil || got.ACPModel != "" {
		t.Fatalf("Cursor ACP with a pinned model = %+v", got)
	}
	grok := fakeAgent(t, t.TempDir(), "grok", `echo 'stdio --no-leader --always-approve'`)
	t.Setenv("PATH", filepath.Dir(grok)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"grok", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "grok", Model: "pinned"}, "P", false, "")
	if strings.Join(got.Argv, " ") != "grok agent --no-leader --always-approve --model pinned stdio" || got.ACPPrompt == nil || got.ACPModel != "pinned" {
		t.Errorf("Grok ACP options = %+v", got)
	}
	oldGrok := fakeAgent(t, t.TempDir(), "grok", `echo 'stdio --always-approve'`)
	t.Setenv("PATH", filepath.Dir(oldGrok)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"grok", "-p", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "grok"}, "P", false, "")
	if got.ACPPrompt != nil {
		t.Fatal("selected Grok ACP without local-only support")
	}
	dsh := fakeAgent(t, t.TempDir(), "dsh", `
if [ "$3" = --dump-config ]; then
  printf '%s\n' '- id: agent-default-model' '  provider: configured'
else
  echo 'Agent Client Protocol'
fi
`)
	t.Setenv("PATH", filepath.Dir(dsh)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got = withACP(context.Background(), procOpts{Argv: []string{"dsh", "--profile", "headless", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "dsh", Model: "provider/model"}, "P", false, "")
	if strings.Join(got.Argv, " ") != "dsh --profile acp" || got.ACPModel != `["provider","model"]` {
		t.Errorf("dsh ACP options = %+v", got)
	}
	got = withACP(context.Background(), procOpts{Argv: []string{"dsh", "--profile", "headless", "P"}, Dir: t.TempDir()}, agent.Spec{Tool: "dsh", Model: "model"}, "P", false, "")
	if got.ACPPrompt == nil || got.ACPModel != `["configured","model"]` {
		t.Fatalf("dsh ACP did not reuse the headless provider: %+v", got)
	}
}
