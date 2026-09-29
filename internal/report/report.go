// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package report renders a run for a reader, on a plain stream rather than on
// a screen. It is the counterpart to internal/ui: both consume the same event
// bus and the same result types, one drawing a dashboard and one writing lines,
// and a run with no TTY is not a run with no report.
//
// It takes a run's contribution as values (a Dir per directory, a Result per
// review) rather than the composition root's own run state, so the layering
// stays one-way: cmd/gauntlet decides what ran, this decides how it reads.
package report

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/envx"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/rivo/uniseg"
)

// ANSI styling for the plain (non-TUI) output. Kept to a handful of codes:
// this is a log, and the dashboard is where color does real work.
type Palette struct{ On bool }

func (p Palette) wrap(code, s string) string {
	if !p.On {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p Palette) Bold(s string) string { return p.wrap("1", s) }

// think renders reasoning: dim and italic, so it stays legible but visibly
// subordinate to what the agent actually wrote.
func (p Palette) Think(s string) string  { return p.wrap("2;3", s) }
func (p Palette) Dim(s string) string    { return p.wrap("2", s) }
func (p Palette) Red(s string) string    { return p.wrap("31", s) }
func (p Palette) Green(s string) string  { return p.wrap("32", s) }
func (p Palette) Yellow(s string) string { return p.wrap("33", s) }
func (p Palette) Blue(s string) string   { return p.wrap("34", s) }

// Consumer-facing environment variables this package reads. One definition,
// so the help screen's environment section (helpEnvVars in help.go) cannot
// drift from what ColorEnabled actually reads.
const (
	EnvNoColor       = "NO_COLOR"
	EnvTerm          = "TERM"
	EnvCLIColorForce = "CLICOLOR_FORCE"
	EnvForceColor    = "FORCE_COLOR"
)

// termDumb is the TERM value that cannot render a Palette.
const termDumb = "dumb"

// ColorEnabled honors NO_COLOR (set at all, see no-color.org), TERM=dumb, and
// whether the stream is a terminal. CLICOLOR_FORCE / FORCE_COLOR turn color
// back on for a pipe, which is what `gauntlet … | less -R` needs.
//
// TERM is compared the way every other documented value is read, trimmed and
// case-folded. An exact match let TERM=DUMB, or a TERM carrying a trailing
// space from a wrapper that appends to it, keep a Palette a dumb terminal
// cannot show, while the variable two lines below answered the same question
// the documented way.
func ColorEnabled(f *os.File) bool {
	if _, set := os.LookupEnv(EnvNoColor); set {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvTerm)), termDumb) {
		return false
	}
	for _, name := range []string{EnvCLIColorForce, EnvForceColor} {
		if envx.On(os.Getenv(name)) {
			return true
		}
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Serialized makes one writer safe for several goroutines. The plain run
// writes to the same destination from the Reporter goroutine and from both
// signal handlers, and with --log that destination is an io.MultiWriter: its
// Write is a loop of independent writes, so an interleaved one leaves a signal
// line wedged into the middle of an agent's output line and a truncated line
// in the log file. The lock makes each caller's line one unit of work.
type Serialized struct {
	mu sync.Mutex
	W  io.Writer
}

func (s *Serialized) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.W.Write(p)
}

// LogWriters returns the --log destination and the console stream that tees to
// it. The file carries a lock of its own rather than borrowing the console
// stream's: while the dashboard owns the screen, the run-control messages, the
// file Reporter, and both signal handlers write to the file alone, and only the
// lines the console stream copies would otherwise be covered. Two writers on
// the same file without one lock between them can split a line, since a write
// the kernel accepts only in part is finished in a second call.
func LogWriters(console, f io.Writer) (log, out io.Writer) {
	log = &Serialized{W: f}
	return log, io.MultiWriter(console, log)
}

// ErrWriter remembers the first write error and ignores every write after it,
// so a command built line by line can report the failure once, at the end,
// instead of checking after each line.
type ErrWriter struct {
	Out io.Writer
	Err error
}

