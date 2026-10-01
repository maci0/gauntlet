// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command gauntlet runs a codebase through its bundled review prompts,
// dispatched to whichever AI coding agents are installed.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/runx"
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
	exitOK          = 0
	exitFail        = 1  // a run failed, or a report (doctor) and an update found nothing usable
	exitUsage       = 2  // usage error
	exitLocked      = 75 // EX_TEMPFAIL: another instance holds the lock
	exitInterrupted = 128 + int(syscall.SIGINT)
)

// interrupted reports whether err is ctx's cancellation, which every caller
// maps to exitInterrupted rather than to a usage error. ctx.Err is read as
// well because the run's own quit cancels ctx without the err it returns
// naming the cause.
func interrupted(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || ctx.Err() != nil
}

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

// reportDirs is the shape the end-of-run summary reads: what each directory
// contributed, and nothing about how the run holds its state. The summary is
// presentation, so it takes a value and not the run's own record.
func reportDirs(runs []*dirRun) []report.Dir {
	dirs := make([]report.Dir, 0, len(runs))
	for _, d := range runs {
		dirs = append(dirs, report.Dir{Stats: d.stats, Loops: d.loops})
	}
	return dirs
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

	// Every stream this process prints goes here, and three goroutines write
	// it in a plain run: the reporter, and the two signal handlers. The
	// wrapper is applied last, after --log has decided the destination, so a
	// caller's write is one unit of work across every file it reaches. A lock
	// per file would not do that: a multi-writer's Write is a loop, and the
	// second file would then be free to interleave inside the first one's
	// line.
	out := io.Writer(os.Stdout)
	pal := report.Palette{On: report.ColorEnabled(os.Stdout) && !opts.noColor}

	// The launcher and the dashboard draw through lipgloss, which sees
	// NO_COLOR but not this flag; hand the request over before either draws.
	if opts.noColor {
		ui.SetMonochrome()
	}

	// --log tees every stream this process writes. Native multi-writer, not a
	// tee subprocess: one less dependency and no broken-pipe failure mode.
	var logWriter io.Writer
	if opts.logFile != "" {
		f, err := openLogFile(opts.logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitUsage
		}
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: closing log file %s: %v\n", opts.logFile, err)
			}
		}()
		// The lock belongs on the file, not on the console stream: while the
		// dashboard owns the screen, the run-control messages, the file
		// reporter, and both signal handlers write here and nowhere else, and
		// only the console stream goes through stdout's wrapper. The file's own
		// wrapper also covers the copy the console stream makes of every line,
		// so the log holds whole lines whichever way they arrived.
		logWriter, out = report.LogWriters(os.Stdout, f)
		pal.On = false // escape codes would land in the file too
	}
	stdout := io.Writer(&report.Serialized{W: out})

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
		return cmdRuns(stdout, pal, opts.runsLimit, opts.restoreRun, opts.json)
	case "show":
		return cmdShow(stdout, opts.showRun)
	case "resume":
		return cmdResume(stdout, pal, opts.resumeRun)
	case "version":
		if _, err := fmt.Fprintf(stdout, "gauntlet %s\n", version); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the version: %v\n", err)
			return exitFail
		}
		return exitOK
	}

	dirs := opts.resolvedDirs

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
		if missing := missingAgentTool(agents, opts.bin); missing != "" {
			fmt.Fprintf(os.Stderr, "Required tool not found in PATH: %s\n", missing)
			return exitUsage
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
	// The same reading everywhere below: the seed this run derives has to be
	// the one the id was stamped with, not a second read a microsecond later.
	clock := func() time.Time { return now }
	if !resumed || runID == "" {
		runID = journal.NewRunID(now)
		origin, startedAt = now, now
		// The run's one RNG seed, resolved here and nowhere else: the suggest
		// step's agent order and the schedule's shuffles both draw from it, so
		// the number the journal prints on failure replays the whole run
		// rather than the schedule alone.
		opts.seed = effectiveSeed(opts.seed, 0, clock)
	} else {
		origin = resumeOrigin(now, prior)
		startedAt = resumeStart(now, prior)
		// The interrupted process's seed, so the reviews this one still has to
		// run draw from the number the journal already recorded.
		opts.seed = effectiveSeed(opts.seed, prior.Seed, clock)
	}

	// The run's clock, defined next to the instant it is anchored on so every
	// reader below shares one handle: the suggest transcript's stamps, the
	// runner's elapsed times and runtime budget, the sample debounce, the
	// event stamps, and the dashboard. Advanced by the monotonic reading, so
	// an NTP step during the run cannot expire or extend a budget.
	runClock := func() time.Time { return startedAt.Add(time.Since(startedAt)) }

	ownArtifacts := map[string]bool{}
	if opts.logFile != "" {
		ownArtifacts[gitx.RealPath(opts.logFile)] = true
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
				return exitFail
			}
			d.lock = lock
			ownArtifacts[gitx.RealPath(lockPath)] = true
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
			err := stackPreflight(ctx, d, opts, prior.Dir(d.dir), resumed, runID,
				ownArtifacts, stdin, interactive, stdout)
			if errors.Is(err, errAborted) {
				fmt.Fprintln(stdout, "Aborted.")
				return exitOK
			}
			if interrupted(ctx, err) {
				return exitInterrupted
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
			if interrupted(ctx, err) {
				return exitInterrupted
			}
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

	// Informational modes print prompts or schedules, then exit. A review
	// found in a later directory is the one asked for: the first directory to
	// carry the name decides, and a name no set has reports the miss against
	// the first one.
	if opts.showPrompt != "" {
		for _, d := range runs {
			if setHasReview(d.set, opts.showPrompt) {
				return cmdShowPrompt(stdout, d.set, opts)
			}
		}
		return cmdShowPrompt(stdout, runs[0].set, opts)
	}

	if err := planReviews(ctx, needPlanning(runs, prior, resumed), opts, agents, stdout, pal,
		runClock); err != nil {
		if errors.Is(err, errAborted) {
			return exitOK
		}
		if interrupted(ctx, err) {
			return exitInterrupted
		}
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errAgentFailed) {
			return exitFail
		}
		return exitUsage
	}

	if opts.list {
		for i, d := range runs {
			if len(runs) > 1 {
				if i > 0 {
					fmt.Fprintln(stdout)
				}
				fmt.Fprintln(stdout, pal.Bold(d.dir))
			}
			if err := report.ListReviews(stdout, pal, d.set, d.reviews, opts.width); err != nil {
				fmt.Fprintf(os.Stderr, "cannot write review listing: %v\n", err)
				return exitFail
			}
		}
		return exitOK
	}
	if opts.dryRun {
		if err := dryRun(stdout, pal, runs, agents, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write dry run: %v\n", err)
			return exitFail
		}
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
	// The run's one clock, set before anything reads it: the dashboard and
	// the reporter take theirs from Clock below, and the runner, the sample
	// debounce, and the event stamps all read the bus itself.
	bus.Now = runClock
	reportEvents := bus.Subscribe(1024)
	journalEvents := bus.Subscribe(1024)
	lockEvents := bus.Subscribe(1024)

	var consumers sync.WaitGroup
	consumers.Go(func() { noteLocks(runs, runID, lockEvents) })
	consumers.Go(func() {
		for ev := range journalEvents {
			if runner.Droppable(ev.Kind) {
				continue
			}
			jrnl.Write(journaledEvent(ev))
			// Flushed per review, not only per loop: a run killed without a
			// word keeps every review it finished, and `gauntlet resume`
			// appends the rest of the run to this same file.
			if ev.Kind == runner.EvLoopEnd || ev.Kind == runner.EvReviewEnd {
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
			// The run's clock, not a second wall clock: the dashboard's
			// first reading and its end-of-run stamp measure elapsed against
			// the same instant the runner does.
			Now: bus.Clock(),
			// `s` on the dashboard is the same request SIGQUIT makes.
			OnFinish: func() { graceful.request(nil) },
		}, bus.Subscribe(4096))
		// Every exit below, including the failures between here and Run that
		// never reach it, has to leave the event forwarder. It parks on the
		// program's unbuffered channel, which only Run serves, so a run that
		// returns first would strand it with the bus subscription.
		defer dash.Release()
	} else {
		rep := &report.Reporter{Out: stdout, Pal: pal, MultiDir: len(runs) > 1, Quiet: opts.quiet, Now: bus.Clock()}
		consumers.Go(func() {
			rep.Consume(reportEvents)
		})
		// No explicit stamp, so each line reads the reporter's own clock at the
		// moment it is written. `now` is one reading taken before planReviews,
		// which blocks on a real agent for as long as --suggest-timeout allows,
		// so passing it here dated every header by the start of the suggest
		// step: a twelve-minute triage wrote a line stamped twelve minutes
		// early, and a --log file carried its timestamps out of order. The
		// next line, the seed, is written from the event stream and is stamped
		// when it happens.
		if resumed {
			rep.Logf(time.Time{}, "Reloaded into gauntlet %s (run %s, reload #%d, %d loops carried over)",
				version, runID, prior.Reloads, prior.Loops())
		}
		rep.Logf(time.Time{}, "gauntlet %s, run %s, agents: %s", version, runID, strings.Join(agent.Labels(agents), ", "))
		if autoDetected {
			rep.Logf(time.Time{}, "Auto-detected agents (name them with --agents to pin the pool)")
		}
		if opts.jobs > 1 {
			rep.Logf(time.Time{}, "Parallel mode: %d lanes, worktree-isolated and merged back", opts.jobs)
		} else if opts.stackedPRs {
			if opts.maxLoops == 1 {
				rep.Logf(time.Time{}, "Stacked PR mode: sequential reviews in one isolated worktree")
			} else {
				rep.Logf(time.Time{}, "Stacked PR mode: sequential reviews, new worktree per loop from the previous tip")
			}
		}
	}
	if opts.tui {
		// The dashboard owns the screen, so the reporter has nowhere to print.
		// With --log it still has somewhere to write: the file. Without one,
		// its subscription is drained, or the bus blocks when its buffer fills.
		if logWriter != nil {
			fileRep := &report.Reporter{Out: logWriter, Pal: report.Palette{}, MultiDir: len(runs) > 1, Quiet: opts.quiet, Now: bus.Clock()}
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

	// One tally for every directory's runner: --token-budget is a ceiling on
	// what the run spends, and a per-runner total would make it a ceiling per
	// directory, so N directories would allow N times the budget. It starts at
	// zero here because each directory's carried results are seeded into it
	// below, which is what carries a reload's spending into its successor.
	var runTokens runner.Tokens

	// Every recorded result and finished loop asks for a fresh crash
	// checkpoint. One slot: a request that finds one already waiting is
	// covered by it, since the checkpoint is written from current state.
	progress := make(chan struct{}, 1)
	onProgress := func() {
		select {
		case progress <- struct{}{}:
		default:
		}
	}

	for _, d := range runs {
		// A reloaded process inherits the same argv, so each directory's loop
		// budget must be reduced by what it already finished before the swap.
		carried := prior.Dir(d.dir)
		maxLoops := opts.maxLoops
		if maxLoops > 0 {
			maxLoops -= carried.Loops
			if maxLoops <= 0 {
				// This directory already ran its loops; keep its results for
				// the summary and do not start it again.
				d.stats = runner.NewStats(startedAt, &runTokens)
				d.stats.Seed(carried.Results, carried.CommitRuns, carried.CommitFails)
				d.loops = carried.Loops
				continue
			}
		}
		cfg := runner.Config{
			NoSandbox: opts.noSandbox, SandboxWrite: opts.sandboxWrite,
			Dir: d.dir, Set: d.set, Reviews: d.reviews, Agents: agents, Bin: opts.bin,
			Timeout: opts.timeout, Jobs: opts.jobs, Retries: opts.retries, MaxLoops: maxLoops,
			MaxReviews: opts.maxReviews,
			Started:    startedAt, ResumeQueue: carried.Pending,
			Runtime: opts.runtime, UsageCmd: opts.usageArgv, UsageLimit: opts.usageLimit,
			TokenBudget: opts.tokenBudget,
			RunTokens:   &runTokens,
			Commit:      opts.commit, Push: opts.push,
			StackedPRs: opts.stackedPRs, PRBase: opts.prBase, PushRemote: opts.pushRemote,
			// The stacked preflight (dirty consent included) already ran,
			// before the suggest step; New reuses its result instead of
			// checking or fetching again.
			StackPrep:            d.prep,
			ResumeLoops:          carried.Loops,
			ResumeStackHead:      carried.StackHead,
			ResumeStackHeadTip:   carried.StackHeadTip,
			ResumeStackPublished: carried.StackPublished,
			MergeInto:            opts.mergeInto,
			ResolveConflicts:     opts.resolveConflicts,
			Seed:                 opts.seed,
			// The dashboard renders the feed from these events, so --tui must
			// not suppress them; only --quiet does.
			Yolo: opts.yolo, Paths: opts.paths, Raw: opts.raw, Quiet: opts.quiet, Stream: opts.stream,
			ContinueSessions: opts.continueSessions,
			RunID:            runID, Version: version, OwnArtifacts: ownArtifacts,
			Generation: prior.Reloads, OnProgress: onProgress,
		}
		r, err := runner.New(ctx, cfg, bus)
		if errors.Is(err, runner.ErrDirtyTree) && !opts.stackedPRs &&
			commitFirst(ctx, d.dir, agents, opts, stdout, pal, bus.Clock()) {
			r, err = runner.New(ctx, cfg, bus)
		}
		if err != nil {
			code := exitUsage
			if interrupted(ctx, err) {
				code = exitInterrupted
			} else {
				fmt.Fprintln(os.Stderr, err)
			}
			bus.Close()
			consumers.Wait()
			// The run never started, so it gets no index row; the quiet close
			// still flushes whatever New logged before it failed.
			jrnl.CloseQuiet()
			return code
		}
		d.r = r
		d.stats = r.Stats()
		d.stats.Seed(carried.Results, carried.CommitRuns, carried.CommitFails)
		d.carriedLoops = carried.Loops
	}

	var checkpoints sync.WaitGroup
	checkpoints.Go(func() {
		writeCheckpoints(progress, runs, runID, origin, startedAt, opts.seed, prior.Reloads, argv, bus, runClock)
	})

	// From here a graceful quit has runners to reach; one that arrived while
	// they were being built applies now.
	graceful.arm(runs, runCtl)

	reloadPath := startReloadWatch(ctx, opts, runs, bus)
	var autoDone sync.WaitGroup
	autoCtx, stopAuto := context.WithCancel(ctx)
	if opts.autoUpdate {
		autoDone.Go(func() { autoUpdateLoop(autoCtx, opts, bus) })
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

	// ranToEnd records that every runner finished on its own. The dashboard
	// returning does not say whether they did: q on a finished screen closes a
	// run that completed, and q during a review stops one that did not.
	var ranToEnd atomic.Bool
	interrupted := false
	if dash != nil {
		// The dashboard owns the terminal and returns when the user quits or
		// the run ends; quitting cancels the run. A pending hot reload closes
		// it instead: the successor needs the terminal, and waiting for a
		// keypress would stall the swap indefinitely.
		go func() {
			workers.Wait()
			ranToEnd.Store(true)
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
		// Settle the ending before the cancel below: that cancel is this
		// process letting go of the context now the dashboard is gone, and a
		// run that finished would then exit 130 for closing its own screen.
		interrupted = wasInterrupted(ctx.Err() != nil, true, ranToEnd.Load())
		stop()
	}
	workers.Wait()
	// Every runner has returned, so nothing sends on progress any more; the
	// last checkpoint is on disk before anything below can exec or exit.
	close(progress)
	checkpoints.Wait()
	stopAuto()
	autoDone.Wait()
	bus.Close()
	consumers.Wait()

	// A pending reload takes over before anything final is printed: the
	// successor continues this run, and it writes the one summary that covers
	// all of it.
	reloadFailed := false
	if path := reloadPath.Load(); path != nil && *path != "" {
		jrnl.CloseQuiet()
		// The run clock, not a second reading of the process's monotonic
		// source: the handoff's elapsed has to be the same figure the
		// checkpoint and the summary carry, or a replayed run records three
		// slightly different elapsed times for one instant.
		if code := doReload(*path, runID, origin, max(runClock().Sub(startedAt), 0), runs, prior,
			opts.seed, argv, stdout); code >= 0 {
			// The exec failed, or the handoff could not be saved and the
			// reload was aborted: no successor is coming, so finish the run
			// here. Returning without the summary would orphan the whole
			// journal, because the quiet close already skipped this process's
			// index row and the successor that was to write it will never
			// exist.
			reloadFailed = true
		}
	}

	wall := max(runClock().Sub(startedAt), 0)
	switch {
	case !opts.tui:
		report.Summary(stdout, pal, reportDirs(runs), wall)
	case logWriter != nil:
		report.Summary(logWriter, report.Palette{}, reportDirs(runs), wall)
	}
	// The dashboard cleared itself on the way out, so a terminal-only run
	// leaves nothing behind: point at the journal before exiting.
	if opts.tui {
		fmt.Fprintf(stdout, "Run %s saved. Replay it with: gauntlet show %s\n", runID, runID)
	}

	// Without a dashboard, nothing cancels the context on the way out but a
	// signal, so the two are the same question.
	if dash == nil {
		interrupted = ctx.Err() != nil
	}
	code := exitCode(interrupted, runs)
	if reloadFailed {
		code = exitFail
	}
	writeSummary(jrnl, origin, runClock(), wall, dirs, agents, runs, code, opts.keepRuns)
	// The run ended on its own and its index row is written: nothing is left
	// to resume. A reload never gets here, so its checkpoint stays as the
	// fallback until the successor writes its own.
	if err := dropCheckpoint(runID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	return code
}

// writeCheckpoints rewrites the run's crash checkpoint each time a runner
// reports progress, until progress closes. A checkpoint that cannot be
// written costs only the ability to resume after a crash, so the run goes on
// and says so once.
//
// now is the run's clock, the one the dashboard and the reporter read, so
// the stamp and the elapsed the checkpoint records are the same figures the
// summary will carry. Reading the wall clock here instead left a second clock
// inside the run: two runs of one seed under one frozen run clock wrote
// checkpoints whose `updated` and whose handoff elapsed differed, so
// `gauntlet resume` after a replay was not the state the replay described.
func writeCheckpoints(progress <-chan struct{}, runs []*dirRun, runID string, origin, startedAt time.Time,
	seed uint64, reloads int, argv []string, bus *runner.Bus, now func() time.Time) {

	cwd, err := os.Getwd()
	warned := false
	warn := func(err error) {
		if !warned {
			warned = true
			bus.Publish(runner.Event{Kind: runner.EvLog,
				Text: fmt.Sprintf("Cannot write the crash checkpoint, so `gauntlet resume` cannot continue this run: %v", err)})
		}
	}
	unfinished := func(d *dirRun) []string {
		if d.r == nil {
			return nil
		}
		return d.r.Unfinished()
	}
	for range progress {
		if err != nil {
			warn(err)
			continue
		}
		// One reading for both fields, as the run id takes one reading: a
		// stamp and an elapsed either side of a tick would disagree about
		// when this progress happened.
		at := now()
		cp := checkpoint{
			Handoff: buildHandoff(runID, origin, max(at.Sub(startedAt), 0), seed, reloads, runs, unfinished),
			Argv:    argv, Cwd: cwd, PID: os.Getpid(), Version: version, Updated: at,
		}
		if serr := saveCheckpoint(cp); serr != nil {
			warn(serr)
		}
	}
}

// openLogFile opens the run's log for append, refusing anything but a regular
// file the process then owns. The Lstat repeats what flag parsing already
// checked, because the path was validated before the run did any work and the
// file can have changed since; O_NOFOLLOW would refuse the last symlink
// anyway, but with an errno rather than a sentence that names the file.
//
// 0600, like the journal: the file captures agent output, and reviews quote
// what they find in the target tree, credentials included. The 0600 in the
// open call applies only at creation, so a pre-existing file that a looser
// umask or an older run left group- or world-readable gets its permissions
// tightened here, before this run writes anything.
func openLogFile(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("cannot write log file %s: is a symlink", path)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot write log file %s: not a regular file", path)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot write log file %s: %w", path, err)
	}
	if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
		if err := f.Chmod(0o600); err != nil {
			return nil, errors.Join(
				fmt.Errorf("cannot secure log file %s: %w", path, err),
				f.Close())
		}
	}
	return f, nil
}

// missingAgentTool returns the binary a run needs and this machine does not
// have, or "" when every one of them is launchable. dsh is exempt when bunx
// can build it, because BuildCmd falls back to bunx and the pinned dsh spec.
func missingAgentTool(agents []agent.Spec, bin map[string]string) string {
	for _, spec := range agents {
		if bin[spec.Tool] != "" {
			continue
		}
		if spec.Tool == "dsh" && agent.Resolve("dsh") == "" && agent.Resolve("bunx") != "" {
			continue
		}
		if b := agent.Binary(spec.Tool); agent.Resolve(b) == "" {
			return b
		}
	}
	return ""
}

// setHasReview reports whether a directory's set has the review --show-prompt
// names, under the given name or its -review suffix: the same two lookups
// cmdShowPrompt makes before it prints.
func setHasReview(set prompt.Set, name string) bool {
	if _, ok := set.Get(name); ok {
		return true
	}
	_, ok := set.Get(name + "-review")
	return ok
}

// stdinIsTerminal reports whether stdin is a real terminal. A character
// device check is not enough: /dev/null is a character device, and treating
// it as interactive would park an unattended run on a prompt nobody answers.
func stdinIsTerminal() bool { return isTerminal(os.Stdin) }

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// wasInterrupted reports whether the run was cut short instead of running to
// its own end. A cancelled context does not say on its own: the dashboard
// cancels the run context as it closes, so a run that finished would report an
// interrupt nobody asked for. Whether the runners had finished by the time the
// dashboard returned settles it: a q on a finished screen closes a run that
// completed, a q during a review stops one that did not, and a signal cancels
// the context either way.
func wasInterrupted(cancelled, dashboardClosing, ranToEnd bool) bool {
	if dashboardClosing {
		return cancelled || !ranToEnd
	}
	return cancelled
}

// exitCode maps the run's outcome onto the documented codes.
func exitCode(interrupted bool, runs []*dirRun) int {
	if interrupted {
		return exitInterrupted
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

// inFlight is what tells two concurrent instances of the same review apart. A
// name is not an identity: a review scheduled twice means weight, and under
// --jobs the two instances run at once in two lanes, each with its own branch.
// Keyed by the name alone, the first one to end removed the other's entry and
// the note read "idle" while a review was still running.
type inFlight struct {
	loop   int
	review string
	branch string
}

// noteLocks keeps each directory's lock file describing what that run is doing
// now, so a second gauntlet turned away from the directory is told what holds
// it rather than only that something does.
func noteLocks(runs []*dirRun, runID string, events <-chan runner.Event) {
	locks := make(map[string]*runner.Lock, len(runs))
	running := make(map[string]map[inFlight]string, len(runs))
	for _, d := range runs {
		locks[d.dir] = d.lock
		running[d.dir] = map[inFlight]string{}
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
				parts = append(parts, review.review+" ("+agent+")")
			}
			fuzzy.Sort(parts) // map order would make the note flicker
			// Two lanes on one repeated review with the same agent would
			// otherwise print it twice, which reads as two reviews rather
			// than the one the reader is waiting for.
			parts = slices.Compact(parts)
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
		key := inFlight{loop: ev.Loop, review: ev.Review, branch: ev.Branch}
		switch ev.Kind {
		case runner.EvReviewStart:
			active[key] = ev.Agent
		case runner.EvReviewEnd:
			delete(active, key)
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

// journaledEvent is an event as it is written to disk. The journal outlives
// the run and is the file a backup, a sync, or a pasted "gauntlet runs
// --json" carries away, so the operator's home directory is shortened in the
// free-text field: git errors and path errors reach Event.Text with the
// absolute path of the reviewed tree in them, and that path names the account.
// The live terminal and the --log file keep the full text.
func journaledEvent(ev runner.Event) runner.Event {
	if ev.Text != "" {
		ev.Text = normalize.RedactHome(ev.Text)
	}
	return ev
}

// journaledArgs is the command line as the index keeps it, for the same
// reason: an argument naming a directory, a log file, or an agent binary
// carries the account name, and the index row is what "gauntlet runs --json"
// hands out.
//
// A credential goes through the same pass. A command line is not only paths:
// an operator who defines a wrapper agent, or pins a gateway endpoint, hands
// the key on the command line because that is where the agent CLIs read it
// (`--agent-cmd 'x=y --key sk-…'`, `--agent-cmd 'x=y --api-key $…'`). That
// argument is written to index.jsonl and to the resume checkpoint verbatim,
// printed by "gauntlet runs", echoed by "gauntlet resume" and
// listCheckpoints, and all of those outlive the run and are read by people who
// are not the operator. runx.RedactSecrets is the same redaction every child
// process's output already goes through, for the same reason: a key printed
// by a tool reaches the journal, and this one is printed by the operator's
// own shell. RedactHome runs too, since a userinfo-bearing argument carries
// both an account and a password.
func journaledArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = runx.RedactSecrets(normalize.RedactHome(a))
	}
	return out
}

// writeSummary closes the journal with this run's index entry. end is the
// run's last clock reading, so the entry's span and its elapsed come from one
// clock rather than a wall-clock read beside them.
func writeSummary(j *journal.Journal, start, end time.Time, elapsed time.Duration, dirs []string,
	agents []agent.Spec, runs []*dirRun, code, keepRuns int) {

	s := journal.Summary{
		Version: version, Dirs: dirs, Agents: agent.Labels(agents),
		Args: journaledArgs(os.Args[1:]), Start: start, End: end, ExitCode: &code,
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
		s.Interrupted += c.Interrupted
		s.Other += c.Other
		ins, del, tokens, _, _, haveLines := d.stats.Totals()
		s.Ins += ins
		s.Del += del
		s.LinesMeasured = s.LinesMeasured || haveLines
		s.Tokens += tokens
	}
	if err := j.Close(s); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: run journal incomplete: %v\n", err)
	}
	// The run's own row is the newest one, so pruning here never touches the
	// run that just finished, and a failure only means the state tree keeps
	// growing: it is not this run's problem to report as its own.
	//
	// Evicted runs are the one loss a prune makes permanent. Every other run
	// it touched was renamed into the quarantine and `gauntlet runs --restore`
	// can bring it back; the ones the bound pushed out of that quarantine were
	// unlinked, which happens when --keep-runs is lowered. Silence there would
	// make history the operator could have restored five minutes ago simply
	// stop existing, so the count is what the run says it destroyed.
	res, err := journal.Prune(keepRuns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot prune old run journals: %v\n", err)
	}
	if res.Evicted > 0 {
		fmt.Fprintf(os.Stderr, "Warning: the --keep-runs %d bound unlinked %s from the quarantine, and nothing else holds them\n",
			keepRuns, humanize.Plural(res.Evicted, "run", "runs"))
	}
}

// needPlanning returns the directories whose reviews still have to be chosen,
// after giving every resumed directory back the schedule it was already
// running. A hot reload is a handover: choosing again would re-ask an agent
// (and the user) with --suggest, or reopen the launcher for a run it composed.
func needPlanning(runs []*dirRun, prior handoff, resumed bool) []*dirRun {
	out := runs[:0:0]
	for _, d := range runs {
		if carried := prior.Dir(d.dir); resumed && len(carried.Reviews) > 0 {
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
	opts *options, out io.Writer, pal report.Palette, now func() time.Time) bool {

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
		NoSandbox: opts.noSandbox, SandboxWrite: opts.sandboxWrite,
		Dir: dir, Agent: spec, Bin: opts.bin, Push: opts.push, Yolo: opts.yolo,
		Timeout: opts.timeout,
		Out:     func(line string) { fmt.Fprintln(out, pal.Dim("  "+line)) },
		Now:     now,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return false
	}
	fmt.Fprintln(out, "Committed. Continuing.")
	return true
}

// assumeFlag names the flag that already answered a prompt on the run's
// behalf, for the message that says so. --yes outranks --yolo, which is the
// order the two are checked in everywhere else.
func assumeFlag(opts *options) string {
	switch {
	case opts.yes:
		return "--yes"
	case opts.yolo:
		return "--yolo"
	}
	return ""
}

// confirmCommit asks whether to hand the tree to an agent. Unattended runs
// keep the error: this writes a commit, which is not something to do to a
// tree nobody is watching unless the flags already said to. term.IsTerminal,
// like confirm's: /dev/null is a character device, and prompting there would
// only read EOF.
func confirmCommit(out io.Writer, opts *options, spec agent.Spec) bool {
	if flag := assumeFlag(opts); flag != "" {
		fmt.Fprintf(out, "Committing with %s first (%s).\n", spec.Label(), flag)
		return true
	}
	if !stdinIsTerminal() {
		return false
	}
	fmt.Fprintf(out, "Commit them with %s first? [y/N] ", spec.Label())
	yes, ok := answeredYes(stdin, false)
	if !ok {
		fmt.Fprintln(out)
	}
	return yes
}

// carriedPending is the part of the current loop this directory had not
// started. An empty result means the loop was complete (or never began).
func carriedPending(d *dirRun) []string {
	if d.r == nil {
		return nil
	}
	return d.r.Pending()
}

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
