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
// So it lives in toktop, and gauntlet reaches for it only when built with
// `-tags toktop`. Without that tag the methods below are a no-op and the
// stdout reading stands alone: fewer numbers for the quietest agents, no
// dependency, and nothing that pretends to a measurement it does not have.
// maxPlausibleTokens bounds a counter read from an agent's own store, the way
// the output parser bounds one read from its stdout: above this it is a
// misread, not a measurement.
const maxPlausibleTokens = 1 << 40

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

// watchTranscript returns a reader for one agent working in one directory.
// since bounds what counts: anything written before it belongs to whatever ran
// earlier.
//
// The implementation is chosen at build time; see usage_toktop.go and
// usage_off.go.
func watchTranscript(tool, dir string, since time.Time) transcriptReader {
	// crush keeps its sessions in a SQLite file inside the project rather than
	// a JSONL under $HOME, so it is read here rather than by the transcript
	// readers, and only by a build that linked a driver.
	if tool == "crush" {
		if r := newCrushReader(dir, since); r != nil {
			return r
		}
		return nopReader{}
	}
	return newTranscriptReader(tool, dir, since)
}
