# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree, typically with their permission systems
bypassed or auto-approved. This document is the systemic view; individual
vulnerability findings belong to sec-review and are recorded here only as
threats.

Last reviewed: 2026-09-21 against commit ef4b056. This pass verified reload
handoff state file validation (.json extension, Lstat regular file check, 16 MiB size cap)
and run ID traversal rejection, self-update fetch response size bounds, branch operation
argument separation with dashes (--), worktree directory confinement under .gauntlet/worktrees,
orphaned directory recovery, and safe operations on removed worktrees, GAUNTLET_HOME
environment variable expansion validation with safe degradation, custom agent blank argument
and whitespace usage suffix validation, dsh overlay provider/model charset validation and overlay
key traversal rejection, exec output and usage callback serialization across streams, atomic
lock note I/O handling EINTR and partial writes, transcript watcher lifecycle synchronization,
picker launch blocking on empty filter matches and dashboard Enter key termination when done,
usage limit warning deduplication via atomic swap, and updated code and line citations across the codebase.
Other inventory references retain earlier baselines and need ongoing re-verification; this
is not a full assurance claim. Owner and review cadence are organizational
decisions; none is assigned here.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | Prompt injection from the reviewed tree drives an agent running with bypassed or auto-approved permissions | B1 -> B2 | Accepted by design; containment is advisory text plus process discipline, never an OS sandbox |
| R2 | Self-update integrity rests on TLS and repository ownership; `checksums.txt` authenticates nothing beyond transport consistency, and hot reload execve's the replaced binary automatically | B4 | Named gap |
| R3 | A prompt-injected or compromised agent reads every secret its user can: environment-inherited API keys, agent config stores, SSH keys, `~/.netrc` | B2/B5 | Consequence of R1; containerization is the documented answer (DESIGN.md non-goals) |
| R4 | Confidentiality of reviewed source: agents send code to third-party model APIs over the network | B2 | Inherent to the tool's purpose; users must know it |
| R5 | `dsh` without a launcher on PATH falls back to `bunx`, fetching `@deepseek-ai/dsh` from the npm registry and executing it; a `dsh:<model>` pin also runs that argv as `--dump-config` before the review | B4 | Named gap (deliberate feature, unreviewed supply-chain hop) |
| R6 | Agent resource consumption or a failed usage probe exhausts host capacity or provider budget | B2 | High when reviewing hostile content: parser caps are not CPU, disk, network, or spend quotas; the usage limit probe runs isolated from the repository but fails open on errors (`internal/runner/exec.go:99-141`, `internal/runner/usagelimit.go:47-73,85-149`) |
| R7 | `--log` persists source or credentials quoted in output at an operator-selected path | B3/B6 | Conditional on enabling logging; regular files are tightened to 0600, but content is not generally secret-redacted and the destination is not confined (`cmd/gauntlet/main.go:218-238`) |

