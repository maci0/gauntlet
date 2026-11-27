// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package streamjson

import (
	"encoding/json"
	"testing"
)

// streamRecord builds one record of a delta stream the way the installed CLI
// prints it (captured from 0.18.0, with the envelope's identifiers shortened).
func streamRecord(eventType string, payload map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"eventId": "e1", "payload": payload, "seq": 1, "sessionId": "s1",
		"timestamp": 1, "traceId": "t1", "turnId": "u1", "type": eventType,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func streamDelta(kind, delta string) string {
	return streamRecord("model.streaming", map[string]any{
		"assistantMessageId": "m1", "delta": delta, "done": false, "kind": kind,
	})
}

// A delta stream says what each delta is in a kind field. Reasoning belongs to
// the reasoning lane. Tool traffic is not the model's own words even where it
// sits under an ordinary text key: a call's input and a tool's output both
// carry file contents under content.
func TestStreamKindSeparatesReasoningTextAndToolTraffic(t *testing.T) {
	cases := []struct {
		name, line string
		want       Event
	}{
		{"text", streamDelta("text_delta", "RESULT: mock review fini"),
			Event{Text: "RESULT: mock review fini", Fragment: true}},
		{"reasoning", streamDelta("reasoning_delta", "check the file first"),
			Event{Thinking: "check the file first", FragmentThinking: true}},
		{"tool input", streamDelta("tool_input_delta", `{"file_path":"a.go","content":"file body"}`), Event{}},
		{"tool call", streamRecord("model.streaming", map[string]any{
			"delta": "", "kind": "tool_call", "toolCallId": "c1", "toolName": "Write",
			"input": map[string]any{"file_path": "a.go", "content": "file body"},
		}), Event{}},
		{"block marker", streamDelta("text_start", ""), Event{}},
	}
	for _, c := range cases {
		ev, ok := Parse([]byte(c.line))
		if !ok {
			t.Errorf("%s: not an event", c.name)
			continue
		}
		if ev != c.want {
			t.Errorf("%s: Parse = %+v, want %+v", c.name, ev, c.want)
		}
	}
}

// The tool update record re-sends the call's input and later the tool's
// output. Neither is assistant text.
func TestToolUpdateRecordsAreNotAssistantText(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"scheduled": {"toolCallId": "c1", "toolName": "Write", "kind": "scheduled",
			"input": map[string]any{"file_path": "a.go", "content": "PATH: trap.go: file body"}},
		"result": {"toolCallId": "c1", "kind": "result", "duration": 34,
			"result": map[string]any{"success": true, "content": "1\tPATH: trap.go: tool output"}},
	} {
		ev, ok := Parse([]byte(streamRecord("tool.updated", payload)))
		if !ok {
			t.Fatalf("%s: not an event", name)
		}
		if ev.Text != "" || ev.Thinking != "" {
			t.Errorf("%s: tool traffic read as model text: %+v", name, ev)
		}
	}
}

// The step summary near the end of a turn is a status record: it repeats the
// text the deltas already delivered, under content, beside the step's
// counters. Read as text, every answer would be shown twice. Its counters are
// the ones the turn record carries, so, like every status record, it
// contributes neither.
func TestStepSummaryRepeatsNeitherTextNorCounters(t *testing.T) {
	line := streamRecord("session.updated", map[string]any{
		"cacheHit":      map[string]any{"totalInputTokens": 3300, "hitRate": 0},
		"content":       "RESULT: mock review finished\nSUBJECT: mock: exercise the stream",
		"contextWindow": 262144, "querySource": "main_turn", "stopReason": "other",
		"toolCallCount": 0,
		"usage":         map[string]any{"inputTokens": 2100, "outputTokens": 96, "totalTokens": 2196, "reasoningTokens": 30},
	})
	ev, ok := Parse([]byte(line))
	if !ok || ev != (Event{}) {
		t.Fatalf("Parse = %+v, %v; want an empty event, true", ev, ok)
	}
}

// The result record of the captured run: the whole answer again, which the
// stream already delivered, and the turn's counters in camelCase.
func TestStreamResultRecordCarriesCountersAndNotTheAnswer(t *testing.T) {
	line := `{"type":"result","sessionId":"s1","traceId":"t1","turnId":"u1",` +
		`"response":"RESULT: mock review finished\nPATH: trap.go: already streamed",` +
		`"usage":{"source":"provider","modelRequestCount":2,"inputTokens":3300,` +
		`"outputTokens":136,"totalTokens":3436,"cacheReadTokens":0,"cacheWriteTokens":0,` +
		`"reasoningTokens":30,"webFetchRequests":0,"webSearchRequests":0},"eventCount":51,` +
		`"projection":{"status":"idle","turnCount":1,"totalTokenCount":3436,` +
		`"contextUsed":3436,"contextWindow":200000}}`
	ev, ok := Parse([]byte(line))
	want := Event{Usage: Usage{Output: 136, Thinking: 30, Total: 3436}}
	if !ok || ev != want {
		t.Fatalf("Parse = %+v, %v; want %+v, true", ev, ok, want)
	}
}

