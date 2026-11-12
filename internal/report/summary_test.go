// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/runner"
)

// The per-agent row names every status the run summary names. ok, fail, and
// timeout are always there; skipped, interrupted, conflict, and unrecognized
// outcomes appear only for the agent that has them, the way the summary's own
// rows stay quiet at zero.
func TestSummaryPerAgentCountsEveryStatus(t *testing.T) {
	d := Dir{Loops: 1, Stats: &runner.Stats{}}
	claude, codex := agent.Spec{Tool: "claude"}, agent.Spec{Tool: "codex"}
	d.Stats.Add(runner.Result{Review: "a-review", Agent: claude, Status: runner.StatusOK})
	d.Stats.Add(runner.Result{Review: "b-review", Agent: claude, Status: runner.StatusSkipped})
	d.Stats.Add(runner.Result{Review: "c-review", Agent: claude, Status: runner.StatusInterrupted})
	d.Stats.Add(runner.Result{Review: "d-review", Agent: claude, Status: runner.StatusConflict})
	d.Stats.Add(runner.Result{Review: "e-review", Agent: claude, Status: runner.Status("deferred")})
	d.Stats.Add(runner.Result{Review: "f-review", Agent: codex, Status: runner.StatusFail})
	d.Stats.Add(runner.Result{Review: "g-review", Agent: codex, Status: runner.StatusTimeout})

	var out bytes.Buffer
	Summary(&out, Palette{}, []Dir{d}, time.Second)
	got := out.String()
	if !strings.Contains(got, "ok=1 fail=0 timeout=0 skipped=1 interrupted=1 conflict=1 other=1") {
		t.Errorf("per-agent row dropped a status the summary counts:\n%s", got)
	}
	// No trailing suffix: a zero bucket must not appear, so the line ends
	// at timeout.
	if !strings.Contains(got, "ok=0 fail=1 timeout=1\n") {
		t.Errorf("an agent with nothing skipped, interrupted, conflicting, or unrecognized gained a suffix:\n%s", got)
	}
}
