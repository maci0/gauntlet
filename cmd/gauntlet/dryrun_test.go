// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/prompt"
	"github.com/maci0/gauntlet/internal/report"
)

// The dry-run listing is a table, and a table only reads as one if its second
// column starts in the same place on every row. Review names come from the
// reviewed tree, so they are not all one column per character: a CJK name is
// two columns per glyph, and a budget counted in runes or bytes puts its row
// out of line with the rest. The --list half of this lives next to
// listReviews; this half is the one the dry run renders.
func TestDryRunColumnsLineUpForWideNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"aaaa-review", "café-review", "日本語-review"} {
		body := "Your goal is to test " + n + ".\nSummary: a description\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}

	// Where the column after the names begins, measured in terminal columns,
	// on every row that has one.
	starts := func(text, marker string) map[int]bool {
		at := map[int]bool{}
		for line := range strings.SplitSeq(text, "\n") {
			if before, _, ok := strings.Cut(line, marker); ok {
				at[report.Cells(before)] = true
			}
		}
		return at
	}

	d := &dirRun{dir: dir, set: set, reviews: set.Names}
	var dry bytes.Buffer
	dryRun(&dry, report.Palette{}, []*dirRun{d}, nil, &options{timeout: time.Minute})
	got := starts(dry.String(), "[project]")
	if len(got) != 1 {
		keys := slices.Sorted(maps.Keys(got))
		t.Errorf("--dry-run starts its origin column at %v, want one column:\n%s", keys, dry.String())
	}
}
