// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/streamjson"
)

// killGrace is how long a process group gets between SIGTERM and SIGKILL.
const killGrace = 10 * time.Second

// drainGrace is how long the output readers get to see EOF after the child
// is gone before runProc returns and closes their descriptors out from under
// them. A var only so tests can shrink it; production always sees the
// default.
var drainGrace = 5 * time.Second

// maxLineBytes bounds one logical line handed to the parsers. Agent output is
// untrusted, and a single physical line can be megabytes (a minified bundle,
// an embedded tool result). Above the cap a line is emitted in chunks rather
// than ending the read: a reader that gives up on an overlong line stops
// draining the pipe, the child then blocks on write and sits there until the
// timeout kills it, so one huge line would burn the review's whole budget on
// display plumbing. A var only so tests can shrink it; production always
// sees the default.
var maxLineBytes = 4 << 20

// streamLineCols is the width cap for one line of agent output, shared by the
// normalizer and by stream events that bypass it (thinking lines, raw echo).
const streamLineCols = 2000

// procResult is the outcome of one agent process.
type procResult struct {
	ExitCode int // -1 when the process was signalled or never exited cleanly
	TimedOut bool
	Canceled bool
	Usage    agent.Usage
	Err      error // launch failure only
}

// procOpts configures one agent launch.
type procOpts struct {
	Argv    []string
	Dir     string
	Timeout time.Duration
	// Sink receives normalized output lines. nil discards output (still
	// drained, so the agent never blocks on a full pipe).
	Sink func(normalize.Line)
	// Raw echoes output verbatim instead of normalizing it.
	Raw bool
	// Stream says the agent was asked for machine-readable output, so lines
	// are parsed as JSON events before falling back to text normalization.
	Stream bool
	// MaxLinesPerSec bounds the sink; 0 is unlimited.
	MaxLinesPerSec int
	// Usage is called whenever the agent reports a larger token count than
	// before, so a dashboard can show throughput while the agent is still
	// running. Agents that report usage only at exit simply call it once.
	Usage func(agent.Usage)
}

