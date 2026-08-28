// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Stopping on a provider usage limit. A subscription's rolling window (the
// five-hour one, for Claude) is not something the runner can observe: the
// figure lives in the provider's API response headers, and none of the agent
// CLIs expose it to a headless launch. So the operator supplies the probe and
// the runner supplies the timing -- it asks between reviews, never mid-review.

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// usageProbeTimeout caps one probe. Reading a percentage is a fast call (a
// cached file, one HTTP request); a probe that hangs must not hold up the
// review that is waiting on the answer, and failing open is the safe default.
const usageProbeTimeout = 10 * time.Second

// checkUsageLimit asks the configured probe how much of the provider's usage
// window is gone and, at or above the limit, converts that into the graceful
// quit the runner already has: the review in flight finishes, its branch is
// pushed and its PR opened, the commit and merge steps still run, and nothing
// new starts. It is deliberately the same mechanism as an operator's finish
// request rather than a second kind of stop.
//
// Called where the loops decide whether to start another review, so the cost
// is one short-lived process per review, not per line of agent output.
func (r *Runner) checkUsageLimit(ctx context.Context) {
	if len(r.cfg.UsageCmd) == 0 || r.cfg.UsageLimit <= 0 {
		return
	}
	// Already quitting: the answer cannot change the outcome, and a probe per
	// remaining loop would keep spawning processes on the way out.
	if r.finish.Load() || r.soft.Load() || ctx.Err() != nil {
		return
	}
	pct, err := probeUsage(ctx, r.cfg.UsageCmd)
	if err != nil {
		// Fail open, and say so once per run rather than once per review: a
		// broken probe must not quietly end a run early, and must not bury
		// the agents' own output either.
		if !r.usageProbeFailed.Swap(true) {
			r.log("Usage probe failed, ignoring the usage limit for this run: %v", err)
		}
		return
	}
	if pct < r.cfg.UsageLimit {
		return
	}
	r.log("Usage at %.1f%% of the provider's window, at or past the %.1f%% limit: "+
		"finishing the review in flight and starting no more", pct, r.cfg.UsageLimit)
	r.finish.Store(true)
}

// probeUsage runs the operator's command and reads a percentage off its
// stdout. The contract is one number, because that is the one thing every
// source of this figure can be reduced to with the tools already on the
// machine; a trailing percent sign is tolerated since that is how the number
// is usually printed.
//
// argv elements are passed to exec directly, so no shell parses the command
// and nothing in it is expanded. The reviewed repository is not the working
// directory: this is the operator's command, not the agent's, and it has no
// business being resolved against untrusted content.
func probeUsage(ctx context.Context, argv []string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, usageProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	// Own process group and an explicit group kill, like every other
	// subprocess here: a probe that forks must not outlive its own timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(errOut.String()); detail != "" {
			return 0, fmt.Errorf("%w: %s", err, firstLine(detail))
		}
		return 0, err
	}
	return parseUsagePercent(out.String())
}

// parseUsagePercent reads the probe's answer. Anything that is not a single
// number in range is an error rather than a guess: a probe whose output drifts
// (an added label, an error written to stdout, an empty reply from a failed
// lookup) must not be read as "plenty of headroom left" and let a run keep
// spending, nor as 100% and end it.
func parseUsagePercent(s string) (float64, error) {
	field := strings.TrimSpace(s)
	// A probe built from `jq` or `printf` may end up printing more than one
	// line; the figure is the last non-empty one, which is what a pipeline
	// that echoes progress first would produce.
	if i := strings.LastIndexByte(strings.TrimRight(field, "\n"), '\n'); i >= 0 {
		field = strings.TrimSpace(field[i+1:])
	}
	field = strings.TrimSuffix(strings.TrimSpace(field), "%")
	field = strings.TrimSpace(field)
	if field == "" {
		return 0, errors.New("probe printed no percentage")
	}
	pct, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, fmt.Errorf("probe printed %q, want a percentage", firstLine(field))
	}
	if pct < 0 || pct > 100 {
		return 0, fmt.Errorf("probe printed %g, outside 0-100", pct)
	}
	return pct, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
