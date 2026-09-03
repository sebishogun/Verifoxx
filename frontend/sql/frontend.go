// Package sql defines bounded SQL frontend profiles and declarations.
package sql

import (
	"errors"

	public "github.com/sebishogun/nornrune/frontend"
)

var ErrInvalidSchema = errors.New("sql frontend: invalid schema")

// Dialect selects one independently tested SQL capability profile.
type Dialect uint8

const (
	DialectInvalid Dialect = iota
	DialectPostgreSQL
	DialectSnowflake
	DialectDatabricks
)

var dialectNames = [...]string{"postgresql", "snowflake", "databricks"}

func (dialect Dialect) Valid() bool {
	return dialect >= DialectPostgreSQL && dialect <= DialectDatabricks
}

func (dialect Dialect) String() string {
	row := int(dialect) - 1
	if row < 0 || row >= len(dialectNames) {
		return "invalid"
	}
	return dialectNames[row]
}

// Command identifies one runtime PostgreSQL RLS operation or phase.
type Command uint8

const (
	CommandInvalid Command = iota
	CommandSelect
	CommandInsert
	CommandUpdateUsing
	CommandUpdateCheck
	CommandDelete
)

var commandNames = [...]string{"select", "insert", "update_using", "update_check", "delete"}

func (command Command) Valid() bool {
	return command >= CommandSelect && command <= CommandDelete
}

func (command Command) String() string {
	row := int(command) - 1
	if row < 0 || row >= len(commandNames) {
		return "invalid"
	}
	return commandNames[row]
}

// Parameter is one typed compile-time SQL parameter binding. Named markers may
// be reused; each ? marker consumes the next ? declaration in slice order.
type Parameter struct {
	Name  string
	Value public.Literal
}
