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
| `gauntlet runs [--limit N]` | list recent runs recorded under `~/.gauntlet` |
| `gauntlet show <run-id>` | replay one run's journal |
| `gauntlet version` / `help` | print the version / this help |

`pick` opens a launcher drawn like the dashboard: reviews as collapsible sets
with a fill meter each and a one-line description beside every name, `suggest` as the first choice in that list (an agent
proposes the reviews, and the run pane names which agent does it), the agents
this machine has in the colors they will keep during the run, concurrency
metered against the CPU count, and the run switches. The composed command line
is on screen the whole time, and `enter` runs exactly that, so the flags are
learned rather than hidden. Picking nothing is not an empty run: it is every
review, which is what the composed command says by saying nothing. A run the
tree cannot support is refused with its reason on screen rather than composed
and failed on launch (concurrency above 1 needs a clean tree). It needs a
terminal on stdin and stdout, and takes `-C/--dir` and `--prompt-dir` to say
what it should offer.

| Key | Action |
|---|---|
| `tab` / `shift+tab` | move between reviews, agents, and run options |
| `up`/`down`, `j`/`k` | move within a pane |
| `space` | toggle a review, a whole set, an agent, or a switch |
| `left`/`right`, `h`/`l` | collapse or expand a set; change the job count |
| `a` | select all or none in this pane |
| `+` / `-` | raise or lower concurrency, from any pane |
| `/` | filter reviews by name or by what they do; `enter` keeps it, `esc` clears it |
| `enter` / `q` | run the composed command / leave without running |

## Options

Path values (`--dir`, `--dirs`, `--log`, `--prompt-dir`, and the path half of
`--bin TOOL=PATH`) expand `$VARIABLES` and a leading `~` before use.

**Choosing reviews**

| Flag | Default | Purpose |
|---|---|---|
| `-r, --reviews LIST` | all | Reviews and/or set names to run. The `-review` suffix is optional (`sec` means `sec-review`). Naming one twice runs it twice per loop. Repeatable. |
| `-x, --exclude LIST` | none | Reviews and/or sets to skip. |
| `-s, --suggest` | off | Shorthand for `--reviews suggest`: an agent inspects the repo and proposes the relevant reviews. |
| `--suggest-agent AGENT` | from `--agents` | Agent to run the suggest step, or `gauntlet` to choose from file signals instead of asking a model: it reads extensions, well-known filenames, and directory names, costs no tokens, and answers in milliseconds. It cannot tell a toy HTTP handler from a payment path; an agent can. |
| `--suggest-timeout DUR` | `30m` | Timeout for the suggest step. |
| `--prompt-dir DIR` | bundled | Use `*-review.md` files from DIR instead of the embedded set. |

Sets: `all`, `project`, `quick`, `standard`, `security`, `frontend`,
`backend`, `agents`, `shipping`. A `*-review.md` file in the reviewed tree is
picked up automatically and overrides a bundled prompt of the same name.

**Choosing agents**

| Flag | Default | Purpose |
|---|---|---|
| `-a, --agents LIST` | auto-detect | `tool` or `tool:model` entries; `mixed` means every installed agent. The model id is passed to the CLI verbatim. Repeatable. |
| `--bin TOOL=PATH` | none | Run an agent from a specific executable. Repeatable. |
| `--agent-cmd NAME=ARGV` | none | Define an agent gauntlet does not ship, e.g. `pi='pi -p {prompt}'`. Repeatable; `~/.gauntlet/agents.json` makes it permanent. |
| `--continue-sessions` | off | Resume each agent's session between reviews (reuses context, bleeds context). Ignored in `--jobs` mode. |

**Execution**

