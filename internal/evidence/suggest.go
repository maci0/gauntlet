// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package evidence is the suggester that is not an agent.
//
// `--suggest-agent gauntlet` answers the same question the triage step asks,
// from what is on disk: which files exist, how many of them, what they say
// inside, what has changed lately, and how past runs on this directory went.
// It costs milliseconds and no tokens, and it is honest about what it is:
// evidence, not judgment. An agent reads the code and can tell a toy HTTP
// handler from a payment path; this cannot.
//
// Every observation is scored rather than merely present: one stray .css file
// in a Go repository is not a frontend, and a directory nobody has touched in
// a quarter is not where the next review should look.
package evidence

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/fuzzy"
	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/prompt"
)

// AgentName is the value --suggest-agent takes to use this instead of
// launching an agent.
const AgentName = "gauntlet"

// Scan limits. A repository with a million files is not worth a better answer
// than the first hundred thousand paths already give, and the content peek
// reads heads, not files: what a source file imports is in its first lines.
const (
	scanMaxFiles = 100_000
	scanMaxDepth = 12
	peekMaxFiles = 2_000
	peekBytes    = 4 << 10
)

// churnWindow is how far back a commit still counts as evidence that a part
// of the tree is alive. A duration rather than git's "90 days ago" so the
// cutoff is an instant on the injected clock: a relative phrase is resolved by
// git against its own wall clock, which would make the suggestion a function
// of the date the run happened on rather than of the tree and the seed.
const churnWindow = 90 * 24 * time.Hour

// Evidence weights. A rule's weight says how much its observation is worth;
// reviews below minScore are not proposed at all, which is what keeps a single
// matching file from dragging in a whole family of reviews.
const (
	weightWeak   = 0.5
	weightNormal = 1.0
	weightStrong = 2.0
	minScore     = 0.5
)

// A language is present when it has real presence, not one file: three files,
// or a twentieth of the tree. Below that its reviews would be noise.
const (
	langMinFiles = 3
	langMinShare = 0.05
)

// How much recent churn moves a language's weight. A live area is worth more
// attention than a dormant one, and a dormant one is usually worth none: it
// drops most single-file leftovers below minScore on its own.
const (
	churnBonus     = 1.25
	churnDormant   = 0.6
	historyBoost   = 1.3
	historyPenalty = 0.5
	// historyMinRuns is how many finished runs it takes before "this review
	// never changes anything here" is a fact rather than a coincidence.
	historyMinRuns = 3
	// historyGoodRate is the share of past runs that must have landed changes
	// for a review to count as productive in this directory.
	historyGoodRate = 0.5
)

// reasonsShown bounds the evidence printed beside a suggestion: the strongest
// few say why, a full list says nothing.
const reasonsShown = 3

// skipDirs are excluded from both git listings and walks: dependency, build,
// and scratch files do not describe the application's capabilities. Every
// lookup folds case, so these exclusions also hold on case-sensitive volumes.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".next": true, ".venv": true,
	"venv": true, ".gauntlet": true, ".crush": true, "__pycache__": true,
	".mypy_cache": true, ".pytest_cache": true, ".tox": true, ".idea": true,
	".deps": true, ".scratch": true, "third-party": true, "third_party": true,
	"thirdparty": true, "toolchain": true, "toolchains": true,
}

// skipDir applies equally to git listings and walks. Versioned MSVC installs
// contain the compiler's headers and examples, not the application's source.
func skipDir(name string) bool {
	name = strings.ToLower(name)
	if skipDirs[name] {
		return true
	}
	version, msvc := strings.CutPrefix(name, "msvc")
	return msvc && version != "" && strings.Trim(version, "0123456789") == ""
}

// sourceExts identify code. Their heads and selected package/build manifests
// are read; other files contribute only their names and extensions.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".rs": true, ".c": true, ".cc": true, ".cpp": true,
	".cxx": true, ".h": true, ".hpp": true, ".java": true, ".kt": true, ".swift": true,
	".rb": true, ".php": true, ".cs": true, ".zig": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".mjs": true, ".vue": true, ".svelte": true,
	".sh": true, ".bash": true, ".sql": true, ".lua": true, ".dart": true, ".ex": true,
	".ino": true, ".scad": true, ".asm": true, ".s": true, ".c3": true,
	".ps1": true, ".bat": true, ".cmd": true, ".m": true, ".mm": true,
	".scala": true, ".pl": true, ".exs": true, ".jl": true, ".r": true,
	".zsh": true,
}

var manifestNames = map[string]bool{
	"package.json": true, "pyproject.toml": true, "cargo.toml": true,
	"plugin.json": true, "cmakelists.txt": true, "setup.cfg": true,
	"dockerfile": true, "containerfile": true,
}

// markEntry is a capability marker. Built-ins bound identifiers; a review's
// own `mark:` signal searches literally in a separate mark: namespace.
type markEntry struct {
	text, says  string
	left, right bool // built-in identifier boundaries; declared marks stay literal
	// needle is text as bytes, set for ASCII marks so peek can search file
	// heads without allocating a copy per file per mark. Nil means the
	// haystack has to be folded for a non-ASCII needle.
	needle []byte
}

// mark is one built-in boundary-aware search. ASCII needles are stored as bytes
// once so peek does not allocate a copy per file per mark.
func mark(text, says string) markEntry {
	e := markEntry{text: text, says: says,
		left: identifierByte(text[0]), right: identifierByte(text[len(text)-1]) && !strings.HasSuffix(text, "_")}
	if fuzzy.IsASCII(text) {
		e.needle = []byte(text)
	}
	return e
}

// prefixMark recognizes API families such as CreateWindowA and psycopg2.
func prefixMark(text, says string) markEntry {
	e := mark(text, says)
	e.right = false
	return e
}

