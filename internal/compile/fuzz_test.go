package compile

import (
	"os"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func FuzzValidateAndLower(f *testing.F) {
	source, err := os.ReadFile("../../testdata/policies/valid-full.json")
	if err != nil {
		f.Fatalf("read valid policy fixture: %v", err)
	}
	fields, symbols := fuzzCompileSchema(f)
	for _, seed := range []struct {
		field  uint8
		action uint8
		index  uint16
		value  uint64
	}{
		{field: 0, action: 0},
		{field: 1, action: 1, value: ^uint64(0)},
		{field: 17, action: 2, index: 3, value: 1},
		{field: 63, action: 1, index: ^uint16(0), value: ^uint64(0)},
	} {
		f.Add(seed.field, seed.action, seed.index, seed.value)
	}

	f.Fuzz(func(t *testing.T, field, action uint8, index uint16, value uint64) {
		builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
		if err := jsonpolicy.Decode(builder, source, fields, symbols, jsonpolicy.Limits{}); err != nil {
			t.Fatalf("decode valid policy fixture: %v", err)
		}
		document := builder.Document()
		mutateFuzzDocument(document, field, action, index, value)

		var validator Validator
		diagnostics := validator.Validate(nil, document, fields)
		want := program.Program{InputBytes: []byte("unchanged"), PolicyName: 99}
		destination := want
		var lowerer Lowerer
		err := lowerer.Lower(&destination, document, fields, symbols)
		if err != nil {
			if !reflect.DeepEqual(destination, want) {
				t.Fatal("failed lowering mutated destination")
			}
			return
		}
		if len(diagnostics) != 0 {
			t.Fatalf("lowering accepted document with %d diagnostics", len(diagnostics))
		}
		if destination.ContentHash == [32]byte{} || len(destination.Opcodes) == 0 {
			t.Fatal("successful lowering produced an empty Program")
		}
	})
}

func fuzzCompileSchema(tb testing.TB) (*schema.Schema, *schema.Interner) {
	tb.Helper()
	symbols := schema.NewSymbolInterner(16)
	fields := schema.NewBuilder()
	for _, field := range []struct {
		name  string
		kind  schema.ValueKind
		group schema.FieldGroup
	}{
		{name: "subject.trust", kind: schema.ValueKindSymbol, group: schema.FieldGroupSubject},
		{name: "context.environment", kind: schema.ValueKindPresence, group: schema.FieldGroupContext},
		{name: "context.usage", kind: schema.ValueKindSymbol, group: schema.FieldGroupContext},
	} {
		name, err := symbols.Intern([]byte(field.name))
		if err != nil {
			tb.Fatalf("intern field %q: %v", field.name, err)
		}
		if _, err := fields.AddField(name, field.kind, field.group); err != nil {
			tb.Fatalf("add field %q: %v", field.name, err)
		}
	}
	return fields.Finish(), symbols
}

func mutateFuzzDocument(document *ast.Document, fieldSelector, action uint8, index uint16, value uint64) {
	documentValue := reflect.ValueOf(document).Elem()
	sliceCount := 0
	for field := range documentValue.NumField() {
		if documentValue.Field(field).Kind() == reflect.Slice {
			sliceCount++
		}
	}
	target := int(fieldSelector) % sliceCount
	for field := range documentValue.NumField() {
		column := documentValue.Field(field)
		if column.Kind() != reflect.Slice {
			continue
		}
		if target != 0 {
			target--
			continue
		}
		switch action % 3 {
		case 0:
			if column.Len() != 0 {
				column.SetLen(int(index) % (column.Len() + 1))
			}
		case 1:
			if column.Len() == 0 {
				column.Set(reflect.Append(column, reflect.Zero(column.Type().Elem())))
			} else {
				setFuzzValue(column.Index(int(index)%column.Len()), value)
			}
		case 2:
			item := reflect.New(column.Type().Elem()).Elem()
			setFuzzValue(item, value)
			column.Set(reflect.Append(column, item))
		}
		return
	}
}

func setFuzzValue(destination reflect.Value, value uint64) {
	switch destination.Kind() {
	case reflect.Bool:
		destination.SetBool(value&1 != 0)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		destination.SetInt(int64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		destination.SetUint(value)
	default:
		destination.Set(reflect.Zero(destination.Type()))
	}
}
