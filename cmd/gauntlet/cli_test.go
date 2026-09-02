// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/runner"
)

// captureStderr swaps os.Stderr for a pipe, runs f, and returns what was
// written.
func captureStderr(t *testing.T, f func() int) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	code := f()
	w.Close()
	os.Stderr = orig
	if code != exitUsage {
		t.Errorf("usage error should exit %d, got %d", exitUsage, code)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// A bad flag is reported once: the flag package prints the message and the
// usage screen, and run must not repeat the message after them.
func TestParseErrorReportedOnce(t *testing.T) {
	got := captureStderr(t, func() int { return run([]string{"--bogus-flag"}) })
	if n := strings.Count(got, "flag provided but not defined"); n != 1 {
		t.Errorf("parse error printed %d times, want exactly once:\n%s", n, got)
	}
	if !strings.Contains(got, "USAGE") {
		t.Error("a bad flag should still show the usage screen")
	}
}

// captureParseStderr swaps os.Stderr for a pipe, runs parseFlags, and returns
// what was written plus whether the error came back marked as reported.
func captureParseStderr(t *testing.T, argv []string) (string, bool) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	_, perr := parseFlags(argv)
	w.Close()
	os.Stderr = orig
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := errors.AsType[parseError](perr)
	return string(out), ok
}

// Every rejection from parsing reports itself the same way the flag package
// does: message first, then the usage screen, each exactly once. An unknown
// command or a rejected value must not read differently from a mistyped flag.
func TestUsageErrorsCarryTheMessageAndTheScreen(t *testing.T) {
	for _, argv := range [][]string{
		{"bogus"},                      // unknown command
		{"show"},                       // missing run id
		{"--limit", "0"},               // value validation
		{"--once", "--max-loops", "2"}, // flag conflict
	} {
		got, reported := captureParseStderr(t, argv)
		if !reported {
			t.Errorf("%v: error not marked as already reported", argv)
			continue
		}
		if n := strings.Count(got, "USAGE"); n != 1 {
			t.Errorf("%v: usage screen shown %d times:\n%s", argv, n, got)
		}
		if msg, screen := strings.Index(got, "\n"), strings.Index(got, "USAGE"); msg < 0 || msg > screen {
			t.Errorf("%v: message must precede the usage screen:\n%.80s", argv, got)
		}
	}
}

// The end-to-end path through run keeps the same shape and the exit code.
func TestUnknownCommandReportsLikeABadFlag(t *testing.T) {
	got := captureStderr(t, func() int { return run([]string{"bogus"}) })
	if n := strings.Count(got, "unknown command"); n != 1 {
		t.Errorf("unknown command printed %d times, want exactly once:\n%s", n, got)
	}
	if !strings.Contains(got, "USAGE") {
		t.Error("an unknown command should still show the usage screen")
	}
}

// A row is laid out in terminal columns, so the cut has to stay inside a
// column budget for text that is not one column per rune.
func TestTrimCellsStaysInsideItsColumnBudget(t *testing.T) {
	for _, s := range []string{
		strings.Repeat("héllo wörld ", 20), // multibyte, one column each
		strings.Repeat("日本語のテキスト ", 20),    // two columns each
		strings.Repeat("aé日 ", 20),         // mixed
	} {
		limit := cells(s)
		for w := 2; w < limit+4; w++ {
			got := trimCells(s, w)
			if !utf8.ValidString(got) {
				t.Fatalf("trimCells(%d) split a rune: %q", w, got)
			}
			if cells(got) > w {
				t.Fatalf("trimCells(%d) returned %d columns: %q", w, cells(got), got)
			}
		}
		if got := trimCells(s, limit); got != s {
			t.Errorf("input already inside the budget must pass through unchanged")
		}
	}
}

// padCells fills to a column count, which is what keeps a listing's second
// column in one place regardless of what the first one holds.
func TestPadCellsFillsToColumns(t *testing.T) {
	for _, s := range []string{"abc", "héllo", "日本語", "aé日"} {
		if got := cells(padCells(s, 12)); got != 12 {
			t.Errorf("padCells(%q, 12) occupies %d columns, want 12", s, got)
		}
	}
	if got := padCells("日本語", 2); got != "日本語" {
		t.Errorf("a value wider than the column must not be cut by padding: %q", got)
	}
}

