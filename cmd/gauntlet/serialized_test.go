// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"testing"
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
	out := &serialized{w: io.MultiWriter(dst, &log)}

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
