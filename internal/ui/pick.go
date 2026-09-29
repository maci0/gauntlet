// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// The launcher: the screen `gauntlet pick` opens so a run can be composed
// without knowing the flags first. It decides nothing on its own. What it
// produces is an argv, which the caller runs through the same parser every
// other invocation goes through, and which it shows on screen the whole time
// so the flags are learned rather than hidden.
//
// It is drawn as the dashboard is drawn, with the same instruments: the
// wordmark and a spread header, titled instrument panels, one hue per agent
// everywhere it appears, and meters that show their unlit remainder. The two
// screens are the same cockpit at two moments, so they read the same way.

package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/fuzzy"
)

// PickConfig is what the launcher needs to know about this machine: what can
// be reviewed, what can review it, and how much of it can run at once.
type PickConfig struct {
	Dir       string      // the directory the composed run will review
	PromptDir string      // custom prompt directory, empty for bundled
	Groups    []PickGroup // review sets, in display order
	Agents    []string    // installed agent labels, empty when none were found
	Branch    string      // the branch the reviews would run on, "" off a branch
	Merge     []string    // other local branches, as merge targets
	Dirty     bool        // tracked files have uncommitted changes, which worktrees refuse
	CPUs      int         // the concurrency meter is drawn against this
	Version   string
	// Reserved are the words --reviews reads as something other than a
	// review name: the set names and the suggest keyword. The launcher
	// abbreviates a selection by dropping the "-review" suffix, and the
	// caller resolves sets before review names, so a stem that lands on one
	// of these has to be written out in full. Passed in rather than looked
	// up here: nothing under internal/ui imports the prompt catalog.
	Reserved []string
	// FastSuggest is the --suggest-agent value that picks reviews from file
	// signals instead of asking a model. Empty omits that choice. Passed in
	// rather than looked up here: the picker imports neither internal/evidence
	// nor internal/runner.
	FastSuggest string
}

// PickGroup is one collapsible category: a review set and the members of it
// that exist in this prompt directory.
type PickGroup struct {
	Name    string
	Reviews []PickReview
}

// PickReview is one review as the launcher shows it: what it is called, what
// it does, and whether it came from the reviewed tree rather than the
// bundled set.
type PickReview struct {
	Name    string
	Desc    string
	Project bool
}

// Pick runs the launcher. It returns the argv of the composed run, or ok=false
// when the user left without launching anything.
func Pick(cfg PickConfig) (argv []string, ok bool, err error) {
	p := newPicker(cfg)
	out, err := tea.NewProgram(p, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, false, err
	}
	done, _ := out.(*picker)
	if done == nil || !done.launch {
		return nil, false, nil
	}
	return done.argv(), true, nil
}

// pane is which column has the keyboard.
type pane int

const (
	paneReviews pane = iota
	paneAgents
	paneOptions
	paneCount
)

// optKind is how one row of the run pane behaves.
type optKind int

const (
	optToggle optKind = iota // on or off
	optCount                 // a number with a floor of one
	optCycle                 // one of a list of values
)

type option struct {
	kind   optKind
	label  string
	help   string
	flag   string   // what it contributes to the argv
	values []string // optCycle: the choices, values[0] being "unset"
	on     bool
	n      int
	idx    int
}

// rowKind distinguishes the one-off suggest row from the review tree.
type rowKind int

const (
	rowSuggest rowKind = iota
	rowGroup
	rowReview
)

// row is one line of the reviews pane.
type row struct {
	kind   rowKind
	group  int
	review PickReview
}

type picker struct {
	cfg    PickConfig
	hues   *hueMap
	w, h   int
	ready  bool
	launch bool

	focus pane

	// quitArmed is q or esc pressed once: the composed run is discarded on the
	// second press of the same key, and any other key takes it back. The
	// dashboard arms q for the same reason, and here it costs a screenful of
	// picking rather than a run. quitKey is the key that armed it, so the
	// status line and the legend can name the press that is being asked for
	// and the one that takes it back.
	quitArmed bool
	quitKey   string

	suggest  bool            // let an agent pick the reviews instead
	filter   string          // narrows the review tree by name or description
	typing   bool            // keys are going into the filter, not the panes
	open     []bool          // per group
	selected map[string]bool // review name -> chosen
	agents   []bool          // per installed agent
	opts     []option

	cursor     [paneCount]int
	scroll     [paneCount]int
	help       bool
	helpScroll int

	// The config is fixed for the life of a session, so two derived views of
	// it are computed once instead of per render: every distinct review, and
	// the folded form each name and description is matched against.
	knownReviews []string
	folds        map[string][2]string
}

