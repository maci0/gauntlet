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
	"os"
	"strings"
	"sync"
	"unicode/utf8"

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
	cText     = adaptive("#4c4f69", "#cdd6f4")
	cDim      = adaptive("#6a6d82", "#9399b2")
	cFaint    = adaptive("#6b6d7b", "#84889f")
	cTrack    = adaptive("#878b99", "#6c7086")
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
)

// adaptive pairs a Latte tone for light backgrounds with its Mocha
// counterpart, resolved per terminal at render time.
func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

var (
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(cLavender)
	styleDim   = lipgloss.NewStyle().Foreground(cDim)
	styleFaint = lipgloss.NewStyle().Foreground(cFaint)
	styleTrack = lipgloss.NewStyle().Foreground(cTrack)
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
// "codex:gpt-5"), and reports whether there is one.
func brandHue(label string) (lipgloss.AdaptiveColor, bool) {
	tool, _, _ := strings.Cut(label, ":")
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
	if dir == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if dir == home {
			dir = "~"
		} else if rest, ok := strings.CutPrefix(dir, home+string(os.PathSeparator)); ok {
			dir = "~" + string(os.PathSeparator) + rest
		}
	}
	if w > 1 && uniseg.StringWidth(dir) > w {
		for uniseg.StringWidth(dir) > w-1 {
			_, size := utf8.DecodeRuneInString(dir)
			dir = dir[size:]
		}
		dir = "…" + dir
	}
	return dir
}

func styled(c lipgloss.TerminalColor, s string) string {
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
// magnitude and health; never used decoratively. The cold end stops at the
// track tone, not a near-background one: every dot it renders is an
// instrument stroke and must clear 3:1 (SC 1.4.11).
func heatColor(f float64) lipgloss.TerminalColor {
	switch {
	case f <= 0.02:
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

// wordmark renders the logo once, with a cyan to peach gradient.
var wordmark = sync.OnceValue(func() string {
	letters := []rune("GAUNTLET")
	colors := []lipgloss.AdaptiveColor{cTeal, cCyan, cBlue, cLavender, cMagenta, cPink, cPeach, cYellow}
	var b strings.Builder
	for i, l := range letters {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[i%len(colors)]).Render(string(l)))
	}
	return b.String()
})
