// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
)

// Doctor assembles its review catalog from agent.ReviewTools and
// agent.ReviewsWithoutTools, while the reviews themselves are defined by
// internal/prompt's embedded prompts. Nothing in the type system ties the two
// together: a new review prompt that skips the tool table would silently
// vanish from doctor, and a stale name would show a phantom row or advertise
// tools for a review that no longer exists.
func TestDoctorReviewCatalogMatchesTheBundledReviews(t *testing.T) {
	bundled := make(map[string]bool)
	for _, n := range prompt.BundledNames() {
		bundled[n] = true
	}

	cataloged := make(map[string]string) // review -> which list named it
	note := func(name, list string) {
		if !bundled[name] {
			t.Errorf("%s lists %q, which is not a bundled review", list, name)
			return
		}
		if prev := cataloged[name]; prev != "" {
			t.Errorf("%q appears in both %s and %s; each bundled review is listed exactly once", name, prev, list)
		}
		cataloged[name] = list
	}
	for name := range agent.ReviewTools {
		note(name, "agent.ReviewTools")
	}
	for _, name := range agent.ReviewsWithoutTools {
		note(name, "agent.ReviewsWithoutTools")
	}

	var missing []string
	for _, n := range prompt.BundledNames() {
		if cataloged[n] == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("bundled reviews absent from doctor's catalog (add them to agent.ReviewTools or agent.ReviewsWithoutTools): %s",
			strings.Join(missing, ", "))
	}
}

func TestDoctorCustomAgentCounting(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "mycustombin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := agent.Register("custombot", agent.Custom{
		Argv: []string{"mycustombin", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}
	// Register writes into a process-global table, so the definition would
	// outlive this test and show up in every later doctor and agent run.
	t.Cleanup(func() { agent.Unregister("custombot") })

	var buf strings.Builder
	code := doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "✓ custombot") {
		t.Fatalf("doctor should show custombot as installed (code %d):\n%s", code, out)
	}
}

func TestDoctorDshViaBunxExitsOneWhenNoAutoDetectableAgent(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	dir := t.TempDir()
	bunxPath := filepath.Join(dir, "bunx")
	if err := os.WriteFile(bunxPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	var buf strings.Builder
	code := doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "✓ dsh") || !strings.Contains(out, "via bunx") {
		t.Fatalf("doctor should show dsh via bunx:\n%s", out)
	}
	if !strings.Contains(out, "No auto-detectable agent CLI found") {
		t.Fatalf("doctor should report no auto-detectable agent found:\n%s", out)
	}
	if code != exitFail {
		t.Fatalf("doctor exit code = %d, want %d (exitFail)", code, exitFail)
	}
}

// The state root decides where the journal, the handoff files, and
// agents.json live, and a GAUNTLET_HOME pointing somewhere unexpected is
// invisible everywhere else on the screen. doctor names it and where the
// answer came from, so a config question needs no second command.
func TestDoctorNamesTheStateRootAndItsSource(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "State: "+state) || !strings.Contains(out, "from GAUNTLET_HOME") {
		t.Fatalf("doctor should name the GAUNTLET_HOME state root and its source:\n%s", out)
	}

	t.Setenv("GAUNTLET_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	buf.Reset()
	doctor(&buf, report.Palette{}, nil, 80)
	out = buf.String()
	want := "State: " + filepath.Join(home, ".gauntlet")
	if !strings.Contains(out, want) || !strings.Contains(out, "from $HOME") {
		t.Fatalf("doctor should fall back to the HOME state root and say so:\n%s", out)
	}
}

// A box with no usable agent is the one whose state root most needs saying, so
// the line is printed before the verdict rather than after it.
func TestDoctorNamesTheStateRootWithoutAUsableAgent(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())

	var buf strings.Builder
	if code := doctor(&buf, report.Palette{}, nil, 80); code != exitFail {
		t.Fatalf("doctor exit code = %d, want %d (exitFail)", code, exitFail)
	}
	if out := buf.String(); !strings.Contains(out, "State: "+state) {
		t.Fatalf("doctor should name the state root even when it fails:\n%s", out)
	}
}

