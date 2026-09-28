// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Everything the dashboard puts on screen: the view, its panels, and the
// helpers that size and clip them. The state it draws and the loop that
// fills it live in ui.go; the two halves share one model type.

package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"

	"github.com/maci0/gauntlet/internal/envx"
	"github.com/maci0/gauntlet/internal/humanize"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runner"
)

func (m *model) View() string {
	if !m.ready {
		// Bubble Tea paints the first frame before the window size arrives, and
		// a blank alternate screen is a screen with nothing on it. The
		// launcher names the wait the same way.
		return "\n  gauntlet is starting…"
	}
	if m.help {
		return m.renderHelp()
	}
	// Four titled panels cost twelve rows of chrome around their content,
	// the header and footer one each, and the sections never shrink below
	// two activity, one agent, one grid, and three feed rows: twenty-one is
	// the smallest terminal the full view can actually hold. The guard takes
	// one row more than that, and below it the fallback answers what matters
	// instead of a frame with its bottom open.
	if m.w < 60 || m.h < 22 {
		return m.renderMinimal()
	}

	inner := m.w - 4 // panel border and padding
	actH, laneH, gridH, feedH := m.sectionHeights()

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		panel(m.activityTitle(), chart(m.activity, inner, actH), inner, actH),
		panel("AGENTS", m.renderLanes(inner, laneH), inner, laneH),
		panel(m.gridTitle(inner), m.renderGrid(inner, gridH), inner, gridH),
		panel(m.feedTitle(inner), m.renderFeed(inner, feedH), inner, feedH),
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
	cols := max(m.gridCols(), 1)
	gridRows := (len(m.order) + cols - 1) / cols
	grid = clampi(gridRows, 1, max(free-act-lanes-3, 1))
	feed = free - act - lanes - grid
	if feed < 3 {
		take := 3 - feed
		act = max(act-take, 2)
		feed = 3
	}
	// Whatever the four floors still overdraw comes out of the lanes: their
	// panel announces hidden agents, so fewer rows never hides one silently.
	// Without this the overflow runs the feed panel off the pane and leaves
	// its border open.
	if over := act + lanes + grid + feed - free; over > 0 {
		lanes = max(lanes-over, 1)
	}
	return act, lanes, grid, feed
}

// sortedOrder is the review order alphabetically, rebuilt only when a review
// is scheduled. The grid shows it every frame; sorting there would spend the
// frame budget re-deriving a constant.
func (m *model) sortedOrder() []string {
	if m.orderDirty {
		m.sorted = append(m.sorted[:0], m.order...)
		sort.Strings(m.sorted)
		m.orderDirty = false
	}
	return m.sorted
}

func (m *model) gridCols() int {
	cell := m.reviewCellWidth()
	return max((m.w-4)/cell, 1)
}

// reviewCellWidth is the width of one review cell: glyph, space, name. The
// longest name is measured in terminal cells, not bytes or runes, so a
// non-ASCII name budgets the space it will actually occupy. Names are fixed
// for the life of a run, so each is segmented once.
func (m *model) reviewCellWidth() int {
	longest := 8
	for _, n := range m.order {
		if w, ok := m.cellWidths[n]; ok {
			longest = max(longest, w)
			continue
		}
		w := uniseg.StringWidth(reviewShort(n))
		if m.cellWidths == nil {
			m.cellWidths = map[string]int{}
		}
		m.cellWidths[n] = w
		longest = max(longest, w)
	}
	return min(longest+4, 22)
}

func (m *model) renderHeader() string {
	elapsed := m.now.Sub(m.cfg.Started)
	mode := "sequential"
	if m.cfg.StackedPRs {
		mode = "stacked PRs"
	} else if m.cfg.Jobs > 1 {
		mode = fmt.Sprintf("%d×lane", m.cfg.Jobs)
	}
	loop := fmt.Sprintf("loop %d", m.loop)
	stateTxt, stateStyle := m.stateLabel()
	right := strings.Join([]string{
		styleDim.Render(loop),
		styleValue.Render(humanize.Duration(elapsed)),
		stateStyle.Render(stateTxt),
	}, "  ")

	// The right side is orientation: the loop, the elapsed time, and the run
	// state. Dim chrome yields to it piece by piece, so a narrow terminal
	// loses the version or the run id before it loses where the run stands.
	left := []string{wordmark()}
	fits := func(piece string) bool {
		joined := strings.Join(left, "  ") + "  " + piece
		return lipgloss.Width(joined)+lipgloss.Width(right)+2 <= m.w
	}
	for _, piece := range []string{
		styleDim.Render("v" + m.cfg.Version),
		styleDim.Render(m.cfg.RunID),
		styleInfo.Render(mode),
	} {
		if fits(piece) {
			left = append(left, piece)
		}
	}

	// The tree being reviewed, as a path rather than a basename: several
	// checkouts of one project share a basename, and a run is not the same run
	// in each of them. It takes the room the rest of the header leaves, and no
	// more: when there is none, it goes rather than pushing the state off the
	// line.
	if len(m.cfg.Dirs) == 1 {
		room := m.w - lipgloss.Width(strings.Join(left, "  ")) - lipgloss.Width(right) - 4
		if room >= 4 {
			left = append(left, styleDim.Render(dirLabel(m.cfg.Dirs[0], room)))
		}
	} else if fits(fmt.Sprintf("%d dirs", len(m.cfg.Dirs))) {
		left = append(left, styleDim.Render(fmt.Sprintf("%d dirs", len(m.cfg.Dirs))))
	}
	return spread(strings.Join(left, "  "), right, m.w)
}

