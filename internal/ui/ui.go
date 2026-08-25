// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"

	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runner"
)

// Config describes the run the dashboard is watching.
type Config struct {
	Version string
	RunID   string
	Dirs    []string
	Agents  []string
	Reviews []string
	Jobs    int
	Timeout time.Duration
	Budget  time.Duration
	Started time.Time
}

// tickEvery drives the redraw. Ten frames a second is enough to feel alive
// and cheap enough that the dashboard never competes with the agents for CPU.
const tickEvery = 100 * time.Millisecond

// feedMax bounds the retained output feed. Memory stays proportional to what
// can be drawn and scrolled, not to what an agent printed.
const feedMax = 2000

// activitySamples is the width of the activity history ring.
const activitySamples = 600

type reviewState struct {
	name     string
	status   string
	agentLbl string
	start    time.Time
	elapsed  time.Duration
	tokens   int
	ins, del int
	flash    time.Time // set on change so the row can pulse
}

type laneState struct {
	label   string
	review  string
	start   time.Time
	done    int
	failed  int
	tokens  int       // finished reviews only
	lines   []float64 // output lines per second, newest last
	pending float64

	// liveTokens is what the running review has reported so far, and
	// tokenRate is the measured throughput. Both are zero for agents that
	// only report usage when they exit: no rate is shown rather than a
	// made-up one.
	liveTokens   int
	liveThinking int
	thinkTokens  int // finished reviews' reasoning share
	lastTokens   int
	lastAt       time.Time
	lastThinkAt  time.Time // when the reasoning share last grew
	tokenRate    float64
}

type feedLine struct {
	text   string
	kind   normalize.Kind
	agent  string
	review string
	repeat int
}

type model struct {
	cfg   Config
	hues  *hueMap
	w, h  int
	ready bool

	order   []string
	reviews map[string]*reviewState
	lanes   map[string]*laneState
	laneOrd []string

	feed      []feedLine
	scroll    int
	paused    bool
	help      bool
	done      bool
	reloading bool

	loop        int
	counts      map[string]int
	tokens      int
	thinking    int
	agentTime   time.Duration
	ins, del    int
	haveLines   bool
	conflicts   []string
	activity    []float64
	liveRate    float64 // measured tok/s across the lanes reporting usage
	pendingRate float64
	lastSample  time.Time
	now         time.Time
}

// Dashboard owns the terminal for the duration of a run.
type Dashboard struct {
	prog   *tea.Program
	events <-chan runner.Event
}

// newModel builds the dashboard state for one run.
func newModel(cfg Config) *model {
	m := &model{
		cfg: cfg, hues: newHueMap(),
		reviews: map[string]*reviewState{},
		lanes:   map[string]*laneState{},
		counts:  map[string]int{},
		now:     time.Now(), lastSample: time.Now(),
	}
	if cfg.Started.IsZero() {
		m.cfg.Started = time.Now()
	}
	for _, r := range cfg.Reviews {
		if _, dup := m.reviews[r]; dup {
			continue // repeats are weight, not extra rows
		}
		m.reviews[r] = &reviewState{name: r, status: "pending"}
		m.order = append(m.order, r)
	}
	// Pre-seed the lanes so the panel shows its structure before the first
	// review starts. The keys must match laneKey, or the first event opens a
	// second row for the same agent.
	for _, a := range cfg.Agents {
		keys := []string{a}
		if len(cfg.Dirs) > 1 {
			keys = keys[:0]
			for _, d := range cfg.Dirs {
				keys = append(keys, a+" @"+filepath.Base(d))
			}
		}
		for _, k := range keys {
			m.lanes[k] = &laneState{label: k}
			m.laneOrd = append(m.laneOrd, k)
			m.hues.get(k)
		}
	}
	return m
}

// New builds a dashboard fed by one subscription to the run's event bus.
func New(cfg Config, events <-chan runner.Event) *Dashboard {
	return &Dashboard{
		prog:   tea.NewProgram(newModel(cfg), tea.WithAltScreen()),
		events: events,
	}
}

