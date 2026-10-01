// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
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
	if !strings.Contains(text, "BINDIR ?= $(HOME)/.local/bin") {
		t.Fatal("BINDIR must default to the per-user directory the README install uses, so the documented install is unchanged")
	}
	if !strings.Contains(text, `install -d "$(BINDIR)"`) {
		t.Fatal(`make install must quote destination path: HOME can contain spaces`)
	}
	if !strings.Contains(text, "is not on PATH") {
		t.Fatal("make install must say when the destination is not on PATH")
	}
	if !strings.Contains(text, `cp -p "$(BINDIR)/$(BINARY)" "$(BINDIR)/$(BINARY).previous"`) {
		t.Fatal(`make install must keep the binary it replaces as $(BINARY).previous, the name gauntlet update keeps its copy under, so a locally built install can be rolled back the same way`)
	}
}

// The compiler is the one build input nothing in the build normalizes: a Go
// binary records the version that compiled it, and GOTOOLCHAIN=local compiles
// with whatever is installed. So the exact release is pinned once, in the
// Makefile, and every job installs that one. A workflow that resolved its Go
// from go.mod would follow the `go` line, which is a minimum, and build
// releases with whichever patch came out that week.
func TestGoVersionPinMatchesCI(t *testing.T) {
	root := moduleRoot(t)
	pin := goPinFromMakefile(makefileText(t))
	if pin == "" {
		t.Fatal("Makefile must set GO_VERSION to the exact Go release artifacts are built with")
	}
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var steps int
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".yml") {
			continue
		}
		name := ent.Name()
		text := readRepoFile(t, filepath.Join(dir, name))
		if strings.Contains(text, "go-version-file:") {
			t.Errorf("%s: go-version-file resolves the `go` line in go.mod, a minimum; pin go-version to %s", name, pin)
		}
		for line := range strings.SplitSeq(text, "\n") {
			if !strings.Contains(line, "uses: actions/setup-go") {
				continue
			}
			steps++
			if !strings.Contains(text, "go-version: "+pin) {
				t.Errorf("%s: actions/setup-go must install go-version: %s, the pin the Makefile builds release artifacts with", name, pin)
			}
		}
	}
	if steps == 0 {
		t.Fatal("no workflow installs Go")
	}
}

