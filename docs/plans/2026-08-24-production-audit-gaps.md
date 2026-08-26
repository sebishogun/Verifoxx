# Production Audit Gap Closure Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Route ordinary production evaluation through the fixed-worker scheduler, add a bounded offline benchmark command, and enforce one pinned field-alignment gate locally and in CI.

**Architecture:** `server.Engine` retains bounded request workspaces for decode/encode/audit ownership but delegates evaluation to one process-wide scheduler. The standalone `evaluate` command uses one command-lifetime scheduler while deterministic debugger and single-row tools stay serial. A no-input `nornrune bench` command measures a repeated embedded typed fixture, and one pinned shell script is the only field-alignment entry point.

**Tech Stack:** Go 1.27, Cobra, existing SoA/CSR evaluator and fixed-worker scheduler, `runtime.MemStats`, POSIX shell, GitHub Actions, pinned `fieldalignment` analyzer.

---

### Task 1: Expose Scheduler Execution Evidence

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

**Step 1: Write the failing stats test**

Add a `scheduler statistics` subtest to `TestScheduler` that executes one batch
below and one batch at the crossover, then expects one serial and one parallel
execution:

```go
before := scheduler.Stats()
if err := scheduler.Execute(context.Background(), &dst, p, small); err != nil {
    t.Fatal(err)
}
if err := scheduler.Execute(context.Background(), &dst, p, parallel); err != nil {
    t.Fatal(err)
}
after := scheduler.Stats()
if after.Executions-before.Executions != 2 ||
    after.Serial-before.Serial != 1 || after.Parallel-before.Parallel != 1 {
    t.Fatalf("scheduler stats delta = %+v -> %+v", before, after)
}
```

Also assert a nil scheduler returns a zero snapshot.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestScheduler/scheduler_statistics' ./internal/scheduler
```

Expected: fail because `Stats` does not exist.

**Step 3: Implement lock-free aggregate statistics**

Add deliberately aligned atomics to `Scheduler` and a public value snapshot:

```go
type Stats struct {
    Executions uint64
    Serial     uint64
    Parallel   uint64
}

func (scheduler *Scheduler) Stats() Stats {
    if scheduler == nil {
        return Stats{}
    }
    return Stats{
        Executions: scheduler.executions.Load(),
        Serial: scheduler.serial.Load(),
        Parallel: scheduler.parallel.Load(),
    }
}
```

Increment `Executions` and exactly one mode counter after shard ranges are fixed.
Do not add per-row counters or locks.

**Step 4: Run GREEN and allocation coverage**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/scheduler
timeout 180s go test -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=200ms -count=1 -timeout=120s ./internal/scheduler
```

Expected: pass; scheduled paths remain `0 B/op`, `0 allocs/op`.

### Task 2: Route The Product CLI Evaluate Command Through The Scheduler

**Files:**
- Modify: `internal/adapters/cli/evaluate.go`
- Test: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing dispatch and cancellation tests**

Add tests that compile/decode the embedded pack and call the intended scheduled
method. Assert scheduler stats increase, results equal the existing serial
result, and a pre-canceled context returns `context.Canceled` without execution.
Keep `engine.evaluate` tests unchanged to prove the debugger-compatible serial
path remains available.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestPipeline(ScheduledEvaluation|ScheduledCancellation)' ./internal/adapters/cli
```

Expected: fail because scheduled CLI evaluation does not exist.

**Step 3: Add one command-lifetime scheduler**

Add a scheduler pointer to the CLI pipeline and implement:

```go
func (e *engine) evaluateScheduled(ctx context.Context, p *program.Program, batch eval.Batch) (*result.Batch, error)
func (e *engine) closeScheduler() error
```

Worker count is `1` below 256 rows; otherwise it is the minimum of
`GOMAXPROCS`, 256, and the number of 64-row words. Construct with queue depth
one, `Capacity{Rows: batch.Rows}`, and default `ParallelRows` (`0`). Preserve
`context.Canceled` and `context.DeadlineExceeded`; wrap other failures as the
existing `evaluate` pipeline stage.

Change only `newEvaluateCommand` to call `evaluateScheduled(cmd.Context(), ...)`
and defer scheduler closure. `demo`, `simulate`, `explain`, TUI, and graph paths
continue to call the serial `evaluate` method.

**Step 4: Run GREEN and command regression tests**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestPipeline|TestEvaluate' ./internal/adapters/cli
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: pass with byte-identical embedded evaluation output.

### Task 3: Route The Service Engine Through One Runtime Scheduler

**Files:**
- Modify: `internal/server/engine.go`
- Modify: `internal/server/serve.go`
- Modify: `internal/server/engine_test.go`
- Modify: `internal/server/security_test.go`

**Step 1: Write failing service dispatch, cancellation, and close tests**

Extend the golden engine test to configure `QueueDepth`, register cleanup, and
assert the engine scheduler records one serial execution. Add a large repeated
request fixture that reaches the parallel counter and returns the same encoded
results as direct evaluation. Add tests that a pre-canceled request is returned
as `context.Canceled`, `Engine.Close` is idempotent, and evaluation after close
fails with service unavailability.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestEngine.*(Scheduler|Cancellation|Close)|TestEngineEvaluatesGolden' ./internal/server
```

