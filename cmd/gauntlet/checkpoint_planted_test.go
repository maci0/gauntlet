// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A crash checkpoint carries the argv `gauntlet resume` executes, so it is read
// through the one guarded open the other repository-planted paths use. The name
// is reachable by the reviewed repository whenever GAUNTLET_HOME points inside
// the tree, and a FIFO in that place has to be refused rather than block the
// resume on a reader waiting for a writer that never comes.
//
// A static FIFO was refused by the old type check too, so this pins the
// no-block half of the contract on its own: the guarded open clears
// O_NONBLOCK only once the descriptor is known to be regular, which a check
// that never opened the file could not promise. Refusing a node swapped into
// the name between the check and the read is the rest of what the open buys,
// and that window cannot be pinned by a test that plants one node and waits.
func TestReadCheckpointRefusesPlantedNode(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	const runID = "20261001T015123Z-1a2b"
	path := mustCheckpointPath(t, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := readCheckpoint(path)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("readCheckpoint read a FIFO as a checkpoint")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("readCheckpoint blocked on a FIFO in the checkpoint's place")
	}
}
