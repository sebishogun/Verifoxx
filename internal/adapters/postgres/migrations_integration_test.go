//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

const (
	postgresImage     = "postgres:19beta3"
	adminPassword     = "test-admin-password"
	migrationPassword = "test-migration-password"
	runtimePassword   = "test-runtime-password"
)

type postgresTestEnvironment struct {
	admin    *pgxpool.Pool
	migrator *pgxpool.Pool
	runtime  *pgxpool.Pool
	adminURL string
	root     string
}

func TestPostgreSQLMigrations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	initRolesPath := filepath.Join(root, "docker", "postgres", "init-roles.sh")

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("verifoxx"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(adminPassword),
		tcpostgres.WithInitScripts(initRolesPath),
		testcontainers.WithEnv(map[string]string{
			"VERIFOXX_MIGRATION_PASSWORD": migrationPassword,
			"VERIFOXX_RUNTIME_PASSWORD":   runtimePassword,
		}),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start PostgreSQL 19: %v", err)
	}

	adminURL, err := container.ConnectionString(ctx, "sslmode=disable", "connect_timeout=10")
	if err != nil {
		t.Fatalf("resolve PostgreSQL address: %v", err)
	}
	environment := postgresTestEnvironment{
		admin:    openTestPool(t, ctx, adminURL, "postgres", adminPassword),
		migrator: openTestPool(t, ctx, adminURL, "verifoxx_migrator", migrationPassword),
		runtime:  openTestPool(t, ctx, adminURL, "verifoxx_runtime", runtimePassword),
		adminURL: adminURL,
		root:     root,
	}

	t.Run("bootstrap", func(t *testing.T) {
		testBootstrapRoles(t, ctx, &environment)
	})
	t.Run("up_down", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigrationUpDown(t, ctx, environment.migrator)
	})
	t.Run("initial_schema", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testInitialSchema(t, ctx, &environment)
	})
	t.Run("integrity", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testSchemaIntegrity(t, ctx, environment.migrator)
	})
	t.Run("privileges", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testRuntimePrivilegesAndImmutability(t, ctx, &environment)
	})
	t.Run("drift", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigrationDrift(t, ctx, environment.migrator)
	})
	t.Run("rollback", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigrationRollback(t, ctx, environment.migrator)
	})
	t.Run("serialization", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigrationSerialization(t, ctx, &environment)
	})
	t.Run("cancellation", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigrationCancellation(t, ctx, &environment)
	})
	t.Run("policy_store_publish", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyStorePublish(t, ctx, &environment)
	})
	t.Run("policy_store_load", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyStoreLoad(t, ctx, &environment)
	})
	t.Run("policy_publish_reload", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyPublishReload(t, ctx, &environment)
	})
	t.Run("policy_concurrent_publish", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyConcurrentPublish(t, ctx, &environment)
	})
	t.Run("policy_notifications", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyNotifications(t, ctx, &environment)
	})
	t.Run("policy_notification_startup", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyNotificationStartupCatchup(t, ctx, &environment)
	})
	t.Run("decision_audit_journal", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testDecisionAuditJournal(t, ctx, &environment)
	})
	t.Run("decision_history", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testDecisionHistoryStoreIntegration(t, ctx, &environment)
	})
	t.Run("policy_graph_schema", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyGraphSchema(t, ctx, &environment)
	})
	t.Run("policy_graph_projection", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyGraphProjection(t, ctx, &environment)
	})
	t.Run("policy_graph_corrupt_claim", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyGraphRejectsCorruptExistingClaim(t, ctx, &environment)
	})
	t.Run("policy_graph_pgq", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		applySchemaMigrations(t, ctx, &environment)
		testPolicyGraphPGQ(t, ctx, &environment)
	})
	t.Run("policy_graph_down", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testPolicyGraphMigrationDown(t, ctx, &environment)
	})
	t.Run("standalone_migrator", func(t *testing.T) {
		resetDatabase(t, ctx, environment.migrator)
		testMigratorWithoutRuntimeRole(t, ctx, &environment)
	})
}

