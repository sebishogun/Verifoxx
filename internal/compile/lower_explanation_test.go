package compile

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func expectedRuntimeTemplateOp(t *testing.T, op ast.TemplateOp) result.TemplateOp {
	t.Helper()
	switch op {
	case ast.TemplateOpLiteral:
		return result.TemplateOpLiteral
	case ast.TemplateOpPolicyName:
		return result.TemplateOpPolicyName
	case ast.TemplateOpPolicyVersion:
		return result.TemplateOpPolicyVersion
	case ast.TemplateOpRequestID:
		return result.TemplateOpRequestID
	case ast.TemplateOpOutcome:
		return result.TemplateOpOutcome
	case ast.TemplateOpRequirementID:
		return result.TemplateOpRequirementID
	case ast.TemplateOpClauseID:
		return result.TemplateOpClauseID
	case ast.TemplateOpNodeID:
		return result.TemplateOpNodeID
	case ast.TemplateOpReason:
		return result.TemplateOpReason
	case ast.TemplateOpEvidenceKind:
		return result.TemplateOpEvidenceKind
	case ast.TemplateOpEvidenceState:
		return result.TemplateOpEvidenceState
	case ast.TemplateOpRequiredEvidenceState:
		return result.TemplateOpRequiredEvidenceState
	case ast.TemplateOpEvidenceID:
		return result.TemplateOpEvidenceID
	default:
		t.Fatalf("unexpected AST template operation %d", op)
		return result.TemplateOpInvalid
	}
}

func expectedMaxProgramSymbolLength(t *testing.T, p *program.Program, ids []schema.SymbolID) uint32 {
	t.Helper()
	var maximum uint32
	for _, id := range ids {
		value, ok := p.Symbol(id)
		if !ok {
			t.Fatalf("symbol %d missing", id)
		}
		if len(value) > int(maximum) {
			maximum = uint32(len(value))
		}
	}
	return maximum
}

func expectedTemplateMaximums(t *testing.T, doc *ast.Document, p *program.Program) []uint32 {
	t.Helper()
	policyName, ok := p.Symbol(p.PolicyName)
	if !ok {
		t.Fatal("policy name missing")
	}
	policyVersion, ok := p.Symbol(p.PolicyVersion)
	if !ok {
		t.Fatal("policy version missing")
	}
	outcomeMax := expectedMaxProgramSymbolLength(t, p, p.Outcomes.Names)
	evidenceKindMax := expectedMaxProgramSymbolLength(t, p, p.EvidenceKindNames)
	evidenceStateMax := expectedMaxProgramSymbolLength(t, p, p.EvidenceStateNames)
	want := make([]uint32, len(doc.TemplateOpStarts))
	for row := range want {
		start := int(doc.TemplateOpStarts[row])
		end := start + int(doc.TemplateOpCounts[row])
		for i, op := range doc.TemplateOps[start:end] {
			switch op {
			case ast.TemplateOpLiteral:
				want[row] += doc.TemplateArgs[start+i]
			case ast.TemplateOpPolicyName:
				want[row] += uint32(len(policyName))
			case ast.TemplateOpPolicyVersion:
				want[row] += uint32(len(policyVersion))
			case ast.TemplateOpRequestID, ast.TemplateOpRequirementID, ast.TemplateOpClauseID,
				ast.TemplateOpNodeID, ast.TemplateOpEvidenceID:
				want[row] += 11 // Namespace prefix plus MaxUint32 decimal digits.
			case ast.TemplateOpOutcome:
				want[row] += outcomeMax
			case ast.TemplateOpReason:
				want[row] += uint32(len("wrong_subject"))
			case ast.TemplateOpEvidenceKind:
				want[row] += evidenceKindMax
			case ast.TemplateOpEvidenceState, ast.TemplateOpRequiredEvidenceState:
				want[row] += evidenceStateMax
			default:
				t.Fatalf("template %d operation %d invalid", row+1, op)
			}
		}
	}
	return want
}

