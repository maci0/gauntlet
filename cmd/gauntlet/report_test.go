// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
)

// TestReporterRendersEveryEventKind pins what a headless run prints per event
// kind: logs get their timestamp, output its lane prefix and repeat marker,
// only conflicting merges are announced, and a loop end carries its tally.
func TestReporterRendersEveryEventKind(t *testing.T) {
	ins, del := 12, 3
	events := []runner.Event{
		{Kind: runner.EvLog, Dir: "/repo/b", Text: "starting"},
		{Kind: runner.EvOutput, Review: "sec-review", Text: "+new",
			LineKind: normalize.DiffAdd, Repeat: 2},
		{Kind: runner.EvMerge, Review: "ok-review", Branch: "gauntlet/x/ok-review",
			Status: runner.StatusOK},
		{Kind: runner.EvMerge, Review: "sec-review", Branch: "gauntlet/x/sec-review",
			Status: runner.StatusConflict, Text: "CONFLICT in main.go"},
		{Kind: runner.EvLoopEnd, Loop: 1, Elapsed: 92, Ins: &ins, Del: &del},
	}
	var out bytes.Buffer
	r := &reporter{out: &out, multiDir: true}
	for _, ev := range events {
		r.handle(ev)
	}
	got := out.String()
	for _, want := range []string{
		"[b] starting",
		"sec-review │ +new (x2)",
		"MERGE CONFLICT: sec-review kept on gauntlet/x/sec-review",
		"=== Loop 1 complete in 1m32s, +12/-3 lines ===",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ok-review") {
		t.Errorf("a clean merge was announced:\n%s", got)
	}

	out.Reset()
	quiet := &reporter{out: &out, quiet: true}
	quiet.handle(runner.Event{Kind: runner.EvOutput, Review: "sec-review", Text: "chatter"})
	if out.Len() != 0 {
		t.Fatalf("quiet mode still printed agent output:\n%s", out.String())
	}
	quiet.handle(runner.Event{Kind: runner.EvLog, Text: "kept"})
	if !strings.Contains(out.String(), "kept") {
		t.Fatalf("quiet mode dropped a log line:\n%s", out.String())
	}

	out.Reset()
	r.handle(runner.Event{Kind: runner.EvLoopEnd, Loop: 2, Elapsed: 45})
	if strings.Contains(out.String(), "lines") {
		t.Fatalf("a loop with unmeasured lines invented a tally:\n%s", out.String())
	}
}

// TestSummaryAggregatesAcrossDirectories pins the end-of-run block scripts
// parse: fixed sections, totals merged over directories, the token rate and
// reasoning floor, and a failure list that says why each review failed.
func TestSummaryAggregatesAcrossDirectories(t *testing.T) {
	d1 := &dirRun{dir: "/repo/a", loops: 1, stats: &runner.Stats{}}
	d1.stats.Add(runner.Result{Review: "a-review", Agent: agent.Spec{Tool: "claude"},
		Status: runner.StatusOK, Tokens: 100, Thinking: 25, Elapsed: 4 * time.Second,
		Ins: 5, Del: 2, HaveLines: true})
	d2 := &dirRun{dir: "/repo/b", loops: 2, stats: &runner.Stats{}}
	d2.stats.Add(runner.Result{Review: "z-review", Agent: agent.Spec{Tool: "codex"},
		Status: runner.StatusFail, ExitCode: 7, Tokens: 300, Elapsed: 2 * time.Second})
	d2.stats.Seed(nil, 2, 1) // two commit steps ran, one failed

	var out bytes.Buffer
	summary(&out, palette{}, []*dirRun{d1, d2}, time.Minute)
	got := out.String()
	for _, want := range []string{
		"Directories: 2",
		"Completed loops: 3",
		"Total reviews run: 2",
		"Passed: 1",
		"Failed: 1",
		"Total time: 1m00s",
		"Agent time: 6s across 2 reviews (avg 3s)",
		"Tokens: 400 reported, ~67 tok/s, 25 reasoning (6%)",
		"Lines changed: +5 -2",
		"Commit steps: 2, 1 failed (changes may be uncommitted)",
		"- z-review (codex): exit 7",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"Skipped", "Interrupted", "Merge conflicts"} {
		if strings.Contains(got, absent+": ") {
			t.Errorf("an all-zero tally still advertises %q:\n%s", absent, got)
		}
	}
	// Per-agent rows merge the directories and sort by label.
	if i, j := strings.Index(got, "claude"), strings.Index(got, "codex"); i < 0 || j < 0 || i > j {
		t.Errorf("per-agent rows missing or out of order:\n%s", got)
	}
	if !strings.Contains(got, "ok=1 fail=0") || !strings.Contains(got, "fail=1") {
		t.Errorf("per-agent tallies wrong:\n%s", got)
	}
}

