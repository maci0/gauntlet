# Changelog

Notable changes per release. Versions follow SemVer against this consumer
contract: review names (`*-review.md` stems consumed by `--reviews`), set
names (`quick`, `standard`, ...), CLI flags and their documented behavior,
the documented environment variables, exit codes, and every non-internal Go
package in this module another program can import (the Go
import-compatibility rule). Removing or renaming any of these is breaking
and waits for a major version; new flags and other additions may land in a
minor. While the project was 0.x, other behavior changes could land in a
minor instead and were listed under Changed.

## Unreleased

### Security

- Guard git rev-parse, log, and diff commands with `--end-of-options` to prevent option injection and arbitrary file write via option-shaped ref arguments, and separate revisions with `--` during hard resets.
- Resolve relative executable paths containing path separators to absolute paths in `runx.LookPath`, preventing unintended binary lookup or execution from working directory changes.
- Use `os.Lstat` and regular-file checks when inspecting prompt files during discovery, refusing symlinks and special files.

### Fixed

- Reject `--once` combined with `--max-loops 0` as conflicting loop limit options in the CLI.
- Restrict git status C-style unquoting to valid byte-range octal escapes (000..377) to prevent truncating integer overflow, guard retry backoff calculation against negative shift amounts on negative attempts, and ignore writes to uninitialized or non-positive tail buffers.
- Reset feed scroll to live output before quitting on Esc in the completed dashboard, quit immediately on Ctrl+C when a run has finished, show in-flight reviews in the minimal dashboard view, and step into the first review when expanding an already-open group in the launcher.
- Floor live activity rate marker to teal to clear the text contrast floor on low rates, support delete and ctrl+h in launcher filter input, and add accessible SVG title and desc metadata.
- Avoid counting interrupted reviews as failures in dashboard lane statistics.
- Validate ISO 8601 calendar dates and leap years with standard time parsing when resolving journal run shards, guard thinking glyph animation against uninitialized clocks and backward time steps, and avoid undefined Unix time calculations on zero timestamps in seed derivation.

- Measure footer key width in terminal cells in the minimal view, terminate ANSI CSI escape tokens on standard final characters in token width calculation, and normalize lock notes, prompt summaries, and suggestion reasons to NFC before truncation.
- Fall back to default ssh when `GIT_SSH_COMMAND` is empty or whitespace, avoid parsing custom agent definitions for non-agent subcommands, and document motion reduction environment variables in example configuration.
- Reject empty arguments and single dashes as unexpected positional arguments instead of silently swallowing them or misreporting them as unknown commands during subcommand peeling in the CLI.
- Avoid re-publishing pull request events and double-counting line changes when resuming stacked pull requests, make branch renames idempotent when target matches source, and guard review history changed counts against duplicate merge and pull request events.
- Skip review confirmation prompts during `--list` and `--dry-run` planning, parse stream JSON role and type values case-insensitively, ignore empty queries and candidates in fuzzy matching, and remove nonexistent review names from heuristic rules.
- Guard against integer overflow and truncation in backoff jitter and token reasoning percentages, and protect dashboard meters, rate formatters, and elapsed durations against NaN and unrepresentable floats.
- Guard dashboard chart rendering and note truncation against non-positive bounds, and handle nil receivers in usage tails and release metadata.
- Bound narrow text trimming and directory labels in the dashboard to non-negative widths, avoid splitting multibyte arguments in attached flag values, and normalize stacked pull request overview notes to NFC.
- Correct the review count across the landing page and design docs, and document the dashboard Enter-to-close and paging keys.
- Clamp reload and review elapsed durations to non-negative values, guard git sample caching against negative intervals, and fall back to reconstructed start time when resume origin is zero or in the future.
- Prevent subprocess cancellation in `runx.Guard` from signaling process group 0 when process PID is non-positive.
- Avoid misclassifying canceled reviews as failures and retrying them when cancellation races with process exit in the runner.
- Preserve index append errors alongside prior write errors during journal close instead of discarding them.
- Avoid parsing trailing uninitialized bytes on short index reads in the journal.
- Surface remote error messages from GitHub API responses when release or asset checks fail in self-update.
- Validate placeholder usage and argument conflicts across custom agent definitions, and degrade `GAUNTLET_HOME` directory resolution safely when pointing to a non-directory file.
- Respect standard `NO_MOTION` and `REDUCED_MOTION` environment variables in the dashboard, and announce disabled states for inactive controls in the launcher help view.
- Clarify launcher agent panel and options hints, keep filter prompt visible during zero-match search, and display paused status in the dashboard feed panel title.
- Avoid treating closed signal channels as active signals or force-kill triggers in signal handling, and access stacked-PR runner state through synchronized methods.
- Bound and reclaim subprocess process groups across dsh probing, GitHub CLI calls, and indexing, and drain HTTP response bodies on self-update error paths.
- Correct stacked pull request body documentation and example in `docs/RUNS.md`, document launcher navigation keys in `docs/CLI.md`, and fix misplaced type docstrings.
- Reclaim git subprocess process groups on command exit, guard repository operations against nil instances, and default pull request validation to GitHub when host is unspecified.

## 1.23.2

### Fixed

- Accept release asset downloads from the GitHub release CDN host, fixing self-update checksum fetches redirected there.

## 1.23.1

### Fixed

- Speed up live feed classification by skipping the error-pattern match on lines without error trigrams.

## 1.23.0

### Added

- Support Enter key to close the live dashboard once a run has finished, and support g and G keys for first and last row navigation in the launcher.
- Support Tab and Shift-Tab pane switching, Ctrl-W word deletion, and Ctrl-U line clearing while typing in the launcher review filter (`gauntlet pick`), and automatically focus the first matching review on Enter.
- Support Page Up, Page Down, and Space keys in the live dashboard feed view and help overlay.
- Support Page Up and Page Down keys (`pgup`, `pgdown`) in the interactive launcher (`gauntlet pick`) across reviews, agents, options, and filter search.
- Ship template configuration files `agents.example.json` and `.env.example` with documented options and placeholder values.

### Security

- Require reload handoff state file paths via `GAUNTLET_STATE` to be absolute, preventing relative path resolution and deletion in the working tree.
- Validate provider and model identifiers in dsh configuration overlays, preventing YAML injection and directory traversal.
- Validate reload handoff state files before reading or removing, refusing non-regular files and symlinks via `GAUNTLET_STATE`.
- Reject oversized responses in self-update checksum downloads instead of silently truncating.
- Separate git branch names and patterns with `--` across merge, rename, and branch deletion operations.
- Isolate `--usage-cmd` process execution and PATH resolution from the reviewed
  working tree, running the probe in the system temporary directory with
  cwd-relative PATH entries dropped.
- Constrain self-update asset downloads to HTTPS endpoints on authorized GitHub
  release hosts, preventing plaintext transfers or untrusted third-party hosts.
- Validate HTTP redirect target URLs in self-update against authorized release hosts.
- Strip authorization bearer tokens on self-update requests whenever redirected away from GitHub hosts to prevent token leakage.
- Use constant-time comparison for self-update asset checksum verification against timing side-channels.
- Reject unclean and path-traversal state file paths via `GAUNTLET_STATE`.
- Shell-quote git conflict resolution hint commands with POSIX single-quoting to prevent shell injection via untrusted commit subjects.
- Isolate agent, indexer, and dsh probe execution with absolute-only PATH environments and clean working directories to prevent relative binary resolution.

### Fixed

- Reject explicit empty `--agents`, `--bin`, and `--agent-cmd` flags with a usage error rather than silently ignoring them or falling back to auto-detection.
- List scheduled and available reviews across all target directories under `--list` when multiple directories are configured via `--dirs`, and search all target trees for `--show-prompt`.
- Adapt agent lane column widths for narrower terminals (<90 cols) so metrics are not clipped off in the live dashboard.
- Document the Escape reset shortcut (`esc:live`) in the dashboard footer whenever the feed is paused at the live edge.
- Show `:change` instead of `:open/close` for arrow keys in the launcher footer when focused on the options pane.
- Prevent space and arrow keys from modifying inactive options (suggest agent when suggest is off, merge target when commits are off) in the launcher.
- Explain that no agents are installed when viewing the agents pane hint with an empty agent pool.
- Abort git rebase on pull conflicts to avoid leaving repositories in an uncleaned mid-rebase state.
- Fall back to subsequent agents in the pool when command building fails for an agent candidate.
- Preserve error context when resolving binary paths and checking baseline revisions during trailer stripping.
- Strip trailing carriage returns in git status porcelain parsing, worktree cleanup, and UI block padding to prevent path corruption and rendering issues with CRLF line endings.