// Run displays the dashboard until the user quits or the run finishes.
func (d *Dashboard) Run() error {
	go func() {
		for ev := range d.events {
			d.prog.Send(eventMsg(ev))
		}
	}()
	_, err := d.prog.Run()
	return err
}

// Finish tells the dashboard the run is over. The screen stays up so the final
// state can be read; q closes it.
func (d *Dashboard) Finish() { d.prog.Send(doneMsg{}) }

// Quit closes the dashboard without waiting for a keypress. A hot reload uses
// it: the successor needs the terminal, and nobody is there to press q.
func (d *Dashboard) Quit() { d.prog.Quit() }

type eventMsg runner.Event
type tickMsg time.Time
type doneMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd { return tick() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h, m.ready = msg.Width, msg.Height, true
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		m.sampleActivity()
		return m, tick()

	case doneMsg:
		m.done = true
		return m, nil

	case eventMsg:
		m.apply(runner.Event(msg))
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if m.help {
				m.help = false
				return m, nil
			}
			return m, tea.Quit
		case " ":
			m.paused = !m.paused
		case "j", "down":
			m.scroll = max(0, m.scroll-1)
		case "k", "up":
			m.scroll = min(m.scroll+1, max(0, len(m.feed)-1))
		case "g":
			m.scroll = max(0, len(m.feed)-1)
		case "G":
			m.scroll = 0
		case "?", "h":
			m.help = !m.help
		}
	}
	return m, nil
}

// sampleActivity folds the lines seen since the last tick into the history
// ring, as a per-second rate.
func (m *model) sampleActivity() {
	elapsed := m.now.Sub(m.lastSample).Seconds()
	if elapsed < 0.2 {
		return
	}
	m.lastSample = m.now
	m.activity = appendRing(m.activity, m.pendingRate/elapsed, activitySamples)
	m.pendingRate = 0
	for _, l := range m.lanes {
		l.lines = appendRing(l.lines, l.pending/elapsed, 120)
		l.pending = 0
	}
}

func appendRing(vals []float64, v float64, maxLen int) []float64 {
	vals = append(vals, v)
	if len(vals) > maxLen {
		vals = vals[len(vals)-maxLen:]
	}
	return vals
}

func (m *model) apply(ev runner.Event) {
	switch ev.Kind {
	case runner.EvLoopStart:
		m.loop = ev.Loop
		// A new loop resets the grid: pending again, results kept in counters.
		for _, r := range m.reviews {
			if r.status != "running" {
				r.status = "pending"
			}
		}
	case runner.EvLoopEnd:
		m.loop = ev.Loop
		if ev.Ins != nil && ev.Del != nil {
			m.ins += *ev.Ins
			m.del += *ev.Del
			m.haveLines = true
		}
	case runner.EvReviewStart:
		r := m.review(ev.Review)
		r.status, r.agentLbl, r.start, r.flash = "running", ev.Agent, ev.Time, ev.Time
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.review, l.start = ev.Review, ev.Time
			l.liveTokens, l.liveThinking, l.lastTokens, l.tokenRate = 0, 0, 0, 0
			l.lastAt, l.lastThinkAt = ev.Time, time.Time{}
		}

	case runner.EvUsage:
		if l := m.lane(m.laneKey(ev)); l != nil {
			if ev.Thinking > l.liveThinking {
				l.liveThinking = ev.Thinking
				l.lastThinkAt = ev.Time
			}
			l.liveTokens = ev.Tokens
			if !l.lastAt.IsZero() {
				if dt := ev.Time.Sub(l.lastAt).Seconds(); dt >= 0.5 {
					if d := ev.Tokens - l.lastTokens; d > 0 {
						// Smooth just enough that the number is readable
						// without hiding a real change.
						rate := float64(d) / dt
						if l.tokenRate == 0 {
							l.tokenRate = rate
						} else {
							l.tokenRate = 0.6*l.tokenRate + 0.4*rate
						}
					}
					l.lastTokens, l.lastAt = ev.Tokens, ev.Time
				}
			} else {
				l.lastTokens, l.lastAt = ev.Tokens, ev.Time
			}
			m.liveRate = m.aggregateRate()
		}
	case runner.EvReviewEnd:
		r := m.review(ev.Review)
		r.status = string(ev.Status)
		r.agentLbl = ev.Agent
		r.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
		r.tokens = ev.Tokens
		r.flash = ev.Time
		if ev.Ins != nil && ev.Del != nil {
			r.ins, r.del = *ev.Ins, *ev.Del
		}
		m.counts[string(ev.Status)]++
		m.tokens += ev.Tokens
		m.thinking += ev.Thinking
		m.agentTime += r.elapsed
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.review = ""
			l.done++
			l.tokens += ev.Tokens
			l.thinkTokens += ev.Thinking
			l.liveTokens, l.liveThinking, l.tokenRate = 0, 0, 0
			if ev.Status != runner.StatusOK {
				l.failed++
			}
		}
	case runner.EvMerge:
		if ev.Status == runner.StatusConflict {
			m.conflicts = append(m.conflicts, ev.Review+" ("+ev.Branch+")")
		}
	case runner.EvReload:
		m.reloading = true
		m.pushFeed(feedLine{text: ev.Text, kind: normalize.Result})
	case runner.EvLog:
		m.pushFeed(feedLine{text: ev.Text, kind: normalize.Plain, review: "runner"})
	case runner.EvOutput:
		m.pendingRate++
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.pending++
		}
		m.pushFeed(feedLine{
			text: ev.Text, kind: ev.LineKind, agent: ev.Agent,
			review: ev.Review, repeat: ev.Repeat,
		})
	}
}

