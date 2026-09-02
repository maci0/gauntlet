// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bytes"
	"cmp"
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/maci0/gauntlet/internal/gitx"
	"github.com/maci0/gauntlet/internal/journal"
	"github.com/maci0/gauntlet/internal/prompt"
)

// The suggester that is not an agent.
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

// FastSuggestAgent is the value --suggest-agent takes to use this instead of
// launching an agent.
const FastSuggestAgent = "gauntlet"

// Scan limits. A repository with a million files is not worth a better answer
// than the first hundred thousand paths already give, and the content peek
// reads heads, not files: what a source file imports is in its first lines.
const (
	scanMaxFiles = 100_000
	scanMaxDepth = 12
	peekMaxFiles = 2_000
	peekBytes    = 4 << 10
	churnWindow  = "90 days ago"
)

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

// archMinFiles is where a codebase is large enough for its shape to be worth
// reviewing on its own, rather than being one program you read end to end.
const archMinFiles = 200

// reasonsShown bounds the evidence printed beside a suggestion: the strongest
// few say why, a full list says nothing.
const reasonsShown = 3

// skipDirs are never walked when git cannot list the tree: they hold other
// people's code or this tool's own scratch space, and neither says anything
// about the project under review.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "target": true, ".next": true, ".venv": true,
	"venv": true, ".gauntlet": true, ".crush": true, "__pycache__": true,
	".mypy_cache": true, ".pytest_cache": true, ".tox": true, ".idea": true,
}

// sourceExts are the files worth reading the head of. Everything else is
// counted but not opened: a lockfile or a minified bundle says nothing that
// its name has not said already.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".rs": true, ".c": true, ".cc": true, ".cpp": true,
	".cxx": true, ".h": true, ".hpp": true, ".java": true, ".kt": true, ".swift": true,
	".rb": true, ".php": true, ".cs": true, ".zig": true, ".ts": true, ".tsx": true,
	".js": true, ".jsx": true, ".mjs": true, ".vue": true, ".svelte": true,
	".sh": true, ".bash": true, ".sql": true, ".lua": true, ".dart": true, ".ex": true,
}

// markEntry is one substring to look for and what finding it says about the
// code. A review's own `mark:` signal becomes one of these too, searching for
// itself under its own name.
type markEntry struct {
	text, says string
	// needle is text as bytes, set for ASCII marks so peek can search file
	// heads without allocating a copy per file per mark. Nil means the
	// haystack has to be folded for a non-ASCII needle.
	needle []byte
}

// mark is one built-in substring search. ASCII needles are stored as bytes
// once so peek does not allocate a copy per file per mark.
func mark(text, says string) markEntry {
	e := markEntry{text: text, says: says}
	if isASCII(text) {
		e.needle = []byte(text)
	}
	return e
}

// marks maps a substring found in source to what it says about the code. Names
// on the left are lowercased before the search, so a match is case-insensitive
// without a regex engine or a second pass over the text.
var marks = []markEntry{
	mark("net/http", "http"), mark("http.handle", "http"), mark("fastapi", "http"),
	mark("flask", "http"), mark("express(", "http"), mark("axum::", "http"), mark("gin.", "http"),
	mark("database/sql", "sql"), mark("psycopg", "sql"), mark("sqlalchemy", "sql"),
	mark("sqlite3", "sql"), mark("pg.pool", "sql"), mark("gorm.", "sql"),
	mark("time.now", "clock"), mark("datetime.now", "clock"), mark("time.sleep", "clock"),
	mark("utcnow", "clock"), mark("cron", "clock"),
	mark("go func", "concurrent"), mark("asyncio", "concurrent"), mark("threading.", "concurrent"),
	mark("sync.mutex", "concurrent"), mark("std::thread", "concurrent"), mark("tokio::", "concurrent"),
	mark("multiprocessing", "concurrent"), mark("pthread_", "concurrent"),
	mark("prometheus", "telemetry"), mark("opentelemetry", "telemetry"), mark("otel", "telemetry"),
	mark("logrus", "telemetry"), mark("structlog", "telemetry"),
	mark("redis", "cache"), mark("memcache", "cache"), mark("lru_cache", "cache"),
	mark("jwt", "auth"), mark("oauth", "auth"), mark("bcrypt", "auth"), mark("argon2", "auth"),
	mark("session[", "auth"), mark("set-cookie", "auth"),
	mark("unsafe.pointer", "unsafe"), mark("ctypes", "unsafe"), mark("eval(", "unsafe"),
	mark("pickle.loads", "unsafe"), mark("innerhtml", "unsafe"),
	mark("subprocess", "exec"), mark("exec.command", "exec"), mark("os/exec", "exec"),
	mark("anthropic", "model"), mark("openai", "model"), mark("completions.create", "model"),
	mark("ollama", "model"), mark("system_prompt", "model"),
	mark("boto3", "cloud"), mark("kubernetes", "cloud"), mark("terraform", "cloud"),
	mark("idempotenc", "retry"), mark("retry(", "retry"), mark("backoff", "retry"),
	mark("celery", "retry"), mark("sqs", "retry"),
	mark("argparse", "cli"), mark("click.command", "cli"), mark("cobra.command", "cli"),
	mark("flag.parse", "cli"), mark("clap::", "cli"), mark("commander", "cli"), mark("yargs", "cli"),
	mark("bubbletea", "tui"), mark("ratatui", "tui"), mark("curses", "tui"),
	mark("rich.console", "tui"), mark("blessed", "tui"),
	mark("gettext", "translate"), mark("i18n", "translate"), mark("usetranslation", "translate"),
	mark("backup", "recovery"), mark("restore(", "recovery"), mark("failover", "recovery"),
	mark("float32", "numeric"), mark("float64", "numeric"), mark("numpy", "numeric"),
	mark("decimal(", "numeric"), mark("round(", "numeric"),
}

