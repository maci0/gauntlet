// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command gauntlet runs a codebase through ~50 specialized review prompts,
// dispatched to whichever AI coding agents are installed.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
	"github.com/maci0/gauntlet/internal/ui"
)

// version is stamped at build time: go build -ldflags "-X main.version=1.2.3".
// A build that stamps nothing keeps the source default, and then the version
// the Go toolchain embedded is read instead, so `go install ...@v1.2.3`
// reports 1.2.3 rather than dev. Resolution happens in main, not here: -X
// can only replace variables whose initializer is a constant, and this file
// must stay one for release stamping to reach every display surface.
var version = "dev"

func main() {
	version = resolveVersion(version)
	os.Exit(run(os.Args[1:]))
}

// resolveVersion returns the stamped version unless nothing was stamped, in
// which case it falls back to the module version the build recorded.
func resolveVersion(stamped string) string {
	bi, _ := debug.ReadBuildInfo()
	return resolveStamped(stamped, bi)
}

// resolveStamped keeps an explicit -ldflags stamp untouched. A toolchain build
// without one records "(devel)" for an out-of-module build, possibly nothing,
// and since go1.27 its own tag plus "+dirty" for a working-tree build; the
// first two stay dev, everything else names the tag honestly rather than
// inventing a number an update check could act on.
func resolveStamped(stamped string, bi *debug.BuildInfo) string {
	if stamped != "dev" || bi == nil || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return stamped
	}
	return strings.TrimPrefix(bi.Main.Version, "v")
}

// Exit codes, matching the Python original so scripts keep working.
const (
	exitOK     = 0
	exitFail   = 1  // a review failed, timed out, was skipped, would not merge, or a commit step failed
	exitUsage  = 2  // usage error
	exitLocked = 75 // EX_TEMPFAIL: another instance holds the lock
)

// autoUpdateEvery is how often --auto-update asks GitHub for a new release.
// Deliberately slow: an update check is never on the critical path.
const autoUpdateEvery = 6 * time.Hour

// autoUpdateDelay is how long --auto-update waits before its first check, so
// the network never delays the first review.
const autoUpdateDelay = 30 * time.Second

// dirRun is one directory's slice of a run.
type dirRun struct {
	dir     string
	set     prompt.Set
	reviews []string
	lock    *runner.Lock
	r       *runner.Runner
	stats   *runner.Stats
	loops   int
	// carriedLoops are the loops this directory finished in an earlier
	// process, before a hot reload handed the run over.
	carriedLoops int
	// prep and snapshot exist only in stacked mode: the preflight already run
	// for this directory, and the checkout of its pinned base commit that
	// prompt discovery and the suggest step read instead of the user's tree.
	prep     *runner.StackPrep
	repo     *gitx.Repo
	snapshot *gitx.Worktree
}

// scanDir is where this directory's prompts and suggestion signals are read.
// A stacked run reads the snapshot of the fetched remote base, so uncommitted
// or local-only files cannot steer a run that publishes remote-based work.
func (d *dirRun) scanDir() string {
	if d.snapshot != nil {
		return d.snapshot.Dir
	}
	return d.dir
}

// handoff is the state a hot reload carries across the exec. It holds the
// full results, not just counters: the successor prints the run's summary and
// writes its index row, and both must describe the whole run.
type handoff struct {
	RunID     string    `json:"run_id"`
	StartedAt time.Time `json:"started_at"`
	// Elapsed is how long the run had been going when this handoff was
	// written, measured on the predecessor's monotonic clock. The successor
	// reconstructs a start from it so --runtime and the dashboard do not
	// jump when the wall clock steps (NTP, a manual set) during the exec.
	// Omitempty keeps an old handoff that lacks the field readable: resume
	// then falls back to the wall-clock difference from StartedAt.
	Elapsed time.Duration         `json:"elapsed,omitempty"`
	Reloads int                   `json:"reloads"`
	Dirs    map[string]dirHandoff `json:"dirs"`
}

// dirHandoff is one directory's progress at the moment of the swap.
type dirHandoff struct {
	Loops int `json:"loops"`
	// Pending is what the interrupted loop had not started yet, so the
	// successor finishes that loop instead of starting it over.
	Pending []string `json:"pending,omitempty"`
	// Reviews is the schedule this directory resolved, so a successor
	// continues the same run instead of deciding again: a suggest step would
	// ask an agent (and the user) a second time, and a launcher-composed run
	// would open the launcher.
	Reviews     []string        `json:"reviews,omitempty"`
	CommitRuns  int             `json:"commit_runs,omitempty"`
	CommitFails int             `json:"commit_fails,omitempty"`
	Results     []runner.Result `json:"results,omitempty"`
	// StackBase and StackBaseTip pin a stacked run to the exact remote base
	// commit its first process fetched. The successor resumes from this
	// commit rather than fetching a tip that may have advanced, which would
	// rename every layer and split the run into a new stack.
	StackBase    string `json:"stack_base,omitempty"`
	StackBaseTip string `json:"stack_base_tip,omitempty"`
}

// Loops totals the loops finished before the reload.
func (h handoff) Loops() int {
	n := 0
	for _, d := range h.Dirs {
		n += d.Loops
	}
	return n
}

// resumeStart reconstructs a start time that carries this process's
// monotonic clock, offset by the elapsed the predecessor already measured.
// time.Since of the result equals that elapsed plus whatever this process
// then spends, even if the wall clock stepped during the exec.
//
// An old handoff with no elapsed field falls back to the wall-clock span
// from StartedAt, which is what those binaries already measured against.
func resumeStart(now time.Time, prior handoff) time.Time {
	elapsed := prior.Elapsed
	if elapsed == 0 && !prior.StartedAt.IsZero() {
		elapsed = now.Sub(prior.StartedAt)
	}
	return now.Add(-elapsed)
}

