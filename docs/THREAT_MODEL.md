# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree, typically with their permission systems
bypassed or auto-approved. This document is the systemic view; individual
vulnerability findings belong to sec-review and are recorded here only as
threats.

Last reviewed: 2026-09-27 against commit 4cdb72c. This pass read the eighteen
commits since the previous baseline (ef6eb5c) and changed no risk, no entry
point, and no gap; it re-anchored citations the build commits moved. Three
commits in that range changed the Makefile's shape, and the pointers into it
were not carried along: `make release` sits at `Makefile:446` and `make repro`
at `Makefile:508-532`, so every reference to the earlier anchors was stale and
`TestDocsPointAtTheMakefileLineTheyName` was red on both. That test only
checked the pointers naming `make release` and `GOVULNCHECK_VERSION`, which is
why the other five went unnoticed; it now covers the `repro` target and its
member list too. The `make repro` entry point and gap added at the previous
baseline still stand: it archives the whole working tree, so anything a
developer has in their checkout that is not in `.gitignore` is copied under
`$HOME/.cache/gauntlet/repro` for the length of the build
(`Makefile:508-532`; the archive's members come from git's ignore-aware
listing and tests hold the recipe to it, `cmd/gauntlet/makefile_test.go:865-981`).
The two controls that baseline added are re-anchored, not changed: a commit
subject an agent supplies is dropped for the generated one when it credits a
model or an agent CLI (`attributionRe`, `internal/runner/subject.go:55`),
because the trailer sweep on the finished message never reached the subject;
and the conflict-resolution marker scan covers the commit's whole scope rather
than only the paths git reported as conflicted (`commitScope`,
`internal/runner/conflict.go:102-119`), so a marker the resolver left in a file
it was not asked about blocks the merge. Citations that moved with those
commits were re-anchored (`internal/runner/conflict.go`, `subject.go`,
`internal/agent/notes.go`, `internal/runx/runx.go`), and one pointer was
corrected: `make release` is at `Makefile:447`, not 413, which had left
`TestDocsPointAtTheMakefileLineTheyName` failing. Owner and review cadence are
organizational decisions; none is assigned here.

The previous pass's baseline follows, retained rather than re-verified.

Last reviewed previously: 2026-09-27 against commit a6e6c7f. That pass
covered the twelve commits between dd793d8 and that commit. One named gap
closed: `--semcode`
indexer output now passes through the same display filter as every other child
stream (`normalize.NewDisplayWriter`, `internal/normalize/normalize.go:436-501`,
wired per stream in `runIndexer`, `cmd/gauntlet/semcode.go:86-108`), so R8 is
no longer a gap. One new surface: `doctor` reports which documented environment
variables this process saw, printing a secret-bearing name as `(set)` and never
as a value (`envSettingLines`, `cmd/gauntlet/doctor.go:289-311`; the table it
reads is `helpEnvVars`, `cmd/gauntlet/help.go:172-184`). New controls verified
against the code: the writer is per stream and flushed after `Run` joins the
copy goroutines (`normalize.go:443-463`, `semcode.go:92-100`), with a 1 MiB cap
on the partial line it holds so an endless line cannot grow the buffer
(`maxPendingBytes`, `normalize.go:414-418`); invalid UTF-8 is repaired on the
fast path of `Sanitize` too, so a hostile file name no longer renders
differently depending on what sits beside it (`stripControl`,
`normalize.go:265-282`); run IDs keep the whole pid, so two live processes
cannot share a journal file (`runIDFor`, `internal/journal/journal.go:69-73`);
atomic writes are one helper with a `Sync()` and a rename
(`gauntlethome.WriteFileAtomic`, `internal/gauntlethome/gauntlethome.go:138-161`),
which `SaveState`, the dsh overlay patch, and the journal index all call, so a
partial write is no longer a shape each of the three can have differently; the
picker's `q` arms before it discards a composed run, like the dashboard's
(`quitArmed`, `internal/ui/pick.go:301-309`); and every environment name the
binary reads is pinned to the documented table or to a stated reason it is
internal (`env_surface_test.go:31-33,35-59`). Re-anchored the citations that
moved: `normalize.go` (the display path gained the writer),
`internal/selfupdate/selfupdate.go` (the temp-file helpers moved out), and
`internal/journal/history.go`. The nine commits after it (through d5d79a9) were
read for surface changes rather than re-verified end to end. Four touch
something this document covers: agent discovery falls back to absolute PATH
prefixes when `PATH` is empty, so a box with no `PATH` resolves an agent CLI
from `$HOME/.local/bin` and the system prefixes, which widens the set of
executables a run will launch and sits under no numbered risk here; the
display writer's pending-line bound cuts at a rune start, which is a fix to the
R8 path rather than a new surface; `--tui --log` writes the log under one lock
so a signal line cannot be spliced into an agent's line; and release artifacts
are built with one pinned Go release, which narrows R2 rather than widening it.
The two `Makefile:` pointers above were re-anchored when the Makefile moved
under them, and re-checked again this pass (both still land on the line they
name). The twelve commits after d5d79a9 (through 15b8fa9) moved one citation
set and corrected one claim. The correction is the more important of the two:
this document asserted three times that `show`'s journal replay isolated a pager
environment with `runx.AbsPATHEnv()` (`cmd/gauntlet/runs.go:170`). `show` spawns
nothing. It writes a `bufio.Writer` straight to the caller's stdout
(`cmd/gauntlet/runs.go:108-140`), imports no `os/exec` and no `runx`, and no
`PAGER` or `LESS` lookup survives anywhere in the tree, so the mitigation named a
process that does not exist. The sanitization it was hanging on is real and is
still there (`normalize.Sanitize`, `cmd/gauntlet/runs.go:132`); the pager is not,
and the three PATH claims that listed a pager alongside the agents, git, `gh`,
and the usage probe no longer count it. The moved citations are the other half:
splitting the dashboard and launcher into state and view halves
(`internal/ui/view.go`, `internal/ui/pick_view.go`) took about 700 lines out of
each of `ui.go` and `pick.go`, so every pointer into those two files was
re-anchored on the symbol rather than the old number, including the picker's
launch gate, which now reads its reason from `blocked`
(`internal/ui/pick_view.go:114-125`, consulted at `internal/ui/pick.go:328-332`)
instead of where it used to sit. Two surfaces this pass added: `runs --limit N`,
which is an unbounded number reaching a local allocation and is now capped by
`maxTailHint` before it reserves anything (`internal/journal/index.go:833-852`),
and the run seed, which is resolved once per run and carried across a hot
reload, so the seed the journal records now replays the draws it names
(`cmd/gauntlet/reload.go:106-110`, `internal/runner/draw.go:45-110`). The
2026-09-26 and 2026-09-27 baselines for
everything else are retained rather than re-verified; this is not a full
assurance claim. Owner and review cadence are organizational decisions; none is
assigned here.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | Prompt injection from the reviewed tree drives an agent running with bypassed or auto-approved permissions | B1 -> B2 | Accepted by design; containment is advisory text plus process discipline, never an OS sandbox |
| R2 | Self-update integrity rests on TLS and repository ownership; `checksums.txt` authenticates nothing beyond transport consistency, and hot reload execve's the replaced binary automatically | B4 | Named gap |
| R3 | A prompt-injected or compromised agent reads every secret its user can: environment-inherited API keys, agent config stores, SSH keys, `~/.netrc` | B2/B5 | Consequence of R1; containerization is the documented answer (DESIGN.md non-goals) |
| R4 | Confidentiality of reviewed source: agents send code to third-party model APIs over the network | B2 | Inherent to the tool's purpose; users must know it |
| R5 | `dsh` without a launcher on PATH falls back to `bunx`, fetching `@deepseek-ai/dsh` from the npm registry and executing it; a `dsh:<model>` pin also runs that argv as `--dump-config` before the review | B4 | Named gap (deliberate feature, unreviewed supply-chain hop) |
| R6 | Agent resource consumption or a failed usage probe exhausts host capacity or provider budget | B2 | High when reviewing hostile content: parser caps are not CPU, disk, network, or spend quotas. `--token-budget` now bounds the tokens the run's own reviews report, but the counters are agent-reported, exclude the commit and conflict steps, and are checked between reviews, not inside one (`budgetExhausted`, `internal/runner/runner.go:640-648`); the usage limit probe runs isolated from the repository but fails open on errors (`internal/runner/exec.go:100-143`, `internal/runner/usagelimit.go:46-72,84-113`) |
| R7 | `--log` persists source or credentials quoted in output at an operator-selected path | B3/B6 | Conditional on enabling logging; a symlink or non-regular destination is refused, the open carries `O_NOFOLLOW` and 0600 with a post-open chmod, but content is not secret-redacted and the parent directory is not confined (`openLogFile`, `cmd/gauntlet/main.go:672-696`) |
| R8 | `--semcode` indexer output carrying file names from a hostile tree reaches the operator's terminal | B1 -> B3 | Closed: both of the indexer's streams pass through `normalize.DisplayWriter`, one writer per stream, flushed after the child exits (`cmd/gauntlet/semcode.go:86-108`, `internal/normalize/normalize.go:414-501`) |
| R9 | The run history is bounded and evicted on every run (`--keep-runs`, default 200), and eviction is ordered by the run ID's timestamp rather than by a protected property of the journal | B6 | Partly closed. Eviction moves the journal to `pruned/` and `gauntlet runs --restore` moves it back, so a wrong bound is recoverable until the same number of newer runs has replaced it; what remains is a run older than that, which no bound distinguishes. The move is confined to real shard directories holding validated run IDs, and the run just finished is the newest row, so a skewing clock or a hostile planted file under `runs/` is what puts evidence out of reach (`internal/journal/retain.go:36-165`, `internal/journal/quarantine.go`, `internal/journal/index.go:571-614`) |

The order reflects reachability and blast radius, not measured likelihood.
R1/R3/R4 require only content reaching a launched agent; R2 requires control
of the update publisher or executable path; R5 requires selecting the fallback.
No server authentication boundary is claimed here: the operator's OS account
supplies child-process authority (`internal/runner/exec.go:107-111`), and
publication uses that account's Git credentials (`internal/runner/commit.go:95`).

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  (DESIGN.md "Isolated parallel reviews", rule 1), and why the runner rewinds
  only its own worktrees to a base commit between retry attempts
  (`git reset --hard` + `git clean -fd`, `ResetToBase` in
  `internal/gitx/worktree.go:504-522`). In-place retries restore a snapshot of the
  user's checkout taken before the attempt (`Snapshot`/`Restore` in
  `internal/gitx/snapshot.go`): the user's own uncommitted files come back,
  the failed attempt's edits do not, and HEAD is never moved past that
  snapshot.