Expected: fail because `Engine` has no scheduler lifecycle.

**Step 3: Construct and use the global scheduler**

Add `QueueDepth int` to `EngineConfig`. In `NewEngine`, validate it and create:

```go
scheduler.NewScheduler(scheduler.Config{
    Capacity:   scheduler.Capacity{Rows: config.Limits.MaxBatchRows},
    Workers:    config.Workers,
    QueueDepth: min(config.QueueDepth, config.Workers),
})
```

Remove `eval.Executor` from `engineWorker`, retain its merged `result.Batch`, and
replace direct execution with the shared scheduler. Return `ctx.Err()` directly
when cancellation caused scheduler failure; retain `service.ErrUnavailable` for
other evaluator failures.

Implement `Engine.Close(context.Context) error` by closing the scheduler once
and respecting caller cancellation while waiting. Include the scheduler in
`Engine.valid`.

**Step 4: Wire startup and graceful shutdown**

Pass `cfg.QueueDepth` from `Serve`, close the engine on every later startup
failure through one defer, and set:

```go
JoinWorkers: engine.Close,
```

The lifecycle already drains active admissions before invoking `JoinWorkers`,
so scheduler shutdown does not race active requests.

**Step 5: Run GREEN, race, and lifecycle tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/server ./internal/service
timeout 180s go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 120s ./internal/server ./internal/service ./internal/scheduler
```

Expected: pass with no race/checkptr report.

### Task 4: Add The Bounded Offline Product Benchmark

**Files:**
- Create: `internal/adapters/cli/bench.go`
- Create: `internal/adapters/cli/bench_test.go`
- Modify: `internal/adapters/cli/root.go`

**Step 1: Write failing command contract tests**

Test that:

- root help and `bench --help` expose `bench`, `--rows`, `--iterations`, and
  `--workers`, but no policy/request/evidence flags;
- zero and over-limit values return usage exit code `2` before fixture setup;
- a small successful run emits one valid JSON object with exact shape, SIMD,
  mode, timing, throughput, byte, and allocation fields;
- scheduled output matches a direct reference; and
- pre-canceled execution returns an operational cancellation error.

Use real embedded fixtures and small iteration counts; do not mock evaluator
results.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestBench' ./internal/adapters/cli
```

Expected: fail because the command does not exist.

**Step 3: Build one deterministic repeated typed batch**

In `bench.go`, compile/decode the embedded sources once and repeat source rows
through `eval.Builder`. Copy every present typed field by Program field kind,
copy evidence records once, and build exact CSR offsets/references. Reject
overflow before allocation. This setup is outside timing.

**Step 4: Implement benchmark execution and append-only JSON reporting**

Define bounded options (`rows <= 65536`, `iterations <= 100000`,
`workers <= 256`). Construct and prime one scheduler, compute a direct reference,
snapshot scheduler and `runtime.MemStats`, execute the requested iterations,
then report the stats delta. Build JSON with `append` and `strconv`; do not use a
map or reflection.

