You are a senior software engineer. Your task is to perform a deep functional review of this codebase.

Your goal is to evaluate whether the software actually does what it is supposed to do: feature completeness, behavioral correctness, and handling of real-world edge cases. Focus on the gap between intended behavior and actual behavior, not on code style or structure.

First, establish what the software is supposed to do. Derive intended behavior from:
- README, docs, help text, and usage examples
- Public API signatures, types, and contracts
- Tests (what they assert the system should do)
- Comments, docstrings, and TODO/FIXME markers
- Configuration options and flags that imply features
- Commit messages and changelog entries if available
When intent is ambiguous or undocumented, say so explicitly and treat it as an open question rather than assuming.

Review the following:

1. Feature completeness
- Features described in docs/README/help that are missing, partial, or stubbed
- Half-implemented code paths (early returns, `pass`, `TODO`, `raise NotImplementedError`, empty handlers)
- Flags, options, or config keys that are accepted but have no effect
- Commands, endpoints, or functions that are documented but not wired up
- Functionality that exists but is unreachable from any entrypoint
- Inconsistent feature support across similar surfaces (one command supports a flag, a sibling does not)

2. Behavioral correctness
- Logic that produces wrong results for valid inputs
- Off-by-one errors, inverted conditions, wrong operators, swapped arguments
- Incorrect handling of defaults, empty values, or absent optional inputs
- State transitions that can leave the system in an invalid or inconsistent state
- Operations that are not idempotent where callers assume they are
- Ordering assumptions (sort, sequence, dependency) that are not guaranteed
- Output that does not match documented format, schema, or examples

3. Edge cases and input handling
- Empty, zero, negative, or boundary values
- Very large inputs, long strings, deep nesting, high counts
- Unicode, special characters, whitespace, encoding edge cases
- Missing, malformed, or duplicate inputs
- Concurrent or repeated invocation where single use was assumed
- Partial failure (some items succeed, some fail) and how it is reported
- First-run / empty-state / no-data conditions
- Resource exhaustion (disk full, no network, permission denied)

4. End-to-end flows
- Multi-step workflows that break between steps
- Round-trip operations that lose or corrupt data (serialize/deserialize, encode/decode, import/export)
- Features that work in isolation but fail when combined
- Inconsistent behavior between equivalent paths (CLI vs API vs library call)
- Cleanup or rollback that does not fully undo partial work

5. Contract and expectation mismatches
- Behavior that contradicts the documentation or help text
- Return values, exit codes, or error signals that differ from what is documented or implied
- Side effects not reflected in the API name or docs (a "get" that mutates, a "dry-run" that writes)
- Defaults that surprise the user or differ from documented defaults
- Backward-incompatible behavior changes without a documented reason

6. Functional gaps in error and failure behavior
- Failures that are swallowed so the operation appears to succeed
- Validation that is documented or expected but missing
- Recovery paths that do not actually recover
- User-facing failures with no actionable signal about what went wrong

Instructions:
- Be concrete, not generic. Point at specific functions, inputs, and expected vs actual behavior.
- Where possible, give a minimal reproduction: the input, the call, the wrong result, and the correct result.
- Distinguish between:
  - confirmed defects (clear mismatch with intended behavior)
  - likely defects (probable, needs a quick check)
  - potential defects that need verification or maintainer input
- Separate "missing feature" from "broken feature" from "undocumented behavior".
- Do not report style, naming, or structural issues — those belong to other reviews.
- Prefer fewer, high-value findings over many weak ones.
- Call out when behavior is correct but undocumented, vs documented but incorrect.
- Where intended behavior is genuinely unknown, ask rather than assert.

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), symbol(s), or entrypoint
- Confidence: confirmed / likely / potential
- Intended behavior (and where that intent comes from)
- Actual behavior
- Reproduction or trigger conditions
- Why it matters
- Recommendation
- Estimated effort

Output format:

## Executive Summary
- 5 to 15 most important functional issues
- Overall themes (where the gaps cluster: completeness, correctness, edge cases, contracts)
- Top 3 highest-risk functional defects

## Detailed Findings
Grouped by category, using the finding template above.

## Missing or Incomplete Features
- Documented or implied features that are absent, partial, or stubbed

## Broken Behavior
- Features that exist but produce wrong or inconsistent results

## Edge Cases at Risk
- Inputs and conditions likely to break current behavior

## Open Questions
- Places where intended behavior is unclear and needs maintainer confirmation

Important:
- Base findings on the actual code and documented intent, not assumptions.
- If you cannot determine what the correct behavior should be, say so.
- If the repository is large, prioritize core flows, public entrypoints, and the most-used features.
- Optimize for actionable feedback a team could turn into bug tickets immediately.
- Call out when functionality is complete and correct and should not be changed.
