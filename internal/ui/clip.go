// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Cutting styled text to a column budget. theme.go holds the palette; this
// holds the measurement and the cuts, because a frame that does not fit its
// pane is a geometry problem and not a color one.

package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
)

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
	return clipNarrow(s, w)
}

// clipEllipsis is clip with the cut announced: the last cell becomes the
// marker, so styled text too wide for its pane reads as cut rather than as a
// word that happens to end there. trim does the same for unstyled names; a
// panel title and a status line carry color, so this is the form they use.
func clipEllipsis(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return clip(s, w-1) + "…"
}

// clipNarrow is clip for a string already known to be wider than w, so it skips
// the width measurement clip starts with. A cut never lands inside a grapheme
// cluster, and a styled run left open by the cut is closed so the reset cannot
// leak into whatever is drawn next. Plain text carries no run to close, so it
// is returned as it stands: a reset appended to a string that never turned a
// style on is an escape nobody can see, and one per clipped line is a write
// spent on nothing.
func clipNarrow(s string, w int) string {
	var b strings.Builder
	visible := 0
	styled := false
	for _, tok := range widthTokens(s) {
		if tok[0] == 0x1b {
			b.WriteString(tok)
			styled = true
			continue
		}
		cw := uniseg.StringWidth(tok)
		if visible+cw > w {
			if styled {
				return b.String() + "\x1b[0m"
			}
			return b.String()
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
			if j < len(s) {
				switch s[j] {
				case '[': // CSI sequence
					j++
					for j < len(s) {
						r, size := utf8.DecodeRuneInString(s[j:])
						j += size
						if r >= '@' && r <= '~' {
							break
						}
					}
				case ']': // OSC sequence
					j++
					for j < len(s) {
						r, size := utf8.DecodeRuneInString(s[j:])
						if r == 0x07 || r == 0x1b {
							if r == 0x07 {
								j += size
							}
							break
						}
						j += size
					}
				default:
					_, size := utf8.DecodeRuneInString(s[j:])
					j += size
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
