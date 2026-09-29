// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maci0/gauntlet/internal/report"
)

// splitWriter takes a line apart between two destinations, the way an
// io.MultiWriter does: it records the bytes each Write received, so a caller
// whose Write interleaves with another's shows up as a line cut in two.
type splitWriter struct {
	mu   sync.Mutex
	part []string
}

func (s *splitWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.part = append(s.part, string(p))
	return len(p), nil
}

func (s *splitWriter) whole() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.part, "")
}

// The reporter and the signal handlers share one destination, and with --log
// that destination writes to two files in sequence. Unsynchronized, a signal
// line lands between the halves of an output line: the terminal hides it (one
// write syscall per file), the log file keeps the wreckage.
func TestSerializedKeepsLinesWholeUnderAMultiWriter(t *testing.T) {
	var log bytes.Buffer
	dst := &splitWriter{}
	out := &report.Serialized{W: io.MultiWriter(dst, &log)}

	const writers, lines = 4, 250
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range lines {
				fmt.Fprintf(out, "[w%d] line %d padded to a real length\n", w, i)
				// Yield between the two writes a single Fprintf may make, so
				// the interleave is reachable rather than merely possible.
				if rand.Intn(4) == 0 {
					fmt.Fprintf(out, "[w%d] interleave %d\n", w, i)
				}
			}
		})
	}
	wg.Wait()

	got := dst.whole()
	for _, part := range dst.part {
		if !strings.HasSuffix(part, "\n") {
			t.Fatalf("a write ended mid-line, so a line was split across two writers:\n%q", part)
		}
	}
	if log.String() != got {
		t.Fatalf("the two destinations disagree: log has %d bytes, screen %d", log.Len(), len(got))
	}
}

// With the dashboard up, three writers reach the log file and only one of them
// goes through the console stream: the file reporter, both signal handlers, and
// whatever the console stream copies. Each must be a whole line, whichever of
// them the kernel happens to split.
func TestLogWritersSerializeEveryWriterToTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	var screen bytes.Buffer
	log, _ := report.LogWriters(&screen, f)
	stdout := io.Writer(&report.Serialized{W: log})

	const writers, perWriter = 3, 200
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range perWriter {
				fmt.Fprintf(stdout, "[console w%d] line %d padded to a real length\n", w, i)
				// The file reporter and the signal handlers write to the file
				// without passing the console stream at all.
				fmt.Fprintf(log, "[file w%d] line %d padded to a real length\n", w, i)
			}
		})
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(got), "\n"), "\n")
	if len(lines) != writers*perWriter*2 {
		t.Fatalf("log has %d lines, want %d", len(lines), writers*perWriter*2)
	}
	for _, l := range lines {
		if !strings.Contains(l, "padded to a real length") {
			t.Fatalf("a line was split between two writers: %q", l)
		}
	}
}
