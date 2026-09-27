BINARY  := gauntlet
CMD     := ./cmd/gauntlet
DIST    := dist
VERSION ?= dev

GO      ?= go
GOFMT   ?= $(shell $(GO) env GOROOT)/bin/gofmt
LDFLAGS := -s -w -X main.version=$(VERSION)

# Honor go.sum: a missing or extra module must fail the command rather than
# rewrite the manifests. `make vuln` clears this because govulncheck is not a
# build input.
export GOFLAGS += -mod=readonly
export GOWORK := off
export GOTOOLCHAIN := local
export GOAMD64 := v1
export GOARM64 := v8.0

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

# Release artifacts must not depend on the build host's locale: the shell
# orders glob expansion with strcoll, so checksums.txt and sbom.txt would
# list assets in a different order on hosts with a different LC_COLLATE.
export LC_ALL := C

# Tests must not write into a tmpfs (RAM) or into an ignored path inside this
# repo, which would make prompt discovery see its own fixtures as ignored.
#
# `:=`, not `?=`: make gives an exported environment variable the same status
# as a command-line one, and `?=` keeps it. TMPDIR is exported on most Linux
# shells and by launchd on macOS, usually pointing at the tmpfs the rule above
# exists to avoid, and setting it in the environment was then silently
# ignored. A command line (`make test TMPDIR=...`) still wins.
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
# string comes from main.version, and sbom.txt is the inventory.
.PHONY: build
build: | toolchain-min
build: ## build the gauntlet binary for this host
	CGO_ENABLED=0 $(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: run
run: build ## build, then run one loop here with the dashboard
	./$(BINARY) --once --tui

# RUN is a go test -run pattern (default: every test in the package).
RUN ?=
# test-pkg only. `make test` always runs the whole tree, so it takes no package
# argument, and a PKG= passed there would have nothing to select from.
PKG ?=

# `go test -run` exits 0 when the pattern selects nothing, so a mistyped test
# name reads as a pass and the edit-test loop loses an iteration before the
# contributor notices. tee keeps the per-package lines streaming; the status
# file carries go test's own exit code, which a pipeline would drop, because
# this Makefile is POSIX sh and has no pipefail.
define RUN_TESTS
	@log="$(TMPDIR)/test.$$$$.log"; \
	{ TMPDIR="$(TMPDIR)" CGO_ENABLED=1 $(GO) test $(GOTAGS) -race -shuffle=on -run '$(RUN)' $(1) 2>&1; \
	echo $$? >"$$log.status"; } | tee "$$log"; \
	rc=$$(cat "$$log.status"); \
	if [ "$$rc" -ne 0 ]; then rm -f "$$log" "$$log.status"; exit "$$rc"; fi; \
	if [ -n "$(RUN)" ] && ! awk '/^ok / && $$0 !~ /no tests to run/ { ran = 1 } END { exit !ran }' "$$log"; then \
		echo "test: no test matches RUN='$(RUN)'; go test reports success when a -run pattern selects nothing" >&2; \
		echo "test: list the candidates with 'make test-pkg PKG=$(PKG)' (no RUN), or 'go test $(GOTAGS) -list . $(PKG)'" >&2; \
		rm -f "$$log" "$$log.status"; exit 1; \
	fi; \
	rm -f "$$log" "$$log.status"
endef

.PHONY: test
test: | toolchain-min test-tmpdir test-cgo
test: ## run all tests with the race detector, shuffled order
	$(call RUN_TESTS,./...)

# One package at a time keeps the edit-test loop fast; the flags match `make
# test` so a green package here stays green in the full run.
.PHONY: test-pkg
test-pkg: | toolchain-min test-tmpdir test-cgo
test-pkg: ## run one package's tests: make test-pkg PKG=./internal/prompt [RUN=TestName]
	@case "$(PKG)" in ""|./...) \
		echo "test-pkg needs a package: make test-pkg PKG=./internal/prompt [RUN=TestName]" >&2; \
		echo "test-pkg: PKG is empty, so it would run every package under a target that promises one" >&2; \
		exit 1 ;; \
	esac
	$(call RUN_TESTS,$(PKG))

.PHONY: cover
cover: | toolchain-min test-tmpdir test-cgo
cover: ## test coverage summary, gated by COVER_MIN
	@mkdir -p $(DIST)
	TMPDIR="$(TMPDIR)" CGO_ENABLED=1 $(GO) test $(GOTAGS) -race -shuffle=on -coverprofile=$(DIST)/coverage.out ./...
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
vet: ## run go vet
	$(GO) vet $(GOTAGS) ./...

