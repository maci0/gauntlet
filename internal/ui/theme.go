// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package ui renders the live gauntlet dashboard.
//
// It follows the instrument rules the project borrowed from TMOG: the screen
// is a cockpit, not a report. Live data is the brightest thing on it, chrome
// and grids stay dim, one hue means one agent everywhere, meters show their
// unlit remainder, and nothing is interpolated or faked when data is missing.
package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// Palette: Catppuccin Mocha, degrading to the nearest 256/16 colors on old
// terminals. Secondary text stays above 4.5:1 on the base.
var (
	cSurface  = lipgloss.Color("#313244")
	cDim      = lipgloss.Color("#9399b2")
	cText     = lipgloss.Color("#cdd6f4")
	cBorder   = lipgloss.Color("#45475a")
	cRed      = lipgloss.Color("#f38ba8")
	cGreen    = lipgloss.Color("#a6e3a1")
	cYellow   = lipgloss.Color("#f9e2af")
	cPeach    = lipgloss.Color("#fab387")
	cBlue     = lipgloss.Color("#89b4fa")
	cCyan     = lipgloss.Color("#89dceb")
	cTeal     = lipgloss.Color("#94e2d5")
	cMagenta  = lipgloss.Color("#cba6f7")
	cPink     = lipgloss.Color("#f5c2e7")
	cLavender = lipgloss.Color("#b4befe")
)

var (
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(cLavender)
	styleDim   = lipgloss.NewStyle().Foreground(cDim)
	styleFaint = lipgloss.NewStyle().Foreground(cBorder)
	styleValue = lipgloss.NewStyle().Bold(true).Foreground(cText)
	styleOK    = lipgloss.NewStyle().Foreground(cGreen)
	styleWarn  = lipgloss.NewStyle().Foreground(cYellow)
	styleBad   = lipgloss.NewStyle().Foreground(cRed)
	styleInfo  = lipgloss.NewStyle().Foreground(cCyan)
	styleMagic = lipgloss.NewStyle().Foreground(cMagenta)
	// Reasoning is real output, but it is not the answer: lavender keeps it
	// legible while visibly subordinate to the text the agent actually wrote.
	styleThink = lipgloss.NewStyle().Foreground(cLavender)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
)

// agentHues is the fixed rotation for per-agent color. One concept, one hue:
// an agent keeps its color in its lane, its review rows, and its feed lines.
var agentHues = []lipgloss.Color{cBlue, cPeach, cTeal, cMagenta, cPink, cYellow, cCyan, cLavender, cGreen}

// hueFor assigns a stable color per agent label, in first-seen order.
type hueMap struct {
	mu     sync.Mutex
	order  []string
	byName map[string]lipgloss.Color
}

func newHueMap() *hueMap { return &hueMap{byName: map[string]lipgloss.Color{}} }

func (h *hueMap) get(label string) lipgloss.Color {
	if label == "" {
		return cDim
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.byName[label]; ok {
		return c
	}
	c := agentHues[len(h.order)%len(agentHues)]
	h.order = append(h.order, label)
	h.byName[label] = c
	return c
}

func styled(c lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// panel wraps content in a titled rounded box, forcing exact inner dimensions.
// lipgloss Width() is deliberately avoided for the body: its wrapping
// mishandles densely styled cells like braille charts.
func panel(title, content string, innerW, innerH int) string {
	return styleTitle.Render(title) + "\n" + panelStyle.Render(padBlock(content, innerW, innerH))
}

// padBlock forces content to exactly innerW columns and innerH rows.
func padBlock(content string, innerW, innerH int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
	}
	for i, ln := range lines {
		if gap := innerW - lipgloss.Width(ln); gap > 0 {
			ln += strings.Repeat(" ", gap)
		} else {
			ln = clip(ln, innerW)
		}
		lines[i] = ln
	}
	for len(lines) < innerH {
		lines = append(lines, strings.Repeat(" ", innerW))
	}
	return strings.Join(lines, "\n")
}

// clip truncates a styled string to w visible columns, keeping escapes intact.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	visible, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
			b.WriteRune(r)
		case inEsc:
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		default:
			if visible >= w {
				return b.String() + "\x1b[0m"
			}
			b.WriteRune(r)
			visible++
		}
	}
	return b.String()
}

// heatColor maps 0..1 intensity onto a cold to hot ramp. Reserved for
// magnitude and health; never used decoratively.
func heatColor(f float64) lipgloss.Color {
	switch {
	case f <= 0.02:
		return cSurface
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

// wordmark renders the logo once, with a cyan to peach gradient.
var wordmark = sync.OnceValue(func() string {
	letters := []rune("GAUNTLET")
	colors := []lipgloss.Color{cTeal, cCyan, cBlue, cLavender, cMagenta, cPink, cPeach, cYellow}
	var b strings.Builder
	for i, l := range letters {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[i%len(colors)]).Render(string(l)))
	}
	return b.String()
})
