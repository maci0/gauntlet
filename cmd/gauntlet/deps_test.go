// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Direct modules in go.mod are the supply-chain surface this repository
// chose. An unused one still downloads, still hashes, and still sits in the
// module graph; dropping it is the fix, and this test is how a leftover is
// found before it ships. Test files do not count: a module imported only
// from _test.go is a test dependency listed as production.
func TestDirectModulesAreImported(t *testing.T) {
	root := moduleRoot(t)
	direct := directModules(t, root)
	if len(direct) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	used := importedModules(t, root, direct)
	for _, path := range direct {
		if !used[path] {
			t.Errorf("go.mod requires %s, but no non-test .go file imports it; remove the unused module", path)
		}
	}
}

// directModuleSites is the "Contained by" column in docs/DESIGN.md. A new
// direct module must appear here with the packages allowed to import it;
// an import outside those prefixes is the coupling that column exists to
// prevent.
var directModuleSites = map[string][]string{
	"github.com/charmbracelet/bubbletea": {"internal/ui/"},
	"github.com/charmbracelet/lipgloss":  {"internal/ui/"},
	"github.com/muesli/termenv":          {"internal/ui/"},
	"github.com/maci0/toktop":            {"cmd/gauntlet/", "internal/runner/"},
	"github.com/rivo/uniseg":             {"cmd/gauntlet/", "internal/ui/"},
	"golang.org/x/text":                  {"internal/fuzzy/", "internal/prompt/", "internal/runner/", "internal/ui/"},
	"golang.org/x/term":                  {"cmd/gauntlet/"},
}

