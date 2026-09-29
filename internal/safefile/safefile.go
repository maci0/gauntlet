// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package safefile is the one open a file whose path a repository's contents
// can plant goes through: a project prompt, a run journal, an exclude file.
// Every refusal names the path it refused, because the alternative is an errno
// that leaves an operator with "bad file descriptor" and nothing to act on.
package safefile

import (
	"errors"
	"os"
	"syscall"
)

// errNotRegular is a descriptor stat says is not a regular file, which a
// no-follow open still admits when the target itself is a FIFO or a device.
var errNotRegular = errors.New("not a regular file")

// OpenRead opens path read-only and returns it with the stat taken at open
// time, refusing a symlink at the last component and a non-regular file in its
// place. Callers that already Lstat'd still open this way: the file can be
// swapped between the check and the read, which is how out-of-tree content
// would reach a permission-bypassed agent.
//
// O_NONBLOCK is only to survive the open of a planted FIFO, whose reader
// blocks until a writer appears, which for a planted node is never. It is
// cleared once the descriptor is known to be regular, so a read through it
// cannot get EAGAIN. O_CLOEXEC keeps the descriptor out of a child that forks
// while the read is in flight.
//
// Hardlinks are not refused: a package manager legitimately hardlinks a prompt
// from its cache, and two worktrees can share one exclude.
func OpenRead(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f, fi, err := adoptRegular(fd, path)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		f.Close()
		return nil, nil, &os.PathError{Op: "setnonblock", Path: path, Err: err}
	}
	return f, fi, nil
}

// Append opens path for appending, creating it at perm if it does not exist,
// and refusing a symlink at the last component and a non-regular file in its
// place. os.OpenFile has no O_NOFOLLOW, and a component the reviewed
// repository picks can be a link: without the flag the write lands in whatever
// the link points at.
//
// O_NONBLOCK serves the same purpose as in OpenRead, and is cleared for the
// same reason: the append must not be able to see EAGAIN once the descriptor
// is known to be regular.
func Append(path string, perm os.FileMode) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC,
		uint32(perm))
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f, fi, err := adoptRegular(fd, path)
	if err != nil {
		return nil, nil, err
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		f.Close()
		return nil, nil, &os.PathError{Op: "setnonblock", Path: path, Err: err}
	}
	return f, fi, nil
}

// adoptRegular takes ownership of fd and hands it back only if stat says it is
// a regular file. The descriptor is closed on every error path, so a caller
// never has to.
func adoptRegular(fd int, path string) (*os.File, os.FileInfo, error) {
	f := os.NewFile(uintptr(fd), path)
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	return f, fi, nil
}
