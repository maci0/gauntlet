// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a set of files, creating the directories they need.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The heuristic suggester proposes what the files justify, and nothing else:
// proposing everything would be the same as proposing nothing.
func TestFastSuggestFollowsTheFiles(t *testing.T) {
	pool := []string{
		"code-review", "sec-review", "container-review", "db-review",
		"ux-review", "test-review", "i18n-review", "mobile-review",
	}
	cases := []struct {
		name    string
		files   []string
		want    []string
		notWant []string
	}{
		{
			name:    "a Go service with tests and a Dockerfile",
			files:   []string{"main.go", "main_test.go", "Dockerfile"},
			want:    []string{"code-review", "sec-review", "test-review", "container-review"},
			notWant: []string{"ux-review", "mobile-review", "db-review"},
		},
		{
			name:    "a web frontend",
			files:   []string{"src/app.tsx", "src/app.css", "index.html"},
			want:    []string{"ux-review", "code-review"},
			notWant: []string{"container-review", "db-review"},
		},
		{
			name:    "migrations and queries",
			files:   []string{"db/migrations/001_init.sql"},
			want:    []string{"db-review"},
			notWant: []string{"ux-review"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, s := range fastSuggest(tree(t, c.files...), pool) {
				got[s.Name] = true
				if s.Reason == "" {
					t.Fatalf("%s was proposed with no evidence", s.Name)
				}
			}
			for _, want := range c.want {
				if !got[want] {
					t.Errorf("%s was not proposed for %v", want, c.files)
				}
			}
			for _, no := range c.notWant {
				if got[no] {
					t.Errorf("%s was proposed for %v with nothing to justify it", no, c.files)
				}
			}
		})
	}
}

// Only reviews in the pool are proposed: --exclude and a project's own prompt
// set decide what exists, and the suggester does not get to widen that.
func TestFastSuggestStaysInThePool(t *testing.T) {
	dir := tree(t, "main.go", "Dockerfile")
	for _, s := range fastSuggest(dir, []string{"code-review"}) {
		if s.Name != "code-review" {
			t.Fatalf("%s is outside the pool", s.Name)
		}
	}
}

// An empty directory justifies nothing, and says so by proposing nothing
// rather than falling back to everything.
func TestFastSuggestProposesNothingForAnEmptyTree(t *testing.T) {
	if got := fastSuggest(t.TempDir(), []string{"code-review", "sec-review"}); len(got) != 0 {
		t.Fatalf("an empty tree produced %v", got)
	}
}

// The walk stays out of dependency and build directories: what npm downloaded
// says nothing about the project under review.
func TestFastSuggestIgnoresVendoredTrees(t *testing.T) {
	dir := tree(t, "node_modules/react/index.tsx", "vendor/lib/thing.c", "README.md")
	var names []string
	for _, s := range fastSuggest(dir, []string{"ux-review", "resource-review", "doc-review"}) {
		names = append(names, s.Name)
	}
	if strings.Contains(strings.Join(names, ","), "ux-review") ||
		strings.Contains(strings.Join(names, ","), "resource-review") {
		t.Fatalf("vendored files drove the suggestion: %v", names)
	}
}