func run(argv []string) int {
	opts, err := parseFlags(argv)
	if err != nil {
		if errors.Is(err, errHelp) {
			return exitOK
		}
		if _, ok := errors.AsType[parseError](err); !ok {
			fmt.Fprintln(os.Stderr, err)
		}
		return exitUsage
	}

	stdout := io.Writer(os.Stdout)
	pal := palette{on: colorEnabled(os.Stdout) && !opts.noColor}

	// The launcher and the dashboard draw through lipgloss, which sees
	// NO_COLOR but not this flag; hand the request over before either draws.
	if opts.noColor {
		ui.SetMonochrome()
	}

	// --log tees every stream this process writes. Native multi-writer, not a
	// tee subprocess: one less dependency and no broken-pipe failure mode.
	//
	// 0600, like the journal: the file captures agent output, and reviews
	// quote what they find in the target tree, credentials included. On a
	// multi-user host that must not land world-readable.
	var logWriter io.Writer
	if opts.logFile != "" {
		f, err := os.OpenFile(opts.logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot write log file %s: %v\n", opts.logFile, err)
			return exitUsage
		}
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: closing log file %s: %v\n", opts.logFile, err)
			}
		}()
		logWriter = f
		stdout = io.MultiWriter(os.Stdout, f)
		pal.on = false // escape codes would land in the file too
	}

	// Run-control messages ("Finishing: …", signal receipts) must not fight
	// the dashboard for a screen it owns: a raw write into the alt screen
	// leaves stray text across the frame. While --tui is up, the header's
	// FINISHING label carries the news, and --log keeps the words.
	runCtl := io.Writer(stdout)
	if opts.tui {
		if logWriter != nil {
			runCtl = logWriter
		} else {
			runCtl = io.Discard
		}
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	graceful := &gracefulStop{}
	watchSignals(ctx, stop, runCtl, graceful)

	switch opts.command {
	case "pick":
		return cmdPick(ctx, stdout, opts)
	case "doctor":
		return doctor(stdout, pal, opts.bin, opts.width)
	case "update":
		return cmdUpdate(ctx, stdout, pal, opts)
	case "runs":
		return cmdRuns(stdout, pal, opts.runsLimit)
	case "show":
		return cmdShow(stdout, opts.showRun)
	case "version":
		fmt.Fprintf(stdout, "gauntlet %s\n", version)
		return exitOK
	}

	dirs, err := resolveDirs(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	// Continue a run that a hot reload interrupted, so counters and the
	// journal survive the swap. Loaded before discovery: a resumed stacked
	// run must pin its predecessor's base commit before anything is read.
	// A successor whose handoff cannot be read must stop here: starting a
	// fresh run would re-apply finished reviews under a new id.
	var prior handoff
	resumed, err := selfupdate.LoadState(&prior)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot resume the interrupted run: %v\n", err)
		return exitFail
	}
	if prior.Dirs == nil {
		prior.Dirs = map[string]dirHandoff{}
	}

	// Agents: explicit list, or everything installed. --list and --show-prompt
	// only read prompts, so they must work on a machine that has not installed
	// an agent yet; doctor is how you find that out.
	agents := opts.agents
	autoDetected := false
	if len(agents) == 0 {
		agents = agent.Installed()
		autoDetected = true
	}
	if opts.needsAgents() {
		if len(agents) == 0 {
			fmt.Fprintf(os.Stderr, "No auto-detectable agents found in PATH. Install one of: %s, "+
				"or name an agent explicitly with --agents.\n", strings.Join(agent.Valid, ", "))
			return exitUsage
		}
		for _, spec := range agents {
			if opts.bin[spec.Tool] != "" {
				continue
			}
			if spec.Tool == "dsh" && agent.Resolve("dsh") == "" && agent.Resolve("bunx") != "" {
				continue // BuildCmd falls back to bunx @deepseek-ai/dsh
			}
			if agent.Resolve(spec.Tool) == "" {
				fmt.Fprintf(os.Stderr, "Required tool not found in PATH: %s\n", spec.Tool)
				return exitUsage
			}
		}
	}

	runs := make([]*dirRun, 0, len(dirs))
	for _, dir := range dirs {
		runs = append(runs, &dirRun{dir: dir})
	}
	defer releaseAll(runs)

	runID := prior.RunID
	// origin is the wall instant the run began, for the journal. startedAt
	// is what time.Since measures against in this process: a fresh run uses
	// now (monotonic), a resumed one is reconstructed from the predecessor's
	// measured elapsed so an NTP step during the exec cannot exhaust or
	// extend --runtime.
	var origin, startedAt time.Time
	// One clock read for both the id and the shard: two reads either side of
	// a UTC midnight would put a fresh run's id date and its directory one
	// day apart.
	now := time.Now()
	if !resumed || runID == "" {
		runID = journal.NewRunID(now)
		origin, startedAt = now, now
	} else {
		origin = prior.StartedAt
		startedAt = resumeStart(now, prior)
	}

	ownArtifacts := map[string]bool{}
	if opts.logFile != "" {
		ownArtifacts[runner.RealPath(opts.logFile)] = true
	}
	locked := false
	lockAll := func() int {
		for _, d := range runs {
			lockPath := runner.LockPath(d.dir)
			lock, err := runner.Acquire(lockPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				if errors.Is(err, runner.ErrLocked) {
					return exitLocked
				}
				return exitUsage
			}
			d.lock = lock
			ownArtifacts[runner.RealPath(lockPath)] = true
		}
		locked = true
		return -1
	}

	// Stacked mode fronts every check before any agent, the suggestion agent
	// included: the dirty-checkout consent, remote and gh validation, the
	// pinned base fetch, and the snapshot worktree that discovery reads. The
	// locks come first, so the fetch and the snapshot never race another
	// gauntlet in the same clone. Informational modes skip all of it: they
	// read the local tree and open no remote of their own. They are not
	// silent, though -- the suggest step below runs before --list and
	// --dry-run print, because the schedule they print is what it decides.
	informational := opts.showPrompt != "" || opts.list || opts.dryRun
	if opts.stackedPRs && !informational {
		if code := lockAll(); code >= 0 {
			return code
		}
		stdin := bufio.NewReader(os.Stdin)
		interactive := stdinIsTerminal()
		// Registered before the loop: with several directories, a decline or
		// failure on a later one must still remove the snapshots the earlier
		// ones already created.
		defer cleanupSnapshots(runs)
		for _, d := range runs {
			err := stackPreflight(ctx, d, opts, prior.Dirs[d.dir], resumed, runID,
				ownArtifacts, stdin, interactive, stdout)
			if errors.Is(err, errAborted) {
				fmt.Fprintln(stdout, "Aborted.")
				return exitOK
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return exitUsage
			}
		}
	}

	// Discovery and selection happen per directory: a project can carry its
	// own *-review.md files, so the available set differs per tree. A stacked
	// run discovers in its base snapshot rather than the user's checkout.
	for _, d := range runs {
		set, warnings, err := prompt.Discover(ctx, opts.promptDir, d.scanDir())
		if err != nil {
			if opts.promptDir != "" {
				err = fmt.Errorf("--prompt-dir %s: %w", opts.promptDir, err)
			}
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
		}
		if set.Len() == 0 {
			fmt.Fprintf(os.Stderr, "No reviews found for %s\n", d.dir)
			return exitUsage
		}
		d.set = set
	}

	// Informational modes act on the first directory, then exit.
	if opts.showPrompt != "" {
		return cmdShowPrompt(stdout, runs[0].set, opts)
	}

	if err := planReviews(ctx, needPlanning(runs, prior, resumed), opts, agents, stdout, pal); err != nil {
		if errors.Is(err, errAborted) {
			return exitOK
		}
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errAgentFailed) {
			return exitFail
		}
		return exitUsage
	}

	if opts.list {
		listReviews(stdout, pal, runs[0].set, runs[0].reviews, opts.width)
		return exitOK
	}
	if opts.dryRun {
		dryRun(stdout, pal, runs, agents, opts)
		return exitOK
	}

	// From here the run is real: take the locks before doing anything an agent
	// could observe. A stacked run already took them, before its preflight.
	if !locked {
		if code := lockAll(); code >= 0 {
			return code
		}
	}

	// The index describes the tree, not this process: a reload inherits the
	// one its predecessor built rather than spending another half hour.
	if opts.semcode && !resumed {
		if code := buildSemcodeIndex(ctx, stdout, runs); code != exitOK {
			return code
		}
	}

	jrnl, err := journal.Open(runID, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot write run journal: %v\n", err)
	}

	bus := runner.NewBus()
	reportEvents := bus.Subscribe(1024)
	journalEvents := bus.Subscribe(1024)
	lockEvents := bus.Subscribe(1024)

	var consumers sync.WaitGroup
	consumers.Go(func() { noteLocks(runs, runID, lockEvents) })
	consumers.Go(func() {
		for ev := range journalEvents {
			// The feed and the live usage ticks are high volume and
			// reconstructible; the results they add up to are not, and those
			// are recorded in full.
			if ev.Kind == runner.EvOutput || ev.Kind == runner.EvUsage {
				continue
			}
			jrnl.Write(ev)
			if ev.Kind == runner.EvLoopEnd {
				jrnl.Flush()
			}
		}
	})

	var dash *ui.Dashboard
	if opts.tui {
		dash = ui.New(ui.Config{
			Version:    version,
			RunID:      runID,
			Dirs:       dirs,
			Agents:     agent.Labels(agents),
			Reviews:    allReviews(runs),
			Jobs:       opts.jobs,
			StackedPRs: opts.stackedPRs,
			Timeout:    opts.timeout,
			Budget:     opts.runtime,
			Started:    startedAt,
			// `s` on the dashboard is the same request SIGQUIT makes.
			OnFinish: func() { graceful.request(nil) },
		}, bus.Subscribe(4096))
	} else {
		rep := &reporter{out: stdout, pal: pal, multiDir: len(runs) > 1, quiet: opts.quiet}
		consumers.Go(func() {
			rep.Consume(reportEvents)
		})
		if resumed {
			rep.logf(now, "Reloaded into gauntlet %s (run %s, reload #%d, %d loops carried over)",
				version, runID, prior.Reloads, prior.Loops())
		}
		rep.logf(now, "gauntlet %s, run %s, agents: %s", version, runID, strings.Join(agent.Labels(agents), ", "))
		if autoDetected {
			rep.logf(now, "Auto-detected agents (name them with --agents to pin the pool)")
		}
		if opts.jobs > 1 {
			rep.logf(now, "Parallel mode: %d lanes, worktree-isolated and merged back", opts.jobs)
		} else if opts.stackedPRs {
			rep.logf(now, "Stacked PR mode: sequential reviews in one isolated worktree")
		}
	}
	if opts.tui {
		// The dashboard owns the screen, so the reporter has nowhere to print.
		// With --log it still has somewhere to write: the file. Without one,
		// its subscription is drained, or the bus blocks when its buffer fills.
		if logWriter != nil {
			fileRep := &reporter{out: logWriter, pal: palette{}, multiDir: len(runs) > 1, quiet: opts.quiet}
			consumers.Go(func() {
				fileRep.Consume(reportEvents)
			})
		} else {
			consumers.Go(func() {
				for range reportEvents {
				}
			})
		}
	}

	for _, d := range runs {
		// A reloaded process inherits the same argv, so each directory's loop
		// budget must be reduced by what it already finished before the swap.
		carried := prior.Dirs[d.dir]
		maxLoops := opts.maxLoops
		if maxLoops > 0 {
			maxLoops -= carried.Loops
			if maxLoops <= 0 {
				// This directory already ran its loops; keep its results for
				// the summary and do not start it again.
				d.stats = &runner.Stats{Start: startedAt}
				d.stats.Seed(carried.Results, carried.CommitRuns, carried.CommitFails)
				d.loops = carried.Loops
				continue
			}
		}
		cfg := runner.Config{
			Dir: d.dir, Set: d.set, Reviews: d.reviews, Agents: agents, Bin: opts.bin,
			Timeout: opts.timeout, Jobs: opts.jobs, Retries: opts.retries, MaxLoops: maxLoops,
			Started: startedAt, ResumeQueue: carried.Pending,
			Runtime: opts.runtime, UsageCmd: opts.usageArgv, UsageLimit: opts.usageLimit,
			Commit: opts.commit, Push: opts.push,
			StackedPRs: opts.stackedPRs, PRBase: opts.prBase, PushRemote: opts.pushRemote,
			// The stacked preflight (dirty consent included) already ran,
			// before the suggest step; New reuses its result instead of
			// checking or fetching again.
			StackPrep:        d.prep,
			MergeInto:        opts.mergeInto,
			ResolveConflicts: opts.resolveConflicts,
			Seed:             opts.seed,
			// The dashboard renders the feed from these events, so --tui must
			// not suppress them; only --quiet does.
			Yolo: opts.yolo, Paths: opts.paths, Raw: opts.raw, Quiet: opts.quiet, Stream: opts.stream,
			ContinueSessions: opts.continueSessions,
			RunID:            runID, Version: version, OwnArtifacts: ownArtifacts,
		}
		r, err := runner.New(ctx, cfg, bus)
		if errors.Is(err, runner.ErrDirtyTree) && !opts.stackedPRs &&
			commitFirst(ctx, d.dir, agents, opts, stdout, pal) {
			r, err = runner.New(ctx, cfg, bus)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			bus.Close()
			consumers.Wait()
			// The run never started, so it gets no index row; the quiet close
			// still flushes whatever New logged before it failed.
			jrnl.CloseQuiet()
			return exitUsage
		}
		d.r = r
		d.stats = r.Stats()
		d.stats.Seed(carried.Results, carried.CommitRuns, carried.CommitFails)
		d.carriedLoops = carried.Loops
	}

	// From here a graceful quit has runners to reach; one that arrived while
	// they were being built applies now.
	graceful.arm(runs, runCtl)

	reloadPath := startReloadWatch(ctx, opts, runs, bus)
	if opts.autoUpdate {
		go autoUpdateLoop(ctx, opts, bus)
	}

	// One goroutine per directory: distinct trees, distinct locks, no shared
	// mutable state beyond the event bus.
	var workers sync.WaitGroup
	for _, d := range runs {
		if d.r == nil {
			continue // finished its loops before the reload
		}
		workers.Go(func() {
			d.r.Run(ctx)
			d.loops = d.carriedLoops + d.r.Loops()
		})
	}

	if dash != nil {
		// The dashboard owns the terminal and returns when the user quits or
		// the run ends; quitting cancels the run. A pending hot reload closes
		// it instead: the successor needs the terminal, and waiting for a
		// keypress would stall the swap indefinitely.
		go func() {
			workers.Wait()
			dash.Finish()
			// A reload needs the terminal back, and a graceful quit was a
			// request to leave: neither should wait for a keypress.
			if p := reloadPath.Load(); (p != nil && *p != "") || graceful.asking() {
				dash.Quit()
			}
		}()
		if err := dash.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "dashboard error: %v\n", err)
		}
		stop()
	}
	workers.Wait()
	bus.Close()
	consumers.Wait()

	// A pending reload takes over before anything final is printed: the
	// successor continues this run, and it writes the one summary that covers
	// all of it.
	reloadFailed := false
	if path := reloadPath.Load(); path != nil && *path != "" {
		jrnl.CloseQuiet()
		if code := doReload(*path, runID, origin, time.Since(startedAt), runs, prior, argv, stdout); code >= 0 {
			// The exec failed, or the handoff could not be saved and the
			// reload was aborted: no successor is coming, so finish the run
			// here. Returning without the summary would orphan the whole
			// journal, because the quiet close already skipped this process's
			// index row and the successor that was to write it will never
			// exist.
			reloadFailed = true
		}
	}

	wall := time.Since(startedAt)
	switch {
	case !opts.tui:
		summary(stdout, pal, runs, wall)
	case logWriter != nil:
		summary(logWriter, palette{}, runs, wall)
	}
	// The dashboard cleared itself on the way out, so a terminal-only run
	// leaves nothing behind: point at the journal before exiting.
	if opts.tui {
		fmt.Fprintf(stdout, "Run %s saved. Replay it with: gauntlet show %s\n", runID, runID)
	}

	code := exitCode(ctx, runs)
	if reloadFailed {
		code = exitFail
	}
	writeSummary(jrnl, runID, origin, wall, dirs, agents, runs, code)
	return code
}