// signals is what one pass over a tree found. Counts, not booleans: how much
// of a thing there is decides whether its reviews are worth proposing.
type signals struct {
	files int
	ext   map[string]int
	name  map[string]bool
	path  map[string]bool
	mark  map[string]int
	hot   map[string]int // extension to files changed inside the churn window
	churn bool           // the repository reported recent commits
	tests bool
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
// Missing tests, missing docs, and missing CI are the strongest arguments for
// the reviews that would add them, and presence-only rules said the opposite.
func absent(fn func(signals) bool) func(signals) float64 {
	return func(s signals) float64 {
		if s.files > 0 && !fn(s) {
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
		if n == 0 || (n < langMinFiles && float64(n)/float64(max(s.files, 1)) < langMinShare) {
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
	hasWeb = lang(".html", ".css", ".scss", ".jsx", ".tsx", ".vue", ".svelte")
)

var fastRules = []rule{
	// Every tree with code in it gets these: any code can be wrong, wasteful,
	// padded, sloppily typed, or carrying a vulnerability.
	{"source files to read", weightNormal, present(func(s signals) bool { return s.files > 0 }),
		[]string{"code-review", "sec-review", "minimalism-review", "slop-review",
			"lint-review", "functionality-review", "structure-review"}},
	{"a test suite", weightNormal, present(hasTests), []string{"test-review", "dst-review"}},
	{"no tests found anywhere in the tree", weightStrong, absent(hasTests),
		[]string{"test-review", "fuzz-review"}},
	{"documentation", weightNormal, present(hasDocs), []string{"doc-review"}},
	{"no documentation in the tree", weightStrong, absent(hasDocs),
		[]string{"doc-review", "specs-review"}},
	{"CI workflows", weightNormal, present(hasCI), []string{"infra-review", "build-review"}},
	{"no CI configuration", weightNormal, absent(hasCI), []string{"build-review", "release-review"}},
	{"a linter configuration", weightNormal, present(hasLinter), []string{"lint-review", "style-review"}},
	{"a Dockerfile or compose file", weightStrong, present(func(s signals) bool {
		return s.anyName("dockerfile", "containerfile", "docker-compose.yml", "compose.yaml", "compose.yml")
	}), []string{"container-review"}},
	{"infrastructure as code", weightStrong, present(func(s signals) bool {
		return s.count(".tf", ".tfvars") > 0 || s.anyName("ansible.cfg", "playbook.yml") ||
			s.anyPath("charts", "manifests", "kustomization.yaml")
	}), []string{"infra-review", "container-review", "dr-review"}},
	{"a build system", weightNormal, present(func(s signals) bool {
		return s.anyName("makefile", "justfile", "cmakelists.txt", "meson.build", "build.gradle", "pom.xml")
	}), []string{"build-review"}},
	{"shell scripts", weightNormal, lang(".sh", ".bash", ".zsh"),
		[]string{"cli-review", "compat-review"}},
	{"a Go module", weightNormal, present(func(s signals) bool { return s.anyName("go.mod") }),
		[]string{"error-review", "deps-review"}},
	{"a Rust crate", weightNormal, present(func(s signals) bool { return s.anyName("cargo.toml") }),
		[]string{"resource-review", "deps-review"}},
	{"C or C++ sources", weightNormal, lang(".c", ".cc", ".cpp", ".cxx", ".h", ".hpp"),
		[]string{"resource-review", "numerics-review", "fuzz-review"}},
	{"a Python package", weightNormal, present(func(s signals) bool {
		return s.anyName("pyproject.toml", "requirements.txt", "setup.py")
	}), []string{"deps-review", "error-review"}},
	{"a JavaScript or TypeScript package", weightNormal,
		present(func(s signals) bool { return s.anyName("package.json") }),
		[]string{"deps-review", "dx-review"}},
	{"a web frontend", weightNormal, hasWeb,
		[]string{"ux-review", "a11y-review", "uislop-review", "webperf-review", "design-review"}},
	{"mobile sources", weightNormal, lang(".swift", ".kt", ".dart"), []string{"mobile-review"}},
	{"SQL or migrations", weightNormal, present(func(s signals) bool {
		return s.count(".sql") > 0 || s.anyPath("migrations", "migrate")
	}), []string{"db-review", "dr-review"}},
	{"an API description", weightStrong, present(func(s signals) bool {
		return s.anyName("openapi.yaml", "openapi.json", "swagger.yaml", "schema.graphql") ||
			s.count(".proto") > 0
	}), []string{"api-review", "compat-review", "sdk-review"}},
	{"configuration files", weightWeak, present(func(s signals) bool {
		return s.count(".yaml", ".yml", ".toml", ".ini") > 0 || s.anyName(".env.example")
	}), []string{"config-review"}},
	{"translation files", weightStrong, present(func(s signals) bool {
		return s.count(".po", ".pot") > 0 || s.anyPath("locales", "i18n", "translations")
	}), []string{"i18n-review"}},
	{"agent instructions or prompts", weightStrong, present(func(s signals) bool {
		return s.anyName("claude.md", "agents.md", "cursor.md", "copilot-instructions.md") ||
			s.anyPath("prompts", ".claude", ".cursor")
	}), []string{"agentrules-review", "prompt-review", "skills-review"}},
	{"a packaged or released artifact", weightNormal, present(func(s signals) bool {
		return s.anyName("changelog.md", "pkgbuild", "debian") || s.count(".spec") > 0
	}), []string{"release-review", "pkg-review"}},
	{"a public API surface", weightWeak, present(func(s signals) bool {
		return s.anyPath("pkg", "lib", "api", "include", "sdk")
	}), []string{"sdk-review", "specs-review"}},
	{"compiled code where speed is visible", weightNormal, lang(".c", ".cc", ".cpp", ".rs", ".go", ".zig"),
		[]string{"perf-review"}},
	{"benchmarks", weightStrong, present(func(s signals) bool { return s.anyPath("bench", "benchmarks") }),
		[]string{"perf-review"}},
	{"secrets or credentials handling", weightStrong, present(func(s signals) bool {
		return s.anyName(".env", "secrets.yaml") || s.anyPath("secrets", "credentials")
	}), []string{"sec-review", "privacy-review"}},
	{"a tree with many parts", weightNormal, present(func(s signals) bool {
		return s.files >= archMinFiles || s.anyPath("packages", "services", "apps", "crates", "modules", "cmd")
	}), []string{"arch-review", "abstractions-review", "structure-review"}},

	// What the files say inside. Directory names are a guess about a codebase;
	// what it imports is a fact about it.
	{"HTTP handlers in the source", weightStrong, present(func(s signals) bool { return s.anyMark("http") }),
		[]string{"api-review", "authz-review", "threat-review", "o11y-review", "perf-review"}},
	{"database access in the source", weightStrong, present(func(s signals) bool { return s.anyMark("sql") }),
		[]string{"db-review", "sec-review", "dr-review"}},
	{"clock and calendar handling", weightStrong, present(func(s signals) bool { return s.anyMark("clock") }),
		[]string{"time-review"}},
	{"threads, goroutines, or async code", weightStrong,
		present(func(s signals) bool { return s.anyMark("concurrent") }),
		[]string{"concurrency-review", "resource-review"}},
	{"metrics or tracing calls", weightStrong, present(func(s signals) bool { return s.anyMark("telemetry") }),
		[]string{"o11y-review"}},
	{"a cache client", weightStrong, present(func(s signals) bool { return s.anyMark("cache") }),
		[]string{"cache-review", "perf-review"}},
	{"authentication code", weightStrong, present(func(s signals) bool { return s.anyMark("auth") }),
		[]string{"authz-review", "sec-review", "privacy-review", "threat-review"}},
	{"unsafe or dynamic evaluation", weightStrong, present(func(s signals) bool { return s.anyMark("unsafe") }),
		[]string{"sec-review", "fuzz-review", "resource-review"}},
	{"subprocess execution", weightStrong, present(func(s signals) bool { return s.anyMark("exec") }),
		[]string{"sec-review", "compat-review"}},
	{"model or LLM calls", weightStrong, present(func(s signals) bool { return s.anyMark("model") }),
		[]string{"llm-review", "prompt-review", "privacy-review"}},
	{"cloud or cluster APIs", weightStrong, present(func(s signals) bool { return s.anyMark("cloud") }),
		[]string{"infra-review", "dr-review", "config-review"}},
	{"retries, queues, or backoff", weightStrong, present(func(s signals) bool { return s.anyMark("retry") }),
		[]string{"idempotency-review", "dr-review", "error-review"}},
	{"translated strings in the source", weightStrong,
		present(func(s signals) bool { return s.anyMark("translate") }), []string{"i18n-review"}},
	{"backup or failover code", weightStrong, present(func(s signals) bool { return s.anyMark("recovery") }),
		[]string{"dr-review", "idempotency-review"}},
	{"floating-point or decimal arithmetic", weightNormal,
		present(func(s signals) bool { return s.anyMark("numeric") }), []string{"numerics-review"}},
	{"a command line interface", weightStrong, present(func(s signals) bool { return s.anyMark("cli") }),
		[]string{"cli-review", "ux-review", "dx-review"}},
	{"a terminal interface", weightStrong, present(func(s signals) bool { return s.anyMark("tui") }),
		[]string{"ux-review", "design-review", "a11y-review", "uislop-review"}},
	{"user-facing text", weightWeak, present(func(s signals) bool {
		return s.count(".md", ".txt", ".html") > 0
	}), []string{"unicode-review"}},
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
	reasons []string
}

// fastSuggest reads the tree and returns the reviews its files justify, best
// evidence first, restricted to the pool. Reviews whose evidence does not
// reach minScore are left out: proposing everything would be the same as
// proposing nothing.
func fastSuggest(dir string, pool []string, set prompt.Set) []prompt.Suggestion {
	s := scan(dir, declaredMarks(pool, set))
	rank := make(map[string]int, len(pool))
	for i, name := range pool {
		rank[name] = i
	}
	by := map[string]*scored{}
	add := func(name, reason string, points float64) {
		if _, ok := rank[name]; !ok || points <= 0 {
			return
		}
		got := by[name]
		if got == nil {
			got = &scored{name: name}
			by[name] = got
		}
		got.score += points
		if !slices.Contains(got.reasons, reason) {
			got.reasons = append(got.reasons, reason)
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
		review, ok := set.Get(name)
		if !ok {
			continue
		}
		if token, matched := matchDeclared(s, review.Signals()); matched {
			add(name, "signals it declares ("+token+")", weightStrong)
		}
	}

	history, _ := journal.History(dir)
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
			Reason: strings.Join(got.reasons[:min(len(got.reasons), reasonsShown)], ", "),
		})
	}
	return picked
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
			ok = s.mark[value] > 0
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
func scan(dir string, declared []string) signals {
	s := signals{
		ext: map[string]int{}, name: map[string]bool{},
		path: map[string]bool{}, mark: map[string]int{}, hot: map[string]int{},
	}
	root := filepath.Clean(dir)
	paths, repo := listTree(root)
	for _, rel := range paths {
		if s.files >= scanMaxFiles {
			break
		}
		s.files++
		record(&s, rel)
	}
	peek(root, paths, &s, declared)
	if repo == nil {
		return s
	}
	// Git listed the tree, so it can also say which part of it is alive.
	// The same handle already paid for the safe-config overlay on ListFiles;
	// a second Open would recompute it and rev-parse a baseline this scan
	// never uses.
	ctx, cancel := context.WithTimeout(context.Background(), churnTimeout)
	defer cancel()
	if changed, err := repo.ChangedSince(ctx, churnWindow); err == nil && len(changed) > 0 {
		s.churn = true
		for _, rel := range changed {
			s.hot[strings.ToLower(filepath.Ext(nfcPath(rel)))]++
		}
	}
	return s
}

// churnTimeout caps the history read. A suggestion is not worth waiting on a
// repository with a decade of commits.
const churnTimeout = 10 * time.Second

// nfcPath slashes and NFC-normalizes one path received from outside (git
// output or a directory walk). ASCII input passes through untouched at
// no cost; only a path with combining marks pays.
func nfcPath(p string) string {
	return norm.NFC.String(filepath.ToSlash(p))
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
	}
}

// listTree returns the tree's files relative to root, and the git handle that
// listed them (nil when the walk was the fallback). Git knows what the project
// considers source; the walk is for a directory that is not a repository. The
// handle is reused for the churn window so the safe-config overlay is paid
// once.
func listTree(root string) ([]string, *gitx.Repo) {
	ctx, cancel := context.WithTimeout(context.Background(), churnTimeout)
	defer cancel()
	repo := gitx.Open(root)
	if paths, err := repo.ListFiles(ctx); err == nil && len(paths) > 0 {
		return paths, repo
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner says nothing; keep walking
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			switch {
			case p == root:
				return nil
			case skipDirs[strings.ToLower(d.Name())],
				strings.Count(filepath.ToSlash(rel), "/")+1 > scanMaxDepth:
				return fs.SkipDir
			}
			return nil
		}
		if len(out) >= scanMaxFiles {
			return fs.SkipAll
		}
		out = append(out, rel)
		return nil
	})
	return out, nil
}