| Flag | Default | Purpose |
|---|---|---|
| `-C, --dir DIR` | cwd | Directory to review. |
| `--dirs LIST` | none | Review several directories in parallel; globs are expanded. Conflicts with `--dir`. Also accepted as `--target-dirs`, the name the Python tool used. |
| `--retries N` | 2 | Reruns of a failed review on the same agent, waiting longer each time (5s, then doubling, jittered). A run that exhausts them still falls back to another agent. Timeouts are never retried. |
| `-j, --jobs N` | 1 | Reviews at a time **per directory**; >1 uses worktree isolation and merges back. With `--dirs`, the agents running at once are `jobs x directories`. |
| `-t, --timeout DUR` | `30m` | Per-review timeout (`90s`, `30m`, `1h`, `2d`). |
| `--runtime DUR` | unlimited | Wall-clock budget for the whole run. |
| `-1, --once` | off | One loop, then stop. |
| `-n, --max-loops N` | unlimited | Stop after N loops. |
| `--seed N` | random | RNG seed for review order and agent picks, recorded in the journal so a rerun can replay it. Accepts any integer literal (`0x…` included); `0` derives one from the clock. |
| `-c, --commit` / `-p, --push` | off | After each review, an agent writes a commit message (no AI attribution) and commits on the branch you are on, optionally pushing it. Neither merges anywhere. |
| `--merge-into BRANCH` | none | After each loop, merge this branch's committed work into BRANCH, in a scratch checkout so your own is never switched. Needs `--commit` or `--push`, since only committed work merges. A conflict aborts, leaves both branches untouched, and makes the run exit nonzero. |
| `--yolo` | off | Drop the caution rules: no fix count or diff-size limit, public APIs may change. Containment is unaffected. It commits nothing on its own; it does answer yes when a `--jobs` run offers to commit a dirty tree. |
| `-y, --yes` | off | Skip the suggest confirmation. |
| `--semcode` | off | Build a semcode index before the loop. |

**Output and modes**

| Flag | Purpose |
|---|---|
| `doctor` | Report installed agent CLIs and helper tools. Exits 1 if no agent is usable. |
| `-l, --list` / `--dry-run` | Show reviews and sets / the planned schedule, then exit. |
| `--show-prompt REVIEW` | Print the exact composed prompt an agent would receive. |
| `--log FILE` | Also write all output to FILE. |
| `-q, --quiet` / `--raw` | Discard agent output / echo it verbatim instead of normalizing. |
| `--stream` | Ask agents for machine-readable output where they support it: live token counts, and the reasoning/output split shown separately in the feed. |
| `--no-color` | Disable color everywhere, the plain log and the dashboard/launcher both. The `NO_COLOR` environment variable does the same. |
| `--opencode-db` | Read opencode's SQLite session store for its token counts. The driver ships in a default build; a build without it refuses the flag at startup rather than measuring nothing. |
| `--tui` | Live dashboard on the alt screen, redrawing several times a second. It is off by default: plain scrolling output stays in the scrollback and reads linearly, which is the path for screen readers and copied transcripts. |
| `-V, --version` | Print the version. |

**Updating**

| Flag | Default | Purpose |
|---|---|---|
| `--hot-reload` | on | When this binary is replaced during a run (by `gauntlet update`, `make install`, or a rebuild), finish the reviews in flight and hand the rest of the loop to the new binary instead of exiting. |
| `--auto-update` | off | During a run, check for a new release shortly after start and every six hours, install it, and hand over at the next safe point like a hot reload. A failed check is reported and the run goes on. |
| `--update-repo REPO` | `maci0/gauntlet` | GitHub repository `gauntlet update` and `--auto-update` fetch releases from. |

## Environment variables

None is required; unset, everything lives under `~/.gauntlet`.

| Variable | Effect |
|---|---|
| `GAUNTLET_HOME` | Root of the state tree instead of `~/.gauntlet`: the run journal, hot-reload handoff files, and `agents.json`. |
| `GITHUB_TOKEN` | Optional. Sent only to api.github.com by `gauntlet update` and `--auto-update`, for a higher API rate limit. |
| `NO_COLOR` | If set at all, no color anywhere. Wins over the two below. |
| `CLICOLOR_FORCE` / `FORCE_COLOR` | Anything but empty or `0`: force color on, so piping through `less -R` keeps its palette. |
| `TERM=dumb` | Disables color unless forced. |

(`GAUNTLET_STATE` exists too, but only within one hot reload: it names the
handoff file passed across the exec.)

## Signals

| Signal | Effect |
|---|---|
| `SIGQUIT` (`Ctrl-\`) | Finish gracefully: no new review starts, the ones running end and land their work, then the run exits normally. `s` on the dashboard does the same. |
| `SIGINT` (`Ctrl-C`), `SIGTERM` | Terminate the running reviews and exit 130. A second one force-kills. |

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
token counts. The file is plain JSON: comments, trailing commas, and unknown
keys are refused at startup rather than half-read. `gauntlet doctor` lists
every agent it knows, defined ones included, and the file it read them from.
