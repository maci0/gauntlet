// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// documentedFlags is every flag name the help screen claims to have.
func documentedFlags() map[string]bool {
	out := map[string]bool{}
	for _, g := range helpGroups {
		for _, f := range g.Flags {
			for _, n := range f.names() {
				out[n] = true
			}
		}
	}
	return out
}

// registeredFlags is every flag the CLI actually accepts.
func registeredFlags() map[string]bool {
	fs, _ := buildFlagSet(&options{})
	out := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { out[f.Name] = true })
	return out
}

// A flag nobody documents is a flag nobody finds, and a documented flag that
// does not exist is a lie. Both are caught here.
func TestHelpMatchesTheRealFlags(t *testing.T) {
	doc, real := documentedFlags(), registeredFlags()
	for name := range real {
		if !doc[name] {
			t.Errorf("flag -%s exists but is missing from the help screen", name)
		}
	}
	for name := range doc {
		if !real[name] {
			t.Errorf("help screen documents -%s, which is not a real flag", name)
		}
	}
}

func TestUsageRendersEverySection(t *testing.T) {
	var b strings.Builder
	printUsage(&b, palette{}, 100)
	got := b.String()
	for _, want := range []string{
		"USAGE", "REVIEWS", "AGENTS", "EXECUTION", "MODES", "OUTPUT",
		"UPDATES", "HISTORY", "EXAMPLES", "EXIT CODES", "ENVIRONMENT",
		"gauntlet doctor", "--jobs", "GAUNTLET_HOME", "FORCE_COLOR", "gauntlet version",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help screen is missing %q", want)
		}
	}
	for line := range strings.SplitSeq(got, "\n") {
		if len(line) > 100 {
			t.Errorf("help line exceeds the terminal width (%d): %q", len(line), line)
		}
	}
}

