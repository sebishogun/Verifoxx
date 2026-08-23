package program

import (
	"reflect"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func installProgramExplanationFixture(p *Program) {
	p.TemplateBytes = []byte("x")
	p.TemplateOpStarts = []uint32{0}
	p.TemplateOpCounts = []uint16{1}
	p.TemplateLiteralStarts = []uint32{0}
	p.TemplateMaxBytes = []uint32{1}
	p.TemplateOps = []result.TemplateOp{result.TemplateOpLiteral}
	p.TemplateArgs = []uint32{1}
	p.ExplanationRationaleTemplateIDs = []schema.TemplateID{1}
	p.ExplanationUncertaintyStarts = []uint32{0}
	p.ExplanationUncertaintyCounts = []uint16{1}
	p.ExplanationUncertaintyTemplateIDs = []schema.TemplateID{1}
	p.AssumptionTemplateIDs = []schema.TemplateID{1}
	p.Resolutions.ExplanationIDs = make([]schema.ExplanationID, len(p.Resolutions.OutcomeIDs))
	for i := range p.Resolutions.ExplanationIDs {
		p.Resolutions.ExplanationIDs[i] = 1
	}
}

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
	installProgramExplanationFixture(&src)
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
	installProgramExplanationFixture(&src)
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
	src.FactIndexSpec = policyindex.FactSpec{
		FieldIDs:    []schema.FieldID{1},
		Columns:     []uint32{0},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		UseCounts:   []uint32{96},
		Values:      []schema.SymbolID{10},
	}
	frozen, err := Freeze(&src)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	if !reflect.DeepEqual(frozen.FieldIndex, src.FieldIndex) ||
		!reflect.DeepEqual(frozen.ApplicabilityIndex, src.ApplicabilityIndex) ||
		!reflect.DeepEqual(frozen.FactIndexSpec, src.FactIndexSpec) {
		t.Fatalf("frozen indexes differ:\n got  %+v / %+v / %+v\n want %+v / %+v / %+v",
			frozen.FieldIndex, frozen.ApplicabilityIndex, frozen.FactIndexSpec,
			src.FieldIndex, src.ApplicabilityIndex, src.FactIndexSpec)
	}
	assertFrozenIndexStorage(t, reflect.ValueOf(frozen.FieldIndex), reflect.ValueOf(src.FieldIndex))
	assertFrozenIndexStorage(t, reflect.ValueOf(frozen.ApplicabilityIndex), reflect.ValueOf(src.ApplicabilityIndex))
	assertFrozenIndexStorage(t, reflect.ValueOf(frozen.FactIndexSpec), reflect.ValueOf(src.FactIndexSpec))
	src.FieldIndex.Kinds[0] = schema.ValueKindBoolean
	src.ApplicabilityIndex.AllMask[0] = 0
	src.FactIndexSpec.Values[0] = 11
	if frozen.FieldIndex.Kinds[0] != schema.ValueKindSymbol || frozen.ApplicabilityIndex.AllMask[0] != 3 ||
		frozen.FactIndexSpec.Values[0] != 10 {
		t.Fatal("source mutation changed frozen indexes")
	}
}