// The indexes of the run-pane rows that the other panes reach into.
const (
	optConcurrency  = 0
	optSuggestAgent = 1
)

func newPicker(cfg PickConfig) *picker {
	if cfg.CPUs < 1 {
		cfg.CPUs = 1
	}
	seen := map[string]bool{}
	knownReviews := []string{}
	for _, g := range cfg.Groups {
		for _, rev := range g.Reviews {
			if !seen[rev.Name] {
				seen[rev.Name] = true
				knownReviews = append(knownReviews, rev.Name)
			}
		}
	}
	p := &picker{
		cfg:          cfg,
		hues:         newHueMap(),
		open:         make([]bool, len(cfg.Groups)),
		selected:     map[string]bool{},
		agents:       make([]bool, len(cfg.Agents)),
		knownReviews: knownReviews,
		folds:        map[string][2]string{},
		opts: []option{
			{kind: optCount, label: "concurrency", n: 1,
				help: "parallel lanes (-j), worktree-isolated and merged back"},
			{kind: optCycle, label: "suggest agent", flag: "--suggest-agent",
				// FastSuggest is the suggester that is not an agent: it reads
				// the tree for signals, costs nothing, and answers at once.
				values: suggestAgentValues(cfg),
				help:   "who proposes the reviews for a suggested run"},
			{kind: optToggle, label: "once", flag: "--once", on: true,
				help: "one loop, then stop"},
			{kind: optToggle, label: "dashboard", flag: "--tui", on: true,
				help: "live screen instead of scrolling output"},
			{kind: optToggle, label: "stacked PRs", flag: "--stacked-prs",
				help: "one isolated worktree; each changed review opens a PR on the previous one"},
			{kind: optToggle, label: "commit", flag: "--commit",
				help: "an agent commits what the reviews changed, on this branch"},
			{kind: optToggle, label: "push", flag: "--push",
				help: "commit, then git push to the remote (implies commit)"},
			{kind: optCycle, label: "merge into", flag: "--merge-into",
				values: append([]string{"stay on " + branchLabel(cfg.Branch)}, cfg.Merge...),
				help:   "merge each loop's commits into another branch (needs commit)"},
			{kind: optToggle, label: "yolo", flag: "--yolo",
				help: "drop the caution rules: bigger changes"},
		},
	}
	// One hue per agent, assigned in the order the dashboard would assign it,
	// so an agent keeps its color from this screen into the run.
	for _, a := range cfg.Agents {
		p.hues.get(a)
	}
	return p
}

// suggestAgentValues is the --suggest-agent cycle: unset, then the file-signal
// suggester when the caller named one, then every installed agent.
func suggestAgentValues(cfg PickConfig) []string {
	out := make([]string, 0, 2+len(cfg.Agents))
	out = append(out, "from the pool")
	if cfg.FastSuggest != "" {
		out = append(out, cfg.FastSuggest)
	}
	return append(out, cfg.Agents...)
}

// branchLabel names the branch the run would sit on, for a screen that must
// say something even off a branch.
func branchLabel(b string) string {
	if b == "" {
		return "this checkout"
	}
	return b
}

func (p *picker) Init() tea.Cmd { return nil }

// rows flattens the reviews pane as it currently stands: the suggest choice,
// every group header, and the members of the groups that are open.
func (p *picker) rows() []row {
	out := make([]row, 0, len(p.cfg.Groups)*4+1)
	out = append(out, row{kind: rowSuggest})
	for i, g := range p.cfg.Groups {
		matches := p.matching(g)
		// A filter is a search: it opens what it finds, and hides the rest.
		if p.filter != "" && len(matches) == 0 {
			continue
		}
		out = append(out, row{kind: rowGroup, group: i})
		if p.open[i] || p.filter != "" {
			for _, r := range matches {
				out = append(out, row{kind: rowReview, group: i, review: r})
			}
		}
	}
	return out
}

