// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
)

// ANSI styling for the plain (non-TUI) output. Kept to five codes: this is a
// log, and the dashboard is where color does real work.
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

// colorEnabled honors NO_COLOR (set at all, see no-color.org), TERM=dumb, and
// whether the stream is a terminal. CLICOLOR_FORCE / FORCE_COLOR turn color
// back on for a pipe, which is what `gauntlet … | less -R` needs.
func colorEnabled(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	for _, name := range []string{"CLICOLOR_FORCE", "FORCE_COLOR"} {
		if v := os.Getenv(name); v != "" && v != "0" {
			return true
		}
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// reporter turns the event stream into terminal output. It is the non-TUI
// consumer, and the only one that writes to stdout.
type reporter struct {
	out      io.Writer
	pal      palette
	multiDir bool
	quiet    bool
}

func (r *reporter) logf(format string, args ...any) {
	fmt.Fprintf(r.out, "[%s] %s\n", time.Now().Format("15:04:05"),
		normalize.Sanitize(fmt.Sprintf(format, args...)))
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
	case runner.EvLog:
		r.logf("%s%s", tag, ev.Text)
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
			r.logf("%sMERGE CONFLICT: %s kept on %s", tag, ev.Review, ev.Branch)
		}
	case runner.EvLoopEnd:
		lines := ""
		if ev.Ins != nil && ev.Del != nil {
			lines = fmt.Sprintf(", +%d/-%d lines", *ev.Ins, *ev.Del)
		}
		fmt.Fprintln(r.out)
		r.logf("%s=== Loop %d complete in %s%s ===", tag, ev.Loop,
			humanize.Duration(time.Duration(ev.Elapsed*float64(time.Second))), lines)
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
		return r.pal.yellow(text)
	default:
		return text
	}
}

// summary prints the end-of-run statistics block.
func summary(out io.Writer, pal palette, results []*dirRun, wall time.Duration) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, pal.bold("=== Review loop stopped ==="))

	var agg runner.Counts
	loops, commitRuns, commitFails := 0, 0, 0
	var ins, del, tokens, thinking int
	var agentTime time.Duration
	var timed int
	haveLines := false

	for _, d := range results {
		if d.stats == nil {
			continue
		}
		c := d.stats.Counts()
		agg.OK += c.OK
		agg.Fail += c.Fail
		agg.Timeout += c.Timeout
		agg.Skipped += c.Skipped
		agg.Interrupted += c.Interrupted
		agg.Conflict += c.Conflict
		i, dl, t, at, tm, hl := d.stats.Totals()
		ins, del, tokens = ins+i, del+dl, tokens+t
		for _, r := range d.stats.Results() {
			thinking += r.Thinking
		}
		agentTime += at
		timed += tm
		haveLines = haveLines || hl
		loops += d.loops
		commitRuns += d.stats.CommitRuns
		commitFails += d.stats.CommitFails
	}

	if len(results) > 1 {
		fmt.Fprintf(out, "Directories: %d\n", len(results))
	}
	fmt.Fprintf(out, "Completed loops: %d\n", loops)
	fmt.Fprintf(out, "Total reviews run: %d\n", agg.Total())
	fmt.Fprintf(out, "  Passed: %s\n", pal.green(fmt.Sprint(agg.OK)))
	fmt.Fprintf(out, "  Failed: %s\n", pal.red(fmt.Sprint(agg.Fail+agg.Timeout)))
	for label, n := range map[string]int{
		"  Skipped": agg.Skipped, "  Interrupted": agg.Interrupted,
		"  Merge conflicts": agg.Conflict,
	} {
		if n > 0 {
			fmt.Fprintf(out, "%s: %d\n", label, n)
		}
	}
	fmt.Fprintf(out, "Total time: %s\n", humanize.Duration(wall))
	if timed > 0 {
		fmt.Fprintf(out, "Agent time: %s across %d reviews (avg %s)\n",
			humanize.Duration(agentTime), timed, humanize.Duration(agentTime/time.Duration(timed)))
	}
	if tokens > 0 {
		rate := ""
		if agentTime >= time.Second {
			rate = fmt.Sprintf(", ~%.0f tok/s", float64(tokens)/agentTime.Seconds())
		}
		note := ""
		if thinking > 0 {
			// Only agents that disclose the split contribute here, so this is
			// a floor on reasoning, not a measurement of every agent.
			note = fmt.Sprintf(", %s reasoning (%d%%)", humanize.Count(thinking), 100*thinking/tokens)
		}
		fmt.Fprintf(out, "Tokens: %s reported%s%s\n", humanize.Count(tokens), rate, note)
	}
	if haveLines {
		fmt.Fprintf(out, "Lines changed: +%d -%d\n", ins, del)
	}
	if commitRuns > 0 {
		note := ""
		if commitFails > 0 {
			note = fmt.Sprintf(", %d failed (changes may be uncommitted)", commitFails)
		}
		fmt.Fprintf(out, "Commit steps: %d%s\n", commitRuns, note)
	}

	// Per-agent breakdown, merged across directories.
	byAgent := map[string]runner.AgentSummary{}
	for _, d := range results {
		if d.stats == nil {
			continue
		}
		for _, a := range d.stats.ByAgent() {
			cur := byAgent[a.Label]
			cur.Label = a.Label
			cur.Counts.OK += a.Counts.OK
			cur.Counts.Fail += a.Counts.Fail
			cur.Counts.Timeout += a.Counts.Timeout
			cur.Counts.Skipped += a.Counts.Skipped
			cur.Counts.Interrupted += a.Counts.Interrupted
			cur.Counts.Conflict += a.Counts.Conflict
			cur.Tokens += a.Tokens
			cur.Elapsed += a.Elapsed
			byAgent[a.Label] = cur
		}
	}
	if len(byAgent) > 1 {
		labels := make([]string, 0, len(byAgent))
		for l := range byAgent {
			labels = append(labels, l)
		}
		sort.Strings(labels)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Per-agent stats:")
		for _, l := range labels {
			a := byAgent[l]
			rate := ""
			if tps := a.TokensPerSec(); tps > 0 {
				rate = fmt.Sprintf(", ~%.0f tok/s", tps)
			}
			fmt.Fprintf(out, "  %-20s ok=%d fail=%d timeout=%d%s\n",
				l, a.Counts.OK, a.Counts.Fail, a.Counts.Timeout, rate)
		}
	}

	var failures []runner.Result
	for _, d := range results {
		if d.stats != nil {
			failures = append(failures, d.stats.Failures()...)
		}
	}
	if len(failures) > 0 {
		sort.Slice(failures, func(i, j int) bool { return failures[i].Review < failures[j].Review })
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Failed reviews:")
		for _, f := range failures {
			detail := ""
			switch f.Status {
			case runner.StatusTimeout:
				detail = "timeout"
			case runner.StatusConflict:
				detail = "merge conflict, kept on " + f.Branch
			case runner.StatusFail:
				if f.ExitCode >= 0 {
					detail = fmt.Sprintf("exit %d", f.ExitCode)
				} else {
					detail = "launch failed"
				}
			}
			fmt.Fprintf(out, "  - %s (%s): %s\n", f.Review, f.Agent.Label(), detail)
		}
	}
}

