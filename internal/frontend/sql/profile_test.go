package sql

import (
	"encoding/json"
	"os"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
	"github.com/sebishogun/nornrune/frontend/sql/databricks"
	"github.com/sebishogun/nornrune/frontend/sql/snowflake"
	corefrontend "github.com/sebishogun/nornrune/internal/frontend"
)

func TestSnowflakeAndDatabricksFixtureProfilesCompileWithoutEngineClaims(t *testing.T) {
	encoded, err := os.ReadFile("../../../testdata/frontends/sql/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Name       string `json:"name"`
			Snowflake  string `json:"snowflake"`
			Databricks string `json:"databricks"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(encoded, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("profile corpus is empty")
	}
	for _, fixture := range corpus.Cases {
		t.Run(fixture.Name, func(t *testing.T) {
			compileProfileExpression(t, publicsql.DialectSnowflake, fixture.Snowflake)
			compileProfileExpression(t, publicsql.DialectDatabricks, fixture.Databricks)
		})
	}
}

func compileProfileExpression(t *testing.T, dialect publicsql.Dialect, source string) {
	t.Helper()
	schema := profileSchema(t, dialect)
	var policy *public.Policy
	var diagnostics []publicsql.Diagnostic
	switch dialect {
	case publicsql.DialectSnowflake:
		policy, diagnostics = snowflake.CompileExpression([]byte(source), schema, public.DefaultLimits())
	case publicsql.DialectDatabricks:
		policy, diagnostics = databricks.CompileExpression([]byte(source), schema, public.DefaultLimits())
	default:
		t.Fatalf("unsupported test dialect %v", dialect)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("CompileExpression(%q) diagnostics = %#v", source, diagnostics)
	}
	compiled, semanticDiagnostics, err := corefrontend.Compile(policy)
	if err != nil || len(semanticDiagnostics) != 0 || compiled == nil {
		t.Fatalf("semantic Compile(%q) = program %v diagnostics %#v error %v", source, compiled, semanticDiagnostics, err)
	}
}

func profileSchema(t *testing.T, dialect publicsql.Dialect) publicsql.Schema {
	t.Helper()
	team, count, enabled := "team", "count", "enabled"
	if dialect == publicsql.DialectSnowflake {
		team, count, enabled = "TEAM", "COUNT", "ENABLED"
	}
	bindings := public.BindingSet{
		Name: "sql-profile", Version: "v1",
		Fields: []public.Binding{
			{Source: team, Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: count, Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: enabled, Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
		},
	}
	schema, err := publicsql.NewSchema(dialect, bindings, nil, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
