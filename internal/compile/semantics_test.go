package compile

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func lowerSemanticFixture(t *testing.T) (*program.Program, *schema.Schema) {
	t.Helper()
	doc, fields, syms := lowerFixture(t)
	copyDoc := *doc
	copyDoc.ClauseOnMissing = append([]schema.OutcomeID(nil), doc.ClauseOnMissing...)
	copyDoc.ClauseOnStale = append([]schema.OutcomeID(nil), doc.ClauseOnStale...)
	copyDoc.ClauseOnUnclear = append([]schema.OutcomeID(nil), doc.ClauseOnUnclear...)
	copyDoc.ClauseOnUnverifiable = append([]schema.OutcomeID(nil), doc.ClauseOnUnverifiable...)
	copyDoc.ClauseOnConflict = append([]schema.OutcomeID(nil), doc.ClauseOnConflict...)
	copyDoc.ClauseOnMissing[0] = 3      // Revise: nonterminal.
	copyDoc.ClauseOnStale[0] = 4        // Escalate: terminal.
	copyDoc.ClauseOnUnclear[0] = 1      // Approve: terminal.
	copyDoc.ClauseOnUnverifiable[0] = 2 // Reject: terminal.
	copyDoc.ClauseOnConflict[0] = 3     // Revise: nonterminal.
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, &copyDoc, fields, syms); err != nil {
		t.Fatalf("lowerConstants: %v", err)
	}
	if err := lowerer.lowerInstructions(&got, &copyDoc); err != nil {
		t.Fatalf("lowerInstructions: %v", err)
	}
	if err := lowerer.lowerSemantics(&got, &copyDoc); err != nil {
		t.Fatalf("lowerSemantics: %v", err)
	}
	return &got, fields
}

