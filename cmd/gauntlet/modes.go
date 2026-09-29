// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The non-looping modes: printing a prompt, planning a loop, and turning flags
// (or an agent's triage) into the list of reviews to schedule.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/runner"
)

// cmdShowPrompt prints the exact text an agent would receive.
func cmdShowPrompt(out io.Writer, set prompt.Set, opts *options) int {
	rev, ok := set.Get(opts.showPrompt)
	if !ok {
		if rev, ok = set.Get(opts.showPrompt + "-review"); !ok {
			candidates := append([]string{}, set.Names...)
			for _, n := range set.Names {
				if stem, ok := strings.CutSuffix(n, "-review"); ok {
					candidates = append(candidates, stem)
				}
			}
			hint := ""
			if c := fuzzy.Closest(opts.showPrompt, candidates); c != "" {
				hint = fmt.Sprintf(" (did you mean %q?)", c)
			}
			fmt.Fprintf(os.Stderr, "Unknown review: %s%s\nReviews: %s\n",
				opts.showPrompt, hint, strings.Join(set.Names, ", "))
			return exitUsage
		}
	}
	name := rev.Name
	body, err := rev.Body()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read prompt for %s: %v\n", name, err)
		return exitFail
	}
	// The point of --show-prompt is the exact text, and part of that text is
	// what this machine has: probe the same way a run would.
	//
	// What reaches the terminal is the display form, though, not the bytes:
	// the body may come from a hostile *-review.md, and an escape sequence
	// planted there (an OSC 52 clipboard overwrite, cursor addressing) would
	// fire against whoever inspected it here before deciding to run. The
	// agent still receives the exact bytes; Document strips only what could
	// drive or spoof the terminal, and keeps the line breaks that make the
	// prompt readable as the document it is.
	if _, err := fmt.Fprintln(out, normalize.Document(
		prompt.Compose(body, opts.timeout, name, opts.yolo, toolsFor(name), opts.paths))); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write the prompt: %v\n", err)
		return exitFail
	}
	return exitOK
}

// toolsFor reports which of a review's helper binaries this machine has,
// probing the PATH fresh: --show-prompt composes one prompt, so it does not
// share the runner's startup-wide probe.
func toolsFor(review string) prompt.Tools {
	names := agent.ToolsFor(review)
	have, missing := agent.SplitTools(names, agent.ResolveMany(agent.ToolBins(names)))
	return prompt.Tools{Have: have, Missing: missing}
}

// dryRun prints the planned schedule. It starts no review, but --suggest has
// already run by the time the mode is reached, so a dry run can still cost
// tokens.
func dryRun(out io.Writer, pal report.Palette, runs []*dirRun, agents []agent.Spec, opts *options) error {
	w := report.ErrWriter{Out: out}
	for _, d := range runs {
		if len(runs) > 1 {
			w.Printf("\n%s\n", pal.Bold(d.dir))
		}
		w.Println(pal.Bold("Dry run") + pal.Dim(": planned schedule for one loop"))
		names := append([]string(nil), d.reviews...)
		capped := opts.maxReviews > 0 && opts.maxReviews < len(d.reviews)
		if opts.stackedPRs {
			// Stack mode never shuffles, so the capped pass is knowable here:
			// the first N of the configured order.
			if capped {
				names = names[:opts.maxReviews]
			}
		} else {
			fuzzy.Sort(names)
		}
		col := 0
		for _, n := range names {
			col = max(col, report.Cells(n))
		}
		for _, n := range names {
			rev, _ := d.set.Get(n)
			origin := ""
			if rev.IsProject() {
				origin = " [project]"
			}
			w.Printf("  %s%s\n", report.PadCells(n, col+1), origin)
		}
		w.Println()
		repeats := len(d.reviews) - len(uniq(d.reviews))
		extra := ""
		if repeats > 0 {
			extra = fmt.Sprintf(" (%d extra from repeats)", repeats)
		}
		if capped {
			w.Printf("Reviews per loop: %d of %d%s, capped by --max-reviews\n",
				opts.maxReviews, len(d.reviews), extra)
		} else {
			w.Printf("Reviews per loop: %d%s\n", len(d.reviews), extra)
		}
	}
	mode := "sequential, in place"
	if opts.stackedPRs {
		mode = "sequential, one worktree, linear PR stack"
		if opts.maxLoops != 1 {
			mode = "sequential, new worktree per loop, linear PR stacks"
		}
	} else if opts.jobs > 1 {
		mode = fmt.Sprintf("%d lanes, worktree-isolated and merged back", opts.jobs)
	}
	yolo := ""
	if opts.yolo {
		yolo = "  |  YOLO"
	}
	w.Printf("Agents: %s  |  timeout: %s  |  mode: %s%s\n",
		strings.Join(agent.Labels(agents), ", "), humanize.Duration(opts.timeout), mode, yolo)
	limit := "infinite"
	if opts.maxLoops > 0 {
		limit = fmt.Sprint(opts.maxLoops)
	}
	w.Printf("Loop limit: %s\n", limit)
	if opts.runtime > 0 {
		w.Printf("Runtime budget: %s\n", humanize.Duration(opts.runtime))
	}
	if opts.commit || opts.push {
		action := "commit"
		if opts.push {
			action = "commit+push"
		}
		w.Printf("After each review: %s step (agent writes the message, no AI attribution)\n", action)
	}
	return w.Err
}