The order reflects reachability and blast radius, not measured likelihood.
R1/R3/R4 require only content reaching a launched agent; R2 requires control
of the update publisher or executable path; R5 requires selecting the fallback.
No server authentication boundary is claimed here: the operator's OS account
supplies child-process authority (`internal/runner/exec.go:103-107`), and
publication uses that account's Git credentials (`internal/runner/commit.go:95`).

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  (DESIGN.md "Isolated parallel reviews", rule 1), and why the runner rewinds
  only its own worktrees to a base commit between retry attempts
  (`git reset --hard` + `git clean -fd`, `ResetToBase` in
  `internal/gitx/worktree.go:468-479`). In-place retries restore a snapshot of the
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
  source and credentials quoted by an agent (`cmd/gauntlet/main.go:218-238`).

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
  the requested push (`internal/runner/commit.go:137-226`). This divides
  workflow responsibility, not OS authority: the agent still inherits the
  user's credentials and can disobey the prompt's no-push instruction. The same
  hand-off exists outside a loop: when `--jobs` refuses a dirty tree,
  `commitFirst` offers to give the uncommitted work to one agent, consented
  by `--yes`/`--yolo` or an interactive confirmation, never on an unattended
  guess (`cmd/gauntlet/main.go:1207-1240`, executed by `runner.CommitNow`,
  `internal/runner/commit.go:47`). A third launch, `resolveConflict`
  (`internal/runner/conflict.go:36`), hands a permission-bypassed agent a
  scratch checkout of a conflicted merge; the runner commits and merges only
  if conflict markers are gone. The agent then acts with the user's
  authority inside the reviewed tree, with network access. Containment is
  prompt-level (embedded rules in `internal/prompt/rules/`, composed in
  `internal/prompt/compose.go`) plus process discipline (own session -- which
  detaches the controlling terminal, so an agent cannot open /dev/tty and put
  the operator's terminal into a raw mode where Ctrl-C stops generating
  SIGINT; the kernel's SIGTTOU guard does not cover a runtime that ignores
  SIGTTOU, which Node-style CLIs routinely do -- stdin is the null device,
  hard timeout with SIGKILL escalation, deferred process-group SIGKILL on
  normal exit to reap orphaned grandchild processes, and SIGKILL escalation on
  drain timeout to unblock readers when orphaned processes hold open stdout/stderr
  pipes (`internal/runner/exec.go:99-340, 135-141, 320-326, 447-480`). There is
  deliberately no OS sandbox (DESIGN.md non-goals).
  Anything crossing B1 that reaches the prompt crosses into B2 with this
  advisory fence as the only gate.
- **B3, agents <-> user terminal, dashboard, journal.** Agent output is
  untrusted display input; sanitization before any terminal write, including
  the two inspection paths (`--show-prompt`, `cmd/gauntlet/modes.go:67-68`;
  `show`'s journal replay, `cmd/gauntlet/runs.go:127`) and the dashboard feed
  (`internal/ui/ui.go:577-586`).
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload. The
  publishing side of the same channel is GitHub Actions: `release.yml` builds
  and uploads the assets `update` verifies (the write token is in the
  environment only for that upload), and `vulnscan.yml` runs
  govulncheck weekly and on `go.mod`/`go.sum` pull-request changes and main
  pushes (`.github/workflows/vulnscan.yml:10-22`). Actions are commit-pinned,
  the runner uses `ubuntu-24.04`, and checkout disables persisted credentials.
  The scanner is version-pinned through `GOVULNCHECK_VERSION` in `Makefile:34`,
  invoked by `make vuln` (`.github/workflows/vulnscan.yml:33-44`). Release and
  checksum downloads enforce `validateAssetURL` across HTTP redirects and cap
  redirects at 10 (`client.CheckRedirect`, `internal/selfupdate/selfupdate.go:139-153`).
  Stacked publication is a second internet path: the `gh` CLI, resolved from an
  absolute-only PATH (`internal/ghx/ghx.go:98-110`), using the operator's own
  `gh` credentials, not the `GH_TOKEN` gauntlet reads for self-update.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward GitHub (only when `GH_TOKEN` or
  `GITHUB_TOKEN` is set, and only on gauntlet's own HTTPS client) and toward
  each agent's model provider. Git stderr is redacted for embedded userinfo
  credentials before errors are printed or journaled (`internal/gitx/gitx.go:351-353`).
  `gh` and `git push` use whatever credentials those tools already have.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, `agents.json`. Journal paths are
  guarded by run ID validation (`validRunID`, `internal/journal/journal.go:938-958`)
  and date-sharded from run ID timestamps (`shardFromRunID`, `internal/journal/journal.go:1013-1036`).
  Optional `--log` crosses into a separate operator-selected output path, not
  necessarily that state directory (`cmd/gauntlet/main.go:218-238`).

## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | git `ls-files` glob, walk fallback, `internal/prompt/discover.go`; read `prompt.go` via `readNoFollow` (`prompt.go:257-298`) | regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap; skipDirs and hidden directories dropped; git-ignored files refused; project prompts override bundled ones of the same name; duplicate detection reads bounded |
| Prompt names (file stems) | `discover.go:68,88` | NFC-normalized keys (`prompt.go:307-309`); control/Cf characters reject the file with a warning (`discover.go:70,90`; strip at `prompt.go:313-315`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:90-104`, fed to the suggest catalog `compose.go:246-285` | name and description both fenced: `</catalog>` and `RELEVANT:` neutralized; 200-rune cap on a rune boundary (`compose.go:246-285`) |
| Prompt `Summary:` line | `prompt.go:123-140`, fed to stacked PR bodies `internal/runner/prbody.go` | sanitized and cut to 60 runes at read; PR rendering strips controls, flattens to one line, NFC-normalizes to prevent rune splitting, and bounds again (`prbody.go:26-44,86,146-173`) |
| Prompt `Signals:` line | `prompt.go:168-201`, consumed by the file-signal suggester `internal/runner/suggest_fast.go:488-490` | known kinds only (`ext`/`name`/`path`/`mark`), charset-restricted values, 12 tokens × 40 runes; anything else dropped |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR`, `--dirs` | `cmd/gauntlet/flags.go`, `cmd/gauntlet/paths.go`; parsed by `ParseSpecs` (`agent.go:333`), `ParseBin` (`agent.go:639`), `ParseAgentCmd` (`custom.go:283`), `discover.go:44-77`, `resolveDirs` (`paths.go:23-86`) | allow-listed tool names; dsh model charset-restricted (`dshModelRe`); `@effort` charset-restricted for every agent and refused where no verified flag exists (`effortRe`, `takesEffort`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`ParseBin`); non-empty checks for `--usage-cmd`, `--suggest-agent`, `--exclude`, `--merge-into`, `--pr-base`, `--show-prompt` (`cmd/gauntlet/flags.go:478-641`); target dirs expanded and deduplicated by realpath (`paths.go:23-86`); `--prompt-dir` takes regular files only, control-char names rejected; subcommands refuse flags they do not read (`rejectStrayFlags`, `flags.go:786`); branch commands separate options with `--` (`branch.go:86,167,409`, `worktree.go:535,543`) |
| Environment: `PATH` | `pathNoCWD` (`agent.go:175`), `resolveGit`/`gitEnv` (`gitx.go:84-96,371-407`), `ghx.binary` (`ghx.go:98-110`), `probeEnv`/`resolveProbe` (`usagelimit.go:108-138`) | cwd-relative and relative entries dropped for agent, git, git-child, `gh`, and usage probe resolution via `CleanPATH`/`AbsPATH` (`internal/runx/runx.go:98-114`) |
| Environment: `GAUNTLET_HOME`, `GH_TOKEN`/`GITHUB_TOKEN`, `TERM`/`NO_COLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR`, `GAUNTLET_STATE`, `GAUNTLET_NO_ANIMATION`, `GIT_SSH_COMMAND` | `gauntlethome.go:30-42`, `selfupdate.go:85-103`, `cmd/gauntlet/report.go:48-72`, `selfupdate/reload.go:20,184,192-226`, `internal/ui/ui.go:925-932`, `internal/gitx/gitx.go:387-390` | operator-controlled, same-user trust; `GAUNTLET_HOME` validates unresolvable environment variables at startup (`flags.go:422-426`) and degrades safely to local `.gauntlet` (`gauntlethome.go:30-42`); expands tildes and environment variables and is made absolute at resolution so the state root cannot depend on the current directory (`CustomFilePath`, `custom.go:407`); `GAUNTLET_STATE` handoff file is verified by `LoadState` to require `.json` extension, verified via `os.Lstat` as a regular file (rejecting symlinks and directories without deleting), and read capped to 16 MiB (`maxHandoffBytes`, `reload.go:184,192-226`); `SaveState` validates `runID` against directory traversal and path separators (`reload.go:133-136`); `GAUNTLET_NO_ANIMATION` disables TUI animation glyphs (`internal/ui/ui.go:925-932`); `GIT_SSH_COMMAND` overrides repository-local SSH commands (`internal/gitx/gitx.go:387-390`); color variables configure ANSI output (`cmd/gauntlet/report.go:48-72`); `--update-repo` is `owner/repo` only (`ParseRepo`, `selfupdate.go:60`) |
| Agent stdout/stderr | pipes in `exec.go:108-124`; line scan `exec.go:361-417` | 4 MiB per line emitted in chunks with UTF-8 rune boundary preservation (`exec.go:34-42,379-392`), trailing CR stripped (`exec.go:365-367`), escape/control/bidi/separator strip before terminal (`normalize.go:337-360 Display`), width cap 2000 cols (`exec.go:46`), rate limit 200 lines/s (`runner.go:26`); stalled command groups force-killed with SIGKILL on drain timeout (`exec.go:320-326`); even `--raw` passes `Display` (`exec.go:267-274`); usage and sink callbacks serialized across stdout and stderr streams (`exec.go:164,203,221`) |
| Stream-JSON events from agents | `internal/runner/exec.go:240-260`; `internal/streamjson/streamjson.go:81-99,107-138,217-244` | extraction depth capped at 8; objects marked role `tool`/`user` or type `user`/`tool_use`/`tool_result` contribute neither text nor usage; classification is not origin authentication; malformed JSON falls back to plain text and `--raw` bypasses classification |
| Token counters parsed from output and transcripts | `exec.go:139-178,273-287`; transcript watch off with `-tags notoktop`, `usage_toktop.go:17`; custom roots `custom.go:69` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:155` | HTTPS, 4 MiB decode cap; bounded by `fetch` (`selfupdate.go:336-362`); `owner/repo` charset-checked before it is concatenated into the API path |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:216` | validated by `validateAssetURL` (`selfupdate.go:300-334`) to require HTTPS and authorized GitHub release hosts (`github.com`, `api.github.com`, `objects.githubusercontent.com`, or loopback in tests); HTTP redirects re-verify `validateAssetURL` and are capped at 10 (`client.CheckRedirect`, `selfupdate.go:139-153`); checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap, SHA-256 must match before rename into place; `fetch` rejects responses exceeding size limit (`selfupdate.go:336-362`); a listing entry counts only as 64 hex digits (`checksumFor`/`isHexDigest`, `selfupdate.go:399-429`) |
| `gh` CLI JSON and PR URLs | `internal/ghx/ghx.go` `Find`/`Create` | argv-only; PR URL must be `https` on the expected host (`validateURL`, `ghx.go:229-235`); a PR is reused only when its head owner matches the push destination (`ownsHead`, `ghx.go:168-173`) |
| Git outputs (shortstat, porcelain, check-ignore) | `gitx.go:424,704,778` | regex/line parsing; C-quoted paths decoded (`unquoteC`, `gitx.go:815`) then display-sanitized before any message (`safePaths`, `internal/runner/runner.go:559-571`; dashboard `internal/ui/ui.go:577-586`; plain reporter `report.go:87`); counts only, never executed |
| Reviewed repo's `.git/config` and `.gitattributes` | every git call, `internal/gitx/gitx.go:38-52,229-265,329-370` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external`, `core.gitProxy` forced empty; `protocol.ext.allow=never`; `attr.tree` pointed at the empty tree so in-tree `.gitattributes` cannot select a smudge filter or merge driver; local `filter.*`/`merge.*`/`diff.*` commands, `core.editor`, `core.askpass`, and peers blanked from `--local --list`; extra safe config propagated to worktrees and sub-repos via `subRepo` (`internal/gitx/gitx.go:647-659`, `internal/gitx/worktree.go:33-51`); git resolved on absolute-only PATH so a planted `./git` cannot run; `GIT_SSH_COMMAND=ssh` outranks a repo-local `core.sshCommand` unless the operator already exported one, and git's children inherit that absolute-only PATH (`mergeGitEnv`); git's own children die with its process group and bounded pipe wait (`gitx.go:338-343`); git stderr redacts embedded userinfo credentials (`gitx.go:351-353`); branch commands separate options with `--` (`internal/gitx/branch.go:86,167,409`, `internal/gitx/worktree.go:535,543`) |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:113-129` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized before it reaches a terminal (`lock.go:22-25,84-109,113-129`); `Note` and `readNote` handle `EINTR` and partial writes in retry loops (`lock.go:94-106,115-128`); lock release preserves the descriptor flock and truncates the note to prevent file deletion and inode recycling races (`lock.go:131-145`) |
| Conflicted paths interpolated into the conflict prompt | `git diff -z` into `runConflictAgent`, `conflict.go:101-135`, then `ConflictPrompt` (`compose.go:204-244`) | a path is named only when it equals `normalize.Sanitize(p)` and `conflictPathOK` (control/Cf omitted, `RESOLVE:` and `</files>` omitted, 1024-rune cap); 50-file cap (over-cap skips the launch); list fenced in `<files>`; a dropped path is still scanned for markers, so it holds the branch with a human |
| Helper-tool inventory appended to prompts | PATH probe at startup, `internal/runner/runner.go:548-557`, rendered `compose.go:85-115` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| File-signal suggester tree walk | `suggest_fast.go:535,640-670` | 100k files, depth 12, 2k file heads × 4 KiB; opens via `os.OpenRoot` (`suggest_fast.go:682`) so a symlink or path that escapes the reviewed tree is skipped |
| `~/.gauntlet/agents.json` | `internal/agent/custom.go:86-170,283,306-340,407-412` | rejects null, unknown fields, duplicate keys (including case-variant duplicates and nested usage duplicates), trailing data, and built-in redefinitions; requires `{prompt}` in argv exactly once and forbids `{prompt}` in model/effort/stream/continue; requires `{model}` in model and `{effort}` in effort if set; rejects empty/blank arguments in argv, model, effort, stream, and continue; validates non-empty usage roots and model; rejects whitespace-only usage suffix; validates all definitions before registration; strips UTF-8 BOM if present (`custom.go:314`); `CustomFilePath` (`custom.go:407`) returns empty without a state root; file read has no size cap and follows symlinks, so state storage must remain trusted |
| `--usage-cmd` probe | `internal/runner/usagelimit.go:85-149` | argv-only; isolated execution directory (`os.TempDir()`); relative and cwd-relative entries dropped from `PATH` in `probeEnv()` and executable resolved against absolute-only PATH (`resolveProbe`); 10s deadline, 4 KiB per output stream; own process group killed on return as well as cancellation; final non-empty line parsed as finite 0-100 (`parseUsagePercent`, `internal/runner/usagelimit.go:150-179`); failure warns once and leaves the threshold unenforced; finish flag set via atomic swap (`internal/runner/usagelimit.go:69-72`) |
| `--log FILE` destination | `cmd/gauntlet/main.go:218-238` | append-open requests 0600; a successful regular-file stat triggers chmod to 0600 before output; no no-follow open, regular-file requirement, directory confinement, or size/rotation limit |
| `dsh --dump-config` probe | `dshDefaultProvider`, `internal/agent/dsh.go:67-85` | runs only for a `dsh:<model>` pin; own process group, 120s cap, SIGKILL on the group; provider parsed with a narrow regex (`dshProviderRe`); overlay values charset-restricted before they are quoted into YAML (`dshModelRe`, `agent.go:320`); provider and model validated against `dshModelRe` (`dsh.go:98-105`); overlay key rejects path separators and traversal (`dsh.go:117-120`) |
| Interactive launcher / picker keyboard input | `cmd/gauntlet/pick.go`, `internal/ui/pick.go`, `internal/ui/ui.go` | navigation keys jump to bounds (`g`/`G` in `internal/ui/pick.go:347-353`); empty filter matches block launch with warning message (`internal/ui/pick.go:983-989`); Enter key terminates completed runs (`internal/ui/ui.go:340-344`) |
| Planted symlinks/FIFOs in the tree | prompt reads `prompt.go:257-298`, lock creation `runner/lock.go:48-68`, untracked counting `gitx.go:483-556`, reload handoff `reload.go:192-226` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stat after open, stat errors propagated on open regular files (`gitx.go:545-555`); `LoadState` verifies regular file with `Lstat` (`reload.go:200-206`) |

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

**B2 (agent misbehavior directly).** Spoofing: fabricated agent output cannot
drive the terminal (B3 mitigations) but fabricated *content* flows into
commits, so attribution of prose is by review name, not by verified origin.
Who authors the commits depends on the mode, and the difference is a real
privilege transition:

- **`--jobs > 1` (worktree isolation):** the runner stages and commits each
  worktree itself (`Worktree.CommitAll`, called from `runIsolated` in
  `internal/runner/runner.go`); agents stay forbidden to run git (DESIGN.md
  rule 3). Between retry attempts the runner alone rewinds its worktree to
  the base commit so attempt N+1 starts where N did (`ResetToBase` in
  `internal/gitx/worktree.go:468-479`, called from `resetForRetry`): more runner-side
  git authority, exercised only inside gauntlet-created worktrees.
  Persistent lanes reuse one worktree across reviews; each advance or retry
  still starts from a known commit.
- **`--stacked-prs` (worktree isolation plus publication):** the runner has
  the same staging, commit, and retry-reset authority inside one scratch
  worktree, then invokes Git to push each committed child branch and `gh` to
  create its PR. `PrepareStack` (`internal/runner/stack.go:39`) runs every
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
  pushes to (`ownsHead` in `internal/ghx/ghx.go:168-173`): a PR opened from
  someone else's fork under a name a run is about to use is ignored, not
  adopted. Git and `gh` receive fixed argv elements rather than shell text;
  branch commands separate branch names with `--` option delimiters
  (`internal/gitx/branch.go:86,167,409`, `internal/gitx/worktree.go:535,543`);
  review names, commit subjects, and PR bodies cannot become commands. A PR
  body is assembled from values that originate in the reviewed repository --
  the agent's commit subject, the review prompt's own summary line, the paths
  its commit touched -- so each is stripped of control characters, flattened
  to a single line, NFC-normalized to prevent rune splitting, and length-bounded
  before it is rendered, and a path is escaped into a code span that a backtick
  in it cannot close (`internal/runner/prbody.go:26-44,86,146-173`). Markdown posted to
  GitHub is display, not execution, but a forged heading or an unbounded path
  list is still a reviewer reading something the run did not say. Git itself
  runs hardened against the reviewed repository's own config (see the `.git/config`
  row). Gauntlet's scratch worktrees are refused when `.gauntlet` or
  `.gauntlet/worktrees` is a symlink or not a real directory inside the
  repository (`ensureWorktreeRoot` in `internal/gitx/worktree.go:109-146`);
  worktree paths are verified to remain strictly inside `.gauntlet/worktrees`,
  leftover checkout directories and pruned metadata are purged on preparation
  and removal, and worktree operations on removed checkouts safely error or
  no-op (`removeWorktreeDir` and `Worktree.Remove` in `internal/gitx/worktree.go:211-237,505-518`).
- **Sequential in-place with `--commit`/`--push`:** a failed review's retry
  restores a snapshot of the user's checkout taken before the attempt
  (`Snapshot`/`Restore` in `internal/gitx/snapshot.go`), the same rewind
  authority as a worktree reset, bounded to putting back files the user
  already had. The commit step is itself an agent launch. `runCommitStep`
  (`commit.go:137`) execs one agent with
  `prompt.CommitPrompt`, which instructs it to run `git commit` and never
  to push; the runner strips injected AI attribution trailers from the new
  commit (`StripAITrailers`) and pushes itself under `--push`
  (`internal/runner/commit.go:200-216`). Neither trailer stripping nor a clean
  tracked-file status is a security inspection of the committed content, and
  neither prevents the agent from invoking Git itself. Under `--yolo` a
  rejected push escalates to a runner-side `git pull --rebase` and retry; a
  rebase conflict fails the step rather than returning to the agent. The
  same launch, offered standalone when a dirty tree blocks `--jobs`, is
  gated on explicit consent (`main.go:1207-1240`). The prompt is embedded
  text only (`rules/commit.md`), capped at 5 minutes (`commit.go:24`),
  under the same process discipline as any review.
- **Conflict resolution:** `resolveConflict` (`conflict.go:36`) cuts a
  scratch checkout, replays the branch, and launches one agent with
  `ConflictPrompt` (`compose.go:204-244`, rules in `rules/conflict.md`) for up to
  10 minutes (`conflict.go:25`). The prompt lists only sanitization-safe
  paths and forbids git; the runner commits and merges the result, or keeps
  the original branch if markers remain. Same advisory fence, same
  permission-bypassed process, narrower file set.
- **`--usage-cmd` (the usage-limit probe):** the operator supplies argv to
  decide whether more reviews start (`internal/runner/usagelimit.go:47-73`).
  `probeUsage` isolates execution from the reviewed directory: `cmd.Dir` is
  set to `os.TempDir()`, `cmd.Env` strips cwd-relative and relative PATH
  entries (`probeEnv`), and bare executable names are resolved only against
  absolute PATH entries (`resolveProbe`) (`internal/runner/usagelimit.go:85-149`).
  No shell is added, but an operator-selected relative executable or script path
  (e.g. `./probe.sh` or an explicit path into the tree) can still consume
  repository content. The probe has a 10-second deadline, a 5-second pipe
  wait bound, and 4 KiB per output stream; excess output fails the probe.
  `runx.Bound` provides a process group and cancellation kill (`internal/runx/runx.go:56-74`); a deferred
  group kill also cleans up remaining group members after the direct child exits
  (`internal/runner/usagelimit.go:94-96`). Neither prevents a child from
  deliberately escaping its process group.
  Parsing takes the last non-empty line, accepts a trailing `%`, and rejects
  non-finite or out-of-range values (`internal/runner/usagelimit.go:150-179`).
  Failure warns once and leaves the usage threshold unenforced; later checks
  may succeed. Spending can continue beyond the intended threshold, though
  this does not enlarge the independently configured runtime budget. A valid
  but false low reading also permits continued scheduling. The probe is
  trusted telemetry, not an authenticated quota service.

Repudiation is only partly addressed: when journaling is available, a run
records seed, schedule, outcomes, and the prompt fingerprint each launch ran under
(`internal/runner/event.go`, DESIGN.md "Run journal"), the
commit step is journaled as its own event with the chosen agent
(`internal/runner/commit.go:222-225`, kind at `internal/runner/event.go:24`), and worktree-mode commits carry
the runner as author. Stacked publication adds a `pull_request` event carrying
the exact head, base, status, and URL; the terminal summary repeats every URL.

**B3.** Display sanitization addresses terminal escape injection, not the
truth or origin of printable content. The composed prompt shown by
`--show-prompt` (`modes.go:67-68`), the journal replayed by `show`
(`runs.go:127`), the plain reporter (`report.go:87`), and the dashboard feed
(`internal/ui/ui.go:577-586`) pass through the same stripping, which removes ASCII controls,
Unicode formatting/bidi (`Cf`), and line/paragraph separators (`Zl`/`Zp`), and
truncates at whole grapheme cluster boundaries (`internal/normalize/normalize.go:337-380`).
Stream classification excludes recognized tool/user objects before their text reaches the report
parsers, but trusts the producer's role/type fields
(`internal/streamjson/streamjson.go:217-244`,
`internal/runner/exec.go:240-260`). A compromised agent can still supply false
assistant text or plain-text report lines. Output buffering has line and tail caps, but
`--raw` bypasses normalizer rate/width limits while retaining `Display`
(`internal/runner/exec.go:267-274`). Usage and sink callbacks are serialized
(`sinkMu`, `usageReportMu`) across concurrent stdout/stderr streams (`internal/runner/exec.go:164,203,221`),
and transcript watcher lifecycle cleanup is synchronized with reviews (`internal/runner/runner.go:1049-1055`).
When readers are stalled by orphaned grandchildren, gauntlet issues a process-group SIGKILL on
drain timeout (`exec.go:320-326`). Parser bounds do not limit a child's disk writes, network
traffic, CPU, or provider spend. A non-positive process timeout also removes the per-launch
deadline (`internal/runner/exec.go:293-298`).

**B4.** Update metadata is requested over HTTPS; release asset and checksum downloads
are constrained by `validateAssetURL` (`internal/selfupdate/selfupdate.go:300-334`) to
HTTPS on authorized GitHub release hosts (`github.com`, `api.github.com`,
`objects.githubusercontent.com`, or loopback in tests), preventing cleartext transfers
or links pointing to untrusted third-party infrastructure. Download requests validate
redirect targets against the same host allowlist and stop after 10 redirects (`client.CheckRedirect`,
`internal/selfupdate/selfupdate.go:139-153`). Downloads are compared
with SHA-256 entries in `checksums.txt`
(`internal/selfupdate/selfupdate.go:155-170,216-298,399-429`).
Download responses are bounded and verified by `fetch` to reject oversized responses
exceeding the limit (`internal/selfupdate/selfupdate.go:336-362`).
The checksum proves the download matches the listing, not that the publisher
is benign: the same publisher controls both. Compromise of the GitHub repo
or its release process yields arbitrary code execution on updating machines,
amplified by hot reload re-exec'ing the new binary mid-run without user
confirmation (`internal/selfupdate/reload.go:228-251`). No signed-release mechanism exists. The
advisory scanner in CI (`vulnscan.yml`) watches the dependency graph, not this
channel. The `bunx @deepseek-ai/dsh` fallback (and the `--dump-config` probe
that uses the same argv) is a second fetch-and-execute path, from the npm
registry rather than GitHub releases, with no checksum (`internal/agent/agent.go:544-555`,
`internal/agent/dsh.go:67-85`). Overlay configurations validate provider and model identifiers
against `dshModelRe` (`internal/agent/dsh.go:98-105`), and overlay keys reject path traversal
(`internal/agent/dsh.go:117-120`).

**B5.** Secret egress: the updater explicitly attaches `GH_TOKEN` /
`GITHUB_TOKEN` only to requests whose scheme is HTTPS and hostname is
`api.github.com` or `github.com` (`internal/selfupdate/selfupdate.go:96-103`).
Asset and checksum endpoints are separately checked against the HTTPS release host
allowlist (`validateAssetURL`, `internal/selfupdate/selfupdate.go:300-334`).
Git error messages redact embedded basic-auth userinfo credentials (`runx.RedactUserinfo`,
`internal/gitx/gitx.go:351-353`) before display or journaling. Agent CLIs receive the inherited
environment and hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal creation requests 0600 files and 0700 directories
(`internal/journal/journal.go:152-155,256-259`); handoffs use a sibling temp
file and atomic rename (`internal/selfupdate/reload.go:133-168`). Stale temp
files in the journal index directory are swept periodically
(`internal/journal/journal.go:670-722`). Run IDs from CLI flags or events
are strictly validated against directory traversal and non-safe charsets (`validRunID`,
`internal/journal/journal.go:938-958`), and date shards derive from the run ID timestamp
(`shardFromRunID`, `internal/journal/journal.go:1013-1036`). Creation
modes do not tighten permissions on pre-existing paths or authenticate local
state. Journal opens follow existing paths, so a protected state directory is
a precondition, not an enforced property of any `GAUNTLET_HOME` value
(`GAUNTLET_HOME` expands tildes and environment variables, with unresolvable
references rejected at startup via `cmd/gauntlet/flags.go:422-426` and safe degradation
via `internal/gauntlethome/gauntlethome.go:30-42`).
`LoadState` (`internal/selfupdate/reload.go:184,192-226`) requires the `.json` extension,
verifies via `os.Lstat` that the state file is a regular file (rejecting symlinks, directories,
and non-regular files without deleting target), and bounds reads to 16 MiB (`maxHandoffBytes`),
rejecting oversized files before removal. `SaveState` (`internal/selfupdate/reload.go:133-136`)
validates `runID` against directory traversal and path separators. The tree lock's symlink
check is a separate control (`internal/runner/lock.go:48-68`); `Note` and `readNote` handle
partial writes and `EINTR` retry loops (`internal/runner/lock.go:94-106,115-128`), and lock release
preserves the open file descriptor flock and truncates the note to prevent
file deletion and inode recycling races (`internal/runner/lock.go:131-145`).
A malicious agent already runs as that operator and can tamper with local evidence.

`--log` is a separate confidentiality and availability boundary: it retains
output that the journal omits. Regular files are chmod'd to 0600 when stat
succeeds, including existing files, but append-open follows symlinks and does
not require a regular file (`cmd/gauntlet/main.go:218-238`). The operator must
control the destination and its parent directories. Display sanitization does
not redact arbitrary secrets (`internal/runner/exec.go:261-267`), and this
writer has no rotation or size bound. Same-user agents can read or alter the
log just as they can the journal.

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, `protocol.ext.allow=never`, `attr.tree` empty, local drivers blanked, absolute-only git PATH, `GIT_SSH_COMMAND=ssh`; propagated to worktrees and sub-repos via `subRepo`; branch commands separate options with `--` | `internal/gitx/gitx.go:38-52,229-265,329-370`, `gitx.go:647-659`, `worktree.go:33-51`, `branch.go:86,167,409`, `worktree.go:535,543` |
| Runaway git grandchildren (hooks, merge drivers) holding pipes | process-group SIGKILL on deadline, bounded WaitDelay | `gitx.go:338-343` |
| Planted executables shadowing agents/git/`gh` | cwd-relative PATH entries stripped for agent, git, git-child, `gh`, and usage probe resolution | `agent.go:175`, `gitx.go:84-96,371-407`, `ghx.go:98-110`, `usagelimit.go:108-138` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps; stat errors propagated on open regular files; suggester peeks via `os.OpenRoot`; reload handoff verified regular file via `Lstat` | `prompt.go:257-298`, `gitx.go:483-556`, `runner/lock.go:48-68`, `suggest_fast.go:682`, `reload.go:200-206` |
| Prompt injection blending into containment rules | begin/end markers, both markers escaped in the body, report-section stripping fails open | `compose.go:34-39,53-75,170-175` |
| Injection via suggest catalog | description *and* name sanitize, fence-neutralizing, 200-rune cap, strict suggestion grammar checked against known set, reasons capped to the same budget | `compose.go:246-285` |
| Injection via `Signals:` into the file-signal suggester | known kinds, charset, 12×40-rune caps; `mark:` values search file heads, not executed | `prompt.go:168-201`, `suggest_fast.go:488-490,711` |
| Terminal-driven or spoofed output, including prompt preview, journal replay, reporter, and dashboard | `Display`/`Sanitize` strip escapes, controls, bidi, and separators; width cap; rate limit; duplicate collapse; grapheme-preserving truncation; usage and sink callbacks serialized | `normalize.go:337-380`, `modes.go:67-68`, `runs.go:127`, `report.go:87`, `internal/ui/ui.go:577-586`, `exec.go:42,46,164,203,221,267-274`, `runner.go:26` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before a terminal write | `gitx.go:815`, `internal/runner/runner.go:559-571`, `lock.go:22-25`, `conflict.go:101-135` |
| Hostile file names forging conflict-prompt instructions | drop unsanitary paths (control/Cf, `RESOLVE:`, fence-closer); 1024-rune path cap; 50-file cap (over-cap skips the launch); list fenced in `<files>`; markers still block the merge | `compose.go:204-244`, `conflict.go:101-135` |
| Model output written as a commit subject | ASCII controls, Unicode Cf (bidi), Zl/Zp stripped; grapheme cluster preserved; 100-rune / 72-rune caps; one line | `internal/agent/usage.go:185-242`, `internal/runner/subject.go:184-199` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks with UTF-8 rune boundary preservation, trailing CR stripped, bounded tail buffers (1 MiB suggest tail) | `exec.go:34-42,365-367,379-392,447-461` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:33-37`, `agent.go:448-454,488-494` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, deferred SIGKILL on normal exit to clean up orphaned grandchildren, drain timeout process group SIGKILL escalation to unblock stuck readers, stdin null device, own session (no controlling terminal, so Ctrl-C cannot be disabled from inside an agent), drain grace for stuck grandchildren | `exec.go:25-32,99-340,135-141,320-326,447-480` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; runner-side publication is workflow separation, not removal of agent credentials | `internal/runner/commit.go:24,137-226` |
| Conflict step running away | separate 10-minute cap, same process discipline; unresolved markers keep the branch | `conflict.go:25,36-88` |
| Unbounded or unauthorized downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; HTTPS and authorized GitHub release host check (`validateAssetURL`); HTTP redirects re-verify host allowlist and stop after 10 redirects; fetch strictly rejects responses exceeding limit; digest-shaped entries only; verify-before-rename, atomic replace | `selfupdate.go:112,139-153,155,216,300-334,336-362,399-429` |
| Partial binary observed by reload | two immediate identical stat readings reject visible changes, but do not prove completeness or authenticity; safe replacement depends on atomic writers | `internal/selfupdate/reload.go:83-113`; updater rename at `internal/selfupdate/selfupdate.go:268` |
| Concurrent agents corrupting one tree | flock per directory with inode preservation across releases; partial-write and EINTR retry handling on note I/O; parallelism only across directories or with worktree isolation + serialized merges; worktree paths confined to `.gauntlet/worktrees`, orphaned directory cleanup on add and remove; a conflict is resolved in a scratch checkout or keeps its branch | `runner/lock.go:48-145`, `worktree.go:211-237,505-518`, DESIGN.md concurrency section |
| Accidental launch of unconstrained run from empty filter | picker blocks launch when active filter matches zero reviews, displaying clear warning | `internal/ui/pick.go:983-989` |
| Reviewed tree defining its own agents | `CustomFilePath` empty without state root; argv is exec, not shell; argv, model, effort, stream, continue reject blank elements; case-insensitive duplicate field detection, single `{prompt}` placeholder requirement, prompt forbidden in model/effort/stream/continue, `{model}` and `{effort}` required when defined, UTF-8 BOM stripped, non-empty model and usage roots, usage.suffix cannot be whitespace only | `custom.go:86-170,283,306-412` |
| Directory traversal or poisoned journal run IDs | run ID length bounded (<= 128), charset-restricted (`[a-zA-Z0-9_.-]`), ".." rejected; date sharding derived from run ID timestamp | `internal/journal/journal.go:134,614,938-958,1013-1036` |
| Embedded basic-auth credentials in remote URLs | userinfo stripped from git stderr strings before errors are returned, printed, or journaled | `runx.RedactUserinfo`, `internal/gitx/gitx.go:351-353` |
| Known-vulnerable dependencies shipping to users | govulncheck weekly and on dependency changes in CI | `.github/workflows/vulnscan.yml` |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential;
   consider signing releases or documenting the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:230`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`internal/agent/agent.go:544-555`). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:294-315`); a
   `dsh:<model>` pin additionally execs the same argv as `--dump-config`
   (`internal/agent/dsh.go:67-85`). Documentation should say plainly that naming `dsh`
   without the launcher installs and runs an npm package, including at
   probe time.
3. **No SECURITY.md.** There is no documented path from "vulnerability
   reported" to "fix shipped": no disclosure contact, no supported-version
   statement. Creating one requires an owner decision, so it is only noted
   here.
4. **R6, resource and budget enforcement.** The launch path sets no OS
   resource limits (`internal/runner/exec.go:103-107`); the optional usage
   threshold is checked between reviews and fails open on probe errors
   (`internal/runner/usagelimit.go:47-73,85-149`). Neither is a hard spend quota.
5. **Secrets hygiene around spawned agents.** Gauntlet inherits the full
   operator environment to every agent; a scrubbed env or documented
   container workflow would shrink R3. Design decision, not a bug.

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
  (`internal/runner/commit.go:200-216`). Those checks do not inspect the diff
  for secrets or authorize its content. The same-user agent can also publish
  directly despite the prompt prohibition. Containment is advisory, not a
  credential or network restriction (`internal/runner/exec.go:103-107`).
- **Consent-surfaced commit.** Refuse `--jobs` on a dirty tree and gauntlet
  offers to hand that tree, unreviewed, to an agent that commits it; `--yes`
  or `--yolo` on the original command is that consent, so an operator who
  scripts those flags has pre-approved agent-authored commits of whatever
  the reviews left behind (`main.go:1207-1240`).
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:204,264-285`) bound it to reviews that exist
  in the discovered set, including the attacker's own.
- **Signals: steering.** A planted `Signals:` line on a project prompt is
  parsed into the file-signal suggester (`prompt.go:168-201`,
  `matchDeclared` in `suggest_fast.go:488-490`). Charset, count, and length are
  bounded; a matching tree still lets the attacker add their review to the
  auto-picked set. Same outcome as suggestion gaming, different path, and it
  does not need an agent (`--suggest-agent gauntlet`).
- **Conflict-step write.** After a merge conflict, `resolveConflict`
  (`conflict.go:36`) launches an agent in a scratch checkout with the
  conflicted paths named in the prompt. The agent edits those files; the
  runner commits and merges if markers are gone. Containment is the same
  advisory fence. Unresolved markers keep the branch.

- **Tool-output laundering.** Repository text returned by a tool can contain
  report-shaped prose. Recognized tool/user stream objects are excluded before
  tail parsing, so their text and counters are not treated as the assistant's
  report (`internal/streamjson/streamjson.go:217-244`,
  `internal/runner/exec.go:240-260`). Regression coverage pins this class in
  `internal/streamjson/streamjson_test.go`; unmarked output or an assistant
  repeating the text remains unauthenticated.

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was re-checked against the code on 2026-09-02 and
  matches the controls it names (O_NOFOLLOW prompt reads, cwd-free PATH
  resolution, forced-empty git config, display sanitization, directory
  flock). It does not list the later git overlays (`attr.tree`, local
  driver blanks, `protocol.ext.allow=never`, `GIT_SSH_COMMAND=ssh`); those
  are additional, not contradictory.
- Response readiness: the journal supports run reconstruction, not a complete
  security audit. Agent output and live usage events are explicitly excluded
  (`cmd/gauntlet/main.go:500-506`), writes are buffered, and write failures are
  retained for reporting at close (`internal/journal/journal.go:164-192,202-220`).
  It does not authenticate agent claims or record every child action; same-user
  agents can alter local evidence. The disclosure and supported-version gaps
  above remain undocumented organizational decisions.
- Verification scope and baseline: 2026-09-21 against commit ef4b056. Verified
  reload handoff state file validation (.json extension, Lstat regular file check,
  16 MiB size cap) and run ID traversal rejection, self-update fetch response size
  bounds, branch operation argument separation with dashes (--), worktree directory
  confinement under .gauntlet/worktrees, orphaned directory recovery, and safe operations
  on removed worktrees, GAUNTLET_HOME environment variable expansion validation with safe
  degradation, custom agent blank argument and whitespace usage suffix validation,
  dsh overlay provider/model charset validation and overlay key traversal rejection,
  exec output and usage callback serialization across streams, atomic lock note I/O
  handling EINTR and partial writes, transcript watcher lifecycle synchronization,
  picker launch blocking on empty filter matches and dashboard Enter key termination when done,
  usage limit warning deduplication via atomic swap, and updated line citations across
  the codebase. Older inventory references are retained rather than represented as
  newly verified.

