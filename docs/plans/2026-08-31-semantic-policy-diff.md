# Semantic Policy Diff Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add deterministic semantic policy comparison that reports bounded equivalence or classified change, emits replayable counterexamples, supports CI regression rules, and returns `Inconclusive` whenever concrete proof preconditions are not met.

**Architecture:** Public `policy/diff` types alias an internal pointerless/owned contract so external callers never depend on evaluator internals. Native policy sources compile independently into immutable Programs; an identical-semantic-slab fast path avoids search, while a deterministic mixed-radix generator fills reusable old/new SoA batches for complete finite-domain evaluation. Optional proof providers may propose claims or witnesses, but every witness is replayed and only native exhaustive coverage of a closed domain may publish `Equivalent`.

**Tech Stack:** Go 1.27, existing JSON policy decoder/compiler, immutable Program tables, evaluator SoA Builder/Executor, caller-buffer JSON/text renderers, Cobra CLI, fuzzing, `-benchmem`, race/checkptr, 386, pure-Go, pinned `fieldalignment`.

---

## Proof And Ownership Rules

- Outcomes are exactly `Equivalent`, `Widened`, `Narrowed`, `Changed`, and `Inconclusive`.
- Decision rows are exactly `Approve`, `Reject`, `Revise`, and `Escalate`; a policy with a different outcome catalog is unsupported and yields `Inconclusive`.
- A caller supplies all 16 old/new decision transition classes and whether each transition is allowed. NornRune does not impose a universal decision ordering.
- One `FieldDomain` binds by canonical field name and kind. String dimensions must be explicitly `Closed`; an open string domain can find a change but cannot prove equivalence.
- Every referenced field must have a domain, every field domain must include Missing, and evidence-using Programs require at least one closed evidence-set dimension. Missing dimensions make a no-difference result `Inconclusive`.
- One `EvidenceSet` is an owned scenario containing zero or more typed evidence records. Scenarios cover absent, current, stale, unclear, and conflicting records without adding special evaluator semantics.
- Candidate cardinality is the checked product of field-option counts and evidence-set count. Overflow, zero dimensions, `MaxCandidates` exhaustion, invalid `BatchRows`, cancellation, or evaluator errors are inconclusive/error boundaries, never equivalence.
- Search order is deterministic: dimensions referenced by changed instructions first, then remaining referenced dimensions, then canonical field name. No referenced dimension is dropped from complete proof.
- Programs receive separate Builders because symbol IDs are Program-local. Candidate values are copied into each reusable batch by kind-local columns; no maps, reflection, or per-row strings enter evaluator execution.
- The first differing candidate in mixed-radix order is owned by the result. Classification still scans the complete allowed budget so mixed widening/narrowing or forbidden transitions become `Changed`/forbidden correctly.
- A counterexample records field/evidence input, old/new decisions, reason IDs, requirement IDs, driver clause/node IDs, evidence IDs/states, remediation IDs, explanation IDs, and source spans. It contains no borrowed Program, Builder, Executor, or source slices.
- A proof-provider witness is replayed through both concrete Programs. A mismatch returns `Inconclusive`. Provider equivalence claims are advisory; the concrete finite-domain engine still exhausts the domain before returning `Equivalent`.

### Task 1: Define Stable Public Types And Domain Validation

**Files:**
- Create: `internal/diff/types.go`
- Create: `internal/diff/domain.go`
- Create: `policy/diff/diff.go`
- Create: `policy/diff/result.go`
- Create: `policy/diff/domain.go`
- Create: `policy/diff/counterexample.go`
- Test: `policy/diff/diff_test.go`
- Test: `internal/diff/domain_test.go`

**Step 1: Write failing enum and matrix tests**

Require append-stable values, text/JSON names, all 16 matrix entries, owned cloning, invalid zero values, duplicate fields, wrong kinds, absent Missing options, open strings, empty evidence scenarios, candidate-product overflow, invalid limits, and deterministic validation diagnostics.

Core contract:

```go
type Outcome uint8
const (
    OutcomeInvalid Outcome = iota
    Equivalent
    Widened
    Narrowed
    Changed
    Inconclusive
)

type Decision uint8
const (
    DecisionInvalid Decision = iota
    Approve
    Reject
    Revise
    Escalate
)

type Transition struct {
    Class   Outcome
    Allowed bool
}

type RiskMatrix struct { Transitions [16]Transition }

type ValueState uint8
const (
    ValueStateInvalid ValueState = iota
    ValueStateMissing
    ValueStatePresent
)

type Value struct {
    String    string
    Integer   int64
    Timestamp int64
    State     ValueState
    Kind      frontend.ValueKind
    Boolean   bool
}

type FieldDomain struct {
    Name   string
    Values []Value
    Kind   frontend.ValueKind
    Closed bool
}

type Evidence struct {
    Kind, State, Subject, Scope, Timing string
}

type EvidenceSet struct { Records []Evidence }

type Domain struct {
    Fields        []FieldDomain
    EvidenceSets  []EvidenceSet
    MaxCandidates uint64
    BatchRows     uint32
}
```

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s ./policy/diff ./internal/diff`

Expected: FAIL because packages and types do not exist.

**Step 3: Implement minimal pointer-conscious types**

Keep pointer-bearing fields first and scalar tails last. Implement `Valid`, `String`, text marshaling, checked cardinality, stable field lookup by bounded linear scan, deep clone helpers, and sentinel errors. Do not add maps or `sync.Pool`.

**Step 4: Run GREEN and layout gate**

Run: `timeout 120s go test -count=1 -timeout 90s ./policy/diff ./internal/diff`

Run: `timeout 300s ./scripts/check-fieldalignment.sh`

Expected: PASS; analyzer prints nothing.

### Task 2: Compile Canonical Native Policies And Detect Semantic Identity

**Files:**
- Create: `internal/diff/compile.go`
- Create: `internal/diff/identity.go`
- Test: `internal/diff/identity_test.go`
- Create: `testdata/diff/equivalent-old.json`
- Create: `testdata/diff/equivalent-new.json`

**Step 1: Write failing compilation and identity tests**

Cover malformed source, schema mismatch, nonstandard outcome catalogs, borrowed input mutation, formatting-only source changes, policy-name/version changes, source-span changes, identical executable/result slabs, changed instructions, changed resolutions, changed remediation, and changed evidence catalogs.

Require:

```go
type Analyzer struct {
    oldDecoder, newDecoder jsonpolicy.Decoder
    oldAST, newAST         ast.Builder
    oldCompiler, newCompiler compile.Lowerer
    // reusable search and output scratch follows
}

func (a *Analyzer) Compare(
    ctx context.Context,
    dst *Result,
    oldSource, newSource []byte,
    fields FieldSchema,
    domain Domain,
    matrix RiskMatrix,
    prover Prover,
) error
```

`FieldSchema` owns canonical field names, kinds, and groups and builds the existing schema/interner without exposing internal IDs publicly.

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s -run 'Test(CompilePair|SemanticIdentity)' ./internal/diff`

Expected: FAIL with missing analyzer/identity functions.

**Step 3: Implement canonical compile and slab comparison**

Decode and lower each source independently. Compare executable, value, symbol, field, requirement, clause, resolution, outcome, remediation, explanation, and evidence-catalog slabs while intentionally ignoring retained source bytes, content hash, policy name/version, and source-span-only columns. Validate exact four-decision catalog names before search.

**Step 4: Run GREEN**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/diff ./policy/diff`

Expected: PASS; formatting-only equivalent sources take the no-generation path.

### Task 3: Build Deterministic Dependency And Mixed-Radix Search Plans

**Files:**
- Create: `internal/diff/prune.go`
- Create: `internal/diff/search.go`
- Test: `internal/diff/search_test.go`
- Test: `internal/diff/exhaustive_test.go`

**Step 1: Write failing search-plan tests**

Cover changed-field-first order, unchanged referenced fields retained, unreferenced fields pruned, evidence dependencies, result-table-only changes forcing all dependencies, canonical name tie-breaking, checked cardinality, batch tails at 1/63/64/65 rows, budget exactly reached/exceeded, cancellation, deterministic enumeration, and no duplicate candidates.

Mixed-radix state uses parallel numeric columns:

```go
type searchPlan struct {
    fieldRows     []uint32
    optionCounts  []uint32
    strides       []uint64
    changed       []uint8
    cardinality   uint64
    evidenceCount uint32
}
```

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s -run 'Test(Search|Dependency|ExhaustiveOrder)' ./internal/diff`

