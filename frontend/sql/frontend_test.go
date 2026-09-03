package sql

import (
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
)

func TestSQLProfileEnumsAreStable(t *testing.T) {
	if got, want := []uint8{uint8(DialectPostgreSQL), uint8(DialectSnowflake), uint8(DialectDatabricks)}, []uint8{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dialects = %v, want %v", got, want)
	}
	if got, want := []string{DialectPostgreSQL.String(), DialectSnowflake.String(), DialectDatabricks.String()}, []string{"postgresql", "snowflake", "databricks"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dialect names = %v, want %v", got, want)
	}
	if got, want := []uint8{uint8(CommandSelect), uint8(CommandInsert), uint8(CommandUpdateUsing), uint8(CommandUpdateCheck), uint8(CommandDelete)}, []uint8{1, 2, 3, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if got, want := []string{CommandSelect.String(), CommandInsert.String(), CommandUpdateUsing.String(), CommandUpdateCheck.String(), CommandDelete.String()}, []string{"select", "insert", "update_using", "update_check", "delete"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("command names = %v, want %v", got, want)
	}
	if DialectInvalid.Valid() || Dialect(255).Valid() || CommandInvalid.Valid() || Command(255).Valid() {
		t.Fatal("invalid SQL enum reported valid")
	}
	if public.LanguageSQL.String() != "sql" || !public.LanguageSQL.Valid() {
		t.Fatalf("LanguageSQL = (%q, %t)", public.LanguageSQL, public.LanguageSQL.Valid())
	}
}

func TestCapabilitiesAreProfileSpecificAndOwned(t *testing.T) {
	postgres := Capabilities(DialectPostgreSQL)
	snowflake := Capabilities(DialectSnowflake)
	databricks := Capabilities(DialectDatabricks)
	for name, capabilities := range map[string][]public.Capability{
		"postgresql": postgres,
		"snowflake":  snowflake,
		"databricks": databricks,
	} {
		if len(capabilities) == 0 {
			t.Fatalf("%s capabilities are empty", name)
		}
		for _, capability := range capabilities {
			if capability.Name == "" || !capability.Support.Valid() {
				t.Fatalf("%s capability = %#v", name, capability)
			}
		}
	}
	if supportFor(postgres, "row_level_security") != public.SupportSupported {
		t.Fatalf("PostgreSQL RLS support = %v", supportFor(postgres, "row_level_security"))
	}
	if supportFor(snowflake, "row_level_security") != public.SupportRejected || supportFor(databricks, "row_level_security") != public.SupportRejected {
		t.Fatal("expression-only profiles accepted PostgreSQL RLS")
	}
	postgres[0].Name = "mutated"
	if Capabilities(DialectPostgreSQL)[0].Name == "mutated" {
		t.Fatal("Capabilities returned package storage")
	}
	if Capabilities(DialectInvalid) != nil {
		t.Fatal("invalid dialect returned capabilities")
	}
}

func TestNewSchemaValidatesAndOwnsBindingsAndParameters(t *testing.T) {
	bindings := public.BindingSet{
		Name: "sql-policy", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "sql_command", Target: "context.sql_command", Kind: public.ValueKindString, Group: public.FieldGroupContext},
			{Source: "sql_role", Target: "context.sql_role", Kind: public.ValueKindString, Group: public.FieldGroupContext},
		},
	}
	parameters := []Parameter{
		{Name: "$1", Value: public.StringLiteral([]byte("blue"))},
		{Name: "$2", Value: public.StringLiteral([]byte("green"))},
	}
	schema, err := NewSchema(DialectPostgreSQL, bindings, parameters, "sql_command", "sql_role", public.DefaultLimits())
	if err != nil {
		t.Fatalf("NewSchema() error = %v", err)
	}
	bindings.Fields[0].Source = "mutated"
	parameters[0].Name = "$2"
	parameters[0].Value.String[0] = 'X'
	parameters[1].Value.String[0] = 'X'
	if schema.Bindings.Fields[0].Source != "team" || schema.Parameters[0].Name != "$1" || string(schema.Parameters[0].Value.String) != "blue" ||
		string(schema.Parameters[1].Value.String) != "green" {
		t.Fatalf("schema borrowed input: %#v", schema)
	}
	if cap(schema.Parameters[0].Value.String) != len(schema.Parameters[0].Value.String) {
		t.Fatalf("first parameter capacity = %d, want %d", cap(schema.Parameters[0].Value.String), len(schema.Parameters[0].Value.String))
	}
	schema.Parameters[0].Value.String = append(schema.Parameters[0].Value.String, '!')
	if got := string(schema.Parameters[1].Value.String); got != "green" {
		t.Fatalf("second parameter changed after append: %q", got)
	}
	if schema.CommandField != "sql_command" || schema.RoleField != "sql_role" || schema.Dialect != DialectPostgreSQL {
		t.Fatalf("schema metadata = %#v", schema)
	}
	if err := schema.Validate(public.DefaultLimits()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestNewSchemaRejectsMalformedDeclarations(t *testing.T) {
	valid := func() (public.BindingSet, []Parameter) {
		return public.BindingSet{
			Name: "sql-policy", Version: "v1",
			Fields: []public.Binding{
				{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
				{Source: "sql_command", Target: "context.sql_command", Kind: public.ValueKindString, Group: public.FieldGroupContext},
				{Source: "sql_role", Target: "context.sql_role", Kind: public.ValueKindString, Group: public.FieldGroupContext},
			},
		}, []Parameter{{Name: "$1", Value: public.StringLiteral([]byte("blue"))}}
	}
	tests := []struct {
		name   string
		mutate func(*Dialect, *public.BindingSet, *[]Parameter, *string, *string, *public.Limits)
	}{
		{name: "dialect", mutate: func(d *Dialect, _ *public.BindingSet, _ *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			*d = DialectInvalid
		}},
		{name: "bindings", mutate: func(_ *Dialect, b *public.BindingSet, _ *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			b.Name = ""
		}},
		{name: "parameter syntax", mutate: func(_ *Dialect, _ *public.BindingSet, p *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			(*p)[0].Name = ":name"
		}},
		{name: "parameter kind", mutate: func(_ *Dialect, _ *public.BindingSet, p *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			(*p)[0].Value.Kind = public.ValueKindInvalid
		}},
		{name: "duplicate parameter", mutate: func(_ *Dialect, _ *public.BindingSet, p *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			*p = append(*p, (*p)[0])
		}},
		{name: "parameter count", mutate: func(_ *Dialect, _ *public.BindingSet, p *[]Parameter, _ *string, _ *string, l *public.Limits) {
			*p = append(*p, Parameter{Name: "$2", Value: public.StringLiteral([]byte("green"))})
			l.MaxLiterals = 1
		}},
		{name: "unknown command field", mutate: func(_ *Dialect, _ *public.BindingSet, _ *[]Parameter, c *string, _ *string, _ *public.Limits) {
			*c = "unknown"
		}},
		{name: "wrong role type", mutate: func(_ *Dialect, b *public.BindingSet, _ *[]Parameter, _ *string, _ *string, _ *public.Limits) {
			b.Fields[2].Kind = public.ValueKindBoolean
		}},
		{name: "string limit", mutate: func(_ *Dialect, _ *public.BindingSet, _ *[]Parameter, _ *string, _ *string, l *public.Limits) {
			l.MaxStringBytes = 8
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialect := DialectPostgreSQL
			bindings, parameters := valid()
			command, role := "sql_command", "sql_role"
			limits := public.DefaultLimits()
			test.mutate(&dialect, &bindings, &parameters, &command, &role, &limits)
			if _, err := NewSchema(dialect, bindings, parameters, command, role, limits); err == nil {
				t.Fatal("NewSchema() error = nil")
			}
		})
	}
}

func TestNewSchemaRejectsParameterLimitBeforeAllocating(t *testing.T) {
	bindings := public.BindingSet{
		Name: "sql-policy", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
		},
	}
	parameters := []Parameter{
		{Name: "$1", Value: public.StringLiteral([]byte("blue"))},
		{Name: "$2", Value: public.StringLiteral([]byte("green"))},
	}
	limits := public.DefaultLimits()
	limits.MaxLiterals = 1
	allocations := testing.AllocsPerRun(100, func() {
		if _, err := NewSchema(DialectPostgreSQL, bindings, parameters, "", "", limits); err == nil {
			t.Fatal("NewSchema() error = nil")
		}
	})
	if allocations != 0 {
		t.Fatalf("NewSchema() allocations = %v, want 0", allocations)
	}
}

func TestSQLDiagnosticIsPointerless(t *testing.T) {
	assertSQLPointerless(t, reflect.TypeOf(Diagnostic{}))
}

func supportFor(capabilities []public.Capability, name string) public.Support {
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability.Support
		}
	}
	return public.SupportInvalid
}

func assertSQLPointerless(t *testing.T, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.String, reflect.Interface, reflect.Func, reflect.Chan:
		t.Fatalf("%v contains pointers", typ)
	case reflect.Array:
		assertSQLPointerless(t, typ.Elem())
	case reflect.Struct:
		for row := 0; row < typ.NumField(); row++ {
			assertSQLPointerless(t, typ.Field(row).Type)
		}
	}
}