// stateLabel names the run's phase for the header and the small-terminal
// fallback, so both views tell the run's state the same way.
func (m *model) stateLabel() (string, lipgloss.Style) {
	switch {
	case m.quitArmed:
		return "● q TO STOP", styleBad
	case m.done:
		return "● DONE", styleDim
	case m.finishing:
		return "● FINISHING", styleWarn
	case m.reloading:
		return "● RELOADING", styleInfo
	case m.paused:
		return "‖ FEED PAUSED", styleWarn
	default:
		return "● RUNNING", styleOK
	}
}

// logKind classifies runner narration for the feed filter. Failures and the
// "how to land it" hint after a conflict have to survive the narrowed feed, or
// the lines a reader narrowed the feed to see disappear.
func logKind(text string) normalize.Kind {
	switch {
	case strings.HasPrefix(text, "MERGE CONFLICT"),
		strings.HasPrefix(text, "MERGE FAILED"),
		strings.HasPrefix(text, "FAILED"),
		strings.HasPrefix(text, "TIMEOUT"),
		strings.HasPrefix(text, "STACK STOPPED"),
		strings.HasPrefix(text, "Cannot "),
		strings.HasPrefix(text, "Interrupted"),
		strings.HasPrefix(text, "Warning:"),
		strings.HasPrefix(text, "Conflict step"),
		strings.HasPrefix(text, "To land it"),
		strings.Contains(text, " failed"):
		return normalize.Error
	}
	return normalize.Plain
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
	// The live-edge marker is text, so a resting or idle chart shows it dim
	// but legible; only a real measurement rides the heat ramp. Any active rate
	// clears the 4.5:1 text floor (WCAG 1.4.3), starting at teal rather than
	// the unlit track tone.
	var fg lipgloss.TerminalColor = cDim
	if cur > 0 {
		fg = heatColor(clamp01(cur / activityRateFull))
		if fg == cTrack {
			fg = cTeal
		}
	}
	return "ACTIVITY " + styleDim.Render("agent lines/s") + "  " +
		lipgloss.NewStyle().Bold(true).Foreground(fg).Render("◆ "+value)
}