func testDecisionHistoryStoreIntegration(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
) {
	t.Helper()
	policyStore, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	version, err := policyStore.PublishActive(ctx, policyCandidate(
		"history-policy",
		"1.0.0",
		"history-compiler",
		[]byte(`{"name":"history-policy","version":"1.0.0"}`),
	))
	if err != nil {
		t.Fatalf("publish history policy: %v", err)
	}
	auditStore, err := NewAuditStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct audit store: %v", err)
	}
	batch := testWriterBatch()
	batch.PolicyVersionID = version.ID
	if err := auditStore.Append(ctx, &batch); err != nil {
		t.Fatalf("append first history decision: %v", err)
	}
	setAuditKey(t, &batch, "audit-2")
	batch.StartedAt = batch.StartedAt.Add(time.Hour)
	batch.CompletedAt = batch.CompletedAt.Add(time.Hour)
	batch.Findings.Decisions[0] = persistence.DecisionReject
	if err := auditStore.Append(ctx, &batch); err != nil {
		t.Fatalf("append second history decision: %v", err)
	}

	history, err := NewDecisionHistoryStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct decision history store: %v", err)
	}
	entries, err := history.Load(ctx, "R1", nil)
	if err != nil {
		t.Fatalf("load decision history: %v", err)
	}
	if len(entries) != 2 || entries[0].Decision != "Reject" || entries[1].Decision != "Approve" ||
		entries[0].Policy != "history-policy" || entries[0].Version != "1.0.0" ||
		!entries[0].CompletedAt.After(entries[1].CompletedAt) {
		t.Fatalf("decision history = %+v", entries)
	}
	entries, err = history.Load(ctx, "missing-request", entries[:0])
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing decision history = (%+v, %v)", entries, err)
	}
}

func openTestPool(t *testing.T, ctx context.Context, adminURL, role, password string) *pgxpool.Pool {
	t.Helper()

	connectionURL, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse container address: %v", err)
	}
	connectionURL.User = url.UserPassword(role, password)

	config, err := pgxpool.ParseConfig(connectionURL.String())
	if err != nil {
		t.Fatalf("configure %s pool: %v", role, err)
	}
	config.MaxConns = 2
	config.MinConns = 0

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create %s pool: %v", role, err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect %s pool: %v", role, err)
	}
	return pool
}

func testBootstrapRoles(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	var major int
	if err := environment.admin.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::integer / 10000",
	).Scan(&major); err != nil {
		t.Fatalf("query PostgreSQL version: %v", err)
	}
	if major != 19 {
		t.Fatalf("PostgreSQL major version = %d, want 19", major)
	}

	assertCurrentRole(t, ctx, environment.migrator, "verifoxx_migrator")
	assertCurrentRole(t, ctx, environment.runtime, "verifoxx_runtime")

	if _, err := environment.runtime.Exec(ctx, "CREATE SCHEMA runtime_must_not_create"); err == nil {
		_, _ = environment.admin.Exec(ctx, "DROP SCHEMA runtime_must_not_create CASCADE")
		t.Fatal("runtime CREATE SCHEMA succeeded")
	}
	if _, err := environment.migrator.Exec(ctx, "CREATE SCHEMA migrator_bootstrap_probe"); err != nil {
		t.Fatalf("migrator CREATE SCHEMA: %v", err)
	}
	if _, err := environment.migrator.Exec(ctx, "DROP SCHEMA migrator_bootstrap_probe CASCADE"); err != nil {
		t.Fatalf("migrator DROP SCHEMA: %v", err)
	}
}

func assertCurrentRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want string) {
	t.Helper()

	var got string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&got); err != nil {
		t.Fatalf("query current role: %v", err)
	}
	if got != want {
		t.Fatalf("current role = %q, want %q", got, want)
	}
}

