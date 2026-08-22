# Batch And Evidence Builders Design

**Date:** 2026-08-22
**Status:** Approved

## Goal

Materialize request facts and evidence into reusable, evaluator-ready
struct-of-arrays storage. The evaluator must receive typed contiguous columns,
bit-packed presence and Boolean planes, and CSR request-to-evidence edges. No
map, reflection, string conversion, or per-row allocation enters this boundary.

## Ownership And Lifecycle

`eval.Builder` owns one mutable `Batch` and one batch-local symbol interner. A
successful `Begin` sizes and clears all active slabs from the compiled
`index.Schema`, row count, evidence count, and evidence-reference count. Callers
then populate rows through typed setters and publish a borrowed `Batch` value
with `Finish`.

The returned slices remain valid until the next successful `Begin` on that
builder. This matches the service-context ownership model: decode, evaluate,
encode, then reset the entire context. Failed calls validate before writing;
failed `Begin` leaves the previous batch and extension symbols intact.

The builder is not safe for concurrent use. Separate service contexts own
separate builders. No `sync.Pool` is used.

## Fact Layout

`Batch.Rows` is fixed-width. Scalar values are column-major, so a field-local
column occupies one contiguous `Rows`-element range:

```text
value index = column * rows + row
```

Symbols use `[]schema.SymbolID`; integers and timestamps use separate `[]int64`
slabs. Boolean values use one bitmap per Boolean column. Presence uses one
bitmap per FieldID for every value kind:

```text
word count     = ceil(rows / 64)
bitmap index   = plane * wordCount + row / 64
bitmap bit     = 1 << (row % 64)
```

Typed setters write the value and set its presence bit in one operation.
Presence-only fields set only their presence bit. Missing facts retain zeroed
values and clear presence bits. Clearing every active slab at `Begin` also
clears final-word tail bits and prevents stale data from a larger prior batch.

`RequestIDs` contains one nonzero ID per row. Empty batches have zero-length
value and ID slabs, a one-element zero evidence-offset slice, and no bitmap
words.

## Evidence Layout

`EvidenceBatch` stores parallel columns for record ID, kind, state, subject,
scope, reviewer, timing, and timestamp. Record ID, kind, and state are required
and nonzero. Optional symbolic attributes use zero for absent; a zero timestamp
is absent for this bounded evidence model.

Request-to-evidence links use standard CSR:

- `EvidenceOffsets` has `Rows + 1` entries, starts at zero, is monotonic, and
  ends at `len(EvidenceRefs)`.
- `EvidenceRefs` contains zero-based evidence-row indices, each strictly less
  than the evidence-row count.

The bulk CSR setter validates all source ranges before copying, so malformed
edges cannot partially replace a valid relation.

## Symbol Identity

`InternSymbol` first probes the immutable Program symbol table. Unknown bytes
are interned once in the builder's reusable extension interner and receive:

```text
ProgramSymbolCount + local extension SymbolID
```

One extension table serves facts and evidence for the whole batch. Equal
unknown bytes therefore compare equal, different unknown bytes remain
distinct, and the Program is never mutated. Addition is checked before
publishing an ID so zero and uint32 wraparound are impossible.

`Builder.Symbol` resolves both Program and extension IDs through the same
service-context lifetime. Extension bytes and IDs are local to that builder's
current batch and must not be mixed with a batch from another builder. The
next successful `Begin` invalidates extension bytes from the prior batch.

When a builder binds a different Program, it validates that
`ProgramSymbolCount`, symbol rows, and frozen probe slots describe the same
complete ID range before reserving extension IDs above it. The immutable
Program contract lets repeated `Begin` calls on the same Program skip that
cold validation.

## Errors And Bounds

The package exposes stable sentinel errors for invalid builder state, invalid
rows or fields, wrong value kinds, malformed evidence/CSR, incomplete batches,
and fixed-width or host-index overflow. Size products are computed in `uint64`
and checked against `int` before any slice is resized.

`Finish` verifies all required request and evidence IDs plus the complete CSR
shape. It returns a shallow `Batch` value; this is a borrowed view, not an
immutable clone.

## Verification

Tests cover:

- Kind-local column offsets and typed values.
- Presence and Boolean planes across word and tail boundaries.
- CSR evidence ranges and malformed references.
- Program and extension symbol identity.
- Empty batches and invalid/incomplete inputs.
- Capacity reuse and poisoned-tail clearing after smaller rebuilds.
- Failure atomicity for `Begin` and individual setters.
- 386-safe size arithmetic and zero allocations after warm-up.

The benchmark reports `BatchBuilder` allocation counts after capacities and the
extension symbol table are warm.