func TestLowerSemanticsTablesAndResolver(t *testing.T) {
	doc, _, _ := lowerFixture(t)
	got, _ := lowerSemanticFixture(t)

	if !reflect.DeepEqual(got.RequirementIDs, doc.RequirementIDs) ||
		!reflect.DeepEqual(got.RequirementClauseStarts, doc.RequirementClauseStarts) ||
		!reflect.DeepEqual(got.RequirementClauseCounts, doc.RequirementClauseCounts) ||
		!reflect.DeepEqual(got.RequirementClauseIDs, doc.RequirementClauseIDs) ||
		!reflect.DeepEqual(got.RequirementSourceStarts, doc.RequirementSourceStarts) ||
		!reflect.DeepEqual(got.RequirementSourceEnds, doc.RequirementSourceEnds) {
		t.Fatal("requirement rows or spans changed during lowering")
	}
	root := requireSingleInstruction(t, got, doc.RequirementApplicabilityRoots[0])
	if len(got.RequirementRoots) != 1 || got.RequirementRoots[0] != root {
		t.Fatalf("requirement roots = %v, want [%d]", got.RequirementRoots, root)
	}
	assertion := requireSingleInstruction(t, got, doc.ClauseAssertionRoots[0])
	if !reflect.DeepEqual(got.ClauseAssertionRoots, []schema.InstructionID{assertion}) {
		t.Fatalf("clause assertion roots = %v, want [%d]", got.ClauseAssertionRoots, assertion)
	}
	if len(doc.ClauseEvidenceNodeIDs) != 1 {
		t.Fatalf("fixture evidence roots = %d, want 1", len(doc.ClauseEvidenceNodeIDs))
	}
	evidence := requireSingleInstruction(t, got, doc.ClauseEvidenceNodeIDs[0])
	if !reflect.DeepEqual(got.ClauseEvidenceStarts, []uint32{0}) ||
		!reflect.DeepEqual(got.ClauseEvidenceCounts, []uint16{1}) ||
		!reflect.DeepEqual(got.ClauseEvidenceIDs, []schema.InstructionID{evidence}) {
		t.Fatalf("clause evidence rows = %v/%v/%v", got.ClauseEvidenceStarts, got.ClauseEvidenceCounts, got.ClauseEvidenceIDs)
	}
	if !reflect.DeepEqual(got.ClauseSourceStarts, doc.ClauseSourceStarts) ||
		!reflect.DeepEqual(got.ClauseSourceEnds, doc.ClauseSourceEnds) {
		t.Fatal("clause spans changed during lowering")
	}

	if len(got.Outcomes.Names) != len(doc.OutcomeNames) ||
		!reflect.DeepEqual(got.Outcomes.Precedence, doc.OutcomePrecedence) ||
		!reflect.DeepEqual(got.Outcomes.Terminal, doc.OutcomeTerminal) ||
		!reflect.DeepEqual(got.OutcomeSourceStarts, doc.OutcomeSourceStarts) ||
		!reflect.DeepEqual(got.OutcomeSourceEnds, doc.OutcomeSourceEnds) {
		t.Fatal("outcome catalog did not lower exactly")
	}
	for i, valueID := range doc.OutcomeNames {
		bytes, ok := doc.SymbolValue(valueID)
		if !ok {
			t.Fatalf("outcome %d source name missing", i+1)
		}
		want, ok := got.LookupSymbol(bytes)
		if !ok || got.Outcomes.Names[i] != want {
			t.Fatalf("outcome %d name = %d, want %d", i+1, got.Outcomes.Names[i], want)
		}
	}

	if !reflect.DeepEqual(got.Remediations.Kinds, []result.RemediationKind{
		result.RemediationSetField, result.RemediationAddEvidence,
	}) {
		t.Fatalf("remediation kinds = %v", got.Remediations.Kinds)
	}
	if got.Remediations.Fields[0] == 0 || got.Remediations.Values[0] == 0 || got.Remediations.EvidenceKinds[0] != 0 {
		t.Fatalf("set-field remediation = fields %v values %v evidence %v",
			got.Remediations.Fields, got.Remediations.Values, got.Remediations.EvidenceKinds)
	}
	if got.Remediations.Fields[1] != 0 || got.Remediations.Values[1] != 0 || got.Remediations.EvidenceKinds[1] == 0 {
		t.Fatalf("add-evidence remediation = fields %v values %v evidence %v",
			got.Remediations.Fields, got.Remediations.Values, got.Remediations.EvidenceKinds)
	}
	if !reflect.DeepEqual(got.RemediationSourceStarts, doc.RemediationSourceStarts) ||
		!reflect.DeepEqual(got.RemediationSourceEnds, doc.RemediationSourceEnds) {
		t.Fatal("remediation spans changed during lowering")
	}
	if !reflect.DeepEqual(got.ClauseRemediationStarts, []uint32{0}) ||
		!reflect.DeepEqual(got.ClauseRemediationCounts, []uint16{2}) ||
		!reflect.DeepEqual(got.ClauseRemediationIDs, []schema.RemediationID{1, 2}) {
		t.Fatalf("clause remediation rows = %v/%v/%v",
			got.ClauseRemediationStarts, got.ClauseRemediationCounts, got.ClauseRemediationIDs)
	}

	wantReasons := []schema.OutcomeID{3, 4, 1, 2, 2, 2, 2, 2, 3}
	if !reflect.DeepEqual(got.Resolutions.OutcomeIDs, wantReasons) {
		t.Fatalf("reason outcomes = %v, want %v", got.Resolutions.OutcomeIDs, wantReasons)
	}
	if got.Resolutions.RemediationStarts[0] != 0 || got.Resolutions.RemediationCounts[0] != 2 {
		t.Fatalf("Missing remediation range = (%d,%d), want (0,2)",
			got.Resolutions.RemediationStarts[0], got.Resolutions.RemediationCounts[0])
	}
	for row := 1; row < truth.ReasonCount-1; row++ {
		if got.Resolutions.RemediationCounts[row] != 0 {
			t.Fatalf("terminal reason row %d exposes %d remediations", row, got.Resolutions.RemediationCounts[row])
		}
	}
	conflictRow := int(truth.ReasonConflict) - 1
	if got.Resolutions.RemediationStarts[conflictRow] != 0 ||
		got.Resolutions.RemediationCounts[conflictRow] != 2 {
		t.Fatalf("Conflict remediation range = (%d,%d), want (0,2)",
			got.Resolutions.RemediationStarts[conflictRow],
			got.Resolutions.RemediationCounts[conflictRow])
	}
	if !reflect.DeepEqual(got.Resolutions.RemediationIDs, got.ClauseRemediationIDs) {
		t.Fatal("resolution and clause remediation edges must share Program-owned IDs")
	}
	resolver := got.ResultResolver()
	missing, ok := resolver.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok || missing.Outcome != 3 || missing.Terminal ||
		!reflect.DeepEqual(missing.Remediations, []schema.RemediationID{1, 2}) {
		t.Fatalf("Missing resolution = %+v, %v", missing, ok)
	}
	stale, ok := resolver.Resolve(1, truth.ReasonBit(truth.ReasonStale))
	if !ok || stale.Outcome != 4 || !stale.Terminal || len(stale.Remediations) != 0 {
		t.Fatalf("Stale resolution = %+v, %v", stale, ok)
	}
	if !reflect.DeepEqual(got.ClauseOnSatisfied, doc.ClauseOnSatisfied) ||
		!reflect.DeepEqual(got.ClauseOnFalse, doc.ClauseOnFalse) {
		t.Fatal("satisfied/false outcomes changed during lowering")
	}
}

