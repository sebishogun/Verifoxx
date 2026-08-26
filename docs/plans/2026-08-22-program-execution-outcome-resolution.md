# Program Execution And Outcome Resolution Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Execute immutable compiled Programs over SoA request batches and produce deterministic, policy-owned result batches with zero warm allocations.

**Architecture:** A reusable scalar `eval.Executor` binds one Program, owns flat liveness-slotted truth/reason scratch, executes the topological instruction schedule by whole bitplanes, and resolves applicability plus clause roots row by row. A caller-owned `result.Batch` retains compact numeric provenance; a bound applicability query and one-time evidence validation keep static checks outside row and evidence-instruction loops.

**Tech Stack:** Go 1.27, existing `program.Program`, `truth.Planes`, scalar leaf kernels, immutable applicability indexes, CSR tables, and `testing.AllocsPerRun`/Go benchmarks.

---

### Task 1: Define Caller-Owned Result Batch Storage

**Files:**
- Create: `internal/result/batch.go`
- Test: `internal/result/batch_test.go`

**Step 1: Write the failing shape and reset tests**

Define tests that instantiate `result.Batch`, poison every active column, call
`Reset`, and assert:

- fixed `OutcomeIDs` has `rows` entries;
- each offset column has `rows+1` zero entries;
- every edge column has length zero while retaining capacity;
- switching 65 -> 3 -> 65 rows clears all active fixed and offset entries;
- nil receivers and lengths that cannot fit `int` return `ErrBatchTooLarge`
  without mutation.

The expected production shape is:

```go
type Batch struct {
    OutcomeIDs []schema.OutcomeID

    RequirementOffsets []uint32
    RequirementIDs     []schema.RequirementID

    DriverOffsets      []uint32
    DriverRequirements []schema.RequirementID
    DriverClauses      []schema.ClauseID
    DriverNodes        []schema.NodeID
    DriverReasons      []schema.ReasonID

    EvidenceOffsets []uint32
    EvidenceIDs     []schema.EvidenceID
    ReasonOffsets   []uint32
    ReasonIDs       []schema.ReasonID

    RemediationOffsets []uint32
    RemediationIDs     []schema.RemediationID

    Rows uint32
}
```

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestBatch'
```

Expected: FAIL because `Batch`, `ErrBatchTooLarge`, and `Reset` do not exist.

**Step 3: Implement the minimal reusable batch**

Add `ErrBatchTooLarge`, the struct above with pointer-bearing fields before the
scalar tail, and:

```go
func (b *Batch) Reset(rows uint32) error
```

Use a private generic resize-and-clear helper for fixed/offset columns. Set all
edge slices to `[:0]` without clearing retained capacity. Do not allocate any
per-row object and do not add lookup methods until a caller needs them.

**Step 4: Run focused result tests and static checks**

Run:

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestBatch'
timeout 60s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/result
```

Expected: PASS with no analyzer output.

**Step 5: Commit**

```bash
git add internal/result/batch.go internal/result/batch_test.go
git commit -m "feat: add reusable result batches"
```

### Task 2: Bind Applicability Queries Once

**Files:**
- Create: `internal/index/query.go`
- Modify: `internal/index/policy.go`
- Modify: `internal/index/policy_test.go`

**Step 1: Write failing bound-query tests**

Using `buildTestPolicy`, test:

```go
var query Query
if err := query.Bind(&policy); err != nil { t.Fatal(err) }
if err := query.Candidates(dst, values, present); err != nil { t.Fatal(err) }
```

Cover exact selectors, missing selectors, an absent symbol, empty policy, a
65-requirement tail, malformed value/presence/destination lengths, nil bind,
malformed immutable columns, atomic failed rebind, and zero allocations over
1,000 warm queries. Values and presence are in `policy.FieldIDs` order, so the
bound path accepts no field slice and performs no field binary search.

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestQuery'
```

Expected: FAIL because `Query` is undefined.

**Step 3: Implement static binding and the unchecked intersection core**

Add:

```go
type Query struct {
    policy *Policy
}

