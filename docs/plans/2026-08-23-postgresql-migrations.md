# PostgreSQL 19 Compose And Migrations Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a runnable local PostgreSQL 19 environment, least-privilege roles, the initial durable schema, and a transactional pgx migration library with self-contained integration coverage.

**Architecture:** Keep migration discovery as a pure filesystem operation and execute each complete up/down request in one pgx transaction protected by a transaction-scoped advisory lock. Compose and Testcontainers share one role-bootstrap script; tagged integration tests start one PostgreSQL 19 container, use separate pools for each role, and reset the schema and ledger between scenarios.

**Tech Stack:** Go 1.27, pgx/v5 v5.10.0, Testcontainers for Go PostgreSQL module v0.44.0, PostgreSQL 19 Beta 3, Docker Compose v2, SQL/PLpgSQL migrations.

**Repository Rule:** Do not create commits unless the user explicitly requests them. The commit checkpoint at the end is optional only.

---

### Task 1: Discover And Validate Migration Files

**Files:**
- Create: `internal/adapters/postgres/migrate.go`
- Create: `internal/adapters/postgres/migrate_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write failing discovery tests**

Use package `postgres` and `testing/fstest.MapFS`. Cover:

- deterministic numeric ordering independent of directory order;
- exact six-digit positive versions and lowercase underscore names;
- rejection of version zero, malformed names, unknown `.sql` directions,
  duplicate directions, duplicate versions, missing pairs, mismatched pair
  names, and version gaps;
- a hard-coded SHA-256 golden covering `up bytes + 0 byte + down bytes`;
- nil pool and nil filesystem constructor errors; and
- `Down(ctx, 0)` returning `ErrInvalidDownCount` before touching the pool.

Use compact fixtures such as:

```go
source := fstest.MapFS{
    "000001_initial.up.sql":   {Data: []byte("select 1;")},
    "000001_initial.down.sql": {Data: []byte("select 2;")},
}
```

Assert `errors.Is` for exported sentinel errors and inspect unexported migration
versions, names, SQL bytes, and checksums directly from the package test.

**Step 2: Run the unit test and verify failure**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/postgres
```

Expected: FAIL because the package and migration discovery do not exist.

**Step 3: Pin pgx and implement discovery**

```bash
timeout 120s go get github.com/jackc/pgx/v5@v5.10.0
```

Add these API and data boundaries:

```go
var (
    ErrInvalidMigrationSource = errors.New("postgres: invalid migration source")
    ErrInvalidDownCount       = errors.New("postgres: down count must be positive")
    ErrMigrationDrift         = errors.New("postgres: applied migration differs from source")
)

type Migrator struct {
    pool       *pgxpool.Pool
    migrations []migration
}

type migration struct {
    up       []byte
    down     []byte
    name     string
    checksum [sha256.Size]byte
    version  uint32
}

func NewMigrator(pool *pgxpool.Pool, source fs.FS) (*Migrator, error)
func (m *Migrator) Up(ctx context.Context) (int, error)
func (m *Migrator) Down(ctx context.Context, steps uint32) (int, error)
```

`NewMigrator` must reject nil dependencies, fully discover and read the source,
and publish a usable `Migrator` only after validation succeeds. Discovery must:

1. Read only the filesystem root with `fs.ReadDir(source, ".")`.
2. Ignore non-SQL files, but reject every `.sql` file that does not match
   `NNNNNN_name.up.sql` or `NNNNNN_name.down.sql`.
3. Parse the six digits without regular expressions, require version `>= 1`,
   and accept names containing only lowercase ASCII letters, digits, and
   underscores without leading/trailing underscores.
4. Reject files larger than 8 MiB before `fs.ReadFile`.
5. Pair by numeric version, require matching names, sort ascending, and require
   versions `1..N` without gaps.
6. Hash the exact up bytes, one zero separator byte, and exact down bytes.

Add an early `steps == 0` guard to `Down`; leave database execution returning a
temporary internal not-implemented error until Task 3.

**Step 4: Run discovery tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/postgres
```

Expected: PASS.

### Task 2: Add Compose And Shared Role Bootstrap

**Files:**
- Create: `compose.yaml`
- Create: `.env.example`
- Create: `docker/postgres/init-roles.sh`
- Create: `internal/adapters/postgres/migrations_integration_test.go`
- Modify: `.gitignore`
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Write the tagged bootstrap integration test**

Start the file with:

```go
//go:build integration