- **Credentials reachable by the user account**: cloud keys, SSH keys,
  `GH_TOKEN`/`GITHUB_TOKEN`, every agent's own API-key store (`~/.claude`,
  `~/.gemini`, and peers). Agents run with full user privileges.
- **Reviewed source confidentiality**: leaves the machine through each agent's
  model API calls.
- **The gauntlet binary itself**: self-update replaces it on disk and hot
  reload re-executes it mid-run (`internal/selfupdate`).
- **Host availability**: reviews run unbounded CPU/network inside the timeout
  window (`internal/runner/exec.go`).
- **Audit trail**: the journal under `~/.gauntlet`
  (`internal/journal/journal.go`) and the commits each run leaves behind.
  Optional `--log` files also retain displayed output, potentially including
  source and credentials quoted by an agent (`openLogFile`, `cmd/gauntlet/main.go:672-696`).

## Trust boundaries

- **B1, reviewed repo <-> gauntlet process.** Everything read from the target
  tree is hostile: prompt files, file names, `.git/config`, `.gitattributes`.
  Mitigations are listed per entry point below.
- **B2, gauntlet <-> spawned agents.** The privilege transition of the whole
  system: gauntlet execs agents so they can edit the tree without stopping
  for approval. How that is spelled depends on the CLI
  (`internal/agent/agent.go` `buildBuiltin`): `claude
  --dangerously-skip-permissions`, `codex exec
  --dangerously-bypass-approvals-and-sandbox`, `grok --permission-mode
  bypassPermissions`, `agy --dangerously-skip-permissions`, `gemini`/`qwen
  -y`, `opencode run --auto`, `crush run` (the CLI auto-approves that
  session), `kimi -p` (prompt mode auto-approves). `dsh` and `clanker` take
  permissions from their own config; `cursor-agent` is invoked `--print -f`
  with no extra bypass flag in argv. Custom agents (`--agent-cmd`,
  `~/.gauntlet/agents.json`, the pi family in `custom.go`) use operator-defined
  argv: gauntlet does not add a bypass flag. With `--commit` (also implied by
  `--push`), one launch per loop receives commit instructions; the runner
  verifies tracked-file cleanliness, strips attribution trailers, and performs
  the requested push (`internal/runner/commit.go:137-222`). This divides
  workflow responsibility, not OS authority: the agent still inherits the
  user's credentials and can disobey the prompt's no-push instruction. The same
  hand-off exists outside a loop: when `--jobs` refuses a dirty tree,
  `commitFirst` offers to give the uncommitted work to one agent, consented
  by `--yes`/`--yolo` or an interactive confirmation, never on an unattended
  guess (`cmd/gauntlet/main.go:875-905`, executed by `runner.CommitNow`,
  `internal/runner/commit.go:47`). A third launch, `resolveConflict`
  (`internal/runner/conflict.go:37`), hands a permission-bypassed agent a
  scratch checkout of a conflicted merge; the runner commits and merges only
  if conflict markers are gone. The agent then acts with the user's
  authority inside the reviewed tree, with network access. Containment is
  prompt-level (embedded rules in `internal/prompt/rules/`, composed in
  `internal/prompt/compose.go`) plus process discipline (own session -- which
  detaches the controlling terminal, so an agent cannot open /dev/tty and put
  the operator's terminal into a raw mode where Ctrl-C stops generating
  SIGINT; the kernel's SIGTTOU guard does not cover a runtime that ignores
  SIGTTOU, which Node-style CLIs routinely do -- stdin is the null device,
  child environment isolated to absolute-only PATH via `runx.AbsPATHEnv()`,
  hard timeout with SIGKILL escalation, deferred process-group SIGKILL on
  normal exit to reap orphaned grandchild processes, and SIGKILL escalation on
  drain timeout to unblock readers when orphaned processes hold open stdout/stderr
  pipes (`internal/runner/exec.go:100-350, 140-146, 327-334, 453-485`). There is
  deliberately no OS sandbox (DESIGN.md non-goals).
  Anything crossing B1 that reaches the prompt crosses into B2 with this
  advisory fence as the only gate.
- **B3, agents <-> user terminal, dashboard, journal.** Agent output is
  untrusted display input; sanitization before any terminal write, including
  the two inspection paths (`--show-prompt`, `cmd/gauntlet/modes.go:65-66`;
  `show`'s journal replay, `cmd/gauntlet/runs.go:132`, which writes through a
  `bufio.Writer` to the caller's stdout and spawns nothing, so there is no
  pager environment to isolate) and the dashboard feed
  (`internal/ui/ui.go:683-687`).
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload. The
  publishing side of the same channel is GitHub Actions: `release.yml` builds
  and uploads the assets `update` verifies (the write token is in the
  environment only for that upload), and `vulnscan.yml` runs
  govulncheck weekly and on `go.mod`/`go.sum` pull-request changes and main
  pushes (`.github/workflows/vulnscan.yml:10-22`). Actions are commit-pinned,
  the runner uses `ubuntu-24.04`, and checkout disables persisted credentials.
  The scanner is version-pinned through `GOVULNCHECK_VERSION` in `Makefile:51`,
  invoked by `make vuln` (`.github/workflows/vulnscan.yml:33-44`). Release and
  checksum downloads enforce `validateAssetURL` across HTTP redirects and cap
  redirects at 10 (`client.CheckRedirect`, `internal/selfupdate/selfupdate.go:145-157`).
  Release checksum verification uses constant-time comparison
  (`subtle.ConstantTimeCompare`, `internal/selfupdate/selfupdate.go:266`), and the
  downloaded binary is flushed with `Sync()` before atomic replacement (`selfupdate.go:269`).
  Stacked publication is a second internet path: the `gh` CLI, resolved from an
  absolute-only PATH (`internal/ghx/ghx.go:97, 239`), using the operator's own
  `gh` credentials, not the `GH_TOKEN` gauntlet reads for self-update.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward GitHub (only when `GH_TOKEN` or
  `GITHUB_TOKEN` is set, and only on gauntlet's own HTTPS client) and toward
  each agent's model provider. Git stderr is redacted for embedded userinfo
  credentials before errors are printed or journaled (`internal/gitx/gitx.go:358-364`).
  `gh` and `git push` use whatever credentials those tools already have.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, `agents.json`. This is also the one
  boundary where the run deletes: `--keep-runs` prunes the history at the end
  of every run (`internal/journal/retain.go:36-140`). Journal paths are
  guarded by run ID validation (`validRunID`, `internal/journal/history.go:88-104`)
  and date-sharded from run ID timestamps (`shardFromRunID`, `internal/journal/history.go:167`).
  Optional `--log` crosses into a separate operator-selected output path, not
  necessarily that state directory (`openLogFile`, `cmd/gauntlet/main.go:672-696`), validated to
  not name an existing directory (`validateLog`, `cmd/gauntlet/flags.go:794-819`).

## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | git `ls-files` glob, walk fallback, `internal/prompt/discover.go`; read `prompt.go` via `readNoFollow` (`prompt.go:278-329`) | prompt discovery candidates inspected with `os.Lstat` requiring regular files (`discover.go:244,278`); regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap; skipDirs and hidden directories dropped; git-ignored files refused; project prompts override bundled ones of the same name; duplicate detection reads bounded |
| Prompt names (file stems) | `discover.go:68,88` | NFC-normalized keys (`prompt.go:338-340`); control/Cf characters reject the file with a warning (`discover.go:70,90`; strip at `prompt.go:342-346`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:88-108`, fed to the suggest catalog `compose.go:247-294,304-313` | the prefix is matched on the line as written, before sanitize repairs it, so a line the file never wrote as a goal cannot supply a description; the extracted value is sanitized and NFC-normalized; name and description both fenced: `</catalog>` and `RELEVANT:` neutralized; 200-rune cap on a rune boundary (`compose.go:247-294,304-313`) |
| Prompt `Summary:` line | `prompt.go:153-171`, fed to stacked PR bodies `internal/runner/prbody.go` | sanitized, cut to 60 runes at read, and normalized to NFC before truncation (`prompt.go:159-170`), the goal-line fallback bounded the same way; PR rendering strips controls, flattens to one line, NFC-normalizes to prevent rune splitting, and bounds again (`prbody.go:26-44,86,146-173; stack.go:655`) |
| Prompt `Signals:` line | `prompt.go:199-232`, consumed by the file-signal suggester `internal/runner/suggest_fast.go:527-556` | known kinds only (`ext`/`name`/`path`/`mark`), charset-restricted values, 12 tokens × 40 runes; anything else dropped |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR`, `--dirs` | `cmd/gauntlet/flags.go`, `cmd/gauntlet/paths.go`; parsed by `ParseSpecs` (`agent.go:336`), `ParseBin` (`agent.go:648`), `ParseAgentCmd` (`custom.go:346`), `discover.go:44-77`, `resolveDirs` (`paths.go:23-86`) | allow-listed tool names; dsh model charset-restricted (`dshModelRe`); `@effort` charset-restricted for every agent and refused where no verified flag exists (`effortRe`, `takesEffort`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`ParseBin`); non-empty checks for `--agents`, `--bin`, `--agent-cmd`, `--usage-cmd`, `--suggest-agent`, `--exclude`, `--merge-into`, `--pr-base`, `--show-prompt` (`cmd/gauntlet/flags.go:560-660`); target dirs expanded and deduplicated by realpath (`paths.go:23-86`); `--prompt-dir` takes regular files only, validated to be a directory if existing (`flags.go:775-788`), control-char names rejected; `--log` validates destination is not a directory (`validateLog`, `flags.go:794-819`); subcommands refuse flags they do not read (`rejectStrayFlags`, `flags.go:535,874`); custom agent loading gated to subcommands using agents (`usesAgents`, `flags.go:555,821-830`); subcommand peeling ignores empty arguments and lone dashes (`flags.go:1002-1030`); branch and revision commands separate options with `--` and `--end-of-options` (`branch.go:51,90,125,146,177,276,289,363,397,429,440,456,478,489`, `worktree.go:278,308,436,509,530,583,604,788`, `gitx.go:667,686`) |
| Run budgets: `--runtime`, `--token-budget` | `cmd/gauntlet/flags.go:341-344`; `budgetExhausted` (`internal/runner/runner.go:640-648`), `Stats.Tokens` (`internal/runner/stats.go:177-185`) | operator-set, both default 0 (unlimited) and both refused negative (`flags.go:648-659`); the token ceiling is a count of what the reviews themselves reported, summed across loops, lanes, and a hot-reload predecessor, so it is a scheduling bound, not a spend quota: it excludes the commit and conflict launches, is checked between reviews rather than during one, and a review that under-reports its tokens lowers nothing else |
| `--keep-runs N` history prune | `journal.Prune` (`internal/journal/retain.go:36-140`) called from `writeSummary` (`cmd/gauntlet/main.go:846-848`); default 200 (`cmd/gauntlet/flags.go:201-206`) | `N <= 0` keeps everything; negative refused (`flags.go:654-656`); deletion is taken from a walk that takes only real shard directories (`d.IsDir()`, `internal/journal/index.go:571-593`) and only `<id>.jsonl` files whose stem passes `validRunID` (`internal/journal/history.go:88-104`), so a symlinked shard or a planted name never widens the blast radius; the index row is dropped before its journal, the rewrite is under the index lock, an index line is capped at 4 MiB (`indexLineMax`, `retain.go:169-173`), and a prune failure is a warning that leaves the tree growing |
| `runs --limit N` index listing | `fs.IntVar(&o.runsLimit, "limit", ...)`, `cmd/gauntlet/flags.go:403`; refused on any subcommand but `runs` (`flags.go:532,868`); read by `journal.Recent` into `parseTail` (`internal/journal/index.go:852`) | operator-set on local state, but the number is unbounded, so it no longer sizes an allocation: `parseTail` reserves `min(n, maxTailHint)` with `maxTailHint = 1024` (`index.go:833-838`), because a `Summary` is a few hundred bytes and `runs --limit 100000000` otherwise reserved gigabytes for an index holding a handful of rows. The cap is a hint, not a limit: append still grows the slice to what the index really holds |
| `doctor` state-root probe | `stateRootProblem` (`cmd/gauntlet/doctor.go:329-357`) | diagnostic only, but it writes: a temp file named `.gauntlet-doctor-*` in the state root, closed and removed before the check reports, and an unwritable or undeletable root is now the command's failure verdict (`doctor.go:260-266,278-280`) rather than a line that scrolls past; a root that does not exist yet is not probed, so doctor creates nothing the first run would not |
| `.git/info/exclude` append | `ExcludeOwnArtifacts` (`internal/gitx/worktree.go:73-116`) | the reviewed tree picks `gitDir`, so `MkdirAll` and `OpenFile` would follow a planted `.git` symlink or gitfile out of the repository; the parent directory is re-checked with `os.Lstat` requiring a real directory (`realDir`, `worktree.go:122-133`) and the append is skipped when it is not |
| Environment: `PATH` | `pathNoCWD` (`agent.go:173`), `resolveGit`/`gitEnv` (`gitx.go:68-96,375-412`), `ghx.binary` (`internal/ghx/ghx.go:97-111`), `probeEnv`/`resolveProbe` (`usagelimit.go:84-113`) | cwd-relative and relative entries dropped for agent, git, git-child, `gh`, and usage probe resolution; child process environments isolated to absolute-only PATH via `CleanPATH`/`AbsPATH`/`AbsPATHEnv` (`internal/runx/runx.go:111-133`) and executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths (`internal/runx/runx.go:150-166`) |
| Environment: `HOME` (through `os.UserHomeDir`), `GAUNTLET_HOME`, `GH_TOKEN`/`GITHUB_TOKEN`, `TERM`/`NO_COLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR`, `GAUNTLET_STATE`, `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, `REDUCED_MOTION`, `GIT_SSH_COMMAND` | `internal/gauntlethome/gauntlethome.go:33-52`, `selfupdate.go:84-108`, `cmd/gauntlet/report.go:46-86`, `internal/selfupdate/reload.go:22,188-196,220-244`, `internal/ui/view.go:385-398`, `internal/envx/envx.go`, `internal/gitx/gitx.go:378-393` | operator-controlled, same-user trust; the list is closed by a test that walks non-test source for `os.Getenv`/`os.LookupEnv` and requires every literal, constant, or ranged-over name to be in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`), so a new knob cannot ship undocumented; `HOME` is the default state root only, and a home that cannot be read degrades to a local `.gauntlet` that custom agent loading refuses (`internal/gauntlethome/gauntlethome.go:28-46`); `GAUNTLET_HOME` validates unresolvable environment variables at startup (`flags.go:545-552`) and degrades safely to local `.gauntlet` (`internal/gauntlethome/gauntlethome.go:33-52`); expands tildes and environment variables and is made absolute at resolution so the state root cannot depend on the current directory (`CustomFilePath`, `custom.go:474`); `GAUNTLET_STATE` is read only where the process set it (`reload.go:188-196`) and is replaced on re-exec rather than inherited (`Reexec`, `reload.go:220-244`); its handoff file is verified by `LoadState` to require `.json` extension, absolute path, clean path without traversal, verified via `os.Lstat` as a regular file (rejecting symlinks and directories without deleting), and read capped to 16 MiB (`maxHandoffBytes`, `reload.go:179-188,198-208`); `SaveState` validates `runID` against directory traversal and path separators (`reload.go:136`) and flushes with `tmp.Sync()` before atomic rename; `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, and `REDUCED_MOTION` disable TUI animation glyphs (`internal/ui/view.go:385-398`); the values that mean off for those three and for `CLICOLOR_FORCE`/`FORCE_COLOR` are one list, trimmed and case-folded, in `internal/envx/envx.go`, and `TERM` is compared against `dumb` the same way, so a wrapper exporting `TERM=DUMB` or a padded value still loses its palette (`cmd/gauntlet/report.go:48-84`); `GIT_SSH_COMMAND` overrides repository-local SSH commands and empty or whitespace-only values default to `ssh` (`internal/gitx/gitx.go:378-393`); color variables configure ANSI output (`cmd/gauntlet/report.go:46-86`); `--update-repo` is `owner/repo` only (`ParseRepo`, `selfupdate.go:65`) |
| Agent stdout/stderr | pipes in `exec.go:115-136`; line scan `exec.go:370-420` | 4 MiB per line emitted in chunks with UTF-8 rune boundary preservation (`exec.go:34-42,380-394`), trailing CR stripped (`exec.go:365-367`), escape/control/bidi/separator strip before terminal (`internal/normalize/normalize.go:373 Display`), width cap 2000 cols (`exec.go:47`), rate limit 200 lines/s (`runner.go:26`); ANSI CSI sequences terminated on standard final characters in token width calculation (`internal/ui/theme.go:246-283`); stalled command groups force-killed with SIGKILL on drain timeout (`exec.go:321-328`); even `--raw` passes `Display` (`exec.go:269-276`); usage and sink callbacks serialized across stdout and stderr streams (`exec.go:169,196-198,208,226-228`) |
| Stream-JSON events from agents | `internal/runner/exec.go:245-265`; `internal/streamjson/streamjson.go:81-99,107-138,199,202,217-248` | extraction depth capped at 8; role and type values normalized case-insensitively; objects marked role `tool`/`user` or type `user`/`tool_use`/`tool_result` contribute neither text nor usage; classification is not origin authentication; malformed JSON falls back to plain text and `--raw` bypasses classification |
| Token counters parsed from output and transcripts | `exec.go:167-203,334-346`; transcript watch off with `-tags notoktop`, `transcript_toktop.go:17`; custom roots `custom.go:69` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:159` | HTTPS, 4 MiB decode cap; bounded by `fetch` (`selfupdate.go:362-404`); `owner/repo` charset-checked before it is concatenated into the API path |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:221` | validated by `validateAssetURL` (`selfupdate.go:287-324`) to require HTTPS and authorized GitHub release hosts (`github.com`, `api.github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests); HTTP requests routed through `getAsset` (`selfupdate.go:324-344`); HTTP redirects re-verify `validateAssetURL` and are capped at 10 (`client.CheckRedirect`, `selfupdate.go:145-157`); checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap; SHA-256 verified using constant-time comparison (`subtle.ConstantTimeCompare`, `selfupdate.go:266`); downloaded binary flushed via `tmp.Sync()` (`selfupdate.go:269`) before atomic replace; `fetch` rejects responses exceeding size limit (`selfupdate.go:362-404`); download and error paths drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:184,187,346,356,373,393,396,349,358`); remote error messages decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:180,345-355,362-380`); a listing entry counts only as 64 hex digits (`checksumFor`/`isHexDigest`, `selfupdate.go:405-429`) |
| `gh` CLI JSON and PR URLs | `internal/ghx/ghx.go` `Find`/`Create` | argv-only; executable invoked with `runx.AbsPATHEnv()` (`internal/ghx/ghx.go:239`); PR URL must be `https` on the expected host (`validateURL`, `internal/ghx/ghx.go:222-228`); a PR is reused only when its head owner matches the push destination (`ownsHead`, `internal/ghx/ghx.go:161-170`) |
| Git outputs (shortstat, porcelain, check-ignore) | `gitx.go:431,705,788` | regex/line parsing; CRLF `\r` stripped (`gitx.go:715,799`); C-quoted paths decoded (`unquoteC`, `gitx.go:854-916`) then display-sanitized before any message (`safePaths`, `internal/runner/sanitize.go:14-19`; dashboard `internal/ui/ui.go:683-687`; plain reporter `report.go:87-100`); counts only, never executed |
| Reviewed repo's `.git/config` and `.gitattributes` | every git call, `internal/gitx/gitx.go:43-67,233-274,321-372` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external`, `core.gitProxy` forced empty; `protocol.ext.allow=never`; `attr.tree` pointed at the empty tree so in-tree `.gitattributes` cannot select a smudge filter or merge driver; local `filter.*`/`merge.*`/`diff.*` commands, `core.editor`, `core.askpass`, and peers blanked from `--local --list`; extra safe config propagated to worktrees and sub-repos via `subRepo` (`internal/gitx/gitx.go:648-663`, `internal/gitx/worktree.go:33-51`); git resolved on absolute-only PATH so a planted `./git` cannot run; `GIT_SSH_COMMAND=ssh` outranks a repo-local `core.sshCommand` unless the operator already exported one, and empty or whitespace-only values default to `ssh` (`mergeGitEnv`, `gitEnv`, `gitx.go:378-393`); git's own children die with its process group and bounded pipe wait (`gitx.go:349-357`); git stderr redacts embedded userinfo credentials (`gitx.go:358-364`); branch and revision commands separate options with `--` and `--end-of-options` (`internal/gitx/branch.go:51,90,125,146,177,276,289,363,397,429,440,456,478,489`, `internal/gitx/worktree.go:278,308,436,509,530,583,604,788`, `internal/gitx/gitx.go:667,686`) |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:123-139` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized and NFC-normalized before it reaches a terminal or file (`lock.go:22-25,94-119,123-139`); `Note` and `readNote` handle `EINTR` and partial writes in retry loops (`lock.go:99-119,123-139`); lock release preserves the descriptor flock and truncates the note to prevent file deletion and inode recycling races (`lock.go:161-175`) |
| Conflicted paths interpolated into the conflict prompt | `git diff -z` into `runConflictAgent`, `conflict.go:130-183`, then `ConflictPrompt` (`compose.go:202-242`) | a path is named only when it equals `normalize.Sanitize(p)` and `conflictPathOK` (control/Cf omitted, `RESOLVE:` and `</files>` omitted, 1024-rune cap); 50-file cap (over-cap skips the launch); list fenced in `<files>`; human-facing conflict hints quote branch and message via `runx.ShQuote` (`conflict.go:185-187`); a dropped path is still scanned for markers, so it holds the branch with a human |
| Helper-tool inventory appended to prompts | PATH probe at startup, `internal/runner/runner.go:617-625`, rendered `compose.go:95-125` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| File-signal suggester tree walk | `suggest_fast.go:570-604` | 100k files, depth 12, 2k file heads × 4 KiB; opens via `os.OpenRoot` (`suggest_fast.go:687-692`) so a symlink or path that escapes the reviewed tree is skipped |
| `~/.gauntlet/agents.json` | `internal/agent/custom.go:87-225,293,346,369-406,474-480` | rejects null, unknown fields, duplicate keys (including NFC-normalized case-variant duplicates and nested usage duplicates), trailing data, and built-in redefinitions; requires `{prompt}` in argv exactly once and forbids `{prompt}` in model/effort/stream/continue; requires `{model}` in model and `{effort}` in effort if set; forbids `{model}` and `{effort}` across stream and continue; forbids model when argv contains `{model}` and effort when argv contains `{effort}`; forbids placeholders in argv executable, usage roots, and usage suffix; rejects empty/blank arguments in argv, model, effort, stream, and continue; rejects whitespace-only notes; expands tildes and environment variables in custom executable `cmd[0]` (`agent.go:490-496`); validates non-empty usage roots and model; rejects whitespace-only usage suffix; validates all definitions before registration; strips UTF-8 BOM if present (`custom.go:377`); `CustomFilePath` (`custom.go:474`) returns empty without a state root; file read has no size cap and follows symlinks, so state storage must remain trusted |
| `--usage-cmd` probe | `internal/runner/usagelimit.go:84-113` | argv-only; isolated execution directory (`os.TempDir()`); relative and cwd-relative entries dropped from `PATH` in `probeEnv()` and executable resolved against absolute-only PATH (`resolveProbe`, `runx.LookPath`); child process environment isolated via `runx.AbsPATHEnv()`; 10s deadline, 4 KiB per output stream; own process group killed on return as well as cancellation; final non-empty line parsed as finite 0-100, rejecting NaN and Inf (`parseUsagePercent`, `internal/runner/usagelimit.go:125-154`); usage limit check guards against NaN (`usagelimit.go:47`); failure warns once and leaves the threshold unenforced; finish flag set via atomic swap (`internal/runner/usagelimit.go:68-71`) |
| `--log FILE` destination | `openLogFile`, `cmd/gauntlet/main.go:672-696`; `validateLog`, `flags.go:794-819` | startup rejects an existing directory target; at open an `os.Lstat` refuses a symlink and any non-regular file, the open itself carries `syscall.O_NOFOLLOW` with 0600, and a regular file that survived is chmod'd to 0600 before any output; no parent-directory confinement, no size cap, and no rotation |
| `--semcode` indexer | `buildSemcodeIndex`/`runIndexer`, `cmd/gauntlet/semcode.go:86-108` | `semcode-index` resolved through `agent.Resolve` (`pathNoCWD`, `internal/agent/agent.go:228-236`), so a planted `./semcode-index` in the tree cannot win; argv-only, no shell; child environment isolated with `runx.AbsPATHEnv()`; 30-minute per-directory cap with a deferred process-group SIGKILL (`runx.Guard`, `runx.KillGroup`); the child's working directory is the reviewed tree, so the tree decides both what it walks and the file names it reports, and each of its streams gets its own `normalize.DisplayWriter` (`cmd/gauntlet/semcode.go:91-100`), which passes every complete line through `Display` and holds an unterminated one until `Flush` after `Run` joins the copy goroutines (`internal/normalize/normalize.go:414-501`); the held partial line is capped at 1 MiB and released on a rune boundary (`normalize.go:471-501`) |
| `doctor` environment report | `envSettingLines`, `cmd/gauntlet/doctor.go:250,289-311`; table `helpEnvVars`, `cmd/gauntlet/help.go:172-184` | reads only the documented names, so nothing the operator did not set for gauntlet is echoed; a variable the table marks secret is reported as `(set)` and never as a value, so a pasted `doctor` transcript carries no token (`doctor.go:296-306`); `GAUNTLET_HOME` is left out because the State line above already names it; nothing here interprets the value, and presence is the only thing reported. A test pins the set: every `os.Getenv`/`os.LookupEnv` name in non-test source is either in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`) |
| `dsh --dump-config` probe | `dshDefaultProvider`, `internal/agent/dsh.go:74-111` | runs only for a `dsh:<model>` pin; child environment isolated with `runx.AbsPATHEnv()` (`dsh.go:81`); bounded to 4 MiB output via `runx.Bound` (`dsh.go:65,84`); own process group, 120s cap, deferred group SIGKILL via `runx.KillGroup` (`dsh.go:85`); provider parsed with a narrow regex (`dshProviderRe`); overlay values charset-restricted before they are quoted into YAML (`dshModelRe`, `agent.go:323`); provider and model validated against `dshModelRe` (`dsh.go:135-136`); overlay key rejects path separators and traversal (`dsh.go:116-133`) |
| Interactive launcher / picker keyboard input | `cmd/gauntlet/pick.go`, `internal/ui/pick.go`, `internal/ui/ui.go` | navigation keys jump to bounds (`g`/`G` in `internal/ui/pick.go:364-367`); cursor clamped to valid review rows via `clampReviewCursor` (`internal/ui/pick.go:320,409,412,454-464`); Esc/Ctrl-C during filter editing resets typing and clamps cursor (`internal/ui/pick.go:407-409`); empty filter matches block launch, with the reason returned by `blocked` and checked on Enter (`internal/ui/pick_view.go:114-125`, `internal/ui/pick.go:328-332`); Enter key terminates completed runs (`internal/ui/ui.go:425-429`) |
| Planted symlinks/FIFOs in the tree | prompt discovery inspects candidates with `os.Lstat` requiring regular files (`prompt/discover.go:244,278`); prompt reads `prompt.go:278-329`, lock creation `runner/lock.go:53-89`, untracked counting `gitx.go:543-625`, reload handoff `reload.go:188-196` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stats, size caps; stat errors propagated on open regular files (`gitx.go:545-557`); `LoadState` verifies regular file with `Lstat` (`reload.go:187-196`) |
| Developer build surface: `make repro` archives the working tree | `Makefile:508-532`; member list and archive `Makefile:516-517`; the tests holding the recipe to git's ignore rules `cmd/gauntlet/makefile_test.go:865-981` | the target is a developer convenience, not a shipped code path, but the archive is the whole checkout: it is written to `$(HOME)/.cache/gauntlet/repro` and the recipe refuses to run with `HOME` unset rather than writing to `/.cache`; the members are `git ls-files --cached --others --exclude-standard`, so a build output, cache, or local state that `.env`, `.gauntlet/`, and `.gauntlet.lock` already are cannot be copied, a new `.gitignore` entry covers the archive the moment it is written, and a pattern tar would match too broadly no longer decides; the directory is removed on exit by a shell trap. What the list cannot express is a file a developer has not ignored: an untracked credentials file is archived to `$HOME/.cache` for the duration of the build, where it is neither reviewed nor redacted |

## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and both the opening and closing
marker strings in the body are escaped (`compose.go:170-175`). The agent that
receives it has its own permission system disabled or auto-approved (or, for a
custom agent, whatever the operator put in argv). Applicable classes: elevation of privilege (repo
author -> code execution with user rights), tampering (working tree,
commits), info disclosure (secrets, source exfiltration). The fence is
advisory: the ground rules forbid git, deletion, and persistence, and
prompt-review gets a narrow exception (`compose.go:157-164`), but nothing
technical stops a sufficiently persuasive prompt from talking an agent out of
them. This is the accepted core risk; DESIGN.md answers it operationally: run
untrusted repos in a container.

**B1 -> B3 (the indexer path).** `--semcode` builds the optional semantic
index before the first review launches, and the indexer is the one child
process whose output is not parsed: it names files and symbols out of a tree
the reviewed repository controls, so its stdout and stderr are untrusted
display input like any agent stream. It is now filtered as one:
`runIndexer` gives each stream its own `normalize.DisplayWriter`
(`cmd/gauntlet/semcode.go:91-100`), which passes every complete line through
`Display` and holds a partial line until the child is done, then `Flush`es
after `Run` has joined both copy goroutines
(`internal/normalize/normalize.go:414-501`). One writer per stream matters:
`DisplayWriter` is per-stream state and the two copy goroutines run
concurrently, so sharing one would interleave held bytes. The held partial line
is capped at 1 MiB and released on a rune boundary, so an indexer streaming one
endless line grows the buffer to the cap and no further
(`normalize.go:471-501`).
What remains on this path is display integrity, not a second code-execution
path. The indexer's working directory is the reviewed tree (`cmd.Dir = d.dir`),
so the tree decides what it walks, and the binary itself is not plantable from
the tree: it is resolved with `agent.Resolve`, whose `pathNoCWD` PATH drops
cwd-relative entries (`internal/agent/agent.go:228-236`, `agent.go:173`), and it
runs argv-only with an absolute-only-PATH environment, a 30-minute per-directory
cap, and a deferred process-group SIGKILL
(`cmd/gauntlet/semcode.go:25-29,94-100`). The filter removes escape, control,
and bidi sequences; it does not make a symbol name the indexer chose true.

