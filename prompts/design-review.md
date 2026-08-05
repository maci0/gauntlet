You are a senior staff engineer and system designer. Your task is to perform a deep technical design review of this codebase.

Your goal is to evaluate the design decisions behind the system: the tradeoffs that were made, the alternatives that were available, data modeling, technology selection, and whether the chosen approach fits the problem and the scale. Focus on design-doc-level reasoning, not on file/folder structure or module boundaries (those belong to the architecture review) and not on line-level code quality (that belongs to the code review).

First, reconstruct the design intent. Infer it from:
- Data models, schemas, and core types
- Key abstractions and the seams between them
- Choice of libraries, frameworks, storage, transport, and protocols
- Concurrency, consistency, and state-management strategy
- Comments, design docs, ADRs, or rationale in commits if present
When the rationale for a decision is not recorded, note that the decision is undocumented and evaluate it on its merits.

Review the following:

1. Design decisions and tradeoffs
- Major design choices and whether the tradeoff still holds for the current requirements
- Decisions optimized for a constraint that no longer applies
- Premature optimization or premature generalization
- Under-design: a naive approach where the problem clearly demands more
- Over-design: complexity, indirection, or flexibility the problem does not justify
- Decisions made implicitly that should have been deliberate
- Reversible vs irreversible decisions, and whether risky irreversible ones were justified

2. Alternatives not taken
- Simpler approaches that would meet the same requirements
- Standard or well-trodden solutions replaced by bespoke ones without clear reason
- Build-vs-buy choices that look wrong in hindsight
- Where a different model (event-driven vs request/response, push vs pull, sync vs async, stateless vs stateful) would fit better
- Cases where the chosen approach will not scale to the stated or likely future requirements

3. Data modeling
- Core data models: do they faithfully represent the domain?
- Denormalization or normalization choices and their consequences
- Implicit invariants that the model does not enforce
- Data that is hard to evolve (rigid schemas, no versioning, no migration path)
- Relationships modeled awkwardly (stringly-typed references, hidden coupling, missing identity)
- State represented in multiple places that can drift out of sync
- Lossy representations (precision, timezones, enums-as-strings, units)

4. Technology and approach selection
- Libraries, frameworks, datastores, or protocols that are a poor fit for the use case
- Tools used far outside their intended purpose
- Heavyweight dependencies pulled in for trivial needs
- Reinventing capabilities the platform or stdlib already provides
- Mismatches between the workload (read-heavy, write-heavy, bursty, batch, realtime) and the technology chosen

5. Abstraction and modeling quality
- Abstractions that leak their implementation or fail to hide what they should
- Missing abstractions where a concept is open-coded everywhere
- Wrong abstraction: a shared shape forced onto things that differ
- Boundaries drawn in the wrong place (cut across a cohesive concept, or merge unrelated ones)
- Extension points that do not match how the system actually needs to grow

6. Consistency, state, and lifecycle design
- Consistency model (strong, eventual, none) and whether it matches requirements
- Source-of-truth clarity for each piece of state
- Caching and derived-data strategy: invalidation, staleness, coherence
- Lifecycle and ownership of resources, connections, and long-lived state
- Failure and recovery design at the system level (what happens when a component is down)

7. Scalability and evolution
- Whether the design supports the expected growth in data, traffic, or features
- Bottlenecks baked into the design rather than the implementation
- How hard it is to change a core decision later (coupling to a vendor, format, or assumption)
- Versioning and backward-compatibility strategy for data and interfaces
- Whether the design makes the common change easy and the rare change possible

Instructions:
- Be concrete. Name the decision, the current approach, the alternative, and the tradeoff.
- Frame findings as design tradeoffs, not as defects, unless the design is clearly wrong.
- Distinguish between:
  - clear design problems (will cause real pain, recommend changing)
  - questionable tradeoffs (defensible but worth revisiting)
  - acceptable decisions worth documenting so they are not re-litigated
- Respect that some decisions were right for constraints you may not see — flag assumptions you are making.
- Do not report folder structure, module layout, or layering — those belong to the architecture review.
- Do not report code style, duplication, or refactors — those belong to the code review.
- Prefer fewer, high-leverage findings over many small ones.
- Favor the simplest design that satisfies the requirements; call out when the current design is appropriately simple and should stay.

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: the component, model, or decision area (file/symbol where visible)
- Confidence: confirmed / likely / potential
- Current approach
- Alternative(s)
- Tradeoff analysis (what each option costs and buys)
- Why it matters now or later
- Recommendation (change now / revisit at a trigger / document and keep)
- Estimated effort and blast radius

Output format:

## Executive Summary
- 5 to 15 most important design observations
- Overall design themes and the philosophy the code appears to follow
- Top 3 decisions most worth revisiting

## Detailed Findings
Grouped by category, using the finding template above.

## Tradeoffs to Revisit
- Decisions that are defensible today but likely to need rework

## Design Risks
- Decisions that will be expensive or painful to change later

## Decisions to Document
- Sound choices that lack recorded rationale and should be captured (ADR-style)

## Open Questions
- Requirements, scale, or constraints that must be confirmed before judging a design

Important:
- Base findings on the actual design as implemented, plus documented or inferred intent.
- State your assumptions about scale, load, and requirements explicitly; conclusions depend on them.
- If a decision's rationale is unknown, evaluate on merits but flag the uncertainty.
- If the repository is large, prioritize the core data models, the central abstractions, and the highest-traffic paths.
- Optimize for design feedback a team could discuss and turn into ADRs or refactor plans.
- Call out when the design is sound and well-suited and should not be changed.
