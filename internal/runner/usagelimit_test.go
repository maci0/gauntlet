// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCappedWriterKeepsThenDiscards(t *testing.T) {
	w := &cappedWriter{limit: 8}
	n, err := w.Write([]byte("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if !w.hit || w.String() != "hello wo" {
		t.Fatalf("got %q hit=%v", w.String(), w.hit)
	}
	n, err = w.Write([]byte("more"))
	if err != nil || n != 4 || w.String() != "hello wo" {
		t.Fatalf("discarded write: %d %v %q", n, err, w.String())
	}
}

func TestParseUsagePercent(t *testing.T) {
	// The probe's answer decides whether a run keeps spending. Reading a bad
	// answer as a low number would spend the rest of the window; reading it as
	// a high one would end the run for nothing. So anything that is not a
	// single number in range is an error, not a guess.
	for _, tc := range []struct {
		in   string
		want float64
		bad  bool
	}{
		{in: "42", want: 42},
		{in: "  85.5\n", want: 85.5},
		{in: "91%", want: 91},
		{in: "0", want: 0},
		{in: "100", want: 100},
		{in: "100.0", want: 100},
		// A pipeline that narrates before printing the figure: the answer is
		// the last line, not the first.
		{in: "probing...\n77\n", want: 77},
		{in: "", bad: true},
		{in: "\n \n", bad: true},
		{in: "unknown", bad: true},
		{in: "usage: 42", bad: true},
		{in: "-1", bad: true},
		{in: "100.1", bad: true},
		{in: "101", bad: true},
		// ParseFloat takes these, and a range check cannot reject NaN: every
		// comparison against it is false, so it would pass as a percentage
		// and then read as "at or past the limit" at the call site.
		{in: "NaN", bad: true},
		{in: "nan", bad: true},
		{in: "Inf", bad: true},
		{in: "+Inf", bad: true},
		{in: "-Inf", bad: true},
	} {
		got, err := parseUsagePercent(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("parseUsagePercent(%q) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseUsagePercent(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

// probeScript writes a probe that prints the given answers in order, one per
// invocation, repeating the last one once it runs out. A counter on disk is
// what makes "under the limit, then over it" expressible in a shell script.
func probeScript(t *testing.T, answers ...string) []string {
	t.Helper()
	dir := t.TempDir()
	counter := filepath.Join(dir, "n")
	var cases strings.Builder
	for i, a := range answers {
		cases.WriteString("  " + itoa(i) + ") printf '" + a + "\\n' ;;\n")
	}
	body := "n=0\n" +
		"[ -f " + counter + " ] && n=$(cat " + counter + ")\n" +
		"expr $n + 1 > " + counter + "\n" +
		"case $n in\n" + cases.String() +
		"  *) printf '" + answers[len(answers)-1] + "\\n' ;;\n" +
		"esac\n"
	return []string{fakeAgent(t, dir, "probe", body)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestUsageLimitFinishesTheReviewInFlight(t *testing.T) {
	// The point of the feature: the review that is running is not interrupted,
	// and no further review starts. A hard stop here would leave the tree with
	// uncommitted agent edits, which is the thing the graceful stop exists to
	// avoid.
	repo := testRepo(t)
	set, _ := promptSet(t, "first-review", "second-review")
	bin := fakeAgent(t, t.TempDir(), "claude", "true")
	cfg := baseConfig(t, repo, set, []string{"first-review", "second-review"}, bin)
	// Under the limit for the first review, at the limit before the second:
	// at-or-past is the documented stop, so answering exactly 80 must not
	// keep spending.
	cfg.UsageCmd = probeScript(t, "10", "80")
	cfg.UsageLimit = 80

	r := runQuiet(t, cfg)
	got := r.Stats().Counts()
	if got.OK != 1 {
		t.Fatalf("ran %d reviews, want exactly the one that was already in flight: %+v", got.OK, got)
	}
	if pending := r.Pending(); len(pending) != 0 {
		t.Fatalf("a graceful stop must drop what it never started, still queued: %v", pending)
	}
}

func TestUsageLimitUnderThresholdRunsEverything(t *testing.T) {
	// The control: the same wiring with the probe answering below the limit
	// must not change the run at all. Without this, a test that only checks
	// the stop cannot tell a working threshold from one stuck at "stop".
	repo := testRepo(t)
	set, _ := promptSet(t, "first-review", "second-review")
	bin := fakeAgent(t, t.TempDir(), "claude", "true")
	cfg := baseConfig(t, repo, set, []string{"first-review", "second-review"}, bin)
	cfg.UsageCmd = probeScript(t, "10")
	cfg.UsageLimit = 80

	if got := runQuiet(t, cfg).Stats().Counts(); got.OK != 2 {
		t.Fatalf("ran %d reviews, want 2: %+v", got.OK, got)
	}
}

func TestBrokenUsageProbeCannotEndTheRun(t *testing.T) {
	// Failing open is deliberate. A probe that exits nonzero, or answers with
	// something that is not a percentage, must be reported and ignored -- not
	// read as a reason to stop, which would let a typo in the command quietly
	// truncate every run.
	for name, probe := range map[string][]string{
		"exits nonzero":       {"/bin/sh", "-c", "exit 3"},
		"prints no number":    {"/bin/sh", "-c", "echo unavailable"},
		"does not exist":      {filepath.Join(t.TempDir(), "absent")},
		"prints out of range": {"/bin/sh", "-c", "echo 4000"},
		// NaN is the one bad answer a range check cannot see: it compares
		// false against every bound, so it used to pass validation and then
		// end the run on the first check.
		"prints NaN":      {"/bin/sh", "-c", "echo NaN"},
		"prints infinity": {"/bin/sh", "-c", "echo Inf"},
		// A probe that dumps until the timeout used to fill RAM once per
		// review. The cap fails it open, like any other broken probe.
		"prints too much": {"/bin/sh", "-c", "printf '%8192s\\n' x; echo 10"},
	} {
		t.Run(name, func(t *testing.T) {
			set, _ := promptSet(t, "first-review", "second-review")
			bin := fakeAgent(t, t.TempDir(), "claude", "true")
			cfg := baseConfig(t, testRepo(t), set,
				[]string{"first-review", "second-review"}, bin)
			cfg.UsageCmd = probe
			cfg.UsageLimit = 80
			if got := runQuiet(t, cfg).Stats().Counts(); got.OK != 2 {
				t.Fatalf("a broken probe stopped the run: %+v", got)
			}
		})
	}
}
