// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gauntlethome resolves the root of gauntlet's state tree: the
// directory holding the run journal, the hot-reload handoff files, and
// agents.json. Every consumer of that root resolves it here, so two
// copies of the rule cannot drift apart.
package gauntlethome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// ExpandPath expands $VARIABLES and a leading ~ in a user-supplied path.
//
// Every flag or file entry that takes a directory or file from the user goes
// through this one function, so --dir, --dirs, --log, --prompt-dir, and
// --bin all expand the same way. A bare "~" or "~user" is left alone: only
// "~/..." names something under HOME.
//
// A $VAR or ${VAR} whose environment variable is unset or empty is an error:
// replacing it with nothing would turn --dir $TYPO into the current
// directory, --prompt-dir $TYPO into the bundled prompts, and --log $TYPO
// into a silently dropped log. A leading ~/ with no usable HOME is the same
// class of miss: the path would otherwise be taken relative to cwd.
func ExpandPath(p string) (string, error) {
	var missing []string
	seen := map[string]bool{}
	expanded := os.Expand(p, func(key string) string {
		v, ok := os.LookupEnv(key)
		if !ok || v == "" {
			if !seen[key] {
				seen[key] = true
				missing = append(missing, key)
			}
			return ""
		}
		return v
	})
	if len(missing) == 1 {
		return "", fmt.Errorf("environment variable %s is unset or empty", missing[0])
	}
	if len(missing) > 1 {
		return "", fmt.Errorf("environment variables %s are unset or empty", strings.Join(missing, ", "))
	}
	after, ok := strings.CutPrefix(expanded, "~"+string(os.PathSeparator))
	if !ok {
		return expanded, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand ~: home directory is unknown")
	}
	return filepath.Join(home, after), nil
}
