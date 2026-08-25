// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build tokentop

package main

import (
	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/tokentop/agentusage"
)

// registerTranscript teaches the reader where a defined agent keeps its
// session transcripts, which is what gives a non-built-in agent live counts.
func registerTranscript(name string, u *agent.UsageSpec) error {
	return agentusage.RegisterSpec(name, agentusage.Spec{
		Roots:      u.Roots,
		Suffix:     u.Suffix,
		Cumulative: u.Cumulative,
		HeaderCwd:  u.HeaderCwd,
	})
}

// tokenSourceLine tells doctor which token sources this build can read.
const tokenSourceLine = "Token rates: from what agents print, plus their session transcripts (via tokentop)."