func (q *Query) Bind(p *Policy) error
func (q *Query) Candidates(dst []uint64, values []schema.SymbolID, present []uint8) error
```

`Bind` validates the Policy's static parallel columns, CSR ranges, mask widths,
tail mask, sorted field/value segments, and nonzero IDs before replacing the
old pointer. `Candidates` checks only the three dynamic lengths, copies
`AllMask`, and intersects each present field's exact value mask or wildcard
mask. Return early when the candidate mask becomes zero.

Refactor `Policy.Candidates` to retain its existing arbitrary-field API and
tests while sharing only the mask-intersection primitive where that reduces
duplication. Do not make an exported unchecked function.

**Step 4: Run index tests and allocation proof**

Run:

```bash
go test -count=1 -timeout 60s ./internal/index
```

Expected: PASS, including existing `Policy.Candidates` behavior and the new
warm zero-allocation test.

**Step 5: Commit**

```bash
git add internal/index/query.go internal/index/policy.go internal/index/policy_test.go
git commit -m "perf: bind applicability queries once"
```

### Task 3: Hoist Evidence Batch Validation

**Files:**
- Modify: `internal/eval/evidence_eval.go`
- Modify: `internal/eval/evidence_eval_test.go`
- Modify: `internal/eval/scalar_bench_test.go`

**Step 1: Write the failing validated-kernel tests**

Add tests that call the direct wrapper and the executor-facing validated path:

- `evalEvidence` still rejects malformed batch CSR/columns and malformed
  predicates before mutating poisoned output;
- `requireEvidenceBatch` validates a complete batch without a predicate;
- `evalEvidenceValidated` validates only the fixed predicate IDs, overwrites
  every active output word, and matches `evalEvidence` exactly;
- repeated validated calls allocate zero bytes.

**Step 2: Run the focused tests and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^(TestEvidenceValidation|TestEvalEvidenceValidated)'
```

Expected: FAIL because the split helpers do not exist.

**Step 3: Split validation without changing semantics**

Refactor to these responsibilities:

```go
func requireEvidenceBatch(batch Batch, p *program.Program, states *EvidenceStateIndex)
func requireEvidencePredicate(p *program.Program, states *EvidenceStateIndex, predicate EvidencePredicate)
func evalEvidenceValidated(dst truth.Planes, reasons ReasonPlanes, batch Batch, p *program.Program, states *EvidenceStateIndex, predicate EvidencePredicate)
```

Keep `evalEvidence` as the safe package-private wrapper that invokes both
validators and then `evalEvidenceValidated`. Move no semantic branch out of the
existing reducer. The executor will call `requireEvidenceBatch` once and then
the validated reducer for every Evidence instruction.

**Step 4: Run all evaluator tests and benchmarks**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval
go test -count=1 -timeout 120s ./internal/eval -run '^$' -bench '^BenchmarkEvalEvidence$' -benchmem
```

Expected: PASS and `0 B/op`, `0 allocs/op`.

**Step 5: Commit**

```bash
git add internal/eval/evidence_eval.go internal/eval/evidence_eval_test.go internal/eval/scalar_bench_test.go
git commit -m "perf: validate evidence batches once"
```

### Task 4: Execute The Topological Schedule In Liveness Slots

**Files:**
- Create: `internal/eval/executor.go`
- Create: `internal/eval/executor_test.go`

**Step 1: Write failing schedule and alias tests**

Build small immutable Programs directly in the test with valid leaf columns,
operand CSR, source nodes, and explicit truth/reason slots. Add table tests for:

- Equal/Exists/Evidence leaves reaching their assigned slots;
- `All`, `Any`, and `Not` truth tables over complete request bitplanes;
- reason union through All/Any and unchanged reasons through Not;
- group destinations aliasing the first, middle, last, and no operand truth
  slot;
- an independently different alias choice for the reason slot;
- 65 rows with clean truth and reason tails.

Test through an unexported `executeSchedule` helper so outcome reduction is not
required yet.

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteSchedule'
```

Expected: FAIL because `Executor` and schedule execution do not exist.

**Step 3: Implement flat scratch and slot views**

Add sentinel errors for nil/malformed execution inputs and fixed-width size
overflow. Define `Executor` with Program binding, `EvidenceStateIndex`, bound
`index.Query`, flat truth/reason words, candidate words, selector values and
presence, plus selected-remediation scratch.

Implement widened sizing and these private views:

