// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package gauntlethome

import (
	"path/filepath"
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