// runProc launches one agent, streams its output, and enforces the timeout.
//
// The child runs in its own process group so a timeout can kill the whole
// tree, not just the launcher. stdin is /dev/null: agents run headless and
// must never read the terminal, since an agent that grabs it can put the
// shared tty in raw mode and stop Ctrl+C from generating a signal at all.
func runProc(ctx context.Context, o procOpts) procResult {
	if len(o.Argv) == 0 {
		return procResult{ExitCode: -1, Err: errors.New("empty command")}
	}
	cmd := exec.Command(o.Argv[0], o.Argv[1:]...)
	cmd.Dir = o.Dir
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Explicitly owned pipes, not cmd.StdoutPipe: Wait closes the pipes it
	// created as soon as the process exits, which silently truncates whatever
	// the readers had not consumed yet (the usage line lives at the very end,
	// so it is exactly what gets lost). Here the parent owns the read ends and
	// Wait cannot touch them.
	outR, outW, err := os.Pipe()
	if err != nil {
		return procResult{ExitCode: -1, Err: err}
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return procResult{ExitCode: -1, Err: err}
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return procResult{ExitCode: -1, Err: err}
	}
	// The child holds its own copies now; the parent must drop the write ends
	// or the readers never see EOF.
	outW.Close()
	errW.Close()
	defer func() {
		outR.Close()
		errR.Close()
	}()

	tail := agent.NewTail(agent.TailBytes)
	var tailMu sync.Mutex
	var wg sync.WaitGroup

	// Live usage tracking. Counters an agent prints are cumulative within a
	// run, so the maximum seen so far is the current value.
	var usageMu sync.Mutex
	live := agent.Usage{Output: -1, Thinking: -1, Total: -1}
	report := func(u agent.Usage) {
		if o.Usage == nil || !u.Known() {
			return
		}
		usageMu.Lock()
		grew := false
		if u.Output > live.Output {
			live.Output, grew = u.Output, true
		}
		if u.Total > live.Total {
			live.Total, grew = u.Total, true
		}
		if u.Thinking > live.Thinking {
			live.Thinking, grew = u.Thinking, true
		}
		snapshot := live
		usageMu.Unlock()
		if grew {
			o.Usage(snapshot)
		}
	}
	observe := func(line string) {
		if o.Usage == nil || !agent.MayCarryUsage(line) {
			return
		}
		report(agent.ParseUsage([]byte(line)))
	}

	pump := func(r io.Reader) {
		defer wg.Done()
		norm := normalize.New(normalize.Config{
			MaxLinesPerSec: o.MaxLinesPerSec,
			MaxWidth:       streamLineCols,
		})
		emit := func(l normalize.Line) {
			if o.Sink != nil {
				o.Sink(l)
			}
		}
		handle := func(line string) {
			tailMu.Lock()
			_, _ = tail.WriteString(line)
			_, _ = tail.WriteString("\n")
			tailMu.Unlock()

			// In stream mode the agent emits JSON events, which carry usage and
			// separate reasoning from visible output. A line that is not JSON
			// (a warning, a crash) still goes through text normalization, so
			// nothing is swallowed.
			if o.Stream && !o.Raw {
				if ev, ok := streamjson.Parse([]byte(line)); ok {
					report(agent.Usage{
						Output:   pick(ev.Usage.Output),
						Thinking: pick(ev.Usage.Thinking),
						Total:    pick(ev.Usage.Total),
					})
					emitStream(ev, norm, emit)
					return
				}
			}

			observe(line)
			if o.Sink == nil {
				return
			}
			if o.Raw {
				// Verbatim means the visible characters survive untouched; the
				// escape sequences and controls that could drive or spoof the
				// terminal do not. Agent output is untrusted, and this line is
				// headed for a terminal (or a log file) as-is.
				emit(normalize.Line{Text: normalize.Display(line), Repeat: 1})
				return
			}
			for _, l := range norm.Push(line) {
				emit(l)
			}
		}
		scanLines(r, handle)
		for _, l := range norm.Flush() {
			emit(l)
		}
	}
	wg.Add(2)
	go pump(outR)
	go pump(errR)

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	var res procResult
	timer := time.NewTimer(o.Timeout)
	defer timer.Stop()

	select {
	case err := <-waitErr:
		res.ExitCode = exitCode(cmd, err)
	case <-timer.C:
		res.TimedOut = true
		res.ExitCode = terminate(cmd, waitErr)
	case <-ctx.Done():
		res.Canceled = true
		res.ExitCode = terminate(cmd, waitErr)
	}

	// EOF follows the child and, via the group kill, its children. A
	// grandchild that somehow holds a pipe open only costs this grace period:
	// the deferred closes below evict any read still parked on these
	// descriptors, so neither the pumps nor this function outlive it.
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(drainGrace):
	}

	tailMu.Lock()
	final := agent.ParseUsage(tail.Bytes())
	tailMu.Unlock()
	usageMu.Lock()
	res.Usage = agent.Usage{
		Output:   max(final.Output, live.Output),
		Thinking: max(final.Thinking, live.Thinking),
		Total:    max(final.Total, live.Total),
	}
	usageMu.Unlock()
	return res
}

// pick maps "absent" (zero) onto the -1 that agent.Usage uses for unknown.
func pick(n int) int {
	if n <= 0 {
		return -1
	}
	return n
}

