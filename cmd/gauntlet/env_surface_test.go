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
	"LC_ALL":         "read to decide whether a child git runs under the C locale this program parses its output in; not a setting, and an operator's own value is kept",
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

// envNamesReadIn returns every name passed to os.Getenv or os.LookupEnv in
// non-test Go files under root. A name reaches one of those calls in three
// ways in this tree, and all three are followed: a string literal, a constant
// declared in the same package, and a range over a slice literal whose body
// reads the environment. A computed name is left out: os.Expand's key
// function takes whatever the user typed, and nothing static can be said
// about those.
func envNamesReadIn(root string) ([]string, error) {
	var sources []envSource
	// Constants are collected for the whole tree before anything is matched:
	// a constant may be declared in a file the walk has not reached yet.
	consts := map[string]map[string]string{}
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
		sources = append(sources, envSource{path: path, file: file})
		dir := filepath.Dir(path)
		for ident, value := range stringConsts(file) {
			if consts[dir] == nil {
				consts[dir] = map[string]string{}
			}
			consts[dir][ident] = value
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var found []string
	add := func(name string) {
		if name == "" || slices.Contains(found, name) {
			return
		}
		found = append(found, name)
	}
	for _, src := range sources {
		pkg := consts[filepath.Dir(src.path)]
		ast.Inspect(src.file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if name, ok := envNameArg(node, pkg); ok {
					add(name)
				}
			case *ast.RangeStmt:
				for _, name := range rangedEnvNames(node, pkg) {
					add(name)
				}
			}
			return true
		})
	}
	return found, nil
}

// envSource is one parsed non-test file, held until every constant in the
// tree is known.
type envSource struct {
	path string
	file *ast.File
}

// envNameArg returns the variable name a call to os.Getenv or os.LookupEnv
// reads: the string literal passed directly, or the constant that identifier
// names. The second case is the common one, and skipping it left every
// consumer-facing name in this tree invisible to the check.
func envNameArg(call *ast.CallExpr, consts map[string]string) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
		return "", false
	}
	if sel.Sel.Name != "Getenv" && sel.Sel.Name != "LookupEnv" {
		return "", false
	}
	return constName(call.Args[0], consts), true
}

// rangedEnvNames returns the names a range statement walks over when its body
// reads the environment. Those loops hold the read in the body and the names
// in the slice, so neither the call nor the names alone can be matched.
func rangedEnvNames(rs *ast.RangeStmt, consts map[string]string) []string {
	lit, ok := rs.X.(*ast.CompositeLit)
	if !ok || !readsEnv(rs.Body) {
		return nil
	}
	names := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		name := constName(elt, consts)
		if name == "" {
			// One unknown element makes the whole set unprovable, so nothing
			// is claimed rather than a partial list that reads as complete.
			return nil
		}
		names = append(names, name)
	}
	return names
}

// readsEnv reports whether the subtree contains a call to os.Getenv or
// os.LookupEnv.
func readsEnv(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" &&
			(sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv") {
			found = true
		}
		return true
	})
	return found
}

// constName resolves an expression to a name: a string literal unquoted, or
// an identifier naming a string constant. Anything else is "".
func constName(expr ast.Expr, consts map[string]string) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return ""
		}
		unquoted, err := strconv.Unquote(e.Value)
		if err != nil {
			return ""
		}
		return unquoted
	case *ast.Ident:
		return consts[e.Name]
	}
	return ""
}

// stringConsts returns the string constants a file declares, by name.
func stringConsts(file *ast.File) map[string]string {
	declared := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range values.Names {
				if i >= len(values.Values) {
					continue
				}
				if name := constName(values.Values[i], nil); name != "" {
					declared[ident.Name] = name
				}
			}
		}
	}
	return declared
}
