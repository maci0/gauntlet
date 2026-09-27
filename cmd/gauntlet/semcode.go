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
	"github.com/maci0/gauntlet/internal/runx"
)

// semcodeIndexTimeout bounds one directory's index build. The indexer walks
// the whole tree before the first review starts; without a cap a wedged build
// would hang the run indefinitely. It is a var so tests can shrink it;
// production always sees 30 minutes.
var semcodeIndexTimeout = 30 * time.Minute

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
			return 128 + int(syscall.SIGINT)
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

// indexerWaitGrace is how long Wait may outlive the deadline kill before it
// gives up on an unreapable child, the same insurance runProc carries.
const indexerWaitGrace = 10 * time.Second

// runIndexer runs a helper binary to completion, streaming nothing: its output
// goes straight to the terminal.
//
// The child gets its own process group and the deadline kill takes down the
// whole group: semcode-index is an external binary that may have children of
// its own, and killing only its pid would orphan them (see runProc for the
// same rule applied to agents).
func runIndexer(ctx context.Context, bin string, args []string, dir string) int {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = runx.AbsPATHEnv()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	runx.Guard(cmd, indexerWaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}