```go
func (e *Executor) truthSlot(slot schema.SlotID, rows uint32) truth.Planes
func (e *Executor) reasonSlot(slot schema.SlotID, rows uint32) ReasonPlanes
func (e *Executor) executeSchedule(p *program.Program, batch Batch)
```

Each instruction uses `p.TruthSlots[row]` and `p.ReasonSlots[row]`. Leaf rows
call existing kernels. Boolean rows choose an alias-safe driver separately for
truth and reasons, copy only when no operand owns the destination, and reduce
the remaining operands in source order. Implement reason union as direct
whole-word OR over each of the nine planes; do not allocate a temporary plane.

**Step 4: Run schedule tests on amd64 and 386**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteSchedule'
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteSchedule'
```

Expected: PASS on both architectures.

**Step 5: Commit**

```bash
git add internal/eval/executor.go internal/eval/executor_test.go
git commit -m "feat: execute compiled instruction schedules"
```

### Task 5: Resolve Applicability, Clauses, Outcomes, And Drivers

**Files:**
- Modify: `internal/eval/executor.go`
- Modify: `internal/eval/executor_test.go`

**Step 1: Write failing semantic path tests**

Create one policy fixture with policy-defined outcomes of deliberately different
precedence and clauses covering these rows:

- applicability false: outcome zero, no applied requirement or driver;
- applicability true plus satisfied assertion/evidence: `OnSatisfied`;
- applicability true plus definite false: `OnFalse`;
- missing assertion fact: reason resolution through `RuleSetID == ClauseID`;
- stale evidence: stale resolution;
- conflicting evidence: conflict resolution;
- unknown applicability: unresolved outcome, never `OnFalse`;
- simultaneous requirements: higher precedence wins;
- equal precedence with different IDs: lower OutcomeID wins;
- repeated same winning OutcomeID: first requirement/clause source-order driver
  remains selected;
- nonterminal winner retains remediations; terminal winner exposes none.

Assert exact `RequirementOffsets/IDs`, driver columns, ascending reason edges,
remediation edges, and valid empty evidence ranges.

**Step 2: Run the focused tests and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^(TestExecutePaths|TestExecuteOutcomePrecedence|TestExecuteDriverSelection)'
go test -count=1 -timeout 60s ./internal/eval ./internal/result ./internal/index
```

Expected: FAIL because `Executor.Execute` and resolution are incomplete.

**Step 3: Implement row resolution**

Add:

```go
func (e *Executor) Execute(dst *result.Batch, p *program.Program, batch Batch) error
```

Before mutating `dst`:

1. Validate/bind Program evidence states and applicability query.
2. Validate request/evidence batch shapes once.
3. Check scratch and worst-case result widths with widened arithmetic.
4. Provision Executor scratch.

Then reset `dst`, execute the schedule, and resolve each request:

1. Gather indexed symbolic selector values/presence and query candidates.
2. Visit candidate requirements in row order.
3. Exclude definite-false applicability; record every other requirement ID.
4. For unresolved applicability, resolve its root reasons once per referenced
   clause.
5. For active requirements, combine assertion plus clause evidence roots with
   scalar four-valued AND for the selected row and union their root reasons.
6. Produce satisfied, false, or reason-resolved clause candidates.
7. Reduce candidates with `p.Outcomes.Prefer`; retain the first driver when the
   winner ID does not change.
8. Choose the driver node from the first source-order root carrying the winning
   polarity/reason.
9. Write one driver edge, all winning candidate reasons in ascending ID order,
   and selected remediations.

Keep the selected candidate in one stack struct containing scalar IDs, a
`truth.ReasonMask`, and a borrowed remediation slice. Do not append output for a
candidate until global reduction for that request is complete.

**Step 4: Run semantic tests and full package tests**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^(TestExecutePaths|TestExecuteOutcomePrecedence|TestExecuteDriverSelection)'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/executor.go internal/eval/executor_test.go
git commit -m "feat: resolve compiled policy outcomes"
```

### Task 6: Prove Boundaries, Reuse, And Scalar Baseline

**Files:**
- Modify: `internal/eval/executor_test.go`
- Create: `internal/eval/executor_bench_test.go`

**Step 1: Write failing reuse and boundary tests**

Add tests for rows `0, 1, 64, 65`, large -> small -> large result reuse,
poisoned truth/reason/result capacity, Program A -> B -> A rebinding, unchanged
destination on rejected Program/batch input, and repeated warm execution.

Use `testing.AllocsPerRun(1000, ...)` after one priming call and require exactly
zero allocations. Keep all assertion formatting outside the measured closure.

**Step 2: Run the focused tests and verify RED if any stale state remains**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^(TestExecutorBoundaries|TestExecutorReuse|TestExecutorRejectsAtomically|TestExecutorAllocations)'
```

