# Command line reference

Every flag, environment variable, and exit code. `gauntlet --help` prints the
same flags from the binary itself, and `gauntlet doctor` reports what is
installed.

## Commands

| Command | What it does |
|---|---|
| `gauntlet [flags]` | review the current directory, looping until stopped |
| `gauntlet pick` | compose a run on screen, then run it |
| `gauntlet doctor` | report which agent CLIs and helper tools are installed, the git version against the floor every review needs, whether this host can confine an agent's filesystem writes, whether the state root is usable, what the run history holds: journals on disk, whether the index matches them, journals cut mid-line, and pruned runs still recoverable, and which local `gauntlet/` branches hold a review whose merge did not land |
| `gauntlet update [--check]` | replace this binary with the latest verified release |
| `gauntlet runs [--limit N] [--restore RUN-ID] [--json]` | list recent runs recorded under `~/.gauntlet`, A listing rebuilds a missing `index.jsonl` from the journal files, appends every newer unindexed journal when the listing is stale, and fills a crashed run that sits behind a later Close from its journal. The journal path and the note naming pruned runs still recoverable are printed even when the listing itself is empty. |
| `gauntlet show <run-id>` | replay one run's journal |
| `gauntlet resume [<run-id>]` | with no id, list the runs a crash cut off (an OOM kill, a crashed session, a power cut) and whether each can continue; with one, continue that run where it stopped. See [Resuming after a crash](RUNS.md#resuming-after-a-crash). |
| `gauntlet version` / `help` | print the version / this help |

Each subcommand reads only the flags that mean something to it: `pick` takes
`-C/--dir`, `--dirs` (and its `--target-dirs` alias), and `--prompt-dir`, `doctor` takes `--bin` and
`--agent-cmd`, `update` takes `--check` and `--update-repo`, `runs` takes
`--limit`, `--restore`, and `--json`, and `show`, `resume`, `version`, and `help` take none of their own. `--log` and
`--no-color` work everywhere, and may precede the subcommand, so
`gauntlet --no-color doctor` is the same as `gauntlet doctor --no-color`.
`show` and `resume` take their run id anywhere among the flags: `gauntlet show --no-color RUN`
and `gauntlet show RUN --no-color` are the same. Any other flag is refused with
a usage error (exit 2) rather than parsed and silently dropped, so
`gauntlet runs --jobs 4` fails loudly instead of printing a table that ignores
the concurrency it was given. (The `-V` flag form of version is the one
exception: it means "print the version and exit" and wins over scoping, like
help does.) `--check`, `--limit`, `--restore`, and `--json` are the four names
a bare `gauntlet` has no use for, and they say so rather than starting a run
that ignores them.

A subcommand is only read as one when it is the first word, so the flags above
are the flags that may lead: `gauntlet --json runs` leaves `runs` as a stray
argument and says so, rather than starting the default run. A flag name the
command does not take is reported with a "did you mean" drawn from the flags
that command does take, so a typo on a subcommand is never pointed at a flag
the next invocation would refuse.

`pick` opens a launcher drawn like the dashboard: reviews as collapsible sets
with a fill meter each and a one-line description beside every name, `suggest` as the first choice in that list (an agent
proposes the reviews, and the run pane names which agent does it), the agents
this machine has in the colors they will keep during the run, concurrency
metered against the CPU count, and the run switches (including stacked PRs).
The composed command line is on screen the whole time, and `enter` runs exactly
that, so the flags are learned rather than hidden. Picking nothing is not an empty run: it is every
review, which is what the composed command says by saying nothing. A run the
tree cannot support is refused with its reason on screen rather than composed
and failed on launch (concurrency above 1 needs no uncommitted changes to tracked files). It needs a
terminal on stdin and stdout, and takes `-C/--dir`, `--dirs`, and `--prompt-dir` to say
what it should offer. The launcher composes one run for one tree: `--dirs` with
several paths is a usage error (exit 2) rather than a run over the first of
them, so the run never covers a different set of trees than the ones named.

| Key | Action |
|---|---|
| `tab` / `shift+tab` | move between reviews, agents, and run options |
| `up`/`down`, `j`/`k` | move within a pane |
| `pgup` / `pgdn` | move by page within a pane |
| `space` | toggle a review, a whole set, an agent, or a switch. A set header toggles the members the filter is showing. |
| `left`/`right`, `h`/`l` | collapse or expand a set; change the job count; from the suggest row, step to the neighbouring pane |
| `a` | select all or none of what this pane is showing (the filter, if any, bounds it) |
| `+` / `-` | raise or lower concurrency, from any pane, up to the machine's cpu count |
| `/` | filter reviews by set name, by name, or by what they do; `enter` keeps it, `esc` clears it, `ctrl+u` clears it outright and `ctrl+w` drops the last word. While typing, the key legend names those keys instead of run/cancel. `ctrl+c` leaves the launcher from here as it does from everywhere else. A filter holds every set open, so the arrow keys step between panes while it is open. |
| `home` / `end`, `g` / `G` | first / last row in the focused pane |
| `?` | toggle a help overlay; `q` / `esc` / `ctrl+c` close it. When the terminal is too short for the whole overlay it scrolls: `j` / `k` or the arrow keys page it, `pgup` / `pgdn` jump, and `home` / `end` reach the ends. |
| `enter` / `q` / `esc` | run the composed command / leave without running. `q` and `esc` both ask first: a second press of the same key discards the run, the other one keeps it, and any other key takes the ask back. `esc` gets there through its own depths first, cancelling an armed quit, then clearing the filter. |

## Options

Path values (`--dir`, `--dirs`, `--log`, `--prompt-dir`, and the path half of
`--bin TOOL=PATH`) expand `$VARIABLES` and a leading `~` before use. A `$VAR`
that is unset or empty is a usage error rather than expanding to nothing. A
path that is not there, or is there and is not a directory, is a usage error
(exit 2) reported while parsing, named by the flag it came from and followed by
the help screen, the same as any other bad flag value. An
explicit empty `--prompt-dir`, `--log`, `--paths`, `--show-prompt`, `--merge-into`,
`--pr-base`, `--push-remote`, `--update-repo`, `--suggest-agent`, `--exclude`,
`--agents`, or `--dirs` is refused the same way `--dir` is.
An empty `--reviews` is refused too, and by the run rather than by the parser, so
that it cannot quietly expand to every review: nothing was filtered, and the
message names the flag.

A shorthand takes its value glued on, spaced, or with an equals sign: `-j3`,
`-j 3`, and `-j=3` are the same flag.

**Choosing reviews**

| Flag | Default | Purpose |
|---|---|---|
| `-r, --reviews LIST` | all | Reviews and/or set names to run. The `-review` suffix is optional (`sec` means `sec-review`). Naming one twice runs it twice per loop. Repeatable. |
| `-x, --exclude LIST` | none | Reviews and/or sets to skip. |
| `--paths LIST` | whole tree | Scope every review to these paths, relative to the reviewed directory. An entry may be a single file (`scripts/bolide.py`), a directory (everything under it), or a glob; comma-separated and repeatable. The agent still works from the full repository for context — the scope is prompt-enforced, not mechanical — but is told to report findings on and modify only the listed paths. An explicit empty `--paths` is refused, as is an entry carrying a line break, a backtick, or more than 200 characters: the entries are pasted into the prompt as instructions, so one that is not a path is refused where the operator can see it. |
| `--max-reviews N` | unlimited | Cap on reviews per loop. The cut happens after the seeded per-loop shuffle, so `--seed` replays exactly which N ran and different loops of one run sample different reviews. A review scheduled twice (weighting) fills two of the N slots when the shuffle places both inside the cut. With `--stacked-prs` each ordered pass is truncated to its first N entries, so at most N PRs per loop. A value at or above the schedule length changes nothing. |
| `-s, --suggest` | off | The built-in file-signal suggester picks relevant reviews and assigns 1–3 passes from accumulated evidence and review history. Read failures and repeatedly unproductive reviews retain one pass. An explicit `--suggest-agent AGENT` uses that agent to inspect the repo and assign each a weight of 1–3 passes per loop according to expected review value. Weight 0 skips a review. The preview shows repeats as `(x2)` or `(x3)`. Anything named with `--reviews` is also scheduled: naming a review once adds one pass to its suggested weight. `--reviews suggest,sec` says the same thing. Older agent output without weights schedules one pass per pick. The step runs before the schedule exists, so it runs under `--list` and `--dry-run` too. `--max-reviews` caps the resulting schedule, including repeats. |
| `--suggest-agent AGENT` | `gauntlet` | Agent to run the suggest step, or `gauntlet` to choose from file signals without a model: it costs no tokens and answers in milliseconds. It ranks reviews using source languages, import declarations, capability markers, typed package fields and build metadata, changes in the last 90 days, and past review outcomes. Each specialized review needs its own evidence: agent rules do not imply skills or prompts, a CLI does not imply a visual UI, and a Dockerfile does not imply Kubernetes. Missing documentation or source-tree tests can justify documentation and test-coverage reviews; missing CI does not imply release contracts. Dependency, build, scratch, and bundled compiler trees are excluded from both git listings and walks. It still cannot judge the consequences of a defect as an agent can. |
| `--suggest-timeout DUR` | `30m` | Timeout for the suggest step. |
| `--prompt-dir DIR` | bundled | Use `*-review.md` files from DIR instead of the embedded set. |

Sets: `all`, `project`, `quick`, `standard`, `security`, `frontend`,
`backend`, `agents`, `shipping`, `gitops`. A `*-review.md` file in the reviewed tree is
picked up automatically and overrides a bundled prompt of the same name.

**Choosing agents**

| Flag | Default | Purpose |
|---|---|---|
| `-a, --agents LIST` | auto-detect | `tool`, `tool:model`, or `tool:model@effort` entries (`claude:opus-5@xhigh`, `claude@max`); `mixed` means every installed agent. The model id and effort level are passed to the CLI verbatim: gauntlet cannot know which pairs a third-party CLI serves, so an unserved value fails at launch, by that CLI's own error. The part after the **last** `@` is the effort, since `:` and `/` occur inside model ids; a model id that itself ends in `@something` therefore cannot be written without an effort. Only agents whose effort flag was verified accept one — currently `claude` (`--effort`), `opencode` (`--variant`), and `microagent` (`--reasoning-effort`) — plus defined agents with an `effort` list (or an `{effort}` placeholder); the rest refuse at startup. Repeatable. |
| `--bin TOOL=PATH` | none | Run an agent from a specific executable. Repeatable. |
| `--agent-cmd NAME=ARGV` | none | Define an agent gauntlet does not ship, e.g. `pi='pi -p {prompt}'`. Repeatable; `~/.gauntlet/agents.json` makes it permanent. |
| `--continue-sessions` | off | Resume each agent's session between reviews (reuses context, bleeds context). Conflicts with `--jobs > 1` and `--stacked-prs`: each review is a fresh worktree, so there is no session to resume. |

**Execution**

| Flag | Default | Purpose |
|---|---|---|
| `-C, --dir DIR` | cwd | Directory to review. |
| `--dirs LIST` | none | Review several directories in parallel; globs are expanded. Conflicts with `--dir`. Also accepted as `--target-dirs`, the name the Python tool used. |
| `--retries N` | 2 | Reruns of a failed review on the same agent, waiting longer each time (5s, then doubling, jittered, capped at 2m). Each retry starts from the same tree the first attempt saw. A run that exhausts them still falls back to another agent. Retries and fallback require a restorable starting tree; unavailable snapshots (including outside Git) or failed restoration stop them. Timeouts are never retried, and neither is a command that would not build: the same argv would fail the same way, so that goes straight to the fallback. Retries and fallback stop when a run budget is exhausted, including during backoff. |
| `-j, --jobs N` | 1 | Parallel lanes **per directory**; >1 uses N persistent worktrees and merges back. With `--dirs`, the agents running at once are `jobs x directories`. Above 1 the run needs a repository with at least one commit and no uncommitted changes to tracked files, which it refuses to start without: commit or stash first. Untracked files are fine, and stay out of the review. |
| `-t, --timeout DUR` | `30m` | Per-review timeout (`90s`, `30m`, `1h`, `2d`). |
| `--runtime DUR` | unlimited | Wall-clock budget for the whole run. |
| `--token-budget N` | unlimited | Stop starting reviews once the run's agents have reported N tokens in total, across every loop, lane, agent, and directory, and stop the review that reaches N on its own. Wall clock bounds a slow machine, not the bill: an agent that stalls can spend a whole timeout's tokens in seconds, and one launch is not a step the schedule reaches. Counted from the tokens the reviews report, so a review that reports none costs nothing against the ceiling. The review in flight finishes unless it is the one that reached the ceiling, in which case it is stopped, logged as `OVER BUDGET`, and not retried. The figure that stops work is the provider's own, from the machine-readable usage envelope or the session transcript, so a number the model printed cannot end its own review; the commit and conflict steps launch agents whose tokens are not counted, and an agent in `--stream=false` mode reports no such figure. The commit and merge steps still run. |
| `--usage-cmd CMD` | none | Command whose stdout is the percentage of the provider's usage window already spent. Split on whitespace and executed directly, so no shell parses it; a value that splits into nothing is a usage error, and so is a first word that does not resolve to an executable on the absolute-only `PATH`. Used only with `--usage-limit`. A probe that fails once the run is under way is reported and ignored, since a ceiling that ends a run on a transient error is worse than none. |
| `--usage-limit PCT` | unlimited | Stop starting reviews once `--usage-cmd` reports this percentage or more. The two are used together or not at all: either alone is a usage error. The review in flight finishes, its branch is pushed and its PR opened, the commit and merge steps still run, then the run ends. |
| `-1, --once` | off | One loop, then stop. Conflicts with `--max-loops`. |
| `-n, --max-loops N` | unlimited (1 with `--stacked-prs`) | Stop after N loops. With `--stacked-prs`, omitting the flag is one pass; an explicit `0` is unlimited passes, each a fresh worktree from the previous tip. |
| `--seed N` | random | RNG seed for review order and agent picks, recorded in the journal so a rerun can replay it. Accepts a nonnegative decimal value or a `0x…` hex literal, with `_` allowed between digits; a leading zero is decimal, not octal, so `--seed 010` is ten. `0` derives one from the clock. A headless run prints the effective seed as its first event line, just under the run banner, so a run started without `--seed` reports the one that replays it, but only an explicit value replays on its own: a derived seed differs every run. One seed drives the whole run, the suggest step's agent order included, and a hot reload carries it across the exec. |
| `-c, --commit` / `-p, --push` | off | After each review, an agent writes a commit message (no AI attribution) and commits on the branch you are on, optionally pushing it. Neither merges anywhere. |
| `--resolve-conflicts` | on | When a review's branch will not merge, an agent resolves it in a scratch checkout and the result is merged. A resolution the agent finished but the merge then refused is kept on its own branch, named in the log with the command to land it, because that commit is the only copy of the resolver's work. Off (`--resolve-conflicts=false`) keeps the branch unmerged for a human, which is the older behavior. |
| `--merge-into BRANCH` | none | After each loop, merge this branch's committed work into BRANCH, in a scratch checkout so your own is never switched. Needs `--commit` or `--push`, since only committed work merges. Untracked files do not block it, matching `--jobs`. A dirty tree, or one whose git status cannot be read, is refused rather than reported as merged. A conflict aborts, leaves both branches untouched, and makes the run exit nonzero. |
| `--stacked-prs` | off | Run the selected reviews in their configured order, using one isolated worktree per loop. Every changed review is committed, pushed, and opened as a PR against the preceding changed review. Nothing is merged and the original checkout is untouched. This mode owns commits and pushes, forces `--jobs 1`, and conflicts with `--commit`, `--push`, and `--merge-into`. Default is one loop. `-n N` (or `-n 0` for unlimited) starts each later loop in a fresh worktree cut from the previous loop's last published tip, so already-applied fixes are in the tree and later passes no-op instead of reopening them. Loop 1 keeps the historical `review/<NN>-<review>-<topic>` branch names; later loops insert the loop number. |
| `--pr-base BRANCH` | current branch name | Remote base for `--stacked-prs`. Gauntlet fetches `REMOTE/BRANCH` and starts the isolated worktree at that commit; the local branch and checkout do not move or need to match it. Requires `--stacked-prs`. |
| `--push-remote REMOTE` | `origin` | Remote receiving stack branches and identifying the GitHub PR repository. Gauntlet verifies a dry-run new-branch push before launching an agent. Requires `--stacked-prs`. |
| `--no-sandbox` | off | Disable the default kernel filesystem write sandbox for trusted runs. Independent of `--yolo`. |
| `--sandbox-write DIR` | none | Allow writes beneath an additional existing directory; repeatable. Relative paths resolve against each reviewed worktree; `~` and environment variables expand. A grant that is not an existing directory is a usage error, reported while parsing, as for the other path flags. |
| `--yolo` | off | Drop the caution rules: no fix count or diff-size limit, public APIs may change. Containment is unaffected. It commits nothing on its own; it does answer yes to confirmation prompts. |
| `-y, --yes` | off | Answer yes to confirmation prompts, including excluding the original checkout's uncommitted files from a stacked run. |
| `--semcode` | off | Build a semcode index before the loop. |
| `--keep-runs N` | 200 | How many run journals to keep under `GAUNTLET_HOME`. A run files a journal and an index row, and at the end of every run the ones past the newest `N` leave the listing, along with the day directories they emptied. The index row is dropped with the journal, so the listing never names a run whose journal is gone. The journal itself is moved to `pruned/`, not deleted, and the quarantine is bounded by the same `N`, so a run stays recoverable until `N` newer runs have replaced it: see [Backing up and restoring the state tree](RUNS.md#backup-and-restore). A journal a run still has open is never pruned, so a run sharing `GAUNTLET_HOME` with a longer one is not moved out of the listing while it is writing. `0` keeps every run. |

**Output and modes**

| Flag or subcommand | Purpose |
|---|---|
| `doctor` (subcommand) | Report installed agent CLIs and helper tools, the state root in use, the file agent definitions were read from, and which of the environment variables below this process actually saw. A variable set to empty is reported as `(empty)`, so it is distinguishable from one left unset. `GITHUB_TOKEN` and `GH_TOKEN` are reported as `(set)` and never as values, so a pasted transcript cannot leak one. A state root that is not a directory or cannot be written to is reported, since a run that cannot write it loses the journal. The local branches under `gauntlet/` are reported too: a run deletes a lane branch once its review lands, so what is left is a review whose merge conflicted or failed, whose commits no copy of the state tree holds. Stacked layers under `review/` are not listed, since a stacked run publishes each one as a pull request. Exits 1 if no agent is usable or the state root is unusable. |
| `-l, --list` / `--dry-run` | Show reviews and sets / the planned schedule, then exit. `--list` does not need an agent CLI on PATH; `--dry-run` does, because it names the agents a real run would launch. Neither launches a review, but both print the schedule that `--suggest` produces, so with `--suggest` the built-in file-signal step runs first, for free. Naming an external `--suggest-agent AGENT` calls that agent and spends its tokens. |
| `--show-prompt REVIEW` | Print the exact composed prompt an agent would receive. Does not need an agent CLI on PATH. |
| `--log FILE` | Also write all output to FILE. The file is created if it is not there; a symlink, a directory, a non-regular file, or a parent directory that is missing or is not a directory is a usage error (exit 2). |
| `-q, --quiet` / `--raw` | Discard agent output / echo it verbatim instead of normalizing. |
| `--stream` | On by default: agents that have a machine-readable mode are asked for it, giving live token counts and the reasoning/output split shown separately in the feed (`--stream=false` launches them as before). |
| `--no-color` | Disable color everywhere, the plain log and the dashboard/launcher both. The `NO_COLOR` environment variable does the same. |
| `--opencode-db` | Read opencode's SQLite session store for its token counts. The driver ships in a default build; a build without it refuses the flag at startup rather than measuring nothing. |
| `--tui` | Live dashboard on the alt screen, redrawing several times a second. It is off by default: plain scrolling output stays in the scrollback and reads linearly, which is the path for screen readers and copied transcripts. `q` stops the run after two presses, and `esc` cancels that armed quit; `s` is the graceful finish. It needs a terminal on stdin and stdout, like `pick`: the dashboard reads keys, and a redirected stdin would hand it end-of-file and quit the run on the first tick. |
| `-V, --version` | Print the version. |
| `-h, --help` | Print the help screen, the flags below, and exit 0. `gauntlet help` is the same thing, and a command after it selects the topic: `gauntlet help runs` prints the same screen, whose SUBCOMMAND FLAGS section marks the flags `runs` reads. A word naming no command is a usage error (exit 2) rather than a plain `gauntlet help`, so a misspelling cannot exit 0 over a page of flags. |

**Updating**

| Flag | Default | Purpose |
|---|---|---|
| `--hot-reload` | on | When this binary is replaced during a run (by `gauntlet update`, `make install`, or a rebuild), finish the reviews in flight and hand the rest of the loop to the new binary instead of exiting. `--hot-reload=false` watches for nothing, so the run finishes on the binary it started with whatever replaces it on disk. |
| `--auto-update` | off | During a run, check for a new release shortly after start and every six hours, install it, and hand over at the next safe point like a hot reload. A failed check is reported and the run goes on. |
| `--update-repo REPO` | `maci0/gauntlet` | GitHub repository `gauntlet update` and `--auto-update` fetch releases from, as `owner/repo`. A URL or extra path segment is a usage error. |
| `--check` | off | Report the latest release without installing. |

Only the latest release is supported. `gauntlet update` resolves
`releases/latest` rather than a version list, so an older tag is what a user
keeps until they update, and a fix (a security one included) ships as a new
patch release rather than as a second release on an old one. Older tags stay
downloadable: the journal a run left is read with the version that wrote it.

An update keeps the binary it replaced beside the new one, as
`<binary>.previous`, so an install that succeeds and then misbehaves is
rolled back by renaming that copy back over it:

```sh
mv "$(command -v gauntlet)" "$(command -v gauntlet).broken"
mv "$(command -v gauntlet).previous" "$(command -v gauntlet)"
```

A run in flight hands over to the restored binary at its next safe point, the
same way it hands over to a new one. The copy is replaced by the next update,
so it is the version before the last one and no further back.

**History**

| Flag | Default | Purpose |
|---|---|---|
| `--limit N` | `20` | How many past runs to list in `gauntlet runs`. At least 1. |
| `--restore RUN-ID` | none | Put a pruned run back in the listing, by the id `gauntlet runs` names under "Pruned, still recoverable". The journal moves out of `pruned/` and its index row is written again, so `gauntlet show RUN-ID` replays it. A run that is not pruned, or is already listed, is a usage error (exit 2) rather than a silent no-op. Given twice, it is a usage error too: the last value would otherwise win and the id the first named would be restored by nobody. |
| `--json` | off | Print the listing as one JSON object on stdout, for a script or a dashboard: `home` and `journals` (the state paths, with the home directory shortened to `~`, so a script expands the leading `~` before handing either to a tool), `runs` (the index rows, each the same fields the journal wrote, with every count a number rather than a humanized column), `pruned` (the ids `--restore` takes, all of them rather than the five the table names), and `history` (the state tree behind the rows: `journals`, `rows`, `disagreed`, `pruned`, `truncated`, the counts `gauntlet doctor` prints on its Run history line). Nothing else is written to stdout, and the table's legend and column layout are left behind, so a pipe carries the document alone. An empty listing is `{"runs": [], ...}`, not a message. With `--restore` it prints `{"restored": "RUN-ID"}`. Errors, including the exit codes, are unchanged. |

## Environment variables

None is required; unset, everything lives under `~/.gauntlet`.

| Variable | Effect |
|---|---|
| `TMPDIR` | An absolute temporary directory is added to sandbox writable roots alongside the platform's own temporary directory (`TMPDIR` when set, `/tmp` otherwise). A value naming a directory that does not exist grants nothing and is not an error, the same as the platform's own temporary directory; a relative value grants nothing. `make test` sets its own scratch directory and ignores an exported value; override it on the make command line. |
| `GAUNTLET_HOME` | Root of the state tree instead of `~/.gauntlet`: the run journal, hot-reload handoff files, crash checkpoints, and `agents.json`. A leading `~` and any `$VAR` expand, a value that is empty is the variable unset, and a relative path is resolved against the working directory once, so every later read of the root agrees wherever in the process it happens. Two values are refused at startup rather than read from: one whose `$VAR` is unset or empty, and one naming something that is not a directory. `~` expands only as `~/...`, and a value that is left a tilde — a bare `~`, or another account's `~user` — is refused rather than taken as a path: for the state root, unlike a file name, there is no directory it could be meant to be, and reading it literally put the whole state tree in a directory called `~` beside the repository under review. `gauntlet doctor` prints the root in use, where it came from, and whether it can be written to, so a mistyped value is visible without reading the journal. A root that is neither absent nor a directory (behind a file, a symlink loop, or a permission this process lacks) is not usable: the journal falls back to `.gauntlet` in the working directory, and a hot reload refuses rather than write its handoff there, so the run finishes in this process and the new binary is picked up at the next start. |
| `GAUNTLET_NO_ANIMATION` | Anything but empty, `0`, `false`, `no`, or `off`: the dashboard stops moving. The animated reasoning glyph holds one frame instead of cycling, and the screen stops repainting itself ten times a second: the frame changes when a review reports or a key is pressed, and otherwise every thirty seconds, so the clock and the timeout meters stay honest. The token count beside the glyph keeps updating, so an active agent still reads as one. Standard `NO_MOTION` and `REDUCED_MOTION` are also honored, but `GAUNTLET_NO_ANIMATION` is read first: set to one of the five values above it turns the motion back on even when a desktop session exports `REDUCED_MOTION=1`. Left empty it defers to the other two. |
| `GITHUB_TOKEN` | Optional. Sent only to GitHub by `gauntlet update` and `--auto-update`, for a higher API rate limit and for private release assets. |
| `GH_TOKEN` | Same as `GITHUB_TOKEN`, and wins when both carry a token. An empty value is ignored rather than counted as set, so `GH_TOKEN=` exported to clear a token does not mask a `GITHUB_TOKEN` the same environment also carries. |
| `NO_COLOR` | If set at all, no color anywhere. Wins over the two below. |
| `CLICOLOR_FORCE` / `FORCE_COLOR` | Anything but empty, `0`, `false`, `no`, or `off`: force color on, so piping through `less -R` keeps its palette. |
| `TERM=dumb` | Disables color; even `CLICOLOR_FORCE` does not override it. Read like the two above, so any case and surrounding space are ignored. |
| `GIT_SSH_COMMAND` | Optional. The command git uses for SSH. Empty or whitespace-only defaults to `ssh`, which outranks a repository-local `core.sshCommand`; set it to use a different binary or options. |

(`GAUNTLET_STATE` exists too, but only within one hot reload or `gauntlet
resume`: it names the handoff file passed across the exec.)

## Signals

| Signal | Effect |
|---|---|
| `SIGQUIT` (`Ctrl-\`) | Finish gracefully: no new review starts, the ones running end and land their work, then the run exits normally. `s` on the dashboard does the same. |
| `SIGINT` (`Ctrl-C`) | Staged. The first finishes gracefully, exactly like `SIGQUIT` — the review in flight lands its work, commit, push and PR included. The second terminates the running reviews and exits 130. The third force-kills. A `Ctrl-C` after any finish request skips straight to terminating. |
| `SIGTERM` | Terminate the running reviews and exit 130. A second one force-kills. Not staged: a supervisor's `SIGTERM` means stop now. |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | the command did what it was asked: every review ran and passed, or the listing, replay, or report was printed |
| 1 | a review failed, timed out, was skipped, or would not merge; a commit step failed; `doctor` found no agent to launch; `update` failed; the run listing could not be read in full |
| 2 | usage error |
| 75 | another instance holds the lock for that directory |
| 130 | interrupted |

## Defining an agent gauntlet does not ship

`--agent-cmd` defines one for a single run; `~/.gauntlet/agents.json` keeps it:

```json
{"myagent": {"argv": ["myagent", "-p", "{prompt}"],
             "stream": ["--mode", "json"],
             "continue": ["--resume"],
             "usage": {"roots": ["~/.myagent/sessions"]}}}
```

`argv` is required and must contain `{prompt}`. `stream` is the argument list
that asks the CLI for machine-readable output (`--stream`), `continue` is the
argument list that resumes the agent's last session in this directory under
`--continue-sessions`, and `usage` says where it keeps session transcripts,
which is what gives a defined agent live token counts. `usage.cumulative`
switches the reader to subtracting a per-session baseline, for a format whose
counters only ever grow across the whole file rather than resetting per record;
`usage.header_cwd` says the working directory appears once at the top of a
session file rather than on every record. `model` (e.g.
`["--model", "{model}"]`) is appended when a spec pins a model, and `effort`
(e.g. `["--effort", "{effort}"]`) likewise when a spec pins a reasoning
effort; without an `effort` list (or an `{effort}` placeholder in `argv`),
`name:model@effort` is refused at startup. When specified, `model` must contain
the `{model}` placeholder, and `effort` must contain `{effort}`; neither `model`,
`effort`, `stream`, nor `continue` may contain `{prompt}`; `model` cannot be
specified when `argv` contains `{model}`, and `effort` cannot be specified when
`argv` contains `{effort}`; `argv[0]`, `usage.roots`, and `usage.suffix` cannot
contain placeholders. `opt_in` (boolean)
keeps the agent out of auto-detection and `mixed`: it runs only when named
explicitly with `--agents`. `note` is an optional explanatory string shown in
`gauntlet doctor`. Every placeholder in an `argv` entry is expanded, so one
argument may carry several (`"--opts=model={model},effort={effort}"`); the composed
prompt is substituted as content, so a placeholder the review's own text happens
to contain is left alone. An argument mentioning a `{model}` or `{effort}` the run
did not pin is left out whole, the way an unused `model` block is, so the agent is
never handed `--model=`; put settings that vary independently in arguments of
their own. The argument holding `{prompt}` is always kept, whatever else it
mentions. The name
itself cannot contain spaces, commas, colons, equals signs, or at signs:
they are the separators `--agents`, `--bin`, and `--agent-cmd` parse by. On
the same run, a `--agent-cmd` for a name the file also
defines wins over the file's entry; the file is what survives for later runs.
The file must be a JSON object (`{}` for no custom definitions, not `null`):
comments, trailing commas, unknown keys, and duplicate keys within an object
are refused at startup rather than half-read. Field names match without regard
to case, so `opt_in` and `OPT_IN` in the same definition are duplicates too.
Duplicate agent names are also refused; definitions must not rely on JSON key
order to override values. A name gauntlet ships is refused the same way, by
`--agent-cmd` and by the file: a definition of a built-in name is a startup
error, not a silent override, because the compiled-in flags are the ones the
agent is launched with. An entry that named a built-in before that name shipped
(`microagent` is the one added in 1.27.0) has to be deleted on upgrade; the file
is the only place it can have come from, and `gauntlet doctor` names the file it
read. `gauntlet doctor` lists every agent it knows, defined ones included, and
the file it read them from.

## Filesystem write sandbox

Agents run under Landlock on Linux (kernel 5.13+ with Landlock enabled) and
Seatbelt on macOS (`/usr/bin/sandbox-exec`), including their shell commands and
MCP children. A sandbox failure stops the launch; `--no-sandbox` explicitly
disables it. It does not restrict reads or network access.

`gauntlet doctor` reports whether this host can confine a run, and which
mechanism answered: the Landlock ABI on Linux, the Seatbelt launcher on macOS.
The check is a capability probe rather than a platform name, so a kernel built
without `CONFIG_SECURITY_LANDLOCK` and a macOS without its launcher are both
reported before a run starts rather than by an agent that never launched. A
host that cannot confine still runs, with `--no-sandbox`.

Writable roots are the active worktree and shared `.git` metadata, the
platform's temporary directory (an absolute `TMPDIR`, `/tmp` otherwise; it is
skipped when the host has none), and the selected agent's usual state directories:
`~/.claude`, `~/.codex`, `~/.gemini`, `~/.qwen`, `~/.grok`,
`~/.gemini/antigravity-cli` (agy),
`~/.cursor`, `~/.kimi-code`, `~/.microagent`, or `~/.dsh`. For opencode
and crush, their own subdirectories under `~/.local/share`, `~/.local/state`,
and `~/.cache` are writable. These state directories are created if missing;
the rest of the home directory stays read-only.

Custom agents, nonstandard state paths, and external build caches need explicit
grants, for example `--sandbox-write ~/.cache/go-build`. Grants must already
exist and name directories, and symlinks are resolved before granting them.
An agent that writes global configuration outside its state directory needs
a grant for that configuration's parent directory. Every grant permits writes
to the entire subtree; grant only what the run needs. Linux kernels with older
Landlock ABIs cannot enforce newer operations (truncation needs ABI 3).

## Memory limits on Linux

Gauntlet does not impose a memory limit on agents or their subprocesses.
The filesystem sandbox does not limit memory. On Linux with systemd and the
cgroup v2 memory controller available to your user manager, wrap the run in
a transient scope:

```sh
systemd-run --user --scope \
  -p MemoryMax=4G -p MemorySwapMax=0 \
  gauntlet --once -a microagent
```

`MemoryMax=4G` caps the scope's aggregate memory usage at 4 GiB, including
Gauntlet, all parallel agents, and their subprocesses. The cap is shared
across `--jobs`. `MemorySwapMax=0` disables swap for the scope. If memory
cannot be reclaimed to stay within the cap, the kernel may kill processes
in the scope, which can fail the run. Adjust `4G` for your workload.

These are `systemd-run` properties; see
[systemd's memory controls](https://github.com/systemd/systemd/blob/main/man/systemd.resource-control.xml)
for details.
