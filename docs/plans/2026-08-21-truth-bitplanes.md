# Four-Valued Truth Bitplanes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add allocation-free scalar `Not`, `And`, and `Or` operations over caller-owned positive and negative truth bitplanes.

**Architecture:** `internal/truth` exposes a non-owning `Planes` view whose two `[]uint64` columns encode four truth states. Dedicated scalar loops operate on exact caller-sized slices, support exact in-place aliases, validate shapes before writing, and clear unused bits in the final word. SIMD dispatch, reason masks, and scratch ownership remain later tasks.

**Tech Stack:** Go 1.27, standard library bit operations, table-driven tests, Go benchmarks

---

Read `docs/plans/2026-08-21-truth-bitplanes-design.md` before implementation.
Invoke `@superpowers:test-driven-development` before changing production code.
All test, build, and benchmark commands below have explicit timeouts. Do not run
them in loops or watch mode. Do not commit unless the user explicitly requests
it; the commit steps are checkpoints only.

### Task 1: Add The Non-Owning Plane View

**Files:**
- Create: `internal/truth/planes.go`
- Create: `internal/truth/planes_test.go`

**Step 1: Write the failing word-count test**

Create `internal/truth/planes_test.go`:

```go
package truth

import (
	"math"
	"testing"
)

func TestWordCount(t *testing.T) {
	tests := []struct {
		rows uint32
		want int
	}{
		{rows: 0, want: 0},
		{rows: 1, want: 1},
		{rows: 63, want: 1},
		{rows: 64, want: 1},
		{rows: 65, want: 2},
		{rows: 127, want: 2},
		{rows: 128, want: 2},
		{rows: 129, want: 3},
		{rows: math.MaxUint32, want: 67_108_864},
	}

	for _, tt := range tests {
		if got := WordCount(tt.rows); got != tt.want {
			t.Fatalf("WordCount(%d) = %d, want %d", tt.rows, got, tt.want)
		}
	}
}
```

**Step 2: Run the focused test to verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestWordCount$'
```

Expected: FAIL because `WordCount` does not exist.

**Step 3: Add the minimal representation**

Create `internal/truth/planes.go`:

```go
package truth

// Planes is a non-owning batch view over positive and negative truth bits.
type Planes struct {
	Positive []uint64
	Negative []uint64
}

// WordCount returns the number of uint64 words needed for rows truth values.
func WordCount(rows uint32) int {
	return int((uint64(rows) + 63) >> 6)
}
```

The `uint64` widening before addition is required for `math.MaxUint32`.

**Step 4: Run the focused test to verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestWordCount$'
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/truth/planes.go internal/truth/planes_test.go
git commit -m "feat: add truth plane representation"
```

### Task 2: Lock The Exhaustive Truth Tables

**Files:**
- Modify: `internal/truth/planes.go`
- Modify: `internal/truth/planes_test.go`

**Step 1: Add explicit scalar truth tables**

Append test-only state definitions and an exhaustive test to
`internal/truth/planes_test.go`:

