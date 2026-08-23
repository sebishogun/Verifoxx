# Policy-Authored Explanations Design

**Date:** 2026-08-22
**Status:** Approved by the user
**Roadmap:** Task 24, Phase 10

## Goal

Produce complete, bounded, deterministic explanations from compiled policy data
and numeric evaluation provenance. Policy authors own decision wording. The
evaluator continues to emit IDs and ranges only, and text is parsed once at the
policy boundary, compiled into immutable tables, and rendered only when an
adapter requests it.

The design is intended for a reusable evidence-driven policy product, not only
the supplied five requests. No request ID or supplied outcome is embedded in Go
control flow.

## Decisions

- Change policy schema version 1 in place. No policy format has been released,
  so a second compatibility path has no consumer.
- Require policy assumptions and complete explanation data for every clause
  resolution branch.
- Use hybrid ownership: resolution branches own rationale and unresolved
  uncertainty; evidence source nodes own evidence-issue wording.
- Support a closed set of typed placeholders. Do not add a general template
  language, functions, conditions, includes, or runtime expression evaluation.
- Preserve source NodeID identity separately from semantic instruction CSE.
- Compile templates into pointerless operation and argument columns.
- Record correlated reason, source-node, evidence-ID, and evidence-state
  provenance for the winning candidate.
- Append into caller-owned reusable storage and keep warmed materialization
  allocation-free.
- Keep text rendering scalar. Literal copies use bulk append/runtime memmove;
  custom SIMD is considered only after a benchmark demonstrates a useful gain.

## Source Schema

The root object gains one required `assumptions` array. It may be empty, and
each entry is a template string.

Every evidence predicate gains one required `explanation` object. `issue` is a
required fallback template. A policy may override any engine evidence reason:
`missing`, `stale`, `unclear`, `unverifiable`, `wrong_scope`, `wrong_subject`,
`wrong_timing`, `invalid`, or `conflict`.

```json
{
  "op": "evidence_matches",
  "kind": "approval_record",
  "state": "valid",
  "timing": "before_execution",
  "explanation": {
    "issue": "{evidence_kind} is {reason}.",
    "missing": "{evidence_kind} is missing from request {request_id}.",
    "conflict": "{evidence_id} {evidence_kind} has conflicting state."
  }
}
```

Each of the seven existing resolution entries changes from an outcome string to
an object containing an outcome and decision-level explanation. All seven keys
remain required: `satisfied`, `false`, `missing`, `stale`, `unclear`,
`unverifiable`, and `conflict`.

```json
"missing": {
  "outcome": "Revise",
  "explanation": {
    "rationale": "The request can be corrected by supplying the required scoped approval.",
    "uncertainty": [
      "Whether qualifying approval will be supplied remains unresolved."
    ]
  }
}
```

Outcome and wording are colocated so parallel objects cannot drift. Existing
structured remediation remains clause-owned and is not duplicated in prose.

## Template Language

`{name}` inserts one typed binding. `{{` and `}}` emit literal braces. A lone
brace, empty placeholder, unknown name, nested brace, or placeholder illegal in
the current context is rejected before compilation.

Decision rationale and uncertainty templates may use:

- `policy_name`
- `policy_version`
- `request_id`
- `outcome`
- `requirement_id`
- `clause_id`
- `node_id`
- `reason` only on unresolved branches

Policy assumptions may use only `policy_name`, `policy_version`, and
`request_id`.

Evidence issue templates may additionally use:

- `evidence_kind`
- `evidence_state`
- `required_evidence_state`
- `evidence_id` only in a non-missing reason override

The required fallback issue template may handle Missing, so it cannot use an
actual evidence ID or state. A Missing override has the same restriction. A
missing record has no semantically valid EvidenceID; the output names the
required evidence kind instead of inventing an ID.

IDs render with stable namespace prefixes (`R`, `C`, `N`, and `E`) followed by
unsigned decimal digits. Reasons render as fixed lower-snake-case names.
Outcome, policy, evidence-kind, and evidence-state names are borrowed from the
compiled symbol slab.

