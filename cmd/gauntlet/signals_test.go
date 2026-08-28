// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// driveInterrupts starts the interrupt stage machine on a plain channel, so a
// test can deliver "signals" without touching process-global signal state:
// signal.Notify fans every delivery out to all registered channels and never
// unregisters, so signaling the real process would leave each test's handler
// armed for the next one.
func driveInterrupts(t *testing.T) (chan<- os.Signal, context.Context, *gracefulStop, <-chan int) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	graceful := &gracefulStop{}
	ch := make(chan os.Signal, 3)
	exited := make(chan int, 1)
	go watchInterrupts(ctx, ch, stop, io.Discard, graceful, func(code int) { exited <- code })
	return ch, ctx, graceful, exited
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The staged Ctrl-C: the first is the graceful quit -- the review in flight
// finishes and lands its work, whichever agent CLI is running it -- the second
// terminates the running reviews, the third force-kills the process.
func TestInterruptIsStagedGracefulThenStopThenKill(t *testing.T) {
	ch, ctx, graceful, exited := driveInterrupts(t)

	ch <- os.Interrupt
	waitFor(t, "the first Ctrl-C asking for the graceful quit", graceful.asking)
	// The graceful quit must not have terminated anything: the whole point is
	// that the review in flight keeps running. Give the handler room to have
	// gotten it wrong before checking.
	time.Sleep(20 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("the first Ctrl-C terminated the run instead of draining it")
	}

	ch <- os.Interrupt
	waitFor(t, "the second Ctrl-C terminating the run", func() bool { return ctx.Err() != nil })

	ch <- os.Interrupt
	select {
	case code := <-exited:
		if code != 128+int(syscall.SIGINT) {
			t.Fatalf("force-kill exited %d, want %d", code, 128+int(syscall.SIGINT))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the third Ctrl-C never force-killed")
	}
}

// Once a finish was asked for by any path -- SIGQUIT, `s` on the dashboard, a
// tripped usage limit -- the operator has already seen the "finishing"
// message, and a Ctrl-C on top of it means "stop now", not a second request
// to keep draining.
func TestInterruptAfterAFinishRequestStopsNow(t *testing.T) {
	ch, ctx, graceful, _ := driveInterrupts(t)

	graceful.request(io.Discard)
	ch <- os.Interrupt
	waitFor(t, "Ctrl-C after a finish request terminating the run",
		func() bool { return ctx.Err() != nil })
}

// SIGTERM is not staged: it comes from a supervisor or a kill, both of which
// mean "stop now", and a service manager escalating to SIGKILL on its own
// schedule must not be met with a run that decided to keep going.
func TestSigtermStopsImmediately(t *testing.T) {
	ch, ctx, graceful, _ := driveInterrupts(t)

	ch <- syscall.SIGTERM
	waitFor(t, "SIGTERM terminating the run", func() bool { return ctx.Err() != nil })
	if graceful.asking() {
		t.Fatal("SIGTERM must not be softened into a finish request")
	}
}
