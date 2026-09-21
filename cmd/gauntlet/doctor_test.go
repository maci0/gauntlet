// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/prompt"
)

// Doctor assembles its review catalog from agent.ReviewTools and
// agent.ReviewsWithoutTools, while the reviews themselves are defined by
// internal/prompt's embedded prompts. Nothing in the type system ties the two
// together: a new review prompt that skips the tool table would silently
// vanish from doctor, and a stale name would show a phantom row or advertise
// tools for a review that no longer exists.
func TestDoctorReviewCatalogMatchesTheBundledReviews(t *testing.T) {
	bundled := make(map[string]bool)
	for _, n := range prompt.BundledNames() {
		bundled[n] = true
	}

	cataloged := make(map[string]string) // review -> which list named it
	note := func(name, list string) {
		if !bundled[name] {
			t.Errorf("%s lists %q, which is not a bundled review", list, name)
			return
		}
		if prev := cataloged[name]; prev != "" {
			t.Errorf("%q appears in both %s and %s; each bundled review is listed exactly once", name, prev, list)
		}
		cataloged[name] = list
	}
	for name := range agent.ReviewTools {
		note(name, "agent.ReviewTools")
	}
	for _, name := range agent.ReviewsWithoutTools {
		note(name, "agent.ReviewsWithoutTools")
	}

	var missing []string
	for _, n := range prompt.BundledNames() {
		if cataloged[n] == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("bundled reviews absent from doctor's catalog (add them to agent.ReviewTools or agent.ReviewsWithoutTools): %s",
			strings.Join(missing, ", "))
	}
}

func TestDoctorCustomAgentCounting(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "mycustombin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := agent.Register("custombot", agent.Custom{
		Argv: []string{"mycustombin", "-p", "{prompt}"},
	}); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	code := doctor(&buf, palette{}, nil, 80)
	out := buf.String()
	if !strings.Contains(out, "✓ custombot") {
		t.Fatalf("doctor should show custombot as installed (code %d):\n%s", code, out)
	}
}

func TestDoctorReportsOutputFailure(t *testing.T) {
	sink := &doctorFailWriter{remaining: 0}
	code, diagnostic := captureStderrFor(t, func() int {
		return doctor(sink, palette{}, nil, 80)
	})
	if code != exitFail || !strings.Contains(diagnostic.String(), "cannot write doctor report: "+io.ErrClosedPipe.Error()) {
		t.Fatalf("exit %d, stderr %q", code, diagnostic.String())
	}
}

type doctorFailWriter struct {
	bytes.Buffer
	remaining int
}

func (w *doctorFailWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.remaining)
	w.Buffer.Write(p[:n])
	w.remaining -= n
	if n < len(p) {
		return n, io.ErrClosedPipe
	}
	return n, nil
}
