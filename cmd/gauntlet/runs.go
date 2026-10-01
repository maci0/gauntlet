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
	"github.com/maci0/gauntlet/internal/report"
)

// cmdRuns lists recent runs from ~/.gauntlet/index.jsonl. A run id restores a
// pruned run, which is a different job from listing and happens instead of it.
func cmdRuns(out io.Writer, pal report.Palette, limit int, restore string, asJSON bool) (code int) {
	if restore != "" {
		return restoreRun(out, pal, restore, asJSON)
	}
	entries, listErr := journal.Recent(limit)
	if listErr != nil {
		fmt.Fprintf(os.Stderr, "run listing is incomplete: %v\n", listErr)
	}
	// One read of the state tree for both forms. It is what carries a journal
	// cut mid-line, and a listing that cannot tell a whole archive from a
	// short one is the answer a restore cannot be checked against. The JSON
	// form has always counted it; the table reads the same number here so the
	// human and machine views cannot disagree about whether the history is
	// complete.
	st, stErr := journal.Inspect()
	if stErr != nil {
		fmt.Fprintf(os.Stderr, "cannot read the run history: %v\n", stErr)
		return exitFail
	}
	if asJSON {
		if code := writeRunsJSON(out, entries, st); code != 0 {
			return code
		}
		return exitCodeFor(errors.Join(listErr, truncatedError(st.Truncated)))
	}
	bw := bufio.NewWriter(out)
	w := report.ErrWriter{Out: bw}
	defer func() {
		if err := bw.Flush(); w.Err == nil && err != nil {
			w.Err = err
		}
		if w.Err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the run listing: %v\n", w.Err)
			code = exitFail
		}
	}()
	if len(entries) == 0 {
		w.Printf("No runs recorded yet under %s\n", journal.Home())
	} else {
		cols := newRunsColumns(entries)
		w.Println(cols.header())
		// The column's name overstates what it counts; say so once, right where
		// it first appears, or a run that only skipped reviews reads as broken.
		// It has to name every bucket, including the one that catches a terminal
		// status a newer journal wrote and this build cannot read.
		w.Println(pal.Dim("FAILED counts timeouts, skipped reviews, merge conflicts, and statuses this build does not recognize"))
		for i := range entries {
			w.Println(cols.row(i, pal))
		}
	}
	// Where the journals live is a fact about this machine, not a row of the
	// table, so it goes to stderr. The legend above stays on stdout: it heads
	// the table and a consumer skipping two lines knows where the rows start.
	// Without this the last line of `gauntlet runs | tail -1` was a path.
	fmt.Fprintf(os.Stderr, "\nJournals: %s\n", filepath.Join(journal.Home(), "runs"))
	// A prune is unattended, so the runs it moved out of the listing are
	// named where the user reads the listing: a dropped run is recoverable
	// only by someone who knows it is still on disk. The bound keeps a
	// large quarantine from printing two hundred ids. An empty listing says
	// it too, since an index that has been pruned to nothing is exactly the
	// case where the quarantine is the only place the history is.
	held, err := journal.Quarantined()
	if err != nil {
		// The JSON form reports this, so the table does too: a pruned list that
		// could not be read would otherwise read as a claim that nothing is
		// recoverable.
		fmt.Fprintf(os.Stderr, "cannot read the pruned run list: %v\n", err)
		return exitFail
	}
	if len(held) > 0 {
		shown := held
		more := ""
		if len(shown) > listedQuarantined {
			more = fmt.Sprintf(" and %d more under %s",
				len(shown)-listedQuarantined, filepath.Join(journal.Home(), "pruned"))
			shown = shown[:listedQuarantined]
		}
		w.Printf("%s\n", pal.Dim(fmt.Sprintf(
			"Pruned, still recoverable (gauntlet runs --restore ID): %s%s",
			strings.Join(shown, " "), more)))
	}
	// A journal cut mid-line is a silent loss the table cannot show: the run
	// still lists, and its counts look whole, because the half-line every
	// reader drops is the only evidence its tail is gone. The listing is where
	// an operator checks a restored tree against what it remembers, so the
	// shortfall is named there and reflected in the exit code beside the rows
	// that did read.
	if st.Truncated > 0 {
		w.Printf("%s\n", pal.Yellow(fmt.Sprintf(
			"%s end mid-line: the last events are missing, so those runs list as shorter than they were",
			humanize.Plural(st.Truncated, "journal ends", "journals end"))))
	}
	return exitCodeFor(errors.Join(listErr, truncatedError(st.Truncated)))
}

// truncatedError reports a mid-line journal as a listing shortfall, so the exit
// code says the answer is incomplete the same way it does for a journal that
// could not be read. Zero cut journals is not a shortfall.
func truncatedError(n int) error {
	if n <= 0 {
		return nil
	}
	return fmt.Errorf("%s end mid-line: the last events are missing",
		humanize.Plural(n, "journal ends", "journals end"))
}

