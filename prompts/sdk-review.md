You are a senior software engineer specializing in SDK and client library design. Your task is to perform a deep SDK audit of this codebase.

Your goal is to evaluate the developer experience, API surface, consistency, correctness, and long-term maintainability of the SDK from the perspective of a developer integrating it into their application. Focus on issues that cause integration friction, surprising behavior, upgrade pain, or poor discoverability.

Review the following:

1. API surface design
- Inconsistent method naming across resources or operations
- Methods that do too many things or require too many parameters
- Missing convenience methods for common workflows
- Overly verbose call patterns for simple operations
- Inconsistent return types for similar operations
- Methods that expose internal implementation details to consumers
- Missing builder, options, or fluent patterns for complex configuration
- Unclear distinction between required and optional parameters
- Overloaded methods that create ambiguity
- Missing or inconsistent support for async and sync variants

2. Initialization and configuration
- Client construction that requires too many steps or boilerplate
- Missing sensible defaults for common configuration (timeouts, retries, base URL)
- Inconsistent configuration patterns across client classes or modules
- Missing support for environment variable or config file-based setup
- Auth configuration that is confusing, insecure, or inconsistent
- Missing validation of configuration at construction time (fail-fast)
- No way to override or customize HTTP client, transport, or middleware
- Global state or singletons that prevent multiple client instances
- Missing named or per-environment presets (production, sandbox, local)
- Hard to configure logging, tracing, or debug output

3. Error handling and types
- Inconsistent error types across methods or resources
- Missing error hierarchy distinguishing client errors, server errors, network errors, and validation errors
- Error messages that lack context (which operation, which resource, what was expected)
- Missing structured error data (error code, request ID, retry-after, field-level detail)
- Missing retry logic for transient errors or no retry configuration
- Exceptions or errors that leak HTTP-layer details (raw status codes, headers) without wrapping
- Inconsistent behavior for rate limiting, timeouts, and auth failures
- Missing timeout configuration or defaults that are too long or too short
- Errors that are impossible to handle programmatically (string matching required)
- Missing distinction between errors the caller can recover from and those they cannot

4. Type safety and models
- Missing or incomplete type definitions for request and response objects
- Overly permissive types (any, object, map[string]interface{}) where specific types exist
- Enum or constant values represented as raw strings instead of typed enums
- Missing nullability annotations or optional field markers
- Request/response models that do not match the API they wrap
- Missing generated or versioned types that stay in sync with the API schema
- Models that expose internal serialization details (JSON tags, wire format names)
- Inconsistent field naming between SDK models and API documentation
- Missing helper methods on models (equality, serialization, validation)
- Deep or confusing type hierarchies that make code completion unhelpful

5. Backward compatibility and versioning
- Breaking changes in minor or patch releases
- Missing deprecation annotations or warnings on outgoing features
- Deprecated methods without documented migration path or replacement
- Missing changelog or upgrade guide for major versions
- Public API surface that includes internal types or implementation details
- Missing semantic versioning discipline
- Breaking changes that could be avoided with additive design
- Version pinning requirements that conflict with other common dependencies
- Missing compatibility policy documentation (which API versions are supported)
- Generated code that changes unnecessarily between versions (noise diffs)

6. Documentation and examples
- Missing or incomplete README with quickstart and installation
- API reference that does not cover all public methods and types
- Missing code examples for common use cases
- Examples that do not compile, run, or reflect current API
- Missing error handling examples showing how to catch and handle SDK errors
- Missing migration guides between major versions
- Documentation that assumes knowledge of the underlying API rather than the SDK
- Inconsistent documentation quality across methods or modules
- Missing inline code documentation (docstrings, JSDoc, GoDoc, etc.)
- Missing authentication and configuration examples for all supported auth methods

7. Idiomatic design
- Patterns that violate conventions of the target language or ecosystem
- Naming that feels foreign to developers in the target language
- Missing use of language-native patterns (iterators, streams, async/await, contexts)
- Java-style getters/setters in Python, Python-style duck typing in TypeScript, etc.
- Error handling that does not match the language's conventions (exceptions vs result types)
- Package or module structure that does not follow ecosystem norms
- Missing support for popular frameworks or integrations in the ecosystem
- Import paths or package names that are unnecessarily long or confusing
- Missing use of dependency injection, interfaces, or protocols where expected
- Configuration patterns that feel unnatural for the language

8. Performance and resource management
- Missing connection pooling or reuse across requests
- No pagination support or awkward pagination for list endpoints
- Missing streaming or chunked transfer for large payloads
- Resources (connections, file handles, channels) not properly cleaned up
- Missing support for cancellation or context propagation
- No batching support for high-throughput use cases
- Large dependency tree or bundle size for the functionality provided
- Startup or initialization cost that is unnecessarily high
- Missing lazy initialization for expensive resources
- Memory leaks from caching, event listeners, or retained references