// aggregateRate sums the measured throughput of every lane that reports it.
func (m *model) aggregateRate() float64 {
	total := 0.0
	for _, l := range m.lanes {
		total += l.tokenRate
	}
	return total
}

func (m *model) review(name string) *reviewState {
	if r, ok := m.reviews[name]; ok {
		return r
	}
	r := &reviewState{name: name, status: "pending"}
	m.reviews[name] = r
	m.order = append(m.order, name)
	return r
}

// laneKey identifies the row an event belongs to. One agent working two
// directories is two lanes: sharing a row would make each overwrite the
// other's current review.
func (m *model) laneKey(ev runner.Event) string {
	if len(m.cfg.Dirs) < 2 || ev.Dir == "" {
		return ev.Agent
	}
	return ev.Agent + " @" + filepath.Base(ev.Dir)
}

func (m *model) lane(label string) *laneState {
	if label == "" {
		return nil
	}
	if l, ok := m.lanes[label]; ok {
		return l
	}
	l := &laneState{label: label}
	m.lanes[label] = l
	m.laneOrd = append(m.laneOrd, label)
	m.hues.get(label)
	return l
}

func (m *model) pushFeed(l feedLine) {
	// The feed mixes pre-normalized agent output with log lines that carry
	// fragments of a possibly hostile repository (git stderr, merge output,
	// prompt names). Every line is sanitized here, once, so nothing reaches
	// the terminal able to drive it; visible text is untouched.
	l.text = normalize.Sanitize(l.text)
	m.feed = append(m.feed, l)
	if len(m.feed) > feedMax {
		m.feed = m.feed[len(m.feed)-feedMax:]
	}
	// A paused or scrolled-back reader holds their place: the viewport stays
	// anchored to the lines it shows while history grows underneath, and
	// nothing printed during a pause is discarded.
	if m.paused || m.scroll > 0 {
		m.scroll++
	}
}

func (m *model) View() string {
	if !m.ready {
		return "\n  gauntlet is warming up…"
	}
	if m.help {
		return m.renderHelp()
	}
	if m.w < 60 || m.h < 18 {
		return m.renderMinimal()
	}

	inner := m.w - 4 // panel border and padding
	actH, laneH, gridH, feedH := m.sectionHeights()

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		panel(m.activityTitle(), chart(m.activity, inner, actH), inner, actH),
		panel("AGENTS", m.renderLanes(inner, laneH), inner, laneH),
		panel(m.gridTitle(), m.renderGrid(inner, gridH), inner, gridH),
		panel(m.feedTitle(), m.renderFeed(inner, feedH), inner, feedH),
	)

	// The frame is exactly h rows: the footer owns the last one, and content
	// is padded or cut to fit above it. Letting the body decide the height is
	// how a footer ends up wrapped onto the last content line.
	rows := strings.Split(body, "\n")
	content := max(m.h-1, 1)
	for len(rows) < content {
		rows = append(rows, "")
	}
	rows = append(rows[:content], m.renderFooter())
	return strings.Join(rows, "\n")
}

