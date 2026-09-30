// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/unix"
)

func landlockABI() (uintptr, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, fmt.Errorf("landlock unavailable: %w (use --no-sandbox only for trusted runs)", errno)
	}
	return abi, nil
}

func confinedCommand(cmd *exec.Cmd, roots []string, path string) (*exec.Cmd, error) {
	if _, err := landlockABI(); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(roots)
	if err != nil {
		return nil, err
	}
	args := append([]string{sandboxExecArg, string(encoded), path}, cmd.Args...)
	child := exec.Command(self, args...)
	child.Dir, child.Env, child.Stdin, child.SysProcAttr = cmd.Dir, cmd.Env, cmd.Stdin, cmd.SysProcAttr
	return child, nil
}

func landlockAccess(abi uintptr) uint64 {
	rights := uint64((1 << 13) - 1)
	if abi >= 2 {
		rights |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		rights |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		rights |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return rights
}

func enforceSandbox(roots []string) error {
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	handled := landlockAccess(abi)
	// Only the first eight bytes: older ABIs do not know the network/scope fields.
	attr := unix.LandlockRulesetAttr{Access_fs: handled}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), 8, 0)
	if errno != 0 {
		return fmt.Errorf("landlock ruleset: %w", errno)
	}
	defer unix.Close(int(fd))
	add := func(path string, access uint64, directory bool) error {
		flags := unix.O_PATH | unix.O_CLOEXEC
		if directory {
			flags |= unix.O_DIRECTORY
		}
		parent, err := unix.Open(path, flags, 0)
		if err != nil {
			return fmt.Errorf("sandbox root %q: %w", path, err)
		}
		defer unix.Close(parent)
		rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(parent)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("landlock rule %q: %w", path, errno)
		}
		return nil
	}
	read := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	if err := add("/", read, true); err != nil {
		return err
	}
	for _, root := range roots {
		if err := add(root, handled, true); err != nil {
			return err
		}
	}
	// Stdio may be reopened by children. Files grant only file rights.
	if err := add("/dev/null", unix.LANDLOCK_ACCESS_FS_WRITE_FILE|(handled&unix.LANDLOCK_ACCESS_FS_IOCTL_DEV), false); err != nil {
		return err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("sandbox no_new_privs: %w", err)
	}
	_, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock restrict: %w", errno)
	}
	return nil
}
