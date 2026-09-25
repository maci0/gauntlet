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

func TestExpandPathTildeNeedsHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("UserHomeDir falls back to the passwd database when HOME is empty")
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
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	SweepStaleTemps(dir, ".prefix-", 24*time.Hour)

	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive the sweep: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the old file should be gone (stat err=%v)", err)
	}
}