Expected: FAIL because planning/generation is absent.

**Step 3: Implement checked deterministic planning**

Compare instruction identities without hashing allocations. Mark field/evidence operands reached from changed roots; if output/resolution semantics differ, conservatively mark all referenced dependencies. Generate option indexes by division/modulo into caller-sized row slabs and advance once per batch.

**Step 4: Run GREEN**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/diff`

Expected: PASS with exact candidate order and cardinality.

### Task 4: Materialize Reusable Old/New SoA Batches

**Files:**
- Create: `internal/diff/batch.go`
- Test: `internal/diff/batch_test.go`
- Benchmark: `internal/diff/benchmark_test.go`

**Step 1: Write failing batch tests**

Cover all field kinds, Missing, empty strings, extension symbols, integer/timestamp boundaries, booleans, distinct Program symbol IDs, zero/many evidence records, evidence CSR, stale/unclear/conflicting scenarios, poisoned Builder reuse, batch tails, atomic errors, and warm allocation count after priming.

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s -run 'TestCandidateBatch' ./internal/diff`

Expected: FAIL because candidate materialization is missing.

**Step 3: Implement separate reusable Builders**

Pre-count evidence rows/refs for each generated batch. Call `Begin` once per Program, intern symbols into that Program's extension table, write typed columns, complete CSR once, and call `Finish`. Do not allocate per row or convert bytes to strings in the fill loop.

**Step 4: Verify behavior and allocations**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/diff`

Run: `timeout 120s go test -timeout 90s -run '^$' -bench 'BenchmarkCandidateBatch' -benchmem -count=6 ./internal/diff`

Expected: tests pass; primed candidate fill reports `0 B/op`, `0 allocs/op`.

### Task 5: Compare Bulk Results And Own Counterexamples

**Files:**
- Create: `internal/diff/compare.go`
- Create: `internal/diff/counterexample.go`
- Test: `internal/diff/compare_test.go`
- Test: `internal/diff/exhaustive_test.go`
- Create: `testdata/diff/widened-old.json`
- Create: `testdata/diff/widened-new.json`
- Create: `testdata/diff/narrowed-old.json`
- Create: `testdata/diff/narrowed-new.json`

**Step 1: Write failing semantic comparison tests**

Require exact detection of Equivalent, Widened, Narrowed, mixed Changed, non-order Changed, and Inconclusive. Cover every 4x4 decision transition, caller matrix inversion, allowed/forbidden aggregation, reasons-only changes, applied requirement changes, evidence/provenance changes, remediation changes, explanations, source spans, smallest witness, deterministic reruns, destination reset, destination unchanged on infrastructure error, and source/domain mutation after return.

**Step 2: Run RED**

Run: `timeout 150s go test -count=1 -timeout 120s -run 'TestCompare' ./internal/diff ./policy/diff`

Expected: FAIL because result comparison/counterexample ownership is missing.

**Step 3: Implement bulk evaluate and classification**

Execute old/new batches with separate reusable Executors and result batches. Compare fixed-width outcome rows first, then CSR ranges only when needed. Translate outcome catalog names to the four stable decisions. Aggregate all observed transition classes and forbidden bits; copy the first differing input and result provenance into owned slabs.

**Step 4: Run GREEN plus symmetry tests**

Run: `timeout 150s go test -count=1 -timeout 120s ./internal/diff ./policy/diff`

Expected: PASS; swapping old/new and transposing the matrix produces the expected inverse class and witness decisions.

### Task 6: Add Optional Proof Providers With Mandatory Replay

**Files:**
- Create: `internal/diff/prover.go`
- Create: `policy/diff/prover.go`
- Test: `internal/diff/prover_test.go`

**Step 1: Write failing provider tests**

Cover nil provider, deterministic exhaustive provider, valid proposed witness, fabricated witness, mismatched decisions, unsupported claim, timeout, cancellation, provider panic containment decision, and advisory equivalence followed by concrete exhaustion.

Contract:

```go
type ProofClaim uint8
const (
    ProofClaimInvalid ProofClaim = iota
    ProofClaimEquivalent
    ProofClaimChanged
    ProofClaimInconclusive
)