// sectionHeights splits the frame into exact inner heights. Fixed chrome is
// the header, four panel titles, four box borders, and the footer.
func (m *model) sectionHeights() (act, lanes, grid, feed int) {
	chrome := 1 + 4*3 + 1
	free := max(m.h-chrome, 8)
	lanes = clampi(len(m.laneOrd), 1, 8)
	act = clampi(free/5, 3, 8)
	gridRows := (len(m.order) + m.gridCols() - 1) / max(m.gridCols(), 1)
	grid = clampi(gridRows, 1, max(free-act-lanes-3, 1))
	feed = free - act - lanes - grid
	if feed < 3 {
		take := 3 - feed
		act = max(act-take, 2)
		feed = 3
	}
	return act, lanes, grid, feed
}

func (m *model) gridCols() int {
	cell := m.reviewCellWidth()
	return max((m.w-4)/cell, 1)
}

// reviewCellWidth is the width of one review cell: glyph, space, name. The
// longest name is measured in terminal cells, not bytes or runes, so a
// non-ASCII name budgets the space it will actually occupy.
func (m *model) reviewCellWidth() int {
	longest := 8
	for _, n := range m.order {
		longest = max(longest, uniseg.StringWidth(strings.TrimSuffix(n, "-review")))
	}
	return min(longest+4, 22)
}

func (m *model) renderHeader() string {
	elapsed := m.now.Sub(m.cfg.Started)
	left := []string{
		wordmark(),
		styleDim.Render("v" + m.cfg.Version),
		styleDim.Render(m.cfg.RunID),
	}
	mode := "sequential"
	if m.cfg.Jobs > 1 {
		mode = fmt.Sprintf("%d×worktree", m.cfg.Jobs)
	}
	left = append(left, styleInfo.Render(mode))
	if len(m.cfg.Dirs) == 1 {
		left = append(left, styleDim.Render(filepath.Base(m.cfg.Dirs[0])))
	} else {
		left = append(left, styleDim.Render(fmt.Sprintf("%d dirs", len(m.cfg.Dirs))))
	}

	loop := fmt.Sprintf("loop %d", m.loop)
	stateTxt, stateStyle := m.stateLabel()
	right := strings.Join([]string{
		styleDim.Render(loop),
		styleValue.Render(humanize.Duration(elapsed)),
		stateStyle.Render(stateTxt),
	}, "  ")
	return spread(strings.Join(left, "  "), right, m.w)
}

// stateLabel names the run's phase for the header and the small-terminal
// fallback, so both views tell the run's state the same way.
func (m *model) stateLabel() (string, lipgloss.Style) {
	switch {
	case m.done:
		return "● DONE", styleDim
	case m.reloading:
		return "● RELOADING", styleMagic
	case m.paused:
		return "‖ FEED PAUSED", styleWarn
	default:
		return "● RUNNING", styleOK
	}
}

func (m *model) activityTitle() string {
	// The current-value marker at the live edge is the eye anchor. A rate of
	// zero is a measurement; no samples yet is not, and the two must not look
	// the same.
	value := "n/a"
	cur := 0.0
	if len(m.activity) > 0 {
		cur = m.activity[len(m.activity)-1]
		value = fmtRate(cur)
		if cur <= 0 {
			value = "0"
		}
	}
	return "ACTIVITY " + styleDim.Render("agent lines/s") + "  " +
		lipgloss.NewStyle().Bold(true).Foreground(heatColor(clamp01(cur/50))).Render("◆ "+value)
}

