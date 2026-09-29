// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Everything the launcher puts on screen: its view, its panels, and the
// helpers that size and fold them. The rows it offers and the argv it
// composes live in pick.go, the same split the dashboard makes between
// ui.go and view.go.

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// filterMissedMsg is what a filter that matched nothing says, in the two
// places that have room for a sentence: the block line and the hint.
const filterMissedMsg = "no reviews match this filter (esc clears it)"

// minPickerW and minPickerH are the smallest terminal the launcher holds: the
// tree beside two stacked panels, each of them a frame around one row at the
// tightest. View guards on them and the fallback names them, so the size the
// screen asks for and the size it accepts cannot drift apart.
const (
	minPickerW = 50
	minPickerH = 12
)

func (p *picker) View() string {
	if !p.ready {
		return "\n  gauntlet is warming up…"
	}
	if p.help {
		return p.renderHelp()
	}
	if p.w < minPickerW || p.h < minPickerH {
		return p.renderNarrow()
	}
	// The run pane is a label and a value per row, and a pane too narrow for
	// both cuts the option's name: "suggest ag" beside "from the pool" reads
	// as two different options. So the right column keeps a floor wide enough
	// for the longest label and a short value, and the tree takes the rest.
	// Below the width where both columns can hold that, the tree keeps the
	// room and the run pane scrolls, as it always has.
	capW := p.w - 28
	if p.w-36 >= 34 {
		capW = p.w - 36
	}
	leftW := clampi(p.w*3/5, min(34, capW), capW)
	rightW := p.w - leftW - 1
	// The two panels on the right divide the rows they have between them, and
	// the tree beside them takes what it needs.
	runH := p.paneHeight(paneOptions)
	agentH := p.paneHeight(paneAgents)
	// The tree takes what it needs and no more; the panels beside it are
	// sized by the terminal, not by how many groups happen to be open.
	reviewH := p.paneHeight(paneReviews)

	right := lipgloss.JoinVertical(lipgloss.Left,
		p.agentPanel(rightW, agentH),
		p.runPanel(rightW, runH),
	)
	return bottomAnchor(strings.Split(strings.Join([]string{
		p.renderHeader(),
		lipgloss.JoinHorizontal(lipgloss.Top, p.reviewPanel(leftW, reviewH), " ", right),
		p.renderCommand(),
		p.renderStatus(),
		p.renderKeys(),
	}, "\n"), "\n"), p.h)
}

// bottomAnchor drops the room a taller terminal leaves between the panels and
// the three lines under them. The panes are sized by what they hold, so a
// terminal with few groups open and a short agent list leaves a gap; the gap
// belongs above the command, the status line, and the keys, never inside the
// last row. That is where the dashboard puts its footer too, so the keys a
// reader reaches for sit on the bottom row at every terminal size.
func bottomAnchor(rows []string, h int) string {
	const floor = 3 // command, status, keys
	if gap := h - len(rows); gap > 0 && len(rows) > floor {
		rows = append(rows[:len(rows)-floor],
			append(make([]string, gap), rows[len(rows)-floor:]...)...)
	}
	return strings.Join(rows, "\n")
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
	return clipEllipsis(styleDim.Render("$ ")+styleValue.Render(cmd), p.w)
}

// blocked reports why the composed run would not start, or "" when it would.
func (p *picker) blocked() string {
	if why := p.blockReason(); why != "" {
		return why
	}
	if p.filterMissed(p.rows()) {
		return filterMissedMsg
	}
	return ""
}

// noAgentsMsg is the one answer to "nothing here can be launched". The block
// reason and the status hint both owe it, and one literal keeps them from
// drifting into two different sentences.
const noAgentsMsg = "no agent CLI is installed: install one (see: gauntlet doctor)"

