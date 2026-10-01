// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
)

// Palette: Catppuccin Mocha on dark terminals, Latte on light ones. Every
// color that can sit behind text ships as an adaptive pair, and the pairs are
// pinned by test: body, secondary, de-emphasized, and status text clears
// 4.5:1 (WCAG 2.2 AA SC 1.4.3) and instrument strokes clear 3:1 (SC 1.4.11)
// against the base they are drawn on. Borders are decorative chrome carrying
// no information, so they alone are exempt.
var (
	cText  = adaptive("#4c4f69", "#cdd6f4")
	cDim   = adaptive("#6a6d82", "#9399b2")
	cFaint = adaptive("#6b6d7b", "#84889f")
	// cTrack draws the unlit remainder of every meter and the chart's
	// baseline, so it sits between the 3:1 non-text floor and the 4.5:1 text
	// floor on purpose: a step visible enough to read as a stroke, faint
	// enough to read as empty. The light tone is not the 3.00:1 the palette
	// floor lands on exactly, because a stroke with no margin above the floor
	// is one rounding step from failing it.
	cTrack    = adaptive("#7f8391", "#6c7086")
	cBorder   = adaptive("#acb0be", "#45475a")
	cRed      = adaptive("#d20f39", "#f38ba8")
	cGreen    = adaptive("#327d22", "#a6e3a1")
	cYellow   = adaptive("#996214", "#f9e2af")
	cPeach    = adaptive("#bc4a08", "#fab387")
	cBlue     = adaptive("#1d64ef", "#89b4fa")
	cCyan     = adaptive("#0376a3", "#89dceb")
	cTeal     = adaptive("#137a80", "#94e2d5")
	cMagenta  = adaptive("#8839ef", "#cba6f7")
	cPink     = adaptive("#a2528c", "#f5c2e7")
	cLavender = adaptive("#5767c2", "#b4befe")
	// cMark is the path-arrow in assets/mark.svg. Dark is that ink as drawn
	// (#0e96a8). Light is the same hue pulled darker until it clears 4.5:1
	// on Latte. Distinct from cTeal, which stays the Catppuccin instrument
	// tone used in the heat ramp and the agent rotation.
	cMark = adaptive("#0b7988", "#0e96a8")
)

// adaptive pairs a Latte tone for light backgrounds with its Mocha
// counterpart, resolved per terminal at render time.
func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

var (
	// Panel names are chrome: live values inside the title stay independently
	// colored, and the label itself stays dim so it never competes with them.
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(cDim)
	styleDim   = lipgloss.NewStyle().Foreground(cDim)
	styleFaint = lipgloss.NewStyle().Foreground(cFaint)
	styleTrack = lipgloss.NewStyle().Foreground(cTrack)
	styleValue = lipgloss.NewStyle().Bold(true).Foreground(cText)
	styleOK    = lipgloss.NewStyle().Foreground(cGreen)
	styleWarn  = lipgloss.NewStyle().Foreground(cYellow)
	styleBad   = lipgloss.NewStyle().Foreground(cRed)
	styleInfo  = lipgloss.NewStyle().Foreground(cCyan)
	// Magenta is for diff hunk headers, which already read as "meta" in a
	// unified diff. It is not a chrome accent: keys, titles, and status
	// use the dim/value/info styles the launcher already uses.
	styleMagic = lipgloss.NewStyle().Foreground(cMagenta)
	// Reasoning is real output, but it is not the answer: lavender keeps it
	// legible while visibly subordinate to the text the agent actually wrote,
	// and the italic is what marks it as the agent thinking out loud in every
	// view, so both decisions live here and nowhere else.
	styleThink = lipgloss.NewStyle().Foreground(cLavender).Italic(true)
	// A merge conflict is the one tally a run cannot resolve on its own, and
	// it is the only label in the review tally without a status token of its
	// own: none of the four status styles means "a branch needs a hand".
	styleConflict = lipgloss.NewStyle().Foreground(cPeach)

	// panelStyle is the frame panel used to draw and no longer does: it is the
	// library's own border, padding included, and it is kept as the oracle
	// TestPanelEqualsLipglossFrame measures the hand-drawn frame against.
	// Nothing renders with it.
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
)

