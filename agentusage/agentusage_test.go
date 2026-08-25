// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agentusage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWatchReadsATranscriptAndYieldsARate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := t.TempDir()

	dir := filepath.Join(home, ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")

	w := Watch("claude", work, time.Now())
	if w == nil {
		t.Fatal("claude keeps a readable transcript and should be watchable")
	}
	if w.Tool() != "claude" || w.Dir() == "" {
		t.Fatalf("watcher identity wrong: %q %q", w.Tool(), w.Dir())
	}

	write(t, path, line(work, 100))
	first := w.Read()
	if first.Output != 100 {
		t.Fatalf("first reading %+v", first)
	}

	time.Sleep(20 * time.Millisecond)
	write(t, path, line(work, 300))
	second := w.Read()
	if second.Output != 400 {
		t.Fatalf("second reading %+v", second)
	}

	rate, ok := Rate(first, second)
	if !ok || rate <= 0 {
		t.Fatalf("no rate from two growing readings: %v %v", rate, ok)
	}
}

func TestRateRefusesToInvent(t *testing.T) {
	now := time.Now()
	cases := map[string][2]Sample{
		"no time passed":  {{Output: 10, At: now}, {Output: 20, At: now}},
		"no growth":       {{Output: 10, At: now}, {Output: 10, At: now.Add(time.Second)}},
		"counter reset":   {{Output: 50, At: now}, {Output: 10, At: now.Add(time.Second)}},
		"clock went back": {{Output: 10, At: now}, {Output: 20, At: now.Add(-time.Second)}},
	}
	for name, c := range cases {
		if _, ok := Rate(c[0], c[1]); ok {
			t.Errorf("%s: reported a rate it cannot know", name)
		}
	}
}

func TestUnwatchableAgentIsNilNotAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	w := Watch("opencode", t.TempDir(), time.Now())
	if w != nil {
		t.Fatal("opencode keeps no readable transcript; a watcher would be a lie")
	}
	// A nil watcher stays usable, so callers never branch.
	if !w.Read().Empty() || w.Tool() != "" {
		t.Fatal("nil watcher reported something")
	}
	w.Run(context.Background(), time.Millisecond, func(Sample) {
		t.Fatal("nil watcher called back")
	})
}

func TestAgentsAndSupportedAgree(t *testing.T) {
	all := Agents()
	if len(all) < 10 {
		t.Fatalf("expected the full agent list, got %v", all)
	}
	var supported int
	for _, a := range all {
		if Supported(a) {
			supported++
		}
	}
	if supported == 0 {
		t.Fatal("no agent reports usage, which contradicts the whole package")
	}
	// Claims must be specific: these three keep transcripts, opencode does not.
	for _, a := range []string{"claude", "codex", "qwen"} {
		if !Supported(a) {
			t.Errorf("%s should be readable", a)
		}
	}
	if Supported("opencode") {
		t.Error("opencode has no readable transcript and must not claim one")
	}
}

func TestParseStreamHandlesTheDialects(t *testing.T) {
	cases := map[string]struct {
		line       string
		wantOutput int
		wantText   string
	}{
		"anthropic": {`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"output_tokens":42}}}`, 42, "hi"},
		"gemini":    {`{"content":{"parts":[{"text":"ok"}]},"usageMetadata":{"candidatesTokenCount":7}}`, 7, "ok"},
		"openai":    {`{"choices":[{"delta":{"content":"x"}}],"usage":{"completion_tokens":9}}`, 9, "x"},
	}
	for name, c := range cases {
		ev, ok := ParseStream([]byte(c.line))
		if !ok {
			t.Fatalf("%s: rejected valid JSON", name)
		}
		if ev.Output != c.wantOutput || ev.Text != c.wantText {
			t.Errorf("%s: got %+v", name, ev)
		}
	}
	if _, ok := ParseStream([]byte("just some text")); ok {
		t.Error("plain text parsed as an event")
	}
}

func TestStreamFlagsAreReal(t *testing.T) {
	if f := StreamFlags("claude"); len(f) == 0 || !strings.Contains(strings.Join(f, " "), "stream-json") {
		t.Fatalf("claude stream flags: %v", f)
	}
	if f := StreamFlags("codex"); f != nil {
		t.Fatalf("codex has no machine-readable mode; claiming one would break it: %v", f)
	}
}

func TestDiscoverFindsThisMachinesAgentsOrNothing(t *testing.T) {
	// Discovery is environment-dependent: what it must never do is report a
	// process it cannot attribute.
	for _, p := range Discover() {
		if p.PID <= 0 || p.Tool == "" || p.Dir == "" {
			t.Fatalf("incomplete process record: %+v", p)
		}
		if !contains(Agents(), p.Tool) {
			t.Fatalf("discovered an agent nobody knows: %+v", p)
		}
	}
}

func TestLoadDefinitionsTeachesNewAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	body := `{"housebot":{"argv":["housebot","-p","{prompt}"],"usage":{"roots":["` + dir + `/logs"]}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadDefinitions(path); err != nil {
		t.Fatal(err)
	}
	if !contains(Agents(), "housebot") {
		t.Fatal("a defined agent should join the list")
	}
	if !Supported("housebot") {
		t.Fatal("a definition naming a transcript root should be readable")
	}
	if err := LoadDefinitions(filepath.Join(dir, "absent.json")); err != nil {
		t.Fatalf("a missing definitions file is normal: %v", err)
	}
}

func line(cwd string, out int) string {
	return `{"type":"assistant","cwd":"` + cwd +
		`","message":{"usage":{"output_tokens":` + itoa(out) + `}}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func write(t *testing.T, path, l string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(l + "\n"); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}
