# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree, typically with their permission systems
bypassed or auto-approved. This document is the systemic view; individual
vulnerability findings belong to sec-review and are recorded here only as
threats.

Last reviewed: 2026-09-02 against commit 798fa00. Owner and review cadence are
organizational decisions; none is assigned here.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | Prompt injection from the reviewed tree drives an agent running with bypassed or auto-approved permissions | B1 -> B2 | Accepted by design; containment is advisory text plus process discipline, never an OS sandbox |
| R2 | Self-update integrity rests on TLS and repository ownership; `checksums.txt` authenticates nothing beyond transport consistency, and hot reload execve's the replaced binary automatically | B4 | Named gap |
| R3 | A prompt-injected or compromised agent reads every secret its user can: environment-inherited API keys, agent config stores, SSH keys, `~/.netrc` | B2/B5 | Consequence of R1; containerization is the documented answer (DESIGN.md non-goals) |
| R4 | Confidentiality of reviewed source: agents send code to third-party model APIs over the network | B2 | Inherent to the tool's purpose; users must know it |
| R5 | `dsh` without a launcher on PATH falls back to `bunx`, fetching `@deepseek-ai/dsh` from the npm registry and executing it; a `dsh:<model>` pin also runs that argv as `--dump-config` before the review | B4 | Named gap (deliberate feature, unreviewed supply-chain hop) |
| R6 | DoS via oversized or hostile input | all | Well bounded; caps listed under Mitigations |

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  (DESIGN.md "Isolated parallel reviews", rule 1), and why the runner rewinds
  only its own worktrees between retry attempts (`git reset --hard` +
  `git clean -fd`, `ResetToBase` in `internal/gitx/worktree.go`; never the
  user's checkout).
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
  argv: gauntlet does not add a bypass flag. With `--push`, one launch per
  loop is additionally granted git commit and remote-push authority
  (`runCommitStep`, `internal/runner/commit.go:97`; prompt in
  `internal/prompt/rules/commit.md`, composed at `compose.go:147`). The same
  hand-off exists outside a loop: when `--jobs` refuses a dirty tree,
  `commitFirst` offers to give the uncommitted work to one agent, consented
  by `--yes`/`--yolo` or an interactive confirmation, never on an unattended
  guess (`cmd/gauntlet/main.go:1121-1152`, executed by `runner.CommitNow`,
  `internal/runner/commit.go:45`). A third launch, `resolveConflict`
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
  hard timeout with SIGKILL escalation; `internal/runner/exec.go:81-290,
  386-419`). There is deliberately no OS sandbox (DESIGN.md non-goals).
  Anything crossing B1 that reaches the prompt crosses into B2 with this
  advisory fence as the only gate.
