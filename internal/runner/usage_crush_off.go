// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !sqlite

package runner

import "time"

// newCrushReader reports nothing: crush records its token counts in a SQLite
// database, and this build linked no driver to read one. Build with
// `-tags sqlite` (the same tag `--opencode-db` needs) to count crush's tokens.
func newCrushReader(string, time.Time) transcriptReader { return nil }
