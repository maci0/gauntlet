// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func demoPicker() *picker {
	p := newPicker(PickConfig{
		Dir: "/home/dev/project",
		Groups: []PickGroup{
			{Name: "quick", Reviews: []PickReview{
				{Name: "sec-review", Desc: "hunt for vulnerabilities"},
				{Name: "code-review", Desc: "correctness and clarity"},
			}},
			{Name: "frontend", Reviews: []PickReview{
				{Name: "ux-review", Desc: "flows a person has to follow"},
				{Name: "a11y-review", Desc: "keyboard and contrast", Project: true},
			}},
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

// The launcher header's right side is what the run would cover, and it
// changes with every toggle: a narrow terminal must lose dim chrome (the
// version, the title, the tree) before it loses the scope, and a wide one
// keeps all of it.
func TestLauncherHeaderKeepsScopeAtNarrowWidths(t *testing.T) {
	p := demoPicker()
	p.w = 50
	header := stripANSI(p.renderHeader())
	for _, want := range []string{"all 4 reviews", "all installed"} {
		if !strings.Contains(header, want) {
			t.Fatalf("a %d-column header lost %q:\n%s", p.w, want, header)
		}
	}
	p.w = 100
	if header := stripANSI(p.renderHeader()); !strings.Contains(header, "compose a run") {
		t.Fatalf("a wide header dropped the title:\n%s", header)
	}
}

// The narrow fallback has no status line, so the blocked reason must ride on
// its rows: enter is dead there too, and without the reason the only thing
// the view offers is a command that cannot run.
func TestNarrowLauncherSaysWhyItCannotRun(t *testing.T) {
	p := demoPicker()
	p.cfg.Dirty = true
	p.concurrency().n = 2
	p.w, p.h = 40, 10
	if got := stripANSI(p.renderNarrow()); !strings.Contains(got, "concurrency above 1") {
		t.Fatalf("the narrow fallback hides why enter does nothing:\n%s", got)
	}
	if got := stripANSI(demoPicker().renderNarrow()); strings.Contains(got, "⚠") {
		t.Fatalf("an unblocked narrow fallback grew a warning:\n%s", got)
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
		{"suggest rides with what is ticked, which weights it", func(p *picker) {
			p.selected["ux-review"] = true
			p.toggle() // the cursor starts on the suggest row
		}, "-C /home/dev/project --suggest -r ux --once --tui"},
		{"suggest alone names no reviews", func(p *picker) {
			p.toggle()
		}, "-C /home/dev/project --suggest --once --tui"},
		{"a suggest agent is only passed for a suggested run", func(p *picker) {
			p.opts[optSuggestAgent].idx = 3 // codex:gpt-5
		}, "-C /home/dev/project --once --tui"},
		{"the suggest agent rides along with suggest", func(p *picker) {
			p.suggest = true
			p.opts[optSuggestAgent].idx = 3
		}, "-C /home/dev/project --suggest --suggest-agent codex:gpt-5 --once --tui"},
		{"gauntlet itself can be the suggester", func(p *picker) {
			p.suggest = true
			p.opts[optSuggestAgent].idx = 1
		}, "-C /home/dev/project --suggest --suggest-agent gauntlet --once --tui"},
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

// The filter is a search across names and descriptions: it opens what it
// finds, hides what it does not, and typing never reaches the panes.
func TestPickFilterFindsByNameAndDescription(t *testing.T) {
	p := demoPicker()
	press(p, "/")
	if !p.typing {
		t.Fatal("/ should open the filter")
	}
	press(p, "q")
	if p.filter != "q" {
		t.Fatalf("filter is %q: q must type, not quit", p.filter)
	}
	press(p, "u", "i")
	names := []string{}
	for _, r := range p.rows() {
		if r.kind == rowReview {
			names = append(names, r.review.Name)
		}
	}
	if len(names) != 0 {
		t.Fatalf("no review matches \"qui\", got %v", names)
	}
	p.filter = "vulnerab" // a word only a description carries
	found := false
	for _, r := range p.rows() {
		if r.kind == rowReview && r.review.Name == "sec-review" {
			found = true
		}
	}
	if !found {
		t.Fatal("the filter must match what a review says it does, not only its name")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.typing || p.filter != "" {
		t.Fatal("esc should clear the filter and return the keys to the panes")
	}
}

// A filter that matches nothing hides the whole tree: the pane must say so
// and name the way out, or silence reads as an empty prompt set.
func TestPickEmptyFilterSaysSo(t *testing.T) {
	p := demoPicker()
	press(p, "/", "z", "z", "z") // nothing is named or described with zzz
	if view := stripANSI(p.View()); !strings.Contains(view, "no reviews match this filter") {
		t.Fatalf("a fruitless filter left no trace in the pane:\n%s", view)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter}) // keep the filter, leave typing
	if view := stripANSI(p.View()); !strings.Contains(view, "no reviews match this filter") {
		t.Fatalf("the kept filter lost its empty state:\n%s", view)
	}
	p.filter = "vulnerab" // a match brings the tree back and the notice goes
	if view := stripANSI(p.View()); strings.Contains(view, "no reviews match this filter") {
		t.Fatalf("a matching filter still shows the empty state:\n%s", view)
	}
}

// Discovery stores every review name NFC, while typed text arrives in
// whatever form the terminal sends it: a dead-key accent lands as its own
// rune after the letter. The filter must still find the review, the same way
// --reviews does on the command line.
func TestPickFilterMatchesDecomposedSpelling(t *testing.T) {
	p := demoPicker()
	p.cfg.Groups[1].Reviews = append(p.cfg.Groups[1].Reviews,
		PickReview{Name: "caf\u00e9-review", Desc: "taste and aroma", Project: true})
	p.filter = "cafe\u0301" // decomposed: e + combining acute
	found := false
	for _, r := range p.rows() {
		if r.kind == rowReview && r.review.Name == "caf\u00e9-review" {
			found = true
		}
	}
	if !found {
		t.Fatal("a decomposed spelling of the same word must match an NFC name")
	}
}

// Case in the filter is folded, not lowercased, so one spelling of a letter
// finds text spelled with another: lowercasing equates neither the Greek
// final and ordinary sigma nor the long and round s, folding equates both.
func TestPickFilterFoldsCase(t *testing.T) {
	p := demoPicker()
	p.cfg.Groups[1].Reviews = append(p.cfg.Groups[1].Reviews,
		PickReview{Name: "logos-review", Desc: "the \u03BB\u03BF\u03B3\u03BF\u03C2 of the code", Project: true})
	p.filter = "\u03BB\u03BF\u03B3\u039F\u03A3" // uppercase, ordinary sigma
	found := false
	for _, r := range p.rows() {
		if r.kind == rowReview && r.review.Name == "logos-review" {
			found = true
		}
	}
	if !found {
		t.Fatal("a query spelled with one sigma must find text spelled with the other")
	}
}

// One backspace is one keystroke's worth of text, not one code point: a
// combining accent typed separately from its letter must leave with it.
func TestPickBackspaceRemovesWholeCluster(t *testing.T) {
	p := demoPicker()
	press(p, "/")
	press(p, "c", "a", "f", "e")
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\u0301")})
	if got, want := p.filter, "cafe\u0301"; got != want {
		t.Fatalf("filter is %q, want %q", got, want)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got, want := p.filter, "caf"; got != want {
		t.Fatalf("backspace left %q, want %q: the accent must not outlive its letter", got, want)
	}
}

// Worktree isolation needs a clean tree, so the launcher says so instead of
// composing a command that fails on launch.
func TestPickRefusesConcurrencyOnADirtyTree(t *testing.T) {
	p := demoPicker()
	p.cfg.Dirty = true
	press(p, "+")
	if p.blocked() == "" {
		t.Fatal("a dirty tree with concurrency above 1 must be refused")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.launch {
		t.Fatal("enter launched a run the tree cannot support")
	}
	if !strings.Contains(p.View(), "clean tree") {
		t.Fatalf("the reason is not on screen:\n%s", p.View())
	}
	press(p, "-")
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.launch {
		t.Fatal("back at one job the run is fine, and enter should launch it")
	}
}

// Every composed run auto-detects its agents, so an empty pool cannot launch
// at all: enter must refuse here, with the reason on screen, rather than hand
// back a command that dies the moment it starts.
func TestPickRefusesToLaunchWithoutAgents(t *testing.T) {
	p := demoPicker()
	p.cfg.Agents = nil
	p.agents = nil
	if p.blocked() == "" {
		t.Fatal("an empty agent pool must be refused")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.launch {
		t.Fatal("enter launched a run that has no agents to run")
	}
	if !strings.Contains(p.View(), "gauntlet doctor") {
		t.Fatalf("the reason is not on screen:\n%s", p.View())
	}
}

// The status line is where a review's description is read whole: the pane
// column truncates every one of them.
func TestPickHintShowsTheFocusedReviewDescription(t *testing.T) {
	p := demoPicker()
	p.open[0] = true          // the tree starts collapsed; open quick to reach its rows
	p.cursor[paneReviews] = 2 // past suggest and the group header, on sec-review
	if got := p.hint(); got != "hunt for vulnerabilities" {
		t.Fatalf("hint %q, want the review's own description", got)
	}
	noDesc := PickReview{Name: "bare-review"}
	p.cfg.Groups[0].Reviews[0] = noDesc
	if got := p.hint(); got != "space takes this review on its own" {
		t.Fatalf("hint %q, want the action fallback when there is no description", got)
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

// The agents pane takes focus even when nothing is installed, so its
// empty state must carry the cursor bar: a screen with no ❯ leaves the
// keyboard nowhere to be.
func TestPickEmptyAgentsPaneKeepsFocusVisible(t *testing.T) {
	p := demoPicker()
	p.cfg.Agents = nil
	for _, focus := range []pane{paneReviews, paneAgents} {
		p.focus = focus
		view := stripANSI(p.View())
		hasCursor := strings.Contains(view, "❯ ")
		inAgents := strings.Contains(view, "❯ none installed")
		if !hasCursor || (focus == paneAgents) != inAgents {
			t.Fatalf("focus %d: cursor bar wrong (❯ present=%t, on the empty pane=%t):\n%s",
				focus, hasCursor, inAgents, view)
		}
	}
}

// A narrow terminal clips the key list from its right end, so the keys that
// strand a keyboard user who cannot find them must come first: how to run,
// leave, and move between rows are visible even at the launcher's own
// minimum size, and row movement is documented wherever the panels are.
func TestPickFooterKeepsCriticalKeysVisible(t *testing.T) {
	for _, w := range []int{104, 80, 50} {
		p := demoPicker()
		p.w, p.h, p.ready = w, 30, true
		footer := lastLine(p.View())
		if !strings.Contains(footer, ":run") || !strings.Contains(footer, ":cancel") {
			t.Fatalf("at %d columns the footer lost launch or quit:\n%s", w, footer)
		}
		if !strings.Contains(footer, ":move") {
			t.Fatalf("at %d columns the footer lost row movement:\n%s", w, footer)
		}
	}
	p := demoPicker()
	p.w, p.h, p.ready = 104, 30, true
	footer := lastLine(p.View())
	for _, want := range []string{":pane", ":toggle", ":open/close", ":filter"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("a wide terminal should document more than the essentials (%q missing):\n%s", want, footer)
		}
	}
}

// The launcher's narrow fallback clips like the dashboard's does: a wrapped
// fallback is a taller broken screen, not a smaller one.
func TestNarrowLauncherClipsToThePane(t *testing.T) {
	for _, w := range []int{10, 20, 30, 49} {
		p := demoPicker()
		p.w, p.h, p.ready = w, 10, true
		p.optByFlag("--commit").on = true // the widest argv the demo can compose
		for i, ln := range strings.Split(p.View(), "\n") {
			if got := lipgloss.Width(ln); got > w {
				t.Fatalf("w=%d: row %d is %d columns wide: %q", w, i, got, stripANSI(ln))
			}
		}
	}
}
