// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLandlockABIMasks(t *testing.T) {
	for abi, want := range map[uintptr]uint64{1: (1 << 13) - 1, 2: (1 << 14) - 1, 3: (1 << 15) - 1, 4: (1 << 15) - 1, 5: (1 << 16) - 1, 100: (1 << 16) - 1} {
		if got := landlockAccess(abi); got != want {
			t.Fatalf("ABI %d: %#x, want %#x", abi, got, want)
		}
	}
}

func TestLandlockSetupFailureDoesNotExecAgent(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	cmd := exec.Command("/bin/sh", "-c", `touch "$1"`, "sh", marker)
	cmd.Dir = dir
	child, err := confinedCommand(cmd, []string{filepath.Join(dir, "missing")}, cmd.Path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := child.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "agent not started") {
		t.Fatalf("setup failure: %v, %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("agent ran: %v", err)
	}
}
