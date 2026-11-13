# gauntlet: project conventions

The Go implementation of gauntlet, dispatching bundled review prompts to installed AI
coding agents, which apply fixes to the working tree.
`TAGS=notoktop` drops transcript reading; `TAGS=` keeps transcript reading
but drops the sqlite driver.

## Build and test

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and the release workflow.

- `make doctor` reports all missing prerequisites before failing: Go, a C
  compiler, git, uvx, shellcheck, tar, cmp, install, a checksum tool, the test
  scratch directory, and macOS's `/usr/bin/sandbox-exec`. Add any new target
  prerequisite to this check in the same change.
- `make check` requires an empty `go mod tidy -diff`, checks formatting without
  rewriting, then runs `go fix -diff`, vet, and staticcheck under `sqlite`,
  bare, and `notoktop` tags. Use `make fmt` for formatting; apply Go fixes under
  the tag set that reported them. Staticcheck is fetched with `go run` at
  `STATICCHECK_VERSION`, pinned only in the Makefile.
- `make ci` runs `make check` and `make test` for the selected `TAGS` (default
  `sqlite`). CI also tests all three tag sets on Linux and macOS, scripts,
  coverage, release artifacts, smoke checks, and reproducibility.
- `make verify` runs `make check`, `make check-scripts`, and tests under all
  three tag sets. It excludes coverage and the release checks.
- `make check-scripts` runs version-pinned ruff, mypy `--strict` (with rich's
  type information), and yamllint `--strict` through uvx, plus shellcheck from
  PATH. Rules live in `pyproject.toml` and `.yamllint`. Shellcheck version drift
  warns rather than fails. `make fmt-scripts` applies ruff formatting.
- `make check-workflow-shell`, also part of `check-scripts`, extracts workflow
  `run: |` bodies under `TMPDIR` and shellchecks them. Finding no bodies fails;
  a new workflow block needs no Makefile change.
- `make test RUN=TestName` runs the suite with race detection and shuffled order.
  `make test-pkg PKG=./internal/prompt [RUN=TestName]` selects one package;
  `make test-fast PKG=...` omits race detection for the edit loop.
- `make cover` enforces `COVER_MIN`. Local machines with agent CLIs installed
  can read about two points above CI. Raise the floor when CI supports it;
  never lower it to fit a change.
- Run `make dist`, `make artifacts`, `make smoke`, and `make repro` when changing
  release recipes. Artifacts require the exact Go release (`make toolchain`),
  rather than the language minimum accepted by `make check`. Repro builds
  twice from separate trees; `make release` also requires `make clean-tree`.
- Tests must use a disk-backed scratch directory outside this repository's
  ignored paths, or prompt discovery sees its own fixtures as ignored.
  The Makefile sets `TMPDIR` and ignores an exported value; override it on the
  make command line.
- Makefile recipes, workflow behavior, and tool pins are held by
  `cmd/gauntlet/{makefile,ci,deps}_test.go`. Update their contracts with changes.
- Record user-visible changes under `## Unreleased` in `CHANGELOG.md`. Each
  impact heading (`Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`,
  `Security`) appears at most once per version. `Removed` is forbidden under
  Unreleased: removals are breaking and wait for a major release. Internal
  refactors without visible behavior need no entry.

## Layout

`cmd/gauntlet` is flags and dispatch, and `cmd/sbom` is the release-time
command behind the `sbom.json` every release ships. Implementation packages live in
`internal/`.
No package inside `internal/` imports `ui`, so headless runs do not depend on the dashboard.
The layering is pinned in `allowedInternalImports`
(`cmd/gauntlet/layout_test.go`): a new internal package or a new edge between
two of them lands there and in `docs/DESIGN.md` in the same change, or
`make test` fails on an undeclared package.
`docs/DESIGN.md` is the map. A new bundled review is
`internal/prompt/prompts/NAME-review.md`, whose `Summary:` line is the README
grid cell, plus its stem in `goldenReviewNames`
(`internal/prompt/contract_test.go`) and documented in `CHANGELOG.md`; review
names are API. The set it belongs to is `internal/prompt/sets.go`, and a new
set name is pinned in `goldenSetNames` beside it. The grid lives between the
`BEGIN REVIEWS` and `END REVIEWS` markers in `README.md`, and its test prints
the finished block to paste back, so no cell is transcribed by hand.

## Tree conventions

- Every `.go` file opens with the two-line header
  (`// Copyright (C) 2026 Marcel W. Wysocki`, then
  `// SPDX-License-Identifier: AGPL-3.0-or-later`), which
  `TestEveryGoFileCarriesTheLicenseHeader` holds every file to, the way
  ruff's `CPY` rule holds the scripts. Text is LF
  (`.gitattributes`), and the runner needs POSIX process groups, `flock`,
  `O_NOFOLLOW`, and `execve`, so Linux and macOS only.