```go
type testState uint8

const (
	testUnknown testState = iota
	testTrue
	testFalse
	testConflict
)

var testStateNames = [...]string{"unknown", "true", "false", "conflict"}

func oneRow(state testState) Planes {
	return Planes{
		Positive: []uint64{uint64(state) & 1},
		Negative: []uint64{uint64(state>>1) & 1},
	}
}

func stateAt(planes Planes, row uint32) testState {
	word := row >> 6
	bit := uint64(1) << (row & 63)
	positive := (planes.Positive[word] & bit) >> (row & 63)
	negative := (planes.Negative[word] & bit) >> (row & 63)
	return testState(positive | negative<<1)
}

func TestTruthTables(t *testing.T) {
	notWant := [...]testState{
		testUnknown,
		testFalse,
		testTrue,
		testConflict,
	}
	andWant := [4][4]testState{
		{testUnknown, testUnknown, testFalse, testFalse},
		{testUnknown, testTrue, testFalse, testConflict},
		{testFalse, testFalse, testFalse, testFalse},
		{testFalse, testConflict, testFalse, testConflict},
	}
	orWant := [4][4]testState{
		{testUnknown, testTrue, testUnknown, testTrue},
		{testTrue, testTrue, testTrue, testTrue},
		{testUnknown, testTrue, testFalse, testConflict},
		{testTrue, testTrue, testConflict, testConflict},
	}

	for state := testUnknown; state <= testConflict; state++ {
		t.Run("not/"+testStateNames[state], func(t *testing.T) {
			dst := oneRow(testUnknown)
			Not(dst, oneRow(state), 1)
			if got := stateAt(dst, 0); got != notWant[state] {
				t.Fatalf("Not(%s) = %s, want %s", testStateNames[state], testStateNames[got], testStateNames[notWant[state]])
			}
		})
	}

	binary := []struct {
		name string
		run  func(Planes, Planes, Planes, uint32)
		want [4][4]testState
	}{
		{name: "and", run: And, want: andWant},
		{name: "or", run: Or, want: orWant},
	}
	for _, op := range binary {
		for left := testUnknown; left <= testConflict; left++ {
			for right := testUnknown; right <= testConflict; right++ {
				t.Run(op.name+"/"+testStateNames[left]+"/"+testStateNames[right], func(t *testing.T) {
					dst := oneRow(testUnknown)
					op.run(dst, oneRow(left), oneRow(right), 1)
					if got := stateAt(dst, 0); got != op.want[left][right] {
						t.Fatalf("got %s, want %s", testStateNames[got], testStateNames[op.want[left][right]])
					}
				})
			}
		}
	}
}
```

The expected matrices are literals. Do not calculate expected values with the
production equations under test.

**Step 2: Run the truth-table test to verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestTruthTables$'
```

Expected: FAIL because `Not`, `And`, and `Or` do not exist.

**Step 3: Add minimal direct scalar loops**

Append to `internal/truth/planes.go`:

```go
// Not swaps positive and negative evidence for each row.
func Not(dst, src Planes, rows uint32) {
	words := WordCount(rows)
	for i := 0; i < words; i++ {
		dst.Positive[i] = src.Negative[i]
		dst.Negative[i] = src.Positive[i]
	}
}

// And combines two truth values under four-valued conjunction.
func And(dst, left, right Planes, rows uint32) {
	words := WordCount(rows)
	for i := 0; i < words; i++ {
		dst.Positive[i] = left.Positive[i] & right.Positive[i]
		dst.Negative[i] = left.Negative[i] | right.Negative[i]
	}
}

// Or combines two truth values under four-valued disjunction.
func Or(dst, left, right Planes, rows uint32) {
	words := WordCount(rows)
	for i := 0; i < words; i++ {
		dst.Positive[i] = left.Positive[i] | right.Positive[i]
		dst.Negative[i] = left.Negative[i] & right.Negative[i]
	}
}
```

Do not add allocation, generic callbacks, or SIMD calls.

**Step 4: Run the package tests to verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/truth/planes.go internal/truth/planes_test.go
git commit -m "feat: add four-valued truth operations"
```

### Task 3: Enforce Clean Tail Bits At Word Boundaries

**Files:**
- Modify: `internal/truth/planes.go`
- Modify: `internal/truth/planes_test.go`

**Step 1: Add boundary and dirty-tail tests**

Add helpers that construct patterned rows using the state order from Task 2.
For each row `i`, use `testState(i & 3)` on the left and
`testState((i*3 + 1) & 3)` on the right. Add one table test that:

```go
func TestOperationsClearTailBits(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65, 127, 128, 129} {
		// Build exact-sized left, right, and destination planes.
		// Set every valid row to the deterministic state patterns above.
		// Set every unused bit in the final source and destination words to 1.
		// Run Not, And, and Or in separate subtests.
		// Assert every valid row against notWant, andWant, or orWant.
		// Assert both destination tail masks contain zero unused bits.
	}
}
```

Use a test-local tail-mask helper rather than the production helper. Preserve a
copy of each out-of-place source and assert it was not changed.