// blockReason is the part of blocked that is not the reader's own filter: the
// machine cannot run the run, whatever the filter says. It is its own
// predicate so a status line can outrank the filter hint with it while the
// reader is still typing, without also warning about every keystroke of a
// search that has not matched yet.
func (p *picker) blockReason() string {
	// Worktree isolation cuts branches from a commit, so uncommitted edits to
	// tracked files would be invisible to every review: better to say so here
	// than to compose a command that fails on launch. Untracked files do not
	// block --jobs, so they do not block the launcher either. The same goes for
	// an empty agent pool: every run auto-detects its agents, so nothing here
	// can launch at all.
	if len(p.cfg.Agents) == 0 {
		return noAgentsMsg
	}
	if p.cfg.Dirty && p.concurrency().n > 1 && !p.stacked() {
		return "concurrency above 1 needs a clean tree: commit or stash first, or set it back to 1"
	}
	return ""
}

// hint explains the row under the cursor, in the one place that always has
// room for a sentence. A review row explains itself: descriptions are longer
// than any pane column, so the status line is where one is read whole.
func (p *picker) hint() string {
	if p.typing {
		if p.filterMissed(p.rows()) {
			return "filter: " + p.filter + "▏  (no reviews match)  ⏎ keep it, esc clear it"
		}
		return "filter: " + p.filter + "▏  ⏎ keep it, esc clear it"
	}
	if p.filterMissed(p.rows()) {
		return filterMissedMsg
	}
	switch p.focus {
	case paneOptions:
		o := p.opts[p.cursor[paneOptions]]
		if p.optionInert(&o) {
			return "stacked PRs own this; turn that off to change it"
		}
		if o.flag == "--suggest-agent" {
			switch {
			case !p.suggest:
				return "suggest is off; tick suggest in reviews to choose who suggests"
			case p.suggestAgent() == p.cfg.FastSuggest && p.cfg.FastSuggest != "":
				return "gauntlet reads the files for signals instead of asking a model"
			case p.suggestAgent() == "":
				return "sample an agent from the installed pool to propose reviews"
			default:
				return fmt.Sprintf("use %s to read the repo and propose reviews", p.suggestAgent())
			}
		}
		if o.flag == "--merge-into" && !p.committing() {
			return "commits are off; turn on commit or push to choose a merge target"
		}
		return o.help
	case paneAgents:
		if len(p.cfg.Agents) == 0 {
			return noAgentsMsg
		}
		return "the pool reviews are drawn from; none picked means auto-detect"
	default:
		switch r := p.rowAt(p.cursor[paneReviews]); r.kind {
		case rowSuggest:
			return "an agent reads the repo and proposes the reviews, before any run"
		case rowGroup:
			// A filter opens every set and keeps it open, so the arrows the
			// hint otherwise names have nothing left to close. Saying they
			// fold here is a promise the tree cannot keep: left on a header
			// only moves the cursor.
			if p.filter != "" {
				return "a filter holds every set open; space takes the whole set"
			}
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
// anything is, otherwise what the cursor is on. Every branch clips with the
// marker rather than a bare cut: the sentences that land here name the fix
// (a command to run, a key to press), and a line that stops mid-word without
// saying so turns "gauntlet doctor" into a command that does not exist.
func (p *picker) renderStatus() string {
	// An armed quit outranks both: until it is answered, the question the
	// reader has is whether the run is about to be thrown away, and this is
	// the only line that answers it. It names the key that asked, because
	// the press that confirms and the one that declines differ with it.
	if p.quitArmed {
		return clipEllipsis(styleWarn.Render(
			fmt.Sprintf("⚠ %s again to discard the run, %s to keep it", p.quitKey, otherQuitKey(p.quitKey))), p.w)
	}
	if p.typing {
		// A reason the run cannot start outranks the filter line: it is what
		// makes enter dead, and it does not stop being true because the reader
		// opened a search. Typing used to hide it entirely, so a box with no
		// agent CLI installed read as a working launcher for as long as the
		// search was open.
		if why := p.blockReason(); why != "" {
			return clipEllipsis(styleWarn.Render("⚠ "+why), p.w)
		}
		return clipEllipsis(styleDim.Render(p.hint()), p.w)
	}
	if why := p.blocked(); why != "" {
		return clipEllipsis(styleWarn.Render("⚠ "+why), p.w)
	}
	return clipEllipsis(styleDim.Render(p.hint()), p.w)
}

// otherQuitKey is the key that takes an armed quit back: the one of the two
// that did not ask.
func otherQuitKey(armed string) string {
	if armed == "esc" {
		return "q"
	}
	return "esc"
}

// keyHint is one key and what it does, as the key line shows it.
type keyHint struct{ k, v string }

// renderKeys names the keys, most important first. A key the focused pane
// does not act on is left off rather than advertised as one that does
// nothing. What does not fit is dropped from the right end, after the gap
// between segments has been tightened, because a keyboard user who cannot
// find how to move between rows or leave is stranded. What fits is what is
// shown, never clipped mid-name.
func (p *picker) renderKeys() string {
	arrowAction := "open/close"
	switch p.focus {
	case paneOptions:
		arrowAction = "change"
	case paneAgents:
		arrowAction = "pane"
	case paneReviews:
		// The tree is not one kind of row. A set header has a fold in both
		// directions; a review inside a set has only the left arrow, which
		// folds the set it is in; the suggest row has no arrows at all, and
		// both step to the neighbouring pane. Naming all of them
		// "open/close" advertised a fold on a row that cannot make one, and
		// a key that does nothing is the one a keyboard user cannot tell
		// from a key that is broken (WCAG 3.3.2).
		switch p.rowAt(p.cursor[paneReviews]).kind {
		case rowGroup:
		case rowReview:
			arrowAction = arrowFold
		default:
			arrowAction = "pane"
		}
		// A filter holds every set open, so no row of the tree has a fold
		// left to make and the arrows step panes there, the way the hint on
		// a set header already says.
		if p.filter != "" {
			arrowAction = "pane"
		}
	}
	q := "cancel"
	if p.quitArmed {
		q = "discard"
	}
	keys := []keyHint{
		{"⏎", "run"}, {"q", q}, {"j/k", "move"},
		{"?", "help"}, {"tab", "pane"}, {"space", "toggle"}, {"←/→", arrowAction},
		{"/", "filter"},
	}
	// Only the key that armed the ask offers to confirm it, so the legend
	// cannot name two that both discard the run.
	if p.quitArmed && p.quitKey != "q" {
		keys[1] = keyHint{p.quitKey, "discard"}
	}
	// Concurrency is the one key that reaches from any pane, so it earns its
	// place in the legend before the pane's own. It has no effect in stack
	// mode, or on a machine with one cpu, and is dropped then rather than
	// advertised as a key that does nothing.
	if p.concurrencyKeys() {
		keys = append(keys, keyHint{"+/-", "concurrency"})
	}
	if p.quitArmed {
		keys = append(keys, keyHint{otherQuitKey(p.quitKey), "keep"})
	}
	// a fills or empties what the focused pane is showing. The run pane shows
	// switches rather than a selection, so there is nothing there for it to
	// take: the key is left off rather than advertised as one that does
	// nothing. The dashboard's legend drops dead keys the same way.
	if p.focus != paneOptions {
		keys = append(keys, keyHint{"a", "all/none"})
	}
	switch {
	case p.typing:
		// q types into the filter rather than leaving, and enter keeps the
		// filter rather than launching. Advertising the pane keys here is
		// how a reader thinks they cancelled a run they only searched.
		keys = []keyHint{{"⏎", "keep"}, {"esc", "clear"}, {"↑↓", "move"}}
	case p.filter != "" && !p.quitArmed:
		// A live filter keeps the run keys and adds the one that clears it.
		keys = slices.Insert(slices.Clone(keys), 2, keyHint{"esc", "clear"})
	}
	// A pane that offers a to take carries ten segments, which two spaces
	// apart will not fit on a hundred columns. The gap tightens first, then
	// the arrow keys say "fold" rather than "open/close"; a terminal narrower
	// than that drops from the right end.
	for _, tight := range []bool{false, true} {
		for _, gap := range []string{"  ", " "} {
			if line, fits := joinKeys(keys, gap, arrowFold, p.w, tight); fits {
				return line
			}
		}
	}
	line, _ := joinKeys(keys, " ", arrowFold, p.w, true)
	return line
}

// arrowFold names the arrow keys when the legend is short on columns. fold
// covers both halves of open/close, which is what the keys do to a set.
const arrowFold = "fold"

// joinKeys renders the segments separated by gap, stopping before the one
// that would pass w. tight swaps the arrow keys' long action for arrowFold.
// It reports whether every segment fitted; the first is always written, since
// a lone segment that overflows is clipped rather than leaving the line blank.
func joinKeys(keys []keyHint, gap, fold string, w int, tight bool) (string, bool) {
	segs := make([]string, 0, len(keys))
	for _, k := range keys {
		action := k.v
		if tight && k.v == "open/close" {
			action = fold
		}
		segs = append(segs, styleValue.Render(k.k)+styleDim.Render(":"+action))
	}
	full := strings.Join(segs, gap)
	if lipgloss.Width(full) <= w {
		return full, true
	}
	for i, seg := range segs {
		if i > 0 && lipgloss.Width(strings.Join(segs[:i], gap))+len(gap)+lipgloss.Width(seg) > w {
			return strings.Join(segs[:i], gap), false
		}
	}
	return full, true
}

// paneTitle lays a launcher pane's title from its readings, the way the
// dashboard's panel titles are laid: whole readings drop from the right, and
// what did not fit is marked. panel clips a title to the frame, and a clip
// lands wherever the width runs out, so a title carrying "+3 more" could end
// at "AGENTS  none picked: auto-d" and take the count of hidden rows with it.
// inner is the pane's content width; the title is held to the row it is drawn
// on, which is that width plus the frame.
func paneTitle(segs []string, inner int) string {
	return fitTitle(segs, inner+panelBorderColumns)
}

// panelBorderColumns is what panel's frame costs a title row: the border and
// its padding on both sides. panel measures its titles against the same number,
// so the two cannot drift.
const panelBorderColumns = 4

// fitSegments lays already-formatted segments on one row, dropping whole ones
// from the right rather than cutting the last one: a key line ending in
// "/ fi" names a key that does not exist. It is the small-terminal fallback's
// version of joinKeys, which does the same over the styled legend.
func fitSegments(segs []string, gap string, w int) string {
	out, dropped := segs[0], false
	for _, s := range segs[1:] {
		if lipgloss.Width(out)+lipgloss.Width(gap)+lipgloss.Width(s) > w {
			dropped = true
			break
		}
		out += gap + s
	}
	// What did not fit is marked, the way fitRight and fitTitle mark theirs. A
	// key row that silently ends at "? help" reads as the whole set of keys, and
	// help is the one segment a narrow pane is likeliest to lose.
	if dropped && lipgloss.Width(out)+lipgloss.Width(gap)+1 <= w {
		out += gap + "…"
	}
	return out
}

// renderNarrow is the fallback for a terminal too small for the panels: the
// choices still matter, so the command line is what survives. Rows clip to
// the pane for the same reason the dashboard's fallback does: a wrapped
// fallback is a taller broken screen, not a smaller one.
func (p *picker) renderNarrow() string {
	command := styleValue.Render("gauntlet " + strings.Join(p.argv(), " "))
	rows := []string{
		wordmark() + styleDim.Render("  compose a run"),
		// A fallback that only says it is the fallback leaves the reader on a
		// screen with no way to choose anything. Resizing is the way out, and
		// it is immediate: the panes draw on the next frame, so the fallback
		// names the size that brings them back.
		styleDim.Render(fmt.Sprintf("launcher needs %d×%d; resize to pick reviews",
			minPickerW, minPickerH)),
		command,
	}
	// Enter is dead while the run is blocked, and the reason is the only
	// thing here that says so: the wide view carries it on the status line,
	// but this view has no status line to carry it. An armed q replaces it:
	// this view has no status line either, so the warning goes here.
	if p.quitArmed {
		rows = append(rows, styleWarn.Render(
			fmt.Sprintf("⚠ %s again to discard, %s to keep", p.quitKey, otherQuitKey(p.quitKey))))
	} else if why := p.blocked(); why != "" {
		rows = append(rows, styleWarn.Render("⚠ "+why))
	}
	// The filter key is named here, and not dropped with the panes: without the
	// panels the composed command is the whole screen, and / is the only key
	// that can still change it. It therefore sits ahead of help, the one
	// segment here a reader can afford to lose: the wide legend keeps it late
	// because the panes are there to make it unnecessary.
	keys := []string{"⏎ run", "/ filter", "q cancel", "? help"}
	if p.typing {
		rows = append(rows, styleInfo.Render("filter: "+p.filter+"▏"))
		// No arrow keys here: this view draws no pane, so the cursor they
		// move is one nothing on screen points at. A key named here that
		// cannot be seen to do anything is a key a keyboard user cannot tell
		// from a broken one (WCAG 3.3.2).
		keys = []string{"⏎ keep", "esc clear"}
	} else if p.filter != "" {
		rows = append(rows, styleInfo.Render("filter: /"+p.filter))
		keys = []string{"⏎ run", "/ filter", "esc clear", "q cancel", "? help"}
	}
	rows = append(rows, styleDim.Render(fitSegments(keys, "  ", p.w)))
	if p.h > 0 && len(rows) > p.h {
		// A terminal too short for the whole view keeps the command it
		// composes and the keys that act on it. The wordmark and the notice
		// are what go: without the keys there is no way out of this screen,
		// and without the command there is nothing to run. A one-row terminal
		// has room for one of them, and takes the keys, the way the
		// dashboard's fallback does: two rows on one line scroll, and what
		// scrolls away is whatever the terminal felt like keeping.
		keys := rows[len(rows)-1]
		if p.h == 1 {
			rows = []string{keys}
		} else {
			rows = append(rows[:max(p.h-2, 0):len(rows)-1],
				command, keys)
		}
	}
	for i, r := range rows {
		rows[i] = clipEllipsis(r, p.w)
	}
	return strings.Join(rows, "\n")
}

func (p *picker) renderHelp() string {
	return renderHelpPage(p.helpLines(), p.helpScroll, p.w, p.h)
}

// qLeave is how leaving is spelled: one press arms, a second throws the run
// away, and the way back is stated on the same line.
const qLeave = "  q, esc       leave without running (press the same key twice; the other one keeps it)"

// escLeave is what esc does at each depth, in the order the key meets them: it
// is the way back, so it has to be said where the way back is being looked for.
const escLeave = "  esc          cancel an armed quit, clear the filter, or ask to leave once there is nothing to clear"

func (p *picker) helpLines() []string {
	lines := []string{
		styleTitle.Render("compose a run"),
		styleDim.Render("q  esc  ?  ctrl+c  close this help"),
		"",
	}
	lines = append(lines, p.focusedLines()...)
	lines = append(lines, "")
	lines = append(lines, p.stateLines()...)
	lines = append(lines,
		"",
		"  tab / shift+tab reviews, agents, and run options",
		"  ↑ / ↓, j / k move within a pane",
		"  pgup / pgdn  move by page",
		"  home / end first / last row in this pane; g / G the same, not while filtering",
		"  space        toggle a review, a set, an agent, or a switch",
		"  ← / →, h / l open or close a set, change a value, or step to the next pane",
		"  a            all or none of what this pane is showing",
		"  /            filter reviews by set, name, or description; enter keeps it, esc clears",
		"  ctrl+u / ctrl+w   clear the filter, or drop the word before the cursor",
		"  enter        run the composed command",
		qLeave,
		escLeave,
		"",
		styleDim.Render("  Picking no reviews runs all of them."),
		styleDim.Render("  suggest: an agent proposes the reviews; anything ticked is also scheduled."),
		styleDim.Render("  stacked PRs: each changed review opens a PR on the previous one."),
	)
	// The key lines, like the legend, list what this screen can act on: a
	// stack owns the job count, and one cpu leaves nothing to raise it to.
	if p.concurrencyKeys() {
		lines = append(lines, "  + / -        raise or lower concurrency, up to the cpu count")
	}
	if why := p.blocked(); why != "" {
		lines = append(lines, "", styleWarn.Render("  "+why))
	}
	return lines
}

func (p *picker) focusedLines() []string {
	selection := func(on bool) string {
		if on {
			return "selected"
		}
		return "not selected"
	}
	var detail string
	switch p.focus {
	case paneReviews:
		r := p.rowAt(p.cursor[paneReviews])
		switch r.kind {
		case rowSuggest:
			detail = "suggest: " + selection(p.suggest)
		case rowGroup:
			state := "collapsed"
			if p.open[r.group] || p.filter != "" {
				state = "expanded"
			}
			g := p.cfg.Groups[r.group]
			detail = fmt.Sprintf("%s: %s; %d of %d selected", g.Name, state, p.groupOn(r.group), len(g.Reviews))
		case rowReview:
			detail = reviewLabel(r.review) + ": " + selection(p.selected[r.review.Name])
		}
	case paneAgents:
		if len(p.cfg.Agents) == 0 {
			detail = "no agents installed"
		} else {
			i := p.cursor[paneAgents]
			detail = p.cfg.Agents[i] + ": " + selection(p.agents[i])
		}
	case paneOptions:
		o := p.opts[p.cursor[paneOptions]]
		value := "off"
		switch o.kind {
		case optCount:
			value = fmt.Sprint(o.n)
		case optCycle:
			value = o.values[o.idx]
		case optToggle:
			if o.on {
				value = "on"
			}
		}
		detail = o.label + ": " + value
		if p.optionDisabled(&o) {
			detail += " (disabled)"
		}
	}
	return []string{"Focused control:", "  " + detail, "  " + p.hint()}
}

// stateLines spells the launcher's choices as full sentences, so a value the
// panes clip (a long review name or description, an agent label, the current
// option value) is still readable in text: the help overlay is the keyboard's
// fallback view when the panes cannot hold a string whole. Empty sections are
// left out rather than drawn as empty rows.
func (p *picker) stateLines() []string {
	var lines []string
	if p.suggest {
		who := "any agent"
		if a := p.suggestAgent(); a != "" {
			who = a
		}
		lines = append(lines, "  suggest is on; "+who+" proposes the reviews.")
	} else {
		lines = append(lines, "  suggest is off.")
	}
	for i, g := range p.cfg.Groups {
		if n := p.groupOn(i); n > 0 {
			lines = append(lines, fmt.Sprintf("  %s: %d of %d reviews selected",
				g.Name, n, len(g.Reviews)))
		}
	}
	if len(p.cfg.Agents) > 0 {
		if picked := p.pickedAgents(); len(picked) > 0 {
			lines = append(lines, "  agents: "+strings.Join(picked, ", "))
		} else {
			lines = append(lines, "  agents: auto-detect (all "+fmt.Sprint(len(p.cfg.Agents))+" installed)")
		}
	}
	for _, o := range p.opts {
		switch o.kind {
		case optCount:
			if !p.optionInert(&o) && o.n != 1 {
				lines = append(lines, fmt.Sprintf("  %s: %d of %d cpus",
					o.label, o.n, max(p.cfg.CPUs, 1)))
			}
		case optCycle:
			if o.idx != 0 && !p.optionInert(&o) {
				if o.flag == "--merge-into" && !p.committing() {
					break
				}
				if o.flag == "--suggest-agent" && !p.suggest {
					break
				}
				lines = append(lines, "  "+o.label+": "+o.values[o.idx])
			}
		default:
			if o.on && !p.optionInert(&o) {
				lines = append(lines, "  "+o.label+" is on")
			}
		}
	}
	return lines
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
				count+" "+meter(float64(on)/float64(max(len(g.Reviews), 1)), 6, cMark)))
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
	// The dashboard's feed carries a parallel message for the same reason.
	if p.filterMissed(rows) {
		lines = append(lines, styleFaint.Render(filterMissedMsg))
	}
	// The title is laid as segments, so a pane too narrow for them all drops
	// whole readings instead of being cut mid-word, and what it dropped is
	// marked. A cut title could end at "none picked: auto-d", and it could
	// lose the "+N more" that says the pane is holding rows back: a review or
	// an agent missing from the list then reads as one that does not exist.
	// The dashboard's panel titles are fitted this way already.
	segs := []string{"REVIEWS"}
	if hidden := len(rows) - (to - from); hidden > 0 {
		segs = append(segs, styleDim.Render(fmt.Sprintf("+%d more", hidden)))
	}
	switch n := p.chosen(); {
	case p.suggest && n > 0:
		segs = append(segs, styleInfo.Render(fmt.Sprintf("agent-picked, plus %d also scheduled", n)))
	case p.suggest:
		segs = append(segs, styleInfo.Render("chosen by an agent at run time"))
	case n == 0:
		segs = append(segs, fmt.Sprintf("all %d", len(p.knownReviews)))
	default:
		segs = append(segs, fmt.Sprintf("%d of %d", n, len(p.knownReviews)))
	}
	if p.filter != "" {
		segs = append(segs, styleInfo.Render("/"+p.filter))
	}
	return panel(paneTitle(segs, inner), strings.Join(lines, "\n"), inner, h)
}

func (p *picker) agentPanel(w, h int) string {
	inner := w - 4
	if len(p.cfg.Agents) == 0 {
		// The pane still takes focus when it has nothing to list, so it must
		// carry the cursor bar like any other pane: a screen with no ❯ leaves
		// the keyboard nowhere to be. The row says what is missing and stops
		// there: a panel is padded to its width, not wrapped, so a second
		// clause naming the fix is cut mid-word ("gauntlet doc") with nothing
		// to mark the cut. The full sentence is on the status line, and in
		// the help and the narrow fallback, all of which have room for it.
		body := styleBad.Render("none installed")
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
	segs := []string{"AGENTS"}
	if hidden := len(p.cfg.Agents) - (to - from); hidden > 0 {
		segs = append(segs, styleDim.Render(fmt.Sprintf("+%d more", hidden)))
	}
	checked := 0
	for _, on := range p.agents {
		if on {
			checked++
		}
	}
	switch {
	case checked == 0:
		segs = append(segs, styleDim.Render("none picked: auto-detect"))
	case checked == len(p.cfg.Agents):
		segs = append(segs, styleDim.Render("all picked"))
	default:
		segs = append(segs, styleDim.Render(fmt.Sprintf("%d of %d picked", checked, len(p.cfg.Agents))))
	}
	return panel(paneTitle(segs, inner), strings.Join(lines, "\n"), inner, h)
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
		note := p.inertNote(&o)
		switch o.kind {
		case optCount:
			left = "  " + o.label
			// Concurrency against the machine, drawn like every other meter:
			// the unlit remainder is the headroom left.
			frac := float64(o.n) / float64(max(p.cfg.CPUs, 1))
			value := styleValue.Render(fmt.Sprint(o.n)) + styleDim.Render(fmt.Sprintf("/%d cpu", p.cfg.CPUs))
			right = meter(frac, 8, heatColor(frac)) + " " + value
			if lipgloss.Width(left)+lipgloss.Width(right)+1 > inner-2 {
				right = value
			}
			if inert {
				left = styleFaint.Render("  " + o.label + note)
				right = styleFaint.Render(fmt.Sprint(o.n) + fmt.Sprintf("/%d cpu", p.cfg.CPUs))
			}
		case optCycle:
			// A cycle row that cannot apply yet is drawn inert: the suggest
			// agent without suggest, a merge target without commits.
			value := o.values[o.idx]
			applies := !p.optionDisabled(&o)
			chosen := styled(p.hues.get(value), value)
			if o.flag == "--merge-into" {
				chosen = styleValue.Render(value)
			}
			left = "  " + o.label
			switch {
			case !applies:
				left = styleFaint.Render("  " + o.label + note)
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
				left = styleFaint.Render(mark + o.label + note)
			}
		}
		if right != "" {
			// A row too narrow for label and value together keeps the one
			// that fits in half the row and cuts the other with the marker.
			// Cutting both in half left "suggest ag" beside "from the poo",
			// which reads as two options rather than as one row.
			bodyW := inner - 2
			lw, rw := lipgloss.Width(left), lipgloss.Width(right)
			if lw+rw+1 > bodyW {
				if lw <= bodyW/2 {
					right = clipEllipsis(right, max(bodyW-lw-1, 1))
				} else {
					right = clipEllipsis(right, max(bodyW/2, 1))
					left = clipEllipsis(left, max(bodyW-lipgloss.Width(right)-1, 1))
				}
			}
		}
		lines = append(lines, pickLine(cur, inner, left, right))
	}
	segs := []string{"RUN"}
	if hidden := len(p.opts) - (to - from); hidden > 0 {
		segs = append(segs, styleDim.Render(fmt.Sprintf("+%d more", hidden)))
	}
	return panel(paneTitle(segs, inner), strings.Join(lines, "\n"), inner, h)
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
