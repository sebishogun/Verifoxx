package postgres

import (
	"bytes"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	publicsql "github.com/sebishogun/nornrune/frontend/sql"
)

func TestCompileRLSOwnsSoAPolicyRowsAndCSRRoles(t *testing.T) {
	source := []byte(`
CREATE POLICY visible ON records FOR SELECT TO analyst, auditor
  USING (team = 'blue');
CREATE POLICY bounded ON records AS RESTRICTIVE FOR ALL TO PUBLIC
  USING (enabled) WITH CHECK (count < 10);
`)
	rls, diagnostics := CompileRLS(source, rlsSchema(t), public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() diagnostics = %#v", diagnostics)
	}
	if rls == nil || rls.Semantic == nil || rls.Semantic.Default != public.DefaultReject {
		t.Fatalf("RLS = %#v", rls)
	}
	if got, want := rls.Modes, []PolicyMode{PolicyModePermissive, PolicyModeRestrictive}; !reflect.DeepEqual(got, want) {
		t.Fatalf("modes = %v, want %v", got, want)
	}
	if got, want := rls.Commands, []PolicyCommand{PolicyCommandSelect, PolicyCommandAll}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if got, want := rls.RoleStarts, []uint32{0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("role starts = %v, want %v", got, want)
	}
	if got, want := rls.RoleCounts, []uint16{2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("role counts = %v, want %v", got, want)
	}
	if got, want := rls.RoleNames(), []string{"analyst", "auditor", "public"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	if got, want := rls.PolicyNames(), []string{"visible", "bounded"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("policy names = %v, want %v", got, want)
	}
	if string(rls.Table) != "records" || len(rls.UsingRoots) != 2 || len(rls.CheckRoots) != 2 || rls.UsingRoots[0] == 0 || rls.CheckRoots[0] == 0 {
		t.Fatalf("RLS metadata = %#v", rls)
	}
	before := append([]byte(nil), rls.Semantic.Source...)
	source[1] = 'X'
	if !bytes.Equal(rls.Semantic.Source, before) {
		t.Fatal("CompileRLS() borrowed source")
	}
}

func TestCompileRLSAppliesPostgreSQLClauseDefaults(t *testing.T) {
	source := []byte(`CREATE POLICY defaults ON records;`)
	rls, diagnostics := CompileRLS(source, rlsSchema(t), public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileRLS() diagnostics = %#v", diagnostics)
	}
	if len(rls.Modes) != 1 || rls.Modes[0] != PolicyModePermissive || rls.Commands[0] != PolicyCommandAll {
		t.Fatalf("default row = modes %v commands %v", rls.Modes, rls.Commands)
	}
	if got := rls.RoleNames(); len(got) != 1 || got[0] != "public" {
		t.Fatalf("default roles = %v", got)
	}
	if rls.UsingRoots[0] == 0 || rls.CheckRoots[0] != rls.UsingRoots[0] {
		t.Fatalf("default roots = using %d check %d", rls.UsingRoots[0], rls.CheckRoots[0])
	}
}

func TestCompileRLSRejectsMalformedOrUnsupportedPolicies(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   public.DiagnosticCode
	}{
		{name: "empty", source: ``, code: public.CodeSyntax},
		{name: "not create", source: `ALTER POLICY p ON records;`, code: public.CodeUnsupported},
		{name: "duplicate", source: `CREATE POLICY p ON records; CREATE POLICY p ON records;`, code: public.CodeDuplicate},
		{name: "different table", source: `CREATE POLICY p ON records; CREATE POLICY q ON other;`, code: public.CodeInvalidPolicy},
		{name: "qualified table", source: `CREATE POLICY p ON app.records;`, code: public.CodeUnsupported},
		{name: "insert using", source: `CREATE POLICY p ON records FOR INSERT USING (enabled);`, code: public.CodeUnsupported},
		{name: "select check", source: `CREATE POLICY p ON records FOR SELECT WITH CHECK (enabled);`, code: public.CodeUnsupported},
		{name: "unbalanced", source: `CREATE POLICY p ON records USING (enabled;`, code: public.CodeSyntax},
		{name: "trailing", source: `CREATE POLICY p ON records; junk`, code: public.CodeUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rls, diagnostics := CompileRLS([]byte(test.source), rlsSchema(t), public.DefaultLimits())
			if rls != nil {
				t.Fatalf("CompileRLS() = %#v, want nil", rls)
			}
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code || diagnostics[0].Dialect != publicsql.DialectPostgreSQL {
				t.Fatalf("diagnostics = %#v, want code %v", diagnostics, test.code)
			}
		})
	}
}

func TestCompileRLSRequiresBoundStringCommandAndRoleFields(t *testing.T) {
	schema := rlsSchema(t)
	schema.CommandField = ""
	if rls, diagnostics := CompileRLS([]byte(`CREATE POLICY p ON records;`), schema, public.DefaultLimits()); rls != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeInvalidBinding {
		t.Fatalf("missing command field = RLS %#v diagnostics %#v", rls, diagnostics)
	}
	schema = rlsSchema(t)
	schema.Bindings.Fields[4].Kind = public.ValueKindBoolean
	if rls, diagnostics := CompileRLS([]byte(`CREATE POLICY p ON records;`), schema, public.DefaultLimits()); rls != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeInvalidBinding {
		t.Fatalf("invalid role field = RLS %#v diagnostics %#v", rls, diagnostics)
	}
}

func TestCompileRLSRoleLimitIsAtomic(t *testing.T) {
	limits := public.DefaultLimits()
	limits.MaxChildren = 1
	rls, diagnostics := CompileRLS([]byte(`CREATE POLICY p ON records TO one, two;`), rlsSchema(t), limits)
	if rls != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeLimit {
		t.Fatalf("role limit = RLS %#v diagnostics %#v", rls, diagnostics)
	}
}

func rlsSchema(t *testing.T) publicsql.Schema {
	t.Helper()
	bindings := public.BindingSet{
		Name: "postgres-rls", Version: "v1",
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