Required output keys:

```text
rows policy_nodes evidence_records evidence_refs iterations execution_mode
simd_tier workers elapsed_ns rows_per_second allocated_bytes allocations
```

Register `newBenchCommand(deps)` in `newRoot`. The command accepts no source
flags or stdin payload.

**Step 5: Run GREEN and product smoke tests**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestBench' ./internal/adapters/cli
timeout 120s go run ./cmd/nornrune bench --rows 1024 --iterations 10 --workers 4
```

Expected: tests pass; smoke output is one JSON object reporting parallel mode
and bounded metrics.

### Task 5: Add The Shared Pinned Field-Alignment Gate

**Files:**
- Create: `scripts/check-fieldalignment.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `internal/doccheck/ci_test.go`

**Step 1: Write failing repository contract tests**

Require the script, exact analyzer version, `-test=false`, production package
patterns, an internal timeout, and a CI step invoking the script under an outer
timeout. Run the script with a temporary failing `timeout` executable first in
`PATH` and assert its nonzero exit code is propagated.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test(FieldAlignment|CI)' ./internal/doccheck
```

Expected: fail because the script and CI step do not exist.

**Step 3: Implement the single gate**

Create an executable POSIX script using `set -eu`, resolve the repository root,
and execute:

```sh
timeout 240s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 \
    -test=false ./internal/... ./cmd/... ./policies/...
```

Add `timeout 300s ./scripts/check-fieldalignment.sh` to the native CI job after
`go vet`.

**Step 4: Run GREEN and the real gate**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/doccheck
timeout 300s ./scripts/check-fieldalignment.sh
```

Expected: pass with no analyzer diagnostics.

### Task 6: Measure And Document The Production Contract

**Files:**
- Modify: `README.md`
- Modify: `docs/performance.md`
- Modify: `docs/concurrency.md`
- Modify: `docs/architecture.md`
- Modify: `docs/operations.md`

**Step 1: Run scheduler and product benchmark measurements**

```bash
timeout 240s go test -run='^$' -bench='^BenchmarkScheduler$' -benchmem -benchtime=300ms -count=6 -timeout=180s ./internal/scheduler
timeout 120s go run ./cmd/nornrune bench --rows 4096 --iterations 100 --workers 4
```

Record actual output only. Confirm scheduler benchmark cases remain zero
allocation and retain the 256-row crossover before documenting it.

**Step 2: Update user and architecture documentation**

Document `nornrune bench`, its no-input contract and JSON fields, the shared
field-alignment command, process-wide scheduler ownership, global token budget,
shutdown order, crossover, and measured output. Remove statements that the
production server does not use the scheduler.

**Step 3: Run documentation and formatting checks**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/doccheck
timeout 30s gofmt -w internal/scheduler internal/server internal/adapters/cli internal/doccheck
git diff --check
```

Expected: all checks pass with no whitespace errors.

### Task 7: Complete Review And Release Gates

**Files:**
- Verify all Task 59 files

**Step 1: Run focused static and allocation gates**

```bash
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 180s go test -run='^$' -bench='^(BenchmarkScheduler|BenchmarkEvaluateCommand)$' -benchmem -benchtime=200ms -count=1 -timeout=120s ./internal/scheduler ./internal/adapters/cli
```

**Step 2: Run the release matrix**

```bash
timeout 240s go test -count=1 -timeout 180s ./...
timeout 300s go test -count=1 -timeout 240s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -timeout 240s -tags=purego ./...
timeout 360s go test -count=1 -timeout 300s -tags=integration ./...
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 120s go mod tidy -diff
git diff --check
```

Expected: every command exits zero.

**Step 3: Request code review**

Review the complete working-tree diff against
`docs/plans/2026-08-24-production-audit-gaps-design.md`, this plan, and Task 59
in the roadmap. Fix Critical, High, and valid Medium findings, then rerun affected
gates.

**Step 4: Commit and push**

Stage only Task 59 files. Keep
`docs/plans/2026-08-24-policy-compatibility-frontends-design.md` out of the
commit.

```bash
git commit -m "feat: close production audit gaps"
git push origin main
```
