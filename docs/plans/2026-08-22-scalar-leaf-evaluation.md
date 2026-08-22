# Scalar Leaf Evaluation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Evaluate compiled fact and evidence leaves over request batches into four-valued truth and reason bitplanes without warm-path allocation.

**Architecture:** Add non-owning reason-plane views and direct scalar SoA kernels in `internal/eval`. Fact kernels resolve existing Program fields and constants once per leaf; evidence kernels reduce each request's CSR range using a reusable Program-bound state classifier and a fixed-width optional-constraint query.

**Tech Stack:** Go 1.27, existing `program.Program`, `eval.Batch`, `truth.Planes`, typed schema IDs, table-driven tests, Go benchmarks.

---

### Task 1: Define Non-Owning Reason Output Views

**Files:**
- Create: `internal/eval/scalar.go`
- Create: `internal/eval/predicate_test.go`

**Step 1: Write failing reason-view tests**

Add tests for rows 0, 1, 63, 64, and 65. Allocate exactly
`truth.ReasonCount * truth.WordCount(rows)` words, verify every one-based reason
selects its own contiguous range, and verify zero/out-of-range reasons or wrong
storage lengths panic before mutation.

Use this intended shape:

```go
type ReasonPlanes struct {
    Words []uint64
}

func (p ReasonPlanes) Plane(reason schema.ReasonID, rows uint32) []uint64
```

Add a poisoned-output test for the package-private leaf reset helper. It must
clear both truth planes and all reason words, including dirty tail bits.

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestReasonPlanes|TestResetLeafOutputs' -count=1
```

Expected: FAIL because `ReasonPlanes` and the reset helper do not exist.

**Step 3: Implement the minimal view and shared scalar helpers**

In `internal/eval/scalar.go`:

- Define `ReasonPlanes` as one slice header only.
- Validate exact truth and reason lengths before clearing either destination.
- Compute all products in `uint64` before converting to `int`.
- Keep a package-private unchecked reason-range helper for kernels after one
  shape check.
- Add a final-word mask helper that returns all ones for complete words and the
  low `rows&63` bits otherwise.
- Panic with static messages for evaluator defects; do not format errors.

Do not add ownership, constructors that allocate, maps, callbacks, or a row
object.

**Step 4: Run the focused test and verify GREEN**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestReasonPlanes|TestResetLeafOutputs' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/scalar.go internal/eval/predicate_test.go
git commit -m "feat: add evaluator reason planes"
```

### Task 2: Evaluate Presence And Equality Leaves

**Files:**
- Create: `internal/eval/predicate.go`
- Modify: `internal/eval/predicate_test.go`

**Step 1: Write failing `Exists` and `Equal` tests**

Build a compact immutable Program fixture with symbol, integer, Boolean,
timestamp, and presence fields plus canonical literal payloads. Build batches
through `eval.Builder`, not by hand, so field-column and presence layouts match
production.

For every legal equality kind, assert rows for:

- present and equal: True `(1,0)`;
- present and unequal: False `(0,1)`;
- missing: Unknown `(0,0)` plus `ReasonMissing`;
- present zero/false: treated as present values, not missing.

For `Exists`, assert present is True and missing is Unknown plus
`ReasonMissing`. Include rows 0 and 65 to lock empty and partial-tail behavior.

Use this package-private entry point:

```go
func evalPredicate(
    dst truth.Planes,
    reasons ReasonPlanes,
    batch Batch,
    p *program.Program,
    instruction schema.InstructionID,
)
```

**Step 2: Run the focused tests and verify RED**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalPredicate(Exists|Equal)' -count=1
go test -timeout 60s ./internal/eval -count=1
```

Expected: FAIL because `evalPredicate` does not exist.

**Step 3: Implement equality and presence kernels**

Implement one instruction-level dispatch, then kind-specific loops:

- Resolve `FieldID -> (ValueKind, column)` once through `Program.FieldIndex`.
- Resolve the Program literal once before the row loop.
- Read the field's presence words from `(field-1)*wordCount`.
- For symbols, integers, and timestamps, scan contiguous `column*rows` values
  and build one match word at a time.
- For Booleans, compare complete bitmap words.
- For `Exists`, copy the presence mask into Positive and leave Negative zero.
- Set Negative only for present mismatches.
- Put absent bits only in the Missing reason plane.
- Mask every final output word.

Validate the instruction row, opcode operands, selected field/value columns,
and output shapes before writing. Symbol comparison is numeric `SymbolID`
comparison; never resolve bytes in the row loop.

**Step 4: Run focused and package tests**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalPredicate(Exists|Equal)' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/predicate.go internal/eval/predicate_test.go
git commit -m "feat: evaluate presence and equality leaves"
```

### Task 3: Complete Scalar Comparison Operators

**Files:**
- Modify: `internal/eval/predicate.go`
- Modify: `internal/eval/predicate_test.go`

