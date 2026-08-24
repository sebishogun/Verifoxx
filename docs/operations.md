# Operations

Verifoxx has two deployment boundaries:

- Embedded CLI mode evaluates the checked-in policy and fixtures without a
  database or network listener.
- Service mode requires PostgreSQL, publishes an immutable policy, and serves
  HTTP and gRPC under bounded admission and audit settings.

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
export VERIFOXX_DATABASE_URL='postgresql://verifoxx_runtime:verifoxx-runtime-local@127.0.0.1:5432/verifoxx?sslmode=disable'
export VERIFOXX_AUDIT_MODE=required
export VERIFOXX_WORKERS=2
export VERIFOXX_QUEUE_DEPTH=8
timeout 30m ./cli/devx serve
```

On startup the service connects to PostgreSQL, allocates all fixed worker and
journal storage, publishes the embedded policy, binds both listeners, and then
accepts traffic. Startup fails rather than serving without its configured
database, active policy, valid limits, journal, or listener.

## Configuration

The product `serve`, `migrate`, and healthcheck commands currently call the
configuration loader without command-line configuration arguments. Their
effective precedence is:

1. `VERIFOXX_*` environment variables;
2. strict JSON selected by `VERIFOXX_CONFIG`; and
3. built-in defaults.

Unknown JSON fields and trailing JSON values are rejected. The configuration
package supports flags for embedded callers, but service-specific flags are not
exposed by the current Cobra product command.

| Setting | Environment | Default |
|---|---|---|
| HTTP address | `VERIFOXX_HTTP_ADDRESS` | `127.0.0.1:8080` |
| gRPC address | `VERIFOXX_GRPC_ADDRESS` | `127.0.0.1:9090` |
| policy name | `VERIFOXX_POLICY_NAME` | `verifoxx` |
| database URL | `VERIFOXX_DATABASE_URL` | unset |
| request timeout | `VERIFOXX_REQUEST_TIMEOUT` | `30s` |
| shutdown timeout | `VERIFOXX_SHUTDOWN_TIMEOUT` | `30s` |
| max body/output bytes | `VERIFOXX_MAX_BODY_BYTES` | 8 MiB |
| max batch rows | `VERIFOXX_MAX_BATCH_ROWS` | 65,536 |
| evaluator workers | `VERIFOXX_WORKERS` | `min(GOMAXPROCS, 256)` |
| admission depth | `VERIFOXX_QUEUE_DEPTH` | `min(workers * 2, 4096)` |
| audit mode | `VERIFOXX_AUDIT_MODE` | `off` |
| audit writers | `VERIFOXX_AUDIT_WRITERS` | `1` |
| audit queue depth | `VERIFOXX_AUDIT_QUEUE_DEPTH` | `64` |
| audit write timeout | `VERIFOXX_AUDIT_WRITE_TIMEOUT` | `5s` |
| database connect timeout | `VERIFOXX_DATABASE_CONNECT_TIMEOUT` | `5s` |
| database pool minimum | `VERIFOXX_DATABASE_MIN_CONNECTIONS` | `0` |
| database pool maximum | `VERIFOXX_DATABASE_MAX_CONNECTIONS` | `16` |

The audit write timeout must be shorter than the shutdown timeout whenever
auditing is enabled. Hard ceilings are 256 workers, 4,096 admissions, 64 MiB
body/output, 1,048,576 rows, 262,144 evidence records, and a 30-minute request
timeout. The admission setting creates up to 4,096 active slots and allows the
same number of callers to wait for a slot before returning `service busy`.

`VERIFOXX_POLICY_NAME` is validated configuration for durable reload and
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

Compose health checks run the hidden `verifoxx healthcheck` command, which
probes the configured HTTP `/readyz` endpoint with a five-second client timeout.

## Metrics

Prometheus text exposition is available at `/metrics`. Collectors use fixed
names and labels and update once per batch, not once per node:

| Metric | Meaning |
|---|---|
| `verifoxx_evaluation_batches_total` | completed or failed observed batches |
| `verifoxx_evaluation_batch_failures_total` | failed observations |
| `verifoxx_evaluation_rows_total` | request rows presented |
| `verifoxx_evaluation_duration_seconds` | end-to-end batch histogram |
| `verifoxx_evaluation_outcomes_total{outcome}` | four bounded decision labels |
| `verifoxx_service_queue_depth` | callers waiting for admission |
| `verifoxx_audit_journal_failures_total` | failed audit batches |
| `verifoxx_evaluation_workers` | configured engine worker count |
| `verifoxx_simd_tier_info{tier}` | selected runtime SIMD tier |

Best-effort queue drops are retained in `Journal.Stats` but are not currently a
separate Prometheus collector. Listener reconnect metrics and pprof helpers also
exist as package APIs but are not wired into `server.Serve`.

## Capacity And Alerts

Treat sustained queue depth near `VERIFOXX_QUEUE_DEPTH`, audit failures, HTTP
`503`, gRPC `Unavailable`, readiness failure, and request deadline growth as
capacity or dependency incidents. Diagnose in this order:

1. check `/livez` and `/readyz` separately;
2. inspect PostgreSQL health and connection saturation;
3. inspect admission depth, evaluation latency, and journal failures;
4. confirm the selected SIMD tier and configured worker count;
5. reproduce with one bounded request before increasing limits; and
6. use the [performance methodology](performance.md#methodology) before changing
   worker or crossover settings.

Increasing queue depth increases retained admission and audit storage and may
only hide a slow database. Worker count is bounded at construction; it is not a
runtime autoscaling control.

## Graceful Shutdown

SIGINT or SIGTERM cancels the root command context. One process-owned lifecycle:

1. rejects new requests and wakes queued callers;
2. drains admitted evaluations under the shutdown budget minus reserved journal
   flush time;
3. drains required or best-effort journal slots;
4. gracefully stops HTTP and gRPC, forcing gRPC stop at deadline;
5. closes PostgreSQL; and
6. reports joined shutdown errors.

Set the container stop grace period at least as long as
`VERIFOXX_SHUTDOWN_TIMEOUT`. The checked-in Compose file uses 30 seconds, which
matches its service default.

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