- Expand tildes and environment variables in custom agent executable paths at launch, and reject unresolvable variables.
- Validate that GAUNTLET_HOME and --prompt-dir name directories and --log names a file at startup.
- Align documented `GIT_SSH_COMMAND` default in `.env.example` with the runtime default (`ssh`).
- Reject mismatched placeholders across custom agent `model`, `effort`, `stream`, and `continue` configurations.
- Do not count opt-in agents launchable only via bunx (`dsh`) as usable auto-detectable CLIs in the doctor report, correctly reporting missing agents and exiting 1.
- Separate revision arguments and branch names with `--` across git worktree operations, diff statistics, trailer stripping, and commit subject extraction to prevent option injection and file name collision ambiguity.
- Validate JSON key types when decoding custom agent definitions (`agents.json`), returning an error on non-string keys instead of panicking on type assertion.
- Handle incomplete octal escape sequences without consuming invalid digits or malforming bytes in git filename unquoting (`unquoteC`).
- Populate line metrics, review status, and subjects when recovering stacked PR layers.
- Serialize stream sink and token usage callbacks during agent execution, retry interrupted lock note updates on EINTR, and synchronize watcher teardown during runner shutdown.
- Normalize custom agent names and definition keys to NFC, rejecting duplicate keys across NFC and NFD spellings and aligning lookup forms.
- Handle non-positive column budgets and 1-column cuts in terminal cell trimming, reserving width for the ellipsis and returning empty strings on non-positive bounds.
- Use canonical review names when expanding review sets and displaying review prompts, preventing unnormalized names from reaching prompt composition.
- Block the launcher (`gauntlet pick`) from starting an unconstrained run when an active review filter matches no reviews, displaying a clear warning.
- Reject unresolvable environment variable references in `GAUNTLET_HOME` at startup and degrade `gauntlethome.Dir` safely instead of resolving unexpanded paths against the working tree.
- Reject empty argument strings in custom agent `model`, `effort`, `stream`, and `continue` configurations, and reject whitespace-only usage suffixes.
- Normalize available review names to NFC in suggestion parsing, matching decomposed names against agent suggestions.
- Normalize pull request body text to NFC before rune truncation, preserving combining characters on decomposed filenames and descriptions.
- Recognize Unicode whitespace when stripping agent output noise, gutters, and trailing spacing in the line normalizer, and in launcher filter word trimming.
- Distinguish complete Unicode replacement characters from incomplete multi-byte sequences at process output chunk boundaries.
- Surface Escape cancel and live-feed reset keys in the dashboard footer and help overlay, and display active filter queries and clear shortcuts in the narrow launcher fallback.
- Prevent Escape from abruptly terminating an active run when quit is armed in the dashboard; Escape now cancels the quit prompt and resets paused or scrolled feeds to live output.
- Make git worktree removal idempotent on already-removed checkouts, and prune git metadata when the checkout directory has already been deleted.
- Clean orphaned worktree directories and prune stale metadata during worktree preparation, ensuring worktree creation and removal converge across interrupted runs.
- Strip UTF-8 byte-order marks (BOM) when loading custom agent definitions (`agents.json`), preventing parse errors on Windows-formatted files.
- Pad clipped lines with trailing spaces in dashboard panel formatting when multi-column wide characters are truncated, preventing misaligned panel borders.
- Count Unicode code points instead of bytes when checking for short-flag misses, preventing single non-ASCII flags from triggering typo suggestions.
- Normalize file paths to NFC when correlating file notes to git changes in stacked PR summaries and commit subjects, matching decomposed macOS filenames with NFC text.
- Strip relative build directory paths from release SBOM inventory headers to match asset filenames, and clean scratch files and stray binaries on make clean.
- Prevent auto-update from repeatedly re-applying the already-installed release tag during an active run.
- Unlock git worktrees before removal during merge cleanup, preventing leftover locked worktrees on failure.
- Format branch listings cleanly when deleting matching review branches.
- Derive journal date shards from the run ID timestamp instead of the local clock so midnight UTC crossings place journals in the matching shard, and validate run IDs on open.
- Synchronize stacked PR head and publication state with the runner mutex, guard worktree branch renaming, and force-kill stalled command groups on drain timeout.
- Check write errors on command output streams across subcommands (`gauntlet doctor`, `gauntlet runs`, `gauntlet show`, and `gauntlet pick`), exiting 1 on failure instead of reporting success.
- Propagate cancellation exit code 130 when the interactive launcher or review planning is interrupted by context cancellation.
- Record loop line changes in parallel worktree mode (`--jobs > 1`) and preserve line metrics on pull request events in stacked-PR mode (`--stacked-prs`) across the journal, history, and dashboard.
- Correct documentation in CLI and runs reference for `--usage-cmd` execution directory, custom agent definition fields and validation rules, and missing `--check` and `--limit` option tables.
- Reject empty string arguments for `--show-prompt`, `--merge-into`, `--pr-base`, `--suggest-agent`, `--usage-cmd`, and `--exclude` at startup instead of silently accepting them.
- Enforce placeholder validation on custom agent definitions (`model` requires `{model}`, `effort` requires `{effort}`, and forbid `{prompt}` in `stream` or `continue`).
- Abort and reset in-progress merges cleanly even when interrupted by a cancelled context, preventing unmerged index state from persisting.
- Preserve the underlying error on retry failure when removing git worktrees.
- Include unmerged-path inspection error context when squashing conflicted branches.
- Warn on snapshot worktree cleanup failures and guard against nil repository references.
- Abort prompt discovery directory traversal early when context is cancelled.
- Parse duration flags with 64-bit integer precision so values above 2^31-1
  nanoseconds parse without overflow on 32-bit platforms.
- Format missing run start timestamps as `n/a` instead of `0001-01-01` in
  `gauntlet runs`.
- Generated commit subjects clip at whole grapheme boundaries within the 72-rune
  limit, preserving combining accents, flags, and emoji sequences.
- Filenames in generated commit subjects strip C1 controls, bidi overrides,
  zero-width spaces, and Unicode line breaks, preventing terminal spoofing.
- Display text sanitization strips Unicode line and paragraph separators (U+2028
  and U+2029), preserving line integrity in output feeds and summaries.
- Distinguish internal prompt read failures from bad review arguments in
  `--show-prompt`, exiting 1 on I/O error instead of 2.
- Distinguish interactive launcher runtime errors from usage errors in
  `gauntlet pick`, exiting 1 on TUI failure instead of 2.
- Exit 1 on directory lock acquisition errors other than existing locks instead
  of reporting them as usage errors.
- Ensure flag-requested help is rendered to stdout when `flag.ErrHelp` is returned
  during parsing.
- Reject empty model specifications when a colon delimiter is provided in agent
  specifications, and reject colons for tools that do not support models.
- Validate custom agent definitions to require exactly one `{prompt}` placeholder
  in `argv`, forbid `{prompt}` inside `model` or `effort` options, and reject
  empty directory entries in `usage.roots`.
- Expand leading `~` and environment variables in `GAUNTLET_HOME`, and treat
  whitespace-only values as unset.
- Recognize boolean false values (`false`, `no`, `off`, `0`) in `GAUNTLET_NO_ANIMATION`,
  `CLICOLOR_FORCE`, and `FORCE_COLOR` instead of treating them as truthy.
- Resolve the defined executable rather than the custom agent name when validating
  and auto-detecting custom agents, allowing custom agents whose names differ from
  their binary to run without "tool not found" errors.
- Correct the installed agent count in `gauntlet doctor` so custom agent binaries,
  override paths, and fallback launchers are counted accurately in the inventory.
- Preserve `--prompt-dir` in the composed command line generated by `gauntlet pick`.

## 1.22.1

### Fixed

- Pass `--add-dir` to `agy` so it knows which directory it is reviewing.
  Without it the model received no workspace context and hallucinated paths
  like `/home/user/repo`, failing every review that tried to list or edit
  files. Especially visible with `--jobs` where the review runs in a
  worktree the CLI has never seen before.

- Kill the agent's process group on the normal exit path, not only on
  timeout and cancel. A grandchild that outlived the agent (a background
  task, a language server) no longer survives as an orphaned process.

- Sweep slash-separated lane branches (`gauntlet/<tag>/lane-*`) on cancel,
  not only dash-separated ones. A cancelled `--jobs` run no longer leaves
  leftover branches behind.

## 1.22.0

### Changed

- Stream parsing retains only text-bearing fields for deferred classification,
  reducing allocations for metadata-heavy output without changing text or usage.

### Fixed

- Empty `SUBJECT:` and `PATH:` fields no longer consume the following output
  line as a commit subject or file note.
- Run-end summaries report the loops the run completed instead of always zero.
- Agent configuration rejects case-variant duplicate fields, including nested
  usage fields, instead of silently letting JSON key order override settings.
- Release concurrency is scoped per tag so unrelated tag pushes cannot cancel
  queued releases. Runs for the same tag remain serialized.
- Launcher help exposes the focused control's full name, state, value, and
  description in scrollable text when terminal panes clip them.
- Stream lines, prompt descriptions, and PR summaries truncate at whole grapheme
  boundaries within their rune budgets, preserving combining accents and flags.
- Changelog validation accepts SemVer prerelease and build-metadata headings,
  so the release checks no longer block release candidates before publication.
  Versions sort by SemVer precedence, and negative version components are refused.
- Narrow launcher panes retain concurrency and selected option values instead
  of hiding them. Panel titles stay within their assigned width so long titles
  cannot push adjacent panels past the terminal edge.
- Deterministic-simulation guidance preserves cryptographic randomness in
  production and confines seeded substitutes to tests or simulation mode.
- Launcher help calls the existing state-summary helper instead of an undefined
  method, restoring compilation.
- Directory lock files keep their inode after release, preventing overlapping
  starts from acquiring separate locks for the same tree. Release clears the
  holder note instead of removing the file.
- Reported subjects and file notes truncate at whole grapheme boundaries within
  their rune limits, preserving combining accents, flags, and variation selectors.
- Release tags with a prerelease suffix are published as GitHub prereleases,
  including when retrying a draft, so stable installs and self-updates do not
  select release candidates. Build metadata alone does not mark a prerelease.
- Run summaries retain interrupted review counts, including summaries rebuilt
  from journal events, instead of dropping that outcome from the status totals.
- Build recipes pin `GOAMD64=v1` and `GOARM64=v8.0`, preventing ambient CPU
  settings from producing binaries that require newer processors.
- Recovered run listings retain the original start time when hot reload or
  multiple directories produce repeated run-start events.
- Persistent review lanes discard staged and unstaged edits when advancing,
  so failed attempts cannot carry tracked changes into the next review.
- Stream parsing no longer treats tool-payload and user-turn text as assistant
  output, so report lines and usage inside a tool result cannot replace the
  run's own subject, file notes, or counters.
- Version output exits with a failure and reports errors on stderr when stdout
  or the `--log` destination cannot be written.
- Output rate limiting starts its first window at the first line, including
  when an injected clock starts near zero time.
- Failed attempts stop retrying or falling back to another agent once the
  runtime budget is exhausted, including when it expires during backoff.
- Seeded review schedules retain their logical loop number after hot reload,
  so subsequent shuffles and `--max-reviews` selections match uninterrupted runs.
- Hot reload counts a sequential loop completed during its final review, so
  the successor does not repeat finished work or exceed `--max-loops`.
- `--show-prompt` exits with a failure and reports output errors on stderr
  when the prompt cannot be written, including partial output.
- Build and test recipes export `GOWORK=off`, so a `go.work` above the
  checkout can no longer add workspace modules or replace directives to the
  build; the dependency set is go.mod's and go.sum's alone.
- Clearing a kept review search in the launcher moves the selection onto the
  first visible review instead of leaving it stranded off the restored list.
- `--log` tightens a pre-existing log file to owner-only permissions before
  writing, instead of leaving permissions from an earlier looser creation.
- `make fmt` handles Go formatter paths containing spaces, matching `make check`.
- Commit steps retain their five-minute timeout when `--timeout 0` leaves
  reviews unlimited, rather than allowing a stalled commit to block the run.
- Custom agent files reject duplicate agent names and configuration keys instead
  of silently replacing earlier values.
- Recovered run listings count completed loops across directories and hot reloads,
  without counting an interrupted loop as finished.
- Git status parsing preserves Unicode whitespace in filenames instead of
  stripping it and reporting a different path.
- The completed dashboard freezes elapsed time, budget consumption, and activity
  history while it remains open for inspection.
- Dashboard throughput sums lanes in stable order so replayed usage events
  produce identical rates, counting repeated configured lanes only once.
- Usage probes kill remaining process-group members on every exit, preventing
  background helpers from accumulating between reviews.
- The help overlays on the dashboard and launcher scroll when the terminal is
  too short for them, so every instruction stays reachable by keyboard, and
  wrap long lines instead of clipping them at the pane edge.
- Suggestion parsing rejects malformed names instead of scheduling a review
  whose name matches only a prefix of the response.
- `runs` exits with a failure and reports output errors on stderr when its
  listing cannot be written, including empty history and partial output.
- Release builds stop when any binary's module inventory cannot be read,
  instead of reporting success with an incomplete `sbom.txt`.
- Database checks use saved query plans instead of connecting to existing
  databases or executing statements through `EXPLAIN ANALYZE`.
