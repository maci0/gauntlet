// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The env snapshots elsewhere pin the documented names; nothing tied that
// list to the names the binary actually reads, so a new knob could ship
// undocumented: invisible in `gauntlet help`, absent from docs/CLI.md and
// .env.example, and read from the operator's environment by a package no
// document mentions. This walks the source for os.Getenv and os.LookupEnv
// with a literal name and requires each one to be either documented (the
// help table) or listed below with the reason it is not a knob.

// envInternalNames are read by the code and deliberately not part of the
// consumer contract. Each entry says why.
var envInternalNames = map[string]string{
	"PATH":           "where executables are resolved from; a security control (absolute-only PATH), not a setting",
	"HOME":           "read through os.UserHomeDir, the default state root; GAUNTLET_HOME is the documented knob",
	"GAUNTLET_STATE": "names the handoff file across one hot reload; docs/CLI.md says so, and it is set by the process itself",
}

func TestEveryEnvVarReadIsDocumented(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	read, err := envNamesReadIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) == 0 {
		t.Fatal("no environment variable was found being read; the walk is broken, not the binary")
	}
	documented := make([]string, 0, len(helpEnvVars))
	for _, e := range helpEnvVars {
		documented = append(documented, e.Name)
	}
	for _, name := range read {
		if slices.Contains(documented, name) || envInternalNames[name] != "" {
			continue
		}
		t.Errorf("the code reads %s but `gauntlet help` does not list it, so docs/CLI.md and .env.example cannot be expected to either. Add it to helpEnvVars, goldenEnvVars, docs/CLI.md and .env.example, or record why it is internal in envInternalNames.", name)
	}
}

// envNamesReadIn returns every name passed to os.Getenv or os.LookupEnv as
// a string literal in non-test Go files under root. A computed name is left
// out: os.Expand's key function takes whatever the user typed, and nothing
// static can be said about those.
func envNamesReadIn(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case path == root:
				return nil
			case strings.HasPrefix(name, "."), name == "vendor", name == "testdata", name == "dist":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			if sel.Sel.Name != "Getenv" && sel.Sel.Name != "LookupEnv" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			unquoted, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if unquoted != "" && !slices.Contains(found, unquoted) {
				found = append(found, unquoted)
			}
			return true
		})
		return nil
	})
	return found, err
}