// stackPreflight runs one directory's stacked-mode checks before any agent
// (the suggestion agent included) can start: the dirty-checkout consent, the
// remote and gh validation, the pinned base fetch, and the snapshot worktree
// that prompt discovery and the suggest step read instead of the checkout.
func stackPreflight(ctx context.Context, d *dirRun, opts *options, carried dirHandoff,
	resumed bool, runID string, ownArtifacts map[string]bool,
	in *bufio.Reader, interactive bool, out io.Writer) error {

	cfg := runner.Config{
		Dir: d.dir, StackedPRs: true, PRBase: opts.prBase, PushRemote: opts.pushRemote,
		OwnArtifacts: ownArtifacts, RunID: runID,
		// A hot-reload successor continues an isolation decision already made
		// by the original process, and keeps the base commit it pinned:
		// prompting again could strand the run, and fetching again could hand
		// it a base that moved, splitting the resumed stack into a new one.
		AllowDirtyStack: resumed,
		ResumeStackTip:  carried.StackBaseTip,
	}
	if resumed && carried.StackBase != "" {
		cfg.PRBase = carried.StackBase
	}
	prep, err := runner.PrepareStack(ctx, cfg)
	if dirty, ok := errors.AsType[*runner.StackDirtyError](err); ok {
		proceed, confirmErr := confirmStackIsolationWith(out, in, interactive, opts, dirty)
		if confirmErr != nil {
			return confirmErr
		}
		if !proceed {
			return errAborted
		}
		cfg.AllowDirtyStack = true
		prep, err = runner.PrepareStack(ctx, cfg)
	}
	if err != nil {
		return err
	}
	d.prep = prep
	d.repo = gitx.Open(d.dir)
	snap, err := d.repo.AddSnapshotWorktree(ctx, runID, prep.BaseTip)
	if err != nil {
		return fmt.Errorf("cannot create the base snapshot worktree in %s: %w", d.dir, err)
	}
	d.snapshot = snap
	return nil
}

