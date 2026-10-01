// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/evidence"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/runx"
	"github.com/maci0/gauntlet/internal/selfupdate"
	"golang.org/x/term"
)

// errHelp means help was requested; run prints it on stdout and exits 0.
var errHelp = errors.New("help requested")

// parseError marks a failure whose message and usage screen have already been
// written to stderr by reportUsage. run must not print either again.
type parseError struct{ err error }

func (e parseError) Error() string { return e.err.Error() }

type options struct {
	command string // "", help, pick, doctor, update, runs, show, version

	// selection
	reviews    string
	reviewsSet bool // an explicit (possibly empty) --reviews was given
	// suggest runs the triage step. It composes with --reviews rather than
	// replacing it: what an agent picks and what a person named are one
	// schedule, and a review named on both sides is scheduled twice, which is
	// how this tool has always spelled "weight this more".
	suggest        bool
	exclude        string
	suggestAgent   *agent.Spec
	suggestTimeout time.Duration
	promptDir      string
	// paths scopes review prompts to these files/dirs/globs, relative to the
	// reviewed directory. Prompt-enforced: the agent keeps the whole tree.
	paths []string

	// agents
	agents []agent.Spec
	bin    map[string]string

	// execution
	dir  string
	dirs []string
	// resolvedDirs is dirs (or the single dir) after expansion and
	// validation, absolute and deduplicated. It is filled while parsing so a
	// path that is not there is reported like every other bad flag value,
	// rather than from the run path with no usage screen beside it.
	resolvedDirs     []string
	timeout          time.Duration
	runtime          time.Duration
	tokenBudget      int
	usageCmd         string
	usageArgv        []string
	usageLimit       float64
	jobs             int
	retries          int
	mergeInto        string
	resolveConflicts bool
	maxLoops         int
	maxReviews       int
	seed             uint64
	commit           bool
	push             bool
	stackedPRs       bool
	prBase           string
	pushRemote       string
	noSandbox        bool
	sandboxWrite     []string
	yolo             bool
	yes              bool
	semcode          bool
	continueSessions bool

	// output and modes
	list       bool
	dryRun     bool
	showPrompt string
	logFile    string
	quiet      bool
	raw        bool
	stream     bool
	tui        bool
	noColor    bool
	width      int

	// opencode is the one agent that neither prints counters nor keeps a
	// JSONL transcript, so its tokens are only visible in its database.
	openCodeDB bool

	// updates
	hotReload  bool
	autoUpdate bool
	updateRepo string
	checkOnly  bool

	// history
	runsLimit  int
	keepRuns   int
	showRun    string
	resumeRun  string // resume: the run to continue, "" to list them
	restoreRun string
	json       bool
}

// reportUsage writes the message and the help screen to stderr, mirroring how
// the flag package reports its own failures, and returns the error marked as
// reported. Every rejection from parsing goes through this, so any bad
// invocation reads the same way: cause first, then the screen.
func reportUsage(o *options, err error) error {
	fmt.Fprintln(os.Stderr, err)
	printUsage(os.Stderr, report.Palette{On: report.ColorEnabled(os.Stderr) && !o.noColor}, o.width)
	return parseError{err}
}

// listFlag collects a repeatable, comma-separated flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	*l = append(*l, splitNames(v)...)
	return nil
}

// durationFlag accepts the CLI's duration syntax (90s, 30m, 1h, 2d). Flags
// with allowZero also take 0, where the runner reads it as "unlimited".
type durationFlag struct {
	d         *time.Duration
	allowZero bool
}

func (f durationFlag) String() string {
	if f.d == nil {
		return ""
	}
	return humanize.Duration(*f.d)
}

func (f durationFlag) Set(v string) error {
	d, err := parseDuration(v)
	if err != nil {
		return err
	}
	if !f.allowZero && d <= 0 {
		return fmt.Errorf("duration must be positive: %q", v)
	}
	*f.d = d
	return nil
}

func parseDuration(s string) (time.Duration, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, fmt.Errorf("invalid duration: %q (e.g. 90s, 30m, 1h, 2d)", s)
	}
	unit := time.Second
	digits := trimmed
	switch last := trimmed[len(trimmed)-1]; last {
	case 's', 'S':
		digits = trimmed[:len(trimmed)-1]
	case 'm', 'M':
		unit, digits = time.Minute, trimmed[:len(trimmed)-1]
	case 'h', 'H':
		unit, digits = time.Hour, trimmed[:len(trimmed)-1]
	case 'd', 'D':
		unit, digits = 24*time.Hour, trimmed[:len(trimmed)-1]
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if errors.Is(err, strconv.ErrRange) || (err == nil && n > math.MaxInt64/int64(unit)) {
		return 0, fmt.Errorf("duration is too large: %q", s)
	}
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid duration: %q (e.g. 90s, 30m, 1h, 2d)", s)
	}
	return time.Duration(n) * unit, nil
}

// parseSeed reads a --seed value as the help states it: decimal, or a 0x hex
// literal, with the underscores a Go literal allows between digits. Base 0 is
// not what the flag means, because it reads a leading zero as octal: --seed 010
// ran as 8, and --seed 08 was refused with a syntax error rather than the
// message the flag promises. The seed is the run's replay value, so a number
// that reads as something else is written to the journal and reproduced by
// every later rerun.
func parseSeed(v string) (uint64, error) {
	digits, base := v, 10
	if len(v) > 2 && (v[:2] == "0x" || v[:2] == "0X") {
		digits, base = v[2:], 16
	}
	// ParseUint takes underscores only with base 0, and a missing one there
	// would come back as a syntax error; stripping them keeps 1_000 working
	// and leaves a malformed placement to the same message.
	if !strings.HasPrefix(digits, "_") && !strings.HasSuffix(digits, "_") &&
		!strings.Contains(digits, "__") && !strings.Contains(digits, "_x") &&
		!strings.Contains(digits, "_X") {
		digits = strings.ReplaceAll(digits, "_", "")
	}
	return strconv.ParseUint(digits, base, 64)
}

// Flag defaults the documentation quotes. They live here so docs/CLI.md, the
// help table, and the parser cannot drift apart.
const (
	// defaultTimeout bounds one review, and the suggest step that precedes
	// it: long enough for a real review of a large tree, short enough that a
	// wedged agent does not hold a loop for an hour.
	defaultTimeout = 30 * time.Minute
	// defaultRetries reruns a review whose agent failed to launch or exited
	// nonzero, before falling back to another agent.
	defaultRetries = 2
	// defaultRunsLimit is how many past runs `gauntlet runs` prints.
	defaultRunsLimit = 20
	// defaultKeepRuns is how many run journals survive under ~/.gauntlet. A
	// run files a journal and an index row and nothing else ever removes
	// either, so without a bound the state tree keeps one file per run for
	// the life of the install. Far past what a listing or the review-history
	// weights ever read, so the bound costs no history anyone looks at.
	defaultKeepRuns = 200
	// maxUsageLimit is the top of the percentage range --usage-limit accepts;
	// above 100 the provider's window is already past.
	maxUsageLimit = 100
	// minTerminalWidth is the narrowest terminal the help screen is laid out
	// for; narrower than that, and the default below, is the assumed width.
	minTerminalWidth = 60
	// defaultTerminalWidth stands in when the real width is unknown.
	defaultTerminalWidth = 100
)

