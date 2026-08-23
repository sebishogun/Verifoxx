# Fixed Worker Scheduler Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add zero-copy 64-row evaluator ranges and a bounded fixed-worker scheduler with deterministic result merging and a measured parallel crossover.

**Architecture:** `eval.Executor.ExecuteRange` creates a private physical row view over existing column-major storage and rebases only evidence CSR offsets. A scheduler owns fixed worker, admission, work-token, and shard-result storage; workers write private results and the caller merges once in shard order.

**Tech Stack:** Go 1.27, SoA/CSR views, SIMD and fact indexes, bounded channels, `sync.WaitGroup`, `sync.Once`, Task 21 context leases, `testing.AllocsPerRun`, race/checkptr, 386, vet, and pinned field alignment.

---

### Task 1: Add Zero-Copy Scalar Row Ranges

**Files:**
- Create: `internal/eval/range_test.go`
- Modify: `internal/eval/batch.go`
- Modify: `internal/eval/executor.go`
- Modify: `internal/eval/predicate.go`

**Step 1: Write the failing scalar range test**

Add `TestExecuteRange` in package `eval`. Reuse the existing package-private
execution fixture helpers and add a test helper that repeats one valid batch to
129 rows without changing its physical column layout. Cover ranges `[0,64)`,
`[64,128)`, and `[128,129)`.

For each range:

1. execute the complete 129-row source through forced scalar mode;
2. execute the range through a new private `executeRangeMode` with
   `executionScalar` and caller-owned `make([]uint32, rangeRows+1)` scratch;
3. compare the range result with the corresponding rows and rebased CSR ranges
   from the complete result; and
4. assert source scalar/packed/evidence backing pointers and contents are
   unchanged.

Also test empty, start-after-end, end-after-source, unaligned start,
non-tail-unaligned end, short scratch, malformed source evidence offsets, and
nil executor/destination/program. Invalid calls must not mutate the prior
destination.

Use these signatures:

```go
func (e *Executor) ExecuteRange(
	dst *result.Batch,
	p *program.Program,
	batch Batch,
	start, end uint32,
	evidenceOffsets []uint32,
) error

func (e *Executor) executeRangeMode(
	dst *result.Batch,
	p *program.Program,
	batch Batch,
	start, end uint32,
	evidenceOffsets []uint32,
	mode executionMode,
) error
```

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteRange$'
```

Expected: FAIL because `ExecuteRange` and physical row views do not exist.

**Step 3: Add private physical layout metadata**

Append non-pointer fields to `eval.Batch`:

```go
Rows      uint32
rowBase   uint32
rowStride uint32
```

`rowStride == 0` means the existing compact full-batch layout. Add private,
widened helpers:

```go
func (b Batch) sourceRows() uint32
func (b Batch) sourceWords() uint32
func (b Batch) wordBase() uint32
func (b Batch) validPhysicalRange() bool
func batchRowColumn[T any](b Batch, values []T, column uint32) []T
func batchWordColumn(b Batch, values []uint64, column uint32) []uint64
```

Every slice calculation must widen before multiplication/addition and narrow
only after validating against `len`. Adapt `Batch.Present` and `Batch.Boolean`
to use the helpers; existing full-batch behavior stays byte-for-byte identical.

**Step 4: Build the range view atomically**

`executeRangeMode` validates all range arithmetic before writing scratch:

- `start <= end <= batch.Rows`;
- `start` is divisible by 64;
- `end` is divisible by 64 or equals `batch.Rows`;
- the source is a full compact batch, not a nested view;
- `len(evidenceOffsets) == int(end-start)+1`; and
- request/evidence slices can be indexed without overflow.

For programs containing an evidence opcode, validate the selected source CSR
range, subtract the first edge offset into scratch, slice only the selected
reference edges, and retain global evidence columns. For programs without an
evidence opcode, fill scratch with zeros without making evidence shape a new
validation requirement.

Create a local copied `Batch` with `Rows=end-start`, `rowBase=start`,
`rowStride=source.Rows`, sliced request IDs, rebased offsets, and selected refs.
Call `executeMode` only after the view is complete. Return existing bounded
evaluator errors; do not allocate fallback scratch.

**Step 5: Teach scalar execution the physical stride**

Replace compact-width calculations in `validExecutionBatchColumns`,
`selectRequirementCandidates`, `fillInMatches`, and `evalPredicate` with the
private row/word helpers. Truth/reason/result rows remain local zero-based rows;
only source fact lookups use the physical base and stride. Evidence evaluation
continues to consume the compact rebased CSR view unchanged.

**Step 6: Run scalar GREEN and regression tests**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteRange$'
go test -count=1 -timeout 60s ./internal/eval
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteRange$'
```

