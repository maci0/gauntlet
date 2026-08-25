# Changelog

## 1.0.0

Rewritten in Go, shipped as one static binary. The Python implementation
(0.26.0 and earlier) is replaced; prompts, injected rules, containment, and
exit codes are unchanged, so existing invocations keep working.

Added:

- Parallel reviews with git-level isolation: `--jobs N` gives every review its
  own worktree and branch, then merges them back one at a time. A conflicting
  merge keeps its branch instead of dropping the work.
- `--dirs` reviews several repositories at once, each with its own lock,
  baseline, and lane.
- `--tui`, a live dashboard: per-agent lanes, activity chart, the full review
  grid, and a normalized feed.
- Output normalization: escapes, spinners, repainted progress lines, tool
  gutters, and duplicate narration collapse instead of scrolling past. Diffs
  are colored by sign.
- Live token throughput, read from what agents already report: their streams,
  their own session transcripts, and their machine-readable modes (`--stream`,
  on by default where an agent has one). Reasoning tokens are tracked and shown
  separately. Agents that report nothing show no rate rather than a zero.
- A run journal under `~/.gauntlet`, with `gauntlet runs` and `gauntlet show`.
- `gauntlet update`: verified self-update, plus hot reload that finishes the
  reviews in flight, hands over the unfinished part of the loop, and re-execs
  without losing counters.
- Agents can be defined rather than compiled in (`--agent-cmd`,
  `~/.gauntlet/agents.json`), including where they keep transcripts. The pi
  family (`pi`, `prime-agent`, `feynman`, `omp`) ships as such definitions.
- `agentusage`, a public package exposing the token reading, so other tools can
  report the same numbers.

Changed:

- Linux and macOS only. The runner depends on process groups, `flock`,
  `O_NOFOLLOW`, and `execve`; Windows has no equivalent that keeps those
  guarantees.
- `--target-dirs` is now `--dirs`, with the old name kept as an alias.


Notable changes per release.

## 0.26.0

### Added

- `--runtime DUR`: wall-clock budget for the entire run (e.g. `8h`, `30m`,
  `2d`). When the budget is reached, the current review and its commit/push
  step finish before stopping. 0 (default) means unlimited.
- `--target-dirs DIR [DIR ...]`: run a parallel review loop per directory,
  each with its own lock, git baseline, and stats. Output is prefixed with
  the directory name. Shell globs are expanded (quoted or not). Conflicts
  with `--dir`.
- `--tui`: experimental curses dashboard with live review status, stats
  gauges, and scrollable agent output. No dependencies (stdlib curses).
- Short flags: `-c` (commit), `-p` (push), `-s` (suggest), `-1` (once).
- `--raw`: echo agent output verbatim. Default is now normalized: ANSI
  escapes stripped, spinner frames dropped, repeated progress lines
  collapsed. Different agents (claude, gemini, kimi, codex, etc.) have
  wildly different verbosity; normalization gives a consistent signal.

### Changed

- `--push` now silently implies `--commit` (no warning when both given).
- Removed deprecated `--models` alias (use `--agents`).
- `--quiet-agents` long form removed; use `-q` or `--quiet`.

## 0.25.0

### Added

- `--suggest-agent AGENT`: use a specific agent for the suggest triage
  step, independent of `--agents` which governs the reviews themselves.
  When set, only this agent is tried; default: sample from `--agents`.
- `--suggest-timeout DUR`: timeout for the suggest step (default 30m),
  independent of `--timeout` which governs per-review execution.

### Changed

- `--list` now silently takes precedence over `--suggest`, `--reviews
  suggest`, `--commit`, and `--push` instead of erroring. Compose flags
  freely; `--list` always wins.
- Suggest timeout default raised from 5m to 30m.

## 0.24.0

### Changed

- `specs-review` and `agentrules-review` enforce industry-standard
  PRD/RFC/ADR taxonomy: a PRD is product requirements (what and why), an
  RFC is a proposal for comment (before the decision), an ADR records a
  decision that has been made. A "proposed ADR" or a "decided RFC" is now
  a finding.
