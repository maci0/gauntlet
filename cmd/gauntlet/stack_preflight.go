// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/runner"
)

// stackPreflight runs one directory's stacked-mode checks before any agent
// (the suggestion agent included) can start: the dirty-checkout consent, the
// remote and gh validation, the pinned base fetch, and the snapshot worktree
// that prompt discovery and the suggest step read instead of the checkout.
func stackPreflight(ctx context.Context, d *dirRun, opts *options, carried dirHandoff,
	resumed bool, runID string, ownArtifacts map[string]bool,
	in *bufio.Reader, interactive bool, out io.Writer) error {

	cfg := runner.Config{
		Dir: d.dir, StackedPRs: true, PRBase: opts.prBase, PushRemote: opts.pushRemote,
		OwnArtifacts: ownArtifacts, RunID: runID,
		// A hot-reload successor continues an isolation decision already made
		// by the original process, and keeps the base commit it pinned:
		// prompting again could strand the run, and fetching again could hand
		// it a base that moved, splitting the resumed stack into a new one.
		AllowDirtyStack: resumed,
		ResumeStackTip:  carried.StackBaseTip,
	}
	if resumed && carried.StackBase != "" {
		cfg.PRBase = carried.StackBase
	}
	prep, err := runner.PrepareStack(ctx, cfg)
	if dirty, ok := errors.AsType[*runner.StackDirtyError](err); ok {
		proceed, confirmErr := confirmStackIsolationWith(out, in, interactive, opts, dirty)
		if confirmErr != nil {
			return confirmErr
		}
		if !proceed {
			return errAborted
		}
		cfg.AllowDirtyStack = true
		prep, err = runner.PrepareStack(ctx, cfg)
	}
	if err != nil {
		return err
	}
	d.prep = prep
	d.repo = gitx.Open(d.dir)
	snap, err := d.repo.AddSnapshotWorktree(ctx, runID, prep.BaseTip)
	if err != nil {
		return fmt.Errorf("cannot create the base snapshot worktree in %s: %w", d.dir, err)
	}
	d.snapshot = snap
	return nil
}

// cleanupSnapshots removes the base snapshot checkouts a stacked run read its
// prompts from. They outlive the runners on purpose: prompt bodies are read
// from them for as long as reviews launch.
func cleanupSnapshots(runs []*dirRun) {
	for _, d := range runs {
		if d.snapshot == nil {
			continue
		}
		if err := d.snapshot.Remove(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot remove snapshot worktree %s: %v\n", d.snapshot.Dir, err)
		} else if d.repo != nil {
			d.repo.CleanWorktreeRoot()
		}
	}
}

// confirmStackIsolationWith makes the omission boundary explicit. A stacked
// run is safe beside a dirty checkout because it starts from a fetched remote
// commit, but silently reviewing a different tree from the one on screen is
// surprising. Unattended callers must opt in with --yes.
func confirmStackIsolationWith(out io.Writer, in *bufio.Reader, interactive bool, opts *options,
	dirty *runner.StackDirtyError) (bool, error) {
	paths := dirty.DisplayPaths()
	fmt.Fprintf(out, "\nUNCOMMITTED FILES (%d)\n", len(paths))
	const displayLimit = 20
	for _, path := range paths[:min(len(paths), displayLimit)] {
		fmt.Fprintf(out, "  %s\n", path)
	}
	if len(paths) > displayLimit {
		fmt.Fprintf(out, "  ... and %d more\n", len(paths)-displayLimit)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "These files stay in the original checkout.")
	fmt.Fprintln(out, "They will not be reviewed or included in the PRs.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "STACK BASE")
	fmt.Fprintf(out, "  remote  %s\n", dirty.Remote)
	fmt.Fprintf(out, "  branch  %s\n", dirty.Base)
	fmt.Fprintln(out, "  After confirmation, gauntlet fetches this remote branch and starts")
	fmt.Fprintln(out, "  the isolated worktree from the fetched commit.")
	if opts.yes || opts.yolo {
		flag := "--yes"
		if opts.yolo {
			flag = "--yolo"
		}
		fmt.Fprintf(out, "Proceeding (%s).\n", flag)
		return true, nil
	}
	if !interactive {
		return false, errors.New("stacked PRs need confirmation to exclude uncommitted changes; rerun with --yes")
	}
	fmt.Fprint(out, "Continue? [y/N] ")
	line, err := in.ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