func testMigrationUpDown(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	upSQL := []byte("CREATE SCHEMA migration_probe; CREATE TABLE migration_probe.items (id bigint PRIMARY KEY);")
	downSQL := []byte("DROP SCHEMA migration_probe CASCADE;")
	source := fstest.MapFS{
		"000001_probe.up.sql":   {Data: upSQL},
		"000001_probe.down.sql": {Data: downSQL},
	}
	migrator, err := NewMigrator(pool, source)
	if err != nil {
		t.Fatalf("construct migrator: %v", err)
	}

	changed, err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if changed != 1 {
		t.Fatalf("up count = %d, want 1", changed)
	}
	assertRelationExists(t, ctx, pool, "migration_probe.items", true)

	var (
		checksum []byte
		applied  time.Time
		name     string
		version  int32
	)
	if err := pool.QueryRow(ctx, `
		SELECT version, name, checksum, applied_at
		FROM public.verifoxx_schema_migrations
	`).Scan(&version, &name, &checksum, &applied); err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	wantChecksum := migrationChecksum(upSQL, downSQL)
	if version != 1 || name != "probe" || !slices.Equal(checksum, wantChecksum[:]) || applied.IsZero() {
		t.Fatalf("ledger row = version:%d name:%q checksum:%x applied:%v", version, name, checksum, applied)
	}

	changed, err = migrator.Up(ctx)
	if err != nil {
		t.Fatalf("repeat migrate up: %v", err)
	}
	if changed != 0 {
		t.Fatalf("repeat up count = %d, want 0", changed)
	}

	changed, err = migrator.Down(ctx, 1)
	if err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if changed != 1 {
		t.Fatalf("down count = %d, want 1", changed)
	}
	assertRelationExists(t, ctx, pool, "migration_probe.items", false)
	if got := queryCount(t, ctx, pool, "SELECT count(*) FROM public.verifoxx_schema_migrations"); got != 0 {
		t.Fatalf("ledger count after down = %d, want 0", got)
	}

	if _, err := migrator.Down(ctx, 1); !errors.Is(err, ErrInvalidDownCount) {
		t.Fatalf("repeat down error = %v, want ErrInvalidDownCount", err)
	}
	if changed, err = migrator.Up(ctx); err != nil || changed != 1 {
		t.Fatalf("re-up = (%d, %v), want (1, nil)", changed, err)
	}
}

func assertRelationExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, relation string, want bool) {
	t.Helper()

	var got bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", relation).Scan(&got); err != nil {
		t.Fatalf("query relation %q: %v", relation, err)
	}
	if got != want {
		t.Fatalf("relation %q exists = %t, want %t", relation, got, want)
	}
}

func queryCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	return count
}

func testInitialSchema(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	migrator, err := NewMigrator(environment.migrator, os.DirFS(filepath.Join(environment.root, "migrations")))
	if err != nil {
		t.Fatalf("construct schema migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 2 {
		t.Fatalf("schema migrate up = (%d, %v), want (2, nil)", changed, err)
	}

	wantTables := []string{
		"benchmark_runs",
		"debug_traces",
		"evaluation_evidence",
		"evaluation_findings",
		"evaluation_runs",
		"evidence_snapshots",
		"policies",
		"policy_edges",
		"policy_nodes",
		"policy_versions",
		"requests",
	}
	gotTables := queryStrings(t, ctx, environment.migrator, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'verifoxx' AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`)
	if !slices.Equal(gotTables, wantTables) {
		t.Fatalf("application tables = %v, want %v", gotTables, wantTables)
	}

	var owner string
	if err := environment.migrator.QueryRow(ctx, `
		SELECT pg_get_userbyid(nspowner)
		FROM pg_namespace
		WHERE nspname = 'verifoxx'
	`).Scan(&owner); err != nil {
		t.Fatalf("query schema owner: %v", err)
	}
	if owner != "verifoxx_migrator" {
		t.Fatalf("schema owner = %q, want verifoxx_migrator", owner)
	}

	wantIndexes := []string{
		"benchmark_runs_policy_version_idx",
		"debug_traces_evaluation_run_idx",
		"debug_traces_policy_version_idx",
		"evaluation_evidence_snapshot_idx",
		"evaluation_findings_request_idx",
		"evaluation_runs_policy_version_idx",
		"policies_active_version_idx",
		"policy_edges_source_idx",
		"policy_edges_target_idx",
		"policy_versions_policy_idx",
	}
	for _, index := range wantIndexes {
		assertRelationExists(t, ctx, environment.migrator, "verifoxx."+index, true)
	}

	wantConstraints := []string{
		"evaluation_runs_idempotency_key_key",
		"evidence_snapshots_evidence_key_hash_key",
		"policies_active_version_fkey",
		"policies_name_key",
		"policy_versions_content_hash_key",
		"policy_versions_policy_id_id_key",
		"policy_versions_policy_semantic_version_key",
		"requests_request_key_hash_key",
	}
	gotConstraints := queryStrings(t, ctx, environment.migrator, `
		SELECT conname
		FROM pg_constraint
		WHERE connamespace = 'verifoxx'::regnamespace
		  AND contype IN ('f', 'u')
		  AND conname = ANY($1::text[])
		ORDER BY conname
	`, wantConstraints)
	if !slices.Equal(gotConstraints, wantConstraints) {
		t.Fatalf("selected constraints = %v, want %v", gotConstraints, wantConstraints)
	}

	assertColumnType(t, ctx, environment.migrator, "policy_versions", "source", "bytea")
	assertColumnType(t, ctx, environment.migrator, "policy_versions", "content_hash", "bytea")
	assertColumnType(t, ctx, environment.migrator, "requests", "payload", "jsonb")
	assertColumnType(t, ctx, environment.migrator, "evidence_snapshots", "payload", "jsonb")
	assertColumnType(t, ctx, environment.migrator, "evaluation_runs", "execution_metadata", "jsonb")
	assertColumnType(t, ctx, environment.migrator, "evaluation_findings", "remediation", "jsonb")
	assertColumnType(t, ctx, environment.migrator, "debug_traces", "payload", "bytea")
	assertColumnType(t, ctx, environment.migrator, "benchmark_runs", "measurements", "jsonb")

	var activeVersionDefinition string
	if err := environment.migrator.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE connamespace = 'verifoxx'::regnamespace
		  AND conname = 'policies_active_version_fkey'
	`).Scan(&activeVersionDefinition); err != nil {
		t.Fatalf("query active-version constraint: %v", err)
	}
	for _, fragment := range []string{
		"FOREIGN KEY (id, active_version_id)",
		"REFERENCES verifoxx.policy_versions(policy_id, id)",
		"DEFERRABLE INITIALLY DEFERRED",
	} {
		if !strings.Contains(activeVersionDefinition, fragment) {
			t.Fatalf("active-version constraint %q lacks %q", activeVersionDefinition, fragment)
		}
	}

	assertRelationExists(t, ctx, environment.migrator, "verifoxx.policy_nodes", true)
	assertRelationExists(t, ctx, environment.migrator, "verifoxx.policy_edges", true)
}

func queryStrings(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) []string {
	t.Helper()

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		t.Fatalf("query strings: %v", err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan string: %v", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate strings: %v", err)
	}
	return values
}

