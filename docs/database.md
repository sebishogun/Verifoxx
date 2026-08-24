# Database

PostgreSQL stores policy identity, immutable source versions, replay inputs,
decision records, and a derived policy graph. Evaluation itself remains in
process over an immutable `Program`; no SQL query occurs per request row,
policy node, clause, or evidence record.

## PostgreSQL 19 Beta

[`compose.yaml`](../compose.yaml) currently pins `postgres:19beta3`. PostgreSQL
19 and its SQL/PGQ property-graph syntax are beta dependencies until the image
moves to a PostgreSQL 19 GA release. Do not treat the beta data directory as an
in-place GA upgrade path. Take a logical backup and use the PostgreSQL release
upgrade procedure when changing image versions.

There is no PostgreSQL 18 compatibility path because migration 000002 creates a
PostgreSQL 19 `PROPERTY GRAPH`.

## Roles And Trust Boundary

The local container initializes two non-superuser roles:

| Role | Responsibility |
|---|---|
| `verifoxx_migrator` | owns schema changes and the migration ledger |
| `verifoxx_runtime` | selects and inserts runtime data and updates only `policies.active_version_id` |

Public schema, table, sequence, function, and graph privileges are revoked.
Published policy versions, request snapshots, evidence snapshots, evaluation
runs, findings, evidence links, policy nodes, and policy edges reject updates
and deletes through triggers. Corrections create new versions or records.

The credentials in `compose.yaml` are loopback-only development defaults. Use
separate secrets, TLS, and separately managed roles outside local development.

## Migrations

Migration pairs are embedded from [`migrations`](../migrations). Names follow
`NNNNNN_name.up.sql` and `NNNNNN_name.down.sql`, with contiguous versions from
one. The migrator:

1. begins one transaction;
2. takes a fixed `pg_advisory_xact_lock`;
3. creates or reads `public.verifoxx_schema_migrations`;
4. reconciles every applied version, name, and SHA-256 checksum;
5. applies all pending migrations in order; and
6. commits the ledger and schema together.

The checksum covers both up and down source. Edited applied migrations,
unknown database versions, gaps, duplicate directions, and unpaired files fail
as migration drift. A failed migration transaction rolls back as one unit.

Start the local database:

```bash
./cli/devx db:up
```

The following workflows run isolated integration verification against a
container; they do not mutate the long-running Compose database:

```bash
./cli/devx migrate
./cli/devx graph:check
```

Full Compose applies the embedded migrations through its dedicated migrator
service. To apply them manually to the local Compose database, use the migration
role:

```bash
VERIFOXX_DATABASE_URL='postgresql://verifoxx_migrator:verifoxx-migrator-local@127.0.0.1:5432/verifoxx?sslmode=disable' \
  timeout 120s go run ./cmd/verifoxx migrate
```

Create a new exclusive pair with:

```bash
./cli/devx migrate:create --name add_example
./cli/devx migrate:check
```

Review both directions. Never repair drift by editing the ledger or an applied
migration.

## Schema

Migration 000001 creates:

| Table | Canonical responsibility |
|---|---|
| `policies` | stable policy name and active version pointer |
| `policy_versions` | source, semantic version, hash, compiler version |
| `requests` | content-addressed request snapshots |
| `evidence_snapshots` | content-addressed evidence plus capture/expiry time |
| `evaluation_runs` | idempotent run metadata and pinned policy version |
| `evaluation_findings` | decision, rationale, provenance, uncertainty, remediation |
| `evaluation_evidence` | ordered finding-to-evidence references |
| `debug_traces` | optional expiring trace payloads |
| `benchmark_runs` | optional retained measurement metadata |

Migration 000002 adds immutable `policy_nodes` and `policy_edges`, typed views,
and the `verifoxx.policy_graph` property graph.

## Policy Publication

`PolicyStore.PublishActive` uses one transaction to:

1. ensure the stable policy identity;
2. insert source metadata idempotently by content hash;
3. validate any existing row against the candidate;
4. insert the complete graph projection under a transaction-local claim;
5. update the active version; and
6. queue `pg_notify` with the content hash.

The notification is visible only after commit. Other instances use a dedicated
`LISTEN` connection, reload the durable active version after every reconnect,
verify the notification hash, compile if absent locally, and atomically activate
the immutable program. The current `server.Serve` path publishes the embedded
policy at startup; the reusable listener is available for multi-instance
coordination but is not started by that path yet.

## Audit Transactions

One accepted audit batch writes one `evaluation_runs` row, content-addressed
request and evidence snapshots, all findings, and all evidence links in one
transaction. `COPY` is used for finding and evidence-link rows. The
idempotency key makes a retry of the same materialized journal batch a no-op
rather than a duplicate. A new API evaluation receives a new start time and
sequence and is a distinct run even when its policy, request, and evidence
bytes match.

Audit behavior is configured as `off`, `best-effort`, or `required`:

- `off` performs no persistence.
- `best-effort` returns the result after bounded journal admission; write
  failures and full-queue drops are metrics.
- `required` returns success only after the audit transaction commits.

## Property Graph

The graph is a derived inspection view of the canonical policy version.
Vertices represent policy version, requirement, clause, expression, evidence
requirement, outcome, and remediation. Edges are `CONTAINS`, `CHILD`,
`APPLIES_WHEN`, `REQUIRES`, `RESOLVES_TO`, and `REMEDIATES_WITH`.

Graph rows carry one policy version and are immutable after their publication
transaction. They can be rebuilt from canonical policy source if derived graph
data is lost. They are never consulted by the evaluator.

## Recovery

### Process Or Transaction Failure

- Before commit, PostgreSQL rollback leaves no partial policy, graph, or audit
  batch.
- After policy commit but before local activation, the durable active version
  is authoritative; restart or `Publisher.ReloadActive` recompiles and activates
  it.
- A required audit failure returns service unavailable, so the caller must not
  treat that evaluation as durably accepted.
- A best-effort failure leaves the response valid but increments journal failure
  or drop counters.

### Migration Drift

Stop the service, preserve the database, and compare the embedded pair with the
ledger. Restore the matching application source or restore a backup. Do not
change checksums or renumber applied files. Run `./cli/devx migrate` against a
fresh isolated database before retrying production migration.

### Backup And Restore

Back up canonical tables and the migration ledger before image upgrades or
schema changes. For the local stack, a logical dump can be captured with:

```bash
timeout 120s docker compose exec -T postgres pg_dump -U postgres -d verifoxx -Fc > verifoxx.dump
```

Restore into a newly initialized cluster whose roles exist, then run the product
`migrate` command against that restored database so its ledger is reconciled.
Use `./cli/devx graph:check` to verify that the application source and selected
PostgreSQL image can create and query the graph in an isolated database before
starting service traffic. `./cli/devx db:reset` deletes the local Compose volume
and is destructive; it is not a recovery command.

See [operations](operations.md) for service startup and health checks.
