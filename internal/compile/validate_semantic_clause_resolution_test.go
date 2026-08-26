package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// clauseResolutionSlots names the seven clause resolution outcome columns in
// declared scan order with the setter that assigns a value to the slot in a
// built document's first clause row.
var clauseResolutionSlots = []struct {
	name   string
	member MemberKind
	set    func(*ast.Document, schema.OutcomeID)
}{
	{"satisfied", MemberOutcomeSatisfied, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnSatisfied[0] = o }},
	{"false", MemberOutcomeFalse, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnFalse[0] = o }},
	{"missing", MemberOutcomeMissing, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnMissing[0] = o }},
	{"stale", MemberOutcomeStale, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnStale[0] = o }},
	{"unclear", MemberOutcomeUnclear, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnUnclear[0] = o }},
	{"unverifiable", MemberOutcomeUnverifiable, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnUnverifiable[0] = o }},
	{"conflict", MemberOutcomeConflict, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnConflict[0] = o }},
}

// TestValidateSemanticClauseResolutionCanonicalClean proves the Task 7.3.7.2
// rule stays silent on canonical documents: a clause whose seven resolution
// outcome columns are all nonzero (in range) emits no CodeMissingResolution.
func TestValidateSemanticClauseResolutionCanonicalClean(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		var v Validator
		want(t, v.Validate(nil, doc, fields), nil)
	})
	t.Run("fixture", func(t *testing.T) {
		doc, fields := fixture(t)
		var v Validator
		want(t, v.Validate(nil, doc, fields), nil)
	})
}

// TestValidateSemanticClauseResolutionMissingIndividually covers each of the
// seven outcome slots missing on its own: a zero OutcomeID appends exactly one
// CodeMissingResolution on TableClause with that slot's Member, the one-based
// clause row, the Clause ID, the valid clause owner span, and a zero Outcome.
// The evidence edge of the built clause targets a NodeKindEvidence node, so no
// evidence diagnostic interferes.
func TestValidateSemanticClauseResolutionMissingIndividually(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	for _, tc := range clauseResolutionSlots {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			tc.set(doc, 0)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeMissingResolution, Table: TableClause, Member: tc.member, Row: 1, Clause: 1, Span: span},
			})
		})
	}
}

// TestValidateSemanticClauseResolutionAllSevenMissingOrder proves all seven
// zero slots on one clause row append one CodeMissingResolution each in the
// declared column order: satisfied, false, missing, stale, unclear,
// unverifiable, then conflict.
func TestValidateSemanticClauseResolutionAllSevenMissingOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseOnSatisfied[0] = 0
	doc.ClauseOnFalse[0] = 0
	doc.ClauseOnMissing[0] = 0
	doc.ClauseOnStale[0] = 0
	doc.ClauseOnUnclear[0] = 0
	doc.ClauseOnUnverifiable[0] = 0
	doc.ClauseOnConflict[0] = 0
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeFalse, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeMissing, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeStale, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeUnclear, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeUnverifiable, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeConflict, Row: 1, Clause: 1, Span: span},
	})
}

// TestValidateSemanticClauseResolutionHighStructuralOnly proves a nonzero
// out-of-range OutcomeID is structural-only for that slot: the structural
// CodeInvalidOutcome fires and no CodeMissingResolution and no other semantic
// output is appended for the same slot.
func TestValidateSemanticClauseResolutionHighStructuralOnly(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	for _, tc := range clauseResolutionSlots {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			out := schema.OutcomeID(len(doc.OutcomeNames) + 1)
			tc.set(doc, out)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidOutcome, Table: TableClause, Member: tc.member, Row: 1, Clause: 1, Outcome: out, Span: span},
			})
		})
	}
}

// TestValidateSemanticClauseResolutionIndependentOfStructuralState proves the
// missing-resolution checks are independent of the assertion root, the
// evidence CSR, and the remediation CSR of the same safe row: each structural
// defect fires in the structural phase and the CodeMissingResolution still
// appends after it.
func TestValidateSemanticClauseResolutionIndependentOfStructuralState(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("bad-assertion", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseAssertionRoots[0] = 0
		doc.ClauseOnSatisfied[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: 1, Clause: 1, Node: 0, Span: span},
			{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Span: span},
		})
	})
	t.Run("bad-evidence-csr", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceStarts[0] = math.MaxUint32
		doc.ClauseOnFalse[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Span: span},
			{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeFalse, Row: 1, Clause: 1, Span: span},
		})
	})
	t.Run("bad-remediation-csr", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseRemediationStarts[0] = math.MaxUint32
		doc.ClauseOnMissing[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberRemediations, Row: 1, Clause: 1, Span: span},
			{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeMissing, Row: 1, Clause: 1, Span: span},
		})
	})
}

