package postgres

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

func TestCompileExpressionUsesPostgreSQLProfile(t *testing.T) {
	schema, err := publicsql.NewSchema(publicsql.DialectPostgreSQL, public.BindingSet{
		Name: "postgres", Version: "v1",
		Fields: []public.Binding{{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject}},
	}, nil, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	policy, diagnostics := CompileExpression([]byte(`team = 'blue'`), schema, public.DefaultLimits())
	if len(diagnostics) != 0 || policy == nil {
		t.Fatalf("CompileExpression() = policy %#v diagnostics %#v", policy, diagnostics)
	}
}
