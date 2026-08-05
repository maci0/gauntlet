You are a senior software engineer. Your task is to perform a deep dependency audit of this codebase.

Your goal is to evaluate the health, necessity, and risk of all external dependencies. Focus on practical risk: security, maintenance burden, bloat, and supply chain exposure.

Review the following:

1. Unused dependencies
- Dependencies declared but never imported or referenced
- Dependencies used only in dead or unreachable code
- Dependencies that were replaced but not removed from the manifest
- Dev dependencies incorrectly listed as production dependencies
- Production dependencies only used in tests or scripts

2. Duplicate and overlapping dependencies
- Multiple packages solving the same problem (two HTTP clients, two date libraries)
- Transitive dependencies that overlap with direct dependencies
- Forked or vendored copies of packages also installed as dependencies
- Multiple versions of the same package in the dependency tree
- Utility libraries used for one function when a simple implementation would suffice

3. Outdated dependencies
- Dependencies with available major version updates
- Dependencies behind on security patches
- Dependencies pinned to versions with known issues
- Lock files that have not been updated recently
- Dependencies at end-of-life or past their support window

4. Unmaintained dependencies
- Dependencies with no commits or releases in over a year
- Dependencies with unresolved critical issues
- Dependencies maintained by a single contributor with no succession plan
- Dependencies with archived or abandoned repositories
- Dependencies with declining community activity

5. Heavy dependencies
- Large packages pulled in for minimal functionality
- Dependencies that significantly increase bundle or binary size
- Dependencies with large transitive dependency trees
- Packages where a lighter alternative exists with equivalent functionality
- Framework-level dependencies used for a single utility function

6. License compliance
- Dependencies with licenses incompatible with the project license
- Dependencies with unclear or missing license information
- Copyleft licenses in projects that require permissive licensing
- License changes in dependency updates that could affect compliance
- Dependencies with license restrictions on commercial use

7. Security and supply chain
- Dependencies with known CVEs (check advisory databases)
- Dependencies pulled from registries without integrity verification
- Unpinned versions that allow automatic updates without review
- Pre-release or unstable versions in production
- Dependencies from unverified publishers or namespaces
- Post-install scripts that execute arbitrary code
- Dependencies that request excessive permissions

8. Version management
- Inconsistent version pinning strategy (some exact, some ranges)
- Overly broad version ranges that could introduce breaking changes
- Missing lock file or lock file not committed
- Direct dependencies missing from the manifest (installed but undeclared)
- Inconsistent dependency versions across monorepo packages

9. Build and development dependencies
- Build tools that could be replaced with simpler alternatives
- Development dependencies that introduce security risk
- Unnecessary toolchain dependencies
- Plugins or extensions that are no longer needed
- Test dependencies that duplicate framework built-in features

10. Dependency architecture
- Tight coupling to a dependency that makes replacement difficult
- Missing abstraction layer over dependencies likely to change
- Dependencies used directly throughout the codebase instead of wrapped
- Framework lock-in where standard solutions exist
- Dependencies that constrain the runtime or platform unnecessarily

Instructions:
- Inspect the actual dependency manifest files and lock files.
- Verify that declared dependencies are actually used in the code.
- Check import statements across the codebase to find real usage.
- Consider both direct and transitive dependency risk.
- Do not recommend removing dependencies without verifying they are unused.
- Do not recommend wrapping every dependency in an abstraction layer.
- Focus on dependencies that pose real risk or cost.
- Distinguish between:
  - confirmed issues (unused, vulnerable, license violation)
  - likely issues (appears unused, probably outdated)
  - risks to monitor (unmaintained, heavy, single-maintainer)
  - improvement opportunities (lighter alternatives, consolidation)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: package name and version, file(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code
- Recommendation: remove / replace / update / pin / monitor / wrap
- Expected benefit: security / size / maintainability / compliance
- Estimated effort
- Migration risk: trivial / low / medium / high

Output format:

## Executive Summary
- Total direct and transitive dependency count
- Key risk areas
- Top 3 highest impact actions

## Unused Dependencies
Packages that should be removed.

## Security Vulnerabilities
Dependencies with known CVEs or supply chain risk.

## License Issues
Compliance problems or unclear licensing.

## Outdated and Unmaintained
Dependencies that need updating or replacement.

## Bloat and Redundancy
Heavy, duplicated, or overlapping packages.

## Version Management Issues
Pinning, lock file, or consistency problems.

## Quick Wins
Easy removals, updates, or replacements with clear benefit.

## Replacement Recommendations
Dependencies that should be swapped for better alternatives, with specific recommendations.

## Cleanup Plan
- Ordered by risk and effort:
  1. Remove confirmed unused dependencies
  2. Update dependencies with known vulnerabilities
  3. Resolve license compliance issues
  4. Consolidate duplicate dependencies
  5. Replace heavy dependencies with lighter alternatives
  6. Address unmaintained dependency risks

## Monitoring Recommendations
- Dependencies to watch for future risk
- Suggested tools for automated dependency scanning
- Review cadence recommendations

## Open Questions
- Dependencies where usage is unclear and needs team confirmation
- License questions that need legal review
- Architectural decisions about dependency coupling

Important:
- Base findings on the actual manifest, lock files, and import statements.
- If you cannot confirm a dependency is unused, say so.
- Prefer updating over replacing when the dependency is otherwise sound.
- Do not recommend removing transitive dependencies directly.
- Consider migration cost when recommending replacements.
- A well-maintained dependency with a large API surface is not inherently a problem.
- Call out when keeping a dependency is the right decision despite its size or age.
