// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/report"
)

// `gauntlet resume` with no run id lists every run a crash left behind, and
// each entry names the directories that run was reviewing. Those names come
// from the reviewed tree, so this is one of the few places a checkout named in
// a script with case ("Ökonto", "Ärchi") or with no case at all ("日本語") is
// printed. Under byte order every accented capital sorts after every plain
// letter, so an operator reading their own filesystem did not find their
// checkouts where they looked. This pins the collation every other printed
// list of names in the tool already uses.
func TestListCheckpointsOrdersDirsByCollation(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", t.TempDir())
	root := t.TempDir()
	// The basenames below are the whole point of the test, so the tree
	// carries them under one common parent rather than under per-test
	// temp dirs with random names.
	order := []string{"Zebra", "apple", "Ökonto", "Ärchi", "Ökonomie", "日本語", "тест", "École"}
	var dirs []string
	for _, name := range order {
		d := filepath.Join(root, name)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Skipf("this filesystem rejects %q: %v", name, err)
		}
		dirs = append(dirs, d)
	}

	cp := checkpoint{
		Handoff: handoff{RunID: "20261001T015123Z-1a2b", Dirs: map[string]dirHandoff{}},
		Updated: time.Now(),
		Argv:    []string{"gauntlet", "resume", "20261001T015123Z-1a2b"},
	}
	for _, d := range dirs {
		cp.Handoff.Dirs[handoffKey(d)] = dirHandoff{}
	}
	if err := saveCheckpoint(cp); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	if code := listCheckpoints(&sb, report.Palette{}); code != exitOK {
		t.Fatalf("listCheckpoints = %d:\n%s", code, sb.String())
	}

	// The printed line is the one carrying every directory, comma-joined.
	var printed string
	for line := range strings.SplitSeq(sb.String(), "\n") {
		if strings.Contains(line, filepath.Join(root, "apple")) {
			printed = line
			break
		}
	}
	if printed == "" {
		t.Fatalf("no listing line naming the directories:\n%s", sb.String())
	}
	got := make([]string, 0, len(dirs))
	for cell := range strings.SplitSeq(printed, ",") {
		cell = strings.TrimSpace(cell)
		// The clause after the last name is "N unfinished in the current
		// loop, ...", not a path.
		if !strings.HasPrefix(cell, root) {
			break
		}
		got = append(got, cell)
	}
	if len(got) != len(dirs) {
		t.Fatalf("listing names %d directories, want %d:\n%s", len(got), len(dirs), printed)
	}
	// Every name survives the listing whole: the path is printed, not a slug.
	for _, d := range dirs {
		if !slices.Contains(got, d) {
			t.Errorf("listing lost %q, printed:\n%s", d, printed)
		}
	}
	// The accented capitals are the discriminator: byte order puts every one
	// of them after "Zebra", and collation files each under the letter a
	// reader of that name looks for.
	if i, j := indexOf(got, filepath.Join(root, "Ärchi")), indexOf(got, filepath.Join(root, "Zebra")); i > j {
		t.Errorf("Ärchi printed at %d, after Zebra at %d; the list is not collated:\n%s", i, j, printed)
	}
	if i, j := indexOf(got, filepath.Join(root, "apple")), indexOf(got, filepath.Join(root, "Ökonomie")); i > j {
		t.Errorf("apple printed at %d, after Ökonomie at %d; the list is not collated:\n%s", i, j, printed)
	}
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