func (e *ErrWriter) Printf(format string, args ...any) {
	if e.Err == nil {
		_, e.Err = fmt.Fprintf(e.Out, format, args...)
	}
}

func (e *ErrWriter) Println(args ...any) {
	if e.Err == nil {
		_, e.Err = fmt.Fprintln(e.Out, args...)
	}
}

// Reporter turns the event stream into terminal output. It is the non-TUI
// consumer, and the only one that writes to stdout.
type Reporter struct {
	Out      io.Writer
	Pal      Palette
	MultiDir bool
	Quiet    bool
	// now stamps a line whose event carried no timestamp of its own. Nil
	// means time.Now; the run's own clock is passed in so a line written
	// outside the event stream reads the same clock the events do.
	Now func() time.Time
}

func (r *Reporter) Logf(at time.Time, format string, args ...any) {
	if at.IsZero() {
		at = r.clock()()
	}
	fmt.Fprintf(r.Out, "[%s] %s\n", humanize.Clock(at),
		normalize.Sanitize(fmt.Sprintf(format, args...)))
}

// clock is the Reporter's clock: the injected one, or wall time.
func (r *Reporter) clock() func() time.Time {
	if r.Now != nil {
		return r.Now
	}
	return time.Now
}

// Consume drains the bus until it closes.
func (r *Reporter) Consume(events <-chan runner.Event) {
	for ev := range events {
		r.handle(ev)
	}
}

