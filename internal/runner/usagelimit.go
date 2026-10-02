// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Stopping on a provider usage limit. A subscription's rolling window (the
// five-hour one, for Claude) is not something the runner can observe: the
// figure lives in the provider's API response headers, and none of the agent
// CLIs expose it to a headless launch. So the operator supplies the probe and
// the runner supplies the timing -- it asks between reviews, never mid-review.

package runner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maci0/gauntlet/internal/runx"
)

// usageProbeTimeout caps one probe. Reading a percentage is a fast call (a
// cached file, one HTTP request); a probe that hangs must not hold up the
// review that is waiting on the answer, and failing open is the safe default.
const usageProbeTimeout = 10 * time.Second

// usageProbeWait is how long Wait may outlive the deadline kill before it
// gives up on an unreapable child, the same insurance runProc's drainGrace
// carries. Shorter than the probe timeout: the kill already went out.
const usageProbeWait = 5 * time.Second

// checkUsageLimit asks the configured probe how much of the provider's usage
// window is gone and, at or above the limit, converts that into the graceful
// quit the runner already has: the review in flight finishes, its branch is
// pushed and its PR opened, the commit and merge steps still run, and nothing
// new starts. It is deliberately the same mechanism as an operator's finish
// request rather than a second kind of stop.
//
// Called before each attempt a run starts, including a retry relaunch and an
// agent-pool fallback, and never on the way out, so the cost is one
// short-lived process per started attempt: not per line of agent output, and
// not one wasted per lane when there is nothing left to stop.
func (r *Runner) checkUsageLimit(ctx context.Context) {
	if len(r.cfg.UsageCmd) == 0 || r.cfg.UsageLimit <= 0 || math.IsNaN(r.cfg.UsageLimit) {
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
	if !r.finish.Swap(true) {
		r.log("Usage at %.1f%% of the provider's window, at or past the %.1f%% limit: "+
			"finishing the review in flight and starting no more", pct, r.cfg.UsageLimit)
	}
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
	bin := resolveProbe(argv[0])
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Dir = os.TempDir()
	cmd.Env = runx.AbsPATHEnv()
	cmd.Stdin = nil
	// Own process group and an explicit group kill, like every other
	// subprocess here: a probe that forks must not outlive its own timeout.
	out, errOut := runx.Bound(cmd, usageProbeMaxBytes, usageProbeWait)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if err := runx.Outcome(ctx, cmd.Run()); err != nil {
		if detail := strings.TrimSpace(errOut.String()); detail != "" {
			return 0, fmt.Errorf("%w: %s", err, runx.FirstLine(detail))
		}
		return 0, err
	}
	if out.Hit || errOut.Hit {
		return 0, fmt.Errorf("probe printed more than %d bytes", usageProbeMaxBytes)
	}
	return parseUsagePercent(out.String())
}

func resolveProbe(name string) string {
	if p := runx.LookPath(name); p != "" {
		return p
	}
	return name
}

// usageProbeMaxBytes is plenty for a percentage and a line or two of
// narration. A var so tests can shrink it; production always sees this.
var usageProbeMaxBytes = 4 << 10

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
	// The percent sign is written after a space in half the locales, and
	// fr_FR writes a narrow no-break space there, which TrimSpace (ASCII
	// spaces only) does not drop.
	field = strings.TrimSpace(strings.TrimSuffix(strings.TrimRightFunc(field, unicode.IsSpace), "%"))
	if field == "" {
		return 0, errors.New("probe printed no percentage")
	}
	pct, err := parseLocaleFloat(field)
	if err != nil {
		return 0, fmt.Errorf("probe printed %q, want a percentage", runx.FirstLine(field))
	}
	// Before the range check, because a range check cannot catch these.
	// ParseFloat accepts "NaN" and the infinities as valid floats, and every
	// comparison against NaN is false: `pct < 0 || pct > 100` waves NaN
	// through, and so does the caller's `pct < limit`, so a probe printing
	// "NaN" would read as "at or past the limit" and end the run on its very
	// first check. None of the three is a measurement.
	if math.IsNaN(pct) || math.IsInf(pct, 0) {
		return 0, fmt.Errorf("probe printed %q, want a percentage", runx.FirstLine(field))
	}
	if pct < 0 || pct > 100 {
		return 0, fmt.Errorf("probe printed %g, outside 0-100", pct)
	}
	return pct, nil
}