**B2 (agent misbehavior directly).** Spoofing: fabricated agent output cannot
drive the terminal (B3 mitigations) but fabricated *content* flows into
commits, so attribution of prose is by review name, not by verified origin.
Who authors the commits depends on the mode, and the difference is a real
privilege transition:

- **`--jobs > 1` (worktree isolation):** the runner stages and commits each
  worktree itself (`Worktree.CommitAll`, called from `runLaneReview` in
  `internal/runner/runner.go:927`); agents stay forbidden to run git (DESIGN.md
  rule 3). Between retry attempts the runner alone rewinds its worktree to
  the base commit so attempt N+1 starts where N did (`ResetToBase` in
  `internal/gitx/worktree.go:504-522`, called from `resetForRetry`): more runner-side
  git authority, exercised only inside gauntlet-created worktrees.
  Persistent lanes reuse one worktree across reviews; each advance or retry
  still starts from a known commit.
- **`--stacked-prs` (worktree isolation plus publication):** the runner has
  the same staging, commit, and retry-reset authority inside one scratch
  worktree, then invokes Git to push each committed child branch and `gh` to
  create its PR. `PrepareStack` (`internal/runner/stack.go:78-189`) runs every
  check before any agent starts, the suggestion agent included, in a fixed
  order: local validation, then the dirty-checkout boundary (interactive
  consent or `--yes` before anything touches the network), then remote-URL
  validation (the base repository from the configured fetch URL, the PR head
  owner from the configured push URL, so a fork workflow opens its PRs with
  an OWNER:BRANCH head), and only then the fetch of the named remote base,
  `gh` authentication and repository access, and a dry-run new-branch push.
  The fetched base commit is pinned for the whole run and carried across a
  hot reload, and project prompts and suggestion signals are read from a
  snapshot worktree of that commit, never from uncommitted files in the
  checkout. A publication failure stops later reviews, so no agent runs on a
  branch that does not exist as a usable remote PR base. Stack branch names
  are derived from a public base commit, so a pull request is only reused as
  a run's own layer when its head branch lives in the repository gauntlet
  pushes to (`ownsHead` in `internal/ghx/ghx.go:161-170`): a PR opened from
  someone else's fork under a name a run is about to use is ignored, not
  adopted. Git and `gh` receive fixed argv elements rather than shell text;
  branch commands separate branch names with `--` option delimiters and
  rev-parse, log, and diff separate options with `--end-of-options`
  (`internal/gitx/branch.go:51,90,125,146,177,276,289,363,397,429,440,456,478,489`, `internal/gitx/worktree.go:278,308,436,509,530,583,604,788`, `internal/gitx/gitx.go:667,686`);
  review names, commit subjects, and PR bodies cannot become commands. A PR
  body is assembled from values that originate in the reviewed repository --
  the agent's commit subject, the review prompt's own summary line, the paths
  its commit touched -- so each is stripped of control characters, flattened
  to a single line, NFC-normalized to prevent rune splitting, and length-bounded
  before it is rendered (with overview notes deduplicated across NFC/NFD forms), and a path is escaped into a code span that a backtick
  in it cannot close (`internal/runner/prbody.go:26-44,86,146-173`, `internal/runner/stack.go:655`). Markdown posted to
  GitHub is display, not execution, but a forged heading or an unbounded path
  list is still a reviewer reading something the run did not say. Git itself
  runs hardened against the reviewed repository's own config (see the `.git/config`
  row). Gauntlet's scratch worktrees are refused when `.gauntlet` or
  `.gauntlet/worktrees` is a symlink or not a real directory inside the
  repository (`ensureWorktreeRoot` in `internal/gitx/worktree.go:134-159`);
  worktree paths are verified to remain strictly inside `.gauntlet/worktrees`,
  leftover checkout directories and pruned metadata are purged on preparation
  and removal, and worktree operations on removed checkouts safely error or
  no-op (`removeWorktreeDir` and `Worktree.Remove` in `internal/gitx/worktree.go:249-273,544-557`).
