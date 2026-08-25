# What happens during a run

A run is a loop over reviews. Each review is one agent invocation in one
directory, with a per-review timeout and a per-run wall clock. This page
covers what surrounds that: isolation, the record it leaves, and how a running
loop takes a new binary.

## Parallel reviews

One agent editing a working tree is safe. Several are not: they overwrite each
other's edits, each one's verification step sees the others' half-applied
changes, and no diff can be attributed afterwards. So parallelism inside one
repository is only granted with isolation:

```sh
gauntlet -j 4 -a mixed --once
```

(The fan-out and merge-back are drawn in the [README](../README.md#how-a-run-works).)


- Requires git and a **clean working tree** (a branch is cut from a commit, so
  uncommitted work would be invisible to every review).
- Each review gets `git worktree add -b gauntlet/<run>/<review>` under
  `.gauntlet/worktrees/`, excluded from git status via `.git/info/exclude`.
- The runner (never the agent) commits each worktree, then merges the branches
  back into your branch one at a time, `--no-ff`.
- A conflicting merge is **aborted, and its branch is kept**, named after the
  review, so the work can be inspected or merged by hand. Conflicts are
  reported as their own outcome and make the run exit nonzero.
- Merged branches and their checkouts are cleaned up; unmerged ones survive.

Without `--jobs`, reviews run one at a time and edit the tree in place, exactly
like the Python original, dirty tree and all.

`--jobs` counts per directory, not per run: every directory in `--dirs` gets
its own loop, its own lock, and its own pool of `N`. `--dirs a,b,c -j 4` is up
to 12 agents at once, so size it against your CPU count and your API limits,
not against the review list.

## Several directories at once

`--dirs a,b,c` (also `--target-dirs`) reviews each tree in its own goroutine,
with its own lock, its own baseline, and its own `--jobs` pool, so the agents
running at once are `jobs x directories`.

Review discovery is per directory too: a tree carrying its own `*-review.md`
files has a different set from its neighbor. `--suggest` follows from that, so
the suggest step runs once per directory, and those steps run together rather
than in sequence: their log lines carry the directory they belong to, the
proposals are printed in directory order, and one confirmation covers all of
them.

## Committing, and where the work lands

Reviews edit the working tree. What happens to those edits is three separate
choices:

- `--commit` has an agent write a message and commit **on the branch you are
  on**. `--push` does that and pushes it. Neither creates a branch, and
  neither merges anything.
- `--jobs N` is the only thing that branches: each review works on
  `gauntlet/<run>/<review>`, and the runner merges those back into **the
  branch you are on**, one at a time, `--no-ff`. Nothing here targets `main`
  by name; if you are on `feature-x`, the work lands on `feature-x`.
- `--merge-into BRANCH` is the step after that: once a loop's work is
  committed, the branch you are on is merged into BRANCH. It runs in a scratch
  checkout of BRANCH under `.gauntlet/worktrees/`, so the branch the reviews
  are running on stays checked out and the run stays watchable. It needs
  `--commit` or `--push`, because only committed work can merge, and it
  refuses (rather than reporting a merge that moved nothing) if the tree is
  still dirty. A conflict aborts the merge and leaves both branches where they
  were, for a human to resolve.

If `--jobs` refuses because the tree is dirty, gauntlet offers the step rather
than only naming it: it asks whether to commit the changes with an agent
first, and tries again if the tree ends up clean. `--yes` and `--yolo` are
that consent; a run with no terminal keeps the plain error rather than
committing unattended.

The merge is local. Pushing the branch that was merged into is deliberately
not part of it: that is a decision about a shared branch, and it stays yours.

## Stopping a run

Three ways, and they mean different things:

| How | What happens |
|---|---|
| `s` on the dashboard, or `SIGQUIT` (`Ctrl-\`) | Graceful: no new review starts, the ones running finish, their work is committed, pushed, and merged as the flags ask, and the run then exits normally. Reviews not yet started are dropped. |
| `Ctrl-C`, `SIGINT`, `SIGTERM` | Stops now: running agents are killed by process group, and the run exits 130. A second one force-kills. |
| `--once`, `--max-loops N`, `--runtime DUR` | Planned endings, decided before the run starts. |

The graceful stop is the one to reach for when a loop is halfway through and
the tree should not be left with uncommitted agent edits: it is the only stop
that still runs the commit and merge steps.

## Run journal

Every run is recorded under `~/.gauntlet` (override with `GAUNTLET_HOME`):

```
~/.gauntlet/
  runs/2026-08-25/20260825T131500Z-a91f.jsonl   the full event stream
  index.jsonl                                    one summary line per run
  state/                                         hot-reload handoff files
```

```sh
gauntlet runs --limit 10        # recent runs: when, how long, pass/fail
gauntlet show <run-id>          # replay one run's events
jq 'select(.ev=="review_end")' ~/.gauntlet/runs/*/*.jsonl   # or use your own tools
```

Events are one JSON object per line (`run_start`, `loop_start`,
`review_start`, `review_end`, `merge`, `commit`, `loop_end`, `reload`,
`run_end`, plus runner log lines). Agent output and the live token ticks are
not journaled: they are large and reconstructible, and the results are what
matter later.

## Updating and hot reload

```sh
gauntlet update --check   # what the latest release is
gauntlet update           # download, verify SHA-256, replace this binary
gauntlet --auto-update    # check periodically during a long run
```

The download is verified against the release's `checksums.txt` before it
replaces anything; a mismatch leaves the running binary untouched.

Hot reload is on by default (`--hot-reload=false` disables it). When the
executable on disk changes, by `gauntlet update`, `make install`, or a fresh
`go build`, the swap is seamless:

- **No agent is killed.** Reviews already running finish normally, including
  their commit and merge. Only then does the process hand over.
- **No work is repeated.** The unfinished part of the current loop is handed
  to the successor, which finishes that loop rather than starting it over.
- **Nothing is lost.** Run id, start time, per-review results, per-agent
  totals, and loop budget cross the gap, so the final summary and the journal
  describe the whole run as one. The process keeps its pid and terminal:
  `execve`, not a respawn.

The cost is latency, not correctness: the swap waits for the reviews in flight,
which can take up to `--timeout`.
