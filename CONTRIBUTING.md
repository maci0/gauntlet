# Contributing

The short version: `make verify` must pass before you push. That is `make check`,
`make check-scripts`, and the suite under all three build-tag configurations,
which is everything CI runs on a pull request except the coverage, dist, and
reproducibility legs. `make ci` is the Go-only edit-test loop, and it does not
run the scripts lint, so a Python or shell change can be green there and red in
the `scripts` job.

## Prerequisites

On a new machine, start here:

```sh
make doctor            # every prerequisite below, checked in one run
```

It reports each missing tool, what to install it with, and which targets need
it, and it reports all of them before it fails, so one run answers the whole
question instead of one tool per loop. It exits 0 when nothing is missing. The
individual checks it folds in also fire on their own, which is why an older Go
fails `make build` and `make test` directly, and why `make check-scripts` names
`uvx` and shellcheck itself. The rest no other target preflights: `tar` and
`cmp` for `repro`, `install` for `make install`, and a checksum tool for
`artifacts`.

- Go. The minimum version is the `go` line in [go.mod](go.mod); any newer
  toolchain builds, tests, and formats the tree. `make build`, `make check`,
  and the test targets preflight that minimum and say what to install when
  the local Go is older, rather than letting the go command fail on
  `GOTOOLCHAIN=local`, which the Makefile sets. `make dist` and `make repro`
  are the exception: release artifacts are built with the exact release named
  by `GO_VERSION` in the [Makefile](Makefile), which is what every CI job
  installs, and they refuse anything else. A deliberate toolchain bump edits
  that one line, and `make dist GO_VERSION=<x.y.z>` overrides it without it.
- GNU make and git, on Linux or macOS. The runner depends on POSIX semantics
  (process groups, flock, O_NOFOLLOW), so there is no Windows build.
- A working C compiler for the race detector used by `make test`, `make
  test-pkg`, `make cover`, and `make ci`: GCC or Clang on Linux, or the Xcode
  Command Line Tools on macOS. The test targets enable cgo (`CGO_ENABLED=1`)
  automatically and preflight that a C compiler is present on `PATH`.
  `make build` disables cgo and does not require a C compiler.
