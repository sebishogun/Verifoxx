package result

import (
	"math"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

var (
	templateLookupSink Template
	templateLookupOK   bool
)

func runtimeTemplateFixture() TemplateTable {
	return TemplateTable{
		LiteralBytes:  []byte("hello !fixed"),
		OpStarts:      []uint32{0, 3},
		OpCounts:      []uint16{3, 1},
		LiteralStarts: []uint32{0, 7},
		MaxBytes:      []uint32{32, 5},
		Ops: []TemplateOp{
			TemplateOpLiteral, TemplateOpRequestID, TemplateOpLiteral,
			TemplateOpLiteral,
		},
		Args: []uint32{6, 0, 1, 5},
	}
}

func TestTemplateOpClosedSet(t *testing.T) {
	if TemplateOpInvalid.Valid() {
		t.Fatal("invalid operation reported valid")
	}
	valid := 0
	for value := 0; value < 256; value++ {
		if TemplateOp(value).Valid() {
			valid++
		}
	}
	if valid != int(TemplateOpEvidenceID-TemplateOpLiteral+1) {
		t.Fatalf("valid operation count = %d", valid)
	}
}

func TestTemplateTableLookup(t *testing.T) {
	table := runtimeTemplateFixture()
	if err := table.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	first, ok := table.Lookup(1)
	if !ok {
		t.Fatal("Lookup(1) failed")
	}
	if !slices.Equal(first.Ops, table.Ops[:3]) || !slices.Equal(first.Args, table.Args[:3]) ||
		!slices.Equal(first.LiteralBytes, []byte("hello !")) || first.MaxBytes != 32 {
		t.Fatalf("Lookup(1) = %+v", first)
	}
	if &first.Ops[0] != &table.Ops[0] || &first.Args[0] != &table.Args[0] || &first.LiteralBytes[0] != &table.LiteralBytes[0] {
		t.Fatal("Lookup copied borrowed storage")
	}
	second, ok := table.Lookup(2)
	if !ok || !slices.Equal(second.LiteralBytes, []byte("fixed")) || second.MaxBytes != 5 {
		t.Fatalf("Lookup(2) = %+v, %v", second, ok)
	}
	for _, id := range []schema.TemplateID{0, 3, math.MaxUint32} {
		if got, ok := table.Lookup(id); ok || got.Ops != nil || got.Args != nil || got.LiteralBytes != nil || got.MaxBytes != 0 {
			t.Fatalf("Lookup(%d) = %+v, %v", id, got, ok)
		}
	}
}

func TestTemplateTableRejectsMalformed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TemplateTable)
	}{
		{"op starts", func(table *TemplateTable) { table.OpStarts = table.OpStarts[:1] }},
		{"op counts", func(table *TemplateTable) { table.OpCounts = table.OpCounts[:1] }},
		{"literal starts", func(table *TemplateTable) { table.LiteralStarts = table.LiteralStarts[:1] }},
		{"max bytes", func(table *TemplateTable) { table.MaxBytes = table.MaxBytes[:1] }},
		{"arguments", func(table *TemplateTable) { table.Args = table.Args[:3] }},
		{"operation range", func(table *TemplateTable) { table.OpStarts[0] = math.MaxUint32 }},
		{"operation overlap", func(table *TemplateTable) { table.OpStarts[1] = 2 }},
		{"too many operations", func(table *TemplateTable) { table.OpCounts[0] = MaxTemplateOperations + 1 }},
		{"invalid operation", func(table *TemplateTable) { table.Ops[0] = TemplateOpInvalid }},
		{"placeholder argument", func(table *TemplateTable) { table.Args[1] = 1 }},
		{"literal range", func(table *TemplateTable) { table.LiteralStarts[0] = math.MaxUint32 }},
		{"literal overlap", func(table *TemplateTable) { table.LiteralStarts[1] = 6 }},
		{"max below literals", func(table *TemplateTable) { table.MaxBytes[0] = 6 }},
		{"rendered maximum", func(table *TemplateTable) { table.MaxBytes[0] = MaxRenderedTemplateBytes + 1 }},
		{"dangling operation", func(table *TemplateTable) {
			table.Ops = append(table.Ops, TemplateOpRequestID)
			table.Args = append(table.Args, 0)
		}},
		{"dangling literal", func(table *TemplateTable) { table.LiteralBytes = append(table.LiteralBytes, 'x') }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := runtimeTemplateFixture()
			test.mutate(&table)
			if err := table.Validate(); err != ErrInvalidTemplateTable {
				t.Fatalf("Validate = %v, want %v", err, ErrInvalidTemplateTable)
			}
		})
	}
}

func TestTemplateTableEmptyAlignedValid(t *testing.T) {
	var table TemplateTable
	if err := table.Validate(); err != nil {
		t.Fatalf("empty Validate = %v", err)
	}
}

func TestTemplateTableWarmOperationsAllocateZero(t *testing.T) {
	table := runtimeTemplateFixture()
	if err := table.Validate(); err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		templateLookupSink, templateLookupOK = table.Lookup(1)
	}); allocs != 0 {
		t.Fatalf("Lookup allocations = %g", allocs)
	}
	if !templateLookupOK {
		t.Fatal("warm Lookup failed")
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		if table.Validate() != nil {
			templateLookupOK = false
		}
	}); allocs != 0 {
		t.Fatalf("Validate allocations = %g", allocs)
	}
}

