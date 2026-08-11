# Changelog

Notable changes per release. Review names (the `*-review.md` stems consumed by
`--reviews`) are the user-facing contract: renaming or removing one is a
breaking change.

## Unreleased

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
