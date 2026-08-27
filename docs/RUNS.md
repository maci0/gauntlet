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


- Requires git and no **uncommitted changes to tracked files** (a branch is cut
  from a commit, so that work would be invisible to every review and then
  collide with its merge). Untracked files do not block the run: they are in
  nobody's way, and the run says once that they are not reviewed.
- Each review gets `git worktree add -b gauntlet/<run>/<review>` under
  `.gauntlet/worktrees/`, excluded from git status via `.git/info/exclude`.
- The runner (never the agent) commits each worktree, then lands the branches
  on your branch one at a time, squashed: one commit per review, no merge
  nodes. The commit is authored by you, with your git identity, and its
  subject is the one the review printed for its own change (`SUBJECT: fix: …`
  in the output protocol), so the history reads like the project's, not like a
  tool's. A review that prints none gets `chore(<review>): apply review
  findings`.
- With `--push`, each review is pushed as it lands rather than all of them at
  the end, so a long run publishes as it goes.
- Each worktree is cut from the tree's **current tip**, not from where the
  loop began, so a review that waited hours for a lane still starts on what
  has landed since.
- A conflicting merge is **handed to an agent**: the conflict is replayed in a
  scratch checkout cut from the tip, one agent launch resolves the marked
  files, and the result is merged. The merge lock is held throughout, so the
  tip cannot move under it. `--resolve-conflicts=false` skips this.
- What the agent does not resolve (markers left in place, a launch that
  failed, a timeout) leaves the merge **aborted and the branch kept**, named
  after the review, so the work can be inspected or merged by hand. Unresolved
  conflicts are reported as their own outcome and make the run exit nonzero.
- A review that is retried (`--retries`, after a launch failure or a nonzero
  exit) starts over from the commit its worktree was cut from: whatever the
  failed attempt left behind is reset away, so attempt N+1 sees exactly what
  attempt N saw. In-place reviews (`--jobs` unset) retry in the live tree,
  which belongs to you and is not rewound.
- Merged branches and their checkouts are cleaned up; unmerged ones survive.

Without `--jobs`, reviews run one at a time and edit the tree in place, exactly
like the Python original, dirty tree and all.

`--jobs` counts per directory, not per run: every directory in `--dirs` gets
its own loop, its own lock, and its own pool of `N`. `--dirs a,b,c -j 4` is up
to 12 agents at once, so size it against your CPU count and your API limits,
not against the review list.

## Stacked pull requests

Stack mode keeps the original checkout fixed and advances one separate
worktree through a linear series of branches. Run it against a repository
where the selected remote accepts your branch pushes:

```sh
gauntlet -r quick --stacked-prs --pr-base main
```

The first changed review branches from `main` and opens a PR to `main`. The
next changed review branches from that commit and opens a PR to the preceding
review branch. Every agent therefore sees all earlier fixes, while every PR's
comparison contains only that review's commit.

| Layer | Branch starts at | PR base |
|---|---|---|
| first changed review | `main` | `main` |
| second changed review | first review branch | first review branch |
| third changed review | second review branch | second review branch |

The mode is one ordered, sequential pass; it does not shuffle reviews or use
extra `--jobs` lanes. One persistent checkout lives under
`.gauntlet/worktrees/` for the run, then is removed. Local and remote review
branches survive because open PRs need them. The branch checked out in the
original directory never moves and no stack branch is merged automatically.

Before launching an agent, gauntlet fetches the selected branch from
`--push-remote` and cuts the isolated worktree directly from that commit. It
does not pull, update a local branch, or read uncommitted files from the
original checkout. When that checkout has tracked or untracked changes,
gauntlet names them, explains that they will not be reviewed or included in
the PRs, and asks before continuing; `--yes` provides the same consent for an
unattended run. It also requires Git, `gh` authentication, repository access,
and a successful dry-run new-branch push. The remote URL selects the GitHub
repository in which `gh` creates the PRs.

A review with no file changes leaves no branch or PR; the next review stays on
the last changed layer. A failed agent is reset to its layer's base before a
retry, and an exhausted failure is discarded before the next review starts.
A commit, push, or PR failure is different: publication stops the stack, keeps
any committed branch, and starts no later review.

Branch names are deterministic from the initial base commit, schedule
position, and review name. Before creating a PR gauntlet searches all PR
states for that exact head/base pair. A repeated run reuses it; a hot reload
walks the published prefix and resumes from the unfinished suffix. A branch
that was committed or pushed before a stopped PR call is published on the
next invocation instead of rerunning its agent.

## Several directories at once

`--dirs a,b,c` (also `--target-dirs`) reviews each tree in its own goroutine,
with its own lock, its own baseline, and its own `--jobs` pool, so the agents
running at once are `jobs x directories`.

A review a project carries can say what it keys on, which is what makes it
reachable by `--suggest-agent gauntlet`: the built-in rules only know built-in
names. One line anywhere in the prompt does it, and any single match proposes
the review:

    Signals: ext:.zig, name:build.zig, path:src/plugins, mark:comptime

`ext` is a file extension, `name` a file's base name, `path` a directory or
relative path, and `mark` a substring found near the top of a source file. The
line comes from the reviewed tree, so it is parsed strictly: those four kinds,
at most 12 tokens, 40 characters each, and a charset with no room for
structure. Anything else on the line is dropped.

Review discovery is per directory too: a tree carrying its own `*-review.md`
files has a different set from its neighbor. `--suggest` follows from that, so
the suggest step runs once per directory, and those steps run together rather
than in sequence: their log lines carry the directory they belong to, the
proposals are printed in directory order, and one confirmation covers all of
them.

## Committing, and where the work lands

Reviews edit a checkout. What happens to those edits is four separate
choices:

- `--commit` has an agent write a message and commit **on the branch you are
  on**. `--push` does that and pushes it. Neither creates a branch, and
  neither merges anything.
- `--jobs N` is the only thing that branches: each review works on
  `gauntlet/<run>/<review>`, and the runner merges those back into **the
  branch you are on**, one at a time, squashed. Nothing here targets `main`
  by name; if you are on `feature-x`, the work lands on `feature-x`.
- `--merge-into BRANCH` is the step after that: once a loop's work is
  committed, the branch you are on is merged into BRANCH. It runs in a scratch
  checkout of BRANCH under `.gauntlet/worktrees/`, so the branch the reviews
  are running on stays checked out and the run stays watchable. It needs
  `--commit` or `--push`, because only committed work can merge, and it
  refuses (rather than reporting a merge that moved nothing) if the tree is
  still dirty. A conflict aborts the merge and leaves both branches where they
  were, for a human to resolve.
- `--stacked-prs` never edits or merges into the checked-out branch. It owns
  one separate worktree, commits and pushes each changed review branch, and
  opens the linear PR chain described above.

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
| `s` on the dashboard, or `SIGQUIT` (`Ctrl-\`) | Graceful: no new review starts, the ones running finish, and their work is committed, pushed, published as a PR, or merged as the mode asks. The run then exits normally and reviews not yet started are dropped. |
| `Ctrl-C`, `SIGINT`, `SIGTERM` | Stops now: running agents are killed by process group, and the run exits 130. A second one force-kills. |
| `--once`, `--max-loops N`, `--runtime DUR` | Planned endings, decided before the run starts. |

The graceful stop is the one to reach for when a loop is halfway through and
the tree should not be left with uncommitted agent edits: it is the only stop
that still runs the configured commit, publication, and merge steps.

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
`run_end`, plus runner log lines). `review_start` and `review_end` carry
`prompt_sha256`, the SHA-256 of the prompt text that launch was composed
from, so an output stays attributable to exact words after the prompt file
has changed or disappeared. Agent output and the live token ticks are
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
`go build`, the swap costs the run nothing:

- **No agent is killed.** Reviews already running finish normally, including
  their commit, publication, and merge work. Only then does the process hand over.
- **No work is repeated.** The unfinished part of the current loop is handed
  to the successor, which finishes that loop rather than starting it over.
- **Nothing is lost.** Run id, start time, per-review results, per-agent
  totals, and loop budget cross the gap, so the final summary and the journal
  describe the whole run as one. The process keeps its pid and terminal:
  `execve`, not a respawn.

The cost is latency, not correctness: the swap waits for the reviews in flight,
which can take up to `--timeout`.
