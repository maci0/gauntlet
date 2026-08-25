// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/journal"
)

// TestRunIndexerKillsProcessGroup pins the rule runProc states for agents:
// the deadline must take down the whole process tree, not just the direct
// child. The fixture backgrounds a sleeper and records its pid, so without
// the group kill the grandchild outlives the killed parent and answers a
// liveness probe.
func TestRunIndexerKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()

	sleeper := filepath.Join(dir, "sleeper")
	writeScript(t, sleeper, "#!/bin/sh\nsleep 30\n")
	pidFile := filepath.Join(dir, "sleeper.pid")
	tree := filepath.Join(dir, "tree")
	writeScript(t, tree, "#!/bin/sh\n\""+sleeper+"\" &\necho $! > \""+pidFile+"\"\nwait\n")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if code := runIndexer(ctx, tree, nil, dir); code == 0 {
		t.Fatal("the indexer reported success after its deadline killed it")
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		t.Fatalf("fixture wrote no usable sleeper pid: %q", data)
	}

	// kill(pid, 0) reports whether anything still answers at that pid, on
	// every POSIX target this project ships for, so no /proc walk is needed.
	// A survivor runs sleep 30 and stays alive far longer than this wait; a
	// killed one disappears within it. Its only possible parent (the killed
	// script) died in the same group kill, so it cannot linger unreaped as a
	// zombie and keep the probe green.
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatal("a grandchild of the killed indexer is still alive")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// processAlive probes one pid with a zero signal: nil or EPERM means something
// is there, ESRCH means it is gone.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A replayed journal reaches a terminal through `gauntlet show`. Event text
// records fragments of a possibly hostile tree verbatim (git merge errors,
// file names), and json.Marshal escapes control bytes but passes bidi
// overrides through as raw UTF-8, so the replay must strip them the way every
// other display surface already does.
func TestShowSanitizesReplayedEvents(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	runID := "20260826T120000Z-dead"
	dir := filepath.Join(journal.Home(), "runs", "2026-08-26")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Valid JSON escapes: decoding yields a real ESC, BEL, and bidi pair.
	line := `{"ev":"merge","ts":"2026-08-26T12:00:00Z","status":"conflict",` +
		`"text":"CONFLICT in evil\u001b]0;pwned\u0007.md \u202ebad\u202c.md"}`
	if err := os.WriteFile(filepath.Join(dir, runID+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code := cmdShow(&buf, runID); code != exitOK {
		t.Fatalf("replaying a recorded run should exit %d, got %d", exitOK, code)
	}
	out := buf.String()
	for _, bad := range []string{"\x1b", "\x07", "\u202e", "\u202c"} {
		if strings.Contains(out, bad) {
			t.Errorf("replay emitted %q to the terminal: %q", bad, out)
		}
	}
	if !strings.Contains(out, "CONFLICT") || !strings.Contains(out, "evil") {
		t.Errorf("replay lost the readable content: %q", out)
	}
}
