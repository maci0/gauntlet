// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !sqlite

package main

// crushSourceNote says what this build cannot read: crush records its token
// counts in a SQLite database inside the project, and no driver is linked in.
const crushSourceNote = " (crush reports nothing: its counters live in a SQLite database, build with -tags sqlite)"
