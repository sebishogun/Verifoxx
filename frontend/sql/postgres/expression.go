// Package postgres exposes the bounded PostgreSQL SQL profile.
package postgres

import (
	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

// CompileExpression compiles one PostgreSQL-profile scalar expression.
func CompileExpression(source []byte, schema publicsql.Schema, limits public.Limits) (*public.Policy, []publicsql.Diagnostic) {
	return publicsql.CompileExpression(source, publicsql.DialectPostgreSQL, schema, limits)
}
