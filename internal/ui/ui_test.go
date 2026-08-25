// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runner"
)

func demoConfig() Config {
	return Config{
		Version: "1.0.0", RunID: "20260825T000000Z-abcd",
		Dirs: []string{"/home/dev/project"}, Agents: []string{"claude", "codex:gpt-5"},
		Reviews: []string{"sec-review", "code-review", "doc-review", "perf-review", "test-review"},
		Jobs:    2, Timeout: 30 * time.Minute, Budget: 4 * time.Hour,
		Started: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
	}
}

func demoEvents() []runner.Event {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	ins, del := 12, 3
	return []runner.Event{
		{Kind: runner.EvLoopStart, Loop: 1, Time: base},
		{Kind: runner.EvReviewStart, Review: "sec-review", Agent: "claude", Loop: 1, Time: base},
		{Kind: runner.EvReviewStart, Review: "code-review", Agent: "codex:gpt-5", Loop: 1, Time: base},
		{Kind: runner.EvOutput, Review: "sec-review", Agent: "claude", Text: "Bash(go test ./...)", LineKind: normalize.Tool, Repeat: 1},
		{Kind: runner.EvOutput, Review: "code-review", Agent: "codex:gpt-5", Text: "error: nil map write", LineKind: normalize.Error, Repeat: 1},
		// A streaming agent reporting growing usage, which becomes a live rate.
		{Kind: runner.EvUsage, Review: "code-review", Agent: "codex:gpt-5", Tokens: 400, Time: base.Add(2 * time.Second)},
		{Kind: runner.EvUsage, Review: "code-review", Agent: "codex:gpt-5", Tokens: 1600, Thinking: 640, Time: base.Add(6 * time.Second)},
		{Kind: runner.EvOutput, Review: "code-review", Agent: "codex:gpt-5", Text: "the caller already validates this", LineKind: normalize.Thinking, Repeat: 1},
		{Kind: runner.EvReviewEnd, Review: "sec-review", Agent: "claude", Loop: 1, Status: runner.StatusOK,
			Elapsed: 92, Tokens: 41234, Ins: &ins, Del: &del, Time: base.Add(92 * time.Second)},
		{Kind: runner.EvReviewEnd, Review: "doc-review", Agent: "claude", Loop: 1, Status: runner.StatusTimeout, Elapsed: 1800},
		{Kind: runner.EvMerge, Review: "sec-review", Branch: "gauntlet/x/sec-review", Status: runner.StatusConflict, Text: "CONFLICT in main.go"},
	}
}

func TestStaticFrameHasEveryInstrument(t *testing.T) {
	frame := staticFrame(demoConfig(), demoEvents(), 120, 40)
	for _, want := range []string{
		"GAUNTLET", "ACTIVITY", "AGENTS", "REVIEWS", "FEED",
		"sec-review", "claude", "2×worktree", "1.0.0",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q", want)
		}
	}
}

func TestStaticFrameFitsItsPane(t *testing.T) {
	const w, h = 100, 30
	frame := staticFrame(demoConfig(), demoEvents(), w, h)
	lines := strings.Split(frame, "\n")
	if len(lines) > h {
		t.Fatalf("frame is %d rows tall, pane is %d", len(lines), h)
	}
	for i, ln := range lines {
		if got := lipgloss.Width(ln); got > w {
			t.Fatalf("row %d is %d columns wide, pane is %d: %q", i, got, w, ln)
		}
	}
}

func TestSmallTerminalFallsBackInsteadOfBreaking(t *testing.T) {
	frame := staticFrame(demoConfig(), demoEvents(), 40, 10)
	if !strings.Contains(frame, "too small") {
		t.Fatalf("expected the minimal view, got:\n%s", frame)
	}
}

// The fallback must keep what answers "is it done, did anything break", plus
// the keys: a view that hides how to quit is its own dead end.
func TestMinimalViewKeepsStateTallyAndKeys(t *testing.T) {
	frame := stripANSI(staticFrame(demoConfig(), demoEvents(), 40, 10))
	for _, want := range []string{"RUNNING", "loop 1", "pass 1", "timeout 1", "? help", "too small"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("minimal view lost %q:\n%s", want, frame)
		}
	}
}

