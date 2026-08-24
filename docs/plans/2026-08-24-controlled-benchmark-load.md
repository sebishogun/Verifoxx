# Controlled Benchmark And Load Commands Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add deterministic evaluator fixtures, independently selectable execution benchmarks, bounded HTTP and gRPC load generation, and reproducible interleaved A/B comparison commands.

**Architecture:** `internal/benchdata` generates exact-size typed columns and CSR edges once, outside timed regions. Package-local evaluator benchmarks consume those neutral columns so they can force private scalar, SIMD, and index modes without adding a production tuning API; the existing scheduler benchmark remains the parallel measurement. `cmd/loadgen` drives only public transports with a fixed request budget and bounded context, while `scripts/bench-compare.sh` alternates two prebuilt test binaries and feeds their separate samples to `benchstat`.

**Tech Stack:** Go 1.27, `testing.B`, existing evaluator/scheduler/SIMD packages, `net/http`, generated gRPC clients, POSIX shell, `benchstat`, and Linux `perf stat`.

---

### Task 1: Deterministic Benchmark Data

**Files:**
- Create: `internal/benchdata/generate.go`
- Create: `internal/benchdata/generate_test.go`

**Step 1: Write failing shape and determinism tests**

Cover invalid zero or excessive rows, zero policy nodes, percentages above 100, duplicate target/other symbols, and multiplication overflow. For a valid configuration, assert exact pre-sized lengths, monotonic CSR offsets, in-range references, exact floor-rounded match and evidence counts, stable output across two calls, and different row placement for a different seed.

**Step 2: Run the focused test and verify RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/benchdata
```

Expected: FAIL because `Generate` and its configuration types do not exist.

**Step 3: Implement exact-size typed generation**

Define a bounded `Config` containing rows, generated predicate nodes, evidence percentage, match percentage, seed, and distinct target/other symbols. Return a `Dataset` with request IDs, symbol values, generated predicate values, evidence IDs/states, and CSR offsets/references. Compute every length before allocation, allocate each typed slice once, distribute selected rows deterministically with a seed-derived rotation, and never append in a per-row loop.

**Step 4: Run focused tests and portability**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/benchdata
timeout 90s env GOARCH=386 go test -count=1 -timeout 60s ./internal/benchdata
```

Expected: PASS.

### Task 2: Controlled Evaluator Benchmarks

**Files:**
- Create: `internal/eval/benchmark_test.go`
- Modify: `internal/scheduler/scheduler_bench_test.go`

**Step 1: Write a failing benchmark-fixture contract test**

Build each controlled shape through `eval.Builder`, extend the existing indexed benchmark program with generated equality nodes on a private reusable scratch slot, and execute it once in scalar, SIMD, and indexed modes. Assert identical results and the configured row, predicate-node, evidence-density, and match-density shape.

**Step 2: Run the contract test and verify RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/eval -run '^TestControlledBenchmarkFixture$'
```

Expected: FAIL because the controlled fixture does not exist.

**Step 3: Implement `BenchmarkEvaluate`**

Generate all data before `ResetTimer`, prime the executor and result storage, verify every forced mode against the scalar reference, then benchmark scalar, SIMD, and indexed execution separately. Put `tier`, `rows`, `nodes`, evidence percentage, match percentage, mode, and workers in stable sub-benchmark names; report rows, nodes, percentages, workers, and allocations as benchmark metrics. Extend `BenchmarkScheduler` names with the active SIMD tier and retain its independent direct, scheduled-serial, and scheduled-parallel cases.

**Step 4: Verify benchmark behavior and allocation output**

Run:

```bash
timeout 300s go test -count=1 -timeout 60s ./internal/eval ./internal/scheduler
timeout 300s go test -timeout 240s -run='^$' -bench='^BenchmarkEvaluate$' -benchmem -benchtime=50ms ./internal/eval
```

Expected: tests pass; each benchmark name carries the complete shape and reports allocation metrics.

### Task 3: Bounded HTTP And gRPC Load Generator

**Files:**
- Create: `cmd/loadgen/main.go`
- Create: `cmd/loadgen/main_test.go`

**Step 1: Write failing option and HTTP tests**

Test strict `http`/`grpc` protocols, host:port targets, request and concurrency bounds, positive bounded timeouts, exact static work partitioning, cancellation, non-2xx HTTP rejection, malformed response rejection, and successful machine-readable summary output against `httptest.Server`.

**Step 2: Write a failing gRPC transport test**

Start a generated `PolicyService` test server on a loopback listener, return valid result JSON from `EvaluateBatch`, and assert the requested call count and clean connection shutdown.

**Step 3: Run tests and verify RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/loadgen
```

