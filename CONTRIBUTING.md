# Contributing

`make verify` must pass before you push: `make check`, `make check-scripts`,
and the suite under all three build-tag configurations. That is everything CI
runs on a pull request except the coverage, dist, and reproducibility legs.
`make ci` is the Go-only half (`make check && make test`) and skips the
scripts lint, so a Python or shell change can be green there and red in the
`scripts` job.

## Prerequisites

On a new machine, start here:

```sh
make doctor            # every prerequisite below, checked in one run
```

It reports every missing tool at once, with what to install and which targets
need it, and exits 0 when nothing is missing. The targets also preflight their
own tools, so an older Go fails `make build` directly and `make check-scripts`
names `uvx` and shellcheck itself. `tar` and `cmp` for `repro`, `install` for
`make install`, and a checksum tool for `artifacts` are checked by
`make doctor` alone.

- Go. The minimum is the `go` line in [go.mod](go.mod); any newer toolchain
  builds, tests, and formats the tree. The Makefile sets `GOTOOLCHAIN=local`,
  and `make build`, `make check`, and the test targets name what to install
  when the local Go is older. `make dist` and `make repro` instead require the
  exact release in `GO_VERSION` in the [Makefile](Makefile), which every CI job
  installs; a toolchain bump edits that one line, and
  `make dist GO_VERSION=<x.y.z>` overrides it.
- GNU make and git, on Linux or macOS. The runner needs POSIX process groups,
  `flock`, and `O_NOFOLLOW`, so there is no Windows build.
- A C compiler for the race detector behind `make test`, `make test-pkg`,
  `make cover`, and `make ci`: GCC or Clang on Linux, the Xcode Command Line
  Tools on macOS. The test targets set `CGO_ENABLED=1` and check for one;
  `make build` disables cgo and needs none.
- On macOS, the system `/usr/bin/sandbox-exec` Seatbelt launcher, which
  sandboxed agent tests and runs need.
