// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLocked means another gauntlet holds this directory's lock.
var ErrLocked = errors.New("another gauntlet is already running here")

// Lock is an exclusive, advisory lock on one review directory.
type Lock struct {
	path string
	fd   int
}

// Acquire takes the directory lock. The descriptor stays open for the lifetime
// of the process: closing it would release the flock.
//
// O_NOFOLLOW rejects a symlinked lock path and O_NONBLOCK keeps a planted FIFO
// from blocking the open forever; the mode is checked on the descriptor, not
// the path, so the check cannot be raced.
func Acquire(path string) (*Lock, error) {
	fd, err := syscall.Open(path,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cannot open lock file %s: %w "+
			"(the lock path must be a creatable regular file)", path, err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		syscall.Close(fd)
		return nil, fmt.Errorf("lock path is not a regular file: %s", path)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (lock: %s)", ErrLocked, path)
		}
		return nil, err
	}
	return &Lock{path: path, fd: fd}, nil
}

// Release drops the lock and removes the file, so reviewed repos are not
// littered with stray locks.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	_ = os.Remove(l.path)
	_ = syscall.Flock(l.fd, syscall.LOCK_UN)
	_ = syscall.Close(l.fd)
}

// RealPath resolves a path for own-artifact comparisons. Symlinks are resolved
// so a repo file merely named like the lock is still seen as a real change.
func RealPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}