// Skips are part of "did anything break": the small-terminal tally counts
// them the way it counts conflicts, once any exist.
func TestMinimalViewCountsSkips(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 40, 10, true
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "doc-review",
		Agent: "claude", Status: runner.StatusSkipped, Time: base})
	if got := stripANSI(m.renderMinimal()); !strings.Contains(got, "skipped 1") {
		t.Fatalf("minimal tally %q omits the skip count", got)
	}
	if got := stripANSI(newModel(demoConfig()).renderMinimal()); strings.Contains(got, "skipped") {
		t.Fatalf("an empty tally still advertises skips: %q", got)
	}
}

// A grid that cannot hold every review says how many it dropped; a fitting
// grid stays clean.
func TestHiddenReviewsAreAnnouncedNotSilent(t *testing.T) {
	cfg := demoConfig()
	cfg.Reviews = nil
	for i := range 120 {
		cfg.Reviews = append(cfg.Reviews, fmt.Sprintf("r%03d-review", i))
	}
	frame := stripANSI(staticFrame(cfg, nil, 120, 30))
	m := regexp.MustCompile(`\+(\d+) more`).FindStringSubmatch(frame)
	if m == nil {
		t.Fatalf("an overflowing grid hid reviews without announcing it:\n%s", frame)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 || n >= 120 {
		t.Fatalf("announced %q hidden reviews, want some but not all of 120", m[1])
	}
	if !strings.Contains(frame, "r000") {
		t.Fatal("the marker replaced every visible cell")
	}
	if fit := stripANSI(staticFrame(demoConfig(), demoEvents(), 120, 40)); strings.Contains(fit, "more\n") {
		t.Fatalf("a fitting grid grew a marker: %s", fit)
	}
}

// Lanes past the panel's cap are announced the same way.
func TestHiddenAgentsAreAnnouncedNotSilent(t *testing.T) {
	cfg := demoConfig()
	cfg.Agents = nil
	for i := range 10 {
		cfg.Agents = append(cfg.Agents, fmt.Sprintf("agent%02d", i))
	}
	frame := stripANSI(staticFrame(cfg, nil, 120, 40))
	if !strings.Contains(frame, "+2 more agents") {
		t.Fatalf("10 lanes in an 8-row panel must announce the 2 hidden:\n%s", frame)
	}
}

func TestLiveTokenRateIsMeasuredNotInvented(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	lane := m.lanes["codex:gpt-5"]
	// The rate is an exponential average over the reported intervals: 400
	// tokens in the first 2s (which includes the agent's startup latency),
	// then 1200 more over 4s. Both bounds are real measurements, so the
	// smoothed value must land between them and never outside.
	if lane.tokenRate < 200 || lane.tokenRate > 300 {
		t.Fatalf("measured rate %.0f tok/s, want between the 200 and 300 samples", lane.tokenRate)
	}
	if lane.liveTokens != 1600 {
		t.Fatalf("live tokens %d, want 1600", lane.liveTokens)
	}
	// An agent that reported nothing gets no rate at all.
	if quiet := m.lanes["claude"]; quiet.tokenRate != 0 {
		t.Fatalf("invented a rate for a silent agent: %.2f", quiet.tokenRate)
	}
	if !strings.Contains(staticFrame(demoConfig(), demoEvents(), 120, 40), "tok/s live") {
		t.Fatal("live rate is measured but never shown")
	}
}

func TestThinkingIsShownAsAShareNotAGuess(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 130, 40, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if got := m.lanes["codex:gpt-5"].liveThinking; got != 640 {
		t.Fatalf("live thinking %d, want 640", got)
	}
	// An agent that never reported reasoning shows none.
	if got := m.lanes["claude"].liveThinking; got != 0 {
		t.Fatalf("invented reasoning for a silent agent: %d", got)
	}
	frame := staticFrame(demoConfig(), demoEvents(), 130, 40)
	if !strings.Contains(stripANSI(frame), "640") {
		t.Fatal("reasoning share is tracked but never shown")
	}
	if !strings.Contains(stripANSI(frame), "the caller already validates this") {
		t.Fatal("reasoning text missing from the feed")
	}
}

func TestCountersReflectResults(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if m.counts["ok"] != 1 || m.counts["timeout"] != 1 {
		t.Fatalf("counts wrong: %+v", m.counts)
	}
	if m.tokens != 41234 {
		t.Fatalf("tokens: %d", m.tokens)
	}
	if len(m.conflicts) != 1 {
		t.Fatalf("conflicts not tracked: %+v", m.conflicts)
	}
	if m.lanes["claude"].done != 2 || m.lanes["claude"].failed != 1 {
		t.Fatalf("lane tally wrong: %+v", m.lanes["claude"])
	}
}

// Interrupted reviews keep their ␘ cells, so the tally must account for them
// once any exist, and stay out of the way while there are none.
func TestInterruptedReviewsReachTheTally(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	m := newModel(demoConfig())
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "sec-review",
		Agent: "claude", Status: runner.StatusInterrupted, Time: base})
	if got := stripANSI(m.gridTitle()); !strings.Contains(got, "interrupted 1") {
		t.Fatalf("tally %q omits the interrupted count", got)
	}
	if got := stripANSI(newModel(demoConfig()).gridTitle()); strings.Contains(got, "interrupted") {
		t.Fatalf("an empty tally still advertises interruptions: %q", got)
	}
}