// errUsageCmdBlank is returned for both ways --usage-cmd can come out empty:
// a blank value, and a value that is all whitespace and so splits into no
// argv at all.
var errUsageCmdBlank = errors.New("--usage-cmd is blank: it is split on whitespace " +
	"and executed directly, so it needs a command to run")

func parseFlags(argv []string) (*options, error) {
	o := &options{
		bin: map[string]string{}, timeout: defaultTimeout,
		suggestTimeout: defaultTimeout, jobs: 1, retries: defaultRetries,
		hotReload: true, stream: true,
		runsLimit: defaultRunsLimit, keepRuns: defaultKeepRuns, width: terminalWidth(),
	}

	cmd, argv := peelSubcommand(argv)
	switch {
	case cmd == "":
		// the default run
	case !slices.Contains(commandNames, cmd):
		return nil, reportUsage(o, unknownCommand(cmd))
	default:
		o.command = cmd
	}

	fs, raw := buildFlagSet(o)
	if o.command == "show" || o.command == "resume" {
		// A run id never starts with '-', so flags are peeled off either side
		// of it (`show --no-color RUN` and `show RUN --no-color` mean the same
		// thing). Whether an id was given is decided in finishFlags, after the
		// flags parse: a miss on one of them is what a user who mistyped
		// `--limt` needs to read, and reporting the missing id instead sends
		// them looking in the wrong place.
		id, rest := peelShowRun(fs, argv)
		if o.command == "show" {
			o.showRun = id
		} else {
			o.resumeRun = id
		}
		argv = rest
	}

	// Report unknown flags ourselves so a close miss can carry a "did you
	// mean" hint, the same shape as an unknown command. The flag package
	// would otherwise print the bare message and the usage screen first.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(expandAttachedValues(fs, argv)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout, report.Palette{On: report.ColorEnabled(os.Stdout) && !o.noColor}, o.width)
			return nil, errHelp
		}
		return nil, reportUsage(o, enhanceFlagError(err, o, fs))
	}
	opts, err := finishFlags(o, fs, raw)
	if err != nil {
		if errors.Is(err, errHelp) {
			return nil, err // already rendered on stdout; nothing to report
		}
		// o still holds whatever parsing learned (--no-color, width), which is
		// what reportUsage needs.
		return nil, reportUsage(o, err)
	}
	return opts, nil
}

// raw holds the flag values that need post-processing before they become
// options: repeatable lists, and the shorthands that rewrite other fields.
type rawFlags struct {
	reviews, exclude, agents, bins, dirs listFlag
	agentCmds, paths                     listFlag
	suggestAgent                         string
	restore                              countedString
	suggest, once, showVersion, help     bool
}