- URL credential redaction handles apostrophes and embedded `@` characters
  without mistaking query or fragment text for credentials.
- Token usage parsing ignores fractional and exponential counts instead of
  recording their leading digits as whole-token counts.
- Custom agent files reject a top-level `null` instead of silently starting
  with built-in definitions; use `{}` for an empty configuration.
- The launcher no longer blocks stacked PRs on a dirty checkout because of a
  saved concurrency setting that stack mode ignores. Leaving stack mode restores
  the setting and its clean-tree requirement.
- `make vuln` scans the selected build tags, including the sqlite driver in
  the default CI scan, instead of silently scanning only the untagged build.
- Custom agent files report validation errors in name order, so identical
  definitions produce the same startup diagnostic across runs.
- Timeout and cancellation kill remaining subprocess-group members even when
  the leader exits before children that ignore SIGTERM.
- `show` exits with a failure when replay output cannot be written, rather
  than reporting success for a partial or missing replay.
- Prompt discovery excludes `--prompt-dir` when a symlink gives the same
  directory a different path, including macOS `/var` and `/private/var` aliases.
- Recovering the run index no longer appends a reconstructed row for a run
  that already Closed: when runs close out of start order (a long run still
  going when a short later one finishes), the duplicate row could replace the
  completed summary in `runs` listings, losing args, exit code, and measured
  elapsed.
- In-place retries and agent fallback stop when the starting tree could not
  be snapshotted, preventing repeated writes on top of a failed attempt.
- Releases reject whitespace-only and heading-only changelog sections instead
  of publishing without release notes.
- The launcher preserves all explicitly selected reviews when suggestions are
  enabled, instead of silently running only the suggested subset.
- `make check` analyzes all three shipped build modes even when `TAGS` is
  overridden, keeping local checks aligned with CI.
- `show` preserves exact JSON numbers, including 64-bit RNG seeds, so a
  seed copied from a recorded run replays the original schedule.
- A resumed run whose handoff was written before the wall clock was set
  back no longer resumes in the future with extra runtime.

## 1.21.0

### Changed

- `--stacked-prs` accepts `-n` / `--max-loops`. Default remains one ordered
  pass. An explicit N (or `0` for unlimited) starts each later pass in a
  fresh worktree cut from the previous pass's last published tip, so
  already-applied fixes stay in the tree and a no-op review opens no second
  PR. Loop 1 keeps the historical `review/<NN>-<review>-<topic>` branch
  names; later loops insert the loop number.

## 1.20.1

The changes below were present at tag `v1.20.1` but were left under
Unreleased and later attributed to 1.21.0. The release workflow requires a
matching version heading, so it could not publish binary assets for that
tag. Source installs at `v1.20.1` include these changes, as does 1.21.0.

### Fixed

- The commit step strips `Co-Authored-By` and `Generated-by` attribution
  trailers before the runner pushes the new commit.
- agy print-mode launches request `--output-format stream-json` when
  `--stream` is on and forward the runner's wait bound as `--print-timeout`,
  instead of exiting at the agent's five-minute default during longer reviews.

### Changed

- README dashboard and launcher screenshots now stamp the current release.

## 1.20.0

### Changed

- Share process-group kill, WaitDelay, and capped stdout/stderr across git,
  gh, usage probes, dsh config dumps, and the indexer.
- Parse agent JSON streams by extracting text and usage during decode instead
  of building an intermediate tree.
- Clip catalog descriptions, suggestion reasons, and review summaries with
  the same rune-bounded ellipsis the rest of the binary uses.

## 1.19.0

### Changed

- The prompt review now leaves well-constructed prompts alone in auto-fix
  runs instead of asking for report-only praise.
- Releases are now assembled as drafts before becoming visible, and rerunning
  a release refuses to replace an already-published version's assets or notes.
- Pin the `govulncheck` executable used locally and in CI while continuing to
  scan against the current vulnerability database.
- Dashboard and launcher panels now use square instrument frames instead of
  generic rounded cards.
- The run documentation now includes a quiesced backup and restore drill for
  durable state, including restore verification and explicit RPO/RTO guidance.

### Fixed

- Invalid or unrepresentable elapsed values in the run index now fall back to
  the recorded start and end times instead of displaying a wrapped duration.
- Deduplicate `--dirs` targets that name the same tree through symlinks.
- Preserve launcher run options when stacked PR mode is toggled off again.
- Build and help targets no longer create the test scratch directory; only
  test targets set up and use it.
- Custom agent files are validated completely before any definitions are
  registered, and blank executable names now fail at startup.

### Security

- Self-update authentication is now sent only to GitHub over HTTPS, preventing
  release metadata from forwarding a GitHub token to another host.

## 1.18.0

### Added

- `--max-reviews N` caps how many reviews one loop runs, however large the
  expanded `--reviews`/set schedule is. The cut happens after the seeded
  per-loop shuffle, so `--seed` replays exactly which N ran and different
  loops sample different reviews; a review scheduled twice fills two of the
  N slots when both land inside the cut. With `--stacked-prs` the single
  ordered pass is truncated to its first N entries. `0` (the default) is
  unlimited, and `--dry-run` reports the capped count.
- Three reviews for Kubernetes and GitOps repos: `k8s-review` (manifests,
  cross-resource reference integrity, API deprecations, and kustomize
  structure, components included), `gitops-review` (the Argo CD / Flux
  delivery layer: source pinning, sync and prune posture, ordering and
  health, secrets delivery, environment promotion), and `helm-review`
  (chart authoring: template correctness, the values contract, hooks, CRD
  lifecycle). Each gates on evidence in the tree and reviews
  tool-agnostically when the delivery tool leaves no markers. A new
  `gitops` set schedules them together with `container-review`,
  `infra-review`, `sec-review`, and `dr-review`.
- `--paths LIST` scopes every review to the named files, directories, or
  globs, relative to the reviewed directory (comma-separated, repeatable).
  The agent still works from the whole repository for context; the composed
  review prompt tells it to report findings on and modify only the listed
  paths, so the scope is prompt-enforced, not mechanical. Without the flag,
  prompts are byte-identical to before. Suggest, commit, and conflict
  prompts are unchanged, and an explicit empty `--paths` is refused.
- Stacked-PR bodies open with an overview of what the change is about: the
  `PATH:` lines a review prints are matched against the layer's own commit,
  deduplicated, and joined into one short paragraph under `## Summary`; the
  `## Changes` file list stays bare paths. Notes naming files the commit
  never touched are dropped. The overview is flattened, length-bounded, and
  backtick-neutralized like every other untrusted value in the body, and the
  whole body is now capped as well.

### Changed

- `container-review` and `infra-review` split Kubernetes workload
  ownership more sharply now that `k8s-review` and `helm-review` exist:
  manifest structure, probes, security context, and resource limits stay
  with those reviews; `infra-review` keeps compose, CI/CD, and IaC wiring.
  `container-review` will use `dockle`, `kubeconform`, and `conftest` when
  they are on PATH. `lint-review` names the project linters it should run.
- Stack branches are named `review/<NN>-<review>-<topic>` (e.g.
  `review/03-sec-review-input-validation`) instead of
  `gauntlet/stack/<tip>/<NN>-<review>`: the 1-based layer number keeps merge
  order sortable and the topic is a slug cut from the commit subject. Each
  layer starts under a deterministic provisional name
  (`review/<NN>-<review>-wip-<base>`) and is renamed once its commit exists,
  before the push. A resumed run finds published layers by listing the
  deterministic `review/<NN>-<review>` prefix and verifying candidates by
  commit graph -- a layer must be a one-commit child of the previous layer's
  tip -- so a same-named branch from an older stack is rejected by ancestry;
  when it occupies the topic name, the new layer appends the stack's short
  base commit. The preflight dry-run probe moved to the same `review/`
  namespace, and `review/` branches are no longer offered as merge targets,
  matching `gauntlet/`.

### Fixed

- Streamed runs (`--stream`, the default) lost every commit subject and
  per-file note: the report parsers read the output tail, which held the raw
  JSON event lines, and `SUBJECT:`/`PATH:` sit inside one escaped string
  there, where the parsers' line anchors match nothing. Commits fell back to
  the generated `chore: update <file>` subject every time. The tail now keeps
  each stream event's decoded text, so subjects, per-file notes, and the
  branch topics cut from subjects come from what the agent actually printed.

## 1.17.0

### Added

- `make ci` runs the Go pull-request checks (`make check` then `make test`).
- A missing or empty `index.jsonl` is rebuilt from the run journals, and a
  stale one has every newer unindexed journal appended, so `gauntlet runs`
  and the file-signal suggester still see a run whose process died after
  flushing the journal. `gauntlet show` already read those files.
  Reconstructed rows have no `args` or `exit_code`.
- `GH_TOKEN` is read for release lookups, the same name GitHub CLI uses. It
  wins over `GITHUB_TOKEN` when both are set, and both now authenticate the
  checksum and asset downloads as well as the release listing, so a private
  `--update-repo` can actually install.
- `gauntlet pick` can compose `--stacked-prs` from the run pane. Turning it
  on clears `--commit`, `--push`, and `--merge-into` and pins concurrency at
  1, so the launcher cannot emit a command the parser would refuse.
- `?` on the launcher opens a help overlay, the same key the dashboard uses.
  `q` / `esc` close it; they do not leave the picker.
- A project prompt that contains the opening review marker can no longer
  close the fence: both `BEGIN REVIEW` and `END REVIEW` in the body are
  rewritten, matching what the end marker already did.
- Conflicted paths named in the resolver prompt are fenced, dropped when they
  carry the resolver's output protocol or formatting characters, and capped;
  a conflict with more files than the prompt will name is left for a human
  instead of launching an agent that cannot finish.
- Commit subjects taken from agent output drop bidi overrides and Unicode
  line separators, not only ASCII controls, so a model cannot spoof `git log`
  or forge a commit body.
- Suggestion reasons from the triage agent are rune-capped like catalog
  descriptions, so one overlong line cannot flood the suggest listing.

### Changed

- `--merge-into` refuses to merge when git status cannot be read, the same
  way it already refuses a dirty tree, so a merge event cannot report work
  that never moved.
- A missing or stale run index that cannot be reconstructed is an error
  from `gauntlet runs`, not a listing that silently omits the newest run.
- A stacked-PR reload that cannot verify its pinned base commit fails
  rather than fetching a new tip and splitting the stack.
- Dashboard and launcher wordmark is the path-arrow teal of the mark
  (`#0e96a8` on dark terminals, a darker pull of that hue on light), one
  hue, not Catppuccin teal and not a per-letter gradient. Footer keys are
  body-colored chrome like the launcher's. The budget meter rides the heat
  ramp. Reload status uses the info hue. Panel names stay dim with the
  rest of the chrome.
- Dashboard lanes and the feed drop the `-review` suffix the grid already
  omitted, so a name is spelled the same way on every instrument.
- `f` in the dashboard footer says `widen` while the feed is narrowed, the
  same way `space` says `resume` while paused.