Expected: FAIL because the command does not exist.

**Step 4: Implement the minimal load client**

Parse `-protocol`, `-target`, `-requests`, `-concurrency`, and `-timeout` with the standard flag package. Build the canonical request/evidence payload once, divide the fixed request total across private worker loops, cancel on the first transport or validation failure, read and validate every response, close all response bodies and gRPC connections, and emit one JSON report containing protocol, completed requests, concurrency, elapsed nanoseconds, and requests per second. Do not add a production benchmark endpoint.

**Step 5: Run tests, race, and 386 compile**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/loadgen
timeout 120s go test -count=1 -race -timeout 90s ./cmd/loadgen
timeout 90s env GOARCH=386 go test -count=1 -timeout 60s ./cmd/loadgen
```

Expected: PASS.

### Task 4: Interleaved A/B Workflow And Documentation

**Files:**
- Create: `scripts/bench-compare.sh`
- Modify: `cmd/devx/cmd/performance.go`
- Modify: `cmd/devx/cmd/status.go`
- Modify: `cmd/devx/cmd/root_test.go`
- Modify: `docs/performance.md`

**Step 1: Write failing devx plan and availability tests**

Assert that `load` invokes `go run ./cmd/loadgen` with bounded local defaults, requires only Go plus the loadgen source asset, and no longer has a static blocker or `ghz` dependency.

**Step 2: Run the focused devx tests and verify RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd -run='Test.*(Performance|Workflow|Status|Requirements)'
```

Expected: FAIL on the old `ghz` workflow and static blocker.

**Step 3: Implement the interleaved comparison script**

Accept baseline test binary, current test binary, benchmark regex, optional rounds, and optional benchtime. Validate executables and numeric bounds, alternate A/B then B/A order by round, bound every binary invocation with `timeout`, keep separate temporary output files under `mktemp`, clean them with a trap, and invoke `benchstat` once. The script must not build, modify, or switch a checkout.

**Step 4: Wire devx load and document operations**

Replace the placeholder `ghz` load workflow with the bounded Go load generator. Document controlled evaluator metrics, the scheduler's separate parallel benchmark, HTTP and gRPC examples against Compose, construction of baseline/current benchmark binaries in separate worktrees, interleaved script usage, `benchstat`, and `perf stat -r`. Do not commit benchmark numbers from a non-quiet run.

**Step 5: Verify shell and workflows**

Run:

```bash
timeout 30s sh -n scripts/bench-compare.sh
timeout 90s go test -count=1 -timeout 60s ./cmd/devx/cmd
```

Expected: PASS.

### Task 5: Task 45 Gates

**Files:**
- Review all Task 45 production, test, script, and documentation files.

**Step 1: Run focused benchmark and load tests**

```bash
timeout 300s go test -timeout 240s -run='^$' -bench='^BenchmarkEvaluate$' -benchmem -benchtime=50ms ./internal/eval
timeout 120s go test -count=1 -race -timeout 90s ./internal/benchdata ./cmd/loadgen ./cmd/devx/cmd
```

**Step 2: Run repository gates**

```bash
timeout 180s go test -count=1 -timeout 60s ./...
timeout 180s go vet ./...
timeout 30s git diff --check
```

**Step 3: Review the worktree**

Confirm only Task 45 files are staged and leave the unrelated `AGENTS.md` modification untouched.

**Step 4: Commit and push**

```bash
git commit -m "perf: add evaluation benchmark harness"
git push
```