func (r *Reporter) handle(ev runner.Event) {
	tag := ""
	if r.MultiDir && ev.Dir != "" {
		tag = "[" + filepath.Base(ev.Dir) + "] "
	}
	switch ev.Kind {
	case runner.EvRunStart:
		// The effective seed, not the configured one: --seed 0 derives it
		// from the clock, so this is the only place a headless run states
		// the seed that replays it.
		r.Logf(ev.Time, "%sseed %d, rerun with --seed %d", tag, ev.Seed, ev.Seed)
	case runner.EvLog:
		r.Logf(ev.Time, "%s%s", tag, ev.Text)
	case runner.EvOutput:
		if r.Quiet {
			return
		}
		line := r.paint(ev.LineKind, ev.Text)
		if ev.Repeat > 1 {
			line = fmt.Sprintf("%s %s", line, r.Pal.Dim(fmt.Sprintf("(x%d)", ev.Repeat)))
		}
		prefix := r.Pal.Dim(fmt.Sprintf("%s%s │ ", tag, ev.Review))
		fmt.Fprintf(r.Out, "%s%s\n", prefix, line)
	case runner.EvMerge:
		if ev.Status == runner.StatusConflict {
			r.Logf(ev.Time, "%sMERGE CONFLICT: %s kept on %s", tag, ev.Review, ev.Branch)
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
		fmt.Fprintln(r.Out)
		r.Logf(ev.Time, "%s=== Loop %d complete in %s%s ===", tag, ev.Loop,
			humanize.Duration(loopElapsed), lines)
		fmt.Fprintln(r.Out)
	}
}

// paint colors one agent output line by what it is. Diffs are the case that
// matters: a pasted patch is unreadable without the signs standing out.
func (r *Reporter) paint(k normalize.Kind, text string) string {
	switch k {
	case normalize.DiffAdd:
		return r.Pal.Green(text)
	case normalize.DiffDel:
		return r.Pal.Red(text)
	case normalize.DiffMeta:
		return r.Pal.Bold(text)
	case normalize.Thinking:
		return r.Pal.Think(text)
	case normalize.Error:
		// Errors are red everywhere else this program renders them (the
		// dashboard's feed, doctor's failures, the Summary's tally); yellow
		// here would read as a warning, one degree softer than what it is.
		//
		// The ! is the dashboard's feedMark, and it is here for the same
		// reason. Every other line kind names itself in its own text: a diff
		// carries the sign it was added or removed with, a result line begins
		// RESULT: or PATH:, reasoning is italic, progress says what it is
		// doing. An error is the agent's own sentence about something that
		// broke, so under --no-color, on a monochrome terminal, or to a reader
		// who cannot separate the hues, the one line kind a narrowed feed
		// exists to surface read as ordinary narration (SC 1.4.1). This is the
		// path a screen reader and a monochrome terminal actually read, so the
		// mark cannot live only on the dashboard.
		return r.Pal.Red("!" + text)
	default:
		return text
	}
}

// Totals is what every directory's stats add up to. Collecting it is
// separate from printing it: the block below reads one struct, so a new figure
// is one field here rather than another loop over results.
type Totals struct {
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
	// a Summary that quietly printed a short list would read as the whole run.
	dropped int
}

// Dir is one directory's contribution to a run's totals. The composition root
// builds these from its own run state; the summary reads a shape, not the
// CLI's per-directory record, so it needs nothing from a run but what it
// prints. A nil Stats is a directory that contributed no result, and is
// counted as nothing rather than as a zero.
type Dir struct {
	Stats *runner.Stats
	Loops int
}

func collectTotals(dirs []Dir) Totals {
	var t Totals
	merged := map[string]runner.AgentSummary{}
	for _, d := range dirs {
		if d.Stats == nil {
			continue
		}
		t.counts.Add(d.Stats.Counts())
		i, dl, tok, at, tm, hl := d.Stats.Totals()
		t.ins, t.del, t.tokens = t.ins+i, t.del+dl, t.tokens+tok
		t.agentTime += at
		t.timed += tm
		t.haveLines = t.haveLines || hl
		t.loops += d.Loops
		t.commitRuns += d.Stats.CommitRuns()
		t.commitFails += d.Stats.CommitFails()
		t.dropped += d.Stats.DetailDropped()
		t.thinking += d.Stats.Thinking()
		for _, r := range d.Stats.Results() {
			if r.URL != "" {
				t.pullRequest = append(t.pullRequest, r)
			}
		}
		for _, a := range d.Stats.ByAgent() {
			cur := merged[a.Label]
			cur.Label = a.Label
			cur.Counts.Add(a.Counts)
			cur.Tokens += a.Tokens
			cur.Elapsed += a.Elapsed
			merged[a.Label] = cur
		}
		t.failures = append(t.failures, d.Stats.Failures()...)
	}
	t.byAgent = make([]runner.AgentSummary, 0, len(merged))
	for _, a := range merged {
		t.byAgent = append(t.byAgent, a)
	}
	// A fixed order, not a map range: the same run must summarize the same way
	// every time, whichever order the directories finished in. The failure
	// list is sorted stably for the reason Stats.Failures is: a review that
	// failed in two loops is two rows, and the loops have to stay in run
	// order. Both keys are ordered by the same collation the lists they merged
	// were, so the totals are printed in the order the per-directory rows were.
	byName := fuzzy.Comparator()
	slices.SortFunc(t.byAgent, func(a, b runner.AgentSummary) int {
		return byName(a.Label, b.Label)
	})
	slices.SortStableFunc(t.failures, func(a, b runner.Result) int { return byName(a.Review, b.Review) })
	return t
}

// Summary prints the end-of-run statistics block.
func Summary(out io.Writer, pal Palette, dirs []Dir, wall time.Duration) {
	t := collectTotals(dirs)
	fmt.Fprintln(out)
	fmt.Fprintln(out, pal.Bold("=== Review loop stopped ==="))

	if len(dirs) > 1 {
		fmt.Fprintf(out, "%s %d\n", pal.Blue("Directories:"), len(dirs))
	}
	fmt.Fprintf(out, "%s %d\n", pal.Blue("Completed loops:"), t.loops)
	if len(t.pullRequest) > 0 {
		// One field per line: branch names and URLs are reference detail, and
		// cramming them onto one row makes none of the three readable.
		fmt.Fprintln(out, pal.Bold("Pull requests"))
		for _, pr := range t.pullRequest {
			fmt.Fprintf(out, "  %s\n", pr.Review)
			fmt.Fprintf(out, "    branch  %s\n", pr.Branch)
			fmt.Fprintf(out, "    base    %s\n", pr.Base)
			fmt.Fprintf(out, "    url     %s\n", pr.URL)
		}
	}
	fmt.Fprintf(out, "%s %d\n", pal.Blue("Total reviews run:"), t.counts.Total())
	fmt.Fprintf(out, "  Passed: %s\n", pal.Green(fmt.Sprint(t.counts.OK)))
	fmt.Fprintf(out, "  Failed: %s\n", pal.Red(fmt.Sprint(t.counts.Fail+t.counts.Timeout)))
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
		fmt.Fprintf(out, "  %s\n", pal.Dim(fmt.Sprintf(
			"note: the per-review detail list holds the most recent %d results; %d earlier ones are counted above but not listed",
			runner.MaxDetailResults, t.dropped)))
	}
	fmt.Fprintf(out, "%s %s\n", pal.Blue("Total time:"), humanize.Duration(wall))
	if t.timed > 0 {
		fmt.Fprintf(out, "%s %s across %d reviews (avg %s)\n", pal.Blue("Agent time:"),
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
		fmt.Fprintf(out, "%s %s reported%s%s\n", pal.Blue("Tokens:"),
			humanize.Count(t.tokens), rate, note)
	}
	if t.haveLines {
		fmt.Fprintf(out, "%s +%d -%d\n", pal.Blue("Lines changed:"), t.ins, t.del)
	}
	if t.commitRuns > 0 {
		note := ""
		if t.commitFails > 0 {
			note = fmt.Sprintf(", %d failed (changes may be uncommitted)", t.commitFails)
		}
		fmt.Fprintf(out, "%s %d%s\n", pal.Blue("Commit steps:"), t.commitRuns, note)
	}
	if len(t.byAgent) > 1 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, pal.Bold("Per-agent stats"))
		for _, a := range t.byAgent {
			rate := ""
			if tps := a.TokensPerSec(); tps > 0 {
				rate = fmt.Sprintf(", ~%.0f tok/s", tps)
			}
			fmt.Fprintf(out, "  %s ok=%d fail=%d timeout=%d%s\n",
				PadCells(a.Label, 20), a.Counts.OK, a.Counts.Fail, a.Counts.Timeout, rate)
		}
	}
	if len(t.failures) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, pal.Bold("Failed reviews"))
		for _, f := range t.failures {
			fmt.Fprintf(out, "  - %s (%s): %s\n", f.Review, f.Agent.Label(), FailureDetail(f))
		}
	}
}