// renderLanes draws one row per agent: what it is doing, how long it has been
// doing it, and its recent output rate. Columns are fixed so the eye learns
// where each number lives.
func (m *model) renderLanes(w, h int) string {
	nameW := 16
	if len(m.cfg.Dirs) > 1 {
		nameW = 24
	}
	revW := 20
	meterW := 12
	elapsedW, statsW := 7, 52
	if w < 90 {
		revW = 14
		meterW = 8
		if len(m.cfg.Dirs) == 1 {
			nameW = 12
		}
	}
	if w < 74 {
		// The narrowest tier gives the fixed columns room for the counters,
		// which are what the panel is read for. The meter is the first thing
		// to lose width: it reads from its own length, so a shorter one is
		// still a correct meter, just a smaller one.
		nameW, revW, meterW, elapsedW, statsW = 10, 12, 6, 5, 0
	}
	rows := make([]string, 0, h)
	// A lane that does not fit is announced, not dropped: an agent missing
	// from the panel reads as one that is not running.
	//
	// The marker occupies one of the h rows, so what stays hidden is
	// everything past the rows actually drawn -- renderGrid does the same
	// arithmetic for its own "+N more" cell and says so. Counting against h
	// instead undercounts by exactly one, which drops an agent from the very
	// tally that exists so none is dropped.
	maxRows := h
	if len(m.laneOrd) > maxRows {
		maxRows = h - 1
	}
	hiddenLanes := max(len(m.laneOrd)-maxRows, 0)
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
			name := reviewShort(l.review)
			if l.attempt > 1 {
				// A retry looks like a restart otherwise: same review, same
				// lane, clock back to zero.
				name = fmt.Sprintf("%s ↻%d", name, l.attempt)
			}
			work = pad(styleValue.Render(trim(name, revW)), revW) + " " +
				meter(frac, meterW, hue) + " " +
				pad(styleDim.Render(trim(humanize.Duration(m.now.Sub(l.start)), elapsedW)), elapsedW)
		} else {
			work = pad(styleDim.Render("idle"), revW) + " " +
				styleTrack.Render(strings.Repeat("▱", meterW)) + " " +
				strings.Repeat(" ", elapsedW)
		}

		prefix := pad(styled(hue, trim(label, nameW)), nameW) + " " + work + "  "
		// The counters are built by priority into the room the prefix leaves,
		// so a narrow pane leaves out the reasoning share whole rather than
		// cutting its token count to "◌ 11": a number that reads as a
		// measurement and is not one is worse than no number.
		statW := w - lipgloss.Width(prefix) - 2
		statLine, dropped := laneStats(m, l, hue, statW)
		// The counters keep a fixed column wherever there is room for it, so
		// the eye learns where each number lives and the sparkline starts at
		// the same place on every lane. statsW is the column budget, statW
		// the room the prefix left; the lane takes the smaller, never a
		// negative one.
		statCol := min(statsW, max(statW, 0))
		// laneStats only guarantees that a whole segment fits the room statW
		// claims. A prefix wider than the pane leaves less than that, and the
		// clip below would then cut the first segment mid-word, turning "3
		// done" into a bare count that reads as a measurement. Drop the
		// counters instead of showing a partial one.
		row := prefix
		if lipgloss.Width(statLine) <= statW {
			row += pad(statLine, statCol)
			if !dropped {
				if sparkW := statW - statCol; sparkW > 4 {
					row += "  " + chart(l.lines, sparkW, 1)
				}
			}
		}
		rows = append(rows, clip(row, w))
	}
	if hiddenLanes > 0 {
		rows = append(rows, clip(styleFaint.Render(fmt.Sprintf("+%d more agents", hiddenLanes)), w))
	}
	return strings.Join(rows, "\n")
}

// laneStats is one lane's counters, most important first: what finished, what
// broke, what it cost, then the measured rate, then the reasoning share. A
// segment that does not fit is left out whole, and dropped says so, because a
// count cut to a few digits is a false reading rather than a missing one.
func laneStats(m *model, l *laneState, hue lipgloss.AdaptiveColor, w int) (string, bool) {
	segs := []string{
		styleValue.Render(fmt.Sprint(l.done)) + styleDim.Render(" done"),
		failStyle(l.failed).Render(fmt.Sprint(l.failed)) + styleDim.Render(" fail"),
		styleDim.Render(humanize.Count(l.tokens+l.liveTokens)) + styleDim.Render(" tok"),
	}
	if l.tokenRate > 0 {
		segs = append(segs, styled(hue, fmtRate(l.tokenRate)+"/s"))
	}
	// Reasoning: the share of output the model spent before writing anything,
	// and a marker while it is still spending it. It is drawn subordinate to
	// the counters, so it is the first one a narrow pane gives up.
	if t := l.thinkTokens + l.liveThinking; t > 0 {
		segs = append(segs, styleThink.Render(thinkGlyph(m.now, l.lastThinkAt)+" "+humanize.Count(t)))
	}
	out := segs[0]
	for _, s := range segs[1:] {
		if lipgloss.Width(out)+2+lipgloss.Width(s) > w {
			return out, true
		}
		out += "  " + s
	}
	return out, false
}

// thinkingStill is how long after the last growth in reasoning the glyph stops
// turning, and thinkingFrame is how fast it turns while it does. Long enough
// that a pause between tokens does not stop it, short enough that a finished
// thought does.
const (
	thinkingStill = 3 * time.Second
	thinkingFrame = 200 * time.Millisecond
)

// motionStill is the frame a frozen reasoning glyph holds: mid-turn, from the
// same set the animation uses, so "thinking now" stays readable without
// anything moving.
const motionStill = "◐"

// The names the motion accommodation is read from, in the order they are
// consulted. Project-specific first: it is the one a user of this tool sets
// deliberately, and an explicit false there has to be able to overrule a
// REDUCED_MOTION inherited from a desktop session.
const (
	envNoAnimation   = "GAUNTLET_NO_ANIMATION"
	envNoMotion      = "NO_MOTION"
	envReducedMotion = "REDUCED_MOTION"
)

