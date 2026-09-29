// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/envx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/rivo/uniseg"
)

// ANSI styling for the plain (non-TUI) output. Kept to a handful of codes:
// this is a log, and the dashboard is where color does real work.
type palette struct{ on bool }

func (p palette) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) bold(s string) string { return p.wrap("1", s) }

// think renders reasoning: dim and italic, so it stays legible but visibly
// subordinate to what the agent actually wrote.
func (p palette) think(s string) string  { return p.wrap("2;3", s) }
func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }
func (p palette) green(s string) string  { return p.wrap("32", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }
func (p palette) blue(s string) string   { return p.wrap("34", s) }

// Consumer-facing environment variables this package reads. One definition,
// so the help screen's environment section (helpEnvVars in help.go) cannot
// drift from what colorEnabled actually reads.
const (
	envNoColor       = "NO_COLOR"
	envTerm          = "TERM"
	envCLIColorForce = "CLICOLOR_FORCE"
	envForceColor    = "FORCE_COLOR"
)

// termDumb is the TERM value that cannot render a palette.
const termDumb = "dumb"

// colorEnabled honors NO_COLOR (set at all, see no-color.org), TERM=dumb, and
// whether the stream is a terminal. CLICOLOR_FORCE / FORCE_COLOR turn color
// back on for a pipe, which is what `gauntlet … | less -R` needs.
//
// TERM is compared the way every other documented value is read, trimmed and
// case-folded. An exact match let TERM=DUMB, or a TERM carrying a trailing
// space from a wrapper that appends to it, keep a palette a dumb terminal
// cannot show, while the variable two lines below answered the same question
// the documented way.
func colorEnabled(f *os.File) bool {
	if _, set := os.LookupEnv(envNoColor); set {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(envTerm)), termDumb) {
		return false
	}
	for _, name := range []string{envCLIColorForce, envForceColor} {
		if envx.On(os.Getenv(name)) {
			return true
		}
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// serialized makes one writer safe for several goroutines. The plain run
// writes to the same destination from the reporter goroutine and from both
// signal handlers, and with --log that destination is an io.MultiWriter: its
// Write is a loop of independent writes, so an interleaved one leaves a signal
// line wedged into the middle of an agent's output line and a truncated line
// in the log file. The lock makes each caller's line one unit of work.
type serialized struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *serialized) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// logWriters returns the --log destination and the console stream that tees to
// it. The file carries a lock of its own rather than borrowing the console
// stream's: while the dashboard owns the screen, the run-control messages, the
// file reporter, and both signal handlers write to the file alone, and only the
// lines the console stream copies would otherwise be covered. Two writers on
// the same file without one lock between them can split a line, since a write
// the kernel accepts only in part is finished in a second call.
func logWriters(console, f io.Writer) (log, out io.Writer) {
	log = &serialized{w: f}
	return log, io.MultiWriter(console, log)
}

// errWriter remembers the first write error and ignores every write after it,
// so a command built line by line can report the failure once, at the end,
// instead of checking after each line.
type errWriter struct {
	out io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.out, format, args...)
	}
}

func (e *errWriter) println(args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintln(e.out, args...)
	}
}

// reporter turns the event stream into terminal output. It is the non-TUI
// consumer, and the only one that writes to stdout.
type reporter struct {
	out      io.Writer
	pal      palette
	multiDir bool
	quiet    bool
	// now stamps a line whose event carried no timestamp of its own. Nil
	// means time.Now; the run's own clock is passed in so a line written
	// outside the event stream reads the same clock the events do.
	now func() time.Time
}

func (r *reporter) logf(at time.Time, format string, args ...any) {
	if at.IsZero() {
		at = r.clock()()
	}
	fmt.Fprintf(r.out, "[%s] %s\n", humanize.Clock(at),
		normalize.Sanitize(fmt.Sprintf(format, args...)))
}

// clock is the reporter's clock: the injected one, or wall time.
func (r *reporter) clock() func() time.Time {
	if r.now != nil {
		return r.now
	}
	return time.Now
}

