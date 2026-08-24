# Verifoxx Policy Engine Design

**Status:** Approved, with implementation planning pending

**Date:** 2026-08-20

## Performance Tenets

Performance is an architectural property, not a cleanup phase. The design follows this causal chain:

```text
SoA data + grouped lifetimes + zero per-record allocation
    -> contiguous, uniformly typed arrays
    -> bulk kernels become possible
    -> SIMD execution and natural parallel shards
```

The working rules are:

1. Establish order of magnitude before implementation. Classify work as once per policy, once per batch, once per request, or once per node and row.
2. Target zero allocations on every per-request, per-node, and per-record path. Size storage, reuse caller-owned buffers, and inspect escape analysis.
3. Design data before control flow. Use SoA for column scans, CSR for variable relationships, integer slab references instead of pointers, and lifetime-sized arenas.
4. Execute in bulk. Whole columns and bitplanes go through verified SIMD kernels rather than per-element calls.
5. Avoid work before accelerating it. Prune policies before decoding or evaluating irrelevant facts, hoist invariants, and avoid duplicate scans.
6. Parallelize only after work per shard exceeds coordination cost. Shard on row and bitset boundaries, use private scratch and output, and merge once.
7. Treat `sync.Pool` as a last resort. Prefer fixed worker ownership, explicit arenas, capacity hints, and caller-provided storage.
8. Measure the resulting machine behavior. Use `-benchmem`, escape analysis, runtime dispatch checks, disassembly, instructions, cycles, and interleaved benchmark comparisons.
9. Treat correctness as mandatory and measured performance as a coequal acceptance constraint after correctness. Retain local hot-path specialization when a shared abstraction causes a statistically significant regression, and compare linked consumers because code layout can amplify a helper's machine-code change.

## Abstract

Verifoxx will be a general, evidence-aware policy compiler and decision engine written in Go 1.27. The three requirements and five requests in the candidate exercise will form its first policy pack and conformance data set. They will not be encoded as request-specific branches.

The system will translate a bounded policy document into a pointerless semantic abstract syntax tree, validate and normalize that tree, and compile it into an immutable struct-of-arrays execution program. Requests and evidence will be evaluated in batches. The hot path will use contiguous columns, bitmaps, indexes, and `github.com/sebishogun/simd` v1.21.0. Large batches will be divided among workers at existing bitset boundaries. Each worker will own its scratch memory and output range.

The execution model will preserve four semantic states: true, false, unknown, and conflict. Outcome names will belong to a policy pack rather than the engine. The Verifoxx pack will define `Approve`, `Reject`, `Revise`, and `Escalate`.

PostgreSQL 19 will store canonical policy versions, evidence snapshots, and immutable decision records. Its SQL/PGQ support will expose a derived property-graph view of normalized policy nodes and edges. PostgreSQL will not participate in expression evaluation. The compiled program will remain in process memory.

The product will include a scriptable CLI, a Bubble Tea semantic debugger, HTTP and gRPC adapters, Delve DAP integration, Docker and Compose configurations, a Makefile, and a `devx` command for setup and development workflows. The default exercise demonstration will run without external services. Full service mode will start PostgreSQL 19 and the network adapters.

## 1. Problem Statement

The candidate exercise asks for a program that converts natural-language requirements into an intermediate semantic representation and evaluates requests against that representation and an evidence pack. A result must be one of four decisions and must explain the requirement, evidence condition, or uncertainty responsible for that decision.

The difficult part is not parsing five rows. It is preserving enough meaning to distinguish these cases:

- A known violation of a non-negotiable condition
- A bounded modification that would make a request acceptable
- Missing or unverifiable information
- Conflicting evidence
- A request that satisfies every applicable condition

A flat record of extracted fields cannot express applicability, obligations, evidence quality, uncertainty, remediation, or outcome precedence. The representation must therefore behave like a small policy language and compiler.

## 2. Scope

The implementation will include the complete production-oriented system described here. The assignment artifacts remain a first-class release boundary:

- Runnable source
- A README with setup, commands, dependencies, and formats
- A design note no longer than one page
- Machine-readable results for requests R1 through R5
- A brief statement of AI tool use
- Tests centered on decision boundaries and uncertainty

This architecture document is not the one-page submission note. The submission note will summarize the semantic model, decision logic, escalation boundaries, and next improvements without reproducing this document.

## 3. Design Criteria

The following criteria determine whether the design is acceptable.

### 3.1 Semantic criteria

- Requirement IDs and request IDs occupy distinct types and namespaces.
- Policies are data, not branches on supplied request IDs.
- Applicability is separate from satisfaction.
- Known falsehood is separate from missing knowledge.
- Conflicting evidence is representable without discarding either side.
- Remediation is structured and bounded.
- Outcomes and precedence are policy-defined.
- Every result can identify its driving policy nodes and evidence.

### 3.2 Performance criteria

- The evaluator compiles a policy once and applies it many times.
- Request and evidence data are columnar in the hot path.
- Evaluation performs no per-request or per-node allocation after capacities are established.
- SIMD calls operate on whole columns or mask regions.
- Policies are pruned before their expressions are evaluated.
- Worker boundaries follow existing row and bitset boundaries.
- Workers have private scratch and disjoint output.
- Scalar, SIMD, parallel, and debug execution produce identical semantic results.
- Performance claims require benchmark output and verified runtime dispatch.
- Refactors that touch hot code require interleaved linked-binary comparison; source deduplication does not justify a statistically significant regression.

### 3.3 Operational criteria

- `devx demo` works without PostgreSQL or network services.
- Full service mode starts with Docker Compose.
- Published policy versions and decision records are reproducible.
- The server supports health checks, metrics, cancellation, limits, and graceful shutdown.
- A fresh machine can inspect and install prerequisites through `devx doctor` and `devx install`.
- Build, test, generation, migration, benchmark, and debugging workflows have one discoverable command surface.

## 4. Principal Decisions