// goPinFromMakefile returns the Makefile's GO_VERSION, without the variable's
// `?=` assignment.
func goPinFromMakefile(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "GO_VERSION ?= ")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// The artifact targets are the ones whose bytes ship, so they are the ones
// that refuse a toolchain the pin does not name. build, test, and check must
// keep working on whatever a contributor has installed.
func TestToolchainPinGatesArtifactTargets(t *testing.T) {
	text := makefileText(t)
	for _, target := range []string{"dist", "repro"} {
		if !strings.Contains(text, target+": | toolchain\n") {
			t.Errorf("make %s must require the toolchain pin; a release is the compiler version recorded in every binary it ships", target)
		}
	}
	for _, target := range []string{"build", "test", "check", "ci"} {
		if strings.Contains(text, "\n"+target+": | toolchain\n") {
			t.Errorf("make %s must not require the toolchain pin; contributors build and test on the toolchain they have", target)
		}
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
		case "MAKEFLAGS", "MFLAGS", "MAKEOVERRIDES", "GOFLAGS", "GOWORK", "GOAMD64", "GOARM64", "GOTOOLCHAIN", "GOEXPERIMENT", "GOFIPS140":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Env = append(cmd.Env, "GOWORK=/nonexistent/go.work", "GOFLAGS=-buildvcs=false", "GOAMD64=v3", "GOARM64=v9.0", "GOTOOLCHAIN=auto",
		"GOEXPERIMENT=loopvar", "GOFIPS140=inprocess")
	cmd.Stdin = strings.NewReader(strings.Join([]string{
		"include Makefile",
		"print-env:",
		"\t@printf 'GOFLAGS=%s\\nGOWORK=%s\\nGOAMD64=%s\\nGOARM64=%s\\nGOTOOLCHAIN=%s\\nGOEXPERIMENT=%s\\nGOFIPS140=%s\\n' \"$$GOFLAGS\" \"$$GOWORK\" \"$$GOAMD64\" \"$$GOARM64\" \"$$GOTOOLCHAIN\" \"$$GOEXPERIMENT\" \"$$GOFIPS140\"",
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
	if env, want := got["GOFLAGS"], "-mod=readonly"; env != want {
		t.Fatalf("GOFLAGS in a recipe environment: %q, want %q", env, want)
	}
	if env := got["GOWORK"]; env != "off" {
		t.Fatalf("GOWORK in a recipe environment: %q, want \"off\"", env)
	}
	if env := got["GOTOOLCHAIN"]; env != "local" {
		t.Fatalf("GOTOOLCHAIN in a recipe environment: %q, want \"local\"", env)
	}
	for key, want := range map[string]string{"GOAMD64": "v1", "GOARM64": "v8.0", "GOEXPERIMENT": "", "GOFIPS140": "off"} {
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
			// The scan command is the last recipe line; the order-only
			// prerequisites above it (the scratch directory the go command
			// refuses to start without) are not part of this contract.
			printed := strings.Split(strings.TrimSpace(string(out)), "\n")
			got := strings.Join(strings.Fields(printed[len(printed)-1]), " ")
			want := "GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 " + tc.want
			if got != want {
				t.Fatalf("scan command:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// staticcheck is fetched rather than installed, so the version a run uses is
// the one the Makefile records: a recipe that dropped the pin would resolve
// whatever the proxy serves that week, and a green tree would stop meaning
// the same thing twice. It reads the same TAGS variable vet does, so one of
// the three shipped configurations can be checked on its own.
func TestMakefileStaticcheckIsPinnedAndScansSelectedTags(t *testing.T) {
	pin := makefilePin(makefileText(t), "STATICCHECK_VERSION")
	if pin == "" {
		t.Fatal("the Makefile does not pin STATICCHECK_VERSION, so make check resolves whatever the proxy serves")
	}
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
			args := append([]string{"--no-print-directory", "-n", "staticcheck", "GO=go", "STATICCHECK_VERSION=" + pin}, tc.args...)
			cmd := exec.CommandContext(ctx, "make", args...)
			cmd.Dir = moduleRoot(t)
			cmd.Env = cleanMakeEnv()
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make staticcheck dry run: %v\n%s", err, out)
			}
			// The analyzer command is the last recipe line; the order-only
			// prerequisite above it (the scratch directory the go command
			// refuses to start without) is not part of this contract.
			printed := strings.Split(strings.TrimSpace(string(out)), "\n")
			got := strings.Join(strings.Fields(printed[len(printed)-1]), " ")
			want := "GOFLAGS= go run honnef.co/go/tools/cmd/staticcheck@" + pin + " " + tc.want
			if got != want {
				t.Fatalf("analysis command:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// The gofmt of the toolchain on PATH, asked of the toolchain rather than
// read off the test binary. runtime.GOROOT is deprecated: it is the root the
// running binary was built with, not the one whose gofmt the Makefile
// invokes, and the two differ whenever the test runs under a toolchain
// wrapper or a rebuilt binary.
func toolchainGofmt(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "bin", "gofmt")
}

func TestMakefileFmtWithSpacedToolchainPath(t *testing.T) {
	dir := t.TempDir()
	formatter := filepath.Join(dir, "go fmt")
	if err := os.Symlink(toolchainGofmt(t), formatter); err != nil {
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
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", "check", "TAGS="+tags, "GO=go", "GOFMT=gofmt", "GOFILES=.", "MAKE=make")
			cmd.Dir = moduleRoot(t)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make check dry run: %v\n%s", err, out)
			}
			var analysis []string
			for line := range strings.SplitSeq(string(out), "\n") {
				// The go fix legs carry the shell guard that names the command
				// to apply the rewrites, so the recorded command is the line up
				// to it, with the recipe's silencing @ stripped.
				cmd, _, _ := strings.Cut(strings.TrimPrefix(line, "@"), " ||")
				cmd = strings.TrimSpace(cmd)
				switch {
				case strings.HasPrefix(cmd, "go fix "), strings.HasPrefix(cmd, "go vet "):
					analysis = append(analysis, cmd)
				case strings.HasPrefix(cmd, "make --no-print-directory staticcheck"):
					analysis = append(analysis, cmd)
				}
			}
			want := strings.Join([]string{
				"go fix -diff -tags sqlite ./...",
				"go fix -diff ./...",
				"go fix -diff -tags notoktop ./...",
				"go vet -tags sqlite ./...",
				"go vet ./...",
				"go vet -tags notoktop ./...",
				"make --no-print-directory staticcheck TAGS=sqlite",
				"make --no-print-directory staticcheck TAGS=",
				"make --no-print-directory staticcheck TAGS=notoktop",
			}, "\n")
			if got := strings.Join(analysis, "\n"); got != want {
				t.Fatalf("analysis commands:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// A require nobody imports is still downloaded, still hashed, and still in
// the module graph, and -mod=readonly stops a build from noticing. `make
// check` asks the module graph itself, with -diff so the check cannot become
// the edit it is checking for.
func TestMakefileCheckVerifiesModuleManifests(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "check: tidy\n") {
		t.Fatal("make check must run the tidy target, or an untidy go.mod ships without a build noticing")
	}
	if !strings.Contains(text, "$(GO) mod tidy -diff") {
		t.Fatal("make tidy must ask for the diff rather than applying it, so a check never rewrites go.mod or go.sum")
	}
	if strings.Contains(text, "$(GO) mod tidy\n") {
		t.Fatal("a bare 'go mod tidy' would rewrite the manifests from whatever target ran it")
	}
}

// The dry run has to name the command, not just declare the target: a
// prerequisite that no rule defines makes `make check` fail, not pass, so
// this is about the recipe being reachable at all.
func TestMakefileTidyIsReachableFromCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", "check", "GO=go", "GOFMT=gofmt", "GOFILES=.")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make check dry run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "mod tidy -diff") {
		t.Fatalf("make check does not reach go mod tidy -diff:\n%s", out)
	}
}

// make ci is the one local command that covers the pull-request Go job.
func TestMakefileHasCITarget(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "\nci: check test ##") {
		t.Fatal("make ci must run check then test, the Go checks a pull request runs")
	}
}

// The full local verification is otherwise a sentence in CONTRIBUTING that
// names five commands, which is how a leg gets skipped and the failure only
// appears after push. make verify runs them, all three tag legs included.
func TestMakefileHasVerifyTarget(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "\nverify: check check-scripts ##") {
		t.Fatal("make verify must run check and check-scripts, the two static pull-request jobs")
	}
	for _, want := range []string{
		"\t$(MAKE) test\n",
		"\t$(MAKE) test TAGS=\n",
		"\t$(MAKE) test TAGS=notoktop\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("make verify missing recipe line %q", want)
		}
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
		"uvx yamllint@$(YAMLLINT_VERSION) --strict .github",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
}

// `make test-pkg` promises one package. With PKG unset it used to fall back
// to ./... and run the whole suite under that name, so the target meant for
// the fast loop was the slow one.
func TestMakefileTestPkgRefusesToRunEveryPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-pkg")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make test-pkg with no PKG ran the whole tree instead of refusing:\n%s", out)
	}
	if !strings.Contains(string(out), "make test-pkg PKG=./internal/prompt") {
		t.Fatalf("make test-pkg must name the invocation that works:\n%s", out)
	}
}

// The other direction. `make test PKG=./internal/prompt` took the package and
// ran ./... under the race detector without saying so, which is the six-minute
// gate the per-package targets exist to avoid, reached by typing the PKG the
// docs describe on the target whose name matches them.
func TestMakefileTestRefusesAPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test", "PKG=./internal/humanize")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make test with a PKG ran the whole tree instead of refusing:\n%s", out)
	}
	if !strings.Contains(string(out), "make test-pkg PKG=./internal/humanize") {
		t.Fatalf("make test must name the invocation that runs one package:\n%s", out)
	}
}

// PKG=./... is the whole tree written out, which is what a sub-make of the
// gate passes, so the refusal above cannot be the whole story. A RUN that
// matches nothing ends the run at the selection check, which is the point
// where the guard would have fired had it fired at all.
func TestMakefileTestAcceptsTheWholeTreeAsAPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test",
		"PKG=./...", "RUN=TestNoSuchTestNameAnywhere")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, _ := cmd.CombinedOutput()
	if strings.Contains(string(out), "takes no PKG") {
		t.Fatalf("make test PKG=./... is the whole tree and must not be refused:\n%s", out)
	}
	if !strings.Contains(string(out), "no test matches RUN=") {
		t.Fatalf("make test PKG=./... must reach the test run, not stop at the guard:\n%s", out)
	}
}

// `go test -run` exits 0 when the pattern selects nothing, so a mistyped test
// name reported a pass. Both targets that take RUN must turn that into a
// failure that names the pattern and how to list the real names.
func TestMakefileTestRefusesARunThatSelectsNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-pkg",
		"PKG=./internal/humanize", "RUN=TestNoSuchTestNameAnywhere")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make test-pkg with a RUN that matches nothing reported success:\n%s", out)
	}
	if !strings.Contains(string(out), "TestNoSuchTestNameAnywhere") {
		t.Fatalf("the failure must name the pattern that selected nothing:\n%s", out)
	}
	if !strings.Contains(string(out), "-list") {
		t.Fatalf("the failure must say how to list the real test names:\n%s", out)
	}
}

