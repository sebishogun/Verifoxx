# Production Telemetry

NornRune telemetry is optional, bounded, and never part of evaluator kernels.
Every counter is updated once per completed batch or bounded service event at a
service boundary; scalar and SIMD evaluation paths contain no callbacks, spans,
locks, maps, formatting, or exporter calls.

## Metric names, units, and temporality

All metrics are cumulative counters or fixed-bucket cumulative histograms with
the `nornrune_` namespace:

| Metric | Kind | Meaning |
| --- | --- | --- |
| `nornrune_evaluation_batches_total` | counter | Batches observed, including failures |
| `nornrune_evaluation_batch_failures_total` | counter | Batches reported as failed |
| `nornrune_evaluation_rows_total` | counter | Request rows presented to batches |
| `nornrune_evaluation_outcomes_total{outcome}` | counter | Decisions by fixed outcome label |
| `nornrune_evaluation_escalations_total{reason}` | counter | Escalation reasons by fixed reason label |
| `nornrune_evaluation_duration_seconds` | histogram | End-to-end batch duration buckets |
| `nornrune_service_queue_wait_seconds` | histogram | Admission queue wait buckets |
| `nornrune_service_queue_depth` | gauge | Requests waiting for admission |
| `nornrune_service_active_admissions` | gauge | Currently admitted requests across all operations |
| `nornrune_audit_outcomes_total{outcome}` | counter | Audit persistence outcomes |
| `nornrune_audit_journal_failures_total` | counter | Journal batches that failed to persist |
| `nornrune_policy_reloads_total{outcome}` | counter | Policy reload outcomes |
| `nornrune_telemetry_export_drops_total` | counter | Optional exports dropped or failed |
| `nornrune_shutdown_failures_total` | counter | Telemetry shutdown flush failures |
| `nornrune_evaluation_workers` | gauge | Configured worker count |
| `nornrune_simd_tier_info{tier}` | gauge | Selected SIMD tier (one value from the runtime SIMD tier set) |

An evaluation batch becomes observable after request and evidence decoding has
produced a valid row count. From that boundary onward, success or failure is
recorded exactly once. A failed scheduler, encoder, output-limit, or required
audit path contributes one batch, one failure, its decoded row count, and no
completed decisions. A decode rejection has no trusted row count and is not an
evaluation-batch metric.

End-to-end duration retains Go's monotonic clock component through evaluation
and required audit acknowledgment. UTC audit timestamps are derived from that
nonnegative elapsed duration, so a wall-clock correction cannot discard
telemetry or make an otherwise valid required-audit batch fail.

Required-mode audit persistence is recorded after its synchronous commit
acknowledgment. Best-effort queue admission records no terminal outcome; the
journal writer records `persisted` or `optional_drop` only after its asynchronous
commit attempt finishes. Immediate queue rejection is also an `optional_drop`,
and journal write failures remain available through the separate journal-failure
counter.

## OTLP instrument names and encoding

The OTLP pipeline exports the same snapshot under dotted names, all cumulative
unless noted: `nornrune.evaluation.batches`, `nornrune.evaluation.batch_failures`,
`nornrune.evaluation.rows`, `nornrune.evaluation.outcomes{outcome}`,
`nornrune.evaluation.escalations{reason}`, `nornrune.audit.outcomes{outcome}`,
`nornrune.policy.reloads{outcome}`, `nornrune.telemetry.export_drops`,
`nornrune.shutdown.failures`, `nornrune.evaluation.duration_ns`,
`nornrune.service.queue_wait_ns`, `nornrune.evaluation.duration_bucket{le}`,
`nornrune.service.queue_wait_bucket{le}`, and the gauges
`nornrune.service.queue_depth` and `nornrune.service.active_admissions`.
Duration and queue buckets are counters labeled with a numeric-second `le`
bound (`0.00001` … `10`) plus `+Inf`, carrying **cumulative monotonic**
counts; the `+Inf` bucket equals the total observation count, so
`histogram_quantile` works on OTLP-to-Prometheus converted series exactly as
it does on the native Prometheus histograms. OTLP integer counters saturate at
`2^63 - 1024`, the largest SDK-safe sum below the signed boundary, rather than
wrapping after the internal `uint64` counters exceed that range.

## Fixed cardinality and privacy

Labels have fixed cardinality: `outcome` is one of the four fixed decisions,
`reason` is one of the nine fixed engine reasons, audit and reload labels are
fixed enums, and `tier` is the runtime SIMD tier. No other label exists.
Telemetry APIs accept no request ID, evidence value, policy name or hash,
policy source, user, URL, database credentials, or error string; those values
are not representable in the telemetry types. Span attributes carry only the
fixed operation name. If a value of that shape ever needs to be recorded, it
must become a new fixed enum first.

