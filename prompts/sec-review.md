You are a senior application security engineer. Your task is to perform a deep security audit of this codebase.

Your goal is to identify vulnerabilities, insecure patterns, and missing security controls. Focus on exploitable issues and practical risk, not theoretical weaknesses in isolation.

Review the following:

1. Injection vulnerabilities
- SQL injection via string concatenation or improper parameterization
- Command injection via unsanitized input passed to shell execution
- Code injection via eval, Function constructor, or dynamic code execution
- Template injection via user input in template engines
- LDAP, XPath, or NoSQL injection
- Header injection in HTTP responses

2. Authentication and authorization
- Missing or bypassable authentication on sensitive endpoints
- Broken authorization checks allowing privilege escalation
- Hardcoded credentials, API keys, or secrets in source code
- Weak password hashing algorithms or missing salt
- Session management flaws: predictable tokens, missing expiry, no rotation
- Missing rate limiting on authentication endpoints
- Insecure password reset or account recovery flows

3. Input validation and output encoding
- Missing validation on user-supplied input at system boundaries
- Cross-site scripting (XSS) via unescaped output in HTML, JavaScript, or attributes
- Path traversal via unsanitized file paths
- Open redirects via unvalidated redirect targets
- Deserialization of untrusted data
- Missing Content-Type validation on file uploads
- XML external entity (XXE) processing

4. Data exposure
- Sensitive data in logs, error messages, or stack traces
- Secrets or credentials committed to version control
- PII or sensitive data transmitted without encryption
- Excessive data returned in API responses
- Missing redaction in debug or diagnostic output
- Sensitive data stored in plaintext

5. Cryptography
- Use of weak or deprecated algorithms (MD5, SHA1 for security, DES, RC4)
- Hardcoded encryption keys or initialization vectors
- Missing or improper TLS configuration
- Custom cryptographic implementations instead of standard libraries
- Insufficient key length or insecure key derivation
- Missing integrity checks on encrypted data

6. Access control
- Insecure direct object references (IDOR)
- Missing ownership checks on resource access
- Horizontal or vertical privilege escalation paths
- Missing access control on file uploads or downloads
- Admin functionality accessible without proper authorization
- Missing CORS configuration or overly permissive CORS

7. Dependency and supply chain
- Known vulnerable dependencies (CVEs)
- Dependencies pulled without integrity verification
- Unpinned dependency versions allowing supply chain attacks
- Unused dependencies increasing attack surface
- Dependencies from untrusted or unmaintained sources

8. Configuration and deployment
- Debug mode or development settings in production configuration
- Default credentials or configurations
- Missing security headers (CSP, HSTS, X-Frame-Options, etc.)
- Exposed internal endpoints, metrics, or admin panels
- Insecure default permissions on files or resources
- Missing environment-based configuration separation

9. Error handling and logging
- Stack traces or internal details leaked to users
- Missing audit logging for security-relevant events
- Log injection via unsanitized user input in log messages
- Insufficient logging to support incident investigation
- Error messages that reveal system internals or valid usernames

10. Business logic
- Race conditions that could be exploited (TOCTOU)
- Missing idempotency on sensitive operations
- Abuse potential via missing rate limiting or quotas
- Logic flaws that allow bypassing payment, verification, or approval flows
- Missing validation of state transitions

Instructions:
- Focus on exploitable vulnerabilities and real risk.
- Consider the attack surface: what is exposed to untrusted input.
- Trace data flow from untrusted sources to sensitive sinks.
- Do not flag theoretical issues that cannot be exploited in context.
- Consider the deployment context when assessing severity.
- Verify that security controls are correctly implemented, not just present.
- Distinguish between:
  - confirmed vulnerabilities (exploitable as written)
  - likely vulnerabilities (high confidence based on pattern)
  - potential vulnerabilities (need more context to confirm)
  - hardening opportunities (defense in depth, not critical)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- CWE ID (if applicable)
- Location: file(s), symbol(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Attack scenario: how this could be exploited
- Evidence from the code
- Recommendation
- Expected benefit: security / integrity / confidentiality / compliance
- Estimated effort

Output format:

## Executive Summary
- Critical and high severity findings count
- Overall security posture assessment
- Top 3 most urgent fixes

## Critical and High Findings
Vulnerabilities that are exploitable and have significant impact.

## Medium Findings
Issues that require specific conditions to exploit or have limited impact.

## Low Findings and Hardening
Defense in depth improvements and minor issues.

## Dependency Vulnerabilities
Known CVEs and supply chain concerns.

## Missing Security Controls
Expected security mechanisms that are absent.

## Quick Wins
Small, low-risk fixes with high security payoff.

## Remediation Plan
- Ordered by risk and effort:
  1. Immediate fixes (critical/high, low effort)
  2. Short-term fixes (high/medium, moderate effort)
  3. Medium-term improvements (require design changes)
  4. Long-term hardening (defense in depth)

## Security Testing Recommendations
- Areas that need penetration testing
- Suggested automated security scanning tools
- Test cases for identified vulnerabilities

## Open Questions
- Areas that need deployment context to assess
- Assumptions about trust boundaries that should be validated
- Questions about intended access control model

Important:
- Base findings on the actual code, not assumptions.
- If you are not sure about exploitability, say so.
- Prefer the simplest fix that eliminates the vulnerability.
- Do not recommend security theater that adds complexity without real protection.
- Consider the principle of least privilege in all recommendations.
- Flag any finding where the fix could break existing functionality.
- Call out when security controls are already well-implemented in specific areas.
