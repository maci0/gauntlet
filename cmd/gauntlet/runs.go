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
	"strconv"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
)

// cmdRuns lists recent runs from ~/.gauntlet/index.jsonl. A run id restores a
// pruned run, which is a different job from listing and happens instead of it.
func cmdRuns(out io.Writer, pal palette, limit int, restore string, asJSON bool) (code int) {
	if restore != "" {
		return restoreRun(out, pal, restore, asJSON)
	}
	entries, err := journal.Recent(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read run index: %v\n", err)
		return exitFail
	}
	if asJSON {
		return writeRunsJSON(out, entries)
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
	cols := newRunsColumns(entries)
	w.println(cols.header())
	// The column's name overstates what it counts; say so once, right where
	// it first appears, or a run that only skipped reviews reads as broken.
	// It has to name every bucket, including the one that catches a terminal
	// status a newer journal wrote and this build cannot read.
	w.println(pal.dim("FAILED counts timeouts, skipped reviews, merge conflicts, and statuses this build does not recognize"))
	for i := range entries {
		w.println(cols.row(i, pal))
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

// runsJSON is what `gauntlet runs --json` prints. The table is for a person:
// fixed columns, a legend line above them, and a dim note whose width follows
// the terminal. A script wants the same facts under stable names and nothing
// else on the stream it parses, so the legend, the column layout, and the
// humanized durations stay behind, and every count is the number the run
// recorded. The runs are the index rows as journaled, so a field a future
// journal adds reaches the consumer without a flag change.
type runsJSON struct {
	Home     string            `json:"home"`
	Journals string            `json:"journals"`
	Runs     []journal.Summary `json:"runs"`
	Pruned   []string          `json:"pruned"`
}

// writeRunsJSON prints the index for a consumer, and prints nothing else: the
// paths a human is told about on stderr are in the object, so a redirected
// stdout holds one document and a pipe is never split by a note. An empty
// listing is an empty array, never a null and never a message on the stream a
// caller is parsing.
func writeRunsJSON(out io.Writer, entries []journal.Summary) int {
	// A failed read is not reported as an empty quarantine: a pruned list that
	// could not be read would read as a claim that nothing is recoverable.
	pruned, err := journal.Quarantined()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read the pruned run list: %v\n", err)
		return exitFail
	}
	if pruned == nil {
		pruned = []string{}
	}
	if entries == nil {
		entries = []journal.Summary{}
	}
	doc := runsJSON{
		Home:     journal.Home(),
		Journals: filepath.Join(journal.Home(), "runs"),
		Runs:     entries,
		Pruned:   pruned,
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write the run listing: %v\n", err)
		return exitFail
	}
	return exitOK
}

// restoreRun puts a pruned journal back in the listing and says so plainly:
// a restore that failed has to read as a failure, not as a listing.
func restoreRun(out io.Writer, pal palette, runID string, asJSON bool) int {
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
	if asJSON {
		// The same one fact a line said, under a name. The hint the human form
		// appends is already in every consumer's error path, and here it would
		// be a second string to parse.
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Restored string `json:"restored"`
		}{runID}); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the restore result: %v\n", err)
			return exitFail
		}
		return exitOK
	}
	fmt.Fprintf(out, "Restored %s; %s\n", runID,
		pal.dim("gauntlet show "+runID))
	return exitOK
}

// runsColumn is one column of the listing. A number is right-aligned and a
// name is not, and every column is as wide as its widest cell rather than a
// width picked when the flag was written: a run id is a stamp plus the pid in
// hex, so it is 17 characters on a machine that has not reused pid 1M yet and
// 24 on one that has, and a fixed width that lined up the header on the first
// shoved every column after RUN sideways on the second.
type runsColumn struct {
	head  string
	right bool
	cells []string
}

// width is what the column prints at: the widest of its header and its cells.
func (c runsColumn) width() int {
	w := len(c.head)
	for _, cell := range c.cells {
		if len(cell) > w {
			w = len(cell)
		}
	}
	return w
}

// field pads one cell to the column. The FAILED column is colored, so it pads
// the number before the escape codes go on: fmt counts a color escape as
// width, and a padded colored cell shoves every later column off its header.
func (c runsColumn) field(row int, width int, pal palette) string {
	cell := c.cells[row]
	if c.right {
		cell = fmt.Sprintf("%*s", width, cell)
	} else {
		cell = fmt.Sprintf("%-*s", width, cell)
	}
	if c.head == "FAILED" && strings.TrimSpace(cell) != "0" {
		return pal.red(cell)
	}
	return cell
}

func (c runsColumn) headField(width int) string {
	if c.right {
		return fmt.Sprintf("%*s", width, c.head)
	}
	return fmt.Sprintf("%-*s", width, c.head)
}

// runsColumns is the whole table, columns in print order.
type runsColumns []runsColumn

// newRunsColumns lays the runs out into cells, one slice per column.
func newRunsColumns(entries []journal.Summary) runsColumns {
	cols := runsColumns{
		{head: "RUN"},
		{head: "STARTED"},
		{head: "DURATION", right: true},
		{head: "LOOPS", right: true},
		{head: "OK", right: true},
		{head: "FAILED", right: true},
		{head: "TOKENS", right: true},
		{head: "LINES", right: true},
		{head: "DIRS"},
	}
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
		for i, cell := range []string{
			e.RunID, started, dur,
			strconv.Itoa(e.Loops), strconv.Itoa(e.OK), strconv.Itoa(bad),
			tokens, lines, strings.Join(dirs, ","),
		} {
			cols[i].cells = append(cols[i].cells, cell)
		}
	}
	return cols
}

// runsGap is the space between two columns: two, so the eye separates values
// it might otherwise read as one.
const runsGap = "  "

func (cs runsColumns) header() string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.headField(c.width()))
	}
	return trimRunsGap(out)
}

func (cs runsColumns) row(i int, pal palette) string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.field(i, c.width(), pal))
	}
	return trimRunsGap(out)
}

// trimRunsGap drops the padding the last column carries, so a line is not
// padded out to the width of the column below it.
func trimRunsGap(cells []string) string {
	return strings.TrimRight(strings.Join(cells, runsGap), " ")
}

// showTime renders a journal timestamp as local wall clock and its offset.
// UnmarshalText
// is the inverse of encoding/json's time.Time marshal, so a Z stamp, an
// offset, and a fractional second all round-trip; Parse(RFC3339Nano) is
// close but not that inverse, and a stamp the encoder wrote must replay.
func showTime(s string) string {
	var t time.Time
	if err := t.UnmarshalText([]byte(s)); err != nil || t.IsZero() {
		return ""
	}
	return humanize.Clock(t)
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