// buildFlagSet registers every flag. It is separate from parsing so a test can
// enumerate the flags and compare them against the help screen.
func buildFlagSet(o *options) (*flag.FlagSet, *rawFlags) {
	raw := &rawFlags{}

	fs := flag.NewFlagSet("gauntlet", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		// A --no-color seen before the error applies here too, matching how
		// -h renders its screen.
		printUsage(os.Stderr, report.Palette{On: report.ColorEnabled(os.Stderr) && !o.noColor}, o.width)
	}

	alias := func(short, long string, register func(name string)) {
		register(long)
		if short != "" {
			register(short)
		}
	}

	alias("r", "reviews", func(n string) {
		fs.Var(&raw.reviews, n, "reviews and/or sets to run (comma-separated, repeatable); "+
			"repeats add weight, 'suggest' adds an agent's picks")
	})
	alias("x", "exclude", func(n string) { fs.Var(&raw.exclude, n, "reviews and/or sets to skip") })
	fs.Var(&raw.paths, "paths", "scope reviews to these files, directories, or globs, "+
		"relative to the reviewed directory (comma-separated, repeatable)")
	alias("s", "suggest", func(n string) {
		fs.BoolVar(&raw.suggest, n, false, "pick relevant reviews, beside any named with --reviews")
	})
	fs.StringVar(&raw.suggestAgent, "suggest-agent", evidence.AgentName,
		"agent to run the suggest step, or 'gauntlet' to pick from file signals instead")
	fs.Var(durationFlag{d: &o.suggestTimeout}, "suggest-timeout",
		fmt.Sprintf("timeout for the suggest step (default %dm)", int(defaultTimeout/time.Minute)))
	fs.StringVar(&o.promptDir, "prompt-dir", "", "directory of *-review.md files (default: the bundled set)")

	alias("a", "agents", func(n string) {
		fs.Var(&raw.agents, n, "agent CLIs, optionally agent:model@effort; 'mixed' means every installed agent")
	})
	fs.Var(&raw.bins, "bin", "run an agent from a specific executable, TOOL=PATH (repeatable)")
	fs.Var(&raw.agentCmds, "agent-cmd", "define an agent: NAME=ARGV with a {prompt} placeholder (repeatable)")

	alias("C", "dir", func(n string) { fs.StringVar(&o.dir, n, ".", "directory to review") })
	fs.Var(&raw.dirs, "dirs", "review several directories in parallel, each with its own --jobs pool (repeatable, comma-separated)")
	// The name the Python tool used. Scripts that pass it keep working, but it
	// takes a comma-separated list here rather than space-separated arguments.
	fs.Var(&raw.dirs, "target-dirs", "alias of --dirs")
	alias("t", "timeout", func(n string) {
		fs.Var(durationFlag{d: &o.timeout}, n,
			fmt.Sprintf("per-review timeout (default %dm)", int(defaultTimeout/time.Minute)))
	})
	fs.Var(durationFlag{d: &o.runtime, allowZero: true}, "runtime", "wall-clock budget for the whole run (0 = unlimited)")
	fs.IntVar(&o.tokenBudget, "token-budget", 0,
		"stop starting reviews once the run's agents have reported this many tokens in total (0 = unlimited)")
	fs.StringVar(&o.usageCmd, "usage-cmd", "",
		"command printing the percentage of the provider's usage window already spent, for --usage-limit")
	fs.Float64Var(&o.usageLimit, "usage-limit", 0,
		"stop starting reviews at this percentage of the provider's usage window, per --usage-cmd (0 = unlimited)")
	alias("j", "jobs", func(n string) {
		fs.IntVar(&o.jobs, n, 1, "reviews to run at once per directory; >1 gives each its own git worktree and merges back")
	})
	fs.IntVar(&o.retries, "retries", defaultRetries, "reruns of a failed review on the same agent from the same tree, waiting longer each time (0 = none)")
	alias("n", "max-loops", func(n string) { fs.IntVar(&o.maxLoops, n, 0, "stop after N loops (0 = unlimited)") })
	fs.IntVar(&o.maxReviews, "max-reviews", 0, "cap reviews per loop, cut after the seeded shuffle; "+
		"a repeated review fills one slot per listing (0 = unlimited)")
	fs.Func("seed", "RNG seed for review order and agent picks; recorded in the journal (0 = random)", func(v string) error {
		n, err := parseSeed(v)
		if err != nil {
			return errors.New("must be a nonnegative integer (decimal or 0x hex)")
		}
		o.seed = n
		return nil
	})
	alias("1", "once", func(n string) { fs.BoolVar(&raw.once, n, false, "run a single loop and exit") })
	alias("c", "commit", func(n string) { fs.BoolVar(&o.commit, n, false, "commit after each review") })
	alias("p", "push", func(n string) { fs.BoolVar(&o.push, n, false, "commit and push after each review") })
	fs.StringVar(&o.mergeInto, "merge-into", "",
		"after each loop, merge this branch's committed work into BRANCH")
	fs.BoolVar(&o.stackedPRs, "stacked-prs", false,
		"run reviews sequentially in an isolated worktree and open a linear PR stack; -n starts a new stack from the previous tip")
	fs.StringVar(&o.prBase, "pr-base", "",
		"remote base branch fetched for --stacked-prs (default: current branch name)")
	fs.StringVar(&o.pushRemote, "push-remote", "origin",
		"Git remote that receives --stacked-prs branches")
	fs.BoolVar(&o.resolveConflicts, "resolve-conflicts", true,
		"hand a review branch that will not merge to an agent to resolve "+
			"(--resolve-conflicts=false keeps it for a human)")
	fs.BoolVar(&o.noSandbox, "no-sandbox", false, "disable kernel filesystem write confinement for trusted runs")
	fs.Func("sandbox-write", "allow writes under an additional existing directory (repeatable)", func(v string) error {
		if strings.TrimSpace(v) == "" {
			return errors.New("directory must not be empty")
		}
		o.sandboxWrite = append(o.sandboxWrite, v)
		return nil
	})
	fs.BoolVar(&o.yolo, "yolo", false, "drop the caution rules: bigger, more ambitious changes")
	alias("y", "yes", func(n string) { fs.BoolVar(&o.yes, n, false, "answer yes to confirmation prompts") })
	fs.BoolVar(&o.semcode, "semcode", false, "build a semcode index before the loop")
	fs.BoolVar(&o.continueSessions, "continue-sessions", false, "resume each agent's session between reviews")
	fs.IntVar(&o.keepRuns, "keep-runs", defaultKeepRuns,
		"how many run journals to keep in the state root; older ones move to pruned/ at the end of a run (0 = keep all)")

	alias("l", "list", func(n string) { fs.BoolVar(&o.list, n, false, "list available reviews and sets, then exit") })
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the planned schedule, then exit")
	fs.StringVar(&o.showPrompt, "show-prompt", "", "print the composed prompt for one review, then exit")
	fs.StringVar(&o.logFile, "log", "", "also write all output to FILE")
	alias("q", "quiet", func(n string) { fs.BoolVar(&o.quiet, n, false, "discard agent output") })
	fs.BoolVar(&o.raw, "raw", false, "echo agent output verbatim instead of normalizing it")
	fs.BoolVar(&o.openCodeDB, "opencode-db", false, "read opencode's SQLite session store for its token counts (the driver is in a default build)")
	fs.BoolVar(&o.stream, "stream", true, "ask agents for machine-readable output where supported: live token counts and reasoning (--stream=false to disable)")
	fs.BoolVar(&o.tui, "tui", false, "live dashboard")
	fs.BoolVar(&o.noColor, "no-color", false, "disable color")

	fs.BoolVar(&o.hotReload, "hot-reload", true, "reload automatically when this binary is replaced")
	fs.BoolVar(&o.autoUpdate, "auto-update", false, "check for new releases during the run and install them")
	fs.StringVar(&o.updateRepo, "update-repo", selfupdate.DefaultRepo, "GitHub repo to fetch releases from")
	fs.BoolVar(&o.checkOnly, "check", false, "update: report the latest release without installing")
	alias("V", "version", func(n string) { fs.BoolVar(&raw.showVersion, n, false, "print the version and exit") })
	alias("h", "help", func(n string) { fs.BoolVar(&raw.help, n, false, "show this help and exit") })
	fs.IntVar(&o.runsLimit, "limit", defaultRunsLimit, "runs: how many entries to list")
	raw.restore.dst = &o.restoreRun
	fs.Var(&raw.restore, "restore", "runs: put a pruned run back in the listing by run id")
	fs.BoolVar(&o.json, "json", false,
		"runs: print the listing as JSON on stdout, and nothing else there")

	return fs, raw
}

// configureAgents loads the agent definitions for a run: the user's file
// first, then the --agent-cmd definitions on the command line, which win.
func configureAgents(o *options, fs *flag.FlagSet, agentCmds listFlag) error {
	if path := agent.CustomFilePath(); path != "" {
		if err := agent.LoadCustomFile(path); err != nil {
			return err
		}
	}
	// A repeated --agent-cmd with a different definition is a typo, not a
	// choice: the last one would silently win, which is how --bin came to
	// refuse its own duplicates. The file loaded above is still overridden on
	// purpose (the command line wins for its run), so this counts only what
	// the command line itself said. Checking every entry before registering
	// any also keeps a bad list from half-defining agents.
	type namedDef struct {
		raw  string
		def  agent.Custom
		name string
	}
	if isFlagSet(fs, "agent-cmd") && len(agentCmds) == 0 {
		return errors.New("--agent-cmd is empty: want NAME=ARGV")
	}
	defs := make([]namedDef, 0, len(agentCmds))
	cmdDefs := map[string]string{}
	for _, c := range agentCmds {
		name, def, err := agent.ParseAgentCmd(c)
		if err != nil {
			return fmt.Errorf("--agent-cmd %s: %w", c, err)
		}
		if prev, dup := cmdDefs[strings.ToLower(name)]; dup && prev != c {
			return fmt.Errorf("--agent-cmd given twice for %s: %s and %s", name, prev, c)
		}
		cmdDefs[strings.ToLower(name)] = c
		defs = append(defs, namedDef{raw: c, def: def, name: name})
	}
	for _, d := range defs {
		// Whatever the file and the shipped definitions already say about this
		// name, the command line says something else and that is what runs.
		// A built-in tool is not a definition and is not in the registry, so
		// this removes nothing there and Register still refuses to rename it.
		agent.Unregister(d.name)
		if err := agent.Register(d.name, d.def); err != nil {
			return fmt.Errorf("--agent-cmd %s: %w", d.raw, err)
		}
	}
	if o.openCodeDB && !enableOpenCodeDB() {
		return errors.New("--opencode-db needs a build with -tags sqlite")
	}
	// A definition may say where its agent keeps transcripts, which is what
	// gives a non-built-in agent live token counts.
	for _, name := range agent.CustomNames() {
		def, ok := agent.CustomDef(name)
		if !ok || def.Usage == nil {
			continue
		}
		if err := registerTranscript(name, def.Usage); err != nil {
			return err
		}
	}
	return nil
}

