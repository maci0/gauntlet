// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/normalize"
)

// runFakeProc runs one fake agent and returns every line its sink received.
func runFakeProc(t *testing.T, body string, tune func(*procOpts)) ([]normalize.Line, procResult) {
	t.Helper()
	bin := fakeAgent(t, t.TempDir(), "agent", body)
	var got []normalize.Line
	opts := procOpts{
		Argv:    []string{bin},
		Timeout: 30 * time.Second,
		Sink:    func(l normalize.Line) { got = append(got, l) },
	}
	if tune != nil {
		tune(&opts)
	}
	return got, runProc(context.Background(), opts)
}

func TestRawOutputCannotDriveTheTerminal(t *testing.T) {
	got, res := runFakeProc(t,
		`printf '\033[31mRED\033[0m plain\n'`,
		func(o *procOpts) { o.Raw = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 line, got %d: %+v", len(got), got)
	}
	if want := "RED plain"; got[0].Text != want {
		t.Fatalf("got %q, want %q", got[0].Text, want)
	}
}

func TestStreamThinkingCannotDriveTheTerminal(t *testing.T) {
	got, res := runFakeProc(t,
		`printf '%s\n' '{"type":"thinking","thinking":"step \u001b]0;pwned\u0007one"}'`,
		func(o *procOpts) { o.Stream = true })
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: %+v", res)
	}
	var think []normalize.Line
	for _, l := range got {
		if l.Kind == normalize.Thinking {
			think = append(think, l)
		}
	}
	if len(think) != 1 {
		t.Fatalf("want 1 thinking line, got %d in %+v", len(think), got)
	}
	if want := "step one"; think[0].Text != want {
		t.Fatalf("got %q, want %q", think[0].Text, want)
	}
}
