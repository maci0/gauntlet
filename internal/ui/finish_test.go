// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"reflect"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/maci0/gauntlet/internal/runner"
)

// recorder is a program that writes down every message it is handed.
type recorder struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (r *recorder) Send(msg tea.Msg) {
	r.mu.Lock()
	r.msgs = append(r.msgs, msg)
	r.mu.Unlock()
}

func (r *recorder) Run() (tea.Model, error) { return nil, nil }
func (r *recorder) Quit()                   {}
func (r *recorder) Kill()                   {}

// parked is a program that behaves like a real one before Run: Send blocks,
// because the message channel is unbuffered and nothing serves it yet. Only
// Kill releases it, as a real program cancels its own context.
type parked struct {
	killed chan struct{}
	once   sync.Once
}

func newParked() *parked { return &parked{killed: make(chan struct{})} }

func (p *parked) Send(tea.Msg) { <-p.killed }
func (p *parked) Run() (tea.Model, error) {
	return nil, nil
}
func (p *parked) Quit() {}
func (p *parked) Kill() { p.once.Do(func() { close(p.killed) }) }

func (r *recorder) seen() []tea.Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tea.Msg(nil), r.msgs...)
}

// Finish is called from a second goroutine once the workers are done, and it
// shares the program's unbuffered message channel with the event forwarder.
// Two senders into one channel are unordered, so the marker has to travel
// through the forwarder: applied ahead of the run's last events it stops the
// tick, and the summary screen freezes one sample short of the truth.
func TestFinishFollowsQueuedEvents(t *testing.T) {
	events := make(chan runner.Event, 8)
	prog := &recorder{}
	d := newDashboard(events, prog)

	queued := []runner.Event{
		{Kind: runner.EvReviewEnd, Review: "a"},
		{Kind: runner.EvLoopEnd, Loop: 1},
		{Kind: runner.EvReviewEnd, Review: "b"},
	}
	for _, ev := range queued {
		events <- ev
	}

	d.Finish()
	close(events)

	got := prog.seen()
	if len(got) != len(queued)+1 {
		t.Fatalf("program got %d messages, want %d: %v", len(got), len(queued)+1, got)
	}
	for i, ev := range queued {
		m, ok := got[i].(eventMsg)
		if !ok || !reflect.DeepEqual(runner.Event(m), ev) {
			t.Fatalf("message %d is %#v, want exactly %#v", i, got[i], ev)
		}
	}
	if _, ok := got[len(got)-1].(doneMsg); !ok {
		t.Fatalf("last message is %#v, want doneMsg", got[len(got)-1])
	}
}

// Finish must not wait for a program that is gone: the bus is closed and the
// forwarder is returning, so there may be no acknowledgement left to receive.
func TestFinishReturnsAfterTheBusCloses(t *testing.T) {
	events := make(chan runner.Event)
	prog := &recorder{}
	d := newDashboard(events, prog)
	close(events)

	done := make(chan struct{})
	go func() { d.Finish(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Finish blocked after the event channel closed")
	}
	// The marker may still be delivered when the forwarder happened to be in
	// its select when Finish arrived; a real program is already shut down by
	// then and drops it. Nothing else may reach it, and an empty log only
	// satisfies the second half vacuously.
	for i, msg := range prog.seen() {
		if _, ok := msg.(doneMsg); ok {
			if i != 0 {
				t.Fatalf("doneMsg arrived at position %d, want it first if at all", i)
			}
			continue
		}
		t.Fatalf("message %d is %#v after shutdown, want doneMsg or nothing", i, msg)
	}
}

// A run can fail between subscribing the dashboard and entering Run, leaving
// the forwarder parked in Send on a channel no one serves. Only Run or Release
// shuts that program down, so without Release the goroutine, and the bus
// subscription with it, is stranded for the rest of the process.
func TestReleaseJoinsTheForwarderWithoutRun(t *testing.T) {
	events := make(chan runner.Event, 1)
	prog := newParked()
	d := newDashboard(events, prog)

	// Queued while the forwarder is still finding its feet, so it reaches Send.
	events <- runner.Event{Kind: runner.EvLog, Text: "runner is starting"}

	done := make(chan struct{})
	go func() { d.Release(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Release blocked on a program Run was never entered on")
	}

	// Joined, not merely signalled: the forwarder is gone, not on its way out.
	select {
	case <-d.forwarded:
	default:
		t.Fatal("Release returned before the forwarder did")
	}

	// Idempotent, so a path that releases and then unwinds through another
	// return does not block on the second call.
	d.Release()
}