// Text arrives a few tokens per record, so one line of the answer spans
// several records, and the report parsers anchor on line starts. The decoder
// hands back a line only once it is whole. These deltas are the captured run's.
func TestDecoderJoinsTextDeltasIntoLines(t *testing.T) {
	var d Decoder
	feed := func(line string) Event {
		t.Helper()
		ev, ok := d.Parse([]byte(line))
		if !ok {
			t.Fatalf("not an event: %s", line)
		}
		return ev
	}
	if ev := feed(streamDelta("text_start", "")); ev.Text != "" {
		t.Fatalf("a block marker produced text %q", ev.Text)
	}
	for _, c := range []struct{ delta, want string }{
		{"RESULT: mock review fini", ""},
		{"shed\nSUBJECT: mock: exer", "RESULT: mock review finished"},
		{"cise the stream\nPATH: /t", "SUBJECT: mock: exercise the stream"},
		{"mp/zcode-probe/run/hello", ""},
		{".txt: created by the moc", ""},
		{"k", ""},
	} {
		if ev := feed(streamDelta("text_delta", c.delta)); ev.Text != c.want {
			t.Fatalf("after delta %q: line %q, want %q", c.delta, ev.Text, c.want)
		}
	}
	// The block's end completes the last line.
	const last = "PATH: /tmp/zcode-probe/run/hello.txt: created by the mock"
	if ev := feed(streamDelta("text_end", "")); ev.Text != last {
		t.Fatalf("at text_end: line %q, want %q", ev.Text, last)
	}
	completed := streamRecord("turn.completed", map[string]any{
		"response": "RESULT: mock review finished", "tokenCount": 2196,
		"usage": map[string]any{"outputTokens": 96, "reasoningTokens": 30, "totalTokens": 2196},
	})
	want := Event{Usage: Usage{Output: 96, Thinking: 30, Total: 2196}}
	if ev := feed(completed); ev != want {
		t.Fatalf("turn.completed = %+v, want %+v", ev, want)
	}
}

// Reasoning arrives a few tokens per record too, and a short prompt has
// produced 727 of them. The decoder joins it into lines the way it joins
// text, and holds the two apart because they alternate within a turn.
func TestDecoderJoinsReasoningDeltasIntoLines(t *testing.T) {
	var d Decoder
	feed := func(line string) Event {
		t.Helper()
		ev, ok := d.Parse([]byte(line))
		if !ok {
			t.Fatalf("not an event: %s", line)
		}
		return ev
	}
	for _, c := range []struct{ delta, want string }{
		{"The", ""},
		{" user", ""},
		{" asks", ""},
		{" me.\nNow", "The user asks me."},
		{" I", ""},
		{" check.", ""},
	} {
		if ev := feed(streamDelta("reasoning_delta", c.delta)); ev.Thinking != c.want || ev.Text != "" {
			t.Fatalf("after delta %q: thinking %q, want %q", c.delta, ev.Thinking, c.want)
		}
	}
	// Reasoning ends and the answer begins: what was held is a whole line
	// now. The answer's first text is a fragment of its own, so it is held
	// until the stream ends rather than joined onto the reasoning.
	first := feed(streamDelta("text_delta", "The file compiles."))
	if want := (Event{Fragment: true, Thinking: "Now I check."}); first != want {
		t.Fatalf("first text record = %+v, want %+v", first, want)
	}
	if ev := d.Flush(); ev != (Event{Text: "The file compiles."}) {
		t.Fatalf("Flush = %+v, want the answer's unfinished line", ev)
	}
}

// A stream can end on a delta with no line break after it. What is left is
// still a line, and it is handed over once.
func TestDecoderFlushesAnUnterminatedLastLine(t *testing.T) {
	var d Decoder
	if ev, ok := d.Parse([]byte(streamDelta("text_delta", "RESULT: no line break"))); !ok || ev.Text != "" {
		t.Fatalf("an unterminated delta produced %+v, %v", ev, ok)
	}
	if ev := d.Flush(); ev.Text != "RESULT: no line break" {
		t.Fatalf("Flush = %+v, want the pending line", ev)
	}
	if ev := d.Flush(); ev.Text != "" {
		t.Fatalf("second Flush = %+v, want nothing", ev)
	}
}

// A record that is not a delta is a whole event, and the decoder must hand it
// over unchanged after any line it was holding.
func TestDecoderPassesWholeEventsThrough(t *testing.T) {
	var d Decoder
	if _, ok := d.Parse([]byte(`not json`)); ok {
		t.Fatal("a non-JSON line parsed as an event")
	}
	d.Parse([]byte(streamDelta("text_delta", "SUBJECT: held until the next")))
	whole := `{"type":"assistant","message":{"content":[{"type":"text","text":"RESULT: done"}]}}`
	ev, ok := d.Parse([]byte(whole))
	if want := (Event{Text: "SUBJECT: held until the next\nRESULT: done"}); !ok || ev != want {
		t.Fatalf("Parse = %+v, %v; want %+v, true", ev, ok, want)
	}
	plain, ok := Parse([]byte(whole))
	if !ok || plain != (Event{Text: "RESULT: done"}) {
		t.Fatalf("Parse = %+v, %v; want the whole event, true", plain, ok)
	}
}
