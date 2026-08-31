package postgres

import (
	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

type PolicyMode = publicsql.PolicyMode
type PolicyCommand = publicsql.PolicyCommand
type RLS = publicsql.RLS

const (
	PolicyModeInvalid     = publicsql.PolicyModeInvalid
	PolicyModePermissive  = publicsql.PolicyModePermissive
	PolicyModeRestrictive = publicsql.PolicyModeRestrictive

	PolicyCommandInvalid = publicsql.PolicyCommandInvalid
	PolicyCommandAll     = publicsql.PolicyCommandAll
	PolicyCommandSelect  = publicsql.PolicyCommandSelect
	PolicyCommandInsert  = publicsql.PolicyCommandInsert
	PolicyCommandUpdate  = publicsql.PolicyCommandUpdate
	PolicyCommandDelete  = publicsql.PolicyCommandDelete
)

// CompileRLS parses and composes bounded PostgreSQL CREATE POLICY statements.
func CompileRLS(source []byte, schema publicsql.Schema, limits public.Limits) (*RLS, []publicsql.Diagnostic) {
	return publicsql.CompilePostgreSQLRLS(source, schema, limits)
}
