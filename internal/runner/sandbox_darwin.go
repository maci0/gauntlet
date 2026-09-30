// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"fmt"
	"os/exec"
)

func confinedCommand(cmd *exec.Cmd, roots []string, path string) (*exec.Cmd, error) {
	profile, err := seatbeltProfile(roots)
	if err != nil {
		return nil, err
	}
	// The system launcher calls sandbox_init (Seatbelt), without needing cgo in
	// the distributed binary. An absolute path cannot be shadowed by the repo.
	args := append([]string{"-p", profile, path}, cmd.Args[1:]...)
	child := exec.Command("/usr/bin/sandbox-exec", args...)
	child.Dir, child.Env, child.Stdin, child.SysProcAttr = cmd.Dir, cmd.Env, cmd.Stdin, cmd.SysProcAttr
	return child, nil
}

func enforceSandbox([]string) error { return fmt.Errorf("seatbelt uses /usr/bin/sandbox-exec") }
