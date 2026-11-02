// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build sqlite && !notoktop

package runner

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/toktop/agentusage"
)

func TestOpenCode2TranscriptUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "opencode.db")
	t.Setenv("OPENCODE_DB", path)
	if !agentusage.EnableOpenCodeDB(true) {
		t.Fatal("OpenCode database reader unavailable")
	}
	t.Cleanup(func() { agentusage.EnableOpenCodeDB(false) })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT, fork_session_id TEXT, time_created INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, data TEXT)`,
		`CREATE INDEX session_message_session_time_created_id_idx ON session_message (session_id, time_created, id)`,
		`CREATE TABLE event (id TEXT PRIMARY KEY, aggregate_id TEXT, seq INTEGER, type TEXT, created INTEGER, data TEXT)`,
		`CREATE INDEX event_aggregate_type_seq_idx ON event (aggregate_id, type, seq)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Now().Add(-time.Second)
	reader := newTranscriptReader("opencode", dir, since)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cap := newReviewCap(35, cancel)
	r := &Runner{cfg: Config{Dir: dir}, bus: NewBus()}
	budgetWatch := r.watchUsage(ctx, agent.Spec{Tool: "opencode"}, dir, "review", 1, since, cap.reading)
	t.Cleanup(budgetWatch.halt)
	stamp := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO session_v2 VALUES
		('root', NULL, ?, NULL, ?), ('child', 'root', ?, NULL, ?), ('other', NULL, ?, NULL, ?)`,
		dir, stamp, dir, stamp, t.TempDir(), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session_message VALUES
		('m1', 'root', 'assistant', 1, ?, '{"tokens":{"output":20,"reasoning":3}}'),
		('m2', 'child', 'assistant', 1, ?, '{"tokens":{"output":5,"reasoning":2}}'),
		('m3', 'other', 'assistant', 1, ?, '{"tokens":{"output":999}}')`, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event VALUES
		('e1', 'root', 1, 'session.usage.recorded.1', ?, '{"source":"title","tokens":{"output":1,"reasoning":1}}'),
		('e2', 'root', 2, 'session.step.failed.1', ?, '{"assistantMessageID":"m1","tokens":{"output":4,"reasoning":1}}'),
		('e3', 'root', 3, 'session.step.ended.1', ?, '{"assistantMessageID":"m1","tokens":{"output":20,"reasoning":3}}')`, stamp, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("OpenCode 2 database usage did not reach the review budget")
	}
	if tokens, tripped := cap.spent(); !tripped || tokens != 37 {
		t.Fatalf("budget = %d, tripped = %v; want 37 and true", tokens, tripped)
	}
	watch := &usageWatch{reader: reader, stop: func() {}, publish: func(int, int) {}}
	input := `{"id":1,"result":{"protocolVersion":1}}` + "\n" +
		`{"id":2,"result":{"sessionId":"s"}}` + "\n" +
		`{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":25,"thoughtTokens":5}}}`
	var requests bytes.Buffer
	if err := acpSession(strings.NewReader(input), &requests, dir, "review", "", "", "", "", true, false, nil, nil, func(u agent.Usage) {
		watch.report(u.Reported(), u.Thinking)
	}, nil); err != nil {
		t.Fatal(err)
	}
	if output, thinking := watch.settle(); output != 37 || thinking != 7 {
		t.Fatalf("OpenCode 2 output = %d, thinking = %d; want 37, 7", output, thinking)
	}
	bin := fakeAgent(t, t.TempDir(), "opencode", `
read -r init
printf '%s\n' '{"id":1,"result":{"protocolVersion":1}}'
read -r session
printf '%s\n' '{"id":2,"result":{"sessionId":"s"}}'
read -r prompt
printf '%s\n' '{"id":5,"result":{"stopReason":"end_turn","usage":{"outputTokens":60,"thoughtTokens":8}}}'
`)
	watch = &usageWatch{reader: reader, stop: func() {}, publish: func(int, int) {}}
	prompt := "review"
	result := runProc(context.Background(), procOpts{
		Tool: "opencode", Argv: []string{bin}, ACPPrompt: &prompt, Dir: dir, NoSandbox: true, Timeout: 5 * time.Second,
		Usage: func(u agent.Usage) { watch.report(u.Reported(), max(u.Thinking, 0)) },
	})
	if result.Err != nil || result.StreamErr != nil || result.ExitCode != 0 {
		t.Fatalf("ACP process = %+v", result)
	}
	if output, thinking := watch.settle(); output != 68 || thinking != 8 {
		t.Fatalf("ACP-dominant output = %d, thinking = %d; want 68, 8", output, thinking)
	}
}