var (
	errAborted     = errors.New("aborted")
	errAgentFailed = errors.New("agent failed")
)

// planReviews fills in every directory's scheduled reviews.
//
// The suggest step is one agent launch per directory, each with its own
// timeout, so they run together: several trees would otherwise mean one
// half-hour wait after another before the first review starts. Their output
// is collected and printed in directory order, and one confirmation covers
// the lot.
//
// now stamps the suggest step's log lines. It is a parameter rather than a
// wall-clock read inside the closure so a caller replaying a run drives the
// whole transcript from one handle; production passes the run's clock, the
// same one the runner measures against, because these lines are live progress
// and a frozen stamp would be a lie.
func planReviews(ctx context.Context, runs []*dirRun, opts *options, agents []agent.Spec,
	out io.Writer, pal report.Palette, now func() time.Time) error {

	suggesting := opts.suggest
	pools := make([][]string, len(runs))
	// Expanded once per directory and kept. What the confirmation shows and
	// what the schedule ends up holding have to be filtered by the same set:
	// working it out twice is how they came to disagree, with the preview
	// naming reviews --exclude had already removed.
	excludes := make([]map[string]bool, len(runs))
	for i, d := range runs {
		excluded, err := excludedIn(d, opts)
		if err != nil {
			return err
		}
		excludes[i] = excluded
		if !suggesting {
			reviews, err := scheduleFor(d, opts, excluded)
			if err != nil {
				return err
			}
			d.reviews = reviews
			continue
		}
		for _, n := range d.set.Names {
			if !excluded[n] {
				pools[i] = append(pools[i], n)
			}
		}
		if len(pools[i]) == 0 {
			return fmt.Errorf("%s: no reviews remain after filtering", d.dir)
		}
	}
	if !suggesting {
		return nil
	}

	type suggestion struct {
		picked []prompt.Suggestion
		// named is the reviews the person put on the command line, already
		// filtered. It is what both the confirmation and the schedule are built
		// from, so the two cannot disagree.
		named []string
		spec  agent.Spec
		err   error
	}
	results := make([]suggestion, len(runs))
	var logMu sync.Mutex
	logf := func(dir, format string, a ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		where := ""
		if len(runs) > 1 {
			where = " [" + filepath.Base(dir) + "]"
		}
		fmt.Fprintf(out, "[%s]%s %s\n", humanize.Clock(now()), where,
			fmt.Sprintf(format, a...))
	}

	var wg sync.WaitGroup
	for i, d := range runs {
		wg.Go(func() {
			picked, spec, err := runner.Suggest(ctx, runner.SuggestConfig{
				// scanDir, not dir: a stacked run's suggestion signals come
				// from the fetched base snapshot, never the dirty checkout.
				Dir: d.scanDir(), Set: d.set, Pool: pools[i], Agents: agents, Only: opts.suggestAgent,
				Bin: opts.bin, Timeout: opts.suggestTimeout, Seed: opts.seed,
				// The same clock the log lines are stamped from, so a
				// caller with no run seed derives the shuffle from the
				// clock it is already printing.
				Now: now,
				Log: func(f string, a ...any) { logf(d.dir, f, a...) },
			})
			results[i] = suggestion{picked: picked, spec: spec, err: err}
		})
	}
	wg.Wait()

	total := 0
	for i, d := range runs {
		r := results[i]
		if r.err != nil {
			if errors.Is(r.err, context.Canceled) {
				return r.err
			}
			return fmt.Errorf("%w: %s: %w", errAgentFailed, d.dir, r.err)
		}
		where := ""
		if len(runs) > 1 {
			where = " in " + filepath.Base(d.dir)
		}
		fmt.Fprintf(out, "\n%s suggests %d of %d reviews%s:\n",
			r.spec.Label(), len(r.picked), len(pools[i]), where)
		col := 0
		for _, p := range r.picked {
			col = max(col, report.Cells(p.Name))
		}
		for _, p := range r.picked {
			reason := p.Reason
			if reason == "" {
				reason = "(no reason given)"
			}
			fmt.Fprintf(out, "  %s %s\n", report.PadCells(p.Name, col+1), pal.Dim(reason))
		}
		total += len(r.picked)
		// The same exclusions the schedule below applies, and the error is
		// raised rather than swallowed: a bad --reviews is worth reporting
		// before consent is asked for, not after it is given.
		named, err := namedIn(d, opts, excludes[i])
		if err != nil {
			return err
		}
		r.named = named
		results[i] = r
		if len(r.named) > 0 {
			fmt.Fprintf(out, "  %s\n", pal.Dim(fmt.Sprintf(
				"and %s, named on the command line", weighted(r.named))))
			total += len(r.named)
		}
	}
	if !opts.list && !opts.dryRun {
		if !confirm(out, opts, total) {
			fmt.Fprintln(out, "Aborted.")
			return errAborted
		}
	}
	for i, d := range runs {
		var scheduled []string
		for _, p := range results[i].picked {
			scheduled = append(scheduled, p.Name)
		}
		// What the person named rides along with what the agent picked, and a
		// review on both lists lands twice: repeats are weight to the
		// scheduler, so naming one is how you ask for more of it.
		scheduled = append(scheduled, results[i].named...)
		if len(scheduled) == 0 {
			return fmt.Errorf("%s: the suggest step picked no reviews", d.dir)
		}
		d.reviews = scheduled
	}
	return nil
}

