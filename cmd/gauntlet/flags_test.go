// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/selfupdate"
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
		// Defaults the help screen promises must track the parser's consts.
		fmt.Sprintf("(default %dm)", int(defaultTimeout/time.Minute)),
		fmt.Sprintf("(default %d)", defaultRunsLimit),
		"(0 = unlimited)",
		selfupdate.DefaultRepo,
		"GH_TOKEN",
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
	if o.timeout != defaultTimeout || o.suggestTimeout != defaultTimeout ||
		o.jobs != 1 || o.retries != defaultRetries || o.runsLimit != defaultRunsLimit || !o.hotReload ||
		o.updateRepo != selfupdate.DefaultRepo {
		t.Fatalf("unexpected defaults: %+v", o)
	}
	if o.reviewsSet {
		t.Fatal("an omitted --reviews must not count as explicit")
	}
}

// The defaults const block exists so docs/CLI.md, the help table, and the
// parser cannot drift apart; these assertions make that hold for the flag
// package's own bookkeeping too. IntVar writes its value through on
// registration, so a literal here would silently override what the options
// struct initialized from the const.
func TestRegisteredDefaultsMatchTheConsts(t *testing.T) {
	fs, _ := buildFlagSet(&options{})
	for _, c := range []struct {
		name string
		want string
	}{
		{"retries", strconv.Itoa(defaultRetries)},
		{"limit", strconv.Itoa(defaultRunsLimit)},
		{"update-repo", selfupdate.DefaultRepo},
	} {
		f := fs.Lookup(c.name)
		if f == nil {
			t.Fatalf("flag -%s is not registered", c.name)
		}
		if f.DefValue != c.want {
			t.Errorf("-%s registers default %q, want %s (the const)", c.name, f.DefValue, c.want)
		}
	}
}

