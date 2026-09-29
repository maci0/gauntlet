// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/maci0/gauntlet/internal/runner"
)

// brailleBits maps (sub-row, sub-column) inside a braille cell to its Unicode
// bit: dot1..8 = 0x01,0x02,0x04,0x08,0x10,0x20,0x40,0x80.
var brailleBits = [4][2]byte{
	{0x01, 0x08},
	{0x02, 0x10},
	{0x04, 0x20},
	{0x40, 0x80},
}

// brailleSuffix is the glyph pattern for a cell whose last n sub-rows are lit,
// for n from 0 to 4. The level fills a cell from its top down, so the lit
// sub-rows are always the topmost ones and that count is the whole of what a
// cell's pattern depends on: five patterns, not the 256 a bitwise index over
// all eight dots would allow.
var brailleSuffix = func() (t [5]int) {
	for n := 1; n < len(t); n++ {
		for sr := 4 - n; sr < 4; sr++ {
			t[n] |= int(brailleBits[sr][0]) | int(brailleBits[sr][1])
		}
	}
	return t
}()

// dot is one rendered chart cell: the color it was drawn in and the glyph that
// color produces. A cell holding an empty color is the one thing that means
// "not drawn yet".
type dot struct {
	col   lipgloss.TerminalColor
	glyph string
}

// chart renders a series as a braille dot matrix. Every cell is a 2x4 dot
// grid, and a lit cell always sets both of its columns, so the extra
// resolution is vertical: a w by h chart draws w columns by h*4 dot rows.
// Values fill upward from the baseline and hug the right edge like a scope
// trace, so the newest value is the rightmost column.
//
// An empty series still draws its grid: absence of signal is information.
func chart(vals []float64, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	cols, peak := tailCols(vals, w)
	dotH := h * 4

	// A braille cell is eight dots, so a rendered glyph for one is fixed once
	// the color is known. The table is a local array rather than a map: chart
	// runs once per lane plus once for the activity strip on every frame, and
	// a map keyed by the formatted color would allocate and hash a string for
	// each of its cells.
	var cache [len(brailleSuffix)]dot
	rows := make([]strings.Builder, h)
	for cy := range h {
		for cx := range w {
			frac := clamp01(cols[cx] / peak)
			level := frac * float64(dotH)
			// Sub-row sr lights when dotH-(cy*4+sr) <= level, that is when
			// sr >= dotH-cy*4-level, so the lit run is the sub-rows from
			// that bound to the cell's last.
			lit := 4 - clampi(int(math.Ceil(float64(dotH-cy*4)-level)), 0, 4)
			pattern := brailleSuffix[lit]
			var col lipgloss.TerminalColor
			switch {
			case pattern == 0 && cy == h-1:
				// Unlit baseline grid: dim, several times fainter than data,
				// but still a readable stroke (3:1), not near-invisible.
				pattern, lit = brailleSuffix[1], 1
				col = cTrack
			case pattern == 0:
				rows[cy].WriteByte(' ')
				continue
			default:
				col = heatColor(frac)
			}
			d := &cache[lit]
			if d.col != col {
				// One pattern can be drawn in two colors: data at the heat
				// ramp, or the unlit baseline stroke. The color is stored beside
				// the glyph so a second one replaces it rather than serving the
				// first.
				*d = dot{col: col, glyph: styled(col, string(rune(0x2800+pattern)))}
			}
			rows[cy].WriteString(d.glyph)
		}
	}
	out := make([]string, h)
	for i := range rows {
		out[i] = rows[i].String()
	}
	return strings.Join(out, "\n")
}

// tailCols returns exactly w columns carrying the last w values, zero padded on
// the left, plus their peak (floored at 1 so an all-zero series still renders).
func tailCols(vals []float64, w int) ([]float64, float64) {
	if w <= 0 {
		return nil, 1
	}
	vs := vals
	if len(vs) > w {
		vs = vs[len(vs)-w:]
	}
	peak := 0.0
	for _, v := range vs {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			peak = max(peak, v)
		}
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
	filled = max(0, min(filled, w))
	return styled(on, strings.Repeat("▰", filled)) +
		styleTrack.Render(strings.Repeat("▱", w-filled))
}

// statusGlyph is the review grid's cell: one column, one meaning.
func statusGlyph(s runner.Status) (string, lipgloss.TerminalColor) {
	switch s {
	case statusRunning:
		return "▸", cCyan
	case runner.StatusOK:
		return "✓", cGreen
	case runner.StatusFail:
		return "✗", cRed
	case runner.StatusTimeout:
		return "⧖", cYellow
	case runner.StatusConflict:
		return "⑂", cPeach
	case runner.StatusSkipped:
		return "–", cDim
	case runner.StatusInterrupted:
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
	case math.IsNaN(v) || math.IsInf(v, 0) || v <= 0:
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
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}
