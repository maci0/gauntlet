// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package evidence

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The scanners exercised here run over whatever a reviewed tree holds, on the
// interactive --suggest-agent path, with no agent and no tokens spent: Go
// through go/parser, the rest through a regex import matcher and a
// line-at-a-time declaration recognizer, manifests through encoding/json and
// a bounded TOML/INI reader. That is the untrusted-input surface of the
// suggester, and a crash in any of it takes the launcher down rather than one
// review. scanHead is the same sequence peek performs over one file.

// builtinMarkKeys is every category the regex mark table records, spelled out
// rather than derived from marks, so a new mark has to be acknowledged here
// too and the assertion below stays a closed set instead of restating whatever
// the table happens to hold today.
var builtinMarkKeys = map[string]bool{
	"auth": true, "cache": true, "cli": true, "clock": true, "cloud": true,
	"concurrent": true, "config": true, "dependency": true, "dom": true,
	"exec": true, "gui": true, "http": true, "httpclient": true, "logging": true,
	"model": true, "numeric": true, "parse": true, "personal": true,
	"portable": true, "recovery": true, "resource": true, "retry": true,
	"sql": true, "telemetry": true, "translate": true, "tui": true,
	"unsafe": true, "write": true,
}

// structuredMarkKeys are the categories the structured readers record: the
// import subjects in imports.go, plus the four package-field facts a manifest
// head establishes. They are listed rather than derived so this harness does
// not inherit a widening of the subject table as a widening of its own
// assertion; a new subject is a deliberate edit here too.
var structuredMarkKeys = map[string]bool{
	"library": true, "package": true, "release": true,
	"ddd_model": true, "ddd_behavior": true,
}

// freshSignals is one scanner's starting state: the maps record fills from a
// file's name and its head, with no evidence in them yet.
func freshSignals() signals {
	return signals{
		ext: map[string]int{}, name: map[string]bool{},
		path: map[string]bool{}, mark: map[string]int{}, hot: map[string]int{},
	}
}

// scanHead is what peek does with one file: record its name, run the scanner
// its extension or basename selects, and search its head for the marks the
// rule table keys on. Hoisted out of peek so a fuzzer can reach the scanners
// without a repository, a git listing, and a journal behind them.
func scanHead(name string, head []byte) signals {
	s := freshSignals()
	record(&s, name)
	switch base := strings.ToLower(filepath.Base(name)); base {
	case "package.json", "plugin.json", "pyproject.toml", "cargo.toml", "setup.cfg":
		packageMetadata(&s, base, head)
	default:
		sourceImports(&s, name, head)
		domainSignals(&s, name, head)
	}
	markFound(&s, markSearch(nil), asciiFold(nil, head))
	return s
}

// assertOneMarkPerFile fails on a mark no rule keys on, on a non-positive
// count, and on one recorded twice: the count is a count of files that proved
// a category, not of occurrences, so a head carrying one needle twenty times
// is still one file's evidence.
func assertOneMarkPerFile(t *testing.T, name string, head []byte, s signals) {
	t.Helper()
	for key, n := range s.mark {
		if !knownMark(key) {
			t.Fatalf("%s %q recorded mark %q, which is not a category any rule keys on", name, head, key)
		}
		if n <= 0 || n > 1 {
			t.Fatalf("%s %q recorded mark %q %d times", name, head, key, n)
		}
	}
}

// knownMark is the closed set of categories a scan may record: the built-in
// table, the four structured facts, an import subject, and the mark: namespace
// a review's own Signals: line searches in.
func knownMark(key string) bool {
	if builtinMarkKeys[key] || structuredMarkKeys[key] {
		return true
	}
	if _, subject := importSubjects[key]; subject {
		return true
	}
	return strings.HasPrefix(key, "mark:")
}

