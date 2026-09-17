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
		Agents:      []string{"claude", "codex:gpt-5"},
		FastSuggest: "gauntlet",
		Branch:      "work",
		Merge:       []string{"main", "release"},
		CPUs:        8,
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
		{"suggest preserves every explicitly selected review", func(p *picker) {
			press(p, "a", " ")
		}, "-C /home/dev/project --suggest -r quick,frontend --once --tui"},
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
		{"stacked PRs are a flag of their own", func(p *picker) {
			p.optByFlag("--stacked-prs").on = true
		}, "-C /home/dev/project --once --tui --stacked-prs"},
		{"stacked PRs drop commit, push, merge, and jobs", func(p *picker) {
			p.optByFlag("--commit").on = true
			p.optByFlag("--push").on = true
			p.optByFlag("--merge-into").idx = 1
			p.concurrency().n = 4
			p.optByFlag("--stacked-prs").on = true
		}, "-C /home/dev/project --once --tui --stacked-prs"},
		{"leaving stacked PRs restores prior choices", func(p *picker) {
			p.optByFlag("--commit").on = true
			p.optByFlag("--merge-into").idx = 1
			p.concurrency().n = 4
			p.focus = paneOptions
			p.cursor[paneOptions] = 4
			p.toggle()
			p.toggle()
		}, "-C /home/dev/project -j 4 --once --tui --commit --merge-into main"},
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

// FastSuggest is passed in rather than imported from the runner, so a caller
// that does not name one must not grow a --suggest-agent gauntlet of its own.
func TestPickerOmitsFileSignalSuggesterWhenUnset(t *testing.T) {
	p := newPicker(PickConfig{
		Dir:    "/home/dev/project",
		Groups: []PickGroup{{Name: "quick", Reviews: []PickReview{{Name: "sec-review", Desc: "d"}}}},
		Agents: []string{"claude"},
		CPUs:   8,
	})
	p.w, p.h, p.ready = 100, 30, true
	p.suggest = true
	p.opts[optSuggestAgent].idx = 1
	got := strings.Join(p.argv(), " ")
	if strings.Contains(got, "--suggest-agent gauntlet") {
		t.Fatalf("an unset FastSuggest still composed the file-signal suggester:\n%s", got)
	}
	if !strings.Contains(got, "--suggest-agent claude") {
		t.Fatalf("idx 1 should be the first agent when FastSuggest is omitted:\n%s", got)
	}
}

// A group header toggles its members, and toggling a whole group off empties
// it rather than filling it again.
// The launcher drops the "-review" suffix to keep the composed command short,
// and --reviews resolves set names and the suggest keyword before it looks at
// review names. A tree carrying security-review.md would therefore be launched
// as "-r security", which runs the eight-review security set and not the one
// review that was ticked; "suggest" would turn on the triage agent instead.
// Those names are written out in full.
func TestPickNamesAReviewThatCollidesWithASetInFull(t *testing.T) {
	newPickerWith := func(names ...string) *picker {
		revs := make([]PickReview, 0, len(names))
		for _, n := range names {
			revs = append(revs, PickReview{Name: n, Desc: "d", Project: true})
		}
		p := newPicker(PickConfig{
			Dir:      "/home/dev/project",
			Groups:   []PickGroup{{Name: "project", Reviews: revs}},
			Agents:   []string{"claude"},
			CPUs:     8,
			Reserved: []string{"quick", "security", "project", "all", "suggest"},
		})
		p.w, p.h, p.ready = 100, 30, true
		return p
	}

	for _, c := range []struct{ pick, want string }{
		{"security-review", "security-review"}, // a set name
		{"suggest-review", "suggest-review"},   // the triage keyword
		{"cache-review", "cache"},              // nothing reserved: still abbreviated
	} {
		p := newPickerWith("security-review", "suggest-review", "cache-review")
		p.selected[c.pick] = true
		if got := p.reviewArgs(); got != c.want {
			t.Errorf("ticking %s composed -r %q, want %q", c.pick, got, c.want)
		}
	}

	// A whole group still collapses to its set name: that spelling is the set
	// on both sides, so it means what it says.
	p := newPickerWith("security-review", "suggest-review", "cache-review")
	for _, n := range []string{"security-review", "suggest-review", "cache-review"} {
		p.selected[n] = true
	}
	if got := p.reviewArgs(); got != "" {
		t.Errorf("selecting everything should name nothing, got %q", got)
	}
}

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
	for _, want := range []string{":pane", ":toggle", ":open/close", ":filter", ":help"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("a wide terminal should document more than the essentials (%q missing):\n%s", want, footer)
		}
	}
}

func TestPickHelpAgentSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		agents   []string
		selected []bool
		want     string
	}{
		{"no installed agents", nil, nil, ""},
		{"none selected", []string{"one", "two"}, []bool{false, false}, "  agents: auto-detect (all 2 installed)"},
		{"all selected", []string{"one", "two"}, []bool{true, true}, "  agents: auto-detect (all 2 installed)"},
		{"subset selected", []string{"one", "two", "three"}, []bool{true, false, true}, "  agents: one, three"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPicker(PickConfig{Agents: tc.agents})
			p.agents = tc.selected
			var got []string
			for _, line := range p.helpLines() {
				if strings.HasPrefix(line, "  agents:") {
					got = append(got, line)
				}
			}
			if text := strings.Join(got, "\n"); text != tc.want {
				t.Fatalf("agent summary = %q, want %q", text, tc.want)
			}
		})
	}
}

// ? opens a help overlay the way the dashboard does. q on that overlay
// closes it rather than leaving the launcher, so a reader who opened help
// does not cancel the run they were composing.
func TestPickHelpOverlayClosesWithoutLeaving(t *testing.T) {
	p := demoPicker()
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !p.help {
		t.Fatal("? did not open help")
	}
	got := stripANSI(p.View())
	for _, want := range []string{"close this help", "Picking no reviews runs all of them", "tab", "stacked PRs"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help lost %q:\n%s", want, got)
		}
	}
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if p.help {
		t.Fatal("q on the overlay should close help")
	}
	if p.launch {
		t.Fatal("q on the overlay must not launch")
	}
	// A later q, with the view back, still leaves.
	press(p, "q")
	if p.launch {
		t.Fatal("q should leave without launching")
	}
}

// Stack mode owns commits and the job count: conflicting choices are kept for
// later but omitted while stacking, and +/- must not sneak a -j back in.
func TestPickStackedPRsPreserveConflictingOptions(t *testing.T) {
	p := demoPicker()
	p.concurrency().n = 4
	p.optByFlag("--push").on = true
	p.optByFlag("--merge-into").idx = 1
	p.focus = paneOptions
	for i, o := range p.opts {
		if o.flag == "--stacked-prs" {
			p.cursor[paneOptions] = i
			break
		}
	}
	p.toggle()
	if !p.stacked() {
		t.Fatal("space did not turn stacked PRs on")
	}
	if p.concurrency().n != 4 {
		t.Fatalf("jobs %d, want preserved value 4 after stacked PRs", p.concurrency().n)
	}
	if !p.optByFlag("--push").on {
		t.Fatal("push choice was lost under stacked PRs")
	}
	if p.optByFlag("--merge-into").idx != 1 {
		t.Fatal("merge target was lost under stacked PRs")
	}
	press(p, "+", "+")
	if p.concurrency().n != 4 {
		t.Fatal("+ still raised jobs under stacked PRs")
	}
	if got := strings.Join(p.argv(), " "); got != "-C /home/dev/project --once --tui --stacked-prs" {
		t.Fatalf("argv is %q", got)
	}
}

