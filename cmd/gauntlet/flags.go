// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
	"golang.org/x/term"
)

// errHelp means help was requested; run prints it on stdout and exits 0.
var errHelp = errors.New("help requested")

// parseError marks a failure whose message and usage screen have already been
// written to stderr, by the flag package or by reportUsage. run must not
// print either again.
type parseError struct{ err error }

func (e parseError) Error() string { return e.err.Error() }

// reportUsage writes the message and the help screen to stderr, mirroring how
// the flag package reports its own failures, and returns the error marked as
// reported. Every rejection from parsing goes through this, so any bad
// invocation reads the same way: cause first, then the screen.
func reportUsage(o *options, err error) error {
	fmt.Fprintln(os.Stderr, err)
	printUsage(os.Stderr, palette{on: colorEnabled(os.Stderr) && !o.noColor}, o.width)
	return parseError{err}
}

type options struct {
	command string // "", doctor, update, runs, show, version

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

	// agents
	agents []agent.Spec
	bin    map[string]string

	// execution
	dir              string
	dirs             []string
	timeout          time.Duration
	runtime          time.Duration
	jobs             int
	retries          int
	mergeInto        string
	resolveConflicts bool
	maxLoops         int
	seed             uint64
	commit           bool
	push             bool
	stackedPRs       bool
	prBase           string
	pushRemote       string
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
	runsLimit int
	showRun   string
}

// listFlag collects a repeatable, comma-separated flag.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for part := range strings.SplitSeq(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*l = append(*l, p)
		}
	}
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
	var d time.Duration
	var err error
	if f.allowZero {
		d, err = humanize.ParseDurationAllowZero(v)
	} else {
		d, err = humanize.ParseDuration(v)
	}
	if err != nil {
		return err
	}
	*f.d = d
	return nil
}

// seedFlag accepts any Go integer literal, base 0 like the flag package's own
// uint64 value (decimal, 0x hex, underscores), and rejects everything else
// with the requirement spelled out rather than a bare "parse error".
type seedFlag struct{ d *uint64 }

func (f seedFlag) String() string {
	if f.d == nil {
		return ""
	}
	return strconv.FormatUint(*f.d, 10)
}

func (f seedFlag) Set(v string) error {
	n, err := strconv.ParseUint(v, 0, 64)
	if err != nil {
		return errors.New("must be a nonnegative integer (decimal or 0x hex)")
	}
	*f.d = n
	return nil
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
)

func parseFlags(argv []string) (*options, error) {
	o := &options{
		bin: map[string]string{}, timeout: defaultTimeout,
		suggestTimeout: defaultTimeout, jobs: 1, retries: defaultRetries,
		hotReload: true, stream: true,
		runsLimit: defaultRunsLimit, width: terminalWidth(),
	}

	// Subcommands come first and take their own small flag sets.
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		switch argv[0] {
		case "doctor", "update", "runs", "show", "version", "pick":
			o.command = argv[0]
			argv = argv[1:]
			if o.command == "show" {
				if len(argv) > 0 && (argv[0] == "-h" || argv[0] == "--help") {
					printUsage(os.Stdout, palette{on: colorEnabled(os.Stdout)}, o.width)
					return nil, errHelp
				}
				// A run id never starts with '-', so a flag there means the
				// id was forgotten, not that a run is named "--limit".
				if len(argv) == 0 || strings.HasPrefix(argv[0], "-") {
					return nil, reportUsage(o, errors.New("show needs a run id (see: gauntlet runs)"))
				}
				o.showRun, argv = argv[0], argv[1:]
			}
		case "help":
			// The word every other CLI answers to. Anything after it is
			// ignored: this screen is the same for every command.
			printUsage(os.Stdout, palette{on: colorEnabled(os.Stdout)}, o.width)
			return nil, errHelp
		default:
			return nil, reportUsage(o, fmt.Errorf(
				"unknown command: %q (try: help, pick, doctor, update, runs, show, version)", argv[0]))
		}
	}

	fs, raw := buildFlagSet(o)
	if err := fs.Parse(expandAttachedValues(fs, argv)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, errHelp
		}
		return nil, parseError{err}
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
	agentCmds                            listFlag
	suggestAgent                         string
	suggest, once, showVersion, help     bool
}

