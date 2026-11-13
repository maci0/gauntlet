# What happens during a run

A run loops over reviews, each with a per-review timeout and optional retries
or agent fallback. The run's elapsed-time budget uses a monotonic clock. This
page covers isolation, commits and publication, stopping and recovery, the run
journal, and binary updates. See [CLI.md](CLI.md) for the flag reference and
[DESIGN.md](DESIGN.md) for implementation details.

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
- `--jobs N` creates **N persistent lane worktrees** under
  `.gauntlet/worktrees/`, excluded from git status via `.git/info/exclude`.
  The schedule is split up front: review `i` goes to lane `i % N`. A lane
  runs its assigned reviews in order and cannot take work from another lane.
  Each review still gets its own branch; the lane directory stays put so later
  reviews in that lane can reuse the agent's prompt cache.
- The runner (never the agent) commits each review, then lands the branches
  on your branch one at a time, squashed: one commit per review, no merge
  nodes. The commit uses the identity Git resolves for the repository (local
  `user.name`/`user.email`, then global configuration), and its
  subject is the one the review printed for its own change (`SUBJECT: fix: …`
  in the output protocol), so the history reads like the project's, not like a
  tool's. A review that prints none gets a subject naming the files it
  touched (`chore: add helper.go`), never a line about the review itself.
- With `--push`, each review is pushed as it lands rather than all of them at
  the end, so a long run publishes as it goes.
- Each review's branch is cut from the tree's **current tip**, not from where
  the loop began, so a review that waited for a lane still starts on what has
  landed since. After a merge the lane advances to that tip.
- A conflicting merge is **handed to an agent**: the conflict is replayed in a
  scratch checkout cut from the tip, one agent launch resolves the marked
  files, and the result is merged. The merge lock is held throughout, so the
  tip cannot move under it. `--resolve-conflicts=false` skips this. A conflict
  with more files than the prompt will name, or with no path safe to put in
  the prompt, is left aborted with the branch kept, the same as a resolver
  that did not finish.
- What the agent does not resolve (markers left in place) leaves the merge
  **aborted and the branch kept**, named after the review, so the work can be
  inspected or merged by hand. So does a merge that fails outright.
  Unresolved conflicts are reported as their own outcome and make the run exit
  nonzero. A launch failure or a timeout never reaches a merge: its branch is
  deleted, since the review committed nothing worth keeping.
- A review that is retried (`--retries`, after a launch failure or a nonzero
  exit) starts over from the same files the first attempt saw: whatever the
  failed attempt left behind is reset away. Isolated reviews rewind their
  worktree to the commit it was cut from. In-place reviews (`--jobs` 1)
  restore a snapshot of the working tree taken before the attempt, including
  uncommitted files that were already there, so your own dirty files survive
  and the agent's debris does not. If the snapshot is unavailable (including
  outside a Git repository) or restoration fails, neither same-agent retries
  nor fallback to another agent proceed. A missing snapshot leaves the failed
  attempt's files untouched.
- Lane checkouts are removed at the end of the loop; merged review branches
  are deleted; unmerged ones survive.

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

Each loop is one ordered, sequential pass; it does not shuffle reviews or use
extra `--jobs` lanes. Default is one loop. `-n N` (or `-n 0` for unlimited)
starts each later loop from the previous loop's last pushed tip, so agents
see already-applied fixes and a no-op review opens no second PR. One checkout
lives under `.gauntlet/worktrees/` for the current loop. It is removed only
when the pass has pushed at least one layer and every changed layer has a
confirmed PR. Otherwise it is kept, its path is logged, and a later loop
reuses it. Local and remote review
branches survive because open PRs need them. The branch checked out in the
original directory never moves and no stack branch is merged automatically.

### How a branch is named

A published layer is named `review/<NN>-<review>-<topic>`, e.g.
`review/03-sec-bolide-input-validation`: `NN` is the layer's 1-based schedule
position, which keeps merge order visible and sortable, and the topic is a
short slug cut from the commit subject. Later `--max-loops` passes insert the
loop number (`review/02-03-sec-bolide-input-validation`) so they cannot be
recovered as an earlier pass's layer; the first pass keeps the historical
form. The topic does not exist until the review has committed, so each layer
starts under a deterministic provisional name
(`review/<NN>-<review>-wip-<base>`), takes its topic name once the commit
exists, and only then is pushed. A resumed run finds already-published
layers by listing branches under the deterministic prefix, then verifies each
candidate by commit graph -- a recovered layer must be a one-commit child of
the previous layer's tip -- so a same-named leftover from an older stack is
rejected by ancestry rather than mistaken for this run's work. When the topic
name is already taken by such a leftover, the new layer appends the stack's
short base commit at the end of the name.

