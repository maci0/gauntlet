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

// terminalStream is what a child wrote to one of the terminal handles while a
// capture was in place.
type terminalStream struct{ stdout, stderr string }

// captureTerminal runs f with both terminal handles pointed at temporary
// files, so a child that writes to them can be inspected afterwards. It
// returns the child's exit code alongside what it wrote; assertions belong to
// the caller, because a failure inside f would unwind past the reads.
func captureTerminal(t *testing.T, f func() int) (code int, got terminalStream) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.CreateTemp(dir, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer outFile.Close()
	errFile, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer errFile.Close()

	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	code = f()
	os.Stdout, os.Stderr = realOut, realErr

	for _, file := range []*os.File{outFile, errFile} {
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	out, err := os.ReadFile(outFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.ReadFile(errFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, terminalStream{stdout: string(out), stderr: string(errOut)}
}

// The indexer reports the files it walked, and a reviewed repository names
// them. Its output is the one child stream that reaches the operator's
// terminal, so both handles have to carry the same Display filter every other
// child output does: a file name carrying an escape sequence or a bidi
// override must not be able to drive the terminal, and the surrounding text
// must still arrive.
func TestRunIndexerSanitizesHostileFileNames(t *testing.T) {
	bindir := t.TempDir()
	fake := filepath.Join(bindir, "semcode-index")
	body := "printf '\\033[2J\\033[31mindexed\\033[0m %s\\n' \"$(ls)\"\n" +
		"printf 'failed %s\\n' \"$(ls)\" >&2\n"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}

	// The child names the files it walked, so the hostile name has to be in
	// the tree it is pointed at: the payload arrives through the filesystem,
	// exactly as a hostile repository would deliver it.
	dir := t.TempDir()
	hostile := "a\u202eb.go"
	if err := os.WriteFile(filepath.Join(dir, hostile), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	code, got := captureTerminal(t, func() int {
		return runIndexer(t.Context(), fake, []string{"-s", "."}, dir)
	})
	if code != exitOK {
		t.Fatalf("runIndexer returned %d, want %d (stdout %q, stderr %q)",
			code, exitOK, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "indexed a") {
		t.Fatalf("sanitizing dropped the indexer's own output: %q", got.stdout)
	}
	if !strings.Contains(got.stderr, "failed a") {
		t.Fatalf("sanitizing dropped the indexer's own error output: %q", got.stderr)
	}
	for _, stream := range []string{got.stdout, got.stderr} {
		if strings.ContainsRune(stream, 0x1b) {
			t.Fatalf("an escape sequence reached the terminal: %q", stream)
		}
		if strings.ContainsRune(stream, 0x202e) {
			t.Fatalf("a bidi override reached the terminal: %q", stream)
		}
	}
}
