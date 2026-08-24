# Performance

## Methodology

Performance claims use bounded, reproducible commands and retain the machine
context needed to interpret them:

1. Record both commit IDs, Go version, `GOOS/GOARCH`, CPU, `GOMAXPROCS`, runtime
   SIMD tier, build tags, benchmark regex, benchtime, and sample count.
2. Generate fixtures, construct Programs and batches, allocate destinations,
   and prime reusable storage before the timer unless setup cost is the stated
   subject.
3. Use `-benchmem`; steady-state evaluator, scheduler, explanation, and encoder
   kernels must report `0 B/op` and `0 allocs/op`.
4. Collect at least six samples on a quiet host. Use all samples with `benchstat`
   for a comparison; do not select one favorable run. Tables in this document
   that report a minimum say so explicitly.
5. Build baseline and candidate test binaries before measurement and alternate
   A/B then B/A execution order. Do not switch or rebuild a checkout between
   samples.
6. Confirm the selected runtime backend through `simdops.Runtime` and, for
   kernel claims, through disassembly or hardware counters.
7. Never use `-tags=debug` or `-gcflags=all='-N -l'` for a performance result.

Correctness is mandatory, and measured performance is a coequal acceptance
constraint after correctness. A shared abstraction is rejected when an
interleaved comparison shows a statistically significant regression in an
affected production path. Small local specializations are retained when they
are faster; source-level deduplication is not worth slower machine behavior.

The standard evaluator run is:

```bash
./cli/devx bench
```

Interleaved comparison requires prebuilt baseline and current binaries plus the
environment variables documented below:

```bash
./cli/devx bench:compare
```

Linux hardware-counter inspection of the cold product path is available as:

```bash
./cli/devx perf
```

Use the explicit prebuilt-binary `perf stat` command later in this guide for a
kernel comparison so compilation remains outside the measured process.

## Refactor Acceptance And Code Layout

The reusable-path audit on 2026-08-24 consolidated byte-identical adapter work
without moving per-row evaluator storage helpers across package boundaries:

- canonical JSON-string and SHA-256 encoding is shared by the CLI and result
  adapters, and SHA-256 decoding is shared by the HTTP and PostgreSQL adapters;
- policy catalog name lookup is shared inside the policy JSON decoder;
- `jsonbatch.resizeZero`, `eval.resizeClear`, `index.resizeIndex`, and
  `compile.resizeSlots` remain local specializations.

All retained wire paths preserved their allocation counts. Interleaved runs
found no significant change for result encoding (2.830 versus 2.835 us/op,
`p=0.818`), policy decoding fresh (10.061 versus 9.847 us/op, `p=0.310`) or on
reuse (6.003 versus 6.079 us/op, `p=0.699`), or the three affected CLI paths.

The local resize functions are behaviorally identical, but replacing them with
one generic function in `internal/arena` changed Go 1.27 amd64 machine layout.
The direct `eval.Builder.Begin` comparison regressed by 2.00% (`p=0.002`),
and the linked `jsonbatch.BenchmarkDecodeBatch` consumer regressed by 12.44%
(`p<0.001`), with `0 B/op` and `0 allocs/op` in both variants. Restoring the
local compiler helper returned `ValidateCatalogUniqueScaling/Rows128` to parity
at 36.11 versus 36.09 us/op (`p=0.699`).

Disassembly isolated the mechanism. The local and shared `Builder.Begin`
variants each contained 539 non-NOP instructions and 41 calls. The local
variant was 2,793 bytes with 13 NOP instructions occupying 30 bytes. The shared
generic variant was 2,821 bytes with 27 NOP instructions occupying 58 bytes.
Nested cross-package inlining introduced unmatched `OpInlMark` positions;
`cmd/compile/internal/ssagen` materialized them as hardware NOPs, and the amd64
assembler's fused-jump boundary rule inserted further padding to avoid crossing
or ending at a 32-byte boundary. The net growth was 14 NOP instructions and 28
bytes, not additional semantic work.

