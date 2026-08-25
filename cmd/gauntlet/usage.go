// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The help screen. Go's flag.PrintDefaults lists every alias as its own entry
// and sorts them alphabetically, which turns 30 flags into 40 unordered lines.
// This renders them grouped, aliased, aligned, and wrapped instead. A test
// keeps this table and the registered flags from drifting apart.

import (
	"fmt"
	"io"
	"strings"
)

// flagDoc is one row of the help screen.
type flagDoc struct {
	Short string // without the dash, empty when there is none
	Long  string // without the dashes
	Arg   string // metavar, empty for booleans
	Help  string
}

// names lists every flag name this row documents, for the drift test.
func (f flagDoc) names() []string {
	out := []string{f.Long}
	if f.Short != "" {
		out = append(out, f.Short)
	}
	return out
}

// left renders the flag column, e.g. "-r, --reviews LIST".
func (f flagDoc) left() string {
	var b strings.Builder
	if f.Short != "" {
		b.WriteString("-" + f.Short + ", ")
	} else {
		b.WriteString("    ")
	}
	b.WriteString("--" + f.Long)
	if f.Arg != "" {
		b.WriteString(" " + f.Arg)
	}
	return b.String()
}

type flagGroup struct {
	Title string
	Flags []flagDoc
}

var helpGroups = []flagGroup{
	{"Reviews", []flagDoc{
		{"r", "reviews", "LIST", "reviews and/or sets to run; the -review suffix is optional, repeats add weight, 'suggest' lets an agent choose (repeatable)"},
		{"x", "exclude", "LIST", "reviews and/or sets to skip (repeatable)"},
		{"s", "suggest", "", "shorthand for --reviews suggest"},
		{"", "suggest-agent", "AGENT", "agent to run the triage step (default: sample from --agents)"},
		{"", "suggest-timeout", "DUR", "timeout for the triage step"},
		{"", "prompt-dir", "DIR", "use *-review.md files from DIR instead of the embedded set"},
	}},
	{"Agents", []flagDoc{
		{"a", "agents", "LIST", "agent CLIs, optionally agent:model; 'mixed' means every installed agent (repeatable, default: auto-detect)"},
		{"", "bin", "TOOL=PATH", "run an agent from a specific executable (repeatable)"},
		{"", "agent-cmd", "NAME=ARGV", "define an agent gauntlet does not know, e.g. pi='pi -p {prompt}' (repeatable)"},
		{"", "continue-sessions", "", "resume each agent's session between reviews"},
	}},
	{"Execution", []flagDoc{
		{"C", "dir", "DIR", "directory to review"},
		{"", "dirs", "LIST", "review several directories in parallel; globs are expanded (repeatable)"},
		{"", "target-dirs", "LIST", "alias of --dirs, the name the Python tool used"},
		{"j", "jobs", "N", "reviews at a time; above 1, each gets its own git worktree and is merged back"},
		{"t", "timeout", "DUR", "per-review timeout: 90s, 30m, 1h, 2d"},
		{"", "runtime", "DUR", "wall-clock budget for the whole run"},
		{"1", "once", "", "run a single loop and exit"},
		{"n", "max-loops", "N", "stop after N loops (0 means unlimited)"},
		{"c", "commit", "", "after each review, an agent commits the changes"},
		{"p", "push", "", "like --commit, and pushes"},
		{"", "yolo", "", "drop the caution rules: bigger, more ambitious changes"},
		{"y", "yes", "", "skip the suggest confirmation"},
		{"", "semcode", "", "build a semcode index before the loop"},
	}},
	{"Modes", []flagDoc{
		{"l", "list", "", "list available reviews and sets, then exit"},
		{"", "dry-run", "", "print the planned schedule, then exit"},
		{"", "show-prompt", "REVIEW", "print the exact prompt an agent would receive, then exit"},
		{"V", "version", "", "print the version and exit"},
		{"h", "help", "", "show this help and exit"},
	}},
	{"Output", []flagDoc{
		{"", "tui", "", "live dashboard: lanes, activity chart, review grid, feed"},
		{"q", "quiet", "", "discard agent output"},
		{"", "raw", "", "echo agent output verbatim instead of normalizing it"},
		{"", "stream", "", "machine-readable agent output where supported: live token counts and reasoning (default true; --stream=false to disable)"},
		{"", "log", "FILE", "also write all output to FILE"},
		{"", "no-color", "", "disable color"},
	}},
	{"Updates", []flagDoc{
		{"", "hot-reload", "", "reload when this binary is replaced (default true)"},
		{"", "auto-update", "", "install new releases during the run"},
		{"", "update-repo", "REPO", "GitHub repo to fetch releases from"},
		{"", "check", "", "update: report the latest release without installing"},
	}},
	{"History", []flagDoc{
		{"", "limit", "N", "runs: how many entries to list"},
	}},
}