// exitCodeFor turns a listing that came back short into a failing exit. The
// rows that did read are still printed: a listing that names what it missed
// beats no listing, and a script reading the exit code still learns the answer
// is not the whole answer.
func exitCodeFor(err error) int {
	if err != nil {
		return exitFail
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
// journal adds reaches the consumer without a flag change. The two state paths
// are the exception to "as journaled": they are not run data, and they carry
// the home directory shortened to "~" (see writeRunsJSON).
type runsJSON struct {
	Home     string            `json:"home"`
	Journals string            `json:"journals"`
	Runs     []journal.Summary `json:"runs"`
	Pruned   []string          `json:"pruned"`
	History  historyJSON       `json:"history"`
}

// historyJSON is what the state tree holds behind those rows: the journals the
// index is derived from, the rows the index could answer with, the runs the two
// copies tell apart, how many pruned journals are still restorable, and how
// many journals were cut short mid-line. It is
// what `gauntlet doctor` prints on its Run history line, carried here because
// a restore is checked against the tree rather than against the exit code of the
// run that wrote it, and doctor's exit code is about the agent inventory.
type historyJSON struct {
	Journals  int `json:"journals"`
	Rows      int `json:"rows"`
	Disagreed int `json:"disagreed"`
	Pruned    int `json:"pruned"`
	Truncated int `json:"truncated"`
}

// writeRunsJSON prints the index for a consumer, and prints nothing else: the
// paths a human is told about on stderr are in the object, so a redirected
// stdout holds one document and a pipe is never split by a note. An empty
// listing is an empty array, never a null and never a message on the stream a
// caller is parsing.
//
// st is the state-tree read cmdRuns already made: one read serves both forms,
// so the counts in this document and the warning the table prints are read off
// the same tree at the same instant rather than two walks that can disagree.
func writeRunsJSON(out io.Writer, entries []journal.Summary, st journal.Status) int {
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
	// The two state paths name where this install keeps its tree, which is a
	// fact about the machine and not about any run, so nothing matches on them
	// and no consumer needs the account to use them. The home directory is
	// shortened for the same reason the journal's free text is: this document is
	// the one an archive job or a script carries off the machine, and a resolved
	// path under /home/<account> names the operator in every copy. "~" is the
	// spelling the operator recognizes and can expand. A row's own paths are
	// shortened the same way (see redactSummaryPaths): the index keeps them
	// resolved because the listing and the history matcher resolve against
	// them, and nothing here does.
	doc := runsJSON{
		Home:     normalize.RedactHome(journal.Home()),
		Journals: normalize.RedactHome(filepath.Join(journal.Home(), "runs")),
		Runs:     redactSummaryPaths(entries),
		Pruned:   pruned,
		History: historyJSON{
			Journals:  st.Journals,
			Rows:      st.Rows,
			Disagreed: st.Disagreed,
			Pruned:    st.Pruned,
			Truncated: st.Truncated,
		},
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintf(os.Stderr, "cannot write the run listing: %v\n", err)
		return exitFail
	}
	return exitOK
}

// redactSummaryPaths returns entries with the home directory shortened in the
// two path fields a row carries: where its journal sits, and the directories
// the run reviewed. Both are resolved paths under the operator's account in
// every case where the tree lives in their home, and this document is the copy
// that leaves the machine, so a resolved one puts the account name in every
// archive and every dashboard payload that keeps it.
//
// The index rows themselves stay resolved, because the listing and the history
// matcher resolve a path the person typed against them. Only the rendered copy
// is shortened, and only where there is an account to take out: a
// GAUNTLET_HOME outside the home directory, or a reviewed tree on another
// mount, has no account in it and comes out whole.
func redactSummaryPaths(entries []journal.Summary) []journal.Summary {
	out := make([]journal.Summary, len(entries))
	for i, e := range entries {
		e.Path = normalize.RedactHome(e.Path)
		if e.Dirs != nil {
			dirs := make([]string, len(e.Dirs))
			for j, d := range e.Dirs {
				dirs[j] = normalize.RedactHome(d)
			}
			e.Dirs = dirs
		}
		out[i] = e
	}
	return out
}

// restoreRun puts a pruned journal back in the listing and says so plainly:
// a restore that failed has to read as a failure, not as a listing.
func restoreRun(out io.Writer, pal report.Palette, runID string, asJSON bool) int {
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
		pal.Dim("gauntlet show "+runID))
	return exitOK
}

// runsColumn is one column of the listing. A number is right-aligned and a
// name is not, and every column is as wide as its widest cell rather than a
// width picked when the flag was written: a run id is a 16-character stamp, a
// dash, and the pid in unpadded hex, so it is 18 characters on a machine that
// has not reused pid 1 yet and grows with the pid from there, and a fixed width
// that lined up the header on the first shoved every column after RUN sideways
// on the second.
type runsColumn struct {
	head  string
	right bool
	cells []string
	// w is what the column prints at, measured once from the header and the
	// cells by newRunsColumns. Measuring per row made the listing quadratic in
	// --limit: every cell of every column was walked and run through a
	// grapheme count once for the header and again for each of the rows.
	w int
}

// measure records what the column prints at: the widest of its header and its
// cells. The RUN and DIRS cells hold names from the reviewed tree, so the
// measure is terminal cells (cells), not the byte or rune count the same table
// used to take. Every other column here is ASCII, so the two differ only on
// the two that are not.
func (c *runsColumn) measure() {
	c.w = report.Cells(c.head)
	for _, cell := range c.cells {
		c.w = max(c.w, report.Cells(cell))
	}
}

// width is what the column prints at, as measure recorded it.
func (c runsColumn) width() int { return c.w }

// field pads one cell to the column. The FAILED column is colored, so it pads
// the number before the escape codes go on: fmt counts a color escape as
// width, and a padded colored cell shoves every later column off its header.
func (c runsColumn) field(row int, width int, pal report.Palette) string {
	cell := c.cells[row]
	if c.right {
		cell = report.PadCellsLeft(cell, width)
	} else {
		cell = report.PadCells(cell, width)
	}
	if c.head == "FAILED" && strings.TrimSpace(cell) != "0" {
		return pal.Red(cell)
	}
	return cell
}

func (c runsColumn) headField(width int) string {
	if c.right {
		return report.PadCellsLeft(c.head, width)
	}
	return report.PadCells(c.head, width)
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
		// The reviewed tree picks these directory names, and the listing
		// writes them straight to a terminal: a name holding an escape
		// sequence or a control character would otherwise repaint, beep, or
		// hide part of the row. RUN needs no such care because ValidRunID
		// admits only a safe charset.
		dirs := make([]string, 0, len(e.Dirs))
		for _, d := range e.Dirs {
			dirs = append(dirs, normalize.Sanitize(filepath.Base(d)))
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
			started = startCell(e.Start, time.Local)
		}
		for i, cell := range []string{
			e.RunID, started, dur,
			strconv.Itoa(e.Loops), strconv.Itoa(e.OK), strconv.Itoa(bad),
			tokens, lines, strings.Join(dirs, ","),
		} {
			cols[i].cells = append(cols[i].cells, cell)
		}
	}
	for i := range cols {
		cols[i].measure()
	}
	return cols
}