- `deps-review` gains license-attribution checks: bundled third-party
  code without NOTICE/ATTRIBUTION, and patent-grant clause awareness
  across transitive dependencies.

### Added

- `AGENTS.md` documents project conventions for agents working on this
  repo.

## 0.23.0

### Changed

- `sec-review` gains post-quantum cryptography checks: harvest-now-
  decrypt-later risk on classical-only asymmetric crypto, crypto agility
  (hardcoded algorithm identifiers with no swap path), and PQ hybrid key
  exchange not negotiated when the library supports it.
- `build-review` gains binary hardening checks: stack canaries, PIE,
  RELRO, FORTIFY_SOURCE, non-executable stack, CFI/shadow stack, and
  sanitizers (ASan/UBSan) not wired into the CI test build.

## 0.22.0

### Added

- End-of-run summary now reports agent wall-clock time (total and average)
  and token usage (output tokens where the agent reports them, session
  totals otherwise) with an approximate tok/s rate. Per-tool breakdown
  includes the rate when multiple agents ran. Token counters are parsed
  best-effort from agent output tails (Claude, Gemini, Codex, and any
  tool printing a JSON or text usage summary).

### Changed

- Seven more review prompts sharpened with checks sourced from project
  review files and the ct-recomp decomp playbook:
  `concurrency-review` flags file writes that overwrite in place instead
  of write-to-temp/fsync/rename; `o11y-review` names the MELT framework
  and checks that the four signals connect into one investigative path;
  `config-review` flags a malformed config silently falling back to
  defaults; `sec-review` flags declared capabilities wider than what the
  code exercises; `api-review` flags schema-vs-implementation field
  drift; `agentrules-review` flags agents that can weaken their own
  quality gates; `fuzz-review` flags round-trip harnesses that only
  exercise synthetic inputs.

## 0.21.0

### Changed

- Five review prompts sharpened with checks ported from oxlint-standards:
  `db-review` flags DELETE/UPDATE with no predicate (a builder chain whose
  `where` is optional and absent rewrites the whole table); `error-review`
  names the concrete shapes of a fallback standing in for a failure the
  caller should have seen; `code-review` flags branch bodies that are
  identical; `slop-review` flags scratch and debug residue shipped as
  source; `lint-review` distinguishes rule categories never enabled from
  ones deliberately disabled, and prefers analyzer builtins over custom
  rules.

## 0.20.0

### Changed

- `code-review` now flags fabricated or discarded type evidence — code that
  makes a value look safer to the compiler than it is (a frequent tell of
  machine-generated code): chained/widen-then assertions, `unknown`/`any`/
  `object` concealing a real contract, ad-hoc `typeof` narrowing instead of
  boundary parsing, reflection over typed access, and unjustified casts.
  Fenced to error-review (boundary validation) and test-review (module
  mocking vs dependency seams); `oxlint` added to its tool list.
- README polish: install/run TL;DR near the top, a feature-highlights grid,
  and a cleaner pipeline diagram.

## 0.19.0

### Security

- A hostile target repo can no longer execute code in the runner process.
  Its `.git/config` could set `core.fsmonitor` (or other config-as-command
  keys) to any program, which git ran during the runner's ordinary
  read-only calls — during discovery (so even `--list`/`--dry-run`), in the
  lines-changed stats before the first agent, and in the commit step. Every
  git call now forces those keys empty and resolves the git binary on a
  cwd-independent PATH.
