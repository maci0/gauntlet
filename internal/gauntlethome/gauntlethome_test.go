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

// A bare "~" is the one spelling of the home directory ExpandPath refuses to
// resolve, because for a flag naming a file it might be a directory actually
// called "~". The state root has no such reading: it resolved the literal
// string, made it absolute against the working directory, and handed a run a
// directory named "~" beside the tree under review, which the run then created,
// journaled into, and reported through doctor as a working setup. "~/..." is
// the spelling that means home and "~user" names another account's, so both
// are refused here rather than resolved by guesswork.
func TestDirRefusesAnUnexpandedTildeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, value := range []string{"~", "~nosuchuser", "~nosuchuser/state"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("GAUNTLET_HOME", value)
			got, ok := Dir()
			if ok {
				t.Fatalf("GAUNTLET_HOME %q was left unexpanded, so Dir resolved it against the working directory and reported a usable root at %q", value, got)
			}
			if got != ".gauntlet" {
				t.Fatalf("Dir = %q, want the degraded %q", got, ".gauntlet")
			}
		})
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

// The other half of the contract, and the half the callers depend on: writing
// the same payload again leaves the tree in the state one write leaves. Every
// caller of this is re-executed in the ordinary course of a run — the reload
// handoff is written again by a successor that takes the swap to a second
// safe point, the checkpoint is rewritten after every recorded result, the dsh
// overlay is rewritten when a second run picks the same provider and model —
// and a write that appended, accumulated a temp, or left a stale sibling would
// grow the state tree with every repetition.
//
// Replacement is the separate property TestWriteFileAtomicReplacesTheWholeFile
// covers, and it needs a second payload to be visible; this one needs the same
// payload twice, which is what an actual repetition looks like.
func TestWriteFileAtomicReExecutionIsIdempotent(t *testing.T) {
	const payload = `{"run_id":"20260101T000000Z-a1b2c3d4","loops":3}`

	// Once and twice, compared against each other rather than against a
	// hand-written expectation: what a second write must not change is
	// whatever the first one produced.
	tree := func(writes int) (body string, others []string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")
		for i := 0; i < writes; i++ {
			if err := WriteFileAtomic(dir, ".state-", path, []byte(payload)); err != nil {
				t.Fatalf("write %d: %v", i+1, err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != "state.json" {
				others = append(others, e.Name())
			}
		}
		return string(got), others
	}

	once, onceOthers := tree(1)
	twice, twiceOthers := tree(2)
	if once != twice {
		t.Fatalf("a second write changed the file: once %q, twice %q", once, twice)
	}
	if once != payload {
		t.Fatalf("file holds %q, want %q", once, payload)
	}
	// The deferred Remove is a no-op only because the rename consumed the
	// temp; a repetition that did not consume it would leave one per write,
	// and the state tree is swept for leftovers only by the next write of the
	// same prefix, a day later.
	if len(onceOthers) != 0 || len(twiceOthers) != 0 {
		t.Fatalf("writes left files beside the target: after one %v, after two %v", onceOthers, twiceOthers)
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

// The state root degrades to ".gauntlet" beside the working directory when
// GAUNTLET_HOME names nothing usable and there is no usable HOME, which puts
// it inside the reviewed tree. A repository that ships one of these as a
// symlink would otherwise decide where the journal's renames land, so every
// existing component has to be a real directory and the mode is 0700.
func TestMkdirAllPrivateRefusesASymlinkedComponent(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllPrivate(filepath.Join(link, "runs", "2026-08-25")); err == nil {
		t.Fatal("MkdirAllPrivate created through a symlinked component")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("directory was created outside the state root: %v entries, %v", entries, err)
	}

	// The real path still works, and the components it makes are private.
	made := filepath.Join(root, "ok", "runs")
	if err := MkdirAllPrivate(made); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(made)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("made directory mode is %o, want 700", fi.Mode().Perm())
	}

	// A plain file where a directory belongs is refused too, not just a link.
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllPrivate(filepath.Join(root, "file", "under")); err == nil {
		t.Fatal("MkdirAllPrivate created under a regular file")
	}
}

// The durable variant exists for the directories a first run creates: a synced
// journal inside runs/<shard>/ whose ancestors the filesystem has never been
// told about is a journal a power cut loses even though every file in it was
// synced. What can be asserted here is the chain it builds, the refusal it
// keeps, and that a tree already on disk reports nothing made — the last is
// what keeps a long-lived state root from paying a sync per existing
// directory on every append.
func TestMkdirAllPrivateDurableBuildsTheChainAndKeepsTheRefusal(t *testing.T) {
	root := t.TempDir()
	chain := filepath.Join(root, "state", "runs", "2026-08-25")
	if err := MkdirAllPrivateDurable(chain); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "state"),
		filepath.Join(root, "state", "runs"),
		chain,
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s was not created: %v", p, err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode is %o, want 700", p, fi.Mode().Perm())
		}
	}
	// The leaf is synced, so the directory that would hold a journal cut
	// short by a power cut is syncable at least.
	if err := SyncDir(chain); err != nil {
		t.Fatalf("SyncDir on a directory this process created: %v", err)
	}

	// The refusal MkdirAllPrivate makes is still made, and nothing outside
	// the tree is touched by the attempt.
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllPrivateDurable(filepath.Join(link, "runs")); err == nil {
		t.Fatal("MkdirAllPrivateDurable created through a symlinked component")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("directory was created outside the state root: %v entries, %v", entries, err)
	}

	// A tree that already exists is the steady state of every run after the
	// first: nothing is made, so nothing is synced, and the call is the plain
	// creation MkdirAllPrivate already was.
	made, err := mkdirAllPrivate(chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 0 {
		t.Errorf("mkdirAllPrivate reported creating %v on a tree that exists", made)
	}
}