- A direct module in `go.mod` must be a tagged release, imported by a non-test
  file, allowed for its import sites in `directModuleSites`, licensed MIT or
  BSD-3-Clause, and named in the tables under `## External dependencies` in
  `docs/DESIGN.md`: `cmd/gauntlet/deps_test.go` fails on any of the five.

## Rules that are not style preferences

- **Concurrency in one repository requires isolation.** Reviews run
  sequentially in place, as `--jobs N` persistent lane worktrees with a
  merge step, or as a stacked-PR pass (`--stacked-prs`) that advances one
  isolated worktree sequentially. Later `--max-loops` rounds continue from
  the last pushed tip, reusing a retained checkout or creating a new one.
  Those are the only modes, and every one
  keeps the invariant: no flag lets two agents share a tree. The per-review
  and lane checkouts live under `.gauntlet/worktrees` in the reviewed
  repository, gitignored, and a run cuts no other checkout. The one exception
  is the base snapshot a stacked run reads prompts from
  (`AddSnapshotWorktree`, cut in `cmd/gauntlet/stack_preflight.go`): same
  root, detached at the fetched base, so an uncommitted or local-only
  `*-review.md` cannot steer a run that publishes remote-based work. The
  other paths it writes are `.gauntlet.lock` at the reviewed tree's root and
  the journal under `GAUNTLET_HOME` (`~/.gauntlet/runs` by default).
- **Never fake data in the dashboard.** Missing is missing (`n/a`, `~`), an
  unlit meter shows its remainder, and no series is smoothed or interpolated.
- **Containment is prompt text plus process discipline.** Changing the rule
  files in `internal/prompt/rules/` changes what agents are allowed to do:
  treat those files as security-relevant.
- **Untrusted input** is anything from the reviewed repository: prompt names,
  descriptions, agent output. Sanitize before display, never interpolate into
  a prompt without fencing.
- **Nothing git-visible names this tool.** Commit subjects, merge messages, and
  PR titles and bodies say what changed, never "gauntlet", the review pass, or
  the automation behind it. Write "the CLI", "the dashboard", or the package.
  `internal/prompt/rules/commit.md` states it for agents,
  `internal/runner/subject.go` drops an agent subject that credits a model
  and writes one from the changed files instead,
  `internal/runner/subject_test.go` pins that, and it holds for commits
  written by hand here too. Two exceptions. A literal identifier the message is
  about: `GAUNTLET_HOME`, `gauntlet pick`, `.gauntlet.lock`. And the refs the
  runner cuts for itself (`AddWorktree`, `AddStackWorktree`), namespaced
  `gauntlet/...` and `review/...` so a reviewed repository can list and delete
  them as a set. That is why a stacked PR body names its base branch: the
  reader has to check it out, and GitHub prints it above the diff either way.
- **A conflicting merge is resolved or keeps its branch.** The conflict step
  may hand it to an agent in a scratch checkout; what comes back unresolved
  stays on its branch. Losing a review's entire output silently is worse than
  a noisy failure.

## Docs

- `uv run scripts/suggest-calibrate.py` scores the file-signal suggester
  against what `--suggest-agent gauntlet` picked in past runs, read from the
  journal under `GAUNTLET_HOME`; a machine that has run none exits with
  `no --suggest runs recorded yet` rather than a number. Run it before and
  after touching `internal/evidence/suggest.go`, and read recall and precision
  as movement between runs of the script, not as a grade: the agent's pick is a
  reference, not ground truth. `--detail` prints the per-directory misses. The
  script builds through `make build` and keeps its binary under `.scratch/`,
  gitignored, rather than the system temp dir.
- `assets/dashboard.png` and `assets/launcher.png` are the README's
  screenshots, regenerated by `scripts/shots.sh` from the renderer's own
  output: `internal/ui/shots_test.go` writes the `.ansi` frames and nothing
  else does, and the script exports and rasterizes them. Refresh them when
  the screen changes shape; the script needs uv, chromium, and ImageMagick,
  and nothing else in the build reads them.
- `docs/THREAT_MODEL.md` carries a `Last reviewed: <date> against commit
  <sha>` stamp. Its `Makefile:<line>` pointers are checked against the named
  target by `cmd/gauntlet/makefile_test.go`; other source pointers are checked
  by `cmd/gauntlet/threatmodel_test.go`. Re-read the controls and update the
  stamp when a change adds or removes a surface.
- `README.md` is the landing page: keep it short; detail belongs in `docs/`.
- A new flag is documented in `docs/CLI.md`, the help table in
  `cmd/gauntlet/help.go`, `CHANGELOG.md`, and `goldenFlagNames`
  (`cmd/gauntlet/contract_test.go`); flags are API. A new environment
  variable needs `docs/CLI.md`, the `helpEnvVars` table in `help.go`, a
  `.env.example` entry, `goldenEnvVars`, and `CHANGELOG.md`; the contract
  test reads both directions, so a leftover template name also fails.
- `docs/IDEAS.md` records what was deliberately not built, and why. Move an
  entry out of it when it ships; do not leave both.
