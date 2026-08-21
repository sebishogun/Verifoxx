# Scratch-Slot Liveness Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add deterministic reusable truth/reason slot assignments to every frozen Program and prove that no live result is overwritten.

**Architecture:** A linear last-use pass operates on the final topological InstructionID schedule. Release buckets and a lowest-free-ID bitset assign separate truth and reason slots without maps, recursion, or per-instruction allocation; semantic roots remain live through an end sentinel, while retain-all mode gives debug execution unique slots.

**Tech Stack:** Go 1.27, pointerless SoA/CSR Program columns, `schema.SlotID`, `math/bits`, Go tests and benchmarks.

---

### Task 1: Define Frozen Slot Columns

**Files:**
- Create: `internal/program/slots.go`
- Modify: `internal/program/program.go`
- Modify: `internal/program/freeze.go`
- Test: `internal/program/freeze_test.go`

**Step 1: Write the failing model/freeze test**

Add a valid minimal Program fixture to `freeze_test.go`, populate:

```go
TruthSlots:      []schema.SlotID{1, 1},
ReasonSlots:     []schema.SlotID{1, 1},
TruthSlotCount:  1,
ReasonSlotCount: 1,
```

Freeze it and assert both columns are equal but have distinct backing arrays,
their `len == cap`, and the scalar counts are preserved. Mutating source slot
columns must not change the frozen copy.

**Step 2: Verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestFreezeCopiesSlotPlan$'
```

Expected: compile failure because the Program slot fields do not exist.

**Step 3: Add the Program model**

Create `internal/program/slots.go` with the slot contract documentation. Add
parallel slot columns among Program's pointer-bearing instruction data and the
two peak counts in its scalar tail:

```go
TruthSlots  []schema.SlotID
ReasonSlots []schema.SlotID

TruthSlotCount  uint32
ReasonSlotCount uint32
```

Update `Freeze`:

```go
TruthSlots:      cloneExact(src.TruthSlots),
ReasonSlots:     cloneExact(src.ReasonSlots),
TruthSlotCount:  src.TruthSlotCount,
ReasonSlotCount: src.ReasonSlotCount,
```

Keep field order compatible with the pinned `fieldalignment` analyzer.

**Step 4: Verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program
```

Expected: PASS and no analyzer output.

**Step 5: Commit**

```bash
git add internal/program/slots.go internal/program/program.go internal/program/freeze.go internal/program/freeze_test.go
git commit -m "feat: define program scratch slots"
```

### Task 2: Compute Last Uses And Validate Slot Inputs

**Files:**
- Create: `internal/compile/liveness.go`
- Create: `internal/compile/liveness_test.go`
- Modify: `internal/compile/lower.go`

**Step 1: Write failing last-use tests**

Build direct pointerless Program fixtures for:

- a three-row chain with last uses `[1, 2, end]`;
- a shared row consumed twice, retaining its last use at the later consumer;
- two independent semantic roots retained through the end sentinel;
- one CSE row carrying several root flags but only one retained lifetime.

Call the private `computeLastUses` helper and assert exact zero-based last-use
rows. Add malformed cases for misaligned instruction columns, invalid opcode,
bad CSR range, zero operand, forward/self operand, and unknown root bits; each
must return `ErrInvalidGeneratedProgram` without panic.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestSlotLastUse'
```

Expected: compile failure because liveness helpers do not exist.

**Step 3: Add reusable scratch and validation**

Append pointer-bearing scratch before `Lowerer.output`:

```go
slotLastUses    []uint32
slotReleaseHead []schema.InstructionID
slotReleaseNext []schema.InstructionID
slotFreeWords   []uint64
slotReasonLive  []uint8
slotTruth       []schema.SlotID
slotReasons     []schema.SlotID
```

Implement `validateSlotProgram` with widened CSR arithmetic and strict
`operand < consumer InstructionID`. Implement `computeLastUses`:

```go
for row := range n {
    last[row] = uint32(row)
}
for consumer := range n {
    for each operand {
        dependency := int(operand - 1)
        last[dependency] = uint32(consumer)
    }
}
for row, flags := range p.RootFlags {
    if flags != 0 {
        last[row] = uint32(n)
    }
}
```

Support a non-root instruction with no consumer defensively by returning its
newly assigned slot immediately after that row. Task 10 liveness compaction
does not normally emit such a row, but accepting it keeps reason-relevance
tests and future generated-program tooling bounds-safe.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/liveness.go internal/compile/liveness_test.go internal/compile/lower.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestSlotLastUse'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/compile/liveness.go internal/compile/liveness_test.go internal/compile/lower.go
git commit -m "feat: compute instruction slot lifetimes"
```