Linker alignment rounded that growth into a `0x20` address shift for the
otherwise byte-identical JSON decoder functions that followed it. A controlled
binary retained the local `Builder.Begin` byte-for-byte and introduced
only the same downstream `0x20` shift; `BenchmarkDecodeBatch` then regressed by
13.19% (`p<0.001`). This reproduced the downstream cost without the shared
helper.

On the AMD Ryzen AI MAX+ 395 host, 50,000 controlled decodes changed the Zen 5
operation-cache counters as follows:

| Layout | op-cache hits | op-cache misses | accesses | miss rate |
|---|---:|---:|---:|---:|
| local | 2.034B | 125.206M | 2.159B | 5.8% |
| local plus `0x20` shift | 2.136B | 477.733M | 2.614B | 18.3% |

Retired branch mispredicts rose from 4.677M to 5.118M. A separate 200,000-run
comparison retired the same approximately 47.67B instructions while the shifted
layout consumed 9.06B instead of 8.19B cycles. The regression is therefore an
address-sensitive Zen 5 operation-cache and branch-predictor layout effect.

Hot refactors must consequently compare prebuilt linked consumers, not only the
edited helper. Source equivalence, zero allocations, equal call counts, and an
unchanged non-NOP instruction count do not establish performance parity.

## Semantic Graph Visualization

The debugger graph path is outside evaluator kernels. Graph construction is
cold; the reusable layouter, active-path calculation, caller-buffer terminal
renderer, DOT/SVG/HTML exporters, and fixed-capacity browser-state publication
must allocate zero after priming. Browser listener, HTTP, opener, and file I/O
remain cold adapter boundaries.

Measured on 2026-08-24 with Go 1.27.0 on Linux/amd64 and the AMD Ryzen AI MAX+
395. Values are the minimum of six 300 ms samples:

| Operation | Shape | Minimum ns/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| deterministic layout | 64 nodes | 3,146 | 0 | 0 |
| deterministic layout | 256 nodes | 12,472 | 0 | 0 |
| deterministic layout | 1,024 nodes | 51,344 | 0 | 0 |
| colored terminal graph | 256 nodes, 255 edges, 120x40 | 65,012 | 0 | 0 |

Allocation tests separately prime and repeat DOT, SVG, interactive HTML, live
HTML, terminal rendering with active-path calculation, and browser-state
publication 100 times. Every path reports `0 allocs/run` with caller-owned
capacity or fixed storage.

```bash
timeout 180s go test -run='^$' -bench='^(BenchmarkLayout|BenchmarkGraphRenderer)$' -benchmem -benchtime=300ms -count=6 -timeout=120s ./internal/graphview ./internal/adapters/tui
timeout 180s go test -run='^$' -bench='^BenchmarkEvaluate$' -benchmem -benchtime=300ms -count=6 -timeout=120s ./internal/eval
```

The linked evaluator comparison used prebuilt binaries from `08bd81f` and the
graph candidate, alternated A/B then B/A for six 200 ms rounds. Evaluator
geomean changed from 91.21 to 91.11 microseconds (`-0.10%`). No individual case
was statistically significant (`p>=0.132`), and every baseline and candidate
case retained `0 B/op` and `0 allocs/op`.

```bash
timeout 300s scripts/bench-compare.sh /tmp/verifoxx-baseline.test /tmp/verifoxx-current.test '^BenchmarkEvaluate$' 6 200ms
```

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

## Controlled Evaluation Harness

`BenchmarkEvaluate` measures complete evaluator execution over deterministic,
exact-size typed columns generated before the timer starts. Every benchmark name
records the runtime SIMD tier, rows, policy nodes, evidence percentage, match
percentage, worker count, and forced mode. The reported `rows`, `nodes`,
`evidence_pct`, `match_pct`, and `workers` metrics repeat those dimensions in
machine-readable benchmark output.