# Release artifacts, and only those: `make build`, `make test`, and `make
# check` run on whatever toolchain a contributor has, which is right. A tagged
# release is a different question, since the compiler version is recorded in
# every binary it ships and the next patch release of Go would change those
# bytes. The suffix a vendor toolchain carries (`-X:nodwarf5`) is not part of
# the release, so only the goX.Y.Z prefix is compared. `?=` means an explicit
# `make dist GO_VERSION=x.y.z` still overrides, which is how a maintainer ships
# a deliberate toolchain bump without editing this file first.
.PHONY: toolchain
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
check: | toolchain-min
check: ## verify formatting, toolchain fixes, and vet (CI parity)
	@test -x "$(GOFMT)" || { echo "gofmt not found at $(GOFMT); install Go or set GOFMT to this toolchain's gofmt" >&2; exit 1; }
	@test -n "$(GOFILES)" || { echo "go list returned no packages" >&2; exit 1; }; \
		unformatted=$$("$(GOFMT)" -s -l $(GOFILES)) || exit 1; \
		if [ -n "$$unformatted" ]; then \
			echo "needs gofmt:"; echo "$$unformatted"; exit 1; \
		fi
# The three documented build modes are checked: sqlite+toktop, notoktop, and
# no tags at all (transcripts without database readers). CI tests all three; the
# analysis step must see the same set or a mode only it compiles goes unvetted.
	$(GO) fix -diff -tags sqlite ./...
	$(GO) fix -diff ./...
	$(GO) fix -diff -tags notoktop ./...
	$(GO) vet -tags sqlite ./...
	$(GO) vet ./...
	$(GO) vet -tags notoktop ./...

# The Go half of a pull request: analysis across all three tag sets, then
# the race suite. The scripts job is separate (check-scripts) because it
# needs uvx and shellcheck, which a Go-only change does not.
.PHONY: ci
ci: check test ## Go pull-request checks: fmt, fix, vet, and the test suite

# Everything ci.yml runs on a pull request, in one command, on one host. It is
# the answer to "would this be green after push", and it is deliberately not
# the edit-test loop: three race suites in a row is minutes, not seconds. The
# three legs are recursive makes rather than three prerequisites, so a failing
# tag stops the run and names the leg instead of continuing to the next one.
# make cover is left out: it reruns the sqlite suite the first leg already ran,
# and its floor is a CI measurement (see COVER_MIN).
.PHONY: verify
verify: check check-scripts ## everything a pull request runs, locally: check, all three tag legs, scripts lint
	$(MAKE) test
	$(MAKE) test TAGS=
	$(MAKE) test TAGS=notoktop

# Local mirror of ci.yml's scripts job, including the pins. uvx fetches
# those tools on first use; shellcheck stays a PATH binary because that is
# what the Ubuntu runner already has, so its pin is checked rather than
# installed and a mismatch is a note, not a failure. The workflow
# definitions are linted with the same uvx pins as the Python tools, since a
# malformed one is a syntax error the Go build never sees.
.PHONY: check-scripts
check-scripts: ## ruff, mypy --strict, and yamllint --strict, plus shellcheck (CI parity)
	@command -v uvx >/dev/null 2>&1 || { \
		echo "check-scripts: uvx not found on PATH (install uv $(UV_VERSION): https://docs.astral.sh/uv/getting-started/installation/)" >&2; \
		echo "CI runs: uvx ruff@$(RUFF_VERSION) check scripts" >&2; \
		echo "         uvx ruff@$(RUFF_VERSION) format --check scripts" >&2; \
		echo "         uvx --with rich==$(RICH_VERSION) mypy@$(MYPY_VERSION) --strict scripts" >&2; \
		echo "         uvx yamllint@$(YAMLLINT_VERSION) --strict .github" >&2; \
		echo "         shellcheck --enable=check-extra-masked-returns scripts/shots.sh" >&2; \
		exit 1; \
	}
	@command -v shellcheck >/dev/null 2>&1 || { \
		echo "check-scripts: shellcheck not found on PATH (CI uses the Ubuntu runner's copy)" >&2; \
		exit 1; \
	}
	@got_uv=$$(uv version 2>/dev/null | awk '{print $$2}'); \
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
	shellcheck --enable=check-extra-masked-returns scripts/shots.sh

.PHONY: fmt-scripts
fmt-scripts: ## rewrite scripts with ruff format
	@command -v uvx >/dev/null 2>&1 || { \
		echo "fmt-scripts: uvx not found on PATH (install uv $(UV_VERSION): https://docs.astral.sh/uv/getting-started/installation/)" >&2; \
		exit 1; \
	}
	uvx ruff@$(RUFF_VERSION) format scripts

# Advisory scan of the dependency graph, the same invocation vulnscan.yml
# runs on pull requests that touch go.mod or go.sum. The executable is pinned
# for reproducibility; it still reads the current vulnerability database.
# Needs network on first use; everything else in this Makefile does not.
.PHONY: vuln
vuln: ## scan dependencies for reachable vulnerabilities (what vulnscan.yml runs)
	GOFLAGS= $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(GOTAGS) ./...

