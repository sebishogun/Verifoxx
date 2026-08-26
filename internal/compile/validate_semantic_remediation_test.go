package compile

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// TestValidateSemanticRemediationCanonicalShapes proves both canonical
// remediation shapes validate clean through the full public path: row 1 is a
// SetField remediation with a nonzero field and value and a zero evidence
// kind, row 2 is an AddEvidence remediation with zero field and value and a
// nonzero evidence kind.
func TestValidateSemanticRemediationCanonicalShapes(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateSemanticRemediationInvalidKind covers the Task 7.3 invalid-kind
// rule: a zero, out-of-enum, or out-of-range RemediationKind emits exactly one
// CodeInvalidRemediation on MemberRecordKind carrying the one-based row, the
// Remediation strong ID, and the valid owner span, and stops all further
// semantic checks for that row even when its payload members are defective.
func TestValidateSemanticRemediationInvalidKind(t *testing.T) {
	kinds := []struct {
		name string
		kind ast.RemediationKind
	}{
		{"zero", ast.RemediationKindInvalid},
		{"out-of-enum", ast.RemediationKind(3)},
		{"high", ast.RemediationKind(255)},
	}
	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildCatalogDoc(t)
			doc.RemediationKinds[0] = tc.kind
			doc.RemediationFields[0] = 0
			doc.RemediationValues[0] = 0
			doc.RemediationEvidenceKinds[0] = 1
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: ast.SourceSpan{Start: 0, End: 4}, Remediation: 1},
			})
		})
	}
	t.Run("stops-row-checks", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationKinds[0] = 0
		doc.RemediationFields[0] = 0
		doc.RemediationValues[0] = 0
		doc.RemediationEvidenceKinds[0] = 1
		doc.RemediationFields[1] = 1
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Span: ast.SourceSpan{Start: 0, End: 4}, Remediation: 1},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: ast.SourceSpan{Start: 0, End: 4}, Remediation: 2, Field: 1},
		})
	})
}

// TestValidateSemanticRemediationSetFieldMissing covers each missing required
// member of the SetField shape independently: a zero field emits MemberField
// carrying Field 0, a zero value emits MemberValue carrying Value 0, and a
// nonzero evidence kind emits MemberEvidenceKind carrying that kind.
func TestValidateSemanticRemediationSetFieldMissing(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("field-zero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 1, Span: span, Remediation: 1},
		})
	})
	t.Run("value-zero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationValues[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1},
		})
	})
	t.Run("evidence-nonzero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationEvidenceKinds[0] = 1
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Span: span, Remediation: 1, EvidenceKind: 1},
		})
	})
}

// TestValidateSemanticRemediationAddEvidenceForbidden covers each forbidden
// member of the AddEvidence shape independently: a nonzero field emits
// MemberField carrying the field, a nonzero value emits MemberValue carrying
// the value, and a zero evidence kind emits MemberEvidenceKind carrying 0.
func TestValidateSemanticRemediationAddEvidenceForbidden(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("field-nonzero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[1] = 1
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2, Field: 1},
		})
	})
	t.Run("value-nonzero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationValues[1] = schema.ValueID(len(doc.ValueKinds))
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2, Value: schema.ValueID(len(doc.ValueKinds))},
		})
	})
	t.Run("evidence-zero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationEvidenceKinds[1] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, Span: span, Remediation: 2},
		})
	})
}

// TestValidateSemanticRemediationAllDefectsOrder locks accumulation and order
// for one fully defective row of each canonical kind: within a valid-kind row
// the independent shape defects append MemberField, then MemberValue, then
// MemberEvidenceKind, rows ascend, and the caller-supplied seed prefix is
// preserved.
func TestValidateSemanticRemediationAllDefectsOrder(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	doc.RemediationFields[0] = 0
	doc.RemediationValues[0] = 0
	doc.RemediationEvidenceKinds[0] = 1
	doc.RemediationFields[1] = 1
	doc.RemediationValues[1] = schema.ValueID(len(doc.ValueKinds))
	doc.RemediationEvidenceKinds[1] = 0
	span := ast.SourceSpan{Start: 0, End: 4}
	seed := Diagnostic{Code: CodeCycle}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Span: span, Remediation: 1, EvidenceKind: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2, Field: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2, Value: schema.ValueID(len(doc.ValueKinds))},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, Span: span, Remediation: 2},
	})
}

// TestValidateSemanticRemediationHighRequiredStructuralOnly proves nonzero IDs
// count as present even when structurally out of range: a required member that
// is structurally high gets only its structural reference diagnostic and never
// a missing-shape diagnostic.
func TestValidateSemanticRemediationHighRequiredStructuralOnly(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("set-field-field-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.FieldID(fields.Len() + 1)
		doc.RemediationFields[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: 1, Span: span, Remediation: 1, Field: high},
		})
	})
	t.Run("set-field-value-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.ValueID(len(doc.ValueKinds) + 1)
		doc.RemediationValues[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1, Value: high},
		})
	})
	t.Run("add-evidence-evidence-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1)
		doc.RemediationEvidenceKinds[1] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, Span: span, Remediation: 2, EvidenceKind: high},
		})
	})
}

