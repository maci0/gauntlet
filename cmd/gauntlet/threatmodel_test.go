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
// file, a script, a workflow, or the Makefile, as written in backticks. Several
// pointers in one span carry bare line numbers after the first path
// (`reload.go:24,188-196`), so every number in the span is checked against that
// file. The workflow files are here for the same reason as the rest: a pointer
// into `.github/workflows` is a claim about the release pipeline, and it drifts
// when a step is added above the one it names.
var sourcePointer = regexp.MustCompile(
	"`([\\w./-]+\\.(?:go|py|sh|yml)|Makefile):([\\d,-]+)`")

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

// A line range inside the file is not the claim the document makes. Three
// citations into release.yml sat well inside it while naming steps other than
// the ones they describe: a step added above them moved what a reader lands
// on, and TestThreatModelPointersResolve reports nothing, because a range that
// fits the file is a range, not the right one. Every workflow citation is
// therefore a case below, keyed by the file and the line it names, holding
// the text that line's range must contain. A citation with no case is a gap in
// the list, not a pass.
var workflowPointer = regexp.MustCompile("`(?:[\\w./-]*/)?([\\w-]+\\.yml):([\\d,-]+)`")

func TestThreatModelWorkflowPointersNameTheStep(t *testing.T) {
	root := moduleRoot(t)
	doc := strings.Join(fileLines(t, filepath.Join(root, "docs", "THREAT_MODEL.md")), "\n")
	cases := []struct {
		file, want string
		at         int
	}{
		{"release.yml", "git merge-base --is-ancestor HEAD origin/main", 114},
		{"release.yml", "actions/attest-build-provenance@", 175},
		{"release.yml", "dist/sbom.json", 208},
		{"release.yml", "set -eu -o pipefail", 74},
		{"release.yml", "make smoke VERSION=", 159},
		{"ci.yml", "make smoke VERSION=ci", 205},
		{"ci.yml", "make smoke VERSION=ci", 254},
		{"ci.yml", "GITHUB_TOKEN", 49},
		{"vulnscan.yml", "schedule:", 11},
		{"vulnscan.yml", "GITHUB_TOKEN", 45},
		{"vulnscan.yml", "run: make vuln", 36},
	}
	dir := filepath.Join(root, ".github", "workflows")
	covered := map[string]bool{}
	var cited []string
	for _, m := range workflowPointer.FindAllStringSubmatchIndex(doc, -1) {
		file, span := doc[m[2]:m[3]], doc[m[4]:m[5]]
		spans, err := pointerRanges(span)
		if err != nil {
			t.Errorf("docs/THREAT_MODEL.md cites %s:%s: %v", file, span, err)
			continue
		}
		lines := fileLines(t, filepath.Join(dir, file))
		for _, r := range spans {
			if r.last > len(lines) {
				t.Errorf("docs/THREAT_MODEL.md cites %s:%d-%d, which has %d lines",
					file, r.first, r.last, len(lines))
				continue
			}
		}
		first := spans[0].first
		key := file + ":" + strconv.Itoa(first)
		want, known := "", false
		for _, tc := range cases {
			if tc.file == file && tc.at == first {
				want, known = tc.want, true
				covered[key] = true
			}
		}
		if !known {
			t.Errorf("docs/THREAT_MODEL.md cites %s and no case names what it points at; add one", key)
			continue
		}
		// Every range the citation names, not just its first: a step the
		// document points at in its second range is as movable as one in the
		// first, and checking only the first would leave it unchecked.
		for _, r := range spans {
			if r.last > len(lines) {
				continue
			}
			got := strings.Join(lines[r.first-1:r.last], "\n")
			if !strings.Contains(got, want) {
				t.Errorf("docs/THREAT_MODEL.md points at %s (%d-%d), which reads %q, not %q",
					key, r.first, r.last, got, want)
			}
		}
		cited = append(cited, key)
	}
	if len(cited) == 0 {
		t.Fatal("no workflow pointer was found in docs/THREAT_MODEL.md; the walk is broken, not the document")
	}
	for _, tc := range cases {
		if key := tc.file + ":" + strconv.Itoa(tc.at); !covered[key] {
			t.Errorf("docs/THREAT_MODEL.md no longer cites %s; drop the case or cite it", key)
		}
	}
}

// qualifiedPointers maps a file name to the one path the document spells with
// a directory. A bare name is ambiguous where two packages hold the same file
// name and the document qualifies both, so ambiguousNames says which one the
// bare name means. exec.go is that case for the agent-side file: the tables
// name internal/runner/exec.go for the child-process plumbing and spell the
// git one internal/gitx/exec.go wherever it points at git's own execs.
var ambiguousNames = map[string]string{
	"reload.go": "internal/selfupdate/reload.go",
	"main.go":   "cmd/gauntlet/main.go",
	"exec.go":   "internal/runner/exec.go",
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

type lineRange struct{ first, last int }

// pointerRanges splits a citation's comma-separated spans, so a pointer
// naming more than one range has all of them checked rather than the first.
func pointerRanges(span string) ([]lineRange, error) {
	var out []lineRange
	for part := range strings.SplitSeq(span, ",") {
		lo, hi, err := pointerRange(part)
		if err != nil {
			return nil, err
		}
		out = append(out, lineRange{lo, hi})
	}
	return out, nil
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
// command, the scripts directory, the workflows, and each internal package.
func pointerTargets(t *testing.T, root string) []string {
	t.Helper()
	dirs := []string{root, filepath.Join(root, "scripts"), filepath.Join(root, ".github", "workflows")}
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
