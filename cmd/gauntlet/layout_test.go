// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// allowedInternalImports is the downward graph in docs/DESIGN.md: which
// internal package may import which other. cmd/gauntlet is the composition
// root and may import any of them. A new edge is a layering change; add it
// here only when DESIGN.md says the direction is intentional.
var allowedInternalImports = map[string][]string{
	"internal/agent":        {"internal/fuzzy", "internal/gauntlethome"},
	"internal/fuzzy":        {},
	"internal/gauntlethome": {},
	"internal/ghx":          {},
	"internal/gitx":         {},
	"internal/humanize":     {},
	"internal/journal":      {"internal/gauntlethome"},
	"internal/normalize":    {},
	"internal/prompt":       {"internal/fuzzy", "internal/gitx", "internal/humanize"},
	"internal/runner": {
		"internal/agent", "internal/ghx", "internal/gitx", "internal/humanize",
		"internal/journal", "internal/normalize", "internal/prompt", "internal/streamjson",
	},
	"internal/selfupdate": {},
	"internal/streamjson": {},
	"internal/ui":         {"internal/fuzzy", "internal/humanize", "internal/normalize", "internal/runner"},
}

// TestInternalImportGraph fails when a package imports another against the
// documented direction, when a new internal package appears undeclared, or
// when a declared package is gone. ui importing runner is the dashboard
// reading event types; the picker takes FastSuggest on PickConfig so it does
// not need that edge for itself. Nothing in internal/ may import ui.
func TestInternalImportGraph(t *testing.T) {
	root := moduleRoot(t)
	prefix := "github.com/maci0/gauntlet/internal/"
	seen := map[string]bool{}
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
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)
		file, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file = filepath.ToSlash(file)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		if strings.HasPrefix(pkg, "internal/") {
			seen[pkg] = true
			allow, known := allowedInternalImports[pkg]
			if !known {
				t.Errorf("%s is an internal package not listed in allowedInternalImports; add it with the imports docs/DESIGN.md allows, or move the code", pkg)
				return nil
			}
			for _, spec := range f.Imports {
				imp := strings.Trim(spec.Path.Value, `"`)
				if !strings.HasPrefix(imp, prefix) {
					continue
				}
				short := "internal/" + strings.TrimPrefix(imp, prefix)
				if short == "internal/ui" {
					t.Errorf("%s imports ui (%s); a headless run must not pay for the TUI", pkg, file)
					continue
				}
				if !slices.Contains(allow, short) {
					t.Errorf("%s imports %s (%s); docs/DESIGN.md forbids that edge. Add it to allowedInternalImports only if the direction is intentional",
						pkg, short, file)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for pkg := range allowedInternalImports {
		if !seen[pkg] {
			t.Errorf("allowedInternalImports lists %s, but no .go files were found there; drop the stale entry", pkg)
		}
	}
}
