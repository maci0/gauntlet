// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
