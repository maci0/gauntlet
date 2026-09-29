# gauntlet threat model

What can be attacked, what it costs, and what stands in the way, for one
thing: a local CLI that dispatches review prompts to installed AI coding
agents which edit the working tree, typically with their permission systems
bypassed or auto-approved. This document is the systemic view; individual
vulnerability findings belong to sec-review and are recorded here only as
threats.

Last reviewed: 2026-09-30 against commit 0e6754c. This pass read the
thirteen commits since b49e8cc. The commits close one code-execution path the
model had listed only as a category and correct two platform claims the
platform matrix runs on. The execution path is signing: a reviewed repository
carrying `gpg.program` beside `commit.gpgSign` reached a shell on the first
commit any review made, and signing is the one exec a reviewed config triggers
with no attribute file involved, so the same blanking the filter, merge, and
diff drivers already got now covers `gpg.*.program` and forces
`commit.gpgsign`, `tag.gpgsign`, and `push.gpgsign` false
(`isSignProgram`, `internal/gitx/exec.go:292-305`; `isSignToggle`,
`internal/gitx/exec.go:306-321`, in `disableLocalDrivers`,
`internal/gitx/exec.go:248-270`). The blanking
is local to the reviewed config, so an operator signing through a global
`gpg.program` is untouched, and a review's commits, which this tool writes
rather than the operator, are no longer signed on a reviewed repository's
demand. The two platform claims: `flock` is per open file description on Linux
and per process on macOS, so the second acquisition of one tree's lock
conflict-checked on one and converted the lock on the other, and a run that
locked one tree twice carried on with two sets of agents in one tree on macOS
instead of refusing to start. The process now keeps a registry of the locks it
holds, keyed by the lock file's real path, and `Acquire` consults it before the
`flock` (`heldLocks`, `internal/runner/lock.go:43-72`, consulted at
`lock.go:106-113`), which makes the lock mean the same thing on both. The same
asymmetry reached the prune: `journalIdle` asked the kernel whether a journal
had a writer, and on macOS the answer was no for a run this process was
appending to, so a prune could move a live run's evidence into `pruned/`. It
now asks the in-process writer registry first (`journalIdle`,
`internal/journal/retain.go:66-70`, against `holdsStream`,
`internal/journal/index.go:122-134`), which is the one check that cannot
disagree about which process is writing.

Three more controls are carried, none of which changes a numbered row. The
journal and the index are now opened `O_NOFOLLOW` on the append that reaches an
already-existing file, which is the append with no `O_EXCL` to prove the
create; a `GAUNTLET_HOME` resolving beside the working directory, the
degradation path for an unreadable home, puts the state tree inside the
reviewed repository, where a committed `.gauntlet/runs/<shard>/<id>.jsonl`
link would otherwise receive the run's paths, prompt names, and agent output
(`openNoFollow`, `internal/journal/journal.go:286-301`, at `journal.go:260`
and `internal/journal/index.go:228`). The dashboard's unmerged-branch list is
capped at 16 with the dropped count stated wherever the list is drawn, because
a conflict stays on disk until a human takes it and a `--max-loops 0` run has
no bound of its own (`maxConflicts`, `internal/ui/ui.go:96`, applied at
`ui.go:668-675`); every conflict is still in the journal and the end-of-run
summary, so this bounds a screen and not the record. And an index rebuild now
counts a `review_end` that repeats in one stream once, because a hot-reload
successor restarts loop numbering at 1 and re-runs what its predecessor was
interrupted on, so one review was being reported as two with its tokens and
lines counted twice against the run's ceiling (`summarizeFile`,
`internal/journal/index.go:999`, the claim at `index.go:1037-1051`). The last
two are display and accounting bounds on the run's own evidence; neither is a
mitigation for an attacker. The
remaining six commits in the range change nothing this model names: an
injectable run clock and retry wait, a small-terminal size, a blocked run that
stays visible, agent-name normalization before case folding, and a flag parser
that reports a missing `--prompt-dir` or `--dir` the way it reports every other
bad flag value.

Last reviewed previously: 2026-09-29 against commit b49e8cc. That pass read
the twenty-one commits since 77fa164 and entered the two surfaces among them
the model had never named, and carried the controls the rest added. The first
surface is what the journal stores about the operator: a git error or a path
error carries the absolute path of the reviewed tree into `Event.Text`, and an
argument naming a directory, a log file, or an agent binary carries it into
the index row, so the text a backup, a sync, or a `gauntlet runs --json`
consumer took away held the account name of the OS user who ran it. Both
free-text fields now shorten the home directory to `~` on the way to disk and
only there, so the live terminal and the `--log` file keep the full path
(`journaledEvent`, `cmd/gauntlet/main.go:874-882`, `journaledArgs`,
`main.go:885-893`; `RedactHome`, `internal/normalize/display.go:108-143`,
which rewrites an occurrence only at a whole path component so `/home/alice`
does not shorten `/home/alicia`). The second is the license inventory the
release generator reads: `go list` and each module's grant file are a
subprocess and a whole-file read that the entry-point row described as one
bounded call, and they now carry the same bounds as any other subprocess in
this tree, a 10s pipe wait, an 8 MiB listing cap, a 1 MiB grant cap, and
`runx` for the process group and the deadline kill
(`internal/sbom/license.go:32-45,116-144`).

Four controls those commits added are carried. `.git/info/exclude` is read to
check for two short entries, and the reviewed tree picks the file, so the read
is now capped rather than whole (`maxExcludeBytes`,
`internal/gitx/worktree.go:576-580`, read at `worktree.go:104-112`). A checkout
handle keeps its directory until the directory is gone, so a failed removal
cannot report success over a checkout still on disk, and its git handle is
built at creation rather than on first use, which removes the check-then-act a
second lane would take reaching one checkout (`Worktree.Remove`,
`worktree.go:681-714`; `newWorktree`, `worktree.go:43-58`). The two places that
commit on what is staged now read the index through one function, so they
cannot disagree about what a `diff --cached --quiet` exit means, and any other
outcome is an error rather than an answer
(`nothingStaged`, `internal/gitx/status.go:139-155`; called from
`worktree.go:497-503` and `internal/gitx/branch.go:111-117`). The stacked-PR
overview stops joining notes once the render's own bound is spent, so a review
that reported a note per file builds a bounded string rather than megabytes
joined to be cut (`internal/runner/stack.go:651-680`).

One risk row narrowed. `--token-budget` is now one ceiling for the run rather
than one per directory: a shared tally every directory's runner adds to, a
hot reload's predecessor included, checked before a loop, before a review is
taken, before a lane starts, before a retry or its backoff, and before the next
stack step (`Tokens`, `internal/runner/stats.go:104-133,346-356`;
`budgetExhausted`, `internal/runner/loop.go:223-243`, consulted at
`loop.go:259`, `internal/runner/runner.go:340`, `internal/runner/attempt.go:41,556,570`,
and `internal/runner/stack.go:284`). What it does not change is what R6 already
said: the counters are agent-reported, the commit and conflict launches are
outside the sum, a review in flight runs to its own timeout, and the budget
stops what starts next rather than what already ran. No row was added and none
was closed.

Last reviewed previously: 2026-09-28 against commit 77fa164. That pass read the
thirty-six commits since 2ec5135 and found no new boundary, no new asset, and
no new numbered risk. What it found was drift: the citations into
`internal/agent/agent.go`, `internal/agent/custom.go`,
`cmd/gauntlet/flags.go`, `cmd/gauntlet/runs.go`, `internal/gitx/gitx.go`, and
`internal/gauntlethome/gauntlethome.go` were re-anchored against the code they
name, and two controls those commits changed are now described in the shape the
code has. The first is the precedence between a definitions file and
`--agent-cmd`: the command line unregisters whatever the file and the shipped
definitions said under that name and installs its own, so the file is the base
of the registry rather than its limit (`configureAgents`,
`cmd/gauntlet/flags.go:421-480`). The second is what the temp sweep treats as
removable, now decided from the lstat rather than the directory entry type,
which restores the sweep on a state root that is a mount reporting no entry
type and still leaves a planted symlink alone, because the link itself is what
is stat-ed (`gauntlethome.SweepStaleTemps`,
`internal/gauntlethome/gauntlethome.go:180-208`). Neither adds a row: the
first is operator-supplied on both sides, the second narrows nothing that
already held.

Last reviewed previously: 2026-09-28 against commit 2ec5135. This pass read the
twenty-seven commits since 41faffe and entered the two surfaces among them the
model had never named. An update now keeps the binary it replaced, beside it
as `<binary>.previous` (`keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`,
called from `applyTo`, `selfupdate.go:292`, before the rename, and kept the same
way by `make install`, `Makefile:416`), and the documented rollback is a
`mv` of that copy back over the one in use (`docs/CLI.md:162-167`). It narrows
nothing in R2 and widens it by one file: the copy is the previous release's
bytes, verified once at download and not again at restore, and the rollback
trusts a name in the install directory, which is the directory the attacker R2
already assumes. The second surface is `gauntlet runs --json`
(`writeRunsJSON`, `cmd/gauntlet/runs.go:133-176`, flag at `flags.go:413`), the
first output of this tool meant for a program rather than a person: index rows
carrying the reviewed tree's directory names, the run's own argv, the state
root, the pruned list, and the counts of what the state tree holds. The encoder escapes `<`, `>`, `&`, and every control
byte, so no escape sequence crosses as raw text, and no `Display` runs over a
field, so what is left is whatever a consumer does with a value it decoded.
Four controls these commits added are carried: inline
Markdown delimiters are escaped in a pull-request body, so an agent note or a
project `Summary:` line can no longer close a link, an autolink, or a raw tag in
the one document a person reads about the run
(`escapeInline`, `internal/runner/prbody.go:179-203`, which the B2 publication
note had described as flattening only); a line count read out of `git diff
--shortstat` is refused unless it parses and clears `maxPlausibleCount`, so a
hostile tree cannot turn a clamped `Atoi` result into a negative in the journal
(`parseCount`, `internal/gitx/stats.go:38-50`); a bundled prompt may no longer
edit a value it has no source for, which is an auto-fix allowance crossing
B1->B2 as prose (`internal/prompt/prompts/`); and `make release` refuses the
`dev` default `VERSION` carries, so a rehearsed release cannot publish complete
assets at a version no tag names (`release-version`, `Makefile:669-675`).
Nothing in the numbered risk table gained a row.

The same pass closed a gap between what CONTRIBUTING requires of a release
and what the release job checks, and narrowed a credential's reach in the
pull-request suite. The release job now refuses a tag whose commit
`origin/main` does not carry
(`.github/workflows/release.yml:132-140`), before it builds anything: a tag is
the only review the bytes behind it get, since the job runs `make release`
(the suite, `check`) but not the pull-request gates, and both `update` and the
README install serve the tag. The check is an ancestry test against a full
checkout, so an annotated tag is read through to its commit, and it is a
claim about which commits reached a pull request, not an integrity control:
it narrows nothing in the numbered risk table. It does not cover a rewritten
`main`, which the branch protection rules on the host do, and which cannot be
read from the tree. Every job in every workflow clears `GITHUB_TOKEN` and
`GH_TOKEN` (`.github/workflows/ci.yml:48-50` for the pull-request `test` job,
and `vulnscan.yml:44-46` for the scanner), the pattern the release job
already used for its write token. The rule is the same everywhere because the
exposure is: each of those jobs runs code that inherits its environment, and
the widest of them is the `test` job, which starts the agent CLIs the suite
finds on `PATH`, followed by the jobs that resolve and execute tools from PyPI
and the module proxy. No step outside the release job's Publish needs the
token, the workflows grant it `contents: read`, and the release token can
write only inside the step that publishes, so this removes a handle rather
than a capability.

Last reviewed previously: 2026-09-28 against commit 41faffe. That pass added one
surface and one check. The release job now signs a build-provenance
attestation per entry in `dist/checksums.txt`
(`.github/workflows/release.yml:168-171`), which the workflow can do because
it now holds `id-token: write` and `attestations: write` alongside
`contents: write`; signing needs the runner's OIDC identity, and the
statement is a claim about which commit built which bytes, not a control on
what a release serves. R2 therefore stands, narrowed only for a reader who
runs `gh attestation verify`: `update` still installs what the release page
serves. `make check` gained `go mod tidy -diff` (`Makefile:252-262`, run
from `Makefile:298`), so a require the source no longer imports is caught
before it ships; the manifest is an input to the build, and a stale line in
it is a module fetched and hashed on every build. Nothing in the numbered
risk table gained a row.

Last reviewed previously: 2026-09-27 against commit e35371d. That pass read the
twenty-three commits since the previous baseline (c36fa56) and entered the one
entry point among them the model had never named: the operator's `--paths`
scope, whose entries are pasted into the review prompt as instructions
(`pathsNote`, `internal/prompt/compose.go:212-245`). A wrapper that builds the
flag from a file list rather than from a person typing it is free text reaching
an agent with its permissions bypassed, so an entry is now refused at the flag
when it carries a line break, a control character, a backtick, or more than
200 runes (`PathEntrySafe`, `internal/prompt/compose.go:156-175`, called from
`finishFlags`, `cmd/gauntlet/flags.go:640-654`), and an entry that cannot be
named as itself is left out of the scope block with the count reported, because
a scope that reads shorter than the flag is a wider one
(`PathsNamed`, `internal/prompt/compose.go:201-210`; the refusal note in
`pathsNote`). The prompt fence gained the same treatment: a review body whose
markers share their dashes re-formed one at the seam of a single rewrite pass,
so the agent read the review as ending mid-body with the body's own text where
the ground rules belong. The rewrite now repeats until no marker is left
(`escapeMarkers`, `internal/prompt/compose.go:44-61`). Three controls the other
commits added are carried: a lane drops its unstarted queue once the usage
probe trips, where it used to take a review off the queue and launch it
without re-reading the flag (`runLane`, `internal/runner/attempt.go:36-64`),
which is R6's subject and not a closure of it; untracked paths in a lane run's
start-of-run note are sanitized like every other git path the package prints
(`safePaths`, `internal/runner/sanitize.go:14-19`, called at
`internal/runner/loop.go:50,54`); and an index rebuild keeps the row a journal
it cannot summarize instead of dropping the only copy of the arguments, exit
code, and elapsed time the journal does not carry
(`rebuildIndex`, `internal/journal/index.go:864-873`), which is the repudiation
half of R9. Nothing in the numbered risk table changed. Owner and review cadence
are organizational decisions; none is assigned here.

