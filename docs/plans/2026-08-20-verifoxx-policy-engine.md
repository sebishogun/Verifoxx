# Verifoxx Policy Engine Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the complete production-oriented Verifoxx policy compiler, SIMD batch evaluator, audit service, semantic debugger, network adapters, developer tooling, and required candidate-exercise submission.

**Architecture:** Parse bounded policy documents into a pointerless AST, validate and lower them into an immutable SoA program, then evaluate SoA request batches through scalar, SIMD, and row-sharded executors. Keep adapters outside the core; persist canonical policy versions and immutable decisions in PostgreSQL 19 while exposing normalized AST nodes and edges through SQL/PGQ.

**Tech Stack:** Go 1.27, `github.com/sebishogun/simd` v1.21.0, PostgreSQL 19, pgx v5, Bubble Tea, Cobra, Charmbracelet `huh`, gRPC and Protocol Buffers, Prometheus, Delve DAP, Docker, Docker Compose, and GNU Make.

---

## Performance Tenets

The implementation order follows one causal chain:

```text
SoA data + grouped lifetimes + zero per-record allocation
    -> contiguous, uniformly typed arrays
    -> bulk kernels become possible
    -> SIMD execution and natural parallel shards
```

1. Classify every operation as policy, batch, request, or node-row work before implementing it.
2. Permit no allocation inside per-request, per-node, or per-record evaluation paths.
3. Lay out columns, masks, edges, and lifetimes before writing evaluator control flow.
4. Send whole slices through verified SIMD kernels and confirm runtime dispatch.
5. Prune irrelevant policies and facts before decoding or evaluation, and never scan the same data twice without a measured reason.
6. Shard only above a measured crossover, on row and bitset boundaries, with private scratch and output.
7. Use fixed ownership and caller-provided buffers before considering `sync.Pool`.
8. Prove behavior with `-benchmem`, escape analysis, disassembly, instructions, cycles, and interleaved benchmark comparisons.

## Execution Rules

- Read `AGENTS.md` and `docs/plans/2026-08-20-verifoxx-policy-engine-design.md` before each phase.
- Use `@superpowers/test-driven-development` for every behavioral change.
- Use `@superpowers/systematic-debugging` for every unexpected failure.
- Use `@superpowers/verification-before-completion` before marking a task complete.
- Keep the scalar evaluator as the executable specification for SIMD, parallel, and debug paths.
- Never add a per-row allocation, interface dispatch, callback, log call, or lock to the evaluator.
- Every test or build command must have an explicit timeout.
- Do not run tests or builds in a watch loop.
- Commit steps in this plan are executed only when the user requests commits.

## Phase 1: Establish The Executable Baseline

### Task 1: Initialize The Module And Command Skeleton

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `cmd/verifoxx/main.go`
- Create: `internal/app/app.go`
- Create: `internal/buildinfo/buildinfo.go`
- Test: `internal/app/app_test.go`

**Steps:**

1. Initialize module `github.com/sebishogun/verifoxx` with Go 1.27.
2. Add `github.com/sebishogun/simd@v1.21.0`.
3. Write a failing test asserting that `app.Run` returns exit code zero for `--version` and writes a non-empty version line into a caller-provided buffer or writer.
4. Run `go test -timeout 60s ./internal/app`; expect failure because `Run` is missing.
5. Implement the smallest command dispatcher needed for `--version` and `help` without introducing a CLI framework yet.
6. Run `go test -timeout 60s ./internal/app`; expect success.
7. Run `timeout 120s go build ./cmd/verifoxx`; expect success.
8. Run `go mod tidy` and inspect `go.mod` and `go.sum`.
9. Commit when requested: `chore: initialize verifoxx module`.

### Task 2: Add Embedded Assignment Inputs

**Files:**
- Create: `internal/fixtures/embed.go`
- Create: `internal/fixtures/verifoxx-policy.json`
- Create: `internal/fixtures/verifoxx-requests.json`
- Create: `internal/fixtures/verifoxx-evidence.json`
- Test: `internal/fixtures/embed_test.go`

**Steps:**

1. Transcribe the three requirements, five requests, and four evidence records from the PDF into versioned JSON fixtures.
2. Keep requirement IDs and request IDs in separate JSON fields and schemas.
3. Write failing tests for fixture presence, JSON validity, unique IDs within each namespace, and exact counts.
4. Run `go test -timeout 60s ./internal/fixtures`; expect failure before embedding exists.
5. Embed the files with `go:embed` and expose read-only byte slices.
6. Run `go test -timeout 60s ./internal/fixtures`; expect success.
7. Compare every fixture value against the PDF, which remains the source of truth.
8. Commit when requested: `chore: add candidate exercise fixtures`.

## Phase 2: Define The Semantic Data Model

### Task 3: Add Strong IDs, Value Types, And Schema