func (p *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.w, p.h, p.ready = msg.Width, msg.Height, true
	case tea.KeyMsg:
		return p.key(msg)
	}
	return p, nil
}

func (p *picker) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if p.help {
		// Same overlay contract as the dashboard: q/esc close help, they do
		// not leave the launcher. h stays a navigation key here.
		switch key {
		case "q", "ctrl+c", "esc", "?":
			p.help = false
		default:
			p.helpScroll = scrollHelp(p.helpScroll, key, p.helpLines(), p.w, p.h)
		}
		return p, nil
	}
	if p.typing {
		// Every key typed is a keypress, and an arm waits out a deliberate
		// second press of one of the two keys that set it, not a search
		// whose letters happen to spell one of them.
		p.disarm()
		return p.filterKey(msg, key)
	}
	switch key {
	case "ctrl+c":
		return p, tea.Quit
	case "q":
		// One press arms, so a slip of the finger does not throw away what
		// was picked; a second one leaves. Any other key takes the arm back,
		// which is what the footer and the status line say is happening.
		if p.quitArmed {
			if p.quitKey == "q" {
				return p, tea.Quit
			}
			p.disarm()
			return p, nil
		}
		p.arm("q")
		return p, nil
	case "esc":
		if p.quitArmed {
			// Only the key that asked confirms it. Esc answering a q's ask by
			// taking it back is the same courtesy q shows an armed esc, and
			// it is what keeps the pair from being two spellings of one
			// destructive key: a press of the other one is a reader changing
			// their mind, not a second one.
			if p.quitKey == "esc" {
				return p, tea.Quit
			}
			p.disarm()
			return p, nil
		}
		if p.filter != "" {
			p.filter = ""
			// Clearing a search restores the full tree. The cursor held a
			// row of the narrowed view; clamp first, then put it on the
			// first review the widened tree shows, so the bar the keys act
			// on is never stranded off the list (or on the suggest row).
			p.clampReviewCursor()
			return p, nil
		}
		// Nothing left for esc to go back from, so it asks the way q does
		// rather than leaving on the first press. Esc is the key a keyboard
		// user reaches for to back out of whatever they are in, and here the
		// one thing it can reach is the whole composed run: a press meant to
		// dismiss something, or a second press of a habit formed on the
		// dashboard, threw the picking away with no way back (WCAG 3.3.4).
		p.arm("esc")
		return p, nil
	case "?":
		p.help = true
		p.helpScroll = 0
		return p, nil
	case "enter":
		if p.blocked() != "" {
			return p, nil // the reason is on screen; nothing to launch yet
		}
		p.launch = true
		return p, tea.Quit
	case "tab":
		p.focus = (p.focus + 1) % paneCount
	case "shift+tab":
		p.focus = (p.focus + paneCount - 1) % paneCount
	case "right", "l":
		p.arrow(+1)
	case "left", "h":
		p.arrow(-1)
	case "down", "j":
		p.move(+1)
	case "up", "k":
		p.move(-1)
	case "pgdown", "pagedown":
		p.pageMove(+1)
	case "pgup", "pageup":
		p.pageMove(-1)
	case "home", "g":
		p.cursor[p.focus] = 0
	case "end", "G":
		if n := p.paneLen(p.focus); n > 0 {
			p.cursor[p.focus] = n - 1
		}
	case " ":
		p.toggle()
	case "a":
		p.toggleAll()
	case "/":
		p.typing, p.focus = true, paneReviews
	case "+", "=":
		if !p.stacked() {
			// The same ceiling every other key gives this row: past the
			// machine's cpus the meter is full and the extra lane has
			// nothing to run on, so + stops where the pane's own toggle
			// stops.
			p.stepConcurrency(+1)
		}
	case "-", "_":
		if !p.stacked() {
			p.stepConcurrency(-1)
		}
	}
	// A key that is neither of the two the arm waits for takes it back, so an
	// arm set minutes ago cannot turn the next q into a press the reader never
	// saw. The two cases above return first, so they are unaffected.
	p.disarm()
	return p, nil
}

