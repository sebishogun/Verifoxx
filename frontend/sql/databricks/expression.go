// Package databricks exposes the bounded Databricks SQL profile.
package databricks

import (
	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

// CompileExpression compiles one Databricks-profile scalar expression.
func CompileExpression(source []byte, schema publicsql.Schema, limits public.Limits) (*public.Policy, []publicsql.Diagnostic) {
	return publicsql.CompileExpression(source, publicsql.DialectDatabricks, schema, limits)
}
