# Changelog

Notable changes per release. Versions follow SemVer against this consumer
contract: review names (`*-review.md` stems consumed by `--reviews`), set
names (`quick`, `standard`, ...), CLI flags and their documented behavior,
the documented environment variables, exit codes, and the event stream under
`~/.gauntlet/runs`, which `docs/RUNS.md` tells operators to read with `jq`
or their own tools. Removing or renaming any of these is breaking and waits
for a major version; new flags and other additions may land in a minor. While
the project was 0.x, other behavior changes could land in a minor instead and
were listed under Changed.

There is no Go API in this contract, because no other program can import
anything in this module: every package is under `internal/`, and the ones
outside it, `cmd/gauntlet` and `cmd/sbom`, are `package main`. Signatures under `internal/`
are refactored freely: they change without a major bump, and each change
lands in Changed for anyone vendoring the tree rather than going unrecorded.
`TestNoAccidentalPublicPackages` in `cmd/gauntlet` fails the build if that
stops being true, which is the moment a major version would be owed.

Each part of the contract has a snapshot that fails the suite when it moves,
so a breaking change cannot reach a tag unnoticed: flags, commands, exit
codes, and environment variables in `cmd/gauntlet/contract_test.go`, review
and set names in `internal/prompt/contract_test.go`, and the `ev` values of
the journaled event stream in `internal/runner/contract_test.go`.

## Unreleased

### Fixed

- `gauntlet pick` reports a repository it could not read instead of showing
  it as a clean tree. The launcher asked git for the branch, the merge
  targets, and whether tracked files were dirty, and any failure answered all
  three as "none of it": the screen then said `this checkout` over a tree
  nobody had read, so a run composed against it could be planned as branchless
  and clean when git had simply refused. A failing git is named and the
  command stops; a directory git does not manage is still the empty answer it
  always was, since there is no branch there to offer.
- `gauntlet pick` reports the project prompts prompt discovery dropped. The
  launcher discarded the warnings a hand-typed run prints, so a project review
  whose name is not printable text, and one of two files where a name
  conflicts, silently vanished from the reviews the screen offered and the run
  went on to schedule.
- A review scheduled twice is counted twice again in `gauntlet runs`. The
  index row folds a repeated `review_end` into one review so a hot-reload
  successor does not report work its predecessor already counted, and the
  key it folded on is the directory, loop, review, and branch. A weighted
  review run in place shares all four with its own first pass, so the second
  pass vanished from the row along with its tokens, its lines, and any
  failure it recorded. The row is now the statement about the run the
  runner's own tally already makes, and the replay it still folds is an
  ending a later process wrote over a review an earlier one was interrupted
  on. `docs/RUNS.md` states the rule and what a journal with no `run_start`
  and no timestamp does with it.
