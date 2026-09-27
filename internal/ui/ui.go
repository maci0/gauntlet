// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"math"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runner"
)

// SetMonochrome strips color from every style this package renders.
//
// lipgloss honors NO_COLOR and TERM=dumb on its own, but a command-line flag
// is invisible to that detection, so the command hands --no-color over here
// before either the launcher or the dashboard draws. Without it the flag
// would silence the plain reporter yet leave the TUI fully colored.
func SetMonochrome() {
	lipgloss.SetColorProfile(termenv.Ascii)
}

// Config describes the run the dashboard is watching.
type Config struct {
	Version    string
	RunID      string
	Dirs       []string
	Agents     []string
	Reviews    []string
	Jobs       int
	StackedPRs bool
	Timeout    time.Duration
	Budget     time.Duration
	Started    time.Time

	// OnFinish is the graceful quit: stop starting reviews, let the ones
	// running land their work, then end the run. Nil disables the key.
	OnFinish func()
}

// tickEvery drives the redraw. Ten frames a second is enough to feel alive
// and cheap enough that the dashboard never competes with the agents for CPU.
const tickEvery = 100 * time.Millisecond

// feedMax bounds the retained output feed. Memory stays proportional to what
// can be drawn and scrolled, not to what an agent printed.
const feedMax = 2000

// activitySamples is the width of the activity history ring, and
// laneSamples the width of the per-lane one drawn in a lane's own row.
const (
	activitySamples = 600
	laneSamples     = 120
)

// activityRateFull is the lines/s that reads as the top of the heat ramp on
// the activity marker, so the color means the same thing every frame.
const activityRateFull = 50

// statusPending and statusRunning are the dashboard's own states: a review
// the runner has not started, and one currently in flight. Everything past
// them is the runner's vocabulary (runner.Status), carried through typed.
const (
	statusPending runner.Status = "pending"
	statusRunning runner.Status = "running"
)

type reviewState struct {
	name     string
	status   runner.Status
	agentLbl string
	start    time.Time
	elapsed  time.Duration
	tokens   int
	ins, del int
}

type laneState struct {
	label   string
	review  string
	start   time.Time
	done    int
	failed  int
	tokens  int       // finished reviews only
	lines   []float64 // output lines per second, newest last
	pending float64

	// liveTokens is what the running review has reported so far, and
	// tokenRate is the measured throughput. Both are zero for agents that
	// only report usage when they exit: no rate is shown rather than a
	// made-up one.
	liveTokens   int
	liveThinking int
	thinkTokens  int // finished reviews' reasoning share
	lastTokens   int
	lastAt       time.Time
	lastThinkAt  time.Time // when the reasoning share last grew
	tokenRate    float64
	attempt      int // 1, or which retry of this review is running
}

// feedFilter is how much of the feed is worth the screen right now. Four
// agents narrating at once bury the two lines that matter, so the feed can be
// narrowed without pausing it or losing what it already collected.
type feedFilter int

// feedFilters is the number of states the f key cycles over, not a state of
// its own: the feed toggles between everything and the signal in it.
const (
	feedAll     feedFilter = iota // everything the agents said
	feedSignal                    // errors, results, and diffs
	feedFilters                   // count, for cycling
)

// keep reports whether a line survives the current filter.
func (f feedFilter) keep(l feedLine) bool {
	if f == feedAll {
		return true
	}
	switch l.kind {
	case normalize.Error, normalize.Result,
		normalize.DiffAdd, normalize.DiffDel, normalize.DiffMeta:
		return true
	}
	return false
}

func (f feedFilter) label() string {
	if f == feedSignal {
		return "results and errors"
	}
	return ""
}

type feedLine struct {
	text   string
	kind   normalize.Kind
	agent  string
	review string
	repeat int
}