// marks maps a substring found in source to what it says about the code. Names
// on the left are lowercased before the search, so a match is case-insensitive
// without a regex engine or a second pass over the text.
var marks = []markEntry{
	prefixMark("http.listenandserve", "http"), prefixMark("http.serve", "http"), prefixMark("http.handle", "http"),
	mark("express(", "http"), mark("axum::", "http"), mark("gin.", "http"),
	mark("new hono(", "http"), mark("grpc.newserver", "http"), mark("http.server", "http"),
	mark("database/sql", "sql"), prefixMark("psycopg", "sql"), mark("sqlalchemy", "sql"),
	prefixMark("sqlite3", "sql"), mark("pg.pool", "sql"), mark("gorm.", "sql"),
	mark("time.now", "clock"), mark("datetime.now", "clock"), mark("time.sleep", "clock"),
	mark("utcnow", "clock"), mark("cron", "clock"),
	mark("go func", "concurrent"), mark("asyncio", "concurrent"), mark("threading.", "concurrent"),
	mark("sync.mutex", "concurrent"), mark("std::thread", "concurrent"), mark("tokio::", "concurrent"),
	mark("multiprocessing", "concurrent"), mark("pthread_", "concurrent"),
	mark("createthread(", "concurrent"), mark("thread::spawn", "concurrent"), mark("promise.all", "concurrent"), mark("promise.allsettled", "concurrent"),
	prefixMark("prometheus", "telemetry"), prefixMark("opentelemetry", "telemetry"), mark("otel", "telemetry"),
	mark("logrus", "telemetry"), mark("structlog", "telemetry"),
	mark("logger.", "logging"), mark("logging.", "logging"), mark("log.", "logging"),
	mark("redis.", "cache"), mark("memcache.", "cache"), mark("lru_cache", "cache"),
	mark("functools.cache", "cache"), mark("cached_property", "cache"), mark("cache.get", "cache"),
	mark("jwt", "auth"), mark("oauth", "auth"), mark("bcrypt", "auth"), mark("argon2", "auth"),
	mark("session[", "auth"), mark("set-cookie", "auth"),
	mark("unsafe.pointer", "unsafe"), mark("ctypes", "unsafe"), mark("eval(", "unsafe"),
	mark("pickle.loads", "unsafe"), mark("innerhtml", "unsafe"),
	mark("subprocess", "exec"), mark("exec.command", "exec"), mark("os/exec", "exec"),
	mark("anthropic.", "model"), mark("openai.", "model"), mark("completions.create", "model"),
	mark("chat/completions", "model"), mark("anthropic-version", "model"),
	mark("ollama.", "model"), mark("system_prompt", "model"),
	mark("dsh-llm", "model"), mark("llama.cpp", "model"), mark("vllm", "model"),
	mark("google.genai", "model"), mark("google_genai", "model"), mark("bedrock", "model"),
	mark("boto3", "cloud"), mark("kubernetes", "cloud"), mark("terraform", "cloud"),
	prefixMark("idempotenc", "retry"), mark("retry(", "retry"), mark("backoff", "retry"),
	mark("celery", "retry"), mark("sqs", "retry"),
	mark("argparse", "cli"), mark("click.command", "cli"), mark("cobra.command", "cli"),
	mark("flag.parse", "cli"), mark("clap::", "cli"), mark("commander", "cli"), mark("yargs", "cli"),
	mark("bubbletea", "tui"), mark("ratatui", "tui"), mark("curses", "tui"), mark("ncurses", "tui"),
	mark("rich.live", "tui"), mark("rich.layout", "tui"), mark("blessed", "tui"),
	mark("prompt_toolkit", "tui"), mark("readline/readline.h", "tui"), mark("readline.createinterface", "tui"), mark("linenoise", "tui"),
	mark("document.queryselector", "dom"), mark("document.createelement", "dom"),
	mark("getelementbyid(", "dom"), mark("react-dom", "dom"),
	prefixMark("createwindow", "gui"), prefixMark("dialogbox", "gui"), mark("wm_paint", "gui"),
	mark("qapplication", "gui"), mark("gtk_", "gui"), mark("imgui", "gui"),
	mark("system.windows.forms", "gui"), mark("swiftui", "gui"),
	mark("sys.argv", "cli"), mark("os.args", "cli"), mark("getopt", "cli"), mark("getopt_", "cli"),
	mark("process.argv", "cli"),
	mark("date.now(", "clock"), mark("new date(", "clock"), mark("settimeout(", "clock"),
	mark("setinterval(", "clock"), prefixMark("gettickcount", "clock"), mark("std::chrono", "clock"),
	mark("runtime.goos", "portable"), mark("path/filepath", "portable"),
	mark("os.path", "portable"), mark("pathlib", "portable"),
	mark("#ifdef _win32", "portable"), mark("__linux__", "portable"),
	mark("find_package(", "dependency"), mark("fetchcontent_", "dependency"),
	mark("apt-get ", "dependency"), mark("apk add ", "dependency"), mark("pip install ", "dependency"),
	mark("json.loads", "parse"), mark("json.decode", "parse"), mark("json.unmarshal", "parse"),
	mark("encoding/json", "parse"), mark("serde_json", "parse"), mark("struct.unpack", "parse"),
	mark("json.parse", "parse"), mark("fread(", "parse"), mark("sscanf(", "parse"),
	mark("fgets(", "parse"), mark("getline(", "parse"),
	mark("os.write", "write"), mark("writefile", "write"), mark("write_file", "write"),
	mark("fwrite(", "write"), mark("write_text(", "write"), mark("write_bytes(", "write"),
	mark("fetch(", "httpclient"), mark("requests.", "httpclient"), mark("http.client", "httpclient"),
	mark("http.get", "httpclient"), mark("http.post", "httpclient"), mark("curl", "httpclient"),
	mark("os.getenv", "config"), mark("os.environ", "config"), mark("process.env", "config"),
	mark("getenv(", "config"), mark("env::var", "config"), mark("viper.", "config"),
	mark("export const config", "config"),
	mark("malloc(", "resource"), mark("calloc(", "resource"), mark("fopen(", "resource"),
	prefixMark("createfile", "resource"), mark("globalalloc", "resource"),
	mark("oauth", "personal"), mark("user.email", "personal"), mark("user_email", "personal"),
	mark("analytics", "personal"), mark("telemetry.track", "personal"),
	mark("gettext", "translate"), mark("i18n", "translate"), mark("usetranslation", "translate"),
	mark("backup", "recovery"), mark("restore(", "recovery"), mark("failover", "recovery"),
	mark("numpy", "numeric"), mark("math.", "numeric"), mark("sqrt(", "numeric"),
	mark("floor(", "numeric"), mark("ceil(", "numeric"), mark("sin(", "numeric"), mark("cos(", "numeric"),
	mark("decimal(", "numeric"), mark("round(", "numeric"),
}

