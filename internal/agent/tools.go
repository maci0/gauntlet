// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"strings"

	"github.com/maci0/gauntlet/internal/fuzzy"
)

// CoreTools are the binaries every review is told to reach for: the search and
// rewrite tools the injected rules name, plus the git the runner drives.
// "a|b" means either binary satisfies the check.
var CoreTools = []struct{ Name, Purpose string }{
	{"git", "line stats, and required for --jobs worktrees"},
	{"rg", "text search"},
	{"ast-grep|sg", "structural search and rewrite"},
	{"patchwork", "AST-native find/replace"},
	{"semcode", "semantic C/C++/Rust queries"},
}

// ReviewsWithoutTools are bundled reviews with no purpose-built CLI tooling.
// Listed so doctor's review set matches --list instead of silently omitting
// them.
var ReviewsWithoutTools = []string{
	"agentrules-review", "cache-review", "ddd-review", "design-review", "dr-review",
	"dst-review", "dx-review", "functionality-review", "numerics-review",
	"perfectionism-review", "prompt-review", "skills-review", "specs-review",
	"tdd-review", "threat-review", "uislop-review",
}

// RecommendedTools are worth installing on any machine: language-agnostic and
// useful in most repos. Everything else in ReviewTools is ecosystem-specific.
var RecommendedTools = map[string]bool{
	"actionlint": true, "codespell": true, "diffoscope": true, "gitleaks": true,
	"hadolint": true, "hyperfine": true, "jscpd": true, "lychee": true,
	"markdownlint": true, "osv-scanner": true, "semgrep": true, "shellcheck": true,
	"shfmt": true, "tokei": true, "yamllint": true,
}

// ReviewTools are optional per-review helpers, mirroring the "If available,
// use:" lines in the prompts. Entries are binaries, so package-only names
// (Atheris, Jazzer, eslint plugins) and SQL keywords are deliberately absent.
var ReviewTools = map[string][]string{
	"a11y-review":  {"pa11y", "lighthouse", "axe", "vnu"},
	"error-review": {"errcheck", "staticcheck"},
	"lint-review": {"golangci-lint", "ruff", "eslint", "biome", "clang-tidy",
		"clang-format", "cpplint", "gofumpt", "shellcheck", "yamllint"},
	"mobile-review":  {"swiftlint", "ktlint", "detekt"},
	"privacy-review": {"semgrep"},
	"api-review":     {"spectral", "oasdiff", "buf"},
	"arch-review":    {"madge", "depcruise", "pydeps", "lint-imports"},
	"authz-review":   {"semgrep"},
	"build-review":   {"diffoscope", "shellcheck", "shfmt", "reuse"},
	"cli-review":     {"shellcheck", "shfmt"},
	"code-review": {"ruff", "mypy", "cargo-clippy", "eslint", "oxlint", "biome", "jscpd",
		"staticcheck", "gocritic", "cppcheck", "clang-tidy", "vulture", "knip", "ts-prune"},
	"compat-review":      {"shellcheck", "vermin", "cargo-msrv"},
	"concurrency-review": {"valgrind", "clang-tidy"},
	"config-review": {"check-jsonschema", "yamllint", "taplo", "dotenv-linter",
		"editorconfig-checker|ec", "shfmt"},
	"container-review": {"hadolint", "dockle", "kube-score", "kubesec", "kubeconform", "conftest", "trivy"},
	"db-review":        {"sqlfluff", "pg_format"},
	"deps-review": {"osv-scanner", "govulncheck", "pip-audit", "deptry", "cargo-audit",
		"cargo-udeps", "cargo-deny", "depcheck", "knip", "syft", "grype", "trivy", "cosign"},
	"doc-review":         {"vale", "markdownlint", "lychee", "codespell", "typos"},
	"fuzz-review":        {"cargo-fuzz", "afl-fuzz", "honggfuzz"},
	"gitops-review":      {"kustomize", "helm", "kubeconform", "kube-linter", "yq"},
	"helm-review":        {"helm", "kubeconform", "kube-score", "pluto", "yamllint"},
	"i18n-review":        {"xgettext", "msgfmt", "i18next-parser"},
	"idempotency-review": {"semgrep"},
	"infra-review": {"hadolint", "shellcheck", "actionlint", "tflint", "checkov",
		"conftest", "ansible-lint", "kubeconform"},
	"k8s-review": {"kubeconform", "kustomize", "kube-linter", "kube-score", "pluto",
		"conftest"},
	"llm-review": {"promptfoo", "garak"},
	"minimalism-review": {"vulture", "knip", "ts-prune", "cargo-udeps", "deadcode",
		"include-what-you-use", "tokei", "cloc"},
	"o11y-review":    {"promtool", "otel-cli"},
	"perf-review":    {"hyperfine", "perf", "heaptrack", "valgrind", "flamegraph", "bpftrace"},
	"webperf-review": {"lighthouse"},
	"pkg-review": {"lintian", "rpmlint", "namcap", "hadolint", "dive", "shellcheck",
		"desktop-file-validate", "appstream-util", "check-wheel-contents"},
	"release-review":  {"cargo-semver-checks", "api-extractor", "oasdiff", "git-cliff"},
	"resource-review": {"valgrind", "heaptrack", "bloaty"},
	"sdk-review":      {"api-extractor", "cargo-public-api", "stubtest"},
	"sec-review": {"semgrep", "gitleaks", "trufflehog", "bandit", "gosec", "shellcheck",
		"scan-build", "clang-tidy", "codeql"},
	"slop-review": {"jscpd"},
	"test-review": {"coverage", "cargo-llvm-cov", "cargo-tarpaulin", "c8", "nyc",
		"mutmut", "cargo-mutants", "stryker"},
	"time-review":    {"zdump"},
	"unicode-review": {"uconv"},
	"ux-review":      {"lighthouse", "vnu", "htmlhint", "stylelint"},
}

