// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/prompt"
)

// discover is a thin test helper around prompt.Discover.
func discover(t *testing.T, dir string) prompt.Set {
	t.Helper()
	set, _, err := prompt.Discover(context.Background(), dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// suggestHome points the journal at an empty tree, so a test judges the files
// in front of it and never the machine's own run history.
func suggestHome(t *testing.T) {
	t.Helper()
	t.Setenv("GAUNTLET_HOME", t.TempDir())
}

// frozenClock is the instant the suggester's churn window is measured back
// from. Fixed rather than wall time so a test judges the tree in front of it
// and not the date it happens to run on: a commit made "now" is inside any
// 90-day window, and one made long ago is outside any.
var frozenClock = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// reviews is Reviews for the cases that are not about its error: the
// history read is expected to succeed, so an error fails the test rather than
// passing unnoticed.
func reviews(t *testing.T, dir string, pool []string, set prompt.Set) []prompt.Suggestion {
	t.Helper()
	picked, err := Reviews(dir, pool, set, func() time.Time { return frozenClock })
	if err != nil {
		t.Fatalf("Reviews(%s): %v", dir, err)
	}
	return picked
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
		"ux-review", "test-review", "i18n-review", "mobile-review", "pkg-review",
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
			want:    []string{"code-review", "sec-review", "test-review", "pkg-review"},
			notWant: []string{"ux-review", "mobile-review", "db-review", "container-review"},
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
			for _, s := range reviews(t, tree(t, c.files...), pool, prompt.Set{}) {
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
	var names []string
	for _, s := range reviews(t, dir, []string{"code-review"}, prompt.Set{}) {
		if s.Name != "code-review" {
			t.Fatalf("%s is outside the pool", s.Name)
		}
		names = append(names, s.Name)
	}
	// The loop above also passes when Reviews returns nothing at all, so
	// the pool check needs a positive control: this tree is a Go program, and
	// code-review is in the pool.
	if len(names) != 1 {
		t.Fatalf("a Go tree proposed %v, want exactly code-review", names)
	}
}

// An empty directory justifies nothing, and says so by proposing nothing
// rather than falling back to everything.
func TestFastSuggestProposesNothingForAnEmptyTree(t *testing.T) {
	suggestHome(t)
	if got := reviews(t, t.TempDir(), []string{"code-review", "sec-review"}, prompt.Set{}); len(got) != 0 {
		t.Fatalf("an empty tree produced %v", got)
	}
}

// The walk stays out of dependency and build directories: what npm downloaded
// says nothing about the project under review.
func TestFastSuggestIgnoresVendoredTrees(t *testing.T) {
	dir := tree(t, "node_modules/react/index.tsx", "vendor/lib/thing.c", "README.md")
	var names []string
	for _, s := range reviews(t, dir, []string{"ux-review", "resource-review", "doc-review"}, prompt.Set{}) {
		names = append(names, s.Name)
	}
	if strings.Contains(strings.Join(names, ","), "ux-review") ||
		strings.Contains(strings.Join(names, ","), "resource-review") {
		t.Fatalf("vendored files drove the suggestion: %v", names)
	}
	// Only the README is outside the vendored trees, so it is the one signal
	// that has to survive: without it an empty proposal would pass.
	if !slices.Contains(names, "doc-review") {
		t.Fatalf("a tree with a README proposed %v, want doc-review", names)
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

	var names []string
	for _, s := range reviews(t, tree(t, files...), pool, prompt.Set{}) {
		names = append(names, s.Name)
		if strings.HasPrefix(s.Name, "ux") || strings.HasPrefix(s.Name, "a11y") ||
			strings.HasPrefix(s.Name, "webperf") {
			t.Fatalf("one .css file proposed %s (%s)", s.Name, s.Reason)
		}
	}
	// The absence checks pass on an empty proposal, so pin the one the Go
	// files alone must justify.
	if !slices.Contains(names, "code-review") {
		t.Fatalf("30 .go files proposed %v, want code-review", names)
	}
}

// Missing docs and source-tree tests justify coverage work. Missing tests or
// CI do not imply input parsing, release contracts, or specifications.
func TestFastSuggestReadsWhatIsMissing(t *testing.T) {
	pool := []string{"test-review", "doc-review", "build-review", "code-review", "fuzz-review", "release-review", "specs-review", "dst-review"}
	dir := tree(t, "main.go", "internal/app/app.go", "Makefile")
	got := map[string]string{}
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
		got[s.Name] = s.Reason
	}
	for _, want := range []string{"doc-review", "build-review", "test-review"} {
		if got[want] == "" {
			t.Errorf("%s was not proposed for a tree that has none of it", want)
		}
	}
	if !strings.Contains(got["doc-review"], "no documentation") {
		t.Errorf("doc-review's evidence was %q, which does not name the absence", got["doc-review"])
	}
	for _, no := range []string{"fuzz-review", "release-review", "specs-review", "dst-review"} {
		if got[no] != "" {
			t.Errorf("missing tests/CI proposed %s: %s", no, got[no])
		}
	}
}

func TestFastSuggestSeparatesReviewSubjects(t *testing.T) {
	pool := []string{"code-review", "perfectionism-review", "agentrules-review", "prompt-review", "skills-review", "container-review", "pkg-review", "infra-review", "cli-review", "ux-review", "api-review", "fuzz-review", "numerics-review", "specs-review"}
	cases := []struct {
		name                string
		files, want, absent []string
	}{
		{"instructions", []string{"AGENTS.md"}, []string{"agentrules-review"}, []string{"prompt-review", "skills-review", "code-review", "perfectionism-review"}},
		{"skill", []string{".claude/skills/example/SKILL.md"}, []string{"skills-review"}, []string{"agentrules-review", "prompt-review", "perfectionism-review"}},
		{"image", []string{"Dockerfile"}, []string{"pkg-review", "infra-review"}, []string{"container-review"}},
		{"CLI", []string{"main.py\x00import argparse\np = argparse.ArgumentParser()\n"}, []string{"cli-review", "perfectionism-review"}, []string{"ux-review", "fuzz-review", "numerics-review"}},
		{"HTTP client", []string{"main.go\x00package main\nimport \"net/http\"\nfunc main(){ http.Get(\"https://example.invalid\") }\n"}, []string{"code-review"}, []string{"api-review"}},
		{"C utility", []string{"main.c\x00int main(void) { return 0; }\n"}, []string{"code-review", "perfectionism-review"}, []string{"fuzz-review", "numerics-review", "specs-review"}},
		{"specification", []string{"docs/adr/ADR-001-storage.md"}, []string{"specs-review"}, []string{"code-review", "perfectionism-review"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, p := range reviews(t, tree(t, c.files...), pool, prompt.Set{}) {
				got[p.Name] = true
			}
			for _, n := range c.want {
				if !got[n] {
					t.Errorf("missing %s", n)
				}
			}
			for _, n := range c.absent {
				if got[n] {
					t.Errorf("unrelated %s selected", n)
				}
			}
		})
	}
}

func TestFastSuggestIgnoresTrackedDependenciesAndToolchains(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git required")
	}
	dir := tree(t, "main.go\x00package main\n",
		"vendor/copy/client.py\x00import fastapi\nimport anthropic\n",
		"third-party/copy/source.c\x00malloc(10);\n",
		"MSVC1500/include/windows.h\x00CreateThread();\n",
		".scratch/example.ts\x00import express from 'express';\n")
	commitAll(t, dir, "2026-03-01T12:00:00Z")
	pool := []string{"code-review", "api-review", "llm-review", "resource-review", "concurrency-review"}
	got := reviews(t, dir, pool, prompt.Set{})
	if len(got) != 1 || got[0].Name != "code-review" {
		t.Fatalf("tracked dependencies drove suggestions: %+v", got)
	}
}

func TestFastSuggestReadsPackageMetadata(t *testing.T) {
	dir := tree(t, "src/lib.py\x00def f(): pass\n", "setup.cfg\x00[metadata]\nname=example\n", "pyproject.toml\x00[project]\nname='example'\nversion = '1.0'\n[build-system]\nrequires=['setuptools']\n")
	got := map[string]bool{}
	for _, p := range reviews(t, dir, []string{"release-review", "pkg-review", "config-review"}, prompt.Set{}) {
		got[p.Name] = true
	}
	if !got["release-review"] || !got["pkg-review"] || got["config-review"] {
		t.Fatalf("package metadata was misclassified: %v", got)
	}
}

func TestFastSuggestUsesImportDeclarations(t *testing.T) {
	cases := []struct {
		name, file, want string
		absent           []string
	}{
		{"Go framework alias", "main.go\x00package main\nimport server \"github.com/gin-gonic/gin\"\nfunc main() {", "api-review", nil},
		{"Python framework", "main.py\x00from fastapi import FastAPI\napp = FastAPI()\n", "api-review", nil},
		{"JavaScript framework", "main.js\x00import { Hono } from 'hono';\nconst app = new Hono();\n", "api-review", nil},
		{"CommonJS provider", "main.js\x00const sdk = require('@anthropic-ai/sdk');\n", "llm-review", nil},
		{"Rust framework", "main.rs\x00use axum::{Router, routing};\n", "api-review", nil},
		{"Python cache alias", "main.py\x00import os, redis as cache_client\n", "cache-review", nil},
		{"Go comment", "main.go\x00package main\n// import \"github.com/gin-gonic/gin\"\n", "code-review", []string{"api-review"}},
		{"Python docstring", "main.py\x00\"\"\"Example only:\nimport openai\nfrom flask import Flask\n\"\"\"\nprint('hello')\n", "code-review", []string{"api-review", "llm-review"}},
		{"JavaScript comment", "main.js\x00/* Example only:\nimport openai from 'openai';\n*/\nconst text = 'anthropic';\n", "code-review", []string{"llm-review"}},
		{"Type-only framework", "main.ts\x00import type { Request } from 'express';\nexport type Input = Request;\n", "code-review", []string{"api-review"}},
	}
	pool := []string{"code-review", "api-review", "llm-review", "cache-review"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, p := range reviews(t, tree(t, c.file), pool, prompt.Set{}) {
				got[p.Name] = true
			}
			if !got[c.want] {
				t.Errorf("missing %s", c.want)
			}
			for _, n := range c.absent {
				if got[n] {
					t.Errorf("non-import evidence proposed %s", n)
				}
			}
		})
	}
}