**Files:**
- Create: `internal/schema/id.go`
- Create: `internal/schema/value.go`
- Create: `internal/schema/field.go`
- Create: `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Steps:**

1. Write compile-time and runtime tests proving `RequirementID`, `RequestID`, `EvidenceID`, `NodeID`, `FieldID`, `ValueID`, `OutcomeID`, and `RemediationID` are distinct named types.
2. Define bounded value kinds for symbols, integers, Booleans, timestamps, and presence.
3. Define a schema builder that rejects duplicate fields and incompatible redefinitions.
4. Run `go test -timeout 60s ./internal/schema`; expect failure.
5. Implement the minimal schema tables as contiguous slices with integer IDs.
6. Run `go test -timeout 60s ./internal/schema`; expect success.
7. Run `timeout 120s go build -gcflags=-m ./internal/schema` and inspect unexpected escapes.
8. Commit when requested: `feat: define policy schema types`.

### Task 4: Implement The Byte Arena And Symbol Interner

**Files:**
- Create: `internal/arena/bytes.go`
- Create: `internal/schema/symbols.go`
- Test: `internal/arena/bytes_test.go`
- Test: `internal/schema/symbols_test.go`
- Benchmark: `internal/schema/symbols_bench_test.go`

**Steps:**

1. Write failing tests for appending bytes, stable offset-length references, reset semantics, duplicate symbol reuse, hash collisions, growth, and zero-value use.
2. Write a benchmark that interns repeated byte slices and reports allocations.
3. Run `go test -timeout 60s ./internal/arena ./internal/schema`; expect failure.
4. Implement one byte arena and an open-addressed symbol table backed by pre-sized slices.
5. Avoid converting input bytes to strings during lookup.
6. Run `go test -timeout 60s ./internal/arena ./internal/schema`; expect success.
7. Run `go test -timeout 120s -bench=Symbol -benchmem ./internal/schema`; record allocation behavior.
8. Commit when requested: `feat: add byte arena and symbol interner`.

## Phase 3: Build The Pointerless AST

### Task 5: Add AST Node Tables And Builder

**Files:**
- Create: `internal/ast/kind.go`
- Create: `internal/ast/document.go`
- Create: `internal/ast/builder.go`
- Create: `internal/ast/source.go`
- Create: `internal/ast/value.go`
- Create: `internal/ast/semantic.go`
- Test: `internal/ast/builder_test.go`

**Steps:**

1. Write failing tests for typed literals, scalar and `In` compares, n-ary groups, negation, evidence nodes and catalogues, policy provenance, source spans, requirements, clauses, satisfied/false/unresolved outcomes, bounded remediations, and every CSR range.
2. Assert that relationships use integer IDs and that no AST node contains a pointer or child slice.
3. Run `go test -timeout 60s ./internal/ast`; expect failure.
4. Implement `Document` as top-level node-kind and node-ref columns plus typed SoA expression, value, requirement, clause, outcome, and remediation tables.
5. Retain source bytes once, store decoded symbol literals in one byte slab, and use CSR for group children, `In` values, requirement clauses, clause evidence, and remediation alternatives.
6. Implement capacity hints and reset without per-node, per-value, or per-edge allocation.
7. Run `go test -timeout 60s ./internal/ast`; expect success.
8. Add warm-reuse and cold benchmarks constructing policies of 16, 128, 1,024, and 8,192 nodes.
9. Run `go test -timeout 120s -bench=AST -benchmem ./internal/ast`.
10. Commit when requested: `feat: add pointerless policy ast`.

### Task 6: Decode Policy JSON Directly Into The AST

**Files:**
- Create: `internal/adapters/jsonpolicy/decoder.go`
- Create: `internal/adapters/jsonpolicy/errors.go`
- Test: `internal/adapters/jsonpolicy/decoder_test.go`
- Fuzz: `internal/adapters/jsonpolicy/fuzz_test.go`
- Test data: `testdata/policies/`

**Steps:**

1. Write failing tests for every expression kind, source metadata, resolution states, remediation, unknown fields, malformed JSON, and duplicate IDs.
2. Add malformed policies for truncated input, excessive depth, invalid arity, and invalid references.
3. Run `go test -timeout 60s ./internal/adapters/jsonpolicy`; expect failure.
4. Implement a bounded decoder that fills `DocumentBuilder` and rejects unknown fields.
5. Keep parser errors separate from semantic compile diagnostics.
6. Run `go test -timeout 60s ./internal/adapters/jsonpolicy`; expect success.
7. Run one bounded fuzz invocation: `go test -timeout 60s -fuzz=FuzzDecodePolicy -fuzztime=10s ./internal/adapters/jsonpolicy`.
8. Commit when requested: `feat: decode policy documents`.

### Task 7: Validate AST Structure And Semantics

**Files:**
- Create: `internal/compile/diagnostic.go`
- Create: `internal/compile/validate.go`
- Test: `internal/compile/validate_test.go`

**Steps:**

1. Write failing table tests for cycles, duplicate IDs, empty groups, unknown fields, type mismatches, invalid operator arity, missing resolutions, unreachable nodes, and invalid source spans.
2. Require diagnostics to include source ranges and stable machine-readable codes.
3. Run `go test -timeout 60s ./internal/compile`; expect failure.
4. Implement one structural and semantic validation pass where possible.
5. Reject empty `All` and `Any` expressions explicitly.
6. Run `go test -timeout 60s ./internal/compile`; expect success.
7. Commit when requested: `feat: validate policy ast`.

## Phase 4: Define Truth And Resolution

### Task 8: Implement Four-Valued Truth Bitplanes

**Files:**
- Create: `internal/truth/planes.go`
- Create: `internal/truth/scalar.go`
- Test: `internal/truth/planes_test.go`
- Benchmark: `internal/truth/planes_bench_test.go`

**Steps:**

1. Write exhaustive truth-table tests for `Not`, `And`, and `Or` over true, false, unknown, and conflict.
2. Write boundary tests for zero rows, partial words, dirty tail bits, and aliasing rules.
3. Run `go test -timeout 60s ./internal/truth`; expect failure.
4. Implement positive and negative `[]uint64` planes and scalar word operations.
5. Mask tail bits after each externally visible operation.
6. Run `go test -timeout 60s ./internal/truth`; expect success.
7. Run `go test -timeout 120s -bench=Truth -benchmem ./internal/truth`.
8. Commit when requested: `feat: add four-valued truth planes`.

### Task 9: Implement Reason Masks, Outcomes, And Remediation

**Files:**
- Create: `internal/truth/reason.go`
- Create: `internal/result/outcome.go`
- Create: `internal/result/remediation.go`
- Create: `internal/result/resolution.go`
- Test: `internal/result/resolution_test.go`

**Steps:**

1. Write failing tests that distinguish missing, stale, unclear, unverifiable, wrong-scope, wrong-subject, wrong-timing, invalid, and conflict.
2. Test policy-defined precedence and deterministic ties.
3. Test that missing usage approval can map to `Revise` with a bounded usage remediation while stale approval maps to `Escalate`.
4. Run `go test -timeout 60s ./internal/result`; expect failure.
5. Implement generic outcome and remediation tables using IDs and slices.
6. Run `go test -timeout 60s ./internal/result`; expect success.
7. Commit when requested: `feat: add generic outcome resolution`.

## Phase 5: Compile An Immutable Program

### Task 10: Normalize And Lower The AST

**Files:**
- Create: `internal/compile/normalize.go`
- Create: `internal/compile/lower.go`
- Create: `internal/program/program.go`
- Create: `internal/program/instruction.go`
- Test: `internal/compile/lower_test.go`

**Steps:**

1. Write failing tests for nested group flattening, topological order, safe common-subexpression reuse, stable source maps, and exact instruction counts.
2. Assert that compiled nodes contain integer operands and no node pointers.
3. Run `go test -timeout 60s ./internal/compile ./internal/program`; expect failure.
4. Implement normalization and lower into opcode and operand columns.
5. Preserve deterministic order independent of map iteration.
6. Run `go test -timeout 60s ./internal/compile ./internal/program`; expect success.
7. Commit when requested: `feat: compile policy ast into program`.

### Task 11: Add Scratch-Slot Liveness Allocation

**Files:**
- Create: `internal/compile/liveness.go`
- Create: `internal/program/slots.go`
- Test: `internal/compile/liveness_test.go`

**Steps:**

1. Write failing tests for linear expressions, branching DAGs, shared nodes, roots retained for explanation, and debug mode without reuse.
2. Run `go test -timeout 60s ./internal/compile`; expect failure.
3. Implement last-use analysis and assign reusable truth and reason slots.
4. Verify no slot is overwritten before its final consumer.
5. Run `go test -timeout 60s ./internal/compile`; expect success.
6. Benchmark peak scratch bytes with and without liveness reuse.
7. Commit when requested: `feat: reuse compiled evaluation slots`.

### Task 12: Build Applicability And Field Indexes

**Files:**
- Create: `internal/index/schema.go`
- Create: `internal/index/policy.go`
- Create: `internal/compile/index.go`
- Test: `internal/index/policy_test.go`

**Steps:**

1. Write failing tests for action, resource, and trust selectors, including missing fields that must remain candidates.
2. Test deterministic bitmap construction and exact candidate sets.
3. Run `go test -timeout 60s ./internal/index`; expect failure.
4. Implement immutable field and candidate-policy indexes.
5. Keep index results conservative under unknown values.
6. Run `go test -timeout 60s ./internal/index`; expect success.
7. Commit when requested: `feat: add policy applicability indexes`.

## Phase 6: Decode SoA Request Batches

### Task 13: Implement Batch And Evidence Builders

**Files:**
- Create: `internal/eval/batch.go`
- Create: `internal/eval/evidence.go`
- Create: `internal/eval/builder.go`
- Test: `internal/eval/builder_test.go`
- Benchmark: `internal/eval/builder_bench_test.go`

**Steps:**

1. Write failing tests for column offsets, presence masks, CSR evidence ranges, capacity reuse, empty batches, and tail clearing.
2. Run `go test -timeout 60s ./internal/eval`; expect failure.
3. Implement column-major fact storage and evidence SoA.
4. Require the builder to size from row and evidence counts when available.
5. Run `go test -timeout 60s ./internal/eval`; expect success.
6. Run `go test -timeout 120s -bench=BatchBuilder -benchmem ./internal/eval`.
7. Commit when requested: `feat: add soa request batches`.

### Task 14: Decode Request And Evidence JSON Into SoA

**Files:**
- Create: `internal/adapters/jsonbatch/decoder.go`
- Create: `internal/adapters/jsonbatch/errors.go`
- Test: `internal/adapters/jsonbatch/decoder_test.go`
- Fuzz: `internal/adapters/jsonbatch/fuzz_test.go`

**Steps:**

1. Write failing tests for the five supplied requests, missing fields, unknown fields, duplicate IDs, missing references, wrong types, and malformed input.
2. Run `go test -timeout 60s ./internal/adapters/jsonbatch`; expect failure.
3. Implement direct decoding into caller-owned batch columns.
4. Distinguish malformed transport data from semantically missing policy facts.
5. Run `go test -timeout 60s ./internal/adapters/jsonbatch`; expect success.
6. Run `go test -timeout 60s -fuzz=FuzzDecodeBatch -fuzztime=10s ./internal/adapters/jsonbatch`.
7. Commit when requested: `feat: decode request batches`.

## Phase 7: Implement The Reference Evaluator

### Task 15: Evaluate Leaf Predicates And Evidence

**Files:**
- Create: `internal/eval/scalar.go`
- Create: `internal/eval/predicate.go`
- Create: `internal/eval/evidence_eval.go`
- Test: `internal/eval/predicate_test.go`
- Test: `internal/eval/evidence_eval_test.go`

**Steps:**

1. Write failing tests for every comparison operator, presence, missing values, valid evidence, stale evidence, mismatched scope, and conflicting records.
2. Run `go test -timeout 60s ./internal/eval`; expect failure.
3. Implement scalar whole-column leaf evaluation into bitplanes and reason masks.
4. Reduce request evidence through CSR without allocating per request.
5. Run `go test -timeout 60s ./internal/eval`; expect success.
6. Commit when requested: `feat: evaluate policy leaves`.

### Task 16: Execute Programs And Resolve Outcomes

**Files:**
- Create: `internal/eval/executor.go`
- Create: `internal/result/batch.go`
- Test: `internal/eval/executor_test.go`

**Steps:**

1. Write failing tests for applicable, inactive, true, false, unknown, and conflicting policy paths.
2. Test simultaneous outcomes and precedence.
3. Run `go test -timeout 60s ./internal/eval`; expect failure.
4. Execute the compiled schedule over caller-owned scratch and result batches.
5. Use liveness-assigned slots and deterministic driver selection.
6. Run `go test -timeout 60s ./internal/eval`; expect success.
7. Run `timeout 120s go build -gcflags=-m ./internal/eval` and inspect escapes.
8. Commit when requested: `feat: execute compiled policies`.

### Task 17: Add Verifoxx Conformance Tests And Golden Results

**Files:**
- Create: `policies/verifoxx/policy.json`
- Create: `internal/conformance/verifoxx_test.go`
- Create: `results/requests.json`
- Create: `testdata/golden/requests.json`

**Steps:**

1. Author the three supplied requirements in the bounded policy format with source text and remediations.
2. Write failing conformance tests expecting R1 Approve, R2 Reject, R3 Revise, R4 Escalate, and R5 Escalate.
3. Assert all required output fields and exact applied requirement IDs.
4. Run `go test -timeout 60s ./internal/conformance`; expect failure.
5. Correct policy semantics or evaluator behavior without branching on request IDs.
6. Run `go test -timeout 60s ./internal/conformance`; expect success.
7. Generate and compare deterministic golden JSON.
8. Commit when requested: `feat: add verifoxx policy conformance`.

## Phase 8: Integrate SIMD

### Task 18: Inventory And Wrap Required SIMD Operations

**Files:**
- Create: `internal/simdops/ops.go`
- Create: `internal/simdops/purego.go`
- Create: `internal/simdops/simd.go`
- Create: `internal/simdops/info.go`
- Test: `internal/simdops/ops_test.go`
- Document: `docs/performance.md`

**Steps:**

1. Inspect the v1.21 public API for uint32 comparisons, uint64 bit operations, masks, and compression.
2. Record selected operations, thresholds, alias contracts, and fallback behavior in `docs/performance.md`.
3. Write differential tests against local scalar references for zero length, vector boundaries, unaligned subslices, and partial tails.
4. Run `go test -timeout 60s ./internal/simdops`; expect failure.
5. Implement thin whole-slice wrappers with no per-element function calls.
6. Expose runtime tier and threshold metadata for diagnostics.
7. Run `GOARCH=386 go test -timeout 60s ./internal/simdops`; expect scalar-fallback success.
8. Run `go test -timeout 60s -tags=purego ./internal/simdops`; expect success.
9. Commit when requested: `feat: integrate simd primitives`.

### Task 19: Add The SIMD Evaluator

**Files:**
- Create: `internal/eval/simd.go`
- Test: `internal/eval/simd_test.go`
- Benchmark: `internal/eval/eval_bench_test.go`

**Steps:**

1. Write differential tests comparing scalar and SIMD outputs across all truth states and evidence reasons.
2. Cover rows 0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 65 and each library threshold plus or minus one.
3. Run `go test -timeout 60s ./internal/eval`; expect failure before SIMD execution exists.
4. Route compatible program stages through `simdops` whole-slice operations.
5. Retain scalar handling below measured crossover and for unsupported shapes.
6. Run `go test -timeout 60s ./internal/eval`; expect success.
7. Run `go test -timeout 120s -bench=Evaluate -benchmem ./internal/eval`.
8. Verify selected dispatch through library diagnostics and disassembly.
9. Commit when requested: `feat: evaluate policy batches with simd`.

### Task 20: Add Reused Fact Bitmap Indexes

**Files:**
- Create: `internal/index/batch.go`
- Modify: `internal/compile/index.go`
- Modify: `internal/eval/executor.go`
- Test: `internal/index/batch_test.go`
- Benchmark: `internal/index/batch_bench_test.go`

**Steps:**

1. Write tests for exact masks, missing values, sparse matches, dense matches, and irrelevant fields.
2. Add paired benchmarks for direct scans and build-plus-reuse at one through many predicates.
3. Run `go test -timeout 60s ./internal/index`; expect failure.
4. Implement index construction only for compiler-selected reused fields.
5. Set the initial crossover from measured results, not a guessed constant.
6. Run `go test -timeout 60s ./internal/index ./internal/eval`.
7. Run `go test -timeout 120s -bench=BatchIndex -benchmem ./internal/index`.
8. Commit when requested: `feat: prune batch predicates with bitmaps`.

## Phase 9: Add Parallel Execution And Arena Ownership

### Task 21: Implement Worker Scratch And Context Arenas

**Files:**
- Create: `internal/scheduler/context.go`
- Create: `internal/scheduler/arena.go`
- Test: `internal/scheduler/arena_test.go`

**Steps:**

1. Write failing tests for borrow, exclusive ownership, reset, growth, return, double return, escaped output rejection, and poison reuse.
2. Run `go test -timeout 60s ./internal/scheduler`; expect failure.
3. Implement capacity-sized service contexts transferred through a bounded ownership channel.
4. Do not use `sync.Pool`.
5. Run `go test -timeout 60s ./internal/scheduler`; expect success.
6. Commit when requested: `feat: add evaluator arena ownership`.

### Task 22: Implement The Fixed Worker Scheduler

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/worker.go`
- Create: `internal/scheduler/shard.go`
- Test: `internal/scheduler/scheduler_test.go`
- Benchmark: `internal/scheduler/scheduler_bench_test.go`

