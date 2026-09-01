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
it does on the native Prometheus histograms.

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

Export queues are bounded and non-blocking: the OTLP batch span processor
silently drops spans past its queue limit, and any failed or dropped export
flush increments `nornrune_telemetry_export_drops_total`. Queue-full span
drops are dropped by the SDK without a counter; the queue size bound is the
configured `NORNRUNE_TELEMETRY_QUEUE_SIZE`. Exporter availability never gates
policy evaluation or required audit persistence. Shutdown flushes both
providers within the caller-supplied deadline as the lifecycle
`FlushTelemetry` hook, after the database closes and before workers join; a
flush failure increments `nornrune_shutdown_failures_total` and joins the
shutdown error without blocking cleanup of other components.

## Readiness and liveness

`/readyz`, `/livez`, and dependency health remain separate from telemetry and
never read telemetry state. Active policy version, SIMD tier, and build
version appear only as bounded gauge metadata, not as alert labels.

## Alerting

`deploy/telemetry/prometheus-rules.yaml` defines bounded multi-window alerts
for decision-rate changes, escalation spikes, audit failures, queue
saturation, reload failures, and shutdown timeouts. Alerts carry only fixed
`severity` labels; annotations never interpolate protected values.

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