- `GAUNTLET_HOME` set to a bare `~` is refused at startup instead of quietly
  putting the state tree in a directory called `~`. The tilde expands only as
  `~/...`; a value left a tilde (a bare `~`, or another account's `~user`) was
  read as an ordinary path and made absolute against the working directory, so
  the run journal, the hot-reload handoff and `agents.json` all landed in a
  directory named `~` inside the repository under review, while `gauntlet
  doctor` reported a working setup. The refusal names the `~/` spelling that
  works.
- The startup check on `GAUNTLET_HOME` now asks the resolver that every later
  read of the root goes through, rather than holding its own copy of the rule.
  The two answers had drifted: a value startup accepted could still leave a
  run journaling into a fallback directory with nothing said.
- `make check-workflow-shell` read a `run:` only where it started a line, so a
  step written `- run: |` was never linted, and every one of release.yml's
  four bodies is written that way: the release notes, the tag guard, the smoke
  test, and the step that publishes the release. shellcheck reported five
  bodies where the workflows hold nine and said nothing, so the shell that
  decides whether a tag is published was the shell nobody checked. The
  extractor now reads the key and the block indicator wherever the step puts
  them, takes the body's cut from the indent its own first line carries rather
  than a fixed ten spaces, and refuses a body that came out empty by name,
  because shellcheck reads a preamble-only script as a clean one. The five
  bodies it already linted are unchanged. `cmd/gauntlet` holds all three:
  the two spellings and a two-indent nesting, run through the target's own
  recipe, and a masked command substitution in one of them failing the lint.
- The project page test read up to eight parent directories with an index
  counter `go fix` rewrites to a range, which made `make check` red on every
  tag set before it got as far as vet.
- The launcher's scrolled panes now say how far they are scrolled, and which
  end the rows went. Each pane title carried a count of the rows below the
  fold only, so a list scrolled to its end still read "+N more" while the N
  rows it held were the ones already scrolled off the top. A pane now reports
  the rows above it, the rows below, or both, and says nothing when it holds
  everything it has, matching how the dashboard's feed title already reports
  its scrollback and how the help overlay names a page.
- The launcher's review filter answers a near miss with the review it meant.
  Typing a review name one edit away returned "no reviews match this
  filter" and stopped there, on the status line, in the reviews pane, and
  while the filter was still open, although `--reviews` and the agent spec
  parser both name the nearest match. A filter nothing resembles still gets
  the plain sentence, so a suggestion stays an answer rather than noise.
- The project page was below the contrast floor in both color schemes and had
  no keyboard focus ring. `--muted` was 4.27:1 and `--accent` 4.46:1 on the
  light background, under the 4.5:1 a body text color owes (WCAG SC 1.4.3);
  both are darker now and the dark scheme's `--muted` is lighter. The links
  had no focus indicator of their own, so every link on the page was tabbed
  past with nothing on screen to say where the keyboard was (SC 2.4.7), and the
  quick-start block scrolled sideways at 320px instead of wrapping, taking the
  page's reflow with it (SC 1.4.10). `cmd/gauntlet` holds the page to the same
  floors `internal/ui` holds the terminal palette to, so a color edited there
  now fails the suite.
- Truncating a line cut on a character, not a byte: `normalize.Clip` and
  `normalize.Truncate` now repair bytes that are not valid UTF-8 before
  measuring the cut, so what they return is always text. A file name holding
  one raw byte is legal on ext4 and APFS, and a cut landing next to it put a
  half-written character into a commit subject, a pull request body, or the
  lock note another run reads, where the terminal and every width measurement
  downstream have to guess what they are looking at.
- Every `Makefile:` line reference in `docs/THREAT_MODEL.md` past the release
  target had drifted, so a reader following one landed on the wrong recipe: a
  seven-line recipe added ahead of that target moved each of them, and
  `TestDocsPointAtTheMakefileLineTheyName` was red for every pointer it checks.
  The pointers are re-anchored, and one that named the comment above the
  checksums recipe now names the recipe.
- `make smoke` resolved the asset to execute from `go env GOOS`, which echoes
  an in-flight cross-compile target from the environment. An exported `GOOS`,
  or `dist` setting it per target, named a binary that cannot run on the
  machine executing it, and the release gate refused a healthy release over a
  file it was never going to run. It resolves from `GOHOSTOS`/`GOHOSTARCH`.

- Truncating a line cut on a character, not a byte: `normalize.Clip` and
  `normalize.Truncate` now repair bytes that are not valid UTF-8 before
  measuring the cut, so what they return is always text. A file name holding
  one raw byte is legal on ext4 and APFS, and a cut landing next to it put a
  half-written character into a commit subject, a pull request body, or the
  lock note another run reads, where the terminal and every width measurement
  downstream have to guess what they are looking at.

- Every `Makefile:` line reference in `docs/THREAT_MODEL.md` past the release
  target had drifted, so a reader following one landed on the wrong recipe: a
  seven-line recipe added ahead of that target moved each of them, and
  `TestDocsPointAtTheMakefileLineTheyName` was red for every pointer it checks.
  The pointers are re-anchored, and one that named the comment above the
  checksums recipe now names the recipe.

### Changed

- `prompt-review` no longer tells an agent that a prompt the loader cannot dispatch is one "a name the runner's name list does not carry": the bundled set is a file glob and a golden list is a test that fails the build, so neither is a dispatch gate, and absence from either is not a defect. The skip conditions the loader actually applies are named instead (skipped directory, wrong suffix, symlink, gitignored file), the review tells an agent how to find the three sources before judging them, the auto-fix line says which bounded edits to make on the spot and which to report, and the tool line points at the loader's own source when the runner's binary is not on PATH.
- The dashboard's braille charts render each cell's escape sequence once instead of once per cell per frame. A busy frame (eight lanes, a 120-column activity strip) drops from about 5,770 allocations to about 3,505, and the chart itself from 877 to 53, measured by `BenchmarkView` and `BenchmarkChart`; the drawn output is byte-identical, and `--no-color` still drops the table with the color profile.
- The heat ramp is one table with one set of cut points, so the color a meter
  draws and the index the chart's glyph table is keyed by cannot drift apart:
  `internal/ui`'s `heatColor` is now a lookup over `heatIndex`, and
  `internal/gitx`'s `openRegular` and `openAppendNoFollow` are gone, their
  call sites reaching `safefile` directly.
- `make check` runs staticcheck with `-checks=all` instead of the tool's
  default set. The defaults leave the style and quickfix groups off, so a doc
  comment that stopped naming the symbol it documents would never have failed
  a run; the tree passes every check the analyzer carries under all three
  build-tag configurations, so the whole set is now the gate, and a rule a
  later staticcheck release adds fails `make check` rather than staying off
  until somebody reports it. Four doc comments over `internal/gitx`'s
  `MinVersion` and `DeleteMergedBranchesMatching` and `internal/report`'s
  `Palette` and `Think` were the findings it revealed.

### Security

- The run journal kept the whole command line, home directory aside. An
  operator defines a wrapper agent by handing its key on the command line,
  because that is where the agent CLIs read it, so `--agent-cmd` and a
  credential-bearing remote reached `index.jsonl`, `gauntlet runs --json`, and
  `gauntlet resume` verbatim, and outlived the run. Credentials are now
  redacted from a journaled argument, and the redaction covers the shapes a
  command line uses: the value after a flag that names one, and the userinfo of
  a URL. The resume checkpoint still holds the real argument, because it is
  replayed; it is written owner-only and is now shown redacted.

- The agent filesystem sandbox granted `/tmp` unconditionally. On macOS that
  directory is a symlink to the shared, world-writable `/private/tmp`, not the
  per-user temporary directory the agent writes in, so every sandboxed agent
  could reach every other user's scratch files. The root is now the platform's
  own temporary directory (`os.TempDir`, an absolute `TMPDIR` or `/tmp`), an
  operator-set `TMPDIR` still adds its own directory, and a temporary directory
  the host does not have is skipped instead of refusing the launch.
- The project site is served with a Content-Security-Policy that allows only its own styles and images, plus `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, and HSTS, through a `_headers` file Cloudflare Workers Static Assets applies to every asset.

## 1.34.1

### Fixed

- Built-in suggestions now carry their evidence-based priority into the weighted review schedule instead of leaving every selected review at one pass. Accumulated file evidence and past review outcomes determine 1–3 passes; read failures and reviews that repeatedly finish without changes stay at one. The preview shows repeats, manual entries add to them, and `--max-reviews` still caps the schedule.

## 1.34.0

### Changed

- Suggestions default to the built-in file-signal suggester when `--suggest-agent` is omitted. `--list --suggest` works without an installed agent CLI; an explicit suggestion agent still uses that model. The launcher shows the built-in default.
- File-signal suggestions follow each review's applicability scope: code-quality reviews recognize more source languages; test, skill, prompt, specification, runtime configuration, UI, API, and packaging reviews require their own evidence. Package/build metadata and native or browser interfaces contribute signals. Dependency, scratch, and bundled compiler trees are excluded even when tracked by git.
- Built-in capability markers respect identifier boundaries, so `plugin.` does not imply Gin and "rediscover" does not imply Redis. File `readline()` and plain Rich console output no longer imply an interactive terminal UI. Declared `mark:` signals keep their literal substring behavior.
- File-signal suggestions recognize source import declarations and package fields. Go imports use the standard parser; other supported declaration forms use bounded recognition. Manifest descriptions and dependency names alone no longer establish implementation subjects; public exports, executable entries, package versions, and dependency sections contribute their own evidence. Private package exports do not establish a public SDK, and incomplete JSON is left undecoded. Importable Go root interfaces and explicit module versions contribute SDK and release evidence. Declared literal marks remain separate from derived capability categories.

## 1.33.0

### Added

- Suggestion agents assign review weights of 1–3 passes per loop based on expected review value. The preview shows repeats, manual review entries add to those weights, and older unweighted suggestions still schedule one pass. Explicit zero weights skip a review; malformed or out-of-range weights are ignored.
- Documented Linux memory limits for review runs using a systemd scope, including the shared cap across agents and subprocesses, swap control, and possible out-of-memory kills.
- Added the project website with installation guidance and a dashboard preview.

### Changed

- File-based suggestions recognize more source formats and native GUI code, and use parsing, side effects, resource acquisition and personal-data signals for specialized reviews.

### Fixed

- macOS checks now export the closed Go flags with make 3.81, isolate the credential-helper fixture from system git config, and skip the raw-byte filename fixture only when the filesystem refuses it. The recursive-analysis fixture pins the make command independently of its inherited path.
- Release builds keep the test scratch directory out of the compiler environment, so a fresh host that exports TMPDIR can build artifacts before running tests.

## 1.32.0

### Added

- `gauntlet resume` continues a run that was killed without a word: an OOM kill, a crashed desktop session, a power cut. Every recorded result and every finished loop now rewrites a checkpoint under `state/checkpoints/`, the hot-reload handoff plus the command line and working directory, with the reviews whose agents were running counted unfinished. A run that ends on its own deletes it. `gauntlet resume` with no id lists the runs that have one and whether a gauntlet still holds their directory; `gauntlet resume <run-id>` changes to the run's directory and hands over to this binary the way a hot reload does, keeping the run id, schedule, seed, results, and loop budget, and running only what the kill left unfinished. A held directory is refused with exit 75.

### Changed

- The run journal is flushed after every review, not only at the end of a loop, so a killed run keeps the events of every review it finished.
- A run that was handed over (a hot reload or `gauntlet resume`) starts its lane and review branch names with `gauntlet/<run-id>-g<N>-l<loop>`, `N` counting the processes before it. A resumed run keeps its run id, and the branches its killed predecessor left sit at an older base or carry commits, so reusing their names was refused and the loop fell back to reviewing in place. A `--jobs` run that was handed over also deletes the branches an earlier process of the run left that HEAD already contains, with `git branch -d`, so one carrying a commit HEAD lacks is kept.
- `journal.ValidRunID` is exported, for the checkpoint path.

### Fixed


- `--stacked-prs` keeps going when a layer cannot be published. A failed push leaves that commit on its branch, records the layer as failed, and later reviews in the pass still run from the last base that did push. A push that landed whose pull request a head/base lookup cannot see stays in the chain, and the scratch checkout stays on disk until that lookup succeeds; a later pass of the same run reuses it, including when that checkout was reached through a symlink. The checkout is removed only after every changed layer was pushed and confirmed. A review that edits the launch checkout, or reports a file edit the scratch checkout does not contain, fails that layer without ending the pass, and the launch checkout is left as it was written. A URL printed by `gh pr create` is confirmed with a second head/base lookup. A push that landed whose tip cannot be read stops the pass instead of branching the next review from the previous base. A discard git refuses on an empty layer does the same, and the checkout stays.

## 1.31.0

### Added

- Agents now run in a filesystem write sandbox by default: Landlock on Linux and Seatbelt on macOS, inherited by shell commands and MCP children. Sandbox setup failures stop the launch. `--sandbox-write DIR` grants additional writable directories; `--no-sandbox` explicitly disables confinement. An absolute `TMPDIR` is also a writable root. Reads and networking remain available.

## 1.30.0

### Added

- `--token-budget` now stops the review that reaches it. The ceiling was a scheduling bound: it was read before a loop, a review, and a retry, so it decided what started next and never what was already running, and a single launch an agent kept extending was billed for every turn until its timeout killed it. A review whose own reported tokens reach the whole budget is stopped, the run logs `OVER BUDGET` and starts no successor, and the launch is not retried, since a second attempt at the same review costs what the first one did. The figure that stops work is the provider's own: the machine-readable usage envelope and the session transcript, both written by the provider rather than by the model, so a number the model printed cannot end the review it was printed in. An agent in prose mode (`--stream=false`) reports no such figure and stays bounded by its timeout, as before.

- `make check-workflow-shell` lints the shell a workflow `run:` step executes, and `make check-scripts` and the CI scripts job run it. The five `run:` bodies in this repository are bash that a tagged release runs, four of them deciding what is published, and until now nothing read them: the Go build never parses a workflow, yamllint reads the YAML around a body rather than the shell inside it, and the only script shellcheck was given was `scripts/shots.sh`. Each body is written out under the test scratch directory and shellchecked with the flags `shots.sh` gets. The release guard's message now reads the commit it names into a variable before printing it (a command substitution inside the `echo` reported a failing `git` as an empty commit), and the publish step's prerelease `case` names its default branch instead of leaning on the `case`'s own zero status.

- `gauntlet doctor` names the review branches a run kept after a merge that did not land. A review whose merge conflicts or fails leaves its work on a branch under `gauntlet/`, and a run deletes that branch once the review lands, so what survives is exactly the work no merge carried. It is the one output no copy of the state tree holds: the journal records the branch name and nothing about the commits on it, and the run that left it behind is long gone by the time a machine is retired. Doctor listed the run history and the recoverable pruned runs and said nothing about this; it now prints the branches, up to five of them and a count for the rest, and the one command that copies what is on them, `git bundle create <file> --all`. Stacked layers under `review/` are not listed, since a stacked run publishes each one as a pull request and the remote holds those commits.

- `make test-fast` runs one package's tests without the race detector, for the loop between two edits rather than the check before a push. `make test-pkg` is the only per-package target, and it pays for the detector every iteration: `internal/runner` alone is 373s of the 6m18 `make test` measured on Linux. The one command that costs less was a bare `go test`, which leaves off the `-tags sqlite` this Makefile passes, so the loop a contributor actually runs tested the no-database build rather than the default one. `test-fast` keeps the tags, the shuffled order, the scratch directory, the package guard, and the refusal a mistyped `RUN=` gets, and drops only `-race`; it is not a gate, and `make test-pkg` is what runs before pushing.
- `make install` takes a `BINDIR`, so a machine that keeps binaries outside `~/.local/bin` can install there: `make install BINDIR=/usr/local/bin`. The default is unchanged, so the documented source install writes where it always did, and a system install writes wherever the caller can write rather than into a directory the Makefile picked.
- A release now uploads the license text beside its binaries, and `sbom.json` names the subject's grant. The AGPL asks a distributed work to carry the terms it is offered under, and a consumer who installed the binary alone had no way to read them; the CycloneDX inventory listed a license for every linked module and none for the program itself, so a scanner reading it saw the terms of the dependencies and not of the artifact. `make artifacts` copies `LICENSE` into `dist/`, the release uploads it with the binaries and the checksum list, and the inventory's subject component carries `AGPL-3.0-or-later`.
- `gauntlet doctor` reports the git version and the floor it needs. Every call that takes a ref or path the reviewed repository supplied separates it from the options with `--end-of-options` (2.24), and branches are created with `git switch` (2.23), and the README states the floor, but nothing read the installed version: a git older than that rejected each call with `unknown option: --end-of-options`, an error naming neither git nor a version, at the first step of every review. The version now sits under Core tools, red below the floor, and a git whose version cannot be read says so rather than passing.
- `make doctor` reports every missing contributor prerequisite in one run, and what to install and which targets need it. A new machine discovered the Go minimum, the C compiler the race detector needs, git, `uvx`, shellcheck, and a writable disk-backed scratch directory one failed command at a time, and the preflight that already covered them fired only on the target that wanted each one. Every gap is now reported before the target fails, the checks that passed are still printed, and the exit status is 1 when anything is missing, so a script can gate on it. The Go comparison, the version to name, and the tool pins are the ones `toolchain-min` and `check-scripts` already use, so the three cannot report three different answers about one machine.
- Lane narration in the run journal now names the review and the lane it was published from (`review` and `lane` on a `log` event, 1-based, absent on a sequential run). The lanes of a `--jobs` run publish concurrently, so which lane's line lands first is the scheduler's choice and two runs of one seed interleave them differently; a line naming no review could not be placed back in either recording. Run-scope narration, which is about the run rather than one lane's work, is unchanged and still names no review.
- The pull-request gate and the advisory scan can be started by hand from the Actions tab, so a runner-image or package-index incident is answered by re-running the workflow rather than by pushing an empty commit. A manual run no longer cancels the push it repeats: the event is part of each workflow's concurrency group, since both resolve to the same ref.
- A conflict resolution the agent finished is no longer deleted when the merge that would carry it in still fails. The resolution is one commit nothing else holds, and the scratch branch it was built on was removed on every exit, so a merge git refused for a reason no edit to the conflicted lines can clear (an untracked file it would overwrite) destroyed the resolver's work and left the review reported as a conflict with nothing resolved to show. The branch is now kept, named in the run's log, and printed with the command that lands it, the way a review branch a plain conflict leaves behind is.
- A state tree can now be checked for a copy that is short. A journal is only ever written whole lines, so a file ending mid-line was cut, by a power cut or by a backup job that copied the tree with a run still writing to it. Nothing reported such a file: the half-line is not JSON, so the run lists, replays, and summarizes as a complete run that is missing its last events, and the counts a restore is checked against (journals, rows, disagreements, pruned runs) are all the ones a whole archive and a short one share. `gauntlet runs --json` carries a `truncated` count in its `history` object, `gauntlet doctor` names the count on its run history line, and an empty journal is not one, since a run that has recorded nothing has lost nothing.
- Every `.go` file is held to the two-line license header `AGENTS.md` states, by `TestEveryGoFileCarriesTheLicenseHeader`. ruff's `CPY` rule has held the scripts to the same notice since the rule was selected, and nothing checked the 173 files carrying the rest of the tree, so a file added without them read the same as one that carried them. The walk covers the module's own directories and skips a review lane's checkout, a scratch tree, and `dist`, so it cannot report on a tree this repository does not ship.
- `make check` runs `staticcheck` under all three build-tag configurations, beside `go vet` and `go fix`. `go vet` is the compiler's own set of checks and answers only what the compiler can see; a mistyped verb in a log line, a value assigned and never read, a conversion the type already guarantees, a branch that cannot be taken, and dead code all compiled and ran clean, and nothing in the tree reported them. The analyzer is fetched with `go run` at the version pinned in `STATICCHECK_VERSION`, the way `make vuln` fetches `govulncheck`, so a contributor installs nothing and CI and a local run read the same number. The first run over this tree found nine: a discarded read result in the duplicate-prompt comparison (fixed below), an unused test helper, a loop the analyzer could fold, a deprecated `runtime.GOROOT` in a test, and four bidirectional fixtures written as a literal invisible character rather than the `\u202e` escape the rest of the tree already used.

- A flag's default is held to the contract as its name is. Every other snapshot on the CLI surface watches names, and a default is the half a consumer relies on without typing anything: raising `--keep-runs`, retrying a failed review a third time, or repointing `--update-repo` changes what every run does, and no name moves, so nothing in the suite could see it. `TestFlagDefaultsMatchTheContract` reads the parser's own `DefValue` for the defaults `docs/CLI.md` spells as a value and requires the documented cell to say the same. A default the page words rather than spells (`unlimited`, `off`, `none`, `auto-detect`, a duration) is a reader's to judge, and a change to one shows in the help screen and lands here.

### Fixed

- Twenty-one pointers in `docs/THREAT_MODEL.md` into `release.yml` and the `Makefile` named lines that had moved, and the two tests that hold a doc pointer to the thing it names were red over them. The publish step grew the draft refusal and the explicit prerelease branch, and the `Makefile` grew `make doctor`, `make test-fast`, and `make install BINDIR`, so the release build, the publish step, `make repro`, `make doctor`, the platform check, the license copy, the checksum redirect, and `make install` all sat under citations pointing somewhere else. Every one names its line again, and the sentence about `release-version`, which the 200-character window attributed to `make release`, now cites the target it is about.
- `gauntlet pick --dirs a,b` is a usage error instead of a run over the first path. The launcher composes one run for one tree and took `resolvedDirs[0]`, so a list naming several directories was silently narrowed to one and the run that started covered a different set of trees than the ones asked for. Several is now refused with exit 2, the count named, and `docs/CLI.md` records the change.
- `gauntlet help <topic>` refuses a topic that names no command. A misspelled word was read as a plain `gauntlet help`: the whole screen printed and the process exited 0, so a request for the wrong page reported success. A known command is answered by the same screen, whose SUBCOMMAND FLAGS section marks that command's own flags, and an unknown one is a usage error with the topic list and a "did you mean" from a close misspelling.
- `gauntlet runs --restore` refuses to be given twice. The flag package keeps the last value of a repeated flag and forgets the rest, so `--restore a --restore b` restored `b` and reported the failure against `b` alone, leaving the reader no idea `a` had been asked for. The flag now counts its own occurrences, the way `--bin` and `--agent-cmd` already do.
- The help screen counts the bundled review prompts from the embedded set rather than a number typed into the string, which went stale whenever a review was added.
- A seeded `--jobs` run reports a weighted review's two rows in the same order on every replay. A review named twice in one loop is two lanes under one name, and the report and the failure list sorted those rows by the order the results arrived, which is the order the OS scheduler let the lanes finish in, so two runs of one seed printed them the other way round with each row's branch, elapsed time, and line counts swapped. Both lists order same-name rows by the branch the review ran on, which is assigned from the seed when the queue is built; the tie only exists in a parallel run, since a sequential run records no branch and keeps its own order. Name ordering is a total order now, in the sort every list of review, set, and agent names goes through: two spellings of one name the collator calls equal (an accented letter as one code point and as a base letter plus a combining mark) fell back to the order the list arrived in, and the lists built by ranging a map arrived in Go's map order, so a name could print in a different place on every run.
- A launcher probe that failed is retried after a clock steps backwards. The provider a `dsh:model` launcher resolves to comes from a probe of the launcher's headless config, and a failure is memoized for 30 seconds so a run does not pay a subprocess per launch. The window was measured as a plain difference, so a clock set back (an NTP correction, a manual `date`) left the memoized reading in the future and the age negative, which compares below the window for as long as the process lives: the failure was then kept for the rest of the run and a launcher a later probe would have resolved stayed unresolvable. The age is now only inside the window when it is not negative, the way the git sampling cache and the output rate limiter already guard theirs.
- The refusal of a planted file node is written once. Reading a project prompt, reading a run journal, and reading or appending a shared exclude each had their own copy of the guarded open, and the copies had drifted: one returned a bare errno, so a failure reached an operator as "bad file descriptor" with no path to act on, and another discarded a failed clear of `O_NONBLOCK` on the grounds that there was nothing to do about it, which is the same refusal the other two treated as an error. They now share `internal/safefile`, where every refusal carries the path, and the append gained the `O_NONBLOCK` the reads already had: a FIFO committed where the exclude goes blocked the untracked walk until a writer appeared, which for a planted node is never.
- `gauntlet runs` lists what it could read when part of the tree it reads is not readable. A run journal or a shard directory that cannot be opened made the whole listing an error and printed no runs at all, naming one bad entry as if the operator had never run anything, and the same failure silently stripped every review's history from `--suggest-agent gauntlet`. A journal the walk could not read has travelled beside the batch since the walk started returning both, and `Recent` and `History` are the two readers that threw it away. Both now carry the runs that did read, name what they missed, and fail the listing's exit code. The plain reporter's loop footer printed `complete in 0s` for an elapsed the decoder had refused, which is a measurement the code declined to make; it reads `n/a` like every other missing value. A prune counted a quarantined journal that was already gone as one it had destroyed, and that count is the only record of a permanent loss, so a file removed by something else no longer inflates the warning.
- The branch sweep no longer reports the branches it deleted. It deletes every match in one `git branch -D`, and that command deletes what it can and exits nonzero on the rest, so the per-name retry that followed walked a list that was already half gone: every branch the batch had taken came back as a survivor, and a run that tidied up after a cancelled `--jobs` reported a pile left behind that it had in fact cleared. It re-lists the pattern before the retry, so the report names the branch that survived and stays silent about the ones that did not.
- A run that unlinked quarantined journals said so. A prune renames the runs past `--keep-runs` into `pruned/`, where `gauntlet runs --restore` can bring them back, and then bounds that quarantine by the same number, unlinking whatever falls outside. The unlinked ones are the only journals the tool ever deletes outright, and the count was dropped: a run that tightened the bound destroyed history the tree had been holding and reported nothing, so a year of runs could stop existing between two listings with no line anywhere saying so. The prune now reports what it moved and what it evicted, and a run that evicted prints the count with the bound that did it. The same bound on the first run after an upgrade, which moves every run past it out of the listing, prints one large count rather than losing them quietly.
- `gauntlet doctor` no longer says nothing about a state tree whose index is all that is left of it. The run history line was printed only when a journal or a quarantined run was there, so a tree restored without its `runs/`, the one loss a listing cannot repair, produced no line at all and read like a fresh install. It prints now, and the disagreement it names says which direction it found: a journal the index does not name is one the next listing appends, a row whose journal is gone is a run no listing can rebuild, so the line no longer points at `gauntlet runs` for a tree it cannot fix.
- The launcher's filter finds a set by its name. It searched review names and descriptions only, so typing a word standing in the tree as a set header answered "no reviews match this filter", which teaches the reader the word is not there and sends them spelling out a review name they never had to know. A needle carrying a set's name now brings the whole set back, and the help and `docs/CLI.md` say so.
- The launcher's arrow keys step between panes while a filter is open. A filter holds every set open, so there is no fold left for them to make: the key line still named the action `fold`, and on a review row the key did nothing at all, which a keyboard user cannot tell from a broken key. The hint on a set header already said the tree was held open, and the key line and the keys now agree with it.
- A finished run that printed nothing says so. The feed panel answered "waiting for agent output…" for a run that had ended, next to a header reading `DONE`: a promise nothing would keep, in the panel a reader checks for what happened. A run that ended without a line now says it produced no output.
- Failures that reported success no longer do. A read of `HEAD` that failed before the commit step was dropped, leaving the trailer strip with an empty base and an amend of a commit the step had not written; the churn read behind `--suggest-agent` dropped its error, which scales every area as though it were being edited this quarter; a journal that would not open was declared idle, so the prune could move a run another gauntlet was appending to; and a project prompt whose file would not read listed under a blank subject. Each now stops or reports instead: the commit step refuses to run without its base, a suggestion carries the churn read's error beside the picks, a journal the prune cannot open is kept and named, and a prompt that cannot be read says so in the listing, the picker, and a PR's scope line.
- Removing a review's worktree no longer leaves the directory behind. Git's `worktree remove` and a `stat` decided it, and a stat that failed for any reason other than "already gone" read as though the checkout had been taken, so a full copy of the repository stayed on disk with nothing left naming it. The directory is removed unless a stat says it is gone.
- A `--pr-repo` run that cannot read the remote's push URL is refused. The error was reported only when the repository name had to be inferred from that remote, so the documented fork path pushed to a URL the stack's recovery read a different one from, and a layer that had landed looked absent and was pushed again.
- A cancel that could not signal the process it was cancelling is reported instead of swallowed. The group kill fell back to the process and dropped the fallback's error, so a run that named a clean timeout could have left the agent's whole tree running, and a self-update that had already replaced the binary and failed only to record the rename said the update had not happened. The kill failure now reaches `Wait`, and the update names the binary that is in place.
- An unreadable prompt file now names itself. The hardened open that refuses a symlink or a planted FIFO reported a stat or a descriptor failure as the bare syscall error, so a review could fail to load with nothing to say which file refused it, while the same open in the journal and in the git layer wrapped both. Every refusal from it now carries the path and the operation.
- `gauntlet doctor` no longer puts the per-review helper table out of column for a review named in a script that draws double-width. The name column was measured in bytes and padded with `%-*s`, which counts runes, so a CJK or fullwidth name is given a budget short of the space it draws in and every column after it on that line sits further left than the ones above. The column is now measured in terminal cells, the way the run listing and the report already measure the same text.
- A force-kill no longer leaves the agents it launched running. Every launch kills its process group on the way out, and the third `Ctrl-C` (or a second `SIGTERM`) leaves the process through `os.Exit`, which runs no deferred function: the group kill was the one release that exit skipped, so an agent blocked on a provider stream was reparented to init and kept holding the worktree it was reviewing for as long as it cared to. The live agent groups are registered for the life of their launch, and the force-kill takes them with it before the process leaves.
- `sbom -h` answers on stdout and exits zero, and a refused invocation exits 2 rather than 1. The command handed its flag set to the flag package, which sent the usage screen to stderr behind `sbom: flag: help requested` and exited 1: help was a failure, on the wrong stream, and indistinguishable from a run that could not be inventoried. Each now goes where it belongs (help on stdout with no error, the error and the usage that explains it on stderr), a usage error exits 2 the way `docs/CLI.md` states for the CLI, and a run that failed still exits 1. The screen carries the synopsis and a worked example, which the flag package's defaults could not, and `make artifacts` fails either way.
- A hot reload no longer writes its handoff into the reviewed repository. An unusable state root (a `GAUNTLET_HOME` behind a file, a symlink loop, a permission this process does not hold, or no `HOME` at all) degrades to `.gauntlet` beside the working directory, and the reload wrote the handoff there and execed: the file the successor reads back carries the argv of the agents it launches, in a directory the tree under review controls. A reload with no usable state root now aborts, the run finishes in this process, and the new binary is picked up at the next start. The journal still accepts the fallback, since nothing it writes is load-bearing, and `agents.json` already refused it.
- The run listing no longer measures every column once per row. Each column's width was recomputed by walking all of its cells and counting their terminal cells, once for the header and again for every row, so a listing of `n` runs paid `9n²` grapheme walks and `gauntlet runs --limit 5000` paid millions. The widths are constant for the table and are now measured once when the columns are laid out.
- The file-signal suggester reads each review's prompt once. `Signals` re-reads the prompt body, and a project prompt is an open and a read per call, so the `mark:` collection and the scoring loop each read every review in the pool. One pass now collects both, and the picks are the same ones.
- A run's line totals are no longer carried past the end of an integer. A journal is a file the tool reads, and a line count in it is whatever the bytes held: two events each claiming a figure near the int64 ceiling summed to a negative total, and the rebuilt summary reported a run that deleted a negative number of lines, wrote it to the index, and told the file-signal suggester the review had changed nothing. Every other place a count is read out of untrusted output already bounded what a counter may claim; the journal's own reads did not, and now bound each figure and the running total to the same ceiling, so a misparse claims nothing and the measured lines beside it still stand.
- A run past two thousand reviews no longer copies its whole result list on every result, and a hot reload no longer lists a review twice. The detail ring reclaimed its dropped rows when the rows still held passed the reclaim point rather than when the rows it had dropped did, so past the cap every result rewrote the entire list under the mutex every lane contends for, which is the per-result cost the cap and the reclaim between them exist to avoid. The reclaim test now counts what was dropped, which is what its own comment describes. A seed also prepended the carried-over results to the whole buffer instead of the part of it still held, so the rows the ring had already dropped came back into the listing while `detailDropped` kept counting them as dropped.
- A run row a failed write left half of no longer takes the next row down with it. `os.File.Write` reports how much of the line it wrote and returns an error for the rest, and the bytes it did write stay in `index.jsonl`. The next append starts at the end of the file, so the fragment was welded onto its own row and the two parsed as neither: the run that hit the failure and the run that followed it both stopped listing. A short write now truncates back to where the append started, and a rollback that itself fails is reported beside the write that asked for it.
- A journal that stops recording says how much of the run it stopped recording. The first failed event set the journal's error and every event after it was turned away without a word, so a run that lost its tail at ninety percent reported one error and summarized as a run that ended there. The turned-away events are counted, and the count rides on the error `Close` returns, where the summary is written.
- A run's history now says when it could not be read. A journal whose recorded path no longer opens falls back to a read by run id, and the result of that read was discarded, so a fallback that failed too left the file-signal suggester weighting reviews by a history the run had never been counted in, with no error to explain it. The runs that could not be read are now reported beside the answer, and the suggester logs them and carries on with what it did read.
- A run journal the listing walk could not stat no longer vanishes from every listing without a word. The walk already reported a shard it could not read; the one entry it could not stat was dropped, which left the run out of the run listing, out of the keep window `gauntlet runs --prune` computes, and out of the anchor the index recovery compares against, with nothing saying why. The entries that did read are still read, and the ones that did not are named.
- A partial prune reports what it moved. Only the first failure was kept, and the count was reported as zero, so a prune that moved nine of ten runs and met one failure told the operator nothing was removed and named one run of the ten. Every failure is kept, and the count is what actually left `runs/`.
- A `GAUNTLET_HOME` that exists but cannot be stat-ed, behind a permission the process does not hold or a symlink loop, is refused rather than trusted. It was neither absent nor a directory, but it read as a usable state root, and it is the root `agents.json` is loaded from: the boolean that says a root is usable exists so the reviewed tree cannot define its own agents. A root that simply does not exist yet is still accepted, since the journal creates it on first write.
- A `GAUNTLET_HOME` that resolves to nothing usable no longer leaves a `.gauntlet` directory behind in the working directory. A root that exists but is not a directory (or sits under one) was already refused, but the state directory built on it from the relative fallback anyway, so a hot reload wrote its handoff beside the reviewed tree, where no successor reads it, and a test run left the directory itself there. The state directory now names nothing in that case and the reload aborts, which is the outcome the unsavable handoff already reported.
- The header lines a plain run prints are stamped when they are written, not before the suggest step. Each one carried a single reading of the wall clock taken before `planReviews`, which blocks on a real agent for as long as `--suggest-timeout` allows, so a twelve-minute triage printed `gauntlet 0.1.0, run ..., agents: ...` with a timestamp twelve minutes before the line reached the terminal, and the seed line printed after it carried the real one. Under `--log` the file's timestamps ran backwards. They now read the reporter's own clock, the same one the event stream stamps with, so a log can be sorted or binned by time.
- A credential an agent prints is no longer carried into the commit message or the pull request. The `SUBJECT:` and `PATH:` lines are read out of the same tail as the note, one line away from it, and the note was redacted while these two were not: a subject a steered agent builds from a token it read through the CLI became the commit message, the merge message, and the pull request title, and a file note became part of the pull request body, all of which outlive the run and are read by everyone who can see the repository. Both go through the same redaction now.
- A credential an agent prints is no longer carried into the run journal, the log file, or the dashboard's scrollback. The `SUBJECT:` and `PATH:` lines were redacted, and so is the last line an error quotes, but the bulk of what the model said was not: every normalized output line went to the journal and the terminal as it came, and a provider that rejects a key says so by printing it, so a single line put a live credential in `~/.gauntlet/runs` where it outlives the run and is read by everyone with access to the state tree. The output sink now runs the same redaction the subject and the file notes get, which rewrites the value and leaves the line's shape intact.
- Git's stderr is stripped of credentials as well as of URL userinfo before it becomes an error. A remote stored as `https://user:pass@host/...` was already redacted; a token in the query or the path of a remote, or one an operator's credential helper or `ssh` printed, was not, and git echoes the failing URL verbatim. Every other child-output-to-error path in the tree already ran both passes.
- The state tree is no longer read through a path a repository controls. `index.jsonl` and each run journal were opened with `os.Open` while the writes beside them carried `O_NOFOLLOW`, so a state root that resolves inside the reviewed tree (no usable `HOME`) let a committed FIFO block every run's close, the run listing, and the file-signal suggester forever, and a committed symlink be followed. Both now go through the same guarded open the write side uses.
- A single index row is bounded. `indexLines` grew a line for as long as the file asked, so a corrupt or hand-edited `index.jsonl` with no newline in it made the next run's close allocate whatever the file named. The ceiling the two sibling readers of that file already put on a row now applies here.
- An error an agent reported is marked in the plain run's output too, not only in the dashboard's feed. The mark is the same `!` in the line's own style, and it landed only where color does real work, so the path `--tui` exists to avoid, the one that stays in the scrollback and reads linearly for a screen reader, a monochrome terminal, and a copied transcript, identified an error by its hue alone. Every other line kind there names itself in its own text: a diff carries the sign it was added or removed with, a result line begins `RESULT:` or `PATH:`, reasoning is italic, progress says what it is doing. An error is the agent's own sentence about something that broke, so under `--no-color`, on a terminal that cannot show the hues, or to a reader who cannot separate them, the one line kind worth surfacing read as ordinary narration (SC 1.4.1).
- The lane meters' limit is stated in text. A lane's meter shows how far through the review timeout it is and the elapsed column beside it says how long it has been running, but the timeout itself was named nowhere on the screen, while the same timeout is what produces the grid's timeout glyph and the tally's timeout count. A lane a third full was a reading nobody could place against a number they could not see (SC 1.1.1). The agents panel now names the limit it draws against, in whole segments that a narrow pane drops the way every other panel title drops what does not fit, and a run with no timeout draws no meter and names no limit.
- Turning suggest on in the launcher takes the keyboard with it. The row the toggle moves the cursor to is the one naming who proposes the reviews, and the run pane is where that choice lives, but only the cursor moved: a pane draws its cursor bar on the row the keys act on, so the row it pointed at was drawn like any other, the toggle that had just happened went unmarked, and the status line kept describing the suggest row it had left. The keyboard now follows, so the bar is drawn on the row the toggle points at and the hint under the command explains what to do there.
- The dashboard's help names the pause key from the state the reader is in. The footer legend has always said `resume` while the feed is held; the overlay said `pause the feed` in every state, so a reader who opened it to find the way back from a pause was told to pause. It follows the footer now.
- A launcher's filter that matched nothing says one thing, in one place. The reviews pane and the status line each carried their own sentence for it, so the two could drift into answers a reader had to reconcile, and the constant meant to keep them in step was only used by one of them. Both read the same sentence now.
- The `runs` table lines up when a checkout directory is not narrow. The `DIRS` column holds base names out of the reviewed tree, and it was the one column still measured in bytes while the padding verb counted runes, so a run cut under a CJK name padded the column to three times its content and the header walked away from its cells. Every column in the table is now measured in terminal cells, the measurement `report.go` and the dashboard already use.
- The tail of an agent's output no longer opens with half a character. The ring keeps a fixed byte count, so its window could start in the middle of a multi-byte sequence: a raw echo showed the fragment as U+FFFD, and the line-scrapers that read the same bytes for a subject, a per-file note, and the last note all anchor at the start of a line, so the line the fragment belonged to was lost. The head is now cut back to the next rune boundary, the same cut the lock file already made on the line it reads back, and the window is never longer for it.
- `scripts/shots.sh` keeps its `go test` run off a RAM-backed `TMPDIR`. The Makefile deliberately ignores an exported `TMPDIR` and pins `$(HOME)/.cache/gauntlet/test`, because a shell or launchd exports one pointing at the tmpfs the rule exists to avoid; the script read `${TMPDIR:-...}` instead, so an ambient value won and the two entry points disagreed about the same test run. It now takes the same path the Makefile hands its tests.
- The BSD half of `make artifacts` and `make smoke` runs in CI. Both targets have a GNU and a BSD branch, and macOS is the only runner that takes the BSD one, so the `shasum -a 256` that writes `checksums.txt` there and the `sha256sum -c || shasum -a 256 -c` fallback that verifies it had been written but never executed by anything. A release cut by a macOS maintainer is the one that would have found out. The `repro-macos` job now builds and checks the artifacts before `make repro`, on a bound widened for the four cross-compiles that adds.
- `make build` and `make dist` no longer require a writable test scratch directory. Both toolchain preflights depended on `test-tmpdir`, which resolves under `$HOME`, so a machine with no `HOME` could not build the binary or cross-compile a release artifact and was told the problem was tests. Neither preflight runs a test or reads `TMPDIR`: only the targets that hand it to a `go test` name it now. `make doctor` also reports `install`, which `make install` writes the binary with and which no target preflighted.
- A subcommand's flags are now found in the two places they used to be reported from the wrong end. `gauntlet show` peeled its run id off either side of the flags and then asked whether an id had been given before they were parsed, so a mistyped flag was answered with "show needs a run id": the reader was sent to the listing instead of to the typo. A flag belonging to another subcommand gave the same answer when it came before the id and its own message when it came after, and the two forms were not required to agree. The question is now asked after the flags parse and after the owner checks, so a miss reports itself and `show --limit 3` reports the same on either side of the id.
- A "did you mean" now names a flag the command would then accept, and one close enough to the miss to read as a typo of it. The candidates were every flag on the CLI, so `gauntlet runs --nope` was pointed at `--jobs` and `gauntlet doctor --limt` at `--limit`, each of which the next invocation refused, spending two runs to learn what the first message already knew. The bound was the fuzzy package's shared ceiling, which three edits turn "limt" into "--log" on a name that short. Candidates are the flags the command reads, and the distance is half the name, at least one edit and never past the shared ceiling. The suggestion for a review name, an agent, and an unknown command is unchanged.
- A subcommand word left after a flag that is not global says where it belongs. A subcommand is only one when it is the first word, so `gauntlet --json runs` reached the default run with "runs" as a stray argument and was told that, which points at the wrong end of the line; the help screen named the two flags that may lead and said nothing about the rest. The message now gives the order, and the screen says both halves of the rule.
- A pointer in `docs/THREAT_MODEL.md` no longer ranges past the end of the file it cites. The release-build row pointed at `.github/workflows/release.yml:195-201` in a 196-line file, and `TestThreatModelPointersResolve` and `TestThreatModelWorkflowPointersNameTheStep` were both red over it. The citation names the release step that writes and uploads `dist/sbom.json`.
- A token count taken from an agent's own prose is no longer billed as the run's spend. Under a machine-readable output mode the transcript the parsers read is the model's visible text, so a counter matched out of it is a number the model wrote: a fixture it quoted, an example it printed, a provider's JSON it pasted into an answer. The larger of that guess and the figure the agent's stream reported was the one recorded, so one review could end a `--token-budget` run and report a spend no provider ever billed. An agent that reports its counters is now believed over the text around them, and the text readings remain what an agent without that mode leaves behind.
- `--seed` reads a leading zero as decimal. The flag parsed its value with Go's base-0 rules while the help promised a decimal or `0x` hex value, so `--seed 010` ran as seed 8 and recorded 8 in the journal, the value every later replay of that run reproduced, and `--seed 08` was refused with a syntax error instead of the message the flag states. Hex literals and `_` between digits still work.
- A defined agent whose executable names an unexported variable says so in `gauntlet doctor`. A definition like `{"pi": {"argv": ["$AGENT_ROOT/bin/pi", "-p", "{prompt}"]}}` is refused at launch, with the variable named, but detection resolved the unexpanded string against the working directory, found nothing, and reported the agent as a CLI that is not installed: the operator was sent after a download for a definition one `export` away from working. The row now carries the reason.
- The `--keep-runs` help named `~/.gauntlet` on both copies in the binary, while the run prunes the state root, so a run under a `GAUNTLET_HOME` elsewhere was told it pruned a tree it never touched. Both copies name the state root and where it comes from, as `docs/CLI.md` already did.
- A generated commit subject names the file a reader would look for first, and reads a path's case as the conventions around it do. The changed files were ordered by byte value, and that order decides which of them the subject names, so a commit touching `Zebra.go` and `apple.go` was titled after `Zebra.go` and one touching `README.md` and `docs/CLI.md` after `README.md`. They are now ordered the way the tool already orders every other list a person reads from, which is the order a pull request title and a merge message follow. A path is also classified case-insensitively, as the file-signal suggester already classifies one: the tables the type and scope come from are lowercase, so `FOO_TEST.GO` was typed `chore` rather than `test`, and a `SRC/` directory was used as a conventional-commit scope, naming "the src package", where the same directory spelled `src/` was not.
- A scratch script committed at the repository root is gone. `.scratch-fields.sh` held two lines: `set -e` and a `cd` into one review lane's worktree on the machine that wrote it. It was tracked, so `git ls-files` named it, and `TestAnalysisScopesCoverEveryLintedFile` requires every tracked shell script to sit under a path `check-scripts` hands to shellcheck, so the suite was red on a clean checkout over a file nothing builds. `.gitignore` listed `.scratch_*`, which matches the underscore spelling only, so the dash spelling it was written under was never ignored and a second such file lands in the tree again; `.scratch-*` is listed beside it.
- Four pointers in `docs/THREAT_MODEL.md` now name lines that exist. Three ranged past the end of the file they cited (`internal/agent/notes.go`, `internal/runx/runx.go`, `cmd/sbom/main.go`), and a fourth row of the controls table was wrapped in a stray backtick with its own citation escaped, so it rendered as text rather than as a row. Three of the four were unreachable by the test that checks them: an unterminated backtick stops a citation from matching, which is how a pointer goes stale unnoticed. The ranges are corrected, the row is a row again, and the test is red without the fix.
- A release is no longer refused twice for the same tag, and the check that runs is the one that reads the tag correctly. The release job asked whether the tagged commit was on `main` in two steps: the first compared `$GITHUB_SHA` against `refs/remotes/origin/main` and had no fallback fetch, and the second compared `HEAD` against `origin/main` after fetching the branch if the checkout lacked it. An annotated tag makes `GITHUB_SHA` the tag object, so the first check read a ref it could not resolve and failed the release the second would have passed. The version-order guard and the ancestry check are now one step that fetches `main` when it is missing and tests `HEAD`, the commit the tag resolves to, and the error it prints is the one that names the commit `main` does not carry.
- Renaming a stacked-PR layer to its published name converges when it is repeated. The recovery pass that finishes a rename a killed run left between commit and rename looked the layer up by both its provisional and published name, so a second pass found the published one and skipped the rename, but a caller replaying the rename itself (a retry, or a pass that re-read a branch it had not yet recorded) got git's "branch not found" for a rename that had already happened. A rename whose source is gone and whose target is there is now the state the first call produced and succeeds, and the worktree's handle follows the new name. A rename to a name that exists nowhere still fails, and a rename onto an existing branch is still refused.
- Three dashboard readings no longer report a state with no way out of it. The help overlay still described `space` as pausing the feed on a run that had already finished, where the key does nothing and the footer had already stopped advertising it; the feed title reported a scrollback as a count alone, and the footer's `esc:live` is fitted to the terminal and is among the first segments dropped, so a narrow pane left the reader in the scrollback with no key named; and the small-terminal fallback's one running row joined every busy agent into a line that was cut wherever the width ran out, with nothing saying how many were behind the cut. The pause line is gone with the run, the feed title names `esc` the way the unmerged count already names `?`, and the fallback names the lanes it could not fit and counts the rest, the way the agents panel and the reviews grid already do.
- A journal being written no longer leaves its last line half on disk. The write buffer held 32 KiB and wrote whatever filled it, so an event larger than that, or a run that recorded more than 32 KiB between loop boundaries, cut a JSON line in two and left the file ending on something other than a newline. `truncated` then counted a live, intact run as a journal cut short, and doctor sent the operator to re-take an archive that was never short. The buffer now writes whole lines only.
- A journal whose directory sync failed at open is released again. The open path holds the stream for the life of the journal so a prune skips a run that is still being appended to, and the failed open returned no journal, so nothing was ever going to release the hold: the run's journal could not be pruned for the rest of the process, and every further failed open added one more entry to the table that answers the question. The hold is now dropped on that path, and an open journal is released when it is closed.
- `GAUNTLET_NO_ANIMATION` now stops the whole dashboard moving, not only the one glyph in it. The screen repainted itself ten times a second for as long as the run lasted, and no key stopped that: `space` pauses the feed while the frame keeps moving, so a reader who set the variable for motion sensitivity got a still glyph inside a screen in motion for hours. Under it (and under `NO_MOTION` and `REDUCED_MOTION`, on the same precedence) the frame now changes when a review reports or a key is pressed, and every thirty seconds otherwise, which keeps the elapsed clock and the timeout meters honest. A run that reports nothing repaints at that rate; one that reports draws the change the moment it lands.
- The help overlay says which page it is showing, and its own key row names `g` and `G`. The overlay is taller than most panes, and the last page looked exactly like the first, so a reader paging down could not tell they had reached the end without pressing the key once more and watching nothing move. `g` and `G` scrolled the overlay and were named nowhere in it, which is the same omission that once left `space` looking broken. The position is a segment in the key row rather than a fixed string: a pane too narrow for the whole row keeps the keys and drops the position, because the keys are how the next page is reached.
- Line counts are measured again after a tree that could not be measured becomes measurable. The commit every count is diffed against was recorded once per handle, and the attempt was spent whether the read answered or not, so a run whose first probe failed (git not yet on `PATH`, a worktree whose `HEAD` was not written yet) reported `n/a` for insertions and deletions for the rest of its life although the tree had a commit all along. Only a commit retires the probe now, and a failed one is retried by the next caller.
- The overlay a `dsh` launch is handed is no longer whatever another build left under the same name. The overlay is written to a directory in the user cache dir that every run and every version shares, and its key names the provider and the model and nothing else, so a file already sitting there was accepted on the strength of existing: another version of the program that formatted the overlay differently, or pinned a different field, was read back verbatim and `dsh` was pointed at it. The body is now compared with the one this build wants, and a file that differs is rewritten, whole, the way a missing one already was.
- A bare `dsh:model` resolves after a launcher probe fails once. The probe of the headless profile's provider kept its error for the life of the process with no way to clear it, so a launcher that was not on `PATH` yet, a `bunx` fallback whose fetch failed, or a config dump that came back empty left the model unresolvable for the rest of the run. A failure is now evidence about a moment: it is still kept, so a run does not pay a subprocess per launch, but it is re-probed after a window. A probe that succeeded is kept as before, because the provider in the headless profile is a property of the install.
- A failure to read a stack branch's parent is no longer read as a rejection. Every other git failure on the recovery walk is reported, and a layer branch that descends from the pinned base always has a parent, so the only thing the ancestry check can say there is that git failed. Reading it as a rejection concluded the stack had no recovered layer, and the review behind it ran again on top of work already committed, pushed, and possibly open as a pull request.
- A worktree checkout the run could not remove is now named in the failure that left it behind. The cleanup of a half-created worktree and of the scratch checkout `--merge-into` builds is the one cleanup whose result the caller never learned, so a failed add leaving a full copy of a possibly private tree standing in the reviewed repository, and a merge reported as landed over a checkout nobody removed, read exactly like a clean one.
- A tree the file-signal suggester cannot list, open, or read history from now says so instead of answering as though the code were not there. A root that could not be walked returned an empty file list, a root that could not be opened left every content rule off, and a churn read that failed left every dormant area weighted as live. All three were reported as evidence of absence: `no review matched anything in this tree` is a verdict on the code, and the one thing the operator could act on, the path, went unnamed. The picks still come back beside the error, and the walk that replaces a git listing is now bounded by the same deadline, so a repository too slow to list no longer turns a ten-second failure into a hang.
- A commit subject derived from a tree whose status git could not read now says why it is generic. The read failed silently and the fallback describes nothing, so `chore: update files` reached history, a merge message, and a pull request title with nothing recorded about the reason.
- A review whose agent output pipe breaks part way through is no longer filed as a finished review. The tail the subject, note, and file notes are parsed from was truncated silently, so a commit subject cut in half and a note that was really the last line before a dead connection read exactly like ones the agent had finished printing. A stream that ends early is now reported, retried like any other failed attempt, and named in the run's log; the commit, conflict, and suggest steps refuse a result they cannot vouch for rather than acting on half of it.
- `gauntlet update` retries a release request that fails for a transport reason. A dial that timed out, a connection cut mid-handshake, or a body that ended early now gets a second and third attempt, waiting a jittered doubling interval between them. The retry covers the request up to the response headers only, never a body already partly written, so a resumed transfer cannot append a second copy of the asset. A refused status, a rejected URL, and a cancelled run are answers already given and are not repeated. A transfer that still fails now names the asset and how many bytes arrived, where it used to report a bare read error indistinguishable from a pipe failure.
- The two dashboard meters that carried no figure now name it. The budget row read `budget` and a bar of blocks, and an agent lane's sparkline was its output rate as a shape with no number beside it, so a reader who cannot take the shape or the hue had no reading at all (WCAG 1.4.11 was met, 1.1.1 was not). The budget names the share spent and says `over` past its ceiling, which the bar cannot; a lane names its lines/s, and a lane that has printed nothing names neither. The reasoning share, the token rate, the timeout, and the concurrency count were already spelled out and are unchanged.
- A run-pane row the launcher draws dim now says in its own label why it cannot apply: `stacked`, `suggest off`, or `commit off`. Faintness is a difference in luminance, not a reading, so on a monochrome terminal or to a reader at low vision those rows read as ordinary rows, and the status line only names the reason once the cursor is on one. A row that applies carries no note, and the narrow pane still cuts the label with a marker.
- The dashboard's help overlay now lists every scheduled review under its whole name with its outcome spelled out beside the glyph. A grid cell is one column wide and the feed trims the same name to sixteen columns on every line, so a review named from a project's own prompt directory existed on screen only as an ellipsis, and its status only as a shape. The overlay is reachable by keyboard from anywhere and has the width for both.
- `--usage-cmd` is checked against the same absolute-only `PATH` the probe itself resolves on, and a command that names nothing runnable is a usage error. The probe fails open by design, so a typo there left the run enforcing no limit at all for its whole length, reported by one line under the agent output, while the operator believed the ceiling was in force. A probe that breaks once a run is under way is still reported and ignored.
- An in-place review's pre-review snapshot is checked against the object store before the retry path resets anything. The snapshot's trees are unreachable, so a gc that prunes them leaves nothing to restore from, and the restore applied `reset --hard` first: the review reported that the tree could not be restored only after discarding the working state those objects were the only copy of, which for an in-place run is the operator's own uncommitted files. The three objects are now looked up first, and a snapshot the store no longer holds is reported with the tree untouched.
- Branch names are read from git as the bytes they are. A refname forbids only ASCII space and control characters, so a no-break space is legal inside one, and every reader trimmed the whole Unicode whitespace set off it: a branch whose name ended in a no-break space was offered without it when picking a merge target, a published stack layer whose name held one read as missing to the recovery pass, and a branch sweep handed `git branch -D` the same name twice, stranding a review branch it had just listed while reporting the second delete as a failure. The line terminator git wrote is now what gets trimmed, and `ls-remote` output is cut on the tab between the object id and the ref rather than split into fields.
- A `--paths` entry spelled in decomposed Unicode is accepted. macOS hands over the decomposed name of a file the filesystem created that way, and tab completion produces it, so a legal path was refused with a message about line breaks, backticks, and length that the entry did not have. The entry is composed before the check, and the prompt still names it in composed form.
- A review that reports one file in both Unicode spellings lists it once. The per-file notes were indexed on the raw bytes, so the same file got two overview lines in a stacked pull-request body where the later note was meant to win.
- The state tree is no longer written or read through a symlink the reviewed repository planted. A `GAUNTLET_HOME` with no usable `HOME` degrades to `.gauntlet` beside the working directory, so a hostile tree can ship `.gauntlet/runs`, `.gauntlet/pruned`, or a journal under it as a link. The appends already refused one; the reads did not, so `gauntlet show` and `gauntlet runs` parsed a link's target and printed it as the run's event stream. Every existing directory component is now checked with `lstat` before the journal is created, moved, or read there.
- A worktree snapshot is refused when the repository's `.git` is a symlink. The private index it writes is created inside that directory, and the real index is copied into it, so a planted link put both outside the repository.
- The conflict-marker scan no longer follows a symlink out of the checkout. The paths come from `git diff --name-only --diff-filter=U`, and the agent asked to resolve one can leave a link at that name, so the scan read up to its size limit of whatever it pointed at.
- `gauntlet --show-prompt REVIEW` prints the prompt as the document it is again. Every line break in it was stripped on the way to the terminal, so a 13 KB prompt arrived as one unbroken line: unreadable, and the one thing the flag exists to show. The stripping itself is unchanged, and still removes what a hostile `*-review.md` could use to drive or spoof a terminal, on either side of a break.
- `gauntlet show <run-id>` on a pruned run names the command that brings it back. The message was "no journal for run X ... (see: gauntlet runs)", and the listing it pointed at does not hold the run: the id appears only under the quarantine note, and the one line there that carries the command to run is easy to read past. It now says `gauntlet runs --restore <run-id>`. An id that was never written still says `see: gauntlet runs`.
- `gauntlet show <run-id>` on a run whose journal holds no events says so on stderr instead of printing nothing. The run was interrupted before its first event reached the disk, and a silent success reads as a replay of a run that did nothing. Stdout still carries the replay and nothing else.
- A bare `dsh:<model>` pin now reads the provider from the launcher that runs the review. `--bin` replaces the executable after the command is composed, but the provider probe ran whatever `dsh` was on PATH, so the model was pinned against a config the review never loaded; the probe runs the override now.
- The provider probe is memoized per launcher argv rather than once per process. A `--bin` override, the launcher on PATH, and the `bunx` fallback each read their own config, and one memo answered all three, so a failure probing one launcher was reported as the reason the others could not resolve, and a provider read from one pinned the model of another.
- The file-signal suggester's churn window is now measured from the run's clock
  rather than from git's own. `git log --since="90 days ago"` was resolved by
  git against wall time, so the same tree and the same `--seed` proposed
  different reviews on a later day, and a replayed run did not reproduce the
  original's picks. The window is an absolute cutoff now, and the seed the
  journal prints replays the suggest step along with the schedule.
- The launcher's pane titles now drop whole readings and mark the cut, the way the dashboard's panel titles already did. A title the frame was too narrow for was cut wherever the width ran out, so a pane holding fewer rows than it had read "AGENTS  none picked: auto-d", and could lose the "+3 more" that says it is holding rows back: a review or an agent missing from the list then read as one that does not exist. The same now holds for a key row that had to leave keys off, in the help overlay and in the small-terminal fallback: what did not fit is marked, so a row carrying two of four keys reads as two.
- The small-terminal fallback kept the run state at every width. Its first row held the clock and the state together and was cut at the right when they did not both fit, which left a ten-column terminal showing "● RU…" where the one reading the fallback exists for had been. The clock goes first now, as the version and the loop number already did.
- Reading a run journal now refuses a symlink and a non-regular file in its place, on the read paths as well as the write path. A FIFO named after a run id listed as a journal, and opening it for reading blocked until a writer appeared, which for a planted node is never: every later run in that state tree hung on the exit-time prune.
- A persisted `elapsed_s` at the edge of the nanosecond range no longer renders as a negative duration. The one decoder of that field refused a value past the range with a `>` against the quotient, and `float64(math.MaxInt64)` rounds up to 2^63, so a value equal to the quotient multiplied to exactly 2^63, which is one past the largest int64 and converted to `MinInt64`: the negative duration the guard exists to refuse, about 292 years of elapsed seconds, in the run report, the dashboard's agent panel, and the journal summary. `json.Unmarshal` hands back whatever the bytes held, and a journal is a file the tool reads rather than one it wrote, so the band was reachable. The bound is the quotient with a `>=` now, and the largest seconds that still represent a duration are still decoded.
- `gauntlet runs --json` and `gauntlet show` no longer carry the operator's account name in a run's paths. Both already shorten their own state paths, and the journal already shortens the free text, because these are the copies an archive job, a dashboard, a script, or a pasted issue takes off the machine, and each then printed a reviewed tree under the operator's home as `/home/<account>/…`: every row's `dirs` and `path` in the JSON document, which is the field a consumer reads to learn which project a run covered, and the `dir` on every replayed event. All of them are shortened the way the free text is. What is on disk is unchanged and still resolved, because the listing, the history matcher, and the run's own locks resolve a path the person typed against it, and a tree outside the home directory has no account in it to take out.
- A memoized binary is now looked up on the `PATH` it is filed under. The agent resolver and the git lookup were both keyed by the `PATH` they were filled from (1.7.2), so a hit could not be served to a `PATH` that had not been searched, but the key and the search were still two separate reads of a mutable input: a miss was resolved by `runx.LookPath`, which reads the ambient `PATH` when it is called, and stored under the key read before it. A `PATH` that changed in between filed one machine's answer under another's, and returning to the earlier `PATH` served the path the other one produced, which does not exist on it: an agent CLI or `git` reported installed, and a run that then failed to launch it. The git memo also keyed on the raw `PATH` while searching the resolved one, so a `PATH` carrying a relative or empty entry, or none at all (the case `launchd`, `systemd`, and `env -i` hand a program), was never recognized as the one already answered and every call re-ran the search. Both memos now key and search the same `runx.AbsPATH()`, through `runx.LookPathIn`, which is the one entry point that resolves against the string it is handed; `runx.LookPath` keeps its meaning and is that call over the ambient `PATH`.
- The thinking share no longer reads 0% for a run that disclosed more thinking than it reported in total. `humanize.Share` promises that a part above the whole reads as 100, and widens the multiply to int64, but `part*100` leaves one word long before the inputs stop being plausible: a part of 2^58 and a whole of 1 wrapped to 0. An agent whose disclosed split does not add up to the total it also reported is exactly the case the clamp exists for, and it printed the opposite in the run report and the dashboard's agent panel. The product is now carried in two words and divided once, so every pair of inputs answers as documented.
- `gitx.Snapshot` no longer reports a failure to read `HEAD` as `cannot read HEAD: %!w(<nil>)`. `Tip` hands back the command's output with a nil error whenever git exits zero, so a `HEAD` that was not a commit took the branch that wrapped a nil error and printed Go's placeholder for one. The two failures are separate again: the command's own error is wrapped, and a `HEAD` that is not a commit names what came back, the way the `write-tree` check below it already did.
- The duplicate-prompt comparison no longer ignores a failed read of the second file. `sameFile` stat-ed both copies, then read each one bounded, and the two reads reused the same two variables as the stats: the second read's error overwrote the stat's without ever being looked at, so a second copy that could not be read was compared by its length alone against the first. The two reads now carry their own results, and a copy that did not read in full is not equal to anything.

### Changed

- Two bundled reviews stopped asking for what the agent cannot act on. `infra-review` carried three instructions no agent can carry out: "Consider the operational burden of the current setup", "Focus on reliability, security, and developer experience in that order" (a restatement of the fix order two lines above it), and "Consider what happens when things fail, not just the happy path". The first now names the cost and the tree evidence that shows it, the third names the step shapes to look for, the second is gone, and the closing block's "Consider the team size and operational maturity", a fact no tree states and one that same block already rules out of scope as organizational, is gone with them. `ux-review` told the agent to report form labels whose wording does not say what the field is for, in the one section whose fence hands labels to `a11y-review` and whose own closing line repeats the hand-off, while `a11y-review` lists the missing label and the placeholder-as-label antipattern as its own. The bullet is now the part `ux-review` owns: the wording of a field's visible text, read without a screen reader.
- The two environment variables whose contract was wider than their documentation now state it. `GAUNTLET_HOME` expands a leading `~` and any `$VAR`, treats an empty value as the variable unset, resolves a relative path once, and refuses at startup a value whose `$VAR` is unset or a path that is not a directory, none of which the help screen, `docs/CLI.md`, or `.env.example` said. `GH_TOKEN` wins over `GITHUB_TOKEN` when both carry a token, and an empty one is ignored rather than counted as set, so `GH_TOKEN=` exported to clear a token no longer reads as a mask over a `GITHUB_TOKEN` the same environment carries. The calibration script resolves the state root the way the resolver does, `$VAR` included, and refuses a path the CLI would have refused.
- `make repro` compares the two files a release ships beside its binaries, not only the binaries. Each copy now builds under the release asset name and writes `checksums.txt` and `sbom.json` from what it built, so a release is six files and the claim covered four of them: `gauntlet update` reads the first and a scanner reads the second, and both are built from the binaries, so a difference between the two trees could reach a tag in either. `sbom.json` is the one carrying a determinism claim of its own (its serial number is hashed from what it describes rather than drawn at random, and its licenses are resolved out of the module cache), which is why the check belongs here rather than in a comment. The stamp is `VERSION`, the value `make dist` is handed.
- The run summary, the failure list, and the per-tool breakdown are ordered by Unicode collation instead of by byte value. They are keyed by a review name and a `tool:model` label, and both are the reviewed tree's and the operator's own text, so an accented or non-Latin name compared by code point landed after every ASCII one whatever letter it began with, and the summary read as if those rows were the tail of the run rather than rows in it. This is the same order the picker's lists, the dry run, `doctor`, and the review grid have used since the collation landed in `internal/fuzzy`; `fuzzy.Comparator` is now what orders a list of records by such a field, so the runner and the reporter order by it too. A list of the lowercase ASCII names the tool ships is already in this order, so nothing built in moves.
- Reasoning is drawn from one token everywhere, not one per view. The feed's reasoning lines built their own style at the call site while the lane's reasoning-share counter used the theme's, so the two were the same hue but not the same mark: the italic that says the agent is thinking out loud reached the feed and nothing else. Both draw the thinking token now, and the review tally's conflict label is a token as well rather than a color written at its one call site. The colors themselves did not move.
- `gauntlet runs --json` writes its `home` and `journals` fields with the home directory shortened, so neither field puts the OS account name into a copy a script or an archive job carries off the machine. The two fields name the state root where the install keeps its tree, which is a fact about the machine rather than about any run, so nothing matches on them. Before, a root under the operator's home came out resolved (`/home/<account>/.gauntlet`); it is now written the way the journal's free text and a row's `args` already were (`~/.gauntlet`). A root that does not sit under the home directory comes out whole, and the row fields `dirs` and `path` are still resolved, because the listing and the history matcher need a path a person typed to keep resolving. A consumer that passes either field to `test -d`, `realpath`, or another tool has to expand the leading `~` first; the table and the stderr journal path are unchanged.
- Two bundled reviews ask for a finding to name something concrete. `test-review` told the agent to consider what bugs would slip through the suite, which a missing assertion in a helper can answer with any number of hypotheticals; it now requires the input that triggers the gap, the wrong value it produces, and the line the assertion belongs on, and says a gap with no bug behind it is a coverage number rather than a finding. `ux-review` asked for a finding to be evaluated from the user's perspective, which is a framing rather than a question a screen can fail; it now names the screen or component, the state the user is in, and the next action that fails or has no path onward, traces the flow from entry point to outcome rather than the screen alone, and measures consistency against the app's own dominant path so a deliberate exception is not reported.
- `make dist` checks the microarchitecture level of every release binary, not only its platform. The asset name carries GOOS and GOARCH and nothing else, so the loop at the end of the target reads both out of the binary the compiler stamped there; the level it also stamps was left unchecked, and a `go env -w GOAMD64=v3` or `GOARM64=v8.1` left behind on a build host would have shipped bytes that no name and no check accounted for. The comparison names the Makefile's own exported values, so raising the pin moves the check with it.
- The exit-code table in `--help` and in `docs/CLI.md` now covers the subcommands, not only a run. `gauntlet doctor` has exited 1 when no agent CLI is launchable and `gauntlet update` has exited 1 when the update fails, while the table said 1 meant a review failed, timed out, was skipped, or would not merge, so a script reading it could not tell a broken install from a failed review. Code 0 is worded for the whole command as well, since `runs`, `show`, and `--list` return it without a review ever running.
- A merge that failed the post-squash `git diff --cached --quiet` now puts the tree back, like every other merge failure does. A timeout or a contended index on that one check left the whole review staged in the reviewed tree, which the next lane and the next run then read as a dirty tree.
- A directory that already finished its loops before a hot reload now seeds its carried results into the run-wide token tally, so `--token-budget` measures the run against what the run has actually spent rather than ignoring the finished directory.
- The summary's reasoning share is computed from the exact reasoning total instead of the capped result list, so a run past the 2000-result detail cap no longer prints a share that falls as old results are dropped.
- `doctor` names the evidence tools the time and Unicode reviews already
  instruct an agent to reach for: `zdump` for `time-review` and `uconv` for
  `unicode-review` were named in the prompts and missing from the tool
  catalog, so both reviews were reported as having no helper tools and the
  prompt never learned whether the tool was installed.
- The generated dsh model overlay under the user cache dir is now swept and its in-process table bounded. Nothing removed either: the directory gained a file per provider/model pair for the life of the install, shared by every run, and the table kept one entry per pair a long run pinned. An overlay past 90 days is rewritten identically the next run that wants it, and the table is dropped whole past 256 entries, since a miss costs a stat.
- Every list of names the CLI prints is now ordered by Unicode collation instead of by byte value. The agent names an unknown-tool error offers, the review and set names the picker, the dry run, and `doctor` show, the project prompts discovery finds, and the dashboard's review grid all went through `sort.Strings`, which compares code points: "Zebra" came before "apple", and a name carrying an accent or written in another script came after every ASCII name whatever letter it began with. A name is a user's own (`--agent-cmd`, `~/.gauntlet/agents.json`, a `*-review.md` in the reviewed tree), so "Ähre" and "日本語" landed at the end of the list and read as the list ending there rather than as one name among many. The same names were already matched case-insensitively by the picker's filter and by the "did you mean" hints, so a list ordered by bytes also contradicted the order the rest of the tool found them in. The order is root collation rather than a language, since the tool has no locale setting and root is what English, German, and Turkish readers agree on; a list of the lowercase ASCII names the tool ships is already in this order, so nothing built in moves.
- The maintainer scripts' mypy run reads two more error codes, `annotation-unchecked` and `empty-body`. A `@no_type_check` decorator and a method whose body is only a docstring are both signatures that stop describing what their annotations claim, and both passed the strict run unnoticed. `check-scripts` and the scripts job already carry the list, so nothing outside `pyproject.toml` moved.
- The release inventory stops the release when a linked module's license cannot be resolved. `cmd/sbom` read every grant it could out of the module cache and named the ones it could not on stderr, then wrote `dist/sbom.json` and exited 0, so a release could publish a CycloneDX document that lists a module and says nothing about the terms it ships under, and the only sign was a line in a build log nobody reads before tagging. The document exists so a consumer and a vulnerability scanner know what is in a binary; a component with no license field is the one hole in it, and it is now a failure instead of a note: the run names every module whose grant is missing or unrecognized and writes nothing, so `make artifacts` and the release that calls it fail before a binary is published. Every module linked into the current release resolves, so no build changes.
- `code-review` dropped the checklist items no agent can act on and named what it owns instead. Its sections already fenced themselves against `slop-review`, `minimalism-review`, and `arch-review`, and then carried the bullets those fences exclude ("Poor separation of concerns", "Weak naming", "Tight coupling", "Interfaces that could be made clearer or smaller", "Repeated patterns that should be abstracted"), so the same ground was claimed twice with different rules. The remaining bullets each name a shape to search for, the goal paragraph states what the review is for instead of "practical, high-signal", and three instructions that only shape a written report, or that repeat the finding template's confidence field, are gone.

### Security

- The `bunx` fallback for `dsh` fetches one pinned version of the package. Naming `dsh` without a launcher on `PATH` ran `bunx @deepseek-ai/dsh`, which resolved whatever the registry served at the moment of the fetch and executed it, and a `dsh:<model>` pin runs the same argv as `--dump-config` before the review even starts: the code a run executed was a property of that moment rather than of the tree that launched it. The spec now names an exact version (`agent.DshNpmPackage`), so a bump is a reviewed change like every other dependency bump, and `gauntlet doctor` prints that same spec beside the `dsh` row rather than a second spelling of it. The registry still resolves the pinned name and nothing is checksummed or signed, so the publisher of that version remains trusted.
- A credential an agent prints is redacted out of the note that carries it. Every child process runs with the operator's keys in its environment, and a provider rejects a bad one by printing it (`Incorrect API key provided: sk-ant-...`), on the last line of the run: `runx.FirstLine`, the one place every piece of child output passes through on its way into an error string, a report row, and the run journal, stripped URL userinfo and nothing else, because that is the shape git prints. Those three copies outlive the run and are read by whoever opens the journal next, and a shell or a config error reaches them the same way an agent's does. A value assigned to a name that says it is a credential, and a token carrying the fixed prefix its issuer gives it, are now replaced wherever that line is shown or stored. A value too short to be a credential, and a flag that names one (`--max-tokens=4096`), are left as they are: redacting those would cost the note its meaning and leak nothing.

## 1.29.0

### Changed

- `--suggest-agent gauntlet` now prints the strongest evidence beside a review, not the first three the rule table happened to reach. A repository with tests, documentation and a Go module scored an HTTP handler at two units but printed "source files to read, a test suite, documentation", because those three rules are evaluated first and each worth one, so the evidence that actually ranked the review was dropped from the line explaining it. Each reason now carries the weight that admitted it, and the three printed are the heaviest, with equal weights keeping table order.
- The help overlay's key row, on the dashboard and in the launcher, is now fitted to the pane instead of being one fixed string, and it names every key the overlay answers to. The row was cut mid-name on a narrow terminal ("j/k scrol", a key that does not exist) and left `space` out even though it pages the overlay down, so a key that worked read as broken. Whole segments drop from the right now, and the closing keys keep the row. The dashboard's close line also names `h`, which closes the overlay there and was unmentioned; the launcher's does not, because `h` folds a set there instead.
- Locking one directory twice inside a single process is refused the same way on macOS as on Linux, and the refusal now names the holder's note. `flock` alone could not promise that: Linux ties a lock to the open file description, so a second open of the same lock file conflicts, while macOS ties it to the process and a second call converts the lock already held. A run that reached the same tree twice therefore refused to start on one platform and carried on with two sets of agents in one tree on the other, against the rule that no two agents ever share a tree. The lock now also records what this process holds, keyed by the lock file's real path so a symlinked spelling of the same tree is the same lock, and `Release` gives it back.
- The exit-time prune no longer calls a journal this process is still writing idle on macOS. Same divergence as the lock above, and the same place it bites: the writer takes a shared `flock` and the prune takes an exclusive one on a descriptor of its own, which conflicts on Linux and converts the process's own shared lock on macOS. There the prune read a live event stream as idle and moved it into `pruned/`, leaving the row its `Close` appends naming a file the listing no longer held. The process now records the streams it has open, and the prune consults that before asking the kernel.
- Four bundled reviews stopped telling the agent to skip an item whenever it was unsure. In an auto-fix run the composed prompt already says doing nothing is the failure mode, so an unqualified "if you are not sure, skip it" in the block that survives composition left the agent to choose between the two, and a cautious reading meant the pass changed nothing. `code-review` now traces the function, its callers, and its tests until it can point at the wrong line, and skips the item only where the code does not settle the question; `doc-review` settles a doubtful claim against the code it describes instead of dropping it; `perf-review` measures the impact or shows it by inspection (an N+1 query, unbounded growth, a regex compiled per call) and skips only what it cannot prove; `sec-review` requires the call path from untrusted input to the sink, allows a hardening note without one, and skips the item where no path exists. The intent behind each line, no report on a guess, is unchanged.
- `docs/CLI.md` now states the rule `microagent` being added in 1.27.0 already enforced: a `~/.gauntlet/agents.json` entry naming an agent gauntlet ships is refused at startup, and one that named `microagent` before it shipped has to be deleted on upgrade. The 1.27.0 notes said so, the page a reader opens to define an agent did not, so the migration step was only in the release notes of a version they had to already have read.

### Fixed

- The run now measures itself against one clock. Every in-run reading of the time (event timestamps, review and loop elapsed times, the `--runtime` budget, the line-sample debounce that decides which review a diff belongs to, the dashboard's stamps, the suggest transcript's log lines, and the journal summary's end instant) went to the wall clock separately, so a run's timing was a set of unrelated readings and a clock step could expire or extend a budget the operator set. The run's start instant and the monotonic reading now define a single handle the whole run reads, and the journal summary records its end from that handle rather than from a second wall-clock read beside it. A run replayed from its seed can replace the one handle instead of each reader reaching for the wall clock on its own, and the pause between two attempts of a failed review, the only wait on the review path, is now injectable the same way: its length was already a keyed draw from the seed, so a replay spends simulated time instead of the backoff the seed chose.
- A path flag naming somewhere that is not there now says so the way every other bad flag value does. `--dir` and `--dirs` were checked when the run started, and `--prompt-dir` only for the half of the mistake where the path exists and is a file, so a missing path printed a bare line with no help screen while a file at the same path printed the screen: one flag, two shapes, and the one without a screen was the common case. All of them are resolved and refused while parsing, named by the flag they came from, and the syscall text that said which call failed is replaced by the directory that is not there.
- `gauntlet runs` no longer hides its recoverable history behind an empty listing. The note naming the pruned runs, and the journal path on stderr, were printed below the table, so a state tree whose index listed nothing said "No runs recorded yet" and stopped: the one case where the quarantine is all the history there is was the one case that did not mention it. Both are now printed either way.
- The small-terminal fallbacks were dead ends. Both said the terminal was too small for the screen they were standing in for, which leaves the reader with no next action, and the launcher is the worse of the two: without its panels nothing on it can be chosen, so the only way out is to make room. Each now names the size the full view needs, from the same constants the view guards on, and says that resizing brings the panels back on the next frame. The dashboard's armed stop now reads `q AGAIN TO STOP`, naming the repeat the launcher has always named on its own armed line. The launcher's status line no longer lets the filter input hide a run that cannot start: a box with no agent CLI installed, or a dirty tree with more than one lane, read as a working launcher for as long as a search was open. A filter that has matched nothing stays the hint's own sentence, so a search does not warn on every keystroke.
- A stacked pull request's summary could open a code span that swallowed the rest of the paragraph, and was built far larger than the 400 runes it keeps. The backtick was replaced at one call site rather than where the prose is flattened, so only the overview got it and a title or a declared scope reading `restore the parser path` rendered with `parser path` set in code and no other reader able to tell. The overview's bound was also applied after the per-file notes were joined, so a review that reported one note per file spent megabytes of string building text the render then threw away. The replacement is in the flattening, so every prose position gets it, and the bound is spent while the notes are collected, so the work stops at the character the render would have cut at.
- An agent name is now normalized before its case is folded, at every site that stores one or looks one up. Folding first split the two spellings of a name apart: the decomposed capital I a macOS filesystem hands out lowercases to an `i` followed by a combining dot that no composition takes back, so `-a` given the decomposed spelling of a registered name was rejected as an unknown agent, and simple case folding did not close the gap either, leaving a definition registered `İşık` unfindable under the `işık` every lookup folded to. `-a`, `--tools` and `--bin` now key on one spelling, the one the review `Signals:` line and the pick filter already use.
- An unlimited run (`--max-loops 0`) no longer accumulates one in-memory result record per review per loop for the life of the process. Every number the summary reports is now folded in as a result arrives and stays exact for all of them, which also makes a stacked run's per-loop line accounting constant-time instead of quadratic. The per-review detail list (the pull-request and failure rows) is bounded at the most recent 2000 results and the summary says how many earlier ones it counted but did not list, rather than printing a short list as the whole run. A hot reload also stops serializing the whole history into its handoff file, which on a long run grew past the 16 MiB read cap its reader applies, and the reader treats an oversized blob as corrupt and exits on: such a run could lose its resume entirely.
- `doctor` now removes probe files an earlier interrupted run left in the state root, so a killed `doctor` no longer leaves one file behind per run.
- The dashboard's list of unmerged branches is bounded, and it no longer opens the help overlay. A merge conflict leaves a branch on disk until a human takes it, so the list only grew, and an unlimited run has no bound of its own; the overlay spends a fixed screen on one line per conflict, so a run that conflicted enough branches pushed the key bindings the page exists to document below the fold. The 16 most recent are kept, every conflict is still in the run summary and the journal, and both places the list is drawn say how many the bound left out rather than showing a short list as the whole run. The small-terminal fallback leads with that count, because the row is clipped at the terminal's width and a count at the end is the part that gets cut.
- Slug the leaf `worktreeDir` builds its path from, so the guarantee that a checkout stays under `.gauntlet/worktrees` is enforced by the one function that turns a name into a path rather than by each caller's discipline. No caller's own name changes: every one already passes a slugged fragment.

### Security

- Blank the signing program and the `commit`/`tag`/`push` `gpgSign` toggles from a reviewed repository's own `.git/config`, the way smudge filters, merge drivers, and credential helpers already are. Signing is the one program a repository config reaches with no attribute file involved, so a repository carrying `gpg.program` beside `commit.gpgSign = true` had that program executed on the first commit a review made. A review's commits are written by the CLI rather than by the operator, so a repository that asks for them to be signed does not get them; an operator's own `gpg.program` through their global config is untouched.
- Open the run journal and the run index with `O_NOFOLLOW` on the append that reaches an existing file. A `GAUNTLET_HOME` that resolves beside the working directory (no usable `HOME`) puts the state tree inside the reviewed repository, where a committed `.gauntlet/runs/<shard>/<id>.jsonl` symlink would otherwise have received the run's paths, prompt names, and agent output.

## 1.28.0

### Changed

- A microagent review now gets live token counts from microagent's own session log as well as from the usage line it prints. The bundled transcript reader is toktop v0.21.0, which learned the store: one JSONL record per model response under `~/.microagent/sessions`, carrying that response's counters, the working directory it ran in, and `elapsed_ms`, so the rate is taken over the time the model spent rather than over the gap to the previous response, which covers the tool calls in between. No flag or definition is involved; the store is read wherever session transcripts already are, and `-tags notoktop` still drops the whole path.
- The same bump brought the reader's newer adapters, so five lanes that reported a rate only under `--stream` now report one from their own session logs too: `gemini` (`~/.gemini/tmp`, the project named by `.project_root`), `kimi` (`~/.kimi-code/sessions`, one directory per project derived from the working directory's hash and confirmed by the cwd the session records), `cursor-agent` (`~/.cursor/projects/<project>/agent-transcripts`), `grok` (`updates.jsonl`'s `turn_completed` record, whose duration is the span its rate is taken over), and `agy` (`transcript.jsonl`, workspace from `history.jsonl` or `cache/last_conversations.json`). `dsh` is read from the one project directory the review's working directory names instead of walking the whole store, which is why the dsh fixture in the usage test had to move under that derived name. The bump also moved `modernc.org/sqlite`, `modernc.org/libc`, and `klauspost/compress` to the versions that release requires.

## 1.27.0

### Added

- `microagent` is a supported agent, invoked `microagent [--model ID] [--reasoning-effort LEVEL] -p PROMPT`. It needs no approval flag, has no prompt-mode resume, and asks for no stream flag: its stdout already carries one `{"type":"usage","usage":{...}}` line per response beside the model's own text. Gauntlet reads that line either way, as the stdout counter any agent may print and, under `--stream`, as a JSON event, so a microagent lane shows live tokens and the reasoning share with no flag added to its argv. `microagent:model@effort` parses like any other spec, with `--reasoning-effort` taking the levels the binary documents (`minimal`, `low`, `medium`, `high`, `none`). No `~/.gauntlet/agents.json` entry is needed; one that defines `microagent` is now refused at startup as a built-in redefinition, as it already was for every compiled-in name, so delete that entry.

## 1.26.0

### Added

- The help screen names the flags each subcommand reads. A subcommand refuses every flag outside its own, so `gauntlet runs --jobs 4` fails with a usage error naming what `runs` takes; the answer was only available by spending that exit code or by reading `docs/CLI.md`. The screen now carries the same list, generated from the table the refusal reads, so the two cannot name different flags.
- `gauntlet runs --json` prints the run index as one JSON object on stdout: the absolute state paths, the index rows with every count a number rather than a humanized column, and every pruned id instead of the five the table names. Nothing else is written to stdout there, so a pipe carries the document alone, and an empty listing is an empty array rather than a message. The table is unchanged for a person, and `gauntlet show` already emits one JSON object per journaled event, so a script has a parseable surface over the run history. The document also carries a `history` object: the journals on disk, the index rows, the runs the two copies tell apart, and the quarantined runs, which is what `gauntlet doctor` prints on its `Run history` line. A restore from a copied state root is checked against those counts, because doctor's exit code answers whether an agent CLI is installed and not whether the history came through whole; a tree that cannot be read exits non-zero rather than reporting zeros.
- `perfectionism-review` finishes work the code itself shows is unfinished: the missing half of a pair, the last unhandled case of a closed set, and a shortcut the code admits, and only when a sibling line already determines the edit. A hole with no such line is reported and left. It does not invent the missing behavior. A documented or tested contract stays with `functionality-review`, a comment the code has since made obsolete stays with `doc-review`, narration stays with `slop-review`, and a swallowed or misclassified error stays with `error-review`. Each of those now points the sibling case here. Joins `standard`.
- `gauntlet update` keeps the binary it replaced, beside the new one as `<binary>.previous`. The rename that installs a release is the one step an update cannot undo, so a release that installs cleanly and then misbehaves had no way back short of a build or a second download. The copy is written atomically before the rename, with its mode and its directory entry made durable, and an update that cannot write it is refused rather than performed without a rollback path. Rolling back is `mv <binary>.previous <binary>`, and a run in flight hands over to the restored binary at its next safe point, the same way it hands over to a new one. The copy is the version before the last update and no further back. `make install` keeps the binary it replaces under that same name, so a locally built install rolls back the same way.
- Every release signs a build-provenance attestation for each binary it ships, one statement per entry in `dist/checksums.txt`. A checksum says which bytes shipped; the attestation also says which workflow and commit built them, and a consumer can check it with `gh attestation verify gauntlet_<platform> --repo maci0/gauntlet` instead of trusting the release page. `gauntlet update` does not read it, so an automatic update still installs what the release serves; the threat model records that as it stands.
- `make tidy`: fails unless `go.mod` and `go.sum` are what `go mod tidy` writes, reporting the diff rather than applying it. A module that stopped being imported is still downloaded and still hashed on every build, and `-mod=readonly` stops a build from noticing. `make check` runs it, so it is CI's first step too.
- Every component of `sbom.json` carries the SPDX identifier of the license the module ships. The inventory named what a release links and at which hash, and said nothing about the terms it links it under, so a consumer could not trace a grant back to its origin without rebuilding the release and reading the module cache. `cmd/sbom` reads each grant from the module directory the toolchain reports and records the license as CycloneDX `licenses[].license.id`; a module whose grant is missing or unrecognized carries no `licenses` field and is named on stderr, rather than the document claiming the nearest license it could.

### Changed

- The run journal keeps the account name out of the text it stores. The command line on an index row, and the free-text field of an event, are written with the home directory shortened to `~`: an argument like `--dir` or `--log` and a git or path error both carry the absolute path of the reviewed tree, and that path names the operator on the machine. The journal is the copy that gets backed up, synced, and pasted into a bug report as `gauntlet runs --json`, so it now records `~/src/gauntlet` where it used to record `/Users/alice/src/gauntlet`. The live terminal, `--log`, the `dir` field events and summaries are matched on, and the `path` field the listing opens are unchanged: the paths a person typed still resolve, and the ones a machine matches on are still the ones it wrote.
- Four bundled reviews stopped claiming ground a sibling already owns, and stopped asking for a number the repository never states. `design-review` asked for a caching and derived-data strategy covering invalidation, staleness, and coherence, which is what `cache-review` reviews, and the two never referred to each other; it now asks for derived data with no stated recompute trigger and points at `cache-review` for the mechanics of a cache that exists. `code-review` limited its dead-code section to unused imports and dead locals in a file already open, then listed over-engineered abstractions and pointless wrappers under it, the first of which `minimalism-review` owns and the second of which the same file assigns to `slop-review`; the two bullets are gone. `error-review` said twice, in two sections, not to recommend try/catch everywhere, and the second copy now carries only the part the first lacks. `perf-review` asked the agent to consider expected scale; it now sizes every such claim against a number the tree states and says the threshold is unstated where the tree states none, rather than leaving the agent to invent one.

### Fixed

- A review that failed because the provider would not serve it was launched again anyway. The retry backoff assumed every failure was transient, so a run whose usage window was spent, whose key was rejected, or whose pinned model the account could not reach spent a whole review budget per attempt to hear the same answer, on the same agent, and could do it once per agent in the pool. The runner already reads the agent's last line to explain a failure; it now reads the same line to decide, and spends no attempt when the answer is a spent quota, a rejected key, or a model that does not exist. A 429, an overloaded region, and a dropped connection are still retried, since those are what the backoff is for, and the fallback to a different agent still runs, because another CLI may hold another account (`--usage-limit` is what stops a run whose agents share one). The line is also on the review's failure row in the journal now, so a run that stopped this way says why without the terminal in front of it.
- The merge lock no longer covers the run's own reporting. A log line is a blocking publish on the event bus, so a subscriber that stopped draining (a terminal nobody was reading, a full journal buffer) parked whichever lane held the merge lock, and with it every other lane waiting to merge. Under `--jobs N` one stalled reader froze the whole merge phase, and the conflict step, which runs under that lock for as long as its agent takes, took every lane down with it. The merge and conflict steps now collect their log lines and publish them once the lock is free, in the order they were produced.
- Per-review and lane worktrees left behind by a killed or crashed run are reclaimed at the start of the next run. AddWorktree prunes git's metadata for checkouts that are gone from disk, but `os.Remove` on a non-empty directory fails, so one interrupted run left its checkouts pinned in `.gauntlet/worktrees` for every run after it. A startup sweep under the run lock now removes every checkout directory git did not clean up and the worktree root itself. Planted symlinks under the root are skipped, so a link inside the reviewed tree cannot redirect the sweep into an arbitrary path.
- `--token-budget` cut the loop short before its commit and merge steps, so the reviews it did let run were never committed and never merged, and `--merge-into` reported nothing. The budget returned out of the loop rather than breaking out of it, which is the shape the graceful finish and `--usage-limit` use, and those two still committed and merged what they had run. All three stops now behave alike, and the run ends after the merge step.
- `gauntlet runs` and `gauntlet runs --json` disagreed about a state tree they read: a pruned run list that could not be read was reported as an error and a nonzero exit by the JSON form and silently dropped by the table, so the table claimed nothing was recoverable. `--json` documents that its errors and exit codes are unchanged by the flag, so the table reports the same failure the same way.
- `--token-budget` was a ceiling per directory rather than per run. A process given several trees builds one runner per directory, each with its own token total, so a three-tree run under `--token-budget 1000000` could report three million before any runner stopped. The runners share one tally now, and a hot reload seeds it with the tokens its predecessor carried, so the ceiling survives the swap as it already did within one runner.
- A review that removed lines an earlier review had added was credited with twice the deletions it made. Both cumulative line samples are diffs against the same baseline, so removing an added line moves insertions down and deletions up by the same count: the two readings named one event, and the conversion added them. The number reached the review's own result, the `review_end` event, the index row, and the dashboard footer, so a review that reverted a file reported 20 deletions for 10 lines.
- A lane merge committed on a failed index read. The check that decides whether a squash staged anything treats every nonzero status as "something is staged", so a timeout, a killed process, or a broken repository at that point ran `git commit` on whatever happened to be in the index. It now reads the index the same way the worktree commit beside it does, and a broken read reports the failure instead of committing on it.
- A branch name git refused and a `check-ref-format` that failed for want of git, a timeout, or a killed process reported the same thing, and the message said `stack branch` only on some of the paths. One validation answers all three now, so a reader is sent to fix a name only when the name is what git refused.
- `.git/info/exclude` was read whole, with no size bound, on the way to checking it for two short strings. It is the one read in the package that was not capped, and a repository shipping a very large one made a run allocate it before the first agent started. It is bounded now, like every other file read here.
- Removing a lane checkout cleared the handle to its directory before the removal ran. A removal that failed left the only record of where the checkout was already discarded, and the retry the idempotent contract promises found an empty handle and reported success over a directory still on disk. The state clears once the directory is gone, as `Advance` and `DiscardCurrent` already did.
- A repository whose first commit is an empty tree was scanned by walking the filesystem instead of by git, so the churn window was never read and every language was weighted as if nothing in the tree had ever changed. An empty listing from git is an answer, and it is taken as one.
- A terminal too short for the launcher kept a warning or filter line where the comment said it kept the composed command, so a blocked run with a short terminal showed the reason and dropped the command it was blocking.
- The threat model cited code that had moved again, in the rows covering the agent registry, the CLI flags, the run listing, the parsed line counts, and the temp sweep. A reader checking a claim against the function it named was sent to the wrong place, which reads as checked. The pointers name the current lines, the model now records the precedence a `--agent-cmd` has over a definitions file and the stat the sweep decides removability from, and its last-reviewed stamp carries the commit it was read against.
- `gauntlet update` and `make install` installed over a binary that already was the release, overwriting `<binary>.previous` with the bytes just installed, so a second `gauntlet update`, a second auto-update tick after a reload, or a second `make install` of an unchanged build left the rollback copy naming the version it replaces and nothing to roll back to. Both now skip the install when the installed binary already matches, so the copy is the one the update before it made.
- The launcher threw away a composed run on one `esc`, while `q` asked twice. `esc` is the key a keyboard user reaches for to back out of whatever they are in, and here the one thing it could reach was the whole screenful of picking: a press meant to dismiss something, or a second press of a habit formed on the dashboard, discarded it with no way back (WCAG 3.3.4). It now asks the way `q` does, and the status line and the key legend name which press confirms and which declines it, so the pair is two ways to ask rather than two spellings of one destructive key. `ctrl+c` from the filter input cleared the search instead of leaving, even though every other screen treats it as the way out: a reader reaching for it lost their search and stayed (WCAG 3.3.2). `esc` is the key that clears a filter, and it still does. The small-terminal fallback also named the arrow keys while typing, over a pane it does not draw, where moving a cursor nothing points at is a key a keyboard user cannot tell from a broken one.
- A prune could move a run's journal while that run was still writing to it. Two gauntlet runs sharing one `GAUNTLET_HOME` overlap easily, and a run long enough to finish behind a later one sorts past the `--keep-runs` window: the other run's prune renamed the live event stream into `pruned/`, the finished run's index row then named a file the listing no longer held, and the run dropped out of `gauntlet runs` while `gauntlet status` reported a disagreement it could not repair. A journal a run has open is now left where it is, with its index row, however far past the bound its start time puts it.
- The sweeps that clear interrupted writes (`gauntlet update`'s partial download beside the binary, a run's snapshot index in the git directory) and the journal listing all judged what an entry was by the type the directory entry carried. A filesystem that reports no entry type (some network and FUSE mounts, XFS without `ftype`) hands that type back as irregular, so on a state root or a repository on such a mount the sweeps never removed anything and the partial downloads accumulated beside the binary forever, and a directory carrying a run id was listed as a run whose journal could not be read. Each now settles the question from the stat it already takes for the modification time.
- `gauntlet runs` printed its columns at widths chosen when the format was written, and a run id is a UTC stamp plus the pid in hex, so it is 17 characters on a machine that has not handed out pid 1M and 24 on one that has. The header lined up on the first and every column after RUN was shoved sideways on the second, which is most current machines. Every column is now as wide as its widest cell, so the table lines up whatever the ids, token counts, and line counts in it are, and the last column carries no trailing padding.
- `gauntlet --help` documented `--resolve-conflicts` and `--hot-reload` as defaulting to true without saying how to turn either off, so a reader of the help screen could not tell that they were switches at all, while `--stream` on the same screen named its own `--stream=false`. Both name theirs now, and `docs/CLI.md` says what `--hot-reload=false` leaves a run doing.
- The dashboard's feed identified an error the agent reported by its hue alone. Every other line kind names itself in its own text: a diff line carries the sign it was added or removed with, a result line begins `RESULT:` or `PATH:`, reasoning is already italic, progress says what it is doing. An error line is the agent's own sentence about something that broke, so under `--no-color`, on a monochrome terminal, or to a reader who cannot separate the hues, the one line kind the feed's signal filter exists to surface was the one line that read as ordinary narration (SC 1.4.1). It carries a `!` in the line's own style now, and the help overlay names the mark beside the review glyphs.
- The dashboard's help described a `ctrl+c` the handler does not implement. It claimed `ctrl+c x2` quits a draining finish, where one press has always closed it, and it left the key out entirely on a run with nothing to finish into, where `ctrl+c` is the only key that stops the run outright. The help is a keyboard reader's only list of what the keys do, so it names `ctrl+c` in all three states now, and no longer asks for a second press that closes a run the reader can no longer watch.
- The launcher's key line named one action for the arrow keys on all three kinds of review row. A set header folds both ways, a review inside one folds only left, and the suggest row folds nothing at all and only steps between panes, so the legend advertised a control that does nothing on the two rows that are not a header. The segment follows the row under the cursor now, reusing the `fold` wording the narrow legend already had.
- The launcher's arrow keys did nothing on the suggest row, the one row the key line names them `pane` for. There is no fold to make there, so they step to the neighbouring pane instead, and the help says as much.
- The dashboard's help and the launcher's both left `ctrl+c` out of the line that says how to close the overlay, while the dashboard's own `ctrl+c` entry below it read as a promise to stop the run. While the overlay is up it closes the overlay, so the closing keys name it, and the entries after that line say they are the dashboard's own.
- The launcher's help did not name the filter's own editing keys. `ctrl+u` clears it and `ctrl+w` drops the word before the cursor, both of which it has always done; a reader fixing a typo had backspace and nothing else.
- The track tone drew the unlit remainder of every meter and the chart's baseline at exactly 3.00:1 on the light background: the SC 1.4.11 floor with no margin, and a stroke one rounding step from failing it. The light tone is darker, and the contrast test now pins every step of the heat ramp to that floor and to the fraction it is drawn at, not only the track the ramp starts on.
- An empty `PATH` was answered two different ways. The agent lookup fell back to `$HOME/.local/bin` and the system prefixes, so a box that launchd, systemd, or `env -i` started found its agent CLI, while `runx.AbsPATH`, which git, `gh`, and the usage probe are resolved through, searched nothing: `git` came back unavailable and every run on that box failed with "git is not available". One fallback now lives in `runx.AbsPATH`, so every executable is resolved the same way, and it widens the set of executables a run will launch to the same fixed list of absolute prefixes the agent lookup already used, not to anything the tree under review can name.
- The test suite read the operator's own `~/.gauntlet/agents.json`, because every flag-parsing test reaches the agent definitions and the state root is the home directory unless `GAUNTLET_HOME` says otherwise. A definition written for real work was loaded into the tests, and a second parse in the same process then failed to register it again, so what the suite asserted depended on what the machine happened to carry. The package now runs against a temporary state root, which is the state a new install is in.
- A log prefix and a journal replay stamp named a local wall clock and dropped its zone. `15:04:05` alone is one ambiguous reading per day: a run long enough to cross local midnight prints `23:59:59` and then `00:00:01` as if time had run backwards, and on each DST transition the same local hour is either absent or repeated, so a run logging `02:30:00+02:00` and an hour later `02:30:00+01:00` in Europe/Warsaw printed the identical stamp for two events an hour apart. The suggest-step log, the plain reporter's prefix, and the `gauntlet show` timestamp column carry the offset now (`15:04:05-0700`, still the 13 columns that column was already padded to), through one `humanize.Clock` so the three surfaces cannot disagree.
- Three durability gaps in the state tree and the install, all in the window a power cut falls into. A hot reload that failed to exec removed its handoff with a plain unlink, so a machine that lost power before the filesystem recorded it kept the handoff, and the next start read counters from a run that had already finished. An update recorded the directory that renames the new binary in only half the sequence: the rollback copy's name was made durable, the install's was not, so a cut between the two left the old binary under a name an update had reported replacing. The retention bound's quarantine dropped the journals it had evicted without syncing the directories that held them, so an evicted run could come back after a cut and the bound would not hold. Each is synced now, and a failed reload reports a handoff it could not drop.
- Bundled review prompts: `minimalism-review` and `perfectionism-review` rated findings `high / medium / low` where the rest of the set uses `critical / high / medium / low`, so a reader or a summary sorting on the scale had two vocabularies for one weight. Both carry the four levels now, with the domain weighting in the parenthesis, as every other prompt does.
- `config-review` asked the agent to consider the production and onboarding consequences of a default and to prefer better patterns, and never bounded an auto-fix pass, so a preference could arrive dressed as a finding and a pass had no stated limit. It now names the value, the default, and the path that breaks, keeps an improvement only where the repository already uses that pattern, and fixes one default or one validation at a time.
- One run could be listed twice, out of order. The listing ordered journals by shard directory and then by run id, so a journal filed where its id does not name (a hand-named run, a tree rearranged by hand, a backup restored beside itself) was printed at the position of the directory it sat in, and the same run id under two shards produced two rows, one of them out of order, each spending a slot of the `--keep-runs` window. Runs are now ordered by run id across the whole tree, and one id is one run: the copy in the shard the id names wins. `gauntlet runs --restore` no longer puts a second copy of a run under an id already in the listing, and `journalPath` asks the tree where a run is rather than deriving a path that `Open` does not use.
- A quarantined run whose id names no shard was invisible. It is filed at the top of `pruned/`, and the quarantine walk read only the shard directories, so `gauntlet runs` did not name it as restorable, `doctor` did not count it, and the keep bound never reached it, which left the unbounded history `--keep-runs` exists to stop. Both trees are now walked at the top as well as per shard, and a shard that cannot be listed is reported rather than passed over in silence.
- The launcher's concurrency row had no ceiling on the arrow keys. The meter draws the job count against the machine's cpus and the summary reads "N of M cpus", but the `+` key, the row's own space toggle, and the right arrow on the options row each carried their own idea of the bound and the arrow keys carried none at all, so holding right on the row set `-j` to whatever it liked and the runner cut that many worktrees. Every key that moves the count now goes through one bounded step, and a machine reporting one cpu is one lane, as the legend already said.
- A removed git credential helper stayed in force for the rest of a run. The `-c` overlay that blanks a planted `credential.helper` puts the operator's own system and global helpers back behind the reset, so its value depends on those config files as well as on `.git/config`, but only the local one was watched for a change. A helper the operator removed mid-run, in the same window in which a review can write config itself, kept being asserted into every later git call. The overlay is now rebuilt when any config file it was read from changes, and the global and system scopes are read with `--show-origin` so an included file counts too.
- `.venv` is gitignored. The maintainer scripts run under uv, and `uv sync` writes a virtual environment into the repository root, where nothing kept it out: `make release` refuses a tree carrying an untracked path, so a developer who synced once could not cut a tag, and `make repro` archives the working tree, so the two copies it builds from carried a `pyvenv.cfg` naming that machine's interpreter. The screenshots script also pins the build baselines the Makefile pins, so the checked-in PNGs come from the same configuration as the release binaries, and points `go test` at a disk-backed temp directory.
- A review that ended in a status this build does not recognize was counted by no tally. A hot reload continues a run from a handoff the predecessor wrote, so a status a newer build named reaches an older one; nothing claimed it, the run's index row counted reviews its own buckets did not add up to, the plain reporter's total was short of the dashboard's, and the dashboard's grid showed a clean sweep of fewer reviews than ran. The tally has an `other` bucket now, the reporter prints it, and the row reconciles the way one rebuilt from the event stream already did.
- A merge could hand the next review the previous one's line count. The cumulative line sample is debounced for 750 ms, and neither `Merge` nor `PullRebase` dropped it, so a sample taken right after a review's branch landed in the tree could be the one measured before that merge, and the review that sampled next was credited with the merged review's insertions. Both drop the cached sample now, the way the snapshot restore already did.
- A hot reload could lose a directory's progress. The handoff is keyed by the directory path and written as JSON, and JSON rewrites every byte that is not valid UTF-8 to U+FFFD in both directions. A run in a directory whose name holds a byte outside UTF-8 (legal on Linux) therefore came back keyed under a name that was not the one it was written with, every lookup missed, and the successor re-ran the loops that had already finished, re-asked `--suggest`, and refetched the base commit a resumed stack was pinned to, which renames every layer and splits the chain. The key is now repaired and composed, so both processes spell it the same way. A decomposed directory name (what macOS hands out) resolves the same way.
- A release could be published under a version older than the newest one, or from a commit `main` does not carry. A tag is the whole release process and the only gate on it was a CHANGELOG section, which an older released heading satisfies: `releases/latest` is what `gauntlet update` and the README install resolve, so publishing a stale version there is a downgrade handed to every consumer, which the workflow's own immutability rule then refuses to repair, and a tag off `main` ships code the next release from `main` reverts. The workflow now asks the API for the published versions and the clone for `main`'s history, and refuses either before it builds. `fetch-depth: 0` is what gives it that history.
- A line count too large for an `int` was reported as a review that deleted nine quintillion lines. The counts come out of `git diff --shortstat`, and `strconv.Atoi` hands back the clamped maximum beside its range error rather than zero, so a figure that does not fit wrapped negative the moment the untracked files' own counts were added to it, and the sum went to the journal, the index row, the dashboard, and the token report. A count that does not parse, does not fit, or clears a bound no diff can reach is now read as no count.
- A run could hang at shutdown behind a subscriber that had stopped draining. The event bus held its read lock across the delivery of an event, and a delivery to a full channel blocks, so one subscriber that stopped reading parked every later `Subscribe` and `Close` behind it, and a run that ended while that was true never exited. The subscriber list is now read under the lock and delivered outside it, and `Close` waits for the deliveries already under way before it closes the channels, which is what keeps the snapshot safe to iterate.
- The per-agent stats table padded its label to 20 characters counted as runes, while the rest of the output measures terminal columns. A `--agents claude:café` label shifted the `ok=`/`fail=` columns left on that line alone.
- Run history ordering: two runs started in the same second carry process ids of different hex widths (`1f4` for 500, `1f400` for 128000), and the listing, `--keep-runs`, and the prune quarantine ordered runs by comparing those ids as text, which files the later run as the older one. A prune that landed on a same-second pair kept the older run and moved the newer one out of the listing. The order now reads the id's tail as the number it is.
- Dashboard: a lane's elapsed column and its timeout meter were read against the review_start timestamp off the event stream, which carries no monotonic reading, so an NTP step or a manual clock change during a review emptied the meter and rewound the elapsed column on a run the timeout timer had not touched. Both are read against the dashboard's own clock now, the same clock the timeout itself is measured on.
- Bundled review prompts: an auto-fix allowance that contradicted its own limit, or asked for a value the agent has no source for (a resource-request number, a cache TTL, a timeout, a database constraint written over rows that may already violate it, a lock at a site that already runs under one). Each now names the source the value must come from, or says report instead of edit. New: `webperf-review` had no auto-fix limit at all, and a cache or compression header is a repository-wide edit.
- Bundled review prompts: overlap where two passes would fix the same code by different rules (`infra-review` restated the `dx-review` contributor path, `api-review` demanded the changelog `release-review` owns, `ux-review` claimed the labels `a11y-review` fixes, `dr-review` and `idempotency-review` both owned the ack-before-durable write), and `doc-review`'s output template asked for the deletion candidates its own instructions hand to `slop-review`.
- Bundled review prompts: `fuzz-review` was told to add harnesses without running them, and to cover real payload shapes without saying where samples may come from; `deps-review` never required a build after a manifest edit; `dx-review` could start a container or server to exercise a flow.
- Bundled review prompts: `container-review` shipped two findings with their confirmation already filled in; `arch-review` asked for JS-style barrel files in any language; `test-review` asked for merging assertions; `time-review` offered `date -d` without saying it is GNU-only; `prompt-review` named a file class the loader cannot dispatch.
- `make check-scripts` read the local uv version with `uv version`, which reports the version pyproject.toml declares (`0.0.0`) rather than uv's own. The drift note compared that placeholder against `UV_VERSION` and fired on every run, so the one signal meant to separate a resolver difference from a clean tree was itself always red. It reads `uv --version` now.
- `make release` refuses the `dev` default `VERSION` carries. Nothing derives the version from the tag on its own: the workflow reads it off `GITHUB_REF_NAME` and passes it in, so a release rehearsed from the documented command line inherited the placeholder and built a complete, self-consistent release at a version no tag names, with `gauntlet_dev_darwin_arm64` assets and a binary reporting `gauntlet dev`. `make smoke` passed it, because it compares the stamp against the same `VERSION` it was handed. The check runs before the suite and the four cross-compiles.
- The 1.25.0 notes are the notes GitHub shows. Its `### Fixed` heading sat on the line after the last `### Changed` bullet, which a heading needs a blank line to be one; the workflow dumps a section verbatim as the release notes, and the section is now repaired. `TestChangelogSectionsAreWellFormed` checks every heading in the file, so the next one fails the suite instead of a release.
- The dashboard's `REVIEWS` and `FEED` panel titles drop whole readings on a narrow terminal and mark what went, instead of being cut mid-word. A title reading `3 lin…` named a distance the screen never states.
- The small-terminal fallback keeps the run state and the clock on its first row at every width, giving up the version and the loop number first. The row was cut at the right, which took the state with it (`‖ FEED…`) on exactly the terminals that have least room for it.
- The small-terminal fallback names `esc live` while the feed is held or scrolled back. It draws no feed, but it does report the state in its header, and nothing on that screen said how to clear it.
- Two checks reported success over work that had failed. `make tidy` read `go mod tidy -diff` into a command substitution, whose exit status the following `if` discarded, so a tidy that could not resolve the module graph wrote no diff and passed; a fetch error or an inconsistent graph read as a clean tree. `make artifacts` verified `dist/checksums.txt` in a `||` chain that a later `for` and `echo` overrode, so a checksum that matched nothing still printed the success line. Both take the failing status as the recipe's own now.
- The dashboard's `FEED` title counts the branches left unmerged and stops there. A kept branch is work only the reader can do, and the only place on screen naming it was the help overlay, which nothing pointed at. The count names the key that lists them.
- The launcher's empty `AGENTS` pane was cut mid-word at the launcher's own widths (`none installed (see: gauntlet doc`), because a panel is padded to its width rather than wrapped and nothing marks the cut. The row now says what is missing; the sentence naming `gauntlet doctor` is on the status line, in the help, and in the narrow fallback, all of which have room for it.
- The launcher's status line and composed command were cut at the right with no marker, against the rule the rest of the screen follows. At fifty columns the blocked reason stopped at `(see: gau`, which reads as a command that does not exist, and a long `$ gauntlet` line looked like the whole run. Both now end in the ellipsis that says they are cut.
- The launcher advertised `→/← open and close it` on a set header while a filter was live. A filter holds every set open, so left only moved the cursor: the hint promised a fold the tree could not make.
- Two halves of the quarantine the prune bound already had for the listing. A run id filed twice in `pruned/` was counted twice, so a duplicate spent a slot of the keep and could unlink a real quarantined run to stay inside it, and the id was printed twice in what a user is told they can still restore. The walk now dedupes by run id and prefers the copy in the shard the id names, exactly as the listing of `runs/` does. A restore also left the shard it emptied behind, one empty directory per restore, which no later trim can clear: that one unlinks files, and the emptied shard holds none. The move now removes it, the way the prune's own move does.
- A failed commit step was the one failure the narrowed feed dropped. The classifier read the verdict at the start of a line, and the commit and merge steps carry it in the middle (`commit+push step FAILED (codex), exit 1`, `Not merging into main: this tree is on a detached HEAD`), so those lines were read as narration and filtered out while the run reported `Commit steps: 1, 1 failed`. Mid-line failures classify as errors now, as their siblings already did.
- `gauntlet runs` printed each run's STARTED as a local wall clock with no zone, the one timestamp surface the offset fix above had not reached. A local wall clock names one instant per day unambiguously, so on each fall-back the listing gave two runs started an hour apart the same STARTED and could not say which ran first: in Europe/Warsaw on 2026-10-25, 00:30 UTC and 01:30 UTC are both `2026-10-25 02:30:00`. The column carries the offset now, as the log prefix and the `gauntlet show` stamp already do, and the test names the repeated hour rather than a date that happens to be an ordinary one.
- The threat model cited code that had moved. Every claim about the environment row pointed at line numbers the files no longer had: the `GAUNTLET_HOME` check, the `GIT_SSH_COMMAND` override, the state root, the custom-definition path, the motion and color readers, and the re-exec handoff, plus two ranges past the end of their files. A reader checking a claim against the function it named was sent to the wrong place, which reads as checked. The pointers name the current lines, and a test walks every `file.go:NN` in the document and fails when one names a line the file does not have, so the next move of a function cannot go unnoticed.
- The help screen left out two rules the flags enforce. `--usage-cmd` and `--usage-limit` are refused unless they are used together, which neither line said, and `--retries` did not name the ceiling the backoff stops at. Both are named where a user reads them before setting them.
- `--agent-cmd` could not override anything. The file is loaded first and the command line second, and the docs, the example on the help screen (`--agent-cmd pi='pi -p {prompt}' -a pi`), and the shipped `omp` note all say the command line wins for its run, but the registry treated a second definition of one name as the duplicate it refuses inside a file, so a name a definition already claimed (including every one gauntlet ships) aborted the run at startup instead of being replaced. A name given on the command line is now dropped from the registry before it is registered again, and a built-in tool is still refused, because a tool is not a definition to replace.
- An agent `note` holding a control or formatting character reached the terminal unfiltered, in `gauntlet doctor` and on the launcher's own line, while an agent *name* holding one is refused at load. A newline started a second row, an escape sequence colored text the run had not styled (and did so under `--no-color`, which suppresses only gauntlet's own styling), and a right-to-left override reversed the rest of the line. A note is refused the same way a name is, rather than rewritten.

### Security

- A credential helper planted in a reviewed repository's `.git/config` no
  longer runs. `credential.helper = !command` is a shell command git executes
  the first time it needs a credential, so a repository unpacked with such a
  line in its config, or a review whose agent wrote one, had arbitrary code
  run in this process at the next push. The per-repository overlay now blanks
  the key alongside the filter, merge, diff, editor, and askpass drivers it
  already neutralized, and re-asserts the operator's own system and global
  helpers behind the reset: an empty helper resets git's helper list, so a
  bare reset would have dropped `gh auth setup-git` and the macOS keychain
  along with the planted entry and stopped the run's own pushes from
  authenticating. A repository that names no helper is left alone.
- Inline Markdown in a stacked pull request's body is escaped. The Summary
  section carries an agent's per-file notes and a review's own `Summary:`
  line, both read from the reviewed repository, and both reach it after a
  prompt that read the same untrusted sources. Flattening the text already
  made an injected heading or code fence inert, but a link, an image, an
  autolink, or a raw tag needs no line of its own: a note could put a
  tracking pixel, a look-alike host, or a `<details>` block over the real
  diff in the body a reviewer reads. `[`, `]`, `<`, `>`, and `*` are now
  escaped in prose, so such a construct renders as the text it is. Paths in
  the file list are code spans and are unchanged.
- A custom agent name carrying a control character or a Unicode formatting
  character is refused at load, and one that is not valid UTF-8 is refused with
  it. The name is the key every agent lookup uses, and it is drawn as a bare
  label in the dashboard and the launcher: a newline breaks a row of the
  launcher, a right-to-left override reverses the rest of the line, and a
  zero-width joiner renders two different names identically. Bytes that are not
  valid UTF-8 reach the journal and come back repaired to U+FFFD, so the agent a
  run selected and the one its record names would differ. A name that has to be
  rewritten is not the name the operator wrote, so the definition is refused
  instead.

## 1.25.0

### Added

- `make artifacts` writes `dist/checksums.txt` and `dist/sbom.json` from the binaries `make dist` built, and verifies the checksums against them. Split out of `make release` so the pull-request `dist` job runs it on every push: those two files are what a consumer verifies a download against, and until now nothing tested them before a tag shipped them.
- `make clean-tree`, and `make release` runs it before anything else. A release is a claim about a tag, and `-buildvcs=false` is what makes the shipped bytes reproducible, so it is also what removes the evidence of what was built: assets built from a modified tree are indistinguishable from clean ones, and the tag names source nobody reviewed. The check refuses the release and prints the paths; outside a git work tree, where a source tarball has no notion of clean, it passes.
- Every release ships `sbom.json`, a CycloneDX 1.5 inventory of the modules the released binaries link, each with its `go.sum` hash. It replaces the `sbom.txt` dump of `go version -m`, which a scanner could not read: the format was the project's own, so consumers and vulnerability scanners had no way to learn what shipped. The new file is generated by `cmd/sbom` from the build info already inside each binary, so it describes what shipped rather than what the current tree would resolve to, and it is reproducible: the serial number is derived from the contents, not drawn at random. The generator uses the standard library alone.
- `gauntlet doctor` reports what the state tree holds: how many run journals are on disk, whether `index.jsonl` matches them, and how many pruned runs are still recoverable. A restore is checked against this rather than against the exit code of the run that wrote the tree.
- A pruned run can be brought back: `gauntlet runs --restore <run-id>` moves its journal out of `GAUNTLET_HOME/pruned/` and rewrites its index row. `--keep-runs` is the only deletion path in the state tree, it fires unattended at the end of every run, and its input is a flag, so what it drops is now moved aside instead of unlinked, bounded by the same keep. `gauntlet runs` names what is waiting in the quarantine.

### Changed

- The maintainer scripts' Python floor is declared. `pyproject.toml` had no `[project]` table, so `uv run scripts/...` had no `requires-python` to read and ran under uv's own default, which the empty lockfile had recorded as `>=3.14`, while both scripts' headers asked for `>=3.11` and mypy checked them against 3.11. The table states the floor the scripts already claimed, and `uv.lock` records it.
- `internal/humanize` gained `Plural`, and the two private `plural` helpers in `cmd/gauntlet` and `internal/runner` are gone. The runner's took a bare noun and appended an "s", so any word not ending in one was miscounted; both callers now name both spellings. The wording they render is unchanged, except that a `gauntlet doctor` count in the thousands is now grouped (`1,234 journals`) like every other count in the tree.
- The release asset `sbom.txt` is now `sbom.json`. Anyone parsing the old file reads the CycloneDX document instead; the module paths and versions it listed are unchanged.
- A `--paths` entry that is not a path is refused. The entries are pasted into the review prompt as instructions, and nothing checked them: a wrapper that built the flag from a file list could put a line break or a line of prose in a file name, and the scope block would carry it as the agent's own instructions. An entry with a line break, a backtick, a control or formatting character, or more than 200 characters is now refused at the flag, and the prompt renders the rest one line per entry, counted rather than clipped when the list runs past its cap. Every other entry is unchanged.

### Fixed

- Git runs in the C locale. Its own output is translated, and that output is what the journal records, what an event carries, and what a failed review reports, so a run on a machine set to a translated locale wrote different bytes for the same work than a CI machine did, and a rerun from the recorded seed no longer diffed cleanly against it. It is also what the line counts are read out of: `git diff --shortstat` answers in the language its own message catalog carries, and both counts were read out of English "N insertion(s)" text, so a translated git reported zero changed lines for a review that changed thousands. `LC_ALL=C` is set for every git invocation now; paths are unaffected, since `core.quotepath` decides those.
- A tree's own past runs are found through a symlink to it. Review history matched directories as text, and on macOS `/tmp` is a link into `/private/tmp`, so a tree first run under one spelling and then under the other read as a tree that had never been reviewed. The path is resolved on both sides before it is compared.
- Skipped directories are skipped whichever case they are spelled in. Prompt discovery compared directory names exactly while the file-signal suggester folded them, so a repository holding `Pods` or `Vendor` was skipped by one scan and walked by the other, and the two disagreed on which project prompts exist.
- The dashboard read the reduced-motion variables through its own list of the values that mean off, while the plain reporter and the documentation went through the shared reader. Two copies of one rule is two answers the moment either is edited, and the copy in the dashboard already disagreed with the documentation on precedence: `GAUNTLET_NO_ANIMATION` is consulted first and an explicit false in it overrules `NO_MOTION` and `REDUCED_MOTION`, which neither `docs/CLI.md`, the help screen, nor `.env.example` said. The dashboard now goes through the shared reader, and the precedence is documented in all three.
- An agent named in a decomposed spelling is the agent the CLI already knew. `--agents` and `--bin` lowercased the name and stopped there, while every lookup of an agent name normalizes to NFC, so a terminal handing over `e` + U+0301 where the definition stored U+00E9 gave one agent two identities: `--agents café,café` scheduled the same agent twice, a retry could draw it again, and the `--bin` override was stored under a key no code path read. Both names are normalized where they are parsed now.
- A file note no longer disappears because its name is not valid UTF-8. Such a name is legal on ext4 and APFS, git hands out its raw bytes, and the agent's line describing it had already been repaired to U+FFFD on the way out of the parser, so the two never met and the sentence about that file was dropped from the pull request overview. Both sides are repaired the same way before they are compared.
- The review listing stays in line when a review is weighted ten times. `x10` is three columns where every other mark is two, and the padding only pads, so the name, project, and description columns of that row shifted one column right of its neighbours.
- A subject crediting a model in words is now recognized as one. The banned phrases that are written with spaces (`generated by`, `written by`, `created by`, `powered by`, `reviewed by`) were anchored to a trailing colon that a subject does not have, so only the trailer forms were ever rejected and `SUBJECT: generated by Claude Code` reached the commit, the pull request title, and the merge message.
- A quarantined run is no longer at risk from a name the package would not open. `trimQuarantine` unlinks whatever falls outside the `--keep-runs` window, and the walk over `pruned/` took any `*.jsonl` stem, so a file named there by anything but a prune took a keep slot and could push a real quarantined run out. The walk now applies the same `validRunID` check the listing does.
- A rebuild of `index.jsonl` no longer spends the row of a journal it cannot read. `Args`, `ExitCode`, and `Elapsed` are written to the index alone, so a journal that failed to open took the only copy of all three with it, permanently. Such a run now keeps the row the index already held.
- A per-review checkout is a second copy of the reviewed repository, and it is now readable by its owner only. `.gauntlet/worktrees` and the checkout itself were created at 0755, so on a machine with more than one local account every other user could read a private repository's contents out of the scratch tree. Both are now 0700, including a tree an earlier run created under a looser umask.
- `make repro` varies the timezone, not only the locale. The Makefile exported `LC_ALL` but not `TZ`, so the second tree copy inherited UTC from the very export meant to pin the first one and the reproducibility comparison never saw a host zone. `TZ` is exported now, and the second copy strips both.
- `scripts/suggest-calibrate.py` scores the build a release ships. It ran its own `go build` with no tags while the Makefile's default tag set is `sqlite`, so a recall number was being read off a flavor of the binary that no release contains, and could not be compared with the run before it. It calls `make build` now; the tag set, the ldflags, and the build environment have one owner.
- A layer published twice into one run journal counts once in the summary. A hot-reload successor re-records a layer its predecessor already published, and the rebuilt `ins`/`del` totals summed both copies, so `gauntlet runs` and the dashboard reported one diff as two. The rows are now summed over layers, keyed on the branch the work landed on, the same rule the review history already used.
- A `--tui` run that failed before the dashboard took the terminal no longer strands its event forwarder. The forwarder parks on the program's unbuffered message channel, which only `Program.Run` ever serves, so a failure between subscribing the dashboard and entering `Run` left that goroutine blocked for the rest of the process, holding the run's bus subscription. The dashboard now releases it on every exit path.
- Every review with an applicability gate now carries an `## Applicability` section in its output format, naming the subject that makes the review fit. The gate already told the agent to print a skip result, but 22 of the gated prompts had nowhere in their template to put one.
- `functionality-review` gained the applicability gate its siblings carry. It is the one review in `quick` with no stated ground to stand on: where no README, help text, public API, or test says what the software is supposed to do, every item below was a finding about an intent the agent had to invent.
- `gauntlet runs` keeps the table on stdout. The trailing line naming the journal directory went there with the rows, so `gauntlet runs | tail -1` printed a path instead of the newest run; it is on stderr now, where a note about this machine belongs. The header and the FAILED legend stay on stdout: they head the table.
- An explicitly empty `--reviews` names the flag. The refusal said "no reviews remain after filtering", naming a filter that never ran; it names `--reviews` and says what to do instead, and `--suggest` gets the same answer rather than dropping the value silently.
- `make repro` archives the same tree whichever tar reads it. The archive's members now come from `git ls-files --cached --others --exclude-standard` instead of tar `--exclude` patterns: libarchive matches a pattern with no slash against the basename of every path component, so `--exclude=./$(BINARY)` also dropped `cmd/$(BINARY)` and both copies of the tree failed to build, while GNU tar anchors its patterns and archived nothing of the sort. A name list has no dialect, and it reaches into subdirectories, where an anchored `./__pycache__` pattern never did.
- `make check` and `make repro` no longer fail on a host whose scratch directory does not exist yet. Overriding `TMPDIR` keeps it exported, so every recipe that runs the go command hands it that path, and go refuses to start when the directory is missing; a fresh macOS runner has no `$HOME/.cache`, which failed the `check` and `repro-macos` jobs.
- The linked-module inventory is resolved for every platform a release ships instead of for the host that reads it. `ncruces/go-strftime` links through `modernc.org/libc` in the darwin build and not in the Linux one, so Linux passed and macOS failed on a row naming a module the build does link. The row is in `docs/DESIGN.md`, and the test unions the four release targets so every runner reaches the same verdict.
- `scripts/shots.sh` no longer trips SC2015: the size check is an `if` rather than `A && B || C`, which also satisfies the older shellcheck the Ubuntu runner image carries.
- Text the terminal has no room for now ends in the marker on both screens. Panel titles, the small-terminal fallbacks, the help overlay, and the run pane's rows cut mid-word before, so a reader saw `AGENTS  none picked: auto-d` and `suggest ag` beside `from the poo`, and could not tell a word the program wrote from a word the terminal cut. The launcher's run pane also keeps a floor for its own rows: at 80 columns its option names were cut because the review tree took the room, and the two panels on the right now divide the rows they actually have rather than reserving one panel's worth for the pair, which left empty rows under a full-height run pane. A dashboard footer whose last reading did not fit ends in the marker too, so a budget meter that fell off the line is not read as a run with no budget.
- A review reporting no usable duration no longer keeps the previous run of the same name. Zero, a NaN, or a value past the nanosecond range is what `humanize.Seconds` rejects, and the row kept the duration it had before, so the dashboard's average tok/s divided a whole run's tokens by a time that run never took. The row is cleared before the new duration is read.
- The failure list sorts stably, in the report and in `Stats.Failures`. A review that failed in two loops is two rows, each carrying its own branch, exit code, and line count, and the rows came out in either order.
- A review body can no longer reopen the fence around it. The wrapper rewrites a body-supplied marker to its `(text)` form in one pass, and that form ends in the same `---` the marker does, so a body whose markers shared their dashes re-formed one at the seam and the agent read the review as ending mid-body, with the body's own text where the ground rules belong. The rewrite now repeats until no marker is left, which is the invariant the compose fuzzer asserts.
- A reported unknown review name is at most 200 code points. The bound was applied before the ellipsis `Truncate` appends, so a longer name came back at 201, past what the reported bound and its test allow.
- `--usage-limit` stops a lane the way it stops a sequential run. A lane took a review off the queue and launched it without re-reading the flag the probe had just set, so up to `--jobs` further reviews started against a provider window the run had already decided was spent. The lane now drops the rest of its queue once the probe trips, which is what the sequential and stacked loops already did.
- Untracked paths in a lane run's start-of-run note are sanitized, like every other git path the package prints. A name carrying an escape sequence reached the run journal verbatim.
- A pruned run's emptied day directory is now synced in `runs/` and not one level above it. The removals were flushed to the state root, which holds no entry for them, so a power cut could leave the directory back.
- The state root is synced after an index append creates `index.jsonl`, not only after a rebuild renames it. The row of an install's first run was fsync'd into a file whose name the root had not recorded, so a power cut could lose a summary the run had reported writing. The create is proved with `O_EXCL` and reported, so only the call that made the file syncs the directory.
- A consumed hot-reload handoff is unlinked durably, and a failure to drop it is reported. The removal's error was discarded and the directory never synced, so a power cut could bring the handoff back and resume a run that had already finished, counting the reviews it had already done. A handoff another process dropped first is not a failure.
- A directory lock under `--jobs N` names every review still running, not one per name. A repeated review means weight, so two instances of it run at once in two lanes, each on its own branch, and the first one to end removed the other's entry: the note read `idle` while a review was still going, which is what the instance turned away from that directory (exit 75) is told. The key is the loop, the review, and the branch, and two lanes on one review with one agent print it once.
- An external command the deadline killed is reported as a timeout. `git`, `gh`, the usage probe, and the `dsh` config dump each reported `signal: killed` or `exit status 128`, which no operator can tell apart from a genuine failure, and a hung remote is the commonest cause. The context's own error is wrapped in and the child's kept, so `errors.Is(err, context.DeadlineExceeded)` classifies a timeout for a caller that wants to.
- A failed commit step and a failed review suggestion now say why the agent stopped. Both captured its output and used only the success branch, so the error carried a label and an exit number while the reason (quota spent, credentials rejected, model name unknown) went only to the run journal. The agent's last line is appended to both, and the commit step keeps a tail of the output even for a caller that passes no output sink, which is every caller that redirects or runs the TUI.
- A cancelled project-prompt walk no longer returns a partial review set. The walk stopped at the cancel and the set built from what it had reached looked exactly like a complete one, so the run behind it silently skipped the reviews it never found. `Discover` names the walk and the failure instead.
- A per-review checkout that could not be narrowed to its owner is a failure, not a best effort. The `chmod` was discarded on the path whose whole purpose is that a private repository's second copy stays unreadable to other local accounts, and nothing in the run said the copy was left at 0755. The checkout is removed and the add fails with the path and the cause.

### Security

- The per-repository git overlay that blanks `filter.*`, `merge.*`, and `diff.*` drivers from a reviewed repository's own `.git/config` is rebuilt when that file changes instead of being computed once per run. A review runs with its permissions bypassed and can write `.git/config` itself, so a driver planted after the first git command was executed by the next checkout or merge with nothing blanking it. The check is one lstat per git call, against the two subprocesses a rebuild costs, and the sub-repository and worktree handles inherit both the overlay and the check that retires it.

## 1.24.0

### Added

- The repository's YAML is linted: `make check-scripts` and the `scripts` CI job run `yamllint --strict` on `.github`, with the rule set in `.yamllint` and the version pinned beside the ruff and mypy pins. A malformed workflow is a syntax error nothing else in the tree can see, and a warning now fails instead of scrolling past.
- ruff's `TRY` rules on `scripts/`: an exception that re-raises a bare name, a `try` whose body is `pass`, or an `except` that swallows an error without a name is a defect class the tree was never checked for, and it already passes.
- ruff's `CPY` and `FAST` rules on `scripts/`. The notice regex names the same two-line header every `.go` file opens with, so a script now carries the repository's copyright and SPDX lines instead of none, and `pyproject.toml` states why `COM`, `T20`, `NPY`, and `AIR` stay out of the selection.
- The four throwaway captures `.scratch_a.txt`, `.scratch_b.txt`, `.scratch_doc.txt`, and `.scratch_flags.txt` are out of the repository. They are the same class as `.scratch_refs.txt`, removed in 1.7.2: sorted name and flag listings written to the project root, which holds config, manifests, and top-level docs.

- `--token-budget N` stops a run once its agents have reported N tokens in total, so a stalled agent that spends a whole timeout's tokens in seconds is bounded by what it costs as well as by how long it takes. Zero is unlimited, the default.
- `gauntlet doctor` names the state root in use and whether it came from `GAUNTLET_HOME` or `$HOME`, printed before the verdict so a box with no agent CLI still shows it.
- `--keep-runs N` bounds the run history: at the end of a run, journals and index rows past the newest `N` (200 by default, 0 keeps all) are deleted, and day directories left empty are removed. Nothing deleted the state tree before, so it kept one file per run for the life of the install.
- `make host-artifact` prints the path `make dist` uses for the binary built for the current host, so the release smoke tests resolve the asset name from one place instead of restating it in each workflow.
- `make build`, `make check`, and the test targets preflight the Go minimum in `go.mod` and name it when the local toolchain is older. The Makefile pins `GOTOOLCHAIN=local`, so a Go below that line failed inside the go command with a message about a knob the caller never set, and the file stating the requirement was never pointed at. The minimum, not the release pin, is what the dev loop checks.
- `GIT_SSH_COMMAND` is part of the documented environment: `gauntlet help` lists it, `docs/CLI.md` gives it a row, and the value is taken only when it is set to something other than empty or whitespace.
- `gauntlet help` lists `NO_MOTION` and `REDUCED_MOTION` beside `GAUNTLET_NO_ANIMATION`. Both have always frozen the dashboard's animated glyph and both are named in `docs/CLI.md` and `.env.example`; only the help screen omitted them, because the check tying every variable the binary reads to the help screen followed string literals only and read almost nothing.
- `gauntlet doctor` prints the documented environment variables this process saw, with a variable set to empty shown as `(empty)` so it reads differently from one left unset. `GITHUB_TOKEN` and `GH_TOKEN` are shown as `(set)` and never as values, so a pasted transcript cannot leak one. Nothing showed this before, and a knob set to the wrong value or set to empty was only discoverable by reading the source.
- `gauntlet doctor` says when the state root is not a directory or cannot be written to, and exits 1 for it. A `GAUNTLET_HOME` that cannot hold a journal previously surfaced only as one warning line in the middle of a run. A root that does not exist yet is left alone: the first run creates it.

### Security

- A subject read back out of history during a stacked-PR recovery pass is sanitized before it becomes a pull request title and a journal line. The commit holding it was made by an agent that committed on its own, by a conflict resolution, or by an operator, so its subject can carry the escape sequence or bidi override no `SUBJECT:` line was allowed to, and nothing filtered it on the way out.
- Every subject now passes one clipping helper that sanitizes as well as clips, so a source added later inherits the guarantee instead of having to remember it.
- `--semcode` runs the indexer's output through the same display filter as every other child process, on both streams. It reports the file names it walked, so a reviewed repository could otherwise hand a file name carrying an escape sequence or a bidi override to the operator's terminal through the one child whose output nothing filtered.
- The documented install script verifies the downloaded release binary against the release's `checksums.txt` before making it executable, and aborts on a mismatch.
- The install script downloads the release asset under the name `checksums.txt` lists, so the checksum it verifies is the checksum of the bytes it installs, and a failed verification now aborts the install instead of continuing to `chmod`.
- The run lock is created `0o600` and an existing one is tightened to match, so the run id, review, and agent CLI it records are not readable by every local account.
- Appending to `.git/info/exclude` refuses a symlinked `info` directory, so a repository that plants one no longer redirects the write outside its own tree.
- `--merge-into` is checked with `check-ref-format` before it reaches `git worktree add`, which takes no `--` and would otherwise read a leading dash as an option.
- A remote URL with a leading-dash host (`https://--json/owner/repo`) is refused when parsed, instead of reaching `gh repo view` as a bare positional.
- The documented install script refuses to install when `checksums.txt` lists no entry for the asset, naming the asset, instead of handing the verifier an empty file whose verdict depends on the local coreutils or Perl `shasum`.
- A negative `--token-budget` is refused like every other count flag, instead of being read as unlimited because the budget is only enforced above zero.

### Changed

- `prompt-review` names where an existing review of a subject is looked for instead of pointing at "the sibling reviews above", a list the prompt never prints, and says the new-file format binds the file being created rather than reading as a bar on editing the prompts already in the tree. Its dispatchability bullet now names the runner's name list: a curated set may omit a prompt and still dispatch it by name, so set membership was never the defect. The two lines that both said to leave well-constructed prompts alone are one.
- `prompt-review` no longer tells an agent to create a review of the repository's own rule files, docs, and ADRs: that ground is already owned by `agentrules-review`, `doc-review`, and `specs-review`, and the bullet licensed a near-duplicate. The rule now names candidate subjects, requires checking the sibling reviews for one that owns the ground first, and says to extend that prompt instead. Its evidence line names the review listing as the `--list` flag, which is what the runner ships; there is no `list` subcommand.
- Release artifacts are built with one exact Go release, pinned as `GO_VERSION` in the Makefile and installed by every CI job, instead of whatever 1.27.x the runner had. A Go binary records the compiler that built it, and the `go` line in `go.mod` is a language minimum, so two releases of the same source could differ in their bytes alone. `make dist` and `make repro` refuse a toolchain the pin does not name; `make build`, `make test`, and `make check` keep running on whatever a contributor has.
- One seed per run, resolved once where the run's start instant is read and carried across a hot reload. The suggest step's agent order and the schedule's shuffles drew from separately derived numbers, so a run started without `--seed` printed a seed that replayed the schedule but not which agent was asked first, and a reload resumed the remaining reviews under a seed the journal had never recorded. An explicit `--seed` behaves as before.
- `time-review`, `unicode-review`, and `dst-review` name the evidence an agent should run instead of only the defect to look for: the zone database (`zdump`, `date` under a zone) settles whether a wall-clock time exists or repeats, ICU's `uconv` and the language's own normalization settle whether two spellings are the same string, and a seeded harness run twice with one seed settles determinism. The three were the only prompts demanding a concrete failing input with no way to produce one.
- `gauntlet pick` asks for a second `q` before it discards a composed run, and says so on the status line and in the key legend while the first press is waiting. It arms the key the dashboard already arms, for the same reason: a slip of the finger used to throw away a screenful of picking. Any other key, or `esc`, takes it back.
- `+` in the launcher stops at the machine's cpu count, the ceiling `space` already applied to the same row. Past it the concurrency meter reads full and the extra lane has nothing to run on, so the two keys now stop in the same place.
- The dashboard's key legend and help no longer offer the graceful finish (`s`, `ctrl+c`) to a run that has no finish to ask for, instead of listing a key that does nothing.
- The dashboard's small-terminal fallback no longer offers `space`. It draws no feed, so pausing one only turned the state label into a statement about something the reader cannot see; a feed already paused still says so.
- `--tui` is refused unless stdin and stdout are both terminals, the same gate `pick` uses. A redirected stdin hands the dashboard end-of-file, which quit the run on the first tick and killed the reviews in flight with nothing said about it.
- A `--log` file whose parent directory is missing, or is not a directory, is a usage error at parse time like every other `--log` mistake, instead of failing after the run had started with a bare syscall message.
- A finished run writes its index row without reading and decoding the whole index first. The duplicate check walks the file a line at a time and only decodes a row that already spells the run id, so the cost of finishing a run no longer grows with the number of runs the install has recorded, and neither does its memory use.
- `prompt-review` looks for prompts in the three places a loader can supply them (the project tree, an operator's prompt directory, a runner's compiled set) instead of skipping a repository that keeps no prompt file of its own, tells the agent to register a prompt it creates where the loader looks for it, and no longer asks it to add a missing data-not-instructions line to a prompt the runner already composes one into.
- A review whose agent command would not build goes straight to the fallback agent instead of spending its retries on an argv that would fail the same way every time.
- A prompt file in the reviewed tree no longer gets a description out of a goal line it did not write. Sanitizing a line deletes the control characters out of it, so a line reading `Yo<NUL>ur goal is to ...` was repaired into one that matched the goal prefix, and the text after it became the review's description in the picker, `--list`, and the README grid. The prefix is matched on the line as written and the value is sanitized once it is extracted, which is what the `Summary:` line already did.
- A review's short subject is bounded whether it declares a `Summary:` line or falls back to its goal line, so a project prompt with only the longer goal line was the one entry in a one-line-per-review list that never fit.
- `make repro` proves byte-identical output for every platform in `PLATFORMS`, not only the host's, and `make release` runs `make check` so a tag cannot publish a tree that fails `gofmt` or `go vet` under any of the three shipped tag sets.
- `make dist` cross-compiles the four release platforms at once instead of one after another, then reads each finished binary back with `go version -m` and refuses to leave it in `dist/` unless the platform recorded inside it is the one its name claims. The release smoke test only ever ran the host's binary, so a mislabeled asset used to ship under a correct-looking name.
- `make repro` builds the two copies of a platform at the same time, halving the target's wall time without changing what it compares.
- The release workflow pins `LC_ALL=C` and `TZ=UTC` for the whole job, so the asset order of a published release follows the repository's collation rule rather than the runner's.
- A run listing shows `n/a` instead of `+0/-0` when the run's line counts could not be attributed, so a run with no counts no longer reads like a run that changed nothing.
- A retry handed to a different agent continues the attempt sequence instead of restarting it, and every attempt now publishes a `review_start` carrying an `attempt` number (1 is the first try) while the attempt that decides the review publishes the matching `review_end`, so an end says which launch closed it and a retried review no longer reads as one that restarted. An attempt superseded by a retry has a start and no end; only the end counts the review. `docs/RUNS.md` documents the field for anyone reading the stream with `jq`.
- Report a run's reviews in review-name order rather than the order parallel lanes happened to finish in, so a replayed seed prints the same result and pull-request list every time.
- `--jobs N` splits the loop's schedule across the lanes when it is built (review `i` runs in lane `i%N`) instead of handing the next review to whichever lane frees up first, so a review's lane, and with it its branch name and worktree path, is a function of the seed rather than of the OS scheduler. A lane with one slow review no longer takes the tail of another lane's list, so a loop ends when the slowest lane's last review does.
- Print the effective seed on the first line of a headless run, so a run started without `--seed` reports the seed that replays it.
- The command reference, the design map, and the token telemetry page say what the code does: the dashboard stops on `q` and `esc` only cancels an armed quit, `gauntlet pick` also takes `--target-dirs`, a defined agent's `usage.cumulative` and `usage.header_cwd` are documented, a clock-derived `--seed` is not replayable, `--max-loops` defaults to 1 only under `--stacked-prs`, an agent with no transcript adapter still gets a rate from the stream, and `gitx.DeleteBranch` is documented as the force-delete it is. The help screen's review count matches the 53 bundled prompts, and it now says the same about `--max-loops` as the command reference.
- The design map's file-signal suggester names the marker's shape rather than seven of them, so the list it prints matches the table the code searches.
- The threat model's pointer at the `make release` target named a line the Makefile had long since moved off, so it sent a reader into the middle of the scripts lint. It points at the target now, and a test in `cmd/gauntlet` fails the next time an edit slides a numbered reference out from under the sentence that names it.
- `make test` and `make test-pkg` fail when a `RUN=` pattern selects no test, naming the pattern and the command that lists the real names. `go test` exits 0 there, so a mistyped test name used to report a pass and the edit-test loop lost an iteration over it.
- `make test-pkg` refuses to run without `PKG=`, naming the invocation that works. It fell back to `./...` and ran the whole suite under a target named for one package, so the fast loop was the slow one.
- `make test`, `make cover`, `make test-pkg`, and `make repro` ignore an exported `TMPDIR` or `REPRO_DIR` from the environment and use `~/.cache/gauntlet` as documented, so a shell that exports the tmpfs those rules exist to avoid cannot move the test scratch space, and `make repro` cannot be pointed at an arbitrary path to delete. Override them on the make command line.
- Exported signatures under `internal/` changed, which is a Changed entry rather than a major bump because nothing outside this module can import them: `gitx.DeleteBranch` and `gitx.DeleteBranchesMatching` return the failure instead of swallowing it, `prompt.ParseSuggestions` returns a third value counting the names it had to drop, `gitx.StackBranchPrefix`, `gitx.StackProvisionalBranch`, and `gitx.StackFinalBranch` are gone, and `gitx.Repo` gained a `Now` clock field. Anyone vendoring the tree updates these call sites.
- `make repro` gives each copy of the tree its own build cache, so the second build compiles from source rather than reusing the first build's objects, which `-trimpath` had made interchangeable. A build that embedded its own directory would have compared equal to itself, and the target exists to catch exactly that. It also leaves `.gauntlet/` out of the archive, so a run of the tool in this checkout no longer copies its lane worktrees twice.
- Dependabot groups the weekly module bumps into one pull request, leaving a major version in its own so a version adopted through a dedicated migration pass is never bundled with a patch bump.

### Fixed

- The documentation says what the code does. `docs/RUNS.md` told a reader to press `esc` twice for the hard stop, which cancels an armed `q` and otherwise scrolls, and told a `--jobs` lane to start the next review off a shared queue, which is the model the runner replaced when it split the schedule across the lanes up front; `docs/DESIGN.md` still drew a lane taking whichever one was free. `docs/TOKEN_TELEMETRY.md` said `Discover` lists processes on Linux and nowhere else, where the pinned module walks `/proc` on Linux and asks `ps` and `lsof` on macOS.
- A discarded stacked-PR layer reported success while its branch stayed. `DiscardCurrent` cleared the branch name and dropped the `git branch -D` result, so a deletion git refused left a leftover no later sweep names, and the caller went on to publish the next layer. The failure is returned, and the name is still cleared so the next child can take it.
- The git exclude that keeps `.gauntlet/worktrees` and the lock file out of a reviewed repository's status reported nothing. A failed write (and a short one, which the next run's substring check would not match, so the entry was appended again every run) is now reported and logged; the run continues, since nothing else depends on it.
- The index lock no longer waits forever. A cross-process `LOCK_EX` parked `gauntlet runs`, `gauntlet history`, and the exit-time prune behind a peer rebuilding the index, with no way out. The acquisition retries `LOCK_NB` for 30 seconds and then fails naming the lock file.
- A file whose read failed or returned nothing is no longer scanned for declaration markers. The partial head a short read leaves was searched as if it were the whole file, marking a signal undeclared or declared on the strength of bytes this pass never finished reading.
- `--log` validation keeps the underlying error, so the errno on a log path that cannot be written stays inspectable with `errors.Is`.
- `gauntlet pick` fills the terminal. The panes are sized by what they hold, so a tall window left the composed command, the status line, and the key legend under a short stack of boxes with the rest of the screen blank; the gap now goes above them and the keys sit on the last row at every size, as the dashboard's footer already did. The panes also get the two rows the height budget had been spending on chrome that is not there, which is what a machine with several agent CLIs needs to show more than one of them.
- The small-terminal fallbacks keep the key line. Both were trimmed from the bottom, so a window three rows tall showed a tally and no way to leave; the launcher's short view also dropped the command it composes, which is the one thing it exists to show. The dashboard's first frame says it is starting rather than painting a blank alternate screen.
- `make repro` keeps a developer's `.env` out of the tree copies it builds from. The archive is the whole working tree, so `.env` (the copy of `.env.example` holding real tokens) and `.gauntlet.lock` were extracted into both copies, where only one had them. The exclude list is now every entry in `.gitignore`, which is checked against it, so a new ignored build output cannot become an input to one copy and not the other by leaving off an exclude. The stale `gauntlet-go` entry is out of `.gitignore` (no target builds it) and `.scratch_*` joins the excludes.
- `--suggest-agent gauntlet` says so when the run history cannot be read. The file-signal suggester drops an unreadable journal silently, which leaves every review at its neutral weight and quietly re-proposes the ones that have already finished here several times without changing a line. The picks stand on the file evidence alone, so the warning names the journal instead of turning into an error.
- A commit subject an agent printed is held to the same 72 runes as a generated one, and a subject that credits a model or an agent CLI (a `Co-Authored-By`/`Generated-By` line a CLI injects, a "generated with" phrase, the robot emoji) is dropped for the subject the files earn. The agent's line reached history, a pull request title, and a merge message verbatim at up to 100 runes, and the trailer sweep only ever cleaned the message's trailers, so a credit line in the subject itself survived it. A subject that merely names one of those tools, which in a repository that integrates them is a true description of the change, is untouched.
- The conflict step's marker scan covers every path its commit would contain, not only the files git reported as conflicted. The resolver runs with the whole scratch checkout open and `git add -A` stages all of it, so a marker it left in any other file landed in the merge unread. A file the scan cannot read whole because it is past 8 MiB counts as unresolved rather than clean, and reading one no longer pulls it into memory.
- `TERM` is read the way every other documented environment value is read. It was compared exactly against `dumb`, so `TERM=DUMB` or a value a wrapper padded with a space kept a palette the terminal cannot show, while `CLICOLOR_FORCE` and `FORCE_COLOR` in the same function were trimmed and case-folded. The list of values that mean off now lives in one place, `internal/envx`, which the reporter and the dashboard both read, so the rule `docs/CLI.md` states once cannot hold differently in the two.
- A process started with no `PATH` (launchd, systemd, `env -i`) found agent CLIs through the Linux default alone, so on Apple Silicon every Homebrew install was invisible and `gauntlet doctor` reported no agent on a fully stocked Mac. The fallback now lists both platforms' absolute prefixes, starting with `$HOME/.local/bin` where `make install` and the README put the binary, and drops the directories the host does not have.
- The pending-line bound in the display writer finds a character boundary in a line that is not mostly ASCII. The walk it used stepped back over continuation bytes, but the byte before a boundary is a continuation byte, so in a line of nothing but multibyte characters it passed every boundary it should have stopped at and cut the front of the line, repairing the tail to a replacement character on screen. It now cuts at the nearest rune start at or below the cap, so the split character stays held and is emitted whole once the rest of it arrives.
- The display writer keeps blank lines. It dropped the terminator of an empty line along with the line, so a blank line a child process wrote never reached the terminal, which is a rewrite of the child's output rather than a filter.
- A `*-review.md` whose file name is not text (a filesystem name holding bytes that are not valid UTF-8) is ignored with a warning, the way a name carrying a control character already was: the name is identity, and JSON rewrote it to U+FFFD in the run journal, so the run named a review that `--reviews` could not ask for and that no later command could name again.
- Text that is not valid UTF-8 is repaired the same way whether or not a control character sits beside it, instead of being passed through untouched unless the string also held something to strip.
- The pending-line bound in the display writer cuts at a character boundary: a line that had grown past the cap with a multibyte character starting just before it was emitted with that character split, and the repair above turned the fragment into a replacement character on screen.
- `--tui --log FILE` writes the log file through one lock, so the file reporter, the run-control messages, and both signal handlers can no longer split one line between two of them. The lock belonged to the console stream, which only the console stream used while the dashboard owned the screen.
- A review canceled while it waits to retry is recorded as interrupted rather than failed, the way every other cancellation already was, so a run ended by Ctrl+C no longer reports the review that was in flight as one the repository did not pass.
- The triage step keeps and prints a bounded number of the review names an agent proposed that no catalog holds, and each name at a bounded length, so an agent that answered with a screenful of `RELEVANT:` lines naming nothing no longer leaves a run holding every one of them and logging them without end. The log line says how many it left out.
- The launcher's key legend keeps the keys its pane acts on at a hundred columns: it tightens the gap between segments and shortens the arrow keys' action before dropping anything, where the `a` key fell off the end and a pane that offers it showed no way to select all.
- The launcher's catch-all group names its members instead of the group heading, so a tree carrying a review no bundled set claims composes a command line the parser accepts.
- A flag the parser rejects is named the way the help screen names it in the suggestion too: an unknown `--tuii` reads `flag provided but not defined: --tuii (did you mean --tui?)`.
- `gauntlet runs --limit 100000000` reserves a bounded hint instead of one slot per requested row. `--limit` has no upper bound and a summary is a few hundred bytes, so listing a short index asked for gigabytes up front; the slice still grows into every row the index holds.
- The journaled event names are pinned by a test, so renaming or dropping one fails the suite instead of breaking a consumer's `jq` query after the release. `docs/RUNS.md` names the `log` event it had described only as "runner log lines", and the journal writer asks the runner which kinds are droppable rather than restating the list. The kinds on the bus but not in the journal (`output`, `usage`) are not part of the contract and are not pinned.
- `gauntlet doctor` reports a per-review helper with alternative binaries as present when any of them is installed, and names it by its primary, the way the prompt does; entries like `ast-grep|sg` were compared against a probe keyed per binary and so never matched.
- An agent defined with a capital letter in its name is selectable under any spelling, the way a built-in is; two definitions differing only by case are refused rather than leaving one of them unreachable.
- `--raw` output is width-capped like every other line headed for a terminal, so one overlong line no longer arrives as a single uncapped frame.
- The dashboard footer keeps the run's diff, token, and budget readings on a terminal too narrow to hold them beside the key legend: it drops whole readings from the right end and marks the ones that cannot fit, where it used to drop all of them with no sign they had been there.
- The dashboard's agent panel drops a whole counter column on a narrow terminal instead of cutting one at the pane edge, so a token total is no longer drawn truncated (`◌ 11` standing in for `11,111,110`) and a counter is never clipped mid-label.
- A flag the parser rejects is named the way the help screen names it: `--timeout`, not `-timeout`. An unknown flag, a flag missing its value, and a value it cannot parse all say so, while a shorthand keeps its one dash.
- A run driven by the dashboard exits with the code its reviews earned instead of always exiting 130, which the dashboard's own teardown caused by cancelling the run context. A run that finished and was then closed with `q` exits 0 and journals 0, as the same run does without `--tui`; a run stopped mid-review still exits 130.
- A run journal rebuilt after a crash no longer records a zero exit code for a run that never closed; `exit_code` is absent until a clean `Close` records it.
- A review that ends with a status this build does not recognize reconciles into a new `other` index bucket, so every review a rebuilt summary counts lands in exactly one bucket and the FAILED column keeps explaining the exit code.
- The usage-limit probe runs once per review a lane actually starts, and not at all on the way out, so a cancelled or finished loop no longer waits out concurrent probes.
- Report a review or lane branch that could not be deleted instead of leaving it stranded: `gitx.DeleteBranch` and `gitx.DeleteBranchesMatching` return the failure, and every runner call site logs it. A sweep that cannot list its own pattern now reports that too, rather than reading as a sweep that found nothing to delete.
- Fsync a run journal when it closes, its shard directory when the file is created, the index directory after a rebuild renames it into place, and the reload handoff directory after saving it, so a power cut cannot leave a listing, a run, or a hot-reload handoff without the file it names.
- Drive the line-sample debounce from the run's injected clock instead of wall time, so a replayed run attributes the same lines to the same review.
- Dashboard footer no longer keeps a live token rate from a lane whose review already started or finished.
- The run index holds one row per run even when the run is listed before it closes: a listing that reconstructs the row from a journal still in flight no longer leaves a second row behind when the run's own close lands, and that row is the close's, with its args and exit code.
- The documented install snippet builds the release base URL with a trailing slash, so the tag it resolves is the tag both fetches come from.
- A `git status` rename whose source name contains ` -> ` reports its real destination path instead of a path that names no file on disk.
- Two dsh overlay pins whose provider or model differ only in case get separate overlay files, so on a case-insensitive volume (macOS by default) a run pinning `gpt-5` no longer picks up the overlay another spec wrote for `GPT-5`.
- A `--prompt-dir` that is also a project directory is excluded from project discovery on a case-insensitive volume, so it is no longer re-registered as a project prompt overriding the bundled one.
- The dashboard's end-of-run marker follows the events the bus had already queued, so the summary screen no longer freezes one sample short of the run it is reporting.
- A signal line is never spliced into the middle of an agent's output line in a `--log` file: the plain reporter and both signal handlers now write through one lock, so a multi-file write stays a single unit of work.
- The launcher's small-terminal view names the filter key and drops whole keys instead of cutting one in half. Without the panels the composed command is the whole screen, and `/` is the only key that can still change it, but the key line did not carry it and clipped `/ filter` to `/ fi` on a narrow terminal. A one-row terminal now keeps the key line alone, the way the dashboard's fallback does, instead of writing two rows onto one.
- The dashboard's narrowed feed is labelled with what it actually keeps, results, errors, and diffs, as the README already called it. Diffs survive the filter, so a title reading "results and errors" over hunks of code taught the reader the label was decoration.
- The launcher's help says what `esc` does at each depth: it cancels a `q`, clears the filter, and leaves once there is nothing left to clear. The help documented it only as the cancel key, so a reader who pressed it to dismiss something found a composed run gone. The pane keys line says `home / end` where it used to promise `g / G` as well, which type as letters while a filter is open.