func TestFreezeCopiesAndRebindsExplanationStorage(t *testing.T) {
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
		EvidenceIssueNodeIDs:         []schema.NodeID{4},
		EvidenceIssueTemplateIDs:     []schema.TemplateID{1, 1, 1, 1, 1, 1, 1, 1, 1},
		RequirementSourceNodeIDs:     []schema.NodeID{2},
		ClauseAssertionSourceNodeIDs: []schema.NodeID{3},
		ClauseEvidenceSourceNodeIDs:  []schema.NodeID{4},
		ClauseExplanationIDs:         []schema.ExplanationID{1, 1, 1, 1, 1, 1, 1},
	}
	installProgramExplanationFixture(&src)
	frozen, err := Freeze(&src)
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	ownerFields := []string{
		"TemplateBytes", "TemplateOpStarts", "TemplateOpCounts", "TemplateLiteralStarts",
		"TemplateMaxBytes", "TemplateOps", "TemplateArgs", "ExplanationRationaleTemplateIDs",
		"ExplanationUncertaintyStarts", "ExplanationUncertaintyCounts", "ExplanationUncertaintyTemplateIDs",
		"AssumptionTemplateIDs",
		"EvidenceIssueNodeIDs", "EvidenceIssueTemplateIDs", "RequirementSourceNodeIDs", "ClauseAssertionSourceNodeIDs",
		"ClauseEvidenceSourceNodeIDs", "ClauseExplanationIDs",
	}
	frozenValue, srcValue := reflect.ValueOf(frozen), reflect.ValueOf(src)
	for _, name := range ownerFields {
		got := frozenValue.FieldByName(name)
		original := srcValue.FieldByName(name)
		if !got.IsValid() || got.Kind() != reflect.Slice || got.Len() == 0 {
			t.Fatalf("%s missing or empty", name)
		}
		if got.Len() != got.Cap() {
			t.Fatalf("%s len/cap = %d/%d", name, got.Len(), got.Cap())
		}
		if got.Pointer() == original.Pointer() {
			t.Fatalf("%s borrows source storage", name)
		}
	}
	if &frozen.Templates.LiteralBytes[0] != &frozen.TemplateBytes[0] ||
		&frozen.Templates.Ops[0] != &frozen.TemplateOps[0] ||
		&frozen.Explanations.RationaleTemplateIDs[0] != &frozen.ExplanationRationaleTemplateIDs[0] ||
		&frozen.Explanations.AssumptionTemplateIDs[0] != &frozen.AssumptionTemplateIDs[0] {
		t.Fatal("result table views were not rebound to frozen owner columns")
	}
	if &frozen.Resolutions.ExplanationIDs[0] == &src.Resolutions.ExplanationIDs[0] {
		t.Fatal("resolution explanation IDs borrow source storage")
	}
	src.TemplateBytes[0] = 'z'
	src.RequirementSourceNodeIDs[0] = 99
	if string(frozen.TemplateBytes) != "x" || frozen.RequirementSourceNodeIDs[0] != 2 {
		t.Fatal("source mutation changed frozen explanation storage")
	}
}

func TestValidateResultTablesClearsViewsOnFailure(t *testing.T) {
	outcomes := make([]schema.OutcomeID, truth.ReasonCount)
	for i := range outcomes {
		outcomes[i] = 1
	}
	p := Program{
		Outcomes: result.OutcomeTable{Names: []schema.SymbolID{1}, Precedence: []uint8{1}, Terminal: []bool{true}},
		Resolutions: result.ResolutionTable{
			OutcomeIDs: outcomes, RemediationStarts: make([]uint32, truth.ReasonCount), RemediationCounts: make([]uint16, truth.ReasonCount),
		},
	}
	installProgramExplanationFixture(&p)
	if err := p.ValidateResultTables(); err != nil {
		t.Fatalf("initial ValidateResultTables: %v", err)
	}
	p.TemplateMaxBytes[0] = result.MaxRenderedTemplateBytes + 1
	if err := p.ValidateResultTables(); err != result.ErrInvalidTemplateTable {
		t.Fatalf("invalid ValidateResultTables = %v", err)
	}
	if _, ok := p.Templates.Lookup(1); ok {
		t.Fatal("failed validation retained template view")
	}
	if _, ok := p.Explanations.Lookup(1); ok {
		t.Fatal("failed validation retained explanation view")
	}
}

func TestValidateResultTablesRejectsResolutionExplanationReference(t *testing.T) {
	outcomes := make([]schema.OutcomeID, truth.ReasonCount)
	for i := range outcomes {
		outcomes[i] = 1
	}
	p := Program{
		Outcomes: result.OutcomeTable{Names: []schema.SymbolID{1}, Precedence: []uint8{1}, Terminal: []bool{true}},
		Resolutions: result.ResolutionTable{
			OutcomeIDs: outcomes, RemediationStarts: make([]uint32, truth.ReasonCount), RemediationCounts: make([]uint16, truth.ReasonCount),
		},
	}
	installProgramExplanationFixture(&p)
	p.Resolutions.ExplanationIDs[0] = 2
	if err := p.ValidateResultTables(); err != result.ErrInvalidExplanationReference {
		t.Fatalf("ValidateResultTables = %v, want %v", err, result.ErrInvalidExplanationReference)
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
