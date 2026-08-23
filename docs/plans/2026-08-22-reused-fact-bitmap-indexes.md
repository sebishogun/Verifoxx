# Reused Fact Bitmap Indexes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build compiler-selected per-batch symbol bitmaps once and reuse them
across repeated `Equal`, `NotEqual`, and `In` leaves without changing
four-valued outcomes or warm allocation counts.

**Architecture:** The compiler emits a sorted immutable `index.FactSpec` for
only reused symbol fields and policy symbols. `index.FactBuilder` scans each
selected SoA symbol column once through a reusable dense SymbolID lookup and
writes one flat bitmap row per queried value. `eval.Executor` owns the mutable
index, uses it before SIMD/scalar leaf execution, and keeps direct, SIMD, and
indexed modes independently measurable.

**Tech Stack:** Go 1.27, typed schema IDs, pointerless SoA/CSR metadata, uint64
bitmaps, `internal/simdops`, table-driven differential tests, `testing.B`,
`AllocsPerRun`, 386 overflow checks, race/checkptr, pinned `fieldalignment`.

---

### Task 1: Define The Reusable Fact Index Contract

**Files:**
- Create: `internal/index/batch.go`
- Create: `internal/index/batch_test.go`
- Modify: `internal/index/schema.go`

**Step 1: Write the failing component test**

Add one top-level `TestFactIndexBuildAndLookup` with subtests for:

- exact masks at rows `0`, `1`, `63`, `64`, and `65`;
- zero/missing symbol values;
- sparse and dense matches;
- an irrelevant symbol column;
- extension symbols above `ProgramSymbolCount`;
- unknown queried values returning an indexed all-zero result;
- dirty large -> small -> large reuse with clean tails;
- malformed parallel columns, unsorted fields/values, duplicate values,
  out-of-range columns and symbols, and short source slabs;
- overflow helpers at 386-sized limits;
- destination atomicity on validation errors; and
- zero warm allocations.

Use a contract shaped like:

```go
spec := FactSpec{
	FieldIDs:    []schema.FieldID{1, 3},
	Columns:     []uint32{0, 1},
	ValueStarts: []uint32{0, 2},
	ValueCounts: []uint32{2, 1},
	UseCounts:   []uint32{4, 2},
	Values:      []schema.SymbolID{1, 2, 3},
}
columns := SymbolColumns{
	Values:             values,
	Rows:               65,
	Count:              2,
	ProgramSymbolCount: 3,
}
var builder FactBuilder
var got FactIndex
if err := builder.Build(&got, &spec, columns); err != nil {
	t.Fatal(err)
}
mask, indexed := got.Lookup(1, 2)
```

Assert exact least-significant-bit-first words and zero padding in the last
word. Fill `got.ValueMasks` with `math.MaxUint64` before reuse so stale data is
observable.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestFactIndexBuildAndLookup$'
```

Expected: FAIL because `FactSpec`, `FactBuilder`, `FactIndex`, and
`SymbolColumns` do not exist.

**Step 3: Implement the minimal index**

In `internal/index/schema.go`, add:

```go
ErrInvalidFactIndex = errors.New("index: invalid fact bitmap index")
```

In `internal/index/batch.go`, add:

```go
type FactSpec struct {
	FieldIDs    []schema.FieldID
	Columns     []uint32
	ValueStarts []uint32
	ValueCounts []uint32
	UseCounts   []uint32
	Values      []schema.SymbolID
}

type SymbolColumns struct {
	Values             []schema.SymbolID
	Rows               uint32
	Count              uint32
	ProgramSymbolCount uint32
}

type FactIndex struct {
	spec       *FactSpec
	ValueMasks []uint64
	Rows       uint32
	WordCount  uint32
}

type FactBuilder struct {
	valueRows []uint32
}
```

Implement:

- `FactSpec.Clone()` with exact-capacity copies;
- `FactSpec.Valid(fields Schema, programSymbolCount uint32) bool`;
- widened `factWordCount` and `factMaskLen` helpers that reject any byte length
  above `math.MaxInt` before converting to `int`;
- `(*FactBuilder).Build(dst *FactIndex, spec *FactSpec, columns SymbolColumns)
  error`;
- `(*FactIndex).Reset()` retaining `ValueMasks` capacity; and
- `(FactIndex).Lookup(field schema.FieldID, value schema.SymbolID)
  (mask []uint64, indexed bool)`.

Validate every input before resizing `dst`. Clear every active mask word. For
each field, install only that field's values in `valueRows`, scan its contiguous
column once, set matching row bits, then clear only the installed entries. A
lookup for a selected field and unknown value returns `nil, true`; an unselected
field returns `nil, false`.

Do not store one bitmap per observed batch value, allocate per field, use a map,
or use `sync.Pool`.

**Step 4: Run GREEN and portability checks**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestFactIndexBuildAndLookup$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/index -run '^TestFactIndexBuildAndLookup$'
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/index -run '^TestFactIndexBuildAndLookup$'
```

Expected: PASS and the warm subtest reports zero allocations.

### Task 2: Measure Build-Plus-Reuse Before Selecting Fields