var helpCommands = []struct{ Cmd, Help string }{
	{"gauntlet [flags]", "review the current directory, looping until stopped"},
	{"gauntlet doctor", "report which agent CLIs and helper tools are installed"},
	{"gauntlet update [--check]", "replace this binary with the latest verified release"},
	{"gauntlet runs [--limit N]", "list recent runs recorded under ~/.gauntlet"},
	{"gauntlet show <run-id>", "replay one run's journal"},
	{"gauntlet version", "print the version and exit"},
	{"gauntlet help", "show this help and exit"},
}

var helpExamples = []struct{ Cmd, Help string }{
	{"gauntlet -a claude --once", "one pass over every review, then stop"},
	{"gauntlet -r quick -x test-review", "a named set, minus one review"},
	{"gauntlet -j 4 -a mixed", "four at a time, worktree-isolated and merged"},
	{"gauntlet --dirs ~/src/*", "every repo under ~/src, in parallel"},
	{"gauntlet --suggest --yes --tui", "agent-picked reviews, live dashboard"},
	{"gauntlet --agent-cmd pi='pi -p {prompt}' -a pi", "run an agent gauntlet does not ship"},
}

var helpExitCodes = []struct{ Code, Meaning string }{
	{"0", "every review ran and passed"},
	{"1", "a review failed, timed out, was skipped, or would not merge"},
	{"2", "usage error"},
	{"75", "another instance holds the lock for that directory"},
	{"130", "interrupted"},
}

// printUsage renders the help screen.
func printUsage(out io.Writer, pal palette, width int) {
	if width < 60 {
		width = 60
	}
	head := func(s string) { fmt.Fprintf(out, "\n%s\n", pal.bold(strings.ToUpper(s))) }

	fmt.Fprintf(out, "%s %s\n%s\n",
		pal.bold("gauntlet"), pal.dim(version),
		pal.dim("Run your codebase through ~50 specialized review prompts, dispatched to"))
	fmt.Fprintln(out, pal.dim("the AI coding agents you have installed. Fixes land in the working tree."))

	head("usage")
	col := 0
	for _, c := range helpCommands {
		col = max(col, len(c.Cmd))
	}
	for _, c := range helpCommands {
		fmt.Fprintf(out, "  %-*s  %s\n", col, c.Cmd, pal.dim(c.Help))
	}

	// One column width across every group, so the help text lines up down the
	// whole screen rather than jumping per section.
	flagCol := 0
	for _, g := range helpGroups {
		for _, f := range g.Flags {
			flagCol = max(flagCol, len(f.left()))
		}
	}
	indent := flagCol + 4
	for _, g := range helpGroups {
		head(g.Title)
		for _, f := range g.Flags {
			fmt.Fprintf(out, "  %-*s  %s\n", flagCol, f.left(),
				pal.dim(wrapIndent(f.Help, width, indent)))
		}
	}

	head("examples")
	col = 0
	for _, e := range helpExamples {
		col = max(col, len(e.Cmd))
	}
	for _, e := range helpExamples {
		fmt.Fprintf(out, "  %-*s  %s\n", col, e.Cmd, pal.dim(e.Help))
	}

	head("exit codes")
	for _, c := range helpExitCodes {
		fmt.Fprintf(out, "  %-4s %s\n", c.Code, pal.dim(c.Meaning))
	}

	head("environment")
	for _, e := range []struct{ Name, Help string }{
		{"GAUNTLET_HOME", "where run journals live (default ~/.gauntlet)"},
		{"NO_COLOR", "disable color, however it is set"},
		{"CLICOLOR_FORCE", "keep color when the output is piped"},
		{"FORCE_COLOR", "same as CLICOLOR_FORCE"},
		{"GITHUB_TOKEN", "used for release lookups, to avoid rate limits"},
	} {
		fmt.Fprintf(out, "  %-14s %s\n", e.Name, pal.dim(e.Help))
	}
	fmt.Fprintln(out)
}
