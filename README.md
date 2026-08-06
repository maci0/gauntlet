# review-prompts

Auto-fix review loop for codebases. Runs a set of review prompts (security, perf, docs, etc.) via `claude`, `gemini`, `qwen`, `codex`, `grok`, `agy`, and/or `cursor-agent` CLI tools against the current directory, applying fixes directly rather than producing reports.

## Contents

- `prompts/*-review.md` — review prompts, one per concern. Auto-discovered by the runner.
- `test_review_loop.py` — tests for the runner's parsing and discovery logic.
- `review-loop.py` — runner. Iterates over selected reviews, dispatches each to a randomly chosen tool/model, repeats until stopped.

### Available reviews

| Review | Focus |
|---|---|
| [`a11y-review`](prompts/a11y-review.md) | Accessibility, WCAG 2.2 AA, keyboard, screen readers, contrast, motion |
| [`api-review`](prompts/api-review.md) | API design, consistency, error handling, versioning |
| [`arch-review`](prompts/arch-review.md) | Architecture, module boundaries, dependency direction, layering |
| [`build-review`](prompts/build-review.md) | Build reproducibility, hermeticity, toolchain pinning, artifact correctness |
| [`cli-review`](prompts/cli-review.md) | CLI usability, flags, help text, output design, scripting support |
| [`code-review`](prompts/code-review.md) | Code quality, duplication, dead code, refactoring, type safety |
| [`concurrency-review`](prompts/concurrency-review.md) | Race conditions, deadlocks, shared state, async correctness, thread safety |
| [`config-review`](prompts/config-review.md) | Configuration management, environment separation, secrets, feature flags |
| [`db-review`](prompts/db-review.md) | Schema design, queries, migrations, data integrity, indexing |
| [`deps-review`](prompts/deps-review.md) | Dependency health, unused packages, vulnerabilities, licenses, SBOM, provenance, registry risk |
| [`design-review`](prompts/design-review.md) | Technical design decisions, tradeoffs, alternatives, data modeling, tech selection |
| [`doc-review`](prompts/doc-review.md) | Documentation accuracy, coverage, onboarding, architecture docs |
| [`dst-review`](prompts/dst-review.md) | Deterministic simulation testing: injected clock/RNG/IO, fault injection, seed replay |
| [`error-review`](prompts/error-review.md) | Error handling, resilience, retries, timeouts, failure isolation |
| [`functionality-review`](prompts/functionality-review.md) | Feature completeness, behavioral correctness, edge cases, contract mismatches |
| [`fuzz-review`](prompts/fuzz-review.md) | Fuzz testing coverage across API surfaces, untrusted input, crash/hang robustness |
| [`i18n-review`](prompts/i18n-review.md) | Internationalization, localization, locale handling, RTL, formatting |
| [`infra-review`](prompts/infra-review.md) | CI/CD, containers, IaC, deployment, secret management |
| [`llm-review`](prompts/llm-review.md) | LLM integrations: prompt injection, untrusted output, agent loops, cost, evals, drift |
| [`minimalism-review`](prompts/minimalism-review.md) | Necessity proof per line, YAGNI, simpler/stdlib alternatives, deletion ledger |
| [`mobile-review`](prompts/mobile-review.md) | Mobile citizenship: lifecycle, offline, battery/data budgets, permissions, store readiness |
| [`o11y-review`](prompts/o11y-review.md) | Observability: logging, metrics, tracing, alerting, health checks |
| [`perf-review`](prompts/perf-review.md) | Performance bottlenecks, memory, I/O, caching, hot paths |
| [`pkg-review`](prompts/pkg-review.md) | Packaging: deb/rpm/PKGBUILD, Flatpak/Snap, container images, install/upgrade lifecycle |
| [`privacy-review`](prompts/privacy-review.md) | Data privacy, GDPR/CCPA compliance, PII handling, consent, data subject rights |
| [`release-review`](prompts/release-review.md) | Versioning/semver, breaking-change gating, changelog, deprecation, migration |
| [`sdk-review`](prompts/sdk-review.md) | SDK developer experience, API surface, types, versioning, testability, docs |
| [`sec-review`](prompts/sec-review.md) | Security vulnerabilities, auth, injection, data exposure, cryptography |
| [`slop-review`](prompts/slop-review.md) | Noise removal: redundant comments, copy-paste, dead code, churn, over-engineering |
| [`test-review`](prompts/test-review.md) | Test quality, coverage gaps, flaky tests, mock quality, test design |
| [`uislop-review`](prompts/uislop-review.md) | Generic AI visual design: template sameness, default tokens, microcopy slop, identity absence |
| [`ux-review`](prompts/ux-review.md) | UX, accessibility, interaction design, forms, responsive layout |

## Requirements

- Python 3.10+
- At least one of: `claude`, `gemini`, `qwen`, `codex`, `grok`, `agy`, `cursor-agent` in `PATH`
- `tee` (only if `--log` is used)

## Quick start