// A failed re-exec ends the run for good, so the handoff file it just wrote
// must not sit in the state dir forever.
// A reload is a handover, not a restart: a resumed directory keeps the
// schedule the run already resolved, so --suggest does not ask an agent (and
// the user) a second time and a launcher-composed run does not reopen the
// launcher.
func TestResumedDirectoriesKeepTheirSchedule(t *testing.T) {
	carried := &dirRun{dir: "/a"}
	fresh := &dirRun{dir: "/b"}
	prior := handoff{Dirs: map[string]dirHandoff{"/a": {Reviews: []string{"sec-review"}}}}

	got := needPlanning([]*dirRun{carried, fresh}, prior, true)
	if len(got) != 1 || got[0] != fresh {
		t.Fatalf("planning was asked for %v, want only the directory with no schedule", got)
	}
	if len(carried.reviews) != 1 || carried.reviews[0] != "sec-review" {
		t.Fatalf("the carried schedule was lost: %v", carried.reviews)
	}

	// A fresh process ignores whatever state happens to be lying around.
	carried.reviews = nil
	if got := needPlanning([]*dirRun{carried, fresh}, prior, false); len(got) != 2 {
		t.Fatalf("a run that is not resuming must plan every directory, got %v", got)
	}
}

func TestFailedReloadRemovesHandoffState(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	runID := "test-run"
	if code := doReload(filepath.Join(t.TempDir(), "missing-binary"),
		runID, time.Now(), 0, nil, handoff{}, []string{"--once"}, io.Discard); code != exitFail {
		t.Fatalf("a failed exec should exit %d, got %d", exitFail, code)
	}
	if _, err := os.Stat(filepath.Join(journal.StateDir(), runID+".json")); !os.IsNotExist(err) {
		t.Fatalf("handoff state survived a failed reload: %v", err)
	}
}