**Steps:**

1. Write failing tests for serial execution, 64-row-aligned shards, bounded admission, cancellation while queued, deterministic output, and graceful close.
2. Run `go test -timeout 60s ./internal/scheduler`; expect failure.
3. Start a fixed number of long-lived workers with private scratch.
4. Use disjoint result ranges and private diagnostic buffers.
5. Add a global work budget that prevents nested oversubscription.
6. Run `go test -timeout 60s -race ./internal/scheduler`; expect success.
7. Benchmark batch and worker sizes; record the measured parallel crossover.
8. Run `go test -timeout 120s -bench=Scheduler -benchmem ./internal/scheduler`.
9. Commit when requested: `feat: evaluate large batches in parallel`.

### Task 23: Add Immutable Policy Registry Publication

**Files:**
- Create: `internal/program/registry.go`
- Test: `internal/program/registry_test.go`

**Steps:**

1. Write concurrent tests for lookup, copy-on-write publication, active evaluations retaining old programs, and duplicate compilation suppression.
2. Run `go test -timeout 60s -race ./internal/program`; expect failure.
3. Implement atomic immutable snapshots and a short publication mutex.
4. Use content hashes as registry keys.
5. Run `go test -timeout 60s -race ./internal/program`; expect success.
6. Commit when requested: `feat: publish immutable policy programs`.