## 1.23.3

### Security

- Reject symlinks and non-regular files for `--log`, and open log files with `O_NOFOLLOW` to prevent symlink traversal and unintended file permission modification.
- Harden journal and reload state operations by rejecting single-dot run IDs, guarding the journal index lock with `O_NOFOLLOW` and regular-file verification, and checking cross-platform path separators in state and overlay keys.
- Reject embedded userinfo credentials in GitHub pull request URLs during stacked publication validation.
- Guard git rev-parse, log, and diff commands with `--end-of-options` to prevent option injection and arbitrary file write via option-shaped ref arguments, and separate revisions with `--` during hard resets.
- Resolve relative executable paths containing path separators to absolute paths in `runx.LookPath`, preventing unintended binary lookup or execution from working directory changes.
- Use `os.Lstat` and regular-file checks when inspecting prompt files during discovery, refusing symlinks and special files.

### Changed

- Document dashboard completion keys in README, help subcommand and empty flag validation in CLI docs, and correct misplaced or drifted doc comments across runner, gitx, prompt, and runx.

### Fixed

- Clarify cross-prompt fencing boundaries and replace conversational phrasing with actionable verification commands across bundled review instructions.
- Reject `--once` combined with `--max-loops 0` as conflicting loop limit options in the CLI.
- Restrict git status C-style unquoting to valid byte-range octal escapes (000..377) to prevent truncating integer overflow, guard retry backoff calculation against negative shift amounts on negative attempts, and ignore writes to uninitialized or non-positive tail buffers.
- Reset feed scroll to live output before quitting on Esc in the completed dashboard, quit immediately on Ctrl+C when a run has finished, show in-flight reviews in the minimal dashboard view, and step into the first review when expanding an already-open group in the launcher.
- Floor live activity rate marker to teal to clear the text contrast floor on low rates, support delete and ctrl+h in launcher filter input, and add accessible SVG title and desc metadata.
- Avoid counting interrupted reviews as failures in dashboard lane statistics.
- Validate ISO 8601 calendar dates and leap years with standard time parsing when resolving journal run shards, guard thinking glyph animation against uninitialized clocks and backward time steps, and avoid undefined Unix time calculations on zero timestamps in seed derivation.