// cleanupSnapshots removes the base snapshot checkouts a stacked run read its
// prompts from. They outlive the runners on purpose: prompt bodies are read
// from them for as long as reviews launch.
func cleanupSnapshots(runs []*dirRun) {
	for _, d := range runs {
		if d.snapshot == nil {
			continue
		}
		if err := d.snapshot.Remove(context.Background()); err == nil {
			d.repo.CleanWorktreeRoot()
		}
	}
}

// confirmStackIsolationWith makes the omission boundary explicit. A stacked
// run is safe beside a dirty checkout because it starts from a fetched remote
// commit, but silently reviewing a different tree from the one on screen is
// surprising. Unattended callers must opt in with --yes.
func confirmStackIsolationWith(out io.Writer, in *bufio.Reader, interactive bool, opts *options,
	dirty *runner.StackDirtyError) (bool, error) {
	paths := dirty.DisplayPaths()
	fmt.Fprintf(out, "\nUNCOMMITTED FILES (%d)\n", len(paths))
	const displayLimit = 20
	for _, path := range paths[:min(len(paths), displayLimit)] {
		fmt.Fprintf(out, "  %s\n", path)
	}
	if len(paths) > displayLimit {
		fmt.Fprintf(out, "  ... and %d more\n", len(paths)-displayLimit)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "These files stay in the original checkout.")
	fmt.Fprintln(out, "They will not be reviewed or included in the PRs.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "STACK BASE")
	fmt.Fprintf(out, "  remote  %s\n", dirty.Remote)
	fmt.Fprintf(out, "  branch  %s\n", dirty.Base)
	fmt.Fprintln(out, "  After confirmation, gauntlet fetches this remote branch and starts")
	fmt.Fprintln(out, "  the isolated worktree from the fetched commit.")
	if opts.yes || opts.yolo {
		flag := "--yes"
		if opts.yolo {
			flag = "--yolo"
		}
		fmt.Fprintf(out, "Proceeding (%s).\n", flag)
		return true, nil
	}
	if !interactive {
		return false, errors.New("stacked PRs need confirmation to exclude uncommitted changes; rerun with --yes")
	}
	fmt.Fprint(out, "Continue? [y/N] ")
	line, err := in.ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// stdinIsTerminal reports whether stdin is a real terminal. A character
// device check is not enough: /dev/null is a character device, and treating
// it as interactive would park an unattended run on a prompt nobody answers.
func stdinIsTerminal() bool { return isTerminal(os.Stdin) }

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// exitCode maps the run's outcome onto the documented codes.
func exitCode(ctx context.Context, runs []*dirRun) int {
	if ctx.Err() != nil {
		return 128 + int(syscall.SIGINT)
	}
	for _, d := range runs {
		if d.stats == nil {
			continue
		}
		if d.stats.Counts().Failures() > 0 || d.stats.CommitFails() > 0 {
			return exitFail
		}
	}
	return exitOK
}

// noteLocks keeps each directory's lock file describing what that run is doing
// now, so a second gauntlet turned away from the directory is told what holds
// it rather than only that something does.
func noteLocks(runs []*dirRun, runID string, events <-chan runner.Event) {
	locks := make(map[string]*runner.Lock, len(runs))
	running := make(map[string]map[string]string, len(runs))
	for _, d := range runs {
		locks[d.dir] = d.lock
		running[d.dir] = map[string]string{}
		d.lock.Note(fmt.Sprintf("gauntlet %s (pid %d, run %s): starting", version, os.Getpid(), runID))
	}
	write := func(dir string) {
		lock, ok := locks[dir]
		if !ok {
			return
		}
		what := "idle"
		if active := running[dir]; len(active) > 0 {
			parts := make([]string, 0, len(active))
			for review, agent := range active {
				parts = append(parts, review+" ("+agent+")")
			}
			sort.Strings(parts) // map order would make the note flicker
			what = strings.Join(parts, ", ")
		}
		lock.Note(fmt.Sprintf("gauntlet %s (pid %d, run %s): %s",
			version, os.Getpid(), runID, what))
	}
	for ev := range events {
		active, ours := running[ev.Dir]
		if !ours {
			continue // an event from a directory this process does not hold
		}
		switch ev.Kind {
		case runner.EvReviewStart:
			active[ev.Review] = ev.Agent
		case runner.EvReviewEnd:
			delete(active, ev.Review)
		default:
			continue
		}
		write(ev.Dir)
	}
}

func releaseAll(runs []*dirRun) {
	for _, d := range runs {
		d.lock.Release()
	}
}

// writeSummary closes the journal with this run's index entry.
func writeSummary(j *journal.Journal, runID string, start time.Time, elapsed time.Duration, dirs []string,
	agents []agent.Spec, runs []*dirRun, code int) {

	s := journal.Summary{
		Version: version, Dirs: dirs, Agents: agent.Labels(agents),
		Args: os.Args[1:], Start: start, End: time.Now(), ExitCode: code,
	}
	if elapsed > 0 {
		s.Elapsed = elapsed.Seconds()
	}
	for _, d := range runs {
		if d.stats == nil {
			continue
		}
		c := d.stats.Counts()
		s.Loops += d.loops
		s.Reviews += c.Total()
		s.OK += c.OK
		s.Failed += c.Fail + c.Timeout
		s.Skipped += c.Skipped
		s.Conflicts += c.Conflict
		ins, del, tokens, _, _, _ := d.stats.Totals()
		s.Ins += ins
		s.Del += del
		s.Tokens += tokens
	}
	if err := j.Close(s); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: run journal incomplete: %v\n", err)
	}
}

// watchSignals maps process signals onto the two kinds of stop.
//
// Ctrl-C (SIGINT) is staged: the first one is the graceful quit -- the review
// in flight finishes and lands its work, commit, push, PR and merge included,
// exactly as SIGQUIT, `s` on the dashboard, and a tripped usage limit stop a
// run -- the second terminates the running reviews, and the third force-kills
// the process. An agent mid-review never sees the terminal's SIGINT at all
// (every agent runs in its own process group, whichever CLI it is), so what
// Ctrl-C means is decided entirely here, and a review that is seconds from
// committing is worth one more Ctrl-C to kill. SIGTERM is not staged: it
// comes from a supervisor or a kill, both of which mean "stop now", and a
// service manager that escalates to SIGKILL on its own schedule must not be
// met with a run that decided to finish an agent launch first.
func watchSignals(ctx context.Context, stop context.CancelFunc, out io.Writer, graceful *gracefulStop) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGQUIT)
	go func() {
		defer signal.Stop(quit)
		for {
			select {
			case <-quit:
				graceful.request(out)
			case <-ctx.Done():
				return
			}
		}
	}()

	ch := make(chan os.Signal, 3)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(ch)
		watchInterrupts(ctx, ch, stop, out, graceful, os.Exit)
	}()
}

