// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maci0/gauntlet/internal/prompt"
)

// The suggester that is not an agent.
//
// `--suggest-agent gauntlet` answers the same question the triage step asks,
// from what is on disk: extensions, well-known filenames, and directory names.
// It costs milliseconds and no tokens, and it is honest about what it is: file
// signals, not judgment. An agent reads the code and can tell a toy HTTP
// handler from a payment path; this cannot. What it is good at is the obvious
// half, which is most of it: no Dockerfile, no container-review.

// FastSuggestAgent is the value --suggest-agent takes to use this instead of
// launching an agent.
const FastSuggestAgent = "gauntlet"

// scanLimits bound the walk. A repository with a million files is not worth a
// better answer than the first hundred thousand paths already give.
const (
	scanMaxFiles = 100_000
	scanMaxDepth = 12
)

// skipDirs are never walked: they hold other people's code or this tool's own
// scratch space, and neither says anything about the project under review.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".next": true, ".venv": true,
	"venv": true, ".gauntlet": true, ".crush": true, "__pycache__": true,
	".mypy_cache": true, ".pytest_cache": true, ".tox": true, ".idea": true,
}

// signals is what one walk of a tree found: the file extensions present, the
// base names present, and the path fragments present. Membership is all the
// rules below need.
type signals struct {
	ext   map[string]bool
	name  map[string]bool
	path  map[string]bool
	files int
	tests bool // a file named like a test, wherever it sits
}

func (s signals) anyExt(exts ...string) bool {
	for _, e := range exts {
		if s.ext[e] {
			return true
		}
	}
	return false
}

func (s signals) anyName(names ...string) bool {
	for _, n := range names {
		if s.name[n] {
			return true
		}
	}
	return false
}

func (s signals) anyPath(frags ...string) bool {
	for _, f := range frags {
		if s.path[f] {
			return true
		}
	}
	return false
}

// rule maps one observation about a tree to the reviews it justifies. The
// reason is what the run prints, so it names the evidence, never the verdict.
type rule struct {
	reason  string
	when    func(signals) bool
	reviews []string
}

var fastRules = []rule{
	// Every tree with code in it gets these: any code can be wrong, wasteful,
	// or padded, and any code can carry a vulnerability.
	{"source files to read", func(s signals) bool { return s.files > 0 },
		[]string{"code-review", "sec-review", "minimalism-review", "slop-review"}},
	{"a test suite", func(s signals) bool {
		return s.tests || s.anyPath("test", "tests", "spec", "__tests__") || s.anyName("conftest.py")
	}, []string{"test-review", "dst-review"}},
	{"documentation", func(s signals) bool { return s.anyExt(".md", ".rst", ".adoc") },
		[]string{"doc-review"}},
	{"a Dockerfile or compose file", func(s signals) bool {
		return s.anyName("dockerfile", "containerfile", "docker-compose.yml", "compose.yaml", "compose.yml")
	}, []string{"container-review"}},
	{"CI workflows", func(s signals) bool { return s.anyPath(".github/workflows", ".gitlab-ci.yml") },
		[]string{"infra-review", "build-review"}},
	{"infrastructure as code", func(s signals) bool {
		return s.anyExt(".tf", ".tfvars") || s.anyName("ansible.cfg", "playbook.yml") ||
			s.anyPath("charts", "manifests", "kustomization.yaml")
	}, []string{"infra-review", "container-review"}},
	{"a build system", func(s signals) bool {
		return s.anyName("makefile", "justfile", "cmakelists.txt", "meson.build", "build.gradle", "pom.xml")
	}, []string{"build-review"}},
	{"shell scripts", func(s signals) bool { return s.anyExt(".sh", ".bash", ".zsh") },
		[]string{"cli-review", "compat-review"}},
	{"a Go module", func(s signals) bool { return s.anyName("go.mod") },
		[]string{"error-review", "concurrency-review", "deps-review"}},
	{"a Rust crate", func(s signals) bool { return s.anyName("cargo.toml") },
		[]string{"concurrency-review", "resource-review", "deps-review"}},
	{"C or C++ sources", func(s signals) bool {
		return s.anyExt(".c", ".cc", ".cpp", ".cxx", ".h", ".hpp")
	}, []string{"resource-review", "concurrency-review", "numerics-review", "fuzz-review"}},
	{"a Python package", func(s signals) bool {
		return s.anyName("pyproject.toml", "requirements.txt", "setup.py")
	}, []string{"deps-review", "error-review"}},
	{"a JavaScript or TypeScript package", func(s signals) bool { return s.anyName("package.json") },
		[]string{"deps-review", "dx-review"}},
	{"a web frontend", func(s signals) bool {
		return s.anyExt(".html", ".css", ".scss", ".jsx", ".tsx", ".vue", ".svelte")
	}, []string{"ux-review", "a11y-review", "uislop-review", "webperf-review", "design-review"}},
	{"mobile sources", func(s signals) bool { return s.anyExt(".swift", ".kt", ".dart") },
		[]string{"mobile-review"}},
	{"SQL or migrations", func(s signals) bool {
		return s.anyExt(".sql") || s.anyPath("migrations", "migrate")
	}, []string{"db-review"}},
	{"an API description", func(s signals) bool {
		return s.anyName("openapi.yaml", "openapi.json", "swagger.yaml", "schema.graphql") ||
			s.anyExt(".proto")
	}, []string{"api-review", "compat-review", "sdk-review"}},
	{"configuration files", func(s signals) bool {
		return s.anyExt(".yaml", ".yml", ".toml", ".ini") || s.anyName(".env.example")
	}, []string{"config-review"}},
	{"translation files", func(s signals) bool {
		return s.anyExt(".po", ".pot") || s.anyPath("locales", "i18n", "translations")
	}, []string{"i18n-review"}},
	{"agent instructions or prompts", func(s signals) bool {
		return s.anyName("claude.md", "agents.md", "cursor.md", "copilot-instructions.md") ||
			s.anyPath("prompts", ".claude", ".cursor")
	}, []string{"agentrules-review", "prompt-review", "skills-review"}},
	{"model or LLM code", func(s signals) bool {
		return s.anyPath("prompts", "llm", "inference") || s.anyName("modelfile")
	}, []string{"llm-review"}},
	{"authentication or authorization code", func(s signals) bool {
		return s.anyPath("auth", "authz", "identity", "session", "login")
	}, []string{"authz-review", "sec-review", "privacy-review", "threat-review"}},
	{"caching or storage code", func(s signals) bool {
		return s.anyPath("cache", "redis", "memcached")
	}, []string{"cache-review"}},
	{"telemetry or metrics code", func(s signals) bool {
		return s.anyPath("metrics", "telemetry", "otel", "prometheus")
	}, []string{"o11y-review"}},
	{"a packaged or released artifact", func(s signals) bool {
		return s.anyName("changelog.md", "pkgbuild", "debian", "*.spec") ||
			s.anyPath(".github/workflows")
	}, []string{"release-review", "pkg-review"}},
	{"a public API surface", func(s signals) bool {
		return s.anyPath("pkg", "lib", "api", "include", "sdk")
	}, []string{"sdk-review", "specs-review"}},
	{"performance-sensitive code paths", func(s signals) bool {
		return s.anyPath("bench", "benchmarks") || s.anyExt(".c", ".cc", ".cpp", ".rs", ".go")
	}, []string{"perf-review"}},
	{"secrets or credentials handling", func(s signals) bool {
		return s.anyName(".env", ".env.example", "secrets.yaml") || s.anyPath("secrets", "credentials")
	}, []string{"sec-review", "privacy-review"}},
	{"time or date handling", func(s signals) bool {
		return s.anyPath("scheduler", "cron", "calendar")
	}, []string{"time-review"}},
	{"user-facing text", func(s signals) bool { return s.anyExt(".md", ".txt", ".html") },
		[]string{"unicode-review"}},
}