// renderLanes draws one row per agent: what it is doing, how long it has been
// doing it, and its recent output rate. Columns are fixed so the eye learns
// where each number lives.
func (m *model) renderLanes(w, h int) string {
	nameW := 16
	if len(m.cfg.Dirs) > 1 {
		nameW = 24
	}
	const (
		revW     = 20
		meterW   = 12
		elapsedW = 7
		statsW   = 52
	)
	rows := make([]string, 0, h)
	// A lane that does not fit is announced, not dropped: an agent missing
	// from the panel reads as one that is not running.
	maxRows, hiddenLanes := h, len(m.laneOrd)-h
	if hiddenLanes > 0 {
		maxRows = h - 1
	}
	for _, label := range m.laneOrd {
		if len(rows) >= maxRows {
			break
		}
		l := m.lanes[label]
		hue := m.hues.get(label)

		var work string
		if l.review != "" {
			frac := 0.0
			if m.cfg.Timeout > 0 {
				frac = m.now.Sub(l.start).Seconds() / m.cfg.Timeout.Seconds()
			}
			work = pad(styleValue.Render(trim(l.review, revW)), revW) + " " +
				meter(frac, meterW, hue) + " " +
				pad(styleDim.Render(humanize.Duration(m.now.Sub(l.start))), elapsedW)
		} else {
			work = pad(styleDim.Render("idle"), revW) + " " +
				styleFaint.Render(strings.Repeat("▱", meterW)) + " " +
				strings.Repeat(" ", elapsedW)
		}

		tokens := humanize.Count(l.tokens + l.liveTokens)
		rate := ""
		if l.tokenRate > 0 {
			rate = "  " + styled(hue, fmtRate(l.tokenRate)+"/s")
		}
		// Reasoning: the share of output the model spent before writing
		// anything, and a marker while it is still spending it.
		think := ""
		if t := l.thinkTokens + l.liveThinking; t > 0 {
			think = "  " + styleThink.Render(thinkGlyph(m.now, l.lastThinkAt)+" "+humanize.Count(t))
		}
		stats := pad(fmt.Sprintf("%s done  %s fail  %s tok%s%s",
			styleValue.Render(fmt.Sprint(l.done)),
			failStyle(l.failed).Render(fmt.Sprint(l.failed)),
			styleDim.Render(tokens), rate, think), statsW)

		row := pad(styled(hue, trim(label, nameW)), nameW) + " " + work + "  " + stats
		if sparkW := w - lipgloss.Width(row) - 2; sparkW > 4 {
			row += "  " + sparkline(l.lines, sparkW)
		}
		rows = append(rows, clip(row, w))
	}
	if hiddenLanes > 0 {
		rows = append(rows, clip(styleFaint.Render(fmt.Sprintf("+%d more agents", hiddenLanes)), w))
	}
	return strings.Join(rows, "\n")
}

// thinkGlyph animates only while reasoning is actively growing: a still glyph
// means the agent thought earlier, a turning one means it is thinking now.
func thinkGlyph(now, last time.Time) string {
	if last.IsZero() || now.Sub(last) > 3*time.Second {
		return "◌"
	}
	frames := []string{"◐", "◓", "◑", "◒"}
	return frames[(now.UnixNano()/int64(200*time.Millisecond))%int64(len(frames))]
}

func failStyle(n int) lipgloss.Style {
	if n > 0 {
		return styleBad
	}
	return styleDim
}

func (m *model) gridTitle() string {
	c := m.counts
	title := fmt.Sprintf("REVIEWS  %s %s  %s %s  %s %s  %s %s  %s %s",
		styleOK.Render("pass"), styleValue.Render(fmt.Sprint(c["ok"])),
		styleBad.Render("fail"), styleValue.Render(fmt.Sprint(c["fail"])),
		styleWarn.Render("timeout"), styleValue.Render(fmt.Sprint(c["timeout"])),
		lipgloss.NewStyle().Foreground(cPeach).Render("conflict"), styleValue.Render(fmt.Sprint(c["conflict"])),
		styleDim.Render("skip"), styleValue.Render(fmt.Sprint(c["skipped"])))
	// Interrupted cells carry the ␘ glyph; once any exist, the tally says how
	// many, the way the summary's own conditional rows do.
	if n := c["interrupted"]; n > 0 {
		title += "  " + styleWarn.Render("interrupted") + " " + styleValue.Render(fmt.Sprint(n))
	}
	return title
}

