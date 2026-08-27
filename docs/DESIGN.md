# gauntlet design

The Go implementation of gauntlet: run ~50 specialized review prompts through installed
AI coding agents, which apply fixes directly to the working tree.

The Python original is a single 2700-line sequential script. This port keeps
its trust model and prompt semantics, and changes four things: reviews can run
in parallel with git-level isolation, agent output is normalized into
structured events instead of raw bytes, every run is journaled, and the binary
can replace and reload itself while a loop is running.

## Goals

1. Same review semantics as the Python original: same prompts, same injected
   rules, same containment, same exit codes.
2. Parallel where it is safe: across directories always, and inside one
   directory only with a worktree per review and a merge step.
3. A dashboard that reads like an instrument, not a log tail.
4. Fast: sub-100ms to first useful output, no measurable runner overhead
   against agent wall time.
5. One static binary that can update and reload itself.

## Non-goals

- Sandboxing the agents. Containment is prompt-level and process-level
  (new session, no stdin, hard timeout), exactly as in the original. Running
  untrusted repos still means running them in a container.
- Reimplementing agent CLIs. The runner is a scheduler and a screen.

## Package layout

| Package | Responsibility |
|---|---|
| `cmd/gauntlet` | flag parsing, mode dispatch, exit codes |
| `internal/agent` | agent specs, PATH resolution, command construction, doctor inventory |
| `internal/prompt` | embedded prompts, project prompt discovery, sets, composition |
| `internal/normalize` | agent output noise reduction and line classification |
| `internal/gitx` | hardened git invocation, worktree line stats |
| `internal/runner` | scheduler, worktrees, timeouts, lock, commit step, events |
| `internal/journal` | the JSONL run log under `~/.gauntlet` |
| `internal/gauntlethome` | the one resolver of the state root (`GAUNTLET_HOME`, else `~/.gauntlet`), shared by the journal and agent definitions |
| `internal/streamjson` | envelope-agnostic parser for agents' machine-readable output |
| `internal/ui` | bubbletea dashboard |
| `internal/selfupdate` | release check, verified download, atomic replace, re-exec |
| `internal/humanize` | one formatter for durations and counts, shared by all of them |
| `internal/fuzzy` | typo-tolerant name matching, behind every "did you mean" hint |
| `internal/runner/usage*.go` | the bridge to toktop's transcript reading, on unless `-tags notoktop` |

Dependency direction is strictly downward: `runner` imports `agent`,
`prompt`, `normalize`, `gitx`, and `streamjson`; `ui` imports
`runner`'s event types plus the shared `normalize` line kinds, `humanize`
formatters, and the `fuzzy` fold behind the picker's filter, and nothing
else. `prompt` imports `gitx`, so project discovery's
one git call (check-ignore) uses the same hardened resolver and safe config
as every other git invocation. `agent` and `prompt` import `fuzzy`, so a
mistyped review or agent name gets the same suggestion everywhere.
Nothing imports `ui`, so the loop runs headless with zero TUI cost.

## External dependencies

Seven direct modules and one deliberate transitive; everything else is
standard library. The default is no
new dependency: each row earns its place by doing something the standard
library cannot, and each was kept small on purpose.

| Module | Why it exists | Contained by |
|---|---|---|
| `charmbracelet/bubbletea` | dashboard event loop | imported by `internal/ui` only |
| `charmbracelet/lipgloss` | dashboard styling and adaptive color pairs | imported by `internal/ui` only |
| `muesli/termenv` | color-profile control for `--no-color`; lipgloss v1's profile API takes a termenv profile, so setting it means importing the type | `internal/ui.SetMonochrome` only |
| `maci0/toktop` | transcript token counts for agents that print none | build tag `-tags notoktop` drops it entirely |
| `modernc.org/sqlite` | crush/opencode keep counters in databases, not transcripts | pulled in through toktop; `TAGS=` builds drop it; pure Go, so `CGO_ENABLED=0` cross-compilation is unaffected |
| `rivo/uniseg` | grapheme-cluster width, truncation, and segmentation so CJK and emoji render in alignment | display paths in `internal/ui` only |
| `golang.org/x/text` | NFC normalization under fuzzy matching, prompt-name handling, and the picker's filter | `internal/fuzzy`, `internal/prompt`, `internal/ui` |
| `golang.org/x/term` | terminal detection and size before the TUI starts | `cmd/gauntlet` only |

What the hand-rolled packages replace: `humanize`, `streamjson`, and `fuzzy`
exist because a general library for each would cost more in weight and
supply-chain surface than the few hundred lines they stand in for.

Supply-chain posture, and what any new dependency inherits as obligations:

- Every module is pinned to an exact version and verified against `go.sum`
  at build time. GitHub Actions are pinned by commit SHA, not tag.
