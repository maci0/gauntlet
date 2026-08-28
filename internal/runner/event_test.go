// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"sync"
	"testing"
	"time"
)

// TestBusPublishAfterClose pins the shutdown contract: the auto-update and
// hot-reload watchers can hold an event past the end of the run, and a Publish
// landing after Close must be dropped, not panic on a closed channel.
func TestBusPublishAfterClose(t *testing.T) {
	bus := NewBus()
	ch := bus.Subscribe(4)
	bus.Close()

	bus.Publish(Event{Kind: EvLog, Text: "late"})
	if _, ok := <-ch; ok {
		t.Fatal("closed bus delivered an event")
	}
}

func TestBusCloseIdempotent(t *testing.T) {
	bus := NewBus()
	bus.Subscribe(1)
	bus.Close()
	bus.Close() // second close must not re-close the channels
}

// TestBusSubscribeAfterClose gives a late subscriber a closed channel so its
// consumer exits instead of blocking forever.
func TestBusSubscribeAfterClose(t *testing.T) {
	bus := NewBus()
	bus.Close()
	ch := bus.Subscribe(1)
	if _, ok := <-ch; ok {
		t.Fatal("subscription to a closed bus is not closed")
	}
}

// TestBusInjectedClock pins the stamping contract: an event published without
// a timestamp gets the bus's Now when one is injected, time.Now otherwise.
func TestBusInjectedClock(t *testing.T) {
	stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	bus := NewBus()
	bus.Now = func() time.Time { return stamp }
	ch := bus.Subscribe(1)
	bus.Publish(Event{Kind: EvLog})
	if got := <-ch; !got.Time.Equal(stamp) {
		t.Fatalf("injected clock ignored: got %v, want %v", got.Time, stamp)
	}

	plain := NewBus()
	ch = plain.Subscribe(1)
	plain.Publish(Event{Kind: EvLog})
	if got := <-ch; got.Time.IsZero() {
		t.Fatal("nil clock left the event unstamped")
	}
}

// TestBusConcurrentClose exercises the publish/close race under the detector:
// publishers running while Close lands must either deliver or drop, never
// send into a closed channel.
func TestBusConcurrentClose(t *testing.T) {
	bus := NewBus()
	subs := make([]<-chan Event, 4)
	for i := range subs {
		subs[i] = bus.Subscribe(64)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for i := range 200 {
				bus.Publish(Event{Kind: EvOutput, Text: "line", Repeat: i})
				bus.Publish(Event{Kind: EvLog, Text: "log"})
			}
		})
	}
	// Non-output events block until delivered, so consumers run alongside the
	// publishers, exactly as the journal, reporter, and TUI do during a run.
	var drained sync.WaitGroup
	for _, ch := range subs {
		drained.Go(func() {
			for range ch {
			}
		})
	}
	time.Sleep(time.Millisecond)
	bus.Close()
	wg.Wait()
	drained.Wait()
}
