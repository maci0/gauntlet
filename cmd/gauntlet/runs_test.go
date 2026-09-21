// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/runner"
)

// TestRunIndexerKillsProcessGroup pins the rule runProc states for agents:
// the deadline must take down the whole process tree, not just the direct
// child. The fixture backgrounds a sleeper and records its pid, so without
// the group kill the grandchild outlives the killed parent and answers a
// liveness probe.
//
// The cancel waits for that pid rather than firing on a fixed timeout. A
// timeout short enough to keep the test quick is also short enough to lose a
// race the test is not about: on macOS the first exec of a freshly written
// script pays a one-time security check, and a 300ms deadline landed before
// the shell had backgrounded anything at all. What that produced was not a
// failure of the group kill but an empty temp directory, and the "did it exit
// nonzero" assertion could not tell the difference, since a launch that never
// happened exits nonzero too.
func TestRunIndexerKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()

	sleeper := filepath.Join(dir, "sleeper")
	writeScript(t, sleeper, "#!/bin/sh\nsleep 30\n")
	pidFile := filepath.Join(dir, "sleeper.pid")
	tree := filepath.Join(dir, "tree")
	writeScript(t, tree, "#!/bin/sh\n\""+sleeper+"\" &\necho $! > \""+pidFile+"\"\nwait\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan int, 1)
	go func() { exited <- runIndexer(ctx, tree, nil, dir) }()

	pid := waitForPid(t, pidFile)
	cancel()
	select {
	case code := <-exited:
		if code == 0 {
			t.Fatal("the indexer reported success after the cancel killed it")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the indexer outlived its cancel")
	}

	// kill(pid, 0) reports whether anything still answers at that pid, on
	// every POSIX target this project ships for, so no /proc walk is needed.
	// A survivor runs sleep 30 and stays alive far longer than this wait; a
	// killed one disappears within it. Its only possible parent (the killed
	// script) died in the same group kill, so it cannot linger unreaped as a
	// zombie and keep the probe green.
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatal("a grandchild of the killed indexer is still alive")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForPid blocks until the fixture has a grandchild to report. The file is
// truncated before it is written, so an unparseable read is "not yet", not a
// broken fixture; only the wait running out says that.
func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 1 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fixture never recorded a sleeper pid in %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// processAlive probes one pid with a zero signal: nil or EPERM means something
// is there, ESRCH means it is gone.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWriteSummaryPreservesInterruptedCount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	j, err := journal.Open(journal.NewRunID(start), start)
	if err != nil {
		t.Fatal(err)
	}
	defer j.CloseQuiet()
	stats := &runner.Stats{}
	stats.Add(runner.Result{Status: runner.StatusOK})
	stats.Add(runner.Result{Status: runner.StatusInterrupted})
	stats.Add(runner.Result{Status: runner.StatusInterrupted})
	writeSummary(j, start, time.Minute, []string{"/project"}, nil,
		[]*dirRun{{stats: stats}}, 130)
	data, err := os.ReadFile(filepath.Join(home, "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["interrupted"]) != "2" || string(fields["reviews"]) != "3" || string(fields["ok"]) != "1" || string(fields["failed"]) != "0" {
		t.Fatalf("unexpected summary counts: %s", data)
	}
}

// A replayed journal reaches a terminal through `gauntlet show`. Event text
// records fragments of a possibly hostile tree verbatim (git merge errors,
// file names), and json.Marshal escapes control bytes but passes bidi
// overrides through as raw UTF-8, so the replay must strip them the way every
// other display surface already does.
// STARTED is a local ISO datetime, not US MM-DD without a year: 01-02 is
// 2 January or 1 February depending on who reads it, and two runs a year
// apart on the same calendar day would otherwise look identical.
func TestRunsListsStartAsISOLocal(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	id := journal.NewRunID(start)
	j, err := journal.Open(id, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(journal.Summary{
		Start: start, End: start.Add(90 * time.Second),
		Dirs: []string{"/tmp/proj"},
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdRuns(&buf, palette{}, 10); code != exitOK {
		t.Fatalf("listing runs should exit %d, got %d", exitOK, code)
	}
	want := start.Local().Format("2006-01-02 15:04:05")
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("STARTED should be ISO local %q, got:\n%s", want, buf.String())
	}
}

func TestRunsFailsWhenOutputCannotBeWritten(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(strconv.FormatBool(populated), func(t *testing.T) {
			t.Setenv("GAUNTLET_HOME", t.TempDir())
			if populated {
				start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
				j, err := journal.Open(journal.NewRunID(start), start)
				if err != nil {
					t.Fatal(err)
				}
				if err := j.Close(journal.Summary{Start: start, End: start.Add(time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
			var rendered bytes.Buffer
			if code := cmdRuns(&rendered, palette{}, 10); code != exitOK {
				t.Fatalf("listing exited %d", code)
			}
			for _, limit := range []int{0, rendered.Len() / 2, rendered.Len() - 1} {
				sink := &listingFailWriter{remaining: limit}
				code, diagnostic := captureStderrFor(t, func() int {
					return cmdRuns(sink, palette{}, 10)
				})
				if code != exitFail || !strings.Contains(diagnostic.String(), "cannot write the run listing: "+io.ErrClosedPipe.Error()) {
					t.Fatalf("limit %d: exit %d, stderr %q", limit, code, diagnostic.String())
				}
				if sink.String() != rendered.String()[:limit] {
					t.Fatalf("limit %d: unexpected partial listing %q", limit, sink.String())
				}
			}
		})
	}
}

type listingFailWriter struct {
	bytes.Buffer
	remaining int
}

func (w *listingFailWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.remaining)
	w.Buffer.Write(p[:n])
	w.remaining -= n
	if n < len(p) {
		return n, io.ErrClosedPipe
	}
	return n, nil
}

func TestRunsListsMeasuredElapsedWhenTheWallClockJumped(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	start := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	id := journal.NewRunID(start)
	j, err := journal.Open(id, start)
	if err != nil {
		t.Fatal(err)
	}
	// NTP stepped the wall clock two hours forward during a 90-minute run.
	// End.Sub(Start) would list 2h30m; the measured elapsed is what Total
	// time printed when the run ended.
	if err := j.Close(journal.Summary{
		Start: start, End: start.Add(2*time.Hour + 90*time.Minute),
		Elapsed: (90 * time.Minute).Seconds(),
		Dirs:    []string{"/tmp/proj"},
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdRuns(&buf, palette{}, 10); code != exitOK {
		t.Fatalf("listing runs should exit %d, got %d", exitOK, code)
	}
	out := buf.String()
	if !strings.Contains(out, "1h30m") {
		t.Fatalf("DURATION should be the measured 1h30m, got:\n%s", out)
	}
	if strings.Contains(out, "2h30m") {
		t.Fatalf("DURATION used the wall-clock span after an NTP step:\n%s", out)
	}
}

func TestRunsListsNAWhenEndPrecedesStart(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	start := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	id := journal.NewRunID(start)
	j, err := journal.Open(id, start)
	if err != nil {
		t.Fatal(err)
	}
	// An old index row with no elapsed_s, and a wall clock that stepped
	// backward: End.Sub(Start) is negative and used to render as 0s.
	if err := j.Close(journal.Summary{
		Start: start, End: start.Add(-time.Hour),
		Dirs: []string{"/tmp/proj"},
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdRuns(&buf, palette{}, 10); code != exitOK {
		t.Fatalf("listing runs should exit %d, got %d", exitOK, code)
	}
	if !strings.Contains(buf.String(), "n/a") {
		t.Fatalf("DURATION should be n/a when the wall clock stepped back, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "0s") {
		t.Fatalf("DURATION must not invent 0s for a negative wall span:\n%s", buf.String())
	}
}

func TestRunsListsAfterDeletedIndex(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	id := journal.NewRunID(start)
	j, err := journal.Open(id, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(journal.Summary{
		Start: start, End: start.Add(90 * time.Second),
		Dirs: []string{"/tmp/proj"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(journal.Home(), "index.jsonl")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdRuns(&buf, palette{}, 10); code != exitOK {
		t.Fatalf("listing after a deleted index should exit %d, got %d", exitOK, code)
	}
	if !strings.Contains(buf.String(), id) {
		t.Fatalf("listing after a deleted index should still name the run:\n%s", buf.String())
	}
}

func TestShowPreservesExactNumbers(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	runID := journal.NewRunID(now)
	j, err := journal.Open(runID, now)
	if err != nil {
		t.Fatal(err)
	}
	defer j.CloseQuiet()
	for _, seed := range []uint64{1<<53 + 1, 18446744073709551615} {
		j.Write(struct {
			Kind    string      `json:"ev"`
			Seed    uint64      `json:"seed"`
			Elapsed json.Number `json:"elapsed_s"`
		}{"run_start", seed, json.Number("1.0000000000000001")})
	}
	j.Flush()
	var buf bytes.Buffer
	if code := cmdShow(&buf, runID); code != exitOK {
		t.Fatalf("show exited %d", code)
	}
	for _, want := range []string{
		`"seed":9007199254740993`,
		`"seed":18446744073709551615`,
		`"elapsed_s":1.0000000000000001`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("show lost %s: %s", want, buf.String())
		}
	}
}

func TestShowSanitizesReplayedEvents(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	runID := "20260826T120000Z-dead"
	dir := filepath.Join(journal.Home(), "runs", "2026-08-26")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Valid JSON escapes: decoding yields a real ESC, BEL, and bidi pair.
	line := `{"ev":"merge","ts":"2026-08-26T12:00:00Z","status":"conflict",` +
		`"text":"CONFLICT in evil\u001b]0;pwned\u0007.md \u202ebad\u202c.md"}`
	if err := os.WriteFile(filepath.Join(dir, runID+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdShow(&buf, runID); code != exitOK {
		t.Fatalf("replaying a recorded run should exit %d, got %d", exitOK, code)
	}
	out := buf.String()
	for _, bad := range []string{"\x1b", "\x07", "\u202e", "\u202c"} {
		if strings.Contains(out, bad) {
			t.Errorf("replay emitted %q to the terminal: %q", bad, out)
		}
	}
	if !strings.Contains(out, "CONFLICT") || !strings.Contains(out, "evil") {
		t.Errorf("replay lost the readable content: %q", out)
	}
}

func TestShowReportsOutputFailure(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	start := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	id := journal.NewRunID(start)
	j, err := journal.Open(id, start)
	if err != nil {
		t.Fatal(err)
	}
	j.Write(map[string]string{"ev": "log", "text": "replay output"})
	if err := j.Close(journal.Summary{Start: start, End: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	var rendered bytes.Buffer
	if code := cmdShow(&rendered, id); code != exitOK {
		t.Fatalf("replay exited %d, want %d", code, exitOK)
	}
	if want := "  log           {\"text\":\"replay output\"}\n"; rendered.String() != want {
		t.Fatalf("replay output = %q, want %q", rendered.String(), want)
	}
	out, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if code := cmdShow(out, id); code != exitFail {
		t.Fatalf("failed replay output exited %d, want %d", code, exitFail)
	}
}

func TestShowRendersEventTimeFromOffsetStamp(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	runID := "20261101T063000Z-dead"
	dir := filepath.Join(journal.Home(), "runs", "2026-11-01")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 01:30 EST on US fall-back day: the hour happens twice, and only the
	// offset says which. The journal encoder writes time.Time this way;
	// UnmarshalText is the inverse, so the replay must not drop the prefix.
	stamp := time.Date(2026, 11, 1, 1, 30, 0, 0, time.FixedZone("EST", -5*3600))
	line, err := json.Marshal(struct {
		Ev   string    `json:"ev"`
		TS   time.Time `json:"ts"`
		Text string    `json:"text"`
	}{Ev: "log", TS: stamp, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, runID+".jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdShow(&buf, runID); code != exitOK {
		t.Fatalf("replaying a recorded run should exit %d, got %d", exitOK, code)
	}
	want := stamp.Local().Format("15:04:05")
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("replay lost the event time prefix %s: %q", want, buf.String())
	}
}

func TestRunsRendersMissingStartTimeAsNA(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	id := "20260102T150405Z-0001"
	j, err := journal.Open(id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(journal.Summary{
		Dirs: []string{"/tmp/proj"},
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdRuns(&buf, palette{}, 10); code != exitOK {
		t.Fatalf("listing runs should exit %d, got %d", exitOK, code)
	}
	if strings.Contains(buf.String(), "0001-01-01") {
		t.Fatalf("zero start time should not format as 0001-01-01, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "n/a") {
		t.Fatalf("zero start time should format as n/a, got:\n%s", buf.String())
	}
}

func TestShowTimeZero(t *testing.T) {
	if got := showTime(""); got != "" {
		t.Fatalf("showTime(\"\") = %q, want empty", got)
	}
	if got := showTime("0001-01-01T00:00:00Z"); got != "" {
		t.Fatalf("showTime(zero) = %q, want empty", got)
	}
	if got := showTime("invalid-timestamp"); got != "" {
		t.Fatalf("showTime(invalid) = %q, want empty", got)
	}
	stamp := time.Date(2026, 8, 25, 13, 15, 30, 0, time.UTC)
	want := stamp.Local().Format("15:04:05")
	if got := showTime(stamp.Format(time.RFC3339)); got != want {
		t.Fatalf("showTime(RFC3339) = %q, want %q", got, want)
	}
}
