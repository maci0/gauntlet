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

## Safety-critical review family (MISRA, CERT, Power of Ten)

**Problem.** The bundled prompts review code the way a senior engineer would:
find the bug, apply the fix. Firmware, avionics, medical, and automotive work
is judged against a written standard instead: MISRA C:2012 and its amendments,
AUTOSAR C++14, CERT C/C++, JPL/NASA Power of Ten, and the process standards
above them (DO-178C, ISO 26262, IEC 62304). None of that fits "find something
worth fixing": conformance is per-rule, per-line, and every departure needs a
recorded justification rather than a quiet edit.

**Why it fits anyway.** Everything about a review here is already a prompt
plus a set: `misra-review.md`, `cert-review.md`, `poweroften-review.md`, a
`critical` set that runs them together, and a repo can override any of them
with its own file. The parts that do not fit are the interesting ones:

- **Rule text is licensed.** MISRA's guidelines cannot be embedded in this
  repo. The prompt can cite rule numbers and describe intent in its own words,
  and a team with a licensed copy can drop the real text into
  `misra-review.md` in their own tree, where prompt discovery already picks it
  up over the bundled one.
- **The deterministic half belongs to a checker.** `cppcheck --addon=misra`,
  `clang-tidy` with `cert-*`/`misra-*`, or a commercial analyzer will always
  beat an agent at "line 412 violates 10.3". Where an agent is genuinely
  better is the half no checker does: triaging the report, separating true
  violations from noise, and drafting the deviation record a standard requires
  (rule, location, rationale, risk, sign-off). So the review should run the
  checker (it already may run any tool) and hand the agent its output.
- **Editing certified code is the wrong default.** These reviews want an
  advisory mode: report and justify, change nothing. Today that means a prompt
  that forbids edits, which is a rule the agent could break; a real
  `--no-edit` (snapshot the tree, refuse the run's commits, diff at the end)
  would enforce it. `--stacked-prs` is the other half, and it already ships: a
  traceable change, reviewed by a human, is the only kind such a project can
  take.
- **Traceability is already there.** The run journal records which agent,
  which model, which commit, and what changed, and each launch carries the
  SHA-256 of the exact prompt text it ran under (`prompt_sha256` on
  `review_start`/`review_end`), so a bundled or project prompt can be cited
  by content even after its file moves on.

**What would need deciding.** Whether "conformance" outcomes deserve their own
result kind next to ok/fail/timeout (a review that finds 40 violations is not
a failure), and whether deviation records belong in the tree (a
`deviations/` directory the next run reads back) or only in the journal.

## The launcher as the bare `gauntlet` default

The launcher shipped as `gauntlet pick`: reviews as collapsible sets with a
fill meter each, suggest with its own agent picker, the agent pool, the run
switches, concurrency metered against the CPU count, and the composed command
line on screen the whole time, run through the real parser on `enter`.

What never shipped is pointing a bare `gauntlet` at it. Typing `gauntlet`
with no flags still starts reviewing the current directory with every prompt
and whatever agents are installed, forever: right for someone who knows the
tool, a wall of work for everyone else.

**What would need deciding.** Whether a bare `gauntlet` on a TTY opens the
launcher (never when stdout is a pipe, under `--quiet`, or with `TERM=dumb`),
or keeps starting the run. Changing what a bare `gauntlet` does is a change
to documented behavior, so it waits for a minor version and an explicit
escape hatch (`--yes` to take the defaults without a screen).

Smaller pieces left out of the shipped launcher, each worth having once the
default question is settled:

- Agents are shown with their model field and unavailable ones greyed with
  the reason.
- A directories pane: add paths and globs, mark trees that are dirty (a
  `--jobs > 1` run needs a clean tree), and show the product, "4 jobs x 3
  directories = up to 12 agents at once".
- Recent runs from `~/.gauntlet/index.jsonl`, which already records the argv,
  offered as one-key presets.
- A key that copies the composed command line.
