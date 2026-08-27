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
	"github.com/maci0/gauntlet/internal/prompt"
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

// The -r suggest flow must turn one agent's RELEVANT lines into exactly that
// schedule, and report which agent chose it.
func TestSelectReviewsRunsTheSuggestStep(t *testing.T) {
	d, opts := suggestFixture(t, `echo "thinking"; echo "RELEVANT: sec-review: has auth code"`)
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts, []agent.Spec{{Tool: "claude"}}, &out, palette{})
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
		[]agent.Spec{{Tool: "claude"}}, &out, palette{}); err != nil {
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

// An exit code of 0 with unusable output is as much a failure as a nonzero
// exit: running everything by accident is the alternative.
func TestSelectReviewsRefusesAnAgentWithNoSuggestions(t *testing.T) {
	d, opts := suggestFixture(t, `echo "I would run everything"`)
	var out bytes.Buffer

	err := planReviews(context.Background(), []*dirRun{d}, opts,
		[]agent.Spec{{Tool: "claude"}}, &out, palette{})
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
		[]agent.Spec{{Tool: "claude"}}, &out, palette{})
	if err != nil {
		t.Fatal(err)
	}
	if got := d.reviews; len(got) != 1 || got[0] != "doc-review" {
		t.Fatalf("excluded suggestion survived: %v", got)
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
		[]agent.Spec{{Tool: "claude"}}, &out, palette{})
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
