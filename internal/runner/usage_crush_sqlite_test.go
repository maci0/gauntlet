// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build sqlite

package runner

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// crushDB writes a database shaped like crush's own, with the sessions it is
// given: id, completion tokens, and when it was last written.
func crushDB(t *testing.T, dir string, sessions map[string][2]int64) string {
	t.Helper()
	path := filepath.Join(dir, ".crush", "crush.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id, v := range sessions {
		if _, err := db.Exec(
			`INSERT INTO sessions (id, completion_tokens, updated_at) VALUES (?, ?, ?)`,
			id, v[0], v[1]); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// What crush spent on this review is what it wrote while the review ran:
// sessions from before it belong to whatever ran earlier.
func TestCrushReaderCountsOnlyThisReview(t *testing.T) {
	dir := t.TempDir()
	since := time.Now()
	crushDB(t, dir, map[string][2]int64{
		"before": {5000, since.Add(-time.Hour).UnixMilli()},
		"during": {1200, since.Add(time.Minute).UnixMilli()},
		"also":   {300, since.Add(2 * time.Minute).UnixMilli()},
	})

	out, think := newCrushReader(dir, since).Final()
	if out != 1500 {
		t.Fatalf("output tokens %d, want the 1500 written during the review", out)
	}
	if think != 0 {
		t.Fatalf("thinking %d: crush reports no reasoning split, so it must claim none", think)
	}
}

// A directory crush has never run in reports nothing rather than failing.
func TestCrushReaderWithoutADatabase(t *testing.T) {
	if out, think := newCrushReader(t.TempDir(), time.Now()).Final(); out != 0 || think != 0 {
		t.Fatalf("got %d/%d from a tree with no crush database", out, think)
	}
}

// A worktree under the project uses the project's database, which is where
// crush puts it: it resolves the git root, not the current directory.
func TestCrushReaderFindsTheProjectDatabase(t *testing.T) {
	root := t.TempDir()
	since := time.Now()
	crushDB(t, root, map[string][2]int64{"s": {42, since.Add(time.Second).UnixMilli()}})
	sub := filepath.Join(root, ".gauntlet", "worktrees", "sec-review")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, _ := newCrushReader(sub, since).Final(); out != 42 {
		t.Fatalf("output tokens %d, want the project database's 42", out)
	}
}
