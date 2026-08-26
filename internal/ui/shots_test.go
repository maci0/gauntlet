package ui

import (
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/maci0/gauntlet/internal/runner"
)

// TestWriteShots writes the renderer's own ANSI output for the README
// screenshots, so the pictures in the README are what the program draws
// rather than a drawing of it. scripts/shots.sh turns them into PNGs; nothing
// runs here unless SHOT_DIR asks for it.
func TestWriteShots(t *testing.T) {
	dir := os.Getenv("SHOT_DIR")
	if dir == "" {
		t.Skip("not a shot run")
	}
	// The picture should carry the palette the theme actually specifies, not
	// the 16 colors a pipe negotiates down to.
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	cfg := demoConfig()
	cfg.Version = "1.7.0"
	cfg.Dirs = []string{"/home/dev/src/acme/project"}
	cfg.Reviews = []string{"sec-review", "code-review", "doc-review", "perf-review",
		"test-review", "arch-review", "deps-review", "ux-review"}
	base := cfg.Started
	ins, del := 34, 12
	events := append(demoEvents(),
		runner.Event{Kind: runner.EvReviewStart, Review: "arch-review", Agent: "claude", Loop: 1, Time: base.Add(40 * time.Second)},
		runner.Event{Kind: runner.EvUsage, Review: "sec-review", Agent: "claude", Tokens: 8200, Thinking: 2400, Time: base.Add(50 * time.Second)},
		runner.Event{Kind: runner.EvOutput, Review: "arch-review", Agent: "claude", Text: "internal/runner/runner.go: the loop owns three responsibilities", Repeat: 1},
		runner.Event{Kind: runner.EvOutput, Review: "arch-review", Agent: "claude", Text: "+ func (r *Runner) schedule(loop int) []string {", Repeat: 1},
		runner.Event{Kind: runner.EvReviewEnd, Review: "deps-review", Agent: "codex:gpt-5", Loop: 1,
			Status: runner.StatusOK, Elapsed: 128, Tokens: 15400, Thinking: 3100,
			Ins: &ins, Del: &del, Time: base.Add(70 * time.Second)},
		runner.Event{Kind: runner.EvUsage, Review: "arch-review", Agent: "claude", Tokens: 3100, Thinking: 900, Time: base.Add(80 * time.Second)},
	)
	if err := os.WriteFile(dir+"/dashboard.ansi", []byte(staticFrame(cfg, events, 104, 30)), 0o644); err != nil {
		t.Fatal(err)
	}

	rev := func(n, d string) PickReview { return PickReview{Name: n, Desc: d} }
	p := newPicker(PickConfig{
		Dir: "/home/dev/src/acme/project", Version: "1.7.0",
		Groups: []PickGroup{
			{Name: "quick", Reviews: []PickReview{
				rev("sec-review", "injection, secrets, and unsafe defaults"),
				rev("code-review", "correctness bugs and unclear code"),
				rev("test-review", "coverage of the paths that matter"),
			}},
			{Name: "standard", Reviews: []PickReview{
				rev("arch-review", "boundaries, layering, and coupling"),
				{Name: "doc-review", Desc: "docs that drifted from the code", Project: true},
			}},
			{Name: "security", Reviews: []PickReview{rev("deps-review", "vulnerable and unused dependencies")}},
			{Name: "frontend", Reviews: []PickReview{rev("ux-review", "flows a person has to follow")}},
		},
		Agents: []string{"claude", "codex:gpt-5", "gemini", "crush", "opencode"},
		Branch: "work", Merge: []string{"main"},
		CPUs: 16,
	})
	p.w, p.h, p.ready = 104, 28, true
	p.open[0] = true
	p.selected["sec-review"], p.selected["code-review"] = true, true
	p.agents[0], p.agents[3] = true, true
	p.concurrency().n = 4
	p.optByFlag("--push").on = true
	p.cursor[paneReviews] = 2
	if err := os.WriteFile(dir+"/launcher.ansi", []byte(p.View()), 0o644); err != nil {
		t.Fatal(err)
	}
}