Expected: PASS. Do not commit.

### Task 2: Extend SIMD And Fact Indexes To Ranges

**Files:**
- Modify: `internal/eval/range_test.go`
- Modify: `internal/eval/simd.go`
- Modify: `internal/eval/batch_index.go`
- Modify: `internal/index/batch.go`
- Modify: `internal/index/batch_test.go`

**Step 1: Add failing backend differential cases**

Extend `TestExecuteRange` to run the same 64/64/1 ranges through
`executionScalar`, `executionSIMD`, `executionIndex`, and `executionAuto` and
compare every result column. Include selected and unselected fact-index fields,
Boolean values, missing values, evidence, and the one-row tail.

Extend `TestFactBuilder` with `SymbolColumns` where active rows are a middle
64-row interval of a wider two-column source. Assert exact masks and unchanged
source storage. Add malformed offset/stride combinations and 32-bit widened
boundaries.

**Step 2: Run the focused RED tests**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestExecuteRange$'
go test -count=1 -timeout 60s ./internal/index -run '^TestFactBuilder'
```

Expected: FAIL because SIMD and fact-index paths still assume compact columns.

**Step 3: Slice complete SIMD inputs**

In `evalPredicateSIMD`, obtain packed presence/Boolean and typed comparison
inputs through `batchWordColumn` and `batchRowColumn`. Keep local shard rows for
thresholds, destination masks, and tail clearing. SIMD receives complete
contiguous shard slices; do not invoke a kernel per source column element.

**Step 4: Add strided symbol-column input**

Extend `index.SymbolColumns`:

```go
type SymbolColumns struct {
	Values             []schema.SymbolID
	Rows               uint32
	Count              uint32
	ProgramSymbolCount uint32
	RowOffset          uint32
	RowStride          uint32
}
```

Zero `RowStride` retains `Rows`. Validate `RowOffset+Rows <= RowStride`, exact
physical source length, and widened element bounds before replacing the prior
index. Scan `column*RowStride + RowOffset : +Rows` for each selected column.

Pass `Batch.rowBase` and `Batch.sourceRows()` from `buildFactIndex`; use
`batchWordColumn` for indexed predicate presence. Keep index masks local to
shard rows.

**Step 5: Run backend GREEN and portability checks**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/index ./internal/eval
GOARCH=386 go test -count=1 -timeout 60s ./internal/index ./internal/eval
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/index ./internal/eval
```

Expected: PASS. Do not commit.

### Task 3: Add Shard Partitioning And Deterministic Merge

**Files:**
- Create: `internal/scheduler/shard.go`
- Create: `internal/scheduler/scheduler_test.go`

**Step 1: Write failing shard helper tests**

Add `TestSchedulerShardHelpers` with subtests for:

- exact range coverage at rows `0, 1, 63, 64, 65, 127, 128, 129, 4096` and
  worker counts `1, 2, 3, 4`;
- nonempty ranges, 64-row starts, aligned non-tail ends, and maximum word-count
  balance difference of one;
- merge of deliberately completed-out-of-order shard results;
- every fixed and CSR result column, including all parallel driver columns;
- destination capacity reuse and zero warm merge allocations; and
- malformed private result shapes and widened total-edge overflow leaving the
  destination unchanged.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestSchedulerShardHelpers$'
```

Expected: FAIL because row partition and result merge do not exist.

**Step 3: Implement word-balanced partitioning**

Add:

```go
type rowRange struct {
	start uint32
	end   uint32
}

