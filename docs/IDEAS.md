# Ideas

Not committed to, not scheduled. Each entry says what problem it solves, so a
future reader can judge whether the problem still exists.

## Worktree setup hook

**Problem.** `--jobs N` runs each review in a fresh `git worktree`. Many repos
do not build in a bare checkout: local `.env` files, `node_modules`,
virtualenvs, and build caches are untracked, so they are absent. The agent's
verification step then fails for reasons unrelated to the review, and the
review either wastes its budget or reports a false problem.

**Shape of the fix.** `--worktree-setup CMD`, run once in each new checkout
before the agent starts (symlink `node_modules`, copy `.env`, warm a cache).
Needs a timeout of its own and a clear failure mode: a setup that fails should
skip that review, not run it in a broken tree.

## Conflict resolution by agent

Today a merge conflict aborts the merge and keeps the branch for a human. The
alternative is to hand the conflict to an agent as its own task, with its own
prompt, containment rules, and timeout. Worth doing only once there is data on
how often conflicts actually happen across real runs (the journal will tell).

## PR-per-review workflow

The worktree machinery already produces one branch per review. Stopping before
the merge and pushing instead gives one PR per review: `gh pr create` with a
body generated from the review's own `RESULT:`/`PATH:` protocol lines, a label
per review name, and a `--pr-base` flag. Open question: what happens when 50
reviews open 50 PRs on a busy repo. Batching by review set, or one PR with all
branches merged into it, may be the better shape.

## Cross-run learning from the journal

Every run already writes JSONL to `~/.gauntlet/runs/`. That is enough history
to answer: which reviews never find anything in this repo (drop them from the
default set), which agent is fastest per review kind (bias the lane
assignment), which reviews time out repeatedly (raise their budget). All of it
is offline analysis over files that already exist; none of it needs a service.

## Per-review timeout budgets

One global `--timeout` is blunt: `doc-review` rarely needs 30 minutes,
`arch-review` on a large tree often needs more. The journal has the data to
propose per-review defaults (p95 of past successful runs).

## Token throughput out of the network layer

Transcript tailing and stream parsing have shipped for every agent with a
readable store or a machine-readable mode; `docs/TOKEN_TELEMETRY.md` records
the per-agent coverage. What was never built is reading the number out of the
TLS stream itself, by proxy or eBPF uprobe. The design and its real costs
(privileges, fragility, plaintext source code in kernel buffers) are written
up at the end of `docs/TOKEN_TELEMETRY.md`.
