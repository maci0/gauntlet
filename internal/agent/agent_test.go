// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestParseSpecsEffort(t *testing.T) {
	cases := map[string]Spec{
		"claude:opus-5@xhigh": {Tool: "claude", Model: "opus-5", Effort: "xhigh"},
		"claude@max":          {Tool: "claude", Model: "", Effort: "max"},
		"opencode:anthropic/claude-sonnet-5@medium": {
			Tool: "opencode", Model: "anthropic/claude-sonnet-5", Effort: "medium"},
		// The last @ separates: a Vertex-style version pin stays in the model.
		"claude:sonnet@20240620@xhigh": {Tool: "claude", Model: "sonnet@20240620", Effort: "xhigh"},
	}
	for in, want := range cases {
		got, err := ParseSpecs(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
		if got[0].Label() != in {
			t.Errorf("%q: label %q should round-trip", in, got[0].Label())
		}
	}
}

func TestParseSpecsEffortRejects(t *testing.T) {
	cases := []string{
		"gemini:flash@high", // no verified effort flag
		"codex@high",        // no verified effort flag
		"clanker@high",      // takes neither model nor effort
		"claude@",           // empty effort
		"claude:opus@",      // empty effort after a model
		"claude@hi/gh",      // effort charset excludes separators
	}
	for _, in := range cases {
		if _, err := ParseSpecs(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestParseSpecsMixedTakesNoPin(t *testing.T) {
	// The keyword names a set, not one CLI; the error must not fall through
	// to "unknown tool", which would call mixed unknown and valid at once.
	for _, in := range []string{"mixed@high", "mixed:opus", "all@x", "mixed@"} {
		_, err := ParseSpecs(in)
		if err == nil {
			t.Errorf("%q should be rejected", in)
			continue
		}
		if strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("%q: want the every-installed-agent error, got %v", in, err)
		}
	}
}

func TestParseSpecsSuggestsClosest(t *testing.T) {
	_, err := ParseSpecs("claud")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("want a suggestion for claude, got %v", err)
	}
}

func TestSplitProvider(t *testing.T) {
	p, n := splitProvider("openai/gpt-4")
	if p != "openai" || n != "gpt-4" {
		t.Fatalf("got provider %q name %q", p, n)
	}
	p, n = splitProvider("gpt-4")
	if p != "" || n != "gpt-4" {
		t.Fatalf("no slash: got provider %q name %q", p, n)
	}
	p, n = splitProvider("a/b/c")
	if p != "a/b" || n != "c" {
		t.Fatalf("last slash: got provider %q name %q", p, n)
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

// crush's non-interactive mode is a subcommand, and its interactive --yolo
// flag is not accepted there: `crush run --yolo` exits with "unknown flag"
// instead of running, and the run itself auto-approves the session anyway.
func TestBuildCmdCrushUsesRunMode(t *testing.T) {
	argv, err := BuildCmd(Spec{Tool: "crush", Model: "openai/gpt-5"}, "P", BuildOpts{Continue: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"crush", "run", "-C", "--quiet", "-m", "openai/gpt-5", "P"}
	if len(argv) != len(want) {
		t.Fatalf("argv %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv %v, want %v", argv, want)
		}
	}
	for _, a := range argv {
		if a == "--yolo" || a == "-y" {
			t.Fatalf("crush run does not take %q: %v", a, argv)
		}
	}
}

// The runner writes the commit in worktree mode, and only the agent knows
// what its change was: the subject it prints is what the history will say.
// The resolver caches what it found, and PATH decides what there is to find:
// a process that changes PATH (a wrapper adding a directory, a test) must not
// keep being told an agent is missing because it was missing a moment ago.
// ParseSubject reads agent output, which is untrusted, and what it returns
// goes into a commit message. Whatever it is fed, the result must be one line
// of printable text and nothing longer than a subject line.
func FuzzParseSubject(f *testing.F) {
	f.Add("SUBJECT: fix: guard the nil map write\nRESULT: changed=1")
	f.Add("subject:\tfeat(ui): tighten the lanes\r\n")
	f.Add("SUBJECT: " + strings.Repeat("x", 4096))
	f.Add("SUBJECT: fix\x1b[31m: colored\x00 and \x07 belled")
	f.Add("PATH: a.go\nSUBJECT:   \nSUBJECT: chore: the last one wins\n")
	f.Fuzz(func(t *testing.T, tail string) {
		got := ParseSubject([]byte(tail))
		if len(got) > subjectMax {
			t.Fatalf("subject is %d bytes, want at most %d: %q", len(got), subjectMax, got)
		}
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("a subject spanning lines can forge a commit body: %q", got)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control byte %q survived into %q", r, got)
			}
		}
		if got != strings.TrimSpace(got) {
			t.Fatalf("subject keeps outer whitespace: %q", got)
		}
	})
}

func TestResolveFollowsPathChanges(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "pretend-agent")

	if got := Resolve("pretend-agent"); got != "" {
		t.Fatalf("nothing is installed yet, got %q", got)
	}
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := Resolve("pretend-agent"); got != stub {
		t.Fatalf("Resolve = %q after PATH gained %s, want %q", got, dir, stub)
	}

	// And the reverse: dropping the directory drops the answer.
	t.Setenv("PATH", "/nonexistent-"+filepath.Base(dir))
	if got := Resolve("pretend-agent"); got != "" {
		t.Fatalf("Resolve = %q after PATH lost the directory, want none", got)
	}
}