// watchInterrupts is watchSignals' Interrupt/SIGTERM stage machine, split out
// so a test can drive it through an ordinary channel: signal.Notify fans every
// delivery out to all registered channels, so a test that signals the process
// would leave handlers behind for the next one. watchSignals itself unregisters
// on ctx.Done.
func watchInterrupts(ctx context.Context, ch <-chan os.Signal, stop context.CancelFunc,
	out io.Writer, graceful *gracefulStop, exit func(int)) {
	for {
		var sig os.Signal
		select {
		case sig = <-ch:
		case <-ctx.Done():
			return
		}
		// The first Ctrl-C asks for the graceful quit -- unless one was
		// already asked for by any path, in which case the operator has
		// seen the "finishing" message and this press means "stop now".
		if sig == os.Interrupt && !graceful.asking() {
			graceful.request(out)
			continue
		}
		fmt.Fprintf(out, "\nSignal received (%s), terminating running reviews. Again to force-kill.\n", sig)
		stop()
		<-ch
		fmt.Fprintln(out, "\nForce-killing.")
		exit(128 + int(syscall.SIGINT))
		return
	}
}

// gracefulStop carries the "finish and quit" request from whoever asks for it
// (a signal, a key in the dashboard) to the runners, which may not exist yet
// when the signal handler is installed.
type gracefulStop struct {
	mu    sync.Mutex
	runs  []*dirRun
	asked bool
	out   io.Writer
}

