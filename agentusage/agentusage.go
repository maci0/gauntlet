// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentusage reports how fast AI coding agents are generating tokens,
// by reading what those agents already write down.
//
// It is the public face of the machinery gauntlet uses for its own dashboard,
// so other tools can show the same numbers without repeating the per-agent
// archaeology: which CLI keeps its transcript where, whose counters are
// cumulative rather than per message, and which field means generated output
// rather than context size.
//
// The contract is the one gauntlet holds itself to: every number is something
// an agent actually reported. An agent that reports nothing yields no rate,
// never a zero that reads like a measurement.
//
// Two entry points:
//
//	Watch(tool, dir, since)  live usage for one agent working in one directory
//	Discover()               the agent processes running on this machine
package agentusage

import (
	"context"
	"sort"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/streamjson"
	"github.com/maci0/gauntlet/internal/usagewatch"
)

// Sample is cumulative usage observed since a watcher attached.
type Sample struct {
	// Output is generated tokens: the number a tok/s figure is built from.
	Output int
	// Thinking is the reasoning share of Output, 0 when the agent does not
	// report one separately.
	Thinking int
	// Total is the largest total the agent reported, which for most agents is
	// context size rather than a running cost.
	Total int
	// At is when the reading was taken, so successive samples make a rate.
	At time.Time
}

// Empty reports whether nothing has been observed yet.
func (s Sample) Empty() bool { return s.Output == 0 && s.Thinking == 0 && s.Total == 0 }

// Rate returns tokens per second between two samples, and whether it could be
// computed at all. It never extrapolates: without two readings and a positive
// span there is no rate to report.
func Rate(prev, cur Sample) (float64, bool) {
	span := cur.At.Sub(prev.At).Seconds()
	if span <= 0 || cur.Output <= prev.Output {
		return 0, false
	}
	return float64(cur.Output-prev.Output) / span, true
}

// Agents lists every agent name this package knows, built in and defined.
func Agents() []string {
	out := append([]string(nil), agent.Valid...)
	out = append(out, agent.CustomNames()...)
	sort.Strings(out)
	return out
}

// Supported reports whether live usage can be read for an agent from its own
// transcripts. An agent outside this set can still be measured from its
// machine-readable output, which the caller would have to be capturing.
func Supported(tool string) bool { return usagewatch.Supported(tool) }

// LoadDefinitions reads agent definitions from a JSON file, the same format
// gauntlet uses (~/.gauntlet/agents.json). It teaches this package about
// agents it was not compiled to know, including where they keep transcripts.
// A missing file is not an error.
func LoadDefinitions(path string) error { return agent.LoadCustomFile(path) }

// DefinitionsPath is where agent definitions live by default.
func DefinitionsPath() string { return agent.CustomFilePath() }

// Watcher follows one agent's transcripts and reports what it spends.
type Watcher struct {
	inner *usagewatch.Watcher
	tool  string
	dir   string
}

// Watch starts reading usage for one agent working in one directory. It
// returns nil when that agent keeps no readable transcript, which callers
// should treat as "no rate available" rather than an error.
//
// since bounds which transcripts are considered, and anything already written
// when the watcher attaches belongs to whatever ran before: only growth from
// now on is counted.
func Watch(tool, dir string, since time.Time) *Watcher {
	w := usagewatch.New(tool, dir, since)
	if w == nil {
		return nil
	}
	return &Watcher{inner: w, tool: tool, dir: dir}
}

// Tool is the agent this watcher follows.
func (w *Watcher) Tool() string {
	if w == nil {
		return ""
	}
	return w.tool
}

// Dir is the working directory this watcher attributes usage to.
func (w *Watcher) Dir() string {
	if w == nil {
		return ""
	}
	return w.dir
}

// Read takes one reading now. It is safe to call from any goroutine, and safe
// on a nil watcher, which reports nothing.
func (w *Watcher) Read() Sample {
	if w == nil {
		return Sample{}
	}
	return convert(w.inner.Poll())
}

// Run reads on an interval until the context is canceled, calling onChange
// whenever the observed usage grows. Pass 0 for the default cadence.
func (w *Watcher) Run(ctx context.Context, every time.Duration, onChange func(Sample)) {
	if w == nil {
		return
	}
	w.inner.Run(ctx, every, func(s usagewatch.Sample) {
		if onChange != nil {
			onChange(convert(s))
		}
	})
}

func convert(s usagewatch.Sample) Sample {
	return Sample{Output: s.Output, Thinking: s.Thinking, Total: s.Total, At: time.Now()}
}

// StreamEvent is one line of an agent's machine-readable output.
type StreamEvent struct {
	Text     string
	Thinking string
	Kind     string
	Cwd      string
	Output   int
	Reason   int
	Total    int
	Input    int
}

// ParseStream reads one line of an agent's JSON output mode, for callers that
// capture agent stdout themselves. ok is false when the line is not JSON, which
// is how a caller knows to treat it as ordinary text.
//
// It models no single agent's envelope: values are collected by key, so the
// Anthropic, OpenAI, and Gemini shaped dialects all work, and a shape nobody
// anticipated contributes nothing rather than something wrong.
func ParseStream(line []byte) (StreamEvent, bool) {
	ev, ok := streamjson.Parse(line)
	if !ok {
		return StreamEvent{}, false
	}
	return StreamEvent{
		Text: ev.Text, Thinking: ev.Thinking, Kind: ev.Kind, Cwd: ev.Cwd,
		Output: ev.Usage.Output, Reason: ev.Usage.Thinking,
		Total: ev.Usage.Total, Input: ev.Usage.Input,
	}, true
}

// StreamFlags returns the arguments that put an agent into its machine
// readable mode, or nil when it has none. Each was read from that CLI's own
// help output.
func StreamFlags(tool string) []string { return agent.StreamFlags(tool) }