// buildFlagSet registers every flag. It is separate from parsing so a test can
// enumerate the flags and compare them against the help screen.
func buildFlagSet(o *options) (*flag.FlagSet, *rawFlags) {
	raw := &rawFlags{}
	reviews, exclude, agents, bins, dirs := &raw.reviews, &raw.exclude, &raw.agents, &raw.bins, &raw.dirs
	agentCmds := &raw.agentCmds
	suggestAgent, suggest, once, showVersion := &raw.suggestAgent, &raw.suggest, &raw.once, &raw.showVersion
	help := &raw.help

	fs := flag.NewFlagSet("gauntlet", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		// A --no-color seen before the error applies here too, matching how
		// -h renders its screen.
		printUsage(os.Stderr, palette{on: colorEnabled(os.Stderr) && !o.noColor}, o.width)
	}

	alias := func(short, long string, register func(name string)) {
		register(long)
		if short != "" {
			register(short)
		}
	}

	alias("r", "reviews", func(n string) {
		fs.Var(reviews, n, "reviews and/or sets to run (comma-separated, repeatable); "+
			"repeats add weight, 'suggest' adds an agent's picks")
	})
	alias("x", "exclude", func(n string) { fs.Var(exclude, n, "reviews and/or sets to skip") })
	alias("s", "suggest", func(n string) {
		fs.BoolVar(suggest, n, false, "have an agent pick the reviews, beside any named with --reviews")
	})
	fs.StringVar(suggestAgent, "suggest-agent", "",
		"agent to run the suggest step, or 'gauntlet' to pick from file signals instead")
	fs.Var(durationFlag{d: &o.suggestTimeout}, "suggest-timeout",
		fmt.Sprintf("timeout for the suggest step (default %dm)", int(defaultTimeout/time.Minute)))
	fs.StringVar(&o.promptDir, "prompt-dir", "", "directory of *-review.md files (default: the bundled set)")

	alias("a", "agents", func(n string) {
		fs.Var(agents, n, "agent CLIs, optionally agent:model; 'mixed' means every installed agent")
	})
	fs.Var(bins, "bin", "run an agent from a specific executable, TOOL=PATH (repeatable)")
	fs.Var(agentCmds, "agent-cmd", "define an agent: NAME=ARGV with a {prompt} placeholder (repeatable)")

	alias("C", "dir", func(n string) { fs.StringVar(&o.dir, n, ".", "directory to review") })
	fs.Var(dirs, "dirs", "review several directories in parallel, each with its own --jobs pool (repeatable, comma-separated)")
	// The name the Python tool used. Scripts that pass it keep working, but it
	// takes a comma-separated list here rather than space-separated arguments.
	fs.Var(dirs, "target-dirs", "alias of --dirs")
	alias("t", "timeout", func(n string) {
		fs.Var(durationFlag{d: &o.timeout}, n,
			fmt.Sprintf("per-review timeout (default %dm)", int(defaultTimeout/time.Minute)))
	})
	fs.Var(durationFlag{&o.runtime, true}, "runtime", "wall-clock budget for the whole run (0 = unlimited)")
	alias("j", "jobs", func(n string) {
		fs.IntVar(&o.jobs, n, 1, "reviews to run at once per directory; >1 gives each its own git worktree and merges back")
	})
	fs.IntVar(&o.retries, "retries", defaultRetries, "reruns of a failed review on the same agent, waiting longer each time (0 = none)")
	alias("n", "max-loops", func(n string) { fs.IntVar(&o.maxLoops, n, 0, "stop after N loops (0 = unlimited)") })
	fs.Var(seedFlag{&o.seed}, "seed", "RNG seed for review order and agent picks; recorded in the journal (0 = random)")
	alias("1", "once", func(n string) { fs.BoolVar(once, n, false, "run a single loop and exit") })
	alias("c", "commit", func(n string) { fs.BoolVar(&o.commit, n, false, "commit after each review") })
	alias("p", "push", func(n string) { fs.BoolVar(&o.push, n, false, "commit and push after each review") })
	fs.StringVar(&o.mergeInto, "merge-into", "",
		"after each loop, merge this branch's committed work into BRANCH")
	fs.BoolVar(&o.stackedPRs, "stacked-prs", false,
		"run reviews sequentially in one worktree and open a linear PR stack")
	fs.StringVar(&o.prBase, "pr-base", "",
		"remote base branch fetched for --stacked-prs (default: current branch name)")
	fs.StringVar(&o.pushRemote, "push-remote", "origin",
		"Git remote that receives --stacked-prs branches")
	fs.BoolVar(&o.resolveConflicts, "resolve-conflicts", true,
		"hand a review branch that will not merge to an agent to resolve "+
			"(--resolve-conflicts=false keeps it for a human)")
	fs.BoolVar(&o.yolo, "yolo", false, "drop the caution rules: bigger, more ambitious changes")
	alias("y", "yes", func(n string) { fs.BoolVar(&o.yes, n, false, "answer yes to confirmation prompts") })
	fs.BoolVar(&o.semcode, "semcode", false, "build a semcode index before the loop")
	fs.BoolVar(&o.continueSessions, "continue-sessions", false, "resume each agent's session between reviews")

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
	fs.StringVar(&o.updateRepo, "update-repo", "", "GitHub repo to fetch releases from")
	fs.BoolVar(&o.checkOnly, "check", false, "update: report the latest release without installing")
	alias("V", "version", func(n string) { fs.BoolVar(showVersion, n, false, "print the version and exit") })
	alias("h", "help", func(n string) { fs.BoolVar(help, n, false, "show this help and exit") })
	fs.IntVar(&o.runsLimit, "limit", defaultRunsLimit, "runs: how many entries to list")

	return fs, raw
}

