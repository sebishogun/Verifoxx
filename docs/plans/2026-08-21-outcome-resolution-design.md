# Outcome Resolution Design

## Scope

Task 9 adds the engine-defined uncertainty reasons and policy-defined outcome,
remediation, and resolution tables used by later compiler and evaluator tasks.
The runtime stores numeric IDs and contiguous slices only. It does not embed
the NornRune decision labels in engine control flow.

Leaf evaluation, clause lowering, batch result storage, and explanation text
remain later tasks.

## Alternatives

Three representations were considered:

1. Fixed engine enums for Approve, Reject, Revise, and Escalate. This is small,
   but it violates the policy-pack ownership of outcome labels and precedence.
2. String-keyed maps of reason rules and remediation objects. This is flexible,
   but adds hashing, allocation, and pointer-heavy data to the row path.
3. Fixed reason bits plus policy-owned structure-of-arrays tables. This keeps
   uncertainty semantics explicit while retaining bulk, allocation-free data
   access. This is the selected representation.

## Reasons

Engine reasons use the existing one-based `schema.ReasonID` namespace:

```text
1 Missing
2 Stale
3 Unclear
4 Unverifiable
5 Wrong scope
6 Wrong subject
7 Wrong timing
8 Invalid
9 Conflict
```

`truth.ReasonMask` is a `uint16` whose bit `reason-1` indicates an active
reason. Helpers create one reason bit, test membership, and reject bits outside
the nine-reason domain. The mask is the scalar form used during resolution;
later evaluator tasks store each active reason across rows in bitplanes.

Reasons remain distinct even when they select the same outcome. Wrong scope,
wrong subject, wrong timing, and invalid evidence initially lower through a
clause's `OnUnverifiable` outcome, but the selected `ReasonID` remains available
for explanations.

## Outcome Table

Outcomes remain policy-defined:

```go
type OutcomeTable struct {
    Names      []schema.SymbolID
    Precedence []uint8
    Terminal   []bool
}
```

`OutcomeID` is one-based. Higher numeric precedence wins. Equal precedence is
resolved by the lower `OutcomeID`, which makes catalogue order the stable final
tie-break. The `Terminal` column is returned as metadata; it does not stop
reduction before higher-precedence candidates have been considered.

Lookup returns a small value record without allocating. A generic `Prefer`
operation supports later reduction of satisfied, false, and unresolved clause
outcomes.

## Remediation Table

Runtime remediations use a second non-owning SoA view:

```go
type RemediationTable struct {
    Kinds         []RemediationKind
    Fields        []schema.FieldID
    Values        []schema.ValueID
    EvidenceKinds []schema.EvidenceKindID
}
```

The supported kinds mirror the bounded source language:

- Set one field to one typed value.
- Request one allowed evidence kind.

The source AST and compiled result table intentionally have separate kind
types. Task 10 converts between the source and runtime layers while lowering.
Lookup returns a fixed-size record. No arbitrary commands, maps, callbacks, or
text enter the table.

## Resolution Table

Resolution rules are flattened into fixed nine-row blocks:

```go
type ResolutionTable struct {
    OutcomeIDs        []schema.OutcomeID
    RemediationStarts []uint32
    RemediationCounts []uint16
    RemediationIDs    []schema.RemediationID
}
```

`RuleSetID` is one-based. For a rule set, row `base + reason - 1` selects one
outcome and one CSR range of remediation alternatives. Per-reason ranges allow
missing usage approval to return bounded revision alternatives while stale or
conflicting approval returns escalation without presenting a relaxable fix.

`NewResolver` validates all table shapes, outcome references, remediation
payloads, and CSR ranges once. It stores borrowed slice headers and requires
the backing arrays to remain immutable. Validation is a policy-load operation,
not row work.

`Resolver.Resolve` then:

1. Scans reason IDs 1 through 9 in stable order.
2. Ignores inactive reason bits.
3. Selects the highest-precedence outcome.
4. Uses lower `OutcomeID` for an equal-precedence tie.
5. Retains the lower `ReasonID` when the same outcome is driven by several
   reasons.
6. Returns the winning outcome, driver reason, terminal metadata, and a
   borrowed remediation-ID range.

An empty reason mask produces no resolution. Invalid masks and invalid rule-set
IDs are programming errors after construction and panic with static messages.
The valid path performs no allocation.

## Source Lowering

Task 10 expands each validated source clause into one rule block:

| Runtime reason | Source resolution column |
|---|---|
| Missing | `OnMissing` |
| Stale | `OnStale` |
| Unclear | `OnUnclear` |
| Unverifiable | `OnUnverifiable` |
| Wrong scope | `OnUnverifiable` |
| Wrong subject | `OnUnverifiable` |
| Wrong timing | `OnUnverifiable` |
| Invalid | `OnUnverifiable` |
| Conflict | `OnConflict` |

Satisfied and false assertion outcomes are not reasons. The executor feeds
those outcome IDs into the same policy-precedence reducer separately.

## Tests

Tests will cover:

- Nine unique one-hot reasons and invalid high bits.
- Policy-defined precedence and lower-ID ties.
- Stable driver-reason ties.
- Every reason remaining distinguishable after resolution.
- Missing usage approval selecting Revise with set-usage and allowed-evidence
  alternatives.
- Stale approval selecting Escalate with no remediation.
- Simultaneous missing and stale reasons selecting by policy precedence.
- Outcome and remediation lookup.
- Constructor rejection of malformed columns, references, payloads, and CSR
  ranges.
- Zero allocations in repeated valid resolution.

The tests use numeric IDs and fixed slices. Assignment labels appear only as
test fixture meaning, not as engine branches.
