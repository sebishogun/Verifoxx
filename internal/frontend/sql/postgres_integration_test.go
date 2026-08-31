//go:build integration

package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
	"github.com/sebishogun/nornrune/frontend/sql/postgres"
	"github.com/sebishogun/nornrune/internal/eval"
	corefrontend "github.com/sebishogun/nornrune/internal/frontend"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

const postgresDifferentialImage = "postgres:19beta3"

type expressionFixture struct {
	Name       string  `json:"name"`
	Expression string  `json:"expression"`
	Team       *string `json:"team"`
	Count      *int64  `json:"count"`
	Enabled    *bool   `json:"enabled"`
}

func TestPostgreSQL19Differential(t *testing.T) {
	fixtures := readPostgreSQLExpressionFixtures(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	container, err := tcpostgres.Run(ctx, postgresDifferentialImage,
		tcpostgres.WithDatabase("nornrune_sql"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("sql-differential-password"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL 19: %v", err)
	}
	testcontainers.CleanupContainer(t, container)
	connectionString, err := container.ConnectionString(ctx, "sslmode=disable", "connect_timeout=10")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version string
	if err := pool.QueryRow(ctx, `SHOW server_version_num`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "19") {
		t.Fatalf("PostgreSQL server_version_num = %q, want major 19", version)
	}
	t.Run("expressions", func(t *testing.T) {
		testPostgreSQLExpressions(t, ctx, pool, fixtures)
	})
	t.Run("row_level_security", func(t *testing.T) {
		testPostgreSQLRLS(t, ctx, pool)
	})
}

func readPostgreSQLExpressionFixtures(t *testing.T) []expressionFixture {
	t.Helper()
	encoded, err := os.ReadFile("../../../testdata/frontends/sql/postgres/differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []expressionFixture `json:"cases"`
	}
	if err := json.Unmarshal(encoded, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("PostgreSQL differential corpus is empty")
	}
	return corpus.Cases
}

func testPostgreSQLExpressions(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixtures []expressionFixture) {
	t.Helper()
	sqlSchema := postgresExpressionSchema(t)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			policy, diagnostics := postgres.CompileExpression([]byte(fixture.Expression), sqlSchema, public.DefaultLimits())
			if len(diagnostics) != 0 {
				t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
			}
			compiled, semanticDiagnostics, err := corefrontend.Compile(policy)
			if err != nil || len(semanticDiagnostics) != 0 {
				t.Fatalf("semantic Compile() = diagnostics %#v error %v", semanticDiagnostics, err)
			}
			query := fmt.Sprintf(`SELECT (%s) FROM (VALUES ($1::text, $2::bigint, $3::boolean)) AS input(team, count, enabled)`, fixture.Expression)
			var postgreSQL *bool
			if err := pool.QueryRow(ctx, query, nullableString(fixture.Team), nullableInt64(fixture.Count), nullableBool(fixture.Enabled)).Scan(&postgreSQL); err != nil {
				t.Fatal(err)
			}
			got := evaluateExpression(t, compiled, fixture)
			want := schema.OutcomeID(4)
			if postgreSQL != nil {
				if *postgreSQL {
					want = 1
				} else {
					want = 2
				}
			}
			if got != want {
				t.Fatalf("expression %q = outcome %d, PostgreSQL truth %v wants %d", fixture.Expression, got, postgreSQL, want)
			}
		})
	}
}

