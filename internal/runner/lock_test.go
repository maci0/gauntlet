// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lock note names the run id, the review, and the agent CLI, and the
// reviewed tree chooses where the file lands, so the lock is not readable by
// every local account.
func TestLockFileIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)

	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	lock.Note("running sec-review")
	lock.Release()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("lock file mode is %o; want no group or other access", perm)
	}
}

// A lock file left behind by an older run keeps the mode it was created with,
// since O_CREAT's mode only applies to a new file. Acquire tightens it.
func TestAcquireTightensAPreExistingLooseLock(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("lock file mode is %o after Acquire; want it tightened to 0o600", perm)
	}
}

// Two locks on one tree from one process is the same conflict as two
// processes, and it has to read the same on both claimed platforms. flock
// alone cannot promise that: Linux ties a lock to the open file description,
// so the second open conflicts, while macOS ties it to the process and the
// second call converts the lock already held. The registry is what makes the
// refusal portable, and this is what keeps it from being dropped.
func TestAcquireTwiceInOneProcessIsLocked(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)

	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	lock.Note("running sec-review")

	second, err := Acquire(path)
	if second != nil {
		second.Release()
		t.Fatal("Acquire returned a second lock on a tree this process already holds")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire returned %v; want %v", err, ErrLocked)
	}
	if !strings.Contains(err.Error(), "running sec-review") {
		t.Fatalf("second Acquire reported %q; want the holder's note", err)
	}
	// Releasing hands the tree back, so a later run in the same process is
	// not refused by a lock nobody holds any more.
	lock.Release()
	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	again.Release()
}

// A symlinked spelling of a held tree names the same lock file, and the
// refusal must follow the file rather than the string.
func TestAcquireThroughASymlinkedPathIsLocked(t *testing.T) {
	dir := t.TempDir()
	path := LockPath(dir)
	link := filepath.Join(t.TempDir(), "tree")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	if _, err := Acquire(LockPath(link)); !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire through a symlink returned %v; want %v", err, ErrLocked)
	}
}
