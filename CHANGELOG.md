# Changelog

Notable changes per release.

The consumer contract is review names (`*-review.md` stems consumed by
`--reviews`), set names (`quick`, `standard`, ...), CLI flags, and exit
codes. Renaming or removing a review or set name is a breaking change.
While the project is 0.x, other behavior changes may land in a minor;
they are listed under Changed.

## Unreleased

### Added

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
