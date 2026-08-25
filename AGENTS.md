# gauntlet: project conventions

The Go implementation of gauntlet: ~50 review prompts dispatched to installed AI coding
agents, applying fixes to the working tree. One static binary, prompts
embedded, bubbletea for the dashboard, everything else standard library.

## Build and test

- `make check`: gofmt, `go fix -diff`, and vet. All must be clean; run
  `go fix ./...` before committing if the fix step reports anything.
- `make test`: the suite with the race detector and shuffled order.
- Tests must not write into a tmpfs or into a gitignored path inside this
  repo: the prompt discovery tests would then see their own fixtures as
  ignored. `TMPDIR` is set by the Makefile for that reason.

## Layout

`cmd/gauntlet` is flags and dispatch only. Everything real lives in
`internal/`, and dependencies point one way: `runner` uses `agent`, `prompt`,
`normalize`, `gitx`, `journal`; `ui` uses only the runner's event types.
Nothing imports `ui`, so a headless run costs nothing.

## Rules that are not style preferences

- **Concurrency in one repository requires isolation.** Reviews run
  sequentially in place, or in a `git worktree` per review with a merge step.
  There is no third mode, and no flag that lets two agents share a tree.
- **Never fake data in the dashboard.** Missing is missing (`n/a`, `~`), an
  unlit meter shows its remainder, and no series is smoothed or interpolated.
  See `docs/DESIGN.md`.
- **Containment is prompt text plus process discipline**, and both are ported
  verbatim from the original. Changing the rule files in
  `internal/prompt/rules/` changes what agents are allowed to do: treat those
  files as security-relevant.
- **Untrusted input** is anything from the reviewed repository: prompt names,
  descriptions, agent output. Sanitize before display, never interpolate into
  a prompt without fencing.
- **A conflicting merge keeps its branch.** Losing a review's entire output
  silently is worse than a noisy failure.

## Docs

- `docs/DESIGN.md` records the architecture and what each decision costs.
- `docs/IDEAS.md` records what was deliberately not built, and why. Move an
  entry out of it when it ships; do not leave both.
