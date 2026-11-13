<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img src="assets/logo-light.svg" alt="gauntlet" width="420">
  </picture>
</p>

<p align="center">
  <em>Run your codebase through the gauntlet: 56 specialized review prompts,
  dispatched to whichever AI coding agents you have installed, applying fixes
  directly to the working tree.</em>
</p>

<p align="center">
  <a href="https://github.com/maci0/gauntlet/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/maci0/gauntlet/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/maci0/gauntlet/releases/latest"><img alt="latest release" src="https://img.shields.io/github/v/release/maci0/gauntlet?color=6aa84f"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/badge/license-AGPL--3.0-blue"></a>
  <img alt="platforms" src="https://img.shields.io/badge/platform-linux%20%7C%20macOS-lightgrey">
  <img alt="go" src="https://img.shields.io/github/go-mod/go-version/maci0/gauntlet">
</p>

<p align="center">
  <img src="assets/dashboard.png" alt="The gauntlet dashboard: an activity chart, one lane per agent with live token rates, the review grid, and a normalized feed" width="900">
</p>

One static binary loops 56 review prompts over your repository, hands each one
to an agent CLI you already have installed, and applies what it finds.

- **Isolation when it runs in parallel.** `--jobs N` gives N persistent lane
  worktrees, each assigned its share of the loop's reviews up front, each
  review on its own branch, merged back one at a time. Concurrent agents never
  share a working tree.
- **Unmerged review stacks.** `--stacked-prs` runs reviews in one isolated
  worktree and opens each changed review as a child PR of the previous one.
  The branch in your editor is never changed.
- **Many repositories at once.** `--dirs a,b,c` reviews several trees, each
  with its own lock, baseline, and lane. `--jobs` applies within each.
- **A dashboard.** `--tui` puts lanes, throughput, the review grid, and a
  normalized feed on one screen.
- **Readable output.** Spinner frames, escape sequences, repainted progress
  lines, tool gutters, and duplicate narration are collapsed rather than
  echoed, per agent.
- **Measured, never guessed.** Token counts come from what the agent prints,
  its session transcript, and its machine-readable mode; an agent that reports
  nothing shows no rate.
- **A record.** Every run is JSONL under `~/.gauntlet`, replayable with
  `gauntlet show`. A run an OOM kill or a crash cut off continues with
  `gauntlet resume`.
- **Self-updating, hot-reloading.** A running loop takes a new binary between
  reviews without losing its counters.

## Quick start

Check which installed agents gauntlet can use, then compose a run:

```sh
gauntlet doctor
gauntlet pick
```

Run one isolated, unmerged PR stack from `main`:

```sh
gauntlet -r quick --stacked-prs --pr-base main
```

## Install

Install the latest release binary:

```sh
mkdir -p ~/.local/bin
ver=$(curl -fsSL https://api.github.com/repos/maci0/gauntlet/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
asset="gauntlet_${ver}_$(uname -s | tr A-Z a-z)_$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)"
base="https://github.com/maci0/gauntlet/releases/download/v${ver}"
# Download under the name checksums.txt lists, verify that file, and only then
# install it as gauntlet. The subshell aborts on the first failure, so a
# mismatch leaves nothing executable behind.
(
	set -e
	cd ~/.local/bin
	curl -fsSL "$base/$asset" -o "$asset"
	curl -fsSL "$base/checksums.txt" -o checksums.txt
	awk -v a="$asset" '$2 == a || $2 == "*"a' checksums.txt > asset.sha256
	if [ ! -s asset.sha256 ]; then
		echo "install failed: checksums.txt has no entry for $asset" >&2
		exit 1
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c asset.sha256
	else
		shasum -a 256 -c asset.sha256
	fi
	rm -f asset.sha256 checksums.txt
	mv "$asset" gauntlet
	chmod +x gauntlet
)
```

`~/.local/bin` must be on `PATH`. Most Linux environments already put it
there; on macOS, add it.

Or build and install from source:

```sh
make install                     # into ~/.local/bin
make install BINDIR=/usr/local/bin   # or wherever the machine keeps binaries
```

Check or update the installation:

```sh
gauntlet doctor
gauntlet update
```

`doctor` probes the machine rather than the platform name: the git version,
and whether this host can confine an agent's filesystem writes (Landlock on
Linux, Seatbelt on macOS). A host that cannot runs with `--no-sandbox`.

Coming from the Python `gauntlet-review` package: the flags are the same.
Remove it with `uv tool uninstall gauntlet-review` (or `pipx uninstall
gauntlet-review`).

Linux and macOS only: the runner needs process groups, `flock`, `O_NOFOLLOW`,
and `execve`, which Windows has no equivalent for.
Git 2.24 or newer: branch and revision arguments are separated with
`--end-of-options`, which older git rejects.

