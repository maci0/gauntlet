// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build notoktop

package main

import "github.com/maci0/gauntlet/internal/agent"

// registerTranscript is a no-op: this build opted out of transcript reading
// with `-tags notoktop`, so a definition's usage block has nowhere to go.
func registerTranscript(string, *agent.UsageSpec) error { return nil }

// enableOpenCodeDB reports that this build cannot read opencode's database.
func enableOpenCodeDB() bool { return false }

// tokenSourceLine tells doctor which token sources this build can read.
const tokenSourceLine = "Token rates: from what agents print (this build opted out of reading session transcripts with -tags notoktop)" +
	crushSourceNote + "."
