# AST Validation Design

## Scope

Task 7 validates a decoded or programmatically constructed `ast.Document`
before normalization and lowering. Validation remains separate from JSON
parsing: `jsonpolicy.Error` reports source grammar and decode failures, while
`compile.Diagnostic` reports structural and semantic AST defects.

The validator must accept corrupted public AST columns without panicking. It
must not mutate the document or field schema. Diagnostics accumulate in a
stable order so the CLI, debugger, and tests receive reproducible output.

## API And Ownership

`compile.Validator` owns reusable graph state and an iterative traversal stack.
Its zero value is usable and it is not safe for concurrent use. Validation
appends into caller-supplied diagnostic storage:

```go
func (v *Validator) Validate(
    dst []Diagnostic,
    doc *ast.Document,
    fields *schema.Schema,
) []Diagnostic
```

Callers use `dst[:0]` to retain diagnostic capacity. A package-level
convenience function may use a fresh validator for cold callers. A warmed
validator with adequately sized diagnostic storage must validate a valid
document with zero allocations.

`Diagnostic` contains a stable bounded code, a bounded table kind and one-based
row, a bounded member discriminator, an exact source span when one is
available, and strong-ID context for affected nodes, clauses, requirements,
fields, values, outcomes, remediations, evidence kinds, and evidence states.
Table/row identifies the owner and member identifies the exact field, including
zero-valued missing references. This keeps diagnostics machine-identifiable
without multiplying the stable code set. It does not store an allocated
message. The fixed enums provide human-readable text through `String()`.

## Validation Phases

### 1. Structural Safety

The first phase checks parallel-column lengths, source ranges, typed payload
references, CSR starts and counts, and every strong-ID bound in a fixed table
order. It never indexes one column based on an unchecked peer column. Invalid
rows are marked so later phases skip unsafe dereferences while continuing to
collect independent diagnostics.

### 2. Semantic Checks

The second phase validates only structurally safe rows. It checks:

- compare operation arity and field/value kind compatibility;
- relational operations restricted to ordered kinds;
- non-empty groups, `In` lists, and requirement clause ranges;
- valid group, negation, requirement, clause, and evidence node references;
- evidence-only entries in clause evidence ranges;
- all seven resolution outcomes;
- symbol-valued catalog and outcome names;
- unique, non-zero requirement IDs;
- exact remediation payload shape and field/value type compatibility;
- valid source spans for all source-addressed records.

### 3. Graph Checks

An iterative tri-color traversal avoids call-stack growth. Requirement
applicability roots seed reachability. Only clauses referenced by requirements
seed assertion and evidence roots. Invalid edges are diagnosed and skipped.
The traversal detects cycles, then emits unreachable nodes in ascending
`NodeID` order. This phase uses one reusable byte of state per node plus a
reusable frame stack.

## Diagnostic Order

Diagnostics are deterministic:

1. structural table and column diagnostics in documented table order;
2. semantic diagnostics in ascending row or strong-ID order;
3. cycle diagnostics in deterministic root and edge order;
4. unreachable-node diagnostics in ascending `NodeID` order.

When a corrupt record has no valid source range, its diagnostic uses a zero
span and retains the relevant strong ID. Downstream compilation stops when any
diagnostic is present.

## Performance

Validation runs once when a policy is loaded, never per request. Structural
checks and iterative graph traversal are linear in document columns, nodes,
and graph edges. Exact-byte catalog-name uniqueness and requirement-ID
uniqueness use deterministic predecessor scans, making the complete bound
`O(linear work + evidenceKinds^2 + evidenceStates^2 + outcomes^2 +
requirements^2)`. Production decoders must set nonzero `MaxCatalogItems` and
`MaxRequirements`; zero disables those limits. No map, recursive call stack,
per-node allocation, or `sync.Pool` is used.

Five one-second runs on Go 1.27.0, Linux/amd64, AMD Ryzen AI MAX+ 395 measured
warm validation at 22.55 ns/node for 16 nodes and 14.54 ns/node for 8,192
nodes, with `0 B/op` and `0 allocs/op` at every benchmarked size. The
8,192-row worst cases measured 147.7 ms for unique catalog names and 7.02 ms
for unique requirement IDs, also with zero warm allocations. The main design
records the full table and ranges. The linear path does not justify pass
fusion; the quadratic scans require bounded admission.

## Tests

Tests decode the canonical full policy and expect no diagnostics. Separate
table cases mutate fresh documents to cover every code, mismatched columns,
invalid CSR ranges, wrong value types, missing resolutions, duplicate IDs,
cycles, and unreachable nodes. Multi-defect tests lock ordering. Corrupt input
tests assert no panic, and reuse tests assert deterministic output and retained
scratch capacity.
