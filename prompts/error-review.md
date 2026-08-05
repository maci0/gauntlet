You are a senior software engineer specializing in reliability and error handling. Your task is to perform a deep error handling and resilience audit of this codebase.

Your goal is to evaluate how the codebase handles errors, failures, edge cases, and degraded conditions. Focus on issues that cause silent data loss, misleading behavior, poor debuggability, or cascading failures.

Review the following:

1. Error propagation
- Errors caught and silently discarded without logging or re-throwing
- Error context lost during propagation (original cause stripped, generic re-wrap)
- Inconsistent error propagation strategies across modules (throw vs return vs callback vs result type)
- Errors swallowed inside catch-all handlers
- Functions that can fail but do not signal failure to callers
- Missing error propagation in async chains, promises, or goroutines
- Error handling that differs between similar code paths without justification

2. Error types and classification
- No distinction between recoverable and unrecoverable errors
- Missing error codes or error types for programmatic handling
- Generic error types used where specific types would aid handling (e.g., "Error" instead of "NotFoundError", "ValidationError")
- Inconsistent error class hierarchy or error code scheme
- Errors that conflate different failure modes into one type
- Missing sentinel errors or error constants for known conditions
- Status codes or error codes that do not match the actual failure

3. Error messages and context
- Error messages that lack context: what operation failed, what input caused it, what was expected
- Error messages that are too technical for the intended audience
- Error messages that leak internal implementation details, file paths, or stack internals
- Inconsistent error message format or tone across the codebase
- Error messages that are identical for different failure modes
- Missing structured data in errors (operation, entity, field, constraint)
- Errors without timestamps or correlation IDs in systems that need them

4. Input validation and boundary errors
- Missing validation at public API or module boundaries
- Validation errors that do not specify which field or constraint failed
- Inconsistent validation logic for the same data type across entry points
- Validation that happens too late (after side effects)
- Missing validation of environment variables, configuration, or external input
- Partial validation that catches some invalid inputs but misses others
- Type coercion or default substitution that masks invalid input

5. Resource cleanup on error
- Resources not released on error paths (file handles, connections, locks, temp files)
- Missing try/finally, defer, using, or equivalent cleanup patterns
- Partial operations that leave state inconsistent when an error occurs midway
- Transactions not rolled back on error
- Missing cleanup in async or concurrent error paths
- Goroutine, thread, or task leaks on error

6. Retry and recovery
- Missing retry logic for transient failures (network, database, external service)
- Retry logic without exponential backoff or jitter
- Unlimited retries that can loop forever
- Retries on non-idempotent operations that could cause duplicate effects
- Missing circuit breaker or fallback for persistent failures
- Recovery paths that silently use stale or default data without logging
- Missing dead letter queue or poison message handling for async processing

7. Failure isolation and blast radius
- Single component failure that crashes the entire process
- Missing graceful degradation when a non-critical dependency is unavailable
- Errors in background tasks that go unobserved
- Panics, unhandled exceptions, or process exits in library code
- Missing bulkheads or isolation between independent subsystems
- Error in one request or job affecting other concurrent requests or jobs

8. Timeout and cancellation
- Missing timeouts on external calls (HTTP, database, file system, DNS)
- Operations that can hang indefinitely without timeout
- Inconsistent timeout configuration across similar operations
- Missing cancellation propagation (context, abort controller, cancellation token)
- Timeouts that are too short (cause false failures) or too long (cause resource exhaustion)
- Missing deadline propagation across service boundaries
- Cleanup not performed when an operation is cancelled

9. Error observability
- Errors not logged or logged at wrong severity level
- Missing structured error logging (just logging error.message, not the full error)
- Missing error rate metrics or error count instrumentation
- Errors logged without the request or operation context needed to debug
- Missing alerting thresholds on error rates
- Error tracking that does not deduplicate or group related errors
- Missing distinction between expected errors (404, validation) and unexpected errors (500, panic)

10. Edge cases and defensive coding
- Division by zero, nil/null dereference, or index-out-of-bounds not guarded
- Missing handling for empty collections, zero-length input, or missing optional values
- Assumptions about data shape or ordering that are not validated
- Integer overflow or underflow in arithmetic operations
- Unicode, encoding, or locale edge cases not handled
- Time zone, daylight saving, or clock skew assumptions
- Missing handling for concurrent modification or stale data

Instructions:
- Trace error paths from origin to final handler. Check that context is preserved at each step.
- Look for catch blocks, error handlers, and recovery code. Verify they are correct, not just present.
- Consider what happens when every external call fails. Does the system handle it gracefully?
- Do not flag every missing try/catch. Focus on errors that would cause real damage if unhandled.
- Prefer error handling that is explicit and visible over clever or implicit patterns.
- Consider the operational burden of errors: can an on-call engineer understand and fix the issue from the error output alone?
- Distinguish between:
  - silent failures (errors discarded, data lost, behavior incorrect without any signal)
  - noisy failures (errors logged or surfaced but with wrong context or severity)
  - missing resilience (no retry, timeout, or fallback for expected failure modes)
  - inconsistencies (different error handling for similar operations)
  - hardening opportunities (defense in depth, not urgent)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), symbol(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Failure scenario: what goes wrong in production
- Evidence from the code
- Recommendation
- Expected benefit: reliability / debuggability / data integrity / user experience
- Estimated effort

Output format:

## Executive Summary
- Overall error handling quality assessment
- Critical silent failure risks
- Top 3 highest impact improvements

## Silent Failures
Errors discarded, swallowed, or handled in ways that hide real problems.

## Context and Message Quality
Errors that lack context, leak internals, or are unhelpful for debugging.

## Resource Leaks on Error
Missing cleanup, unclosed resources, or inconsistent state after errors.

## Missing Resilience
Missing retry, timeout, fallback, or circuit breaker patterns.

## Validation Gaps
Missing or inconsistent input validation at boundaries.

## Error Propagation Issues
Lost context, inconsistent strategies, or broken error chains.

## Edge Cases
Unguarded operations that fail on unexpected but valid input.

## Quick Wins
Small changes that significantly improve error handling quality.

## Improvement Plan
- Ordered by impact:
  1. Fix silent failures (errors that cause data loss or incorrect behavior)
  2. Add missing timeouts and resource cleanup
  3. Improve error messages and context
  4. Add retry and fallback for transient failures
  5. Standardize error handling patterns across the codebase

## Open Questions
- Error handling decisions that need team input
- Assumptions about failure modes that should be validated
- Areas where current error handling intent is unclear

Important:
- Base findings on actual error handling code, catch blocks, and error paths.
- If you are not sure whether an error is intentionally discarded, say so.
- Prefer the simplest fix that prevents the failure. Do not over-engineer error handling.
- Consider that some errors are expected and should be handled differently from unexpected ones.
- A visible error is always better than a silent failure.
- Do not recommend try/catch on every line. Focus on boundaries, I/O, and critical operations.
- Call out when error handling is already thorough and well-designed in specific areas.
