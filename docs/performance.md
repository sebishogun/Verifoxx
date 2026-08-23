# Performance

## SIMD Boundary

Verifoxx pins `github.com/sebishogun/simd` v1.21.0 and reaches it only through
`internal/simdops`. The facade hands over complete contiguous slices; it never
calls a function per row or per element.

Normal builds use the library's startup-selected assembly tier and measured
kernel guards. `-tags=purego` selects direct Go loops with the same semantics.
The slice API does not require `GOEXPERIMENT=simd`. The pinned release's
experiment-gated vector-type file still uses Go 1.26 `archsimd` names and does
not compile against Go 1.27; Verifoxx does not import that API. Re-enable that
build only after a compatible dependency release.

`simdops.Runtime()` reports the selected tier, the dependency's diagnostic
description, whether the facade was built as pure Go, and current thresholds:

| Wrapper family | v1.21 operation | Threshold unit |
|---|---|---|
| `CompareU32` | scalar-mask comparisons over `uint32` | rows |
| `CompareI64` | scalar-mask comparisons over `int64` | rows |
| word bitwise | byte `And`/`Or`/`Xor`/`AndNot` | uint64 words |
| Boolean mask bitwise | v1.21 mask-family guard | mask elements |
| `CompressU32` | `CompressInto[int32]` | rows |
| `PackMask` | `MaskBits` over Boolean bytes | rows |

Threshold values are architecture-specific and come from
`simd.KernelThreshold`; the facade does not duplicate them or add another
crossover. The word threshold is the byte threshold rounded up to uint64 words.
Scalar-only architectures correctly report zero for every threshold.
Under `purego` on a kernel-capable architecture, the dependency intentionally
reports the normal build's guard values even though `Tier` is `scalar`; callers
must use `PureGo` and `Tier`, not threshold values, to identify the active
backend.
The pinned release has no distinct public threshold keys for bitwise-mask
operations. Its generated mask operations share one guard, so `BoolBitwise`
uses the public `All`, `Any`, and `Count` mask-family keys instead of duplicating
that constant.
## Data And Ownership

The evaluator retains typed `schema` IDs and uint64 bitplanes. Private,
allocation-free views inside `internal/simdops` reinterpret:

- defined `~uint32` IDs as `uint32` for unsigned comparison;
- defined `~int64` values as `int64` for ordered comparison;
- defined `~uint32` IDs as `int32` for stable compression;
- Boolean comparison masks as bytes for bit packing; and
- uint64 bitplanes as bytes for bitwise operations.

Compression only copies selected element bits, so the signed view does not
change values or ordering. Byte-wise bit operations produce the same uint64
result on every endian because each output bit depends only on the corresponding
input bits.

Go 1.27 stores each valid Boolean as one canonical zero or one byte. On
little-endian hosts, `PackMask` passes that private byte view to `MaskBits`,
whose least-significant-bit-first output is the evaluator's uint64 bitplane
layout. Big-endian hosts use the allocation-free portable packer. Both paths
validate capacity before mutation, overwrite every active word, and clear
unused final-word bits.

No hot-path operation wrapper allocates. Comparison and compression
destinations are caller-owned; `Runtime` is a cold diagnostic call.
The supported alias contracts are:

- word and Boolean-mask destinations may exactly alias either input;
- Boolean NOT may operate in place;
- comparisons do not overlap their differently typed source;
- mask packing does not overlap its differently typed source;
- compression does not support source/destination overlap; and
- shifted partial overlap is unsupported.

All operations process the shortest input. Compression preserves source order,
stops at destination capacity, and returns the number written. Invalid
comparison modes panic before mutating output.

## Evaluator Crossovers

Measured on 2026-08-22 with Go 1.27.0 on Linux/amd64, an AMD Ryzen AI MAX+
395, and the `avx512` tier. Values below are the minimum of six runs; every
case reported `0 B/op` and `0 allocs/op` after priming.

