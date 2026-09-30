BINARY  := gauntlet
CMD     := ./cmd/gauntlet
DIST    := dist
VERSION ?= dev

# Where `make install` puts the binary. The default is the per-user directory
# the README install and the runner's fallback PATH already assume, so the
# documented install is unchanged; a system install overrides it on the
# command line (`make install BINDIR=/usr/local/bin`), which writes wherever
# the caller can write rather than into a directory this Makefile picked.
BINDIR ?= $(HOME)/.local/bin

GO      ?= go
GOFMT   ?= $(shell $(GO) env GOROOT)/bin/gofmt
LDFLAGS := -s -w -X main.version=$(VERSION)

# Honor go.sum: a missing or extra module must fail the command rather than
# rewrite the manifests. `make vuln` clears this because govulncheck is not a
# build input.
#
# `override`, not `+=`: an exported GOFLAGS is a build input like any other,
# and a `go env -w GOFLAGS=-tags=notoktop` or an inherited GOFLAGS carrying
# -tags, -gcflags, or -ldflags would compile a different program under the same
# name, which is what -mod=readonly is here to rule out. A command line still
# wins, so `make test GOFLAGS=-v` is the escape hatch it has always been.
override export GOFLAGS := -mod=readonly
export GOWORK := off
export GOTOOLCHAIN := local
export GOAMD64 := v1
export GOARM64 := v8.0
# The rest of the toolchain state that changes the bytes rather than the build's
# shape, closed for the same reason GOAMD64 is: an ambient setting left in the
# go env file compiles the same source into a different binary, and the
# compiler records what it used. GOEXPERIMENT is recorded in every binary
# (`go version -m` prints it), so `dist` checks it below; GOFIPS140 selects a
# FIPS module for the standard library and is recorded by neither, so it is
# closed here rather than checked. Empty GOEXPERIMENT is the toolchain's own
# default set, not "no experiments": a Go release that turns one on by default
# still gets it.
export GOEXPERIMENT :=
export GOFIPS140 := off

# Reading an agent's own session transcript is on by default: it lives in
# toktop, costs one pure-Go dependency, and is the only source of counts for
# agents that print none. `sqlite` is on for the same reason: crush and
# opencode keep their counters in databases rather than transcripts, and the
# driver is pure Go, so cross-compilation is unaffected.
#
# TAGS=notoktop drops transcript reading; TAGS= keeps transcript reading but
# drops the database driver. Both builds retain the CLI and dashboard modules.
TAGS    ?= sqlite
GOTAGS  := $(if $(TAGS),-tags $(TAGS),)

# The one build input that cannot be normalized away is the compiler: a Go
# binary records the version that compiled it, so the same source under two
# toolchains is not the same artifact. The `go` line in go.mod is a language
# minimum, and GOTOOLCHAIN=local below compiles with whatever is installed, so
# without this pin the release is built by whichever 1.27.x the runner happened
# to have that week. Every workflow installs exactly this release, and the
# artifact targets refuse any other (TestGoVersionPinMatchesCI).
GO_VERSION ?= 1.27.1

# Scripts job pins, matching .github/workflows/ci.yml (TestScriptsToolPinsMatchCI).
RUFF_VERSION ?= 0.16.4
MYPY_VERSION ?= 2.3.1
RICH_VERSION ?= 15.0.0
YAMLLINT_VERSION ?= 1.38.0
UV_VERSION ?= 0.12.6
# The one lint tool that is not installed by uvx: shellcheck is a PATH binary,
# because that is the copy the Ubuntu runner already carries and pinning it
# here is a record of what CI runs, not something the job can install. The
# recipes below compare against it and warn on drift, and
# TestScriptsToolPinsMatchCI keeps this line and the workflow's in step.
SHELLCHECK_VERSION ?= 0.11.0
GOVULNCHECK_VERSION ?= v1.7.0
# staticcheck is the Go half's linter the way ruff is the scripts half's: it
# is fetched through `go run` at a pinned version, exactly as govulncheck
# below it, so `make check` needs nothing installed and CI and a contributor
# read the same number. The version is the staticcheck release, which is
# also the Go release it was built against; bump it together with the
# toolchain pin above, never alone, since an analyzer that cannot read this
# compiler's export data fails every package rather than reporting findings.
STATICCHECK_VERSION ?= v0.8.1

# Release artifacts must not depend on the build host's locale or timezone:
# the shell orders glob expansion with strcoll, so checksums.txt would list
# assets in a different order on hosts with a different LC_COLLATE, and a
# recipe that formats a date would read the host's zone. `make repro` is the
# exception: it strips both from its second tree copy, which is the point of
# that target, so pinning them here is what makes the comparison meaningful.
export LC_ALL := C
export TZ := UTC

# Tests must not write into a tmpfs (RAM) or into an ignored path inside this
# repo, which would make prompt discovery see its own fixtures as ignored.
#
# `:=`, not `?=`: make gives an exported environment variable the same status
# as a command-line one, and `?=` keeps it. TMPDIR is exported on most Linux
# shells and by launchd on macOS, usually pointing at the tmpfs the rule above
# exists to avoid, and setting it in the environment was then silently
# ignored. A command line (`make test TMPDIR=...`) still wins.
#
# Overriding an exported variable keeps it exported, so every recipe that runs
# the go command hands it this path, and go refuses to start when its work
# directory is missing. `test-tmpdir` creates it, and every target that runs
# the test suite depends on it directly: on a fresh macOS runner $HOME/.cache
# does not exist, and `make check` and `make test` failed there before they
# depended on it.
#
# The preflights must not. `toolchain-min` and `toolchain` read the go version
# and compare it, which is all they do, yet both declared this dependency, so a
# machine with no HOME could not build a binary or cross-compile a release
# artifact and was told the problem was tests. Only the targets that hand
# TMPDIR to a `go test` name test-tmpdir; `build` and `dist` never do.
TMPDIR := $(HOME)/.cache/gauntlet/test

# POSIX only, deliberately: killing an agent's whole process tree needs process
# groups, the directory lock needs flock, prompt reads need O_NOFOLLOW, and hot
# reload needs execve. Windows has no equivalent that keeps those guarantees.
PLATFORMS := \
	linux/amd64 linux/arm64 \
	darwin/amd64 darwin/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## show available targets
# Plain greedy ERE on purpose: a lazy `.*?` is a PCRE-ism that POSIX ERE
# leaves undefined, and BSD grep and awk (macOS) reject the adjacent
# duplication with "repetition-operator operand invalid". `## ` appears at
# most once per documented target line, so greedy matches the same split.
	@grep -E '^[a-zA-Z_-]+:.*## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# The tree has no cgo sources, and dist/repro pin CGO_ENABLED=0; building the
