# Policy Compatibility Frontends Design

**Status:** Approved by the Phase 18 roadmap

**Date:** 2026-08-24

## Goal

Add bounded CEL, Rego, Cedar, and Protobuf compilation frontends without adding
another evaluator. Each accepted source program becomes the same Verifoxx AST
and immutable Program used by native policy JSON. Compatibility claims apply
only to the documented subset and its differential corpus.

## Boundaries

- Parsing, type checking, source diagnostics, and generated-code reflection are
  cold compile-time work.
- Evaluator packages do not import CEL, OPA, Cedar, protobuf reflection, or a
  frontend package.
- Every parser has source-byte, node, depth, field, literal, and child limits.
- Unsupported syntax is rejected with an exact source span; it is never
  approximated or silently ignored.
- Runtime missing values remain explicit uncertainty unless the source
  language defines a bounded default that can be represented by the policy
  resolution table.

## Shared Contract

The public `frontend` package defines language names, source spans, bounded
diagnostics, field declarations, capabilities, limits, and a pointerless
semantic `Policy`. The policy stores expression kind, compare operation, field
reference, literal reference, child start/count, and source start/end in
parallel columns. Variable-length child and list relationships use CSR arrays.
An append-only definedness node carries one field and no literal or edges. It
lowers to an append-only core `Defined` instruction that returns known true for
a present field and known false for an absent field. The existing `Exists`
instruction remains unchanged: absent input is unknown with `ReasonMissing`.
Field and literal payloads are compile-time tables, not evaluator data.

Each language package owns only conversion from its official parser AST into
that table. `internal/frontend` validates the table, binds declared fields into
`schema.Schema`, builds an `ast.Document`, and invokes the existing compiler.
The lowerer creates one requirement and one clause with policy-defined
`Approve`, `Reject`, and `Escalate` branches. A known expression result maps to
approve/reject; missing or unsupported runtime knowledge maps to escalate
unless an explicit source-language default maps it to reject.

An applicability tautology is built as `expr OR NOT expr`. It is true for known
Boolean results and remains unknown for missing data, so a false policy still
reaches its rejecting clause while absent input remains unresolved.

## Capability Matrix

### CEL

| Construct | Status |
|---|---|
| Declared `bool`, `int`, and `string` variables | Supported |
| `==`, `!=`, `<`, `<=`, `>`, `>=` | Supported for matching scalar types |
| `&&`, `||`, `!` | Supported |
| `in` with a constant homogeneous list | Supported |
| Missing declared activation value | Lowered to Verifoxx unknown |
| Object selection with an explicit field binding | Lowered with restrictions |
| Macros, dynamic dispatch, calls, maps, messages, comprehensions | Rejected |
| `double`, `uint`, bytes, duration, timestamp, null | Rejected initially |

The CEL environment disables macros and declares every accepted variable before
checking. The checked AST, rather than source-token pattern matching, drives
lowering.

### Rego

| Construct | Status |
|---|---|
| Rego v1 package with one configured Boolean decision | Supported |
| Optional Boolean `default` for that decision | Supported |
| Complete `allow if { ... }`-style rules | Supported |
| Multiple complete rules | Lowered as OR |
| Conjunctive rule bodies and scalar comparisons | Supported |
| `input.<path>` references and scalar literals | Supported |
| `in` over a constant homogeneous array/set | Supported |
| `not` over a supported scalar expression | Supported with exact presence-aware lowering |
| Imports, `data`, functions, recursion, comprehensions, partial sets/objects | Rejected |
| Mutation-like or other built-ins | Rejected |

Without a default, undefined input produces uncertainty. An explicit
`default allow := false` maps unresolved evaluation to Reject, matching the
bounded complete-rule decision contract.

Rego negation is negation-as-failure: `not E(input.x)` succeeds when `input.x`
is absent, whereas the shared four-valued `Not` preserves missing as unknown.
The Rego frontend therefore lowers a negated field atom as
`NOT DEFINED(field) OR NOT E(field)`. Negated constants use ordinary `Not`.
`Defined` reads the existing presence masks in one bitwise pass and clears all
reason planes. It is distinct from native `Exists`, so CEL and native policy
semantics remain unchanged.

### Cedar

| Construct | Status |
|---|---|
| Static `permit` and `forbid` policies | Supported |
| Principal, action, and resource equality constraints | Supported |
| Boolean `when`/`unless` conditions over declared context fields | Supported |
| Scalar context comparisons and Boolean composition | Supported |
| Multiple permit/forbid policies | Supported with forbid precedence |
| Entity hierarchy `in` and entity attributes | Rejected initially |
| Sets, records, extension functions, templates, annotations | Rejected |

All permit expressions are ORed, all forbid expressions are ORed, and the final
expression is `anyPermit AND NOT anyForbid`. No permit therefore rejects, and a
matching forbid wins independently of source order. Missing context escalates.

### Protobuf

`frontend/proto/options.proto` defines message options for policy name,
version, and CEL expression plus field options for canonical field names.
`protoc-gen-verifoxx` validates scalar field types and emits a static
`frontend.BindingSet`. Generated code contains no descriptor walk or runtime
reflection. Repeated, map, oneof, message, enum, floating-point, unsigned, and
bytes fields are rejected until their semantics receive an explicit capability
entry.

## Data Flow

```text
source bytes + declarations
        |
        v
official parser/checker
        |
        v
bounded frontend.Policy (SoA + CSR + spans)
        |
        v
internal/frontend validation and binding
        |
        v
ast.Document -> compile.Lowerer -> immutable Program
        |
        v
existing scalar/SIMD/indexed/parallel evaluator
```

## Diagnostics And Limits

Diagnostics are deterministic and sorted by source start, source end, then
code. Codes distinguish syntax, type, unsupported, unknown field, duplicate,
and limit failures. A failed compile returns no partial policy. Limits are
checked before allocating parser-owned proportional storage where the upstream
API permits it, then checked again while translating nodes and edges.

No diagnostic includes request or evidence payloads. Source excerpts are
caller-owned presentation data and are not retained in errors.

## Testing

- Public contract tests lock limits, spans, ordering, malformed table handling,
  and deterministic capabilities.
- Each language has positive and negative corpus cases plus malformed, Unicode,
  depth, size, duplicate, and unsupported tests.
- Supported CEL, Rego, and Cedar cases are evaluated by both the official
  engine and the Verifoxx Program over the same typed rows.
- Cross-frontend conformance proves equivalent supported expressions compile to
  equivalent decisions for true, false, and missing inputs.
- Protobuf tests construct a code-generator request, inspect deterministic
  output, compile a fixture, and run generated-code drift checks.
- Fuzzers target every parser and the shared semantic validator.

## Performance

Benchmarks separate official parse/check, semantic translation, Verifoxx
lowering, and warm evaluation. Parser allocations are reported but are outside
the evaluator contract. Warm scalar, SIMD, indexed, and parallel evaluation
must retain their existing zero-allocation behavior and linked-binary parity;
frontend dependencies cannot be imported by evaluator packages.
