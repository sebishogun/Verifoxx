# JSON Batch Decoder Design

**Date:** 2026-08-22
**Status:** Approved

## Goal

Decode the request and evidence JSON packs directly into the reusable Task 13
SoA builder. Malformed transport data must return stable positional errors;
missing policy facts must remain absent so the evaluator can produce Unknown
rather than converting absence into a decode failure.

## API And Ownership

`jsonbatch.Decoder` is a reusable, single-goroutine worker. Its method accepts
an `eval.Builder`, immutable `program.Program`, request source, evidence source,
and explicit limits:

```go
func (d *Decoder) Decode(
    dst *eval.Builder,
    p *program.Program,
    requests []byte,
    evidence []byte,
    limits Limits,
) (eval.Batch, error)
```

The package-level `Decode` wrapper uses a fresh Decoder. On success, the
returned Batch has the Task 13 borrowed lifetime. Once `Begin` has occurred,
any decode error calls `Builder.Abort`; partial columns can never be published
with `Finish`. A later Decode reuses their capacities.

The decoder retains no source or Program pointer after a call. It retains only
typed scratch capacities and open-address lookup slabs.

## Input Contract

The evidence document is decoded before requests so every reference can resolve
to a zero-based evidence row. Both roots require:

- `schema_version: 1`
- `pack`: exact match for `Program.PolicyName`
- Exactly one payload array: `evidence` or `requests`

Request IDs use canonical `R<n>` syntax and evidence IDs/references use
canonical `E<n>` syntax. `n` is a nonzero uint32 decimal with no leading zero.
The numeric portion becomes the strong ID. Reusable open-address tables reject
duplicate request/evidence IDs and map evidence IDs to rows without strings or
maps.

A request object requires `id`; `evidence_refs` is optional and defaults to an
empty range. Other keys are facts. One nested object level is flattened with a
dot, preserving the JSON path exactly:

```text
"requester": {"team": "external_partner"}
    -> requester.team
```

Top-level scalar facts are also permitted. The path must resolve through the
Program's immutable field-name symbols and the decoder's reusable
`SymbolID -> FieldID` table. Unknown paths, duplicate paths, arrays/objects in
fact positions, and values of the wrong schema kind are transport errors.

An omitted fact or explicit JSON `null` is semantically missing: its presence
bit remains clear. Symbols are interned through `eval.Builder.InternSymbol`;
integers and timestamps accept JSON int64 grammar; Booleans accept true/false;
presence-only fields accept true and set only the presence bit.

## Evidence Normalization

An evidence object requires `id`, `type`, and `attributes`. `type` resolves to
the Program evidence-kind catalog. `attributes.status` is required and resolves
to the Program evidence-state catalog. The remaining supported attributes map
to the fixed evidence SoA:

| JSON attribute | Evidence column |
|---|---|
| `subject` | `Subjects` |
| `adjustment_type` | `Subjects` as the adjusted target |
| `scope` | `Scopes` |
| `reviewer` | `Reviewers` |
| `reviewer_state` | `Reviewers` when no reviewer is present |
| `timing` | `Timings` |
| `timestamp` | `Timestamps` |

`timestamp_state` and `attestation_state` are bounded state qualifiers. Values
`current` and `valid` leave the primary status unchanged. An unresolved
qualifier (`stale`, `unclear`, `unverifiable`, `invalid`, `conflicting`, or
`conflict`) replaces the primary state with the corresponding Program state;
`reviewer_state=one_valid_one_revoked` similarly becomes `conflicting`. This
preserves the assignment's uncertainty information without adding per-record
attribute maps. A required override absent from the Program state catalog is an
invalid reference rather than silently discarded evidence.

Unknown attributes and duplicate semantic columns are rejected. Optional
attributes remain zero when absent.

## Decoder Shape

The fixed input format does not carry row counts. The decoder therefore uses:

1. A lightweight structural pass over each source to count requests, evidence
   rows, and evidence references while enforcing source/count/depth limits.
2. One exact `Builder.Begin` call.
3. A semantic pass that writes evidence records and request facts directly to
   the caller-owned SoA columns.
4. One validated CSR copy and `Builder.Finish`.

The count pass is preferred over staging decoded records: it touches input
bytes twice but avoids duplicate typed data, token arenas, and a third replay
pass. Every source byte and output cell still has bounded linear work for valid
input.

Field, evidence-kind, and evidence-state lookup tables are rebuilt only when
the Program pointer changes. Their capacities, ID tables, duplicate-ID tables,
CSR offsets/refs, seen-field bitsets, path bytes, and string decode buffers are
reused. No map, reflection, `encoding/json`, `[]byte`-to-string conversion, or
per-record allocation is used.

## Errors And Limits

`Error` carries an input enum (`requests` or `evidence`), stable `ErrorCode`,
byte offset, and static message. Codes distinguish malformed/truncated JSON,
trailing data, unknown or duplicate keys, missing structural keys, unsupported
version, wrong JSON type, invalid UTF-8, limit overflow, invalid Program
references, duplicate IDs/fields/references, and invalid canonical IDs.

Zero-valued limits disable that bound. Supported bounds cover source bytes,
decoded string bytes, requests, evidence rows, evidence references, facts per
request, attributes per evidence record, and structural depth. Count limits are
checked before `Builder.Begin`, preventing untrusted JSON from requesting huge
slabs.

## Verification

Tests cover:

- All five supplied requests and four evidence records, exact typed columns,
  presence masks, and CSR ranges.
- Missing and null facts versus missing required structural keys.
- Unknown/duplicate fields, IDs, keys, and evidence references.
- Wrong scalar types, malformed/truncated JSON, escapes, UTF-8, canonical-ID
  overflow, source/count/string/depth limits, and trailing input.
- Evidence kind/state resolution and uncertainty qualifier overrides.
- Failed-decode abortion followed by successful capacity-reusing recovery.
- Deterministic decode, 386 safety, fuzzing, and zero warm allocations for the
  supplied fixed shape.
