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
	"time"
)

// flagDoc is one row of the help screen.
type flagDoc struct {
	Short string // without the dash, empty when there is none
	Long  string // without the dashes
	Arg   string // metavar, empty for booleans
	Help  string
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
		{"r", "reviews", "LIST", "reviews and/or sets to run; the -review suffix is optional, repeats add weight, 'suggest' adds an agent's picks to the list (repeatable)"},
		{"x", "exclude", "LIST", "reviews and/or sets to skip (repeatable)"},
		{"s", "suggest", "", "an agent picks the reviews; any named with --reviews are scheduled as well"},
		{"", "suggest-agent", "AGENT", "agent to run the suggest step, or 'gauntlet' to choose from file signals with no agent at all (default: sample from --agents)"},
		{"", "suggest-timeout", "DUR", fmt.Sprintf("timeout for the suggest step (default %dm)", int(defaultTimeout/time.Minute))},
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
		{"", "dirs", "LIST", "review several directories in parallel, each with its own --jobs pool; globs are expanded (repeatable)"},
		{"", "target-dirs", "LIST", "alias of --dirs, kept for scripts from the Python tool; takes a comma-separated list"},
		{"j", "jobs", "N", "reviews at a time per directory; above 1, each gets its own git worktree and is merged back"},
		{"t", "timeout", "DUR", fmt.Sprintf("per-review timeout: 90s, 30m, 1h, 2d (default %dm)", int(defaultTimeout/time.Minute))},
		{"", "merge-into", "BRANCH", "after each loop, merge this branch's committed work into BRANCH"},
		{"", "resolve-conflicts", "", "have an agent resolve a review branch that will not merge (default true)"},
		{"", "stacked-prs", "", "one isolated worktree; each changed review opens a PR based on the previous one"},
		{"", "pr-base", "BRANCH", "remote base fetched for --stacked-prs (default: current branch name)"},
		{"", "push-remote", "REMOTE", "remote receiving stacked PR branches (default: origin)"},
		{"", "retries", "N", fmt.Sprintf("reruns of a failed review on the same agent, waiting longer each time (default %d)", defaultRetries)},
		{"", "runtime", "DUR", "wall-clock budget for the whole run (0 = unlimited)"},
		{"1", "once", "", "run a single loop and exit"},
		{"n", "max-loops", "N", "stop after N loops (0 means unlimited)"},
		{"", "seed", "N", "RNG seed for review order and agent picks, recorded in the journal (default: random)"},
		{"c", "commit", "", "after each review, an agent commits the changes"},
		{"p", "push", "", "like --commit, and pushes"},
		{"", "yolo", "", "drop the caution rules: bigger, more ambitious changes"},
		{"y", "yes", "", "answer yes to confirmation prompts"},
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
		{"", "opencode-db", "", "read opencode's session database for its token counts; the driver ships in a default build, this flag opens the store"},
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
		{"", "limit", "N", fmt.Sprintf("runs: how many entries to list (default %d)", defaultRunsLimit)},
	}},
}

var helpCommands = []struct{ Cmd, Help string }{
	{"gauntlet [flags]", "review the current directory, looping until stopped"},
	{"gauntlet pick", "compose a run on screen: reviews, agents, concurrency"},
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
	{"gauntlet -r quick --stacked-prs --pr-base main", "selected reviews as an unmerged PR stack"},
	{"gauntlet --dirs ~/src/*", "every repo under ~/src, in parallel"},
	{"gauntlet --suggest --yes --tui", "agent-picked reviews, live dashboard"},
	{"gauntlet --agent-cmd pi='pi -p {prompt}' -a pi", "run an agent gauntlet does not ship"},
}

var helpExitCodes = []struct{ Code, Meaning string }{
	{"0", "every review ran and passed"},
	{"1", "a review failed, timed out, was skipped, or would not merge; a commit step failed"},
	{"2", "usage error"},
	{"75", "another instance holds the lock for that directory"},
	{"130", "interrupted"},
}

// helpEnvVars is the environment section: the variables a consumer can set to
// change gauntlet's behavior. GAUNTLET_HOME and GITHUB_TOKEN are read by
// internal packages (gauntlethome, selfupdate); the color names come from
// report.go's consts, so this table cannot drift from colorEnabled.
var helpEnvVars = []struct{ Name, Help string }{
	{"GAUNTLET_HOME", "root of the state tree: journals, reload handoff, agents.json (default ~/.gauntlet)"},
	{"GAUNTLET_NO_ANIMATION", "freeze the dashboard's animated reasoning glyph (reduced motion)"},
	{"NO_COLOR", "disable color, however it is set"},
	{"CLICOLOR_FORCE", "keep color when the output is piped"},
	{"FORCE_COLOR", "same as CLICOLOR_FORCE"},
	{"TERM", "\"dumb\" disables color, even with the two above"},
	{"GITHUB_TOKEN", "used for release lookups, to avoid rate limits"},
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
	for _, e := range helpEnvVars {
		fmt.Fprintf(out, "  %-14s %s\n", e.Name, pal.dim(e.Help))
	}
	fmt.Fprintln(out)
}
