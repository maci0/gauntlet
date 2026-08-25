// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// handoffBlob mirrors what a reload carries: counters plus the unfinished
// queue, both of which must survive the exec byte for byte.
type handoffBlob struct {
	Loops   int      `json:"loops"`
	Pending []string `json:"pending"`
}

func TestSaveAndLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := handoffBlob{Loops: 2, Pending: []string{"sec-review", "doc-review"}}

	path, err := SaveState(dir, "run-1", want)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "run-1.json") {
		t.Fatalf("state at %q, want it named after the run id", path)
	}
	t.Setenv(stateEnv, path)

	var got handoffBlob
	if !LoadState(&got) {
		t.Fatal("a written handoff must be found")
	}
	if got.Loops != want.Loops || len(got.Pending) != 2 ||
		got.Pending[0] != "sec-review" || got.Pending[1] != "doc-review" {
		t.Fatalf("handoff corrupted across the reload: %+v", got)
	}
	// Read once means gone: a stale handoff must not resurrect old counters
	// on the next manual start.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the handoff file survived its own load")
	}
	if LoadState(&got) {
		t.Fatal("the same handoff was served twice")
	}
}

func TestLoadStateWithoutHandoff(t *testing.T) {
	t.Setenv(stateEnv, "")
	var v handoffBlob
	if LoadState(&v) {
		t.Fatal("a normal start has no state to load")
	}
}

func TestLoadStateRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run-1.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(stateEnv, path)

	var v handoffBlob
	if LoadState(&v) {
		t.Fatal("garbage loaded as handoff state")
	}
	// A rejected handoff is still consumed: retrying the start must not trip
	// over it forever.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an unreadable handoff was left behind")
	}
}

func TestSaveStateMarshalsWhatLoadStateReads(t *testing.T) {
	// The two ends live on opposite sides of an exec, so their contract is
	// exactly the JSON round trip.
	dir := t.TempDir()
	want := handoffBlob{Loops: 7, Pending: nil}
	if _, err := SaveState(dir, "run", want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON map[string]any
	if err := json.Unmarshal(raw, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if viaJSON["loops"] != float64(7) {
		t.Fatalf("loops not encoded: %s", raw)
	}
}
