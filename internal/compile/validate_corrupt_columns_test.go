package compile

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// columnCase names one public slice column of ast.Document and the typed
// truncation that replaces exactly that slice header in dst with the first cut
// elements of the same column in src. full is the baseline column length.
type columnCase struct {
	name  string
	full  int
	trunc func(dst, src *ast.Document, cut int)
}

// corruptColumnCases enumerates every public slice column of ast.Document in
// declaration order. The expected total is 84; the test fails setup if the
// table ever drifts from that count.
func corruptColumnCases(baseline *ast.Document) []columnCase {
	return []columnCase{
		{"NodeKinds", len(baseline.NodeKinds), func(d, s *ast.Document, cut int) { d.NodeKinds = s.NodeKinds[:cut] }},
		{"NodeRefs", len(baseline.NodeRefs), func(d, s *ast.Document, cut int) { d.NodeRefs = s.NodeRefs[:cut] }},

		{"TemplateBytes", len(baseline.TemplateBytes), func(d, s *ast.Document, cut int) { d.TemplateBytes = s.TemplateBytes[:cut] }},
		{"TemplateOpStarts", len(baseline.TemplateOpStarts), func(d, s *ast.Document, cut int) { d.TemplateOpStarts = s.TemplateOpStarts[:cut] }},
		{"TemplateOpCounts", len(baseline.TemplateOpCounts), func(d, s *ast.Document, cut int) { d.TemplateOpCounts = s.TemplateOpCounts[:cut] }},
		{"TemplateLiteralStarts", len(baseline.TemplateLiteralStarts), func(d, s *ast.Document, cut int) { d.TemplateLiteralStarts = s.TemplateLiteralStarts[:cut] }},
		{"TemplateMaxBytes", len(baseline.TemplateMaxBytes), func(d, s *ast.Document, cut int) { d.TemplateMaxBytes = s.TemplateMaxBytes[:cut] }},
		{"TemplateContexts", len(baseline.TemplateContexts), func(d, s *ast.Document, cut int) { d.TemplateContexts = s.TemplateContexts[:cut] }},
		{"TemplateOps", len(baseline.TemplateOps), func(d, s *ast.Document, cut int) { d.TemplateOps = s.TemplateOps[:cut] }},
		{"TemplateArgs", len(baseline.TemplateArgs), func(d, s *ast.Document, cut int) { d.TemplateArgs = s.TemplateArgs[:cut] }},
		{"AssumptionTemplateIDs", len(baseline.AssumptionTemplateIDs), func(d, s *ast.Document, cut int) { d.AssumptionTemplateIDs = s.AssumptionTemplateIDs[:cut] }},
		{"AssumptionsSet", len(baseline.AssumptionsSet), func(d, s *ast.Document, cut int) { d.AssumptionsSet = s.AssumptionsSet[:cut] }},

		{"ExplanationRationaleIDs", len(baseline.ExplanationRationaleIDs), func(d, s *ast.Document, cut int) { d.ExplanationRationaleIDs = s.ExplanationRationaleIDs[:cut] }},
		{"ExplanationUncertaintyStarts", len(baseline.ExplanationUncertaintyStarts), func(d, s *ast.Document, cut int) {
			d.ExplanationUncertaintyStarts = s.ExplanationUncertaintyStarts[:cut]
		}},
		{"ExplanationUncertaintyCounts", len(baseline.ExplanationUncertaintyCounts), func(d, s *ast.Document, cut int) {
			d.ExplanationUncertaintyCounts = s.ExplanationUncertaintyCounts[:cut]
		}},
		{"ExplanationUncertaintyIDs", len(baseline.ExplanationUncertaintyIDs), func(d, s *ast.Document, cut int) { d.ExplanationUncertaintyIDs = s.ExplanationUncertaintyIDs[:cut] }},

		{"CompareFields", len(baseline.CompareFields), func(d, s *ast.Document, cut int) { d.CompareFields = s.CompareFields[:cut] }},
		{"CompareOps", len(baseline.CompareOps), func(d, s *ast.Document, cut int) { d.CompareOps = s.CompareOps[:cut] }},
		{"CompareValues", len(baseline.CompareValues), func(d, s *ast.Document, cut int) { d.CompareValues = s.CompareValues[:cut] }},
		{"CompareListStarts", len(baseline.CompareListStarts), func(d, s *ast.Document, cut int) { d.CompareListStarts = s.CompareListStarts[:cut] }},
		{"CompareListCounts", len(baseline.CompareListCounts), func(d, s *ast.Document, cut int) { d.CompareListCounts = s.CompareListCounts[:cut] }},
		{"ListValueIDs", len(baseline.ListValueIDs), func(d, s *ast.Document, cut int) { d.ListValueIDs = s.ListValueIDs[:cut] }},

		{"GroupChildStarts", len(baseline.GroupChildStarts), func(d, s *ast.Document, cut int) { d.GroupChildStarts = s.GroupChildStarts[:cut] }},
		{"GroupChildCounts", len(baseline.GroupChildCounts), func(d, s *ast.Document, cut int) { d.GroupChildCounts = s.GroupChildCounts[:cut] }},
		{"ChildNodeIDs", len(baseline.ChildNodeIDs), func(d, s *ast.Document, cut int) { d.ChildNodeIDs = s.ChildNodeIDs[:cut] }},

		{"NotChildren", len(baseline.NotChildren), func(d, s *ast.Document, cut int) { d.NotChildren = s.NotChildren[:cut] }},

		{"EvidenceKinds", len(baseline.EvidenceKinds), func(d, s *ast.Document, cut int) { d.EvidenceKinds = s.EvidenceKinds[:cut] }},
		{"EvidenceStates", len(baseline.EvidenceStates), func(d, s *ast.Document, cut int) { d.EvidenceStates = s.EvidenceStates[:cut] }},
		{"EvidenceSubjects", len(baseline.EvidenceSubjects), func(d, s *ast.Document, cut int) { d.EvidenceSubjects = s.EvidenceSubjects[:cut] }},
		{"EvidenceScopes", len(baseline.EvidenceScopes), func(d, s *ast.Document, cut int) { d.EvidenceScopes = s.EvidenceScopes[:cut] }},
		{"EvidenceTimings", len(baseline.EvidenceTimings), func(d, s *ast.Document, cut int) { d.EvidenceTimings = s.EvidenceTimings[:cut] }},
		{"EvidenceIssueTemplateIDs", len(baseline.EvidenceIssueTemplateIDs), func(d, s *ast.Document, cut int) { d.EvidenceIssueTemplateIDs = s.EvidenceIssueTemplateIDs[:cut] }},

		{"SourceStarts", len(baseline.SourceStarts), func(d, s *ast.Document, cut int) { d.SourceStarts = s.SourceStarts[:cut] }},
		{"SourceEnds", len(baseline.SourceEnds), func(d, s *ast.Document, cut int) { d.SourceEnds = s.SourceEnds[:cut] }},
		{"InputBytes", len(baseline.InputBytes), func(d, s *ast.Document, cut int) { d.InputBytes = s.InputBytes[:cut] }},

		{"ValueKinds", len(baseline.ValueKinds), func(d, s *ast.Document, cut int) { d.ValueKinds = s.ValueKinds[:cut] }},
		{"ValueRefs", len(baseline.ValueRefs), func(d, s *ast.Document, cut int) { d.ValueRefs = s.ValueRefs[:cut] }},
		{"SymbolStarts", len(baseline.SymbolStarts), func(d, s *ast.Document, cut int) { d.SymbolStarts = s.SymbolStarts[:cut] }},
		{"SymbolLengths", len(baseline.SymbolLengths), func(d, s *ast.Document, cut int) { d.SymbolLengths = s.SymbolLengths[:cut] }},
		{"SymbolBytes", len(baseline.SymbolBytes), func(d, s *ast.Document, cut int) { d.SymbolBytes = s.SymbolBytes[:cut] }},
		{"IntegerValues", len(baseline.IntegerValues), func(d, s *ast.Document, cut int) { d.IntegerValues = s.IntegerValues[:cut] }},
		{"BooleanValues", len(baseline.BooleanValues), func(d, s *ast.Document, cut int) { d.BooleanValues = s.BooleanValues[:cut] }},
		{"TimestampValues", len(baseline.TimestampValues), func(d, s *ast.Document, cut int) { d.TimestampValues = s.TimestampValues[:cut] }},

		{"EvidenceKindNames", len(baseline.EvidenceKindNames), func(d, s *ast.Document, cut int) { d.EvidenceKindNames = s.EvidenceKindNames[:cut] }},
		{"EvidenceKindSourceStarts", len(baseline.EvidenceKindSourceStarts), func(d, s *ast.Document, cut int) { d.EvidenceKindSourceStarts = s.EvidenceKindSourceStarts[:cut] }},
		{"EvidenceKindSourceEnds", len(baseline.EvidenceKindSourceEnds), func(d, s *ast.Document, cut int) { d.EvidenceKindSourceEnds = s.EvidenceKindSourceEnds[:cut] }},
		{"EvidenceStateNames", len(baseline.EvidenceStateNames), func(d, s *ast.Document, cut int) { d.EvidenceStateNames = s.EvidenceStateNames[:cut] }},
		{"EvidenceStateSourceStarts", len(baseline.EvidenceStateSourceStarts), func(d, s *ast.Document, cut int) { d.EvidenceStateSourceStarts = s.EvidenceStateSourceStarts[:cut] }},
		{"EvidenceStateSourceEnds", len(baseline.EvidenceStateSourceEnds), func(d, s *ast.Document, cut int) { d.EvidenceStateSourceEnds = s.EvidenceStateSourceEnds[:cut] }},

		{"OutcomeNames", len(baseline.OutcomeNames), func(d, s *ast.Document, cut int) { d.OutcomeNames = s.OutcomeNames[:cut] }},
		{"OutcomePrecedence", len(baseline.OutcomePrecedence), func(d, s *ast.Document, cut int) { d.OutcomePrecedence = s.OutcomePrecedence[:cut] }},
		{"OutcomeTerminal", len(baseline.OutcomeTerminal), func(d, s *ast.Document, cut int) { d.OutcomeTerminal = s.OutcomeTerminal[:cut] }},
		{"OutcomeSourceStarts", len(baseline.OutcomeSourceStarts), func(d, s *ast.Document, cut int) { d.OutcomeSourceStarts = s.OutcomeSourceStarts[:cut] }},
		{"OutcomeSourceEnds", len(baseline.OutcomeSourceEnds), func(d, s *ast.Document, cut int) { d.OutcomeSourceEnds = s.OutcomeSourceEnds[:cut] }},

		{"RemediationKinds", len(baseline.RemediationKinds), func(d, s *ast.Document, cut int) { d.RemediationKinds = s.RemediationKinds[:cut] }},
		{"RemediationFields", len(baseline.RemediationFields), func(d, s *ast.Document, cut int) { d.RemediationFields = s.RemediationFields[:cut] }},
		{"RemediationValues", len(baseline.RemediationValues), func(d, s *ast.Document, cut int) { d.RemediationValues = s.RemediationValues[:cut] }},
		{"RemediationEvidenceKinds", len(baseline.RemediationEvidenceKinds), func(d, s *ast.Document, cut int) { d.RemediationEvidenceKinds = s.RemediationEvidenceKinds[:cut] }},
		{"RemediationSourceStarts", len(baseline.RemediationSourceStarts), func(d, s *ast.Document, cut int) { d.RemediationSourceStarts = s.RemediationSourceStarts[:cut] }},
		{"RemediationSourceEnds", len(baseline.RemediationSourceEnds), func(d, s *ast.Document, cut int) { d.RemediationSourceEnds = s.RemediationSourceEnds[:cut] }},

		{"ClauseAssertionRoots", len(baseline.ClauseAssertionRoots), func(d, s *ast.Document, cut int) { d.ClauseAssertionRoots = s.ClauseAssertionRoots[:cut] }},
		{"ClauseEvidenceStarts", len(baseline.ClauseEvidenceStarts), func(d, s *ast.Document, cut int) { d.ClauseEvidenceStarts = s.ClauseEvidenceStarts[:cut] }},
		{"ClauseEvidenceCounts", len(baseline.ClauseEvidenceCounts), func(d, s *ast.Document, cut int) { d.ClauseEvidenceCounts = s.ClauseEvidenceCounts[:cut] }},
		{"ClauseEvidenceNodeIDs", len(baseline.ClauseEvidenceNodeIDs), func(d, s *ast.Document, cut int) { d.ClauseEvidenceNodeIDs = s.ClauseEvidenceNodeIDs[:cut] }},
		{"ClauseRemediationStarts", len(baseline.ClauseRemediationStarts), func(d, s *ast.Document, cut int) { d.ClauseRemediationStarts = s.ClauseRemediationStarts[:cut] }},
		{"ClauseRemediationCounts", len(baseline.ClauseRemediationCounts), func(d, s *ast.Document, cut int) { d.ClauseRemediationCounts = s.ClauseRemediationCounts[:cut] }},
		{"ClauseRemediationIDs", len(baseline.ClauseRemediationIDs), func(d, s *ast.Document, cut int) { d.ClauseRemediationIDs = s.ClauseRemediationIDs[:cut] }},
		{"ClauseOnSatisfied", len(baseline.ClauseOnSatisfied), func(d, s *ast.Document, cut int) { d.ClauseOnSatisfied = s.ClauseOnSatisfied[:cut] }},
		{"ClauseOnFalse", len(baseline.ClauseOnFalse), func(d, s *ast.Document, cut int) { d.ClauseOnFalse = s.ClauseOnFalse[:cut] }},
		{"ClauseOnMissing", len(baseline.ClauseOnMissing), func(d, s *ast.Document, cut int) { d.ClauseOnMissing = s.ClauseOnMissing[:cut] }},
		{"ClauseOnStale", len(baseline.ClauseOnStale), func(d, s *ast.Document, cut int) { d.ClauseOnStale = s.ClauseOnStale[:cut] }},
		{"ClauseOnUnclear", len(baseline.ClauseOnUnclear), func(d, s *ast.Document, cut int) { d.ClauseOnUnclear = s.ClauseOnUnclear[:cut] }},
		{"ClauseOnUnverifiable", len(baseline.ClauseOnUnverifiable), func(d, s *ast.Document, cut int) { d.ClauseOnUnverifiable = s.ClauseOnUnverifiable[:cut] }},
		{"ClauseOnConflict", len(baseline.ClauseOnConflict), func(d, s *ast.Document, cut int) { d.ClauseOnConflict = s.ClauseOnConflict[:cut] }},
		{"ClauseExplanationIDs", len(baseline.ClauseExplanationIDs), func(d, s *ast.Document, cut int) { d.ClauseExplanationIDs = s.ClauseExplanationIDs[:cut] }},
		{"ClauseSourceStarts", len(baseline.ClauseSourceStarts), func(d, s *ast.Document, cut int) { d.ClauseSourceStarts = s.ClauseSourceStarts[:cut] }},
		{"ClauseSourceEnds", len(baseline.ClauseSourceEnds), func(d, s *ast.Document, cut int) { d.ClauseSourceEnds = s.ClauseSourceEnds[:cut] }},

		{"RequirementIDs", len(baseline.RequirementIDs), func(d, s *ast.Document, cut int) { d.RequirementIDs = s.RequirementIDs[:cut] }},
		{"RequirementApplicabilityRoots", len(baseline.RequirementApplicabilityRoots), func(d, s *ast.Document, cut int) {
			d.RequirementApplicabilityRoots = s.RequirementApplicabilityRoots[:cut]
		}},
		{"RequirementClauseStarts", len(baseline.RequirementClauseStarts), func(d, s *ast.Document, cut int) { d.RequirementClauseStarts = s.RequirementClauseStarts[:cut] }},
		{"RequirementClauseCounts", len(baseline.RequirementClauseCounts), func(d, s *ast.Document, cut int) { d.RequirementClauseCounts = s.RequirementClauseCounts[:cut] }},
		{"RequirementClauseIDs", len(baseline.RequirementClauseIDs), func(d, s *ast.Document, cut int) { d.RequirementClauseIDs = s.RequirementClauseIDs[:cut] }},
		{"RequirementSourceStarts", len(baseline.RequirementSourceStarts), func(d, s *ast.Document, cut int) { d.RequirementSourceStarts = s.RequirementSourceStarts[:cut] }},
		{"RequirementSourceEnds", len(baseline.RequirementSourceEnds), func(d, s *ast.Document, cut int) { d.RequirementSourceEnds = s.RequirementSourceEnds[:cut] }},
	}
}

