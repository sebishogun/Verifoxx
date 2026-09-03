package databricks

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

func TestCompileExpressionUsesDatabricksQuotedIdentifiers(t *testing.T) {
	schema, err := publicsql.NewSchema(publicsql.DialectDatabricks, public.BindingSet{
		Name: "databricks", Version: "v1",
		Fields: []public.Binding{{Source: "Team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject}},
	}, nil, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	policy, diagnostics := CompileExpression([]byte("`Team` = 'blue'"), schema, public.DefaultLimits())
	if len(diagnostics) != 0 || policy == nil {
		t.Fatalf("CompileExpression() = policy %#v diagnostics %#v", policy, diagnostics)
	}
}