type model struct {
	cfg   Config
	hues  *hueMap
	w, h  int
	ready bool

	order   []string
	reviews map[string]*reviewState
	lanes   map[string]*laneState
	laneOrd []string

	// Everything below is a view of the state above, rebuilt only when that
	// state changes: the frame runs ten times a second, and recomputing
	// constants each frame would buy nothing but jitter.
	sorted     []string       // order, sorted; stale while orderDirty
	orderDirty bool           // order grew and sorted must be rebuilt
	cellWidths map[string]int // review name -> terminal cells, names never change
	feedView   []feedLine     // feed through the current filter, while feedDirty is false
	feedDirty  bool           // the feed or the filter changed; feedView must be rebuilt

	feed       []feedLine
	scroll     int
	filter     feedFilter
	paused     bool
	finishing  bool // a graceful quit was asked for and is draining
	help       bool
	helpScroll int
	done       bool
	reloading  bool
	quitArmed  bool // q/esc was pressed once; a second press stops the run

	loop        int
	counts      map[string]int
	tokens      int
	thinking    int
	agentTime   time.Duration
	ins, del    int
	haveLines   bool
	conflicts   []string
	activity    []float64
	liveRate    float64 // measured tok/s across the lanes reporting usage
	pendingRate float64
	lastSample  time.Time
	now         time.Time
}

// program is the part of a tea.Program the dashboard drives.
type program interface {
	Send(tea.Msg)
	Run() (tea.Model, error)
	Quit()
}

// Dashboard owns the terminal for the duration of a run.
type Dashboard struct {
	prog   program
	events <-chan runner.Event

	// done carries a request to deliver the end-of-run marker. It is a channel
	// rather than a method body because the program takes messages on an
	// unbuffered channel, and two senders into one are unordered: a Finish sent
	// from a second goroutine could overtake the run's last events.
	//
	// forwarded closes when the delivery goroutine returns, which is how a
	// caller that arrives late (or after the bus is closed) learns not to wait
	// for an acknowledgement that will never come.
	done      chan chan struct{}
	forwarded chan struct{}
}

// newModel builds the dashboard state for one run.
func newModel(cfg Config) *model {
	m := &model{
		cfg: cfg, hues: newHueMap(),
		reviews: map[string]*reviewState{},
		lanes:   map[string]*laneState{},
		counts:  map[string]int{},
		now:     time.Now(), lastSample: time.Now(),
	}
	if cfg.Started.IsZero() {
		m.cfg.Started = time.Now()
	}
	for _, r := range cfg.Reviews {
		if _, dup := m.reviews[r]; dup {
			continue // repeats are weight, not extra rows
		}
		m.reviews[r] = &reviewState{name: r, status: statusPending}
		m.order = append(m.order, r)
	}
	// Pre-seed the lanes so the panel shows its structure before the first
	// review starts. The keys must match laneKey, or the first event opens a
	// second row for the same agent.
	for _, a := range cfg.Agents {
		keys := []string{a}
		if len(cfg.Dirs) > 1 {
			keys = keys[:0]
			for _, d := range cfg.Dirs {
				keys = append(keys, a+" @"+filepath.Base(d))
			}
		}
		for _, k := range keys {
			m.lane(k)
		}
	}
	m.orderDirty = len(m.order) > 0 // pre-seeded rows are not in sorted yet
	return m
}

// New builds a dashboard fed by one subscription to the run's event bus.
func New(cfg Config, events <-chan runner.Event) *Dashboard {
	return newDashboard(cfg, events,
		tea.NewProgram(newModel(cfg), tea.WithAltScreen()))
}

// newDashboard is New with the program supplied, so the message ordering
// between the bus and the end-of-run marker is testable without a terminal.
func newDashboard(cfg Config, events <-chan runner.Event, prog program) *Dashboard {
	d := &Dashboard{
		prog:      prog,
		events:    events,
		done:      make(chan chan struct{}),
		forwarded: make(chan struct{}),
	}
	// Started here, not in Run: Finish can be called from another goroutine
	// before Run is entered, and a forwarder that did not exist yet would let
	// the end-of-run marker land ahead of everything the bus had already
	// queued. Send parks on the program's unbuffered channel and returns
	// immediately once the program has shut down, so an early or late send is
	// safe either way.
	go d.forward()
	return d
}

// forward hands the program's every message. Run events and the end-of-run
// marker share this one goroutine, so the marker follows the events the bus
// produced before it.
func (d *Dashboard) forward() {
	defer close(d.forwarded)
	for {
		select {
		case ev, ok := <-d.events:
			if !ok {
				return
			}
			d.prog.Send(eventMsg(ev))
		case ack := <-d.done:
			d.drain()
			d.prog.Send(doneMsg{})
			close(ack)
		}
	}
}