// A state root that cannot be written loses the run journal, and the only
// other report of that is one warning line in the middle of a run. doctor is
// where a mistyped GAUNTLET_HOME is looked at, so the unusable root is the
// finding and the exit code says so.
func TestDoctorReportsAnUnusableStateRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a mode-0500 directory")
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(state, 0o700) })
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())

	var buf strings.Builder
	code := doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "State root unusable") || !strings.Contains(out, "is not writable") {
		t.Fatalf("doctor should report a state root that cannot be written:\n%s", out)
	}
	if code != exitFail {
		t.Fatalf("doctor exit code = %d, want %d (exitFail) for an unusable state root", code, exitFail)
	}
}

// A root that does not exist yet is a fresh install, not a misconfiguration:
// the first run creates it, and doctor must not create it to find out.
func TestDoctorLeavesAMissingStateRootAlone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "later")
	t.Setenv("GAUNTLET_HOME", root)
	t.Setenv("PATH", t.TempDir())

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	if out := buf.String(); strings.Contains(out, "State root unusable") {
		t.Fatalf("doctor should accept a state root the first run will create:\n%s", out)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("doctor created the state root %s; it is a diagnostic", root)
	}
}

func TestStateRootProblem(t *testing.T) {
	dir := t.TempDir()
	if problem := stateRootProblem(dir); problem != "" {
		t.Errorf("a writable state root reported %q", problem)
	}
	if problem := stateRootProblem(filepath.Join(dir, "later")); problem != "" {
		t.Errorf("a state root the first run creates reported %q", problem)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if problem := stateRootProblem(file); !strings.Contains(problem, "is not a directory") {
		t.Errorf("a state root that is a file reported %q", problem)
	}
}

// The writability probe leaves nothing behind: a leftover file in the state
// root would be mistaken for run state.
func TestDoctorProbeLeavesNoFile(t *testing.T) {
	state := t.TempDir()
	if problem := stateRootProblem(state); problem != "" {
		t.Fatalf("a writable temp state root reported %q", problem)
	}
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the probe left %d entries behind in the state root", len(entries))
	}
}

func TestDoctorReportsOutputFailure(t *testing.T) {
	sink := &failWriter{remaining: 0}
	code, diagnostic := captureStderrFor(t, func() int {
		return doctor(sink, report.Palette{}, nil, 80)
	})
	if code != exitFail || !strings.Contains(diagnostic.String(), "cannot write doctor report: "+io.ErrClosedPipe.Error()) {
		t.Fatalf("exit %d, stderr %q", code, diagnostic.String())
	}
}

// Which of the documented variables this process saw is otherwise knowable
// only by reading the source, so a knob set to the wrong value, or set to
// empty, has no way to be told from unset. doctor prints them, keeps a token
// out of the transcript, and says so when nothing is set.
func TestDoctorReportsTheEnvironmentItSaw(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /tmp/id_test")
	t.Setenv("GH_TOKEN", "ghp_do_not_print_this")

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 200)
	out := buf.String()
	if !strings.Contains(out, "GIT_SSH_COMMAND=ssh -i /tmp/id_test") {
		t.Fatalf("doctor should report the value of a non-secret variable it saw:\n%s", out)
	}
	if strings.Contains(out, "ghp_do_not_print_this") || !strings.Contains(out, "GH_TOKEN=(set)") {
		t.Fatalf("doctor must report a token as present without printing it:\n%s", out)
	}

	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("NO_COLOR", "")
	// t.Setenv registers the restore; unsetting after it leaves the variable
	// absent for this test and the original back afterwards.
	if err := os.Unsetenv("GIT_SSH_COMMAND"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctor(&buf, report.Palette{}, nil, 200)
	out = buf.String()
	if !strings.Contains(out, "NO_COLOR=(empty)") {
		t.Fatalf("doctor should tell a variable set to empty from one unset:\n%s", out)
	}
	if strings.Contains(out, "GIT_SSH_COMMAND") {
		t.Fatalf("doctor should not name a variable that is unset:\n%s", out)
	}
}

