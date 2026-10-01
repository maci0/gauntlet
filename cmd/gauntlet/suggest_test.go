// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/evidence"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
)

// suggestFixture builds a two-review catalog and a fake triage agent whose
// output the tests control.
func suggestFixture(t *testing.T, agentBody string) (*dirRun, *options) {
	t.Helper()
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sec-review", "doc-review"} {
		body := "Your goal is to test " + n + ".\n"
		if err := os.WriteFile(filepath.Join(promptDir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := prompt.Discover(context.Background(), promptDir, promptDir)
	if err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\n" + agentBody + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	d := &dirRun{dir: dir, set: set}
	opts := &options{
		suggest:        true,
		bin:            map[string]string{"claude": bin},
		suggestTimeout: 30 * time.Second,
		yes:            true, // stdin is not interactive under go test anyway; be explicit
	}
	return d, opts
}

// The suggest step names the directory it read and says which agent chose
// what, and both go straight to a terminal from planReviews rather than
// through a reporter that would sanitize. The directory is the reviewed
// repository's to name, so its base is cleaned before it reaches the screen.
func TestSuggestSanitizesTheDirectoryTag(t *testing.T) {
	// Two runs are what turn a directory into a tag: with one, planReviews
	// prints no name at all and the hostile component never reaches a line.
	first, opts := suggestFixture(t, `echo "RELEVANT: doc-review: docs drifted"`)
	second, _ := suggestFixture(t, `echo "RELEVANT: doc-review: docs drifted"`)
	// The last component is the reviewed repository's to name, so move one
	// run's directory under a name carrying an escape sequence and a BEL.
	second.dir = filepath.Join(filepath.Dir(second.dir), "evil\x1b[31mred\x07")
	if err := os.MkdirAll(second.dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := planReviews(context.Background(), []*dirRun{first, second}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("the suggest header passed an escape or a BEL to the terminal: %q", got)
	}
	if !strings.Contains(got, "suggests") {
		t.Fatalf("the suggest header did not print:\n%s", got)
	}
}

// The -r suggest flow must turn one agent's RELEVANT lines into exactly that
// schedule, and report which agent chose it.
func TestSelectReviewsRunsTheSuggestStep(t *testing.T) {
	d, opts := suggestFixture(t, `echo "thinking"; echo "RELEVANT: sec-review: has auth code"`)
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts, []agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.reviews; len(got) != 1 || got[0] != "sec-review" {
		t.Fatalf("schedule %v, want the one suggested review", got)
	}
	if !strings.Contains(out.String(), "suggests 1 of 2") ||
		!strings.Contains(out.String(), "has auth code") {
		t.Fatalf("the choice and its reason were not reported:\n%s", out.String())
	}
}

// A review named beside --suggest is scheduled as well as what the agent
// picks, and one that appears on both lists is scheduled twice: repeats are
// weight, which is how a person says "and lean on this one".
func TestSuggestAddsWhatWasNamed(t *testing.T) {
	d, opts := suggestFixture(t, `echo "RELEVANT: sec-review: has auth code"`)
	opts.reviews, opts.reviewsSet = "sec,doc", true
	var out bytes.Buffer

	if err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now); err != nil {
		t.Fatal(err)
	}
	got := d.reviews
	if len(got) != 3 {
		t.Fatalf("schedule %v, want the suggestion plus both named reviews", got)
	}
	sec := 0
	for _, n := range got {
		if n == "sec-review" {
			sec++
		}
	}
	if sec != 2 {
		t.Fatalf("sec-review appears %d times in %v, want twice: suggested and named", sec, got)
	}
	if !strings.Contains(out.String(), "named on the command line") {
		t.Fatalf("the report hides what was named:\n%s", out.String())
	}
}

func TestSuggestWeightsAddToManualRepeats(t *testing.T) {
	d, opts := suggestFixture(t, `echo "RELEVANT: sec-review: weight=3: critical auth"; echo "RELEVANT: doc-review: weight=0: skip"`)
	opts.reviews, opts.reviewsSet = "sec,doc", true
	var out bytes.Buffer
	if err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.reviews, ","); got != "sec-review,sec-review,sec-review,sec-review,doc-review" {
		t.Fatalf("schedule %s, want three suggested passes plus both manual entries", got)
	}
	if !strings.Contains(out.String(), "(x3) critical auth") || !strings.Contains(out.String(), "suggests 1 of 2") {
		t.Fatalf("the preview hides suggested weights:\n%s", out.String())
	}
}

// An exit code of 0 with unusable output is as much a failure as a nonzero
// exit: running everything by accident is the alternative.
func TestSelectReviewsRefusesAnAgentWithNoSuggestions(t *testing.T) {
	d, opts := suggestFixture(t, `echo "I would run everything"`)
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if !errors.Is(err, errAgentFailed) {
		t.Fatalf("an agent that picks nothing must fail the triage step, got %v", err)
	}
}

// Exclusions apply to suggested reviews too: an agent cannot talk its way
// around --exclude.
func TestSelectReviewsFiltersSuggestedReviewsThroughExclude(t *testing.T) {
	d, opts := suggestFixture(t, `echo "RELEVANT: sec-review: x"; echo "RELEVANT: doc-review: y"`)
	opts.exclude = "sec"
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.reviews; len(got) != 1 || got[0] != "doc-review" {
		t.Fatalf("excluded suggestion survived: %v", got)
	}
}

// The confirmation is the consent gate, so it has to describe the run that
// will actually happen. --exclude applies to what was named on the command
// line as well as to what the agent picked, and the preview used to be built
// with no exclusions at all: it listed a review the schedule then dropped, and
// counted it in the total the prompt asks about.
func TestSuggestPreviewHidesWhatExcludeRemoves(t *testing.T) {
	d, opts := suggestFixture(t, `echo "RELEVANT: doc-review: y"`)
	opts.reviews, opts.reviewsSet = "sec,doc", true
	opts.exclude = "sec"
	var out bytes.Buffer

	if err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now); err != nil {
		t.Fatal(err)
	}
	// The schedule is the control: it has always applied the exclusion, so a
	// test that only read the preview could not tell which half was wrong.
	if got := d.reviews; len(got) != 2 {
		t.Fatalf("schedule %v, want the suggestion plus the one surviving named review", got)
	}
	for _, n := range d.reviews {
		if n == "sec-review" {
			t.Fatalf("excluded review reached the schedule: %v", d.reviews)
		}
	}
	report := out.String()
	if !strings.Contains(report, "doc-review") {
		t.Fatalf("the preview lost what was named:\n%s", report)
	}
	if strings.Contains(report, "sec-review") {
		t.Fatalf("the preview offers an excluded review for confirmation:\n%s", report)
	}
}