9. Testing and mocking
- SDK is hard to mock or stub in consumer tests
- Missing interfaces or protocols that would allow test doubles
- No test helpers, fixtures, or fake server provided for consumers
- Internal HTTP calls not abstracted, forcing consumers to mock at HTTP level
- Missing recorded or snapshot-based test support
- SDK depends on live services for its own tests (no offline test suite)
- Missing factory methods or builders for constructing test objects
- Side effects in constructors or imports that make testing difficult
- Missing documentation on recommended testing patterns for consumers
- No way to inject a custom transport or middleware for test interception

10. Distribution and packaging
- Package name that is confusing, generic, or conflicts with other packages
- Unnecessary dependencies that increase install size or conflict risk
- Missing or incorrect peer dependency declarations
- Package not published to the expected registry for the language
- Missing support for common build systems, bundlers, or runtimes
- License not clearly specified or incompatible with common use
- Missing TypeScript declarations, type stubs, or header files where expected
- Large package size due to bundled test fixtures, docs, or unnecessary assets
- Missing tree-shaking support for JavaScript/TypeScript SDKs
- Install or post-install scripts that cause friction or security concerns

Instructions:
- Evaluate the SDK from a consumer's perspective: someone integrating it into their application for the first time.
- Consider the full lifecycle: discovery, installation, first call, error handling, testing, upgrading.
- Test the API mentally by writing pseudocode for common use cases and checking if the SDK makes them easy.
- Compare patterns across all resources, methods, and modules for consistency.
- Do not recommend abstraction for its own sake. SDK APIs should be obvious, not clever.
- Consider both new users (discoverability, onboarding) and experienced users (power features, escape hatches).
- Focus on issues that cause real integration friction, not style preferences.
- If the SDK wraps a REST/GraphQL/gRPC API, verify the SDK correctly and completely covers the API surface.
- Distinguish between:
  - broken behavior (SDK does not match the API, wrong types, missing methods)
  - friction points (works but harder than it should be)
  - inconsistencies (different patterns for the same operation)
  - missing features (expected SDK capabilities not present)
  - design improvements (better patterns available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: method(s), class(es), module(s), file(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Consumer impact: how this affects developers using the SDK
- Evidence from the code
- Recommendation
- Expected benefit: usability / consistency / reliability / testability / upgrade safety
- Estimated effort

Output format:

## Executive Summary
- Overall SDK quality assessment
- Key developer experience strengths and weaknesses
- Top 3 highest impact improvements

## Breaking and Incorrect Behavior
SDK behavior that does not match the underlying API or produces wrong results.

## Developer Experience Friction
Patterns that are unnecessarily hard, verbose, or confusing for consumers.

## Inconsistencies
Different patterns, naming, or behavior for similar operations.

## Error Handling Issues
Missing error types, unhelpful messages, or inconsistent error behavior.

## Type Safety Issues
Missing types, overly broad types, or model mismatches.

## Testing and Mocking Issues
Barriers that make the SDK hard to test in consumer applications.

## Documentation Gaps
Missing or inaccurate documentation, examples, or migration guides.

## Compatibility and Versioning Issues
Breaking change risks, deprecation gaps, or upgrade friction.

## Quick Wins
Small changes that significantly improve the SDK developer experience.

## Improvement Plan
- Ordered by consumer impact:
  1. Fix incorrect behavior (SDK does not match API contract)
  2. Fix error handling (consumers cannot handle failures properly)
  3. Improve consistency across the API surface
  4. Add missing convenience methods and types
  5. Improve documentation and examples
  6. Enhance testability and mocking support

## API Surface Checklist
- Methods or resources in the underlying API that the SDK does not cover
- Methods that exist but behave differently from the API documentation
- Missing overloads or convenience methods for common call patterns

## Open Questions
- Design decisions that need team or product input
- Questions about intended consumer use cases and priorities
- Assumptions about target language or ecosystem conventions

Important:
- Base findings on the actual SDK code, types, documentation, and examples.
- If you are not sure whether a design choice is intentional, say so.
- Prefer additive, non-breaking changes over redesigns.
- Consider the cost to existing consumers of any recommended change.
- A simple SDK that covers 90% of use cases well is better than a complex one that covers 100%.
- Do not recommend wrapping every internal detail in an abstraction. Flat, obvious APIs win.
- Call out when the SDK is already well-designed and idiomatic in specific areas.
