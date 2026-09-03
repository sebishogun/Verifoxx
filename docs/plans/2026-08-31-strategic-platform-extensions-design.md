# Strategic Policy Platform Extensions Design

**Date:** 2026-08-31

**Status:** Approved

**Roadmap:** Tasks 54-57, Phase 19

## Goal

Add bounded SQL and RLS frontends, semantic policy regression analysis, a
portable WebAssembly target, and optional production telemetry without forking
NornRune's semantic model or evaluator. Named optional integrations receive
explicit capability profiles and conformance tests; none imply complete vendor
compatibility.

## Shared Constraints

- Keep source parsing, artifact decoding, exporter work, maps, reflection,
  formatting, and host calls outside per-row and per-node evaluator paths.
- Reuse the existing pointerless semantic policy, immutable Program, SoA batch,
  evaluator, result, and observability contracts.
- Keep warmed evaluator paths at `0 B/op`, `0 allocs/op`.
- Bound every source, table, edge list, domain, search budget, artifact section,
  memory region, label, trace attribute, queue, and shutdown interval.
- Return stable machine-readable outcomes and exact source spans. Unsupported or
  unprovable behavior fails closed rather than being approximated.

## SQL And RLS Frontends

### Profiles

The shared SQL package defines dialect IDs, schema declarations, parameters,
commands, roles, policy modes, source spans, and capability rows. PostgreSQL 19
supports the first executable subset. Snowflake and Databricks expose separate
expression-only profiles over the common parser and semantic table. Each profile
has its own capability matrix and fixtures; syntax accepted in one profile is
not assumed valid in another.

The common scalar subset is Boolean literals, typed string/integer/Boolean
literals, bound identifiers, `AND`, `OR`, `NOT`, `=`, `<>`, `<`, `<=`, `>`,
`>=`, `IN`, `IS NULL`, `IS NOT NULL`, parentheses, and explicitly declared
parameters. PostgreSQL positional parameters and profile-specific parameter
forms are normalized only after schema binding. Unsupported functions, dynamic
casts, subqueries, joins, aggregates, windows, collation, session access, and
catalog access return exact-span diagnostics.

### Data Layout And Parsing

A hand-written bounded lexer emits compact token columns and byte spans. The
precedence parser fills the existing `frontend.Policy` through its Builder;
there is no general object AST. Dialect-specific lexical rules and capability
checks run before lowering. Identifiers bind against an explicit schema without
database access. Quoted or Unicode identifiers are either bound exactly by a
profile or rejected with their complete source span.

PostgreSQL RLS definitions use parallel policy columns and CSR role edges:
command, permissive/restrictive mode, `USING` root, `WITH CHECK` root, and role
ranges. Composition adds explicit `sql.command` and `sql.role` bindings and
builds one semantic root:

- applicable permissive policies combine with OR;
- applicable restrictive policies combine with AND;
- role and command mismatches do not participate;
- no applicable permissive policy denies;
- omitted `WITH CHECK` reuses `USING` where PostgreSQL does;
- SELECT/DELETE use `USING`, INSERT uses `WITH CHECK`, and UPDATE represents both
  existing-row and proposed-row phases explicitly.

SQL NULL is represented by an absent value plus the existing missing reason.
Scalar three-valued logic therefore maps to NornRune unknown without coercion.
RLS uses `DefaultReject`, so a not-true predicate denies while retaining its
missing reason. Standalone expression compilation uses `DefaultEscalate`.

### Verification

Unit and fuzz tests cover precedence, comments, strings, numeric boundaries,
parameters, case folding, Unicode, source spans, depth/size limits, unsupported
features, and malformed RLS. PostgreSQL integration tests compare supported
expressions and RLS combinations against PostgreSQL 19. Snowflake and Databricks
fixtures test only documented profile behavior because no local official engine
is assumed. Benchmarks separate lex/parse/lower cost from existing evaluator
cost and report NULL density and data layout.

## Semantic Policy Diff

### Contract

The public result is exactly `Equivalent`, `Widened`, `Narrowed`, `Changed`, or
`Inconclusive`. Classification uses a caller-supplied 4x4 decision-risk matrix;
the library does not impose one universal ordering. A finite domain declares
field values, presence states, evidence states, and a maximum candidate budget.
An incomplete or unsupported domain cannot produce an equivalence proof.

