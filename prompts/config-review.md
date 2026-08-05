You are a senior software engineer specializing in application configuration and environment management. Your task is to perform a deep configuration audit of this codebase.

Your goal is to evaluate how the application is configured, how configuration flows through the system, and whether the configuration approach is correct, consistent, and safe. Focus on issues that cause misconfiguration incidents, environment-specific bugs, secret leaks, or operational friction.

Review the following:

1. Configuration sources and loading
- Missing centralized configuration loading (config scattered across files and modules)
- Configuration read from multiple sources without clear precedence (env vars vs config files vs flags vs defaults)
- Missing validation of configuration values at startup
- Configuration loaded lazily in unexpected places instead of at initialization
- Missing documentation of all configuration options and their valid values
- Configuration files in formats that do not support comments or are error-prone (e.g., JSON for human-edited config)
- Missing example or template configuration files
- Configuration reloaded at runtime without clear consistency guarantees

2. Environment separation
- Production configuration values hardcoded or committed to version control
- Missing distinction between environments (dev, staging, production) in configuration
- Environment-specific behavior implemented as conditionals in application code instead of configuration
- Configuration that assumes a specific environment without checking
- Missing environment variable prefixing or namespacing
- Test configuration that leaks into production or vice versa
- Development defaults that are unsafe for production (debug mode, verbose logging, permissive CORS)

3. Secrets management
- Secrets (API keys, database passwords, tokens) hardcoded in source code
- Secrets stored in plain text configuration files committed to version control
- Secrets in environment variables without documentation of which are required
- Missing secrets in example configuration or .env.example files
- Secrets logged or included in error messages
- Secrets passed as command-line arguments (visible in process listings)
- Missing integration with a secret manager (Vault, AWS Secrets Manager, etc.)
- Shared secrets across environments
- Missing secret rotation support or documentation

4. Default values and fallbacks
- Missing defaults for optional configuration, requiring explicit setup for every deployment
- Unsafe defaults (permissive security settings, debug enabled, open CORS)
- Default values that differ from documented values
- Fallback chains that silently use incorrect values
- Magic numbers or strings used as defaults without documentation
- Defaults that make sense for development but are dangerous in production
- Missing distinction between "not set" and "set to empty"

5. Validation and type safety
- Configuration values parsed as strings but used as numbers, booleans, or URLs without validation
- Missing validation of required configuration at startup (fail-fast)
- Range, format, or constraint violations not caught until runtime failure
- Configuration keys silently ignored when misspelled
- Missing type checking or schema validation for configuration files
- Configuration that accepts any value but only works with specific ones
- Missing validation of dependent configuration (A requires B, mutual exclusion)

6. Feature flags and toggles
- Feature flags without documentation of what they control
- Feature flags without cleanup plan (permanent toggles accumulating)
- Missing default values for feature flags
- Feature flags checked inconsistently across the codebase
- Missing feature flag evaluation logging for debugging
- Feature flags that affect data schema or migrations without rollback plan
- Stale feature flags for features that have shipped or been removed
- Missing per-environment feature flag configuration

7. Configuration drift and consistency
- Configuration defined in multiple places with overlapping responsibility
- Duplicate configuration keys with different values in different files
- Configuration in code that should be externalized
- Externalized configuration that should be code (rarely changes, tightly coupled to logic)
- Missing configuration documentation that drifts from actual options
- Default values defined in multiple locations (code, config files, deployment scripts)
- Inconsistent naming conventions for configuration keys (camelCase vs SCREAMING_SNAKE_CASE vs kebab-case)

8. Twelve-factor and deployment readiness
- Configuration that prevents running the same build in different environments
- Missing support for environment variable-based configuration
- State stored locally that prevents horizontal scaling
- Port, host, or URL configuration that assumes a specific deployment topology
- Missing health check or readiness configuration
- Missing configuration for graceful shutdown timeouts
- Log level and output configuration not externalized
- Database connection configuration not supporting connection pools or replicas

9. Build-time vs runtime configuration
- Values baked into builds that should be runtime-configurable
- Runtime configuration that could be validated at build time
- Build-time constants that vary per environment but require rebuild to change
- Missing documentation of which values are build-time vs runtime
- Missing compile-time or startup-time validation of build-time configuration
- Feature flags or API URLs hardcoded during build without override capability

10. Configuration observability and debugging
- No way to inspect the active configuration at runtime (debug endpoint, startup log)
- Missing startup logging of configuration values (with secrets redacted)
- Configuration errors that produce confusing downstream failures instead of clear messages
- Missing documentation of how to verify configuration is correct
- No way to diff configuration between environments
- Missing configuration change audit trail

Instructions:
- Inspect actual configuration files, environment setup, and how configuration is consumed in code.
- Trace configuration values from source to usage to verify correctness.
- Consider what happens when the application runs with default configuration in production.
- Do not recommend over-engineering for simple projects with few configuration options.
- Focus on issues that cause real incidents: wrong values in production, leaked secrets, silent misconfiguration.
- Consider the operational and onboarding burden of the configuration approach.
- Distinguish between:
  - dangerous defaults (configuration that causes incidents or security issues)
  - missing validation (bad values accepted silently)
  - inconsistencies (different patterns for the same thing)
  - operational friction (hard to configure, deploy, or debug)
  - improvement opportunities (better patterns available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), variable(s), key(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code or configuration
- Recommendation
- Expected benefit: safety / reliability / operability / onboarding / consistency
- Estimated effort

Output format:

## Executive Summary
- Overall configuration health assessment
- Key safety and consistency concerns
- Top 3 highest impact improvements

## Dangerous Defaults and Secrets
Configuration that could cause security incidents or data loss.

## Validation Gaps
Missing or insufficient validation of configuration values.

## Environment Separation Issues
Configuration that does not properly separate environments.

## Consistency Issues
Different patterns, naming, or approaches for configuration across the codebase.

## Feature Flag Issues
Stale, undocumented, or inconsistently applied feature flags.

## Configuration Drift
Documentation, defaults, or definitions that have fallen out of sync.

## Quick Wins
Small changes with high configuration safety or clarity payoff.

## Improvement Plan
- Ordered by risk:
  1. Fix secrets exposure and dangerous defaults
  2. Add startup validation for required configuration
  3. Standardize configuration patterns across the codebase
  4. Clean up stale feature flags and dead configuration
  5. Improve configuration documentation and observability

## Open Questions
- Configuration decisions that need team input
- Assumptions about deployment environment that affect configuration design
- Areas where configuration intent is unclear

Important:
- Base findings on actual configuration files, environment setup, and code that reads configuration.
- If you are not sure whether a configuration choice is intentional, say so.
- Prefer fail-fast validation at startup over runtime errors from bad configuration.
- Do not recommend a configuration framework for a project with three environment variables.
- Consider the cost of migration when recommending configuration changes.
- A simple, well-documented configuration approach is better than a sophisticated one that nobody understands.
- Call out when configuration is already clean and well-managed in specific areas.