| Area | Decision | Reason |
|---|---|---|
| Language | Go 1.27 | It matches the developer's strongest language and provides the required systems tooling. |
| SIMD | `github.com/sebishogun/simd` v1.21.0 whole-slice API | The library provides measured kernels, runtime dispatch, portable fallbacks, masks, and columnar primitives without an experiment flag. |
| Policy representation | Bounded semantic AST | A custom AST can preserve evidence state, remediation, and uncertainty without outsourcing the main assignment. |
| AST layout | Pointerless typed slabs with SoA payloads and integer references | This follows the useful part of WunderGraph's AST architecture while keeping hot columns contiguous. |
| Execution representation | Immutable, indexed SoA program | Compilation can remove names and pointers from the hot path and arrange operations for bulk execution. |
| Truth model | Positive and negative bitplanes plus reason masks | Two bitplanes represent true, false, unknown, and conflict and compose through bit operations. |
| Parallel axis | Request rows | Rows are independent under one immutable program and already align with mask words. |
| Persistence | PostgreSQL 19 | It provides ACID audit storage, relational integrity, JSON and binary payloads, and SQL/PGQ graph views in one system. |
| Graph storage | Derived AST node and edge projection | The source policy remains canonical. The graph exists for inspection and historical queries, not execution. |
| Interactive UI | Bubble Tea semantic debugger | The TUI can expose policy structure, compiled instructions, evidence, masks, and outcomes in a terminal. |
| Go debugger | Delve DAP | Neovim can own the standard DAP connection while the TUI uses a separate semantic channel. |
| Developer tooling | Cobra plus Charmbracelet `huh` in `cmd/devx` | This provides testable command discovery and built-in fuzzy selection without an external `fzf` dependency. |
| Hot helper reuse | Share only after linked-consumer A/B parity | Go inlining and linker placement can change downstream operation-cache and branch-predictor behavior even when semantic instructions and allocations are unchanged. |
| Make | Thin wrapper over `devx` | Workflow logic remains testable Go code rather than duplicated shell recipes. |
| Containers | Release Dockerfile, debug Dockerfile, and Compose | These provide reproducible standalone and full-service operation. |

## 5. System Context

```text
Policy source or future NLP output
                |
                v
        Frontend adapter
                |
                v
        DocumentBuilder
                |
                v
       Pointerless source AST
                |
                v
 Validator -> Normalizer -> Compiler
                |
                v
      Immutable Program
                |
       +--------+--------+
       |                 |
       v                 v
 BatchExecutor      DebugExecutor
       |                 |
       v                 v
 ResultBatch        DebugState stream
       |                 |
       +--------+--------+
                |
 CLI / TUI / HTTP / gRPC adapters
                |
                v
       PostgreSQL journal
```

The compiler and evaluator form the core. JSON, NLP, terminals, network protocols, and PostgreSQL are adapters around it.

## 6. Policy Semantics

### 6.1 Policy pack

A policy pack contains:

- A schema of known fields and value types
- An outcome catalogue and precedence table
- Requirements and original source text
- Applicability expressions
- Clauses and assertions
- Evidence requirements
- State-specific resolution rules
- Structured remediations
- Source spans
- A semantic version and content hash

### 6.2 Requirement and clause model

A requirement has one applicability expression and one or more clauses. A clause contains an assertion, optional evidence requirements, a resolution table, and optional remediation.

```text
Requirement
  ID
  Source text
  Applies expression
  Clauses

Clause
  Assertion expression
  Evidence requirements
  Resolution on false
  Resolution on missing
  Resolution on stale
  Resolution on unclear
  Resolution on unverifiable
  Resolution on conflict
  Remediation
```

The separate unresolved states are necessary. For example, absence of a usage-adjustment approval can support a bounded revision to standard usage, while stale or conflicting approval evidence requires escalation.

### 6.3 Expression set

The initial language is deliberately bounded:

- `All`
- `Any`
- `Not`
- `Equal`
- `NotEqual`
- `In`
- `Exists`
- `Less`
- `LessEqual`
- `Greater`
- `GreaterEqual`
- `EvidenceMatches`

The language has no loops, reflection, dynamic function calls, arbitrary Go expressions, or user-provided executable code.

### 6.4 Applicability

Applicability is not a Boolean assertion result. An inactive requirement is `NotApplicable`; it is not satisfied or violated. Missing information needed to establish applicability remains unresolved unless a conservative index can prove non-applicability.

### 6.5 Generic outcomes

The engine stores outcome IDs. A policy pack supplies labels and precedence.

```go
type OutcomeCatalog struct {
    Names      []SymbolID
    Precedence []uint8
    Terminal   []bool
}

type ResolutionTable struct {
    OnSatisfied    []OutcomeID
    OnFalse        []OutcomeID
    OnMissing      []OutcomeID
    OnStale        []OutcomeID
    OnUnclear      []OutcomeID
    OnUnverifiable []OutcomeID
    OnConflict     []OutcomeID
    Remediations   []RemediationID
}
```

The Verifoxx pack defines `Approve`, `Reject`, `Revise`, and `Escalate` exactly as required by the assignment.

## 7. Verifoxx Policy Pack

The first policy pack models the supplied requirements without checking request IDs.

| Request | Expected result | Main driver |
|---|---|---|
| Request R1 | `Approve` | Aggregate output, current approval, and verified local environment |
| Request R2 | `Reject` | Individual-level disclosure violates a non-negotiable condition |
| Request R3 | `Revise` | Lower usage to standard or add the permitted current usage adjustment |
| Request R4 | `Escalate` | The execution environment cannot be verified |
| Request R5 | `Escalate` | Pre-execution approval evidence is conflicting |

Requirement IDs and request IDs will use separate Go types. Their string values may overlap without creating type ambiguity.

## 8. Source AST

### 8.1 Layout

The source AST will use one owning document, integer node references, retained source bytes, and typed payload tables. Payload fields scanned independently will use SoA.

```go
type Document struct {
    Metadata PolicyMetadata

    NodeKinds []NodeKind
    NodeRefs  []uint32

    CompareFields []FieldID
    CompareOps    []CompareOp
    CompareValues []ValueID

    CompareListStarts []uint32
    CompareListCounts []uint16
    ListValueIDs      []ValueID

    GroupChildStarts []uint32
    GroupChildCounts []uint16
    ChildNodeIDs     []NodeID

    EvidenceKinds  []EvidenceKindID
    EvidenceStates []EvidenceStateID

    SourceStarts []uint32
    SourceEnds   []uint32

    InputBytes    []byte
    SymbolBytes   []byte
    SymbolStarts  []uint32
    SymbolLengths []uint32

    ValueKinds []ValueKind
    ValueRefs  []uint32

    EvidenceKindNames  []ValueID
    EvidenceStateNames []ValueID

    RequirementIDs                []RequirementID
    RequirementApplicabilityRoots []NodeID
    RequirementClauseStarts       []uint32
    RequirementClauseCounts       []uint16
    RequirementClauseIDs          []ClauseID

    ClauseAssertionRoots     []NodeID
    ClauseEvidenceStarts     []uint32
    ClauseEvidenceCounts     []uint16
    ClauseEvidenceNodeIDs    []NodeID
    ClauseRemediationStarts  []uint32
    ClauseRemediationCounts  []uint16
    ClauseRemediationIDs     []RemediationID

    ClauseOnSatisfied    []OutcomeID
    ClauseOnFalse        []OutcomeID
    ClauseOnMissing      []OutcomeID
    ClauseOnStale        []OutcomeID
    ClauseOnUnclear      []OutcomeID
    ClauseOnUnverifiable []OutcomeID
    ClauseOnConflict     []OutcomeID
}
```