Last reviewed previously: 2026-09-27 against commit c36fa56. That pass read the
fourteen commits since ef6eb5c and entered the one surface they added, a
release-time inventory generator the document had never covered:
`cmd/sbom` reads the modules out of each built binary's own build info and
writes the CycloneDX document a release ships
(`cmd/sbom/main.go:47-86`, `internal/sbom/sbom.go:98-127`, run by the
release target, `Makefile:547`). What it is, and is not, is now stated where
the publishing boundary is described and in the gaps: an inventory is a claim
the compiler stamped into a file, so it narrows R2's dependency picture without
being an integrity control for it, and the writer takes an operator-supplied
`-o` path with `os.WriteFile` at 0644, following a symlink and leaving a
pre-existing file's mode alone, where the runtime `--log` path refuses both
(`cmd/sbom/main.go:86` against `cmd/gauntlet/main.go:713-733). Four controls
that landed in these commits are now carried: `clipSubject` is the one function
every commit subject passes through, including one read back out of history
during a stack recovery, where an agent that committed on its own, a resolved
conflict, or the operator wrote it
(`internal/runner/subject.go:229-235`, `internal/runner/stack.go:527-535`);
the cross-process index lock is now a bounded wait rather than an unbounded
`LOCK_EX` that could park `runs`, `history`, and the exit-time prune behind a
peer that never released it (`lockIndex`, `internal/journal/index.go:56-90`);
a failed or partial append to `.git/info/exclude` is reported rather than
swallowed, including the short write that would otherwise make every
subsequent run append the same entry again (`internal/gitx/worktree.go:91-155`,
reported by the run at `internal/runner/runner.go:281-286`); and a branch the
stack could not delete is reported instead of dropped, because by then the
name it was named for is gone (`internal/gitx/worktree.go:470-497`). The
citations that moved under their sentences were re-anchored, among them the
`make release` pointer the Makefile's own edit had left stale, which was
holding `TestDocsPointAtTheMakefileLineTheyName` red. Nothing in the numbered
risk table changed: the new surface is a build-time tool on this repository,
not a path a reviewed repository reaches, and the four controls are local
rather than the closure of a numbered risk. Owner and review cadence are
organizational decisions; none is assigned here.

The previous passes' baselines follow, retained rather than re-verified.

Last reviewed previously: 2026-09-27 against commit c63fa85. That pass read
the six commits since the previous baseline (4cdb72c). One adds a control,
recorded in B6 below: a per-review checkout is a second copy of a possibly
private repository, so the checkout and the scratch root under it are narrowed
to their owner. The rest change no risk, no entry point, and no gap. The build
commit re-anchored the Makefile pointers in the same change that moved the
Makefile, so both anchors were stale on arrival and
`TestDocsPointAtTheMakefileLineTheyName` was red; they read `Makefile:547` and
`Makefile:628-653` now.

Last reviewed previously: 2026-09-27 against commit 4cdb72c. That pass read
the eighteen commits since ef6eb5c and changed no risk, no entry point, and no
gap; it re-anchored citations the build commits moved. Three commits in that
range changed the Makefile's shape, and the pointers into it were not carried
along: `make release` sits at `Makefile:547` and `make repro` at
`Makefile:628-653`, so every reference to the earlier anchors was stale and
`TestDocsPointAtTheMakefileLineTheyName` was red on both. That test only
checked the pointers naming `make release` and `GOVULNCHECK_VERSION`, which is
why the other five went unnoticed; it now covers the `repro` target and its
member list too. The `make repro` entry point and gap added at the ef6eb5c
baseline still stand.

Last reviewed previously: 2026-09-27 against commit ef6eb5c. That pass read
the eight commits between 93b004c and that commit and changed three things in
the model. One entry point and one gap were added for a surface the document
had never covered: `make repro` archives the whole working tree, so anything a
developer has in their checkout that is not in `.gitignore` is copied under
`$HOME/.cache/gauntlet/repro` for the length of the build
(`Makefile:628-653`; the archive's members now come from git's ignore-aware
listing and tests hold the recipe to it, `cmd/gauntlet/makefile_test.go:1029-1108`). Two controls that
landed since the last baseline are now carried: a commit subject an agent
supplies is dropped for the generated one when it credits a model or an agent
CLI (`attributionRe`, `internal/runner/subject.go:55`), because the trailer
sweep on the finished message never reached the subject; and the
conflict-resolution marker scan now covers the commit's whole scope rather
than only the paths git reported as conflicted (`commitScope`,
`internal/runner/conflict.go:102-119`), so a marker the resolver left in a file
it was not asked about blocks the merge. Citations that moved with those
commits were re-anchored (`internal/runner/conflict.go`, `subject.go`,
`internal/agent/notes.go`, `internal/runx/runx.go`), and one pointer was
corrected: `make release` is at `Makefile:547`, not 413, which had left
`TestDocsPointAtTheMakefileLineTheyName` failing.

Last reviewed previously: 2026-09-27 against commit a6e6c7f. That pass
covered the twelve commits between dd793d8 and that commit. One named gap
closed: `--semcode`
indexer output now passes through the same display filter as every other child
stream (`normalize.NewDisplayWriter`, `internal/normalize/display.go:128-224`,
wired per stream in `runIndexer`, `cmd/gauntlet/semcode.go:86-108`), so R8 is
no longer a gap. One new surface: `doctor` reports which documented environment
variables this process saw, printing a secret-bearing name as `(set)` and never
as a value (`envSettingLines`, `cmd/gauntlet/doctor.go:314-336`; the table it
reads is `helpEnvVars`, `cmd/gauntlet/help.go:177-189`). New controls verified
against the code: the writer is per stream and flushed after `Run` joins the
copy goroutines (`internal/normalize/display.go:153-161`, `semcode.go:92-100`), with a 1 MiB cap
on the partial line it holds so an endless line cannot grow the buffer
(`maxPendingBytes`, chr(96) . "internal/normalize/display.go:110" . chr(96)); invalid UTF-8 is repaired on the
fast path of `Sanitize` too, so a hostile file name no longer renders
differently depending on what sits beside it (`stripControl`,
`normalize.go:268-284`); run IDs keep the whole pid, so two live processes
cannot share a journal file (`runIDFor`, `internal/journal/journal.go:86-90);
atomic writes are one helper with a `Sync()` and a rename
(`gauntlethome.WriteFileAtomic`, `internal/gauntlethome/gauntlethome.go:138-161`),
which `SaveState`, the dsh overlay patch, and the journal index all call, so a
partial write is no longer a shape each of the three can have differently; the
picker's `q` arms before it discards a composed run, like the dashboard's
(`quitArmed`, `internal/ui/pick.go:311-325`); and every environment name the
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
(`cmd/gauntlet/runs.go:362-395`), imports no `os/exec` and no `runx`, and no
`PAGER` or `LESS` lookup survives anywhere in the tree, so the mitigation named a
process that does not exist. The sanitization it was hanging on is real and is
still there (`normalize.Sanitize`, `cmd/gauntlet/runs.go:386`); the pager is not,
and the three PATH claims that listed a pager alongside the agents, git, `gh`,
and the usage probe no longer count it. The moved citations are the other half:
splitting the dashboard and launcher into state and view halves
(`internal/ui/view.go`, `internal/ui/pick_view.go`) took about 700 lines out of
each of `ui.go` and `pick.go`, so every pointer into those two files was
re-anchored on the symbol rather than the old number, including the picker's
launch gate, which now reads its reason from `blocked`
(`internal/ui/pick_view.go:145-160`, consulted at `internal/ui/pick.go:356-360`)
instead of where it used to sit. Two surfaces this pass added: `runs --limit N`,
which is an unbounded number reaching a local allocation and is now capped by
`maxTailHint` before it reserves anything (`internal/journal/index.go:1104-1150`),
and the run seed, which is resolved once per run and carried across a hot
reload, so the seed the journal records now replays the draws it names
(`cmd/gauntlet/reload.go:124-140`, `internal/runner/draw.go:35-110`). The
2026-09-26 and 2026-09-27 baselines for
everything else are retained rather than re-verified; this is not a full
assurance claim. Owner and review cadence are organizational decisions; none is
assigned here.

## Risk-ranked summary

| # | Risk | Boundary | Status |
|---|---|---|---|
| R1 | Prompt injection from the reviewed tree drives an agent running with bypassed or auto-approved permissions | B1 -> B2 | Accepted by design; containment is advisory text plus process discipline, never an OS sandbox |
| R2 | Self-update integrity rests on TLS and repository ownership; `checksums.txt` authenticates nothing beyond transport consistency, and hot reload execve's the replaced binary automatically. The `sbom.json` every release now ships does not narrow this: it is generated from the binaries it describes, by the same publisher, and `update` does not read it. Neither does the provenance attestation every release now publishes: it records which workflow and commit built each binary, so a reader can check it, but the automatic path still installs what the release page serves. The `<binary>.previous` copy each update leaves behind is the same channel read backwards: the documented rollback renames it into place with nothing checked, and it is the version before the last update, so a rollback also undoes whatever that version fixed | B4 | Named gap, narrowed for a reader checking, not for the automatic path |
| R3 | A prompt-injected or compromised agent reads every secret its user can: environment-inherited API keys, agent config stores, SSH keys, `~/.netrc` | B2/B5 | Consequence of R1; containerization is the documented answer (DESIGN.md non-goals) |
| R4 | Confidentiality of reviewed source: agents send code to third-party model APIs over the network | B2 | Inherent to the tool's purpose; users must know it |
| R5 | `dsh` without a launcher on PATH falls back to `bunx`, fetching `@deepseek-ai/dsh` from the npm registry and executing it; a `dsh:<model>` pin also runs that argv as `--dump-config` before the review | B4 | Named gap (deliberate feature, unreviewed supply-chain hop) |
| R6 | Agent resource consumption or a failed usage probe exhausts host capacity or provider budget | B2 | High when reviewing hostile content: parser caps are not CPU, disk, network, or spend quotas. `--token-budget` now bounds the tokens the run's own reviews report as one ceiling for the whole run rather than one per directory: a shared tally every directory's runner adds to and a hot reload's predecessor carries, checked before a loop, a review, a lane, a retry or its backoff, and the next stack step (`Tokens`, `internal/runner/stats.go:104-133,346-356`; `budgetExhausted`, `internal/runner/loop.go:223-243`, at `loop.go:262`, `internal/runner/runner.go:340`, `internal/runner/attempt.go:41,556,570`, `internal/runner/stack.go:284`). It is still a scheduling bound, not a spend quota: the counters are agent-reported, the commit and conflict launches are excluded from the sum, it is checked between steps rather than inside one, it stops what starts next rather than what already ran, and a review that under-reports its tokens lowers nothing else; a lane now drops its unstarted queue the moment the usage probe trips, so `--jobs N` cannot spend the window on reviews taken off the queue before the flag was read (`runLane`, `internal/runner/attempt.go:36-64`); the usage limit probe runs isolated from the repository but fails open on errors (`internal/runner/exec.go:100-143`, `internal/runner/usagelimit.go:47-72,93-114`) |
| R7 | `--log` persists source or credentials quoted in output at an operator-selected path | B3/B6 | Conditional on enabling logging; a symlink or non-regular destination is refused, the open carries `O_NOFOLLOW` and 0600 with a post-open chmod, but content is not secret-redacted and the parent directory is not confined (`openLogFile`, `cmd/gauntlet/main.go:713-733) |
| R8 | `--semcode` indexer output carrying file names from a hostile tree reaches the operator's terminal | B1 -> B3 | Closed: both of the indexer's streams pass through `normalize.DisplayWriter`, one writer per stream, flushed after the child exits (`cmd/gauntlet/semcode.go:86-108`, `internal/normalize/display.go:145-243`) |
| R9 | The run history is bounded and evicted on every run (`--keep-runs`, default 200), and eviction is ordered by the run ID's timestamp rather than by a protected property of the journal | B6 | Partly closed. Eviction moves the journal to `pruned/` and `gauntlet runs --restore` moves it back, so a wrong bound is recoverable until the same number of newer runs has replaced it; what remains is a run older than that, which no bound distinguishes. The move is confined to real shard directories holding validated run IDs, and the run just finished is the newest row, so a skewing clock or a hostile planted file under `runs/` is what puts evidence out of reach (`internal/journal/retain.go:45-56,78-198`, `internal/journal/quarantine.go`, `internal/journal/index.go:697-740`). An index rebuild now keeps the row the index already holds for a journal it cannot summarize, because the argv, exit code, and elapsed time live on that row alone and nothing reconstructs them afterwards (`rebuildIndex`, `internal/journal/index.go:864-873`), so a corrupt or truncated journal no longer spends the only copy of its own summary |

The order reflects reachability and blast radius, not measured likelihood.
R1/R3/R4 require only content reaching a launched agent; R2 requires control
of the update publisher or executable path; R5 requires selecting the fallback.
No server authentication boundary is claimed here: the operator's OS account
supplies child-process authority (`internal/runner/exec.go:107-111`), and
publication uses that account's Git credentials (`internal/runner/commit.go:102-110`).

## Assets

- **Working-tree integrity** of the reviewed repository. Agents edit it in
  place; who commits depends on the mode (see B2). Corruption here destroys
  uncommitted user work, which is why `--jobs N>1` demands a clean tree
  (DESIGN.md "Isolated parallel reviews", rule 1), and why the runner rewinds
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
  rollback (`internal/selfupdate/selfupdate.go:322-367`,
  `docs/CLI.md:162-167`).
- **Host availability**: reviews run unbounded CPU/network inside the timeout
  window (`internal/runner/exec.go`).
- **Audit trail**: the journal under `~/.gauntlet`
  (`internal/journal/journal.go`) and the commits each run leaves behind.
  Optional `--log` files also retain displayed output, potentially including
  source and credentials quoted by an agent (`openLogFile`, `cmd/gauntlet/main.go:713-733).

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
  `--push`), one launch per loop receives commit instructions; the runner
  verifies tracked-file cleanliness, strips attribution trailers, and performs
  the requested push (`internal/runner/commit.go:160-245`). This divides
  workflow responsibility, not OS authority: the agent still inherits the
  user's credentials and can disobey the prompt's no-push instruction. The same
  hand-off exists outside a loop: when `--jobs` refuses a dirty tree,
  `commitFirst` offers to give the uncommitted work to one agent, consented
  by `--yes`/`--yolo` or an interactive confirmation, never on an unattended
  guess (`cmd/gauntlet/main.go:960-984, executed by `runner.CommitNow`,
  `internal/runner/commit.go:54`). A third launch, `resolveConflict`
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
  `show`'s journal replay, `cmd/gauntlet/runs.go:386`, which writes through a
  `bufio.Writer` to the caller's stdout and spawns nothing, so there is no
  pager environment to isolate) and the dashboard feed
  (`internal/ui/ui.go:780-808`).
- **B4, internet <-> binary.** GitHub releases API and release assets reach
  the self-update path; what lands on disk is executed by hot reload. The
  publishing side of the same channel is GitHub Actions: `release.yml` builds
  and uploads the assets `update` verifies (the write token is in the
  environment only for that upload), and `vulnscan.yml` runs
  govulncheck weekly and on `go.mod`/`go.sum` pull-request changes and main
  pushes (`.github/workflows/vulnscan.yml:10-22`). Actions are commit-pinned,
  the runner uses `ubuntu-24.04`, and checkout disables persisted credentials.
  The scanner is version-pinned through `GOVULNCHECK_VERSION` in `Makefile:51`,
  invoked by `make vuln` (`.github/workflows/vulnscan.yml:35-58`). Release and
  checksum downloads enforce `validateAssetURL` across HTTP redirects and cap
  redirects at 10 (`client.CheckRedirect`, `internal/selfupdate/selfupdate.go:145-157`).
  Release checksum verification uses constant-time comparison
  (`subtle.ConstantTimeCompare`, `internal/selfupdate/selfupdate.go:273`), and the
  downloaded binary is flushed with `Sync()` before atomic replacement (`selfupdate.go:276`).
  Every release now also ships `dist/sbom.json`, the CycloneDX inventory of the
  modules the built binaries link (`internal/sbom/sbom.go`, written by the
  release target at `Makefile:547`, uploaded beside the binaries,
  `.github/workflows/release.yml:195-201`). It travels this boundary and
  `update` does not read it: the artifact is for a scanner and for whoever
  reads a release page. Nothing authenticates it beyond the `checksums.txt`
  the binaries travel under, and it is generated from those binaries' own
  build info, so whoever can replace a binary can replace the inventory that
  describes it. It narrows what a reader has to guess about a release's
  dependency surface; it is not a second integrity control.
  The binaries themselves carry one more record the inventory does not: the
  release workflow signs a build-provenance attestation per entry in
  `dist/checksums.txt` (`.github/workflows/release.yml:168-171`), stating the
  workflow, the tag, and the commit that produced the bytes, verifiable with
  `gh attestation verify`. The claim it makes is about provenance, not
  integrity of transport, and `update` does not check it, so the gap recorded
  as R2 stands for the automatic path.
  The install side of this boundary writes one more file. Before the rename
  that installs a release, the binary being replaced is copied to
  `<binary>.previous` in the same directory, at its own mode, through a
  temp file and a directory sync, and an update that cannot write that copy is
  refused rather than performed without a way back
  (`keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`,
  `selfupdate.go:292`; `make install` keeps the same copy, `Makefile:416`).
  A release whose bytes already match the installed binary installs nothing
  (`fileSum` against the verified download, `applyTo`): a repeat of an update
  would otherwise put the release it just installed in the copy and leave the
  rollback naming the same version the rollback replaces. `make install` skips
  the copy the same way, with `cmp` on the built binary.
  The copy is verified once, as part of the download that produced the binary
  it replaced. The rollback the documentation gives is a rename, so nothing
  checks it a second time: `docs/CLI.md:162-167` tells the reader to
  `mv <binary>.previous <binary>`, and the file that move installs is whatever
  is under that name. The copy is also a downgrade path, since it is the
  version before the last update and no further back, and a rollback undoes
  whatever that version fixed.
  Stacked publication is a second internet path: the `gh` CLI, resolved from an
  absolute-only PATH (`internal/ghx/ghx.go:100-103, 258-267`), using the operator's own
  `gh` credentials, not the `GH_TOKEN` gauntlet reads for self-update.
- **B5, secrets <-> processes.** Secrets enter from the operator environment
  and agent config stores; they leave toward GitHub (only when `GH_TOKEN` or
  `GITHUB_TOKEN` is set, and only on gauntlet's own HTTPS client) and toward
  each agent's model provider. Git stderr is redacted for embedded userinfo
  credentials before errors are printed or journaled (`internal/gitx/exec.go:327-395`).
  `gh` and `git push` use whatever credentials those tools already have.
- **B6, gauntlet <-> local state.** `~/.gauntlet` (or `GAUNTLET_HOME`): the
  JSONL journal, hot-reload handoff files, `agents.json`. This is also the one
  boundary where the run deletes: `--keep-runs` prunes the history at the end
  of every run (`internal/journal/retain.go:45-56,78-198`). Journal paths are
  guarded by run ID validation (`validRunID`, `internal/journal/history.go:88-104`)
  and date-sharded from run ID timestamps (`shardFromRunID`, `internal/journal/history.go:167`).
  The state tree outlives the run and is the file a backup, a sync, or a
  `runs --json` consumer takes away, so the free text written into it names no
  OS account: the event's text and the index's argv have the home directory
  shortened to `~` on the way to disk and only there (`journaledEvent`,
  `cmd/gauntlet/main.go:874-882`, `journaledArgs`, `main.go:885-893`,
  `RedactHome`, `internal/normalize/display.go:108-143`).
  Optional `--log` crosses into a separate operator-selected output path, not
  necessarily that state directory (`openLogFile`, `cmd/gauntlet/main.go:713-733), validated to
  not name an existing directory (`validateLog`, `cmd/gauntlet/flags.go:854-891`).

