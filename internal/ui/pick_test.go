// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func demoPicker() *picker {
	p := newPicker(PickConfig{
		Dir: "/home/dev/project",
		Groups: []PickGroup{
			{Name: "quick", Reviews: []string{"sec-review", "code-review"}},
			{Name: "frontend", Reviews: []string{"ux-review", "a11y-review"}},
		},
		Agents: []string{"claude", "codex:gpt-5"},
		Branch: "work",
		Merge:  []string{"main", "release"},
		CPUs:   8,
	})
	p.w, p.h, p.ready = 100, 30, true
	return p
}

func press(p *picker, keys ...string) {
	for _, k := range keys {
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
}

// The launcher's whole output is an argv, so that is what the tests pin: what
// it composes must be a command a person could have typed.
func TestPickComposesTheCommandItShows(t *testing.T) {
	cases := []struct {
		name string
		act  func(p *picker)
		want string
	}{
		{"defaults are not spelled out", func(*picker) {},
			"-C /home/dev/project --once --tui"},
		{"a whole set is named by its set", func(p *picker) {
			p.cursor[paneReviews] = 1 // past the suggest row, on the first group
			p.toggle()
		}, "-C /home/dev/project -r quick --once --tui"},
		{"suggest replaces the review list", func(p *picker) {
			p.selected["ux-review"] = true
			p.toggle() // the cursor starts on the suggest row
		}, "-C /home/dev/project --suggest --once --tui"},
		{"a suggest agent is only passed for a suggested run", func(p *picker) {
			p.opts[optSuggestAgent].idx = 2 // codex:gpt-5
		}, "-C /home/dev/project --once --tui"},
		{"the suggest agent rides along with suggest", func(p *picker) {
			p.suggest = true
			p.opts[optSuggestAgent].idx = 2
		}, "-C /home/dev/project --suggest --suggest-agent codex:gpt-5 --once --tui"},
		{"a single review is named by its short name", func(p *picker) {
			p.selected["ux-review"] = true
		}, "-C /home/dev/project -r ux --once --tui"},
		{"everything selected is the default again", func(p *picker) {
			p.toggleAll()
		}, "-C /home/dev/project --once --tui"},
		{"push implies commit, so only push is passed", func(p *picker) {
			p.optByFlag("--commit").on = true
			p.optByFlag("--push").on = true
		}, "-C /home/dev/project --once --tui --push"},
		{"a merge target without commits is not passed", func(p *picker) {
			p.optByFlag("--merge-into").idx = 1 // main
		}, "-C /home/dev/project --once --tui"},
		{"a merge target rides with the commits it moves", func(p *picker) {
			p.optByFlag("--commit").on = true
			p.optByFlag("--merge-into").idx = 1
		}, "-C /home/dev/project --once --tui --commit --merge-into main"},
		{"a subset of agents is passed, all of them is not", func(p *picker) {
			p.agents[0] = true
		}, "-C /home/dev/project -a claude --once --tui"},
		{"every agent means auto-detect", func(p *picker) {
			p.agents[0], p.agents[1] = true, true
		}, "-C /home/dev/project --once --tui"},
		{"+ raises concurrency from any pane", func(p *picker) {
			press(p, "+", "+", "+")
		}, "-C /home/dev/project -j 4 --once --tui"},
		{"concurrency above one is passed", func(p *picker) {
			p.focus = paneOptions
			p.adjust(+1)
			p.adjust(+1)
			p.adjust(+1)
		}, "-C /home/dev/project -j 4 --once --tui"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := demoPicker()
			c.act(p)
			if got := strings.Join(p.argv(), " "); got != c.want {
				t.Fatalf("argv is %q, want %q", got, c.want)
			}
		})
	}
}

// A group header toggles its members, and toggling a whole group off empties
// it rather than filling it again.
func TestPickGroupHeaderTogglesItsMembers(t *testing.T) {
	p := demoPicker()
	p.cursor[paneReviews] = 1
	p.toggle()
	if p.chosen() != 2 {
		t.Fatalf("%d reviews chosen, want the group's two", p.chosen())
	}
	p.toggle()
	if p.chosen() != 0 {
		t.Fatalf("%d reviews chosen, want none after the second toggle", p.chosen())
	}
}

// Navigation must not walk off either end, whatever the terminal size.
func TestPickCursorStaysInBounds(t *testing.T) {
	p := demoPicker()
	for range 50 {
		p.move(+1)
	}
	if p.cursor[paneReviews] >= len(p.rows()) {
		t.Fatalf("cursor %d is past the last row (%d)", p.cursor[paneReviews], len(p.rows()))
	}
	for range 50 {
		p.move(-1)
	}
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("cursor %d, want the first row", p.cursor[paneReviews])
	}
}

// The screen renders at any size the terminal reports, including one too
// small to hold the panes.
func TestPickRendersAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {60, 12}, {20, 6}} {
		p := demoPicker()
		p.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := p.View()
		// The command line is what every size must keep; the panels only
		// appear where they fit.
		if !strings.Contains(view, "gauntlet ") {
			t.Fatalf("%dx%d lost the command line:\n%s", size[0], size[1], view)
		}
		if size[0] >= 50 && size[1] >= 12 && !strings.Contains(view, "REVIEWS") {
			t.Fatalf("%dx%d lost the panels:\n%s", size[0], size[1], view)
		}
	}
}

// Enter launches, q leaves with nothing.
func TestPickQuitKeys(t *testing.T) {
	p := demoPicker()
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.launch {
		t.Fatal("enter should launch")
	}
	p = demoPicker()
	press(p, "q")
	if p.launch {
		t.Fatal("q should leave without launching")
	}
}