// FailureDetail is the one-line reason a review did not pass: what the agent
// said when it said something, and the outcome's own vocabulary when it did
// not. Every status that counts as a failure has a line here, so none prints
// as an empty parenthesis.
func FailureDetail(f runner.Result) string {
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

// ListReviews prints the available reviews, which are scheduled, and the sets.
func ListReviews(out io.Writer, pal Palette, set prompt.Set, scheduled []string, width int) error {
	weight := map[string]int{}
	for _, r := range scheduled {
		weight[r]++
	}
	w := ErrWriter{Out: out}
	w.Printf("Available reviews (%d):\n", set.Len())
	// The marks below are only as readable as their legend: spell them out
	// once, right where they first appear. WrapIndent indents only its
	// continuation lines, so the first line carries its own.
	w.Println(pal.Dim(strings.Repeat(" ", 4) + WrapIndent(
		"✓ scheduled   ○ available, not selected   xN selected with repeated weight   "+
			"[project] discovered in the reviewed tree", width, 4)))
	nameCol := 0
	markCol := 2
	for _, n := range set.Names {
		nameCol = max(nameCol, Cells(n))
		// "x10" is three columns where every other mark is two, and PadCells
		// only pads, so a mark wider than its column would push the rest of
		// that row out of line with the rows around it.
		markCol = max(markCol, Cells(ReviewMark(weight[n])))
	}
	nameCol++
	for _, name := range set.Names {
		rev, _ := set.Get(name)
		mark := ReviewMark(weight[name])
		origin := ""
		if rev.IsProject() {
			origin = "[project]"
		}
		prefix := "  " + PadCells(mark, markCol) + " " + PadCells(name, nameCol) + PadCells(origin, 10) + " "
		desc := rev.Summary()
		if desc == "" {
			desc = "(no description)"
		}
		room := max(width-Cells(prefix), 20)
		w.Println(prefix + pal.Dim(TrimCells(desc, room)))
	}

	w.Println()
	names := prompt.SetNames()
	w.Printf("Sets usable with --reviews/--exclude (%d):\n", len(names))
	setCol := 0
	for _, n := range names {
		setCol = max(setCol, Cells(n))
	}
	setCol++
	for _, name := range names {
		if desc, dynamic := prompt.DynamicSets[name]; dynamic {
			count := set.Len()
			if name == "project" {
				count = len(set.ProjectNames())
			}
			w.Printf("  %s %s (%d)\n", PadCells(name, setCol), desc, count)
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
		w.Printf("  %s %s\n", PadCells(name, setCol), WrapIndent(body, width, setCol+3))
	}
	return w.Err
}

// ReviewMark is the leading glyph for a review in the listing: how often it
// was scheduled, with the repeat weight spelled out.
func ReviewMark(weight int) string {
	switch {
	case weight > 1:
		return fmt.Sprintf("x%d", weight)
	case weight == 1:
		return "✓"
	default:
		return "○"
	}
}

// Cells is how many terminal columns s occupies.
//
// Neither bytes nor runes answer that. A CJK glyph is two columns wide and a
// combining mark is none, so a column budget counted either way lets a review
// name from the reviewed tree push everything after it out of line. The
// dashboard already measures this way; these listings are the same text on
// the same terminal, and were the last place still counting something else.
func Cells(s string) int { return uniseg.StringWidth(s) }

// PadCells right-pads s to w terminal columns. fmt's %-*s pads to a rune
// count, which is the same number only for text that happens to be narrow.
func PadCells(s string, w int) string {
	if gap := w - Cells(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// PadCellsLeft left-pads s to w terminal columns, the %*s side. fmt's
// right-aligning verb has the same rune-count limit PadCells exists to avoid.
func PadCellsLeft(s string, w int) string {
	if gap := w - Cells(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// ReviewNameColumn is the width the per-review listings give a review name,
// one column wider than the widest of them. Measured in Cells for the reason
// Cells exists: every one of these names can come from a reviewed repository
// and be written in any script, and a column budget counted in bytes or runes
// is short by exactly the double-width glyphs in the longest name, which puts
// the whole rest of the line one column-group left of the header.
func ReviewNameColumn(names []string) int {
	w := 0
	for _, n := range names {
		w = max(w, Cells(n))
	}
	return w + 1
}

// TrimCells cuts s to at most w terminal columns, ellipsis included, between
// grapheme clusters so a cut never lands inside one.
func TrimCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Cells(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for len(s) > 0 {
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(s, -1)
		cw := Cells(cluster)
		if used+cw > w-1 { // one column is reserved for the ellipsis
			break
		}
		used += cw
		b.WriteString(cluster)
		s = rest
	}
	return strings.TrimRight(b.String(), " ") + "…"
}

// WrapIndent wraps a comma-separated list under a hanging indent. Columns
// count terminal Cells, the same measurement Cells gives every other column
// here, so a name from the reviewed tree that is not narrow does not push the
// rest of its line out of place.
func WrapIndent(s string, width, indent int) string {
	if width-indent < 20 {
		return s
	}
	var b strings.Builder
	col := indent
	for i, word := range strings.Split(s, " ") {
		n := Cells(word)
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