// colorEnabled honors NO_COLOR (set at all), TERM=dumb, and terminal
// detection, with CLICOLOR_FORCE / FORCE_COLOR turning color back on for a
// pipe. The precedence is the contract: an explicit opt-out beats an opt-in.
func TestColorEnabledPrecedence(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "pipe"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// The ambient environment must not vote: a CI exporting FORCE_COLOR would
	// otherwise flip these cases. Save, clear, and restore the four variables.
	const keys = "NO_COLOR\x00TERM\x00CLICOLOR_FORCE\x00FORCE_COLOR"
	saved := map[string]string{}
	for k := range strings.SplitSeq(keys, "\x00") {
		if v, ok := os.LookupEnv(k); ok {
			saved[k] = v
		}
	}
	t.Cleanup(func() {
		for k := range strings.SplitSeq(keys, "\x00") {
			os.Unsetenv(k)
		}
		for k, v := range saved {
			os.Setenv(k, v)
		}
	})

	cases := []struct {
		name string
		env  []string
		want bool
	}{
		{"a pipe has no color", nil, false},
		{"CLICOLOR_FORCE turns a pipe into color", []string{"CLICOLOR_FORCE=1"}, true},
		{"FORCE_COLOR does too", []string{"FORCE_COLOR=1"}, true},
		{"a zero value forces nothing", []string{"CLICOLOR_FORCE=0"}, false},
		{"NO_COLOR beats any opt-in", []string{"NO_COLOR=1", "CLICOLOR_FORCE=1"}, false},
		{"NO_COLOR counts when empty", []string{"NO_COLOR=", "CLICOLOR_FORCE=1"}, false},
		{"TERM=dumb beats any opt-in", []string{"TERM=dumb", "CLICOLOR_FORCE=1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k := range strings.SplitSeq(keys, "\x00") {
				os.Unsetenv(k)
			}
			for _, kv := range c.env {
				k, v, _ := strings.Cut(kv, "=")
				if err := os.Setenv(k, v); err != nil {
					t.Fatal(err)
				}
			}
			if got := colorEnabled(f); got != c.want {
				t.Errorf("colorEnabled with %v = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

func TestListReviewsMarksWeightsAndLegend(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"sec-review", "doc-review"} {
		body := "Your goal is to test " + n + ".\n"
		if err := os.WriteFile(dir+"/"+n+".md", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	listReviews(&out, palette{}, set,
		[]string{"doc-review", "sec-review", "sec-review"}, 100)
	got := out.String()
	for _, want := range []string{
		"Available reviews (2)",
		"xN selected with repeated weight",
		"x2 sec-review",
		"test doc-review.",
		"Sets usable with --reviews/--exclude",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review list is missing %q:\n%s", want, got)
		}
	}
	// Naming a review twice must not read as available-but-unselected.
	if !strings.Contains(got, "✓  doc-review") {
		t.Errorf("the scheduled review lost its mark:\n%s", got)
	}
	if strings.Contains(got, "○ sec-review") || strings.Contains(got, "○  doc-review") {
		t.Errorf("a scheduled review was left unmarked:\n%s", got)
	}
}
