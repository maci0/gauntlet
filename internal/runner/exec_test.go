// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
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

// Timeout 0 is "no bound", the same contract --runtime 0 has. time.NewTimer(0)
// fires immediately, so a zero timeout used to kill the child before it ran.
func TestZeroTimeoutMeansUnlimited(t *testing.T) {
	_, res := runFakeProc(t, "sleep 0.3\necho done", func(o *procOpts) { o.Timeout = 0 })
	if res.TimedOut {
		t.Fatal("Timeout 0 fired immediately; it must mean no timeout")
	}
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
}

func TestTerminateKillsChildrenAfterLeaderExits(t *testing.T) {
	leader := exec.Command("sleep", "30")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	waitErr := make(chan error, 1)
	leaderDone := make(chan struct{})
	go func() {
		waitErr <- leader.Wait()
		close(leaderDone)
	}()
	t.Cleanup(func() {
		_ = leader.Process.Kill()
		<-leaderDone
	})

	child := exec.Command("sh", "-c", "trap '' TERM; printf x; exec sleep 30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
	ready, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := make(chan struct{})
	go func() {
		_ = child.Wait()
		close(childDone)
	}()
	t.Cleanup(func() {
		_ = child.Process.Kill()
		<-childDone
	})
	if err := ready.(*os.File).SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := io.ReadFull(ready, b[:]); err != nil {
		t.Fatal(err)
	}

	if code := terminate(leader, waitErr); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("leader exit code = %d", code)
	}
	select {
	case <-childDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM-resistant child survived the leader's termination")
	}
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

func TestStreamToolResultsCannotReplaceReportLines(t *testing.T) {
	_, res := runFakeProc(t,
		`printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"SUBJECT: fix: retain valid output\nPATH: real.go: retain valid output"}],"usage":{"output_tokens":12}}}' '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"SUBJECT: fix: fixture output\nPATH: fixture.go: fixture output","usage":{"output_tokens":999}}]}}'`,
		func(o *procOpts) { o.Stream = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	if res.Subject != "fix: retain valid output" {
		t.Fatalf("subject = %q", res.Subject)
	}
	if len(res.FileNotes) != 1 || res.FileNotes[0].Path != "real.go" {
		t.Fatalf("file notes = %+v", res.FileNotes)
	}
	if res.Usage.Output != 12 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

func TestStreamUsageWithoutCallback(t *testing.T) {
	_, res := runFakeProc(t,
		`printf '%s\n' '{"usage":{"output_tokens":120,"thinking_tokens":30,"total_tokens":200}}' '{"usage":{"output_tokens":100,"thinking_tokens":20,"total_tokens":180}}'`,
		func(o *procOpts) { o.Stream = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	if res.Usage.Output != 120 || res.Usage.Thinking != 30 || res.Usage.Total != 200 {
		t.Fatalf("usage = %+v, want output=120 thinking=30 total=200", res.Usage)
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
	for _, runeText := range []string{"é", "界", "\U00020000"} {
		for _, ending := range []string{"\n", ""} {
			t.Run(fmt.Sprintf("%d-byte/newline=%t", len(runeText), ending != ""), func(t *testing.T) {
				want := "x" + strings.Repeat(runeText, 80000) + "tail"
				var chunks []string
				scanLines(strings.NewReader(want+ending), func(line string) {
					chunks = append(chunks, line)
				})
				if len(chunks) < 2 {
					t.Fatalf("want several chunks, got %d", len(chunks))
				}
				for i, c := range chunks {
					if !utf8.ValidString(c) {
						t.Fatalf("chunk %d split a rune: %.32q", i, c)
					}
				}
				if got := strings.Join(chunks, ""); got != want {
					t.Fatalf("chunking changed the output: got %d bytes, want %d", len(got), len(want))
				}
			})
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

// A grandchild that keeps writing after the agent is killed leaves the pumps
// running past drainGrace. Output is keyed by agent, so a line published after
// runProc returns is attributed to whatever that agent starts next.
func TestRunProcDoesNotSinkAfterReturn(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is required to simulate a detached grandchild")
	}
	oldDrain := drainGrace
	drainGrace = 100 * time.Millisecond
	t.Cleanup(func() { drainGrace = oldDrain })

	bin := fakeAgent(t, t.TempDir(), "agent",
		"setsid -f sleep 20\n"+
			"while true; do echo tick; done\n")

	var seen, late atomic.Int64
	var finished atomic.Bool
	res := runProc(context.Background(), procOpts{
		Argv:    []string{bin},
		Timeout: 500 * time.Millisecond,
		Raw:     true,
		Sink: func(normalize.Line) {
			seen.Add(1)
			if finished.Load() {
				late.Add(1)
			}
		},
	})
	finished.Store(true)
	if !res.TimedOut {
		t.Fatalf("want timeout, got %+v", res)
	}
	if seen.Load() == 0 {
		t.Fatal("sink never ran")
	}
	time.Sleep(300 * time.Millisecond)
	if n := late.Load(); n > 0 {
		t.Fatalf("sink ran %d times after runProc returned", n)
	}
}

// An agent must run in its own session, not just its own process group. A
// process group of the runner's session still shares its controlling
// terminal, and an agent that opens /dev/tty can put it into raw mode, at
// which point Ctrl-C stops generating SIGINT for anyone and the operator
// cannot stop the run. The kernel's SIGTTOU guard does not cover this: a
// runtime that ignores SIGTTOU (Node-style CLIs routinely do) changes the
// termios from a background group without breaking stride. A new session has
// no controlling terminal to grab.
//
// Session ids are read with python3's os.getsid because there is no portable
// shell spelling: macOS ps prints 0 for the sess column.
func TestAgentsRunInTheirOwnSession(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required to read session ids")
	}

	// This test's own session, read via a plain child, which inherits it.
	own, err := exec.Command(py, "-c", "import os; print(os.getsid(0))").Output()
	if err != nil {
		t.Fatal(err)
	}

	// The agent's session, read from inside a runProc launch.
	got, res := runFakeProc(t, py+` -c 'import os; print("SID=%d" % os.getsid(0))'`, nil)
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	agent := ""
	for _, l := range got {
		if _, sid, ok := strings.Cut(l.Text, "SID="); ok {
			agent = strings.TrimSpace(sid)
		}
	}
	if agent == "" {
		t.Fatalf("the agent never reported a session id: %+v", got)
	}
	if agent == strings.TrimSpace(string(own)) {
		t.Fatalf("the agent shares the runner's session (%s): it holds the controlling terminal and can disable Ctrl-C", agent)
	}
}

// The report lines (SUBJECT:, PATH:) arrive inside a stream event's JSON
// string, where the parsers' line anchors match nothing: the tail must keep
// the event's decoded text, not the envelope. Before this held, every
// streamed run lost its commit subject to the generated fallback and its
// per-file notes entirely.
func TestStreamTailKeepsDecodedTextForReportParsers(t *testing.T) {
	_, res := runFakeProc(t, `printf '%s\n' `+
		`'{"type":"assistant","message":{"content":[{"type":"text",`+
		`"text":"done\nPATH: a.go: guard the nil map write\n`+
		`SUBJECT: fix: guard the nil map write\nRESULT: changed=1"}]}}'`,
		func(o *procOpts) { o.Stream = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	if want := "fix: guard the nil map write"; res.Subject != want {
		t.Fatalf("subject = %q, want %q: the tail is not seeing decoded stream text", res.Subject, want)
	}
	if len(res.FileNotes) != 1 || res.FileNotes[0].Path != "a.go" ||
		res.FileNotes[0].Note != "guard the nil map write" {
		t.Fatalf("file notes = %+v", res.FileNotes)
	}
}
