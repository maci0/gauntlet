# Command line reference

Every flag, environment variable, and exit code. `gauntlet --help` prints the
same flags from the binary itself, and `gauntlet doctor` reports what is
installed.

## Commands

| Command | What it does |
|---|---|
| `gauntlet [flags]` | review the current directory, looping until stopped |
| `gauntlet pick` | compose a run on screen, then run it |
| `gauntlet doctor` | report which agent CLIs and helper tools are installed |
| `gauntlet update [--check]` | replace this binary with the latest verified release |
| `gauntlet runs [--limit N]` | list recent runs recorded under `~/.gauntlet`. Rebuilds a missing `index.jsonl` from the journal files, and appends every newer unindexed journal when the listing is stale. |
| `gauntlet show <run-id>` | replay one run's journal |
| `gauntlet version` / `help` | print the version / this help |

Each subcommand reads only the flags that mean something to it: `pick` takes
`-C/--dir`, `--dirs`, and `--prompt-dir`, `doctor` takes `--bin` and
`--agent-cmd`, `update` takes `--check` and `--update-repo`, `runs` takes
`--limit`, and `show` and `version` take none of their own. `--log` and
`--no-color` work everywhere, and may precede the subcommand, so
`gauntlet --no-color doctor` is the same as `gauntlet doctor --no-color`.
`show` takes its run id anywhere among the flags: `gauntlet show --no-color RUN`
and `gauntlet show RUN --no-color` are the same. Any other flag is refused with
a usage error (exit 2) rather than parsed and silently dropped, so
`gauntlet runs --jobs 4` fails loudly instead of printing a table that ignores
the concurrency it was given. (The `-V` flag form of version is the one
exception: it means "print the version and exit" and wins over scoping, like
help does.)

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
several paths uses the first.

| Key | Action |
|---|---|
| `tab` / `shift+tab` | move between reviews, agents, and run options |
| `up`/`down`, `j`/`k` | move within a pane |
| `space` | toggle a review, a whole set, an agent, or a switch. A set header toggles the members the filter is showing. |
| `left`/`right`, `h`/`l` | collapse or expand a set; change the job count |
| `a` | select all or none of what this pane is showing (the filter, if any, bounds it) |
| `+` / `-` | raise or lower concurrency, from any pane |
| `/` | filter reviews by name or by what they do; `enter` keeps it, `esc` clears it. While typing, the key legend names those keys instead of run/cancel. |
| `home` / `end` | first / last row in the focused pane |
| `?` | toggle a help overlay; `q` / `esc` close it |
| `enter` / `q` | run the composed command / leave without running |

## Options

Path values (`--dir`, `--dirs`, `--log`, `--prompt-dir`, and the path half of
`--bin TOOL=PATH`) expand `$VARIABLES` and a leading `~` before use. A `$VAR`
that is unset or empty is a usage error rather than expanding to nothing. An
explicit empty `--prompt-dir` or `--log` is refused the same way `--dir` is.

A shorthand takes its value glued on, spaced, or with an equals sign: `-j3`,
`-j 3`, and `-j=3` are the same flag.

**Choosing reviews**

| Flag | Default | Purpose |
|---|---|---|
| `-r, --reviews LIST` | all | Reviews and/or set names to run. The `-review` suffix is optional (`sec` means `sec-review`). Naming one twice runs it twice per loop. Repeatable. |
| `-x, --exclude LIST` | none | Reviews and/or sets to skip. |
| `-s, --suggest` | off | An agent inspects the repo and proposes the relevant reviews. It composes with `--reviews` rather than replacing it: anything named there is scheduled as well, and a review the agent also picks is scheduled twice, which is how repeats have always asked for more weight. `--reviews suggest,sec` says the same thing. The step runs before the schedule exists, so it runs under `--list` and `--dry-run` too. |
| `--suggest-agent AGENT` | from `--agents` | Agent to run the suggest step, or `gauntlet` to choose from file signals instead of asking a model: it costs no tokens and answers in milliseconds. It weighs how much of the tree each language is, reads the head of source files for what they import and call, asks git which files have changed in the last 90 days, counts what is missing (no tests, no docs, no CI) as evidence of its own, and demotes reviews that have finished in this directory several times without changing a line. Reviews are ranked by that evidence and the weakest are not proposed. It still cannot tell a toy HTTP handler from a payment path; an agent can. |
| `--suggest-timeout DUR` | `30m` | Timeout for the suggest step. |
| `--prompt-dir DIR` | bundled | Use `*-review.md` files from DIR instead of the embedded set. |