- Measure footer key width in terminal cells in the minimal view, terminate ANSI CSI escape tokens on standard final characters in token width calculation, and normalize lock notes, prompt summaries, and suggestion reasons to NFC before truncation.
- Fall back to default ssh when `GIT_SSH_COMMAND` is empty or whitespace, avoid parsing custom agent definitions for non-agent subcommands, and document motion reduction environment variables in example configuration.
- Reject empty arguments and single dashes as unexpected positional arguments instead of silently swallowing them or misreporting them as unknown commands during subcommand peeling in the CLI.
- Avoid re-publishing pull request events and double-counting line changes when resuming stacked pull requests, make branch renames idempotent when target matches source, and guard review history changed counts against duplicate merge and pull request events.
- Skip review confirmation prompts during `--list` and `--dry-run` planning, parse stream JSON role and type values case-insensitively, ignore empty queries and candidates in fuzzy matching, and remove nonexistent review names from heuristic rules.
- Guard against integer overflow and truncation in backoff jitter and token reasoning percentages, and protect dashboard meters, rate formatters, and elapsed durations against NaN and unrepresentable floats.
- Guard dashboard chart rendering and note truncation against non-positive bounds, and handle nil receivers in usage tails and release metadata.
- Bound narrow text trimming and directory labels in the dashboard to non-negative widths, avoid splitting multibyte arguments in attached flag values, and normalize stacked pull request overview notes to NFC.
- Correct the review count across the landing page and design docs, and document the dashboard Enter-to-close and paging keys.
- Clamp reload and review elapsed durations to non-negative values, guard git sample caching against negative intervals, and fall back to reconstructed start time when resume origin is zero or in the future.
- Prevent subprocess cancellation in `runx.Guard` from signaling process group 0 when process PID is non-positive.
- Avoid misclassifying canceled reviews as failures and retrying them when cancellation races with process exit in the runner.
- Preserve index append errors alongside prior write errors during journal close instead of discarding them.
- Avoid parsing trailing uninitialized bytes on short index reads in the journal.
- Surface remote error messages from GitHub API responses when release or asset checks fail in self-update.
- Validate placeholder usage and argument conflicts across custom agent definitions, and degrade `GAUNTLET_HOME` directory resolution safely when pointing to a non-directory file.
- Respect standard `NO_MOTION` and `REDUCED_MOTION` environment variables in the dashboard, and announce disabled states for inactive controls in the launcher help view.
- Clarify launcher agent panel and options hints, keep filter prompt visible during zero-match search, and display paused status in the dashboard feed panel title.
- Avoid treating closed signal channels as active signals or force-kill triggers in signal handling, and access stacked-PR runner state through synchronized methods.
- Bound and reclaim subprocess process groups across dsh probing, GitHub CLI calls, and indexing, and drain HTTP response bodies on self-update error paths.
- Correct stacked pull request body documentation and example in `docs/RUNS.md`, document launcher navigation keys in `docs/CLI.md`, and fix misplaced type docstrings.
- Reclaim git subprocess process groups on command exit, guard repository operations against nil instances, and default pull request validation to GitHub when host is unspecified.