## Phase 10: Explanations And Output

### Task 24: Materialize Bounded Explanations

**Files:**
- Create: `internal/result/explain.go`
- Create: `internal/result/templates.go`
- Test: `internal/result/explain_test.go`

**Steps:**

1. Write failing tests for applied requirements, used evidence, missing evidence, conflicting evidence, assumptions, uncertainty, and remediation.
2. Assert rationales are bounded and deterministic.
3. Run `go test -timeout 60s ./internal/result`; expect failure.
4. Materialize text lazily from node, evidence, reason, and remediation IDs.
5. Append into caller-owned byte buffers.
6. Run `go test -timeout 60s ./internal/result`; expect success.
7. Commit when requested: `feat: explain policy outcomes`.

### Task 25: Encode Machine-Readable Results

**Files:**
- Create: `internal/adapters/jsonresult/encoder.go`
- Test: `internal/adapters/jsonresult/encoder_test.go`
- Benchmark: `internal/adapters/jsonresult/encoder_bench_test.go`

**Steps:**

1. Write golden tests for the exact required output fields and deterministic ordering.
2. Run `go test -timeout 60s ./internal/adapters/jsonresult`; expect failure.
3. Implement append-based JSON encoding into supplied destination storage.
4. Avoid `fmt.Sprintf` and per-field string allocation.
5. Run `go test -timeout 60s ./internal/adapters/jsonresult`; expect success.
6. Run `go test -timeout 120s -bench=Encode -benchmem ./internal/adapters/jsonresult`.
7. Regenerate `results/requests.json` and compare it with the golden file.
8. Commit when requested: `feat: encode decision results`.

## Phase 11: Product CLI

### Task 26: Add CLI Commands

**Files:**
- Create: `internal/adapters/cli/root.go`
- Create: `internal/adapters/cli/evaluate.go`
- Create: `internal/adapters/cli/validate.go`
- Create: `internal/adapters/cli/compile.go`
- Create: `internal/adapters/cli/explain.go`
- Create: `internal/adapters/cli/simulate.go`
- Modify: `internal/app/app.go`
- Test: `internal/adapters/cli/cli_test.go`

**Steps:**

1. Add Cobra at a pinned version.
2. Write failing command tests using in-memory input and output.
3. Cover no-argument evaluation, external policy/input files, invalid arguments, exit codes, and JSON stdout without log contamination.
4. Run `go test -timeout 60s ./internal/adapters/cli`; expect failure.
5. Implement commands over the same compiler and evaluator APIs.
6. Run `go test -timeout 60s ./internal/adapters/cli`; expect success.
7. Run `timeout 120s go run ./cmd/verifoxx evaluate`; compare output with `results/requests.json`.
8. Commit when requested: `feat: add verifoxx cli`.

## Phase 12: PostgreSQL 19 Persistence

### Task 27: Add Compose PostgreSQL And Migration Tooling

**Files:**
- Create: `compose.yaml`
- Create: `.env.example`
- Create: `migrations/000001_initial.up.sql`
- Create: `migrations/000001_initial.down.sql`
- Create: `internal/adapters/postgres/migrate.go`
- Test: `internal/adapters/postgres/migrations_test.go`

**Steps:**

1. Pin the PostgreSQL 19 Beta 3 image until GA is released.
2. Define health checks, persistent volume, runtime database, migration role, and application role.
3. Write migration SQL for policies, versions, requests, evidence snapshots, evaluations, findings, evidence links, traces, and benchmark runs.
4. Add foreign keys, immutable publication constraints, idempotency keys, timestamps, and hashes.
5. Write an integration test that starts PostgreSQL 19 and migrates up and down.
6. Run `go test -timeout 300s -tags=integration ./internal/adapters/postgres`; expect failure before migration support exists.
7. Implement migration execution under a PostgreSQL advisory lock.
8. Run `go test -timeout 300s -tags=integration ./internal/adapters/postgres`; expect success.
9. Commit when requested: `feat: add postgres schema and migrations`.