`NodeID` indexes `NodeKinds` and `NodeRefs`. `NodeRefs[node]` selects a row from the payload table corresponding to `NodeKinds[node]`.

Variable-degree relationships use compressed sparse rows:

```text
GroupChildStarts[node] -> first edge
GroupChildCounts[node] -> edge count
ChildNodeIDs[start:end] -> child IDs
```

### 8.2 Strings and symbols

Source spans remain ranges in one retained input byte arena. Decoded symbol literals use ranges in a second document-owned byte slab so JSON escapes are resolved once during parsing without allocating one Go string per token. `ValueID` indexes parallel kind/ref columns; refs select symbol, integer, Boolean, or timestamp payload tables. `In` operands use one CSR value-ID edge column. Compilation interns recurring symbol bytes into integer IDs. The interner will use a capacity-sized open-addressed table over byte references. It will not convert every source token into a separately allocated Go string.

### 8.3 Requirements, clauses, and remediation

Requirements and clauses are typed tables, not expression node variants. A requirement stores its applicability root and a CSR range of `ClauseID`s. A clause stores its assertion root, CSR evidence-node and remediation ranges, and one outcome ID for satisfied, false, and each unresolved state. Outcome, evidence-kind, and evidence-state catalogues map IDs to symbol values. Fixed metadata stores the policy name, semantic version, and SHA-256 of retained source. Source remediation is deliberately bounded to two actions: setting a field to a typed value or requesting an allowed evidence kind. This covers reduced scope/usage and additional evidence without introducing arbitrary executable actions or maps.

### 8.4 Builder lifetime

The parser writes into chunked typed builder slabs. Builder objects share one compilation lifetime. The final lowering pass computes exact sizes and freezes the program into immutable slabs. The mutable builder can then be discarded as one group.

## 9. Compiler Pipeline

The compiler performs these stages:

1. Decode the policy document into builder slabs.
2. Validate identifiers, references, value types, and operator arity.
3. Reject cycles, duplicate IDs, empty groups, and invalid source ranges.
4. Normalize nested Boolean expressions.
5. Intern fields, values, outcomes, evidence kinds, reasons, and remediations.
6. Deduplicate safe common subexpressions into a DAG.
7. Build conservative applicability indexes.
8. Topologically schedule expression nodes.
9. Group compatible instructions by opcode and operand column.
10. Assign reusable truth-plane slots through liveness analysis.
11. Emit mappings among source spans, node IDs, and instruction ranges.
12. Freeze the program into immutable slabs.

Normalization, lowering, and final compaction will be fused where doing so avoids a redundant pass without obscuring correctness.

### 9.1 Validation measurements

Validation is a policy-load operation, not a request-evaluation operation. On
2026-08-21, Go 1.27.0 on Linux/amd64 with an AMD Ryzen AI MAX+ 395 measured the
warm, valid-document path as follows. Values are medians of five one-second
runs; the range is across those runs.

| Nodes | Median ns/op | Median ns/node | ns/node range | B/op | allocs/op |
|---:|---:|---:|---:|---:|---:|
| 16 | 360.7 | 22.55 | 22.10-23.35 | 0 | 0 |
| 128 | 1,996 | 15.59 | 15.31-15.77 | 0 | 0 |
| 1,024 | 14,993 | 14.64 | 14.35-14.82 | 0 | 0 |
| 8,192 | 119,087 | 14.54 | 14.24-14.62 | 0 | 0 |

A fresh validator used four allocations for reusable state: 48 B at 16 nodes
and 8,224 B at 8,192 nodes. Priming the validator and caller-owned diagnostic
buffer removes those allocations.

Structural checks and iterative graph traversal are linear in columns, nodes,
and edges. Exact-byte catalog-name uniqueness and requirement-ID uniqueness use
deterministic predecessor scans, so total validation complexity is:

```text
O(columns + nodes + edges + evidenceKinds^2 + evidenceStates^2
  + outcomes^2 + requirements^2)
```

Worst-case unique-row fixtures measured the quadratic scans separately:

| Rows | Catalog names | Requirement IDs | B/op | allocs/op |
|---:|---:|---:|---:|---:|
| 16 | 0.762 us | 0.224 us | 0 | 0 |
| 128 | 39.3 us | 3.20 us | 0 | 0 |
| 1,024 | 2.37 ms | 0.129 ms | 0 | 0 |
| 8,192 | 147.7 ms | 7.02 ms | 0 | 0 |

The 8,192-row catalog result is the median of five three-second runs
(146.7-148.8 ms); the requirement result is the median of five one-second runs
(6.99-7.18 ms). Production decoders must set nonzero `MaxCatalogItems` and
`MaxRequirements`; zero disables those limits and leaves programmatically
constructed ASTs unbounded. The measured linear path does not justify pass
fusion, while the quadratic scans make admission limits mandatory.

## 10. Compiled Program

The program contains:

- Instruction opcode columns
- Operand columns
- Child ranges
- Interned constants
- Field-to-column mappings
- Applicability indexes
- Outcome resolution tables
- Source maps
- Scratch-slot assignments
- Requirement and policy roots

The program is immutable after publication. Evaluations load one program pointer at their start. A later policy publication cannot change the program observed by an active evaluation.

## 11. Batch Data Layout

### 11.1 Request facts

The logical field groups are subject, action, resource, output, and context. The physical layout is column-major.

```go
type Batch struct {
    Rows uint32

    SymbolValues  []SymbolID
    IntegerValues []int64
    BooleanValues []uint64
    PresenceMasks []uint64

    RequestIDs []RequestID

    EvidenceOffsets []uint32
    EvidenceRefs    []uint32
    Evidence        EvidenceBatch
}
```

The compiled schema maps each field ID to a type and column offset. Values for one field across all rows are contiguous.