- **Sequential in-place with `--commit`/`--push`:** a failed review's retry
  restores a snapshot of the user's checkout taken before the attempt
  (`Snapshot`/`Restore` in `internal/gitx/snapshot.go`), the same rewind
  authority as a worktree reset, bounded to putting back files the user
  already had. The commit step is itself an agent launch. `runCommitStep`
  (`commit.go:137-222`) execs one agent with
  `prompt.CommitPrompt`, which instructs it to run `git commit` and never
  to push; the runner strips injected AI attribution trailers from the new
  commit (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) and pushes itself under `--push`
  (`internal/runner/commit.go:204-216`). Neither trailer stripping nor a clean
  tracked-file status is a security inspection of the committed content, and
  neither prevents the agent from invoking Git itself. Under `--yolo` a
  rejected push escalates to a runner-side `git pull --rebase` and retry; a
  rebase conflict cleans up mid-rebase state via `git rebase --abort`
  (`internal/gitx/branch.go:251-255`) and fails the step rather than returning to the agent. The
  same launch, offered standalone when a dirty tree blocks `--jobs`, is
  gated on explicit consent (`cmd/gauntlet/main.go:875-905`). The prompt is embedded
  text only (`rules/commit.md`), capped at 5 minutes (`commit.go:24`),
  under the same process discipline as any review.
- **Conflict resolution:** `resolveConflict` (`conflict.go:37`) cuts a
  scratch checkout, replays the branch, and launches one agent with
  `ConflictPrompt` (`compose.go:202-242`, rules in `rules/conflict.md`) for up to
  10 minutes (`conflict.go:26`). The prompt lists only sanitization-safe
  paths and forbids git; conflict hints quote branch and message via `runx.ShQuote`
  (`conflict.go:185-187`) to prevent shell injection if pasted; the marker
  scan before the commit covers the resolution's whole commit scope, the
  conflicted paths plus everything the resolver touched in the scratch
  checkout, because the commit stages it whole
  (`commitScope`, `conflict.go:102-119`, fed by `Worktree.CommitScope`,
  `internal/gitx/worktree.go:469-487`); a status that cannot be read falls back
  to the conflicted list. The runner commits and merges the result, or keeps the
  original branch if markers remain. Same
  advisory fence, same permission-bypassed process, narrower file set.
- **`--usage-cmd` (the usage-limit probe):** the operator supplies argv to
  decide whether more reviews start (`internal/runner/usagelimit.go:46-72`).
  `probeUsage` isolates execution from the reviewed directory: `cmd.Dir` is
  set to `os.TempDir()`, `cmd.Env` isolates the child environment with an
  absolute-only PATH (`runx.AbsPATHEnv()`, `probeEnv`), and bare executable
  names are resolved only against absolute PATH entries (`resolveProbe`,
  `runx.LookPath`) (`internal/runner/usagelimit.go:84-113`).
  No shell is added, but an operator-selected relative executable or script path
  (e.g. `./probe.sh` or an explicit path into the tree) can still consume
  repository content. The probe has a 10-second deadline, a 5-second pipe
  wait bound, and 4 KiB per output stream; excess output fails the probe.
  `runx.Bound` provides a process group and cancellation kill (`internal/runx/runx.go:68-92`); a deferred
  group kill also cleans up remaining group members after the direct child exits
  (`internal/runner/usagelimit.go:93-95`). Neither prevents a child from
  deliberately escaping its process group.
  Parsing takes the last non-empty line, accepts a trailing `%`, and rejects
  non-finite, NaN, or out-of-range values (`internal/runner/usagelimit.go:47,125-158`).
  Failure warns once and leaves the usage threshold unenforced; later checks
  may succeed. Spending can continue beyond the intended threshold, though
  this does not enlarge the independently configured runtime budget. A valid
  but false low reading also permits continued scheduling. The probe is
  trusted telemetry, not an authenticated quota service.

**B2, spend (the two budgets).** `--runtime` bounds wall clock and
`--token-budget` bounds the tokens the run's reviews report
(`budgetExhausted`, `internal/runner/runner.go:640-648`; `Stats.Tokens`,
`internal/runner/stats.go:177-185`). Both are read before a loop and before a
review is taken, never during one, and the token figure is a sum the agents
supplied through their own output, carried across a hot reload through
`Stats.Tokens` seeding so the ceiling survives the exec. Denial of service
against the provider's budget therefore remains: a review that under-reports
its tokens is under-counted, the commit and conflict launches are outside the
sum, and a single review already in flight runs to its own timeout whatever
either budget says. The bounds are a schedule, not a quota; the model does not
claim otherwise.

Repudiation is only partly addressed: when journaling is available, a run
records seed, schedule, outcomes, and the prompt fingerprint each launch ran under
(`internal/runner/event.go`, DESIGN.md "Run journal"), the
commit step is journaled as its own event with the chosen agent
(`internal/runner/commit.go:218-222`, kind at `internal/runner/event.go:24`), and worktree-mode commits carry
the runner as author. Stacked publication adds a `pull_request` event carrying
the exact head, base, status, and URL; the terminal summary repeats every URL.
The recorded seed is now the run's only one: `effectiveSeed`
(`cmd/gauntlet/reload.go:106-110`) resolves it once, from `--seed` or from the
handoff a hot reload carries, and hands the same number to the schedule and the
suggest shuffle, which draw from it through `SeedOrClock`/`drawIndex`
(`internal/runner/draw.go:45-110`). Before that, each consumer read the clock
itself and a reload handed the suggest step a different seed than the journal
recorded, so the seed in the journal did not replay the run it named. An unset
seed is derived from `time.Now().UnixNano()`, never 0, and is not a secret: it
orders agents and retry backoff, and nothing in the draw is a capability.

**B3.** Display sanitization addresses terminal escape injection, not the
truth or origin of printable content. The composed prompt shown by
`--show-prompt` (`modes.go:65-66`), the journal replayed by `show`
(`runs.go:132`, which spawns no pager), the plain reporter
(`report.go:87-100`), and the dashboard feed
(`internal/ui/ui.go:683-687`) pass through the same stripping, which removes ASCII controls,
Unicode formatting/bidi (`Cf`), and line/paragraph separators (`Zl`/`Zp`), and
truncates at whole grapheme cluster boundaries (`internal/normalize/normalize.go:375-397`).
Stream classification excludes recognized tool/user objects (normalized case-insensitively) before their text reaches the report
parsers, but trusts the producer's role/type fields
(`internal/streamjson/streamjson.go:199,202,217-248`,
`internal/runner/exec.go:245-265`). A compromised agent can still supply false
assistant text or plain-text report lines. Output buffering has line and tail caps, but
`--raw` bypasses normalizer rate/width limits while retaining `Display`
(`internal/runner/exec.go:269-280`). Usage and sink callbacks are serialized
(`sinkMu`, `usageReportMu`) across concurrent stdout/stderr streams (`internal/runner/exec.go:169,196-198,208,226-228`),
and transcript watcher lifecycle cleanup is synchronized with reviews
(`usageWatch.halt`, `internal/runner/runner.go:1266-1283`).
When readers are stalled by orphaned grandchildren, gauntlet issues a process-group SIGKILL on
drain timeout (`exec.go:327-334`). The one child whose output is not parsed,
the `--semcode` indexer, is filtered by the same `Display` on the way to the
terminal, one `DisplayWriter` per stream (`cmd/gauntlet/semcode.go:91-100`,
`internal/normalize/normalize.go:414-501`), so no subprocess stream reaches a
terminal unfiltered. Parser bounds do not limit a child's disk writes, network
traffic, CPU, or provider spend. A non-positive process timeout also removes the per-launch
deadline (`internal/runner/exec.go:293-300`).