// runsGap is the space between two columns: two, so the eye separates values
// it might otherwise read as one.
const runsGap = "  "

func (cs runsColumns) header() string {
	out := make([]string, 0, len(cs))
	for i := range cs {
		out = append(out, cs[i].headField(cs[i].w))
	}
	return trimRunsGap(out)
}

func (cs runsColumns) row(i int, pal report.Palette) string {
	out := make([]string, 0, len(cs))
	for j := range cs {
		out = append(out, cs[j].field(i, cs[j].w, pal))
	}
	return trimRunsGap(out)
}

// trimRunsGap drops the padding the last column carries, so a line is not
// padded out to the width of the column below it.
func trimRunsGap(cells []string) string {
	return strings.TrimRight(strings.Join(cells, runsGap), " ")
}

// startCell renders a run's start for the listing: local wall clock with the
// zone offset, the same fields humanize.Clock carries for a log line.
//
// The offset is not decoration here either. Local wall clock names one instant
// per day unambiguously, so on each fall-back the listing prints two runs an
// hour apart under the same STARTED: in Europe/Warsaw on 2026-10-25 a run at
// 00:30 UTC and one at 01:30 UTC are both 02:30:00 locally, and the listing
// could not say which ran first. A run start is an instant, and the id beside
// it names the zone the offset came from.
func startCell(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02 15:04:05-0700")
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
	events := 0
	err := journal.Events(runID, func(ev map[string]any) {
		if werr != nil {
			return
		}
		events++
		ts := ""
		if s, ok := ev["ts"].(string); ok {
			ts = showTime(s)
		}
		kind, _ := ev["ev"].(string)
		delete(ev, "ts")
		delete(ev, "ev")
		// The event carries the reviewed tree's absolute path because the run
		// matches its own locks and index rows against it. A replay is a copy
		// people paste into an issue or a chat, and a resolved path under
		// /home/<account> names the operator in every one of them, so the
		// rendered line says "~" where the field on disk stays resolved.
		if dir, ok := ev["dir"].(string); ok {
			ev["dir"] = normalize.RedactHome(dir)
		}
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
			// A pruned journal is not missing, and sending its reader to
			// gauntlet runs shows the id only under the quarantine note, with
			// the command that brings it back unsaid. Name it instead.
			if journal.Pruned(runID) {
				fmt.Fprintf(os.Stderr, "%v (restore it with: gauntlet runs --restore %s)\n", err, runID)
			} else {
				fmt.Fprintf(os.Stderr, "%v (see: gauntlet runs)\n", err)
			}
			return exitUsage
		}
		fmt.Fprintln(os.Stderr, err)
		return exitFail
	}
	// A run that started and recorded nothing has a journal, so this is not a
	// miss, and silence reads as a successful replay of an empty run. It says
	// so on stderr, leaving stdout as the replay and nothing else.
	if events == 0 {
		fmt.Fprintf(os.Stderr, "run %s recorded no events: it was interrupted before its first event was written\n", runID)
	}
	return exitOK
}
