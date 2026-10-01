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

// panel wraps content in a titled instrument frame, forcing exact inner dimensions.
// lipgloss Width() is deliberately avoided for the body: its wrapping
// mishandles densely styled cells like braille charts.
func panel(title, content string, innerW, innerH int) string {
	return clipEllipsis(styleTitle.Render(title), innerW+panelBorderColumns) +
		"\n" + panelStyle.Render(padBlock(content, innerW, innerH))
}

// padBlock forces content to exactly innerW columns and innerH rows.
func padBlock(content string, innerW, innerH int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	for i, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		// One measurement per line. lipgloss.Width walks the text grapheme by
		// grapheme, and a frame measures every row of every panel twice over
		// when the width is asked for once here and again inside clip.
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

// heatColor maps 0..1 intensity onto a cold to hot ramp. Reserved for
// magnitude and health; never used decoratively. The cold end stops at the
// track tone, not a near-background one: every dot it renders is an
// instrument stroke and must clear 3:1 (SC 1.4.11).
func heatColor(f float64) lipgloss.TerminalColor {
	switch {
	case math.IsNaN(f) || f <= 0.02:
		return cTrack
	case f < 0.25:
		return cTeal
	case f < 0.5:
		return cCyan
	case f < 0.72:
		return cGreen
	case f < 0.88:
		return cYellow
	default:
		return cRed
	}
}

// heatSteps is the ramp above as an indexed table, in the order heatColor
// returns its steps, so a hot loop can name the color it drew a cell in by
// index. heatIndex walks the same ramp; TestHeatIndexNamesTheRampStep holds
// the two to each other.
var heatSteps = [...]lipgloss.TerminalColor{cTrack, cTeal, cCyan, cGreen, cYellow, cRed}

// heatIndex is the heatSteps index of the color heatColor returns for f. The
// two carry the same ramp, one as a value and one as a position in the table
// a cell of a chart or a meter renders from without asking lipgloss.
func heatIndex(f float64) int {
	switch {
	case math.IsNaN(f) || f <= 0.02:
		return 0
	case f < 0.25:
		return 1
	case f < 0.5:
		return 2
	case f < 0.72:
		return 3
	case f < 0.88:
		return 4
	default:
		return 5
	}
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
