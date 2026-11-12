// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"
)

// The tally is the one row that answers whether anything broke, and a short
// terminal used to drop it first: trimming from the top kept the header and
// the keys, so a terminal two rows tall showed the state with no outcome
// beside it. The resize hint and the running-work rows are what goes instead.
func TestMinimalViewKeepsTheTallyOnAShortTerminal(t *testing.T) {
	for _, h := range []int{2, 3, 4, 5} {
		m := newModel(demoConfig())
		m.w, m.h, m.ready = 60, h, true
		for _, ev := range demoEvents() {
			m.apply(ev)
		}
		got := stripANSI(m.renderMinimal())
		if !strings.Contains(got, "pass 1") || !strings.Contains(got, "timeout 1") {
			t.Errorf("h=%d: the fallback lost the outcome tally:\n%s", h, got)
		}
		if got := len(strings.Split(m.renderMinimal(), "\n")); got > h {
			t.Errorf("h=%d: the fallback is %d rows", h, got)
		}
	}
}
