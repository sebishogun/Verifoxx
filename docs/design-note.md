# Design Note

## Semantic Representation

Verifoxx represents policy meaning rather than copying phrases into flat
fields. The versioned policy document defines requirement applicability,
Boolean assertions, evidence obligations, outcome precedence, uncertainty
resolutions, explanation templates, and bounded remediations. Every requirement
and clause retains typed IDs and source spans. Evidence rules retain kind,
required state, subject, scope, and timing, so "approval exists" is distinct
from "current approval from the required reviewer existed before execution."

Strict JSON decoding builds a pointerless semantic AST in parallel typed
columns with CSR child edges. Validation checks references, types, catalogs,
cycles, resolution completeness, and resource limits. Lowering canonicalizes
that AST into an immutable instruction schedule with struct-of-arrays values,
four-valued truth masks, reason masks, provenance tables, and precompiled
explanations. Evaluation therefore operates over one reusable representation;
the five request IDs are data, not branches in the evaluator.

## Why It Exceeds Flat Extraction

A flat extraction could record `environment=local`, but could not preserve why
the environment matters, which attestation verifies it, what stale or
conflicting evidence means, or whether a correction is allowed. The graph keeps
applicability separate from obligations and keeps hard restrictions separate
from uncertainty. It can consequently evaluate new requests consistently,
identify the driving requirement and clause, report used and missing evidence,
and produce a bounded remediation without reparsing the requirement prose.

## Decision Logic

Applicable requirements are evaluated in bulk against typed request columns and
CSR evidence references. Clause assertions and evidence obligations produce
truth plus explicit reason masks. The policy resolution table maps those states
to the four decisions, and deterministic precedence combines multiple clauses:

- `Approve` means every relevant condition and required evidence is satisfied.
- `Reject` is reserved for a violated non-negotiable condition, such as
  individual-level disclosure.
- `Revise` requires an enumerated bounded correction. In the supplied policy,
  an otherwise eligible trusted internal request may add the allowed scoped
  usage-adjustment approval.
- `Escalate` prevents automatic approval when safe evaluation is impossible.

The result materializer follows retained provenance to emit the driver,
requirements applied, evidence used, assumptions, unresolved uncertainty, and
remediation. Outcome text is policy-owned rather than assembled ad hoc in the
evaluation kernel.

## Escalation Boundaries

Missing, incomplete, stale, unclear, unverifiable, or conflicting required
evidence escalates when it affects a mandatory approval or approved execution
environment. A remote or unverified environment never becomes acceptable
through trust. Disclosure restrictions and pre-execution approval are
non-negotiable, including for trusted internal teams. `Revise` is used only when
the policy names a finite allowed change; it is not a fallback for uncertainty.

## Next Improvements

Next improvements would add a reviewed natural-language-to-policy proposal
workflow with exact citations, broaden hand-reviewed conformance cases, and add
authoring diagnostics that show semantic diffs before publication. Production
hardening would also expand deployment telemetry and compatibility testing
without changing the deterministic core or exposing protected rows in audit,
logs, or traces.
