// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/parser"
	"go/token"
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
	"internal/agent":        {"internal/fuzzy", "internal/gauntlethome", "internal/normalize", "internal/runx"},
	"internal/envx":         {},
	"internal/evidence":     {"internal/fuzzy", "internal/gitx", "internal/journal", "internal/prompt"},
	"internal/fuzzy":        {},
	"internal/gauntlethome": {},
	"internal/ghx":          {"internal/runx"},
	"internal/gitx":         {"internal/runx"},
	"internal/humanize":     {},
	"internal/journal":      {"internal/gauntlethome", "internal/humanize"},
	"internal/normalize":    {},
	"internal/prompt":       {"internal/fuzzy", "internal/gitx", "internal/humanize", "internal/normalize"},
	"internal/runner": {
		"internal/agent", "internal/evidence", "internal/ghx", "internal/gitx", "internal/humanize",
		"internal/normalize", "internal/prompt", "internal/runx", "internal/streamjson",
	},
	"internal/runx":       {},
	"internal/selfupdate": {"internal/gauntlethome"},
	"internal/sbom":       {"internal/runx"},
	"internal/streamjson": {},
	"internal/ui":         {"internal/envx", "internal/fuzzy", "internal/humanize", "internal/normalize", "internal/runner"},
}

// TestInternalImportGraph fails when a package imports another against the
// documented direction, when a new internal package appears undeclared, when
// a declared package is gone, or when the map permits an edge nothing takes.
// ui importing runner is the dashboard reading event types; the picker takes
// FastSuggest on PickConfig so it does not need that edge for itself. ui
// importing envx is the motion-off variables read by the one boolean reader,
// so the single list of values that mean off is the list the dashboard answers
// by. runner importing evidence is the triage step calling the suggester that
// is not an agent; that edge is the only reason the file-signal suggester is
// its own package rather than a second mode inside the scheduler, and it is
// the one place the runner's public Suggest reaches past its own files.
// Nothing in internal/ may import ui. A permission no import uses is a
// hole left open for the next file, and docs/DESIGN.md would describe a
// dependency that does not exist, so the map has to name only the edges the
// tree really has.
func TestInternalImportGraph(t *testing.T) {
	root := moduleRoot(t)
	prefix := "github.com/maci0/gauntlet/internal/"
	seen := map[string]bool{}
	used := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err := walkGoFiles(root, func(rel, path string) error {
		pkg := filepath.ToSlash(filepath.Dir(rel))
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
			if used[pkg] == nil {
				used[pkg] = map[string]bool{}
			}
			for _, spec := range f.Imports {
				imp := strings.Trim(spec.Path.Value, `"`)
				if !strings.HasPrefix(imp, prefix) {
					continue
				}
				short := "internal/" + strings.TrimPrefix(imp, prefix)
				if short == "internal/ui" {
					t.Errorf("%s imports ui (%s); a headless run must not pay for the TUI", pkg, rel)
					continue
				}
				used[pkg][short] = true
				if !slices.Contains(allow, short) {
					t.Errorf("%s imports %s (%s); docs/DESIGN.md forbids that edge. Add it to allowedInternalImports only if the direction is intentional",
						pkg, short, rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for pkg, allow := range allowedInternalImports {
		if !seen[pkg] {
			t.Errorf("allowedInternalImports lists %s, but no .go files were found there; drop the stale entry", pkg)
			continue
		}
		for _, dep := range allow {
			if !used[pkg][dep] {
				t.Errorf("allowedInternalImports permits %s to import %s, but nothing does; drop the edge so the map names only the dependencies the tree has", pkg, dep)
			}
		}
	}
}