## Modes

The server always maintains the atomic counters and serves Prometheus at
`/metrics`, because the batch fold is allocation-free and the scrape endpoint
is part of the service contract. `NORNRUNE_TELEMETRY_ENABLED` gates the OTLP
pipeline and tracing:

- **Disabled (default):** `NORNRUNE_TELEMETRY_ENABLED=false`. No OTLP export,
  no spans, no export goroutines; an endpoint configured while disabled is
  never contacted. A `telemetry.Runtime` constructed with `Enabled: false`
  outside the server records nothing at all: no counters, no providers, no
  goroutines.
- **Counters-only / Prometheus:** enabled with no `NORNRUNE_OTEL_ENDPOINT`.
  Atoms update per batch and are exposed only at `/metrics`, which scrapes one
  immutable snapshot per request.
- **OTLP:** enabled with an endpoint. The same snapshot feeds periodic OTLP
  metric export, and sampled tracing is exported over OTLP HTTP.

Configuration is validated for bounded values (`NORNRUNE_TRACE_SAMPLE_RATIO`
in [0,1], `NORNRUNE_TELEMETRY_EXPORT_INTERVAL`, `NORNRUNE_TELEMETRY_QUEUE_SIZE`)
and rejects credentialed or non-HTTP OTLP endpoints.

## Tracing

Spans cover admission, decode, policy lookup, evaluation, audit
acknowledgment, and response encoding. HTTP adapters extract and inject W3C
trace context (`traceparent`/`tracestate`) headers; the gRPC adapter uses an
OpenTelemetry stats handler with the same propagator. Sampling is
`ParentBased(TraceIDRatioBased)` with the configured ratio. No row-level or
node-level spans exist. Span status descriptions carry only the fixed gRPC or
HTTP status message constants this codebase emits; tests scan exported span
attributes and status descriptions for protected values and fail on any
match.

## Backpressure and shutdown

Export queues are bounded and non-blocking. The custom span processor increments
`nornrune_telemetry_export_drops_total` for each sampled span rejected by a full
queue, for each late sampled span delivered after shutdown admission closes, and
once for each failed span batch export. Accepted spans left queued when the
internally bounded cleanup deadline expires are removed and counted so span
snapshots are not retained. The metric exporter increments the same counter once
per failed metric export. `ForceFlush` adds one fallback drop only when a provider
reports an error and the shared drop count did not otherwise advance during that
call, avoiding double counting. The queue size bound is the configured
`NORNRUNE_TELEMETRY_QUEUE_SIZE`. Exporter availability never gates policy
evaluation or required audit persistence.

Shutdown processes traces before metrics so a trace shutdown failure is visible
in the final metric export. Shutdown admission closes atomically before the final
drain, and no span can enqueue after that drain. Exactly-once cleanup continues
under an internal bounded context even if the initiating caller cancels. A
caller-supplied deadline bounds only that caller's wait; later callers can wait
independently and receive the terminal exporter result. The lifecycle
`FlushTelemetry` hook still runs after the database closes and before workers
join. A failed terminal shutdown increments `nornrune_shutdown_failures_total`
once and joins provider errors without blocking cleanup of other components.

## Readiness and liveness

`/readyz`, `/livez`, and dependency health remain separate from telemetry and
never read telemetry state. Active policy version, SIMD tier, and build
version appear only as bounded gauge metadata, not as alert labels.

## Alerting

`deploy/telemetry/prometheus-rules.yaml` defines bounded multi-window alerts
for decision-rate changes, escalation spikes, audit failures, queue
saturation, reload failures, and shutdown timeouts. Alerts carry only fixed
`severity` labels; annotations never interpolate protected values. The decision
mix rule compares per-outcome shares only when both windows contain traffic, so
uniform volume changes and idle windows do not alert. Queue pressure alerts at
two waiting requests per configured evaluation worker, matching the default
admission bound.

## Verification and overhead

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/telemetry ./telemetry ./internal/observability
timeout 240s go test -count=1 -timeout 210s -race ./internal/telemetry ./telemetry ./internal/observability
timeout 300s go test -run '^$' -bench 'Benchmark(ObserveBatch|Telemetry|Metrics)' -benchmem -count=6 -timeout 270s ./internal/telemetry ./telemetry ./internal/observability
```

On the documented development machine (see `docs/performance.md`), disabled
recording is a no-op at `0 B/op` and `0 allocs/op`; counters-only updates and
snapshots are also allocation-free; spans and scrapes are boundary costs
reported separately. These are same-machine comparisons, not cross-runtime
throughput claims.
