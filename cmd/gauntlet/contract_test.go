// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// CHANGELOG.md states the consumer contract: CLI flags, the commands, the
// environment variables, the exit codes, and the run journal's event stream
// are API. Removing or renaming one is breaking and waits for the next major
// version; additions may land in a minor. There is no Go API in the contract,
// because nothing here is importable; TestNoAccidentalPublicPackages is what
// keeps that true. These snapshots turn drift into a failed test, so every
// change to the surface is conscious, lands in the same commit as its
// changelog entry, and gets the right bump kind.
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
	"j", "jobs", "json", "keep-runs",
	"l", "limit", "list", "log",
	"max-loops", "max-reviews", "merge-into",
	"n", "no-color", "no-sandbox",
	"once", "opencode-db",
	"p", "paths", "pr-base", "prompt-dir", "push", "push-remote",
	"q", "quiet",
	"r", "raw", "resolve-conflicts", "restore", "retries", "reviews", "runtime",
	"s", "sandbox-write", "seed", "semcode", "show-prompt", "stacked-prs", "stream", "suggest",
	"suggest-agent", "suggest-timeout",
	"t", "target-dirs", "timeout", "token-budget", "tui",
	"update-repo", "usage-cmd", "usage-limit",
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
	"gauntlet runs [--limit N] [--json]",
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
	"GIT_SSH_COMMAND",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"NO_COLOR",
	"NO_MOTION",
	"REDUCED_MOTION",
	"TERM",
	"TMPDIR",
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

// docs/CLI.md is consumer-facing documentation of the contract surface.
// mentionsFlag reports whether text spells the long flag on its own. A bare
// substring search lets a longer flag carry a shorter one: --dirs satisfied
// --dir, --push-remote satisfied --push, --suggest-agent satisfied --suggest.
func mentionsFlag(text, name string) bool {
	for at := 0; ; {
		i := strings.Index(text[at:], "--"+name)
		if i < 0 {
			return false
		}
		end := at + i + len(name) + 2
		if end == len(text) || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789-", rune(text[end])) {
			return true
		}
		at = end
	}
}

// A flag, environment variable, or exit code missing from docs/CLI.md is an
// undocumented API or contract drift.
func TestDocsCLIMatchesTheContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range goldenFlagNames {
		if len(name) < 2 {
			continue
		}
		if !mentionsFlag(text, name) {
			t.Errorf("docs/CLI.md does not document flag --%s; flags are consumer contract API", name)
		}
	}
	for _, env := range goldenEnvVars {
		if !strings.Contains(text, env) {
			t.Errorf("docs/CLI.md does not document environment variable %s; env vars are consumer contract API", env)
		}
	}
	for _, code := range goldenExitCodes {
		needle := "| " + code + " |"
		if !strings.Contains(text, needle) {
			t.Errorf("docs/CLI.md does not document exit code %s in its exit codes table; exit codes are consumer contract API", code)
		}
	}
}

// goldenFlagDefaults are the flag defaults docs/CLI.md states as a literal
// value, which the parser's own DefValue spells the same way. A default is
// documented behavior, so it is contract as the flag's name is: raising
// --keep-runs, retrialing a review a third time by default, or repointing
// --update-repo changes what every consumer who never typed the flag gets,
// and the flag-name snapshot would report nothing, because no name moved.
// Defaults docs/CLI.md words rather than spells (unlimited, off, none,
// auto-detect, a duration) are a reader's, not a comparison's: a change to
// one shows in the help screen and belongs in the changelog.
var goldenFlagDefaults = map[string]string{
	"jobs":        "1",
	"keep-runs":   "200",
	"limit":       "20",
	"push-remote": "origin",
	"retries":     "2",
	"update-repo": "maci0/gauntlet",
}