// validateCorruptColumn runs Validate on a truncated document, recovering any
// panic into a column/cut-identified test failure. It returns the active
// diagnostic slice after Validate so the caller retains capacity growth across
// the matrix. No panic is expected; the recover exists to diagnose one if it
// ever appears.
func validateCorruptColumn(t *testing.T, name string, cut int, v *Validator, dst []Diagnostic, doc *ast.Document, fields *schema.Schema) (diags []Diagnostic) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Validate panicked on %s cut %d: %v", name, cut, r)
		}
	}()
	return v.Validate(dst, doc, fields)
}

// TestValidateCorruptColumnsNoPanic truncates each public slice column of a
// canonical document at every boundary and proves the public Validate path
// does not panic. Each case changes only a shallow-copy slice header, so the
// test itself leaves baseline elements untouched. The full-length boundary of
// every column must reproduce the valid document and yield zero diagnostics;
// truncated-boundary diagnostics are intentionally not asserted. Task 7.5.3.
func TestValidateCorruptColumnsNoPanic(t *testing.T) {
	baseline, fields := fixture(t)
	columns := corruptColumnCases(baseline)
	if len(columns) != 84 {
		t.Fatalf("corrupt-column table has %d entries, want 84", len(columns))
	}

	var v Validator
	var dst []Diagnostic
	calls, expected := 0, 0
	for _, col := range columns {
		expected += col.full + 1
		for cut := 0; cut <= col.full; cut++ {
			doc := *baseline
			col.trunc(&doc, baseline, cut)
			dst = dst[:0]
			dst = validateCorruptColumn(t, col.name, cut, &v, dst, &doc, fields)
			calls++
			if cut == col.full && len(dst) != 0 {
				t.Errorf("%s full-length boundary returned %d diagnostics: %+v", col.name, len(dst), dst)
			}
		}
	}
	if calls != expected {
		t.Fatalf("Validate calls = %d, want %d (sum of len(column)+1)", calls, expected)
	}
	t.Logf("validated %d boundaries across %d columns with no panic", calls, len(columns))
}

