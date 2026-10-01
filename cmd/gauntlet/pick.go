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
	"github.com/maci0/gauntlet/internal/evidence"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/ui"
)

// cmdPick opens the launcher and runs what it composed. The launcher itself
// decides nothing: it hands back an argv, which goes through the same parser
// and the same run path as a hand-typed one, so there is no second way to
// start a run that could drift from the first.
func cmdPick(ctx context.Context, out io.Writer, opts *options) int {
	// The launcher composes one run for one tree, so a --dirs that names
	// several is refused rather than narrowed: taking the first would drop
	// the rest of the list without a word, and the run the user composed would
	// cover a different set of trees than the one they named.
	if len(opts.resolvedDirs) > 1 {
		fmt.Fprintf(os.Stderr, "pick reviews one directory; --dirs named %d. "+
			"Run gauntlet once per directory instead.\n", len(opts.resolvedDirs))
		return exitUsage
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "pick needs a terminal on stdin and stdout")
		return exitUsage
	}
	dir := opts.resolvedDirs[0]

	set, warnings, err := prompt.Discover(ctx, opts.promptDir, dir)
	if err != nil {
		if interrupted(ctx, err) {
			return exitInterrupted
		}
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	// A dropped project prompt is a review the launcher will not offer and the
	// run will not schedule, and a conflicting duplicate is one of two files
	// where only one is ever read. main.go prints the same warnings for the
	// hand-typed path; a launcher that omitted them would let the same tree
	// report two different review sets depending on which way the run was
	// started.
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}
	if set.Len() == 0 {
		fmt.Fprintf(os.Stderr, "No reviews found for %s\n", dir)
		return exitUsage
	}

	branch, targets, dirty, err := treeState(ctx, dir)
	if err != nil {
		// A git failure and a detached HEAD both leave the launcher with no
		// branch to name, and the screen renders the first as "this checkout".
		// An operator composing a run would read that as a tree with nothing to
		// merge into and nothing uncommitted, when the truth is that git never
		// answered. Saying so costs one line and keeps the composed run from
		// being chosen against a repository state nobody could read.
		fmt.Fprintf(os.Stderr, "cannot read the repository state of %s: %v\n", dir, err)
		return exitFail
	}
	argv, ok, err := ui.Pick(ui.PickConfig{
		Dir:         dir,
		PromptDir:   opts.promptDir,
		Groups:      pickGroups(set),
		Reserved:    append(prompt.SetNames(), prompt.Suggest),
		FastSuggest: evidence.AgentName,
		Agents:      agent.Labels(agent.Installed()),
		Branch:      branch,
		Merge:       targets,
		Dirty:       dirty,
		CPUs:        runtime.NumCPU(),
		Version:     version,
	})
	if err != nil {
		if interrupted(ctx, err) {
			return exitInterrupted
		}
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	if !ok {
		if ctx.Err() != nil {
			return exitInterrupted
		}
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
//
// A git that cannot answer is not a repository with nothing to say.
// CurrentBranch already goes to some lengths to keep a detached HEAD
// ("", nil) apart from a broken repository, and this was the one caller that
// threw the distinction away: a failing git rendered as a branchless, clean
// checkout, and an operator composing a run would read that as a tree with
// nothing to merge into and nothing uncommitted. A directory git does not
// manage is still not a failure — there is no branch to offer — so
// ErrNotRepository keeps the preflight's answers empty instead of refusing
// the launcher over a tree that never had git state to read.
func treeState(ctx context.Context, dir string) (branch string, targets []string, dirty bool, err error) {
	repo := gitx.Open(dir)
	branch, err = repo.CurrentBranch(ctx)
	if err != nil {
		if gitx.IsNotRepository(err) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	for _, b := range repo.Branches(ctx) {
		if b != branch {
			targets = append(targets, b)
		}
	}
	ch, err := repo.Status(ctx, nil)
	if err != nil {
		return "", nil, false, err
	}
	return branch, targets, len(ch.Tracked) > 0, nil
}
