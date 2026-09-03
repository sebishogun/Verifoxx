package snowflake

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

func TestCompileExpressionUsesSnowflakeFoldingAndParameters(t *testing.T) {
	schema, err := publicsql.NewSchema(publicsql.DialectSnowflake, public.BindingSet{
		Name: "snowflake", Version: "v1",
		Fields: []public.Binding{{Source: "TEAM", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject}},
	}, []publicsql.Parameter{{Name: ":team", Value: public.StringLiteral([]byte("blue"))}}, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	policy, diagnostics := CompileExpression([]byte(`team = :team`), schema, public.DefaultLimits())
	if len(diagnostics) != 0 || policy == nil {
		t.Fatalf("CompileExpression() = policy %#v diagnostics %#v", policy, diagnostics)
	}
}
