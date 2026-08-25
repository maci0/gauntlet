// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gauntlethome resolves the root of gauntlet's state tree: the
// directory holding the run journal, the hot-reload handoff files, and
// agents.json. Every consumer of that root resolves it here, so two
// copies of the rule cannot drift apart.
package gauntlethome

import (
	"os"
	"path/filepath"
)

// Dir returns the state root and whether it rests on a usable HOME.
//
// Precedence: GAUNTLET_HOME when set to anything non-empty, else $HOME/.gauntlet.
// A GAUNTLET_HOME that is not already absolute is resolved against the
// working directory once, here, so every later read of the root agrees no
// matter where in the process it happens.
//
// The boolean is false only when neither source applies: GAUNTLET_HOME unset
// and no usable HOME. Dir then degrades to ".gauntlet" beside the working
// directory. That fallback is acceptable for the journal (nothing it writes is
// load-bearing) and must be refused for anything carrying executable argv:
// a definitions file picked up from there would let the reviewed tree define
// its own agents.
func Dir() (string, bool) {
	if h := os.Getenv("GAUNTLET_HOME"); h != "" {
		return absolute(h), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gauntlet", false
	}
	return filepath.Join(home, ".gauntlet"), true
}

// absolute resolves p against the working directory. If the working directory
// cannot be determined, p passes through unchanged rather than failing a path
// that may well be fine.
func absolute(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
