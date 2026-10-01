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

// The probe doctor prints and the launch every agent goes through must be one
// answer: a host the probe calls confined, and a launch that then refuses to
// build its sandbox roots, leaves an operator told the run is safe when the
// first agent never starts.
func TestSandboxSupportAgreesWithTheLaunch(t *testing.T) {
	detail, ok, err := SandboxSupport()
	if !ok {
		// Landlock is a kernel feature and this host may not have it. What
		// must hold is that the reason names the requirement, since it is the
		// only place an operator meets one before a run does.
		if err == nil {
			t.Fatal("SandboxSupport reported no mechanism and no reason")
		}
		t.Logf("this host has no Landlock: %v", err)
		return
	}
	if detail == "" {
		t.Fatal("SandboxSupport reported the mechanism with no detail")
	}
	cmd := exec.Command("/bin/sh", "-c", "true")
	child, err := confinedCommand(cmd, []string{os.TempDir()}, cmd.Path)
	if err != nil {
		t.Fatalf("SandboxSupport reported %q but a launch failed: %v", detail, err)
	}
	if len(child.Args) < 2 || child.Args[1] != sandboxExecArg {
		t.Fatalf("launch did not go through the sandbox re-exec: %v", child.Args)
	}
}
