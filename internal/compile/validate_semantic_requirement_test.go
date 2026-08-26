package compile

import (
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// appendRequirement appends one structurally safe requirement row: a valid
// applicability root, a nonempty valid clause CSR range holding one valid
// ClauseID appended at the current RequirementClauseIDs tail, and span. The ID
// and clause columns stay structurally clean so a defect under test in either
// is the sole trigger.
func appendRequirement(t *testing.T, doc *ast.Document, id schema.RequirementID, root schema.NodeID, span ast.SourceSpan) {
	t.Helper()
	doc.RequirementIDs = append(doc.RequirementIDs, id)
	doc.RequirementApplicabilityRoots = append(doc.RequirementApplicabilityRoots, root)
	doc.RequirementClauseStarts = append(doc.RequirementClauseStarts, uint32(len(doc.RequirementClauseIDs)))
	doc.RequirementClauseCounts = append(doc.RequirementClauseCounts, 1)
	doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, 1)
	doc.RequirementSourceStarts = append(doc.RequirementSourceStarts, span.Start)
	doc.RequirementSourceEnds = append(doc.RequirementSourceEnds, span.End)
}

// TestValidateSemanticRequirementZeroID covers the Task 7.3.6.1 zero-ID rule: a
// zero RequirementID emits exactly one CodeInvalidID on TableRequirement
// MemberID carrying the one-based row, Requirement 0, and the valid owner span.
func TestValidateSemanticRequirementZeroID(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = 0
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

// TestValidateSemanticRequirementDistinctIDs proves distinct nonzero IDs are
// clean across multiple rows, including the maximum uint32 value: any nonzero
// ID is otherwise valid.
func TestValidateSemanticRequirementDistinctIDs(t *testing.T) {
	doc, fields := buildMinimal(t)
	appendRequirement(t, doc, 2, 5, ast.SourceSpan{Start: 0, End: 1})
	appendRequirement(t, doc, 3, 5, ast.SourceSpan{Start: 0, End: 1})
	appendRequirement(t, doc, schema.RequirementID(math.MaxUint32), 5, ast.SourceSpan{Start: 0, End: 1})
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateSemanticRequirementDuplicate covers the duplicate rule: a later
// row matching an earlier nonzero ID emits exactly one CodeDuplicateID on the
// current row carrying its Requirement ID and valid span; the first occurrence
// stays clean.
func TestValidateSemanticRequirementDuplicate(t *testing.T) {
	doc, fields := buildMinimal(t)
	appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: 1, Span: span},
	})
}

// TestValidateSemanticRequirementTriple proves three equal IDs diagnose rows 2
// and 3 exactly once each: each later row stops at its first equal predecessor.
func TestValidateSemanticRequirementTriple(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = 7
	appendRequirement(t, doc, 7, 5, ast.SourceSpan{Start: 0, End: 1})
	appendRequirement(t, doc, 7, 5, ast.SourceSpan{Start: 0, End: 1})
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: 7, Span: span},
		{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 3, Requirement: 7, Span: span},
	})
}

// TestValidateSemanticRequirementMaxUint32Duplicate proves MaxUint32 is a valid
// nonzero ID that participates in the duplicate comparison like any other.
func TestValidateSemanticRequirementMaxUint32Duplicate(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = schema.RequirementID(math.MaxUint32)
	appendRequirement(t, doc, schema.RequirementID(math.MaxUint32), 5, ast.SourceSpan{Start: 0, End: 1})
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: schema.RequirementID(math.MaxUint32), Span: span},
	})
}

// TestValidateSemanticRequirementInvalidSpanZeroAttachment proves an invalid
// owner span does not suppress ID diagnostics: the structural
// CodeInvalidSourceSpan is emitted first, then the ID diagnostic attaches a
// zero span.
func TestValidateSemanticRequirementInvalidSpanZeroAttachment(t *testing.T) {
	t.Run("zero-id", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementIDs[0] = 0
		doc.RequirementSourceStarts[0] = 5
		doc.RequirementSourceEnds[0] = 2
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 1},
			{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1},
		})
	})
	t.Run("duplicate", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
		doc.RequirementSourceStarts[1] = 5
		doc.RequirementSourceEnds[1] = 2
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 2, Requirement: 1},
			{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: 1},
		})
	})
}