// FuzzScanFileSignals drives record, sourceImports, packageMetadata, and
// markFound with a file name and a head of bytes a reviewed tree controls.
//
// The fuzzer proves the scanners do not crash; the assertions are what make a
// wrong answer visible instead. Every assumption the rule table makes about a
// signal is checked here: only the built-in categories, the four structured
// facts, an import subject, and the mark: namespace are ever recorded, so a
// rule keyed on anyMark can never be steered by content the tree chose; an
// empty head records no capability at all; one file is never counted as two
// source files; a manifest head that is not valid JSON invents no package
// field; and scanning is deterministic, which is what lets a replay of a tree
// on a seed propose the same reviews.
func FuzzScanFileSignals(f *testing.F) {
	type seed struct{ name, body string }
	seeds := []seed{
		{"main.go", "package main\nimport \"github.com/gin-gonic/gin\"\n"},
		{"main.go", "package main\nimport (\n\t\"net/http\"\n\t\"os/exec\"\n)\nfunc main() { http.ListenAndServe() }"},
		{"client.go", "package client\nimport \"database/sql\"\nfunc NewClient() {}\n"},
		{"domain/order.go", "package domain\ntype Order struct { Status int }\nfunc (o *Order) Approve() { if o.Status == 0 { o.Status = 1 } }\n"},
		{"order.py", "class Order(AggregateRoot):\n    pass\n"},
		{"notes.ts", "/* class AggregateRoot {} */\nconst example = 'class ValueObject {}';\n"},
		{"main.go", "package main\nvar version = \"1.0\"\n"},
		{"main_test.go", "package main\nconst version = \"1.0\"\n"},
		{"main.go", "package main\n/* import \"gin\" */\n"},
		{"server.ts", "import express from 'express';\nimport {redis} from \"ioredis\";\nexport const VERSION = '1.0';"},
		{"app.js", "const pg = require('pg');\nimport(\"openai\");\n"},
		{"mod.rs", "use sqlx;\nextern crate rocket;\n"},
		{"main.rs", "use crate::redis;\nmod redis;\n"},
		{"setup.py", "from fastapi import FastAPI\nimport redis, openai\n"},
		{"setup.py", "def f():\n    version = '1.0'\n"},
		{"setup.py", "__version__ = '1.2.3'\n"},
		{"pkg/setup.py", "# from setuptools import setup\n"},
		{"main.zig", "const std = @import(\"std\");\n"},
		{"main.tsx", "export const version = '1';\n"},
		{"package.json", `{"version":"1.2.3","exports":{".":"./src/index.js"},"files":["src"],"bin":{"tool":"./cli.js"},"dependencies":{"express":"1"}}`},
		{"package.json", `{"private":true,"types":"./index.d.ts"}`},
		{"package.json", `{"exports":false,"bin":42,"files":[]}`},
		{"package.json", `{"version":"1","exports":{".":`},
		{"plugin.json", `{"main":"index.js","dependencies":{"redis":"1"}}`},
		{"pyproject.toml", "[project]\nname='x'\nversion='1.0'\ndependencies=['fastapi']\n"},
		{"pyproject.toml", "[build-system]\nrequires=[]\n[tool.poetry]\nversion='1'\n"},
		{"cargo.toml", "[package]\nversion = \"0.1\"\n[dependencies]\nserde='1'\n"},
		{"setup.cfg", "[metadata]\nversion = 1.0\ninstall_requires=\n  redis\n"},
		{"README.md", "import express from 'express'\n"},
		{"", "package main\nimport \"unsafe\"\n"},
		{"weird name.go", "package main\n"},
		{"main.go", "\xff\xfe\x00invalid utf8 \xed\xa0\x80"},
		{"server.py", "'''\nimport redis\n'''\nimport openai\n"},
		{"server.py", "/* unterminated\nimport redis\n"},
		{"package.json", `{"version":"1","exports":"single-string"}`},
		{"package.json", `{"dependencies":{"":"","express":""}}`},
		{"app.py", "from . import redis\nfrom ..server import pg\n"},
	}
	for _, s := range seeds {
		f.Add(s.name, s.body)
	}

	f.Fuzz(func(t *testing.T, name, body string) {
		head := []byte(body)
		s := scanHead(name, head)
		assertOneMarkPerFile(t, name, head, s)

		// A head with no content carries no import and no manifest field, so
		// nothing about this file's capabilities may be recorded from it. The
		// name is still evidence, and the maps keyed by name keep it: what
		// cannot be true is a mark, an extension count, or source presence
		// attributed to bytes that are not there.
		if strings.TrimSpace(body) == "" {
			for key, n := range s.mark {
				t.Fatalf("%s %q recorded mark %q (%d) from an empty head", name, head, key, n)
			}
		}

		// Determinism is what lets a replay of the same tree on the same seed
		// propose the same reviews, and the churn bonus divides by it.
		again := scanHead(name, head)
		if !maps.Equal(again.mark, s.mark) || !maps.Equal(again.ext, s.ext) ||
			!maps.Equal(again.name, s.name) || !maps.Equal(again.path, s.path) ||
			again.source != s.source || again.tests != s.tests || again.files != s.files {
			t.Fatalf("scanning %s %q twice gave different signals", name, head)
		}

		// The source count is what the language rules read (lang and liveness
		// key off s.source against langMinFiles), so it has to be a property of
		// the extension table rather than of anything the name happens to look
		// like: a file is source only when its extension is one of them, and
		// scanHead is one file, so the count can never exceed one.
		if s.source > 0 {
			if !slices.ContainsFunc(slices.Collect(maps.Keys(s.ext)), func(ext string) bool {
				return sourceExts[ext]
			}) {
				t.Fatalf("%s %q counted as source (%d) with no source extension in %v",
					name, head, s.source, s.ext)
			}
			if s.source > 1 {
				t.Fatalf("%s %q is one file and counted %d source files", name, head, s.source)
			}
		}

		// A manifest head that is not one complete JSON document supplies no
		// invented fields: a truncated package.json proves nothing about the
		// package, and a review proposed from it would name a version or an
		// entry point the file never declared.
		if base := strings.ToLower(filepath.Base(name)); base == "package.json" || base == "plugin.json" {
			if !json.Valid(head) {
				for _, key := range []string{"library", "package", "release", "cli"} {
					if s.mark[key] > 0 {
						t.Fatalf("%s %q is not valid JSON and still recorded %q", name, head, key)
					}
				}
			}
		}
	})
}