## 1.23.2

### Fixed

- Accept release asset downloads from the GitHub release CDN host, fixing self-update checksum fetches redirected there.

## 1.23.1

### Fixed

- Speed up live feed classification by skipping the error-pattern match on lines without error trigrams.

## 1.23.0

### Added

- Support Enter key to close the live dashboard once a run has finished, and support g and G keys for first and last row navigation in the launcher.
- Support Tab and Shift-Tab pane switching, Ctrl-W word deletion, and Ctrl-U line clearing while typing in the launcher review filter (`gauntlet pick`), and automatically focus the first matching review on Enter.
- Support Page Up, Page Down, and Space keys in the live dashboard feed view and help overlay.
- Support Page Up and Page Down keys (`pgup`, `pgdown`) in the interactive launcher (`gauntlet pick`) across reviews, agents, options, and filter search.
- Ship template configuration files `agents.example.json` and `.env.example` with documented options and placeholder values.

### Security

- Require reload handoff state file paths via `GAUNTLET_STATE` to be absolute, preventing relative path resolution and deletion in the working tree.
- Validate provider and model identifiers in dsh configuration overlays, preventing YAML injection and directory traversal.
- Validate reload handoff state files before reading or removing, refusing non-regular files and symlinks via `GAUNTLET_STATE`.
- Reject oversized responses in self-update checksum downloads instead of silently truncating.
- Separate git branch names and patterns with `--` across merge, rename, and branch deletion operations.
- Isolate `--usage-cmd` process execution and PATH resolution from the reviewed
  working tree, running the probe in the system temporary directory with
  cwd-relative PATH entries dropped.
- Constrain self-update asset downloads to HTTPS endpoints on authorized GitHub
  release hosts, preventing plaintext transfers or untrusted third-party hosts.
- Validate HTTP redirect target URLs in self-update against authorized release hosts.
- Strip authorization bearer tokens on self-update requests whenever redirected away from GitHub hosts to prevent token leakage.
- Use constant-time comparison for self-update asset checksum verification against timing side-channels.
- Reject unclean and path-traversal state file paths via `GAUNTLET_STATE`.
- Shell-quote git conflict resolution hint commands with POSIX single-quoting to prevent shell injection via untrusted commit subjects.
- Isolate agent, indexer, and dsh probe execution with absolute-only PATH environments and clean working directories to prevent relative binary resolution.

### Fixed

- Reject explicit empty `--agents`, `--bin`, and `--agent-cmd` flags with a usage error rather than silently ignoring them or falling back to auto-detection.
- List scheduled and available reviews across all target directories under `--list` when multiple directories are configured via `--dirs`, and search all target trees for `--show-prompt`.
- Adapt agent lane column widths for narrower terminals (<90 cols) so metrics are not clipped off in the live dashboard.
- Document the Escape reset shortcut (`esc:live`) in the dashboard footer whenever the feed is paused at the live edge.
- Show `:change` instead of `:open/close` for arrow keys in the launcher footer when focused on the options pane.
- Prevent space and arrow keys from modifying inactive options (suggest agent when suggest is off, merge target when commits are off) in the launcher.
- Explain that no agents are installed when viewing the agents pane hint with an empty agent pool.
- Abort git rebase on pull conflicts to avoid leaving repositories in an uncleaned mid-rebase state.
- Fall back to subsequent agents in the pool when command building fails for an agent candidate.
- Preserve error context when resolving binary paths and checking baseline revisions during trailer stripping.
- Strip trailing carriage returns in git status porcelain parsing, worktree cleanup, and UI block padding to prevent path corruption and rendering issues with CRLF line endings.

- Expand tildes and environment variables in custom agent executable paths at launch, and reject unresolvable variables.
- Validate that GAUNTLET_HOME and --prompt-dir name directories and --log names a file at startup.
- Align documented `GIT_SSH_COMMAND` default in `.env.example` with the runtime default (`ssh`).
- Reject mismatched placeholders across custom agent `model`, `effort`, `stream`, and `continue` configurations.
- Do not count opt-in agents launchable only via bunx (`dsh`) as usable auto-detectable CLIs in the doctor report, correctly reporting missing agents and exiting 1.
- Separate revision arguments and branch names with `--` across git worktree operations, diff statistics, trailer stripping, and commit subject extraction to prevent option injection and file name collision ambiguity.
- Validate JSON key types when decoding custom agent definitions (`agents.json`), returning an error on non-string keys instead of panicking on type assertion.
- Handle incomplete octal escape sequences without consuming invalid digits or malforming bytes in git filename unquoting (`unquoteC`).
- Populate line metrics, review status, and subjects when recovering stacked PR layers.
- Serialize stream sink and token usage callbacks during agent execution, retry interrupted lock note updates on EINTR, and synchronize watcher teardown during runner shutdown.
- Normalize custom agent names and definition keys to NFC, rejecting duplicate keys across NFC and NFD spellings and aligning lookup forms.
- Handle non-positive column budgets and 1-column cuts in terminal cell trimming, reserving width for the ellipsis and returning empty strings on non-positive bounds.
- Use canonical review names when expanding review sets and displaying review prompts, preventing unnormalized names from reaching prompt composition.
- Block the launcher (`gauntlet pick`) from starting an unconstrained run when an active review filter matches no reviews, displaying a clear warning.
- Reject unresolvable environment variable references in `GAUNTLET_HOME` at startup and degrade `gauntlethome.Dir` safely instead of resolving unexpanded paths against the working tree.
- Reject empty argument strings in custom agent `model`, `effort`, `stream`, and `continue` configurations, and reject whitespace-only usage suffixes.
- Normalize available review names to NFC in suggestion parsing, matching decomposed names against agent suggestions.
- Normalize pull request body text to NFC before rune truncation, preserving combining characters on decomposed filenames and descriptions.
- Recognize Unicode whitespace when stripping agent output noise, gutters, and trailing spacing in the line normalizer, and in launcher filter word trimming.
- Distinguish complete Unicode replacement characters from incomplete multi-byte sequences at process output chunk boundaries.
- Surface Escape cancel and live-feed reset keys in the dashboard footer and help overlay, and display active filter queries and clear shortcuts in the narrow launcher fallback.
- Prevent Escape from abruptly terminating an active run when quit is armed in the dashboard; Escape now cancels the quit prompt and resets paused or scrolled feeds to live output.
- Make git worktree removal idempotent on already-removed checkouts, and prune git metadata when the checkout directory has already been deleted.
- Clean orphaned worktree directories and prune stale metadata during worktree preparation, ensuring worktree creation and removal converge across interrupted runs.
- Strip UTF-8 byte-order marks (BOM) when loading custom agent definitions (`agents.json`), preventing parse errors on Windows-formatted files.
- Pad clipped lines with trailing spaces in dashboard panel formatting when multi-column wide characters are truncated, preventing misaligned panel borders.
- Count Unicode code points instead of bytes when checking for short-flag misses, preventing single non-ASCII flags from triggering typo suggestions.
- Normalize file paths to NFC when correlating file notes to git changes in stacked PR summaries and commit subjects, matching decomposed macOS filenames with NFC text.
- Strip relative build directory paths from release SBOM inventory headers to match asset filenames, and clean scratch files and stray binaries on make clean.
- Prevent auto-update from repeatedly re-applying the already-installed release tag during an active run.
- Unlock git worktrees before removal during merge cleanup, preventing leftover locked worktrees on failure.
- Format branch listings cleanly when deleting matching review branches.
- Derive journal date shards from the run ID timestamp instead of the local clock so midnight UTC crossings place journals in the matching shard, and validate run IDs on open.
- Synchronize stacked PR head and publication state with the runner mutex, guard worktree branch renaming, and force-kill stalled command groups on drain timeout.
- Check write errors on command output streams across subcommands (`gauntlet doctor`, `gauntlet runs`, `gauntlet show`, and `gauntlet pick`), exiting 1 on failure instead of reporting success.
- Propagate cancellation exit code 130 when the interactive launcher or review planning is interrupted by context cancellation.
- Record loop line changes in parallel worktree mode (`--jobs > 1`) and preserve line metrics on pull request events in stacked-PR mode (`--stacked-prs`) across the journal, history, and dashboard.
- Correct documentation in CLI and runs reference for `--usage-cmd` execution directory, custom agent definition fields and validation rules, and missing `--check` and `--limit` option tables.
- Reject empty string arguments for `--show-prompt`, `--merge-into`, `--pr-base`, `--suggest-agent`, `--usage-cmd`, and `--exclude` at startup instead of silently accepting them.
- Enforce placeholder validation on custom agent definitions (`model` requires `{model}`, `effort` requires `{effort}`, and forbid `{prompt}` in `stream` or `continue`).
- Abort and reset in-progress merges cleanly even when interrupted by a cancelled context, preventing unmerged index state from persisting.
- Preserve the underlying error on retry failure when removing git worktrees.
- Include unmerged-path inspection error context when squashing conflicted branches.
- Warn on snapshot worktree cleanup failures and guard against nil repository references.
- Abort prompt discovery directory traversal early when context is cancelled.
- Parse duration flags with 64-bit integer precision so values above 2^31-1
  nanoseconds parse without overflow on 32-bit platforms.
