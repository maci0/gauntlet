// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"math"
	"regexp"
	"slices"
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
		// Every run the CLI starts has a graceful quit to ask for; the one
		// that does not is what the finish key is hidden from.
		OnFinish: func() {},
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

// TestDashboardReadsTheInjectedClock pins the one wall-clock read the
// dashboard used to make for itself: the model's first reading and the
// end-of-run stamp. Both now come from Config.Now, which is the run's clock,
// so two runs of the same seed under the same clock draw the same frames.
func TestDashboardReadsTheInjectedClock(t *testing.T) {
	stamp := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return stamp }
	cfg := demoConfig()
	cfg.Started = stamp.Add(-time.Minute)
	cfg.Now = now

	m := newModel(cfg)
	if !m.now.Equal(stamp) {
		t.Fatalf("model clock %v, want the injected %v", m.now, stamp)
	}
	if !m.lastSample.Equal(stamp) {
		t.Fatalf("activity baseline %v, want the injected %v", m.lastSample, stamp)
	}
	m.w, m.h, m.ready = 120, 40, true
	m.Update(doneMsg{})
	if !m.now.Equal(stamp) {
		t.Fatalf("completion stamped %v, want the injected %v", m.now, stamp)
	}
	// A config with no clock still reads wall time rather than the zero instant.
	if got := newModel(demoConfig()).now; got.IsZero() {
		t.Fatal("an unconfigured dashboard started on the zero instant")
	}
}

func TestCompletedDashboardFreezesClock(t *testing.T) {
	cfg := demoConfig()
	cfg.Started = time.Now().Add(-10 * time.Minute)
	m := newModel(cfg)
	m.w, m.h, m.ready = 120, 40, true
	m.Update(tickMsg(time.Now()))
	before := time.Now()
	m.Update(doneMsg{})
	after := time.Now()
	finished := m.now
	view := m.View()
	activityLen := len(m.activity)

	_, cmd := m.Update(tickMsg(finished.Add(time.Hour)))
	if !m.now.Equal(finished) {
		t.Fatalf("completed clock advanced from %v to %v", finished, m.now)
	}
	if cmd != nil {
		t.Fatal("completed dashboard scheduled another tick")
	}
	if got := m.View(); got != view {
		t.Fatalf("completed view changed after a later tick:\n%s", got)
	}
	if len(m.activity) != activityLen {
		t.Fatal("completed dashboard added an idle activity sample")
	}
	if finished.Before(before) || finished.After(after) {
		t.Fatalf("completion time %v outside [%v, %v]", finished, before, after)
	}
	m.Update(doneMsg{})
	if !m.now.Equal(finished) {
		t.Fatal("repeated completion changed the clock")
	}
}

// The live rate is a sum of per-lane rates, so the same event sequence used
// to land on a different float depending on the order the lanes came back in.
// Lane order fixes that, and the check is on the bits: an equal-looking rate
// that summed in a different order is exactly the regression, and a rounded
// comparison would not see it.
func TestUsageRateReplay(t *testing.T) {
	for _, configured := range [][]string{
		nil,
		{"first", "second", "third"},
		{"first", "second", "first", "third"},
	} {
		cfg := demoConfig()
		cfg.Agents = configured
		m := newModel(cfg)
		for i, label := range []string{"first", "second", "third"} {
			m.Update(eventMsg{Kind: runner.EvReviewStart, Review: label, Agent: label, Time: cfg.Started})
			m.Update(eventMsg{Kind: runner.EvUsage, Agent: label, Tokens: i + 1, Time: cfg.Started.Add(10 * time.Second)})
		}
		if got, want := math.Float64bits(m.liveRate), uint64(0x3fe3333333333334); got != want {
			t.Fatalf("agents %v: rate bits = %#x, want %#x", configured, got, want)
		}
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

// The lane meter shows how far through the review timeout a lane is, and the
// same timeout is what the grid's timeout glyph and the tally count. The limit
// itself has to be stated in text somewhere, or a lane a third full is a
// reading nobody can place (SC 1.1.1). A run with no timeout draws no meter
// and names no limit.
func TestLanesTitleStatesTheTimeout(t *testing.T) {
	cfg := demoConfig()
	if got := stripANSI(newModel(cfg).lanesTitle(100)); !strings.Contains(got, "timeout 30m") {
		t.Fatalf("the lanes panel does not state the limit its meters are drawn against: %q", got)
	}
	cfg.Timeout = 0
	if got := stripANSI(newModel(cfg).lanesTitle(100)); strings.Contains(got, "timeout") {
		t.Fatalf("a run with no timeout names a limit nothing is measured against: %q", got)
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
			opened, closed := strings.Count(frame, "┌"), strings.Count(frame, "└")
			if opened != closed {
				t.Fatalf("%d agents at %d rows: %d panels opened, %d closed, "+
					"the frame ran off the pane:\n%s", agents, h, opened, closed, stripANSI(frame))
			}
			// The full view is exactly the pane; the minimal fallback is
			// deliberately short. Either way there is something to show, and
			// no row may outrun the pane: at the fallback heights nothing else
			// here would catch a row wider than 100 columns, because the
			// fallback draws no panel for opened/closed to count.
			lines := strings.Split(frame, "\n")
			for i, ln := range lines {
				if got := lipgloss.Width(ln); got > 100 {
					t.Fatalf("%d agents at %d rows: row %d is %d columns wide, pane is 100: %q",
						agents, h, i, got, ln)
				}
			}
			if h >= minDashboardH {
				if len(lines) != h {
					t.Fatalf("%d agents at %d rows: frame is %d rows, pane is %d",
						agents, h, len(lines), h)
				}
			} else if strings.TrimSpace(stripANSI(frame)) == "" {
				t.Fatalf("%d agents at %d rows: below the full-view floor the "+
					"fallback must still draw, got an empty frame", agents, h)
			}
		}
	}
}

func TestSmallTerminalFallsBackInsteadOfBreaking(t *testing.T) {
	frame := staticFrame(demoConfig(), demoEvents(), 40, 10)
	if !strings.Contains(frame, "needs") {
		t.Fatalf("expected the minimal view, got:\n%s", frame)
	}
}

// The fallback must keep what answers "is it done, did anything break", plus
// the keys: a view that hides how to quit is its own dead end, and one that
// hides how to finish gracefully forces the harsher one.
func TestMinimalViewKeepsStateTallyAndKeys(t *testing.T) {
	frame := stripANSI(staticFrame(demoConfig(), demoEvents(), 40, 10))
	for _, want := range []string{"RUNNING", "loop 1", "pass 1", "timeout 1", "? help", "s finish", "needs"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("minimal view lost %q:\n%s", want, frame)
		}
	}
}

// The fallback is a screen the reader arrived at by having too little room,
// so it has to say what to do about it. Resizing is the next action and it is
// immediate, but only if the reader is told the size the panels need: a line
// reading only that the terminal is too small is a dead end with no next
// step, and the number is the same one the view guards on.
func TestMinimalViewSaysHowToGetThePanelsBack(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 40, 10, true
	got := stripANSI(m.renderMinimal())
	want := fmt.Sprintf("dashboard needs %d×%d", minDashboardW, minDashboardH)
	if !strings.Contains(got, want) || !strings.Contains(got, "resize") {
		t.Fatalf("the fallback does not name the size that brings the panels back:\n%s", got)
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

var (
	moreReviewsRe = regexp.MustCompile(`\+(\d+) more`)
	moreAgentsRe  = regexp.MustCompile(`\+(\d+) more agents`)
)

// titleRoom is the row width a panel title is asserted against when the test
// is about what the title says rather than how it fits.
const titleRoom = 200

// A grid that cannot hold every review says how many it dropped; a fitting
// grid stays clean.
func TestHiddenReviewsAreAnnouncedNotSilent(t *testing.T) {
	cfg := demoConfig()
	cfg.Reviews = nil
	for i := range 120 {
		cfg.Reviews = append(cfg.Reviews, fmt.Sprintf("r%03d-review", i))
	}
	frame := stripANSI(staticFrame(cfg, nil, 120, 30))
	m := moreReviewsRe.FindStringSubmatch(frame)
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
	if fit := stripANSI(staticFrame(demoConfig(), demoEvents(), 120, 40)); moreReviewsRe.MatchString(fit) {
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
	m := moreAgentsRe.FindStringSubmatch(frame)
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

// Reasoning is one decision, not two. The feed line and the lane's share
// counter are the same token, lavender and italic, so the agent thinking
// looks the same wherever it is read; the drift pinned here is a style rebuilt
// inline at one of the two call sites. The review tally is the same rule: a
// label colored by hand is a label the token set no longer governs.
func TestReasoningAndTallyLabelsComeFromTheTokenSet(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

	think := sgrPrefix(styleThink, "x")
	if !strings.Contains(think, ";3") {
		t.Fatalf("the thinking token no longer marks reasoning italic: %q", think)
	}
	if styleConflict.GetForeground() != lipgloss.TerminalColor(cPeach) {
		t.Fatal("the conflict label drifted off the peach it is documented as")
	}

	frame := staticFrame(demoConfig(), demoEvents(), 130, 40)
	if !strings.Contains(frame, styleThink.Render("the caller already validates this")) {
		t.Fatalf("the feed draws its reasoning line outside the thinking token:\n%q", frame)
	}
	m := newModel(demoConfig())
	m.now = m.cfg.Started.Add(90 * time.Second)
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if !strings.Contains(m.renderLanes(116, 8), think) {
		t.Fatalf("the lane reasoning share is drawn outside the thinking token:\n%q", m.renderLanes(116, 8))
	}
	if !strings.Contains(m.gridTitle(120), styleConflict.Render("conflict")) {
		t.Fatalf("the review tally draws its conflict label outside the token:\n%q", m.gridTitle(120))
	}
}

// sgrPrefix is the escape run a style puts in front of probe, which is what
// a rendered frame is matched on: the styled text itself is also there in a
// hand-built style, and only the sequence says which token drew it.
func sgrPrefix(s lipgloss.Style, probe string) string {
	rendered := s.Render(probe)
	before, _, ok := strings.Cut(rendered, probe)
	if !ok {
		return rendered
	}
	return before
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

// A review that reports no usable duration must not keep the duration its
// previous run of the same name recorded: agentTime would count that span a
// second time and the footer's average tok/s would divide by a time the run
// never took.
func TestReviewEndWithoutDurationDoesNotKeepTheLast(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	m := newModel(demoConfig())
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "sec-review",
		Agent: "claude", Status: runner.StatusOK, Elapsed: 92, Time: base})
	if m.agentTime != 92*time.Second {
		t.Fatalf("agent time = %v, want 92s", m.agentTime)
	}
	for _, elapsed := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "sec-review",
			Agent: "claude", Status: runner.StatusOK, Elapsed: elapsed, Time: base})
		if m.agentTime != 92*time.Second {
			t.Fatalf("elapsed %v: agent time = %v, want the 92s already recorded", elapsed, m.agentTime)
		}
	}
}

