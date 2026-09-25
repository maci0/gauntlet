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
	// and the caller treats both as unknown (see runner's pick helper).
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
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var ev Event
	var text, thinking strings.Builder
	if err := extractValue(dec, &ev, &text, &thinking, 0, false); err != nil {
		return Event{}, false
	}
	// Trailing bytes after the value mean the line is not one JSON document,
	// which is the answer json.Unmarshal gave for the same input.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Event{}, false
	}
	ev.Text = strings.TrimRight(text.String(), "\n")
	ev.Thinking = strings.TrimRight(thinking.String(), "\n")
	return ev, true
}

// errNotJSON marks input the decoder reached but cannot be a JSON value. It
// never escapes extractValue, which reports the same "not JSON" the caller
// already handles by falling back to text.
var errNotJSON = errors.New("not a JSON value")

// maxDepth bounds the walk. Agent envelopes nest a few levels; anything deeper
// is a tool result payload, whose contents are not this package's business.
const maxDepth = 8

// extractValue reads one JSON value and picks up text and usage as it goes.
// Keys arrive in the order the agent wrote them, so sibling text fields
// concatenate the way they were emitted rather than in a randomized map order.
func extractValue(dec *json.Decoder, ev *Event, text, thinking *strings.Builder, depth int, inThinking bool) error {
	if depth > maxDepth {
		return skipValue(dec)
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return extractFrom(dec, tok, ev, text, thinking, depth, inThinking)
}

func extractFrom(dec *json.Decoder, tok json.Token, ev *Event, text, thinking *strings.Builder, depth int, inThinking bool) error {
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return nil // string, float64, bool, or nil: not content on its own
	}
	if depth > maxDepth {
		return skipRest(dec, delim)
	}
	switch delim {
	case '{':
		return extractObject(dec, ev, text, thinking, depth, inThinking)
	case '[':
		for dec.More() {
			if err := extractValue(dec, ev, text, thinking, depth+1, inThinking); err != nil {
				return err
			}
		}
		_, err := dec.Token() // the closing bracket
		return err
	}
	return errNotJSON // a closing delimiter where a value belongs
}

// objField is one object member held until the object's type is known, so a
// reasoning block whose "type" arrives after its "text" still classifies the
// text as thinking. Nested containers are extracted as they close, then
// promoted if this object turns out to be a reasoning block.
type objField struct {
	key      string
	str      string
	hasStr   bool
	nested   bool
	text     string
	thinking string
}

func extractObject(dec *json.Decoder, ev *Event, text, thinking *strings.Builder, depth int, inThinking bool) error {
	var fields []objField
	var typeStr, role string
	var local Event
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errNotJSON
		}
		lower := strings.ToLower(key)
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, isDelim := tok.(json.Delim); isDelim {
			var nested Event
			var nText, nThink strings.Builder
			if err := extractFrom(dec, delim, &nested, &nText, &nThink, depth+1, inThinking); err != nil {
				return err
			}
			if nText.Len() > 0 || nThink.Len() > 0 {
				fields = append(fields, objField{
					key: lower, nested: true,
					text: nText.String(), thinking: nThink.String(),
				})
			}
			local.Usage.Output = max(local.Usage.Output, nested.Usage.Output)
			local.Usage.Thinking = max(local.Usage.Thinking, nested.Usage.Thinking)
			local.Usage.Total = max(local.Usage.Total, nested.Usage.Total)
			continue
		}
		switch v := tok.(type) {
		case string:
			if lower == "type" {
				typeStr = strings.ToLower(v)
			}
			if lower == "role" {
				role = strings.ToLower(v)
			}
			if textKeys[lower] || thinkingTextKeys[lower] {
				fields = append(fields, objField{key: lower, str: v, hasStr: true})
			}
		case float64, json.Number:
			if n, ok := asInt(v); ok {
				assign(&local, lower, n)
			}
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return err
	}

	if role == "tool" || role == "user" || typeStr == "user" ||
		typeStr == "tool_use" || typeStr == "tool_result" {
		return nil
	}
	ev.Usage.Output = max(ev.Usage.Output, local.Usage.Output)
	ev.Usage.Thinking = max(ev.Usage.Thinking, local.Usage.Thinking)
	ev.Usage.Total = max(ev.Usage.Total, local.Usage.Total)

	thinkingHere := inThinking || typeIsThinking(typeStr)
	for _, f := range fields {
		switch {
		case f.hasStr && thinkingTextKeys[f.key]:
			appendText(thinking, f.str)
		case f.hasStr && (outputKeys[f.key] || thinkingKeys[f.key] || totalKeys[f.key]):
			// a numeric counter that arrived as a string is not a count
		case f.hasStr && textKeys[f.key]:
			if thinkingHere {
				appendText(thinking, f.str)
			} else {
				appendText(text, f.str)
			}
		case f.nested:
			if thinkingHere && !inThinking {
				appendText(thinking, f.text)
			} else {
				appendText(text, f.text)
			}
			appendText(thinking, f.thinking)
		}
	}
	return nil
}

func typeIsThinking(t string) bool {
	t = strings.ToLower(t)
	return strings.Contains(t, "thinking") || strings.Contains(t, "reasoning")
}

// skipValue consumes one value without extracting, counting delimiters
// rather than recursing: what it is called on is arbitrarily deep by
// definition, so it must not add a stack frame per level. The decoder's
// input is the line, so Token eventually EOFs; a closer with no matching
// opener is not a value, the same answer extractValue gives at shallower
// depths.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok {
		return skipRest(dec, d)
	}
	return nil
}

func skipRest(dec *json.Decoder, first json.Delim) error {
	if first != '{' && first != '[' {
		return errNotJSON
	}
	open := 1
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			continue
		}
		if d == '{' || d == '[' {
			open++
		} else {
			open--
		}
		if open == 0 {
			return nil
		}
	}
}

// assign records a counter, keeping the largest value seen for that field on
// this line: agents sometimes repeat a total in a nested summary.
func assign(ev *Event, lower string, n int) {
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

// maxPlausible is the largest token counter accepted, the same trillion-token
// cap agent.ParseUsage uses. A 2^62 counter fits in int and then overflows
// the run total when two of them are added, or wraps 100*thinking/tokens to
// a negative percentage. No review generates that many tokens.
const maxPlausible = 1 << 40

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		// The conversion below is only defined within the range of int, and
		// out-of-range results differ by platform (amd64 gives the minimum,
		// arm64 saturates to the maximum). A counter outside it is not a
		// measurement: report nothing rather than a platform-dependent lie.
		//
		// JSON numbers arrive here as float64, so a fractional count would
		// otherwise truncate (1.9 -> 1). MaxInt sits beside maxPlausible
		// because a 32-bit int fills first. !(n >= 1) is what rejects NaN:
		// n < 1 does not.
		if !(n >= 1) || n != math.Trunc(n) || n > maxPlausible || n > float64(math.MaxInt) {
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
		if err != nil || i < 1 || i > math.MaxInt || i > maxPlausible {
			return 0, false
		}
		return int(i), true
	}
	return 0, false
}