// resolveUsage checks the --usage-cmd/--usage-limit pair and splits the
// command into the argv the probe runs.
func resolveUsage(o *options, fs *flag.FlagSet) error {
	if isFlagSet(fs, "usage-cmd") && strings.TrimSpace(o.usageCmd) == "" {
		return errUsageCmdBlank
	}
	// Either flag alone is a misconfiguration worth refusing rather than
	// silently ignoring: a limit with no probe never trips, and a probe with
	// no limit spawns a process per review to no effect.
	if (o.usageCmd == "") != (o.usageLimit == 0) {
		return errors.New("--usage-cmd and --usage-limit are used together, or not at all")
	}
	// ParseFloat accepts "NaN" and the infinities as valid floats. Every
	// comparison against NaN is false, so the range check below cannot see
	// it, and neither can the runner's `pct < limit`: a NaN limit reads as
	// "at or past" on the first check and ends the run. The probe already
	// rejects these; the flag has to as well.
	if math.IsNaN(o.usageLimit) || math.IsInf(o.usageLimit, 0) ||
		o.usageLimit < 0 || o.usageLimit > maxUsageLimit {
		return fmt.Errorf("--usage-limit %g: want a percentage between 0 and %d", o.usageLimit, maxUsageLimit)
	}
	if o.usageCmd != "" {
		// The command is split on whitespace and executed directly, so a
		// value made only of whitespace splits into no argv at all. The
		// pairing check above compares the raw string and cannot see that:
		// `--usage-cmd " " --usage-limit 80` passed it and left the run with
		// a limit that could never trip, because there was no probe to run,
		// the exact half-configured state that check exists to refuse.
		o.usageArgv = strings.Fields(o.usageCmd)
		if len(o.usageArgv) == 0 {
			return errUsageCmdBlank
		}
		// The probe's first word has to name something runnable, checked here
		// with the same resolution the probe itself gets at run time
		// (runx.LookPath over the absolute-only PATH), so this cannot disagree
		// with it. The runner fails open on a probe that will not run, which is
		// right for a probe that breaks mid-run and wrong for one that never
		// could: a typo there left the run enforcing no limit at all, reported
		// by a single line that scrolls past under the agent output, while the
		// operator believed the ceiling was in force. --bin refuses an
		// unresolvable path for the same reason.
		if runx.LookPath(o.usageArgv[0]) == "" {
			return fmt.Errorf("--usage-cmd: not an executable: %s", o.usageArgv[0])
		}
	}
	return nil
}