func runtimeExplanationFixture() (TemplateTable, ExplanationTable) {
	templates := runtimeTemplateFixture()
	return templates, ExplanationTable{
		RationaleTemplateIDs:   []schema.TemplateID{1, 2},
		UncertaintyStarts:      []uint32{0, 1},
		UncertaintyCounts:      []uint16{1, 0},
		UncertaintyTemplateIDs: []schema.TemplateID{2},
		AssumptionTemplateIDs:  []schema.TemplateID{2},
	}
}

func TestExplanationTableLookup(t *testing.T) {
	templates, table := runtimeExplanationFixture()
	if err := table.Validate(&templates); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	first, ok := table.Lookup(1)
	if !ok || first.Rationale != 1 || !slices.Equal(first.Uncertainty, []schema.TemplateID{2}) {
		t.Fatalf("Lookup(1) = %+v, %v", first, ok)
	}
	if &first.Uncertainty[0] != &table.UncertaintyTemplateIDs[0] {
		t.Fatal("Lookup copied uncertainty storage")
	}
	if assumptions := table.Assumptions(); !slices.Equal(assumptions, []schema.TemplateID{2}) ||
		&assumptions[0] != &table.AssumptionTemplateIDs[0] {
		t.Fatalf("Assumptions = %v", assumptions)
	}
	for _, id := range []schema.ExplanationID{0, 3, math.MaxUint32} {
		if got, ok := table.Lookup(id); ok || got.Rationale != 0 || got.Uncertainty != nil {
			t.Fatalf("Lookup(%d) = %+v, %v", id, got, ok)
		}
	}
}

func TestExplanationTableRejectsMalformed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExplanationTable)
		want   error
	}{
		{"empty", func(table *ExplanationTable) {
			table.RationaleTemplateIDs = nil
			table.UncertaintyStarts = nil
			table.UncertaintyCounts = nil
		}, ErrInvalidExplanationTable},
		{"starts", func(table *ExplanationTable) { table.UncertaintyStarts = table.UncertaintyStarts[:1] }, ErrInvalidExplanationTable},
		{"counts", func(table *ExplanationTable) { table.UncertaintyCounts = table.UncertaintyCounts[:1] }, ErrInvalidExplanationTable},
		{"range", func(table *ExplanationTable) { table.UncertaintyStarts[0] = math.MaxUint32 }, ErrInvalidExplanationTable},
		{"range overlap", func(table *ExplanationTable) { table.UncertaintyStarts[1] = 0 }, ErrInvalidExplanationTable},
		{"too many uncertainty", func(table *ExplanationTable) { table.UncertaintyCounts[0] = MaxUncertaintyTemplates + 1 }, ErrInvalidExplanationTable},
		{"dangling uncertainty", func(table *ExplanationTable) { table.UncertaintyTemplateIDs = append(table.UncertaintyTemplateIDs, 2) }, ErrInvalidExplanationTable},
		{"zero rationale", func(table *ExplanationTable) { table.RationaleTemplateIDs[0] = 0 }, ErrInvalidTemplateReference},
		{"high rationale", func(table *ExplanationTable) { table.RationaleTemplateIDs[0] = 3 }, ErrInvalidTemplateReference},
		{"zero uncertainty", func(table *ExplanationTable) { table.UncertaintyTemplateIDs[0] = 0 }, ErrInvalidTemplateReference},
		{"high uncertainty", func(table *ExplanationTable) { table.UncertaintyTemplateIDs[0] = 3 }, ErrInvalidTemplateReference},
		{"zero assumption", func(table *ExplanationTable) { table.AssumptionTemplateIDs[0] = 0 }, ErrInvalidTemplateReference},
		{"high assumption", func(table *ExplanationTable) { table.AssumptionTemplateIDs[0] = 3 }, ErrInvalidTemplateReference},
		{"too many assumptions", func(table *ExplanationTable) {
			table.AssumptionTemplateIDs = []schema.TemplateID{1, 1, 1, 1, 1, 1, 1, 1, 1}
		}, ErrInvalidExplanationTable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			templates, table := runtimeExplanationFixture()
			test.mutate(&table)
			if err := table.Validate(&templates); err != test.want {
				t.Fatalf("Validate = %v, want %v", err, test.want)
			}
		})
	}
}

func TestExplanationTableRejectsCombinedExpansion(t *testing.T) {
	templates := TemplateTable{
		OpStarts:      make([]uint32, 5),
		OpCounts:      make([]uint16, 5),
		LiteralStarts: make([]uint32, 5),
		MaxBytes:      []uint32{1024, 1024, 1024, 1024, 1024},
	}
	if err := templates.Validate(); err != nil {
		t.Fatalf("template fixture: %v", err)
	}
	table := ExplanationTable{
		RationaleTemplateIDs:   []schema.TemplateID{1},
		UncertaintyStarts:      []uint32{0},
		UncertaintyCounts:      []uint16{4},
		UncertaintyTemplateIDs: []schema.TemplateID{2, 3, 4, 5},
	}
	if err := table.Validate(&templates); err != ErrInvalidExplanationTable {
		t.Fatalf("Validate = %v, want %v", err, ErrInvalidExplanationTable)
	}
}

func TestExplanationTableRejectsInvalidTemplateTable(t *testing.T) {
	templates, table := runtimeExplanationFixture()
	templates.Args[1] = 1
	if err := table.Validate(&templates); err != ErrInvalidTemplateTable {
		t.Fatalf("Validate = %v, want %v", err, ErrInvalidTemplateTable)
	}
}