// isTestFile recognizes the naming conventions test files follow, since a
// project is as likely to keep them beside the code as in a tests directory.
func isTestFile(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return strings.HasSuffix(base, "_test") || strings.HasSuffix(base, "_spec") ||
		strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") ||
		strings.HasPrefix(base, "test_")
}

// fastSuggest reads the tree and returns the reviews its files justify,
// restricted to the pool and in pool order. Reviews with no signal behind them
// are left out: proposing everything would be the same as proposing nothing.
func fastSuggest(dir string, pool []string) []prompt.Suggestion {
	s := scan(dir)
	inPool := make(map[string]bool, len(pool))
	for _, name := range pool {
		inPool[name] = true
	}
	reasons := map[string][]string{}
	for _, r := range fastRules {
		if !r.when(s) {
			continue
		}
		for _, review := range r.reviews {
			if inPool[review] && !slices.Contains(reasons[review], r.reason) {
				reasons[review] = append(reasons[review], r.reason)
			}
		}
	}
	out := make([]prompt.Suggestion, 0, len(reasons))
	for _, name := range pool {
		if rs := reasons[name]; len(rs) > 0 {
			out = append(out, prompt.Suggestion{Name: name, Reason: strings.Join(rs, ", ")})
		}
	}
	return out
}

// scan walks the tree once, collecting the three kinds of membership the rules
// ask about. Names and fragments are lowercased: a repository is not required
// to agree with this file about capitalization.
func scan(dir string) signals {
	s := signals{ext: map[string]bool{}, name: map[string]bool{}, path: map[string]bool{}}
	root := filepath.Clean(dir)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner says nothing; keep walking
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(rel), "/"))
		if d.IsDir() {
			switch {
			case path == root:
				return nil
			case skipDirs[strings.ToLower(d.Name())], depth > scanMaxDepth:
				return fs.SkipDir
			}
			s.path[strings.ToLower(d.Name())] = true
			s.path[strings.ToLower(filepath.ToSlash(rel))] = true
			return nil
		}
		if s.files >= scanMaxFiles {
			return fs.SkipAll
		}
		s.files++
		lower := strings.ToLower(d.Name())
		if isTestFile(lower) {
			s.tests = true
		}
		s.name[lower] = true
		s.path[strings.ToLower(filepath.ToSlash(rel))] = true
		if ext := strings.ToLower(filepath.Ext(lower)); ext != "" {
			s.ext[ext] = true
		}
		return nil
	})
	return s
}
