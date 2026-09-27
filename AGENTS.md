# gauntlet: project conventions

The Go implementation of gauntlet, dispatching bundled review prompts to installed AI
coding agents, which apply fixes to the working tree.
`TAGS=notoktop` drops transcript reading; `TAGS=` keeps transcript reading
but drops the sqlite driver.

## Build and test

- `make check`: checks formatting without rewriting; `go fix -diff` and vet
  under `sqlite`, bare, and `notoktop` tags. Run `make fmt` to fix formatting;
  apply reported Go fixes under the same three tag sets before committing.
- `make ci`: `make check` across all three tag sets, then `make test` for
  the selected `TAGS` (default `sqlite`). The GitHub workflow
  (`.github/workflows/ci.yml`) is wider: bare and `notoktop` builds on Linux
  and macOS, plus coverage, dist, and reproducibility.
- `make verify`: `make check`, `make check-scripts`, then the suite under all
  three tag sets, the whole pull-request gate in one command. `make ci` is the
  edit-test loop, this is the before-push gate.
- `make check-scripts`: ruff and mypy `--strict` via version-pinned `uvx`,
  plus shellcheck from PATH on `scripts/`, and yamllint `--strict` on
  `.github/`. Rule selection is `pyproject.toml` for the Python
  tools and `.yamllint` for the YAML. shellcheck is the one tool CI
  cannot install a pinned copy of, so its pin is a warning on drift
  rather than a lock.
  Run `make fmt-scripts` to rewrite scripts with ruff format.
- `make test RUN=TestName`: the suite with the race detector and shuffled order.
- `make test-pkg PKG=./internal/prompt [RUN=TestName]`: one package or test
  with the same race, shuffle, and tag flags.
- `make cover`: the same suite with a coverage profile, gated by `COVER_MIN`
  in the Makefile. The floor is CI's number: a machine with agent CLIs
  installed reads about two points high. Raise it when CI reports higher,
  never lower it to make a change fit.
- Tests must not write into a tmpfs or into a gitignored path inside this
  repo: the prompt discovery tests would then see their own fixtures as
  ignored. `TMPDIR` is set by the Makefile for that reason, and an exported
  `TMPDIR` in the environment is ignored on purpose; override it on the make
  command line.
- The Makefile, the workflows, and the tool pins in `scripts/` are pinned by
  tests in `cmd/gauntlet/` (`makefile_test.go`, `ci_test.go`, `deps_test.go`):
  change one without the other and `make test` fails.
- User-visible changes land in `CHANGELOG.md` under `## Unreleased` in the
  same change (each impact heading `Added`, `Changed`, `Deprecated`, `Removed`,
  `Fixed`, `Security` at most once per version). `## Unreleased` may not carry
  `Removed`: removals are breaking and wait for a major release, and
  `cmd/gauntlet/changelog_test.go` fails the suite on one. Internal refactors
  with no visible behavior need nothing.

## Layout

`cmd/gauntlet` is flags and dispatch. Everything real lives in `internal/`.
No package inside `internal/` imports `ui`, so a headless run costs nothing.
`docs/DESIGN.md` is the map. A new bundled review is
`internal/prompt/prompts/NAME-review.md` plus its stem in `goldenReviewNames`
(`internal/prompt/contract_test.go`) and documented in `CHANGELOG.md`; review
names are API.

## Tree conventions

- Every `.go` file opens with the two-line header
  (`// Copyright (C) 2026 Marcel W. Wysocki`, then
  `// SPDX-License-Identifier: AGPL-3.0-or-later`). Text is LF
  (`.gitattributes`), and the runner needs POSIX process groups, `flock`,
  `O_NOFOLLOW`, and `execve`, so Linux and macOS only.
- A direct module in `go.mod` must be a tagged release, imported by a non-test
  file, allowed for its import sites in `directModuleSites`, licensed MIT or
  BSD-3-Clause, and named in the linked-module table in `docs/DESIGN.md`:
  `cmd/gauntlet/deps_test.go` fails on any of the five.

## Rules that are not style preferences

- **Concurrency in one repository requires isolation.** Reviews run
  sequentially in place, as `--jobs N` persistent lane worktrees with a
  merge step, or as a stacked-PR pass (`--stacked-prs`) that advances one
  isolated worktree sequentially (a later `--max-loops` round cuts a new
  worktree from the previous tip). Those are the only modes, and every one
  keeps the invariant: no flag lets two agents share a tree.
- **Never fake data in the dashboard.** Missing is missing (`n/a`, `~`), an
  unlit meter shows its remainder, and no series is smoothed or interpolated.
  See `docs/DESIGN.md`.
- **Containment is prompt text plus process discipline.** Changing the rule
  files in `internal/prompt/rules/` changes what agents are allowed to do:
  treat those files as security-relevant.
- **Untrusted input** is anything from the reviewed repository: prompt names,
  descriptions, agent output. Sanitize before display, never interpolate into
  a prompt without fencing.
- **Nothing git-visible names this tool.** Commit subjects, merge messages, and
  PR titles and bodies say what changed, never "gauntlet", the review pass, or
  the automation behind it. Write "the CLI", "the dashboard", or the package.
  `internal/prompt/rules/commit.md` states the rule for agents,
  `internal/runner/runner_test.go` pins it for a reviewed repository, and it
  holds for commits written by hand here too. Two exceptions. A literal
  identifier the message is about: `GAUNTLET_HOME`, `gauntlet pick`,
  `.gauntlet.lock`. And the refs the runner cuts for itself (`AddWorktree`,
  `AddStackWorktree`), namespaced `gauntlet/...` and `review/...` so a reviewed
  repository can list and delete them as a set. That is why a stacked PR body
  names its base branch: the reader has to check it out, and GitHub prints it
  above the diff either way.
- **A conflicting merge is resolved or keeps its branch.** The conflict step
  may hand it to an agent in a scratch checkout; what comes back unresolved
  stays on its branch. Losing a review's entire output silently is worse than
  a noisy failure.

## Docs

- `uv run scripts/suggest-calibrate.py` scores the file-signal suggester
  against what agents picked in past runs, read from the journal. Run it before
  and after touching `internal/runner/suggest_fast.go`: the rules are judged by
  that number, not by how sensible they read.
- `assets/dashboard.png` and `assets/launcher.png` are the README's
  screenshots, regenerated by `scripts/shots.sh` from the renderer's own
  output (`internal/ui/shots_test.go`, the only writer). Refresh them when the
  screen changes shape; the script needs uv, chromium, and ImageMagick, and
  nothing else in the build reads them.
- `docs/THREAT_MODEL.md` carries a `Last reviewed: <date> against commit
  <sha>` stamp, and its `Makefile:<line>` pointers are checked against the
  named target by `cmd/gauntlet/makefile_test.go`. Re-read the controls and
  move the stamp when a change adds or removes a surface; one commit past the
  stamp is the claim going stale.
- `README.md` is the landing page: keep it short; detail belongs in `docs/`.
- A new flag is documented in `docs/CLI.md`, the help table in
  `cmd/gauntlet/help.go`, `CHANGELOG.md`, and `goldenFlagNames`
  (`cmd/gauntlet/contract_test.go`); flags are API.
- `docs/IDEAS.md` records what was deliberately not built, and why. Move an
  entry out of it when it ships; do not leave both.
