package compile

import (
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// accumulationFixture is the combined Task 7.3.8 defect document: every
// current semantic table stage carries exactly one structurally safe,
// independent defect, built from the canonical minimal document. It is also
// the independently rebuilt expected copy: each call builds a fresh document
// and applies identical mutations, so comparing documents built by two calls
// proves Validate never mutates its inputs.
type accumulationFixture struct {
	doc    *ast.Document
	fields *schema.Schema
	scalar schema.ValueID // appended integer literal shared by the scalar and name defects
	span   ast.SourceSpan
}

// buildAccumulationFixture starts from buildMinimal and introduces one
// structurally safe defect per semantic stage, in stage order: an Exists row
// with a nonzero valid scalar, the three catalog name rows pointing at a
// structurally valid non-symbol literal, one peer-complete remediation row
// with an invalid kind, a clause evidence edge targeting a valid non-evidence
// node with one zero resolution slot, and a zero requirement ID whose clause
// CSR stays valid and nonempty. The evidence edge is appended, not replaced,
// so the evidence node stays reachable and the clause evidence CSR covers both
// edges in CSR order.
func buildAccumulationFixture(t *testing.T) accumulationFixture {
	t.Helper()
	doc, fields := buildMinimal(t)
	lit := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.CompareValues[0] = lit
	doc.EvidenceKindNames[0] = lit
	doc.EvidenceStateNames[0] = lit
	doc.OutcomeNames[0] = lit
	doc.RemediationKinds = append(doc.RemediationKinds, ast.RemediationKindInvalid)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, 0)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 1)
	doc.ClauseEvidenceNodeIDs = append(doc.ClauseEvidenceNodeIDs, 2)
	doc.ClauseEvidenceCounts[0] = 2
	doc.ClauseOnConflict[0] = 0
	doc.RequirementIDs[0] = 0
	return accumulationFixture{doc: doc, fields: fields, scalar: lit, span: ast.SourceSpan{Start: 0, End: 1}}
}

// accumulationWant returns the exact expected diagnostics for the fixture in
// global accumulation order: seed prefix, expression, evidence-kind name,
// evidence-state name, outcome name, remediation, clause evidence, clause
// missing resolution, then requirement. Every expected field is explicit and
// every unspecified field stays zero, which the reflect.DeepEqual in the
// caller proves.
func accumulationWant(f accumulationFixture) []Diagnostic {
	span := f.span
	lit := f.scalar
	return []Diagnostic{
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit},
		{Code: CodeTypeMismatch, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: span, EvidenceKind: 1, Value: lit},
		{Code: CodeTypeMismatch, Table: TableEvidenceState, Member: MemberName, Row: 1, Span: span, EvidenceState: 1, Value: lit},
		{Code: CodeTypeMismatch, Table: TableOutcome, Member: MemberName, Row: 1, Span: span, Outcome: 1, Value: lit},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Span: span, Node: 2},
		{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeConflict, Row: 1, Clause: 1, Span: span},
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
	}
}

// TestValidateSemanticAccumulation locks the Task 7.3.8 contract: seeded
// prefix retention and backing-array reuse, the exact cross-table diagnostic
// sequence of every independent semantic stage defect, validator reuse with a
// reused destination across a defective document, a clean canonical document,
// and the defective document again, and document and schema immutability
// against an independently rebuilt identical expected copy.
func TestValidateSemanticAccumulation(t *testing.T) {
	f := buildAccumulationFixture(t)
	wantF := buildAccumulationFixture(t)
	wantDiags := accumulationWant(f)

	seed := Diagnostic{Code: CodeCycle, Row: 77}
	dst := make([]Diagnostic, 1, len(wantDiags)+1)
	dst[0] = seed
	first := &dst[0]

	var v Validator
	got1 := v.Validate(dst, f.doc, f.fields)

	if len(got1) != len(wantDiags)+1 {
		t.Fatalf("defect doc produced %d diagnostics, want %d: %+v", len(got1), len(wantDiags)+1, got1)
	}
	if &got1[0] != first {
		t.Fatal("Validate reallocated the seeded destination backing array")
	}
	if got1[0] != seed {
		t.Fatalf("seed prefix not preserved: got %+v", got1[0])
	}
	want(t, got1[1:], wantDiags)

	clean, cleanFields := buildMinimal(t)
	got2 := v.Validate(dst[:1], clean, cleanFields)
	if &got2[0] != first {
		t.Fatal("Validate reallocated the seeded destination for the clean document")
	}
	want(t, got2, []Diagnostic{seed})

	got3 := v.Validate(dst[:1], f.doc, f.fields)
	if &got3[0] != first {
		t.Fatal("Validate reallocated the seeded destination on validator reuse")
	}
	want3 := make([]Diagnostic, 1, len(wantDiags)+1)
	want3[0] = seed
	want3 = append(want3, wantDiags...)
	want(t, got3, want3)

	if !reflect.DeepEqual(*f.doc, *wantF.doc) {
		t.Fatal("Validate mutated the defective ast.Document")
	}
	if !reflect.DeepEqual(*f.fields, *wantF.fields) {
		t.Fatal("Validate mutated the schema.Schema")
	}
}
