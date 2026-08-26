BINARY  := gauntlet
CMD     := ./cmd/gauntlet
DIST    := dist
VERSION ?= dev

GO      ?= go
LDFLAGS := -s -w -X main.version=$(VERSION)

# Reading an agent's own session transcript is on by default: it lives in
# toktop, costs one pure-Go dependency, and is the only source of counts for
# agents that print none. `sqlite` is on for the same reason: crush and
# opencode keep their counters in databases rather than transcripts, and the
# driver is pure Go, so cross-compilation is unaffected.
#
# TAGS=notoktop drops transcript reading; TAGS= drops the database driver too,
# leaving a gauntlet that depends on nothing but the standard library and
# reads only what agents print.
TAGS    ?= sqlite
GOTAGS  := $(if $(TAGS),-tags $(TAGS),)

# Release artifacts must not depend on the build host's locale: the shell
# orders glob expansion with strcoll, so checksums.txt and sbom.txt would
# list assets in a different order on hosts with a different LC_COLLATE.
export LC_ALL := C

# Tests must not write into a tmpfs (RAM) or into an ignored path inside this
# repo, which would make prompt discovery see its own fixtures as ignored.
export TMPDIR ?= $(HOME)/.cache/gauntlet/test
# Every go command inherits TMPDIR, and go refuses to start when it does not
# exist, so it is created at parse time rather than per target: a fresh CI
# runner has no such directory.
$(shell mkdir -p $(TMPDIR))

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
.PHONY: build
build: ## build the gauntlet binary for this host
	CGO_ENABLED=0 $(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: run
run: build ## build, then run one loop here with the dashboard
	./$(BINARY) --once --tui

.PHONY: test
test: ## run all tests with the race detector, shuffled order
	@mkdir -p $(TMPDIR)
	$(GO) test $(GOTAGS) -race -shuffle=on ./...

# One package at a time keeps the edit-test loop fast; the flags match `make
# test` so a green package here stays green in the full run. RUN is a go test
# -run pattern (default: every test in the package).
PKG ?= ./...
RUN ?=

.PHONY: test-pkg
test-pkg: ## run one package's tests: make test-pkg PKG=./internal/prompt [RUN=TestName]
	@mkdir -p $(TMPDIR)
	$(GO) test $(GOTAGS) -race -shuffle=on -run '$(RUN)' $(PKG)

.PHONY: cover
cover: ## test coverage summary, gated by COVER_MIN
	@mkdir -p $(TMPDIR) $(DIST)
	$(GO) test $(GOTAGS) -race -shuffle=on -coverprofile=$(DIST)/coverage.out ./...
	@total=$$($(GO) tool cover -func=$(DIST)/coverage.out | awk '/^total:/ {print $$3}'); \
		echo "total coverage: $$total (floor $(COVER_MIN)%)"; \
		awk -v got="$${total%\%}" -v min="$(COVER_MIN)" 'BEGIN { \
			if (got + 0 < min + 0) { \
				printf "coverage fell to %s%%, below the %s%% floor\n", got, min > "/dev/stderr"; exit 1 \
			} }' 

# The coverage floor. It ratchets: raise it when a change carries coverage up,
# never lower it to make a change fit. `make cover` fails below it, so a
# refactor that quietly drops a tested path is caught where it happens.
COVER_MIN ?= 76.0

.PHONY: vet
vet: ## run go vet
	$(GO) vet $(GOTAGS) ./...

# Package directories only: the Go tool already ignores dot-directories, but
# gofmt walks everything, including scratch fixtures.
GOFILES = $(shell $(GO) list -f '{{.Dir}}' ./...)

.PHONY: fmt
fmt: ## rewrite all Go files with gofmt
	gofmt -s -w $(GOFILES)

# CI tests all three tag configurations (see the matrix in ci.yml); check
# compiles each of them so a break under one of them fails here and not
# after push. The bare pass is the third configuration: neither tag defined.
.PHONY: check
check: ## verify formatting, toolchain fixes, and vet (CI parity)
	@unformatted=$$(gofmt -s -l $(GOFILES)); \
		if [ -n "$$unformatted" ]; then \
			echo "needs gofmt:"; echo "$$unformatted"; exit 1; \
		fi
# The three documented build modes are checked: sqlite+toktop, notoktop, and
# no tags at all (the stdlib-only binary TAGS= ships). CI tests all three; the
# analysis step must see the same set or a mode only it compiles goes unvetted.
	$(GO) fix -diff $(GOTAGS) ./...
	$(GO) fix -diff ./...
	$(GO) fix -diff -tags notoktop ./...
	$(GO) vet $(GOTAGS) ./...
	$(GO) vet ./...
	$(GO) vet -tags notoktop ./...

# Advisory scan of the dependency graph, the same invocation vulnscan.yml
# runs on pull requests that touch go.mod or go.sum. Unpinned for the reason
# stated there: a scanner is worth only what its advisory database knows,
# and its result never becomes a build input to gauntlet itself. Needs
# network on first use; everything else in this Makefile does not.
.PHONY: vuln
vuln: ## scan dependencies for reachable vulnerabilities (what vulnscan.yml runs)
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: install
install: build ## install into ~/.local/bin
	install -d ~/.local/bin
	install -m 0755 $(BINARY) ~/.local/bin/$(BINARY)

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(DIST) $(BINARY)

# Release artifacts are the binaries, checksums.txt (the contract `gauntlet
# update` verifies against: see internal/selfupdate.assetName), and sbom.txt, a
# module inventory read back out of each binary with `go version -m`: versions
# and hashes of everything that shipped. Changing asset names or checksums.txt
# breaks self-update for every installed binary.
.PHONY: dist
dist: ## build every release platform into dist/
	@mkdir -p $(DIST)
	# A previous dist with a different VERSION or PLATFORMS must not leak into
	# this one: release globs dist/gauntlet_* both into checksums.txt and the
	# uploaded assets, so stale binaries here would ship as release artifacts.
	@rm -f $(DIST)/$(BINARY)_*
	@for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		name="$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			$(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD) || exit 1; \
	done

.PHONY: release
release: test dist ## build every platform and write dist/checksums.txt and dist/sbom.txt
	# if/else, not `cmd && sum || fallback`: a sha256sum that exists but fails
	# mid-list would otherwise fall through to shasum and append a second,
	# conflicting copy of the entries to checksums.txt.
	@if command -v sha256sum >/dev/null 2>&1; then \
		cd $(DIST) && sha256sum $(BINARY)_* > checksums.txt; \
	else \
		cd $(DIST) && shasum -a 256 $(BINARY)_* > checksums.txt; \
	fi
	@for f in $(DIST)/$(BINARY)_*; do \
		echo "## $$f"; \
		$(GO) version -m "$$f"; \
	done > $(DIST)/sbom.txt
	@echo "release artifacts in $(DIST)/ (upload every binary plus checksums.txt and sbom.txt)"

# The same source must produce the same bytes wherever it is built: -trimpath
# strips build paths and nothing in a Go binary embeds a timestamp, so two
# builds from different directories under different locale and timezone are
# byte-identical. This target proves it instead of asserting it: two full
# copies of the tree, one built pinned to C/UTC, one under the ambient
# environment, then cmp. CI runs it on every push.
REPRO_DIR ?= $(HOME)/.cache/gauntlet/repro

.PHONY: repro
repro: ## verify reproducibility: build twice from different paths/locale/TZ, compare
	@rm -rf "$(REPRO_DIR)" && mkdir -p "$(REPRO_DIR)/a" "$(REPRO_DIR)/b" && \
		trap 'rm -rf "$(REPRO_DIR)"' EXIT && \
		for side in a b; do \
			tar --exclude=./.git --exclude=./$(DIST) --exclude=./$(BINARY) --exclude=./$(BINARY)_* \
				-cf - . | tar -C "$(REPRO_DIR)/$$side" -xf - || exit 1; \
		done && \
		echo "repro: copy a (LC_ALL=C TZ=UTC)" && \
		(cd "$(REPRO_DIR)/a" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 TZ=UTC LC_ALL=C \
			$(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY)_linux_amd64 $(CMD)) && \
		echo "repro: copy b (ambient locale and TZ)" && \
		(cd "$(REPRO_DIR)/b" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
			$(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY)_linux_amd64 $(CMD)) && \
		cmp "$(REPRO_DIR)/a/$(BINARY)_linux_amd64" "$(REPRO_DIR)/b/$(BINARY)_linux_amd64" && \
		echo "repro: identical bytes from different paths, locales, and timezones"
