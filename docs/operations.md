# Operations

NornRune has two deployment boundaries:

- Embedded CLI mode evaluates the checked-in policy and fixtures without a
  database or network listener.
- Service mode requires PostgreSQL, publishes an immutable policy, and serves
  HTTP and gRPC under bounded admission and audit settings.

CEL, Rego, and Cedar are explicit CLI compilation adapters, and Protobuf is a
generation-only adapter. Service publication and persisted registry policies
remain native JSON; runtime services do not load parser engines or reflect over
descriptors. See the [compatibility frontend guide](frontends.md).

## Preflight

Report which local workflows can run:

```bash
./cli/devx status
```

Start the complete local stack with PostgreSQL, migrations, required auditing,
HTTP, and gRPC:

```bash
timeout 600s ./cli/devx full
```

Compose publishes PostgreSQL, HTTP, and gRPC only on `127.0.0.1`. Its passwords
and disabled database TLS are development defaults, not deployment settings.

## Direct Service Startup

Start and migrate PostgreSQL as described in the [database guide](database.md),
then configure the runtime role:

```bash
export NORNRUNE_DATABASE_URL='postgresql://nornrune_runtime:nornrune-runtime-local@127.0.0.1:5432/nornrune?sslmode=disable'
export NORNRUNE_AUDIT_MODE=required
export NORNRUNE_WORKERS=2
export NORNRUNE_QUEUE_DEPTH=8
timeout 30m ./cli/devx serve
```

On startup the service connects to PostgreSQL, allocates fixed request
workspaces, scheduler workers, and journal storage, publishes the embedded
policy, binds both listeners, and then accepts traffic. Startup fails rather
than serving without its configured database, active policy, valid limits,
journal, scheduler, or listener.

## Configuration

The product `serve`, `migrate`, and healthcheck commands currently call the
configuration loader without command-line configuration arguments. Their
effective precedence is:

1. `NORNRUNE_*` environment variables;
2. strict JSON selected by `NORNRUNE_CONFIG`; and
3. built-in defaults.

Unknown JSON fields and trailing JSON values are rejected. The configuration
package supports flags for embedded callers, but service-specific flags are not
exposed by the current Cobra product command.

| Setting | Environment | Default |
|---|---|---|
| HTTP address | `NORNRUNE_HTTP_ADDRESS` | `127.0.0.1:8080` |
| gRPC address | `NORNRUNE_GRPC_ADDRESS` | `127.0.0.1:9090` |
| policy name | `NORNRUNE_POLICY_NAME` | `nornrune` |
| database URL | `NORNRUNE_DATABASE_URL` | unset |
| request timeout | `NORNRUNE_REQUEST_TIMEOUT` | `30s` |
| shutdown timeout | `NORNRUNE_SHUTDOWN_TIMEOUT` | `30s` |
| max body/output bytes | `NORNRUNE_MAX_BODY_BYTES` | 8 MiB |
| max batch rows | `NORNRUNE_MAX_BATCH_ROWS` | 65,536 |
| evaluator workers | `NORNRUNE_WORKERS` | `min(GOMAXPROCS, 256)` |
| admission depth | `NORNRUNE_QUEUE_DEPTH` | `min(workers * 2, 4096)` |
| audit mode | `NORNRUNE_AUDIT_MODE` | `off` |
| audit writers | `NORNRUNE_AUDIT_WRITERS` | `1` |
| audit queue depth | `NORNRUNE_AUDIT_QUEUE_DEPTH` | `64` |
| audit write timeout | `NORNRUNE_AUDIT_WRITE_TIMEOUT` | `5s` |
| database connect timeout | `NORNRUNE_DATABASE_CONNECT_TIMEOUT` | `5s` |
| database pool minimum | `NORNRUNE_DATABASE_MIN_CONNECTIONS` | `0` |
| database pool maximum | `NORNRUNE_DATABASE_MAX_CONNECTIONS` | `16` |

The audit write timeout must be shorter than the shutdown timeout whenever
auditing is enabled. Hard ceilings are 256 workers, 4,096 admissions, 64 MiB
body/output, 1,048,576 rows, 262,144 evidence records, and a 30-minute request
timeout. The admission setting creates up to 4,096 active slots and allows the
same number of callers to wait for a slot before returning `service busy`.
One process-wide scheduler also limits admitted evaluator batches to
`min(queue depth, workers)` and shares exactly `workers` shard tokens across all
requests. Increasing admission depth does not create more evaluator goroutines.

`NORNRUNE_POLICY_NAME` is validated configuration for durable reload and
multi-instance listener use. The current `server.Serve` startup publishes the
embedded policy identity and does not use that setting to select another
policy.

Database URLs are stored in a redacting type: formatting, structured logging,
JSON, and text serialization return `[REDACTED]` rather than credentials.

## Audit Modes