- `*-review.md` prompt reads are bounded: a multi-GB prompt no longer OOMs
  the runner (1 MiB cap; a single argv over ~128 KiB fails at exec anyway),
  and a planted symlink-to-FIFO no longer hangs the lines-changed stat
  unkillably. (Hardlinks are read normally: a package manager legitimately
  hardlinks the bundled prompts, and an out-of-tree hardlink in an
  untrusted repo is the trust model's container case.)

### Added

- `--show-prompt REVIEW` prints the exact composed prompt an agent would
  receive (after stripping and the auto-fix suffix; honors `--yolo` and
  `--timeout`), then exits — for debugging project-local prompts.
- `-V` as a short alias for `--version`.
- Lines-changed stats now count new (untracked) files and attribute a
  review that reverts another's lines as deletions, so the final summary
  cannot contradict the worktree.
- `--exclude` is applied before `--reviews suggest`: excluded reviews never
  reach the triage agent or the confirmation list. A set that matches
  nothing under `--exclude` (e.g. `--exclude project` in a repo with no
  project prompts) is a valid no-op.
- Commit-step outcomes are counted in the exit summary; a failed
  `--commit`/`--push` step now exits 1 (changes are stranded), and suggest
  falls back to the next agent when one exits 0 with no usable output.

### Changed

- `code-review`'s doctor tools gain `cargo-clippy`.
- A crashing `semcode-index` now exits 1 (a runtime failure after the lock),
  not 2 (usage error).
- Project-prompt discovery skips hidden directories and anything git ignores,
  and duplicate-prompt warnings fire only when the copies actually differ.
- Numerous doc corrections (the `--agents`/`--continue-sessions` option
  rows, exit-code table, "adding a review" registration requirements).
- Prompt fencing tightened: `error-review` hands off overflow/time/encoding
  to numerics/time/unicode-review; the map-used-as-cache is owned entirely
  by `cache-review`; `sec-review` gains the homoglyph and insecure-randomness
  items that unicode/numerics-review fence to it.

## 0.18.0

### Added

- Logo: two facing rows of chevrons with the code's path arrowing
  through the corridor (running the gauntlet, literally). SVG mark plus
  light/dark wordmark variants in `assets/`, shown in the README via a
  theme-aware `picture` element.
- Static-analysis posture: ruff and mypy configured in `pyproject.toml`
  and enforced in CI (pinned `ruff==0.16.1`, `mypy==1.19.0`). Ruff runs
  E/W/F/B/UP/SIM/RUF/PLE/PLW at line-length 100 with E501 and SIM105
  deferred with written reasons; mypy checks `cli.py` with strict
  equality and untyped-def checking. Fixes the tools surfaced:
  `NoReturn` on `usage_error`, two Optional-narrowing defects, a
  shadowed loop variable, and assorted cleanups.
- `container-review` now flags process state that breaks horizontal
  scaling (sessions, caches-as-truth, job progress in process memory or
  on pod-local disk), the twelve-factor stateless-process property.

### Changed

- README: centered hero with the logo and nav links, a real
  sample-session transcript, agent notes folded into a details block,
  `uv tool install` documented, the options table split into four
  task-oriented groups, exit codes as a table, and the trust model as a
  warning callout.

## 0.17.0

### Added

- Standard Python project layout: `src/gauntlet/` package with `cli.py`
  and the prompts as package data, `tests/`, and a PEP 621 `pyproject.toml`
  (hatchling). `uv tool install` / `pipx install` yield a `gauntlet`
  command; `python -m gauntlet` and running `src/gauntlet/cli.py` directly
  keep working, with zero runtime dependencies. The PyPI-style project
  name is `gauntlet-review` (bare `gauntlet` is taken); the command stays
  `gauntlet`. `VERSION` in `cli.py` remains the single source of truth
  (hatchling reads it from there).
- `## Install` section in the README: clone + symlink onto `PATH`,
  run-in-place, or release tarball.
- Release workflow: pushing a `v*` tag now auto-creates the GitHub
  release with that version's CHANGELOG section as notes. Releases had
  stalled at v0.11.0 while tags kept moving; v0.12.0 through v0.16.0 were
  backfilled by hand.

## 0.16.0

### Changed

- Project renamed to **gauntlet** (repo `maci0/gauntlet`; old
  `review-prompts` URLs redirect). `review-loop.py` is now `gauntlet.py`,
  the test file `test_gauntlet.py`, the lockfile `.gauntlet.lock`, and
  `--version` reports `gauntlet`. The `review-loop: keep` code marker is
  deliberately unchanged: it already lives in target repos' code, and
  renaming it would silently invalidate existing markers.
- README: the 50 reviews are grouped into ten collapsible domain
  categories; badges (CI, release, license, Python, zero dependencies)
  and a mermaid diagram of the review pass added.

### Fixed

- CI: the `--commit`/`--push` warning test no longer depends on agent
  CLIs being installed on the runner, and the three git `subprocess.run`
  calls pass an explicit `check=False` (ruff PLW1510). First green CI
  since 0.13.0.

## 0.15.0

### Added

- `--suggest` flag as shorthand for `--reviews suggest`; conflicts with
  `--reviews` and inherits the suggest-mode guards (`--list`/`--dry-run`
  rejected).
- `code-review` now flags shell scripts that embed another language via
  heredocs or `-c` one-liners (`python -c`, `node -e`, inline awk/perl):
  the embedded code escapes syntax checking, linting, and editor tooling.
  One language per file; call a proper script instead.

### Changed

- README intro updated to the current 50-review count and to describe the
  `--commit`/`--push` commit step alongside "review agents never commit".

## 0.14.0

### Added

- Nine new bundled reviews, each with an applicability gate and single-owner
  fencing against its neighbors:
  - `compat-review`: cross-platform portability. Paths and filesystem
    assumptions, bashisms and GNU-vs-BSD tool drift, line endings,
    endianness and word-size assumptions, glibc/musl variants, and drift
    between the claimed support matrix (README, CI, packaging) and what CI
    actually tests.
  - `time-review`: time correctness. Naive vs aware datetimes, DST and
    calendar arithmetic ("+24h means tomorrow"), wall vs monotonic clock
    choice, mixed epoch units, ambiguous parsing, cron timezones, and
    range/expiry boundary defects.
  - `numerics-review`: numeric correctness. Money in binary floats,
    rounding-mode and allocation errors, silent overflow and truncating
    casts, division and negative-modulo surprises, unit mismatches, and
    precision loss across serialization boundaries (2^53 IDs in JS).
  - `authz-review`: the authorization matrix in depth. Object-level misses
    (IDOR), function-level gaps, tenant isolation, privilege-escalation
    paths, enforcement consistency across duplicate API surfaces, indirect
    access via search/export/webhooks, and deny-side test coverage.
    sec-review keeps authn and point vulnerabilities.
  - `cache-review`: caching correctness. Invalidation completeness at
    write paths, key design (collisions, missing tenant/locale dimensions),
    staleness policy, stampede/dogpile behavior, cross-layer coherence,
    bounds, and sensitive data in shared caches. perf-review keeps
    whether to cache.
  - `resource-review`: resource lifecycle. Descriptor/socket/connection
    leaks, pool drains, zombie processes, leaked goroutines/tasks,
    listener and timer accumulation, unbounded in-memory growth, and
    lock/lease release paths. error-review keeps the error-path slice.
  - `dr-review`: durability and disaster recovery. State inventory vs
    backup coverage, restore reality (tested, loadable, orderable),
    ack-before-durable write windows, failure-domain concentration
    (backups deletable by the same credential), rollback of bad deploys,
    and RPO/RTO plus runbook existence. Takes over db-review's
    operational edges.
  - `unicode-review`: text encoding correctness. Encoding boundaries,
    NFC/NFD normalization policy, byte/code-point/grapheme length and
    truncation defects, case folding, confusables and invisible characters
    in identifiers, and lossy round-trips. i18n-review keeps locale,
    translation, and RTL.
  - `dx-review`: contributor experience. Clone-to-green-test bootstrap,
    edit-test loop speed and single-test paths, command discoverability,
    local/CI parity, contribution mechanics, and dev-path error messages.
    doc-review keeps prose accuracy; this owns the runnable path.
- Set updates: `standard` gains compat, time, numerics, resource;
  `security` gains authz; `backend` gains authz, cache, dr; `frontend`
  gains unicode; `shipping` gains dx.

### Changed

- Reciprocal fencing lines added to sec-review (authz depth), perf-review
  (cache correctness, resource lifecycle), error-review (resource
  lifecycle), i18n-review (encoding mechanics), db-review (recovery
  posture), and doc-review (runnable onboarding path), keeping every
  concern single-owner.

## 0.13.0

### Added

- Lines-changed stats: in a git repository, each review, each completed
  loop, and the final exit summary now report `+insertions/-deletions`,
  measured via `git diff --shortstat` against the commit that was `HEAD`
  when the run started. Silently omitted outside a git repo.

### Changed

- A warning is now printed when `--commit` and `--push` are both given,
  since `--push` already implies `--commit`.
- Project-local prompt discovery now skips hidden directories (`.git`,
  `.venv`, worktrees, etc.), so stray prompt copies under them (e.g. a
  `.clanker-worktrees` snapshot) no longer trigger duplicate-prompt
  warnings.

### Fixed

- `container-review` (added in 0.12.0) was missing from `doctor`'s tool
  table and from the `shipping` review set; both are corrected.

## 0.12.0

### Added

- `container-review` audits container-native readiness of Kubernetes
  workloads declared in Deployment/StatefulSet/DaemonSet manifests, Helm
  charts, and Kustomize overlays. Covers health probes (liveness, readiness,
  startup — including dependency checks in the readiness probe), graceful
  shutdown (SIGTERM handling, shell-form CMD, preStop hook,
  terminationGracePeriodSeconds), observability wiring (ServiceMonitor/
  PodMonitor presence, stdout log delivery, OTel annotation injection),
  security context (non-root, capabilities drop, seccomp, read-only root
  filesystem, fsGroup), configuration management (externalized config,
  no inline secrets), image hygiene (digest pinning, .dockerignore,
  multi-stage), resource requests/limits, resilience (PDB, anti-affinity,
  rolling update strategy, retry over init-container polling), networking
  (NetworkPolicy deny-all baseline, no hostNetwork/hostPort), and Kubernetes
  object hygiene (standard labels, dedicated ServiceAccount, RBAC scoping).
  Scoped to k8s-manifest-side concerns; infra-review owns CI/CD and
  compose wiring, o11y-review owns application instrumentation depth.

- `--commit` flag: after each review, an agent inspects the diff, writes a
  human-style commit message (no AI attribution), and commits any changes.
  Skipped when the working tree is clean.

- `--push` flag: like `--commit` but also pushes after committing. Both flags
  may be combined; the effect is the same as `--push` alone. When `--push`
  is combined with `--yolo`, the agent is also instructed to rebase and retry
  if the push is rejected due to a diverged remote.

## 0.11.0

### Changed

- Review checklists absorb TigerBeetle Tiger Style
  (https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/TIGER_STYLE.md),
  distributed into the reviews that already own each concern:
  assertion density and pairing, bounds on loops and queues,
  explicitly-sized types, ~70-line functions, push-ifs-up,
  batching and resource-order sketches, static allocation,
  programmer-vs-operating errors, exhaustive valid/invalid tests,
  zero-dependency default, strict compiler warnings and ~100-column
  measure, named arguments, and why-comments. No new review name.

## 0.10.0

### Added

- `lint-review` owns the static-analysis posture: tool coverage per
  language, configuration strictness, suppression hygiene (stale and
  unjustified ignores), typing coverage, and blocking CI enforcement. The
  analyzers' findings stay with their owning reviews (code-review, sec-review);
  this one reviews the analyzers themselves. Joins `standard`.

The consumer contract is review names (`*-review.md` stems consumed by
`--reviews`), set names (`quick`, `standard`, ...), CLI flags, and exit
codes. Renaming or removing a review or set name is a breaking change.
While the project is 0.x, other behavior changes may land in a minor;
they are listed under Changed.

## 0.9.0

### Added

- `threat-review` builds and maintains a living threat model in the repo
  (`docs/THREAT_MODEL.md` plus `SECURITY.md` accuracy): attack-surface
  inventory, trust boundaries, assets, STRIDE-style threats per boundary,
  mitigations mapped to code, and abuse cases with code-level evidence. It
  writes security documentation only: vulnerabilities are recorded and
  handed to sec-review, never fixed or demonstrated here. Joins `security`.

- `specs-review` covers PRDs, ADRs, RFCs, and design docs as documents:
  drift against the implementation, ADR structure and lifecycle,
  requirement testability, traceability, and redundancy across documents
  (duplicates consolidate to one canonical copy). Decision substance stays
  with design-review, general doc prose with doc-review. Joins `standard`.
- `dsh` (DeepSeek Harness) as a supported agent, run as
  `dsh --profile headless <prompt>`. Permissions and the default model come
  from the profile's config; `dsh:<model>` pins a configured model through
  a generated `--patch` overlay (the profile's provider is probed once from
  `--dump-config`; `dsh:<provider>/<model>` sets both explicitly, since the
  overlay replaces the plugin config and the provider is required). One-shot
  mode has no resume, so
  `--continue-sessions` starts it fresh. When the launcher is not in `PATH`
  but `bunx` is, a named `--agents dsh` falls back to `bunx @deepseek-ai/dsh`
  (auto-detect and `mixed` stay PATH-based because the fallback fetches the
  package on first use).
- `vnu` (offline W3C HTML/CSS validation) as a suggested tool for
  `ux-review` and `a11y-review`; `htmlhint` and `stylelint` for `ux-review`.
- `-y, --yes` skips the `--reviews suggest` confirmation without enabling
  `--yolo`. Implied when stdin is not a terminal.
- `--quiet` is an explicit alias of `--quiet-agents`.

### Deprecated

- `--models` now warns on stderr; use `--agents`. The alias still works.

### Fixed

- `--reviews suggest` interpolated project review descriptions through
  `str.format`, so a `{placeholder}` in a "Your goal" line crashed triage
  (or, with a matching name, rewrote the template). Descriptions are now
  spliced in as data, fenced, truncated, and stripped of `RELEVANT:` markers.
- `--continue-sessions` resumed via `-c` / `--resume latest` even when two
  models of the same CLI were in the pool, mixing their sessions. Resume
  now requires that CLI to appear only once.
- A typo in `--reviews`/`--exclude` on a real run was reported as "another
  instance is running" (exit 75) when a lock was held, because names were
  validated after lock acquisition.
- `--reviews ''` (and `--reviews "$UNSET"`) ran every review. An explicit
  empty list is now a usage error.
- `--dir`, `--prompt-dir`, and `--log` now expand `~` and `$VAR`, matching
  `--bin`. A `--prompt-dir` that exists but is not a directory says so.
- `--bin` unknown-agent errors now include a "did you mean" hint.
- Agent names in `--agents`/`--bin` are case-insensitive (`CLAUDE` = `claude`).
- OSError messages no longer leak `[Errno N]`.
- `--semcode` without `semcode-index` is now a usage error before the lock
  is taken, so a held lock reports the missing tool (exit 2) instead of
  "another instance is running" (75). `--dry-run --semcode` warns when the
  indexer is missing.
- `--yolo` is printed on `--dry-run` and logged at the start of a real run.
- SIGTERM during `--reviews suggest` or `--semcode` no longer orphans the
  `start_new_session` child (a permission-bypassed agent, or an indexer
  still writing `.semcode.db` after the lock is released). Both paths now
  kill the process group and reap with a timeout, matching the review loop.

### Changed

- Long-option prefixes are no longer accepted (`--dry` is not `--dry-run`).
  Spell the flag in full; short forms (`-q`, `-l`, ...) are unchanged.
- Review bodies are wrapped in `BEGIN/END REVIEW` markers so a project-local
  prompt cannot blend into the containment suffix.
- Suggest triage is capped at 5 minutes (`SUGGEST_TIMEOUT_CAP`) and falls
  back to the next `--agents` entry if the first launch or run fails.
- A review that fails (launch or non-zero exit, not timeout) is retried on
  another agent from `--agents` when one remains, so a single provider
  outage does not burn the slot.

## 0.8.0

### Changed

- `--reviews suggest` with `--yolo` skips the confirmation prompt; the picked
  reviews and their reasons are still printed.

## 0.7.0

### Added

- `--reviews suggest`: one agent from `--agents` inspects the repo against
  the review catalog (names and descriptions only, never prompt bodies, under
  a classification-only rule), lists the relevant reviews with a reason each,
  asks for confirmation on a terminal (non-interactive runs proceed), then
  the loop runs exactly those. Composable with `--exclude`, not with other
  `--reviews` values or `--list`/`--dry-run`.

### Changed

- Review sets now cover every bundled prompt (enforced by a test):
  `design-review` joins `standard`, `mobile-review` joins `frontend`, and
  `sdk-review` and `infra-review` join `shipping`.

## 0.6.0

### Added

- Short flags for the common options: `-a` (`--agents`), `-r` (`--reviews`),
  `-x` (`--exclude`), `-t` (`--timeout`), `-n` (`--max-loops`), `-C` (`--dir`),
  `-q` (`--quiet-agents`), `-l` (`--list`).
- The `-review` suffix may be omitted in `--reviews`/`--exclude`: `sec` means
  `sec-review`. Sets and exact names take precedence over the expansion.
- Unknown review, set, and agent names now come with a "did you mean" hint.

### Changed

- `--agents`, `--reviews`, and `--exclude` are repeatable, matching `--bin`:
  `--reviews sec-review --reviews code-review` merges like one comma list
  (repeats still count as weight for `--reviews`).
- `--help` groups the options (modes, review selection, agent selection,
  execution, output) and uses descriptive metavars (`DURATION`, `N`, `FILE`,
  `LIST`).
- `doctor`, `--list`, and `--dry-run` now reject being combined instead of
  silently picking one.
- `--log` works in every mode; it was silently ignored with `doctor`,
  `--list`, and `--dry-run`. A relative FILE is now resolved against the
  invocation directory (like `--prompt-dir`), not the `--dir` target.
- `--help` documents the exit codes.

## 0.5.0

### Changed

- `--reviews` treats repetition as weight: naming a review or a set more than
  once schedules it that many times per loop, so `all,sec-review,sec-review`
  runs everything once and security three times. `--list` shows weighted
  reviews as `×N` and `--exclude` still removes a name outright. Repeated
  names previously collapsed, so `--reviews quick,code-review` now schedules
  code-review twice rather than once.

## 0.4.0

### Added

- clanker as a supported agent (`clanker run <prompt>`). Its model and
  permissions come from its own config, and resuming requires an explicit
  session id, so it takes no `tool:model` form and is not eligible for
  `--continue-sessions`. It loads config from the working directory only, so
  it can review just the repository holding that config, and is therefore
  opt-in: auto-detect and `mixed` skip it, and it must be named explicitly.
- `webperf-review` covers web delivery and first paint: compression
  negotiation and effort matched to cacheability, the critical path and the
  initial congestion window, loading strategy and split-chunk failure, caching
  and revalidation headers, payload shape, main-thread responsiveness,
  third-party weight, protocol-era workarounds, measurement, and budgets in
  CI. perf-review keeps server and runtime performance and now defers browser
  delivery to it; the `frontend` set swaps perf for webperf.
- `--yolo` swaps the caution half of the injected rules for an ambitious one:
  no fix count or diff-size cap, public APIs and structure may change, and an
  agent should build missing groundwork rather than decline the work.
  Containment, the ban on touching your uncommitted changes, the
  `review-loop: keep` marker, and the baseline-then-verify step are unchanged.
  It also removes deference: no waiting for sign-off, report-only instructions
  in a review body are superseded, and a broken build or plain bug is in scope
  even when unrelated to the review's topic.
- `--bin TOOL=PATH` runs an agent from a chosen executable instead of `PATH`,
  for wrappers and alternate builds. Repeatable, one per agent, with `~` and
  `$VAR` expanded because shells leave them alone after `=`.

### Fixed

- Ctrl+C could stop working entirely during a review. Agents inherited the
  runner's stdin, and an agent that puts the terminal in raw mode with ISIG
  disabled (clanker does this for line editing) suppresses signal generation
  for itself and for the runner. Agents now run with stdin closed, and the
  runner restores terminal settings after every review.

## 0.3.0

### Added

- opencode as a supported agent (`opencode run --auto`), bringing the total to
  nine: claude, gemini, qwen, codex, grok, agy, cursor-agent, kimi, opencode.

### Fixed

- `--continue-sessions` placed its resume flag next to the binary, which is
  wrong for agents invoked as `binary subcommand ...`; the flag now follows the
  subcommand. No shipped agent was affected, but opencode would have been.

## 0.2.0

### Added

- Four reviews for repos that ship agent instructions or need re-execution
  safety: `prompt-review` (review prompts as agent instructions),
  `skills-review` (SKILL.md packages), `agentrules-review`
  (CLAUDE.md/AGENTS.md/.cursorrules), and `idempotency-review` (retries,
  at-least-once delivery, reruns, crash recovery). 32 prompts to 36.
- Review sets for `--reviews`/`--exclude`: `all`, `project`, `quick`,
  `standard`, `security`, `frontend`, `backend`, `agents`, `shipping`. They
  compose with plain review names, and `--list` prints their members.
- Kimi Code (`kimi`) as a supported agent.
- `--quiet-agents` discards agent stdout/stderr for agents that narrate every
  step.
- `--continue-sessions` resumes each agent's own session after its first run so
  already-read context is reused. Off by default: contexts bleed between
  reviews and history is resent every turn.
- `--semcode` builds a semcode index of the target before the loop so reviews
  answer call-graph and type queries from the index.
- SBOM, provenance, and registry-attack coverage in `deps-review`
  (dependency confusion, typosquats, hash pinning, `syft`/`grype`/`cosign`).

### Changed

- The injected rule suffix is rewritten around containment: repo content is
  data rather than instructions, git is read-only, no installs or writes
  outside the tree, nothing may outlive the run, and agents must undo bad
  edits by re-editing rather than by git revert (the tree may hold your
  uncommitted work). Agents now learn their wall-clock budget and end with a
  machine-readable `RESULT:` line. A `review-loop: keep` comment marks code
  every agent must leave alone.
- Prompts are composed for auto-fix at dispatch: report-only sections are
  stripped, which drops roughly a third of each prompt that the suffix
  overrode anyway. The `.md` files stay complete for standalone use.
- Reviews fence each other explicitly, so two agents no longer fix the same
  code by different rules.

### Fixed

- `kimi` was invoked with `--auto -p`, which its prompt mode rejects; every
  kimi review failed instantly.
- Prompt files are opened `O_NOFOLLOW` and non-blocking, closing a symlink
  swap window and a FIFO that could hang discovery forever.
- The timeout and signal paths kill the agent before logging, so a dead `tee`
  can no longer leave a permission-bypassed agent running orphaned.
- A signal during a review now shortens the wait to ten seconds instead of
  blocking for the full timeout against an agent that traps SIGTERM.
- Section stripping fails open: a prompt whose report marker is never closed
  keeps its text instead of being truncated to the marker.

## 0.1.0

First tagged release: 32 review prompts, the runner with `doctor`, project-local
prompt discovery, the flock lockfile and exit-code contract, tests, and CI.