**B4.** Update metadata is requested over HTTPS; release asset and checksum downloads
are constrained by `validateAssetURL` (`internal/selfupdate/selfupdate.go:287-324`) to
HTTPS on authorized GitHub release hosts (`github.com`, `api.github.com`,
`objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests), preventing cleartext transfers
or links pointing to untrusted third-party infrastructure. Download requests validate
redirect targets against the same host allowlist and stop after 10 redirects (`client.CheckRedirect`,
`internal/selfupdate/selfupdate.go:145-157`). Downloads are routed through `getAsset`
(`internal/selfupdate/selfupdate.go:324-344`) and compared with SHA-256 entries in `checksums.txt`
using constant-time comparison (`subtle.ConstantTimeCompare`,
`internal/selfupdate/selfupdate.go:159-196,221-286,405-429`). Downloaded binaries are
flushed to disk with `Sync()` (`selfupdate.go:269`) before atomic rename.
Download responses are bounded and verified by `fetch` to reject oversized responses
exceeding the limit (`internal/selfupdate/selfupdate.go:362-404`); download and error paths
drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:184,187,346,356,373,393,396,349,358`);
error messages are decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:180,345-355,362-380`).
The checksum proves the download matches the listing, not that the publisher
is benign: the same publisher controls both. Compromise of the GitHub repo
or its release process yields arbitrary code execution on updating machines,
amplified by hot reload re-exec'ing the new binary mid-run without user
confirmation (`internal/selfupdate/reload.go:220-244`). Auto-update background
goroutines are coordinated via context cancellation and WaitGroup on shutdown
(`cmd/gauntlet/main.go:661-701`). No signed-release mechanism exists. The
advisory scanner in CI (`vulnscan.yml`) watches the dependency graph, not this
channel. The `bunx @deepseek-ai/dsh` fallback (and the `--dump-config` probe
that uses the same argv) is a second fetch-and-execute path, from the npm
registry rather than GitHub releases, with no checksum (`internal/agent/agent.go:548-560`,
`internal/agent/dsh.go:74-111`). The probe is bounded to 4 MiB output via `runx.Bound`
(`internal/agent/dsh.go:65,84`) and reclaims process groups via `runx.KillGroup` (`dsh.go:85`).
Overlay configurations validate provider and model identifiers
against `dshModelRe` (`internal/agent/dsh.go:135-136`), and overlay keys reject path traversal
(`internal/agent/dsh.go:116-133`).

**B5.** Secret egress: the updater explicitly attaches `GH_TOKEN` /
`GITHUB_TOKEN` only to requests whose scheme is HTTPS and hostname is
`api.github.com` or `github.com` (`internal/selfupdate/selfupdate.go:90-108`).
Asset and checksum endpoints are separately checked against the HTTPS release host
allowlist (`validateAssetURL`, `internal/selfupdate/selfupdate.go:287-324`).
Git error messages redact embedded basic-auth userinfo credentials (`runx.RedactUserinfo`,
`internal/gitx/gitx.go:358-364`) before display or journaling. Agent CLIs receive the inherited
environment and hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal creation requests 0600 files and 0700 directories
(`internal/journal/journal.go:176-186`, `internal/journal/index.go:78-93,682-705`);
handoffs use a sibling temp
file, disk flush via `Sync()`, and atomic rename (`internal/selfupdate/reload.go:135-172`).
Stale temp files in the journal index directory, the reload state directory, and
the dsh overlay cache are swept periodically
via `gauntlethome.SweepStaleTemps` (`internal/gauntlethome/gauntlethome.go:162-179`,
`internal/journal/index.go:685`, `internal/selfupdate/reload.go:142`,
`internal/agent/dsh.go:174`), and every rename-based write in those three is
the one helper (`gauntlethome.WriteFileAtomic`,
`internal/gauntlethome/gauntlethome.go:138-161`), so a partial write is not a
shape each caller can have differently.
Journal entry and index writes are flushed with `Sync()` (`internal/journal/index.go:105,279,705`),
and Close preserves and joins both file and index errors (`internal/journal/journal.go:243-265`).
Run IDs from CLI flags or events are strictly validated against directory traversal and non-safe
charsets (`validRunID`, `internal/journal/history.go:88-104`), and date shards derive from the run ID timestamp
(`shardFromRunID`, `internal/journal/history.go:167`). Creation
modes do not tighten permissions on pre-existing paths or authenticate local
state. Journal opens follow existing paths, so a protected state directory is
a precondition, not an enforced property of any `GAUNTLET_HOME` value
(`GAUNTLET_HOME` expands tildes and environment variables, with unresolvable
references rejected at startup via `cmd/gauntlet/flags.go:545-552` and safe degradation
via `internal/gauntlethome/gauntlethome.go:28-46`).
`LoadState` (`internal/selfupdate/reload.go:173-219`) requires the `.json` extension,
requires absolute paths, verifies clean paths without traversal, verifies via `os.Lstat`
that the state file is a regular file (rejecting symlinks, directories, and non-regular
files without deleting target), and bounds reads to 16 MiB (`maxHandoffBytes`), rejecting
oversized files before removal. `SaveState` (`internal/selfupdate/reload.go:136`)
validates `runID` against directory traversal and path separators. The tree lock's symlink
check is a separate control (`internal/runner/lock.go:53-89`); `Note` normalizes to NFC
before truncation (`internal/runner/lock.go:94-119`); `Note` and `readNote` handle
partial writes and `EINTR` retry loops (`internal/runner/lock.go:99-119,123-139`), and lock release
preserves the open file descriptor flock and truncates the note to prevent
file deletion and inode recycling races (`internal/runner/lock.go:161-175`).
A malicious agent already runs as that operator and can tamper with local evidence.

`--log` is a separate confidentiality and availability boundary: it retains
output that the journal omits. `openLogFile` (`cmd/gauntlet/main.go:672-696`)
refuses a symlink and any non-regular file with `os.Lstat` before opening,
opens with `syscall.O_NOFOLLOW` and 0600, and chmods a surviving regular file
to 0600 before this run writes anything, so a looser umask or an older run's
group- or world-readable file is tightened rather than inherited. What remains
unbounded is the path's surroundings: the parent directory is not confined or
checked for writability by anyone else, and the writer has no size cap and no
rotation. Display sanitization does
not redact arbitrary secrets (`internal/runner/exec.go:269-280`). Same-user
agents can read or alter the log just as they can the journal.

**B6, destruction.** The state tree is the only place a run deletes, and it
does so on every exit rather than on request. `Prune` (`internal/journal/retain.go:36-140`)
keeps the newest `keep` journals and index rows by run ID, which embeds a UTC
start time, and removes the rest oldest first (`internal/journal/index.go:571-614`).
Threats: **repudiation**, in the ordinary sense of a run whose evidence is gone
before anyone asked for it, which is what a default of 200 does to a long-lived
install; **tampering**, because the ordering is a property of the ID string
rather than of anything the journal asserts, so a run whose clock sat behind
another run's is evicted first, and a same-user agent that can write `runs/`
controls which names sort above which; **denial of service**, bounded and
small, since the index rewrite is the only unbounded read in the path
(`readAllIndex` with a 4 MiB line cap, `retain.go:141-173`). What the walk
prevents is the version of this that reaches outside: it takes only entries
the readdir reports as real directories and only `<id>.jsonl` files whose stem
passes `validRunID`, so a planted symlinked shard is skipped rather than
followed and `os.Remove` deletes a name, never a link target. A prune that
fails is a warning; the run's own report has already been written
(`cmd/gauntlet/main.go:840-849`).

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, `protocol.ext.allow=never`, `attr.tree` empty, local drivers blanked, absolute-only git PATH, `GIT_SSH_COMMAND=ssh` (defaulting on empty/whitespace env, `gitx.go:378-393`); propagated to worktrees and sub-repos via `subRepo`; branch and revision commands separate options with `--` and `--end-of-options`; a merge target is refused before `worktree add`, which takes no `--`, can read a leading dash as an option | `internal/gitx/gitx.go:43-67,233-274,321-372,374-389,633-647,663,682`, `worktree.go:33-51,278,308,436,509,530,583,604,788`, `branch.go:139-150,465-489` |
| Writing outside the repository through a planted `.git` | `.git/info/exclude` append re-checks the parent directory on the path it is about to write, with `os.Lstat` requiring a real directory, because the reviewed tree picks `gitDir` and both `MkdirAll` and `OpenFile` follow a link at any component | `internal/gitx/worktree.go:73-133` |
| Runaway git grandchildren (hooks, merge drivers) holding pipes | process-group SIGKILL on deadline, bounded WaitDelay | `gitx.go:349-357` |
| Planted executables shadowing agents/git/`gh` | cwd-relative PATH entries stripped and child environments isolated with absolute-only PATH via `runx.AbsPATHEnv` for agent, git, git-child, `gh`, and usage probe resolution; executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths | `agent.go:173`, `gitx.go:68-96,375-412`, `internal/ghx/ghx.go:97-111,239`, `usagelimit.go:84-113`, `exec.go:109`, `runx.go:111-166` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps; stat errors propagated on open regular files; prompt discovery inspects candidates with `os.Lstat` requiring regular files; suggester peeks via `os.OpenRoot`; reload handoff verified regular file via `Lstat`; the tree lock is created 0600 and `Fchmod`ed to 0600 on the descriptor, so a note naming the run, review, and agent CLI is not left group- or world-readable in a directory the reviewed tree picks; a note read is trimmed of a partial UTF-8 rune before it reaches a terminal | `prompt.go:278-329`, `discover.go:244,278`, `gitx.go:543-625`, `runner/lock.go:53-89,123-159`, `suggest_fast.go:687-692`, `reload.go:187-196` |
| Prompt injection blending into containment rules | begin/end markers, both markers escaped in the body, report-section stripping fails open | `compose.go:34-39,53-75,170-175` |
| Injection via suggest catalog | description *and* name sanitize, fence-neutralizing, 200-rune cap, NFC normalization before truncation, strict suggestion grammar checked against known set, reasons capped to the same budget; the unknown half of a triage answer is capped at 20 names of 200 runes with the remainder counted and reported as truncated, so a confused or hostile agent cannot leave a megabyte of retained strings or a line of log with no end | `compose.go:247-294,304-313,317-354` |
| Injection via `Signals:` into the file-signal suggester | known kinds, charset, 12×40-rune caps; `mark:` values search file heads, not executed | `prompt.go:199-232`, `suggest_fast.go:527-556,687-692` |
| Terminal-driven or spoofed output, including prompt preview, journal replay, reporter, and dashboard | `Display`/`Sanitize` strip escapes, controls, bidi, and separators, and repair bytes that are not valid UTF-8 so one file name renders the same whatever sits beside it; width cap; rate limit; duplicate collapse; grapheme-preserving truncation; ANSI CSI sequences terminated on standard final characters in token width calculation; usage and sink callbacks serialized | `internal/normalize/normalize.go:265-282,375-397`, `modes.go:65-66`, `runs.go:132`, `report.go:87-100`, `internal/ui/ui.go:683-687`, `internal/ui/theme.go:246-283`, `exec.go:42,47,169,196-198,208,226-228,272-279`, `runner.go:26` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before a terminal write; CRLF stripped | `gitx.go:715,799,854-916`, `internal/runner/sanitize.go:14-19`, `lock.go:22-25`, `conflict.go:130-183` |
| Hostile file names forging conflict-prompt instructions | drop unsanitary paths (control/Cf, `RESOLVE:`, fence-closer); 1024-rune path cap; 50-file cap (over-cap skips the launch); list fenced in `<files>`; markers still block the merge | `compose.go:202-242`, `conflict.go:130-183` |
| A conflict marker left outside the conflicted file set reaching the merge | the marker scan covers the commit's whole scope, the conflicted paths plus every tracked and untracked path `CommitAll` would stage, and a file the scan cannot read counts as unresolved rather than clean; an unreadable status falls back to the narrower conflicted list | `commitScope`, `internal/runner/conflict.go:68,102-119`, `Worktree.CommitScope`, `internal/gitx/worktree.go:469-487` |
| Shell injection in conflict resolution hints | branch names and commit messages quoted with POSIX escaping via `runx.ShQuote` in `conflictHint` | `internal/runner/conflict.go:185-187`, `internal/runx/runx.go:176-184` |
| Model output written as a commit subject | ASCII controls, Unicode Cf (bidi), Zl/Zp stripped; grapheme cluster preserved; NFC normalization before truncation; 100-rune cap where the line is parsed and 72 where it is committed, so a long line is clipped before it reaches history, a PR title, and a merge message; a subject carrying an attribution credit line is dropped for the generated one; one line | `internal/agent/notes.go:76-141`, `internal/runner/subject.go:40-63,73-228` |
| A commit subject an agent supplies crediting itself or a model CLI | a narrow regex over the parsed subject (`attributionRe`, `internal/runner/subject.go:54`) drops a trailer or phrase that credits a tool, replacing it with the generated subject; the word alone is not a match, so a change that genuinely concerns one of those tools still describes itself; the trailer sweep on the finished message (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) does not reach the subject, which is why the check is here | `internal/runner/subject.go:40-63` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks with UTF-8 rune boundary preservation, trailing CR stripped, feed error trigram prefilter, bounded tail buffers (1 MiB suggest tail) | `exec.go:34-42,365-367,380-394,448-462`, `internal/normalize/normalize.go:304-323` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:33-37`, `agent.go:496-503` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, deferred SIGKILL on normal exit to clean up orphaned grandchildren, drain timeout process group SIGKILL escalation to unblock stuck readers, stdin null device, own session (no controlling terminal, so Ctrl-C cannot be disabled from inside an agent), drain grace for stuck grandchildren | `exec.go:25-32,100-350,140-146,327-334,453-485` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; runner-side publication is workflow separation, not removal of agent credentials | `internal/runner/commit.go:24,137-225` |
| Conflict step running away | separate 10-minute cap, same process discipline; unresolved markers keep the branch | `conflict.go:26,37-90` |
| Indexer binary shadowed by a planted `./semcode-index` | resolved with `agent.Resolve` (`pathNoCWD`), argv-only, absolute-only PATH in the child, 30-minute per-directory cap, deferred process-group SIGKILL | `cmd/gauntlet/semcode.go:86-108`, `internal/agent/agent.go:228-236`, `internal/runx/runx.go:111-166` |
| File names from a hostile tree reaching the terminal through the indexer | one `normalize.DisplayWriter` per stream, each complete line through `Display`, partial line held to a 1 MiB cap and flushed after the child is reaped; no child stream is written straight to the terminal any more | `cmd/gauntlet/semcode.go:91-100`, `internal/normalize/normalize.go:414-501` |
| A secret reaching a pasted `doctor` transcript | the environment report prints a name the table marks secret as `(set)`, never as a value, and reads only documented names; every environment name in the source is pinned to the table or to a stated reason it is internal | `cmd/gauntlet/doctor.go:289-311`, `cmd/gauntlet/help.go:172-184`, `cmd/gauntlet/env_surface_test.go:31-33,35-59` |
| Unbounded or unauthorized downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; HTTPS and authorized GitHub release host check (`validateAssetURL`); unified `getAsset` check; HTTP redirects re-verify host allowlist and stop after 10 redirects; fetch strictly rejects responses exceeding limit; download and error responses drain HTTP bodies up to limits via `drainBody`; error messages decoded up to 4 KiB on failure via `responseError`; digest-shaped entries only; constant-time checksum comparison; binary flushed with `Sync()`; verify-before-rename, atomic replace | `selfupdate.go:117,145-157,159,180,184,187,221,266,269,287-324,324-344,373,381-404,362-380,405-429` |
| Partial binary observed by reload | two immediate identical stat readings reject visible changes, but do not prove completeness or authenticity; safe replacement depends on atomic writers and disk flushes | `internal/selfupdate/reload.go:87-134`; updater rename and sync at `internal/selfupdate/selfupdate.go:269-274` |
| Concurrent agents corrupting one tree | flock per directory with inode preservation across releases; partial-write and EINTR retry handling on note I/O; parallelism only across directories or with worktree isolation + serialized merges; worktree paths confined to `.gauntlet/worktrees`, worktree mutex synchronization, orphaned directory cleanup on add and remove; a conflict is resolved in a scratch checkout or keeps its branch | `runner/lock.go:53-175`, `worktree.go:195-208,249-273,504-543,556-557`, DESIGN.md concurrency section |
| Option injection and revision collisions in git commands | branch and revision arguments separated with `--` and `--end-of-options` across rev-parse, log, diff, merge, squash, rebase, delete, rename, trailer stripping, and reset; `worktree add` takes no `--`, so a merge target is shape-validated with `ValidateBranchName` before it is passed; a GitHub remote whose host or repository name begins with a dash is refused at parse, before the selector reaches `gh repo view`'s positional | `internal/gitx/gitx.go:667,686`, `internal/gitx/trailers.go:82`, `internal/gitx/branch.go:51,90,125,139-150,465-489`, `internal/gitx/worktree.go:278,308,436,509,530,583,604,788`, `internal/gitx/snapshot.go:41`, `internal/ghx/ghx.go:54-98` |
| Accidental launch of unconstrained run from empty filter | `blocked` returns the reason before the flag is set, and Enter consults it first, so a filter matching zero reviews cannot compose a run of all of them; the same notice is rendered in the pane | `internal/ui/pick_view.go:114-125,564-568`, `internal/ui/pick.go:328-332` |
| Reviewed tree defining its own agents | `CustomFilePath` empty without state root; argv is exec, not shell; argv, model, effort, stream, continue reject blank elements; NFC normalization of keys and names; case-insensitive duplicate field detection, single `{prompt}` placeholder requirement, `{prompt}` forbidden in model/effort/stream/continue, `{model}` and `{effort}` restricted to appropriate fields, tilde and environment variable expansion in executable `cmd[0]`, UTF-8 BOM stripped, non-empty model and usage roots, usage.suffix cannot be whitespace only | `custom.go:87-225,293,346,369-406,474-480`, `agent.go:490-496` |
| Directory traversal or poisoned journal run IDs | run ID length bounded (<= 128), charset-restricted (`[a-zA-Z0-9_.-]`), ".." rejected; date sharding derived from run ID timestamp; journal writes flushed via `Sync()` | `internal/journal/journal.go:156`, `internal/journal/index.go:105,279,705`, `internal/journal/history.go:88-104,167` |
| History prune reaching outside the state tree | the walk yields only real shard directories and only `<id>.jsonl` names that pass `validRunID`; the index row is rewritten before its journal is unlinked, under the index lock, with a 4 MiB line cap; `keep <= 0` deletes nothing | `internal/journal/retain.go:36-140,141-173`, `internal/journal/index.go:571-614` |
| Embedded basic-auth credentials in remote URLs | userinfo stripped from git stderr strings before errors are returned, printed, or journaled | `runx.RedactUserinfo`, `internal/gitx/gitx.go:358-364` |
| Known-vulnerable dependencies shipping to users | govulncheck weekly and on dependency changes in CI | `.github/workflows/vulnscan.yml` |
| Local state and secrets in the `make repro` archive | the members come from `git ls-files --cached --others --exclude-standard`, so every `.gitignore` entry is out of it, `.env` is one of them, and tests read `.gitignore` and fail on an entry whose rule no longer bites, so the list cannot drift by forgetting a new build output; the archive lives under `$(HOME)/.cache/gauntlet/repro` and is removed by an exit trap | `Makefile:508-532`, `cmd/gauntlet/makefile_test.go:865-981`, `.gitignore:10` |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential;
   consider signing releases or documenting the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:447`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`internal/agent/agent.go:548-560`). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:300-321`); a
   `dsh:<model>` pin additionally execs the same argv as `--dump-config`
   (`internal/agent/dsh.go:74-111`). Documentation should say plainly that naming `dsh`
   without the launcher installs and runs an npm package, including at
   probe time.
