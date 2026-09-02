// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Makefile is the build: a go command that can rewrite go.mod/go.sum
// would let the lockfile drift from the source that produced a binary.
func TestMakefileHonorsGoSum(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "-mod=readonly") {
		t.Fatal("Makefile must pass -mod=readonly so a build cannot rewrite go.mod or go.sum")
	}
	if !strings.Contains(text, "GOFLAGS= $(GO) run golang.org/x/vuln/cmd/govulncheck@latest") {
		t.Fatal("make vuln must clear GOFLAGS; govulncheck is fetched unpinned and is not a build input")
	}
	if !strings.Contains(text, `mkdir -p "$(TMPDIR)"`) {
		t.Fatal(`mkdir TMPDIR must quote the path: HOME can contain spaces`)
	}
	if !strings.Contains(text, "is not on PATH") {
		t.Fatal("make install must say when ~/.local/bin is not on PATH")
	}
}

// AGENTS.md is loaded into every agent session. The three shipped tag
// sets must be named the way make and CI invoke them: a wrong command
// here is re-run by something that trusts it.
func TestAgentsMdDocumentsTheThreeTagSets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "TAGS=notoktop") {
		t.Fatal("AGENTS.md must name TAGS=notoktop (drops transcript reading)")
	}
	if !strings.Contains(text, "`TAGS=`") {
		t.Fatal("AGENTS.md must name TAGS= (empty tags: drops the sqlite driver)")
	}
	if strings.Contains(text, "drop both") {
		t.Fatal("AGENTS.md must not treat TAGS=notoktop as dropping toktop and sqlite together; TAGS= is the sqlite-off build")
	}
}
