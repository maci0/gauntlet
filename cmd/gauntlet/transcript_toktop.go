// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !notoktop

package main

import (
	"fmt"

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
//
// The roots arrive resolved: a definition writes them the way every other path
// in this program is written, with ~ and $VAR, and this is where those are
// turned into a path the reader can open. The reader expands neither $VAR nor
// anything else, so a root it received verbatim would name a directory that
// does not exist and the counts would silently stay at zero.
func registerTranscript(name string, u *agent.UsageSpec) error {
	roots, err := u.ResolvedRoots()
	if err != nil {
		return fmt.Errorf("custom agent %q: %w", name, err)
	}
	return agentusage.RegisterSpec(name, agentusage.Spec{
		Roots:      roots,
		Suffix:     u.Suffix,
		Cumulative: u.Cumulative,
		HeaderCwd:  u.HeaderCwd,
	})
}

// tokenSourceLine tells doctor which token sources this build can read.
var tokenSourceLine = "Token rates: from what agents print, plus their session transcripts (via toktop)" + crushNote()
