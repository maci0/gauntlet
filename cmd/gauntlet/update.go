// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

// autoUpdateEvery is how often --auto-update asks GitHub for a new release.
// Deliberately slow: an update check is never on the critical path.
const autoUpdateEvery = 6 * time.Hour

// autoUpdateDelay is how long --auto-update waits before its first check, so
// the network never delays the first review.
const autoUpdateDelay = 30 * time.Second

// autoUpdateLoop replaces the binary in the background. The reload watcher
// notices the new file and schedules the swap, so nothing here touches the run.
func autoUpdateLoop(ctx context.Context, opts *options, bus *runner.Bus) {
	t := time.NewTicker(autoUpdateEvery)
	defer t.Stop()
	// A first check shortly after start, so a run shorter than the interval
	// still benefits; then on the interval. Never on the startup path itself.
	first := time.NewTimer(autoUpdateDelay)
	defer first.Stop()
	firstC := first.C
	applied := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-firstC:
			firstC = nil
		case <-t.C:
		}
		rel, err := selfupdate.Check(ctx, opts.updateRepo)
		if err != nil {
			// An opted-in updater that keeps failing should say so, not sit
			// silently on an old binary.
			bus.Publish(runner.Event{Kind: runner.EvLog,
				Text: fmt.Sprintf("update check failed: %v", err)})
			continue
		}
		if !rel.NewerThan(version) || rel.TagName == applied {
			continue
		}
		if _, err := selfupdate.Apply(ctx, rel); err != nil {
			bus.Publish(runner.Event{Kind: runner.EvLog,
				Text: fmt.Sprintf("auto-update to %s failed: %v", rel.TagName, err)})
			continue
		}
		applied = rel.TagName
		bus.Publish(runner.Event{Kind: runner.EvLog,
			Text: fmt.Sprintf("updated to %s on disk; reloading at the next safe point", rel.TagName)})
	}
}

// cmdUpdate implements `gauntlet update`.
func cmdUpdate(ctx context.Context, out io.Writer, pal palette, opts *options) int {
	rel, err := selfupdate.Check(ctx, opts.updateRepo)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return 128 + int(syscall.SIGINT)
		}
		fmt.Fprintf(os.Stderr, "cannot check for updates: %v\n", err)
		return exitFail
	}
	if !rel.NewerThan(version) {
		if _, err := fmt.Fprintf(out, "gauntlet %s is current (latest release: %s)\n", version, rel.TagName); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write update status: %v\n", err)
			return exitFail
		}
		return exitOK
	}
	if _, err := fmt.Fprintf(out, "New release: %s (running %s)\n", pal.bold(rel.TagName), version); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write update status: %v\n", err)
		return exitFail
	}
	if opts.checkOnly {
		if _, err := fmt.Fprintln(out, rel.HTMLURL); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write update status: %v\n", err)
			return exitFail
		}
		return exitOK
	}
	path, err := selfupdate.Apply(ctx, rel)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return 128 + int(syscall.SIGINT)
		}
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return exitFail
	}
	if _, err := fmt.Fprintf(out, "Installed %s to %s\n", rel.TagName, path); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write update status: %v\n", err)
		return exitFail
	}
	return exitOK
}