// Consume drains the bus until it closes.
func (r *reporter) Consume(events <-chan runner.Event) {
	for ev := range events {
		r.handle(ev)
	}
}

func (r *reporter) handle(ev runner.Event) {
	tag := ""
	if r.multiDir && ev.Dir != "" {
		tag = "[" + filepath.Base(ev.Dir) + "] "
	}
	switch ev.Kind {
	case runner.EvRunStart:
		// The effective seed, not the configured one: --seed 0 derives it
		// from the clock, so this is the only place a headless run states
		// the seed that replays it.
		r.logf(ev.Time, "%sseed %d, rerun with --seed %d", tag, ev.Seed, ev.Seed)
	case runner.EvLog:
		r.logf(ev.Time, "%s%s", tag, ev.Text)
	case runner.EvOutput:
		if r.quiet {
			return
		}
		line := r.paint(ev.LineKind, ev.Text)
		if ev.Repeat > 1 {
			line = fmt.Sprintf("%s %s", line, r.pal.dim(fmt.Sprintf("(x%d)", ev.Repeat)))
		}
		prefix := r.pal.dim(fmt.Sprintf("%s%s │ ", tag, ev.Review))
		fmt.Fprintf(r.out, "%s%s\n", prefix, line)
	case runner.EvMerge:
		if ev.Status == runner.StatusConflict {
			r.logf(ev.Time, "%sMERGE CONFLICT: %s kept on %s", tag, ev.Review, ev.Branch)
		}
	case runner.EvLoopEnd:
		lines := ""
		if ev.Ins != nil && ev.Del != nil {
			lines = fmt.Sprintf(", +%d/-%d lines", *ev.Ins, *ev.Del)
		}
		var loopElapsed time.Duration
		if d, ok := humanize.Seconds(ev.Elapsed); ok {
			loopElapsed = d
		}
		fmt.Fprintln(r.out)
		r.logf(ev.Time, "%s=== Loop %d complete in %s%s ===", tag, ev.Loop,
			humanize.Duration(loopElapsed), lines)
		fmt.Fprintln(r.out)
	}
}

// paint colors one agent output line by what it is. Diffs are the case that
// matters: a pasted patch is unreadable without the signs standing out.
func (r *reporter) paint(k normalize.Kind, text string) string {
	switch k {
	case normalize.DiffAdd:
		return r.pal.green(text)
	case normalize.DiffDel:
		return r.pal.red(text)
	case normalize.DiffMeta:
		return r.pal.bold(text)
	case normalize.Thinking:
		return r.pal.think(text)
	case normalize.Error:
		// Errors are red everywhere else this program renders them (the
		// dashboard's feed, doctor's failures, the summary's tally); yellow
		// here would read as a warning, one degree softer than what it is.
		return r.pal.red(text)
	default:
		return text
	}
}

// runTotals is what every directory's stats add up to. Collecting it is
// separate from printing it: the block below reads one struct, so a new figure
// is one field here rather than another loop over results.
type runTotals struct {
	counts      runner.Counts
	loops       int
	commitRuns  int
	commitFails int
	ins, del    int
	tokens      int
	thinking    int
	agentTime   time.Duration
	timed       int
	haveLines   bool
	pullRequest []runner.Result
	byAgent     []runner.AgentSummary
	failures    []runner.Result
	// dropped is how many results a directory's per-result detail no longer
	// holds, past the bound in runner.maxDetailResults. Every count above is
	// exact regardless; only the row lists below are short by this much, and
	// a summary that quietly printed a short list would read as the whole run.
	dropped int
}

