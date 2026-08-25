// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !tokentop

package runner

import "time"

// newTranscriptReader reports nothing: this build reads tokens only from what
// agents print. Build with `-tags tokentop` to add transcript reading.
func newTranscriptReader(string, string, time.Time) transcriptReader { return nopReader{} }

// transcriptSource names where the optional numbers come from, for `doctor`.
const transcriptSource = ""