Sets: `all`, `project`, `quick`, `standard`, `security`, `frontend`,
`backend`, `agents`, `shipping`. A `*-review.md` file in the reviewed tree is
picked up automatically and overrides a bundled prompt of the same name.

**Choosing agents**

| Flag | Default | Purpose |
|---|---|---|
| `-a, --agents LIST` | auto-detect | `tool`, `tool:model`, or `tool:model@effort` entries (`claude:opus-5@xhigh`, `claude@max`); `mixed` means every installed agent. The model id and effort level are passed to the CLI verbatim: gauntlet cannot know which pairs a third-party CLI serves, so an unserved value fails at launch, by that CLI's own error. The part after the **last** `@` is the effort, since `:` and `/` occur inside model ids; a model id that itself ends in `@something` therefore cannot be written without an effort. Only agents whose effort flag was verified accept one — currently `claude` (`--effort`) and `opencode` (`--variant`) — plus defined agents with an `effort` list (or an `{effort}` placeholder); the rest refuse at startup. Repeatable. |
| `--bin TOOL=PATH` | none | Run an agent from a specific executable. Repeatable. |
| `--agent-cmd NAME=ARGV` | none | Define an agent gauntlet does not ship, e.g. `pi='pi -p {prompt}'`. Repeatable; `~/.gauntlet/agents.json` makes it permanent. |
| `--continue-sessions` | off | Resume each agent's session between reviews (reuses context, bleeds context). Conflicts with `--jobs > 1` and `--stacked-prs`: each review is a fresh worktree, so there is no session to resume. |

**Execution**

| Flag | Default | Purpose |
|---|---|---|
| `-C, --dir DIR` | cwd | Directory to review. |
| `--dirs LIST` | none | Review several directories in parallel; globs are expanded. Conflicts with `--dir`. Also accepted as `--target-dirs`, the name the Python tool used. |
| `--retries N` | 2 | Reruns of a failed review on the same agent, waiting longer each time (5s, then doubling, jittered). Each retry starts from the same tree the first attempt saw. A run that exhausts them still falls back to another agent. Timeouts are never retried. |
| `-j, --jobs N` | 1 | Parallel lanes **per directory**; >1 uses N persistent worktrees and merges back. With `--dirs`, the agents running at once are `jobs x directories`. |
| `-t, --timeout DUR` | `30m` | Per-review timeout (`90s`, `30m`, `1h`, `2d`). |
| `--runtime DUR` | unlimited | Wall-clock budget for the whole run. |
| `--usage-cmd CMD` | none | Command whose stdout is the percentage of the provider's usage window already spent. Split on whitespace and executed directly, so no shell parses it; a value that splits into nothing is a usage error. Used only with `--usage-limit`. |
| `--usage-limit PCT` | unlimited | Stop starting reviews once `--usage-cmd` reports this percentage or more. The review in flight finishes, its branch is pushed and its PR opened, the commit and merge steps still run, then the run ends. |
| `-1, --once` | off | One loop, then stop. |
| `-n, --max-loops N` | unlimited | Stop after N loops. |
| `--seed N` | random | RNG seed for review order and agent picks, recorded in the journal so a rerun can replay it. Accepts any nonnegative integer literal (`0x…` included); `0` derives one from the clock. |
| `-c, --commit` / `-p, --push` | off | After each review, an agent writes a commit message (no AI attribution) and commits on the branch you are on, optionally pushing it. Neither merges anywhere. |
| `--resolve-conflicts` | on | When a review's branch will not merge, an agent resolves it in a scratch checkout and the result is merged. Off (`--resolve-conflicts=false`) keeps the branch unmerged for a human, which is the older behavior. |
| `--merge-into BRANCH` | none | After each loop, merge this branch's committed work into BRANCH, in a scratch checkout so your own is never switched. Needs `--commit` or `--push`, since only committed work merges. A dirty tree, or one whose git status cannot be read, is refused rather than reported as merged. A conflict aborts, leaves both branches untouched, and makes the run exit nonzero. |
| `--stacked-prs` | off | Run the selected reviews once, in their configured order, using one isolated worktree. Every changed review is committed, pushed, and opened as a PR against the preceding changed review. Nothing is merged and the original checkout is untouched. This mode owns commits and pushes, forces `--jobs 1` and one loop, and conflicts with `--commit`, `--push`, and `--merge-into`. |
| `--pr-base BRANCH` | current branch name | Remote base for `--stacked-prs`. Gauntlet fetches `REMOTE/BRANCH` and starts the isolated worktree at that commit; the local branch and checkout do not move or need to match it. Requires `--stacked-prs`. |
| `--push-remote REMOTE` | `origin` | Remote receiving stack branches and identifying the GitHub PR repository. Gauntlet verifies a dry-run new-branch push before launching an agent. Requires `--stacked-prs`. |
| `--yolo` | off | Drop the caution rules: no fix count or diff-size limit, public APIs may change. Containment is unaffected. It commits nothing on its own; it does answer yes to confirmation prompts. |
| `-y, --yes` | off | Answer yes to confirmation prompts, including excluding the original checkout's uncommitted files from a stacked run. |
| `--semcode` | off | Build a semcode index before the loop. |