- Format missing run start timestamps as `n/a` instead of `0001-01-01` in
  `gauntlet runs`.
- Generated commit subjects clip at whole grapheme boundaries within the 72-rune
  limit, preserving combining accents, flags, and emoji sequences.
- Filenames in generated commit subjects strip C1 controls, bidi overrides,
  zero-width spaces, and Unicode line breaks, preventing terminal spoofing.
- Display text sanitization strips Unicode line and paragraph separators (U+2028
  and U+2029), preserving line integrity in output feeds and summaries.
- Distinguish internal prompt read failures from bad review arguments in
  `--show-prompt`, exiting 1 on I/O error instead of 2.
- Distinguish interactive launcher runtime errors from usage errors in
  `gauntlet pick`, exiting 1 on TUI failure instead of 2.
- Exit 1 on directory lock acquisition errors other than existing locks instead
  of reporting them as usage errors.
- Ensure flag-requested help is rendered to stdout when `flag.ErrHelp` is returned
  during parsing.
- Reject empty model specifications when a colon delimiter is provided in agent
  specifications, and reject colons for tools that do not support models.
- Validate custom agent definitions to require exactly one `{prompt}` placeholder
  in `argv`, forbid `{prompt}` inside `model` or `effort` options, and reject
  empty directory entries in `usage.roots`.
- Expand leading `~` and environment variables in `GAUNTLET_HOME`, and treat
  whitespace-only values as unset.
- Recognize boolean false values (`false`, `no`, `off`, `0`) in `GAUNTLET_NO_ANIMATION`,
  `CLICOLOR_FORCE`, and `FORCE_COLOR` instead of treating them as truthy.
- Resolve the defined executable rather than the custom agent name when validating
  and auto-detecting custom agents, allowing custom agents whose names differ from
  their binary to run without "tool not found" errors.
- Correct the installed agent count in `gauntlet doctor` so custom agent binaries,
  override paths, and fallback launchers are counted accurately in the inventory.
- Preserve `--prompt-dir` in the composed command line generated by `gauntlet pick`.

## 1.22.1

### Fixed

- Pass `--add-dir` to `agy` so it knows which directory it is reviewing.
  Without it the model received no workspace context and hallucinated paths
  like `/home/user/repo`, failing every review that tried to list or edit
  files. Especially visible with `--jobs` where the review runs in a
  worktree the CLI has never seen before.

- Kill the agent's process group on the normal exit path, not only on
  timeout and cancel. A grandchild that outlived the agent (a background
  task, a language server) no longer survives as an orphaned process.

- Sweep slash-separated lane branches (`gauntlet/<tag>/lane-*`) on cancel,
  not only dash-separated ones. A cancelled `--jobs` run no longer leaves
  leftover branches behind.

## 1.22.0

### Changed

- Stream parsing retains only text-bearing fields for deferred classification,
  reducing allocations for metadata-heavy output without changing text or usage.

### Fixed

- Empty `SUBJECT:` and `PATH:` fields no longer consume the following output
  line as a commit subject or file note.
- Run-end summaries report the loops the run completed instead of always zero.
- Agent configuration rejects case-variant duplicate fields, including nested
  usage fields, instead of silently letting JSON key order override settings.
- Release concurrency is scoped per tag so unrelated tag pushes cannot cancel
  queued releases. Runs for the same tag remain serialized.
- Launcher help exposes the focused control's full name, state, value, and
  description in scrollable text when terminal panes clip them.
- Stream lines, prompt descriptions, and PR summaries truncate at whole grapheme
  boundaries within their rune budgets, preserving combining accents and flags.
- Changelog validation accepts SemVer prerelease and build-metadata headings,
  so the release checks no longer block release candidates before publication.
  Versions sort by SemVer precedence, and negative version components are refused.
- Narrow launcher panes retain concurrency and selected option values instead
  of hiding them. Panel titles stay within their assigned width so long titles
  cannot push adjacent panels past the terminal edge.
- Deterministic-simulation guidance preserves cryptographic randomness in
  production and confines seeded substitutes to tests or simulation mode.
- Launcher help calls the existing state-summary helper instead of an undefined
  method, restoring compilation.
- Directory lock files keep their inode after release, preventing overlapping
  starts from acquiring separate locks for the same tree. Release clears the
  holder note instead of removing the file.
- Reported subjects and file notes truncate at whole grapheme boundaries within
  their rune limits, preserving combining accents, flags, and variation selectors.
- Release tags with a prerelease suffix are published as GitHub prereleases,
  including when retrying a draft, so stable installs and self-updates do not
  select release candidates. Build metadata alone does not mark a prerelease.
- Run summaries retain interrupted review counts, including summaries rebuilt
  from journal events, instead of dropping that outcome from the status totals.
- Build recipes pin `GOAMD64=v1` and `GOARM64=v8.0`, preventing ambient CPU
  settings from producing binaries that require newer processors.
- Recovered run listings retain the original start time when hot reload or
  multiple directories produce repeated run-start events.
- Persistent review lanes discard staged and unstaged edits when advancing,
  so failed attempts cannot carry tracked changes into the next review.
- Stream parsing no longer treats tool-payload and user-turn text as assistant
  output, so report lines and usage inside a tool result cannot replace the
  run's own subject, file notes, or counters.
- Version output exits with a failure and reports errors on stderr when stdout
  or the `--log` destination cannot be written.
- Output rate limiting starts its first window at the first line, including
  when an injected clock starts near zero time.
- Failed attempts stop retrying or falling back to another agent once the
  runtime budget is exhausted, including when it expires during backoff.
- Seeded review schedules retain their logical loop number after hot reload,
  so subsequent shuffles and `--max-reviews` selections match uninterrupted runs.
- Hot reload counts a sequential loop completed during its final review, so
  the successor does not repeat finished work or exceed `--max-loops`.
- `--show-prompt` exits with a failure and reports output errors on stderr
  when the prompt cannot be written, including partial output.
- Build and test recipes export `GOWORK=off`, so a `go.work` above the
  checkout can no longer add workspace modules or replace directives to the
  build; the dependency set is go.mod's and go.sum's alone.
- Clearing a kept review search in the launcher moves the selection onto the
  first visible review instead of leaving it stranded off the restored list.
- `--log` tightens a pre-existing log file to owner-only permissions before
  writing, instead of leaving permissions from an earlier looser creation.
- `make fmt` handles Go formatter paths containing spaces, matching `make check`.
- Commit steps retain their five-minute timeout when `--timeout 0` leaves
  reviews unlimited, rather than allowing a stalled commit to block the run.
- Custom agent files reject duplicate agent names and configuration keys instead
  of silently replacing earlier values.
- Recovered run listings count completed loops across directories and hot reloads,
  without counting an interrupted loop as finished.
- Git status parsing preserves Unicode whitespace in filenames instead of
  stripping it and reporting a different path.
- The completed dashboard freezes elapsed time, budget consumption, and activity
  history while it remains open for inspection.
- Dashboard throughput sums lanes in stable order so replayed usage events
  produce identical rates, counting repeated configured lanes only once.
- Usage probes kill remaining process-group members on every exit, preventing
  background helpers from accumulating between reviews.
- The help overlays on the dashboard and launcher scroll when the terminal is
  too short for them, so every instruction stays reachable by keyboard, and
  wrap long lines instead of clipping them at the pane edge.
- Suggestion parsing rejects malformed names instead of scheduling a review
  whose name matches only a prefix of the response.
- `runs` exits with a failure and reports output errors on stderr when its
  listing cannot be written, including empty history and partial output.
- Release builds stop when any binary's module inventory cannot be read,
  instead of reporting success with an incomplete `sbom.txt`.
- Database checks use saved query plans instead of connecting to existing
  databases or executing statements through `EXPLAIN ANALYZE`.
- URL credential redaction handles apostrophes and embedded `@` characters
  without mistaking query or fragment text for credentials.
- Token usage parsing ignores fractional and exponential counts instead of
  recording their leading digits as whole-token counts.
- Custom agent files reject a top-level `null` instead of silently starting
  with built-in definitions; use `{}` for an empty configuration.
- The launcher no longer blocks stacked PRs on a dirty checkout because of a
  saved concurrency setting that stack mode ignores. Leaving stack mode restores
  the setting and its clean-tree requirement.
- `make vuln` scans the selected build tags, including the sqlite driver in
  the default CI scan, instead of silently scanning only the untagged build.
- Custom agent files report validation errors in name order, so identical
  definitions produce the same startup diagnostic across runs.
- Timeout and cancellation kill remaining subprocess-group members even when
  the leader exits before children that ignore SIGTERM.
- `show` exits with a failure when replay output cannot be written, rather
  than reporting success for a partial or missing replay.
- Prompt discovery excludes `--prompt-dir` when a symlink gives the same
  directory a different path, including macOS `/var` and `/private/var` aliases.
- Recovering the run index no longer appends a reconstructed row for a run
  that already Closed: when runs close out of start order (a long run still
  going when a short later one finishes), the duplicate row could replace the
  completed summary in `runs` listings, losing args, exit code, and measured
  elapsed.
- In-place retries and agent fallback stop when the starting tree could not
  be snapshotted, preventing repeated writes on top of a failed attempt.
- Releases reject whitespace-only and heading-only changelog sections instead
  of publishing without release notes.
- The launcher preserves all explicitly selected reviews when suggestions are
  enabled, instead of silently running only the suggested subset.
- `make check` analyzes all three shipped build modes even when `TAGS` is
  overridden, keeping local checks aligned with CI.
- `show` preserves exact JSON numbers, including 64-bit RNG seeds, so a
  seed copied from a recorded run replays the original schedule.
- A resumed run whose handoff was written before the wall clock was set
  back no longer resumes in the future with extra runtime.

## 1.21.0

### Changed

- `--stacked-prs` accepts `-n` / `--max-loops`. Default remains one ordered
  pass. An explicit N (or `0` for unlimited) starts each later pass in a
  fresh worktree cut from the previous pass's last published tip, so
  already-applied fixes stay in the tree and a no-op review opens no second
  PR. Loop 1 keeps the historical `review/<NN>-<review>-<topic>` branch
  names; later loops insert the loop number.

## 1.20.1

The changes below were present at tag `v1.20.1` but were left under
Unreleased and later attributed to 1.21.0. The release workflow requires a
matching version heading, so it could not publish binary assets for that
tag. Source installs at `v1.20.1` include these changes, as does 1.21.0.

### Fixed

- The commit step strips `Co-Authored-By` and `Generated-by` attribution
  trailers before the runner pushes the new commit.
- agy print-mode launches request `--output-format stream-json` when
  `--stream` is on and forward the runner's wait bound as `--print-timeout`,
  instead of exiting at the agent's five-minute default during longer reviews.

### Changed

- README dashboard and launcher screenshots now stamp the current release.

## 1.20.0

### Changed

- Share process-group kill, WaitDelay, and capped stdout/stderr across git,
  gh, usage probes, dsh config dumps, and the indexer.
- Parse agent JSON streams by extracting text and usage during decode instead
  of building an intermediate tree.
- Clip catalog descriptions, suggestion reasons, and review summaries with
  the same rune-bounded ellipsis the rest of the binary uses.

## 1.19.0

### Changed

- The prompt review now leaves well-constructed prompts alone in auto-fix
  runs instead of asking for report-only praise.
- Releases are now assembled as drafts before becoming visible, and rerunning
  a release refuses to replace an already-published version's assets or notes.
- Pin the `govulncheck` executable used locally and in CI while continuing to
  scan against the current vulnerability database.
- Dashboard and launcher panels now use square instrument frames instead of
  generic rounded cards.
- The run documentation now includes a quiesced backup and restore drill for
  durable state, including restore verification and explicit RPO/RTO guidance.

### Fixed

- Invalid or unrepresentable elapsed values in the run index now fall back to
  the recorded start and end times instead of displaying a wrapped duration.
- Deduplicate `--dirs` targets that name the same tree through symlinks.
- Preserve launcher run options when stacked PR mode is toggled off again.
- Build and help targets no longer create the test scratch directory; only
  test targets set up and use it.
- Custom agent files are validated completely before any definitions are
  registered, and blank executable names now fail at startup.

### Security

- Self-update authentication is now sent only to GitHub over HTTPS, preventing
  release metadata from forwarding a GitHub token to another host.

## 1.18.0

### Added

- `--max-reviews N` caps how many reviews one loop runs, however large the
  expanded `--reviews`/set schedule is. The cut happens after the seeded
  per-loop shuffle, so `--seed` replays exactly which N ran and different
  loops sample different reviews; a review scheduled twice fills two of the
  N slots when both land inside the cut. With `--stacked-prs` the single
  ordered pass is truncated to its first N entries. `0` (the default) is
  unlimited, and `--dry-run` reports the capped count.
- Three reviews for Kubernetes and GitOps repos: `k8s-review` (manifests,
  cross-resource reference integrity, API deprecations, and kustomize
  structure, components included), `gitops-review` (the Argo CD / Flux
  delivery layer: source pinning, sync and prune posture, ordering and
  health, secrets delivery, environment promotion), and `helm-review`
  (chart authoring: template correctness, the values contract, hooks, CRD
  lifecycle). Each gates on evidence in the tree and reviews
  tool-agnostically when the delivery tool leaves no markers. A new
  `gitops` set schedules them together with `container-review`,
  `infra-review`, `sec-review`, and `dr-review`.
- `--paths LIST` scopes every review to the named files, directories, or
  globs, relative to the reviewed directory (comma-separated, repeatable).
  The agent still works from the whole repository for context; the composed
  review prompt tells it to report findings on and modify only the listed
  paths, so the scope is prompt-enforced, not mechanical. Without the flag,
  prompts are byte-identical to before. Suggest, commit, and conflict
  prompts are unchanged, and an explicit empty `--paths` is refused.
- Stacked-PR bodies open with an overview of what the change is about: the
  `PATH:` lines a review prints are matched against the layer's own commit,
  deduplicated, and joined into one short paragraph under `## Summary`; the
  `## Changes` file list stays bare paths. Notes naming files the commit
  never touched are dropped. The overview is flattened, length-bounded, and
  backtick-neutralized like every other untrusted value in the body, and the
  whole body is now capped as well.

### Changed

- `container-review` and `infra-review` split Kubernetes workload
  ownership more sharply now that `k8s-review` and `helm-review` exist:
  manifest structure, probes, security context, and resource limits stay
  with those reviews; `infra-review` keeps compose, CI/CD, and IaC wiring.
  `container-review` will use `dockle`, `kubeconform`, and `conftest` when
  they are on PATH. `lint-review` names the project linters it should run.
- Stack branches are named `review/<NN>-<review>-<topic>` (e.g.
  `review/03-sec-review-input-validation`) instead of
  `gauntlet/stack/<tip>/<NN>-<review>`: the 1-based layer number keeps merge
  order sortable and the topic is a slug cut from the commit subject. Each
  layer starts under a deterministic provisional name
  (`review/<NN>-<review>-wip-<base>`) and is renamed once its commit exists,
  before the push. A resumed run finds published layers by listing the
  deterministic `review/<NN>-<review>` prefix and verifying candidates by
  commit graph -- a layer must be a one-commit child of the previous layer's
  tip -- so a same-named branch from an older stack is rejected by ancestry;
  when it occupies the topic name, the new layer appends the stack's short
  base commit. The preflight dry-run probe moved to the same `review/`
  namespace, and `review/` branches are no longer offered as merge targets,
  matching `gauntlet/`.

### Fixed

- Streamed runs (`--stream`, the default) lost every commit subject and
  per-file note: the report parsers read the output tail, which held the raw
  JSON event lines, and `SUBJECT:`/`PATH:` sit inside one escaped string
  there, where the parsers' line anchors match nothing. Commits fell back to
  the generated `chore: update <file>` subject every time. The tail now keeps
  each stream event's decoded text, so subjects, per-file notes, and the
  branch topics cut from subjects come from what the agent actually printed.

## 1.17.0

### Added

- `make ci` runs the Go pull-request checks (`make check` then `make test`).
- A missing or empty `index.jsonl` is rebuilt from the run journals, and a
  stale one has every newer unindexed journal appended, so `gauntlet runs`
  and the file-signal suggester still see a run whose process died after
  flushing the journal. `gauntlet show` already read those files.
  Reconstructed rows have no `args` or `exit_code`.
- `GH_TOKEN` is read for release lookups, the same name GitHub CLI uses. It
  wins over `GITHUB_TOKEN` when both are set, and both now authenticate the
  checksum and asset downloads as well as the release listing, so a private
  `--update-repo` can actually install.
- `gauntlet pick` can compose `--stacked-prs` from the run pane. Turning it
  on clears `--commit`, `--push`, and `--merge-into` and pins concurrency at
  1, so the launcher cannot emit a command the parser would refuse.
- `?` on the launcher opens a help overlay, the same key the dashboard uses.
  `q` / `esc` close it; they do not leave the picker.
- A project prompt that contains the opening review marker can no longer
  close the fence: both `BEGIN REVIEW` and `END REVIEW` in the body are
  rewritten, matching what the end marker already did.
- Conflicted paths named in the resolver prompt are fenced, dropped when they
  carry the resolver's output protocol or formatting characters, and capped;
  a conflict with more files than the prompt will name is left for a human
  instead of launching an agent that cannot finish.
- Commit subjects taken from agent output drop bidi overrides and Unicode
  line separators, not only ASCII controls, so a model cannot spoof `git log`
  or forge a commit body.
- Suggestion reasons from the triage agent are rune-capped like catalog
  descriptions, so one overlong line cannot flood the suggest listing.

### Changed

- `--merge-into` refuses to merge when git status cannot be read, the same
  way it already refuses a dirty tree, so a merge event cannot report work
  that never moved.
- A missing or stale run index that cannot be reconstructed is an error
  from `gauntlet runs`, not a listing that silently omits the newest run.
- A stacked-PR reload that cannot verify its pinned base commit fails
  rather than fetching a new tip and splitting the stack.
- Dashboard and launcher wordmark is the path-arrow teal of the mark
  (`#0e96a8` on dark terminals, a darker pull of that hue on light), one
  hue, not Catppuccin teal and not a per-letter gradient. Footer keys are
  body-colored chrome like the launcher's. The budget meter rides the heat
  ramp. Reload status uses the info hue. Panel names stay dim with the
  rest of the chrome.
- Dashboard lanes and the feed drop the `-review` suffix the grid already
  omitted, so a name is spelled the same way on every instrument.
- `f` in the dashboard footer says `widen` while the feed is narrowed, the
  same way `space` says `resume` while paused.
- `home` / `end` jump the dashboard feed the way `g` / `G` do, and jump to
  the first or last row of the focused launcher pane.
- The launcher shows the same "warming up" line as the dashboard until the
  terminal reports its size, instead of a blank screen.
- `--continue-sessions` with `--jobs` above 1 or `--stacked-prs` is a usage
  error. Those modes give each review a fresh worktree, so there is no session
  to resume; the flag used to be accepted and silently ignored. Scripts that
  passed both will see exit 2.
- `q` / `esc` on the live dashboard arms a hard stop instead of killing the
  run on the first press (1.15.0 still documented immediate quit). A second
  press, or `q` after the run has finished or is already draining, closes the
  dashboard and cancels the run. The header shows `q TO STOP` while armed;
  any other key disarms it. `q` on the help overlay still only closes help.
- `gauntlet runs` prints STARTED as local `YYYY-MM-DD HH:MM:SS`. The old
  `MM-DD HH:MM:SS` column had no year and swapped day and month for readers
  used to ISO dates.
- `gauntlet runs` DURATION uses the monotonic elapsed Close records, matching
  the run's Total time. An NTP step or a manual clock set between start and
  end can no longer stretch or shrink the listing. Old index rows without
  `elapsed_s` still use End−Start; a pair that moved backwards prints `n/a`
  instead of `0s`.
- Bundled review prompts: integer-width and abbreviation rules no longer
  rewrite language-idiomatic types and names; post-quantum crypto items are
  note-only; Kubernetes-native checks skip Dockerfile-only trees; docs-vs-code
  disagreements have a single owner.
- Ruff on `scripts/` selects the bugbear, pylint, pyupgrade, and bandit
  groups (and every other category those two files already pass), with a
  100-column cap, instead of the default four error codes. `scripts/shots.sh`
  is gated with shellcheck in the same CI job. Rule selection lives in
  `pyproject.toml`. `make check-scripts` runs the same pinned ruff, mypy, and
  shellcheck steps as that job, so a scripts/ change fails locally.
- Docs for `--jobs N` describe the persistent lane worktrees the runner
  actually uses, not the per-review throwaway checkouts that 1.13.0 replaced.
- Design docs name the git hardening the runner actually applies
  (`core.pager=cat`, `attr.tree`, local driver blanks) and the conflict-step
  cap that leaves an oversized conflict for a human. `--stream` is documented
  as on by default, matching the flag. The landing-page trust model names
  `core.pager=cat` rather than an empty pager.
- Live usage ticks on the event bus are droppable, the same as agent output:
  a slow subscriber no longer stalls the scheduler on reconstructible
  telemetry. Final token counts still ride on `review_end`.
- `gauntlet show` and suggest history look up a generated run id by the date
  it encodes, instead of probing every day directory under the journal.
- `gauntlet runs` parses the index tail from the end, so a long index costs
  the rows shown rather than a split of the whole slice.
- The file-signal suggester reuses one git handle for the tree listing and
  the churn window. Opening a repo no longer runs `rev-parse HEAD` until
  line stats need a baseline. A million-file tree is listed only up to the
  scan cap, so the unused tail does not stay in memory.
- Project prompt discovery asks git for `*-review.md` by name instead of
  walking the tree. Generated and hidden directories are still skipped;
  a directory that is not a repository still walks.
- Worktree line samples run `git diff --shortstat` and `ls-files -o`
  together instead of one after the other.

### Fixed

- A hot-reload handoff that cannot be read or parsed now aborts the successor
  instead of starting a fresh run. The unparseable case was silent, so the new
  process re-ran every finished review under a new run id.
- Lane worktree removal failures and unreadable `HEAD` reads during `--jobs`
  scheduling are logged. Removal errors were discarded, and a failed `HEAD`
  read was indistinguishable from an unchanged tip, so a lane silently kept
  its stale base.
- `gauntlet update` network and JSON decode errors name the URL that failed.
- `--merge-into` no longer treats untracked files as uncommitted work. The
  merge is a scratch checkout of committed work, so a local notes.txt was
  never going to be in it; refusing the merge used to drop a loop's
  committed changes. Tracked dirty files still block, matching `--jobs`.
- A pump that outlives an agent's process no longer publishes output or
  usage after that review has ended. Those events are keyed by agent, so
  they used to land on whatever the same agent started next.
- A trailing `}` or `]` after `agents.json` is refused, matching
  `encoding/json`. `json.Decoder.More` treats those closers as end-of-value,
  so `{}}` used to load as an empty definition set.
- Listing runs recovers the whole tail of journals missing from
  `index.jsonl`, not only the newest, so two runs that died before Close
  both appear. A failed index write is retried on the next Close, and a
  rebuild cannot overwrite a Close that races it.
- `gauntlet runs` and the file-signal suggester list a crashed run that
  sits behind a later Close, not only an unindexed suffix. The index is
  a cache of summaries; the n newest journals are the listing, so a hole
  in that window is filled from the event stream without rewriting Close
  rows.
- Path flags (`--dir`, `--dirs`, `--log`, `--prompt-dir`, `--bin`) refuse an
  environment variable that is unset or empty instead of expanding it to
  nothing. `$MISSING` used to become the current directory (`--dir`), the
  bundled prompts (`--prompt-dir`), or a silently dropped log (`--log`). An
  explicit empty `--prompt-dir` or `--log` is a usage error too, matching
  `--dir`. A leading `~/` with no usable HOME is refused rather than taken
  relative to the working directory.
- The launcher's `a` key and a set header's space bar act on the reviews the
  filter is showing, not the ones it hid. A fruitless filter no longer
  selects the whole catalog.
- Typing a review filter on the launcher replaces the key legend with the
  keys that work there (`enter` keeps it, `esc` clears it). The legend used
  to keep advertising `run` and `cancel`, which those keys do not do until
  the filter is closed.
- A `--usage-limit` probe that prints more than 4 KiB is ignored, the same
  as any other broken probe, instead of filling memory until the timeout.
- One git or `gh` command's captured output is capped (32 MiB and 8 MiB)
  so a hostile tree or a runaway listing cannot grow without bound.
- Closing `--log` reports a write error instead of dropping it.
- The dashboard feed's scroll offset stays inside the retained ring, so a
  long pause cannot claim thousands of lines back after history is trimmed.
- An in-place retry restores the working tree to the snapshot taken before
  the failed attempt, including the user's own uncommitted files, so the
  next try starts from the same files the first one saw. Isolated reviews
  already reset their worktrees. `--continue-sessions` no longer resumes a
  failed attempt's session.
- The untracked-file line-count cache drops files that have vanished or
  left the untracked set, so a long loop that creates then commits files
  does not fill the table with dead keys and re-read every later file.
- Git and GitHub errors that quote a remote URL drop URL userinfo. A remote
  stored as `https://alice:token@host/repo.git` is reported as
  `https://host/repo.git`, so the account name does not reach the terminal
  or the run journal.
- dsh model overlays are keyed uniquely: `foo/bar` and `foo_bar` no longer
  share one `--patch` file, so a later pin cannot launch with an earlier
  pair's provider and model. A deleted overlay is rewritten instead of
  handed to dsh as a missing path.
- The untracked-file line-count table stops admitting new keys at its cap
  instead of wiping the working set, so a tree with more than 4096 new
  files does not re-read every already-counted file on the next sample.
- `--usage-limit NaN` (and `nan`) is a usage error. `flag.Float64Var` accepts
  it, the 0-100 range check cannot see it, and the runner's `pct < limit`
  comparison is then always false, so a run configured with a NaN limit
  stopped before its first review. Non-finite values are refused the same
  way a probe that prints them already is.
- Stream-JSON token counters that are not whole numbers, or that claim more
  than a trillion tokens, are ignored rather than truncated or stored.
  `{"output_tokens": 1.9}` used to record 1, because JSON numbers decode as
  `float64` and `int(1.9)` is 1. A 2^62 counter fitted in `int` and then
  overflowed the run total. Both match what `json.Number` parsing and the
  text-usage cap already required.
- The dashboard reasoning glyph no longer panics when the clock is set
  before 1970. Go's remainder keeps the sign of a negative `UnixNano`, so
  the frame index was -1.
- Unknown commands and flags include a "did you mean" hint, matching unknown
  review and agent names. `--show-prompt` does too.
- `gauntlet --list` and `--show-prompt` no longer require an agent CLI in
  PATH. They only read prompts; `gauntlet doctor` reports which agents are
  installed.
- Global flags (`--help`, `--version`, `--log`, `--no-color`) may precede the
  subcommand, so `gauntlet --no-color doctor` works. `gauntlet show` accepts
  flags before the run id, so `gauntlet show --no-color RUN` and
  `gauntlet show --help` after `--no-color` work.
- `gauntlet show` on an unknown run id points at `gauntlet runs`.
- Stacked PR creation treats head and base as an idempotency key: if
  `gh pr create` times out after GitHub accepted the pull request, or fails
  because it already exists, the existing URL is reused instead of stopping
  the stack or opening a second PR.
- `StartBranch` on a lane or stack worktree converges when the branch already
  exists at its base, the same leftover-empty-branch rule `AddWorktree`
  already follows. A branch that carries commits is still refused.
- A review that never launched (unknown name, unreadable prompt, or a command
  line that could not be built) now publishes `review_end` like every other
  outcome, so the journal, the dashboard, and `gauntlet show` record it. The
  file-signal suggester no longer treats those, or failed, timed-out, and
  interrupted launches, as finished runs that changed nothing.
- `gauntlet pick` no longer treats untracked files as blocking `--jobs`.
  The runner has allowed them since 1.12; the launcher was still using a
  full dirty-tree check and refused a run that would have started.
- Stacked PRs refuse a remote whose path is not OWNER/REPO on both sides.
  `https://github.com/owner/.git` and `https://github.com//repo` used to
  pass the one-slash check and be handed to gh as `owner/` and `/repo`.
- The README install snippet fetches the binary for the version it just
  resolved, rather than `releases/latest/download`, so a release published
  between the two curls cannot pair a new tag with the previous binary.
- The README install snippet and `make install` say when `~/.local/bin` is
  not on PATH. On macOS it is not there by default, so the binary was
  installed and then not found.
- The directory lock is no longer inherited by agent and git children. The
  lock descriptor is opened close-on-exec, so a killed parent cannot leave
  the tree locked for as long as those children live.
- Agent timeout and output-drain waits no longer leave a timer running after
  the process has already exited. Each wait is a timer that is stopped when
  the other path wins, matching the retry backoff.
- `--update-repo` is checked as `owner/repo` at startup. A URL, a missing
  slash, or an extra path segment used to reach the GitHub API and fail there
  with a status line, or to hit a different endpoint entirely.
- `--dir`, `--dirs`, and `--push-remote` refuse an empty value instead of
  treating it as the current directory or as `origin`.
- A defined agent's `usage` with no `roots` is refused when the definition
  is loaded, rather than later when transcript registration fails without
  naming the file.
- A hot-reload handoff records the predecessor's monotonic elapsed time, so
  `--runtime` and the dashboard clock do not jump when the wall clock steps
  during the exec. An older handoff without the field still uses the
  wall-clock span from when the run started.

## 1.16.0

### Added

- Journal `review_start` and `review_end` events carry the worktree's `branch`
  in worktree mode. With `--jobs` above one the lane a review lands in is
  whichever goroutine won the queue race, so the branch is the only record of
  which working directory the agent actually saw.

### Changed

- `gauntlet suggest` orders its agent pool with the same keyed draw the review
  schedule uses, so the order is a pure function of the recorded seed and the
  pool size. It was `math/rand`'s `Shuffle`, whose sequence nothing in-tree
  pins, so the same seed could pick a different agent after a toolchain update.

### Fixed

- Reload handoff files whose successor never ran are swept on the next save.
  A kill, an OOM, a power cut, or a failed exec between the save and the
  re-exec skips every defer and leaves a blob no process will ever read; they
  accumulated in the state directory. Files older than the retention window
  the temp sweep already uses are removed, best effort.

## 1.15.0

### Fixed

- An agent can no longer disable `Ctrl-C` for the whole run. Agents now start
  in their own session, so they have no controlling terminal: previously an
  agent CLI could open `/dev/tty` and put the shared terminal into raw mode,
  after which `Ctrl-C` generated no signal for anyone and the run could not be
  stopped from the keyboard at all. The kernel's SIGTTOU guard does not
  prevent this -- a runtime that ignores SIGTTOU, as Node-style CLIs routinely
  do, changes the terminal settings from a background process group without
  breaking stride. Reproduced with an opencode-style agent; the group-kill
  semantics on timeout and termination are unchanged, since a session leader's
  process group is its own.

- A `--usage-cmd` probe that prints `NaN` no longer ends the run on its first
  check. `NaN` parses as a float and compares false against every bound, so it
  passed the 0-100 range check and then read as "at or past the limit": a run
  configured with `--usage-limit` stopped before its first review, reporting a
  graceful finish rather than the broken probe. Non-finite answers are now
  rejected with the other unreadable ones, and the limit is ignored for the
  run, which is what every other probe failure already did.

- `--usage-cmd` is no longer accepted when it holds nothing but whitespace.
  The value is split on whitespace into an argv, so a blank one produced no
  command at all; the check that refuses `--usage-limit` without a probe
  compared the raw string, so `--usage-cmd " " --usage-limit 80` passed it and
  the run carried a limit that could never trip. It is now a usage error.

- A defined agent's `argv` now expands every placeholder in an argument, not
  just the first kind it mentions. An entry packing more than one into a
  single option (`"--opts=model={model},effort={effort}"`) had the rest passed
  through verbatim, so the CLI was launched with a literal `{effort}` on its
  command line. A `{model}` or `{effort}` written inside the review prompt is
  still left alone: the prompt is content, not a template.

- File names keep their leading and trailing spaces. Git's NUL-separated
  output was trimmed record by record, so a file called ` notes.md` came back
  as `notes.md`: a stacked PR body listed a path the commit did not touch, and
  the file-signal suggester keyed on a name the tree does not have. The
  records are now taken exactly as git wrote them, which is what `-z` is for.

- `--stacked-prs` accepts a remote URL written with a trailing slash. Git
  stores `remote.<name>.url` exactly as it was typed, and a browser address
  bar copies `https://github.com/owner/project/`; the trailing slash left an
  empty last path segment, so the OWNER/REPO check counted two separators and
  a stacked run refused a remote every other git command works with. The same
  slash also made `https://github.com/only-owner/` parse as a repository,
  which it is not; both now resolve correctly.

- A defined agent no longer receives an empty `--model=` when the run pinned no
  model. An `{model}` or `{effort}` placeholder written into a definition's
  `argv` expanded to nothing, while the equivalent `model` block was simply not
  appended, so the two spellings of the same setting behaved differently: one
  launched `myagent -p PROMPT`, the other `myagent --model= -p PROMPT`, which
  the CLI either rejects or takes as a model named the empty string. An
  argument mentioning an unpinned placeholder is now left out whole. The
  argument carrying `{prompt}` is always kept.