- Each release ships `checksums.txt` (the contract `gauntlet update` verifies
  downloads against) and `sbom.txt`, a per-binary module inventory from
  `go version -m`.
- A scheduled `govulncheck` job, plus one on every `go.mod`/`go.sum` change,
  reports vulnerabilities reachable from this code; dependabot owns version
  bumps, the scan owns advisories.
- Licenses are MIT or BSD-3-Clause throughout, compatible with this repo's
  AGPL-3.0-or-later. A dependency's license is checked before adoption,
  not after.
- The sqlite driver tracks upstream SQLite closely; when auditing, read the
  `SQLITE_VERSION` constant in its `lib/sqlite.go`. Gauntlet only runs
  self-constructed queries against agent-owned database files, never SQL
  from untrusted input.
- Major versions of the Charm stack (bubbletea/lipgloss v2, February 2026)
  are adopted through a dedicated migration pass, never a drive-by bump:
  the v2 View API changes wholesale, and the v1 line continues to receive
  fixes in the meantime.

## Concurrency model

Two agents editing one working tree corrupt each other's work: one rewrites a
file the other is mid-way through fixing, one's verification run sees the
other's half-applied change, and neither diff is attributable afterwards. So
the unit of safe parallelism is **the directory**, not the agent.

```
--dirs ~/a ~/b ~/c
    ┌─ worker(~/a) ─ lock ─ sequential loop over reviews ─┐
    ├─ worker(~/b) ─ lock ─ sequential loop over reviews ─┼─► events
    └─ worker(~/c) ─ lock ─ sequential loop over reviews ─┘
```

- One worker per target directory, each with its own `.gauntlet.lock`, git
  baseline, review queue, and lane of the event stream. This is where the
  parallelism lives, and it is always safe: distinct trees, distinct agents.
- Inside one directory, reviews run **one at a time** by default, exactly as
  the Python original does, editing the working tree in place.
- `--jobs N` (N > 1) turns on **isolated parallel reviews**, described below.
- A review that fails to launch or exits non-zero is retried on a different
  agent, and again on further agents until the pool is exhausted. Timeouts are
  not retried.
- Cancellation is a `context.Context` per review, plus process-group kill
  (SIGTERM, then SIGKILL after 10s) exactly as the original.
- Events are published on one buffered channel per run and fanned out to the
  logger, the journal, and the TUI. Publishing never blocks the scheduler:
  output events may be dropped when a subscriber's buffer is full; results
  are never dropped.

### Isolated parallel reviews (`--jobs N`)

Concurrent agents in one working tree corrupt each other, so the runner does
not allow it. Parallelism inside a directory is granted only with isolation:

```
baseline commit
   ├── git worktree add -b gauntlet/<run>/sec-review   .gauntlet/worktrees/…
   ├── git worktree add -b gauntlet/<run>/perf-review  .gauntlet/worktrees/…
   └── git worktree add -b gauntlet/<run>/doc-review   .gauntlet/worktrees/…
         agents run concurrently, each in its own checkout
   ↓
   one commit per review (runner-authored, no AI attribution)
   ↓
   serialized merge --squash back into the original branch
```

Rules the runner enforces:

1. **Git required, tree clean.** A branch is cut from a commit, so uncommitted
   work would be invisible to every review and then collide with the merges.
   `--jobs N` on a dirty tree is a usage error, not a warning.
2. **One worktree per review**, under `.gauntlet/worktrees/`, added to
   `.git/info/exclude` so the checkouts never appear as untracked files.
3. **The runner commits, not the agent.** Agents remain forbidden to run git
   (unchanged containment). After a review, the runner stages and commits its
   worktree in one commit. Nothing to commit means nothing merged.
4. **Merges are serialized** on the main tree, in completion order. A
   conflicting merge goes to the conflict step (below) and, if that does not
   land it, is aborted with its branch kept, named after the review, so the
   work can be inspected or merged by hand. Conflicts are reported as their
   own outcome in the summary and the journal, never silently dropped.
5. **Cleanup on the way out**: worktree removed, merged branches deleted,
   `git worktree prune` run. Unmerged branches survive on purpose.
6. Per-review line stats come from the review's own commit, so they stay
   exact under parallelism (unlike a shared-tree diff, which cannot be
   attributed).
7. `--commit`/`--push` still forces a quiescent point: all lanes drain and
   merge before the commit step runs.
8. **Worktrees are cut from the current tip**, not from the loop's starting
   commit: lanes refill for as long as the loop runs, and a stale base turns
   every later merge in a loop into a conflict.
9. **The conflict step** (`--resolve-conflicts`, on by default) replays a
   refused branch into a scratch checkout of the tip, hands the marked files
   to one agent launch, and merges what comes back. It runs under the merge
   lock, so the tip is fixed for its duration; it commits nothing that still
   carries conflict markers; and every failure path leaves exactly what a
   plain conflict leaves. The agent sees only the conflicted files and is
   forbidden to run git, like every other agent this tool launches.