type Proof struct {
    Witness Candidate
    Claim   ProofClaim
}

type Prover interface {
    Prove(context.Context, ProofRequest) (Proof, error)
}
```

`ProofRequest` owns source and domain snapshots; it exposes no mutable Program or evaluator state.

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s -run 'TestProver' ./internal/diff ./policy/diff`

Expected: FAIL because provider contracts/replay are absent.

**Step 3: Implement replay boundary**

Call providers only on the cold path under caller context. Replay changed witnesses concretely before using them. Treat provider errors, unsupported claims, invalid witnesses, and disagreement as Inconclusive uncertainty. Always exhaust the finite domain before publishing provider-suggested equivalence.

**Step 4: Run GREEN and race**

Run: `timeout 180s go test -count=1 -timeout 150s -race ./internal/diff ./policy/diff`

Expected: PASS with no provider retaining analyzer-owned memory.

### Task 7: Add Regression Rules And Expiring Exceptions

**Files:**
- Create: `policy/diff/regression.go`
- Create: `internal/diff/regression.go`
- Test: `internal/diff/regression_test.go`
- Create: `testdata/diff/exceptions/allowed-revision.json`

**Step 1: Write failing CI-rule tests**

Cover forbidden approval widening, allowed bounded revision, expected escalation transition, exact old/new source digests, exception ID/reason/owner, UTC expiry, stale exception, wrong witness digest, wrong transition, malformed/trailing JSON, deterministic result, and no wall-clock reads inside comparison.

**Step 2: Run RED**

Run: `timeout 120s go test -count=1 -timeout 90s -run 'TestRegression' ./internal/diff ./policy/diff`

Expected: FAIL because regression rules do not exist.

**Step 3: Implement explicit-time exception matching**

Accept `now time.Time` from the adapter, normalize UTC, verify exact policy/counterexample digests and transition, and never let an exception turn Inconclusive into allowed. Keep parser/JSON work outside search/evaluator loops.

