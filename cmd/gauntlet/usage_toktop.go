// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !notoktop

package main

import (
	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/toktop/agentusage"
)

// enableOpenCodeDB turns on reading opencode's SQLite session store, which is
// the only way to see what an opencode review spends: it prints no counters
// and keeps no JSONL. It reports false unless this build also carries the
// `sqlite` tag, since the database driver is linked in by that tag alone.
func enableOpenCodeDB() bool { return agentusage.EnableOpenCodeDB(true) }

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
const tokenSourceLine = "Token rates: from what agents print, plus their session transcripts (via toktop)."