// Interrupted reviews keep their ␘ cells, so the tally must account for them
// once any exist, and stay out of the way while there are none.
func TestInterruptedReviewsReachTheTally(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	m := newModel(demoConfig())
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "sec-review",
		Agent: "claude", Status: runner.StatusInterrupted, Time: base})
	if got := stripANSI(m.gridTitle(titleRoom)); !strings.Contains(got, "interrupted 1") {
		t.Fatalf("tally %q omits the interrupted count", got)
	}
	if m.lanes["claude"].failed != 0 {
		t.Fatalf("interrupted review incremented lane failures: %d", m.lanes["claude"].failed)
	}
	if got := stripANSI(newModel(demoConfig()).gridTitle(titleRoom)); strings.Contains(got, "interrupted") {
		t.Fatalf("an empty tally still advertises interruptions: %q", got)
	}
}

// A status this build does not name reaches the dashboard through a run
// continued across a hot reload. The tally is keyed by the raw string, so the
// review is counted, and every cell that renders has to show it: a row that
// drops it reports a run shorter than the one that happened.
func TestUnrecognizedStatusReachesTheTally(t *testing.T) {
	base := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 60, 20, true
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "sec-review",
		Agent: "claude", Status: runner.Status("deferred"), Time: base})
	if got := stripANSI(m.gridTitle(titleRoom)); !strings.Contains(got, "other 1") {
		t.Fatalf("tally %q omits the unrecognized outcome", got)
	}
	if got := stripANSI(m.renderMinimal()); !strings.Contains(got, "other 1") {
		t.Fatalf("minimal tally %q omits the unrecognized outcome", got)
	}
	// A review_end with no status is publication metadata recovered without
	// an agent, not an outcome, so it stays out of the tally.
	m.apply(runner.Event{Kind: runner.EvReviewEnd, Review: "metadata-only",
		Agent: "claude", Time: base})
	if got := stripANSI(m.gridTitle(titleRoom)); !strings.Contains(got, "other 1") || strings.Contains(got, "other 2") {
		t.Fatalf("tally %q counts an empty status as an outcome", got)
	}
	if got := stripANSI(newModel(demoConfig()).gridTitle(titleRoom)); strings.Contains(got, "other") {
		t.Fatalf("an empty tally still advertises other outcomes: %q", got)
	}
}

// Scrolling back from the live edge is invisible otherwise: the title says
// how far.
func TestFeedTitleMarksScrolledBack(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.feedTitle(titleRoom)); got != "FEED" {
		t.Fatalf("live-edge title %q, want plain FEED", got)
	}
	m.feed = make([]feedLine, 40)
	m.scroll = 12
	if got := stripANSI(m.feedTitle(titleRoom)); !strings.Contains(got, "12 lines back") {
		t.Fatalf("scrolled-back title %q, want the distance from the live edge", got)
	}
}

// Unmerged branches used to live only in the help overlay. The feed title
// carries the count so a reader who never presses ? still sees that work
// was kept, and it names the key that lists them, so a branch left for a
// human to merge is findable from the screen that reports it.
func TestFeedTitleMarksUnmergedBranches(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.feedTitle(titleRoom)); strings.Contains(got, "unmerged") {
		t.Fatalf("a clean run advertised unmerged branches: %q", got)
	}
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	got := stripANSI(m.feedTitle(titleRoom))
	if !strings.Contains(got, "1 unmerged") {
		t.Fatalf("feed title %q, want the unmerged count", got)
	}
	if !strings.Contains(got, "? lists them") {
		t.Fatalf("feed title %q, want the key that lists the branch names", got)
	}
	// The pointer rides in the same segment as the count, so a title too
	// narrow for it drops the whole reading rather than half of one.
	if narrow := stripANSI(m.feedTitle(20)); strings.Contains(narrow, "lists them") &&
		!strings.Contains(narrow, "unmerged") {
		t.Fatalf("a narrow feed title kept the pointer and lost the count: %q", narrow)
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
	if got := trim("abc", 1); got != "…" {
		t.Fatalf("trim(\"abc\", 1) = %q, want …", got)
	}
	if got := trim("abc", 0); got != "" {
		t.Fatalf("trim(\"abc\", 0) = %q, want empty", got)
	}
	if got := trim("abc", -1); got != "" {
		t.Fatalf("trim(\"abc\", -1) = %q, want empty", got)
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
	if got := dirLabel("/path/to/repo", 1); got != "…" {
		t.Fatalf("dirLabel(..., 1) = %q, want …", got)
	}
	if got := dirLabel("/path/to/repo", 0); got != "" {
		t.Fatalf("dirLabel(..., 0) = %q, want empty", got)
	}
}

func TestChartDrawsGridWhenEmpty(t *testing.T) {
	// Absence of signal is information: the baseline must still be visible.
	got := chart(nil, 10, 2)
	if strings.TrimSpace(stripANSI(got)) == "" {
		t.Fatal("empty series rendered nothing")
	}
	if got := chart(nil, 0, 0); got != "" {
		t.Fatalf("chart(0, 0) = %q, want empty", got)
	}
	if cols, peak := tailCols([]float64{1, 2, 3}, 0); cols != nil || peak != 1 {
		t.Fatalf("tailCols(..., 0) = %v, %v, want nil, 1", cols, peak)
	}
	if cols, peak := tailCols([]float64{1, 2, 3}, -5); cols != nil || peak != 1 {
		t.Fatalf("tailCols(..., -5) = %v, %v, want nil, 1", cols, peak)
	}
}

// The chart's glyph table is indexed by ramp step, and every other call site
// names the ramp by color, so both have to answer the same question the same
// way. A step that drifted would draw a magnitude in a color that means
// something else.
func TestHeatIndexNamesTheRampStep(t *testing.T) {
	for _, f := range []float64{
		math.NaN(), 0, 0.02, 0.021, 0.1, 0.249, 0.25, 0.4, 0.5,
		0.6, 0.72, 0.8, 0.88, 0.9, 1,
	} {
		step := heatIndex(f)
		if step < 0 || step >= len(heatSteps) {
			t.Fatalf("heatIndex(%v) = %d, outside the ramp", f, step)
		}
		if got, want := heatSteps[step], heatColor(f); got != want {
			t.Fatalf("heatIndex(%v) = %v, want %v", f, got, want)
		}
	}
	// The unlit baseline is stroked in the track, which is the ramp's cold
	// end, so the table's extra row has to be that same step.
	if track := chartGlyphTable()[len(heatSteps)]; track[brailleSuffix[1]] != chartGlyphTable()[0][brailleSuffix[1]] {
		t.Fatal("the unlit baseline is not drawn in the ramp's cold end")
	}
}

// The table is rendered once and reused for the process, so what a cell writes
// has to be what asking lipgloss for the same glyph in the same color
// produces. A drift is a dashboard whose charts changed color without
// anything about the run changing.
func TestChartGlyphsMatchARenderedGlyph(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() {
		r.SetColorProfile(prev)
		chartGlyphs.Store(nil)
	})
	r.SetColorProfile(termenv.ANSI256)
	chartGlyphs.Store(nil)

	table := chartGlyphTable()
	for i, c := range heatSteps {
		for _, pattern := range brailleSuffix {
			glyph := string(rune(0x2800 + pattern))
			if got, want := table[i][pattern], styled(c, glyph); got != want {
				t.Fatalf("chart glyph for step %d is %q, want %q", i, got, want)
			}
		}
	}
}

// --no-color has to reach the glyphs too, and they are rendered once: a table
// built before the flag would leave the charts the one thing still colored.
func TestSetMonochromeRebuildsChartGlyphs(t *testing.T) {
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() {
		r.SetColorProfile(prev)
		chartGlyphs.Store(nil)
	})
	r.SetColorProfile(termenv.TrueColor)
	chartGlyphs.Store(nil)

	chartGlyphTable() // the table a frame would have drawn with
	SetMonochrome()

	for step, row := range chartGlyphTable() {
		for pattern, glyph := range row {
			if strings.Contains(glyph, "\x1b[") {
				t.Fatalf("step %d, pattern %#x is still styled after --no-color: %q",
					step, pattern, glyph)
			}
		}
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
	if got := stripANSI(meter(math.NaN(), 10, cGreen)); strings.Count(got, "▱") != 10 {
		t.Fatalf("NaN meter should render as empty track: %q", got)
	}
	if got := stripANSI(meter(-1, 10, cGreen)); strings.Count(got, "▱") != 10 {
		t.Fatalf("negative meter should render as empty track: %q", got)
	}
	if got := stripANSI(meter(2, 10, cGreen)); strings.Count(got, "▰") != 10 {
		t.Fatalf("overflow meter should cap at full track: %q", got)
	}
}

