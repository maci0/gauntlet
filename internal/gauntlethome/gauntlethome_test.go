// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gauntlethome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
