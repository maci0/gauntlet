You are a senior software architect. Your task is to perform a deep architecture review of this codebase.

Your goal is to evaluate the structural health of the project: folder and file organization, module boundaries, dependency direction, API surface design, layering, and separation of concerns. Focus on issues that make the codebase hard to navigate, extend, or maintain at its current scale.

Review the following:

1. Project structure and file organization
- Inconsistent directory naming or nesting conventions
- Files placed in directories that do not match their purpose
- Flat structures that should be grouped or deep nesting that should be flattened
- Mixed concerns in the same directory (handlers next to models next to utils)
- Inconsistent file naming conventions across the project
- Missing or misleading directory names
- Entrypoints that are hard to find or ambiguously named
- Configuration files scattered instead of centralized
- No clear convention for where new code should go

2. Module and package boundaries
- Modules that do too many things (god modules)
- Modules that are split too finely to be useful
- Circular dependencies between modules or packages
- Modules that reach into each other's internals instead of using public interfaces
- Business logic mixed into infrastructure or framework code
- Unclear ownership of shared functionality
- Features scattered across multiple unrelated modules
- Missing module-level documentation or index files that explain purpose

3. Layering and separation of concerns
- Business logic in controllers, handlers, or transport layer
- Data access logic mixed into business rules
- Presentation concerns in the domain layer
- Missing or inconsistent layering (no clear boundary between domain, application, infrastructure)
- Cross-cutting concerns (logging, auth, validation) implemented inconsistently across layers
- Framework types leaking across layer boundaries
- Domain models coupled to persistence or serialization formats

4. Dependency direction and coupling
- Lower-level modules importing higher-level modules
- Shared utilities depending on application-specific code
- Bidirectional dependencies between packages
- Concrete implementations imported where interfaces should be used
- Hard dependencies on external services without abstraction at boundaries
- Tight coupling between features that should be independent
- Import graphs that suggest incorrect dependency direction

5. API boundaries and public surfaces
- Internal implementation details exported or exposed publicly
- Missing barrel files, index modules, or explicit public API definitions
- Inconsistent export patterns across modules
- Public APIs that are too wide (exposing too much)
- Public APIs that are too narrow (forcing consumers to reach into internals)
- Missing type definitions or interfaces at module boundaries
- Unclear contract between modules about what is stable vs internal

6. Code placement and colocation
- Related code spread across distant parts of the tree
- Tests far from the code they test with no clear convention
- Types defined far from where they are primarily used
- Constants, enums, or config values duplicated instead of shared
- Shared utilities that belong closer to their primary consumer
- Feature code that should be colocated but is split by technical layer

7. Naming and conventions
- Inconsistent naming patterns for similar constructs across the codebase
- Generic names that obscure purpose (utils, helpers, misc, common, shared)
- Naming that does not reflect the domain or the bounded context
- Inconsistent patterns for similar files (some use index files, some do not)
- Ambiguous names that could refer to multiple things
- Technical naming where domain naming would be clearer

8. Scalability of the architecture
- Patterns that work now but will not scale with team size or feature count
- Missing abstractions that will cause widespread changes for common additions
- Monolithic structures that resist parallel development
- Missing extension points for anticipated growth areas
- Architectural decisions that force sequential development
- Areas where adding a new feature requires touching many unrelated files

9. Entry points and bootstrapping
- Application wiring or bootstrapping that is tangled or hard to follow
- Dependency injection or service setup that is implicit or scattered
- Missing clear entry point for understanding the application flow
- Configuration loading that is spread across multiple unrelated locations
- Initialization order dependencies that are not documented or enforced
- Multiple competing patterns for how services are created or connected

10. Architectural consistency
- Different architectural patterns used in different parts of the codebase without justification
- Partial migrations between patterns (half MVC, half clean architecture)
- Inconsistent error propagation strategies across modules
- Mixed patterns for the same cross-cutting concern
- Abandoned architectural conventions still partially present
- New code following a different structure than existing code without clear migration

Instructions:
- Map the actual structure before judging it. Understand what exists before recommending changes.
- Consider the size and maturity of the project when assessing architecture.
- Do not recommend enterprise patterns for small projects or startup patterns for large ones.
- Focus on issues that cause real friction: hard to navigate, hard to extend, hard to onboard.
- Consider how the architecture supports or hinders team workflows.
- Do not recommend restructuring for its own sake. Every change should have a clear payoff.
- Distinguish between:
  - structural defects (things that are wrong and cause problems now)
  - inconsistencies (different patterns for the same thing)
  - scaling risks (things that will become problems as the project grows)
  - improvement opportunities (better patterns available without urgency)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: directory(ies), module(s), file(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code
- Recommendation
- Expected benefit: navigability / extensibility / maintainability / onboarding / team velocity
- Estimated effort
- Blast radius: how many files or modules would the fix touch

Output format:

## Executive Summary
- Overall architectural health assessment
- Top structural themes in the codebase
- Top 3 highest ROI structural improvements

## Structural Defects
Problems that actively cause confusion, coupling, or incorrect dependency direction.

## Boundary Violations
Places where modules, layers, or features leak into each other.

## Inconsistencies
Different organizational patterns used for similar things.

## Naming and Discoverability Issues
Problems that make the codebase hard to navigate or understand.

## Colocation Problems
Related code that is unnecessarily separated or unrelated code that is grouped together.

## Scaling Risks
Patterns that will resist growth in features, team size, or complexity.

## Quick Wins
Small structural changes with high navigability or maintainability payoff.

## Restructuring Plan
- Ordered by impact and risk:
  1. Fix dependency direction violations (low risk, high clarity gain)
  2. Consolidate inconsistent patterns (moderate risk, consistency gain)
  3. Reorganize misplaced code (moderate risk, navigability gain)
  4. Introduce missing boundaries (higher risk, extensibility gain)
  5. Larger structural migrations (if warranted by scale)

## Dependency Map
- High-level description of the module dependency graph
- Problematic dependency cycles or unexpected edges
- Suggested target dependency direction

## Open Questions
- Structural decisions that need team input
- Assumptions about growth direction that affect architecture choices
- Areas where the current structure might be intentional but looks accidental

Important:
- Base findings on the actual directory tree, file contents, and import graph.
- If you are not sure whether a structural choice is intentional, say so.
- Prefer incremental restructuring over big-bang rewrites.
- Consider the cost of churn when recommending moves or renames.
- A flat structure is not inherently bad. Deep nesting is not inherently good.
- Do not recommend patterns from architecture books unless they solve a concrete problem here.
- Call out when the current structure is working well and should not be changed.