// Every directory gets its own triage: their prompt sets differ, so one
// directory's answer is not another's. They run together, because several
// trees would otherwise be one suggest timeout after another.
func TestSuggestRunsForEveryDirectory(t *testing.T) {
	// The fake agent answers with whatever the directory it runs in is named
	// after: proof that each directory was asked on its own. Both agents also
	// register a slot while they work, and report an overlap the moment two
	// slots exist at once, which is proof of concurrency that no wall clock
	// under load can fake or flake.
	bar := t.TempDir()
	body := `
slots="` + bar + `/slots"
mkdir -p "$slots"
mkdir "$slots/$(basename "$PWD")" 2>/dev/null
i=0
while [ "$i" -lt 150 ]; do
	if [ "$(ls "$slots" | wc -l)" -ge 2 ]; then
		echo overlapping > "` + bar + `/concurrent"
		break
	fi
	i=$((i+1))
	sleep 0.02
done
case "$PWD" in
*a) echo "RELEVANT: sec-review: auth code";;
*) echo "RELEVANT: doc-review: docs drifted";;
esac`
	first, opts := suggestFixture(t, body)
	second, _ := suggestFixture(t, body)
	// Both directories share one agent binary and one options set.
	second.dir = renameDir(t, second.dir, "a")

	var out bytes.Buffer
	err := planReviews(context.Background(), []*dirRun{first, second}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got := first.reviews; len(got) != 1 || got[0] != "doc-review" {
		t.Fatalf("first directory scheduled %v, want its own answer", got)
	}
	if got := second.reviews; len(got) != 1 || got[0] != "sec-review" {
		t.Fatalf("second directory scheduled %v, want its own answer", got)
	}
	if _, err := os.Stat(filepath.Join(bar, "concurrent")); err != nil {
		t.Fatalf("the two suggest steps never overlapped, so they did not run together: %v", err)
	}
	if !strings.Contains(out.String(), "reviews in ") {
		t.Fatalf("the report does not say which directory each answer belongs to:\n%s", out.String())
	}
}

// renameDir moves a test directory so its basename is predictable.
func renameDir(t *testing.T, dir, name string) string {
	t.Helper()
	next := filepath.Join(filepath.Dir(dir), name)
	if err := os.Rename(dir, next); err != nil {
		t.Fatal(err)
	}
	return next
}

// --dry-run and --list never run reviews, so they must not prompt for
// confirmation before displaying their output.
func TestSuggestDryRunAndListDoNotAskForConfirmation(t *testing.T) {
	for _, mode := range []string{"dry-run", "list"} {
		t.Run(mode, func(t *testing.T) {
			d, opts := suggestFixture(t, `echo "RELEVANT: sec-review: has auth code"`)
			opts.yes = false
			if mode == "dry-run" {
				opts.dryRun = true
			} else {
				opts.list = true
			}
			var out bytes.Buffer
			err := planReviews(context.Background(), []*dirRun{d}, opts, []agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
			if err != nil {
				t.Fatalf("mode %s failed with: %v", mode, err)
			}
			if strings.Contains(out.String(), "Run these") || strings.Contains(out.String(), "[Y/n]") {
				t.Fatalf("mode %s prompted for confirmation:\n%s", mode, out.String())
			}
			if len(d.reviews) != 1 || d.reviews[0] != "sec-review" {
				t.Fatalf("mode %s scheduled %v, want sec-review", mode, d.reviews)
			}
		})
	}
}

// Built-in repeat weights use the same additive schedule and cap as model
// weights. The fake external agent must never run when the default is local.
func TestBuiltinSuggestWeightsAddToManualRepeats(t *testing.T) {
	d, opts := suggestFixture(t, "exit 99")
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(d.dir, "main.go"), []byte("package main\n// bcrypt database/sql unsafe.Pointer exec.Command\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseFlags([]string{"--suggest", "--reviews", "sec,sec", "--exclude", "doc"})
	if err != nil {
		t.Fatal(err)
	}
	opts.suggestAgent, opts.reviews, opts.reviewsSet, opts.exclude = parsed.suggestAgent, parsed.reviews, parsed.reviewsSet, parsed.exclude
	var out bytes.Buffer
	if err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(d.reviews, ","); got != "sec-review,sec-review,sec-review,sec-review,sec-review" {
		t.Fatalf("schedule %s, want three suggested passes plus two manual repeats", got)
	}
	if !strings.Contains(out.String(), "(x3)") || !strings.Contains(out.String(), "gauntlet suggests 1 of 1") {
		t.Fatalf("the preview hides built-in weights:\n%s", out.String())
	}
}

// The built-in file-signal suggester is not an agent: it launches nothing, so
// a tree it finds no signal in is not a failed agent, and saying "agent
// failed" names a launch that never happened. It is an empty schedule, which
// is a usage error like every other one, and the message has to say what to do
// about it.
func TestBuiltinSuggestWithNoSignalIsAUsageErrorNotAFailedAgent(t *testing.T) {
	d, opts := suggestFixture(t, "exit 99")
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	// A tree with no source at all: nothing for the signal reader to match.
	d.dir = t.TempDir()
	opts.suggestAgent = &agent.Spec{Tool: evidence.AgentName}
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if err == nil {
		t.Fatal("the file-signal suggester picking nothing must be an error, not an empty schedule")
	}
	if errors.Is(err, errAgentFailed) {
		t.Fatalf("a suggester that launched no agent must not be reported as a failed one: %v", err)
	}
	for _, want := range []string{"--reviews", "--suggest-agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not say what to do (%s): %v", want, err)
		}
	}
}

// The agent step keeps its own exit code: a real CLI that failed to produce
// suggestions is a run failure (exit 1), not a usage error.
func TestAgentSuggestFailureIsStillAFailedAgent(t *testing.T) {
	d, opts := suggestFixture(t, "exit 1")
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, report.Palette{}, time.Now)
	if !errors.Is(err, errAgentFailed) {
		t.Fatalf("an agent that fails to suggest must stay a failed agent, got %v", err)
	}
}