## Speed

Agent wall time dominates by three orders of magnitude, so "fast" means the
runner never adds to it and never makes the user wait to see state.

- **Prompts are embedded** (`go:embed`), so the bundled set costs no syscalls
  and no prompt directory has to exist. Project prompt discovery walks the tree
  once, skipping the same directories as the original, and asks git about
  ignored files in a single batched call rather than one per file.
- **PATH resolution is memoized** per process. The original ran `shutil.which`
  repeatedly; `doctor` alone did hundreds of stat calls serially. Here the
  inventory probes every candidate binary in parallel with a bounded pool.
- **Git stats are sampled, not polled per review.** One `git diff --shortstat`
  plus one `ls-files -o` per sample, shared by all lanes behind a mutex, with
  a minimum interval between samples.
- **Output is streamed, never buffered whole.** Each lane reads into a fixed
  ring (64 KiB tail for token parsing) and pushes normalized lines onward.
  Nothing accumulates per-review output in memory.
- **The TUI redraws on change**, capped at 10 fps, and renders only the rows
  that fit. Braille charts precompute their style cache per frame.
- Startup does no network I/O. Update checks are explicit or run in the
  background, never on the critical path to the first review.

## Output normalization

Agent CLIs are chatty and terminal-oriented. The original stripped ANSI codes,
dropped spinner frames, and collapsed repeated progress verbs. This port keeps
that and adds what a dashboard needs:

1. Strip CSI/OSC/DEC escapes and control characters; honor `\r` by keeping
   only the last segment of a rewritten line (that is what the terminal would
   have shown).
2. Drop lines that carry no information: empty, spinner-only, box-drawing-only.
3. Collapse consecutive duplicates into one line with a repeat count.
4. Collapse consecutive progress lines of the same verb, as the original did.
5. Rate-limit per lane. A burst beyond the cap is summarized
   (`… N lines suppressed`) rather than flooding the feed.
6. Strip the decorative left gutter agents draw down their tool output
   (`|` for opencode, `⏺`/`⎿` for claude, `•` for codex) and judge what is
   left, so a gutter line with nothing after it disappears while its content
   survives. A pipe inside a command is not a gutter and is left alone.
7. Classify each surviving line as `plain`, `tool`, `error`, `progress`,
   `result`, a unified-diff line, or model reasoning, so the dashboard can
   color it and the summary can pick out `RESULT:` lines without a second
   pass.
8. Read token counters out of the stream as they go, and publish a usage event
   whenever the number grows. That is what makes the dashboard's tok/s a
   measurement rather than an average computed at the end. Agents that report
   nothing produce no rate, never a guess.

Normalization is pure and table-tested. `--raw` bypasses the classification,
collapsing, and rate limiting, but not the safety floor: every line that
reaches a terminal or log passes through `normalize.Display`, which strips
escapes and control characters while leaving visible text alone. Agent output
is untrusted, so no mode echoes it byte-for-byte. Stream events (thinking
lines) get the same treatment at `emitStream`, plus the width cap.

## Self-update and hot reload

Two separate mechanisms that compose:

**Self-update** (`gauntlet update`, or `--auto-update` checking in the
background during a long run) fetches the latest release for this `GOOS/GOARCH`,
verifies its SHA-256 against the release's `checksums.txt`, writes it next to
the current binary, and renames it into place atomically. A failed
verification leaves the running binary untouched. Nothing is executed from
the download before verification. Every release also ships `sbom.txt`, a
module inventory produced by `go version -m` over each built binary: anyone
auditing a release can read which module versions and hashes shipped without
rebuilding it. The binaries themselves are bit-reproducible: `-trimpath`
strips build paths, `-buildvcs=false` keeps git metadata (revision, commit
time, dirty flag) out, and nothing embeds a timestamp, so the same source
built from a clone, a source tarball, or a dirty tree — in a different
directory, under a different locale and timezone — yields identical bytes.
The one input that cannot be normalized is the toolchain: a binary records
the compiler version, so reproducing a release byte-for-byte means checking
out the tag with a clean tree and the Go version the `go` line in `go.mod`
names. `make repro` proves the rest on every CI run by building twice and
comparing; the second build strips the locale the Makefile pins, so it runs
under the host's ambient one.

**Hot reload** watches the running executable's inode, size, and mtime every
few seconds, and requires two identical readings before acting so a
half-written binary is never executed. When it changes (self-update,
`make install`, a fresh `go build`) the swap proceeds like this:

1. Every runner is asked to stop softly. A soft stop never signals an agent:
   reviews in flight run to completion, including their commit and merge.
