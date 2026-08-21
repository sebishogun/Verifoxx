package ast

import (
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestTypedValuesAndSymbolOwnership(t *testing.T) {
	b := NewBuilder(Hints{
		Values:          4,
		SymbolValues:    1,
		SymbolBytes:     5,
		IntegerValues:   1,
		BooleanValues:   1,
		TimestampValues: 1,
	})
	symbol := []byte("alpha")
	symbolID, err := b.AddSymbolValue(symbol)
	if err != nil {
		t.Fatal(err)
	}
	integerID, err := b.AddIntegerValue(-42)
	if err != nil {
		t.Fatal(err)
	}
	booleanID, err := b.AddBooleanValue(true)
	if err != nil {
		t.Fatal(err)
	}
	timestampID, err := b.AddTimestampValue(123456789)
	if err != nil {
		t.Fatal(err)
	}
	if symbolID != 1 || integerID != 2 || booleanID != 3 || timestampID != 4 {
		t.Fatalf("ValueIDs = (%d, %d, %d, %d), want 1..4", symbolID, integerID, booleanID, timestampID)
	}
	symbol[0] = 'X'
	d := b.Document()
	if got, ok := d.SymbolValue(symbolID); !ok || string(got) != "alpha" {
		t.Fatalf("SymbolValue = (%q, %v), want (alpha, true)", got, ok)
	}
	if got, ok := d.IntegerValue(integerID); !ok || got != -42 {
		t.Fatalf("IntegerValue = (%d, %v), want (-42, true)", got, ok)
	}
	if got, ok := d.BooleanValue(booleanID); !ok || !got {
		t.Fatalf("BooleanValue = (%v, %v), want (true, true)", got, ok)
	}
	if got, ok := d.TimestampValue(timestampID); !ok || got != 123456789 {
		t.Fatalf("TimestampValue = (%d, %v), want (123456789, true)", got, ok)
	}
	wantKinds := []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
	}
	if !reflect.DeepEqual(d.ValueKinds, wantKinds) {
		t.Fatalf("ValueKinds = %v, want %v", d.ValueKinds, wantKinds)
	}
	if _, ok := d.IntegerValue(symbolID); ok {
		t.Fatal("IntegerValue(symbol) must fail")
	}
}

func TestInCompareUsesValueCSR(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 2, CompareNodes: 2, CompareListValues: 3, Values: 4, IntegerValues: 4})
	values := make([]schema.ValueID, 3)
	for i := range values {
		id, err := b.AddIntegerValue(int64(i + 1))
		if err != nil {
			t.Fatal(err)
		}
		values[i] = id
	}
	in, err := b.AddIn(1, values, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	values[0] = 99
	scalarValue, err := b.AddIntegerValue(9)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := b.AddCompare(1, CompareOpEqual, scalarValue, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}

	d := b.Document()
	wantValues := []schema.ValueID{1, 2, 3}
	if got, ok := d.InValues(in); !ok || !reflect.DeepEqual(got, wantValues) {
		t.Fatalf("InValues = (%v, %v), want (%v, true)", got, ok, wantValues)
	}
	if start, count, ok := d.CompareListRange(in); !ok || start != 0 || count != 3 {
		t.Fatalf("CompareListRange(in) = (%d, %d, %v), want (0, 3, true)", start, count, ok)
	}
	if start, count, ok := d.CompareListRange(equal); !ok || start != 3 || count != 0 {
		t.Fatalf("CompareListRange(equal) = (%d, %d, %v), want (3, 0, true)", start, count, ok)
	}
	if _, err := b.AddCompare(1, CompareOpIn, 1, SourceSpan{}); err == nil {
		t.Fatal("scalar AddCompare must reject CompareOpIn")
	}
}