func collectTotals(results []*dirRun) runTotals {
	var t runTotals
	merged := map[string]runner.AgentSummary{}
	for _, d := range results {
		if d.stats == nil {
			continue
		}
		t.counts.Add(d.stats.Counts())
		i, dl, tok, at, tm, hl := d.stats.Totals()
		t.ins, t.del, t.tokens = t.ins+i, t.del+dl, t.tokens+tok
		t.agentTime += at
		t.timed += tm
		t.haveLines = t.haveLines || hl
		t.loops += d.loops
		t.commitRuns += d.stats.CommitRuns()
		t.commitFails += d.stats.CommitFails()
		t.dropped += d.stats.DetailDropped()
		t.thinking += d.stats.Thinking()
		for _, r := range d.stats.Results() {
			if r.URL != "" {
				t.pullRequest = append(t.pullRequest, r)
			}
		}
		for _, a := range d.stats.ByAgent() {
			cur := merged[a.Label]
			cur.Label = a.Label
			cur.Counts.Add(a.Counts)
			cur.Tokens += a.Tokens
			cur.Elapsed += a.Elapsed
			merged[a.Label] = cur
		}
		t.failures = append(t.failures, d.stats.Failures()...)
	}
	t.byAgent = make([]runner.AgentSummary, 0, len(merged))
	for _, a := range merged {
		t.byAgent = append(t.byAgent, a)
	}
	// A fixed order, not a map range: the same run must summarize the same way
	// every time, whichever order the directories finished in. The failure
	// list is sorted stably for the reason Stats.Failures is: a review that
	// failed in two loops is two rows, and the loops have to stay in run
	// order.
	slices.SortFunc(t.byAgent, func(a, b runner.AgentSummary) int {
		return cmp.Compare(a.Label, b.Label)
	})
	slices.SortStableFunc(t.failures, func(a, b runner.Result) int { return cmp.Compare(a.Review, b.Review) })
	return t
}