// TestValidateSemanticRequirementStructuralIndependent proves ID diagnostics are
// independent of structurally invalid applicability or clause edges in the same
// safe row: the structural defect and the ID defect both fire in structural
// then semantic order.
func TestValidateSemanticRequirementStructuralIndependent(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("duplicate-with-bad-applicability", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 1, 0, span)
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableRequirement, Member: MemberApplicability, Row: 2, Requirement: 1, Node: 0, Span: span},
			{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: 1, Span: span},
		})
	})
	t.Run("zero-id-with-bad-clause-csr", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementIDs[0] = 0
		doc.RequirementClauseStarts[0] = math.MaxUint32
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableRequirement, Member: MemberClauses, Row: 1, Span: span},
			{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
		})
	})
}

// TestValidateSemanticRequirementTruncatedStructuralOnly truncates each of the
// five requirement peer columns (never the ID column) below a defective second
// row and asserts exactly one CodeColumnLength: the semantic scan is bounded by
// the safe minimum row count, so the excluded tail row stays structural-only
// and neither its duplicate ID nor its empty clause range is ever diagnosed.
func TestValidateSemanticRequirementTruncatedStructuralOnly(t *testing.T) {
	peers := []func(*ast.Document){
		func(d *ast.Document) { d.RequirementApplicabilityRoots = d.RequirementApplicabilityRoots[:1] },
		func(d *ast.Document) { d.RequirementClauseStarts = d.RequirementClauseStarts[:1] },
		func(d *ast.Document) { d.RequirementClauseCounts = d.RequirementClauseCounts[:1] },
		func(d *ast.Document) { d.RequirementSourceStarts = d.RequirementSourceStarts[:1] },
		func(d *ast.Document) { d.RequirementSourceEnds = d.RequirementSourceEnds[:1] },
	}
	for i, mutate := range peers {
		t.Run(requirementPeerName(i), func(t *testing.T) {
			doc, fields := buildMinimal(t)
			appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
			doc.RequirementClauseCounts[1] = 0
			mutate(doc)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeColumnLength, Table: TableRequirement},
			})
		})
	}
}

// TestValidateSemanticRequirementClauseArityClean proves a requirement row
// whose clause CSR range is structurally valid and nonempty is clean: arity is
// satisfied regardless of the individual ClauseID edges, so an invalid edge in
// a nonempty range stays structural-only.
func TestValidateSemanticRequirementClauseArityClean(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), nil)
	})
	t.Run("appended-nonempty", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 2, 5, ast.SourceSpan{Start: 0, End: 1})
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), nil)
	})
	t.Run("invalid-edge-structural-only", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementClauseIDs[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableRequirement, Member: MemberClause, Row: 1, Requirement: 1, Clause: 0, Span: ast.SourceSpan{Start: 0, End: 1}},
		})
	})
}

// TestValidateSemanticRequirementClauseArityEmpty covers the empty-range rule:
// a structurally valid CSR range with count zero emits exactly one
// CodeInvalidArity on MemberClauses with the one-based row, the Requirement ID,
// and the valid owner span, whether the empty range sits at the start or at the
// tail of the edge column.
func TestValidateSemanticRequirementClauseArityEmpty(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("empty-at-start", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 2, 5, span)
		doc.RequirementClauseStarts[1] = 0
		doc.RequirementClauseCounts[1] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 2, Requirement: 2, Span: span},
		})
	})
	t.Run("empty-at-tail", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 2, 5, span)
		doc.RequirementClauseCounts[1] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 2, Requirement: 2, Span: span},
		})
	})
}

