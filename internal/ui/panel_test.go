// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// panel draws its own border and pads its own body where lipgloss used to do
// both, because the library re-measured every row of a block whose width
// padBlock had already forced, and a dashboard frame is drawn ten times a
// second for the life of a run. This holds the hand-drawn frame against the
// library's own border, escape for escape, so the frame the dashboard draws
// is the frame it always drew.
func TestPanelEqualsLipglossFrame(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.TrueColor} {
		t.Run(profileName(profile), func(t *testing.T) {
			r := lipgloss.DefaultRenderer()
			prev := r.ColorProfile()
			t.Cleanup(func() { r.SetColorProfile(prev) })
			r.SetColorProfile(profile)
			// The frame's glyphs are built once against the profile in force
			// and dropped when the profile changes, which is what
			// SetMonochrome does. A test drawing in color on a table another
			// test left in Ascii would be comparing two different frames.
			frameGlyphs.Store(nil)

			bodies := []struct {
				name    string
				content string
			}{
				{"empty", ""},
				{"one row", "plain row"},
				{"short rows", "a\nbb\nccc"},
				{"too many rows", "one\ntwo\nthree\nfour\nfive"},
				{"too few rows", "one"},
				{"styled", styleValue.Render("value") + "\n" + styleBad.Render("error")},
				{"wide glyphs", "認証認証"},
				{"over-wide row", strings.Repeat("z", 60)},
				{"over-wide styled", styleDim.Render(strings.Repeat("z", 60))},
				{"carriage returns", "hello\r\nworld\r"},
				{"tab in a row", "a\tb"},
			}
			titles := []string{"ACTIVITY", "", "REVIEW GRID pass 0 fail 0", strings.Repeat("LONG ", 40)}
			for _, innerW := range []int{1, 2, 5, 8, 20, 40, 96} {
				for _, innerH := range []int{1, 2, 5} {
					for _, b := range bodies {
						for _, title := range titles {
							got := panel(title, b.content, innerW, innerH)
							want := lipglossFrame(title, b.content, innerW, innerH)
							// The two differ in one place and one place only:
							// lipgloss writes the border's escape sequence
							// around the whole horizontal edge, and this
							// writes it around the corners and the run
							// between them. A terminal draws the two the same,
							// so the frames are compared as drawn: every row,
							// its cells, and the color of every cell that
							// carries one.
							if err := sameFrame(got, want); err != nil {
								t.Errorf("panel(%s, %q, innerW %d, innerH %d): %v\n got %q\nwant %q",
									b.name, title, innerW, innerH, err, got, want)
							}
						}
					}
				}
			}
		})
	}
}

// lipglossFrame is the frame as it was drawn before panel drew its own: the
// library's border, its padding, and its width handling, all of it.
func lipglossFrame(title, content string, innerW, innerH int) string {
	return clipEllipsis(styleTitle.Render(title), innerW+panelBorderColumns) +
		"\n" + panelStyle.Render(padBlock(content, innerW, innerH))
}

// sameFrame reports what two frames differ by, if anything. It is not a byte
// comparison: the horizontal edge is one escape sequence around a run of cells
// on one side and one around the corners and the run between them on the
// other, and those are the same picture. What a terminal sees is the cells of
// each row and the color each is drawn in, so that is what is compared: the
// rows have to draw the same characters, and the colors have to be the same
// set in the same order, which is what the sequences between them say.
func sameFrame(got, want string) error {
	gotRows, wantRows := strings.Split(got, "\n"), strings.Split(want, "\n")
	if len(gotRows) != len(wantRows) {
		return fmt.Errorf("%d rows, want %d", len(gotRows), len(wantRows))
	}
	for i := range gotRows {
		if g, w := stripEscapes(gotRows[i]), stripEscapes(wantRows[i]); g != w {
			return fmt.Errorf("row %d draws %q, want %q", i, g, w)
		}
		if g, w := colorsOf(gotRows[i]), colorsOf(wantRows[i]); g != w {
			return fmt.Errorf("row %d colors %q, want %q", i, g, w)
		}
	}
	return nil
}

// stripEscapes is the row without its SGR sequences, which is what a terminal
// draws it as.
func stripEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if end := strings.IndexByte(s[i:], 'm'); end >= 0 {
				i += end + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// colorsOf is the row's SGR sequences, repeats dropped: a terminal colors a
// cell by the last sequence that set a color, so naming the same color once or
// three times over a run of cells is the same instruction. What the two frames
// have to agree on is which colors a row is drawn in, not how many times a
// border repeats its own.
func colorsOf(s string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			i++
			continue
		}
		end := strings.IndexByte(s[i:], 'm')
		if end < 0 {
			break
		}
		seq := s[i : i+end+1]
		if seq != "\x1b[0m" && !seen[seq] {
			seen[seq] = true
			b.WriteString(seq)
		}
		i += end + 1
	}
	return b.String()
}

// The frame's border glyphs are constants, and they are what the whole
// equivalence above rests on: a panel is a NormalBorder in the border tone, so
// the table built for the profile in force has to be that border's own
// sequence, and an edge of n cells between two corners has to span n+2.
func TestFrameTableMatchesTheBorderItReplaces(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.TrueColor} {
		t.Run(profileName(profile), func(t *testing.T) {
			r := lipgloss.DefaultRenderer()
			prev := r.ColorProfile()
			t.Cleanup(func() { r.SetColorProfile(prev) })
			r.SetColorProfile(profile)
			frameGlyphs.Store(nil)
			f := frameTable()

			// One row of the library's border, corner to corner, is the whole
			// of it: the glyphs it draws and the sequences around them.
			row := strings.Split(panelStyle.Render(""), "\n")
			if len(row) < 2 {
				t.Fatalf("a bordered block has %d rows, want at least 2", len(row))
			}
			edge := row[0]
			top := f.topStart + frameEdge(f, lipgloss.Width(edge)-2) + f.topEnd
			if err := sameFrame(top, edge); err != nil {
				t.Errorf("hand-drawn top row: %v", err)
			}
			bottom := row[len(row)-1]
			bot := f.bottomStart + frameEdge(f, lipgloss.Width(bottom)-2) + f.bottomEnd
			if err := sameFrame(bot, bottom); err != nil {
				t.Errorf("hand-drawn bottom row: %v", err)
			}
			mid := row[1]
			side := f.side + f.pad + f.pad + f.side
			if !strings.HasPrefix(mid, side) || !strings.HasSuffix(mid, side) {
				t.Errorf("a body row is not flanked by the border's glyphs: %q", mid)
			}
		})
	}
}

// profileName names a profile for a subtest.
func profileName(p termenv.Profile) string {
	switch p {
	case termenv.Ascii:
		return "ascii"
	case termenv.TrueColor:
		return "truecolor"
	default:
		return "profile" + strconv.Itoa(int(p))
	}
}
