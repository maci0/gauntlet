// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// --semcode: the optional index the semantic queries in some reviews need.
// Building it is a precondition of the run rather than a review of its own, so
// it runs once per directory before the loop and its failure is the run's.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runx"
)

// semcodeIndexTimeout bounds one directory's index build. The indexer walks
// the whole tree before the first review starts; without a cap a wedged build
// would hang the run indefinitely. It is a var so tests can shrink it;
// production always sees 30 minutes.
var semcodeIndexTimeout = 30 * time.Minute

// semcodeIndexer is the helper --semcode builds the index with. Named once so
// the check run() makes before taking the locks, the one buildSemcodeIndex
// makes when it gets there, and the dry-run warning cannot each go looking for
// a different binary.
const semcodeIndexer = "semcode-index"

// buildSemcodeIndex runs the indexer once per directory before the loop, so
// reviews can answer call-graph and type queries from an index.
func buildSemcodeIndex(ctx context.Context, out io.Writer, runs []*dirRun) int {
	idx := agent.Resolve(semcodeIndexer)
	if idx == "" {
		fmt.Fprintf(os.Stderr, "Required tool not found in PATH: %s\n", semcodeIndexer)
		return exitUsage
	}
	for _, d := range runs {
		fmt.Fprintf(out, "Building semcode index in %s\n", d.dir)
		ictx, cancel := context.WithTimeout(ctx, semcodeIndexTimeout)
		code := runIndexer(ictx, idx, []string{"-s", "."}, d.dir)
		// The deadline is latched by the context that fired it, so reading it
		// after the cancel below still answers DeadlineExceeded for a build
		// that ran out of budget; a deadline that had not fired yet is not a
		// timeout to report.
		timedOut := errors.Is(ictx.Err(), context.DeadlineExceeded)
		cancel()
		if code == 0 {
			continue
		}
		if ctx.Err() != nil {
			return exitInterrupted
		}
		switch {
		case timedOut:
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

// runIndexer runs a helper binary to completion. Its output is not parsed or
// stored, only filtered on its way to the terminal.
//
// Both streams pass through the same display filter as every other child
// output, because the indexer reports what it walked and the reviewed tree
// names the files: a repository that ships a file name carrying an escape
// sequence or a bidi override would otherwise drive the operator's terminal
// through the one child whose output nobody filters.
//
// The child gets its own process group and the deadline kill takes down the
// whole group: semcode-index is an external binary that may have children of
// its own, and killing only its pid would orphan them (see runProc for the
// same rule applied to agents).
func runIndexer(ctx context.Context, bin string, args []string, dir string) int {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = runx.AbsPATHEnv()
	// One writer per stream: DisplayWriter is per-stream state, and the copy
	// goroutines for stdout and stderr run concurrently.
	stdout, stderr := normalize.NewDisplayWriter(os.Stdout), normalize.NewDisplayWriter(os.Stderr)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runx.Guard(cmd, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	err := cmd.Run()
	// Run has joined both copy goroutines, so whatever each held is final.
	if ferr := errors.Join(stdout.Flush(), stderr.Flush()); err == nil {
		err = ferr
	}
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}