// TestValidateCorruptNotChildrenNoPanic supplies the one real NotChildren
// truncation absent from the canonical fixture, whose NotChildren column is
// empty. buildMinimal contains one Not node, so cutting its child column from
// one row to zero exercises the shortened-payload path.
func TestValidateCorruptNotChildrenNoPanic(t *testing.T) {
	baseline, fields := buildMinimal(t)
	if len(baseline.NotChildren) != 1 {
		t.Fatalf("minimal NotChildren len = %d, want 1", len(baseline.NotChildren))
	}
	doc := *baseline
	doc.NotChildren = baseline.NotChildren[:0]
	var v Validator
	_ = validateCorruptColumn(t, "NotChildren", 0, &v, nil, &doc, fields)
}

func TestValidateBooleanNodeRequiresBooleanValue(t *testing.T) {
	booleanDocument := func(t *testing.T) (*ast.Document, *schema.Schema, schema.ValueID, schema.ValueID) {
		t.Helper()
		doc, fields := buildMinimal(t)
		var booleanID, symbolID schema.ValueID
		for row, kind := range doc.ValueKinds {
			switch kind {
			case schema.ValueKindBoolean:
				if booleanID == 0 {
					booleanID = schema.ValueID(row + 1)
				}
			case schema.ValueKindSymbol:
				if symbolID == 0 {
					symbolID = schema.ValueID(row + 1)
				}
			}
		}
		if booleanID == 0 || symbolID == 0 {
			t.Fatal("minimal fixture lacks Boolean or symbol values")
		}
		doc.NodeKinds[0] = ast.NodeKindBoolean
		doc.NodeRefs[0] = uint32(booleanID)
		return doc, fields, booleanID, symbolID
	}

	doc, fields, _, _ := booleanDocument(t)
	if diagnostics := Validate(nil, doc, fields); len(diagnostics) != 0 {
		t.Fatalf("valid Boolean node diagnostics = %+v", diagnostics)
	}

	tests := []struct {
		name   string
		mutate func(*ast.Document, schema.ValueID, schema.ValueID)
	}{
		{"missing value", func(doc *ast.Document, _, _ schema.ValueID) { doc.NodeRefs[0] = 0 }},
		{"out of range value", func(doc *ast.Document, _, _ schema.ValueID) { doc.NodeRefs[0] = uint32(len(doc.ValueKinds) + 1) }},
		{"non Boolean value", func(doc *ast.Document, _, symbol schema.ValueID) { doc.NodeRefs[0] = uint32(symbol) }},
		{"bad Boolean payload ref", func(doc *ast.Document, boolean, _ schema.ValueID) {
			doc.ValueRefs[boolean-1] = uint32(len(doc.BooleanValues))
		}},
		{"bad Boolean payload", func(doc *ast.Document, boolean, _ schema.ValueID) {
			doc.BooleanValues[doc.ValueRefs[boolean-1]] = 2
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc, fields, booleanID, symbolID := booleanDocument(t)
			test.mutate(doc, booleanID, symbolID)
			if diagnostics := Validate(nil, doc, fields); len(diagnostics) == 0 {
				t.Fatal("invalid Boolean node produced no diagnostics")
			}
		})
	}
}
