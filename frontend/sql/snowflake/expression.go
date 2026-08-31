// Package snowflake exposes the bounded Snowflake SQL profile.
package snowflake

import (
	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

// CompileExpression compiles one Snowflake-profile scalar expression.
func CompileExpression(source []byte, schema publicsql.Schema, limits public.Limits) (*public.Policy, []publicsql.Diagnostic) {
	return publicsql.CompileExpression(source, publicsql.DialectSnowflake, schema, limits)
}