// namedIn expands the reviews a person named on the command line, minus the
// exclusions. Empty when --reviews was not given: then the suggestion stands
// on its own.
func namedIn(d *dirRun, opts *options, excluded map[string]bool) ([]string, error) {
	if err := refuseEmptyReviews(opts); err != nil {
		return nil, err
	}
	if !opts.reviewsSet {
		return nil, nil
	}
	names, err := d.set.Expand(opts.reviews, "--reviews", false)
	if err != nil {
		return nil, err
	}
	kept := names[:0]
	for _, n := range names {
		if !excluded[n] {
			kept = append(kept, n)
		}
	}
	return kept, nil
}

// refuseEmptyReviews reports the one --reviews value that is a flag asking for
// nothing. Parsing keeps an empty --reviews explicit on purpose so it cannot
// expand to every review, and the run is where it is caught: by then the flag
// is the only thing that can say so, and "no reviews remain after filtering"
// names a filter that never ran.
func refuseEmptyReviews(opts *options) error {
	if !opts.reviewsSet || strings.TrimSpace(opts.reviews) != "" {
		return nil
	}
	return errors.New("--reviews is empty: name at least one review or set, " +
		"or drop the flag to run every review")
}

// weighted names a schedule the way a person reads it: one entry per review,
// with the repeats that give it weight spelled out.
func weighted(names []string) string {
	counts := map[string]int{}
	order := make([]string, 0, len(names))
	for _, n := range names {
		if counts[n] == 0 {
			order = append(order, n)
		}
		counts[n]++
	}
	parts := make([]string, 0, len(order))
	for _, n := range order {
		if counts[n] > 1 {
			parts = append(parts, fmt.Sprintf("%s (x%d)", n, counts[n]))
			continue
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, ", ")
}

// excludedIn expands --exclude against one directory's discovered set.
func excludedIn(d *dirRun, opts *options) (map[string]bool, error) {
	excluded := map[string]bool{}
	if opts.exclude == "" {
		return excluded, nil
	}
	names, err := d.set.Expand(opts.exclude, "--exclude", true)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		excluded[n] = true
	}
	return excluded, nil
}

// scheduleFor turns --reviews (or its absence) into one directory's list.
func scheduleFor(d *dirRun, opts *options, excluded map[string]bool) ([]string, error) {
	if err := refuseEmptyReviews(opts); err != nil {
		return nil, err
	}
	var scheduled []string
	if opts.reviewsSet {
		names, err := d.set.Expand(opts.reviews, "--reviews", false)
		if err != nil {
			return nil, err
		}
		scheduled = names
	} else {
		scheduled = append(scheduled, d.set.Names...)
	}
	kept := scheduled[:0]
	for _, n := range scheduled {
		if !excluded[n] {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return nil, errors.New("no reviews remain after filtering")
	}
	return kept, nil
}

// stdin is the one buffered view of os.Stdin for the whole process. A second
// bufio.Reader over the same file reads ahead and drops what it buffered, so
// two prompts in one run (the run plan, then the commit a parallel run needs)
// would answer the second from EOF. Piped answers hit this first: bufio
// takes every line the pipe holds in its first read.
var stdin = bufio.NewReader(os.Stdin)

// answeredYes reads one line from in and reports whether it accepts the
// prompt. defaultYes is what an empty answer means, and is set by the
// [Y/n] or [y/N] the prompt prints.
func answeredYes(in *bufio.Reader, defaultYes bool) (bool, bool) {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return defaultYes, true
	case "y", "yes":
		return true, true
	}
	return false, true
}

func confirm(out io.Writer, opts *options, n int) bool {
	if opts.yes || opts.yolo {
		fmt.Fprintln(out, "Proceeding without confirmation.")
		return true
	}
	// A real terminal check, not a ModeCharDevice one: /dev/null is a
	// character device, and a run under cron would otherwise prompt, read
	// EOF, and abort. The same answer cmdPick's tty gate gives.
	if !stdinIsTerminal() {
		fmt.Fprintln(out, "stdin is not a terminal: proceeding without confirmation.")
		return true
	}
	fmt.Fprintf(out, "\nRun these %d reviews? [Y/n] ", n)
	yes, ok := answeredYes(stdin, true)
	if !ok {
		fmt.Fprintln(out)
	}
	return yes
}
