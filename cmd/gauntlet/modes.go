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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
)

// cmdShowPrompt prints the exact text an agent would receive.
func cmdShowPrompt(out io.Writer, set prompt.Set, opts *options) int {
	name := opts.showPrompt
	if _, ok := set.Get(name); !ok {
		if _, ok := set.Get(name + "-review"); ok {
			name += "-review"
		} else {
			fmt.Fprintf(os.Stderr, "Unknown review: %s\nReviews: %s\n",
				opts.showPrompt, strings.Join(set.Names, ", "))
			return exitUsage
		}
	}
	rev, _ := set.Get(name)
	body, err := rev.Body()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read prompt for %s: %v\n", name, err)
		return exitUsage
	}
	// The point of --show-prompt is the exact text, and part of that text is
	// what this machine has: probe the same way a run would.
	//
	// What reaches the terminal is the display form, though, not the bytes:
	// the body may come from a hostile *-review.md, and an escape sequence
	// planted there (an OSC 52 clipboard overwrite, cursor addressing) would
	// fire against whoever inspected it here before deciding to run. The
	// agent still receives the exact bytes; Display strips only what could
	// drive or spoof the terminal.
	fmt.Fprintln(out, normalize.Display(
		prompt.Compose(body, opts.timeout, name, opts.yolo, toolsFor(name))))
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

// dryRun prints the planned schedule without launching anything.
func dryRun(out io.Writer, pal palette, runs []*dirRun, agents []agent.Spec, opts *options) {
	for _, d := range runs {
		if len(runs) > 1 {
			fmt.Fprintf(out, "\n%s\n", pal.bold(d.dir))
		}
		fmt.Fprintln(out, pal.bold("Dry run")+pal.dim(": planned schedule for one loop"))
		names := append([]string(nil), d.reviews...)
		if !opts.stackedPRs {
			sort.Strings(names)
		}
		col := 0
		for _, n := range names {
			col = max(col, len(n))
		}
		for _, n := range names {
			rev, _ := d.set.Get(n)
			origin := ""
			if rev.IsProject() {
				origin = " [project]"
			}
			fmt.Fprintf(out, "  %-*s%s\n", col+1, n, origin)
		}
		fmt.Fprintln(out)
		repeats := len(d.reviews) - len(uniq(d.reviews))
		extra := ""
		if repeats > 0 {
			extra = fmt.Sprintf(" (%d extra from repeats)", repeats)
		}
		fmt.Fprintf(out, "Reviews per loop: %d%s\n", len(d.reviews), extra)
	}
	mode := "sequential, in place"
	if opts.stackedPRs {
		mode = "sequential, one worktree, linear PR stack"
	} else if opts.jobs > 1 {
		mode = fmt.Sprintf("%d lanes, worktree-isolated and merged back", opts.jobs)
	}
	yolo := ""
	if opts.yolo {
		yolo = "  |  YOLO"
	}
	fmt.Fprintf(out, "Agents: %s  |  timeout: %s  |  mode: %s%s\n",
		strings.Join(runner.AgentLabels(agents), ", "), humanize.Duration(opts.timeout), mode, yolo)
	limit := "infinite"
	if opts.maxLoops > 0 {
		limit = fmt.Sprint(opts.maxLoops)
	}
	fmt.Fprintf(out, "Loop limit: %s\n", limit)
	if opts.runtime > 0 {
		fmt.Fprintf(out, "Runtime budget: %s\n", humanize.Duration(opts.runtime))
	}
	if opts.commit || opts.push {
		action := "commit"
		if opts.push {
			action = "commit+push"
		}
		fmt.Fprintf(out, "After each review: %s step (agent writes the message, no AI attribution)\n", action)
	}
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
func planReviews(ctx context.Context, runs []*dirRun, opts *options, agents []agent.Spec,
	out io.Writer, pal palette) error {

	suggesting := opts.suggest
	pools := make([][]string, len(runs))
	for i, d := range runs {
		excluded, err := excludedIn(d, opts)
		if err != nil {
			return err
		}
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
		spec   agent.Spec
		err    error
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
		fmt.Fprintf(out, "[%s]%s %s\n", time.Now().Format("15:04:05"), where,
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
				return errAborted
			}
			return fmt.Errorf("%w: %s: %v", errAgentFailed, d.dir, r.err)
		}
		where := ""
		if len(runs) > 1 {
			where = " in " + filepath.Base(d.dir)
		}
		fmt.Fprintf(out, "\n%s suggests %d of %d reviews%s:\n",
			r.spec.Label(), len(r.picked), len(pools[i]), where)
		col := 0
		for _, p := range r.picked {
			col = max(col, len(p.Name))
		}
		for _, p := range r.picked {
			reason := p.Reason
			if reason == "" {
				reason = "(no reason given)"
			}
			fmt.Fprintf(out, "  %-*s %s\n", col+1, p.Name, pal.dim(reason))
		}
		total += len(r.picked)
		if named, err := namedIn(d, opts, map[string]bool{}); err == nil && len(named) > 0 {
			fmt.Fprintf(out, "  %s\n", pal.dim(fmt.Sprintf(
				"and %s, named on the command line", weighted(named))))
			total += len(named)
		}
	}
	if !confirm(out, opts, total) {
		fmt.Fprintln(out, "Aborted.")
		return errAborted
	}
	for i, d := range runs {
		var scheduled []string
		for _, p := range results[i].picked {
			scheduled = append(scheduled, p.Name)
		}
		// What the person named rides along with what the agent picked, and a
		// review on both lists lands twice: repeats are weight to the
		// scheduler, so naming one is how you ask for more of it.
		excluded, err := excludedIn(d, opts)
		if err != nil {
			return err
		}
		named, err := namedIn(d, opts, excluded)
		if err != nil {
			return err
		}
		scheduled = append(scheduled, named...)
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
	if !opts.reviewsSet || strings.TrimSpace(opts.reviews) == "" {
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
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}
