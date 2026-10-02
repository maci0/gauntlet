// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/report"
)

// The resume listing prints when each interrupted run was last written, so an
// operator tells a crash from yesterday from one an hour ago. Local wall clock
// alone cannot say that across a fall-back: one local hour is repeated, so the
// two checkpoints below are an hour apart as instants and land on the same
// wall clock. The offset is what tells them apart, and the listing carries it
// for the same reason the `runs` listing's STARTED column and humanize.Clock do.
func TestListCheckpointsSeparatesRepeatedWallClock(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("no zone database: %v", err)
	}
	// startCell renders in the location it is handed, and the listing hands it
	// time.Local, so the host is the zone under test here. time.Local is a
	// package var the runtime reads through, not one a TZ change refreshes,
	// which is why this assigns it rather than setting TZ.
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })

	t.Setenv("GAUNTLET_HOME", t.TempDir())
	// 2026-10-25 00:30 UTC and 01:30 UTC: 02:30 CEST and 02:30 CET, the same
	// wall clock an hour apart.
	stamps := []time.Time{
		time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC),
	}
	for i, at := range stamps {
		cp := checkpoint{
			Handoff: handoff{
				RunID: "20261025T00300Z-1a" + string(rune('0'+i)),
				Dirs:  map[string]dirHandoff{},
			},
			Updated: at,
			Argv:    []string{"gauntlet", "resume"},
		}
		if err := saveCheckpoint(cp); err != nil {
			t.Fatal(err)
		}
	}

	var sb strings.Builder
	if code := listCheckpoints(&sb, report.Palette{}); code != exitOK {
		t.Fatalf("listCheckpoints = %d:\n%s", code, sb.String())
	}
	out := sb.String()
	if want := "2026-10-25 02:30:00+0200"; !strings.Contains(out, want) {
		t.Errorf("listing is missing %q:\n%s", want, out)
	}
	if want := "2026-10-25 02:30:00+0100"; !strings.Contains(out, want) {
		t.Errorf("listing is missing %q:\n%s", want, out)
	}
}
