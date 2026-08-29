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
	"errors"
	"io"
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
// slices it, and the decoder reads that slice in place. The strings it hands
// back are the only allocations, as they were before.
func Parse(line []byte) (Event, bool) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Event{}, false
	}
	doc, ok := decode(trimmed)
	if !ok {
		return Event{}, false
	}
	var ev Event
	var text, thinking strings.Builder
	walk(doc, &ev, &text, &thinking, 0, false)
	ev.Text = strings.TrimRight(text.String(), "\n")
	ev.Thinking = strings.TrimRight(thinking.String(), "\n")
	return ev, true
}

// member is one key/value pair of a JSON object, kept where it was written.
type member struct {
	key string
	val any
}

// object is a decoded JSON object that remembers the order of its keys.
//
// encoding/json decodes into map[string]any, and Go randomizes map iteration,
// so walking that map concatenated an envelope's text fields in a different
// order from one line to the next: two sibling blocks holding "FIRST" and
// "SECOND" came out swapped about one line in ten. That is a feed that
// reorders an agent's sentences and a transcript that does not match what the
// agent said. The order belongs to the agent, so it is preserved rather than
// replaced with an order of ours.
type object struct{ members []member }

// get returns the value written under key, last one wins, matching what
// encoding/json does with a repeated key. Envelope records carry a handful of
// fields and only one key is ever looked up, so a scan costs less than the map
// it would replace.
func (o *object) get(key string) any {
	var out any
	for _, m := range o.members {
		if m.key == key {
			out = m.val
		}
	}
	return out
}

// errNotJSON marks input the decoder reached but cannot be a JSON value. It
// never escapes decode, which reports the same "not JSON" the caller already
// handles by falling back to text.
var errNotJSON = errors.New("not a JSON value")

// decodeDepth bounds the decoder's recursion. encoding/json's scanner has a
// nesting limit of its own, but it is thousands deep; a line arriving from an
// agent is untrusted, and this is per output line. Anything past the bound is
// consumed without being built, iteratively, so a nested tool payload still
// leaves the line valid JSON -- it just contributes nothing, which is already
// true of everything below walk's own maxDepth.
const decodeDepth = 64

// decode reads one JSON value, building objects that keep their key order.
func decode(data []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, false
	}
	// Trailing bytes after the value mean the line is not one JSON document,
	// which is the answer json.Unmarshal gave for the same input.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return v, true
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > decodeDepth {
		return nil, skipValue(dec)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil // string, float64, bool, or nil
	}
	switch delim {
	case '{':
		obj := &object{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyTok.(string)
			if !ok {
				return nil, errNotJSON
			}
			val, err := decodeValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			// A repeated key keeps both members rather than the last: the
			// text of each is something the agent wrote, and dropping one
			// would lose output. get is what resolves a lookup to one value.
			obj.members = append(obj.members, member{key: key, val: val})
		}
		_, err := dec.Token() // the closing brace
		return obj, err
	case '[':
		var arr []any
		for dec.More() {
			val, err := decodeValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		_, err := dec.Token() // the closing bracket
		return arr, err
	}
	return nil, errNotJSON // a closing delimiter where a value belongs
}

// skipValue consumes one value without building it, counting delimiters
// rather than recursing: what it is called on is arbitrarily deep by
// definition, so it must not add a stack frame per level.
func skipValue(dec *json.Decoder) error {
	open := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				open++
			} else {
				open--
			}
		}
		if open == 0 {
			return nil // a scalar, or the container just closed
		}
	}
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
	case *object:
		thinkingHere := inThinking || isThinkingBlock(v)
		for _, m := range v.members {
			lower := strings.ToLower(m.key)
			child := m.val
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
func isThinkingBlock(o *object) bool {
	t, _ := o.get("type").(string)
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