// agentHues is the fixed rotation for per-agent color. One concept, one hue:
// an agent keeps its color in its lane, its review rows, and its feed lines.
var agentHues = []lipgloss.AdaptiveColor{cBlue, cPeach, cTeal, cMagenta, cPink, cYellow, cCyan, cLavender, cGreen}

// brandHues give the agents whose vendor has a recognizable color that color,
// so a lane is identifiable before its name is read. Each is the brand hue
// pulled toward its background until it clears the same 4.5:1 text floor as
// every other token here: a brand is a hint, never a reason to ship
// unreadable text. Agents whose vendor has no such color, or whose color
// would be another one of these, keep the rotation.
var brandHues = map[string]lipgloss.AdaptiveColor{
	"claude": adaptive("#a8471f", "#e08a63"), // Anthropic terracotta
	"codex":  adaptive("#0a6b53", "#3fcfa6"), // OpenAI green
	"gemini": adaptive("#1a56c4", "#7aa9ff"), // Google blue
	"qwen":   adaptive("#5b21b6", "#c4a7fb"), // Qwen violet
	"grok":   adaptive("#3a3a3a", "#e4e4e7"), // xAI monochrome
}

// brandHue returns the vendor color for an agent label ("claude",
// "codex:gpt-5", "claude@xhigh"), and reports whether there is one.
func brandHue(label string) (lipgloss.AdaptiveColor, bool) {
	tool, _, _ := strings.Cut(label, ":")
	tool, _, _ = strings.Cut(tool, "@")
	c, ok := brandHues[tool]
	return c, ok
}

// hueFor assigns a stable color per agent label, in first-seen order.
type hueMap struct {
	mu     sync.Mutex
	order  []string
	byName map[string]lipgloss.AdaptiveColor
	taken  map[lipgloss.AdaptiveColor]bool
}

func newHueMap() *hueMap { return &hueMap{byName: map[string]lipgloss.AdaptiveColor{}} }