// Scrolling back from the live edge is invisible otherwise: the title says
// how far.
func TestFeedTitleMarksScrolledBack(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.feedTitle()); got != "FEED" {
		t.Fatalf("live-edge title %q, want plain FEED", got)
	}
	m.feed = make([]feedLine, 40)
	m.scroll = 12
	if got := stripANSI(m.feedTitle()); !strings.Contains(got, "12 lines back") {
		t.Fatalf("scrolled-back title %q, want the distance from the live edge", got)
	}
}

// Pausing must hold, not drop: everything an agent prints while the feed is
// paused stays readable afterwards, and the viewport does not move until the
// reader asks it to.
func TestPauseHoldsInsteadOfDropping(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	send := func(text string) {
		m.apply(runner.Event{Kind: runner.EvOutput, Review: "sec-review",
			Agent: "claude", Text: text, Time: m.cfg.Started})
	}
	send("before the pause")
	m.paused = true
	send("held one")
	send("held two")
	if len(m.feed) != 3 {
		t.Fatalf("feed holds %d lines, want 3: output printed during a pause may not be dropped", len(m.feed))
	}
	if m.scroll != 2 {
		t.Fatalf("scroll %d, want 2 so the viewport holds still while paused", m.scroll)
	}
	if frozen := stripANSI(m.renderFeed(100, 10)); strings.Contains(frozen, "held two") {
		t.Fatalf("the view moved while paused:\n%s", frozen)
	}
	m.paused = false
	m.scroll = 0 // G: back to the live edge
	live := stripANSI(m.renderFeed(100, 10))
	for _, want := range []string{"before the pause", "held one", "held two"} {
		if !strings.Contains(live, want) {
			t.Fatalf("resumed feed lost %q:\n%s", want, live)
		}
	}
}

// A reader parked in history stays parked when the ring overflows and trims:
// skipping the anchor on a trimmed push lets the window slide toward the
// live edge instead of holding the lines it shows.
func TestScrollAnchorSurvivesRingTrim(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	send := func(i int) {
		m.apply(runner.Event{Kind: runner.EvOutput, Review: "sec-review",
			Agent: "claude", Text: fmt.Sprintf("line %04d", i), Time: m.cfg.Started})
	}
	for i := range feedMax + 10 {
		send(i)
	}
	m.scroll = 20 // j twenty times: parked twenty lines back
	parkedAt := m.feed[len(m.feed)-m.scroll-1].text
	for i := range 50 {
		send(feedMax + 10 + i) // every push trims once the ring is full
	}
	if got := m.feed[len(m.feed)-m.scroll-1].text; got != parkedAt {
		t.Fatalf("parked reader now looks at %q, want %q", got, parkedAt)
	}
}