Expected before final fixes: at least one failure from unhandled reset, tail,
binding, or capacity behavior.

**Step 3: Make the minimal reuse fixes**

Clear only active result lengths/offsets, overwrite every active scratch word,
mask all partial tails, and preserve prior Program bindings/output until a new
binding and all preflight checks succeed. Pre-size known maximum edge columns;
reuse retained capacity on later calls. Do not add `sync.Pool`.

**Step 4: Add the scalar benchmark**

Benchmark a deterministic 1,024-row Program containing fact, evidence,
All/Any/Not, multiple requirements, and simultaneous outcomes:

```go
func BenchmarkExecuteScalar(b *testing.B) {
    // Build and prime outside the timer.
    b.ReportAllocs()
    b.ResetTimer()
    for range b.N {
        if err := executor.Execute(&dst, p, batch); err != nil {
            b.Fatal(err)
        }
    }
}
```

**Step 5: Run allocation, benchmark, race, and 386 checks**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^(TestExecutorBoundaries|TestExecutorReuse|TestExecutorRejectsAtomically|TestExecutorAllocations)'
go test -count=1 -timeout 120s ./internal/eval -run '^$' -bench '^BenchmarkExecuteScalar$' -benchmem
go test -count=1 -timeout 60s -race ./internal/eval
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval
```

Expected: all tests PASS and benchmark reports `0 B/op`, `0 allocs/op`.

**Step 6: Commit**

```bash
git add internal/eval/executor.go internal/eval/executor_test.go internal/eval/executor_bench_test.go
git commit -m "test: prove scalar executor reuse"
```

### Task 7: Run Cross-Cutting Gates And Review

**Files:**
- Modify only files required by confirmed review findings.

**Step 1: Run the complete fresh verification matrix**

Run each command once with its explicit timeout:

```bash
go test -count=1 -timeout 60s ./...
go test -count=1 -timeout 60s -race ./internal/eval ./internal/index ./internal/result
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval ./internal/index ./internal/result
timeout 60s go vet ./...
timeout 120s go build -o /dev/null ./cmd/nornrune
timeout 120s go build -gcflags=-m ./internal/eval
timeout 60s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/eval ./internal/index ./internal/result
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
go test -count=1 -timeout 120s ./internal/eval -run '^$' -bench '^(BenchmarkExecuteScalar|BenchmarkEvalPredicate|BenchmarkEvalEvidence)$' -benchmem
git diff --check
```

Expected: all commands exit zero; format/module/diff/analyzer commands print no
output; all evaluator benchmarks report zero allocations.

**Step 2: Request a read-only code review**

Review the complete Task 16 commit range against:

- `docs/plans/2026-08-22-program-execution-outcome-resolution-design.md`;
- this implementation plan;
- Task 16 in the authoritative roadmap;
- four-valued truth, deterministic precedence/driver, CSR, slot-alias, and
  zero-allocation contracts.

Require findings ordered by severity with file/line references. The review
agent must not modify files and must use explicit timeouts for any command it
runs.

**Step 3: Apply only confirmed findings with RED/GREEN tests**

For each correctness or contract defect, first add a focused failing test, run
it once to prove RED, make the smallest fix, and rerun the focused plus affected
package tests. Record later-task optimizations separately rather than expanding
Task 16.

**Step 4: Re-run affected gates and commit fixes**

Stage only Task 16 files and commit confirmed fixes with a bounded message. Do
not amend earlier commits.

**Step 5: Mark Task 16 complete**

Inspect:

```bash
git status --short --branch
git log --oneline -12
```

Expected: clean `feature/policy-engine` worktree with Task 16 design, plan,
implementation, tests, and any review fixes committed. Update the full tracker:
Task 16 complete, Task 17 in progress.
