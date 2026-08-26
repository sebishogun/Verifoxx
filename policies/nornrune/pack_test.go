package nornrune_test

import (
	"bytes"
	"testing"

	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

var wantFields = []struct {
	name  string
	group schema.FieldGroup
}{
	{"requester.team", schema.FieldGroupSubject},
	{"requester.trust", schema.FieldGroupSubject},
	{"action.type", schema.FieldGroupAction},
	{"action.output", schema.FieldGroupOutput},
	{"action.dataset", schema.FieldGroupResource},
	{"environment.execution_env", schema.FieldGroupContext},
	{"environment.usage", schema.FieldGroupContext},
}

func TestPackOwnsSemanticPolicyAndSchema(t *testing.T) {
	source := nornrune.Source()
	if source == "" || !bytes.Contains([]byte(source), []byte(`"requirements"`)) {
		t.Fatalf("Source() does not contain a semantic policy: %q", source)
	}

	fields, symbols, err := nornrune.NewSchema()
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	if fields.Len() != len(wantFields) {
		t.Fatalf("schema fields = %d, want %d", fields.Len(), len(wantFields))
	}
	for row, want := range wantFields {
		field := schema.FieldID(row + 1)
		name, ok := fields.Name(field)
		if !ok {
			t.Fatalf("field %d has no name", field)
		}
		gotName, ok := symbols.Bytes(name)
		if !ok || string(gotName) != want.name {
			t.Errorf("field %d name = %q, %v; want %q, true", field, gotName, ok, want.name)
		}
		kind, ok := fields.Kind(field)
		if !ok || kind != schema.ValueKindSymbol {
			t.Errorf("field %d kind = %v, %v; want symbol, true", field, kind, ok)
		}
		group, ok := fields.Group(field)
		if !ok || group != want.group {
			t.Errorf("field %d group = %v, %v; want %v, true", field, group, ok, want.group)
		}
		resolved, ok := symbols.Lookup([]byte(want.name))
		if !ok || resolved != name {
			t.Errorf("Lookup(%q) = %d, %v; want %d, true", want.name, resolved, ok, name)
		}
	}

	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	if err := jsonpolicy.Decode(builder, []byte(source), fields, symbols, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode embedded policy: %v", err)
	}
	if _, err := compile.Lower(builder.Document(), fields, symbols); err != nil {
		t.Fatalf("compile embedded policy: %v", err)
	}
}