// TestValidateSemanticRemediationHighForbiddenBoth proves a forbidden nonzero
// high ID receives both its structural reference diagnostic and its
// independent shape diagnostic, structural first, then semantic.
func TestValidateSemanticRemediationHighForbiddenBoth(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("set-field-evidence-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1)
		doc.RemediationEvidenceKinds[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Span: span, Remediation: 1, EvidenceKind: high},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Span: span, Remediation: 1, EvidenceKind: high},
		})
	})
	t.Run("add-evidence-field-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.FieldID(fields.Len() + 1)
		doc.RemediationFields[1] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2, Field: high},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2, Field: high},
		})
	})
	t.Run("add-evidence-value-high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.ValueID(len(doc.ValueKinds) + 1)
		doc.RemediationValues[1] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2, Value: high},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2, Value: high},
		})
	})
}

// TestValidateSemanticRemediationMultipleRowsOrder scans three defective rows
// ascending with one independent defect each and preserves the caller seed
// prefix.
func TestValidateSemanticRemediationMultipleRowsOrder(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	doc.RemediationValues[0] = 0
	doc.RemediationEvidenceKinds[1] = 0
	value := schema.ValueID(len(doc.ValueKinds))
	doc.RemediationKinds = append(doc.RemediationKinds, ast.RemediationKindSetField)
	doc.RemediationFields = append(doc.RemediationFields, 0)
	doc.RemediationValues = append(doc.RemediationValues, value)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 4)
	span := ast.SourceSpan{Start: 0, End: 4}
	seed := Diagnostic{Code: CodeUnreachableNode}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, Span: span, Remediation: 2},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 3, Span: span, Remediation: 3},
	})
}

// TestValidateSemanticRemediationTableOrder locks the semantic table order:
// remediation shape diagnostics append after the outcome-name diagnostics of
// the same validation run.
func TestValidateSemanticRemediationTableOrder(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.OutcomeNames[0] = id
	doc.RemediationValues[0] = 0
	span := ast.SourceSpan{Start: 0, End: 4}
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeTypeMismatch, Table: TableOutcome, Member: MemberName, Row: 1, Span: span, Outcome: 1, Value: id},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1},
	})
}

// TestValidateSemanticRemediationInvalidSpanZeroAttachment proves an invalid
// owner span does not suppress shape or kind diagnostics: the structural
// CodeInvalidSourceSpan is emitted first, then every semantic diagnostic
// attaches a zero span.
func TestValidateSemanticRemediationInvalidSpanZeroAttachment(t *testing.T) {
	t.Run("shape-defects", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationSourceStarts[0] = 5
		doc.RemediationSourceEnds[0] = 2
		doc.RemediationFields[0] = 0
		doc.RemediationValues[0] = 0
		doc.RemediationEvidenceKinds[0] = 1
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRemediation, Member: MemberSpan, Row: 1, Remediation: 1},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 1, Remediation: 1},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Remediation: 1},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Remediation: 1, EvidenceKind: 1},
		})
	})
	t.Run("invalid-kind", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationSourceStarts[0] = 5
		doc.RemediationSourceEnds[0] = 2
		doc.RemediationKinds[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRemediation, Member: MemberSpan, Row: 1, Remediation: 1},
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: 1, Remediation: 1},
		})
	})
}

// TestValidateSemanticRemediationTruncatedStructuralOnly truncates each of the
// six remediation peer columns and asserts exactly one CodeColumnLength: the
// semantic scan is bounded by the safe minimum row count, so truncated rows
// stay structural-only and never panic.
func TestValidateSemanticRemediationTruncatedStructuralOnly(t *testing.T) {
	doc0, _ := buildCatalogDoc(t)
	valName := schema.ValueID(len(doc0.ValueKinds))
	trunc := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[0] = 0
		doc.RemediationValues[0] = valName
		doc.RemediationEvidenceKinds[0] = 1
		mutate(doc)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableRemediation},
		})
	}
	trunc(t, func(d *ast.Document) { d.RemediationKinds = d.RemediationKinds[:0] })
	trunc(t, func(d *ast.Document) { d.RemediationFields = d.RemediationFields[:0] })
	trunc(t, func(d *ast.Document) { d.RemediationValues = d.RemediationValues[:0] })
	trunc(t, func(d *ast.Document) { d.RemediationEvidenceKinds = d.RemediationEvidenceKinds[:0] })
	trunc(t, func(d *ast.Document) { d.RemediationSourceStarts = d.RemediationSourceStarts[:0] })
	trunc(t, func(d *ast.Document) { d.RemediationSourceEnds = d.RemediationSourceEnds[:0] })
}

// TestValidateSemanticRemediationValidatorReuse proves the reusable validator
// keeps working across a defective remediation document and a clean catalog
// document without retaining stale diagnostics.
func TestValidateSemanticRemediationValidatorReuse(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	doc.RemediationKinds[0] = 0
	var v Validator
	if got := v.Validate(nil, doc, fields); len(got) != 1 {
		t.Fatalf("defect doc produced %d diagnostics, want 1: %+v", len(got), got)
	}
	clean, cleanFields := buildCatalogDoc(t)
	want(t, v.Validate(nil, clean, cleanFields), nil)
}