### Task 28: Persist And Reload Policy Versions

**Files:**
- Create: `internal/persistence/policy.go`
- Create: `internal/adapters/postgres/policy.go`
- Test: `internal/adapters/postgres/policy_test.go`

**Steps:**

1. Write integration tests for publishing, duplicate hashes, immutable versions, loading source, and concurrent publication.
2. Run `go test -timeout 300s -tags=integration ./internal/adapters/postgres`; expect failure.
3. Implement transactional canonical source and metadata storage using pgx v5.
4. Compile outside the registry publication lock and atomically publish after commit.
5. Run `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`; expect success.
6. Commit when requested: `feat: persist policy versions`.

### Task 29: Add The Decision Audit Journal

**Files:**
- Create: `internal/persistence/journal.go`
- Create: `internal/adapters/postgres/journal.go`
- Create: `internal/adapters/postgres/writer.go`
- Test: `internal/adapters/postgres/journal_test.go`

**Steps:**

1. Write integration tests for `off`, `best-effort`, and `required` modes.
2. Cover transaction rollback, database loss, bounded queue saturation, idempotency, and exact replay references.
3. Run `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`; expect failure.
4. Implement fixed persistence writers and ownership-transfer batches.
5. Use batch or copy protocols rather than one insert transaction per row.
6. Run `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`; expect success.
7. Benchmark journal batch sizes independently from evaluator benchmarks.
8. Commit when requested: `feat: journal immutable decisions`.

### Task 30: Add SQL/PGQ Policy Graph Projection

**Files:**
- Create: `migrations/000002_policy_graph.up.sql`
- Create: `migrations/000002_policy_graph.down.sql`
- Create: `internal/adapters/postgres/graph.go`
- Test: `internal/adapters/postgres/graph_test.go`

**Steps:**

1. Write normalized node and edge projection tests for one policy version.
2. Add the PostgreSQL 19 `CREATE PROPERTY GRAPH` definition over those tables.
3. Write SQL/PGQ tests for requirement-to-outcome paths, evidence dependencies, and version isolation.
4. Run `go test -timeout 300s -tags=integration ./internal/adapters/postgres`; expect failure.
5. Persist graph projection in the same policy publication transaction.
6. Run `go test -timeout 300s -tags=integration ./internal/adapters/postgres`; expect success.
7. Commit when requested: `feat: expose policy ast through sql pgq`.

### Task 31: Add Policy Reload Notifications

**Files:**
- Create: `internal/adapters/postgres/notify.go`
- Test: `internal/adapters/postgres/notify_test.go`

**Steps:**

1. Write integration tests for publication notification, duplicate delivery, reconnect, cancellation, and shutdown.
2. Run `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`; expect failure.
3. Implement `LISTEN/NOTIFY` as a hint; reload the configured policy's durable
   active version, use the payload hash only to detect stale or foreign hints,
   and tolerate duplicates.
4. Run `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`; expect success.
5. Commit when requested: `feat: reload published policies`.

## Phase 13: Service APIs And Operations

