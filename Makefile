BINARY  := gauntlet
CMD     := ./cmd/gauntlet
DIST    := dist
VERSION ?= dev

GO      ?= go
LDFLAGS := -s -w -X main.version=$(VERSION)

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
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

.PHONY: run
run: build ## build, then run one loop here with the dashboard
	./$(BINARY) --once --tui

.PHONY: test
test: ## run all tests with the race detector, shuffled order
	@mkdir -p $(TMPDIR)
	$(GO) test -race -shuffle=on ./...

.PHONY: cover
cover: ## test coverage summary
	@mkdir -p $(TMPDIR) $(DIST)
	$(GO) test -race -shuffle=on -coverprofile=$(DIST)/coverage.out ./... && \
		$(GO) tool cover -func=$(DIST)/coverage.out | tail -1

.PHONY: vet
vet: ## run go vet
	$(GO) vet ./...

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
	$(GO) fix -diff ./...
	$(GO) vet ./...

.PHONY: install
install: build ## install into ~/.local/bin
	install -d ~/.local/bin
	install -m 0755 $(BINARY) ~/.local/bin/$(BINARY)

.PHONY: clean
clean: ## remove build artifacts
	rm -rf $(DIST) $(BINARY)

# Asset names and checksums.txt are the contract `gauntlet update` verifies
# against: see internal/selfupdate.AssetName. Changing either breaks
# self-update for every installed binary.
.PHONY: dist
dist: ## build every release platform into dist/
	@mkdir -p $(DIST)
	@for target in $(PLATFORMS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		name="$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$$name $(CMD) || exit 1; \
	done

.PHONY: release
release: test dist ## build every platform and write dist/checksums.txt
	@cd $(DIST) && { command -v sha256sum >/dev/null 2>&1 && sha256sum $(BINARY)_* || shasum -a 256 $(BINARY)_*; } > checksums.txt
	@echo "release artifacts in $(DIST)/ (upload every binary plus checksums.txt)"