The three forced modes isolate scalar leaf execution, SIMD leaf execution, and
fact-index execution without exposing a production tuning API. Each case is
primed before timing, reuses executor and result storage, and is checked against
the scalar result. Data generation, Program construction, batch construction,
and the equality checks are excluded from the timed region. The evaluator loop
must report `0 B/op` and `0 allocs/op`:

```bash
timeout 180s go test -run='^$' -bench='^BenchmarkEvaluate$' -benchmem -benchtime=200ms -count=6 -timeout=120s ./internal/eval
./cli/devx bench
```

Parallel scheduling is intentionally a separate measurement. Its names carry
the active SIMD tier plus rows and workers, and distinguish direct,
scheduled-serial, and scheduled-parallel execution:

```bash
timeout 180s go test -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=300ms -count=6 -timeout=120s ./internal/scheduler
```

### Interleaved A/B Comparison

Build one evaluator test binary from each source tree before measuring. Use
separate worktrees so the comparison never switches or modifies a checkout:

```bash
timeout 120s env GOWORK=off go test -mod=readonly -c -o /tmp/verifoxx-baseline.test ./internal/eval  # run in baseline worktree
timeout 120s env GOWORK=off go test -mod=readonly -c -o /tmp/verifoxx-current.test ./internal/eval   # run in current worktree
timeout 300s scripts/bench-compare.sh /tmp/verifoxx-baseline.test /tmp/verifoxx-current.test '^BenchmarkEvaluate$' 6 200ms
```

The script validates both prebuilt binaries, alternates A/B then B/A by round,
bounds every invocation, keeps samples in separate temporary files, and calls
`benchstat` once. It does not build, edit, or switch either source tree. Six
rounds is the minimum accepted sample count. The devx wrapper accepts the same
inputs through environment variables:

```bash
BENCH_BASELINE_BINARY=/tmp/verifoxx-baseline.test \
BENCH_CURRENT_BINARY=/tmp/verifoxx-current.test \
BENCH_COMPARE_REGEX='^BenchmarkEvaluate$' \
BENCH_COMPARE_ROUNDS=6 BENCH_COMPARE_TIME=200ms \
./cli/devx bench:compare
```

Record both commit IDs, Go version, host CPU, runtime tier, benchmark regex, and
benchtime with retained results. Run on a quiet host and inspect distributions
from `benchstat`; do not select one favorable sample. For hardware-counter work,
run one prebuilt binary directly so compilation remains outside the measurement:

```bash
timeout 180s perf stat -r 6 -- /tmp/verifoxx-current.test -test.run='^$' -test.bench='^BenchmarkEvaluate$' -test.benchtime=1s -test.count=1 -test.cpu=1 -test.timeout=120s
```

### Public Transport Load

`cmd/loadgen` sends the embedded canonical request and evidence payload through
the public HTTP or gRPC evaluation API. It divides one fixed request budget
across private worker loops, uses one bounded run context, cancels on the first
transport, status, size, or JSON validation failure, and closes every response
body and client connection. Its single JSON report contains the protocol,
target, requested and completed requests, concurrency, elapsed nanoseconds, and
requests per second. This is adapter and service load, not a private benchmark
endpoint.

Start the Compose environment, then exercise either transport:

```bash
timeout 300s docker compose up -d --build --wait
timeout 60s go run ./cmd/loadgen -protocol http -target 127.0.0.1:8080 -requests 1000 -concurrency 4 -timeout 30s
timeout 60s go run ./cmd/loadgen -protocol grpc -target 127.0.0.1:9090 -requests 1000 -concurrency 4 -timeout 30s
timeout 120s ./cli/devx load
timeout 60s docker compose down -v
```

The devx load command uses the HTTP values shown above. Increase request count,
concurrency, or timeout only within the command's enforced bounds; a partial
report and non-zero exit identify an incomplete run.

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

Measured on 2026-08-24 with Go 1.27.0 on Linux/amd64, the AMD Ryzen AI MAX+ 395,
and the `avx512` tier. The fixture uses the compiled Verifoxx policy, canonical
request/evidence rows, fixed worker goroutines, private shard results, and the
complete deterministic CSR merge. Values are the minimum of six runs after
priming every worker context and result slot. Every case reported `0 B/op` and
`0 allocs/op`.

