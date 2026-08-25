// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build notoktop

package runner

import "time"

// newTranscriptReader reports nothing: this build opted out of transcript
// reading with `-tags notoktop`, so tokens come only from what agents print.
func newTranscriptReader(string, string, time.Time) transcriptReader { return nopReader{} }

// transcriptSource names where the optional numbers come from, for `doctor`.
const transcriptSource = ""