```sh
# auto-detect installed tools
./review-loop.py

# list available reviews
./review-loop.py --list

# check which recommended CLI tools are installed
./review-loop.py doctor

# single tool
./review-loop.py --agents claude

# all supported tools, default models per tool
./review-loop.py --agents mixed

# pick randomly across an explicit list
./review-loop.py --agents claude,gemini,codex

# pin specific models
./review-loop.py --agents claude:opus-4-7,codex:gpt-5-codex,gemini

# same tool with multiple models
./review-loop.py --agents claude:opus-4-7,claude:sonnet-4-6

# run only specific reviews
./review-loop.py --agents claude --reviews code-review,sec-review,error-review

# skip reviews that don't apply
./review-loop.py --exclude db-review,ux-review
```

Run `./review-loop.py --help` for the full option list.

## Options

| Flag | Default | Purpose |
|---|---|---|
| `doctor` | — | Subcommand: report which agent CLIs and recommended review tools are installed. Exits 1 if no agent CLI is found. |
| `--agents` (`--models` still accepted) | auto-detect | Comma-separated `tool` or `tool:model` entries (one is sampled per review; `agy` takes no model). `mixed`/`random`/`all` expands to every installed supported tool. Default: every tool found in `PATH`. |
| `--dir` | cwd | `cd` here before running. |
| `--once` | off | Run a single loop and exit. |
| `--max-loops N` | 0 (infinite) | Stop after N loops. |
| `--timeout DUR` | `30m` | Per-review timeout (`90s`, `30m`, `1h`, `2d`). |
| `--log FILE` | — | Tee stdout/stderr to FILE. |
| `--prompt-dir DIR` | `prompts/` next to script | Where `*-review.md` files live. |
| `--reviews LIST` | all | Comma-separated subset to run. |
| `--exclude LIST` | none | Comma-separated reviews to skip. |
| `--dry-run` | off | Print planned schedule and exit. |
| `--list` | off | List available reviews and exit. |
| `--version` | — | Print the version and exit. |

## Behavior

- Each loop: reviews are shuffled, each runs once with a random model from `--agents`.
- Each review is hard-bounded by `--timeout`; on timeout the process group is `SIGTERM`'d, then `SIGKILL`'d after 10s.
- `Ctrl+C` once: terminates the active review and stops cleanly. Twice: force-kills.
- A `flock`-based lockfile (`.review-loop.lock`) prevents concurrent runs in the same directory.
- The injected prompt header/footer constrains each tool to: apply small fixes only, skip vendored/generated/lockfile paths, never commit, run lint/typecheck/tests if configured, and revert on failure.
- At exit, summary statistics are printed: totals, per-tool breakdown (when multiple tools/models ran), and a list of failed or timed-out reviews.
- Exit code: 0 all reviews ran and passed; 1 any review failed, timed out, or was skipped; 2 usage error; 75 another instance holds the lock; 128+signal when interrupted, so 130 for SIGINT and 143 for SIGTERM (takes precedence over 1).

## Adding a review

Drop a new `<name>-review.md` into `prompts/`. It is auto-discovered — no code changes needed.

Projects can also carry their own prompts: any `*-review.md` found in the project tree (the directory the loop runs against) is discovered too, shown as `[project]` in `--list`, `--dry-run`, and run logs, and usable with `--reviews`. A project-local prompt overrides a bundled one with the same name. Vendored/build directories (`node_modules`, `vendor`, `dist`, `target`, `.git`, ...) are skipped.

**Security note:** the loop runs AI tools with permission prompts disabled against the target codebase, and project-local prompts are fed to them verbatim. Only run it against repositories you trust — a malicious repo could steer the AI tools through crafted file content or bundled prompt files.

## Prompt structure

All review prompts follow a consistent structure:

1. **Role and goal** — who the reviewer is and what they evaluate.
2. **Numbered checklist** (typically 10 sections) — specific items to check, grouped by concern.
3. **Instructions** — how to approach the review: priorities, distinctions, scope.
4. **Finding template** — fields for each finding. Most prompts share Title, Severity, Category, Location, Confidence, Why, Evidence, Recommendation, Expected benefit and Estimated effort; individual reviews drop fields that do not apply and add domain-specific ones (WCAG criterion, ladder rung, nondeterminism introduced).
5. **Output format** — structured report sections.
6. **Important** — constraints and ground rules.

## Tests

```sh
./test_review_loop.py        # or: pytest
```

Stdlib only, no framework required. Covers duration and agent parsing (with a
fuzz pass), the exact argv built for every agent including its permission-bypass
flags, prompt discovery (bundled-vs-project precedence, skipped directories,
symlink rejection, duplicate handling), `sanitize`, and `doctor` output in both
plain and colored modes.

## License

Copyright (C) 2026 Marcel W. Wysocki.

GNU Affero General Public License v3.0 or later (`AGPL-3.0-or-later`). See
[LICENSE](LICENSE).

Version numbers are informational: `--version` reports the `VERSION` constant in
`review-loop.py`, which is bumped by hand. Review names (the `*-review.md` stems
consumed by `--reviews`) are the user-facing contract; renaming or removing one
breaks existing invocations.