// drain moves everything already queued on the event channel into the program
// before a non-event message is delivered. Applied out of order, done stops the
// tick that samples the lanes, so the summary screen freezes one sample short
// and the last interval of agent output never reaches the meters.
func (d *Dashboard) drain() {
	for {
		select {
		case ev, ok := <-d.events:
			if !ok {
				return
			}
			d.prog.Send(eventMsg(ev))
		default:
			return
		}
	}
}

// Run displays the dashboard until the user quits or the run finishes.
func (d *Dashboard) Run() error {
	_, err := d.prog.Run()
	return err
}

// Finish tells the dashboard the run is over. The screen stays up so the final
// state can be read; q closes it. It returns once the marker is in front of the
// program, so the caller may print its summary knowing every event is applied.
func (d *Dashboard) Finish() {
	ack := make(chan struct{})
	select {
	case d.done <- ack:
	case <-d.forwarded:
		return
	}
	select {
	case <-ack:
	case <-d.forwarded:
	}
}

// Quit closes the dashboard without waiting for a keypress. A hot reload uses
// it: the successor needs the terminal, and nobody is there to press q.
func (d *Dashboard) Quit() { d.prog.Quit() }

type eventMsg runner.Event
type tickMsg time.Time
type doneMsg struct{}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd { return tick() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h, m.ready = msg.Width, msg.Height, true
		return m, nil

	case tickMsg:
		if m.done {
			return m, nil
		}
		m.now = time.Time(msg)
		m.sampleActivity()
		return m, tick()

	case doneMsg:
		if !m.done {
			m.now = time.Now()
			m.sampleActivity()
			m.done = true
		}
		return m, nil

	case eventMsg:
		m.apply(runner.Event(msg))
		return m, nil

	case tea.KeyMsg:
		if m.help {
			// The overlay owns the screen: a keypress acting on the hidden
			// dashboard changes state nobody can see, so it answers only its
			// closing keys. q here closes the overlay; it does not stop the run.
			switch msg.String() {
			case "q", "ctrl+c", "esc", "?", "h":
				m.help = false
			default:
				m.helpScroll = scrollHelp(m.helpScroll, msg.String(), m.helpLines(), m.w, m.h)
			}
			return m, nil
		}
		key := msg.String()
		switch key {
		case "q":
			// q while the run is live is a hard stop. One press arms it so an
			// accidental tap does not kill reviews; a second press, or q
			// after the run has finished or is already draining, closes.
			if m.done || m.finishing || m.quitArmed {
				return m, tea.Quit
			}
			m.quitArmed = true
			return m, nil
		case "esc":
			if m.quitArmed {
				m.quitArmed = false
				return m, nil
			}
			if m.paused {
				m.paused = false
				m.scroll = 0
				return m, nil
			}
			if m.scroll > 0 {
				m.scroll = 0
				return m, nil
			}
			if m.done || m.finishing {
				return m, tea.Quit
			}
			return m, nil
		case "enter":
			if m.done {
				return m, tea.Quit
			}
			return m, nil
		default:
			if m.quitArmed {
				m.quitArmed = false
			}
		}
		switch key {
		case "ctrl+c":
			if m.done {
				return m, tea.Quit
			}
			// Staged like the terminal's Ctrl-C: the first asks for the
			// graceful quit the `s` key makes, the second closes the
			// dashboard, which stops the run.
			if m.cfg.OnFinish != nil && !m.finishing {
				m.finishing = true
				m.cfg.OnFinish()
				return m, nil
			}
			return m, tea.Quit
		case " ":
			if !m.done {
				m.paused = !m.paused
				if !m.paused {
					m.scroll = 0
				}
			}
		case "j", "down":
			m.scroll = max(0, m.scroll-1)
		case "k", "up":
			m.scroll = min(m.scroll+1, max(0, len(m.visibleFeed())-1))
		case "pgdown", "pagedown":
			_, _, _, feedH := m.sectionHeights()
			m.scroll = max(0, m.scroll-max(feedH-1, 1))
		case "pgup", "pageup":
			_, _, _, feedH := m.sectionHeights()
			m.scroll = min(m.scroll+max(feedH-1, 1), max(0, len(m.visibleFeed())-1))
		case "g", "home":
			m.scroll = max(0, len(m.visibleFeed())-1)
		case "G", "end":
			m.scroll = 0
		case "f":
			m.filter = (m.filter + 1) % feedFilters
			m.feedDirty = true // the line count changed under the scrollback
			m.scroll = 0
		case "s":
			// Asking twice changes nothing, so the screen says it once and
			// keeps saying it in the header until the run ends.
			if m.cfg.OnFinish != nil && !m.finishing && !m.done {
				m.finishing = true
				m.cfg.OnFinish()
			}
		case "?", "h":
			m.help = !m.help
			m.helpScroll = 0
		}
	}
	return m, nil
}

