// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
		"sec", "claude", "2×lane", "1.0.0",
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

// The frame is exactly the pane: every terminal size and lane count must
// leave room for all four panels and the footer, or the last panel runs off
// the bottom of the screen and its border is left open.
func TestFrameFitsAtEverySize(t *testing.T) {
	for _, agents := range []int{1, 2, 5, 8, 12} {
		for _, h := range []int{18, 20, 22, 24, 26, 30, 40, 50} {
			cfg := demoConfig()
			cfg.Agents = nil
			for i := range agents {
				cfg.Agents = append(cfg.Agents, fmt.Sprintf("agent%02d", i))
			}
			frame := staticFrame(cfg, demoEvents(), 100, h)
			opened, closed := strings.Count(frame, "╭"), strings.Count(frame, "╰")
			if opened != closed {
				t.Fatalf("%d agents at %d rows: %d panels opened, %d closed, "+
					"the frame ran off the pane:\n%s", agents, h, opened, closed, stripANSI(frame))
			}
			// The full view is exactly the pane; the minimal fallback is
			// deliberately short.
			if h >= 22 {
				if lines := strings.Split(frame, "\n"); len(lines) != h {
					t.Fatalf("%d agents at %d rows: frame is %d rows, pane is %d",
						agents, h, len(lines), h)
				}
			}
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
// the keys: a view that hides how to quit is its own dead end, and one that
// hides how to finish gracefully forces the harsher one.
func TestMinimalViewKeepsStateTallyAndKeys(t *testing.T) {
	frame := stripANSI(staticFrame(demoConfig(), demoEvents(), 40, 10))
	for _, want := range []string{"RUNNING", "loop 1", "pass 1", "timeout 1", "? help", "s finish", "too small"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("minimal view lost %q:\n%s", want, frame)
		}
	}
}

// The fallback must not wrap: a narrow terminal renders what it cannot hold
// as a taller broken screen, and the fallback exists to be the smaller one.
func TestMinimalViewClipsToThePane(t *testing.T) {
	for _, w := range []int{20, 30, 40, 49} {
		m := newModel(demoConfig())
		m.w, m.h, m.ready = w, 10, true
		for _, ev := range demoEvents() {
			m.apply(ev)
		}
		m.haveLines = true
		m.ins, m.del = 1234567, 234567
		for i, ln := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(ln); got > w {
				t.Fatalf("w=%d: row %d is %d columns wide: %q", w, i, got, stripANSI(ln))
			}
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
//
// The count is checked against the lanes actually drawn rather than against a
// number worked out here: the marker takes one of the panel's rows, so an
// assertion that recomputes "total minus panel height" reproduces the same
// off-by-one the panel could have, and agrees with it.
func TestHiddenAgentsAreAnnouncedNotSilent(t *testing.T) {
	const total = 10
	cfg := demoConfig()
	cfg.Agents = nil
	for i := range total {
		cfg.Agents = append(cfg.Agents, fmt.Sprintf("agent%02d", i))
	}
	frame := stripANSI(staticFrame(cfg, nil, 120, 40))

	drawn := 0
	for i := range total {
		if strings.Contains(frame, fmt.Sprintf("agent%02d ", i)) {
			drawn++
		}
	}
	if drawn == 0 || drawn == total {
		t.Fatalf("want some lanes drawn and some hidden, drew %d of %d:\n%s", drawn, total, frame)
	}
	m := regexp.MustCompile(`\+(\d+) more agents`).FindStringSubmatch(frame)
	if m == nil {
		t.Fatalf("%d of %d lanes were dropped without announcing it:\n%s", total-drawn, total, frame)
	}
	announced, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if want := total - drawn; announced != want {
		t.Fatalf("announced %d hidden agents, but %d of %d are not on screen:\n%s",
			announced, want, total, frame)
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

// Unmerged branches used to live only in the help overlay. The feed title
// carries the count so a reader who never presses ? still sees that work
// was kept, and ? still lists the names.
func TestFeedTitleMarksUnmergedBranches(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.feedTitle()); strings.Contains(got, "unmerged") {
		t.Fatalf("a clean run advertised unmerged branches: %q", got)
	}
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if got := stripANSI(m.feedTitle()); !strings.Contains(got, "1 unmerged") {
		t.Fatalf("feed title %q, want the unmerged count", got)
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

// A long pause used to increment scroll for every arriving line even after
// the ring had trimmed, so the offset outran the retained feed.
func TestScrollStaysInsideTheFeedRing(t *testing.T) {
	m := newModel(demoConfig())
	m.paused = true
	for i := range feedMax * 2 {
		m.apply(runner.Event{Kind: runner.EvOutput, Review: "sec-review",
			Agent: "claude", Text: fmt.Sprintf("line %04d", i), Time: m.cfg.Started})
	}
	if len(m.feed) != feedMax {
		t.Fatalf("feed grew to %d, cap is %d", len(m.feed), feedMax)
	}
	if m.scroll > len(m.feed)-1 {
		t.Fatalf("scroll %d outran the %d-line ring", m.scroll, len(m.feed))
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

// dirLabel cuts from the left, keeping the tail that identifies the tree.
// The cut must land between grapheme clusters: a combining mark or an emoji
// ZWJ sequence dropped onto the ellipsis is the same class of split trim
// already refuses.
func TestDirLabelCutsBetweenGraphemes(t *testing.T) {
	nfd := strings.Repeat("x", 20) + "cafe\u0301" + strings.Repeat("y", 5)
	got := dirLabel(nfd, 8)
	if strings.Contains(got, "\u0301") && !strings.Contains(got, "e\u0301") {
		t.Fatalf("cut orphaned a combining mark onto the ellipsis: %q", got)
	}
	if w := lipgloss.Width(got); w > 8 {
		t.Fatalf("dirLabel is %d cells, want at most 8: %q", w, got)
	}
	family := "👨\u200d👩\u200d👧"
	long := strings.Repeat("a", 20) + family + "/src"
	got = dirLabel(long, 8)
	if rest, ok := strings.CutPrefix(got, "…"); ok && strings.HasPrefix(rest, "\u200d") {
		t.Fatalf("cut landed inside an emoji sequence: %q", got)
	}
	if w := lipgloss.Width(got); w > 8 {
		t.Fatalf("dirLabel is %d cells, want at most 8: %q", w, got)
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

// One hue per agent, and the vendor's own where there is one: a lane is
// identifiable before its name is read. Two models of one vendor must still
// be distinguishable, so the second takes the rotation.
func TestAgentHuesPreferTheVendorColor(t *testing.T) {
	h := newHueMap()
	if got := h.get("claude"); got != brandHues["claude"] {
		t.Fatalf("claude got %v, want the vendor color", got)
	}
	if got := h.get("codex:gpt-5"); got != brandHues["codex"] {
		t.Fatalf("codex:gpt-5 got %v, want codex's vendor color", got)
	}
	if got := h.get("claude:opus"); got == brandHues["claude"] {
		t.Fatal("a second model of one vendor must not reuse its lane color")
	}
	if got := h.get("opencode"); got == brandHues["claude"] || got == brandHues["codex"] {
		t.Fatalf("an agent with no vendor color took one: %v", got)
	}
	if first, again := h.get("claude"), h.get("claude"); first != again {
		t.Fatal("an agent's color must be stable across lookups")
	}
}

// The graceful quit is a request, not an exit: the screen says it is
// finishing and keeps running until the reviews in flight are done.
func TestDashboardFinishKeyAsksOnce(t *testing.T) {
	asked := 0
	cfg := demoConfig()
	cfg.OnFinish = func() { asked++ }
	m := newModel(cfg)
	m.w, m.h, m.ready = 100, 30, true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if asked != 1 {
		t.Fatalf("the finish request was made %d times, want once", asked)
	}
	if got := m.View(); !strings.Contains(got, "FINISHING") {
		t.Fatalf("the header does not say the run is finishing:\n%s", got)
	}
}

// Theme tokens are pinned to WCAG 2.2 AA on both background variants they
// ship with: any color that can sit behind text clears 4.5:1 (SC 1.4.3),
// including the status hues that color grid names and feed lines, and the
// instrument strokes clear the 3:1 non-text floor (SC 1.4.11). Borders are
// decorative chrome carrying no information, so they alone are exempt.
func TestThemeClearsWCAGContrastFloors(t *testing.T) {
	const darkBase, lightBase = "#1e1e2e", "#eff1f5"
	textTokens := map[string]lipgloss.AdaptiveColor{
		"text": cText, "dim": cDim, "faint": cFaint,
		"red": cRed, "green": cGreen, "yellow": cYellow, "peach": cPeach,
		"blue": cBlue, "cyan": cCyan, "teal": cTeal, "magenta": cMagenta,
		"pink": cPink, "lavender": cLavender, "mark": cMark,
	}
	// Agent lanes render their label in the vendor's color, so those clear the
	// same floor: a brand is never a reason to ship unreadable text.
	for tool, hue := range brandHues {
		textTokens["brand "+tool] = hue
	}
	for name, fg := range textTokens {
		if got := contrastRatio(t, fg.Dark, darkBase); got < 4.5 {
			t.Errorf("%s dark %q is %.2f:1 on the dark base, want at least 4.5", name, fg.Dark, got)
		}
		if got := contrastRatio(t, fg.Light, lightBase); got < 4.5 {
			t.Errorf("%s light %q is %.2f:1 on the light base, want at least 4.5", name, fg.Light, got)
		}
	}
	if got := contrastRatio(t, cTrack.Dark, darkBase); got < 3 {
		t.Errorf("track dark %q is %.2f:1 on the dark base, want at least 3", cTrack.Dark, got)
	}
	if got := contrastRatio(t, cTrack.Light, lightBase); got < 3 {
		t.Errorf("track light %q is %.2f:1 on the light base, want at least 3", cTrack.Light, got)
	}
}

// The wordmark is the path-arrow teal, one hue: the README logos are a
// single-color name next to that arrow, and a Catppuccin teal or a
// per-letter gradient would be a color the mark does not use.
func TestWordmarkIsTheBrandTeal(t *testing.T) {
	if cMark.Dark != "#0e96a8" {
		t.Fatalf("wordmark dark is %q, want the mark's #0e96a8", cMark.Dark)
	}
	want := lipgloss.NewStyle().Bold(true).Foreground(cMark).Render("GAUNTLET")
	if got := wordmark(); got != want {
		t.Fatalf("wordmark is not the mark teal:\n got %q\nwant %q", got, want)
	}
	if got := stripANSI(wordmark()); got != "GAUNTLET" {
		t.Fatalf("wordmark text = %q, want GAUNTLET", got)
	}
}

// Keys are chrome: live data is the bright thing, and magenta is reserved
// for diff hunk headers. The launcher already draws keys in the body color;
// the dashboard footer has to match or the two screens read as different
// products.
func TestFooterKeysAreChromeNotAccent(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	footer := lastLine(m.View())
	if strings.Contains(footer, styleMagic.Render("q")) {
		t.Fatal("footer keys used the accent reserved for diff metadata")
	}
	if !strings.Contains(footer, styleValue.Render("q")) {
		t.Fatal("footer keys are not body-colored chrome")
	}
}

// A budget is magnitude, so its meter rides the heat ramp. Lavender is the
// reasoning hue; using it here would make time-spent look like thinking.
func TestBudgetMeterUsesHeatNotReasoning(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.cfg.Budget = time.Hour
	m.now = m.cfg.Started.Add(30 * time.Minute)
	footer := lastLine(m.View())
	if strings.Contains(footer, meter(0.5, 10, cLavender)) {
		t.Fatal("budget meter used the reasoning hue")
	}
	if !strings.Contains(footer, meter(0.5, 10, heatColor(0.5))) {
		t.Fatal("budget meter is not on the heat ramp")
	}
}

func contrastRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	a, b := wcagLuminance(t, fg), wcagLuminance(t, bg)
	hi, lo := max(a, b), min(a, b)
	return (hi + 0.05) / (lo + 0.05)
}

func wcagLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("color %q is not #rrggbb", hex)
	}
	var lin [3]float64
	for i := range lin {
		v, err := strconv.ParseInt(hex[1+2*i:3+2*i], 16, 64)
		if err != nil {
			t.Fatalf("color %q has a bad channel: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			lin[i] = c / 12.92
		} else {
			lin[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*lin[0] + 0.7152*lin[1] + 0.0722*lin[2]
}

// The reasoning glyph is the dashboard's one animation: it starts on its own
// and turns for as long as an agent keeps reasoning. GAUNTLET_NO_ANIMATION
// freezes it to one held frame, so the actively-thinking state stays readable
// without motion; the resting glyphs are not motion and do not change.
func TestThinkGlyphFreezesUnderNoAnimation(t *testing.T) {
	now := time.Now()
	last := now.Add(-time.Second) // reasoning is actively growing

	t.Setenv("GAUNTLET_NO_ANIMATION", "1")
	frozen := thinkGlyph(now, last)
	if frozen != motionStill {
		t.Fatalf("frozen glyph %q, want the held frame %q", frozen, motionStill)
	}
	if thinkGlyph(now.Add(thinkingFrame), last) != frozen {
		t.Fatal("the glyph still moves under GAUNTLET_NO_ANIMATION")
	}

	t.Setenv("GAUNTLET_NO_ANIMATION", "")
	if thinkGlyph(now, last) == thinkGlyph(now.Add(thinkingFrame), last) {
		t.Fatal("the glyph does not turn without the variable")
	}

	// The freeze must not touch the states that are already still.
	if got := thinkGlyph(now, time.Time{}); got != "◌" {
		t.Fatalf("a never-thinking lane reads %q, want the resting glyph", got)
	}
	if got := thinkGlyph(now.Add(thinkingStill+time.Second), last); got != "◌" {
		t.Fatalf("a finished thought reads %q, want the resting glyph", got)
	}
}

func TestThinkGlyphPreEpochDoesNotPanic(t *testing.T) {
	// Go's % keeps the dividend's sign: UnixNano before 1970 is negative, so
	// the frame index used to be -1 and the slice lookup panicked.
	t.Setenv("GAUNTLET_NO_ANIMATION", "")
	now := time.Unix(-1, 0)
	got := thinkGlyph(now, now)
	switch got {
	case "◐", "◓", "◑", "◒":
	default:
		t.Fatalf("pre-epoch glyph %q, want a turning frame", got)
	}
}

// The footer clips from its right end once the counters are wide, so the
// keys that keep a reader oriented must outlast the ones only the data
// hungry need: quit, help, and pause survive; filter is the first to go.
func TestFooterKeepsHelpAndPauseWhileStatsGrow(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 61, 30, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	m.haveLines = true
	m.ins, m.del = 1234567, 234567 // every right-side segment at full width
	m.tokens, m.thinking = 98765432, 43210987
	m.liveRate = 8888.8
	footer := lastLine(stripANSI(m.View()))
	for _, want := range []string{"q:quit", "?:help", "space:pause", "j/k:scroll"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("a wide-stats footer lost %q:\n%s", want, footer)
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// The header's right side is orientation: the loop, the elapsed time, and
// the run state. A narrow terminal must lose dim chrome (version, run id,
// the tree) before it loses any of that; a wide one keeps all of it.
func TestHeaderKeepsTheRunStateAtNarrowWidths(t *testing.T) {
	m := newModel(demoConfig())
	m.now = m.cfg.Started.Add(2 * time.Minute)
	for _, w := range []int{60, 65, 70, 80} {
		m.w = w
		header := stripANSI(m.renderHeader())
		for _, want := range []string{"loop", "2m00s", "RUNNING"} {
			if !strings.Contains(header, want) {
				t.Fatalf("a %d-column header lost %q:\n%s", w, want, header)
			}
		}
	}
	m.w = 120
	if header := stripANSI(m.renderHeader()); !strings.Contains(header, "20260825T000000Z-abcd") {
		t.Fatalf("a wide header dropped the run id:\n%s", header)
	}
}

// The fallback advertises only keys whose effect it can show: scrolling acts
// on a feed it does not draw, so naming j/k there would be a dead key.
func TestMinimalViewAdvertisesOnlyKeysItCanShow(t *testing.T) {
	frame := stripANSI(staticFrame(demoConfig(), demoEvents(), 40, 10))
	if strings.Contains(frame, "scroll") {
		t.Fatalf("the minimal view advertises scroll with no feed to scroll:\n%s", frame)
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

// The feed filter narrows what is on screen without losing what was collected:
// widening it brings the dropped lines back.
func TestFeedFilterNarrowsWithoutDiscarding(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 40, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	all := len(m.visibleFeed())
	if all == 0 {
		t.Fatal("the demo events should have filled the feed")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	narrowed := m.visibleFeed()
	if len(narrowed) >= all {
		t.Fatalf("filter kept %d of %d lines, want fewer", len(narrowed), all)
	}
	for _, l := range narrowed {
		if l.kind == normalize.Plain || l.kind == normalize.Thinking {
			t.Fatalf("narration survived the filter: %+v", l)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if len(m.visibleFeed()) != all {
		t.Fatalf("widening left %d lines, want the original %d", len(m.visibleFeed()), all)
	}
}

// A retry reuses the lane and resets the clock, so the lane has to say which
// attempt is running or it reads as a review that restarted itself.
func TestLaneNamesTheRetry(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	m.apply(runner.Event{Kind: runner.EvReviewStart, Review: "sec-review",
		Agent: "claude", Attempt: 2, Time: m.cfg.Started})
	m.now = m.cfg.Started.Add(time.Second)
	if got := m.View(); !strings.Contains(got, "↻2") {
		t.Fatalf("the second attempt is not visible on the lane:\n%s", got)
	}
}

// The help overlay owns the screen while it is up: a key acting on the hidden
// dashboard would change state nobody can see, so it answers only its closing
// keys and every other key is inert until the view is back.
func TestHelpOverlayShieldsTheDashboard(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	key := func(s string) {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	}
	key("?")
	if !m.help {
		t.Fatal("? did not open help")
	}
	key(" ")
	key("f")
	key("k")
	if m.paused || m.filter != feedAll || m.scroll != 0 {
		t.Fatalf("a key acted behind the overlay: paused=%t filter=%d scroll=%d",
			m.paused, m.filter, m.scroll)
	}
	key("?")
	if m.help {
		t.Fatal("? did not close help")
	}
	key(" ") // with the view back, the same key acts again
	if !m.paused {
		t.Fatal("space stopped working after help closed")
	}
}

// While help is up, q closes the overlay. The overlay has to say so first:
// listing "q quits" as the first line is how a reader stops a run they opened
// help to understand.
func TestHelpOverlayLeadsWithHowToClose(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 80, 24, true
	m.help = true
	got := stripANSI(m.View())
	closeAt := strings.Index(got, "close this help")
	quitAt := strings.Index(got, "stop the run")
	if closeAt < 0 {
		t.Fatalf("help does not say how to close it:\n%s", got)
	}
	if quitAt >= 0 && quitAt < closeAt {
		t.Fatalf("help lists quit before close:\n%s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.help {
		t.Fatal("q on the overlay should close help, not stop the run")
	}
	if m.quitArmed {
		t.Fatal("closing help must not arm the hard stop")
	}
}

// The overlay is a full-screen view: it has to clip like every other one, or
// a small terminal that advertised "?" wraps into a taller broken screen.
func TestHelpOverlayFitsThePane(t *testing.T) {
	m := newModel(demoConfig())
	m.help, m.ready = true, true
	for _, size := range [][2]int{{40, 10}, {60, 12}, {80, 24}} {
		m.w, m.h = size[0], size[1]
		for i, ln := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(ln); got > size[0] {
				t.Fatalf("%dx%d: row %d is %d columns: %q",
					size[0], size[1], i, got, stripANSI(ln))
			}
		}
		if rows := strings.Split(m.View(), "\n"); len(rows) > size[1] {
			t.Fatalf("%dx%d: help is %d rows", size[0], size[1], len(rows))
		}
	}
}

// q while reviews are running is a hard stop. One press arms it; a second
// press closes. Any other key cancels the arming, so an accidental tap does
// not kill the run.
func TestQuitNeedsASecondPressWhileRunning(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Fatal("the first q stopped the run")
	}
	if !m.quitArmed {
		t.Fatal("the first q should ask for confirmation")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "q TO STOP") {
		t.Fatalf("the header does not say q will stop the run:\n%s", got)
	}
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "q:stop now") {
		t.Fatalf("the footer still advertises a plain quit:\n%s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if m.quitArmed {
		t.Fatal("a different key should cancel the armed stop")
	}
	if !m.paused {
		t.Fatal("space should still pause after cancelling the stop")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("the second q should stop the run")
	}
}

// Once the run has finished, q closes the screen on the first press: there is
// nothing left to kill, and a confirm would strand the reader on a dead view.
func TestQuitClosesImmediatelyWhenDone(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.done = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q on a finished run should close the dashboard")
	}
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "q:close") {
		t.Fatalf("a finished run still advertises quit:\n%s", lastLine(stripANSI(m.View())))
	}
	if strings.Contains(lastLine(stripANSI(m.View())), "s:finish") {
		t.Fatalf("a finished run still advertises finish:\n%s", lastLine(stripANSI(m.View())))
	}
}

// The footer names the action space will take now, not the one it already took.
func TestFooterSaysResumeWhenPaused(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.paused = true
	footer := lastLine(stripANSI(m.View()))
	if !strings.Contains(footer, "space:resume") {
		t.Fatalf("a paused feed still says pause:\n%s", footer)
	}
	if strings.Contains(footer, "space:pause") {
		t.Fatalf("a paused feed still advertises pause:\n%s", footer)
	}
}

// f is the same kind of toggle: the footer says what the next press does.
func TestFooterSaysWidenWhenFiltered(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.filter = feedSignal
	footer := lastLine(stripANSI(m.View()))
	if !strings.Contains(footer, "f:widen") {
		t.Fatalf("a narrowed feed still says filter:\n%s", footer)
	}
	if strings.Contains(footer, "f:filter") {
		t.Fatalf("a narrowed feed still advertises filter:\n%s", footer)
	}
}

// Lanes and the feed spell a review the way the grid already did: without the
// -review suffix that is the same on every name.
func TestDashboardSpellsReviewNamesWithoutTheSuffix(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	m.apply(runner.Event{Kind: runner.EvReviewStart, Review: "sec-review",
		Agent: "claude", Time: m.cfg.Started})
	m.apply(runner.Event{Kind: runner.EvOutput, Review: "sec-review", Agent: "claude",
		Text: "looking at auth", LineKind: normalize.Plain})
	got := stripANSI(m.View())
	if !strings.Contains(got, "sec") {
		t.Fatal("short review name missing from the frame")
	}
	if strings.Contains(got, "sec-review") {
		t.Fatalf("the -review suffix leaked onto a lane or feed prefix:\n%s", got)
	}
}

// home / end are the same jumps as g / G, for keyboards that have them.
func TestFeedHomeAndEndMatchG(t *testing.T) {
	m := newModel(demoConfig())
	m.feed = make([]feedLine, 40)
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if m.scroll != 39 {
		t.Fatalf("home left scroll %d, want the oldest line", m.scroll)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if m.scroll != 0 {
		t.Fatalf("end left scroll %d, want the live edge", m.scroll)
	}
}

// A merge conflict is the line a reader narrowed the feed to see. Runner
// narration used to land as plain text, so the "results and errors" filter
// hid it.
func TestMergeConflictSurvivesTheFeedFilter(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 40, true
	m.apply(runner.Event{Kind: runner.EvLog,
		Text: "MERGE CONFLICT: sec-review kept on branch gauntlet/x/sec-review (CONFLICT in main.go)"})
	m.apply(runner.Event{Kind: runner.EvLog,
		Text: "To land it after resolving: git merge gauntlet/x/sec-review"})
	m.apply(runner.Event{Kind: runner.EvLog, Text: "Running code-review with claude"})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	got := stripANSI(m.renderFeed(100, 10))
	for _, want := range []string{"MERGE CONFLICT", "To land it after resolving"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the signal filter hid %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Running code-review") {
		t.Fatalf("narration survived the filter:\n%s", got)
	}
}

// The small-terminal fallback is "did anything break": a conflict count
// without the branch name leaves the reader no next action.
func TestMinimalViewNamesUnmergedBranches(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 40, 10, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	got := stripANSI(m.renderMinimal())
	if !strings.Contains(got, "unmerged:") || !strings.Contains(got, "sec-review") {
		t.Fatalf("the fallback lost the unmerged branch:\n%s", got)
	}
}

// --no-color must reach the TUI: lipgloss applies NO_COLOR on its own, but it
// cannot see a command-line flag, so run hands the request over through
// SetMonochrome before the launcher or dashboard draws. Deterministic by
// forcing a color profile first: a bare terminal-less test run would already
// be monochrome and prove nothing.
func TestSetMonochromeStripsStyle(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

	style := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	if s := style.Render("x"); !strings.Contains(s, "\x1b[") {
		t.Fatalf("precondition failed: colored profile rendered %q", s)
	}

	SetMonochrome()
	if s := style.Render("x"); strings.Contains(s, "\x1b[") {
		t.Fatalf("SetMonochrome left escape codes in place: %q", s)
	}
}
