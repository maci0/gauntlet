// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
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
		// The group names are set names, the way the launcher builds them.
		Reserved: []string{"quick", "frontend", "all", "suggest"},
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

// The narrow fallback draws no panels, so the composed command is the whole
// screen and / is the only key that can still change it. A key line that
// stops before it leaves the reader with nothing to compose from.
func TestNarrowLauncherNamesTheFilterKey(t *testing.T) {
	for _, state := range []struct {
		name  string
		setup func(*picker)
	}{
		{"at rest", func(*picker) {}},
		{"a kept filter", func(p *picker) { p.filter = "sec" }},
	} {
		t.Run(state.name, func(t *testing.T) {
			p := demoPicker()
			p.w, p.h = 40, 10
			state.setup(p)
			if got := stripANSI(p.renderNarrow()); !strings.Contains(got, "/ filter") {
				t.Fatalf("the narrow fallback does not name the filter key:\n%s", got)
			}
		})
	}
}

// A terminal one row tall keeps the keys, the way the dashboard's fallback
// does. Two rows on one line scroll, and the command is what scrolls away.
func TestNarrowLauncherKeepsTheKeysOnOneRow(t *testing.T) {
	p := demoPicker()
	p.w, p.h = 40, 1
	rows := strings.Split(stripANSI(p.renderNarrow()), "\n")
	if len(rows) != 1 {
		t.Fatalf("a one-row terminal drew %d rows:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(rows[0], "q cancel") {
		t.Fatalf("a one-row terminal dropped the keys:\n%s", rows[0])
	}
}

// The narrow fallback draws no pane, so the arrow keys move a cursor nothing
// on the screen points at. Naming them there is a key a keyboard user cannot
// tell from a broken one (WCAG 3.3.2), and the wide legend drops dead keys
// for the same reason.
func TestNarrowLauncherDoesNotNameDeadArrowKeys(t *testing.T) {
	p := demoPicker()
	p.w, p.h = 40, 10
	press(p, "/", "s", "e", "c")
	if got := stripANSI(p.renderNarrow()); strings.Contains(got, "↑↓") {
		t.Fatalf("the narrow fallback names arrow keys over a pane it does not draw:\n%s", got)
	}
	if !strings.Contains(stripANSI(p.renderNarrow()), "esc clear") {
		t.Fatal("the narrow fallback dropped the key that clears the filter")
	}
}

// The narrow fallback is a screen the reader reached by having too little
// room, so it has to name the way out. Resizing draws the panes on the next
// frame, and only a reader told the size the panes need can act on that.
func TestNarrowLauncherSaysHowToGetThePanelsBack(t *testing.T) {
	p := demoPicker()
	p.w, p.h = 40, 10
	got := stripANSI(p.renderNarrow())
	want := fmt.Sprintf("launcher needs %d×%d", minPickerW, minPickerH)
	if !strings.Contains(got, want) || !strings.Contains(got, "resize") {
		t.Fatalf("the narrow fallback does not name the size that brings the panes back:\n%s", got)
	}
}

// A reason the run cannot start is what makes enter dead, and opening the
// filter did not make it any less true. Typing used to put the filter line
// there instead, so a box with no agent CLI installed read as a working
// launcher for as long as the search was open.
func TestBlockReasonSurvivesTheFilterLine(t *testing.T) {
	p := demoPicker()
	p.cfg.Agents = nil
	press(p, "/", "s", "e", "c")
	if !p.typing {
		t.Fatal("the filter did not open")
	}
	got := stripANSI(p.renderStatus())
	if !strings.Contains(got, "no agent CLI is installed") {
		t.Fatalf("the filter line hid the reason the run cannot start:\n%s", got)
	}
	// The search's own state is not a machine problem, so a filter that has
	// matched nothing stays the hint's sentence rather than becoming a warning.
	q := demoPicker()
	press(q, "/", "z", "z", "z")
	if got := stripANSI(q.renderStatus()); strings.Contains(got, "⚠") {
		t.Fatalf("a fruitless search warned on every keystroke:\n%s", got)
	}
	if got := stripANSI(q.renderStatus()); !strings.Contains(got, "no reviews match") {
		t.Fatalf("the hint lost the fruitless search:\n%s", got)
	}
}

// esc is the way back, so the help has to say what it goes back from: a
// reader who pressed it once to dismiss something and found the launcher
// gone has no way to know it also leaves with nothing left to clear.
func TestLauncherHelpSaysWhatEscDoes(t *testing.T) {
	p := demoPicker()
	lines := stripANSI(strings.Join(p.helpLines(), "\n"))
	if !strings.Contains(lines, "esc") || !strings.Contains(lines, "leave once there is nothing to clear") {
		t.Fatalf("the help does not say that esc leaves:\n%s", lines)
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

// The job count is drawn against the machine's cpus and the summary reads
// "N of M cpus", so no key that raises it may pass M. The arrow keys used to
// come through adjust() unbounded, and the row is what composes --jobs, so
// holding right set the run to a lane count the machine cannot give.
func TestConcurrencyStopsAtTheMachineCPUs(t *testing.T) {
	for _, c := range []struct {
		name string
		act  func(p *picker)
	}{
		{"plus key", func(p *picker) { press(p, "+", "+", "+", "+", "+", "+", "+") }},
		{"right arrow on the options row", func(p *picker) {
			p.focus = paneOptions
			press(p, "l", "l", "l", "l", "l", "l", "l")
		}},
		{"space on the options row", func(p *picker) {
			p.focus = paneOptions
			for range 7 {
				p.toggle()
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := demoPicker()
			c.act(p)
			if got := p.concurrency().n; got != p.cfg.CPUs {
				t.Errorf("concurrency is %d after raising it, want the %d cpus", got, p.cfg.CPUs)
			}
		})
	}

	// A machine reporting one cpu is one lane: the legend drops the +/-
	// keys there, and the row has to agree rather than compose "-j 2 of 1".
	p := demoPicker()
	p.cfg.CPUs = 1
	press(p, "+", "+", "l", "l", " ")
	if got := p.concurrency().n; got != 1 {
		t.Errorf("concurrency is %d on a one-cpu machine, want 1", got)
	}
	if got := strings.Join(p.argv(), " "); strings.Contains(got, "-j") {
		t.Errorf("a one-cpu machine composed %q, want no -j at all", got)
	}
}

// Turning suggest on raises the next question a suggested run asks, and the
// run pane is where it is answered. The keys have to go there with the cursor:
// a pane draws its cursor bar only where the keys act, so a cursor pointed at
// a row in another pane left the row it pointed at looking like any other, and
// the toggle that had just happened went unmarked on screen.
func TestSuggestToggleTakesTheKeyboardToTheSuggesterRow(t *testing.T) {
	p := demoPicker()
	p.toggle()
	if !p.suggest {
		t.Fatal("space on the suggest row did not turn suggest on")
	}
	if p.focus != paneOptions || p.cursor[paneOptions] != optSuggestAgent {
		t.Fatalf("the keyboard is on pane %d row %d, want the run pane's suggest agent (row %d)",
			p.focus, p.cursor[paneOptions], optSuggestAgent)
	}
	// A focus nothing on screen marks is not orientation, so the bar has to be
	// drawn on the row it names.
	pane := stripANSI(p.runPanel(40, len(p.opts)))
	marked := false
	for ln := range strings.SplitSeq(pane, "\n") {
		if strings.Contains(ln, "suggest agent") {
			marked = strings.Contains(ln, "❯")
		}
	}
	if !marked {
		t.Fatalf("the suggest agent row carries no cursor bar:\n%s", pane)
	}
}

// A filter that matched nothing is reported twice: by the pane that has
// nothing to draw and by the status line that owes the reader a next action.
// One literal says it, so the two cannot drift into sentences a reader has to
// reconcile, the way the missing-agent sentence is not written twice.
func TestFilterMissIsOneSentenceEverywhereItIsReported(t *testing.T) {
	p := demoPicker()
	p.filter = "nothing-matches-this"
	rows := p.rows()
	if !p.filterMissed(rows) {
		t.Fatal("the demo filter matched a review")
	}
	if got := stripANSI(p.reviewPanel(60, p.paneHeight(paneReviews))); !strings.Contains(got, filterMissedMsg) {
		t.Fatalf("the reviews pane does not carry the shared notice:\n%s", got)
	}
	if got := stripANSI(p.hint()); got != filterMissedMsg {
		t.Fatalf("the status hint is %q, want the shared %q", got, filterMissedMsg)
	}
}

// A filter one typo from a review says which review it meant. The review tree
// is the only search this screen has, and "nothing matched" under a word the
// reader can see the shape of is the dead end --reviews and the agent spec
// parser both answer with a "did you mean", so the launcher is the odd screen
// out. The hint while typing matters most: that is the moment the spelling
// is still being written.
func TestFilterNearMissNamesTheReviewItMeant(t *testing.T) {
	p := demoPicker()
	p.filter = "secreview"
	if !p.filterMissed(p.rows()) {
		t.Fatal("the near-miss filter matched a review, so there is nothing to suggest")
	}
	// Every surface that reports the miss carries the same suggestion, so a
	// reader who learns the name from one of them finds the others agree.
	if got := p.filterMissedMessage(); !strings.Contains(got, "sec-review") {
		t.Errorf("the shared notice does not name the review: %q", got)
	}
	if got := stripANSI(p.reviewPanel(60, p.paneHeight(paneReviews))); !strings.Contains(got, "sec-review") {
		t.Errorf("the reviews pane does not name the review:\n%s", got)
	}
	if got := stripANSI(p.hint()); !strings.Contains(got, "sec-review") {
		t.Errorf("the status hint does not name the review: %q", got)
	}
	// While the filter is still open the same suggestion rides the line that
	// carries the typed text, which is the only line the reader is watching.
	p.typing = true
	if got := stripANSI(p.hint()); !strings.Contains(got, "did you mean") || !strings.Contains(got, "sec-review") {
		t.Errorf("the typing hint does not offer the near review: %q", got)
	}
}

// A filter nothing resembles is not a misspelling of any one review, so the
// bare sentence stands: a suggestion offered on every fruitless search stops
// being read as an answer to this one.
func TestFilterFarMissKeepsTheBareSentence(t *testing.T) {
	p := demoPicker()
	p.filter = "zzzzqqqq"
	if !p.filterMissed(p.rows()) {
		t.Fatal("the far-miss filter matched a review")
	}
	if got := p.filterMissedMessage(); got != filterMissedMsg {
		t.Errorf("the far miss grew a suggestion: %q", got)
	}
	p.typing = true
	if got := stripANSI(p.hint()); strings.Contains(got, "did you mean") {
		t.Errorf("the typing hint suggests a review for a filter like this: %q", got)
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

// The catch-all group is a heading the launcher gives reviews no set claims,
// not a set --reviews knows. Ticking it whole names its members instead, since
// composing "-r other" is a command line the parser refuses.
func TestPickNamesTheCatchAllGroupByItsMembers(t *testing.T) {
	p := newPicker(PickConfig{
		Dir: "/home/dev/project",
		Groups: []PickGroup{
			{Name: "quick", Reviews: []PickReview{{Name: "sec-review", Desc: "d"}}},
			{Name: "other", Reviews: []PickReview{{Name: "cache-review", Desc: "d"}}},
		},
		Agents:   []string{"claude"},
		CPUs:     8,
		Reserved: []string{"quick", "all", "suggest"},
	})
	p.w, p.h, p.ready = 100, 30, true
	p.knownReviews = []string{"sec-review", "cache-review"}
	p.selected["sec-review"] = true
	p.selected["cache-review"] = true
	p.suggest = true
	if got, want := p.reviewArgs(), "quick,cache"; got != want {
		t.Fatalf("ticking the catch-all group composed -r %q, want %q", got, want)
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

// The filter is a search across set names, review names, and descriptions: it
// opens what it finds, hides what it does not, and typing never reaches the
// panes.
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
	press(p, "q", "u", "e")
	names := []string{}
	for _, r := range p.rows() {
		if r.kind == rowReview {
			names = append(names, r.review.Name)
		}
	}
	if len(names) != 0 {
		t.Fatalf("no review matches \"que\", got %v", names)
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

// A set's name is on screen while the reader types, so a needle carrying it
// has to find the set. The fruitless-filter notice under a word standing in
// the tree teaches the reader the word is not there, and sends them spelling
// out a review name they never had to know.
func TestPickFilterFindsBySetName(t *testing.T) {
	p := demoPicker()
	press(p, "/")
	press(p, "q", "u", "i", "c", "k")
	if p.filterMissed(p.rows()) {
		t.Fatalf("the set named %q is on screen, so filtering it must not report a miss:\n%s",
			p.filter, stripANSI(p.reviewPanel(50, 20)))
	}
	want := map[string]bool{"sec-review": false, "code-review": false}
	for _, r := range p.rows() {
		if r.kind == rowReview {
			want[r.review.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("filtering by the set name left %s out: %v", name, want)
		}
	}
	if got := p.chosen(); got != 0 {
		t.Fatalf("a filter selected %d reviews, want none: filtering is not ticking", got)
	}
}

func TestPickGAndGKeysJumpToTopAndBottom(t *testing.T) {
	p := demoPicker()
	press(p, "G")
	if last := len(p.rows()) - 1; p.cursor[paneReviews] != last {
		t.Fatalf("G did not jump to last row (%d), got %d", last, p.cursor[paneReviews])
	}
	press(p, "g")
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("g did not jump to first row, got %d", p.cursor[paneReviews])
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
	if p.blocked() == "" {
		t.Fatal("empty filter must be reported by blocked()")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter}) // try to launch while filter matches nothing
	if p.launch {
		t.Fatal("enter launched a run while active filter matched zero reviews")
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
	// Delete key also trims cluster for keyboards that send KeyDelete
	p.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got, want := p.filter, "ca"; got != want {
		t.Fatalf("delete key left %q, want %q", got, want)
	}
	// Ctrl+H (traditional terminal backspace) trims cluster as well
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	if got, want := p.filter, "c"; got != want {
		t.Fatalf("ctrl+h left %q, want %q", got, want)
	}
}

func TestPickFilterEditingAndNavigation(t *testing.T) {
	p := demoPicker()
	press(p, "/")
	if !p.typing {
		t.Fatal("/ did not enter filter typing mode")
	}
	press(p, "c", "o", "d", "e", " ", "t", "e", "s", "t")
	if got, want := p.filter, "code test"; got != want {
		t.Fatalf("filter is %q, want %q", got, want)
	}
	// Ctrl+w deletes the last word
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if got, want := p.filter, "code "; got != want {
		t.Fatalf("ctrl+w left %q, want %q", got, want)
	}
	// Ctrl+u clears the entire filter
	p.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if p.filter != "" {
		t.Fatalf("ctrl+u left %q, want empty filter", p.filter)
	}

	// Tab exits filter and moves to next pane (paneAgents)
	press(p, "s", "e", "c")
	p.Update(tea.KeyMsg{Type: tea.KeyTab})
	if p.typing {
		t.Fatal("tab did not exit typing mode")
	}
	if p.focus != paneAgents {
		t.Fatalf("tab focused pane %d, want paneAgents (%d)", p.focus, paneAgents)
	}

	// Shift+Tab from typing exits and moves to previous pane (paneOptions)
	press(p, "/")
	p.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if p.typing {
		t.Fatal("shift+tab did not exit typing mode")
	}
	if p.focus != paneOptions {
		t.Fatalf("shift+tab focused pane %d, want paneOptions (%d)", p.focus, paneOptions)
	}

	// Enter moves cursor to the first matching review, not the suggest row
	p.filter = ""
	p.focus = paneReviews
	p.cursor[paneReviews] = 0 // on suggest row
	press(p, "/", "s", "e", "c")
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.typing {
		t.Fatal("enter did not exit typing mode")
	}
	curRow := p.rowAt(p.cursor[paneReviews])
	if curRow.kind != rowReview || curRow.review.Name != "sec-review" {
		t.Fatalf("enter did not place cursor on matching review, got row kind=%d name=%q",
			curRow.kind, curRow.review.Name)
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

// Enter launches. q leaves with nothing, but only on the second press: a
// composed run is a screenful of picking, and the dashboard arms the same key
// for the same reason.
func TestPickQuitKeys(t *testing.T) {
	p := demoPicker()
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !p.launch {
		t.Fatal("enter should launch")
	}
	p = demoPicker()
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Fatal("the first q must not leave")
	}
	if !strings.Contains(stripANSI(p.renderStatus()), "q again to discard") {
		t.Fatalf("an armed q says nothing on screen:\n%s", stripANSI(p.renderStatus()))
	}
	if !strings.Contains(stripANSI(p.renderKeys()), "q:discard") {
		t.Fatalf("the key line still offers to cancel:\n%s", stripANSI(p.renderKeys()))
	}
	// esc takes the arm back rather than clearing an empty filter, so the
	// run is still there to be launched.
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc must cancel the arm, not leave")
	}
	if p.quitArmed {
		t.Fatal("esc did not take the arm back")
	}
	if p.quitKey != "" {
		t.Fatalf("a disarmed picker still names the key that asked: %q", p.quitKey)
	}
	press(p, "q")
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("a second q should leave")
	}
	if p.launch {
		t.Fatal("q should leave without launching")
	}
}

// esc is the key a keyboard user reaches for to back out of whatever they are
// in, so with nothing left to go back from it has to ask before it throws the
// composed run away (WCAG 3.3.4). One press used to leave, and the help's
// "leave once there is nothing to clear" read as a warning about losing the
// picking, which is exactly what it was.
func TestPickEscAsksBeforeLeaving(t *testing.T) {
	p := demoPicker()
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("the first esc must not leave")
	}
	if !p.quitArmed || p.quitKey != "esc" {
		t.Fatalf("esc did not arm itself: armed=%t key=%q", p.quitArmed, p.quitKey)
	}
	status := stripANSI(p.renderStatus())
	if !strings.Contains(status, "esc again to discard") || !strings.Contains(status, "q to keep it") {
		t.Fatalf("an armed esc does not say what confirms and what declines it:\n%s", status)
	}
	// The legend may not offer two keys that both discard the run: the one
	// that armed the ask confirms it, the other takes it back.
	keys := stripANSI(p.renderKeys())
	if !strings.Contains(keys, "esc:discard") || !strings.Contains(keys, "q:keep") {
		t.Fatalf("the key line does not match the armed key:\n%s", keys)
	}
	// The other key declines rather than confirms, so a reader reaching for
	// q out of habit is not taken as a second press of the wrong one.
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Fatal("q must take an armed esc back, not leave")
	}
	if p.quitArmed {
		t.Fatal("q did not take the esc arm back")
	}
	// And a second esc, with the same key that armed it, does leave.
	press(p, "esc")
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("a second esc must leave")
	}
	if p.launch {
		t.Fatal("esc should leave without launching")
	}
}

// ctrl+c closes every other screen, so it has to close this one too. From the
// filter it cleared the search instead, which left a reader who reached for it
// to leave still in the launcher, their search gone (WCAG 3.3.2).
func TestPickCtrlCQuitsWhileFiltering(t *testing.T) {
	p := demoPicker()
	press(p, "/", "s", "e", "c")
	if !p.typing {
		t.Fatal("the picker is not typing, so the test is not testing it")
	}
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c while filtering must leave the launcher")
	}
	if p.launch {
		t.Fatal("ctrl+c should leave without launching")
	}
	// esc is the key that clears a filter, and it still does.
	p = demoPicker()
	press(p, "/", "s", "e", "c")
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc clears the filter rather than leaving")
	}
	if p.filter != "" || p.typing {
		t.Fatalf("esc left filter=%q typing=%t", p.filter, p.typing)
	}
}

// Any key other than q or esc takes the arm back, so an arm set before a
// search cannot turn the next q into a second press the reader never saw.
func TestPickArmedQuitIsDroppedByAnotherKey(t *testing.T) {
	p := demoPicker()
	press(p, "q", "j")
	if p.quitArmed {
		t.Fatal("moving the cursor did not take the arm back")
	}
	press(p, "q", "/", "s", "e", "c", "enter")
	if p.quitArmed {
		t.Fatal("typing a filter left the arm set behind an invisible status line")
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

// A panel is padded to its width, not wrapped, so a sentence longer than the
// pane is cut with nothing to mark the cut. The empty agents pane therefore
// says only what is missing, and the sentence naming the fix lives on the
// lines that have room for it: the status line and the narrow fallback at a
// width that holds it, and the help at every width, since the help wraps.
func TestPickEmptyAgentsPaneIsNotCutMidWord(t *testing.T) {
	p := demoPicker()
	p.cfg.Agents = nil
	for _, w := range []int{50, 62, 80, 100} {
		p.w, p.h = w, 30
		panel := stripANSI(p.agentPanel(rightColumnWidth(w), p.paneHeight(paneAgents)))
		if !strings.Contains(panel, "none installed") {
			t.Fatalf("at %d columns the empty agents pane lost its message:\n%s", w, panel)
		}
		if strings.Contains(panel, "doc") {
			t.Fatalf("at %d columns the pane carries a clause it cannot fit:\n%s", w, panel)
		}
		// The status line is one row and the sentence naming the fix is
		// longer than a narrow terminal: what it must not do is stop
		// mid-word unmarked, which reads as a command that does not exist.
		status := stripANSI(p.renderStatus())
		if !strings.HasSuffix(status, "…") && !strings.Contains(status, "gauntlet doctor") {
			t.Fatalf("at %d columns the status line was cut without a marker:\n%s", w, status)
		}
		if !strings.Contains(stripANSI(strings.Join(p.helpLines(), "\n")), "gauntlet doctor") {
			t.Fatalf("at %d columns the help lost the fix", w)
		}
	}
	p.w, p.h = 100, 30
	if !strings.Contains(stripANSI(p.renderStatus()), "gauntlet doctor") {
		t.Fatalf("a wide status line lost the fix:\n%s", stripANSI(p.renderStatus()))
	}
	if !strings.Contains(stripANSI(p.renderNarrow()), "gauntlet doctor") {
		t.Fatalf("a wide narrow-fallback lost the fix:\n%s", stripANSI(p.renderNarrow()))
	}
}

// rightColumnWidth is the width the launcher's right column gets at a given
// terminal width, so a test measures the pane at the size it is really drawn.
func rightColumnWidth(w int) int {
	capW := w - 28
	if w-36 >= 34 {
		capW = w - 36
	}
	return w - clampi(w*3/5, min(34, capW), capW) - 1
}

// A filter holds every set open, so the arrows the hint otherwise names have
// nothing left to close: left on a header only moves the cursor. The hint
// must not promise a fold the tree cannot make.
func TestPickGroupHintUnderAFilter(t *testing.T) {
	p := demoPicker()
	press(p, "j", "j") // onto the quick group header
	if got := stripANSI(p.hint()); !strings.Contains(got, "open and close it") {
		t.Fatalf("an unfiltered set header hint %q, want the fold keys", got)
	}
	press(p, "/", "u", "x", "enter")
	p.focus, p.cursor[paneReviews] = paneReviews, 1
	if r := p.rowAt(p.cursor[paneReviews]); r.kind != rowGroup {
		t.Fatalf("row %d is not a set header", p.cursor[paneReviews])
	}
	got := stripANSI(p.hint())
	if strings.Contains(got, "open and close it") {
		t.Fatalf("a filtered set header still advertises the fold keys: %q", got)
	}
	if !strings.Contains(got, "space takes the whole set") {
		t.Fatalf("filtered set header hint %q, want the action that works", got)
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
	// The arrow segment names what the arrows do on the row the cursor is
	// on. The demo picker opens on the suggest row, which has no fold of its
	// own, so it reads "pane"; the set header and the review row below are
	// checked against their own actions.
	for _, want := range []string{":pane", ":toggle", ":filter", ":help"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("a wide terminal should document more than the essentials (%q missing):\n%s", want, footer)
		}
	}
	if strings.Contains(footer, ":open/close") {
		t.Fatalf("the suggest row advertises a fold it cannot make:\n%s", footer)
	}
}

// The tree is not one kind of row, and the arrow key has to say so: a set
// header folds both ways, a review inside one folds only left, and the
// suggest row only steps between panes. A key legend that names one action
// for all three advertises a control that does nothing on two of them, which
// a keyboard user cannot tell from a broken key.
func TestPickArrowKeysNameTheActionOfTheRowUnderThem(t *testing.T) {
	arrow := func(t *testing.T, p *picker) string {
		t.Helper()
		p.w, p.h, p.ready = 104, 30, true
		footer := lastLine(stripANSI(p.View()))
		_, act, ok := strings.Cut(footer, "←/→:")
		if !ok {
			t.Fatalf("the key line names no arrow keys:\n%s", footer)
		}
		if end := strings.Index(act, " "); end >= 0 {
			act = act[:end]
		}
		return act
	}
	for _, tc := range []struct {
		name string
		keys []string
		want string
	}{
		{"suggest row", nil, "pane"},
		{"set header", []string{"j", "j"}, "open/close"},
		{"review in a set", []string{"j", "l", "j"}, "fold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoPicker()
			press(p, tc.keys...)
			got := arrow(t, p)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("arrow keys read %q, want the action %q", got, tc.want)
			}
		})
	}
}

// The legend calls the arrows "pane" while the cursor is on the suggest row,
// the one row of the tree with no fold, so they have to move the pane there:
// a key the legend names and the key that does nothing look the same on
// screen.
func TestPickArrowsLeaveTheSuggestRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		want pane
	}{
		{"right", "l", paneAgents},
		{"left", "h", paneOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoPicker()
			if got := p.rowAt(p.cursor[paneReviews]).kind; got != rowSuggest {
				t.Fatalf("the demo picker opens on row kind %v, want the suggest row", got)
			}
			press(p, tc.key)
			if p.focus != tc.want {
				t.Fatalf("%s on the suggest row left the focus on pane %d, want %d",
					tc.key, p.focus, tc.want)
			}
		})
	}
}

// A filter holds every set open, so no row of the tree has a fold left to
// make. The arrows were still named "fold", and on a review row they did
// nothing at all, which a keyboard user cannot tell from a broken key: they
// step panes here, the way they do on the suggest row.
func TestPickArrowsStepPanesWhileFiltering(t *testing.T) {
	arrow := func(t *testing.T, p *picker) string {
		t.Helper()
		p.w, p.h, p.ready = 104, 30, true
		footer := lastLine(stripANSI(p.renderKeys()))
		_, act, ok := strings.Cut(footer, "←/→:")
		if !ok {
			t.Fatalf("the key line names no arrow keys:\n%s", footer)
		}
		if end := strings.Index(act, " "); end >= 0 {
			act = act[:end]
		}
		return act
	}
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"set header", []string{"j"}},
		{"review in a set", []string{"j", "j"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoPicker()
			p.filter = "code" // kept, not being typed: the state enter leaves
			press(p, tc.keys...)
			if got := arrow(t, p); got != "pane" {
				t.Fatalf("a filtered %s advertises %q for the arrows, want the action they have", tc.name, got)
			}
			at := p.cursor[paneReviews]
			press(p, "l")
			if p.focus != paneAgents {
				t.Fatalf("right on a filtered %s left the focus on pane %d, want %d",
					tc.name, p.focus, paneAgents)
			}
			if p.cursor[paneReviews] != at {
				t.Fatalf("a filtered %s moved the cursor, which reads as a fold that did not happen", tc.name)
			}
		})
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
	for _, want := range []string{"close this help", "Picking no reviews runs all of them", "tab", "stacked PRs", "pgup / pgdn"} {
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
	// A later q, with the view back, still leaves, on its second press.
	press(p, "q", "q")
	if p.launch {
		t.Fatal("q should leave without launching")
	}
}

// The launcher's help names every key that edits a filter, so the key a reader
// reaches for by habit is a documented one rather than an unmentioned key that
// reads as broken (WCAG 3.3.2). Each name is checked against the binding it
// documents, so a key that is renamed or dropped fails here.
func TestPickHelpNamesTheFilterEditingKeys(t *testing.T) {
	p := demoPicker()
	press(p, "?")
	got := stripANSI(p.View())
	for _, want := range []string{"backspace", "delete", "ctrl+h", "ctrl+u", "ctrl+w"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help does not name %q:\n%s", want, got)
		}
	}
	// And what the help names is what the filter does with it.
	press(p, "q")
	press(p, "/")
	for _, r := range "abc" {
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	p.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if p.filter != "ab" {
		t.Fatalf("backspace left the filter at %q, want %q", p.filter, "ab")
	}
}

// Stack mode owns commits and the job count: conflicting choices are kept for
// later but omitted while stacking, and +/- must not sneak a -j back in.
func setupConflictingOptionsPicker() *picker {
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
	return p
}

func assertConflictingOptionsPreserved(t *testing.T, p *picker) {
	t.Helper()
	if !p.stacked() {
		t.Fatal("stacked PRs was not turned on")
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

func TestPickStackedPRsPreserveConflictingOptions(t *testing.T) {
	p := setupConflictingOptionsPicker()
	p.toggle()
	assertConflictingOptionsPreserved(t, p)
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
		}, []string{"suggest agent: gauntlet (default)", "suggest is off"}},
		{"inert option", func(p *picker) {
			p.focus = paneOptions
			p.optByFlag("--stacked-prs").on = true
		}, []string{"concurrency: 1 (disabled)", "stacked PRs own this"}},
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

func TestNarrowLauncherShowsActiveFilterAndClear(t *testing.T) {
	p := demoPicker()
	p.w, p.h, p.ready = 40, 10, true
	p.filter = "ux"
	view := stripANSI(p.View())
	if !strings.Contains(view, "filter: /ux") {
		t.Fatalf("narrow view lost active filter label:\n%s", view)
	}
	if !strings.Contains(view, "esc clear") {
		t.Fatalf("narrow view lost esc clear key hint:\n%s", view)
	}
	p.typing = true
	viewTyping := stripANSI(p.View())
	if !strings.Contains(viewTyping, "filter: ux") {
		t.Fatalf("narrow view lost typing filter label:\n%s", viewTyping)
	}
	if !strings.Contains(viewTyping, "esc clear") {
		t.Fatalf("narrow view lost esc clear while typing:\n%s", viewTyping)
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

func TestPickExpandStepsIntoFirstMemberWhenAlreadyOpen(t *testing.T) {
	p := demoPicker()
	p.cursor[paneReviews] = 1 // quick group header (collapsed)
	if p.open[0] {
		t.Fatal("group should start collapsed")
	}
	press(p, "l") // expands group
	if !p.open[0] {
		t.Fatal("l did not expand group")
	}
	if p.cursor[paneReviews] != 1 {
		t.Fatalf("first l should keep cursor on header, got %d", p.cursor[paneReviews])
	}
	press(p, "l") // group is already expanded; steps into first child
	if p.cursor[paneReviews] != 2 {
		t.Fatalf("second l on open group should step into first review, got %d", p.cursor[paneReviews])
	}
	if r := p.rowAt(p.cursor[paneReviews]); r.kind != rowReview || r.review.Name != "sec-review" {
		t.Fatalf("cursor expected on sec-review, got %+v", r)
	}
	press(p, "h") // collapses group and returns to header
	if p.open[0] {
		t.Fatal("h should collapse group")
	}
	if p.cursor[paneReviews] != 1 {
		t.Fatalf("h should return cursor to header, got %d", p.cursor[paneReviews])
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

func TestPickPageUpDownJumpThePane(t *testing.T) {
	reviews := make([]PickReview, 40)
	for i := range reviews {
		reviews[i] = PickReview{Name: fmt.Sprintf("rev-%02d", i), Desc: "test review"}
	}
	p := newPicker(PickConfig{
		Groups: []PickGroup{
			{Name: "all", Reviews: reviews},
		},
		Agents: []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"},
	})
	p.w, p.h, p.ready = 100, 20, true
	p.open[0] = true

	step := max(p.paneHeight(paneReviews)-1, 1)
	if step <= 1 {
		t.Fatalf("step too small: %d", step)
	}

	// In paneReviews
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if p.cursor[paneReviews] != step {
		t.Fatalf("pgdown moved cursor to %d, want %d", p.cursor[paneReviews], step)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if p.cursor[paneReviews] != step*2 {
		t.Fatalf("second pgdown moved cursor to %d, want %d", p.cursor[paneReviews], step*2)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if p.cursor[paneReviews] != step {
		t.Fatalf("pgup moved cursor to %d, want %d", p.cursor[paneReviews], step)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("second pgup moved cursor to %d, want 0", p.cursor[paneReviews])
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("pgup past top should clamp to 0, got %d", p.cursor[paneReviews])
	}

	// In filter typing mode
	press(p, "/", "rev")
	if !p.typing {
		t.Fatal("expected picker to be in typing mode")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if p.cursor[paneReviews] == 0 {
		t.Fatal("pgdown inside filterKey did not advance cursor")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if p.cursor[paneReviews] != len(p.rows())-1 {
		t.Fatalf("end inside filterKey did not jump to end: got %d, want %d", p.cursor[paneReviews], len(p.rows())-1)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyHome})
	if p.cursor[paneReviews] != 0 {
		t.Fatalf("home inside filterKey did not jump to start: got %d", p.cursor[paneReviews])
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})

	// In paneAgents
	p.focus = paneAgents
	agentStep := max(p.paneHeight(paneAgents)-1, 1)
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if p.cursor[paneAgents] != agentStep {
		t.Fatalf("pgdown in paneAgents moved cursor to %d, want %d", p.cursor[paneAgents], agentStep)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if p.cursor[paneAgents] != 0 {
		t.Fatalf("pgup in paneAgents moved cursor to %d, want 0", p.cursor[paneAgents])
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
	// A second esc now that the filter is gone asks to leave, and a third
	// confirms it. One press threw the composed run away with no way back,
	// and esc is the key a keyboard user reaches for to back out of
	// whatever they are in (WCAG 3.3.4).
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("esc on an unfiltered picker must ask before leaving")
	}
	if !p.quitArmed {
		t.Fatal("esc on an unfiltered picker left without asking")
	}
	_, cmd = p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("a second esc, with nothing left to clear, must leave")
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
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune("l")},
	} {
		p := setupConflictingOptionsPicker()
		p.Update(key)
		assertConflictingOptionsPreserved(t, p)
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

func TestPickerPreservesPromptDir(t *testing.T) {
	p := newPicker(PickConfig{
		Dir:       "/home/dev/project",
		PromptDir: "/tmp/custom-prompts",
		Groups:    []PickGroup{{Name: "quick", Reviews: []PickReview{{Name: "sec-review", Desc: "d"}}}},
		Agents:    []string{"claude"},
		CPUs:      8,
	})
	p.w, p.h, p.ready = 100, 30, true
	got := strings.Join(p.argv(), " ")
	if !strings.Contains(got, "--prompt-dir /tmp/custom-prompts") {
		t.Fatalf("argv missing --prompt-dir: %s", got)
	}
}

func TestTrimLastWordWithUnicodeSpaces(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"hello world", "hello "},
		{"hello\u3000world", "hello\u3000"},
		{"hello\u00a0world", "hello\u00a0"},
		{"hello   ", ""},
		{"single", ""},
	} {
		if got := trimLastWord(tc.input); got != tc.want {
			t.Fatalf("trimLastWord(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPickFooterShowsChangeInOptionsPane(t *testing.T) {
	p := demoPicker()
	p.w, p.h, p.ready = 104, 30, true
	p.focus = paneOptions
	footer := lastLine(p.View())
	if !strings.Contains(footer, ":change") {
		t.Fatalf("options pane footer should show :change for arrow keys, got:\n%s", footer)
	}
	if strings.Contains(footer, ":open/close") {
		t.Fatalf("options pane footer should not show :open/close, got:\n%s", footer)
	}
}

func TestPickEmptyAgentsPaneHintExplainsNoneInstalled(t *testing.T) {
	p := newPicker(PickConfig{
		Dir:    "/home/dev/project",
		Groups: []PickGroup{{Name: "quick", Reviews: []PickReview{{Name: "sec-review", Desc: "d"}}}},
		Agents: nil,
	})
	p.focus = paneAgents
	hint := p.hint()
	if !strings.Contains(hint, "no agent CLI is installed") {
		t.Fatalf("empty agents pane hint %q, want explanation that none are installed", hint)
	}
}

func TestPickDisabledOptionsCannotBeModified(t *testing.T) {
	p := demoPicker()
	p.focus = paneOptions
	// Suggest agent when suggest is off
	for i, o := range p.opts {
		if o.flag == "--suggest-agent" {
			p.cursor[paneOptions] = i
			break
		}
	}
	origSuggestIdx := p.opts[p.cursor[paneOptions]].idx
	press(p, " ", "l", "h")
	if p.opts[p.cursor[paneOptions]].idx != origSuggestIdx {
		t.Fatal("suggest agent was modified while suggest was off")
	}

	// Merge into when committing is off
	for i, o := range p.opts {
		if o.flag == "--merge-into" {
			p.cursor[paneOptions] = i
			break
		}
	}
	origMergeIdx := p.opts[p.cursor[paneOptions]].idx
	press(p, " ", "l", "h")
	if p.opts[p.cursor[paneOptions]].idx != origMergeIdx {
		t.Fatal("merge into was modified while commits were off")
	}
}

func TestPickFooterShowsPaneInAgentsPane(t *testing.T) {
	p := demoPicker()
	p.w, p.h, p.ready = 104, 30, true
	p.focus = paneAgents
	footer := lastLine(p.View())
	if !strings.Contains(footer, ":pane") {
		t.Fatalf("agents pane footer should show :pane for arrow keys, got:\n%s", footer)
	}
	if strings.Contains(footer, ":open/close") {
		t.Fatalf("agents pane footer should not show :open/close, got:\n%s", footer)
	}
}

func TestPickAgentPanelTitleWhenAllPicked(t *testing.T) {
	p := demoPicker()
	p.w, p.h, p.ready = 104, 30, true
	p.focus = paneAgents
	press(p, "a") // selects all agents
	view := stripANSI(p.View())
	if !strings.Contains(view, "AGENTS  all picked") {
		t.Fatalf("agents panel title should show 'AGENTS  all picked' when all are selected, got:\n%s", view)
	}
}

func TestPickSuggestAgentHint(t *testing.T) {
	p := demoPicker()
	p.suggest = true
	p.focus = paneOptions
	p.cursor[paneOptions] = optSuggestAgent
	if got := p.hint(); !strings.Contains(got, "read the files for signals without a model") {
		t.Fatalf("default suggest hint must describe built-in suggestions, got %q", got)
	}

	// FastSuggest agent
	p.opts[optSuggestAgent].idx = 1
	if got := p.hint(); !strings.Contains(got, "reads the files for signals") {
		t.Fatalf("fast suggest hint should explain file signals, got %q", got)
	}

	// Model agent
	p.opts[optSuggestAgent].idx = 2 // claude
	if got := p.hint(); !strings.Contains(got, "use claude to read the repo") {
		t.Fatalf("agent suggest hint should name the agent, got %q", got)
	}
}

func TestPickFilterTypingShowsPromptEvenWithNoMatches(t *testing.T) {
	p := demoPicker()
	press(p, "/", "z", "z", "z")
	status := stripANSI(p.renderStatus())
	if !strings.Contains(status, "filter: zzz") {
		t.Fatalf("typing mode should keep the filter prompt visible, got %q", status)
	}
	if !strings.Contains(status, "no reviews match") {
		t.Fatalf("typing mode should indicate no matches, got %q", status)
	}
}

// The key legend names what the keys do in the pane that has the keyboard.
// The run pane shows switches rather than a selection, so a does nothing
// there: advertising it is naming a dead key. The reviews and agent panes do
// have something to take, and keep it.
func TestKeyLegendDropsTheDeadAllNoneKeyInTheRunPane(t *testing.T) {
	p := demoPicker()
	p.focus = paneOptions
	got := stripANSI(p.renderKeys())
	if strings.Contains(got, "all/none") {
		t.Fatalf("the run pane advertises a dead key: %s", got)
	}
	for _, want := range []string{"⏎:run", "q:cancel", "space:toggle", "+/-:concurrency"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the run pane legend lost %q: %s", want, got)
		}
	}
	for _, focus := range []pane{paneReviews, paneAgents} {
		p.focus = focus
		if got := stripANSI(p.renderKeys()); !strings.Contains(got, "a:all/none") {
			t.Fatalf("pane %d lost a:all/none: %s", focus, got)
		}
	}
	// A live filter keeps its place in the legend, so esc clears it from any
	// pane that offers the key.
	p.focus = paneOptions
	p.filter = "sec"
	got = stripANSI(p.renderKeys())
	if !strings.Contains(got, "esc:clear") {
		t.Fatalf("a filtered legend lost esc:clear: %s", got)
	}
	if strings.Contains(got, "all/none") {
		t.Fatalf("a filtered run pane advertises a dead key: %s", got)
	}
}

// The legend drops every key the screen cannot act on, and +/- is no
// exception: stack mode owns the job count, and a one-cpu machine has nothing
// to raise it to.
func TestPickLegendDropsDeadConcurrencyKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *picker)
	}{
		{"stack mode", func(p *picker) { p.optByFlag("--stacked-prs").on = true }},
		{"one cpu", func(p *picker) { p.cfg.CPUs = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := demoPicker()
			tc.setup(p)
			if got := stripANSI(p.renderKeys()); strings.Contains(got, "concurrency") {
				t.Fatalf("the legend offers a key that does nothing: %s", got)
			}
		})
	}
	p := demoPicker()
	if got := stripANSI(p.renderKeys()); !strings.Contains(got, "+/-:concurrency") {
		t.Fatalf("a live machine lost the concurrency keys: %s", got)
	}
}

// + and space reach the same row, so they stop in the same place: past the
// machine's cpus the meter is full and the lane has nothing to run on.
func TestPickConcurrencyKeysStopAtTheCPUs(t *testing.T) {
	p := demoPicker()
	p.cfg.CPUs = 2
	press(p, "+", "+", "+", "+")
	if p.concurrency().n != 2 {
		t.Fatalf("concurrency is %d, want the 2-cpu ceiling", p.concurrency().n)
	}
	press(p, "-", "-", "-")
	if p.concurrency().n != 1 {
		t.Fatalf("concurrency is %d, want the floor of 1", p.concurrency().n)
	}
}

// The panes are sized by what they hold, so a terminal taller than the
// launcher leaves a gap. It belongs above the command, the status line, and
// the keys, never inside the last row: a reader who has to look for the keys
// on a big terminal has to look for them every time.
func TestLauncherAnchorsTheKeyLineToTheBottomRow(t *testing.T) {
	for _, h := range []int{20, 30, 45} {
		p := demoPicker()
		p.w, p.h, p.ready = 100, h, true
		rows := strings.Split(p.View(), "\n")
		if len(rows) != h {
			t.Errorf("h=%d: the frame is %d rows, want %d", h, len(rows), h)
		}
		if keys := stripANSI(lastLine(p.View())); !strings.Contains(keys, "run") {
			t.Errorf("h=%d: the last row is not the key line: %q", h, keys)
		}
		if !strings.Contains(stripANSI(strings.Join(rows, "\n")), "$ gauntlet -C") {
			t.Errorf("h=%d: the composed command left the screen", h)
		}
	}
}

// The narrow view has no panes and no status line, so a terminal too short
// for the whole of it keeps the command it composes and the keys that act on
// it. A screen that cannot say how to leave, or what it would run, is the
// dead end the fallback exists to avoid.
func TestNarrowLauncherKeepsTheCommandAndTheKeys(t *testing.T) {
	for h := 2; h <= 5; h++ {
		p := demoPicker()
		p.w, p.h, p.ready = 40, h, true
		view := stripANSI(p.View())
		if !strings.Contains(view, "gauntlet -C") {
			t.Errorf("h=%d: the composed command left the screen:\n%s", h, view)
		}
		if keys := stripANSI(lastLine(p.View())); !strings.Contains(keys, "run") || !strings.Contains(keys, "q") {
			t.Errorf("h=%d: the last row is not the key line: %q", h, keys)
		}
	}
}

// A pane too narrow for its own rows says where the text stops. A hard cut
// left "auto-d" and "suggest ag" on the screen, which read as the words they
// happened to end on rather than as text the terminal ran out of room for.
func TestNarrowPanesMarkCutText(t *testing.T) {
	// A title wider than its pane ends in the marker rather than in whatever
	// word the cut happened to land on: "AGENTS  none picked: auto-d" read as
	// a complete label.
	got := stripANSI(firstLine(panel("AGENTS  none picked: auto-detect", "", 20, 1)))
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a cut panel title does not say it is cut: %q", got)
	}
	if whole := stripANSI(firstLine(panel("AGENTS", "", 20, 1))); whole != "AGENTS" {
		t.Errorf("a title that fits was cut: %q", whole)
	}

	// The same for a run-pane row: a pane wide enough for the longest label
	// and a short value holds it whole, and a narrower one keeps the label and
	// marks the value. The longest label is the suggest agent's, which is why
	// it carries a reason: a row drawn dim for a state it never names is a
	// state only the faintness says (SC 1.4.1), so the words are part of the
	// label and the pane has to budget for them.
	p := demoPicker()
	for _, c := range []struct {
		w    int
		want []string
	}{
		{56, []string{"suggest agent (suggest off)", "gauntlet (default)"}},
		{36, []string{"…"}},
		{28, []string{"…"}},
	} {
		rows := strings.Split(stripANSI(p.runPanel(c.w, p.paneHeight(paneOptions))), "\n")
		var row string
		for _, r := range rows {
			if strings.Contains(r, "suggest") {
				row = r
			}
		}
		for _, want := range c.want {
			if !strings.Contains(row, want) {
				t.Errorf("a %d column run pane did not carry %q:\n%s", c.w, want, row)
			}
		}
	}
}

// The two panels in the right column, their borders, and the four rows above
// and below them are the whole frame. Reserving one panel's worth for the
// pair left rows empty under a full run pane, and cost the pane a reader
// composes the run in a row it had space for.
func TestRightColumnFillsTheFrame(t *testing.T) {
	for _, h := range []int{12, 14, 18, 24, 30} {
		p := demoPicker()
		p.w, p.h, p.ready = 100, h, true
		agents, run := p.paneHeight(paneAgents), p.paneHeight(paneOptions)
		if used := agents + run + 2*panelChrome; used > h-viewChrome {
			t.Errorf("h=%d: the right column asks for %d rows of %d", h, used, h-viewChrome)
		}
		if run < 1 {
			t.Errorf("h=%d: the run pane has %d rows", h, run)
		}
		if h >= 30 && run < len(p.opts) {
			t.Errorf("h=%d: a full frame gives the run pane %d rows for %d options",
				h, run, len(p.opts))
		}
	}
}

// A pane title that cannot hold every reading drops whole ones and says what
// went, the way the dashboard's panel titles do. Cutting it instead could end
// at "none picked: auto-d" and take the count of hidden rows with it: an agent
// missing from the list with nothing to say so reads as one that is not
// installed.
func TestPaneTitlesDropWholeReadings(t *testing.T) {
	p := demoPicker()
	// One agent row and one option row: both panes hold fewer rows than they
	// have, so both titles carry a hidden count.
	for _, w := range []int{24, 30, 50} {
		agents := firstLine(p.agentPanel(w, 1))
		run := firstLine(p.runPanel(w, 1))
		if got := stripANSI(agents); !strings.Contains(got, "+1 more") {
			t.Errorf("at %d columns the agents title does not say it is hiding one: %q", w, got)
		}
		if got := stripANSI(run); !strings.Contains(got, "+8 more") {
			t.Errorf("at %d columns the run title does not say it is hiding options: %q", w, got)
		}
		// Whatever the width, a title is never a word the cut happened to land
		// on: it either fits whole or ends in the marker.
		for _, title := range []string{stripANSI(agents), stripANSI(run)} {
			if cut := strings.Index(title, "…"); cut >= 0 && cut < len(title)-len("…") {
				t.Errorf("at %d columns a title was cut mid-word: %q", w, title)
			}
			if got := lipgloss.Width(title); got > w+panelBorderColumns {
				t.Errorf("at %d columns the title is %d wide: %q", w, got, title)
			}
		}
	}
	// Wide enough for everything, the readings are all there.
	if got := stripANSI(firstLine(p.agentPanel(50, 5))); !strings.Contains(got, "none picked: auto-detect") {
		t.Errorf("a pane holding every agent does not say what is picked: %q", got)
	}
}

// A pane scrolled away from its top says so on both sides. One count of what
// is below the fold cannot describe a list whose cursor is part way down: the
// rows already scrolled off the top are hidden too, and "+6 more" under a
// list whose last row is at the cursor says there is more below when there is
// none. The dashboard's feed and its help overlay already answer both ways,
// so the panes are held to the same reading.
func TestScrolledPaneNamesRowsAboveAndBelow(t *testing.T) {
	for _, c := range []struct {
		name string
		want string
		keys []string
	}{{
		name: "at the top it counts only what is below",
		want: "+6 more",
		keys: []string{"home"},
	}, {
		name: "at the bottom it counts what it scrolled off",
		want: "6 above",
		keys: []string{"G"},
	}, {
		name: "in the middle it gives both ends",
		want: "2 above, 4 more",
		// Nine options in a three-row pane: the pane scrolls only once the
		// cursor passes the third row, so four presses leave the slice
		// starting at the third option with two rows above it.
		keys: []string{"j", "j", "j", "j"},
	}} {
		p := demoPicker()
		p.w, p.h, p.ready = 100, 14, true
		p.focus = paneOptions
		press(p, c.keys...)
		if got := stripANSI(firstLine(p.runPanel(60, 3))); !strings.Contains(got, c.want) {
			t.Errorf("%s: title %q does not carry %q", c.name, got, c.want)
		}
	}
	// A pane holding every row carries no count at all, at either end: this
	// reading is only ever about rows a reader cannot see.
	p := demoPicker()
	p.focus = paneOptions
	if got := stripANSI(firstLine(p.runPanel(60, len(p.opts)))); strings.Contains(got, "more") {
		t.Errorf("a pane holding every option grew a hidden count: %q", got)
	}
	// The other two panes are held to the same reading, and on a machine with
	// several agents installed the agents pane is the first one to scroll.
	wide := demoPicker()
	wide.cfg.Agents = nil
	for i := range 12 {
		wide.cfg.Agents = append(wide.cfg.Agents, fmt.Sprintf("agent-%02d", i))
	}
	wide.agents = make([]bool, len(wide.cfg.Agents))
	wide.w, wide.h, wide.ready = 100, 14, true
	wide.focus = paneAgents
	press(wide, "G")
	if got := stripANSI(firstLine(wide.agentPanel(60, 3))); !strings.Contains(got, "9 above") {
		t.Errorf("the agents title does not say what was scrolled off: %q", got)
	}
}

// The fallback's key row drops whole keys, and marks that it did: a row ending
// at "q cancel" reads as the whole set, and "? help" is the one key that says
// how to learn the rest of the screen.
func TestNarrowLauncherMarksDroppedKeys(t *testing.T) {
	for _, w := range []int{20, 26, 30, 40} {
		p := demoPicker()
		p.w, p.h, p.ready = w, 8, true
		keys := stripANSI(fitSegments([]string{"⏎ run", "/ filter", "q cancel", "? help"}, "  ", w))
		if !strings.HasPrefix(keys, "⏎ run") {
			t.Errorf("at %d columns the first key was dropped: %q", w, keys)
		}
		for seg := range strings.SplitSeq(keys, "  ") {
			switch seg {
			case "⏎ run", "/ filter", "q cancel", "? help", "…":
			default:
				t.Errorf("at %d columns a key name was cut: %q", w, seg)
			}
		}
		kept := strings.TrimSuffix(keys, "  …")
		if !strings.HasSuffix(keys, "? help") && !strings.HasSuffix(keys, "…") &&
			lipgloss.Width(kept)+3 <= w {
			t.Errorf("at %d columns dropped keys are not marked: %q", w, keys)
		}
	}
}

// A run-pane row that cannot apply is drawn dim, and dim is a difference in
// luminance rather than a reading: a state carried by faintness alone is one a
// reader at low vision, on a monochrome terminal, or through a screen reader
// cannot take, and the status line only names it once the cursor is on the row
// (SC 1.4.1). So the row names it itself, in the words it is drawn with.
func TestRunRowsNameWhyTheyCannotApply(t *testing.T) {
	row := func(p *picker, label string) string {
		t.Helper()
		for r := range strings.SplitSeq(stripANSI(p.runPanel(60, p.paneHeight(paneOptions))), "\n") {
			if strings.Contains(r, label) {
				return r
			}
		}
		// A missing row is not a row without a note, which is the one case the
		// negative assertion below would otherwise pass on.
		t.Fatalf("the run pane draws no %q row", label)
		return ""
	}
	p := demoPicker()
	for _, want := range []struct{ label, note string }{
		{"suggest agent", "(suggest off)"},
		{"merge into", "(commit off)"},
	} {
		if got := row(p, want.label); !strings.Contains(got, want.note) {
			t.Errorf("a %s row does not say why it is dim: %q, want %q", want.label, got, want.note)
		}
	}
	// A row that applies is left alone: a note on every row is a note on none.
	if got := row(p, "yolo"); strings.Contains(got, "(") {
		t.Errorf("a row that applies carries a reason it does not have: %q", got)
	}
	// Stacked mode owns the job count and the git rows, and says so on each.
	stacked := 4
	for i, o := range demoPicker().opts {
		if o.flag == "--stacked-prs" {
			stacked = i
		}
	}
	p.focus, p.cursor[paneOptions] = paneOptions, stacked
	p.toggle()
	for _, label := range []string{"concurrency", "commit", "push", "merge into"} {
		if got := row(p, label); !strings.Contains(got, "(stacked)") {
			t.Errorf("a %s row in stacked mode does not say stacked owns it: %q", label, got)
		}
	}
}