### 11.2 Evidence

```go
type EvidenceBatch struct {
    IDs        []EvidenceID
    Kinds      []EvidenceKindID
    Statuses   []EvidenceStatusID
    Subjects   []SymbolID
    Scopes     []SymbolID
    Reviewers  []SymbolID
    Timings    []SymbolID
    Timestamps []int64
}
```

Request-to-evidence relationships use CSR through `EvidenceOffsets` and `EvidenceRefs`. Records for one request are packed contiguously when the decoder controls ordering.

### 11.3 No dynamic maps

Adapters may use temporary lookup structures while compiling or decoding a batch. No `map[string]any` or reflective value enters the evaluator.

## 12. Truth and Uncertainty

### 12.1 Four values

The evaluator represents truth through positive and negative evidence bitplanes.

| Positive | Negative | Meaning |
|---:|---:|---|
| 1 | 0 | True |
| 0 | 1 | False |
| 0 | 0 | Unknown |
| 1 | 1 | Conflict |

```go
type TruthPlanes struct {
    Positive []uint64
    Negative []uint64
}
```

### 12.2 Boolean composition

```text
NOT:
positive = child.negative
negative = child.positive

AND:
positive = left.positive AND right.positive
negative = left.negative OR right.negative

OR:
positive = left.positive OR right.positive
negative = left.negative AND right.negative
```

These equations operate over complete mask words and therefore admit whole-slice SIMD kernels.

### 12.3 Reason masks

Truth does not identify why information is unresolved. Evidence leaves therefore produce sideband masks for:

- Missing
- Stale
- Unclear
- Unverifiable
- Wrong scope
- Wrong subject
- Wrong timing
- Invalid
- Conflict

The compiler allocates reason slots only where resolution or explanation needs them.

## 13. Indexing and Pruning

The system will maintain these indexes:

| Index | Purpose |
|---|---|
| `FieldID -> column` | Direct fact access |
| `(action, resource, trust) -> policy mask` | Candidate-policy pruning |
| `EvidenceKind -> evidence range` | Skip irrelevant evidence |
| `RequestID -> row` | Result and debugger lookup |
| `NodeID -> instruction range` | AST-to-program correlation |
| `InstructionID -> source span` | Diagnostics and debugging |
| `OutcomeID -> precedence` | Generic outcome reduction |
| `Policy hash -> Program` | Registry lookup |

Frequently reused categorical fields may receive per-batch bitmap indexes. The compiler knows how many predicates query each field. The runtime will construct an index only when measured reuse justifies its cost.

Indexes must be conservative. A missing field cannot be treated as a definite mismatch when the correct semantic result is unknown.

## 14. SIMD Execution

### 14.1 Toolchain

```text
Go 1.27.0
github.com/sebishogun/simd v1.21.0
normal runtime-dispatched slice API
```

### 14.2 Responsibility

The SIMD library owns operation thresholds and runtime CPU dispatch. The evaluator passes complete columns or mask regions to its public whole-slice operations. It does not call a SIMD function once per row.

Expected SIMD work includes:

- Symbol equality and set membership
- Integer comparisons
- Presence and validity masks
- Evidence-state masks
- Truth-plane operations
- Candidate-mask intersection
- Outcome-mask reduction
- Bitmap population and compression

### 14.3 Custom intrinsics

A direct Go 1.27 intrinsic implementation is allowed only when all of these conditions hold:

- The v1.21 library has no matching operation.
- The proposed implementation fuses work that otherwise requires multiple passes.
- A scalar reference exists.
- Differential tests pass.
- Benchmarks and disassembly show a real gain.
- Runtime dispatch reaches the intended implementation.

### 14.4 Verification matrix

The project will verify:

- Normal optimized build
- `purego` build
- 386 scalar-fallback build
- Pinned SIMD tiers through the library's controls
- Scalar and accelerated equivalence
- Lengths around every vector and dispatch boundary

The v1.21.0 experiment-gated vector-type API uses Go 1.26 `archsimd` names and
does not compile with Go 1.27. Verifoxx does not use that API; add its build lane
only after the pinned dependency ships a compatible release.

## 15. Evaluation Algorithm

The fast executor performs these steps:

1. Reset caller-owned output and scratch lengths.
2. Load one immutable program snapshot.
3. Select candidate policies through applicability indexes.
4. Construct active row masks.
5. Evaluate grouped leaf predicates over fact columns.
6. Evaluate evidence predicates over evidence columns.
7. Reduce evidence states to request rows through CSR ranges.
8. Compose expression results through truth bitplanes.
9. Apply reason-specific resolution rules.
10. Reduce outcomes according to the policy pack's precedence.
11. Write outcome and driver IDs.
12. Materialize explanations only when requested.
13. Encode into the caller's output buffer.
14. Submit a persistence batch when journaling is enabled.

Fast evaluation invokes no per-node callback and acquires no lock.

## 16. Concurrency Model

### 16.1 Parallel axis

Rows are independent under one immutable policy program. Large batches are split into contiguous row ranges aligned to 64-row bitset words.

```text
rows 0..4095       -> worker 0
rows 4096..8191    -> worker 1
rows 8192..12287   -> worker 2
rows 12288..end    -> worker 3
```

Each worker owns:

- Active masks
- Truth-plane scratch
- Reason-plane scratch
- Sparse diagnostics
- A disjoint output range
- SIMD execution state

Workers never append to a shared slice. Diagnostics are produced in private buffers and concatenated once in shard order.

### 16.2 Scheduler

The service starts a fixed number of evaluator workers. Each worker is a long-lived goroutine with a private scratch arena. A bounded queue transfers shard ownership and provides backpressure.

The scheduler chooses among these modes:

- Direct serial execution for small batches
- One worker for medium batches
- Multiple row shards for large batches
- Service-level concurrency for many small independent batches

A global work budget prevents concurrent network requests from each consuming all available workers. SIMD kernels remain single-threaded within a shard, which prevents nested parallelism.

### 16.3 Other goroutines

Goroutines are appropriate for:

- Evaluator workers
- HTTP and gRPC request handling, subject to admission limits
- Persistence writers
- PostgreSQL policy notifications
- Independent semantic debugger sessions
- DAP protocol handling
- Metrics and graceful-shutdown coordination

Goroutines are not initially appropriate for:

- Individual request rows
- Individual AST nodes
- Policy compilation stages
- Single-request evaluation
- One semantic debugger execution

Compilation may later parallelize independent policy packs if measurement shows that compile latency matters.

