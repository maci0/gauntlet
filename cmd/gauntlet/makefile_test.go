// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	if !strings.Contains(text, `install -d "$(HOME)/.local/bin"`) {
		t.Fatal(`make install must quote destination path: HOME can contain spaces`)
	}
	if !strings.Contains(text, "is not on PATH") {
		t.Fatal("make install must say when ~/.local/bin is not on PATH")
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

// The Makefile exports the build environment, so a test that shells out to it
// inherits a command line the caller never wrote.
func cleanMakeEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "MAKEFLAGS", "MFLAGS", "MAKELEVEL":
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
func TestDocsPointAtTheMakefileLineTheyName(t *testing.T) {
	root := moduleRoot(t)
	lines := strings.Split(makefileText(t), "\n")
	doc := readRepoFile(t, filepath.Join(root, "docs", "THREAT_MODEL.md"))
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "GOVULNCHECK_VERSION", want: "GOVULNCHECK_VERSION"},
		{name: "make release", want: ".PHONY: release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checked := 0
			for i := 0; ; {
				at := strings.Index(doc[i:], "Makefile:")
				if at < 0 {
					break
				}
				i += at + len("Makefile:")
				// The sentence that owns the reference is the one naming
				// it, so a reference belongs to a case only when that
				// name is close behind it.
				if from := max(0, i-200); !strings.Contains(doc[from:i], tc.name) {
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
	if !strings.Contains(text, "\nrelease: check test dist ##") {
		t.Fatal("make release must run check as well as the tests and dist")
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

// A shared GOCACHE lets the second copy reuse the first copy's compiled
// objects, since -trimpath makes both builds hash to one cache key, so a build
// that leaked its own directory would still compare equal. Each side needs its
// own. `.gauntlet/` holds a lane worktree per job when this tool runs in its own
// checkout, which has no business in a reproducibility archive or in a commit.
func TestMakefileReproIsolatesBuildCachesAndWorktrees(t *testing.T) {
	text := makefileText(t)
	for _, want := range []string{`GOCACHE="$(REPRO_DIR)/a.gocache"`, `GOCACHE="$(REPRO_DIR)/b.gocache"`, "--exclude=./.gauntlet"} {
		if !strings.Contains(text, want) {
			t.Errorf("make repro missing %q", want)
		}
	}
	ignore := readRepoFile(t, filepath.Join(moduleRoot(t), ".gitignore"))
	for _, want := range []string{".gauntlet/", ".gauntlet.lock"} {
		if !strings.Contains(ignore, want) {
			t.Errorf(".gitignore must list %q; a run of the tool writes both into the reviewed tree", want)
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
