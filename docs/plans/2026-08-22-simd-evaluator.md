# SIMD Evaluator Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add measured, allocation-free SIMD execution for compatible fact and bitplane stages while retaining the existing scalar evaluator as the reference and fallback.

**Architecture:** Extend `internal/simdops` with Boolean-mask-to-word packing, then add one mode-aware schedule to the existing reusable `eval.Executor`. Automatic mode selects compatible leaf comparisons and word reductions by measured/runtime thresholds; evidence, `In`, small batches, pure-Go, and scalar-only targets keep their scalar paths.

**Tech Stack:** Go 1.27, `github.com/sebishogun/simd` v1.21.0 whole-slice API, typed SoA columns, uint64 truth/reason planes, caller-owned scratch, Go tests and benchmarks.

---

### Task 1: Pack Comparison Masks Into Bitplanes

**Files:**
- Modify: `internal/simdops/ops.go`
- Modify: `internal/simdops/simd.go`
- Modify: `internal/simdops/purego.go`
- Modify: `internal/simdops/info.go`
- Modify: `internal/simdops/ops_test.go`
- Modify: `docs/performance.md`

**Step 1: Extend the existing acceptance test**

Add `3, 4, 5, 63, 64, 65` to the operation boundary lengths. For each length,
build a deterministic Boolean mask and compare `PackMask` with a local
least-significant-bit-first uint64 packer. Also require:

- dirty output words are fully overwritten for the active result;
- unused final-word bits are zero;
- an undersized destination panics before mutation; and
- warm `PackMask` adds no allocation.

Extend `Thresholds` expectations with:

```go
PackMask: simd.KernelThreshold("MaskBits"),
```

**Step 2: Run the focused test and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/simdops
```

Expected: build failure because `simdops.PackMask` and `Thresholds.PackMask` do
not exist.

**Step 3: Implement portable packing and metadata**

In `ops.go`, add a shared shape helper and portable packer. Compute the word
count without `len(src)+63` overflow:

```go
func maskWordCount(rows int) int {
    words := rows / 64
    if rows&63 != 0 {
        words++
    }
    return words
}

func packMaskPortable(dst []uint64, src []bool) {
    words := maskWordCount(len(src))
    if len(dst) < words {
        panic("simdops: mask destination too short")
    }
    clear(dst[:words])
    for row, set := range src {
        if set {
            dst[row>>6] |= uint64(1) << (uint(row) & 63)
        }
    }
}
```

The `purego` backend calls this helper. The normal backend:

1. validates destination length before mutation;
2. uses the portable helper on big-endian hosts;
3. zeroes the final active word;
4. privately views `[]bool` as bytes; and
5. calls `simd.MaskBits(wordBytes(dst[:words]), boolBytes(src), 1)`.

Keep native-endian detection package-private and initialized once. Add
`PackMask int` to `Thresholds` and populate it from `MaskBits`.

**Step 4: Verify both backends and 386**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/simdops
go test -count=1 -timeout 60s -tags=purego ./internal/simdops
GOARCH=386 go test -count=1 -timeout 60s ./internal/simdops
```

Expected: PASS with zero warm allocations.

**Step 5: Document the primitive**

Update `docs/performance.md` with the `MaskBits` threshold in rows, Boolean
byte-view rationale, LSB-first word representation, big-endian fallback,
destination contract, and final-tail clearing.

### Task 2: Write The SIMD Evaluator Acceptance Test

**Files:**
- Create: `internal/eval/simd_test.go`

**Step 1: Add one differential top-level test**

Create `TestSIMDScheduleMatchesScalar` with subcases over the deduplicated union
of:

```text
0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 65
CompareU32 threshold +/- 1
CompareI64 threshold +/- 1
PackMask threshold +/- 1
WordBitwise threshold * 64 +/- 1
```

For every row count:

- compare scalar and forced-SIMD symbol equal/not-equal leaves;
- compare all six integer and timestamp comparisons;
- compare packed Boolean equal/not-equal and `Exists` behavior;
- assert missing reasons and clean partial tails;
- compare `All`, `Any`, and `Not` over inputs containing True, False, Unknown,
  and Conflict;
- compare reason unions with every reason plane populated; and
- include exact truth-slot and reason-slot destination aliases.

Use current test fixture helpers and local scalar outputs. Keep `In` and evidence
in the Program and assert forced mode routes them to the existing scalar
kernels. Add one 1,024-row warm allocation assertion.

**Step 2: Run the acceptance test and verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestSIMDScheduleMatchesScalar$'
```

Expected: build failure because execution modes and SIMD schedule helpers do not
exist.

### Task 3: Add Mode-Aware SIMD Schedule Execution

**Files:**
- Create: `internal/eval/simd.go`
- Modify: `internal/eval/executor.go`
- Modify: `internal/eval/executor_bench_test.go`

**Step 1: Add internal execution modes and reusable scratch**

Define:

```go
type executionMode uint8