// What the state tree holds is what a restore has to be checked against: a
// restored tree that lists fewer journals than it should, or an index row with
// no journal behind it, reads exactly like a healthy one from the outside.
func TestDoctorReportsTheRunHistory(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())
	base := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

	second := closedRun(t, base.Add(time.Hour))
	closedRun(t, base)

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	if out := buf.String(); !strings.Contains(out, "Run history: 2 journals, index agrees") {
		t.Fatalf("doctor should report two agreeing journals:\n%s", out)
	}

	// A run that flushed its journal and died before Close: the journal is on
	// disk and the index never learned of it, which is what a restore of an
	// interrupted run looks like.
	open, err := journal.Open(journal.NewRunID(base.Add(2*time.Hour)), base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	open.Write(map[string]string{"ev": "run_start"})
	open.Flush()

	if _, err := journal.Prune(1); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctor(&buf, report.Palette{}, nil, 80)
	if out := buf.String(); !strings.Contains(out, "Run history: 1 journal, 1 not matched by the index") {
		t.Fatalf("doctor should report the journal the index does not name:\n%s", out)
	}
	if out := buf.String(); !strings.Contains(out, "1 pruned run still recoverable") {
		t.Fatalf("doctor should name the recoverable run:\n%s", out)
	}

	// The pruned journal is put back, and a listing repairs the crashed run,
	// so the tree agrees again.
	if err := journal.Restore(second); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Recent(10); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "Run history: 2 journals, index agrees") {
		t.Fatalf("doctor should report a healthy tree after the restore:\n%s", out)
	}
	if strings.Contains(out, "still recoverable") {
		t.Fatalf("doctor still reports a recoverable run after restoring %s:\n%s", second, out)
	}
	if strings.Contains(out, second) {
		t.Fatalf("doctor leaked a run id into its report:\n%s", out)
	}
}

// The other way the two copies tell a different story is the one no listing
// repairs: an index row whose journal is gone, which is what a restore that
// carried the index over and left the runs behind looks like. Doctor has to
// say so, and it has to stop offering the listing as the repair.
func TestDoctorReportsAnIndexWithNoJournals(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())
	at := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

	closedRun(t, at)
	// The copy that lost the journals and kept the derived index beside them.
	if err := os.RemoveAll(filepath.Join(journal.Home(), "runs")); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "Run history: 0 journals") {
		t.Fatalf("doctor should report the empty tree rather than say nothing:\n%s", out)
	}
	if !strings.Contains(out, "1 row named by the index with no journal on disk") {
		t.Fatalf("doctor should name the row whose journal is gone:\n%s", out)
	}
	if strings.Contains(out, "gauntlet runs repairs") {
		t.Fatalf("doctor points at a listing that cannot rebuild a missing journal:\n%s", out)
	}
}