| Mode | Response boundary | Full queue or write failure |
|---|---|---|
| `off` | after encoding | no journal exists |
| `best-effort` | after bounded journal submission | result succeeds; drop/failure metric increments |
| `required` | after PostgreSQL commit | request fails as audit unavailable |

Compose service mode uses `required`. Admission remains owned through the
required acknowledgment, so the configured queue bounds requests waiting on
the database as well as requests evaluating.

## Health

```bash
curl -fsS http://127.0.0.1:8080/livez
curl -fsS http://127.0.0.1:8080/readyz
```

`/livez` checks that the probe can execute; graceful shutdown and dependency
failure do not make the process dead. `/readyz` and `/healthz` require open
admission, an active policy, and a successful PostgreSQL ping. During shutdown,
readiness fails before active requests drain.

Compose health checks run the hidden `nornrune healthcheck` command, which
probes the configured HTTP `/readyz` endpoint with a five-second client timeout.

## Metrics

Prometheus text exposition is available at `/metrics`. One immutable snapshot
per scrape feeds fixed-name, fixed-cardinality series that update once per
batch or bounded service event, never once per node. The complete table,
including escalation reasons, audit and reload outcomes, queue wait, active
admissions, and telemetry drop counters, is defined in the
[production telemetry guide](telemetry.md).

| Metric | Meaning |
|---|---|
| `nornrune_evaluation_batches_total` | completed or failed observed batches |
| `nornrune_evaluation_batch_failures_total` | failed observations |
| `nornrune_evaluation_rows_total` | request rows presented |
| `nornrune_evaluation_duration_seconds` | end-to-end batch histogram |
| `nornrune_evaluation_outcomes_total{outcome}` | four bounded decision labels |
| `nornrune_evaluation_escalations_total{reason}` | nine bounded escalation reasons |
| `nornrune_service_queue_depth` | callers waiting for admission |
| `nornrune_audit_journal_failures_total` | failed audit batches |
| `nornrune_evaluation_workers` | configured scheduler and request-workspace count |
| `nornrune_simd_tier_info{tier}` | selected runtime SIMD tier |

Optional OTLP export and sampled tracing share the same snapshot; bounded
alerting rules ship in `deploy/telemetry/prometheus-rules.yaml`.

## Capacity And Alerts

Treat sustained queue depth near `NORNRUNE_QUEUE_DEPTH`, audit failures, HTTP
`503`, gRPC `Unavailable`, readiness failure, and request deadline growth as
capacity or dependency incidents. Diagnose in this order:

1. check `/livez` and `/readyz` separately;
2. inspect PostgreSQL health and connection saturation;
3. inspect admission depth, evaluation latency, and journal failures;
4. confirm the selected SIMD tier and configured worker count;
5. reproduce with one bounded request before increasing limits; and
6. use the [performance methodology](performance.md#methodology) before changing
   worker or crossover settings.

Increasing queue depth increases retained admission and scheduler bookkeeping
and may only hide a slow database. Worker count is bounded at construction; it
is not a runtime autoscaling control.

Use the offline benchmark rather than a production endpoint when checking local
evaluation capacity:

```bash
timeout 120s go run ./cmd/nornrune bench --rows 4096 --iterations 100 --workers 4
```

## Graceful Shutdown

SIGINT or SIGTERM cancels the root command context. One process-owned lifecycle:

1. rejects new requests and wakes queued callers;
2. drains admitted evaluations under the shutdown budget minus reserved journal
   flush time;
3. drains required or best-effort journal slots;
4. gracefully stops HTTP and gRPC, forcing gRPC stop at deadline;
5. closes PostgreSQL; and
6. closes and joins scheduler workers;
7. reports joined shutdown errors.

Set the container stop grace period at least as long as
`NORNRUNE_SHUTDOWN_TIMEOUT`. The checked-in Compose file uses 30 seconds, which
matches its service default.

## Production Layout Gate

Run the same pinned field-alignment analysis used by CI:

```bash
timeout 300s ./scripts/check-fieldalignment.sh
```

The script checks production packages only and reports candidate struct
reorderings. Review each diagnostic against access locality and pointer-scan
cost; do not apply analyzer fixes automatically.

## Recovery And Upgrade

Canonical policy and audit writes are transactional and immutable. Follow the
[database recovery runbook](database.md#recovery) for migration drift, backup,
restore, and PostgreSQL 19 beta upgrades. A required-audit error means the
caller did not receive a durable success. An independent retry is a new
evaluation run; reconcile possible ambiguous commit outcomes at the calling
application boundary.

## Exposure Limits

The service does not implement client authentication, authorization, or TLS
termination. Keep direct listeners on loopback or a private network and place
an authenticated TLS proxy in front of non-local traffic. Use a TLS PostgreSQL
URL and external secret management outside development. Do not include raw
evidence payloads or database URLs in operational logs.
