You are a senior software engineer. Your task is to perform a deep code quality review of this codebase.

Your goal is to produce a practical, high-signal review focused on maintainability, correctness, consistency, and simplicity.

Review the following:

1. Inconsistencies
(network API consistency belongs to api-review; published library surface to sdk-review; here own in-module naming and pattern consistency.)
- Naming inconsistencies
- API/design inconsistencies inside a module
- Style inconsistencies
- Different patterns used for the same problem
- Mismatches between similar modules/components

2. Duplication
(slop-review owns near-verbatim copy-paste and cosmetic duplication; here own duplicated logic that has drifted: copies that now disagree.)
- Duplicated code
- Near-duplicated logic
- Repeated patterns that should be abstracted
- Copy-paste code with minor variations that has since diverged
- Repeated validation, parsing, error handling, or data transformation logic

3. Dead or unnecessary code
(slop-review owns unused parameters, always-true guards, and commented-out blocks; minimalism-review owns project-wide unused-symbol deletion. Here only unused imports and obviously dead locals in a file you already have open. Unused tests: flag, never delete.)
- Unused imports and dead locals in a file already open
- Code paths that appear unreachable in that same file
- Over-engineered abstractions that provide little value
- Wrappers/helpers that do not meaningfully simplify anything

4. Opportunities to reduce lines of code
(slop-review owns local verbose constructs and wrappers; minimalism-review owns project-wide necessity proofs. Here only simplify logic in a function you already have open because it is hard to follow.)
- Places where logic can be simplified
- Boilerplate that can be removed
- Repeated branches that can be merged
- Verbose code that can be replaced with clearer, smaller constructs
- Unnecessary indirection

5. Refactoring opportunities
(arch-review owns module-scale separation of concerns. Here own function-scale structure inside one file.)
- Functions that are too long or do too many things
- Poor separation of concerns
- Confusing control flow
- Weak naming
- Hidden assumptions
- Tight coupling
- Data structures that make the code harder to understand
- Interfaces that could be made clearer or smaller
- Opportunities to improve expressiveness and readability without changing behavior

6. Code clarity and expressiveness
- Places where intent is unclear
- Magic values
- Unclear ownership or lifecycle
- Hard-to-follow state changes
- Excessive nesting
- Unhelpful abstractions
- Patterns that obscure rather than clarify

7. Error handling patterns
(Deep error-handling/resilience analysis belongs to error-review. Do not change error types, propagation, retries, or cleanup; here only make inconsistent patterns match an existing one in the same module.)
- Inconsistent error handling strategies in the same module (throw vs return vs ignored)
- Inconsistent use of error types, error codes, or result types in the same module

8. Type safety and contracts
(error-review owns validation of external or user input at boundaries; api-review owns request validation on network endpoints. Here own type-level contracts: overly broad types, unsafe casts, optional-as-present.)
- Functions that accept overly broad types (any, object, interface{}) when specific types exist
- Missing null or undefined checks on values that can be absent (add a check only when a concrete path produces the absent value)
- Type assertions or casts that could fail at runtime
- Implicit contracts between modules that are not enforced by types or assertions
- Optional values treated as always present
- Inconsistent use of type narrowing or discriminated unions

9. Risky areas
(functionality-review owns intended-vs-actual and documented edge cases. Here only a local logic error you can prove from the function body without consulting docs: inverted condition, swapped arguments, off-by-one.)
- Suspicious logic or possible bugs visible in the function body
- Implicit behavior that should be explicit
- Race conditions or shared mutable state (note only; concurrency-review owns the fix)
- Assumptions about ordering, uniqueness, or data shape that are not validated
- Code that works by coincidence rather than by design

Instructions:
- Fix order: type-safety gaps and missing null checks with a concrete path > duplication causing drift > inconsistent patterns. Cosmetic cleanups last.
- If available, use: the project's own linter first (`ruff`, `clippy`, `eslint`), `jscpd` (duplication). `vulture`/`knip`/`ts-prune` may confirm an unused import in a file you already have open; do not treat their project-wide reports as a deletion list (minimalism-review). The linter's own config, strictness, and suppressions belong to lint-review. Never install tools.
- Do not hunt comment noise, copy-paste style, unused parameters, or visual genericness (slop-review, uislop-review). Do not run a project-wide unused-symbol deletion pass (minimalism-review); unused imports in a file you already have open are in scope.
- Do not edit review prompts, SKILL.md, or agent rule files (prompt-review, skills-review, agentrules-review). Do not edit THREAT_MODEL.md or SECURITY.md (threat-review). Do not rewrite tests (test-review).
- Be concrete, not generic.
- Do not praise the code unless necessary for contrast.
- Prefer fewer, high-value findings over many weak ones.
- Group similar findings together.
- Where possible, suggest the smallest effective refactor first.
- Distinguish between:
  - confirmed issues
  - likely issues
  - potential issues that need verification
- Do not suggest large abstractions unless they clearly reduce complexity.
- Avoid recommending refactors that make the code more clever but less obvious.
- Favor explicit, readable code over abstraction for its own sake.
- Call out when duplication is acceptable and should remain.
- Call out when deleting code is safer than refactoring it.

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), symbol(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code
- Recommendation
- Expected benefit: readability / maintainability / correctness / LoC reduction
- Estimated effort

Output format:

## Executive Summary
- 5 to 15 most important issues
- Overall themes in the codebase
- Top 3 highest ROI cleanup opportunities

## Detailed Findings
Grouped by category, using the finding template above.

## Quick Wins
- Small, low-risk changes with high payoff

## Deletions
- Code that should likely be removed entirely

## Refactor Plan
- Ordered plan:
  1. Immediate cleanups
  2. Safe simplifications
  3. Medium-risk refactors
  4. Optional structural improvements

## Open Questions
- Things that need maintainer confirmation before changing

Important:
- Base findings on the actual code, not assumptions.
- If you are not sure, skip it.
- If the repository is large, prioritize the parts with the most duplication, complexity, inconsistency, or churn.
- Identify patterns, not just isolated issues.
- Optimize for actionable feedback that a team could turn into tickets immediately.
- Call out when code is already clean and should not be changed.
