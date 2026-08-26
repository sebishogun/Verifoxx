# PostgreSQL 19 Compose And Migrations Design

**Status:** Approved

**Date:** 2026-08-23

## Goal

Add the durable PostgreSQL 19 boundary needed by later policy publication,
decision journaling, SQL/PGQ projection, and service tasks without placing a
database call anywhere in policy compilation or evaluation.

Task 27 provides a local Compose database, separate migration and runtime
roles, the initial relational schema, a transactional migration library, and
self-contained PostgreSQL integration tests. It does not persist Programs or
decisions yet; Tasks 28 and 29 own those adapters.

## Dependencies

- PostgreSQL image: `postgres:19beta3`
- PostgreSQL driver and pool: `github.com/jackc/pgx/v5 v5.10.0`
- Integration containers: `github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0`

PostgreSQL 19 Beta 3 is the current approved database target. The image moves
to PostgreSQL 19 GA when available; no PostgreSQL 18 compatibility path is
added. Testcontainers is integration-test infrastructure only. Default unit,
purego, 386, and race suites do not start Docker.

## Compose Boundary

`compose.yaml` runs one PostgreSQL service with:

- the pinned Beta 3 image;
- a named persistent volume at PostgreSQL's data directory;
- a configurable port bound to `127.0.0.1` only;
- `pg_isready` health checks with bounded intervals and retries;
- a restart policy suitable for local development; and
- an entrypoint initialization script that creates dedicated login roles.

`.env.example` contains clearly marked local-development defaults for the
admin, migration, and runtime passwords, database name, host port, and image.
No real `.env` or production secret is committed. Changing bootstrap
credentials for an existing named volume requires recreating that local
volume, because PostgreSQL entrypoint scripts run only on first initialization.

`docker/postgres/init-roles.sh` is shared by Compose and Testcontainers. It
uses fixed role names, reads passwords from environment variables, passes them
to `psql` as safely quoted variables, and creates:

- `nornrune_migrator`, which owns the application schema and DDL; and
- `nornrune_runtime`, which receives only explicit runtime grants.

The bootstrap `postgres` role is local infrastructure and is not used by the
application or migration library.

## Schema Boundary

The migration ledger is
`public.nornrune_schema_migrations`. Application data is isolated in a
`nornrune` schema owned by `nornrune_migrator`. Public schema creation and
default privileges are revoked where appropriate.

Migration `000001_initial` creates these tables:

### `policies`

One stable policy identity per non-empty unique name. `active_version_id` is
the only mutable publication pointer and may be null before first publication.
A composite foreign key guarantees the active version belongs to the same
policy.

### `policy_versions`

Immutable policy publications containing policy identity, semantic version,
exact source bytes, 32-byte SHA-256 content hash, compiler version, and
publication timestamp. Content hashes are globally unique; semantic versions
are unique within one policy.

### `requests`

Immutable request-metadata snapshots containing the caller-visible request
key, 32-byte content hash, JSON object payload, and capture timestamp. The
table never stores protected dataset rows. A request key may have several
versions, while identical key/hash pairs deduplicate.

### `evidence_snapshots`

Immutable evidence metadata and provenance snapshots containing evidence key,
32-byte content hash, JSON object payload, capture time, and optional expiry.
Identical key/hash pairs deduplicate.

### `evaluation_runs`

One immutable batch-level audit record with a unique idempotency key, exact
policy version, engine version, start/completion times, row count, and JSON
execution metadata.

### `evaluation_findings`

One ordered immutable decision row per request in an evaluation run. It pins
the request snapshot, exact `Approve`/`Reject`/`Revise`/`Escalate` outcome,
rationale, driver requirement/clause/reason, applied requirements, missing or
conflicting evidence text, assumptions, uncertainty, and structured JSON
remediation.

### `evaluation_evidence`

An immutable ordered many-to-many link from one finding to exact evidence
snapshots. Its foreign key includes run ID and row index so evidence cannot be
attached to the wrong finding.

### `debug_traces`

Optional derived trace payloads with policy/run references, explicit format,
content hash, creation time, and retention expiry. They may be deleted by
future maintenance tooling.

### `benchmark_runs`