func TestClamp01HandlesNaNAndBounds(t *testing.T) {
	if got := clamp01(math.NaN()); got != 0 {
		t.Fatalf("clamp01(NaN) = %v, want 0", got)
	}
	if got := clamp01(-0.5); got != 0 {
		t.Fatalf("clamp01(-0.5) = %v, want 0", got)
	}
	if got := clamp01(1.5); got != 1 {
		t.Fatalf("clamp01(1.5) = %v, want 1", got)
	}
	if got := clamp01(0.75); got != 0.75 {
		t.Fatalf("clamp01(0.75) = %v, want 0.75", got)
	}
}

func TestFmtRateSpecialValues(t *testing.T) {
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := fmtRate(v); got != "n/a" {
			t.Fatalf("fmtRate(%v) = %q, want n/a", v, got)
		}
	}
	if got := fmtRate(50); got != "50.0" {
		t.Fatalf("fmtRate(50) = %q, want 50.0", got)
	}
	if got := fmtRate(250); got != "250" {
		t.Fatalf("fmtRate(250) = %q, want 250", got)
	}
	if got := fmtRate(1500); got != "1.5k" {
		t.Fatalf("fmtRate(1500) = %q, want 1.5k", got)
	}
}

func TestTailColsIgnoresNaN(t *testing.T) {
	cols, peak := tailCols([]float64{10, math.NaN(), 20}, 3)
	if peak != 20 {
		t.Fatalf("tailCols peak with NaN = %v, want 20", peak)
	}
	if len(cols) != 3 {
		t.Fatalf("tailCols len = %d, want 3", len(cols))
	}
}

// A lane that has been sampling longer than the chart is wide keeps the
// newest samples: the rightmost column is the latest reading, so keeping the
// oldest w would redraw every lane as a stale window. The peak is measured
// over the window the chart draws, not over the samples that fell out of it.
func TestTailColsKeepsTheNewestSamples(t *testing.T) {
	cols, peak := tailCols([]float64{1, 2, 3, 4, 5}, 3)
	if len(cols) != 3 {
		t.Fatalf("tailCols len = %d, want 3", len(cols))
	}
	if want := []float64{3, 4, 5}; !slices.Equal(cols, want) {
		t.Fatalf("tailCols = %v, want the newest %v", cols, want)
	}
	if peak != 5 {
		t.Fatalf("tailCols peak = %v, want 5", peak)
	}
	// A spike that has scrolled out of the window does not scale the bars.
	if _, peak := tailCols([]float64{99, 1, 1}, 2); peak != 1 {
		t.Fatalf("tailCols peak = %v, want 1: a sample outside the window scaled the chart", peak)
	}
}

// Every status the runner reports has a cell of its own. A failure drawing the
// pending dot, or a conflict drawing a check, is the reading the grid exists
// to carry, and the glyph is the only thing on screen that says which.
func TestStatusGlyphIsDistinctForEveryStatus(t *testing.T) {
	cases := []struct {
		status runner.Status
		glyph  string
	}{
		{statusPending, "·"},
		{statusRunning, "▸"},
		{runner.StatusOK, "✓"},
		{runner.StatusFail, "✗"},
		{runner.StatusTimeout, "⧖"},
		{runner.StatusConflict, "⑂"},
		{runner.StatusSkipped, "–"},
		{runner.StatusInterrupted, "␘"},
	}
	seen := map[string]runner.Status{}
	for _, c := range cases {
		got, _ := statusGlyph(c.status)
		if got != c.glyph {
			t.Errorf("statusGlyph(%q) = %q, want %q", c.status, got, c.glyph)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("statusGlyph(%q) and statusGlyph(%q) share the glyph %q", c.status, other, got)
		}
		seen[got] = c.status
	}
}

func TestHeatColorNaN(t *testing.T) {
	if got := heatColor(math.NaN()); got != cTrack {
		t.Fatalf("heatColor(NaN) = %v, want cTrack (%v)", got, cTrack)
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
	// Non-letter CSI terminators (@ through ~) must not swallow subsequent text.
	withCSI := "\x1b[3~abcdef"
	if got := clip(withCSI, 4); lipgloss.Width(got) != 4 {
		t.Fatalf("clip with CSI escape produced %d columns, want 4: %q", lipgloss.Width(got), got)
	}
	// A styled line cut open has to be closed, and a plain one must not gain a
	// reset it never turned a style on for.
	if got := clip("\x1b[31mabcdefghij\x1b[0m", 4); !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("clip left a styled run open: %q", got)
	}
	if got := clip("abcdefghij", 4); strings.Contains(got, "\x1b") {
		t.Errorf("clip appended an escape to plain text: %q", got)
	}
}

func TestPadBlockWideCharAlignment(t *testing.T) {
	// A 4-character CJK string is 8 columns. Clipping to 5 columns keeps 2 glyphs (4 columns).
	// padBlock must pad the remaining 1 column with a space so the line width is exactly 5.
	got := padBlock("認証認証", 5, 1)
	if w := lipgloss.Width(got); w != 5 {
		t.Fatalf("padBlock produced %d columns, want 5: %q", w, got)
	}
}

