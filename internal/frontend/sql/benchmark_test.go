package sql

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/frontend/sql/postgres"
	corefrontend "github.com/sebishogun/nornrune/internal/frontend"
	"github.com/sebishogun/nornrune/internal/program"
)

var benchmarkSQLExpression = []byte(`team = 'blue' OR count >= -2 AND enabled`)

var benchmarkRLSSource = []byte(`
CREATE POLICY visible ON records FOR SELECT TO analyst, auditor USING (team = 'blue');
CREATE POLICY bounded ON records AS RESTRICTIVE FOR ALL TO PUBLIC USING (enabled) WITH CHECK (count < 10);
`)

func BenchmarkSQLParse(b *testing.B) {
	schema := testRLSSchema(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkSQLExpression)))
	b.ResetTimer()
	for b.Loop() {
		policy, diagnostics := postgres.CompileExpression(benchmarkSQLExpression, schema, public.DefaultLimits())
		if len(diagnostics) != 0 || policy == nil {
			b.Fatalf("CompileExpression() = policy %v diagnostics %#v", policy, diagnostics)
		}
	}
}

func BenchmarkSQLLower(b *testing.B) {
	policy, diagnostics := postgres.CompileExpression(benchmarkSQLExpression, testRLSSchema(b), public.DefaultLimits())
	if len(diagnostics) != 0 {
		b.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
	}
	var compiler corefrontend.Compiler
	var destination program.Program
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkSQLExpression)))
	b.ResetTimer()
	for b.Loop() {
		semanticDiagnostics, err := compiler.Compile(&destination, policy)
		if err != nil || len(semanticDiagnostics) != 0 {
			b.Fatalf("Compile() = diagnostics %#v error %v", semanticDiagnostics, err)
		}
	}
}

func BenchmarkRLSCompile(b *testing.B) {
	schema := testRLSSchema(b)
	var compiler Compiler
	var destination program.Program
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkRLSSource)))
	b.ResetTimer()
	for b.Loop() {
		diagnostics, err := compiler.CompileRLS(&destination, benchmarkRLSSource, schema, public.DefaultLimits())
		if err != nil || len(diagnostics) != 0 {
			b.Fatalf("CompileRLS() = diagnostics %#v error %v", diagnostics, err)
		}
	}
}