const (
    executionAuto executionMode = iota
    executionScalar
    executionSIMD
)
```

Add `compareMask []bool` to `Executor`, ordered after reviewing field alignment.
Move the current `Execute` body to an unexported mode-aware method; public
`Execute` delegates with `executionAuto`. Tests and benchmarks may call the
private method with forced modes.

Initialize one immutable package diagnostic from `simdops.Runtime()`. Auto mode
must not select SIMD when `PureGo` is true or `Tier == "scalar"`.

**Step 2: Implement compatible fact leaves**

Add an Executor method that returns false for unsupported scalar shapes and
otherwise writes the complete leaf output:

- symbols: `CompareU32`, then `PackMask`;
- integers/timestamps: `CompareI64`, then `PackMask`;
- Booleans: direct word `AndWords`/`AndNotWords` against presence;
- `Exists`: retain scalar copy/clear;
- `In`: return false for scalar fallback.

Map Program opcodes to `simdops.Comparison` once per instruction. Resolve the
constant and contiguous source column once. Resize `compareMask` only for a
selected typed comparison. Compute positive, negative, and missing words with
whole-slice operations and clear every inactive tail bit.

**Step 3: Implement truth and reason reductions**

Keep one driver-selection loop and branch once per group between scalar and SIMD
word operations. Use:

```text
AND positive = left positive AND right positive
AND negative = left negative OR right negative
OR  positive = left positive OR right positive
OR  negative = left negative AND right negative
reason union = destination OR source
```

Continue to use the existing `truth.Not` path. Preserve exact aliases and apply
the final row mask after each truth reduction.

**Step 4: Connect automatic selection**

Use the dependency-reported word threshold for word stages. Introduce one
temporary comparison-plus-pack crossover constant for measurement; forced modes
ignore it. Do not add thresholds for evidence or `In`.

**Step 5: Run focused correctness gates**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/eval -run '^TestSIMDScheduleMatchesScalar$'
go test -count=1 -timeout 60s -tags=purego ./internal/eval -run '^TestSIMDScheduleMatchesScalar$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval -run '^TestSIMDScheduleMatchesScalar$'
go test -count=1 -race -timeout 60s ./internal/eval -run '^TestSIMDScheduleMatchesScalar$'
```

Expected: PASS and zero warm allocations.

### Task 4: Measure And Set The Evaluator Crossover

**Files:**
- Create: `internal/eval/eval_bench_test.go`
- Modify: `internal/eval/simd.go`
- Modify: `internal/eval/executor_bench_test.go`
- Modify: `docs/performance.md`

**Step 1: Add paired benchmarks**

Benchmark forced scalar and forced SIMD for:

- one contiguous integer predicate plus mask packing;
- a truth/reason group over reusable words; and
- the existing deterministic end-to-end Executor fixture.

Use row counts `16, 32, 64, 128, 256, 512, 1024, 4096`. Prime every Executor
before timing and report allocations. Rename the existing end-to-end scalar
benchmark only if needed to keep its forced mode explicit.

**Step 2: Run interleaved repeated measurements**

Run once with repeated suites, bounded by the benchmark timeout:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkEvaluateBackends$' -benchmem -benchtime=200ms -count=6 ./internal/eval
```

Expected: `0 B/op`, `0 allocs/op` after priming. Compare paired minima and only
select a crossover where SIMD remains outside the measured noise floor at that
size and larger sizes.

**Step 3: Set and lock automatic selection**

Set the comparison-plus-pack crossover in `simd.go`. Add acceptance cases at
crossover minus one, crossover, and crossover plus one. Do not encode a claimed
gain for truth stages beyond the dependency's own threshold unless the paired
benchmark demonstrates one.

**Step 4: Record results**

Append CPU, selected tier, Go version, benchmark command, paired results,
crossover, and allocation counts to `docs/performance.md`. State unsupported or
slower shapes as scalar fallbacks rather than forcing SIMD.

### Task 5: Complete Task 19 Verification

**Files:**
- Verify all Task 19 files

**Step 1: Run package variants and static checks**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/eval ./internal/simdops
go test -count=1 -timeout 60s -tags=purego ./internal/eval ./internal/simdops
GOARCH=386 go test -count=1 -timeout 60s ./internal/eval ./internal/simdops
go test -count=1 -race -timeout 60s ./internal/eval ./internal/simdops
timeout 60s go vet ./internal/eval ./internal/simdops
timeout 60s go vet -tags=purego ./internal/eval ./internal/simdops
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/eval ./internal/simdops
```

Expected: PASS; analyzers print nothing.

**Step 2: Run the one full repository gate**

Run:

```bash
go test -count=1 -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace commands print nothing.

**Step 3: Commit only when explicitly requested**

When requested, stage the Task 18 facade work and Task 19 evaluator work as
separate commits so the dependency boundary remains reviewable.