// The command the failure prints is the only recovery a contributor has, so
// it is run here rather than believed: it named an empty PKG and a bare `.`
// regexp, which the go command reads as the module root and answers "no Go
// files" while listing nothing.
func TestMakefileTestPrintsAListCommandThatRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-pkg",
		"PKG=./internal/humanize", "RUN=TestNoSuchTestNameAnywhere")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make test-pkg with a RUN that matches nothing reported success:\n%s", out)
	}

	const prefix = "test: list the candidates with: "
	var listed string
	for line := range strings.Lines(string(out)) {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			listed = strings.TrimSpace(after)
		}
	}
	if listed == "" {
		t.Fatalf("the failure must print the listing command after %q:\n%s", prefix, out)
	}

	run := exec.CommandContext(ctx, "sh", "-c", listed)
	run.Dir = moduleRoot(t)
	run.Env = cleanMakeEnv()
	names, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("the listing command the failure prints does not run: %s\n%s", listed, names)
	}
	if !strings.Contains(string(names), "Test") {
		t.Fatalf("the listing command names no tests: %s\n%s", listed, names)
	}
}

// A package whose tests really do match must still pass, so the guard above
// cannot be satisfied by refusing everything.
func TestMakefileTestPkgRunsAMatchingTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-pkg",
		"PKG=./internal/humanize", "RUN=TestDuration")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make test-pkg PKG=./internal/humanize RUN=TestDuration: %v\n%s", err, out)
	}
}

// `make test-fast` exists to cut the wait between two edits, so the only thing
// it may drop is the race detector. Everything else it passes has to match
// what `test-pkg` passes, build tags above all: a bare `go test` leaves those
// off and runs the no-database build, which is the trap the target replaces.
func TestMakefileTestFastDropsOnlyTheRaceDetector(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	flags := func(target string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "--dry-run", target,
			"PKG=./internal/humanize")
		cmd.Dir = moduleRoot(t)
		cmd.Env = cleanMakeEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make --dry-run %s: %v\n%s", target, err, out)
		}
		return string(out)
	}

	fast := flags("test-fast")
	if strings.Contains(fast, "-race") {
		t.Fatalf("make test-fast still passes -race, so it is the slow loop under a new name:\n%s", fast)
	}
	if !strings.Contains(fast, "-tags sqlite") {
		t.Fatalf("make test-fast dropped the build tags, so it tests a configuration nothing ships:\n%s", fast)
	}
	if !strings.Contains(fast, "-shuffle=on") {
		t.Fatalf("make test-fast dropped -shuffle=on, so it is not the run test-pkg does:\n%s", fast)
	}
	if !strings.Contains(flags("test-pkg"), "-race") {
		t.Fatal("make test-pkg no longer passes -race, so the gate a contributor runs before pushing stopped checking for races")
	}
}

// A fast target that quietly stopped running, or that grew a divergence from
// the recipe it shares, would read as a passing loop.
func TestMakefileTestFastRunsAMatchingTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-fast",
		"PKG=./internal/humanize", "RUN=TestDuration")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make test-fast PKG=./internal/humanize RUN=TestDuration: %v\n%s", err, out)
	}
}

// It takes the package it is named for, on the same terms test-pkg does.
func TestMakefileTestFastRefusesToRunEveryPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "test-fast")
	cmd.Dir = moduleRoot(t)
	cmd.Env = cleanMakeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make test-fast with no PKG ran the whole tree instead of refusing:\n%s", out)
	}
	if !strings.Contains(string(out), "make test-fast PKG=./internal/prompt") {
		t.Fatalf("make test-fast must name the invocation that works:\n%s", out)
	}
}

// The Makefile exports the build environment, so a test that shells out to it
// inherits a command line the caller never wrote. PKG and RUN go with
// MAKEFLAGS: make exports a command-line variable to every recipe, so the
// documented `make test-pkg PKG=./cmd/gauntlet` reached the nested
// `make test-pkg` with PKG set, the guard never fired, and the package ran
// itself until the test binary's timeout killed it. TAGS and MAKEOVERRIDES
// also go: each nested check must use its own requested build configuration.
func cleanMakeEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKEOVERRIDES", "TAGS", "PKG", "RUN":
			continue
		}
		env = append(env, entry)
	}
	return env
}

// GOTOOLCHAIN is pinned to local, so a Go older than the `go` line in go.mod
// fails inside the go command with a message about a knob the caller never
// set. The preflight has to name the minimum instead, and the dev targets have
// to run it: the minimum is not the release pin `toolchain` gates.
func TestMakefileToolchainMinNamesTheGoRequirement(t *testing.T) {
	text := makefileText(t)
	for _, target := range []string{"build", "check"} {
		if !strings.Contains(text, "\n"+target+": | toolchain-min\n") {
			t.Errorf("make %s must run the toolchain-min preflight", target)
		}
	}
	for _, want := range []string{
		"the go line in go.mod",
		"GOTOOLCHAIN=auto",
		"go.dev/dl/",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile toolchain-min missing %q", want)
		}
	}
	// A stub go stands in for the toolchain the contributor has, so the
	// comparison is exercised without installing one.
	stub := func(t *testing.T, version string) string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "go")
		script := "#!/bin/sh\n[ \"$1 $2\" = \"env GOVERSION\" ] && echo " + version + " && exit 0\nexit 1\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	root := moduleRoot(t)
	for _, tc := range []struct {
		version string
		wantErr bool
	}{
		{version: "go1.27.0"},
		{version: "go1.28.1"},
		{version: "go1.27.1-X:nodwarf5"},
		{version: "go1.26.3", wantErr: true},
		{version: "go1.26.3-X:nodwarf5", wantErr: true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "toolchain-min",
				"GO="+stub(t, tc.version), "GOFMT=gofmt")
			cmd.Dir = root
			cmd.Env = cleanMakeEnv()
			out, err := cmd.CombinedOutput()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toolchain-min accepted %s:\n%s", tc.version, out)
				}
				if !strings.Contains(string(out), "1.27.0") {
					t.Fatalf("the failure must name the minimum:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("toolchain-min rejected %s: %v\n%s", tc.version, err, out)
			}
		})
	}
}

func TestMakefileTestPreflight(t *testing.T) {
	text := makefileText(t)
	for _, want := range []string{
		"C compiler",
		"not found on PATH",
		"CGO_ENABLED=1",
		"-run '$(RUN)'",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile missing %q", want)
		}
	}
}

func TestMakefileFmtScriptsTarget(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "fmt-scripts: ## rewrite scripts with ruff format") {
		t.Fatal("Makefile missing fmt-scripts target")
	}
	if !strings.Contains(text, "uvx ruff@$(RUFF_VERSION) format scripts") {
		t.Fatal("Makefile fmt-scripts must invoke ruff format with pinned version")
	}
}

// The asset name has three would-be sources of truth: the Makefile's dist
// target, the two release workflows' smoke tests, and the runtime lookup in
// internal/selfupdate. The workflows must run the Makefile's check, which
// resolves the asset through host-artifact, or a rename leaves a job running a
// path that no longer exists.
func TestReleaseSmokeTestsAskTheMakefileForTheAsset(t *testing.T) {
	root := moduleRoot(t)
	for _, workflow := range []string{"ci.yml", "release.yml"} {
		t.Run(workflow, func(t *testing.T) {
			text := readRepoFile(t, filepath.Join(root, ".github", "workflows", workflow))
			if !strings.Contains(text, "make smoke VERSION=") {
				t.Errorf("%s: the smoke test must run `make smoke`, the one implementation of the check", workflow)
			}
			if strings.Contains(text, "gauntlet_${version}_linux_amd64") || strings.Contains(text, "dist/gauntlet_ci_linux_amd64") {
				t.Errorf("%s: the smoke test restates the asset name; make host-artifact owns it", workflow)
			}
		})
	}
}

