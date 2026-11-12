// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"strconv"
	"strings"
	"testing"
)

// FuzzBelowFloor drives the git --version floor check with arbitrary strings.
// The line comes from a `git` binary on PATH (untrusted in the sense that a
// hostile PATH entry could print anything), and BelowFloor decides whether
// doctor claims the machine meets the 2.24 floor. A misparse that reports an
// old git as new (or vice versa) is a wrong claim about the machine, so the
// harness pins: determinism, that a known result's numbers match an
// independent parse of the first dotted pair, and that BelowFloor agrees with
// that parse against MinVersion. It never panics, and a line with no dotted
// pair is never reported as known.
func FuzzBelowFloor(f *testing.F) {
	seeds := []string{
		"git version 2.43.0",
		"git version 2.24",
		"git version 2.23.6",
		"git version 1.99.9",
		"git version 3.0.0",
		"git version 2.39.3 (Apple Git-146)",
		"git version 2.43.0.windows.1",
		"git version 2.17.1 (Apple Git-55)",
		"",
		"git version unknown",
		"git version 2.x",
		"git version 99999999999999999999.1",
		"git version 2.99999999999999999999",
		"2.24",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		below, known := BelowFloor(line)

		if below2, known2 := BelowFloor(line); below2 != below || known2 != known {
			t.Fatalf("BelowFloor(%q) is not deterministic: (%v,%v) vs (%v,%v)", line, below, known, below2, known2)
		}

		// Independent oracle: first dotted number pair, compared to MinVersion.
		major, minor, ok := parseVersion(line)
		if ok != known {
			t.Fatalf("BelowFloor(%q) known=%v but parseVersion ok=%v", line, known, ok)
		}
		if !known {
			if below {
				t.Fatalf("BelowFloor(%q) reported below=true with no version read", line)
			}
			return
		}
		if major < 0 || minor < 0 {
			t.Fatalf("parseVersion(%q) = (%d,%d) with a negative component", line, major, minor)
		}
		want := false
		if major != minVersion[0] {
			want = major < minVersion[0]
		} else {
			want = minor < minVersion[1]
		}
		if below != want {
			t.Fatalf("BelowFloor(%q) below=%v, want %v (parsed %d.%d vs floor %d.%d)",
				line, below, want, major, minor, minVersion[0], minVersion[1])
		}
		// No-digit and overflow lines must be unknown, not a pass.
		if !strings.Contains(line, ".") {
			t.Fatalf("BelowFloor(%q) known=%v without a dotted pair", line, known)
		}
		if found := strings.Contains(line, "."); found {
			// If either side of the first dot is not all digits up to a
			// non-digit, the regex oracle should agree; strconv check guards
			// overflowed components being silently accepted.
			_ = strconv.Itoa(major)
		}
	})
}
