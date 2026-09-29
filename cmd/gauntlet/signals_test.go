// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
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
	return driveInterruptsKilling(t, func() {})
}

// driveInterruptsKilling is driveInterrupts with the force-kill's agent-group
// kill observable, so a test can assert it ran before the process left.
func driveInterruptsKilling(t *testing.T, kill func()) (chan<- os.Signal, context.Context, *gracefulStop, <-chan int) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	graceful := &gracefulStop{}
	ch := make(chan os.Signal, 3)
	exited := make(chan int, 1)
	go watchInterrupts(ctx, ch, stop, io.Discard, graceful, kill, func(code int) { exited <- code })
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

// The force-kill leaves through os.Exit, which runs no deferred function, so
// the group kill every agent launch defers on its own return never fires on
// that path. The handler kills the live agent groups itself, and does it
// before it exits rather than after.
func TestForceKillStopsTheAgentsItLeavesBehind(t *testing.T) {
	var killed bool
	ch, _, graceful, exited := driveInterruptsKilling(t, func() { killed = true })

	graceful.request(io.Discard)
	ch <- os.Interrupt
	ch <- os.Interrupt
	select {
	case code := <-exited:
		if code != 128+int(syscall.SIGINT) {
			t.Fatalf("force-kill exited %d, want %d", code, 128+int(syscall.SIGINT))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second Ctrl-C after a finish request never force-killed")
	}
	if !killed {
		t.Fatal("the force-kill left without stopping the agents it launched")
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

// arm is where a finish request that arrived before the runners existed is
// handed to them, and where the run decides which stream the "Finishing" line
// belongs on. That stream is the log file or io.Discard under --tui, because a
// raw write into the alt screen leaves stray text across the frame, which is
// why the dashboard's own `s` key asks with no writer at all (main.go's
// OnFinish) and relies on the armed one. Both halves are pinned here: arming
// asks for nothing by itself, and a later request lands on the armed stream
// rather than the one it was handed.
func TestArmedRequestLandsOnTheRunStream(t *testing.T) {
	var armed, callers bytes.Buffer
	g := &gracefulStop{}
	g.arm(nil, &armed)

	if g.asking() {
		t.Fatal("arming the runners must not ask for a finish nobody requested")
	}
	if armed.Len() != 0 {
		t.Fatalf("arming wrote %q before anything asked for a finish", armed.String())
	}

	// What the dashboard's finish key does: ask without a writer of its own.
	g.request(nil)
	if !g.asking() {
		t.Fatal("a request must be recorded, so the runners behind it can see it")
	}
	if !strings.Contains(armed.String(), "Finishing:") {
		t.Fatalf("the finish notice belongs on the armed stream, got %q", armed.String())
	}

	// A request made later by a path that has a writer of its own still goes
	// to the armed one, for the same reason.
	g.request(&callers)
	if callers.Len() != 0 {
		t.Fatalf("the notice went to the caller's stream too, %q", callers.String())
	}
}

// A finish asked for before the runners were armed applies the moment they
// are, and the operator sees the notice once: they have already seen it.
func TestArmAppliesAnEarlyRequestWithoutAskingTwice(t *testing.T) {
	var early, armed bytes.Buffer
	g := &gracefulStop{}
	g.request(&early)
	if !strings.Contains(early.String(), "Finishing:") {
		t.Fatalf("the first request should announce itself, got %q", early.String())
	}

	g.arm(nil, &armed)
	if !g.asking() {
		t.Fatal("arming must leave an early request standing")
	}
	if armed.Len() != 0 {
		t.Fatalf("the notice was printed a second time on arming: %q", armed.String())
	}
}

// A closed channel (such as during test cleanup or teardown) must return cleanly
// without being misinterpreted as an interrupt or triggering a force-kill.
func TestInterruptClosedChannelDoesNotForceKill(t *testing.T) {
	ch, _, _, exited := driveInterrupts(t)
	close(ch)
	select {
	case code := <-exited:
		t.Fatalf("closing channel force-killed with code %d", code)
	case <-time.After(50 * time.Millisecond):
	}
}