// finishFlags turns parsed values into validated options.
func finishFlags(o *options, fs *flag.FlagSet, raw *rawFlags) (*options, error) {
	reviews, exclude, agents := raw.reviews, raw.exclude, raw.agents
	bins, dirs, agentCmds := raw.bins, raw.dirs, raw.agentCmds
	suggestAgent, suggest, once, showVersion := raw.suggestAgent, raw.suggest, raw.once, raw.showVersion

	// Help wins over every other flag and validation: an explicitly requested
	// screen belongs on stdout, where redirection and pipes can capture it.
	// The `help` word is the same request, and is parsed rather than handled
	// before flags so `gauntlet --no-color help` still honors --no-color.
	//
	// A word after it names the topic. There is one screen, so a known command
	// prints the same thing `gauntlet help` does, which the SUBCOMMAND FLAGS
	// section answers. An unknown one is refused rather than swallowed: reading
	// `gauntlet helo runs` as a plain `gauntlet help` exits 0 over a page of
	// flags and reports success for a request it never answered.
	if raw.help || o.command == "help" {
		for _, arg := range fs.Args() {
			if err := helpTopic(arg); err != nil {
				return nil, err
			}
		}
		printUsage(os.Stdout, report.Palette{On: report.ColorEnabled(os.Stdout) && !o.noColor}, o.width)
		return nil, errHelp
	}

	if fs.NArg() > 0 {
		return nil, misplacedArgument(fs.Arg(0))
	}
	if showVersion {
		o.command = "version"
	}

	// Version wins over scoping, as help does above. Otherwise a flag that
	// belongs to one subcommand must not be swallowed silently by another:
	// `gauntlet --limit 5` would otherwise start an unlimited run while
	// pretending to honor it.
	if o.command != "version" {
		if isFlagSet(fs, "check") && o.command != "update" {
			return nil, errors.New("--check requires 'gauntlet update'")
		}
		if isFlagSet(fs, "limit") && o.command != "runs" {
			return nil, errors.New("--limit requires 'gauntlet runs'")
		}
		if isFlagSet(fs, "restore") && o.command != "runs" {
			return nil, errors.New("--restore requires 'gauntlet runs'")
		}
		// The default run reads every flag and drops the ones it has no use
		// for, so a stray --json there would start a full review and say
		// nothing about the output format that was asked for.
		if isFlagSet(fs, "json") && o.command != "runs" {
			return nil, errors.New("--json requires 'gauntlet runs'")
		}
		// A second --restore is a mistake, not a choice: the last one would win
		// and the id the first named would be restored by nobody. The rule
		// --bin and --agent-cmd hold themselves to, for the same reason.
		if o.command == "runs" && raw.restore.n > 1 {
			return nil, errors.New("--restore given more than once: want one run id")
		}
	}
	if err := rejectStrayFlags(o, fs, raw.showVersion); err != nil {
		return nil, err
	}
	// Last, so every flag still has its own say: an unknown one has already
	// been reported by Parse, and one that belongs to another subcommand by
	// the checks above. What is left is the flagless request itself.
	if o.command == "show" && o.showRun == "" {
		return nil, errors.New("show needs a run id (see: gauntlet runs)")
	}
	if o.command == "version" {
		if err := validateLog(o, fs); err != nil {
			return nil, err
		}
		return o, nil
	}

	// The state root, validated by the resolver every later read goes
	// through rather than by a second copy of the rule here. gauntlethome
	// already knows what "usable" means (GAUNTLET_HOME expanding to a real or
	// not-yet-existing directory), so asking it is the only way the refusal
	// below and the root a run then writes to are the same question. A value
	// the resolver cannot use is a usage error: it names the directory
	// refusing it, because a mistyped variable must not read as a working
	// setup that quietly journals somewhere else.
	if err := checkStateHome(); err != nil {
		return nil, err
	}

	if o.usesAgents() {
		if err := configureAgents(o, fs, agentCmds); err != nil {
			return nil, err
		}
	}

	if err := resolveUsage(o, fs); err != nil {
		return nil, err
	}

	if isFlagSet(fs, "bin") && len(bins) == 0 {
		return nil, errors.New("--bin is empty: want TOOL=PATH")
	}
	for _, b := range bins {
		tool, path, err := agent.ParseBin(b)
		if err != nil {
			return nil, fmt.Errorf("--bin %s: %w", b, err)
		}
		if prev, dup := o.bin[tool]; dup && prev != path {
			return nil, fmt.Errorf("--bin given twice for %s: %s and %s", tool, prev, path)
		}
		o.bin[tool] = path
	}
	if isFlagSet(fs, "agents", "a") && len(agents) == 0 {
		return nil, errors.New("--agents is empty")
	}
	if len(agents) > 0 {
		specs, err := agent.ParseSpecs(strings.Join(agents, ","))
		if err != nil {
			return nil, err
		}
		o.agents = specs
	}
	if isFlagSet(fs, "suggest-agent") && strings.TrimSpace(suggestAgent) == "" {
		return nil, errors.New("--suggest-agent is empty")
	}
	if suggestAgent == evidence.AgentName {
		// Not an agent: gauntlet itself, reading the tree for signals.
		o.suggestAgent = &agent.Spec{Tool: evidence.AgentName}
	} else if suggestAgent != "" {
		specs, err := agent.ParseSpecs(suggestAgent)
		if err != nil {
			return nil, err
		}
		if len(specs) != 1 {
			return nil, errors.New("--suggest-agent takes exactly one agent (tool, tool:model, or tool:model@effort)")
		}
		o.suggestAgent = &specs[0]
	}

	// An omitted --reviews means all. An explicit empty value (--reviews '')
	// must not silently expand to everything: that is how a script wipes a
	// repo by accident.
	o.reviewsSet = isFlagSet(fs, "reviews", "r")
	o.reviews = strings.Join(reviews, ",")
	if isFlagSet(fs, "exclude", "x") && len(exclude) == 0 {
		return nil, errors.New("--exclude is empty")
	}
	o.exclude = strings.Join(exclude, ",")
	o.dirs = dirs

	// An omitted --paths means the whole tree. An explicit empty one would
	// silently mean the same thing, the opposite of the narrowing whoever
	// typed it asked for: refuse it, in the spirit of --reviews ''.
	if isFlagSet(fs, "paths") && len(raw.paths) == 0 {
		return nil, errors.New("--paths is empty: name at least one file, directory, or glob, or drop the flag")
	}
	// Each entry is pasted into the review prompt as an instruction, so one
	// carrying a line break or a backtick is prompt text, not a path. Refuse it
	// here, where the operator can see which entry it was, rather than
	// rewriting it into the prompt behind a scope block that reads narrower
	// than what was asked for.
	for _, p := range raw.paths {
		if !prompt.PathEntrySafe(p) {
			return nil, fmt.Errorf("invalid --paths entry %q: name a file, directory, or glob "+
				"with no line break, no backtick, and at most %d characters",
				p, prompt.PathEntryMax)
		}
	}
	o.paths = raw.paths

	// "suggest" is a request, not a review name: it can arrive as --suggest or
	// inside --reviews, and either way the rest of the list survives it.
	o.suggest = suggest
	if named := splitNames(o.reviews); slices.Contains(named, prompt.Suggest) {
		o.suggest = true
		kept := make([]string, 0, len(named))
		for _, n := range named {
			if n != prompt.Suggest {
				kept = append(kept, n)
			}
		}
		o.reviews = strings.Join(kept, ",")
		o.reviewsSet = len(kept) > 0
	}
	if slices.Contains(splitNames(o.exclude), prompt.Suggest) {
		return nil, fmt.Errorf("%q is not a review name; it cannot be excluded", prompt.Suggest)
	}

	if once {
		if isFlagSet(fs, "max-loops", "n") {
			return nil, errors.New("--once conflicts with --max-loops")
		}
		o.maxLoops = 1
	}
	if o.maxLoops < 0 {
		return nil, errors.New("--max-loops must be >= 0")
	}
	if o.maxReviews < 0 {
		return nil, errors.New("--max-reviews must be >= 0")
	}
	if o.keepRuns < 0 {
		return nil, errors.New("--keep-runs must be >= 0")
	}
	if o.tokenBudget < 0 {
		return nil, errors.New("--token-budget must be >= 0")
	}
	if o.push {
		o.commit = true
	}
	if o.jobs < 1 {
		return nil, errors.New("--jobs must be >= 1")
	}
	if o.retries < 0 {
		return nil, errors.New("--retries must be >= 0")
	}
	if o.continueSessions && (o.jobs > 1 || o.stackedPRs) {
		return nil, errors.New("--continue-sessions cannot be used with --jobs > 1 or --stacked-prs: each review is a fresh worktree")
	}
	if err := trimFlag(fs, &o.dir, "dir", "C"); err != nil {
		return nil, err
	}
	if err := trimFlag(fs, &o.pushRemote, "push-remote"); err != nil {
		return nil, err
	}
	if isFlagSet(fs, "update-repo") && strings.TrimSpace(o.updateRepo) == "" {
		return nil, errors.New("--update-repo is empty: want owner/repo")
	}
	repo, err := selfupdate.ParseRepo(o.updateRepo)
	if err != nil {
		return nil, fmt.Errorf("--update-repo: %w", err)
	}
	o.updateRepo = repo
	if isFlagSet(fs, "dirs", "target-dirs") && len(dirs) == 0 {
		return nil, errors.New("--dirs is empty")
	}
	if err := trimFlag(fs, &o.mergeInto, "merge-into"); err != nil {
		return nil, err
	}
	if err := trimFlag(fs, &o.prBase, "pr-base"); err != nil {
		return nil, err
	}
	if err := trimFlag(fs, &o.showPrompt, "show-prompt"); err != nil {
		return nil, err
	}
	if o.stackedPRs {
		if o.commit || o.push {
			return nil, errors.New("--stacked-prs owns its commits and pushes; drop --commit/--push")
		}
		if o.mergeInto != "" {
			return nil, errors.New("--stacked-prs creates unmerged PRs and conflicts with --merge-into")
		}
		o.jobs = 1
		// Default stacked is one pass, matching the original. An explicit
		// --max-loops (including 0 = unlimited) is further isolated stacks,
		// each a fresh worktree cut from the previous pass's last tip.
		if !isFlagSet(fs, "max-loops", "n") && !once {
			o.maxLoops = 1
		}
		if !gitx.Available() {
			return nil, errors.New("--stacked-prs needs git")
		}
	} else {
		if isFlagSet(fs, "pr-base") {
			return nil, errors.New("--pr-base requires --stacked-prs")
		}
		if isFlagSet(fs, "push-remote") {
			return nil, errors.New("--push-remote requires --stacked-prs")
		}
	}
	if o.mergeInto != "" {
		// Only committed work can be merged, so the flag that produces the
		// commits is not optional here: without it the merge would report a
		// success that moved nothing the reviews wrote.
		if !o.commit {
			return nil, errors.New("--merge-into needs --commit (or --push): only committed work merges")
		}
		if !gitx.Available() {
			return nil, errors.New("--merge-into needs git")
		}
	}
	if o.jobs > 1 && !gitx.Available() {
		return nil, errors.New("--jobs > 1 needs git: each review runs in its own worktree")
	}
	if len(o.dirs) > 0 && isFlagSet(fs, "dir", "C") {
		return nil, errors.New("--dirs conflicts with --dir")
	}
	// Zero or negative would crash the index reader or report runs that exist
	// as absent.
	if o.runsLimit < 1 {
		return nil, errors.New("--limit must be >= 1")
	}

	// Checked in declaration order, not map order, so the error names the
	// conflicting modes the same way every time.
	modes := []struct {
		name string
		on   bool
	}{
		{"--list", o.list},
		{"--dry-run", o.dryRun},
		{"--show-prompt", o.showPrompt != ""},
	}
	var active []string
	for _, m := range modes {
		if m.on {
			active = append(active, m.name)
		}
	}
	if len(active) > 1 {
		return nil, fmt.Errorf("%s are mutually exclusive", strings.Join(active, " and "))
	}
	if o.tui && len(active) > 0 {
		return nil, fmt.Errorf("--tui conflicts with %s", active[0])
	}
	if o.tui && (!term.IsTerminal(int(os.Stdout.Fd())) || !stdinIsTerminal()) {
		// Both, the same gate cmdPick applies. A dashboard reads keys from
		// stdin, so a redirected one hands it EOF and the run quits on the
		// first tick: the reviews in flight die with nothing said on the way
		// out, which is the opposite of what --tui was asked for.
		return nil, errors.New("--tui needs a terminal on stdin and stdout; drop it to get plain log output")
	}
	if o.list {
		// Nothing is executed in list mode, so a commit step would be a lie.
		o.commit, o.push = false, false
	}
	if err := resolveSandboxWrites(o); err != nil {
		return nil, err
	}
	if isFlagSet(fs, "prompt-dir") {
		if err := trimFlag(fs, &o.promptDir, "prompt-dir"); err != nil {
			return nil, err
		}
		expanded, err := gauntlethome.ExpandPath(o.promptDir)
		if err != nil {
			return nil, fmt.Errorf("--prompt-dir: %w", err)
		}
		// A path that is not there is as much a bad flag value as one that is
		// there and is a file, so the missing half gets the same usage screen
		// as the wrong-type half. Letting discovery find it instead would
		// report the two mistakes a user can make with one flag differently,
		// and the missing one without one.
		fi, err := os.Stat(expanded)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("--prompt-dir %s: no such directory", expanded)
		case err != nil:
			return nil, fmt.Errorf("--prompt-dir %s: %w", expanded, err)
		case !fi.IsDir():
			return nil, fmt.Errorf("--prompt-dir %s: not a directory", expanded)
		}
		o.promptDir = expanded
	}
	// Same reasoning for --dir and --dirs: a path that is not a directory is
	// a flag value the parser refuses, so it is reported here rather than
	// from the run path, which prints the message without the screen.
	resolved, err := resolveDirs(o)
	if err != nil {
		return nil, err
	}
	o.resolvedDirs = resolved
	if err := validateLog(o, fs); err != nil {
		return nil, err
	}
	return o, nil
}