// sampleActivity folds the lines seen since the last tick into the history
// ring, as a per-second rate.
func (m *model) sampleActivity() {
	elapsed := m.now.Sub(m.lastSample).Seconds()
	if elapsed < 0 {
		m.lastSample = m.now
		return
	}
	if elapsed < 0.2 {
		return
	}
	m.lastSample = m.now
	m.activity = appendRing(m.activity, m.pendingRate/elapsed, activitySamples)
	m.pendingRate = 0
	for _, l := range m.lanes {
		l.lines = appendRing(l.lines, l.pending/elapsed, laneSamples)
		l.pending = 0
	}
}

func appendRing(vals []float64, v float64, maxLen int) []float64 {
	vals = append(vals, v)
	if len(vals) > maxLen {
		vals = vals[len(vals)-maxLen:]
	}
	return vals
}

func (m *model) apply(ev runner.Event) {
	switch ev.Kind {
	case runner.EvLoopStart:
		m.loop = ev.Loop
		// A new loop resets the grid: pending again, results kept in counters.
		for _, r := range m.reviews {
			if r.status != statusRunning {
				r.status = statusPending
			}
		}
	case runner.EvLoopEnd:
		m.loop = ev.Loop
		if ev.Ins != nil && ev.Del != nil {
			m.ins += *ev.Ins
			m.del += *ev.Del
			m.haveLines = true
		}
	case runner.EvReviewStart:
		r := m.review(ev.Review)
		r.status, r.agentLbl, r.start = statusRunning, ev.Agent, ev.Time
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.review, l.start, l.attempt = ev.Review, ev.Time, ev.Attempt
			l.liveTokens, l.liveThinking, l.lastTokens, l.tokenRate = 0, 0, 0, 0
			l.lastAt, l.lastThinkAt = ev.Time, time.Time{}
			m.liveRate = m.aggregateRate()
		}

	case runner.EvUsage:
		if l := m.lane(m.laneKey(ev)); l != nil {
			if ev.Thinking > l.liveThinking {
				l.liveThinking = ev.Thinking
				l.lastThinkAt = ev.Time
			}
			l.liveTokens = ev.Tokens
			l.sampleRate(ev.Tokens, ev.Time)
			m.liveRate = m.aggregateRate()
		}
	case runner.EvReviewEnd:
		r := m.review(ev.Review)
		r.status = ev.Status
		r.agentLbl = ev.Agent
		if ev.Elapsed > 0 && !math.IsNaN(ev.Elapsed) && !math.IsInf(ev.Elapsed, 0) &&
			ev.Elapsed <= float64(math.MaxInt64/int64(time.Second)) {
			r.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
		}
		r.tokens = ev.Tokens
		if ev.Ins != nil && ev.Del != nil {
			r.ins, r.del = *ev.Ins, *ev.Del
		}
		m.counts[string(ev.Status)]++
		m.tokens += ev.Tokens
		m.thinking += ev.Thinking
		m.agentTime += r.elapsed
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.review = ""
			l.done++
			l.tokens += ev.Tokens
			l.thinkTokens += ev.Thinking
			l.liveTokens, l.liveThinking, l.tokenRate = 0, 0, 0
			if ev.Status.Failed() {
				l.failed++
			}
			m.liveRate = m.aggregateRate()
		}
	case runner.EvMerge, runner.EvPullRequest:
		if ev.Kind == runner.EvMerge && ev.Status == runner.StatusConflict {
			entry := ev.Review + " (" + ev.Branch + ")"
			if !slices.Contains(m.conflicts, entry) {
				m.conflicts = append(m.conflicts, entry)
			}
		}
		if ev.Review != "" && ev.Ins != nil && ev.Del != nil {
			r := m.review(ev.Review)
			r.ins, r.del = *ev.Ins, *ev.Del
		}
	case runner.EvReload:
		m.reloading = true
		m.pushFeed(feedLine{text: ev.Text, kind: normalize.Result})
	case runner.EvLog:
		m.pushFeed(feedLine{text: ev.Text, kind: logKind(ev.Text), review: "runner"})
	case runner.EvOutput:
		m.pendingRate++
		if l := m.lane(m.laneKey(ev)); l != nil {
			l.pending++
		}
		m.pushFeed(feedLine{
			text: ev.Text, kind: ev.LineKind, agent: ev.Agent,
			review: ev.Review, repeat: ev.Repeat,
		})
	}
}