func (h *hueMap) get(label string) lipgloss.AdaptiveColor {
	if label == "" {
		return cDim
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.byName[label]; ok {
		return c
	}
	// The vendor color goes to the first agent of that vendor. A second one
	// (two models of the same CLI) takes the rotation instead: telling two
	// lanes apart matters more than showing the brand twice.
	c, ok := brandHue(label)
	if !ok || h.taken[c] {
		c = agentHues[len(h.order)%len(agentHues)]
	}
	if h.taken == nil {
		h.taken = map[lipgloss.AdaptiveColor]bool{}
	}
	h.taken[c] = true
	h.order = append(h.order, label)
	h.byName[label] = c
	return c
}

// dirLabel is a directory as a person recognizes it: the home prefix as "~",
// and long paths cut from the left, since the tail is what identifies a tree.
func dirLabel(dir string, w int) string {
	if dir == "" || w <= 0 {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if dir == home {
			dir = "~"
		} else if rest, ok := strings.CutPrefix(dir, home+string(os.PathSeparator)); ok {
			dir = "~" + string(os.PathSeparator) + rest
		}
	}
	if uniseg.StringWidth(dir) <= w {
		return dir
	}
	if w == 1 {
		return "…"
	}
	for uniseg.StringWidth(dir) > w-1 {
		_, rest, _, _ := uniseg.FirstGraphemeClusterInString(dir, -1)
		if rest == dir {
			break
		}
		dir = rest
	}
	return "…" + dir
}

// reviewShort is a review as the screens name it: the -review suffix is noise
// repeated on every cell, lane, and feed line.
func reviewShort(name string) string {
	return strings.TrimSuffix(name, "-review")
}

func styled(c lipgloss.TerminalColor, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// The frame's own glyphs. They are constants -- a NormalBorder in the border
// tone -- and every one of them is the same escape sequence on every frame of
// every run, so a frame costs one copy per piece rather than a style
// resolution. They are built on first use and dropped whenever the profile
// under them changes, which is what SetMonochrome does: a frame drawn in
// color and reused under --no-color would leave a border the flag asked to be
// rid of.
var frameGlyphs atomic.Pointer[frameParts]

// frameParts is one panel frame's worth of glyphs.
type frameParts struct {
	side                   string
	pad                    string
	topStart, topEnd       string
	bottomStart, bottomEnd string
	// edgeOpen and reset wrap a run of horizontal cells in the border's color
	// once instead of once per cell. Under a profile that draws no color at
	// all both are empty and the run is the bare glyphs.
	edgeOpen, reset string
}

// frameTable is the glyph table for the profile in force, built on first use.
// Lipgloss detects the terminal's profile once and caches it, so for every run
// but the one that changed it, this returns the same table.
func frameTable() *frameParts {
	if t := frameGlyphs.Load(); t != nil {
		return t
	}
	t := &frameParts{
		side:        styled(cBorder, "\u2502"),
		pad:         " ",
		topStart:    styled(cBorder, "\u250c"),
		topEnd:      styled(cBorder, "\u2510"),
		bottomStart: styled(cBorder, "\u2514"),
		bottomEnd:   styled(cBorder, "\u2518"),
	}
	// One horizontal cell is its open sequence, the glyph, and its reset; a
	// run of them is that open sequence, the glyphs, and one reset. The
	// rendered cell ends in the reset the style closes with, so the open
	// sequence is everything before the last one, and a profile that renders
	// no color at all leaves both empty -- which is why the table is built per
	// profile rather than once at init.
	const reset = "\x1b[0m"
	cell := styled(cBorder, "\u2500")
	if open, tail, ok := strings.Cut(cell, reset); ok && strings.HasSuffix(open, "\u2500") {
		t.edgeOpen = strings.TrimSuffix(open, "\u2500")
		t.reset = tail
	}
	frameGlyphs.Store(t)
	return t
}

// frameEdge is a run of n horizontal border cells between two corners. The
// library draws this row a rune at a time, measuring each one as it goes; a
// border cell is one cell wide and one glyph long whatever the profile under
// it, so the run is one copy and one wrap.
func frameEdge(t *frameParts, n int) string {
	if n <= 0 {
		return ""
	}
	return t.edgeOpen + strings.Repeat("\u2500", n) + t.reset
}

// frameRow is one row inside a frame: a left glyph, the padding, the content,
// the padding, a right glyph.
func frameRow(t *frameParts, row string) string {
	var b strings.Builder
	b.Grow(len(t.side) + len(row) + 2*len(t.pad) + len(t.side))
	b.WriteString(t.side)
	b.WriteString(t.pad)
	b.WriteString(row)
	b.WriteString(t.pad)
	b.WriteString(t.side)
	return b.String()
}

// panel wraps content in a titled instrument frame, forcing exact inner
// dimensions.
//
// The frame is drawn here rather than by lipgloss's border, and the body never
// reaches lipgloss at all. A border over a block this package has already cut
// to an exact width re-measures every one of its rows to find the width it
// was told, and a profile of a busy dashboard put two thirds of a frame's CPU
// there, at ten frames a second for the life of a run. What is drawn is the
// frame lipgloss was drawing: a corner, that width of horizontal, a corner,
// and then a left glyph, the padding, the row, the padding, and a right glyph
// on every line, each piece in the border's own escape sequence.
// TestPanelEqualsLipglossFrame holds that against the library's own border,
// escape for escape; the horizontal edge is the one place the two bytes differ
// for the same picture, and TestFrameEdgeEqualsLipgloss holds that separately.
func panel(title, content string, innerW, innerH int) string {
	f := frameTable()
	rows := strings.Split(padBlock(content, innerW, innerH), "\n")
	for i, row := range rows {
		rows[i] = frameRow(f, row)
	}
	// The edge spans the two padding spaces inside the verticals and the
	// content, which is innerW+2 cells wide with a corner at each end.
	edge := frameEdge(f, max(innerW+2, 1))
	// The title is held to the frame's own width and left at whatever length
	// it is: a panel's title is the text and nothing else, and the two
	// pickers' panes read it as such. The dashboard, where a short title has to
	// come out the width of the rows under it, fills it out on the way past.
	return clipEllipsis(styleTitle.Render(title), innerW+panelBorderColumns) +
		"\n" + f.topStart + edge + f.topEnd + "\n" +
		strings.Join(rows, "\n") + "\n" +
		f.bottomStart + edge + f.bottomEnd
}

// tabCells is how wide a tab is once a row is drawn. lipgloss expands tabs to
// four spaces before a style measures anything, and a row carrying one that
// reached the border intact would be a cell short of the frame it sits in.
const tabCells = 4

// padBlock forces content to exactly innerW columns and innerH rows. The
// first two rewrites are the ones lipgloss applies to a style it renders, and
// they have to stay ahead of the measurement: a \r\n left in a row ends it a
// cell early, and a tab is one byte that would be counted as one cell where
// the renderer draws four.
func padBlock(content string, innerW, innerH int) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\t", strings.Repeat(" ", tabCells))
	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	for i, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		// One measurement per line. A frame measures every row of every panel
		// twice over when the width is asked for once here and again inside
		// clip.
		w := lipgloss.Width(ln)
		switch {
		case innerW <= 0:
			ln, w = "", 0
		case w > innerW:
			// A cut lands on a cluster boundary, so it can come up short of
			// innerW (two 2-cell glyphs do not fit in 5). The remainder is
			// real, so the cut result is measured again before it is padded.
			ln = clipNarrow(ln, innerW)
			w = lipgloss.Width(ln)
		}
		if gap := innerW - w; gap > 0 {
			ln += strings.Repeat(" ", gap)
		}
		lines[i] = ln
	}
	for len(lines) < innerH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}
	return strings.Join(lines, "\n")
}