- `uvx` (shipped with [uv](https://docs.astral.sh/uv/getting-started/installation/))
  and shellcheck on `PATH`, for `make check-scripts` and therefore for
  `make verify`. Nothing in the Go half of the tree needs either. macOS ships
  no shellcheck: install it with `brew install shellcheck`. `check-scripts`
  preflights both and names what to install.

## Build and test

```sh
make build            # ./gauntlet for this host
make test             # every package, race detector, shuffled order
```

`make test` is a gate, not a loop: on a Linux box with the race detector and
a warm build cache it measured 6m18, and `internal/runner` alone was 373s of
it (`internal/journal` 53s, everything else under 25s). The first run on a
fresh clone adds the compile of every package under the race detector on top.
Use `make test-pkg` below while you work.

The first run downloads Go modules; after that the loop is offline.
`make` passes `-mod=readonly` on every target except two. `make vuln` clears
it, because govulncheck is not a build input, and `make tidy` sets
`-mod=mod`, because computing the answer is its job. An inherited `GOFLAGS`
is replaced rather than appended to, so it cannot add a `-tags` or `-ldflags`
of its own; a command line still wins, which is how `make test GOFLAGS=-v`
works. So a drift from go.sum
fails the command instead of rewriting the lockfile, and the one target that
may rewrite it only reports the diff. Change modules with `go get` /
`go mod tidy`, not as a side effect of the build.
`gofmt` is the one from `$(go env GOROOT)`, not whatever happens to be first on
`PATH`.

For the edit-test loop, run one package or one test instead of the suite:

```sh
make test RUN=TestStripReportSections            # one test anywhere in the tree
make test-pkg PKG=./internal/prompt              # one package
make test-pkg PKG=./internal/prompt RUN=TestStripReportSections   # one test in package
```

`test-pkg` uses the same tags, race detector, and temp directory as
`make test`, so a green loop stays green in the full run. It takes the
package it is named for and refuses to run without one; `PKG` applies to it
alone, since `make test` always runs the whole tree, and `make test PKG=...`
refuses rather than dropping the package and running the tree under the race
detector. A
`RUN=` pattern that matches no test is an error rather than a pass, so a
mistyped name cannot read as a green loop; the error names the pattern and
the command that lists the real ones.

The race detector is most of what `test-pkg` costs, and on the slowest
package it is the whole of the wait. When you are between two edits rather
than about to push, `test-fast` is the same run without it:

```sh
make test-fast PKG=./internal/runner                # one package, no race detector
make test-fast PKG=./internal/runner RUN=TestName   # one test in it
```

It is the one target that is a loop and not a check: it drops `-race` and
keeps everything else, including `-tags sqlite` and the shuffled order, so a
green `test-fast` is the build `test-pkg` would have run. Run `make test-pkg`
on the package before you push, because nothing in the fast target looks for
the interleavings the race detector finds. Reach for plain `go test` instead
and the tags go with it: that is the no-database build, not the default one,
so the loop stops matching what ships.

Tests must not write into a tmpfs or into an ignored path inside this repo:
the prompt discovery tests would otherwise see their own fixtures as
ignored. The Makefile points `TMPDIR` at `~/.cache/gauntlet/test` for that
reason; leave it alone unless you know better. An exported `TMPDIR` in your
shell is ignored on purpose, since the usual one is the tmpfs this avoids;
point TMPDIR somewhere else on the make command line
(`make test TMPDIR=...`) when you need to. A read-only home, a cache directory
another user left behind, or a sandbox that denies the write all leave a
directory that exists and cannot be written to, which is what `test-tmpdir`
and `make doctor` probe for: the test targets refuse it there, with the path
and the override, rather than failing later inside the go command.

## Checks a pull request must pass

```sh
make ci               # make check && make test
```

For the gate rather than the loop, one command runs the analysis, the scripts
lint, and the suite under all three tag sets:

```sh
make verify           # check, check-scripts, and the suite under all three tag sets
```

It is minutes rather than seconds. A pull request also runs `make cover`,
`make artifacts` (which builds `make dist` first), `make smoke`, and
`make repro`, which `make verify` leaves out on purpose; run those when the
change touches the release path or removes tested code.

`make check` is `go mod tidy -diff`, gofmt, `go fix`, vet, and staticcheck across
all three tag configurations CI tests (default `sqlite`, bare, and
`notoktop`). It mirrors ci.yml's first step exactly: if `make check` is green
locally, that step is green there. A `tidy` diff means go.mod or go.sum no
longer says what the module graph resolves to, so a require is missing, stale,
or a leftover: run `go mod tidy` and commit the result. staticcheck is fetched
at the pinned `STATICCHECK_VERSION` with `go run`, the way `make vuln` fetches
govulncheck, so there is nothing to install, and it runs with `-checks=all`
rather than the tool's default set: the tree passes every check the analyzer
carries, so the style and quickfix groups run too, and a rule a later release
adds fails the gate instead of staying off. `make staticcheck TAGS=...` runs
one tag set on its own. The
pull request template
([.github/pull_request_template.md](.github/pull_request_template.md))
restates this list as a checklist.

`make check` reports what to do with each finding, and each one has a target
that fixes it rather than a command to work out:

```sh
make fmt     # gofmt -s -w over every package directory, for "needs gofmt:"
```

`go fix` is reported the same way: the diff it prints names the exact
invocation without `-diff` that applies it, under the tag set the diff came
from (`go fix -tags sqlite ./...`, `go fix ./...`, and
`go fix -tags notoktop ./...` for the three legs). Apply it, then run
`make check` again.

CI additionally runs the full suite under each tag configuration, then
`make dist` and `make repro`. Reproduce the other two matrix legs locally
with `make test TAGS=notoktop` and `make test TAGS=` when your change
touches tagged files; you do not need dist or repro unless you touched the
release path. CI also runs `make cover` with a coverage floor (`COVER_MIN`
in the Makefile); run `make cover` locally before pushing a change that
removes tested code paths.

A separate `scripts` job lints `scripts/` with ruff (rules in
[pyproject.toml](pyproject.toml)), mypy `--strict`, and shellcheck on
`scripts/shots.sh` and on the shell inside the workflows, and lints the YAML
in [.github/](.github) with yamllint `--strict` (rules in
[.yamllint](.yamllint)). `make check-scripts` runs those same steps with
the versions CI pins. The workflow half is `make check-workflow-shell`: each
`run:` body is written out under `TMPDIR` and given to the same shellcheck,
because the shell a tag runs is no better covered than a script is. It needs `uvx` (shipped with
[uv](https://docs.astral.sh/uv/getting-started/installation/)) and
shellcheck on PATH; macOS ships no shellcheck, so install it with
`brew install shellcheck` before running the gate there. Shellcheck is the one
tool the runner image supplies
rather than `uvx` installing, so a local copy whose version differs from
`SHELLCHECK_VERSION` gets a note instead of a failure. `make fmt-scripts`
rewrites scripts with ruff format.

Three things in the loop reach the network, and all three do it on first use
only: the first `go` command downloads the module graph, `check-scripts`
downloads its four pinned tools through `uvx`, and `make vuln` downloads
govulncheck (then reads the advisory database on every run). Once those are
warm, the rest of the Makefile is offline.

A maintainer can also repeat the gate by hand from the Actions tab:
`ci` and `vulnscan` both take `workflow_dispatch`, so a runner-image or
package-index incident is answered by re-running rather than by pushing an
empty commit. Neither manual run cancels the push it repeats; each workflow
puts the event in its concurrency group for that reason.

Pull requests that touch `go.mod` or `go.sum` additionally run govulncheck,
the advisory scan of the dependency graph
([vulnscan.yml](.github/workflows/vulnscan.yml)). `make vuln` runs the same
scan locally; run it before pushing a dependency bump rather than learning
about it from a red check.

## Maintainer scripts

`scripts/` is not part of the build, and only its lint runs on a pull
request. `make check-scripts` holds every file in it to the same rules CI
does, but the two entry points below are run by hand, and neither has a
`make` target.

The README screenshots (`assets/dashboard.png`, `assets/launcher.png`) are
generated, and the command that regenerates them is not `make`:

```sh
./scripts/shots.sh
```

It needs a Chromium-based browser and ImageMagick on top of `uv`, and the
scripts job in CI installs only uv, so this is a maintainer task: the
checked-in PNGs are what a clone gets. It also needs the two faces
`scripts/shots/render.py` names, DejaVu Sans Mono and MesloLGS Nerd Font
Mono, because the browser resolves that list and nothing else. macOS ships
neither, so on a Mac the chain falls through to the generic `monospace`,
which has no braille and no block elements, and Chromium substitutes those
glyphs at different advance widths: the picture it writes is a valid render
of the same frame, and not the one that is checked in. The script cannot
detect that from the outside, so check before committing:

```sh
fc-match 'DejaVu Sans Mono'
fc-match 'MesloLGS Nerd Font Mono'
```

Each must print the family it was asked for rather than a substitute. The
frames being rendered come from the renderer's own output:
`internal/ui/shots_test.go` writes the ANSI frames into `.scratch/shots`,
`scripts/shots/render.py` exports the SVG from them, and the browser
rasterizes that. Nothing else writes those files, so a change to the
dashboard or the launcher that does not regenerate them leaves the README
showing the old screen. Refresh them in the same change when the screen
changes shape, and render them where the faces are.

The file-signal suggester is scored against what agents actually picked in
past runs, read from the journal:

```sh
uv run scripts/suggest-calibrate.py --detail
```

Run it before and after touching `internal/evidence/suggest.go`; read
the numbers as movement between runs rather than as a grade, because the
agent's pick is a reference and not ground truth.

## Changelog

User-visible changes land in [CHANGELOG.md](CHANGELOG.md) under `##
Unreleased` in the same change that makes them. Each impact heading
(`Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`) appears
at most once per version: add another bullet, not another heading. The
release workflow turns that section into the release notes and fails a tag
push that has no matching `## <version>` section, so a fix or feature
without an entry is found after push rather than before it. Internal
refactors with no visible behavior need nothing.

## Releasing

A release is a tag push; nothing else. Move the `## Unreleased` heading down
to `## <version>` in CHANGELOG.md and put an empty `## Unreleased` back
above it, so the released section keeps the entries it already had and the
next change has somewhere to land. Do not rename it in place:
`TestChangelogSectionsAreWellFormed` requires the first version heading to be
`Unreleased`, so a tree without one fails the suite the release job runs. The
number in the heading follows from what the section holds:
`TestChangelogSemVerBumps` rejects an `### Added` in a patch and a `### Removed`
anywhere but a major release, `## Unreleased` included. It also rejects a
`### Changed` in a patch that announces a raised minimum Go version, which is
the one `Changed` entry that breaks a consumer who only built from source; that
one wants a minor. 1.12.2 shipped it in a patch and is named in the test as the
exception it is. Commit that, then:

```sh
ver=X.Y.Z                            # the version that heading now names
git tag -s "v$ver" -m "v$ver"   # -s signs; drop it if you have no GPG key
git push origin "v$ver"
```

[release.yml](.github/workflows/release.yml) runs the race suite, builds
every platform, smoke-tests the host binary against every line of
`checksums.txt`, signs a build-provenance attestation for each of them,
uploads the inventory, the checksum list, and the license text, and
publishes through a draft, so a failed upload is never
visible to consumers. A tag with no matching CHANGELOG section fails before
anything is built. Cut the tag from a commit `main` already carries: the
workflow does not re-run the pull-request checks, and a tag is what
`gauntlet update` serves. It refuses a tag older than the newest published
release, and one whose commit `origin/main` does not carry, both before the build:
`releases/latest` is what every consumer resolves, so a stale tag published
there is a downgrade delivered to everyone and the immutability rule then
blocks putting it right. Land the branch first, then tag `main`.

The post-build half of that is `make smoke VERSION=<version>`, the target both
the release job and the pull-request `dist` job run: it starts the host binary
`dist` just built and compares the version it reports with the one it was
stamped with. Run it after `make dist VERSION=<version>` to check a release
path by hand before tagging.

The other half is `make artifacts VERSION=<version>`, which writes
`dist/checksums.txt` and `dist/sbom.json` from the binaries `dist` built and
verifies the checksums against them. `make release VERSION=<version>` is
`clean-tree release-version check test dist artifacts`; the pull-request `dist`
job runs `make artifacts` too, so a change that broke the inventory or the
checksums fails there rather than at a tag.

`VERSION` is required, not inherited: `make build` and the other local targets
default it to `dev`, and a release built on that default would be complete and
self-consistent at the wrong version, with assets named `gauntlet_dev_*` and a
`make smoke` that passes against the same placeholder. `make release` refuses
it before the suite runs.

## Rolling back a bad release

A published release is immutable. The workflow refuses to replace a
published version's assets or notes, and a self-updating client compares
versions exactly rather than by semver, so a "downgrade" is applied when
published deliberately. Two moves, in order:

1. Delete the release on GitHub (Edit release, then Delete). That stops
   `gauntlet update` and the README install from resolving to it; users who
   already installed it keep running it until they update again.
2. Ship the fix forward as a new patch tag. Deleting a release does not
   un-send a binary, so a yanked version is not a rollback.

To check what users would get before publishing one, `gauntlet update
--check` resolves the same `releases/latest` endpoint the install snippet
uses.

## Supported versions

One supported version: the latest release. `gauntlet update` and the README
install both resolve `releases/latest`, never a version list, so an older tag
is what a user keeps until they update. There is no backport branch, and a
security fix ships as a new patch tag rather than as a second release on an
old one, so a consumer has to be on the latest to receive it. Older tags stay
downloadable and immutable: they are what a reported issue is reproduced
against, and the journal a run left is read with the version that wrote it.

## Layout

`cmd/gauntlet` is flags and dispatch only; everything real lives in
`internal/`, and dependencies point one way: `runner` uses `agent`,
`prompt`, `normalize`, `gitx`, and friends, and no package inside
`internal/` imports `ui`. A test in `cmd/gauntlet` pins that graph.
[docs/DESIGN.md](docs/DESIGN.md) is the map and
records what each decision costs.

Adding a review prompt is dropping `NAME-review.md` into
`internal/prompt/prompts/`: prompts are embedded by glob and discovered by
filename. Review names are API (see the consumer contract at the top of
[CHANGELOG.md](CHANGELOG.md)), so the same change also adds the stem to
`goldenReviewNames` in [internal/prompt/contract_test.go](internal/prompt/contract_test.go):
that test fails until you do, with these instructions. Named sets such as
`quick` are separate, in `internal/prompt/sets.go`. Files under
`internal/prompt/rules/` are different: they are the containment text every
agent runs under, so treat changes there as security-relevant.

`site/public/` is the one-page project site, a condensed README rendered in a
browser. It is not part of the build: static HTML, no bundler, deployed with
`wrangler` from `site/wrangler.jsonc`. Nothing in `internal/` reads it, so a
change there ships unreviewed except through
[cmd/gauntlet/site_test.go](cmd/gauntlet/site_test.go), which pins the color
tokens and the markup invariants WCAG 2.2 AA needs. The logos and the
dashboard screenshot under `site/public/` are copies of the ones in `assets/`,
kept beside the page because the site deploys `site/public` alone; update both
copies in the same change, and regenerate the screenshot with
`./scripts/shots.sh` as described above.

On macOS, sandboxed agent tests and runs require the system
`/usr/bin/sandbox-exec` Seatbelt launcher; `make doctor` checks it.