**Step 1: Write failing operator-table tests**

Add table-driven cases for:

- `NotEqual` over symbol, integer, Boolean, and timestamp fields.
- `Less`, `LessEqual`, `Greater`, and `GreaterEqual` over integer and timestamp
  fields, including `math.MinInt64`, zero, and `math.MaxInt64`.
- `In` over every literal-bearing kind with zero, one, duplicate, and several
  values.
- Missing rows for every opcode.
- Malformed Program list ranges, mixed list kinds, bad payload references, and
  illegal opcode/kind combinations panicking before output mutation.

Use one expected row-state helper in tests; do not duplicate bit arithmetic in
every case.

**Step 2: Run and verify RED**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalPredicate(NotEqual|Ordered|In|RejectsMalformed)' -count=1
```

Expected: FAIL on unimplemented operators.

**Step 3: Implement the remaining scalar operators**

- Share typed Program value decoding with equality.
- For `In`, validate the complete Program-owned literal range before clearing
  output, then scan the small literal range without temporary slices or maps.
- Invert equality only inside the present mask for `NotEqual`.
- Keep ordered comparisons restricted to integer and timestamp kinds.
- Do not use a per-row function value, interface, or closure.

**Step 4: Run focused and package tests**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalPredicate' -count=1
go test -timeout 60s ./internal/eval -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/predicate.go internal/eval/predicate_test.go
git commit -m "feat: evaluate scalar comparison leaves"
```

### Task 4: Bind Evidence State Semantics Once

**Files:**
- Create: `internal/eval/evidence_eval.go`
- Create: `internal/eval/evidence_eval_test.go`

**Step 1: Write failing state-index tests**

Construct Program catalogs in arbitrary order and assert exact classifications:

```text
stale         -> ReasonStale
unclear       -> ReasonUnclear
unverifiable  -> ReasonUnverifiable
invalid       -> ReasonInvalid
conflict      -> ReasonConflict
conflicting   -> ReasonConflict
all others    -> zero (resolved)
```

Test nil Programs, malformed symbol ranges, rebinding a different Program with
fewer states, repeated binding of the same immutable Program, and failed-bind
atomicity. Poison spare capacity before rebinding so stale classifications are
observable.

Use this owner:

```go
type EvidenceStateIndex struct {
    reasons []schema.ReasonID
    program *program.Program
}

func (i *EvidenceStateIndex) Bind(p *program.Program) error
```

**Step 2: Run and verify RED**

Run:

```bash
go test -timeout 60s ./internal/eval -run TestEvidenceStateIndex -count=1
go test -timeout 60s ./internal/eval -count=1
```

Expected: FAIL because the index does not exist.

**Step 3: Implement reusable cold binding**

- Add a static `ErrInvalidEvidenceProgram` sentinel.
- First validate every state-name SymbolID and symbol range without mutating the
  receiver.
- Resize and clear the reason slice only after validation succeeds.
- Classify by `bytes.Equal` against package-level byte constants; never convert
  catalog bytes to strings.
- Cache the immutable Program pointer and return immediately on same-pointer
  rebinding.
- Preserve the prior index after any failed bind.

This operation may allocate when first seeing a larger catalog. Repeated
evaluation against a bound Program must not allocate.

**Step 4: Run focused and package tests**

Run:

```bash
go test -timeout 60s ./internal/eval -run TestEvidenceStateIndex -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/evidence_eval.go internal/eval/evidence_eval_test.go
git commit -m "feat: bind evidence state semantics"
```

### Task 5: Reduce Request Evidence Through CSR

**Files:**
- Modify: `internal/eval/evidence_eval.go`
- Modify: `internal/eval/evidence_eval_test.go`

**Step 1: Write failing evidence-reduction tests**

Define the fixed query:

```go
type EvidencePredicate struct {
    Kind    schema.EvidenceKindID
    State   schema.EvidenceStateID
    Subject schema.SymbolID
    Scope   schema.SymbolID
    Timing  schema.SymbolID
}
```

Add exact tests for:

- no referenced record of the kind: Unknown plus Missing;
- exact kind/state: True;
- resolved wrong state: False;
- stale, unclear, unverifiable, and invalid states: Unknown plus their reason;
- conflict state: Conflict plus Conflict reason;
- wrong subject, scope, and timing, including absent optional attributes;
- zero query constraints ignoring optional evidence attributes;
- valid plus resolved-wrong records: Conflict;
- valid plus stale records: True with stale sideband retained;
- records of irrelevant kinds and arbitrary CSR/reference order;
- rows 0, 1, 63, 64, and 65 with poisoned outputs;
- malformed offsets, references, parallel evidence columns, unbound index, and
  zero/out-of-range query IDs panicking with static evaluator errors.

Use this package-private kernel:

```go
func evalEvidence(
    dst truth.Planes,
    reasons ReasonPlanes,
    batch Batch,
    p *program.Program,
    states *EvidenceStateIndex,
    predicate EvidencePredicate,
)
```

**Step 2: Run and verify RED**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalEvidence' -count=1
go test -timeout 60s ./internal/eval -count=1
```

Expected: FAIL because the reducer is absent.

**Step 3: Implement one-pass CSR reduction**

For each request row:

- Load its half-open CSR range.
- Scan references once and ignore other evidence kinds.
- Track positive, negative, found-kind, and accumulated reasons in fixed local
  scalars only.
- Compare optional subject/scope/timing constraints by `SymbolID`.
- Classify nonmatching states by numeric lookup in the bound state index.
- Treat out-of-range evidence states as invalid evaluator input.
- Set Conflict reason whenever both truth bits become active, including when
  separate records disagree.
- Set Missing only when no referenced record has the requested kind.

Write one row bit into destination planes after its complete CSR scan. Do not
stage records, sort references, deduplicate, or allocate per request.

**Step 4: Run focused and package tests**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestEvalEvidence' -count=1
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/evidence_eval.go internal/eval/evidence_eval_test.go
git commit -m "feat: evaluate request evidence leaves"
```

### Task 6: Prove Reuse And Scalar Baselines

**Files:**
- Create: `internal/eval/scalar_bench_test.go`
- Modify: `internal/eval/predicate_test.go`
- Modify: `internal/eval/evidence_eval_test.go`

**Step 1: Add failing reuse and allocation tests**

- Evaluate a larger batch, poison every output word, then evaluate a smaller
  partial-word batch and assert no stale truth, reasons, or tail bits survive.
- Use `testing.AllocsPerRun(1000, ...)` after all builders, Program metadata,
  state indexes, and output slabs are warm. Require exactly zero allocations for
  fact and evidence kernels.
- Keep assertion formatting outside measured closures.

**Step 2: Run the allocation tests**

Run:

```bash
go test -timeout 60s ./internal/eval -run 'TestLeafEvaluation(Reuse|Allocations)' -count=1
```

Expected: FAIL if any destination reset or lookup allocates or retains stale
bits.

**Step 3: Fix only measured allocation or reuse defects**

Reuse caller-owned slices and index capacity. Do not add `sync.Pool`, unsafe
code, or alternate fast paths.

**Step 4: Add and run scalar benchmarks**

Benchmark one representative 1,024-row integer predicate and one 1,024-row
evidence query with bounded CSR fanout. Call `b.ReportAllocs()` and exclude all
fixture construction from the timed loop.

Run:

```bash
go test -timeout 120s ./internal/eval -run '^$' -bench 'BenchmarkEval(Predicate|Evidence)$' -benchmem -count=1
```

Expected: both benchmarks report `0 B/op` and `0 allocs/op`. Record throughput
as a scalar baseline without adding a performance threshold.

**Step 5: Run package tests and commit**

Run:

```bash
go test -timeout 60s ./internal/eval -count=1
```

Expected: PASS.

```bash
git add internal/eval/scalar_bench_test.go internal/eval/predicate_test.go internal/eval/evidence_eval_test.go
git commit -m "test: prove scalar leaf evaluator reuse"
```

### Task 7: Run Cross-Cutting Gates And Review

**Files:**
- Modify only files required by confirmed findings.

**Step 1: Run focused portability and race gates**

```bash
go test -timeout 60s -race ./internal/eval -count=1
GOARCH=386 go test -timeout 60s ./internal/eval -count=1
```

Expected: PASS.

**Step 2: Run repository gates**

```bash
go test -timeout 60s ./... -count=1
timeout 60s go vet ./...
timeout 60s go build -o /dev/null ./cmd/verifoxx
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
```

Expected: every command exits zero and formatting/module commands print
nothing.

**Step 3: Audit layout and escapes**

```bash
timeout 60s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/eval
go test -timeout 60s -run '^$' -gcflags=all=-m=2 ./internal/eval
```

Expected: fieldalignment prints nothing. Inspect evaluator-owned production
types and confirm benchmarked leaf calls remain zero-allocation; error-path
objects and test harness escapes are not hot-path regressions.

**Step 4: Request read-only review**

Review the Task 15 design and implementation commit range. Focus on truth-table
semantics, Missing versus False, reason-plane layout, tail masking, value-ref
bounds, CSR corruption, evidence conflict reduction, stale index reuse, source
retention, 386 arithmetic, and warm allocation.

**Step 5: Fix confirmed findings with RED/GREEN tests**

For each Critical or Important finding, first add a focused failing regression,
run it with `go test -timeout 60s`, implement the smallest correction, and rerun
the focused and package suites. Commit one coherent correction set if needed.

**Step 6: Run final fresh verification**

Repeat the complete test, race, 386, vet, build, format, module, alignment,
escape, and benchmark matrix after the last code change. Confirm the worktree is
clean.
