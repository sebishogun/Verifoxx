package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// appendClause appends one structurally safe clause row: a valid assertion
// root, an evidence CSR range covering the given edges appended at the current
// ClauseEvidenceNodeIDs tail, an empty remediation range, valid outcome slots,
// and span. Every peer column grows so the semantic safe minimum keeps
// scanning the new row.
func appendClause(t *testing.T, doc *ast.Document, assertion schema.NodeID, evidence []schema.NodeID, span ast.SourceSpan) {
	t.Helper()
	doc.ClauseAssertionRoots = append(doc.ClauseAssertionRoots, assertion)
	doc.ClauseEvidenceStarts = append(doc.ClauseEvidenceStarts, uint32(len(doc.ClauseEvidenceNodeIDs)))
	doc.ClauseEvidenceCounts = append(doc.ClauseEvidenceCounts, uint16(len(evidence)))
	doc.ClauseEvidenceNodeIDs = append(doc.ClauseEvidenceNodeIDs, evidence...)
	doc.ClauseRemediationStarts = append(doc.ClauseRemediationStarts, uint32(len(doc.ClauseRemediationIDs)))
	doc.ClauseRemediationCounts = append(doc.ClauseRemediationCounts, 0)
	doc.ClauseOnSatisfied = append(doc.ClauseOnSatisfied, 1)
	doc.ClauseOnFalse = append(doc.ClauseOnFalse, 1)
	doc.ClauseOnMissing = append(doc.ClauseOnMissing, 1)
	doc.ClauseOnStale = append(doc.ClauseOnStale, 1)
	doc.ClauseOnUnclear = append(doc.ClauseOnUnclear, 1)
	doc.ClauseOnUnverifiable = append(doc.ClauseOnUnverifiable, 1)
	doc.ClauseOnConflict = append(doc.ClauseOnConflict, 1)
	doc.ClauseSourceStarts = append(doc.ClauseSourceStarts, span.Start)
	doc.ClauseSourceEnds = append(doc.ClauseSourceEnds, span.End)
}

// TestValidateSemanticClauseEvidenceCanonicalClean proves the Task 7.3.7.1
// rule stays silent on canonical documents: every clause evidence edge targets
// a node whose declared kind is NodeKindEvidence, and empty evidence ranges are
// valid and clean.
func TestValidateSemanticClauseEvidenceCanonicalClean(t *testing.T) {
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

// TestValidateSemanticClauseEvidenceEmptyRange covers the empty-range rule:
// a structurally valid evidence CSR with count zero is valid and clean whether
// the empty range sits at the start or at the tail of the edge column.
func TestValidateSemanticClauseEvidenceEmptyRange(t *testing.T) {
	t.Run("empty-at-start", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceStarts[0] = 0
		doc.ClauseEvidenceCounts[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), nil)
	})
	t.Run("empty-at-tail", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceStarts[0] = 1
		doc.ClauseEvidenceCounts[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), nil)
	})
}

// TestValidateSemanticClauseEvidenceValidNonEvidenceKinds covers each valid
// node kind other than NodeKindEvidence as an in-range evidence target: the
// scan appends exactly one CodeInvalidEvidence on TableClause MemberEvidence
// carrying the one-based clause row, the Clause ID, the target Node ID, and
// the valid clause span.
func TestValidateSemanticClauseEvidenceValidNonEvidenceKinds(t *testing.T) {
	// buildMinimal constructs these five expression kinds as nodes 1 through 5
	// and its evidence node as node 6.
	tests := []struct {
		name string
		node schema.NodeID
	}{
		{"compare-exists", 1},
		{"compare", 2},
		{"not", 3},
		{"all", 4},
		{"any", 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseEvidenceNodeIDs[0] = tc.node
			var v Validator
			want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: tc.node, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

// TestValidateSemanticClauseEvidenceMultipleEdgesCSROrder proves one clause
// row with several evidence edges appends one diagnostic per non-evidence
// target in CSR edge order, skipping the evidence-kind target in between, all
// on the owning clause row.
func TestValidateSemanticClauseEvidenceMultipleEdgesCSROrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceNodeIDs[0] = 4
	doc.ClauseEvidenceNodeIDs = append(doc.ClauseEvidenceNodeIDs,
		schema.NodeID(3), schema.NodeID(6), schema.NodeID(2))
	doc.ClauseEvidenceCounts[0] = 4
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 4, Span: span},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 3, Span: span},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 2, Span: span},
	})
}

