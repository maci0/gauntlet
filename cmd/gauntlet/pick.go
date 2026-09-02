// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/ui"
)

// cmdPick opens the launcher and runs what it composed. The launcher itself
// decides nothing: it hands back an argv, which goes through the same parser
// and the same run path as a hand-typed one, so there is no second way to
// start a run that could drift from the first.
func cmdPick(ctx context.Context, out io.Writer, opts *options) int {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "pick needs a terminal on stdin and stdout")
		return exitUsage
	}
	dirs, err := resolveDirs(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	dir := dirs[0] // the launcher composes one run for one tree

	set, _, err := prompt.Discover(ctx, opts.promptDir, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if set.Len() == 0 {
		fmt.Fprintf(os.Stderr, "No reviews found for %s\n", dir)
		return exitUsage
	}

	branch, targets, dirty := treeState(ctx, dir)
	argv, ok, err := ui.Pick(ui.PickConfig{
		Dir:         dir,
		Groups:      pickGroups(set),
		Reserved:    append(prompt.SetNames(), prompt.Suggest),
		FastSuggest: runner.FastSuggestAgent,
		Agents:      agent.Labels(agent.Installed()),
		Branch:      branch,
		Merge:       targets,
		Dirty:       dirty,
		CPUs:        runtime.NumCPU(),
		Version:     version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	if !ok {
		return exitOK
	}
	fmt.Fprintln(out, "gauntlet "+strings.Join(argv, " "))
	return run(argv)
}

// pickGroups turns the discovered reviews into the launcher's collapsible
// categories: the named sets first, then whatever no set claims, so every
// review is reachable from the tree.
func pickGroups(set prompt.Set) []ui.PickGroup {
	describe := func(name string) ui.PickReview {
		rev, _ := set.Get(name)
		return ui.PickReview{Name: name, Desc: rev.Summary(), Project: rev.IsProject()}
	}
	claimed := map[string]bool{}
	var groups []ui.PickGroup
	for _, name := range prompt.SetNames() {
		if _, dynamic := prompt.DynamicSets[name]; dynamic {
			continue // "all" and "project" are views, not choices
		}
		var members []ui.PickReview
		for _, r := range prompt.Sets[name] {
			if _, ok := set.Get(r); ok {
				members = append(members, describe(r))
				claimed[r] = true
			}
		}
		if len(members) > 0 {
			groups = append(groups, ui.PickGroup{Name: name, Reviews: members})
		}
	}
	var rest []ui.PickReview
	for _, name := range set.Names {
		if !claimed[name] {
			rest = append(rest, describe(name))
		}
	}
	if len(rest) > 0 {
		groups = append(groups, ui.PickGroup{Name: "other", Reviews: rest})
	}
	return groups
}

// treeState is what the launcher needs to know about the repository: the
// branch a run would sit on, the other local branches it could be merged
// into, and whether tracked files are dirty, which is what worktree isolation
// refuses. Untracked files do not block --jobs, so they do not block the
// launcher either. Outside a git repository there is none of it, and the
// launcher simply does not offer those choices.
func treeState(ctx context.Context, dir string) (branch string, targets []string, dirty bool) {
	repo := gitx.Open(dir)
	if repo == nil {
		return "", nil, false
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return "", nil, false
	}
	for _, b := range repo.Branches(ctx) {
		if b != branch {
			targets = append(targets, b)
		}
	}
	ch, err := repo.Status(ctx, nil)
	return branch, targets, err == nil && len(ch.Tracked) > 0
}