### 16.4 Synchronization choices

| Component | Mechanism | Rationale |
|---|---|---|
| Active default policy | `atomic.Pointer[Program]` | Hot reads require no lock. |
| Policy registry | Atomic pointer to immutable snapshot | Lookup is read-heavy and publication is rare. |
| Registry publication | `sync.Mutex` | It prevents lost copy-on-write updates. |
| Duplicate compilation | `singleflight` or keyed mutex | It avoids compiling the same content hash twice. |
| Evaluator shard | None | State and output are private. |
| Batch completion | `sync.WaitGroup` or atomic counter | One completion event occurs per shard. |
| Worker admission | Bounded channel | It transfers ownership and applies backpressure. |
| Service arenas | Bounded ownership channel | Lifetime and capacity are explicit. |
| Metrics | Atomic counters, batch-level updates | Per-node metrics would distort the kernel. |
| Persistence | Bounded channel and dedicated writers | Database ownership stays outside evaluation. |
| Debug session | Actor goroutine and command channel | One owner controls mutable step state. |
| DAP output | Single writer goroutine | DAP framing cannot tolerate interleaved writes. |
| TUI model | Bubble Tea event loop | Model updates are serialized by design. |
| Database connections | `pgxpool` | The driver owns connection synchronization. |
| Migrations | PostgreSQL advisory lock | Only one process may migrate a shared database. |

No `RWMutex` lies on the evaluation path.

### 16.5 False sharing

Workers write disjoint row ranges and own separate scratch objects. Shared completion state is touched once per shard. Metrics update once per batch. Cache-line padding will be introduced only when measurements identify a shared hot word.

### 16.6 Cancellation and shutdown

Cancellation is checked before admission, between major execution stages, at large block boundaries, before persistence, and while waiting for a required audit acknowledgment. It is not checked for every row.

Graceful shutdown follows this order:

1. Stop accepting new network work.
2. Cancel queued requests.
3. Let admitted evaluations finish or reach their deadline.
4. Flush required persistence batches.
5. Stop notification and debugger sessions.
6. Close PostgreSQL connections.
7. Join worker goroutines.

## 17. Memory and Ownership

### 17.1 Lifetime groups

| Lifetime | Storage |
|---|---|
| Source parse | Input bytes and chunked builder slabs |
| Policy version | Immutable compiled slabs |
| Service worker | Decoder, evaluator, result, and output arenas |
| Batch | Row columns and evidence CSR |
| Debug session | Retained step state and bounded checkpoints |
| Persistence batch | Immutable audit rows until commit or failure |

### 17.2 Arena reuse

Each service context owns its complete set of buffers:

- Input bytes
- Fact columns
- Evidence columns
- Active masks
- Truth and reason scratch
- Result columns
- Diagnostic ranges
- Encoded output

A bounded ownership channel lends one context to one request and receives it back only after the response is complete. No output slice may outlive the context that owns it.

Before reuse, lengths and active ranges are reset and tail bits are cleared. A poisoning differential test will fill reused buffers with impossible values and compare the result with a fresh reference execution.

`sync.Pool` is excluded unless a measured production workload demonstrates that fixed worker ownership is insufficient.

## 18. Result Model

```go
type ResultBatch struct {
    OutcomeIDs []OutcomeID

    DriverOffsets []uint32
    DriverNodeIDs []NodeID

    EvidenceOffsets []uint32
    EvidenceIDs     []EvidenceID

    ReasonOffsets []uint32
    ReasonIDs     []ReasonID
}
```

The core stores IDs and ranges. Adapters materialize strings and JSON.

Every machine-readable result includes:

- Request ID
- Decision
- Bounded rationale
- Applied requirements
- Evidence used
- Missing or conflicting evidence
- Assumptions
- Unresolved uncertainty
- Structured remediation
- Policy version and hash
- Engine version

## 19. Persistence Decision

### 19.1 Database selection

PostgreSQL 19 is the sole database. It combines the properties this system needs:

- ACID transactions for policy publication and audit records
- Foreign keys and constraints for reproducibility
- Mature operational tooling and Go support
- JSON and binary payload storage
- Batch ingestion
- SQL/PGQ property graph views

Native graph systems such as Neo4j and Memgraph improve graph traversal but do not improve the canonical audit workload. ArangoDB and SurrealDB add multi-model behavior without a stronger integrity model for this use case. SQLite is a credible embedded alternative but does not match the concurrent service and SQL/PGQ requirements. A second event store or graph database would create polyglot persistence around derived data.

PostgreSQL 19 Beta 3 will be pinned during development. The image tag will move to the PostgreSQL 19 GA release when available. No PostgreSQL 18 compatibility layer is planned.

### 19.2 Boundary

PostgreSQL is not an execution engine for policies.

```text
PostgreSQL policy source
          |
          v
compile once into Program
          |
          v
evaluate entirely in memory
          |
          v
batch append audit records
```

No query occurs per node, clause, evidence record, or request row.

### 19.3 Canonical data

Canonical storage includes:

- Policy identity and versions
- Original requirement text
- Source policy document
- Content hash and compiler version
- Request metadata snapshots
- Evidence snapshots and provenance
- Approval and attestation versions
- Decision records
- Applied findings
- Used evidence references
- Assumptions and uncertainty
- Engine version and execution metadata

Published policy versions and decision records are immutable. Corrections create new records or versions.

### 19.4 Derived data

Derived storage includes:

- Normalized AST nodes and edges
- Materialized aggregates
- Replay outputs
- Optional compiled artifact cache
- Optional debug traces
- Optional benchmark history

Derived data may be deleted and rebuilt from canonical policy versions and decision inputs.

### 19.5 Schema

The initial schema contains:

```text
policies
policy_versions
policy_nodes
policy_edges

requests
evidence_snapshots

evaluation_runs
evaluation_findings
evaluation_evidence

debug_traces
benchmark_runs
```

Every evaluation pins its policy version, request snapshot, evidence snapshots, and engine version. A decision string without these references is not an adequate audit record.

### 19.6 Property graph projection

PostgreSQL 19 exposes `policy_nodes` and `policy_edges` as a read-only property graph. Vertex labels include policy version, requirement, clause, expression, evidence requirement, outcome, and remediation. Edge labels include `CONTAINS`, `CHILD`, `APPLIES_WHEN`, `REQUIRES`, `RESOLVES_TO`, and `REMEDIATES_WITH`.

SQL/PGQ queries support historical questions such as:

- Which requirements can reach a rejection outcome?
- Which clauses depend on environment evidence?
- What path connects a source requirement to an outcome?
- Which expressions are shared?
- Which nodes change between policy versions?

The TUI uses the live in-memory graph for stepping. SQL/PGQ is for persistence inspection and historical analysis.

### 19.7 Audit modes

| Mode | Behavior |
|---|---|
| `off` | No persistence |
| `best-effort` | Return the result before journal acknowledgment |
| `required` | Return success only after the audit transaction commits |

CLI demonstration defaults to `off`. HTTP and gRPC service mode defaults to `required`.

Persistence writers own complete batches and use PostgreSQL batch or copy protocols. A bounded queue provides backpressure. Required mode waits for an acknowledgment. Best-effort mode exposes failures and drops through metrics.

## 20. Security and Data Retention

- The system does not store protected dataset rows.
- It stores request metadata and evidence needed to reproduce a decision.
- Snapshot hashes detect mutation.
- PostgreSQL connections use TLS outside local development.
- Migration and runtime database roles are separate.
- Runtime roles cannot update or delete published audit records.
- Debug traces and sensitive evidence have explicit retention periods.
- API limits bound policy size, graph size, batch rows, evidence count, and output size.
- Logs do not include complete evidence payloads by default.

## 21. Policy Publication

Publication follows this sequence:

1. Decode and validate a candidate policy.
2. Compile it outside the registry lock.
3. Persist canonical source and derived graph in one transaction.
4. Acquire the registry publication mutex.
5. Construct a new immutable registry snapshot.
6. Atomically publish the snapshot.
7. Release the mutex.
8. Notify other instances through PostgreSQL `LISTEN/NOTIFY`.

Active evaluations retain their old program pointer. New evaluations observe the new version.

## 22. Debug Execution

### 22.1 Two executors

```text
BatchExecutor
  SIMD
  optional parallel shards
  scratch-slot reuse
  no callbacks

DebugExecutor
  deterministic scalar steps
  retained state
  semantic breakpoints
  replay
```

Both execute the same compiled instruction schedule. Differential tests require identical final truth, reason, outcome, and driver data.

### 22.2 Semantic controls

The semantic debugger supports:

- Step one instruction
- Step one logical node
- Step over a subtree
- Continue and pause
- Restart
- Replay to an instruction
- Break on node
- Break on truth state
- Break on evidence state
- Break on outcome
- Watch fields, masks, evidence, and outcomes

Reverse stepping replays from bounded checkpoints instead of copying all state after every instruction.

### 22.3 Debug state

Debug state includes:

- Instruction ID
- Node ID
- Source span
- Active mask
- Positive and negative masks
- Reason masks
- Outcome and remediation IDs
- Worker and shard
- Physical slab and slot offsets
- SIMD operation identity where relevant

## 23. TUI

The Bubble Tea TUI runs in a terminal separate from Neovim.

```text
+ Requests ------+ AST or Program Graph --------+ Runtime State -------+
| R1 Approve     | R2 protected data             | Instruction: 42      |
| R2 Reject      | +- protected == true          | Node: evidence 7     |
| R3 Revise      | +- environment == local       | Truth: Unknown       |
| R4 Escalate    | `- attestation valid          | Reason: Missing      |
| R5 Escalate    |                               | Worker: 0            |
+----------------+-------------------------------+----------------------+
| step  over  continue  break  watch  replay  source  ast  program      |
+-----------------------------------------------------------------------+
```

The TUI can display:

- Original requirement text
- Source AST
- Normalized DAG
- Compiled instruction order
- Selected request trace
- Evidence and provenance
- Outcome and remediation
- Physical layout offsets
- Worker and shard
- Historical policies and decisions from PostgreSQL

Shared DAG nodes may be displayed once with references or expanded as a logical tree.

## 24. Delve and DAP

```text
Neovim nvim-dap
       |
       | DAP
       v
Delve DAP server
       |
       v
Engine debug worker
       ^
       |
Unix semantic debug socket
       |
Bubble Tea TUI
```

Neovim owns the DAP connection. The TUI uses a separate Unix-domain semantic channel and therefore does not contend for the DAP client role.

A debug-only, non-inlined hook provides a stable breakpoint:

```go
debugtrap.Reached(nodeID, instructionID)
```

The source map correlates policy text, node ID, instruction ID, and Go debug location. Debug binaries use `-gcflags=all=-N -l` and are never used for performance measurements. A SIMD kernel appears as one semantic instruction; Delve and disassembly are used when inspection must enter that kernel.

## 25. Product Interfaces

### 25.1 CLI

The product binary supports:

```text
verifoxx evaluate
verifoxx validate
verifoxx compile
verifoxx explain <request-id>
verifoxx simulate <request-id> --set field=value
verifoxx tui
verifoxx debug-worker
verifoxx serve
verifoxx bench
```

Default policy and candidate data are embedded so `verifoxx evaluate` requires no arguments.

### 25.2 HTTP

```text
POST /v1/policies/validate
POST /v1/policies/compile
POST /v1/evaluate
GET  /v1/policies/{hash}
GET  /healthz  (readiness compatibility alias)
GET  /readyz
GET  /livez
GET  /metrics
```

The HTTP adapter accepts batches. It applies admission limits before decoding large bodies. Readiness requires open admission and healthy runtime dependencies; liveness reports only whether the process can serve its probe. Metrics use Prometheus text exposition rather than the JSON response contract.

### 25.3 gRPC

```text
ValidatePolicy
CompilePolicy
EvaluateBatch
EvaluateStream
```

Unary batch evaluation is implemented before streaming. Streaming is retained only if load tests demonstrate a practical benefit. Protobuf types do not enter the core.

## 26. Developer Experience

### 26.1 Decision

The project will provide a Go-based `devx` command. It will not require `fzf`. Cobra provides command discovery and completion. Charmbracelet `huh` provides an interactive selector with fuzzy filtering.

The command lives at:

```text
cmd/devx
```

Running `devx` without arguments opens the menu. Running `devx <command>` is scriptable and suitable for CI.

### 26.2 Command groups

```text
Setup:
  install
  uninstall
  doctor
  status
  completion

Build:
  build
  build:exp
  build:purego
  clean

Run:
  demo
  tui
  serve
  full

Database:
  db:up
  db:down
  db:reset
  db:status
  migrate
  migrate:create
  migrate:check
  graph:check

Generation:
  proto:gen
  proto:check
  policy:compile
  policy:check
  results:gen
  results:check