func TestParseSubject(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"a plain subject", "PATH: x\nSUBJECT: fix: guard the nil map write\nRESULT: changed=1",
			"fix: guard the nil map write"},
		{"none printed", "PATH: x\nRESULT: changed=1", ""},
		{"the last one wins", "SUBJECT: chore: first\nSUBJECT: feat: second\n", "feat: second"},
		{"empty is no subject", "SUBJECT:   \n", ""},
		{"leading space is fine", "   SUBJECT: docs: explain the lock\n", "docs: explain the lock"},
	}
	for _, c := range cases {
		if got := ParseSubject([]byte(c.in)); got != c.want {
			t.Errorf("%s: ParseSubject = %q, want %q", c.name, got, c.want)
		}
	}
}

// The subject becomes a commit message, so it is one line of printable text
// and nothing else: a newline in it would forge a body or an author trailer.
func TestParseSubjectRefusesToCarryStructure(t *testing.T) {
	got := ParseSubject([]byte("SUBJECT: fix: thing\x1b[31m\x07 and \x00more\n"))
	if strings.ContainsAny(got, "\x1b\x07\x00\n") {
		t.Fatalf("control bytes survived: %q", got)
	}
	long := "SUBJECT: " + strings.Repeat("x", 500) + "\n"
	if n := len(ParseSubject([]byte(long))); n > subjectMax {
		t.Fatalf("subject is %d bytes, want at most %d", n, subjectMax)
	}
}

