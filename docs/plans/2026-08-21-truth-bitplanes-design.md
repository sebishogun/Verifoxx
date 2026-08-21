# Four-Valued Truth Bitplanes Design

## Scope

Task 8 provides the scalar, allocation-free truth kernel used by later evaluator
and SIMD stages. It represents true, false, unknown, and conflict for a batch of
rows without per-row objects. Reason masks, evaluator scratch ownership, and
SIMD dispatch remain separate tasks.

## Representation And Ownership

The package exposes a non-owning view over two contiguous bitplanes:

```go
type Planes struct {
    Positive []uint64
    Negative []uint64
}
```

For one row, `(positive, negative)` means:

| Positive | Negative | Meaning |
|---:|---:|---|
| 0 | 0 | Unknown |
| 1 | 0 | True |
| 0 | 1 | False |
| 1 | 1 | Conflict |

Callers own and size all storage. `WordCount(rows uint32) int` returns the exact
number of words required. Operations neither allocate nor grow slices:

```go
func Not(dst, src Planes, rows uint32)
func And(dst, left, right Planes, rows uint32)
func Or(dst, left, right Planes, rows uint32)
```

Every positive and negative slice must have length `WordCount(rows)`. Shape
checks happen before any output write and panic on a mismatch because malformed
scratch is an internal evaluator defect, not a recoverable policy condition.

## Operations

The scalar word equations are:

```text
NOT: positive = child.negative
     negative = child.positive

AND: positive = left.positive AND right.positive
     negative = left.negative OR right.negative

OR:  positive = left.positive OR right.positive
     negative = left.negative AND right.negative
```

`scalar.go` holds the word equations. `planes.go` holds dedicated whole-slice
loops. A callback-based shared loop is excluded so each operation remains a
simple compiler-visible kernel and a direct future SIMD replacement point.

## Aliasing

Each iteration loads every source word before writing either destination word.
The destination may therefore be exactly the same `Planes` value as the unary
source or either binary source. This supports liveness-assigned in-place slot
reuse without a temporary plane.

The positive and negative destination planes must be distinct. Shifted partial
overlap is unsupported; detecting arbitrary overlap would require unsafe
address inspection and would add work to every operation. Out-of-place
operations leave their sources unchanged.

## Tail Invariant

`rows` defines the logical width independently of slice capacity. After every
externally visible operation, bits at indexes greater than or equal to `rows`
are zero in both destination planes. Zero rows touch no storage. Exact multiples
of 64 retain the complete final word; partial final words are masked after the
kernel. Dirty source or destination tails cannot escape into a result.

## Tests And Measurement

Tests exhaust all four unary states and all 16 ordered state pairs for `And`
and `Or`. Boundary coverage uses 0, 1, 63, 64, 65, 127, 128, and 129 rows and
includes dirty tails. Alias tests compare out-of-place results with in-place
unary, left, and right execution. Shape tests vary every operand plane, require
a pre-write panic, and verify that the destination remains unchanged.

Benchmarks run `Not`, `And`, and `Or` over representative batch sizes using
preallocated storage and setup outside the timer. The required steady-state
result is the `-benchmem` allocation metrics `0 B/op` and `0 allocs/op`, not
zero memory traffic; throughput is reported separately as MB/s via `SetBytes`.
Focused tests, race tests, repository tests, benchmarks, and escape analysis
all run with explicit timeouts.

## Measurements (2026-08-21)

Environment: `go1.27.0 linux/amd64`, AMD RYZEN AI MAX+ 395 w/ Radeon 8060S
(strix-halo), `-cpu=1`.

Exact command:
`go test -timeout 120s -run '^$' -bench '^BenchmarkTruth$' -benchmem -benchtime=1s -count=3 -cpu=1 ./internal/truth`

Median of three runs (range in parentheses), all cases `0 B/op`, `0 allocs/op`:

| Case | words | ns/op (median, range) | ns/word | MB/s |
|------|-------|-----------------------|---------|------|
| Not/Rows64   |   1 | 4.566 (4.546-4.601)  | 4.57 | 7 009 |
| And/Rows64   |   1 | 5.058 (5.006-5.099)  | 5.06 | 9 489 |
| Or/Rows64    |   1 | 5.071 (5.068-5.120)  | 5.07 | 9 465 |
| Not/Rows65   |   2 | 9.127 (9.081-9.170)  | 4.56 | 7 012 |
| And/Rows65   |   2 | 10.21 (10.19-10.24)  | 5.10 | 9 406 |
| Or/Rows65    |   2 | 10.19 (10.18-10.20)  | 5.10 | 9 419 |
| Not/Rows1024 |  16 | 7.399 (7.355-7.519)  | 0.462 | 69 198 |
| And/Rows1024 |  16 | 10.46 (10.44-10.60)  | 0.654 | 73 399 |
| Or/Rows1024  |  16 | 10.37 (10.32-10.44)  | 0.648 | 74 059 |
| Not/Rows8192 | 128 | 37.11 (35.71-37.64)  | 0.290 | 110 360 |
| And/Rows8192 | 128 | 46.86 (46.86-47.10)  | 0.366 | 131 112 |
| Or/Rows8192  | 128 | 46.40 (46.36-47.26)  | 0.362 | 132 417 |

`SetBytes` counts kernel-loop traffic: Not = `words*32` (2 loads + 2 stores),
And/Or = `words*48` (4 loads + 2 stores). For partial rows it excludes the
fixed final-word read-modify-writes in `maskTail`.

### Bounds-check A/B (rows 8192)

Baseline (A) indexed the plane fields in the loop and retained 3 bounds branches
per word in `Not` and 5 in `And`/`Or` despite `requireShape`. Candidate B added
exact full-slice locals after all shape checks (`dst.Positive[:words:words]`,
etc.) and used only those in the kernel, preserving read-all-before-store alias
behavior and a single `maskTail` call. Three interleaved pairs with
`-count=1`, focused command:
`go test -timeout 45s -run '^$' -bench '^BenchmarkTruth/(Not|And|Or)/Rows8192$' -benchmem -benchtime=1s -count=1 -cpu=1 ./internal/truth`

| Run | Not A->B (ns/op) | And A->B (ns/op) | Or A->B (ns/op) |
|-----|------------------|------------------|-----------------|
| 1 | 88.57 -> 33.67 (2.63x) | 134.5 -> 48.02 (2.80x) | 135.9 -> 46.50 (2.92x) |
| 2 | 89.70 -> 36.05 (2.49x) | 135.2 -> 46.51 (2.91x) | 132.8 -> 45.69 (2.91x) |
| 3 | 90.20 -> 33.63 (2.68x) | 132.8 -> 47.66 (2.79x) | 133.8 -> 47.55 (2.81x) |

B was faster in every paired run, 2.49x-2.92x, with the same sign across all
three kernels, so candidate B is kept because every pair exceeds the
preselected 5% retention threshold. B's disassembly has no per-word
bounds-panic branches and uses loop-local exact slice bases. This experiment
does not isolate each codegen effect's contribution; no claim is made about
which effect drives the difference.

Escape analysis (`go build -gcflags=-m ./internal/truth`) shows no data escapes;
only the `requireShape` panic string literal escapes. `go tool objdump` on the
built test binary shows `Not`'s kernel as two loads + two stores with only the
loop-counter compare and no per-word bounds-panic call or branch. These are
scalar kernels; no SIMD is used and no SIMD performance is claimed.
