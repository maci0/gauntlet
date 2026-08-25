// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// brailleBits maps (sub-row, sub-column) inside a braille cell to its Unicode
// bit: dot1..8 = 0x01,0x02,0x04,0x08,0x10,0x20,0x40,0x80.
var brailleBits = [4][2]byte{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// chart renders a series as a braille dot matrix: every terminal cell is a 2x4
// dot grid, so a w by h chart resolves w*2 by h*4 dots. Values fill upward from
// the baseline and hug the right edge like a scope trace, with the newest
// column marked.
//
// An empty series still draws its grid: absence of signal is information.
func chart(vals []float64, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	cols, peak := tailCols(vals, w)
	dotH := h * 4

	cache := map[[2]int]string{}
	rows := make([]strings.Builder, h)
	for cy := range h {
		for cx := range w {
			frac := clamp01(cols[cx] / peak)
			pattern := 0
			level := frac * float64(dotH)
			for sr := range 4 {
				if float64(dotH-(cy*4+sr)) <= level {
					pattern |= int(brailleBits[sr][0]) | int(brailleBits[sr][1])
				}
			}
			var col lipgloss.TerminalColor
			switch {
			case pattern == 0 && cy == h-1:
				// Unlit baseline grid: dim, several times fainter than data,
				// but still a readable stroke (3:1), not near-invisible.
				pattern = int(brailleBits[3][0]) | int(brailleBits[3][1])
				col = cTrack
			case pattern == 0:
				rows[cy].WriteByte(' ')
				continue
			default:
				col = heatColor(frac)
			}
			key := [2]int{int(colorKey(col)), pattern}
			s, ok := cache[key]
			if !ok {
				s = lipgloss.NewStyle().Foreground(col).Render(string(rune(0x2800 + pattern)))
				cache[key] = s
			}
			rows[cy].WriteString(s)
		}
	}
	out := make([]string, h)
	for i := range rows {
		out[i] = rows[i].String()
	}
	return strings.Join(out, "\n")
}

// colorKey turns a color into a cheap cache key. Adaptive pairs stringify
// stably, so one key covers both resolved variants.
func colorKey(c lipgloss.TerminalColor) uint32 {
	var k uint32
	for _, ch := range fmt.Sprint(c) {
		k = k*31 + uint32(ch)
	}
	return k
}

// tailCols returns exactly w columns carrying the last w values, zero padded on
// the left, plus their peak (floored at 1 so an all-zero series still renders).
func tailCols(vals []float64, w int) ([]float64, float64) {
	vs := vals
	if len(vs) > w {
		vs = vs[len(vs)-w:]
	}
	peak := 0.0
	for _, v := range vs {
		peak = max(peak, v)
	}
	if peak <= 0 {
		peak = 1
	}
	cols := make([]float64, w)
	copy(cols[w-len(vs):], vs)
	return cols, peak
}

// meter renders a quantized segment bar with its unlit remainder visible. The
// distance to full is as important as the current level, so the empty track is
// always drawn.
func meter(frac float64, w int, on lipgloss.TerminalColor) string {
	if w < 3 {
		w = 3
	}
	frac = clamp01(frac)
	filled := int(frac*float64(w) + 0.5)
	filled = min(filled, w)
	return styled(on, strings.Repeat("▰", filled)) +
		styleTrack.Render(strings.Repeat("▱", w-filled))
}

// sparkline is a one-row braille trace, for lanes and small cells. A thumbnail
// is a real instrument, scaled down.
func sparkline(vals []float64, w int) string { return chart(vals, w, 1) }

// statusGlyph is the review grid's cell: one column, one meaning.
func statusGlyph(s string) (string, lipgloss.TerminalColor) {
	switch s {
	case "running":
		return "▸", cCyan
	case "ok":
		return "✓", cGreen
	case "fail":
		return "✗", cRed
	case "timeout":
		return "⧖", cYellow
	case "conflict":
		return "⑂", cPeach
	case "skipped":
		return "–", cDim
	case "interrupted":
		return "␘", cYellow
	default:
		// Pending is real state, not chrome: the glyph reads at text
		// contrast, matching its faint-but-legible name in the cell.
		return "·", cFaint
	}
}

// fmtRate formats a tokens-per-second figure compactly.
func fmtRate(v float64) string {
	switch {
	case v <= 0:
		return "n/a"
	case v >= 1000:
		return fmt.Sprintf("%.1fk", v/1000)
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	default:
		return fmt.Sprintf("%.1f", v)
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
