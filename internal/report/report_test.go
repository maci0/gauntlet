// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package report

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
)

// TestReporterStampsUntimedLinesFromItsClock pins the one line the Reporter
// timestamps itself: a log event published before the bus could stamp it. The
// stamp comes from the injected clock, the run's own, so a transcript of a
// replayed run reads the same clock end to end.
func TestReporterStampsUntimedLinesFromItsClock(t *testing.T) {
	stamp := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	r := &Reporter{Out: &out, Now: func() time.Time { return stamp }}
	r.handle(runner.Event{Kind: runner.EvLog, Text: "untimed"})
	if got, want := out.String(), "["+humanize.Clock(stamp)+"] untimed\n"; got != want {
		t.Fatalf("untimed line = %q, want %q", got, want)
	}

	// A timed event keeps its own stamp, and a Reporter with no clock still
	// falls back to wall time rather than the zero instant.
	out.Reset()
	r.handle(runner.Event{Kind: runner.EvLog, Text: "timed",
		Time: stamp.Add(time.Minute)})
	if got, want := out.String(), "["+humanize.Clock(stamp.Add(time.Minute))+"] timed\n"; got != want {
		t.Fatalf("timed line = %q, want %q", got, want)
	}
	out.Reset()
	plain := &Reporter{Out: &out}
	plain.handle(runner.Event{Kind: runner.EvLog, Text: "wall"})
	if got := out.String(); !strings.HasPrefix(got, "[") || !strings.Contains(got, "wall") {
		t.Fatalf("an unconfigured Reporter printed %q", got)
	}
}

// The plain Reporter's one line kind that its own text does not identify is an
// error the agent reported: a diff carries the sign, a result line says
// RESULT:, reasoning is italic, progress says what it is doing. So the error
// has to be marked in the line itself, and it has to survive a monochrome
// terminal, which is what --no-color and NO_COLOR leave behind and what a
// reader who cannot separate the hues is looking at either way (SC 1.4.1).
// This path is the one a screen reader and a pipe read, so the dashboard's
// mark cannot be the only place it exists.
func TestReporterMarksAgentErrorsWithoutColor(t *testing.T) {
	var out bytes.Buffer
	r := &Reporter{Out: &out}
	for _, ev := range []runner.Event{
		{Kind: runner.EvOutput, Review: "sec-review", Text: "reading main.go", LineKind: normalize.Plain},
		{Kind: runner.EvOutput, Review: "sec-review", Text: "the build failed on line 12", LineKind: normalize.Error},
		{Kind: runner.EvOutput, Review: "sec-review", Text: "+ added a line", LineKind: normalize.DiffAdd},
	} {
		r.handle(ev)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("the Reporter drew %d rows, want 3:\n%s", len(lines), out.String())
	}
	if strings.Contains(lines[0], "!") || strings.Contains(lines[2], "!") {
		t.Fatalf("a line that names itself carries the error mark anyway:\n%s", out.String())
	}
	if !strings.Contains(lines[1], "│ !the build failed on line 12") {
		t.Fatalf("an agent error is not marked, so --no-color leaves it as narration:\n%s", out.String())
	}
	// The mark rides in the line's own style, so with color on it is one red
	// run rather than a colored glyph in front of uncolored text.
	if got := (&Reporter{Pal: Palette{On: true}}).paint(normalize.Error, "boom"); got != "\x1b[31m!boom\x1b[0m" {
		t.Fatalf("the marked error line is %q, want the mark inside the line's own style", got)
	}
}