**Step 2: Run the boundary test to verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsClearTailBits$'
```

Expected: FAIL for partial words because dirty high bits remain set.

**Step 3: Add one shared tail cleanup**

Append to `internal/truth/planes.go`:

```go
func maskTail(dst Planes, rows uint32) {
	remaining := rows & 63
	if remaining == 0 {
		return
	}
	last := len(dst.Positive) - 1
	mask := (uint64(1) << remaining) - 1
	dst.Positive[last] &= mask
	dst.Negative[last] &= mask
}
```

Call `maskTail(dst, rows)` once after each operation loop. Do not mask each word
or mutate source-only planes.

**Step 4: Run focused and package tests to verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsClearTailBits$'
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/truth/planes.go internal/truth/planes_test.go
git commit -m "fix: clear unused truth tail bits"
```

### Task 4: Support Exact In-Place Aliases

**Files:**
- Create: `internal/truth/scalar.go`
- Modify: `internal/truth/planes.go`
- Modify: `internal/truth/planes_test.go`

**Step 1: Add differential alias tests**

Add `clonePlanes` and equality helpers, then test 129 patterned rows for:

- `Not(dst, src)` compared with `Not(src, src)`.
- `And(dst, left, right)` compared with destination exactly equal to `left`.
- `And(dst, left, right)` compared with destination exactly equal to `right`.
- The same left and right alias cases for `Or`.
- Out-of-place operations leaving both sources unchanged.

Name the test `TestOperationsSupportExactAliasing`. Do not test shifted partial
overlaps; they are outside the contract.

**Step 2: Run the alias test to verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsSupportExactAliasing$'
```

Expected: FAIL for in-place `Not`, whose first store currently overwrites a
source word needed by its second store.

**Step 3: Add scalar word operations**

Create `internal/truth/scalar.go`:

```go
package truth

func notWord(positive, negative uint64) (uint64, uint64) {
	return negative, positive
}

func andWord(leftPositive, leftNegative, rightPositive, rightNegative uint64) (uint64, uint64) {
	return leftPositive & rightPositive, leftNegative | rightNegative
}

func orWord(leftPositive, leftNegative, rightPositive, rightNegative uint64) (uint64, uint64) {
	return leftPositive | rightPositive, leftNegative & rightNegative
}
```

**Step 4: Load all source words before destination stores**

Change each loop in `internal/truth/planes.go` to this shape:

```go
positive, negative := notWord(src.Positive[i], src.Negative[i])
dst.Positive[i] = positive
dst.Negative[i] = negative
```

For binary operations, pass all four indexed source words to `andWord` or
`orWord`, receive both results, then perform both stores. Keep dedicated loops;
do not introduce a function-valued kernel.

**Step 5: Run focused and package tests to verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsSupportExactAliasing$'
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 6: Commit only when requested**

```bash
git add internal/truth/scalar.go internal/truth/planes.go internal/truth/planes_test.go
git commit -m "feat: support in-place truth operations"
```

### Task 5: Reject Malformed Shapes Before Mutation

**Files:**
- Modify: `internal/truth/planes.go`
- Modify: `internal/truth/planes_test.go`

**Step 1: Add a complete malformed-shape matrix**

Use `rows = 65`, so the valid length is two words. Generate cases for every
positive and negative plane of every operand to be one word short and one word
long:

- `Not`: destination and source, 8 cases.
- `And`: destination, left, and right, 12 cases.
- `Or`: destination, left, and right, 12 cases.

For each case, retain the destination's full backing slices, fill them with
canaries, invoke the operation under a panic catcher, and assert:

1. A panic occurred.
2. Every destination backing word still equals its canary.

Name the test `TestOperationsRejectMalformedShapesBeforeWrite`.

**Step 2: Run the malformed-shape test to verify RED**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsRejectMalformedShapesBeforeWrite$'
```

Expected: FAIL. Long slices currently do not panic, and some short source cases
write destination words before the bounds panic.

**Step 3: Add constant-time preflight checks**

Add to `internal/truth/planes.go`:

```go
func requireShape(planes Planes, words int) {
	if len(planes.Positive) != words || len(planes.Negative) != words {
		panic("truth: plane length does not match row count")
	}
}
```