### Task 3: Assign Deterministic Reusable Slots

**Files:**
- Modify: `internal/compile/liveness.go`
- Modify: `internal/compile/liveness_test.go`

**Step 1: Write failing allocation tests**

Assert:

- a linear chain uses one truth slot;
- two simultaneously live branches use two slots;
- a shared node is not reused before its final consumer;
- two independent roots retain distinct slots;
- allocation always chooses the lowest released SlotID;
- every assigned SlotID is nonzero and at most the reported peak;
- interval verification permits alias only when the prior value's final
  consumer is the new producer row.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestAssignTruthSlots'
```

Expected: failure because slot assignment is absent.

**Step 3: Implement release buckets and the free bitset**

Add an internal mode:

```go
type slotMode uint8

const (
    slotReuse slotMode = iota
    slotRetainAll
)
```

Build one release bucket per consumer row using one-based instruction links.
Before assigning row `i`, return every earlier slot in bucket `i` to
`slotFreeWords`. If row `i` has no consumer and is not retained, return its
slot immediately after assigning it so the next row may reuse it.
Select the lowest free slot with `bits.TrailingZeros64`; otherwise increment a
dense `uint32` peak and convert to `schema.SlotID` only after bounds checks.

Retain-all mode assigns unique dense slots in instruction order and skips all
release operations.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/liveness.go internal/compile/liveness_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestAssignTruthSlots'
GOARCH=386 go test -count=1 -timeout 60s ./internal/compile -run '^TestAssignTruthSlots'
```

Expected: PASS on amd64 and 386.

**Step 5: Commit**

```bash
git add internal/compile/liveness.go internal/compile/liveness_test.go
git commit -m "feat: reuse truth result slots"
```

### Task 4: Add Reason Slots And Retain-All Debug Planning

**Files:**
- Modify: `internal/compile/liveness.go`
- Modify: `internal/compile/liveness_test.go`

**Step 1: Write failing reason/debug tests**

Assert that reverse root closure marks every operand dependency, leaves an
unrelated synthetic row at reason slot zero, and produces a separate dense
reason peak. In retain-all mode, assert truth SlotID equals InstructionID and
every reason-relevant row has a unique dense reason slot. Run the interval
safety checker independently over both columns.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^(TestAssignReasonSlots|TestAssignSlotsRetainAll)'
```

Expected: failure because reason planning and retain-all output are absent.

**Step 3: Implement reverse reason closure**

Seed every row with a defined `RootFlags` role, then walk rows from `n-1` to
zero. For each marked Boolean/Not row, mark its operands. Current leaf opcodes
are reason-capable; marked leaves terminate the walk. Reuse the same allocator
with `slotReasonLive` as an eligibility mask. Ineligible output cells remain
zero because `resizeSlots` clears the active range.

Implement one orchestration method:

```go
func (l *Lowerer) assignSlots(p *program.Program, mode slotMode) error
```

It validates and computes into Lowerer scratch, then replaces Program slot
columns and counts only after both plans succeed.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/liveness.go internal/compile/liveness_test.go
go test -count=1 -timeout 60s ./internal/compile -run '^(TestAssignReasonSlots|TestAssignSlotsRetainAll)'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/compile/liveness.go internal/compile/liveness_test.go
git commit -m "feat: plan reason and debug slots"
```

### Task 5: Integrate Slot Planning Into Atomic Lowering

**Files:**
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_api_test.go`
- Modify: `internal/compile/lower_boundaries_test.go`
- Modify: `internal/program/freeze.go`

**Step 1: Write failing public integration tests**

Extend `TestLowerAPI` and the normalization integration fixture to assert:

- both slot columns align with `Opcodes`;
- all truth slots are valid and every reason-relevant row has a valid reason
  slot;
- peak counts are lower than or equal to instruction count;
- frozen slots have exact capacity and remain unchanged after another warm
  `Lowerer` call and source mutation;
- malformed slot planning leaves the public destination unchanged;
- cold and warm Lower calls produce identical slot columns and counts.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^(TestLowerAPI|TestLowerOwnership|TestLowerDeterministic|TestLowerIntegration)'
```

