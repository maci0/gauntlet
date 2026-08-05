You are a senior software engineer and technical writer. Your task is to perform a deep documentation and comment audit of this codebase.

Your goal is to verify that documentation and comments are accurate, useful, and consistent with the actual code. Focus on clarity, correctness, usefulness, and removing unnecessary or misleading text.

Review the following:

1. Accuracy
- Comments that contradict the code
- Docstrings that describe behavior the code no longer has
- Documentation that references removed features, parameters, or behavior
- Incorrect examples
- Mismatches between function signatures and documentation
- Configuration or usage instructions that do not match the implementation

2. Outdated documentation
- Comments referencing old behavior, temporary hacks, or TODOs that are obsolete
- Documentation that refers to files, modules, or APIs that no longer exist
- Comments describing previous implementations rather than current behavior

3. Redundant or low-value comments
- Comments that restate obvious code
- Comments that simply narrate what each line does
- Excessively verbose comments that obscure the real logic
- Documentation duplicated across multiple places

4. Missing documentation
- Public APIs without docstrings
- Non-obvious algorithms without explanation
- Complex control flow without context
- Implicit assumptions not documented
- Invariants not documented
- Important side effects not documented

5. Misleading naming or documentation
- Names that conflict with documented behavior
- Functions whose purpose differs from their docstring
- Parameters that are poorly explained
- Documentation that hides important constraints or edge cases

6. Consistency problems
- Different documentation styles across modules
- Mixed terminology for the same concept
- Inconsistent parameter descriptions
- Inconsistent formatting for examples or code blocks

7. Clarity and readability
- Unclear or vague language
- Overly long paragraphs where structure would help
- Missing examples where examples would clarify behavior
- Documentation that assumes too much internal knowledge

8. Architecture and design documentation
- Missing high-level architecture documentation (system diagram, component overview)
- Design decisions not recorded (no ADRs, no decision log)
- Missing explanation of key abstractions, patterns, or conventions used
- Missing data flow or sequence documentation for complex interactions
- Architecture documentation that has drifted from the actual system
- Missing documentation of non-obvious constraints or trade-offs

9. Onboarding and getting started
- Missing or incomplete setup instructions for new contributors
- Prerequisites or system dependencies not documented
- Missing quickstart or hello-world example
- Development workflow not documented (how to run, test, build, deploy)
- Missing troubleshooting section for common setup problems
- Assumed knowledge not stated (expected familiarity with tools, frameworks, or domain)

10. Opportunities to improve documentation quality
- Replace comments with clearer code
- Consolidate duplicated documentation
- Convert large comment blocks into structured documentation
- Move architectural explanations to higher-level docs
- Add short summaries for complex modules

Instructions:
- Verify claims against the code. Do not assume documentation is correct.
- Prefer concise and precise documentation.
- Favor explaining "why" and constraints instead of restating "what the code does".
- Recommend deleting comments when the code is already clear.
- Do not recommend adding comments for trivial code.
- Distinguish between:
  - incorrect documentation
  - outdated documentation
  - missing documentation
  - low-value documentation
- Prefer minimal changes that significantly improve clarity.

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), symbol(s), or section
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code or documentation
- Recommendation
- Expected benefit: accuracy / clarity / maintainability / consistency
- Estimated effort

Output format:

## Executive Summary
- Major documentation problems
- Overall documentation quality assessment
- Top 3 highest impact improvements

## Incorrect or Misleading Documentation
Findings where documentation contradicts the code.

## Outdated Documentation
Findings where comments or docs describe old behavior.

## Low-Value or Redundant Comments
Comments that should likely be deleted.

## Missing Documentation
Places where documentation is necessary for understanding.

## Consistency Issues
Terminology, formatting, or style inconsistencies.

## Quick Wins
Small, low-risk changes with high documentation payoff.

## Deletions
Comments or documentation that should be removed.

## Improvement Plan
- Ordered by priority:
  1. Fix incorrect or misleading documentation
  2. Remove low-value comments
  3. Add missing critical documentation
  4. Improve consistency and formatting

## Open Questions
- Areas where documentation intent needs maintainer clarification
- Questions about intended audience and detail level

Important:
- Base conclusions on the actual code.
- If uncertain, mark the issue as needing confirmation.
- Prioritize issues that could mislead developers or cause misuse of APIs.
- Focus on actionable improvements rather than stylistic nitpicks.
- Call out when documentation is already clear and sufficient.