### Search

Comparison begins with stable immutable Program slab and dependency checks. If
all executable and resolution data match, the result is equivalent without row
generation. Otherwise a deterministic mixed-radix generator fills reusable SoA
batches directly. Changed field and evidence dependencies are varied first;
unchanged dimensions are pruned. Native bulk evaluation compares decisions,
reasons, applied requirements, evidence, remediation, explanations, and source
provenance.

The smallest differing candidate in domain order becomes an owned
counterexample. Budget exhaustion, cancellation, unsupported Program features,
or incomplete field coverage returns `Inconclusive` with bounded uncertainty.

### Optional Proof Backend

A `Prover` interface accepts the same canonical pair and finite domain. The
first backend is deterministic exhaustive finite-domain proof using the concrete
evaluator. Future SMT adapters remain optional. Any backend claim is replayed
against concrete evaluation; disagreement returns `Inconclusive`.

Exhaustive small-domain tests, generated mutation tests, symmetry checks, fuzzing,
and policy-pack assertions prove classification and counterexample replay.

## WebAssembly Target

### Artifact And ABI

The exporter serializes immutable Program columns into a canonical
little-endian envelope containing magic, ABI version, Program schema version,
limits, section descriptors, SHA-256, and host capability requirements. Loaders
validate total size, section ordering, overlap, alignment, counts, checksums, and
cross-references before allocating evaluator storage. Go pointers and transport
objects never cross the ABI.

The versioned ABI exposes metadata, Program load, bounded column upload, batch
evaluation, result read, reset, cancellation/fuel, and last-error operations.
Callers own input/output byte regions; the module owns pre-sized request,
scratch, and result arenas. Warm evaluation performs no memory growth, host
calls, or per-row allocation.

### Runtime Profiles

WASI and browser are base profiles. Envoy, Istio, and Cloudflare profiles are
explicit manifests over the same ABI and declare request metadata, clocks,
storage, network, and logging capabilities. Missing required capabilities reject
module activation. Host profiles cannot weaken policy evidence or environment
requirements. WebAssembly SIMD remains disabled until runtime detection and
scalar differential tests pass.

Native and module evaluators run the same conformance corpus in two available
independent runtimes and a browser-compatible harness. Tests cover malformed
artifacts, insufficient memory/fuel, traps, cancellation, Unicode, large
batches, deterministic builds, and exact output parity. Benchmarks separate
startup, artifact load, host copies, evaluation, and encoding.

## Production Telemetry

### Counters And Snapshots

Telemetry extends the existing observability path. Cache-line-separated atomic
arrays store fixed decisions, reasons, audit outcomes, reload outcomes, shutdown
failures, rows, batches, queue wait, and latency buckets. Service boundaries
aggregate one completed batch; evaluator kernels receive no callbacks, spans,
interfaces, locks, maps, formatting, or exporter calls.

One immutable bounded snapshot feeds Prometheus and optional OpenTelemetry
metrics. Stable labels are fixed enums only. Request IDs, evidence, policy names
or hashes, users, URLs, source, credentials, and error strings are not accepted
by telemetry APIs.

### Tracing And Backpressure

Optional sampled spans cover admission, decode, policy lookup, evaluation, audit
acknowledgment, and response encoding. Span attributes are fixed operation,
outcome, reason, status, and duration values. HTTP and gRPC propagate trace
context through adapters; no protected payload enters a span.

Export queues are bounded and non-blocking. Backpressure drops optional exports
and increments a fixed drop counter; it never blocks policy evaluation or
required audit persistence. Shutdown flushes within a caller-supplied deadline.
Readiness/liveness remain separate from telemetry.

Tests cover exact snapshots, exporter parity, races, cardinality, redaction,
backpressure, unavailable collectors, shutdown, and trace propagation.
Interleaved benchmarks measure disabled, counters-only, Prometheus, sampled
OpenTelemetry, and forced tracing modes before setting an overhead budget.

## Deferred Claims

- Complete PostgreSQL, Snowflake, or Databricks language compatibility.
- Unbounded equivalence proofs or solver-backed proofs without concrete replay.
- Proxy-vendor certification or deployment support beyond the declared host ABI.
- Zero-cost enabled telemetry or exporter availability guarantees.