// signals is what one pass over a tree found. Counts, not booleans: how much
// of a thing there is decides whether its reviews are worth proposing.
type signals struct {
	files  int
	source int
	ext    map[string]int
	name   map[string]bool
	path   map[string]bool
	mark   map[string]int
	hot    map[string]int // extension to files changed inside the churn window
	churn  bool           // the repository reported recent commits
	tests  bool
}

func (s signals) count(exts ...string) int {
	n := 0
	for _, e := range exts {
		n += s.ext[e]
	}
	return n
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

func (s signals) anyMark(keys ...string) bool {
	for _, k := range keys {
		if s.mark[k] > 0 {
			return true
		}
	}
	return false
}

// liveness scales an observation by whether those files are still being
// edited. Without commit history every area is equally plausible, so the
// scaling is skipped rather than guessed.
func (s signals) liveness(exts ...string) float64 {
	if !s.churn {
		return 1
	}
	for _, e := range exts {
		if s.hot[e] > 0 {
			return churnBonus
		}
	}
	return churnDormant
}

// rule maps one observation about a tree to the reviews it justifies. The
// reason is what the run prints, so it names the evidence, never the verdict.
type rule struct {
	reason  string
	weight  float64
	when    func(signals) float64 // 0 when the rule does not apply
	reviews []string
}

// present turns a yes-or-no observation into a rule condition.
func present(fn func(signals) bool) func(signals) float64 {
	return func(s signals) float64 {
		if fn(s) {
			return 1
		}
		return 0
	}
}

// absent fires when an observation is missing from a tree that has code in it.
// Only reviews which can act on that gap use it: documentation can be added,
// but a test-quality review needs existing tests, not their absence.
func absent(fn func(signals) bool) func(signals) float64 {
	return func(s signals) float64 {
		if s.source > 0 && !fn(s) {
			return 1
		}
		return 0
	}
}

// lang fires when a language has real presence in the tree, scaled by whether
// its files are still being edited.
func lang(exts ...string) func(signals) float64 {
	return func(s signals) float64 {
		n := s.count(exts...)
		if n == 0 {
			return 0
		}
		share := float64(n) / float64(max(s.files, 1))
		if n < langMinFiles && share < langMinShare {
			return 0
		}
		return s.liveness(exts...)
	}
}

// The predicates shared by a presence rule and its absence counterpart, so the
// two can never drift apart.
var (
	hasTests = func(s signals) bool {
		return s.tests || s.anyPath("test", "tests", "spec", "__tests__") || s.anyName("conftest.py")
	}
	hasDocs = func(s signals) bool {
		return s.ext[".md"]+s.ext[".rst"]+s.ext[".adoc"] > 0
	}
	hasCI = func(s signals) bool {
		return s.anyPath(".github/workflows", ".gitlab-ci.yml") ||
			s.anyName(".gitlab-ci.yml", ".drone.yml", "jenkinsfile")
	}
	hasLinter = func(s signals) bool {
		return s.anyName(".eslintrc", ".eslintrc.json", "eslint.config.js", ".oxlintrc.json",
			".golangci.yml", ".golangci.yaml", "ruff.toml", ".ruff.toml", "clippy.toml",
			".clang-tidy", ".clang-format", ".editorconfig", "setup.cfg", ".flake8")
	}
)

// hasWeb is a language rule, not a predicate: a tree with no frontend in it is
// not evidence for anything, so it has no absence counterpart to share.
var hasWeb = lang(".html", ".css", ".scss", ".jsx", ".tsx", ".vue", ".svelte")

var fastRules = []rule{
	// Every tree with code in it gets these: any code can be wrong, wasteful,
	// padded, sloppily typed, or carrying a vulnerability.
	{"source files to read", weightNormal, present(func(s signals) bool { return s.source > 0 }),
		[]string{"code-review", "sec-review", "minimalism-review", "slop-review",
			"lint-review", "perfectionism-review", "error-review", "perf-review"}},
	{"a documented or tested program", weightNormal, present(func(s signals) bool {
		return s.source > 0 && (hasDocs(s) || hasTests(s) || s.anyMark("cli"))
	}), []string{"functionality-review"}},
	{"a test suite", weightNormal, present(hasTests), []string{"test-review"}},
	{"no tests in the source tree", weightStrong, present(func(s signals) bool {
		return s.source > 0 && !hasTests(s)
	}), []string{"test-review"}},
	{"documentation", weightNormal, present(hasDocs), []string{"doc-review"}},
	{"prose to read", weightNormal, present(hasDocs), []string{"slop-review"}},
	{"no documentation in the tree", weightStrong, absent(hasDocs),
		[]string{"doc-review"}},
	{"CI workflows", weightNormal, present(hasCI), []string{"infra-review", "build-review"}},
	{"a linter configuration", weightNormal, present(hasLinter), []string{"lint-review"}},
	{"a Dockerfile or compose file", weightStrong, present(func(s signals) bool {
		return s.anyName("dockerfile", "containerfile", "docker-compose.yml", "compose.yaml", "compose.yml")
	}), []string{"infra-review", "pkg-review", "threat-review", "build-review"}},
	{"infrastructure as code", weightStrong, present(func(s signals) bool {
		return s.count(".tf", ".tfvars") > 0 || s.anyName("ansible.cfg", "playbook.yml") ||
			s.anyPath("charts", "manifests", "kustomization.yaml")
	}), []string{"infra-review"}},
	{"Kubernetes manifests or kustomize files", weightStrong, present(func(s signals) bool {
		return s.anyName("kustomization.yaml", "kustomization.yml") ||
			s.anyPath("manifests", "overlays")
	}), []string{"k8s-review", "container-review"}},
	{"a GitOps delivery layer", weightStrong, present(func(s signals) bool {
		return s.anyPath("flux-system", "argocd") ||
			s.anyName("gotk-components.yaml", "gotk-sync.yaml", "application.yaml", "applicationset.yaml", "helmrelease.yaml")
	}), []string{"gitops-review", "k8s-review", "dr-review"}},
	{"a Helm chart", weightStrong, present(func(s signals) bool {
		return s.anyName("chart.yaml") || s.anyPath("charts")
	}), []string{"helm-review", "container-review"}},
	{"a build system", weightNormal, present(func(s signals) bool {
		return s.anyName("makefile", "justfile", "cmakelists.txt", "meson.build", "build.gradle", "build.gradle.kts", "pom.xml", "build.zig", "go.mod", "cargo.toml") || s.count(".csproj", ".vcxproj", ".sln") > 0
	}), []string{"build-review"}},
	{"shell scripts", weightNormal, lang(".sh", ".bash", ".zsh"),
		[]string{"compat-review", "idempotency-review"}},
	{"a Go module", weightNormal, present(func(s signals) bool { return s.anyName("go.mod") }),
		[]string{"error-review", "deps-review"}},
	{"a Rust crate", weightNormal, present(func(s signals) bool { return s.anyName("cargo.toml") }),
		[]string{"resource-review", "deps-review"}},
	{"C or C++ sources", weightNormal, lang(".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"),
		[]string{"build-review"}},
	{"portability-sensitive source", weightStrong, present(func(s signals) bool { return s.anyMark("portable") }),
		[]string{"compat-review"}},
	{"a Python package", weightNormal, present(func(s signals) bool {
		return s.anyName("pyproject.toml", "requirements.txt", "setup.py")
	}), []string{"deps-review", "error-review"}},
	{"a JavaScript or TypeScript package", weightNormal,
		present(func(s signals) bool { return s.anyName("package.json") }),
		[]string{"deps-review", "dx-review", "build-review"}},
	{"dependency manifests", weightNormal, present(func(s signals) bool {
		return s.anyName("build.zig.zon", "vcpkg.json", "conanfile.py", "conanfile.txt", "composer.json", "gemfile", "pom.xml", "build.gradle", "build.gradle.kts")
	}), []string{"deps-review"}},
	{"a web frontend", weightNormal, hasWeb,
		[]string{"ux-review", "a11y-review", "uislop-review", "webperf-review", "design-review"}},
	{"mobile sources", weightNormal, lang(".swift", ".kt", ".dart"), []string{"mobile-review"}},
	{"SQL or migrations", weightNormal, present(func(s signals) bool {
		return s.count(".sql") > 0 || s.anyPath("migrations", "migrate")
	}), []string{"db-review", "dr-review"}},
	{"an API description", weightStrong, present(func(s signals) bool {
		return s.anyName("openapi.yaml", "openapi.json", "swagger.yaml", "schema.graphql") ||
			s.count(".proto") > 0
	}), []string{"api-review", "compat-review"}},
	{"configuration files", weightWeak, present(func(s signals) bool {
		for name := range s.name {
			if strings.HasSuffix(name, ".cfg") && name != "setup.cfg" {
				return true
			}
		}
		return s.count(".ini", ".conf") > 0 || s.anyName(".env.example", "config.yaml", "config.yml", "config.toml") || s.anyMark("config")
	}), []string{"config-review"}},
	{"translation files", weightStrong, present(func(s signals) bool {
		return s.count(".po", ".pot") > 0 || s.anyPath("locale", "locales", "i18n", "translations")
	}), []string{"i18n-review"}},
	{"agent instruction files", weightStrong, present(func(s signals) bool {
		return s.anyName("claude.md", "agents.md", ".cursorrules", ".windsurfrules", "copilot-instructions.md")
	}), []string{"agentrules-review"}},
	{"review prompts or prompt templates", weightStrong, present(func(s signals) bool {
		for name := range s.name {
			if strings.HasSuffix(name, "-review.md") {
				return true
			}
		}
		return s.anyPath("prompts")
	}), []string{"prompt-review"}},
	{"skill definitions", weightStrong, present(func(s signals) bool {
		return s.anyName("skill.md") || s.anyPath(".claude/skills", ".agents/skills", ".claude/commands")
	}), []string{"skills-review"}},
	{"a packaged or released artifact", weightNormal, present(func(s signals) bool {
		return s.anyName("changelog.md", "pkgbuild", "debian") || s.count(".spec") > 0 || s.anyMark("release")
	}), []string{"release-review"}},
	{"packaging definitions", weightStrong, present(func(s signals) bool {
		return s.anyName("pkgbuild", "setup.py", "manifest.in", "control", "flatpak.json", "snapcraft.yaml") || s.count(".spec") > 0 || s.anyPath("debian") || s.anyMark("package")
	}), []string{"pkg-review"}},
	{"a public API surface", weightWeak, present(func(s signals) bool {
		return s.anyMark("library") || s.anyPath("pkg", "lib", "api", "include", "sdk") && s.anyName("lib.rs", "setup.py", "index.d.ts", "py.typed")
	}), []string{"sdk-review"}},
	{"decision or requirement documents", weightStrong, present(func(s signals) bool {
		if s.anyPath("docs/adr", "docs/decisions", "docs/rfcs", "docs/specs") {
			return true
		}
		for name := range s.name {
			if strings.HasPrefix(name, "adr-") || strings.HasPrefix(name, "rfc-") || strings.HasSuffix(name, "-adr.md") || strings.HasSuffix(name, ".prd.md") || name == "requirements.md" {
				return true
			}
		}
		return false
	}), []string{"specs-review"}},
	{"compiled code where speed is visible", weightNormal, lang(".c", ".cc", ".cpp", ".rs", ".go", ".zig"),
		[]string{"perf-review"}},
	{"benchmarks", weightStrong, present(func(s signals) bool { return s.anyPath("bench", "benchmarks") }),
		[]string{"perf-review"}},
	{"secrets or credentials handling", weightStrong, present(func(s signals) bool {
		return s.anyName(".env", "secrets.yaml") || s.anyPath("secrets", "credentials")
	}), []string{"sec-review", "config-review"}},
	{"a multi-file source tree", weightNormal, present(func(s signals) bool {
		return s.source >= 2
	}), []string{"arch-review", "design-review"}},

	// What the files say inside. Directory names are a guess about a codebase;
	// what it imports is a fact about it.
	{"HTTP handlers or server framework imports", weightStrong, present(func(s signals) bool { return s.anyMark("http") }),
		[]string{"api-review", "threat-review", "perf-review", "fuzz-review", "resource-review"}},
	{"database access in the source", weightStrong, present(func(s signals) bool { return s.anyMark("sql") }),
		[]string{"db-review", "sec-review", "dr-review"}},
	{"clock and calendar handling", weightStrong, present(func(s signals) bool { return s.anyMark("clock") }),
		[]string{"time-review"}},
	{"threads, goroutines, or async code", weightStrong,
		present(func(s signals) bool { return s.anyMark("concurrent") }),
		[]string{"concurrency-review", "resource-review"}},
	{"service logs, metrics or tracing", weightStrong, present(func(s signals) bool {
		return s.anyMark("http", "concurrent", "gui") && s.anyMark("telemetry", "logging")
	}),
		[]string{"o11y-review"}},
	{"a cache client", weightStrong, present(func(s signals) bool { return s.anyMark("cache") }),
		[]string{"cache-review", "perf-review"}},
	{"authentication code", weightStrong, present(func(s signals) bool { return s.anyMark("auth") }),
		[]string{"authz-review", "sec-review", "threat-review"}},
	{"unsafe or dynamic evaluation", weightStrong, present(func(s signals) bool { return s.anyMark("unsafe") }),
		[]string{"sec-review"}},
	{"subprocess execution", weightStrong, present(func(s signals) bool { return s.anyMark("exec") }),
		[]string{"sec-review", "compat-review"}},
	{"model SDK imports or calls", weightStrong, present(func(s signals) bool { return s.anyMark("model") }),
		[]string{"llm-review"}},
	{"cloud or cluster APIs", weightStrong, present(func(s signals) bool { return s.anyMark("cloud") }),
		[]string{"infra-review", "config-review"}},
	{"retries, queues, or backoff", weightStrong, present(func(s signals) bool { return s.anyMark("retry") }),
		[]string{"idempotency-review", "dr-review", "error-review"}},
	{"translated strings in the source", weightStrong,
		present(func(s signals) bool { return s.anyMark("translate") }), []string{"i18n-review"}},
	{"backup or failover code", weightStrong, present(func(s signals) bool { return s.anyMark("recovery") }),
		[]string{"dr-review", "idempotency-review"}},
	{"floating-point or decimal arithmetic", weightNormal,
		present(func(s signals) bool { return s.anyMark("numeric") }), []string{"numerics-review"}},
	{"procedural geometry", weightNormal, lang(".scad"), []string{"numerics-review"}},
	{"a command line interface", weightStrong, present(func(s signals) bool { return s.anyMark("cli") }),
		[]string{"cli-review", "threat-review"}},
	{"a terminal interface", weightStrong, present(func(s signals) bool { return s.anyMark("tui") }),
		[]string{"ux-review", "design-review", "a11y-review", "uislop-review"}},
	{"a graphical interface", weightStrong, present(func(s signals) bool { return s.anyMark("gui") || s.count(".xaml", ".qml") > 0 }),
		[]string{"ux-review", "design-review", "a11y-review", "uislop-review", "resource-review"}},
	{"browser DOM code", weightStrong, present(func(s signals) bool { return s.anyMark("dom") }),
		[]string{"ux-review", "a11y-review", "uislop-review", "webperf-review"}},
	{"input parsing", weightStrong, present(func(s signals) bool { return s.anyMark("parse") }),
		[]string{"fuzz-review", "threat-review", "unicode-review"}},
	{"file or network side effects", weightStrong, present(func(s signals) bool { return s.anyMark("write", "httpclient") }),
		[]string{"idempotency-review", "threat-review", "unicode-review"}},
	{"resource acquisition", weightStrong, present(func(s signals) bool { return s.anyMark("resource") }),
		[]string{"resource-review"}},
	{"personal data handling", weightStrong, present(func(s signals) bool { return s.anyMark("personal") }),
		[]string{"privacy-review"}},
	{"concurrent state or queued work", weightStrong, present(func(s signals) bool {
		return s.anyMark("concurrent") && s.anyMark("sql", "retry", "write")
	}),
		[]string{"dst-review"}},
	{"declared build dependencies", weightNormal, present(func(s signals) bool { return s.anyMark("dependency") }),
		[]string{"deps-review"}},
	{"a source tree with a build and contribution surface", weightNormal, present(func(s signals) bool {
		return s.source >= 2 && (hasCI(s) || hasTests(s) || hasDocs(s)) && s.anyName("makefile", "cmakelists.txt", "go.mod", "cargo.toml", "package.json", "pyproject.toml")
	}), []string{"dx-review"}},
}

// isTestFile recognizes the naming conventions test files follow, since a
// project is as likely to keep them beside the code as in a tests directory.
func isTestFile(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return strings.HasSuffix(base, "_test") || strings.HasSuffix(base, "_spec") ||
		strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") ||
		strings.HasPrefix(base, "test_")
}

// scored is one review with the evidence behind it.
type scored struct {
	name    string
	score   float64
	reasons []reason
}

// reason is one piece of evidence for a review and what that evidence was
// worth, so the printed line can lead with the strongest rather than the first
// the rule table happened to reach.
type reason struct {
	text   string
	weight float64
}

// topReasons returns the reasons worth printing, strongest first, dropping the
// rest. Ties keep rule order, so an equal-weight list reads in table order.
func (s scored) topReasons(n int) string {
	ordered := slices.Clone(s.reasons)
	slices.SortStableFunc(ordered, func(a, b reason) int {
		return cmp.Compare(b.weight, a.weight)
	})
	texts := make([]string, 0, min(len(ordered), n))
	for _, r := range ordered[:min(len(ordered), n)] {
		texts = append(texts, r.text)
	}
	return strings.Join(texts, ", ")
}

// Reviews reads the tree and returns the reviews its files justify, best
// evidence first, restricted to the pool. Reviews whose evidence does not
// reach minScore are left out: proposing everything would be the same as
// proposing nothing.
//
// The error is the journal this directory's history is read from, returned
// beside the picks rather than folded into them: an unreadable index leaves
// every review at its neutral weight, which quietly re-proposes the reviews
// that have already finished here without changing a line several times
// over. The file evidence stands on its own, so the picks are still worth
// having; the caller says why the weighting is missing.
//
// now is the clock the churn window is measured back from. Nil means wall
// time. The caller passes the run's bus clock, so a seeded run replayed later
// reads the same churn over the same tree and proposes the same reviews
// instead of a different set because the calendar moved.
func Reviews(dir string, pool []string, set prompt.Set, now func() time.Time) ([]prompt.Suggestion, error) {
	if now == nil {
		now = time.Now
	}
	marks, declaredBy := declared(pool, set)
	s, scanErr := scan(dir, marks, now)
	rank := make(map[string]int, len(pool))
	for i, name := range pool {
		rank[name] = i
	}
	by := map[string]*scored{}
	add := func(name, why string, points float64) {
		if _, ok := rank[name]; !ok || points <= 0 {
			return
		}
		got := by[name]
		if got == nil {
			got = &scored{name: name}
			by[name] = got
		}
		got.score += points
		if !slices.ContainsFunc(got.reasons, func(r reason) bool { return r.text == why }) {
			got.reasons = append(got.reasons, reason{text: why, weight: points})
		}
	}

	for _, r := range fastRules {
		strength := r.when(s)
		if strength <= 0 {
			continue
		}
		for _, review := range r.reviews {
			add(review, r.reason, r.weight*strength)
		}
	}
	// A review can also speak for itself, which is the only way a project's
	// own prompt is reachable here: the rules above only know built-in names.
	for _, name := range pool {
		if token, matched := matchDeclared(s, declaredBy[name]); matched {
			add(name, "signals it declares ("+token+")", weightStrong)
		}
	}

	history, historyErr := journal.History(dir)
	out := make([]scored, 0, len(by))
	for _, got := range by {
		got.score *= historyWeight(history[got.name])
		if got.score >= minScore {
			out = append(out, *got)
		}
	}
	slices.SortFunc(out, func(a, b scored) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(rank[a.name], rank[b.name])
	})

	picked := make([]prompt.Suggestion, 0, len(out))
	for _, got := range out {
		picked = append(picked, prompt.Suggestion{
			Name:   got.name,
			Reason: got.topReasons(reasonsShown),
		})
	}
	return picked, errors.Join(scanErr, historyErr)
}