// parseLocaleFloat reads a number a probe wrote in its own locale. The probe is
// the operator's own command and runs with the ambient environment, so its
// answer carries the host's number formatting, not the C one: de_DE and fr_FR
// write the decimal point as a comma and group with a period or a no-break
// space ("85,5", "1.234,5", "1 234,5"). strconv.ParseFloat reads the C form
// only and refuses all three, so on such a host every probe failed to parse,
// the limit was reported as ignored, and the run went on to spend the very
// window --usage-limit exists to stop.
//
// Only the separators are rewritten, and only for a field that is a single
// decimal number in one of those forms: the digits either side of the decimal
// separator are read, and every other mark has to sit between three-digit
// groups. "1,2,3" and "1,23" are therefore left to ParseFloat and fail as they
// did before, so a probe's error message cannot become a figure, and a field
// already in the C form parses to the value it always did.
func parseLocaleFloat(field string) (float64, error) {
	body, sign, exponent, ok := localeNumberParts(field)
	if !ok {
		return strconv.ParseFloat(field, 64)
	}
	// Whichever of the marks comes last is the decimal one: "1.234,5" is one
	// and a bit, "1,234.5" is a thousand and a bit. With only one present it
	// is the decimal separator in every locale that writes one, so "85,5" and
	// "85.5" both read as eighty-five and a half. The caller's range check
	// bounds the rest: "1.500" read as 1.5 rather than 1500 is not a figure a
	// run can act on, and a 1500% window is not one either.
	whole, frac, mark := splitDecimal(body)
	if !mark {
		// No decimal separator: every mark in the whole part has to be a
		// grouping one, so "1 234" is a number and "1 23" is not.
		if !grouped(whole) {
			return strconv.ParseFloat(field, 64)
		}
		return strconv.ParseFloat(sign+stripGrouping(whole)+exponent, 64)
	}
	// A mark inside the fraction is a thousands separator in no locale: the
	// digits after the decimal point are written as they are.
	if strings.ContainsFunc(frac, isGroupMark) || !grouped(whole) {
		return strconv.ParseFloat(field, 64)
	}
	return strconv.ParseFloat(sign+stripGrouping(whole)+"."+stripGrouping(frac)+exponent, 64)
}

// splitDecimal cuts body at the last mark a locale writes in it, and reports
// whether there was one. Each side is validated by the caller.
func splitDecimal(body string) (whole, frac string, found bool) {
	// A group space is several bytes, so the marks are walked as runes rather
	// than as bytes.
	last, size := -1, 0
	for i, r := range body {
		if isGroupMark(r) {
			last, size = i, utf8.RuneLen(r)
		}
	}
	if last < 0 {
		return body, "", false
	}
	return body[:last], body[last+size:], true
}

// grouped reports whether every mark in s sits between three-digit groups,
// which is how a locale writes them: "1,234,567" and "1 234 567" are numbers,
// "1,23" is text that happens to contain a comma.
func grouped(s string) bool {
	for {
		i := strings.IndexFunc(s, isGroupMark)
		if i < 0 {
			return true
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		s = s[i+w:]
		if len(s) < 3 || !allDigits(s[:3]) {
			return false
		}
		s = s[3:]
	}
}

// allDigits reports whether s is ASCII digits, which is what a group of three
// has to be for the mark before it to be a thousands separator.
func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// localeNumberParts splits a signed decimal number whose digits may be grouped
// with ".", "," or a no-break space into its sign, digit body, and exponent.
// false means field is not such a number and the caller must not touch it.
//
// The exponent comes off first: "1,5E3" is 1.5e3, and the comma there is the
// decimal separator rather than a grouping mark, so the caller has to be
// looking at the mantissa alone when it decides which mark that is.
func localeNumberParts(field string) (body, sign, exponent string, ok bool) {
	if field == "" {
		return "", "", "", false
	}
	switch field[0] {
	case '+', '-':
		sign, field = field[:1], field[1:]
	}
	if i := strings.LastIndexAny(field, "eE"); i >= 0 {
		field, exponent = field[:i], field[i:]
	}
	if field == "" {
		return "", "", "", false
	}
	for _, r := range field {
		if !(r >= '0' && r <= '9') && !isGroupMark(r) {
			return "", "", "", false
		}
	}
	return field, sign, exponent, true
}

// stripGrouping drops the thousands separators a locale writes and keeps the
// digits, so what is left is the form ParseFloat reads.
func stripGrouping(s string) string {
	return strings.Map(func(r rune) rune {
		if isGroupMark(r) {
			return -1
		}
		return r
	}, s)
}

// isGroupMark reports whether r is a mark a locale puts between digit groups
// or between the whole and the fraction: ".", ",", and the no-break space fr
// and ru group with, the narrow no-break space that separates "85" from "%" in
// "85 %", and the thin space a few locales use.
func isGroupMark(r rune) bool {
	return r == '.' || r == ',' || r == ' ' || r == 0x00a0 || r == 0x202f || r == 0x2009
}