func partitionRows(dst []rowRange, rows uint32, shards int) []rowRange
```

Clamp shards to one for zero rows and otherwise to the source word count and
`len(dst)`. Divide words by quotient/remainder, convert starts in widened
arithmetic, and clamp only the final end to `rows`. Never append beyond caller
capacity.

**Step 4: Implement validate-then-merge**

Add `mergeResults(dst *result.Batch, shards []result.Batch, ranges []rowRange,
rows uint32) error`. First validate all row counts, offset lengths, zero starts,
monotonic offsets, final edge counts, driver parallel lengths, contiguous range
coverage, and widened total capacities. Only then call `dst.Reset(rows)`, reserve
each edge family once, and copy/append shards in range order while rebasing every
offset by its current edge base.

Use caller-owned destination storage and exact capacity hints. Do not use maps,
reflection, or per-row helper allocation.

**Step 5: Run merge GREEN and allocation checks**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestSchedulerShardHelpers$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/scheduler -run '^TestSchedulerShardHelpers$'
```

Expected: PASS and warm merge allocations equal zero. Do not commit.

### Task 4: Implement Bounded Fixed Workers And Lifecycle

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/worker.go`
- Modify: `internal/scheduler/scheduler_test.go`

**Step 1: Add the failing scheduler acceptance test**

Add `TestScheduler` using the compiled embedded Verifoxx fixture and a helper
that repeats its valid five-row batch to `63, 64, 65, 127, 128, 129`, and a
larger benchmark-like shape. Cover:

- invalid worker/queue/capacity/config and nil execution arguments;
- direct one-shard equality with `eval.Executor.Execute`;
- forced parallel equality of every result column across repeated runs;
- exact private shard ranges visible from package-internal state;
- bounded admission by withholding every preallocated batch state;
- pre-canceled and deterministically canceled queued admission without sleeps;
- cancellation while waiting for the first global work token;
- concurrent batches sharing at most `Workers` tokens;
- evaluator failure and cancellation leaving a poisoned destination unchanged;
- poisoned batch-state/result reuse;
- zero warm allocations for one admitted state after every worker/result slot is
  primed; and
- Close rejecting new work, draining an admitted state, joining workers, and
  returning successfully from concurrent repeated calls.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestScheduler$'
```

Expected: FAIL because `Config`, `Scheduler`, workers, and lifecycle errors do
not exist.

**Step 3: Define fixed scheduler storage**

Add exported `Config`, `NewScheduler`, `Execute`, and `Close` exactly as shown
in the design. Add sentinels `ErrInvalidScheduler` and `ErrSchedulerClosed`.

Preallocate one fixed `[]batchState`; each state owns `[]rowRange`,
`[]result.Batch`, and `[]error` sized to `Workers`, plus one noncopied
`sync.WaitGroup`. Prefill a bounded available-state channel and a work-token
channel. Create an exact-capacity shard-job channel and one Task 21 Arena with
`Workers` contexts. Start exactly `Workers` long-lived goroutines.

Validate all multiplication and channel counts before allocation. Do not add a
coordinator goroutine, per-call channel, callback, map, mutex on evaluation, or
`sync.Pool`.

**Step 4: Implement admission and work budgeting**

`Execute` validates arguments and acquires a batch state through a select on
available state, caller cancellation, and stopping. Recheck stopping after the
receive. Reserve the first work token with caller cancellation, then reserve
additional tokens only through nonblocking receives up to the desired shard
count. This prevents partial-reservation deadlock and lets concurrent batches
fall back to fewer shards.

The desired count is one below the active crossover; otherwise it is bounded by
workers and source words. `ParallelRows == 0` uses a package constant that
remains disabled until Task 5 measures it; a nonzero override forces tests and
benchmarks.

Reset every active state slot without replacing its backing slices. Set the
wait-group count before any execution or enqueue.

**Step 5: Implement direct and worker execution**

One acquired shard executes synchronously in the caller. Multiple shards send
small `{state,index}` jobs by value. Both paths call one shared shard executor
that:

1. checks caller cancellation;
2. borrows a Task 21 context;
3. grows only its row-offset scratch when needed;
4. obtains its private executor;
5. calls full `Execute` for a full range or `ExecuteRange` otherwise into the
   state's disjoint result slot;
6. returns the context;
7. returns exactly one work token; and
8. calls exactly one `WaitGroup.Done`.

Workers perform this function in their long-lived loop and create no child
goroutine. Store errors by shard index. After `Wait`, cancellation takes
precedence, then the first nonnil shard error; merge only on complete success.
Clear all borrowed context/program/batch/destination pointers before returning
the state to admission.