// ToolsFor lists the helper binaries one review can use: the core search
// tools every review is pointed at, then that review's own. The order is the
// catalog's, so the prompt names them the way doctor does. Git is left out: it
// is the runner's own tool, and a review is not allowed to run it.
//
// No entry is repeated within a list and none of the core names appears in
// ReviewTools, so the result is the concatenation rather than a merge of two
// sets. A duplicate here would print a tool twice in one prompt's "If
// available, use:" line, which reads as a broken catalog.
func ToolsFor(review string) []string {
	own := ReviewTools[review]
	out := make([]string, 0, len(CoreTools)+len(own))
	for _, c := range CoreTools {
		if c.Name == "git" {
			continue
		}
		out = append(out, c.Name)
	}
	return append(out, own...)
}

// ToolBins expands helper entries into the individual binaries they may need:
// an entry "ast-grep|sg" contributes both names, duplicates are dropped. This
// is the input ResolveMany wants, so one parallel pass can probe them all.
func ToolBins(entries []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		for alt := range strings.SplitSeq(e, "|") {
			if !seen[alt] {
				seen[alt] = true
				out = append(out, alt)
			}
		}
	}
	return out
}

// SplitTools sorts helper entries into the ones this machine has and the ones
// it lacks, given ResolveMany's result over ToolBins(entries). An entry with
// alternatives counts as present when any of its binaries resolved, and is
// named by its primary (the first alternative): that is what the rules call it.
func SplitTools(entries []string, found map[string]string) (have, missing []string) {
	for _, entry := range entries {
		present := false
		for alt := range strings.SplitSeq(entry, "|") {
			present = present || found[alt] != ""
		}
		primary, _, _ := strings.Cut(entry, "|")
		if present {
			have = append(have, primary)
		} else {
			missing = append(missing, primary)
		}
	}
	return have, missing
}

// AllProbeNames lists every binary doctor asks about, so they can be resolved
// in one parallel pass instead of one blocking lookup at a time.
func AllProbeNames() []string {
	names := make([]string, 0, len(Valid)+1+len(CoreTools))
	names = append(names, Valid...)
	names = append(names, "bunx")
	for _, c := range CoreTools {
		names = append(names, c.Name)
	}
	for _, tools := range ReviewTools {
		names = append(names, tools...)
	}
	out := ToolBins(names)
	fuzzy.Sort(out)
	return out
}