// finishFlags turns parsed values into validated options.
func finishFlags(o *options, fs *flag.FlagSet, raw *rawFlags) (*options, error) {
	reviews, exclude, agents := raw.reviews, raw.exclude, raw.agents
	bins, dirs, agentCmds := raw.bins, raw.dirs, raw.agentCmds
	suggestAgent, suggest, once, showVersion := raw.suggestAgent, raw.suggest, raw.once, raw.showVersion

	// Help wins over every other flag and validation: an explicitly requested
	// screen belongs on stdout, where redirection and pipes can capture it.
	if raw.help {
		printUsage(os.Stdout, palette{on: colorEnabled(os.Stdout) && !o.noColor}, o.width)
		return nil, errHelp
	}

	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument: %q (see: gauntlet help)", fs.Arg(0))
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
	}
	if err := rejectStrayFlags(o, fs, raw.showVersion); err != nil {
		return nil, err
	}

	// A file of definitions first, then the command line, which wins.
	if path := agent.CustomFilePath(); path != "" {
		if err := agent.LoadCustomFile(path); err != nil {
			return nil, err
		}
	}
	// A repeated --agent-cmd with a different definition is a typo, not a
	// choice: the last one would silently win, which is how --bin came to
	// refuse its own duplicates. The file loaded above is still overridden on
	// purpose — the command line wins for its run — so this counts only what
	// the command line itself said. Checking every entry before registering
	// any also keeps a bad list from half-defining agents.
	type namedDef struct {
		raw  string
		def  agent.Custom
		name string
	}
	defs := make([]namedDef, 0, len(agentCmds))
	cmdDefs := map[string]string{}
	for _, c := range agentCmds {
		name, def, err := agent.ParseAgentCmd(c)
		if err != nil {
			return nil, fmt.Errorf("--agent-cmd %s: %w", c, err)
		}
		if prev, dup := cmdDefs[name]; dup && prev != c {
			return nil, fmt.Errorf("--agent-cmd given twice for %s: %s and %s", name, prev, c)
		}
		cmdDefs[name] = c
		defs = append(defs, namedDef{raw: c, def: def, name: name})
	}
	for _, d := range defs {
		if err := agent.Register(d.name, d.def); err != nil {
			return nil, fmt.Errorf("--agent-cmd %s: %w", d.raw, err)
		}
	}
	if o.openCodeDB && !enableOpenCodeDB() {
		return nil, errors.New("--opencode-db needs a build with -tags sqlite")
	}
	// A definition may say where its agent keeps transcripts, which is what
	// gives a non-built-in agent live token counts.
	for _, name := range agent.CustomNames() {
		def, ok := agent.CustomDef(name)
		if !ok || def.Usage == nil {
			continue
		}
		if err := registerTranscript(name, def.Usage); err != nil {
			return nil, err
		}
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
	if len(agents) > 0 {
		specs, err := agent.ParseSpecs(strings.Join(agents, ","))
		if err != nil {
			return nil, err
		}
		o.agents = specs
	}
	if suggestAgent == runner.FastSuggestAgent {
		// Not an agent: gauntlet itself, reading the tree for signals.
		o.suggestAgent = &agent.Spec{Tool: runner.FastSuggestAgent}
	} else if suggestAgent != "" {
		specs, err := agent.ParseSpecs(suggestAgent)
		if err != nil {
			return nil, err
		}
		if len(specs) != 1 {
			return nil, errors.New("--suggest-agent takes exactly one agent (tool or tool:model)")
		}
		o.suggestAgent = &specs[0]
	}

	// An omitted --reviews means all. An explicit empty value (--reviews '')
	// must not silently expand to everything: that is how a script wipes a
	// repo by accident.
	o.reviewsSet = isFlagSet(fs, "reviews", "r")
	o.reviews = strings.Join(reviews, ",")
	o.exclude = strings.Join(exclude, ",")
	o.dirs = dirs

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
		if o.maxLoops != 0 {
			return nil, errors.New("--once conflicts with --max-loops")
		}
		o.maxLoops = 1
	}
	if o.maxLoops < 0 {
		return nil, errors.New("--max-loops must be >= 0")
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
	if o.stackedPRs {
		if o.commit || o.push {
			return nil, errors.New("--stacked-prs owns its commits and pushes; drop --commit/--push")
		}
		if o.mergeInto != "" {
			return nil, errors.New("--stacked-prs creates unmerged PRs and conflicts with --merge-into")
		}
		if o.maxLoops > 1 {
			return nil, errors.New("--stacked-prs runs one ordered review pass; --max-loops cannot exceed 1")
		}
		o.jobs, o.maxLoops = 1, 1
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
	if o.tui && !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, errors.New("--tui needs a terminal; drop it to get plain log output")
	}
	if o.list {
		// Nothing is executed in list mode, so a commit step would be a lie.
		o.commit, o.push = false, false
	}
	if o.promptDir != "" {
		o.promptDir = gauntlethome.ExpandPath(o.promptDir)
	}
	if o.logFile != "" {
		o.logFile = gauntlethome.ExpandPath(o.logFile)
	}
	return o, nil
}