## Bounds

Hard limits apply even when adapter limits are otherwise disabled:

| Item | Maximum |
|---|---:|
| Decoded template bytes | 512 |
| Operations per template | 32 |
| Policy assumptions | 8 |
| Uncertainty entries per explanation | 8 |
| Rendered template bytes | 1,024 |
| Complete rendered explanation bytes per row | 4,096 |

Decoder limits may tighten these values but cannot raise them. The AST builder
uses widened arithmetic for every byte and edge count. Compilation rejects any
template whose maximum expansion can exceed its bound, and rejects any
explanation whose combined maximum can exceed the row bound.

## AST Representation

`schema.TemplateID` and `schema.ExplanationID` are one-based strong IDs. The AST
stores no per-template object or string:

```go
type Document struct {
    TemplateBytes         []byte
    TemplateOpStarts      []uint32
    TemplateOpCounts      []uint16
    TemplateLiteralStarts []uint32
    TemplateMaxBytes      []uint32
    TemplateOps           []TemplateOp
    TemplateArgs          []uint32

    ExplanationRationales      []schema.TemplateID
    ExplanationUncertaintyStarts []uint32
    ExplanationUncertaintyCounts []uint16
    ExplanationUncertaintyIDs    []schema.TemplateID
    AssumptionTemplateIDs         []schema.TemplateID

    EvidenceIssueStarts      []uint32
    EvidenceIssueCounts      []uint8
    EvidenceIssueTemplateIDs []schema.TemplateID
}
```

The builder parses each decoded template once. Literal operations refer to a
byte count in `TemplateArgs`; placeholder operation kinds encode the binding.
All literal fragments for one template are contiguous. The evidence fallback
and overrides are expanded into nine TemplateIDs in fixed reason order, so the
runtime has no fallback branch.

Each clause stores seven ExplanationIDs in fixed resolution-state order.
Structural validation covers every parallel column, CSR range, ID reference,
operation kind, literal extent, context mask, and expansion bound. Lowering
does not rescan template source text.

## Program Representation

The frozen Program owns result-package template and explanation views over
exact copied columns:

```go
type TemplateTable struct {
    LiteralBytes  []byte
    OpStarts      []uint32
    OpCounts      []uint16
    LiteralStarts []uint32
    MaxBytes      []uint32
    Ops           []TemplateOp
    Args          []uint32
}

type ExplanationTable struct {
    RationaleTemplateIDs   []schema.TemplateID
    UncertaintyStarts      []uint32
    UncertaintyCounts      []uint16
    UncertaintyTemplateIDs []schema.TemplateID
    AssumptionTemplateIDs  []schema.TemplateID
}
```

Program columns also retain:

- requirement applicability source NodeIDs;
- clause assertion source NodeIDs;
- clause evidence source NodeIDs parallel to canonical InstructionIDs;
- sparse NodeID-to-nine-issue-template ranges;
- satisfied and false ExplanationIDs per clause; and
- ExplanationIDs parallel to the nine rows of each resolution rule set.

Semantic CSE continues to merge equivalent instructions. Explanation lookup
uses the retained source NodeID, so two equivalent expressions with different
authored wording remain distinct without duplicating evaluator work.

The four engine reasons lowered through `unverifiable` reuse that branch's
decision ExplanationID. Their node-owned issue templates preserve the more
specific WrongScope, WrongSubject, WrongTiming, or Invalid reason.

## Evaluation Provenance

`outcomeCandidate` carries the selected ExplanationID. `result.Batch` adds:

```go
DriverExplanations []schema.ExplanationID

ReasonNodes          []schema.NodeID
ReasonEvidenceIDs    []schema.EvidenceID
ReasonEvidenceStates []schema.EvidenceStateID
```

The three reason columns are parallel to `ReasonIDs` and use its existing CSR
offsets. EvidenceID and state are zero when no record exists or when the reason
comes from a fact predicate.

For each winning unresolved candidate, reasons remain in ascending ReasonID
order. The evaluator records the first causal source node for each reason. For
an evidence node it records the first causal evidence record in request edge
order. Aggregate conflict without a conflict-state record uses the first
participating record. This is deterministic and bounded.

