// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseSpecs(t *testing.T) {
	got, err := ParseSpecs("claude:opus, codex:gpt-5-codex ,claude:opus")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("duplicates should collapse, got %v", got)
	}
	if got[0].Label() != "claude:opus" || got[1].Label() != "codex:gpt-5-codex" {
		t.Fatalf("order or labels wrong: %v", got)
	}
}

func TestParseSpecsRejects(t *testing.T) {
	cases := []string{
		"claud",          // typo
		"agy:some-model", // agy takes no model
		"dsh:bad model!", // dsh models are spliced into YAML
		"",               // nothing named
	}
	for _, in := range cases {
		if _, err := ParseSpecs(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestParseSpecsSuggestsClosest(t *testing.T) {
	_, err := ParseSpecs("claud")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("want a suggestion for claude, got %v", err)
	}
}

func TestParseBinRejects(t *testing.T) {
	cases := []string{
		"claude",       // no = separator
		"=/bin/sh",     // no tool named
		"claude=",      // no path given
		"nope:/bin/sh", // unknown tool
	}
	for _, in := range cases {
		if _, _, err := ParseBin(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestParseBinResolvesBeforeAnyChdir(t *testing.T) {
	// The override must come back absolute: a relative --bin that only
	// resolves after the runner chdirs into the reviewed tree would let the
	// tree choose which executable runs with permissions bypassed.
	self, err := os.Executable()
	if err != nil {
		t.Skip("cannot locate this test binary")
	}
	tool, path, err := ParseBin("claude=" + self)
	if err != nil || tool != "claude" || !filepath.IsAbs(path) {
		t.Fatalf("got %q %q %v", tool, path, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "fake-agent")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, path, err = ParseBin("codex=~/bin/fake-agent")
	if err != nil || path != fake {
		t.Fatalf("~ expansion lost: %q %v", path, err)
	}

	if _, _, err := ParseBin("kimi=./relative-agent"); err == nil {
		t.Fatal("a relative path must not resolve")
	}
}

func TestBuildCmdPlacesPromptLast(t *testing.T) {
	cases := map[string]Spec{
		"claude": {Tool: "claude", Model: "opus"},
		"codex":  {Tool: "codex"},
		"kimi":   {Tool: "kimi", Model: "k2"},
	}
	for name, spec := range cases {
		argv, err := BuildCmd(spec, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if argv[len(argv)-1] != "PROMPT" {
			t.Errorf("%s: prompt is not the last argument: %v", name, argv)
		}
		if spec.Model != "" && !slices.Contains(argv, spec.Model) {
			t.Errorf("%s: model missing from %v", name, argv)
		}
	}
}

func TestBuildCmdContinueFlagPosition(t *testing.T) {
	// A subcommand agent takes session flags after the subcommand.
	argv, err := BuildCmd(Spec{Tool: "opencode"}, "P", BuildOpts{Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "opencode" || argv[1] != "run" || argv[2] != "-c" {
		t.Fatalf("want [opencode run -c ...], got %v", argv)
	}

	argv, err = BuildCmd(Spec{Tool: "claude"}, "P", BuildOpts{Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	if argv[1] != "-c" {
		t.Fatalf("want -c right after the binary, got %v", argv)
	}
}

func TestBuildCmdNoContinueForFreshOnlyAgents(t *testing.T) {
	// cursor-agent has no prompt-mode resume: asking for one must not inject
	// a flag it would reject.
	argv, err := BuildCmd(Spec{Tool: "cursor-agent"}, "P", BuildOpts{Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(argv, "-c") {
		t.Fatalf("unexpected resume flag: %v", argv)
	}
}

func TestBuildCmdBinaryOverride(t *testing.T) {
	argv, err := BuildCmd(Spec{Tool: "claude"}, "P", BuildOpts{Binary: "/opt/claude"})
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "/opt/claude" {
		t.Fatalf("override ignored: %v", argv)
	}
}

func TestBuildCmdRefusesAPromptOverTheArgvLimit(t *testing.T) {
	// Linux fails the whole exec with E2BIG once one argument passes
	// MAX_ARG_STRLEN; a prompt past MaxPromptArg must be refused here, once,
	// instead of failing at launch on every agent.
	big := strings.Repeat("a", MaxPromptArg+1)
	for _, tool := range []string{"claude", "codex", "opencode"} {
		if _, err := BuildCmd(Spec{Tool: tool}, big, BuildOpts{}); err == nil {
			t.Errorf("%s: accepted a %d-byte argument", tool, len(big))
		}
	}
	// A custom definition splices the prompt into its own argv; the guard must
	// hold there too.
	if _, err := BuildCmd(Spec{Tool: "pi"}, big, BuildOpts{}); err == nil {
		t.Error("custom agent accepted an oversized prompt")
	}
	small, err := BuildCmd(Spec{Tool: "claude"}, "P", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if small[len(small)-1] != "P" {
		t.Fatalf("normal prompts broken: %v", small)
	}
}

func TestParseUsage(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantOut   int
		wantTotal int
	}{
		{"json output", `{"usage":{"output_tokens":1234,"input_tokens":9}}`, 1234, -1},
		{"camel case", `"outputTokens": 7_000`, 7000, -1},
		{"codex summary", "tokens used: 12,345", -1, 12345},
		{"session total json", `{"total_tokens":555}`, -1, 555},
		{"line form output", "\nOutput tokens: 42\n", 42, -1},
		{"line form total", "\nTotal tokens: 77\n", -1, 77},
		{"cumulative max wins", `"output_tokens":10 ... "output_tokens":90`, 90, -1},
		{"nothing", "no usage here", -1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := ParseUsage([]byte(c.in))
			if u.Output != c.wantOut || u.Total != c.wantTotal {
				t.Fatalf("got %+v, want output=%d total=%d", u, c.wantOut, c.wantTotal)
			}
		})
	}
}

func TestUsageReported(t *testing.T) {
	if got := (Usage{Output: 5, Total: 100}).Reported(); got != 5 {
		t.Fatalf("output tokens should win, got %d", got)
	}
	if got := (Usage{Output: -1, Total: 100}).Reported(); got != 100 {
		t.Fatalf("total should be the fallback, got %d", got)
	}
	if (Usage{Output: -1, Total: -1}).Known() {
		t.Fatal("unknown usage reported as known")
	}
}

func TestTailKeepsLastBytes(t *testing.T) {
	tl := NewTail(8)
	tl.Write([]byte("abcdef"))
	tl.Write([]byte("ghijkl"))
	if got := string(tl.Bytes()); got != "efghijkl" {
		t.Fatalf("got %q, want %q", got, "efghijkl")
	}

	tl = NewTail(4)
	tl.Write([]byte("0123456789"))
	if got := string(tl.Bytes()); got != "6789" {
		t.Fatalf("oversized write: got %q", got)
	}
}

// FuzzUsageTail drives the output-tail ring buffer with arbitrary writes in
// arbitrary chunkings and checks it against a reference model: the retained
// bytes are always exactly the last size bytes written, never more. The
// retained tail is what ParseUsage reads, so its counters are pinned too:
// absent stays -1, found is positive, and Reported follows Output-then-Total.
func FuzzUsageTail(f *testing.F) {
	f.Add([]byte("abcdefghijkl"), 8, 6)
	f.Add([]byte("0123456789"), 4, 10)
	f.Add([]byte(`"output_tokens":1234`), 10, 5)
	f.Add([]byte("tokens used: 12,345"), 32, 1)
	f.Add([]byte("héllo wörld — ünïcode tail"), 12, 3)
	f.Add([]byte{}, 1, 0)
	f.Add([]byte("x"), 4096, 0)
	f.Fuzz(func(t *testing.T, data []byte, size, split int) {
		if size < 1 {
			size = 1
		} else if size > 4096 {
			size = 4096
		}
		split = min(max(split, 0), len(data))

		tl := NewTail(size)
		if _, err := tl.WriteString(string(data[:split])); err != nil {
			t.Fatal(err)
		}
		if _, err := tl.Write(data[split:]); err != nil {
			t.Fatal(err)
		}

		got := tl.Bytes()
		if len(got) > size {
			t.Fatalf("ring kept %d bytes under size %d", len(got), size)
		}
		want := string(data)
		if len(want) > size {
			want = want[len(want)-size:]
		}
		if string(got) != want {
			t.Fatalf("tail %q, want last %d bytes %q", got, size, want)
		}

		u := ParseUsage(got)
		if u.Output < -1 || u.Thinking < -1 || u.Total < -1 {
			t.Fatalf("impossible counters on %q: %+v", got, u)
		}
		reported := u.Reported()
		switch {
		case u.Output >= 0:
			if reported != u.Output {
				t.Fatalf("Reported ignored output: %+v -> %d", u, reported)
			}
		case u.Total >= 0:
			if reported != u.Total {
				t.Fatalf("Reported ignored total: %+v -> %d", u, reported)
			}
		default:
			if reported != 0 || u.Known() {
				t.Fatalf("unknown usage leaked: %+v known=%v reported=%d",
					u, u.Known(), reported)
			}
		}
	})
}

func TestParseDshProvider(t *testing.T) {
	dump := `
plugins:
  - id: other-plugin
    provider: 'nope'
  - id: agent-default-model
    provider: "deepseek"
    model: 'chat'
`
	if got := ParseDshProvider(dump); got != "deepseek" {
		t.Fatalf("got %q, want deepseek", got)
	}
	if got := ParseDshProvider("nothing here"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestParseUsagePicksUpThinkingTokens(t *testing.T) {
	u := ParseUsage([]byte(`{"usage":{"output_tokens":900,"output_tokens_details":{"thinking_tokens":300}}}`))
	if u.Output != 900 || u.Thinking != 300 {
		t.Fatalf("got %+v", u)
	}
	// An agent that reports no reasoning share leaves it at zero, which is not
	// the same as claiming zero thinking happened.
	if q := ParseUsage([]byte(`{"usage":{"output_tokens":5}}`)); q.Thinking > 0 {
		t.Fatalf("invented a reasoning share: %+v", q)
	}
}

func TestCustomAgentInvocation(t *testing.T) {
	t.Cleanup(resetCustom(t))
	if err := Register("myagent", Custom{
		Argv:     []string{"myagent", "run", "--flag", "{prompt}"},
		Model:    []string{"--model", "{model}"},
		Stream:   []string{"--json"},
		Continue: []string{"--resume"},
	}); err != nil {
		t.Fatal(err)
	}
	if !IsValid("myagent") {
		t.Fatal("a defined agent should be usable")
	}

	argv, err := BuildCmd(Spec{Tool: "myagent"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "myagent run --flag PROMPT" {
		t.Fatalf("argv: %v", argv)
	}

	// Flags land before the prompt, which agents expect last.
	argv, err = BuildCmd(Spec{Tool: "myagent", Model: "big"}, "PROMPT",
		BuildOpts{Stream: true, Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "myagent run --flag --json --resume --model big PROMPT"
	if strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}
}

func TestCustomAgentRejectsBadDefinitions(t *testing.T) {
	t.Cleanup(resetCustom(t))
	cases := map[string]Custom{
		"no argv":   {},
		"no prompt": {Argv: []string{"x", "--flag"}},
	}
	for name, def := range cases {
		if err := Register("tmp", def); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	if err := Register("bad name", Custom{Argv: []string{"x", "{prompt}"}}); err == nil {
		t.Error("a name with a space should be rejected")
	}
	// Built-ins are not redefinable: a wrong redefinition of claude would be
	// invisible and would run something else entirely.
	if err := Register("claude", Custom{Argv: []string{"x", "{prompt}"}}); err == nil {
		t.Error("redefining a built-in should be rejected")
	}
}

func TestParseAgentCmd(t *testing.T) {
	name, def, err := ParseAgentCmd("pi=pi --agent reviewer -p {prompt}")
	if err != nil {
		t.Fatal(err)
	}
	if name != "pi" {
		t.Fatalf("name: %q", name)
	}
	if strings.Join(def.Argv, " ") != "pi --agent reviewer -p {prompt}" {
		t.Fatalf("argv: %v", def.Argv)
	}
	for _, bad := range []string{"noequals", "=argv", "name=", "name=no-placeholder"} {
		if _, _, err := ParseAgentCmd(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestPiFamilyDefinitions(t *testing.T) {
	// The pi family ships as definitions rather than code. Each one must
	// produce a runnable argv with the prompt last.
	for _, name := range []string{"pi", "prime-agent", "feynman", "omp"} {
		def, ok := CustomDef(name)
		if !ok {
			t.Fatalf("%s should ship as a definition", name)
		}
		if err := def.Validate(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		argv, err := BuildCmd(Spec{Tool: name}, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if argv[0] != name || argv[len(argv)-1] != "PROMPT" {
			t.Errorf("%s: argv %v", name, argv)
		}
		if !TakesModel(name) {
			t.Errorf("%s: should accept a model", name)
		}
	}
}

func TestPiFamilyStreamAndTranscripts(t *testing.T) {
	// pi, prime-agent, and omp have a json output mode; feynman does not, and
	// must not be given a flag it would reject.
	for _, name := range []string{"pi", "prime-agent", "omp"} {
		if !SupportsStream(name) {
			t.Errorf("%s should support machine-readable output", name)
		}
	}
	if SupportsStream("feynman") {
		t.Error("feynman has no json mode; claiming one would break it")
	}

	// Verified agents are auto-detectable; unverified ones are not.
	if IsOptIn("pi") || IsOptIn("prime-agent") || IsOptIn("feynman") {
		t.Error("verified definitions should be auto-detectable")
	}
	if !IsOptIn("omp") {
		t.Error("an unverified invocation must not be auto-detected")
	}

	// The three with known session stores can report live tokens.
	for _, name := range []string{"pi", "prime-agent", "feynman"} {
		def, _ := CustomDef(name)
		if def.Usage == nil || len(def.Usage.Roots) == 0 {
			t.Errorf("%s: no transcript location, so no live tokens", name)
		}
	}
}

func TestCustomAgentFileRoundTrip(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	body := `{"piclone":{"argv":["piclone","-p","{prompt}"],"stream":["--jsonl"],"note":"test"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadCustomFile(path); err != nil {
		t.Fatal(err)
	}
	if !SupportsStream("piclone") {
		t.Fatal("stream flags from the file were lost")
	}
	// A missing file is normal, not an error.
	if err := LoadCustomFile(filepath.Join(dir, "absent.json")); err != nil {
		t.Fatalf("missing file should be fine: %v", err)
	}
	// A malformed one must refuse rather than run with a half-loaded set.
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadCustomFile(bad); err == nil {
		t.Fatal("malformed definitions should be rejected")
	}
}

// resetCustom restores the definition registry after a test mutates it.
func resetCustom(t *testing.T) func() {
	t.Helper()
	customMu.Lock()
	saved := make(map[string]Custom, len(custom))
	maps.Copy(saved, custom)
	customMu.Unlock()
	return func() {
		customMu.Lock()
		custom = saved
		customMu.Unlock()
	}
}

func TestDshStreamAsksForReadableSessions(t *testing.T) {
	// dsh compresses its session log by default, which no reader can follow.
	// Stream mode passes an overlay that turns compression off; without it the
	// command must be untouched.
	plain, err := BuildCmd(Spec{Tool: "dsh"}, "P", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	streamed, err := BuildCmd(Spec{Tool: "dsh"}, "P", BuildOpts{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain, "--patch") {
		t.Fatalf("no overlay should be passed without --stream: %v", plain)
	}
	if !slices.Contains(streamed, "--patch") {
		t.Fatalf("stream mode should pass the overlay: %v", streamed)
	}
	if streamed[len(streamed)-1] != "P" {
		t.Fatalf("prompt must stay last: %v", streamed)
	}
	// The overlay must actually say what we think it says.
	var patch string
	for i, a := range streamed {
		if a == "--patch" && i+1 < len(streamed) {
			patch = streamed[i+1]
		}
	}
	body, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "session-persistence-jsonl") ||
		!strings.Contains(string(body), "compression: 'none'") {
		t.Fatalf("overlay does not disable compression:\n%s", body)
	}
}
