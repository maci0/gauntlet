// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gauntlethome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDirPrefersGauntletHome(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("GAUNTLET_HOME", custom)
	got, ok := Dir()
	if !ok {
		t.Fatal("GAUNTLET_HOME set, but Dir reports no usable root")
	}
	if got != custom {
		t.Fatalf("Dir = %q, want GAUNTLET_HOME %q", got, custom)
	}
}

func TestDirMakesRelativeGauntletHomeAbsolute(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", "state")
	got, ok := Dir()
	if !ok {
		t.Fatal("GAUNTLET_HOME set, but Dir reports no usable root")
	}
	want, err := filepath.Abs("state")
	if err != nil {
		t.Fatal(err)
	}
	// A relative GAUNTLET_HOME must not make the root depend on where in the
	// process it is read from: the journal and agents.json would otherwise
	// resolve against whatever directory is current at that moment.
	if got != want {
		t.Fatalf("Dir = %q, want the absolute form %q", got, want)
	}
}

func TestDirExpandsTildeGauntletHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GAUNTLET_HOME", "~/custom_state")
	got, ok := Dir()
	if !ok {
		t.Fatal("GAUNTLET_HOME with ~ set, but Dir reports no usable root")
	}
	want := filepath.Join(home, "custom_state")
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestDirIgnoresWhitespaceOnlyGauntletHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GAUNTLET_HOME", "   ")
	got, ok := Dir()
	if !ok {
		t.Fatal("usable HOME set, but Dir reports no usable root")
	}
	if got != filepath.Join(home, ".gauntlet") {
		t.Fatalf("Dir = %q, want %q", got, filepath.Join(home, ".gauntlet"))
	}
}

func TestDirRefusesGauntletHomeItCannotStat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A symlink pointing at itself cannot be resolved, so the stat fails with
	// something other than "not exist". That is neither a directory nor a root
	// waiting to be created, and it is exactly the case the boolean refuses:
	// a definitions file read from there would define the reviewed tree's own
	// agents.
	loop := filepath.Join(home, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("GAUNTLET_HOME", loop)
	got, ok := Dir()
	if ok {
		t.Fatalf("GAUNTLET_HOME %q cannot be stat-ed, but Dir claims a usable root", loop)
	}
	if got != ".gauntlet" {
		t.Fatalf("Dir = %q, want the degraded %q", got, ".gauntlet")
	}
}

func TestDirAcceptsGauntletHomeThatDoesNotExistYet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A root the journal has not created yet is not a broken one: it is what
	// the first run of a fresh install points at.
	want := filepath.Join(home, "state", "deeper")
	t.Setenv("GAUNTLET_HOME", want)
	got, ok := Dir()
	if !ok {
		t.Fatal("GAUNTLET_HOME names a directory to be created, but Dir refuses it")
	}
	if got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}

func TestDirDefaultsToGauntletUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", "")
	t.Setenv("HOME", home)
	got, ok := Dir()
	if !ok {
		t.Fatal("usable HOME set, but Dir reports no usable root")
	}
	if got != filepath.Join(home, ".gauntlet") {
		t.Fatalf("Dir = %q, want %q", got, filepath.Join(home, ".gauntlet"))
	}
}

func TestDirWithoutUsableHomeDegrades(t *testing.T) {
	t.Setenv("GAUNTLET_HOME", "")
	t.Setenv("HOME", "")
	got, ok := Dir()
	if ok {
		t.Fatal("no GAUNTLET_HOME and no HOME, but Dir claims a usable root")
	}
	if got != ".gauntlet" {
		t.Fatalf("degraded root = %q, want %q", got, ".gauntlet")
	}
}

func TestDirUnresolvableGauntletHomeDegrades(t *testing.T) {
	// Emptying the referenced variable is what makes the expansion miss: a
	// name that happens to be exported in the ambient environment would
	// otherwise resolve to a usable root, and the test would fail for a
	// reason that has nothing to do with the code.
	t.Setenv("GAUNTLET_NONEXISTENT_DIR_VAR", "")
	t.Setenv("GAUNTLET_HOME", "$GAUNTLET_NONEXISTENT_DIR_VAR/state")
	got, ok := Dir()
	if ok {
		t.Fatal("unresolvable GAUNTLET_HOME should not report a usable root")
	}
	if got != ".gauntlet" {
		t.Fatalf("degraded root = %q, want %q", got, ".gauntlet")
	}
}

func TestDirFileGauntletHomeDegrades(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAUNTLET_HOME", file)
	got, ok := Dir()
	if ok {
		t.Fatal("file GAUNTLET_HOME should not report a usable root")
	}
	if got != ".gauntlet" {
		t.Fatalf("degraded root = %q, want %q", got, ".gauntlet")
	}
}