// A run id that names nothing is a bad argument (exit 2), while a journal
// that exists but cannot be read is a general failure (exit 1). Scripts use
// the difference to tell "I typed the wrong id" from "gauntlet broke".
func TestShowExitCodes(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	if code := cmdShow(io.Discard, "no-such-run"); code != exitUsage {
		t.Errorf("unknown run id should exit %d, got %d", exitUsage, code)
	}

	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	dir := filepath.Join(home, "runs", "2026-08-25")
	if err := os.MkdirAll(filepath.Join(dir, "unreadable.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A directory where the journal belongs passes findRun's stat but cannot
	// be read as JSONL, whatever user the suite runs as.
	if code := cmdShow(io.Discard, "unreadable"); code != exitFail {
		t.Errorf("unreadable journal should exit %d, got %d", exitFail, code)
	}
}

// The FAILED column must line up with its header with color on: pad first,
// color second, because escapes inside a width verb count as width.
func TestRunsFailedCellStaysAlignedUnderColor(t *testing.T) {
	strip := func(s string) string {
		return regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(s, "")
	}
	plain := failedCell(palette{}, 3)
	colored := failedCell(palette{on: true}, 3)
	if plain != fmt.Sprintf("%6d", 3) {
		t.Errorf("plain cell %q is not right-aligned in 6 columns", plain)
	}
	if got := strip(colored); got != plain {
		t.Errorf("colored cell %q renders as %q, want %q", colored, got, plain)
	}
}

// doctor's "exits 1 if no agent is usable" contract covers --bin overrides:
// an override that names nothing runnable must not count as a working agent.
func TestBinRunnable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "agent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	noexec := filepath.Join(dir, "noexec")
	if err := os.WriteFile(noexec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		want bool
	}{
		{exe, true},
		{noexec, false},
		{dir, false},
		{filepath.Join(dir, "missing"), false},
		{"", false},
	} {
		if got := binRunnable(c.path); got != c.want {
			t.Errorf("binRunnable(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// Pinning every agent to an override that names nothing runnable is the empty
// box again: doctor must say so and exit 1, not report a working setup
// because an override was present.
func TestDoctorBrokenOverridesExitLikeAnEmptyBox(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "missing")
	overrides := make(map[string]string)
	for _, a := range agent.AllNames() {
		overrides[a] = bin
	}
	var out strings.Builder
	if code := doctor(&out, palette{}, overrides, 100); code != 1 {
		t.Errorf("only broken --bin overrides should exit 1, got %d:\n%s", code, out.String())
	}
}

// exitCode maps a run onto the codes docs/CLI.md promises to scripts. The
// contract test pins that the documented set does not drift; this one pins
// which outcome produces which number, including the two easy to get
// backwards: an interruption is not a failure (it says nothing about the
// reviews), and a failed commit step is (the fixes are sitting uncommitted).
func TestExitCodeMapsTheDocumentedCodes(t *testing.T) {
	run := func(status runner.Status) *dirRun {
		d := &dirRun{dir: "/repo", stats: &runner.Stats{}}
		d.stats.Add(runner.Result{Review: "r-review", Status: status})
		return d
	}

	t.Run("nothing failed", func(t *testing.T) {
		for _, runs := range [][]*dirRun{
			nil,
			{&dirRun{dir: "/repo"}}, // no stats yet: not a failure
			{run(runner.StatusOK)},
			{run(runner.StatusInterrupted)},
			{run(runner.StatusOK), run(runner.StatusInterrupted)},
		} {
			if got := exitCode(context.Background(), runs); got != exitOK {
				t.Errorf("exitCode(%d results) = %d, want %d", len(runs), got, exitOK)
			}
		}
	})

	t.Run("any review failure fails the run", func(t *testing.T) {
		for _, status := range []runner.Status{
			runner.StatusFail, runner.StatusTimeout,
			runner.StatusConflict, runner.StatusSkipped,
		} {
			if got := exitCode(context.Background(), []*dirRun{run(status)}); got != exitFail {
				t.Errorf("%s should exit %d, got %d", status, exitFail, got)
			}
		}
	})

	t.Run("a failed commit step fails the run on its own", func(t *testing.T) {
		d := &dirRun{dir: "/repo", stats: &runner.Stats{}}
		d.stats.Add(runner.Result{Review: "r-review", Status: runner.StatusOK})
		d.stats.Seed(nil, 2, 1) // two commit steps ran, one failed
		if got := exitCode(context.Background(), []*dirRun{d}); got != exitFail {
			t.Errorf("commit step failure should exit %d, got %d", exitFail, got)
		}
	})

	t.Run("an interrupted run reports the interrupt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got := exitCode(ctx, []*dirRun{run(runner.StatusOK)}); got != 130 {
			t.Errorf("a cancelled context should exit 130, got %d", got)
		}
	})
}

// A run that dies while the runners are being built must still close its
// journal: the quiet close flushes what New logged before it failed, and a
// run that never started writes no index row. The first directory logs its
// untracked-file notice before the second one fails construction, so the
// notice sitting in the journal file is the proof the flush happened.
func TestFailedRunConstructionStillClosesTheJournal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for this test")
	}
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)

	// A stub agent satisfies the PATH check; nothing is ever spawned because
	// construction fails on the second directory first.
	bin := t.TempDir()
	stub := filepath.Join(bin, "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	good := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = good
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(good, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	// Untracked files are what make New log to the bus before it succeeds.
	if err := os.WriteFile(filepath.Join(good, "notes.txt"),
		[]byte("not tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := t.TempDir() // not a git repository: --jobs > 1 refuses it

	stderr := captureStderr(t, func() int {
		return run([]string{"--dirs", good + "," + bad, "--jobs", "2", "--agents", "claude"})
	})
	if !strings.Contains(stderr, "needs a git repository") {
		t.Errorf("the non-git directory should be named as the failure:\n%s", stderr)
	}

	matches, err := filepath.Glob(filepath.Join(home, "runs", "*", "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one journal file under %s (err=%v): %v", home, err, matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "untracked") {
		t.Errorf("the event logged before construction failed was not flushed:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(home, "index.jsonl")); !os.IsNotExist(err) {
		t.Errorf("a run that never started must not get an index row (stat err=%v)", err)
	}
}