// arm asks for a second press before the composed run is thrown away. key is
// the key that asked, and the only one whose second press answers: pressing
// the other one takes the arm back rather than confirming it, so a reader who
// reaches for esc after a q hears the same question and can decline it.
func (p *picker) arm(key string) {
	p.quitArmed, p.quitKey = true, key
}

// disarm takes the ask back. It is the only way quitArmed goes false, so
// quitKey is never left naming a key that is no longer asking.
func (p *picker) disarm() {
	p.quitArmed, p.quitKey = false, ""
}

// concurrencyKeys reports whether the +/- keys can change anything here. The
// legend drops them when they cannot, the way it drops every other key the
// focused pane does not act on.
func (p *picker) concurrencyKeys() bool {
	return !p.stacked() && p.cfg.CPUs > 1
}

// filterKey types into the review filter. Everything is a character while it
// is open, so a review named "quick" can be found by typing q-u-i-c-k without
// the q quitting the launcher.
func (p *picker) filterKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "ctrl+c":
		// The universal way out, and the one key the help overlay names as
		// closing whatever is on screen. It used to clear the filter here
		// like esc does, so a reader reaching for it to leave instead lost
		// their search and stayed: a key that is on screen as "close" and
		// does not close (WCAG 3.3.2). esc is the key that clears.
		return p, tea.Quit
	case "esc":
		p.filter, p.typing = "", false
		p.clampReviewCursor()
	case "enter":
		p.typing = false // the filter stays, the keys go back to the panes
		p.clampReviewCursor()
	case "tab":
		p.typing = false
		p.focus = (p.focus + 1) % paneCount
	case "shift+tab":
		p.typing = false
		p.focus = (p.focus + paneCount - 1) % paneCount
	case "ctrl+u":
		p.filter = ""
	case "ctrl+w":
		p.filter = trimLastWord(p.filter)
	case "backspace", "delete", "ctrl+h":
		if p.filter != "" {
			p.filter = trimLastCluster(p.filter)
		}
	case "up":
		p.move(-1)
	case "down":
		p.move(+1)
	case "pgup", "pageup":
		p.pageMove(-1)
	case "pgdown", "pagedown":
		p.pageMove(+1)
	case "home":
		p.cursor[paneReviews] = 0
	case "end":
		if n := p.paneLen(paneReviews); n > 0 {
			p.cursor[paneReviews] = n - 1
		}
	default:
		if key == " " {
			p.filter += " "
		} else if msg.Type == tea.KeyRunes {
			p.filter += string(msg.Runes)
		}
	}
	// The row set changes with the filter, so every key re-bounds the cursor,
	// not only the ones that close it. Where it lands is a pane decision:
	// home and end go to the ends of the list as-is.
	p.clampCursorRange()
	return p, nil
}

// clampCursorRange restores the reviews cursor to the last row when the list
// it indexes has shrunk under it.
func (p *picker) clampCursorRange() {
	p.cursor[paneReviews] = min(p.cursor[paneReviews], max(len(p.rows())-1, 0))
}

// clampReviewCursor restores the reviews cursor into range and onto the first
// review row if it was stranded off the list or on a non-review row.
func (p *picker) clampReviewCursor() {
	p.clampCursorRange()
	if r := p.rowAt(p.cursor[paneReviews]); r.kind != rowReview {
		for i, cand := range p.rows() {
			if cand.kind == rowReview {
				p.cursor[paneReviews] = i
				break
			}
		}
	}
}

// trimLastWord removes the trailing word and any whitespace following it,
// matching the standard terminal Ctrl-W editing shortcut.
func trimLastWord(s string) string {
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	if idx := strings.LastIndexFunc(s, unicode.IsSpace); idx >= 0 {
		_, size := utf8.DecodeRuneInString(s[idx:])
		return s[:idx+size]
	}
	return ""
}