// arm gives the request somewhere to land. A request that arrived before the
// runners existed is applied now.
func (g *gracefulStop) arm(runs []*dirRun, out io.Writer) {
	g.mu.Lock()
	g.runs, g.out = runs, out
	asked := g.asked
	g.mu.Unlock()
	if asked {
		g.request(out)
	}
}

// request asks every runner to finish what it started and stop. Repeating it
// is harmless, and says so once rather than twice.
func (g *gracefulStop) request(out io.Writer) {
	g.mu.Lock()
	first, runs := !g.asked, g.runs
	g.asked = true
	if g.out != nil {
		out = g.out
	}
	g.mu.Unlock()
	for _, d := range runs {
		if d.r != nil {
			d.r.RequestFinish()
		}
	}
	if first && out != nil {
		fmt.Fprintln(out, "\nFinishing: no new reviews will start. "+
			"The ones running will end, commit, publish or merge as configured. Ctrl-C to stop now.")
	}
}

// asking reports whether a graceful quit was requested, for the caller that
// decides whether the dashboard should close itself.
func (g *gracefulStop) asking() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.asked
}

// semcodeIndexTimeout bounds one directory's index build. The indexer walks
// the whole tree before the first review starts; without a cap a wedged build
// would hang the run indefinitely.
const semcodeIndexTimeout = 30 * time.Minute

// buildSemcodeIndex runs the indexer once per directory before the loop, so
// reviews can answer call-graph and type queries from an index.
func buildSemcodeIndex(ctx context.Context, out io.Writer, runs []*dirRun) int {
	idx := agent.Resolve("semcode-index")
	if idx == "" {
		fmt.Fprintln(os.Stderr, "Required tool not found in PATH: semcode-index")
		return exitUsage
	}
	for _, d := range runs {
		fmt.Fprintf(out, "Building semcode index in %s\n", d.dir)
		ictx, cancel := context.WithTimeout(ctx, semcodeIndexTimeout)
		code := runIndexer(ictx, idx, []string{"-s", "."}, d.dir)
		cancel()
		if code == 0 {
			continue
		}
		switch {
		case ictx.Err() == context.DeadlineExceeded:
			fmt.Fprintf(os.Stderr, "semcode-index timed out after %v in %s\n",
				semcodeIndexTimeout, d.dir)
		case code < 0:
			fmt.Fprintf(os.Stderr, "semcode-index failed in %s (interrupted or killed)\n", d.dir)
		default:
			fmt.Fprintf(os.Stderr, "semcode-index failed in %s with exit code %d\n", d.dir, code)
		}
		return exitFail
	}
	return exitOK
}

