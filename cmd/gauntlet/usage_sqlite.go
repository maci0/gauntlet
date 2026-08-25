// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build sqlite

package main

// crushSourceNote says that crush's own database is readable in this build.
// crush keeps per-session counters in SQLite inside the project, which is why
// it needs the driver this tag links in.
const crushSourceNote = ", and crush's project database"