// TestReporterRendersEveryEventKind pins what a headless run prints per event
// kind: logs get their timestamp, output its lane prefix and repeat marker,
// only conflicting merges are announced, and a loop end carries its tally.
func TestReporterRendersEveryEventKind(t *testing.T) {
	ins, del := 12, 3
	events := []runner.Event{
		{Kind: runner.EvRunStart, Seed: 7},
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
	r := &Reporter{Out: &out, MultiDir: true}
	for _, ev := range events {
		r.handle(ev)
	}
	got := out.String()
	for _, want := range []string{
		"seed 7, rerun with --seed 7",
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
	quiet := &Reporter{Out: &out, Quiet: true}
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

func TestReporterConsumeDrainsUntilClosed(t *testing.T) {
	var out bytes.Buffer
	r := &Reporter{Out: &out}
	ch := make(chan runner.Event, 3)
	ch <- runner.Event{Kind: runner.EvLog, Text: "first"}
	ch <- runner.Event{Kind: runner.EvLog, Text: "second"}
	ch <- runner.Event{Kind: runner.EvLog, Text: "third"}
	close(ch)

	r.Consume(ch)
	got := out.String()
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(got, want) {
			t.Errorf("Consume missing %q:\n%s", want, got)
		}
	}
}

// The prefix is the event's instant, not when the line was printed: a
// buffered log would otherwise shift every timestamp, and a replay would
// disagree with the live run.
func TestReporterLogsTheEventTime(t *testing.T) {
	stamp := time.Date(2026, 11, 1, 1, 30, 0, 0, time.FixedZone("EST", -5*3600))
	var out bytes.Buffer
	r := &Reporter{Out: &out}
	r.handle(runner.Event{Kind: runner.EvLog, Text: "hello", Time: stamp})
	want := "[" + humanize.Clock(stamp) + "]"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("logged %q, want the event time prefix %s", out.String(), want)
	}
}

// TestSummaryAggregatesAcrossDirectories pins the end-of-run block scripts
// parse: fixed sections, totals merged over directories, the token rate and
// reasoning floor, and a failure list that says why each review failed.
func TestSummaryAggregatesAcrossDirectories(t *testing.T) {
	d1 := Dir{Loops: 1, Stats: &runner.Stats{}}
	d1.Stats.Add(runner.Result{Review: "a-review", Agent: agent.Spec{Tool: "claude"},
		Status: runner.StatusOK, Tokens: 100, Thinking: 25, Elapsed: 4 * time.Second,
		Ins: 5, Del: 2, HaveLines: true, Branch: "gauntlet/stack/a", Base: "main",
		URL: "https://github.com/owner/repo/pull/1"})
	d2 := Dir{Loops: 2, Stats: &runner.Stats{}}
	d2.Stats.Add(runner.Result{Review: "z-review", Agent: agent.Spec{Tool: "codex"},
		Status: runner.StatusFail, ExitCode: 7, Tokens: 300, Elapsed: 2 * time.Second})
	d2.Stats.Seed(nil, 2, 1) // two commit steps ran, one failed

	var out bytes.Buffer
	Summary(&out, Palette{}, []Dir{d1, d2}, time.Minute)
	got := out.String()
	for _, want := range []string{
		"Directories: 2",
		"Completed loops: 3",
		"Pull requests",
		"  a-review\n",
		"    branch  gauntlet/stack/a\n",
		"    base    main\n",
		"    url     https://github.com/owner/repo/pull/1\n",
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
			t.Errorf("Summary is missing %q:\n%s", want, got)
		}
	}
	for _, absent := range []string{"Skipped", "Interrupted", "Merge conflicts"} {
		if strings.Contains(got, absent+": ") {
			t.Errorf("an all-zero tally still advertises %q:\n%s", absent, got)
		}
	}
	d3 := Dir{Loops: 1, Stats: &runner.Stats{}}
	d3.Stats.Add(runner.Result{Review: "ghost-review", Agent: agent.Spec{Tool: "claude"},
		Status: runner.StatusSkipped, ExitCode: -1, Detail: "unknown name"})
	out.Reset()
	Summary(&out, Palette{}, []Dir{d3}, time.Second)
	if !strings.Contains(out.String(), "- ghost-review (claude): skipped: unknown name") {
		t.Errorf("skipped review did not use Detail:\n%s", out.String())
	}

	// Per-agent rows merge the directories and sort by label.
	if i, j := strings.Index(got, "claude"), strings.Index(got, "codex"); i < 0 || j < 0 || i > j {
		t.Errorf("per-agent rows missing or out of order:\n%s", got)
	}
	if !strings.Contains(got, "ok=1 fail=0") || !strings.Contains(got, "fail=1") {
		t.Errorf("per-agent tallies wrong:\n%s", got)
	}
}

// A status this build does not name still ran, so the Summary counts it and
// says so, and does not list it as a failure it cannot diagnose.
func TestSummaryCountsUnrecognizedOutcomes(t *testing.T) {
	d := Dir{Loops: 1, Stats: &runner.Stats{}}
	d.Stats.Add(runner.Result{Review: "a-review", Agent: agent.Spec{Tool: "claude"},
		Status: runner.StatusOK})
	d.Stats.Seed([]runner.Result{{Review: "b-review", Agent: agent.Spec{Tool: "claude"},
		Status: runner.Status("deferred")}}, 0, 0)
	var out bytes.Buffer
	Summary(&out, Palette{}, []Dir{d}, time.Second)
	got := out.String()
	for _, want := range []string{"Total reviews run: 2", "Unrecognized outcomes: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Failed reviews") {
		t.Errorf("an unrecognized status was reported as a failure:\n%s", got)
	}
}

// ColorEnabled honors NO_COLOR (set at all), TERM=dumb, and terminal
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
		{"a false value forces nothing", []string{"CLICOLOR_FORCE=false"}, false},
		{"a no value forces nothing", []string{"FORCE_COLOR=no"}, false},
		{"an off value forces nothing", []string{"FORCE_COLOR=off"}, false},
		{"NO_COLOR beats any opt-in", []string{"NO_COLOR=1", "CLICOLOR_FORCE=1"}, false},
		{"NO_COLOR counts when empty", []string{"NO_COLOR=", "CLICOLOR_FORCE=1"}, false},
		{"TERM=dumb beats any opt-in", []string{"TERM=dumb", "CLICOLOR_FORCE=1"}, false},
		{"TERM=DUMB is the same answer", []string{"TERM=DUMB", "CLICOLOR_FORCE=1"}, false},
		{"a padded TERM=dumb is too", []string{"TERM= dumb ", "CLICOLOR_FORCE=1"}, false},
		{"TERM=xterm-256color is not dumb", []string{"TERM=xterm-256color", "CLICOLOR_FORCE=1"}, true},
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
			if got := ColorEnabled(f); got != c.want {
				t.Errorf("ColorEnabled with %v = %v, want %v", c.env, got, c.want)
			}
		})
	}
}

