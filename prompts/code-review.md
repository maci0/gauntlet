You are a senior software engineer. Your task is to perform a deep code quality review of this codebase.

Your goal is to produce a practical, high-signal review focused on maintainability, correctness, consistency, and simplicity.

Review the following:

1. Inconsistencies
- Naming inconsistencies
- API/design inconsistencies
- Style inconsistencies
- Different patterns used for the same problem
- Mismatches between similar modules/components

2. Duplication
- Duplicated code
- Near-duplicated logic
- Repeated patterns that should be abstracted
- Copy-paste code with minor variations
- Repeated validation, parsing, error handling, or data transformation logic

3. Dead or unnecessary code
- Unused functions, methods, classes, files, variables, constants, imports, flags, config, feature toggles, tests, assets
- Code paths that appear unreachable
- Over-engineered abstractions that provide little value
- Wrappers/helpers that do not meaningfully simplify anything

4. Opportunities to reduce lines of code
- Places where logic can be simplified
- Boilerplate that can be removed
- Repeated branches that can be merged
- Verbose code that can be replaced with clearer, smaller constructs
- Unnecessary indirection

5. Documentation and comments issues
- Comments that are outdated, misleading, redundant, or state the obvious
- Docstrings that do not match behavior
- README or developer docs that appear inaccurate based on the code
- Missing comments only where the code is genuinely hard to understand

6. Refactoring opportunities
- Functions that are too long or do too many things
- Poor separation of concerns
- Confusing control flow
- Weak naming
- Hidden assumptions
- Tight coupling
- Data structures that make the code harder to understand
- Interfaces that could be made clearer or smaller
- Opportunities to improve expressiveness and readability without changing behavior

7. Code clarity and expressiveness
- Places where intent is unclear
- Magic values
- Unclear ownership or lifecycle
- Hard-to-follow state changes
- Excessive nesting
- Unhelpful abstractions
- Patterns that obscure rather than clarify

8. Error handling patterns
- Inconsistent error handling strategies across the codebase
- Errors silently swallowed or caught and ignored
- Missing error propagation where callers need to know about failures
- Generic catch-all handlers that hide specific errors
- Error messages that lack context (what operation, what input, what was expected)
- Inconsistent use of error types, error codes, or result types
- Missing distinction between recoverable and unrecoverable errors
- Retry logic without backoff, limits, or jitter
- Error paths that leak resources (unclosed handles, dangling state)

9. Type safety and contracts
- Functions that accept overly broad types (any, object, interface{}) when specific types exist
- Missing null or undefined checks on values that can be absent
- Type assertions or casts that could fail at runtime
- Missing validation at module boundaries or public API entry points
- Implicit contracts between modules that are not enforced by types or assertions
- Optional values treated as always present
- Inconsistent use of type narrowing or discriminated unions

10. Risky areas
- Suspicious logic or possible bugs
- Edge cases not handled
- Implicit behavior that should be explicit
- Race conditions or shared mutable state
- Assumptions about ordering, uniqueness, or data shape that are not validated
- Code that works by coincidence rather than by design

Instructions:
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
- If you are not sure, say so.
- If the repository is large, prioritize the parts with the most duplication, complexity, inconsistency, or churn.
- Identify patterns, not just isolated issues.
- Optimize for actionable feedback that a team could turn into tickets immediately.
- Call out when code is already clean and should not be changed.