## Entry points

Untrusted inputs with their validation point:

| Entry point | Where it enters | Validation / cap |
|---|---|---|
| Project `*-review.md` files | git `ls-files` glob, walk fallback, `internal/prompt/discover.go`; read `prompt.go` via `readNoFollow` (`prompt.go:278-329`) | prompt discovery candidates inspected with `os.Lstat` requiring regular files (`discover.go:244,278`); regular files only, `O_NOFOLLOW\|O_NONBLOCK`, 1 MiB cap; skipDirs and hidden directories dropped; git-ignored files refused; project prompts override bundled ones of the same name; duplicate detection reads bounded |
| Prompt names (file stems) | `discover.go:68,88` | NFC-normalized keys (`prompt.go:338-340`); control/Cf characters reject the file with a warning (`discover.go:70,90`; strip at `prompt.go:342-346`) |
| Prompt descriptions ("Your goal" line) | `prompt.go:88-108`, fed to the suggest catalog `compose.go:369-396` | the prefix is matched on the line as written, before sanitize repairs it, so a line the file never wrote as a goal cannot supply a description; the extracted value is sanitized and NFC-normalized; name and description both fenced: `</catalog>` and `RELEVANT:` neutralized; 200-rune cap on a rune boundary (`compose.go:369-396`) |
| Prompt `Summary:` line | `prompt.go:153-171`, fed to stacked PR bodies `internal/runner/prbody.go` | sanitized, cut to 60 runes at read, and normalized to NFC before truncation (`prompt.go:159-170`), the goal-line fallback bounded the same way; PR rendering strips controls, flattens to one line, NFC-normalizes to prevent rune splitting, and bounds again (`prbody.go:26-50,88,150-178; stack.go:655`) |
| Prompt `Signals:` line | `prompt.go:199-232`, consumed by the file-signal suggester `internal/evidence/suggest.go:556-600` | known kinds only (`ext`/`name`/`path`/`mark`), charset-restricted values, 12 tokens × 40 runes; anything else dropped |
| CLI flags: `--agents`, `--bin TOOL=PATH`, `--agent-cmd NAME=ARGV`, `--prompt-dir DIR`, `--dirs` | `cmd/gauntlet/flags.go`, `cmd/gauntlet/paths.go`; parsed by `ParseSpecs` (`agent.go:375`), `ParseBin` (`agent.go:711`), `ParseAgentCmd` (`custom.go:434-445`), `discover.go:44-77`, `resolveDirs` (`paths.go:23-86`) | allow-listed tool names; dsh model charset-restricted (`dshModelRe`); `@effort` charset-restricted for every agent and refused where no verified flag exists (`effortRe`, `takesEffort`); argv split on spaces, no shell; `--bin` paths made absolute before any chdir (`ParseBin`); non-empty checks for `--agents`, `--bin`, `--agent-cmd`, `--usage-cmd`, `--suggest-agent`, `--exclude`, `--merge-into`, `--pr-base`, `--show-prompt` (`cmd/gauntlet/flags.go:440,592,605,615,637,717,725`, and `trimFlag`, `flags.go:976-985`); `--usage-cmd`'s first word resolved against the same absolute-only `PATH` the probe gets, so a command that cannot launch is refused at the flag rather than failing open on every check (`resolveUsage`, `flags.go:503-517`); target dirs expanded and deduplicated by realpath (`paths.go:23-86`); `--prompt-dir` takes regular files only, validated to be a directory, and a path that is not there is refused at the flag (`flags.go:817-838`), control-char names rejected; `--log` validates destination is not a directory (`validateLog`, `flags.go:854-891`); subcommands refuse flags they do not read (`rejectStrayFlags`, `flags.go:946`, the table at `flags.go:916-935`); custom agent loading gated to subcommands using agents (`usesAgents`, `flags.go:581,894`); subcommand peeling ignores empty arguments and lone dashes (`flags.go:1067-1100`); branch and revision commands separate options with `--` and `--end-of-options` (`branch.go:51,97,290,303,377,411,446,457,473,495,506`, `worktree.go:321,396,534,735,756,983`, `internal/gitx/status.go:20,39`) |
| `--paths` scope entries | `raw.paths` collected in `cmd/gauntlet/flags.go`, refused in `finishFlags` (`flags.go:648-654`), then pasted into the review prompt as an instruction by `pathsNote` (`internal/prompt/compose.go:212-245`), outside the review markers with the other operator instructions | operator-set, but free text wherever a wrapper built the list rather than a person typing it, and what the agent obeys is the prompt, not the flag: an entry is refused where the operator can see which one it was when it is empty, past `PathEntryMax` (200 runes), or carries a backtick, a control character, a `Cf`/`Zl`/`Zp` character, or any whitespace but a plain space (`PathEntrySafe`, `compose.go:156-175`). The scope block names at most 20 entries (`pathsNoteMax`, `compose.go:141-154`) and re-renders each through `pathEntry` (line breaks and controls to spaces, a backtick to an apostrophe, NFC, then a render equal to the entry or nothing, `compose.go:177-198`), so an entry that survives is named as itself and cannot read as an instruction around `quoteList`'s backticks. What is dropped is counted in the prompt rather than silently narrowed (`PathsNamed`, `compose.go:201-210`; the note in `pathsNote`); when nothing survives, the block says the scope is empty and asks for no findings and no edits. The scope is prompt-enforced, not mechanical: an agent is told the listed paths and can ignore them, and the suggest, commit, and conflict prompts deliberately keep the whole tree in view (`pathsNote`) |
| Run budgets: `--runtime`, `--token-budget` | `cmd/gauntlet/flags.go:350-352`; `budgetExhausted` (`internal/runner/loop.go:223-243`), `Stats.Tokens` (`internal/runner/stats.go:346-356`) | operator-set, both default 0 (unlimited) and both refused negative (`flags.go:691-697`); the token ceiling is a count of what the reviews themselves reported, summed across loops, lanes, and a hot-reload predecessor, so it is a scheduling bound, not a spend quota: it excludes the commit and conflict launches, is checked between reviews rather than during one, and a review that under-reports its tokens lowers nothing else |
| `--keep-runs N` history prune | `journal.Prune` (`internal/journal/retain.go:45-56,78-198`) called from `writeSummary` (`cmd/gauntlet/main.go:926-933`); default 200 (`cmd/gauntlet/flags.go:390`) | `N <= 0` keeps everything; negative refused (`flags.go:691-693`); deletion is taken from a walk that takes only real shard directories (`d.IsDir()`, `internal/journal/index.go:697-711`) and only `<id>.jsonl` files whose stem passes `validRunID` (`internal/journal/history.go:88-104`), so a symlinked shard or a planted name never widens the blast radius; the index row is dropped before its journal, the rewrite is under the index lock, an index line is capped at 4 MiB (`indexLineMax`, `retain.go:236-238), and a prune failure is a warning that leaves the tree growing |
| `runs --limit N` index listing | `fs.IntVar(&o.runsLimit, "limit", ...)`, `cmd/gauntlet/flags.go:410`; refused on any subcommand but `runs` (`flags.go:548-550,775-777`); read by `journal.Recent` into `parseTail` (`internal/journal/index.go:1119`) | operator-set on local state, but the number is unbounded, so it no longer sizes an allocation: `parseTail` reserves `min(n, maxTailHint)` with `maxTailHint = 1024` (`index.go:1104-1127`), because a `Summary` is a few hundred bytes and `runs --limit 100000000` otherwise reserved gigabytes for an index holding a handful of rows. The cap is a hint, not a limit: append still grows the slice to what the index really holds |
| `<binary>.previous`, the copy an update leaves behind | `keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`, called from `applyTo` (`selfupdate.go:292`) before the install rename; `make install` writes the same name (`Makefile:416`) | operator's install directory, which the update already has to write to install at all, so the boundary is unchanged. The copy is made through `os.CreateTemp` in that directory, `Sync`ed, closed, chmod'ed to the replaced binary's own mode, and renamed onto the destination, and the directory entry is `Sync`ed after the rename so a power cut cannot leave a file the next boot cannot find; every failure aborts the update rather than installing with no way back. The destination is not opened `O_NOFOLLOW` and no regular-file check precedes the rename, but a rename replaces a symlink rather than following it, so the only thing a planted `<binary>.previous` controls is what a later manual rollback installs. Nothing re-verifies the copy: `docs/CLI.md:162-167` gives the rollback as `mv <binary>.previous <binary>` |
| `gauntlet runs --json` | flag `flags.go:413`, refused on any other subcommand (`flags.go:557,923`); `writeRunsJSON`, `cmd/gauntlet/runs.go:133-176`, encoding `journal.Summary` rows (`internal/journal/journal.go:154-187`) plus the state root, the journals directory, and the pruned list | operator-set, on local state the same user wrote; the rows carry the reviewed tree's directory basenames and the run's own argv, so the content is B1 and B6 text going to a program rather than a person. `json.Encoder` with `SetIndent` leaves HTML escaping on, so `<`, `>`, `&`, and every control byte below 0x20 are written as `\uXXXX` and no escape sequence crosses as raw text; no `Display` runs over a field here or in the human table, whose DIRS column prints `filepath.Base` of each row directly, so on this path the encoder is the only thing between a repository's directory name and a terminal, and the remaining exposure is a consumer that decodes a field and re-renders it raw. The argv the index keeps carries the same free text as the event log, so both are shortened to `~` before they reach disk (`journaledArgs`, `cmd/gauntlet/main.go:885-893`; `journaledEvent`, `main.go:874-882`; `RedactHome`, `internal/normalize/display.go:108-143`, rewriting only at a whole path component); what is on disk therefore names no OS account, while the live terminal and the `--log` file still do. A read failure is an error, never an empty quarantine, and empty listings are `[]`, never `null` (`runs.go:157-163`), so a consumer cannot read a failed read as a claim that nothing is recoverable |
| `doctor` state-root probe | `stateRootProblem` (`cmd/gauntlet/doctor.go:354-386) | diagnostic only, but it writes: a temp file named `.gauntlet-doctor-*` in the state root, closed and removed before the check reports, and an unwritable or undeletable root is now the command's failure verdict (`doctor.go:260-266,278-280`) rather than a line that scrolls past; a root that does not exist yet is not probed, so doctor creates nothing the first run would not |
| `.git/info/exclude` append | `ExcludeOwnArtifacts` (`internal/gitx/worktree.go:91-155`) | the reviewed tree picks `gitDir`, so `MkdirAll` and `OpenFile` would follow a planted `.git` symlink or gitfile out of the repository; the parent directory is re-checked with `os.Lstat` requiring a real directory (`realDir`, `worktree.go:157-167`) and the append is refused when it is not. The read of that file, which happens only to check for two short entries the run needs, is capped at 1 MiB rather than taken whole, because the tree chooses the file and an oversized one is not worth the memory (`maxExcludeBytes`, `worktree.go:576-580`, read at `worktree.go:104-112`). Every failure now comes back, including the write and the close, and the run logs it (`internal/runner/runner.go:281-286`): a short write is a failure rather than a success, because the next run's substring check would not match a truncated line and would append the same entry again on every run |
| Environment: `PATH` | `pathNoCWD` (`agent.go:196-206`), `gitPath`/`gitEnv` (`internal/gitx/gitx.go:42-57`, `internal/gitx/exec.go:396-416`), `ghx.Available` (`internal/ghx/ghx.go:100-103`), `probeEnv`/`resolveProbe` (`usagelimit.go:93-114`) | cwd-relative and relative entries dropped for agent, git, git-child, `gh`, and usage probe resolution; child process environments isolated to absolute-only PATH via `CleanPATH`/`AbsPATH`/`AbsPATHEnv` (`internal/runx/runx.go:131-145) and executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths (`internal/runx/runx.go:225-252). An empty `PATH` falls back to `$HOME/.local/bin` and the fixed system prefixes (`defaultPath`, `runx.go:164-185`), for every executable rather than for agents alone, which widens the set a run will launch to those absolute directories and to nothing the reviewed tree can name. The rule is stated once, in `AbsPATH`, so no two resolvers can disagree about it |
| Environment: `HOME` (through `os.UserHomeDir`), `GAUNTLET_HOME`, `GH_TOKEN`/`GITHUB_TOKEN`, `TERM`/`NO_COLOR`/`CLICOLOR_FORCE`/`FORCE_COLOR`, `GAUNTLET_STATE`, `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, `REDUCED_MOTION`, `GIT_SSH_COMMAND` | `internal/gauntlethome/gauntlethome.go:33-48`, `selfupdate.go:84-108`, `cmd/gauntlet/report.go:51-83`, `internal/selfupdate/reload.go:24-25,187-196,238-267`, `internal/ui/view.go:404-408,420-437`, `internal/envx/envx.go`, `internal/gitx/exec.go:396-416` | operator-controlled, same-user trust; the list is closed by a test that walks non-test source for `os.Getenv`/`os.LookupEnv` and requires every literal, constant, or ranged-over name to be in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`), so a new knob cannot ship undocumented; `HOME` is the default state root only, and a home that cannot be read degrades to a local `.gauntlet` that custom agent loading refuses (`internal/gauntlethome/gauntlethome.go:33-48`); `GAUNTLET_HOME` validates unresolvable environment variables at startup (`cmd/gauntlet/flags.go:570-580`) and degrades safely to local `.gauntlet` (`internal/gauntlethome/gauntlethome.go:33-48`); expands tildes and environment variables and is made absolute at resolution so the state root cannot depend on the current directory (`CustomFilePath`, `custom.go:562-568`); `GAUNTLET_STATE` is read only where the process set it (`reload.go:188-196`) and is replaced on re-exec rather than inherited (`Reexec`, `reload.go:238-267`); its handoff file is verified by `LoadState` to require `.json` extension, absolute path, clean path without traversal, verified via `os.Lstat` as a regular file (rejecting symlinks and directories without deleting), and read capped to 16 MiB (`maxHandoffBytes`, `reload.go:179-208`); `SaveState` validates `runID` against directory traversal and path separators (`reload.go:136`) and flushes with `tmp.Sync()` before atomic rename; `GAUNTLET_NO_ANIMATION`, `NO_MOTION`, and `REDUCED_MOTION` disable TUI animation glyphs (`internal/ui/view.go:404-408,420-437`); the values that mean off for those three and for `CLICOLOR_FORCE`/`FORCE_COLOR` are one list, trimmed and case-folded, in `internal/envx/envx.go`, and `TERM` is compared against `dumb` the same way, so a wrapper exporting `TERM=DUMB` or a padded value still loses its palette (`cmd/gauntlet/report.go:51-83`); `GIT_SSH_COMMAND` overrides repository-local SSH commands and empty or whitespace-only values default to `ssh` (`internal/gitx/exec.go:396-416`); color variables configure ANSI output (`cmd/gauntlet/report.go:51-83`); `--update-repo` is `owner/repo` only (`ParseRepo`, `selfupdate.go:65`) |
| Agent stdout/stderr | pipes in `internal/runner/exec.go:115-136`; line scan `internal/runner/exec.go:370-420` | 4 MiB per line emitted in chunks with UTF-8 rune boundary preservation (`internal/runner/exec.go:34-42,380-394`), trailing CR stripped (`internal/runner/exec.go:390-392`), escape/control/bidi/separator strip before terminal (`internal/normalize/display.go:32 Display`), width cap 2000 cols (`internal/runner/exec.go:47`), rate limit 200 lines/s (`attempt.go:26`); ANSI CSI sequences terminated on standard final characters in token width calculation (`internal/ui/clip.go:75-117`); stalled command groups force-killed with SIGKILL on drain timeout (`internal/runner/exec.go:321-328`); even `--raw` passes `Display` (`internal/runner/exec.go:269-276`); usage and sink callbacks serialized across stdout and stderr streams (`internal/runner/exec.go:169,196-198,208,226-228`) |
| Stream-JSON events from agents | `internal/runner/exec.go:245-265`; `internal/streamjson/streamjson.go:81-99,107-138,199,202,217-248` | extraction depth capped at 8; role and type values normalized case-insensitively; objects marked role `tool`/`user` or type `user`/`tool_use`/`tool_result` contribute neither text nor usage; classification is not origin authentication; malformed JSON falls back to plain text and `--raw` bypasses classification |
| Token counters parsed from output and transcripts | `internal/runner/exec.go:167-203,334-346`; transcript watch off with `-tags notoktop`, `transcript_toktop.go:17`; custom roots `custom.go:69` | integers only; transcripts are other files under `$HOME` |
| GitHub release metadata | `selfupdate.Check`, `selfupdate.go:159` | HTTPS, 4 MiB decode cap; bounded by `fetch` (`selfupdate.go:447-464`); `owner/repo` charset-checked before it is concatenated into the API path |
| Release asset + `checksums.txt` | `applyTo`, `selfupdate.go:228` | validated by `validateAssetURL` (`selfupdate.go:372-388`) to require HTTPS and authorized GitHub release hosts (`github.com`, `api.github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests); HTTP requests routed through `getAsset` (`selfupdate.go:409-428`); HTTP redirects re-verify `validateAssetURL` and are capped at 10 (`client.CheckRedirect`, `selfupdate.go:145-157`); checksums fetched first (1 MiB cap), asset streamed with 256 MiB cap; SHA-256 verified using constant-time comparison (`subtle.ConstantTimeCompare`, `selfupdate.go:273`); downloaded binary flushed via `tmp.Sync()` (`selfupdate.go:277`) before atomic replace; `fetch` rejects responses exceeding size limit (`selfupdate.go:447-464`); download and error paths drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:184,187,431,458,461,478,481`); remote error messages decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:180,430-445`); a listing entry counts only as 64 hex digits (`checksumFor`/`isHexDigest`, `selfupdate.go:505-529`) |
| `gh` CLI JSON and PR URLs | `internal/ghx/ghx.go` `Find`/`Create` | argv-only; executable invoked with `runx.AbsPATHEnv()` (`internal/ghx/ghx.go:258-267`); PR URL must be `https` on the expected host (`validateURL`, `internal/ghx/ghx.go:250-256`); a PR is reused only when its head owner matches the push destination (`ownsHead`, `internal/ghx/ghx.go:178-196`) |
| Git outputs (shortstat, porcelain, check-ignore) | `internal/gitx/stats.go:53-97`, `internal/gitx/status.go:58-86,157-177`, `internal/gitx/list.go:34-57` | regex/line parsing; CRLF `\r` stripped (`internal/gitx/status.go:68,179`); C-quoted paths decoded (`unquoteC`, `internal/gitx/status.go:223-285`) then display-sanitized before any message (`safePaths`, `internal/runner/sanitize.go:14-19`; dashboard `internal/ui/ui.go:780-808`; plain reporter `report.go:143-148`); counts only, never executed |
| Reviewed repo's `.git/config` and `.gitattributes` | every git call, `internal/gitx/exec.go:26-33,73-136,233-270` | `core.fsmonitor`, `core.hooksPath=/dev/null`, `core.pager=cat`, `diff.external`, `core.gitProxy` forced empty; `protocol.ext.allow=never`; `attr.tree` pointed at the empty tree so in-tree `.gitattributes` cannot select a smudge filter or merge driver; local `filter.*`/`merge.*`/`diff.*` commands, `core.editor`, `core.askpass`, `gpg.*.program`, and peers blanked from `--local --list`, and `commit.gpgsign`, `tag.gpgsign`, and `push.gpgsign` forced false; the signing key is in that list because it is the one exec a reviewed config triggers with no attribute file involved, so a repository carrying `gpg.program` beside `commit.gpgSign` reached a shell on the first commit any review made, before an agent was consulted (`isSignProgram`, `internal/gitx/exec.go:292-305`; `isSignToggle`, `internal/gitx/exec.go:306-321`, in `disableLocalDrivers`, `internal/gitx/exec.go:248-270`). The blanking is local to the reviewed config, so an operator signing through a global `gpg.program` still does, and a review's commits, which this tool writes rather than the operator, are no longer signed on a reviewed repository's demand; the overlay is rebuilt whenever the size or mtime of any config file it was derived from moves, which is the local one and the system and global files the operator's own `credential.helper` values were read from (`--show-origin`), because a review runs with its permissions bypassed and can plant a driver in the local config mid-run and because a helper the operator removed has to stop being asserted; the cost is one lstat per watched file per git call against two subprocesses per rebuild (`stampConfig`, `internal/gitx/gitx.go:169-184`, `safeWatchedStale`, `internal/gitx/exec.go:450-457`, `extraSafeConfigLocked`, `internal/gitx/exec.go:439-448`); extra safe config propagated to worktrees and sub-repos via `subRepo` (`adoptSafeConfig`, `internal/gitx/gitx.go:226-240`, `internal/gitx/worktree.go:35-58`); git resolved on absolute-only PATH so a planted `./git` cannot run; `GIT_SSH_COMMAND=ssh` outranks a repo-local `core.sshCommand` unless the operator already exported one, and empty or whitespace-only values default to `ssh` (`mergeGitEnv`, `gitEnv`, `internal/gitx/exec.go:396-416`); git's own children die with its process group and bounded pipe wait (`internal/gitx/exec.go:327-395`); git stderr redacts embedded userinfo credentials (`internal/gitx/exec.go:327-395`); branch and revision commands separate options with `--` and `--end-of-options` (`internal/gitx/branch.go:51,97,290,303,377,411,446,457,473,495,506`, `internal/gitx/worktree.go:321,396,534,735,756,983`, `internal/gitx/status.go:20,39`) |
| `.gauntlet.lock` holder note | read back on lock conflict, `runner/lock.go:123-139` | the note lives in the reviewed tree, where an agent could rewrite it: one line, 120 runes, `Display`-sanitized and NFC-normalized before it reaches a terminal or file (`lock.go:22-28,135-160,163-186`); `Note` and `readNote` handle `EINTR` and partial writes in retry loops (`lock.go:135-160,163-186`); lock release preserves the descriptor flock and truncates the note to prevent file deletion and inode recycling races (`lock.go:202-215`) |
| Conflicted paths interpolated into the conflict prompt | `git diff -z` into `runConflictAgent`, `conflict.go:130-183`, then `ConflictPrompt` (`compose.go:305-351`) | a path is named only when it equals `normalize.Sanitize(p)` and `conflictPathOK` (control/Cf omitted, `RESOLVE:` and `</files>` omitted, 1024-rune cap); 50-file cap (over-cap skips the launch); list fenced in `<files>`; human-facing conflict hints quote branch and message via `runx.ShQuote` (`conflict.go:185-187`); a dropped path is still scanned for markers, so it holds the branch with a human |
| Helper-tool inventory appended to prompts | PATH probe at startup, `internal/runner/loop.go:205-212`, rendered `compose.go:99-138` | operator-machine facts crossing outward with every prompt: which helper binaries exist and that installing missing ones is forbidden |
| File-signal suggester tree walk | `evidence/suggest.go:600-640` | 100k files, depth 12, 2k file heads × 4 KiB; opens via `os.OpenRoot` (`evidence/suggest.go:716-726`) so a symlink or path that escapes the reviewed tree is skipped |
| `~/.gauntlet/agents.json` | `internal/agent/custom.go:93-283,351-366,455-511,513-552` | rejects null, unknown fields, duplicate keys (including NFC-normalized case-variant duplicates and nested usage duplicates), trailing data, and built-in redefinitions; requires `{prompt}` in argv exactly once and forbids `{prompt}` in model/effort/stream/continue; requires `{model}` in model and `{effort}` in effort if set; forbids `{model}` and `{effort}` across stream and continue; forbids model when argv contains `{model}` and effort when argv contains `{effort}`; forbids placeholders in argv executable, usage roots, and usage suffix; rejects empty/blank arguments in argv, model, effort, stream, and continue; rejects whitespace-only notes and notes holding control or formatting characters, which are printed beside the agent's name in a doctor row and on the launcher's own line (`validate`, `internal/agent/custom.go:93-141`); expands tildes and environment variables in custom executable `cmd[0]` (`agent.go:305-316`); validates non-empty usage roots and model; rejects whitespace-only usage suffix; validates all definitions before registration; strips UTF-8 BOM if present (`custom.go:465`); `CustomFilePath` (`custom.go:562-568`) returns empty without a state root; file read has no size cap and follows symlinks, so state storage must remain trusted. The file is the base of the registry, not its limit: a `--agent-cmd` for the same name unregisters whatever the file and the shipped definitions said and installs the command line's own argv, and a built-in tool is not in the registry, so that removes no tool and `Register` still refuses to rename one (`configureAgents`, `cmd/gauntlet/flags.go:421-480`; `Unregister`, `custom.go:369-375`). A repeated `--agent-cmd` naming one agent two different ways is refused instead of letting the last one win, case-folded so `Claude` and `claude` cannot both install (`flags.go:446-452`), and the whole list is parsed and checked before any of it registers, so a bad entry leaves no half-configured registry. The precedence is a usability decision, not a trust claim: both the file and the flag are operator-supplied, and the flag is the more specific of the two |
| `--usage-cmd` probe | `internal/runner/usagelimit.go:93-114` | argv-only; isolated execution directory (`os.TempDir()`); relative and cwd-relative entries dropped from `PATH` in `probeEnv()` and executable resolved against absolute-only PATH (`resolveProbe`, `runx.LookPath`); child process environment isolated via `runx.AbsPATHEnv()`; 10s deadline, 4 KiB per output stream; own process group killed on return as well as cancellation; final non-empty line parsed as finite 0-100, rejecting NaN and Inf (`parseUsagePercent`, `internal/runner/usagelimit.go:125-154`); usage limit check guards against NaN (`usagelimit.go:47`); failure warns once and leaves the threshold unenforced, and a command that could never launch is caught earlier, at the flag (`resolveUsage`, `cmd/gauntlet/flags.go:503-517`); finish flag set via atomic swap (`internal/runner/usagelimit.go:68-71`) |
| `--log FILE` destination | `openLogFile`, `cmd/gauntlet/main.go:713-733; `validateLog`, `flags.go:854-891` | startup rejects an existing directory target; at open an `os.Lstat` refuses a symlink and any non-regular file, the open itself carries `syscall.O_NOFOLLOW` with 0600, and a regular file that survived is chmod'd to 0600 before any output; no parent-directory confinement, no size cap, and no rotation |
| `--semcode` indexer | `buildSemcodeIndex`/`runIndexer`, `cmd/gauntlet/semcode.go:86-108` | `semcode-index` resolved through `agent.Resolve` (`pathNoCWD`, `internal/agent/agent.go:204-253`), so a planted `./semcode-index` in the tree cannot win; argv-only, no shell; child environment isolated with `runx.AbsPATHEnv()`; 30-minute per-directory cap with a deferred process-group SIGKILL (`runx.Guard`, `runx.KillGroup`); the child's working directory is the reviewed tree, so the tree decides both what it walks and the file names it reports, and each of its streams gets its own `normalize.DisplayWriter` (`cmd/gauntlet/semcode.go:91-100`), which passes every complete line through `Display` and holds an unterminated one until `Flush` after `Run` joins the copy goroutines (`internal/normalize/display.go:145-243`); the held partial line is capped at 1 MiB and released on a rune boundary (`display.go:196-242`) |
| `doctor` environment report | `envSettingLines`, `cmd/gauntlet/doctor.go:314-336; table `helpEnvVars`, `cmd/gauntlet/help.go:177-189` | reads only the documented names, so nothing the operator did not set for gauntlet is echoed; a variable the table marks secret is reported as `(set)` and never as a value, so a pasted `doctor` transcript carries no token (`doctor.go:313-335`); `GAUNTLET_HOME` is left out because the State line above already names it; nothing here interprets the value, and presence is the only thing reported. A test pins the set: every `os.Getenv`/`os.LookupEnv` name in non-test source is either in the help table or recorded as internal with a reason (`cmd/gauntlet/env_surface_test.go:31-33,35-59`) |
| `dsh --dump-config` probe | `dumpDshConfig`/`dshDefaultProvider`, `internal/agent/dsh.go:116-163` | runs only for a `dsh:<model>` pin; child environment isolated with `runx.AbsPATHEnv()` (`dsh.go:120`); bounded to 4 MiB output via `runx.Bound` (`dsh.go:81,124`); own process group, 120s cap, deferred group SIGKILL via `runx.KillGroup` (`dsh.go:123`); provider parsed with a narrow regex (`dshProviderRe`, `dsh.go:50`); the memo is keyed by the launcher argv, and the argv carries the `--bin` override, so one launcher's config cannot pin another (`dshProbes`, `dsh.go:99-101`, consulted in `dshDefaultProvider`, `dsh.go:150-163`); overlay values charset-restricted before they are quoted into YAML (`dshModelRe`, `agent.go:456`); provider and model validated against `dshModelRe` (`dsh.go:191-209`); overlay key rejects path separators and traversal (`dsh.go:169-188`) |
| Interactive launcher / picker keyboard input | `cmd/gauntlet/pick.go`, `internal/ui/pick.go`, `internal/ui/ui.go` | navigation keys jump to bounds (`g`/`G` in `internal/ui/pick.go:378-382`); cursor clamped to valid review rows via `clampReviewCursor` (`internal/ui/pick.go:341,445,448,499-510`); Esc/Ctrl-C during filter editing resets typing and clamps cursor (`internal/ui/pick.go:485-490`); empty filter matches block launch, with the reason returned by `blocked` and checked on Enter (`internal/ui/pick_view.go:145-160,263,435-436,523`, `internal/ui/pick.go:356-360`); Enter key terminates completed runs (`internal/ui/ui.go:780-808`) |
| Planted symlinks/FIFOs in the tree | prompt discovery inspects candidates with `os.Lstat` requiring regular files (`prompt/discover.go:244,278`); prompt reads `prompt.go:278-329`, lock creation `runner/lock.go:53-89`, untracked counting `internal/gitx/stats.go:98-154`, reload handoff `reload.go:188-196` | `O_NOFOLLOW\|O_NONBLOCK` at open time, regular-file stats, size caps; stat errors propagated on open regular files (`internal/gitx/stats.go:169-190`); `LoadState` verifies regular file with `Lstat` (`reload.go:187-196`) |
| Developer build surface: `make repro` archives the working tree | `Makefile:628-653`; member list and archive `Makefile:636-639`; the tests holding the recipe to git's ignore rules `cmd/gauntlet/makefile_test.go:1029-1108` | the target is a developer convenience, not a shipped code path, but the archive is the whole checkout: it is written to `$(HOME)/.cache/gauntlet/repro` and the recipe refuses to run with `HOME` unset rather than writing to `/.cache`; the members are `git ls-files --cached --others --exclude-standard`, so a build output, cache, or local state that `.env`, `.gauntlet/`, and `.gauntlet.lock` already are cannot be copied, a new `.gitignore` entry covers the archive the moment it is written, and a pattern tar would match too broadly no longer decides; the directory is removed on exit by a shell trap. What the list cannot express is a file a developer has not ignored: an untracked credentials file is archived to `$HOME/.cache` for the duration of the build, where it is neither reviewed nor redacted |
| Release build surface: `cmd/sbom` writes the shipped dependency inventory | `run`, `cmd/sbom/main.go:30-88`; inventory `internal/sbom/sbom.go:98-220`, licenses `internal/sbom/license.go:34-81`; run by the release target (`Makefile:547`) and uploaded as `dist/sbom.json` (`.github/workflows/release.yml:195-201`) | a release-time tool on this repository, not a path a reviewed repository reaches, and it adds no dependency: the package doc says why (`internal/sbom/sbom.go:4-8`), and the implementation is the standard library plus `debug/buildinfo`, so the artifact that describes the dependency surface does not widen it. Its inputs are the built binary paths from argv, read with `buildinfo.ReadFile`, which follows a symlink and is not size-capped, so a path naming something other than a built binary is either refused or inventoried as whatever build info it carries. The license half reads two more untrusted shapes and bounds both: `go list` is a child process and each module's grant is a whole-file read, so the listing is capped at 8 MiB, the grant at 1 MiB, the pipe wait at 10s, and the child runs through `runx` for the process group and the deadline kill that takes any grandchild with it (`goListWait`, `goListMaxBytes`, `licenseFileMax`, `internal/sbom/license.go:32-45`; `moduleDirs`, `license.go:116-144`) `Merge` refuses a module path recorded at two versions, which catches binaries that did not come out of one tree, and `run` refuses a binary whose main module path is not the first one's, so one release cannot be described as two programs (`cmd/sbom/main.go:30-92). It does not verify that a binary is the one `checksums.txt` covers, nor that a binary is what the build produced: the inventory is a claim the compiler stamped into a file, so it is not an integrity control for R2. The output write is `os.WriteFile(*out, ..., 0o644)` (`main.go:71`): it follows a symlink at the destination and leaves a pre-existing file's mode as it found it, where the runtime `--log` destination is refused when it is a symlink or any non-regular file and is opened `O_NOFOLLOW` at 0600 and then chmod'd (`cmd/gauntlet/main.go:713-733). Nothing in a release workspace is hostile without a compromised build, which is R2's subject |

## Threats per boundary

**B1 -> B2 (the injection path).** A hostile repository plants
`evil-review.md`; discovery prefers it over the bundled prompt of the same
name, composition fences it between markers, and both the opening and closing
marker strings in the body are escaped, repeatedly until none is left
(`escapeMarkers`, `compose.go:44-61`). The agent that
receives it has its own permission system disabled or auto-approved (or, for a
custom agent, whatever the operator put in argv). Applicable classes: elevation of privilege (repo
author -> code execution with user rights), tampering (working tree,
commits), info disclosure (secrets, source exfiltration). The fence is
advisory: the ground rules forbid git, deletion, and persistence, and
prompt-review gets a narrow exception (`compose.go:265-272`), but nothing
technical stops a sufficiently persuasive prompt from talking an agent out of
them. This is the accepted core risk; DESIGN.md answers it operationally: run
untrusted repos in a container.
The operator's own text is a second fence, and on this path the one an attacker
does not need the repository to reach: `--paths` is pasted into the same prompt
as a scope the agent is told the review body cannot widen
(`pathsNote`, `internal/prompt/compose.go:212-245`). An entry that is not a
path is refused at the flag, and the entries that survive are capped, counted,
and rendered so none can read as an instruction around the list
(`PathEntrySafe`, `compose.go:156-175`; `PathsNamed`, `compose.go:201-210`).
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
(`cmd/gauntlet/semcode.go:91-100`), which passes every complete line through
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
cwd-relative entries (`internal/agent/agent.go:196-206,245-253`), and it
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
  `internal/runner/attempt.go:132`); agents stay forbidden to run git (DESIGN.md
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
  checkout. A publication failure stops later reviews, so no agent runs on a
  branch that does not exist as a usable remote PR base. Stack branch names
  are derived from a public base commit, so a pull request is only reused as
  a run's own layer when its head branch lives in the repository gauntlet
  pushes to (`ownsHead` in `internal/ghx/ghx.go:156-177`): a PR opened from
  someone else's fork under a name a run is about to use is ignored, not
  adopted. Git and `gh` receive fixed argv elements rather than shell text;
  branch commands separate branch names with `--` option delimiters and
  rev-parse, log, and diff separate options with `--end-of-options`
  (`internal/gitx/branch.go:51,97,290,303,377,411,446,457,473,495,506`, `internal/gitx/worktree.go:321,396,534,735,756,983`, `internal/gitx/status.go:20,39`);
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
  (`commit.go:137-222`) execs one agent with
  `prompt.CommitPrompt`, which instructs it to run `git commit` and never
  to push; the runner strips injected AI attribution trailers from the new
  commit (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) and pushes itself under `--push`
  (`internal/runner/commit.go:228-236`). Neither trailer stripping nor a clean
  tracked-file status is a security inspection of the committed content, and
  neither prevents the agent from invoking Git itself. Under `--yolo` a
  rejected push escalates to a runner-side `git pull --rebase` and retry; a
  rebase conflict cleans up mid-rebase state via `git rebase --abort`
  (`internal/gitx/branch.go:262-266`) and fails the step rather than returning to the agent. The
  same launch, offered standalone when a dirty tree blocks `--jobs`, is
  gated on explicit consent (`cmd/gauntlet/main.go:960-984). The prompt is embedded
  text only (`rules/commit.md`), capped at 5 minutes (`commit.go:24`),
  under the same process discipline as any review.
