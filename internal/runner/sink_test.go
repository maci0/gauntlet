// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runx"
)

// TestOutputSinkRedactsCredentials pins what the sink owes every subscriber
// of EvOutput. The journal writes these lines to disk and the TUI keeps them
// in scrollback, and an agent prints a rejected key by naming it, so the bulk
// of what the model said carries the same exposure the subject and the file
// notes are held to.
func TestOutputSinkRedactsCredentials(t *testing.T) {
	bus := NewBus()
	events := bus.Subscribe(8)
	r := &Runner{bus: bus, cfg: Config{Dir: "/repo"}}

	sink := r.outputSink("sec-review", "claude:sonnet")
	if sink == nil {
		t.Fatal("sink is nil without --quiet")
	}
	sink(normalize.Line{
		Text:   "ANTHROPIC_API_KEY=sk-abcdefghijklmnopqrst rejected",
		Repeat: 1,
	})

	select {
	case ev := <-events:
		if ev.Kind != EvOutput {
			t.Fatalf("kind = %q, want %q", ev.Kind, EvOutput)
		}
		if strings.Contains(ev.Text, "sk-abcdefghijklmnopqrst") {
			t.Fatalf("event carries the credential: %q", ev.Text)
		}
		if !strings.Contains(ev.Text, runx.Redacted) {
			t.Fatalf("event does not carry the redaction marker: %q", ev.Text)
		}
		// Redaction rewrites the value, not the line: the name and the shape
		// around it are what a reader needs to recognize the failure.
		if !strings.HasPrefix(ev.Text, "ANTHROPIC_API_KEY=") {
			t.Fatalf("redaction changed the line's shape: %q", ev.Text)
		}
		if ev.LineKind != normalize.Plain || ev.Repeat != 1 || ev.Review != "sec-review" {
			t.Fatalf("sink dropped fields: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event published")
	}
}

// TestOutputSinkKeepsOrdinaryOutput pins the other half: a line with no
// credential in it reaches the subscribers byte for byte.
func TestOutputSinkKeepsOrdinaryOutput(t *testing.T) {
	bus := NewBus()
	events := bus.Subscribe(8)
	r := &Runner{bus: bus, cfg: Config{Dir: "/repo"}}

	const line = "updated the token counter in internal/agent/usage.go"
	r.outputSink("code-review", "claude")(normalize.Line{Text: line, Repeat: 1})

	select {
	case ev := <-events:
		if ev.Text != line {
			t.Fatalf("text = %q, want %q", ev.Text, line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event published")
	}
}

// TestOutputSinkDropsOutputWhenQuiet keeps --quiet at the source: a quiet run
// must not pay for a sink it never reads.
func TestOutputSinkDropsOutputWhenQuiet(t *testing.T) {
	r := &Runner{bus: NewBus(), cfg: Config{Dir: "/repo", Quiet: true}}
	if r.outputSink("code-review", "claude") != nil {
		t.Fatal("sink is not nil under --quiet")
	}
}
