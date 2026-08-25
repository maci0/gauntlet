// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	CPUs    int         // the concurrency meter is drawn against this
	Version string
}

// PickGroup is one collapsible category: a review set and the members of it
// that exist in this prompt directory.
type PickGroup struct {
	Name    string
	Reviews []string
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
	review string
}

type picker struct {
	cfg    PickConfig
	hues   *hueMap
	w, h   int
	ready  bool
	launch bool

	focus pane

	suggest  bool            // let an agent pick the reviews instead
	open     []bool          // per group
	selected map[string]bool // review name -> chosen
	agents   []bool          // per installed agent
	opts     []option

	cursor [paneCount]int
	scroll [paneCount]int
}

// optSuggestAgent is the index of the run-pane row that names the agent for
// the suggest step, so the reviews pane can point the cursor at it.
const optSuggestAgent = 1

func newPicker(cfg PickConfig) *picker {
	p := &picker{
		cfg:      cfg,
		hues:     newHueMap(),
		open:     make([]bool, len(cfg.Groups)),
		selected: map[string]bool{},
		agents:   make([]bool, len(cfg.Agents)),
		opts: []option{
			{kind: optCount, label: "jobs", n: 1,
				help: "reviews at a time, each in its own git worktree"},
			{kind: optCycle, label: "suggest agent", flag: "--suggest-agent",
				values: append([]string{"from the pool"}, cfg.Agents...),
				help:   "which agent proposes the reviews"},
			{kind: optToggle, label: "once", flag: "--once", on: true,
				help: "one loop, then stop"},
			{kind: optToggle, label: "dashboard", flag: "--tui", on: true,
				help: "live screen instead of scrolling output"},
			{kind: optToggle, label: "commit", flag: "--commit",
				help: "commit after each review"},
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

func (p *picker) Init() tea.Cmd { return nil }

// rows flattens the reviews pane as it currently stands: the suggest choice,
// every group header, and the members of the groups that are open.
func (p *picker) rows() []row {
	out := make([]row, 0, len(p.cfg.Groups)*4+1)
	out = append(out, row{kind: rowSuggest})
	for i, g := range p.cfg.Groups {
		out = append(out, row{kind: rowGroup, group: i})
		if p.open[i] {
			for _, r := range g.Reviews {
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
	switch key {
	case "ctrl+c", "q", "esc":
		return p, tea.Quit
	case "enter":
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
	case " ":
		p.toggle()
	case "a":
		p.toggleAll()
	}
	return p, nil
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
			p.selected[r.review] = !p.selected[r.review]
		case rowGroup:
			// A header fills its group, and empties it when it is already full.
			g := p.cfg.Groups[r.group]
			want := p.groupOn(r.group) < len(g.Reviews)
			for _, name := range g.Reviews {
				p.selected[name] = want
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
		switch o.kind {
		case optToggle:
			o.on = !o.on
		case optCycle:
			p.adjust(+1)
		}
	}
}

// toggleAll clears the focused pane, or fills it when it is already empty.
func (p *picker) toggleAll() {
	switch p.focus {
	case paneReviews:
		want := p.chosen() == 0
		for _, g := range p.cfg.Groups {
			for _, name := range g.Reviews {
				p.selected[name] = want
			}
		}
	case paneAgents:
		want := !slicesAny(p.agents)
		for i := range p.agents {
			p.agents[i] = want
		}
	}
}

func (p *picker) groupOn(i int) int {
	n := 0
	for _, name := range p.cfg.Groups[i].Reviews {
		if p.selected[name] {
			n++
		}
	}
	return n
}

// chosen counts distinct selected reviews: a review in two sets is one review.
func (p *picker) chosen() int {
	n := 0
	for _, name := range p.known() {
		if p.selected[name] {
			n++
		}
	}
	return n
}

// known is every review the launcher offers, deduplicated.
func (p *picker) known() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, g := range p.cfg.Groups {
		for _, name := range g.Reviews {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
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
	} else if r := p.reviewArgs(); r != "" {
		out = append(out, "-r", r)
	}
	if agents := p.pickedAgents(); len(agents) > 0 {
		out = append(out, "-a", strings.Join(agents, ","))
	}
	for _, o := range p.opts {
		switch {
		case o.kind == optCount && o.n > 1:
			out = append(out, "-j", fmt.Sprint(o.n))
		case o.kind == optCycle:
			// Already emitted next to the choice it qualifies.
		case o.kind == optToggle && o.on:
			out = append(out, o.flag)
		}
	}
	return out
}

// reviewArgs names the selection as briefly as it can: nothing when
// everything is selected, set names where a set is selected whole, and the
// leftovers by name with the -review suffix dropped, as the flag allows.
func (p *picker) reviewArgs() string {
	all := p.known()
	if p.chosen() == 0 || p.chosen() == len(all) {
		return ""
	}
	var parts []string
	covered := map[string]bool{}
	for i, g := range p.cfg.Groups {
		if len(g.Reviews) > 0 && p.groupOn(i) == len(g.Reviews) {
			parts = append(parts, g.Name)
			for _, name := range g.Reviews {
				covered[name] = true
			}
		}
	}
	for _, name := range all {
		if p.selected[name] && !covered[name] {
			parts = append(parts, strings.TrimSuffix(name, "-review"))
		}
	}
	return strings.Join(parts, ",")
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
		return ""
	}
	if p.w < 50 || p.h < 12 {
		return p.renderNarrow()
	}
	leftW := clampi(p.w*3/5, 34, p.w-28)
	rightW := p.w - leftW - 1
	// Chrome: header, two panel titles and borders on the right, the command
	// line, the hint, and the keys.
	free := max(4, p.h-9)
	runH := len(p.opts)
	agentH := clampi(len(p.cfg.Agents), 1, max(free-runH-3, 1))
	// The tree takes what it needs and no more; the panels beside it are
	// sized by the terminal, not by how many groups happen to be open.
	reviewH := clampi(len(p.rows()), 1, free)

	right := lipgloss.JoinVertical(lipgloss.Left,
		p.agentPanel(rightW, agentH),
		p.runPanel(rightW, runH),
	)
	return strings.Join([]string{
		p.renderHeader(),
		lipgloss.JoinHorizontal(lipgloss.Top, p.reviewPanel(leftW, reviewH), " ", right),
		p.renderCommand(),
		clip(styleDim.Render(p.hint()), p.w),
		p.renderKeys(),
	}, "\n")
}

// renderHeader is the dashboard's header at rest: the same wordmark, version,
// and spread, saying what this run would be rather than what it is doing.
func (p *picker) renderHeader() string {
	left := []string{
		wordmark(),
		styleDim.Render("v" + p.cfg.Version),
		styleInfo.Render("compose a run"),
		styleDim.Render(filepath.Base(p.cfg.Dir)),
	}
	scope := fmt.Sprintf("%d of %d reviews", p.chosen(), len(p.known()))
	if p.suggest {
		scope = "agent-picked reviews"
	}
	agents := "all installed"
	if picked := p.pickedAgents(); len(picked) > 0 {
		agents = fmt.Sprintf("%d of %d agents", len(picked), len(p.cfg.Agents))
	}
	right := strings.Join([]string{
		styleValue.Render(scope),
		styleDim.Render(agents),
	}, "  ")
	return spread(strings.Join(left, "  "), right, p.w)
}

func (p *picker) renderCommand() string {
	cmd := "gauntlet " + strings.Join(p.argv(), " ")
	return clip(styleDim.Render("$ ")+styleValue.Render(cmd), p.w)
}

// hint explains the row under the cursor, in the one place that always has
// room for a sentence.
func (p *picker) hint() string {
	switch p.focus {
	case paneOptions:
		return p.opts[p.cursor[paneOptions]].help
	case paneAgents:
		return "the pool reviews are drawn from; none picked means auto-detect"
	default:
		switch p.rowAt(p.cursor[paneReviews]).kind {
		case rowSuggest:
			return "an agent reads the repo and proposes the reviews, before any run"
		case rowGroup:
			return "space takes the whole set, →/← open and close it"
		default:
			return "space takes this review on its own"
		}
	}
}

func (p *picker) renderKeys() string {
	keys := []struct{ k, v string }{
		{"tab", "pane"}, {"space", "toggle"}, {"←/→", "open, adjust"},
		{"a", "all/none"}, {"⏎", "run"}, {"q", "cancel"},
	}
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(styleValue.Render(k.k) + styleDim.Render(":"+k.v))
	}
	return clip(b.String(), p.w)
}

// renderNarrow is the fallback for a terminal too small for the panels: the
// choices still matter, so the command line is what survives.
func (p *picker) renderNarrow() string {
	return strings.Join([]string{
		wordmark() + styleDim.Render("  compose a run"),
		styleDim.Render("terminal too small for the launcher"),
		clip(styleValue.Render("gauntlet "+strings.Join(p.argv(), " ")), p.w),
		styleDim.Render("⏎ run  q cancel"),
	}, "\n")
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

func (p *picker) reviewPanel(w, h int) string {
	inner := w - 4 // the panel border and its padding
	rows := p.rows()
	from, to := p.window(paneReviews, len(rows), h)
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
			if p.suggest {
				name = styleFaint.Render(g.Name)
				count = styleFaint.Render(fmt.Sprintf("%d/%d", on, len(g.Reviews)))
			}
			lines = append(lines, pickLine(cur, inner,
				styleDim.Render(mark)+" "+name,
				count+" "+meter(float64(on)/float64(max(len(g.Reviews), 1)), 6, cTeal)))
		case rowReview:
			name := strings.TrimSuffix(r.review, "-review")
			text := "   " + checkbox(p.selected[r.review]) + " " + name
			if p.suggest {
				text = styleFaint.Render("   " + checkboxPlain(p.selected[r.review]) + " " + name)
			}
			lines = append(lines, pickLine(cur, inner, text, ""))
		}
	}
	title := fmt.Sprintf("REVIEWS  %d of %d", p.chosen(), len(p.known()))
	if p.suggest {
		title = "REVIEWS  " + styleInfo.Render("chosen by an agent at run time")
	}
	if hidden := len(rows) - (to - from); hidden > 0 {
		title += styleDim.Render(fmt.Sprintf("   +%d more", hidden))
	}
	return panel(title, strings.Join(lines, "\n"), inner, h)
}

func (p *picker) agentPanel(w, h int) string {
	inner := w - 4
	if len(p.cfg.Agents) == 0 {
		return panel("AGENTS", styleBad.Render("none installed")+
			styleDim.Render(" (see: gauntlet doctor)"), inner, h)
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
	return panel(title, strings.Join(lines, "\n"), inner, h)
}

func (p *picker) runPanel(w, h int) string {
	inner := w - 4
	lines := make([]string, 0, len(p.opts))
	for i, o := range p.opts {
		cur := p.focus == paneOptions && i == p.cursor[paneOptions]
		var left, right string
		switch o.kind {
		case optCount:
			left = "  " + o.label
			// Concurrency against the machine, drawn like every other meter:
			// the unlit remainder is the headroom left.
			frac := float64(o.n) / float64(max(p.cfg.CPUs, 1))
			right = meter(frac, 8, heatColor(frac)) + " " +
				styleValue.Render(fmt.Sprint(o.n)) + styleDim.Render(fmt.Sprintf("/%d cpu", p.cfg.CPUs))
		case optCycle:
			left = "  " + o.label
			value := o.values[o.idx]
			switch {
			case !p.suggest:
				left = styleFaint.Render("  " + o.label)
				right = styleFaint.Render(value)
			case o.idx == 0:
				right = styleDim.Render(value)
			default:
				right = styled(p.hues.get(value), value)
			}
		default:
			left = checkbox(o.on) + " " + o.label
		}
		lines = append(lines, pickLine(cur, inner, left, right))
	}
	return panel("RUN", strings.Join(lines, "\n"), inner, h)
}

func checkbox(on bool) string {
	if on {
		return styleOK.Render("[x]")
	}
	return styleDim.Render("[ ]")
}

// checkboxPlain is the same mark without color, for rows the current choices
// have made inert.
func checkboxPlain(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}

func slicesAny(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
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
