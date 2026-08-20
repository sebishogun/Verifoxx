# Repository Guide

## Sources Of Truth

- `Verifoxx_AI_Engineer_Assignment.pdf` is the original assignment; `Requirements.md` is its text transcription. Resolve any discrepancy in favor of the PDF.
- The repository is not scaffolded yet: there is no module, source code, README, CI, or build/test configuration. Do not invent commands or assume Go solely from the parent directory name; update this file once the implementation establishes them.

## Assignment Constraints

- Keep the solution within the stated 4-5 hour exercise scope: a small runnable program or service with minimal setup, not a large system or polished UI.
- Model the three natural-language requirements as a reusable intermediate semantic representation. Do not hardcode outcomes for the five supplied requests or reduce the representation to flat field extraction.
- Requirement IDs `R1`-`R3` and request IDs `R1`-`R5` occupy different namespaces. Name their types and variables accordingly to avoid mixing them.
- Decisions are exactly `Approve`, `Reject`, `Revise`, or `Escalate`:
  - `Reject` is for a violated non-negotiable condition.
  - `Revise` requires a bounded corrective change, such as reduced scope or usage, or an allowed additional evidence item.
  - `Escalate` is required for missing, incomplete, stale, unclear, or conflicting required evidence, and for an unverifiable execution environment.
- Protected-data processing must use a verified approved local environment. Disclosure restrictions and pre-execution approval cannot be relaxed, including for trusted internal teams.
- Usage above the standard limit is available only to trusted internal teams with a specific, current usage-adjustment approval.

## Required Submission

- Include runnable source, a README with setup/run instructions and input/output format, and machine-readable results for all five supplied requests.
- Keep the design note to at most one page. Cover the semantic representation, why it is more useful than flat extraction, decision logic, escalation boundaries, and next improvements.
- Results should identify the request, decision, bounded rationale, applied requirements, used evidence, missing or conflicting evidence, assumptions, and unresolved uncertainty.
- State briefly where AI tools were used.
- Tests are optional in the brief; if added, prioritize edge cases around non-negotiable violations, bounded revisions, missing attestations, and stale or conflicting approvals.