func TestLowerTemplateExplanationAndSourceNodes(t *testing.T) {
	doc, fields, symbols := lowerFixture(t)
	got, err := Lower(doc, fields, symbols)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !slices.Equal(got.TemplateBytes, doc.TemplateBytes) ||
		!slices.Equal(got.TemplateOpStarts, doc.TemplateOpStarts) ||
		!slices.Equal(got.TemplateOpCounts, doc.TemplateOpCounts) ||
		!slices.Equal(got.TemplateLiteralStarts, doc.TemplateLiteralStarts) ||
		!slices.Equal(got.TemplateArgs, doc.TemplateArgs) {
		t.Fatal("template backing columns did not lower exactly")
	}
	wantOps := make([]result.TemplateOp, len(doc.TemplateOps))
	for i, op := range doc.TemplateOps {
		wantOps[i] = expectedRuntimeTemplateOp(t, op)
	}
	if !slices.Equal(got.TemplateOps, wantOps) {
		t.Fatalf("template operations = %v, want %v", got.TemplateOps, wantOps)
	}
	if want := expectedTemplateMaximums(t, doc, got); !slices.Equal(got.TemplateMaxBytes, want) {
		t.Fatalf("template maximums = %v, want %v", got.TemplateMaxBytes, want)
	}
	if !slices.Equal(got.ExplanationRationaleTemplateIDs, doc.ExplanationRationaleIDs) ||
		!slices.Equal(got.ExplanationUncertaintyStarts, doc.ExplanationUncertaintyStarts) ||
		!slices.Equal(got.ExplanationUncertaintyCounts, doc.ExplanationUncertaintyCounts) ||
		!slices.Equal(got.ExplanationUncertaintyTemplateIDs, doc.ExplanationUncertaintyIDs) ||
		!slices.Equal(got.AssumptionTemplateIDs, doc.AssumptionTemplateIDs) ||
		!slices.Equal(got.EvidenceIssueTemplateIDs, doc.EvidenceIssueTemplateIDs) ||
		!slices.Equal(got.ClauseExplanationIDs, doc.ClauseExplanationIDs) {
		t.Fatal("explanation ID columns did not lower exactly")
	}
	if !slices.Equal(got.RequirementSourceNodeIDs, doc.RequirementApplicabilityRoots) ||
		!slices.Equal(got.ClauseAssertionSourceNodeIDs, doc.ClauseAssertionRoots) ||
		!slices.Equal(got.ClauseEvidenceSourceNodeIDs, doc.ClauseEvidenceNodeIDs) {
		t.Fatalf("source nodes = requirement %v clause %v evidence %v",
			got.RequirementSourceNodeIDs, got.ClauseAssertionSourceNodeIDs, got.ClauseEvidenceSourceNodeIDs)
	}
	wantResolutionExplanations := []schema.ExplanationID{
		doc.ClauseExplanationIDs[2], doc.ClauseExplanationIDs[3], doc.ClauseExplanationIDs[4],
		doc.ClauseExplanationIDs[5], doc.ClauseExplanationIDs[5], doc.ClauseExplanationIDs[5],
		doc.ClauseExplanationIDs[5], doc.ClauseExplanationIDs[5], doc.ClauseExplanationIDs[6],
	}
	if !slices.Equal(got.Resolutions.ExplanationIDs, wantResolutionExplanations) {
		t.Fatalf("resolution explanations = %v, want %v", got.Resolutions.ExplanationIDs, wantResolutionExplanations)
	}
	resolver := got.ResultResolver()
	wrongScope, ok := resolver.Resolve(1, truth.ReasonBit(truth.ReasonWrongScope))
	if !ok || wrongScope.Explanation != doc.ClauseExplanationIDs[5] {
		t.Fatalf("WrongScope resolution = %+v, %v", wrongScope, ok)
	}
	if _, ok := got.Templates.Lookup(schema.TemplateID(len(doc.TemplateOpStarts))); !ok {
		t.Fatal("frozen template view is not bound")
	}
	if _, ok := got.Explanations.Lookup(schema.ExplanationID(len(doc.ExplanationRationaleIDs))); !ok {
		t.Fatal("frozen explanation view is not bound")
	}
}

type explanationCSEFixture struct {
	doc       *ast.Document
	fields    *schema.Schema
	symbols   *schema.Interner
	evidenceA schema.NodeID
	evidenceB schema.NodeID
	issueA    schema.TemplateID
	issueB    schema.TemplateID
}

