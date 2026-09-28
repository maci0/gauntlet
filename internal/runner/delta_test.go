// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"testing"

	"github.com/maci0/gauntlet/internal/gitx"
)

func TestDelta(t *testing.T) {
	tests := []struct {
		name          string
		before, after gitx.Stats
		wantIns       int
		wantDel       int
	}{
		{
			name:    "pure insertion",
			before:  gitx.Stats{},
			after:   gitx.Stats{Ins: 10},
			wantIns: 10,
		},
		{
			name:    "pure deletion",
			before:  gitx.Stats{},
			after:   gitx.Stats{Del: 7},
			wantDel: 7,
		},
		{
			name:    "add and delete in one review",
			before:  gitx.Stats{},
			after:   gitx.Stats{Ins: 5, Del: 3},
			wantIns: 5,
			wantDel: 3,
		},
		{
			// Deleting a file a previous review added moves Ins down and
			// Del up by the same count: one event, read two ways.
			name:    "revert of a previous insertion",
			before:  gitx.Stats{Ins: 10},
			after:   gitx.Stats{Ins: 0, Del: 10},
			wantDel: 10,
		},
		{
			// The partial case: three of ten added lines go back.
			name:    "partial revert of a previous insertion",
			before:  gitx.Stats{Ins: 10},
			after:   gitx.Stats{Ins: 7, Del: 3},
			wantDel: 3,
		},
		{
			name:    "restoration of a previous deletion",
			before:  gitx.Stats{Del: 10},
			after:   gitx.Stats{},
			wantIns: 10,
		},
		{
			// Adding lines and restoring deleted ones are two events, and
			// both count as insertions.
			name:    "insertion beside a restoration",
			before:  gitx.Stats{Del: 3},
			after:   gitx.Stats{Ins: 5},
			wantIns: 8,
		},
		{
			name:   "no change",
			before: gitx.Stats{Ins: 4, Del: 2},
			after:  gitx.Stats{Ins: 4, Del: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ins, del := delta(tc.before, tc.after)
			if ins != tc.wantIns || del != tc.wantDel {
				t.Errorf("delta(%+v, %+v) = +%d/-%d, want +%d/-%d",
					tc.before, tc.after, ins, del, tc.wantIns, tc.wantDel)
			}
		})
	}
}