| Rows | Workers | Direct ns/op | Scheduled serial ns/op | Parallel ns/op | Parallel gain |
|---:|---:|---:|---:|---:|---:|
| 64 | 2 | 7,320 | 8,101 | 8,028 | 0.91x |
| 64 | 4 | 7,344 | 8,147 | 8,105 | 0.91x |
| 256 | 2 | 28,239 | 30,095 | 23,067 | 1.22x |
| 256 | 4 | 28,056 | 30,075 | 21,151 | 1.33x |
| 1,024 | 2 | 111,611 | 114,648 | 73,397 | 1.52x |
| 1,024 | 4 | 110,983 | 115,496 | 53,916 | 2.06x |
| 4,096 | 2 | 446,092 | 458,454 | 263,178 | 1.69x |
| 4,096 | 4 | 446,363 | 459,048 | 196,855 | 2.27x |
| 16,384 | 2 | 1,873,008 | 1,924,542 | 1,098,167 | 1.71x |
| 16,384 | 4 | 1,917,032 | 1,930,397 | 771,831 | 2.48x |

Automatic scheduling starts at 256 rows. At that boundary the slowest
two-worker parallel sample was 24,748 ns/op, below the fastest corresponding
direct sample at 28,239 ns/op. The 64-row scheduled range was slower than direct
execution in all six samples. One-worker scheduling remains the direct caller
path; `ParallelRows` can override the default for tests and controlled tuning.
Every non-final shard starts and ends on a 64-row bitset boundary; only the
final shard may end in a partial word.

The service owns one process-lifetime scheduler. Request workspaces retain
decoder, batch, encoder, result, and audit storage, but all ordinary evaluation
shares the scheduler's fixed worker and token budget rather than starting a
nested worker set. The standalone `evaluate` command uses one command-lifetime
scheduler; debugger execution stays serial and deterministic.

Measurement command:

```bash
timeout 240s go test -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=300ms -count=6 -timeout=180s ./internal/scheduler
```

### Offline Product Benchmark

`verifoxx bench` compiles and decodes only the embedded fixture, repeats it into
an exact typed batch, verifies scheduled output against direct evaluation, and
primes every fixed worker context and admission state before measuring. It
accepts only bounded shape flags and does not expose a service endpoint or
accept policy, request, evidence, or stdin payloads. The JSON fields are `rows`,
`policy_nodes`, `evidence_records`,
`evidence_refs`, `iterations`, `execution_mode`, `simd_tier`, `workers`,
`elapsed_ns`, `rows_per_second`, `allocated_bytes`, and `allocations`.

One 2026-08-24 run on the host above reported:

```bash
timeout 120s go run ./cmd/verifoxx bench --rows 4096 --iterations 100 --workers 4
```

```json
{"rows":4096,"policy_nodes":14,"evidence_records":4,"evidence_refs":8192,"iterations":100,"execution_mode":"parallel","simd_tier":"avx512","workers":4,"elapsed_ns":41748013,"rows_per_second":9811245,"allocated_bytes":0,"allocations":0}
```

This is an illustrative bounded product run, not an A/B comparison. Use the Go
benchmark and interleaved workflow above for performance-change claims.

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

Measured on 2026-08-23 on the same host and `avx512` runtime tier; the scheduled
`evaluate` row was refreshed on 2026-08-24. These benchmarks intentionally
rebuild the policy Program and decode the canonical
five-row batch on every iteration; they are cold adapter measurements, not the
zero-allocation steady-state evaluator contract.

| Path | ns/op | Input MB/s | B/op | allocs/op |
|---|---:|---:|---:|---:|
| demo pipeline, excluding Cobra | 85,476 | 219.43 | 124,824 | 701 |
| complete `verifoxx demo` command | 106,525 | 176.07 | 166,393 | 849 |
| complete `verifoxx evaluate` command | 131,241 | 142.91 | 201,432 | 981 |

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