// A byte-counted cut would land mid-rune and put mojibake into git history.
func TestParseSubjectTruncatesOnARuneBoundary(t *testing.T) {
	subject := strings.Repeat("café ", 30)
	if got := len(subject); got <= subjectMax || subjectMax%6 == 0 {
		t.Fatalf("fixture no longer exercises a mid-rune cut: %d bytes", got)
	}
	got := ParseSubject([]byte("SUBJECT: " + subject + "\n"))
	if !utf8.ValidString(got) {
		t.Fatalf("subject truncation produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > subjectMax {
		t.Fatalf("subject is %d runes, want at most %d", n, subjectMax)
	}
	if !strings.HasPrefix(subject, got) {
		t.Fatalf("truncated subject is not a rune-clean prefix: %q", got)
	}
}

func TestBuildCmdEffortFlags(t *testing.T) {
	argv, err := BuildCmd(Spec{Tool: "claude", Model: "opus-5", Effort: "xhigh"}, "P", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	want := "claude --dangerously-skip-permissions --model opus-5 --effort xhigh -p P"
	if strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}

	argv, err = BuildCmd(Spec{Tool: "opencode", Effort: "minimal"}, "P", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	want = "opencode run --auto --variant minimal P"
	if strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}
}

func TestBuildCmdPlacesPromptLast(t *testing.T) {
	cases := map[string]Spec{
		"claude": {Tool: "claude", Model: "opus"},
		"codex":  {Tool: "codex"},
		"kimi":   {Tool: "kimi", Model: "k2"},
		"crush":  {Tool: "crush", Model: "anthropic/claude-sonnet-4"},
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
	// MAX_ARG_STRLEN; a prompt past maxPromptArg must be refused here, once,
	// instead of failing at launch on every agent.
	big := strings.Repeat("a", maxPromptArg+1)
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
		// A review reading source or transcripts prints other people's
		// sentinels; taken as a count, one of those overflows the run total.
		{"absurd counter ignored", `{"usage":{"total_tokens":9223372036854775807}}`, -1, -1},
		{"absurd counter does not hide a real one", `"output_tokens":123456789012345 "output_tokens":900`, 900, -1},
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
	tl.WriteString("abcdef")
	tl.WriteString("ghijkl")
	if got := string(tl.Bytes()); got != "efghijkl" {
		t.Fatalf("got %q, want %q", got, "efghijkl")
	}

	tl = NewTail(4)
	tl.WriteString("0123456789")
	if got := string(tl.Bytes()); got != "6789" {
		t.Fatalf("oversized write: got %q", got)
	}
}

// TestTailRingManyWrites drives the tail through hundreds of small writes
// with every chunking a pump might produce, checking it against the same
// reference model as FuzzUsageTail: exactly the last size bytes written.
// Two writes cannot reach steady state, so this is where wrap-around,
// offsets past the end, and repeated oversized writes get pinned.
func TestTailRingManyWrites(t *testing.T) {
	for _, size := range []int{1, 2, 3, 7, 64, 1000} {
		rng := rand.New(rand.NewPCG(uint64(size), uint64(size)))
		data := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
		tl := NewTail(size)
		var written []byte
		for range 500 {
			chunk := make([]byte, rng.IntN(3*size+10))
			for i := range chunk {
				chunk[i] = byte(data[rng.IntN(len(data))])
			}
			written = append(written, chunk...)
			if _, err := tl.WriteString(string(chunk)); err != nil {
				t.Fatal(err)
			}
		}
		want := string(written)
		if len(want) > size {
			want = want[len(want)-size:]
		}
		if got := string(tl.Bytes()); got != want {
			t.Fatalf("size %d: tail %q, want last %d bytes %q", size, got, size, want)
		}
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
		if _, err := tl.WriteString(string(data[split:])); err != nil {
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

// FuzzParseUsage drives ParseUsage with unbounded raw agent output. Where
// FuzzUsageTail only sees the ring's retained last few kilobytes, this pins
// the extractor's own contracts over the whole stream the pump could hand it:
// a counter is absent (-1) or within [0, maxPlausible], so another agent's
// sentinel can never leak into the run totals; Known and Reported stay
// coherent; and parsing is deterministic. Counters are deliberately not
// monotone under concatenation: a greedy digit run that crosses maxPlausible
// is rejected as one match, so a prefix may parse to a number its whole
// refuses (testdata corpus 561d58e24361a7f0).
func FuzzParseUsage(f *testing.F) {
	seeds := []string{
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":42}}}`,
		`{"usage":{"output_tokens":900,"output_tokens_details":{"thinking_tokens":300}}}`,
		`{"choices":[{"delta":{"content":"partial"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
		"tokens used: 12,345",
		"\nOutput tokens: 42\nTotal tokens: 77\n",
		// The plausibility boundary itself: 2^40 is a measurement, one more
		// is a misparse, and the int64 max sentinel from someone else's log
		// must never survive either.
		`{"usage":{"total_tokens":1099511627776}}`,
		`{"usage":{"total_tokens":1099511627777}}`,
		`{"usage":{"total_tokens":9223372036854775807}}`,
		`{"output_tokens":"1_000_000","completion_tokens":"12,345"}`,
		`{"outputTokens":` + strings.Repeat("9", 4096) + `}`,
		"OUTPUT TOKENS: 5\nToKeNs used: 6",
		strings.Repeat(`{"output_tokens":1}`, 1000),
		"héllo wörld — ünïcode tail 🐐",
	}
	for _, s := range seeds {
		f.Add([]byte(s), 0)
	}
	f.Fuzz(func(t *testing.T, data []byte, split int) {
		split = min(max(split, 0), len(data))
		for _, in := range [][]byte{data, data[:split], data[split:]} {
			u := ParseUsage(in)
			for _, c := range []struct {
				name string
				n    int
			}{
				{"output", u.Output}, {"thinking", u.Thinking}, {"total", u.Total},
			} {
				if c.n < -1 || c.n > maxPlausible {
					t.Fatalf("%s counter out of contract on %q: %d", c.name, in, c.n)
				}
			}

			reported := u.Reported()
			switch {
			case u.Output >= 0:
				if reported != u.Output {
					t.Fatalf("Reported ignored output on %q: %+v -> %d", in, u, reported)
				}
			case u.Total >= 0:
				if reported != u.Total {
					t.Fatalf("Reported ignored total on %q: %+v -> %d", in, u, reported)
				}
			default:
				if reported != 0 || u.Known() {
					t.Fatalf("unknown usage leaked on %q: %+v known=%v reported=%d",
						in, u, u.Known(), reported)
				}
			}

			if again := ParseUsage(in); again != u {
				t.Fatalf("ParseUsage is not deterministic on %q", in)
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
	if got := parseDshProvider(dump); got != "deepseek" {
		t.Fatalf("got %q, want deepseek", got)
	}
	if got := parseDshProvider("nothing here"); got != "" {
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
	if !isValid("myagent") {
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

// A definition can splice the model into its own argv with a {model}
// placeholder instead of a Model flag block. Such an agent accepts a pinned
// model, which lands exactly where the placeholder sits; one with nowhere to
// put a model refuses it instead of dropping it silently.
func TestCustomAgentModelPlaceholderInArgv(t *testing.T) {
	t.Cleanup(resetCustom(t))
	if err := Register("withmodel", Custom{
		Argv: []string{"withmodel", "--model={model}", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register("nomodel", Custom{
		Argv: []string{"nomodel", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}

	specs, err := ParseSpecs("withmodel:m1")
	if err != nil {
		t.Fatalf("an argv placeholder should accept a model: %v", err)
	}
	if len(specs) != 1 || specs[0].Model != "m1" {
		t.Fatalf("parsed specs wrong: %+v", specs)
	}
	argv, err := BuildCmd(Spec{Tool: "withmodel", Model: "m1"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "withmodel --model=m1 PROMPT"; strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}

	if _, err := ParseSpecs("nomodel:m"); err == nil {
		t.Error("an agent with nowhere to put a model should refuse one")
	}
}

// A definition accepts a pinned effort through an Effort flag block or an
// {effort} placeholder in argv, mirroring how Model works; one with neither
// refuses @effort instead of dropping it silently.
func TestCustomAgentEffort(t *testing.T) {
	t.Cleanup(resetCustom(t))
	if err := Register("witheffort", Custom{
		Argv:   []string{"witheffort", "-p", "{prompt}"},
		Model:  []string{"--model", "{model}"},
		Effort: []string{"--reasoning", "{effort}"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register("inargv", Custom{
		Argv: []string{"inargv", "--effort={effort}", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register("noeffort", Custom{
		Argv: []string{"noeffort", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}

	specs, err := ParseSpecs("witheffort:m1@high")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Effort != "high" {
		t.Fatalf("parsed specs wrong: %+v", specs)
	}
	argv, err := BuildCmd(specs[0], "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "witheffort -p --model m1 --reasoning high PROMPT"; strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}

	if _, err := ParseSpecs("inargv@low"); err != nil {
		t.Fatalf("an argv placeholder should accept an effort: %v", err)
	}
	argv, err = BuildCmd(Spec{Tool: "inargv", Effort: "low"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "inargv --effort=low PROMPT"; strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}

	if _, err := ParseSpecs("noeffort@high"); err == nil {
		t.Error("an agent with nowhere to put an effort should refuse one")
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
		if err := def.validate(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		argv, err := BuildCmd(Spec{Tool: name}, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if argv[0] != name || argv[len(argv)-1] != "PROMPT" {
			t.Errorf("%s: argv %v", name, argv)
		}
		if !takesModel(name) {
			t.Errorf("%s: should accept a model", name)
		}
	}
}

func TestPiFamilyStreamAndTranscripts(t *testing.T) {
	// pi, prime-agent, and omp have a json output mode, so Stream must change
	// their argv; feynman does not, and must not be given a flag it would
	// reject.
	for _, name := range []string{"pi", "prime-agent", "omp"} {
		plain, err := BuildCmd(Spec{Tool: name}, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		streamed, err := BuildCmd(Spec{Tool: name}, "PROMPT", BuildOpts{Stream: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if slices.Equal(plain, streamed) {
			t.Errorf("%s should support machine-readable output", name)
		}
	}
	plain, err := BuildCmd(Spec{Tool: "feynman"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	streamed, err := BuildCmd(Spec{Tool: "feynman"}, "PROMPT", BuildOpts{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plain, streamed) {
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
	argv, err := BuildCmd(Spec{Tool: "piclone"}, "PROMPT", BuildOpts{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--jsonl") {
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

// A misspelled key must refuse startup rather than silently change what the
// definition does: an ignored "optin" would auto-detect an agent its author
// had marked opt-in, and a dropped "usage" would quietly lose live tokens.
func TestCustomAgentFileUnknownField(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	body := `{"piclone":{"argv":["piclone","-p","{prompt}"],"optin":true}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := LoadCustomFile(path)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want an unknown-field rejection naming the key, got %v", err)
	}
}

func TestCustomAgentFileTrailingData(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte(`{} {"piclone":{"argv":["x","{prompt}"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadCustomFile(path); err == nil {
		t.Fatal("data after the JSON object must be rejected, not half-read")
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

// isolateDshPatches points UserCacheDir at a fresh temp dir and empties the
// patched-overlay table, restoring whatever was there when the test ends.
func isolateDshPatches(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if got, err := os.UserCacheDir(); err != nil || got != cache {
		t.Skip("UserCacheDir does not follow XDG_CACHE_HOME here")
	}

	dshPatchMu.Lock()
	saved := maps.Clone(dshPatches)
	clear(dshPatches)
	dshPatchMu.Unlock()
	t.Cleanup(func() {
		dshPatchMu.Lock()
		clear(dshPatches)
		maps.Copy(dshPatches, saved)
		dshPatchMu.Unlock()
	})
	return cache
}

// dsh pins its model through a generated --patch overlay rather than a model
// flag, so a pinned model must produce an overlay file naming both provider
// and model while the prompt stays last. Everything before the last slash is
// the provider, so org-scoped models keep their namespace.
func TestBuildCmdDshModelPinsAnOverlay(t *testing.T) {
	cache := isolateDshPatches(t)

	modelPatch := func(model string) string {
		t.Helper()
		argv, err := BuildCmd(Spec{Tool: "dsh", Model: model}, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if argv[len(argv)-1] != "PROMPT" {
			t.Fatalf("%s: prompt must stay last: %v", model, argv)
		}
		var patch string
		for i, a := range argv {
			if a == "--patch" && i+1 < len(argv) {
				patch = argv[i+1]
			}
		}
		if patch == "" {
			t.Fatalf("%s: no --patch overlay in %v", model, argv)
		}
		return patch
	}

	for _, c := range []struct{ model, key, want string }{
		{"deepseek/chat", "deepseek_chat",
			"- id: agent-default-model\n  config:\n    provider: 'deepseek'\n    model: 'chat'\n"},
		{"openrouter/deepseek/deepseek-chat", "openrouter_deepseek_deepseek-chat",
			"- id: agent-default-model\n  config:\n    provider: 'openrouter/deepseek'\n    model: 'deepseek-chat'\n"},
	} {
		patch := modelPatch(c.model)
		if want := filepath.Join(cache, "gauntlet", "dsh", c.key+".yml"); patch != want {
			t.Errorf("%s: overlay at %q, want %q", c.model, patch, want)
		}
		body, err := os.ReadFile(patch)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != c.want {
			t.Errorf("%s: overlay does not pin the pair:\n got %q\nwant %q", c.model, body, c.want)
		}
	}
}

func TestWriteDshPatchReplacesWholeFile(t *testing.T) {
	cache := isolateDshPatches(t)

	dir := filepath.Join(cache, "gauntlet", "dsh")
	// Callers slug the key first (dshModelPatch), so it is always one path
	// element.
	path, err := writeDshPatch("prov_model", "body-one\n")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "prov_model.yml") {
		t.Fatalf("patch at %q, want %q", path, filepath.Join(dir, "prov_model.yml"))
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "body-one\n" {
		t.Fatalf("first write wrong: %q, %v", body, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("patch mode %v, want 0600", fi.Mode().Perm())
	}

	// The cache is shared across processes: a rewrite must replace the file
	// whole (rename) rather than truncate it in place, so a concurrent reader
	// never sees a half-written overlay.
	dshPatchMu.Lock()
	delete(dshPatches, "prov_model")
	dshPatchMu.Unlock()
	if _, err := writeDshPatch("prov_model", "body-two\n"); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "body-two\n" {
		t.Fatalf("overwrite lost or torn: %q, %v", body, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".prov_model.yml-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestToolBins(t *testing.T) {
	got := ToolBins([]string{"rg", "ast-grep|sg", "ast-grep|sg", "shellcheck"})
	want := []string{"rg", "ast-grep", "sg", "shellcheck"}
	if !slices.Equal(got, want) {
		t.Fatalf("ToolBins = %v, want %v", got, want)
	}
	if got := ToolBins(nil); got != nil {
		t.Fatalf("no entries should give no bins, got %v", got)
	}
}

func TestSplitTools(t *testing.T) {
	found := map[string]string{"ast-grep": "/usr/bin/ast-grep"}
	have, missing := SplitTools([]string{"rg", "ast-grep|sg"}, found)
	// The alternative pair counts as present through its second name, and is
	// reported by the first, which is what the rules call it.
	if !slices.Equal(have, []string{"ast-grep"}) {
		t.Fatalf("have = %v, want [ast-grep]", have)
	}
	if !slices.Equal(missing, []string{"rg"}) {
		t.Fatalf("missing = %v, want [rg]", missing)
	}
	// An entry present in the map with an empty path counts as absent.
	have, missing = SplitTools([]string{"rg", "patchwork"},
		map[string]string{"rg": "", "patchwork": "/usr/bin/patchwork"})
	if len(have) != 1 || have[0] != "patchwork" || len(missing) != 1 || missing[0] != "rg" {
		t.Fatalf("empty-path resolution must count as missing: have=%v missing=%v", have, missing)
	}
	have, missing = SplitTools(nil, found)
	if have != nil || missing != nil {
		t.Fatalf("no entries should split to nothing, got %v and %v", have, missing)
	}
}

func TestToolsForOrderAndAlts(t *testing.T) {
	// The core tools come first (git excluded: it is the runner's own tool),
	// then the review's own helpers in catalog order, duplicates dropped.
	got := ToolsFor("config-review")
	want := []string{"rg", "ast-grep|sg", "patchwork", "semcode",
		"check-jsonschema", "yamllint", "taplo", "dotenv-linter",
		"editorconfig-checker|ec", "shfmt"}
	if !slices.Equal(got, want) {
		t.Fatalf("ToolsFor(config-review) = %v, want %v", got, want)
	}
	if got := ToolsFor("agentrules-review"); slices.Contains(got, "git") {
		t.Fatalf("git must not be offered to agents: %v", got)
	}
}
