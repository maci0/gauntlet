// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// CHANGELOG.md states the consumer contract: CLI flags, the commands, the
// environment variables, the exit codes, and every importable non-internal
// Go package in this module are API. Removing or renaming one is breaking
// and waits for the next major version; additions may land in a minor.
// These snapshots turn drift into a failed test, so every change to the
// surface is conscious, lands in the same commit as its changelog entry,
// and gets the right bump kind.
//
// The flags snapshot rides on TestHelpMatchesTheRealFlags: helpGroups is
// already proven equal to the registered flag set, so pinning help pins what
// a consumer can type. The environment snapshot rides on helpEnvVars, whose
// color names are compile-time tied to what colorEnabled reads.

// goldenFlagNames is every flag name (long and short) consumers can pass.
var goldenFlagNames = []string{
	"1", "C", "V",
	"a", "agent-cmd", "agents", "auto-update",
	"bin",
	"c", "check", "commit", "continue-sessions",
	"dir", "dirs", "dry-run",
	"exclude",
	"h", "help", "hot-reload",
	"j", "jobs",
	"l", "limit", "list", "log",
	"max-loops", "merge-into",
	"n", "no-color",
	"once", "opencode-db",
	"p", "pr-base", "prompt-dir", "push", "push-remote",
	"q", "quiet",
	"r", "raw", "resolve-conflicts", "retries", "reviews", "runtime",
	"s", "seed", "semcode", "show-prompt", "stacked-prs", "stream", "suggest",
	"suggest-agent", "suggest-timeout",
	"t", "target-dirs", "timeout", "tui",
	"update-repo",
	"version",
	"x",
	"y", "yolo", "yes",
}

// goldenCommands is the command surface of the binary.
var goldenCommands = []string{
	"gauntlet [flags]",
	"gauntlet pick",
	"gauntlet doctor",
	"gauntlet update [--check]",
	"gauntlet runs [--limit N]",
	"gauntlet show <run-id>",
	"gauntlet version",
	"gauntlet help",
}

// goldenExitCodes is the exit-code contract documented in docs/CLI.md.
var goldenExitCodes = []string{"0", "1", "2", "75", "130"}

// goldenEnvVars is the environment-variable surface: the names docs/CLI.md
// and the help screen tell consumers to set. GAUNTLET_STATE is deliberately
// absent; docs/CLI.md documents it as a hot-reload handoff detail, not part
// of the contract.
var goldenEnvVars = []string{
	"CLICOLOR_FORCE",
	"FORCE_COLOR",
	"GAUNTLET_HOME",
	"GAUNTLET_NO_ANIMATION",
	"GITHUB_TOKEN",
	"NO_COLOR",
	"TERM",
}

func TestFlagNamesMatchTheContract(t *testing.T) {
	var got []string
	for name := range documentedFlags() {
		got = append(got, name)
	}
	assertSurfaceUnchanged(t, "flag name", goldenFlagNames, got)
}

func TestCommandNamesMatchTheContract(t *testing.T) {
	got := make([]string, 0, len(helpCommands))
	for _, c := range helpCommands {
		got = append(got, c.Cmd)
	}
	assertSurfaceUnchanged(t, "command name", goldenCommands, got)
}

func TestExitCodesMatchTheContract(t *testing.T) {
	got := make([]string, 0, len(helpExitCodes))
	for _, c := range helpExitCodes {
		got = append(got, c.Code)
	}
	assertSurfaceUnchanged(t, "exit code", goldenExitCodes, got)
}

func TestEnvVarNamesMatchTheContract(t *testing.T) {
	got := make([]string, 0, len(helpEnvVars))
	for _, e := range helpEnvVars {
		got = append(got, e.Name)
	}
	assertSurfaceUnchanged(t, "environment variable", goldenEnvVars, got)
}

// A package outside internal/ that is not a main package is importable by
// other programs, which makes its exported API part of the consumer contract
// whether it was meant to be or not. New code belongs under internal/; a
// deliberately public package updates this test and the changelog together.
func TestNoAccidentalPublicPackages(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(root, "cmd")
	internalDir := filepath.Join(root, "internal")

	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			switch {
			case path == root:
				return nil
			case strings.HasPrefix(name, "."), name == "testdata":
				return fs.SkipDir
			case path == cmdDir || path == internalDir,
				path == filepath.Join(root, "dist"),
				path == filepath.Join(root, "assets"),
				path == filepath.Join(root, "docs"):
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		if slices.Contains(found, dir) {
			return nil
		}
		found = append(found, dir)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("Go packages outside internal/ and cmd/ are importable by other programs and become consumer-facing API under CHANGELOG.md's contract: %s. Move the code under internal/, or accept the public surface deliberately: update this test and record the package in CHANGELOG.md.",
			strings.Join(found, ", "))
	}
}

// assertSurfaceUnchanged fails with the SemVer consequence spelled out,
// separating removals from additions so the required bump kind is obvious.
func assertSurfaceUnchanged(t *testing.T, what string, want, got []string) {
	t.Helper()
	sort.Strings(want)
	sorted := make([]string, len(got))
	copy(sorted, got)
	sort.Strings(sorted)
	removed, added := nameDiff(want, sorted)
	if len(removed) == 0 && len(added) == 0 {
		return
	}
	t.Fatalf("the %s surface changed; CHANGELOG.md's consumer contract makes these names API\n  removed: %s\n  added:   %s\nremovals and renames are breaking and wait for the next major version; additions may land in a minor. Record the change in CHANGELOG.md and update the snapshot in this test in the same commit.",
		what, quoteAll(removed), quoteAll(added))
}

func nameDiff(want, got []string) (missing, extra []string) {
	unseen := make(map[string]bool, len(want))
	for _, n := range want {
		unseen[n] = true
	}
	for _, n := range got {
		if unseen[n] {
			delete(unseen, n)
			continue
		}
		extra = append(extra, n)
	}
	for n := range unseen {
		missing = append(missing, n)
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}