// checkStateHome refuses a GAUNTLET_HOME the state root cannot be built from.
//
// gauntlethome.Dir is the resolver every reader of the root goes through, so
// the check is made by asking it rather than by re-deriving the rule: a second
// copy of "what counts as usable" is one that answers differently the moment
// either is edited, and a startup that accepts a value the resolver then
// refuses is a mistyped variable that reads as a working setup and journals
// into a fallback directory instead. An unset or whitespace-only value is the
// variable unset and costs nothing to leave to $HOME; anything set is either
// usable now or is a usage error naming what it resolved to.
func checkStateHome() error {
	h := strings.TrimSpace(os.Getenv("GAUNTLET_HOME"))
	if h == "" {
		return nil
	}
	exp, err := gauntlethome.ExpandPath(h)
	if err != nil {
		return fmt.Errorf("GAUNTLET_HOME: %w", err)
	}
	root, ok := gauntlethome.Dir()
	if ok {
		return nil
	}
	if strings.HasPrefix(exp, "~") {
		// ExpandPath expands only "~/..." on purpose, leaving a bare "~" (and
		// another account's "~user") as it was written rather than guessing
		// whose home was meant. The state root has no reading of that: it
		// would resolve to a directory literally named "~" beside the tree
		// under review, so the answer names the spelling that does work.
		return fmt.Errorf("GAUNTLET_HOME %s: write ~%s to mean your home directory, not a directory called %q",
			exp, string(os.PathSeparator), exp)
	}
	// A root that exists and is not a directory is the common mistype, so it
	// gets its own message rather than the catch-all.
	if fi, err := os.Stat(exp); err == nil && !fi.IsDir() {
		return fmt.Errorf("GAUNTLET_HOME %s: not a directory", exp)
	}
	return fmt.Errorf("GAUNTLET_HOME %s: no usable state root; gauntlet would fall back to .gauntlet in the working directory", root)
}

func validateLog(o *options, fs *flag.FlagSet) error {
	if !isFlagSet(fs, "log") {
		return nil
	}
	o.logFile = strings.TrimSpace(o.logFile)
	if o.logFile == "" {
		return errors.New("--log is empty")
	}
	expanded, err := gauntlethome.ExpandPath(o.logFile)
	if err != nil {
		return fmt.Errorf("--log: %w", err)
	}
	if fi, err := os.Lstat(expanded); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("--log %s: is a symlink", expanded)
		}
		if fi.IsDir() {
			return fmt.Errorf("--log %s: is a directory", expanded)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("--log %s: not a regular file", expanded)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("--log %s: %w", expanded, err)
	} else if fi, err := os.Stat(filepath.Dir(expanded)); err != nil || !fi.IsDir() {
		// The file not existing yet is the normal case and openLogFile creates
		// it. Its parent existing is not, and the open is the only thing that
		// says so: without this the one --log value no other rejection covers
		// failed after the run had started, as a bare syscall message rather
		// than the usage error every other --log mistake gets.
		if err != nil {
			return fmt.Errorf("--log %s: %w", expanded, err)
		}
		return fmt.Errorf("--log %s: %s is not a directory", expanded, filepath.Dir(expanded))
	}
	o.logFile = expanded
	return nil
}

// usesAgents reports whether this command can launch or inspect agent definitions.
func (o *options) usesAgents() bool {
	return o.command == "" || o.command == "pick" || o.command == "doctor"
}

// needsAgents reports whether this invocation has to find a launchable agent
// CLI. --list and --show-prompt only read prompts, so they must work on a
// machine that has not installed one yet; doctor is how you find that out.
// --list --suggest uses the built-in file-signal suggester by default and
// needs a CLI only when an external suggestion agent is explicitly named.
func (o *options) needsAgents() bool {
	if o.showPrompt != "" {
		return false
	}
	if o.list && !o.suggest {
		return false
	}
	if o.list && o.suggestAgent != nil && o.suggestAgent.Tool == evidence.AgentName {
		return false
	}
	return true
}