**Step 4: Run GREEN**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/diff ./policy/diff`

Expected: PASS with deterministic expiry behavior.

### Task 8: Add JSON And Human-Readable CLI Output

**Files:**
- Create: `internal/adapters/jsondiff/decode.go`
- Create: `internal/adapters/jsondiff/encode.go`
- Create: `internal/adapters/jsondiff/decode_test.go`
- Create: `internal/adapters/jsondiff/encode_test.go`
- Create: `internal/adapters/cli/diff.go`
- Modify: `internal/adapters/cli/root.go`
- Test: `internal/adapters/cli/diff_test.go`
- Modify: `cmd/nornrune/main_test.go`

**Step 1: Write failing adapter tests**

Require `nornrune diff --old-policy --new-policy --domain [--format json|text] [--exceptions]`; strict bounded JSON; one stdin reader; no auto-detection; stable field order; escaped strings; complete witness/provenance; context cancellation; write failures; and exit codes: `0` equivalent/allowed, `3` forbidden regression, `4` inconclusive, `1` operational, `2` usage.

**Step 2: Run RED**

Run: `timeout 150s go test -count=1 -timeout 120s ./internal/adapters/jsondiff ./internal/adapters/cli ./cmd/nornrune`

Expected: FAIL because adapters and command are absent.

**Step 3: Implement bounded adapters**

Use strict hand-written or streaming JSON decoding with duplicate/trailing-field rejection and configured source/domain limits. Render JSON/text into caller-owned byte buffers. Keep file I/O, time, JSON parsing, and text materialization in adapters.

**Step 4: Run GREEN and executable smoke tests**

Run: `timeout 150s go test -count=1 -timeout 120s ./internal/adapters/jsondiff ./internal/adapters/cli ./cmd/nornrune`

Expected: PASS with stable output and exit codes.

### Task 9: Exhaustive Oracles, Fuzzing, And Mutation Coverage

**Files:**
- Test: `internal/diff/exhaustive_test.go`
- Create: `internal/diff/fuzz_test.go`
- Create: `policy/diff/fuzz_test.go`
- Create: `testdata/diff/native-frontend-equivalent.json`

**Step 1: Build an independent tiny-policy oracle**

Enumerate generated Boolean policies over present/missing facts and evidence states. Compare analyzer output to direct row-by-row evaluator enumeration. Cover all four truth states, all four decisions, mutation of each opcode/value/resolution/remediation/evidence state, symmetry, native/frontend-equivalent Programs, stale evidence, conflict, budget tails, and deterministic witness order.

**Step 2: Add fuzz seeds and invariants**

Fuzz domain validation, source pairs, matrix entries, budgets, cancellation points, and malformed provider witnesses. Invariants: no panic, valid outcome, Equivalent implies complete concrete coverage, witness replay differs, swapping pair/matrix is symmetric, and output never borrows fuzz input.

**Step 3: Run bounded fuzz campaigns**

Run: `timeout 90s go test -count=1 -timeout 60s -run '^$' -fuzz '^FuzzDomain$' -fuzztime=10s ./internal/diff`

Run: `timeout 90s go test -count=1 -timeout 60s -run '^$' -fuzz '^FuzzCompare$' -fuzztime=10s ./policy/diff`

Expected: PASS with no persisted fuzz artifacts.

### Task 10: Benchmark And Document The Exact Proof Boundary

**Files:**
- Modify: `internal/diff/benchmark_test.go`
- Create: `docs/policy-diff.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/performance.md`
- Modify: `internal/doccheck/links_test.go`
- Create: `internal/doccheck/diff_test.go`

**Step 1: Write failing documentation-contract tests**

Require finite/closed domain, transition matrix, Inconclusive boundaries, counterexample contents, native replay, optional-provider limits, exact CLI exit codes, exception expiry, cold search vs warm evaluator allocation distinction, and no unbounded/SMT proof claim.

**Step 2: Run RED**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/doccheck`

Expected: FAIL because the guide is absent.

**Step 3: Add stage-separated benchmarks and docs**

Benchmark identical fast path, candidate planning, batch fill, 64/1K/64K candidate searches, first/last witness, evidence scenarios, and budget exhaustion. Report domain cardinality, field/evidence shape, rows per batch, SIMD tier, setup cost, B/op, allocs/op, and hardware; do not call finite-domain results universal proofs.

**Step 4: Run benchmark/doc gates**

Run: `timeout 180s go test -count=1 -timeout 150s ./internal/doccheck ./internal/diff ./policy/diff ./internal/adapters/jsondiff ./internal/adapters/cli`

Run: `timeout 180s go test -timeout 150s -run '^$' -bench 'Benchmark(Diff|Candidate)' -benchmem -count=6 ./internal/diff`

Run: `timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor' -benchmem -count=6 ./internal/eval`

Expected: docs/tests pass; warm evaluator remains `0 B/op`, `0 allocs/op`.

### Task 11: Review, Verify, Commit, And Merge Task 55

**Files:**
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine.md`
- Review: all Task 55 files

**Step 1: Run read-only security/correctness review**

Review false-equivalence paths, open-domain handling, product overflow, budget off-by-one, cancellation, malformed Programs, outcome-name mapping, risk aggregation, witness replay/ownership, exception expiry, JSON injection, CLI exit codes, allocation boundaries, and unchanged evaluator kernels. Fix confirmed Critical/High/Medium findings with RED/GREEN tests.

**Step 2: Run complete bounded matrix**

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s go test -count=1 -tags=integration -timeout 360s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 30s git diff --check
```

Expected: PASS with no generated/build artifacts in the worktree and baseline R1-R5 unchanged.

**Step 3: Mark Task 55 complete**

Add `**Status:** Complete (2026-08-31)` beneath the Task 55 heading only after the matrix passes.

**Step 4: Commit when requested**

Inspect status, diff, and recent log; stage exact Task 55 files and commit:

```bash
git commit -m "feat: add semantic policy regression analysis"
```

Do not amend, skip hooks, or include generated output.