// summary prints the end-of-run statistics block.
func summary(out io.Writer, pal palette, results []*dirRun, wall time.Duration) {
	t := collectTotals(results)
	fmt.Fprintln(out)
	fmt.Fprintln(out, pal.bold("=== Review loop stopped ==="))

	if len(results) > 1 {
		fmt.Fprintf(out, "%s %d\n", pal.blue("Directories:"), len(results))
	}
	fmt.Fprintf(out, "%s %d\n", pal.blue("Completed loops:"), t.loops)
	if len(t.pullRequest) > 0 {
		// One field per line: branch names and URLs are reference detail, and
		// cramming them onto one row makes none of the three readable.
		fmt.Fprintln(out, pal.bold("Pull requests"))
		for _, pr := range t.pullRequest {
			fmt.Fprintf(out, "  %s\n", pr.Review)
			fmt.Fprintf(out, "    branch  %s\n", pr.Branch)
			fmt.Fprintf(out, "    base    %s\n", pr.Base)
			fmt.Fprintf(out, "    url     %s\n", pr.URL)
		}
	}
	fmt.Fprintf(out, "%s %d\n", pal.blue("Total reviews run:"), t.counts.Total())
	fmt.Fprintf(out, "  Passed: %s\n", pal.green(fmt.Sprint(t.counts.OK)))
	fmt.Fprintf(out, "  Failed: %s\n", pal.red(fmt.Sprint(t.counts.Fail+t.counts.Timeout)))
	for _, row := range []struct {
		label string
		n     int
	}{
		{"  Skipped", t.counts.Skipped},
		{"  Interrupted", t.counts.Interrupted},
		{"  Merge conflicts", t.counts.Conflict},
		{"  Unrecognized outcomes", t.counts.Other},
	} {
		if row.n > 0 {
			fmt.Fprintf(out, "%s: %d\n", row.label, row.n)
		}
	}
	if t.dropped > 0 {
		// The counts above are exact; only the row lists (pull requests,
		// failures) are short. Say so rather than let a truncated list read as
		// the whole run.
		fmt.Fprintf(out, "  %s\n", pal.dim(fmt.Sprintf(
			"note: the per-review detail list holds the most recent %d results; %d earlier ones are counted above but not listed",
			runner.MaxDetailResults, t.dropped)))
	}
	fmt.Fprintf(out, "%s %s\n", pal.blue("Total time:"), humanize.Duration(wall))
	if t.timed > 0 {
		fmt.Fprintf(out, "%s %s across %d reviews (avg %s)\n", pal.blue("Agent time:"),
			humanize.Duration(t.agentTime), t.timed,
			humanize.Duration(t.agentTime/time.Duration(t.timed)))
	}
	if t.tokens > 0 {
		rate := ""
		if t.agentTime >= time.Second {
			rate = fmt.Sprintf(", ~%.0f tok/s", float64(t.tokens)/t.agentTime.Seconds())
		}
		note := ""
		if t.thinking > 0 {
			// Only agents that disclose the split contribute here, so this is
			// a floor on reasoning, not a measurement of every agent.
			pct := humanize.Share(t.thinking, t.tokens)
			note = fmt.Sprintf(", %s reasoning (%d%%)", humanize.Count(t.thinking), pct)
		}
		fmt.Fprintf(out, "%s %s reported%s%s\n", pal.blue("Tokens:"),
			humanize.Count(t.tokens), rate, note)
	}
	if t.haveLines {
		fmt.Fprintf(out, "%s +%d -%d\n", pal.blue("Lines changed:"), t.ins, t.del)
	}
	if t.commitRuns > 0 {
		note := ""
		if t.commitFails > 0 {
			note = fmt.Sprintf(", %d failed (changes may be uncommitted)", t.commitFails)
		}
		fmt.Fprintf(out, "%s %d%s\n", pal.blue("Commit steps:"), t.commitRuns, note)
	}
	if len(t.byAgent) > 1 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, pal.bold("Per-agent stats"))
		for _, a := range t.byAgent {
			rate := ""
			if tps := a.TokensPerSec(); tps > 0 {
				rate = fmt.Sprintf(", ~%.0f tok/s", tps)
			}
			fmt.Fprintf(out, "  %s ok=%d fail=%d timeout=%d%s\n",
				padCells(a.Label, 20), a.Counts.OK, a.Counts.Fail, a.Counts.Timeout, rate)
		}
	}
	if len(t.failures) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, pal.bold("Failed reviews"))
		for _, f := range t.failures {
			fmt.Fprintf(out, "  - %s (%s): %s\n", f.Review, f.Agent.Label(), failureDetail(f))
		}
	}
}

// failureDetail is the one-line reason a review did not pass: what the agent
// said when it said something, and the outcome's own vocabulary when it did
// not. Every status that counts as a failure has a line here, so none prints
// as an empty parenthesis.
func failureDetail(f runner.Result) string {
	switch f.Status {
	case runner.StatusTimeout:
		return "timeout"
	case runner.StatusConflict:
		return "merge conflict, kept on " + f.Branch
	case runner.StatusSkipped:
		if f.Detail == "" {
			return "skipped: never ran (unknown name or unreadable prompt)"
		}
		return "skipped: " + normalize.Sanitize(f.Detail)
	case runner.StatusFail:
		if f.Detail != "" {
			return normalize.Sanitize(f.Detail)
		}
		if f.ExitCode >= 0 {
			return fmt.Sprintf("exit %d", f.ExitCode)
		}
		return "launch failed"
	}
	return string(f.Status)
}