func TestPickStackedPRsIgnoreSavedConcurrencyOnDirtyTree(t *testing.T) {
	p := demoPicker()
	p.cfg.Dirty = true
	press(p, "+")
	p.focus = paneOptions
	for i, o := range p.opts {
		if o.flag == "--stacked-prs" {
			p.cursor[paneOptions] = i
			break
		}
	}
	press(p, " ")
	if got := p.blocked(); got != "" {
		t.Fatalf("stacked launch blocked by an inactive option: %s", got)
	}
	if strings.Contains(stripANSI(p.View()), "clean tree") {
		t.Fatal("stack mode still shows the concurrency warning")
	}
	press(p, " ")
	if p.concurrency().n != 2 || p.blocked() == "" {
		t.Fatal("leaving stack mode must restore concurrency and its validation")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.launch {
		t.Fatal("parallel mode launched on a dirty tree")
	}
	press(p, " ")
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.launch || cmd == nil {
		t.Fatal("stacked mode did not hand off to run preflight")
	}
	if got := strings.Join(p.argv(), " "); got != "-C /home/dev/project --once --tui --stacked-prs" {
		t.Fatalf("stacked command includes inactive options: %s", got)
	}
}

func TestPickHelpOverlayScrollsAndRestoresFocus(t *testing.T) {
	p := demoPicker()
	p.w, p.h = 40, 6
	p.focus = paneOptions
	p.cursor[paneOptions] = 2
	press(p, "?")
	first := stripANSI(p.View())
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if stripANSI(p.View()) == first {
		t.Fatal("page down did not scroll help")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if stripANSI(p.View()) != first {
		t.Fatal("page up did not return to the first page")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(stripANSI(p.View()), "previous one") {
		t.Fatal("the end of the last instruction is unreachable")
	}
	press(p, "q")
	if p.help || p.launch || p.focus != paneOptions || p.cursor[paneOptions] != 2 {
		t.Fatal("help navigation changed the launcher's focus or selection")
	}
	press(p, "?")
	if stripANSI(p.View()) != first {
		t.Fatal("reopening help did not start at the top")
	}
}

func TestPickHelpExposesFocusedControl(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*picker)
		want  []string
	}{
		{"review", func(p *picker) {
			p.cfg.Groups[0].Reviews[0] = PickReview{
				Name: "very-long-review-name-review", Desc: "a description with an otherwise unreachable ending", Project: true,
			}
			p.open[0] = true
			p.cursor[paneReviews] = 2
		}, []string{"very-long-review-name [project]", "not selected", "otherwise unreachable ending"}},
		{"selected review", func(p *picker) {
			p.open[0] = true
			p.cursor[paneReviews] = 2
			p.selected["sec-review"] = true
		}, []string{"sec: selected", "hunt for vulnerabilities"}},
		{"filtered group", func(p *picker) {
			p.filter = "sec"
			p.cursor[paneReviews] = 1
		}, []string{"quick: expanded", "0 of 2 selected"}},
		{"collapsed group", func(p *picker) {
			p.cursor[paneReviews] = 1
		}, []string{"quick: collapsed"}},
		{"agent", func(p *picker) {
			p.focus = paneAgents
			p.cfg.Agents[0] = "agent-with-a-very-long-model-label"
		}, []string{"agent-with-a-very-long-model-label", "not selected"}},
		{"empty agents", func(p *picker) {
			p.focus = paneAgents
			p.cfg.Agents = nil
		}, []string{"no agents installed"}},
		{"off toggle", func(p *picker) {
			p.focus = paneOptions
			p.cursor[paneOptions] = 5
		}, []string{"commit: off", "on this branch"}},
		{"default cycle", func(p *picker) {
			p.focus = paneOptions
			p.cursor[paneOptions] = optSuggestAgent
		}, []string{"suggest agent: from the pool", "suggest is off"}},
		{"inert option", func(p *picker) {
			p.focus = paneOptions
			p.optByFlag("--stacked-prs").on = true
		}, []string{"concurrency: 1", "stacked PRs own this"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoPicker()
			tc.setup(p)
			p.w, p.h = 40, 6
			focus, cursor := p.focus, p.cursor
			press(p, "?")
			var seen strings.Builder
			for range 150 {
				view := stripANSI(p.View())
				if lipgloss.Width(view) > p.w || lipgloss.Height(view) > p.h {
					t.Fatalf("help exceeds the viewport: %s", view)
				}
				seen.WriteString(strings.Join(strings.Fields(view), " "))
				seen.WriteByte('\n')
				p.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			for _, want := range tc.want {
				if !strings.Contains(seen.String(), want) {
					t.Errorf("focused control detail %q is unreachable", want)
				}
			}
			p.Update(tea.KeyMsg{Type: tea.KeyEsc})
			if p.focus != focus || p.cursor != cursor || p.launch {
				t.Fatal("reading control details changed focus or launched the run")
			}
		})
	}
}

func TestPickHelpOverlayFitsThePane(t *testing.T) {
	p := demoPicker()
	p.help = true
	for _, size := range [][2]int{{20, 6}, {40, 10}, {80, 24}} {
		p.w, p.h = size[0], size[1]
		view := p.View()
		for i, ln := range strings.Split(view, "\n") {
			if got := lipgloss.Width(ln); got > size[0] {
				t.Fatalf("%dx%d: row %d is %d columns: %q",
					size[0], size[1], i, got, stripANSI(ln))
			}
		}
		if rows := strings.Split(view, "\n"); len(rows) > size[1] {
			t.Fatalf("%dx%d: help is %d rows", size[0], size[1], len(rows))
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

// A filter is a search: bulk select and a set header must not reach through
// it and toggle reviews the tree is not showing.
func TestPickFilterBoundsBulkSelect(t *testing.T) {
	p := demoPicker()
	p.toggleAll() // all four selected
	p.filter = "ux"
	p.toggleAll()
	if p.selected["ux-review"] {
		t.Fatal("a on a filtered pane should clear the visible review")
	}
	for _, name := range []string{"sec-review", "code-review", "a11y-review"} {
		if !p.selected[name] {
			t.Fatalf("%s was hidden by the filter and still got cleared", name)
		}
	}
	p.toggleAll()
	if !p.selected["ux-review"] {
		t.Fatal("a on an empty visible set should fill it")
	}
	if p.chosen() != 4 {
		t.Fatalf("filling the visible set touched hidden rows: chosen %d", p.chosen())
	}
}

func TestPickFilterBoundsGroupToggle(t *testing.T) {
	p := demoPicker()
	p.filter = "ux"
	p.cursor[paneReviews] = 1 // frontend header; quick has no match
	p.toggle()
	if !p.selected["ux-review"] {
		t.Fatal("space on the filtered group should take the visible member")
	}
	if p.selected["a11y-review"] {
		t.Fatal("space on the filtered group selected a hidden member")
	}
}

// While the filter is open, enter keeps it and q types. The footer has to
// name those keys, or it keeps advertising a launch the key no longer does.
func TestPickFilterFooterNamesFilterKeys(t *testing.T) {
	p := demoPicker()
	press(p, "/")
	footer := lastLine(stripANSI(p.View()))
	for _, want := range []string{":keep", ":clear"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("typing a filter, footer lost %q:\n%s", want, footer)
		}
	}
	for _, dead := range []string{":run", ":cancel"} {
		if strings.Contains(footer, dead) {
			t.Fatalf("typing a filter, footer still advertises %q:\n%s", dead, footer)
		}
	}
}

func TestPickWarmupMatchesTheDashboard(t *testing.T) {
	p := newPicker(PickConfig{})
	if got := p.View(); !strings.Contains(got, "warming up") {
		t.Fatalf("an unready launcher was blank: %q", got)
	}
}

func TestPickHomeEndJumpThePane(t *testing.T) {
	p := demoPicker()
	p.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if want := len(p.rows()) - 1; p.cursor[paneReviews] != want {
		t.Fatalf("end left cursor %d, want the last row %d", p.cursor[paneReviews], want)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyHome})
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("home left cursor %d, want the first row", p.cursor[paneReviews])
	}
}

// When a filter was kept with enter, pressing esc clears the filter rather
// than abruptly quitting the application and discarding composed options.
func TestPickClearingKeptFilterKeepsCursorOnVisibleRow(t *testing.T) {
	p := demoPicker()
	p.selected["sec-review"] = true
	press(p, "/", "review")
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	p.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if p.rowAt(p.cursor[paneReviews]).review.Name != "a11y-review" {
		t.Fatal("search did not reach the last review")
	}
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || p.filter != "" || p.typing {
		t.Fatal("clearing the kept filter did not return to the catalog")
	}
	if view := stripANSI(p.View()); !strings.Contains(view, "❯") {
		t.Fatalf("clearing search left no visible selection:\n%s", view)
	}
	if cur := p.cursor[paneReviews]; cur < 0 || cur >= len(p.rows()) {
		t.Fatalf("cursor %d is outside the restored catalog", cur)
	}
	if !p.selected["sec-review"] {
		t.Fatal("clearing search discarded a selected review")
	}
	press(p, " ")
	if !p.selected["ux-review"] || !p.selected["a11y-review"] {
		t.Fatal("toggle did not select the visible frontend group")
	}
}

func TestPickEscClearsKeptFilterInsteadOfQuitting(t *testing.T) {
	p := demoPicker()
	press(p, "/", "s", "e", "c")
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.typing || p.filter != "sec" {
		t.Fatalf("filter should be kept at 'sec' while typing is false, got filter=%q typing=%t", p.filter, p.typing)
	}
	out, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc on a kept filter must not quit")
	}
	done, ok := out.(*picker)
	if !ok || done.filter != "" {
		t.Fatalf("esc should clear the filter, got filter=%q", done.filter)
	}
	// A second esc now that the filter is gone does quit.
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc on an unfiltered picker must quit")
	}
}