### What a PR says

The title is the commit subject the agent wrote for its change. The body adds
what the title has no room for: what can be read back from the branch itself,
plus the agent's own one-line account of each file it reported on, combined
into an overview paragraph.

```markdown
## Summary

fix(cache): drop the stale entry before the refill

guard the refill against a stale read; cover the refill race.

Scope: stale reads, cross-tenant bleed, stampedes.

## Changes

- `internal/cache/store.go`
- `internal/cache/store_test.go`

2 files changed, 41 insertions, 12 deletions.

## Stack

Layer 3 of a stack, targeting `review/02-sec-review-drop-stale-entries`
rather than `main`, so this comparison holds only this change. The base
merges first.
```

`Scope` is the review's own `Summary:` line, or its goal sentence when it
declares none; a prompt with neither leaves the line out. The per-file notes
are the `PATH:` lines the review printed; a note naming a file the commit never
touched is dropped, and the rest are assembled into an overview paragraph under
`## Summary`. The touched files under `## Changes` stay bare paths. The path
list stops at ten and counts the rest. The layer number counts published
branches, not schedule positions, so a review that changed nothing does not
leave a gap in the numbering. No body names the agent that wrote the change or
the pass it came from, for the same reason commit subjects do not. Every value
read out of the reviewed repository -- an agent's subject, a note, a prompt's
summary, a path -- is flattened to one line and length-bounded before it becomes
Markdown, and the whole body is capped.

Every check runs before any agent starts, the `--suggest` agent included.
When the original checkout has tracked or untracked changes, gauntlet names
them, explains that they will not be reviewed or included in the PRs, and
asks before doing anything else; `--yes` provides the same consent for an
unattended run, and a stdin that is not a real terminal (a pipe, `/dev/null`)
requires it. Only after that consent does gauntlet fetch the selected branch
from `--push-remote` and cut the isolated worktree directly from the fetched
commit. It does not pull, update a local branch, or read uncommitted files
from the original checkout: project prompts and `--suggest` signals come from
a snapshot of the fetched base, so a prompt file that exists only in the
dirty checkout cannot steer the run. It also requires Git, `gh`
authentication, repository access, and a successful dry-run new-branch push.
The remote's configured fetch URL selects the GitHub repository the PRs open
in; when the remote pushes somewhere else (a fork workflow with a separate
push URL), the PR heads are qualified with the push-side owner.

A review with no file changes leaves no branch or PR; the next review stays on
the last changed layer. A failed agent is reset to its layer's base before a
retry, and an exhausted failure is discarded before the next review starts.
A push failure keeps the commit on its branch and later reviews start from
the last successfully pushed layer. If the push succeeds but PR creation or
confirmation fails, later reviews stack on that pushed commit. Both failures
mark the layer failed and keep the checkout. A commit failure discards the
failed layer and lets later reviews run if the reset succeeds. An unreadable
pushed tip or a failed reset stops the pass because the next base cannot be
established safely. A URL printed by `gh pr create` is insufficient: a
head/base lookup must confirm the PR.

Branch names are deterministic from the initial base commit, schedule
position, and review name. That base commit is fetched once per logical run
and pinned: a hot reload hands it to the successor, so a remote base that
advances mid-run cannot rename the layers and split the resumed run into a
new stack. Before creating a PR gauntlet searches all PR states for that
exact head/base pair. A repeated run reuses it; a hot reload walks the
published prefix and resumes from the unfinished suffix. A branch that was
committed or pushed before a stopped PR call is published on the next
invocation instead of rerunning its agent.

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

## Project review metadata

A review a project carries can say what it keys on, which is what makes it
reachable by `--suggest-agent gauntlet`: the built-in rules only know built-in
names. One line anywhere in the prompt does it, and any single match proposes
the review:

    Signals: ext:.zig, name:build.zig, path:src/plugins, mark:comptime

`ext` is a file extension, `name` a file's base name, `path` a directory or
relative path, and `mark` a substring found near the top of a source file: a
declared `mark` is looked for in the same file heads the built-in rules read,
so it sees what they see and no more. The line comes from the reviewed tree, so
it is parsed strictly: those four kinds, at most 12 tokens, 40 characters each,
and a charset with no room for structure. Anything else on the line is dropped.
Across the whole prompt set at most 64 declared marks are searched for, since
each one costs a pass over every head.