func TestLowerSemanticsEmptyRemediationsRemainValid(t *testing.T) {
	fx := buildNormalizeFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, fx.doc, fx.fields, fx.syms); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerInstructions(&got, fx.doc); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerSemantics(&got, fx.doc); err != nil {
		t.Fatalf("lowerSemantics: %v", err)
	}
	if len(got.Remediations.Kinds) != 0 || len(got.ClauseRemediationIDs) != 0 ||
		len(got.Resolutions.RemediationIDs) != 0 {
		t.Fatal("empty remediation tables became nonempty")
	}
	if len(got.Resolutions.OutcomeIDs) != len(fx.doc.ClauseAssertionRoots)*truth.ReasonCount {
		t.Fatalf("resolution rows = %d", len(got.Resolutions.OutcomeIDs))
	}
	resolver := got.ResultResolver()
	if _, ok := resolver.Resolve(result.RuleSetID(len(fx.doc.ClauseAssertionRoots)), truth.ReasonBit(truth.ReasonConflict)); !ok {
		t.Fatal("last dense RuleSetID did not resolve")
	}
}

func TestLowerSemanticsClearsResolverBeforeFailure(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerInstructions(&got, doc); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerSemantics(&got, doc); err != nil {
		t.Fatal(err)
	}
	bad := *doc
	bad.RequirementClauseCounts = nil
	if err := lowerer.lowerSemantics(&got, &bad); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("lowerSemantics error = %v, want %v", err, ErrInvalidDocument)
	}
	if !reflect.DeepEqual(got.ResultResolver(), result.Resolver{}) {
		t.Fatal("failed semantic lowering retained the previous Resolver")
	}
}

func TestLowerSemanticsRejectsInvalidRequirementClause(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerInstructions(&got, doc); err != nil {
		t.Fatal(err)
	}
	bad := *doc
	bad.RequirementClauseIDs = append([]schema.ClauseID(nil), doc.RequirementClauseIDs...)
	bad.RequirementClauseIDs[0] = schema.ClauseID(len(doc.ClauseAssertionRoots) + 1)
	if err := lowerer.lowerSemantics(&got, &bad); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("lowerSemantics error = %v, want %v", err, ErrInvalidDocument)
	}
}

func TestLowerSemanticsClassifiesInvalidSourceRoot(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerInstructions(&got, doc); err != nil {
		t.Fatal(err)
	}
	bad := *doc
	bad.RequirementApplicabilityRoots = append([]schema.NodeID(nil), doc.RequirementApplicabilityRoots...)
	bad.RequirementApplicabilityRoots[0] = schema.NodeID(len(doc.NodeKinds) + 1)
	if err := lowerer.lowerSemantics(&got, &bad); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("lowerSemantics error = %v, want %v", err, ErrInvalidDocument)
	}
}

func TestLowerSemanticsRejectsMisalignedReasonColumns(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatal(err)
	}
	if err := lowerer.lowerInstructions(&got, doc); err != nil {
		t.Fatal(err)
	}
	bad := *doc
	bad.ClauseOnMissing = append(append([]schema.OutcomeID(nil), doc.ClauseOnMissing...), doc.ClauseOnMissing[0])
	if err := lowerer.lowerSemantics(&got, &bad); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("lowerSemantics error = %v, want %v", err, ErrInvalidDocument)
	}
}