// trimLastCluster removes the final grapheme cluster of s. One backspace is
// one keystroke's worth of text: a dead-key accent, an emoji sequence, or a
// flag arrives as several code points and must leave as one, or deleting a
// decomposed é would first peel the accent off and leave the letter behind.
func trimLastCluster(s string) string {
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, _ = uniseg.FirstGraphemeClusterInString(rest, -1)
		if len(rest) == 0 {
			return s[:len(s)-len(cluster)]
		}
	}
	return s
}

// concurrency is the run pane's job-count row, which +/- reach from any pane:
// it is the choice with a cost attached, so it should never need hunting for.
// It is the first row, and the only one of its kind, so the row is named by
// its position rather than found by scanning.
func (p *picker) concurrency() *option {
	return &p.opts[optConcurrency]
}

// paneLen is how many rows the given pane has, for cursor bounds.
func (p *picker) paneLen(which pane) int {
	switch which {
	case paneReviews:
		return len(p.rows())
	case paneAgents:
		return len(p.cfg.Agents)
	default:
		return len(p.opts)
	}
}

func (p *picker) move(d int) {
	n := p.paneLen(p.focus)
	if n == 0 {
		return
	}
	p.cursor[p.focus] = min(max(p.cursor[p.focus]+d, 0), n-1)
}

// viewChrome is the rows the launcher spends outside the panels: the header,
// the command, the status line, and the key line. What is left is what the
// panes divide between them.
const viewChrome = 4

// panelChrome is the border and padding one titled panel costs, and the right
// column stacks two of them.
const panelChrome = 3

// paneHeight returns the visible content height of the given pane.
func (p *picker) paneHeight(which pane) int {
	// The tree stands beside the two stacked panes, so it has the whole frame
	// to itself; the agent list and the run pane split what the right column
	// has left after their two frames. Reserving one frame's worth for the
	// pair is what left rows empty under a full-height run pane, and a
	// one-row agent list while three rows of screen went unused.
	free := max(4, p.h-viewChrome)
	switch which {
	case paneReviews:
		reviewRows := len(p.rows())
		if p.filterMissed(p.rows()) {
			reviewRows++ // the fruitless-filter notice needs its own row
		}
		return clampi(reviewRows, 1, free)
	case paneAgents:
		// The agent list takes what it needs, up to half of the column; the
		// run pane keeps the rest, so a long option list costs the list
		// rows rather than the pane a reader composes the run in.
		return clampi(len(p.cfg.Agents), 1, max((free-2*panelChrome)/2, 1))
	case paneOptions:
		return clampi(len(p.opts), 1, max(free-2*panelChrome-p.paneHeight(paneAgents), 1))
	default:
		return 1
	}
}

// pageMove steps the focused pane's cursor by one visible page.
func (p *picker) pageMove(d int) {
	step := max(p.paneHeight(p.focus)-1, 1)
	p.move(d * step)
}

func (p *picker) rowAt(i int) row {
	rows := p.rows()
	return rows[min(max(i, 0), len(rows)-1)]
}

// arrow is what the left and right keys do, by pane: they fold a set in the
// reviews tree, change a value in the run pane, and step to the neighbouring
// pane everywhere else. The suggest row is the one row of the tree with no
// fold, so there the arrows step panes too, which is what the legend promises
// while the cursor is on it. Keying them to nothing instead left a key the
// legend advertised doing nothing on the one row it named it for.
func (p *picker) arrow(d int) {
	switch p.focus {
	case paneReviews:
		if p.rowAt(p.cursor[paneReviews]).kind == rowSuggest {
			p.focus = p.sidePane(d)
			return
		}
		p.expand(d > 0)
	case paneOptions:
		p.adjust(d)
	default:
		p.focus = p.sidePane(d)
	}
}

// sidePane is the pane d steps to: one forward for the right key, one back for
// the left, wrapping at either end.
func (p *picker) sidePane(d int) pane {
	if d > 0 {
		return (p.focus + 1) % paneCount
	}
	return (p.focus + paneCount - 1) % paneCount
}