A `Summary:` line is the short form of what the review looks for, for the
places that show the whole catalog one line at a time: `--list`, the launcher's
picker, and the README grid:

    Summary: stale reads, cross-tenant bleed, stampedes, growth

Every bundled review declares one. A project review that does not falls back to
its `Your goal` sentence, which those places then have to cut mid-clause. The
line is display text from the reviewed tree, so it is sanitized and cut to 60
characters.

## Committing, and where the work lands

Reviews edit a checkout. What happens to those edits is four separate
choices:

- `--commit` has an agent write a message and commit **on the branch you are
  on**. `--push` does that and pushes it. Neither creates a branch, and
  neither merges anything.
- `--jobs N` (N > 1) creates isolated review branches: each review works on a
  branch under `gauntlet/`, and the runner merges those back into **the
  branch you are on**, one at a time, squashed. Nothing here targets `main`
  by name; if you are on `feature-x`, the work lands on `feature-x`.
- `--merge-into BRANCH` is the step after that: once a loop's work is
  committed, the branch you are on is merged into BRANCH. It runs in a scratch
  checkout of BRANCH under `.gauntlet/worktrees/`, so the branch the reviews
  are running on stays checked out and the run stays watchable. It needs
  `--commit` or `--push`, because only committed work can merge, and it
  refuses (rather than reporting a merge that moved nothing) if the tree is
  still dirty or git status cannot be read. A conflict aborts the merge and
  leaves both branches where they were, for a human to resolve.
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

Several ways, and they mean different things:

| How | What happens |
|---|---|
| `s` on the dashboard, `SIGQUIT` (`Ctrl-\`), or the first `Ctrl-C` | Graceful: no new review starts, the ones running finish, and their work is committed, pushed, published as a PR, or merged as the mode asks. The run then exits normally and reviews not yet started are dropped. |
| A second `Ctrl-C`, or `SIGTERM` | Stops now: running agents are killed by process group, and the run exits 130. One more force-kills. |
| `q` twice on the dashboard | Hard stop: the dashboard closes and the run is cancelled, the same outcome as `SIGTERM`. The first press only arms it (the header reads `q AGAIN TO STOP`); any other key disarms, and `esc` cancels an armed one. `q` on the help overlay closes help and does not arm the stop. |
| `--once`, `--max-loops N`, `--runtime DUR`, `--token-budget N` | Planned endings, decided before the run starts. |
| `--usage-limit PCT` with `--usage-cmd CMD` | The graceful stop, triggered by a provider's usage window rather than by hand. |
| `SIGKILL`, an OOM kill, a crashed desktop session, a power cut | No cleanup runs. Agents may survive a process-only kill, and the run gets no closing index row. Its last checkpoint stays, and `gauntlet resume` continues it (see [Resuming after a crash](#resuming-after-a-crash)). |

The graceful stop is the one to reach for when a loop is halfway through and
the tree should not be left with uncommitted agent edits: it is the only stop
that still runs the configured commit, publication, and merge steps.

`Ctrl-C` reaches it first because the hard stop is rarely what an interactive
operator means. An agent mid-review never sees the terminal's `SIGINT` at all
-- every agent runs in its own session, whichever CLI it is -- so what
`Ctrl-C` means is decided by gauntlet, and a review that is seconds from
committing is worth one more `Ctrl-C` to kill. The session also means the
agent has no controlling terminal: it cannot open `/dev/tty` and put the
shared terminal into a raw mode where `Ctrl-C` stops generating a signal for
anyone, which is what some terminal-aware CLIs otherwise do the moment they
detect one, and which reads from the outside as a run that ignores `Ctrl-C`
entirely. Once any finish request is
already draining (`s`, `SIGQUIT`, a tripped usage limit), the next `Ctrl-C`
skips straight to terminating: the "finishing" message has been seen, and
pressing again means stop now. `SIGTERM` is never staged, because a
supervisor's `SIGTERM` means stop now and is usually followed by a `SIGKILL`
on a schedule gauntlet does not control.

### Stopping on a provider usage limit

A subscription's rolling window runs out mid-loop, and the review that hits
the wall fails for a reason that has nothing to do with the code. Given a
command that reports how much of the window is gone, the run can end itself
before that happens:

```sh
gauntlet -r quick --stacked-prs --usage-cmd 'my-usage-probe' --usage-limit 85
```

Between reviews -- never during one -- the runner asks the command for a
percentage. At or above the limit it takes the graceful stop from the table
above: the review in flight finishes, its layer is committed, pushed and
published as a PR, the commit and merge steps still run, and no further review
starts. Both flags are needed; either alone is refused at startup, since a
limit with no probe never trips and a probe with no limit spawns a process per
review to no effect.

The command is the operator's, not the agent's. It is split on whitespace and
executed directly, so no shell parses it, and it runs in the system temporary
directory with cwd-relative PATH entries dropped rather than in the tree under review.
Its last nonempty
stdout line must contain one finite number in 0-100, optionally with a trailing
`%`; earlier lines are ignored. An empty answer, a label on the last line, or
an invalid number is an error rather than a guessed percentage. Probe
failures are ignored, with only the first failure reported. Checks continue
between reviews; a later successful reading at or above the limit still ends
the run gracefully.

**Why a command, and not a number gauntlet reads itself.** The figure lives in
the provider's API response headers -- for Anthropic,
`anthropic-ratelimit-unified-5h-utilization` -- and no supported agent CLI
passes it to a headless launch. Claude Code, for instance, turns that header
into the `rate_limits.five_hour.used_percentage` field it hands to a status
line, and status lines do not run under `--print`, which is how every agent
here is launched. Its headless JSON stream carries only a coarser
`rate_limit_event` (`allowed`, `allowed_warning`, `rejected`) with no
percentage. Reading the header directly would mean holding the provider
credential, which is not something this tool does.

## Resuming after a crash

A process killed without a word (an OOM kill, a crashed desktop session, a
power cut) runs none of the stops above. Its last reviews may leave no
recorded result. Killing the parent process alone does not guarantee that its
agent processes exit: check for surviving agents and stop them before resuming
against the same checkout. What survives is the run's checkpoint:

```sh
gauntlet resume              # list the runs a crash cut off
gauntlet resume <run-id>     # continue one where it stopped
```

```
20261001T015123Z-1a2b  2026-10-01 09:51  ready
  ~/src/beam, 9 unfinished in the current loop, 1 loop done
  gauntlet -a microagent --tui -j 10 --yolo --push --commit

Continue one with: gauntlet resume <run-id>
```

- **What a checkpoint is.** The hot-reload handoff, written to
  `state/checkpoints/<run-id>.json` after every recorded result and every
  finished loop, plus the command line and the directory the run started in.
  A run that ends on its own, finished, stopped, or interrupted, deletes it, so
  one still listed belongs to a process that never got to. It is the one copy
  of the command line that keeps its credentials, because `gauntlet resume`
  execs it; it is written owner-only, and the listing prints it redacted.
- **What a resume does.** It changes to the recorded directory and hands
  over to this binary exactly as a hot reload would: same run id, schedule,
  seed, start time, and carried results, the command line it was started
  with, and the loop budget less the loops already finished. The journal and
  the one index row continue the same run. The line it prints and the listing
  both redact a credential the run was started with, so a command shown as
  `gauntlet --agent-cmd 'agent=cli --api-key [redacted]'` is a display copy;
  the resumed command uses the original credential from the checkpoint.
- **What runs again.** The reviews whose agents were running at the kill,
  then the ones not started. A review with a recorded result never runs
  twice; one that finished in the instant before the kill may, because the
  checkpoint counts it unfinished until its result is on disk.
- **What it will not do.** A directory a gauntlet still holds is refused
  (exit 75): the listing says `busy`. A `--jobs` run still needs a clean tree,
  and a tree a kill left mid-merge gets the same commit offer a fresh run
  does.
- **Branches.** A resumed run starts its lane and review branch names with
  `gauntlet/<run-id>-g<N>-l<loop>`, `N` counting the processes before it,
  so it never collides with what the killed one left. A `--jobs` resume then
  deletes the leftovers HEAD already contains (a review the kill cut off
  before anything was committed) and keeps any with a commit HEAD lacks (one
  between its commit and its merge), which `gauntlet doctor` lists. A hot
  reload's successor uses the same naming.

## Run journal

Every run is recorded under `~/.gauntlet` (override with `GAUNTLET_HOME`):

```
~/.gauntlet/
  runs/2026-08-25/20260825T131500Z-a91f.jsonl   the full event stream
  index.jsonl                                    one summary line per run
  pruned/2026-08-25/                             journals the keep bound moved out
  .index.lock                                    serializes index rebuilds and Close
  state/                                         hot-reload handoff files
  state/checkpoints/                             crash checkpoints, one per run in progress
  agents.json                                    custom agent definitions, not written by the CLI
```

```sh
gauntlet runs --limit 10        # recent runs: when, how long, pass/fail
gauntlet show <run-id>          # replay one run's events
jq 'select(.ev=="review_end")' ~/.gauntlet/runs/*/*.jsonl   # or use your own tools
```

Events are one JSON object per line (`run_start`, `loop_start`,
`review_start`, `review_end`, `merge`, `pull_request`, `commit`, `loop_end`,
`reload`, `run_end`, plus the runner's own `log` lines, which carry the
narration in `text`). The `text` field and the `args` on an index row are
written with the home directory shortened to `~`, because git errors and path
errors reach them carrying the absolute path of the reviewed tree, and this
is the copy that leaves the machine.

An `args` argument is stripped of credentials before it is written: a value
assigned to a name that says it is one, the value after a flag that names one
(`--agent-cmd 'agent=cli --api-key sk-…'`, the spelling the agent CLIs
document), a token carrying a published fixed prefix, and the userinfo of a
URL all read `[redacted]`. The flag name and the rest of the argument are
kept, so a row still says which option carried the key. An operator who
defines a wrapper agent is defining a credential on the command line, and
this copy outlives the run, so what is kept on disk and what `gauntlet runs`
prints are both redacted. The resume checkpoint is the exception: it holds the
real command line, because `gauntlet resume` execs it, and it is written
owner-only. A script that reads `args` therefore sees `[redacted]` where the
key was, and a value under eight characters is left alone as a flag or a
placeholder. `gauntlet runs --json` shortens its `home`
and `journals` fields the same way: they are facts about the install rather
than about any run, so nothing matches on them, and a resolved path under
`/home/<account>` would put the account name in every copy a script or an
archive job takes away. A script that hands either field to `test -d`,
`realpath`, or another tool expands the leading `~` first: the field is a
display spelling, not a path the shell resolves on its own. It shortens the
`dirs` and `path` of every run in the document for the same reason, and only in
the rendered copy: the index row on disk keeps them resolved, because the
listing and the history matcher need the path a person typed to still resolve.
The `dir` field of an event is stored resolved for the same reason, and
`gauntlet show` shortens it on the replayed line, since a replay is a copy a
reader pastes into an issue. `review_start` and `review_end` carry
`prompt_sha256`, the SHA-256 of the prompt text that launch was composed
from, so an output stays attributable to exact words after the prompt file
has changed or disappeared, and both carry `attempt`, the 1-based try the
event belongs to. One `review_start` is published per attempt, and the
attempt that decides the review publishes the `review_end` carrying its
number, so the end names the launch it closes and shares its `review` and
`prompt_sha256`. A launch that never got as far as building its command
publishes an end with no matching start. A retry to a different agent carries
on counting instead of
restarting at 1. An attempt that failed and was retried therefore has a start
with no end, and an end whose `attempt` is above 1 says how many tries came
before it; only the end counts the review, since the earlier attempts have no
outcome of their own. An end with no `prompt_sha256` is a review that never
launched: skipped, or interrupted before it started. Agent output and live
token ticks are not journaled; the journal records outcomes and narration.

The journals under `runs/` are the durable copy. `index.jsonl` is derived:
`gauntlet runs` and the file-signal suggester rebuild it when the file is
missing or empty, and append every newer unindexed journal when it is stale,
which is how a process that flushed events then died before writing the
summary row still lists. Several such runs in a row are all appended, oldest
first. A crash that is not at the tail, because a later run Closed, is still
listed: the n newest journals are the listing, and a hole in that window is
filled from the event stream without rewriting Close rows. `gauntlet show
<run-id>` reads the journal file directly and does not need the index. A
reconstructed summary has no `args`, `exit_code`, or `elapsed_s`; only a
clean close records those. A zero exit is therefore never invented for a run
that died before closing, which is what `exit_code` being a pointer buys: a
plain zero would have claimed the run succeeded. Rebuilds and Close serialize
so a listing cannot drop a summary that landed while the index was being
rewritten.

`ins` and `del` carry the same rule through `lines_measured`. Line counts are
skipped where they cannot be attributed, and a run with no counts is listed
`n/a`, not `+0/-0`: git may have been missing or unable to supply an
attributable diff. A run whose journal carries no `ins`
field on any event is unmeasured for the same reason. A `merge` or
`pull_request` event repeated in one stream counts once: the rows are summed
over layers, keyed on the branch the work landed on, so a layer a hot-reload
successor re-recorded is one diff and not two.

A `review_end` repeated under the same directory, loop, review, and branch is
counted once only when the repeat is a replay: an ending a hot-reload successor
wrote after a `run_start` of its own, closing a review its predecessor was
already interrupted on. A review scheduled twice is weight and runs twice, and
in place its two passes share every one of those fields, so the process and
the instant on the ending are what tell them apart: a pass that ran inside the
process already writing the journal is counted, an ending arriving from a later
process is that earlier work seen again. A journal carrying neither a
`run_start` nor an instant has nothing to judge a repeat by and reads every
repeat as a replay, which under-counts a run rather than inflating it.

`other` counts reviews whose terminal `status` a build does not recognize, and
reviews whose ending carries no status at all: an ending cut short, or one
written before the outcome was named. It exists so a journal written by a
newer version still reconciles: every review the row counts sits in exactly one
of `ok`, `failed`, `timeouts`, `skipped`, `conflicts`, `interrupted`, or
`other`, so the FAILED column keeps explaining the run's exit code. An ending
with no status is never a pass — reading it as one put a review in `ok` that
the run never recorded passing.

`timeouts` is its own count because a review the per-review deadline stopped is
not a review the agent failed, and the live tally already kept the two apart. A
row written before the field existed counted those reviews inside `failed` and
carries `timeouts_folded`, so a reader does not add a zero `timeouts` on top of
a `failed` that already holds them and count each deadline twice. A row this build writes, and one rebuilt from a journal, names the count
and leaves the marker off. `gauntlet runs --json` reports that fact as
`timeouts_known` per row and omits the marker, since the index is re-read and
storing the marker would make every old row claim a split it does not have.

What this tree holds, and what a lost `GAUNTLET_HOME` actually costs:

| Path | What it is | Survives |
|---|---|---|
| `runs/YYYY-MM-DD/<id>.jsonl` | one run's event stream | copy the files; `gauntlet runs` rebuilds the index from them |
| `index.jsonl` | listing cache | rebuilt from `runs/` |
| `pruned/YYYY-MM-DD/<id>.jsonl` | runs the retention bound moved out of the listing | kept until `--keep-runs` newer runs replace them; `gauntlet runs --restore <id>` moves one back |
| `.index.lock` | serializes index rebuilds and Close | ephemeral |
| `agents.json` | custom agent definitions | not written by gauntlet; copy it yourself if you need it after a disk loss |
| `state/<id>.json` | hot-reload handoff | ephemeral, deleted and the deletion synced after pickup, so a power cut cannot bring a handoff back and resume a run that already finished; a lost one aborts the successor (see [Updating and hot reload](#updating-and-hot-reload)) |
| `state/checkpoints/<id>.json` | crash checkpoint | rewritten after every recorded result and finished loop, deleted when the run ends on its own; one still on disk is a run `gauntlet resume` can continue |
| `<repo>/.gauntlet.lock` | directory lock | persistent inode; holder note cleared on release; do not remove while runs can start |
| `<repo>/.gauntlet/worktrees/` | isolated checkouts, `0700` | ephemeral; unmerged review branches stay in git |
| `<repo>` refs under `gauntlet/` and `review/` | reviews that did not land, the only output no copy of `GAUNTLET_HOME` holds | back up the repository's refs: the journal names the branch and none of its contents; `gauntlet doctor` lists the `gauntlet/` ones still on disk |

Review output lives in the reviewed repository's git history, not here. A
run journals into a 32KiB buffer it writes after every review and at loop
boundaries (`Flush` on `review_end` and `loop_end`), so a killed process
loses only the events of the reviews it was still running. The file is fsync'd when the run closes, before the index row is
written, and the state root is fsync'd after a rebuild renames the index or
after an append creates it, so a power cut cannot lose a row the run reported
writing, nor leave a listing whose journal is gone. Nothing here is
replicated off the machine: `GAUNTLET_HOME` is a directory, and backups of it
are yours.

The history is bounded. `--keep-runs` (200 by default, `0` keeps every run)
prunes at the end of a run by the run id's start instant: rows past the newest
N go, the journals
move to `pruned/`, and empty day directories under `runs/` are removed.
Open journals are kept; a closed long run can already be outside the window
when it finishes.
The first run after an upgrade therefore moves the runs past the newest 200 out
of the listing, and `gauntlet show` cannot reach a run that is not in the
listing. Raise the bound or set it to `0` before the first run on an install you
want to keep in full.

A prune is the one deletion path in the tree, it fires unattended at the end of
every run, and its input is a flag, so what it drops is recoverable rather than
gone: a pruned journal is renamed into `pruned/<shard>/<id>.jsonl` and the
quarantine is bounded by the same N, which leaves a run recoverable until N
newer runs have replaced it. `gauntlet runs` names what is waiting there, and

```sh
gauntlet runs --restore 20260825T131500Z-a91f
```

moves it back and rewrites its index row, after which `gauntlet show` replays
it. N runs later the quarantine drops it, so a long absence is a real loss:
nothing copies an evicted run anywhere. That eviction is the only deletion
here that unlinks a journal, so a run that does it says so on stderr:

```
Warning: the --keep-runs 200 bound unlinked 4600 runs from the quarantine, and nothing else holds them
```

Lowering the bound is what produces one, and the first run after an upgrade
that tightens it can produce a large count in a single pass. The moved runs
stay restorable; the unlinked ones are gone, which is why the count is printed
rather than left for the next listing to contradict.

A journal a run still has open is never pruned, so two gauntlet runs sharing
one `GAUNTLET_HOME` cannot move a running one's event stream out from under it.
A long run started before another one keeps its place in the listing until it
closes, however far past N it is by the order the keep window reads.

### Backup and restore

Choose the backup schedule from how much run history and custom agent
configuration you can afford to lose: an hourly copy gives an RPO of at most
one hour, for example. The CLI has no RTO guarantee; measure the restore below
with an archive the size of production and record the result in your own
incident runbook.

Back up the reviewed repository's git as well, or at least its refs. A copy of
`GAUNTLET_HOME` is a copy of the record, not of the work: a review that landed
is in the repository's history. An unmerged review branch exists nowhere else;
an in-place review may leave only uncommitted files, which require a working-tree
backup as well. The journal names branches but does not hold their contents. `git bundle create` over the
refs the run named, or a `git clone --mirror` of the repository, keeps them
while the machine is gone. The first command is the one a scheduled job runs;
the two after it are the restore:

```sh
set -e
git -C /path/to/repo bundle create /path/on/other-storage/repo.bundle --all
git clone /path/on/other-storage/repo.bundle /path/to/restored-repo
git -C /path/to/restored-repo fetch origin \
  'refs/heads/gauntlet/*:refs/heads/gauntlet/*' \
  'refs/heads/review/*:refs/heads/review/*'
```

The bundle includes all refs (`--all`), including unmerged reviews. Cloning
normally leaves those review branches under `refs/remotes/origin/`; the fetch
restores them as local branches, where `gauntlet doctor` can find the
`gauntlet/` namespace. The mappings are fast-forward-only and safe to repeat.
Use a new clone destination for each drill: cloning into a nonempty directory
fails. Bundle creation replaces the existing bundle atomically.

`cmd/gauntlet/bundlerecipe_test.go` executes this block, checks the restored
local branches and their content, and repeats the backup to the same path.

List branches that need preserving directly; `gauntlet doctor` also lists
leftovers under `gauntlet/`:

```sh
git -C /path/to/repo for-each-ref refs/heads/gauntlet refs/heads/review
```

Stop running CLI processes before taking the copy so the archive cannot catch
a journal line halfway through a write. Back up `runs/`, `pruned/`, and
`agents.json` to a different failure domain; `index.jsonl`, locks, worktrees, and hot-reload state
are intentionally excluded because they are derived or ephemeral. A crash
checkpoint under `state/checkpoints/` is excluded for a different reason and
not by accident: it names a directory, a command line, and a lane worktree that
were on the machine that wrote it, and resuming one restored onto a machine
that never had them would re-run reviews against a working tree no longer
there. The journal that records what the killed run had already finished is
what a restored tree needs; the checkpoint is only meaningful next to the
machine it was written on.

```sh
state=${GAUNTLET_HOME:-"$HOME/.gauntlet"}
archive=/path/on/other-storage/gauntlet-state.tgz
set --
[ ! -d "$state/runs" ] || set -- "$@" runs
[ ! -d "$state/pruned" ] || set -- "$@" pruned
[ ! -f "$state/agents.json" ] || set -- "$@" agents.json
if [ "$#" -gt 0 ]; then
  partial=$archive.$$.partial
  trap 'rm -f "$partial"' EXIT HUP INT TERM
  if tar -C "$state" -czf "$partial" "$@" && tar -tzf "$partial" >/dev/null; then
    mv -f "$partial" "$archive" || exit 1
    # The rename is a directory entry. Until the directory that holds the
    # archive records it, a power cut loses the copy this job just checked
    # and reports as written, and the previous archive is already gone.
    sync -f "$(dirname "$archive")" || exit 1
  else
    echo "gauntlet: archive not written, $archive left as it was" >&2
    exit 1
  fi
fi
```

The recipe builds and verifies a temporary archive beside the destination,
then renames it into place. A failed write leaves the previous archive intact;
keeping both paths on one filesystem preserves atomic rename. The PID suffix
separates overlapping jobs, and the trap removes a failed job's temporary.
`sync -f` makes the rename durable on systems that support that option; failure
is reported rather than claiming a durable backup. Check this command on your
backup host before scheduling it.

Only existing members are added. An empty state tree writes nothing, preserving
any previous archive.

Protect the destination with credentials and deletion controls independent of
the machine holding `GAUNTLET_HOME`; another directory on the same disk does
not protect against instance loss or malicious deletion.

Restore into an empty directory first rather than overwriting live state. Set
`restore` to a directory that does not exist yet, and `run` to one run id the
listing below prints:

```sh
set -e                          # every command has to succeed, not just the last
restore=/path/to/empty-restore
run=<run-id from the listing>   # no hyphen: a shell variable name takes none
mkdir -p "$restore"             # -p, so a re-run over an earlier drill is not a failure
tar -C "$restore" -xzf /path/on/other-storage/gauntlet-state.tgz
GAUNTLET_HOME="$restore" gauntlet runs --limit 10
GAUNTLET_HOME="$restore" gauntlet show "$run"
GAUNTLET_HOME="$restore" gauntlet runs --json
```

`set -e` stops at the first failed command. `mkdir -p` lets the drill repeat
over its own scratch directory; use a fresh directory to check that the archive
is complete without files from an earlier restore.

The three CLI commands rebuild the index, replay a journal, and report the
restored tree's `history` counts:

```json
"history": { "journals": 41, "rows": 41, "disagreed": 0, "pruned": 12, "truncated": 0, "agents": 2 }
```

Compare `history` with the source tree:

| Field | Check |
|---|---|
| `journals` | same number of event streams |
| `rows` | persisted summaries; matches journals after rebuilding the absent index in a fresh restore |
| `disagreed` | zero after that fresh rebuild; an index row cannot replace a lost journal |
| `pruned` | same number of quarantined runs |
| `truncated` | zero; a journal ending mid-line may list and replay while missing events |
| `agents` | same number of custom definitions; losing `agents.json` need not affect journal counts |

An existing index can retain older crash holes even when a listing reconstructs
those rows for display. Use a fresh restore directory to check the rebuilt
index rather than expecting every listing to repair the persisted cache.

An unreadable tree exits nonzero rather than printing zero counts. Doctor shows
these counts except `rows`, but its exit code also reflects installed agents,
so a nonzero doctor result alone does not establish a failed restore. If
`truncated` is nonzero, retake the archive with no run writing and check again.

Only after those commands succeed and the counts agree should the restored
directory replace the lost `GAUNTLET_HOME`.

Existing tests execute the backup and restore blocks, including empty and
quarantine-only state, failed writes, overlapping backups, and a repeated
restore. `TestRestoredTreeListsWhatSurvived` in `internal/journal` also checks
history and quarantine restoration without an index; the CLI checks the JSON
counts in `TestRunsJSONCountsARestoredTreeWithoutItsIndex`.

Run the commands after changing the archive job or upgrading across versions,
and periodically with the largest archive, because a backup that has only been
written has not been proven restorable.

## Updating and hot reload

```sh
gauntlet update --check   # what the latest release is
gauntlet update           # download, verify SHA-256, replace this binary
gauntlet --auto-update    # check periodically during a long run
```

The download is verified against the release's `checksums.txt` before it
replaces anything; a mismatch leaves the running binary untouched. The binary
an update replaces is kept beside it as `<binary>.previous`, so a release that
installs and then misbehaves can be rolled back without a build or download.
The [rollback recipe](CLI.md#updating) copies the current binary aside before
moving the previous copy into place; a failed copy aborts the replacement. A run in flight hands over to the restored
binary at its next safe point, like any other hot reload.

Hot reload is on by default (`--hot-reload=false` disables it). When the
executable is replaced atomically, for example by `gauntlet update` or
`make install`, the process drains its running reviews and hands over:

- **No agent is killed.** Reviews already running finish normally, including
  their commit, publication, and merge work. Only then does the process hand over.
- **No work is repeated.** The unfinished part of the current loop is handed
  to the successor, which finishes that loop rather than starting it over.
- **Nothing is lost.** Run id, start time, per-review results, per-agent
  totals, and loop budget cross the gap, so the final summary and the journal
  describe the whole run as one. The process keeps its pid and terminal:
  `execve`, not a respawn. If the successor cannot read the handoff, it exits
  instead of starting a new run: repeating finished reviews under a new id is
  worse than stopping.

The swap waits for running reviews and their publication or merge steps.
The watcher checks every five seconds and requires two matching readings;
that does not prove an in-place write is complete. Build a replacement to a
separate path and rename it over the executable atomically.