At the start of each public operation, calculate `words` once and call
`requireShape` for every operand before entering the loop. Check destination
first, followed by sources in signature order. Keep the panic string static and
do not use `fmt`.

**Step 4: Run focused and package tests to verify GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestOperationsRejectMalformedShapesBeforeWrite$'
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 5: Inspect the final production shape**

Confirm manually that:

- Each operation computes `WordCount` once.
- Every shape check happens before the first output store.
- Each loop loads all inputs for one word before either output store.
- Tail cleanup runs once after the loop.
- There are no maps, callbacks, interfaces, per-row objects, or allocations.

**Step 6: Commit only when requested**

```bash
git add internal/truth/planes.go internal/truth/planes_test.go
git commit -m "feat: validate truth plane shapes"
```

### Task 6: Add Allocation Benchmarks

**Files:**
- Create: `internal/truth/planes_bench_test.go`

**Step 1: Add the benchmark matrix**

Create benchmarks named under `BenchmarkTruth` for `Not`, `And`, and `Or` at
64, 65, 1,024, and 8,192 rows. Allocate and initialize exact-sized inputs and
destinations before `ResetTimer`, call `ReportAllocs`, and assign one destination
word to a package-level `uint64` sink after the timed loop.

Use direct operation functions in each sub-benchmark. Do not dispatch through a
function value inside the timed loop. The benchmark must measure only a complete
whole-slice operation.

**Step 2: Run the benchmark suite**

Run:

```bash
timeout 150s go test -timeout 120s -run '^$' -bench '^BenchmarkTruth$' -benchmem -benchtime=1s -count=3 ./internal/truth
```

Expected: every sub-benchmark reports `0 B/op` and `0 allocs/op`. Record the
three-run range and `ns/row`; do not set an absolute latency gate from one host.

**Step 3: Run escape analysis**

Run:

```bash
timeout 120s go build -gcflags=-m ./internal/truth
```

Expected: no valid operation allocates or causes caller-owned plane storage to
escape through the truth package.

**Step 4: Record measurements**

Append the date, Go version, OS/architecture, CPU, benchmark command, measured
ranges, and allocation counts to
`docs/plans/2026-08-21-truth-bitplanes-design.md`. Keep the table factual and do
not claim SIMD performance from scalar loops.

**Step 5: Commit only when requested**

```bash
git add internal/truth/planes_bench_test.go docs/plans/2026-08-21-truth-bitplanes-design.md
git commit -m "perf: benchmark truth bitplanes"
```

### Task 7: Run Final Verification And Review

**Files:**
- Review: `internal/truth/planes.go`
- Review: `internal/truth/scalar.go`
- Review: `internal/truth/planes_test.go`
- Review: `internal/truth/planes_bench_test.go`
- Review: `docs/plans/2026-08-21-truth-bitplanes-design.md`

**Step 1: Format the Task 8 files**

Run:

```bash
timeout 30s gofmt -w internal/truth/planes.go internal/truth/scalar.go internal/truth/planes_test.go internal/truth/planes_bench_test.go
```

Expected: command exits zero.

**Step 2: Run focused tests**

Run:

```bash
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 3: Run race tests**

Run:

```bash
go test -race -count=1 -timeout 120s ./internal/truth
```

Expected: PASS.

**Step 4: Run the repository suite**

Run:

```bash
go test -count=1 -timeout 60s ./...
```

Expected: PASS.

**Step 5: Run static checks**

Run:

```bash
timeout 120s go vet ./...
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
git diff --check
```

Expected: all commands exit zero; `gofmt -l .` and `go mod tidy -diff` print
nothing.

**Step 6: Request code review**

Invoke `@superpowers:requesting-code-review`. Review specifically for wrong
four-valued matrices, dirty-tail leaks, partial destination writes on panic,
unsupported aliases accidentally implied by comments, integer overflow, and
hidden allocations. Fix confirmed findings with a new RED/GREEN cycle and rerun
Steps 1 through 5.

**Step 7: Commit only when requested**

```bash
git add internal/truth docs/plans/2026-08-21-truth-bitplanes-design.md docs/plans/2026-08-21-truth-bitplanes.md
git commit -m "feat: add four-valued truth planes"
```
