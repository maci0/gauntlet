// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maci0/gauntlet/internal/sbom"
)

// goCmdTimeout bounds every `go` subprocess the dependency tests run. Two of
// them reach the network (`go mod download`, and the `go list` that asks for a
// module's directory), and on a machine that cannot answer one waits out its
// own retry schedule rather than failing: the package then sits in a wedged
// state until the whole binary is killed, and the failure it eventually
// reports is a timeout rather than the thing that broke. A slow toolchain on a
// cold cache is not slow enough to need minutes, so the bound is far above a
// healthy run and far below a hang.
const goCmdTimeout = 5 * time.Minute

// goCmd starts one `go` command in dir under a deadline, so a toolchain that
// stops answering fails the test that asked for it instead of stalling every
// test queued behind it.
func goCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), goCmdTimeout)
	t.Cleanup(cancel)
	return exec.CommandContext(ctx, "go", args...)
}

// Direct modules in go.mod are the supply-chain surface this repository
// chose. An unused one still downloads, still hashes, and still sits in the
// module graph; dropping it is the fix, and this test is how a leftover is
// found before it ships. Test files do not count: a module imported only
// from _test.go is a test dependency listed as production.
func TestDirectModulesAreImported(t *testing.T) {
	root := moduleRoot(t)
	direct := directModules(t, root)
	if len(direct) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	used := importedModules(t, root, direct)
	for _, path := range direct {
		if !used[path] {
			t.Errorf("go.mod requires %s, but no non-test .go file imports it; remove the unused module", path)
		}
	}
}

// directModuleSites is the "Contained by" column in docs/DESIGN.md. A new
// direct module must appear here with the packages allowed to import it;
// an import outside those prefixes is the coupling that column exists to
// prevent.
var directModuleSites = map[string][]string{
	"github.com/charmbracelet/bubbletea": {"internal/ui/"},
	"github.com/charmbracelet/lipgloss":  {"internal/ui/"},
	"github.com/muesli/termenv":          {"internal/ui/"},
	"github.com/maci0/toktop":            {"cmd/gauntlet/", "internal/runner/"},
	"github.com/rivo/uniseg":             {"cmd/gauntlet/", "internal/ui/", "internal/report/", "internal/agent/", "internal/normalize/"},
	"golang.org/x/text":                  {"cmd/gauntlet/", "internal/evidence/", "internal/fuzzy/", "internal/prompt/", "internal/runner/", "internal/ui/"},
	"golang.org/x/term":                  {"cmd/gauntlet/"},
}

