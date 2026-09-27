// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
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
	d := newDashboard(Config{}, events, prog)

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
		if !ok || m.Kind != ev.Kind || m.Review != ev.Review {
			t.Fatalf("message %d is %#v, want kind %q review %q", i, got[i], ev.Kind, ev.Review)
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
	d := newDashboard(Config{}, events, prog)
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
	// then and drops it. Nothing else may reach it.
	for i, msg := range prog.seen() {
		if _, ok := msg.(doneMsg); !ok {
			t.Fatalf("message %d is %#v after shutdown, want doneMsg or nothing", i, msg)
		}
	}
}