// TestDirectModuleImportSites fails when a direct module is imported from
// a package docs/DESIGN.md does not allow, when a new direct module has no
// containment entry, or when an entry outlives the require. toktop is
// extra-constrained: every import site must carry a notoktop build tag, or
// `-tags notoktop` would not drop it.
func TestDirectModuleImportSites(t *testing.T) {
	root := moduleRoot(t)
	direct := directModules(t, root)
	for _, path := range direct {
		if _, ok := directModuleSites[path]; !ok {
			t.Errorf("go.mod requires %s, but directModuleSites has no allowed import prefixes; add the docs/DESIGN.md containment", path)
		}
	}
	for path := range directModuleSites {
		if !slices.Contains(direct, path) {
			t.Errorf("directModuleSites lists %s, which is not a direct module; drop the stale entry", path)
		}
	}

	fset := token.NewFileSet()
	err := walkGoFiles(root, func(rel, path string) error {
		body := readRepoFile(t, path)
		f, err := parser.ParseFile(fset, path, body, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for mod, prefixes := range directModuleSites {
				if imp != mod && !strings.HasPrefix(imp, mod+"/") {
					continue
				}
				ok := false
				for _, p := range prefixes {
					if strings.HasPrefix(rel, p) {
						ok = true
						break
					}
				}
				if !ok {
					t.Errorf("%s imports %s; docs/DESIGN.md allows that module in %s", rel, mod, strings.Join(prefixes, ", "))
				}
				if mod == "github.com/maci0/toktop" && !fileHasBuildTag(body, "notoktop") {
					t.Errorf("%s imports toktop without a notoktop build tag; -tags notoktop would not drop it", rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A direct module pinned to a pseudo-version is an unpublished commit in
// the production graph. Transitives inherit whatever their parents asked
// for; the modules this repository chose must be tagged releases.
func TestDirectModulesAreTagged(t *testing.T) {
	root := moduleRoot(t)
	reqs := directReqs(t, root)
	if len(reqs) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	for _, r := range reqs {
		if pseudoVersion.MatchString(r.version) {
			t.Errorf("go.mod requires %s %s, a pseudo-version; pin a tagged release", r.path, r.version)
		}
	}
}

// docs/DESIGN.md says every linked module is MIT or BSD-3-Clause. Direct
// modules are the ones this repository adopted; their LICENSE files are
// the check that claim runs against, before a new require lands.
func TestDirectModuleLicenses(t *testing.T) {
	root := moduleRoot(t)
	reqs := directReqs(t, root)
	if len(reqs) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	dirs := moduleDirs(t, root, reqs)
	for _, r := range reqs {
		dir := dirs[r.path]
		if dir == "" {
			t.Errorf("%s: go list -m did not report a module directory", r.path)
			continue
		}
		body := readLicense(t, dir, r.path)
		switch kind := licenseKind(body); kind {
		case "MIT", "BSD-3-Clause":
		default:
			t.Errorf("%s license is %s, not MIT or BSD-3-Clause; docs/DESIGN.md requires a check before adoption", r.path, kind)
		}
	}
}

// sqlite and klauspost/compress are not imported here; they link because
// toktop does. docs/DESIGN.md has to name them or the graph they add looks
// like an accident.
func TestDesignDocumentsToktopTransitives(t *testing.T) {
	text := readRepoFile(t, filepath.Join(moduleRoot(t), "docs", "DESIGN.md"))
	for _, want := range []string{"klauspost/compress", "modernc.org/sqlite"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/DESIGN.md must name %s: it links through toktop", want)
		}
	}
}

// The screenshot renderer and the scripts CI job must resolve the same rich.
// `uv run scripts/shots/render.py` reads the PEP 723 header; the mypy step
// passes `--with rich==...`. A floating header would screenshot with a
// different renderer than CI typechecks.
func TestShotRendererRichPinMatchesCI(t *testing.T) {
	root := moduleRoot(t)
	script := readRepoFile(t, filepath.Join(root, "scripts", "shots", "render.py"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	scriptVer := richPin(script)
	ciVer := richPin(ci)
	if scriptVer == "" {
		t.Fatal("scripts/shots/render.py does not pin rich==VERSION in its PEP 723 header")
	}
	if ciVer == "" {
		t.Fatal(".github/workflows/ci.yml does not pin rich==VERSION")
	}
	if scriptVer != ciVer {
		t.Errorf("rich pin mismatch: render.py has %s, ci.yml has %s; bump them together", scriptVer, ciVer)
	}
}

// check-scripts must use the same ruff, mypy, and rich pins as the scripts
// job. A Makefile that lints with whatever is on PATH, or a CI bump that
// forgets the Makefile, is an after-push failure.
func TestScriptsToolPinsMatchCI(t *testing.T) {
	root := moduleRoot(t)
	makefile := readRepoFile(t, filepath.Join(root, "Makefile"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	script := readRepoFile(t, filepath.Join(root, "scripts", "shots", "render.py"))

	checks := []struct {
		name, makefile, ci string
	}{
		{"ruff", makefilePin(makefile, "RUFF_VERSION"), toolAtPin(ci, "ruff")},
		{"mypy", makefilePin(makefile, "MYPY_VERSION"), toolAtPin(ci, "mypy")},
		{"rich", makefilePin(makefile, "RICH_VERSION"), richPin(ci)},
	}
	for _, c := range checks {
		if c.makefile == "" {
			t.Errorf("Makefile does not set %s_VERSION", strings.ToUpper(c.name))
			continue
		}
		if c.ci == "" {
			t.Errorf("ci.yml does not pin %s", c.name)
			continue
		}
		if c.makefile != c.ci {
			t.Errorf("%s pin mismatch: Makefile has %s, ci.yml has %s; bump them together", c.name, c.makefile, c.ci)
		}
	}
	if scriptVer := richPin(script); scriptVer != makefilePin(makefile, "RICH_VERSION") {
		t.Errorf("rich pin mismatch: render.py has %s, Makefile has %s", scriptVer, makefilePin(makefile, "RICH_VERSION"))
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod: %v", root, err)
	}
	return root
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type moduleReq struct {
	path, version string
}

func directModules(t *testing.T, root string) []string {
	t.Helper()
	reqs := directReqs(t, root)
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.path
	}
	return out
}

func directReqs(t *testing.T, root string) []moduleReq {
	t.Helper()
	var out []moduleReq
	inRequire := false
	for raw := range strings.SplitSeq(readRepoFile(t, filepath.Join(root, "go.mod")), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "require (":
			inRequire = true
		case inRequire && line == ")":
			inRequire = false
		case strings.HasPrefix(line, "require ") && !strings.HasPrefix(line, "require ("):
			if r, ok := requireModule(line); ok {
				out = append(out, r)
			}
		case inRequire:
			if r, ok := requireModule(line); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

func requireModule(line string) (moduleReq, bool) {
	if strings.Contains(line, "// indirect") {
		return moduleReq{}, false
	}
	if i := strings.Index(line, "//"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) > 0 && fields[0] == "require" {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return moduleReq{}, false
	}
	return moduleReq{path: fields[0], version: fields[1]}, true
}

func importedModules(t *testing.T, root string, modules []string) map[string]bool {
	t.Helper()
	used := make(map[string]bool, len(modules))
	fset := token.NewFileSet()
	err := walkGoFiles(root, func(rel, path string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for _, mod := range modules {
				if imp == mod || strings.HasPrefix(imp, mod+"/") {
					used[mod] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func walkGoFiles(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			switch {
			case path == root:
				return nil
			case strings.HasPrefix(name, "."), name == "testdata":
				return fs.SkipDir
			default:
				return nil
			}
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), path)
	})
}

func fileHasBuildTag(body, tag string) bool {
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if !strings.HasPrefix(line, "//go:build") && !strings.HasPrefix(line, "// +build") {
			continue
		}
		if strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

func moduleDirs(t *testing.T, root string, reqs []moduleReq) map[string]string {
	t.Helper()
	paths := make([]string, 0, len(reqs))
	for _, r := range reqs {
		paths = append(paths, r.path)
	}
	dirs := listModuleDirs(t, root, paths)
	// A module nothing imports under the active build tags is never
	// downloaded by the build, so go list -m reports no Dir for it.
	// Fetch the stragglers into the cache and ask again.
	var missing []string
	for _, path := range paths {
		if dirs[path] == "" {
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return dirs
	}
	cmd := exec.Command("go", append([]string{"mod", "download"}, missing...)...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod download %s: %v\n%s", strings.Join(missing, " "), err, out)
	}
	for path, dir := range listModuleDirs(t, root, missing) {
		dirs[path] = dir
	}
	return dirs
}

func listModuleDirs(t *testing.T, root string, paths []string) map[string]string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-m", "-f", "{{.Path}}\t{{.Dir}}"}, paths...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go list -m: %v\n%s", err, stderr)
	}
	dirs := make(map[string]string, len(paths))
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		path, dir, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("go list -m: unexpected line %q", line)
		}
		dirs[path] = dir
	}
	return dirs
}

func readLicense(t *testing.T, dir, module string) string {
	t.Helper()
	for _, name := range []string{"LICENSE", "LICENSE.txt", "LICENSE.md"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return string(b)
		}
	}
	t.Fatalf("%s has no LICENSE in %s", module, dir)
	return ""
}

func licenseKind(body string) string {
	switch {
	case strings.Contains(body, "MIT License"), strings.Contains(body, "Permission is hereby granted"):
		return "MIT"
	case strings.Contains(body, "Redistribution and use in source and binary forms"):
		return "BSD-3-Clause"
	default:
		return "unknown"
	}
}

var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

var richPinned = regexp.MustCompile(`rich==([0-9][0-9A-Za-z._-]*)`)

func richPin(text string) string {
	m := richPinned.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

func makefilePin(text, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` \?= ([0-9][0-9A-Za-z._-]*)$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

func toolAtPin(text, tool string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(tool) + `@([0-9][0-9A-Za-z._-]*)`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}