package postgres
```

Add one top-level `TestPostgreSQLMigrations` that creates a bounded context,
starts `postgres:19beta3`, and runs sequential subtests. Use:

```go
container, err := tcpostgres.Run(ctx, "postgres:19beta3",
    tcpostgres.WithDatabase("verifoxx"),
    tcpostgres.WithUsername("postgres"),
    tcpostgres.WithPassword(adminPassword),
    tcpostgres.WithInitScripts(initRolesPath),
    testcontainers.WithEnv(map[string]string{
        "VERIFOXX_MIGRATION_PASSWORD": migrationPassword,
        "VERIFOXX_RUNTIME_PASSWORD": runtimePassword,
    }),
    tcpostgres.BasicWaitStrategies(),
)
testcontainers.CleanupContainer(t, container)
if err != nil {
    t.Fatalf("start PostgreSQL 19: %v", err)
}
```

Register cleanup immediately after `Run`, before checking `err`. Resolve the
repository root with `filepath.Abs("../../..")`; do not search the filesystem.
Build migration/runtime connection URLs by parsing the container's admin URL
with `net/url` and replacing only `URL.User`, so passwords are escaped and are
never printed.

The first subtest must assert:

- `current_setting('server_version_num')::int / 10000 == 19`;
- migrator and runtime login pools both connect;
- `current_user` is the expected role in each pool;
- runtime cannot `CREATE SCHEMA`; and
- migrator can create and drop a scratch schema.

**Step 2: Pin Testcontainers and verify the red state**

```bash
timeout 120s go get github.com/testcontainers/testcontainers-go/modules/postgres@v0.44.0
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/bootstrap$' ./internal/adapters/postgres
```

Expected: FAIL because `docker/postgres/init-roles.sh` does not exist.

**Step 3: Add local configuration and bootstrap roles**

Add `.env` to `.gitignore`. Add local-only defaults to `.env.example`:

```dotenv
POSTGRES_IMAGE=postgres:19beta3
POSTGRES_DB=verifoxx
POSTGRES_PORT=5432
POSTGRES_ADMIN_PASSWORD=verifoxx-admin-local
VERIFOXX_MIGRATION_PASSWORD=verifoxx-migrator-local
VERIFOXX_RUNTIME_PASSWORD=verifoxx-runtime-local
```

`compose.yaml` must define one `postgres` service with:

- `${POSTGRES_IMAGE:-postgres:19beta3}`;
- fixed admin user `postgres` and the configured database/passwords;
- `127.0.0.1:${POSTGRES_PORT:-5432}:5432`;
- `docker/postgres/init-roles.sh` mounted read-only under
  `/docker-entrypoint-initdb.d/`;
- a named volume mounted at `/var/lib/postgresql`, which is the PostgreSQL 18+
  image volume boundary;
- `pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"` with shell-dollar
  escaping, 2-second interval, 5-second timeout, and 30 retries; and
- `restart: unless-stopped`.

The shell script must use `set -eu`, fixed role identifiers, and psql variables
for every password/database value:

```sql
CREATE ROLE verifoxx_migrator LOGIN PASSWORD :'migration_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
CREATE ROLE verifoxx_runtime LOGIN PASSWORD :'runtime_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
ALTER DATABASE :"database_name" OWNER TO verifoxx_migrator;
REVOKE ALL ON DATABASE :"database_name" FROM PUBLIC;
GRANT CONNECT ON DATABASE :"database_name" TO verifoxx_runtime;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
```

Invoke `psql` with `--set=migration_password=...`,
`--set=runtime_password=...`, and `--set=database_name=...`; never interpolate
secrets into SQL text in the shell.

**Step 4: Validate Compose and role bootstrap**

```bash
timeout 120s docker compose --env-file .env.example config --quiet
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/bootstrap$' ./internal/adapters/postgres
```

Expected: PASS.

### Task 3: Execute Transactional Up And Down Migrations

**Files:**
- Modify: `internal/adapters/postgres/migrate.go`
- Modify: `internal/adapters/postgres/migrations_integration_test.go`

**Step 1: Write failing migration-runner subtests**

Use `fstest.MapFS` with one migration that creates/drops a scratch table. Reset
`public.verifoxx_schema_migrations` and scratch objects before the subtest.
Assert:

- first `Up` returns 1 and creates the object plus one ledger row;
- repeated `Up` returns 0;
- `Down(ctx, 1)` returns 1 and removes the object and ledger row;
- repeated `Down(ctx, 1)` fails because no applied migration remains; and
- re-running `Up` succeeds.

Also assert the ledger row stores exact version, name, 32-byte checksum, and a
nonzero `applied_at` timestamp.

**Step 2: Run the subtest and verify failure**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/up_down$' ./internal/adapters/postgres
```

Expected: FAIL on the temporary migration execution error.

**Step 3: Implement one-transaction execution**

Use the fixed signed 64-bit advisory key `0x56455249464f5858`. For every `Up`
or bounded `Down`:

1. Begin one pgxpool transaction.
2. Defer rollback with a five-second context derived from
   `context.WithoutCancel(ctx)` so a canceled caller cannot retain a pooled
   transaction.
3. Execute `SELECT pg_advisory_xact_lock($1)`.
4. Create `public.verifoxx_schema_migrations` in the same transaction with
   positive integer version, non-empty name, exact 32-byte checksum, and
   `applied_at timestamptz NOT NULL DEFAULT clock_timestamp()`. Revoke public
   access and conditionally revoke `verifoxx_runtime` access when that role
   exists, without making the generic migrator depend on the application role.
5. Read applied rows ordered by version and require an exact local prefix:
   versions, names, and checksums must match before executing any SQL.
6. Execute each complete SQL file using
   `pgx.QueryExecModeSimpleProtocol`, then insert/delete its ledger row.
7. Commit once and return the number changed.

`Up` applies all pending migrations. `Down` requires `steps > 0` and
`steps <= applied migrations`, then executes newest-first. Wrap errors with the
operation plus migration version/name; do not include connection strings,
environment values, or full SQL text.

**Step 4: Run unit and runner tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/postgres
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/(bootstrap|up_down)$' ./internal/adapters/postgres
```

Expected: PASS.

### Task 4: Add The Initial Durable Schema

**Files:**
- Create: `migrations/000001_initial.up.sql`
- Create: `migrations/000001_initial.down.sql`
- Modify: `internal/adapters/postgres/migrations_integration_test.go`

**Step 1: Write failing schema-shape tests**

Run the real migration directory through `os.DirFS`. Assert these nine tables
exist in schema `verifoxx` and no graph table/property graph exists yet:

```text
policies
policy_versions
requests
evidence_snapshots
evaluation_runs
evaluation_findings
evaluation_evidence
debug_traces
benchmark_runs
```

Query `pg_constraint`, `pg_indexes`, and `information_schema.columns` to assert:

- every foreign key has an explicit leading-column index;
- policy content hashes and run idempotency keys are unique;
- `(policy_id, semantic_version)` and `(request_key, content_hash)` are unique;
- findings and evidence links use ordered composite keys;
- hashes are `bytea`, source/trace payloads are byte-preserving `bytea`, and
  user-shaped metadata is `jsonb`; and
- `(policies.id, active_version_id)` references
  `(policy_versions.policy_id, policy_versions.id)`.

