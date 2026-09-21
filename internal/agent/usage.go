// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"regexp"
	"strconv"
	"strings"
)

// TailBytes is how much of an agent's output is kept for usage parsing. Usage
// lines sit at the end of a run, so a bounded tail keeps memory flat for
// chatty agents without losing what is parsed.
const TailBytes = 64 << 10

// Usage is what an agent reported about its own token consumption. Output and
// Total are -1 when the agent printed nothing recognizable for them. Thinking
// is the reasoning share of Output, exposed only by the machine-readable
// output modes and the session transcripts; it is 0 when unreported, since
// "no reasoning tokens" and "no reasoning reported" are indistinguishable
// from the outside and neither should show a rate.
type Usage struct {
	Output   int
	Thinking int
	Total    int
}

// Reported returns the count worth displaying: output tokens where the agent
// reports them, else the session total, else 0.
func (u Usage) Reported() int {
	if u.Output >= 0 {
		return u.Output
	}
	if u.Total >= 0 {
		return u.Total
	}
	return 0
}

// Known reports whether the agent said anything about usage at all. Thinking
// does not count: a reasoning share with no output behind it says nothing
// about how much the agent generated.
func (u Usage) Known() bool { return u.Output >= 0 || u.Total >= 0 }

// Best-effort counters for headless agent output. Each family is tried in
// order and every match is collected; the max wins because cumulative per-turn
// prints only ever grow within one run.
var (
	outputTokenRes = []*regexp.Regexp{
		regexp.MustCompile(`"(?:output_tokens|completion_tokens|outputTokens|completionTokens)"\s*:\s*(\d[\d,_]*)`),
		regexp.MustCompile(`(?im)^Output tokens?:\s*(\d[\d,_]*)`),
	}
	thinkingTokenRes = []*regexp.Regexp{
		regexp.MustCompile(`"(?:thinking_tokens|reasoning_tokens|reasoning_output_tokens|thoughtsTokenCount|thinkingTokens)"\s*:\s*(\d[\d,_]*)`),
	}
	totalTokenRes = []*regexp.Regexp{
		regexp.MustCompile(`"(?:total_tokens|totalTokens|totalTokenCount)"\s*:\s*(\d[\d,_]*)`),
		// codex exec's end-of-run summary ("tokens used: 12,345", session total).
		regexp.MustCompile(`(?i)\btokens used\b[^\d\n]{0,20}(\d[\d,_]*)`),
		regexp.MustCompile(`(?im)^Total tokens?:\s*(\d[\d,_]*)`),
	}
)

// ParseUsage extracts token usage from an agent's output tail.
//
// Heuristic by design: headless CLIs print usage in a dozen shapes or not at
// all. A miss leaves the fields at -1; a hit that misreads a label only skews
// a display-only stat, never the loop itself.
func ParseUsage(tail []byte) Usage {
	text := string(tail)
	return Usage{
		Output:   maxMatch(outputTokenRes, text),
		Thinking: maxMatch(thinkingTokenRes, text),
		Total:    maxMatch(totalTokenRes, text),
	}
}

// MayCarryUsage is a fast pre-filter: agents print thousands of lines and only
// a handful mention tokens, so the regexes should not see the rest.
func MayCarryUsage(line string) bool {
	return strings.Contains(line, "oken") || strings.Contains(line, "OKEN")
}

// maxPlausible bounds what a counter may claim. Agents print their own
// output, and that output quotes files, tests, and other agents' JSON, so a
// usage-shaped match can be someone else's sentinel: a stray math.MaxInt64
// read as a count overflows the run total into nonsense. No review generates
// a trillion tokens, so anything above this is a misparse, not a measurement.
const maxPlausible = 1 << 40

func maxMatch(pats []*regexp.Regexp, text string) int {
	best := -1
	for _, p := range pats {
		for _, m := range p.FindAllStringSubmatchIndex(text, -1) {
			end := m[3]
			if end < len(text) {
				next := text[end]
				if next == 'e' || next == 'E' ||
					(next == '.' && end+1 < len(text) && text[end+1] >= '0' && text[end+1] <= '9') {
					continue
				}
			}
			digits := strings.NewReplacer(",", "", "_", "").Replace(text[m[2]:end])
			n, err := strconv.Atoi(digits)
			if err == nil && n > best && n <= maxPlausible {
				best = n
			}
		}
	}
	return best
}

// Tail is a fixed-size ring that keeps only the last TailBytes of a stream.
type Tail struct {
	buf   []byte
	size  int
	off   int // index in buf of the first retained byte
	valid int // bytes currently retained, capped at size
}

// NewTail returns a tail buffer holding at most size bytes.
func NewTail(size int) *Tail { return &Tail{size: size} }

// WriteString appends s and keeps only the last size bytes. Cost is
// O(len(s)): the oldest bytes are abandoned behind a moving offset, never
// shifted, so a chatty stream pays per byte written rather than per byte kept.
func (t *Tail) WriteString(s string) (int, error) {
	n := len(s)
	if n == 0 {
		return 0, nil
	}
	if t.buf == nil {
		t.buf = make([]byte, t.size)
	}
	if n >= t.size {
		copy(t.buf, s[n-t.size:])
		t.off = 0
		t.valid = t.size
		return n, nil
	}
	pos := (t.off + t.valid) % t.size
	if room := t.size - t.valid; n > room {
		t.off = (t.off + n - room) % t.size
		t.valid = t.size
	} else {
		t.valid += n
	}
	first := min(t.size-pos, n)
	copy(t.buf[pos:], s[:first])
	copy(t.buf, s[first:])
	return n, nil
}

// Bytes returns the retained tail. It aliases the ring until the retained
// bytes wrap; when they do it is a fresh copy assembled from both ends.
// Either way: copy before holding past the next Write.
func (t *Tail) Bytes() []byte {
	if t.valid == 0 {
		return nil
	}
	end := t.off + t.valid
	if end <= t.size {
		return t.buf[t.off:end]
	}
	out := make([]byte, t.valid)
	copied := copy(out, t.buf[t.off:])
	copy(out[copied:], t.buf[:end-t.size])
	return out
}