- Agent output in a stream-mode run keeps the order the agent wrote it. JSON
  lines were decoded into a map, and Go randomizes map iteration, so an
  envelope carrying two text fields, or two sibling blocks each carrying text,
  came out swapped roughly one line in ten: the dashboard feed reordered an
  agent's sentences and the run journal recorded them that way. Objects now
  keep their key order through decoding. A repeated key contributes both
  values rather than only the last, since both are text the agent emitted.

- The dashboard's agent panel counts hidden agents correctly. The "+N more
  agents" line spends one of the panel's rows, which the count did not
  subtract, so it always named one agent fewer than it was hiding: eight rows
  and ten agents drew seven lanes and reported two hidden rather than three.
  The review grid's own overflow marker already did this arithmetic right.

- `--suggest`'s confirmation no longer offers reviews that `--exclude` has
  already removed. The preview was built with no exclusions applied, so a run
  with `--reviews sec --exclude sec` listed `sec-review` as "named on the
  command line" and counted it in the total the prompt asks about, while the
  schedule correctly dropped it. The preview and the schedule now share one
  expansion, and a bad `--reviews` is reported before consent is asked for
  rather than after it is given.

- `gauntlet pick` no longer composes a run that selects different reviews than
  were ticked. The launcher shortens a selection by dropping the `-review`
  suffix, and `--reviews` resolves set names and the `suggest` keyword before
  review names, so a tree carrying `security-review.md` was launched as
  `-r security`: the eight-review security set ran and the ticked review did
  not. A name whose short form is one of those words is now written out in
  full.

- `--list` and `--dry-run` line their columns up for review names that are not
  one column per character. The name column was budgeted in runes (`--list`)
  or bytes (`--dry-run`) and padded by `fmt`, which counts runes; a terminal
  lays out in cells, so a project prompt with a CJK name pushed the `[project]`
  column three places out of line and could run its description past the right
  edge. Widths, padding, wrapping, and the description cut are now measured in
  terminal cells, as the dashboard already measured them.

- A review's `mark:` signal now matches. `Signals: mark:comptime` is the
  example both `docs/RUNS.md` and the prompt reference give, and it could never
  fire: the file-signal suggester recorded only the built-in table's category
  labels (`http`, `sql`, `concurrent`, ...), so a declared value matched only by
  colliding with one of those. Declared substrings are now searched for in the
  same file heads the built-in rules read, bounded to 64 across the prompt set.
  A tree whose reviews declare no `mark:` is scanned exactly as before.

### Changed

- Transcript reading requires toktop v0.7.0, which follows dsh's default
  zstd session logs (`session.jsonl.zstd`). `--stream` no longer patches
  dsh to write uncompressed JSONL.

- `Ctrl-C` is staged. The first one is now the graceful quit -- the review in
  flight finishes and lands its work, commit, push and PR included, exactly as
  `SIGQUIT`, `s` on the dashboard, and a tripped usage limit end a run -- the
  second terminates the running reviews and exits 130, and the third
  force-kills. A `Ctrl-C` arriving while any finish request is already
  draining skips straight to terminating. `SIGTERM` is unchanged and never
  staged: a supervisor's `SIGTERM` means stop now. The dashboard's `Ctrl-C`
  follows the same stages; `q` and `esc` still quit immediately.

### Added

- `--usage-limit PCT` with `--usage-cmd CMD` ends a run gracefully when a
  provider's usage window is nearly spent, rather than letting the next review
  hit the wall. Between reviews the runner asks the command what percentage is
  gone; at or above the limit it stops starting reviews, and the one in flight
  finishes normally — commit, push, PR, merge all still happen. The percentage
  has to come from a command because no agent CLI reports it to a headless
  launch: Claude Code, for instance, reads it from an API response header and
  passes it to a status line, which does not run under `--print`. A probe that
  fails or answers with something other than a percentage is reported once and
  then ignored for the rest of the run, so a broken probe cannot end a run
  early.

## 1.14.2

### Fixed

- A worktree commit whose review printed no `SUBJECT:` line names the files
  it touched (`chore: add helper.go`) instead of `chore(<review>): apply
  review findings`. The fallback is what git status shows, not the review
  that produced the diff, so the history still reads as the project's.

## 1.14.1

### Fixed

- A cancel during `git worktree add` no longer leaves a locked,
  half-created worktree behind. All three worktree creation paths
  (lanes, stacked PRs, snapshot) now unlock and remove on failure.
  `Worktree.Remove` retries after unlocking when the first attempt fails.

## 1.14.0

### Changed

- Plain reporter summary uses blue stat labels and bold section headers
  (Pull requests, Per-agent stats, Failed reviews) for visual structure.
  Two new palette codes: blue, cyan.

- All user-facing text now says "lane" instead of "worktree" when describing
  `--jobs N` parallel mode: dashboard header, dry-run output, startup log,
  `--jobs` help text, and the pick TUI.

## 1.13.0

### Changed

- `--jobs N` now creates N persistent lane worktrees reused across reviews
  instead of a throwaway worktree per review. Within a lane, reviews run
  sequentially in the same directory, so the agent's system prompt prefix is
  byte-identical and the provider's prompt cache hits after the first review.
  Cache cold starts scale with the lane count, not the review count: a
  15-review run with `--jobs 3` pays 3 cold starts and gets 12 cache hits,
  compared to 15 cold starts and 0 hits previously. Falls back to sequential
  mode if lane creation fails.

- An `--agents` entry can pin a reasoning effort alongside the model:
  `claude:opus-5@xhigh`, `opencode:anthropic/claude-sonnet-5@medium`, or
  `claude@max` with no model. The part after the last `@` is the effort, and
  like a model id it is passed to the CLI verbatim. It is wired only for
  agents whose flag was verified from the CLI's own help, `claude`
  (`--effort`) and `opencode` (`--variant`), and for defined agents via a
  new `effort` argument list (or an `{effort}` placeholder) in
  `agents.json` / `--agent-cmd`; every other agent refuses `@effort` at
  startup instead of guessing a flag.
- Custom agent names may no longer contain `@`, which now separates the
  effort. A definition in `agents.json` or `--agent-cmd` that uses one is
  refused at startup, like the other reserved separators.

- A stacked run's pull requests describe themselves. The body was the commit
  subject under a `## Summary` heading, which repeated the title and said
  nothing else; it now adds the review's declared subject area, the paths the
  layer's commit touched (ten, then a count), its diff stat, and where the
  branch sits in the chain, so a PR can be triaged without opening the diff. A
  value git or the prompt will not answer is left out rather than guessed at,
  and everything read out of the reviewed repository is flattened to one line
  and length-bounded before it becomes Markdown.

### Fixed

- `--agents mixed:opus` (or `mixed@high`) no longer falls through to an
  error that calls `mixed` unknown and valid in the same sentence; it now
  says the keyword selects every installed agent and cannot pin a model or
  effort.

## 1.12.2

### Changed

- Source builds and `go install` need Go 1.27.

### Fixed

- The file-signal suggester no longer follows a last-component symlink out of
  the reviewed tree, and no longer blocks on a planted FIFO, when it peeks at
  source. Duplicate-prompt comparison uses the same open as prompt body reads
  (`O_NOFOLLOW`, non-blocking), so a swap between lstat and read cannot pull
  out-of-tree content into the comparison.

## 1.12.1

### Fixed

- Cancelling a stacked run while it sets up a layer records that review as
  interrupted instead of failed. The cancel kills the git or gh command
  building the layer, and the step reported its own failure, so a Ctrl-C
  counted a failed review and published a `pull_request` failure event for a
  layer no agent had touched. Only reachable on a slow enough machine to be
  mid-setup when the cancel lands, which is why it showed up on macOS first.

## 1.12.0

### Added

- `--stacked-prs` runs the selected reviews sequentially in one isolated
  worktree. Each changed review is committed and pushed on a child branch,
  then opened as a pull request against the preceding changed review; the
  original checkout is never changed and no pull request is merged. The base
  commit is fetched directly from the publishing remote after every preflight
  check passes, the dirty-checkout consent included and before any agent
  starts, the suggestion agent too. That commit is then pinned for the whole
  run: a hot reload resumes the same stack even when the remote base has
  advanced. Project prompts and suggestion signals are read from a snapshot
  of that fetched base, never from uncommitted files in the checkout. A
  remote with distinct fetch and push URLs opens cross-fork PRs with the head
  qualified by the push-side owner. `--pr-base` selects the first base and
  `--push-remote` selects the publishing remote.
- A prompt may declare a `Summary:` line, the short form of what the review
  looks for, joining `Signals:` as a line a project's own review can carry. It
  is what `--list` and the launcher's picker now print, so the catalog reads as
  one line per review instead of a goal sentence cut mid-clause, and a review
  that declares none still falls back to that sentence. Documented in
  docs/RUNS.md.

### Changed

- The README carries the full review grid: every bundled review and what it
  finds, so the front page shows the catalog it used to only count. A test
  rebuilds the table from the prompts and fails when the two disagree.
- Every review's fixing rules gained two checks around proving a finding.
  After proving one real, the agent now argues the author's side, and that
  counterargument only retires the finding when it can name the code backing
  it, so a plausible-sounding excuse no longer discards a true positive. The
  second check bars the opposite failure: "unlikely in practice" is not
  grounds to drop a deadlock, crash, or data-corruption finding, only
  "structurally impossible" is, so a wait with no timeout still gets fixed
  when the trigger needs bad timing.

## 1.11.0

### Added

- Each launch journals the SHA-256 of the prompt text it ran under
  (`prompt_sha256` on `review_start` and `review_end`), so an output stays
  attributable to the exact words that produced it after the prompt file has
  changed or disappeared. Bundled prompts have the run's version line for
  identity; project prompts and `--prompt-dir` files had none at all.
- `GAUNTLET_NO_ANIMATION` joins the environment surface: set it to anything
  but empty or `0` and the dashboard's animated reasoning glyph holds one
  frame instead of cycling, for motion sensitivity. The token count beside it
  keeps updating, so an active agent still reads as one.
- `TERM` is documented as part of the environment surface it always was:
  `TERM=dumb` disables color, even with `CLICOLOR_FORCE` or `FORCE_COLOR` set.
  The help screen's environment section now lists it beside the other color
  variables, matching docs/CLI.md.

### Changed

- A binary that was not stamped reports the version the Go toolchain embedded
  rather than `dev`: `go install github.com/maci0/gauntlet/cmd/gauntlet@latest`
  names its release, so `gauntlet update --check` compares against it, and a
  build from a source tree one tag behind the working tree says so
  (`1.10.0+dirty`). Builds that do stamp (`make dist`, `release.yml`) report
  exactly the value `-ldflags` put there; only `(devel)` and absent module
  versions stay `dev`.
- A repeated `--agent-cmd` giving one agent two different definitions refuses
  the run instead of letting the later one silently win; an exact repeat stays
  idempotent. Scripts that stacked definitions unnoticed now see the error,
  the way `--bin` has always refused its own duplicates.
- The agent output tail is a ring buffer instead of an unbounded transcript,
  journal history is filtered on the way in rather than after it is read, and
  the launcher and dashboard redraw from cached frames when nothing changed,
  so watching a long run does not get slower as it grows.

### Fixed

- A merge failure that leaves nothing unmerged is reported as failed rather
  than MERGE CONFLICT: the conflict resolver is no longer sent to fix what no
  edit fixes, the summary does not call a broken merge a conflict, and the
  branch-keeping rule keeps its meaning for real conflicts. A file that
  cannot be read fails the conflict-marker scan instead of silently counting
  as resolved.
- Reviews a hard cancel will never start are recorded as interrupted in the
  sequential loop, the way the parallel loop already recorded its stranded
  lanes, so the summary, the stats, and the journal no longer drop them.
- A review whose worktree add loses a cancel race is cleaned up (the
  half-written worktree registration and its branch) and reported like any
  other skipped review instead of leaving both behind.
- A review skipped because its worktree could not be created journals its
  `review_end` like every other outcome, so a consumer rebuilding a run from
  the journal no longer waits on an event that never came.
- A run whose stdin is a character device that is not a terminal (`/dev/null`
  under cron) proceeds without confirmation, instead of prompting, reading
  EOF, and aborting. The tty check is `term.IsTerminal`, the same answer the
  launcher's gate gives.
- The dashboard, the launcher, and their non-interactive fallbacks render
  inside the terminal's width and height instead of scrolling it, and the
  keys that matter stay visible when a narrow terminal clips the footers.
- The help screen documents the defaults it was omitting: `--timeout` and
  `--suggest-timeout` name their 30 minutes, `--limit` its 20 entries, and
  `--runtime` its `0 = unlimited`.

## 1.10.0

### Changed

- `--suggest-agent gauntlet` weighs evidence instead of matching filenames. It
  now counts how much of a tree each language is (one stylesheet no longer
  proposes five frontend reviews), reads the head of source files for what they
  import and call, asks git which files have changed in the last 90 days,
  treats absence as evidence (no tests, no docs, no CI argue for the reviews
  that would fix that), and demotes reviews that have finished in a directory
  several times without changing a line. Proposals are ranked by that evidence
  and the weakest are dropped. Against the reviews agents picked in this
  install's own journals, recall went from 0.62 to 0.82 at the same precision.
- The tree is listed with `git ls-files` where there is a repository, so the
  project's own ignore rules decide what counts as source. The walk with a
  built-in skip list stays as the fallback.
- Five bundled reviews (`arch`, `dr`, `functionality`, `idempotency`, `lint`)
  could not be proposed by any rule. They can now.

### Added

- A review can declare what it keys on with a `Signals: ext:.zig, name:build.zig`
  line, which is what makes a project's own prompt reachable by the file-signal
  suggester: the built-in rules only know built-in names. The line comes from
  the reviewed tree, so it is parsed strictly and bounded.
- `scripts/suggest-calibrate.py` scores the suggester against the picks agents
  made in past runs, so a rule change is measured rather than argued about.

## 1.9.0

### Added

- `--resolve-conflicts` (on by default): a review branch the main tree refuses
  is handed to an agent, which resolves the conflict in a scratch checkout cut
  from the current tip; the result is merged and the branch deleted. The merge
  lock is held for the whole step, nothing carrying conflict markers is
  committed, and every failure path leaves what a plain conflict left, the
  branch kept for a human. `--resolve-conflicts=false` restores that older
  behavior.

### Fixed

- Parallel reviews cut their worktree from the tree's current tip instead of
  the commit the loop started on. Lanes refill for as long as a loop runs, so
  a review that waited hours had to merge against everything that landed
  meanwhile: in real runs the first merge of a loop never conflicted and half
  the later ones did.

## 1.8.0

### Changed

- `--suggest` composes with `--reviews` instead of refusing to run beside it.
  The triage step picks, and every review named on the command line is
  scheduled as well; one that appears on both lists is scheduled twice, which
  is what repeats in `--reviews` have always meant. `--reviews suggest,sec`
  says the same thing, so "suggest" is no longer required to be the only value
  in the list. The proposal prints what rode along with it, and the launcher
  keeps its review tree live when suggest is ticked, weighting what you tick
  rather than greying it out.
- Single-letter flags accept their value glued on, so `-j3` means `-j 3` the
  way it does in make and tar. The spaced and `=` forms are unchanged.

## 1.7.4

### Fixed

- The coverage floor is CI's measurement rather than a developer machine's.
  1.7.3 shipped it at 76.0%, which is what this suite measures where the agent
  CLIs are installed; a runner without them covers about two points less, so
  every pull request would have failed the gate the release introduced.

## 1.7.3

### Fixed

- `SUBJECT:` lines keep no ragged end. Control bytes were stripped after the
  trim, so a line ending in spaces and a NUL left the spaces in the commit
  subject. Found by the fuzz target added for it, whose seed is now the
  regression corpus.

### Changed

- Coverage ratchets: `make cover` fails below `COVER_MIN` and CI runs it, so a
  refactor that quietly drops a tested path fails where it happens rather than
  a release later. This release carries 76.0%, a measurement taken where agent
  CLIs are installed; CI measures about two points lower, so pull requests
  fail until 1.7.4 lowers the floor to CI's own 74.0%.
- Two parsers that read input from outside the program are fuzzed:
  `SUBJECT:` lines, which are agent output on their way into a commit message,
  and `--timeout` durations, which are whatever a person typed.
- Production code that only tests called is gone or moved: the runner's
  `finishing`, gitx's uncached `countLines` (its test drives the cached path
  the program uses), the dashboard's `staticFrame`, and the help table's
  `names` accessor. The two that stayed live beside their callers, in test
  files.

## 1.7.2

### Fixed

- The binary lookup follows `PATH` instead of answering from a cache built
  before it changed. Both resolvers memoized on nothing: the agent probe and
  the git lookup each answered once per process, so a program that added a
  directory to `PATH` (a wrapper, a test harness) kept being told the tool it
  had just put there was missing. Each memo is keyed by the `PATH` it was
  built from now. This is what made 1.7.1 fail to publish: its own test suite
  hit the stale answer on a machine where the agent it stubbed was not
  otherwise installed.

### Changed

- The prompt library and the docs follow the writing rules the project sets
  for the code it reviews: no em dashes (49 of them, rewritten as the comma,
  colon, or sentence break each one was standing in for), and none of the
  weasel words the style rules ban, of which "actionable" alone appeared 27
  times in one boilerplate line that read better without it.
- Tuning constants that were sitting inline are named where they are set: the
  flag defaults the docs quote (`--timeout`, `--retries`, `runs --limit`), the
  dashboard's thinking-glyph timings, and the four budgets every git command
  now picks from instead of carrying a bare duration.
- `.scratch_refs.txt`, a throwaway file that reached a commit, is out of the
  repository. The root holds config, manifests, and top-level docs, and
  nothing else.
- The journal reports a write it lost. `Flush` and `CloseQuiet` dropped their
  errors on the floor, so a disk that filled halfway through a run left a
  truncated journal that still closed clean; both keep the first error now,
  and `Close` reports it the way it always claimed to.
- CI gates the one Python file here (`scripts/shots/render.py`) with ruff and
  mypy `--strict`, the way every other language in this repository is gated.
- Every remaining discarded error says what it swallows and why nothing else
  can reach it: a process group that is already gone, a ring buffer that
  cannot fail, a cleanup whose failure the next command reports anyway.
- `scripts/shots.sh` no longer writes Python from shell or works in `/tmp`:
  the renderer is `scripts/shots/render.py`, a uv script with inline
  dependencies, and the working directory is `.scratch/`, which is on disk
  rather than in RAM.

- The README shows the dashboard and the launcher as screenshots rather than
  an ASCII transcript. Both are the renderer's own output: `scripts/shots.sh`
  writes the frames from `internal/ui`, exports them as a terminal, and
  rasterizes that, so refreshing them after a change to the screen is one
  command rather than a hand-drawn approximation.

## 1.7.0

### Added

- `make vuln` runs locally the same govulncheck advisory scan that
  vulnscan.yml runs on pull requests touching go.mod or go.sum, so a
  vulnerable dependency bump surfaces before push instead of after.
  CONTRIBUTING.md documents it and the real steps for adding a review
  prompt: the name-surface snapshot in contract_test.go must gain the new
  stem in the same change.
- A release-contract guard. Tests now pin every surface this file declares
  consumer-facing: bundled review names, review set names, CLI flag names,
  commands, exit codes, the documented environment variable names
  (`GAUNTLET_HOME`, `GITHUB_TOKEN`, `NO_COLOR`, `CLICOLOR_FORCE`,
  `FORCE_COLOR`), and that no importable Go package lives outside
  `internal/`. Removing or renaming one fails the suite with the required
  bump kind spelled out, and a set member left pointing at a renamed review
  is caught instead of silently shrinking its set. This is the automated
  check whose absence let the public `agentusage` removal ship in the 1.0.1
  patch release unrecorded.

### Changed

- A subcommand now refuses flags it does not read, instead of parsing and
  silently dropping them: `gauntlet runs --jobs 4` used to print its table
  while ignoring the concurrency it was given. Each command accepts what
  docs/CLI.md lists for it (`pick`: `-C/--dir`, `--dirs`, `--prompt-dir`;
  `doctor`: `--bin`, `--agent-cmd`; `update`: `--check`, `--update-repo`;
  `runs`: `--limit`; `show`: nothing of its own), plus the global `--log`
  and `--no-color`, and names the flag and the command in the error.
  Scripts that passed such flags and never noticed will see exit 2.
- gauntlet's own scratch is excluded from the repository it reviews: the run
  lock (`.gauntlet.lock`) joins the worktree root in `.git/info/exclude`, in
  every run rather than only in `--jobs` mode, so it can never be committed by
  a review, a commit step, or a person running `git add -A`. The exclusion is
  local to the clone, which is where one tool's scratch belongs.
- Worktree runs leave a history that looks like the project's. Review branches
  are squashed rather than merged, so a loop of forty reviews leaves forty
  commits and not forty merge nodes; the commits are authored with your git
  identity instead of a `gauntlet <gauntlet@localhost>` one; and the subject
  is what the review says its change was, in the project's own conventional
  form, rather than "automated review fixes". Reviews print it as
  `SUBJECT: fix: …` alongside their `PATH:` lines, and a review that prints
  none falls back to `chore(<review>): apply review findings`.
- `--push` pushes each review as it lands in worktree mode, not once at the
  end of the loop, so a long run publishes as it goes. A failed push is
  reported and counted rather than fatal: the work is committed, and the next
  push carries it.
- The offer to commit a dirty tree uses the run's own agent, never
  `--suggest-agent`: that one was asked which reviews apply, which says
  nothing about who should write commits.