// TestDirectModuleImportSites fails when a direct module is imported from
// a package docs/DESIGN.md does not allow, when a new direct module has no
// containment entry, or when an entry outlives the require. toktop is
// extra-constrained: every import site must carry a notoktop build tag, or
// `-tags notoktop` would not drop it.
func TestDirectModuleImportSites(t *testing.T) {
	root := moduleRoot(t)
	direct := directModules(t, root)
	for _, path := range direct {
		if _, ok := directModuleSites[path]; !ok {
			t.Errorf("go.mod requires %s, but directModuleSites has no allowed import prefixes; add the docs/DESIGN.md containment", path)
		}
	}
	for path := range directModuleSites {
		if !slices.Contains(direct, path) {
			t.Errorf("directModuleSites lists %s, which is not a direct module; drop the stale entry", path)
		}
	}

	fset := token.NewFileSet()
	err := walkGoFiles(root, func(rel, path string) error {
		body := readRepoFile(t, path)
		f, err := parser.ParseFile(fset, path, body, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for mod, prefixes := range directModuleSites {
				if imp != mod && !strings.HasPrefix(imp, mod+"/") {
					continue
				}
				ok := false
				for _, p := range prefixes {
					if strings.HasPrefix(rel, p) {
						ok = true
						break
					}
				}
				if !ok {
					t.Errorf("%s imports %s; docs/DESIGN.md allows that module in %s", rel, mod, strings.Join(prefixes, ", "))
				}
				if mod == "github.com/maci0/toktop" && !fileHasBuildTag(body, "notoktop") {
					t.Errorf("%s imports toktop without a notoktop build tag; -tags notoktop would not drop it", rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A direct module pinned to a pseudo-version is an unpublished commit in
// the production graph. Transitives inherit whatever their parents asked
// for; the modules this repository chose must be tagged releases.
func TestDirectModulesAreTagged(t *testing.T) {
	root := moduleRoot(t)
	reqs := directReqs(t, root)
	if len(reqs) == 0 {
		t.Fatal("go.mod listed no direct modules")
	}
	for _, r := range reqs {
		if pseudoVersion.MatchString(r.version) {
			t.Errorf("go.mod requires %s %s, a pseudo-version; pin a tagged release", r.path, r.version)
		}
	}
}

// docs/DESIGN.md says every linked module is MIT or BSD-3-Clause. The
// modules this repository chose are one half of that claim, but the graph
// they drag in is the other half, and an unnamed transitive is an
// unaudited one: the LICENSE files of everything a release links into are
// what the claim is checked against, before a require lands and again on
// every version bump.
func TestLinkedModuleLicenses(t *testing.T) {
	root := moduleRoot(t)
	mods := shippedModules(t, root)
	dirs := moduleDirs(t, root, moduleReqs(mods))
	for _, mod := range mods {
		dir := dirs[mod]
		if dir == "" {
			t.Errorf("%s: go list -m did not report a module directory", mod)
			continue
		}
		body := readLicense(t, dir, mod)
		switch kind := licenseKind(body); kind {
		case "MIT", "BSD-3-Clause":
		default:
			t.Errorf("%s license is %s, not MIT or BSD-3-Clause; docs/DESIGN.md requires a check before adoption", mod, kind)
		}
	}
}

// docs/DESIGN.md inventories what a release ships. A module that links into
// a shipped build and is named nowhere is an anonymous addition to the
// binary, and a row left behind after a dependency leaves is a claim about
// the supply chain that is no longer true. Both directions are checked
// against the table itself, so an edit has to keep the whole surface honest.
func TestDesignDocumentsLinkedModules(t *testing.T) {
	root := moduleRoot(t)
	design := readRepoFile(t, filepath.Join(root, "docs", "DESIGN.md"))
	documented := designModules(design)

	shipped := shippedModules(t, root)
	for _, mod := range shipped {
		if !documented[designModuleName(mod)] {
			t.Errorf("docs/DESIGN.md does not name %s, which links into a shipped build; the dependency inventory is the record of what ships", mod)
		}
	}

	direct := make(map[string]bool)
	for _, path := range directModules(t, root) {
		direct[designModuleName(path)] = true
	}
	for name := range documented {
		if !direct[name] && !slices.ContainsFunc(shipped, func(mod string) bool { return designModuleName(mod) == name }) {
			t.Errorf("docs/DESIGN.md names %s, which no shipped build links; drop the stale row", name)
		}
	}
}

// shipTagSets are the build-tag configurations a release ships, in the order
// the Makefile documents them. A module linked by only some of them still
// ships, so the inventory is the union: a release covers all three.
var shipTagSets = []string{"sqlite", "", "notoktop"}

// shipTargets are the platforms a release ships, matching the assets the
// dist target publishes. The module graph is resolved per target: a
// transitive only the darwin build links — `ncruces/go-strftime`, reached
// through `modernc.org/libc` — is still in a binary that ships, so the
// inventory unions the targets instead of reporting what the host resolves.
// Without this, the same commit passes on Linux and fails on macOS, and a row
// added for the darwin build reads as stale on Linux.
var shipTargets = []struct {
	goos   string
	goarch string
}{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

const mainModule = "github.com/maci0/gauntlet"

// shippedModules returns every third-party module whose packages link into a
// build under any shipped tag set, on any shipped platform. go.mod lists more
// than this: the modules it requires to resolve the graph but no binary
// imports. The difference is the point, since the linked set is what a
// consumer's binary contains.
func shippedModules(t *testing.T, root string) []string {
	t.Helper()
	seen := make(map[string]bool)
	for _, tags := range shipTagSets {
		for _, target := range shipTargets {
			for _, mod := range linkedModules(t, root, tags, target.goos, target.goarch) {
				seen[mod] = true
			}
		}
	}
	mods := slices.Sorted(maps.Keys(seen))
	if len(mods) == 0 {
		t.Fatal("no module links into any shipped build")
	}
	return mods
}

// linkedModules reports the modules of the packages the build imports under
// the given build tags for the given platform, the main module excluded. An
// empty tag set is the TAGS= build, and -tags= says so as explicitly as an
// empty string.
func linkedModules(t *testing.T, root, tags, goos, goarch string) []string {
	t.Helper()
	cmd := goCmd(t, root, "list", "-tags="+tags, "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./...")
	cmd.Dir = root
	// GOOS/GOARCH are the target, not the host: the release is cross-compiled
	// with CGO_ENABLED=0, and `go list` resolves the imports that build sees.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -tags=%s -deps for %s/%s: %v", tags, goos, goarch, err)
	}
	seen := make(map[string]bool)
	var mods []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		mod := strings.TrimSpace(line)
		if mod == "" || mod == mainModule || seen[mod] {
			continue
		}
		seen[mod] = true
		mods = append(mods, mod)
	}
	slices.Sort(mods)
	return mods
}

func moduleReqs(paths []string) []moduleReq {
	out := make([]moduleReq, len(paths))
	for i, path := range paths {
		out[i] = moduleReq{path: path}
	}
	return out
}

// designModuleName is how docs/DESIGN.md writes a module path: the GitHub
// host is noise in a table of module names, and the golang.org and
// modernc.org paths are the module identity, not a prefix over it.
func designModuleName(mod string) string {
	return strings.TrimPrefix(mod, "github.com/")
}

// designModules returns the module column of the External dependencies
// tables. The section is delimited so the other tables in the document, which
// name packages and not modules, are not read as dependency rows.
func designModules(design string) map[string]bool {
	mods := make(map[string]bool)
	inSection := false
	for raw := range strings.SplitSeq(design, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "## "):
			inSection = line == "## External dependencies"
		case !inSection, !strings.HasPrefix(line, "|"):
			continue
		}
		// The leading pipe is the row delimiter, not a column, so the
		// module name is the cell after it.
		row := strings.TrimPrefix(line, "|")
		name, _, ok := strings.Cut(row, "|")
		name = strings.Trim(strings.TrimSpace(name), "`")
		if !ok || !strings.Contains(name, "/") {
			continue
		}
		mods[name] = true
	}
	return mods
}

// The screenshot renderer and the scripts CI job must resolve the same rich.
// `uv run scripts/shots/render.py` reads the PEP 723 header; the mypy step
// passes `--with rich==...`. A floating header would screenshot with a
// different renderer than CI typechecks.
func TestShotRendererRichPinMatchesCI(t *testing.T) {
	root := moduleRoot(t)
	script := readRepoFile(t, filepath.Join(root, "scripts", "shots", "render.py"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	scriptVer := richPin(script)
	ciVer := richPin(ci)
	if scriptVer == "" {
		t.Fatal("scripts/shots/render.py does not pin rich==VERSION in its PEP 723 header")
	}
	if ciVer == "" {
		t.Fatal(".github/workflows/ci.yml does not pin rich==VERSION")
	}
	if scriptVer != ciVer {
		t.Errorf("rich pin mismatch: render.py has %s, ci.yml has %s; bump them together", scriptVer, ciVer)
	}
}

// check-scripts must use the same ruff, mypy, rich, and yamllint pins as the
// scripts job. A Makefile that lints with whatever is on PATH, or a CI bump
// that forgets the Makefile, is an after-push failure.
func TestScriptsToolPinsMatchCI(t *testing.T) {
	root := moduleRoot(t)
	makefile := readRepoFile(t, filepath.Join(root, "Makefile"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	script := readRepoFile(t, filepath.Join(root, "scripts", "shots", "render.py"))

	checks := []struct {
		name, makefile, ci string
	}{
		{"ruff", makefilePin(makefile, "RUFF_VERSION"), toolAtPin(ci, "ruff")},
		{"mypy", makefilePin(makefile, "MYPY_VERSION"), toolAtPin(ci, "mypy")},
		{"rich", makefilePin(makefile, "RICH_VERSION"), richPin(ci)},
		{"yamllint", makefilePin(makefile, "YAMLLINT_VERSION"), toolAtPin(ci, "yamllint")},
		{"uv", makefilePin(makefile, "UV_VERSION"), uvSetupPin(ci)},
		{"shellcheck", makefilePin(makefile, "SHELLCHECK_VERSION"), shellcheckPin(ci)},
	}
	for _, c := range checks {
		if c.makefile == "" {
			t.Errorf("Makefile does not set %s_VERSION", strings.ToUpper(c.name))
			continue
		}
		if c.ci == "" {
			t.Errorf("ci.yml does not pin %s", c.name)
			continue
		}
		if c.makefile != c.ci {
			t.Errorf("%s pin mismatch: Makefile has %s, ci.yml has %s; bump them together", c.name, c.makefile, c.ci)
		}
	}
	if scriptVer := richPin(script); scriptVer != makefilePin(makefile, "RICH_VERSION") {
		t.Errorf("rich pin mismatch: render.py has %s, Makefile has %s", scriptVer, makefilePin(makefile, "RICH_VERSION"))
	}
}

// Matching pins are not the same as running the same checks: the two can
// agree on every version and still differ on a flag, so a rule enabled
// locally reads as passing while the pull request runs something else.
// Every analysis command in check-scripts, pins resolved, must appear in the
// scripts job.
func TestScriptsChecksMatchCI(t *testing.T) {
	root := moduleRoot(t)
	makefile := readRepoFile(t, filepath.Join(root, "Makefile"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	cmds := checkScriptsCommands(makefile)
	if len(cmds) == 0 {
		t.Fatal("check-scripts runs no analysis commands")
	}
	for _, cmd := range cmds {
		if !strings.Contains(ci, cmd) {
			t.Errorf("check-scripts runs %q, which ci.yml does not: local and CI enforce different rules", cmd)
		}
	}
}

// The other direction. A check added to the scripts job and not to
// check-scripts runs in CI and nowhere else, so the local gate a contributor
// runs before pushing never sees it and a failure arrives on the pull request.
// Every analysis command in the job must appear in the recipe, pins read back
// as the Makefile's own references.
func TestScriptsJobChecksAreReproducedLocally(t *testing.T) {
	root := moduleRoot(t)
	makefile := readRepoFile(t, filepath.Join(root, "Makefile"))
	ci := readRepoFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	recipe := makefileRecipe(makefile, "check-scripts")

	steps := 0
	for line := range strings.SplitSeq(ci, "\n") {
		cmd, ok := analysisCommand(line)
		if !ok {
			continue
		}
		steps++
		if local := unresolveMakeVars(cmd, makefile); !strings.Contains(recipe, local) {
			t.Errorf("ci.yml runs %q, which make check-scripts does not: the local gate misses a check CI enforces", cmd)
		}
	}
	if steps == 0 {
		t.Fatal("ci.yml runs no analysis commands, so the comparison is over nothing")
	}
}

// lintOwner names the tool that has an opinion about a file extension, and so
// the path a file of that extension must sit under to be analyzed at all.
var lintOwner = map[string][]string{
	".py":   {"ruff", "mypy"},
	".sh":   {"shellcheck"},
	".yml":  {"yamllint"},
	".yaml": {"yamllint"},
}

// Agreement on the commands is not coverage of the tree. The shellcheck
// command names one file, so a second script beside it is analyzed by nothing,
// and nothing turns red when it arrives. Every tracked file a tool can judge
// has to sit under a path that tool is given, which is the same invariant the
// direct-module tests hold for import sites.
func TestAnalysisScopesCoverEveryLintedFile(t *testing.T) {
	root := moduleRoot(t)
	scopes := analysisScopes(readRepoFile(t, filepath.Join(root, "Makefile")))

	for _, tool := range []string{"ruff", "mypy", "shellcheck", "yamllint"} {
		if len(scopes[tool]) == 0 {
			t.Errorf("check-scripts never points %s at a path, so nothing of the language it owns is analyzed", tool)
		}
	}

	var covered int
	for _, file := range trackedFiles(t, root) {
		owners, ok := lintOwner[strings.ToLower(filepath.Ext(file))]
		if !ok {
			continue
		}
		for _, tool := range owners {
			if underScope(file, scopes[tool]) {
				covered++
				break
			}
			t.Errorf("%s: no %s path in check-scripts covers it; add it, or move it under one of %v", file, tool, scopes[tool])
		}
	}
	if covered == 0 {
		t.Fatal("no tracked file carries an extension an analysis tool owns, so the comparison is over nothing")
	}
}

// underScope reports whether a repository-relative file is one of the paths a
// tool was given, or sits under one of them.
func underScope(file string, paths []string) bool {
	for _, path := range paths {
		if file == path || strings.HasPrefix(file, path+"/") {
			return true
		}
	}
	return false
}

// analysisScopes maps each analysis tool in the check-scripts recipe to the
// paths it is pointed at, read as the last non-flag field of the command: the
// tool's own flags, its version pin, and the subcommand that selects the check
// all come before the path. The preflight lines that print what CI would run
// open with `echo` and are not commands, so they contribute nothing.
func analysisScopes(makefile string) map[string][]string {
	scopes := map[string][]string{}
	for line := range strings.SplitSeq(makefileRecipe(makefile, "check-scripts"), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		tool, path := "", ""
		for i, f := range fields {
			switch {
			case f == "shellcheck":
				tool = "shellcheck"
			case strings.HasPrefix(f, "ruff@"):
				tool = "ruff"
			case strings.HasPrefix(f, "mypy@"):
				tool = "mypy"
			case strings.HasPrefix(f, "yamllint@"):
				tool = "yamllint"
			case tool == "", strings.HasPrefix(f, "-"), i != len(fields)-1:
				continue
			default:
				path = f
			}
		}
		if tool == "" || path == "" || slices.Contains(scopes[tool], path) {
			continue
		}
		scopes[tool] = append(scopes[tool], path)
	}
	for tool := range scopes {
		slices.Sort(scopes[tool])
	}
	return scopes
}

// trackedFiles lists the files git has, which is the set the tree ships: a
// scratch directory or a build output is not analyzed because it is not part
// of it.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files := strings.Fields(string(out))
	if len(files) == 0 {
		t.Fatalf("git ls-files in %s returned nothing", root)
	}
	return files
}

// analysisCommand returns the analysis command a workflow line runs, however
// the step wraps it: a run step carries the `- run:` key, and a step with a
// name puts the name on the previous line. A comment, and the `echo` that
// prints which tool version a runner image carries, run no check.
func analysisCommand(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	for _, tool := range []string{"uvx ", "shellcheck "} {
		i := strings.Index(trimmed, tool)
		if i < 0 {
			continue
		}
		cmd := trimmed[i:]
		if strings.HasPrefix(trimmed[:i], "echo ") {
			continue
		}
		return cmd, true
	}
	return "", false
}

// unresolveMakeVars replaces a resolved pin in a CI command with the Makefile
// reference the recipe is written with, so the two are compared as written
// rather than as spelled by three different files.
func unresolveMakeVars(cmd, makefile string) string {
	for _, name := range []string{"RUFF_VERSION", "MYPY_VERSION", "RICH_VERSION", "YAMLLINT_VERSION"} {
		if v := makefilePin(makefile, name); v != "" {
			cmd = strings.ReplaceAll(cmd, v, "$("+name+")")
		}
	}
	return cmd
}

// checkScriptsCommands returns the analysis commands of the check-scripts
// recipe with $(NAME)_VERSION references resolved to the values the Makefile
// sets. Commands that are not analysis (the preflight probes, which only
// print what CI would run) are skipped.
func checkScriptsCommands(makefile string) []string {
	recipe := makefileRecipe(makefile, "check-scripts")
	cmds := make([]string, 0, 4)
	for line := range strings.SplitSeq(recipe, "\n") {
		cmd := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(cmd, "uvx "), strings.HasPrefix(cmd, "shellcheck "):
		default:
			continue
		}
		cmds = append(cmds, resolveMakeVars(cmd, makefile))
	}
	return cmds
}

// resolveMakeVars replaces $(NAME_VERSION) with the Makefile's assignment.
func resolveMakeVars(cmd, makefile string) string {
	for _, name := range []string{"RUFF_VERSION", "MYPY_VERSION", "RICH_VERSION", "YAMLLINT_VERSION"} {
		if v := makefilePin(makefile, name); v != "" {
			cmd = strings.ReplaceAll(cmd, "$("+name+")", v)
		}
	}
	return cmd
}

// makefileRecipe returns the lines of the named target, from the rule to the
// next rule that starts in column zero.
func makefileRecipe(makefile, target string) string {
	lines := strings.Split(makefile, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, target+":") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	// A target can declare itself twice, once with its prerequisites and once
	// with the help text, and the recipe follows both: `dist` is
	// `dist: | toolchain` and then `dist: ## build every release platform`.
	// Stopping at the second line would hand back an empty recipe.
	for start < len(lines) && strings.HasPrefix(lines[start], target+":") {
		start++
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if line := lines[i]; line != "" && !strings.HasPrefix(line, "\t") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod: %v", root, err)
	}
	return root
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type moduleReq struct {
	path, version string
}

func directModules(t *testing.T, root string) []string {
	t.Helper()
	reqs := directReqs(t, root)
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.path
	}
	return out
}

func directReqs(t *testing.T, root string) []moduleReq {
	t.Helper()
	var out []moduleReq
	inRequire := false
	for raw := range strings.SplitSeq(readRepoFile(t, filepath.Join(root, "go.mod")), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "require (":
			inRequire = true
		case inRequire && line == ")":
			inRequire = false
		case strings.HasPrefix(line, "require ") && !strings.HasPrefix(line, "require ("):
			if r, ok := requireModule(line); ok {
				out = append(out, r)
			}
		case inRequire:
			if r, ok := requireModule(line); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

func requireModule(line string) (moduleReq, bool) {
	if strings.Contains(line, "// indirect") {
		return moduleReq{}, false
	}
	if i := strings.Index(line, "//"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) > 0 && fields[0] == "require" {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		return moduleReq{}, false
	}
	return moduleReq{path: fields[0], version: fields[1]}, true
}

func importedModules(t *testing.T, root string, modules []string) map[string]bool {
	t.Helper()
	used := make(map[string]bool, len(modules))
	fset := token.NewFileSet()
	err := walkGoFiles(root, func(rel, path string) error {
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range f.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for _, mod := range modules {
				if imp == mod || strings.HasPrefix(imp, mod+"/") {
					used[mod] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func walkGoFiles(root string, fn func(rel, path string) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), path)
	})
}

func fileHasBuildTag(body, tag string) bool {
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if !strings.HasPrefix(line, "//go:build") && !strings.HasPrefix(line, "// +build") {
			continue
		}
		if strings.Contains(line, tag) {
			return true
		}
	}
	return false
}

func moduleDirs(t *testing.T, root string, reqs []moduleReq) map[string]string {
	t.Helper()
	paths := make([]string, 0, len(reqs))
	for _, r := range reqs {
		paths = append(paths, r.path)
	}
	dirs := listModuleDirs(t, root, paths)
	// A module nothing imports under the active build tags is never
	// downloaded by the build, so go list -m reports no Dir for it.
	// Fetch the stragglers into the cache and ask again.
	var missing []string
	for _, path := range paths {
		if dirs[path] == "" {
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return dirs
	}
	cmd := goCmd(t, root, append([]string{"mod", "download"}, missing...)...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod download %s: %v\n%s", strings.Join(missing, " "), err, out)
	}
	maps.Copy(dirs, listModuleDirs(t, root, missing))
	return dirs
}

func listModuleDirs(t *testing.T, root string, paths []string) map[string]string {
	t.Helper()
	cmd := goCmd(t, root, append([]string{"list", "-m", "-f", "{{.Path}}\t{{.Dir}}"}, paths...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go list -m: %v\n%s", err, stderr)
	}
	dirs := make(map[string]string, len(paths))
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		path, dir, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("go list -m: unexpected line %q", line)
		}
		dirs[path] = dir
	}
	return dirs
}

func readLicense(t *testing.T, dir, module string) string {
	t.Helper()
	for _, name := range sbom.LicenseFileNames() {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return string(b)
		}
	}
	t.Fatalf("%s ships no grant in %s under any of %v", module, dir, sbom.LicenseFileNames())
	return ""
}

// licenseKind names the license of a linked module's LICENSE file, so a
// verdict that is neither MIT nor BSD-3-Clause names the license it found
// instead of reporting "unknown".
func licenseKind(body string) string {
	switch {
	case strings.Contains(body, "Redistribution and use in source and binary forms") &&
		nonEndorsement.MatchString(body):
		return "BSD-3-Clause"
	case strings.Contains(body, "Redistribution and use in source and binary forms"):
		return "BSD-2-Clause"
	// A BSD license with the endorsement clause (BSD-4-Clause, AFL-3.0) also
	// opens with the MIT grant, so the MIT case has to come last and to
	// refuse a body carrying that clause. Testing it first reported those as
	// MIT and admitted them to go.mod.
	case strings.Contains(body, "MIT License"):
		return "MIT"
	case strings.Contains(body, "Permission is hereby granted") &&
		!nonEndorsement.MatchString(body):
		return "MIT"
	default:
		return "unknown"
	}
}

var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

// nonEndorsement is the third BSD condition, the one that separates
// BSD-3-Clause from BSD-2-Clause. Projects word it with a singular or a
// plural name ("Neither the name of ...", "Neither the names of ..."), so
// the marker is the endorsement clause, not the count of bullets.
var nonEndorsement = regexp.MustCompile(`(?i)neither the names? of`)

var richPinned = regexp.MustCompile(`rich==([0-9][0-9A-Za-z._-]*)`)

func richPin(text string) string {
	m := richPinned.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// makefilePin reads a `NAME ?= VERSION` line. The leading v is optional: a
// Go module version carries one and a Python or shell tool version does not,
// and both spellings are a version the recipe pastes into a command.
func makefilePin(text, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` \?= (v?[0-9][0-9A-Za-z._-]*)$`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

func toolAtPin(text, tool string) string {
	re := regexp.MustCompile(regexp.QuoteMeta(tool) + `@([0-9][0-9A-Za-z._-]*)`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// uvSetupPin extracts the version from the astral-sh/setup-uv action's
// `version:` field (format: version: "0.12.6").
var uvVersionField = regexp.MustCompile(`(?m)^\s+version:\s+"([0-9][0-9A-Za-z._-]*)"`)

func uvSetupPin(text string) string {
	m := uvVersionField.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// shellcheck is the one lint tool the scripts job cannot install a pinned
// copy of, so its version is recorded in the step's env instead of an
// argument. That makes it the pin the two files must agree on, and the one
// `make check-scripts` warns about when the local copy drifts.
var shellcheckVersionField = regexp.MustCompile(`(?m)^\s+SHELLCHECK_VERSION:\s+"([0-9][0-9A-Za-z._-]*)"`)

func shellcheckPin(text string) string {
	m := shellcheckVersionField.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}