// The check the two workflows share must live in the Makefile, ask
// host-artifact for the asset, and compare what the binary reports against the
// version it was stamped with. A workflow that inlines the comparison again
// is the duplication this target exists to end.
func TestSmokeTargetRunsTheHostArtifact(t *testing.T) {
	recipe := makefileRecipe(makefileText(t), "smoke")
	if recipe == "" {
		t.Fatal("Makefile smoke target has no recipe")
	}
	for _, want := range []string{
		"host-artifact VERSION=$(VERSION)",
		`"$$binary" version`,
		`[ "$$got" = "$(BINARY) $(VERSION)" ]`,
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make smoke missing %q", want)
		}
	}
}

// The target has to pass on a binary that reports the stamped version and
// fail on one that does not, or the workflows run a check that decides
// nothing. DIST points the recipe at a stand-in, so no cross-compilation is
// needed to drive the real target.
func TestSmokePassesAndFailsOnWhatTheBinaryReports(t *testing.T) {
	const version = "1.2.3"
	for _, tc := range []struct {
		name    string
		reports string
		wantErr bool
	}{
		{name: "stamped", reports: "gauntlet " + version},
		{name: "wrong version", reports: "gauntlet 0.0.1", wantErr: true},
		{name: "not the binary", reports: "segfault", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			asset := filepath.Join(dist, "gauntlet_"+version+"_"+runtime.GOOS+"_"+runtime.GOARCH)
			if err := os.WriteFile(asset, []byte("#!/bin/sh\necho '"+tc.reports+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "smoke",
				"VERSION="+version, "DIST="+dist)
			cmd.Dir = moduleRoot(t)
			cmd.Env = cleanMakeEnv()
			out, err := cmd.CombinedOutput()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("a binary reporting %q passed the smoke test:\n%s", tc.reports, out)
				}
				if !strings.Contains(string(out), "smoke:") {
					t.Fatalf("the failure must say which check failed:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("make smoke on a binary reporting %q: %v\n%s", tc.reports, err, out)
			}
		})
	}
}

// A release built from the default VERSION is a release nobody tagged: the
// assets are named gauntlet_dev_*, the binary reports `gauntlet dev`, and
// `make smoke VERSION=dev` passes because it compares the stamp against the
// same placeholder it was handed. The refusal belongs to the target the
// release workflow runs, before the suite and the cross-compiles.
func TestReleaseRefusesTheDefaultVersion(t *testing.T) {
	if !strings.Contains(makefileText(t), "release: | clean-tree release-version ") {
		t.Error("make release must run release-version; a release is cut from a tag, and the tag is what names the version")
	}
	for _, tc := range []struct {
		version string
		wantErr bool
	}{
		{version: "", wantErr: true},
		{version: "dev", wantErr: true},
		{version: "1.26.0"},
		{version: "1.26.0-rc.1"},
		{version: "ci"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "release-version", "VERSION="+tc.version)
			cmd.Dir = moduleRoot(t)
			cmd.Env = cleanMakeEnv()
			out, err := cmd.CombinedOutput()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("make release-version VERSION=%q succeeded; it ships unversioned assets:\n%s", tc.version, out)
				}
				if !strings.Contains(string(out), "release-version:") {
					t.Fatalf("the refusal must name the check that failed:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("make release-version VERSION=%q: %v\n%s", tc.version, err, out)
			}
		})
	}
}

// make host-artifact must print a path dist actually built, or the smoke
// tests that consume it fail on a file that was never produced.
func TestHostArtifactNamesABinaryDistBuilds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "host-artifact", "VERSION=1.2.3", "GO=go")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make host-artifact: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if want := filepath.Join("dist", "gauntlet_1.2.3_"+runtime.GOOS+"_"+runtime.GOARCH); got != want {
		t.Fatalf("make host-artifact = %q, want %q", got, want)
	}
}

// The README install snippet names the asset from uname rather than asking
// the Makefile, because it runs before anything is built. That only works
// while every platform in PLATFORMS is one the snippet can spell: a release
// adding one its uname mapping does not produce ships an asset a user on that
// platform cannot install, and the smoke tests never see it because they run
// on the CI runner.
func TestReadmeInstallNamesEveryReleasedPlatform(t *testing.T) {
	root := moduleRoot(t)
	readme := readRepoFile(t, filepath.Join(root, "README.md"))
	_, rest, ok := strings.Cut(readme, "\n## Install\n")
	if !ok {
		t.Fatal("README.md has no Install section")
	}
	block, _, _ := strings.Cut(rest, "\n## ")

	asset := strings.Index(block, "asset=\"gauntlet_")
	if asset < 0 {
		t.Fatal("README install script does not build the asset name")
	}
	expr := block[asset:]
	if end := strings.IndexByte(expr, '\n'); end >= 0 {
		expr = expr[:end]
	}

	platforms := releasePlatforms(makefileText(t))
	if len(platforms) == 0 {
		t.Fatal("no platform parsed out of the Makefile's PLATFORMS; this test would pass on nothing")
	}
	for _, platform := range platforms {
		goos, goarch, _ := strings.Cut(platform, "/")
		if !strings.Contains(expr, "uname -s") {
			t.Fatalf("README asset expression %q does not read uname -s, so it cannot name %s", expr, platform)
		}
		if !strings.Contains(expr, "uname -m") {
			t.Fatalf("README asset expression %q does not read uname -m, so it cannot name %s", expr, platform)
		}
		// The lowercased uname -s has to equal the GOOS the asset carries, and
		// the sed has to map some uname -m onto the GOARCH. Both hold for every
		// entry, so a platform outside the vocabulary below is what needs a
		// mapping added, not a test change.
		switch goos {
		case "linux", "darwin":
		default:
			t.Errorf("PLATFORMS ships %s; the README install snippet maps uname -s onto linux and darwin only", platform)
		}
		lower := goarch
		if goarch == "amd64" {
			lower = "x86_64"
		} else if goarch == "arm64" {
			lower = "aarch64"
		} else {
			t.Errorf("PLATFORMS ships %s; the README install snippet maps uname -m with the x86_64 and aarch64 cases only", platform)
		}
		if !strings.Contains(expr, lower) {
			t.Errorf("README asset expression %q does not map uname -m %s, so it names no %s asset", expr, lower, platform)
		}
	}
}