// matchDeclared reports whether the tree carries any signal a review declared,
// and which one, so the run can print the evidence rather than the claim.
func matchDeclared(s signals, declared []string) (string, bool) {
	for _, token := range declared {
		kind, value, ok := strings.Cut(token, ":")
		if !ok {
			continue
		}
		switch kind {
		case "ext":
			ok = s.ext[value] > 0
		case "name":
			ok = s.name[value]
		case "path":
			ok = s.path[value]
		case "mark":
			ok = s.mark["mark:"+value] > 0
		default:
			ok = false
		}
		if ok {
			return token, true
		}
	}
	return "", false
}

// historyWeight is what this directory's own past runs say about a review:
// one that keeps finding work here is worth more, and one that has finished
// several times without changing a line is worth less. Directories with no
// history are unaffected.
func historyWeight(h journal.ReviewHistory) float64 {
	switch {
	case h.Runs >= historyMinRuns && h.Changed == 0:
		return historyPenalty
	case h.Runs > 0 && float64(h.Changed)/float64(h.Runs) >= historyGoodRate:
		return historyBoost
	default:
		return 1
	}
}

// scan collects what the rules ask about, in one pass over the tree. declared
// are the `mark:` substrings the reviews in the pool asked for, looked for
// while the heads are being read anyway.
//
// The error is what the scan could not see, not whether the scan worked: a
// tree that could not be listed and a root that could not be opened both
// still yield signals, but the ones derived from what is missing would read
// as evidence of absence. Reviews joins it with the history read's error so
// one message covers everything this pass had to assume.
func scan(dir string, declared []string, now func() time.Time) (signals, error) {
	s := signals{
		ext: map[string]int{}, name: map[string]bool{},
		path: map[string]bool{}, mark: map[string]int{}, hot: map[string]int{},
	}
	root := filepath.Clean(dir)
	ctx, cancel := context.WithTimeout(context.Background(), churnTimeout)
	defer cancel()
	paths, repo, err := listTree(ctx, root)
	for _, rel := range paths {
		if s.files >= scanMaxFiles {
			break
		}
		s.files++
		record(&s, rel)
	}
	peekErr := peek(root, paths, &s, declared)
	if repo == nil {
		return s, errors.Join(err, peekErr)
	}
	// Git listed the tree, so it can also say which part of it is alive.
	// The same handle already paid for the safe-config overlay on ListFiles;
	// a second Open would build that overlay again for the same repo.
	//
	// The window is measured back from the run's own clock, so a replay of the
	// same tree on the same seed sees the same churn: git resolving "90 days
	// ago" against its wall clock would let the calendar, not the seed, decide
	// which parts of a tree count as alive.
	cutoff := now().Add(-churnWindow)
	changed, churnErr := repo.ChangedSince(ctx, cutoff)
	switch {
	case churnErr != nil:
		// A read that failed leaves churn off, and churn off weights every
		// dormant area as live. That is a silent change to the suggestion
		// set, so it is reported rather than absorbed.
		churnErr = fmt.Errorf("cannot read which files changed since %s: %w",
			cutoff.Format(time.DateOnly), churnErr)
	case len(changed) > 0:
		s.churn = true
		for _, rel := range changed {
			s.hot[strings.ToLower(filepath.Ext(nfcPath(rel)))]++
		}
	}
	return s, errors.Join(err, peekErr, churnErr)
}

