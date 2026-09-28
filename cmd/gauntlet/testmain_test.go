// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

// TestMain points the state root at an empty directory for the whole package.
//
// Every parseFlags call reaches configureAgents, which reads agents.json from
// the state root, and the root is $HOME/.gauntlet unless GAUNTLET_HOME says
// otherwise. Left alone that is the operator's own, so what the suite asserts
// depends on what the machine happens to carry: a definition written for real
// work is loaded into the tests, and the second parseFlags in the process then
// fails to register it again and reports the clash. A run of the suite on a
// workstation with a real agents.json is not the same run as CI's.
//
// A fresh directory holds no definitions, which is the state a new install is
// in. Tests that want definitions in the root set GAUNTLET_HOME themselves,
// and t.Setenv restores the value this sets when they are done.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gauntlet-state")
	if err != nil {
		panic("gauntlet: cannot create a temporary state root: " + err.Error())
	}
	os.Setenv("GAUNTLET_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
