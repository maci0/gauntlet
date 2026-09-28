// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"sync"
	"time"

	"github.com/maci0/gauntlet/internal/normalize"
)

// Kind identifies what an Event reports. Values are stable: they are the "ev"
// field of every line written to the run journal.
type Kind string

const (
	EvRunStart    Kind = "run_start"
	EvLoopStart   Kind = "loop_start"
	EvReviewStart Kind = "review_start"
	EvReviewEnd   Kind = "review_end"
	EvMerge       Kind = "merge"
	EvPullRequest Kind = "pull_request"
	EvCommit      Kind = "commit"
	EvLoopEnd     Kind = "loop_end"
	EvRunEnd      Kind = "run_end"
	EvUsage       Kind = "usage"  // an agent reported more tokens mid-run
	EvLog         Kind = "log"    // runner narration
	EvOutput      Kind = "output" // one normalized line from an agent
	EvReload      Kind = "reload" // a new binary is on disk
)

// Status is the outcome of one review.
type Status string

const (
	StatusOK          Status = "ok"
	StatusFail        Status = "fail"
	StatusTimeout     Status = "timeout"
	StatusInterrupted Status = "interrupted"
	StatusSkipped     Status = "skipped"
	StatusConflict    Status = "conflict" // ran fine, but its branch would not merge
)

// Failed reports whether a status should make the run exit nonzero.
func (s Status) Failed() bool {
	switch s {
	case StatusFail, StatusTimeout, StatusSkipped, StatusConflict:
		return true
	}
	return false
}

// Event is one thing that happened during a run. It is both the TUI's input
// and one line of the JSONL journal, so fields stay flat and omitempty.
type Event struct {
	Kind Kind      `json:"ev"`
	Time time.Time `json:"ts"`

	Dir    string `json:"dir,omitempty"`
	Review string `json:"review,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Loop   int    `json:"loop,omitempty"`

	// PromptSHA is the SHA-256, hex-encoded, of the review prompt text this
	// launch was composed from. It sits on review_start and review_end: the
	// journal then answers "which words produced this output" without the
	// prompt file still existing or matching what ran.
	PromptSHA string `json:"prompt_sha256,omitempty"`

	// Attempt is which try this is, from 1. Above 1 it says a retry is under
	// way, which is otherwise invisible: the review looks like it restarted.
	Attempt int `json:"attempt,omitempty"`

	Status   Status  `json:"status,omitempty"`
	ExitCode *int    `json:"exit_code,omitempty"`
	Elapsed  float64 `json:"elapsed_s,omitempty"`

	// Lines added and removed by this review, or by this loop. Nil when git is
	// unavailable, or when concurrent reviews share one tree and no honest
	// attribution is possible.
	Ins *int `json:"ins,omitempty"`
	Del *int `json:"del,omitempty"`

	Tokens int `json:"tokens,omitempty"`
	// Thinking is the reasoning share of Tokens, when the agent reports it.
	Thinking int `json:"thinking,omitempty"`

	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	URL    string `json:"url,omitempty"`

	// Text carries narration (EvLog), agent output (EvOutput), or a detail
	// string on other events.
	Text string `json:"text,omitempty"`
	// LineKind classifies EvOutput text.
	LineKind normalize.Kind `json:"line_kind,omitempty"`
	Repeat   int            `json:"repeat,omitempty"`

	// Version appears on EvRunStart so a journal line is self-describing.
	Version string   `json:"version,omitempty"`
	Agents  []string `json:"agents,omitempty"`
	Total   int      `json:"total,omitempty"` // reviews scheduled per loop
	// Seed is the effective RNG seed, on EvRunStart only: review shuffles and
	// agent sampling replay from it.
	Seed uint64 `json:"seed,omitempty"`
}

// Bus fans one run's events out to every subscriber (logger, journal, TUI).
//
// Publishing must never block the scheduler: an agent that prints thousands of
// lines per second cannot be allowed to slow the reviews down. Output and live
// usage ticks are therefore droppable; results are never dropped.
//
// Background publishers (the auto-update check, the hot-reload watcher) can
// still hold a live event when the run ends, so a Publish that lands after
// Close is dropped rather than sent into a closed channel.
type Bus struct {
	mu     sync.RWMutex
	subs   []chan Event
	closed bool

	// inflight counts the Publish calls that have read the subscriber list and
	// are delivering to it. A delivery to a stalled subscriber blocks, and it
	// must not block anything else: Close waits on this counter instead of on
	// the subscriber lock, and a subscriber list that is being read is never
	// mutated under a reader.
	inflight sync.WaitGroup

	// Now is the run's clock. It stamps events that arrive without a
	// timestamp of their own, and the runner reads it for start time,
	// elapsed, the runtime budget, and a clock-derived seed. Injectable
	// for tests; nil means time.Now.
	Now func() time.Time
}

// NewBus returns a bus with no subscribers.
func NewBus() *Bus { return &Bus{} }

// now is the bus clock: the injected Now, or wall time. Nil-safe so New can
// stamp a start time before the runner exists.
func (b *Bus) now() time.Time {
	if b != nil && b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// Clock returns the bus's clock as a handle, so a consumer outside this
// package (the dashboard, the reporter) reads the same one the runner does
// rather than its own wall clock. Wall time when none was injected.
func (b *Bus) Clock() func() time.Time {
	if b != nil && b.Now != nil {
		return b.Now
	}
	return time.Now
}

// Subscribe returns a channel receiving every future event. It is safe to call
// while a run is publishing; the returned channel is closed by Close. On a
// closed bus it returns an already-closed channel, so a late subscriber drains
// immediately instead of waiting forever.
func (b *Bus) Subscribe(buffer int) <-chan Event {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(ch)
		return ch
	}
	b.subs = append(b.subs, ch)
	return ch
}

// Droppable reports whether a full subscriber may miss this event, and
// whether it is written to the run journal. Output and live usage are
// high-volume and reconstructible (the final counts ride on review_end);
// everything else is a result and is delivered and journaled.
func Droppable(k Kind) bool { return k == EvOutput || k == EvUsage }

// Publish delivers an event to every subscriber. Droppable kinds are skipped
// for a subscriber whose buffer is full; every other kind blocks until
// delivered. After Close it does nothing.
//
// The subscriber list is read under the lock and the deliveries happen outside
// it, because a delivery to a subscriber that stopped draining blocks, and
// holding the lock across it would park every Subscribe and Close behind that
// one subscriber. Close waits for the deliveries already under way before it
// closes the channels, which is why the snapshot is safe.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = b.now()
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	subs := b.subs
	b.inflight.Add(1)
	b.mu.RUnlock()
	defer b.inflight.Done()
	for _, ch := range subs {
		if Droppable(e.Kind) {
			select {
			case ch <- e:
			default:
			}
			continue
		}
		ch <- e
	}
}

// Close closes every subscriber channel, after the deliveries already in
// flight have finished, so no send lands on a closed channel. Later Publish
// calls are dropped, and Close itself is idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := b.subs
	b.subs = nil
	b.mu.Unlock()
	b.inflight.Wait()
	for _, ch := range subs {
		close(ch)
	}
}
