// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/journal"
)

// TestRunIndexerKillsProcessGroup pins the rule runProc states for agents:
// the deadline must take down the whole process tree, not just the direct
// child. The fixture backgrounds a marked sleeper, so without the group kill
// the grandchild outlives the killed parent and shows up in /proc.
func TestRunIndexerKillsProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the leak check walks /proc")
	}
	dir := t.TempDir()
	marker := "gauntlet-group-kill-test-" + time.Now().Format("150405.000000000")

	sleeper := filepath.Join(dir, "sleeper")
	writeScript(t, sleeper, "#!/bin/sh\nsleep 30\n")
	tree := filepath.Join(dir, "tree")
	writeScript(t, tree, "#!/bin/sh\n\""+sleeper+"\" \""+marker+"\" &\nwait\n")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if code := runIndexer(ctx, tree, nil, dir); code == 0 {
		t.Fatal("the indexer reported success after its deadline killed it")
	}

	// A zombie keeps its /proc entry but an empty cmdline, so only a live
	// orphan matches.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if !liveCmdlineContains(t, marker) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a grandchild of the killed indexer is still alive")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// liveCmdlineContains reports whether any live process carries needle in its
// command line.
func liveCmdlineContains(t *testing.T, needle string) bool {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skipf("cannot read /proc: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || !isNumeric(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue // exited between the listing and the read
		}
		if strings.Contains(string(data), needle) {
			return true
		}
	}
	return false
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
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
