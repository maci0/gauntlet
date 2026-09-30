// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The help screen. Go's flag.PrintDefaults lists every alias as its own entry
// and sorts them alphabetically, which turns the flag set into one unordered
// wall of lines. This renders them grouped, aliased, aligned, and wrapped
// instead. A test
// keeps this table and the registered flags from drifting apart.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

func crushNote() string {
	if haveSQLite {
		return ", and crush's project database."
	}
	return " (crush reports nothing: its counters live in a SQLite database, build with -tags sqlite)."
}

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
		{"", "paths", "LIST", "scope reviews to these files, directories, or globs, relative to the reviewed directory; prompt-enforced, the agent keeps the whole tree (repeatable)"},
		{"", "max-reviews", "N", "cap reviews per loop, cut after the seeded shuffle so --seed replays which N ran; a repeated review fills one slot per listing (0 = unlimited)"},
		{"s", "suggest", "", "an agent picks the reviews; any named with --reviews are scheduled as well"},
		{"", "suggest-agent", "AGENT", "agent to run the suggest step, or 'gauntlet' to choose from file signals with no agent at all (default: sample from --agents)"},
		{"", "suggest-timeout", "DUR", fmt.Sprintf("timeout for the suggest step (default %dm)", int(defaultTimeout/time.Minute))},
		{"", "prompt-dir", "DIR", "use *-review.md files from DIR instead of the embedded set"},
	}},
	{"Agents", []flagDoc{
		{"a", "agents", "LIST", "agent CLIs, optionally agent:model@effort; 'mixed' means every installed agent (repeatable, default: auto-detect)"},
		{"", "bin", "TOOL=PATH", "run an agent from a specific executable (repeatable)"},
		{"", "agent-cmd", "NAME=ARGV", "define an agent gauntlet does not know, e.g. pi='pi -p {prompt}' (repeatable)"},
		{"", "continue-sessions", "", "resume each agent's session between reviews (conflicts with --jobs > 1 and --stacked-prs)"},
	}},
	{"Execution", []flagDoc{
		{"C", "dir", "DIR", "directory to review"},
		{"", "dirs", "LIST", "review several directories in parallel, each with its own --jobs pool; globs are expanded (repeatable)"},
		{"", "target-dirs", "LIST", "alias of --dirs, kept for scripts from the Python tool; takes a comma-separated list"},
		{"j", "jobs", "N", "parallel lanes per directory; above 1, each runs in its own worktree and is merged back"},
		{"t", "timeout", "DUR", fmt.Sprintf("per-review timeout: 90s, 30m, 1h, 2d (default %dm)", int(defaultTimeout/time.Minute))},
		{"", "merge-into", "BRANCH", "after each loop, merge this branch's committed work into BRANCH"},
		{"", "resolve-conflicts", "", "have an agent resolve a review branch that will not merge (default true; --resolve-conflicts=false to disable)"},
		{"", "stacked-prs", "", "isolated worktree; each changed review opens a PR on the previous one; -n starts a new stack from the last tip"},
		{"", "pr-base", "BRANCH", "remote base fetched for --stacked-prs (default: current branch name)"},
		{"", "push-remote", "REMOTE", "remote receiving stacked PR branches (default: origin)"},
		{"", "retries", "N", fmt.Sprintf("reruns of a failed review on the same agent from the same tree, waiting longer each time (default %d)", defaultRetries)},
		{"", "runtime", "DUR", "wall-clock budget for the whole run (0 = unlimited)"},
		{"", "token-budget", "N", "stop at N reported tokens for the run, and stop the review that reaches it (0 = unlimited)"},
		{"", "usage-cmd", "CMD", "command printing the percent of the provider's usage window already spent; must resolve to an executable, and is used with --usage-limit or not at all"},
		{"", "usage-limit", "PCT", "stop starting reviews at this percent of that window (0 = unlimited); needs --usage-cmd"},
		{"1", "once", "", "run a single loop and exit"},
		{"n", "max-loops", "N", "stop after N loops (0 means unlimited; default 1 with --stacked-prs)"},
		{"", "seed", "N", "RNG seed for review order and agent picks, recorded in the journal (default: random)"},
		{"c", "commit", "", "after each review, an agent commits the changes"},
		{"p", "push", "", "like --commit, and pushes"},
		{"", "no-sandbox", "", "disable kernel filesystem write confinement for trusted runs"},
		{"", "sandbox-write", "DIR", "allow writes under an additional existing directory (repeatable)"},
		{"", "yolo", "", "drop the caution rules: bigger, more ambitious changes"},
		{"y", "yes", "", "answer yes to confirmation prompts"},
		{"", "semcode", "", "build a semcode index before the loop"},
		{"", "keep-runs", "N", fmt.Sprintf("run journals kept in the state root (GAUNTLET_HOME, else ~/.gauntlet); older ones move to pruned/ at the end of a run, 0 keeps all (default %d)", defaultKeepRuns)},
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
		{"", "hot-reload", "", "reload when this binary is replaced (default true; --hot-reload=false to disable)"},
		{"", "auto-update", "", "install new releases during the run"},
		{"", "update-repo", "REPO", "GitHub repo to fetch releases from (default " + selfupdate.DefaultRepo + ")"},
		{"", "check", "", "update: report the latest release without installing"},
	}},
	{"History", []flagDoc{
		{"", "limit", "N", fmt.Sprintf("runs: how many entries to list (default %d)", defaultRunsLimit)},
		{"", "restore", "RUN-ID", "runs: put a pruned run back in the listing"},
		{"", "json", "", "runs: print the listing as JSON on stdout, for scripts and dashboards"},
	}},
}