// subcommandFlags names the flags each subcommand actually reads, on top of
// the global ones. The default run (no subcommand) is not listed: it reads
// them all.
var subcommandFlags = map[string][]string{
	"pick":   {"C", "dir", "dirs", "target-dirs", "prompt-dir"},
	"doctor": {"agent-cmd", "bin"},
	"update": {"check", "update-repo"},
	"runs":   {"limit", "restore", "json"},
	"show":   {},
	"resume": {},
	// help is handled in finishFlags before stray-flag checks, so extra
	// flags are ignored the way they are after --help. The entry exists so
	// a later check cannot treat `help` as the default run.
	"help": {},
	// The -V flag form keeps its "wins over scoping" reading below; the
	// subcommand word is held to the same discipline as the rest.
	"version": {},
}

// globalFlags are honored no matter the command: help, version, color, and
// the output tee, which run() wires up before dispatching anywhere.
var globalFlags = []string{"h", "help", "V", "version", "log", "no-color"}

// rejectStrayFlags refuses a flag a named subcommand would parse and then
// drop: `gauntlet runs --jobs 4` must fail loudly rather than print its
// table while ignoring the concurrency it was given. The flag is named the
// way it was spelled, so `-j 4` reads as -j.
//
// The -V flag form is exempt: it means "print the version and exit" and wins
// over scoping the way help does, so `gauntlet -V --limit 5` still prints the
// version. The `version` subcommand word gets no such pass.
func rejectStrayFlags(o *options, fs *flag.FlagSet, showVersion bool) error {
	if showVersion {
		return nil
	}
	allowed, known := subcommandFlags[o.command]
	if !known {
		return nil // the default run path reads every flag
	}
	stray := ""
	fs.Visit(func(f *flag.Flag) {
		if stray != "" || slices.Contains(globalFlags, f.Name) || slices.Contains(allowed, f.Name) {
			return
		}
		stray = f.Name
	})
	if stray == "" {
		return nil
	}
	takes := spellFlags(allowed)
	if takes == "" {
		takes = "no flags of its own"
	}
	return fmt.Errorf("%s does not apply to 'gauntlet %s', which takes %s",
		spellFlag(stray), o.command, takes)
}

// trimFlag trims a string flag's value in place and refuses an explicitly
// empty one, so `--dir " "` cannot pass as a directory. Every registered name
// for the flag goes in names, shorthand included, since an explicit value is
// what makes it a request rather than the default.
func trimFlag(fs *flag.FlagSet, dst *string, names ...string) error {
	v := strings.TrimSpace(*dst)
	if v == "" {
		if isFlagSet(fs, names...) {
			return fmt.Errorf("%s is empty", spellFlag(names[0]))
		}
		return nil
	}
	*dst = v
	return nil
}

// resolveSandboxWrites validates a --sandbox-write grant and expands it.
//
// The runner resolves a relative grant against each reviewed worktree, so only
// a path that is already absolute once ~ and $VARIABLES expand can be checked
// here; a relative one is left for the sandbox builder, which knows the
// worktree. But the help screen and docs/CLI.md both promise the directory
// exists, and a grant that does not is otherwise discovered once per review,
// after the lock is taken and the agents have been launched, reported as a
// failed review (exit 1) rather than as the usage error every other path flag
// gets. A mistyped grant is a usage error, and the parse is the only place
// that can still be cheap about it.
//
// Expanding here also means the runner, the per-worktree resolution, and the
// help screen all read the same string.
func resolveSandboxWrites(o *options) error {
	for i, raw := range o.sandboxWrite {
		p, err := gauntlethome.ExpandPath(raw)
		if err != nil {
			return fmt.Errorf("--sandbox-write %s: %w", raw, err)
		}
		if !filepath.IsAbs(p) {
			// Resolved against each worktree at launch; the sandbox builder
			// is the one that can see them.
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("--sandbox-write %s: %w", p, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("--sandbox-write: not a directory: %s", p)
		}
		o.sandboxWrite[i] = p
	}
	return nil
}

// takesNextArg reports whether the flag called name consumes the following
// argument as its value. An attached value (-j3, --log=FILE) owns nothing
// after it, and so does a boolean.
func takesNextArg(fs *flag.FlagSet, name string, attached bool) bool {
	if attached {
		return false
	}
	f := fs.Lookup(name)
	return f != nil && !isBoolFlag(f)
}

func isFlagSet(fs *flag.FlagSet, names ...string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		for _, n := range names {
			if f.Name == n {
				set = true
			}
		}
	})
	return set
}

// countedString is a string flag that remembers how many times it was given.
// The flag package records the last value of a repeated flag and forgets the
// rest, so a flag whose repetition is a mistake rather than a choice needs its
// own count: the value is still the last one, so nothing else changes.
type countedString struct {
	dst *string
	n   int
}

func (c *countedString) String() string {
	if c.dst == nil {
		return ""
	}
	return *c.dst
}

func (c *countedString) Set(v string) error {
	c.n++
	*c.dst = v
	return nil
}

func splitNames(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w >= minTerminalWidth {
		return w
	}
	return defaultTerminalWidth
}

// expandAttachedValues rewrites `-j3` into `-j 3`. The flag package takes only
// `-j 3` and `-j=3`, but every tool that has ever had a `-j` also takes it glued
// on, so the habit carried in from make or tar reads as an unknown flag here.
// Only single-letter flags that want a value are split; booleans, long forms,
// and anything past `--` or the first positional arrive as they were typed.
func expandAttachedValues(fs *flag.FlagSet, argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			return append(out, argv[i:]...)
		}
		name := strings.TrimLeft(arg, "-")
		if strings.ContainsRune(name, '=') {
			out = append(out, arg)
			continue
		}
		if arg[1] != '-' && len(name) > 1 && name[0] < utf8.RuneSelf {
			if f := fs.Lookup(name[:1]); f != nil && !isBoolFlag(f) {
				out = append(out, arg[:2], arg[2:])
				continue
			}
		}
		out = append(out, arg)
		// A flag that takes a value owns the next argument, whatever it
		// looks like: it is not the positional that ends the flags.
		if takesNextArg(fs, name, false) && i+1 < len(argv) {
			i++
			out = append(out, argv[i])
		}
	}
	return out
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// peelSubcommand pulls a subcommand word off argv. It may sit first, or after
// only global flags, so `gauntlet --no-color doctor` is the same command as
// `gauntlet doctor --no-color`. A non-global flag stops the scan: then this
// is the default run, and a later word is an unexpected argument.
func peelSubcommand(argv []string) (cmd string, rest []string) {
	if len(argv) == 0 {
		return "", nil
	}
	if argv[0] != "" && !strings.HasPrefix(argv[0], "-") {
		return argv[0], argv[1:]
	}
	dummy, _ := buildFlagSet(&options{})
	argv = expandAttachedValues(dummy, argv)
	i := 0
	for i < len(argv) {
		a := argv[i]
		if a == "--" || len(a) < 2 || a[0] != '-' {
			break
		}
		name := strings.TrimLeft(a, "-")
		name, _, attached := strings.Cut(name, "=")
		if !slices.Contains(globalFlags, name) {
			break
		}
		i++
		if takesNextArg(dummy, name, attached) && i < len(argv) {
			i++
		}
	}
	if i < len(argv) && argv[i] != "" && !strings.HasPrefix(argv[i], "-") {
		cmd = argv[i]
		rest = make([]string, 0, len(argv)-1)
		rest = append(rest, argv[:i]...)
		rest = append(rest, argv[i+1:]...)
		return cmd, rest
	}
	return "", argv
}