**Step 6: Implement graceful Close**

Use `sync.Once` only for the shutdown transition. Close the stopping channel,
receive all preallocated batch states, close jobs, wait for worker goroutines,
then close one shared closed channel. Do not cancel already admitted work and do
not close jobs while an admitted producer can send. Nil/zero schedulers return
`ErrInvalidScheduler`; subsequent valid Close calls wait for and return the same
completed shutdown.

**Step 7: Run scheduler GREEN and race gates**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestScheduler$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/scheduler -run '^TestScheduler$'
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/scheduler
```

Expected: PASS with no leak, deadlock, race, or warm allocation. Do not commit.

### Task 5: Measure And Set The Parallel Crossover

**Files:**
- Create: `internal/scheduler/scheduler_bench_test.go`
- Modify: `internal/scheduler/scheduler.go`
- Modify: `docs/performance.md`

**Step 1: Add paired scheduler benchmarks**

Create `BenchmarkScheduler` over rows `64, 256, 1024, 4096, 16384` and workers
`1, 2, 4`. Prime every batch state, worker context, shard result slot, and
destination before timing. Benchmark:

- direct `eval.Executor.Execute` as the one-thread reference;
- scheduler forced to one shard; and
- scheduler forced parallel at each worker count.

Report allocations and fail setup if any result differs from the direct
reference. Do not compile/decode/build the batch inside the timed loop.

**Step 2: Run bounded measurement passes**

Run:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=300ms -count=6 ./internal/scheduler
```

Expected: all warmed cases report `0 B/op`, `0 allocs/op`. Save complete raw
output outside the repository only while comparing runs.

If runs disagree near a candidate threshold, run one narrowed command once:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkScheduler/rows=(CANDIDATES)/' -benchmem -benchtime=500ms -count=6 ./internal/scheduler
```

Do not loop commands or select a threshold inside the spread.

**Step 3: Set and document the measured default**

Set `defaultParallelRows` to the first row count where the weakest supported
multiworker result beats forced serial outside the observed spread. If no tested
shape wins, leave automatic parallelism disabled and record that result rather
than shipping a guessed crossover.

Append a `## Fixed Worker Scheduler` section to `docs/performance.md` with host,
Go version, worker counts, direct/serial/parallel minima, gain, allocations,
selected threshold, shard alignment, and exact measurement commands.

**Step 4: Re-run behavioral tests at the chosen boundary**

Add boundary cases immediately below, at, and above the selected threshold, or
disabled-auto behavior if no crossover exists. Run:

```bash
go test -count=1 -timeout 60s ./internal/scheduler
```

Expected: PASS. Do not commit.

### Task 6: Verify Architecture And Repository Gates

**Files:**
- Verify all Task 22 production, test, benchmark, design, plan, and performance files.

**Step 1: Run static and portability gates independently**

```bash
timeout 60s go vet ./internal/index ./internal/eval ./internal/scheduler
timeout 60s env GOARCH=386 go vet ./internal/index ./internal/eval ./internal/scheduler
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/index ./internal/eval ./internal/scheduler
GOARCH=386 go test -count=1 -timeout 60s ./internal/index ./internal/eval ./internal/scheduler
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/index ./internal/eval ./internal/scheduler
```

Expected: every command passes; vet and field alignment print nothing.

**Step 2: Check hot-path architecture**

Search Task 22 production files for `sync.Pool`, maps, reflection, callback
interfaces, per-request goroutines, and shared result appends. Expect no match.
Inspect `-gcflags=-m` output for `Scheduler.Execute`, shard execution, range view,
and merge; construction and explicit growth may escape, but a primed execution
must not.

**Step 3: Run final repository gates**

```bash
go test -count=1 -timeout 60s ./...
go test -count=1 -tags=purego -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace commands print nothing.

**Step 4: Review and checkpoint**

Run separate specification and code-quality reviews against roadmap Task 22 and
`docs/plans/2026-08-22-fixed-worker-scheduler-design.md`. Fix every Critical and
Important finding, add a RED regression for each behavior defect, and rerun the
affected bounded gates plus the final repository test. Keep Tasks 18-22
separable and do not commit unless the user explicitly requests it; the roadmap
commit message would be `feat: evaluate large batches in parallel`.
