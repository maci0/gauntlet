// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gitx

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"syscall"

	"github.com/maci0/gauntlet/internal/runx"
)

// The oldest git this package can talk to. The calls that take a ref or path
// the reviewed repository supplied separate it from the options with
// `--end-of-options` (git 2.24), and branches are created with `git switch`
// (2.23), so an older git answers "unknown option" where a broken repository
// would have answered something else. README states the floor; BelowFloor is
// how `gauntlet doctor` checks the machine against it.
const MinVersion = "2.24"

// minVersion is MinVersion as the numbers BelowFloor compares.
var minVersion = mustParseVersion(MinVersion)

// versionMaxBytes bounds what `git --version` may print. The line is one
// version and, on macOS, a distribution suffix.
const versionMaxBytes = 256

// versionRe finds the version in a `git --version` line; parseVersion reads
// the pair.
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// Version returns the first line of `git --version` for the git on PATH, as
// git printed it ("git version 2.43.0"), or "" when git is missing, not
// executable, or printed nothing. The same PATH memo gitPath keeps applies, so
// this and a run's first git call name the same binary.
func Version(ctx context.Context) string {
	git := gitPath()
	if git == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, gitQuick)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, "--version")
	cmd.Env = runx.AbsPATHEnv()
	cmd.Stdin = nil
	out, _ := runx.Bound(cmd, versionMaxBytes, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if err := runx.Outcome(ctx, cmd.Run()); err != nil && out.String() == "" {
		return ""
	}
	return runx.FirstLine(out.String())
}

// BelowFloor reports whether a `git --version` line names a git older than the
// minimum, and whether the line carried a version at all. An unreadable
// version is not a pass: a caller that cannot read it has no ground to claim
// the machine meets the floor, and says so in its own words.
func BelowFloor(line string) (below, known bool) {
	major, minor, ok := parseVersion(line)
	if !ok {
		return false, false
	}
	if major != minVersion[0] {
		return major < minVersion[0], true
	}
	return minor < minVersion[1], true
}

// parseVersion reads the first dotted number pair in a `git --version` line,
// so a distribution suffix ("git version 2.39.3 (Apple Git-146)", "git
// version 2.43.0.windows.1") does not decide the comparison.
func parseVersion(line string) (major, minor int, ok bool) {
	m := versionRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(m[2])
	if err != nil || minor < 0 {
		return 0, 0, false
	}
	return major, minor, true
}

// mustParseVersion reads the floor constant, so a malformed one fails at
// package load rather than as every call answering "old git".
func mustParseVersion(s string) [2]int {
	major, minor, ok := parseVersion(s)
	if !ok {
		panic("gitx: MinVersion is not a version: " + s)
	}
	return [2]int{major, minor}
}