// peek reads the head of source files and records what they import and call.
// Heads only: what a file talks to is declared at the top, and a bounded read
// keeps this a scan rather than an indexing pass. Opens are rooted at the
// reviewed tree, so a symlink, FIFO, or path that escapes it is skipped.
//
// Marks are checked for presence, not counted: every consumer treats a kind
// as seen or unseen, so a kind already observed stops being searched for and
// the scan ends early once all kinds are.
func peek(root string, paths []string, s *signals, declared []string) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return
	}
	defer dir.Close()
	wanted := markSearch(declared)
	kinds := markKinds(wanted)
	buf := make([]byte, peekBytes)
	scratch := make([]byte, peekBytes)
	seenAll := len(s.mark) == kinds
	read := 0
	for _, rel := range paths {
		if read >= peekMaxFiles || seenAll {
			return
		}
		if !sourceExts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		f, err := openPeek(dir, rel)
		if err != nil {
			continue
		}
		n, _ := f.Read(buf)
		f.Close()
		read++
		head := asciiFold(scratch[:0], buf[:n])
		var folded string
		hasFolded := false
		for _, m := range wanted {
			if s.mark[m.says] > 0 {
				continue
			}
			hit := false
			if m.needle != nil {
				hit = bytes.Contains(head, m.needle)
			} else {
				// Non-ASCII needles are stored NFC+ToLower by Signals; a
				// macOS file may hold the NFD spelling, and a capital É
				// survives asciiFold. Fold the haystack the same way so
				// the two forms meet.
				if !hasFolded {
					folded = strings.ToLower(norm.NFC.String(string(head)))
					hasFolded = true
				}
				hit = strings.Contains(folded, m.text)
			}
			if hit {
				s.mark[m.says]++
				seenAll = len(s.mark) == kinds
			}
		}
	}
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
// A declared value searches for itself and is recorded under its own name,
// which is the token matchDeclared then looks up. Without this the mark set
// only ever held the built-in category labels, so a review's own `mark:` could
// match only by colliding with one of those -- the documented example,
// `mark:comptime`, could never fire at all.
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
		if d == "" || seen[d] || len(out)-len(marks) >= declaredMarkMax {
			continue
		}
		seen[d] = true
		out = append(out, mark(d, d))
	}
	return out
}

// markKinds is how many distinct things the given searches can say, the point
// at which peek's answer can no longer change.
func markKinds(wanted []markEntry) int {
	says := make(map[string]bool, len(wanted))
	for _, m := range wanted {
		says[m.says] = true
	}
	return len(says)
}

// declaredMarks collects the `mark:` values the pool's reviews declare, in
// pool order and without repeats, so peek can look for them in the same pass
// it already makes over the file heads.
func declaredMarks(pool []string, set prompt.Set) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range pool {
		rev, ok := set.Get(name)
		if !ok {
			continue
		}
		for _, token := range rev.Signals() {
			kind, value, ok := strings.Cut(token, ":")
			if !ok || kind != "mark" || value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
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

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
