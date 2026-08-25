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
| `internal/streamjson` | envelope-agnostic parser for agents' machine-readable output |
| `internal/ui` | bubbletea dashboard |
| `internal/selfupdate` | release check, verified download, atomic replace, re-exec |
| `internal/humanize` | one formatter for durations and counts, shared by all of them |
| `internal/runner/usage*.go` | the optional bridge to toktop's transcript reading, chosen by build tag |

Dependency direction is strictly downward: `runner` imports `agent`,
`prompt`, `normalize`, `gitx`, and `streamjson`; `ui` imports
`runner`'s event types plus the shared `normalize` line kinds and `humanize`
formatters, and nothing else. `prompt` imports `gitx`, so project discovery's
one git call (check-ignore) uses the same hardened resolver and safe config
as every other git invocation. Nothing imports `ui`, so the loop runs headless
with zero TUI cost.

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
   serialized merge --no-ff back into the original branch
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
   conflicting merge is aborted and its branch is kept, named after the
   review, so the work can be inspected or merged by hand. Conflicts are
   reported as their own outcome in the summary and the journal, never
   silently dropped.
5. **Cleanup on the way out**: worktree removed, merged branches deleted,
   `git worktree prune` run. Unmerged branches survive on purpose.
6. Per-review line stats come from the review's own commit, so they stay
   exact under parallelism (unlike a shared-tree diff, which cannot be
   attributed).
7. `--commit`/`--push` still forces a quiescent point: all lanes drain and
   merge before the commit step runs.

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
rebuilding it.

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
