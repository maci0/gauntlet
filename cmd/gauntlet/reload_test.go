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

	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
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

func TestResumeStartDoesNotMoveIntoTheFuture(t *testing.T) {
	now := time.Now()
	for _, prior := range []handoff{
		{StartedAt: now.Add(time.Hour)},
		{Elapsed: -time.Minute},
		{Elapsed: time.Duration(-1 << 63)},
	} {
		started := resumeStart(now, prior)
		if got := now.Sub(started); got != 0 {
			t.Errorf("invalid elapsed must resume at zero, got %s", got)
		}
	}
}

func TestResumeOriginClamped(t *testing.T) {
	now := time.Now()
	for _, prior := range []handoff{
		{StartedAt: now.Add(time.Hour)},
		{StartedAt: time.Time{}},
	} {
		origin := resumeOrigin(now, prior)
		if origin.IsZero() || origin.After(now) {
			t.Errorf("origin must be non-zero and not in future, got %v", origin)
		}
	}
}

// A clock that did not step keeps the handoff's own start, so the positive
// case holds the clamp above honest: clamping unconditionally would silently
// restart the runtime budget of every ordinary resume.
func TestResumeOriginKeepsAPlausibleHandoff(t *testing.T) {
	now := time.Now()
	prior := handoff{StartedAt: now.Add(-90 * time.Minute), Elapsed: 90 * time.Minute}
	if got := resumeOrigin(now, prior); !got.Equal(prior.StartedAt) {
		t.Fatalf("origin %v, want the handoff's own start %v", got, prior.StartedAt)
	}
}

func TestDoReloadAbortsWhenStateCannotBeSaved(t *testing.T) {
	// gauntlethome.StateDir() is GAUNTLET_HOME/state; make that uncreatable by
	// putting a regular file where the directory goes, so MkdirAll fails with
	// ENOTDIR. Blocking a parent of GAUNTLET_HOME instead no longer reaches
	// it: Dir refuses a root it cannot stat, and falls back to a working
	// directory that saves its handoff perfectly well.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_HOME", home)

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
		return doReload("", "20260827T120000Z-dead", start, 0, runs, handoff{}, 7, []string{}, &out)
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
	// The state root is isolated alongside the handoff itself: without it the
	// run reads the operator's own ~/.gauntlet/agents.json, and a duplicate
	// agent name in that file answers with a usage error before the handoff
	// is ever parsed, which is a verdict about the machine rather than about
	// the code under test.
	t.Setenv("GAUNTLET_HOME", t.TempDir())
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
	code, out := captureFD(t, &os.Stderr, f)
	return code, bytes.NewBufferString(out)
}

// The seed has to survive the exec: the journal records the number the run
// drew from, and a successor that resolved a fresh one would leave that
// number unable to replay the reviews the successor still had to run.
func TestHandoffCarriesTheSeed(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	path, err := selfupdate.SaveState(gauntlethome.StateDir(), "seeded-run",
		handoff{RunID: "seeded-run", Seed: 4242, Dirs: map[string]dirHandoff{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_STATE", path)

	var got handoff
	ok, err := selfupdate.LoadState(&got)
	if err != nil || !ok {
		t.Fatalf("LoadState = %v, %v; want the handoff back", ok, err)
	}
	if got.Seed != 4242 {
		t.Fatalf("the handoff seed is %d, want 4242", got.Seed)
	}

	// A handoff written before the field existed still loads; it just has no
	// seed to continue from.
	old := filepath.Join(t.TempDir(), "old.json")
	if err := os.WriteFile(old, []byte(`{"run_id":"seeded-run","dirs":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_STATE", old)
	var prior handoff
	if ok, err := selfupdate.LoadState(&prior); err != nil || !ok {
		t.Fatalf("LoadState = %v, %v; want an old handoff to load", ok, err)
	}
	if prior.Seed != 0 {
		t.Fatalf("an old handoff read a seed of %d, want 0", prior.Seed)
	}
}

// A directory's path is the key its progress is filed under, and a filesystem
// may hold any byte outside NUL and the separator. JSON rewrites a byte that is
// not valid UTF-8 to U+FFFD in both directions, so a raw key would come back
// under a name that is not the one it was written with: every lookup misses,
// the successor repeats the loops it had finished, and a resumed stack
// refetches a base it was pinned to. The same applies to a name the filesystem
// holds in a decomposed spelling, which JSON would carry through unchanged.
func TestHandoffFindsADirectoryWhoseNameDoesNotSurviveJSON(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	dir := string([]byte{'/', 't', 'm', 'p', 0xff, 0x65, '/'}) + "cafe\xcc\x81"
	written := handoff{RunID: "odd-dir", Dirs: map[string]dirHandoff{
		handoffKey(dir): {Loops: 3, Reviews: []string{"go-review"}},
	}}
	path, err := selfupdate.SaveState(gauntlethome.StateDir(), "odd-dir", written)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_STATE", path)

	var got handoff
	ok, err := selfupdate.LoadState(&got)
	if err != nil || !ok {
		t.Fatalf("LoadState = %v, %v; want the handoff back", ok, err)
	}
	carried := got.Dir(dir)
	if carried.Loops != 3 || len(carried.Reviews) != 1 {
		t.Fatalf("the successor found %d loops and %d reviews, want the 3 and 1 that were written",
			carried.Loops, len(carried.Reviews))
	}
}

// One seed per run: --seed wins, a resumed run continues its predecessor's,
// and only a run with neither derives one. Two clock reads here would hand the
// suggest step and the schedule different seeds, so the number printed on
// failure would replay only the schedule.
func TestEffectiveSeedPrefersTheFlagThenThePredecessor(t *testing.T) {
	now := func() time.Time { return time.Unix(0, 1700000000000000000).UTC() }
	if got := effectiveSeed(7, 99, now); got != 7 {
		t.Errorf("an explicit --seed must win, got %d", got)
	}
	if got := effectiveSeed(0, 99, now); got != 99 {
		t.Errorf("a resumed run must keep the interrupted seed, got %d", got)
	}
	if got := effectiveSeed(0, 0, now); got != uint64(now().UnixNano()) {
		t.Errorf("a fresh run derives its seed from the clock, got %d", got)
	}
}