3. **No SECURITY.md.** There is no documented path from "vulnerability
   reported" to "fix shipped": no disclosure contact, no supported-version
   statement. Creating one requires an owner decision, so it is only noted
   here.
4. **R6, resource and budget enforcement.** The launch path sets no OS
   resource limits (`internal/runner/exec.go:107-111`); the optional usage
   threshold is checked between reviews and fails open on probe errors
   (`internal/runner/usagelimit.go:46-72,84-113`); `--token-budget` counts what
   the reviews report, excludes the commit and conflict launches, and is checked
   only between reviews (`internal/runner/runner.go:640-648`). None is a hard
   spend quota.
5. **Secrets hygiene around spawned agents.** Gauntlet inherits the full
   operator environment to every agent; a scrubbed env or documented
   container workflow would shrink R3. Design decision, not a bug.
6. **R9, bounded audit trail.** `--keep-runs` deletes run journals and index
   rows on every run's exit (`internal/journal/retain.go:36-140`,
   `cmd/gauntlet/main.go:846-848`), ordering by the timestamp inside the run ID
   rather than by anything the journal asserts, with no way to keep everything
   except by setting 0. A security owner who wants the history of an incident
   has to know to raise the bound before the run that shows the incident, and
   the failure to keep it is silent apart from a count in the run's own report.
