package index

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

const (
	testActionField   schema.FieldID = 1
	testResourceField schema.FieldID = 2
	testTrustField    schema.FieldID = 3
)

func policyConstraints() Constraints {
	return Constraints{
		Rows:        []uint32{2, 1, 4, 0, 1},
		Fields:      []schema.FieldID{testTrustField, testResourceField, testActionField, testTrustField, testActionField},
		ValueStarts: []uint32{0, 1, 2, 4, 5},
		ValueCounts: []uint32{1, 1, 2, 1, 1},
		Values:      []schema.SymbolID{11, 30, 22, 21, 10, 20},
	}
}

func reversedPolicyConstraints() Constraints {
	return Constraints{
		Rows:        []uint32{1, 0, 4, 1, 2},
		Fields:      []schema.FieldID{testActionField, testTrustField, testActionField, testResourceField, testTrustField},
		ValueStarts: []uint32{0, 1, 2, 4, 5},
		ValueCounts: []uint32{1, 1, 2, 1, 1},
		Values:      []schema.SymbolID{20, 10, 21, 22, 30, 11},
	}
}

func TestPolicyBuildCanonicalMasks(t *testing.T) {
	var builder PolicyBuilder
	var got Policy
	if err := builder.Build(&got, 5, policyConstraints()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got.RequirementCount != 5 || got.WordCount != 1 {
		t.Fatalf("counts = requirements %d words %d", got.RequirementCount, got.WordCount)
	}
	if !reflect.DeepEqual(got.FieldIDs, []schema.FieldID{1, 2, 3}) ||
		!reflect.DeepEqual(got.FieldValueStarts, []uint32{0, 3, 4}) ||
		!reflect.DeepEqual(got.FieldValueCounts, []uint32{3, 1, 2}) {
		t.Fatalf("field columns = %v %v %v", got.FieldIDs, got.FieldValueStarts, got.FieldValueCounts)
	}
	if !reflect.DeepEqual(got.Values, []schema.SymbolID{20, 21, 22, 30, 10, 11}) {
		t.Fatalf("values = %v", got.Values)
	}
	if !reflect.DeepEqual(got.AllMask, []uint64{0x1f}) {
		t.Fatalf("all mask = %#x", got.AllMask)
	}
	if !reflect.DeepEqual(got.WildcardMasks, []uint64{0x0d, 0x1d, 0x1a}) {
		t.Fatalf("wildcard masks = %#x", got.WildcardMasks)
	}
	if !reflect.DeepEqual(got.ValueMasks, []uint64{0x0f, 0x1d, 0x1d, 0x1f, 0x1b, 0x1e}) {
		t.Fatalf("value masks = %#x", got.ValueMasks)
	}

	var reordered Policy
	if err := builder.Build(&reordered, 5, reversedPolicyConstraints()); err != nil {
		t.Fatalf("reordered Build: %v", err)
	}
	if !reflect.DeepEqual(reordered, got) {
		t.Fatalf("input order changed Policy:\n got  %+v\n want %+v", reordered, got)
	}
}

func TestPolicyBuildClearsTailBits(t *testing.T) {
	constraints := Constraints{
		Rows:        []uint32{64},
		Fields:      []schema.FieldID{testTrustField},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		Values:      []schema.SymbolID{10},
	}
	var builder PolicyBuilder
	var got Policy
	if err := builder.Build(&got, 65, constraints); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AllMask, []uint64{math.MaxUint64, 1}) ||
		!reflect.DeepEqual(got.WildcardMasks, []uint64{math.MaxUint64, 0}) ||
		!reflect.DeepEqual(got.ValueMasks, []uint64{math.MaxUint64, 1}) {
		t.Fatalf("65-row masks = all %#x wildcard %#x value %#x", got.AllMask, got.WildcardMasks, got.ValueMasks)
	}
}

func TestPolicyBuildCloneOwnsExactStorage(t *testing.T) {
	var builder PolicyBuilder
	var src Policy
	if err := builder.Build(&src, 5, policyConstraints()); err != nil {
		t.Fatal(err)
	}
	clone := src.Clone()
	if !reflect.DeepEqual(clone, src) {
		t.Fatalf("Clone differs: %+v / %+v", clone, src)
	}
	assertPolicyExactAndDistinct(t, &clone, &src)
	if err := builder.Build(&src, 0, Constraints{}); err != nil {
		t.Fatal(err)
	}
	if clone.RequirementCount != 5 || !reflect.DeepEqual(clone.AllMask, []uint64{0x1f}) {
		t.Fatalf("source rebuild changed clone: %+v", clone)
	}
	empty := src.Clone()
	if empty.FieldIDs != nil || empty.AllMask != nil || empty.Values != nil {
		t.Fatalf("empty Clone has nonnil storage: %+v", empty)
	}
}

func assertPolicyExactAndDistinct(t *testing.T, clone, src *Policy) {
	t.Helper()
	cloneValue := reflect.ValueOf(clone).Elem()
	srcValue := reflect.ValueOf(src).Elem()
	for i := 0; i < cloneValue.NumField(); i++ {
		field := cloneValue.Field(i)
		if field.Kind() != reflect.Slice || field.Len() == 0 {
			continue
		}
		if field.Len() != field.Cap() {
			t.Fatalf("%s len/cap = %d/%d", cloneValue.Type().Field(i).Name, field.Len(), field.Cap())
		}
		if field.Pointer() == srcValue.Field(i).Pointer() {
			t.Fatalf("%s borrows source storage", cloneValue.Type().Field(i).Name)
		}
	}
}

func TestPolicyBuildRejectsMalformedAtomically(t *testing.T) {
	var builder PolicyBuilder
	var dst Policy
	if err := builder.Build(&dst, 5, policyConstraints()); err != nil {
		t.Fatal(err)
	}
	want := dst.Clone()
	tests := []struct {
		name        string
		constraints func() Constraints
	}{
		{"misaligned rows", func() Constraints { c := policyConstraints(); c.Rows = c.Rows[:4]; return c }},
		{"bad CSR range", func() Constraints { c := policyConstraints(); c.ValueStarts[0] = math.MaxUint32; return c }},
		{"zero field", func() Constraints { c := policyConstraints(); c.Fields[0] = 0; return c }},
		{"zero value", func() Constraints { c := policyConstraints(); c.Values[0] = 0; return c }},
		{"row out of range", func() Constraints { c := policyConstraints(); c.Rows[0] = 5; return c }},
		{"duplicate field row", func() Constraints {
			return Constraints{
				Rows:        []uint32{1, 1},
				Fields:      []schema.FieldID{testActionField, testActionField},
				ValueStarts: []uint32{0, 1},
				ValueCounts: []uint32{1, 1},
				Values:      []schema.SymbolID{20, 21},
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := builder.Build(&dst, 5, tt.constraints()); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("Build error = %v, want %v", err, ErrInvalidPolicy)
			}
			if !reflect.DeepEqual(dst, want) {
				t.Fatalf("failed Build changed destination: %+v", dst)
			}
		})
	}
	if err := builder.Build(nil, 0, Constraints{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil destination error = %v", err)
	}
	var nilBuilder *PolicyBuilder
	if err := nilBuilder.Build(&dst, 0, Constraints{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil builder error = %v", err)
	}
}