func assertColumnType(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column, want string) {
	t.Helper()

	var got string
	if err := pool.QueryRow(ctx, `
		SELECT format_type(attribute.atttypid, attribute.atttypmod)
		FROM pg_attribute AS attribute
		JOIN pg_class AS relation ON relation.oid = attribute.attrelid
		JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
		WHERE namespace.nspname = 'verifoxx'
		  AND relation.relname = $1
		  AND attribute.attname = $2
		  AND attribute.attnum > 0
		  AND NOT attribute.attisdropped
	`, table, column).Scan(&got); err != nil {
		t.Fatalf("query type for %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("type for %s.%s = %q, want %q", table, column, got, want)
	}
}

type seededAuditRows struct {
	policyID        int64
	policyVersionID int64
	requestID       int64
	evidenceID      int64
	evaluationRunID int64
}

func applySchemaMigrations(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	migrator, err := NewMigrator(environment.migrator, os.DirFS(filepath.Join(environment.root, "migrations")))
	if err != nil {
		t.Fatalf("construct initial migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 2 {
		t.Fatalf("apply schema migrations = (%d, %v), want (2, nil)", changed, err)
	}
}

func testSchemaIntegrity(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	seed := seedAuditBase(t, ctx, pool, 4)
	assertSQLState(t, execError(ctx, pool,
		"INSERT INTO verifoxx.policies (name) VALUES ('   ')",
	), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.policy_versions
		    (policy_id, semantic_version, source, content_hash, compiler_version)
		VALUES ($1, 'bad-hash', 'x', $2, 'test')
	`, seed.policyID, bytes.Repeat([]byte{9}, 31)), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.requests (request_key, content_hash, payload)
		VALUES ('bad-json-shape', $1, '[]'::jsonb)
	`, bytes.Repeat([]byte{10}, 32)), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.evidence_snapshots
		    (evidence_key, content_hash, payload, captured_at, expires_at)
		VALUES ('bad-expiry', $1, '{}'::jsonb, clock_timestamp(), clock_timestamp() - interval '1 second')
	`, bytes.Repeat([]byte{11}, 32)), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.evaluation_runs
		    (idempotency_key, policy_version_id, engine_version, started_at, completed_at, row_count, execution_metadata)
		VALUES ('negative-count', $1, 'test', clock_timestamp(), clock_timestamp(), -1, '{}'::jsonb)
	`, seed.policyVersionID), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.evaluation_runs
		    (idempotency_key, policy_version_id, engine_version, started_at, completed_at, row_count, execution_metadata)
		VALUES ('bad-time', $1, 'test', clock_timestamp(), clock_timestamp() - interval '1 second', 0, '{}'::jsonb)
	`, seed.policyVersionID), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.evaluation_runs
		    (idempotency_key, policy_version_id, engine_version, started_at, completed_at, row_count, execution_metadata)
		VALUES ('bad-metadata', $1, 'test', clock_timestamp(), clock_timestamp(), 0, '[]'::jsonb)
	`, seed.policyVersionID), "23514")
	assertSQLState(t, insertFinding(ctx, pool, seed.evaluationRunID, -1, seed.requestID, "Approve", "[]"), "23514")
	assertSQLState(t, insertFinding(ctx, pool, seed.evaluationRunID, 10, seed.requestID, "Unknown", "[]"), "23514")
	assertSQLState(t, insertFinding(ctx, pool, seed.evaluationRunID, 11, seed.requestID, "Approve", "{}"), "23514")
	assertSQLState(t, execError(ctx, pool, `
		INSERT INTO verifoxx.debug_traces
		    (policy_version_id, format, payload, content_hash, created_at, expires_at)
		VALUES ($1, 'binary', 'x', $2, clock_timestamp(), clock_timestamp() - interval '1 second')
	`, seed.policyVersionID, bytes.Repeat([]byte{12}, 32)), "23514")

	for row, decision := range []string{"Approve", "Reject", "Revise", "Escalate"} {
		if err := insertFinding(ctx, pool, seed.evaluationRunID, int64(row), seed.requestID, decision, "[]"); err != nil {
			t.Fatalf("insert valid %s finding: %v", decision, err)
		}
	}
	if got := queryCount(t, ctx, pool,
		"SELECT count(*) FROM verifoxx.evaluation_findings WHERE run_id = $1",
		seed.evaluationRunID,
	); got != 4 {
		t.Fatalf("valid decision count = %d, want 4", got)
	}
}