// expand opens or closes the group the cursor is in. Closing from a member
// moves the cursor up to its header, so the cursor never lands off-screen.
// Opening an already-expanded group steps into its first review.
func (p *picker) expand(open bool) {
	r := p.rowAt(p.cursor[paneReviews])
	if r.kind == rowSuggest {
		return
	}
	if open && r.kind == rowGroup && (p.open[r.group] || p.filter != "") {
		rows := p.rows()
		if cur := p.cursor[paneReviews]; cur+1 < len(rows) && rows[cur+1].kind == rowReview {
			p.cursor[paneReviews] = cur + 1
			return
		}
	}
	p.open[r.group] = open
	if !open {
		for i, cand := range p.rows() {
			if cand.kind == rowGroup && cand.group == r.group {
				p.cursor[paneReviews] = i
				break
			}
		}
	}
}

// stepConcurrency moves the job count by d and holds it inside 1..the
// machine's cpu count. The run pane draws the count against those cpus and
// the summary reads "N of M cpus", so M is the ceiling: past it a lane has
// nothing to run on, and the row is what composes --jobs. Every key that
// moves the count comes through here, so none of them can step past M.
func (p *picker) stepConcurrency(d int) {
	o := p.concurrency()
	o.n = min(max(1, o.n+d), max(p.cfg.CPUs, 1))
}

func (p *picker) adjust(d int) {
	o := &p.opts[p.cursor[paneOptions]]
	if p.optionDisabled(o) {
		return
	}
	switch o.kind {
	case optCount:
		p.stepConcurrency(d)
	case optCycle:
		if n := len(o.values); n > 0 {
			o.idx = ((o.idx+d)%n + n) % n
		}
	default:
		o.on = d > 0
	}
}

func (p *picker) toggle() {
	switch p.focus {
	case paneReviews:
		r := p.rowAt(p.cursor[paneReviews])
		switch r.kind {
		case rowSuggest:
			p.suggest = !p.suggest
			if p.suggest {
				// The next question a suggested run raises is who suggests,
				// and that lives in the run pane. Point at it.
				p.cursor[paneOptions] = optSuggestAgent
			}
		case rowReview:
			p.selected[r.review.Name] = !p.selected[r.review.Name]
		case rowGroup:
			// A filter hides the rest, so those stay put.
			p.fill(p.matching(p.cfg.Groups[r.group]))
			p.open[r.group] = true
		}
	case paneAgents:
		if len(p.agents) > 0 {
			i := p.cursor[paneAgents]
			p.agents[i] = !p.agents[i]
		}
	case paneOptions:
		o := &p.opts[p.cursor[paneOptions]]
		if p.optionDisabled(o) {
			return
		}
		switch o.kind {
		case optCount:
			// Space wraps rather than stops: at the ceiling it returns to
			// one lane, so the key is a two-position dial.
			if o.n >= max(p.cfg.CPUs, 1) {
				o.n = 1
			} else {
				p.stepConcurrency(+1)
			}
		case optToggle:
			o.on = !o.on
		case optCycle:
			p.adjust(+1)
		}
	}
}

// fill takes a set of reviews when they are not all chosen, and empties them
// when they already are.
func (p *picker) fill(revs []PickReview) {
	take := true
	for _, rev := range revs {
		if p.selected[rev.Name] {
			take = false
			break
		}
	}
	for _, rev := range revs {
		p.selected[rev.Name] = take
	}
}

// toggleAll clears the focused pane, or fills it when it is already empty.
// A filter bounds the reviews pane: hidden rows are not selected by accident.
func (p *picker) toggleAll() {
	switch p.focus {
	case paneReviews:
		p.fill(p.visibleReviews())
	case paneAgents:
		want := !slices.Contains(p.agents, true)
		for i := range p.agents {
			p.agents[i] = want
		}
	}
}

// visibleReviews is the reviews the tree is showing: the filter's matches,
// or everything when there is no filter. A review in two sets is one review.
func (p *picker) visibleReviews() []PickReview {
	var out []PickReview
	seen := map[string]bool{}
	for _, g := range p.cfg.Groups {
		for _, rev := range p.matching(g) {
			if seen[rev.Name] {
				continue
			}
			seen[rev.Name] = true
			out = append(out, rev)
		}
	}
	return out
}