// scanLines splits r into lines and hands each to handle. It is bufio.Scanner
// with ScanLines plus one difference: a line past maxLineBytes is emitted in
// bounded chunks instead of ending the read (see maxLineBytes). A trailing
// carriage return is dropped like ScanLines would; a final line without a
// newline still arrives.
func scanLines(r io.Reader, handle func(line string)) {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	emit := func(b []byte) {
		handle(strings.TrimSuffix(string(b), "\r"))
	}
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			// The line continues past the read buffer: accumulate until it
			// ends or reaches the cap, whichever comes first. A chunk ends at
			// the last rune start so a multibyte character is never split
			// across chunks; bytes that decode as nothing (binary garbage)
			// split as-is rather than buffering forever.
			buf = append(buf, chunk...)
			if len(buf) >= maxLineBytes {
				cut := len(buf)
				if r, _ := utf8.DecodeLastRune(buf); r == utf8.RuneError {
					p := len(buf) - 1
					for p > 0 && len(buf)-p < utf8.UTFMax && !utf8.RuneStart(buf[p]) {
						p--
					}
					if p > 0 && utf8.RuneStart(buf[p]) {
						cut = p
					}
				}
				emit(buf[:cut])
				buf = append(buf[:0], buf[cut:]...)
			}
			continue
		}
		if len(chunk) > 0 {
			if chunk[len(chunk)-1] == '\n' {
				if len(buf) == 0 {
					emit(chunk[:len(chunk)-1])
				} else {
					buf = append(buf, chunk[:len(chunk)-1]...)
					emit(buf)
					buf = buf[:0]
				}
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err != nil {
			// EOF or a failed read: whatever is left in the buffer is the
			// stream's last, unterminated line.
			if len(buf) > 0 {
				emit(buf)
			}
			return
		}
	}
}

// emitStream turns one parsed event into feed lines: reasoning first, then
// visible output. Reasoning lines bypass the normalizer's clean() (they are
// already the text of a JSON event, not terminal output), so they are
// sanitized and width-capped here; visible output still goes through the
// normalizer so duplicates and noise collapse the same way they do for text.
func emitStream(ev streamjson.Event, norm *normalize.Normalizer, emit func(normalize.Line)) {
	for l := range strings.SplitSeq(ev.Thinking, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		emit(normalize.Line{
			Text:   normalize.Truncate(normalize.Display(l), streamLineCols),
			Kind:   normalize.Thinking,
			Repeat: 1,
		})
	}
	for l := range strings.SplitSeq(ev.Text, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		for _, out := range norm.Push(l) {
			emit(out)
		}
	}
}

// terminate kills the child's process group, escalating to SIGKILL, and
// returns the resulting exit code.
func terminate(cmd *exec.Cmd, waitErr <-chan error) int {
	killGroup(cmd, syscall.SIGTERM)
	select {
	case err := <-waitErr:
		return exitCode(cmd, err)
	case <-time.After(killGrace):
	}
	killGroup(cmd, syscall.SIGKILL)
	select {
	case err := <-waitErr:
		return exitCode(cmd, err)
	case <-time.After(killGrace):
		// Unreapable (uninterruptible I/O): abandon it rather than block the
		// loop forever.
		return -1
	}
}

// killGroup signals the whole process group, falling back to the pid when the
// group is already gone.
func killGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil {
		if err := syscall.Kill(-pgid, sig); err == nil {
			return
		}
	}
	_ = cmd.Process.Signal(sig)
}

func exitCode(cmd *exec.Cmd, err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		if ee.ExitCode() >= 0 {
			return ee.ExitCode()
		}
	}
	if cmd.ProcessState != nil {
		if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return -1
}

// suggestTailBytes bounds what the triage step keeps of an agent's output.
// The RELEVANT lines are pinned to the very end by the prompt, and dozens of
// them at ~100 bytes each sit far below this, so nothing parseable is lost
// while memory stays flat no matter how much the agent prints.
const suggestTailBytes = 1 << 20

// captureProc runs a command to completion and returns the tail of its stdout.
// Used for the short helper steps whose output is parsed rather than streamed:
// today that is the suggest triage. The bound is what makes an output loop in
// the child a bounded annoyance instead of unbounded memory growth.
func captureProc(ctx context.Context, argv []string, dir string, timeout time.Duration) (string, procResult) {
	tail := agent.NewTail(suggestTailBytes)
	var mu sync.Mutex
	res := runProc(ctx, procOpts{
		Argv:    argv,
		Dir:     dir,
		Timeout: timeout,
		Raw:     true,
		Sink: func(l normalize.Line) {
			mu.Lock()
			_, _ = tail.WriteString(l.Text)
			_, _ = tail.WriteString("\n")
			mu.Unlock()
		},
	})
	mu.Lock()
	defer mu.Unlock()
	return string(tail.Bytes()), res
}