func TestPadBlockStripsCarriageReturns(t *testing.T) {
	got := padBlock("hello\r\nworld\r", 10, 2)
	if strings.Contains(got, "\r") {
		t.Fatalf("padBlock result %q contains carriage return", got)
	}
	want := "hello     \nworld     "
	if got != want {
		t.Fatalf("padBlock = %q, want %q", got, want)
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

// A run with nothing to finish into is not offered the key. s would do
// nothing, so the legend and the help leave it out. ctrl+c still stops the
// run, so the help names it as the stop it is.
// ctrl+c closes the overlay rather than stopping the run, so the overlay has
// to name it among the closing keys: read on its own, the entry below it
// ("ctrl+c stops the run now") promises a key that only closes the help.
func TestHelpOverlayNamesCtrlCAsACloseKey(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.help, m.ready = 100, 30, true, true
	help := stripANSI(m.View())
	head, _, ok := strings.Cut(help, "close this help")
	if !ok {
		t.Fatalf("the overlay does not say how to close it:\n%s", help)
	}
	rows := strings.Split(head, "\n")
	line := strings.TrimSpace(rows[len(rows)-1])
	if !strings.Contains(line, "ctrl+c") || !strings.HasPrefix(line, "q") {
		t.Fatalf("the close line does not lead with q and ctrl+c: %q", line)
	}
	before, _, _ := strings.Cut(help, "close this help")
	if strings.Contains(before, "stop the run") {
		t.Fatalf("the close line is not the first thing the overlay says:\n%s", help)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.help {
		t.Fatal("ctrl+c did not close the help")
	}
	if m.quitArmed || m.finishing {
		t.Fatal("ctrl+c on the overlay changed the run's state")
	}
}

func TestDashboardHidesFinishWhereThereIsNothingToFinish(t *testing.T) {
	cfg := demoConfig()
	cfg.OnFinish = nil
	m := newModel(cfg)
	m.w, m.h, m.ready = 100, 30, true
	for _, got := range []string{lastLine(stripANSI(m.View())), stripANSI(m.renderHelp())} {
		if strings.Contains(got, "finish") {
			t.Fatalf("a run with no finish is offered one:\n%s", got)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.finishing {
		t.Fatal("s started a finish that has nowhere to go")
	}
}

// The help is a keyboard user's only list of what the keys do, so every entry
// has to be the key's actual behavior. ctrl+c is bound in every state, and
// it means three different things across them: the graceful quit while the
// run is live, a hard stop while a finish is draining, and a hard stop when
// there was nothing to finish into. Each state has to say so, and none may
// claim a press count the handler does not require.
func TestDashboardHelpNamesCtrlCInEveryState(t *testing.T) {
	helpFor := func(m *model) string {
		t.Helper()
		m.w, m.h, m.ready = 100, 30, true
		return stripANSI(m.renderHelp())
	}
	live := newModel(demoConfig())
	live.finishing = true
	for _, tc := range []struct {
		name string
		cfg  Config
		arm  func(*model)
		want string
	}{
		{"no finish to ask for", func() Config { c := demoConfig(); c.OnFinish = nil; return c }(),
			nil, "stop the run now"},
		{"finish available", demoConfig(), nil, "finish:"},
		{"finish draining", demoConfig(), func(m *model) { m.finishing = true },
			"quit now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(tc.cfg)
			if tc.arm != nil {
				tc.arm(m)
			}
			help := helpFor(m)
			if !strings.Contains(help, "ctrl+c") {
				t.Fatalf("the help does not name ctrl+c:\n%s", help)
			}
			if !strings.Contains(help, tc.want) {
				t.Fatalf("ctrl+c is documented as %q, want %q:\n%s", tc.want, help, help)
			}
		})
	}
	// The draining state took one press to close before, and the help called
	// it two. A second press is the same hard stop, so the old wording asked
	// for a keystroke that closes the run the reader can no longer watch.
	if help := helpFor(live); strings.Contains(help, "x2") {
		t.Fatalf("the help still asks for two presses to quit a draining run:\n%s", help)
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
	// Every step of the heat ramp draws a stroke (a meter fill, a chart dot)
	// and never text, so the non-text floor applies to all of them and not
	// only to the track the ramp starts on. A ramp step below 3:1 is a
	// reading that is not there at all. The fractions are the bands' middles,
	// and each is pinned to the token the ramp names for it, so a reordering
	// of the ramp that moved a pale step onto a hot reading fails here too.
	ramp := map[float64]lipgloss.AdaptiveColor{
		0.10: cTeal, 0.35: cCyan, 0.60: cGreen, 0.80: cYellow, 0.95: cRed,
	}
	for frac, want := range ramp {
		if got := heatColor(frac); got != lipgloss.TerminalColor(want) {
			t.Errorf("heatColor(%f) is %v, want %v", frac, got, want)
		}
		if got := contrastRatio(t, want.Dark, darkBase); got < 3 {
			t.Errorf("heat %.2f dark %q is %.2f:1 on the dark base, want at least 3", frac, want.Dark, got)
		}
		if got := contrastRatio(t, want.Light, lightBase); got < 3 {
			t.Errorf("heat %.2f light %q is %.2f:1 on the light base, want at least 3", frac, want.Light, got)
		}
	}
}

func TestActivityTitleClearsContrastFloors(t *testing.T) {
	const darkBase, lightBase = "#1e1e2e", "#eff1f5"
	m := newModel(demoConfig())
	for _, rate := range []float64{0, 0.1, 0.5, 1.0, 10.0, 50.0} {
		m.activity = []float64{rate}
		title := m.activityTitle()
		if !strings.Contains(title, "ACTIVITY") {
			t.Fatalf("rate %f: activityTitle missing ACTIVITY header: %q", rate, title)
		}
	}
	// A low positive rate sits where heatColor returns the unlit track tone,
	// so the title has to floor it to the first readable step of the ramp.
	// 0.5 lines/s is 0.01 of the full scale, inside the track band.
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

	if h := heatColor(clamp01(0.5 / activityRateFull)); h != cTrack {
		t.Fatalf("precondition failed: the cold end of the ramp is %v, want the track tone", h)
	}
	if got := contrastRatio(t, cTeal.Dark, darkBase); got < 4.5 {
		t.Errorf("cTeal dark is %.2f:1 on dark base, want >= 4.5", got)
	}
	if got := contrastRatio(t, cTeal.Light, lightBase); got < 4.5 {
		t.Errorf("cTeal light is %.2f:1 on light base, want >= 4.5", got)
	}
	m.activity = []float64{0.5}
	title := m.activityTitle()
	if !strings.Contains(title, styledTitle(cTeal, "0.5")) {
		t.Fatalf("a low positive rate is not floored to the first readable ramp step:\n%q", title)
	}
	// A rate of zero is not a measurement, so it must not wear the ramp at
	// all: the dim value marker and the floored one have to look different.
	m.activity = []float64{0}
	zero := m.activityTitle()
	if !strings.Contains(zero, styledTitle(cDim, "0")) {
		t.Fatalf("a rate of zero is not drawn as an absent measurement:\n%q", zero)
	}
	if strings.Contains(zero, styledTitle(cTeal, "0")) {
		t.Fatalf("a rate of zero rode the heat ramp:\n%q", zero)
	}
}

// styledTitle is the value marker activityTitle paints, so the assertion
// names the color rather than the escape sequence lipgloss chooses for it.
func styledTitle(fg lipgloss.TerminalColor, value string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(fg).Render("◆ " + value)
}

// The wordmark is the path-arrow teal, one hue: the README logos are a
// single-color name next to that arrow, and a Catppuccin teal or a
// per-letter gradient would be a color the mark does not use.
func TestWordmarkIsTheBrandTeal(t *testing.T) {
	if cMark.Dark != "#0e96a8" {
		t.Fatalf("wordmark dark is %q, want the mark's #0e96a8", cMark.Dark)
	}
	// Under the Ascii profile (a piped, NO_COLOR run) both sides render as
	// the bare string and the comparison holds whatever color wordmark picks,
	// so pin the profile the other color tests pin.
	r := lipgloss.DefaultRenderer()
	prev := r.ColorProfile()
	t.Cleanup(func() { r.SetColorProfile(prev) })
	r.SetColorProfile(termenv.TrueColor)

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

// A bar of blocks is a shape and a hue, which is a reading only some readers
// can take, so every meter on the dashboard names its figure in text beside
// it. The budget is the one that had none: its row read "budget" and a length,
// and a reader who cannot see either has no figure at all (SC 1.1.1). A run
// past its ceiling says so in words, because the bar has nothing left to say.
func TestMetersCarryTheirFigureInText(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.cfg.Budget = time.Hour
	m.now = m.cfg.Started.Add(30 * time.Minute)
	if footer := stripANSI(lastLine(m.View())); !strings.Contains(footer, "budget 50%") {
		t.Errorf("the budget row has no figure:\n%s", footer)
	}
	m.now = m.cfg.Started.Add(90 * time.Minute)
	if footer := stripANSI(lastLine(m.View())); !strings.Contains(footer, "budget 150% over") {
		t.Errorf("a run past its budget does not say so:\n%s", footer)
	}
}

// The lane's sparkline is its output rate as a shape. The counters carry the
// same reading as a figure, so the trace is not the only way to take it
// (SC 1.1.1), and a lane that has printed nothing shows neither.
func TestLaneCountersNameTheOutputRate(t *testing.T) {
	m := newModel(demoConfig())
	l := m.lane("claude")
	l.lineRate = 3.5
	stats, _ := laneStats(m, l, cBlue, 52)
	if !strings.Contains(stripANSI(stats), "3.5 lines/s") {
		t.Errorf("the lane counters do not name the output rate:\n%s", stripANSI(stats))
	}
	l.lineRate = 0
	stats, _ = laneStats(m, l, cBlue, 52)
	if strings.Contains(stripANSI(stats), "lines/s") {
		t.Errorf("an idle lane reports an output rate it did not measure:\n%s", stripANSI(stats))
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

	// The two standard names are read from the ambient environment, and the
	// "the glyph does turn" cases below only hold when neither is exported.
	// Clearing them keeps a shell or a CI job that sets one from failing
	// this test for reasons that have nothing to do with the code.
	t.Setenv("NO_MOTION", "")
	t.Setenv("REDUCED_MOTION", "")

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

	for _, env := range []string{"NO_MOTION", "REDUCED_MOTION"} {
		t.Setenv(env, "1")
		if got := thinkGlyph(now, last); got != motionStill {
			t.Fatalf("glyph %q under %s=1, want held frame %q", got, env, motionStill)
		}
		// An empty GAUNTLET_NO_ANIMATION states nothing, so the standard name
		// still decides. A padded false value is still a false value: the
		// list is the shared one in envx, read the documented way.
		t.Setenv("GAUNTLET_NO_ANIMATION", "  ")
		if got := thinkGlyph(now, last); got != motionStill {
			t.Fatalf("glyph %q with GAUNTLET_NO_ANIMATION blank under %s=1, want held frame %q", got, env, motionStill)
		}
		t.Setenv(env, "  OFF  ")
		if thinkGlyph(now, last) == thinkGlyph(now.Add(thinkingFrame), last) {
			t.Fatalf("%s=%q should mean off", env, "  OFF  ")
		}
		t.Setenv(env, "1")
		// Explicit GAUNTLET_NO_ANIMATION=0 overrides generic variables
		t.Setenv("GAUNTLET_NO_ANIMATION", "0")
		if thinkGlyph(now, last) == thinkGlyph(now.Add(thinkingFrame), last) {
			t.Fatalf("GAUNTLET_NO_ANIMATION=0 should override %s=1", env)
		}
		t.Setenv("GAUNTLET_NO_ANIMATION", "")
		t.Setenv(env, "")
	}

	for _, off := range []string{"0", "false", "False", "no", "off", "  0  "} {
		t.Setenv("GAUNTLET_NO_ANIMATION", off)
		if thinkGlyph(now, last) == thinkGlyph(now.Add(thinkingFrame), last) {
			t.Fatalf("the glyph should turn under GAUNTLET_NO_ANIMATION=%q", off)
		}
	}

	// The freeze must not touch the states that are already still.
	if got := thinkGlyph(now, time.Time{}); got != "◌" {
		t.Fatalf("a never-thinking lane reads %q, want the resting glyph", got)
	}
	if got := thinkGlyph(time.Time{}, last); got != "◌" {
		t.Fatalf("a zero clock reads %q, want the resting glyph", got)
	}
	if got := thinkGlyph(last.Add(-time.Second), last); got != "◌" {
		t.Fatalf("clock step backwards reads %q, want the resting glyph", got)
	}
	if got := thinkGlyph(now.Add(thinkingStill+time.Second), last); got != "◌" {
		t.Fatalf("a finished thought reads %q, want the resting glyph", got)
	}
}

// The motion accommodation has to reach the frame, not only the glyph in it.
// The dashboard repaints itself ten times a second for as long as the run
// lasts, and no key stops that (space pauses the feed, the frame keeps
// moving), so a variable that only held the reasoning glyph still left the
// screen in motion for hours. Under the same variables the redraw falls back
// to a slow heartbeat: the frame changes when a review event lands, when a
// key is pressed, and otherwise a few times a minute (WCAG 2.2.2).
func TestRedrawRateFollowsTheMotionAccommodation(t *testing.T) {
	// The standard names are read from the ambient environment, and the case
	// that expects a moving screen only holds when neither is exported.
	t.Setenv("NO_MOTION", "")
	t.Setenv("REDUCED_MOTION", "")
	t.Setenv("GAUNTLET_NO_ANIMATION", "")

	if got := redrawEvery(); got != tickEvery {
		t.Fatalf("redraw gap is %v with no variable set, want %v", got, tickEvery)
	}
	if motionTickEvery <= tickEvery {
		t.Fatalf("the still redraw gap %v is not slower than %v", motionTickEvery, tickEvery)
	}

	for _, env := range []string{envNoAnimation, envNoMotion, envReducedMotion} {
		t.Setenv(env, "1")
		if got := redrawEvery(); got != motionTickEvery {
			t.Fatalf("redraw gap is %v under %s=1, want %v", got, env, motionTickEvery)
		}
		t.Setenv(env, "")
	}

	// The precedence is the documented one and is shared with the glyph: an
	// explicit false in the project name outranks a desktop session's
	// REDUCED_MOTION, and an empty one defers to it.
	t.Setenv(envReducedMotion, "1")
	t.Setenv(envNoAnimation, "false")
	if got := redrawEvery(); got != tickEvery {
		t.Fatalf("redraw gap is %v under GAUNTLET_NO_ANIMATION=false with REDUCED_MOTION=1, want %v", got, tickEvery)
	}
	t.Setenv(envNoAnimation, "")
	if got := redrawEvery(); got != motionTickEvery {
		t.Fatalf("redraw gap is %v with a blank GAUNTLET_NO_ANIMATION under REDUCED_MOTION=1, want %v", got, motionTickEvery)
	}
}

func TestThinkGlyphPreEpochDoesNotPanic(t *testing.T) {
	// Go's % keeps the dividend's sign: UnixNano before 1970 is negative, so
	// the frame index used to be -1 and the slice lookup panicked.
	// NO_MOTION and REDUCED_MOTION are cleared too: either one makes
	// motionOff() hold, thinkGlyph answers with the still frame, and this
	// switch accepts it without ever reaching the index.
	t.Setenv("GAUNTLET_NO_ANIMATION", "")
	t.Setenv("NO_MOTION", "")
	t.Setenv("REDUCED_MOTION", "")
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

// The readings do not vanish whole when the legend takes the line: the
// footer is the only place the run's diff and token totals are drawn, and a
// tally that goes blank reads as a zero rather than as one that did not fit.
// Whole segments drop from the right end instead, in the order they matter.
func TestFooterKeepsReadingsAtCommonWidths(t *testing.T) {
	m := newModel(demoConfig())
	m.h, m.ready = 30, true
	m.haveLines = true
	m.ins, m.del = 120, 30
	m.tokens, m.thinking = 123456, 12345
	m.cfg.Budget = 4 * time.Hour
	m.now = m.cfg.Started.Add(time.Hour)
	for _, w := range []int{120, 110, 100, 90, 80} {
		m.w = w
		footer := lastLine(stripANSI(m.renderFooter()))
		if !strings.Contains(footer, "+120/-30 lines") {
			t.Fatalf("a %d column footer lost the diff:\n%s", w, footer)
		}
		if lipgloss.Width(footer) > w {
			t.Fatalf("a %d column footer is %d wide:\n%s", w, lipgloss.Width(footer), footer)
		}
	}
}

// A line too narrow even for the first reading says so. Silence there is
// indistinguishable from a run that has changed nothing yet, and so is a line
// that ends where the budget meter would have been.
func TestFooterMarksReadingsThatCannotFit(t *testing.T) {
	segs := []string{styleValue.Render("1,234 tok"), styleDim.Render(" budget")}
	if got := fitRight(segs, 3); !strings.Contains(stripANSI(got), "…") {
		t.Fatalf("narrow footer readings %q, want a marked absence", stripANSI(got))
	}
	if got := fitRight(segs, 0); got != "" {
		t.Fatalf("a footer with no room drew %q", got)
	}
	if got := fitRight(nil, 40); got != "" {
		t.Fatalf("a run with nothing measured drew %q", got)
	}
	if got := fitRight(segs, 13); !strings.Contains(stripANSI(got), "…") {
		t.Fatalf("a dropped reading drew %q, want a marked absence", stripANSI(got))
	}
	if got := stripANSI(fitRight(segs, 40)); strings.Contains(got, "…") || !strings.Contains(got, "budget") {
		t.Fatalf("readings that fit were marked as cut: %q", got)
	}
}

// A lane's counters are the panel's content, so a pane too narrow for them
// drops a whole column rather than cutting one: "11,111,110" shortened to
// "11" is a false reading, and a half-written label is not a label.
func TestLaneCountersDropWholeColumnsWhenNarrow(t *testing.T) {
	m := newModel(demoConfig())
	m.now = time.Date(2026, 8, 25, 0, 2, 0, 0, time.UTC)
	l := m.lane("claude")
	l.review, l.start = "perf-review", m.now.Add(-90*time.Second)
	l.done, l.failed, l.tokens, l.thinkTokens = 33, 11, 4567890, 9876543
	l.tokenRate, l.liveThinking, l.lastThinkAt = 12345, 1234567, m.now
	for _, w := range []int{40, 48, 56, 60, 74, 90, 116, 140} {
		lanes := m.renderLanes(w, 8)
		row := stripANSI(firstLine(lanes))
		if strings.Contains(row, "…") {
			t.Fatalf("a %d column lane cut a value: %s", w, row)
		}
		for _, label := range []string{"do", "fa", "to"} {
			if strings.Contains(row, label+" ") {
				t.Fatalf("a %d column lane drew a half-label %q: %s", w, label, row)
			}
		}
		// A counter that lost its label is the failure this test is named
		// for: a bare "33" is a number the reader takes for a measurement
		// when it is a half of "33 done". Counting is all-or-nothing.
		for count, label := range map[string]string{"33": " done", "11": " fail"} {
			if strings.Contains(row, count) && !strings.Contains(row, count+label) {
				t.Fatalf("a %d column lane drew %q without %q: %s", w, count, label, row)
			}
		}
		// Every row the pane draws has to fit the pane it was asked for: a
		// row wider than w is what makes the frame wrap a line per agent.
		for i, line := range strings.Split(lanes, "\n") {
			if got := lipgloss.Width(stripANSI(line)); got > w {
				t.Fatalf("lane row %d of a %d column pane is %d wide: %q", i, w, got, line)
			}
		}
	}
	// The widest pane still shows every column, and the narrowest still shows
	// what finished, which is the one counter a reader cannot do without.
	wide := stripANSI(firstLine(m.renderLanes(140, 8)))
	for _, want := range []string{"33 done", "11 fail", "4,567,890 tok", "12.3k/s", "11,111,110"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("a wide lane lost %q:\n%s", want, wide)
		}
	}
	narrow := stripANSI(firstLine(m.renderLanes(56, 8)))
	if !strings.Contains(narrow, "33 done") {
		t.Fatalf("a 56 column lane lost the done count:\n%s", narrow)
	}
}

func firstLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[0]
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

// The fallback advertises only keys whose effect it can show: scrolling and
// pausing both act on a feed it does not draw, so naming j/k or space there
// would offer keys whose only visible result is a label about a feed the
// reader is not looking at.
func TestMinimalViewAdvertisesOnlyKeysItCanShow(t *testing.T) {
	frame := stripANSI(staticFrame(demoConfig(), demoEvents(), 40, 10))
	for _, dead := range []string{"scroll", "pause"} {
		if strings.Contains(frame, dead) {
			t.Fatalf("the minimal view advertises %q with no feed behind it:\n%s", dead, frame)
		}
	}
}

// stripANSI removes what a terminal consumes rather than shows. Everything
// here reads a rendered frame back as text, so a sequence this misses leaks
// escape bytes into an assertion message and a sequence it over-reads eats
// real content. An OSC is the case that differs: its payload is text and it
// ends at BEL or ST, not at the next letter.
func stripANSI(s string) string {
	var b strings.Builder
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		if rs[i] != 0x1b {
			b.WriteRune(rs[i])
			i++
			continue
		}
		if i+1 < len(rs) && rs[i+1] == ']' {
			i += 2
			for i < len(rs) && rs[i] != 0x07 {
				if rs[i] == 0x1b && i+1 < len(rs) && rs[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
			if i < len(rs) && rs[i] == 0x07 {
				i++ // the BEL that ended it
			}
			continue
		}
		if i+1 < len(rs) && rs[i+1] == '[' {
			// A CSI: parameters, then a final ASCII letter.
			i += 2
			for i < len(rs) && !isASCIILetter(rs[i]) {
				i++
			}
			if i < len(rs) {
				i++
			}
			continue
		}
		// Any other escape is two characters, ESC and one more. Scanning to
		// the next letter would eat the text after it: ESC ( B is a
		// charset selection, not a run of letters to skip.
		i += 2
	}
	return b.String()
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func TestStripANSIRemovesSequencesAndKeepsTheRest(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{name: "plain", in: "plain text", want: "plain text"},
		{name: "sgr", in: "\x1b[31mred\x1b[0m text", want: "red text"},
		{name: "csi", in: "\x1b[2Jcleared", want: "cleared"},
		{name: "two character escape", in: "\x1b(ball", want: "ball"},
		{name: "osc with bel", in: "\x1b]0;window title\x07kept", want: "kept"},
		{name: "osc with st", in: "\x1b]0;window title\x1b\\kept", want: "kept"},
		{name: "trailing esc", in: "text\x1b[", want: "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSI(tc.in); got != tc.want {
				t.Fatalf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
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

// A lane's elapsed column and its timeout meter are read against the model's
// own clock, and the timeout they mirror is a monotonic timer in the process
// that launched the agent. An event's timestamp comes off the journal and has
// no monotonic reading, so a review that started before a wall-clock step
// shows a rewound clock and an empty meter while the agent is still running.
func TestLaneElapsedRunsOnTheModelClock(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	m.now = m.cfg.Started.Add(90 * time.Second)
	// A stamp an hour behind the model: a clock stepped back, or a review
	// replayed from a journal written on another machine.
	ev := runner.Event{Kind: runner.EvReviewStart, Review: "sec-review",
		Agent: "claude", Time: m.now.Add(-time.Hour)}
	m.apply(ev)
	l := m.lane("claude")
	if !l.start.Equal(m.now) {
		t.Fatalf("lane start is %v, want the model clock %v", l.start, m.now)
	}
	got := stripANSI(m.View())
	if !strings.Contains(got, "1m30s") {
		t.Fatalf("the lane does not show the 90 seconds since the model clock:\n%s", got)
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
	if quitAt < 0 {
		t.Fatalf("help does not say what stops the run:\n%s", got)
	}
	if quitAt < closeAt {
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
func TestHelpOverlayScrollsAndReflows(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 40, 6, true
	for i := range 15 {
		m.conflicts = append(m.conflicts, fmt.Sprintf("branch-%d", i))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	first := stripANSI(m.View())
	if !strings.Contains(first, "scroll") {
		t.Fatalf("help does not advertise scrolling: %s", first)
	}
	var seen strings.Builder
	for range 150 {
		view := stripANSI(m.View())
		seen.WriteString(view)
		seen.WriteByte('\n')
		if lipgloss.Height(view) > m.h || lipgloss.Width(view) > m.w {
			t.Fatalf("help exceeds the viewport: %s", view)
		}
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	for _, want := range []string{"branch-14", "interrupted", "press twice"} {
		if !strings.Contains(seen.String(), want) {
			t.Fatalf("help text %q is unreachable", want)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if got := stripANSI(m.View()); got != first {
		t.Fatalf("home did not return to the first page: %s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	// The unmerged branches close the page, below the instructions, so the end
	// of the page is the last branch and every instruction above it.
	if end := stripANSI(m.View()); !strings.Contains(end, "branch-14") {
		t.Fatalf("end did not reach the last unmerged branch: %s", end)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 80})
	if !strings.Contains(stripANSI(m.View()), "close this help") {
		t.Fatal("resizing did not clamp the scroll position")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.help || m.quitArmed || m.scroll != 0 {
		t.Fatal("help navigation changed dashboard state")
	}
}

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
	// "again" is the word the state has to carry: the press that armed the
	// stop is the one that has to be repeated, which is what the launcher's
	// own armed line says in as many words.
	if got := stripANSI(m.View()); !strings.Contains(got, "q AGAIN TO STOP") {
		t.Fatalf("the header does not say the press must be repeated:\n%s", got)
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
	m = newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.done = true
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a finished run should close the dashboard")
	}
}

// Pressing esc while quit is armed cancels the arming without killing the run.
func TestEscCancelsQuitArming(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quitArmed {
		t.Fatal("first q did not arm quit")
	}
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "esc:cancel") {
		t.Fatalf("footer does not document esc:cancel while quit is armed:\n%s", got)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc while quit armed stopped the run instead of cancelling")
	}
	if m.quitArmed {
		t.Fatal("esc did not disarm quit confirmation")
	}
}

// Pressing esc while feed is paused or scrolled back returns to live output.
func TestEscResetsPauseAndScroll(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.feed = []feedLine{{text: "line1"}, {text: "line2"}, {text: "line3"}}
	m.paused = true
	m.scroll = 2
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "esc:live") {
		t.Fatalf("footer does not document esc:live while scrolled back:\n%s", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.paused {
		t.Fatal("esc did not unpause the feed")
	}
	if m.scroll != 0 {
		t.Fatalf("esc did not reset scroll to live edge: got %d", m.scroll)
	}
}

// Pressing esc on a completed run resets feed scroll to live edge rather than
// quitting and losing the inspection context; a second esc at live edge quits.
func TestEscOnDoneResetsScrollBeforeQuit(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.feed = []feedLine{{text: "line1"}, {text: "line2"}, {text: "line3"}}
	m.done = true
	m.scroll = 2
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "esc:live") {
		t.Fatalf("completed footer does not document esc:live while scrolled back:\n%s", got)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc while scrolled on done quit the dashboard instead of resetting scroll")
	}
	if m.scroll != 0 {
		t.Fatalf("esc did not reset scroll to live edge: got %d", m.scroll)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc at live edge on completed run did not quit")
	}
}

// Ctrl+C on a completed run must quit immediately without needing a second press.
func TestCtrlCOnDoneQuitsImmediately(t *testing.T) {
	finished := false
	cfg := demoConfig()
	cfg.OnFinish = func() { finished = true }
	m := newModel(cfg)
	m.done = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c on completed dashboard did not quit immediately")
	}
	if finished {
		t.Fatal("ctrl+c on completed dashboard triggered OnFinish")
	}
}

// The minimal view shows running reviews so a user on a small terminal has
// visibility into what is currently executing.
func TestMinimalViewShowsActiveReviews(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 50, 15, true
	m.apply(runner.Event{
		Kind:   runner.EvReviewStart,
		Review: "sec-review",
		Agent:  "claude",
		Time:   time.Now(),
	})
	got := stripANSI(m.renderMinimal())
	if !strings.Contains(got, "running: claude: sec") {
		t.Fatalf("minimal view missing running review:\n%s", got)
	}
}

func TestFeedAndHelpPagingKeys(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	for i := range 50 {
		m.feed = append(m.feed, feedLine{text: fmt.Sprintf("line %d", i)})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.scroll == 0 {
		t.Fatal("pgup did not scroll into history")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.scroll != 0 {
		t.Fatalf("pgdown did not scroll back toward live edge: got %d", m.scroll)
	}

	// Space in help overlay pages down
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("help row %d", i)
	}
	scrolled := scrollHelp(0, "space", lines, 80, 20)
	if scrolled <= 0 {
		t.Fatalf("space did not page down in help: got %d", scrolled)
	}
	paged := scrollHelp(0, "pagedown", lines, 80, 20)
	if paged <= 0 {
		t.Fatalf("pagedown did not page down in help: got %d", paged)
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

// The narrowed feed's label names everything it keeps. Diffs survive the
// filter, so a title reading "results and errors" over hunks of code teaches
// the reader that the label is decoration.
func TestFeedFilterLabelNamesWhatItKeeps(t *testing.T) {
	label := feedSignal.label()
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.apply(runner.Event{Kind: runner.EvOutput, Agent: "claude",
		Text: "+ added a line", LineKind: normalize.DiffAdd})
	m.filter = feedSignal
	m.feedDirty = true
	if !m.filter.keep(m.feed[0]) {
		t.Fatal("a diff line does not survive the narrowed feed")
	}
	for _, want := range []string{"results", "errors", "diffs"} {
		if !strings.Contains(label, want) {
			t.Fatalf("the filter label %q does not name %s", label, want)
		}
	}
	if !strings.Contains(stripANSI(m.feedTitle(titleRoom)), label) {
		t.Fatalf("the FEED title does not carry the label:\n%s", stripANSI(m.feedTitle(titleRoom)))
	}
	if !strings.Contains(stripANSI(strings.Join(m.helpLines(), "\n")), label) {
		t.Fatalf("the help does not carry the label:\n%s", m.helpLines())
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

// A conflict is a branch that stays on disk until a human takes it, so the
// dashboard's list only grows and a run left going has no bound of its own.
// The list is capped at maxConflicts and keeps the newest, and both places it
// is drawn say how many the cap left out: a short list must never read as the
// whole run, and the help overlay's key bindings must not be pushed below the
// fold by run data.
func TestConflictListIsBoundedAndCountsWhatItDropped(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 120, 40, true
	total := maxConflicts + 5
	for i := range total {
		m.apply(runner.Event{
			Kind: runner.EvMerge, Review: fmt.Sprintf("review-%02d", i),
			Branch: fmt.Sprintf("gauntlet/x/review-%02d", i), Status: runner.StatusConflict,
		})
	}
	if len(m.conflicts) != maxConflicts {
		t.Fatalf("kept %d conflicts, want the bound %d", len(m.conflicts), maxConflicts)
	}
	if m.conflictsDropped != total-maxConflicts {
		t.Fatalf("dropped %d, want %d", m.conflictsDropped, total-maxConflicts)
	}
	if newest := m.conflicts[len(m.conflicts)-1]; !strings.Contains(newest, fmt.Sprintf("review-%02d", total-1)) {
		t.Fatalf("kept %q, want the newest conflict", newest)
	}
	if oldest := m.conflicts[0]; !strings.Contains(oldest, fmt.Sprintf("review-%02d", total-maxConflicts)) {
		t.Fatalf("kept %q, want the oldest still inside the bound", oldest)
	}

	// The same conflict twice is still one branch.
	again := m.conflicts[0]
	m.apply(runner.Event{Kind: runner.EvMerge, Review: "review-06", Branch: "gauntlet/x/review-06", Status: runner.StatusConflict})
	if len(m.conflicts) != maxConflicts || m.conflicts[0] != again {
		t.Fatalf("a repeated conflict grew the list: %d kept, first %q", len(m.conflicts), m.conflicts[0])
	}

	minimal := stripANSI(m.renderMinimal())
	if !strings.Contains(minimal, "older") {
		t.Fatalf("the fallback claims a short list is the whole run:\n%s", minimal)
	}

	// The keys come first, so they are on the page without scrolling however
	// many branches are unmerged.
	lines := m.helpLines()
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "q, esc, enter close") && !strings.Contains(joined, "stop the run, killing what is running") {
		t.Fatalf("the help page lost its key bindings:\n%s", joined)
	}
	unmerged, glyphs := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(stripANSI(l), "Unmerged branches"):
			unmerged = i
		case strings.Contains(stripANSI(l), "Review glyphs"):
			glyphs = i
		}
	}
	if unmerged < 0 {
		t.Fatalf("the help page lost the unmerged branches:\n%s", joined)
	}
	if unmerged < glyphs {
		t.Fatalf("the unmerged list opens at line %d, in front of the last instruction at %d:\n%s", unmerged, glyphs, joined)
	}
	if !strings.Contains(joined, fmt.Sprintf("%d older one(s)", total-maxConflicts)) {
		t.Fatalf("the help page does not say what the cap left out:\n%s", joined)
	}
}

// The feed's one line kind that its own text does not identify is an error
// the agent reported: a diff says the sign it was added or removed with, a
// result line says RESULT:, reasoning is italic. So the error has to be
// marked in the line itself, and it has to survive a monochrome terminal,
// which is what --no-color and NO_COLOR leave behind and what a reader who
// cannot separate the hues is looking at either way (SC 1.4.1).
func TestFeedMarksAgentErrorsWithoutColor(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 40, true
	m.feed = []feedLine{
		{text: "reading main.go", kind: normalize.Plain},
		{text: "the build failed on line 12", kind: normalize.Error},
		{text: "+ added a line", kind: normalize.DiffAdd},
	}
	got := stripANSI(m.renderFeed(100, 10))
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("the feed drew %d rows, want 3:\n%s", len(lines), got)
	}
	if strings.HasPrefix(lines[0], "!") || strings.HasPrefix(lines[2], "!") {
		t.Fatalf("a line that names itself carries the error mark anyway:\n%s", got)
	}
	if !strings.HasPrefix(lines[1], "!") {
		t.Fatalf("an agent error is not marked, so --no-color leaves it as narration:\n%s", got)
	}
	if !strings.Contains(stripANSI(m.renderHelp()), "Feed mark:") {
		t.Fatalf("the feed mark is drawn but not named in the help:\n%s", m.renderHelp())
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

func TestLanesKeepStatsAtEightyColumns(t *testing.T) {
	cfg := demoConfig()
	m := newModel(cfg)
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	frame := stripANSI(staticFrame(cfg, demoEvents(), 80, 24))
	for _, want := range []string{"done", "fail", "tok"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("80-column frame lost %q from agent lanes:\n%s", want, frame)
		}
	}
}

func TestFooterShowsEscLiveWhenPausedAtLiveEdge(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.feed = []feedLine{{text: "line1"}}
	m.paused = true
	m.scroll = 0
	if got := lastLine(stripANSI(m.View())); !strings.Contains(got, "esc:live") {
		t.Fatalf("footer does not document esc:live while paused at live edge:\n%s", got)
	}
}

// A run that ends without a line cannot still be waiting for one: the feed
// saying it is, under a header reading DONE, is a promise nothing will keep,
// and the panel a reader checks for what happened answers none of it.
func TestFeedNamesAFinishedRunThatSaidNothing(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.renderFeed(titleRoom, 3)); !strings.Contains(got, "waiting") {
		t.Fatalf("a running feed with no output reads %q, want the waiting state", got)
	}
	m.done = true
	if got := stripANSI(m.renderFeed(titleRoom, 3)); !strings.Contains(got, "no agent output") {
		t.Fatalf("a finished feed with no output reads %q, want the run to be named", got)
	}
}

func TestFeedTitleMarksPaused(t *testing.T) {
	m := newModel(demoConfig())
	m.paused = true
	if got := stripANSI(m.feedTitle(titleRoom)); !strings.Contains(got, "paused") {
		t.Fatalf("feed title %q, want paused indicator", got)
	}
}

// A pane too narrow for the whole title drops a whole reading and marks the
// cut: "3 lin…" names a distance the screen never states.
func TestNarrowPaneDropsWholeTitleSegments(t *testing.T) {
	m := newModel(demoConfig())
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	m.paused = true
	m.filter = feedSignal
	m.feed = make([]feedLine, 40)
	m.scroll = 3
	got := stripANSI(m.feedTitle(30))
	if !strings.Contains(got, "paused") || !strings.HasSuffix(got, "…") {
		t.Fatalf("narrow feed title %q, want whole segments and a marked cut", got)
	}
	if strings.Contains(got, "lin") {
		t.Fatalf("narrow feed title %q keeps half a reading", got)
	}
	if wide := stripANSI(m.feedTitle(titleRoom)); !strings.Contains(wide, "3 lines back") {
		t.Fatalf("a roomy pane dropped a reading it had space for: %q", wide)
	}
}

// The fallback's first row is read for the run state, so the state and the
// clock are what survive a narrow terminal: the version and the loop number go
// first, and the row is never cut at the state.
func TestMinimalHeaderKeepsTheRunState(t *testing.T) {
	// The narrow widths are the ones this was wrong at: the row held the clock
	// and the state, did not fit, and was cut at the right, so the one reading
	// the fallback exists for arrived as "\u25cf RU\u2026". The clock goes first.
	for _, w := range []int{10, 12, 14, 20, 30, 40, 49} {
		m := newModel(demoConfig())
		m.w, m.h, m.ready = w, 10, true
		for _, ev := range demoEvents() {
			m.apply(ev)
		}
		m.now = m.cfg.Started.Add(90 * time.Second)
		row := stripANSI(strings.Split(m.renderMinimal(), "\n")[0])
		if !strings.Contains(row, "\u25cf RUNNING") {
			t.Fatalf("w=%d: header %q lost the run state", w, row)
		}
		if lipgloss.Width(m.minimalHeader("● RUNNING", styleOK)) > w {
			t.Fatalf("w=%d: header row does not fit the pane", w)
		}
	}
}

// The fallback names the way back to a live feed even though it draws none: the
// state label above it says the feed is held, and a state nothing on the
// screen clears is a dead end.
func TestMinimalViewNamesTheWayBackToLive(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 40, 10, true
	m.paused = true
	keys := lastLine(stripANSI(m.renderMinimal()))
	if !strings.Contains(keys, "esc live") {
		t.Fatalf("fallback keys %q, want esc live while the feed is held", keys)
	}
	if !strings.Contains(stripANSI(m.renderMinimal()), "FEED PAUSED") {
		t.Fatal("the fallback stopped reporting the state it now clears")
	}
	m.paused, m.scroll = false, 0
	if got := lastLine(stripANSI(m.renderMinimal())); strings.Contains(got, "live") {
		t.Fatalf("keys %q advertise a way back from a feed that is live", got)
	}
}

func TestScrollHelpSupportsBForPageUp(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	scroll := 30
	newScroll := scrollHelp(scroll, "b", lines, 80, 20)
	if newScroll >= scroll {
		t.Fatalf("key 'b' did not scroll up: before %d, after %d", scroll, newScroll)
	}
}

// The overlay's own key row names every key scrollHelp binds. space pages the
// overlay down and was unmentioned, so a reader pressing it saw the help move
// and had no way to know the row was lying by omission rather than the key
// being broken (WCAG 3.3.2).
func TestHelpOverlayNamesEveryKeyItBinds(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.help, m.ready = 100, 30, true, true
	legend := lastLine(stripANSI(m.View()))
	for _, want := range []string{"q/esc close", "j/k scroll", "pgup/pgdn", "space", "home/end", "g/G"} {
		if !strings.Contains(legend, want) {
			t.Fatalf("the help overlay's key row does not name %q:\n%s", want, legend)
		}
	}
	// And the keys the close line claims are the ones that close it. h is
	// bound on the dashboard and was missing from that line.
	head, _, ok := strings.Cut(stripANSI(m.View()), "close this help")
	if !ok {
		t.Fatal("the overlay does not say how to close it")
	}
	closeLine := strings.TrimSpace(strings.Split(head, "\n")[len(strings.Split(head, "\n"))-1])
	if !strings.Contains(closeLine, "h") {
		t.Fatalf("h closes this overlay but the close line does not say so: %q", closeLine)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.help {
		t.Fatal("h did not close the help")
	}
}

// The overlay's key row names where the reader is: the last page and the
// first look alike, and a reader paging down has no other way to know the end
// arrived short of pressing the key once more and seeing nothing move (WCAG
// 2.4.5). A page that fits the pane whole says nothing, having nothing to be
// lost by.
func TestHelpOverlaySaysWhichPageItIs(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	const w, h = 100, 10 // a viewport of nine rows under the legend
	first := stripANSI(lastLine(renderHelpPage(lines, 0, w, h)))
	if !strings.Contains(first, "page 1/5") {
		t.Fatalf("the first page does not say where it is: %q", first)
	}
	// A pane too narrow for the whole row keeps the keys and drops the
	// position: the keys are how the reader reaches the next page, and the
	// position is only how they know they have arrived.
	narrow := stripANSI(lastLine(renderHelpPage(lines, 0, 40, h)))
	if strings.Contains(narrow, "page") {
		t.Fatalf("the page position crowded out a key name: %q", narrow)
	}
	if !strings.HasPrefix(narrow, "q/esc close") {
		t.Fatalf("the position crowded out the closing keys: %q", narrow)
	}
	end := scrollHelp(0, "G", lines, w, h)
	if end == 0 {
		t.Fatal("G did not scroll the overlay")
	}
	last := stripANSI(lastLine(renderHelpPage(lines, end, w, h)))
	if strings.Contains(last, "page 1/") {
		t.Fatalf("the bottom of the overlay still reads as the first page: %q", last)
	}
	if !strings.Contains(last, "/5") {
		t.Fatalf("the last page does not name the count of pages: %q", last)
	}
	if short := stripANSI(lastLine(renderHelpPage(lines[:4], 0, w, h))); strings.Contains(short, "page") {
		t.Fatalf("a help page that fits whole still claims a position: %q", short)
	}
}

// A pane too narrow for the whole key row loses whole keys rather than half a
// name: a legend ending in "j/k scrol" advertises a key that does not exist.
// What it lost is marked, so a row that kept two of four reads as two.
func TestHelpLegendDropsWholeSegmentsNotHalfNames(t *testing.T) {
	for _, w := range []int{20, 30, 45, 60, 80, 200} {
		legend := stripANSI(helpLegend(w, ""))
		if got := lipgloss.Width(legend); w >= len("q/esc close") && got > w {
			t.Errorf("at %d columns the legend is %d wide: %q", w, got, legend)
		}
		if !strings.HasPrefix(legend, "q/esc close") {
			t.Errorf("at %d columns the closing keys are not the first thing kept: %q", w, legend)
		}
		segs := slices.Collect(strings.SplitSeq(legend, "  "))
		// Whatever survives is a whole key name, so no segment ends mid-word.
		dropped := 0
		for _, seg := range segs {
			switch seg {
			case "q/esc close", "j/k scroll", "pgup/pgdn, space/b", "home/end, g/G":
			case "…":
				dropped++
				if dropped > 1 {
					t.Errorf("at %d columns the legend marks the cut twice: %q", w, legend)
				}
			default:
				t.Errorf("at %d columns the legend carries a cut segment %q: %q", w, seg, legend)
			}
		}
		if len(segs)-dropped != len(helpLegendKeys) && dropped != 1 &&
			lipgloss.Width(strings.Join(segs[:len(segs)-dropped], "  "))+3 <= w {
			t.Errorf("at %d columns keys were dropped and there was room to say so: %q", w, legend)
		}
	}
}

// The fallback's key line is its last row and is the one that stays: a
// terminal three rows tall trimmed from the bottom is a tally and no way to
// leave, which is the dead end the fallback exists to avoid.
func TestMinimalViewKeepsTheKeysOnAShortTerminal(t *testing.T) {
	for _, h := range []int{3, 4, 5} {
		m := newModel(demoConfig())
		m.w, m.h, m.ready = 40, h, true
		keys := stripANSI(lastLine(m.renderMinimal()))
		if !strings.Contains(keys, "q") || !strings.Contains(keys, "help") {
			t.Errorf("h=%d: the last row is not the key line: %q", h, keys)
		}
		if got := len(strings.Split(m.renderMinimal(), "\n")); got > h {
			t.Errorf("h=%d: the fallback is %d rows", h, got)
		}
	}
}

// The fallback exists for terminals too narrow to hold the frame, and the
// lines it keeps are the ones a reader has to act on. A cut ends in the
// marker, so "unmerged: sec-review (gauntlet/x/sec-rev" reads as text the
// terminal ran out of room for rather than as a branch name.
func TestMinimalViewMarksCutLines(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 32, 8, true
	for line := range strings.SplitSeq(stripANSI(m.renderMinimal()), "\n") {
		if lipgloss.Width(line) > 32 {
			t.Errorf("a %d-column fallback drew a %d-column line: %q", 32, lipgloss.Width(line), line)
		}
		if lipgloss.Width(line) == 32 && !strings.Contains(line, "…") {
			t.Errorf("a full-width fallback line is not marked as cut: %q", line)
		}
	}
}

// Bubble Tea paints a frame before the window size arrives. A blank alternate
// screen is a screen with nothing on it, so the wait is named, the way the
// launcher names it.
func TestDashboardNamesTheWaitForTheFirstFrame(t *testing.T) {
	m := newModel(demoConfig())
	if got := stripANSI(m.View()); !strings.Contains(got, "starting") {
		t.Fatalf("the first frame says nothing: %q", got)
	}
}

// The narrowed feed keeps the failures and drops the narration, so a line the
// classifier reads as plain narration is a failure the reader cannot see. The
// commit and merge steps carry their verdict mid-line, past the prefix arms.
func TestLogKindKeepsFailuresThatDoNotStartTheLine(t *testing.T) {
	for _, c := range []struct {
		text string
		want normalize.Kind
	}{
		{"commit+push step FAILED to launch (codex): no such binary", normalize.Error},
		{"commit step FAILED (claude), exit 1", normalize.Error},
		{"Not merging into main: 2 uncommitted path(s) would be left behind", normalize.Error},
		{"Not merging into main: this tree is on a detached HEAD", normalize.Error},
		{"FAILED: code-review (codex) after 30m, exit 1", normalize.Error},
		{"Push after the commit step failed: rejected", normalize.Error},
		{"MERGE CONFLICT: review/x does not merge into main (add/add)", normalize.Error},
		{"Cannot merge review/x into main: refusing unrelated histories", normalize.Error},
		{"Running commit step with codex", normalize.Plain},
		{"code-review step done (codex)", normalize.Plain},
	} {
		if got := logKind(c.text); got != c.want {
			t.Errorf("logKind(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// A grid cell is one column wide, so a review whose name is longer than that
// is drawn cut, and the feed trims the same name to sixteen columns on every
// line: on the dashboard, a name like this exists only as an ellipsis. The
// help overlay is the one place with the width for it, it is reachable by
// keyboard from anywhere, and it says the outcome in words beside the glyph
// rather than leaving the status to a shape.
func TestHelpNamesEveryReviewWhole(t *testing.T) {
	cfg := demoConfig()
	cfg.Reviews = append(cfg.Reviews, "concurrency-lane-isolation-review")
	m := newModel(cfg)
	m.w, m.h, m.ready = 120, 40, true
	for _, ev := range demoEvents() {
		m.apply(ev)
	}
	if grid := stripANSI(m.renderGrid(116, 4)); strings.Contains(grid, "concurrency-lane-isolation-review") {
		t.Fatalf("precondition failed: the grid held a name its cell cannot fit:\n%s", grid)
	}
	m.help = true
	help := stripANSI(m.View())
	if !strings.Contains(help, "concurrency-lane-isolation") {
		t.Errorf("the help overlay cut a review name the grid could not hold:\n%s", help)
	}
	// The status is a word as well as a glyph, so it survives a screen reader
	// and a terminal that draws the glyph as a box.
	for _, want := range []string{"sec  ok", "doc  timeout", "perf  pending"} {
		if !strings.Contains(help, want) {
			t.Errorf("the roster does not carry %q in text:\n%s", want, help)
		}
	}
}

// The pause key dies with the run, and the footer stops advertising it in
// that state. A help page that still describes it names a key a reader can
// press and see nothing happen, which is the one they cannot tell from a key
// that is broken.
func TestHelpDropsTheDeadPauseKeyWhenTheRunIsOver(t *testing.T) {
	live := stripANSI(strings.Join(newModel(demoConfig()).helpLines(), "\n"))
	if !strings.Contains(live, "pause the feed") {
		t.Fatalf("a running dashboard does not document the pause key:\n%s", live)
	}
	m := newModel(demoConfig())
	m.done = true
	finished := stripANSI(strings.Join(m.helpLines(), "\n"))
	if strings.Contains(finished, "pause the feed") {
		t.Fatalf("a finished run still documents the pause key:\n%s", finished)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if m.paused {
		t.Fatal("space paused a finished run")
	}
	if cmd != nil {
		t.Fatal("space quit a finished run")
	}
}

// The overlay names the pause key from the state the reader is in, so a paused
// reader who opens it to find out how to unpause is not told to pause again.
// The footer legend already follows the state; the page has to agree with it.
func TestHelpNamesThePauseKeyFromTheCurrentState(t *testing.T) {
	m := newModel(demoConfig())
	m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if !m.paused {
		t.Fatal("space did not pause the feed")
	}
	held := stripANSI(strings.Join(m.helpLines(), "\n"))
	if !strings.Contains(held, "resume the feed") {
		t.Fatalf("a paused dashboard documents the pause key as:\n%s", held)
	}
	if strings.Contains(held, "pause the feed") {
		t.Fatalf("a paused dashboard still says space pauses the feed:\n%s", held)
	}
}

// The fallback's running row is clipped like every other line, and a clip
// leaves the tail of the lane list unnamed with nothing to say how much. The
// panel and the grid both count what they dropped, so an agent missing from
// the one row that names the running work has to read as counted, not gone.
func TestMinimalViewCountsTheRunningLanesItCannotName(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 200, 20, true
	active := []string{"claude", "codex", "gemini", "aider", "cline"}
	for _, a := range active {
		m.lane(a).review = a + "-review"
	}
	line := ""
	for l := range strings.SplitSeq(stripANSI(m.renderMinimal()), "\n") {
		if strings.HasPrefix(l, "running:") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the fallback does not report the running lanes")
	}
	if n := len(active) - activeSummaryMax; !strings.Contains(line, fmt.Sprintf("(+%d more)", n)) {
		t.Fatalf("the fallback drops %d lanes without saying so: %q", n, line)
	}
	for _, a := range []string{"aider", "cline"} {
		if strings.Contains(line, a) {
			t.Fatalf("a lane past the bound is still drawn: %q", line)
		}
	}
}

// The feed title reports a scrollback, so it names the key that ends one. The
// footer offers esc:live and drops whole segments to fit, and on a narrow
// terminal it is the first to go; the unmerged count already names its own
// key this way, and a reported state with no way back out of it is a dead end
// the reader has to guess at.
func TestFeedTitleNamesTheWayBackFromAScrollback(t *testing.T) {
	m := newModel(demoConfig())
	m.w, m.h, m.ready = 100, 30, true
	m.feed = []feedLine{{text: "line1"}, {text: "line2"}, {text: "line3"}}
	m.scroll = 2
	title := stripANSI(m.feedTitle(m.w - 4))
	if !strings.Contains(title, "2 lines back") {
		t.Fatalf("the feed title does not report the scrollback: %q", title)
	}
	if !strings.Contains(title, "esc") {
		t.Fatalf("the feed title reports a scrollback with no way back: %q", title)
	}
}

// TestBrailleSuffixMatchesLitSubRows pins the chart's five-entry pattern table
// against the per-cell bit OR it replaced, over every cell row, chart height,
// and quarter step of level a cell can hold. The table is what lets a chart
// skip the four-sub-row loop, so it has to light exactly the same cells.
func TestBrailleSuffixMatchesLitSubRows(t *testing.T) {
	for h := 1; h <= 4; h++ {
		for cy := range h {
			for step := 0; step <= h*4*4; step++ {
				level := float64(step) / 4
				var want int
				for sr := range 4 {
					if float64(h*4-(cy*4+sr)) <= level {
						want |= int(brailleBits[sr][0]) | int(brailleBits[sr][1])
					}
				}
				lit := 4 - clampi(int(math.Ceil(float64(h*4-cy*4)-level)), 0, 4)
				if got := brailleSuffix[lit]; got != want {
					t.Fatalf("h=%d cell row=%d level=%v: pattern %#x, want %#x",
						h, cy, level, got, want)
				}
			}
		}
	}
}