// --suggest composes with --reviews rather than fighting it: the triage step
// picks, and what a person names rides along, wherever the request arrives.
func TestSuggestComposesWithNamedReviews(t *testing.T) {
	cases := []struct {
		name    string
		argv    []string
		reviews string
		set     bool
	}{
		{"the flag alone", []string{"--suggest"}, "", false},
		{"the flag beside a set", []string{"--suggest", "-r", "quick"}, "quick", true},
		{"named inside the list", []string{"-r", "suggest,sec"}, "sec", true},
		{"named alone in the list", []string{"-r", "suggest"}, "", false},
		{"repeats survive, they are weight", []string{"-s", "-r", "sec,sec,doc"}, "sec,sec,doc", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := parseFlags(c.argv)
			if err != nil {
				t.Fatalf("%v: %v", c.argv, err)
			}
			if !o.suggest {
				t.Fatal("the suggest step was not requested")
			}
			if o.reviews != c.reviews {
				t.Fatalf("reviews = %q, want %q", o.reviews, c.reviews)
			}
			if o.reviewsSet != c.set {
				t.Fatalf("reviewsSet = %v, want %v", o.reviewsSet, c.set)
			}
		})
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
		{"continue-sessions in place", []string{"--continue-sessions"}, ""},
		{"update-repo fork", []string{"--update-repo", "other/gauntlet"}, ""},
		{"suggest with reviews", []string{"-s", "-r", "quick"}, ""},
		{"suggest mixed in", []string{"-r", "suggest,sec"}, ""},
		{"exclude suggest", []string{"-x", "suggest"}, "not a review name"},
		{"once with max-loops", []string{"-1", "-n", "3"}, "conflicts"},
		{"negative loops", []string{"-n", "-2"}, "must be >= 0"},
		{"negative seed", []string{"--seed", "-1"}, "must be a nonnegative integer"},
		{"garbage seed", []string{"--seed", "soon"}, "must be a nonnegative integer"},
		{"hex seed", []string{"--seed", "0x10"}, ""},
		{"zero jobs", []string{"-j", "0"}, "must be >= 1"},
		{"stack owns commits", []string{"--stacked-prs", "--commit"}, "owns its commits"},
		{"stack owns pushes", []string{"--stacked-prs", "--push"}, "owns its commits"},
		{"stack never merges", []string{"--stacked-prs", "--merge-into", "main"}, "conflicts"},
		{"stack is one pass", []string{"--stacked-prs", "--max-loops", "2"}, "one ordered review pass"},
		{"base needs stack", []string{"--pr-base", "main"}, "requires --stacked-prs"},
		{"remote needs stack", []string{"--push-remote", "fork"}, "requires --stacked-prs"},
		{"empty dir", []string{"--dir", ""}, "--dir is empty"},
		{"whitespace dir", []string{"--dir", "  "}, "--dir is empty"},
		{"empty dirs", []string{"--dirs", ""}, "--dirs is empty"},
		{"empty dirs commas", []string{"--dirs", ",,,"}, "--dirs is empty"},
		{"empty push-remote", []string{"--stacked-prs", "--push-remote", ""}, "--push-remote is empty"},
		{"empty update-repo", []string{"--update-repo", ""}, "--update-repo is empty"},
		{"update-repo URL", []string{"--update-repo", "https://github.com/maci0/gauntlet"}, "want owner/repo"},
		{"update-repo extra path", []string{"--update-repo", "maci0/gauntlet/extra"}, "want owner/repo"},
		{"update-repo no slash", []string{"--update-repo", "maci0"}, "want owner/repo"},
		{"continue-sessions with jobs", []string{"--continue-sessions", "-j", "2"}, "cannot be used with --jobs"},
		{"continue-sessions with stack", []string{"--continue-sessions", "--stacked-prs"}, "cannot be used with --jobs"},
		{"negative limit", []string{"runs", "--limit", "-3"}, "--limit must be >= 1"},
		{"zero limit", []string{"runs", "--limit", "0"}, "--limit must be >= 1"},
		{"dirs with dir", []string{"--dirs", "a", "-C", "b"}, "conflicts"},
		{"two modes", []string{"--list", "--dry-run"}, "mutually exclusive"},
		{"unknown agent", []string{"-a", "nope"}, "unknown tool"},
		{"bad duration", []string{"-t", "5x"}, "invalid duration"},
		{"zero timeout", []string{"-t", "0"}, "must be positive"},
		{"usage limit without a probe", []string{"--usage-limit", "80"}, "used together"},
		{"usage probe without a limit", []string{"--usage-cmd", "true"}, "used together"},
		// The probe is split on whitespace, so a blank one is no probe at
		// all: it used to pass the pairing check above and leave the limit
		// set and inert for the whole run.
		{"blank usage probe", []string{"--usage-cmd", "   ", "--usage-limit", "80"},
			"--usage-cmd is blank"},
		{"usage probe and limit together", []string{"--usage-cmd", "sh -c 'echo 1'", "--usage-limit", "80"}, ""},
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
		{"stray limit under version", []string{"version", "--limit", "5"},
			"does not apply to 'gauntlet version'"},
		{"stray jobs under version", []string{"version", "--jobs", "4"},
			"does not apply to 'gauntlet version'"},
		{"global log follows version", []string{"version", "--log", "gauntlet.log"}, ""},
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

func TestStackedPRFlagsForceOneSequentialPass(t *testing.T) {
	o, err := parseFlags([]string{"--stacked-prs", "--pr-base", "main", "--push-remote", "fork", "-j", "8"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.stackedPRs || o.prBase != "main" || o.pushRemote != "fork" {
		t.Fatalf("stack options were not retained: %+v", o)
	}
	if o.jobs != 1 || o.maxLoops != 1 {
		t.Fatalf("stack mode must be one sequential pass: jobs=%d loops=%d", o.jobs, o.maxLoops)
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

// A repeated --agent-cmd with a different definition must refuse the way
// --bin refuses a repeated tool, rather than let the later one silently win.
// Both cases here use a built-in name, which Register refuses anyway: the
// error that comes back tells which check fired, and the registry is never
// touched.
func TestAgentCmdDuplicateDefinitions(t *testing.T) {
	_, err := parseFlags([]string{
		"--agent-cmd", "claude=x {prompt}",
		"--agent-cmd", "claude=y {prompt}",
	})
	if err == nil || !strings.Contains(err.Error(), "given twice for claude") {
		t.Fatalf("conflicting --agent-cmd definitions should refuse, got %v", err)
	}
	// An exact repeat is idempotent, not a conflict: it falls through to
	// Register, whose built-in refusal is the error instead.
	_, err = parseFlags([]string{
		"--agent-cmd", "claude=x {prompt}",
		"--agent-cmd", "claude=x {prompt}",
	})
	if err == nil || strings.Contains(err.Error(), "given twice") {
		t.Fatalf("an identical --agent-cmd repeat is not a conflict, got %v", err)
	}
}

func TestShorthandValuesMayBeGluedOn(t *testing.T) {
	o, err := parseFlags([]string{"-j3", "-r", "sec", "-t45m", "-C.", "-n2"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if o.jobs != 3 {
		t.Fatalf("jobs = %d, want 3", o.jobs)
	}
	if o.timeout != 45*time.Minute {
		t.Fatalf("timeout = %v, want 45m", o.timeout)
	}
	if o.dir != "." {
		t.Fatalf("dir = %q, want %q", o.dir, ".")
	}
	if o.retries != 2 {
		t.Fatalf("retries = %d, want 2", o.retries)
	}
}

func TestGluedExpansionLeavesTheRestAlone(t *testing.T) {
	fs, _ := buildFlagSet(&options{})
	cases := []struct {
		argv, want []string
	}{
		{[]string{"-j3"}, []string{"-j", "3"}},
		{[]string{"-j=3"}, []string{"-j=3"}},               // the flag package's own form
		{[]string{"-j", "3"}, []string{"-j", "3"}},         // already separate
		{[]string{"-sy"}, []string{"-sy"}},                 // booleans do not cluster
		{[]string{"-1"}, []string{"-1"}},                   // a boolean shorthand
		{[]string{"-zz"}, []string{"-zz"}},                 // unknown: let the parser say so
		{[]string{"--jobs3"}, []string{"--jobs3"}},         // long form, untouched
		{[]string{"--", "-j3"}, []string{"--", "-j3"}},     // after the terminator
		{[]string{"show", "-j3"}, []string{"show", "-j3"}}, // positional ends the flags
	}
	for _, c := range cases {
		got := expandAttachedValues(fs, c.argv)
		if !slices.Equal(got, c.want) {
			t.Errorf("expandAttachedValues(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}

// FuzzExpandAttachedValues: argv is untrusted, and the expansion runs before
// the flag package sees it. It must never panic, never lose or invent an
// argument's bytes, and never touch anything after a positional.
func FuzzExpandAttachedValues(f *testing.F) {
	for _, seed := range []string{"-j3", "-j=3", "-r sec", "-- -j3", "-\x00\x00", "show -j3", "-"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		argv := strings.Fields(line)
		fs, _ := buildFlagSet(&options{})
		got := expandAttachedValues(fs, argv)
		if len(got) < len(argv) {
			t.Fatalf("expandAttachedValues(%q) dropped arguments: %q", argv, got)
		}
		if strings.Join(got, "") != strings.Join(argv, "") {
			t.Fatalf("expandAttachedValues(%q) = %q, which is not a resplit of it", argv, got)
		}
	})
}