// A default that moved out from under the documentation is a release that
// changes behavior without changing a name, which is the one kind of contract
// drift no other snapshot here can see.
func TestFlagDefaultsMatchTheContract(t *testing.T) {
	fs, _ := buildFlagSet(&options{})
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := docsCLIDefaults(string(data))
	for name, want := range goldenFlagDefaults {
		f := fs.Lookup(name)
		if f == nil {
			t.Errorf("the contract names a default for --%s, which is not a flag", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("--%s defaults to %q, not the contracted %q; a default is documented behavior, so the change needs a CHANGELOG entry and the right bump kind", name, f.DefValue, want)
		}
		rows := documented[name]
		if len(rows) == 0 {
			t.Errorf("docs/CLI.md has no default for --%s", name)
			continue
		}
		for _, got := range rows {
			if got != want {
				t.Errorf("docs/CLI.md documents --%s as defaulting to %q, not %q", name, got, want)
			}
		}
	}
}

// docsCLIFlagName reads the long flag out of a table cell like
// “-j, --jobs N“, which is how docs/CLI.md spells a flag with a metavar.
var docsCLIFlagName = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

// docsCLIDefaults reads the Default column of the flag tables under
// ## Options in docs/CLI.md, keyed by the long flag name, one entry per table
// that states one. Two tables describing one flag with two defaults is drift
// the caller sees, so nothing is collapsed here. The other tables are left
// out: the Commands table spells a flag inside a command line, and Modes and
// Output state what a switch does rather than what it defaults to.
func docsCLIDefaults(text string) map[string][]string {
	out := map[string][]string{}
	inOptions := false
	for line := range strings.SplitSeq(text, "\n") {
		switch {
		case line == "## Options":
			inOptions = true
			continue
		case inOptions && strings.HasPrefix(line, "## "):
			inOptions = false
			continue
		case !inOptions || !strings.HasPrefix(line, "| `"):
			continue
		}
		// A leading empty cell, the flag, its default, and the purpose.
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		m := docsCLIFlagName.FindStringSubmatch(cells[1])
		if m == nil {
			continue
		}
		def := strings.Trim(strings.TrimSpace(cells[2]), "`")
		out[m[1]] = append(out[m[1]], def)
	}
	return out
}

// .env.example is how a consumer discovers the variables this binary reads
// without reading the help screen. Nothing ties it to the contract, so a new
// variable could ship documented in docs/CLI.md and missing from the template,
// or a stale one could linger with no reader. Both directions are checked
// here: every contracted name appears, and every name in the file is
// documented in docs/CLI.md.
func TestEnvExampleMatchesTheContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	cli, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(cli)

	var listed []string
	for line := range strings.SplitSeq(string(data), "\n") {
		entry := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		if strings.ContainsFunc(name, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_'
		}) {
			continue
		}
		listed = append(listed, name)
	}
	if len(listed) == 0 {
		t.Fatal(".env.example names no variables; it is the discovery surface for every variable this binary reads")
	}
	for _, env := range goldenEnvVars {
		if !slices.Contains(listed, env) {
			t.Errorf(".env.example does not mention %s; every contracted variable needs a template entry", env)
		}
	}
	for _, name := range listed {
		if !strings.Contains(text, name) {
			t.Errorf(".env.example lists %s, which docs/CLI.md does not document", name)
		}
	}
}

// A package outside internal/ that is not a main package is importable by
// other programs, which makes its exported API part of the consumer contract
// whether it was meant to be or not. New code belongs under internal/; a
// deliberately public package updates this test and the changelog together.
//
// CHANGELOG.md's contract says the same thing about the packages under cmd/:
// they are not importable, so changing them freely is not a major bump. That
// holds only while every one of them is a main package, so cmd/ is walked too
// and its package clause is read rather than assumed.
func TestNoAccidentalPublicPackages(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(root, "cmd")
	internalDir := filepath.Join(root, "internal")

	var found []string
	nonMain := map[string]string{}
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
			case path == internalDir,
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
		if !strings.HasPrefix(dir, cmdDir) {
			if slices.Contains(found, dir) {
				return nil
			}
			found = append(found, dir)
			return nil
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		clause := packageClause(path)
		if prev, ok := nonMain[rel]; ok && prev != clause {
			nonMain[rel] = "mixed: " + prev + " and " + clause
		} else if !ok {
			nonMain[rel] = clause
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("Go packages outside internal/ and cmd/ are importable by other programs and become consumer-facing API under CHANGELOG.md's contract: %s. Move the code under internal/, or accept the public surface deliberately: update this test and record the package in CHANGELOG.md.",
			strings.Join(found, ", "))
	}
	for rel, clause := range nonMain {
		if clause != "main" {
			t.Errorf("%s is `package %s` under cmd/, so it is importable by other programs and its exported API is consumer surface: CHANGELOG.md promises no Go API to break, and a non-main package under cmd/ makes that promise false. Make it `package main`, move it under internal/, or accept the surface deliberately and update this test and the contract together.",
				rel, clause)
		}
	}
}

// packageClause returns the package name a .go file declares. A file whose
// clause is a build-constrained variant of the same name ("main_test") is the
// package itself; anything the parser cannot read comes back as "" and the
// caller's comparison fails, which is the safe direction.
func packageClause(path string) string {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	if f.Name == nil {
		return ""
	}
	return f.Name.Name
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
