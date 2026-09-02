// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"time"
)

// transcriptReader follows an agent's own session transcript and reports the
// tokens it spends there.
//
// This is the optional half of gauntlet's token accounting. The always-present
// half reads whatever an agent prints on stdout, which covers every agent with
// a counter or a machine-readable mode. Transcripts cover the rest, but knowing
// where each CLI keeps them, whose counters are cumulative, and which field
// means output is a monitoring concern that belongs to a monitoring tool.
//
// So it lives in toktop, and gauntlet links it in a default build; the
// `-tags notoktop` build drops it, the methods below become no-ops, and the
// stdout reading stands alone: fewer numbers for the quietest agents, no
// dependency, and nothing that pretends to a measurement it does not have.
type transcriptReader interface {
	// Run reports usage as it grows, until the context is canceled.
	Run(ctx context.Context, onChange func(output, thinking int))
	// Final takes one last reading, for the records an agent writes as it
	// exits. It returns zeros when there is nothing to read.
	Final() (output, thinking int)
}

// nopReader is what a build without the tag uses, and what a supported build
// uses for an agent that keeps no readable transcript.
type nopReader struct{}

func (nopReader) Run(context.Context, func(int, int)) {}
func (nopReader) Final() (int, int)                   { return 0, 0 }

// openTranscript builds the reader that tails an agent's session log. A var
// so tests can substitute a reader that outlives the agent, which is the
// race the join in runReview is there to close.
var openTranscript = newTranscriptReader

// watchTranscript returns a reader for one agent working in one directory.
// since bounds what counts: anything written before it belongs to whatever ran
// earlier.
//
// The implementation is chosen at build time; see usage_toktop.go and
// usage_off.go.
func watchTranscript(tool, dir string, since time.Time) transcriptReader {
	return openTranscript(tool, dir, since)
}