Evidence matching is not implemented twice. A shared allocation-free
per-record classifier supplies both the bitplane evaluator and selected-row
provenance scan. Provenance scanning occurs only for reasons on the winning
candidate.

`EvidenceIDs` is populated from each request's referenced evidence in input
order. Applied requirements retain Program order. Scheduler shard validation
and merge require every new parallel column to have exactly the corresponding
CSR edge count.

## Materialization API

`program.Program` exposes a borrowed `result.ExplanationCatalog` view. A
zero-value `result.Explainer` binds and validates that immutable catalog once.
Materialization accepts a result batch row and RequestID, validates every input
range before mutation, then fills caller-owned reusable storage:

```go
type TextRange struct {
    Start uint32
    End   uint32
}

type Materialized struct {
    Bytes          []byte
    EvidenceIssues []TextRange
    Assumptions    []TextRange
    Uncertainty    []TextRange
    Remediations   []RenderedRemediation
    Requirements   []schema.RequirementID
    Evidence       []schema.EvidenceID
    Rationale      TextRange
}
```

Requirements and Evidence are zero-copy views into `result.Batch`. Text ranges
index one caller-owned byte slab. Rendered remediation records retain kind and
typed value metadata while their field, value, or evidence-kind names point
into the same byte slab. Task 25 can encode this representation directly
without maps, reflection, `fmt`, or string conversion.

Materialization order is fixed:

1. rationale;
2. evidence issues in ReasonID order;
3. policy assumptions in source order;
4. uncertainty entries in source order; and
5. remediation records in evaluator order.

A sufficiently sized warmed destination performs zero allocations. Literal
segments use bulk append. Decimal formatting appends directly. There is no
custom SIMD text kernel; Task 25 may benchmark bulk JSON escape scanning, and
Task 48 owns broader native-aware dispatch work.

## Errors And Atomicity

The JSON adapter reports existing bounded syntax/reference/limit error codes at
the offending source offset. The AST builder returns static sentinel errors for
invalid template syntax, context, bounds, or explanation shape. The compiler
validator reports table/member/row diagnostics for corrupt manually built
documents.

The result package adds static sentinels for invalid catalog, invalid result
provenance, and explanation output too large. `Explainer.Bind` changes its
usable catalog only after complete validation. `Materialize` validates all row
ranges and IDs before resetting active destination lengths. Failed calls leave
the previous materialized result unchanged.

## Migration

- Keep `schema_version: 1` and migrate every repository policy fixture.
- Preserve the current five policy-authored rationales and assumptions.
- Replace invented IDs in missing-evidence prose with the required evidence
  kind; retain actual evidence IDs for conflicting/stale/invalid records.
- Update policy content hashes and machine-readable golden output.
- Replace the conformance test's hardcoded `semanticDetails` switch with the
  compiled Explainer.
- Add shared fixture builders for complete explanation tables so tests do not
  repeat boilerplate.

## Verification

Tests cover:

- all seven resolution branches and all nine evidence reasons;
- every legal placeholder and every context-invalid placeholder;
- escaped braces, malformed templates, operation limits, byte limits, and
  maximum expansion limits;
- required assumptions, evidence fallback, complete branch data, unknown keys,
  duplicate keys, and deterministic source offsets;
- AST corruption diagnostics for every new column and CSR range;
- exact lowering, freeze ownership, source-node preservation across CSE, and
  poisoned Lowerer reuse;
- scalar, indexed, SIMD, range, and scheduler equivalence for every new result
  provenance column;
- causal evidence selection, aggregate conflict, and missing evidence with a
  zero EvidenceID;
- exact policy-authored text, stable list ordering, structured remediation,
  bounded output, failed-call atomicity, and poisoned destination reuse;
- zero warm materialization allocations with pre-sized caller storage;
- native, purego, 386, race/checkptr, vet, field-alignment, formatting, and
  full-repository gates; and
- an explanation benchmark separated from evaluator and JSON encoding cost.