- `home` / `end` jump the dashboard feed the way `g` / `G` do, and jump to
  the first or last row of the focused launcher pane.
- The launcher shows the same "warming up" line as the dashboard until the
  terminal reports its size, instead of a blank screen.
- `--continue-sessions` with `--jobs` above 1 or `--stacked-prs` is a usage
  error. Those modes give each review a fresh worktree, so there is no session
  to resume; the flag used to be accepted and silently ignored. Scripts that
  passed both will see exit 2.
- `q` / `esc` on the live dashboard arms a hard stop instead of killing the
  run on the first press (1.15.0 still documented immediate quit). A second
  press, or `q` after the run has finished or is already draining, closes the
  dashboard and cancels the run. The header shows `q TO STOP` while armed;
  any other key disarms it. `q` on the help overlay still only closes help.
- `gauntlet runs` prints STARTED as local `YYYY-MM-DD HH:MM:SS`. The old
  `MM-DD HH:MM:SS` column had no year and swapped day and month for readers
  used to ISO dates.
- `gauntlet runs` DURATION uses the monotonic elapsed Close records, matching
  the run's Total time. An NTP step or a manual clock set between start and
  end can no longer stretch or shrink the listing. Old index rows without
  `elapsed_s` still use End−Start; a pair that moved backwards prints `n/a`
  instead of `0s`.
- Bundled review prompts: integer-width and abbreviation rules no longer
  rewrite language-idiomatic types and names; post-quantum crypto items are
  note-only; Kubernetes-native checks skip Dockerfile-only trees; docs-vs-code
  disagreements have a single owner.
- Ruff on `scripts/` selects the bugbear, pylint, pyupgrade, and bandit
  groups (and every other category those two files already pass), with a
  100-column cap, instead of the default four error codes. `scripts/shots.sh`
  is gated with shellcheck in the same CI job. Rule selection lives in
  `pyproject.toml`. `make check-scripts` runs the same pinned ruff, mypy, and
  shellcheck steps as that job, so a scripts/ change fails locally.
- Docs for `--jobs N` describe the persistent lane worktrees the runner
  actually uses, not the per-review throwaway checkouts that 1.13.0 replaced.
- Design docs name the git hardening the runner actually applies
  (`core.pager=cat`, `attr.tree`, local driver blanks) and the conflict-step
  cap that leaves an oversized conflict for a human. `--stream` is documented
  as on by default, matching the flag. The landing-page trust model names
  `core.pager=cat` rather than an empty pager.
- Live usage ticks on the event bus are droppable, the same as agent output:
  a slow subscriber no longer stalls the scheduler on reconstructible
  telemetry. Final token counts still ride on `review_end`.
- `gauntlet show` and suggest history look up a generated run id by the date
  it encodes, instead of probing every day directory under the journal.
- `gauntlet runs` parses the index tail from the end, so a long index costs
  the rows shown rather than a split of the whole slice.
- The file-signal suggester reuses one git handle for the tree listing and
  the churn window. Opening a repo no longer runs `rev-parse HEAD` until
  line stats need a baseline. A million-file tree is listed only up to the
  scan cap, so the unused tail does not stay in memory.
- Project prompt discovery asks git for `*-review.md` by name instead of
  walking the tree. Generated and hidden directories are still skipped;
  a directory that is not a repository still walks.
- Worktree line samples run `git diff --shortstat` and `ls-files -o`
  together instead of one after the other.

### Fixed

- A hot-reload handoff that cannot be read or parsed now aborts the successor
  instead of starting a fresh run. The unparseable case was silent, so the new
  process re-ran every finished review under a new run id.
- Lane worktree removal failures and unreadable `HEAD` reads during `--jobs`
  scheduling are logged. Removal errors were discarded, and a failed `HEAD`
  read was indistinguishable from an unchanged tip, so a lane silently kept
  its stale base.
- `gauntlet update` network and JSON decode errors name the URL that failed.
- `--merge-into` no longer treats untracked files as uncommitted work. The
  merge is a scratch checkout of committed work, so a local notes.txt was
  never going to be in it; refusing the merge used to drop a loop's
  committed changes. Tracked dirty files still block, matching `--jobs`.
- A pump that outlives an agent's process no longer publishes output or
  usage after that review has ended. Those events are keyed by agent, so
  they used to land on whatever the same agent started next.
- A trailing `}` or `]` after `agents.json` is refused, matching
  `encoding/json`. `json.Decoder.More` treats those closers as end-of-value,
  so `{}}` used to load as an empty definition set.
- Listing runs recovers the whole tail of journals missing from
  `index.jsonl`, not only the newest, so two runs that died before Close
  both appear. A failed index write is retried on the next Close, and a
  rebuild cannot overwrite a Close that races it.
- `gauntlet runs` and the file-signal suggester list a crashed run that
  sits behind a later Close, not only an unindexed suffix. The index is
  a cache of summaries; the n newest journals are the listing, so a hole
  in that window is filled from the event stream without rewriting Close
  rows.
- Path flags (`--dir`, `--dirs`, `--log`, `--prompt-dir`, `--bin`) refuse an
  environment variable that is unset or empty instead of expanding it to
  nothing. `$MISSING` used to become the current directory (`--dir`), the
  bundled prompts (`--prompt-dir`), or a silently dropped log (`--log`). An
  explicit empty `--prompt-dir` or `--log` is a usage error too, matching
  `--dir`. A leading `~/` with no usable HOME is refused rather than taken
  relative to the working directory.
- The launcher's `a` key and a set header's space bar act on the reviews the
  filter is showing, not the ones it hid. A fruitless filter no longer
  selects the whole catalog.
- Typing a review filter on the launcher replaces the key legend with the
  keys that work there (`enter` keeps it, `esc` clears it). The legend used
  to keep advertising `run` and `cancel`, which those keys do not do until
  the filter is closed.
- A `--usage-limit` probe that prints more than 4 KiB is ignored, the same
  as any other broken probe, instead of filling memory until the timeout.
- One git or `gh` command's captured output is capped (32 MiB and 8 MiB)
  so a hostile tree or a runaway listing cannot grow without bound.
- Closing `--log` reports a write error instead of dropping it.
- The dashboard feed's scroll offset stays inside the retained ring, so a
  long pause cannot claim thousands of lines back after history is trimmed.
- An in-place retry restores the working tree to the snapshot taken before
  the failed attempt, including the user's own uncommitted files, so the
  next try starts from the same files the first one saw. Isolated reviews
  already reset their worktrees. `--continue-sessions` no longer resumes a
  failed attempt's session.
- The untracked-file line-count cache drops files that have vanished or
  left the untracked set, so a long loop that creates then commits files
  does not fill the table with dead keys and re-read every later file.
- Git and GitHub errors that quote a remote URL drop URL userinfo. A remote
  stored as `https://alice:token@host/repo.git` is reported as
  `https://host/repo.git`, so the account name does not reach the terminal
  or the run journal.
- dsh model overlays are keyed uniquely: `foo/bar` and `foo_bar` no longer
  share one `--patch` file, so a later pin cannot launch with an earlier
  pair's provider and model. A deleted overlay is rewritten instead of
  handed to dsh as a missing path.
- The untracked-file line-count table stops admitting new keys at its cap
  instead of wiping the working set, so a tree with more than 4096 new
  files does not re-read every already-counted file on the next sample.
- `--usage-limit NaN` (and `nan`) is a usage error. `flag.Float64Var` accepts
  it, the 0-100 range check cannot see it, and the runner's `pct < limit`
  comparison is then always false, so a run configured with a NaN limit
  stopped before its first review. Non-finite values are refused the same
  way a probe that prints them already is.
- Stream-JSON token counters that are not whole numbers, or that claim more
  than a trillion tokens, are ignored rather than truncated or stored.
  `{"output_tokens": 1.9}` used to record 1, because JSON numbers decode as
  `float64` and `int(1.9)` is 1. A 2^62 counter fitted in `int` and then
  overflowed the run total. Both match what `json.Number` parsing and the
  text-usage cap already required.
- The dashboard reasoning glyph no longer panics when the clock is set
  before 1970. Go's remainder keeps the sign of a negative `UnixNano`, so
  the frame index was -1.
- Unknown commands and flags include a "did you mean" hint, matching unknown
  review and agent names. `--show-prompt` does too.
- `gauntlet --list` and `--show-prompt` no longer require an agent CLI in
  PATH. They only read prompts; `gauntlet doctor` reports which agents are
  installed.
- Global flags (`--help`, `--version`, `--log`, `--no-color`) may precede the
  subcommand, so `gauntlet --no-color doctor` works. `gauntlet show` accepts
  flags before the run id, so `gauntlet show --no-color RUN` and
  `gauntlet show --help` after `--no-color` work.
- `gauntlet show` on an unknown run id points at `gauntlet runs`.
- Stacked PR creation treats head and base as an idempotency key: if
  `gh pr create` times out after GitHub accepted the pull request, or fails
  because it already exists, the existing URL is reused instead of stopping
  the stack or opening a second PR.
- `StartBranch` on a lane or stack worktree converges when the branch already
  exists at its base, the same leftover-empty-branch rule `AddWorktree`
  already follows. A branch that carries commits is still refused.
- A review that never launched (unknown name, unreadable prompt, or a command
  line that could not be built) now publishes `review_end` like every other
  outcome, so the journal, the dashboard, and `gauntlet show` record it. The
  file-signal suggester no longer treats those, or failed, timed-out, and
  interrupted launches, as finished runs that changed nothing.
- `gauntlet pick` no longer treats untracked files as blocking `--jobs`.
  The runner has allowed them since 1.12; the launcher was still using a
  full dirty-tree check and refused a run that would have started.
- Stacked PRs refuse a remote whose path is not OWNER/REPO on both sides.
  `https://github.com/owner/.git` and `https://github.com//repo` used to
  pass the one-slash check and be handed to gh as `owner/` and `/repo`.
- The README install snippet fetches the binary for the version it just
  resolved, rather than `releases/latest/download`, so a release published
  between the two curls cannot pair a new tag with the previous binary.
- The README install snippet and `make install` say when `~/.local/bin` is
  not on PATH. On macOS it is not there by default, so the binary was
  installed and then not found.
- The directory lock is no longer inherited by agent and git children. The
  lock descriptor is opened close-on-exec, so a killed parent cannot leave
  the tree locked for as long as those children live.
- Agent timeout and output-drain waits no longer leave a timer running after
  the process has already exited. Each wait is a timer that is stopped when
  the other path wins, matching the retry backoff.
- `--update-repo` is checked as `owner/repo` at startup. A URL, a missing
  slash, or an extra path segment used to reach the GitHub API and fail there
  with a status line, or to hit a different endpoint entirely.
- `--dir`, `--dirs`, and `--push-remote` refuse an empty value instead of
  treating it as the current directory or as `origin`.
- A defined agent's `usage` with no `roots` is refused when the definition
  is loaded, rather than later when transcript registration fails without
  naming the file.