// subcommandFlags names the flags each subcommand actually reads, on top of
// the global ones. The default run (no subcommand) is not listed: it reads
// them all.
var subcommandFlags = map[string][]string{
	"pick":   {"C", "dir", "dirs", "target-dirs", "prompt-dir"},
	"doctor": {"agent-cmd", "bin"},
	"update": {"check", "update-repo"},
	"runs":   {"limit"},
	"show":   {},
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
	spell := func(name string) string {
		if len(name) == 1 {
			return "-" + name
		}
		return "--" + name
	}
	takes := make([]string, 0, len(allowed))
	for _, n := range allowed {
		takes = append(takes, spell(n))
	}
	if len(takes) == 0 {
		takes = append(takes, "no flags of its own")
	}
	return fmt.Errorf("%s does not apply to 'gauntlet %s', which takes %s",
		spell(stray), o.command, strings.Join(takes, ", "))
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
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w >= 60 {
		return w
	}
	return 100
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
		if arg[1] != '-' && len(name) > 1 {
			if f := fs.Lookup(name[:1]); f != nil && !isBoolFlag(f) {
				out = append(out, arg[:2], arg[2:])
				continue
			}
		}
		out = append(out, arg)
		// A flag that takes a value owns the next argument, whatever it
		// looks like: it is not the positional that ends the flags.
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(argv) {
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