| Stage | Rows | Scalar ns/op | SIMD ns/op | Result |
|---|---:|---:|---:|---:|
| typed compare + pack | 63 | 44.60 | 43.77 | equal within spread |
| typed compare + pack | 64 | 43.63 | 34.83 | SIMD 1.25x |
| typed compare + pack | 65 | 47.96 | 40.09 | SIMD 1.20x |
| typed compare + pack | 1,024 | 562.2 | 72.90 | SIMD 7.71x |
| Boolean predicate | 448 | 26.50 | 28.29 | scalar 1.07x |
| Boolean predicate | 449 | 28.68 | 26.92 | SIMD 1.07x |
| Boolean predicate | 4,096 | 161.8 | 76.97 | SIMD 2.10x |
| truth + all-reason union | 448 | 19.54 | 22.78 | scalar 1.17x |
| truth + all-reason union | 449 | 21.98 | 17.60 | SIMD 1.25x |
| truth + all-reason union | 1,024 | 43.96 | 21.00 | SIMD 2.09x |
| truth + all-reason union | 4,096 | 148.5 | 34.53 | SIMD 4.30x |

The automatic evaluator therefore selects typed comparison plus packing at 64
rows, and packed Boolean/truth/reason operations at eight uint64 words (449
rows). Those are composite evaluator costs, not replacements for the library's
individual guards. `In`, `Exists`, evidence CSR reduction, `purego`, and a
runtime scalar tier retain the direct scalar paths. Row-wise outcome resolution
dominates the current end-to-end fixture, whose scalar/SIMD difference remained
inside run-to-run spread; no end-to-end throughput gain is claimed yet.

`go run github.com/sebishogun/simd/cmd/simdinfo` reported
`amd64 tier=avx512 available=[scalar sse2 avx2 avx512]`. Test-binary
disassembly shows word operations loading the dependency's `tierIdx` dispatch
table and calling the selected function indirectly; typed comparisons likewise
resolve their operation through the dependency's selected operation table.

Measurement commands:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkEvaluateBackends$' -benchmem -benchtime=200ms -count=6 ./internal/eval
go test -timeout 60s -run='^$' -bench='^BenchmarkEvaluateBackends/predicate/rows=(63|64|65)/' -benchmem -benchtime=500ms -count=6 ./internal/eval
go test -timeout 60s -run='^$' -bench='^BenchmarkEvaluateBackends/(boolean|truth-reasons)/rows=(448|449)/' -benchmem -benchtime=500ms -count=6 ./internal/eval
```

## Reused Fact Bitmap Indexes

Measured on the same 2026-08-22 host and runtime tier. The compiler counts
full-column symbol comparisons in the final CSE-normalized schedule: `Equal`
and `NotEqual` count once, and each `In` value counts once. It emits a fact
specification at 96 uses. Automatic execution builds it at 64 rows or more;
smaller batches and fields below 96 uses retain their existing direct path.

The raw benchmark includes one column scan to build all selected value masks,
then one lookup and mask copy per use. Values are the minimum of six runs:

| Rows | Dense direct ns/op | Dense indexed ns/op | Sparse direct ns/op | Sparse indexed ns/op | Weakest gain |
|---:|---:|---:|---:|---:|---:|
| 64 | 1,223 | 997.5 | 1,282 | 1,039 | 1.23x |
| 256 | 1,468 | 1,188 | 1,527 | 1,250 | 1.22x |
| 1,024 | 2,272 | 2,058 | 2,324 | 2,002 | 1.10x |
| 4,096 | 6,333 | 5,565 | 6,239 | 5,238 | 1.14x |

At 80 uses, the dense 1,024-row minima were 1,819 ns/op direct and 1,814
ns/op indexed, with overlapping six-run spreads. The conservative 96-use
threshold avoids encoding that marginal region. Boundary cases at 95, 96, and
97 uses remain in `BenchmarkBatchIndex`; compiler tests lock exclusion at 95
and inclusion at 96 and 97. The 64-row automatic threshold is the first shape
measured with the complete evaluator, not a claim about smaller batches.

`BenchmarkEvaluateFactIndex` includes index construction, selected and fallback
leaf execution, truth/reason composition, applicability lookup, outcome
resolution, and result materialization. Its direct backend forces supported
whole-slice SIMD and disables the fact index; automatic mode uses the fact
index and normal SIMD selection elsewhere:

| Rows | Dense direct ns/op | Dense auto ns/op | Gain | Sparse direct ns/op | Sparse auto ns/op | Gain |
|---:|---:|---:|---:|---:|---:|---:|
| 64 | 7,443 | 6,086 | 1.22x | 7,341 | 5,835 | 1.26x |
| 256 | 30,405 | 20,408 | 1.49x | 27,068 | 19,366 | 1.40x |
| 1,024 | 123,263 | 78,436 | 1.57x | 104,471 | 73,900 | 1.41x |
| 4,096 | 486,385 | 308,087 | 1.58x | 411,921 | 290,705 | 1.42x |

Every raw and complete case reported `0 B/op` and `0 allocs/op` after priming.
Forced indexed mode disables SIMD and uses scalar fallback for unselected
leaves, keeping its cost independently measurable.

For `Q` unique queried symbols across selected fields and `R` batch rows, the
mutable mask slab is exactly:

```text
8 * Q * ceil(R / 64) bytes
```

The executor also retains a dense construction table of
`4 * (ProgramSymbolCount + 1)` bytes. Immutable Program payload is `20` bytes
per selected field plus `4` bytes per unique queried symbol, excluding slice
headers. None of these terms depends on observed batch cardinality. The
complete fixture has two queried values, so its 4,096-row mask slab is 1,024
bytes.

Measurement commands:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkBatchIndex/(dense|sparse)/rows=(64|256|1024|4096)/uses=(80|96)/(direct|indexed)$' -benchmem -benchtime=500ms -count=6 ./internal/index
go test -timeout 120s -run='^$' -bench='^BenchmarkEvaluateFactIndex$' -benchmem -benchtime=500ms -count=6 ./internal/eval
```

