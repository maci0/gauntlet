# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree, typically with their permission systems
bypassed or auto-approved. This document is the systemic view; individual
vulnerability findings belong to sec-review and are recorded here only as
threats.

Last reviewed: 2026-11-12 against commit ec43dd3.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | A prompt from the reviewed tree drives an agent whose permissions are bypassed or auto-approved | B1 -> B2 | Open for reads, network, writable grants, and `--no-sandbox`. Writes are confined by default (Landlock, Seatbelt). See B2 |
| R2 | Update and rollback install bytes the publisher, or a writer of the install directory, supplied. `checksums.txt`, `sbom.json`, and the provenance attestation do not authenticate the automatic path | B4 | Open. See B4 and gap 1 |
| R3 | That agent can read every secret its user can | B2/B5 | Open. Write confinement does not restrict reads. See gap 5 |
| R4 | Reviewed source leaves the machine through each agent's model API | B2 | Accepted. Inherent to the tool |
| R5 | Naming `dsh` with no launcher fetches and runs one pinned npm package, and a `dsh:<model>` pin probes it with `--dump-config` | B4 | Open for the publisher. The version is pinned (`DshNpmPackage`, `internal/agent/dsh.go:23-29`). See gap 2 |
| R6 | A hostile review, or a usage probe that fails open, spends host capacity or provider budget | B2 | Open. `--token-budget` schedules the next review. It is not a spend quota. See B2 and gap 4 |
| R7 | `--log` keeps source and any secret the redactor does not recognize, at an operator-chosen path | B3/B6 | Open when logging is on. The destination refuses a symlink, and known secret shapes are rewritten. See B5 and B6 |
| R8 | `--semcode` output from a hostile tree reaches the terminal | B1 -> B3 | Closed. Both indexer streams pass through `normalize.DisplayWriter` (`cmd/gauntlet/semcode.go:82-104`) |
| R9 | `--keep-runs` (default 200) drops journals by the run id's start instant, not by a protected property of the journal | B6 | Open. A journal still open is kept. A closed run is kept only when its start instant is inside the window. See B6, destruction |

The order reflects reachability and blast radius, not measured likelihood.
R1/R3/R4 require only content reaching a launched agent; R2 requires control
of the update publisher or executable path; R5 requires selecting the fallback.
No server authentication boundary is claimed here: the operator's OS account
supplies child-process authority (`internal/runner/exec.go:217-221`), and
publication uses that account's Git credentials (`internal/runner/commit.go:105-113`).