// rateWindow is the shortest gap between two usage reports that can produce a
// rate. Shorter than that is the same burst of tokens, not a new one.
const rateWindow = 500 * time.Millisecond

// sampleRate folds one usage report into the lane's measured throughput. The
// first report, and any report whose clock runs backwards, only moves the
// baseline: there is nothing to measure a rate against.
func (l *laneState) sampleRate(tokens int, at time.Time) {
	if l.lastAt.IsZero() || at.Before(l.lastAt) {
		l.lastTokens, l.lastAt = tokens, at
		return
	}
	dt := at.Sub(l.lastAt).Seconds()
	if dt < rateWindow.Seconds() {
		return
	}
	if d := tokens - l.lastTokens; d > 0 {
		// Smooth just enough that the number is readable without hiding a
		// real change.
		rate := float64(d) / dt
		if l.tokenRate == 0 {
			l.tokenRate = rate
		} else {
			l.tokenRate = 0.6*l.tokenRate + 0.4*rate
		}
	}
	l.lastTokens, l.lastAt = tokens, at
}

// aggregateRate sums the measured throughput of every lane that reports it.
func (m *model) aggregateRate() float64 {
	total := 0.0
	for _, key := range m.laneOrd {
		total += m.lanes[key].tokenRate
	}
	return total
}

func (m *model) review(name string) *reviewState {
	if r, ok := m.reviews[name]; ok {
		return r
	}
	r := &reviewState{name: name, status: statusPending}
	m.reviews[name] = r
	m.order = append(m.order, name)
	m.orderDirty = true
	return r
}

// laneKey identifies the row an event belongs to. One agent working two
// directories is two lanes: sharing a row would make each overwrite the
// other's current review.
func (m *model) laneKey(ev runner.Event) string {
	if len(m.cfg.Dirs) < 2 || ev.Dir == "" {
		return ev.Agent
	}
	return ev.Agent + " @" + filepath.Base(ev.Dir)
}

func (m *model) lane(label string) *laneState {
	if label == "" {
		return nil
	}
	if l, ok := m.lanes[label]; ok {
		return l
	}
	l := &laneState{label: label}
	m.lanes[label] = l
	m.laneOrd = append(m.laneOrd, label)
	m.hues.get(label)
	return l
}

func (m *model) pushFeed(l feedLine) {
	// The feed mixes pre-normalized agent output with log lines that carry
	// fragments of a possibly hostile repository (git stderr, merge output,
	// prompt names). Every line is sanitized here, once, so nothing reaches
	// the terminal able to drive it; visible text is untouched.
	l.text = normalize.Sanitize(l.text)
	if len(m.feed) >= feedMax {
		copy(m.feed, m.feed[1:])
		m.feed[len(m.feed)-1] = l
	} else {
		m.feed = append(m.feed, l)
	}
	m.feedDirty = true
	// A paused or scrolled-back reader holds their place: the viewport stays
	// anchored to the lines it shows while history grows underneath, and
	// nothing printed during a pause is discarded.
	if m.paused || m.scroll > 0 {
		if m.filter.keep(l) {
			m.scroll++
			maxBack := len(m.feed) - 1
			if m.filter != feedAll {
				maxBack = len(m.visibleFeed()) - 1
			}
			if m.scroll > maxBack {
				m.scroll = max(maxBack, 0)
			}
		}
	}
}