func TestStateDirSitsUnderTheRoot(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("GAUNTLET_HOME", custom)
	if got, want := StateDir(), filepath.Join(custom, "state"); got != want {
		t.Fatalf("StateDir = %q, want %q", got, want)
	}

	// A degraded root names none: the relative fallback is under the working
	// directory, which in a run is the reviewed tree, and a handoff written
	// there is one nothing will pick up and one the tree should not have
	// gained. The caller that needs the handoff reports the refusal instead.
	t.Setenv("GAUNTLET_HOME", "$GAUNTLET_NONEXISTENT_DIR_VAR/state")
	if got := StateDir(); got != "" {
		t.Fatalf("degraded StateDir = %q, want the empty string", got)
	}
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GAUNTLET_TEST_DIR", filepath.Join(home, "env"))

	got, err := ExpandPath("~" + string(os.PathSeparator) + "src")
	if err != nil || got != filepath.Join(home, "src") {
		t.Errorf("~/src: got %q %v", got, err)
	}
	got, err = ExpandPath("~")
	if err != nil || got != "~" {
		t.Errorf("a bare ~ must be left alone, got %q %v", got, err)
	}
	got, err = ExpandPath("~other/src")
	if err != nil || got != "~other/src" {
		t.Errorf("another user's home is not ours to expand: %q %v", got, err)
	}
	got, err = ExpandPath("$GAUNTLET_TEST_DIR/sub")
	if err != nil || got != filepath.Join(home, "env", "sub") {
		t.Errorf("$VAR: got %q %v", got, err)
	}
	got, err = ExpandPath("${GAUNTLET_TEST_DIR}/sub")
	if err != nil || got != filepath.Join(home, "env", "sub") {
		t.Errorf("${VAR}: got %q %v", got, err)
	}
	got, err = ExpandPath("/plain/path")
	if err != nil || got != "/plain/path" {
		t.Errorf("plain path changed: %q %v", got, err)
	}
}

func TestExpandPathRefusesUnsetOrEmpty(t *testing.T) {
	t.Setenv("GAUNTLET_TEST_MISSING", "x")
	os.Unsetenv("GAUNTLET_TEST_MISSING")
	t.Setenv("GAUNTLET_TEST_EMPTY", "")
	t.Setenv("GAUNTLET_TEST_SET", "ok")

	for _, p := range []string{"$GAUNTLET_TEST_MISSING", "${GAUNTLET_TEST_MISSING}/x"} {
		got, err := ExpandPath(p)
		if err == nil || !strings.Contains(err.Error(), "GAUNTLET_TEST_MISSING") {
			t.Errorf("%q: want unset-var error, got %q %v", p, got, err)
		}
	}
	got, err := ExpandPath("$GAUNTLET_TEST_EMPTY/x")
	if err == nil || !strings.Contains(err.Error(), "GAUNTLET_TEST_EMPTY") {
		t.Errorf("empty var: got %q %v", got, err)
	}
	got, err = ExpandPath("$GAUNTLET_TEST_SET/$GAUNTLET_TEST_MISSING")
	if err == nil || !strings.Contains(err.Error(), "GAUNTLET_TEST_MISSING") {
		t.Errorf("mixed: got %q %v", got, err)
	}
}

// The only way to make os.UserHomeDir fail is an empty HOME; where it
// answers anyway, the refusal path below is unreachable from a test and the
// skip says so rather than passing on a home it did not ask for.
func TestExpandPathTildeNeedsHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("os.UserHomeDir answers for an empty HOME, so ~ cannot be made to fail here")
	}
	got, err := ExpandPath("~" + string(os.PathSeparator) + "src")
	if err == nil || !strings.Contains(err.Error(), "home directory is unknown") {
		t.Fatalf("~/src with no HOME: got %q %v", got, err)
	}
}

// FuzzExpandPath feeds arbitrary strings through ExpandPath and pins its
// contract: it must never panic, must be deterministic, must leave paths
// without '$' or leading '~/' untouched, and must never return a non-empty
// string alongside an error.
func FuzzExpandPath(f *testing.F) {
	seeds := []string{
		"~/src",
		"~",
		"~other/src",
		"/plain/path",
		"$GAUNTLET_FUZZ_DIR/sub",
		"${GAUNTLET_FUZZ_DIR}/sub",
		"$GAUNTLET_FUZZ_MISSING",
		"${GAUNTLET_FUZZ_MISSING}/x",
		"$GAUNTLET_FUZZ_EMPTY/x",
		"$",
		"${",
		"$$",
		"${}",
		"~/~/~",
		"\x00",
		"relative/path",
		"   ",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Setenv("GAUNTLET_FUZZ_DIR", "/fuzz/path")
	f.Setenv("GAUNTLET_FUZZ_EMPTY", "")
	os.Unsetenv("GAUNTLET_FUZZ_MISSING")

	f.Fuzz(func(t *testing.T, p string) {
		got, err := ExpandPath(p)
		if err != nil {
			if got != "" {
				t.Fatalf("ExpandPath(%q) returned non-empty string %q with error: %v", p, got, err)
			}
			return
		}
		// Invariant: if p has no $ and does not start with ~/, it must pass through unchanged.
		if !strings.Contains(p, "$") && !strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
			if got != p {
				t.Fatalf("ExpandPath(%q) = %q, want %q", p, got, p)
			}
		}
		// Deterministic check
		got2, err2 := ExpandPath(p)
		if (err == nil) != (err2 == nil) || got != got2 {
			t.Fatalf("ExpandPath(%q) is non-deterministic: (%q, %v) vs (%q, %v)", p, got, err, got2, err2)
		}
	})
}

