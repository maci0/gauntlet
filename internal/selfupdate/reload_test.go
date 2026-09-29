// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package selfupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/gauntlethome"
)

// handoffBlob mirrors what a reload carries: counters plus the unfinished
// queue, both of which must survive the exec byte for byte.
type handoffBlob struct {
	Loops   int      `json:"loops"`
	Pending []string `json:"pending"`
}

func TestFingerprintSameIgnoresLocation(t *testing.T) {
	utc := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	offset := utc.In(time.FixedZone("test+2", 2*60*60))
	a := fingerprint{inode: 1, size: 10, mtime: utc}
	b := fingerprint{inode: 1, size: 10, mtime: offset}
	if !a.same(b) {
		t.Fatal("the same instant in different zones must count as the same file")
	}
	c := fingerprint{inode: 1, size: 10, mtime: utc.Add(time.Second)}
	if a.same(c) {
		t.Fatal("a later mtime must not count as the same file")
	}
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
	ok, err := LoadState(&got)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
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
	ok, err = LoadState(&got)
	if ok {
		t.Fatal("the same handoff was served twice")
	}
	if err == nil {
		t.Fatal("a consumed handoff must be reported, not treated as a fresh start")
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
	old := time.Now().Add(-(gauntlethome.StaleTempAge + time.Hour))
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
	ok, err := LoadState(&v)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
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
	ok, err := LoadState(&v)
	if ok {
		t.Fatal("garbage loaded as handoff state")
	}
	if err == nil {
		t.Fatal("an unreadable handoff must be an error, not a silent fresh start")
	}
	// A rejected handoff is still consumed: retrying the start must not trip
	// over it forever.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("an unreadable handoff was left behind")
	}
}

func TestSaveStateRejectsInvalidRunID(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", ".", "../escape", "sub/dir", "sub\\dir", "a/b", ".."} {
		if _, err := SaveState(dir, bad, handoffBlob{Loops: 1}); err == nil {
			t.Errorf("SaveState with runID %q succeeded, want error", bad)
		}
	}
}

