package program

import (
	"reflect"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func TestFreezeRejectsInvalidResultTables(t *testing.T) {
	if _, err := Freeze(&Program{}); err == nil {
		t.Fatal("Freeze accepted empty result tables")
	}
}

func TestFreezeCopiesSlotPlan(t *testing.T) {
	outcomes := make([]schema.OutcomeID, truth.ReasonCount)
	for i := range outcomes {
		outcomes[i] = 1
	}
	src := Program{
		TruthSlots:      []schema.SlotID{1, 1},
		ReasonSlots:     []schema.SlotID{1, 1},
		TruthSlotCount:  1,
		ReasonSlotCount: 1,
		Outcomes: result.OutcomeTable{
			Names:      []schema.SymbolID{1},
			Precedence: []uint8{1},
			Terminal:   []bool{true},
		},
		Resolutions: result.ResolutionTable{
			OutcomeIDs:        outcomes,
			RemediationStarts: make([]uint32, truth.ReasonCount),
			RemediationCounts: make([]uint16, truth.ReasonCount),
		},
	}
	frozen, err := Freeze(&src)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if !reflect.DeepEqual(frozen.TruthSlots, src.TruthSlots) ||
		!reflect.DeepEqual(frozen.ReasonSlots, src.ReasonSlots) ||
		frozen.TruthSlotCount != src.TruthSlotCount || frozen.ReasonSlotCount != src.ReasonSlotCount {
		t.Fatalf("frozen slots = %v/%v counts %d/%d",
			frozen.TruthSlots, frozen.ReasonSlots, frozen.TruthSlotCount, frozen.ReasonSlotCount)
	}
	if len(frozen.TruthSlots) != cap(frozen.TruthSlots) || len(frozen.ReasonSlots) != cap(frozen.ReasonSlots) {
		t.Fatalf("frozen slot capacities = %d/%d and %d/%d",
			len(frozen.TruthSlots), cap(frozen.TruthSlots), len(frozen.ReasonSlots), cap(frozen.ReasonSlots))
	}
	if &frozen.TruthSlots[0] == &src.TruthSlots[0] || &frozen.ReasonSlots[0] == &src.ReasonSlots[0] {
		t.Fatal("frozen slot columns borrow source storage")
	}
	src.TruthSlots[0] = 2
	src.ReasonSlots[0] = 2
	if frozen.TruthSlots[0] != 1 || frozen.ReasonSlots[0] != 1 {
		t.Fatal("source mutation changed frozen slot columns")
	}
}

func TestFreezeCopiesIndexes(t *testing.T) {
	outcomes := make([]schema.OutcomeID, truth.ReasonCount)
	for i := range outcomes {
		outcomes[i] = 1
	}
	src := Program{
		Outcomes: result.OutcomeTable{
			Names:      []schema.SymbolID{1},
			Precedence: []uint8{1},
			Terminal:   []bool{true},
		},
		Resolutions: result.ResolutionTable{
			OutcomeIDs:        outcomes,
			RemediationStarts: make([]uint32, truth.ReasonCount),
			RemediationCounts: make([]uint16, truth.ReasonCount),
		},
	}
	if err := policyindex.BuildSchema(&src.FieldIndex, []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
	}); err != nil {
		t.Fatal(err)
	}
	var builder policyindex.PolicyBuilder
	if err := builder.Build(&src.ApplicabilityIndex, 2, policyindex.Constraints{
		Rows:        []uint32{0},
		Fields:      []schema.FieldID{1},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		Values:      []schema.SymbolID{10},
	}); err != nil {
		t.Fatal(err)
	}
	frozen, err := Freeze(&src)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if !reflect.DeepEqual(frozen.FieldIndex, src.FieldIndex) ||
		!reflect.DeepEqual(frozen.ApplicabilityIndex, src.ApplicabilityIndex) {
		t.Fatalf("frozen indexes differ:\n got  %+v / %+v\n want %+v / %+v",
			frozen.FieldIndex, frozen.ApplicabilityIndex, src.FieldIndex, src.ApplicabilityIndex)
	}
	assertFrozenIndexStorage(t, reflect.ValueOf(frozen.FieldIndex), reflect.ValueOf(src.FieldIndex))
	assertFrozenIndexStorage(t, reflect.ValueOf(frozen.ApplicabilityIndex), reflect.ValueOf(src.ApplicabilityIndex))
	src.FieldIndex.Kinds[0] = schema.ValueKindBoolean
	src.ApplicabilityIndex.AllMask[0] = 0
	if frozen.FieldIndex.Kinds[0] != schema.ValueKindSymbol || frozen.ApplicabilityIndex.AllMask[0] != 3 {
		t.Fatal("source mutation changed frozen indexes")
	}
}

func assertFrozenIndexStorage(t *testing.T, frozen, src reflect.Value) {
	t.Helper()
	typ := frozen.Type()
	for i := 0; i < frozen.NumField(); i++ {
		field := frozen.Field(i)
		if field.Kind() != reflect.Slice || field.Len() == 0 {
			continue
		}
		if field.Len() != field.Cap() {
			t.Fatalf("%s len/cap = %d/%d", typ.Field(i).Name, field.Len(), field.Cap())
		}
		if field.Pointer() == src.Field(i).Pointer() {
			t.Fatalf("%s borrows source storage", typ.Field(i).Name)
		}
	}
}
