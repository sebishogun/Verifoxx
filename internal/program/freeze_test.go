package program

import (
	"reflect"
	"testing"

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