// WriteFileAtomic is the only way the state tree replaces a file, so the
// contract that matters is what a reader opening the path at any moment can
// see: the previous contents, or the whole new ones. A truncate-then-write
// would let a reader land between the two and read a short file.
func TestWriteFileAtomicReplacesTheWholeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	if err := WriteFileAtomic(dir, ".index-", path, []byte(`{"old":true}`)); err != nil {
		t.Fatal(err)
	}
	// Longer than the first write, so a partial overwrite would show up as a
	// tail of the old contents rather than a prefix of the new.
	if err := WriteFileAtomic(dir, ".index-", path, []byte(`{"run":"second","rows":[1,2,3]}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"run":"second","rows":[1,2,3]}`; string(got) != want {
		t.Fatalf("file holds %q, want %q", got, want)
	}
	// The temp file is renamed into place, not left beside the target.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "index.json" {
			t.Errorf("write left %q behind in %s", e.Name(), dir)
		}
	}
}

func TestWriteFileAtomicFailsLoudlyOnAMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	err := WriteFileAtomic(dir, ".index-", filepath.Join(dir, "index.json"), []byte("x"))
	if err == nil {
		t.Fatal("writing into a directory that does not exist must fail, not report success")
	}
}

func TestWriteFileAtomicLeavesNoTempBehindOnFailure(t *testing.T) {
	dir := t.TempDir()
	// The rename cannot succeed: the target is a directory, not a file. The
	// write itself succeeds, so this reaches the one step that can fail after
	// the temp file exists.
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(dir, ".target-", filepath.Join(dir, "target"), []byte("x")); err == nil {
		t.Fatal("renaming a file over a directory must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "target" {
			t.Errorf("failed write left %q behind in %s", e.Name(), dir)
		}
	}
}

func TestSyncDir(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Fatalf("syncing a real directory: %v", err)
	}
	// A directory that is not there is a real failure, not the EINVAL case:
	// there was no entry to flush, so nothing was lost either.
	if err := SyncDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("syncing a directory that does not exist must report the error")
	}
}

func TestSweepStaleTemps(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := write(".prefix-old")
	fresh := write(".prefix-new")
	other := write("other")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now }

	SweepStaleTemps(dir, ".prefix-", 24*time.Hour, clock)

	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive the sweep: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old file should be gone (stat err=%v)", err)
	}
}

// The sweep removes files and nothing else. The kind is settled by the stat
// rather than by the type the directory entry carries, so a mount reporting no
// entry type sweeps the same set a local disk does, and a planted directory or
// symlink under the prefix is still left alone.
func TestSweepStaleTempsOnlyRemovesRegularFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-48 * time.Hour)
	age := func(name string) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(dir, name), past, past); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(dir, ".prefix-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".prefix-dir", "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".prefix-link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".prefix-dir", "target", ".prefix-link"} {
		age(name)
	}

	SweepStaleTemps(dir, ".prefix-", 24*time.Hour, func() time.Time { return now })

	for _, name := range []string{".prefix-dir", ".prefix-link", "target"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s must survive the sweep: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, ".prefix-dir", "keep")); err != nil {
		t.Errorf("the sweep must not have recursed into a directory: %v", err)
	}
}

// A sweep that ran at a different time must not sweep differently: the cutoff
// comes from the caller's clock, so a replay of the same state with the same
// clock removes the same files.
func TestSweepStaleTempsFollowsTheInjectedClock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".prefix-a")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	// The clock sits behind the file, so a wall-clock sweep would not have
	// reached it yet either: nothing goes.
	SweepStaleTemps(dir, ".prefix-", 24*time.Hour, func() time.Time {
		return mtime.Add(-time.Hour)
	})
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("a file newer than the cutoff must survive: %v", err)
	}

	// The clock steps past the cutoff and the same file goes.
	SweepStaleTemps(dir, ".prefix-", 24*time.Hour, func() time.Time {
		return mtime.Add(25 * time.Hour)
	})
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("a file past the cutoff should be gone (stat err=%v)", err)
	}
}
