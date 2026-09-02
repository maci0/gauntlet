// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/runner"
)

// A reload whose handoff state cannot be saved must not exec: the successor
// would start a fresh run (new id, every loop restarted) while this process's
// journal was already quiet-closed with no index row, so the run would vanish
// from `gauntlet runs` and the history weighting. The reload is aborted
// instead, and the caller finishes the run in this process.
// resumeStart reconstructs a monotonic-bearing start from the elapsed the
// predecessor measured. An NTP step (or a manual clock set) during the exec
// must not count toward --runtime; an old handoff that never wrote elapsed
// still has to trust the wall clock, which is what those binaries measured.
func TestResumeStartIgnoresAWallClockJumpWhenElapsedIsKnown(t *testing.T) {
	origin := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	measured := 90 * time.Minute
	now := origin.Add(measured + 2*time.Hour)

	started := resumeStart(now, handoff{StartedAt: origin, Elapsed: measured})
	if got := now.Sub(started); got != measured {
		t.Fatalf("elapsed %s includes the wall-clock jump; want the measured %s", got, measured)
	}

	started = resumeStart(now, handoff{StartedAt: origin})
	if got := now.Sub(started); got != measured+2*time.Hour {
		t.Fatalf("wall fallback %s, want %s", got, measured+2*time.Hour)
	}
}

func TestDoReloadAbortsWhenStateCannotBeSaved(t *testing.T) {
	// StateDir() resolves under GAUNTLET_HOME; make it uncreatable by putting
	// it under a regular file, so MkdirAll fails with ENOTDIR.
	file := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_HOME", filepath.Join(file, "home"))

	start := time.Now()
	runs := []*dirRun{{
		dir:   t.TempDir(),
		stats: &runner.Stats{Start: start},
	}}
	runs[0].stats.Add(runner.Result{Review: "error-review", Status: runner.StatusOK})

	var out bytes.Buffer
	// path is empty so a regression that reaches Reexec fails loudly instead
	// of execing anything. The abort is reported on stderr, where the
	// exec-failure path reports too.
	code, errs := captureStderrFor(t, func() int {
		return doReload("", "20260827T120000Z-dead", start, 0, runs, handoff{}, []string{}, &out)
	})
	if code != exitFail {
		t.Errorf("an unsavable handoff should abort the reload with exit %d, got %d", exitFail, code)
	}
	if msg := errs.String(); !strings.Contains(msg, "Reload aborted") {
		t.Errorf("the abort should be named as such, got: %q", msg)
	}
	if msg := out.String(); strings.Contains(msg, "Reloading into the new binary") {
		t.Errorf("an aborted reload must not announce the exec: %q", msg)
	}
}

// A successor whose handoff cannot be parsed must exit rather than start a
// fresh run: repeating finished reviews under a new id is worse than stopping.
func TestRunAbortsOnUnreadableHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_STATE", path)

	code, errs := captureStderrFor(t, func() int {
		return run([]string{"--once", "--dir", t.TempDir()})
	})
	if code != exitFail {
		t.Errorf("an unreadable handoff should exit %d, got %d", exitFail, code)
	}
	if msg := errs.String(); !strings.Contains(msg, "Cannot resume the interrupted run") {
		t.Errorf("the abort should name the handoff, got: %q", msg)
	}
}

// captureStderrFor swaps os.Stderr for a pipe, runs f, and returns its exit
// code along with whatever it wrote to stderr. Unlike captureStderr it makes
// no assumption about which code a failure should produce.
func captureStderrFor(t *testing.T, f func() int) (int, *bytes.Buffer) {
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
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return code, &buf
}
