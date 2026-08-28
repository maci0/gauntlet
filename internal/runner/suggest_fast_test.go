// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/prompt"
)

// suggestHome points the journal at an empty tree, so a test judges the files
// in front of it and never the machine's own run history.
func suggestHome(t *testing.T) {
	t.Helper()
	t.Setenv("GAUNTLET_HOME", t.TempDir())
}

// tree writes a set of files, creating the directories they need. A file may
// carry content as "path\x00body"; without one it gets a byte.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	suggestHome(t)
	dir := t.TempDir()
	for _, f := range files {
		f, body, ok := strings.Cut(f, "\x00")
		if !ok {
			body = "x\n"
		}
		path := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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
			for _, s := range fastSuggest(tree(t, c.files...), pool, prompt.Set{}) {
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
	for _, s := range fastSuggest(dir, []string{"code-review"}, prompt.Set{}) {
		if s.Name != "code-review" {
			t.Fatalf("%s is outside the pool", s.Name)
		}
	}
}

// An empty directory justifies nothing, and says so by proposing nothing
// rather than falling back to everything.
func TestFastSuggestProposesNothingForAnEmptyTree(t *testing.T) {
	suggestHome(t)
	if got := fastSuggest(t.TempDir(), []string{"code-review", "sec-review"}, prompt.Set{}); len(got) != 0 {
		t.Fatalf("an empty tree produced %v", got)
	}
}

// The walk stays out of dependency and build directories: what npm downloaded
// says nothing about the project under review.
func TestFastSuggestIgnoresVendoredTrees(t *testing.T) {
	dir := tree(t, "node_modules/react/index.tsx", "vendor/lib/thing.c", "README.md")
	var names []string
	for _, s := range fastSuggest(dir, []string{"ux-review", "resource-review", "doc-review"}, prompt.Set{}) {
		names = append(names, s.Name)
	}
	if strings.Contains(strings.Join(names, ","), "ux-review") ||
		strings.Contains(strings.Join(names, ","), "resource-review") {
		t.Fatalf("vendored files drove the suggestion: %v", names)
	}
}

// Presence is not proportion: one stylesheet in a Go repository is not a
// frontend, and used to light up five frontend reviews.
func TestFastSuggestWeighsHowMuchOfATreeAThingIs(t *testing.T) {
	var files []string
	for i := range 30 {
		files = append(files, filepath.Join("internal", "pkg", "f"+string(rune('a'+i))+".go"))
	}
	files = append(files, "docs/theme.css")
	pool := []string{"code-review", "ux-review", "a11y-review", "webperf-review"}

	for _, s := range fastSuggest(tree(t, files...), pool, prompt.Set{}) {
		if strings.HasPrefix(s.Name, "ux") || strings.HasPrefix(s.Name, "a11y") ||
			strings.HasPrefix(s.Name, "webperf") {
			t.Fatalf("one .css file proposed %s (%s)", s.Name, s.Reason)
		}
	}
}

// What is missing is evidence too: a tree with no tests is the strongest case
// for the review that would add them, and presence-only rules said the reverse.
func TestFastSuggestReadsWhatIsMissing(t *testing.T) {
	pool := []string{"test-review", "doc-review", "build-review", "code-review"}
	dir := tree(t, "main.go", "internal/app/app.go")
	got := map[string]string{}
	for _, s := range fastSuggest(dir, pool, prompt.Set{}) {
		got[s.Name] = s.Reason
	}
	for _, want := range []string{"test-review", "doc-review", "build-review"} {
		if got[want] == "" {
			t.Errorf("%s was not proposed for a tree that has none of it", want)
		}
	}
	if !strings.Contains(got["test-review"], "no tests") {
		t.Errorf("test-review's evidence was %q, which does not name the absence", got["test-review"])
	}
}

// Directory names are a guess about a codebase; what it imports is a fact.
func TestFastSuggestReadsInsideFiles(t *testing.T) {
	pool := []string{
		"code-review", "concurrency-review", "db-review", "time-review",
		"o11y-review", "cache-review", "llm-review", "idempotency-review",
	}
	dir := tree(t,
		"svc/worker.py\x00import asyncio\nfrom sqlalchemy import text\nimport redis\n",
		"svc/clock.py\x00from datetime import datetime\nx = datetime.now()\n",
		"svc/obs.py\x00from prometheus_client import Counter\n",
		"svc/agent.py\x00import anthropic\n",
		"svc/queue.py\x00def retry(): ...\n# idempotency key\n",
	)
	got := map[string]bool{}
	for _, s := range fastSuggest(dir, pool, prompt.Set{}) {
		got[s.Name] = true
	}
	for _, want := range []string{
		"concurrency-review", "db-review", "time-review",
		"o11y-review", "cache-review", "llm-review", "idempotency-review",
	} {
		if !got[want] {
			t.Errorf("%s was not proposed for source that plainly calls it", want)
		}
	}
}

// A project's own review is unreachable through the built-in rules, which know
// only built-in names. Declaring signals is how it becomes suggestable.
func TestFastSuggestHonorsSignalsAPromptDeclares(t *testing.T) {
	dir := tree(t, "src/main.zig", "build.zig")
	promptDir := t.TempDir()
	body := "You are a Zig reviewer.\n\nSignals: ext:.zig, name:build.zig\n\nYour goal is to review Zig.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "zig-idiomatic-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err := Discover(t, promptDir)
	if err != nil {
		t.Fatal(err)
	}

	var reason string
	for _, s := range fastSuggest(dir, []string{"zig-idiomatic-review"}, set) {
		if s.Name == "zig-idiomatic-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("a review that declared its own signals was not proposed")
	}
	if !strings.Contains(reason, "ext:.zig") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// A macOS tree hands out NFD filenames while an author types NFC into the
// Signals: line of a prompt. Both sides are stored NFC (record normalizes
// what it receives, prompt.Signals normalizes what the author declared), so
// the same word spelled in two forms still matches.
func TestFastSuggestMatchesSignalsAcrossNormalizationForms(t *testing.T) {
	dir := t.TempDir()
	nfd := "cafe\u0301-notes.md" // decomposed é, as a Mac filesystem spells it
	if norm.NFC.String(nfd) == nfd {
		t.Fatal("fixture is not decomposed; it proves nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, nfd), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promptDir := t.TempDir()
	body := "Signals: name:caf\u00e9-notes.md\n\nYour goal is to review caf\u00e9 notes.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "cafe-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err := Discover(t, promptDir)
	if err != nil {
		t.Fatal(err)
	}

	var reason string
	for _, s := range fastSuggest(dir, []string{"cafe-review"}, set) {
		if s.Name == "cafe-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("an NFD filename never matched its NFC-declared signal")
	}
	if !strings.Contains(reason, "name:") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// A review that has finished here several times without changing a line is a
// bad pick for this directory, whatever the files say.
func TestFastSuggestLearnsFromPastRunsInThisDirectory(t *testing.T) {
	dir := tree(t, "main.go", "main_test.go")
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	writeHistory(t, home, dir, "sec-review", 4, 0)
	writeHistory(t, home, dir, "test-review", 4, 4)

	var order []string
	for _, s := range fastSuggest(dir, []string{"sec-review", "test-review", "code-review"}, prompt.Set{}) {
		order = append(order, s.Name)
	}
	if len(order) == 0 || order[0] != "test-review" {
		t.Errorf("the review that keeps finding work here did not rank first: %v", order)
	}
	if len(order) == 0 || order[len(order)-1] != "sec-review" {
		t.Errorf("the review that never changes anything here did not rank last: %v", order)
	}
}

// writeHistory fakes runs in a GAUNTLET_HOME: n finished reviews in dir, of
// which changed left lines behind.
func writeHistory(t *testing.T, home, dir, review string, n, changed int) {
	t.Helper()
	runs := filepath.Join(home, "runs", "2026-01-01")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	index, err := os.OpenFile(filepath.Join(home, "index.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	for i := range n {
		runID := "20260101T00000" + string(rune('0'+i)) + "Z-" + review[:3]
		path := filepath.Join(runs, runID+".jsonl")
		ev := map[string]any{"ev": "review_end", "dir": dir, "review": review, "status": "ok"}
		if i < changed {
			ev["ins"], ev["del"] = 10, 2
		}
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		row, err := json.Marshal(map[string]any{"run_id": runID, "path": path, "dirs": []string{dir}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := index.Write(append(row, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// peek reads heads from the reviewed tree. A symlink, a FIFO, or a path that
// walks out of it must not contribute marks, and must not block the scan.
func TestPeekStaysInsideTheTree(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(outside, []byte("package x\nimport \"net/http\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "http.go"), []byte("package main\nimport \"net/http\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := signals{mark: map[string]int{}}
	peek(dir, []string{"http.go"}, &in)
	if in.mark["http"] == 0 {
		t.Fatal("peek missed an in-tree net/http import")
	}

	if err := os.Symlink(outside, filepath.Join(dir, "evil.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.go"), 0o644); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}

	s := signals{mark: map[string]int{}}
	escape := filepath.Join("..", filepath.Base(outsideDir), "secret.go")
	done := make(chan struct{})
	go func() {
		peek(dir, []string{"main.go", "evil.go", "pipe.go", escape}, &s)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("peek blocked on a fifo or an escaping path")
	}
	if s.mark["http"] > 0 {
		t.Fatal("peek followed a symlink or escaped the tree")
	}
}
