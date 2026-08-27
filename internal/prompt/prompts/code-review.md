Summary: maintainability, correctness, consistency, simplicity

You are a senior software engineer. Your task is to perform a deep code quality review of this codebase.

Your goal is to produce a practical, high-signal review focused on maintainability, correctness, consistency, and simplicity.

Review the following:

1. Inconsistencies
(network API consistency belongs to api-review; published library surface to sdk-review; here own in-module naming and pattern consistency.)
- Naming inconsistencies
- Units or qualifiers first (`max_latency_ms`) or missing. Put units last, most-significant word first (`latency_ms_max`, `latency_ms_min`) so related names group and sort
- Related names of unequal length (`src`/`dest`) where equal length would line up in copies and slices (`source`/`target`)
- Abbreviated names (`buf`, `ctx`, `req`, `idx`) except a primitive integer in a sort or matrix. Spell the word
- Generic names that hide role (`allocator` where `gpa`/`arena` would tell the reader whether to free)
- Overloaded names that mean different things in different modules
- Helper or callback not prefixed with its caller (`read_sector_callback` vs a free-standing `on_done`)
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
- Branches whose bodies are identical (if/else arms, switch cases, ternary sides). Either a condition that was meant to differentiate and does not, or a branch that should not exist
- Verbose code that can be replaced with clearer, smaller constructs
- Unnecessary indirection

5. Refactoring opportunities
(arch-review owns module-scale separation of concerns. Here own function-scale structure inside one file.)
- Functions that are too long or do too many things
- Functions longer than ~70 lines (the scroll discontinuity). Split so the parent owns all branching and mutable state, and helpers are non-branchy and preferably pure
- Recursion where a bounded loop would make the bound obvious. Recursion hides whether execution is bounded
- Control flow scattered across helpers. Push `if`s up and `for`s down: one function owns switches and cases; leaves should not care about control flow
- Compound boolean conditions and `else if` chains that hide cases. Split into nested `if`/`else` trees so every branch is visible, and consider whether each `if` needs a matching `else` that handles or asserts the negative space
- Invariants stated as negations (`index >= length`) where the positive form (`index < length`) is the natural loop condition
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
- Library calls that rely on defaulted options (`fn(x)` or `fn(x, .{})`) instead of passing options explicitly at the call site. Defaults can change under you
- Two same-typed arguments (two integers, two strings, two paths) that can be swapped at the call site without a compile error. Name them (options struct, kwargs)
- Shell scripts embedding another language via heredocs or `-c` one-liners (`python -c`, `node -e`, inline awk/perl programs). The embedded code escapes syntax checking, linting, and editor tooling, and lives in escaping hell; one language per file, call a proper script instead
- Aliases or extra copies of a variable that can drift from the original
- Variables introduced far from their first use, or left in scope after their last use (place-of-check far from place-of-use)
- Variables declared wider than their use, or more names in scope than the function needs. Smallest possible scope, fewest variables
- Allocation and the matching `defer`/`finally`/`using` not grouped together, so leaks are hard to spot
- Function signatures that add viral dimensionality at the call site. Prefer simpler returns: `void` over `bool`, `bool` over an integer, an integer over optional, optional over a throwing/error union, unless the extra case is real

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
- Architecture-sized integers (`usize`, `size_t`, `int`, `long`, `number`). Use explicitly-sized types (`u32`, `uint32_t`, `i64`) for everything; width is part of the contract
- `index`, `count`, and `size` treated as interchangeable. They are distinct: index is 0-based, count is 1-based (index to count adds one), size is count times the unit. Put the qualifier in the name
- Division whose rounding mode is implicit (`/` or `//` when exact, floor, and ceil all mean different things). Name the intent
- Large values (more than ~16 bytes) passed by value when the callee must not copy them. Pass a const pointer or const reference
- Large structs returned then moved into place when they can be constructed in-place via an out-pointer. In-place init is viral: if one field is, the container should be too
- Fabricated or discarded type evidence: code that makes a value look safer to the compiler than it is (a frequent tell of machine-generated code). Each of these erodes the guarantee the type system exists to give:
  - Chained assertions that manufacture evidence (`x as unknown as T`, `as any as T`): they defeat the checker rather than prove the type. Parse or narrow to reach `T` instead
  - Widen-then-assert: broadening a value whose type is already known and then asserting it back (`(x as object) as T`, `JSON.parse` of something you constructed). The round trip discards real evidence
  - `unknown`, `any`, `object`, or `{}` in a signature (parameter, return, dictionary value, type alias) where a real contract exists and is being concealed. `unknown` is defensible only at a true boundary, immediately narrowed
  - Ad-hoc `typeof`/`instanceof` narrowing scattered through business logic instead of validating/parsing once at the boundary (error-review owns the boundary validation; here own the type-level smell)
  - Reflection (`Reflect.get`/`apply`, `getattr`, `[]`-string access) standing in for direct typed access on a shape that is actually known
  - Type assertions with no stated reason: a non-`const` cast that the reader cannot verify. Require a one-line justification next to it, or replace it with a check
  (Where the stack has a linter for this, run its type-safety ruleset (`oxlint`, `eslint`) and flag what it reports; test doubles via module mocking instead of a dependency seam belong to test-review.)

