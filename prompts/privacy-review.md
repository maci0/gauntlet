You are a senior software engineer specializing in data privacy and compliance. Your task is to perform a deep data privacy audit of this codebase.

Your goal is to evaluate how the application collects, stores, processes, shares, and deletes personal data. Focus on compliance risks, data leakage, missing controls, and gaps that could cause privacy incidents or regulatory violations.

Review the following:

1. Personal data inventory
- User data collected without clear documentation of what is collected and why
- PII (personally identifiable information) fields without classification or sensitivity labels
- Unclear distinction between required and optional data collection
- Data collected that is not necessary for the stated purpose (over-collection)
- Personal data stored in unexpected locations (logs, caches, analytics, error reports)
- User-generated content that may contain PII not treated as personal data
- Derived or inferred data (behavior profiles, risk scores) not tracked as personal data
- Third-party data received without documented data processing agreements

2. Consent and legal basis
- Data collected without explicit user consent where required
- Missing or incomplete consent management (no record of what was consented to, when)
- Consent bundled with terms of service instead of granular per-purpose consent
- Pre-checked consent boxes or dark patterns that undermine informed consent
- Missing consent withdrawal mechanism or withdrawal that does not actually stop processing
- Data processing that continues after consent is withdrawn
- Missing legal basis documentation for each processing activity
- Legitimate interest claimed without documented balancing test

3. Data storage and retention
- Personal data stored without defined retention periods
- Missing automated deletion or anonymization after retention period expires
- Backups that retain personal data beyond the primary retention period
- Soft-deleted records that retain PII indefinitely
- Personal data replicated across multiple stores without consistent retention
- Missing data retention documentation or policy
- Archived data accessible without the same access controls as active data
- Retention periods that exceed what is necessary for the stated purpose

4. Data minimization and purpose limitation
- Collecting more data fields than needed for the feature or service
- Personal data used for purposes beyond what was originally communicated to users
- Data collected "just in case" or for future undefined use
- Profile or tracking data collected without a specific product need
- Full data objects passed through the system when only a subset is needed
- Analytics or telemetry that includes unnecessary personal data
- Test or staging environments using production personal data
- Debug logging that captures full request/response bodies containing PII

5. Data subject rights
- Missing ability for users to access all their personal data (right of access)
- No export functionality for user data in a portable format (right to portability)
- Missing or incomplete data deletion (right to erasure / right to be forgotten)
- Deletion that misses personal data in backups, logs, caches, or third-party systems
- No mechanism for users to correct inaccurate personal data (right to rectification)
- Missing ability to restrict or object to specific processing activities
- Data subject requests handled manually without documented process or SLA
- Missing identity verification for data subject requests

6. Data sharing and third parties
- Personal data sent to third-party services without data processing agreements
- Analytics, tracking, or advertising SDKs that receive personal data
- Personal data included in API responses to external consumers
- Missing documentation of all third parties that receive personal data
- Data transferred across jurisdictions without adequate safeguards (EU to non-EU)
- Third-party scripts or pixels that collect data directly from users
- Webhooks or event streams that include personal data sent to external endpoints
- Sub-processors not documented or approved in data processing agreements

