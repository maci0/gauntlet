// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package streamjson reads the machine-readable output modes agent CLIs offer.
//
// Most agents can emit JSONL instead of prose (`--output-format stream-json`
// and its variants). That stream carries three things gauntlet wants and the
// text mode hides: token usage as it accrues, the split between reasoning and
// visible output, and clean text free of spinners and escape codes.
//
// The envelopes differ per agent and change between releases, so this does not
// model any one of them. It walks the decoded JSON and picks up values by key,
// which means an agent that renames its wrapper keeps working and an agent
// that renames its usage fields degrades to "no numbers" instead of to wrong
// numbers. Nothing here guesses: a key that is not recognized contributes
// nothing.
package streamjson

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// Event is what one JSON line contributed.
type Event struct {
	// Text is visible assistant output, already concatenated.
	Text string
	// Thinking is reasoning output, which agents mark separately.
	Thinking string
	// Usage is any token counters found on the line. A counter the line did
	// not carry stays at zero, which is also what a zero counter reports:
	// "none found" and "found, and it was nothing" are the same answer here,
	// and the caller treats both as unknown (see runner.pick).
	Usage Usage
}

// Usage holds token counters found on one line. Agents report a mix of
// per-message and cumulative values; the caller decides which to trust by
// taking the maximum it has seen.
type Usage struct {
	Output   int
	Thinking int
	Total    int
}

// Keys recognized as token counters, mapped onto the fields above. These are
// the names used by the Anthropic, OpenAI, and Gemini shaped APIs, which every
// supported agent's output follows in one dialect or another.
var (
	outputKeys = map[string]bool{
		"output_tokens": true, "outputtokens": true, "completion_tokens": true,
		"completiontokens": true, "candidatestokencount": true, "output": true,
		"outputtokencount": true,
	}
	thinkingKeys = map[string]bool{
		"thinking_tokens": true, "thinkingtokens": true, "reasoning_tokens": true,
		"reasoning_output_tokens": true, "thoughtstokencount": true,
		"reasoningtokens": true, "reasoning": true, "thinking": true,
	}
	totalKeys = map[string]bool{
		"total_tokens": true, "totaltokens": true, "totaltokencount": true,
	}
	// Fields whose string value is visible assistant text.
	textKeys = map[string]bool{"text": true, "content": true, "delta": true, "message": true}
	// Fields whose string value is reasoning output.
	thinkingTextKeys = map[string]bool{
		"thinking": true, "reasoning": true, "thought": true, "reasoning_content": true,
	}
)

// Parse reads one line of an agent's JSON stream. ok is false when the line is
// not JSON at all, which is how a caller knows to treat it as plain text.
//
// This runs once per output line, so nothing here copies the line: TrimSpace
// slices it, and json.Unmarshal neither retains nor modifies its input.
func Parse(line []byte) (Event, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Event{}, false
	}
	var doc any
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return Event{}, false
	}
	var ev Event
	var text, thinking strings.Builder
	walk(doc, &ev, &text, &thinking, 0, false)
	ev.Text = strings.TrimRight(text.String(), "\n")
	ev.Thinking = strings.TrimRight(thinking.String(), "\n")
	return ev, true
}

// maxDepth bounds the walk. Agent envelopes nest a few levels; anything deeper
// is a tool result payload, whose contents are not this package's business.
const maxDepth = 8

// walk descends one decoded line. The rule that makes this envelope-agnostic:
// a text-ish key contributes text only when its value is a string, and any
// container is descended into instead. That way "message" or "content" can be
// a string in one agent's dialect and an object holding usage counters in
// another's, and both work.
//
// inThinking is inherited: once inside a reasoning block, the plain text of
// every nested part is reasoning too.
func walk(node any, ev *Event, text, thinking *strings.Builder, depth int, inThinking bool) {
	if depth > maxDepth {
		return
	}
	switch v := node.(type) {
	case map[string]any:
		thinkingHere := inThinking || isThinkingBlock(v)
		for k, child := range v {
			lower := strings.ToLower(k)
			str, isString := child.(string)
			switch {
			case isString && thinkingTextKeys[lower]:
				appendText(thinking, str)
			case isNumberKey(lower):
				assign(ev, lower, child)
			case isString && textKeys[lower]:
				if thinkingHere {
					appendText(thinking, str)
				} else {
					appendText(text, str)
				}
			case isString:
				// Some other string field: not content, not a counter.
			default:
				walk(child, ev, text, thinking, depth+1, thinkingHere)
			}
		}
	case []any:
		for _, child := range v {
			walk(child, ev, text, thinking, depth+1, inThinking)
		}
	}
}

// isThinkingBlock reports whether a record is a reasoning block, so the plain
// text inside it is read as reasoning rather than as visible output.
func isThinkingBlock(v map[string]any) bool {
	t, _ := v["type"].(string)
	t = strings.ToLower(t)
	return strings.Contains(t, "thinking") || strings.Contains(t, "reasoning")
}

func isNumberKey(lower string) bool {
	return outputKeys[lower] || thinkingKeys[lower] || totalKeys[lower]
}

// assign records a counter, keeping the largest value seen for that field on
// this line: agents sometimes repeat a total in a nested summary.
func assign(ev *Event, lower string, val any) {
	n, ok := asInt(val)
	if !ok || n <= 0 {
		return
	}
	switch {
	case thinkingKeys[lower]:
		ev.Usage.Thinking = max(ev.Usage.Thinking, n)
	case outputKeys[lower]:
		ev.Usage.Output = max(ev.Usage.Output, n)
	case totalKeys[lower]:
		ev.Usage.Total = max(ev.Usage.Total, n)
	}
}

func appendText(into *strings.Builder, s string) {
	if s == "" {
		return
	}
	if into.Len() > 0 {
		into.WriteString("\n")
	}
	into.WriteString(s)
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		// The conversion below is only defined within the range of int, and
		// out-of-range results differ by platform (amd64 gives the minimum,
		// arm64 saturates to the maximum). A counter outside it is not a
		// measurement: report nothing rather than a platform-dependent lie.
		if !(n >= 1) || n >= 1<<63 {
			return 0, false
		}
		return int(n), true
	case json.Number:
		// Not reachable through Parse, which decodes without UseNumber, but
		// the guard belongs next to the conversion rather than with whichever
		// caller happens not to trigger it. int is 32 bits on some builds, so
		// a bare int(i) would silently truncate a counter that does not fit
		// and report 5 for 2^32+5 -- the platform-dependent lie the float64
		// case above exists to avoid.
		i, err := n.Int64()
		if err != nil || i < 1 || i > math.MaxInt {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}
