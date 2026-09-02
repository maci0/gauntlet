// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/fuzzy"
)

// The launcher: the screen `gauntlet pick` opens so a run can be composed
// without knowing the flags first. It decides nothing on its own. What it
// produces is an argv, which the caller runs through the same parser every
// other invocation goes through, and which it shows on screen the whole time
// so the flags are learned rather than hidden.
//
// It is drawn as the dashboard is drawn, with the same instruments: the
// wordmark and a spread header, titled rounded panels, one hue per agent
// everywhere it appears, and meters that show their unlit remainder. The two
// screens are the same cockpit at two moments, so they read the same way.

// PickConfig is what the launcher needs to know about this machine: what can
// be reviewed, what can review it, and how much of it can run at once.
type PickConfig struct {
	Dir     string      // the directory the composed run will review
	Groups  []PickGroup // review sets, in display order
	Agents  []string    // installed agent labels, empty when none were found
	Branch  string      // the branch the reviews would run on, "" off a branch
	Merge   []string    // other local branches, as merge targets
	Dirty   bool        // tracked files have uncommitted changes, which worktrees refuse
	CPUs    int         // the concurrency meter is drawn against this
	Version string
	// Reserved are the words --reviews reads as something other than a
	// review name: the set names and the suggest keyword. The launcher
	// abbreviates a selection by dropping the "-review" suffix, and the
	// caller resolves sets before review names, so a stem that lands on one
	// of these has to be written out in full. Passed in rather than looked
	// up here: nothing under internal/ui imports the prompt catalog.
	Reserved []string
	// FastSuggest is the --suggest-agent value that picks reviews from file
	// signals instead of asking a model. Empty omits that choice. Passed in
	// rather than looked up here: the picker does not import the runner.
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

	suggest  bool            // let an agent pick the reviews instead
	filter   string          // narrows the review tree by name or description
	typing   bool            // keys are going into the filter, not the panes
	open     []bool          // per group
	selected map[string]bool // review name -> chosen
	agents   []bool          // per installed agent
	opts     []option

	cursor [paneCount]int
	scroll [paneCount]int
	help   bool

	// The config is fixed for the life of a session, so two derived views of
	// it are computed once instead of per render: every distinct review, and
	// the folded form each name and description is matched against.
	knownReviews []string
	folds        map[string][2]string
}

// optSuggestAgent is the index of the run-pane row that names the agent for
// the suggest step, so the reviews pane can point the cursor at it.
const optSuggestAgent = 1

func newPicker(cfg PickConfig) *picker {
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
				help:   "who proposes the reviews; gauntlet reads the files instead of asking a model"},
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
		}
		return p, nil
	}
	if p.typing {
		return p.filterKey(msg, key)
	}
	switch key {
	case "ctrl+c", "q", "esc":
		return p, tea.Quit
	case "?":
		p.help = true
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
		switch p.focus {
		case paneReviews:
			p.expand(true)
		case paneOptions:
			p.adjust(+1)
		default:
			p.focus = (p.focus + 1) % paneCount
		}
	case "left", "h":
		switch p.focus {
		case paneReviews:
			p.expand(false)
		case paneOptions:
			p.adjust(-1)
		default:
			p.focus = (p.focus + paneCount - 1) % paneCount
		}
	case "down", "j":
		p.move(+1)
	case "up", "k":
		p.move(-1)
	case "home":
		p.cursor[p.focus] = 0
	case "end":
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
			p.concurrency().n++
		}
	case "-", "_":
		if !p.stacked() {
			p.concurrency().n = max(1, p.concurrency().n-1)
		}
	}
	return p, nil
}

// filterKey types into the review filter. Everything is a character while it
// is open, so a review named "quick" can be found by typing q-u-i-c-k without
// the q quitting the launcher.
func (p *picker) filterKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		p.filter, p.typing = "", false
	case "enter":
		p.typing = false // the filter stays, the keys go back to the panes
	case "backspace":
		if p.filter != "" {
			p.filter = trimLastCluster(p.filter)
		}
	case "up", "down":
		p.move(map[string]int{"up": -1, "down": +1}[key])
	default:
		if msg.Type == tea.KeyRunes || key == " " {
			p.filter += string(msg.Runes)
		}
	}
	p.cursor[paneReviews] = min(p.cursor[paneReviews], max(len(p.rows())-1, 0))
	return p, nil
}