func TestUsageNarrowTerminal(t *testing.T) {
	var b strings.Builder
	printUsage(&b, palette{}, 20) // clamped to a sane minimum, never panics
	if !strings.Contains(b.String(), "USAGE") {
		t.Fatal("narrow help lost its sections")
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	o, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.timeout != 30*time.Minute || o.jobs != 1 || !o.hotReload {
		t.Fatalf("unexpected defaults: %+v", o)
	}
	if o.reviewsSet {
		t.Fatal("an omitted --reviews must not count as explicit")
	}
}

func TestParseFlagsShorthandsAndConflicts(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string // substring of the expected error, empty means success
	}{
		{"once implies one loop", []string{"--once"}, ""},
		{"push implies commit", []string{"--push"}, ""},
		{"suggest with reviews", []string{"-s", "-r", "quick"}, "conflicts"},
		{"suggest mixed in", []string{"-r", "suggest,sec"}, "must be the only"},
		{"exclude suggest", []string{"-x", "suggest"}, "not a review name"},
		{"once with max-loops", []string{"-1", "-n", "3"}, "conflicts"},
		{"negative loops", []string{"-n", "-2"}, "must be >= 0"},
		{"negative seed", []string{"--seed", "-1"}, "must be a nonnegative integer"},
		{"garbage seed", []string{"--seed", "soon"}, "must be a nonnegative integer"},
		{"hex seed", []string{"--seed", "0x10"}, ""},
		{"zero jobs", []string{"-j", "0"}, "must be >= 1"},
		{"negative limit", []string{"runs", "--limit", "-3"}, "--limit must be >= 1"},
		{"zero limit", []string{"runs", "--limit", "0"}, "--limit must be >= 1"},
		{"dirs with dir", []string{"--dirs", "a", "-C", "b"}, "conflicts"},
		{"two modes", []string{"--list", "--dry-run"}, "mutually exclusive"},
		{"unknown agent", []string{"-a", "nope"}, "unknown tool"},
		{"bad duration", []string{"-t", "5x"}, "invalid duration"},
		{"zero timeout", []string{"-t", "0"}, "must be positive"},
		{"trailing argument", []string{"extra"}, "unknown command"},
		{"check outside update", []string{"--check"}, "--check requires 'gauntlet update'"},
		{"limit outside runs", []string{"--limit", "5"}, "--limit requires 'gauntlet runs'"},
		{"limit under show", []string{"show", "20260825T000000Z-abcd", "--limit", "5"},
			"--limit requires 'gauntlet runs'"},
		{"check under update", []string{"update", "--check"}, ""},
		{"limit under runs", []string{"runs", "--limit", "5"}, ""},
		{"version wins over scoping", []string{"-V", "--limit", "5"}, ""},
		{"stray jobs under runs", []string{"runs", "--jobs", "4"}, "does not apply to 'gauntlet runs'"},
		{"stray short flag keeps its spelling", []string{"runs", "-j", "4"}, "-j does not apply"},
		{"stray yolo under doctor", []string{"doctor", "--yolo"}, "does not apply to 'gauntlet doctor'"},
		{"stray tui under show", []string{"show", "20260825T000000Z-abcd", "--tui"},
			"does not apply to 'gauntlet show'"},
		{"doctor reads its bin", []string{"doctor", "--bin", "claude=/bin/sh"}, ""},
		{"pick reads its dir", []string{"pick", "-C", "."}, ""},
		{"global log follows runs", []string{"runs", "--log", "gauntlet.log"}, ""},
		{"no-color follows everything", []string{"runs", "--no-color"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := parseFlags(c.argv)
			if c.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got options %+v", c.want, o)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestParseFlagsOnceAndPush(t *testing.T) {
	o, err := parseFlags([]string{"--once", "--push"})
	if err != nil {
		t.Fatal(err)
	}
	if o.maxLoops != 1 {
		t.Fatalf("--once should mean one loop, got %d", o.maxLoops)
	}
	if !o.commit {
		t.Fatal("--push must imply --commit")
	}
}

// The flag's own help text promises "(0 = unlimited)"; the runner reads
// Runtime == 0 as unbudgeted, so parsing must not stand in the way.
func TestParseFlagsRuntimeZeroMeansUnlimited(t *testing.T) {
	for _, v := range []string{"0", "0s", "0m", " 0 "} {
		o, err := parseFlags([]string{"--runtime", v})
		if err != nil {
			t.Fatalf("--runtime %q: %v", v, err)
		}
		if o.runtime != 0 {
			t.Fatalf("--runtime %q parsed as %v", v, o.runtime)
		}
	}
	if _, err := parseFlags([]string{"--runtime", "-5m"}); err == nil {
		t.Fatal("negative --runtime must be rejected")
	}
}

func TestParseFlagsExplicitEmptyReviews(t *testing.T) {
	// An explicit empty value must stay explicit: expanding it to "everything"
	// is how a script would review a repo it never meant to touch.
	o, err := parseFlags([]string{"--reviews", ""})
	if err != nil {
		t.Fatal(err)
	}
	if !o.reviewsSet || o.reviews != "" {
		t.Fatalf("explicit empty --reviews lost: %+v", o)
	}
}

func TestParseFlagsRepeatableAndCommaLists(t *testing.T) {
	o, err := parseFlags([]string{"-r", "sec,doc", "-r", "perf", "-x", "test", "--dirs", "a,b", "--dirs", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if o.reviews != "sec,doc,perf" {
		t.Fatalf("reviews: %q", o.reviews)
	}
	if o.exclude != "test" {
		t.Fatalf("exclude: %q", o.exclude)
	}
	if strings.Join(o.dirs, "|") != "a|b|c" {
		t.Fatalf("dirs: %v", o.dirs)
	}
}

// The seed value must keep the flag package's base-0 uint64 parsing (hex
// literals and underscores), which the custom Value replaced.
func TestParseFlagsSeedLiterals(t *testing.T) {
	for _, c := range []struct {
		v    string
		want uint64
	}{
		{"42", 42},
		{"0x2a", 42},
		{"1_000", 1000},
	} {
		o, err := parseFlags([]string{"--seed", c.v})
		if err != nil {
			t.Fatalf("--seed %q: %v", c.v, err)
		}
		if o.seed != c.want {
			t.Errorf("--seed %q parsed as %d, want %d", c.v, o.seed, c.want)
		}
	}
}

func TestParseFlagsSubcommands(t *testing.T) {
	for _, c := range []struct{ argv, cmd string }{
		{"doctor", "doctor"}, {"update", "update"}, {"runs", "runs"}, {"version", "version"},
	} {
		o, err := parseFlags([]string{c.argv})
		if err != nil {
			t.Fatalf("%s: %v", c.argv, err)
		}
		if o.command != c.cmd {
			t.Errorf("%s parsed as %q", c.argv, o.command)
		}
	}
	o, err := parseFlags([]string{"show", "20260825T000000Z-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "show" || o.showRun != "20260825T000000Z-abcd" {
		t.Fatalf("show parsed wrong: %+v", o)
	}
	if _, err := parseFlags([]string{"show"}); err == nil {
		t.Fatal("show without an id should fail")
	}
}

func TestVersionFlagBecomesCommand(t *testing.T) {
	o, err := parseFlags([]string{"-V"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "version" {
		t.Fatalf("-V should print the version, got command %q", o.command)
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	fs, _ := buildFlagSet(&options{})
	fs.SetOutput(io.Discard)
	if _, err := parseFlags([]string{"-h"}); err != errHelp {
		t.Fatalf("-h should exit cleanly, got %v", err)
	}
}

// captureStdout swaps os.Stdout for a pipe, runs f, and returns what was
// written. Help must land on stdout so redirection can capture it.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = orig
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestHelpPrintsToStdout(t *testing.T) {
	for _, argv := range [][]string{{"-h"}, {"--help"}, {"doctor", "--help"}} {
		got := captureStdout(t, func() {
			if _, err := parseFlags(argv); err != errHelp {
				t.Errorf("%v: expected errHelp, got %v", argv, err)
			}
		})
		if !strings.Contains(got, "USAGE") || !strings.Contains(got, "--help") {
			t.Errorf("%v: help on stdout lost its content (%d bytes)", argv, len(got))
		}
	}
}

func TestUnknownCommandHintListsEveryCommand(t *testing.T) {
	_, err := parseFlags([]string{"bogus"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, cmd := range []string{"help", "doctor", "update", "runs", "show", "version"} {
		if !strings.Contains(err.Error(), cmd) {
			t.Errorf("hint %q does not mention accepted command %q", err, cmd)
		}
	}
}

// `gauntlet help` must answer with the help screen on stdout, exit 0, like
// every other subcommand-bearing CLI.
func TestHelpSubcommand(t *testing.T) {
	got := captureStdout(t, func() {
		if _, err := parseFlags([]string{"help"}); err != errHelp {
			t.Errorf("help should exit cleanly, got %v", err)
		}
	})
	if !strings.Contains(got, "USAGE") || !strings.Contains(got, "EXIT CODES") {
		t.Errorf("help subcommand lost its content (%d bytes)", len(got))
	}
}

func TestShowRejectsFlagWhereRunIdBelongs(t *testing.T) {
	_, err := parseFlags([]string{"show", "--limit"})
	if err == nil || !strings.Contains(err.Error(), "needs a run id") {
		t.Fatalf("--limit after show should read as a missing run id, got %v", err)
	}
}

// --help after show means help, like it does everywhere else: the run id is
// never allowed to start with '-', so there is no other reading.
func TestShowHelpStillMeansHelp(t *testing.T) {
	got := captureStdout(t, func() {
		if _, err := parseFlags([]string{"show", "--help"}); err != errHelp {
			t.Fatalf("show --help should exit cleanly, got %v", err)
		}
	})
	if !strings.Contains(got, "USAGE") {
		t.Errorf("show --help lost its content (%d bytes)", len(got))
	}
}

// A rejected value must say which flag rejected it.
func TestValueErrorsNameTheFlag(t *testing.T) {
	for _, c := range []struct {
		argv []string
		flag string
	}{
		{[]string{"--bin", "bogus"}, "--bin"},
		{[]string{"--agent-cmd", "bogus"}, "--agent-cmd"},
	} {
		_, err := parseFlags(c.argv)
		if err == nil {
			t.Errorf("%v: expected an error", c.argv)
			continue
		}
		if !strings.Contains(err.Error(), c.flag) {
			t.Errorf("error %q does not name %s", err, c.flag)
		}
	}
}