// TestValidateSemanticRequirementClauseArityInvalidCSR proves an invalid clause
// CSR range stays structural-only: the structural CodeInvalidCSRRange fires and
// no arity diagnostic is added.
func TestValidateSemanticRequirementClauseArityInvalidCSR(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementClauseStarts[0] = math.MaxUint32
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidCSRRange, Table: TableRequirement, Member: MemberClauses, Row: 1, Requirement: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

// TestValidateSemanticRequirementClauseArityIDOrdering proves ID and clause
// arity diagnostics are independent and, within one row, the ID diagnostic
// comes first: a zero-ID row appends CodeInvalidID then the MemberClauses
// arity, and a duplicate-ID row appends CodeDuplicateID then the MemberClauses
// arity. The arity diagnostic always carries the row's Requirement ID, zero
// included.
func TestValidateSemanticRequirementClauseArityIDOrdering(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("zero-id-empty", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementIDs[0] = 0
		doc.RequirementClauseCounts[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
			{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 1, Span: span},
		})
	})
	t.Run("duplicate-id-empty", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 1, 5, span)
		doc.RequirementClauseCounts[1] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
			{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 2, Requirement: 1, Span: span},
			{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 2, Requirement: 1, Span: span},
		})
	})
}

// TestValidateSemanticRequirementClauseArityInvalidSpan proves an invalid owner
// span does not suppress the arity diagnostic: the structural
// CodeInvalidSourceSpan fires first, then the arity diagnostic attaches a zero
// span.
func TestValidateSemanticRequirementClauseArityInvalidSpan(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementClauseCounts[0] = 0
	doc.RequirementSourceStarts[0] = 5
	doc.RequirementSourceEnds[0] = 2
	var v Validator
	want(t, v.validateNoGraph(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 1, Requirement: 1},
		{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 1, Requirement: 1},
	})
}

// TestValidateSemanticRequirementClauseArityOrderingAndPrefix locks the seeded
// prefix, the table ordering (remediation before requirement), and ascending
// requirement rows when ID and arity defects are mixed across rows: row 1 is a
// zero-ID empty row, row 2 is a fresh-ID empty row, and row 3 duplicates row
// 2's ID with an empty range.
func TestValidateSemanticRequirementClauseArityOrderingAndPrefix(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RemediationKinds = append(doc.RemediationKinds, 0)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, 0)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 1)
	doc.RequirementIDs[0] = 0
	doc.RequirementClauseCounts[0] = 0
	appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
	doc.RequirementClauseCounts[1] = 0
	appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
	doc.RequirementClauseCounts[2] = 0
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.validateNoGraph([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
		{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 1, Span: span},
		{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 2, Requirement: 1, Span: span},
		{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: 3, Requirement: 1, Span: span},
		{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: 3, Requirement: 1, Span: span},
	})
}

// TestValidateSemanticRequirementOrderingAndPrefix locks the semantic table
// order: requirement ID diagnostics append after the remediation diagnostics of
// the same run, requirement rows ascend, and the caller-supplied seed prefix is
// preserved.
func TestValidateSemanticRequirementOrderingAndPrefix(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RemediationKinds = append(doc.RemediationKinds, 0)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, 0)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 1)
	doc.RequirementIDs[0] = 0
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
	})
}

// TestValidateSemanticRequirementValidatorReuse proves the reusable validator
// keeps working across a defective requirement document and a clean document
// without retaining stale diagnostics.
func TestValidateSemanticRequirementValidatorReuse(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = 0
	var v Validator
	if got := v.Validate(nil, doc, fields); len(got) != 1 {
		t.Fatalf("defect doc produced %d diagnostics, want 1: %+v", len(got), got)
	}
	clean, cleanFields := buildMinimal(t)
	want(t, v.Validate(nil, clean, cleanFields), nil)
}

// TestValidateSemanticRequirementDoesNotModifyInputs proves the requirement-ID
// scan never mutates the document: a duplicated-requirement document with a
// reversed span validates to the same bytes it started with.
func TestValidateSemanticRequirementDoesNotModifyInputs(t *testing.T) {
	build := func(t *testing.T) (*ast.Document, *schema.Schema) {
		t.Helper()
		doc, fields := buildMinimal(t)
		appendRequirement(t, doc, 1, 5, ast.SourceSpan{Start: 0, End: 1})
		doc.RequirementSourceStarts[1] = 5
		doc.RequirementSourceEnds[1] = 2
		return doc, fields
	}
	doc, fields := build(t)
	wantDoc, wantFields := build(t)
	var v Validator
	v.Validate(nil, doc, fields)
	if !reflect.DeepEqual(*doc, *wantDoc) {
		t.Fatal("requirement scan mutated ast.Document")
	}
	if !reflect.DeepEqual(*fields, *wantFields) {
		t.Fatal("requirement scan mutated schema.Schema")
	}
}
