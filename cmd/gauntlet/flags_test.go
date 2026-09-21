// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		"rebuilds a missing index",
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

func TestDurationFlag(t *testing.T) {
	cases := map[string]time.Duration{
		"90":          90 * time.Second,
		"90s":         90 * time.Second,
		"30m":         30 * time.Minute,
		"1h":          time.Hour,
		"2d":          48 * time.Hour,
		"1H":          time.Hour,
		"9223372036s": 9223372036 * time.Second,
		"2562047h":    2562047 * time.Hour,
		"106751d":     106751 * 24 * time.Hour,
	}
	for _, allowZero := range []bool{false, true} {
		for in, want := range cases {
			var got time.Duration
			f := durationFlag{d: &got, allowZero: allowZero}
			if err := f.Set(in); err != nil {
				t.Errorf("Set(%q), allowZero=%v: %v", in, allowZero, err)
				continue
			}
			if got != want {
				t.Errorf("Set(%q), allowZero=%v: got %v, want %v", in, allowZero, got, want)
			}
		}
		for _, in := range []string{"0", "0s", "0m", "0h", "0d"} {
			got := time.Minute
			f := durationFlag{d: &got, allowZero: allowZero}
			err := f.Set(in)
			if allowZero {
				if err != nil || got != 0 {
					t.Errorf("Set(%q), allowZero=true: got %v, %v", in, got, err)
				}
			} else if err == nil || got != time.Minute {
				t.Errorf("Set(%q), allowZero=false: got %v, %v", in, got, err)
			}
		}
		for _, bad := range []string{"", "-5m", "abc", "10y",
			"9999999999999999999999d", "5124096h", "36893488148s", "177922d"} {
			got := time.Minute
			f := durationFlag{d: &got, allowZero: allowZero}
			if err := f.Set(bad); err == nil {
				t.Errorf("Set(%q), allowZero=%v should fail", bad, allowZero)
			}
			if got != time.Minute {
				t.Errorf("Set(%q), allowZero=%v changed the value to %v", bad, allowZero, got)
			}
		}
	}
}