// releasePlatforms returns the os/arch pairs the Makefile builds a binary for.
// PLATFORMS is a variable assignment, not a rule, and its value is continued
// across indented lines.
func releasePlatforms(makefile string) []string {
	lines := strings.Split(makefile, "\n")
	var out []string
	for i, line := range lines {
		if !strings.HasPrefix(line, "PLATFORMS") {
			continue
		}
		value := line
		for _, next := range lines[i+1:] {
			if !strings.HasPrefix(next, "\t") {
				break
			}
			value += " " + next
		}
		for field := range strings.FieldsSeq(value) {
			if strings.Count(field, "/") == 1 && !strings.HasPrefix(field, "$(") {
				out = append(out, field)
			}
		}
		break
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// A doc pointer into the Makefile is a promise that the line a reader lands
// on is the thing the sentence names. Editing the Makefile moved the release
// target out from under one of them, and the reference that caught it is the
// only thing that keeps the rest from going stale the same way.
//
// Every pointer in the document is a case here. Covering two names while five
// more pointed into the same file is how those five survived a build commit
// that moved every one of them, and how TestDocsPointAtTheMakefileLineTheyName
// sat red for a whole pass. A new `Makefile:` citation is a new case.
func TestDocsPointAtTheMakefileLineTheyName(t *testing.T) {
	root := moduleRoot(t)
	lines := strings.Split(makefileText(t), "\n")
	doc := readRepoFile(t, filepath.Join(root, "docs", "THREAT_MODEL.md"))
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "GOVULNCHECK_VERSION", want: "GOVULNCHECK_VERSION"},
		{name: "STATICCHECK_VERSION", want: "STATICCHECK_VERSION"},
		{name: "make release", want: ".PHONY: release"},
		{name: "release-version", want: ".PHONY: release-version"},
		{name: "make repro", want: ".PHONY: repro"},
		{name: "make dist platform check", want: "GOAMD64=$(GOAMD64)"},
		{name: "make doctor", want: ".PHONY: doctor"},
		{name: "member list and archive", want: "git ls-files -z --cached --others --exclude-standard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checked := 0
			prev := 0
			for i := 0; ; {
				at := strings.Index(doc[i:], "Makefile:")
				if at < 0 {
					break
				}
				i += at + len("Makefile:")
				// The sentence that owns the reference is the one naming
				// it, so a reference belongs to a case only when that
				// name is close behind it. The lookback stops at the
				// previous reference, not at a fixed offset: a
				// sentence that cites two targets would otherwise put
				// both names in front of the second pointer, and each
				// case would then demand the other's line.
				from := max(prev, i-200)
				prev = i
				if !strings.Contains(doc[from:i], tc.name) {
					continue
				}
				end := strings.IndexAny(doc[i:], "-)\n,`")
				if end < 0 {
					t.Fatal("unterminated Makefile reference in THREAT_MODEL.md")
				}
				atol, err := strconv.Atoi(doc[i : i+end])
				if err != nil || atol < 1 || atol > len(lines) {
					t.Fatalf("THREAT_MODEL.md has a Makefile reference with no usable line number: %q", doc[i-9:i+end])
				}
				checked++
				if !strings.Contains(lines[atol-1], tc.want) {
					t.Errorf("THREAT_MODEL.md points at Makefile:%d for %s, which reads %q", atol, tc.name, lines[atol-1])
				}
			}
			if checked == 0 {
				t.Fatalf("THREAT_MODEL.md has no Makefile reference that names %s", tc.name)
			}
		})
	}
}

// The race suite compiles the tree but never vets it or checks its
// formatting, so a release gate that runs only the tests can publish a tag
// that `make ci` rejects.
func TestReleaseRunsCheck(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "\nrelease: | clean-tree release-version check test dist artifacts ##") {
		t.Fatal("make release must run check as well as the tests, dist, the artifacts beside the binaries, the version check, and the clean-tree check")
	}
}

// -buildvcs=false is what makes the shipped bytes reproducible, and it is also
// what removes the evidence of what was built: a release from a modified tree
// produces binaries indistinguishable from a clean one, and the tag would name
// source nobody reviewed. The check is order-only so it refuses before the
// suite and the four cross-compiles, and it passes outside a git work tree,
// where a source tarball has no notion of clean.
func TestReleaseRefusesADirtyTree(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "\nrelease: | clean-tree release-version check test dist artifacts ##") {
		t.Fatal("make release must depend on clean-tree first, so a modified tree cannot reach a release asset")
	}
	recipe := makefileRecipe(text, "clean-tree")
	if recipe == "" {
		t.Fatal("Makefile has no clean-tree target")
	}
	for _, want := range []string{
		"git status --porcelain",
		"git rev-parse --is-inside-work-tree",
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make clean-tree missing %q", want)
		}
	}
	if !strings.Contains(recipe, "exit 1") {
		t.Error("make clean-tree must fail the build, not report a dirty tree and carry on")
	}
}

// The inventory a release ships is a CycloneDX document a scanner can
// read, generated from the binaries dist just built, and it carries no build
// directory path: the modules it names are the same on every machine. Both
// it and checksums.txt are written by `artifacts`, so a target that reaches
// the binaries must not skip that.
func TestMakefileReleaseGeneratesCleanSbom(t *testing.T) {
	text := makefileText(t)
	recipe := makefileRecipe(text, "artifacts")
	if recipe == "" {
		t.Fatal("Makefile has no artifacts target: the files beside the binaries are written by the release recipe and nothing else exercises them")
	}
	if !strings.Contains(recipe, "$(GO) run ./cmd/sbom -o $(DIST)/sbom.json -version $(VERSION) $(DIST)/$(BINARY)_*") {
		t.Fatal("make artifacts must write dist/sbom.json from the binaries dist built, through cmd/sbom")
	}
	if !strings.Contains(recipe, "$(BINARY)_* > checksums.txt") {
		t.Fatal("make artifacts must write dist/checksums.txt from the binaries dist built")
	}
	if !strings.Contains(recipe, "sha256sum -c checksums.txt") {
		t.Fatal("make artifacts must verify the checksums it just wrote against the binaries they name")
	}
	if !strings.Contains(recipe, `for f in checksums.txt sbom.json LICENSE; do`) ||
		!strings.Contains(recipe, `[ -s "$$f" ]`) {
		t.Fatal("make artifacts must refuse to report success for a missing or empty checksums.txt, sbom.json or LICENSE; CI checks the first with `test -s`, and no make target reproduced it")
	}
	if !strings.Contains(recipe, "install -m 0644 LICENSE $(DIST)/LICENSE") {
		t.Fatal("make artifacts must copy LICENSE into dist, so a release ships the grant its binaries are offered under")
	}
	if !strings.Contains(text, "rm -f $(DIST)/$(BINARY)_* $(DIST)/checksums.txt $(DIST)/sbom.json $(DIST)/LICENSE") {
		t.Fatal("make dist must remove a previous sbom.json, so a stale inventory cannot ship with new binaries")
	}
}