var helpCommands = []struct{ Cmd, Help string }{
	{"gauntlet [flags]", "review the current directory, looping until stopped"},
	{"gauntlet pick", "compose a run on screen: reviews, agents, concurrency"},
	{"gauntlet doctor", "report which agent CLIs and helper tools are installed"},
	{"gauntlet update [--check]", "replace this binary with the latest verified release"},
	{"gauntlet runs [--limit N] [--json]", "list recent runs; rebuilds a missing index from the journals"},
	{"gauntlet show <run-id>", "replay one run's journal"},
	{"gauntlet version", "print the version and exit"},
	{"gauntlet help", "show this help and exit"},
}

// commandNames is the subcommand surface, including help. peelSubcommand and
// the unknown-command hint both read this list, so a new word cannot land in
// one place and not the other.
var commandNames = []string{"help", "pick", "doctor", "update", "runs", "show", "version"}

var helpExamples = []struct{ Cmd, Help string }{
	{"gauntlet -a claude --once", "one pass over every review, then stop"},
	{"gauntlet -r quick -x test-review", "a named set, minus one review"},
	{"gauntlet -j 4 -a mixed", "four at a time, worktree-isolated and merged"},
	{"gauntlet -r quick --stacked-prs --pr-base main", "selected reviews as an unmerged PR stack"},
	{"gauntlet --dirs ~/src/*", "every repo under ~/src, in parallel"},
	{"gauntlet --suggest --yes --tui", "agent-picked reviews, live dashboard"},
	{"gauntlet --agent-cmd pi='pi -p {prompt}' -a pi", "run an agent gauntlet does not ship"},
	{"gauntlet runs --restore <run-id>", "put a pruned run back in the listing"},
	{"gauntlet runs --json | jq '.runs[].run_id'", "the run index, for a script"},
}

var helpExitCodes = []struct{ Code, Meaning string }{
	{"0", "the command did what it was asked: every review ran and passed, or the listing, replay, or report was printed"},
	{"1", "a review failed, timed out, was skipped, or would not merge; a commit step failed; doctor found no agent to launch; update failed; the run listing could not be read in full"},
	{"2", "usage error"},
	{"75", "another instance holds the lock for that directory"},
	{"130", "interrupted"},
}

// helpEnvVar is one row of the environment section. Secret marks a variable
// whose value must never be printed; `gauntlet doctor` reports it as present
// and never as a value, so the marking has one home rather than one here and
// one there.
type helpEnvVar struct {
	Name, Help string
	Secret     bool
}

