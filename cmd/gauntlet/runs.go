// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
)

// cmdRuns lists recent runs from ~/.gauntlet/index.jsonl.
func cmdRuns(out io.Writer, pal palette, limit int) int {
	entries, err := journal.Recent(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read run index: %v\n", err)
		return exitFail
	}
	if len(entries) == 0 {
		fmt.Fprintf(out, "No runs recorded yet under %s\n", journal.Home())
		return exitOK
	}
	fmt.Fprintf(out, "%-22s  %-16s  %8s  %5s  %5s  %6s  %9s  %11s  %s\n",
		"RUN", "STARTED", "DURATION", "LOOPS", "OK", "FAILED", "TOKENS", "LINES", "DIRS")
	// The column's name overstates what it counts; say so once, right where
	// it first appears, or a run that only skipped reviews reads as broken.
	fmt.Fprintln(out, pal.dim("   FAILED counts timeouts, skipped reviews, and merge conflicts too"))
	for _, e := range entries {
		dur := humanize.Duration(e.End.Sub(e.Start))
		dirs := make([]string, 0, len(e.Dirs))
		for _, d := range e.Dirs {
			dirs = append(dirs, filepath.Base(d))
		}
		bad := e.Failed + e.Skipped + e.Conflicts
		failed := failedCell(pal, bad)
		// A run that reported no tokens says so, rather than showing zero.
		tokens := "n/a"
		if e.Tokens > 0 {
			tokens = humanize.Count(e.Tokens)
		}
		lines := fmt.Sprintf("+%d/-%d", e.Ins, e.Del)
		fmt.Fprintf(out, "%-22s  %-16s  %8s  %5d  %5d  %s  %9s  %11s  %s\n",
			e.RunID, e.Start.Local().Format("01-02 15:04:05"), dur,
			e.Loops, e.OK, failed, tokens, lines, strings.Join(dirs, ","))
	}
	fmt.Fprintf(out, "\n%s\n", pal.dim("Journals: "+filepath.Join(journal.Home(), "runs")))
	return exitOK
}

// failedCell renders one FAILED cell. The column is padded first and colored
// second: escape codes inside a %6s verb are counted as width, which shoves
// every later column off its header.
func failedCell(pal palette, bad int) string {
	s := fmt.Sprintf("%6d", bad)
	if bad > 0 {
		return pal.red(s)
	}
	return s
}

// cmdShow replays one run's journal as readable lines.
func cmdShow(out io.Writer, runID string) int {
	// One event in memory at a time: a long run's journal can be far larger
	// than the screen it is being replayed onto.
	err := journal.Events(runID, func(ev map[string]any) {
		ts := ""
		if s, ok := ev["ts"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				ts = t.Local().Format("15:04:05")
			}
		}
		kind, _ := ev["ev"].(string)
		delete(ev, "ts")
		delete(ev, "ev")
		rest, _ := json.Marshal(ev)
		fmt.Fprintf(out, "%s  %-13s %s\n", ts, kind, string(rest))
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// An id that names nothing is a bad argument, like an unknown review
		// name for --show-prompt: usage error. A journal that exists but
		// cannot be read is a general failure.
		if errors.Is(err, journal.ErrNoJournal) {
			return exitUsage
		}
		return exitFail
	}
	return exitOK
}

// runIndexer runs a helper binary to completion, streaming nothing: its output
// goes straight to the terminal.
func runIndexer(ctx context.Context, bin string, args []string, dir string) int {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}
