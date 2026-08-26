// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"time"

	"github.com/maci0/gauntlet/internal/runner"
)

// Rendering one frame outside a running program is what the tests assert on
// and what scripts/shots.sh photographs. Nothing in the shipped binary calls
// it, so it lives here rather than beside the dashboard it renders.
// staticFrame renders one frame from a sequence of events, for tests and for
// non-interactive snapshots. It is the same code path the live view uses, so a
// layout regression shows up here too.
func staticFrame(cfg Config, events []runner.Event, w, h int) string {
	mod := newModel(cfg)
	mod.w, mod.h, mod.ready = w, h, true
	for _, ev := range events {
		mod.apply(ev)
	}
	mod.now = mod.cfg.Started.Add(90 * time.Second)
	mod.sampleActivity()
	return mod.View()
}