func testRuntimePrivilegesAndImmutability(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	seed := seedCompleteAudit(t, ctx, environment.runtime)
	for _, table := range []string{
		"policies",
		"policy_versions",
		"requests",
		"evidence_snapshots",
		"evaluation_runs",
		"evaluation_findings",
		"evaluation_evidence",
		"debug_traces",
		"benchmark_runs",
	} {
		if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx."+table); got == 0 {
			t.Fatalf("runtime sees no rows in %s", table)
		}
	}

	if _, err := environment.runtime.Exec(ctx,
		"UPDATE verifoxx.policies SET active_version_id = $1 WHERE id = $2",
		seed.policyVersionID, seed.policyID,
	); err != nil {
		t.Fatalf("runtime update active version: %v", err)
	}

	var otherPolicyID, otherVersionID int64
	if err := environment.runtime.QueryRow(ctx,
		"INSERT INTO verifoxx.policies (name) VALUES ('other-policy') RETURNING id",
	).Scan(&otherPolicyID); err != nil {
		t.Fatalf("insert second policy: %v", err)
	}
	if err := environment.runtime.QueryRow(ctx, `
		INSERT INTO verifoxx.policy_versions
		    (policy_id, semantic_version, source, content_hash, compiler_version)
		VALUES ($1, '1.0.0', 'other-source', $2, 'test')
		RETURNING id
	`, otherPolicyID, bytes.Repeat([]byte{21}, 32)).Scan(&otherVersionID); err != nil {
		t.Fatalf("insert second policy version: %v", err)
	}
	assertSQLState(t, execError(ctx, environment.runtime,
		"UPDATE verifoxx.policies SET active_version_id = $1 WHERE id = $2",
		otherVersionID, seed.policyID,
	), "23503")

	assertSQLState(t, execError(ctx, environment.runtime,
		"CREATE TABLE verifoxx.runtime_must_not_create (id integer)",
	), "42501")
	assertSQLState(t, execError(ctx, environment.runtime,
		"SELECT count(*) FROM public.verifoxx_schema_migrations",
	), "42501")
	if _, err := environment.migrator.Exec(ctx,
		"GRANT SELECT ON public.verifoxx_schema_migrations TO verifoxx_runtime",
	); err != nil {
		t.Fatalf("grant temporary runtime ledger access: %v", err)
	}
	migrator, err := NewMigrator(
		environment.migrator,
		os.DirFS(filepath.Join(environment.root, "migrations")),
	)
	if err != nil {
		t.Fatalf("construct privilege-repair migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 0 {
		t.Fatalf("idempotent privilege repair = (%d, %v), want (0, nil)", changed, err)
	}
	assertSQLState(t, execError(ctx, environment.runtime,
		"SELECT count(*) FROM public.verifoxx_schema_migrations",
	), "42501")
	assertSQLState(t, execError(ctx, environment.runtime,
		"UPDATE verifoxx.policies SET name = 'renamed' WHERE id = $1", seed.policyID,
	), "42501")

	immutableStatements := []string{
		"UPDATE verifoxx.policy_versions SET compiler_version = compiler_version",
		"DELETE FROM verifoxx.policy_versions",
		"UPDATE verifoxx.requests SET payload = payload",
		"DELETE FROM verifoxx.requests",
		"UPDATE verifoxx.evidence_snapshots SET payload = payload",
		"DELETE FROM verifoxx.evidence_snapshots",
		"UPDATE verifoxx.evaluation_runs SET engine_version = engine_version",
		"DELETE FROM verifoxx.evaluation_runs",
		"UPDATE verifoxx.evaluation_findings SET rationale = rationale",
		"DELETE FROM verifoxx.evaluation_findings",
		"UPDATE verifoxx.evaluation_evidence SET evidence_snapshot_id = evidence_snapshot_id",
		"DELETE FROM verifoxx.evaluation_evidence",
	}
	for _, statement := range immutableStatements {
		assertSQLState(t, execError(ctx, environment.runtime, statement), "42501")
		assertSQLState(t, execError(ctx, environment.migrator, statement), "55000")
	}

	assertSQLState(t, execError(ctx, environment.migrator,
		"UPDATE verifoxx.policies SET name = 'renamed' WHERE id = $1", seed.policyID,
	), "55000")
	assertSQLState(t, execError(ctx, environment.migrator,
		"DELETE FROM verifoxx.policies WHERE id = $1", seed.policyID,
	), "55000")
	if _, err := environment.migrator.Exec(ctx, "DELETE FROM verifoxx.debug_traces"); err != nil {
		t.Fatalf("migrator prune debug traces: %v", err)
	}
	if _, err := environment.migrator.Exec(ctx, "DELETE FROM verifoxx.benchmark_runs"); err != nil {
		t.Fatalf("migrator prune benchmark runs: %v", err)
	}
}