// renderGrid draws every scheduled review as one cell: glyph plus short name,
// colored by status, so the whole set is legible at a glance. A pane too small
// for the whole set announces how many cells it dropped: a review missing
// without a word reads as one that was never scheduled.
func (m *model) renderGrid(w, h int) string {
	cols := m.gridCols()
	cellW := m.reviewCellWidth()
	names := append([]string(nil), m.order...)
	sort.Strings(names)

	capacity, hidden := cols*h, 0
	if extra := len(names) - capacity; extra > 0 {
		names = names[:capacity-1] // keep one slot for the marker
		hidden = extra
	}

	var rows []string
	var cur strings.Builder
	inRow := 0
	for _, name := range names {
		r := m.reviews[name]
		glyph, col := statusGlyph(r.status)
		short := trim(strings.TrimSuffix(name, "-review"), cellW-3)
		cell := styled(col, glyph) + " "
		switch r.status {
		case "running":
			cell += styled(m.hues.get(r.agentLbl), short)
		case "pending":
			cell += styleFaint.Render(short)
		default:
			cell += styled(col, short)
		}
		cur.WriteString(pad(cell, cellW))
		inRow++
		if inRow == cols {
			rows = append(rows, cur.String())
			cur.Reset()
			inRow = 0
		}
	}
	if hidden > 0 {
		more := styleFaint.Render(trim(fmt.Sprintf("+%d more", hidden), cellW-2))
		cur.WriteString(pad(more, cellW))
		inRow++
		if inRow == cols {
			rows = append(rows, cur.String())
			cur.Reset()
			inRow = 0
		}
	}
	if inRow > 0 {
		rows = append(rows, cur.String())
	}
	return strings.Join(rows, "\n")
}

// feedTitle keeps the reader oriented while scrolled back: nothing else on
// screen distinguishes reading history from pausing at the live edge.
func (m *model) feedTitle() string {
	if m.scroll <= 0 {
		return "FEED"
	}
	return "FEED  " + styleDim.Render(fmt.Sprintf("%d lines back", m.scroll))
}

func (m *model) renderFeed(w, h int) string {
	if len(m.feed) == 0 {
		return styleFaint.Render("waiting for agent output…")
	}
	end := len(m.feed) - m.scroll
	end = clampi(end, 1, len(m.feed))
	start := max(end-h, 0)
	visible := m.feed[start:end]

	rows := make([]string, 0, len(visible))
	for _, l := range visible {
		prefix := ""
		if l.review != "" {
			prefix = styled(m.hues.get(l.agent), pad(trim(l.review, 16), 16)) + styleFaint.Render(" │ ")
		}
		text := l.text
		if l.repeat > 1 {
			text += fmt.Sprintf(" (x%d)", l.repeat)
		}
		rows = append(rows, clip(prefix+lineStyle(l.kind).Render(text), w))
	}
	return strings.Join(rows, "\n")
}

func lineStyle(k normalize.Kind) lipgloss.Style {
	switch k {
	case normalize.DiffAdd:
		return styleOK
	case normalize.DiffDel:
		return styleBad
	case normalize.DiffMeta:
		return styleMagic
	case normalize.Thinking:
		return lipgloss.NewStyle().Foreground(cLavender).Italic(true)
	case normalize.Error:
		return styleBad
	case normalize.Result:
		return styleValue
	case normalize.Tool:
		return styleInfo
	case normalize.Progress:
		return styleFaint
	default:
		return styleDim
	}
}

func (m *model) renderFooter() string {
	keys := []struct{ k, d string }{
		{"q", "quit"}, {"space", "pause feed"}, {"j/k", "scroll"}, {"?", "help"},
	}
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(styleMagic.Render(k.k) + styleDim.Render(":"+k.d+"  "))
	}
	right := ""
	if m.haveLines {
		right += styleDim.Render(fmt.Sprintf("+%d/-%d lines  ", m.ins, m.del))
	}
	if m.tokens > 0 || m.liveRate > 0 {
		right += styleValue.Render(humanize.Count(m.tokens)) + styleDim.Render(" tok")
		if m.thinking > 0 && m.tokens > 0 {
			right += styleThink.Render(fmt.Sprintf("  ◌ %d%% think", 100*m.thinking/m.tokens))
		}
		switch {
		case m.liveRate > 0:
			// Measured from what the agents report as they stream.
			right += "  " + styleValue.Render(fmtRate(m.liveRate)) + styleDim.Render(" tok/s live")
		case m.agentTime >= time.Second && m.tokens > 0:
			right += styleDim.Render(fmt.Sprintf("  ~%s tok/s avg", fmtRate(float64(m.tokens)/m.agentTime.Seconds())))
		}
	}
	if m.cfg.Budget > 0 {
		frac := m.now.Sub(m.cfg.Started).Seconds() / m.cfg.Budget.Seconds()
		right += "  " + meter(frac, 10, cLavender) + styleDim.Render(" budget")
	}
	return spread(b.String(), right, m.w)
}

