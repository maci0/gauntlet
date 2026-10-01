// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/runner"
)

// A crash checkpoint is what `gauntlet resume` picks the run back up from, so
// the two numbers it carries decide how the rest of the run is treated: the
// stamp answers "is this process still alive" and the handoff's elapsed is
// carried into the successor's startedAt, where it feeds --runtime. Both were
// read from the process's own monotonic clock instead of the run clock, which
// left the checkpoint as the one artifact in the run that no replay could
// reproduce: two runs of one seed under one frozen run clock wrote identical
// journals and two different checkpoints.
//
// The assertion is the pairing the clock promises everywhere else: the stamp
// and the elapsed come from the same reading, and both are that reading. A
// checkpoint stamped from the wall clock while its elapsed comes from the run
// clock fails the second check; one stamped from neither fails both.
func TestCheckpointStampsFromTheRunClock(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())

	const runID = "20261001T015123Z-1a2b"
	origin := time.Date(2026, 10, 1, 1, 51, 23, 0, time.UTC)
	// An hour into the run, so the elapsed is a figure a reader can check by
	// hand and a zero would not pass for a clock that was never asked.
	at := origin.Add(time.Hour)
	runs := []*dirRun{{dir: t.TempDir()}}

	bus := runner.NewBus()
	progress := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		writeCheckpoints(progress, runs, runID, origin, origin, 4242, 0,
			[]string{"--once"}, bus, func() time.Time { return at })
	}()

	progress <- struct{}{}
	close(progress)
	<-done

	cp, err := readCheckpoint(mustCheckpointPath(t, runID))
	if err != nil {
		t.Fatalf("read the checkpoint the writer left: %v", err)
	}
	if !cp.Updated.Equal(at) {
		t.Errorf("checkpoint stamped %s, want the run clock's %s", cp.Updated, at)
	}
	if got := cp.Handoff.Elapsed; got != time.Hour {
		t.Errorf("handoff elapsed %s, want %s measured from the same reading", got, time.Hour)
	}
	// The pairing: elapsed is the stamp's distance from the origin, not a
	// second measurement of its own. A writer that stamped correctly and
	// measured independently passes the two checks above and fails this one.
	if got := at.Sub(origin); got != cp.Handoff.Elapsed {
		t.Errorf("elapsed %s does not match the stamp's %s since the origin",
			cp.Handoff.Elapsed, got)
	}
}

// mustCheckpointPath resolves a run's checkpoint file, failing the test when
// the writer never produced one.
func mustCheckpointPath(t *testing.T, runID string) string {
	t.Helper()
	path, err := checkpointPath(runID)
	if err != nil {
		t.Fatalf("checkpoint path for %s: %v", runID, err)
	}
	return path
}