**Files:**
- Create: `internal/index/batch_bench_test.go`
- Modify later from measured output: `docs/performance.md`

**Step 1: Add paired direct/index benchmarks**

Add `BenchmarkBatchIndex` with deterministic cases over:

- rows `64`, `256`, `1,024`, and `4,096`;
- value uses `1`, `2`, `4`, `8`, `16`, and `32`;
- dense two-value and sparse 64-value columns; and
- direct whole-slice comparison/packing versus one `FactBuilder.Build` plus all
  requested mask copies/unions.

The direct side must use `simdops.CompareU32` and `simdops.PackMask` at the same
shapes the evaluator uses. Prime every destination and builder before timing,
report allocations, and keep build cost inside the indexed timer.

**Step 2: Run repeated isolated measurements**

Run:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkBatchIndex$' -benchmem -benchtime=100ms -count=3 ./internal/index
```

Expected: all cases report `0 B/op`, `0 allocs/op` after priming. Use this broad
run only to locate candidate boundaries. Select:

- `factIndexMinUses`: the first reuse count where build-plus-reuse remains
  outside noise for both sparse and dense cases at that row count and larger;
  and
- `factIndexMinRows`: the first row count where that reuse threshold remains a
  win.

If there is no stable crossover, retain forced indexed mode for correctness and
set automatic selection off. Do not encode a gain from a single marginal case.

**Step 3: Lock benchmark boundaries**

Add benchmark cases immediately below, at, and above both measured thresholds.
Rerun only those isolated cases with `-benchtime=500ms -count=6` before choosing
constants.

### Task 3: Emit Compiler-Selected Fact Specifications

**Files:**
- Modify: `internal/program/program.go`
- Modify: `internal/program/freeze.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/index.go`
- Modify: `internal/compile/index_test.go`

**Step 1: Extend the existing compiler reuse test for RED**

Extend `TestLowerIndexesWarmReuse` rather than adding another top-level test.
Construct final instruction rows containing:

- repeated symbol `Equal`/`NotEqual` values;
- one symbol `In` list with duplicate values;
- Boolean, integer, timestamp, ordered, and `Exists` leaves that must not enter
  the fact spec; and
- one symbol field below the measured reuse threshold.

Assert sorted fields, kind-local columns, exact measured use counts, sorted
unique symbols, exact capacities after `program.Freeze`, no borrowed compiler
storage, deterministic large -> small -> large reuse, and zero warm allocations.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestLowerIndexesWarmReuse$'
```

Expected: FAIL because `Program` has no fact-index specification and
`lowerIndexes` does not count reusable leaves.

**Step 3: Add Program ownership**

Add `FactIndexSpec policyindex.FactSpec` beside the other Program indexes, before
the fixed scalar tail. Clone it in `program.Freeze` and reset every slice in
`resetInstructionColumns`.

Review field order with the pinned analyzer; do not move the fixed scalar tail
ahead of pointer-bearing fields.

**Step 4: Count and canonicalize reusable leaves in two scans**

Add reusable Lowerer scratch:

```go
factUseCounts   []uint32
factValueStarts []uint32
factValueFill   []uint32
factValues      []schema.SymbolID
```

In the first instruction scan, count one use/value for symbol `Equal` and
`NotEqual`, and `ListCounts[row]` uses/values for symbol `In`. Compute per-field
prefix starts with uint64 overflow checks. In the second scan, resolve each
`ValueID` through the existing canonical symbol table and fill its field range.

Iterate fields in FieldID order. Emit only fields whose use count is at least
the measured `factIndexMinUses`; sort and compact each field's scratch values;
append the field, its `FieldIndex` symbol column, exact use count, and unique
values to `p.FactIndexSpec`.

Never scan all instructions once per field. Do not include Boolean,
integer/timestamp, ordered, `Exists`, or evidence rows.

**Step 5: Run GREEN and ownership gates**

Run:

```bash
go test -count=1 -timeout 60s ./internal/compile ./internal/program -run '^TestLowerIndexesWarmReuse$|^TestFreeze'
GOARCH=386 go test -count=1 -timeout 60s ./internal/compile ./internal/program
```

Expected: PASS with exact frozen capacities and zero warm lowering allocations.

### Task 4: Integrate Indexed Leaves Into The Executor

**Files:**
- Modify: `internal/eval/executor.go`
- Modify: `internal/eval/simd.go`
- Modify: `internal/eval/predicate.go` only if a shared validation helper avoids
  duplicated malformed-program checks
- Create: `internal/eval/batch_index_test.go`
- Modify: `internal/eval/eval_bench_test.go`

**Step 1: Write the end-to-end acceptance test**

Add one top-level `TestFactIndexExecutionMatchesDirect` with helpers that build
a valid Program and Batch containing:

- selected symbol `Equal`, `NotEqual`, and overlapping/duplicate `In` leaves;
- missing rows, dense matches, sparse matches, extension symbols, and 65-row
  tails;
- unselected symbol, Boolean, integer, timestamp, `Exists`, ordered, and
  evidence leaves;
