# review-prompts

Auto-fix review loop for codebases. Runs a set of review prompts (security, perf, docs, etc.) via `claude`, `gemini`, `qwen`, `codex`, `grok`, and/or `agy` CLI tools against the current directory, applying fixes directly rather than producing reports.

## Contents

- `*-review.md` — review prompts, one per concern. Auto-discovered by the runner.
- `review-loop.py` — runner. Iterates over selected reviews, dispatches each to a randomly chosen tool/model, repeats until stopped.

### Available reviews

| Review | Focus |
|---|---|
| `a11y-review` | Accessibility, WCAG 2.2 AA, keyboard, screen readers, contrast, motion |
| `api-review` | API design, consistency, error handling, versioning |
| `arch-review` | Architecture, module boundaries, dependency direction, layering |
| `build-review` | Build reproducibility, hermeticity, toolchain pinning, artifact correctness |
| `cli-review` | CLI usability, flags, help text, output design, scripting support |
| `code-review` | Code quality, duplication, dead code, refactoring, type safety |
| `concurrency-review` | Race conditions, deadlocks, shared state, async correctness, thread safety |
| `config-review` | Configuration management, environment separation, secrets, feature flags |
| `db-review` | Schema design, queries, migrations, data integrity, indexing |
| `deps-review` | Dependency health, unused packages, vulnerabilities, license compliance |
| `design-review` | Technical design decisions, tradeoffs, alternatives, data modeling, tech selection |
| `doc-review` | Documentation accuracy, coverage, onboarding, architecture docs |
| `error-review` | Error handling, resilience, retries, timeouts, failure isolation |
| `functionality-review` | Feature completeness, behavioral correctness, edge cases, contract mismatches |
| `fuzz-review` | Fuzz testing coverage across API surfaces, untrusted input, crash/hang robustness |
| `i18n-review` | Internationalization, localization, locale handling, RTL, formatting |
| `infra-review` | CI/CD, containers, IaC, deployment, secret management |
| `o11y-review` | Observability: logging, metrics, tracing, alerting, health checks |
| `perf-review` | Performance bottlenecks, memory, I/O, caching, hot paths |
| `privacy-review` | Data privacy, GDPR/CCPA compliance, PII handling, consent, data subject rights |
| `release-review` | Versioning/semver, breaking-change gating, changelog, deprecation, migration |
| `sdk-review` | SDK developer experience, API surface, types, versioning, testability, docs |
| `sec-review` | Security vulnerabilities, auth, injection, data exposure, cryptography |
| `test-review` | Test quality, coverage gaps, flaky tests, mock quality, test design |
| `ux-review` | UX, accessibility, interaction design, forms, responsive layout |

## Requirements

- Python 3.10+
- At least one of: `claude`, `gemini`, `qwen`, `codex`, `grok`, `agy` in `PATH`
- `tee` (only if `--log` is used)

## Quick start

```sh
# auto-detect installed tools (claude/gemini/qwen/codex/grok/agy)
./review-loop.py

# list available reviews
./review-loop.py --list

# single tool
./review-loop.py --models claude

# all supported tools, default models per tool
./review-loop.py --models mixed

# pick randomly across an explicit list
./review-loop.py --models claude,gemini,codex

# pin specific models
./review-loop.py --models claude:opus-4-7,codex:gpt-5-codex,gemini

# same tool with multiple models
./review-loop.py --models claude:opus-4-7,claude:sonnet-4-6

# run only specific reviews
./review-loop.py --models claude --reviews code-review,sec-review,error-review

# skip reviews that don't apply
./review-loop.py --exclude db-review,ux-review
```

Run `./review-loop.py --help` for the full option list.

## Options

| Flag | Default | Purpose |
|---|---|---|
| `--models` | auto-detect | Comma-separated `tool` or `tool:model` entries (one is sampled per review). `mixed`/`random`/`all` expands to every supported tool. Default: every tool found in `PATH`. |
| `--dir` | cwd | `cd` here before running. |
| `--once` | off | Run a single loop and exit. |
| `--max-loops N` | 0 (infinite) | Stop after N loops. |
| `--timeout DUR` | `30m` | Per-review timeout (`90s`, `30m`, `1h`, `2d`). |
| `--log FILE` | — | Tee stdout/stderr to FILE. |
| `--prompt-dir DIR` | script dir | Where `*-review.md` files live. |
| `--reviews LIST` | all | Comma-separated subset to run. |
| `--exclude LIST` | none | Comma-separated reviews to skip. |
| `--dry-run` | off | Print planned schedule and exit. |
| `--list` | off | List available reviews and exit. |

## Behavior

- Each loop: reviews are shuffled, each runs once with a random model from `--models`.
- Each review is hard-bounded by `--timeout`; on timeout the process group is `SIGTERM`'d, then `SIGKILL`'d after 10s.
- `Ctrl+C` once: terminates the active review and stops cleanly. Twice: force-kills.
- A `flock`-based lockfile (`.review-loop.lock`) prevents concurrent runs in the same directory.
- The injected prompt header/footer constrains each tool to: apply small fixes only, skip vendored/generated/lockfile paths, never commit, run lint/typecheck/tests if configured, and revert on failure.
- At exit, per-tool and per-review statistics are printed.

## Adding a review

Drop a new `<name>-review.md` into the prompt directory. It is auto-discovered — no code changes needed.

## Prompt structure

All review prompts follow a consistent structure:

1. **Role and goal** — who the reviewer is and what they evaluate.
2. **Numbered checklist** (10 sections) — specific items to check, grouped by concern.
3. **Instructions** — how to approach the review: priorities, distinctions, scope.
4. **Finding template** — fields for each finding (Title, Severity, Category, Location, Confidence, Why, Evidence, Recommendation, Expected benefit, Estimated effort, plus domain-specific extras).
5. **Output format** — structured report sections.
6. **Important** — constraints and ground rules.

## License

No license specified. Treat as all-rights-reserved unless changed.