func buildExplanationCSEFixture(t *testing.T) explanationCSEFixture {
	t.Helper()
	symbols := schema.NewSymbolInterner(4)
	fieldName, err := symbols.Intern([]byte("context.environment"))
	if err != nil {
		t.Fatal(err)
	}
	fieldBuilder := schema.NewBuilder()
	field, err := fieldBuilder.AddField(fieldName, schema.ValueKindPresence, schema.FieldGroupContext)
	if err != nil {
		t.Fatal(err)
	}
	fields := fieldBuilder.Finish()
	ab := ast.NewBuilder(ast.Hints{Nodes: 3, CompareNodes: 1, EvidenceNodes: 2, Values: 8, SymbolValues: 8, SymbolBytes: 128,
		EvidenceKinds: 1, EvidenceStates: 1, Outcomes: 1, Clauses: 2, ClauseEvidenceEdges: 2,
		Requirements: 1, RequirementClauseEdges: 2, SourceBytes: 2})
	if err := ab.SetSource([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	name, _ := ab.AddSymbolValue([]byte("cse"))
	version, _ := ab.AddSymbolValue([]byte("1"))
	if err := ab.SetMetadata(name, version); err != nil {
		t.Fatal(err)
	}
	explanations := installValidExplanations(t, ab)
	issueA, err := ab.AddTemplate([]byte("first issue"), ast.TemplateContextEvidenceMissing)
	if err != nil {
		t.Fatal(err)
	}
	issueB, err := ab.AddTemplate([]byte("second issue"), ast.TemplateContextEvidenceMissing)
	if err != nil {
		t.Fatal(err)
	}
	kindName, _ := ab.AddSymbolValue([]byte("attestation"))
	stateName, _ := ab.AddSymbolValue([]byte("current"))
	outcomeName, _ := ab.AddSymbolValue([]byte("Approve"))
	kind, err := ab.AddEvidenceKind(kindName, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	state, err := ab.AddEvidenceState(stateName, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := ab.AddOutcome(outcomeName, 1, true, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := ab.AddExists(field, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	evidenceA, err := ab.AddEvidence(kind, state, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	evidenceB, err := ab.AddEvidence(kind, state, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	issuesA, issuesB := explanations.issues, explanations.issues
	for i := range issuesA {
		issuesA[i] = issueA
		issuesB[i] = issueB
	}
	if err := ab.SetEvidenceIssueTemplates(evidenceA, issuesA); err != nil {
		t.Fatal(err)
	}
	if err := ab.SetEvidenceIssueTemplates(evidenceB, issuesB); err != nil {
		t.Fatal(err)
	}
	resolution := explanations.resolution
	resolution.OnSatisfied = outcome
	resolution.OnFalse = outcome
	resolution.OnMissing = outcome
	resolution.OnStale = outcome
	resolution.OnUnclear = outcome
	resolution.OnUnverifiable = outcome
	resolution.OnConflict = outcome
	clauseA, err := ab.AddClause(assertion, []schema.NodeID{evidenceA}, resolution, nil, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	clauseB, err := ab.AddClause(assertion, []schema.NodeID{evidenceB}, resolution, nil, ast.SourceSpan{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := ab.AddRequirement(1, assertion, []schema.ClauseID{clauseA, clauseB}, ast.SourceSpan{Start: 0, End: 2}); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	if diagnostics := Validate(nil, doc, fields); len(diagnostics) != 0 {
		t.Fatalf("fixture diagnostics: %+v", diagnostics)
	}
	return explanationCSEFixture{doc: doc, fields: fields, symbols: symbols, evidenceA: evidenceA, evidenceB: evidenceB, issueA: issueA, issueB: issueB}
}

func TestLowerExplanationPreservesSourceNodesAcrossCSE(t *testing.T) {
	fixture := buildExplanationCSEFixture(t)
	got, err := Lower(fixture.doc, fixture.fields, fixture.symbols)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	a := requireSingleInstruction(t, got, fixture.evidenceA)
	b := requireSingleInstruction(t, got, fixture.evidenceB)
	if a != b {
		t.Fatalf("equivalent evidence instructions = %d and %d, want one canonical row", a, b)
	}
	if !slices.Equal(got.ClauseEvidenceIDs, []schema.InstructionID{a, a}) ||
		!slices.Equal(got.ClauseEvidenceSourceNodeIDs, []schema.NodeID{fixture.evidenceA, fixture.evidenceB}) {
		t.Fatalf("clause evidence instruction/source rows = %v/%v", got.ClauseEvidenceIDs, got.ClauseEvidenceSourceNodeIDs)
	}
	if !slices.Equal(got.EvidenceIssueNodeIDs, []schema.NodeID{fixture.evidenceA, fixture.evidenceB}) {
		t.Fatalf("evidence issue nodes = %v", got.EvidenceIssueNodeIDs)
	}
	if len(got.EvidenceIssueTemplateIDs) != 2*ast.EvidenceIssueReasonCount {
		t.Fatalf("issue IDs = %d", len(got.EvidenceIssueTemplateIDs))
	}
	for i, id := range got.EvidenceIssueTemplateIDs[:ast.EvidenceIssueReasonCount] {
		if id != fixture.issueA {
			t.Fatalf("first issue[%d] = %d, want %d", i, id, fixture.issueA)
		}
	}
	for i, id := range got.EvidenceIssueTemplateIDs[ast.EvidenceIssueReasonCount:] {
		if id != fixture.issueB {
			t.Fatalf("second issue[%d] = %d, want %d", i, id, fixture.issueB)
		}
	}
}

func TestLowerRejectsEmptyFieldName(t *testing.T) {
	fixture := buildExplanationCSEFixture(t)
	empty, err := fixture.symbols.Intern(nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := schema.NewBuilder()
	if _, err := fields.AddField(empty, schema.ValueKindPresence, schema.FieldGroupContext); err != nil {
		t.Fatal(err)
	}
	if _, err := Lower(fixture.doc, fields.Finish(), fixture.symbols); !errors.Is(err, ErrInvalidSymbols) {
		t.Fatalf("Lower empty field name = %v, want %v", err, ErrInvalidSymbols)
	}
}

func explanationColumnsEqual(a, b *program.Program) bool {
	return slices.Equal(a.TemplateBytes, b.TemplateBytes) &&
		slices.Equal(a.TemplateOpStarts, b.TemplateOpStarts) &&
		slices.Equal(a.TemplateOpCounts, b.TemplateOpCounts) &&
		slices.Equal(a.TemplateLiteralStarts, b.TemplateLiteralStarts) &&
		slices.Equal(a.TemplateMaxBytes, b.TemplateMaxBytes) &&
		slices.Equal(a.TemplateOps, b.TemplateOps) &&
		slices.Equal(a.TemplateArgs, b.TemplateArgs) &&
		slices.Equal(a.ExplanationRationaleTemplateIDs, b.ExplanationRationaleTemplateIDs) &&
		slices.Equal(a.ExplanationUncertaintyStarts, b.ExplanationUncertaintyStarts) &&
		slices.Equal(a.ExplanationUncertaintyCounts, b.ExplanationUncertaintyCounts) &&
		slices.Equal(a.ExplanationUncertaintyTemplateIDs, b.ExplanationUncertaintyTemplateIDs) &&
		slices.Equal(a.AssumptionTemplateIDs, b.AssumptionTemplateIDs) &&
		slices.Equal(a.EvidenceIssueNodeIDs, b.EvidenceIssueNodeIDs) &&
		slices.Equal(a.EvidenceIssueTemplateIDs, b.EvidenceIssueTemplateIDs) &&
		slices.Equal(a.RequirementSourceNodeIDs, b.RequirementSourceNodeIDs) &&
		slices.Equal(a.ClauseAssertionSourceNodeIDs, b.ClauseAssertionSourceNodeIDs) &&
		slices.Equal(a.ClauseEvidenceSourceNodeIDs, b.ClauseEvidenceSourceNodeIDs) &&
		slices.Equal(a.ClauseExplanationIDs, b.ClauseExplanationIDs) &&
		slices.Equal(a.Resolutions.ExplanationIDs, b.Resolutions.ExplanationIDs) &&
		reflect.DeepEqual(a.Templates, b.Templates) && reflect.DeepEqual(a.Explanations, b.Explanations)
}

func TestLowerExplanationPoisonedReuse(t *testing.T) {
	large := buildExplanationCSEFixture(t)
	doc, fields, symbols := lowerFixture(t)
	var lowerer Lowerer
	var first, reused program.Program
	if err := lowerer.Lower(&first, large.doc, large.fields, large.symbols); err != nil {
		t.Fatalf("first Lower: %v", err)
	}
	for i := range lowerer.output.TemplateBytes {
		lowerer.output.TemplateBytes[i] = 0xa5
	}
	for i := range lowerer.output.EvidenceIssueNodeIDs {
		lowerer.output.EvidenceIssueNodeIDs[i] = schema.NodeID(^uint32(0))
	}
	for i := range lowerer.output.EvidenceIssueTemplateIDs {
		lowerer.output.EvidenceIssueTemplateIDs[i] = schema.TemplateID(^uint32(0))
	}
	for i := range lowerer.output.Resolutions.ExplanationIDs {
		lowerer.output.Resolutions.ExplanationIDs[i] = schema.ExplanationID(^uint32(0))
	}
	if err := lowerer.Lower(&reused, doc, fields, symbols); err != nil {
		t.Fatalf("reused Lower: %v", err)
	}
	fresh, err := Lower(doc, fields, symbols)
	if err != nil {
		t.Fatalf("fresh Lower: %v", err)
	}
	if !explanationColumnsEqual(&reused, fresh) {
		t.Fatal("poisoned reusable storage changed lowered explanation columns")
	}
	if string(first.TemplateBytes) == string(lowerer.output.TemplateBytes) {
		t.Fatal("first frozen Program aliases reusable output")
	}
}