func TestFastSuggestSeparatesPackageFieldsFromImplementation(t *testing.T) {
	cases := []struct {
		name, manifest string
		want, absent   []string
	}{
		{"dependency declarations", `{"description":"fastapi redis openai","dependencies":{"openai":"1","redis":"1","fastify":"1"}}`, []string{"deps-review"}, []string{"api-review", "llm-review", "cache-review", "release-review", "sdk-review"}},
		{"public exports", `{"version":"1.2.3","exports":{".":"./src/index.js"},"files":["src"],"bin":{"tool":"./src/cli.js"}}`, []string{"sdk-review", "pkg-review", "release-review", "cli-review"}, []string{"api-review", "llm-review"}},
		{"legacy type entry", `{"typings":"./index.d.ts"}`, []string{"sdk-review"}, []string{"cli-review"}},
		{"private package", `{"private":true,"types":"./src/index.d.ts","exports":{".":"./src/index.js"}}`, nil, []string{"sdk-review", "release-review"}},
		{"nested examples", `{"examples":{"version":"1","types":"T","exports":"example","files":["example"]}}`, nil, []string{"sdk-review", "pkg-review", "release-review"}},
		{"invalid target types", `{"exports":false,"bin":42}`, nil, []string{"sdk-review", "cli-review"}},
		{"truncated JSON", `{"version":"1","exports":{".":`, nil, []string{"sdk-review", "pkg-review", "release-review"}},
	}
	pool := []string{"deps-review", "sdk-review", "pkg-review", "release-review", "cli-review", "api-review", "llm-review", "cache-review"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]bool{}
			for _, p := range reviews(t, tree(t, "package.json\x00"+c.manifest), pool, prompt.Set{}) {
				got[p.Name] = true
			}
			for _, n := range c.want {
				if !got[n] {
					t.Errorf("missing %s", n)
				}
			}
			for _, n := range c.absent {
				if got[n] {
					t.Errorf("unrelated %s selected", n)
				}
			}
		})
	}
}