// churnTimeout caps the history read and the tree listing that runs beside it.
// A suggestion is not worth waiting on a repository with a decade of commits,
// or on one whose file listing never finishes.
const churnTimeout = 10 * time.Second

// nfcPath slashes and NFC-normalizes one path received from outside (git
// output or a directory walk). ASCII input passes through untouched at
// no cost; only a path with combining marks pays.
func nfcPath(p string) string {
	return fuzzy.NFC(filepath.ToSlash(p))
}

// record files one path into the signal sets.
//
// nfcPath first: a tree's filenames arrive in whatever bytes the filesystem
// and git carry (macOS hands out NFD), while the values a review declares in
// its Signals: line are author-typed, usually NFC. Both sides store NFC so
// the two forms meet byte-exactly when matchDeclared compares them.
func record(s *signals, rel string) {
	rel = nfcPath(rel)
	lower := strings.ToLower(rel)
	base := path.Base(lower)
	if isTestFile(base) {
		s.tests = true
	}
	s.name[base] = true
	s.path[lower] = true
	for dir := path.Dir(lower); dir != "." && dir != "/"; dir = path.Dir(dir) {
		s.path[dir] = true
		s.path[path.Base(dir)] = true
	}
	if ext := strings.ToLower(path.Ext(base)); ext != "" {
		s.ext[ext]++
		if sourceExts[ext] {
			s.source++
		}
	}
}