// heatSteps is the ramp as an indexed table, cold to hot, so a hot loop can
// name the color it drew a cell in by index instead of asking lipgloss for a
// value on every cell of every frame.
var heatSteps = [...]lipgloss.TerminalColor{cTrack, cTeal, cCyan, cGreen, cYellow, cRed}

// heatBands are the ramp's cut points: the cold end covers everything up to
// and including the first, and each later entry opens the step above it. One
// fewer entry than heatSteps has colors; the assertion keeps a color added to
// one table and not the other a build failure rather than a ramp whose last
// step never renders.
var heatBands = [...]float64{0.02, 0.25, 0.5, 0.72, 0.88}

var _ = [1]struct{}{}[len(heatBands)-len(heatSteps)+1]

// heatIndex is the heatSteps index of the step intensity f falls in.
func heatIndex(f float64) int {
	if math.IsNaN(f) || f <= heatBands[0] {
		return 0
	}
	for i := 1; i < len(heatBands); i++ {
		if f < heatBands[i] {
			return i
		}
	}
	return len(heatSteps) - 1
}

// heatColor maps 0..1 intensity onto a cold to hot ramp. Reserved for
// magnitude and health; never used decoratively. The cold end stops at the
// track tone, not a near-background one: every dot it renders is an
// instrument stroke and must clear 3:1 (SC 1.4.11).
func heatColor(f float64) lipgloss.TerminalColor {
	return heatSteps[heatIndex(f)]
}

// chartGlyphs is every braille cell the chart can draw, rendered once per
// color it draws one in: the heat ramp's steps in table order, and the dim
// track an unlit baseline is stroked with. A chart is drawn once per lane plus
// once for the activity strip on every one of the dashboard's ten frames a
// second, so a cell that asked lipgloss for its escape sequence would re-parse
// the color and re-convert it against the terminal profile for each of the few
// hundred cells a frame holds. Indexed by step, not by color: lipgloss's
// colors are interface values, and a map keyed on one would hash a string per
// lookup and allocate the interface twice.
//
// The table is dropped whenever the profile under it changes, which is what
// SetMonochrome does: a table rendered in color and reused after --no-color
// would leave a dashboard with color the operator asked to be rid of.
var chartGlyphs atomic.Pointer[[len(heatSteps) + 1]map[int]string]

// chartGlyphTable is the glyph table for the profile in force, built on first
// use. Lipgloss detects the terminal's profile once and caches it, so for
// every run but the one that changed it, this returns the same table.
func chartGlyphTable() [len(heatSteps) + 1]map[int]string {
	if t := chartGlyphs.Load(); t != nil {
		return *t
	}
	var t [len(heatSteps) + 1]map[int]string
	for i, c := range heatSteps {
		row := make(map[int]string, len(brailleSuffix))
		for _, pattern := range brailleSuffix {
			row[pattern] = styled(c, string(rune(0x2800+pattern)))
		}
		t[i] = row
	}
	t[len(heatSteps)] = t[0] // the unlit baseline is the track, the ramp's cold end
	chartGlyphs.Store(&t)
	return t
}

// wordmark is the name in the path-arrow teal of assets/mark.svg, one hue.
func wordmark() string {
	return lipgloss.NewStyle().Bold(true).Foreground(cMark).Render("GAUNTLET")
}