// The path SaveState returns becomes GAUNTLET_STATE, and the successor refuses
// a relative one. The state root degrades to a relative ".gauntlet" whenever
// neither GAUNTLET_HOME nor HOME yields a usable root, so writing a relative
// path would save the handoff, exec, and then fail in the successor with
// nothing said. The returned path has to be absolute, or the reload is refused
// before it starts.
func TestSaveStateReturnsAnAbsolutePath(t *testing.T) {
	// A relative state root, as gauntlethome.Dir yields when it cannot
	// resolve a home.
	t.Chdir(t.TempDir())
	dir := filepath.Join(".gauntlet", "state")
	path, err := SaveState(dir, "run", handoffBlob{Loops: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("SaveState returned the relative path %s, which LoadState refuses", path)
	}
	var v handoffBlob
	t.Setenv(stateEnv, path)
	ok, err := LoadState(&v)
	if err != nil || !ok {
		t.Fatalf("the handoff SaveState wrote did not load back: ok=%v err=%v", ok, err)
	}
}

func TestLoadStateRejectsNonRegularOrNonJSON(t *testing.T) {
	dir := t.TempDir()

	// Non-.json extension
	txtFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(txtFile, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(stateEnv, txtFile)
	var v handoffBlob
	if _, err := LoadState(&v); err == nil {
		t.Fatal("LoadState accepted non-.json file")
	}
	// The file must NOT be deleted
	if _, err := os.Stat(txtFile); err != nil {
		t.Fatal("non-.json file was deleted by LoadState")
	}

	// Symlink must be rejected and target must NOT be deleted
	realFile := filepath.Join(dir, "target.json")
	if err := os.WriteFile(realFile, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "symlink.json")
	if err := os.Symlink(realFile, symlink); err != nil {
		t.Fatal(err)
	}
	t.Setenv(stateEnv, symlink)
	if _, err := LoadState(&v); err == nil {
		t.Fatal("LoadState accepted symlink")
	}
	if _, err := os.Stat(realFile); err != nil {
		t.Fatal("target of symlink was deleted by LoadState")
	}

	// Directory must be rejected and NOT deleted
	subDir := filepath.Join(dir, "nested.json")
	if err := os.Mkdir(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(stateEnv, subDir)
	if _, err := LoadState(&v); err == nil {
		t.Fatal("LoadState accepted directory")
	}
	if _, err := os.Stat(subDir); err != nil {
		t.Fatal("directory was deleted by LoadState")
	}

	// Relative path must be rejected
	t.Setenv(stateEnv, "relative.json")
	if _, err := LoadState(&v); err == nil {
		t.Fatal("LoadState accepted relative path")
	}

	// Unclean / traversal path must be rejected
	t.Setenv(stateEnv, filepath.Join(dir, "sub", "..", "run-1.json"))
	if _, err := LoadState(&v); err == nil {
		t.Fatal("LoadState accepted unclean path with ..")
	}

	// Oversized file must be rejected
	bigFile := filepath.Join(dir, "big.json")
	f, err := os.Create(bigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxHandoffBytes + 10); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	t.Setenv(stateEnv, bigFile)
	if _, err := LoadState(&v); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("LoadState accepted oversized file: %v", err)
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
	// parse and aborts the successor.
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

func TestFingerprintValidAndStat(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fEmpty, err := stat(empty)
	if err != nil {
		t.Fatal(err)
	}
	if fEmpty.valid() {
		t.Fatal("empty file should not be a valid fingerprint")
	}

	nonEmpty := filepath.Join(dir, "nonempty")
	if err := os.WriteFile(nonEmpty, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	fNonEmpty, err := stat(nonEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if !fNonEmpty.valid() {
		t.Fatal("non-empty file should have a valid fingerprint")
	}
	if fNonEmpty.size != 4 {
		t.Fatalf("size = %d, want 4", fNonEmpty.size)
	}
}

// watchFile is Watch without os.Executable: the loop is what decides a reload,
// and the file it watches is the only thing a test may substitute for the
// running binary. Watch itself resolves the real executable, which a test
// cannot replace.
func watchFile(t *testing.T, ctx context.Context, path string) <-chan string {
	t.Helper()
	base, err := stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 1)
	w := &Watcher{path: path, every: 10 * time.Millisecond, base: base, Change: ch}
	go w.run(ctx, ch)
	return ch
}

// replace swaps the watched file the way self-update does: a new file built
// beside it and renamed over, so a reader sees one whole binary or the other.
func replace(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func TestWatchReportsAReplacedBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gauntlet")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	ch := watchFile(t, t.Context(), path)

	// Untouched for a few ticks first: a watcher that fired on the file it
	// started from would reload in a loop forever.
	select {
	case p, ok := <-ch:
		t.Fatalf("watcher fired with no change to the binary: %q (open=%t)", p, ok)
	case <-time.After(100 * time.Millisecond):
	}

	replace(t, path, "new binary, longer")
	select {
	case p, ok := <-ch:
		if !ok {
			t.Fatal("watcher closed the channel without reporting the change")
		}
		if p != path {
			t.Fatalf("watcher named %q, want the watched path %q", p, path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not notice the replaced binary")
	}
	// The loop ends after reporting: a second binary replacing the first
	// needs a fresh watcher, which is what the caller starts.
	select {
	case p, ok := <-ch:
		if ok {
			t.Fatalf("watcher reported a second change %q without a new binary", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop after reporting the change")
	}
}

func TestWatchIgnoresAMissingBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gauntlet")
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	ch := watchFile(t, t.Context(), path)

	// A self-update that removes the target before renaming into place is a
	// rename caught mid-flight, not a deletion the operator asked for. It must
	// not fire a reload on a half-written file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	select {
	case p, ok := <-ch:
		t.Fatalf("watcher reported a change for a missing binary: %q (open=%t)", p, ok)
	case <-time.After(100 * time.Millisecond):
	}

	// The file coming back is the change worth reporting.
	replace(t, path, "restored binary")
	select {
	case p, ok := <-ch:
		if !ok {
			t.Fatal("watcher closed the channel without reporting the restored binary")
		}
		if p != path {
			t.Fatalf("watcher named %q, want %q", p, path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not notice the binary come back")
	}
}

func TestWatchContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w, err := Watch(ctx, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p, ok := <-w.Change:
		if ok {
			t.Fatalf("unexpected change notification on canceled context: %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not terminate when context was cancelled")
	}
}

// Dropping a handoff is the half of read-once that a power cut can undo, so
// the removal is synced and a second drop of the same file is the outcome the
// unlink wanted rather than a failure: the successor is the only reader, and
// it is the reader that just read.
func TestDropHandoffRemovesAndToleratesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveState(dir, "run-1", handoffBlob{Loops: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := DropState(path); err != nil {
		t.Fatalf("dropping a handoff: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the handoff survived its own drop: %v", err)
	}
	if err := DropState(path); err != nil {
		t.Fatalf("dropping a handoff that is already gone must succeed, got: %v", err)
	}
}