func seedAuditBase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rowCount int64) seededAuditRows {
	t.Helper()

	var seed seededAuditRows
	if err := pool.QueryRow(ctx,
		"INSERT INTO verifoxx.policies (name) VALUES ('policy') RETURNING id",
	).Scan(&seed.policyID); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO verifoxx.policy_versions
		    (policy_id, semantic_version, source, content_hash, compiler_version)
		VALUES ($1, '1.0.0', 'policy-source', $2, 'test')
		RETURNING id
	`, seed.policyID, bytes.Repeat([]byte{1}, 32)).Scan(&seed.policyVersionID); err != nil {
		t.Fatalf("seed policy version: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO verifoxx.requests (request_key, content_hash, payload)
		VALUES ('request-1', $1, '{"request":"R1"}'::jsonb)
		RETURNING id
	`, bytes.Repeat([]byte{2}, 32)).Scan(&seed.requestID); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO verifoxx.evidence_snapshots (evidence_key, content_hash, payload, expires_at)
		VALUES ('evidence-1', $1, '{"state":"valid"}'::jsonb, clock_timestamp() + interval '1 hour')
		RETURNING id
	`, bytes.Repeat([]byte{3}, 32)).Scan(&seed.evidenceID); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO verifoxx.evaluation_runs
		    (idempotency_key, policy_version_id, engine_version, started_at, completed_at, row_count, execution_metadata)
		VALUES ('run-1', $1, 'test', clock_timestamp() - interval '1 second', clock_timestamp(), $2, '{}'::jsonb)
		RETURNING id
	`, seed.policyVersionID, rowCount).Scan(&seed.evaluationRunID); err != nil {
		t.Fatalf("seed evaluation run: %v", err)
	}
	return seed
}

func seedCompleteAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool) seededAuditRows {
	t.Helper()

	seed := seedAuditBase(t, ctx, pool, 1)
	if err := insertFinding(ctx, pool, seed.evaluationRunID, 0, seed.requestID, "Approve", "[]"); err != nil {
		t.Fatalf("seed finding: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verifoxx.evaluation_evidence
		    (run_id, row_index, evidence_ordinal, evidence_snapshot_id)
		VALUES ($1, 0, 0, $2)
	`, seed.evaluationRunID, seed.evidenceID); err != nil {
		t.Fatalf("seed finding evidence: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verifoxx.debug_traces
		    (policy_version_id, evaluation_run_id, format, payload, content_hash, expires_at)
		VALUES ($1, $2, 'binary', 'trace', $3, clock_timestamp() + interval '1 hour')
	`, seed.policyVersionID, seed.evaluationRunID, bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatalf("seed debug trace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO verifoxx.benchmark_runs
		    (policy_version_id, engine_version, environment, parameters, measurements)
		VALUES ($1, 'test', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb)
	`, seed.policyVersionID); err != nil {
		t.Fatalf("seed benchmark run: %v", err)
	}
	return seed
}

func insertFinding(
	ctx context.Context,
	pool *pgxpool.Pool,
	runID, rowIndex, requestID int64,
	decision, appliedRequirements string,
) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO verifoxx.evaluation_findings
		    (run_id, row_index, request_id, decision, rationale,
		     applied_requirements, missing_or_conflicting_evidence,
		     assumptions, unresolved_uncertainty, remediation)
		VALUES ($1, $2, $3, $4, 'rationale', $5::jsonb, '[]'::jsonb, '[]'::jsonb, '[]'::jsonb, '[]'::jsonb)
	`, runID, rowIndex, requestID, decision, appliedRequirements)
	return err
}

func execError(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) error {
	_, err := pool.Exec(ctx, query, args...)
	return err
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("SQL succeeded, want SQLSTATE %s", want)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("error type = %T, want *pgconn.PgError", err)
	}
	if postgresError.Code != want {
		t.Fatalf("SQLSTATE = %s, want %s (%v)", postgresError.Code, want, err)
	}
}

func testMigrationDrift(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	original := fstest.MapFS{
		"000001_probe.up.sql":   {Data: []byte("CREATE SCHEMA migration_probe;")},
		"000001_probe.down.sql": {Data: []byte("DROP SCHEMA migration_probe CASCADE;")},
	}
	migrator, err := NewMigrator(pool, original)
	if err != nil {
		t.Fatalf("construct original migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 1 {
		t.Fatalf("apply original migration = (%d, %v), want (1, nil)", changed, err)
	}

	drifted := fstest.MapFS{
		"000001_probe.up.sql":     {Data: []byte("CREATE SCHEMA migration_probe; SELECT 1;")},
		"000001_probe.down.sql":   {Data: []byte("DROP SCHEMA migration_probe CASCADE;")},
		"000002_pending.up.sql":   {Data: []byte("CREATE TABLE migration_probe.pending (id integer);")},
		"000002_pending.down.sql": {Data: []byte("DROP TABLE migration_probe.pending;")},
	}
	migrator, err = NewMigrator(pool, drifted)
	if err != nil {
		t.Fatalf("construct drifted migrator: %v", err)
	}
	if _, err := migrator.Up(ctx); !errors.Is(err, ErrMigrationDrift) {
		t.Fatalf("drift error = %v, want ErrMigrationDrift", err)
	}
	assertRelationExists(t, ctx, pool, "migration_probe.pending", false)
	if got := queryCount(t, ctx, pool, "SELECT count(*) FROM public.verifoxx_schema_migrations"); got != 1 {
		t.Fatalf("ledger count after drift = %d, want 1", got)
	}
}

func testMigrationRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	source := fstest.MapFS{
		"000001_first.up.sql": {
			Data: []byte("CREATE SCHEMA migration_probe; CREATE TABLE migration_probe.first (id integer);"),
		},
		"000001_first.down.sql": {Data: []byte("DROP TABLE migration_probe.first;")},
		"000002_broken.up.sql": {
			Data: []byte("CREATE TABLE migration_probe.second (id integer); SELECT migration_function_that_does_not_exist();"),
		},
		"000002_broken.down.sql": {Data: []byte("DROP TABLE migration_probe.second;")},
	}
	migrator, err := NewMigrator(pool, source)
	if err != nil {
		t.Fatalf("construct rollback migrator: %v", err)
	}
	if _, err := migrator.Up(ctx); err == nil {
		t.Fatal("broken migration succeeded")
	} else {
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) {
			t.Fatalf("broken migration error type = %T, want wrapped *pgconn.PgError", err)
		}
	}

	assertRelationExists(t, ctx, pool, "migration_probe.first", false)
	assertRelationExists(t, ctx, pool, "migration_probe.second", false)
	assertRelationExists(t, ctx, pool, "public.verifoxx_schema_migrations", false)
}

type migrationRunResult struct {
	err   error
	count int
}