func TestPickFrameFitsTerminal(t *testing.T) {
	for _, width := range []int{50, 80, 160} {
		p := demoPicker()
		p.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		press(p, "tab", "tab", "+")
		for line := range strings.SplitSeq(p.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d: row occupies %d columns: %q", width, got, stripANSI(line))
			}
		}
	}
}

func TestPickRunOptionsKeepValuesInNarrowPanels(t *testing.T) {
	for _, width := range []int{50, 60, 80, 100, 160} {
		p := demoPicker()
		p.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		press(p, "tab", "tab", "+")
		view := stripANSI(p.View())
		if !strings.Contains(view, "2/8 cpu") {
			t.Fatalf("width %d hides the updated concurrency:\n%s", width, view)
		}
		if width == 160 && !strings.Contains(view, "▰▰▱▱▱▱▱▱ 2/8 cpu") {
			t.Fatalf("wide panel lost its concurrency meter:\n%s", view)
		}
		p.suggest = true
		press(p, "j", "right")
		view = stripANSI(p.View())
		found := false
		for line := range strings.SplitSeq(view, "\n") {
			if strings.Contains(line, "❯") && strings.Contains(line, "gauntlet") {
				found = true
			}
		}
		if !found {
			t.Fatalf("width %d hides the selected suggester on its row:\n%s", width, view)
		}
		press(p, "j", "j", "j", "j", " ", "j", "j", "right")
		view = stripANSI(p.View())
		found = false
		for line := range strings.SplitSeq(view, "\n") {
			if strings.Contains(line, "❯") && strings.Contains(line, "main") {
				found = true
			}
		}
		if !found || !strings.Contains(strings.Join(p.argv(), " "), "--merge-into main") {
			t.Fatalf("width %d hides or changes the selected merge target:\n%s", width, view)
		}
	}
}