// helpEnvVars is the environment section: the variables a consumer can set to
// change gauntlet's behavior. GAUNTLET_HOME and GH_TOKEN/GITHUB_TOKEN are
// read by internal packages (gauntlethome, selfupdate); the color names are
// report.go's consts, so this table cannot drift from colorEnabled.
var helpEnvVars = []helpEnvVar{
	{"TMPDIR", "absolute temporary directory added to sandbox writable roots alongside /tmp; must exist (make test ignores exported TMPDIR)", false},
	{"GAUNTLET_HOME", "root of the state tree: journals, reload handoff, agents.json; ~ and $VAR expand, an empty value is the variable unset, and one naming something that is not a directory is refused (default ~/.gauntlet)", false},
	{"GAUNTLET_NO_ANIMATION", "stop the dashboard moving (reduced motion): the reasoning glyph holds one frame and the screen stops redrawing ten times a second; read first, so set to 0 or false it outranks the two below", false},
	{"NO_MOTION", "same as GAUNTLET_NO_ANIMATION, unless that one is set to a false value", false},
	{"REDUCED_MOTION", "same as GAUNTLET_NO_ANIMATION, unless that one is set to a false value", false},
	{report.EnvNoColor, "disable color, however it is set", false},
	{report.EnvCLIColorForce, "keep color when the output is piped", false},
	{report.EnvForceColor, "same as CLICOLOR_FORCE", false},
	{report.EnvTerm, "\"dumb\" in any case disables color, even with the two above", false},
	{"GITHUB_TOKEN", "used for GitHub release lookups and downloads", true},
	{"GH_TOKEN", "same as GITHUB_TOKEN; wins when both carry a token, an empty one is ignored", true},
	{"GIT_SSH_COMMAND", "command git uses for SSH; defaults to ssh, so repository-local config cannot replace it", false},
}

// exitCodeIndent is where a wrapped exit-code meaning starts: the two leading
// spaces, the four-character code column, and the space after it.
const exitCodeIndent = 2 + 4 + 1

// printUsage renders the help screen.
func printUsage(out io.Writer, pal report.Palette, width int) {
	if width < minTerminalWidth {
		width = minTerminalWidth
	}
	head := func(s string) { fmt.Fprintf(out, "\n%s\n", pal.Bold(strings.ToUpper(s))) }

	fmt.Fprintf(out, "%s %s\n%s\n",
		pal.Bold("gauntlet"), pal.Dim(version),
		pal.Dim(fmt.Sprintf("Run your codebase through %d specialized review prompts, dispatched to",
			len(prompt.BundledNames()))))
	fmt.Fprintln(out, pal.Dim("the AI coding agents you have installed. Fixes land in the working tree."))

	head("usage")
	col := 0
	for _, c := range helpCommands {
		col = max(col, len(c.Cmd))
	}
	for _, c := range helpCommands {
		fmt.Fprintf(out, "  %-*s  %s\n", col, c.Cmd, pal.Dim(c.Help))
	}

	// A subcommand reads its own flags and refuses the rest, so the screen
	// says which are its own. The list is the one rejectStrayFlags checks, so
	// the screen and the refusal cannot name different flags.
	head("subcommand flags")
	subCol := 0
	takes := make(map[string][]string, len(commandNames))
	for _, name := range commandNames {
		if name == "help" {
			continue
		}
		takes[name] = subcommandFlags[name]
		subCol = max(subCol, len(name))
	}
	for _, name := range commandNames {
		if name == "help" {
			continue
		}
		own := spellFlags(takes[name])
		if own == "" {
			own = "none"
		}
		fmt.Fprintf(out, "  %-*s  %s\n", subCol, name, pal.Dim(own))
	}
	// One sentence carries the rule both ways: the globals may lead, and a
	// subcommand's own flags follow it. Wrapped like the rest, so it stays
	// readable on a narrow terminal.
	fmt.Fprintf(out, "  %s\n", pal.Dim(report.WrapIndent(
		"--log and --no-color work with every command, and may precede it; "+
			"every other flag belongs after the subcommand it is for", width, 2)))

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
				pal.Dim(report.WrapIndent(f.Help, width, indent)))
		}
	}

	head("examples")
	col = 0
	for _, e := range helpExamples {
		col = max(col, len(e.Cmd))
	}
	for _, e := range helpExamples {
		fmt.Fprintf(out, "  %-*s  %s\n", col, e.Cmd, pal.Dim(e.Help))
	}

	head("exit codes")
	for _, c := range helpExitCodes {
		fmt.Fprintf(out, "  %-4s %s\n", c.Code,
			pal.Dim(report.WrapIndent(c.Meaning, width, exitCodeIndent)))
	}

	head("environment")
	envCol := 0
	for _, e := range helpEnvVars {
		envCol = max(envCol, len(e.Name))
	}
	envIndent := envCol + 4
	for _, e := range helpEnvVars {
		fmt.Fprintf(out, "  %-*s  %s\n", envCol, e.Name,
			pal.Dim(report.WrapIndent(e.Help, width, envIndent)))
	}
	fmt.Fprintln(out)
}