## Fixed Worker Scheduler

Measured on the same host with the compiled Verifoxx policy, repeated canonical
request/evidence rows, fixed worker goroutines, private shard results, and the
complete deterministic CSR merge. Values are the minimum of six runs after
priming every worker context and result slot. Every case reported `0 B/op` and
`0 allocs/op`.

| Rows | Workers | Direct ns/op | Scheduled serial ns/op | Parallel ns/op | Parallel gain |
|---:|---:|---:|---:|---:|---:|
| 64 | 2 | 13,229 | 15,113 | 15,156 | 0.87x |
| 64 | 4 | 13,304 | 15,310 | 15,253 | 0.87x |
| 256 | 2 | 51,427 | 56,928 | 37,725 | 1.36x |
| 256 | 4 | 51,206 | 57,449 | 30,688 | 1.67x |
| 1,024 | 2 | 208,948 | 238,708 | 150,399 | 1.39x |
| 1,024 | 4 | 221,676 | 245,854 | 107,084 | 2.07x |
| 4,096 | 2 | 850,500 | 958,337 | 594,163 | 1.43x |
| 4,096 | 4 | 873,927 | 949,407 | 425,001 | 2.06x |
| 16,384 | 2 | 3,596,793 | 4,012,004 | 2,483,113 | 1.45x |
| 16,384 | 4 | 3,650,272 | 4,207,342 | 1,459,117 | 2.50x |

Automatic scheduling starts at 256 rows. At that boundary the slowest
two-worker parallel sample was 44,050 ns/op, below the fastest corresponding
direct sample at 51,427 ns/op. The 64-row parallel range was slower than direct
execution in all six samples. One-worker scheduling remains the direct caller
path; `ParallelRows` can override the default for tests and controlled tuning.
Every non-final shard starts and ends on a 64-row bitset boundary; only the
final shard may end in a partial word.

Measurement command:

```bash
go test -timeout 120s -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=300ms -count=6 ./internal/scheduler
```

## Explanation Materialization

Measured on the same host with one primed caller-owned destination. The fixture
renders a rationale, two evidence issues, two assumptions, two uncertainty
entries, and five structured remediations while exercising every placeholder
and value kind. The minimum of six runs was 717.1 ns/op; every run reported
`0 B/op` and `0 allocs/op`. Catalog binding, policy evaluation, and JSON encoding
are excluded.

