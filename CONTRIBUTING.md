# Contributing

The short version: `make check` and `make test` must pass before you push.
CI runs both on every pull request, plus the other build-tag configurations
and a cross-compilation pass.

## Prerequisites

- Go. The minimum version is the `go` line in [go.mod](go.mod); any newer
  toolchain works. CI installs whatever go.mod asks for, so there is nothing
  else to pin.
- GNU make and git, on Linux or macOS. The runner depends on POSIX semantics
  (process groups, flock, O_NOFOLLOW), so there is no Windows build.

## Build and test

```sh
make build            # ./gauntlet for this host
make test             # every package, race detector, shuffled order (~30s)
```

The first run downloads Go modules; after that the loop is offline.

For the edit-test loop, run one package or one test instead of the suite:

```sh
make test-pkg PKG=./internal/prompt              # one package
make test-pkg PKG=./internal/prompt RUN=TestStripReportSections   # one test
```

`test-pkg` uses the same tags, race detector, and temp directory as
`make test`, so a green loop stays green in the full run. Plain `go test`
also works, but without `-tags sqlite` you are testing the no-database
build rather than the default one.

Tests must not write into a tmpfs or into an ignored path inside this repo:
the prompt discovery tests would otherwise see their own fixtures as
ignored. The Makefile points `TMPDIR` at `~/.cache/gauntlet/test` for that
reason; leave it alone unless you know better.

## Checks a pull request must pass

```sh
make check
```

This is gofmt, `go fix`, and vet across all three tag configurations CI
tests (default `sqlite`, bare, and `notoktop`). It mirrors ci.yml's first
step exactly: if `make check` is green locally, that step is green there.

CI additionally runs the full suite under each tag configuration, then
`make dist` and `make repro`. Reproduce the other two matrix legs locally
with `make test TAGS=notoktop` and `make test TAGS=` when your change
touches tagged files; you do not need dist or repro unless you touched the
release path.

## Layout

`cmd/gauntlet` is flags and dispatch only; everything real lives in
`internal/`, and dependencies point one way: `runner` uses `agent`,
`prompt`, `normalize`, `gitx`, and friends, and no package inside
`internal/` imports `ui`. [docs/DESIGN.md](docs/DESIGN.md) is the map and
records what each decision costs.

Adding a review prompt is dropping `NAME-review.md` into
`internal/prompt/prompts/`: prompts are embedded by glob and discovered by
filename, so there is no registration list to update. Named sets such as
`quick` are separate, in `internal/prompt/sets.go`. Files under
`internal/prompt/rules/` are different: they are the containment text every
agent runs under, so treat changes there as security-relevant.
