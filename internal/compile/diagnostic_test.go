package compile

import (
	"testing"
	"unsafe"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestDiagnosticCodesValidOrderedUnique(t *testing.T) {
	want := []struct {
		code DiagnosticCode
		name string
	}{
		{CodeInvalidDocument, "invalid_document"},
		{CodeColumnLength, "column_length"},
		{CodeInvalidSourceSpan, "invalid_source_span"},
		{CodeInvalidNodeKind, "invalid_node_kind"},
		{CodeInvalidPayloadRef, "invalid_payload_ref"},
		{CodeInvalidCSRRange, "invalid_csr_range"},
		{CodeInvalidNodeReference, "invalid_node_reference"},
		{CodeInvalidField, "invalid_field"},
		{CodeInvalidValue, "invalid_value"},
		{CodeTypeMismatch, "type_mismatch"},
		{CodeInvalidArity, "invalid_arity"},
		{CodeInvalidEvidence, "invalid_evidence"},
		{CodeInvalidOutcome, "invalid_outcome"},
		{CodeMissingResolution, "missing_resolution"},
		{CodeInvalidRemediation, "invalid_remediation"},
		{CodeDuplicateID, "duplicate_id"},
		{CodeDuplicateName, "duplicate_name"},
		{CodeCycle, "cycle"},
		{CodeUnreachableNode, "unreachable_node"},
		{CodeInvalidID, "invalid_id"},
		{CodeInvalidTemplate, "invalid_template"},
		{CodeInvalidExplanation, "invalid_explanation"},
		{CodeMissingExplanation, "missing_explanation"},
	}
	seen := make(map[string]bool, len(want))
	for i, w := range want {
		if uint8(w.code) != uint8(i+1) {
			t.Errorf("code %d = %d, want %d", w.code, w.code, i+1)
		}
		if !w.code.Valid() {
			t.Errorf("%s Valid() = false, want true", w.name)
		}
		if got := w.code.String(); got != w.name {
			t.Errorf("%s String() = %q, want %q", w.name, got, w.name)
		}
		if seen[w.name] {
			t.Errorf("duplicate string %q", w.name)
		}
		seen[w.name] = true
	}
}

func TestDiagnosticCodeInvalidBounds(t *testing.T) {
	for _, code := range []DiagnosticCode{0, 24, 255} {
		if code.Valid() {
			t.Errorf("%d Valid() = true, want false", code)
		}
		if got := code.String(); got != "invalid" {
			t.Errorf("%d String() = %q, want invalid", code, got)
		}
	}
}

func TestTableKindsValidOrderedUnique(t *testing.T) {
	want := []struct {
		table TableKind
		name  string
	}{
		{TableDocument, "document"},
		{TableNode, "node"},
		{TableCompare, "compare"},
		{TableGroup, "group"},
		{TableNot, "not"},
		{TableEvidenceNode, "evidence_node"},
		{TableValue, "value"},
		{TableEvidenceKind, "evidence_kind"},
		{TableEvidenceState, "evidence_state"},
		{TableOutcome, "outcome"},
		{TableRemediation, "remediation"},
		{TableClause, "clause"},
		{TableRequirement, "requirement"},
		{TableTemplate, "template"},
		{TableExplanation, "explanation"},
	}
	seen := make(map[string]bool, len(want))
	for i, w := range want {
		if uint8(w.table) != uint8(i+1) {
			t.Errorf("table %d = %d, want %d", w.table, w.table, i+1)
		}
		if !w.table.Valid() {
			t.Errorf("%s Valid() = false, want true", w.name)
		}
		if got := w.table.String(); got != w.name {
			t.Errorf("%s String() = %q, want %q", w.name, got, w.name)
		}
		if seen[w.name] {
			t.Errorf("duplicate string %q", w.name)
		}
		seen[w.name] = true
	}
}

func TestTableKindInvalidBounds(t *testing.T) {
	for _, table := range []TableKind{0, 16, 255} {
		if table.Valid() {
			t.Errorf("%d Valid() = true, want false", table)
		}
		if got := table.String(); got != "invalid" {
			t.Errorf("%d String() = %q, want invalid", table, got)
		}
	}
}

func TestMemberKindsValidOrderedUnique(t *testing.T) {
	want := []struct {
		member MemberKind
		name   string
	}{
		{MemberID, "id"},
		{MemberName, "name"},
		{MemberRecordKind, "kind"},
		{MemberPayload, "payload"},
		{MemberSpan, "span"},
		{MemberField, "field"},
		{MemberOperation, "operation"},
		{MemberValue, "value"},
		{MemberValues, "values"},
		{MemberChildren, "children"},
		{MemberChild, "child"},
		{MemberEvidenceKind, "evidence_kind"},
		{MemberEvidenceState, "evidence_state"},
		{MemberAssertion, "assertion"},
		{MemberEvidence, "evidence"},
		{MemberRemediations, "remediations"},
		{MemberRemediation, "remediation"},
		{MemberApplicability, "applicability"},
		{MemberClauses, "clauses"},
		{MemberClause, "clause"},
		{MemberOutcomeSatisfied, "outcome_satisfied"},
		{MemberOutcomeFalse, "outcome_false"},
		{MemberOutcomeMissing, "outcome_missing"},
		{MemberOutcomeStale, "outcome_stale"},
		{MemberOutcomeUnclear, "outcome_unclear"},
		{MemberOutcomeUnverifiable, "outcome_unverifiable"},
		{MemberOutcomeConflict, "outcome_conflict"},
		{MemberMetadataName, "metadata_name"},
		{MemberMetadataVersion, "metadata_version"},
		{MemberContext, "context"},
		{MemberTemplate, "template"},
		{MemberRationale, "rationale"},
		{MemberUncertainty, "uncertainty"},
		{MemberAssumptions, "assumptions"},
		{MemberExplanationSatisfied, "explanation_satisfied"},
		{MemberExplanationFalse, "explanation_false"},
		{MemberExplanationMissing, "explanation_missing"},
		{MemberExplanationStale, "explanation_stale"},
		{MemberExplanationUnclear, "explanation_unclear"},
		{MemberExplanationUnverifiable, "explanation_unverifiable"},
		{MemberExplanationConflict, "explanation_conflict"},
	}
	seen := make(map[string]bool, len(want))
	for i, w := range want {
		if uint8(w.member) != uint8(i+1) {
			t.Errorf("member %d = %d, want %d", w.member, w.member, i+1)
		}
		if !w.member.Valid() {
			t.Errorf("%s Valid() = false, want true", w.name)
		}
		if got := w.member.String(); got != w.name {
			t.Errorf("%s String() = %q, want %q", w.name, got, w.name)
		}
		if seen[w.name] {
			t.Errorf("duplicate string %q", w.name)
		}
		seen[w.name] = true
	}
}

func TestMemberKindInvalidBounds(t *testing.T) {
	for _, member := range []MemberKind{0, 42, 255} {
		if member.Valid() {
			t.Errorf("%d Valid() = true, want false", member)
		}
		if got := member.String(); got != "invalid" {
			t.Errorf("%d String() = %q, want invalid", member, got)
		}
	}
}

func TestDiagnosticSize(t *testing.T) {
	if got := unsafe.Sizeof(Diagnostic{}); got != 52 {
		t.Fatalf("sizeof(Diagnostic{}) = %d, want 52", got)
	}
	if got := unsafe.Alignof(Diagnostic{}); got != 4 {
		t.Fatalf("alignof(Diagnostic{}) = %d, want 4", got)
	}
}

func TestDiagnosticFieldRoundTrip(t *testing.T) {
	orig := Diagnostic{
		Code:          CodeDuplicateID,
		Table:         TableClause,
		Member:        MemberEvidenceKind,
		Row:           23,
		Span:          ast.SourceSpan{Start: 3, End: 41},
		Node:          schema.NodeID(7),
		Clause:        schema.ClauseID(11),
		Requirement:   schema.RequirementID(13),
		Field:         schema.FieldID(17),
		Value:         schema.ValueID(19),
		Outcome:       schema.OutcomeID(29),
		Remediation:   schema.RemediationID(31),
		EvidenceKind:  schema.EvidenceKindID(37),
		EvidenceState: schema.EvidenceStateID(41),
	}
	dst := make([]Diagnostic, 0, 1)
	dst = append(dst, orig)
	got := dst[0]
	if got != orig {
		t.Fatalf("round trip = %+v, want %+v", got, orig)
	}
	if got.Table != TableClause || got.Member != MemberEvidenceKind || got.Row != 23 {
		t.Fatalf("round trip lost table/member/row: %+v", got)
	}
	if got.Node == 0 || got.Clause == 0 || got.Requirement == 0 || got.Field == 0 || got.Value == 0 ||
		got.Outcome == 0 || got.Remediation == 0 || got.EvidenceKind == 0 || got.EvidenceState == 0 {
		t.Fatalf("round trip lost a distinct strong ID: %+v", got)
	}
	if got.Span != (ast.SourceSpan{Start: 3, End: 41}) {
		t.Fatalf("round trip lost span: %+v", got.Span)
	}
}
