# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree with their permission systems bypassed.
This document is the systemic view; individual vulnerability findings belong
to sec-review and are recorded here only as threats.

Last reviewed: 2026-08-26 against commit 9c54731. Owner and review cadence are
organizational decisions; none is assigned here.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | Prompt injection from the reviewed tree drives an agent running with bypassed permissions | B1 -> B2 | Accepted by design; containment is advisory text plus process discipline, never an OS sandbox |
| R2 | Self-update integrity rests on TLS and repository ownership; `checksums.txt` authenticates nothing beyond transport consistency, and hot reload execve's the replaced binary automatically | B4 | Named gap |
| R3 | A prompt-injected or compromised agent reads every secret its user can: environment-inherited API keys, agent config stores, SSH keys, `~/.netrc` | B2/B5 | Consequence of R1; containerization is the documented answer (DESIGN.md non-goals) |
| R4 | Confidentiality of reviewed source: agents send code to third-party model APIs over the network | B2 | Inherent to the tool's purpose; users must know it |
| R5 | `dsh` without a launcher on PATH falls back to `bunx`, fetching `@deepseek-ai/dsh` from the npm registry and executing it at run time | B4 | Named gap (deliberate feature, unreviewed supply-chain hop) |
| R6 | DoS via oversized or hostile input | all | Well bounded; caps listed under Mitigations |

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  (DESIGN.md "Isolated parallel reviews", rule 1).
- **Credentials reachable by the user account**: cloud keys, SSH keys,
  `GITHUB_TOKEN`, every agent's own API-key store (`~/.claude`, `~/.gemini`,
  and peers). Agents run with full user privileges.
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
  tree is hostile: prompt files, file names, `.git/config`. Mitigations are
  listed per entry point below.
- **B2, gauntlet <-> spawned agents.** The privilege transition of the whole
  system: gauntlet execs agents with permission bypass flags
  (`claude --dangerously-skip-permissions`, `codex exec
  --dangerously-bypass-approvals-and-sandbox`, `grok --permission-mode
  bypassPermissions`; `internal/agent/agent.go:367-391`). With `--push`, one
  launch per loop is additionally granted git commit and remote-push
  authority (`runCommitStep`, `internal/runner/runner.go:1040`; prompt in
  `internal/prompt/rules/commit.md`, composed at `compose.go:147`). The agent then acts
  with the user's authority inside the reviewed tree, with network access.
  Containment is prompt-level (embedded rules in `internal/prompt/rules/`,
  composed in `internal/prompt/compose.go`) plus process discipline (own
  process group, no stdin, hard timeout with SIGKILL escalation;
  `internal/runner/exec.go:83-258`). There is deliberately no OS sandbox
  (DESIGN.md non-goals). Anything crossing B1 that reaches the prompt crosses
  into B2 with this advisory fence as the only gate.
- **B3, agents <-> user terminal, dashboard, journal.** Agent output is
  untrusted display input; sanitization before any terminal write.
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward api.github.com (only when
  `GITHUB_TOKEN` is set) and toward each agent's model provider.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, `agents.json`.

## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | tree walk, `internal/prompt/discover.go:123`; read `prompt.go:141` (`Body` at `prompt.go:54`) | regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap (`prompt.go:32`); project prompts override bundled ones of the same name (`discover.go:103-110`) |
| Prompt names (file stems) | `discover.go:67,87` | NFC-normalized keys (`prompt.go:175`); control/Cf characters reject the file with a warning (`discover.go:68,88`; strip at `prompt.go:183`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:73`, fed to the suggest catalog `compose.go:181` | sanitized; `</catalog>` and `RELEVANT:` neutralized (`compose.go:188-195`); 200-char cap on a rune boundary (`compose.go:162,196-204`) |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR` | `cmd/gauntlet/flags.go:264`; parsed `agent.go:254,502`, `custom.go:209`, `discover.go:51-77` | allow-listed tool names; dsh model charset-restricted (`agent.go:250,295`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`agent.go:502-520`); `--prompt-dir` takes regular files only, control-char names rejected |
| Environment: `PATH` | `agent.go:136`, `gitx.go:36` | cwd-relative and relative entries dropped for agent and git resolution |
| Environment: `GAUNTLET_HOME`, `GITHUB_TOKEN`, `TERM`/NO_COLOR, `GAUNTLET_STATE` | `gauntlethome.go:30`, `selfupdate.go:74`, `cmd/gauntlet/report.go:50`, `selfupdate/reload.go:18` | operator-controlled, same-user trust; `GAUNTLET_HOME` made absolute at resolution so the state root cannot depend on the current directory (`custom.go:273`) |
| Agent stdout/stderr | pipes in `exec.go:97-106`; line scan `exec.go:273` | 4 MiB per line emitted in chunks (`exec.go:41`), escape/control/bidi strip before terminal (`normalize.go:332 Display`), width cap 2000 cols (`exec.go:45`), rate limit 200 lines/s (`runner.go:31`) |
| Stream-JSON events from agents | `streamjson.Parse` at `exec.go:182` | envelope-agnostic parser; thinking lines sanitized + capped (`exec.go:333`) |
| Token counters parsed from output and transcripts | `exec.go:153-158,247-256`; transcript watch off with `-tags notoktop`, `usage_toktop.go:17`; custom roots `custom.go:63` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:64` | HTTPS, 4 MiB decode cap |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:122-177` | checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap, SHA-256 must match before rename into place |
| Git outputs (shortstat, porcelain, check-ignore) | `gitx.go:133,297-347,356` | regex/line parsing; C-quoted paths decoded (`gitx.go:398`) then display-sanitized before any message (`runner.go:296`); counts only, never executed |
| Reviewed repo's `.git/config` | every git call, `gitx.go:29-34,103-125` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external` forced empty; git resolved on absolute-only PATH so a planted `./git` cannot run |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:87` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized before it reaches a terminal (`lock.go:21-24,77,94`) |
| Helper-tool inventory appended to prompts | PATH probe at startup, `runner.go:254`, rendered `compose.go:90-115` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| `~/.gauntlet/agents.json` | `custom.LoadCustomFile`, `custom.go:232` | malformed file refuses startup rather than silently changing the agent set; unknown JSON keys are errors (`custom.go:255`) |
| Planted symlinks/FIFOs in the tree | prompt reads `prompt.go:141`, lock creation `runner/lock.go:42`, untracked counting `gitx.go:223` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stat after open |

## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and the marker-closing string is
escaped (`compose.go:139`). The agent that receives it has its own permission
system disabled. Applicable classes: elevation of privilege (repo author ->
code execution with user rights), tampering (working tree, commits), info
disclosure (secrets, source exfiltration). The fence is advisory: the ground
rules forbid git, deletion, and persistence, and prompt-review gets a narrow
exception (`compose.go:128-135`), but nothing technical stops a sufficiently
persuasive prompt from talking an agent out of them. This is the accepted
core risk; DESIGN.md answers it operationally: run untrusted repos in a
container.

**B2 (agent misbehavior directly).** Spoofing: fabricated agent output cannot
drive the terminal (B3 mitigations) but fabricated *content* flows into
commits, so attribution of prose is by review name, not by verified origin.
Who authors the commits depends on the mode, and the difference is a real
privilege transition:

- **`--jobs > 1` (worktree isolation):** the runner stages and commits each
  worktree itself (`internal/runner/runner.go:600`); agents stay forbidden to
  run git (DESIGN.md rule 3).
- **Sequential in-place with `--commit`/`--push`:** the commit step is itself
  an agent launch. `runCommitStep` (`runner.go:1040`) execs one agent with
  `prompt.CommitPrompt` (`compose.go:147`), which instructs it to run
  `git commit`, and with `--push` also `git push`; under `--yolo` a rejected
  push escalates to an agent-run `git pull --rebase` and retry
  (`compose.go:151-155`). The prompt is embedded text only
  (`rules/commit.md`), capped at 5 minutes (`runner.go:26`), under the same
  process discipline as any review; but this is gauntlet deliberately handing
  one permission-bypassed agent git-write and remote-push authority. A
  compromised agent does not need to talk its way into `git push`: with
  `--push` it is told to.

Repudiation is covered elsewhere: every run journals seed, schedule,
and outcomes (`internal/runner/event.go`, DESIGN.md "Run journal"), the
commit step is journaled as its own event with the chosen agent
(`runner.go:1103`), and worktree-mode commits carry the runner as author.

**B3.** Terminal escape injection and log spoofing from agent output:
mitigated end to end; even `--raw` mode passes `Display` (`exec.go:197-204`).
DoS by output volume: bounded by line cap, rate limit, and the fixed ring
buffer (DESIGN.md "Speed").

**B4.** Tampering with updates in transit: TLS + SHA-256 against
`checksums.txt`. But both come from the same release over the same channel,
so the checksum proves the download matches the listing, not that the
publisher is benign. Compromise of the GitHub repo or its release process
yields arbitrary code execution on every updating machine, amplified by hot
reload re-exec'ing the new binary mid-run without user confirmation
(`internal/selfupdate/reload.go`). No signed-release mechanism exists.

**B5.** Secret egress: `GITHUB_TOKEN` goes only to `api.github.com`
(`selfupdate.go:74-76`). Agent CLIs receive the inherited environment and
hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal and handoff files are operator-private (0600 files, 0700
dirs, `journal.go:111,114,182,185`); the lock file refuses symlink takeover
(`lock.go:42-56`). Threats here require local same-user access, which already
implies game over.

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, absolute-only git PATH | `gitx.go:29-48` |
| Planted executables shadowing agents/git | cwd-relative PATH entries stripped for agent resolution too | `agent.go:136-148`, `gitx.go:36-48` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps | `prompt.go:141-166`, `gitx.go:223-231`, `runner/lock.go:42-56` |
| Prompt injection blending into containment rules | begin/end markers, end-marker escaping, report-section stripping fails open | `compose.go:31-34,48-70,139` |
| Injection via suggest catalog | description sanitize, fence-neutralizing, length cap, strict suggestion grammar checked against known set | `compose.go:162-207`, `compose.go:219-248` |
| Terminal-driven or spoofed output | `Display`/`Sanitize` strip escapes, controls, bidi; width cap; rate limit; duplicate collapse | `normalize.go`, `exec.go:41,45`, `runner.go:31` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before display | `gitx.go:392-454`, `runner.go:296-302`, `lock.go:21-24` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks, bounded tail buffers (1 MiB suggest tail) | `exec.go:33-41,268-326`, `exec.go:406-434` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:32`, `agent.go:321-359` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, stdin `/dev/null`, drain grace for stuck grandchildren | `exec.go:24-31,83-258,354-372` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; worktree mode takes commit authority back from the agent entirely | `runner.go:26,993-1103` |
| Unbounded downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; verify-before-rename, atomic replace | `selfupdate.go:38,86,140,196-218` |
| Half-written binary executed by reload | two identical inode/size/mtime readings required | DESIGN.md "Hot reload", `selfupdate/reload.go:91-107` |
| Concurrent agents corrupting one tree | flock per directory; parallelism only across directories or with worktree isolation + serialized merges; conflicting merge keeps its branch | `runner/lock.go`, DESIGN.md concurrency section |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential;
   consider signing releases or documenting the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:134-156`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`agent.go:398-409`). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:230-246`); documentation
   should say plainly that naming `dsh` without the launcher installs and
   runs an npm package.
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
  (`discover.go:103-110`). The planted body rides past the markers into an
  agent told to skip permissions. Enabling path: `Discover` -> `Compose` ->
  `BuildCmd` -> `runProc`.
- **Exfiltration-by-push.** A crafted prompt convinces an agent to embed
  secrets from outside the tree into an applied fix; with `--commit --push`
  the commit step then hands that agent `git commit` and `git push` directly
  (`runner.go:1059`, `compose.go:147`), so poisoned content reaches the
  remote without any runner-side inspection of what is being committed.
  Containment forbids reading outside the tree, by prompt only, and the
  commit prompt lifts the git-read-only rule for its launch by design.
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:174,234-240`) bound it to reviews that exist in
  the discovered set, including the attacker's own.

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was re-checked against the code on 2026-08-26 and
  matches (O_NOFOLLOW prompt reads, cwd-free PATH resolution, forced-empty
  git config, display sanitization, directory flock).
- Response readiness: the journal provides the audit trail
  (o11y-review owns its structure); the reporting path is the gap listed
  above.
- This pass re-verified every file reference above against commit 9c54731
  and corrected one false mitigation claim (commit authorship by mode) and
  the stale references left by the 30c98ca..9c54731 review merges.