// renderMinimal is the small-terminal fallback. It keeps what answers "is it
// done, did anything break": the run state, elapsed time, the full tally, and
// the keys, because a fallback that hides how to quit is its own dead end.
func (m *model) renderMinimal() string {
	c := m.counts
	var tally strings.Builder
	tally.WriteString(fmt.Sprintf("pass %d  fail %d  timeout %d", c["ok"], c["fail"], c["timeout"]))
	for _, s := range []string{"skipped", "conflict", "interrupted"} {
		if n := c[s]; n > 0 {
			tally.WriteString(fmt.Sprintf("  %s %d", s, n))
		}
	}
	stateTxt, stateStyle := m.stateLabel()
	return strings.Join([]string{
		fmt.Sprintf("gauntlet %s  loop %d  %s  %s",
			m.cfg.Version, m.loop,
			styleDim.Render(humanize.Duration(m.now.Sub(m.cfg.Started))),
			stateStyle.Render(stateTxt)),
		tally.String(),
		styleDim.Render("terminal too small for the dashboard"),
		styleDim.Render("q quit  space pause feed  j/k scroll  ? help"),
	}, "\n")
}

func (m *model) renderHelp() string {
	lines := []string{
		styleTitle.Render("gauntlet dashboard"),
		"",
		"  q, esc      quit (stops the run)",
		"  space       pause the feed (output collects; reviews keep running)",
		"  j / k       scroll the feed",
		"  g / G       jump to oldest / newest",
		"  ?, h        toggle this help",
		"",
		styleDim.Render("  Review glyphs: · pending  ▸ running  ✓ ok  ✗ fail  ⧖ timeout  ⑂ merge conflict  – skipped  ␘ interrupted"),
	}
	if len(m.conflicts) > 0 {
		lines = append(lines, "", styleWarn.Render("  Unmerged branches:"))
		for _, c := range m.conflicts {
			lines = append(lines, "    "+c)
		}
	}
	return strings.Join(lines, "\n")
}

// spread lays left and right on one row, w columns wide.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return clip(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func pad(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// trim cuts s to at most w terminal cells, ellipsis included, cutting
// between grapheme clusters: names come from the reviewed repository and
// are neither always ASCII nor single-width.
func trim(s string, w int) string {
	return truncateWidth(s, w)
}

// truncateWidth is clip without the styling: at most w cells of s, cut
// between grapheme clusters, marked with an ellipsis when anything was cut.
func truncateWidth(s string, w int) string {
	if w <= 1 {
		return s
	}
	if uniseg.StringWidth(s) <= w {
		return s
	}
	var b strings.Builder
	visible := 0
	for _, tok := range widthTokens(s) {
		cw := uniseg.StringWidth(tok)
		if visible+cw > w-1 { // the cut reserves one cell for the ellipsis
			break
		}
		visible += cw
		b.WriteString(tok)
	}
	return b.String() + "…"
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// staticFrame renders one frame from a sequence of events, for tests and for
// non-interactive snapshots. It is the same code path the live view uses, so a
// layout regression shows up here too.
func staticFrame(cfg Config, events []runner.Event, w, h int) string {
	mod := newModel(cfg)
	mod.w, mod.h, mod.ready = w, h, true
	for _, ev := range events {
		mod.apply(ev)
	}
	mod.now = mod.cfg.Started.Add(90 * time.Second)
	mod.sampleActivity()
	return mod.View()
}
