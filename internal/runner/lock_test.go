// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
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