// Pressing space on the concurrency option cycles it through 1..CPUs so
// the primary toggle key works on every pane row instead of doing nothing.
func TestPickSpaceCyclesConcurrency(t *testing.T) {
	p := demoPicker()
	p.cfg.CPUs = 4
	p.focus = paneOptions
	p.cursor[paneOptions] = 0 // concurrency
	if p.concurrency().n != 1 {
		t.Fatalf("initial concurrency=%d, want 1", p.concurrency().n)
	}
	press(p, " ")
	if p.concurrency().n != 2 {
		t.Fatalf("concurrency after space=%d, want 2", p.concurrency().n)
	}
	press(p, " ", " ")
	if p.concurrency().n != 4 {
		t.Fatalf("concurrency after two more spaces=%d, want 4", p.concurrency().n)
	}
	press(p, " ")
	if p.concurrency().n != 1 {
		t.Fatalf("concurrency after cycling past CPUs=%d, want 1", p.concurrency().n)
	}
}

// Turning on stacked PRs via right arrow or 'l' preserves conflicting options
// just as space does.
func TestPickRightArrowOnStackedPRsPreservesConflicts(t *testing.T) {
	p := demoPicker()
	p.concurrency().n = 4
	p.optByFlag("--push").on = true
	p.optByFlag("--merge-into").idx = 1
	p.focus = paneOptions
	for i, o := range p.opts {
		if o.flag == "--stacked-prs" {
			p.cursor[paneOptions] = i
			break
		}
	}
	press(p, "l")
	if !p.stacked() {
		t.Fatal("'l' did not turn stacked PRs on")
	}
	if p.concurrency().n != 4 {
		t.Fatalf("jobs %d, want preserved value 4 after stacked PRs", p.concurrency().n)
	}
	if !p.optByFlag("--push").on {
		t.Fatal("push choice was lost under stacked PRs")
	}
	if p.optByFlag("--merge-into").idx != 1 {
		t.Fatal("merge target was lost under stacked PRs")
	}
}

// Hints for dimmed/inactive options must explain why they are inactive
// and how to activate them.
func TestPickHintExplainsDimmedOptions(t *testing.T) {
	p := demoPicker()
	p.focus = paneOptions
	for i, o := range p.opts {
		if o.flag == "--suggest-agent" {
			p.cursor[paneOptions] = i
			break
		}
	}
	if got := p.hint(); !strings.Contains(got, "suggest is off") {
		t.Fatalf("suggest agent hint %q, want explanation that suggest is off", got)
	}
	for i, o := range p.opts {
		if o.flag == "--merge-into" {
			p.cursor[paneOptions] = i
			break
		}
	}
	if got := p.hint(); !strings.Contains(got, "commits are off") {
		t.Fatalf("merge into hint %q, want explanation that commits are off", got)
	}
}

// When a filter is active, the footer documents esc:clear so the user
// knows how to return to the full review catalog.
func TestPickFooterShowsClearFilterWhenFilterActive(t *testing.T) {
	p := demoPicker()
	p.filter = "sec"
	p.typing = false
	footer := lastLine(stripANSI(p.View()))
	if !strings.Contains(footer, "esc:clear") {
		t.Fatalf("filtered footer %q, want esc:clear documented", footer)
	}
}