- A hot-reload handoff records the predecessor's monotonic elapsed time, so
  `--runtime` and the dashboard clock do not jump when the wall clock steps
  during the exec. An older handoff without the field still uses the
  wall-clock span from when the run started.

## 1.16.0

### Added

- Journal `review_start` and `review_end` events carry the worktree's `branch`
  in worktree mode. With `--jobs` above one the lane a review lands in is
  whichever goroutine won the queue race, so the branch is the only record of
  which working directory the agent actually saw.

### Changed

- `gauntlet suggest` orders its agent pool with the same keyed draw the review
  schedule uses, so the order is a pure function of the recorded seed and the
  pool size. It was `math/rand`'s `Shuffle`, whose sequence nothing in-tree
  pins, so the same seed could pick a different agent after a toolchain update.

### Fixed

- Reload handoff files whose successor never ran are swept on the next save.
  A kill, an OOM, a power cut, or a failed exec between the save and the
  re-exec skips every defer and leaves a blob no process will ever read; they
  accumulated in the state directory. Files older than the retention window
  the temp sweep already uses are removed, best effort.

## 1.15.0

### Fixed

- An agent can no longer disable `Ctrl-C` for the whole run. Agents now start
  in their own session, so they have no controlling terminal: previously an
  agent CLI could open `/dev/tty` and put the shared terminal into raw mode,
  after which `Ctrl-C` generated no signal for anyone and the run could not be
  stopped from the keyboard at all. The kernel's SIGTTOU guard does not
  prevent this -- a runtime that ignores SIGTTOU, as Node-style CLIs routinely
  do, changes the terminal settings from a background process group without
  breaking stride. Reproduced with an opencode-style agent; the group-kill
  semantics on timeout and termination are unchanged, since a session leader's
  process group is its own.

- A `--usage-cmd` probe that prints `NaN` no longer ends the run on its first
  check. `NaN` parses as a float and compares false against every bound, so it
  passed the 0-100 range check and then read as "at or past the limit": a run
  configured with `--usage-limit` stopped before its first review, reporting a
  graceful finish rather than the broken probe. Non-finite answers are now
  rejected with the other unreadable ones, and the limit is ignored for the
  run, which is what every other probe failure already did.

- `--usage-cmd` is no longer accepted when it holds nothing but whitespace.
  The value is split on whitespace into an argv, so a blank one produced no
  command at all; the check that refuses `--usage-limit` without a probe
  compared the raw string, so `--usage-cmd " " --usage-limit 80` passed it and
  the run carried a limit that could never trip. It is now a usage error.

- A defined agent's `argv` now expands every placeholder in an argument, not
  just the first kind it mentions. An entry packing more than one into a
  single option (`"--opts=model={model},effort={effort}"`) had the rest passed
  through verbatim, so the CLI was launched with a literal `{effort}` on its
  command line. A `{model}` or `{effort}` written inside the review prompt is
  still left alone: the prompt is content, not a template.

- File names keep their leading and trailing spaces. Git's NUL-separated
  output was trimmed record by record, so a file called ` notes.md` came back
  as `notes.md`: a stacked PR body listed a path the commit did not touch, and
  the file-signal suggester keyed on a name the tree does not have. The
  records are now taken exactly as git wrote them, which is what `-z` is for.

- `--stacked-prs` accepts a remote URL written with a trailing slash. Git
  stores `remote.<name>.url` exactly as it was typed, and a browser address
  bar copies `https://github.com/owner/project/`; the trailing slash left an
  empty last path segment, so the OWNER/REPO check counted two separators and
  a stacked run refused a remote every other git command works with. The same
  slash also made `https://github.com/only-owner/` parse as a repository,
  which it is not; both now resolve correctly.

- A defined agent no longer receives an empty `--model=` when the run pinned no
  model. An `{model}` or `{effort}` placeholder written into a definition's
  `argv` expanded to nothing, while the equivalent `model` block was simply not
  appended, so the two spellings of the same setting behaved differently: one
  launched `myagent -p PROMPT`, the other `myagent --model= -p PROMPT`, which
  the CLI either rejects or takes as a model named the empty string. An
  argument mentioning an unpinned placeholder is now left out whole. The
  argument carrying `{prompt}` is always kept.

- Agent output in a stream-mode run keeps the order the agent wrote it. JSON
  lines were decoded into a map, and Go randomizes map iteration, so an
  envelope carrying two text fields, or two sibling blocks each carrying text,
  came out swapped roughly one line in ten: the dashboard feed reordered an
  agent's sentences and the run journal recorded them that way. Objects now
  keep their key order through decoding. A repeated key contributes both
  values rather than only the last, since both are text the agent emitted.

- The dashboard's agent panel counts hidden agents correctly. The "+N more
  agents" line spends one of the panel's rows, which the count did not
  subtract, so it always named one agent fewer than it was hiding: eight rows
  and ten agents drew seven lanes and reported two hidden rather than three.
  The review grid's own overflow marker already did this arithmetic right.

- `--suggest`'s confirmation no longer offers reviews that `--exclude` has
  already removed. The preview was built with no exclusions applied, so a run
  with `--reviews sec --exclude sec` listed `sec-review` as "named on the
  command line" and counted it in the total the prompt asks about, while the
  schedule correctly dropped it. The preview and the schedule now share one
  expansion, and a bad `--reviews` is reported before consent is asked for
  rather than after it is given.

- `gauntlet pick` no longer composes a run that selects different reviews than
  were ticked. The launcher shortens a selection by dropping the `-review`
  suffix, and `--reviews` resolves set names and the `suggest` keyword before
  review names, so a tree carrying `security-review.md` was launched as
  `-r security`: the eight-review security set ran and the ticked review did
  not. A name whose short form is one of those words is now written out in
  full.

- `--list` and `--dry-run` line their columns up for review names that are not
  one column per character. The name column was budgeted in runes (`--list`)
  or bytes (`--dry-run`) and padded by `fmt`, which counts runes; a terminal
  lays out in cells, so a project prompt with a CJK name pushed the `[project]`
  column three places out of line and could run its description past the right
  edge. Widths, padding, wrapping, and the description cut are now measured in
  terminal cells, as the dashboard already measured them.

- A review's `mark:` signal now matches. `Signals: mark:comptime` is the
  example both `docs/RUNS.md` and the prompt reference give, and it could never
  fire: the file-signal suggester recorded only the built-in table's category
  labels (`http`, `sql`, `concurrent`, ...), so a declared value matched only by
  colliding with one of those. Declared substrings are now searched for in the
  same file heads the built-in rules read, bounded to 64 across the prompt set.
  A tree whose reviews declare no `mark:` is scanned exactly as before.

### Changed

- Transcript reading requires toktop v0.7.0, which follows dsh's default
  zstd session logs (`session.jsonl.zstd`). `--stream` no longer patches
  dsh to write uncompressed JSONL.

- `Ctrl-C` is staged. The first one is now the graceful quit -- the review in
  flight finishes and lands its work, commit, push and PR included, exactly as
  `SIGQUIT`, `s` on the dashboard, and a tripped usage limit end a run -- the
  second terminates the running reviews and exits 130, and the third
  force-kills. A `Ctrl-C` arriving while any finish request is already
  draining skips straight to terminating. `SIGTERM` is unchanged and never
  staged: a supervisor's `SIGTERM` means stop now. The dashboard's `Ctrl-C`
  follows the same stages; `q` and `esc` still quit immediately.

### Added

- `--usage-limit PCT` with `--usage-cmd CMD` ends a run gracefully when a
  provider's usage window is nearly spent, rather than letting the next review
  hit the wall. Between reviews the runner asks the command what percentage is
  gone; at or above the limit it stops starting reviews, and the one in flight
  finishes normally — commit, push, PR, merge all still happen. The percentage
  has to come from a command because no agent CLI reports it to a headless
  launch: Claude Code, for instance, reads it from an API response header and
  passes it to a status line, which does not run under `--print`. A probe that
  fails or answers with something other than a percentage is reported once and
  then ignored for the rest of the run, so a broken probe cannot end a run
  early.

## 1.14.2

### Fixed

- A worktree commit whose review printed no `SUBJECT:` line names the files
  it touched (`chore: add helper.go`) instead of `chore(<review>): apply
  review findings`. The fallback is what git status shows, not the review
  that produced the diff, so the history still reads as the project's.

## 1.14.1

### Fixed

- A cancel during `git worktree add` no longer leaves a locked,
  half-created worktree behind. All three worktree creation paths
  (lanes, stacked PRs, snapshot) now unlock and remove on failure.
  `Worktree.Remove` retries after unlocking when the first attempt fails.

## 1.14.0

### Changed

- Plain reporter summary uses blue stat labels and bold section headers
  (Pull requests, Per-agent stats, Failed reviews) for visual structure.
  Two new palette codes: blue, cyan.

- All user-facing text now says "lane" instead of "worktree" when describing
  `--jobs N` parallel mode: dashboard header, dry-run output, startup log,
  `--jobs` help text, and the pick TUI.

## 1.13.0

### Changed

- `--jobs N` now creates N persistent lane worktrees reused across reviews
  instead of a throwaway worktree per review. Within a lane, reviews run
  sequentially in the same directory, so the agent's system prompt prefix is
  byte-identical and the provider's prompt cache hits after the first review.
  Cache cold starts scale with the lane count, not the review count: a
  15-review run with `--jobs 3` pays 3 cold starts and gets 12 cache hits,
  compared to 15 cold starts and 0 hits previously. Falls back to sequential
  mode if lane creation fails.

- An `--agents` entry can pin a reasoning effort alongside the model:
  `claude:opus-5@xhigh`, `opencode:anthropic/claude-sonnet-5@medium`, or
  `claude@max` with no model. The part after the last `@` is the effort, and
  like a model id it is passed to the CLI verbatim. It is wired only for
  agents whose flag was verified from the CLI's own help, `claude`
  (`--effort`) and `opencode` (`--variant`), and for defined agents via a
  new `effort` argument list (or an `{effort}` placeholder) in
  `agents.json` / `--agent-cmd`; every other agent refuses `@effort` at
  startup instead of guessing a flag.
- Custom agent names may no longer contain `@`, which now separates the
  effort. A definition in `agents.json` or `--agent-cmd` that uses one is
  refused at startup, like the other reserved separators.

- A stacked run's pull requests describe themselves. The body was the commit
  subject under a `## Summary` heading, which repeated the title and said
  nothing else; it now adds the review's declared subject area, the paths the
  layer's commit touched (ten, then a count), its diff stat, and where the
  branch sits in the chain, so a PR can be triaged without opening the diff. A
  value git or the prompt will not answer is left out rather than guessed at,
  and everything read out of the reviewed repository is flattened to one line
  and length-bounded before it becomes Markdown.

### Fixed

- `--agents mixed:opus` (or `mixed@high`) no longer falls through to an
  error that calls `mixed` unknown and valid in the same sentence; it now
  says the keyword selects every installed agent and cannot pin a model or
  effort.

## 1.12.2

### Changed

- Source builds and `go install` need Go 1.27.