The one network-facing surface this project has is the published site, and it
carries no risk row because it has no application logic to attack (B7). What
sits behind it is a hosting account outside this repository, which is a fact
about the deploy rather than about the code, so it is recorded at that
boundary and deliberately not ranked among these rows.

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  ([DESIGN.md](DESIGN.md#isolated-parallel-reviews---jobs-n), rule 1), and why the runner rewinds
  only its own worktrees to a base commit between retry attempts
  (`git reset --hard` + `git clean -fd`, `ResetToBase` in
  `internal/gitx/worktree.go:650-670`). In-place retries restore a snapshot of the
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
  reload re-executes it mid-run (`internal/selfupdate`). Its predecessor is an
  asset too, not just a spare: an update leaves it beside the installed binary
  as `<binary>.previous` and the documented rollback renames it back into
  execution, so whoever can write that name controls the next install after a
  rollback (`internal/selfupdate/selfupdate.go:329-380`,
  [docs/CLI.md](CLI.md#updating)).
- **Host availability**: reviews run unbounded CPU/network inside the timeout
  window (`internal/runner/exec.go`).
- **Audit trail**: the journal under `~/.gauntlet`
  (`internal/journal/journal.go`) and the commits each run leaves behind.
  Optional `--log` files also retain displayed output, potentially including
  source and credentials quoted by an agent (`openLogFile`, `cmd/gauntlet/main.go:908-940`).

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
  with no extra bypass flag in argv. `microagent` has no approval system at
  all: it runs its six tools directly, so there is no flag to add and nothing
  to bypass. Custom agents (`--agent-cmd`,
  `~/.gauntlet/agents.json`, the pi family in `custom.go`) use operator-defined
  argv: gauntlet does not add a bypass flag. With `--commit` (also implied by
  `--push`), a commit-agent launch follows each sequential review or runs
  after parallel lanes drain. The runner verifies tracked-file cleanliness,
  strips attribution trailers, and performs
  the requested push (`internal/runner/commit.go:163-249`). This divides
  workflow responsibility, not OS authority: the agent still inherits the
  user's credentials and can disobey the prompt's no-push instruction. The same
  hand-off exists outside a loop: when `--jobs` refuses a dirty tree,
  `commitFirst` offers to give the uncommitted work to one agent, consented
  by `--yes`/`--yolo` or an interactive confirmation, never on an unattended
  guess (`cmd/gauntlet/main.go:975-999`, executed by `runner.CommitNow`,
  `internal/runner/commit.go:56`). A third launch, `resolveConflict`
  (`internal/runner/conflict.go:37`), hands a permission-bypassed agent a
  scratch checkout of a conflicted merge; the runner commits and merges only
  if conflict markers are gone. The agent then acts with the user's
  authority inside the reviewed tree, with network access. Containment is
  prompt-level (embedded rules in `internal/prompt/rules/`, composed in
  `internal/prompt/compose.go`) plus process discipline (own session -- which
  detaches the controlling terminal, so an agent cannot open /dev/tty and put
  the operator's terminal into a raw mode where Ctrl-C stops generating
  SIGINT; the kernel's SIGTTOU guard does not cover a runtime that ignores
  SIGTTOU, which Node-style CLIs routinely do -- stdin is the null device
  except for ACP, where it is a pipe carrying only the runner's protocol
  messages (`internal/runner/exec.go:174-186,217-239`),
  child environment isolated to absolute-only PATH via `runx.AbsPATHEnv()`,
  hard timeout with SIGKILL escalation, deferred process-group SIGKILL on
  normal exit to reap orphaned grandchild processes, and SIGKILL escalation on
  drain timeout to unblock readers when orphaned processes hold open stdout/stderr
  pipes (`internal/runner/exec.go:217-221,271,548-613,765-786`). There is
  default kernel filesystem write confinement in the shared launcher
  (`internal/runner/sandbox.go`): Landlock on Linux, Seatbelt on macOS.
  Setup errors stop the launch, and `--no-sandbox` explicitly opts out.
  Whether either mechanism exists on this host is now reported before the run
  rather than by the launch that failed: `SandboxSupport` probes the kernel for
  the Landlock ABI it answers with and stats the Seatbelt launcher for presence
  and an execute bit (`internal/runner/sandbox.go:59`,
  `internal/runner/sandbox_linux.go:109-120`, `internal/runner/sandbox_darwin.go:32-42`,
  printed at `cmd/gauntlet/doctor.go:165-178`), because those hosts failed every
  default run with `landlock unavailable`, naming neither the kernel requirement
  nor the flag that is the way past it. A grant widens the writable set by a
  whole subtree and is checked only for existing at parse time
  (`resolveSandboxWrites`, `cmd/gauntlet/flags.go:1217-1252`), so the flag's
  documented promise is a usage check and not a least-privilege one: the
  ceiling is named here so a reader does not infer a narrowing the code does
  not do. Two things do bound it, both at launch rather than at the flag. A
  relative grant is resolved against each worktree where the worktree is
  known, so it cannot name one fixed tree for a run that cuts several, and
  every grant is `EvalSymlinks`-resolved and refused unless the result is a
  directory, so a symlink planted at the grant path after the parse-time check
  cannot widen the policy (`sandboxRoots`,
  `internal/runner/sandbox.go:82-185`). What survives that is still a
  whole-subtree grant, and the two temp roots are classified optional: when
  they do not resolve the grant is dropped and the run continues rather than
  the launch being refused (`internal/runner/sandbox.go:105-110,128-136,166-185`).
  Reads and network remain allowed; the advisory fence still governs actions
  inside writable roots and operations outside filesystem write control.
- **B3, agents <-> user terminal, dashboard, journal.** Agent output is
  untrusted display input; sanitization before any terminal write, including
  the two inspection paths (`--show-prompt`, `cmd/gauntlet/modes.go:65-66`;
  `show`'s journal replay, `cmd/gauntlet/runs.go:637,654`, which writes through a
  `bufio.Writer` to the caller's stdout and spawns nothing, so there is no
  pager environment to isolate) and the dashboard feed
  (`internal/ui/ui.go:780-808`).
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload. The
  publishing side of the same channel is GitHub Actions: `release.yml` builds
  and uploads the assets `update` verifies (the write token is in the
  environment only for that upload), and `vulnscan.yml` runs
  govulncheck weekly and on `go.mod`/`go.sum` pull-request changes and main
  pushes (`.github/workflows/vulnscan.yml:11-23`). Actions are commit-pinned,
  every runner image is named rather than `-latest` (`ubuntu-24.04` and
  `macos-15`, the release job on the former alone), and checkout disables
  persisted credentials.
  The scanner is version-pinned through `GOVULNCHECK_VERSION` in `Makefile:75`,
  invoked by `make vuln` (`.github/workflows/vulnscan.yml:39-64`).
  The module cache those jobs restore is keyed on `go.sum`
  (`cache-dependency-path`, `.github/workflows/ci.yml:69-72`, the same pin in
  `.github/workflows/release.yml:63-66` and
  `.github/workflows/vulnscan.yml:58-61`). The cache still carries only
  downloads a previous run verified against `go.sum`, and `-mod=readonly`
  still refuses a graph the manifests do not describe.
  Every job in every workflow clears `GITHUB_TOKEN` and `GH_TOKEN`
  (`.github/workflows/ci.yml:49-51` for the pull-request `test` job, and
  `.github/workflows/vulnscan.yml:45-47` for the scanner). The release job
  uses the same pattern and puts the write token back only in Publish.
  The CHANGELOG extraction sets `set -eu -o pipefail`
  (`.github/workflows/release.yml:77-79`), so a failed extraction fails the
  release. The tag guard refuses a tag `origin/main` does not carry
  (`.github/workflows/release.yml:117-157`) before anything is built. Publish
  sets `set -eu -o pipefail` (`.github/workflows/release.yml:225-227`), so a
  failed upload cannot fall through and clear the draft.
  Release and
  checksum downloads enforce `validateAssetURL` across HTTP redirects and cap
  redirects at 10 (`client.CheckRedirect`, `internal/selfupdate/selfupdate.go:150-161`).
  Release checksum verification uses constant-time comparison
  (`subtle.ConstantTimeCompare`, `internal/selfupdate/selfupdate.go:286`), and the
  downloaded binary is flushed with `Sync()` before atomic replacement (`selfupdate.go:289`).
  Every release now also ships `dist/sbom.json`, the CycloneDX inventory of the
  modules the built binaries link (`internal/sbom/sbom.go`, written by the
  release target at `Makefile:774`). `make release` refuses the `dev` default
  `VERSION` carries (`release-version`, `Makefile:930-936`), so a rehearsal
  cannot publish assets at a version no tag names. The inventory is uploaded
  beside the binaries,
  `.github/workflows/release.yml:240-248`). It travels this boundary and
  `update` does not read it: the artifact is for a scanner and for whoever
  reads a release page. Nothing authenticates it beyond the `checksums.txt`
  the binaries travel under, and it is generated from those binaries' own
  build info, so whoever can replace a binary can replace the inventory that
  describes it. It narrows what a reader has to guess about a release's
  dependency surface; it is not a second integrity control.
  The release ships the license text beside them as `dist/LICENSE`
  (`Makefile:799`, uploaded at `.github/workflows/release.yml:240-248`),
  because a binary offered under the AGPL carries the terms with it and a
  consumer who installs the binary alone has nowhere else to read them. It is
  a copy of the repository's own `LICENSE` and carries no claim of its own: it
  is outside `dist/checksums.txt` and outside the attestations, which cover
  the binaries, so replacing it changes no claim anything verifies.
  The binaries themselves carry one more record the inventory does not: the
  release workflow signs a build-provenance attestation per entry in
  `dist/checksums.txt` (`.github/workflows/release.yml:205-208`), stating the
  workflow, the tag, and the commit that produced the bytes, verifiable with
  `gh attestation verify`. The claim it makes is about provenance, not
  integrity of transport, and `update` does not check it, so the gap recorded
  as R2 stands for the automatic path.
  The install side of this boundary writes one more file. Before the rename
  that installs a release, the binary being replaced is copied to
  `<binary>.previous` in the same directory, at its own mode, through a
  temp file and a directory sync, and an update that cannot write that copy is
  refused rather than performed without a way back
  (`keepPrevious`, `internal/selfupdate/selfupdate.go:328-373`,
  `selfupdate.go:296`; `make install` keeps the same copy, `Makefile:637`).
  A release whose bytes already match the installed binary installs nothing
  (`fileSum` against the verified download, `applyTo`): a repeat of an update
  would otherwise put the release it just installed in the copy and leave the
  rollback naming the same version the rollback replaces. `make install` skips
  the copy the same way, with `cmp` on the built binary.
  The copy inherits the trust of the binary it replaced; a locally installed
  build need not have come through the download verifier. The
  [rollback recipe](CLI.md#updating) copies the current binary aside before
  moving the previous copy into place. A failed copy aborts the replacement,
  but nothing re-verifies the bytes under `<binary>.previous`. It also restores
  the version before the last update, undoing any fixes introduced since then.
  Stacked publication is a second internet path: the `gh` CLI, resolved from an
  absolute-only PATH (`internal/ghx/ghx.go:95-98, 258-267`), using the operator's own
  `gh` credentials, not the `GH_TOKEN` gauntlet reads for self-update.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward GitHub (only when `GH_TOKEN` or
  `GITHUB_TOKEN` is set, and only on gauntlet's own HTTPS client) and toward
  each agent's model provider. Git stderr is redacted for embedded userinfo
  credentials and for a credential-shaped value before errors are printed or
  journaled (`internal/gitx/exec.go:327-395`; `runx.RedactUserinfo`,
  `runx.RedactSecrets`, `internal/runx/runx.go:206-256`, reached through
  `FirstLine`, which every launch's, git's, `gh`'s, and probe's error text
  funnels through). Authorization headers are also recognized
  (`secretAuthRe`, `internal/runx/runx.go:192-196`). Agent output passes through
  the same secret redactor before publication (`outputSink`,
  `internal/runner/attempt.go:770-780`), protecting the terminal, dashboard
  scrollback, and `--log`. The journal excludes output and usage events
  (`cmd/gauntlet/main.go:525-529`). Unlabelled secrets, unknown token shapes,
  and quoted source remain exposed; redaction is not a confidentiality boundary.
  `gh` and `git push` use whatever credentials those tools already have.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, crash checkpoints, `agents.json`. This is also the one
  boundary where the run deletes: `--keep-runs` prunes the history at the end
  of every run (`internal/journal/retain.go:45-56,78-198`). Journal paths are
  guarded by run ID validation (`ValidRunID`, `internal/journal/history.go:88-104`)
  and date-sharded from run ID timestamps (`shardFromRunID`, `internal/journal/history.go:169-182`).
  The state tree outlives the run and is the file a backup, a sync, or a
  `runs --json` consumer takes away, so the free text written into it names no
  OS account: the event's text and the index's argv have the home directory
  shortened to `~` on the way to disk and only there (`journaledEvent`,
  `cmd/gauntlet/main.go:1075-1086`, `journaledArgs`, `main.go:1088-1111`,
  `RedactHome`, `internal/normalize/display.go:108-143`).
  The crash checkpoint is the exception and the second file here whose argv
  gets executed: `state/checkpoints/<run-id>.json` holds the run's raw argv and
  working directory, because `gauntlet resume` changes to that directory and
  execs that command line (`writeCheckpoints`, `cmd/gauntlet/main.go:860-906`;
  `cmdResumeAt`, `cmd/gauntlet/checkpoint.go:174-224`). It gets the handoff's
  controls: a run-id-validated name, the working-directory fallback refused
  (`checkpointDir`, `checkpoint.go:54-60`), a 0700 directory and an atomic,
  synced write (`saveCheckpoint`, `checkpoint.go:75-95`), and a read that
  opens through `safefile.OpenRead`, so `O_NOFOLLOW` is on the open syscall
  rather than on an `os.Lstat` a reviewed tree can race by swapping the name
  between the check and the read, refuses a non-regular descriptor in its
  place, and then requires a record under 16 MiB whose recorded run id matches
  its name (`readCheckpoint`, `checkpoint.go:105-137`). Anyone who can write the state
  root can already plant a handoff or `agents.json`, so it adds no new writer;
  it does make the planted argv run on an explicit `resume` rather than only
  inside a reload. A resume refuses a directory whose lock is held
  (`busyDir`, `checkpoint.go:139-168`), so it cannot put a second set of agents
  in a tree a live run owns. The file lives until the run ends on its own.
  Optional `--log` crosses into a separate operator-selected output path, not
  necessarily that state directory (`openLogFile`, `cmd/gauntlet/main.go:908-940`), validated to
  not name an existing directory (`validateLog`, `cmd/gauntlet/flags.go:1081-1118`).

- **B7, the internet <-> the published site.** The one boundary in this
  document whose far side is not the operator. `gauntlet.rocks` serves
  `site/public` through Cloudflare Workers Static Assets, bound by
  `site/wrangler.jsonc:1-7`. Nothing in this repository handles a request:
  the assets are static files with no query parameter, no form, and no
  script, so the classes that dominate everywhere else in this document
  (injection, path traversal, deserialization, resource exhaustion against a
  parser) do not arise here. What crosses the boundary is a request and an
  asset, and what stands between them is the header block
  (`site/public/_headers:6-11`) plus an explicit cache policy per path
  (`site/public/_headers:13-33`) plus the hosting account, which is outside
  this tree. The boundary is named because a security owner asking what this
  project exposes to the internet should find the answer here rather than by
  reading the deploy: the domain, the absence of any application logic on it,
  and the fact that the credential able to publish over the name lives in a
  Cloudflare account this repository does not hold. Compromise of that
  account, or of a maintainer's session into it, stands outside the tree the
  same way B4's release publisher does, and nothing in CI gates what gets
  published: no workflow or Makefile target runs `wrangler`, so a deploy is a
  manual action whose result no test in this repository inspects. The
  screenshot the site carries (`site/public/dashboard.png`) is a captured
  frame of a run's dashboard and so can show whatever that run displayed; it
  is regenerated by `scripts/shots.sh` and reviewed as an ordinary image
  change, which is a human check rather than a control.
## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | git `ls-files` glob, walk fallback, `internal/prompt/discover.go`; read `prompt.go` via `readNoFollow` (`prompt.go:278-329`) | prompt discovery candidates inspected with `os.Lstat` requiring regular files (`discover.go:244,278`); regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap; skipDirs and hidden directories dropped; git-ignored files refused; project prompts override bundled ones of the same name; duplicate detection reads bounded |
| Prompt names (file stems) | `discover.go:68,88` | NFC-normalized keys (`prompt.go:325-327`); control/Cf characters reject the file with a warning (`discover.go:70,90`; strip at `prompt.go:331-333`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:88-108`, fed to the suggest catalog `compose.go:384-411` | the prefix is matched on the line as written, before sanitize repairs it, so a line the file never wrote as a goal cannot supply a description; the extracted value is sanitized and NFC-normalized; name and description both fenced: `</catalog>` and `RELEVANT:` neutralized; 200-rune cap on a rune boundary (`compose.go:384-411`) |
| Prompt `Summary:` line | `prompt.go:153-171`, fed to stacked PR bodies `internal/runner/prbody.go` | sanitized, cut to 60 runes at read, and normalized to NFC before truncation (`prompt.go:159-170`), the goal-line fallback bounded the same way; PR rendering strips controls, flattens to one line, NFC-normalizes to prevent rune splitting, and bounds again (`prbody.go:26-50,88,150-178; stack.go:655`) |
| Prompt `Signals:` line | `prompt.go:199-232`, consumed by the file-signal suggester `internal/evidence/suggest.go:711-734` | known kinds only (`ext`/`name`/`path`/`mark`), charset-restricted values, 12 tokens × 40 runes; anything else dropped |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR`, `--dirs` | `cmd/gauntlet/flags.go`, `cmd/gauntlet/paths.go`; parsed by `ParseSpecs` (`agent.go:380`), `ParseBin` (`agent.go:810`), `ParseAgentCmd` (`custom.go:495-523`), `discover.go:44-77`, `resolveDirs` (`paths.go:23-86`) | allow-listed tool names; every pinned model id charset-restricted and refused when it starts with `-` (`validateModel`, `modelRe`, `internal/agent/agent.go:786-799`), with `dsh` narrowed further (`dshModelRe`); `@effort` charset-restricted for every agent and refused where no verified flag exists (`effortRe`, `takesEffort`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`ParseBin`); non-empty checks for `--agents`, `--bin`, `--agent-cmd`, `--usage-cmd`, `--suggest-agent`, `--exclude`, `--merge-into`, `--pr-base`, `--show-prompt` (`cmd/gauntlet/flags.go:504,549,740,753,763,785,884,887,890`, and `trimFlag`, `flags.go:1201-1215`); `--usage-cmd`'s first word resolved against the same absolute-only `PATH` the probe gets, so a command that cannot launch is refused at the flag rather than failing open on every check (`resolveUsage`, `flags.go:547-593`); target dirs expanded and deduplicated by realpath (`paths.go:23-86`); `--prompt-dir` takes regular files only, validated to be a directory, and a path that is not there is refused at the flag (`flags.go:991-1013`), control-char names rejected; `--log` validates destination is not a directory (`validateLog`, `flags.go:1081-1118`); subcommands refuse flags they do not read (`rejectStrayFlags`, `flags.go:1167-1199`, the table at `flags.go:1143-1163`); custom agent loading gated to subcommands using agents (`usesAgents`, `flags.go:1120-1123`); subcommand peeling ignores empty arguments and lone dashes (`flags.go:1355-1392`); branch and revision commands separate options with `--` and `--end-of-options` (`branch.go:51,97,290,303,377,411,446,457,473,495,506`, `worktree.go:321,396,534,735,756,983`, `internal/gitx/status.go:20,39`) |
| `--paths` scope entries | `raw.paths` collected in `cmd/gauntlet/flags.go`, refused in `finishFlags` (`flags.go:615,802-819`), then pasted into the review prompt as an instruction by `pathsNote` (`internal/prompt/compose.go:248-279`), outside the review markers with the other operator instructions | operator-set, but free text wherever a wrapper built the list rather than a person typing it, and what the agent obeys is the prompt, not the flag: an entry is refused where the operator can see which one it was when it is empty, past `PathEntryMax` (200 runes), or carries a backtick, a control character, a `Cf`/`Zl`/`Zp` character, or any whitespace but a plain space (`PathEntrySafe`, `compose.go:168-193`). The scope block names at most 20 entries (`pathsNoteMax`, `compose.go:161,236-241`) and re-renders each through `pathEntry` (line breaks and controls to spaces, a backtick to an apostrophe, NFC, then a render equal to the entry or nothing, `compose.go:195-215`), so an entry that survives is named as itself and cannot read as an instruction around `quoteList`'s backticks. What is dropped is counted in the prompt rather than silently narrowed (`PathsNamed`, `compose.go:232-246`; the note in `pathsNote`); when nothing survives, the block says the scope is empty and asks for no findings and no edits. The scope is prompt-enforced, not mechanical: an agent is told the listed paths and can ignore them, and the suggest, commit, and conflict prompts deliberately keep the whole tree in view (`pathsNote`) |
| `--append` instruction | `o.appendInstruction` collected at `cmd/gauntlet/flags.go:381`, refused past `AppendInstructionMax` (4 KiB) in `finishFlags` (`flags.go:615,825-829`), passed to `Compose` at `internal/prompt/compose.go:312-314` as `Additional instruction from the operator:` after the review body is marker-escaped | operator-set, but free text wherever a wrapper built it rather than a person typing it, and it is pasted into every review prompt the run sends, so an unbounded value was an unbounded prompt resent once per review. The cap is bytes, the unit the token bill is measured in (`AppendInstructionOK`, `compose.go:228-230`); a longer brief belongs in a project prompt, which has its own read cap. No charset refusal like `--paths` applies: the instruction is the operator's own words, and a hostile wrapper that builds it can already name any prompt it wants. It remains subject to the ground rules and containment in the composed prompt, which the same composition carries. Shown by `--show-prompt` |
| `--sandbox-write DIR` grants | collected in `cmd/gauntlet/flags.go`, checked in `resolveSandboxWrites` (`flags.go:1217-1252`) from `finishFlags` (`flags.go:615,1000-1002`) | operator-set, and the one path flag whose value is checked against the filesystem: `~` and `$VARIABLES` expand through `gauntlethome.ExpandPath`, and a grant that is already absolute is refused at parse time unless it stats as an existing directory. A relative grant is left to the sandbox builder, which is the only place that can resolve it against a reviewed worktree, so it is checked at launch instead and reaches the same refusal. What the flag buys is the point: each grant adds its whole subtree to the agent's writable set, so the check that the directory exists is a usage check and not a narrowing of what the grant permits (`internal/runner/sandbox.go`, `internal/runner/sandbox_linux.go`, `internal/runner/sandbox_darwin.go`) |
| `--semcode` indexer presence | checked before the locks in `run` (`cmd/gauntlet/main.go:129,295-303`) and warned beside the schedule under `--dry-run` (`main.go:483-488`); the name is declared once (`semcodeIndexer`, `cmd/gauntlet/semcode.go:35`) | not untrusted input but a moved validation point worth naming: it is the same `agent.Resolve(semcodeIndexer)` the builder makes, so the check beside the flag and the check at build time cannot disagree about which binary is meant. A machine without the helper now fails at the invocation with exit 2 rather than after prompt discovery, the suggest step, and both locks. The resolved path goes through `pathNoCWD` (`internal/agent/agent.go:204-253`), so a planted `./semcode-index` in the reviewed tree still cannot win |
| Run budgets: `--runtime`, `--token-budget` | `cmd/gauntlet/flags.go:399-401`; `budgetExhausted` (`internal/runner/loop.go:259-272`), `Stats.Tokens` (`internal/runner/stats.go:395-402`); live cap at `internal/runner/reviewcap.go:40-61` | operator-set, both default 0 (unlimited) and both refused negative (`flags.go:194-219,864-865`). Scheduling checks stop new work; the live cap cancels a review reaching the whole token ceiling on its own. It does not split the remaining budget among lanes. Missing or under-reported tokens undercount spend, and commit and conflict usage is excluded. See B2, spend. |
| `--keep-runs N` history prune | `journal.Prune` (`internal/journal/retain.go:45-56,78-198`) called from `writeSummary` (`cmd/gauntlet/main.go:1116,1159-1166`); default 200 (`cmd/gauntlet/flags.go:451,256-261`) | `N <= 0` keeps everything; negative refused (`flags.go:861-862`); deletion is taken from a walk that takes only real shard directories (`allJournals`, `internal/journal/index.go:768-823`) and only `<id>.jsonl` files whose stem passes `ValidRunID` (`internal/journal/history.go:88-104`, applied in `journalsInDir`, `index.go:842-885`), so a symlinked shard or a planted name never widens the blast radius; the index row is dropped before its journal, the rewrite is under the index lock, an index line is capped at 4 MiB (`indexLineMax`, `retain.go:254-258`), and a prune failure is a warning that leaves the tree growing |
| `runs --limit N` index listing | `fs.IntVar(&o.runsLimit, "limit", ...)`, `cmd/gauntlet/flags.go:476`; refused on any subcommand but `runs` (`flags.go:670-672`); read by `journal.Recent` into `parseTail` (`internal/journal/index.go:1279-1310`) | operator-set on local state, but the number is unbounded, so it no longer sizes an allocation: `parseTail` reserves `min(n, maxTailHint)` with `maxTailHint = 1024` (`index.go:1264-1269`), because a `Summary` is a few hundred bytes and `runs --limit 100000000` otherwise reserved gigabytes for an index holding a handful of rows. The cap is a hint, not a limit: append still grows the slice to what the index really holds |
| `<binary>.previous`, the copy an update leaves behind | `keepPrevious`, `internal/selfupdate/selfupdate.go:328-373`, called from `applyTo` (`selfupdate.go:232,296`) before the install rename; `make install` writes the same name (`Makefile:637`) | operator's install directory, which the update already has to write to install at all, so the boundary is unchanged. The copy is made through `os.CreateTemp` in that directory, `Sync`ed, closed, chmod'd to the replaced binary's own mode, and renamed onto the destination, and the directory entry is `Sync`ed after the rename so a power cut cannot leave a file the next boot cannot find; every failure aborts the update rather than installing with no way back. The destination is not opened `O_NOFOLLOW` and no regular-file check precedes the rename, but a rename replaces a symlink rather than following it, so the only thing a planted `<binary>.previous` controls is what a later manual rollback installs. The [rollback recipe](CLI.md#updating) preserves the current binary first, then moves this copy into place without re-verifying it |
| `gauntlet runs --json` | flags `--json` and `--output` (`flags.go:471-475`), global on every command, with `auto` staying text including when piped; `writeRunsJSON`, `cmd/gauntlet/runs.go:255-264`, encoding `journal.Summary` rows (`internal/journal/journal.go:154-187`) plus the state root, the journals directory, and the pruned list | operator-set, on local state the same user wrote; the rows carry the reviewed tree's directory basenames and the run's own argv, so the content is B1 and B6 text going to a program rather than a person. `json.Encoder` with `SetIndent` leaves HTML escaping on for this document, so `<`, `>`, `&`, and every control byte below 0x20 are written as `\uXXXX` and no escape sequence crosses as raw text; no `Display` runs over a field here or in the human table, whose DIRS column prints `filepath.Base` of each row directly, so on this path the encoder is the only thing between a repository's directory name and a terminal, and the remaining exposure is a consumer that decodes a field and re-renders it raw. The argv the index keeps carries the same free text as the event log, so both are shortened to `~` before they reach disk (`journaledArgs`, `cmd/gauntlet/main.go:1088-1111`; `journaledEvent`, `main.go:1075-1086`; `RedactHome`, `internal/normalize/display.go:108-143`, rewriting only at a whole path component); what is on disk therefore names no OS account, while the live terminal and the `--log` file still do. A read failure is an error, never an empty quarantine, and empty listings are `[]`, never `null` (`runs.go:157-163`), so a consumer cannot read a failed read as a claim that nothing is recoverable. The `history` object also carries `agents`, a count of the custom definitions the tree holds, read from the tree this document reports on rather than from the registry (`DefinitionsOnDisk`, `internal/agent/custom.go:706-721`, read in `writeRunsJSON`, `cmd/gauntlet/runs.go:255-264`) so it is a count and not a path: an archive that lost `agents.json` restores as a tree that runs, lists its history, and reviews with the built-in agents from the next run on, with nothing else in the object saying a definition is gone. A definitions file that cannot be parsed is an error rather than a zero, because a corrupt copy and a tree that had no agents need opposite responses |
| `doctor` state-root probe | `stateRootProblem` (`cmd/gauntlet/doctor.go:405-438`) | diagnostic only, but it writes: a temp file named `.gauntlet-doctor-*` in the state root, closed and removed before the check reports, and an unwritable or undeletable root is now the command's failure verdict (`doctor.go:276-279,338-340`) rather than a line that scrolls past; a root that does not exist yet is not probed, so doctor creates nothing the first run would not |
| `.git/info/exclude` append | `ExcludeOwnArtifacts` (`internal/gitx/worktree.go:85-172`) | the reviewed tree picks `gitDir`, so `MkdirAll` and `OpenFile` would follow a planted `.git` symlink or gitfile out of the repository; the parent directory is re-checked with `os.Lstat` requiring a real directory (`realDir`, `worktree.go:245-248`) and the append is refused when it is not. The read of that file, which happens only to check for two short entries the run needs, is capped at 1 MiB rather than taken whole, because the tree chooses the file and an oversized one is not worth the memory (`maxExcludeBytes`, `worktree.go:720-724`, read at `worktree.go:139-146`). Every failure now comes back, including the write and the close, and the run logs it (`internal/runner/runner.go:306-311`): a short write is a failure rather than a success, because the next run's substring check would not match a truncated line and would append the same entry again on every run. Deciding what is missing and appending it is one read-modify-write on a file every checkout of the repository shares, so the whole check-then-act runs under one exclusive `flock` on a sibling lock file (`lockExclude`, `internal/gitx/worktree.go:174-216`): `--dirs` builds a `Repo` per directory and two of them can be two checkouts of one repository, and a second gauntlet on the same clone is a separate process. Without it both writers read the file before either appends, both decide the same entries are missing, and the exclusion is written twice, and every later run keeps the duplicates because the presence check stops at the first match. The lock is a sibling rather than the exclude file itself, so taking it neither creates nor truncates the file being decided, and it is opened `O_NOFOLLOW` and refused unless regular, so a planted lock path cannot make a run block on a FIFO |
| Environment: `PATH` | `pathNoCWD` (`agent.go:203-213`), `gitPath`/`gitEnv` (`internal/gitx/gitx.go:43-58`, `internal/gitx/exec.go:396-416`), `ghx.Available` (`internal/ghx/ghx.go:95-98`), `probeEnv`/`resolveProbe` (`usagelimit.go:93-114`) | cwd-relative and relative entries dropped for agent, git, git-child, `gh`, and usage probe resolution; child process environments isolated to absolute-only PATH via `CleanPATH`/`AbsPATH`/`AbsPATHEnv` (`internal/runx/runx.go:264-340`) and executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths (`internal/runx/runx.go:359-364`). An empty `PATH` falls back to `$HOME/.local/bin` and the fixed system prefixes (`defaultPath`, `runx.go:291-313`), for every executable rather than for agents alone, which widens the set a run will launch to those absolute directories and to nothing the reviewed tree can name. The rule is stated once, in `AbsPATH`, so no two resolvers can disagree about it |
| Environment: `HOME` (through `os.UserHomeDir`), `GAUNTLET_HOME`, `GH_TOKEN`/`GITHUB_TOKEN`, `TERM`/`NO_COLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR`, `GAUNTLET_STATE`, `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, `REDUCED_MOTION`, `GIT_SSH_COMMAND` | `internal/gauntlethome/gauntlethome.go:42-73`, `selfupdate.go:96-120`, `internal/report/report.go:51-83`, `internal/selfupdate/reload.go:22-23,185-234,263-280`, `internal/ui/view.go:404-408,420-437`, `internal/envx/envx.go`, `internal/gitx/exec.go:396-416` | operator-controlled, same-user trust; the list is closed by a test that walks non-test source for `os.Getenv`/`os.LookupEnv` and requires every literal, constant, or ranged-over name to be in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`), so a new knob cannot ship undocumented; `HOME` is the default state root only, and a home that cannot be read degrades to a local `.gauntlet` that custom agent loading refuses (`internal/gauntlethome/gauntlethome.go:42-73`); `GAUNTLET_HOME` is checked by asking the resolver rather than by a second copy of its rule, so the startup refusal and the root a run then writes to cannot disagree: a value that cannot be built into a usable root is a usage error naming what it resolved to (`checkStateHome`, `cmd/gauntlet/flags.go:1041-1079`), and a value left a tilde is refused with it, because `ExpandPath` expands only `~/...` on purpose and the state root has no reading of a bare `~` or another account's `~user` for which the answer would be a directory literally named that inside the tree under review rather than the fallback (`Dir`, `internal/gauntlethome/gauntlethome.go:42-73`). A root that is none of the above still degrades to local `.gauntlet` (`internal/gauntlethome/gauntlethome.go:42-73`), which the journal accepts and a reload handoff now refuses, since that path is inside the tree under review (`saveHandoff`, `cmd/gauntlet/reload.go:35-40`); expands tildes and environment variables and is made absolute at resolution so the state root cannot depend on the current directory (`CustomFilePath`, `custom.go:676-688`); `GAUNTLET_STATE` is read only where the process set it (`stateEnv`, `reload.go:22-23`, read at `reload.go:191-192`) and is replaced on re-exec rather than inherited (`Reexec`, `reload.go:263-280`); its handoff file is verified by `LoadState` to require `.json` extension, absolute path, clean path without traversal, opened through `safefile.OpenRead` (`internal/safefile/safefile.go:36-45`), which carries `O_NOFOLLOW` into the open syscall and refuses a non-regular descriptor in its place, and read capped to 16 MiB (`maxHandoffBytes`, `reload.go:180-183`); `SaveState` validates `runID` against directory traversal and path separators (`reload.go:145`) and flushes with `tmp.Sync()` before atomic rename; `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, and `REDUCED_MOTION` disable TUI animation glyphs (`internal/ui/view.go:404-408,420-437`); the values that mean off for those three and for `CLICOLOR_FORCE`/`FORCE_COLOR` are one list, trimmed and case-folded, in `internal/envx/envx.go`, and `TERM` is compared against `dumb` the same way, so a wrapper exporting `TERM=DUMB` or a padded value still loses its palette (`internal/report/report.go:51-83`); `GIT_SSH_COMMAND` overrides repository-local SSH commands and empty or whitespace-only values default to `ssh` (`internal/gitx/exec.go:396-416`); color variables configure ANSI output (`internal/report/report.go:51-83`); `--update-repo` is `owner/repo` only (`ParseRepo`, `selfupdate.go:77-105`) |
| Agent stdout/stderr | pipes in `internal/runner/exec.go:241-269`; line scan `internal/runner/exec.go:665-735` | 4 MiB per line emitted in chunks with UTF-8 rune boundary preservation (`internal/runner/exec.go:40-48,694-705`), trailing CR stripped (`internal/runner/exec.go:680-682`), escape/control/bidi/separator strip before terminal (`internal/normalize/display.go:32 Display`), width cap 2000 code points (`internal/runner/exec.go:54`), rate limit 200 lines/s (`attempt.go:31`); ANSI CSI sequences terminated on standard final characters in token width calculation (`internal/ui/clip.go:75-117`); stalled command groups force-killed with SIGKILL on drain timeout (`internal/runner/exec.go:605-613`); even `--raw` passes `Display` and the width cap (`internal/runner/exec.go:480-490`); usage and sink callbacks serialized across stdout and stderr streams (`internal/runner/exec.go:316,359-361,416,433-436`); a pipe that breaks part way is reported rather than read as a finished answer: `scanLines` returns the read error apart from `io.EOF`, the first one per launch is kept, and the result carries it beside the exit code (`internal/runner/exec.go:665-735,291-299,498,640`) |
| Stream-JSON events from agents | `internal/runner/exec.go:449-473`; `internal/streamjson/streamjson.go:81-99,107-138,199,202,217-248` | extraction depth capped at 8; role and type values normalized case-insensitively; objects marked role `tool`/`user` or type `user`/`tool_use`/`tool_result` contribute neither text nor usage; classification is not origin authentication; malformed JSON falls back to plain text and `--raw` bypasses classification |
| Token counters parsed from output and transcripts | `internal/runner/exec.go:302-305,365-392`; transcript watch off with `-tags notoktop`, `transcript_toktop.go:17`; custom roots `custom.go:68`, resolved at registration by `registerTranscript`, `cmd/gauntlet/transcript_toktop.go:29-43`, through `ResolvedRoots`, `internal/agent/custom.go:299-332` | integers only; transcripts are other files under `$HOME`. A root is operator-set in the operator's own `agents.json`, so it is same-user input, and it is now expanded under the rule every other operator-supplied path in this program follows: `~` and `$VAR` through `gauntlethome.ExpandPath`, with an unset or empty variable an error rather than a silent empty segment, because the transcript reader does not expand `$VAR` and a root handed to it verbatim looked for a directory literally named `$AGENT_HOME/sessions`, found nothing, and left the agent's live counts at zero with no error anywhere. The reader's own `{dir}` placeholder survives the expansion, since no expansion turns it into a path. A root that cannot be resolved is refused when the definition is validated, so a broken root never reaches a run (`validate`, `custom.go:268-297`) |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:166` | HTTPS, 4 MiB decode cap; bounded by `fetch` (`selfupdate.go:574-590`); `owner/repo` charset-checked before it is concatenated into the API path |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:232` | validated by `validateAssetURL` (`selfupdate.go:375-394`) to require HTTPS and authorized GitHub release hosts (`github.com`, `api.github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests); HTTP requests routed through `getAsset` (`selfupdate.go:415-455`), which retries a transport failure up to `assetAttempts` times with a jittered doubling wait, re-running `validateAssetURL` and the redirect cap on every attempt and never retrying a refused status, a rejected URL, or a cancelled run (`transient`, `selfupdate.go:457-488`); the retry ends at the response headers, so a body already partly written to the temp file is never resumed and a second copy cannot be appended to it; HTTP redirects re-verify `validateAssetURL` and are capped at 10 (`client.CheckRedirect`, `selfupdate.go:152-164`); checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap; SHA-256 verified using constant-time comparison (`subtle.ConstantTimeCompare`, `selfupdate.go:277`); downloaded binary flushed via `tmp.Sync()` (`selfupdate.go:280`) before atomic replace; `fetch` rejects responses exceeding size limit (`selfupdate.go:574-590`); download and error paths drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:188,191,558,568-572,585,588,609,612`); remote error messages decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:184,450,557-566`); a listing entry counts only as 64 hex digits (`checksumFor`/`isHexDigest`, `selfupdate.go:631-660`) |
| `gh` CLI JSON and PR URLs | `internal/ghx/ghx.go` `Find`/`Create` | argv-only; executable invoked with `runx.AbsPATHEnv()` (`internal/ghx/ghx.go:253-262`); PR URL must be `https` on the expected host (`validateURL`, `internal/ghx/ghx.go:245-251`); a PR is reused only when its head owner matches the push destination (`ownsHead`, `internal/ghx/ghx.go:173-191`) |
| Git outputs (shortstat, porcelain, check-ignore) | `internal/gitx/stats.go:53-97`, `internal/gitx/status.go:58-86,157-177`, `internal/gitx/list.go:34-57` | regex/line parsing; CRLF `\r` stripped (`internal/gitx/status.go:68,179`); C-quoted paths decoded (`unquoteC`, `internal/gitx/status.go:223-285`) then display-sanitized before any message (`safePaths`, `internal/runner/sanitize.go:21-27`; dashboard `internal/ui/ui.go:780-808`; plain reporter `internal/report/report.go:147-152`); counts only, never executed |
| Reviewed repo's `.git/config` and `.gitattributes` | every git call, `internal/gitx/exec.go:26-33,73-136,233-270` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external`, `core.gitProxy` forced empty; `protocol.ext.allow=never`; `attr.tree` pointed at the empty tree so in-tree `.gitattributes` cannot select a smudge filter or merge driver; local `filter.*`/`merge.*`/`diff.*` commands, `core.editor`, `core.askpass`, `gpg.*.program`, and peers blanked from `--local --list`, and `commit.gpgsign`, `tag.gpgsign`, and `push.gpgsign` forced false; the signing key is in that list because it is the one exec a reviewed config triggers with no attribute file involved, so a repository carrying `gpg.program` beside `commit.gpgSign` reached a shell on the first commit any review made, before an agent was consulted (`isSignProgram`, `internal/gitx/exec.go:292-305`; `isSignToggle`, `internal/gitx/exec.go:306-321`, in `disableLocalDrivers`, `internal/gitx/exec.go:248-270`). The blanking is local to the reviewed config, so an operator signing through a global `gpg.program` still does, and a review's commits, which this tool writes rather than the operator, are no longer signed on a reviewed repository's demand; the overlay is rebuilt whenever the size or mtime of any config file it was derived from moves, which is the local one and the system and global files the operator's own `credential.helper` values were read from (`--show-origin`), because a review runs with its permissions bypassed and can plant a driver in the local config mid-run and because a helper the operator removed has to stop being asserted; the cost is one lstat per watched file per git call against two subprocesses per rebuild (`stampConfig`, `internal/gitx/gitx.go:226-235`, `safeWatchedStale`, `internal/gitx/exec.go:450-457`, `extraSafeConfigLocked`, `internal/gitx/exec.go:439-448`); extra safe config propagated to worktrees and sub-repos via `subRepo` (`adoptSafeConfig`, `internal/gitx/gitx.go:309-323`, `internal/gitx/worktree.go:35-58`); git resolved on absolute-only PATH so a planted `./git` cannot run; `GIT_SSH_COMMAND=ssh` outranks a repo-local `core.sshCommand` unless the operator already exported one, and empty or whitespace-only values default to `ssh` (`mergeGitEnv`, `gitEnv`, `internal/gitx/exec.go:396-416`); git's own children die with its process group and bounded pipe wait (`internal/gitx/exec.go:327-395`); git stderr redacts embedded userinfo credentials (`internal/gitx/exec.go:327-395`); branch and revision commands separate options with `--` and `--end-of-options` (`internal/gitx/branch.go:111,157,201,253,355,368,442,465,509,530,546,568,579`, `internal/gitx/worktree.go:345,420,558,679,700,759,792,826,1056`, `internal/gitx/status.go:20,39`) |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:123-139` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized and NFC-normalized before it reaches a terminal or file (`lock.go:22-28,135-160,163-186`); `Note` and `readNote` handle `EINTR` and partial writes in retry loops (`lock.go:135-160,163-186`); lock release preserves the descriptor flock and truncates the note to prevent file deletion and inode recycling races (`lock.go:202-215`) |
| Conflicted paths interpolated into the conflict prompt | `git diff -z` into `runConflictAgent`, `conflict.go:152-224`, then `ConflictPrompt` (`compose.go:344-355`) | a path is named only when it equals `normalize.Sanitize(p)` and `conflictPathOK` (control/Cf omitted, `RESOLVE:` and `</files>` omitted, 1024-rune cap); 50-file cap (over-cap skips the launch); list fenced in `<files>`; human-facing conflict hints quote branch and message via `runx.ShQuote` (`conflict.go:226-229`); a dropped path is still scanned for markers, so it holds the branch with a human |
| Helper-tool inventory appended to prompts | PATH probe at startup, `internal/runner/loop.go:205-212`, rendered `compose.go:99-138` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| File-signal suggester tree walk | `evidence/suggest.go:868-929` | 100k files, depth 12, 2k file heads × 4 KiB; opens via `os.OpenRoot` (`evidence/suggest.go:936-945,1047-1062`) so a symlink or path that escapes the reviewed tree is skipped |
| `~/.gauntlet/agents.json` | `internal/agent/custom.go:100-297,495-674` | rejects null, unknown fields, duplicate keys (including NFC-normalized case-variant duplicates and nested usage duplicates), trailing data, and built-in redefinitions; requires `{prompt}` in argv exactly once and forbids `{prompt}` in model/effort/stream/continue; requires `{model}` in model and `{effort}` in effort if set; forbids `{model}` and `{effort}` across stream and continue; forbids model when argv contains `{model}` and effort when argv contains `{effort}`; forbids placeholders in argv executable, usage roots, and usage suffix; rejects empty/blank arguments in argv, model, effort, stream, and continue; rejects whitespace-only notes and notes holding control or formatting characters, which are printed beside the agent's name in a doctor row and on the launcher's own line (`validate`, `internal/agent/custom.go:100-152`); expands tildes and environment variables in custom executable `cmd[0]` (`agent.go:305-316`); validates non-empty usage roots and model; rejects whitespace-only usage suffix; validates all definitions before registration; strips UTF-8 BOM if present; `CustomFilePath` returns empty without a state root; the read is `safefile.OpenRead` (`custom.go:550`), so a symlink at the last component and a non-regular file in its place are refused and the file is capped at `maxCustomFileBytes` (`custom.go:525-532`), the same guarded open and bound the project prompt, the run journal, and the shared exclude use; a file exactly at the bound is still read. The file is the base of the registry, not its limit: a `--agent-cmd` for the same name unregisters whatever the file and the shipped definitions said and installs the command line's own argv, and a built-in tool is not in the registry, so that removes no tool and `Register` still refuses to rename one (`configureAgents`, `cmd/gauntlet/flags.go:485-545`; `Unregister`, `custom.go:431-437`). A repeated `--agent-cmd` naming one agent two different ways is refused instead of letting the last one win, case-folded so `Claude` and `claude` cannot both install (`flags.go:493-517`), and the whole list is parsed and checked before any of it registers, so a bad entry leaves no half-configured registry. The precedence is a usability decision, not a trust claim: both the file and the flag are operator-supplied, and the flag is the more specific of the two |
| `--usage-cmd` probe | `internal/runner/usagelimit.go:93-114` | argv-only; isolated execution directory (`os.TempDir()`); relative and cwd-relative entries dropped from `PATH` in `probeEnv()` and executable resolved against absolute-only PATH (`resolveProbe`, `runx.LookPath`); child process environment isolated via `runx.AbsPATHEnv()`; 10s deadline, 4 KiB per output stream; own process group killed on return as well as cancellation; final non-empty line parsed as finite 0-100, rejecting NaN and Inf (`parseUsagePercent`, `internal/runner/usagelimit.go:125-154`); usage limit check guards against NaN (`usagelimit.go:47`); failure warns once and leaves the threshold unenforced, and a command that could never launch is caught earlier, at the flag (`resolveUsage`, `cmd/gauntlet/flags.go:547-593`); finish flag set via atomic swap (`internal/runner/usagelimit.go:68-71`) |
| `--log FILE` destination | `openLogFile`, `cmd/gauntlet/main.go:908-940`; `validateLog`, `flags.go:1081-1118` | startup rejects an existing directory target; at open an `os.Lstat` refuses a symlink and any non-regular file, the open itself carries `syscall.O_NOFOLLOW` with 0600, and a regular file that survived is chmod'd to 0600 before any output; no parent-directory confinement, no size cap, and no rotation |
| `--semcode` indexer | `buildSemcodeIndex`/`runIndexer`, `cmd/gauntlet/semcode.go:82-104` | `semcode-index` resolved through `agent.Resolve` (`pathNoCWD`, `internal/agent/agent.go:204-253`), so a planted `./semcode-index` in the tree cannot win; argv-only, no shell; child environment isolated with `runx.AbsPATHEnv()`; 30-minute per-directory cap with a deferred process-group SIGKILL (`runx.Guard`, `runx.KillGroup`); the child's working directory is the reviewed tree, so the tree decides both what it walks and the file names it reports, and each of its streams gets its own `normalize.DisplayWriter` (`cmd/gauntlet/semcode.go:87-96`), which passes every complete line through `Display` and holds an unterminated one until `Flush` after `Run` joins the copy goroutines (`internal/normalize/display.go:145-243`); the held partial line is capped at 1 MiB and released on a rune boundary (`display.go:196-242`) |
| `doctor` environment report | `envSettingLines`, `cmd/gauntlet/doctor.go:374-400`; table `helpEnvVars`, `cmd/gauntlet/help.go:179-192` | reads only the documented names, so nothing the operator did not set for gauntlet is echoed; a variable the table marks secret is reported as `(set)` and never as a value, so a pasted `doctor` transcript carries no token (`doctor.go:374-400`); `GAUNTLET_HOME` is left out because the State line above already names it; nothing here interprets the value, and presence is the only thing reported. A test pins the set: every `os.Getenv`/`os.LookupEnv` name in non-test source is either in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`) |
| `doctor` git version probe | `Version`, `internal/gitx/version.go:39-55`, reported at `cmd/gauntlet/doctor.go:147-162` | a diagnostic subprocess on the operator's machine, resolved through the same `PATH` memo a run's first git call uses so both name the same binary, run with an absolute-only `PATH` (`runx.AbsPATHEnv()`), bounded to 256 bytes (`versionMaxBytes`, `version.go:27-29`), with its own process group and a deadline. It is the one git call this tool makes with no safe-config overlay, deliberately, because `--version` reads no repository; a `git` on the operator's `PATH` is the one R2 already assumes. The line is parsed against a floor of 2.24 (`MinVersion`, `version.go:22`) and an unparseable one is reported as unknown rather than as a pass (`BelowFloor`, `version.go:61-70`); a `git` that exits nonzero but prints a version still has that version read, because the floor check is the point and the exit status is not the claim |
| `doctor` unlanded review branches | `laneBranches`, `cmd/gauntlet/doctor.go:359-361`; read by `Repo.LaneBranches`, `internal/gitx/branch.go:98-108`; reported at `cmd/gauntlet/doctor.go:327-336` | a read of the reviewed repository's refs on operator-set local state, and a report rather than a control: `for-each-ref refs/heads/gauntlet/` through the hardened invocation and the safe config overlay every run's git calls use, bounded to one line per branch with no file read whole, and it writes nothing. A run deletes a lane branch once its review lands, so the names it prints are branches no merge carried; they are the one review output no copy of the state tree holds. What is printed is branch names, which `git check-ref-format` cannot carry a control character in, so no escape sequence a ref name could hold reaches the terminal. No git, a directory that is not a repository, and a store that will not list its refs all read as nothing to report, which is a missing line rather than a claim that no unlanded review exists |
| `doctor` and `runs --json` state-tree inspection | `Inspect`, `internal/journal/status.go:42-58`; read at `cmd/gauntlet/doctor.go:314` and `cmd/gauntlet/runs.go:44,50`; the half-line test is `truncated`, `internal/journal/status.go:115-130` | operator-set on local state this user wrote, and a report rather than a control: the walk counts journals, index rows, rows the two copies disagree about, pruned journals, and journals ending without the newline every event ends with. The last one reads one byte at each end of every journal under `runs/` and the quarantine, through the symlink-refusing `openRead`, so the cost is one bounded open per file and no file is read whole. It is where an operator learns an archive is short: the half-line such a file ends on is not JSON, so every reader drops it and the run replays as a shorter run that looks complete. The two directions of disagreement are reported apart, because only one of them is repairable: a journal the index does not name is rebuilt by the next listing, and a row whose journal is gone names a run nothing else holds, which is the state a restore that carried the index over and left the runs behind leaves (`unmatchedRuns`, `cmd/gauntlet/doctor.go:448-455`) |
| `dsh --dump-config` probe | `dumpDshConfig`/`dshDefaultProvider`, `internal/agent/dsh.go:145-221` | runs only for a `dsh:<model>` pin; child environment isolated with `runx.AbsPATHEnv()` through `runx.EnvIn` (`dsh.go:156`); bounded to 4 MiB output via `runx.Bound` (`dsh.go:98,158`); own process group, 120s cap, deferred group SIGKILL via `runx.KillGroup` (`dsh.go:159`); provider parsed with a narrow regex (`dshProviderRe`, `dsh.go:71`); the memo is keyed by the launcher argv, and the argv carries the `--bin` override, so one launcher's config cannot pin another (`dshProbes`, `dsh.go:100-117`, consulted in `dshDefaultProvider`, `dsh.go:176-221`); overlay values charset-restricted before they are quoted into YAML (`dshModelRe`, `agent.go:365`); provider and model validated against `dshModelRe` (`dsh.go:272-282`); the overlay key percent-encodes `/`, `:`, and uppercase and joins the two halves with `@`, so it names one file in the overlay directory (`dshPatchKey`, `dsh.go:228-252`) |
| OpenCode help probe | `openCodeV2`, `internal/agent/agent.go` | Runs the configured executable's `run --help` outside the review sandbox to choose its variant syntax and whether to request a standalone server. Output is bounded to 64 KiB, the probe has a five-second deadline and its process group is killed on exit. Failure is reported rather than assuming a version. |
| ACP session | `acpSession`, `internal/runner/acp.go`; `runProc`, `internal/runner/exec.go` | Exchanges bounded newline-delimited JSON with the supervised agent. Only an `allow_once` option for the active session is selected; unsupported requests receive an error. Text updates pass through the normal output sanitizer. The existing timeout and process-group cleanup apply. |
| ACP cached authentication | `acpSession`, `internal/runner/acp.go` | On an authentication-required session response, requests `cached_token` at most once only when advertised and no different default is named. No credential is read or sent by gauntlet, and no interactive login is started. |
| Kimi and Cursor ACP probe | `subcommandACP`, `internal/runner/acp.go` | Runs the executable's `acp --help` outside the review sandbox to detect the native protocol. Output is bounded to 64 KiB, the probe has a five-second deadline and its process group is killed on exit. An unavailable subcommand retains the existing CLI invocation. |
| ACP flag probe | `acpFlag`, `internal/runner/acp.go` | Runs the configured Gemini or Qwen executable's `--help` outside the review sandbox to detect an advertised ACP flag. Output is bounded to 64 KiB, the probe has a five-second deadline and its process group is killed on exit. An unavailable flag retains the existing CLI invocation. |
| Grok ACP probe | `grokACP`, `internal/runner/acp.go` | Runs `agent --help` outside the review sandbox, bounded to 64 KiB and five seconds, with process-group cleanup. ACP is selected only when stdio, local-only, and approval flags are advertised; local-only prevents a shared leader outside the sandbox from doing the work. |
| dsh ACP probe | `dshACP`, `internal/runner/acp.go` | Runs the installed launcher's `--profile acp --help` outside the review sandbox to detect its ACP profile, bounded to 64 KiB and five seconds with process-group cleanup. It runs only for an explicitly pinned provider/model, and the ACP session must confirm that route before prompting. |
| Installed ACP adapters | `withACP`, `internal/runner/acp.go` | Codex and Claude adapters are resolved only on the absolute-only PATH; no package is fetched. The chosen agent executable is passed to the adapter, and the session must confirm its noninteractive mode before a prompt is sent. The adapter and its children run under the existing sandbox and process-group controls. |
| Feynman and OMP ACP | `withACP`, `internal/runner/acp.go` | The `pi-acp` bridge for Feynman and OMP's native ACP mode are not selected automatically because upstream versions can translate a failed prompt into a successful `end_turn`. An operator may explicitly configure a verified adapter. |
| Copilot ACP | `withACP`, `internal/runner/acp.go` | Only selected when the installed `copilot` lists `--acp` on `--help` and the definition is the built-in one; the session runs `copilot --acp --allow-all`, the same permission bypass as the CLI path. The probe is bounded to 64 KiB and five seconds with process-group cleanup. |
| Custom ACP command | `Custom.ACP`, `internal/agent/custom.go`; `withACP`, `internal/runner/acp.go` | An operator-defined agent may name a literal ACP server argv. Empty arguments and placeholders are refused, no shell interprets the argv, and the existing sandbox and process-group controls apply. A different `--bin` executable retains the ordinary command. |
| Interactive launcher / picker keyboard input | `cmd/gauntlet/pick.go`, `internal/ui/pick.go`, `internal/ui/ui.go` | navigation keys jump to bounds (`g`/`G` in `internal/ui/pick.go:382-386`); cursor clamped to valid review rows via `clampReviewCursor` (`internal/ui/pick.go:345,449,452,503-514`); Esc/Ctrl-C during filter editing resets typing and clamps cursor (`internal/ui/pick.go:489-494`); empty filter matches block launch, with the reason returned by `blocked` and checked on Enter (`blocked`, `internal/ui/pick_view.go:210-218`, rendered at `pick_view.go:331,530,627`, checked at `internal/ui/pick.go:360-364`); Enter key terminates completed runs (`internal/ui/ui.go:780-808`); the branch, merge-target and dirty answers the launch gate is built from come from `treeState` (`cmd/gauntlet/pick.go:141-175`), which keeps a git that cannot answer apart from a tree git does not manage: `CurrentBranch` reports a detached HEAD as a state and any other git failure as an error, and only `ErrNotRepository` (`internal/gitx/gitx.go:68-77`, applied by `classifyNotRepo`, `gitx.go:79-103`) degrades to an empty preflight rather than a launcher refusal. Without that distinction a failing git rendered as a branchless, clean checkout, and an operator composing a run read a tree with nothing to merge into and nothing uncommitted |
| Planted symlinks/FIFOs in the tree | prompt discovery inspects candidates with `os.Lstat` requiring regular files (`prompt/discover.go:244,278`); prompt reads `prompt.go:278-329`, lock creation `runner/lock.go:53-89`, untracked counting `internal/gitx/stats.go:98-154`, reload handoff `reload.go:196-217`, crash checkpoint `cmd/gauntlet/checkpoint.go:105-137` | `O_NOFOLLOW|O_NONBLOCK` at open time, regular-file stats, size caps; stat errors propagated on open regular files (`internal/gitx/stats.go:169-190`); `LoadState` requires a `.json` extension, an absolute path, and a clean path without traversal, then opens through `safefile.OpenRead` (`internal/safefile/safefile.go:36-45`) so `O_NOFOLLOW` is on the open itself rather than on a separate earlier `Lstat` (`LoadState`, `reload.go:185-234`) |
| Developer build surface: `make repro` archives the working tree | `Makefile:876-915`; member list and archive `Makefile:884-885`; the tests holding the recipe to git's ignore rules `cmd/gauntlet/makefile_test.go:1029-1108` | the target is a developer convenience, not a shipped code path, but the archive is the whole checkout: it is written to `$(HOME)/.cache/gauntlet/repro` and the recipe refuses to run with `HOME` unset rather than writing to `/.cache`; the members are `git ls-files --cached --others --exclude-standard`, so a build output, cache, or local state that `.env`, `.gauntlet/`, and `.gauntlet.lock` already are cannot be copied, a new `.gitignore` entry covers the archive the moment it is written, and a pattern tar would match too broadly no longer decides; the directory is removed on exit by a shell trap. What the list cannot express is a file a developer has not ignored: an untracked credentials file is archived to `$HOME/.cache` for the duration of the build, where it is neither reviewed nor redacted |
| Release build surface: `cmd/sbom` writes the shipped dependency inventory | `run`, `cmd/sbom/main.go:62-131`; inventory `internal/sbom/sbom.go:104-217`, licenses `internal/sbom/license.go:34-80`; run by the release target (`Makefile:774`) and uploaded as `dist/sbom.json` (`.github/workflows/release.yml:240-248`) | a release-time tool on this repository, not a path a reviewed repository reaches, and it adds no dependency: the package doc says why (`internal/sbom/sbom.go:4-8`), and the implementation is the standard library plus `debug/buildinfo`, so the artifact that describes the dependency surface does not widen it. Its inputs are the built binary paths from argv, read with `buildinfo.ReadFile`, which follows a symlink and is not size-capped, so a path naming something other than a built binary is either refused or inventoried as whatever build info it carries. The license half reads two more untrusted shapes and bounds both: `go list` is a child process and each module's grant is a whole-file read, so the listing is capped at 8 MiB, the grant at 1 MiB, the pipe wait at 10s, and the child runs through `runx` for the process group, the shared `WaitGrace`, and the deadline kill that takes any grandchild with it (`runx.WaitGrace`, `goListMaxBytes`, `licenseFileMax`, `internal/sbom/license.go:41-51`; `moduleDirs`, `license.go:112-163`) `Merge` refuses a module path recorded at two versions, which catches binaries that did not come out of one tree, and `run` refuses a binary whose main module path is not the first one's, so one release cannot be described as two programs (`cmd/sbom/main.go:62-131`). A module whose grant the cache does not carry, or whose text the SPDX table does not recognize, fails the run before the document is written, and so does a module the binary records no `go.sum` hash for, which is every module reached through a local-path or directory `replace` and so is a component a scanner could not check against anything (`checkInventory`, `cmd/sbom/main.go:178-201`), so the inventory a release ships names the license and the go.sum hash of every module it links rather than reporting the gap on stderr and publishing the document anyway. It does not verify that a binary is the one `checksums.txt` covers, nor that a binary is what the build produced: the inventory is a claim the compiler stamped into a file, so it is not an integrity control for R2. The output write is `os.WriteFile(*out, ..., 0o644)` (`main.go:85`): it follows a symlink at the destination and leaves a pre-existing file's mode as it found it, where the runtime `--log` destination is refused when it is a symlink or any non-regular file and is opened `O_NOFOLLOW` at 0600 and then chmod'd (`cmd/gauntlet/main.go:908-940`). Nothing in a release workspace is hostile without a compromised build, which is R2's subject |
| Release build surface: make dist platform check | `Makefile:694-704`, with `GOAMD64`/`GOARM64`/`GOEXPERIMENT`/`GOFIPS140` exported at `Makefile:26-41` | a developer and release-time check on this repository's own artifacts, not a path a reviewed repository reaches. Every asset is read back with `go version -m` and its recorded `GOOS`, `GOARCH`, and, per architecture, its microarchitecture level, and the toolchain default experiment set, must equal what the build was told to use, so a `PLATFORMS` typo, a stale cross-compile, or a `go env -w GOAMD64=v3` left on a build machine fails the build instead of shipping a binary that cannot run on the machine its name advertises. It is a claim about the bytes compared against the name, not an integrity control: it is computed from the same file it describes, so it moves with a replaced asset, and `update` still installs what the release page serves, which is R2 |
| Developer build surface: `make check` runs a fetched analyzer | `STATICCHECK_VERSION`, `Makefile:83`; the invocation `Makefile:385`; the three tag-set arms `Makefile:484-486` | a developer and CI gate on this repository, not a path a reviewed repository reaches, and the same trade `make vuln` makes: the analyzer is not in `go.mod`, so `make check` resolves and runs it from the Go module proxy at one pinned version instead of whatever is on `PATH`, and `GOFLAGS` is cleared because `-mod=readonly` is this module's build flag. It runs `-checks=all` rather than the tool's default set, so every check the pinned version carries runs. The hashes come from the proxy's sum database rather than this tree's `go.sum`, which is the one thing the pin does not carry, and it runs over the tree's own packages and nothing else. It executes a third-party analyzer on the contributor's machine and on every CI push, and it publishes nothing |
| Developer build surface: make doctor prerequisite preflight | `Makefile:968-1048` | a contributor convenience that reads the machine, not the reviewed tree, so it crosses no boundary B1 does not already cross. It resolves the Go toolchain, the C compiler, `git`, `uvx`, `shellcheck`, `tar`, `cmp`, `install`, and either `sha256sum` or `shasum` on the developer's own `PATH`. Go, git, uv, and shellcheck are queried for versions; the other tools are checked for presence. These queries execute whatever `PATH` names, the same trust that `make test` and `make check` require; it creates the test scratch directory and nothing else, reads no repository content, and prints no environment value. Every gap is reported with its install advice and the targets that need it, and the exit status is 1 when any is missing, so one run answers the whole list instead of one tool at a time. It is not a security control and a passing run certifies nothing the pinned versions do not |
| Developer build surface: `make check-scripts` extracts and lints the workflow shell | `check-workflow-shell`, `Makefile:588-611`, run as a prerequisite of `check-scripts` (`Makefile:530`); the extraction the recipe depends on is held by `TestWorkflowShellExtractorCoversEveryRunBody` (`cmd/gauntlet/workflowshell_test.go:29-95`) and `TestWorkflowShellLintRefusesAnEmptyBody` (`workflowshell_test.go:97-199`) | a contributor and CI gate on this repository's own workflows, not a path a reviewed repository reaches, and it reads nothing but `.github/workflows/*.yml`. The awk body is the sharp edge: it parses YAML by indentation rather than by a YAML parser, so a `run: |` block it cannot follow would silently lint nothing. Two conditions close that, and both fail the target rather than pass it quietly: an extraction that produces an empty script writes a marker the recipe reads and exits 1 on (`Makefile:599-605`), and a run that finds no `run: |` body at all refuses to invoke shellcheck over an empty glob (`Makefile:608-611`). Every generated script is written under `$(TMPDIR)/workflow-shell`, which `test-tmpdir` places off this repository so the extraction cannot see its own output as repository content. The tool it runs, shellcheck, is the one CI cannot install a pinned copy of, so its version is a drift warning rather than a lock; the four workflow variables the extracted bodies read (`GITHUB_REF_NAME`, `SHELLCHECK_VERSION`, and the two the preamble names) are silenced with a scoped `SC2154` rather than left to a global disable |
| Release build surface: make smoke executes the artifact dist built | `smoke`, `Makefile:727-738`; the asset name it resolves, `host-artifact`, `Makefile:719-720`; run on every push at `.github/workflows/ci.yml:211` and on the macOS runner at `ci.yml:263`, and gating the release at `.github/workflows/release.yml:175-180` | the one release target that runs the binary rather than reading it, so it is the first place a replaced or misnamed asset is executed rather than described. It is a developer and CI gate on this repository's own artifacts, not a path a reviewed repository reaches, and it crosses no boundary B1 does not already cross: the path comes from this Makefile's own `DIST`/`BINARY`/`VERSION`, and the file at it is the one `dist` just wrote. What it does is execute that file with the contributor's or runner's own authority and no confinement, which is R2's assumption applied to this repository's own build output rather than to a reviewed tree: whoever can write `dist/` between the build and the run gets code execution on the machine that runs it. The check it performs is a version string, so it proves the binary starts and identifies itself and nothing about what it contains, which is the same limit `make dist`'s platform check carries. The asset name resolves from `GOHOSTOS`/`GOHOSTARCH` rather than `GOOS`/`GOARCH` (`Makefile:720`), which is what makes the executed file the one built for the machine running it: `go env GOOS` echoes an in-flight cross-compile target from the environment, so an exported `GOOS`, or this Makefile's own `dist` setting it per target, named a binary that cannot run here and the target refused a healthy release over a file it was never going to execute. Before the change the gate could pass without running anything |
| Published project site (`gauntlet.rocks`, Cloudflare Workers Static Assets) | served from `site/public`, bound by `site/wrangler.jsonc`; response headers are the only server-side control, pinned by `TestSiteHeadersAreTerminatedAndScoped` (`cmd/gauntlet/site_test.go:228-249`) | the one surface here a browser reaches over the network with no operator present, and the only entry point in this table whose inputs are not operator-supplied, since a visitor's request is not something gauntlet parses at all. It is static: `index.html`, three SVGs, and a screenshot. There is no handler, no query parameter, no form, and no script, so the injectable-input class does not arise and the header block is what stands between a visitor and the assets: CSP `default-src 'none'` with `style-src 'unsafe-inline'`, `img-src 'self'`, `base-uri 'none'`, `form-action 'none'`, `frame-ancestors 'none'`, `Referrer-Policy: no-referrer`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and HSTS with `includeSubDomains` (`site/public/_headers:6-11`). Cache freshness is set per path rather than left to the browser's guess from `Last-Modified`: the HTML revalidates on every load and the images and logos are cacheable for an hour, with no `immutable` because the screenshot and the marks are overwritten in place under stable names (`site/public/_headers:13-33`). The paths are named one by one, so an asset added to `site/public` is served with no cache directive until it gets a block of its own. Two of the security headers hold only because the file ends in a newline and names a path pattern, which is why the test asserts both rather than trusting the file: a truncated last line drops the transport header, and a headers file naming no path applies to nothing. What nothing here checks is the deploy itself. `site/wrangler.jsonc` pins the custom domain and the asset directory, while the credential that can publish over `gauntlet.rocks` lives in a Cloudflare account rather than in this tree, so the repository holds no control over what the domain serves and the header block is the whole of the server-side posture a reader can verify from here. The residual risk is the asset content: the screenshot is a captured dashboard frame and carries whatever the run it was taken from showed, and nothing in the release path reviews or re-captures it before deploy |
| `doctor` filesystem-sandbox capability probe | `SandboxSupport` (`internal/runner/sandbox.go:59`), backing `sandboxSupport` on each platform (`internal/runner/sandbox_linux.go:109-120`, `internal/runner/sandbox_darwin.go:32-42`); reported at `cmd/gauntlet/doctor.go:165-178` | a report rather than a control, and named because it decides whether the default sandbox exists on this host, which is R1's floor. On Linux it asks the kernel through `landlockABI` for the ABI that answers, so a kernel older than 5.13 and one built without `CONFIG_SECURITY_LANDLOCK` are each reported as what they are rather than inferred from the running binary's platform name; on macOS it stats `/usr/bin/sandbox-exec` for presence and an execute bit, because that file is what confines the agent and a darwin build without it has no confinement to report. Nothing it reads is untrusted: one syscall and one fixed absolute path. The error text it prints carries no tree data, and reaches the terminal through the doctor's own writer (`internal/normalize/display.go`) |
## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and both the opening and closing
marker strings in the body are escaped, and the rewrite is repeated up to
`markerEscapePasses` (8) so a body that kept re-forming a marker is delivered
rewritten rather than never delivered (`escapeMarkers`,
`internal/prompt/compose.go:60-73`). The seam the bound was written for does
not survive the rewrite order: the closing marker is replaced before the
opening one, so dashes the two markers share are consumed by the first
replacement. The operator's `--append` instruction is escaped on its own,
after the body, so it cannot ride a marker the body re-formed out of the fence
(`internal/prompt/compose.go:312-316`). The agent that
receives it has its own permission system disabled or auto-approved (or, for a
custom agent, whatever the operator put in argv). Applicable classes: elevation of privilege (repo
author -> code execution with user rights), tampering (working tree,
commits), info disclosure (secrets, source exfiltration). The fence is
advisory: the ground rules forbid git, deletion, and persistence, and
prompt-review gets a narrow exception (`compose.go:280-287`), but nothing
technical stops a sufficiently persuasive prompt from talking an agent out of
them. This is the accepted core risk; [DESIGN.md](DESIGN.md#non-goals) answers it operationally: run
untrusted repos in a container.
The operator's own text is a second fence, and on this path the one an attacker
does not need the repository to reach: `--paths` is pasted into the same prompt
as a scope the agent is told the review body cannot widen
(`pathsNote`, `internal/prompt/compose.go:248-279`). An entry that is not a
path is refused at the flag, and the entries that survive are capped, counted,
and rendered so none can read as an instruction around the list
(`PathEntrySafe`, `compose.go:168-193`; `PathsNamed`, `compose.go:232-246`).
The scope is advisory in the way the rest of the suffix is: an agent can ignore
a narrow list and edit the whole tree, which is why the count of what was left
out is stated in the prompt, and why a scope of nothing asks for no findings and
no edits rather than naming an empty list.

**B1 -> B3 (the indexer path).** `--semcode` builds the optional semantic
index before the first review launches, and the indexer is the one child
process whose output is not parsed: it names files and symbols out of a tree
the reviewed repository controls, so its stdout and stderr are untrusted
display input like any agent stream. It is now filtered as one:
`runIndexer` gives each stream its own `normalize.DisplayWriter`
(`cmd/gauntlet/semcode.go:87-96`), which passes every complete line through
`Display` and holds a partial line until the child is done, then `Flush`es
after `Run` has joined both copy goroutines
(`internal/normalize/display.go:128-224`). One writer per stream matters:
`DisplayWriter` is per-stream state and the two copy goroutines run
concurrently, so sharing one would interleave held bytes. The held partial line
is capped at 1 MiB and released on a rune boundary, so an indexer streaming one
endless line grows the buffer to the cap and no further
(`internal/normalize/display.go:170-191`).
What remains on this path is display integrity, not a second code-execution
path. The indexer's working directory is the reviewed tree (`cmd.Dir = d.dir`),
so the tree decides what it walks, and the binary itself is not plantable from
the tree: it is resolved with `agent.Resolve`, whose `pathNoCWD` PATH drops
cwd-relative entries (`internal/agent/agent.go:203-213,246-262`), and it
runs argv-only with an absolute-only-PATH environment, a 30-minute per-directory
cap, and a deferred process-group SIGKILL
(`cmd/gauntlet/semcode.go:25-29,90-96`). The filter removes escape, control,
and bidi sequences; it does not make a symbol name the indexer chose true.

**B2 (agent misbehavior directly).** Spoofing: fabricated agent output cannot
drive the terminal (B3 mitigations) but fabricated *content* flows into
commits, so attribution of prose is by review name, not by verified origin.
Who authors the commits depends on the mode, and the difference is a real
privilege transition:

- **`--jobs > 1` (worktree isolation):** the runner stages and commits each
  worktree itself (`Worktree.CommitAll`, called from `runLaneReview` in
  `internal/runner/attempt.go:132`); agents stay forbidden to run git ([DESIGN.md](DESIGN.md#isolated-parallel-reviews---jobs-n)
  rule 3). Between retry attempts the runner alone rewinds its worktree to
  the base commit so attempt N+1 starts where N did (`ResetToBase` in
  `internal/gitx/worktree.go:650-670`, called from `resetForRetry`): more runner-side
  git authority, exercised only inside gauntlet-created worktrees.
  Persistent lanes reuse one worktree across reviews; each advance or retry
  still starts from a known commit.
- **`--stacked-prs` (worktree isolation plus publication):** the runner has
  the same staging, commit, and retry-reset authority inside one scratch
  worktree, then invokes Git to push each committed child branch and `gh` to
  create its PR. `PrepareStack` (`internal/runner/stack.go:79-193`) runs every
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
  checkout. A failed push records the layer and later reviews branch from
  the last base that did push, so no agent runs on a commit that is not on
  the remote. A push that landed whose pull request the lookup cannot see
  stays in the chain (`ensurePullRequest`, `launchCheckoutChanged`,
  `runLoopStack`). An unreadable tip after that push stops the pass rather
  than branching the next review from the previous base. Adopting a kept
  checkout compares the registered path after symlink resolution
  (`worktreeListed`). The scratch checkout is removed only when every changed
  layer was pushed and that lookup confirmed each pull request. Stack branch names
  are derived from a public base commit, so a pull request is only reused as
  a run's own layer when its head branch lives in the repository gauntlet
  pushes to (`ownsHead` in `internal/ghx/ghx.go:151-172`): a PR opened from
  someone else's fork under a name a run is about to use is ignored, not
  adopted. Git and `gh` receive fixed argv elements rather than shell text;
  branch commands separate branch names with `--` option delimiters and
  rev-parse, log, and diff separate options with `--end-of-options`
  (`internal/gitx/branch.go:111,157,201,253,355,368,442,465,509,530,546,568,579`, `internal/gitx/worktree.go:345,420,558,679,700,759,792,826,1056`, `internal/gitx/status.go:20,39`);
  review names, commit subjects, and PR bodies cannot become commands. A PR
  body is assembled from values that originate in the reviewed repository --
  the agent's commit subject, the review prompt's own summary line, the paths
  its commit touched -- so each is stripped of control characters, flattened
  to a single line, NFC-normalized to prevent rune splitting, and length-bounded
  before it is rendered (with overview notes deduplicated across NFC/NFD forms), the inline delimiters are escaped so a note cannot close a link, an autolink, a raw tag, or an emphasis span without a line break (`escapeInline`, `prbody.go:179-203`), and a path is escaped into a code span that a backtick
  in it cannot close (`internal/runner/prbody.go:26-50,88,150-178`, `internal/runner/stack.go:655`). Markdown posted to
  GitHub is display, not execution, but a forged heading, a tracking image, a
  look-alike host, or an unbounded path
  list is still a reviewer reading something the run did not say. Git itself
  runs hardened against the reviewed repository's own config (see the `.git/config`
  row). Gauntlet's scratch worktrees are refused when `.gauntlet` or
  `.gauntlet/worktrees` is a symlink or not a real directory inside the
  repository (`ensureWorktreeRoot` in `internal/gitx/worktree.go:169-193`);
  worktree paths are verified to remain strictly inside `.gauntlet/worktrees`,
  leftover checkout directories and pruned metadata are purged on preparation
  and removal, and worktree operations on removed checkouts safely error or
  no-op (`removeWorktreeDir` and `Worktree.Remove` in `internal/gitx/worktree.go:285-308,690-716`).
  The startup sweep is the one path that deletes every entry under that root,
  so it makes the same `ensureWorktreeRoot` proof and skips any entry that is
  not a real directory, leaving a planted link unfollowed
  (`SweepWorktreeRoot` in `internal/gitx/worktree.go`). It runs only where the
  run lock for that directory is already held, which is what keeps another run
  in the same clone from having a checkout there.
- **Sequential in-place with `--commit`/`--push`:** a failed review's retry
  restores a snapshot of the user's checkout taken before the attempt
  (`Snapshot`/`Restore` in `internal/gitx/snapshot.go`), the same rewind
  authority as a worktree reset, bounded to putting back files the user
  already had. The commit step is itself an agent launch. `runCommitStep`
  (`commit.go:140-226`) execs one agent with
  `prompt.CommitPrompt`, which instructs it to run `git commit` and never
  to push; the runner strips injected AI attribution trailers from the new
  commit (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) and pushes itself under `--push`
  (`internal/runner/commit.go:232-240`). Neither trailer stripping nor a clean
  tracked-file status is a security inspection of the committed content, and
  neither prevents the agent from invoking Git itself. Under `--yolo` a
  rejected push escalates to a runner-side `git pull --rebase` and retry; a
  rebase conflict cleans up mid-rebase state via `git rebase --abort`
  (`internal/gitx/branch.go:327-334`) and fails the step rather than returning to the agent. The
  same launch, offered standalone when a dirty tree blocks `--jobs`, is
  gated on explicit consent (`cmd/gauntlet/main.go:975-999`). The prompt is embedded
  text only (`rules/commit.md`), capped at 5 minutes (`commit.go:24`),
  under the same process discipline as any review.
- **Conflict resolution:** `resolveConflict` (`conflict.go:45`) cuts a
  scratch checkout, replays the branch, and launches one agent with
  `ConflictPrompt` (`compose.go:344-355`, rules in `rules/conflict.md`) for up to
  10 minutes (`conflict.go:26`). The prompt lists only sanitization-safe
  paths and forbids git; conflict hints quote branch and message via `runx.ShQuote`
  (`conflict.go:226-229`) to prevent shell injection if pasted; the marker
  scan before the commit covers the resolution's whole commit scope, the
  conflicted paths plus everything the resolver touched in the scratch
  checkout, because the commit stages it whole
  (`commitScope`, `conflict.go:126-150`, fed by `Worktree.CommitScope`,
  `internal/gitx/worktree.go:559-575`); a status that cannot be read falls back
  to the conflicted list. The runner commits and merges the result, or keeps the
  original branch if markers remain. Same
  advisory fence, same permission-bypassed process, narrower file set.
- **`--usage-cmd` (the usage-limit probe):** the operator supplies argv to
  decide whether more reviews start (`internal/runner/usagelimit.go:47-72`).
  `probeUsage` isolates execution from the reviewed directory: `cmd.Dir` is
  set to `os.TempDir()`, `cmd.Env` isolates the child environment with an
  absolute-only PATH (`runx.AbsPATHEnv()`, `probeEnv`), and bare executable
  names are resolved only against absolute PATH entries (`resolveProbe`,
  `runx.LookPath`) (`internal/runner/usagelimit.go:84-113`).
  No shell is added, but an operator-selected relative executable or script path
  (e.g. `./probe.sh` or an explicit path into the tree) can still consume
  repository content. The probe has a 10-second deadline, a 5-second pipe
  wait bound, and 4 KiB per output stream; excess output fails the probe.
  `runx.Bound` provides a process group and cancellation kill (`internal/runx/runx.go:100-123`); a deferred
  group kill also cleans up remaining group members after the direct child exits
  (`internal/runner/usagelimit.go:93-95`). Neither prevents a child from
  deliberately escaping its process group.
  Parsing takes the last non-empty line, accepts a trailing `%`, and rejects
  non-finite, NaN, or out-of-range values (`internal/runner/usagelimit.go:47,125-154`).
  Failure warns once and leaves the usage threshold unenforced; later checks
  may succeed. Spending can continue beyond the intended threshold, though
  this does not enlarge the independently configured runtime budget. A valid
  but false low reading also permits continued scheduling. The probe is
  trusted telemetry, not an authenticated quota service.

**B2, spend (the two budgets).** `--runtime` bounds elapsed time on the
monotonic clock; `--token-budget` counts what the run's reviews report.
Scheduling checks run before loops, reviews, and retries (`budgetExhausted`,
`internal/runner/loop.go:259-272`; `Stats.Tokens`,
`internal/runner/stats.go:395-402`). The token tally spans directories and
survives hot reload. A separate live cap cancels a review that reaches the
whole token ceiling on its own, records `OVER BUDGET`, and prevents retry
(`internal/runner/attempt.go:349-359,402-414`,
`internal/runner/reviewcap.go:40-61`). It trusts machine-readable usage or
transcript counters; prose alone cannot trigger it.

Other reviews in flight may finish, and the cap does not divide the remaining
budget among concurrent reviews. Under-reported or missing tokens undercount
spend, and commit and conflict agent usage is excluded. These controls do not
guarantee a provider spend quota; the runtime scheduling check also does not
shorten a review's own timeout.

Repudiation is only partly addressed: when journaling is available, a run
records seed, schedule, outcomes, and the prompt fingerprint each launch ran under
(`internal/runner/event.go`, [DESIGN.md](DESIGN.md#run-journal)), the
commit step is journaled as its own event with the chosen agent
(`internal/runner/commit.go:224-228`, kind at `internal/runner/event.go:24`).
The runner creates worktree commits using the repository's configured Git
identity. Local `user.name` and `user.email` can override global configuration,
so the author field is not proof of who made the change. Stacked publication
adds a `pull_request` event carrying
the exact head, base, status, and URL; the terminal summary repeats every URL.
The recorded seed is now the run's only one: `effectiveSeed`
(`cmd/gauntlet/reload.go:124-140`) resolves it once, from `--seed` or from the
handoff a hot reload carries, and hands the same number to the schedule and the
suggest shuffle, which draw from it through `SeedOrClock`/`drawIndex`
(`internal/runner/draw.go:35-110`). Before that, each consumer read the clock
itself and a reload handed the suggest step a different seed than the journal
recorded, so the seed in the journal did not replay the run it named. An unset
seed is derived from `time.Now().UnixNano()`, never 0, and is not a secret: it
orders agents and retry backoff, and nothing in the draw is a capability. The
one clock a run reads is that run clock, so the audit trail it writes follows
the seed and the tree rather than the date: the crash checkpoint a resume picks
the run up from is stamped and elapsed from the same handle
(`writeCheckpoints`, `cmd/gauntlet/main.go:860-906`) as the reload handoff and
the run summary, so the evidence a crashed run leaves behind describes the
decisions its seed made rather than when a wall clock happened to be read.

**B3.** Display sanitization addresses terminal escape injection, not the
truth or origin of printable content. The composed prompt shown by
`--show-prompt` (`modes.go:65-66`), the journal replayed by `show`
(`runs.go:604-655`, which spawns no pager), the plain reporter
(`internal/report/report.go:147-152`), and the dashboard feed
(`internal/ui/ui.go:780-808`) pass through the same stripping, which removes ASCII controls,
Unicode formatting/bidi (`Cf`), and line/paragraph separators (`Zl`/`Zp`), and
truncates at whole grapheme cluster boundaries (`internal/normalize/display.go:16-43`).
Stream classification excludes recognized tool/user objects (normalized case-insensitively) before their text reaches the report
parsers, but trusts the producer's role/type fields
(`internal/streamjson/streamjson.go:199,202,217-248`,
`internal/runner/exec.go:449-473`). A compromised agent can still supply false
assistant text or plain-text report lines. Output buffering has line and tail caps, but
`--raw` skips the normalizer's rate limit and duplicate collapsing, retaining `Display`
and the 2000-code-point cap (`internal/runner/exec.go:480-490`). Usage and sink callbacks are serialized
(`sinkMu`, `usageReportMu`) across concurrent stdout/stderr streams (`internal/runner/exec.go:316,359-361,416,433-436`),
and transcript watcher lifecycle cleanup is synchronized with reviews
(`usageWatch.halt`, `internal/runner/attempt.go:359,583-586`).
When readers are stalled by orphaned grandchildren, gauntlet issues a process-group SIGKILL on
drain timeout (`internal/runner/exec.go:605-613`). The one child whose output is not parsed,
the `--semcode` indexer, is filtered by the same `Display` on the way to the
terminal, one `DisplayWriter` per stream (`cmd/gauntlet/semcode.go:87-96`,
`internal/normalize/display.go:128-224`), so no subprocess stream reaches a
terminal unfiltered. Parser bounds do not limit a child's disk writes, network
traffic, CPU, or provider spend. A non-positive process timeout also removes the per-launch
deadline (`internal/runner/exec.go:548-555`). Sanitization says nothing about
completeness: a pipe that breaks part way leaves a tail whose subject, note,
and file notes read exactly like ones the agent printed, and a record built
from it is indistinguishable from a whole one. That is now reported rather
than inferred, and every launch that turns a tail into an answer fails on it
instead of filing it: a review retries (`internal/runner/attempt.go:393-405`),
the commit step refuses to commit on a partial answer
(`internal/runner/commit.go:99-103`), the conflict step keeps the branch
because a half resolution cannot be trusted
(`internal/runner/conflict.go:213-216`), and the suggester passes the turn on
rather than scheduling a review list that was cut off
(`internal/runner/suggest.go:118-125`). The arms come below the cancel and
timeout cases, which close the same pipes deliberately, so a killed review is
still a cancellation. The bound is one error per launch, first one wins
(`recordStreamErr`, `internal/runner/exec.go:291-299`), and the launch's exit
code is untouched (`StreamErr`, `internal/runner/exec.go:118-123`), so a
review that ended early still reports how it ended. This is the repudiation
half of R9, and it is the narrower half: an agent that prints a short answer
on purpose and exits cleanly is not detected by any of it.

**B4.** Update metadata is requested over HTTPS; release asset and checksum downloads
are constrained by `validateAssetURL` (`internal/selfupdate/selfupdate.go:375-394`) to
HTTPS on authorized GitHub release hosts (`github.com`, `api.github.com`,
`objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests), preventing cleartext transfers
or links pointing to untrusted third-party infrastructure. Download requests validate
redirect targets against the same host allowlist and stop after 10 redirects (`client.CheckRedirect`,
`internal/selfupdate/selfupdate.go:150-161`). Downloads are routed through `getAsset`
(`internal/selfupdate/selfupdate.go:415-455`) and compared with SHA-256 entries in `checksums.txt`
using constant-time comparison (`subtle.ConstantTimeCompare`,
`internal/selfupdate/selfupdate.go:232-313,593-614`). Downloaded binaries are
flushed to disk with `Sync()` (`selfupdate.go:280`) before atomic rename.
Download responses are bounded and verified by `fetch` to reject oversized responses
exceeding the limit (`internal/selfupdate/selfupdate.go:574-590`); download and error paths
drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:188,191,558,568-572,585,588,609,612`);
error messages are decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:184,450,557-566`).
The checksum proves the download matches the listing, not that the publisher
is benign: the same publisher controls both. Compromise of the GitHub repo
or its release process yields arbitrary code execution on updating machines,
amplified by hot reload re-exec'ing the new binary mid-run without user
confirmation (`internal/selfupdate/reload.go:258-287`). Auto-update background
goroutines are coordinated via context cancellation and WaitGroup on shutdown
(`cmd/gauntlet/main.go:725-728,784-785`). Signed provenance attestations exist, but
the automatic updater does not verify them. The advisory scanner in CI (`vulnscan.yml`) watches the dependency graph, not this
channel. The `bunx` fallback (and the `--dump-config` probe
that uses the same argv) is a second fetch-and-execute path, from the npm
registry rather than GitHub releases, with no checksum (`internal/agent/agent.go:594-680`,
`internal/agent/dsh.go:79-118`). It fetches one pinned version, so the bytes a
run executes are the ones this tree names rather than the registry's current
`latest`, and the publisher of that version is still trusted
(`DshNpmPackage`, `internal/agent/dsh.go:23-29`). The probe is bounded to 4 MiB output via `runx.Bound`
(`internal/agent/dsh.go:98,158`) and reclaims process groups via `runx.KillGroup` (`dsh.go:159`).
Overlay configurations validate provider and model identifiers
against `dshModelRe` (`internal/agent/dsh.go:272-282`), and overlay keys are percent-encoded so they
name one file in the overlay directory (`internal/agent/dsh.go:228-252`).

**B5.** Secret egress: the updater explicitly attaches `GH_TOKEN` /
`GITHUB_TOKEN` only to requests whose scheme is HTTPS and hostname is
`api.github.com` or `github.com` (`internal/selfupdate/selfupdate.go:103-121`).
Asset and checksum endpoints are separately checked against the HTTPS release host
allowlist (`validateAssetURL`, `internal/selfupdate/selfupdate.go:375-394`).
Git error messages redact embedded basic-auth userinfo credentials (`runx.RedactUserinfo`,
`internal/gitx/exec.go:327-395`) before display or journaling. Agent CLIs receive the inherited
environment and hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal creation requests 0600 files and 0700 directories
(`internal/journal/journal.go:230-285`, `internal/journal/index.go:93-120,660-706`).
A per-review worktree is a second copy of a reviewed repository that may be
private, so `.gauntlet/worktrees`, its components, and each finished checkout
are narrowed to their owner rather than left at the 0755 git and `MkdirAll`
produce, including a directory an earlier run made at a looser umask
(`ownerOnly`, `tightenWorktreeRoot`, `tightenCheckout` in
`internal/gitx/worktree.go:323,344-374`); chmod on a component this account
does not own fails, which is left best-effort for the reason the run lock
treats the file it finds already there the same way;
handoffs use a sibling temp
file, disk flush via `Sync()`, and atomic rename (`internal/selfupdate/reload.go:135-172`).
Stale temp files in the reload state directory are swept
via `gauntlethome.SweepStaleTemps` (`internal/gauntlethome/gauntlethome.go:305-334`,
called at `internal/selfupdate/reload.go:144`). The journal index directory and
the dsh overlay cache are not swept, and this document no longer claims they
are: the index writes through `WriteFileAtomic`, whose temp file is removed by
the rename that follows it, and the dsh overlay is one small YAML file per
provider and model pair, written once per key and never deleted
(`writeDshPatch`, `internal/agent/dsh.go:268-315`), so that directory grows
with the number of distinct pins an operator has used rather than with run
count. What the sweep does is decide removable from the
lstat, not from the directory entry type (`gauntlethome.go:288`): a mount
that reports no entry type hands back `ModeIrregular`, which reads as "not a
regular file" and would leave the leftovers accumulating on a state root that
is exactly such a mount. Stat-ing the entry is free where the type is already
known and keeps a symlink out of reach, because the link itself is what is
stat-ed, so a planted `<prefix>*` symlink is still not removed. Every
rename-based write in those paths is
the one helper (`gauntlethome.WriteFileAtomic`,
`internal/gauntlethome/gauntlethome.go:263-292`), so a partial write is not a
shape each caller can have differently.
Journal entry and index writes are flushed with `Sync()` (`internal/journal/index.go:209,432,976`),
and the directories holding them are recorded rather than merely created
(`MkdirAllPrivateDurable`, `internal/gauntlethome/gauntlethome.go:143-170`),
because a synced file inside a directory its parent was never told about is
still lost to a power cut.
and Close preserves and joins both file and index errors (`internal/journal/journal.go:421-450`).
Index mutations take a cross-process `flock` in a sibling file, and the
acquisition is a bounded poll rather than a blocking `LOCK_EX`
(`lockIndex`, `internal/journal/index.go:56-90`): a peer that dies holding
the lock, or a rebuild on a long history or a network home that takes
longer than 30s, fails the command by name instead of parking
`gauntlet runs`, `gauntlet history`, and the exit-time prune behind a holder
that will never release it. A journal that is still being written is left
where it is: the writer holds a shared `flock` on the stream it appends to
(`lockWriter`, `internal/journal/index.go:93-121`) and the prune takes an
exclusive one per journal it is about to move
(`journalIdle`, `internal/journal/retain.go:66-76`), so a run still open is
evicted by neither the keep window nor the run ID order, and keeps its index
row in the same pass. Without that probe a prune by one run moves the live
event stream of another run sharing the same `GAUNTLET_HOME` into `pruned/`,
which is destruction of evidence for a run that has not finished writing it.
The persisted elapsed-seconds field is decoded in
one place, `humanize.Seconds`
(`internal/humanize/humanize.go:28-41`), which refuses a value at or below
zero, a NaN, an infinity, and a magnitude at or past the nanosecond range rather
than clamping: `json.Unmarshal` hands back whatever the bytes held, and a
corrupt or same-user-planted journal is a file this tool reads, where a
negative or wrapped duration renders as a run that never ended.
Run IDs from CLI flags or events are strictly validated against directory traversal and non-safe
charsets (`ValidRunID`, `internal/journal/history.go:88-104`), and date shards derive from the run ID timestamp
(`shardFromRunID`, `internal/journal/history.go:169-182`). Creation
modes do not tighten permissions on pre-existing paths or authenticate local
state. Journal opens follow existing paths, so a protected state directory is
a precondition, not an enforced property of any `GAUNTLET_HOME` value
(`GAUNTLET_HOME` expands tildes and environment variables, with unresolvable
references rejected at startup by `checkStateHome`, `cmd/gauntlet/flags.go:1041-1079`,
and safe degradation
via `internal/gauntlethome/gauntlethome.go:42-73`).
`LoadState` (`internal/selfupdate/reload.go:185-234`) requires the `.json` extension,
requires absolute paths, verifies clean paths without traversal, opens the file through
`safefile.OpenRead` (`internal/safefile/safefile.go:36-45`), which carries
`O_NOFOLLOW` into the open syscall itself and then refuses a descriptor that is not a
regular file, and bounds reads to 16 MiB (`maxHandoffBytes`, `reload.go:180-183`),
rejecting oversized files before removal. The `os.Lstat`-then-`os.Open` it replaced was
a check and a read in two syscalls, and a reviewed tree that swapped a symlink into the
name between them had its contents unmarshalled into the state a successor resumes from;
the guarded open is what closes that window, and a refusal leaves the file where it was.
`SaveState` (`internal/selfupdate/reload.go:145`)
validates `runID` against directory traversal and path separators. The tree lock's symlink
check is a separate control (`internal/runner/lock.go:85-133`); `Note` normalizes to NFC
before truncation (`internal/runner/lock.go:94-119`); `Note` and `readNote` handle
partial writes and `EINTR` retry loops (`internal/runner/lock.go:99-119,123-139`), and lock release
preserves the open file descriptor flock and truncates the note to prevent
file deletion and inode recycling races (`internal/runner/lock.go:161-175`).
A malicious agent already runs as that operator and can tamper with local evidence.

`--log` is a separate confidentiality and availability boundary: it retains
output that the journal omits. `openLogFile` (`cmd/gauntlet/main.go:908-940`)
refuses a symlink and any non-regular file with `os.Lstat` before opening,
opens with `syscall.O_NOFOLLOW` and 0600, and chmods a surviving regular file
to 0600 before this run writes anything, so a looser umask or an older run's
group- or world-readable file is tightened rather than inherited. What remains
unbounded is the path's surroundings: the parent directory is not confined or
checked for writability by anyone else, and the writer has no size cap and no
rotation. Display sanitization does
not redact arbitrary secrets: output redaction covers known credential shapes only (`outputSink`, `internal/runner/attempt.go:757-780`). Same-user
agents can read or alter the log just as they can the journal.

**B6, destruction.** The state tree is the only place a run deletes, and it
does so on every exit rather than on request. `Prune` (`internal/journal/retain.go:45-56,78-198`)
keeps the newest `keep` journals and index rows by run ID. The id embeds a UTC
start instant, not the time the run finished, so a run that started behind
`keep` newer runs is already outside the window when it closes
(`internal/journal/index.go:697-740`). A journal another process still has
open is the exception: the writer's shared lock keeps it, and its index row,
whatever its id says.
Threats: **repudiation**, in the ordinary sense of a run whose evidence is gone
before anyone asked for it, which is what a default of 200 does to a long-lived
install; **tampering**, because the ordering is a property of the ID string
rather than of anything the journal asserts, so a run whose clock sat behind
another run's is evicted first, and a same-user agent that can write `runs/`
controls which names sort above which; **denial of service**, bounded and
small, since the index rewrite is the only unbounded read in the path
(`readAllIndex` with a 4 MiB line cap, `retain.go:205-245`). What the walk
prevents is the version of this that reaches outside: it takes only entries
the readdir reports as real directories and only `<id>.jsonl` files whose stem
passes `ValidRunID`, so a planted symlinked shard is skipped rather than
followed and `os.Remove` deletes a name, never a link target. A prune that
fails is a warning; the run's own report has already been written
(`cmd/gauntlet/main.go:941-950`).

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, `protocol.ext.allow=never`, `attr.tree` empty, local drivers blanked, the signing program blanked and the signing toggles forced off, and the blanking rebuilt when any config file it was derived from changes (local, plus the system and global files the operator's credential helpers were read from), absolute-only git PATH, `GIT_SSH_COMMAND=ssh` (defaulting on empty/whitespace env, `internal/gitx/exec.go:396-416`); propagated to worktrees and sub-repos via `subRepo`; branch and revision commands separate options with `--` and `--end-of-options`; a merge target is refused before `worktree add`, which takes no `--`, can read a leading dash as an option | `internal/gitx/exec.go:26-33,73-136,233-270,396-416,439-457`, `worktree.go:35-58,321,396,534,735,756,983`, `branch.go:150-177,480-511` |
| A reviewed repository reaching a shell through its own signing config | `gpg.*.program` is blanked with the filter, merge, and diff drivers, and `commit.gpgsign`, `tag.gpgsign`, and `push.gpgsign` are forced false, so the one exec a reviewed config can trigger with no attribute file involved has nothing to trigger it and nothing to run. The blanking is local to the reviewed config, so an operator signing their own commits through a global `gpg.program` still does | `isSignProgram`, `internal/gitx/exec.go:292-305`, `isSignToggle`, `internal/gitx/exec.go:306-321`, in `disableLocalDrivers`, `internal/gitx/exec.go:248-270` |
| Two agents in one reviewed tree, on whichever platform the run happens to be on | the process keeps a registry of the lock files it holds, keyed by the real path, and `Acquire` consults it before the `flock`: a `flock` belongs to the open file description on Linux and to the process on macOS, where a second acquisition converts the lock rather than failing, so without the registry the same double-lock refused to start on one claimed platform and carried on with two sets of agents in one tree on the other | `heldLocks`, `internal/runner/lock.go:43-72`, consulted at `lock.go:106-113`, released at `lock.go:210` |
| A prune moving a live run's journal while this process is still appending to it | `journalIdle` asks the in-process writer registry before it asks the kernel, so the check cannot disagree with the writer on either platform, where the shared `flock` a writer holds is per process on macOS and would report the stream idle | `journalIdle`, `internal/journal/retain.go:66-70`, against `holdsStream`, `internal/journal/index.go:122-134` |
| A journal or index path a reviewed repository committed as a symlink | the two appends that reach an already-existing file open `O_NOFOLLOW` and `O_CLOEXEC`, which is the pair with no `O_EXCL` to prove the create; a `GAUNTLET_HOME` resolving beside the working directory, the degradation path for an unreadable home, is the case where the state tree sits inside the reviewed tree | `openNoFollow`, `internal/journal/journal.go:432-438`, at `journal.go:260` and `internal/journal/index.go:228` |
| Writing outside the repository through a planted `.git` | `.git/info/exclude` append re-checks the parent directory on the path it is about to write, with `os.Lstat` requiring a real directory, because the reviewed tree picks `gitDir` and both `MkdirAll` and `OpenFile` follow a link at any component | `internal/gitx/worktree.go:85-172` |
| Runaway git grandchildren (hooks, merge drivers) holding pipes | process-group SIGKILL on deadline, bounded WaitDelay | `internal/gitx/exec.go:327-395` |
| Planted executables shadowing agents/git/`gh` | cwd-relative PATH entries stripped and child environments isolated with absolute-only PATH via `runx.AbsPATHEnv` for agent, git, git-child, `gh`, and usage probe resolution; executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths; an empty `PATH` falls back to the fixed system prefixes for all of them alike, never to a directory the tree names | `agent.go:203-213`, `internal/gitx/gitx.go:43-58`, `internal/gitx/exec.go:396-416`, `internal/ghx/ghx.go:95-98,253-262`, `usagelimit.go:93-114`, `internal/runner/exec.go:171`, `runx.go:264-340,359-364` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps; stat errors propagated on open regular files; prompt discovery inspects candidates with `os.Lstat` requiring regular files; suggester peeks via `os.OpenRoot`; reload handoff and crash checkpoint opened through `safefile.OpenRead`, so `O_NOFOLLOW` is on the open rather than on an earlier `Lstat` the reviewed tree can race; the tree lock is created 0600 and `Fchmod`ed to 0600 on the descriptor, so a note naming the run, review, and agent CLI is not left group- or world-readable in a directory the reviewed tree picks; a note read is trimmed of a partial UTF-8 rune before it reaches a terminal | `prompt.go:278-329`, `discover.go:244,278`, `internal/gitx/stats.go:98-154`, `runner/lock.go:53-89,123-159`, `evidence/suggest.go:936-945,1047-1062`, `reload.go:196-217`, `checkpoint.go:105-137`, `internal/safefile/safefile.go:36-45` |
| Prompt injection blending into containment rules | begin/end markers, both markers escaped in the body and the `--append` instruction escaped on its own after the body, report-section stripping fails open; the escape is bounded at 8 passes (`markerEscapePasses`) so a body that kept re-forming a marker is delivered rewritten rather than never delivered. The seam that bound was written for does not survive the rewrite order: the closing marker is replaced before the opening one, so dashes the two markers share are consumed by the first replacement rather than re-forming a marker | `escapeMarkers`, `internal/prompt/compose.go:44-73,312-316`; report stripping `compose.go:84-111` |
| An agent writing outside its worktree on a host that cannot confine it | default kernel filesystem write confinement on every launch, Landlock on Linux and Seatbelt on macOS, inherited by children; a setup failure stops the launch rather than falling back to an unrestricted one (`sandboxCommand`), and the host's answer is reported before the run by a capability probe rather than by the launch that failed. Reads and network stay outside this control, `--no-sandbox` opts out of it entirely, and every `--sandbox-write` grant adds a whole subtree to the writable set after a check that only the directory exists | `internal/runner/sandbox.go:47-61`, `internal/runner/sandbox_linux.go`, `internal/runner/sandbox_darwin.go`, `cmd/gauntlet/flags.go:1217-1252`, `cmd/gauntlet/doctor.go:165-178` |
| Operator scope text becoming prompt instructions | `--paths` entries are refused at the flag where the operator can see which one it was, unless each is non-empty, at most 200 runes, and free of backticks, controls, `Cf`/`Zl`/`Zp`, and whitespace other than a plain space; the block names at most 20 entries, re-renders each so a surviving entry equals its own rendering, and counts what it dropped, because a scope that reads shorter than the flag is a wider one | `internal/prompt/compose.go:148-260`, `cmd/gauntlet/flags.go:615,802-819` |
| A usage limit probe that trips after a lane has already taken a review | the lane re-reads the finish flag the probe set and drops its unstarted queue, so the remaining `--jobs` reviews do not start against a window the run decided was spent | `internal/runner/attempt.go:36-64`, `internal/runner/loop.go:147` |
| A hostile untracked file name reaching the run journal | every git path a run note prints goes through `safePaths` before the humanize list renders it, the same filter the dirty-tree note and a stack's path list use; the lane's untracked note was the one that did not | `internal/runner/sanitize.go:21-27`, `internal/runner/loop.go:50,54` |
| Injection via suggest catalog | description *and* name sanitize, fence-neutralizing, 200-rune cap, NFC normalization before truncation, strict suggestion grammar checked against known set, reasons capped to the same budget; agent weights are integers 0–3 parsed before reason sanitization, and duplicate names cannot add passes; the unknown half of a triage answer is capped at 20 names of 200 runes with the remainder counted and reported as truncated, so a confused or hostile agent cannot leave a megabyte of retained strings or a line of log with no end | `compose.go:396-429,462-519` |
| Injection via `Signals:` into the file-signal suggester | known kinds, charset, 12×40-rune caps; `mark:` values search file heads, not executed; a head that could not be read whole is left unscanned, because a partial prefix either declares a mark the file does not carry or hides one past the truncation, and a wrong signal steers which reviews auto-run | `prompt.go:199-232`, `evidence/suggest.go:711-734,740-749` |
| Terminal-driven or spoofed output, including prompt preview, journal replay, reporter, and dashboard | `Display`/`Sanitize` strip escapes, controls, bidi, and separators, and repair bytes that are not valid UTF-8 so one file name renders the same whatever sits beside it; width cap; rate limit; duplicate collapse; grapheme-preserving truncation, which repairs invalid UTF-8 before it cuts so a width cut cannot return half a decoded replacement character; ANSI CSI sequences terminated on standard final characters in token width calculation; usage and sink callbacks serialized | `internal/normalize/display.go:16-43`, `modes.go:65-66`, `runs.go:637,654`, `internal/report/report.go:147-152`, `internal/ui/ui.go:780-808`, `internal/ui/clip.go:75-117`, `internal/runner/exec.go:48,54,316,359-361,416,433-436,480-490`, `runner.go:26` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before a terminal write; CRLF stripped | `internal/gitx/status.go:68,179,223-285`, `internal/runner/sanitize.go:21-27`, `lock.go:22-28`, `conflict.go:152-224` |
| Hostile file names forging conflict-prompt instructions | drop unsanitary paths (control/Cf, `RESOLVE:`, fence-closer); 1024-rune path cap; 50-file cap (over-cap skips the launch); list fenced in `<files>`; markers still block the merge | `compose.go:344-355`, `conflict.go:152-224` |
| A conflict marker left outside the conflicted file set reaching the merge | the marker scan covers the commit's whole scope, the conflicted paths plus every tracked and untracked path `CommitAll` would stage, and a file the scan cannot read counts as unresolved rather than clean; an unreadable status falls back to the narrower conflicted list | `commitScope`, `internal/runner/conflict.go:87,126-150`, `Worktree.CommitScope`, `internal/gitx/worktree.go:703-716` |
| Shell injection in conflict resolution hints | branch names and commit messages quoted with POSIX escaping via `runx.ShQuote` in `conflictHint` | `internal/runner/conflict.go:226-229`, `internal/runx/runx.go:422-430` |
| Model output written as a commit subject | ASCII controls, Unicode Cf (bidi), Zl/Zp stripped; grapheme cluster preserved; NFC normalization before truncation; 100-rune cap where the line is parsed and 72 where it is committed, so a long line is clipped before it reaches history, a PR title, and a merge message; a subject carrying an attribution credit line is dropped for the generated one; one line | `internal/agent/notes.go:82-116`, `internal/runner/subject.go:40-72,224-235` |
| A commit subject read back out of history reaching a PR title | one choke point, `clipSubject`, sanitizes and clips whatever a subject's source was: generated from the changed files, parsed out of agent output, or read back with `CommitSubject` during a stack recovery, where an agent that committed on its own, a resolved conflict, or the operator wrote it. The layer's PR title comes from that read, so a title crafted in a branch left behind by a killed run is sanitized on the way to GitHub, and a source added later inherits the guarantee rather than needing its own call | `internal/runner/subject.go:224-235`, `internal/runner/stack.go:527-535` |
| A generated branch name cut inside a multi-byte character | the abbreviated commit id a branch or probe name carries is clipped by runes at a grapheme boundary, not by bytes, so a base ref a hostile repository reported cannot land a half-encoded character in a branch name, a probe ref, or an error message. Both cuts repair bytes that are not valid UTF-8 first, so the clip cannot itself cut a decoded U+FFFD in half: a cut is on a rune boundary by construction, and the boundary of a repaired byte is the one before the replacement character, which is the half of a character every width measurement downstream would then have to guess at | `normalize.Clip` at `internal/runner/stack.go:167,184,601`, `shortTipLen`/`shortDisambiguatorLen` at `internal/runner/stack.go:197-199`, `normalize.Repair` called by `Truncate` and `Clip` at `internal/normalize/display.go:95,276` |
| A commit subject an agent supplies crediting itself or a model CLI | a narrow regex over the parsed subject (`attributionRe`, `internal/runner/subject.go:54`) drops a trailer or phrase that credits a tool, replacing it with the generated subject; the word alone is not a match, so a change that genuinely concerns one of those tools still describes itself; the trailer sweep on the finished message (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) does not reach the subject, which is why the check is here | `internal/runner/subject.go:40-63` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks with UTF-8 rune boundary preservation, trailing CR stripped, feed error trigram prefilter, bounded tail buffers (1 MiB suggest tail) | `internal/runner/exec.go:40-48,440-447,605-613,676-705,809-823`, `internal/normalize/normalize.go:302-326` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:33-37`, `agent.go:481-489` |
| Agent cancellation filed as a failure | a review cancelled while it is running records `interrupted` rather than the failure it was in the middle of, so a cancellation cannot be filed as a failed review with its tokens charged to the run's ceiling | `interruptedOnCancel`, `internal/runner/attempt.go:494-505` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, deferred SIGKILL on normal exit to clean up orphaned grandchildren, drain timeout process group SIGKILL escalation to unblock stuck readers, stdin null device except ACP (a pipe carrying only the runner's protocol messages), own session (no controlling terminal, so Ctrl-C cannot be disabled from inside an agent), drain grace for stuck grandchildren, ACP exit grace (1s) then SIGKILL for servers that ignore closed stdin | `internal/runner/exec.go:28-54,174-221,548-613,765-786` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; runner-side publication is workflow separation, not removal of agent credentials | `internal/runner/commit.go:24,163-249` |
| Conflict step running away | separate 10-minute cap, same process discipline; unresolved markers keep the branch | `conflict.go:26,37-90` |
| Indexer binary shadowed by a planted `./semcode-index` | resolved with `agent.Resolve` (`pathNoCWD`), argv-only, absolute-only PATH in the child, 30-minute per-directory cap, deferred process-group SIGKILL | `cmd/gauntlet/semcode.go:82-104`, `internal/agent/agent.go:204-253`, `internal/runx/runx.go:64-85,100-123` |
| File names from a hostile tree reaching the terminal through the indexer | one `normalize.DisplayWriter` per stream, each complete line through `Display`, partial line held to a 1 MiB cap and flushed after the child is reaped; no child stream is written straight to the terminal any more | `cmd/gauntlet/semcode.go:87-96`, `internal/normalize/display.go:145-243` |
| A secret reaching a pasted `doctor` transcript | the environment report prints a name the table marks secret as `(set)`, never as a value, and reads only documented names; every environment name in the source is pinned to the table or to a stated reason it is internal | `cmd/gauntlet/doctor.go:375-401`, `cmd/gauntlet/help.go:179-192`, `cmd/gauntlet/env_surface_test.go:31-33,35-59` |
| Unbounded or unauthorized downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; HTTPS and authorized GitHub release host check (`validateAssetURL`); unified `getAsset` check; HTTP redirects re-verify host allowlist and stop after 10 redirects; fetch strictly rejects responses exceeding limit; download and error responses drain HTTP bodies up to limits via `drainBody`; error messages decoded up to 4 KiB on failure via `responseError`; digest-shaped entries only; constant-time checksum comparison; binary flushed with `Sync()`; verify-before-rename, atomic replace | `selfupdate.go:111,152-164,172,184,188,191,232,277,280,378-396,420-461,534-550,557-566,596-619` |
| An update that installs bytes and leaves no way back | the replaced binary is copied first, through a temp file in the same directory, `Sync`ed, chmod'ed to its own mode, renamed onto `<binary>.previous`, and the directory entry made durable, and any failure in that sequence refuses the update instead of installing without a rollback path. It is recovery, not verification: the rollback preserves the current binary, then moves this copy into place without re-verifying it | `keepPrevious`, `internal/selfupdate/selfupdate.go:328-373`, `selfupdate.go:296`, [docs/CLI.md](CLI.md#updating) |
| A line count a hostile tree chose in its own shortstat | `parseCount` accepts digits only, and refuses anything over `maxPlausibleCount` (2^40) or `MaxInt`, so a 20-digit figure cannot arrive as the clamped `Atoi` result and go negative once the untracked counts are added to it | `internal/gitx/stats.go:38-50` |
| Markdown a reviewed repository or an agent wrote reaching a pull-request body | control characters stripped, flattened to one line, NFC-normalized, length-bounded, and inline delimiters escaped (`\`, `[`, `]`, `<`, `>`, `*`), so a link, an autolink, a raw tag, or an emphasis span in a note or a project `Summary:` line renders as the text it is. Every prose position, the title, the overview, and the declared scope alike, also has its backticks replaced, for the opposite reason: in prose one opens a code span that swallows what follows, and each of those positions is untrusted text that went out with the span still open | `mdText`/`escapeInline`/`mdCode`, `internal/runner/prbody.go:150-203` |
| A review that reports a note per file building a pull-request overview out of bounds | the notes are joined only while the render's own 400-rune bound is unspent, counting the separators against it, so the string is bounded by what the render would keep anyway rather than by how much the agent reported | `internal/runner/stack.go:651-680` |
| Partial binary observed by reload | two immediate identical stat readings reject visible changes, but do not prove completeness or authenticity; safe replacement depends on atomic writers and disk flushes | `internal/selfupdate/reload.go:87-134`; updater rename and sync at `internal/selfupdate/selfupdate.go:286-316` |
| Concurrent agents corrupting one tree | flock per directory with inode preservation across releases; partial-write and EINTR retry handling on note I/O; parallelism only across directories or with worktree isolation + serialized merges; worktree paths confined to `.gauntlet/worktrees`, worktree mutex synchronization, orphaned directory cleanup on add and remove; a conflict is resolved in a scratch checkout or keeps its branch | `runner/lock.go:53-175`, `worktree.go:195-208,285-308,489-518,605-639`, [DESIGN.md](DESIGN.md#concurrency-model) |
| Option injection and revision collisions in git commands | branch and revision arguments separated with `--` and `--end-of-options` across rev-parse, log, diff, merge, squash, rebase, delete, rename, trailer stripping, and reset; `worktree add` takes no `--`, so a merge target is shape-validated with `ValidateBranchName` before it is passed; a GitHub remote whose host or repository name begins with a dash is refused at parse, before the selector reaches `gh repo view`'s positional | `internal/gitx/status.go:20,39`, `internal/gitx/trailers.go:82`, `internal/gitx/branch.go:111,157,201,253,355,368,442,465,509,530,546,568,579`, `internal/gitx/worktree.go:345,420,558,679,700,759,792,826,1056`, `internal/gitx/snapshot.go:41`, `internal/ghx/ghx.go:49-93` |
| Accidental launch of unconstrained run from empty filter | `blocked` returns the reason before the flag is set, and Enter consults it first, so a filter matching zero reviews cannot compose a run of all of them; the same notice is rendered in the pane | `blocked`, `internal/ui/pick_view.go:210-218`, rendered at `pick_view.go:331,530,627`, checked at `internal/ui/pick.go:360-364` |
| Reviewed tree defining its own agents | `CustomFilePath` empty without state root; argv is exec, not shell; argv, model, effort, stream, continue reject blank elements; NFC normalization of keys and names; case-insensitive duplicate field detection, single `{prompt}` placeholder requirement, `{prompt}` forbidden in model/effort/stream/continue, `{model}` and `{effort}` restricted to appropriate fields, tilde and environment variable expansion in executable `cmd[0]`, UTF-8 BOM stripped, non-empty model and usage roots, usage.suffix cannot be whitespace only, a name or note holding control or formatting characters refused, and a command-line `--agent-cmd` overriding whatever the file defined under that name | `custom.go:100-297,410-437,495-688`, `agent.go:305-316`, `cmd/gauntlet/flags.go:485-545` |
| Directory traversal or poisoned journal run IDs | run ID length bounded (<= 128), charset-restricted (`[a-zA-Z0-9_.-]`), ".." rejected; date sharding derived from run ID timestamp; journal writes flushed via `Sync()` | `internal/journal/journal.go:154`, `internal/journal/index.go:209,432,976`, `internal/journal/history.go:88-104,173` |
| History prune reaching outside the state tree | the walk yields only real shard directories and only `<id>.jsonl` names that pass `ValidRunID`; the index row is rewritten before its journal is unlinked, under the index lock, with a 4 MiB line cap; `keep <= 0` deletes nothing | `internal/journal/retain.go:45-56,78-198`, `internal/journal/index.go:697-740` |
| Embedded basic-auth credentials in remote URLs | userinfo stripped from git stderr strings before errors are returned, printed, or journaled | `runx.RedactUserinfo`, `internal/gitx/exec.go:327-395` |
| A credential a child process printed reaching an error, a report, or the journal | every piece of child output that becomes an error string passes through `FirstLine`, which strips userinfo and then `RedactSecrets`: a value assigned to a name that says it is a credential, the value after a flag from an enumerated list of names that carry one, the value of an `Authorization` header, and a token carrying a published fixed prefix. The result is idempotent, and a value under eight characters is left alone as a flag or a placeholder. The redaction is bounded by what its value class matches: it stops at the first space, and each rule recognizes only the shape it names | `RedactSecrets`, `internal/runx/runx.go:206-256`; `secretFlagRe`, `runx.go:156-172`; `secretAuthRe`, `runx.go:174-194`; `FirstLine`, `runx.go:258-265`; reached from `internal/runner/exec.go:866`, `internal/gitx/branch.go:298,305`, `internal/ghx/ghx.go:220,248,271`, `internal/runner/usagelimit.go:100,140,149`, `internal/agent/dsh.go:146`, `internal/sbom/license.go:135` |
| A machine below the git floor, discovered only by a failing command | `doctor` reads `git --version` through the same `PATH` memo a run's first git call uses, bounded to 256 bytes and run with an absolute-only `PATH`, and compares it to a floor of 2.24, the release that introduced `--end-of-options` and `git switch`. An unreadable or unparseable version is reported as unknown, not as a pass | `Version`, `internal/gitx/version.go:39-55`; `BelowFloor`, `version.go:61-70`; `cmd/gauntlet/doctor.go:147-162` |
| An archive of the run history that is short, read as a whole one | journal writes go out whole-line, and `Inspect` counts the journals that end without the newline every event ends with, through the symlink-refusing reader, on `doctor` and on `runs --json`. It is a report rather than a control: the half-line such a file ends on is not JSON, so every reader drops it and the count is where an operator learns the run replays shorter than it ran | `lineBuffer`, `internal/journal/journal.go:362-421`; `Status.Truncated`, `internal/journal/status.go:31-37`; `Inspect`, `internal/journal/status.go:42-97` (`truncated`, `internal/journal/status.go:115-130`); `cmd/gauntlet/doctor.go:314`, `cmd/gauntlet/runs.go:44,50` |
| A failed backup run destroying the archive it was replacing | the recipe builds into a temporary named after the archive and the job's own process id beside the destination and renames it into place only after `tar -tzf` reads the copy back, so the destination holds the previous archive or the new one and never half of one, and a job that fails exits non-zero with the previous copy untouched. `tar -czf "$archive"` alone truncates the destination before the first byte of the new archive lands, which loses the only good copy to a full disk or a `kill`, and one temporary would also let two overlapping jobs rename a file the other is still appending to. The rename is a directory entry, so the job then records the directory that holds the archive and exits non-zero when that record fails: without it a power cut loses the copy the job just checked, with the previous archive already replaced. The checks are the failing, unrecorded, and overlapping jobs the suite runs against that recipe | [docs/RUNS.md](RUNS.md#backup-and-restore); `TestBackupRecipeKeepsTheLastGoodArchive`, `cmd/gauntlet/backuprecipe_test.go:204-285`; `TestBackupRecipeRefusesAnUnrecordedArchive`, `cmd/gauntlet/backuprecipe_test.go:288-328` |
| A restored unlanded review no run can find | a review left on an unmerged lane branch is a commit in the reviewed repository and no copy of the state tree holds it, so the bundle is the only backup of it. `git clone <bundle>` alone materializes every fetched ref under `refs/remotes/origin/`, while `LaneBranches` reads local `refs/heads/gauntlet/`, so the documented restore now fetches the `gauntlet/` and `review/` namespaces back onto local branches and the suite runs the block against a repository holding an unlanded review, failing unless the restored repository holds it on a local branch at the commit it was taken from | [docs/RUNS.md](RUNS.md#backup-and-restore); `TestBundleRecipeRestoresUnlandedReviewsAsLocalBranches`, `cmd/gauntlet/bundlerecipe_test.go:74-171`; `LaneBranches`, `internal/gitx/branch.go:123` |
| A token figure an agent printed beside its own usage counters | the result is billed from the machine-readable usage envelope whenever the agent reports one, and falls back to the larger of the live and tail scrapings only when it reports none, so a model that prints a figure beside a reported counter cannot inflate what `--token-budget` is charged | `internal/runner/exec.go:371-392,628-637`; `maxUsage`, `exec.go:654-663` |
| Known-vulnerable dependencies shipping to users | govulncheck weekly and on dependency changes in CI | `.github/workflows/vulnscan.yml` |
| Local state and secrets in the `make repro` archive | the members come from `git ls-files --cached --others --exclude-standard`, so every `.gitignore` entry is out of it, `.env` is one of them, and tests read `.gitignore` and fail on an entry whose rule no longer bites, so the list cannot drift by forgetting a new build output; the archive lives under `$(HOME)/.cache/gauntlet/repro` and is removed by an exit trap, and the target now also runs the release inventory generator and a checksum tool inside each copy, so the `go list` and module-cache grant reads the sbom row describes have a second invocation site on a developer's machine | `Makefile:876-915`, `cmd/gauntlet/makefile_test.go:1029-1108`, `.gitignore:10` |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | [DESIGN.md](DESIGN.md#run-journal), `journal/` |
| A wedged peer parking the journal index lock forever | the cross-process index lock is a bounded poll, `LOCK_EX` with `LOCK_NB` retried to a 30s deadline, and the failure names the lock file, so `gauntlet runs`, `gauntlet history`, and the exit-time prune report a held lock instead of waiting on a peer that will never release it. The holder walks the whole journal tree on a rebuild or a prune, so on a long history or a network home the wait is not instant and the bound is not a formality | `lockIndex`, `internal/journal/index.go:56-90`, used at `index.go:88` |
| A duplicated `.git/info/exclude` entry from two writers | the read-modify-write is held under one exclusive `flock` on a sibling lock file, so the second writer reads the first's lines and appends nothing; `--dirs` over two checkouts of one repository and a second gauntlet process both reach the same file | `lockExclude`, `internal/gitx/worktree.go:174-216`; `TestExcludeOwnArtifactsWritesOnceUnderConcurrentWriters`, `internal/gitx/gitx_test.go` |
| Silent loss of a `.git/info/exclude` entry | the append returns its failures, both the write and the close, and the run logs them; a short write counts as a failure, since the next run's substring check would not match a truncated line and would append the same entry again on every run. The exclusion is still best effort in the sense that nothing downstream depends on it, and the run continues | `internal/gitx/worktree.go:85-172`, `internal/runner/runner.go:306-311` |
| A discarded stack layer leaving a branch nothing names | `DiscardCurrent` reports a branch git refuses to delete instead of dropping the error; the name is cleared either way, so a leftover branch on the remote used to survive a pass in which no later cleanup looks for it | `internal/gitx/worktree.go:487-512` |
| A release inventory that describes a different build than the one shipped | `Merge` refuses a module path recorded at two versions across a release's binaries, and the command refuses a binary whose main module path is not the first one's, so a release assembled from two trees fails instead of shipping a merged or arbitrarily chosen version | `internal/sbom/sbom.go:127-152`, `cmd/sbom/main.go:62-131` |
| A prunes or a dashboard run stranding a forwarder on the bus | a run that returns without ever entering the dashboard's `Run` releases the forwarder, which otherwise parks on the program's unbuffered message channel holding the bus subscription and every event it still carries; `Release` is idempotent and kills the program before joining | `Dashboard.Release`, `internal/ui/ui.go:444-456`, deferred at `cmd/gauntlet/main.go:569` |
| The operator's account name reaching text kept on disk | the journal's free-text event field and the index's argv are shortened to `~` before they are written, and only there: the live terminal and the `--log` file keep the full path, so nothing a reader of the run needs is lost and what a backup or a `runs --json` consumer carries names no account. An occurrence is rewritten only at a whole path component, so one user's home does not shorten another's | `journaledEvent`, `cmd/gauntlet/main.go:1075-1086`, `journaledArgs`, `main.go:1088-1111`, `RedactHome`, `internal/normalize/display.go:116-154` |
| An unbounded read of a file the reviewed tree chooses | `.git/info/exclude` is read only to check for two short entries and is capped at 1 MiB rather than taken whole; the module-cache grant the release inventory reads is capped at 1 MiB, the `go list` listing at 8 MiB, and that child's pipe wait at 10s with the process group killed with it | `maxExcludeBytes`, `internal/gitx/worktree.go:724-728`, read at `worktree.go:146-150`; `internal/sbom/license.go:41-51,112-163` |
| One checkout's directory reported gone while it is still on disk | `Remove` clears the handle's directory only after the removal returned, so a failed removal leaves the caller the only record of where the checkout is; the git handle is built with the checkout rather than on first use, removing the check-then-act a second lane would take reaching the same directory | `internal/gitx/worktree.go:690-716`, `newWorktree`, `internal/gitx/worktree.go:43-58` |
| A cut-short agent transcript filed as a finished review, a commit, a merge, or a schedule | the read error is separated from `io.EOF` and carried on the result beside the exit code, so the exit status still says how the agent ended while every caller that parses an answer answers for the truncation: a review is a failure that retries, the commit step refuses, the conflict step keeps the branch, and the suggester hands the turn to the next agent. The arms sit below cancel and timeout, which break the same pipes deliberately | `scanLines`, `internal/runner/exec.go:665-735`; `recordStreamErr`, `internal/runner/exec.go:291-299`; `StreamErr`, `internal/runner/exec.go:118-123`; `internal/runner/attempt.go:393-405`, `internal/runner/commit.go:99-103`, `internal/runner/conflict.go:213-216`, `internal/runner/suggest.go:118-125` |
| A commit decided on an unread answer about what is staged | both readers of the index go through one function, so `CommitAll` and `Merge` cannot disagree about a `diff --cached --quiet` exit, and an exit other than staged or clean is an error rather than an answer | `nothingStaged`, `internal/gitx/status.go:139-155`; `internal/gitx/worktree.go:533`, `internal/gitx/branch.go:171` |

Remaining reliance on the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md. The kernel
sandbox backs filesystem write restrictions, while reads, network operations,
and actions within writable roots still depend on those rules.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential, and
   the provenance attestation a release now publishes
   (`actions/attest-build-provenance`, one statement per entry in
   `dist/checksums.txt`) is read by a person running `gh attestation verify`,
   not by `update`. Making the automatic path check it needs a Sigstore
   verifier inside the client, which is a new dependency against the default
   of no new dependency; otherwise document the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:774`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`internal/agent/agent.go:594-680`). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:321-340`); a
   `dsh:<model>` pin additionally execs the same argv as `--dump-config`
   (`internal/agent/dsh.go:79-118`). The spec is now pinned to one exact
   version, so the gap left is the registry's: the publisher is trusted and
   nothing is checksummed or signed. Documentation should say plainly that naming `dsh`
   without the launcher installs and runs an npm package, including at
   probe time.
3. **No SECURITY.md.** There is no documented path from "vulnerability
   reported" to "fix shipped": no disclosure contact or response process. The
   [supported-version policy](../CONTRIBUTING.md#supported-versions) covers only
   the latest release. A disclosure policy still requires an owner decision.
4. **R6, resource and budget enforcement.** The launch path sets no OS
   resource limits (`internal/runner/exec.go:217-221`); the optional usage
   threshold is checked between reviews and fails open on probe errors
   (`internal/runner/usagelimit.go:47-72,93-114`); `--token-budget` counts what
   reviews report and excludes commit and conflict launches. Scheduling checks
   stop new work (`internal/runner/loop.go:259-272`); a separate live cap cancels
   a review that reaches the whole ceiling on its own, using machine-readable
   or transcript counters (`internal/runner/attempt.go:349-359`,
   `internal/runner/reviewcap.go:40-61`). It does not allocate the remaining
   budget across concurrent reviews. None is a hard spend quota.
5. **Secrets hygiene around spawned agents.** Gauntlet inherits the full
   operator environment to every agent; a scrubbed env or documented
   container workflow would shrink R3. Design decision, not a bug.
6. **R9, bounded audit trail.** `--keep-runs` moves old journals into an equally
   bounded quarantine and removes their index rows at run end
   (`internal/journal/retain.go:45-56,78-198`). Quarantine eviction unlinks files
   and reports the loss on stderr (`cmd/gauntlet/main.go:1158-1165`). Ordering
   uses the start instant in the run ID; an open journal is kept, but a closed
   long run may already be outside the window. Set the bound high enough for
   incident retention, or use `0` to keep everything.
7. **Journal path confinement is by name, not by owner.** `ValidRunID`
   (`internal/journal/history.go:88-104`) and the walk in `Prune` bound what a
   run can read and delete, but nothing on this boundary authenticates the
   state directory: a run ID collision or a planted file under a real shard is
   decided by string shape and timestamps. Run IDs now carry the whole pid
   (`runIDFor`, `internal/journal/journal.go:86-90`), which removes the
   truncation collision, but the ordering property R9 names is unchanged.
   A protected `GAUNTLET_HOME` remains a precondition the code cannot enforce.
8. **The developer build surface is not in the runtime model.** `make repro`
   archives the whole working tree, so anything a developer has in their
   checkout that is not ignored lands under `$HOME/.cache/gauntlet/repro` for
   the length of the build (`Makefile:876-915`). The members are git's
   ignore-aware listing, which covers the entries
   in `.gitignore`, a derivation, not a guarantee: an untracked
   credentials file, a private key, or a second `.env` under another name is
   copied with no redaction and no scan. This is a developer convenience
   target on this repository, not a path any reviewed repository reaches, and
   the copied trees are removed on exit.
9. **The shipped inventory is a claim, not a signature.** Every release now
   ships `dist/sbom.json`, generated from the built binaries' own build info
   (`internal/sbom/sbom.go:98-125`, `cmd/sbom/main.go:91-128`). It is the
   dependency view a scanner and a release-page reader need, and it is worth
   having. It is not a second integrity control for R2: the publisher supplies
   both the binary and the document, `Merge` only refuses a module path
   recorded at two versions across the release's binaries, and nothing binds
   the binaries that were inventoried to the binaries `checksums.txt` covers
   beyond the same `dist/gauntlet_*` glob. A reader who treats the document as
   evidence of what shipped is reading a publisher claim. Signed provenance
   attestations cover the binaries, but the automatic updater does not verify
   them or bind the SBOM to them. Its write also lacks the hardening the runtime `--log`
   destination has: `os.WriteFile` at 0644 (`cmd/sbom/main.go:126`) follows a
   symlink at the destination and leaves a pre-existing file's mode alone,
   where the log destination is refused as a symlink or non-regular file and
   opened `O_NOFOLLOW` at 0600 (`cmd/gauntlet/main.go:908-940`). The rest of
   the release target's output is no tighter: `checksums.txt` comes from a
   shell redirect over `dist` (`Makefile:800-805`), which follows a symlink
   too. Exploitability is low, since reaching either needs a compromised
   release workspace, which is the same attacker R2 assumes; it is recorded
   because a reader should not learn two different answers to "can a planted
   path redirect a write" from one repository.

10. **The rollback copy is trusted by name.** `<binary>.previous` is written
    atomically and refused-if-unwritable, but is not reverified before rollback
    (`keepPrevious`, `internal/selfupdate/selfupdate.go:328-373`). The
    [rollback recipe](CLI.md#updating) preserves the current binary, then moves
    the previous copy into place without a digest, signature, or version check.
    Restoring the previously installed version can also restore vulnerabilities
    fixed by the current version. The writer is not the weak part: the copy
    is made through a temp file, `Sync`ed, chmod'ed to the replaced binary's
    own mode, renamed into place, and its directory entry made durable, and an
    update that cannot do it is refused. What would close the read side is a
    `gauntlet rollback` that verifies the copy against the published
    `checksums.txt` or the provenance attestation before renaming it, which is
    new behavior rather than a fix, and is left to sec-review and a decision.

## Abuse cases

The tool has one authenticated user (the operator), so the hostile actor is
the reviewed repository's author:

- **Scope-laundering through a wrapper.** A CI wrapper builds `--paths` from a
  file list or a changed-file feed rather than from a person typing it, so a
  name carrying a line of prose, a backtick, or a marker arrives as flag text
  and lands in a prompt an agent runs with permissions bypassed. The entry is
  refused where the operator can see which one it was
  (`cmd/gauntlet/flags.go:615,802-819`), the block names at most 20 entries and
  counts the rest rather than reading narrower than the flag
  (`internal/prompt/compose.go:148-260`), and an entry that survives is
  re-rendered so it equals its own rendering. What remains is the design the
  prompt already states: the scope is prompt-enforced, and an agent that
  ignores it is stopped by nothing.
- **Steering the fix fleet.** Plant `sec-review.md` in the tree with the
  bundled name and different content; discovery warns but obeys
  (`discover.go:104-111`). The planted body rides past the markers into an
  agent told to skip permissions or auto-approve. Enabling path: `Discover`
  -> `Compose` -> `BuildCmd` -> `runProc`.
- **Exfiltration-by-push.** Repository instructions steer an agent into
  including sensitive content in a change. With `--commit --push`, the agent
  commits and the runner pushes after tracked-file and attribution checks
  (`internal/runner/commit.go:232-240`). Those checks do not inspect the diff
  for secrets or authorize its content. The same-user agent can also publish
  directly despite the prompt prohibition. Containment is advisory, not a
  credential or network restriction (`internal/runner/exec.go:217-221`).
- **Consent-surfaced commit.** Refuse `--jobs` on a dirty tree and gauntlet
  offers to hand that tree, unreviewed, to an agent that commits it; `--yes`
  or `--yolo` on the original command is that consent, so an operator who
  scripts those flags has pre-approved agent-authored commits of whatever
  the reviews left behind (`cmd/gauntlet/main.go:975-999`).
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:457-495`) bound it to reviews that exist
  in the discovered set, including the attacker's own. It can also steer repeat
  weights and therefore cost; the parser bounds agent-requested repeats to
  three per review and duplicates cannot increase that cap
  (`internal/prompt/compose.go:495-516`).
- **Signals: steering.** A planted `Signals:` line on a project prompt is
  parsed into the file-signal suggester (`prompt.go:199-232`,
  `matchDeclared` in `evidence/suggest.go:711-734`). Charset, count, and length are
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
  `internal/runner/exec.go:449-473`). Regression coverage pins this class in
  `internal/streamjson/streamjson_test.go`; unmarked output or an assistant
  repeating the text remains unauthenticated.

- **Rollback by name.** An actor who can write the install directory (the
  publisher R2 already assumes, or a local account sharing it, or an agent that
  reached the user's `PATH`) plants `<binary>.previous` before the operator runs
  the [rollback recipe](CLI.md#updating) after a bad release. After preserving
  the current binary, the move installs the planted copy: nothing
  between the copy and the rename re-verifies it, and the copy's own integrity
  was established for a download that happened earlier
  (`keepPrevious`, `internal/selfupdate/selfupdate.go:328-373`; the instruction
  at [docs/CLI.md](CLI.md#updating)). The same file is a downgrade with no attacker at
  all: rolling back restores the version before the last update, so a fix for
  a named vulnerability is undone by following the documentation.

- **Evidence eviction.** The reviewed tree names a planted prompt that plants
  run journals. A same-user agent can write `<id>.jsonl` files under
  `~/.gauntlet/runs/`, and `Prune` orders by the ID's embedded timestamp, so
  names it fabricates displace real runs from the newest 200 without ever
  touching a file outside the state tree
  (`internal/journal/retain.go:45-56,78-198`, `internal/journal/index.go:697-740`).
  What it cannot do is delete anything the operator pointed at elsewhere: the
  walk refuses a shard that is not a real directory and a stem that is not a
  valid run ID.

- **Slug collision across reviews.** `--jobs N` gives each review a lane
  branch, a checkout directory, and a merge scratch directory, all named from
  the review's slug (`LaneBranch`, `internal/gitx/branch.go:66-85`;
  `worktreeDir`, `internal/gitx/worktree.go:304-314`; the merge scratch path at
  `internal/gitx/branch.go:259`). `BranchSlug` (`internal/gitx/worktree.go:1140`)
  maps a review name onto a ref fragment, and while it kept only
  `[a-zA-Z0-9_-]`, every rune outside that class became a hyphen: a stem
  written in Japanese, Korean, Russian, or Arabic slugged to the same empty
  string the name's own ASCII tail produced, so three unrelated reviews in one
  tree took one lane branch, one worktree, and one scratch directory, and an
  operator read one review's work under another's name. That is
  misattribution of evidence rather than an escape: nothing leaves the state
  tree, but the record a review leaves no longer says which review made it.
  It is fixed by keeping any Unicode letter or digit and normalizing to NFC
  first, so a decomposed spelling on a macOS filesystem and a composed one are
  one name rather than two, and `ValidateBranchName` plus `check-ref-format`
  still refuse whatever survives. What a name can still do is collide with
  another name in the same tree, which is why the lane tag rather than the slug
  carries the run, loop, and lane identity (`LaneBranch`, `branch.go:66-68`).

## Document status

- SECURITY.md: absent. The README trust model names the controls this
  document ranks: default Landlock or Seatbelt writes, permission prompts
  disabled, `O_NOFOLLOW` prompt reads, a `PATH` without cwd-relative entries,
  the safe git overlay including the reviewed repository's signing program,
  display sanitization, and the per-process lock registry beside `flock`.
- Response readiness: the journal supports run reconstruction, not a complete
  security audit, and it is now bounded: `--keep-runs` evicts the oldest rows on
  every run's exit, default 200 (`internal/journal/retain.go:45-56,78-198`). Agent
  output and live usage events are explicitly excluded
  (`cmd/gauntlet/main.go:525-529`, with the rule in `Droppable`, `internal/runner/event.go:196-200`), writes are buffered, and write failures are
  retained for reporting at close (`internal/journal/journal.go:399-420`, with the errors joined at `Close`, `journal.go:513-542`).
  What is reconstructable is now said apart from what is not: an index row
  whose journal is gone is named as a run no listing can rebuild, so an
  investigation is not sent after a repair that cannot happen
  (`unmatchedRuns`, `cmd/gauntlet/doctor.go:448-455`).
  It does not authenticate agent claims or record every child action; same-user
  agents can alter local evidence. A disclosure contact and response process
  remain undocumented; the supported-version policy is in
  [CONTRIBUTING.md](../CONTRIBUTING.md#supported-versions).
  Restoring the tree is the other half of that readiness, and it is the one an
  incident actually exercises: the documented drill in [docs/RUNS.md](RUNS.md#backup-and-restore) is now
  executed rather than illustrated, against an archive the same repository's
  backup recipe wrote, and it compares the restored tree's `history` counts
  against the tree the copy was taken from and runs the whole block a second
  time into the same directory
  (`TestRestoreRecipeReadsBackAnArchiveTheBackupRecipeWrote`,
  `cmd/gauntlet/restorerecipe_test.go:130-143`). So a restore that extracted a
  file and lost the history in it, or that only works once, fails the suite
  rather than an operator's first attempt. What it cannot cover is the
  credential and state boundaries B5 and B6 name: an archive taken off the machine is
  only as protected as the destination it is copied to, which is a decision no
  code here makes.