func TestFastSuggestDistinguishesArtifactVersionsFromLocalVariables(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{
		{"main.go\x00package main\nvar version = \"dev\"\nfunc main() {", true},
		{"main.go\x00package main\nfunc main() { version := \"1.0\"; _ = version }\n", false},
		{"main.go\x00package main\n// const version = \"1.0\"\n", false},
		{"main_test.go\x00package main\nconst version = \"1.0\"\n", false},
		{"version.py\x00__version__ = '1.2.3'\n", true},
		{"main.py\x00def f():\n    version = '1.2.3'\n", false},
		{"version.ts\x00export const VERSION = '1.2.3';\n", true},
	} {
		got := reviews(t, tree(t, c.file), []string{"release-review"}, prompt.Set{})
		if (len(got) > 0) != c.want {
			t.Errorf("%q: release selection %v, want %v", c.file, got, c.want)
		}
	}
}

func TestFastSuggestRecognizesImportableGoLibraryInterfaces(t *testing.T) {
	for _, c := range []struct {
		file string
		want bool
	}{
		{"client.go\x00package client\nfunc NewClient() {}\n", true},
		{"main.go\x00package main\nfunc Run() {}\n", false},
		{"internal/client/client.go\x00package client\nfunc NewClient() {}\n", false},
		{"client_test.go\x00package client\nfunc NewFixture() {}\n", false},
		{"client.go\x00package client\n// func NewClient() {}\nfunc local() {}\n", false},
	} {
		got := reviews(t, tree(t, c.file), []string{"sdk-review"}, prompt.Set{})
		if (len(got) > 0) != c.want {
			t.Errorf("%q: SDK selection %v, want %v", c.file, got, c.want)
		}
	}
	// A library-shaped path and a library-shaped entry point are two
	// separate facts, and the rule takes either the declared mark or both of
	// them. Each half on its own is not the rule.
	for _, c := range []struct {
		files []string
		want  bool
	}{
		{[]string{"pkg/setup.py\x00from setuptools import setup\n"}, true},
		{[]string{"pkg/thing.go\x00package pkg\nfunc Run() {}\n"}, false},
		{[]string{"src/pkg/lib.rs\x00pub fn new_client() {}\n"}, true},
		{[]string{"src/setup.py\x00import os\n"}, false},
	} {
		got := reviews(t, tree(t, c.files...), []string{"sdk-review"}, prompt.Set{})
		if (len(got) > 0) != c.want {
			t.Errorf("%v: SDK selection %v, want %v", c.files, got, c.want)
		}
	}
}