Expected: failure because public lowering does not invoke slot planning.

**Step 3: Integrate production reuse mode**

After semantic lowering and before `program.Freeze`, call:

```go
if err := l.assignSlots(&l.output, slotReuse); err != nil {
    return err
}
```

Ensure `resetInstructionColumns` clears slot columns/counts on private-stage
failure. Ensure `Freeze` clones slot columns and scalars before rebuilding the
Resolver.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/lower.go internal/compile/lower_api_test.go internal/compile/lower_boundaries_test.go internal/program/freeze.go
go test -count=1 -timeout 60s ./internal/compile ./internal/program
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/compile/lower.go internal/compile/lower_api_test.go internal/compile/lower_boundaries_test.go internal/program/freeze.go
git commit -m "feat: publish liveness-assigned slots"
```

### Task 6: Prove Warm Reuse And Peak Scratch Reduction

**Files:**
- Create: `internal/compile/liveness_bench_test.go`
- Modify: `internal/compile/liveness_test.go`

**Step 1: Add reuse/allocation tests**

Prime one Lowerer and Program, then use `testing.AllocsPerRun` over repeated
`assignSlots` calls. Require zero warm allocations. Interleave a smaller
Program and re-plan the original; require exact equality with a fresh plan and
no stale slot IDs above the peak counts.

**Step 2: Verify the tests**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestAssignSlotsReuse$'
```

Expected: PASS; fix any stale scratch with a new focused RED case.

**Step 3: Add controlled benchmarks**

Create an 8,192-row branching fixture. Benchmark warm reuse and retain-all
planning with `b.ReportAllocs()`. Report calculated evaluator peaks:

```go
words := uint64(truth.WordCount(rows))
truthBytes := words * 8 * 2 * uint64(p.TruthSlotCount)
reasonBytes := words * 8 * truth.ReasonCount * uint64(p.ReasonSlotCount)
b.ReportMetric(float64(truthBytes+reasonBytes), "peak-B")
```

The fixture test must assert reuse peak bytes are strictly below retain-all.

**Step 4: Run the benchmark once**

```bash
go test -count=1 -timeout 120s -run '^$' -bench='AssignSlots' -benchmem ./internal/compile
```

Expected: warm planner reports `0 B/op`, `0 allocs/op`, and lower `peak-B` for
reuse.

**Step 5: Commit**

```bash
git add internal/compile/liveness_test.go internal/compile/liveness_bench_test.go
git commit -m "test: measure liveness slot reuse"
```

### Task 7: Run Cross-Cutting Verification And Review

**Files:**
- Modify only files required by confirmed findings.

**Step 1: Run the complete bounded gate set**

```bash
go test -count=1 -timeout 60s ./internal/program ./internal/compile ./internal/truth
go test -race -count=1 -timeout 120s ./internal/program ./internal/compile ./internal/truth
GOARCH=386 go test -count=1 -timeout 60s ./internal/program ./internal/compile ./internal/truth
go test -count=1 -timeout 60s ./...
timeout 120s go vet ./...
timeout 120s go build ./cmd/verifoxx
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program ./internal/compile
timeout 120s go build -gcflags=-m ./internal/program ./internal/compile
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
git diff --check
git status --short --branch
```

Expected: all commands exit zero; analyzer, formatting, module, and diff checks
print nothing; escape output contains bulk scratch/frozen-slice ownership but
no per-instruction object escape.

**Step 2: Review the Task 11 commit range**

Review for interval endpoint mistakes, root reuse, source/destination aliasing,
non-topological input panic, stale free bits, nondeterministic slot selection,
SlotID narrowing, frozen borrowing, warm allocations, and Task 12/15/16 scope
creep. Fix every confirmed Critical or Important issue with a new RED/GREEN
cycle and repeat Step 1.

**Step 3: Commit review fixes only when needed**

```bash
git add internal/program internal/compile
git commit -m "fix: harden slot liveness boundaries"
```