**Output and modes**

| Flag | Purpose |
|---|---|
| `doctor` | Report installed agent CLIs and helper tools. Exits 1 if no agent is usable. |
| `-l, --list` / `--dry-run` | Show reviews and sets / the planned schedule, then exit. `--list` does not need an agent CLI on PATH; `--dry-run` does, because it names the agents a real run would launch. Neither launches a review, but both print the schedule that `--suggest` produces, so with `--suggest` the suggest step runs first and really does call an agent (and spend its tokens). `--suggest-agent gauntlet` answers the same question from file signals, for free. |
| `--show-prompt REVIEW` | Print the exact composed prompt an agent would receive. Does not need an agent CLI on PATH. |
| `--log FILE` | Also write all output to FILE. |
| `-q, --quiet` / `--raw` | Discard agent output / echo it verbatim instead of normalizing. |
| `--stream` | On by default: agents that have a machine-readable mode are asked for it, giving live token counts and the reasoning/output split shown separately in the feed (`--stream=false` launches them as before). |
| `--no-color` | Disable color everywhere, the plain log and the dashboard/launcher both. The `NO_COLOR` environment variable does the same. |
| `--opencode-db` | Read opencode's SQLite session store for its token counts. The driver ships in a default build; a build without it refuses the flag at startup rather than measuring nothing. |
| `--tui` | Live dashboard on the alt screen, redrawing several times a second. It is off by default: plain scrolling output stays in the scrollback and reads linearly, which is the path for screen readers and copied transcripts. `q` / `esc` stop the run after two presses; `s` is the graceful finish. |
| `-V, --version` | Print the version. |

**Updating**

| Flag | Default | Purpose |
|---|---|---|
| `--hot-reload` | on | When this binary is replaced during a run (by `gauntlet update`, `make install`, or a rebuild), finish the reviews in flight and hand the rest of the loop to the new binary instead of exiting. |
| `--auto-update` | off | During a run, check for a new release shortly after start and every six hours, install it, and hand over at the next safe point like a hot reload. A failed check is reported and the run goes on. |
| `--update-repo REPO` | `maci0/gauntlet` | GitHub repository `gauntlet update` and `--auto-update` fetch releases from, as `owner/repo`. A URL or extra path segment is a usage error. |

