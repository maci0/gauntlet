// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"strings"
	"testing"
)

// A git older than MinVersion answers "unknown option: --end-of-options" to
// every call this package makes, so the floor is a comparison the distribution
// suffix must not be able to move.
func TestBelowFloor(t *testing.T) {
	cases := []struct {
		line  string
		below bool
		known bool
	}{
		{"git version 2.43.0", false, true},
		{"git version 2.24", false, true},
		{"git version 2.24.1", false, true},
		{"git version 2.23.6", true, true},
		{"git version 2.9.5", true, true},
		{"git version 1.99.9", true, true},
		{"git version 3.0.0", false, true},
		// macOS and the Windows installer append a distribution suffix.
		{"git version 2.39.3 (Apple Git-146)", false, true},
		{"git version 2.43.0.windows.1", false, true},
		{"git version 2.17.1 (Apple Git-55)", true, true},
		// Nothing to compare: not a pass, and not a claim of failure.
		{"", false, false},
		{"git version unknown", false, false},
		{"git version 2.x", false, false},
	}
	for _, c := range cases {
		below, known := BelowFloor(c.line)
		if below != c.below || known != c.known {
			t.Errorf("BelowFloor(%q) = (%v, %v), want (%v, %v)", c.line, below, known, c.below, c.known)
		}
	}
}

func TestMinVersionParses(t *testing.T) {
	if _, _, ok := parseVersion(MinVersion); !ok {
		t.Fatalf("MinVersion %q is not a version the comparison can read", MinVersion)
	}
}

func TestVersionReportsTheGitOnPath(t *testing.T) {
	line := Version(context.Background())
	if line == "" {
		t.Skip("no git on PATH")
	}
	if !strings.HasPrefix(line, "git version") {
		t.Fatalf("Version() = %q, want a `git version` line", line)
	}
	if _, known := BelowFloor(line); !known {
		t.Fatalf("Version() = %q, which carries no version to compare", line)
	}
}