// trimLastCluster removes the final grapheme cluster of s. One backspace is
// one keystroke's worth of text: a dead-key accent, an emoji sequence, or a
// flag arrives as several code points and must leave as one, or deleting a
// decomposed é would first peel the accent off and leave the letter behind.
func trimLastCluster(s string) string {
	cut := 0
	rest := s
	for len(rest) > 0 {
		var cluster string
		cluster, rest, _, _ = uniseg.FirstGraphemeClusterInString(rest, -1)
		if len(rest) == 0 {
			cut = len(s) - len(cluster)
		}
	}
	return s[:cut]
}

// concurrency is the run pane's job-count row, which +/- reach from any pane:
// it is the choice with a cost attached, so it should never need hunting for.
func (p *picker) concurrency() *option {
	for i := range p.opts {
		if p.opts[i].kind == optCount {
			return &p.opts[i]
		}
	}
	return &option{}
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

func (p *picker) rowAt(i int) row {
	rows := p.rows()
	return rows[min(max(i, 0), len(rows)-1)]
}

// expand opens or closes the group the cursor is in. Closing from a member
// moves the cursor up to its header, so the cursor never lands off-screen.
func (p *picker) expand(open bool) {
	r := p.rowAt(p.cursor[paneReviews])
	if r.kind == rowSuggest {
		return
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

func (p *picker) adjust(d int) {
	o := &p.opts[p.cursor[paneOptions]]
	if p.optionInert(o) {
		return
	}
	switch o.kind {
	case optCount:
		o.n = max(1, o.n+d)
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
			// A header fills its visible members, and empties them when they
			// are already full. A filter hides the rest, so those stay put.
			matches := p.matching(p.cfg.Groups[r.group])
			want := false
			for _, rev := range matches {
				if !p.selected[rev.Name] {
					want = true
					break
				}
			}
			for _, rev := range matches {
				p.selected[rev.Name] = want
			}
			p.open[r.group] = true
		}
	case paneAgents:
		if len(p.agents) > 0 {
			i := p.cursor[paneAgents]
			p.agents[i] = !p.agents[i]
		}
	case paneOptions:
		o := &p.opts[p.cursor[paneOptions]]
		if p.optionInert(o) {
			return
		}
		switch o.kind {
		case optToggle:
			o.on = !o.on
			if o.flag == "--stacked-prs" && o.on {
				// Stack mode owns commits, pushes, and the job count; the
				// parser refuses the combination, so the screen must not
				// compose it.
				if c := p.optByFlag("--commit"); c != nil {
					c.on = false
				}
				if u := p.optByFlag("--push"); u != nil {
					u.on = false
				}
				if m := p.optByFlag("--merge-into"); m != nil {
					m.idx = 0
				}
				p.concurrency().n = 1
			}
		case optCycle:
			p.adjust(+1)
		}
	}
}

// toggleAll clears the focused pane, or fills it when it is already empty.
// A filter bounds the reviews pane: hidden rows are not selected by accident.
func (p *picker) toggleAll() {
	switch p.focus {
	case paneReviews:
		revs := p.visibleReviews()
		want := true
		for _, rev := range revs {
			if p.selected[rev.Name] {
				want = false
				break
			}
		}
		for _, rev := range revs {
			p.selected[rev.Name] = want
		}
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
	if p.chosen() == 0 || p.chosen() == len(all) {
		return ""
	}
	var parts []string
	covered := map[string]bool{}
	for i, g := range p.cfg.Groups {
		if len(g.Reviews) > 0 && p.groupOn(i) == len(g.Reviews) {
			parts = append(parts, g.Name)
			for _, rev := range g.Reviews {
				covered[rev.Name] = true
			}
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

func (p *picker) View() string {
	if !p.ready {
		return "\n  gauntlet is warming up…"
	}
	if p.help {
		return p.renderHelp()
	}
	if p.w < 50 || p.h < 12 {
		return p.renderNarrow()
	}
	leftW := clampi(p.w*3/5, 34, p.w-28)
	rightW := p.w - leftW - 1
	// Chrome: header, two panel titles and borders on the right, the command
	// line, the hint, and the keys.
	free := max(4, p.h-9)
	// Each panel costs three rows of chrome. The run pane is where the cost
	// of a run is chosen, so it keeps its rows and the agent list gives first;
	// both scroll rather than spilling off the screen.
	runH := clampi(len(p.opts), 1, max(free-7, 1))
	agentH := clampi(len(p.cfg.Agents), 1, max(free-runH-6, 1))
	// The tree takes what it needs and no more; the panels beside it are
	// sized by the terminal, not by how many groups happen to be open.
	reviewRows := len(p.rows())
	if p.filterMissed(p.rows()) {
		reviewRows++ // the fruitless-filter notice needs its own row
	}
	reviewH := clampi(reviewRows, 1, free)

	right := lipgloss.JoinVertical(lipgloss.Left,
		p.agentPanel(rightW, agentH),
		p.runPanel(rightW, runH),
	)
	return strings.Join([]string{
		p.renderHeader(),
		lipgloss.JoinHorizontal(lipgloss.Top, p.reviewPanel(leftW, reviewH), " ", right),
		p.renderCommand(),
		p.renderStatus(),
		p.renderKeys(),
	}, "\n")
}

// renderHeader is the dashboard's header at rest: the same wordmark, version,
// and spread, saying what this run would be rather than what it is doing.
func (p *picker) renderHeader() string {
	left := []string{wordmark()}
	scope := fmt.Sprintf("%d of %d reviews", p.chosen(), len(p.knownReviews))
	switch {
	case p.suggest && p.chosen() > 0:
		scope = fmt.Sprintf("agent-picked, %d also scheduled", p.chosen())
	case p.suggest:
		scope = "agent-picked reviews"
	case p.chosen() == 0:
		// Nothing picked is not an empty run: it is every review, which is
		// what the composed command says by saying nothing.
		scope = fmt.Sprintf("all %d reviews", len(p.knownReviews))
	}
	agents := "all installed"
	if picked := p.pickedAgents(); len(picked) > 0 {
		agents = fmt.Sprintf("%d of %d agents", len(picked), len(p.cfg.Agents))
	}
	right := strings.Join([]string{
		styleValue.Render(scope),
		styleDim.Render(agents),
	}, "  ")

	// The right side is what this run would be: the scope changes with every
	// toggle and is the header's reason to exist. Dim chrome yields to it
	// piece by piece, so a narrow terminal loses the version or the title
	// before it loses what the run would cover.
	fits := func(piece string) bool {
		joined := strings.Join(left, "  ") + "  " + piece
		return lipgloss.Width(joined)+lipgloss.Width(right)+2 <= p.w
	}
	for _, piece := range []string{
		styleDim.Render("v" + p.cfg.Version),
		styleInfo.Render("compose a run"),
	} {
		if fits(piece) {
			left = append(left, piece)
		}
	}
	// The directory takes what the rest of the header leaves, and none of it
	// when there is none to take.
	room := p.w - lipgloss.Width(strings.Join(left, "  ")) - lipgloss.Width(right) - 4
	if room >= 4 {
		left = append(left, styleDim.Render(dirLabel(p.cfg.Dir, room)))
	}
	return spread(strings.Join(left, "  "), right, p.w)
}

func (p *picker) renderCommand() string {
	cmd := "gauntlet " + strings.Join(p.argv(), " ")
	return clip(styleDim.Render("$ ")+styleValue.Render(cmd), p.w)
}

// blocked reports why the composed run would not start, or "" when it would.
// Worktree isolation cuts branches from a commit, so uncommitted edits to
// tracked files would be invisible to every review: better to say so here
// than to compose a command that fails on launch. Untracked files do not
// block --jobs, so they do not block the launcher either. The same goes for
// an empty agent pool: every run auto-detects its agents, so nothing here
// can launch at all.
func (p *picker) blocked() string {
	if len(p.cfg.Agents) == 0 {
		return "no agent CLI is installed: install one (see: gauntlet doctor)"
	}
	if p.cfg.Dirty && p.concurrency().n > 1 {
		return "concurrency above 1 needs a clean tree: commit or stash first, or set it back to 1"
	}
	return ""
}

// hint explains the row under the cursor, in the one place that always has
// room for a sentence. A review row explains itself: descriptions are longer
// than any pane column, so the status line is where one is read whole.
func (p *picker) hint() string {
	if p.typing {
		return "filter: " + p.filter + "▏  ⏎ keep it, esc clear it"
	}
	switch p.focus {
	case paneOptions:
		o := p.opts[p.cursor[paneOptions]]
		if p.optionInert(&o) {
			return "stacked PRs own this; turn that off to change it"
		}
		return o.help
	case paneAgents:
		return "the pool reviews are drawn from; none picked means auto-detect"
	default:
		switch r := p.rowAt(p.cursor[paneReviews]); r.kind {
		case rowSuggest:
			return "an agent reads the repo and proposes the reviews, before any run"
		case rowGroup:
			return "space takes the whole set, →/← open and close it"
		default:
			if d := strings.TrimSpace(r.review.Desc); d != "" {
				return d
			}
			return "space takes this review on its own"
		}
	}
}

// renderStatus is the line under the command: what is blocking a launch if
// anything is, otherwise what the cursor is on.
func (p *picker) renderStatus() string {
	if why := p.blocked(); why != "" {
		return clip(styleWarn.Render("⚠ "+why), p.w)
	}
	return clip(styleDim.Render(p.hint()), p.w)
}

// renderKeys names the keys, most important first, and drops from the right
// end when the terminal is narrow: a keyboard user who cannot find how to
// move between rows or leave is stranded, so those come before niceties like
// bulk selection. What fits is what is shown, never clipped mid-name.
func (p *picker) renderKeys() string {
	keys := []struct{ k, v string }{
		{"⏎", "run"}, {"q", "cancel"}, {"j/k", "move"},
		{"?", "help"}, {"tab", "pane"}, {"space", "toggle"}, {"←/→", "open/close"},
		{"/", "filter"}, {"+/-", "concurrency"}, {"a", "all/none"},
	}
	if p.typing {
		// q types into the filter rather than leaving, and enter keeps the
		// filter rather than launching. Advertising the pane keys here is
		// how a reader thinks they cancelled a run they only searched.
		keys = []struct{ k, v string }{
			{"⏎", "keep"}, {"esc", "clear"}, {"↑↓", "move"},
		}
	}
	var b strings.Builder
	for _, k := range keys {
		seg := styleValue.Render(k.k) + styleDim.Render(":"+k.v)
		if w := lipgloss.Width(seg); b.Len() > 0 && lipgloss.Width(b.String())+2+w > p.w {
			break
		}
		if b.Len() > 0 {
			b.WriteString("  ")
		}
		b.WriteString(seg)
	}
	return clip(b.String(), p.w)
}

// renderNarrow is the fallback for a terminal too small for the panels: the
// choices still matter, so the command line is what survives. Rows clip to
// the pane for the same reason the dashboard's fallback does: a wrapped
// fallback is a taller broken screen, not a smaller one.
func (p *picker) renderNarrow() string {
	rows := []string{
		wordmark() + styleDim.Render("  compose a run"),
		styleDim.Render("terminal too small for the launcher"),
		styleValue.Render("gauntlet " + strings.Join(p.argv(), " ")),
	}
	// Enter is dead while the run is blocked, and the reason is the only
	// thing here that says so: the wide view carries it on the status line,
	// but this view has no status line to carry it.
	if why := p.blocked(); why != "" {
		rows = append(rows, styleWarn.Render("⚠ "+why))
	}
	keys := "⏎ run  q cancel  ? help"
	if p.typing {
		keys = "⏎ keep  esc clear"
	}
	rows = append(rows, styleDim.Render(keys))
	for i, r := range rows {
		rows[i] = clip(r, p.w)
	}
	return strings.Join(rows, "\n")
}

func (p *picker) renderHelp() string {
	lines := []string{
		styleTitle.Render("compose a run"),
		styleDim.Render("q  esc  ?  close this help"),
		"",
		"  tab          reviews, agents, and run options",
		"  j / k        move within a pane",
		"  space        toggle a review, a set, an agent, or a switch",
		"  ← / →        open or close a set; change a value",
		"  a            all or none of what this pane is showing",
		"  /            filter reviews by name or description; enter keeps it, esc clears",
		"  home / end   first / last row in this pane",
		"  + / -        raise or lower concurrency",
		"  enter        run the composed command",
		"  q            leave without running",
		"",
		styleDim.Render("  Picking no reviews runs all of them."),
		styleDim.Render("  suggest: an agent proposes the reviews; anything ticked is also scheduled."),
		styleDim.Render("  stacked PRs: each changed review opens a PR on the previous one."),
	}
	if why := p.blocked(); why != "" {
		lines = append(lines, "", styleWarn.Render("  "+why))
	}
	return clipBlock(lines, p.w, p.h)
}

// window keeps the cursor inside the visible slice of a pane, scrolling only
// when it would otherwise leave.
func (p *picker) window(which pane, n, h int) (from, to int) {
	cur := min(p.cursor[which], max(0, n-1))
	top := min(cur, p.scroll[which])
	if cur >= top+h {
		top = cur - h + 1
	}
	top = max(0, min(top, max(0, n-h)))
	p.scroll[which] = top
	return top, min(n, top+h)
}

// filterMissed reports whether the current filter matches nothing: rows then
// holds only the suggest row, since every surviving match contributes at
// least its group header. View reserves the notice's row from this, and
// reviewPanel renders the notice from it, so the two cannot drift.
func (p *picker) filterMissed(rows []row) bool {
	return p.filter != "" && len(rows) == 1
}

func (p *picker) reviewPanel(w, h int) string {
	inner := w - 4 // the panel border and its padding
	rows := p.rows()
	from, to := p.window(paneReviews, len(rows), h)
	// One name column for the whole pane, so the descriptions line up.
	nameW := 4
	for _, r := range rows {
		if r.kind == rowReview {
			nameW = max(nameW, lipgloss.Width(reviewLabel(r.review)))
		}
	}
	nameW = min(nameW, inner/3)
	lines := make([]string, 0, h)
	for i := from; i < to; i++ {
		r := rows[i]
		cur := p.focus == paneReviews && i == p.cursor[paneReviews]
		switch r.kind {
		case rowSuggest:
			right := ""
			if p.suggest {
				if a := p.suggestAgent(); a != "" {
					right = styled(p.hues.get(a), a)
				} else {
					right = styleDim.Render("any agent")
				}
			}
			lines = append(lines, pickLine(cur, inner,
				checkbox(p.suggest)+" "+styleValue.Render("suggest")+
					styleDim.Render("  an agent picks the reviews"), right))
		case rowGroup:
			g := p.cfg.Groups[r.group]
			mark := "▸"
			if p.open[r.group] {
				mark = "▾"
			}
			on := p.groupOn(r.group)
			count := styleDim.Render(fmt.Sprintf("%d/%d", on, len(g.Reviews)))
			name := styleValue.Render(g.Name)
			lines = append(lines, pickLine(cur, inner,
				styleDim.Render(mark)+" "+name,
				count+" "+meter(float64(on)/float64(max(len(g.Reviews), 1)), 6, cTeal)))
		case rowReview:
			rev := r.review
			name := reviewLabel(rev)
			box := checkbox(p.selected[rev.Name])
			row := "   " + box + " " + pad(name, nameW)
			// The description is what makes a name mean something. It sits in
			// one column so the eye can run down it, and is the first thing to
			// go when the pane is narrow.
			if room := inner - lipgloss.Width(row) - 3; room > 12 {
				row += " " + styleFaint.Render(trim(rev.Desc, room))
			}
			lines = append(lines, pickLine(cur, inner, row, ""))
		}
	}
	// A filter that matches nothing hides the whole tree: without a word,
	// silence reads as an empty prompt set rather than a search that missed.
	// The dashboard's feed carries the same message for the same reason.
	if p.filterMissed(rows) {
		lines = append(lines, styleFaint.Render("no reviews match this filter (esc clears it)"))
	}
	title := fmt.Sprintf("REVIEWS  %d of %d", p.chosen(), len(p.knownReviews))
	if p.chosen() == 0 {
		title = fmt.Sprintf("REVIEWS  all %d", len(p.knownReviews))
	}
	if p.filter != "" {
		title += styleInfo.Render("   /" + p.filter)
	}
	if p.suggest {
		title = "REVIEWS  " + styleInfo.Render("chosen by an agent at run time")
		if n := p.chosen(); n > 0 {
			title = fmt.Sprintf("REVIEWS  %s", styleInfo.Render(
				fmt.Sprintf("agent-picked, plus %d also scheduled", n)))
		}
	}
	if hidden := len(rows) - (to - from); hidden > 0 {
		title += styleDim.Render(fmt.Sprintf("   +%d more", hidden))
	}
	return panel(title, strings.Join(lines, "\n"), inner, h)
}

func (p *picker) agentPanel(w, h int) string {
	inner := w - 4
	if len(p.cfg.Agents) == 0 {
		// The pane still takes focus when it has nothing to list, so it must
		// carry the cursor bar like any other pane: a screen with no ❯ leaves
		// the keyboard nowhere to be.
		body := styleBad.Render("none installed") +
			styleDim.Render(" (see: gauntlet doctor)")
		if p.focus == paneAgents {
			body = styleInfo.Render("❯ ") + body
		} else {
			body = "  " + body
		}
		return panel("AGENTS", body, inner, h)
	}
	from, to := p.window(paneAgents, len(p.cfg.Agents), h)
	lines := make([]string, 0, h)
	for i := from; i < to; i++ {
		cur := p.focus == paneAgents && i == p.cursor[paneAgents]
		label := p.cfg.Agents[i]
		lines = append(lines, pickLine(cur, inner,
			checkbox(p.agents[i])+" "+styled(p.hues.get(label), label), ""))
	}
	title := "AGENTS"
	if len(p.pickedAgents()) == 0 {
		title += styleDim.Render("  none picked: auto-detect")
	}
	if hidden := len(p.cfg.Agents) - (to - from); hidden > 0 {
		title += styleDim.Render(fmt.Sprintf("   +%d more", hidden))
	}
	return panel(title, strings.Join(lines, "\n"), inner, h)
}

func (p *picker) runPanel(w, h int) string {
	inner := w - 4
	from, to := p.window(paneOptions, len(p.opts), h)
	lines := make([]string, 0, h)
	for i := from; i < to; i++ {
		o := p.opts[i]
		cur := p.focus == paneOptions && i == p.cursor[paneOptions]
		var left, right string
		inert := p.optionInert(&o)
		switch o.kind {
		case optCount:
			left = "  " + o.label
			// Concurrency against the machine, drawn like every other meter:
			// the unlit remainder is the headroom left.
			frac := float64(o.n) / float64(max(p.cfg.CPUs, 1))
			right = meter(frac, 8, heatColor(frac)) + " " +
				styleValue.Render(fmt.Sprint(o.n)) + styleDim.Render(fmt.Sprintf("/%d cpu", p.cfg.CPUs))
			if inert {
				left = styleFaint.Render("  " + o.label)
				right = styleFaint.Render(fmt.Sprint(o.n) + fmt.Sprintf("/%d cpu", p.cfg.CPUs))
			}
		case optCycle:
			// A cycle row that cannot apply yet is drawn inert: the suggest
			// agent without suggest, a merge target without commits.
			value := o.values[o.idx]
			applies := p.suggest && !inert
			chosen := styled(p.hues.get(value), value)
			if o.flag == "--merge-into" {
				applies = p.committing() && !inert
				chosen = styleValue.Render(value)
			}
			left = "  " + o.label
			switch {
			case !applies || inert:
				left = styleFaint.Render("  " + o.label)
				right = styleFaint.Render(value)
			case o.idx == 0:
				right = styleDim.Render(value)
			default:
				right = chosen
			}
		default:
			left = checkbox(o.on) + " " + o.label
			if inert {
				mark := "[ ] "
				if o.on {
					mark = "[x] "
				}
				left = styleFaint.Render(mark + o.label)
			}
		}
		lines = append(lines, pickLine(cur, inner, left, right))
	}
	title := "RUN"
	if hidden := len(p.opts) - (to - from); hidden > 0 {
		title += styleDim.Render(fmt.Sprintf("   +%d more", hidden))
	}
	return panel(title, strings.Join(lines, "\n"), inner, h)
}

// reviewLabel is a review's name as the pane shows it: the -review suffix is
// noise repeated 50 times, and a prompt the reviewed tree carries overrides
// the bundled one of that name, which is worth knowing before picking it.
func reviewLabel(rev PickReview) string {
	name := reviewShort(rev.Name)
	if rev.Project {
		name += " [project]"
	}
	return name
}

func checkbox(on bool) string {
	if on {
		return styleOK.Render("[x]")
	}
	return styleDim.Render("[ ]")
}

// pickLine lays one row out: left text, right text, and the cursor bar that
// says which row the keys will act on.
func pickLine(cursor bool, w int, left, right string) string {
	body := spread(left, right, max(w-2, 1))
	if cursor {
		return styleInfo.Render("❯ ") + body
	}
	return "  " + body
}