Testing:
  test
  test:unit
  test:integration
  test:e2e
  test:race
  fuzz

Performance:
  bench
  bench:compare
  profile
  perf
  load

Debugging:
  debug
  debug:dap
  debug:tui

Containers:
  docker:build
  docker:run
  docker:full
```

### 26.3 Installation

The repository will include:

```text
cli/devx
cli/install.sh
cli/build.sh
```

The repository wrapper selects a host binary when prebuilt binaries are available. The installer copies a repository-neutral dispatcher into `~/.local/bin`; that command runs the nearest ancestor repository with an executable `cli/devx` and fails outside a devx-enabled tree. Installation verifies `PATH` and never edits shell startup files.

`devx install` detects available package managers, checks versions, shows exact commands, marks commands requiring elevated privileges, asks for confirmation, and supports dry-run. `devx doctor` is read-only. `devx status` reports which workflows are runnable and which prerequisite blocks each unavailable workflow.

Expected tools include Go 1.27, Docker and Compose, Delve, Buf, protoc, `timeout`, benchstat, and PostgreSQL client tools. Load generation uses the repository's Go client. The SIMD library does not require a C toolchain for consumption.

### 26.4 Makefile

The Makefile is a thin one-to-one facade over `devx`. Workflow logic does not live in Make recipes.

During bootstrap:

```make
DEVX := go run ./cmd/devx
```

Representative targets are:

```text
make
make menu
make install
make doctor
make demo
make build
make test
make race
make bench
make db-up
make migrate
make tui
make serve
make full
make docker-build
```

The default target opens the `devx` menu.

### 26.5 Scripts

Scripts are limited to bootstrap, cross-build packaging, container entrypoints, and load-test aggregation. Build, test, migration, generation, debugging, and benchmark orchestration remain in the Go `devx` command.

## 27. Containers

The repository includes:

- `Dockerfile` for an optimized release image
- `Dockerfile.debug` with Delve and symbols
- `compose.yaml` for PostgreSQL 19 and service adapters

The release image pins Go 1.27 and uses a multi-stage build. Normal dependency runtime dispatch selects the available SIMD tier. The runtime image contains no compiler or source tree.

`devx demo` runs with embedded data and no database. `devx full` starts PostgreSQL 19, applies migrations, starts HTTP and gRPC, waits for health, and can launch the TUI.

## 28. Configuration and Operations

Configuration sources have a documented precedence:

1. Command-line flags
2. Environment variables
3. Optional configuration file
4. Built-in defaults

Operational features include:

- Readiness and liveness endpoints
- Prometheus metrics
- Optional localhost-only pprof
- Structured logs without evidence payloads
- Request and batch limits
- Timeouts and cancellation
- Graceful shutdown
- Policy version and SIMD tier in diagnostic output
- Database migration status
- Runtime dependency checks

## 29. Benchmarking

Benchmarks isolate these costs:

- Parse and compile
- Scalar evaluation
- SIMD evaluation
- Indexed evaluation
- Parallel evaluation
- Explanation materialization
- PostgreSQL journal writes
- HTTP and gRPC transport

Benchmark dimensions include:

| Dimension | Values |
|---|---|
| Rows | 1, 8, 16, 64, 256, 1K, 16K, 64K |
| Policy nodes | 16, 128, 1K, 8K |
| Evidence per request | 0, 2, 8, 32 |
| Workers | 1, 2, 4, 8, `GOMAXPROCS` |
| Audit mode | off, best-effort, required |

Reports include selected SIMD tier, requests per second, nanoseconds per request, allocations, bytes allocated, instructions, cycles, worker count, and batch size.

Evaluator throughput is reported separately from database and transport throughput. Small performance deltas require instruction and cycle measurements plus interleaved benchmark runs.

## 30. Testing Strategy

| Layer | Purpose |
|---|---|
| Unit | AST, interner, validator, compiler, truth logic, indexes, resolution |
| Differential | Scalar, SIMD, parallel, and debug equivalence |
| Integration | Policy source through compilation and result encoding |
| Database | Migrations, graph projection, audit transactions, replay |
| End-to-end | CLI, TUI model, HTTP, gRPC, and R1-R5 output |
| Concurrency | Publication, saturation, cancellation, persistence failure |
| Fuzz | Policy parser, batch parser, AST validator, compiler |
| Race | Worker scheduler, registry, journal, and debugger |
| Golden | Machine-readable results and selected TUI views |
| Benchmark | Latency, throughput, scaling, and allocation |
| DAP | Debug hook and source-map correlation |

Boundary tests cover lengths around vector widths, library thresholds, bitset words, and worker shards:

```text
0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 65
threshold - 1
threshold
threshold + 1
```

Concurrency tests cover policy replacement during evaluation, simultaneous publication, queue saturation, cancellation in each state, required-audit failure, best-effort overflow, worker failure, deterministic outcomes across worker counts, and arena poisoning.

Every test and build invocation must carry an explicit timeout.

## 31. Edge Conditions

### 31.1 Semantic

- No applicable policy
- Multiple applicable requirements
- Definite rejection plus unrelated uncertainty
- Bounded revision plus unresolved safety condition
- Missing evidence with a safe bounded alternative
- Missing evidence that prevents a safe decision
- Stale, unclear, or conflicting approval
- Wrong evidence subject, scope, reviewer, timing, or environment
- Explicitly unapproved environment
- Unverifiable environment
- Duplicate or absent evidence IDs
- Equal-precedence outcomes
- No policy coverage

### 31.2 Compiler

- Empty policy
- Duplicate IDs
- Cyclic references
- Excessive depth or node count
- Empty Boolean groups
- Incorrect operator arity
- Field and value type mismatch
- Unknown fields
- Missing resolution mappings
- Invalid remediation references
- Unreachable nodes
- Integer overflow during sizing
- Source references outside the retained input

Empty `All` and `Any` expressions are rejected instead of receiving implicit identities.

### 31.3 Execution

- Empty batch
- Unaligned columns
- Partial final vectors
- Dirty tail bits
- Insufficient scratch capacity
- Unsupported SIMD tier
- Pure-Go fallback
- Cancellation
- Worker failure
- Concurrent service load
- Deterministic row and diagnostic order

### 31.4 Persistence

- Duplicate idempotency key
- Policy hash mismatch
- Audit transaction rollback
- Database unavailable
- Graph projection failure
- Duplicate reload notification
- Compiled artifact version mismatch
- Sensitive-evidence retention expiry

### 31.5 Debugging

- Delve pauses the process while the TUI is waiting
- Breakpoint on a shared DAG node
- Reverse replay across a policy publication
- Semantic client disconnect
- DAP client reconnect
- Debug and optimized executors disagree

## 32. Documentation Set

The repository will contain:

| Document | Purpose |
|---|---|
| `README.md` | Product overview, quick start, dependencies, CLI, formats, and assignment results |
| `docs/design-note.md` | The required one-page design note |
| `docs/architecture.md` | Components, data flow, ownership, and package boundaries |
| `docs/policy-language.md` | Schema, expressions, evidence semantics, and examples |
| `docs/performance.md` | Data layout, SIMD dispatch, benchmarks, and allocation contracts |
| `docs/concurrency.md` | Workers, ownership, synchronization, cancellation, and shutdown |
| `docs/database.md` | PostgreSQL schema, migrations, SQL/PGQ projection, and audit modes |
| `docs/api.md` | CLI, HTTP, gRPC, and machine-readable formats |
| `docs/debugging.md` | Semantic TUI, Delve DAP, Neovim, and debug builds |
| `docs/development.md` | Make, `devx`, setup, tests, generation, and containers |
| `docs/operations.md` | Configuration, health, metrics, limits, backup, and recovery |
| `docs/ai-usage.md` | Where AI tools assisted, as required by the exercise |
| `results/requests.json` | Required machine-readable output for R1 through R5 |

Documentation claims about speed will cite committed benchmark output. PostgreSQL 19 will be described as beta until the project pins its GA release.

## 33. Repository Layout

```text
cmd/verifoxx/                  product binary
cmd/devx/                      developer-experience CLI