# host binary without the pin would let an ambient C toolchain flip net and
# os/user onto the cgo path, so `make install` could ship a dynamically
# linked flavor that no release ever produced.
#
# -buildvcs=false keeps git metadata (revision, commit time, dirty flag) out
# of the artifact: the bytes then depend only on the source and the toolchain,
# the same from a clone, a tarball, or a dirty tree. It also removes a
# refusal-to-build edge: with the default -buildvcs=auto, `go build` inside a
# checkout whose `git status` fails (a container running as another uid) dies
# instead of building. Nothing at runtime reads those fields; the version
# string comes from main.version, and sbom.json is the inventory.
.PHONY: build
build: | toolchain-min
build: ## build the gauntlet binary for this host
	CGO_ENABLED=0 $(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: run
run: build ## build, then run one loop here with the dashboard
	./$(BINARY) --once --tui

# RUN is a go test -run pattern (default: every test in the package).
RUN ?=
# test-pkg and test-fast only. `make test` always runs the whole tree, so a
# PKG= passed there has nothing to select from, and NEEDS_NO_PKG below turns
# that into a refusal rather than the six-minute run the contributor was
# trying to avoid.
PKG ?=

# The race detector is what makes a package's suite worth the minutes it costs,
# and it is also what makes the loop between two edits long: internal/runner
# alone is 373s of `make test` under it, on the Linux box that measured the
# 6m18. It is a variable rather than a literal in the recipe below so the fast
# loop is the same command without it, instead of a second copy of the recipe
# that could drift from the one the gate runs. `test-fast` overrides it per
# target; a command line still wins over both.
TESTFLAGS ?= -race -shuffle=on

# `go test -run` exits 0 when the pattern selects nothing, so a mistyped test
# name reads as a pass and the edit-test loop loses an iteration before the
# contributor notices. The command the failure prints has to be the one that
# runs: `-list` takes a regexp, and a bare `.` is read as a package argument
# instead, so the listing it names is the module root and go test answers "no
# Go files" while listing nothing. That is also why the package is `$(1)`, the
# one the caller already named, and not PKG, which is empty under `make test`.
# tee keeps the per-package lines streaming; the status file carries go test's
# own exit code, which a pipeline would drop, because this Makefile is POSIX sh
# and has no pipefail.
define RUN_TESTS
	@log="$(TMPDIR)/test.$$$$.log"; \
	{ TMPDIR="$(TMPDIR)" CGO_ENABLED=1 $(GO) test $(GOTAGS) $(TESTFLAGS) -run '$(RUN)' $(1) 2>&1; \
	echo $$? >"$$log.status"; } | tee "$$log"; \
	rc=$$(cat "$$log.status"); \
	if [ "$$rc" -ne 0 ]; then rm -f "$$log" "$$log.status"; exit "$$rc"; fi; \
	if [ -n "$(RUN)" ] && ! awk '/^ok / && $$0 !~ /no tests to run/ { ran = 1 } END { exit !ran }' "$$log"; then \
		echo "test: no test matches RUN='$(RUN)'; go test reports success when a -run pattern selects nothing" >&2; \
		echo "test: list the candidates with: go test $(GOTAGS) -list '.*' $(1)" >&2; \
		rm -f "$$log" "$$log.status"; exit 1; \
	fi; \
	rm -f "$$log" "$$log.status"
endef

# A per-package target that silently ran the whole tree would be the slow loop
# under the name of the fast one, so an empty PKG is a refusal. Shared by the
# two targets that take one, so the guard cannot be true of one and not the
# other.
define NEEDS_PKG
	@case "$(PKG)" in ""|./...) \
		echo "$(1) needs a package: make $(1) PKG=./internal/prompt [RUN=TestName]" >&2; \
		echo "$(1): PKG is empty, so it would run every package under a target that promises one" >&2; \
		exit 1 ;; \
	esac
endef

# The other direction: `make test` takes no package, and a PKG= passed there
# used to be ignored in silence. A contributor who wants one package and types
# it on the target whose name matches the docs gets the whole tree under the
# race detector instead, which is the six-minute gate the per-package targets
# exist to avoid, and nothing says the PKG= was dropped.
define NEEDS_NO_PKG
	@case "$(PKG)" in ""|./...) ;; \
		*) \
			echo "$(1) runs the whole tree and takes no PKG: make test-pkg PKG=$(PKG) [RUN=TestName]" >&2; \
			echo "$(1): PKG='$(PKG)' was dropped, so the run below is every package under the race detector" >&2; \
			exit 1 ;; \
	esac
endef

.PHONY: test
test: | toolchain-min test-tmpdir test-cgo
test: ## run all tests with the race detector, shuffled order
	$(call NEEDS_NO_PKG,test)
	$(call RUN_TESTS,./...)

# One package at a time keeps the edit-test loop fast; the flags match `make
# test` so a green package here stays green in the full run.
.PHONY: test-pkg
test-pkg: | toolchain-min test-tmpdir test-cgo
test-pkg: ## run one package's tests under the race detector: make test-pkg PKG=./internal/prompt [RUN=TestName]
	$(call NEEDS_PKG,test-pkg)
	$(call RUN_TESTS,$(PKG))

# The same run without the race detector, which is the only difference. A
# contributor editing internal/runner otherwise pays 373s per iteration, and
# the one command that costs less is a bare `go test`, which leaves off the
# build tags this Makefile passes: that is the no-database build, not the
# default one, so a green loop can be green against a configuration nothing
# ships. Everything else is held identical, so a green `test-fast` is the same
# build `test-pkg` would have run, minus the detector.
#
# Not a gate: -race is what catches the interleavings the suite exists to
# catch, and nothing here does. `make test-pkg` on the package before pushing
# is the check; this is the loop between two edits. The flags are per target
# rather than a second recipe, so there is one `go test` line to keep correct.
.PHONY: test-fast
test-fast: TESTFLAGS := -shuffle=on
test-fast: | toolchain-min test-tmpdir test-cgo
test-fast: ## run one package's tests without the race detector: make test-fast PKG=./internal/runner [RUN=TestName]
	$(call NEEDS_PKG,test-fast)
	$(call RUN_TESTS,$(PKG))

.PHONY: cover
cover: | toolchain-min test-tmpdir test-cgo
cover: ## test coverage summary, gated by COVER_MIN
	@mkdir -p $(DIST)
	TMPDIR="$(TMPDIR)" CGO_ENABLED=1 $(GO) test $(GOTAGS) $(TESTFLAGS) -coverprofile=$(DIST)/coverage.out ./...
	@total=$$($(GO) tool cover -func=$(DIST)/coverage.out | awk '/^total:/ {print $$3}'); \
		echo "total coverage: $$total (floor $(COVER_MIN)%)"; \
		awk -v got="$${total%\%}" -v min="$(COVER_MIN)" 'BEGIN { \
			if (got + 0 < min + 0) { \
				printf "coverage fell to %s%%, below the %s%% floor\n", got, min > "/dev/stderr"; exit 1 \
			} }' 