// motionOff reports whether the run asked for a still screen. Terminals have
// no prefers-reduced-motion, so the accommodation is GAUNTLET_NO_ANIMATION,
// NO_MOTION, or REDUCED_MOTION: anything but empty, "0", "false", "no", or "off"
// freezes the dashboard's one animated glyph, whose cycling otherwise starts
// on its own and outlives five seconds of reasoning (WCAG 2.2.2: such motion
// must be stoppable).
//
// The values that mean off come from envx, the same reader the color-force
// variables go through: the list is stated once in docs/CLI.md, and a second
// copy here is one that answers differently the moment one of them is edited.
func motionOff() bool {
	for _, env := range []string{envNoAnimation, envNoMotion, envReducedMotion} {
		v, set := os.LookupEnv(env)
		if !set {
			continue
		}
		if envx.On(v) {
			return true
		}
		// An explicit false on the project-specific name wins over the two
		// standard ones. An empty value states nothing and defers to them,
		// which is what an unset variable in a template means.
		if env == envNoAnimation && strings.TrimSpace(v) != "" {
			return false
		}
	}
	return false
}

// thinkGlyph animates only while reasoning is actively growing: a still glyph
// means the agent thought earlier, a turning one means it is thinking now.
// Under GAUNTLET_NO_ANIMATION the turning glyph holds one frame instead, so
// the state it carries survives the freeze.
func thinkGlyph(now, last time.Time) string {
	if last.IsZero() || now.IsZero() || now.Before(last) || now.Sub(last) > thinkingStill {
		return "◌"
	}
	if motionOff() {
		return motionStill
	}
	frames := []string{"◐", "◓", "◑", "◒"}
	// UnixNano is negative before 1970, and Go's remainder keeps the
	// dividend's sign, so a pre-epoch now would index frames at -1 and panic.
	i := now.UnixNano() / int64(thinkingFrame) % int64(len(frames))
	if i < 0 {
		i += int64(len(frames))
	}
	return frames[i]
}

func failStyle(n int) lipgloss.Style {
	if n > 0 {
		return styleBad
	}
	return styleDim
}

// otherCount is how many reviews ended in a status this build does not name.
// The tally is keyed by the raw status string, so a status a newer binary
// invented (a run continued across a hot reload) sits in a key nothing reads
// and the row reads as a clean sweep of fewer reviews than ran. The empty
// status is not one of them: a review_end with none is publication metadata
// recovered without launching an agent, which is no outcome to count.
func (m *model) otherCount() int {
	n := 0
	for status, c := range m.counts {
		if c <= 0 || status == "" {
			continue
		}
		switch runner.Status(status) {
		case runner.StatusOK, runner.StatusFail, runner.StatusTimeout,
			runner.StatusSkipped, runner.StatusInterrupted, runner.StatusConflict:
		default:
			n += c
		}
	}
	return n
}

func (m *model) gridTitle(w int) string {
	c := m.counts
	segs := []string{"REVIEWS",
		styleOK.Render("pass") + " " + styleValue.Render(fmt.Sprint(c["ok"])),
		styleBad.Render("fail") + " " + styleValue.Render(fmt.Sprint(c["fail"])),
		styleWarn.Render("timeout") + " " + styleValue.Render(fmt.Sprint(c["timeout"])),
		lipgloss.NewStyle().Foreground(cPeach).Render("conflict") + " " + styleValue.Render(fmt.Sprint(c["conflict"])),
		styleDim.Render("skip") + " " + styleValue.Render(fmt.Sprint(c["skipped"])),
	}
	// Interrupted cells carry the ␘ glyph; once any exist, the tally says how
	// many, the way the summary's own conditional rows do.
	if n := c["interrupted"]; n > 0 {
		segs = append(segs, styleWarn.Render("interrupted")+" "+styleValue.Render(fmt.Sprint(n)))
	}
	// Likewise an outcome the tally cannot name: a row that hid a review
	// would report a run shorter than the one that happened.
	if n := m.otherCount(); n > 0 {
		segs = append(segs, styleDim.Render("other")+" "+styleValue.Render(fmt.Sprint(n)))
	}
	return fitTitle(segs, w)
}

