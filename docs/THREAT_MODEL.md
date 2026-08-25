# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree with their permission systems bypassed.
This document is the systemic view; individual vulnerability findings belong
to sec-review and are recorded here only as threats.

Last reviewed: 2026-08-25 against commit 30c98ca. Owner and review cadence are
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
  place; the runner commits (`internal/runner`). Corruption here destroys
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
  (`internal/journal/journal.go`) and runner-authored commits.

## Trust boundaries

- **B1, reviewed repo <-> gauntlet process.** Everything read from the target
  tree is hostile: prompt files, file names, `.git/config`. Mitigations are
  listed per entry point below.
- **B2, gauntlet <-> spawned agents.** The privilege transition of the whole
  system: gauntlet execs agents with permission bypass flags
  (`claude --dangerously-skip-permissions`, `codex exec
  --dangerously-bypass-approvals-and-sandbox`, `grok --permission-mode
  bypassPermissions`; `internal/agent/agent.go:361-456`). The agent then acts
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
| Project `*-review.md` files | tree walk, `internal/prompt/discover.go:123`; read `internal/prompt/prompt.go:128` | regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap (`prompt.go:30`); project prompts override bundled ones of the same name (`discover.go:103-110`) |
| Prompt names (file stems) | `discover.go:68,88` | control/Cf characters rejected or stripped (`prompt.go:159`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:62`, fed to the suggest catalog `compose.go:136` | sanitized; `</catalog>` and `RELEVANT:` neutralized (`compose.go:143-148`); 200-char cap |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV` | `cmd/gauntlet/flags.go`; parsed `agent.go:252,490`, `custom.go:205` | allow-listed tool names; dsh model charset-restricted (`agent.go:248`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`agent.go:511-513`) |
| Environment: `PATH` | `agent.go:134`, `gitx.go:36` | cwd-relative and relative entries dropped for agent and git resolution |
| Environment: `GAUNTLET_HOME`, `GITHUB_TOKEN`, `TERM`/NO_COLOR, `GAUNTLET_STATE` | `gauntlethome.Dir`, `gauntlethome.go:28`, `selfupdate.go:74`, `cmd/gauntlet/report.go:50`, `selfupdate/reload.go:130` | operator-controlled, same-user trust; `GAUNTLET_HOME` made absolute at resolution so the state root cannot depend on the current directory |
| Agent stdout/stderr | pipes in `exec.go:97-106`; line scan `exec.go:273` | 4 MiB per line (`exec.go:41`), escape/control/bidi strip before terminal (`normalize.go:332 Display`), width cap 2000 cols (`exec.go:45`), rate limit 200 lines/s (`runner.go:31`) |
| Stream-JSON events from agents | `streamjson.Parse` at `exec.go:182` | envelope-agnostic parser; thinking lines sanitized + capped (`exec.go:333`) |
| Token counters parsed from output and transcripts | `exec.go:153-158,247-256`; transcript watch off with `-tags notoktop`, `usage_toktop.go:17`; custom roots `custom.go:61` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:64` | HTTPS, 4 MiB decode cap |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:122-177` | checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap, SHA-256 must match before rename into place |
| Git outputs (shortstat, porcelain, check-ignore) | `gitx.go:133,286,315` | regex/line parsing; counts only, never executed |
| Reviewed repo's `.git/config` | every git call, `gitx.go:29-34,103-125` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external` forced empty; git resolved on absolute-only PATH so a planted `./git` cannot run |
| `~/.gauntlet/agents.json` | `custom.LoadCustomFile`, `custom.go:225` | malformed file refuses startup rather than silently changing the agent set |
| Planted symlinks/FIFOs in the tree | prompt reads `prompt.go:128`, lock creation `runner/lock.go:31`, untracked counting `gitx.go:223` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stat after open |

## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and the marker-closing string is
escaped (`compose.go:97`). The agent that receives it has its own permission
system disabled. Applicable classes: elevation of privilege (repo author ->
code execution with user rights), tampering (working tree, commits), info
disclosure (secrets, source exfiltration). The fence is advisory: the ground
rules forbid git, deletion, and persistence, and prompt-review gets a narrow
exception (`compose.go:88-95`), but nothing technical stops a sufficiently
persuasive prompt from talking an agent out of them. This is the accepted
core risk; DESIGN.md answers it operationally: run untrusted repos in a
container.

**B2 (agent misbehavior directly).** Spoofing: fabricated agent output cannot
drive the terminal (B3 mitigations) but fabricated *content* flows into
commits the runner authors, so attribution of prose is by review name, not by
verified origin. Repudiation is covered: every run journals seed, schedule,
and outcomes (`internal/runner/event.go`, DESIGN.md "Run journal"), and
commits are authored by the runner, not the agent.

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
dirs, `journal.go:105,108,163,167`); the lock file refuses symlink takeover
(`lock.go:31`). Threats here require local same-user access, which already
implies game over.

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, absolute-only git PATH | `gitx.go:29-48` |
| Planted executables shadowing agents/git | cwd-relative PATH entries stripped for agent resolution too | `agent.go:134-146`, `gitx.go:36-48` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps | `prompt.go:118-153`, `gitx.go:223-231`, `runner/lock.go:31` |
| Prompt injection blending into containment rules | begin/end markers, end-marker escaping, report-section stripping fails open | `compose.go:30-101` |
| Injection via suggest catalog | description sanitize, fence-neutralizing, length cap, strict suggestion grammar checked against known set | `compose.go:119-198` |
| Terminal-driven or spoofed output | `Display`/`Sanitize` strip escapes, controls, bidi; width cap; rate limit; duplicate collapse | `normalize.go`, `exec.go:41,45`, `runner.go:31` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks, bounded tail buffers | `exec.go:33-41,268-326`, `exec.go:406-410` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:30`, `agent.go:320-357` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, stdin `/dev/null`, drain grace for stuck grandchildren | `exec.go:24-31,83-258,354-372` |
| Unbounded downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; verify-before-rename, atomic replace | `selfupdate.go:38,86,140,196-218` |
| Half-written binary executed by reload | two identical inode/size/mtime readings required | DESIGN.md "Hot reload", `selfupdate/reload.go` |
| Concurrent agents corrupting one tree | flock per directory; parallelism only across directories or with worktree isolation + serialized merges; conflicting merge keeps its branch | `runner/lock.go`, DESIGN.md concurrency section |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential;
   consider signing releases or documenting the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:94-121`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`agent.go:396-407`). Auto-detection already ignores it; documentation
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
- **Exfiltration-by-fix.** A crafted prompt convinces an agent to embed
  secrets from outside the tree into an applied fix; the runner then commits
  and optionally pushes it (`--commit --push`). Containment forbids reading
  outside the tree, by prompt only.
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:172-198`) bound it to reviews that exist in
  the discovered set, including the attacker's own.

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was checked line by line against
  the code above and matches.
- Response readiness: the journal provides the audit trail
  (o11y-review owns its structure); the reporting path is the gap listed
  above.
