// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/runner"
	"github.com/maci0/gauntlet/internal/selfupdate"
)

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
	// StackHead / StackHeadTip are the last published layer when the run was
	// interrupted. The next --max-loops pass cuts from this tip so later
	// rounds see earlier fixes. StackPublished is how many PRs are already
	// in the chain, for layer numbers in later PR bodies.
	StackHead      string `json:"stack_head,omitempty"`
	StackHeadTip   string `json:"stack_head_tip,omitempty"`
	StackPublished int    `json:"stack_published,omitempty"`
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
// A handoff written before a wall clock that was later set back (or carrying
// a corrupt negative) would otherwise resume in the future, so the run then
// has extra runtime no clock reading can account for; treat that as a fresh
// start instead.
func resumeStart(now time.Time, prior handoff) time.Time {
	elapsed := prior.Elapsed
	if elapsed == 0 && !prior.StartedAt.IsZero() {
		elapsed = now.Sub(prior.StartedAt)
	}
	return now.Add(-max(elapsed, 0))
}

// startReloadWatch arms the hot-reload watcher. When the executable changes,
// every runner is asked to stop at its next quiescent point; the exec happens
// after the summary is written.
func startReloadWatch(ctx context.Context, opts *options, runs []*dirRun, bus *runner.Bus) *atomic.Pointer[string] {
	var pending atomic.Pointer[string]
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
		if d.r != nil {
			dh.StackHead, dh.StackHeadTip = d.r.StackHead()
			dh.StackPublished = d.r.StackPublished()
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
