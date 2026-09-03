package sql

import (
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
	"github.com/sebishogun/nornrune/internal/program"
)

func TestCompilerCompilesRLSAtomically(t *testing.T) {
	schema := testRLSSchema(t)
	source := []byte(`CREATE POLICY p ON records FOR SELECT TO PUBLIC USING (enabled);`)
	var compiler Compiler
	var destination program.Program
	diagnostics, err := compiler.CompileRLS(&destination, source, schema, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 || len(destination.Opcodes) == 0 {
		t.Fatalf("CompileRLS() = destination %#v diagnostics %#v error %v", destination, diagnostics, err)
	}

	before := destination
	before.Opcodes = append([]program.Opcode(nil), destination.Opcodes...)
	diagnostics, err = compiler.CompileRLS(&destination, []byte(`CREATE POLICY p ON records USING (missing);`), schema, public.DefaultLimits())
	if err != nil {
		t.Fatalf("invalid CompileRLS() error = %v", err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != public.CodeUnknownField {
		t.Fatalf("invalid CompileRLS() diagnostics = %#v", diagnostics)
	}
	if !reflect.DeepEqual(destination.Opcodes, before.Opcodes) {
		t.Fatal("destination changed on diagnostics")
	}
}

func TestCompilerCompilesPublicRoleRegardlessOfPosition(t *testing.T) {
	schema := testRLSSchema(t)
	for _, source := range []string{
		`CREATE POLICY p ON records FOR SELECT TO PUBLIC, analyst USING (enabled);`,
		`CREATE POLICY p ON records FOR SELECT TO analyst, PUBLIC USING (enabled);`,
		`CREATE POLICY p ON records FOR SELECT TO analyst, PUBLIC, auditor USING (enabled);`,
	} {
		t.Run(source, func(t *testing.T) {
			var compiler Compiler
			var destination program.Program
			diagnostics, err := compiler.CompileRLS(&destination, []byte(source), schema, public.DefaultLimits())
			if err != nil || len(diagnostics) != 0 || len(destination.Opcodes) == 0 {
				t.Fatalf("CompileRLS() = destination %#v diagnostics %#v error %v", destination, diagnostics, err)
			}
		})
	}
}

func TestCompilerRejectsInvalidReceiverOrDestination(t *testing.T) {
	schema := testRLSSchema(t)
	source := []byte(`CREATE POLICY p ON records;`)
	var compiler *Compiler
	var destination program.Program
	if _, err := compiler.CompileRLS(&destination, source, schema, public.DefaultLimits()); err == nil {
		t.Fatal("nil compiler error = nil")
	}
	compiler = &Compiler{}
	if _, err := compiler.CompileRLS(nil, source, schema, public.DefaultLimits()); err == nil {
		t.Fatal("nil destination error = nil")
	}
}

func testRLSSchema(t testing.TB) publicsql.Schema {
	t.Helper()
	bindings := public.BindingSet{
		Name: "rls", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: "enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
			{Source: "sql_command", Target: "context.sql_command", Kind: public.ValueKindString, Group: public.FieldGroupContext},
			{Source: "sql_role", Target: "context.sql_role", Kind: public.ValueKindString, Group: public.FieldGroupContext},
		},
	}
	schema, err := publicsql.NewSchema(publicsql.DialectPostgreSQL, bindings, nil, "sql_command", "sql_role", public.DefaultLimits())
	if err != nil {
		t.Fatalf("NewSchema() error = %v", err)
	}
	return schema
}