Measurement command:

```bash
go test -timeout 90s -run='^$' -bench='^BenchmarkExplainer$' -benchmem -benchtime=500ms -count=6 ./internal/result
```

## Machine-Readable Result Encoding

Measured on 2026-08-22 on the same host with the compiled production policy, all five
canonical decisions, a bound and primed Encoder, and a caller-owned destination
sized to the golden document. The minimum of six runs was 2,744 ns/op (1,567.11
MB/s); every run reported `0 B/op` and `0 allocs/op`. The measurement includes
explanation materialization, provenance validation, JSON escaping, indentation,
and all structured remediation fields. Policy compilation and evaluation are
excluded.

Measurement command:

```bash
go test -run='^$' -bench='Encode' -benchmem -benchtime=500ms -count=6 -timeout 90s ./internal/adapters/jsonresult
```

## Product CLI Cold Path

Measured on 2026-08-23 on the same host and `avx512` runtime tier. These
benchmarks intentionally rebuild the policy Program and decode the canonical
five-row batch on every iteration; they are cold adapter measurements, not the
zero-allocation steady-state evaluator contract.

| Path | ns/op | Input MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| demo pipeline, excluding Cobra | 85,476 | 219.43 | 124,824 | 701 |
| complete `verifoxx demo` command | 106,525 | 176.07 | 166,393 | 849 |
| complete `verifoxx evaluate` command | 98,467 | 190.48 | 181,578 | 827 |

The demo compiles one immutable Program, decodes one SoA batch, evaluates the
five baseline rows, materializes their explanations, then compacts and
evaluates R3 and R2 once each. It does not launch subprocesses or reparse input
for either scenario. A 2 KiB destination capacity hint removed five report
growth allocations and 1,216 B/op. Across six 300 ms runs, the minimum moved
from 95,349 ns/op to 85,527 ns/op; the retained path reports 701 allocations,
all on this once-per-command cold path. The table reports the minimum of six
300 ms runs; the two complete-command ranges overlap, so no relative speed
claim is made between them.

Fresh policy decoding measured 10,519 ns/op and 126 allocations; reuse measured
6,023 ns/op with zero allocations. Warm batch decoding measured 8,512 ns/op,
result encoding 2,962 ns/op, and one explanation 717.1 ns/op, all with zero
allocations. Compilation is included in the complete CLI numbers but was not
isolated in this task.

A `-trimpath` binary completed `demo`, `evaluate`, `compile`, `explain`, and
`simulate` in 2-3 ms each using the shell's 1 ms timer resolution. Process
startup is therefore larger than the approximately 0.1 ms in-process command
work. No daemon, disk cache, or serialized Program is warranted for this
offline command surface.

Measurement commands:

```bash
go test -timeout 120s -run='^$' -bench='Benchmark(DemoPipeline|DemoCommand|EvaluateCommand)$' -benchmem -benchtime=300ms -count=6 ./internal/adapters/cli
go test -timeout 120s -run='^$' -bench='Benchmark(DecodeFullPolicyFresh|DecoderFullPolicyReuse)$' -benchmem ./internal/adapters/jsonpolicy
go test -timeout 120s -run='^$' -bench='BenchmarkDecodeBatch$' -benchmem ./internal/adapters/jsonbatch
timeout 120s go build -trimpath -o /tmp/opencode/verifoxx-task26 ./cmd/verifoxx
```

## Verification

`internal/simdops/ops_test.go` differentially checks the accelerated and pure-Go
backends against local scalar references at empty, tail, unaligned, alias, and
kernel-boundary shapes. It also checks warm allocation count and runtime
metadata under normal, `purego`, and 386 scalar builds. Task 19 adds
evaluator-level differential tests over every supported predicate, all four
truth states, every reason plane, exact aliases, partial tails, scalar fallback,
and the two measured crossover boundaries.
Task 20 adds exact fact-mask, malformed-spec, unknown-extension, direct/SIMD/
indexed differential, measured row-boundary, poisoned reuse, and warm-allocation
coverage.