// A journal cut mid-line is the loss the run history counts cannot show: the
// run is there, the index row is there, and the last events are gone. Doctor
// says so, because its Run history line is what an operator checks a restored
// tree against.
func TestDoctorReportsAJournalCutMidLine(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GAUNTLET_HOME", state)
	t.Setenv("PATH", t.TempDir())
	at := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

	id := closedRun(t, at)
	// The tail a copy taken with the run still writing leaves behind.
	f, err := os.OpenFile(filepath.Join(journal.Home(), "runs", "2026-01-02", id+".jsonl"),
		os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"ev":"loop_start","loop":2`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "Run history: 1 journal, index agrees") {
		t.Fatalf("doctor should still report the run it can list:\n%s", out)
	}
	if !strings.Contains(out, "1 journal ends mid-line") {
		t.Fatalf("doctor should report the journal that lost its last events:\n%s", out)
	}
}

// A review whose merge did not land is the one output no copy of the state
// tree holds: the journal names the branch and nothing about the commits on
// it. Doctor names the branches that are still there, because an operator
// checks a machine before retiring it and the run that left them has long
// since scrolled away.
func TestDoctorReportsUnlandedReviewBranches(t *testing.T) {
	if agent.Resolve("git") == "" {
		t.Skip("no git on PATH")
	}
	dir, run := gitRepo(t, "package main\n")
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	run("branch", gitx.LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"))
	run("branch", gitx.LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"))
	// A branch of the user's own is not review output and must not be named as
	// work a backup has to carry.
	run("branch", "feature/keep-me")
	t.Chdir(dir)

	var buf strings.Builder
	doctor(&buf, report.Palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "Unlanded reviews: 2 branches") {
		t.Fatalf("doctor did not report the branches a merge left behind:\n%s", out)
	}
	for _, name := range []string{
		gitx.LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"),
		gitx.LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"),
	} {
		if !strings.Contains(out, name) {
			t.Errorf("doctor did not name %s:\n%s", name, out)
		}
	}
	if strings.Contains(out, "feature/keep-me") {
		t.Errorf("doctor named a branch that is not review output:\n%s", out)
	}
	if !strings.Contains(out, "git bundle create") {
		t.Errorf("doctor did not say what keeps those commits:\n%s", out)
	}

	// A repository whose reviews all landed holds no lane branch, so the line
	// has nothing to say and stays off the report.
	run("branch", "-D", gitx.LaneBranch("20260929T164112Z-3930d-l1-lane0-03", "security"))
	run("branch", "-D", gitx.LaneBranch("20260929T164112Z-3930d-l1-lane1-07", "performance"))
	buf.Reset()
	doctor(&buf, report.Palette{}, nil, 80)
	if out := buf.String(); strings.Contains(out, "Unlanded reviews") {
		t.Fatalf("doctor reported unlanded reviews in a repository that has none:\n%s", out)
	}
}

// A git older than the floor every call in internal/gitx makes rejects
// --end-of-options, so a run fails on an error that names neither git nor a
// version. Doctor reports the version it found, and the floor it is measured
// against, so the answer arrives before a review does.
func TestDoctorReportsTheGitVersion(t *testing.T) {
	if agent.Resolve("git") == "" {
		t.Skip("no git on PATH")
	}
	var buf bytes.Buffer
	doctor(&buf, report.Palette{}, nil, 200)
	out := buf.String()
	if !strings.Contains(out, "git version") {
		t.Fatalf("doctor did not report the git version:\n%s", out)
	}
	line := gitx.Version(context.Background())
	if line == "" {
		t.Fatal("git resolved but `git --version` produced no line")
	}
	if !strings.Contains(out, line) {
		t.Fatalf("doctor reported no line for git %q:\n%s", line, out)
	}
	if below, known := gitx.BelowFloor(line); !known {
		t.Fatalf("git %q carries no comparable version", line)
	} else if below {
		t.Fatalf("the git on this machine (%s) is older than the floor doctor reports", line)
	}
}

// The floor is stated in the README and compared in code; nothing in the type
// system ties the two together, so a raised constant leaves the docs claiming
// a version this package no longer needs, or the reverse.
func TestReadmeGitFloorMatchesTheConstant(t *testing.T) {
	readme := readRepoFile(t, filepath.Join(moduleRoot(t), "README.md"))
	stated := ""
	for line := range strings.SplitSeq(readme, "\n") {
		if _, rest, ok := strings.Cut(line, "Git "); ok {
			stated = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), ":"))
			break
		}
	}
	if stated == "" {
		t.Fatal("README.md states no git version floor for the reader")
	}
	if !strings.HasPrefix(stated, gitx.MinVersion) {
		t.Fatalf("README states git %q; the code requires %s", stated, gitx.MinVersion)
	}
}

// A review name can be written in any script: a reviewed repository names its
// own prompts, and doctor lists them in one column. The column is measured in
// terminal cells, so a name of double-width glyphs gets the room it draws
// with. Counting bytes or runes instead makes the column short by the width of
// those glyphs and pushes every column after the name out of alignment, which
// is a line that reads as two misaligned tables rather than one.
func TestReviewNameColumnMeasuresTerminalCells(t *testing.T) {
	const cjk = "日本語-review" // 3 double-width + 1 space + 7 narrow
	names := []string{"sec-review", cjk}

	col := report.ReviewNameColumn(names)
	if got, want := col, report.Cells(cjk)+1; got != want {
		t.Errorf("report.ReviewNameColumn = %d, want %d (the widest name in report.Cells, plus one)", got, want)
	}
	if runeCount := utf8.RuneCountInString(cjk) + 1; col <= runeCount {
		t.Errorf("report.ReviewNameColumn = %d, no wider than the %d a rune count yields; the double-width "+
			"glyphs in %q each need two report.Cells", col, runeCount, cjk)
	}
	// Every name has to land in the same column, which is what the padding is
	// for. A name that draws wider than the budget would push the columns
	// after it along.
	for _, n := range names {
		if got := report.Cells(report.PadCells(n, col)); got != col {
			t.Errorf("padded %q occupies %d report.Cells, want %d", n, got, col)
		}
	}
}

// closedRun records a run that started and ended around at, closed the way a
// finished run closes: the journal on disk and the index row beside it. Every
// run-history case starts from one.
func closedRun(t *testing.T, at time.Time) string {
	t.Helper()
	id := journal.NewRunID(at)
	j, err := journal.Open(id, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(journal.Summary{Start: at, End: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	return id
}