### Task 32: Add Configuration And Graceful Lifecycle

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/env.go`
- Create: `internal/service/service.go`
- Create: `internal/service/lifecycle.go`
- Test: `internal/config/config_test.go`
- Test: `internal/service/lifecycle_test.go`

**Steps:**

1. Write tests for flag, environment, file, and default precedence.
2. Write lifecycle tests for admission stop, queue cancellation, evaluation drain, required journal flush, and worker join.
3. Run `go test -timeout 60s -race ./internal/config ./internal/service`; expect failure.
4. Implement validated limits, timeouts, worker counts, audit mode, and database settings.
5. Implement context-driven startup and shutdown.
6. Run `go test -timeout 60s -race ./internal/config ./internal/service`; expect success.
7. Commit when requested: `feat: add service lifecycle`.

### Task 33: Add HTTP Batch API

**Files:**
- Create: `internal/adapters/httpapi/server.go`
- Create: `internal/adapters/httpapi/evaluate.go`
- Create: `internal/adapters/httpapi/policy.go`
- Create: `internal/adapters/httpapi/limits.go`
- Test: `internal/adapters/httpapi/server_test.go`

**Steps:**

1. Write `httptest` failures for validate, compile, evaluate, policy lookup, health, body limits, deadlines, malformed JSON, and audit errors.
2. Run `go test -timeout 60s -race ./internal/adapters/httpapi`; expect failure.
3. Implement batch-first handlers over service ports.
4. Keep policy and evaluator types independent of HTTP types.
5. Run `go test -timeout 60s -race ./internal/adapters/httpapi`; expect success.
6. Commit when requested: `feat: add http policy api`.

### Task 34: Add gRPC API And Generated-Code Checks

**Files:**
- Create: `api/proto/verifoxx/v1/verifoxx.proto`
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `internal/adapters/grpcapi/server.go`
- Test: `internal/adapters/grpcapi/server_test.go`
- Generate: `api/gen/`

**Steps:**

1. Define validate, compile, evaluate batch, and evaluate stream RPCs.
2. Write bufconn tests for unary behavior, streaming order, limits, cancellation, and status mapping.
3. Run `go test -timeout 60s -race ./internal/adapters/grpcapi`; expect failure.
4. Generate pinned protobuf output through a containerized Buf command.
5. Implement adapters without leaking protobuf types into the core.
6. Run `go test -timeout 60s -race ./internal/adapters/grpcapi`; expect success.
7. Add a generated-code drift check.
8. Commit when requested: `feat: add grpc policy api`.

### Task 35: Add Metrics, Health, And Profiling

**Files:**
- Create: `internal/observability/metrics.go`
- Create: `internal/observability/health.go`
- Modify: `internal/adapters/httpapi/server.go`
- Test: `internal/observability/metrics_test.go`

**Steps:**

1. Write tests for batch-level counters, durations, outcomes, queue depth, journal failures, worker count, and SIMD tier.
2. Ensure no metric update occurs per AST node or row.
3. Run `go test -timeout 60s -race ./internal/observability`; expect failure.
4. Implement Prometheus metrics and optional localhost-only pprof.
5. Run `go test -timeout 60s -race ./internal/observability`; expect success.
6. Commit when requested: `feat: add service observability`.

## Phase 14: Semantic Debugger And TUI

### Task 36: Implement Deterministic Debug Execution

**Files:**
- Create: `internal/debug/session.go`
- Create: `internal/debug/state.go`
- Create: `internal/debug/breakpoint.go`
- Create: `internal/debug/replay.go`
- Test: `internal/debug/session_test.go`

**Steps:**

1. Write failing tests for instruction step, node step, step-over, continue, pause, restart, breakpoint types, watches, and replay.
2. Differentially compare final state with scalar and SIMD executors.
3. Run `go test -timeout 60s -race ./internal/debug`; expect failure.
4. Execute the same compiled schedule with retained state and bounded checkpoints.
5. Make one actor goroutine own mutable session state.
6. Run `go test -timeout 60s -race ./internal/debug`; expect success.
7. Commit when requested: `feat: add semantic debug executor`.

### Task 37: Add Semantic Debug Transport

**Files:**
- Create: `internal/debug/protocol.go`
- Create: `internal/debug/server.go`
- Create: `internal/debug/client.go`
- Test: `internal/debug/protocol_test.go`

**Steps:**

1. Write tests over Unix sockets for commands, state snapshots, disconnects, cancellation, framing, and one-writer ordering.
2. Run `go test -timeout 60s -race ./internal/debug`; expect failure.
3. Implement a local semantic protocol with bounded messages and one writer goroutine.
4. Run `go test -timeout 60s -race ./internal/debug`; expect success.
5. Commit when requested: `feat: expose semantic debug protocol`.

### Task 38: Build The Bubble Tea Debugger

**Files:**
- Create: `internal/adapters/tui/model.go`
- Create: `internal/adapters/tui/update.go`
- Create: `internal/adapters/tui/view.go`
- Create: `internal/adapters/tui/tree.go`
- Create: `internal/adapters/tui/styles.go`
- Test: `internal/adapters/tui/model_test.go`
- Golden: `testdata/golden/tui/`

**Steps:**

1. Pin Bubble Tea and styling dependencies.
2. Write model tests for request selection, AST/program toggle, stepping, breakpoints, resize, historical load, and disconnected debug target.
3. Run `go test -timeout 60s ./internal/adapters/tui`; expect failure.
4. Implement three panes for requests, graph, and runtime state plus a command footer.
5. Render shared DAG nodes as references with an expansion mode.
6. Add selected golden views without binding tests to terminal color escape codes.
7. Run `go test -timeout 60s ./internal/adapters/tui`; expect success.
8. Commit when requested: `feat: add semantic debugger tui`.

### Task 39: Integrate Delve DAP And Neovim

**Files:**
- Create: `internal/debug/debugtrap/trap_debug.go`
- Create: `internal/debug/debugtrap/trap_release.go`
- Create: `internal/adapters/dap/launch.go`
- Create: `.vscode/launch.json`
- Create: `docs/debugging.md`
- Test: `internal/adapters/dap/launch_test.go`

**Steps:**

1. Write tests for command construction, port selection, target arguments, build tags, and cancellation.
2. Add a debug-only `debugtrap.Reached(NodeID, InstructionID)` with `go:noinline`.
3. Keep the release implementation free of the debug call path.
4. Run `go test -timeout 60s ./internal/adapters/dap`; expect failure before integration exists.
5. Implement Delve DAP launch and document nvim-dap loading of `.vscode/launch.json`.
6. Keep Neovim as the DAP owner and the TUI on the semantic socket.
7. Run `go test -timeout 60s ./internal/adapters/dap`; expect success.
8. Run `timeout 120s go build -gcflags=all='-N -l' -tags=debug ./cmd/verifoxx`; expect success.
9. Commit when requested: `feat: integrate delve dap debugging`.

## Phase 15: Developer Experience, Make, And Containers

### Task 40: Add The devx CLI

**Files:**
- Create: `cmd/devx/main.go`
- Create: `cmd/devx/cmd/root.go`
- Create: `cmd/devx/cmd/doctor.go`
- Create: `cmd/devx/cmd/status.go`
- Create: `cmd/devx/cmd/install.go`
- Create: `cmd/devx/cmd/build.go`
- Create: `cmd/devx/cmd/test.go`
- Create: `cmd/devx/cmd/database.go`
- Create: `cmd/devx/cmd/performance.go`
- Create: `cmd/devx/cmd/debug.go`
- Create: `cmd/devx/cmd/containers.go`
- Test: `cmd/devx/cmd/root_test.go`

**Steps:**

1. Pin Cobra and Charmbracelet `huh` versions.
2. Write tests for no-argument menu construction, command groups, repo-root detection, status gating, doctor probes, dry-run install plans, and exact subprocess commands.
3. Run `go test -timeout 60s ./cmd/devx/...`; expect failure.
4. Implement the grouped command surface from the design document.
5. Make every subprocess invocation context-bound and timeout-bound.
6. Keep install plans interactive but provide non-interactive CI flags.
7. Run `go test -timeout 60s ./cmd/devx/...`; expect success.
8. Commit when requested: `feat: add devx workflow cli`.

### Task 41: Add devx Installation And Packaging

**Files:**
- Create: `cli/devx`
- Create: `cli/install.sh`
- Create: `cli/build.sh`
- Create: `cli/README.md`
- Test: `cmd/devx/cmd/install_test.go`

**Steps:**

1. Write tests for prefix selection, symlink planning, PATH diagnostics, uninstall, OS and architecture selection, and no shell-rc modification.
2. Run `go test -timeout 60s ./cmd/devx/...`; expect failure.
3. Implement wrapper and installation scripts with strict shell settings.
4. Build host binaries with `CGO_ENABLED=0`, `-trimpath`, and stripped release flags.
5. Run `timeout 180s ./cli/build.sh --host-only`; expect success.
6. Run `timeout 30s ./cli/install.sh --dry-run`; inspect exact actions.
7. Commit when requested: `feat: package devx cli`.

### Task 42: Add The Thin Makefile

**Files:**
- Create: `Makefile`
- Test: `cmd/devx/cmd/makefile_test.go`

**Steps:**

1. Write a test that parses documented Make targets and checks each maps to one `devx` command.
2. Run `go test -timeout 60s ./cmd/devx/...`; expect failure.
3. Add the default menu target plus setup, build, run, database, generation, test, performance, debug, and container targets.
4. Keep recipes to one `devx` invocation each.
5. Run `timeout 30s make help`; expect the same command inventory as `devx --help`.
6. Run `timeout 30s make doctor`; expect a read-only environment report.
7. Commit when requested: `build: add make workflow facade`.

### Task 43: Add Release And Debug Dockerfiles

**Files:**
- Create: `Dockerfile`
- Create: `Dockerfile.debug`
- Create: `.dockerignore`
- Test: `internal/e2e/docker_test.go`

**Steps:**

1. Write an opt-in end-to-end test that builds the image, runs default evaluation, and compares JSON with the golden file.
2. Run `go test -timeout 600s -tags=docker ./internal/e2e`; expect failure.
3. Implement a multi-stage Go 1.27 release image using normal SIMD runtime dispatch.
4. Implement a debug image with Delve and unstripped symbols.
5. Run `timeout 600s docker build -t verifoxx:test .`; expect success.
6. Run `timeout 60s docker run --rm verifoxx:test evaluate`; compare output.
7. Run `go test -timeout 600s -tags=docker ./internal/e2e`; expect success.
8. Commit when requested: `build: add release and debug images`.

### Task 44: Complete Docker Compose Full Mode

**Files:**
- Modify: `compose.yaml`
- Create: `scripts/wait-healthy.sh`
- Test: `internal/e2e/compose_test.go`

**Steps:**

1. Write an opt-in end-to-end test for PostgreSQL health, migration completion, server health, HTTP evaluation, gRPC evaluation, and required audit persistence.
2. Run `go test -timeout 600s -tags=docker ./internal/e2e`; expect failure.
3. Add release server, PostgreSQL 19, migration, and optional debug profiles.
4. Add health dependencies rather than fixed sleeps.
5. Run `timeout 600s docker compose up -d --build --wait`; expect success.
6. Run `go test -timeout 600s -tags=docker ./internal/e2e`; expect success.
7. Run `timeout 120s docker compose down -v`; expect success.
8. Commit when requested: `build: add full compose environment`.

## Phase 16: Performance, Reliability, And Security Gates

### Task 45: Add Controlled Benchmark And Load Commands

**Files:**
- Create: `internal/benchdata/generate.go`
- Create: `internal/eval/benchmark_test.go`
- Create: `cmd/loadgen/main.go`
- Create: `scripts/bench-compare.sh`
- Document: `docs/performance.md`

**Steps:**

1. Add deterministic generators for batch rows, policy nodes, evidence density, and match density.
2. Benchmark scalar, SIMD, indexed, and parallel modes independently.
3. Report SIMD tier, rows, nodes, evidence density, workers, allocations, and bytes.
4. Run `go test -timeout 300s -run='^$' -bench=BenchmarkEvaluate -benchmem ./internal/eval`.
5. Add HTTP and gRPC load generation without exposing a production benchmark endpoint.
6. Add interleaved benchstat comparison and `perf stat` guidance.
7. Commit measured baselines only after a quiet-machine run.
8. Commit when requested: `perf: add evaluation benchmark harness`.

### Task 46: Add Fuzz, Race, And Failure-Injection Suites

**Files:**
- Create: `internal/e2e/failure_test.go`
- Create: `internal/scheduler/stress_test.go`
- Create: `internal/adapters/postgres/failure_test.go`
- Modify: existing fuzz tests

**Steps:**

1. Add policy reload during evaluation, simultaneous publication, queue saturation, worker cancellation, required-audit failure, best-effort overflow, and database reconnect tests.
2. Add arena poisoning and stale-tail tests.
3. Run `go test -timeout 180s -race ./internal/... ./cmd/...`; expect success.
4. Run each fuzz target once with a bounded 10-second duration and explicit 60-second test timeout.
5. Run integration failure tests with `go test -timeout 300s -race -tags=integration ./internal/adapters/postgres`.
6. Commit when requested: `test: add concurrency and failure gates`.

### Task 47: Add Security And Limit Tests

**Files:**
- Create: `internal/security/limits.go`
- Create: `internal/security/redact.go`
- Test: `internal/security/limits_test.go`
- Test: `internal/adapters/httpapi/security_test.go`
- Test: `internal/adapters/grpcapi/security_test.go`

**Steps:**

1. Write failing tests for policy bytes, AST depth, AST nodes, rows, evidence records, output bytes, deadlines, and log redaction.
2. Run `go test -timeout 60s ./internal/security ./internal/adapters/httpapi ./internal/adapters/grpcapi`; expect failure.
3. Implement common validated limits and structured redaction.
4. Ensure protected dataset rows are never accepted as audit payloads.
5. Run `go test -timeout 60s ./internal/security ./internal/adapters/httpapi ./internal/adapters/grpcapi`; expect success.
6. Commit when requested: `feat: enforce service safety limits`.

## Phase 17: Documentation And Submission

### Task 48: Write The README And Required One-Page Design Note

**Files:**
- Create: `README.md`
- Create: `docs/design-note.md`
- Create: `docs/ai-usage.md`
- Test: `internal/doccheck/submission_test.go`

**Steps:**

1. Write a failing documentation test that checks required files, commands, result fields, and a bounded design-note size.
2. Run `go test -timeout 60s ./internal/doccheck`; expect failure.
3. Write README quick starts for embedded CLI, TUI, Docker, and full Compose modes.
4. Document dependencies and input/output formats.
5. Write a one-page design note covering semantic representation, why it exceeds flat extraction, decision logic, escalation boundaries, and next improvements.
6. State where AI tools assisted without disguising authorship.
7. Run `go test -timeout 60s ./internal/doccheck`; expect success.
8. Commit when requested: `docs: add candidate submission guide`.

### Task 49: Write Technical And Operational Documentation

**Files:**
- Create: `docs/architecture.md`
- Create: `docs/policy-language.md`
- Create: `docs/concurrency.md`
- Create: `docs/database.md`
- Create: `docs/api.md`
- Create: `docs/development.md`
- Create: `docs/operations.md`
- Modify: `docs/debugging.md`
- Modify: `docs/performance.md`
- Test: `internal/doccheck/links_test.go`

**Steps:**

1. Add a failing local-link and command-reference test.
2. Run `go test -timeout 60s ./internal/doccheck`; expect failure.
3. Write each document from executable configuration and measured behavior.
4. Mark PostgreSQL 19 as beta until the image moves to GA.
5. Include ownership diagrams, lock table, data layouts, API examples, migrations, recovery, Neovim DAP setup, and benchmark methodology.
6. Run `go test -timeout 60s ./internal/doccheck`; expect success.
7. Commit when requested: `docs: document production operation`.

### Task 50: Add CI And Final Release Verification

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/workflows/integration.yml`
- Create: `.github/workflows/release.yml`
- Create: `.goreleaser.yaml`
- Modify: `cmd/devx/cmd/status.go`

