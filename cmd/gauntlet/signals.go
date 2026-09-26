// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

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
			case _, ok := <-quit:
				if !ok {
					return
				}
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
		var ok bool
		select {
		case sig, ok = <-ch:
			if !ok {
				return
			}
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
		if _, ok := <-ch; !ok {
			return
		}
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