- **B3, agents <-> user terminal, dashboard, journal.** Agent output is
  untrusted display input; sanitization before any terminal write, including
  the two inspection paths (`--show-prompt`, `cmd/gauntlet/modes.go:56-57`;
  `show`'s journal replay, `cmd/gauntlet/runs.go:94`) and the dashboard feed
  (`internal/ui/ui.go:523-528`).
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload. The
  publishing side of the same channel is GitHub Actions: `release.yml` builds
  and uploads the assets `update` verifies (the write token is in the
  environment only for that upload), and `vulnscan.yml` runs
  govulncheck weekly and on every `go.mod`/`go.sum` change (actions pinned by
  commit, runner images pinned, `persist-credentials: false`; the scanner
  itself is deliberately unpinned because its result never becomes a build
  input). Stacked
  publication is a second internet path: the `gh` CLI, resolved from an
  absolute-only PATH (`internal/ghx/ghx.go:81-97`), using the operator's own
  `gh` credentials, not the `GH_TOKEN` gauntlet reads for self-update.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward GitHub (only when `GH_TOKEN` or
  `GITHUB_TOKEN` is set, and only on gauntlet's own HTTPS client) and toward
  each agent's model provider. `gh` and `git push` use whatever credentials
  those tools already have.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, `agents.json`.

## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | tree walk, `internal/prompt/discover.go:124`; read `prompt.go:63` via `readNoFollow` (`prompt.go:249-272`) | regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap (`prompt.go:36`); project prompts override bundled ones of the same name (`discover.go:104-111`); duplicate detection reads bounded (`discover.go:179-207`) |
| Prompt names (file stems) | `discover.go:68,88` | NFC-normalized keys (`prompt.go:307-308`); control/Cf characters reject the file with a warning (`discover.go:70,90`; strip at `prompt.go:315`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:87-103`, fed to the suggest catalog `compose.go:193` | name and description both fenced: `</catalog>` and `RELEVANT:` neutralized (`compose.go:200-207`); 200-rune cap on a rune boundary (`compose.go:174,212-223`) |
| Prompt `Summary:` line | `prompt.go:122-142`, fed to stacked PR bodies `internal/runner/prbody.go` | sanitized and cut to 60 runes at read; PR rendering strips controls, flattens to one line, and bounds again (`prbody.go:19-28`) |
| Prompt `Signals:` line | `prompt.go:144-199`, consumed by the file-signal suggester `internal/runner/suggest_fast.go:460,490` | known kinds only (`ext`/`name`/`path`/`mark`), charset-restricted values, 12 tokens × 40 runes; anything else dropped |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR` | `cmd/gauntlet/flags.go`; parsed by `ParseSpecs` (`agent.go:308`), `ParseBin` (`agent.go:592`), `ParseAgentCmd` (`custom.go:223`), `discover.go:44-77` | allow-listed tool names; dsh model charset-restricted (`dshModelRe`); `@effort` charset-restricted for every agent and refused where no verified flag exists (`effortRe`, `takesEffort`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`ParseBin`); `--prompt-dir` takes regular files only, control-char names rejected; subcommands refuse flags they do not read (`rejectStrayFlags`, `flags.go:679`) |
| Environment: `PATH` | `pathNoCWD` (`agent.go:161`), `resolveGit`/`gitEnv` (`gitx.go:82-94,329-361`), `ghx.binary` (`ghx.go:86-97`) | cwd-relative and relative entries dropped for agent, git, git-child, and `gh` resolution |
| Environment: `GAUNTLET_HOME`, `GH_TOKEN`/`GITHUB_TOKEN`, `TERM`/NO_COLOR, `GAUNTLET_STATE` | `gauntlethome.go:30`, `selfupdate.go:83-97`, `cmd/gauntlet/report.go:48-72`, `selfupdate/reload.go:18,192` | operator-controlled, same-user trust; `GAUNTLET_HOME` made absolute at resolution so the state root cannot depend on the current directory (`CustomFilePath`, `custom.go:294`); `--update-repo` is `owner/repo` only (`ParseRepo`, `selfupdate.go:60`) |
| Agent stdout/stderr | pipes in `exec.go:108-118`; line scan `exec.go:300` | 4 MiB per line emitted in chunks (`exec.go:34-42`), escape/control/bidi strip before terminal (`normalize.go:332 Display`), width cap 2000 cols (`exec.go:46`), rate limit 200 lines/s (`runner.go:27`); even `--raw` passes `Display` (`exec.go:219-224`) |
| Stream-JSON events from agents | `streamjson.Parse` at `exec.go:204` | envelope-agnostic parser; thinking lines sanitized + capped (`exec.go:360-374`) |
| Token counters parsed from output and transcripts | `exec.go:139-178,273-287`; transcript watch off with `-tags notoktop`, `usage_toktop.go:17`; custom roots `custom.go:69` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:139` | HTTPS, 4 MiB decode cap; `owner/repo` charset-checked before it is concatenated into the API path |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:199` | checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap, SHA-256 must match before rename into place; a listing entry counts only as 64 hex digits (`checksumFor`/`isHexDigest`, `selfupdate.go:328-358`) |
| `gh` CLI JSON and PR URLs | `internal/ghx/ghx.go` `Find`/`Create` | argv-only; PR URL must be `https` on the expected host (`validateURL`, `ghx.go:192`); a PR is reused only when its head owner matches the push destination (`ownsHead`, `ghx.go:159`) |
| Git outputs (shortstat, porcelain, check-ignore) | `gitx.go:425,614,655,673,692` | regex/line parsing; C-quoted paths decoded (`unquoteC`, `gitx.go:692`) then display-sanitized before any message (`safePaths`, `runner.go:364`; dashboard `ui.go:523-528`; plain reporter `report.go:84-88`); counts only, never executed |
| Reviewed repo's `.git/config` and `.gitattributes` | every git call, `gitx.go:25-39,197-259,282-361` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external`, `core.gitProxy` forced empty; `protocol.ext.allow=never`; `attr.tree` pointed at the empty tree so in-tree `.gitattributes` cannot select a smudge filter or merge driver; local `filter.*`/`merge.*`/`diff.*` commands, `core.editor`, `core.askpass`, and peers blanked from `--local --list`; git resolved on absolute-only PATH so a planted `./git` cannot run; `GIT_SSH_COMMAND=ssh` outranks a repo-local `core.sshCommand` unless the operator already exported one, and git's children inherit that absolute-only PATH (`gitEnv`); git's own children die with its process group and bounded pipe wait (`gitx.go:305-317`) |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:98` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized before it reaches a terminal (`lock.go:22-25,88,98`) |
| Conflicted paths interpolated into the conflict prompt | `ConflictPrompt`, `compose.go:165`; filtered in `runConflictAgent`, `conflict.go:90-114` | a path is named only when it equals `normalize.Sanitize(p)`; a control-carrying name is omitted and still scanned for markers, so it holds the branch with a human |
| Helper-tool inventory appended to prompts | PATH probe at startup, `runner.go:327`, rendered `compose.go:90-114` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| File-signal suggester tree walk | `suggest_fast.go:535,645` | 100k files, depth 12, 2k file heads × 4 KiB; opens via `os.OpenRoot` so a symlink or path that escapes the reviewed tree is skipped (`suggest_fast.go:640-667`) |
| `~/.gauntlet/agents.json` | `custom.LoadCustomFile`, `custom.go:253` | malformed file refuses startup rather than silently changing the agent set; unknown JSON keys are errors (`custom.go:273-278`); `CustomFilePath` returns empty when HOME is missing so a definitions file is never picked up from `./.gauntlet` in the reviewed tree (`custom.go:294`) |
| `--usage-cmd` probe | `probeUsage`, `internal/runner/usagelimit.go:76` | whitespace-split argv, no shell; runs with gauntlet's cwd, not the reviewed tree; stdout parsed as one finite 0-100 number (`parseUsagePercent`, `usagelimit.go:107`); error or unparseable answer is reported once and ignored |
| `dsh --dump-config` probe | `dshDefaultProvider`, `internal/agent/dsh.go:66` | runs only for a `dsh:<model>` pin; own process group, 120s cap, SIGKILL on the group; provider parsed with a narrow regex (`dshProviderRe`); overlay values charset-restricted before they are quoted into YAML (`dshModelRe`, `agent.go:295`) |
| Planted symlinks/FIFOs in the tree | prompt reads `prompt.go:249-297`, lock creation `runner/lock.go:43`, untracked counting `gitx.go:462-481` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stat after open |

## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and the marker-closing string is
escaped (`compose.go:139`). The agent that receives it has its own permission
system disabled or auto-approved (or, for a custom agent, whatever the
operator put in argv). Applicable classes: elevation of privilege (repo
author -> code execution with user rights), tampering (working tree,
commits), info disclosure (secrets, source exfiltration). The fence is
advisory: the ground rules forbid git, deletion, and persistence, and
prompt-review gets a narrow exception (`compose.go:128`), but nothing
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
  `internal/gitx/worktree.go`, called from `resetForRetry`): more runner-side
  git authority, exercised only inside gauntlet-created worktrees, never the
  user's checkout. Persistent lanes reuse one worktree across reviews; each
  advance or retry still starts from a known commit.
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
  pushes to (`ownsHead` in `internal/ghx/ghx.go:159`): a PR opened from
  someone else's fork under a name a run is about to use is ignored, not
  adopted. Git and `gh` receive fixed argv elements rather than shell text;
  review names, commit subjects, and PR bodies cannot become commands. A PR
  body is assembled from values that originate in the reviewed repository --
  the agent's commit subject, the review prompt's own summary line, the paths
  its commit touched -- so each is stripped of control characters, flattened
  to a single line, and length-bounded before it is rendered, and a path is
  escaped into a code span that a backtick in it cannot close
  (`internal/runner/prbody.go`). Markdown posted to GitHub is display, not
  execution, but a forged heading or an unbounded path list is still a
  reviewer reading something the run did not say. Git itself runs hardened
  against the reviewed repository's own config (see the `.git/config` row).
  Gauntlet's scratch worktrees are refused when `.gauntlet` or
  `.gauntlet/worktrees` is a symlink or not a real directory inside the
  repository (`ensureWorktreeRoot` in `internal/gitx/worktree.go:140`).
- **Sequential in-place with `--commit`/`--push`:** the commit step is itself
  an agent launch. `runCommitStep` (`commit.go:97`) execs one agent with
  `prompt.CommitPrompt` (`compose.go:147`), which instructs it to run
  `git commit`, and with `--push` also `git push`; under `--yolo` a rejected
  push escalates to an agent-run `git pull --rebase` and retry
  (`compose.go:151-155`). The same launch, offered standalone when a dirty
  tree blocks `--jobs`, is gated on explicit consent (`main.go:1121-1152`).
  The prompt is embedded text only (`rules/commit.md`), capped at 5 minutes
  (`commit.go:22-24`), under the same process discipline as any review; but
  this is gauntlet deliberately handing one agent git-write and remote-push
  authority. A compromised agent does not need to talk its way into
  `git push`: with `--push` it is told to.
- **Conflict resolution:** `resolveConflict` (`conflict.go:36`) cuts a
  scratch checkout, replays the branch, and launches one agent with
  `ConflictPrompt` (`compose.go:165`, rules in `rules/conflict.md`) for up to
  10 minutes (`conflict.go:25`). The prompt lists only sanitization-safe
  paths and forbids git; the runner commits and merges the result, or keeps
  the original branch if markers remain. Same advisory fence, same
  permission-bypassed process, narrower file set.
- **`--usage-cmd` (the usage-limit probe):** a command the operator names, run
  between reviews to decide whether to stop starting them. It is split on
  whitespace and handed to exec as argv, so no shell parses it and nothing in
  it is expanded; it runs with gauntlet's own working directory rather than the
  tree under review, so a reviewed repository cannot supply the executable it
  resolves to, and its output is parsed as one finite number in 0-100 and
  nothing else (`probeUsage` and `parseUsagePercent` in
  `internal/runner/usagelimit.go`). The reviewed repository has no channel
  into it. Its failure mode is chosen deliberately: a probe that errors or
  answers unparseably is reported once and then ignored, so the worst a
  broken probe does is leave the limit unenforced -- it cannot end a run
  early, and it cannot extend one, because a missing answer is never read as
  headroom. NaN and infinities are refused (`usagelimit.go:130`).

Repudiation is covered elsewhere: every run journals seed, schedule,
outcomes, and the prompt fingerprint each launch ran under
(`internal/runner/event.go`, DESIGN.md "Run journal"), the
commit step is journaled as its own event with the chosen agent
(`commit.go:159-162`, kind at `event.go:24`), and worktree-mode commits carry
the runner as author. Stacked publication adds a `pull_request` event carrying
the exact head, base, status, and URL; the terminal summary repeats every URL.

**B3.** Terminal escape injection and log spoofing from agent output:
mitigated end to end; the composed prompt shown by `--show-prompt`
(`modes.go:56-57`), the journal replayed by `show` (`runs.go:94`), the
plain reporter (`report.go:84-88`), and the dashboard feed (`ui.go:523-528`)
pass through the same stripping. DoS by output volume: bounded by line cap,
rate limit, and the fixed ring buffer (DESIGN.md "Speed").

**B4.** Tampering with updates in transit: TLS + SHA-256 against
`checksums.txt`, whose entries must parse as real digests
(`selfupdate.go:328-358`). But both come from the same release over the same
channel, so the checksum proves the download matches the listing, not that the
publisher is benign. Compromise of the GitHub repo or its release process
yields arbitrary code execution on every updating machine, amplified by hot
reload re-exec'ing the new binary mid-run without user confirmation
(`internal/selfupdate/reload.go:212`). No signed-release mechanism exists. The
advisory scanner in CI (`vulnscan.yml`) watches the dependency graph, not this
channel. The `bunx @deepseek-ai/dsh` fallback (and the `--dump-config` probe
that uses the same argv) is a second fetch-and-execute path, from the npm
registry rather than GitHub releases, with no checksum.

**B5.** Secret egress: `GH_TOKEN` / `GITHUB_TOKEN` go only to GitHub
(`selfupdate.go:83-97`, attached to the listing, checksums, and asset
requests). Agent CLIs receive the inherited environment and
hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal and handoff files are operator-private (0600 files, 0700
dirs, `journal.go:113,116,191,195`); the lock file refuses symlink takeover
(`lock.go:43-58`). `agents.json` is never resolved into the working directory
when HOME is missing (`custom.go:294`). Threats here require local same-user
access, which already implies game over.

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, `protocol.ext.allow=never`, `attr.tree` empty, local drivers blanked, absolute-only git PATH, `GIT_SSH_COMMAND=ssh` | `gitx.go:25-39,197-259,329-361` |
| Runaway git grandchildren (hooks, merge drivers) holding pipes | process-group SIGKILL on deadline, bounded WaitDelay | `gitx.go:305-317` |
| Planted executables shadowing agents/git/`gh` | cwd-relative PATH entries stripped for agent, git, git-child, and `gh` resolution | `agent.go:161-173`, `gitx.go:82-94,329-361`, `ghx.go:86-97` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps; suggester peeks via `os.OpenRoot` | `prompt.go:249-297`, `gitx.go:462-481`, `runner/lock.go:43-58`, `suggest_fast.go:640-667` |
| Prompt injection blending into containment rules | begin/end markers, end-marker escaping, report-section stripping fails open | `compose.go:31-34,48-70,139` |
| Injection via suggest catalog | description *and* name sanitize, fence-neutralizing, 200-rune cap, strict suggestion grammar checked against known set | `compose.go:174-223,238-261` |
| Injection via `Signals:` into the file-signal suggester | known kinds, charset, 12×40-rune caps; `mark:` values search file heads, not executed | `prompt.go:144-199`, `suggest_fast.go:490,711` |
| Terminal-driven or spoofed output, including prompt preview, journal replay, reporter, and dashboard | `Display`/`Sanitize` strip escapes, controls, bidi; width cap; rate limit; duplicate collapse | `normalize.go`, `modes.go:56-57`, `runs.go:94`, `report.go:84-88`, `ui.go:523-528`, `exec.go:42,46,219-224`, `runner.go:27` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before a terminal write | `gitx.go:673,692`, `runner.go:364`, `lock.go:22-25`, `conflict.go:90-114` |
| Hostile file names forging conflict-prompt instructions | drop unsanitary paths from the named list; markers still block the merge | `conflict.go:90-114` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks, bounded tail buffers (1 MiB suggest tail) | `exec.go:34-42,300-358,438-448` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:36`, `agent.go:409-415,441-446` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, stdin null device, own session (no controlling terminal, so Ctrl-C cannot be disabled from inside an agent), drain grace for stuck grandchildren | `exec.go:25-32,81-290,386-419` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; worktree mode takes commit authority back from the agent entirely | `commit.go:22-24,97-162` |
| Conflict step running away | separate 10-minute cap, same process discipline; unresolved markers keep the branch | `conflict.go:25,36-88` |
| Unbounded downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; digest-shaped entries only; verify-before-rename, atomic replace | `selfupdate.go:107,163,217,297-319,328-358` |
| Half-written binary executed by reload | two identical inode/size/mtime readings required | DESIGN.md "Hot reload", `selfupdate/reload.go:99-107` |
| Concurrent agents corrupting one tree | flock per directory; parallelism only across directories or with worktree isolation + serialized merges; a conflict is resolved in a scratch checkout or keeps its branch | `runner/lock.go`, DESIGN.md concurrency section |
| Reviewed tree defining its own agents | `CustomFilePath` empty without HOME; argv is exec, not shell | `custom.go:223,294` |
| Known-vulnerable dependencies shipping to users | govulncheck weekly and on dependency changes in CI | `.github/workflows/vulnscan.yml` |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential;
   consider signing releases or documenting the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:194`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`agent.go:489-500`). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:272-290`); a
   `dsh:<model>` pin additionally execs the same argv as `--dump-config`
   (`dsh.go:66-91`). Documentation should say plainly that naming `dsh`
   without the launcher installs and runs an npm package, including at
   probe time.
3. **No SECURITY.md.** There is no documented path from "vulnerability
   reported" to "fix shipped": no disclosure contact, no supported-version
   statement. Creating one requires an owner decision, so it is only noted
   here.
4. **Secrets hygiene around spawned agents.** Gauntlet inherits the full
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
- **Exfiltration-by-push.** A crafted prompt convinces an agent to embed
  secrets from outside the tree into an applied fix; with `--commit --push`
  the commit step then hands that agent `git commit` and `git push` directly
  (`commit.go:116`, `compose.go:147`), so poisoned content reaches the
  remote without any runner-side inspection of what is being committed.
  Containment forbids reading outside the tree, by prompt only, and the
  commit prompt lifts the git-read-only rule for its launch by design.
- **Consent-surfaced commit.** Refuse `--jobs` on a dirty tree and gauntlet
  offers to hand that tree, unreviewed, to an agent that commits it; `--yes`
  or `--yolo` on the original command is that consent, so an operator who
  scripts those flags has pre-approved agent-authored commits of whatever
  the reviews left behind (`main.go:1121-1152`).
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:186,238-261`) bound it to reviews that exist
  in the discovered set, including the attacker's own.
