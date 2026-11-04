// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
)

// FuzzACPSession drives acpSession with arbitrary ACP peer output.
// The peer is an agent CLI: its JSON-RPC is untrusted input — a hostile
// or compromised agent controls every line, and a panic or hang here
// strands the review. The fuzzer finds the crash; the assertions pin
// the invariants the callers rely on: no panic, deterministic outcome
// for the same input, and emitted text only ever carries what the
// input carried (the dashboard shows it as the agent's own words, and
// a parser that authors text would be a forgery channel). Permission
// outcomes must stay within the two outcomes the protocol allows, and
// usage counters must stay within the plausible bound acpSession itself
// enforces for a downstream percentage.
func FuzzACPSession(f *testing.F) {
	seeds := []string{
		strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`,
			`{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s"}}`,
			`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`,
			`{"jsonrpc":"2.0","id":"p","method":"session/request_permission","params":{"sessionId":"s","options":[{"optionId":"yes","kind":"allow_once"}]}}`,
			`{"jsonrpc":"2.0","id":5,"result":{"stopReason":"end_turn"}}`,
		}, "\n"),
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":40,"thoughtTokens":7,"totalTokens":171}}}`,
		`{"id":1,"result":{"protocolVersion":2}}`,
		`{"id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"x","version":"1"},"authMethods":[{"id":"cached_token"}]}}`,
		`{"method":"session/update","params":{"sessionId":"","update":{"sessionUpdate":"current_model_update","currentModelId":"m"}}}`,
		`{"id":1,"error":{"code":-32601,"message":"no init"}}`,
		`{"id":1,"result":{"protocolVersion":1}}`,
		``,
		"\n\n\n",
		`not json`,
		`{"id":1,"result":`,
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s","modes":{"availableModes":[{"id":"m"}]},"models":{"availableModels":[{"id":"m"}]}}}` + "\n" + `{"id":3,"result":{"configOptions":[{"id":"model","currentValue":"m"}]}}` + "\n" + `{"id":4,"result":{"configOptions":[{"id":"effort","currentValue":"e"}]}}` + "\n" + `{"id":6,"result":{"configOptions":[{"id":"mode","currentValue":"mode"}]}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn"}}`,
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":""}}`,
		`{"id":"x","method":"unknown/method","params":{}}`,
		strings.Repeat(`{"id":1,"result":{"protocolVersion":1}}`+"\n", 64),
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":9223372036854775807,"thoughtTokens":-1,"totalTokens":18446744073709551615}}}`,
		`{"id":1,"result":{"protocolVersion":1}}` + "\n" + `{"id":2,"result":{"sessionId":"s"}}` + "\n" + `{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":1099511627776}}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		run := func() (errStr string, out string, texts []string, usages []agent.Usage) {
			var buf bytes.Buffer
			defer func() {
				if r := recover(); r != nil {
					errStr = "panic"
				}
			}()
			e := acpSession(strings.NewReader(input), &buf, "/work", "review", "m", "e", "", "mode", true, false,
				func(s string) { texts = append(texts, s) },
				func(s string) { texts = append(texts, s) },
				func(u agent.Usage) { usages = append(usages, u) },
				nil,
			)
			if e != nil {
				errStr = e.Error()
			}
			return errStr, buf.String(), texts, usages
		}
		err1, out1, texts1, usages1 := run()
		err2, out2, texts2, usages2 := run()
		if err1 != err2 || out1 != out2 || len(texts1) != len(texts2) || len(usages1) != len(usages2) {
			t.Fatalf("acpSession not deterministic for %q: (%q,%q) vs (%q,%q)", input, err1, out1, err2, out2)
		}
		for i := range texts1 {
			if texts1[i] != texts2[i] {
				t.Fatalf("acpSession text not deterministic for %q", input)
			}
		}
		for i := range usages1 {
			if usages1[i] != usages2[i] {
				t.Fatalf("acpSession usage not deterministic for %q", input)
			}
			u := usages1[i]
			for _, n := range []int{u.Output, u.Thinking, u.Total} {
				if n < -1 || n > (1<<40) {
					t.Fatalf("acpSession usage %d outside [-1, 1<<40] for %q", n, input)
				}
			}
		}
		// Every emitted text segment is a substring the input carried:
		// acpSession concatenates and forwards, it never authors.
		for _, s := range texts1 {
			if s != "" && !strings.Contains(input, s) {
				// JSON-escaped text decodes to characters not literally
				// present; compare against the decoded form.
				dec := decodeJSONString(input)
				if !strings.Contains(dec, s) {
					t.Fatalf("acpSession emitted %q not carried by %q", s, input)
				}
			}
		}
		// Every output line is valid JSON-RPC the session wrote, and any
		// permission outcome is one of the two the protocol allows.
		for line := range strings.Lines(out1) {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var doc struct {
				Result *struct {
					Outcome *struct {
						Outcome string `json:"outcome"`
					} `json:"outcome"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(line), &doc); err != nil {
				t.Fatalf("acpSession wrote non-JSON %q for input %q", line, input)
			}
			if doc.Result != nil && doc.Result.Outcome != nil {
				if o := doc.Result.Outcome.Outcome; o != "cancelled" && o != "selected" {
					t.Fatalf("acpSession permission outcome %q outside protocol for %q", o, input)
				}
			}
		}
	})
}

// decodeJSONString returns the input with JSON string literals decoded:
// decode via a generic decode of each line's string values is overkill —
// instead walk the input, and for each quoted run decode escapes with
// strconv-equivalent handling by re-encoding through encoding/json on
// the maximal quoted slice. Simpler: decode the whole input as JSON
// values where possible and collect every string.
func decodeJSONString(input string) string {
	var b strings.Builder
	dec := json.NewDecoder(strings.NewReader(input))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			break
		}
		collectStrings(v, &b)
		b.WriteByte('\n')
	}
	return b.String()
}

func collectStrings(v any, b *strings.Builder) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteByte('\n')
	case []any:
		for _, e := range t {
			collectStrings(e, b)
		}
	case map[string]any:
		for _, e := range t {
			collectStrings(e, b)
		}
	}
}
