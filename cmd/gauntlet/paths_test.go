// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := expandPath("~/src"); got != filepath.Join(home, "src") {
		t.Errorf("~/src expanded to %q", got)
	}
	if got := expandPath("~"); got != "~" {
		t.Errorf("a bare ~ must be left alone, got %q", got)
	}
	if got := expandPath("~other/src"); got != "~other/src" {
		t.Errorf("another user's home is not ours to expand: %q", got)
	}

	t.Setenv("GAUNTLET_TEST_DIR", filepath.Join(home, "env"))
	if got := expandPath("$GAUNTLET_TEST_DIR/sub"); got != filepath.Join(home, "env", "sub") {
		t.Errorf("$VAR not expanded: %q", got)
	}
	if got := expandPath("/plain/path"); got != "/plain/path" {
		t.Errorf("plain path changed: %q", got)
	}
}

func TestIsGlob(t *testing.T) {
	for _, p := range []string{"~/src/*", "d?r", "[ab]/x"} {
		if !isGlob(p) {
			t.Errorf("%q should need expansion", p)
		}
	}
	for _, p := range []string{"~/src", "/", "a-b_c.d", ""} {
		if isGlob(p) {
			t.Errorf("%q is a literal path, not a pattern", p)
		}
	}
}

// resolveDirs turns flags into absolute directories. A quoted glob still
// expands (the shell had its chance), globs skip non-directories and may
// match nothing only as a whole, while a literal path that is missing or a
// file is the user naming something specific and must be refused.
func TestResolveDirs(t *testing.T) {
	base := t.TempDir()
	mkdir := func(name string) string {
		t.Helper()
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	d1, d2 := mkdir("d1"), mkdir("d2")
	writeFile := func(name string) string {
		t.Helper()
		p := filepath.Join(base, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	file := writeFile("file.txt")

	t.Run("literal dir becomes absolute", func(t *testing.T) {
		got, err := resolveDirs(&options{dirs: []string{base}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != base {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("one tree twice runs once", func(t *testing.T) {
		got, err := resolveDirs(&options{dirs: []string{base, base}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("a duplicated tree would block on its own lock: %v", got)
		}
	})

	t.Run("glob expands to directories only", func(t *testing.T) {
		got, err := resolveDirs(&options{dirs: []string{filepath.Join(base, "d*")}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != d1 || got[1] != d2 {
			t.Fatalf("want sorted dirs without %s, got %v", file, got)
		}
	})

	t.Run("glob matching no directory is an error", func(t *testing.T) {
		_, err := resolveDirs(&options{dirs: []string{filepath.Join(base, "zzz/*")}})
		if err == nil || !strings.Contains(err.Error(), "--dirs") {
			t.Fatalf("want a --dirs error, got %v", err)
		}
	})

	t.Run("literal missing path is an error", func(t *testing.T) {
		missing := filepath.Join(base, "absent")
		_, err := resolveDirs(&options{dirs: []string{missing}})
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("error should name the bad path, got %v", err)
		}
	})

	t.Run("literal file is an error", func(t *testing.T) {
		_, err := resolveDirs(&options{dirs: []string{file}})
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("want a not-a-directory error, got %v", err)
		}
	})

	// The legacy singular flag reports its problems as --dir, not --dirs:
	// the message names what the user actually typed.
	t.Run("singular flag keeps its own label", func(t *testing.T) {
		_, err := resolveDirs(&options{dir: filepath.Join(base, "absent")})
		if err == nil || !strings.Contains(err.Error(), "--dir:") ||
			strings.Contains(err.Error(), "--dirs") {
			t.Fatalf("want a --dir error, got %v", err)
		}
	})
}
