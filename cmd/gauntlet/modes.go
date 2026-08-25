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
	"sort"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/humanize"
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
	fmt.Fprintln(out, prompt.Compose(body, opts.timeout, name, opts.yolo))
	return exitOK
}

// dryRun prints the planned schedule without launching anything.
func dryRun(out io.Writer, pal palette, runs []*dirRun, agents []agent.Spec, opts *options) {
	for _, d := range runs {
		if len(runs) > 1 {
			fmt.Fprintf(out, "\n%s\n", pal.bold(d.dir))
		}
		fmt.Fprintln(out, "DRY RUN: planned schedule for one loop:")
		names := append([]string(nil), d.reviews...)
		sort.Strings(names)
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
		weighted := len(d.reviews) - len(uniq(d.reviews))
		extra := ""
		if weighted > 0 {
			extra = fmt.Sprintf(" (%d extra from repeats)", weighted)
		}
		fmt.Fprintf(out, "Reviews per loop: %d%s\n", len(d.reviews), extra)
	}
	mode := "sequential, in place"
	if opts.jobs > 1 {
		mode = fmt.Sprintf("%d at a time, one git worktree per review, merged back", opts.jobs)
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

// selectReviews turns the flags into the scheduled review list for one
// directory, running the suggest step when asked.
func selectReviews(ctx context.Context, d *dirRun, opts *options, agents []agent.Spec,
	out io.Writer, pal palette) ([]string, error) {

	excluded := map[string]bool{}
	if opts.exclude != "" {
		names, err := d.set.Expand(opts.exclude, "--exclude", true)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			excluded[n] = true
		}
	}

	var scheduled []string
	switch {
	case strings.TrimSpace(opts.reviews) == prompt.Suggest:
		var pool []string
		for _, n := range d.set.Names {
			if !excluded[n] {
				pool = append(pool, n)
			}
		}
		if len(pool) == 0 {
			return nil, errors.New("no reviews remain after filtering")
		}
		picked, spec, err := runner.Suggest(ctx, runner.SuggestConfig{
			Dir: d.dir, Set: d.set, Pool: pool, Agents: agents, Only: opts.suggestAgent,
			Bin: opts.bin, Timeout: opts.suggestTimeout, Seed: opts.seed,
			Log: func(f string, a ...any) {
				fmt.Fprintf(out, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(f, a...))
			},
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, errAborted
			}
			return nil, fmt.Errorf("%w: %v", errAgentFailed, err)
		}
		fmt.Fprintf(out, "\n%s suggests %d of %d reviews:\n", spec.Label(), len(picked), len(pool))
		col := 0
		for _, p := range picked {
			col = max(col, len(p.Name))
		}
		for _, p := range picked {
			reason := p.Reason
			if reason == "" {
				reason = "(no reason given)"
			}
			fmt.Fprintf(out, "  %-*s %s\n", col+1, p.Name, pal.dim(reason))
		}
		if !confirm(out, opts, len(picked)) {
			fmt.Fprintln(out, "Aborted.")
			return nil, errAborted
		}
		for _, p := range picked {
			scheduled = append(scheduled, p.Name)
		}
	case opts.reviewsSet:
		names, err := d.set.Expand(opts.reviews, "--reviews", false)
		if err != nil {
			return nil, err
		}
		scheduled = names
	default:
		scheduled = append(scheduled, d.set.Names...)
	}

	out2 := scheduled[:0]
	for _, n := range scheduled {
		if !excluded[n] {
			out2 = append(out2, n)
		}
	}
	if len(out2) == 0 {
		return nil, errors.New("no reviews remain after filtering")
	}
	return out2, nil
}

// confirm asks before running an agent-picked review list. --yes, --yolo, and
// a non-terminal stdin all proceed without asking.
func confirm(out io.Writer, opts *options, n int) bool {
	if opts.yes || opts.yolo {
		fmt.Fprintln(out, "Proceeding without confirmation.")
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
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