9. Risky areas
(functionality-review owns intended-vs-actual and documented edge cases. Here only a local logic error you can prove from the function body without consulting docs: inverted condition, swapped arguments, off-by-one.)
- Suspicious logic or possible bugs visible in the function body
- Implicit behavior that should be explicit
- Race conditions or shared mutable state (note only; concurrency-review owns the fix)
- Assumptions about ordering, uniqueness, or data shape that are not validated
- Code that works by coincidence rather than by design
- Loops or queues with no fixed upper bound and no assertion that they cannot terminate (or that they must). Everything has a limit; violations should fail fast
- Buffer not fully used, with padding or unused tail left uninitialized (buffer bleed). When the unread tail can leak secrets or prior contents, note only; sec-review owns the disclosure
- Function that asserts preconditions then suspends or yields, so those assertions may be false after resume (note only; concurrency-review owns the suspend)

10. Assertions and programmer errors
(error-review owns operating errors that must be handled. test-review owns test assertions. fuzz-review owns harness assertions. slop-review removes speculative guards. Here own production assertions that encode the author's mental model. Add an assertion only for a property the function already requires or a concrete path can violate.)
- Missing assertions on arguments, return values, preconditions, postconditions, or invariants. A function must not operate blindly on data it has not checked
- Assertion density well below two per non-trivial function (a lead only: each added assertion still needs the property or concrete path above)
- A property enforced on only one side of a boundary (assert before write but not after read; assert on send but not on receive). Pair assertions
- Compound `assert(a && b)` instead of `assert(a); assert(b);`. Split asserts are easier to read and fail more precisely
- Single-line implication not written as `if (a) assert(b)`
- Missing compile-time checks on constants, type sizes, or design invariants the compiler could reject before run
- Only the valid/happy space asserted; the invalid space never asserted. Bugs live on the valid/invalid boundary
- Programmer errors (broken invariants, impossible states) handled as recoverable operating errors, or the reverse: expected operating errors crashed via assert. Assertions crash; operating errors are handled
- A surprising, load-bearing condition explained only in a comment where a blatantly-true assertion would enforce it

Instructions:
- Fix order: unbounded loops/queues and missing invariant assertions > type-safety gaps and missing null checks with a concrete path > index/count/size mixups and implicit division rounding > duplication causing drift > inconsistent patterns. Cosmetic cleanups last.
- Do not add speculative assertions "for safety". An assertion needs a property the function already requires or a concrete path that can violate it.
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
- Optimize for feedback a team could turn into tickets immediately.
- Call out when code is already clean and should not be changed.
