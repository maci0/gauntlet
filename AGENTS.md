# gauntlet — project conventions

gauntlet is a dependency-free Python review loop: ~50 specialized review
prompts dispatched to installed AI coding agents, applying small fixes
directly to the working tree. Zero runtime dependencies (stdlib only);
packaging in `pyproject.toml`, tests in `tests/`.

## Build & test

- `uv run pytest` — run the suite (tests/test_gauntlet.py). Keep it green.
- `uv run ruff check .` — lint. No code is merged without both.

## Docs: PRD / RFC / ADR roles

gauntlet ships no product docs of its own, but its prompts review other
repos' PRD/RFC/ADR sets, and this repo's own docs and prompts must keep the
same industry-standard boundaries:

- **PRD** — product requirements: what to build and why (problem →
  requirements → acceptance).
- **RFC** — request for comments: the technical proposal (the "how"),
  circulated for review before the decision is locked. Options + a
  recommendation, not a settled spec.
- **ADR** — records a decision that has been made (context → decision →
  consequences); immutable — a reversal supersedes, never edits. A decision
  still being made is an RFC, not a proposed ADR.

Enforcement lives in the review prompts: `specs-review.md` audits ADR/RFC/PRD
sets on reviewed repos (ADR statuses accepted/superseded/deprecated, RFC as
proposal, PRD as requirements), and `agentrules-review.md` flags agent rules
that misstate the roles. When editing these prompts — or any gauntlet doc —
keep the roles above: never reintroduce a "proposed ADR" or an RFC that
records a decision.