- The commit step asks for a conventional type. Its prompt matched whatever
  style the repository already used, which on a repository with no style
  produced whatever the agent felt like; it now asks for `feat`, `fix`,
  `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, or `style`, a
  scope when one area owns the change, imperative mood and no trailing
  period, and still defers to the repository when its own log says otherwise.
- `--merge-into` writes `Merge branch '<branch>'` rather than a message
  naming the run that produced it, and a conflicted review now prints the
  command that lands it without writing the scratch branch name into the
  history: `git merge --squash <branch> && git commit -m "<its subject>"`.

### Fixed

- A retried review in `--jobs` mode now starts from the commit its worktree
  was cut from. Previously a failed attempt's half-applied fixes stayed in
  the checkout, so the retry (and whatever it committed) depended on how
  many attempts had run before it. In-place reviews are unchanged: their
  tree belongs to the user and is not rewound.
- docs/CLI.md documents the six flags its intro claimed to cover but did
  not list: `--seed`, `--opencode-db`, `--no-color`, `--hot-reload`,
  `--auto-update`, and `--update-repo`.
- The help screen's environment section printed `TERM=dumb` twice.
- `gauntlet show` sanitizes journal lines before printing them. An event's
  text can carry fragments of the reviewed repository (git error output,
  merge-conflict file names); JSON escaping removes control bytes but lets
  bidi overrides through, so a hostile tree could rearrange how the replay
  reads on a terminal. Every other display surface already stripped; the
  replay path was missed.
- A review that never ran says so in the final summary (`skipped: never ran
  (unknown name or unreadable prompt)`) instead of listing itself with an
  empty reason.
- A project prompt saved with a UTF-8 byte-order mark keeps its first line:
  editors on Windows write the mark in front of every file, and left in
  place it hid the goal line descriptions are extracted from, so the
  launcher and the suggest catalog showed such reviews as undescribed.
- The suggest step fences a planted review *name* the way it already fenced
  planted goal text: a project-local name carrying `</catalog>` or a
  `RELEVANT:` marker cannot close its fence or forge a proposal line.
- Launcher input matches how terminals actually send text: backspace deletes
  one grapheme cluster rather than one code point (a dead-key accent leaves
  with its letter), filter matching normalizes Unicode so a composed
  spelling finds the same review the command line does, a tree with no
  installed agent CLI is refused with its reason instead of composing a run
  that fails on launch, and the row under the cursor explains itself in the
  status line.
- Taking the directory lock clears a dead run's note. A killed run leaves its
  lock file behind with its note in it; the flock died with the process, so
  the next run takes the lock without trouble, but anything reading the file
  was told a run is under way that ended yesterday.

## 1.6.1

### Fixed

- Untracked files no longer block `--jobs`. A review works from a commit, so
  uncommitted work in a *tracked* file is what it would miss and then collide
  with; a file git never heard of is in nobody's way. Refusing to run over one
  was a dead end besides, since the commit step stages tracked files only
  (`git add -u`) and would leave a scratch script sitting there forever. The
  run now says once which untracked files are not reviewed and gets on with
  it, and both the precondition and the commit offer name the paths that are
  actually in the way.

## 1.6.0

### Added

- A dirty tree no longer just refuses a `--jobs` run: gauntlet offers to
  commit it. The changes are the obstacle, and there is already an agent that
  writes commit messages, so it asks (`Commit them with claude first? [y/N]`),
  runs the commit step, checks with git rather than taking the agent's word,
  and starts the run. `--yes` and `--yolo` answer yes; a run with no terminal
  keeps the plain error rather than committing unattended.

## 1.5.1

### Fixed

- crush's token counts are read for real runs, not just for tests. Its
  `sessions.updated_at` column carries two units (crush writes milliseconds,
  the table's own trigger writes seconds), so the window bound matched nothing
  and every crush review reported zero. Found by running one. Fixed in toktop
  v0.4.5, which this release requires.

## 1.5.0

### Added

- Every review prompt now names the helper tools this machine actually has,
  and the ones it does not: an agent that does not know `cppcheck` is here
  reads the C by hand, and one that does not know it is absent spends budget
  finding out, or tries to install it against the rules. The binaries are
  probed once per run, in one parallel pass, and the same line appears in
  `--show-prompt`.
- The tool catalog covers the toolchains it was missing: C and C++
  (`cppcheck`, `clang-tidy`, `clang-format`, `cpplint`, `scan-build`,
  `include-what-you-use`, `bloaty`), Go (`staticcheck`, `golangci-lint`,
  `gocritic`, `errcheck`, `govulncheck`, `gofumpt`, `deadcode`), typing and
  spelling (`mypy`, `codespell`, `typos`), formatting and config (`shfmt`,
  `editorconfig-checker`, `biome`), infrastructure (`checkov`, `conftest`,
  `kubeconform`, `ansible-lint`, `dockle`), mobile (`swiftlint`, `ktlint`,
  `detekt`), and more besides. `lint-review`, `error-review`,
  `mobile-review`, and `privacy-review` have tooling for the first time.

- A graceful quit: `s` on the dashboard, or `SIGQUIT` (`Ctrl-\`) anywhere,
  stops starting reviews, lets the ones running finish, commits, pushes, and
  merges what they produced, and then ends the run. `Ctrl-C` still means stop
  now. The difference matters mid-loop: interrupting leaves agent edits
  uncommitted in the tree, and this does not.
- `crush` ([charmbracelet/crush](https://github.com/charmbracelet/crush)) is a
  supported agent: `crush run --quiet` is its non-interactive mode, which
  auto-approves the session's tool permissions itself, and `-C` continues the
  last session for `--continue-sessions`.

- `--suggest-agent gauntlet`: the suggester that is not an agent. It reads the
  tree for signals (extensions, well-known filenames, directory names) and
  proposes the reviews those files justify, with the evidence as the reason,
  in milliseconds and for no tokens. It cannot tell a toy HTTP handler from a
  payment path, which is what an agent is for; it is very good at knowing
  there is no Dockerfile. The launcher offers it beside the agents.
- crush's token counts are read from its project database (`.crush/crush.db`,
  `sessions.completion_tokens`) in builds with `-tags sqlite`, the same tag
  `--opencode-db` needs. Only sessions written during a review count toward
  it. `gauntlet doctor` says whether this build can read them. The reading
  lives in toktop's `agentusage` with every other agent's, not here (toktop
  v0.4.3).

### Changed

- Released binaries and `make build` carry the SQLite driver by default
  (`TAGS ?= sqlite`), so crush's project database is read with no flag and
  `--opencode-db` can be asked for on any release. The driver is pure Go, so
  cross-compilation is unaffected. `make build TAGS=notoktop` still drops
  transcript reading, and `make build TAGS=` now drops the driver with it,
  which is the standard-library-only build.

### Fixed

- Token counts are not lost by a short review: toktop's final read was reusing
  a cached listing that predates the transcript the review just wrote, so a
  review finishing within a second of its first output reported nothing at
  all. Fixed in toktop v0.4.3, which this release requires.
- A hot reload is a handover again, not a restart. The successor is exec'd
  with the arguments this process is running rather than the ones it was typed
  with, and every resumed directory keeps the schedule it had already
  resolved, so a run composed by `gauntlet pick` no longer reopens the
  launcher and a `--suggest` run no longer asks an agent (and the user) to
  choose a second time. A resumed run also inherits the `--semcode` index its
  predecessor built instead of spending another half hour on it.
- `journal_test.go` called `Events` with its old signature, which broke
  `go test ./internal/journal` at HEAD: one call site was missed when the API
  became a streaming visitor.

## 1.4.1

### Fixed

- `--dirs` runs the suggest step for every directory at once instead of one
  after another. Each tree has its own prompt set and its own answer, so each
  is asked on its own, and asking six trees in sequence meant up to six
  `--suggest-timeout` waits (three hours at the default) before the first
  review started. The lines from the concurrent steps carry the directory they
  belong to, the proposals are printed in directory order, and one
  confirmation covers all of them.

## 1.4.0

### Added

- The launcher shows what each review does, in a column beside the names, and
  marks the ones a reviewed tree carries itself as `[project]`. A list of 50
  names said nothing about which of them to pick.
- `/` filters the review tree by name or by description, opening what it finds
  and hiding what it does not. Every key types while the filter is open, so a
  review can be found by typing `quick` without the `q` quitting.
- The launcher refuses a run the tree cannot support instead of composing a
  command that fails on launch: concurrency above 1 needs a clean tree, and
  the reason sits under the command line until it is resolved.
- Picking no reviews reads as "all 51" in the launcher rather than "0 of 51",
  which is what the composed command has always meant by saying nothing.

### Changed

- The dashboard header names the tree being reviewed by path (`~/src/acme`),
  not by basename: several checkouts of one project share a basename, and a
  run is not the same run in each of them. The path takes the room the rest of
  the header leaves and is cut from the left, so the loop and the run state
  are never pushed off the line. The launcher header does the same.
- Agents wear their vendor's color where there is one (claude, codex, gemini,
  qwen, grok), in the launcher and in the run that follows, so a lane is
  identifiable before its name is read. Each is pulled toward its background
  until it clears the same 4.5:1 text floor as every other color here, and a
  second model of the same vendor takes the old rotation, because telling two
  lanes apart matters more than showing a brand twice.
- The launcher's job count is labelled `concurrency` and `+`/`-` change it
  from any pane. Its panels also scroll rather than spilling off a short
  terminal, and the run pane keeps its rows while the agent list gives first.
- A tagged release publishes its CHANGELOG section as the GitHub release notes
  again, and refuses to publish when that section is missing: the Go rewrite
  had switched to GitHub's auto-generated notes, which list commits for
  maintainers instead of telling consumers what changed for them. The
  workflow now also smoke-tests what it is about to publish: the host binary
  must run and report the tagged version, and its checksums.txt line must
  verify, so a broken or mislabeled build fails before any asset is uploaded.

## 1.3.0

### Added

- `--merge-into BRANCH`: after each loop, the committed work on the branch the
  reviews ran on is merged into BRANCH. Until now nothing in gauntlet targeted
  a branch by name: `--commit` commits where you are, and the worktree
  machinery merges back into where you are. The merge runs in a scratch
  checkout of the target, so your own checkout is never switched under you; it
  needs `--commit` or `--push`, since only committed work merges; and a
  conflict aborts and leaves both branches untouched. The launcher offers it
  as a branch picker beside the commit switches.
- The launcher offers `push` beside `commit`, which was the one run switch it
  could not compose. Picking it passes `--push` alone, since that already
  implies the commit step.

## 1.2.0

### Added

- `gauntlet pick`: a launcher that composes a run on screen, drawn with the
  dashboard's instruments so the two screens read as one cockpit. Reviews are
  collapsible sets with a fill meter each, `suggest` is the first choice among
  them with its own agent picker, the agent pool carries the hues it will keep
  during the run, and concurrency is metered against the CPU count. The
  command line it is building stays on screen and `enter` runs exactly that,
  through the same parser as a hand-typed one, so the launcher teaches the
  flags instead of replacing them.
- The dashboard says when a review is on its second try (`sec-review ↻2` in
  the lane): a retry reuses the lane and resets the clock, so it read as a
  review that had restarted itself.
- `f` in the dashboard narrows the feed to results, errors, and diffs, and
  back. Four agents narrating at once bury the two lines that matter; the
  filter drops nothing, so widening it brings the history back.
- `--retries N` (default 2): a review whose agent fails to launch or exits
  nonzero is rerun on the same agent, waiting 5s and doubling from there, with
  jitter so reviews that failed together do not come back together. Rate
  limits and dropped connections are what this is for. Exhausting the retries
  still falls back to a different agent, as before, and timeouts are still
  never retried.
- Journal `review_start` records carry `attempt`, so a run's retries can be
  counted from the record rather than inferred from repeated starts.

### Changed

- The README is a landing page again: what the tool is, install, one screen of
  each idea, and links out. The flag, environment, and exit-code reference
  moved to `docs/CLI.md`, worktree isolation, the run journal, and hot reload
  to `docs/RUNS.md`, and the public token-reading API into
  `docs/TOKEN_TELEMETRY.md` where the rest of that subject already lived.
- `--jobs` is documented as the per-directory pool it has always been: with
  `--dirs`, the two multiply, and `--dirs a,b,c -j 4` is up to 12 agents at
  once. The flag help, the help screen, and the README say so now.

## 1.1.0

### Added

- The directory lock names what the run holding it is doing. `.gauntlet.lock`
  carries the version, pid, run id, and the reviews in flight, so a second
  gauntlet turned away from the directory reports `another gauntlet is already
  running here: gauntlet 1.1.0 (pid 8123, run 20260825T…): config-review
  (opencode)` instead of only that something holds it.
- `--seed N`: replayable review order and agent picks. A nonzero seed replays
  the per-loop review shuffle and agent sampling; zero derives one from the
  clock as before. The effective seed rides the run-start event, so a journal
  describes its own rerun, and hex literals (`0x…`) are accepted.

### Changed

- Reading an agent's own session transcript is on by default, in source builds
  too: it costs one pure-Go dependency and is the only source of counts for
  agents that print none, so the tag now opts out (`-tags notoktop`,
  `make build TAGS=notoktop`) rather than in. `--opencode-db` needs `-tags
  sqlite` alone now that `toktop` is implied.
- `agents.json` refuses unknown keys instead of ignoring them: a misspelled
  `opt_in` or `usage` would silently change what a definition does (an
  unverified agent becoming auto-detectable, live token counts vanishing), so
  it now fails at startup like any other malformed definition.
- `code-review` treats assertion density as a lead only: each added assertion
  still needs the property or concrete path it encodes.
- `prompt-review` allows creating new prompt files when its missing-prompts
  rule calls for one, instead of banning them outright.
- `ux-review` cites WCAG 2.2 SC 2.5.8 for the touch-target minimum size.

### Fixed

- The `agents.json` examples were labeled and written as JSONC, one with a
  comment; the loader accepts only plain JSON, so copying them verbatim failed
  at startup. The examples are plain JSON now, and the README documents every
  environment variable (`GAUNTLET_HOME`, `GITHUB_TOKEN`, the color controls).
- The dashboard palette adapts to light and dark terminals instead of
  assuming a dark one, and every color that can sit behind text clears WCAG
  2.2 AA contrast on its background (SC 1.4.3). De-emphasized text such as
  pending review cells was near-invisible, the zero-rate activity marker
  rendered in a near-background tone, and unlit meter segments and the chart
  baseline now meet the 3:1 non-text floor (SC 1.4.11).
- A hot reload that crosses UTC midnight appends to the run's original journal
  file instead of splitting it across two date shards; a worktree rerun after
  a reload rebuilds a branch still at base and fails rather than destroys one
  holding commits.
- The README install one-liner maps `aarch64` to `arm64` (so arm Linux hosts
  fetch a real asset), creates `~/.local/bin` before writing into it, and asks
  for the asset by its real name: release binaries carry the version
  (`gauntlet_1.1.0_linux_amd64`), so the unversioned URL it used was a 404.
- Token counters parsed from an agent's output are bounded. A review that
  prints a usage-shaped sentinel it read somewhere else (a `9223372036854775807`
  in a source file it quoted) was believed, and the run total then overflowed
  to a large negative number with a live rate to match. Anything above a
  trillion tokens now reads as a misparse, not a measurement.

## 1.0.2

### Added

- `--opencode-db`: read opencode's session database for its token counts.
  opencode keeps sessions in SQLite rather than the JSONL every other agent
  writes, which is why it reported no tokens until now. Needs a build with
  `-tags "toktop sqlite"`; a build that cannot honor the flag says so instead
  of silently measuring nothing.

### Changed

- The transcript-reader dependency is renamed: `tokentop` is now `toktop`
  (`github.com/maci0/toktop/agentusage`), and the build tag is `toktop` to
  match. Source builds that passed `TAGS=tokentop` pass `TAGS=toktop`.
  Tracks toktop v0.4.1.

## 1.0.1

### Removed

- **Breaking:** the public `agentusage` package is gone from this module;
  transcript token reading moved to tokentop's
  `github.com/maci0/tokentop/agentusage`. Programs importing
  `github.com/maci0/gauntlet/agentusage` stop compiling against v1.0.1:
  migrate the import to tokentop's package (renamed again to
  `github.com/maci0/toktop/agentusage` in 1.0.2). This removal shipped in a
  patch release without an entry here at the time; it belongs under a major
  bump per the contract above, and is recorded now rather than left silent.

### Fixed

- Worktree bookkeeping is serialized so parallel reviews cannot strand
  branches or stray checkouts after an interrupt: git validates every
  registered worktree on each add or remove, and two at once could read
  another's half-deleted metadata.
- `make build TAGS=` produces a standard-library-only binary that reads only
  what agents print; released binaries carry the reader as before.

## 1.0.0

Rewritten in Go, shipped as one static binary. The Python implementation
(0.26.0 and earlier) is replaced; prompts, injected rules, containment, and
exit codes are unchanged, so existing invocations keep working.

Added:

- Parallel reviews with git-level isolation: `--jobs N` gives every review its
  own worktree and branch, then merges them back one at a time. A conflicting
  merge keeps its branch instead of dropping the work.
- `--dirs` reviews several repositories at once, each with its own lock,
  baseline, and lane.
- `--tui`, a live dashboard: per-agent lanes, activity chart, the full review
  grid, and a normalized feed.
- Output normalization: escapes, spinners, repainted progress lines, tool
  gutters, and duplicate narration collapse instead of scrolling past. Diffs
  are colored by sign.
- Live token throughput, read from what agents already report: their streams,
  their own session transcripts, and their machine-readable modes (`--stream`,
  on by default where an agent has one). Reasoning tokens are tracked and shown
  separately. Agents that report nothing show no rate rather than a zero.
- A run journal under `~/.gauntlet`, with `gauntlet runs` and `gauntlet show`.
- `gauntlet version` and `gauntlet help` print the same screens as `--version`
  and `--help`.
- `gauntlet update`: verified self-update, plus hot reload that finishes the
  reviews in flight, hands over the unfinished part of the loop, and re-execs
  without losing counters.
- Agents can be defined rather than compiled in (`--agent-cmd`,
  `~/.gauntlet/agents.json`), including where they keep transcripts. The pi
  family (`pi`, `prime-agent`, `feynman`, `omp`) ships as such definitions.
- `agentusage`, a public package exposing the token reading, so other tools can
  report the same numbers.

Changed:

- Linux and macOS only. The runner depends on process groups, `flock`,
  `O_NOFOLLOW`, and `execve`; Windows has no equivalent that keeps those
  guarantees.
- `--target-dirs` is now `--dirs`, with the old name kept as an alias.

## 0.26.0

### Added

- `--runtime DUR`: wall-clock budget for the entire run (e.g. `8h`, `30m`,
  `2d`). When the budget is reached, the current review and its commit/push
  step finish before stopping. 0 (default) means unlimited.
- `--target-dirs DIR [DIR ...]`: run a parallel review loop per directory,
  each with its own lock, git baseline, and stats. Output is prefixed with
  the directory name. Shell globs are expanded (quoted or not). Conflicts
  with `--dir`.
- `--tui`: experimental curses dashboard with live review status, stats
  gauges, and scrollable agent output. No dependencies (stdlib curses).
- Short flags: `-c` (commit), `-p` (push), `-s` (suggest), `-1` / `--once`.
- `--raw`: echo agent output verbatim. Default is now normalized: ANSI
  escapes stripped, spinner frames dropped, repeated progress lines
  collapsed. Different agents (claude, gemini, kimi, codex, etc.) have
  wildly different verbosity; normalization gives a consistent signal.

### Changed

- `--push` now silently implies `--commit` (no warning when both given).
- Removed deprecated `--models` alias (use `--agents`).
- `--quiet-agents` long form removed; use `-q` or `--quiet`.

## 0.25.0

### Added

- `--suggest-agent AGENT`: use a specific agent for the suggest triage
  step, independent of `--agents` which governs the reviews themselves.
  When set, only this agent is tried; default: sample from `--agents`.
- `--suggest-timeout DUR`: timeout for the suggest step (default 30m),
  independent of `--timeout` which governs per-review execution.

### Changed

- `--list` now silently takes precedence over `--suggest`, `--reviews
  suggest`, `--commit`, and `--push` instead of erroring. Compose flags
  freely; `--list` always wins.
- Suggest timeout default raised from 5m to 30m.

## 0.24.0

### Changed

- `specs-review` and `agentrules-review` enforce industry-standard
  PRD/RFC/ADR taxonomy: a PRD is product requirements (what and why), an
  RFC is a proposal for comment (before the decision), an ADR records a
  decision that has been made. A "proposed ADR" or a "decided RFC" is now
  a finding.
- `deps-review` gains license-attribution checks: bundled third-party
  code without NOTICE/ATTRIBUTION, and patent-grant clause awareness
  across transitive dependencies.

### Added

- `AGENTS.md` documents project conventions for agents working on this
  repo.

## 0.23.0

### Changed

- `sec-review` gains post-quantum cryptography checks: harvest-now-
  decrypt-later risk on classical-only asymmetric crypto, crypto agility
  (hardcoded algorithm identifiers with no swap path), and PQ hybrid key
  exchange not negotiated when the library supports it.
- `build-review` gains binary hardening checks: stack canaries, PIE,
  RELRO, FORTIFY_SOURCE, non-executable stack, CFI/shadow stack, and
  sanitizers (ASan/UBSan) not wired into the CI test build.

## 0.22.0

### Added

- End-of-run summary now reports agent wall-clock time (total and average)
  and token usage (output tokens where the agent reports them, session
  totals otherwise) with an approximate tok/s rate. Per-tool breakdown
  includes the rate when multiple agents ran. Token counters are parsed
  best-effort from agent output tails (Claude, Gemini, Codex, and any
  tool printing a JSON or text usage summary).

### Changed

- Seven more review prompts sharpened with checks sourced from project
  review files and the ct-recomp decomp playbook:
  `concurrency-review` flags file writes that overwrite in place instead
  of write-to-temp/fsync/rename; `o11y-review` names the MELT framework
  and checks that the four signals connect into one investigative path;
  `config-review` flags a malformed config silently falling back to
  defaults; `sec-review` flags declared capabilities wider than what the
  code exercises; `api-review` flags schema-vs-implementation field
  drift; `agentrules-review` flags agents that can weaken their own
  quality gates; `fuzz-review` flags round-trip harnesses that only
  exercise synthetic inputs.

## 0.21.0

### Changed

- Five review prompts sharpened with checks ported from oxlint-standards:
  `db-review` flags DELETE/UPDATE with no predicate (a builder chain whose
  `where` is optional and absent rewrites the whole table); `error-review`
  names the concrete shapes of a fallback standing in for a failure the
  caller should have seen; `code-review` flags branch bodies that are
  identical; `slop-review` flags scratch and debug residue shipped as
  source; `lint-review` distinguishes rule categories never enabled from
  ones deliberately disabled, and prefers analyzer builtins over custom
  rules.

## 0.20.0

### Changed

- `code-review` now flags fabricated or discarded type evidence: code that
  makes a value look safer to the compiler than it is (a frequent tell of
  machine-generated code): chained/widen-then assertions, `unknown`/`any`/
  `object` concealing a real contract, ad-hoc `typeof` narrowing instead of
  boundary parsing, reflection over typed access, and unjustified casts.
  Fenced to error-review (boundary validation) and test-review (module
  mocking vs dependency seams); `oxlint` added to its tool list.
- README polish: install/run TL;DR near the top, a feature-highlights grid,
  and a cleaner pipeline diagram.

## 0.19.0

### Security

- A hostile target repo can no longer execute code in the runner process.
  Its `.git/config` could set `core.fsmonitor` (or other config-as-command
  keys) to any program, which git ran during the runner's ordinary
  read-only calls, during discovery (so even `--list`/`--dry-run`), in the
  lines-changed stats before the first agent, and in the commit step. Every
  git call now forces those keys empty and resolves the git binary on a
  cwd-independent PATH.
- `*-review.md` prompt reads are bounded: a multi-GB prompt no longer OOMs
  the runner (1 MiB cap; a single argv over ~128 KiB fails at exec anyway),
  and a planted symlink-to-FIFO no longer hangs the lines-changed stat
  unkillably. (Hardlinks are read normally: a package manager legitimately
  hardlinks the bundled prompts, and an out-of-tree hardlink in an
  untrusted repo is the trust model's container case.)

### Added

- `--show-prompt REVIEW` prints the exact composed prompt an agent would
  receive (after stripping and the auto-fix suffix; honors `--yolo` and
  `--timeout`), then exits, for debugging project-local prompts.
- `-V` as a short alias for `--version`.
- Lines-changed stats now count new (untracked) files and attribute a
  review that reverts another's lines as deletions, so the final summary
  cannot contradict the worktree.
- `--exclude` is applied before `--reviews suggest`: excluded reviews never
  reach the triage agent or the confirmation list. A set that matches
  nothing under `--exclude` (e.g. `--exclude project` in a repo with no
  project prompts) is a valid no-op.
- Commit-step outcomes are counted in the exit summary; a failed
  `--commit`/`--push` step now exits 1 (changes are stranded), and suggest
  falls back to the next agent when one exits 0 with no usable output.

### Changed

- `code-review`'s doctor tools gain `cargo-clippy`.
- A crashing `semcode-index` now exits 1 (a runtime failure after the lock),
  not 2 (usage error).
- Project-prompt discovery skips hidden directories and anything git ignores,
  and duplicate-prompt warnings fire only when the copies actually differ.
- Numerous doc corrections (the `--agents`/`--continue-sessions` option
  rows, exit-code table, "adding a review" registration requirements).
- Prompt fencing tightened: `error-review` hands off overflow/time/encoding
  to numerics/time/unicode-review; the map-used-as-cache is owned entirely
  by `cache-review`; `sec-review` gains the homoglyph and insecure-randomness
  items that unicode/numerics-review fence to it.

## 0.18.0

### Added

- Logo: two facing rows of chevrons with the code's path arrowing
  through the corridor (running the gauntlet, literally). SVG mark plus
  light/dark wordmark variants in `assets/`, shown in the README via a
  theme-aware `picture` element.
- Static-analysis posture: ruff and mypy configured in `pyproject.toml`
  and enforced in CI (pinned `ruff==0.16.1`, `mypy==1.19.0`). Ruff runs
  E/W/F/B/UP/SIM/RUF/PLE/PLW at line-length 100 with E501 and SIM105
  deferred with written reasons; mypy checks `cli.py` with strict
  equality and untyped-def checking. Fixes the tools surfaced:
  `NoReturn` on `usage_error`, two Optional-narrowing defects, a
  shadowed loop variable, and assorted cleanups.
- `container-review` now flags process state that breaks horizontal
  scaling (sessions, caches-as-truth, job progress in process memory or
  on pod-local disk), the twelve-factor stateless-process property.

### Changed

- README: centered hero with the logo and nav links, a real
  sample-session transcript, agent notes folded into a details block,
  `uv tool install` documented, the options table split into four
  task-oriented groups, exit codes as a table, and the trust model as a
  warning callout.

## 0.17.0

### Added

- Standard Python project layout: `src/gauntlet/` package with `cli.py`
  and the prompts as package data, `tests/`, and a PEP 621 `pyproject.toml`
  (hatchling). `uv tool install` / `pipx install` yield a `gauntlet`
  command; `python -m gauntlet` and running `src/gauntlet/cli.py` directly
  keep working, with zero runtime dependencies. The PyPI-style project
  name is `gauntlet-review` (bare `gauntlet` is taken); the command stays
  `gauntlet`. `VERSION` in `cli.py` remains the single source of truth
  (hatchling reads it from there).
- `## Install` section in the README: clone + symlink onto `PATH`,
  run-in-place, or release tarball.
- Release workflow: pushing a `v*` tag now auto-creates the GitHub
  release with that version's CHANGELOG section as notes. Releases had
  stalled at v0.11.0 while tags kept moving; v0.12.0 through v0.16.0 were
  backfilled by hand.

## 0.16.0

### Changed

- Project renamed to **gauntlet** (repo `maci0/gauntlet`; old
  `review-prompts` URLs redirect). `review-loop.py` is now `gauntlet.py`,
  the test file `test_gauntlet.py`, the lockfile `.gauntlet.lock`, and
  `--version` reports `gauntlet`. The `review-loop: keep` code marker is
  deliberately unchanged: it already lives in target repos' code, and
  renaming it would silently invalidate existing markers.
- README: the 50 reviews are grouped into ten collapsible domain
  categories; badges (CI, release, license, Python, zero dependencies)
  and a mermaid diagram of the review pass added.

### Fixed

- CI: the `--commit`/`--push` warning test no longer depends on agent
  CLIs being installed on the runner, and the three git `subprocess.run`
  calls pass an explicit `check=False` (ruff PLW1510). First green CI
  since 0.13.0.

## 0.15.0

### Added

- `--suggest` flag as shorthand for `--reviews suggest`; conflicts with
  `--reviews` and inherits the suggest-mode guards (`--list`/`--dry-run`
  rejected).
- `code-review` now flags shell scripts that embed another language via
  heredocs or `-c` one-liners (`python -c`, `node -e`, inline awk/perl):
  the embedded code escapes syntax checking, linting, and editor tooling.
  One language per file; call a proper script instead.

### Changed

- README intro updated to the current 50-review count and to describe the
  `--commit`/`--push` commit step alongside "review agents never commit".

## 0.14.0

### Added

- Nine new bundled reviews, each with an applicability gate and single-owner
  fencing against its neighbors:
  - `compat-review`: cross-platform portability. Paths and filesystem
    assumptions, bashisms and GNU-vs-BSD tool drift, line endings,
    endianness and word-size assumptions, glibc/musl variants, and drift
    between the claimed support matrix (README, CI, packaging) and what CI
    actually tests.
  - `time-review`: time correctness. Naive vs aware datetimes, DST and
    calendar arithmetic ("+24h means tomorrow"), wall vs monotonic clock
    choice, mixed epoch units, ambiguous parsing, cron timezones, and
    range/expiry boundary defects.
  - `numerics-review`: numeric correctness. Money in binary floats,
    rounding-mode and allocation errors, silent overflow and truncating
    casts, division and negative-modulo surprises, unit mismatches, and
    precision loss across serialization boundaries (2^53 IDs in JS).
  - `authz-review`: the authorization matrix in depth. Object-level misses
    (IDOR), function-level gaps, tenant isolation, privilege-escalation
    paths, enforcement consistency across duplicate API surfaces, indirect
    access via search/export/webhooks, and deny-side test coverage.
    sec-review keeps authn and point vulnerabilities.
  - `cache-review`: caching correctness. Invalidation completeness at
    write paths, key design (collisions, missing tenant/locale dimensions),
    staleness policy, stampede/dogpile behavior, cross-layer coherence,
    bounds, and sensitive data in shared caches. perf-review keeps
    whether to cache.
  - `resource-review`: resource lifecycle. Descriptor/socket/connection
    leaks, pool drains, zombie processes, leaked goroutines/tasks,
    listener and timer accumulation, unbounded in-memory growth, and
    lock/lease release paths. error-review keeps the error-path slice.
  - `dr-review`: durability and disaster recovery. State inventory vs
    backup coverage, restore reality (tested, loadable, orderable),
    ack-before-durable write windows, failure-domain concentration
    (backups deletable by the same credential), rollback of bad deploys,
    and RPO/RTO plus runbook existence. Takes over db-review's
    operational edges.
  - `unicode-review`: text encoding correctness. Encoding boundaries,
    NFC/NFD normalization policy, byte/code-point/grapheme length and
    truncation defects, case folding, confusables and invisible characters
    in identifiers, and lossy round-trips. i18n-review keeps locale,
    translation, and RTL.
  - `dx-review`: contributor experience. Clone-to-green-test bootstrap,
    edit-test loop speed and single-test paths, command discoverability,
    local/CI parity, contribution mechanics, and dev-path error messages.
    doc-review keeps prose accuracy; this owns the runnable path.
- Set updates: `standard` gains compat, time, numerics, resource;
  `security` gains authz; `backend` gains authz, cache, dr; `frontend`
  gains unicode; `shipping` gains dx.

### Changed

- Reciprocal fencing lines added to sec-review (authz depth), perf-review
  (cache correctness, resource lifecycle), error-review (resource
  lifecycle), i18n-review (encoding mechanics), db-review (recovery
  posture), and doc-review (runnable onboarding path), keeping every
  concern single-owner.

## 0.13.0

### Added

- Lines-changed stats: in a git repository, each review, each completed
  loop, and the final exit summary now report `+insertions/-deletions`,
  measured via `git diff --shortstat` against the commit that was `HEAD`
  when the run started. Silently omitted outside a git repo.

### Changed

- A warning is now printed when `--commit` and `--push` are both given,
  since `--push` already implies `--commit`.
- Project-local prompt discovery now skips hidden directories (`.git`,
  `.venv`, worktrees, etc.), so stray prompt copies under them (e.g. a
  `.clanker-worktrees` snapshot) no longer trigger duplicate-prompt
  warnings.

### Fixed

- `container-review` (added in 0.12.0) was missing from `doctor`'s tool
  table and from the `shipping` review set; both are corrected.

## 0.12.0

### Added

- `container-review` audits container-native readiness of Kubernetes
  workloads declared in Deployment/StatefulSet/DaemonSet manifests, Helm
  charts, and Kustomize overlays. Covers health probes (liveness, readiness,
  startup, including dependency checks in the readiness probe), graceful
  shutdown (SIGTERM handling, shell-form CMD, preStop hook,
  terminationGracePeriodSeconds), observability wiring (ServiceMonitor/
  PodMonitor presence, stdout log delivery, OTel annotation injection),
  security context (non-root, capabilities drop, seccomp, read-only root
  filesystem, fsGroup), configuration management (externalized config,
  no inline secrets), image hygiene (digest pinning, .dockerignore,
  multi-stage), resource requests/limits, resilience (PDB, anti-affinity,
  rolling update strategy, retry over init-container polling), networking
  (NetworkPolicy deny-all baseline, no hostNetwork/hostPort), and Kubernetes
  object hygiene (standard labels, dedicated ServiceAccount, RBAC scoping).
  Scoped to k8s-manifest-side concerns; infra-review owns CI/CD and
  compose wiring, o11y-review owns application instrumentation depth.

- `--commit` flag: after each review, an agent inspects the diff, writes a
  human-style commit message (no AI attribution), and commits any changes.
  Skipped when the working tree is clean.

- `--push` flag: like `--commit` but also pushes after committing. Both flags
  may be combined; the effect is the same as `--push` alone. When `--push`
  is combined with `--yolo`, the agent is also instructed to rebase and retry
  if the push is rejected due to a diverged remote.

## 0.11.0

### Changed

- Review checklists absorb TigerBeetle Tiger Style
  (https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/TIGER_STYLE.md),
  distributed into the reviews that already own each concern:
  assertion density and pairing, bounds on loops and queues,
  explicitly-sized types, ~70-line functions, push-ifs-up,
  batching and resource-order sketches, static allocation,
  programmer-vs-operating errors, exhaustive valid/invalid tests,
  zero-dependency default, strict compiler warnings and ~100-column
  measure, named arguments, and why-comments. No new review name.

## 0.10.0

### Added

- `lint-review` owns the static-analysis posture: tool coverage per
  language, configuration strictness, suppression hygiene (stale and
  unjustified ignores), typing coverage, and blocking CI enforcement. The
  analyzers' findings stay with their owning reviews (code-review, sec-review);
  this one reviews the analyzers themselves. Joins `standard`.

## 0.9.0

### Added

- `threat-review` builds and maintains a living threat model in the repo
  (`docs/THREAT_MODEL.md` plus `SECURITY.md` accuracy): attack-surface
  inventory, trust boundaries, assets, STRIDE-style threats per boundary,
  mitigations mapped to code, and abuse cases with code-level evidence. It
  writes security documentation only: vulnerabilities are recorded and
  handed to sec-review, never fixed or demonstrated here. Joins `security`.

- `specs-review` covers PRDs, ADRs, RFCs, and design docs as documents:
  drift against the implementation, ADR structure and lifecycle,
  requirement testability, traceability, and redundancy across documents
  (duplicates consolidate to one canonical copy). Decision substance stays
  with design-review, general doc prose with doc-review. Joins `standard`.
- `dsh` (DeepSeek Harness) as a supported agent, run as
  `dsh --profile headless <prompt>`. Permissions and the default model come
  from the profile's config; `dsh:<model>` pins a configured model through
  a generated `--patch` overlay (the profile's provider is probed once from
  `--dump-config`; `dsh:<provider>/<model>` sets both explicitly, since the
  overlay replaces the plugin config and the provider is required). One-shot
  mode has no resume, so
  `--continue-sessions` starts it fresh. When the launcher is not in `PATH`
  but `bunx` is, a named `--agents dsh` falls back to `bunx @deepseek-ai/dsh`
  (auto-detect and `mixed` stay PATH-based because the fallback fetches the
  package on first use).
- `vnu` (offline W3C HTML/CSS validation) as a suggested tool for
  `ux-review` and `a11y-review`; `htmlhint` and `stylelint` for `ux-review`.
- `-y, --yes` skips the `--reviews suggest` confirmation without enabling
  `--yolo`. Implied when stdin is not a terminal.
- `--quiet` is an explicit alias of `--quiet-agents`.

### Deprecated

- `--models` now warns on stderr; use `--agents`. The alias still works.

### Fixed

- `--reviews suggest` interpolated project review descriptions through
  `str.format`, so a `{placeholder}` in a "Your goal" line crashed triage
  (or, with a matching name, rewrote the template). Descriptions are now
  spliced in as data, fenced, truncated, and stripped of `RELEVANT:` markers.
- `--continue-sessions` resumed via `-c` / `--resume latest` even when two
  models of the same CLI were in the pool, mixing their sessions. Resume
  now requires that CLI to appear only once.
- A typo in `--reviews`/`--exclude` on a real run was reported as "another
  instance is running" (exit 75) when a lock was held, because names were
  validated after lock acquisition.
- `--reviews ''` (and `--reviews "$UNSET"`) ran every review. An explicit
  empty list is now a usage error.
- `--dir`, `--prompt-dir`, and `--log` now expand `~` and `$VAR`, matching
  `--bin`. A `--prompt-dir` that exists but is not a directory says so.
- `--bin` unknown-agent errors now include a "did you mean" hint.
- Agent names in `--agents`/`--bin` are case-insensitive (`CLAUDE` = `claude`).
- OSError messages no longer leak `[Errno N]`.
- `--semcode` without `semcode-index` is now a usage error before the lock
  is taken, so a held lock reports the missing tool (exit 2) instead of
  "another instance is running" (75). `--dry-run --semcode` warns when the
  indexer is missing.
- `--yolo` is printed on `--dry-run` and logged at the start of a real run.
- SIGTERM during `--reviews suggest` or `--semcode` no longer orphans the
  `start_new_session` child (a permission-bypassed agent, or an indexer
  still writing `.semcode.db` after the lock is released). Both paths now
  kill the process group and reap with a timeout, matching the review loop.

### Changed

- Long-option prefixes are no longer accepted (`--dry` is not `--dry-run`).
  Spell the flag in full; short forms (`-q`, `-l`, ...) are unchanged.
- Review bodies are wrapped in `BEGIN/END REVIEW` markers so a project-local
  prompt cannot blend into the containment suffix.
- Suggest triage is capped at 5 minutes (`SUGGEST_TIMEOUT_CAP`) and falls
  back to the next `--agents` entry if the first launch or run fails.
- A review that fails (launch or non-zero exit, not timeout) is retried on
  another agent from `--agents` when one remains, so a single provider
  outage does not burn the slot.

## 0.8.0

### Changed

- `--reviews suggest` with `--yolo` skips the confirmation prompt; the picked
  reviews and their reasons are still printed.

## 0.7.0

### Added

- `--reviews suggest`: one agent from `--agents` inspects the repo against
  the review catalog (names and descriptions only, never prompt bodies, under
  a classification-only rule), lists the relevant reviews with a reason each,
  asks for confirmation on a terminal (non-interactive runs proceed), then
  the loop runs exactly those. Composable with `--exclude`, not with other
  `--reviews` values or `--list`/`--dry-run`.

### Changed

- Review sets now cover every bundled prompt (enforced by a test):
  `design-review` joins `standard`, `mobile-review` joins `frontend`, and
  `sdk-review` and `infra-review` join `shipping`.

## 0.6.0

### Added

- Short flags for the common options: `-a` (`--agents`), `-r` (`--reviews`),
  `-x` (`--exclude`), `-t` (`--timeout`), `-n` (`--max-loops`), `-C` (`--dir`),
  `-q` (`--quiet-agents`), `-l` (`--list`).
- The `-review` suffix may be omitted in `--reviews`/`--exclude`: `sec` means
  `sec-review`. Sets and exact names take precedence over the expansion.
- Unknown review, set, and agent names now come with a "did you mean" hint.

### Changed

- `--agents`, `--reviews`, and `--exclude` are repeatable, matching `--bin`:
  `--reviews sec-review --reviews code-review` merges like one comma list
  (repeats still count as weight for `--reviews`).
- `--help` groups the options (modes, review selection, agent selection,
  execution, output) and uses descriptive metavars (`DURATION`, `N`, `FILE`,
  `LIST`).
- `doctor`, `--list`, and `--dry-run` now reject being combined instead of
  silently picking one.
- `--log` works in every mode; it was silently ignored with `doctor`,
  `--list`, and `--dry-run`. A relative FILE is now resolved against the
  invocation directory (like `--prompt-dir`), not the `--dir` target.
- `--help` documents the exit codes.

## 0.5.0

### Changed

- `--reviews` treats repetition as weight: naming a review or a set more than
  once schedules it that many times per loop, so `all,sec-review,sec-review`
  runs everything once and security three times. `--list` shows weighted
  reviews as `×N` and `--exclude` still removes a name outright. Repeated
  names previously collapsed, so `--reviews quick,code-review` now schedules
  code-review twice rather than once.

## 0.4.0

### Added

- clanker as a supported agent (`clanker run <prompt>`). Its model and
  permissions come from its own config, and resuming requires an explicit
  session id, so it takes no `tool:model` form and is not eligible for
  `--continue-sessions`. It loads config from the working directory only, so
  it can review just the repository holding that config, and is therefore
  opt-in: auto-detect and `mixed` skip it, and it must be named explicitly.
- `webperf-review` covers web delivery and first paint: compression
  negotiation and effort matched to cacheability, the critical path and the
  initial congestion window, loading strategy and split-chunk failure, caching
  and revalidation headers, payload shape, main-thread responsiveness,
  third-party weight, protocol-era workarounds, measurement, and budgets in
  CI. perf-review keeps server and runtime performance and now defers browser
  delivery to it; the `frontend` set swaps perf for webperf.
- `--yolo` swaps the caution half of the injected rules for an ambitious one:
  no fix count or diff-size cap, public APIs and structure may change, and an
  agent should build missing groundwork rather than decline the work.
  Containment, the ban on touching your uncommitted changes, the
  `review-loop: keep` marker, and the baseline-then-verify step are unchanged.
  It also removes deference: no waiting for sign-off, report-only instructions
  in a review body are superseded, and a broken build or plain bug is in scope
  even when unrelated to the review's topic.
- `--bin TOOL=PATH` runs an agent from a chosen executable instead of `PATH`,
  for wrappers and alternate builds. Repeatable, one per agent, with `~` and
  `$VAR` expanded because shells leave them alone after `=`.

### Fixed

- Ctrl+C could stop working entirely during a review. Agents inherited the
  runner's stdin, and an agent that puts the terminal in raw mode with ISIG
  disabled (clanker does this for line editing) suppresses signal generation
  for itself and for the runner. Agents now run with stdin closed, and the
  runner restores terminal settings after every review.

## 0.3.0

### Added

- opencode as a supported agent (`opencode run --auto`), bringing the total to
  nine: claude, gemini, qwen, codex, grok, agy, cursor-agent, kimi, opencode.

### Fixed

- `--continue-sessions` placed its resume flag next to the binary, which is
  wrong for agents invoked as `binary subcommand ...`; the flag now follows the
  subcommand. No shipped agent was affected, but opencode would have been.

## 0.2.0

### Added

- Four reviews for repos that ship agent instructions or need re-execution
  safety: `prompt-review` (review prompts as agent instructions),
  `skills-review` (SKILL.md packages), `agentrules-review`
  (CLAUDE.md/AGENTS.md/.cursorrules), and `idempotency-review` (retries,
  at-least-once delivery, reruns, crash recovery). 32 prompts to 36.
- Review sets for `--reviews`/`--exclude`: `all`, `project`, `quick`,
  `standard`, `security`, `frontend`, `backend`, `agents`, `shipping`. They
  compose with plain review names, and `--list` prints their members.
- Kimi Code (`kimi`) as a supported agent.
- `--quiet-agents` discards agent stdout/stderr for agents that narrate every
  step.
- `--continue-sessions` resumes each agent's own session after its first run so
  already-read context is reused. Off by default: contexts bleed between
  reviews and history is resent every turn.
- `--semcode` builds a semcode index of the target before the loop so reviews
  answer call-graph and type queries from the index.
- SBOM, provenance, and registry-attack coverage in `deps-review`
  (dependency confusion, typosquats, hash pinning, `syft`/`grype`/`cosign`).

### Changed

- The injected rule suffix is rewritten around containment: repo content is
  data rather than instructions, git is read-only, no installs or writes
  outside the tree, nothing may outlive the run, and agents must undo bad
  edits by re-editing rather than by git revert (the tree may hold your
  uncommitted work). Agents now learn their wall-clock budget and end with a
  machine-readable `RESULT:` line. A `review-loop: keep` comment marks code
  every agent must leave alone.
- Prompts are composed for auto-fix at dispatch: report-only sections are
  stripped, which drops roughly a third of each prompt that the suffix
  overrode anyway. The `.md` files stay complete for standalone use.
- Reviews fence each other explicitly, so two agents no longer fix the same
  code by different rules.

### Fixed

- `kimi` was invoked with `--auto -p`, which its prompt mode rejects; every
  kimi review failed instantly.
- Prompt files are opened `O_NOFOLLOW` and non-blocking, closing a symlink
  swap window and a FIFO that could hang discovery forever.
- The timeout and signal paths kill the agent before logging, so a dead `tee`
  can no longer leave a permission-bypassed agent running orphaned.
- A signal during a review now shortens the wait to ten seconds instead of
  blocking for the full timeout against an agent that traps SIGTERM.
- Section stripping fails open: a prompt whose report marker is never closed
  keeps its text instead of being truncated to the marker.

## 0.1.0

First tagged release: 32 review prompts, the runner with `doctor`, project-local
prompt discovery, the flock lockfile and exit-code contract, tests, and CI.
The bundled reviews (names are API): `a11y-review`, `api-review`,
`arch-review`, `build-review`, `cli-review`, `code-review`,
`concurrency-review`, `config-review`, `db-review`, `deps-review`,
`design-review`, `doc-review`, `dst-review`, `error-review`,
`functionality-review`, `fuzz-review`, `i18n-review`, `infra-review`,
`llm-review`, `minimalism-review`, `mobile-review`, `o11y-review`,
`perf-review`, `pkg-review`, `privacy-review`, `release-review`,
`sdk-review`, `sec-review`, `slop-review`, `test-review`, `uislop-review`,
`ux-review`.