- **Signals: steering.** A planted `Signals:` line on a project prompt is
  parsed into the file-signal suggester (`prompt.go:144-199`,
  `matchDeclared` in `suggest_fast.go:490`). Charset, count, and length are
  bounded; a matching tree still lets the attacker add their review to the
  auto-picked set. Same outcome as suggestion gaming, different path, and it
  does not need an agent (`--suggest-agent gauntlet`).
- **Conflict-step write.** After a merge conflict, `resolveConflict`
  (`conflict.go:36`) launches an agent in a scratch checkout with the
  conflicted paths named in the prompt. The agent edits those files; the
  runner commits and merges if markers are gone. Containment is the same
  advisory fence. Unresolved markers keep the branch.

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was re-checked against the code on 2026-09-02 and
  matches the controls it names (O_NOFOLLOW prompt reads, cwd-free PATH
  resolution, forced-empty git config, display sanitization, directory
  flock). It does not list the later git overlays (`attr.tree`, local
  driver blanks, `protocol.ext.allow=never`, `GIT_SSH_COMMAND=ssh`); those
  are additional, not contradictory.
- Response readiness: the journal provides the audit trail
  (o11y-review owns its structure); the reporting path is the gap listed
  above.
- This pass re-verified every file reference above against commit 798fa00
  and folded in what the previous review (9269025) did not yet name: the
  conflict-step agent, `Signals:`/`Summary:` as untrusted prompt metadata,
  `extraSafe` git hardening (`.gitattributes` / local drivers), the
  `dsh --dump-config` probe on the bunx path, `gh` as a second internet
  channel, `CustomFilePath` refusing a missing HOME, the file-signal
  suggester's `OpenRoot` peek, and NaN rejection in the usage probe.