7. **Journal path confinement is by name, not by owner.** `validRunID`
   (`internal/journal/history.go:88-104`) and the walk in `Prune` bound what a
   run can read and delete, but nothing on this boundary authenticates the
   state directory: a run ID collision or a planted file under a real shard is
   decided by string shape and timestamps. Run IDs now carry the whole pid
   (`runIDFor`, `internal/journal/journal.go:69-77`), which removes the
   truncation collision, but the ordering property R9 names is unchanged.
   A protected `GAUNTLET_HOME` remains a precondition the code cannot enforce.
8. **The developer build surface is not in the runtime model.** `make repro`
   archives the whole working tree, so anything a developer has in their
   checkout that is not ignored lands under `$HOME/.cache/gauntlet/repro` for
   the length of the build (`Makefile:508-532`). The members are git's
   ignore-aware listing, which covers the entries
   in `.gitignore`, a derivation, not a guarantee: an untracked
   credentials file, a private key, or a second `.env` under another name is
   copied with no redaction and no scan. This is a developer convenience
   target on this repository, not a path any reviewed repository reaches, and
   the copied trees are removed on exit.

## Abuse cases

The tool has one authenticated user (the operator), so the hostile actor is
the reviewed repository's author:

- **Steering the fix fleet.** Plant `sec-review.md` in the tree with the
  bundled name and different content; discovery warns but obeys
  (`discover.go:104-111`). The planted body rides past the markers into an
  agent told to skip permissions or auto-approve. Enabling path: `Discover`
  -> `Compose` -> `BuildCmd` -> `runProc`.
- **Exfiltration-by-push.** Repository instructions steer an agent into
  including sensitive content in a change. With `--commit --push`, the agent
  commits and the runner pushes after tracked-file and attribution checks
  (`internal/runner/commit.go:204-216`). Those checks do not inspect the diff
  for secrets or authorize its content. The same-user agent can also publish
  directly despite the prompt prohibition. Containment is advisory, not a
  credential or network restriction (`internal/runner/exec.go:107-111`).
- **Consent-surfaced commit.** Refuse `--jobs` on a dirty tree and gauntlet
  offers to hand that tree, unreviewed, to an agent that commits it; `--yes`
  or `--yolo` on the original command is that consent, so an operator who
  scripts those flags has pre-approved agent-authored commits of whatever
  the reviews left behind (`cmd/gauntlet/main.go:875-905`).
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:204,266-294`) bound it to reviews that exist
  in the discovered set, including the attacker's own.
- **Signals: steering.** A planted `Signals:` line on a project prompt is
  parsed into the file-signal suggester (`prompt.go:199-232`,
  `matchDeclared` in `suggest_fast.go:527-556`). Charset, count, and length are
  bounded; a matching tree still lets the attacker add their review to the
  auto-picked set. Same outcome as suggestion gaming, different path, and it
  does not need an agent (`--suggest-agent gauntlet`).
- **Conflict-step write.** After a merge conflict, `resolveConflict`
  (`conflict.go:37`) launches an agent in a scratch checkout with the
  conflicted paths named in the prompt. The agent edits those files; the
  runner commits and merges if markers are gone. Containment is the same
  advisory fence. Unresolved markers keep the branch.

- **Tool-output laundering.** Repository text returned by a tool can contain
  report-shaped prose. Recognized tool/user stream objects are excluded before
  tail parsing, so their text and counters are not treated as the assistant's
  report (`internal/streamjson/streamjson.go:199,202,217-248`,
  `internal/runner/exec.go:245-265`). Regression coverage pins this class in
  `internal/streamjson/streamjson_test.go`; unmarked output or an assistant
  repeating the text remains unauthenticated.

- **Evidence eviction.** The reviewed tree names a planted prompt that plants
  run journals. A same-user agent can write `<id>.jsonl` files under
  `~/.gauntlet/runs/`, and `Prune` orders by the ID's embedded timestamp, so
  names it fabricates displace real runs from the newest 200 without ever
  touching a file outside the state tree
  (`internal/journal/retain.go:36-140`, `internal/journal/index.go:571-614`).
  What it cannot do is delete anything the operator pointed at elsewhere: the
  walk refuses a shard that is not a real directory and a stem that is not a
  valid run ID.

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was re-checked against the code on 2026-09-27 and
  matches the controls it names (O_NOFOLLOW prompt reads, cwd-free PATH
  resolution, forced-empty git config, display sanitization, directory
  flock). It does not list the later git overlays (`attr.tree`, local
  driver blanks, `protocol.ext.allow=never`, `GIT_SSH_COMMAND=ssh`); those
  are additional, not contradictory. The one display claim it does make,
  "untrusted text is stripped of control and bidi characters before display",
  now covers every child stream including the indexer's, so it is no longer
  narrower than the code.
- Response readiness: the journal supports run reconstruction, not a complete
  security audit, and it is now bounded: `--keep-runs` evicts the oldest rows on
  every run's exit, default 200 (`internal/journal/retain.go:36-140`). Agent
  output and live usage events are explicitly excluded
  (`cmd/gauntlet/main.go:516-528`), writes are buffered, and write failures are
  retained for reporting at close (`internal/journal/journal.go:205-233`).
  It does not authenticate agent claims or record every child action; same-user
  agents can alter local evidence. The disclosure and supported-version gaps
  above remain undocumented organizational decisions.
- Verification scope and baseline: 2026-09-27 against commit ef6eb5c. This
  pass read the eight commits since 93b004c. It entered `make repro` as an
  entry point, a mitigations row, and a gap, because a target that archives
  the whole working tree is a place secrets go and the document had said
  nothing about it. It carried two controls that landed in those commits but
  were not in the model: the attribution check on an agent-supplied commit
  subject, and the widened conflict-marker scan. It corrected one pointer that
  had left a test red (`make release` at `Makefile:447`), re-anchored the
  citations those commits moved, and closed no risk. Nothing in the numbered
  risk table changed.
- Earlier baseline: 2026-09-27 against commit 15b8fa9. That pass read the
  twelve commits between d5d79a9 and that commit and did
  two things. It removed a false mitigation: `show` no longer spawns a pager, so
  the pager-environment isolation this document claimed for the journal replay
  (three places) and counted in the PATH rows named a process that does not
  exist, and the sanitization those claims rested on was re-anchored on the
  `normalize.Sanitize` call that is still there. It re-anchored every
  `internal/ui/ui.go` and `internal/ui/pick.go` citation onto the symbols the
  state/view split moved, and added the two surfaces that split and those twelve
  commits introduced: the `runs --limit` allocation bound and the once-per-run
  seed. Nothing in the numbered risk table changed, and no risk was closed: the
  R1/R3/R4 core, R2's unsigned channel, R5's `bunx` fallback, and R9's bounded
  audit trail are all still open and still named where they were.
- Earlier baseline: 2026-09-27 against commit a6e6c7f. That
  pass read the twelve commits between dd793d8 and that commit. It closed R8:
  the `--semcode` indexer's two streams now carry a `DisplayWriter` each, so
  the row in the risk table, the entry-point row, the B1->B3 threat, and the
  mitigations map all say filtered rather than unfiltered, and the gap is
  gone. It entered the one new surface it found (`doctor`'s environment
  report) as an entry point and a mitigations row, added the environment-name
  completeness test to the environment row, added the pid-width run-ID control
  and the journal-boundary gap the pid change does not close, and re-anchored
  the citations that moved: `internal/normalize/normalize.go` (the display path
  gained the writer and the UTF-8 repair), `internal/selfupdate/selfupdate.go`
  and `reload.go` (the temp-file helpers moved to `gauntlethome.WriteFileAtomic`),
  `internal/journal/journal.go` and `index.go` (dedupe), `internal/agent/dsh.go`,
  `internal/gauntlethome/gauntlethome.go`, and `cmd/gauntlet/main.go` and
  `doctor.go`. The
  2026-09-27 pass before this one corrected the `--log` mitigation claims in
  all three places that carried them, added the `--semcode` indexer as an entry
  point, a threat, and a mitigations row, recorded it as a gap, and
  re-anchored the citations
  that had drifted since the 2026-09-26 baseline. The 2026-09-26 pass verified
  git option injection hardening with `--end-of-options` across rev-parse, log,
  and diff invocations (`internal/gitx/branch.go`, `internal/gitx/gitx.go`) and
  `--` delimiters across git resets (`abortMerge`, `ResetToBase`, `Advance`,
  `snapshot.go`); prompt discovery candidate inspection with `os.Lstat` requiring
  regular files (`internal/prompt/discover.go`); executable path resolution to
  absolute paths in `runx.LookPath` for relative names containing path separators;
  empty or whitespace-only `GIT_SSH_COMMAND` fallback to `ssh` in `gitEnv`
  (`internal/gitx/gitx.go`); gating custom agent loading in `cmd/gauntlet/flags.go`
  to subcommands executing or inspecting agents (`usesAgents`); subcommand peeling
  rejection of empty arguments and single dashes; subprocess group reclamation
  (`runx.KillGroup`) across dsh probing, indexer runs, and ghx calls, and 4 MiB buffer
  capping (`runx.Bound`) in `dsh --dump-config` probing; HTTP response body draining
  (`drainBody`) and consolidated remote error decoding (`responseError`) in self-update;
  case-insensitive role and type normalization in stream-JSON event parsing
  (`internal/streamjson/streamjson.go`); NFC normalization before truncation across
  lock notes (`internal/runner/lock.go`), prompt summaries and descriptions
  (`internal/prompt/prompt.go`), suggestion reasons (`internal/prompt/compose.go`),
  and stack PR overview deduplication (`internal/runner/stack.go`); ANSI CSI sequence
  termination on standard final characters in terminal token width calculation
  (`internal/ui/theme.go`); NaN and integer overflow guards in usage limits and UI
  meters; and updated code and line citations across the codebase. Older inventory
  references are retained rather than represented as newly verified.
