// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package ui

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runner"
)

// renderModel builds the dashboard a busy run leaves behind: a full review
// grid, every lane working, and a feed the filter has to narrow.
func renderModel(b *testing.B) *model {
	b.Helper()
	dirs := []string{"/w/one", "/w/two"}
	m := &model{
		w: 160, h: 48, ready: true, loop: 2,
		now:        time.Date(2026, 8, 25, 13, 4, 0, 0, time.UTC),
		lastSample: mNow(),
		lanes:      map[string]*laneState{},
		reviews:    map[string]*reviewState{},
		counts:     map[string]int{},
		filter:     feedAll,
		hues:       newHueMap(),
		cfg: Config{
			Version: "0.1.0", RunID: "20260825T130000Z-abcd", Dirs: dirs,
			Started: time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC),
			Jobs:    4, Timeout: 20 * time.Minute, Budget: 2 * time.Hour,
		},
	}
	for _, d := range dirs {
		m.cfg.Dirs = append(m.cfg.Dirs, d)
	}
	agents := []string{"claude", "codex", "gemini", "copilot", "opencode", "crush", "aider", "kilo"}
	for i, a := range agents {
		l := &laneState{
			review: "security-review", start: m.now.Add(-90 * time.Second),
			done: 4 + i, failed: i % 3, tokens: 120000 * (i + 1),
			thinkTokens: 30000 * (i + 1), liveTokens: 4000, liveThinking: 900,
			tokenRate: 12.5 + float64(i), lastAt: m.now, lastThinkAt: m.now,
		}
		m.lanes[a] = l
		m.laneOrd = append(m.laneOrd, a)
		m.hues.get(a)
		for k := range laneSamples {
			l.lines = append(l.lines, 8+math.Sin(float64(k)/3)*6)
		}
	}
	for i := range activitySamples {
		m.activity = append(m.activity, 20+math.Cos(float64(i)/4)*15)
	}
	for i := range 60 {
		m.apply(runner.Event{
			Kind: runner.EvReviewStart, Review: "review-" + strconv.Itoa(i%30),
			Agent: agents[i%len(agents)], Time: m.now, Dir: dirs[i%2],
		})
	}
	m.orderDirty = true
	for i := range feedMax {
		m.pushFeed(feedLine{
			text: "ok  internal/pkg/thing.go:42  a line of agent narration number " + strconv.Itoa(i),
			kind: normalize.Plain, agent: agents[i%len(agents)], review: "review-" + strconv.Itoa(i%30),
		})
	}
	m.filter = feedSignal
	m.feedDirty = true
	return m
}

func mNow() time.Time {
	return time.Date(2026, 8, 25, 13, 3, 0, 0, time.UTC)
}

// BenchmarkView measures one frame of the dashboard at the size a real
// terminal gives it, which is where the render path runs ten times a second
// for the life of a run.
func BenchmarkView(b *testing.B) {
	m := renderModel(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if got := m.View(); got == "" {
			b.Fatal("empty frame")
		}
	}
}

// BenchmarkChart isolates the braille panel the activity strip and every lane
// sparkline are drawn with.
func BenchmarkChart(b *testing.B) {
	vals := make([]float64, 64)
	for i := range vals {
		vals[i] = math.Sin(float64(i)/5)*20 + 20
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if chart(vals, 120, 8) == "" {
			b.Fatal("empty chart")
		}
	}
}
