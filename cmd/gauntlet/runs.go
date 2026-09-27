// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
)

// cmdRuns lists recent runs from ~/.gauntlet/index.jsonl. A run id restores a
// pruned run, which is a different job from listing and happens instead of it.
func cmdRuns(out io.Writer, pal palette, limit int, restore string) (code int) {
	if restore != "" {
		return restoreRun(out, pal, restore)
	}
	entries, err := journal.Recent(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read run index: %v\n", err)
		return exitFail
	}
	bw := bufio.NewWriter(out)
	w := errWriter{out: bw}
	defer func() {
		if err := bw.Flush(); w.err == nil && err != nil {
			w.err = err
		}
		if w.err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the run listing: %v\n", w.err)
			code = exitFail
		}
	}()
	if len(entries) == 0 {
		w.printf("No runs recorded yet under %s\n", journal.Home())
		return exitOK
	}
	w.printf("%-22s  %-19s  %8s  %5s  %5s  %6s  %9s  %11s  %s\n",
		"RUN", "STARTED", "DURATION", "LOOPS", "OK", "FAILED", "TOKENS", "LINES", "DIRS")
	// The column's name overstates what it counts; say so once, right where
	// it first appears, or a run that only skipped reviews reads as broken.
	// It has to name every bucket, including the one that catches a terminal
	// status a newer journal wrote and this build cannot read.
	w.printf("%s\n", pal.dim("   FAILED counts timeouts, skipped reviews, merge conflicts, and statuses this build does not recognize"))
	for _, e := range entries {
		dur := "n/a"
		if d, ok := e.Duration(); ok {
			dur = humanize.Duration(d)
		}
		dirs := make([]string, 0, len(e.Dirs))
		for _, d := range e.Dirs {
			dirs = append(dirs, filepath.Base(d))
		}
		bad := e.Failed + e.Skipped + e.Conflicts + e.Other
		failed := failedCell(pal, bad)
		// A run that reported no tokens says so, rather than showing zero.
		tokens := "n/a"
		if e.Tokens > 0 {
			tokens = humanize.Count(e.Tokens)
		}
		lines := fmt.Sprintf("+%d/-%d", e.Ins, e.Del)
		if !e.LinesMeasured {
			// Git was missing, or concurrent reviews made attribution
			// impossible. An unmeasured run is not a run that changed
			// nothing, and +0/-0 says it was.
			lines = "n/a"
		}
		started := "n/a"
		if !e.Start.IsZero() {
			started = e.Start.Local().Format("2006-01-02 15:04:05")
		}
		w.printf("%-22s  %-19s  %8s  %5d  %5d  %s  %9s  %11s  %s\n",
			e.RunID, started, dur,
			e.Loops, e.OK, failed, tokens, lines, strings.Join(dirs, ","))
	}
	// Where the journals live is a fact about this machine, not a row of the
	// table, so it goes to stderr. The legend above stays on stdout: it heads
	// the table and a consumer skipping two lines knows where the rows start.
	// Without this the last line of `gauntlet runs | tail -1` was a path.
	fmt.Fprintf(os.Stderr, "\nJournals: %s\n", filepath.Join(journal.Home(), "runs"))
	// A prune is unattended, so the runs it moved out of the listing are
	// named where the user reads the listing: a dropped run is recoverable
	// only by someone who knows it is still on disk. The bound keeps a
	// large quarantine from printing two hundred ids.
	if held, err := journal.Quarantined(); err == nil && len(held) > 0 {
		shown := held
		more := ""
		if len(shown) > listedQuarantined {
			more = fmt.Sprintf(" and %d more under %s",
				len(shown)-listedQuarantined, filepath.Join(journal.Home(), "pruned"))
			shown = shown[:listedQuarantined]
		}
		w.printf("%s\n", pal.dim(fmt.Sprintf(
			"Pruned, still recoverable (gauntlet runs --restore ID): %s%s",
			strings.Join(shown, " "), more)))
	}
	return exitOK
}

// listedQuarantined is how many pruned run ids a listing names before it
// counts the rest.
const listedQuarantined = 5

// restoreRun puts a pruned journal back in the listing and says so plainly:
// a restore that failed has to read as a failure, not as a listing.
func restoreRun(out io.Writer, pal palette, runID string) int {
	if err := journal.Restore(runID); err != nil {
		switch {
		case errors.Is(err, journal.ErrNotPruned),
			errors.Is(err, journal.ErrAlreadyListed),
			errors.Is(err, journal.ErrInvalidRunID):
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return exitUsage
		default:
			fmt.Fprintf(os.Stderr, "cannot restore run %s: %v\n", runID, err)
			return exitFail
		}
	}
	fmt.Fprintf(out, "Restored %s; %s\n", runID,
		pal.dim("gauntlet show "+runID))
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

// showTime renders a journal timestamp as local wall clock. UnmarshalText
// is the inverse of encoding/json's time.Time marshal, so a Z stamp, an
// offset, and a fractional second all round-trip; Parse(RFC3339Nano) is
// close but not that inverse, and a stamp the encoder wrote must replay.
func showTime(s string) string {
	var t time.Time
	if err := t.UnmarshalText([]byte(s)); err != nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("15:04:05")
}

// cmdShow replays one run's journal as readable lines.
func cmdShow(out io.Writer, runID string) int {
	// One event in memory at a time: a long run's journal can be far larger
	// than the screen it is being replayed onto.
	bw := bufio.NewWriter(out)
	defer bw.Flush()
	var werr error
	err := journal.Events(runID, func(ev map[string]any) {
		if werr != nil {
			return
		}
		ts := ""
		if s, ok := ev["ts"].(string); ok {
			ts = showTime(s)
		}
		kind, _ := ev["ev"].(string)
		delete(ev, "ts")
		delete(ev, "ev")
		rest, _ := json.Marshal(ev)
		// The journal records events verbatim, and event text can carry
		// fragments of a hostile repository (git error output, merge-conflict
		// file names). json.Marshal escapes control bytes but lets bidi
		// overrides and other formatting characters through, and this line
		// goes straight to a terminal: strip them here, where every other
		// display surface already does.
		_, werr = fmt.Fprintf(bw, "%s  %-13s %s\n", ts, kind, normalize.Sanitize(string(rest)))
	})
	if flushErr := bw.Flush(); err == nil && werr == nil && flushErr != nil {
		werr = flushErr
	}
	if err == nil && werr != nil {
		err = fmt.Errorf("cannot write the journal: %w", werr)
	}
	if err != nil {
		// An id that names nothing is a bad argument, like an unknown review
		// name for --show-prompt: usage error. A journal that exists but
		// cannot be read is a general failure.
		if errors.Is(err, journal.ErrNoJournal) {
			fmt.Fprintf(os.Stderr, "%v (see: gauntlet runs)\n", err)
			return exitUsage
		}
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	return exitOK
}