// listReviews prints the available reviews, which are scheduled, and the sets.
func listReviews(out io.Writer, pal palette, set prompt.Set, scheduled []string, width int) {
	weight := map[string]int{}
	for _, r := range scheduled {
		weight[r]++
	}
	fmt.Fprintf(out, "Available reviews (%d):\n", set.Len())
	nameCol := 0
	for _, n := range set.Names {
		nameCol = max(nameCol, len(n))
	}
	nameCol++
	for _, name := range set.Names {
		rev, _ := set.Get(name)
		mark := "○"
		switch w := weight[name]; {
		case w > 1:
			mark = fmt.Sprintf("x%d", w)
		case w == 1:
			mark = "✓"
		}
		origin := ""
		if rev.IsProject() {
			origin = "[project]"
		}
		prefix := fmt.Sprintf("  %-2s %-*s%-10s ", mark, nameCol, name, origin)
		desc := rev.Desc()
		if desc == "" {
			desc = "(no description)"
		}
		room := max(width-len(prefix), 20)
		if len(desc) > room {
			desc = truncateDesc(desc, room-1) + "…"
		}
		fmt.Fprintln(out, prefix+pal.dim(desc))
	}

	fmt.Fprintln(out)
	names := prompt.SetNames()
	fmt.Fprintf(out, "Sets usable with --reviews/--exclude (%d):\n", len(names))
	setCol := 0
	for _, n := range names {
		setCol = max(setCol, len(n))
	}
	setCol++
	for _, name := range names {
		if desc, dynamic := prompt.DynamicSets[name]; dynamic {
			count := set.Len()
			if name == "project" {
				count = len(set.ProjectNames())
			}
			fmt.Fprintf(out, "  %-*s %s (%d)\n", setCol, name, desc, count)
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
		fmt.Fprintf(out, "  %-*s %s\n", setCol, name, wrapIndent(body, width, setCol+3))
	}
}

// truncateDesc cuts s to at most max bytes without splitting a UTF-8
// sequence, so a multibyte description never ends in mojibake.
func truncateDesc(s string, max int) string {
	if max < 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return strings.TrimRight(s[:max], " ")
}

// wrapIndent wraps a comma-separated list under a hanging indent.
func wrapIndent(s string, width, indent int) string {
	if width-indent < 20 {
		return s
	}
	var b strings.Builder
	col := indent
	for i, word := range strings.Split(s, " ") {
		if i > 0 {
			if col+len(word)+1 > width {
				b.WriteString("\n" + strings.Repeat(" ", indent))
				col = indent
			} else {
				b.WriteString(" ")
				col++
			}
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String()
}
