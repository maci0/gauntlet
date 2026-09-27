// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// CHANGELOG.md states the consumer contract: the event stream under
// ~/.gauntlet/runs is API, because docs/RUNS.md tells operators to read it
// with jq or their own tools. Every other surface the contract names has a
// snapshot (flags, commands, exit codes, environment variables in
// cmd/gauntlet; review and set names in internal/prompt). The `ev` values had
// none, so renaming or dropping one would break a consumer's jq query with
// nothing failing here.

// goldenEventKinds is the `ev` field of every line in a run journal.
var goldenEventKinds = []string{
	"commit", "log", "loop_end", "loop_start", "merge", "pull_request",
	"reload", "review_end", "review_start", "run_end", "run_start",
}

// declaredEventKinds is every Kind constant in event.go, sorted.
func declaredEventKinds() []string {
	kinds := []string{
		string(EvRunStart), string(EvLoopStart), string(EvReviewStart),
		string(EvReviewEnd), string(EvMerge), string(EvPullRequest),
		string(EvCommit), string(EvLoopEnd), string(EvRunEnd),
		string(EvUsage), string(EvLog), string(EvOutput), string(EvReload),
	}
	slices.Sort(kinds)
	return kinds
}

func TestJournalEventKindsMatchTheContract(t *testing.T) {
	got := declaredEventKinds()
	journaled := make([]string, 0, len(got))
	for _, kind := range got {
		if !Droppable(Kind(kind)) {
			journaled = append(journaled, kind)
		}
	}
	if !slices.Equal(journaled, goldenEventKinds) {
		t.Fatalf("the journaled event-kind surface changed\n  got:  %s\n  want: %s\nremovals and renames are breaking and wait for the next major version; additions may land in a minor. Record the change in CHANGELOG.md, document the kind in docs/RUNS.md, and update goldenEventKinds in the same commit.",
			strings.Join(journaled, ", "), strings.Join(goldenEventKinds, ", "))
	}
}

// docs/RUNS.md is what a consumer reads to write the query, so a kind the
// runner journals and the page does not describe is an undocumented event.
func TestJournalEventKindsAreDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "RUNS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, kind := range goldenEventKinds {
		if !strings.Contains(text, "`"+kind+"`") {
			t.Errorf("docs/RUNS.md does not document the %q event; the run journal's event stream is consumer contract API", kind)
		}
	}
}
