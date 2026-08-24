# Production Audit Gap Closure Design

**Status:** Approved

**Date:** 2026-08-24

## Goal

Close the remaining production audit gaps without changing evaluator semantics:
route ordinary service and CLI batch evaluation through the measured fixed-worker
scheduler, expose one bounded offline product benchmark, and make the pinned
field-alignment analyzer a reproducible local and CI gate.

## Runtime Scheduler

`server.Engine` keeps its fixed request-workspace channel because decoding,
result encoding, metrics, and audit materialization need exclusive mutable
storage for the complete request lifetime. Those workspaces no longer own an
`eval.Executor`. One process-wide `scheduler.Scheduler` owns all evaluator
goroutines, shard executors, private shard results, and the global work-token
budget.

The scheduler is constructed once in `NewEngine` from validated runtime values:

- workers are `Config.Workers`;
- scheduler admission storage is `min(Config.QueueDepth, Config.Workers)`, since
  no more than `Config.Workers` request workspaces can submit concurrently;
- each scheduler context is sized for `Limits.MaxBatchRows`; and
- a zero `ParallelRows` selects the measured 256-row crossover.

Small batches and one-worker configurations execute serially in the submitting
goroutine while holding one global token. Large batches claim only currently
available tokens and shard at existing 64-row bitmap boundaries. Concurrent
requests therefore share one fixed evaluator budget instead of nesting a worker
pool under every request.

`Engine.Close` closes the scheduler after service admission and active requests
have drained. `server.Serve` installs it as the lifecycle's `JoinWorkers` hook
and also closes it on startup failures. Cancellation from HTTP, gRPC, or process
shutdown reaches `Scheduler.Execute`; context cancellation is returned as such
rather than being wrapped as service unavailability.

The standalone `evaluate` command creates one scheduler after batch decoding,
uses a queue depth of one, and closes it before command exit. Its worker count is
bounded by `GOMAXPROCS`, 256, and the number of 64-row words; batches below the
parallel crossover use one worker. `demo`, `simulate`, `explain`, graph
construction, and semantic debugger execution remain explicitly serial.

The scheduler publishes lock-free aggregate counts for attempted serial and
parallel executions. Tests and the benchmark command use those counts to prove
which production path ran; no per-row instrumentation is added.

## Offline Benchmark Command

`verifoxx bench` is a local command, not a service endpoint. It accepts only
bounded shape controls:

```text
--rows 1..65536
--iterations 1..100000
--workers 1..256
```

The command compiles the embedded policy, decodes the embedded request/evidence
pack once, and repeats its typed rows into one deterministic SoA/CSR batch.
Setup, scheduler construction, destination growth, and worker priming occur
outside the timed region. No policy, request, evidence, file, stdin, or network
input is accepted.

The timed loop executes the same immutable Program and batch through one
scheduler. A direct serial result is computed before timing and compared with
the scheduled result. Output is one JSON object containing rows, policy nodes,
evidence records and references, iterations, actual serial/parallel mode, SIMD
tier, workers, elapsed nanoseconds, rows per second, allocated bytes, and
allocation count. Runtime allocation counters cover only the timed loop; the
steady-state target is zero.

## Field Alignment Gate

`scripts/check-fieldalignment.sh` is the sole analyzer entry point. It pins
`golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment` at
`v0.47.1-0.20260707181000-a299dadba899`, runs with an explicit timeout and
`-test=false`, and covers production packages under `internal`, `cmd`, and
`policies`.

CI invokes that exact script. Repository contract tests assert the pin, package
scope, CI wiring, explicit timeout, and nonzero-exit propagation. Analyzer
suggestions remain review inputs; the script never applies automatic fixes.

## Errors And Bounds

Invalid worker, queue, row, iteration, or capacity values fail before goroutines
or benchmark storage are created. Scheduler construction and execution errors
retain the existing CLI pipeline or service error boundaries. Benchmark output
is emitted only after successful execution and equivalence checks, so partial or
misleading measurements are not reported.

All repeated evaluator work remains map-free, reflection-free, and
allocation-free after priming. Batch repetition and JSON report construction are
cold command setup/output work. Scheduler state, private results, and request
workspaces remain fixed-size lifetime groups.

## Verification

Tests cover production service and CLI scheduler dispatch, serial/parallel
equivalence, cancellation, queue saturation, scheduler shutdown, benchmark
shape validation and output, analyzer pinning and failure propagation, and
unchanged deterministic debugger behavior. Verification includes native,
pure-Go, 386, race/checkptr, integration, vet, field alignment, scheduler and
product benchmark measurements, and the full release gates.

## Rejected Alternatives

- A scheduler per service request would create nested goroutines and scratch,
  discard reusable state, and oversubscribe the process.
- Replacing request workspaces with scheduler contexts would couple decoding,
  encoding, audit output, and response lifetimes to evaluator shards and require
  a larger ownership rewrite.
- A benchmark HTTP endpoint would expose avoidable production attack surface
  and mix transport/admission cost with evaluator measurement.
- Duplicating the analyzer command in CI and documentation would allow the
  pinned version and package scope to drift.