- `All`, `Any`, and `Not` composition with liveness slot aliases;
- direct scalar, forced SIMD, forced index, and automatic full result batches;
- rows immediately below, at, and above the measured row crossover;
- poisoned large -> small -> large executor reuse; and
- `testing.AllocsPerRun(100)` after priming.

Compare truth words, every reason plane, outcomes, applied requirements,
evidence links, remediations, and compact provenance. Also mutate the compiled
fact spec in subtests and require `Execute` to return `ErrInvalidProgram` before
mutating the result.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestFactIndexExecutionMatchesDirect$'
```

Expected: FAIL because the executor has no fact-index mode or scratch.

**Step 3: Add independent execution selection**

Extend the internal execution modes so they retain isolated benchmark meaning:

```go
const (
	executionAuto executionMode = iota
	executionScalar // scalar leaves/groups, no fact index
	executionSIMD   // forced SIMD where supported, no fact index
	executionIndex  // forced fact index, scalar fallback elsewhere
)
```

`executionAuto` uses the compiled index only when rows reach the measured
`factIndexMinRows`; `executionScalar` and `executionSIMD` disable it.
`executionIndex` disables SIMD so index cost and semantics remain independently
measurable.

**Step 4: Add executor-owned index storage**

Add `policyindex.FactBuilder` and `policyindex.FactIndex` to `Executor`. Before
`dst.Reset`, validate `p.FactIndexSpec` against `p.FieldIndex` and
`ProgramSymbolCount`, validate every selected exact predicate value is present
in the spec, and build from `batch.SymbolValues` when selection is active.

Map invalid specs to `ErrInvalidProgram` and widened sizing failures to
`ErrBatchTooLarge`. Reset the active binding when indexing is disabled so a
previous batch cannot leak masks into a direct execution.

**Step 5: Evaluate selected exact leaves from masks**

Before SIMD/scalar predicate dispatch, ask the active `FactIndex` whether the
instruction field is selected. For a selected symbol predicate:

- validate field, column, opcode, and every `ValueID` before resetting output;
- reset truth/reasons once and compute missing reasons from presence;
- copy the equality mask, invert within presence for `NotEqual`, or OR all `In`
  masks;
- apply presence after mask composition;
- derive negative truth as `presence &^ positive`; and
- clear partial tails through the same helper used by scalar/SIMD leaves.

Return false without mutation for unsupported or unselected leaves. Do not add
maps, callbacks, per-row allocations, or a second batch-column scan.

**Step 6: Verify GREEN across backends**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestFactIndexExecutionMatchesDirect$'
go test -count=1 -timeout 60s -tags=purego ./internal/eval -run '^TestFactIndexExecutionMatchesDirect$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval -run '^TestFactIndexExecutionMatchesDirect$'
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/eval -run '^TestFactIndexExecutionMatchesDirect$'
```

Expected: PASS and zero warm allocations.

### Task 5: Confirm Automatic Crossover And Complete Verification

**Files:**
- Modify: `internal/index/batch_bench_test.go`
- Modify: `internal/eval/eval_bench_test.go`
- Modify: `docs/performance.md`
- Verify all Task 20 files

**Step 1: Benchmark final evaluator paths**

Add direct versus automatic/forced-index cases to the existing evaluator
benchmark fixture at the selected reuse and row boundaries. Include complete
index construction in each indexed iteration and prime all executor/result
storage before timing.

Run:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkBatchIndex$|^BenchmarkEvaluateBackends/indexed' -benchmem -benchtime=300ms -count=6 ./internal/index ./internal/eval
```

Expected: `0 B/op`, `0 allocs/op`; the automatic crossover remains outside the
measured spread. If evaluator integration changes the crossover, update the
constants and exact boundary tests, then rerun one isolated bounded command.

**Step 2: Record measured behavior**

Append CPU, Go version, runtime SIMD tier, benchmark command, distributions,
paired minima, selected row/reuse crossovers, unsupported shapes, memory
formula, and allocation counts to `docs/performance.md`. State any shape that
does not win as a direct fallback; do not extrapolate beyond the measured tier.

**Step 3: Run package variants and static gates**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/index ./internal/compile ./internal/program ./internal/eval
go test -count=1 -timeout 60s -tags=purego ./internal/index ./internal/compile ./internal/program ./internal/eval
GOARCH=386 go test -count=1 -timeout 60s ./internal/index ./internal/compile ./internal/program ./internal/eval
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/index ./internal/compile ./internal/program ./internal/eval
timeout 60s go vet ./internal/index ./internal/compile ./internal/program ./internal/eval
timeout 60s go vet -tags=purego ./internal/index ./internal/compile ./internal/program ./internal/eval
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/index ./internal/compile ./internal/program ./internal/eval
```

Expected: PASS; analyzers print nothing.

**Step 4: Run the one full repository gate**

Run:

```bash
go test -count=1 -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace commands print nothing.

**Step 5: Review and checkpoint**

Review the complete uncommitted Task 20 diff against the roadmap and this plan.
Fix all Critical and Important findings and rerun affected bounded gates. Keep
Task 18, Task 19, and Task 20 separable in the worktree. Commit only when the
user explicitly requests it.