func testMigrationSerialization(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	secondPool := openTestPool(t, ctx, environment.adminURL, "verifoxx_migrator", migrationPassword)
	source := fstest.MapFS{
		"000001_probe.up.sql": {
			Data: []byte("CREATE SCHEMA migration_probe; SELECT pg_sleep(0.25); CREATE TABLE migration_probe.items (id integer);"),
		},
		"000001_probe.down.sql": {Data: []byte("DROP SCHEMA migration_probe CASCADE;")},
	}
	first, err := NewMigrator(environment.migrator, source)
	if err != nil {
		t.Fatalf("construct first concurrent migrator: %v", err)
	}
	second, err := NewMigrator(secondPool, source)
	if err != nil {
		t.Fatalf("construct second concurrent migrator: %v", err)
	}

	opCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan migrationRunResult, 2)
	run := func(migrator *Migrator) {
		<-start
		count, err := migrator.Up(opCtx)
		results <- migrationRunResult{err: err, count: count}
	}
	go run(first)
	go run(second)
	close(start)

	counts := make([]int, 0, 2)
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("concurrent migration: %v", result.err)
			}
			counts = append(counts, result.count)
		case <-opCtx.Done():
			t.Fatalf("concurrent migrations: %v", opCtx.Err())
		}
	}
	slices.Sort(counts)
	if !slices.Equal(counts, []int{0, 1}) {
		t.Fatalf("concurrent up counts = %v, want [0 1]", counts)
	}
	assertRelationExists(t, ctx, environment.migrator, "migration_probe.items", true)
	if got := queryCount(t, ctx, environment.migrator,
		"SELECT count(*) FROM public.verifoxx_schema_migrations",
	); got != 1 {
		t.Fatalf("serialized ledger count = %d, want 1", got)
	}
}

func testMigrationCancellation(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	holder, err := environment.migrator.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = holder.Rollback(rollbackCtx)
	})
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationAdvisoryLock); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}

	contenderPool := openTestPool(t, ctx, environment.adminURL, "verifoxx_migrator", migrationPassword)
	source := fstest.MapFS{
		"000001_probe.up.sql":   {Data: []byte("CREATE SCHEMA migration_probe; CREATE TABLE migration_probe.items (id integer);")},
		"000001_probe.down.sql": {Data: []byte("DROP SCHEMA migration_probe CASCADE;")},
	}
	migrator, err := NewMigrator(contenderPool, source)
	if err != nil {
		t.Fatalf("construct cancellation migrator: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = migrator.Up(waitCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked migration error = %v, want context deadline", err)
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release migration lock: %v", err)
	}

	retryCtx, retryCancel := context.WithTimeout(ctx, 10*time.Second)
	defer retryCancel()
	if changed, err := migrator.Up(retryCtx); err != nil || changed != 1 {
		t.Fatalf("migration after cancellation = (%d, %v), want (1, nil)", changed, err)
	}
}

func testMigratorWithoutRuntimeRole(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	environment.runtime.Close()
	if _, err := environment.admin.Exec(ctx, "DROP OWNED BY verifoxx_runtime"); err != nil {
		t.Fatalf("drop runtime grants: %v", err)
	}
	if _, err := environment.admin.Exec(ctx, "DROP ROLE verifoxx_runtime"); err != nil {
		t.Fatalf("drop runtime role: %v", err)
	}

	source := fstest.MapFS{
		"000001_probe.up.sql":   {Data: []byte("CREATE SCHEMA migration_probe; CREATE TABLE migration_probe.items (id integer);")},
		"000001_probe.down.sql": {Data: []byte("DROP SCHEMA migration_probe CASCADE;")},
	}
	migrator, err := NewMigrator(environment.migrator, source)
	if err != nil {
		t.Fatalf("construct standalone migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 1 {
		t.Fatalf("standalone migrate up = (%d, %v), want (1, nil)", changed, err)
	}
	assertRelationExists(t, ctx, environment.migrator, "migration_probe.items", true)
	assertRelationExists(t, ctx, environment.migrator, "public.verifoxx_schema_migrations", true)
}

func resetDatabase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	for _, statement := range []string{
		"DROP SCHEMA IF EXISTS verifoxx CASCADE",
		"DROP SCHEMA IF EXISTS migration_probe CASCADE",
		"DROP TABLE IF EXISTS public.verifoxx_schema_migrations",
	} {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("reset database with %q: %v", statement, err)
		}
	}
}
