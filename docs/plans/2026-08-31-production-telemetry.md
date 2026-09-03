# Production Telemetry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend the existing observability path with fixed-cardinality atomic snapshots, Prometheus and OpenTelemetry export, bounded sampled tracing, and production alerting without adding evaluator-kernel work or disabled-path allocations.

**Architecture:** Service boundaries aggregate each completed batch once into cache-line-separated atomics. One immutable bounded `Snapshot` feeds the existing Prometheus endpoint and optional OpenTelemetry instruments; transport spans carry only fixed enums and propagate standard context. OTLP work runs behind bounded non-blocking SDK queues and shutdown uses the caller's deadline.

**Tech Stack:** Go 1.27, `sync/atomic`, Prometheus client, OpenTelemetry Go API/SDK/OTLP HTTP exporters, HTTP/gRPC propagators, YAML alert rules, Go benchmarks.

---

Implementation stays in the current Task 55/56 worktree. Do not commit unless the user explicitly requests it. Every test, benchmark, build, vet, or fuzz command must have an outer `timeout`; every `go test` command must also pass `-timeout`.

### Task 1: Freeze The Bounded Telemetry Schema

**Files:**
- Create: `internal/telemetry/counters.go`
- Test: `internal/telemetry/counters_test.go`

**Step 1: Write failing schema and snapshot tests**

Define tests requiring fixed arrays and stable names for:

```go
type Decision uint8 // approve, reject, revise, escalate
type EscalationReason uint8 // missing, incomplete, stale, unclear, conflicting, unverifiable
type AuditOutcome uint8 // persisted, optional_drop, required_failure
type ReloadOutcome uint8 // success, invalid, persistence_failure

type Snapshot struct {
    Decisions       [DecisionCount]uint64
    Escalations     [EscalationReasonCount]uint64
    Audits          [AuditOutcomeCount]uint64
    Reloads         [ReloadOutcomeCount]uint64
    LatencyBuckets  [LatencyBucketCount]uint64
    QueueBuckets    [QueueBucketCount]uint64
    Batches, Rows, ShutdownFailures, ExportDrops uint64
    ActiveAdmissions int64
}
```

Test exact enum names, bucket boundaries, monotonic updates, invalid-enum rejection, saturation/overflow behavior, and an exact snapshot after several updates. Reflection tests must reject maps, strings, and slices in `Counters` and `Snapshot`.

**Step 2: Verify RED**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/telemetry`

Expected: FAIL because the package and schema do not exist.

**Step 3: Implement atomic counters**

Use fixed `[N]atomic.Uint64` arrays. Separate frequently written groups with explicit padding after checking type sizes; no map, lock, interface, formatting, or allocation in update methods. `Snapshot()` performs one bounded load pass and returns by value.

**Step 4: Verify GREEN and field order**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/telemetry`

Run: `timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/telemetry`

Expected: PASS and no analyzer diagnostics.

### Task 2: Aggregate Results Once Per Batch

**Files:**
- Create: `internal/telemetry/batch.go`
- Test: `internal/telemetry/batch_test.go`
- Benchmark: `internal/telemetry/benchmark_test.go`

**Step 1: Write failing aggregation tests**

Require one linear pass over `result.Batch.OutcomeIDs`, with escalation reasons derived only from the bounded `truth.ReasonMissing..ReasonConflict` IDs already present in result CSR columns. Test all four decisions, every escalation reason class, malformed IDs, zero rows, and a 65,536-row batch. A failed aggregate must not partially mutate counters.

**Step 2: Verify RED**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/telemetry -run TestObserveBatch`

Expected: FAIL because `ObserveBatch` is undefined.

**Step 3: Implement validation then one bulk update**

Build a stack-local fixed `BatchDelta`, validate totals and checked additions, then apply one atomic add per nonzero group. Keep Program symbol lookup outside counters; the server maps the four outcome IDs once from the immutable Program before the row scan.

**Step 4: Verify behavior and allocation budget**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/telemetry`

Run: `timeout 180s go test -run '^$' -bench BenchmarkObserveBatch -benchmem -count=1 -timeout 150s ./internal/telemetry`

Expected: PASS; aggregation reports `0 B/op`, `0 allocs/op`.

### Task 3: Add The Public Runtime And Optional OTLP Pipeline

**Files:**
- Create: `telemetry/config.go`
- Create: `telemetry/telemetry.go`
- Create: `telemetry/metrics.go`
- Test: `telemetry/telemetry_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write failing runtime tests**

Require disabled, counters-only, and OTLP modes. Validate endpoint length/scheme, sample ratio, queue capacity, export interval, service/build metadata, and deadlines. Use SDK in-memory/manual exporters to test exact snapshots, exporter parity, unavailable exporters, non-blocking queue overflow with `ExportDrops`, and bounded `Shutdown(ctx)`.

The public API accepts only fixed metadata and operational configuration:

```go
type Config struct {
    Endpoint string
    ServiceVersion string
    BuildVersion string
    ExportInterval time.Duration
    TraceSampleRatio float64
    ExportQueueSize uint32
    Enabled bool
}

