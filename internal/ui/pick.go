// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The launcher: the screen `gauntlet pick` opens so a run can be composed
// without knowing the flags first. It decides nothing on its own. What it
// produces is an argv, which the caller runs through the same parser every
// other invocation goes through, and which it shows on screen the whole time
// so the flags are learned rather than hidden.

// PickConfig is what the launcher needs to know about this machine: what can
// be reviewed, what can review it, and how much of it can run at once.
type PickConfig struct {
	Dir    string      // the directory the composed run will review
	Groups []PickGroup // review sets, in display order
	Agents []string    // installed agent labels, empty when none were found
	CPUs   int         // shown next to the concurrency choice
}

// PickGroup is one collapsible category: a review set and its members that
// exist in this prompt directory.
type PickGroup struct {
	Name    string
	Reviews []string
}

// Pick runs the launcher. It returns the argv of the composed run, or ok=false
// when the user left without launching anything.
func Pick(cfg PickConfig) (argv []string, ok bool, err error) {
	p := newPicker(cfg)
	prog := tea.NewProgram(p, tea.WithAltScreen())
	out, err := prog.Run()
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

// optKind is how one row of the options pane behaves: a toggle, or a number
// with a floor of one.
type optKind int

const (
	optToggle optKind = iota
	optCount
)

type option struct {
	kind  optKind
	label string
	help  string
	flag  string // what it contributes to the argv
	on    bool
	n     int
}

// row is one line of the review tree: a group header, or a review under it.
type row struct {
	group  int
	review string // "" for the header
}

type picker struct {
	cfg    PickConfig
	w, h   int
	ready  bool
	launch bool

	focus pane

	open     []bool          // per group
	selected map[string]bool // review name -> chosen
	agents   []bool          // per installed agent
	opts     []option

	cursor [paneCount]int
	scroll [paneCount]int
}

func newPicker(cfg PickConfig) *picker {
	p := &picker{
		cfg:      cfg,
		open:     make([]bool, len(cfg.Groups)),
		selected: map[string]bool{},
		agents:   make([]bool, len(cfg.Agents)),
		opts: []option{
			{kind: optCount, label: "jobs", n: 1,
				help: "reviews at a time, each in its own worktree"},
			{kind: optToggle, label: "once", flag: "--once", on: true,
				help: "one loop, then stop"},
			{kind: optToggle, label: "dashboard", flag: "--tui", on: true,
				help: "live screen instead of scrolling output"},
			{kind: optToggle, label: "suggest", flag: "--suggest",
				help: "an agent picks the reviews, and says why"},
			{kind: optToggle, label: "commit", flag: "--commit",
				help: "commit after each review"},
			{kind: optToggle, label: "yolo", flag: "--yolo",
				help: "drop the caution rules: bigger changes"},
		},
	}
	return p
}

func (p *picker) Init() tea.Cmd { return nil }

// rows flattens the review tree as it currently stands: every group header,
// and the members of the groups that are open.
func (p *picker) rows() []row {
	out := make([]row, 0, len(p.cfg.Groups)*4)
	for i, g := range p.cfg.Groups {
		out = append(out, row{group: i})
		if p.open[i] {
			for _, r := range g.Reviews {
				out = append(out, row{group: i, review: r})
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
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return p, tea.Quit
	case "enter":
		p.launch = true
		return p, tea.Quit
	case "tab", "right", "l":
		if msg.String() != "tab" && p.focus == paneReviews {
			p.expand(true)
			return p, nil
		}
		if msg.String() != "tab" && p.focus == paneOptions {
			p.adjust(+1)
			return p, nil
		}
		p.focus = (p.focus + 1) % paneCount
	case "shift+tab", "left", "h":
		if msg.String() != "shift+tab" && p.focus == paneReviews {
			p.expand(false)
			return p, nil
		}
		if msg.String() != "shift+tab" && p.focus == paneOptions {
			p.adjust(-1)
			return p, nil
		}
		p.focus = (p.focus + paneCount - 1) % paneCount
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

// paneLen is how many rows the focused pane has, for cursor bounds.
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

// expand opens or closes the group the cursor is in. Closing from a member
// moves the cursor up to its header, so the selection never lands off-screen.
func (p *picker) expand(open bool) {
	rows := p.rows()
	if len(rows) == 0 {
		return
	}
	r := rows[min(p.cursor[paneReviews], len(rows)-1)]
	p.open[r.group] = open
	if !open {
		for i, cand := range p.rows() {
			if cand.group == r.group && cand.review == "" {
				p.cursor[paneReviews] = i
				break
			}
		}
	}
}

func (p *picker) adjust(d int) {
	o := &p.opts[p.cursor[paneOptions]]
	if o.kind == optCount {
		o.n = max(1, o.n+d)
		return
	}
	o.on = d > 0
}

func (p *picker) toggle() {
	switch p.focus {
	case paneReviews:
		rows := p.rows()
		if len(rows) == 0 {
			return
		}
		r := rows[min(p.cursor[paneReviews], len(rows)-1)]
		if r.review != "" {
			p.selected[r.review] = !p.selected[r.review]
			return
		}
		// A header toggles its whole group, off when it is already whole.
		g := p.cfg.Groups[r.group]
		want := p.groupOn(r.group) < len(g.Reviews)
		for _, name := range g.Reviews {
			p.selected[name] = want
		}
		p.open[r.group] = true
	case paneAgents:
		if len(p.agents) > 0 {
			i := p.cursor[paneAgents]
			p.agents[i] = !p.agents[i]
		}
	case paneOptions:
		o := &p.opts[p.cursor[paneOptions]]
		if o.kind == optToggle {
			o.on = !o.on
		}
	}
}

// toggleAll clears the focused pane, or fills it when it is already empty.
func (p *picker) toggleAll() {
	switch p.focus {
	case paneReviews:
		want := len(p.selected) == 0 || p.chosen() == 0
		for _, g := range p.cfg.Groups {
			for _, name := range g.Reviews {
				p.selected[name] = want
			}
		}
	case paneAgents:
		want := true
		for _, on := range p.agents {
			want = want && !on
		}
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
	seen := map[string]bool{}
	for _, g := range p.cfg.Groups {
		for _, name := range g.Reviews {
			if p.selected[name] {
				seen[name] = true
			}
		}
	}
	return len(seen)
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

// argv composes the run. Nothing that matches a default is passed: the point
// is a command a person would have typed, not an exhaustive one.
func (p *picker) argv() []string {
	var out []string
	if p.cfg.Dir != "" {
		out = append(out, "-C", p.cfg.Dir)
	}
	if r := p.reviewArgs(); r != "" {
		out = append(out, "-r", r)
	}
	var agents []string
	for i, on := range p.agents {
		if on {
			agents = append(agents, p.cfg.Agents[i])
		}
	}
	if len(agents) > 0 && len(agents) < len(p.cfg.Agents) {
		out = append(out, "-a", strings.Join(agents, ","))
	}
	for _, o := range p.opts {
		switch {
		case o.kind == optCount && o.n > 1:
			out = append(out, "-j", fmt.Sprint(o.n))
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

func (p *picker) View() string {
	if !p.ready {
		return ""
	}
	leftW := max(30, p.w*3/5)
	rightW := max(24, p.w-leftW-1)
	// Chrome: title, the two box borders, the command line, and the help row.
	// The tree gets what is left, but never more rows than it has.
	bodyH := min(len(p.rows()), max(4, p.h-9))

	left := p.reviewPane(leftW, bodyH)
	right := lipgloss.JoinVertical(lipgloss.Left,
		p.agentPane(rightW, max(3, len(p.cfg.Agents))),
		p.optionPane(rightW),
	)
	title := styleTitle.Render("GAUNTLET") + "  " +
		styleDim.Render("compose a run in "+p.cfg.Dir)

	cmd := "gauntlet " + strings.Join(p.argv(), " ")
	command := styleDim.Render("$ ") + styleValue.Render(cmd)

	help := styleDim.Render("tab pane   space toggle   ←/→ open, adjust   " +
		"a all/none   ⏎ run   q cancel")

	return lipgloss.JoinVertical(lipgloss.Left,
		title, "",
		lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right),
		"", command, help,
	)
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

func (p *picker) reviewPane(w, h int) string {
	rows := p.rows()
	from, to := p.window(paneReviews, len(rows), h)
	lines := make([]string, 0, h)
	for i := from; i < to; i++ {
		r := rows[i]
		cur := p.focus == paneReviews && i == p.cursor[paneReviews]
		if r.review == "" {
			g := p.cfg.Groups[r.group]
			mark := "▸"
			if p.open[r.group] {
				mark = "▾"
			}
			count := fmt.Sprintf("%d/%d", p.groupOn(r.group), len(g.Reviews))
			lines = append(lines, pickLine(cur, w-2,
				mark+" "+styleValue.Render(g.Name), styleDim.Render(count)))
			continue
		}
		box := "[ ]"
		if p.selected[r.review] {
			box = styleOK.Render("[x]")
		}
		name := strings.TrimSuffix(r.review, "-review")
		lines = append(lines, pickLine(cur, w-2, "   "+box+" "+name, ""))
	}
	head := fmt.Sprintf("REVIEWS  %d of %d", p.chosen(), len(p.known()))
	if hidden := len(rows) - (to - from); hidden > 0 {
		head += fmt.Sprintf("  (%d more, scroll with j/k)", hidden)
	}
	return pickBox(head, w, h, lines, p.focus == paneReviews)
}

func (p *picker) agentPane(w, h int) string {
	if len(p.cfg.Agents) == 0 {
		return pickBox("AGENTS", w, 1,
			[]string{styleBad.Render("none installed (see: gauntlet doctor)")},
			p.focus == paneAgents)
	}
	from, to := p.window(paneAgents, len(p.cfg.Agents), h)
	lines := make([]string, 0, h)
	for i := from; i < to; i++ {
		box := "[ ]"
		if p.agents[i] {
			box = styleOK.Render("[x]")
		}
		cur := p.focus == paneAgents && i == p.cursor[paneAgents]
		lines = append(lines, pickLine(cur, w-2, box+" "+p.cfg.Agents[i], ""))
	}
	head := "AGENTS"
	if !slicesAny(p.agents) {
		head += "  (none picked: auto-detect)"
	}
	return pickBox(head, w, h, lines, p.focus == paneAgents)
}

func (p *picker) optionPane(w int) string {
	lines := make([]string, 0, len(p.opts))
	for i, o := range p.opts {
		cur := p.focus == paneOptions && i == p.cursor[paneOptions]
		var left, right string
		if o.kind == optCount {
			left = "  " + o.label
			right = styleValue.Render(fmt.Sprint(o.n)) + styleDim.Render(
				fmt.Sprintf(" of %d cpus", p.cfg.CPUs))
		} else {
			box := "[ ]"
			if o.on {
				box = styleOK.Render("[x]")
			}
			left = box + " " + o.label
		}
		if cur {
			right = styleDim.Render(o.help)
		}
		lines = append(lines, pickLine(cur, w-2, left, right))
	}
	return pickBox("RUN", w, len(p.opts), lines, p.focus == paneOptions)
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
	gap := max(w-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	body := left + strings.Repeat(" ", gap) + right
	if cursor {
		return styleInfo.Render("❯ ") + body
	}
	return "  " + body
}

func pickBox(head string, w, h int, lines []string, focused bool) string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Width(w - 2)
	if focused {
		border = border.BorderForeground(cLavender)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		styleDim.Render(head),
		border.Render(strings.Join(lines, "\n")),
	)
}