// The asset name carries the platform and nothing else, so the loop at the
// end of `make dist` is the only thing that can tell a correctly named binary
// from the wrong one. It reads the settings out of the binary the compiler
// stamped there, and the microarchitecture level is part of that: GOAMD64 and
// GOARM64 are exported precisely so a `go env -w` left behind once cannot
// change the shipped bytes, and a check that read only GOOS/GOARCH would pass
// a release built at a different level than the pin names. The comparison has
// to name the make variables rather than a literal, so raising the pin moves
// the check with it.
func TestDistVerifiesTheBuildSettingsInEveryAsset(t *testing.T) {
	text := makefileText(t)
	recipe := makefileRecipe(text, "dist")
	if recipe == "" {
		t.Fatal("Makefile has no dist recipe")
	}
	for _, want := range []string{
		"$(GO) version -m",
		`kv="GOOS=$$goos GOARCH=$$goarch"`,
		"GOAMD64=$(GOAMD64)",
		"GOARM64=$(GOARM64)",
		`[ "$$got" = "$$want" ]`,
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make dist missing %q: the built settings are read out of the binary, not trusted from the asset name", want)
		}
	}
	for _, want := range []string{"export GOAMD64 := v1", "export GOARM64 := v8.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("Makefile missing %q: without the export the level is whatever the host's go env file says", want)
		}
	}
}

// make clean must sweep dist, build binaries, and scratch files.
func TestMakefileCleanRemovesScratchAndBinaries(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, "rm -rf $(DIST) $(BINARY) $(BINARY)_* .scratch") {
		t.Fatal("make clean must remove dist, binaries, and scratch files")
	}
}

// reproFileList is the command make repro builds its member list with, and the
// only thing standing between the archive and a tree's ignored output. tar
// cannot do this job on its own: libarchive matches an `--exclude` pattern with
// no slash against the basename of every path component, so the pattern that
// drops the built binary also drops the `cmd/<binary>` package and the copies
// stop building. GNU tar anchors its patterns and archived a different tree.
const reproFileList = `git ls-files -z --cached --others --exclude-standard`

// reproRecipe returns the recipe of one target: its line through the last line
// indented as its body.
func reproRecipe(t *testing.T, target string) string {
	t.Helper()
	var out []string
	seen := false
	for line := range strings.SplitSeq(makefileText(t), "\n") {
		if strings.HasPrefix(line, target+":") {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break
		}
		out = append(out, line)
	}
	if !seen {
		t.Fatalf("Makefile has no %s recipe", target)
	}
	return strings.Join(out, "\n")
}

// gitIgnores reports whether .gitignore keeps a repository-root path out of
// git's listing, which is the member list the repro archive is built from.
func gitIgnores(t *testing.T, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "--no-index", "--quiet", path)
	cmd.Dir = moduleRoot(t)
	return cmd.Run() == nil
}

// make repro must keep the tree's ignored output out of the archives, and the
// ignore rules live in .gitignore, so the member list has to come from git's
// own reading of them rather than from a second list that can go stale.
func TestMakefileReproExcludesScratchAndCaches(t *testing.T) {
	recipe := reproRecipe(t, "repro")
	if !strings.Contains(recipe, reproFileList) {
		t.Errorf("make repro must take its member list from %q", reproFileList)
	}
	if strings.Contains(recipe, "--exclude=") || strings.Contains(recipe, "--exclude ") {
		t.Error("make repro must not filter the archive with tar excludes: libarchive matches a pattern with no slash against every basename, so one that drops the built binary also drops the package that shares its name")
	}
	ignore := readRepoFile(t, filepath.Join(moduleRoot(t), ".gitignore"))
	for _, want := range []string{".scratch", ".ruff_cache", ".mypy_cache", "__pycache__"} {
		if !strings.Contains(ignore, want) {
			t.Errorf(".gitignore must list %q; the archive is the working tree minus what git ignores", want)
		}
	}
}

// The maintainer scripts run under uv, and `uv sync` in a checkout with a
// pyproject.toml writes a .venv into the repository root. It is host state:
// pyvenv.cfg records the absolute interpreter path, so the two copies make repro
// builds from would not even hold the same bytes, and `make release` refuses a
// tree carrying an untracked path, so a developer who synced once could not cut
// a tag. Nothing builds through it, but it lands in the checkout all the same.
func TestPythonVirtualenvIsIgnoredAndOutOfTheReproArchive(t *testing.T) {
	if !slices.Contains(gitignoreEntries(t), ".venv") {
		t.Error(".gitignore must list .venv/; `uv sync` writes one into the repository root and nothing else keeps it out of the tree")
	}
	// The listing excludes it because .gitignore does, so prove the rule is
	// the one that decides, with a path the directory has.
	if !gitIgnores(t, ".venv/pyvenv.cfg") {
		t.Error(".gitignore no longer ignores .venv: make repro would archive the host's interpreter path into both tree copies")
	}
}

// The archive is the whole working tree, copied to two directories under
// $HOME, so anything a build or a run of this tool leaves in the checkout is
// an input to one copy and not the other. Every .gitignore entry is such a
// file, which is why the member list is git's ignore-aware listing: it cannot
// drift from .gitignore, and a new entry covers the archive as soon as it is
// written.
func TestMakefileReproArchiveMirrorsGitignore(t *testing.T) {
	recipe := reproRecipe(t, "repro")
	if !strings.Contains(recipe, reproFileList) {
		t.Fatalf("make repro must archive what %q lists", reproFileList)
	}
	for _, entry := range gitignorePatterns(t) {
		// The rule has to still bite for a path the entry names, or the
		// entry has stopped keeping its output out of the archive. A
		// directory pattern needs a path inside the directory: git matches
		// no directory itself, and `.gitignore` lists `.scratch/`.
		sample := strings.ReplaceAll(entry, "*", "1")
		if strings.HasSuffix(sample, "/") {
			sample += "content"
		}
		if !gitIgnores(t, sample) {
			t.Errorf(".gitignore entry %q does not ignore %q: it no longer keeps that output out of the archive", entry, sample)
		}
	}
}

// Dropping .env from both lists at once would satisfy the mirror above, and
// .env is the one file here holding credentials: .env.example is the template
// a developer copies, and the copy is theirs.
func TestEnvFileIsIgnoredAndOutOfTheReproArchive(t *testing.T) {
	if !slices.Contains(gitignoreEntries(t), ".env") {
		t.Error(".gitignore must list .env; the copy of .env.example a developer makes holds real tokens")
	}
	if !strings.Contains(reproRecipe(t, "repro"), reproFileList) {
		t.Error("make repro must archive git's ignore-aware listing; the archive is the working tree, tokens and all")
	}
	// The listing excludes .env because .gitignore does, so prove the rule
	// is the one that decides, with a path the file would have.
	if !gitIgnores(t, ".env") {
		t.Error(".gitignore no longer ignores .env: the archive would carry the tokens")
	}
}

// gitignorePatterns returns the .gitignore lines as git reads them, with the
// leading slash that anchors one at the repository root removed and everything
// else, including a trailing slash, kept.
func gitignorePatterns(t *testing.T) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(readRepoFile(t, filepath.Join(moduleRoot(t), ".gitignore")), "\n") {
		entry := strings.TrimSpace(line)
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		out = append(out, strings.TrimPrefix(entry, "/"))
	}
	return out
}

func gitignoreEntries(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, entry := range gitignorePatterns(t) {
		out = append(out, strings.Trim(entry, "/"))
	}
	return out
}