type Runtime struct { /* immutable providers plus internal counters */ }
func New(context.Context, Config, ...Option) (*Runtime, error)
func (r *Runtime) Snapshot() Snapshot
func (r *Runtime) Shutdown(context.Context) error
```

Options exist only for deterministic exporter/clock tests, not arbitrary labels.

**Step 2: Verify RED**

Run: `timeout 120s go test -count=1 -timeout 90s ./telemetry`

Expected: FAIL because the package does not exist.

**Step 3: Implement the runtime**

Use OTel SDK `ManualReader`/periodic reader for metrics and `BatchSpanProcessor` without blocking mode for traces. Register observable instruments that read the same immutable internal snapshot used by Prometheus. Never install process-global providers; expose providers/propagator from `Runtime` for explicit adapter injection. Disabled mode uses nil/no-op providers and starts no goroutines.

**Step 4: Verify GREEN and module hygiene**

Run: `timeout 120s go test -count=1 -timeout 90s ./telemetry`

Run: `timeout 180s go mod tidy -diff`

Expected: PASS and no tidy diff.

### Task 4: Replace Mutable Prometheus Collectors With Snapshot Collection

**Files:**
- Modify: `internal/observability/metrics.go`
- Modify: `internal/observability/metrics_test.go`
- Test: `internal/observability/metrics_test.go`

**Step 1: Write failing parity/cardinality tests**

Require the existing metric names to remain compatible while adding fixed escalation reason, queue-wait, active admission, policy reload, audit outcome, export drop, and shutdown failure series. Gather descriptor/label pairs and assert the complete finite set; reject request IDs, evidence, policy names/hashes, users, URLs, source, credentials, and error strings at the API boundary by making those values unrepresentable.

**Step 2: Verify RED**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/observability -run 'TestMetrics|TestMetricCardinality'`

Expected: FAIL because the new snapshot-backed series are absent.

**Step 3: Implement one custom collector**

`observability.Metrics` owns or receives `*internal/telemetry.Counters`. Its Prometheus collector snapshots once per scrape and emits only predeclared descriptors and fixed enum labels. `ObserveBatch` delegates to the atomic aggregate instead of updating Prometheus collectors directly. Queue depth and journal failure compatibility sources are folded into snapshot gauges/counters at scrape time without dynamic labels.

**Step 4: Verify GREEN and scrape races**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/observability`

Run: `timeout 180s go test -count=1 -timeout 150s -race ./internal/observability ./internal/telemetry`

Expected: PASS.

### Task 5: Add Fixed-Attribute Tracing And Context Propagation

**Files:**
- Create: `telemetry/tracing.go`
- Modify: `internal/adapters/httpapi/server.go`
- Modify: `internal/adapters/httpapi/evaluate.go`
- Modify: `internal/adapters/httpapi/policy.go`
- Modify: `internal/adapters/httpapi/server_test.go`
- Modify: `internal/adapters/httpapi/security_test.go`
- Modify: `internal/adapters/grpcapi/server.go`
- Modify: `internal/adapters/grpcapi/server_test.go`
- Modify: `internal/adapters/grpcapi/security_test.go`

**Step 1: Write failing propagation and redaction tests**

Use an in-memory span exporter. HTTP and gRPC incoming trace context must parent admission/decode/policy-lookup/evaluation/audit/response-encode spans. Assert span names and attributes exactly; scan exported spans to prove request JSON, evidence values, policy source/name/hash, URL/query, credentials, user values, and raw error strings are absent. Unsampled mode must preserve propagation without recording.

**Step 2: Verify RED**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/adapters/httpapi ./internal/adapters/grpcapi -run 'Trace|Redact'`

Expected: FAIL because adapters do not accept a telemetry runtime or propagate trace context.

**Step 3: Implement explicit adapter injection**

Add a validated tracer/propagator dependency to adapter configs. Extract/inject W3C Trace Context for HTTP; configure OTel gRPC stats/interceptor propagation without global state. Span attributes are fixed operation, transport, status, decision/reason enum, and duration only. Do not create row/node spans.

**Step 4: Verify GREEN**

Run: `timeout 150s go test -count=1 -timeout 120s ./internal/adapters/httpapi ./internal/adapters/grpcapi`

Run: `timeout 240s go test -count=1 -timeout 210s -race ./internal/adapters/httpapi ./internal/adapters/grpcapi ./telemetry`

