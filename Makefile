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
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## build the gauntlet binary for this host
	$(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: run
run: build ## build, then run one loop here with the dashboard
	./$(BINARY) --once --tui

.PHONY: test
test: ## run all tests with the race detector, shuffled order
	@mkdir -p $(TMPDIR)
	$(GO) test $(GOTAGS) -race -shuffle=on ./...

.PHONY: cover
cover: ## test coverage summary
	@mkdir -p $(TMPDIR) $(DIST)
	$(GO) test $(GOTAGS) -race -shuffle=on -coverprofile=$(DIST)/coverage.out ./... && \
		$(GO) tool cover -func=$(DIST)/coverage.out | tail -1

.PHONY: vet
vet: ## run go vet
	$(GO) vet $(GOTAGS) ./...

# Package directories only: the Go tool already ignores dot-directories, but
# gofmt walks everything, including scratch fixtures.
GOFILES = $(shell $(GO) list -f '{{.Dir}}' ./...)

.PHONY: fmt
fmt: ## rewrite all Go files with gofmt
	gofmt -s -w $(GOFILES)

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
	$(GO) fix -diff -tags notoktop ./...
	$(GO) fix -diff ./...
	$(GO) vet $(GOTAGS) ./...
	$(GO) vet -tags notoktop ./...
	$(GO) vet ./...

.PHONY: install
install: build ## install into ~/.local/bin
	install -d ~/.local/bin
	install -m 0755 $(BINARY) ~/.local/bin/$(BINARY)

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(DIST) $(BINARY)

# Release artifacts are the binaries, checksums.txt (the contract `gauntlet
# update` verifies against: see internal/selfupdate.AssetName), and sbom.txt, a
# module inventory read back out of each binary with `go version -m`: versions
# and hashes of everything that shipped. Changing asset names or checksums.txt
# breaks self-update for every installed binary.
.PHONY: dist
dist: ## build every release platform into dist/
	@mkdir -p $(DIST)
	@for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		name="$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			$(GO) build $(GOTAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD) || exit 1; \
	done

.PHONY: release
release: test dist ## build every platform and write dist/checksums.txt and dist/sbom.txt
	@cd $(DIST) && { command -v sha256sum >/dev/null 2>&1 && sha256sum $(BINARY)_* || shasum -a 256 $(BINARY)_*; } > checksums.txt
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
