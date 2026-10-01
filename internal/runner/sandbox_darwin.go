// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"os"
	"os/exec"
)

// seatbeltLauncher is the system binary that applies a Seatbelt profile
// without needing cgo in the distributed binary. Stated once, so the launch
// and the probe that reports whether confinement is possible at all cannot
// name different files.
const seatbeltLauncher = "/usr/bin/sandbox-exec"

func confinedCommand(cmd *exec.Cmd, roots []string, path string) (*exec.Cmd, error) {
	profile, err := seatbeltProfile(roots)
	if err != nil {
		return nil, err
	}
	// An absolute path cannot be shadowed by the repo.
	args := append([]string{"-p", profile, path}, cmd.Args[1:]...)
	child := exec.Command(seatbeltLauncher, args...)
	child.Dir, child.Env, child.Stdin, child.SysProcAttr = cmd.Dir, cmd.Env, cmd.Stdin, cmd.SysProcAttr
	return child, nil
}

func enforceSandbox([]string) error { return fmt.Errorf("seatbelt uses " + seatbeltLauncher) }

// sandboxSupport probes for the Seatbelt launcher rather than trusting that a
// binary built for darwin runs on it: the file is what confines the agent, and
// a macOS install without it has no confinement to report.
func sandboxSupport() (string, bool, error) {
	if fi, err := os.Stat(seatbeltLauncher); err != nil {
		return "", false, fmt.Errorf("%s is missing: %w", seatbeltLauncher, err)
	} else if fi.Mode().Perm()&0o111 == 0 {
		return "", false, fmt.Errorf("%s is not executable", seatbeltLauncher)
	}
	return seatbeltLauncher + " (Seatbelt)", true, nil
}
