# Architecture

NornRune is a deterministic, evidence-aware policy compiler and evaluator. The
JSON, CLI, TUI, HTTP, gRPC, and PostgreSQL packages are adapters around a typed
core. The core does not branch on the five supplied request IDs and does not
query PostgreSQL while evaluating a policy.

## System Boundary

```text
native policy JSON OR explicit CEL/Rego/Cedar source + bindings
    |
    v
bounded cold frontend -> integer-indexed SoA AST -> validator -> compiler -> immutable Program
                                                               |
request JSON + evidence JSON                                  |
    |                                                          |
    v                                                          v
batch decoder -> typed SoA Batch ------------------------> evaluator
                                                               |
                                                               v
                                                        CSR Result Batch
                                                               |
                         +----------------+--------------------+----------------+
                         |                |                    |                |
                         v                v                    v                v
                       CLI JSON         HTTP/gRPC          semantic TUI    audit journal
```

The AST and compiled program live in [`internal/ast`](../internal/ast) and
[`internal/program`](../internal/program). Evaluation lives in
[`internal/eval`](../internal/eval). Adapters may parse JSON or protobuf and may
allocate on cold setup paths, but maps, reflection, database calls, and
transport objects do not enter evaluator kernels.

CEL, Rego, and Cedar use pinned official parsers on the CLI cold path, then
translate only their documented subsets into the shared semantic tables.
Protobuf descriptors run through `protoc-gen-nornrune` at generation time and
produce static bindings; runtime descriptor reflection is not supported.
Persisted service registry sources remain canonical native JSON. See the
[compatibility frontend guide](frontends.md) for the exact boundary.

## Compile And Publish

Compilation is a cold, bounded operation:

1. The policy decoder writes one caller-owned `ast.Builder`.
2. Validation checks column lengths, IDs, references, graph reachability,
   cycles, operator types, resolution tables, and explanation templates.
3. Lowering normalizes expressions, deduplicates safe common subexpressions,
   assigns truth and reason slots, creates indexes, and freezes a self-contained
   `program.Program`.
4. Service publication persists the canonical source and property-graph
   projection in one PostgreSQL transaction.
5. A copy-on-write registry snapshot and an atomic active pointer publish the
   immutable program to readers.

Compilation occurs before the publisher lock. An evaluation loads one active
program pointer and retains that version for the complete call, so later
publication cannot change an in-flight result. See
[`internal/persistence/policy.go`](../internal/persistence/policy.go) and
[`internal/program/registry.go`](../internal/program/registry.go).

## Service Evaluation

```text
HTTP or gRPC request
       |
       v
bounded admission slot
       |
       v
fixed engine workspace -> decoder + builder
       |
       v
process-wide scheduler -> fixed evaluator workers -> deterministic merge
       |
       v
same engine workspace -> result + encoder + audit slab
       |                                      |
       v
canonical JSON response
                                              bounded journal slot
                                                      |
                                                      v
                                           PostgreSQL transaction
```

Admission covers decoding, evaluation, encoding, and required audit
acknowledgment. `server.Engine` preallocates fixed request workspaces and one
fixed-worker scheduler. A workspace is returned only after result encoding,
metrics observation, and audit submission have finished using its mutable slices.
Network request concurrency therefore cannot create an unbounded number of
decoders or evaluator slabs.

[`internal/scheduler`](../internal/scheduler) partitions batches of 256 rows or
more at 64-row bitmap boundaries, gives each shard private scratch and results,
then merges once in row order. Every service request shares the process-wide
worker-token budget; no request creates a nested scheduler or unbounded worker
pool. Smaller batches use its measured serial path.

## Ownership

Ownership follows lifetime, not object type:

| Lifetime | Owner | Storage | Release point |
|---|---|---|---|
| Policy decode | `ast.Builder` | source bytes and typed builder columns | after compilation |
| Policy version | `program.Program` | immutable instructions, symbols, catalogs, indexes | registry lifetime |
| Service process | `server.Engine` | fixed `engineWorker` slab, channel, and scheduler | process shutdown |
| Evaluation | one `engineWorker` | batch columns, merged result, encoder, and audit state | encoding, metrics, and audit submission complete |
| Row shard | one scheduler worker | private executor and result columns | deterministic merge complete |
| Audit submission | one journal slot | copied immutable audit batch | commit or recorded failure |
| Debug session | one actor goroutine | retained scalar execution and checkpoints | session close |

No response may retain a slice backed by a returned worker. Audit-off HTTP
evaluation can append into the handler-owned request buffer. Audit-enabled
evaluation starts a standalone output buffer because result bytes are also
used to build the audit batch; the journal copies that batch into its own slot
before the engine worker is returned.

## Data Layout

### Source AST

`ast.Document` is an integer-indexed graph represented by parallel typed
columns. One-based `NodeID` values index `NodeKinds` and `NodeRefs`; the ref
selects a row in the kind-specific payload table. Variable-degree relationships
use CSR:

```text
GroupChildStarts[row] + GroupChildCounts[row] -> ChildNodeIDs[start:end]
RequirementClauseStarts[row] + count          -> RequirementClauseIDs[start:end]
ClauseEvidenceStarts[row] + count             -> ClauseEvidenceNodeIDs[start:end]
```

Source and symbol text are byte slabs plus start/length columns, not one Go
string allocation per token.

### Compiled Program

[`program.Program`](../internal/program/program.go) stores opcodes, operands,
fields, values, slots, source maps, result catalogs, and indexes in parallel
numeric slices. Symbols use one byte slab and an open-addressed hash table.
Programs own all backing arrays and borrow no AST or compiler memory.

### Request Batch

[`eval.Batch`](../internal/eval/batch.go) is column-major. Values for one field
across all rows are contiguous. Symbols, integers, and timestamps use typed row
columns; Booleans and presence use `uint64` bitplanes. Request-to-evidence
relationships are `EvidenceOffsets` plus `EvidenceRefs`, and evidence itself is
another struct of arrays.

### Truth And Results

Each instruction produces positive and negative bitplanes. Per row, `(1,0)` is
true, `(0,1)` is false, `(0,0)` is unknown, and `(1,1)` is conflict. Reason
planes retain why a value is unresolved. Whole words compose with Boolean
kernels, which makes the layout suitable for SIMD and 64-row shards.

[`result.Batch`](../internal/result/batch.go) stores fixed-width outcome IDs and
CSR provenance for requirements, drivers, evidence, reasons, and remediation.
Human-readable explanations and JSON strings are materialized only at the
adapter boundary.

## Persistence Boundary

PostgreSQL stores canonical policy versions, request/evidence snapshots, and
immutable decision records. It also stores a derived SQL/PGQ policy graph for
inspection. It is not a policy execution engine: there is no query per row,
node, clause, or evidence item. See the [database guide](database.md) for
transactions, migrations, and recovery.

## Further Reading

- [Policy language](policy-language.md)
- [Compatibility frontends](frontends.md)
- [Concurrency](concurrency.md)
- [API](api.md)
- [Performance](performance.md)
- [Operations](operations.md)