func (p *picker) groupOn(i int) int {
	n := 0
	for _, rev := range p.cfg.Groups[i].Reviews {
		if p.selected[rev.Name] {
			n++
		}
	}
	return n
}

// matching is the members of a group the current filter keeps, by name or by
// what the review says it does.
//
// Both sides are normalized to NFC first: discovery stores every name NFC
// (see prompt.Set), but typed text arrives in whatever form the terminal
// sends, and a dead-key or IME spelling of the same word must find the same
// review here that --reviews finds on the command line. Case is then folded,
// not lowercased, so one spelling of a letter (final and ordinary sigma)
// finds the other; the same convention guides the "did you mean" hints
// (see fuzzy.Closest).
//
// The haystack side never changes during a session, so its folded forms are
// cached: folding walks unicode.SimpleFold orbits per rune, and doing that
// for every review on every keystroke would buy nothing.
func (p *picker) matching(g PickGroup) []PickReview {
	if p.filter == "" {
		return g.Reviews
	}
	needle := fuzzy.Fold(norm.NFC.String(p.filter))
	var out []PickReview
	for _, rev := range g.Reviews {
		fn, fd := p.folded(rev)
		if strings.Contains(fn, needle) || strings.Contains(fd, needle) {
			out = append(out, rev)
		}
	}
	return out
}

// folded returns the cached folded name and description of one review.
func (p *picker) folded(rev PickReview) (string, string) {
	key := rev.Name + "\x00" + rev.Desc
	if f, ok := p.folds[key]; ok {
		return f[0], f[1]
	}
	f := [2]string{
		fuzzy.Fold(norm.NFC.String(rev.Name)),
		fuzzy.Fold(norm.NFC.String(rev.Desc)),
	}
	p.folds[key] = f
	return f[0], f[1]
}

// chosen counts distinct selected reviews: a review in two sets is one review.
func (p *picker) chosen() int {
	n := 0
	for _, name := range p.knownReviews {
		if p.selected[name] {
			n++
		}
	}
	return n
}

// pickedAgents is the agent pool as chosen, empty meaning auto-detect.
func (p *picker) pickedAgents() []string {
	var out []string
	for i, on := range p.agents {
		if on {
			out = append(out, p.cfg.Agents[i])
		}
	}
	if len(out) == len(p.cfg.Agents) {
		return nil // every installed agent is what auto-detection already does
	}
	return out
}

// argv composes the run. Nothing that matches a default is passed: the point
// is a command a person would have typed, not an exhaustive one.
func (p *picker) argv() []string {
	var out []string
	if p.cfg.Dir != "" {
		out = append(out, "-C", p.cfg.Dir)
	}
	if p.cfg.PromptDir != "" {
		out = append(out, "--prompt-dir", p.cfg.PromptDir)
	}
	if p.suggest {
		out = append(out, "--suggest")
		if a := p.suggestAgent(); a != "" {
			out = append(out, p.opts[optSuggestAgent].flag, a)
		}
	}
	// Reviews ticked here ride along with a suggested run: the agent picks,
	// and what is named is scheduled as well, which is how weight is asked
	// for. Only a run picking nothing at all leaves --reviews off entirely.
	if r := p.reviewArgs(); r != "" {
		out = append(out, "-r", r)
	}
	if agents := p.pickedAgents(); len(agents) > 0 {
		out = append(out, "-a", strings.Join(agents, ","))
	}
	for _, o := range p.opts {
		switch {
		case o.kind == optCount && o.n > 1 && !p.stacked():
			out = append(out, "-j", fmt.Sprint(o.n))
		case o.kind == optCycle && o.flag == "--merge-into":
			if o.idx > 0 && p.committing() && !p.stacked() {
				out = append(out, o.flag, o.values[o.idx])
			}
		case o.kind == optCycle:
			// The suggest agent is emitted next to the choice it qualifies.
		case o.kind == optToggle && o.on:
			if o.flag == "--commit" && (p.pushing() || p.stacked()) {
				continue // --push already implies it; the stack owns commits
			}
			if o.flag == "--push" && p.stacked() {
				continue
			}
			out = append(out, o.flag)
		}
	}
	return out
}

