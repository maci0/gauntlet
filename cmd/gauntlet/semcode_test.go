// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// semcodeIndex builds with a fake indexer of the given shell body and returns
// its exit code with everything it wrote to stderr.
func semcodeIndex(t *testing.T, body string) (int, string) {
	t.Helper()
	bindir := t.TempDir()
	fake := filepath.Join(bindir, "semcode-index")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))

	restore := semcodeIndexTimeout
	semcodeIndexTimeout = 200 * time.Millisecond
	t.Cleanup(func() { semcodeIndexTimeout = restore })

	errFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()
	realErr := os.Stderr
	os.Stderr = errFile
	defer func() { os.Stderr = realErr }()

	var out bytes.Buffer
	code := buildSemcodeIndex(t.Context(), &out, []*dirRun{{dir: t.TempDir()}})
	if err := errFile.Sync(); err != nil {
		t.Fatal(err)
	}
	said, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(said)
}

// A build that runs out its budget has to say so. The deadline is read before
// the context is cancelled, so the report survives the cancel that follows
// every run; reading it afterwards would report context.Canceled and every
// expired build would look like a plain nonzero exit.
func TestSemcodeIndexTimeoutIsReported(t *testing.T) {
	code, said := semcodeIndex(t, "sleep 30")
	if code != exitFail {
		t.Fatalf("timed-out index build returned %d, want %d", code, exitFail)
	}
	if !strings.Contains(said, "timed out after") {
		t.Fatalf("timed-out index build did not report the timeout:\n%s", said)
	}
}

// A build that exits on its own is not a timeout, and the two reports must not
// be confused.
func TestSemcodeIndexNonzeroExitIsNotATimeout(t *testing.T) {
	code, said := semcodeIndex(t, "exit 3")
	if code != exitFail {
		t.Fatalf("failed index build returned %d, want %d", code, exitFail)
	}
	if strings.Contains(said, "timed out") {
		t.Fatalf("a nonzero exit was reported as a timeout:\n%s", said)
	}
	if !strings.Contains(said, "exit code 3") {
		t.Fatalf("a nonzero exit did not report its status:\n%s", said)
	}
}