internal/schema/               fields, types, symbols
internal/ast/                  pointerless AST and builders
internal/compile/              validation, normalization, lowering
internal/program/              immutable compiled program
internal/eval/                 scalar, SIMD, and parallel executors
internal/index/                policy and fact indexes
internal/truth/                truth and reason bitplanes
internal/result/               generic result batches
internal/scheduler/            worker ownership and admission
internal/debug/                semantic debugger
internal/persistence/          persistence ports

internal/adapters/json/
internal/adapters/cli/
internal/adapters/tui/
internal/adapters/http/
internal/adapters/grpc/
internal/adapters/postgres/
internal/adapters/dap/

api/proto/
policies/verifoxx/
migrations/
testdata/
results/
cli/
scripts/
docs/
```

## 34. Delivery Sequence

1. Module, commands, and baseline documentation
2. Schema, pointerless AST, and builder
3. Verifoxx policy pack
4. Validation, normalization, and compiler
5. Scalar reference evaluator
6. Generic outcomes and required R1-R5 output
7. SoA request and evidence batches
8. SIMD evaluator
9. Bitmaps, indexes, and pruning
10. Parallel scheduler and arena ownership
11. PostgreSQL 19 schema and migrations
12. Policy registry and audit journal
13. SQL/PGQ AST projection
14. Benchmarks and allocation verification
15. Semantic debug executor
16. Bubble Tea TUI
17. Delve DAP and Neovim integration
18. HTTP API and metrics
19. gRPC API
20. Full `devx` workflow
21. Release and debug containers
22. CI and complete documentation

Each accelerated stage remains differentially testable against the scalar reference.

## 35. Acceptance Criteria

The implementation is complete when all of the following statements are supported by tests or measured output:

- R1 through R5 produce the intended machine-readable decisions.
- No evaluator path checks a supplied request ID.
- The policy representation preserves applicability, obligations, evidence quality, uncertainty, and remediation.
- AST and program nodes use integer references rather than object pointers.
- Hot request, evidence, mask, scratch, and result data use SoA or CSR.
- Steady-state evaluation performs zero allocations.
- SIMD dispatch reaches the selected runtime tier.
- Scalar, SIMD, parallel, and debug executors agree exactly.
- Large batches cross a measured threshold before parallel execution.
- Evaluation shards acquire no locks.
- Policy publication uses immutable atomic replacement.
- PostgreSQL does not appear in the evaluator hot path.
- Stored decisions pin exact policy, request, evidence, and engine versions.
- PostgreSQL 19 SQL/PGQ can query normalized AST topology.
- The TUI can step through semantic instructions and highlight the relevant graph node.
- Neovim can connect to the debug worker through Delve DAP.
- The TUI runs in a separate terminal through the semantic debug channel.
- `devx` provides fuzzy interactive command selection and scriptable subcommands.
- The Makefile remains a thin facade over `devx`.
- Release and debug container workflows run from documented commands.
- The default demonstration works without PostgreSQL.
- Full service mode starts PostgreSQL 19 and the network adapters.
- Unit, differential, integration, database, end-to-end, race, fuzz, benchmark, and debugger tests exist.
- The required README, one-page design note, AI-use statement, and R1-R5 results are present.

## 36. Known Risks

| Risk | Mitigation |
|---|---|
| The complete system exceeds the original 4-5 hour scope | Preserve the required evaluator and output as an early complete release boundary. |
| PostgreSQL 19 is still beta | Pin Beta 3 for development, test migrations, and move to the GA image when released. |
| SIMD work may not pay at assignment scale | Let the library own thresholds and publish benchmark results for both small and large batches. |
| Generic policy features can obscure the assignment | Keep the Verifoxx pack, expected results, and one-page design note prominent. |
| Parallel overhead may exceed useful work | Select parallel execution only after benchmarked crossover points. |
| A debugger can diverge from the fast engine | Execute the same compiled program and require differential equivalence. |
| Persistent full traces can grow rapidly | Store driving findings by default and apply retention to opt-in traces. |
| Developer tooling can duplicate Make and scripts | Keep `devx` authoritative and every wrapper thin. |

## 37. Conclusion

The system is a compiler because it converts policy source into a validated, normalized, and scheduled execution representation. It is a decision engine because it evaluates facts and evidence under explicit uncertainty semantics. Its physical design follows the workload: immutable policy programs, columnar request batches, bitplane logic, whole-slice SIMD operations, and row-aligned parallel shards.

PostgreSQL 19 provides durable policy history and decision auditability without entering the hot path. The semantic debugger, TUI, and Delve integration expose the same compiled program at three levels: policy meaning, execution state, and Go implementation. The `devx`, Make, Docker, Compose, and documentation surfaces make both the assignment demonstration and the full service reproducible from a fresh environment.