// startReloadWatch arms the hot-reload watcher. When the executable changes,
// every runner is asked to stop at its next quiescent point; the exec happens
// after the summary is written.
func startReloadWatch(ctx context.Context, opts *options, runs []*dirRun, bus *runner.Bus) *atomicString {
	var pending atomicString
	if !opts.hotReload {
		return &pending
	}
	w, err := selfupdate.Watch(ctx, 5*time.Second)
	if err != nil {
		// The run can proceed without reloads; staying silent would leave the
		// user assuming a safety net that is not there.
		bus.Publish(runner.Event{Kind: runner.EvLog,
			Text: fmt.Sprintf("hot reload disabled: %v", err)})
		return &pending
	}
	go func() {
		path, ok := <-w.Change
		if !ok || path == "" {
			return
		}
		pending.Store(&path)
		bus.Publish(runner.Event{
			Kind: runner.EvReload,
			Text: "new binary detected, reloading after in-flight reviews finish",
		})
		for _, d := range runs {
			if d.r != nil {
				d.r.RequestStop()
			}
		}
	}()
	return &pending
}

// doReload hands control to the new binary. It returns a nonnegative exit code
// only when the exec failed and the caller should exit normally instead.
func doReload(path, runID string, start time.Time, elapsed time.Duration, runs []*dirRun, prior handoff,
	argv []string, out io.Writer) int {
	h := handoff{
		RunID: runID, StartedAt: start, Elapsed: elapsed, Reloads: prior.Reloads + 1,
		Dirs: make(map[string]dirHandoff, len(runs)),
	}
	for _, d := range runs {
		if d.stats == nil {
			continue
		}
		loops := d.loops
		if d.r != nil {
			loops = d.carriedLoops + d.r.Loops()
		}
		pending := carriedPending(d)
		dh := dirHandoff{
			Loops:       loops,
			Pending:     pending,
			Reviews:     d.reviews,
			CommitRuns:  d.stats.CommitRuns(),
			CommitFails: d.stats.CommitFails(),
			// Seeded stats already include what earlier processes did, so this
			// is the whole run's history, not just this process's slice.
			Results: d.stats.Results(),
		}
		if d.prep != nil {
			dh.StackBase, dh.StackBaseTip = d.prep.Base, d.prep.BaseTip
		}
		h.Dirs[d.dir] = dh
	}
	statePath, err := selfupdate.SaveState(journal.StateDir(), runID, h)
	if err != nil {
		// Without the handoff the successor would start a fresh run: a new
		// run id, every loop restarted, and this process's journal already
		// quiet-closed with no index row, so the run would vanish from
		// `gauntlet runs` and the history weighting while its reviews ran a
		// second time. Stay in this process instead: the caller finishes the
		// run normally (summary, index row, lock release), and the new binary
		// is picked up at the next start.
		fmt.Fprintf(os.Stderr, "Reload aborted: cannot save reload state: %v\n", err)
		return exitFail
	}
	// Locks are released here, before the exec: the successor takes them.
	releaseAll(runs)
	fmt.Fprintf(out, "Reloading into the new binary at %s\n", path)
	if err := selfupdate.Reexec(path, statePath, argv); err != nil {
		// The exec failed, so no successor will pick the handoff up; leaving
		// it would strand one file in the state dir per failed reload.
		if statePath != "" {
			os.Remove(statePath)
		}
		fmt.Fprintf(os.Stderr, "Reload failed: %v\n", err)
		return exitFail
	}
	return -1 // unreachable: exec replaced this process
}

// needPlanning returns the directories whose reviews still have to be chosen,
// after giving every resumed directory back the schedule it was already
// running. A hot reload is a handover: choosing again would re-ask an agent
// (and the user) with --suggest, or reopen the launcher for a run it composed.
func needPlanning(runs []*dirRun, prior handoff, resumed bool) []*dirRun {
	out := runs[:0:0]
	for _, d := range runs {
		if carried := prior.Dirs[d.dir]; resumed && len(carried.Reviews) > 0 {
			d.reviews = carried.Reviews
			continue
		}
		out = append(out, d)
	}
	return out
}