// reviewArgs names the selection as briefly as it can: nothing when
// everything is selected, set names where a set is selected whole, and the
// leftovers by name with the -review suffix dropped, as the flag allows.
func (p *picker) reviewArgs() string {
	all := p.knownReviews
	if p.chosen() == 0 || (!p.suggest && p.chosen() == len(all)) {
		return ""
	}
	var parts []string
	covered := map[string]bool{}
	for i, g := range p.cfg.Groups {
		if len(g.Reviews) == 0 || p.groupOn(i) != len(g.Reviews) {
			continue
		}
		// Only a group named the way --reviews reads a set can be named in
		// one word. The launcher's catch-all group is a heading, not a set,
		// and passing it would compose a command line the parser refuses; its
		// members are named one by one below instead.
		if !slices.Contains(p.cfg.Reserved, g.Name) {
			continue
		}
		parts = append(parts, g.Name)
		for _, rev := range g.Reviews {
			covered[rev.Name] = true
		}
	}
	for _, name := range all {
		if p.selected[name] && !covered[name] {
			parts = append(parts, p.abbreviate(name))
		}
	}
	return strings.Join(parts, ",")
}

// abbreviate drops the "-review" suffix the flag allows to be left off, unless
// what remains is a word --reviews reads as something else.
//
// A tree carrying security-review.md would otherwise be named as "security",
// which the parser resolves as the security set -- eight other reviews, and
// not the one that was ticked. The same applies to "suggest", which would turn
// on the triage agent. Sets and the keyword are resolved before review names,
// so the full name is the only spelling that means what was selected.
func (p *picker) abbreviate(name string) string {
	short := reviewShort(name)
	if short == name {
		return name
	}
	if slices.Contains(p.cfg.Reserved, short) {
		return name
	}
	return short
}

// optByFlag finds a run-pane row by the flag it contributes.
func (p *picker) optByFlag(flag string) *option {
	for i := range p.opts {
		if p.opts[i].flag == flag {
			return &p.opts[i]
		}
	}
	return nil
}

// stacked reports whether the composed run is an unmerged PR stack.
func (p *picker) stacked() bool {
	o := p.optByFlag("--stacked-prs")
	return o != nil && o.on
}

// optionInert reports a run-pane row that cannot apply in the current mode:
// stack mode owns commits, pushes, merge targets, and the job count, so those
// rows are drawn and keyed as inert rather than composed into a command the
// parser would refuse.
func (p *picker) optionInert(o *option) bool {
	if o.flag == "--stacked-prs" {
		return false
	}
	if !p.stacked() {
		return false
	}
	if o.kind == optCount {
		return true
	}
	switch o.flag {
	case "--commit", "--push", "--merge-into":
		return true
	}
	return false
}

// optionDisabled reports an option that is currently inactive: stacked-mode
// overrides, suggest agent when suggest is off, or merge targets when commits are off.
func (p *picker) optionDisabled(o *option) bool {
	if p.optionInert(o) {
		return true
	}
	if o.flag == "--suggest-agent" && !p.suggest {
		return true
	}
	if o.flag == "--merge-into" && !p.committing() {
		return true
	}
	return false
}

// committing reports whether the composed run produces commits at all, which
// is what a merge target needs to mean anything.
func (p *picker) committing() bool {
	if p.stacked() {
		return false
	}
	c := p.optByFlag("--commit")
	return p.pushing() || (c != nil && c.on)
}

// pushing reports whether the composed run ends in a git push.
func (p *picker) pushing() bool {
	o := p.optByFlag("--push")
	return o != nil && o.on
}

// suggestAgent is the label chosen for the suggest step, or "" for whichever
// agent the pool offers.
func (p *picker) suggestAgent() string {
	o := p.opts[optSuggestAgent]
	if o.idx == 0 {
		return ""
	}
	return o.values[o.idx]
}
