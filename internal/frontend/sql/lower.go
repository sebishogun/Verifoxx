// Package sql lowers bounded SQL frontend results into native Programs.
package sql

import (
	"errors"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
	"github.com/sebishogun/nornrune/frontend/sql/postgres"
	corefrontend "github.com/sebishogun/nornrune/internal/frontend"
	"github.com/sebishogun/nornrune/internal/program"
)

// ErrInvalidCompiler reports an unusable compiler receiver or destination.
var ErrInvalidCompiler = errors.New("sql frontend compiler: invalid receiver or destination")

// Compiler owns reusable semantic-lowering and diagnostic scratch. It is not
// safe for concurrent use. Published Programs never borrow it.
type Compiler struct {
	diagnostics []publicsql.Diagnostic
	semantic    corefrontend.Compiler
}

// CompileRLS parses PostgreSQL policies and lowers their composed semantics
// atomically into dst. Diagnostics remain valid until the next call.
func (compiler *Compiler) CompileRLS(dst *program.Program, source []byte, schema publicsql.Schema, limits public.Limits) ([]publicsql.Diagnostic, error) {
	if compiler == nil || dst == nil {
		return nil, ErrInvalidCompiler
	}
	compiler.diagnostics = compiler.diagnostics[:0]
	rls, diagnostics := postgres.CompileRLS(source, schema, limits)
	if len(diagnostics) != 0 {
		compiler.diagnostics = append(compiler.diagnostics, diagnostics...)
		return compiler.diagnostics, nil
	}
	semanticDiagnostics, err := compiler.semantic.Compile(dst, rls.Semantic)
	if err != nil {
		return nil, err
	}
	if len(semanticDiagnostics) == 0 {
		return compiler.diagnostics, nil
	}
	if cap(compiler.diagnostics) < len(semanticDiagnostics) {
		compiler.diagnostics = make([]publicsql.Diagnostic, 0, len(semanticDiagnostics))
	}
	for _, diagnostic := range semanticDiagnostics {
		compiler.diagnostics = append(compiler.diagnostics, publicsql.Diagnostic{
			Span: diagnostic.Span, Row: diagnostic.Row, Field: diagnostic.Field,
			Dialect: publicsql.DialectPostgreSQL, Code: diagnostic.Code,
		})
	}
	return compiler.diagnostics, nil
}
