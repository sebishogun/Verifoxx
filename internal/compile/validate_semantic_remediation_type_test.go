package compile

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// TestValidateSemanticRemediationSetFieldTypeMatch proves SetField rows whose
// field kind and literal value kind are identical validate clean for all four
// literal kinds.
func TestValidateSemanticRemediationSetFieldTypeMatch(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	adds := [...]struct {
		field schema.FieldID
		value schema.ValueID
	}{
		{sf.symbol, lit.symbol},
		{sf.integer, lit.integer},
		{sf.boolean, lit.boolean},
		{sf.timestamp, lit.timestamp},
	}
	for _, a := range adds {
		if _, err := ab.AddSetFieldRemediation(a.field, a.value, span); err != nil {
			t.Fatal(err)
		}
	}
	var v Validator
	want(t, v.Validate(nil, ab.Document(), sf.schema), nil)
}

// TestValidateSemanticRemediationSetFieldTypeMismatch covers cross-kind
// mismatches: each SetField row whose field kind differs from its literal
// value kind emits exactly one CodeTypeMismatch on MemberValue with the
// one-based row, Remediation ID, valid owner span, field, and value, in
// ascending row order.
func TestValidateSemanticRemediationSetFieldTypeMismatch(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	cases := []struct {
		field schema.FieldID
		value schema.ValueID
	}{
		{sf.symbol, lit.integer},
		{sf.integer, lit.symbol},
		{sf.boolean, lit.timestamp},
		{sf.timestamp, lit.boolean},
	}
	var wantDiags []Diagnostic
	for i, c := range cases {
		if _, err := ab.AddSetFieldRemediation(c.field, c.value, span); err != nil {
			t.Fatal(err)
		}
		wantDiags = append(wantDiags, Diagnostic{
			Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue,
			Row: uint32(i + 1), Span: span, Remediation: schema.RemediationID(i + 1),
			Field: c.field, Value: c.value,
		})
	}
	var v Validator
	want(t, v.Validate(nil, ab.Document(), sf.schema), wantDiags)
}

// TestValidateSemanticRemediationSetFieldTypePresenceField covers a presence
// field, whose kind never matches any literal: the SetField row emits one type
// mismatch carrying the presence field and the literal value.
func TestValidateSemanticRemediationSetFieldTypePresenceField(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddSetFieldRemediation(sf.presence, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	var v Validator
	want(t, v.Validate(nil, ab.Document(), sf.schema), []Diagnostic{
		{Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1, Field: sf.presence, Value: lit.symbol},
	})
}

// TestValidateSemanticRemediationSetFieldTypeSuppression proves type output is
// suppressed for every structural defect while the existing structural and
// shape diagnostics remain exact: zero or high field/value IDs, shortened
// ValueKinds or ValueRefs, a non-literal stored kind, and an invalid value
// payload ref/range never cascade into a type diagnostic.
func TestValidateSemanticRemediationSetFieldTypeSuppression(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 1}
	t.Run("zero-field", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.RemediationFields[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 1, Span: span, Remediation: 1},
		})
	})
	t.Run("zero-value", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.RemediationValues[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1},
		})
	})
	t.Run("high-field", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		high := schema.FieldID(sf.schema.Len() + 1)
		doc.RemediationFields[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: 1, Span: span, Remediation: 1, Field: high},
		})
	})
	t.Run("high-value", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		high := schema.ValueID(len(doc.ValueKinds) + 1)
		doc.RemediationValues[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1, Value: high},
		})
	})
	t.Run("shortened-value-kinds", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ValueKinds = doc.ValueKinds[:1]
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeColumnLength, Table: TableValue},
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1, Value: lit.integer},
		})
	})
	t.Run("shortened-value-refs", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ValueRefs = doc.ValueRefs[:0]
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeColumnLength, Table: TableValue},
		})
	})
	t.Run("non-literal-stored-kind", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ValueKinds = append(doc.ValueKinds, schema.ValueKindPresence)
		doc.ValueRefs = append(doc.ValueRefs, 0)
		doc.RemediationValues[0] = schema.ValueID(len(doc.ValueKinds))
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableValue, Row: 5, Value: 5},
		})
	})
	t.Run("invalid-payload-ref", func(t *testing.T) {
		ab, sf, lit, _ := newSemDoc(t)
		if _, err := ab.AddSetFieldRemediation(sf.symbol, lit.integer, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ValueRefs[1] = 1
		var v Validator
		want(t, v.Validate(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableValue, Row: 2, Value: lit.integer},
		})
	})
}

// TestValidateSemanticRemediationSetFieldTypeOrdering locks the placement of
// the type diagnostic: all shape diagnostics for a row come first in
// Field->Value->EvidenceKind order, and the type mismatch appends after them.
// A forbidden nonzero EvidenceKind on SetField never suppresses the type check.
// Rows ascend and the caller seed prefix is preserved.
func TestValidateSemanticRemediationSetFieldTypeOrdering(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	span := ast.SourceSpan{Start: 0, End: 4}
	doc.RemediationValues[0] = id
	doc.RemediationEvidenceKinds[0] = 1
	doc.RemediationKinds[1] = ast.RemediationKindSetField
	doc.RemediationFields[1] = 0
	doc.RemediationValues[1] = 0
	doc.RemediationEvidenceKinds[1] = 1
	doc.RemediationKinds = append(doc.RemediationKinds, ast.RemediationKindSetField)
	doc.RemediationFields = append(doc.RemediationFields, 1)
	doc.RemediationValues = append(doc.RemediationValues, id)
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, 4)
	seed := Diagnostic{Code: CodeCycle}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, Span: span, Remediation: 1, EvidenceKind: 1},
		{Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue, Row: 1, Span: span, Remediation: 1, Field: 1, Value: id},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, Span: span, Remediation: 2, EvidenceKind: 1},
		{Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue, Row: 3, Span: span, Remediation: 3, Field: 1, Value: id},
	})
}

// TestValidateSemanticRemediationSetFieldTypeInvalidSpan proves an invalid
// owner span does not suppress the type mismatch: the structural span
// diagnostic is emitted first, then the type mismatch attaches a zero span.
func TestValidateSemanticRemediationSetFieldTypeInvalidSpan(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.RemediationValues[0] = id
	doc.RemediationSourceStarts[0] = 5
	doc.RemediationSourceEnds[0] = 2
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableRemediation, Member: MemberSpan, Row: 1, Remediation: 1},
		{Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue, Row: 1, Remediation: 1, Field: 1, Value: id},
	})
}

// TestValidateSemanticRemediationAddEvidenceNoType proves AddEvidence rows
// never perform field/value type checks even when their forbidden members are
// nonzero and would mismatch: only the independent shape diagnostics fire.
func TestValidateSemanticRemediationAddEvidenceNoType(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	span := ast.SourceSpan{Start: 0, End: 4}
	doc.RemediationFields[1] = 1
	doc.RemediationValues[1] = id
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: 2, Span: span, Remediation: 2, Field: 1},
		{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: 2, Span: span, Remediation: 2, Value: id},
	})
}