- `uvx` (from [uv](https://docs.astral.sh/uv/getting-started/installation/))
  and shellcheck on `PATH`, for `make check-scripts` and so for `make verify`.
  The Go half of the tree needs neither. macOS ships no shellcheck:
  `brew install shellcheck`.

## Build and test

```sh
make build            # ./gauntlet for this host
make test             # every package, race detector, shuffled order
```

`make test` is a gate, not a loop: it takes minutes under the race detector,
and `internal/runner` is most of the wait. While you work, run one package or
one test:

```sh
make test RUN=TestStripReportSections            # one test anywhere in the tree
make test-pkg PKG=./internal/prompt              # one package
make test-pkg PKG=./internal/prompt RUN=TestStripReportSections   # one test in package
```

`test-pkg` uses the same tags, race detector, and temp directory as
`make test`, so a green loop stays green in the full run. It refuses to run
without `PKG`, and `make test PKG=...` refuses too rather than dropping the
package and running the whole tree. A `RUN=` pattern that matches no test is
an error that names the pattern, so a typo cannot pass as green.

Between two edits, `test-fast` is the same run without the race detector:

```sh
make test-fast PKG=./internal/runner                # one package, no race detector
make test-fast PKG=./internal/runner RUN=TestName   # one test in it
```

It keeps `-tags sqlite` and the shuffled order, so it builds what `test-pkg`
builds; run `make test-pkg` on the package before you push, since only the
race detector finds the interleavings. Plain `go test` drops the tags and
tests the no-database build, not the default one.

Tests must not write into a tmpfs or into an ignored path inside this repo:
the prompt discovery tests would see their own fixtures as ignored. The
Makefile points `TMPDIR` at `~/.cache/gauntlet/test` and ignores an exported
`TMPDIR`, since the usual one is a tmpfs; override it on the make command line
(`make test TMPDIR=...`). A directory that exists but cannot be written to (a
read-only home, another user's cache, a sandbox denial) is refused by the test
targets and by `make doctor`, with the path and the override.

The Makefile replaces any `GOFLAGS`, inherited or on the command line, with
`-mod=readonly`, so a drift from go.sum fails instead of rewriting the
lockfile. Three targets differ: `make staticcheck` and `make vuln` clear it
for the tools they `go run`, and `make tidy` runs `go mod tidy -diff` under
`-mod=mod` and only reports the diff. Change modules with `go get` and
`go mod tidy`. `gofmt` is the one in `$(go env GOROOT)`, not the first on
`PATH`.

To pass test flags, set `TESTFLAGS`; it replaces the default `-race -shuffle=on`:

```sh
make test TESTFLAGS='-race -shuffle=on -v'
```

The network is reached on first use only: the first `go` command downloads
the module graph, `check-scripts` downloads its pinned tools through `uvx`,
and `make vuln` downloads govulncheck (and reads the advisory database on
every run).

## Checks a pull request must pass

```sh
make ci               # make check && make test
make verify           # check, check-scripts, and the suite under all three tag sets
```

`make check` is `go mod tidy -diff`, a gofmt check, `go fix -diff`, vet, and
staticcheck, across the three tag configurations CI tests (default `sqlite`,
bare, and `notoktop`). It mirrors the first step of ci.yml. Each finding names
its fix:

- `needs gofmt:` is fixed by `make fmt`.
- A `go fix` diff names the invocation that applies it, under the tag set it
  came from (`go fix -tags sqlite ./...`, `go fix ./...`,
  `go fix -tags notoktop ./...`).
- A `tidy` diff means go.mod or go.sum no longer matches the module graph: run
  `go mod tidy` and commit the result.

staticcheck runs at the pinned `STATICCHECK_VERSION` through `go run`, so
nothing needs installing, and with `-checks=all`, so a check a later release
adds fails the gate instead of staying off. `make staticcheck TAGS=...` runs
one tag set.

The `scripts` job lints `scripts/` with ruff (rules in
[pyproject.toml](pyproject.toml)) and mypy `--strict`, the YAML in
[.github/](.github) with yamllint `--strict` (rules in [.yamllint](.yamllint)),
and runs shellcheck on `scripts/shots.sh` and on every workflow `run:` body
(`make check-workflow-shell`). `make check-scripts` runs the same steps at the
versions CI pins. shellcheck comes from the runner image rather than `uvx`, so
a local version that differs from `SHELLCHECK_VERSION` gets a note, not a
failure. `make fmt-scripts` rewrites scripts with ruff format.

CI also runs, and `make verify` leaves out:

- `make cover`, gated by `COVER_MIN` in the Makefile. Run it before pushing a
  change that removes tested code.
- `make artifacts` (which builds `make dist`), `make smoke`, and `make repro`.
  Run them when the change touches the release path.
- govulncheck ([vulnscan.yml](.github/workflows/vulnscan.yml)) on a change to
  `go.mod` or `go.sum`. Run `make vuln` before pushing a dependency bump.

The [pull request template](.github/pull_request_template.md) restates this as
a checklist. `ci` and `vulnscan` also take `workflow_dispatch`, so a
runner-image or package-index incident is answered by re-running from the
Actions tab, not by an empty commit; the event is in each concurrency group,
so a manual run never cancels the push it repeats.

## Maintainer scripts

`scripts/` is not part of the build; only its lint runs on a pull request.
The two entry points below are run by hand and have no `make` target.

The README screenshots (`assets/dashboard.png`, `assets/launcher.png`) are
regenerated with:

```sh
./scripts/shots.sh
```

It needs uv, a Chromium-based browser, and ImageMagick, which CI does not
install, so the checked-in PNGs are what a clone gets.
`internal/ui/shots_test.go` writes the renderer's ANSI frames into
`.scratch/shots`, `scripts/shots/render.py` exports SVG from them, and the
browser rasterizes that. It also needs the two faces `render.py` names,
DejaVu Sans Mono and MesloLGS Nerd Font Mono. macOS ships neither, and the
fallback `monospace` has no braille or block elements, so Chromium substitutes
glyphs at other advance widths and writes a valid picture that is not the
checked-in one. The script cannot detect that, so check first:

```sh
fc-match 'DejaVu Sans Mono'
fc-match 'MesloLGS Nerd Font Mono'
```

Each must print the family it was asked for. Regenerate the screenshots in
the same change whenever the dashboard or launcher changes shape.

The file-signal suggester is scored against what agents picked in past runs,
read from the journal:

```sh
uv run scripts/suggest-calibrate.py --detail
```

Run it before and after touching `internal/evidence/suggest.go`, and read the
numbers as movement between runs, not as a grade: the agent's pick is a
reference, not ground truth.

## Changelog

User-visible changes land in [CHANGELOG.md](CHANGELOG.md) under
`## Unreleased` in the same change. Each impact heading (`Added`, `Changed`,
`Deprecated`, `Removed`, `Fixed`, `Security`) appears at most once per
version: add a bullet, not a heading. The release workflow turns the section
into the release notes. Internal refactors with no visible behavior need
nothing.

## Releasing

A release is a tag push. Move the `## Unreleased` heading down to
`## <version>` and put an empty `## Unreleased` back above it; do not rename
it in place, because `TestChangelogSectionsAreWellFormed` requires the first
version heading to be `Unreleased`. The version follows from the section:
`TestChangelogSemVerBumps` rejects an `### Added` in a patch, a `### Removed`
anywhere but a major release (`## Unreleased` included), and a patch
`### Changed` entry that raises the minimum Go version, which breaks source
builds and so wants a minor. 1.0.1 and 1.0.2 are exempt by name as
historical exceptions, and 1.12.2 from the Go rule, which it broke. Commit
that on `main`, then:

```sh
ver=X.Y.Z                            # the version that heading now names
git tag -s "v$ver" -m "v$ver"   # -s signs; drop it if you have no GPG key
git push origin "v$ver"
```

[release.yml](.github/workflows/release.yml) refuses, before building, a tag
with no matching CHANGELOG section, a tag older than the newest published
release, and a tag on a commit `origin/main` does not carry: `releases/latest`
is what every consumer resolves, and a published release cannot be replaced.
It does not re-run the pull-request checks, so land the branch first and tag
`main`. It then runs `make release` (the race suite and every platform),
smoke-tests the host binary and verifies every line of `checksums.txt`, signs
a build-provenance attestation for each binary, and publishes the binaries,
the checksum list, the SBOM, and the license text through a draft, so a failed
upload is never visible.

To check the release path by hand before tagging:

- `make artifacts VERSION=<version>` builds every platform, writes
  `dist/checksums.txt` and `dist/sbom.json`, and verifies the checksums. Then
  `make smoke VERSION=<version>` runs the host binary and checks its version.
  The pull-request `dist` job runs both.
- `make dist VERSION=<version>` checks cross-compilation alone; `artifacts`
  already includes it.
- `make release VERSION=<version>` is
  `clean-tree release-version check test dist artifacts`.

`VERSION` is required: the local targets default it to `dev`, and a release
built on that default is self-consistent at the wrong version (assets named
`gauntlet_dev_*`, a `make smoke` that passes), so `make release` refuses it
before the suite runs.

## Rolling back a bad release

A published release is immutable: the workflow refuses to replace its assets
or notes. A self-updating client compares versions exactly, not by semver, so
it would apply a deliberately published older version. Two moves, in order:

1. Delete the release on GitHub (Edit release, then Delete). `gauntlet update`
   and the README install stop resolving to it; users who installed it keep
   it until they update.
2. Ship the fix forward as a new patch tag. Deleting a release does not
   un-send a binary.

`gauntlet update --check` resolves the same `releases/latest` endpoint the
install snippet uses, so it shows what users would get.

## Supported versions

Only the latest release. `gauntlet update` and the README install both
resolve `releases/latest`, there is no backport branch, and a security fix
ships as a new patch tag, so a consumer must be on the latest to receive it.
Older tags stay downloadable and immutable: a reported issue is reproduced
against them, and a run's journal is read with the version that wrote it.

## Layout

`cmd/gauntlet` is flags and dispatch; everything real lives in `internal/`.
Dependencies point one way, no package inside `internal/` imports `ui`, and
`cmd/gauntlet/layout_test.go` pins the graph. [docs/DESIGN.md](docs/DESIGN.md)
is the map and records what each decision costs;
[AGENTS.md](AGENTS.md) holds the tree's rules.

A review prompt is `internal/prompt/prompts/NAME-review.md`, embedded by glob
and discovered by filename. Review names are API (see the consumer contract at
the top of [CHANGELOG.md](CHANGELOG.md)), so the same change adds the stem to
`goldenReviewNames` in
[internal/prompt/contract_test.go](internal/prompt/contract_test.go); the test
fails until you do and says so. Named sets such as `quick` live in
`internal/prompt/sets.go`. Files under `internal/prompt/rules/` are the
containment text every agent runs under: treat changes there as
security-relevant.

`site/public/` is the one-page project site, a condensed README: static HTML
with no build step, deployed with `wrangler` from `site/wrangler.jsonc`.
Nothing in the Go tree reads it, so only
[cmd/gauntlet/site_test.go](cmd/gauntlet/site_test.go) checks it, pinning the
color tokens and markup invariants WCAG 2.2 AA needs. Its logos and dashboard
screenshot are copies of the ones in `assets/`, because the site deploys
`site/public` alone: update both copies in the same change.