// A listing is a table, and a table only reads as one if its second column
// starts in the same place on every row. Review names come from the reviewed
// tree, so they are not all one column per character: a CJK name is two
// columns per glyph, and a budget counted in runes or bytes puts its row out
// of line with the rest.
func TestListingColumnsLineUpForWideNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"aaaa-review", "café-review", "日本語-review"} {
		body := "Your goal is to test " + n + ".\nSummary: a description\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}

	// Where the column after the names begins, measured in terminal columns,
	// on every row that has one.
	starts := func(text, marker string) map[int]bool {
		at := map[int]bool{}
		for line := range strings.SplitSeq(text, "\n") {
			if before, _, ok := strings.Cut(line, marker); ok {
				at[Cells(before)] = true
			}
		}
		return at
	}

	var list bytes.Buffer
	ListReviews(&list, Palette{}, set, set.Names, 100)
	// The legend names [project] too and is not a row of the table.
	rows := ""
	for line := range strings.SplitSeq(list.String(), "\n") {
		if strings.Contains(line, "-review") {
			rows += line + "\n"
		}
	}
	// Every fixture name has to be on a row of its own. Without this a name
	// dropped from the listing leaves the surviving rows aligned and the
	// one-column check still holds.
	for _, n := range []string{"aaaa-review", "café-review", "日本語-review"} {
		if !strings.Contains(rows, n) {
			t.Errorf("%s missing from --list:\n%s", n, rows)
		}
	}
	if got := starts(rows, "[project]"); len(got) != 1 {
		t.Errorf("--list starts its origin column at %v, want one column:\n%s", keysOf(got), rows)
	}
}

func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
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
	ListReviews(&out, Palette{}, set,
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

func TestPaletteColors(t *testing.T) {
	off := Palette{On: false}
	if off.Think("text") != "text" || off.Bold("text") != "text" || off.Dim("text") != "text" ||
		off.Red("text") != "text" || off.Green("text") != "text" || off.Yellow("text") != "text" || off.Blue("text") != "text" {
		t.Error("Palette with color off should return plain text")
	}

	on := Palette{On: true}
	if on.Think("text") != "\x1b[2;3mtext\x1b[0m" {
		t.Errorf("on.think = %q", on.Think("text"))
	}
	if on.Bold("text") != "\x1b[1mtext\x1b[0m" {
		t.Errorf("on.bold = %q", on.Bold("text"))
	}
	if on.Dim("text") != "\x1b[2mtext\x1b[0m" {
		t.Errorf("on.dim = %q", on.Dim("text"))
	}
	if on.Red("text") != "\x1b[31mtext\x1b[0m" {
		t.Errorf("on.red = %q", on.Red("text"))
	}
	if on.Green("text") != "\x1b[32mtext\x1b[0m" {
		t.Errorf("on.green = %q", on.Green("text"))
	}
	if on.Yellow("text") != "\x1b[33mtext\x1b[0m" {
		t.Errorf("on.yellow = %q", on.Yellow("text"))
	}
	if on.Blue("text") != "\x1b[34mtext\x1b[0m" {
		t.Errorf("on.blue = %q", on.Blue("text"))
	}
}

// The one-line reason a review did not pass is the only place a run says why.
// Every status that counts as a failure has to name itself here, and the two
// StatusFail cases have to stay apart: an agent that ran and exited 7 and an
// agent that never started are different problems, and "exit 0" on the second
// would report a launch failure as a pass.
func TestFailureDetailNamesEveryFailingStatus(t *testing.T) {
	cases := []struct {
		name   string
		result runner.Result
		want   string
	}{
		{"timeout", runner.Result{Status: runner.StatusTimeout}, "timeout"},
		{"conflict names its branch", runner.Result{Status: runner.StatusConflict, Branch: "gauntlet/x/sec-review"},
			"merge conflict, kept on gauntlet/x/sec-review"},
		{"skipped with nothing to say", runner.Result{Status: runner.StatusSkipped},
			"skipped: never ran (unknown name or unreadable prompt)"},
		{"skipped with a reason", runner.Result{Status: runner.StatusSkipped, Detail: "prompt not found"},
			"skipped: prompt not found"},
		{"agent exit code", runner.Result{Status: runner.StatusFail, ExitCode: 7}, "exit 7"},
		{"never launched", runner.Result{Status: runner.StatusFail, ExitCode: -1}, "launch failed"},
		{"agent said why", runner.Result{Status: runner.StatusFail, ExitCode: 7, Detail: "the tests fail"}, "the tests fail"},
		{"anything else is its own name", runner.Result{Status: runner.Status("elsewhere")}, "elsewhere"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FailureDetail(c.result); got != c.want {
				t.Fatalf("FailureDetail(%+v) = %q, want %q", c.result, got, c.want)
			}
		})
	}
}