**Steps:**

1. Add CI jobs for normal runtime-dispatched SIMD, purego, 386 scalar fallback, unit, race, integration, generated-code drift, docs, and Docker.
2. Add a release matrix for Linux and macOS on amd64 and arm64 where supported.
3. Ensure every test/build command in CI has an explicit timeout.
4. Run `timeout 300s go run ./cmd/devx test`; expect success.
5. Run `timeout 300s go run ./cmd/devx build`; expect success.
6. Run `timeout 300s go run ./cmd/devx policy:check`; expect success.
7. Run `timeout 300s go run ./cmd/devx results:check`; expect success.
8. Run `timeout 300s go run ./cmd/devx proto:check`; expect success.
9. Run `timeout 600s go run ./cmd/devx docker:build`; expect success.
10. Run `git diff --check`; expect no whitespace errors.
11. Inspect `git status --short` and confirm only intended files changed.
12. Commit when requested: `ci: verify verifoxx release`.

### Task 51: Audit And Consolidate Reusable Core Paths

**Files:**
- Modify: production and test files identified by the audit
- Modify: `docs/performance.md`
- Modify: `docs/plans/2026-08-20-verifoxx-policy-engine-design.md`

**Steps:**

1. Inventory repeated parsing, validation, sizing, CSR, provenance, formatting, and adapter logic after Tasks 1-50 are complete.
2. Distinguish semantic duplication from intentionally specialized hot paths; do not introduce abstraction into per-row or per-element kernels without measurement.
3. Consolidate only behaviorally identical paths behind allocation-free helpers or shared immutable data.
4. Add regression tests before every behavior-affecting consolidation.
5. Run focused `-benchmem` and interleaved before/after benchmarks for every hot-path change; delete any cleanup that regresses the measured path.
6. Audit production struct layout with the pinned field-alignment analyzer and review GC pointer-scan bytes and cache locality.
7. Run native, purego, 386, race/checkptr, vet, generated-code, docs, integration, and full release gates with explicit timeouts.
8. Record retained specializations and their measured rationale in `docs/performance.md`.
9. Commit when requested: `refactor: consolidate reusable core paths`.