// The feed mixes pre-normalized agent output with log lines carrying
// fragments of a possibly hostile repository (git stderr, merge output).
// Nothing may reach the screen able to drive or spoof the terminal, and
// sanitization must not eat visible text.
func TestFeedSanitizesUntrustedText(t *testing.T) {
	m := newModel(demoConfig())
	m.apply(runner.Event{Kind: runner.EvLog,
		Text: "merge failed\x1b[2J\x07 in \u202Eevil\u202C", Time: m.cfg.Started})
	m.apply(runner.Event{Kind: runner.EvOutput, Review: "sec-review", Agent: "claude",
		Text: "plain output survives \x1b[31muntouched\x1b[0m", Time: m.cfg.Started})
	if len(m.feed) != 2 {
		t.Fatalf("feed holds %d lines, want 2", len(m.feed))
	}
	for i, l := range m.feed {
		for _, r := range l.text {
			if r == '\x1b' || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
				t.Fatalf("line %d kept a control or formatting rune (%q): %q", i, r, l.text)
			}
		}
	}
	logLine, outLine := m.feed[0].text, m.feed[1].text
	for _, want := range []string{"merge failed[2J in evil", "plain output survives [31muntouched[0m"} {
		if !strings.Contains(logLine+outLine, want) {
			t.Fatalf("sanitization dropped visible text %q (log %q, output %q)",
				want, logLine, outLine)
		}
	}
}

// Review names are untrusted repository content and not always ASCII: a cut
// must never split a rune into mojibake. Width follows the shared helper's
// pinned contract: w cells kept, ellipsis included.
func TestTrimNeverSplitsARune(t *testing.T) {
	s := strings.Repeat("é", 20)
	got := trim(s, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("trim split a multibyte rune: %q", got)
	}
	if lipgloss.Width(got) > 11 {
		t.Fatalf("trim returned %d columns, want at most 11", lipgloss.Width(got))
	}
}

// Width is measured in terminal cells, not runes: two CJK glyphs occupy four
// columns, so a w-column budget holds half as many of them.
func TestTrimRespectsDisplayWidth(t *testing.T) {
	s := "認証テスト設定" // seven wide glyphs, fourteen cells
	if got := trim(s, 5); lipgloss.Width(got) != 5 {
		t.Fatalf("trim(%q, 5) = %q (%d columns), want exactly 5", s, got, lipgloss.Width(got))
	}
	// A combining mark belongs to its base: the cut must not orphan one onto
	// the ellipsis.
	accented := strings.Repeat("e\u0301", 12)
	if got := trim(accented, 4); !utf8.ValidString(got) || lipgloss.Width(got) != 4 {
		t.Fatalf("trim split a grapheme or miscounted: %q (%d columns)", got, lipgloss.Width(got))
	}
	// Exactly-fitting text comes back whole, no ellipsis.
	if got := trim("認証テスト", 10); got != "認証テスト" {
		t.Fatalf("trim cut a fitting string: %q", got)
	}
}

func TestChartDrawsGridWhenEmpty(t *testing.T) {
	// Absence of signal is information: the baseline must still be visible.
	got := chart(nil, 10, 2)
	if strings.TrimSpace(stripANSI(got)) == "" {
		t.Fatal("empty series rendered nothing")
	}
}

func TestMeterShowsUnlitRemainder(t *testing.T) {
	got := stripANSI(meter(0.5, 10, cGreen))
	if strings.Count(got, "▰") != 5 || strings.Count(got, "▱") != 5 {
		t.Fatalf("meter should show both halves: %q", got)
	}
	if got := stripANSI(meter(0, 10, cGreen)); strings.Count(got, "▱") != 10 {
		t.Fatalf("empty meter should still draw its track: %q", got)
	}
}

func TestClipKeepsVisibleWidth(t *testing.T) {
	styledText := lipgloss.NewStyle().Foreground(cGreen).Render("abcdefghij")
	if got := lipgloss.Width(clip(styledText, 4)); got != 4 {
		t.Fatalf("clip produced %d columns, want 4", got)
	}
	// Wide characters spend their real width: clipping ten CJK glyphs to
	// four columns must yield four columns, not five half-cut ones.
	styledWide := lipgloss.NewStyle().Foreground(cGreen).Render("認証認証認証認証認証")
	if got := clip(styledWide, 4); lipgloss.Width(got) != 4 {
		t.Fatalf("clip produced %d columns, want 4: %q", lipgloss.Width(got), got)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