Supported agents: `claude`, `codex`, `gemini`, `qwen`, `grok`, `agy`,
`cursor-agent`, `kimi`, `opencode`, `crush`, `clanker`, `dsh`, `microagent`,
the pi family (`pi`, `prime-agent`, `feynman`, `omp`), and GitHub Copilot
(`copilot`). `clanker`, `omp`, and `copilot` run only when named with
`--agents`. At least one agent must be on `PATH`. Any other agent is added
without a new binary: `--agent-cmd` for one run, `~/.gauntlet/agents.json` to
keep it (see
[docs/CLI.md](docs/CLI.md#defining-an-agent-gauntlet-does-not-ship)).

## The reviews

Every review is one prompt file. Run all of them, a named set (`-r quick`,
`-r standard`, `-r security`, `-r backend`, `-r frontend`, `-r shipping`,
`-r agents`, `-r gitops`, plus the discovered `all` and `project`), or any
list you like. `gauntlet --list` prints this table with what is scheduled.

<!-- BEGIN REVIEWS: generated from internal/prompt/prompts, checked by TestReadmeGridMatchesBundled -->
| Review | Finds |
| --- | --- |
| `a11y-review` | keyboard, screen readers, contrast, motion, WCAG 2.2 AA |
| `agentrules-review` | the CLAUDE.md and AGENTS.md files every session pays for |
| `api-review` | REST, GraphQL, and gRPC surfaces: consistency, correctness |
| `arch-review` | module boundaries, dependency direction, layering |
| `authz-review` | IDOR, unprotected admin routes, cross-tenant leakage |
| `build-review` | reproducible, hermetic builds and a pinned toolchain |
| `cache-review` | stale reads, cross-tenant bleed, stampedes, growth |
| `cli-review` | exit codes, stdout vs stderr, piping, --help, clispec |
| `code-review` | maintainability, correctness, consistency, simplicity |
| `compat-review` | path separators, bashisms, endianness, musl vs glibc |
| `concurrency-review` | races, deadlocks, and corruption under load |
| `config-review` | precedence, validation, secrets in the wrong place |
| `container-review` | probes, graceful shutdown, image hygiene on Kubernetes |
| `db-review` | schema, queries, migrations, data integrity |
| `ddd-review` | bounded contexts, domain language, aggregates, tactical DDD |
| `deps-review` | necessity, supply-chain risk, maintenance burden |
| `design-review` | tradeoffs, alternatives, data modeling, fit to scale |
| `doc-review` | docs and comments that disagree with the code |
| `dr-review` | what is backed up, and has restore ever been proven |
| `dst-review` | can a single seed replay the whole run byte-for-byte |
| `dx-review` | clone to passing tests without losing an afternoon |
| `error-review` | silent data loss, misleading behavior, cascading failure |
| `functionality-review` | the gap between intended and actual behavior |
| `fuzz-review` | untrusted-input surfaces with no fuzz harness |
| `gitops-review` | Argo CD / Flux delivery: pinning, sync, drift, secrets |
| `helm-review` | chart templating, values contract, hooks, CRDs |
| `i18n-review` | translations, locale formatting, layout in other scripts |
| `idempotency-review` | what happens when the operation runs twice |
| `infra-review` | CI/CD, IaC, deployment, environment wiring |
| `k8s-review` | manifests, kustomize structure, API deprecations |
| `lint-review` | linters present, strict enough, and blocking CI |
| `llm-review` | prompt injection, cost, and unvalidated model output |
| `minimalism-review` | prove every line is needed, or delete it |
| `mobile-review` | lifecycle, offline, battery, permissions, store readiness |
| `numerics-review` | money in floats, truncating casts, negative modulo |
| `o11y-review` | logs, metrics, traces, alerts, and whether they connect |
| `perf-review` | algorithms, memory, I/O, startup: measured, not guessed |
| `perfectionism-review` | the missing half, the last case, the known shortcut |
| `pkg-review` | deb, rpm, Flatpak, wheels, images: what actually ships |
| `privacy-review` | personal data collected, shared, retained, deleted |
| `prompt-review` | whether these prompts work as instructions to an agent |
| `release-review` | semver honesty, breaking changes, changelog, migration |
| `resource-review` | handles, goroutines, listeners, maps that only grow |
| `sdk-review` | the integrating developer's experience of the surface |
| `sec-review` | exploitable vulnerabilities and missing controls |
| `skills-review` | does the skill fire, teach, and stay cheap when loaded |
| `slop-review` | machine-written noise: dead code, restating comments |
| `specs-review` | requirements and decision records the code contradicts |
| `tdd-review` | concrete bugs fixed through red, green, refactor |
| `test-review` | tests that give false confidence or miss real bugs |
| `threat-review` | attack surface, trust boundaries, a living threat model |
| `time-review` | DST, leap days, wall-clock durations, cron that misfires |
| `uislop-review` | the interchangeable generated look, no visual identity |
| `unicode-review` | encodings, NFC/NFD, length in the wrong units |
| `ux-review` | friction and confusion from the user's side |
| `webperf-review` | bytes on the wire, blocking the first paint |
<!-- END REVIEWS -->

## How a run works

Reviews run one at a time against your working tree, forever, until you stop
them. `--jobs N` buys parallelism with isolation instead: N persistent lane
worktrees split each loop's reviews round-robin when the loop starts, each
review on its own branch. The runner commits each one and merges them back
one at a time.

Stack mode is sequential for a different reason: every review must see the
commits below it. From base `main`, changed reviews produce `review/01-… → main`,
then `review/02-… → review/01-…`, and so on. The PRs stay open. The scratch
checkout is removed after the pass pushes at least one layer and every changed
layer has a confirmed PR; otherwise it is kept and its path is logged. The
remote base is fetched without moving your local branch. If the original
checkout has uncommitted files, gauntlet names them
and asks before leaving them out of the review. See
[stacked pull requests](docs/RUNS.md#stacked-pull-requests) for the branch
lifecycle and recovery rules.

```mermaid
flowchart LR
    T[your branch<br/>clean tree] --> S[loop schedule<br/>review i to lane i mod N]
    S --> L0[lane-0]
    S --> L1[lane-1]
    S --> L2[lane-2]
    L0 --> C[runner commits]
    L1 --> C
    L2 --> C
    C --> M[merge --squash<br/>one at a time]
    M --> T2[your branch]
    M -. conflict .-> R[agent resolves<br/>in a scratch checkout]
    R -. merged .-> T2
    R -. unresolved .-> K[branch kept<br/>run exits nonzero]
    classDef tree fill:#e0f1f4,stroke:#0e96a8,color:#0b4a55
    classDef work fill:#fdf3d9,stroke:#b8891b,color:#5c4210
    classDef step fill:#e6f0e6,stroke:#3f7d3f,color:#1f3d1f
    classDef fail fill:#fbe4e6,stroke:#c0392b,color:#6b1f18
    class T,T2 tree
    class S,L0,L1,L2,R work
    class C,M step
    class K fail
```

`--jobs` counts per directory, so `--dirs a,b,c -j 4` is up to 12 agents at
once. A branch that will not merge is handed to an agent, which resolves it in
a scratch checkout; what it cannot resolve stays on its branch for you
(`--resolve-conflicts=false` skips the attempt). Everything lands
on the branch you are already on; `--merge-into BRANCH` is what carries a
loop's committed work anywhere else. The details, plus the run journal and hot
reload, are in [docs/RUNS.md](docs/RUNS.md).

## Dashboard

`--tui` renders the screen at the top of this page: header, activity chart,
one lane per agent, the full review grid, and the feed. `gauntlet pick`
composes a run before it starts, and shows the command it is building while
you do:

<p align="center">
  <img src="assets/launcher.png" alt="The gauntlet launcher: review sets with descriptions, the installed agents, concurrency against the CPU count, and the composed command line" width="900">
</p>

`?` on either screen lists its keys; both key tables and the review glyphs
are in [docs/CLI.md](docs/CLI.md#dashboard-keys).

## Trust model

Agents run with filesystem writes sandboxed by default (Landlock on Linux,
Seatbelt on macOS); their permission prompts are disabled. Prime Agent may
send work to a pre-existing daemon outside that sandbox; see
[the sandbox documentation](docs/CLI.md#filesystem-write-sandbox). The other
controls: prompts are read with `O_NOFOLLOW` and size-capped; agent binaries
resolve on a `PATH` without cwd-relative entries; git runs with
`core.fsmonitor` and `diff.external` blanked, `core.hooksPath=/dev/null`,
`core.pager=cat`, and the reviewed repository's own signing program ignored;
untrusted text is stripped of control and bidi characters before display;
and a `flock` on `.gauntlet.lock`, plus an in-process registry of held locks
(macOS converts a second `flock` instead of failing it), keeps two runs out of
one directory.

Reviewing a repository you do not trust still means running it in a container.
The boundaries are drawn one by one in
[docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).

## Token counts

Counts come from what the agent prints, its machine-readable mode
(`--stream`), its own session transcript, and, for the two agents that keep
databases instead (crush, opencode), those. The release build reads all of
them; `make build TAGS=` drops the SQLite driver, and `make build
TAGS=notoktop` drops transcript reading as well. `gauntlet doctor` says which
build you have. opencode's store holds every project on the machine, so it is
read only with `--opencode-db`. Per-agent coverage and the public reading API
are in [docs/TOKEN_TELEMETRY.md](docs/TOKEN_TELEMETRY.md).

## Documentation

| Page | What is in it |
|---|---|
| [docs/CLI.md](docs/CLI.md) | every flag, environment variable, and exit code; defining your own agent |
| [docs/RUNS.md](docs/RUNS.md) | worktree isolation and merging, the run journal, updating and hot reload |
| [docs/DESIGN.md](docs/DESIGN.md) | architecture, concurrency model, and what each decision costs |
| [docs/TOKEN_TELEMETRY.md](docs/TOKEN_TELEMETRY.md) | where token counts come from, per agent, and the public reading API |
| [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) | trust boundaries, what is validated where, and what is out of scope |
| [docs/IDEAS.md](docs/IDEAS.md) | things deliberately not built yet, and why |
| [CONTRIBUTING.md](CONTRIBUTING.md) | building, testing, and the checks a pull request runs |

## License

AGPL-3.0-or-later.