// TestValidateSemanticClauseEvidenceZeroHighStructuralOnly proves a zero or
// high edge target stays structural-only: only the structural
// CodeInvalidNodeReference fires and no semantic kind diagnostic is added.
func TestValidateSemanticClauseEvidenceZeroHighStructuralOnly(t *testing.T) {
	doc0, _ := buildMinimal(t)
	nodeMax := schema.NodeID(len(doc0.NodeKinds))
	tests := []struct {
		name string
		id   schema.NodeID
	}{
		{"zero", 0},
		{"high", nodeMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseEvidenceNodeIDs[0] = tc.id
			var v Validator
			want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: tc.id, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

// TestValidateSemanticClauseEvidenceInvalidTargetKindStructuralOnly proves an
// in-range evidence target whose own node kind is invalid produces no semantic
// output: structural node-kind validation owns that row, so only the
// structural CodeInvalidNodeKind fires.
func TestValidateSemanticClauseEvidenceInvalidTargetKindStructuralOnly(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceNodeIDs[0] = 1
	doc.NodeKinds[0] = ast.NodeKindInvalid
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeKind, Table: TableNode, Row: 1, Node: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

// TestValidateSemanticClauseEvidenceInvalidCSRStructuralOnly proves an invalid
// evidence CSR range stays structural-only: the structural
// CodeInvalidCSRRange fires and no semantic kind diagnostic is added.
func TestValidateSemanticClauseEvidenceInvalidCSRStructuralOnly(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceStarts[0] = math.MaxUint32
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

// TestValidateSemanticClauseEvidenceIndependentOfClauseState proves the
// evidence-kind scan does not blanket-skip a clause marked unsafe by an
// independent structural defect: a bad assertion root or a bad remediation CSR
// does not suppress the semantic diagnostic when the evidence range itself is
// safe, structural first, then semantic.
func TestValidateSemanticClauseEvidenceIndependentOfClauseState(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("bad-assertion", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseAssertionRoots[0] = 0
		doc.ClauseEvidenceNodeIDs[0] = 2
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: 1, Clause: 1, Node: 0, Span: span},
			{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 2, Span: span},
		})
	})
	t.Run("bad-remediation-csr", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseRemediationStarts[0] = math.MaxUint32
		doc.ClauseEvidenceNodeIDs[0] = 3
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberRemediations, Row: 1, Clause: 1, Span: span},
			{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 3, Span: span},
		})
	})
}

// TestValidateSemanticClauseEvidencePayloadDefectDistinction proves the scan
// checks only the declared node kind, never the target's payload: a
// NodeKindEvidence target whose evidence payload peers are structurally
// truncated stays clean of kind diagnostics, while a non-evidence target stays
// diagnosed even when its own payload peers are structurally truncated.
func TestValidateSemanticClauseEvidencePayloadDefectDistinction(t *testing.T) {
	t.Run("evidence-target-defective-payload-clean", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.EvidenceKinds = doc.EvidenceKinds[:0]
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableEvidenceNode},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 6, Node: 6, Span: ast.SourceSpan{Start: 0, End: 1}},
		})
	})
	t.Run("compare-target-defective-payload-still-diagnosed", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceNodeIDs[0] = 2
		doc.CompareFields = doc.CompareFields[:0]
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableCompare},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 1, Node: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
			{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
		})
	})
}

// TestValidateSemanticClauseEvidenceInvalidSpanZeroAttachment proves an invalid
// clause owner span does not suppress the kind diagnostic: the structural
// CodeInvalidSourceSpan fires first, then the semantic diagnostic attaches a
// zero span.
func TestValidateSemanticClauseEvidenceInvalidSpanZeroAttachment(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseSourceStarts[0] = 5
	doc.ClauseSourceEnds[0] = 2
	doc.ClauseEvidenceNodeIDs[0] = 4
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: 1, Clause: 1},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 4},
	})
}

// TestValidateSemanticClauseEvidenceTruncatedStructuralOnly truncates one
// clause peer column below a defective second row and asserts exactly one
// CodeColumnLength: the semantic scan is bounded by the safe minimum row
// count, so the excluded tail row's evidence-kind defect stays structural-only.
func TestValidateSemanticClauseEvidenceTruncatedStructuralOnly(t *testing.T) {
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
			tc.mutate(doc)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeColumnLength, Table: TableClause},
			})
		})
	}
}

// TestValidateSemanticClauseEvidenceMultipleRowsAscending scans defective
// clause rows ascending: row 1's evidence edge targets a Compare node and row
// 2's targets a Not node, each appended on its own clause row in row order.
func TestValidateSemanticClauseEvidenceMultipleRowsAscending(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceNodeIDs[0] = 2
	appendClause(t, doc, 4, []schema.NodeID{3}, ast.SourceSpan{Start: 0, End: 1})
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 2, Span: span},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 2, Clause: 2, Node: 3, Span: span},
	})
}

// TestValidateSemanticClauseEvidenceOrderAndPrefix locks the seeded prefix and
// the semantic table order: remediation diagnostics, then clause evidence-kind
// rows, then requirement rows, all ascending within a table.
func TestValidateSemanticClauseEvidenceOrderAndPrefix(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RemediationKinds = append(doc.RemediationKinds, 0)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, 0)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 1)
	doc.ClauseEvidenceNodeIDs[0] = 5
	doc.RequirementIDs[0] = 0
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.validateNoGraph([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: 5, Span: span},
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
	})
}

// TestValidateSemanticClauseEvidenceValidatorReuse proves the reusable
// validator keeps working across a defective clause-evidence document and a
// clean document without retaining stale diagnostics.
func TestValidateSemanticClauseEvidenceValidatorReuse(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseEvidenceNodeIDs[0] = 3
	var v Validator
	if got := v.validateNoGraph(nil, doc, fields); len(got) != 1 {
		t.Fatalf("defect doc produced %d diagnostics, want 1: %+v", len(got), got)
	}
	clean, cleanFields := buildMinimal(t)
	want(t, v.validateNoGraph(nil, clean, cleanFields), nil)
}