## Environment variables

None is required; unset, everything lives under `~/.gauntlet`.

| Variable | Effect |
|---|---|
| `GAUNTLET_HOME` | Root of the state tree instead of `~/.gauntlet`: the run journal, hot-reload handoff files, and `agents.json`. |
| `GAUNTLET_NO_ANIMATION` | Anything but empty or `0`: the dashboard's animated reasoning glyph holds one frame instead of cycling, for motion sensitivity. The token count beside it keeps updating, so an active agent still reads as one. |
| `GITHUB_TOKEN` | Optional. Sent only to GitHub by `gauntlet update` and `--auto-update`, for a higher API rate limit and for private release assets. |
| `GH_TOKEN` | Same as `GITHUB_TOKEN`. Wins if both are set, matching GitHub CLI. |
| `NO_COLOR` | If set at all, no color anywhere. Wins over the two below. |
| `CLICOLOR_FORCE` / `FORCE_COLOR` | Anything but empty or `0`: force color on, so piping through `less -R` keeps its palette. |
| `TERM=dumb` | Disables color; even `CLICOLOR_FORCE` does not override it. |

(`GAUNTLET_STATE` exists too, but only within one hot reload: it names the
handoff file passed across the exec.)

## Signals

| Signal | Effect |
|---|---|
| `SIGQUIT` (`Ctrl-\`) | Finish gracefully: no new review starts, the ones running end and land their work, then the run exits normally. `s` on the dashboard does the same. |
| `SIGINT` (`Ctrl-C`) | Staged. The first finishes gracefully, exactly like `SIGQUIT` — the review in flight lands its work, commit, push and PR included. The second terminates the running reviews and exits 130. The third force-kills. A `Ctrl-C` after any finish request skips straight to terminating. |
| `SIGTERM` | Terminate the running reviews and exit 130. A second one force-kills. Not staged: a supervisor's `SIGTERM` means stop now. |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | every review ran and passed |
| 1 | a review failed, timed out, was skipped, or would not merge; a commit step failed |
| 2 | usage error |
| 75 | another instance holds the lock for that directory |
| 130 | interrupted |

## Defining an agent gauntlet does not ship

`--agent-cmd` defines one for a single run; `~/.gauntlet/agents.json` keeps it:

```json
{"myagent": {"argv": ["myagent", "-p", "{prompt}"],
             "stream": ["--mode", "json"],
             "usage": {"roots": ["~/.myagent/sessions"]}}}
```

`argv` is required and must contain `{prompt}`. `stream` is the argument list
that asks the CLI for machine-readable output (`--stream`), and `usage` says
where it keeps session transcripts, which is what gives a defined agent live
token counts. `model` (e.g. `["--model", "{model}"]`) is appended when a spec
pins a model, and `effort` (e.g. `["--effort", "{effort}"]`) likewise when a
spec pins a reasoning effort; without an `effort` list (or an `{effort}`
placeholder in `argv`), `name:model@effort` is refused at startup. Every
placeholder in an `argv` entry is expanded, so one argument may carry several
(`"--opts=model={model},effort={effort}"`); the composed prompt is substituted
as content, so a placeholder the review's own text happens to contain is left
alone. An argument mentioning a `{model}` or `{effort}` the run did not pin is
left out whole, the way an unused `model` block is, so the agent is never
handed `--model=`; put settings that vary independently in arguments of their
own. The argument holding `{prompt}` is always kept, whatever else it
mentions. The name
itself cannot contain spaces, commas, colons, equals signs, or at signs:
they are the separators `--agents`, `--bin`, and `--agent-cmd` parse by. On
the same run, a `--agent-cmd` for a name the file also
defines wins over the file's entry; the file is what survives for later runs.
The file is plain JSON: comments, trailing commas, and unknown
keys are refused at startup rather than half-read. `gauntlet doctor` lists
every agent it knows, defined ones included, and the file it read them from.
