// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Direct modules in go.mod are the supply-chain surface this repository
// chose. An unused one still downloads, still hashes, and still sits in the
// module graph; dropping it is the fix, and this test is how a leftover is
// found before it ships.
func TestDirectModulesAreImported(t *testing.T) {
	root := moduleRoot(t)
	direct := directModules(t, root)
	if len(direct) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	used := importedModules(t, root, direct)
	for _, path := range direct {
		if !used[path] {
			t.Errorf("go.mod requires %s, but no .go file imports it; remove the unused module", path)
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

func directModules(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	inRequire := false
	for raw := range strings.SplitSeq(readRepoFile(t, filepath.Join(root, "go.mod")), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "require (":
			inRequire = true
		case inRequire && line == ")":
			inRequire = false
		case strings.HasPrefix(line, "require ") && !strings.HasPrefix(line, "require ("):
			if path, ok := requirePath(line); ok {
				out = append(out, path)
			}
		case inRequire:
			if path, ok := requirePath(line); ok {
				out = append(out, path)
			}
		}
	}
	return out
}

func requirePath(line string) (string, bool) {
	if strings.Contains(line, "// indirect") {
		return "", false
	}
	if i := strings.Index(line, "//"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) > 0 && fields[0] == "require" {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return "", false
	}
	return fields[0], true
}

func importedModules(t *testing.T, root string, modules []string) map[string]bool {
	t.Helper()
	used := make(map[string]bool, len(modules))
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