// listReviews prints the available reviews, which are scheduled, and the sets.
func listReviews(out io.Writer, pal palette, set prompt.Set, scheduled []string, width int) error {
	weight := map[string]int{}
	for _, r := range scheduled {
		weight[r]++
	}
	w := errWriter{out: out}
	w.printf("Available reviews (%d):\n", set.Len())
	// The marks below are only as readable as their legend: spell them out
	// once, right where they first appear. wrapIndent indents only its
	// continuation lines, so the first line carries its own.
	w.println(pal.dim(strings.Repeat(" ", 4) + wrapIndent(
		"✓ scheduled   ○ available, not selected   xN selected with repeated weight   "+
			"[project] discovered in the reviewed tree", width, 4)))
	nameCol := 0
	markCol := 2
	for _, n := range set.Names {
		nameCol = max(nameCol, cells(n))
		// "x10" is three columns where every other mark is two, and padCells
		// only pads, so a mark wider than its column would push the rest of
		// that row out of line with the rows around it.
		markCol = max(markCol, cells(reviewMark(weight[n])))
	}
	nameCol++
	for _, name := range set.Names {
		rev, _ := set.Get(name)
		mark := reviewMark(weight[name])
		origin := ""
		if rev.IsProject() {
			origin = "[project]"
		}
		prefix := "  " + padCells(mark, markCol) + " " + padCells(name, nameCol) + padCells(origin, 10) + " "
		desc := rev.Summary()
		if desc == "" {
			desc = "(no description)"
		}
		room := max(width-cells(prefix), 20)
		w.println(prefix + pal.dim(trimCells(desc, room)))
	}

	w.println()
	names := prompt.SetNames()
	w.printf("Sets usable with --reviews/--exclude (%d):\n", len(names))
	setCol := 0
	for _, n := range names {
		setCol = max(setCol, cells(n))
	}
	setCol++
	for _, name := range names {
		if desc, dynamic := prompt.DynamicSets[name]; dynamic {
			count := set.Len()
			if name == "project" {
				count = len(set.ProjectNames())
			}
			w.printf("  %s %s (%d)\n", padCells(name, setCol), desc, count)
			continue
		}
		var present []string
		for _, m := range prompt.Sets[name] {
			if _, ok := set.Get(m); ok {
				present = append(present, strings.TrimSuffix(m, "-review"))
			}
		}
		body := strings.Join(present, ", ")
		if body == "" {
			body = "(no members in this prompt dir)"
		}
		w.printf("  %s %s\n", padCells(name, setCol), wrapIndent(body, width, setCol+3))
	}
	return w.err
}

// reviewMark is the leading glyph for a review in the listing: how often it
// was scheduled, with the repeat weight spelled out.
func reviewMark(weight int) string {
	switch {
	case weight > 1:
		return fmt.Sprintf("x%d", weight)
	case weight == 1:
		return "✓"
	default:
		return "○"
	}
}

// cells is how many terminal columns s occupies.
//
// Neither bytes nor runes answer that. A CJK glyph is two columns wide and a
// combining mark is none, so a column budget counted either way lets a review
// name from the reviewed tree push everything after it out of line. The
// dashboard already measures this way; these listings are the same text on
// the same terminal, and were the last place still counting something else.
func cells(s string) int { return uniseg.StringWidth(s) }

// padCells right-pads s to w terminal columns. fmt's %-*s pads to a rune
// count, which is the same number only for text that happens to be narrow.
func padCells(s string, w int) string {
	if gap := w - cells(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// trimCells cuts s to at most w terminal columns, ellipsis included, between
// grapheme clusters so a cut never lands inside one.
func trimCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cells(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for len(s) > 0 {
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(s, -1)
		cw := cells(cluster)
		if used+cw > w-1 { // one column is reserved for the ellipsis
			break
		}
		used += cw
		b.WriteString(cluster)
		s = rest
	}
	return strings.TrimRight(b.String(), " ") + "…"
}

// wrapIndent wraps a comma-separated list under a hanging indent. Columns
// count terminal cells, the same measurement cells gives every other column
// here, so a name from the reviewed tree that is not narrow does not push the
// rest of its line out of place.
func wrapIndent(s string, width, indent int) string {
	if width-indent < 20 {
		return s
	}
	var b strings.Builder
	col := indent
	for i, word := range strings.Split(s, " ") {
		n := cells(word)
		if i > 0 {
			if col+n+1 > width {
				b.WriteString("\n" + strings.Repeat(" ", indent))
				col = indent
			} else {
				b.WriteString(" ")
				col++
			}
		}
		b.WriteString(word)
		col += n
	}
	return b.String()
}
