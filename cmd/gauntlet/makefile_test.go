// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makefileText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The Makefile is the build: a go command that can rewrite go.mod/go.sum
// would let the lockfile drift from the source that produced a binary.
func TestMakefileHonorsGoSum(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "-mod=readonly") {
		t.Fatal("Makefile must pass -mod=readonly so a build cannot rewrite go.mod or go.sum")
	}
	if !strings.Contains(text, "GOFLAGS= $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)") {
		t.Fatal("make vuln must clear GOFLAGS and use the pinned govulncheck version")
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

func TestMakefileCheckAlwaysAnalyzesShippedTags(t *testing.T) {
	for _, tags := range []string{"sqlite", "", "notoktop"} {
		t.Run("tags="+tags, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", "check", "TAGS="+tags, "GO=go", "GOFMT=gofmt", "GOFILES=.")
			cmd.Dir = moduleRoot(t)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make check dry run: %v\n%s", err, out)
			}
			var analysis []string
			for line := range strings.SplitSeq(string(out), "\n") {
				if strings.HasPrefix(line, "go fix ") || strings.HasPrefix(line, "go vet ") {
					analysis = append(analysis, line)
				}
			}
			want := "go fix -diff -tags sqlite ./...\ngo fix -diff ./...\ngo fix -diff -tags notoktop ./...\ngo vet -tags sqlite ./...\ngo vet ./...\ngo vet -tags notoktop ./..."
			if got := strings.Join(analysis, "\n"); got != want {
				t.Fatalf("analysis commands:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// make ci is the one local command that covers the pull-request Go job.
func TestMakefileHasCITarget(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "\nci: check test ##") {
		t.Fatal("make ci must run check then test, the Go checks a pull request runs")
	}
}

// Missing uvx, shellcheck, or gofmt used to be a bare "command not found".
func TestMakefileCheckScriptsPreflight(t *testing.T) {
	text := makefileText(t)
	for _, want := range []string{
		"uvx not found",
		"shellcheck not found",
		"gofmt not found",
		"uvx ruff@$(RUFF_VERSION) check scripts",
		"uvx ruff@$(RUFF_VERSION) format --check scripts",
		"uvx --with rich==$(RICH_VERSION) mypy@$(MYPY_VERSION) --strict scripts",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
}