**Step 2: Run the schema subtest and verify failure**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/initial_schema$' ./internal/adapters/postgres
```

Expected: FAIL because `000001_initial` does not exist.

**Step 3: Write `000001_initial.up.sql`**

Create schema `verifoxx` owned by `verifoxx_migrator`, revoke public access,
set a safe search path, and create the tables below with `bigint GENERATED
ALWAYS AS IDENTITY` internal IDs and `timestamptz` timestamps:

- `policies`: non-empty unique `name`, nullable `active_version_id`, and
  `created_at`; only the active pointer may change.
- `policy_versions`: policy FK, non-empty semantic/compiler versions, non-empty
  exact source bytes, globally unique 32-byte content hash, publication time,
  unique `(policy_id, semantic_version)`, and unique `(policy_id, id)` for the
  active-version composite FK.
- `requests`: non-empty request key, 32-byte hash, JSON object payload, capture
  time, and unique `(request_key, content_hash)`.
- `evidence_snapshots`: non-empty evidence key, 32-byte hash, JSON object
  payload, capture/optional expiry times, expiry not before capture, and unique
  `(evidence_key, content_hash)`.
- `evaluation_runs`: unique non-empty idempotency key, policy-version FK,
  non-empty engine version, coherent start/completion timestamps, nonnegative
  row count, and JSON object execution metadata.
- `evaluation_findings`: `(run_id, row_index)` primary key, request FK, one of
  exactly `Approve`, `Reject`, `Revise`, or `Escalate`, non-empty rationale,
  nullable driver requirement/clause/reason text, and JSON arrays for applied
  requirements, missing/conflicting evidence, assumptions, unresolved
  uncertainty, and remediation.
- `evaluation_evidence`: `(run_id, row_index, evidence_ordinal)` primary key,
  composite finding FK, evidence-snapshot FK, and nonnegative ordinal.
- `debug_traces`: policy-version FK, optional run FK, non-empty format, exact
  payload bytes, 32-byte hash, creation time, and retention expiry not before
  creation.
- `benchmark_runs`: optional policy-version FK, non-empty engine version, JSON
  object environment/parameters/measurements, and recorded time.

Add indexes beginning with every FK column sequence. Add one statement-level
trigger function that rejects `UPDATE` and `DELETE` on policy versions,
request/evidence snapshots, evaluation runs/findings/evidence links. Add a row
trigger on `policies` that rejects deletion and any update changing columns
other than `active_version_id`.

Grant `verifoxx_runtime` only:

- `USAGE` on schema `verifoxx`;
- `SELECT, INSERT` on all nine tables;
- sequence `USAGE, SELECT`; and
- `UPDATE(active_version_id)` on `policies`.

Revoke ledger access and all schema creation from runtime. Revoke public
default privileges for future migrator-owned objects, but do not grant runtime
access through defaults: every later migration must grant each new table and
sequence explicitly.

**Step 4: Write the down migration**

`000001_initial.down.sql` must contain only the bounded reversal:

```sql
DROP SCHEMA IF EXISTS verifoxx CASCADE;
```

It must not drop roles, the database, or the public migration ledger.

**Step 5: Run schema tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/postgres
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/initial_schema$' ./internal/adapters/postgres
```

Expected: PASS.

### Task 5: Enforce Integrity, Immutability, And Runtime Privileges

**Files:**
- Modify: `internal/adapters/postgres/migrations_integration_test.go`
- Modify if a test exposes a defect: `migrations/000001_initial.up.sql`

**Step 1: Write failing integrity and privilege subtests**

Apply the real migration, seed the smallest valid policy/version,
request/evidence snapshot, run, finding, and evidence link, then assert:

- the runtime role can insert/select application rows and update only a
  policy's `active_version_id`;
- a cross-policy active-version update fails the composite FK;
- runtime DDL, ledger reads, policy-name updates, and every immutable
  update/delete fail;
- immutable update/delete also fail through the migrator owner because of the
  trigger;
- invalid hash lengths, non-object metadata, non-array result collections,
  negative counts/indexes, incoherent timestamps, empty required text, and an
  unknown decision all fail; and
- valid `Approve`, `Reject`, `Revise`, and `Escalate` values all succeed in
  separate findings.

Use SQLSTATE or `pgconn.PgError` class/code checks rather than complete server
messages. Do not log role URLs or passwords on failure.

**Step 2: Run the subtests and verify the red state**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/(integrity|privileges)$' ./internal/adapters/postgres
```

Expected: at least one assertion fails until every constraint, trigger, and
grant is complete.

**Step 3: Make the migration satisfy the contract**

Change only `000001_initial.up.sql`. Keep domain integrity in PostgreSQL rather
than duplicating it in the migrator. Do not grant ownership, DDL, blanket
`UPDATE`, `DELETE`, `TRUNCATE`, or migration-ledger access to runtime.

**Step 4: Run the integration subtests**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/(integrity|privileges)$' ./internal/adapters/postgres
```

Expected: PASS.

### Task 6: Prove Drift Rejection, Serialization, And Rollback

**Files:**
- Modify: `internal/adapters/postgres/migrations_integration_test.go`
- Modify if a test exposes a defect: `internal/adapters/postgres/migrate.go`

**Step 1: Write failing failure-mode subtests**

Add sequential scenarios with a clean schema/ledger before each:

- **Drift:** apply one fixture migration, construct a second migrator whose up
  or down bytes differ, and require `errors.Is(err, ErrMigrationDrift)` before
  any pending SQL runs.
- **Rollback:** use two migrations where the second creates an object and then
  executes invalid SQL. Require `Up` to fail with neither migration objects nor
  ledger table/rows remaining, proving ledger creation and all migrations were
  one transaction.