### Fixed

- The file-signal suggester no longer follows a last-component symlink out of
  the reviewed tree, and no longer blocks on a planted FIFO, when it peeks at
  source. Duplicate-prompt comparison uses the same open as prompt body reads
  (`O_NOFOLLOW`, non-blocking), so a swap between lstat and read cannot pull
  out-of-tree content into the comparison.

## 1.12.1

### Fixed

- Cancelling a stacked run while it sets up a layer records that review as
  interrupted instead of failed. The cancel kills the git or gh command
  building the layer, and the step reported its own failure, so a Ctrl-C
  counted a failed review and published a `pull_request` failure event for a
  layer no agent had touched. Only reachable on a slow enough machine to be
  mid-setup when the cancel lands, which is why it showed up on macOS first.

## 1.12.0

### Added

- `--stacked-prs` runs the selected reviews sequentially in one isolated
  worktree. Each changed review is committed and pushed on a child branch,
  then opened as a pull request against the preceding changed review; the
  original checkout is never changed and no pull request is merged. The base
  commit is fetched directly from the publishing remote after every preflight
  check passes, the dirty-checkout consent included and before any agent
  starts, the suggestion agent too. That commit is then pinned for the whole
  run: a hot reload resumes the same stack even when the remote base has
  advanced. Project prompts and suggestion signals are read from a snapshot
  of that fetched base, never from uncommitted files in the checkout. A
  remote with distinct fetch and push URLs opens cross-fork PRs with the head
  qualified by the push-side owner. `--pr-base` selects the first base and
  `--push-remote` selects the publishing remote.
- A prompt may declare a `Summary:` line, the short form of what the review
  looks for, joining `Signals:` as a line a project's own review can carry. It
  is what `--list` and the launcher's picker now print, so the catalog reads as
  one line per review instead of a goal sentence cut mid-clause, and a review
  that declares none still falls back to that sentence. Documented in
  docs/RUNS.md.

### Changed

- The README carries the full review grid: every bundled review and what it
  finds, so the front page shows the catalog it used to only count. A test
  rebuilds the table from the prompts and fails when the two disagree.
- Every review's fixing rules gained two checks around proving a finding.
  After proving one real, the agent now argues the author's side, and that
  counterargument only retires the finding when it can name the code backing
  it, so a plausible-sounding excuse no longer discards a true positive. The
  second check bars the opposite failure: "unlikely in practice" is not
  grounds to drop a deadlock, crash, or data-corruption finding, only
  "structurally impossible" is, so a wait with no timeout still gets fixed
  when the trigger needs bad timing.

## 1.11.0

### Added

- Each launch journals the SHA-256 of the prompt text it ran under
  (`prompt_sha256` on `review_start` and `review_end`), so an output stays
  attributable to the exact words that produced it after the prompt file has
  changed or disappeared. Bundled prompts have the run's version line for
  identity; project prompts and `--prompt-dir` files had none at all.
- `GAUNTLET_NO_ANIMATION` joins the environment surface: set it to anything
  but empty or `0` and the dashboard's animated reasoning glyph holds one
  frame instead of cycling, for motion sensitivity. The token count beside it
  keeps updating, so an active agent still reads as one.
- `TERM` is documented as part of the environment surface it always was:
  `TERM=dumb` disables color, even with `CLICOLOR_FORCE` or `FORCE_COLOR` set.
  The help screen's environment section now lists it beside the other color
  variables, matching docs/CLI.md.

### Changed

- A binary that was not stamped reports the version the Go toolchain embedded
  rather than `dev`: `go install github.com/maci0/gauntlet/cmd/gauntlet@latest`
  names its release, so `gauntlet update --check` compares against it, and a
  build from a source tree one tag behind the working tree says so
  (`1.10.0+dirty`). Builds that do stamp (`make dist`, `release.yml`) report
  exactly the value `-ldflags` put there; only `(devel)` and absent module
  versions stay `dev`.
- A repeated `--agent-cmd` giving one agent two different definitions refuses
  the run instead of letting the later one silently win; an exact repeat stays
  idempotent. Scripts that stacked definitions unnoticed now see the error,
  the way `--bin` has always refused its own duplicates.
- The agent output tail is a ring buffer instead of an unbounded transcript,
  journal history is filtered on the way in rather than after it is read, and
  the launcher and dashboard redraw from cached frames when nothing changed,
  so watching a long run does not get slower as it grows.

### Fixed

- A merge failure that leaves nothing unmerged is reported as failed rather
  than MERGE CONFLICT: the conflict resolver is no longer sent to fix what no
  edit fixes, the summary does not call a broken merge a conflict, and the
  branch-keeping rule keeps its meaning for real conflicts. A file that
  cannot be read fails the conflict-marker scan instead of silently counting
  as resolved.
- Reviews a hard cancel will never start are recorded as interrupted in the
  sequential loop, the way the parallel loop already recorded its stranded
  lanes, so the summary, the stats, and the journal no longer drop them.
- A review whose worktree add loses a cancel race is cleaned up (the
  half-written worktree registration and its branch) and reported like any
  other skipped review instead of leaving both behind.
- A review skipped because its worktree could not be created journals its
  `review_end` like every other outcome, so a consumer rebuilding a run from
  the journal no longer waits on an event that never came.
- A run whose stdin is a character device that is not a terminal (`/dev/null`
  under cron) proceeds without confirmation, instead of prompting, reading
  EOF, and aborting. The tty check is `term.IsTerminal`, the same answer the
  launcher's gate gives.
- The dashboard, the launcher, and their non-interactive fallbacks render
  inside the terminal's width and height instead of scrolling it, and the
  keys that matter stay visible when a narrow terminal clips the footers.
- The help screen documents the defaults it was omitting: `--timeout` and
  `--suggest-timeout` name their 30 minutes, `--limit` its 20 entries, and
  `--runtime` its `0 = unlimited`.

## 1.10.0

### Changed

- `--suggest-agent gauntlet` weighs evidence instead of matching filenames. It
  now counts how much of a tree each language is (one stylesheet no longer
  proposes five frontend reviews), reads the head of source files for what they
  import and call, asks git which files have changed in the last 90 days,
  treats absence as evidence (no tests, no docs, no CI argue for the reviews
  that would fix that), and demotes reviews that have finished in a directory
  several times without changing a line. Proposals are ranked by that evidence
  and the weakest are dropped. Against the reviews agents picked in this
  install's own journals, recall went from 0.62 to 0.82 at the same precision.
- The tree is listed with `git ls-files` where there is a repository, so the
  project's own ignore rules decide what counts as source. The walk with a
  built-in skip list stays as the fallback.
- Five bundled reviews (`arch`, `dr`, `functionality`, `idempotency`, `lint`)
  could not be proposed by any rule. They can now.

### Added

- A review can declare what it keys on with a `Signals: ext:.zig, name:build.zig`
  line, which is what makes a project's own prompt reachable by the file-signal
  suggester: the built-in rules only know built-in names. The line comes from
  the reviewed tree, so it is parsed strictly and bounded.
- `scripts/suggest-calibrate.py` scores the suggester against the picks agents
  made in past runs, so a rule change is measured rather than argued about.

## 1.9.0

### Added

- `--resolve-conflicts` (on by default): a review branch the main tree refuses
  is handed to an agent, which resolves the conflict in a scratch checkout cut
  from the current tip; the result is merged and the branch deleted. The merge
  lock is held for the whole step, nothing carrying conflict markers is
  committed, and every failure path leaves what a plain conflict left, the
  branch kept for a human. `--resolve-conflicts=false` restores that older
  behavior.

### Fixed

- Parallel reviews cut their worktree from the tree's current tip instead of
  the commit the loop started on. Lanes refill for as long as a loop runs, so
  a review that waited hours had to merge against everything that landed
  meanwhile: in real runs the first merge of a loop never conflicted and half
  the later ones did.

## 1.8.0

### Changed

- `--suggest` composes with `--reviews` instead of refusing to run beside it.
  The triage step picks, and every review named on the command line is
  scheduled as well; one that appears on both lists is scheduled twice, which
  is what repeats in `--reviews` have always meant. `--reviews suggest,sec`
  says the same thing, so "suggest" is no longer required to be the only value
  in the list. The proposal prints what rode along with it, and the launcher
  keeps its review tree live when suggest is ticked, weighting what you tick
  rather than greying it out.
- Single-letter flags accept their value glued on, so `-j3` means `-j 3` the
  way it does in make and tar. The spaced and `=` forms are unchanged.

## 1.7.4

### Fixed

- The coverage floor is CI's measurement rather than a developer machine's.
  1.7.3 shipped it at 76.0%, which is what this suite measures where the agent
  CLIs are installed; a runner without them covers about two points less, so
  every pull request would have failed the gate the release introduced.

## 1.7.3

### Fixed

- `SUBJECT:` lines keep no ragged end. Control bytes were stripped after the
  trim, so a line ending in spaces and a NUL left the spaces in the commit
  subject. Found by the fuzz target added for it, whose seed is now the
  regression corpus.

### Changed

- Coverage ratchets: `make cover` fails below `COVER_MIN` and CI runs it, so a
  refactor that quietly drops a tested path fails where it happens rather than
  a release later. This release carries 76.0%, a measurement taken where agent
  CLIs are installed; CI measures about two points lower, so pull requests
  fail until 1.7.4 lowers the floor to CI's own 74.0%.
- Two parsers that read input from outside the program are fuzzed:
  `SUBJECT:` lines, which are agent output on their way into a commit message,
  and `--timeout` durations, which are whatever a person typed.
- Production code that only tests called is gone or moved: the runner's
  `finishing`, gitx's uncached `countLines` (its test drives the cached path
  the program uses), the dashboard's `staticFrame`, and the help table's
  `names` accessor. The two that stayed live beside their callers, in test
  files.

## 1.7.2

### Fixed

- The binary lookup follows `PATH` instead of answering from a cache built
  before it changed. Both resolvers memoized on nothing: the agent probe and
  the git lookup each answered once per process, so a program that added a
  directory to `PATH` (a wrapper, a test harness) kept being told the tool it
  had just put there was missing. Each memo is keyed by the `PATH` it was
  built from now. This is what made 1.7.1 fail to publish: its own test suite
  hit the stale answer on a machine where the agent it stubbed was not
  otherwise installed.

### Changed

- The prompt library and the docs follow the writing rules the project sets
  for the code it reviews: no em dashes (49 of them, rewritten as the comma,
  colon, or sentence break each one was standing in for), and none of the
  weasel words the style rules ban, of which "actionable" alone appeared 27
  times in one boilerplate line that read better without it.
- Tuning constants that were sitting inline are named where they are set: the
  flag defaults the docs quote (`--timeout`, `--retries`, `runs --limit`), the
  dashboard's thinking-glyph timings, and the four budgets every git command
  now picks from instead of carrying a bare duration.
- `.scratch_refs.txt`, a throwaway file that reached a commit, is out of the
  repository. The root holds config, manifests, and top-level docs, and
  nothing else.