// TestValidateSemanticClauseResolutionEvidenceFirst proves the evidence-edge
// diagnostic for a row appends before that row's missing resolutions: a
// non-evidence evidence target emits CodeInvalidEvidence on MemberEvidence
// first, then the missing slot appends in declared order.
func TestValidateSemanticClauseResolutionEvidenceFirst(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceNodeIDs[0] = 2
	doc.ClauseOnSatisfied[0] = 0
	doc.ClauseOnConflict[0] = 0
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 2, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeConflict, Row: 1, Clause: 1, Span: span},
	})
}

// TestValidateSemanticClauseResolutionMultipleRowsAscending scans defective
// clause rows ascending: row 1 has a missing satisfied slot, and row 2 has an
// evidence defect followed by a missing conflict slot, each appended on its
// own clause row in row order.
func TestValidateSemanticClauseResolutionMultipleRowsAscending(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseOnSatisfied[0] = 0
	appendClause(t, doc, 4, []schema.NodeID{3}, ast.SourceSpan{Start: 0, End: 1})
	doc.ClauseOnConflict[1] = 0
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Span: span},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 2, Clause: 2, Node: 3, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeConflict, Row: 2, Clause: 2, Span: span},
	})
}

// TestValidateSemanticClauseResolutionInvalidSpanZeroAttachment proves an
// invalid clause owner span does not suppress the missing-resolution
// diagnostic: the structural CodeInvalidSourceSpan fires first, then the
// semantic diagnostic attaches a zero span.
func TestValidateSemanticClauseResolutionInvalidSpanZeroAttachment(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseSourceStarts[0] = 5
	doc.ClauseSourceEnds[0] = 2
	doc.ClauseOnStale[0] = 0
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: 1, Clause: 1},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeStale, Row: 1, Clause: 1},
	})
}

// TestValidateSemanticClauseResolutionTruncatedStructuralOnly truncates one
// clause peer column below a defective second row and asserts exactly one
// CodeColumnLength: the semantic scan is bounded by the safe minimum row
// count, so the excluded tail row's missing resolution and evidence-kind
// defects stay structural-only.
func TestValidateSemanticClauseResolutionTruncatedStructuralOnly(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	peers := []struct {
		name   string
		mutate func(*ast.Document)
	}{
		{"ClauseAssertionRoots", func(d *ast.Document) { d.ClauseAssertionRoots = d.ClauseAssertionRoots[:1] }},
		{"ClauseEvidenceStarts", func(d *ast.Document) { d.ClauseEvidenceStarts = d.ClauseEvidenceStarts[:1] }},
		{"ClauseEvidenceCounts", func(d *ast.Document) { d.ClauseEvidenceCounts = d.ClauseEvidenceCounts[:1] }},
		{"ClauseRemediationStarts", func(d *ast.Document) { d.ClauseRemediationStarts = d.ClauseRemediationStarts[:1] }},
		{"ClauseRemediationCounts", func(d *ast.Document) { d.ClauseRemediationCounts = d.ClauseRemediationCounts[:1] }},
		{"ClauseOnSatisfied", func(d *ast.Document) { d.ClauseOnSatisfied = d.ClauseOnSatisfied[:1] }},
		{"ClauseOnFalse", func(d *ast.Document) { d.ClauseOnFalse = d.ClauseOnFalse[:1] }},
		{"ClauseOnMissing", func(d *ast.Document) { d.ClauseOnMissing = d.ClauseOnMissing[:1] }},
		{"ClauseOnStale", func(d *ast.Document) { d.ClauseOnStale = d.ClauseOnStale[:1] }},
		{"ClauseOnUnclear", func(d *ast.Document) { d.ClauseOnUnclear = d.ClauseOnUnclear[:1] }},
		{"ClauseOnUnverifiable", func(d *ast.Document) { d.ClauseOnUnverifiable = d.ClauseOnUnverifiable[:1] }},
		{"ClauseOnConflict", func(d *ast.Document) { d.ClauseOnConflict = d.ClauseOnConflict[:1] }},
		{"ClauseSourceStarts", func(d *ast.Document) { d.ClauseSourceStarts = d.ClauseSourceStarts[:1] }},
		{"ClauseSourceEnds", func(d *ast.Document) { d.ClauseSourceEnds = d.ClauseSourceEnds[:1] }},
	}
	for _, tc := range peers {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			appendClause(t, doc, 4, []schema.NodeID{3}, span)
			doc.ClauseOnSatisfied[1] = 0
			tc.mutate(doc)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeColumnLength, Table: TableClause},
			})
		})
	}
}

// TestValidateSemanticClauseResolutionOrderAndPrefix locks the seeded prefix
// and the semantic table order: remediation diagnostics, then clause rows with
// evidence diagnostics before missing resolutions, then requirement rows, all
// ascending within a table.
func TestValidateSemanticClauseResolutionOrderAndPrefix(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RemediationKinds = append(doc.RemediationKinds, 0)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, 0)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 1)
	doc.ClauseEvidenceNodeIDs[0] = 5
	doc.ClauseOnFalse[0] = 0
	doc.RequirementIDs[0] = 0
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.validateNoGraph([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 5, Span: span},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeFalse, Row: 1, Clause: 1, Span: span},
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
	})
}