// A shared GOCACHE lets the second copy reuse the first copy's compiled
// objects, since -trimpath makes both builds hash to one cache key, so a build
// that leaked its own directory would still compare equal. Each side needs its
// own. `.gauntlet/` holds a lane worktree per job when this tool runs in its own
// checkout, which has no business in a reproducibility archive or in a commit.
func TestMakefileReproIsolatesBuildCachesAndWorktrees(t *testing.T) {
	recipe := reproRecipe(t, "repro")
	for _, want := range []string{`GOCACHE="$(REPRO_DIR)/a.gocache"`, `GOCACHE="$(REPRO_DIR)/b.gocache"`, reproFileList} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make repro missing %q", want)
		}
	}
	ignore := readRepoFile(t, filepath.Join(moduleRoot(t), ".gitignore"))
	for _, want := range []string{".gauntlet/", ".gauntlet.lock"} {
		if !strings.Contains(ignore, want) {
			t.Errorf(".gitignore must list %q; a run of the tool writes both into the reviewed tree", want)
		}
	}
	if !gitIgnores(t, ".gauntlet/lanes/x") {
		t.Error(".gitignore no longer ignores a lane worktree: the archive would copy one")
	}
}

// A release ships six files out of the build: the four binaries plus the
// checksums.txt and sbom.json beside them. Both are built from the binaries,
// `gauntlet update` reads the first, and a scanner reads the second, so a
// reproducibility check that compares only the binaries leaves two published
// artifacts unverified. The license text a release also uploads is copied out
// of the tree by `artifacts` rather than built, so two copies of that source
// have nothing to disagree about.
// sbom.json is the one with a determinism claim of its own: its serial number
// is hashed from what it describes, and its licenses are resolved out of the
// module cache the build filled. The copies have to build under the release
// asset name, or the glob filling checksums.txt reads names the release never
// ships and the comparison proves nothing about what it names.
func TestMakefileReproComparesTheWholeAssetSet(t *testing.T) {
	recipe := reproRecipe(t, "repro")
	for _, want := range []string{
		`asset="$(BINARY)_$(VERSION)_$$goos_$$goarch"`,
		`sha256sum $(BINARY)_* > checksums.txt`,
		`$(GO) run ./cmd/sbom -o dist/sbom.json -version $(VERSION) dist/$(BINARY)_$(VERSION)_*`,
		`for f in checksums.txt sbom.json; do`,
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make repro missing %q: a release ships checksums.txt and sbom.json beside the binaries, so the comparison has to cover them", want)
		}
	}
}

// The test scratch directory is a test-only concern, so only the targets that
// hand TMPDIR to a `go test` may name test-tmpdir. Both toolchain preflights
// used to, which refused `make build` and `make dist` on a machine with no
// HOME and told the caller the problem was tests.
func TestToolchainPreflightsDoNotRequireTheTestScratchDirectory(t *testing.T) {
	// The prerequisite line is the one that opens a target's rule, the line
	// that carries its order-only dependencies.
	dependencies := func(target string) string {
		text := makefileText(t)
		for line := range strings.SplitSeq(text, "\n") {
			if strings.HasPrefix(line, target+":") {
				return line
			}
		}
		t.Fatalf("Makefile has no rule for %s", target)
		return ""
	}
	for _, target := range []string{"toolchain-min", "toolchain"} {
		if line := dependencies(target); strings.Contains(line, "test-tmpdir") {
			t.Errorf("make %s must not depend on test-tmpdir (%s): it runs no test and needs no scratch directory", target, line)
		}
	}
	for _, target := range []string{"test", "test-pkg", "cover", "tidy", "vet"} {
		if line := dependencies(target); !strings.Contains(line, "test-tmpdir") {
			t.Errorf("make %s must create the test scratch directory it hands to go (%s)", target, line)
		}
	}
}

// make repro must preflight REPRO_DIR so an unset HOME does not wipe root directories.
func TestMakefileReproPreflight(t *testing.T) {
	text := makefileText(t)
	if !strings.Contains(text, `test "$(REPRO_DIR)" != "/.cache/gauntlet/repro"`) {
		t.Fatal(`make repro must verify REPRO_DIR is not unset`)
	}
}

// An exported TMPDIR points at the tmpfs the test rules exist to avoid, and
// `?=` would keep it: make treats an environment variable as already defined.
// The assignments must be `:=`, which a command-line override still beats.
func TestAmbientTMPDIRDoesNotReachARecipe(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target string
		want   string
	}{
		{target: "test-tmpdir", want: `mkdir -p "` + filepath.Join(home, ".cache/gauntlet/test") + `"`},
		{target: "repro", want: `rm -rf "` + filepath.Join(home, ".cache/gauntlet/repro") + `"`},
	} {
		t.Run(tc.target, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", tc.target)
			cmd.Dir = moduleRoot(t)
			cmd.Env = append(os.Environ(), "TMPDIR=/tmp", "REPRO_DIR=/tmp/repro")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make %s dry run: %v\n%s", tc.target, err, out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("make %s with TMPDIR=/tmp in the environment:\n%swant a recipe using %s", tc.target, out, tc.want)
			}
			if strings.Contains(string(out), "/tmp/repro") {
				t.Errorf("make %s acted on REPRO_DIR from the environment: %s", tc.target, out)
			}
		})
	}
}

// Every target a contributor is told to run is a promise that it exists, and
// nothing held the promise: a rename in the Makefile left README.md,
// CONTRIBUTING.md, and the pull-request checklist naming targets that fail with
// "No rule to make target" on the first try of a clean clone.
func TestDocumentedMakeTargetsExist(t *testing.T) {
	declared := makefileTargets(t)
	root := moduleRoot(t)
	docs := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "CONTRIBUTING.md"),
		filepath.Join(root, "AGENTS.md"),
		filepath.Join(root, ".github", "pull_request_template.md"),
	}
	// CHANGELOG.md is history: it records the targets that shipped in a past
	// release, which is not a claim about the current Makefile.
	more, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, more...)

	documented := map[string]bool{}
	for _, doc := range docs {
		for _, name := range documentedMakeInvocations(readRepoFile(t, doc)) {
			documented[name] = true
			if !declared[name] {
				t.Errorf("%s tells a contributor to run `make %s`, which the Makefile does not declare", relativeTo(root, doc), name)
			}
		}
	}
	// The loop every contributor is told to run has to be in that set, or the
	// scan above could pass by reading nothing.
	for _, name := range []string{"build", "test", "test-pkg", "test-fast", "check", "ci", "verify"} {
		if !documented[name] {
			t.Errorf("no document runs `make %s`, so the scan above is reading less than it claims", name)
		}
	}
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// makefileTargets reads the rules a `make <name>` can reach, which is every
// line that starts at column 0 with a name and a colon. A variable assignment
// is not a rule: its colon is followed by `=` or `?=`, never a space.
func makefileTargets(t *testing.T) map[string]bool {
	t.Helper()
	targets := map[string]bool{}
	for line := range strings.Lines(makefileText(t)) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok || name == "" || strings.ContainsAny(name, " \t$(") {
			continue
		}
		if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
			continue
		}
		targets[name] = true
	}
	if len(targets) < 20 {
		t.Fatalf("read %d targets from the Makefile, which cannot be right", len(targets))
	}
	return targets
}