Expected: PASS.

### Task 6: Wire Service Events And Bounded Shutdown

**Files:**
- Modify: `internal/server/engine.go`
- Modify: `internal/server/engine_test.go`
- Modify: `internal/server/serve.go`
- Modify: `internal/server/security_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/env.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write failing lifecycle tests**

Cover queue wait/active admission, successful and failed policy reloads, persisted/optional/required audit outcomes, exporter drops, and shutdown flush success/timeout. Simulate a permanently unavailable exporter and prove evaluation and required audit submission still complete before their own deadlines. Config tests cover strict JSON/env/flag precedence and ensure endpoints/credentials are redacted from formatting.

**Step 2: Verify RED**

Run: `timeout 150s go test -count=1 -timeout 120s ./internal/server ./internal/config -run 'Telemetry|Exporter|Shutdown|Redact'`

Expected: FAIL because runtime telemetry is not wired.

**Step 3: Integrate outside evaluator kernels**

Construct one telemetry runtime in `Serve`, pass its counters and tracer explicitly, and place its flush in lifecycle shutdown with the caller deadline. Record batch deltas after evaluation, audit outcome after journal submission, and reload outcome after publication. Keep health/readiness independent and emit active policy version, SIMD tier, build version, migration state, and dependency health only through bounded resource/health metadata.

**Step 4: Verify GREEN**

Run: `timeout 180s go test -count=1 -timeout 150s ./internal/server ./internal/config ./internal/service`

Run: `timeout 300s go test -count=1 -tags=integration -timeout 270s ./internal/server ./internal/adapters/postgres`

Expected: PASS.

### Task 7: Add Alerts, Operations Documentation, And Contracts

**Files:**
- Create: `deploy/telemetry/prometheus-rules.yaml`
- Create: `docs/telemetry.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/operations.md`
- Modify: `docs/performance.md`
- Create: `internal/doccheck/telemetry_test.go`

**Step 1: Write failing documentation contracts**

Require stable names/units/temporality, the complete cardinality table, privacy exclusions, disabled/counters/Prometheus/OTLP modes, W3C propagation, queue/drop behavior, shutdown deadlines, readiness separation, benchmark interpretation, and commands. Parse alert YAML and require bounded multi-window rules for decision-rate changes, escalation spikes, audit failures, queue saturation, reload failures, and shutdown timeouts.

**Step 2: Verify RED**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/doccheck -run Telemetry`

Expected: FAIL because contracts and rules are absent.

**Step 3: Write docs and rules**

Document expected false positives and operator actions. Alert labels may contain only fixed severity/window fields; annotations must not interpolate protected labels.

**Step 4: Verify GREEN**

Run: `timeout 120s go test -count=1 -timeout 90s ./internal/doccheck`

Expected: PASS.

### Task 8: Measure Modes And Set The Budget

**Files:**
- Modify: `internal/telemetry/benchmark_test.go`
- Modify: `telemetry/telemetry_test.go`
- Modify: `docs/performance.md`

**Step 1: Add interleaved benchmarks**

Benchmark disabled, counters-only, Prometheus scrape, sampled OTel, and forced tracing. Separate update, snapshot, scrape, queue/export, and shutdown costs. Report hardware, Go version, sample ratio, contention, throughput, ns/op, B/op, allocs/op, and tail distribution where the harness supports it.

**Step 2: Run bounded measurements**

Run: `timeout 300s go test -run '^$' -bench 'Benchmark(ObserveBatch|Telemetry|Prometheus)' -benchmem -count=6 -timeout 270s ./internal/telemetry ./telemetry ./internal/observability`

Expected: disabled and counters-only batch updates are `0 B/op`, `0 allocs/op`; measured values establish, rather than assume, the enabled-mode budget.

**Step 3: Record results**

Add the measured machine/toolchain and separate each boundary cost in `docs/performance.md`. Do not claim cross-machine throughput.

### Task 9: Final Verification And Roadmap Status

**Files:**
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine.md`

**Step 1: Run the complete bounded matrix**

Run:

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 420s go test -count=1 -timeout 360s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s go test -count=1 -tags=integration -timeout 360s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 180s go mod tidy -diff
timeout 30s git diff --check
```

Expected: all commands PASS.

**Step 2: Request final code review**

Review privacy, cardinality, non-blocking behavior, race safety, disabled/counters allocation evidence, shutdown deadlines, alert validity, and compliance with the approved design. Fix every Critical or Important finding and rerun affected gates.

**Step 3: Mark Task 57 complete**

Only after the matrix and review pass, add `**Status:** Complete (2026-08-31)` immediately below the Task 57 heading.