// fitTitle lays a panel title's segments on one row, dropping whole ones from
// the right and marking what went. A title cut mid-word ("3 lin…") names a
// reading that does not exist, and the panels a narrow terminal squeezes are
// the ones carrying the most state, so the least important segment goes before
// any word is broken. The first segment always stays: it is the panel's name.
func fitTitle(segs []string, w int) string {
	out := segs[0]
	dw := lipgloss.Width(out)
	for _, s := range segs[1:] {
		sw := lipgloss.Width(s)
		if dw+2+sw > w {
			if dw+3 <= w {
				out += "  " + styleFaint.Render("…")
			}
			return out
		}
		out += "  " + s
		dw += 2 + sw
	}
	return out
}

// renderGrid draws every scheduled review as one cell: glyph plus short name,
// colored by status, so the whole set is legible at a glance. A pane too small
// for the whole set announces how many cells it dropped: a review missing
// without a word reads as one that was never scheduled.
func (m *model) renderGrid(w, h int) string {
	cellW := m.reviewCellWidth()
	cols := max(w/cellW, 1)
	names := m.sortedOrder()

	capacity, hidden := cols*h, 0
	if extra := len(names) - capacity; extra > 0 {
		// One slot goes to the marker itself, so what stays hidden is
		// everything beyond the capacity-1 cells drawn.
		names = names[:capacity-1]
		hidden = extra + 1
	}

	var rows []string
	var cur strings.Builder
	inRow := 0
	for _, name := range names {
		r := m.reviews[name]
		glyph, col := statusGlyph(r.status)
		short := trim(reviewShort(name), cellW-3)
		cell := styled(col, glyph) + " "
		switch r.status {
		case statusRunning:
			cell += styled(m.hues.get(r.agentLbl), short)
		case statusPending:
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

// visibleFeed is the feed as the current filter leaves it. The filter never
// drops a line from the model: widening it brings the history back. The view
// is rebuilt when the feed or the filter changes, not each frame: at ten
// frames a second the scan would re-read every retained line for the same
// answer.
func (m *model) visibleFeed() []feedLine {
	var feed []feedLine
	if m.filter == feedAll {
		feed = m.feed
	} else if !m.feedDirty {
		feed = m.feedView
	} else {
		m.feedView = m.feedView[:0]
		for _, l := range m.feed {
			if m.filter.keep(l) {
				m.feedView = append(m.feedView, l)
			}
		}
		m.feedDirty = false
		feed = m.feedView
	}
	if maxBack := len(feed) - 1; m.scroll > maxBack {
		m.scroll = max(maxBack, 0)
	}
	return feed
}

// feedTitle keeps the reader oriented while scrolled back or narrowed:
// nothing else on screen distinguishes reading history from pausing at the
// live edge, or a quiet feed from a filtered one. w is the row the title has to
// fit in, so a narrow pane drops a whole reading rather than cutting one.
func (m *model) feedTitle(w int) string {
	segs := []string{"FEED"}
	if m.paused {
		segs = append(segs, styleWarn.Render("paused"))
	}
	if l := m.filter.label(); l != "" {
		segs = append(segs, styleInfo.Render(l))
	}
	if n := len(m.conflicts); n > 0 {
		segs = append(segs, styleWarn.Render(fmt.Sprintf("%d unmerged", n)))
	}
	if m.scroll > 0 {
		segs = append(segs, styleDim.Render(fmt.Sprintf("%d lines back", m.scroll)))
	}
	return fitTitle(segs, w)
}

func (m *model) renderFeed(w, h int) string {
	feed := m.visibleFeed()
	if len(feed) == 0 {
		if len(m.feed) > 0 {
			return styleFaint.Render("nothing matches this filter yet (f to widen it)")
		}
		return styleFaint.Render("waiting for agent output…")
	}
	end := len(feed) - m.scroll
	end = clampi(end, 1, len(feed))
	start := max(end-h, 0)
	visible := feed[start:end]

	rows := make([]string, 0, len(visible))
	for _, l := range visible {
		prefix := ""
		if l.review != "" {
			prefix = styled(m.hues.get(l.agent), pad(trim(reviewShort(l.review), 16), 16)) + styleFaint.Render(" │ ")
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

// renderFooter lays the key legend against the run's readings.
//
// The legend is placed first and holds its ground: the keys that keep a reader
// oriented (quit, help, pause) outlast the ones only the data hungry need, and
// they are the only place on the screen that names the keys at all. The
// readings take what is left, a whole segment at a time, and a line that could
// not hold even the first says so rather than going blank: a tally that
// disappears reads as a zero. The full pause semantics live in help; here one
// word.
func (m *model) renderFooter() string {
	legend := m.footerLegend(m.w)
	room := m.w - lipgloss.Width(legend)
	if lipgloss.Width(legend) > 0 {
		room -= 2
	}
	return spread(legend, fitRight(m.footerReadings(), room), m.w)
}

// footerLegend is the key legend as whole segments fitted to avail columns. The
// first segment always survives: without it a reader has no way to leave.
//
// The running width is carried rather than re-measured, the way renderMinimal
// fits its own hint: lipgloss.Width walks the text grapheme by grapheme, and
// measuring the accumulated legend on every step re-walked it once per key.
func (m *model) footerLegend(avail int) string {
	var b strings.Builder
	w := 0
	for _, k := range m.footerKeys(true) {
		seg := styleValue.Render(k.k) + styleDim.Render(":"+k.d)
		segW := lipgloss.Width(seg)
		if w > 0 && w+2+segW > avail {
			break
		}
		if w > 0 {
			b.WriteString("  ")
			w += 2
		}
		b.WriteString(seg)
		w += segW
	}
	return b.String()
}

// fitRight lays the reading segments into room, dropping whole ones from the
// right end. Whatever did not fit is marked, so a figure that fell off the
// line is visibly absent rather than reading as a run that measured nothing.
func fitRight(segs []string, room int) string {
	if len(segs) == 0 || room <= 0 {
		return ""
	}
	out := segs[0]
	w := lipgloss.Width(out)
	if w > room {
		return styleFaint.Render("…")
	}
	dropped := false
	for _, s := range segs[1:] {
		sw := lipgloss.Width(s)
		if w+2+sw > room {
			dropped = true
			break
		}
		out += "  " + s
		w += 2 + sw
	}
	// The marker needs a column of its own: a run with a budget meter that did
	// not fit says so rather than letting the line end as if the budget were
	// the last thing it had to report.
	if dropped && w+3 <= room {
		out += "  " + styleFaint.Render("…")
	}
	return out
}

// footerReadings is the run's own tally, most important first: the diff, the
// tokens, the reasoning share, the rate, then how far through the budget the
// run is.
func (m *model) footerReadings() []string {
	var segs []string
	if m.haveLines {
		segs = append(segs, styleDim.Render(fmt.Sprintf("+%d/-%d lines", m.ins, m.del)))
	}
	if m.tokens > 0 || m.liveRate > 0 {
		segs = append(segs, styleValue.Render(humanize.Count(m.tokens))+styleDim.Render(" tok"))
		if m.thinking > 0 && m.tokens > 0 {
			pct := humanize.Share(m.thinking, m.tokens)
			segs = append(segs, styleThink.Render("◌ "+fmt.Sprint(pct)+"% think"))
		}
		switch {
		case m.liveRate > 0:
			// Measured from what the agents report as they stream.
			segs = append(segs, styleValue.Render(fmtRate(m.liveRate))+styleDim.Render(" tok/s live"))
		case m.agentTime >= time.Second && m.tokens > 0:
			segs = append(segs, styleDim.Render("~"+fmtRate(float64(m.tokens)/m.agentTime.Seconds())+" tok/s avg"))
		}
	}
	if m.cfg.Budget > 0 {
		frac := m.now.Sub(m.cfg.Started).Seconds() / m.cfg.Budget.Seconds()
		segs = append(segs, meter(frac, 10, heatColor(frac))+styleDim.Render(" budget"))
	}
	return segs
}

// renderMinimal is the small-terminal fallback. It keeps what answers "is it
// done, did anything break": the run state, elapsed time, the full tally, and
// the keys, because a fallback that hides how to quit is its own dead end.
// Every row clips to the pane: a narrow terminal wraps what it cannot hold,
// and a wrapped fallback is a taller broken screen, not a smaller one.
func (m *model) renderMinimal() string {
	c := m.counts
	var tally strings.Builder
	tally.WriteString(fmt.Sprintf("pass %d  fail %d  timeout %d", c["ok"], c["fail"], c["timeout"]))
	for _, s := range []string{"skipped", "conflict", "interrupted"} {
		if n := c[s]; n > 0 {
			tally.WriteString(fmt.Sprintf("  %s %d", s, n))
		}
	}
	if n := m.otherCount(); n > 0 {
		tally.WriteString(fmt.Sprintf("  other %d", n))
	}
	stateTxt, stateStyle := m.stateLabel()
	// The keys degrade the way the launcher's footer does: whole segments
	// drop from the right rather than clipping mid-word, and the ones that
	// keep a reader oriented (quit, help, pause) come first. Only keys this
	// view can show the effect of are listed: scroll acts on a feed the
	// fallback does not draw, so advertising it would name a dead key.
	var hint strings.Builder
	hintW := 0
	for _, k := range m.footerKeys(false) {
		seg := k.k + " " + k.d
		segW := uniseg.StringWidth(seg)
		if hintW > 0 && hintW+2+segW > m.w {
			break
		}
		if hintW > 0 {
			hint.WriteString("  ")
			hintW += 2
		}
		hint.WriteString(seg)
		hintW += segW
	}
	rows := []string{
		m.minimalHeader(stateTxt, stateStyle),
		tally.String(),
	}
	if len(m.conflicts) > 0 {
		rows = append(rows, styleWarn.Render("unmerged: "+strings.Join(m.conflicts, ", ")))
	}
	var active []string
	for _, label := range m.laneOrd {
		if l := m.lanes[label]; l != nil && l.review != "" {
			active = append(active, fmt.Sprintf("%s: %s", label, reviewShort(l.review)))
		}
	}
	if len(active) > 0 {
		rows = append(rows, styleInfo.Render("running: "+strings.Join(active, ", ")))
	}
	rows = append(rows,
		styleDim.Render("terminal too small for the dashboard"),
		styleDim.Render(hint.String()),
	)
	if m.h > 0 && len(rows) > m.h {
		// The key line is the last row and is the one that stays: a terminal
		// three rows tall trimmed from the bottom is a tally and no way to
		// leave, which is the dead end the fallback exists to avoid.
		rows = append(rows[:max(m.h-1, 0):len(rows)-1], rows[len(rows)-1])
	}
	for i, r := range rows {
		rows[i] = clipEllipsis(r, m.w)
	}
	return strings.Join(rows, "\n")
}

// minimalHeader is the fallback's first row, laid from the left inward: the
// run state and the clock are what the fallback exists to report, so they are
// the last pieces standing at any width and the version and the loop number
// are what a narrow terminal gives up first. A row cut at the right instead
// took the state with it ("‖ FEED…"), which is the one reading the reader
// came for.
func (m *model) minimalHeader(stateTxt string, stateStyle lipgloss.Style) string {
	tail := []string{
		styleDim.Render(humanize.Duration(m.now.Sub(m.cfg.Started))),
		stateStyle.Render(stateTxt),
	}
	lead := []string{
		styleDim.Render("gauntlet " + m.cfg.Version),
		styleDim.Render(fmt.Sprintf("loop %d", m.loop)),
	}
	for i := 0; i <= len(lead); i++ {
		row := strings.Join(append(append([]string{}, lead[i:]...), tail...), "  ")
		if i == len(lead) || lipgloss.Width(row) <= m.w {
			return row
		}
	}
	return strings.Join(tail, "  ")
}

func (m *model) renderHelp() string {
	return renderHelpPage(m.helpLines(), m.helpScroll, m.w, m.h)
}

func (m *model) helpLines() []string {
	// Close keys first: while this overlay is up, q and esc close it, they
	// do not stop the run. Listing quit first is how a reader kills a run
	// they opened help to understand.
	lines := []string{
		styleTitle.Render("gauntlet dashboard"),
		styleDim.Render("q  esc  ?  close this help"),
		"",
	}
	if len(m.conflicts) > 0 {
		lines = append(lines, styleWarn.Render("Unmerged branches (kept for you to merge):"))
		for _, c := range m.conflicts {
			lines = append(lines, "  "+c)
		}
		lines = append(lines, "")
	}
	qLine := "  q           stop the run, killing what is running (press twice; esc cancels)"
	if m.done {
		qLine = "  q, esc, enter close (the run has finished)"
	} else if m.finishing {
		qLine = "  q, esc      stop now (a finish is already draining)"
	}
	lines = append(lines, qLine)
	// A run with nothing to finish into does not get the key documented:
	// ctrl+c would fall through to the hard quit, and s would do nothing.
	if m.cfg.OnFinish != nil {
		lines = append(lines,
			"  s, ctrl+c   finish: no new reviews, then commit, publish or merge, and exit",
			"  ctrl+c x2   quit while a finish is draining (stops the run)",
		)
	}
	lines = append(lines,
		"  esc         cancel quit confirmation, or reset paused/scrolled feed to live",
		"  space       pause the feed (output collects; reviews keep running)",
		"  j / k       scroll the feed (or pgup / pgdn)",
		"  g / G       jump to oldest / newest (home / end)",
		"  f           narrow the feed to results, errors, and diffs, and back",
		"  ?, h        toggle this help",
		"",
		styleDim.Render("  Review glyphs: · pending  ▸ running  ✓ ok  ✗ fail  ⧖ timeout  ⑂ merge conflict  – skipped  ␘ interrupted"),
	)
	return lines
}

// footerKeys is the key legend for the full footer and the small-terminal
// fallback. Labels follow the current state so a paused feed says resume, a
// finished run says close, and a dead action (finish after the run ended) is
// not advertised. scrollable is false on the fallback, which has no feed.
func (m *model) footerKeys(scrollable bool) []struct{ k, d string } {
	if m.quitArmed {
		return []struct{ k, d string }{
			{"q", "stop now"}, {"esc", "cancel"}, {"?", "help"},
		}
	}
	q := "quit"
	switch {
	case m.done:
		q = "close"
	case m.finishing:
		q = "stop now"
	}
	keys := []struct{ k, d string }{
		{"q", q}, {"?", "help"},
	}
	// Pausing holds a feed, and the fallback draws none: the key there would
	// flip the state label to a feed the reader cannot see. A feed that is
	// already paused still says so, and that is the state label's work.
	if !m.done && scrollable {
		space := "pause"
		if m.paused {
			space = "resume"
		}
		keys = append(keys, struct{ k, d string }{"space", space})
	}
	if scrollable {
		keys = append(keys, struct{ k, d string }{"j/k", "scroll"})
	}
	// A paused or scrolled feed is named in the header, which the fallback
	// draws too, so the way back to live is named there as well: a state the
	// screen reports and nothing on it can clear is a dead end the reader has
	// to guess out of.
	if m.paused || m.scroll > 0 {
		keys = append(keys, struct{ k, d string }{"esc", "live"})
	}
	if !m.done && !m.finishing && m.cfg.OnFinish != nil {
		keys = append(keys, struct{ k, d string }{"s", "finish"})
	}
	if scrollable {
		f := "filter"
		if m.filter == feedSignal {
			f = "widen"
		}
		keys = append(keys, struct{ k, d string }{"f", f})
	}
	return keys
}

// clipBlock fits lines into a w by h pane the way the small-terminal fallback
// does: each row clips to the width, extra rows are dropped from the bottom.
func clipBlock(lines []string, w, h int) string {
	if h > 0 && len(lines) > h {
		lines = lines[:h]
	}
	if w > 0 {
		for i, ln := range lines {
			lines[i] = clipEllipsis(ln, w)
		}
	}
	return strings.Join(lines, "\n")
}

func helpRows(lines []string, w int) []string {
	return strings.Split(lipgloss.NewStyle().Width(max(w, 1)).Render(strings.Join(lines, "\n")), "\n")
}

// renderHelpPage draws the overlay from its scroll position over a wrapped
// copy of the help text, the visible page ending at the bottom edge, padded
// so the frame stays one block. The last row is the overlay's own key legend:
// closing and scrolling stay named whatever the pane shows, so a reader on
// page one always knows how to reach the rest (WCAG 2.1.1: help content the
// pane cuts must stay reachable by keyboard).
func renderHelpPage(lines []string, scroll, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	rows := helpRows(lines, w)
	viewport := max(h-1, 1)
	start := clampi(scroll, 0, max(len(rows)-viewport, 0))
	out := append([]string{}, rows[start:min(len(rows), start+viewport)]...)
	for len(out) < viewport {
		out = append(out, "")
	}
	out = append(out, styleDim.Render("q/esc close  j/k scroll  pgup/pgdn  home/end"))
	return clipBlock(out, w, h)
}

// scrollHelp moves the overlay's scroll position for one keypress, bounded so
// the last help row can sit at the bottom edge. Arrows and paging keys reuse
// the dashboard's feed bindings, so help answers the keys its own legend
// already names; other keys leave the overlay untouched, and closing it
// resets the position.
func scrollHelp(scroll int, key string, lines []string, w, h int) int {
	viewport := max(h-1, 1)
	bound := max(len(helpRows(lines, w))-viewport, 0)
	scroll = clampi(scroll, 0, bound)
	switch key {
	case "up", "k":
		scroll--
	case "down", "j":
		scroll++
	case "home", "g":
		scroll = 0
	case "end", "G":
		scroll = bound
	case "pgup", "pageup", "b":
		scroll -= max(viewport-1, 1)
	case "pgdown", "pagedown", "space":
		scroll += max(viewport-1, 1)
	}
	return clampi(scroll, 0, bound)
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
// are neither always ASCII nor single-width. It is clip without the styling.
func trim(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
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
	return min(max(v, lo), hi)
}