func TestDeclaredMarksStayLiteralBesideStructuredSignals(t *testing.T) {
	dir := tree(t, "main.go\x00package main\nimport \"github.com/gin-gonic/gin\"\n", "package.json\x00{\"description\":\"rediscover modules\"}\n")
	promptDir := t.TempDir()
	for name, body := range map[string]string{
		"literal-http-review.md":  "Signals: mark:http\n\nInspect literal http.\n",
		"literal-redis-review.md": "Signals: mark:redis\n\nInspect literal redis.\n",
	} {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set := discover(t, promptDir)
	got := reviews(t, dir, []string{"literal-http-review", "literal-redis-review"}, set)
	if len(got) != 1 || got[0].Name != "literal-redis-review" {
		t.Fatalf("structured subjects replaced literal signals: %+v", got)
	}
}

// A review draws more reasons than reasonsShown, and the printed line is the
// only explanation it gets, so it has to lead with the evidence worth most
// rather than whichever rule the table reached first.
func TestFastSuggestLeadsWithTheStrongestEvidence(t *testing.T) {
	pool := []string{"db-review", "test-review", "doc-review", "build-review", "code-review"}
	dir := tree(t,
		"go.mod\x00module x\n",
		"main.go\x00import \"database/sql\"\n",
		"main_test.go\x00package main\n",
	)
	var reason string
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
		if s.Name == "db-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("db-review was not proposed for a tree that opens a database")
	}
	if !strings.HasPrefix(reason, "database access in the source") {
		t.Errorf("db-review's evidence was %q, which does not lead with the strong rule", reason)
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
	for _, s := range reviews(t, dir, pool, prompt.Set{}) {
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
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"zig-idiomatic-review"}, set) {
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

// `mark:` is documented as "a substring found near the top of a source file",
// and the example both docs/RUNS.md and prompt.Signals give is `mark:comptime`.
// It could not work: peek only ever recorded the built-in table's category
// labels, so a declared value matched only by colliding with one of those, and
// `comptime` is not one. A review declaring it was silently unreachable
// through --suggest-agent gauntlet, which is the one way a project's own
// prompt gets proposed at all.
func TestFastSuggestFindsASubstringAReviewDeclares(t *testing.T) {
	dir := tree(t, "src/main.zig\x00const std = @import(\"std\");\n\ncomptime {}\n")
	promptDir := t.TempDir()
	body := "Signals: mark:comptime\n\nYour goal is to review comptime code.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "comptime-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"comptime-review"}, set) {
		if s.Name == "comptime-review" {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("a review declaring mark:comptime was not proposed for a tree containing it")
	}
	if !strings.Contains(reason, "mark:comptime") {
		t.Errorf("evidence was %q, which does not name the signal that matched", reason)
	}
}

// The control: a declared substring the tree does not carry proposes nothing.
// Without this, a suggester that matched every declared mark would pass the
// test above and be no better than the bug.
func TestFastSuggestIgnoresASubstringTheTreeLacks(t *testing.T) {
	dir := tree(t, "src/main.zig\x00const std = @import(\"std\");\n")
	promptDir := t.TempDir()
	body := "Signals: mark:comptime\n\nYour goal is to review comptime code.\n"
	if err := os.WriteFile(filepath.Join(promptDir, "comptime-review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := discover(t, promptDir)
	for _, s := range reviews(t, dir, []string{"comptime-review"}, set) {
		if s.Name == "comptime-review" && strings.Contains(s.Reason, "mark:comptime") {
			t.Fatalf("claimed a mark the tree does not carry: %q", s.Reason)
		}
	}
}

// The built-in table keeps working beside the declared ones, and a declared
// value that repeats one of its labels does not double-count.
func TestMarkSearchAddsDeclaredWithoutDisturbingTheTable(t *testing.T) {
	base := len(marks)
	got := markSearch([]string{"comptime", "comptime", "", "borrow"})
	if len(got) != base+2 {
		t.Fatalf("added %d entries for 2 distinct values", len(got)-base)
	}
	if len(marks) != base {
		t.Fatal("markSearch wrote into the package-level table")
	}
	// matchDeclared looks up the literal substring in the mark: namespace,
	// independently of the built-in capability categories.
	for i, e := range got[base:] {
		if e.says != "mark:borrow" && e.says != "mark:comptime" {
			t.Fatalf("declared entry %d is labelled %q, want the value it was declared with", i, e.says)
		}
	}
	many := make([]string, declaredMarkMax*2)
	for i := range many {
		many[i] = fmt.Sprintf("m%03d", i)
	}
	if got := markSearch(many); len(got) != base+declaredMarkMax {
		t.Fatalf("%d declared values became %d entries, want the %d cap",
			len(many), len(got)-base, declaredMarkMax)
	}
}

func TestBuiltinMarksRespectIdentifiers(t *testing.T) {
	cases := []struct {
		head, kind string
		want       bool
	}{
		{"plugin.addArg(\"-I\")", "http", false},
		{"origin.host", "http", false},
		{"plugin.addArg(); gin.Default()", "http", true},
		{"http.HandleFunc(\"/\", handler)", "http", true},
		{"http.ListenAndServeTLS(addr, cert, key, handler)", "http", true},
		{"rediscover cached details", "cache", false},
		{"import redis\nredis.Redis()", "cache", true},
		{"this function recurses", "tui", false},
		{"#include <ncurses.h>", "tui", true},
		{"line = file.readline()", "tui", false},
		{"from rich.console import Console", "tui", false},
		{"from rich.live import Live", "tui", true},
		{"readline.createInterface({input: process.stdin})", "tui", true},
		{"from psycopg2 import connect", "sql", true},
		{"CreateWindowExW(...)", "gui", true},
		{"pthread_create(...)", "concurrent", true},
		{"await Promise.allSettled(tasks)", "concurrent", true},
		{"redish redis; redis.Redis()", "cache", true},
		{"红redis", "cache", false},
	}
	for _, c := range cases {
		s := signals{mark: map[string]int{}}
		markFound(&s, markSearch([]string{"redis"}), asciiFold(nil, []byte(c.head)))
		if got := s.anyMark(c.kind); got != c.want {
			t.Errorf("%q: %s = %v, want %v", c.head, c.kind, got, c.want)
		}
		if strings.Contains(strings.ToLower(c.head), "redis") && !s.anyMark("mark:redis") {
			t.Errorf("declared literal redis did not match %q", c.head)
		}
	}
}

// A declared mark is stored NFC+lower. File contents are not: macOS editors
// write NFD, and asciiFold leaves non-ASCII capitals alone. Every spelling of
// the same word in a source head must still match the signal.
func TestFastSuggestMatchesDeclaredMarkSpelling(t *testing.T) {
	for _, spelling := range []struct{ head, what string }{
		{"cafe\u0301", "an NFD spelling of café"},
		{"CAFÉ", "CAFÉ"},
	} {
		t.Run(spelling.what, func(t *testing.T) {
			dir := tree(t, "src/main.go\x00package main\n// "+spelling.head+" notes\n")
			promptDir := t.TempDir()
			body := "Signals: mark:caf\u00e9\n\nYour goal is to review café notes.\n"
			if err := os.WriteFile(filepath.Join(promptDir, "cafe-review.md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			set := discover(t, promptDir)

			var reason string
			for _, s := range reviews(t, dir, []string{"cafe-review"}, set) {
				if s.Name == "cafe-review" {
					reason = s.Reason
				}
			}
			if reason == "" {
				t.Fatalf("%s in a source file never matched mark:café", spelling.what)
			}
			if !strings.Contains(reason, "mark:café") {
				t.Errorf("evidence was %q, which does not name the mark that matched", reason)
			}
		})
	}
}

// A macOS tree hands out NFD filenames while an author types NFC into the
// Signals: line of a prompt. Both sides are stored NFC (record normalizes
// what it receives, prompt.Signals normalizes what the author declared), so
// the same word spelled in two forms still matches.
func TestFastSuggestMatchesSignalsAcrossNormalizationForms(t *testing.T) {
	suggestHome(t)
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
	set := discover(t, promptDir)

	var reason string
	for _, s := range reviews(t, dir, []string{"cafe-review"}, set) {
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
	for _, s := range reviews(t, dir, []string{"sec-review", "test-review", "code-review"}, prompt.Set{}) {
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
	if err := os.WriteFile(outside, []byte("package x\nhttp.HandleFunc(\"/\", handler)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "http.go"), []byte("package main\nhttp.HandleFunc(\"/\", handler)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := signals{mark: map[string]int{}}
	if err := peek(dir, []string{"http.go"}, &in, nil); err != nil {
		t.Fatalf("peek on a readable tree: %v", err)
	}
	if in.mark["http"] == 0 {
		t.Fatal("peek missed an in-tree HTTP handler")
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
		if err := peek(dir, []string{"main.go", "evil.go", "pipe.go", escape}, &s, nil); err != nil {
			t.Errorf("peek on a readable tree: %v", err)
		}
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

// A tree that cannot be listed is a failure the operator has to hear about.
// Reporting it as an empty tree is worse than reporting nothing: the file
// rules then find nothing, the caller's "no review matched anything in this
// tree" reads as a verdict on the code, and the one thing the operator could
// do about it, looking at the path, is never named.
func TestFastSuggestReportsATreeItCannotRead(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-tree")
	if _, err := Reviews(missing, []string{"code-review"}, prompt.Set{}, func() time.Time { return frozenClock }); err == nil {
		t.Fatal("a tree that cannot be listed was reported as one with nothing in it")
	}
}

// An index that cannot be read is a failure the operator has to hear about:
// every review then weighs as untried here, and the suggester re-proposes the
// ones that keep finishing without changing a line. The file evidence still
// stands, so the picks come back beside the error rather than instead of it.
func TestFastSuggestReportsAJournalItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a 0000 file anyway, so the failure cannot be provoked here")
	}
	dir := tree(t, "main.go\x00package main\n// bcrypt database/sql unsafe.Pointer exec.Command\n")
	home := t.TempDir()
	t.Setenv("GAUNTLET_HOME", home)
	// An index nobody can read: the shape a state tree another account owns,
	// or one a crashed run left half-written, takes.
	if err := os.WriteFile(filepath.Join(home, "index.jsonl"), nil, 0o000); err != nil {
		t.Fatal(err)
	}

	picked, err := Reviews(dir, []string{"sec-review"}, prompt.Set{}, func() time.Time { return frozenClock })
	if err == nil {
		t.Fatal("an unreadable journal was swallowed")
	}
	if len(picked) == 0 {
		t.Fatal("the tree evidence was thrown away with the journal")
	}
	if picked[0].Weight != 1 {
		t.Fatalf("an incomplete read proposed %d passes, want one", picked[0].Weight)
	}
}

// TestScanChurnWindowReadsTheInjectedClock pins the property the cutoff exists
// for: the same tree scanned under two different clocks sees the history the
// caller asked for, not whatever git thinks is recent. The window reaches back
// from the clock, so a clock a day past the commit still counts it and a clock
// a year past it does not, and only a cutoff the caller computed can answer
// both.
func TestScanChurnWindowReadsTheInjectedClock(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is required to read churn")
	}
	dir := tree(t, "main.go\x00package main\n")
	commit := commitAll(t, dir, "2026-03-01T12:00:00Z")

	after, err := scan(dir, nil, func() time.Time { return commit.Add(24 * time.Hour) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !after.churn {
		t.Fatal("a commit a day old is not churn")
	}
	// The window reaches back from the clock, so a clock a year past the
	// commit puts its cutoff beyond it and the history reads as dormant.
	before, err := scan(dir, nil, func() time.Time { return commit.Add(365 * 24 * time.Hour) })
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if before.churn {
		t.Fatal("a commit a year before the clock's window is still churn")
	}
}

// commitAll makes the tree's single commit carry a stated date, so the test
// decides where the churn window's edge falls rather than when it happens to
// run.
func commitAll(t *testing.T, dir, date string) time.Time {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	git("add", "-A")
	cmd := exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	at, err := time.Parse(time.RFC3339, date)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// Repeats follow independent evidence, not the number of matching files.
// Past clean runs and incomplete scans must not spend extra passes.
func TestFastSuggestPassWeights(t *testing.T) {
	cases := []struct {
		name, body                  string
		runs, changed, copies, want int
	}{
		{"basic source", "package main\n", 0, 0, 1, 1},
		{"one implemented subject", "package main\n// bcrypt\n", 0, 0, 1, 2},
		{"same subject in many files", "package main\n// bcrypt\n", 0, 0, 20, 2},
		{"several security-sensitive subjects", "package main\n// bcrypt database/sql unsafe.Pointer exec.Command\n", 0, 0, 1, 3},
		{"compound evidence", "package main\n// bcrypt database/sql unsafe.Pointer\n", 0, 0, 1, 2},
		{"productive history", "package main\n// bcrypt database/sql unsafe.Pointer\n", 3, 3, 1, 3},
		{"unproductive history", "package main\n// bcrypt database/sql unsafe.Pointer exec.Command\n", 3, 0, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := make([]string, c.copies)
			for i := range files {
				files[i] = fmt.Sprintf("part%d.go\x00%s", i, c.body)
			}
			dir := tree(t, files...)
			if c.runs > 0 {
				home := t.TempDir()
				t.Setenv("GAUNTLET_HOME", home)
				writeHistory(t, home, dir, "sec-review", c.runs, c.changed)
			}
			got := reviews(t, dir, []string{"sec-review"}, prompt.Set{})
			if len(got) != 1 || got[0].Weight != c.want {
				t.Fatalf("picks %+v, want one security review with %d passes", got, c.want)
			}
		})
	}

	t.Run("failed churn read", func(t *testing.T) {
		if !gitx.Available() {
			t.Skip("git is required to provoke an unborn branch")
		}
		dir := tree(t, "main.go\x00package main\n// bcrypt database/sql unsafe.Pointer exec.Command\n")
		cmd := exec.Command("git", "init", "-q", "-b", "main")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		got, err := Reviews(dir, []string{"sec-review"}, prompt.Set{}, func() time.Time { return frozenClock })
		if err == nil || len(got) != 1 || got[0].Weight != 1 {
			t.Fatalf("failed churn read returned %+v, %v; want one pass plus the read error", got, err)
		}
	})
}