2. Each directory's unfinished queue, results, loop count, and commit tallies
   are written to `~/.gauntlet/state/<run-id>.json`.
3. The journal is flushed and closed **without** an index row, and the
   directory locks are released.
4. `execve` replaces the process with the new binary, same pid, same argv,
   same terminal, plus `GAUNTLET_STATE`.
5. The successor seeds its stats from the handoff, resumes the interrupted
   loop from its remaining reviews, subtracts finished loops from
   `--max-loops`, keeps the original start time for `--runtime`, and appends
   to the same journal file.

The result is one run, one run id, one index row, one summary, spanning both
binaries. What a reload costs is latency: it waits for the reviews in flight,
which can take up to `--timeout`.

## Choosing reviews without an agent

`--suggest-agent gauntlet` answers the triage question from evidence on disk,
in milliseconds and for no tokens. What it collects in one pass:

- **What the tree is made of**, by count rather than presence. A language earns
  its reviews at three files or a twentieth of the tree, so one stray
  stylesheet in a Go repository is not a frontend.
- **What the files say inside.** The head of each source file (4 KB, up to
  2000 files) is searched for a fixed table of markers: `net/http`, `asyncio`,
  `sqlalchemy`, `prometheus`, `bcrypt`, `argparse`, `bubbletea`. What a
  codebase imports is a fact about it; a directory name is a guess.
- **What is missing.** No tests, no documentation, no CI: absence is the
  strongest argument for the review that would fix it, and presence-only rules
  said the opposite.
- **What is alive.** `git log --since=90.days --name-only` weights a language
  by whether anyone is still editing it. Without commit history the weighting
  is skipped rather than guessed.
- **What happened here before.** The journal already records each review's outcome
  per directory. A review that has finished here several times without changing
  a line is demoted; one that keeps landing changes is promoted. It is the only
  signal that improves with use.
- **What a prompt declares.** A `Signals:` line in a project's own review makes
  it reachable at all (see RUNS.md); the built-in rules only know built-in
  names.

Each rule contributes weight rather than a yes, the reviews are ranked by the
total, and what does not clear the floor is not proposed. The tree is listed by
`git ls-files` when there is a repository, so the project's own ignore rules
decide what counts as source; a plain walk with a skip list is the fallback.

`scripts/suggest-calibrate.py` scores the result against what agents picked in
past runs. It is a reference, not ground truth: several of these rules are
meant to diverge from a model's opinion.

## Run journal

Every run writes JSONL under `~/.gauntlet` (`GAUNTLET_HOME` overrides):

```
runs/YYYY-MM-DD/<run-id>.jsonl   the event stream, one JSON object per line
index.jsonl                      one summary line per finished run
state/<run-id>.json              hot-reload handoff, removed after pickup
```

Design points:

- The journal is **another subscriber to the same event bus** the dashboard
  reads. No separate instrumentation path exists to drift out of sync.
- Date sharding keeps one directory listing small; the flat index makes "what
  did I run last week" a tail rather than a tree walk.
- Agent output (`output` events) and the live usage ticks (`usage` events)
  are **not** journaled. They are large, they are reconstructible from the
  agents' own logs, and the results are what anyone reads later. Everything
  else is.
- Journaling is never load-bearing: a journal that cannot be opened degrades
  to a warning. A nil journal is a working no-op, so no caller branches on it.
- A hot reload appends to the same file and writes **one** index row, from the
  successor, covering the whole run.

## Dashboard

Follows the TMOG dashboard rules: a cockpit, not a report.

- The whole state fits one screen: lanes, review grid, throughput, feed.
- Live data is bright; grid, borders, and chrome stay dim.
- One hue per agent, used for its lane, its rows, and its trace everywhere.
- Colors adapt to the terminal's background: Catppuccin Latte on light
  terminals, Mocha on dark ones. The pairs are pinned by test to WCAG 2.2 AA:
  text at 4.5:1 (SC 1.4.3) and instrument strokes such as unlit meter
  segments and the chart baseline at 3:1 (SC 1.4.11). Borders are decorative
  and exempt.
- Meters are quantized segments with a visible unlit remainder.
- The throughput chart is braille (2x4 dots per cell) and hugs the right
  edge, with a current-value marker at the live end.
- Missing data shows as missing (`~`, `n/a`), never as zero or an interpolation.

## Trust model (unchanged from the original)

- Prompts are read with `O_NOFOLLOW`, size-capped, regular files only.
- Agent binaries resolve on a PATH with cwd-relative entries removed.
- Git runs with `core.fsmonitor`, `core.hooksPath`, `diff.external`, and the
  pager forced empty, so a hostile repo's config cannot execute code.
- Untrusted text (prompt names, descriptions, agent output) is sanitized of
  control and bidi-formatting characters before display.
- A `flock` on `.gauntlet.lock` prevents concurrent runs in one directory.
