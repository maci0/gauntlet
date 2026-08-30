// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestSaveStateSweepsStaleHandoffs(t *testing.T) {
	dir := t.TempDir()

	// A handoff whose reload died between the save and the exec: old enough
	// that no live reload could still be carrying it.
	stale := filepath.Join(dir, "dead-run.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-(staleTempAge + time.Hour))
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	// The temp a killed SaveState left behind, from the same crashed reload.
	staleTmp := filepath.Join(dir, ".dead-run.json-123456")
	if err := os.WriteFile(staleTmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staleTmp, old, old); err != nil {
		t.Fatal(err)
	}

	// A fresh handoff another live process wrote moments ago must survive.
	fresh := filepath.Join(dir, "live-run.json")
	if err := os.WriteFile(fresh, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := SaveState(dir, "current-run", handoffBlob{Loops: 1}); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{stale, staleTmp} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("stale handoff %s survived the sweep", gone)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a live handoff was swept away: %v", err)
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

func TestSaveStateReplacesWholeFile(t *testing.T) {
	// The handoff is written moments before the exec: a rewrite must replace
	// the file whole rather than truncate it in place, so a kill mid-write
	// leaves either the old state or none instead of a blob that fails to
	// parse and silently restarts every loop.
	dir := t.TempDir()
	first := handoffBlob{Loops: 1, Pending: []string{"a-review"}}
	second := handoffBlob{Loops: 2, Pending: []string{"b-review", "c-review"}}

	path, err := SaveState(dir, "run-1", first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SaveState(dir, "run-1", second); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got handoffBlob
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("rewritten state does not parse (torn write?): %v", err)
	}
	if got.Loops != 2 || len(got.Pending) != 2 || got.Pending[1] != "c-review" {
		t.Fatalf("rewrite lost: %+v", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "run-1.json" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
