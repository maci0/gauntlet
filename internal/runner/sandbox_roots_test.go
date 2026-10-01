// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSandboxRootsGrantThePlatformTempDir(t *testing.T) {
	base := t.TempDir()
	// A temp directory of its own, so the test discriminates on both claimed
	// platforms. On Linux os.TempDir() is /tmp anyway, and a check against it
	// would pass against the hardcoded literal it replaced; here TMPDIR is
	// asserted, which is what macOS launchd sets to a per-user directory and
	// what the /tmp literal named wrongly.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	want, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := sandboxRoots(procOpts{Dir: base, Tool: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(roots, want) {
		t.Fatalf("sandbox roots %q do not include the platform temp dir %q", roots, want)
	}
	// And nothing wider. /tmp is shared and world-writable, and on macOS it is
	// a symlink to /private/tmp rather than a process's own temporary
	// directory: granting it alongside TMPDIR made every sandboxed agent able
	// to reach every other user's scratch files, which is what the temp grant
	// exists to prevent.
	if shared, err := filepath.EvalSymlinks("/tmp"); err == nil && shared != want {
		if slices.Contains(roots, shared) {
			t.Fatalf("sandbox roots %q grant the shared %q as well as %q", roots, shared, want)
		}
	}
	// Every granted root is resolved and is a directory: the seatbelt profile
	// and the Landlock rule both open what they are handed.
	for _, root := range roots {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			t.Fatalf("granted root %q is not a readable directory: %v", root, err)
		}
	}
}

// A TMPDIR naming a directory that is not there costs the run that grant and
// nothing else. os.TempDir() reads the same variable, so the path is both the
// platform temp root and the explicit TMPDIR grant appended after it:
// skipping the first and refusing the second meant one missing directory took
// down every sandboxed review on a host whose TMPDIR had been cleaned up, on
// a rule the design states as skipped rather than fatal.
func TestSandboxRootsSkipAMissingTempDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(base, "no-such-tmp"))
	roots, err := sandboxRoots(procOpts{Dir: base, Tool: "claude"})
	if err != nil {
		t.Fatalf("a missing TMPDIR refused the launch: %v", err)
	}
	for _, root := range roots {
		if root == filepath.Join(base, "no-such-tmp") {
			t.Fatalf("the absent temp dir was granted, roots %q", roots)
		}
	}
}

// The temp roots are the only ones dropped rather than fatal. A missing
// --sandbox-write grant still refuses, which is what the per-root
// classification exists to keep: a count of leading optional entries would
// widen into every root after the second temp root and turn a typo into an
// agent that cannot write where the operator said it could.
func TestSandboxRootsStillRejectAMissingFatalGrant(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	_, err := sandboxRoots(procOpts{Dir: base, Tool: "claude",
		SandboxWrite: []string{filepath.Join(base, "missing")}})
	if err == nil {
		t.Fatal("a --sandbox-write path that does not exist was granted silently")
	}
}

func TestSandboxRootsRejectAMissingGrant(t *testing.T) {
	base := t.TempDir()
	// An explicit --sandbox-write names a path the operator expects to be
	// writable, so a missing one refuses the launch rather than being dropped:
	// the agent would otherwise fail on its first write, mid-review.
	_, err := sandboxRoots(procOpts{Dir: base, Tool: "claude",
		SandboxWrite: []string{filepath.Join(base, "missing")}})
	if err == nil {
		t.Fatal("a --sandbox-write path that does not exist was granted silently")
	}
}