7. Data security for privacy
- Personal data stored without encryption at rest
- PII transmitted without encryption in transit (internal services included)
- Missing access controls on personal data (any authenticated user can access any user's data)
- Personal data accessible via overly broad database queries or API endpoints
- Missing audit logging for access to sensitive personal data
- Personal data in logs accessible to engineers who do not need it for their role
- Missing pseudonymization or tokenization where it would reduce exposure
- Backups of personal data not encrypted or not access-controlled

8. Privacy by design
- Privacy considerations absent from the development process
- Missing privacy impact assessment for new features that process personal data
- Personal data flows not documented or mapped
- Missing privacy-aware defaults (opt-in vs opt-out, minimal collection by default)
- User-facing privacy controls that are hard to find or understand
- Missing privacy documentation, notice, or transparency about data practices
- Cookie or tracking consent implemented incorrectly or incompletely
- Missing data protection officer or privacy contact information where required

9. Anonymization and pseudonymization
- Anonymized datasets that can be re-identified (insufficient anonymization)
- Missing k-anonymity, l-diversity, or differential privacy where needed for published data
- Pseudonymized data stored alongside the key that links it to the original identity
- Analytics on personal data where aggregated or anonymized data would suffice
- Hashed identifiers used as "anonymous" without considering rainbow table attacks
- Missing anonymization for data used in development, testing, or training
- User IDs or session IDs in URLs, logs, or analytics that can be linked back to individuals
- Missing documentation of anonymization methods and their limitations

10. Incident and breach preparedness
- No documented procedure for personal data breach notification
- Missing data breach detection capabilities (unauthorized access, exfiltration)
- Breach notification timeline that does not meet regulatory requirements (72 hours for GDPR)
- Missing inventory of where personal data is stored, needed for breach impact assessment
- No process for notifying affected users of a data breach
- Missing logging and forensics capability to determine breach scope
- Data breach response plan not tested or rehearsed
- Missing data protection authority contact or notification template

Instructions:
- Trace personal data from collection to storage, processing, sharing, and deletion.
- Consider all categories of data subjects: customers, employees, prospects, partners, anonymous visitors.
- Do not assume the application only operates in one jurisdiction. Consider GDPR, CCPA/CPRA, and other applicable regulations.
- Focus on practical privacy risk: data that could cause harm if mishandled, leaked, or misused.
- Do not recommend privacy theater that adds friction without real protection.
- Consider the user's perspective: can they understand and control what happens with their data?
- Distinguish between:
  - compliance violations (legally required controls that are missing or broken)
  - data leakage (personal data appearing where it should not)
  - missing controls (expected privacy mechanisms not implemented)
  - excessive collection (more data collected or retained than necessary)
  - design improvements (better privacy patterns available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: file(s), service(s), data flow(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Regulatory relevance: GDPR, CCPA/CPRA, or general privacy best practice
- Evidence from the code or configuration
- Recommendation
- Expected benefit: compliance / user trust / risk reduction / data minimization
- Estimated effort

Output format:

## Executive Summary
- Overall privacy posture assessment
- Key compliance risks
- Top 3 highest impact improvements

## Compliance Violations
Legally required controls that are missing or implemented incorrectly.

## Data Leakage
Personal data appearing in logs, caches, error reports, or third-party services where it should not.

## Data Subject Rights Gaps
Missing or incomplete access, deletion, portability, or correction capabilities.

## Excessive Collection and Retention
Data collected beyond necessity or retained beyond its purpose.

## Third-Party and Data Sharing Risks
Personal data shared with external services without proper controls.

## Data Security for Privacy
Encryption, access control, and audit logging gaps specific to personal data.

## Anonymization and Pseudonymization Issues
Insufficient de-identification or re-identification risks.

## Quick Wins
Small changes that significantly reduce privacy risk.

## Improvement Plan
- Ordered by compliance risk:
  1. Fix compliance violations (legally required, missing controls)
  2. Stop data leakage (PII in logs, error reports, analytics)
  3. Implement data subject rights (access, deletion, portability)
  4. Reduce excessive collection and enforce retention
  5. Formalize third-party data sharing controls
  6. Improve privacy documentation and transparency

## Data Flow Map
- High-level description of how personal data flows through the system
- Where personal data is collected, stored, processed, and shared
- Which third parties receive personal data and under what basis

## Open Questions
- Privacy decisions that need legal or compliance team input
- Assumptions about applicable regulations that should be validated
- Data processing activities where the legal basis is unclear
- Questions about data subject rights implementation scope

Important:
- Base findings on actual data handling code, database schemas, API payloads, and configuration.
- If you are not sure whether certain data qualifies as personal data, err on the side of caution and flag it.
- Prefer data minimization over adding controls to justify collection.
- Do not recommend compliance frameworks without considering the project's actual regulatory obligations.
- Consider the cost and user experience impact of privacy controls.
- Privacy is not just about compliance. Users trusting the application with their data is a product concern.
- Call out when privacy practices are already thorough and well-implemented in specific areas.
