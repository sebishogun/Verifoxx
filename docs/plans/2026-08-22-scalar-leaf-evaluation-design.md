# Scalar Leaf Evaluation Design

**Date:** 2026-08-22

**Status:** Approved

## Goal

Implement the allocation-free scalar reference for compiled fact and evidence
leaves. Each kernel evaluates a complete request batch into four-valued truth
bitplanes and nine sideband reason planes. Task 16 will compose these leaf
results through the existing liveness-assigned slots; Tasks 18-19 will preserve
the scalar behavior while adding SIMD dispatch.

Task 15 does not execute Boolean instructions, resolve outcomes, prune policies,
materialize explanations, or own evaluator scratch.

## Selected Approach

Use direct typed SoA kernels over `eval.Batch` and immutable `program.Program`
columns. A fact kernel dispatches once by opcode and value kind, then scans the
corresponding contiguous column. An evidence kernel scans each request's CSR
range and reduces matching records without staging per-request objects.

Two alternatives were rejected:

- Predecode every leaf into a second runtime instruction plan. This can remove
  some scalar dispatch, but duplicates immutable Program data before SIMD
  measurements identify a worthwhile shape.
- Evaluate row objects through interfaces or callbacks. That obscures column
  access, prevents whole-slice acceleration, and introduces dispatch and likely
  allocation on the per-row path.

The direct kernels remain the executable specification. Later optimized paths
must match them through differential tests.

## Output Views

Truth uses the existing non-owning `truth.Planes`. Reasons use a non-owning
`ReasonPlanes` view over exactly:

```text
truth.ReasonCount * truth.WordCount(rows)
```

`uint64` words. The layout is reason-major within one reason slot:

```text
[Missing words][Stale words]...[Conflict words]
```

This is the inner layout assumed by Task 11's scratch-size formula. Task 16 can
slice one slot from a flat slot-major scratch slab without allocation. Every
leaf clears its complete destination truth and reason range before writing, and
unused tail bits remain zero.

## Fact Predicates

The kernel reads the instruction's `FieldID`, resolves its kind-local column
through `Program.FieldIndex`, and resolves canonical literals directly from the
Program value columns. It supports all eight leaf opcodes:

- `Equal` and `NotEqual` for symbols, integers, Booleans, and timestamps.
- `In` for a bounded Program-owned literal range of one field kind.
- `Less`, `LessEqual`, `Greater`, and `GreaterEqual` for integers and
  timestamps.
- `Exists` for every field kind, including presence-only fields.

For a present fact, a comparison match is True and a mismatch is False. A
missing fact is Unknown and sets `ReasonMissing`; this includes `Exists`, so a
missing required field escalates instead of becoming a definite policy
violation. There is no invalid-value row state because adapters reject type
errors before a Batch is published.

Symbol equality compares `SymbolID` directly. Program and batch-extension
symbols share the disjoint namespace established by Tasks 13-14, so no byte
comparison or range branch is needed in the row loop. Boolean and presence
columns are consumed a word at a time. Other scalar kinds build one match word
at a time from contiguous values.

## Evidence Predicates

The scalar evidence query is fixed width:

```go
type EvidencePredicate struct {
    Kind    schema.EvidenceKindID
    State   schema.EvidenceStateID
    Subject schema.SymbolID
    Scope   schema.SymbolID
    Timing  schema.SymbolID
}
```

Kind and state are required. Zero subject, scope, or timing means that attribute
is unconstrained. Nonzero constraints compare exact shared `SymbolID`s. The
current compiled Evidence opcode supplies kind and state; the optional fields
make the kernel complete for wrong-subject, wrong-scope, and wrong-timing
semantics without maps and allow later policy lowering to populate those
constraints without changing the evaluator contract.

For each request, the kernel scans only its `EvidenceRefs` CSR range:

1. Records of other kinds are ignored.
2. No record of the requested kind produces Unknown plus `ReasonMissing`.
3. An exact state and attribute match contributes positive evidence.
4. A resolved nonmatching state contributes negative evidence.
5. `stale`, `unclear`, `unverifiable`, or `invalid` states contribute the
   corresponding reason without a positive or negative bit.
6. A conflicting state contributes both truth bits and `ReasonConflict`.
7. Attribute mismatches contribute `ReasonWrongSubject`, `ReasonWrongScope`,
   or `ReasonWrongTiming` and do not satisfy the query.

Contributions are OR-reduced. If separate records produce both positive and
negative evidence, the row is Conflict and also sets `ReasonConflict`.
Unresolved records may coexist with a satisfying record; the truth result then
remains True while reasons stay available for retained diagnostics. Task 16
consults reasons only when the composed semantic root is unresolved.

Evidence state names are classified once per immutable Program into a reusable
`EvidenceStateIndex`, indexed by `EvidenceStateID-1`. The recognized unresolved
names are `stale`, `unclear`, `unverifiable`, `invalid`, `conflict`, and
`conflicting`; other states are resolved states. Binding compares catalog bytes
without string conversion and reuses capacity when the Program changes. The
per-record kernel performs only numeric lookups.

## Contracts And Errors

The source decoder, compiler, and Batch builder establish all structural
invariants before execution. Task 15's unexported hot kernels therefore treat
malformed parallel columns, scratch shapes, value references, CSR ranges, and
zero query IDs as evaluator defects and panic with static messages before row
mutation where practical. Recoverable request errors remain adapter errors and
never enter the kernel.

`EvidenceStateIndex.Bind` is a cold Program-binding operation and returns a
static error for malformed catalog columns or counts that exceed host limits.
A failed bind leaves the previous usable index unchanged. Rebinding the same
Program does no work.

All index arithmetic widens before addition or multiplication. Rows zero is a
valid no-op shape. Kernels retain neither Batch nor source JSON references.

## Verification

Tests cover:

- Every comparison opcode over its legal value kinds.
- True, False, and missing/Unknown rows, including `Exists`.
- `In` with zero, one, duplicate, and several literals.
- Row counts 0, 1, 63, 64, and 65 with poisoned destination tails.
- Exact evidence, absent kind, resolved wrong state, every unresolved state,
  wrong subject/scope/timing, and malformed references.
- Multiple records producing True, False, Unknown, and Conflict reductions.
- Arbitrary evidence catalog order and state-index rebinding without stale
  classifications.
- Warm repeated fact and evidence evaluation at `0 B/op, 0 allocs/op`.
- Normal, race, and `GOARCH=386` test runs plus the pinned field-alignment
  analyzer for new production structs.

Benchmarks report fact and evidence `ns/op`, `B/op`, and `allocs/op` at a
representative batch size. They are scalar baselines for Tasks 18-20, not SIMD
performance claims.