## Final Acceptance Gate

Before calling the system complete, gather fresh output for all of these commands:

```bash
go test -timeout 180s ./...
go test -timeout 180s -race ./internal/... ./cmd/...
GOARCH=386 go test -timeout 180s ./...
go test -timeout 180s -tags=purego ./...
go test -timeout 300s -tags=integration ./...
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 600s go run ./cmd/devx docker:build
git diff --check
```

Then verify manually:

- `results/requests.json` contains all required fields for R1 through R5.
- The evaluator contains no request-ID branch.
- `go test -benchmem` reports zero steady-state evaluation allocations.
- Runtime diagnostics identify the selected SIMD tier.
- Single-worker, multi-worker, SIMD, scalar, and debug outcomes match.
- PostgreSQL 19 can query the persisted policy graph with SQL/PGQ.
- The TUI steps through nodes and displays masks, evidence, outcomes, and source spans.
- Neovim connects to the debug worker through Delve DAP while the TUI runs separately.
- `devx doctor`, `devx demo`, and `devx full` work from documented commands.
- The one-page design note remains within the assignment limit.

## Phase 18: Post-Submission OSS Compatibility Frontends

### Task 52: Add CEL, Rego, Cedar, And Protobuf Compatibility Frontends

**Scope:**

This is a post-submission expansion task, not part of the bounded assignment
deliverable. Treat each source language as a compiler frontend over the shared
Verifoxx semantic IR. Do not claim drop-in compatibility or a fixed speedup
until upstream conformance suites and controlled benchmarks prove both.

**Files:**
- Create: `frontend/frontend.go`
- Create: `frontend/diagnostic.go`
- Create: `frontend/cel/parser.go`
- Create: `frontend/cel/lower.go`
- Create: `frontend/rego/parser.go`
- Create: `frontend/rego/lower.go`
- Create: `frontend/cedar/parser.go`
- Create: `frontend/cedar/lower.go`
- Create: `frontend/proto/options.proto`
- Create: `frontend/proto/plugin.go`
- Create: `internal/frontend/semantic.go`
- Create: `internal/frontend/lower.go`
- Create: `cmd/protoc-gen-verifoxx/main.go`
- Test: `frontend/cel/cel_test.go`
- Test: `frontend/rego/rego_test.go`
- Test: `frontend/cedar/cedar_test.go`
- Test: `frontend/proto/plugin_test.go`
- Test: `internal/frontend/conformance_test.go`
- Benchmark: `internal/frontend/benchmark_test.go`
- Create: `testdata/frontends/cel/`
- Create: `testdata/frontends/rego/`
- Create: `testdata/frontends/cedar/`
- Create: `testdata/frontends/proto/`
- Document: `docs/frontends.md`

**Steps:**

1. Write a language-by-language capability matrix before implementation. Mark
   every CEL macro/function/type, Rego rule/data/comprehension feature, Cedar
   entity/action/resource construct, and Protobuf option shape as supported,
   lowered with restrictions, or rejected. Full-language claims require full
   upstream conformance.
2. Define one bounded public frontend contract for source bytes, schema and
   environment bindings, diagnostics with exact source spans, capability
   reporting, and lowering into the shared semantic representation. Keep
   parser-owned objects out of the evaluator and registry APIs.
3. Add failing differential tests against the official CEL, OPA/Rego, and Cedar
   evaluators for every supported construct. Include true, false, unknown,
   error, missing-data, type-error, and policy-conflict cases; preserve each
   language's semantics rather than translating only convenient syntax.
4. Implement the CEL frontend first. Parse standard CEL source, bind a declared
   environment, lower the supported typed expression graph into canonical
   Verifoxx nodes, and emit explicit diagnostics for unsupported dynamic
   dispatch, macros, functions, or aggregate semantics.
5. Implement the Rego frontend second. Parse modules and data references, lower
   only the capability-matrix subset, preserve undefined/error behavior, and
   reject unsupported recursion, comprehensions, mutation-like built-ins, or
   object/set semantics rather than silently changing their meaning.
6. Implement the Cedar frontend third. Preserve permit/forbid precedence,
   principal/action/resource typing, context conditions, entity references, and
   authorization errors. Extend the shared semantic IR only where a reusable
   authorization concept cannot be represented by existing clauses and
   outcomes.
7. Implement the Protobuf frontend as a deterministic `protoc` plugin over
   custom options. Generate bounded policy/schema bindings at build time; keep
   protobuf reflection and descriptor traversal out of runtime evaluation.
8. Retain a single canonical lowering and optimization pipeline after frontend
   parsing. Frontends may normalize syntax but must not fork evaluator kernels,
   create per-row maps, or introduce reflection, interface dispatch, database
   calls, or allocation into per-node/per-row execution.
9. Add corpus, malformed-input, depth/size-limit, Unicode, source-span,
   duplicate-definition, and fuzz tests for every parser. Pin upstream grammar
   and conformance-corpus revisions and check generated-code drift.
10. Benchmark parse, semantic analysis, lowering, cold compilation, warm scalar
    evaluation, SIMD evaluation, and parallel batch evaluation separately.
    Compare equivalent policies and data against the official engines with
    interleaved runs, `benchstat`, fixed hardware metadata, and `-benchmem`.
11. Publish performance claims only for measured supported subsets. Report
    policy shape, row count, data representation, setup excluded/included,
    allocations, latency distributions, throughput, SIMD tier, and unsupported
    semantics beside every comparison.
12. Add CLI/API format selection and automatic detection only after explicit
    format commands pass conformance. Existing native-policy behavior and
    machine-readable results must remain byte-identical.
13. Run native, purego, 386, race/checkptr, fuzz, vet, field-alignment,
    generated-code, frontend conformance, integration, Docker, and benchmark
    gates with explicit outer and test timeouts.
14. Commit when requested: `feat: add policy language compatibility frontends`.