// commitFirst offers the one thing that turns a refused --jobs run into a
// running one: the uncommitted work is the obstacle, and gauntlet already has
// an agent that writes commit messages. It reports whether the tree is clean
// afterwards, so the caller can simply try again.
//
// It asks first, because committing someone's working tree is not a step to
// take on a guess. --yes and --yolo are that consent, and a run with no
// terminal keeps the plain error rather than committing unattended.
func commitFirst(ctx context.Context, dir string, agents []agent.Spec,
	opts *options, out io.Writer, pal palette) bool {

	// The run's own agent, never --suggest-agent: that one was asked which
	// reviews apply, which says nothing about who should write commits, and a
	// run with `--agents opencode --suggest-agent claude` means opencode.
	spec := agents[0]
	fmt.Fprintf(out, "\n%s has uncommitted changes, and --jobs %d needs a clean tree.\n",
		dir, opts.jobs)
	if !confirmCommit(out, opts, spec) {
		return false
	}
	fmt.Fprintf(out, "Running the commit step with %s...\n", spec.Label())
	err := runner.CommitNow(ctx, runner.CommitOpts{
		Dir: dir, Agent: spec, Bin: opts.bin, Push: opts.push, Yolo: opts.yolo,
		Timeout: opts.timeout,
		Out:     func(line string) { fmt.Fprintln(out, pal.dim("  "+line)) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return false
	}
	fmt.Fprintln(out, "Committed. Continuing.")
	return true
}

// confirmCommit asks whether to hand the tree to an agent. Unattended runs
// keep the error: this writes a commit, which is not something to do to a
// tree nobody is watching unless the flags already said to. term.IsTerminal,
// like confirm's: /dev/null is a character device, and prompting there would
// only read EOF.
func confirmCommit(out io.Writer, opts *options, spec agent.Spec) bool {
	if opts.yes || opts.yolo {
		flag := "--yes"
		if opts.yolo && !opts.yes {
			flag = "--yolo"
		}
		fmt.Fprintf(out, "Committing with %s first (%s).\n", spec.Label(), flag)
		return true
	}
	if !stdinIsTerminal() {
		return false
	}
	fmt.Fprintf(out, "Commit them with %s first? [y/N] ", spec.Label())
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// carriedPending is the part of the current loop this directory had not
// started. An empty result means the loop was complete (or never began).
func carriedPending(d *dirRun) []string {
	if d.r == nil {
		return nil
	}
	return d.r.Pending()
}

// autoUpdateLoop replaces the binary in the background. The reload watcher
// notices the new file and schedules the swap, so nothing here touches the run.
func autoUpdateLoop(ctx context.Context, opts *options, bus *runner.Bus) {
	t := time.NewTicker(autoUpdateEvery)
	defer t.Stop()
	// A first check shortly after start, so a run shorter than the interval
	// still benefits; then on the interval. Never on the startup path itself.
	first := time.NewTimer(autoUpdateDelay)
	defer first.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		rel, err := selfupdate.Check(ctx, opts.updateRepo)
		if err != nil {
			// An opted-in updater that keeps failing should say so, not sit
			// silently on an old binary.
			bus.Publish(runner.Event{Kind: runner.EvLog,
				Text: fmt.Sprintf("update check failed: %v", err)})
			continue
		}
		if !rel.NewerThan(version) {
			continue
		}
		if _, err := selfupdate.Apply(ctx, rel); err != nil {
			bus.Publish(runner.Event{Kind: runner.EvLog,
				Text: fmt.Sprintf("auto-update to %s failed: %v", rel.TagName, err)})
			continue
		}
		bus.Publish(runner.Event{Kind: runner.EvLog,
			Text: fmt.Sprintf("updated to %s on disk; reloading at the next safe point", rel.TagName)})
	}
}

// cmdUpdate implements `gauntlet update`.
func cmdUpdate(ctx context.Context, out io.Writer, pal palette, opts *options) int {
	rel, err := selfupdate.Check(ctx, opts.updateRepo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot check for updates: %v\n", err)
		return exitFail
	}
	if !rel.NewerThan(version) {
		fmt.Fprintf(out, "gauntlet %s is current (latest release: %s)\n", version, rel.TagName)
		return exitOK
	}
	fmt.Fprintf(out, "New release: %s (running %s)\n", pal.bold(rel.TagName), version)
	if opts.checkOnly {
		fmt.Fprintln(out, rel.HTMLURL)
		return exitOK
	}
	path, err := selfupdate.Apply(ctx, rel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(out, "Installed %s to %s\n", rel.TagName, path)
	return exitOK
}

// resolveDirs expands and validates the target directories.
//
// A pattern is expanded here as well as by the shell, so a quoted --dirs
// '~/src/*' works. Non-directories among a pattern's matches are skipped
// (a glob over a source tree hits files too), but a literal path that is not
// a directory is a usage error: the user named something specific.
func resolveDirs(opts *options) ([]string, error) {
	// Name the flag the user actually passed: a bad --dir value must not be
	// reported as a --dirs problem.
	label := "--dir"
	list := opts.dirs
	if len(list) == 0 {
		list = []string{opts.dir}
	} else {
		label = "--dirs"
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, entry := range list {
		expanded, err := gauntlethome.ExpandPath(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if expanded == "" {
			return nil, fmt.Errorf("%s is empty", label)
		}
		matches := []string{expanded}
		globbed := isGlob(expanded)
		if globbed {
			found, err := filepath.Glob(expanded)
			if err != nil {
				return nil, fmt.Errorf("%s: bad pattern %q: %w", label, entry, err)
			}
			if len(found) == 0 {
				return nil, fmt.Errorf("%s: %q matched nothing", label, entry)
			}
			sort.Strings(found)
			matches = found
		}
		added := 0
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				return nil, err
			}
			fi, err := os.Stat(abs)
			if err != nil {
				if globbed {
					continue
				}
				return nil, fmt.Errorf("%s: %s: %w", label, abs, err)
			}
			if !fi.IsDir() {
				if globbed {
					continue
				}
				return nil, fmt.Errorf("%s: not a directory: %s", label, abs)
			}
			added++
			if seen[abs] {
				continue // one tree twice would just block on its own lock
			}
			seen[abs] = true
			out = append(out, abs)
		}
		if globbed && added == 0 {
			return nil, fmt.Errorf("%s: %q matched no directories", label, entry)
		}
	}
	return out, nil
}

// isGlob reports whether a path needs expansion. filepath.Match's
// metacharacters are the ones that matter here.
func isGlob(p string) bool { return strings.ContainsAny(p, "*?[") }

// allReviews is the union of every directory's schedule, for the dashboard's
// review grid.
func allReviews(runs []*dirRun) []string {
	var all []string
	for _, d := range runs {
		all = append(all, d.reviews...)
	}
	return uniq(all)
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// atomicString is a tiny holder for the pending reload path.
type atomicString struct {
	mu sync.Mutex
	v  *string
}

func (a *atomicString) Store(v *string) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicString) Load() *string   { a.mu.Lock(); defer a.mu.Unlock(); return a.v }
