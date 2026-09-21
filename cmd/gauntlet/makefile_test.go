// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if !strings.Contains(text, "export GOWORK := off") {
		t.Fatal("Makefile must export GOWORK=off so an ambient go.work cannot join the build")
	}
	if !strings.Contains(text, "export GOTOOLCHAIN := local") {
		t.Fatal("Makefile must export GOTOOLCHAIN=local so builds use the local toolchain rather than downloading over the network")
	}
	if !strings.Contains(text, "GOFLAGS= $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)") {
		t.Fatal("make vuln must clear GOFLAGS and use the pinned govulncheck version")
	}
	if !strings.Contains(text, `mkdir -p "$(TMPDIR)"`) {
		t.Fatal(`mkdir TMPDIR must quote the path: HOME can contain spaces`)
	}
	if !strings.Contains(text, `install -d "$(HOME)/.local/bin"`) {
		t.Fatal(`make install must quote destination path: HOME can contain spaces`)
	}
	if !strings.Contains(text, "is not on PATH") {
		t.Fatal("make install must say when ~/.local/bin is not on PATH")
	}
}

func TestMakefileExportsBuildEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-f", "-", "print-env")
	cmd.Dir = moduleRoot(t)
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "GOFLAGS", "GOWORK", "GOAMD64", "GOARM64", "GOTOOLCHAIN":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Env = append(cmd.Env, "GOWORK=/nonexistent/go.work", "GOFLAGS=-buildvcs=false", "GOAMD64=v3", "GOARM64=v9.0", "GOTOOLCHAIN=auto")
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		"include Makefile",
		"print-env:",
		"\t@printf 'GOFLAGS=%s\\nGOWORK=%s\\nGOAMD64=%s\\nGOARM64=%s\\nGOTOOLCHAIN=%s\\n' \"$$GOFLAGS\" \"$$GOWORK\" \"$$GOAMD64\" \"$$GOARM64\" \"$$GOTOOLCHAIN\"",
		"",
	}, "\n"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make print-env: %v\n%s", err, out)
	}
	got := make(map[string]string)
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			got[key] = value
		}
	}
	if env, want := got["GOFLAGS"], "-mod=readonly"; !strings.Contains(env, want) {
		t.Fatalf("GOFLAGS in a recipe environment: %q, want it to contain %q", env, want)
	}
	if env := got["GOWORK"]; env != "off" {
		t.Fatalf("GOWORK in a recipe environment: %q, want \"off\"", env)
	}
	if env := got["GOTOOLCHAIN"]; env != "local" {
		t.Fatalf("GOTOOLCHAIN in a recipe environment: %q, want \"local\"", env)
	}
	for key, want := range map[string]string{"GOAMD64": "v1", "GOARM64": "v8.0"} {
		if env := got[key]; env != want {
			t.Errorf("%s in a recipe environment: %q, want %q", key, env, want)
		}
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

func TestMakefileVulnScansSelectedTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default", want: "-tags sqlite ./..."},
		{name: "bare", args: []string{"TAGS="}, want: "./..."},
		{name: "notoktop", args: []string{"TAGS=notoktop"}, want: "-tags notoktop ./..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			args := append([]string{"--no-print-directory", "-n", "vuln", "GO=go", "GOVULNCHECK_VERSION=v1.7.0"}, tc.args...)
			cmd := exec.CommandContext(ctx, "make", args...)
			cmd.Dir = moduleRoot(t)
			for _, env := range os.Environ() {
				key, _, _ := strings.Cut(env, "=")
				switch key {
				case "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "TAGS":
					continue
				}
				cmd.Env = append(cmd.Env, env)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make vuln dry run: %v\n%s", err, out)
			}
			got := strings.Join(strings.Fields(string(out)), " ")
			want := "GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 " + tc.want
			if got != want {
				t.Fatalf("scan command:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestMakefileFmtWithSpacedToolchainPath(t *testing.T) {
	dir := t.TempDir()
	formatter := filepath.Join(dir, "go fmt")
	if err := os.Symlink(filepath.Join(runtime.GOROOT(), "bin", "gofmt"), formatter); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefileText(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(source, []byte("package sample\nvar value=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "fmt", "GOFMT="+formatter, "GOFILES=sample.go")
	cmd.Dir = dir
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make fmt: %v\n%s", err, out)
	}
	got, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if want := "package sample\n\nvar value = 1\n"; string(got) != want {
		t.Fatalf("formatted source = %q, want %q", got, want)
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

// Release artifacts must generate inventory names relative to the dist
// directory without leaking build directory paths into sbom.txt.
func TestMakefileReleaseGeneratesCleanSbom(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "cd $(DIST) && for f in $(BINARY)_*; do") {
		t.Fatal("make release must generate sbom.txt inside $(DIST) so paths match checksums.txt without $(DIST)/ prefixes")
	}
}

// make clean must sweep dist, build binaries, and scratch files.
func TestMakefileCleanRemovesScratchAndBinaries(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "rm -rf $(DIST) $(BINARY) $(BINARY)_* .scratch") {
		t.Fatal("make clean must remove dist, binaries, and scratch files")
	}
}

// make repro must keep scratch and lint caches out of the test archives.
func TestMakefileReproExcludesScratchAndCaches(t *testing.T) {
	text := makefileText(t)
	for _, want := range []string{"--exclude=./.scratch", "--exclude=./.ruff_cache", "--exclude=./.mypy_cache", "--exclude=./__pycache__"} {
		if !strings.Contains(text, want) {
			t.Errorf("make repro missing %q exclude", want)
		}
	}
}