// listTree returns the tree's files relative to root, and the git handle that
// listed them (nil when the walk was the fallback). Git knows what the project
// considers source; the walk is for a directory that is not a repository. The
// handle is reused for the churn window so the safe-config overlay is paid
// once.
//
// ctx bounds both paths and covers the walk as well as the git read. The walk
// is the fallback taken precisely when git failed or was too slow, so leaving
// it unbounded replaced a ten-second failure with a hang on the interactive
// suggest path. The walk's own error is returned: a root that cannot be
// walked is not a tree with nothing in it, and the caller has to be able to
// say which one it got.
func listTree(ctx context.Context, root string) ([]string, *gitx.Repo, error) {
	repo := gitx.Open(root)
	// Cap at scanMaxFiles so a million-file listing is not kept as one
	// string that the first hundred thousand paths would pin. An empty
	// listing is an answer (a tree with no files in it), not a failure to
	// answer, and the walk below cannot tell the two apart.
	if paths, err := repo.ListFilesAtMost(ctx, scanMaxFiles); err == nil {
		paths = slices.DeleteFunc(paths, func(rel string) bool {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			return slices.ContainsFunc(parts[:len(parts)-1], skipDir)
		})
		return paths, repo, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if p == root {
				// The root itself could not be read, so nothing was walked at
				// all. Skipping it like an unreadable corner below would
				// report an unreadable path as a tree with no files in it.
				return err
			}
			return nil // an unreadable corner says nothing; keep walking
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root {
				if skipDir(d.Name()) {
					return fs.SkipDir
				}
				if strings.Count(filepath.ToSlash(rel), "/")+1 > scanMaxDepth {
					return fs.SkipDir
				}
			}
			return nil
		}
		if len(out) >= scanMaxFiles {
			return fs.SkipAll
		}
		out = append(out, rel)
		return nil
	})
	return out, nil, err
}