- **Serialization:** create two separate pgx pools and migrators over the same
  source, release two goroutines from one start channel, and require result
  counts `{0, 1}`, one schema object, and one ledger row without duplicate or
  deadlock errors.
- **Cancellation:** hold the advisory lock in one transaction, call `Up` with a
  short context through another pool, require `context.DeadlineExceeded`, then
  release the holder and prove a fresh `Up` succeeds so no pooled lock leaked.
- **Standalone role boundary:** remove `verifoxx_runtime`, then prove a generic
  fixture migration still succeeds and creates a migration-only ledger.

Bound every goroutine result receive with the parent context; do not use sleep
as synchronization except a short `pg_sleep` inside fixture SQL if needed to
widen the serialization window.

**Step 2: Run the subtests and verify failure where behavior is incomplete**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/(drift|rollback|serialization|cancellation)$' ./internal/adapters/postgres
```

Expected: FAIL until reconciliation, transaction cleanup, and lock handling
meet the contract.

**Step 3: Make the minimal migrator corrections**

Reconcile all applied rows before executing migrations, compare checksums with
`subtle.ConstantTimeCompare`, and preserve `%w` wrapping for context and pgx
errors. Keep one transaction per complete operation; do not replace the
transaction-scoped lock with a session lock or a process mutex.

**Step 4: Run all PostgreSQL tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/postgres
timeout 360s go test -count=1 -timeout 300s -tags=integration ./internal/adapters/postgres
```

Expected: PASS.

### Task 7: Complete Task 27 Verification Gates

**Files:**
- Modify only files identified by gate failures.

**Step 1: Normalize dependencies and formatting**

```bash
timeout 120s go mod tidy
timeout 120s go mod verify
test -z "$(gofmt -l .)"
git diff --check
```

Expected: modules verify and no files or whitespace diagnostics are printed.

**Step 2: Validate and boot the Compose environment**

```bash
timeout 120s docker compose --env-file .env.example config --quiet
timeout 240s bash -c 'set -eu; cleanup() { docker compose --env-file .env.example down --volumes; }; trap cleanup EXIT; docker compose --env-file .env.example up --detach --wait; docker compose --env-file .env.example exec -T postgres pg_isready -U postgres -d verifoxx'
```

Expected: valid configuration, healthy PostgreSQL, successful readiness, and
container plus named-volume cleanup.

**Step 3: Run unit, portability, and race gates**

```bash
timeout 180s go test -count=1 -timeout 120s ./...
timeout 180s go test -count=1 -timeout 120s -tags=purego ./...
timeout 180s env GOARCH=386 go test -count=1 -timeout 120s ./...
timeout 240s go test -count=1 -timeout 180s -race -gcflags=all=-d=checkptr=2 ./...
```

Expected: PASS without starting Docker because integration files are excluded.

**Step 4: Run fresh database integration gates**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration ./internal/adapters/postgres
timeout 480s go test -count=1 -timeout 420s -race -tags=integration ./internal/adapters/postgres
```

Expected: PASS against PostgreSQL major version 19 with containers cleaned up.

**Step 5: Run static and layout gates**

```bash
timeout 120s go vet ./...
timeout 120s env GOARCH=386 go vet ./...
timeout 180s go vet -tags=integration ./internal/adapters/postgres
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/adapters/postgres
```

Expected: no diagnostics. Review any field-alignment suggestion before changing
access-locality-oriented order.

**Step 6: Recheck the shipped CLI contract and build**

```bash
timeout 120s go build -trimpath -o /tmp/opencode/verifoxx-task27 ./cmd/verifoxx
timeout 120s go run ./cmd/verifoxx evaluate > /tmp/opencode/task27-results.json
cmp /tmp/opencode/task27-results.json results/requests.json
```

Expected: build succeeds and the default machine-readable output remains
byte-identical.

**Step 7: Optional commit checkpoint**

If the user explicitly requests a commit:

```bash
git add .gitignore .env.example compose.yaml docker/postgres/init-roles.sh migrations/000001_initial.up.sql migrations/000001_initial.down.sql internal/adapters/postgres/migrate.go internal/adapters/postgres/migrate_test.go internal/adapters/postgres/migrations_integration_test.go go.mod go.sum docs/plans/2026-08-23-postgresql-migrations-design.md docs/plans/2026-08-23-postgresql-migrations.md
git commit -m "feat: add postgres schema and migrations"
```
