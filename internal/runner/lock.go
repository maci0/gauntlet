// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/maci0/gauntlet/internal/normalize"
)

// A lock note is one terminal line, bounded on the way in and on the way out.
// The file sits in the reviewed tree, where an agent could rewrite it, so what
// comes back is untrusted text: one line, stripped before it reaches a
// terminal, and never longer than gauntlet itself would write.
const (
	noteRunes = 120             // what a holder may say
	noteLimit = 4*noteRunes + 8 // its worst case in bytes, plus the newline
)

// ErrLocked means another gauntlet holds this directory's lock.
var ErrLocked = errors.New("another gauntlet is already running here")

// Lock is an exclusive, advisory lock on one review directory.
type Lock struct {
	path string
	fd   int // -1 once released
}

// Acquire takes the directory lock. The descriptor stays open for the lifetime
// of the process: closing it would release the flock.
//
// O_NOFOLLOW rejects a symlinked lock path and O_NONBLOCK keeps a planted FIFO
// from blocking the open forever; the mode is checked on the descriptor, not
// the path, so the check cannot be raced.
func Acquire(path string) (*Lock, error) {
	fd, err := syscall.Open(path,
		syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
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
		defer syscall.Close(fd)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if note := readNote(fd); note != "" {
				return nil, fmt.Errorf("%w: %s (lock: %s)", ErrLocked, note, path)
			}
			return nil, fmt.Errorf("%w (lock: %s)", ErrLocked, path)
		}
		return nil, err
	}
	return &Lock{path: path, fd: fd}, nil
}

// Note records what the holder is doing now, so the next gauntlet to try this
// directory is told what it is waiting for instead of just that it must wait.
// Best effort: a note that cannot be written costs nothing but the message.
func (l *Lock) Note(text string) {
	if l == nil || l.fd < 0 {
		return
	}
	line := append([]byte(normalize.Truncate(normalize.Sanitize(text), noteRunes)), '\n')
	if _, err := syscall.Pwrite(l.fd, line, 0); err != nil {
		return
	}
	// A shorter note must not leave the tail of a longer one behind it.
	_ = syscall.Ftruncate(l.fd, int64(len(line)))
}

// readNote returns the holder's note, or "" when there is none to read. The
// descriptor is the one just opened, so no second path lookup can be raced.
func readNote(fd int) string {
	buf := make([]byte, noteLimit)
	n, err := syscall.Pread(fd, buf, 0)
	if err != nil || n <= 0 {
		return ""
	}
	line, _, _ := strings.Cut(string(buf[:n]), "\n")
	return normalize.Display(strings.TrimSpace(line))
}

// Release drops the lock and removes the file, so reviewed repos are not
// littered with stray locks. It is safe to call twice: a hot reload releases
// before the exec, and a failed exec leaves the deferred release to run on the
// same lock. Closing an already-closed descriptor twice is not harmless, the
// second close can land on a recycled fd, so Release marks the lock spent.
func (l *Lock) Release() {
	if l == nil || l.fd < 0 {
		return
	}
	fd := l.fd
	l.fd = -1
	_ = os.Remove(l.path)
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	_ = syscall.Close(fd)
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