.PHONY: install
install: build ## install into ~/.local/bin
	install -d "$(HOME)/.local/bin"
	install -m 0755 $(BINARY) "$(HOME)/.local/bin/$(BINARY)"
	@case ":$$PATH:" in *:"$(HOME)/.local/bin":*) ;; *) \
		echo "note: $(HOME)/.local/bin is not on PATH; add it so $(BINARY) can be found" >&2 ;; esac

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(DIST) $(BINARY) $(BINARY)_* .scratch

# A previous dist with a different VERSION or PLATFORMS must not leak into
# this one: release globs dist/gauntlet_* both into checksums.txt and the
# uploaded assets, so stale binaries here would ship as release artifacts.
# checksums.txt and sbom.txt are rewritten by `release`; drop them here so
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
# contents are compared instead of trusted.
.PHONY: dist
dist: | toolchain
dist: ## build every release platform into dist/
	@mkdir -p $(DIST)
	@rm -f $(DIST)/$(BINARY)_* $(DIST)/checksums.txt $(DIST)/sbom.txt
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
	@set -e; for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		f="$(DIST)/$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		info=$$($(GO) version -m "$$f" 2>/dev/null) || { echo "dist: $$f is missing or not a Go binary" >&2; exit 1; }; \
		for kv in GOOS=$$goos GOARCH=$$goarch; do \
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

# `check` is a prerequisite, not a separate CI step: the test suite compiles
# the tree but never vets it or checks its formatting, so without it a tag
# could ship a binary built from a tree that `make ci` rejects.
#
# if/else, not `cmd && sum || fallback`: a sha256sum that exists but fails
# mid-list would otherwise fall through to shasum and append a second,
# conflicting copy of the entries to checksums.txt.
.PHONY: release
release: check test dist ## build every platform and write dist/checksums.txt and dist/sbom.txt
	@set -e; if command -v sha256sum >/dev/null 2>&1; then \
		cd $(DIST) && sha256sum $(BINARY)_* > checksums.txt; \
	else \
		cd $(DIST) && shasum -a 256 $(BINARY)_* > checksums.txt; \
	fi
	@set -e; cd $(DIST) && for f in $(BINARY)_*; do \
		echo "## $$f"; \
		$(GO) version -m "$$f"; \
	done > sbom.txt
	@echo "release artifacts in $(DIST)/ (upload every binary plus checksums.txt and sbom.txt)"

# The same source must produce the same bytes wherever it is built: -trimpath
# strips build paths, nothing in a Go binary embeds a timestamp, and
# -buildvcs=false keeps git metadata out, so two builds from different
# directories, locale, timezone, and git state are byte-identical. This
# target proves it instead of asserting it: two full copies of the tree, one
# built pinned to C/UTC, one under the ambient environment, then cmp. Side b
# strips LC_ALL rather than inheriting the C this Makefile exports, so the
# second build really does see the host's locale; TZ is genuinely ambient
# either way. The tree is archived to a file then extracted twice: a tar pipe
# would hide a failing create behind a successful extract (POSIX sh has no
# pipefail). `.gauntlet/` is excluded because a run of this tool in its own
# checkout leaves a lane worktree per job there, and a reproducibility check
# that copies a second checkout of the tree is not one. Every platform in
# PLATFORMS is checked, not just the host's:
# those are the binaries `dist` ships, and a reproducibility claim that covers
# one of four proves nothing about the other three. CI runs it on every push.
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
		mkdir -p "$(REPRO_DIR)/a" "$(REPRO_DIR)/b"; \
		trap 'rm -rf "$(REPRO_DIR)"' EXIT; \
		tar --exclude=./.git --exclude=./$(DIST) --exclude=./$(BINARY) --exclude=./$(BINARY)_* \
			--exclude=./.scratch --exclude=./.ruff_cache --exclude=./.mypy_cache \
			--exclude=./__pycache__ --exclude=./.gauntlet \
			-cf "$(REPRO_DIR)/src.tar" . && \
		for side in a b; do \
			tar -C "$(REPRO_DIR)/$$side" -xf "$(REPRO_DIR)/src.tar" || exit 1; \
		done; \
		for target in $(PLATFORMS); do \
			goos=$${target%/*}; goarch=$${target#*/}; \
			echo "repro: $$target (copy a: LC_ALL=C TZ=UTC, copy b: ambient locale and TZ)"; \
			(cd "$(REPRO_DIR)/a" && GOCACHE="$(REPRO_DIR)/a.gocache" CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch TZ=UTC LC_ALL=C \
				$(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)) & \
			pa=$$!; \
			(cd "$(REPRO_DIR)/b" && GOCACHE="$(REPRO_DIR)/b.gocache" CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch env -u LC_ALL \
				$(GO) build $(GOTAGS) -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)) & \
			pb=$$!; \
			wait $$pa || exit 1; wait $$pb || exit 1; \
			cmp "$(REPRO_DIR)/a/$(BINARY)" "$(REPRO_DIR)/b/$(BINARY)" || exit 1; \
		done; \
		echo "repro: identical bytes from different paths, locales, and timezones"
