// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
)

// runFakeProc runs one fake agent and returns every line its sink received.
func runFakeProc(t *testing.T, body string, tune func(*procOpts)) ([]normalize.Line, procResult) {
	t.Helper()
	bin := fakeAgent(t, t.TempDir(), "agent", body)
	var got []normalize.Line
	opts := procOpts{
		Argv:    []string{bin},
		Timeout: 30 * time.Second,
		Sink:    func(l normalize.Line) { got = append(got, l) },
	}
	if tune != nil {
		tune(&opts)
	}
	return got, runProc(context.Background(), opts)
}

func TestRawOutputCannotDriveTheTerminal(t *testing.T) {
	got, res := runFakeProc(t,
		`printf '\033[31mRED\033[0m plain\n'`,
		func(o *procOpts) { o.Raw = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 line, got %d: %+v", len(got), got)
	}
	if want := "RED plain"; got[0].Text != want {
		t.Fatalf("got %q, want %q", got[0].Text, want)
	}
}

func TestStreamThinkingCannotDriveTheTerminal(t *testing.T) {
	got, res := runFakeProc(t,
		`printf '%s\n' '{"type":"thinking","thinking":"step \u001b]0;pwned\u0007one"}'`,
		func(o *procOpts) { o.Stream = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	var think []normalize.Line
	for _, l := range got {
		if l.Kind == normalize.Thinking {
			think = append(think, l)
		}
	}
	if len(think) != 1 {
		t.Fatalf("want 1 thinking line, got %d in %+v", len(think), got)
	}
	if want := "step one"; think[0].Text != want {
		t.Fatalf("got %q, want %q", think[0].Text, want)
	}
}

func TestOverlongLineDoesNotStallTheStream(t *testing.T) {
	// One physical line past maxLineBytes used to end the read: the child
	// then blocked on a full pipe until the timeout killed it. The whole
	// stream must survive instead.
	got, res := runFakeProc(t,
		`{ printf 'x'; head -c 5000000 /dev/zero | tr '\0' 'x'; printf '\nafter\nRESULT: ok\n'; }`,
		func(o *procOpts) { o.Timeout = 15 * time.Second })
	if res.Err != nil || res.ExitCode != 0 || res.TimedOut || res.Canceled {
		t.Fatalf("run failed: %+v", res)
	}
	sawAfter, sawResult, sawBlob := false, false, false
	for _, l := range got {
		switch l.Text {
		case "after":
			sawAfter = true
		case "RESULT: ok":
			sawResult = true
		default:
			if strings.HasPrefix(l.Text, "xxxx") {
				sawBlob = true
			}
		}
	}
	if !sawBlob {
		t.Fatalf("the oversized line never surfaced: %d lines", len(got))
	}
	if !sawAfter || !sawResult {
		t.Fatalf("output after the oversized line was lost (after=%t result=%t): %d lines",
			sawAfter, sawResult, len(got))
	}
}

// A chunk boundary must land between UTF-8 sequences, not inside one: the
// chunks go to display and JSON parsing downstream, where a split character
// turns into replacement garbage.
func TestOverlongLineChunksOnRuneBoundary(t *testing.T) {
	old := maxLineBytes
	maxLineBytes = 16
	defer func() { maxLineBytes = old }()

	// One ASCII byte shifts every following 2-byte rune by an odd offset, so
	// the flush at the cap lands strictly inside a rune. The line must also
	// exceed scanLines' own read buffer for the chunking path to run at all.
	line := "x" + strings.Repeat("é", 80000) + "\n"
	var chunks []string
	scanLines(strings.NewReader(line), func(line string) {
		chunks = append(chunks, line)
	})
	if len(chunks) < 2 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if !utf8.ValidString(c) {
			t.Fatalf("chunk %d split a rune: %q...", i, c[:32])
		}
	}
}

func TestCaptureProcKeepsTheTailOfHugeOutput(t *testing.T) {
	// The triage protocol lines sit at the very end; an output loop before
	// them must not cost the suggestion or unbounded memory.
	bin := fakeAgent(t, t.TempDir(), "agent",
		`{ head -c 3000000 /dev/zero | tr '\0' 'n'; printf '\nRELEVANT: sec-review: handles secrets\n'; }`)
	out, res := captureProc(context.Background(), []string{bin}, t.TempDir(), 30*time.Second)
	if res.Err != nil || res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("run failed: %+v", res)
	}
	picked, _ := prompt.ParseSuggestions(out, []string{"sec-review"})
	if len(picked) != 1 || picked[0].Name != "sec-review" {
		t.Fatalf("tail lost the suggestion: %+v", picked)
	}
}

func TestRunProcPumpsEndWhenAGrandchildHoldsThePipe(t *testing.T) {
	// An agent that daemonizes a helper (setsid escapes the process group)
	// leaves it holding the pipe's write end, so no EOF ever arrives. The
	// deferred closes must still evict the reads parked on our ends: without
	// that, both pump goroutines outlive runProc by as long as the escapee
	// lives, one pair per such review.
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is required to simulate a detached grandchild")
	}
	oldDrain := drainGrace
	drainGrace = 250 * time.Millisecond
	t.Cleanup(func() { drainGrace = oldDrain })

	// setsid -f forks immediately, so the sleeping grandchild is in a session
	// of its own while the direct child stays killable by the group SIGTERM.
	bin := fakeAgent(t, t.TempDir(), "agent", "setsid -f sleep 20\nexec sleep 20\n")

	before := runtime.NumGoroutine()
	res := runProc(context.Background(), procOpts{
		Argv:    []string{bin},
		Timeout: 500 * time.Millisecond,
	})
	if !res.TimedOut {
		t.Fatalf("want timeout, got %+v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pump goroutines outlived runProc: %d goroutines now, %d before",
		runtime.NumGoroutine(), before)
}
