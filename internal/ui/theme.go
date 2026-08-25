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
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
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

// clip truncates a styled string to w visible columns, keeping escapes
// intact. Widths are terminal cells, not runes: a CJK glyph is two columns
// and a combining mark is zero, and a cut never lands inside a grapheme
// cluster.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var b strings.Builder
	visible := 0
	for _, tok := range widthTokens(s) {
		if tok[0] == 0x1b {
			b.WriteString(tok)
			continue
		}
		cw := uniseg.StringWidth(tok)
		if visible+cw > w {
			return b.String() + "\x1b[0m"
		}
		visible += cw
		b.WriteString(tok)
	}
	return b.String()
}

// widthTokens splits s into the atomic units of column math: each ANSI
// escape sequence is one zero-width unit, and everything between them is
// split into grapheme clusters, so a wide glyph or an emoji sequence moves
// as one piece.
func widthTokens(s string) []string {
	toks := make([]string, 0, 16)
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) {
				r, size := utf8.DecodeRuneInString(s[j:])
				j += size
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					break
				}
			}
			toks = append(toks, s[i:j])
			i = j
			continue
		}
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
		toks = append(toks, cluster)
		i = len(s) - len(rest)
	}
	return toks
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