// peelShowRun takes the first non-flag word as the run id and leaves every
// flag, before or after it, for Parse. `gauntlet show --no-color RUN` and
// `gauntlet show RUN --no-color` then mean the same thing.
func peelShowRun(fs *flag.FlagSet, argv []string) (id string, rest []string) {
	argv = expandAttachedValues(fs, argv)
	rest = make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			rest = append(rest, argv[i:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			rest = append(rest, a)
			name := strings.TrimLeft(a, "-")
			name, _, attached := strings.Cut(name, "=")
			if takesNextArg(fs, name, attached) && i+1 < len(argv) {
				i++
				rest = append(rest, argv[i])
			}
			continue
		}
		if id == "" {
			id = a
			continue
		}
		rest = append(rest, a)
	}
	return id, rest
}

func unknownCommand(name string) error {
	hint := ""
	if c := fuzzy.Closest(name, commandNames); c != "" {
		hint = fmt.Sprintf(" (did you mean %q?)", c)
	}
	return fmt.Errorf("unknown command: %q%s (try: %s)",
		name, hint, strings.Join(commandNames, ", "))
}

// helpTopic refuses a word after `help` or -h that names nothing, with the
// same shape the first-word unknown command has. The screen covers every
// command at once, so a known one is answered by it and only a misspelling
// needs saying.
func helpTopic(word string) error {
	if slices.Contains(commandNames, word) {
		return nil
	}
	hint := ""
	if c := fuzzy.ClosestWithin(word, commandNames, typoDistance(word)); c != "" {
		hint = fmt.Sprintf(" (did you mean %q?)", c)
	}
	return fmt.Errorf("no help topic: %q%s (topics: %s)",
		word, hint, strings.Join(commandNames, ", "))
}

// flagSpelling matches the flag name in each message the flag package writes
// when a flag is unknown, missing its value, or given a value it cannot parse.
var flagSpelling = regexp.MustCompile(`(defined: |argument: |for flag )-([A-Za-z0-9][A-Za-z0-9-]*)`)

// spellFlag names a flag the way the help screen and every message of this
// CLI's own do: two dashes for a long form, one for a shorthand.
func spellFlag(name string) string {
	if utf8.RuneCountInString(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// spellFlags names a set of flags the same way, each under its own long form
// where it has one, so a reader is never handed `-C` and left guessing that
// `--dir` is the other spelling. It is the list the help screen's subcommand
// section and the stray-flag refusal both print.
func spellFlags(names []string) string {
	// A shorthand and the long name it is registered beside are one flag, so
	// the pair is written as `-C/--dir` and the long name is not repeated.
	paired := map[string]bool{}
	for _, n := range names {
		for _, g := range helpGroups {
			for _, f := range g.Flags {
				if f.Short == n && slices.Contains(names, f.Long) {
					paired[f.Long] = true
				}
			}
		}
	}
	spelled := make([]string, 0, len(names))
	for _, n := range names {
		if paired[n] {
			continue
		}
		spelled = append(spelled, spellFlag(n)+longForm(n))
	}
	return strings.Join(spelled, ", ")
}

// longForm is the "/--dir" beside a shorthand, empty for a flag registered
// under its long name only. The pairs are the help table's, which a test holds
// against the registered flags, so neither this nor the screen can fall behind
// the parser.
func longForm(short string) string {
	if utf8.RuneCountInString(short) != 1 {
		return ""
	}
	for _, g := range helpGroups {
		for _, f := range g.Flags {
			if f.Short == short {
				return "/--" + f.Long
			}
		}
	}
	return ""
}

// enhanceFlagError rewrites the flag package's own failure messages. The
// package names every flag with a single dash, whatever length it is, so
// `--timeout` comes back as `-timeout`: a spelling no flag on this CLI has,
// next to messages that write the same flag as `--timeout`. A close miss keeps
// the package's wording, with the spelling fixed, and gains the suggestion
// the unknown-command error has.
//
// The suggestion comes from the flags the command actually reads. Sending a
// `gauntlet runs` typo at --jobs, which rejectStrayFlags then refuses, spends
// two invocations to learn the one thing the first message could have said.
func enhanceFlagError(err error, o *options, fs *flag.FlagSet) error {
	msg := flagSpelling.ReplaceAllStringFunc(err.Error(), func(m string) string {
		g := flagSpelling.FindStringSubmatch(m)
		return g[1] + spellFlag(g[2])
	})
	const prefix = "flag provided but not defined: -"
	name, ok := strings.CutPrefix(err.Error(), prefix)
	// A one-letter miss is one substitution from every short flag; guessing
	// `-1` for `-Z` is noise.
	if !ok || utf8.RuneCountInString(name) < 2 {
		return errors.New(msg)
	}
	c := fuzzy.ClosestWithin(name, readableFlags(o, fs), typoDistance(name))
	if c == "" {
		return errors.New(msg)
	}
	return fmt.Errorf("%s (did you mean %s?)", msg, spellFlag(c))
}

// typoDistance is how far a name may sit from a candidate and still read as a
// typo of it: half the name, never less than one edit and never more than the
// fuzzy package's shared ceiling. A name is short, so the ceiling on its own
// would turn "limt" into "--log", and it would turn "vers" into "help" -- both
// guesses at something the reader did not half-write.
func typoDistance(name string) int {
	return min(max(utf8.RuneCountInString(name)/2, 1), fuzzy.Limit)
}

// misplacedArgument explains a stray word left where a subcommand should have
// been read. A subcommand is only one when it is the first word, so a flag
// that is not global ends the search and `gauntlet --json runs` arrives here
// with "runs" as a leftover. Naming the leftover alone points the reader at
// the wrong end of the line, so the order is what the message says.
func misplacedArgument(word string) error {
	if !slices.Contains(commandNames, word) {
		return fmt.Errorf("unexpected argument: %q (see: gauntlet help)", word)
	}
	return fmt.Errorf("unexpected argument: %q (a subcommand's own flags go after "+
		"it: gauntlet %s ...) (see: gauntlet help)", word, word)
}

// readableFlags lists the flags a "did you mean" may point at: every one for
// the default run, and for a subcommand the ones it reads itself plus the
// globals it honors anywhere. It is the same list the screen and
// rejectStrayFlags name, so a hint never points outside it.
func readableFlags(o *options, fs *flag.FlagSet) []string {
	allowed, known := subcommandFlags[o.command]
	if !known {
		var names []string
		fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
		return names
	}
	names := slices.Clone(globalFlags)
	for _, n := range allowed {
		if fs.Lookup(n) != nil {
			names = append(names, n)
		}
	}
	return names
}
