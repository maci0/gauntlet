// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"
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

func TestLabels(t *testing.T) {
	got := Labels([]Spec{
		{Tool: "claude"},
		{Tool: "codex", Model: "gpt-5"},
		{Tool: "grok", Effort: "high"},
	})
	want := []string{"claude", "codex:gpt-5", "grok@high"}
	if !slices.Equal(got, want) {
		t.Fatalf("Labels() = %v, want %v", got, want)
	}
	if n := len(Labels(nil)); n != 0 {
		t.Fatalf("Labels(nil) length = %d, want 0", n)
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

	t.Setenv("GAUNTLET_TEST_MISSING", "x")
	os.Unsetenv("GAUNTLET_TEST_MISSING")
	if _, _, err := ParseBin("claude=$GAUNTLET_TEST_MISSING"); err == nil ||
		!strings.Contains(err.Error(), "GAUNTLET_TEST_MISSING") {
		t.Fatalf("unset $VAR must be refused, got %v", err)
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
	f.Add("SUBJECT: fix: \u202Eevil\u202C and \u2028break")
	f.Add("PATH: a.go\nSUBJECT:   \nSUBJECT: chore: the last one wins\n")
	f.Add("SUBJECT: " + strings.Repeat("你", 50) + "\n")
	f.Add("SUBJECT: fix: \u202eevil\u202c\n")
	f.Fuzz(func(t *testing.T, tail string) {
		got := ParseSubject([]byte(tail))
		if n := utf8.RuneCountInString(got); n > subjectMax {
			t.Fatalf("subject is %d runes, want at most %d: %q", n, subjectMax, got)
		}
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("a subject spanning lines can forge a commit body: %q", got)
		}
		for _, r := range got {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
				unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
				t.Fatalf("formatting rune %q survived into %q", r, got)
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
	if got != "fix: thing[31m and more" {
		t.Fatalf("printable text did not survive sanitizing: %q", got)
	}
	bidi := ParseSubject([]byte("SUBJECT: fix: \u202Eevil\u202C good\n"))
	if strings.ContainsRune(bidi, '\u202E') || strings.ContainsRune(bidi, '\u202C') {
		t.Fatalf("bidi override survived into a commit subject: %q", bidi)
	}
	if !strings.Contains(bidi, "evil") || !strings.Contains(bidi, "good") {
		t.Fatalf("visible text was lost with the bidi marks: %q", bidi)
	}
	sep := ParseSubject([]byte("SUBJECT: fix: a\u2028b\u2029c\n"))
	if strings.ContainsAny(sep, "\u2028\u2029") {
		t.Fatalf("line/paragraph separator survived into a commit subject: %q", sep)
	}
	got = ParseSubject([]byte("SUBJECT: fix: \u202eevil\u202c\n"))
	if strings.ContainsRune(got, '\u202e') || strings.ContainsRune(got, '\u202c') {
		t.Fatalf("bidi override survived into a commit subject: %q", got)
	}
	if got != "fix: evil" {
		t.Fatalf("visible text did not survive stripping format runes: %q", got)
	}
	long := "SUBJECT: " + strings.Repeat("x", 500) + "\n"
	got = ParseSubject([]byte(long))
	if n := utf8.RuneCountInString(got); n > subjectMax {
		t.Fatalf("subject is %d runes, want at most %d", n, subjectMax)
	}
	if got != strings.Repeat("x", subjectMax) {
		t.Fatalf("truncation dropped the printable prefix: %q", got)
	}
}

// The cap is in runes, matching every other display limit here. A byte
// reading of subjectMax would cut a 40-character CJK subject (120 bytes)
// even though it is well under 100 code points.
func TestParseSubjectLengthIsRunesNotBytes(t *testing.T) {
	subject := strings.Repeat("你", 40)
	if len(subject) <= subjectMax {
		t.Fatalf("fixture is only %d bytes; it no longer exceeds a byte reading of the cap", len(subject))
	}
	got := ParseSubject([]byte("SUBJECT: " + subject + "\n"))
	if got != subject {
		t.Fatalf("a 40-rune CJK subject was mangled: got %q", got)
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

// FuzzParseDshProvider drives the dump-config parser with arbitrary CLI
// output. The captured provider is interpolated into a YAML overlay quoted
// scalar, so a value outside the charset the regex admits would break out of
// that quote or become a different file than dshPatchKey names.
func FuzzParseDshProvider(f *testing.F) {
	seeds := []string{
		`
plugins:
  - id: other-plugin
    provider: 'nope'
  - id: agent-default-model
    provider: "deepseek"
    model: 'chat'
`,
		"  - id: agent-default-model\n    provider: openai\n",
		"  - id: agent-default-model\n    provider: 'deep-seek.v1_2'\n",
		"- id: agent-default-model\nprovider: 'nope'\n",
		"  - id: agent-default-model\n    provider: \"x\"\n  - id: other\n    provider: y\n",
		"  - id: agent-default-model\n    model: chat\n",
		"provider: 'openai'\n  - id: agent-default-model\n",
		"  - id: agent-default-model\n    provider: 'o'penai'\n",
		"  - id: agent-default-model\r\n    provider: grok\r\n",
		"  - id: agent-default-model\n    provider: \n",
		"",
		strings.Repeat("  - id: other\n    provider: x\n", 40) +
			"  - id: agent-default-model\n    provider: last\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, dump string) {
		got := parseDshProvider(dump)
		if again := parseDshProvider(dump); again != got {
			t.Fatalf("parseDshProvider is not deterministic for %q: %q vs %q", dump, got, again)
		}
		if got == "" {
			return
		}
		if !dshProviderCharset(got) {
			t.Fatalf("provider %q is outside the overlay charset", got)
		}
		if strings.ContainsAny(got, "'\"\n\r:") {
			t.Fatalf("provider %q would break the YAML overlay quote", got)
		}
		if !strings.Contains(dump, "agent-default-model") {
			t.Fatalf("invented provider %q from a dump with no agent-default-model entry", got)
		}
		if !strings.Contains(dump, got) {
			t.Fatalf("invented provider %q, not present in %q", got, dump)
		}
	})
}

// dshProviderCharset is the capture of dshProviderRe: ASCII word characters,
// dots, and hyphens. The overlay quotes this value, so anything else is a
// parse bug rather than a YAML-injection hole.
func dshProviderCharset(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '-':
		default:
			return false
		}
	}
	return true
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

// An argument may carry more than one placeholder, and every one of them has
// to expand. A definition that packs its settings into a single option had
// only the first kind replaced, so the agent was launched with a literal
// "{effort}" (or "{model}") on its command line.
func TestCustomAgentExpandsEveryPlaceholderInOneArgument(t *testing.T) {
	t.Cleanup(resetCustom(t))
	if err := Register("packed", Custom{
		Argv: []string{"packed", "--opts=model={model},effort={effort}", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}
	argv, err := BuildCmd(Spec{Tool: "packed", Model: "m1", Effort: "high"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "packed --opts=model=m1,effort=high -p PROMPT"; strings.Join(argv, " ") != want {
		t.Fatalf("argv:\n got %v\nwant %s", argv, want)
	}

	// The prompt is review text, not a template: a placeholder inside it is
	// content the review wrote and must reach the agent unchanged.
	if err := Register("promptfirst", Custom{
		Argv: []string{"promptfirst", "{prompt}", "--model={model}"},
	}); err != nil {
		t.Fatal(err)
	}
	argv, err = BuildCmd(Spec{Tool: "promptfirst", Model: "m1"},
		"describe {model} in the config", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"promptfirst", "describe {model} in the config", "--model=m1"}; !slices.Equal(argv, want) {
		t.Fatalf("argv:\n got %q\nwant %q", argv, want)
	}
}

// A placeholder the spec never pinned must not reach the agent as an empty
// value. The Model and Effort blocks are simply not appended when there is
// nothing to put in them, and an {model} or {effort} written into argv has to
// degrade the same way: "--model=" is not "no model", it is a model named the
// empty string, which the CLI either rejects or takes.
func TestCustomAgentDropsArgumentsWithNothingToPutInThem(t *testing.T) {
	t.Cleanup(resetCustom(t))
	if err := Register("inargv", Custom{
		Argv: []string{"inargv", "--model={model}", "--effort={effort}", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := Register("blocks", Custom{
		Argv:   []string{"blocks", "-p", "{prompt}"},
		Model:  []string{"--model", "{model}"},
		Effort: []string{"--effort", "{effort}"},
	}); err != nil {
		t.Fatal(err)
	}

	// The two spellings agree with each other, pinned or not.
	for _, c := range []struct {
		spec       Spec
		argv, want string
	}{
		{Spec{Tool: "inargv"}, "inargv", "inargv -p PROMPT"},
		{Spec{Tool: "blocks"}, "blocks", "blocks -p PROMPT"},
		{Spec{Tool: "inargv", Model: "m1"}, "inargv", "inargv --model=m1 -p PROMPT"},
		{Spec{Tool: "inargv", Model: "m1", Effort: "high"}, "inargv",
			"inargv --model=m1 --effort=high -p PROMPT"},
		{Spec{Tool: "inargv", Effort: "high"}, "inargv", "inargv --effort=high -p PROMPT"},
	} {
		argv, err := BuildCmd(c.spec, "PROMPT", BuildOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(argv, " "); got != c.want {
			t.Errorf("BuildCmd(%+v):\n got %s\nwant %s", c.spec, got, c.want)
		}
	}

	// The argument holding the prompt is the task. Dropping it would launch
	// the agent with nothing to do, which is worse than an unexpanded token,
	// so it survives whatever else it mentions.
	if err := Register("packed", Custom{
		Argv: []string{"packed", "-p", "{prompt} (model {model})"},
	}); err != nil {
		t.Fatal(err)
	}
	argv, err := BuildCmd(Spec{Tool: "packed"}, "PROMPT", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"packed", "-p", "PROMPT (model )"}; !slices.Equal(argv, want) {
		t.Fatalf("the prompt argument was not kept intact:\n got %q\nwant %q", argv, want)
	}
}

func TestCustomAgentRejectsBadDefinitions(t *testing.T) {
	t.Cleanup(resetCustom(t))
	cases := map[string]Custom{
		"no argv":                {},
		"no prompt":              {Argv: []string{"x", "--flag"}},
		"usage without roots":    {Argv: []string{"x", "{prompt}"}, Usage: &UsageSpec{}},
		"usage with blank roots": {Argv: []string{"x", "{prompt}"}, Usage: &UsageSpec{Roots: []string{"", "  "}}},
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

func TestCustomAgentFileUsageWithoutRoots(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	body := `{"piclone":{"argv":["piclone","-p","{prompt}"],"usage":{}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	err := LoadCustomFile(path)
	if err == nil || !strings.Contains(err.Error(), "usage.roots") {
		t.Fatalf("want usage.roots refusal naming the file, got %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name %s, got %v", path, err)
	}
}

func TestCustomAgentFileTrailingData(t *testing.T) {
	t.Cleanup(resetCustom(t))
	dir := t.TempDir()
	// A second value, and a leftover closer that Decoder.More does not
	// report (FuzzCustomDefinitions/85197b199bd913d6), must both refuse
	// rather than load as an empty definition set.
	for _, body := range []string{
		`{} {"piclone":{"argv":["x","{prompt}"]}}`,
		`{}}`,
		`{}]`,
	} {
		path := filepath.Join(dir, "agents.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := LoadCustomFile(path); err == nil {
			t.Fatalf("trailing data %q must be rejected, not half-read", body)
		}
	}
}

// FuzzCustomDefinitions feeds unmarshalStrict the JSON operators put in
// agents.json. A definition that survives decoding is what Register would
// exec, so the contract is: decoding is deterministic, trailing data and
// unknown fields stay errors, a valid definition keeps its {prompt} until
// expansion and never expands to an empty argv, and a name with separators
// never validates.
func FuzzCustomDefinitions(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{}`),
		[]byte(`{"piclone":{"argv":["piclone","-p","{prompt}"],"stream":["--jsonl"],"note":"test"}}`),
		[]byte(`{"x":{"argv":["x","{prompt}"],"model":["--model","{model}"],"effort":["--effort","{effort}"],"opt_in":true}}`),
		[]byte(`{"x":{"argv":["x","{prompt}"],"usage":{"roots":["~/.x"],"suffix":".jsonl","cumulative":true,"header_cwd":true}}}`),
		[]byte(`{"x":{"argv":["x","{prompt}"],"optin":true}}`),
		[]byte(`{} {"x":{"argv":["x","{prompt}"]}}`),
		[]byte(`{}}`),
		[]byte(`{}]`),
		[]byte(`{"bad name":{"argv":["x","{prompt}"]}}`),
		[]byte(`{"x":{}}`),
		[]byte(`{"x":{"argv":[]}}`),
		[]byte(`{"x":{"argv":["no-placeholder"]}}`),
		[]byte(`[]`),
		[]byte(`null`),
		[]byte(`{not json`),
		[]byte(``),
		[]byte(`{"x":{"argv":["x","{prompt}"],"usage":{}}}`),
		[]byte(`{"packed":{"argv":["packed","-p","{prompt} (model {model})"]}}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var defs map[string]Custom
		err := unmarshalStrict(data, &defs)
		var again map[string]Custom
		err2 := unmarshalStrict(data, &again)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("unmarshalStrict is not deterministic about errors on %q: %v vs %v", data, err, err2)
		}
		if err != nil {
			return
		}
		if !reflect.DeepEqual(defs, again) {
			t.Fatalf("unmarshalStrict is not deterministic for %q", data)
		}
		var viaStd map[string]Custom
		if json.Unmarshal(data, &viaStd) != nil {
			t.Fatalf("accepted something encoding/json rejects: %q", data)
		}
		for name, def := range defs {
			if err := def.validate(name); err != nil {
				continue
			}
			if name == "" || strings.ContainsAny(name, " \t,:=@") {
				t.Fatalf("validate accepted a name with separators: %q", name)
			}
			foundPrompt := false
			for _, a := range def.Argv {
				if strings.Contains(a, promptPlaceholder) {
					foundPrompt = true
					break
				}
			}
			if !foundPrompt {
				t.Fatalf("validate accepted %q with no %s in argv %q", name, promptPlaceholder, def.Argv)
			}
			argv := buildCustom(def, Spec{Tool: name, Model: "m", Effort: "high"}, "PROMPT",
				BuildOpts{Stream: true, Continue: true})
			if len(argv) == 0 {
				t.Fatalf("valid definition %q expanded to no argv", name)
			}
			if again := buildCustom(def, Spec{Tool: name, Model: "m", Effort: "high"}, "PROMPT",
				BuildOpts{Stream: true, Continue: true}); !slices.Equal(argv, again) {
				t.Fatalf("buildCustom is not deterministic for %q: %q vs %q", name, argv, again)
			}
			joined := strings.Join(argv, "\x00")
			if strings.Contains(joined, promptPlaceholder) {
				t.Fatalf("prompt placeholder survived expansion of %q: %q", name, argv)
			}
		}
	})
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

func TestDshStreamDoesNotPatchCompression(t *testing.T) {
	// toktop reads the default zstd session log, so --stream must not add a
	// compression overlay. A model pin is the only --patch dsh takes.
	plain, err := BuildCmd(Spec{Tool: "dsh"}, "P", BuildOpts{})
	if err != nil {
		t.Fatal(err)
	}
	streamed, err := BuildCmd(Spec{Tool: "dsh"}, "P", BuildOpts{Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain, "--patch") {
		t.Fatalf("no overlay without a model pin: %v", plain)
	}
	if !slices.Equal(plain, streamed) {
		t.Fatalf("--stream changed the argv:\n plain %v\nstream %v", plain, streamed)
	}
}

// isolateDshPatches points UserCacheDir at a fresh tree and empties the
// patched-overlay table, restoring whatever was there when the test ends.
//
// UserCacheDir is $XDG_CACHE_HOME on Linux and $HOME/Library/Caches on
// macOS, so an XDG-only override would skip the overlay tests on Darwin.
// HOME plus XDG_CACHE_HOME covers both: Linux reads the XDG dir, Darwin
// reads $HOME/Library/Caches. Callers use the returned path, not a
// hardcoded suffix.
func isolateDshPatches(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
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
	return dir
}

func TestIsolateDshPatchesUsesUserCacheDir(t *testing.T) {
	dir := isolateDshPatches(t)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != cache {
		t.Fatalf("isolateDshPatches = %q, want UserCacheDir %q", dir, cache)
	}
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
		{"deepseek/chat", "deepseek@chat",
			"- id: agent-default-model\n  config:\n    provider: 'deepseek'\n    model: 'chat'\n"},
		{"openrouter/deepseek/deepseek-chat", "openrouter%2Fdeepseek@deepseek-chat",
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

func TestDshPatchKeyDoesNotCollide(t *testing.T) {
	// Flattening "/" and ":" to "_" made these pairs share one overlay, so
	// the second pin launched with the first pair's provider and model.
	pairs := [][2]string{
		{"foo_bar", "baz"},
		{"foo", "bar_baz"},
		{"foo/bar", "baz"},
		{"foo", "bar/baz"},
		{"a:b", "c"},
		{"a", "b:c"},
	}
	seen := map[string][2]string{}
	for _, p := range pairs {
		key := dshPatchKey(p[0], p[1])
		if prev, ok := seen[key]; ok {
			t.Errorf("dshPatchKey(%q, %q) = %q, same as (%q, %q)",
				p[0], p[1], key, prev[0], prev[1])
		}
		seen[key] = p
	}

	cache := isolateDshPatches(t)
	path1, err := dshModelPatch("foo_bar", "baz")
	if err != nil {
		t.Fatal(err)
	}
	path2, err := dshModelPatch("foo", "bar_baz")
	if err != nil {
		t.Fatal(err)
	}
	if path1 == path2 {
		t.Fatalf("distinct pairs shared overlay %q", path1)
	}
	body1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}
	body2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body1), "provider: 'foo_bar'") || !strings.Contains(string(body1), "model: 'baz'") {
		t.Fatalf("first overlay pinned the wrong pair: %s", body1)
	}
	if !strings.Contains(string(body2), "provider: 'foo'") || !strings.Contains(string(body2), "model: 'bar_baz'") {
		t.Fatalf("second overlay pinned the wrong pair: %s", body2)
	}
	dir := filepath.Join(cache, "gauntlet", "dsh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("overlay dir has %d files, want 2", len(entries))
	}
}

func TestWriteDshPatchRewritesAMissingFile(t *testing.T) {
	isolateDshPatches(t)

	path, err := writeDshPatch("gone", "body-one\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The in-process table still names the path; a cache cleaner (or the
	// user) deleting the file must not keep handing dsh a missing overlay.
	got, err := writeDshPatch("gone", "body-two\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("rewrote at %q, want %q", got, path)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "body-two\n" {
		t.Fatalf("missing file was not rewritten: %q, %v", body, err)
	}
}

func TestWriteDshPatchRejectsAPathKey(t *testing.T) {
	isolateDshPatches(t)
	if _, err := writeDshPatch("a"+string(os.PathSeparator)+"b", "x\n"); err == nil {
		t.Fatal("a key with a path separator must be refused")
	}
	if _, err := writeDshPatch("", "x\n"); err == nil {
		t.Fatal("an empty key must be refused")
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