- The journal reports a write it lost. `Flush` and `CloseQuiet` dropped their
  errors on the floor, so a disk that filled halfway through a run left a
  truncated journal that still closed clean; both keep the first error now,
  and `Close` reports it the way it always claimed to.
- CI gates the one Python file here (`scripts/shots/render.py`) with ruff and
  mypy `--strict`, the way every other language in this repository is gated.
- Every remaining discarded error says what it swallows and why nothing else
  can reach it: a process group that is already gone, a ring buffer that
  cannot fail, a cleanup whose failure the next command reports anyway.
- `scripts/shots.sh` no longer writes Python from shell or works in `/tmp`:
  the renderer is `scripts/shots/render.py`, a uv script with inline
  dependencies, and the working directory is `.scratch/`, which is on disk
  rather than in RAM.

- The README shows the dashboard and the launcher as screenshots rather than
  an ASCII transcript. Both are the renderer's own output: `scripts/shots.sh`
  writes the frames from `internal/ui`, exports them as a terminal, and
  rasterizes that, so refreshing them after a change to the screen is one
  command rather than a hand-drawn approximation.

## 1.7.0

### Added

- `make vuln` runs locally the same govulncheck advisory scan that
  vulnscan.yml runs on pull requests touching go.mod or go.sum, so a
  vulnerable dependency bump surfaces before push instead of after.
  CONTRIBUTING.md documents it and the real steps for adding a review
  prompt: the name-surface snapshot in contract_test.go must gain the new
  stem in the same change.
- A release-contract guard. Tests now pin every surface this file declares
  consumer-facing: bundled review names, review set names, CLI flag names,
  commands, exit codes, the documented environment variable names
  (`GAUNTLET_HOME`, `GITHUB_TOKEN`, `NO_COLOR`, `CLICOLOR_FORCE`,
  `FORCE_COLOR`), and that no importable Go package lives outside
  `internal/`. Removing or renaming one fails the suite with the required
  bump kind spelled out, and a set member left pointing at a renamed review
  is caught instead of silently shrinking its set. This is the automated
  check whose absence let the public `agentusage` removal ship in the 1.0.1
  patch release unrecorded.

### Changed

- A subcommand now refuses flags it does not read, instead of parsing and
  silently dropping them: `gauntlet runs --jobs 4` used to print its table
  while ignoring the concurrency it was given. Each command accepts what
  docs/CLI.md lists for it (`pick`: `-C/--dir`, `--dirs`, `--prompt-dir`;
  `doctor`: `--bin`, `--agent-cmd`; `update`: `--check`, `--update-repo`;
  `runs`: `--limit`; `show`: nothing of its own), plus the global `--log`
  and `--no-color`, and names the flag and the command in the error.
  Scripts that passed such flags and never noticed will see exit 2.
- gauntlet's own scratch is excluded from the repository it reviews: the run
  lock (`.gauntlet.lock`) joins the worktree root in `.git/info/exclude`, in
  every run rather than only in `--jobs` mode, so it can never be committed by
  a review, a commit step, or a person running `git add -A`. The exclusion is
  local to the clone, which is where one tool's scratch belongs.
- Worktree runs leave a history that looks like the project's. Review branches
  are squashed rather than merged, so a loop of forty reviews leaves forty
  commits and not forty merge nodes; the commits are authored with your git
  identity instead of a `gauntlet <gauntlet@localhost>` one; and the subject
  is what the review says its change was, in the project's own conventional
  form, rather than "automated review fixes". Reviews print it as
  `SUBJECT: fix: …` alongside their `PATH:` lines, and a review that prints
  none falls back to `chore(<review>): apply review findings`.
- `--push` pushes each review as it lands in worktree mode, not once at the
  end of the loop, so a long run publishes as it goes. A failed push is
  reported and counted rather than fatal: the work is committed, and the next
  push carries it.
- The offer to commit a dirty tree uses the run's own agent, never
  `--suggest-agent`: that one was asked which reviews apply, which says
  nothing about who should write commits.