// documentedMakeInvocations returns every `make <target>` a reader could copy,
// which is the ones inside a fenced block or an inline code span. Prose says
// "make a copy" as readily as it says "make test", and only the copyable one
// is a promise about the Makefile.
func documentedMakeInvocations(doc string) []string {
	var out []string
	fenced := false
	for line := range strings.Lines(doc) {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			out = append(out, makeTargetsInLine(line)...)
			continue
		}
		// Splitting on the backtick leaves the code spans at the odd
		// positions; the first and last are whatever surrounds them.
		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			out = append(out, makeTargetsInLine(parts[i])...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func makeTargetsInLine(line string) []string {
	var out []string
	rest := line
	for {
		at := strings.Index(rest, "make ")
		if at < 0 {
			return out
		}
		fields := strings.Fields(rest[at+len("make "):])
		if len(fields) == 0 {
			return out
		}
		rest = strings.Join(fields[1:], " ")
		name := strings.Trim(fields[0], "\"'`(),.;:")
		// An assignment, not a target: `make GO=1` is a variable.
		if name == "" || strings.Contains(name, "=") {
			continue
		}
		out = append(out, name)
	}
}

// Three places build the shipped binary: the Makefile's `build`, the
// screenshot script, and the suggester calibrator. The first two used to
// spell the go invocation out, which meant the tag set, the ldflags, and the
// build environment had three owners; the calibrator had drifted furthest and
// was scoring a binary no release ships. One build command, called by all
// three, is what keeps a number or a screenshot comparable to the last one.
func TestMaintainerScriptsBuildThroughTheMakefile(t *testing.T) {
	root := moduleRoot(t)
	calibrate := readRepoFile(t, filepath.Join(root, "scripts", "suggest-calibrate.py"))
	shots := readRepoFile(t, filepath.Join(root, "scripts", "shots.sh"))

	if !strings.Contains(calibrate, `"--no-print-directory", "build"`) {
		t.Error("scripts/suggest-calibrate.py must run `make build` rather than a go build of its own: the tag set, ldflags, and build environment have one owner, the Makefile")
	}
	if strings.Contains(calibrate, `"-buildvcs=false"`) {
		t.Error("scripts/suggest-calibrate.py must not spell out a go build; a second copy of the flags is a second thing to forget")
	}

	// shots.sh cannot call `make build`: it needs `go test` to write the
	// frames, not a linked binary. It does have to name the same tag set the
	// Makefile's default does, or a picture is drawn from a build flavor no
	// release ships and nothing downstream notices.
	tags := makefileDefault(makefileText(t), "TAGS")
	if tags == "" {
		t.Fatal("Makefile does not give TAGS a default")
	}
	if want := "-tags " + tags; !strings.Contains(shots, want) {
		t.Errorf("scripts/shots.sh must build the frames with %q, the Makefile's default tag set", want)
	}
}

// makefileDefault reads the default value of a Makefile variable, so a
// reference to it names the tree's setting rather than a value typed again
// where it is checked.
func makefileDefault(makefile, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s+\?=\s*(\S+)$`)
	m := re.FindStringSubmatch(makefile)
	if m == nil {
		return ""
	}
	return m[1]
}

// A new machine finds out what it is missing one target at a time otherwise:
// the Go minimum belongs to toolchain-min, the C compiler to test-cgo, and
// uvx and shellcheck to check-scripts, which `make verify` reaches only after
// `make check` has run. make doctor answers the whole question in one run, and
// it reports every gap before it fails, since a contributor told one missing
// tool at a time re-runs it once per tool, which is the loop it exists to end.
func TestMakefileDoctorPreflightsEveryPrerequisite(t *testing.T) {
	text := makefileText(t)
	recipe := makefileRecipe(text, "doctor")
	if recipe == "" {
		t.Fatal("Makefile must declare a doctor target: one run that names every missing prerequisite")
	}
	// Each prerequisite its own target already preflights, so `make doctor`
	// is a fold-in of the existing checks rather than a second, driftable
	// opinion about what this repository needs.
	for _, want := range []string{
		`awk '$$1 == "go" { print $$2; exit }' go.mod`, // the Go minimum, as toolchain-min reads it
		`command -v "$(GO)"`,                           // go on PATH
		`cc=$$($(GO) env CC)`,                          // the C compiler, as test-cgo reads it
		`command -v "$$cc"`,                            //
		`[ -x /usr/bin/sandbox-exec ]`,
		"command -v git",        //
		"command -v uvx",        // as check-scripts reads it
		"command -v shellcheck", // as check-scripts reads it
		`mkdir -p "$(TMPDIR)"`,  // the test scratch directory test-tmpdir creates
		"command -v tar",        // the archive repro cuts twice
		"command -v cmp",        // the comparison repro ends on
		"command -v sha256sum",  // the checksum artifacts writes and verifies
	} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make doctor must check %q", want)
		}
	}
	// Counting the gaps rather than exiting at the first one is the whole
	// point: a recipe that stops at the first miss sends the reader round the
	// loop once per missing tool.
	if !strings.Contains(recipe, "missing=$$((missing + 1))") {
		t.Error("make doctor must count a missing prerequisite and report every one before it fails")
	}
	if !strings.Contains(recipe, `if [ "$$missing" -gt 0 ]; then`) {
		t.Error("make doctor must exit nonzero only after reporting every missing prerequisite")
	}
	// A gap is only actionable if it says what to install and which targets
	// need it.
	if !strings.Contains(recipe, "install: %s") || !strings.Contains(recipe, "needed by: %s") {
		t.Error("make doctor must name what to install and which targets need each missing prerequisite")
	}
	// The pins it prints are the ones CI installs, read from this Makefile
	// rather than typed again where they are reported.
	for _, want := range []string{"$(UV_VERSION)", "$(SHELLCHECK_VERSION)"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make doctor must report the CI pin %q rather than a value of its own", want)
		}
	}
	// A target nobody is told about is the one nobody runs.
	if doc := readRepoFile(t, filepath.Join(moduleRoot(t), "CONTRIBUTING.md")); !strings.Contains(doc, "make doctor") {
		t.Error("CONTRIBUTING.md must tell a contributor to run `make doctor` before the edit-test loop")
	}
	// The three tools the release-path targets need and nothing else
	// preflights: a gap there otherwise arrives as a raw tar or shasum
	// error, after a contributor has read a target that promised to check
	// this machine.
	for _, want := range []string{`"repro"`, `"artifacts, release"`} {
		if !strings.Contains(recipe, want) {
			t.Errorf("make doctor must name the target that needs %s when that tool is missing", want)
		}
	}
}

// makefileRecipe, which deps_test.go owns, reads the lines of one target
// without its rule line; the doctor test above is its second caller, which is
// what keeps that helper from drifting to one caller's shape.
