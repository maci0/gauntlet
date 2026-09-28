// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/prompt"
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
	code := doctor(&buf, palette{}, nil, 80)
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
	code := doctor(&buf, palette{}, nil, 80)
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
	doctor(&buf, palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "State: "+state) || !strings.Contains(out, "from GAUNTLET_HOME") {
		t.Fatalf("doctor should name the GAUNTLET_HOME state root and its source:\n%s", out)
	}

	t.Setenv("GAUNTLET_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	buf.Reset()
	doctor(&buf, palette{}, nil, 80)
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
	if code := doctor(&buf, palette{}, nil, 80); code != exitFail {
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
	code := doctor(&buf, palette{}, nil, 80)
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
	doctor(&buf, palette{}, nil, 80)
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
	sink := &doctorFailWriter{remaining: 0}
	code, diagnostic := captureStderrFor(t, func() int {
		return doctor(sink, palette{}, nil, 80)
	})
	if code != exitFail || !strings.Contains(diagnostic.String(), "cannot write doctor report: "+io.ErrClosedPipe.Error()) {
		t.Fatalf("exit %d, stderr %q", code, diagnostic.String())
	}
}

type doctorFailWriter struct {
	bytes.Buffer
	remaining int
}

func (w *doctorFailWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.remaining)
	w.Buffer.Write(p[:n])
	w.remaining -= n
	if n < len(p) {
		return n, io.ErrClosedPipe
	}
	return n, nil
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
	doctor(&buf, palette{}, nil, 200)
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
	doctor(&buf, palette{}, nil, 200)
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

	record := func(at time.Time) string {
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
	second := record(base.Add(time.Hour))
	record(base)

	var buf strings.Builder
	doctor(&buf, palette{}, nil, 80)
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
	doctor(&buf, palette{}, nil, 80)
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
	doctor(&buf, palette{}, nil, 80)
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