- The commit step asks for a conventional type. Its prompt matched whatever
  style the repository already used, which on a repository with no style
  produced whatever the agent felt like; it now asks for `feat`, `fix`,
  `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, or `style`, a
  scope when one area owns the change, imperative mood and no trailing
  period, and still defers to the repository when its own log says otherwise.
- `--merge-into` writes `Merge branch '<branch>'` rather than a message
  naming the run that produced it, and a conflicted review now prints the
  command that lands it without writing the scratch branch name into the
  history: `git merge --squash <branch> && git commit -m "<its subject>"`.

### Fixed

- A retried review in `--jobs` mode now starts from the commit its worktree
  was cut from. Previously a failed attempt's half-applied fixes stayed in
  the checkout, so the retry (and whatever it committed) depended on how
  many attempts had run before it. In-place reviews are unchanged: their
  tree belongs to the user and is not rewound.
- docs/CLI.md documents the six flags its intro claimed to cover but did
  not list: `--seed`, `--opencode-db`, `--no-color`, `--hot-reload`,
  `--auto-update`, and `--update-repo`.
- The help screen's environment section printed `TERM=dumb` twice.
- `gauntlet show` sanitizes journal lines before printing them. An event's
  text can carry fragments of the reviewed repository (git error output,
  merge-conflict file names); JSON escaping removes control bytes but lets
  bidi overrides through, so a hostile tree could rearrange how the replay
  reads on a terminal. Every other display surface already stripped; the
  replay path was missed.
- A review that never ran says so in the final summary (`skipped: never ran
  (unknown name or unreadable prompt)`) instead of listing itself with an
  empty reason.
- A project prompt saved with a UTF-8 byte-order mark keeps its first line:
  editors on Windows write the mark in front of every file, and left in
  place it hid the goal line descriptions are extracted from, so the
  launcher and the suggest catalog showed such reviews as undescribed.
- The suggest step fences a planted review *name* the way it already fenced
  planted goal text: a project-local name carrying `</catalog>` or a
  `RELEVANT:` marker cannot close its fence or forge a proposal line.
- Launcher input matches how terminals actually send text: backspace deletes
  one grapheme cluster rather than one code point (a dead-key accent leaves
  with its letter), filter matching normalizes Unicode so a composed
  spelling finds the same review the command line does, a tree with no
  installed agent CLI is refused with its reason instead of composing a run
  that fails on launch, and the row under the cursor explains itself in the
  status line.
- Taking the directory lock clears a dead run's note. A killed run leaves its
  lock file behind with its note in it; the flock died with the process, so
  the next run takes the lock without trouble, but anything reading the file
  was told a run is under way that ended yesterday.

## 1.6.1

### Fixed

- Untracked files no longer block `--jobs`. A review works from a commit, so
  uncommitted work in a *tracked* file is what it would miss and then collide
  with; a file git never heard of is in nobody's way. Refusing to run over one
  was a dead end besides, since the commit step stages tracked files only
  (`git add -u`) and would leave a scratch script sitting there forever. The
  run now says once which untracked files are not reviewed and gets on with
  it, and both the precondition and the commit offer name the paths that are
  actually in the way.

## 1.6.0

### Added

- A dirty tree no longer just refuses a `--jobs` run: gauntlet offers to
  commit it. The changes are the obstacle, and there is already an agent that
  writes commit messages, so it asks (`Commit them with claude first? [y/N]`),
  runs the commit step, checks with git rather than taking the agent's word,
  and starts the run. `--yes` and `--yolo` answer yes; a run with no terminal
  keeps the plain error rather than committing unattended.

## 1.5.1

### Fixed

- crush's token counts are read for real runs, not just for tests. Its
  `sessions.updated_at` column carries two units (crush writes milliseconds,
  the table's own trigger writes seconds), so the window bound matched nothing
  and every crush review reported zero. Found by running one. Fixed in toktop
  v0.4.5, which this release requires.

## 1.5.0

### Added

- Every review prompt now names the helper tools this machine actually has,
  and the ones it does not: an agent that does not know `cppcheck` is here
  reads the C by hand, and one that does not know it is absent spends budget
  finding out, or tries to install it against the rules. The binaries are
  probed once per run, in one parallel pass, and the same line appears in
  `--show-prompt`.
- The tool catalog covers the toolchains it was missing: C and C++
  (`cppcheck`, `clang-tidy`, `clang-format`, `cpplint`, `scan-build`,
  `include-what-you-use`, `bloaty`), Go (`staticcheck`, `golangci-lint`,
  `gocritic`, `errcheck`, `govulncheck`, `gofumpt`, `deadcode`), typing and
  spelling (`mypy`, `codespell`, `typos`), formatting and config (`shfmt`,
  `editorconfig-checker`, `biome`), infrastructure (`checkov`, `conftest`,
  `kubeconform`, `ansible-lint`, `dockle`), mobile (`swiftlint`, `ktlint`,
  `detekt`), and more besides. `lint-review`, `error-review`,
  `mobile-review`, and `privacy-review` have tooling for the first time.

- A graceful quit: `s` on the dashboard, or `SIGQUIT` (`Ctrl-\`) anywhere,
  stops starting reviews, lets the ones running finish, commits, pushes, and
  merges what they produced, and then ends the run. `Ctrl-C` still means stop
  now. The difference matters mid-loop: interrupting leaves agent edits
  uncommitted in the tree, and this does not.
- `crush` ([charmbracelet/crush](https://github.com/charmbracelet/crush)) is a
  supported agent: `crush run --quiet` is its non-interactive mode, which
  auto-approves the session's tool permissions itself, and `-C` continues the
  last session for `--continue-sessions`.

- `--suggest-agent gauntlet`: the suggester that is not an agent. It reads the
  tree for signals (extensions, well-known filenames, directory names) and
  proposes the reviews those files justify, with the evidence as the reason,
  in milliseconds and for no tokens. It cannot tell a toy HTTP handler from a
  payment path, which is what an agent is for; it is very good at knowing
  there is no Dockerfile. The launcher offers it beside the agents.
- crush's token counts are read from its project database (`.crush/crush.db`,
  `sessions.completion_tokens`) in builds with `-tags sqlite`, the same tag
  `--opencode-db` needs. Only sessions written during a review count toward
  it. `gauntlet doctor` says whether this build can read them. The reading
  lives in toktop's `agentusage` with every other agent's, not here (toktop
  v0.4.3).

### Changed

- Released binaries and `make build` carry the SQLite driver by default
  (`TAGS ?= sqlite`), so crush's project database is read with no flag and
  `--opencode-db` can be asked for on any release. The driver is pure Go, so
  cross-compilation is unaffected. `make build TAGS=notoktop` still drops
  transcript reading, and `make build TAGS=` now drops the driver with it,
  which is the standard-library-only build.

### Fixed

- Token counts are not lost by a short review: toktop's final read was reusing
  a cached listing that predates the transcript the review just wrote, so a
  review finishing within a second of its first output reported nothing at
  all. Fixed in toktop v0.4.3, which this release requires.
- A hot reload is a handover again, not a restart. The successor is exec'd
  with the arguments this process is running rather than the ones it was typed
  with, and every resumed directory keeps the schedule it had already
  resolved, so a run composed by `gauntlet pick` no longer reopens the
  launcher and a `--suggest` run no longer asks an agent (and the user) to
  choose a second time. A resumed run also inherits the `--semcode` index its
  predecessor built instead of spending another half hour on it.
- `journal_test.go` called `Events` with its old signature, which broke
  `go test ./internal/journal` at HEAD: one call site was missed when the API
  became a streaming visitor.

## 1.4.1

### Fixed

- `--dirs` runs the suggest step for every directory at once instead of one
  after another. Each tree has its own prompt set and its own answer, so each
  is asked on its own, and asking six trees in sequence meant up to six
  `--suggest-timeout` waits (three hours at the default) before the first
  review started. The lines from the concurrent steps carry the directory they
  belong to, the proposals are printed in directory order, and one
  confirmation covers all of them.

## 1.4.0

### Added

- The launcher shows what each review does, in a column beside the names, and
  marks the ones a reviewed tree carries itself as `[project]`. A list of 50
  names said nothing about which of them to pick.
- `/` filters the review tree by name or by description, opening what it finds
  and hiding what it does not. Every key types while the filter is open, so a
  review can be found by typing `quick` without the `q` quitting.
- The launcher refuses a run the tree cannot support instead of composing a
  command that fails on launch: concurrency above 1 needs a clean tree, and
  the reason sits under the command line until it is resolved.
- Picking no reviews reads as "all 51" in the launcher rather than "0 of 51",
  which is what the composed command has always meant by saying nothing.

### Changed

- The dashboard header names the tree being reviewed by path (`~/src/acme`),
  not by basename: several checkouts of one project share a basename, and a
  run is not the same run in each of them. The path takes the room the rest of
  the header leaves and is cut from the left, so the loop and the run state
  are never pushed off the line. The launcher header does the same.
- Agents wear their vendor's color where there is one (claude, codex, gemini,
  qwen, grok), in the launcher and in the run that follows, so a lane is
  identifiable before its name is read. Each is pulled toward its background
  until it clears the same 4.5:1 text floor as every other color here, and a
  second model of the same vendor takes the old rotation, because telling two
  lanes apart matters more than showing a brand twice.
- The launcher's job count is labelled `concurrency` and `+`/`-` change it
  from any pane. Its panels also scroll rather than spilling off a short
  terminal, and the run pane keeps its rows while the agent list gives first.
- A tagged release publishes its CHANGELOG section as the GitHub release notes
  again, and refuses to publish when that section is missing: the Go rewrite
  had switched to GitHub's auto-generated notes, which list commits for
  maintainers instead of telling consumers what changed for them. The
  workflow now also smoke-tests what it is about to publish: the host binary
  must run and report the tagged version, and its checksums.txt line must
  verify, so a broken or mislabeled build fails before any asset is uploaded.

## 1.3.0

### Added

- `--merge-into BRANCH`: after each loop, the committed work on the branch the
  reviews ran on is merged into BRANCH. Until now nothing in gauntlet targeted
  a branch by name: `--commit` commits where you are, and the worktree
  machinery merges back into where you are. The merge runs in a scratch
  checkout of the target, so your own checkout is never switched under you; it
  needs `--commit` or `--push`, since only committed work merges; and a
  conflict aborts and leaves both branches untouched. The launcher offers it
  as a branch picker beside the commit switches.
- The launcher offers `push` beside `commit`, which was the one run switch it
  could not compose. Picking it passes `--push` alone, since that already
  implies the commit step.

## 1.2.0

### Added

- `gauntlet pick`: a launcher that composes a run on screen, drawn with the
  dashboard's instruments so the two screens read as one cockpit. Reviews are
  collapsible sets with a fill meter each, `suggest` is the first choice among
  them with its own agent picker, the agent pool carries the hues it will keep
  during the run, and concurrency is metered against the CPU count. The
  command line it is building stays on screen and `enter` runs exactly that,
  through the same parser as a hand-typed one, so the launcher teaches the
  flags instead of replacing them.
- The dashboard says when a review is on its second try (`sec-review ↻2` in
  the lane): a retry reuses the lane and resets the clock, so it read as a
  review that had restarted itself.
- `f` in the dashboard narrows the feed to results, errors, and diffs, and
  back. Four agents narrating at once bury the two lines that matter; the
  filter drops nothing, so widening it brings the history back.
- `--retries N` (default 2): a review whose agent fails to launch or exits
  nonzero is rerun on the same agent, waiting 5s and doubling from there, with
  jitter so reviews that failed together do not come back together. Rate
  limits and dropped connections are what this is for. Exhausting the retries
  still falls back to a different agent, as before, and timeouts are still
  never retried.
- Journal `review_start` records carry `attempt`, so a run's retries can be
  counted from the record rather than inferred from repeated starts.

### Changed

- The README is a landing page again: what the tool is, install, one screen of
  each idea, and links out. The flag, environment, and exit-code reference
  moved to `docs/CLI.md`, worktree isolation, the run journal, and hot reload
  to `docs/RUNS.md`, and the public token-reading API into
  `docs/TOKEN_TELEMETRY.md` where the rest of that subject already lived.
- `--jobs` is documented as the per-directory pool it has always been: with
  `--dirs`, the two multiply, and `--dirs a,b,c -j 4` is up to 12 agents at
  once. The flag help, the help screen, and the README say so now.

## 1.1.0

### Added

- The directory lock names what the run holding it is doing. `.gauntlet.lock`
  carries the version, pid, run id, and the reviews in flight, so a second
  gauntlet turned away from the directory reports `another gauntlet is already
  running here: gauntlet 1.1.0 (pid 8123, run 20260825T…): config-review
  (opencode)` instead of only that something holds it.
- `--seed N`: replayable review order and agent picks. A nonzero seed replays
  the per-loop review shuffle and agent sampling; zero derives one from the
  clock as before. The effective seed rides the run-start event, so a journal
  describes its own rerun, and hex literals (`0x…`) are accepted.

### Changed

- Reading an agent's own session transcript is on by default, in source builds
  too: it costs one pure-Go dependency and is the only source of counts for
  agents that print none, so the tag now opts out (`-tags notoktop`,
  `make build TAGS=notoktop`) rather than in. `--opencode-db` needs `-tags
  sqlite` alone now that `toktop` is implied.
- `agents.json` refuses unknown keys instead of ignoring them: a misspelled
  `opt_in` or `usage` would silently change what a definition does (an
  unverified agent becoming auto-detectable, live token counts vanishing), so
  it now fails at startup like any other malformed definition.
- `code-review` treats assertion density as a lead only: each added assertion
  still needs the property or concrete path it encodes.
- `prompt-review` allows creating new prompt files when its missing-prompts
  rule calls for one, instead of banning them outright.
- `ux-review` cites WCAG 2.2 SC 2.5.8 for the touch-target minimum size.

### Fixed

- The `agents.json` examples were labeled and written as JSONC, one with a
  comment; the loader accepts only plain JSON, so copying them verbatim failed
  at startup. The examples are plain JSON now, and the README documents every
  environment variable (`GAUNTLET_HOME`, `GITHUB_TOKEN`, the color controls).
- The dashboard palette adapts to light and dark terminals instead of
  assuming a dark one, and every color that can sit behind text clears WCAG
  2.2 AA contrast on its background (SC 1.4.3). De-emphasized text such as
  pending review cells was near-invisible, the zero-rate activity marker
  rendered in a near-background tone, and unlit meter segments and the chart
  baseline now meet the 3:1 non-text floor (SC 1.4.11).
- A hot reload that crosses UTC midnight appends to the run's original journal
  file instead of splitting it across two date shards; a worktree rerun after
  a reload rebuilds a branch still at base and fails rather than destroys one
  holding commits.
- The README install one-liner maps `aarch64` to `arm64` (so arm Linux hosts
  fetch a real asset), creates `~/.local/bin` before writing into it, and asks
  for the asset by its real name: release binaries carry the version
  (`gauntlet_1.1.0_linux_amd64`), so the unversioned URL it used was a 404.
- Token counters parsed from an agent's output are bounded. A review that
  prints a usage-shaped sentinel it read somewhere else (a `9223372036854775807`
  in a source file it quoted) was believed, and the run total then overflowed
  to a large negative number with a live rate to match. Anything above a
  trillion tokens now reads as a misparse, not a measurement.

## 1.0.2

### Added

- `--opencode-db`: read opencode's session database for its token counts.
  opencode keeps sessions in SQLite rather than the JSONL every other agent
  writes, which is why it reported no tokens until now. Needs a build with
  `-tags "toktop sqlite"`; a build that cannot honor the flag says so instead
  of silently measuring nothing.

### Changed

- The transcript-reader dependency is renamed: `tokentop` is now `toktop`
  (`github.com/maci0/toktop/agentusage`), and the build tag is `toktop` to
  match. Source builds that passed `TAGS=tokentop` pass `TAGS=toktop`.
  Tracks toktop v0.4.1.

## 1.0.1

### Removed

- **Breaking:** the public `agentusage` package is gone from this module;
  transcript token reading moved to tokentop's
  `github.com/maci0/tokentop/agentusage`. Programs importing
  `github.com/maci0/gauntlet/agentusage` stop compiling against v1.0.1:
  migrate the import to tokentop's package (renamed again to
  `github.com/maci0/toktop/agentusage` in 1.0.2). This removal shipped in a
  patch release without an entry here at the time; it belongs under a major
  bump per the contract above, and is recorded now rather than left silent.

### Fixed

- Worktree bookkeeping is serialized so parallel reviews cannot strand
  branches or stray checkouts after an interrupt: git validates every
  registered worktree on each add or remove, and two at once could read
  another's half-deleted metadata.
- `make build TAGS=` produces a standard-library-only binary that reads only
  what agents print; released binaries carry the reader as before.

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
- `gauntlet version` and `gauntlet help` print the same screens as `--version`
  and `--help`.
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
- Short flags: `-c` (commit), `-p` (push), `-s` (suggest), `-1` / `--once`.
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

- `code-review` now flags fabricated or discarded type evidence: code that
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
  read-only calls, during discovery (so even `--list`/`--dry-run`), in the
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
  `--timeout`), then exits, for debugging project-local prompts.
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
  startup, including dependency checks in the readiness probe), graceful
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
The bundled reviews (names are API): `a11y-review`, `api-review`,
`arch-review`, `build-review`, `cli-review`, `code-review`,
`concurrency-review`, `config-review`, `db-review`, `deps-review`,
`design-review`, `doc-review`, `dst-review`, `error-review`,
`functionality-review`, `fuzz-review`, `i18n-review`, `infra-review`,
`llm-review`, `minimalism-review`, `mobile-review`, `o11y-review`,
`perf-review`, `pkg-review`, `privacy-review`, `release-review`,
`sdk-review`, `sec-review`, `slop-review`, `test-review`, `uislop-review`,
`ux-review`.
