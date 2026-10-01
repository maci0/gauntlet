// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A crash checkpoint has to count the review whose agent is running as
// unfinished, and never a review whose result is recorded: the first would be
// lost on resume, the second run twice.
func TestUnfinishedCountsTheRunningReviewAndNotTheRecordedOnes(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review", "c-review")
	gate := t.TempDir()
	started, release := filepath.Join(gate, "started"), filepath.Join(gate, "release")
	// Released on the way out too, so a failing assertion cannot leave the
	// blocked stub agent waiting forever. The stub also stops once the gate
	// directory is gone, since TempDir removes it right after this runs.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
	bin := fakeAgent(t, t.TempDir(), "claude", `
case "$*" in *b-review*)
  touch '`+started+`'
  while [ ! -e '`+release+`' ] && [ -d '`+gate+`' ]; do sleep 0.05; done ;;
esac
echo "RESULT: no-changes"`)

	all := []string{"a-review", "b-review", "c-review"}
	cfg := baseConfig(t, repo, set, all, bin)
	var progress atomic.Int32
	cfg.OnProgress = func() { progress.Add(1) }

	bus := NewBus()
	drain(bus)
	r, err := New(context.Background(), cfg, bus)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		r.Run(context.Background())
		close(done)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("b-review never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	unfinished := r.Unfinished()
	var recorded []string
	for _, res := range r.Stats().Results() {
		recorded = append(recorded, res.Review)
	}
	if len(unfinished) == 0 || unfinished[0] != "b-review" {
		t.Errorf("the running review must come first in Unfinished, got %v", unfinished)
	}
	for _, name := range recorded {
		if slices.Contains(unfinished, name) {
			t.Errorf("%s has a recorded result and is still listed unfinished: %v", name, unfinished)
		}
	}
	if got := len(unfinished) + len(recorded); got != len(all) {
		t.Errorf("unfinished %v and recorded %v should cover the %d reviews exactly", unfinished, recorded, len(all))
	}

	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	<-done
	bus.Close()

	if left := r.Unfinished(); len(left) != 0 {
		t.Errorf("a finished loop left %v unfinished", left)
	}
	// One call per recorded result and one for the finished loop.
	if n := progress.Load(); n != int32(len(all)+1) {
		t.Errorf("OnProgress ran %d times, want %d", n, len(all)+1)
	}
}

// A resumed run keeps its run id, so without a generation in the branch names
// its lanes would collide with the lane branches a killed predecessor left at
// an older base, and the loop would fall back to reviewing in place.
func TestAResumedParallelRunStepsAroundItsPredecessorsBranches(t *testing.T) {
	repo := testRepo(t)
	set, _ := promptSet(t, "a-review", "b-review")
	bin := fakeAgent(t, t.TempDir(), "claude", `echo "RESULT: no-changes"`)

	// What a killed generation-0 process leaves: lane branches with work on
	// them, at a commit the current HEAD is not.
	gitOut(t, repo, "checkout", "-q", "-b", "dead-lane")
	if err := os.WriteFile(filepath.Join(repo, "half.txt"), []byte("unmerged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, repo, "add", "half.txt")
	gitOut(t, repo, "commit", "-qm", "half done")
	gitOut(t, repo, "checkout", "-q", "main")
	for _, lane := range []string{"lane-0", "lane-1"} {
		gitOut(t, repo, "branch", "gauntlet/test-l1/"+lane, "dead-lane")
	}
	// And a review branch the kill cut off before anything was committed.
	gitOut(t, repo, "branch", "gauntlet/test-l1-lane0-00/a-review", "main")

	cfg := baseConfig(t, repo, set, []string{"a-review", "b-review"}, bin)
	cfg.Jobs = 2
	cfg.Generation = 1

	r, got := runRecorded(t, cfg)

	if c := r.Stats().Counts(); c.OK != 2 {
		t.Fatalf("counts: %+v", c)
	}
	sawGeneration := false
	for _, ev := range got {
		if strings.Contains(ev.Text, "falling back to sequential") {
			t.Fatalf("the lanes collided with the predecessor's branches: %s", ev.Text)
		}
		if strings.Contains(ev.Branch, "test-g1-l1") {
			sawGeneration = true
		}
	}
	if !sawGeneration {
		t.Error("no review branch carried the generation")
	}
	if out := gitOut(t, repo, "branch", "--list", "gauntlet/test-l1-lane0-00/a-review"); out != "" {
		t.Errorf("the predecessor's empty review branch was kept: %s", out)
	}
	// The predecessor's branches carry work and are left for a person.
	for _, lane := range []string{"lane-0", "lane-1"} {
		if out := gitOut(t, repo, "branch", "--list", "gauntlet/test-l1/"+lane); out == "" {
			t.Errorf("the predecessor's %s branch was deleted", lane)
		}
	}
}