// peek reads source and package/build manifest heads for capability markers.
// A bounded read keeps this a scan rather than an indexing pass. Opens are
// rooted at the reviewed tree, so a FIFO or escaping path is skipped.
//
// Imports and package fields share the same file/head budget as literal marks.
func peek(root string, paths []string, s *signals, declared []string) error {
	dir, err := os.OpenRoot(root)
	if err != nil {
		// A root the process cannot open turns every content rule off, so the
		// run would answer from file names alone and read as though the tree
		// carries no HTTP, no SQL, no auth. That is missing evidence, not
		// negative evidence.
		return fmt.Errorf("cannot open %s to read its files: %w", root, err)
	}
	defer dir.Close()
	wanted := markSearch(declared)
	buf := make([]byte, peekBytes)
	scratch := make([]byte, peekBytes)
	read := 0
	for _, rel := range paths {
		if read >= peekMaxFiles {
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(rel))] && !manifestNames[strings.ToLower(filepath.Base(rel))] {
			continue
		}
		f, err := openPeek(dir, rel)
		if err != nil {
			continue
		}
		n, readErr := f.Read(buf)
		f.Close()
		// A read that failed or returned nothing is not the head of the file,
		// and a partial head is worse than none: a mark past the truncation
		// reads as undeclared, and one inside the prefix reads as declared by
		// a file this pass never finished. Either way the signal would be
		// wrong, so the file is left unscanned.
		if readErr != nil || n == 0 {
			continue
		}
		read++
		name := strings.ToLower(filepath.Base(rel))
		if name == "package.json" || name == "plugin.json" || name == "pyproject.toml" || name == "cargo.toml" || name == "setup.cfg" {
			packageMetadata(s, name, buf[:n])
			markFound(s, wanted[len(marks):], asciiFold(scratch[:0], buf[:n]))
		} else {
			sourceImports(s, rel, buf[:n])
			markFound(s, wanted, asciiFold(scratch[:0], buf[:n]))
		}
	}
	return nil
}