Derived benchmark history with optional policy version, engine version,
environment, parameters, measurements, and timestamp. It remains separate
from evaluator execution and may be pruned.

Policy graph node/edge tables and the PostgreSQL property graph are excluded
from `000001`; Task 30 adds them in `000002_policy_graph`.

## Integrity And Privileges

Checks enforce non-empty identifiers, exact 32-byte hashes, valid JSON
container shapes, nonnegative row indexes/counts, coherent timestamps, and the
four allowed decision names. Foreign-key indexes are explicit.

Published policy versions, request/evidence snapshots, evaluation runs,
findings, and evidence links reject `UPDATE` and `DELETE` through a shared
trigger. Corrections create new immutable rows. The trigger also protects
against accidental mutation by the migration owner; dropping the schema in a
down migration remains DDL and is unaffected.

`nornrune_runtime` receives schema usage, required sequence usage, explicit
`SELECT`/`INSERT` table grants, and column-scoped permission to update only
`policies.active_version_id`. It cannot create schema objects or update/delete
immutable records. Migration-ledger access remains migration-only.

## Migration Library

`internal/adapters/postgres/migrate.go` exposes a small pgxpool-based API:

```go
func NewMigrator(pool *pgxpool.Pool, source fs.FS) (*Migrator, error)
func (m *Migrator) Up(ctx context.Context) (int, error)
func (m *Migrator) Down(ctx context.Context, steps uint32) (int, error)
```

The injected filesystem is rooted at the migration directory. This keeps the
adapter testable with `fstest.MapFS` and lets later `devx` tooling pass an
`os.DirFS` without embedding or duplicating SQL.

Discovery accepts only paired
`NNNNNN_name.up.sql`/`NNNNNN_name.down.sql` files. Versions start at one, are
unique and contiguous, and names for both directions must match. A SHA-256
checksum covers the up bytes, one separator byte, and the down bytes. An
already-applied version with changed name or checksum fails before SQL runs.

Each complete `Up` or bounded `Down` invocation runs in one transaction:

1. Begin a transaction on pgxpool.
2. Acquire a fixed `pg_advisory_xact_lock` key.
3. Create or inspect the migration ledger under that lock.
4. Reconcile discovered and applied migrations.
5. Execute complete SQL files through pgx simple protocol.
6. Update the ledger in the same transaction.
7. Commit, automatically releasing the advisory lock.

Any discovery, checksum, SQL, context, or commit failure returns without a
partial schema. Transaction-scoped locking avoids leaking a session lock into
a pooled connection after cancellation or connection failure.

Errors identify the operation and, where applicable, migration version and
name. They wrap causes for `errors.Is`/`errors.As` but never include a DSN,
password, or complete environment dump.

## Tests

Untagged unit tests use `fstest.MapFS` to cover:

- deterministic ordering;
- malformed names and zero versions;
- duplicate versions or directions;
- missing pairs and mismatched names;
- version gaps;
- deterministic combined checksums; and
- nil dependencies and invalid down counts.

Integration tests use the PostgreSQL Testcontainers module with
`postgres:19beta3`, random host ports, the shared role script, bounded wait
strategies, and cleanup registered immediately after startup. They verify:

- PostgreSQL major version 19 and valid Compose configuration;
- migration/runtime login separation;
- initial up and exact table/constraint shape;
- idempotent repeated up;
- concurrent up serialization across separate pooled connections;
- applied-file checksum drift rejection;
- runtime insert/select permissions and denied DDL/update/delete operations;
- database-enforced immutability;
- one-step down and clean re-up; and
- rollback leaves neither domain objects nor a ledger row after failed SQL.

All test, build, Docker, and migration invocations have explicit timeouts.
Integration tests run only with `-tags=integration`; an explicitly requested
integration run fails when Docker is unavailable rather than silently skipping
database coverage.

## Exclusions

Task 27 does not add policy persistence methods, audit writer goroutines,
LISTEN/NOTIFY, SQL/PGQ graph projection, service configuration, a migration CLI,
or production TLS. Those remain in Tasks 28-32 and 40. PostgreSQL never appears
in compiler, evaluator, result materialization, or CLI demo kernels.
