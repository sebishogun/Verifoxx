package index

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

func TestSchemaBuildAssignsKindLocalColumns(t *testing.T) {
	kinds := []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindSymbol,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	}
	var got Schema
	if err := BuildSchema(&got, kinds); err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	wantColumns := []uint32{0, 0, 1, 0, 0, 0}
	if !reflect.DeepEqual(got.Kinds, kinds) || !reflect.DeepEqual(got.Columns, wantColumns) {
		t.Fatalf("schema = kinds %v columns %v, want %v/%v", got.Kinds, got.Columns, kinds, wantColumns)
	}
	wantCounts := [6]uint32{0, 2, 1, 1, 1, 1}
	if got.Counts != wantCounts {
		t.Fatalf("counts = %v, want %v", got.Counts, wantCounts)
	}
	for i, kind := range kinds {
		gotKind, column, ok := got.Lookup(schema.FieldID(i + 1))
		if !ok || gotKind != kind || column != wantColumns[i] {
			t.Fatalf("Lookup(%d) = (%d,%d,%v), want (%d,%d,true)", i+1, gotKind, column, ok, kind, wantColumns[i])
		}
	}
	for _, field := range []schema.FieldID{0, 7, ^schema.FieldID(0)} {
		if kind, column, ok := got.Lookup(field); ok || kind != 0 || column != 0 {
			t.Fatalf("Lookup(%d) = (%d,%d,%v), want zero/false", field, kind, column, ok)
		}
	}
	for kind := schema.ValueKindSymbol; kind <= schema.ValueKindPresence; kind++ {
		count, ok := got.ColumnCount(kind)
		if !ok || count != wantCounts[kind] {
			t.Fatalf("ColumnCount(%d) = (%d,%v), want (%d,true)", kind, count, ok, wantCounts[kind])
		}
	}
	if count, ok := got.ColumnCount(schema.ValueKindInvalid); ok || count != 0 {
		t.Fatalf("ColumnCount(invalid) = (%d,%v), want zero/false", count, ok)
	}
}

func TestSchemaCloneOwnsExactStorage(t *testing.T) {
	var src Schema
	if err := BuildSchema(&src, []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger}); err != nil {
		t.Fatal(err)
	}
	clone := src.Clone()
	if !reflect.DeepEqual(clone, src) {
		t.Fatalf("Clone = %+v, want %+v", clone, src)
	}
	if len(clone.Kinds) != cap(clone.Kinds) || len(clone.Columns) != cap(clone.Columns) {
		t.Fatalf("clone capacities = %d/%d and %d/%d", len(clone.Kinds), cap(clone.Kinds), len(clone.Columns), cap(clone.Columns))
	}
	if &clone.Kinds[0] == &src.Kinds[0] || &clone.Columns[0] == &src.Columns[0] {
		t.Fatal("Clone borrows source storage")
	}
	if err := BuildSchema(&src, []schema.ValueKind{schema.ValueKindBoolean}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(clone.Kinds, []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger}) {
		t.Fatalf("source rebuild changed clone kinds: %v", clone.Kinds)
	}
	var empty Schema
	emptyClone := empty.Clone()
	if emptyClone.Kinds != nil || emptyClone.Columns != nil {
		t.Fatalf("empty Clone = %+v, want nil slices", emptyClone)
	}
}

func TestSchemaBuildRejectsInvalidAtomically(t *testing.T) {
	dst := Schema{
		Kinds:   []schema.ValueKind{schema.ValueKindSymbol},
		Columns: []uint32{7},
		Counts:  [6]uint32{0, 8},
	}
	want := dst.Clone()
	if err := BuildSchema(&dst, []schema.ValueKind{schema.ValueKindInvalid}); !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("invalid kind error = %v, want %v", err, ErrInvalidSchema)
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("invalid build changed destination: %+v", dst)
	}
	if err := BuildSchema(nil, nil); !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("nil destination error = %v, want %v", err, ErrInvalidSchema)
	}
}