// markFound records which of the given searches a file head satisfies. A kind
// already recorded is left alone, since every consumer treats a kind as seen or
// unseen rather than counting it.
func markFound(s *signals, wanted []markEntry, head []byte) {
	var folded string
	hasFolded := false
	for _, m := range wanted {
		if s.mark[m.says] > 0 {
			continue
		}
		hit := false
		if m.needle != nil {
			hit = containsMark(head, m)
		} else {
			// Non-ASCII needles are stored NFC+ToLower by Signals; a macOS
			// file may hold the NFD spelling, and a capital É survives
			// asciiFold. Fold the haystack the same way so the two forms meet.
			if !hasFolded {
				folded = strings.ToLower(norm.NFC.String(string(head)))
				hasFolded = true
			}
			hit = strings.Contains(folded, m.text)
		}
		if hit {
			s.mark[m.says]++
		}
	}
}

func identifierByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '$' || b >= 0x80
}

// containsMark skips identifier fragments such as gin. in plugin. and redis
// in rediscover, but keeps searching for a later standalone occurrence.
func containsMark(head []byte, m markEntry) bool {
	for offset := 0; offset < len(head); {
		i := bytes.Index(head[offset:], m.needle)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(m.needle)
		if (!m.left || i == 0 || !identifierByte(head[i-1])) &&
			(!m.right || end == len(head) || !identifierByte(head[end])) {
			return true
		}
		offset = i + 1
	}
	return false
}

// openPeek opens rel under root for a bounded head read. The root is the
// reviewed tree: a path that escapes it, a last-component symlink, or a
// planted FIFO or device is skipped rather than followed or waited on.
func openPeek(root *os.Root, rel string) (*os.File, error) {
	f, err := root.OpenFile(filepath.ToSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, os.ErrInvalid
	}
	_ = syscall.SetNonblock(int(f.Fd()), false)
	return f, nil
}

// declaredMarkMax bounds how many review-declared substrings are searched for.
// Each one costs a scan of every file head, the prompts come from the reviewed
// tree, and Signals already caps a single review at signalMax: this is the
// ceiling across all of them, so a tree carrying fifty prompts cannot turn a
// suggestion into a full-text search.
const declaredMarkMax = 64

// markSearch is the built-in table plus the substrings reviews declared with
// `mark:`.
//
// Declared substrings use their own namespace, so a category derived from an
// import or package field cannot satisfy a literal mark: declaration.
func markSearch(declared []string) []markEntry {
	if len(declared) == 0 {
		return marks
	}
	// A fresh slice: appending onto the package-level table would write into
	// it the moment it had spare capacity.
	out := make([]markEntry, 0, len(marks)+min(len(declared), declaredMarkMax))
	out = append(out, marks...)
	seen := make(map[string]bool, len(declared))
	for _, d := range declared {
		if d == "" || seen[d] {
			continue
		}
		if len(out)-len(marks) >= declaredMarkMax {
			break
		}
		seen[d] = true
		e := mark(d, "mark:"+d)
		e.left, e.right = false, false
		out = append(out, e)
	}
	return out
}

// declared reads the pool's reviews' `Signals:` lines once and returns them
// two ways: the `mark:` values in pool order and without repeats, which is
// what peek looks for in the same pass it already makes over the file heads,
// and every review's full token list, which the scoring loop matches. Reading
// them once is the point: Signals re-reads the prompt body, and a project
// prompt is an open and a read per call.
func declared(pool []string, set prompt.Set) ([]string, map[string][]string) {
	var marks []string
	seen := map[string]bool{}
	byReview := make(map[string][]string, len(pool))
	for _, name := range pool {
		rev, ok := set.Get(name)
		if !ok {
			continue
		}
		tokens := rev.Signals()
		if len(tokens) == 0 {
			continue
		}
		byReview[name] = tokens
		for _, token := range tokens {
			kind, value, ok := strings.Cut(token, ":")
			if !ok || kind != "mark" || value == "" || seen[value] {
				continue
			}
			seen[value] = true
			marks = append(marks, value)
		}
	}
	return marks, byReview
}

// asciiFold appends b lowercased to dst, ASCII-only. Source heads are
// overwhelmingly ASCII and bytes.ToLower would allocate a fresh buffer per
// file; folding in place keeps the peek allocation-free after startup.
func asciiFold(dst, b []byte) []byte {
	for _, c := range b {
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}
