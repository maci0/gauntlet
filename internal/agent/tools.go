// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import "sort"

// CoreTools are the search and rewrite binaries the injected rules point every
// review at. "a|b" means either binary satisfies the check.
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
	"agentrules-review", "cache-review", "design-review", "dr-review",
	"dst-review", "dx-review", "error-review", "functionality-review",
	"lint-review", "mobile-review", "numerics-review", "privacy-review",
	"prompt-review", "skills-review", "specs-review", "threat-review",
	"time-review", "uislop-review", "unicode-review",
}

// RecommendedTools are worth installing on any machine: language-agnostic and
// useful in most repos. Everything else in ReviewTools is ecosystem-specific.
var RecommendedTools = map[string]bool{
	"actionlint": true, "diffoscope": true, "gitleaks": true, "hadolint": true,
	"hyperfine": true, "jscpd": true, "lychee": true, "markdownlint": true,
	"osv-scanner": true, "semgrep": true, "shellcheck": true, "tokei": true,
	"yamllint": true,
}

// ReviewTools are optional per-review helpers, mirroring the "If available,
// use:" lines in the prompts. Entries are binaries, so package-only names
// (Atheris, Jazzer, eslint plugins) and SQL keywords are deliberately absent.
var ReviewTools = map[string][]string{
	"a11y-review":        {"pa11y", "lighthouse", "axe", "vnu"},
	"api-review":         {"spectral", "oasdiff", "buf"},
	"arch-review":        {"madge", "pydeps", "lint-imports"},
	"authz-review":       {"semgrep"},
	"build-review":       {"diffoscope", "shellcheck"},
	"cli-review":         {"shellcheck"},
	"code-review":        {"ruff", "cargo-clippy", "eslint", "oxlint", "jscpd", "vulture", "knip", "ts-prune"},
	"compat-review":      {"shellcheck"},
	"concurrency-review": {"valgrind"},
	"config-review":      {"check-jsonschema", "yamllint", "taplo", "dotenv-linter"},
	"container-review":   {"hadolint", "kube-score", "kubesec", "trivy"},
	"db-review":          {"sqlfluff"},
	"deps-review": {"osv-scanner", "pip-audit", "deptry", "cargo-audit", "cargo-udeps",
		"cargo-deny", "depcheck", "knip", "syft", "grype", "cosign"},
	"doc-review":         {"vale", "markdownlint", "lychee"},
	"fuzz-review":        {"cargo-fuzz", "afl-fuzz"},
	"i18n-review":        {"xgettext", "msgfmt", "i18next-parser"},
	"idempotency-review": {"semgrep"},
	"infra-review":       {"hadolint", "shellcheck", "actionlint", "tflint"},
	"llm-review":         {"promptfoo", "garak"},
	"minimalism-review":  {"vulture", "knip", "ts-prune", "cargo-udeps", "tokei", "cloc"},
	"o11y-review":        {"promtool", "otel-cli"},
	"perf-review":        {"hyperfine", "perf", "heaptrack", "valgrind"},
	"webperf-review":     {"lighthouse"},
	"pkg-review": {"lintian", "rpmlint", "namcap", "hadolint", "dive", "shellcheck",
		"desktop-file-validate", "appstream-util", "check-wheel-contents"},
	"release-review":  {"cargo-semver-checks", "api-extractor", "oasdiff", "git-cliff"},
	"resource-review": {"valgrind", "heaptrack"},
	"sdk-review":      {"api-extractor", "cargo-public-api", "stubtest"},
	"sec-review":      {"semgrep", "gitleaks", "trufflehog", "bandit", "gosec", "shellcheck"},
	"slop-review":     {"jscpd"},
	"test-review":     {"coverage", "cargo-llvm-cov", "c8", "nyc", "mutmut", "cargo-mutants", "stryker"},
	"ux-review":       {"lighthouse", "vnu", "htmlhint", "stylelint"},
}

// AllProbeNames lists every binary doctor asks about, so they can be resolved
// in one parallel pass instead of one blocking lookup at a time.
func AllProbeNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, t := range Valid {
		add(t)
	}
	add("bunx")
	for _, c := range CoreTools {
		for _, alt := range splitAlts(c.Name) {
			add(alt)
		}
	}
	for _, tools := range ReviewTools {
		for _, t := range tools {
			add(t)
		}
	}
	sort.Strings(out)
	return out
}

func splitAlts(name string) []string {
	var out []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '|' {
			out = append(out, name[start:i])
			start = i + 1
		}
	}
	return append(out, name[start:])
}
