# Contributing

The short version: `make ci` must pass before you push. That is `make check`
and `make test`. CI runs both on every pull request, plus the other build-tag
configurations and a cross-compilation pass.

## Prerequisites

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

## Build and test

```sh
make build            # ./gauntlet for this host
make test             # every package, race detector, shuffled order
```

The suite is ~30s once the build cache is warm. The first run on a fresh
clone compiles every package under the race detector and takes minutes.

The first run downloads Go modules; after that the loop is offline.
`make` passes `-mod=readonly` on every target except `make vuln`, so a drift
from go.sum fails the command instead of rewriting the lockfile. Change
modules with `go get` / `go mod tidy`, not as a side effect of the build.
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
alone, since `make test` always runs the whole tree. A
`RUN=` pattern that matches no test is an error rather than a pass, so a
mistyped name cannot read as a green loop; the error names the pattern and
the command that lists the real ones. Plain `go test` also works, but
without `-tags sqlite` you are testing the no-database build rather than the
default one.

Tests must not write into a tmpfs or into an ignored path inside this repo:
the prompt discovery tests would otherwise see their own fixtures as
ignored. The Makefile points `TMPDIR` at `~/.cache/gauntlet/test` for that
reason; leave it alone unless you know better. An exported `TMPDIR` in your
shell is ignored on purpose, since the usual one is the tmpfs this avoids;
override it on the command line (`make test TMPDIR=...`) instead.

## Checks a pull request must pass

```sh
make ci               # make check && make test
```

One command runs the analysis, the scripts lint, and the suite under all three
tag sets, for when a red check would otherwise first appear after push. A
pull request also runs `make cover`, `make dist`, and `make repro`, which the
release path adds on top:

```sh
make verify           # check, check-scripts, and the suite under all three tag sets
```

It is minutes rather than seconds; `make ci` is the loop, `make verify` is the
gate.

`make check` is gofmt, `go fix`, and vet across all three tag configurations
CI tests (default `sqlite`, bare, and `notoktop`). It mirrors ci.yml's first
step exactly: if `make check` is green locally, that step is green there. The
pull request template
([.github/pull_request_template.md](.github/pull_request_template.md))
restates this list as a checklist.

CI additionally runs the full suite under each tag configuration, then
`make dist` and `make repro`. Reproduce the other two matrix legs locally
with `make test TAGS=notoktop` and `make test TAGS=` when your change
touches tagged files; you do not need dist or repro unless you touched the
release path. CI also runs `make cover` with a coverage floor (`COVER_MIN`
in the Makefile); run `make cover` locally before pushing a change that
removes tested code paths.

A separate `scripts` job lints `scripts/` with ruff (rules in
[pyproject.toml](pyproject.toml)), mypy `--strict`, and shellcheck on
`scripts/shots.sh`, and lints the YAML in [.github/](.github) with yamllint `--strict` (rules in
[.yamllint](.yamllint)). `make check-scripts` runs those same steps with
the versions CI pins. It needs `uvx` (shipped with
[uv](https://docs.astral.sh/uv/getting-started/installation/)) and
shellcheck on PATH; shellcheck is the one tool the runner image supplies
rather than `uvx` installing, so a local copy whose version differs from
`SHELLCHECK_VERSION` gets a note instead of a failure. `make fmt-scripts`
rewrites scripts with ruff format.

Pull requests that touch `go.mod` or `go.sum` additionally run govulncheck,
the advisory scan of the dependency graph
([vulnscan.yml](.github/workflows/vulnscan.yml)). `make vuln` runs the same
scan locally; run it before pushing a dependency bump rather than learning
about it from a red check.

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

A release is a tag push; nothing else. Rename `## Unreleased` in
CHANGELOG.md to `## <version>`, commit, then:

```sh
ver=X.Y.Z                            # the version that heading now names
git tag -s "v$ver" -m "v$ver"   # -s signs; drop it if you have no GPG key
git push origin "v$ver"
```

[release.yml](.github/workflows/release.yml) runs the race suite, builds
every platform, smoke-tests the host binary against every line of
`checksums.txt`, and publishes through a draft, so a failed upload is never
visible to consumers. A tag with no matching CHANGELOG section fails before
anything is built. Cut the tag from a commit `main` already carries: the
workflow does not re-run the pull-request checks, and a tag is what
`gauntlet update` serves.

The post-build half of that is `make smoke VERSION=<version>`, the target both
the release job and the pull-request `dist` job run: it starts the host binary
`dist` just built and compares the version it reports with the one it was
stamped with. Run it after `make dist VERSION=<version>` to check a release
path by hand before tagging.

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