func FuzzDurationFlag(f *testing.F) {
	for _, seed := range []string{"90s", "30m", "1h", "2d", "", "0", "-5m", "d", "9999999999999999999d", "1.5h", "1h30m"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, allowZero := range []bool{false, true} {
			d := time.Minute
			flag := durationFlag{d: &d, allowZero: allowZero}
			if err := flag.Set(in); err != nil {
				if d != time.Minute {
					t.Fatalf("Set(%q) changed the value on error to %v", in, d)
				}
				continue
			}
			if d < 0 || (!allowZero && d == 0) {
				t.Fatalf("Set(%q), allowZero=%v accepted %v", in, allowZero, d)
			}
			_ = flag.String()
		}
	})
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
		name  string
		argv  []string
		want  string // substring of the expected error, empty means success
		check func(t *testing.T, o *options)
	}{
		{"once implies one loop", []string{"--once"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.maxLoops != 1 {
				t.Fatalf("maxLoops = %d, want 1", o.maxLoops)
			}
		}},
		{"push implies commit", []string{"--push"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.commit {
				t.Fatal("push should set commit = true")
			}
			if !o.push {
				t.Fatal("push should be true")
			}
		}},
		{"continue-sessions in place", []string{"--continue-sessions"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.continueSessions {
				t.Fatal("continueSessions should be true")
			}
		}},
		{"update-repo fork", []string{"--update-repo", "other/gauntlet"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.updateRepo != "other/gauntlet" {
				t.Fatalf("updateRepo = %q, want %q", o.updateRepo, "other/gauntlet")
			}
		}},
		{"suggest with reviews", []string{"-s", "-r", "quick"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.suggest {
				t.Fatal("suggest should be true")
			}
			if o.reviews != "quick" {
				t.Fatalf("reviews = %q, want %q", o.reviews, "quick")
			}
		}},
		{"suggest mixed in", []string{"-r", "suggest,sec"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.suggest {
				t.Fatal("suggest mixed into reviews should set suggest = true")
			}
		}},
		{"exclude suggest", []string{"-x", "suggest"}, "not a review name", nil},
		{"once with max-loops", []string{"-1", "-n", "3"}, "conflicts", nil},
		{"negative loops", []string{"-n", "-2"}, "must be >= 0", nil},
		{"negative max-reviews", []string{"--max-reviews", "-1"}, "--max-reviews must be >= 0", nil},
		{"zero max-reviews", []string{"--max-reviews", "0"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.maxReviews != 0 {
				t.Fatalf("maxReviews = %d, want 0", o.maxReviews)
			}
		}},
		{"positive max-reviews", []string{"--max-reviews", "3"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.maxReviews != 3 {
				t.Fatalf("maxReviews = %d, want 3", o.maxReviews)
			}
		}},
		{"negative seed", []string{"--seed", "-1"}, "must be a nonnegative integer", nil},
		{"garbage seed", []string{"--seed", "soon"}, "must be a nonnegative integer", nil},
		{"hex seed", []string{"--seed", "0x10"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.seed != 16 {
				t.Fatalf("seed = %d, want 16", o.seed)
			}
		}},
		{"zero jobs", []string{"-j", "0"}, "must be >= 1", nil},
		{"stack owns commits", []string{"--stacked-prs", "--commit"}, "owns its commits", nil},
		{"stack owns pushes", []string{"--stacked-prs", "--push"}, "owns its commits", nil},
		{"stack never merges", []string{"--stacked-prs", "--merge-into", "main"}, "conflicts", nil},
		{"stack allows several passes", []string{"--stacked-prs", "--max-loops", "2"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.stackedPRs {
				t.Fatal("stackedPRs should be true")
			}
			if o.maxLoops != 2 {
				t.Fatalf("maxLoops = %d, want 2", o.maxLoops)
			}
		}},
		{"stack allows unlimited passes", []string{"--stacked-prs", "-n", "0"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.stackedPRs {
				t.Fatal("stackedPRs should be true")
			}
			if o.maxLoops != 0 {
				t.Fatalf("maxLoops = %d, want 0", o.maxLoops)
			}
		}},
		{"base needs stack", []string{"--pr-base", "main"}, "requires --stacked-prs", nil},
		{"remote needs stack", []string{"--push-remote", "fork"}, "requires --stacked-prs", nil},
		{"empty dir", []string{"--dir", ""}, "--dir is empty", nil},
		{"whitespace dir", []string{"--dir", "  "}, "--dir is empty", nil},
		{"empty prompt-dir", []string{"--prompt-dir", ""}, "--prompt-dir is empty", nil},
		{"whitespace prompt-dir", []string{"--prompt-dir", "  "}, "--prompt-dir is empty", nil},
		{"empty log", []string{"--log", ""}, "--log is empty", nil},
		{"empty dirs", []string{"--dirs", ""}, "--dirs is empty", nil},
		{"empty dirs commas", []string{"--dirs", ",,,"}, "--dirs is empty", nil},
		{"empty push-remote", []string{"--stacked-prs", "--push-remote", ""}, "--push-remote is empty", nil},
		{"empty show-prompt", []string{"--show-prompt", ""}, "--show-prompt is empty", nil},
		{"whitespace show-prompt", []string{"--show-prompt", "   "}, "--show-prompt is empty", nil},
		{"empty merge-into", []string{"--merge-into", ""}, "--merge-into is empty", nil},
		{"whitespace merge-into", []string{"--merge-into", "   "}, "--merge-into is empty", nil},
		{"empty pr-base", []string{"--stacked-prs", "--pr-base", ""}, "--pr-base is empty", nil},
		{"whitespace pr-base", []string{"--stacked-prs", "--pr-base", "   "}, "--pr-base is empty", nil},
		{"empty suggest-agent", []string{"--suggest-agent", ""}, "--suggest-agent is empty", nil},
		{"whitespace suggest-agent", []string{"--suggest-agent", "   "}, "--suggest-agent is empty", nil},
		{"empty usage-cmd", []string{"--usage-cmd", ""}, "--usage-cmd is blank", nil},
		{"empty exclude", []string{"--exclude", ""}, "--exclude is empty", nil},
		{"empty update-repo", []string{"--update-repo", ""}, "--update-repo is empty", nil},
		{"update-repo URL", []string{"--update-repo", "https://github.com/maci0/gauntlet"}, "want owner/repo", nil},
		{"update-repo extra path", []string{"--update-repo", "maci0/gauntlet/extra"}, "want owner/repo", nil},
		{"update-repo no slash", []string{"--update-repo", "maci0"}, "want owner/repo", nil},
		{"continue-sessions with jobs", []string{"--continue-sessions", "-j", "2"}, "cannot be used with --jobs", nil},
		{"continue-sessions with stack", []string{"--continue-sessions", "--stacked-prs"}, "cannot be used with --jobs", nil},
		{"negative limit", []string{"runs", "--limit", "-3"}, "--limit must be >= 1", nil},
		{"zero limit", []string{"runs", "--limit", "0"}, "--limit must be >= 1", nil},
		{"dirs with dir", []string{"--dirs", "a", "-C", "b"}, "conflicts", nil},
		{"two modes", []string{"--list", "--dry-run"}, "mutually exclusive", nil},
		{"unknown agent", []string{"-a", "nope"}, "unknown tool", nil},
		{"bad duration", []string{"-t", "5x"}, "invalid duration", nil},
		{"zero timeout", []string{"-t", "0"}, "must be positive", nil},
		{"usage limit without a probe", []string{"--usage-limit", "80"}, "used together", nil},
		{"usage probe without a limit", []string{"--usage-cmd", "true"}, "used together", nil},
		// The probe is split on whitespace, so a blank one is no probe at
		// all: it used to pass the pairing check above and leave the limit
		// set and inert for the whole run.
		{"blank usage probe", []string{"--usage-cmd", "   ", "--usage-limit", "80"},
			"--usage-cmd is blank", nil},
		{"usage probe and limit together", []string{"--usage-cmd", "sh -c 'echo 1'", "--usage-limit", "80"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.usageLimit != 80 {
				t.Fatalf("usageLimit = %f, want 80", o.usageLimit)
			}
			if len(o.usageArgv) == 0 {
				t.Fatal("usageArgv should not be empty")
			}
		}},
		// flag.Float64Var uses ParseFloat, which accepts these. A range
		// check cannot reject NaN (every comparison against it is false),
		// and the runner would then read pct < NaN as "at or past the limit".
		{"usage limit NaN", []string{"--usage-cmd", "true", "--usage-limit", "NaN"}, "percentage", nil},
		{"usage limit nan", []string{"--usage-cmd", "true", "--usage-limit", "nan"}, "percentage", nil},
		{"usage limit Inf", []string{"--usage-cmd", "true", "--usage-limit", "Inf"}, "percentage", nil},
		{"usage limit over 100", []string{"--usage-cmd", "true", "--usage-limit", "101"}, "percentage", nil},
		{"trailing argument", []string{"extra"}, "unknown command", nil},
		{"check outside update", []string{"--check"}, "--check requires 'gauntlet update'", nil},
		{"limit outside runs", []string{"--limit", "5"}, "--limit requires 'gauntlet runs'", nil},
		{"limit under show", []string{"show", "20260825T000000Z-abcd", "--limit", "5"},
			"--limit requires 'gauntlet runs'", nil},
		{"check under update", []string{"update", "--check"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.command != "update" {
				t.Fatalf("command = %q, want %q", o.command, "update")
			}
			if !o.checkOnly {
				t.Fatal("checkOnly should be true")
			}
		}},
		{"limit under runs", []string{"runs", "--limit", "5"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.command != "runs" {
				t.Fatalf("command = %q, want %q", o.command, "runs")
			}
			if o.runsLimit != 5 {
				t.Fatalf("runsLimit = %d, want 5", o.runsLimit)
			}
		}},
		{"version wins over scoping", []string{"-V", "--limit", "5"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.command != "version" {
				t.Fatalf("command = %q, want %q", o.command, "version")
			}
		}},
		{"stray jobs under runs", []string{"runs", "--jobs", "4"}, "does not apply to 'gauntlet runs'", nil},
		{"stray max-reviews under runs", []string{"runs", "--max-reviews", "3"}, "does not apply to 'gauntlet runs'", nil},
		{"stray short flag keeps its spelling", []string{"runs", "-j", "4"}, "-j does not apply", nil},
		{"stray yolo under doctor", []string{"doctor", "--yolo"}, "does not apply to 'gauntlet doctor'", nil},
		{"stray tui under show", []string{"show", "20260825T000000Z-abcd", "--tui"},
			"does not apply to 'gauntlet show'", nil},
		{"stray limit under version", []string{"version", "--limit", "5"},
			"does not apply to 'gauntlet version'", nil},
		{"stray jobs under version", []string{"version", "--jobs", "4"},
			"does not apply to 'gauntlet version'", nil},
		{"global log follows version", []string{"version", "--log", "gauntlet.log"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.logFile != "gauntlet.log" {
				t.Fatalf("logFile = %q, want %q", o.logFile, "gauntlet.log")
			}
		}},
		{"doctor reads its bin", []string{"doctor", "--bin", "claude=/bin/sh"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.command != "doctor" {
				t.Fatalf("command = %q, want %q", o.command, "doctor")
			}
			if o.bin["claude"] != "/bin/sh" {
				t.Fatalf("bin[claude] = %q, want %q", o.bin["claude"], "/bin/sh")
			}
		}},
		{"pick reads its dir", []string{"pick", "-C", "."}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.command != "pick" {
				t.Fatalf("command = %q, want %q", o.command, "pick")
			}
			if o.dir != "." {
				t.Fatalf("dir = %q, want %q", o.dir, ".")
			}
		}},
		{"global log follows runs", []string{"runs", "--log", "gauntlet.log"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if o.logFile != "gauntlet.log" {
				t.Fatalf("logFile = %q, want %q", o.logFile, "gauntlet.log")
			}
		}},
		{"no-color follows everything", []string{"runs", "--no-color"}, "", func(t *testing.T, o *options) {
			t.Helper()
			if !o.noColor {
				t.Fatal("noColor should be true")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := parseFlags(c.argv)
			if c.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if c.check != nil {
					c.check(t, o)
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

func TestParseFlagsUnsetPathVar(t *testing.T) {
	t.Setenv("GAUNTLET_TEST_MISSING", "x")
	os.Unsetenv("GAUNTLET_TEST_MISSING")
	for _, argv := range [][]string{
		{"--prompt-dir", "$GAUNTLET_TEST_MISSING"},
		{"--log", "$GAUNTLET_TEST_MISSING/out.log"},
	} {
		_, err := parseFlags(argv)
		if err == nil || !strings.Contains(err.Error(), "GAUNTLET_TEST_MISSING") {
			t.Errorf("%v: want unset-var error, got %v", argv, err)
		}
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
		t.Fatalf("stack mode defaults to one sequential pass: jobs=%d loops=%d", o.jobs, o.maxLoops)
	}
}

func TestStackedPRFlagsHonorMaxLoops(t *testing.T) {
	o, err := parseFlags([]string{"--stacked-prs", "-n", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if o.jobs != 1 || o.maxLoops != 3 {
		t.Fatalf("explicit -n 3: jobs=%d loops=%d", o.jobs, o.maxLoops)
	}
	o, err = parseFlags([]string{"--stacked-prs", "--max-loops", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if o.jobs != 1 || o.maxLoops != 0 {
		t.Fatalf("explicit -n 0 must stay unlimited: jobs=%d loops=%d", o.jobs, o.maxLoops)
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

func TestParseFlagsPaths(t *testing.T) {
	// A single file, a whole directory, and a glob are all legal entries, and
	// the flag is repeatable and comma-separated like the other lists.
	o, err := parseFlags([]string{"--paths", "scripts/bolide.py,internal/runner", "--paths", "docs/*.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(o.paths, "|") != "scripts/bolide.py|internal/runner|docs/*.md" {
		t.Fatalf("paths: %v", o.paths)
	}

	// Omitted means the whole tree.
	o, err = parseFlags(nil)
	if err != nil || len(o.paths) != 0 {
		t.Fatalf("default paths: %v %v", o.paths, err)
	}

	// An explicit empty --paths would silently mean the whole tree too, the
	// opposite of the narrowing it asked for: refused, like --reviews '' is
	// kept explicit rather than expanded.
	if _, err := parseFlags([]string{"--paths", ""}); err == nil {
		t.Fatal("explicit empty --paths accepted")
	}
	if _, err := parseFlags([]string{"--paths", " , "}); err == nil {
		t.Fatal("whitespace-only --paths accepted")
	}

	// Subcommands do not read it, so it must be refused, not swallowed.
	if _, err := parseFlags([]string{"runs", "--paths", "x"}); err == nil {
		t.Fatal("gauntlet runs --paths was not refused")
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
	defer r.Close()
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
	for _, argv := range [][]string{
		{"-h"}, {"--help"}, {"doctor", "--help"},
		{"show", "--help"}, {"show", "--no-color", "--help"},
		{"--no-color", "help"},
	} {
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
	for _, cmd := range commandNames {
		if !strings.Contains(err.Error(), cmd) {
			t.Errorf("hint %q does not mention accepted command %q", err, cmd)
		}
	}
}

func TestCommandNamesMatchTheHelpScreen(t *testing.T) {
	for _, name := range commandNames {
		found := false
		for _, c := range helpCommands {
			if strings.Contains(c.Cmd, name) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q is missing from the help screen", name)
		}
	}
}

func TestUnknownCommandSuggestsClosest(t *testing.T) {
	_, err := parseFlags([]string{"run"})
	if err == nil || !strings.Contains(err.Error(), `did you mean "runs"`) {
		t.Fatalf("unknown command should hint the closest name, got %v", err)
	}
}

func TestUnknownFlagSuggestsClosest(t *testing.T) {
	_, err := parseFlags([]string{"--tuii"})
	if err == nil || !strings.Contains(err.Error(), "did you mean --tui") {
		t.Fatalf("unknown flag should hint the closest name, got %v", err)
	}
	_, err = parseFlags([]string{"-Z"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("a one-letter miss is too ambiguous to hint: %v", err)
	}
	_, err = parseFlags([]string{"-é"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("a one-letter non-ASCII miss is too ambiguous to hint: %v", err)
	}
}

func TestGlobalFlagsMayPrecedeSubcommand(t *testing.T) {
	o, err := parseFlags([]string{"--no-color", "doctor"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "doctor" || !o.noColor {
		t.Fatalf("gauntlet --no-color doctor parsed as %+v", o)
	}
	o, err = parseFlags([]string{"--log", "gauntlet.log", "runs", "--limit", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "runs" || o.logFile == "" || o.runsLimit != 5 {
		t.Fatalf("gauntlet --log FILE runs --limit 5 parsed as %+v", o)
	}
}

func TestShowFlagsMayPrecedeRunID(t *testing.T) {
	o, err := parseFlags([]string{"show", "--no-color", "20260825T000000Z-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "show" || o.showRun != "20260825T000000Z-abcd" || !o.noColor {
		t.Fatalf("show --no-color RUN parsed as %+v", o)
	}
	o, err = parseFlags([]string{"show", "20260825T000000Z-abcd", "--no-color"})
	if err != nil {
		t.Fatal(err)
	}
	if o.command != "show" || o.showRun != "20260825T000000Z-abcd" || !o.noColor {
		t.Fatalf("show RUN --no-color parsed as %+v", o)
	}
}

func TestNeedsAgentsSkipsInformationalModes(t *testing.T) {
	list, err := parseFlags([]string{"--list"})
	if err != nil {
		t.Fatal(err)
	}
	if list.needsAgents() {
		t.Fatal("--list must not require an agent CLI")
	}
	prompt, err := parseFlags([]string{"--show-prompt", "sec"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.needsAgents() {
		t.Fatal("--show-prompt must not require an agent CLI")
	}
	suggest, err := parseFlags([]string{"--list", "--suggest"})
	if err != nil {
		t.Fatal(err)
	}
	if !suggest.needsAgents() {
		t.Fatal("--list --suggest still launches an agent")
	}
	local, err := parseFlags([]string{"--list", "--suggest", "--suggest-agent", "gauntlet"})
	if err != nil {
		t.Fatal(err)
	}
	if local.needsAgents() {
		t.Fatal("--list --suggest --suggest-agent gauntlet reads the tree, not a CLI")
	}
	run, err := parseFlags([]string{"--once"})
	if err != nil {
		t.Fatal(err)
	}
	if !run.needsAgents() {
		t.Fatal("a real run requires an agent CLI")
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

func TestParseFlagsRejectsUnresolvableGauntletHome(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", "$GAUNTLET_MISSING_DIR/state")
	_, err := parseFlags([]string{"--list"})
	if err == nil || !strings.Contains(err.Error(), "GAUNTLET_HOME") {
		t.Fatalf("want GAUNTLET_HOME error, got %v", err)
	}
}

func TestParseFlagsRejectsFileGauntletHome(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_HOME", file)
	_, err := parseFlags([]string{"--list"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not a directory error, got %v", err)
	}
}

func TestParseFlagsRejectsFilePromptDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFlags([]string{"--prompt-dir", file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want not a directory error, got %v", err)
	}
}

func TestParseFlagsRejectsDirectoryLogFile(t *testing.T) {
	dir := t.TempDir()
	_, err := parseFlags([]string{"--log", dir})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("want is a directory error, got %v", err)
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