- **Conflict resolution:** `resolveConflict` (`conflict.go:37`) cuts a
  scratch checkout, replays the branch, and launches one agent with
  `ConflictPrompt` (`compose.go:305-351`, rules in `rules/conflict.md`) for up to
  10 minutes (`conflict.go:26`). The prompt lists only sanitization-safe
  paths and forbids git; conflict hints quote branch and message via `runx.ShQuote`
  (`conflict.go:185-187`) to prevent shell injection if pasted; the marker
  scan before the commit covers the resolution's whole commit scope, the
  conflicted paths plus everything the resolver touched in the scratch
  checkout, because the commit stages it whole
  (`commitScope`, `conflict.go:102-119`, fed by `Worktree.CommitScope`,
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
  `runx.Bound` provides a process group and cancellation kill (`internal/runx/runx.go:88-114`); a deferred
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

**B2, spend (the two budgets).** `--runtime` bounds wall clock and
`--token-budget` bounds the tokens the run's reviews report
(`budgetExhausted`, `internal/runner/loop.go:223-243`; `Stats.Tokens`,
`internal/runner/stats.go:346-356`). Both are read before a loop and before a
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
(`internal/runner/commit.go:220-224`, kind at `internal/runner/event.go:24`), and worktree-mode commits carry
the runner as author. Stacked publication adds a `pull_request` event carrying
the exact head, base, status, and URL; the terminal summary repeats every URL.
The recorded seed is now the run's only one: `effectiveSeed`
(`cmd/gauntlet/reload.go:124-140`) resolves it once, from `--seed` or from the
handoff a hot reload carries, and hands the same number to the schedule and the
suggest shuffle, which draw from it through `SeedOrClock`/`drawIndex`
(`internal/runner/draw.go:35-110`). Before that, each consumer read the clock
itself and a reload handed the suggest step a different seed than the journal
recorded, so the seed in the journal did not replay the run it named. An unset
seed is derived from `time.Now().UnixNano()`, never 0, and is not a secret: it
orders agents and retry backoff, and nothing in the draw is a capability.

**B3.** Display sanitization addresses terminal escape injection, not the
truth or origin of printable content. The composed prompt shown by
`--show-prompt` (`modes.go:65-66`), the journal replayed by `show`
(`runs.go:352-395`, which spawns no pager), the plain reporter
(`report.go:143-148`), and the dashboard feed
(`internal/ui/ui.go:780-808`) pass through the same stripping, which removes ASCII controls,
Unicode formatting/bidi (`Cf`), and line/paragraph separators (`Zl`/`Zp`), and
truncates at whole grapheme cluster boundaries (`internal/normalize/display.go:16-43`).
Stream classification excludes recognized tool/user objects (normalized case-insensitively) before their text reaches the report
parsers, but trusts the producer's role/type fields
(`internal/streamjson/streamjson.go:199,202,217-248`,
`internal/runner/exec.go:245-265`). A compromised agent can still supply false
assistant text or plain-text report lines. Output buffering has line and tail caps, but
`--raw` bypasses normalizer rate/width limits while retaining `Display`
(`internal/runner/exec.go:269-280`). Usage and sink callbacks are serialized
(`sinkMu`, `usageReportMu`) across concurrent stdout/stderr streams (`internal/runner/exec.go:169,196-198,208,226-228`),
and transcript watcher lifecycle cleanup is synchronized with reviews
(`usageWatch.halt`, `internal/runner/attempt.go:501-504`).
When readers are stalled by orphaned grandchildren, gauntlet issues a process-group SIGKILL on
drain timeout (`internal/runner/exec.go:327-334`). The one child whose output is not parsed,
the `--semcode` indexer, is filtered by the same `Display` on the way to the
terminal, one `DisplayWriter` per stream (`cmd/gauntlet/semcode.go:91-100`,
`internal/normalize/display.go:128-224`), so no subprocess stream reaches a
terminal unfiltered. Parser bounds do not limit a child's disk writes, network
traffic, CPU, or provider spend. A non-positive process timeout also removes the per-launch
deadline (`internal/runner/exec.go:293-300`).

**B4.** Update metadata is requested over HTTPS; release asset and checksum downloads
are constrained by `validateAssetURL` (`internal/selfupdate/selfupdate.go:372-388`) to
HTTPS on authorized GitHub release hosts (`github.com`, `api.github.com`,
`objects.githubusercontent.com`, `release-assets.githubusercontent.com`, or loopback in tests), preventing cleartext transfers
or links pointing to untrusted third-party infrastructure. Download requests validate
redirect targets against the same host allowlist and stop after 10 redirects (`client.CheckRedirect`,
`internal/selfupdate/selfupdate.go:145-157`). Downloads are routed through `getAsset`
(`internal/selfupdate/selfupdate.go:409-428`) and compared with SHA-256 entries in `checksums.txt`
using constant-time comparison (`subtle.ConstantTimeCompare`,
`internal/selfupdate/selfupdate.go:159-196,228-303,505-529`). Downloaded binaries are
flushed to disk with `Sync()` (`selfupdate.go:276`) before atomic rename.
Download responses are bounded and verified by `fetch` to reject oversized responses
exceeding the limit (`internal/selfupdate/selfupdate.go:447-464`); download and error paths
drain HTTP response bodies up to limits via `drainBody` (`selfupdate.go:184,187,431,458,461,478,481`);
error messages are decoded up to 4 KiB on failure via `responseError` (`selfupdate.go:180,430-445`).
The checksum proves the download matches the listing, not that the publisher
is benign: the same publisher controls both. Compromise of the GitHub repo
or its release process yields arbitrary code execution on updating machines,
amplified by hot reload re-exec'ing the new binary mid-run without user
confirmation (`internal/selfupdate/reload.go:238-267`). Auto-update background
goroutines are coordinated via context cancellation and WaitGroup on shutdown
(`cmd/gauntlet/main.go:604-608`). No signed-release mechanism exists. The
advisory scanner in CI (`vulnscan.yml`) watches the dependency graph, not this
channel. The `bunx @deepseek-ai/dsh` fallback (and the `--dump-config` probe
that uses the same argv) is a second fetch-and-execute path, from the npm
registry rather than GitHub releases, with no checksum (`internal/agent/agent.go:594-680,
`internal/agent/dsh.go:83-122`). The probe is bounded to 4 MiB output via `runx.Bound`
(`internal/agent/dsh.go:64,84`) and reclaims process groups via `runx.KillGroup` (`dsh.go:85`).
Overlay configurations validate provider and model identifiers
against `dshModelRe` (`internal/agent/dsh.go:135-137`), and overlay keys reject path traversal
(`internal/agent/dsh.go:124-137`).

**B5.** Secret egress: the updater explicitly attaches `GH_TOKEN` /
`GITHUB_TOKEN` only to requests whose scheme is HTTPS and hostname is
`api.github.com` or `github.com` (`internal/selfupdate/selfupdate.go:90-108`).
Asset and checksum endpoints are separately checked against the HTTPS release host
allowlist (`validateAssetURL`, `internal/selfupdate/selfupdate.go:372-388`).
Git error messages redact embedded basic-auth userinfo credentials (`runx.RedactUserinfo`,
`internal/gitx/exec.go:327-395`) before display or journaling. Agent CLIs receive the inherited
environment and hold their own stored credentials; anything the user can read, a runaway
agent can read and send where its model provider accepts. No gauntlet-side
control exists; the boundary is the operating system, hence the container
guidance.

**B6.** Journal creation requests 0600 files and 0700 directories
(`internal/journal/journal.go:230-285, `internal/journal/index.go:93-120,660-706`).
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
via `gauntlethome.SweepStaleTemps` (`internal/gauntlethome/gauntlethome.go:183-219`,
called at `internal/selfupdate/reload.go:144`). The journal index directory and
the dsh overlay cache are not swept, and this document no longer claims they
are: the index writes through `WriteFileAtomic`, whose temp file is removed by
the rename that follows it, and the dsh overlay is one small YAML file per
provider and model pair, written once per key and never deleted
(`writeDshPatch`, `internal/agent/dsh.go:162-198`), so that directory grows
with the number of distinct pins an operator has used rather than with run
count. What the sweep does is decide removable from the
lstat, not from the directory entry type (`gauntlethome.go:203-215`): a mount
that reports no entry type hands back `ModeIrregular`, which reads as "not a
regular file" and would leave the leftovers accumulating on a state root that
is exactly such a mount. Stat-ing the entry is free where the type is already
known and keeps a symlink out of reach, because the link itself is what is
stat-ed, so a planted `<prefix>*` symlink is still not removed. Every
rename-based write in those paths is
the one helper (`gauntlethome.WriteFileAtomic`,
`internal/gauntlethome/gauntlethome.go:148-168`), so a partial write is not a
shape each caller can have differently.
Journal entry and index writes are flushed with `Sync()` (`internal/journal/index.go:165,199,899`),
and Close preserves and joins both file and index errors (`internal/journal/journal.go:343-365`).
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
(`internal/humanize/humanize.go:28-41), which refuses a value at or below
zero, a NaN, an infinity, and a magnitude at or past the nanosecond range rather
than clamping: `json.Unmarshal` hands back whatever the bytes held, and a
corrupt or same-user-planted journal is a file this tool reads, where a
negative or wrapped duration renders as a run that never ended.
Run IDs from CLI flags or events are strictly validated against directory traversal and non-safe
charsets (`validRunID`, `internal/journal/history.go:88-104`), and date shards derive from the run ID timestamp
(`shardFromRunID`, `internal/journal/history.go:167`). Creation
modes do not tighten permissions on pre-existing paths or authenticate local
state. Journal opens follow existing paths, so a protected state directory is
a precondition, not an enforced property of any `GAUNTLET_HOME` value
(`GAUNTLET_HOME` expands tildes and environment variables, with unresolvable
references rejected at startup via `cmd/gauntlet/flags.go:571-580` and safe degradation
via `internal/gauntlethome/gauntlethome.go:33-48`).
`LoadState` (`internal/selfupdate/reload.go:173-227) requires the `.json` extension,
requires absolute paths, verifies clean paths without traversal, verifies via `os.Lstat`
that the state file is a regular file (rejecting symlinks, directories, and non-regular
files without deleting target), and bounds reads to 16 MiB (`maxHandoffBytes`), rejecting
oversized files before removal. `SaveState` (`internal/selfupdate/reload.go:136`)
validates `runID` against directory traversal and path separators. The tree lock's symlink
check is a separate control (`internal/runner/lock.go:85-133`); `Note` normalizes to NFC
before truncation (`internal/runner/lock.go:94-119`); `Note` and `readNote` handle
partial writes and `EINTR` retry loops (`internal/runner/lock.go:99-119,123-139`), and lock release
preserves the open file descriptor flock and truncates the note to prevent
file deletion and inode recycling races (`internal/runner/lock.go:161-175`).
A malicious agent already runs as that operator and can tamper with local evidence.

`--log` is a separate confidentiality and availability boundary: it retains
output that the journal omits. `openLogFile` (`cmd/gauntlet/main.go:713-733)
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
does so on every exit rather than on request. `Prune` (`internal/journal/retain.go:45-56,78-198`)
keeps the newest `keep` journals and index rows by run ID, which embeds a UTC
start time, and removes the rest oldest first (`internal/journal/index.go:697-740`),
except a journal a run still has open, which the writer's shared lock keeps
out of the move and out of the index rewrite.
Threats: **repudiation**, in the ordinary sense of a run whose evidence is gone
before anyone asked for it, which is what a default of 200 does to a long-lived
install; **tampering**, because the ordering is a property of the ID string
rather than of anything the journal asserts, so a run whose clock sat behind
another run's is evicted first, and a same-user agent that can write `runs/`
controls which names sort above which; **denial of service**, bounded and
small, since the index rewrite is the only unbounded read in the path
(`readAllIndex` with a 4 MiB line cap, `retain.go:205-245). What the walk
prevents is the version of this that reaches outside: it takes only entries
the readdir reports as real directories and only `<id>.jsonl` files whose stem
passes `validRunID`, so a planted symlinked shard is skipped rather than
followed and `os.Remove` deletes a name, never a link target. A prune that
fails is a warning; the run's own report has already been written
(`cmd/gauntlet/main.go:925-934`).

## Mitigations map

| Threat class | Control | Where |
|---|---|---|
| Repo config executing code during git calls | forced-empty safe config, `protocol.ext.allow=never`, `attr.tree` empty, local drivers blanked, the signing program blanked and the signing toggles forced off, and the blanking rebuilt when any config file it was derived from changes (local, plus the system and global files the operator's credential helpers were read from), absolute-only git PATH, `GIT_SSH_COMMAND=ssh` (defaulting on empty/whitespace env, `internal/gitx/exec.go:396-416`); propagated to worktrees and sub-repos via `subRepo`; branch and revision commands separate options with `--` and `--end-of-options`; a merge target is refused before `worktree add`, which takes no `--`, can read a leading dash as an option | `internal/gitx/exec.go:26-33,73-136,233-270,396-416,439-457`, `worktree.go:35-58,321,396,534,735,756,983`, `branch.go:150-177,480-511` |
| A reviewed repository reaching a shell through its own signing config | `gpg.*.program` is blanked with the filter, merge, and diff drivers, and `commit.gpgsign`, `tag.gpgsign`, and `push.gpgsign` are forced false, so the one exec a reviewed config can trigger with no attribute file involved has nothing to trigger it and nothing to run. The blanking is local to the reviewed config, so an operator signing their own commits through a global `gpg.program` still does | `isSignProgram`, `internal/gitx/exec.go:292-305`, `isSignToggle`, `internal/gitx/exec.go:306-321`, in `disableLocalDrivers`, `internal/gitx/exec.go:248-270` |
| Two agents in one reviewed tree, on whichever platform the run happens to be on | the process keeps a registry of the lock files it holds, keyed by the real path, and `Acquire` consults it before the `flock`: a `flock` belongs to the open file description on Linux and to the process on macOS, where a second acquisition converts the lock rather than failing, so without the registry the same double-lock refused to start on one claimed platform and carried on with two sets of agents in one tree on the other | `heldLocks`, `internal/runner/lock.go:43-72`, consulted at `lock.go:106-113`, released at `lock.go:210` |
| A prune moving a live run's journal while this process is still appending to it | `journalIdle` asks the in-process writer registry before it asks the kernel, so the check cannot disagree with the writer on either platform, where the shared `flock` a writer holds is per process on macOS and would report the stream idle | `journalIdle`, `internal/journal/retain.go:66-70`, against `holdsStream`, `internal/journal/index.go:122-134` |
| A journal or index path a reviewed repository committed as a symlink | the two appends that reach an already-existing file open `O_NOFOLLOW` and `O_CLOEXEC`, which is the pair with no `O_EXCL` to prove the create; a `GAUNTLET_HOME` resolving beside the working directory, the degradation path for an unreadable home, is the case where the state tree sits inside the reviewed tree | `openNoFollow`, `internal/journal/journal.go:286-301`, at `journal.go:260` and `internal/journal/index.go:228` |
| Writing outside the repository through a planted `.git` | `.git/info/exclude` append re-checks the parent directory on the path it is about to write, with `os.Lstat` requiring a real directory, because the reviewed tree picks `gitDir` and both `MkdirAll` and `OpenFile` follow a link at any component | `internal/gitx/worktree.go:91-155` |
| Runaway git grandchildren (hooks, merge drivers) holding pipes | process-group SIGKILL on deadline, bounded WaitDelay | `internal/gitx/exec.go:327-395` |
| Planted executables shadowing agents/git/`gh` | cwd-relative PATH entries stripped and child environments isolated with absolute-only PATH via `runx.AbsPATHEnv` for agent, git, git-child, `gh`, and usage probe resolution; executable lookup consolidated via `runx.LookPath` which resolves relative paths with path separators to absolute paths; an empty `PATH` falls back to the fixed system prefixes for all of them alike, never to a directory the tree names | `agent.go:196-206`, `internal/gitx/gitx.go:42-57`, `internal/gitx/exec.go:396-416`, `internal/ghx/ghx.go:100-103,258-267`, `usagelimit.go:93-114`, `internal/runner/exec.go:120`, `runx.go:131-215,225-252` |
| Symlink/FIFO race into permission-bypassed runs | `O_NOFOLLOW` opens, regular-file stats, size caps; stat errors propagated on open regular files; prompt discovery inspects candidates with `os.Lstat` requiring regular files; suggester peeks via `os.OpenRoot`; reload handoff verified regular file via `Lstat`; the tree lock is created 0600 and `Fchmod`ed to 0600 on the descriptor, so a note naming the run, review, and agent CLI is not left group- or world-readable in a directory the reviewed tree picks; a note read is trimmed of a partial UTF-8 rune before it reaches a terminal | `prompt.go:278-329`, `discover.go:244,278`, `internal/gitx/stats.go:98-154`, `runner/lock.go:53-89,123-159`, `evidence/suggest.go:716-726`, `reload.go:187-196` |
| Prompt injection blending into containment rules | begin/end markers, both markers escaped in the body, report-section stripping fails open; the escape repeats until no marker is left (`escapeMarkers`, bounded at 8 passes), because the `(text)` form ends in the same `---` the marker does and a body whose markers shared their dashes re-formed one at the seam, which left the agent reading the review as ended with the body's text where the ground rules belong | `compose.go:36-41,44-61,75-97` |
| Operator scope text becoming prompt instructions | `--paths` entries are refused at the flag where the operator can see which one it was, unless each is non-empty, at most 200 runes, and free of backticks, controls, `Cf`/`Zl`/`Zp`, and whitespace other than a plain space; the block names at most 20 entries, re-renders each so a surviving entry equals its own rendering, and counts what it dropped, because a scope that reads shorter than the flag is a wider one | `internal/prompt/compose.go:148-245`, `cmd/gauntlet/flags.go:640-654` |
| A usage limit probe that trips after a lane has already taken a review | the lane re-reads the finish flag the probe set and drops its unstarted queue, so the remaining `--jobs` reviews do not start against a window the run decided was spent | `internal/runner/attempt.go:36-64`, `internal/runner/loop.go:147` |
| A hostile untracked file name reaching the run journal | every git path a run note prints goes through `safePaths` before the humanize list renders it, the same filter the dirty-tree note and a stack's path list use; the lane's untracked note was the one that did not | `internal/runner/sanitize.go:14-19`, `internal/runner/loop.go:50,54` |
| Injection via suggest catalog | description *and* name sanitize, fence-neutralizing, 200-rune cap, NFC normalization before truncation, strict suggestion grammar checked against known set, reasons capped to the same budget; the unknown half of a triage answer is capped at 20 names of 200 runes with the remainder counted and reported as truncated, so a confused or hostile agent cannot leave a megabyte of retained strings or a line of log with no end | `compose.go:369-396,317-354` |
| Injection via `Signals:` into the file-signal suggester | known kinds, charset, 12×40-rune caps; `mark:` values search file heads, not executed; a head that could not be read whole is left unscanned, because a partial prefix either declares a mark the file does not carry or hides one past the truncation, and a wrong signal steers which reviews auto-run | `prompt.go:199-232`, `evidence/suggest.go:556-600,716-726` |
| Terminal-driven or spoofed output, including prompt preview, journal replay, reporter, and dashboard | `Display`/`Sanitize` strip escapes, controls, bidi, and separators, and repair bytes that are not valid UTF-8 so one file name renders the same whatever sits beside it; width cap; rate limit; duplicate collapse; grapheme-preserving truncation; ANSI CSI sequences terminated on standard final characters in token width calculation; usage and sink callbacks serialized | `internal/normalize/display.go:16-43`, `modes.go:65-66`, `runs.go:386`, `report.go:143-148`, `internal/ui/ui.go:780-808`, `internal/ui/clip.go:75-117`, `internal/runner/exec.go:42,47,169,196-198,208,226-228,272-279`, `runner.go:26` |
| Hostile file names reaching messages or logs | C-quote decoding then sanitization of every git path before a terminal write; CRLF stripped | `internal/gitx/status.go:68,179,223-285`, `internal/runner/sanitize.go:14-19`, `lock.go:22-28`, `conflict.go:130-183` |
| Hostile file names forging conflict-prompt instructions | drop unsanitary paths (control/Cf, `RESOLVE:`, fence-closer); 1024-rune path cap; 50-file cap (over-cap skips the launch); list fenced in `<files>`; markers still block the merge | `compose.go:305-351`, `conflict.go:130-183` |
| A conflict marker left outside the conflicted file set reaching the merge | the marker scan covers the commit's whole scope, the conflicted paths plus every tracked and untracked path `CommitAll` would stage, and a file the scan cannot read counts as unresolved rather than clean; an unreadable status falls back to the narrower conflicted list | `commitScope`, `internal/runner/conflict.go:68,102-119`, `Worktree.CommitScope`, `internal/gitx/worktree.go:559-575` |
| Shell injection in conflict resolution hints | branch names and commit messages quoted with POSIX escaping via `runx.ShQuote` in `conflictHint` | `internal/runner/conflict.go:185-187`, `internal/runx/runx.go:253-259 |
| Model output written as a commit subject | ASCII controls, Unicode Cf (bidi), Zl/Zp stripped; grapheme cluster preserved; NFC normalization before truncation; 100-rune cap where the line is parsed and 72 where it is committed, so a long line is clipped before it reaches history, a PR title, and a merge message; a subject carrying an attribution credit line is dropped for the generated one; one line | `internal/agent/notes.go:83-118`, `internal/runner/subject.go:40-72,224-235` |
| A commit subject read back out of history reaching a PR title | one choke point, `clipSubject`, sanitizes and clips whatever a subject's source was: generated from the changed files, parsed out of agent output, or read back with `CommitSubject` during a stack recovery, where an agent that committed on its own, a resolved conflict, or the operator wrote it. The layer's PR title comes from that read, so a title crafted in a branch left behind by a killed run is sanitized on the way to GitHub, and a source added later inherits the guarantee rather than needing its own call | `internal/runner/subject.go:224-235`, `internal/runner/stack.go:527-535` |
| A generated branch name cut inside a multi-byte character | the abbreviated commit id a branch or probe name carries is clipped by runes at a grapheme boundary, not by bytes, so a base ref a hostile repository reported cannot land a half-encoded character in a branch name, a probe ref, or an error message | `normalize.Clip` at `internal/runner/stack.go:167,184,601`, `shortTipLen`/`shortDisambiguatorLen` at `internal/runner/stack.go:197-199` |
| A commit subject an agent supplies crediting itself or a model CLI | a narrow regex over the parsed subject (`attributionRe`, `internal/runner/subject.go:54`) drops a trailer or phrase that credits a tool, replacing it with the generated subject; the word alone is not a match, so a change that genuinely concerns one of those tools still describes itself; the trailer sweep on the finished message (`StripAITrailers`, `internal/gitx/trailers.go:71-95`) does not reach the subject, which is why the check is here | `internal/runner/subject.go:40-63` |
| Output-volume DoS from a chatty agent | 4 MiB line cap emitted in chunks with UTF-8 rune boundary preservation, trailing CR stripped, feed error trigram prefilter, bounded tail buffers (1 MiB suggest tail) | `internal/runner/exec.go:34-42,365-367,380-394,448-462`, `internal/normalize/normalize.go:302-326` |
| Oversized prompt files | 1 MiB read cap; argv-length pre-check with named failure | `prompt.go:33-37`, `agent.go:481-489` |
| Runaway/hung agents | per-review timeout, process group SIGTERM then SIGKILL, deferred SIGKILL on normal exit to clean up orphaned grandchildren, drain timeout process group SIGKILL escalation to unblock stuck readers, stdin null device, own session (no controlling terminal, so Ctrl-C cannot be disabled from inside an agent), drain grace for stuck grandchildren | `internal/runner/exec.go:25-32,100-350,140-146,327-334,453-485` |
| Commit step running away | separate 5-minute cap, same process discipline, journaled outcome; runner-side publication is workflow separation, not removal of agent credentials | `internal/runner/commit.go:24,160-245` |
| Conflict step running away | separate 10-minute cap, same process discipline; unresolved markers keep the branch | `conflict.go:26,37-90` |
| Indexer binary shadowed by a planted `./semcode-index` | resolved with `agent.Resolve` (`pathNoCWD`), argv-only, absolute-only PATH in the child, 30-minute per-directory cap, deferred process-group SIGKILL | `cmd/gauntlet/semcode.go:86-108`, `internal/agent/agent.go:204-253`, `internal/runx/runx.go:58-112,164-185` |
| File names from a hostile tree reaching the terminal through the indexer | one `normalize.DisplayWriter` per stream, each complete line through `Display`, partial line held to a 1 MiB cap and flushed after the child is reaped; no child stream is written straight to the terminal any more | `cmd/gauntlet/semcode.go:91-100`, `internal/normalize/display.go:145-243` |
| A secret reaching a pasted `doctor` transcript | the environment report prints a name the table marks secret as `(set)`, never as a value, and reads only documented names; every environment name in the source is pinned to the table or to a stated reason it is internal | `cmd/gauntlet/doctor.go:314-336`, `cmd/gauntlet/help.go:177-189`, `cmd/gauntlet/env_surface_test.go:31-33,35-59` |
| Unbounded or unauthorized downloads | 256 MiB asset, 4 MiB metadata, 1 MiB checksum caps; HTTPS and authorized GitHub release host check (`validateAssetURL`); unified `getAsset` check; HTTP redirects re-verify host allowlist and stop after 10 redirects; fetch strictly rejects responses exceeding limit; download and error responses drain HTTP bodies up to limits via `drainBody`; error messages decoded up to 4 KiB on failure via `responseError`; digest-shaped entries only; constant-time checksum comparison; binary flushed with `Sync()`; verify-before-rename, atomic replace | `selfupdate.go:117,145-157,159,180,184,187,228,273,277,372-388,409-428,447-464,430-445,505-529` |
| An update that installs bytes and leaves no way back | the replaced binary is copied first, through a temp file in the same directory, `Sync`ed, chmod'ed to its own mode, renamed onto `<binary>.previous`, and the directory entry made durable, and any failure in that sequence refuses the update instead of installing without a rollback path. It is recovery, not verification: nothing reads the copy again, and the rollback is a documented `mv` | `keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`, `selfupdate.go:292`, `docs/CLI.md:162-167` |
| A line count a hostile tree chose in its own shortstat | `parseCount` accepts digits only, and refuses anything over `maxPlausibleCount` (2^40) or `MaxInt`, so a 20-digit figure cannot arrive as the clamped `Atoi` result and go negative once the untracked counts are added to it | `internal/gitx/stats.go:38-50` |
| Markdown a reviewed repository or an agent wrote reaching a pull-request body | control characters stripped, flattened to one line, NFC-normalized, length-bounded, and inline delimiters escaped (`\`, `[`, `]`, `<`, `>`, `*`), so a link, an autolink, a raw tag, or an emphasis span in a note or a project `Summary:` line renders as the text it is. Every prose position, the title, the overview, and the declared scope alike, also has its backticks replaced, for the opposite reason: in prose one opens a code span that swallows what follows, and each of those positions is untrusted text that went out with the span still open | `mdText`/`escapeInline`/`mdCode`, `internal/runner/prbody.go:150-203` |
| A review that reports a note per file building a pull-request overview out of bounds | the notes are joined only while the render's own 400-rune bound is unspent, counting the separators against it, so the string is bounded by what the render would keep anyway rather than by how much the agent reported | `internal/runner/stack.go:651-680` |
| Partial binary observed by reload | two immediate identical stat readings reject visible changes, but do not prove completeness or authenticity; safe replacement depends on atomic writers and disk flushes | `internal/selfupdate/reload.go:87-134`; updater rename and sync at `internal/selfupdate/selfupdate.go:276-303` |
| Concurrent agents corrupting one tree | flock per directory with inode preservation across releases; partial-write and EINTR retry handling on note I/O; parallelism only across directories or with worktree isolation + serialized merges; worktree paths confined to `.gauntlet/worktrees`, worktree mutex synchronization, orphaned directory cleanup on add and remove; a conflict is resolved in a scratch checkout or keeps its branch | `runner/lock.go:53-175`, `worktree.go:195-208,285-308,489-518,605-639`, DESIGN.md concurrency section |
| Option injection and revision collisions in git commands | branch and revision arguments separated with `--` and `--end-of-options` across rev-parse, log, diff, merge, squash, rebase, delete, rename, trailer stripping, and reset; `worktree add` takes no `--`, so a merge target is shape-validated with `ValidateBranchName` before it is passed; a GitHub remote whose host or repository name begins with a dash is refused at parse, before the selector reaches `gh repo view`'s positional | `internal/gitx/status.go:20,39`, `internal/gitx/trailers.go:82`, `internal/gitx/branch.go:51,97,150-177,290,303,377,411,446,457,473,480-494,495,506`, `internal/gitx/worktree.go:227,321,396,534,735,756,983`, `internal/gitx/snapshot.go:41`, `internal/ghx/ghx.go:54-98` |
| Accidental launch of unconstrained run from empty filter | `blocked` returns the reason before the flag is set, and Enter consults it first, so a filter matching zero reviews cannot compose a run of all of them; the same notice is rendered in the pane | `internal/ui/pick_view.go:145-160,263,435-436,523`, `internal/ui/pick.go:356-360` |
| Reviewed tree defining its own agents | `CustomFilePath` empty without state root; argv is exec, not shell; argv, model, effort, stream, continue reject blank elements; NFC normalization of keys and names; case-insensitive duplicate field detection, single `{prompt}` placeholder requirement, `{prompt}` forbidden in model/effort/stream/continue, `{model}` and `{effort}` restricted to appropriate fields, tilde and environment variable expansion in executable `cmd[0]`, UTF-8 BOM stripped, non-empty model and usage roots, usage.suffix cannot be whitespace only, a name or note holding control or formatting characters refused, and a command-line `--agent-cmd` overriding whatever the file defined under that name | `custom.go:93-283,351-366,369-375,455-511,513-552`, `agent.go:305-316`, `cmd/gauntlet/flags.go:421-480` |
| Directory traversal or poisoned journal run IDs | run ID length bounded (<= 128), charset-restricted (`[a-zA-Z0-9_.-]`), ".." rejected; date sharding derived from run ID timestamp; journal writes flushed via `Sync()` | `internal/journal/journal.go:154`, `internal/journal/index.go:165,199,899`, `internal/journal/history.go:88-104,173` |
| History prune reaching outside the state tree | the walk yields only real shard directories and only `<id>.jsonl` names that pass `validRunID`; the index row is rewritten before its journal is unlinked, under the index lock, with a 4 MiB line cap; `keep <= 0` deletes nothing | `internal/journal/retain.go:45-56,78-198`, `internal/journal/index.go:697-740` |
| Embedded basic-auth credentials in remote URLs | userinfo stripped from git stderr strings before errors are returned, printed, or journaled | `runx.RedactUserinfo`, `internal/gitx/exec.go:327-395` |
| Known-vulnerable dependencies shipping to users | govulncheck weekly and on dependency changes in CI | `.github/workflows/vulnscan.yml` |
| Local state and secrets in the `make repro` archive | the members come from `git ls-files --cached --others --exclude-standard`, so every `.gitignore` entry is out of it, `.env` is one of them, and tests read `.gitignore` and fail on an entry whose rule no longer bites, so the list cannot drift by forgetting a new build output; the archive lives under `$(HOME)/.cache/gauntlet/repro` and is removed by an exit trap | `Makefile:628-653`, `cmd/gauntlet/makefile_test.go:1029-1108`, `.gitignore:10` |
| Silent loss of audit trail | journal as event-bus subscriber, run id + published seed for reproduction; journal failure degrades loudly, not silently | DESIGN.md "Run journal", `journal/` |
| A wedged peer parking the journal index lock forever | the cross-process index lock is a bounded poll, `LOCK_EX` with `LOCK_NB` retried to a 30s deadline, and the failure names the lock file, so `gauntlet runs`, `gauntlet history`, and the exit-time prune report a held lock instead of waiting on a peer that will never release it. The holder walks the whole journal tree on a rebuild or a prune, so on a long history or a network home the wait is not instant and the bound is not a formality | `lockIndex`, `internal/journal/index.go:56-90`, used at `index.go:88` |
| Silent loss of a `.git/info/exclude` entry | the append returns its failures, both the write and the close, and the run logs them; a short write counts as a failure, since the next run's substring check would not match a truncated line and would append the same entry again on every run. The exclusion is still best effort in the sense that nothing downstream depends on it, and the run continues | `internal/gitx/worktree.go:91-155`, `internal/runner/runner.go:281-286` |
| A discarded stack layer leaving a branch nothing names | `DiscardCurrent` reports a branch git refuses to delete instead of dropping the error; the name is cleared either way, so a leftover branch on the remote used to survive a pass in which no later cleanup looks for it | `internal/gitx/worktree.go:463-497` |
| A release inventory that describes a different build than the one shipped | `Merge` refuses a module path recorded at two versions across a release's binaries, and the command refuses a binary whose main module path is not the first one's, so a release assembled from two trees fails instead of shipping a merged or arbitrarily chosen version | `internal/sbom/sbom.go:127-152`, `cmd/sbom/main.go:30-92 |
| An index rebuild spending the only copy of a run's summary | a journal the rebuild cannot summarize keeps the row the index already holds (path refreshed), because argv, exit code, and elapsed time are on the row alone; the walk is unchanged, so a journal that is gone is still just gone | `internal/journal/index.go:864-873` |
| A prunes or a dashboard run stranding a forwarder on the bus | a run that returns without ever entering the dashboard's `Run` releases the forwarder, which otherwise parks on the program's unbuffered message channel holding the bus subscription and every event it still carries; `Release` is idempotent and kills the program before joining | `Dashboard.Release`, `internal/ui/ui.go:414-422`, deferred at `cmd/gauntlet/main.go:483` |
| The operator's account name reaching text kept on disk | the journal's free-text event field and the index's argv are shortened to `~` before they are written, and only there: the live terminal and the `--log` file keep the full path, so nothing a reader of the run needs is lost and what a backup or a `runs --json` consumer carries names no account. An occurrence is rewritten only at a whole path component, so one user's home does not shorten another's | `journaledEvent`, `cmd/gauntlet/main.go:874-882`, `journaledArgs`, `main.go:885-893`, `RedactHome`, `internal/normalize/display.go:108-143` |
| An unbounded read of a file the reviewed tree chooses | `.git/info/exclude` is read only to check for two short entries and is capped at 1 MiB rather than taken whole; the module-cache grant the release inventory reads is capped at 1 MiB, the `go list` listing at 8 MiB, and that child's pipe wait at 10s with the process group killed with it | `maxExcludeBytes`, `internal/gitx/worktree.go:576-580`, read at `worktree.go:104-112`; `internal/sbom/license.go:32-45,116-144` |
| One checkout's directory reported gone while it is still on disk | `Remove` clears the handle's directory only after the removal returned, so a failed removal leaves the caller the only record of where the checkout is; the git handle is built with the checkout rather than on first use, removing the check-then-act a second lane would take reaching the same directory | `internal/gitx/worktree.go:690-716`, `newWorktree`, `internal/gitx/worktree.go:43-58` |
| A commit decided on an unread answer about what is staged | both readers of the index go through one function, so `CommitAll` and `Merge` cannot disagree about a `diff --cached --quiet` exit, and an exit other than staged or clean is an error rather than an answer | `nothingStaged`, `internal/gitx/status.go:139-155`; `internal/gitx/worktree.go:509`, `internal/gitx/branch.go:111` |

Single point of failure: the embedded containment rules
(`internal/prompt/rules/`) carry every high-impact threat on the B1->B2 path.
They are security-relevant text, treated as such in AGENTS.md; there is no
technical backstop behind them.

## Gaps (for sec-review; none fixed here)

1. **R2, unsigned update channel.** `checksums.txt` is self-referential, and
   the provenance attestation a release now publishes
   (`actions/attest-build-provenance`, one statement per entry in
   `dist/checksums.txt`) is read by a person running `gh attestation verify`,
   not by `update`. Making the automatic path check it needs a Sigstore
   verifier inside the client, which is a new dependency against the default
   of no new dependency; otherwise document the GitHub-account trust anchor
   explicitly next to `make release` (`Makefile:547`,
   `.github/workflows/release.yml`).
2. **R5, bunx fallback fetch-and-execute** for `dsh`
   (`internal/agent/agent.go:594-680). Auto-detection already ignores it (`Installed`
   requires the binary under its own name, `agent.go:321-340`); a
   `dsh:<model>` pin additionally execs the same argv as `--dump-config`
   (`internal/agent/dsh.go:83-122`). Documentation should say plainly that naming `dsh`
   without the launcher installs and runs an npm package, including at
   probe time.
3. **No SECURITY.md.** There is no documented path from "vulnerability
   reported" to "fix shipped": no disclosure contact, no supported-version
   statement. Creating one requires an owner decision, so it is only noted
   here.
4. **R6, resource and budget enforcement.** The launch path sets no OS
   resource limits (`internal/runner/exec.go:107-111`); the optional usage
   threshold is checked between reviews and fails open on probe errors
   (`internal/runner/usagelimit.go:47-72,93-114`); `--token-budget` counts what
   the reviews report, excludes the commit and conflict launches, and is checked
   only between reviews (`internal/runner/loop.go:223-243`). None is a hard
   spend quota.
5. **Secrets hygiene around spawned agents.** Gauntlet inherits the full
   operator environment to every agent; a scrubbed env or documented
   container workflow would shrink R3. Design decision, not a bug.
6. **R9, bounded audit trail.** `--keep-runs` deletes run journals and index
   rows on every run's exit (`internal/journal/retain.go:45-56,78-198`,
   `cmd/gauntlet/main.go:926-933`), ordering by the timestamp inside the run ID
   rather than by anything the journal asserts, with no way to keep everything
   except by setting 0. A security owner who wants the history of an incident
   has to know to raise the bound before the run that shows the incident, and
   the failure to keep it is silent apart from a count in the run's own report.
7. **Journal path confinement is by name, not by owner.** `validRunID`
   (`internal/journal/history.go:88-104`) and the walk in `Prune` bound what a
   run can read and delete, but nothing on this boundary authenticates the
   state directory: a run ID collision or a planted file under a real shard is
   decided by string shape and timestamps. Run IDs now carry the whole pid
   (`runIDFor`, `internal/journal/journal.go:86-90), which removes the
   truncation collision, but the ordering property R9 names is unchanged.
   A protected `GAUNTLET_HOME` remains a precondition the code cannot enforce.
8. **The developer build surface is not in the runtime model.** `make repro`
   archives the whole working tree, so anything a developer has in their
   checkout that is not ignored lands under `$HOME/.cache/gauntlet/repro` for
   the length of the build (`Makefile:628-653`). The members are git's
   ignore-aware listing, which covers the entries
   in `.gitignore`, a derivation, not a guarantee: an untracked
   credentials file, a private key, or a second `.env` under another name is
   copied with no redaction and no scan. This is a developer convenience
   target on this repository, not a path any reviewed repository reaches, and
   the copied trees are removed on exit.
9. **The shipped inventory is a claim, not a signature.** Every release now
   ships `dist/sbom.json`, generated from the built binaries' own build info
   (`internal/sbom/sbom.go:98-125`, `cmd/sbom/main.go:47-86`). It is the
   dependency view a scanner and a release-page reader need, and it is worth
   having. It is not a second integrity control for R2: the publisher supplies
   both the binary and the document, `Merge` only refuses a module path
   recorded at two versions across the release's binaries, and nothing binds
   the binaries that were inventoried to the binaries `checksums.txt` covers
   beyond the same `dist/gauntlet_*` glob. A reader who treats the document as
   evidence of what shipped is reading a claim from the party that signs
   nothing. Its write also lacks the hardening the runtime `--log`
   destination has: `os.WriteFile` at 0644 (`cmd/sbom/main.go:86`) follows a
   symlink at the destination and leaves a pre-existing file's mode alone,
   where the log destination is refused as a symlink or non-regular file and
   opened `O_NOFOLLOW` at 0600 (`cmd/gauntlet/main.go:713-733). The rest of
   the release target's output is no tighter: `checksums.txt` comes from a
   shell redirect over `dist` (`Makefile:563-569`), which follows a symlink
   too. Exploitability is low, since reaching either needs a compromised
   release workspace, which is the same attacker R2 assumes; it is recorded
   because a reader should not learn two different answers to "can a planted
   path redirect a write" from one repository.

10. **The rollback copy is trusted by name.** `<binary>.previous` is written
    atomically and refused-if-unwritable, and then never read again
    (`keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`). The
    documented rollback renames it into place over the installed binary with no
    digest, no signature, and no version check (`docs/CLI.md:162-167`), and it
    is one release old, so a rollback is also a way back to a version whose
    known vulnerabilities are fixed. The writer is not the weak part: the copy
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
  (`cmd/gauntlet/flags.go:640-654`), the block names at most 20 entries and
  counts the rest rather than reading narrower than the flag
  (`internal/prompt/compose.go:148-245`), and an entry that survives is
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
  (`internal/runner/commit.go:228-236`). Those checks do not inspect the diff
  for secrets or authorize its content. The same-user agent can also publish
  directly despite the prompt prohibition. Containment is advisory, not a
  credential or network restriction (`internal/runner/exec.go:107-111`).
- **Consent-surfaced commit.** Refuse `--jobs` on a dirty tree and gauntlet
  offers to hand that tree, unreviewed, to an agent that commits it; `--yes`
  or `--yolo` on the original command is that consent, so an operator who
  scripts those flags has pre-approved agent-authored commits of whatever
  the reviews left behind (`cmd/gauntlet/main.go:960-984).
- **Suggestion gaming.** A planted prompt whose description primes
  `RELEVANT:` output steers which reviews auto-run; the grammar check and
  known-set filter (`compose.go:372,381-396`) bound it to reviews that exist
  in the discovered set, including the attacker's own.
- **Signals: steering.** A planted `Signals:` line on a project prompt is
  parsed into the file-signal suggester (`prompt.go:199-232`,
  `matchDeclared` in `evidence/suggest.go:556-600`). Charset, count, and length are
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

- **Rollback by name.** An actor who can write the install directory (the
  publisher R2 already assumes, or a local account sharing it, or an agent that
  reached the user's `PATH`) plants `<binary>.previous` before the operator runs
  the documented `mv` after a bad release. The move installs that file: nothing
  between the copy and the rename re-verifies it, and the copy's own integrity
  was established for a download that happened earlier
  (`keepPrevious`, `internal/selfupdate/selfupdate.go:322-367`; the instruction
  at `docs/CLI.md:162-167`). The same file is a downgrade with no attacker at
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

None of these is demonstrated here; evidence is the cited code paths.

## Document status

- SECURITY.md: absent. Claims to correct: none found elsewhere; README's
  "Trust model" section was re-checked against the code on 2026-09-30. Two of
  its claims had drifted. It named a `flock` on `.gauntlet.lock` as what keeps
  two runs out of one directory, which on macOS is not the control: a `flock`
  there belongs to the process, so a second acquisition converted the lock
  rather than failing and a run that locked one tree twice carried on with two
  sets of agents in it. The README now names the per-process registry beside
  the `flock`. It also listed three of the safe git overlay's keys
  (`core.fsmonitor`, `core.hooksPath=/dev/null`, `diff.external`,
  `core.pager=cat`) without the signing program, which is the one driver in
  that list no attribute file covers; the README now names it. The rest of the
  section is unchanged and still matches: `O_NOFOLLOW` prompt reads,
  cwd-free PATH resolution, display sanitization, and the control-character
  and bidi stripping, which now covers every child stream including the
  indexer's.
- Response readiness: the journal supports run reconstruction, not a complete
  security audit, and it is now bounded: `--keep-runs` evicts the oldest rows on
  every run's exit, default 200 (`internal/journal/retain.go:45-56,78-198`). Agent
  output and live usage events are explicitly excluded
  (`cmd/gauntlet/main.go:507-520`), writes are buffered, and write failures are
  retained for reporting at close (`internal/journal/journal.go:305-341`).
  It does not authenticate agent claims or record every child action; same-user
  agents can alter local evidence. The disclosure and supported-version gaps
  above remain undocumented organizational decisions.
- Verification scope and baseline: 2026-09-30 against commit 0e6754c. This
  pass read the thirteen commits since b49e8cc. It entered the signing-program
  exec as a closed path in the git-config row, the entry-point row, and the
  mitigations map, because it was the one exec a reviewed config reached with
  no attribute file involved and the previous passes described the attribute
  file as covering the config's drivers. It entered the macOS `flock` asymmetry
  twice, once as a double-lock in one tree and once as a prune moving a live
  run's journal, both of which the model had described as closed on the
  strength of a kernel primitive whose ownership model differs between the two
  platforms the project supports. It carried the two `O_NOFOLLOW` appends, the
  bounded conflict list, and the one-review-per-run index count. It changed no
  risk row: R1 through R9 all stand where they stood, and the four new
  controls narrow no numbered risk. The re-anchoring is the larger part of the
  diff. The thirteen commits moved line numbers in eleven of the files this
  document cites, and most of the pointers had slid into the wrong function,
  which is what `TestThreatModelPointersResolve`
  (`cmd/gauntlet/threatmodel_test.go:31-67`) cannot see: it catches a pointer
  past the end of a file, not one that landed in the function after the one it
  named. Every pointer was re-read against the code it names, and the ones
  that had moved are cited on the symbol now.
- Earlier baseline: 2026-09-29 against commit b49e8cc. That
  pass read the twenty-one commits since 77fa164 and entered the two surfaces
  the model had never named, the account name the journal and the index keep
  about the operator, and the license inventory's subprocess and whole-file
  reads. It carried four controls those commits added: the capped
  `.git/info/exclude` read, the checkout handle that keeps its directory until
  the directory is gone and builds its git handle eagerly, the one reader both
  commit paths use for what is staged, and the bounded stacked-PR overview. It
  narrowed R6 to one ceiling per run rather than one per directory, without
  closing it, and re-anchored the pointers the self-update, worktree, flag,
  and normalizer commits had moved.
- Earlier baseline: 2026-09-28 against commit 77fa164. That pass read the
  thirty-six commits since 2ec5135 and added no surface, no boundary, and no
  risk row. It re-anchored the source pointers that had drifted into six
  files, and it entered the two controls those commits changed: the command
  line overriding a definition file, and the temp sweep deciding regularity
  from the lstat.
- Earlier baseline: 2026-09-28 against commit 2ec5135. This
  pass read the twenty-seven commits since 41faffe. It entered two surfaces
  among them: the `<binary>.previous` copy an update leaves in the install
  directory, which the rollback the documentation gives installs by name and
  without a second verification, and `gauntlet runs --json`, the first output
  here meant for a program, which carries the reviewed tree's directory names
  and each run's argv to a consumer. It carried four controls those commits
  added: the inline Markdown escaping in a pull-request body, the refused
  clamped shortstat count, the prompt rule that an auto-fix allowance must
  name the source of the value it edits, and the release target's refusal of
  the default version. It closed no risk: R2's unsigned channel now has one
  more file in it, and the numbered table is otherwise unchanged.
- Earlier baseline: 2026-09-27 against commit e35371d. This
  pass read the twenty-three commits since c36fa56. It entered the operator's
  `--paths` scope as an entry point, a fence, and an abuse case, because the
  entries are pasted into a prompt as instructions and a wrapper that builds
  the list from a file feed is free text reaching an agent with its permissions
  bypassed. It carried four controls those commits added: the repeated marker
  escape, the lane's drop of its unstarted queue when the usage probe trips,
  the sanitized untracked-path note, and the index rebuild that keeps the row a
  journal cannot summarize. It re-anchored the citations the composition,
  normalization, journal, reporter, and dashboard edits moved, and closed no
  risk. The numbered table is unchanged: R1/R3/R4's core, R2's unsigned
  channel, R5's `bunx` fallback, R6's unenforced spend, and R9's bounded audit
  trail are all still open and still named where they were.
- Earlier baseline: 2026-09-27 against commit c36fa56. That
  pass read the fourteen commits since ef6eb5c. It entered the release-time
  inventory generator as an entry point, a boundary note, a mitigations row,
  and a gap, because every release now ships a document describing its own
  dependency surface and nothing in the model said what that document is
  worth. It carried four controls those commits added: the single sanitize
  point every commit subject passes through, including one read back out of
  history during a stack recovery; the bounded index-lock wait; the reported
  `.git/info/exclude` failures, short writes included; and the reported
  branch-deletion failure in a discarded stack layer. It corrected the
  `make release` pointer the Makefile's own edit had left stale, which was
  holding `TestDocsPointAtTheMakefileLineTheyName` red, and re-anchored the
  `make repro` and `.git/info/exclude` citations that moved with the same
  edit. It closed no risk and changed nothing in the numbered risk table.
- Earlier baseline: 2026-09-27 against commit ef6eb5c. That
  pass read the eight commits between 93b004c and that commit. It entered `make repro` as an
  entry point, a mitigations row, and a gap, because a target that archives
  the whole working tree is a place secrets go and the document had said
  nothing about it. It carried two controls that landed in those commits but
  were not in the model: the attribution check on an agent-supplied commit
  subject, and the widened conflict-marker scan. It corrected one pointer that
  had left a test red (`make release` at `Makefile:547`), re-anchored the
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
