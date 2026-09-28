// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The threat model points at the code that implements each control, so a
// reader can check a claim against the function it names. A pointer that names
// a line the file no longer has is worse than none: it sends the reader to
// the wrong place and reads as checked. The Makefile pointers were pinned by
// makefile_test.go; these are the rest of them.

// sourcePointer is a `path:line` or `path:line-line` reference to a Go source
// file or the Makefile, as written in backticks. Several pointers in one span
// carry bare line numbers after the first path (`reload.go:24,188-196`), so
// every number in the span is checked against that file.
var sourcePointer = regexp.MustCompile(
	"`([\\w./-]+\\.(?:go|py|sh)|Makefile):([\\d,-]+)`")

// TestThreatModelPointersResolve checks that every source pointer in
// docs/THREAT_MODEL.md names a file in the tree and a line range inside it.
func TestThreatModelPointersResolve(t *testing.T) {
	root := moduleRoot(t)
	doc := filepath.Join(root, "docs", "THREAT_MODEL.md")
	lines := fileLines(t, doc)
	if len(lines) == 0 {
		t.Fatal("docs/THREAT_MODEL.md is empty; the walk is broken, not the document")
	}
	qualified := qualifiedPointers(lines)
	targets := pointerTargets(t, root)
	depth := map[string]int{}
	checked := 0
	for i, text := range lines {
		for _, m := range sourcePointer.FindAllStringSubmatch(text, -1) {
			path := resolvePointer(t, root, m[1], qualified, targets)
			checked++
			n, seen := depth[path]
			if !seen {
				n = len(fileLines(t, path))
				depth[path] = n
			}
			for span := range strings.SplitSeq(m[2], ",") {
				first, last, err := pointerRange(span)
				if err != nil {
					t.Errorf("docs/THREAT_MODEL.md:%d: %s: %v", i+1, m[1], err)
					continue
				}
				if last > n {
					t.Errorf("docs/THREAT_MODEL.md:%d: %s points at %d-%d but %s has %d lines",
						i+1, m[1], first, last, path[len(root)+1:], n)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source pointer was found in docs/THREAT_MODEL.md; the walk is broken, not the document")
	}
}

// qualifiedPointers maps a file name to the one path the document spells with
// a directory. A bare name is ambiguous where two packages hold the same file
// name and the document qualifies both, so ambiguousNames says which one the
// bare name means.
var ambiguousNames = map[string]string{
	"reload.go": "internal/selfupdate/reload.go",
	"main.go":   "cmd/gauntlet/main.go",
}

func qualifiedPointers(lines []string) map[string]string {
	byName := map[string]map[string]bool{}
	for _, text := range lines {
		for _, m := range sourcePointer.FindAllStringSubmatch(text, -1) {
			if filepath.Dir(m[1]) == "." {
				continue
			}
			name := filepath.Base(m[1])
			if byName[name] == nil {
				byName[name] = map[string]bool{}
			}
			byName[name][m[1]] = true
		}
	}
	qualified := make(map[string]string, len(byName))
	for name, paths := range byName {
		if len(paths) == 1 {
			for p := range paths {
				qualified[name] = p
			}
			continue
		}
		if p, ok := ambiguousNames[name]; ok {
			qualified[name] = p
		}
	}
	return qualified
}

// pointerRange parses `N` or `N-M` into the two line numbers it names.
func pointerRange(span string) (int, int, error) {
	first, last, found := strings.Cut(span, "-")
	if !found {
		last = first
	}
	lo, err := strconv.Atoi(first)
	if err != nil || lo < 1 {
		return 0, 0, errBadPointer
	}
	hi, err := strconv.Atoi(last)
	if err != nil || hi < lo {
		return 0, 0, errBadPointer
	}
	return lo, hi, nil
}

var errBadPointer = errors.New("line pointer is not a range of positive integers")

// resolvePointer turns a pointer's path into one file in the tree. The
// document writes a path the way the surrounding prose does, sometimes from
// the root and sometimes from a package directory (`prompt/discover.go`), so a
// path with a directory is matched on its tail; a bare file name is resolved
// through the qualified name the document gives it elsewhere, then by looking
// in the command, in every internal package, and at the root.
func resolvePointer(t *testing.T, root, name string, qualified map[string]string, targets []string) string {
	t.Helper()
	var found []string
	for _, path := range targets {
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		if rel == name || strings.HasSuffix(rel, string(filepath.Separator)+name) {
			found = append(found, path)
		}
	}
	if len(found) > 1 {
		// Two packages here each hold a reload.go, and the document writes
		// the selfupdate one qualified in the same table it abbreviates
		// elsewhere: the qualified spelling settles which one is meant.
		if spelled, ok := qualified[name]; ok {
			for _, path := range found {
				rel := strings.TrimPrefix(path, root+string(filepath.Separator))
				if rel == spelled || strings.HasSuffix(rel, string(filepath.Separator)+spelled) {
					return path
				}
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0]
	case 0:
		t.Fatalf("docs/THREAT_MODEL.md points at %s, which is in no package in the tree", name)
	default:
		t.Fatalf("docs/THREAT_MODEL.md points at %s and it names %d files: %v; spell one of them with its directory", name, len(found), found)
	}
	return ""
}

// pointerTargets lists every file a pointer can name: the repository root, the
// command, the scripts directory, and each internal package.
func pointerTargets(t *testing.T, root string) []string {
	t.Helper()
	dirs := []string{root, filepath.Join(root, "scripts")}
	for _, parent := range []string{"internal", "cmd"} {
		dirs = append(dirs, filepath.Join(root, parent))
		entries, err := os.ReadDir(filepath.Join(root, parent))
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			if ent.IsDir() {
				dirs = append(dirs, filepath.Join(root, parent, ent.Name()))
			}
		}
	}
	var paths []string
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			paths = append(paths, filepath.Join(dir, f.Name()))
		}
	}
	return paths
}

func fileLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}
