// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !tokentop

package main

import "github.com/maci0/gauntlet/internal/agent"

// registerTranscript is a no-op: this build does not read transcripts, so a
// definition's usage block has nowhere to go. Build with `-tags tokentop` to
// make it count.
func registerTranscript(string, *agent.UsageSpec) error { return nil }

// tokenSourceLine tells doctor which token sources this build can read.
const tokenSourceLine = "Token rates: from what agents print (build with -tags tokentop to also read their session transcripts)."