func testPostgreSQLRLS(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	policySource := []byte(`
CREATE POLICY select_team ON records FOR SELECT TO nr_sql_analyst USING (team = 'blue');
CREATE POLICY insert_count ON records FOR INSERT TO PUBLIC WITH CHECK (count < 10);
CREATE POLICY update_row ON records FOR UPDATE TO nr_sql_analyst USING (team = 'blue') WITH CHECK (count < 10);
CREATE POLICY delete_team ON records FOR DELETE TO nr_sql_analyst USING (team = 'blue');
CREATE POLICY verified ON records AS RESTRICTIVE FOR ALL TO PUBLIC USING (enabled) WITH CHECK (enabled);
`)
	setup := `
CREATE ROLE nr_sql_owner NOLOGIN;
CREATE ROLE nr_sql_analyst NOLOGIN;
CREATE ROLE nr_sql_outsider NOLOGIN;
CREATE TABLE records (team text, count bigint, enabled boolean);
ALTER TABLE records OWNER TO nr_sql_owner;
GRANT SELECT, INSERT, UPDATE, DELETE ON records TO nr_sql_analyst, nr_sql_outsider;
ALTER TABLE records ENABLE ROW LEVEL SECURITY;
ALTER TABLE records FORCE ROW LEVEL SECURITY;
` + string(policySource) + `
INSERT INTO records(team, count, enabled) VALUES
  ('blue', 5, true), ('red', 5, true), ('blue', 11, true),
  ('blue', 5, false), (NULL, 5, true);
`
	if _, err := pool.Exec(ctx, setup); err != nil {
		t.Fatal(err)
	}
	var compiler Compiler
	var compiled program.Program
	diagnostics, err := compiler.CompileRLS(&compiled, policySource, postgresRLSSchema(t), public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() = diagnostics %#v error %v", diagnostics, err)
	}

	yes := true
	analyst, outsider := "nr_sql_analyst", "nr_sql_outsider"
	selectCommand, insertCommand := "select", "insert"
	updateUsing, updateCheck, deleteCommand := "update_using", "update_check", "delete"
	blue, red, green := "blue", "red", "green"
	count5, count11 := int64(5), int64(11)
	assertRLSCount(t, ctx, pool, analyst, `SELECT count(*) FROM records`, 2)
	assertRLSCount(t, ctx, pool, outsider, `SELECT count(*) FROM records`, 0)
	assertRLSDecision(t, &compiled, rlsActivation{team: &blue, enabled: &yes, command: &selectCommand, role: &analyst}, 1)
	assertRLSDecision(t, &compiled, rlsActivation{team: &red, enabled: &yes, command: &selectCommand, role: &analyst}, 2)

	assertRLSExec(t, ctx, pool, outsider, `INSERT INTO records(team, count, enabled) VALUES ($1, $2, $3)`, []any{green, count5, yes}, true, 1)
	assertRLSDecision(t, &compiled, rlsActivation{team: &green, count: &count5, enabled: &yes, command: &insertCommand, role: &outsider}, 1)
	assertRLSExec(t, ctx, pool, outsider, `INSERT INTO records(team, count, enabled) VALUES ($1, $2, $3)`, []any{green, count11, yes}, false, 0)
	assertRLSDecision(t, &compiled, rlsActivation{team: &green, count: &count11, enabled: &yes, command: &insertCommand, role: &outsider}, 2)

	assertRLSExec(t, ctx, pool, analyst, `UPDATE records SET count = count WHERE team = $1 AND count = 5`, []any{blue}, true, 1)
	assertRLSDecision(t, &compiled, rlsActivation{team: &blue, enabled: &yes, command: &updateUsing, role: &analyst}, 1)
	assertRLSExec(t, ctx, pool, analyst, `UPDATE records SET count = 11 WHERE team = $1 AND count = 5`, []any{blue}, false, 0)
	assertRLSDecision(t, &compiled, rlsActivation{count: &count11, enabled: &yes, command: &updateCheck, role: &analyst}, 2)

	assertRLSExec(t, ctx, pool, analyst, `DELETE FROM records WHERE team = $1 AND count = 5`, []any{blue}, true, 1)
	assertRLSDecision(t, &compiled, rlsActivation{team: &blue, enabled: &yes, command: &deleteCommand, role: &analyst}, 1)
}

func assertRLSCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role, query string, want int64) {
	t.Helper()
	tx := beginRoleTransaction(t, ctx, pool, role)
	defer tx.Rollback(ctx)
	var got int64
	if err := tx.QueryRow(ctx, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", role, got, want)
	}
}

func assertRLSExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role, statement string, arguments []any, accepted bool, wantRows int64) {
	t.Helper()
	tx := beginRoleTransaction(t, ctx, pool, role)
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, statement, arguments...)
	if accepted {
		if err != nil || tag.RowsAffected() != wantRows {
			t.Fatalf("accepted statement = tag %v error %v, want %d rows", tag, err, wantRows)
		}
		return
	}
	var pgError *pgconn.PgError
	if err == nil || !asPostgreSQLError(err, &pgError) || pgError.Code != "42501" {
		t.Fatalf("rejected statement error = %v, want RLS insufficient_privilege", err)
	}
}

func beginRoleTransaction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+role); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	return tx
}

func asPostgreSQLError(err error, target **pgconn.PgError) bool {
	value, ok := err.(*pgconn.PgError)
	if ok {
		*target = value
	}
	return ok
}

func assertRLSDecision(t *testing.T, compiled *program.Program, activation rlsActivation, want schema.OutcomeID) {
	t.Helper()
	got, _ := evaluateRLS(t, compiled, activation)
	if got != want {
		t.Fatalf("NornRune RLS outcome = %d, want %d", got, want)
	}
}

func postgresExpressionSchema(t *testing.T) publicsql.Schema {
	t.Helper()
	bindings := public.BindingSet{
		Name: "postgres-differential", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: "enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
		},
	}
	sqlSchema, err := publicsql.NewSchema(publicsql.DialectPostgreSQL, bindings, nil, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return sqlSchema
}

func postgresRLSSchema(t *testing.T) publicsql.Schema {
	t.Helper()
	bindings := testRLSSchema(t).Bindings
	bindings.Fields[4].Source = "sql_role"
	sqlSchema, err := publicsql.NewSchema(publicsql.DialectPostgreSQL, bindings, nil, "sql_command", "sql_role", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return sqlSchema
}

func evaluateExpression(t *testing.T, compiled *program.Program, fixture expressionFixture) schema.OutcomeID {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if fixture.Team != nil {
		symbol, err := builder.InternSymbol([]byte(*fixture.Team))
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 1, symbol); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.Count != nil {
		if err := builder.SetInteger(0, 2, *fixture.Count); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.Enabled != nil {
		if err := builder.SetBoolean(0, 3, *fixture.Enabled); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var outcomes result.Batch
	if err := executor.Execute(&outcomes, compiled, batch); err != nil {
		t.Fatal(err)
	}
	return outcomes.OutcomeIDs[0]
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}