# The coverage floor, measured where it is enforced: a developer machine with
# agent CLIs installed runs paths a CI runner skips, so a number taken locally
# reads about two points high and would fail every pull request. This is CI's
# figure, kept a little under it to absorb the shuffle. It ratchets: raise it
# when CI reports higher, never lower it to make a change fit.
COVER_MIN ?= 74.0

# The `go` line in go.mod is the language minimum, and GOTOOLCHAIN above is
# pinned to local so a build never fetches a toolchain behind the caller's
# back. An older `go` therefore fails deep inside `go build` or `go test`, with
# a message about GOTOOLCHAIN, a knob the caller never set, and no pointer at
# the file that states the minimum. This preflight names the requirement
# instead. It gates the minimum only: the exact release stays `toolchain`,
# which gates the artifacts a release ships and not the dev loop.
.PHONY: toolchain-min
toolchain-min:
toolchain-min:
	@min=$$(awk '$$1 == "go" { print $$2; exit }' go.mod 2>/dev/null); \
	if [ -z "$$min" ]; then \
		echo "toolchain-min: no go.mod in $$(pwd), so there is no minimum to check against" >&2; \
		exit 1; \
	fi; \
	command -v "$(GO)" >/dev/null 2>&1 || { \
		echo "toolchain-min: $(GO) not found on PATH; install Go $$min or newer from https://go.dev/dl/" >&2; \
		exit 1; \
	}; \
	have="$$($(GO) env GOVERSION)"; have=$${have#go}; have=$${have%%-*}; \
	if [ -z "$$have" ]; then \
		echo "toolchain-min: '$(GO) env GOVERSION' printed nothing, so the Go on PATH cannot be identified" >&2; \
		exit 1; \
	fi; \
	awk -v have="$$have" -v want="$$min" 'BEGIN { \
		split(have, h, "."); split(want, w, "."); \
		for (i = 1; i <= 3; i++) { \
			if (h[i] + 0 > w[i] + 0) { exit 0 } \
			if (h[i] + 0 < w[i] + 0) { exit 1 } \
		} \
		exit 0 \
	}' || { \
		echo "toolchain-min: this tree needs Go $$min or newer (the go line in go.mod); $(GO) here is go$$have" >&2; \
		echo "toolchain-min: install $$min or newer from https://go.dev/dl/ , or pass GOTOOLCHAIN=auto to let $(GO) fetch the toolchain go.mod asks for" >&2; \
		exit 1; \
	}

.PHONY: test-tmpdir
test-tmpdir:
	@test "$(TMPDIR)" != "/.cache/gauntlet/test" || { echo "HOME is unset; set HOME or TMPDIR to a disk-backed directory. Tests must not use tmpfs or a gitignored path inside this repo" >&2; exit 1; }
	@mkdir -p "$(TMPDIR)"

.PHONY: test-cgo
test-cgo:
	@cc="$$($(GO) env CC)"; \
	command -v "$$cc" >/dev/null 2>&1 || { \
		echo "test: C compiler '$$cc' not found on PATH; install GCC or Clang (the race detector requires cgo)" >&2; \
		exit 1; \
	}

.PHONY: vet
vet: | test-tmpdir
vet: ## run go vet
	$(GO) vet $(GOTAGS) ./...

# go vet is the compiler's own set, which is why it is not the only analysis
# `check` runs: it answers what the compiler can see (printf verbs, lost
# cancellation, unreachable code) and nothing about the mistakes a compiler
# cannot make, a mistyped format argument in a log line, a redundant
# conversion, a branch that can never be taken. staticcheck is that second
# set, and it is what catches a defect that compiles cleanly.
#
# Fetched with `go run` at a pinned version rather than installed, the same
# trade `make vuln` makes: nothing to install before a contributor can run
# the gate, and the version a run uses is the one this Makefile records
# instead of whatever is on PATH. GOFLAGS is cleared for the same reason
# govulncheck clears it: -mod=readonly is this module's build flag, and the
# analyzer is a separate module resolved by the proxy.
#
# The tag set is the caller's, the same variable vet reads, so one of the
# three shipped configurations can be checked on its own.
.PHONY: staticcheck
staticcheck: | test-tmpdir
staticcheck: ## run staticcheck under $(TAGS)
	GOFLAGS= $(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) $(GOTAGS) ./...

# go.mod carries the supply chain and go.sum the hashes, and nothing else
# reads either: -mod=readonly stops a build from rewriting them, and the
# pinned-module tests in cmd/gauntlet read what is there. A module that
# stopped being imported, or a require line a replacement left behind, is
# still downloaded and still hashed on every build until somebody notices.
# `go mod tidy -diff` reports that as a diff instead of applying it, so the
# check cannot become the change it is checking for. tidy takes no build
# tags: it resolves every configuration at once, so this one command covers
# the sqlite, bare, and notoktop builds alike.
#
# -e is not passed, and the substitution's status is checked instead. A tidy
# that cannot complete writes no diff, so the only thing that can catch it is
# the exit code: without that, `make check` would read an unresolvable module
# graph as a clean tree.
.PHONY: tidy
tidy: | test-tmpdir
tidy: ## fail unless go.mod and go.sum are exactly what go mod tidy writes
	@out="$$(GOFLAGS=-mod=mod $(GO) mod tidy -diff)" || { \
		echo "tidy: 'go mod tidy -diff' failed, so the module graph was never resolved" >&2; \
		exit 1; \
	}; \
		if [ -n "$$out" ]; then \
			echo "tidy: go.mod or go.sum differs from the module graph's own answer:" >&2; \
			echo "$$out" >&2; \
			echo "tidy: run 'go mod tidy' and commit the result, or drop the require it no longer needs" >&2; \
			exit 1; \
		fi

# Release artifacts, and only those: `make build`, `make test`, and `make
# check` run on whatever toolchain a contributor has, which is right. A tagged
# release is a different question, since the compiler version is recorded in
# every binary it ships and the next patch release of Go would change those
# bytes. The suffix a vendor toolchain carries (`-X:nodwarf5`) is not part of
# the release, so only the goX.Y.Z prefix is compared. `?=` means an explicit
# `make dist GO_VERSION=x.y.z` still overrides, which is how a maintainer ships
# a deliberate toolchain bump without editing this file first.
.PHONY: toolchain
toolchain:
toolchain: ## fail unless the local Go release is the one release artifacts are built with
	@got="$$($(GO) env GOVERSION)"; want="go$(GO_VERSION)"; \
		[ "$${got%%-*}" = "$$want" ] || { \
			echo "toolchain: this is $$got, release artifacts are built with $$want" >&2; \
			echo "toolchain: install $$want, or pass GO_VERSION=<x.y.z> to build and record a different one" >&2; \
			exit 1; \
		}

# Package directories only: the Go tool already ignores dot-directories, but
# gofmt walks everything, including scratch fixtures.
GOFILES = $(shell $(GO) list -mod=readonly -f '{{.Dir}}' ./...)

.PHONY: fmt
fmt: ## rewrite all Go files with gofmt
	@test -x "$(GOFMT)" || { echo "gofmt not found at $(GOFMT); install Go or set GOFMT to this toolchain's gofmt" >&2; exit 1; }
	@test -n "$(GOFILES)" || { echo "go list returned no packages" >&2; exit 1; }
	"$(GOFMT)" -s -w $(GOFILES)

# CI tests all three tag configurations (see the matrix in ci.yml); check
# compiles each of them so a break under one of them fails here and not
# after push. The bare pass is the third configuration: neither tag defined.
.PHONY: check
check: tidy
check: | toolchain-min
check: ## verify the module manifests, formatting, toolchain fixes, and vet (CI parity)
	@test -x "$(GOFMT)" || { echo "gofmt not found at $(GOFMT); install Go or set GOFMT to this toolchain's gofmt" >&2; exit 1; }
	@test -n "$(GOFILES)" || { echo "go list returned no packages" >&2; exit 1; }; \
		unformatted=$$("$(GOFMT)" -s -l $(GOFILES)) || exit 1; \
		if [ -n "$$unformatted" ]; then \
			echo "needs gofmt:"; echo "$$unformatted"; \
			echo "check: rewrite them with 'make fmt', then run this again" >&2; \
			exit 1; \
		fi
# The three documented build modes are checked: sqlite+toktop, notoktop, and
# no tags at all (transcripts without database readers). CI tests all three; the
# analysis step must see the same set or a mode only it compiles goes unvetted.
#
# `go fix -diff` prints a unified diff and exits nonzero, which reaches the
# contributor as a red step carrying no command: applying it is the same
# invocation without -diff, under the tag set the diff came from. Each leg says
# so rather than leaving the fix to be guessed at.
	@$(GO) fix -diff -tags sqlite ./... || { \
		echo "check: '$(GO) fix -diff -tags sqlite ./...' reports rewrites; apply them with:" >&2; \
		echo "check:   $(GO) fix -tags sqlite ./..." >&2; \
		echo "check: then run this again" >&2; exit 1; \
	}
	@$(GO) fix -diff ./... || { \
		echo "check: '$(GO) fix -diff ./...' reports rewrites; apply them with:" >&2; \
		echo "check:   $(GO) fix ./..." >&2; \
		echo "check: then run this again" >&2; exit 1; \
	}
	@$(GO) fix -diff -tags notoktop ./... || { \
		echo "check: '$(GO) fix -diff -tags notoktop ./...' reports rewrites; apply them with:" >&2; \
		echo "check:   $(GO) fix -tags notoktop ./..." >&2; \
		echo "check: then run this again" >&2; exit 1; \
	}
	$(GO) vet -tags sqlite ./...
	$(GO) vet ./...
	$(GO) vet -tags notoktop ./...
	$(MAKE) --no-print-directory staticcheck TAGS=sqlite
	$(MAKE) --no-print-directory staticcheck TAGS=
	$(MAKE) --no-print-directory staticcheck TAGS=notoktop

# The Go half of a pull request: analysis across all three tag sets, then
# the race suite. The scripts job is separate (check-scripts) because it
# needs uvx and shellcheck, which a Go-only change does not.
.PHONY: ci
ci: check test ## Go pull-request checks: fmt, fix, vet, and the test suite

# The pull request's static checks and its test matrix, in one command, on one
# host. It is the answer to "would this be green after push", and it is
# deliberately not the edit-test loop: three race suites in a row is minutes,
# not seconds. The three legs are recursive makes rather than three
# prerequisites, so a failing tag stops the run and names the leg instead of
# continuing to the next one. The dist job (dist, smoke, artifacts, repro) is
# left out as well: it compiles every platform for two trees from cold, and
# CONTRIBUTING says to run it when the change touches the release path. So is
# make cover: it reruns the sqlite suite the first leg already ran, and its
# floor is a CI measurement (see COVER_MIN).
.PHONY: verify
verify: check check-scripts ## the pull request's static checks and all three tag legs, locally
	$(MAKE) test
	$(MAKE) test TAGS=
	$(MAKE) test TAGS=notoktop

# Local mirror of ci.yml's scripts job, including the pins. uvx fetches
# those tools on first use; shellcheck stays a PATH binary because that is
# what the Ubuntu runner already has, so its pin is checked rather than
# installed and a mismatch is a note, not a failure. The workflow
# definitions are linted with the same uvx pins as the Python tools, since a
# malformed one is a syntax error the Go build never sees.
#
# shellcheck runs with `--enable=all`, not the default set, so a check a
# later shellcheck adds fails here rather than reading as a clean tree; that
# includes check-extra-masked-returns, where a pipeline, a test, or a process
# substitution reports success while the work inside it died. Two are
# excluded, SC2250 (braces around every expansion) and SC2292 ([[ ]] over
# [ ]), both spelling rules this tree does not follow anywhere and neither
# of which hides a defect. shots.sh passes the rest at the pinned version.
#
# `uv --version`, not `uv version`: the latter with no argument reports the
# version of the project pyproject.toml declares, which is the placeholder
# 0.0.0, so the drift note below compared that against UV_VERSION and fired
# on every run, on every machine, whatever uv was installed.
.PHONY: check-scripts
check-scripts: check-workflow-shell ## ruff, mypy --strict, and yamllint --strict, plus shellcheck (CI parity)
	@command -v uvx >/dev/null 2>&1 || { \
		echo "check-scripts: uvx not found on PATH (install uv $(UV_VERSION): https://docs.astral.sh/uv/getting-started/installation/)" >&2; \
		echo "CI runs: uvx ruff@$(RUFF_VERSION) check scripts" >&2; \
		echo "         uvx ruff@$(RUFF_VERSION) format --check scripts" >&2; \
		echo "         uvx --with rich==$(RICH_VERSION) mypy@$(MYPY_VERSION) --strict scripts" >&2; \
		echo "         uvx yamllint@$(YAMLLINT_VERSION) --strict .github" >&2; \
		echo "         shellcheck --enable=all --exclude=SC2250,SC2292 scripts/shots.sh" >&2; \
		exit 1; \
	}
	@command -v shellcheck >/dev/null 2>&1 || { \
		echo "check-scripts: shellcheck not found on PATH; macOS ships none (brew install shellcheck), Linux packages it as shellcheck" >&2; \
		exit 1; \
	}
	@got_uv=$$(uv --version 2>/dev/null | awk '{print $$2}'); \
		if [ -n "$$got_uv" ] && [ "$$got_uv" != "$(UV_VERSION)" ]; then \
			echo "note: uv $$got_uv; CI pins $(UV_VERSION). Tools are version-locked, but resolver behavior can differ." >&2; \
		fi
	@got_sc=$$(shellcheck --version 2>/dev/null | awk '/^version:/ {print $$2}'); \
		if [ -n "$$got_sc" ] && [ "$$got_sc" != "$(SHELLCHECK_VERSION)" ]; then \
			echo "note: shellcheck $$got_sc; the pin is $(SHELLCHECK_VERSION). A new upstream release adds checks, so a mismatch here is a false red, not a clean tree." >&2; \
		fi
	uvx ruff@$(RUFF_VERSION) check scripts
	uvx ruff@$(RUFF_VERSION) format --check scripts
	uvx --with rich==$(RICH_VERSION) mypy@$(MYPY_VERSION) --strict scripts
	uvx yamllint@$(YAMLLINT_VERSION) --strict .github
	shellcheck --enable=all --exclude=SC2250,SC2292 scripts/shots.sh

# A `run:` body is shell too: the release path runs four of them on every tag,
# and one of those bodies is what decides whether a release is published. The
# Go build never reads a workflow, yamllint reads the YAML around the body and
# not the shell inside it, and the only script shellcheck was given is
# shots.sh, so a quoting or masked-return defect in that shell was found by
# the tag that ran it rather than on the pull request that wrote it.
#
# Each body is written out as its own file and linted with the flags
# shots.sh gets, under the same TMPDIR every other test scratch path uses.
# The generated preamble disables SC2154 for one reason: GITHUB_REF_NAME and
# SHELLCHECK_VERSION are set by the runner, and a fragment of a workflow
# cannot assign them.
#
# An extraction that finds nothing has to fail rather than lint an empty glob:
# a rename of `run:` to something else would otherwise read as a clean tree.
# The awk program is one line because a workflow's YAML indentation is the
# only thing delimiting a body, and a backslash-continued awk function is one
# more dialect between GNU awk and the BSD one a macOS runner ships.
.PHONY: check-workflow-shell
check-workflow-shell: | test-tmpdir
check-workflow-shell: ## shellcheck the shell the workflow `run:` steps execute
	@command -v shellcheck >/dev/null 2>&1 || { \
		echo "check-workflow-shell: shellcheck not found on PATH; macOS ships none (brew install shellcheck), Linux packages it as shellcheck" >&2; \
		exit 1; \
	}
	@dir="$(TMPDIR)/workflow-shell"; \
	rm -rf "$$dir"; mkdir -p "$$dir"; \
	for wf in .github/workflows/*.yml; do \
		stem="$${wf##*/}"; stem="$${stem%.*}"; \
		awk -v dir="$$dir" -v stem="$$stem" 'function flush() { if (out != "") { print "#!/bin/bash" > out; print "# Generated from a `run:` body in " stem ".yml by `make check-workflow-shell`." > out; print "# The variables it reads (GITHUB_REF_NAME, SHELLCHECK_VERSION) are set by the runner." > out; print "# shellcheck disable=SC2154" > out; print body > out; close(out); out = ""; body = "" } } /^[[:space:]]*run: \|[[:space:]]*$$/ { flush(); n++; out = dir "/" stem "-" n ".sh"; next } out != "" { if ($$0 ~ /^          /) { body = body substr($$0, 11) "\n"; next } if ($$0 ~ /^[[:space:]]*$$/) { body = body "\n"; next } flush() } END { flush() }' "$$wf" || exit 1; \
	done; \
	set -- "$$dir"/*.sh; \
	[ -f "$$1" ] || { echo "check-workflow-shell: no \`run:\` body was extracted from .github/workflows; the lint below would cover nothing" >&2; exit 1; }; \
	echo "check-workflow-shell: linting $$# shell bodies"; \
	shellcheck --enable=all --exclude=SC2250,SC2292 "$$dir"/*.sh

.PHONY: fmt-scripts
fmt-scripts: ## rewrite scripts with ruff format
	@command -v uvx >/dev/null 2>&1 || { \
		echo "fmt-scripts: uvx not found on PATH (install uv $(UV_VERSION): https://docs.astral.sh/uv/getting-started/installation/)" >&2; \
		exit 1; \
	}
	uvx ruff@$(RUFF_VERSION) format scripts

# Advisory scan of the dependency graph, the same invocation vulnscan.yml
# runs on pull requests that touch go.mod or go.sum. The executable is pinned
# for reproducibility; it still reads the current advisory database. One of
# three network targets, with check-scripts and the first go command.
.PHONY: vuln
vuln: | test-tmpdir
vuln: ## scan dependencies for reachable vulnerabilities (what vulnscan.yml runs)
	GOFLAGS= $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(GOTAGS) ./...

.PHONY: install
install: build ## install into $(BINDIR) (BINDIR defaults to $(HOME)/.local/bin)
	install -d "$(BINDIR)"
	@# The binary being replaced is kept, under the same name `gauntlet
	@# update` gives its copy, so a locally built install can be rolled back
	@# the same way a released one is, unless it is the same binary: a second
	@# `make install` of an unchanged build would leave nothing to roll back to.
	@if [ -f "$(BINDIR)/$(BINARY)" ] && ! cmp -s "$(BINARY)" "$(BINDIR)/$(BINARY)"; then cp -p "$(BINDIR)/$(BINARY)" "$(BINDIR)/$(BINARY).previous"; fi
	install -m 0755 $(BINARY) "$(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *:"$(BINDIR)":*) ;; *) \
		echo "note: $(BINDIR) is not on PATH; add it so $(BINARY) can be found" >&2 ;; esac

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(DIST) $(BINARY) $(BINARY)_* .scratch

# A previous dist with a different VERSION or PLATFORMS must not leak into
# this one: release globs dist/gauntlet_* both into checksums.txt and the
# uploaded assets, so stale binaries here would ship as release artifacts.
# checksums.txt and sbom.json are rewritten by `release`; drop them here so
# `make dist` cannot leave a previous version's inventory beside new binaries.
#
# The four cross-compiles run concurrently: they share a build cache and
# nothing else, so serializing them made every dist and every release pay four
# compile passes where the slowest one bounds the whole target. Each build
# waits on its own pid and the target fails if any of them did, so parallel
# here does not become "ship whatever finished".
#
# The last loop is the check the asset name alone cannot give: GOOS/GOARCH are
# set per build, so a typo in PLATFORMS produced a correctly named binary for
# the wrong platform, and the smoke test only ever ran the host's. `go version
# -m` reads the platform out of the binary itself, so the name and the
# contents are compared instead of trusted. The microarchitecture level is in
# the same list: GOAMD64 and GOARM64 are exported above precisely because a
# `go env -w GOAMD64=v3` left behind once would compile the same source into
# different bytes, and the compiler records the level it used. The asset name
# cannot carry it, so the binary is the only place the claim can be checked.
# The experiment set is in that list for the same reason and the same way:
# GOEXPERIMENT is pinned empty above, so what the toolchain enables by default
# is what every asset has to record. It is compared only when the default is
# not empty, because `go version -m` prints no line for an empty one.
.PHONY: dist
dist: | toolchain
dist: ## build every release platform into dist/
	@mkdir -p $(DIST)
	@rm -f $(DIST)/$(BINARY)_* $(DIST)/checksums.txt $(DIST)/sbom.json $(DIST)/LICENSE
	@set -e; pids=; for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		name="$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		echo "building $$name"; \
		( CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			$(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD) ) & \
		pids="$$pids $$name:$$!"; \
	done; \
	status=0; for entry in $$pids; do \
		name=$${entry%:*}; \
		if wait "$${entry#*:}"; then echo "built $$name"; else echo "build failed: $$name" >&2; status=1; fi; \
	done; [ $$status -eq 0 ] || exit $$status
	@set -e; experiments="$$($(GO) env GOEXPERIMENT)"; for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		f="$(DIST)/$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		info=$$($(GO) version -m "$$f" 2>/dev/null) || { echo "dist: $$f is missing or not a Go binary" >&2; exit 1; }; \
		kv="GOOS=$$goos GOARCH=$$goarch"; \
		case "$$goarch" in \
			amd64) kv="$$kv GOAMD64=$(GOAMD64)" ;; \
			arm64) kv="$$kv GOARM64=$(GOARM64)" ;; \
		esac; \
		if [ -n "$$experiments" ]; then kv="$$kv GOEXPERIMENT=$$experiments"; fi; \
		for kv in $$kv; do \
			key=$${kv%%=*}; want=$${kv#*=}; \
			got=$$(printf '%s\n' "$$info" | awk -v k="$$key" -v v="$$want" '$$1 == "build" && $$2 == k "=" v { print v }'); \
			[ "$$got" = "$$want" ] || { echo "dist: $$f does not record $$kv, so it is not the binary its name claims" >&2; exit 1; }; \
		done; \
		echo "verified $$f ($$goos/$$goarch)"; \
	done

# The one place a release asset's name is computed. `gauntlet update` derives
# the same name from GOOS/GOARCH at runtime (internal/selfupdate), and both
# release workflows used to spell it out again in shell; asking the Makefile
# means a renamed asset fails one test rather than a job pointing at a file
# that no longer exists.
.PHONY: host-artifact
host-artifact: ## print the path dist/ uses for the binary built for this host
	@echo "$(DIST)/$(BINARY)_$(VERSION)_$$($(GO) env GOOS)_$$($(GO) env GOARCH)"

# Linking is not running: a host binary that builds and then refuses to start
# passes dist and fails only where it is used. Both workflows that build one
# run this target instead of the same block of shell twice, so the check a
# release is gated on is the one a contributor can run by hand before tagging.
.PHONY: smoke
smoke: ## run the host binary dist built and check the version it reports
	@binary="$$($(MAKE) --no-print-directory host-artifact VERSION=$(VERSION))"; \
	[ -x "$$binary" ] || { \
		echo "smoke: $$binary is missing or not executable; build it with 'make dist VERSION=$(VERSION)'" >&2; \
		exit 1; \
	}; \
	got="$$("$$binary" version)"; \
	echo "$$binary: $$got"; \
	[ "$$got" = "$(BINARY) $(VERSION)" ] || { \
		echo "smoke: $$binary reports \"$$got\", want \"$(BINARY) $(VERSION)\"" >&2; \
		exit 1; \
	}

# A release is a claim about a tag, and -buildvcs=false is what makes the
# binaries reproducible: the bytes carry no revision and no dirty flag, so a
# build from a modified tree is indistinguishable from a clean one, and the
# published assets would name source no reviewer read. Order-only, so the
# refusal comes before the suite and the four cross-compiles rather than
# after them.
#
# A source tarball is not a git work tree, and "clean" is a question only git
# can answer there; the tag and the pinned toolchain are the whole claim there,
# and `make dist` and `make repro` are the checks behind it.
.PHONY: clean-tree
clean-tree: ## fail unless the working tree has no uncommitted or untracked change
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0; \
	dirty=$$(git status --porcelain) || exit 1; \
	if [ -n "$$dirty" ]; then \
		echo "clean-tree: the working tree is not the tag, so assets built here would ship unreviewed source:" >&2; \
		printf '%s\n' "$$dirty" >&2; \
		echo "clean-tree: commit or discard the above, then run this again" >&2; \
		exit 1; \
	fi

# `check` is a prerequisite, not a separate CI step: the test suite compiles
# the tree but never vets it or checks its formatting, so without it a tag
# could ship a binary built from a tree that `make ci` rejects.
#
# if/else, not `cmd && sum || fallback`: a sha256sum that exists but fails
# mid-list would otherwise fall through to shasum and append a second,
# conflicting copy of the entries to checksums.txt.
#
# The inventory is CycloneDX JSON, read out of each built binary's own build
# info, so it names what shipped rather than what the tree would resolve to,
# and a scanner reads it without this Makefile. cmd/sbom uses the standard
# library alone: the artifact that describes the dependency surface must not
# add to it.
.PHONY: release
release: | clean-tree release-version check test dist artifacts ## build every platform and write dist/checksums.txt, dist/sbom.json and dist/LICENSE
	@echo "release artifacts in $(DIST)/ (upload every binary plus checksums.txt, sbom.json and LICENSE)"

# The files that sit beside the binaries rather than being them, split out of
# `release` so the dist job runs them on every push. Before this they were
# written only by a tagged release, so a change that broke the inventory, or a
# checksums.txt whose entries no longer matched what it names, reached a tag
# before anything noticed; `dist` and `smoke` in CI exercise the binaries alone.
# A file that verifies its own checksums can still be empty, so the recipe ends
# by refusing to report success for a missing or empty one: `test -s` over
# dist/sbom.json was otherwise a check CI ran and no make target reproduced.
# The verification and the test are each wrapped so their failure is the
# recipe's own: a command substitution or a `||` chain whose status the next
# `;` discards reports success over a checksum that never matched.
#
# LICENSE is the third, copied out of the tree rather than built: a binary
# released under the AGPL travels with the text of the grant it is offered
# under, and a consumer who downloads one over a link has no other way to read
# the terms without the repository beside it. It is not in checksums.txt, which
# names the binaries `gauntlet update` downloads, and the release attestations
# cover the binaries too, so it is a document beside the release rather than an
# artifact of it.
.PHONY: artifacts
artifacts: dist ## write dist/checksums.txt, dist/sbom.json and dist/LICENSE from the built binaries
	@install -m 0644 LICENSE $(DIST)/LICENSE
	@set -e; if command -v sha256sum >/dev/null 2>&1; then \
		cd $(DIST) && sha256sum $(BINARY)_* > checksums.txt; \
	else \
		cd $(DIST) && shasum -a 256 $(BINARY)_* > checksums.txt; \
	fi
	$(GO) run ./cmd/sbom -o $(DIST)/sbom.json -version $(VERSION) $(DIST)/$(BINARY)_*
	@cd $(DIST) || exit 1; \
	{ sha256sum -c checksums.txt 2>/dev/null || shasum -a 256 -c checksums.txt; } >/dev/null || { \
		echo "artifacts: $(DIST)/checksums.txt does not match the binaries it names" >&2; \
		exit 1; \
	}; \
	for f in checksums.txt sbom.json LICENSE; do \
		[ -s "$$f" ] || { echo "artifacts: $(DIST)/$$f is missing or empty" >&2; exit 1; }; \
	done; \
	echo "artifacts: checksums.txt, sbom.json and LICENSE in $(DIST)/"

# The same source must produce the same bytes wherever it is built: -trimpath
# strips build paths, nothing in a Go binary embeds a timestamp, and
# -buildvcs=false keeps git metadata out, so two builds from different
# directories, locale, timezone, and git state are byte-identical. This
# target proves it instead of asserting it: two full copies of the tree, one
# built pinned to C/UTC, one under the ambient environment, then cmp. Side b
# strips LC_ALL and TZ rather than inheriting the C and UTC this Makefile
# exports, so the second build really does see the host's locale and zone. The
# tree is archived to a file then extracted twice: a tar pipe would hide a
# failing create behind a successful extract (POSIX sh has no pipefail). The archive is the whole working tree minus what .gitignore
# covers, so anything a build or a run leaves behind would otherwise be copied
# into two trees under $HOME. That is why `.gauntlet/` and `.gauntlet.lock`
# are ignored (a run of this tool in its own checkout leaves a lane worktree
# per job and its lock behind) and why `.env` is: the tokens .env.example is a
# template for. One of the copies would then build from state the other does
# not have.
#
# The members come from git's own ignore-aware listing, handed to tar as file
# names, not from tar `--exclude` patterns. libarchive matches a pattern with
# no slash against the basename of every path component, so a pattern like
# `--exclude=./gauntlet` also drops `cmd/gauntlet` and the copies cannot
# build, while GNU tar anchors its patterns and never did: the two
# implementations archived different trees. A name list has no dialect. It
# also reaches into subdirectories, where an anchored `./__pycache__` exclude
# never did, and a new `.gitignore` entry covers the archive the moment it is
# written, which is what TestMakefileReproArchiveMirrorsGitignore holds the
# recipe to. A tracked path missing from the working tree makes the tar step
# fail, which is the honest reading of an archive of the working tree.
#
# Every platform in PLATFORMS is checked, not just the host's: those are the
# binaries `dist` ships, and a reproducibility claim that covers one of four
# proves nothing about the other three. CI runs it on every push.
#
# The two files that sit beside the binaries are compared too, and each copy
# writes them under the release asset name rather than a bare binary name, so
# the glob that fills checksums.txt reads the same names here as it does in
# `artifacts`. A release ships six files, not four: checksums.txt and sbom.json
# are built from the binaries and read by `gauntlet update` and by a scanner, so
# a claim covering only the binaries leaves two published artifacts unverified.
# sbom.json is the interesting one, since its serial number is hashed from what
# it describes rather than drawn at random and its licenses are resolved out of
# the module cache: that is a claim about determinism, and this is where it is
# checked. The stamp is VERSION, the same value `dist` is handed, so the two
# targets describe the same release.
#
# The two copies of a platform build at the same time and the platforms stay
# in sequence, so a mismatch is still reported next to the platform it belongs
# to. `set -e` carries the recipe rather than a `&&` chain: `cmd && (build) &`
# would background the whole preceding list, archive and all.
#
# Each copy gets its own GOCACHE, because a shared one lets the second build
# reuse the first build's compiled objects: -trimpath makes the two builds
# hash to the same cache key, so a build that leaked its own directory would
# hand copy b the object copy a compiled, and cmp would compare a with a and
# pass. GOMODCACHE stays shared, since it holds downloads verified by go.sum
# and nothing path-dependent is built from it.
# `:=` for the reason TMPDIR has it, and the recipe below rm -rf's this path
# before building: an exported REPRO_DIR would otherwise choose it.
REPRO_DIR := $(HOME)/.cache/gauntlet/repro

.PHONY: repro
repro: | toolchain
repro: ## verify reproducibility: build twice from different paths/locale/TZ, compare
	@test "$(REPRO_DIR)" != "/.cache/gauntlet/repro" || { echo "HOME is unset; set HOME or REPRO_DIR to a disk-backed directory" >&2; exit 1; }
	@set -e; \
		rm -rf "$(REPRO_DIR)"; \
		mkdir -p "$(REPRO_DIR)/a/dist" "$(REPRO_DIR)/b/dist"; \
		trap 'rm -rf "$(REPRO_DIR)"' EXIT; \
		git ls-files -z --cached --others --exclude-standard > "$(REPRO_DIR)/src.files" && \
		tar -cf "$(REPRO_DIR)/src.tar" --null -T "$(REPRO_DIR)/src.files" && \
		for side in a b; do \
			tar -C "$(REPRO_DIR)/$$side" -xf "$(REPRO_DIR)/src.tar" || exit 1; \
		done; \
		for target in $(PLATFORMS); do \
			goos=$${target%/*}; goarch=$${target#*/}; \
			asset="$(BINARY)_$(VERSION)_$$goos_$$goarch"; \
			echo "repro: $$target (copy a: LC_ALL=C TZ=UTC, copy b: ambient locale and TZ)"; \
			(cd "$(REPRO_DIR)/a" && GOCACHE="$(REPRO_DIR)/a.gocache" CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch TZ=UTC LC_ALL=C \
				$(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/$$asset $(CMD)) & \
			pa=$$!; \
			(cd "$(REPRO_DIR)/b" && GOCACHE="$(REPRO_DIR)/b.gocache" CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch env -u LC_ALL -u TZ \
				$(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/$$asset $(CMD)) & \
			pb=$$!; \
			wait $$pa || exit 1; wait $$pb || exit 1; \
			cmp "$(REPRO_DIR)/a/dist/$$asset" "$(REPRO_DIR)/b/dist/$$asset" || exit 1; \
		done; \
		for side in a b; do \
			(cd "$(REPRO_DIR)/$$side/dist" && if command -v sha256sum >/dev/null 2>&1; then \
				sha256sum $(BINARY)_* > checksums.txt; \
			else \
				shasum -a 256 $(BINARY)_* > checksums.txt; \
			fi) || exit 1; \
			(cd "$(REPRO_DIR)/$$side" && $(GO) run ./cmd/sbom -o dist/sbom.json -version $(VERSION) dist/$(BINARY)_$(VERSION)_*) \
				|| exit 1; \
		done; \
		for f in checksums.txt sbom.json; do \
			cmp "$(REPRO_DIR)/a/dist/$$f" "$(REPRO_DIR)/b/dist/$$f" || exit 1; \
		done; \
		echo "repro: identical bytes from different paths, locales, and timezones"

# A release is cut from a tag, and the tag is the one thing that names the
# version: the workflow derives it from GITHUB_REF_NAME and hands it to both
# `release` and `smoke`. `VERSION` still defaults to `dev` so a contributor's
# `make build` needs no version, and a `make release` that inherited that
# default would build a complete, self-consistent, wrong release: assets named
# gauntlet_dev_darwin_arm64, a binary reporting `gauntlet dev`, and a
# `make smoke VERSION=dev` that passes, because it compares the stamp against
# the same placeholder it was handed. The pull-request dist job is not
# affected: it builds `VERSION=ci`, which is a version like any other and ships
# nothing.
#
# Order-only where `release` names it, so the refusal comes before the suite
# and the four cross-compiles rather than after them, and so it sits below
# every line docs/THREAT_MODEL.md points at.
.PHONY: release-version
release-version:
	@case "$(VERSION)" in \
		""|dev) \
			echo "release-version: VERSION='$(VERSION)' is the local default; a release is built from a tag: 'make release VERSION=x.y.z'" >&2; \
			exit 1;; \
	esac

# The first command a contributor runs on a new machine has to answer the whole
# "what is missing" question, and today no single one does: the Go minimum is
# caught by toolchain-min, the C compiler by test-cgo, and uvx and shellcheck
# by check-scripts, which `make verify` reaches only after `make check` has run.
# A machine missing two of them learns about the second one by installing the
# first and running the loop again, which is an afternoon per missing tool.
#
# Every check below reports the tool, what to install, and which targets need
# it, and the failure is counted rather than raised at the first gap: a
# contributor who is told one missing thing at a time re-runs this once per
# tool, which is the loop this target exists to remove. The exit status is 1
# when anything is missing, so a script can gate on it, and the checks that
# passed are still printed, because a target that works is worth saying so.
#
# The Go comparison, the Go version to name, and the shellcheck pin are the
# ones toolchain-min and check-scripts already use, so the three cannot report
# three different answers about one machine.
#
# tar, cmp, and a checksum tool are the other three, and no other target
# preflights them: repro archives the tree with tar and compares the binaries
# with cmp, and artifacts writes and verifies dist/checksums.txt with one, so a
# machine missing them learns it from a raw tar or shasum error after the
# release-path targets have already been read. `install` belongs here for the
# same reason and was the one gap left: `make install` writes the binary with
# it and compares the one it replaces with cmp, and neither was named anywhere
# but the recipe that needed them.
.PHONY: doctor
doctor: ## report every missing prerequisite in one run, with what to install
	@missing=0; \
	ok() { printf '  ok      %s\n' "$$1"; }; \
	bad() { printf '  MISSING %s\n            install: %s\n            needed by: %s\n' "$$1" "$$2" "$$3" >&2; missing=$$((missing + 1)); }; \
	min=$$(awk '$$1 == "go" { print $$2; exit }' go.mod 2>/dev/null); \
	if [ -z "$$min" ]; then \
		echo "doctor: no go.mod in $$(pwd), so there is no Go minimum to check against" >&2; \
		exit 1; \
	fi; \
	if ! command -v "$(GO)" >/dev/null 2>&1; then \
		bad "Go $$min or newer" "install $$min or newer from https://go.dev/dl/" "build, run, install, and every check and test target"; \
	else \
		have=$$($(GO) env GOVERSION); have=$${have#go}; have=$${have%%-*}; \
		if awk -v have="$$have" -v want="$$min" 'BEGIN { \
			split(have, h, "."); split(want, w, "."); \
			for (i = 1; i <= 3; i++) { \
				if (h[i] + 0 > w[i] + 0) { exit 0 } \
				if (h[i] + 0 < w[i] + 0) { exit 1 } \
			} \
			exit 0 \
		}'; then \
			ok "go $$have (go.mod asks for $$min or newer)"; \
		else \
			bad "Go $$min or newer" "install $$min or newer from https://go.dev/dl/ , or pass GOTOOLCHAIN=auto to let go fetch it" "build, run, install, and every check and test target"; \
		fi; \
	fi; \
	cc=cc; command -v "$(GO)" >/dev/null 2>&1 && cc=$$($(GO) env CC); \
	if command -v "$$cc" >/dev/null 2>&1; then \
		ok "C compiler $$cc (the race detector needs cgo)"; \
	else \
		bad "a C compiler ($$cc)" "install GCC or Clang; on macOS: xcode-select --install" "test, test-pkg, cover, ci, verify"; \
	fi; \
	if command -v git >/dev/null 2>&1; then \
		ok "git $$(git --version 2>/dev/null | awk '{print $$3}')"; \
	else \
		bad "git" "install git" "every target: the worktrees a run cuts live under .gauntlet/worktrees in the reviewed repository"; \
	fi; \
	if [ "$$(uname -s)" = Darwin ]; then \
		if [ -x /usr/bin/sandbox-exec ]; then \
			ok "Seatbelt launcher /usr/bin/sandbox-exec"; \
		else \
			bad "/usr/bin/sandbox-exec" "restore the macOS system Seatbelt launcher" "run, test, test-pkg, cover, ci, verify"; \
		fi; \
	fi; \
	if command -v uvx >/dev/null 2>&1; then \
		ok "uvx (uv $$(uv --version 2>/dev/null | awk '{print $$2}'), CI pins $(UV_VERSION))"; \
	else \
		bad "uvx" "install uv $(UV_VERSION): https://docs.astral.sh/uv/getting-started/installation/" "check-scripts, fmt-scripts, verify"; \
	fi; \
	if command -v shellcheck >/dev/null 2>&1; then \
		ok "shellcheck $$(shellcheck --version 2>/dev/null | awk '/^version:/ {print $$2}') (pin $(SHELLCHECK_VERSION))"; \
	else \
		bad "shellcheck" "macOS: brew install shellcheck; Linux: your package manager ships it as shellcheck" "check-scripts, verify"; \
	fi; \
	if mkdir -p "$(TMPDIR)" 2>/dev/null; then \
		ok "test scratch directory $(TMPDIR)"; \
	else \
		bad "a writable disk-backed test scratch directory" "set TMPDIR on the make command line; tests must not use a tmpfs or an ignored path inside this repository" "test, test-pkg, cover, ci, verify"; \
	fi; \
	if command -v tar >/dev/null 2>&1 && command -v cmp >/dev/null 2>&1; then \
		ok "tar and cmp (repro archives the tree twice and compares the binaries)"; \
	else \
		bad "tar and cmp" "both are in the base system on Linux and macOS; a trimmed container image is the usual gap" "repro"; \
	fi; \
	if command -v install >/dev/null 2>&1; then \
		ok "install (make install writes the binary with it, and cmp keeps the one it replaces)"; \
	else \
		bad "install" "coreutils on Linux, part of the base system on macOS; a minimal container image drops it" "install"; \
	fi; \
	if command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1; then \
		ok "sha256sum or shasum (artifacts writes and verifies dist/checksums.txt with it)"; \
	else \
		bad "sha256sum or shasum" "coreutils on Linux, shasum from perl on macOS" "artifacts, release"; \
	fi; \
	if [ "$$missing" -gt 0 ]; then \
		echo "doctor: $$missing prerequisite(s) missing; the targets above still work without them" >&2; \
		exit 1; \
	fi; \
	echo "doctor: every prerequisite is present. Start with 'make build', then 'make test-pkg PKG=./internal/<package>'."
