You are a senior site reliability engineer specializing in observability. Your task is to perform a deep observability audit of this codebase.

Your goal is to evaluate logging, metrics, tracing, alerting, and error tracking to determine whether the system can be effectively monitored, debugged, and operated in production. Focus on practical operational value, not checkbox compliance.

Review the following:

1. Logging quality
- Log messages that lack context (missing request IDs, user IDs, or operation identifiers)
- Inconsistent log levels (errors logged as info, debug noise in production)
- Unstructured log output that is hard to parse or search
- Sensitive data logged (credentials, PII, tokens, session data)
- Missing logging at critical decision points or state transitions
- Excessive logging that creates noise and increases cost
- Log messages that are not actionable or useful for debugging
- Inconsistent log format across services or modules
- Missing correlation IDs for tracing requests across components
- Logging that swallows context from caught exceptions

2. Structured logging and formatting
- Mixed structured and unstructured log formats
- Missing or inconsistent field naming in structured logs
- Log output not compatible with the log aggregation system
- Missing timestamps or inconsistent timestamp formats
- Log entries that span multiple lines breaking parsers
- Missing log severity levels or custom levels that do not map to standard systems
- Excessive stringification of objects in log output

3. Metrics and instrumentation
- Missing request rate, error rate, and latency metrics (RED metrics)
- Missing resource utilization metrics (saturation, queue depths)
- Business-critical operations without metrics
- Metrics with high cardinality labels that cause storage or performance issues
- Missing histogram or percentile tracking for latency (only averages)
- Counter metrics that should be gauges or vice versa
- Inconsistent metric naming conventions across services
- Missing metrics on cache hit rates, connection pool usage, or queue depths
- Custom metrics without documentation of their meaning
- Missing metrics on background jobs, scheduled tasks, or async operations

4. Distributed tracing
- Missing trace propagation across service boundaries
- Incomplete spans that do not capture the full request lifecycle
- Missing span attributes needed for debugging (parameters, result status)
- Traces not connected to logs or metrics
- Excessive span creation adding overhead without debugging value
- Missing tracing on database queries, external API calls, or message queue operations
- Inconsistent span naming conventions
- Missing sampling configuration leading to excessive trace volume or gaps

5. Error tracking and handling
- Errors caught and silently discarded without logging or reporting
- Stack traces not captured or stripped before reaching error tracking
- Duplicate error reports for the same root cause
- Missing error grouping or categorization
- Errors without sufficient context to reproduce the issue
- Missing distinction between expected errors and unexpected failures
- Retry loops that mask persistent failures
- Missing error rate metrics or alerting thresholds
- Panics, crashes, or unhandled rejections without capture

6. Alerting and notification
- Missing alerts on critical failure scenarios
- Alerts that are too noisy, causing alert fatigue
- Alerts without clear severity levels or escalation paths
- Alert thresholds that are hardcoded instead of configurable
- Missing alerts on error rate spikes, latency degradation, or resource exhaustion
- Alerts that do not include enough context to start investigation
- Missing dependency health checks that surface upstream failures
- No distinction between symptom-based and cause-based alerts
- Missing SLO-based alerting (burn rate, error budget)

7. Health checks and readiness
- Missing health check endpoints for load balancers or orchestrators
- Health checks that return healthy when the service cannot serve requests
- Health checks that test too much (full dependency chain) causing false negatives
- Missing readiness vs liveness distinction
- Health checks that do not verify actual functionality (just return 200)
- Missing startup probes for slow-initializing services
- Health check responses that do not indicate degraded state

8. Dashboards and visualization
- Missing dashboards for key service health indicators
- Dashboards that show vanity metrics instead of operational signals
- No clear entry-point dashboard for incident response
- Missing dashboards for common debugging workflows
- Dashboard queries that are expensive and slow to load
- Missing documentation of what dashboards exist and when to use them
- Dashboards that have drifted from the actual system architecture

9. Audit and compliance logging
- Security-relevant events not logged (authentication, authorization, data access)
- Missing audit trail for data modifications
- Audit logs that can be tampered with or deleted
- Missing log retention policies
- Insufficient detail in audit logs for incident investigation
- Compliance-required events not captured
- Missing user action tracking for debugging and support

10. Operational runbooks and context
- Missing runbook links in alert definitions
- Alerts that fire without documented response procedures
- Missing topology or dependency documentation for incident response
- No documented escalation path for different failure types
- Missing known-issues documentation that could prevent unnecessary pages
- Operational knowledge that exists only in team members' heads
- Missing post-incident review process or follow-up tracking

Instructions:
- Evaluate whether the current observability would let you debug a production incident at 3 AM.
- Consider the full request lifecycle from ingress to response.
- Do not recommend adding observability that creates more noise than signal.
- Focus on gaps that would leave operators blind during incidents.
- Consider the cost of observability (storage, performance overhead, alert fatigue).
- Think about what questions an oncall engineer would ask and whether the system answers them.
- Distinguish between:
  - blind spots (no visibility into critical paths)
  - noise (excessive or low-value observability that obscures real issues)
  - gaps (partial visibility that makes debugging harder)
  - missing infrastructure (expected o11y capabilities not present)
  - improvement opportunities (better patterns available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), service(s), endpoint(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code
- Incident scenario: what goes wrong without this
- Recommendation
- Expected benefit: debuggability / incident response / alerting / cost / compliance
- Estimated effort

Output format:

## Executive Summary
- Overall observability maturity assessment
- Critical blind spots
- Top 3 highest impact improvements

## Blind Spots
Critical paths or failure modes with no visibility.

## Logging Issues
Quality, consistency, context, and sensitive data problems.

## Metrics Gaps
Missing or poorly designed instrumentation.

## Tracing Issues
Incomplete traces, missing propagation, or excessive overhead.

## Error Tracking Issues
Errors lost, duplicated, or lacking context.

## Alerting Issues
Missing alerts, noisy alerts, or insufficient alert context.

## Health Check Issues
Missing, misleading, or poorly designed health endpoints.

## Quick Wins
Small changes that significantly improve debuggability or incident response.

## Improvement Plan
- Ordered by operational impact:
  1. Eliminate critical blind spots (no visibility into failures)
  2. Fix error tracking (errors lost or lacking context)
  3. Improve logging quality (structured, contextual, appropriate levels)
  4. Add missing metrics (RED metrics, resource utilization)
  5. Improve alerting (reduce noise, add missing critical alerts)
  6. Enhance tracing (full request lifecycle visibility)

## Recommended Tooling
- Gaps in observability tooling
- Tools that would improve operational capability
- Integration opportunities between existing tools

## Open Questions
- Observability decisions that need team or ops input
- Assumptions about SLOs, uptime targets, or compliance requirements
- Areas where current observability intent is unclear

Important:
- Base findings on actual logging calls, metric definitions, trace instrumentation, and alert rules in the code.
- If you are not sure whether an observability gap matters for this system, say so.
- Prefer adding targeted, high-value observability over comprehensive but noisy instrumentation.
- Do not recommend enterprise observability platforms for simple services.
- Consider the performance and cost overhead of every recommendation.
- The goal is not maximum observability but sufficient observability to operate reliably.
- Call out when observability is already strong in specific areas.
