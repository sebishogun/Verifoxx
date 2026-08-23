// Package compile validates a decoded policy AST before lowering. Validation
// is separate from JSON parsing: jsonpolicy reports grammar and decode errors,
// while compile reports structural and semantic AST defects.
package compile

import (
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// DiagnosticCode identifies a stable validation defect. Values are append-only:
// new codes extend the list and never reuse a numeric value.
type DiagnosticCode uint8

const (
	CodeInvalidDocument DiagnosticCode = iota + 1
	CodeColumnLength
	CodeInvalidSourceSpan
	CodeInvalidNodeKind
	CodeInvalidPayloadRef
	CodeInvalidCSRRange
	CodeInvalidNodeReference
	CodeInvalidField
	CodeInvalidValue
	CodeTypeMismatch
	CodeInvalidArity
	CodeInvalidEvidence
	CodeInvalidOutcome
	CodeMissingResolution
	CodeInvalidRemediation
	CodeDuplicateID
	CodeDuplicateName
	CodeCycle
	CodeUnreachableNode
	CodeInvalidID
	CodeInvalidTemplate
	CodeInvalidExplanation
	CodeMissingExplanation
)

// codeNames is the fixed name table indexed by code-1. It is the single source
// of human-readable code text.
var codeNames = [...]string{
	"invalid_document",
	"column_length",
	"invalid_source_span",
	"invalid_node_kind",
	"invalid_payload_ref",
	"invalid_csr_range",
	"invalid_node_reference",
	"invalid_field",
	"invalid_value",
	"type_mismatch",
	"invalid_arity",
	"invalid_evidence",
	"invalid_outcome",
	"missing_resolution",
	"invalid_remediation",
	"duplicate_id",
	"duplicate_name",
	"cycle",
	"unreachable_node",
	"invalid_id",
	"invalid_template",
	"invalid_explanation",
	"missing_explanation",
}

// Valid reports whether c identifies a known diagnostic code.
func (c DiagnosticCode) Valid() bool {
	return c >= CodeInvalidDocument && c <= CodeMissingExplanation
}

// String returns the fixed name for c, or "invalid" for zero and out-of-range
// values.
func (c DiagnosticCode) String() string {
	i := int(c - CodeInvalidDocument)
	if i < 0 || i >= len(codeNames) {
		return "invalid"
	}
	return codeNames[i]
}

// TableKind identifies the parallel table a diagnostic's Row indexes. Values
// are append-only: new tables extend the list and never reuse a numeric value.
type TableKind uint8

const (
	TableInvalid TableKind = iota
	TableDocument
	TableNode
	TableCompare
	TableGroup
	TableNot
	TableEvidenceNode
	TableValue
	TableEvidenceKind
	TableEvidenceState
	TableOutcome
	TableRemediation
	TableClause
	TableRequirement
	TableTemplate
	TableExplanation
)

// tableNames is the fixed name table indexed by table-1. It is the single
// source of human-readable table text.
var tableNames = [...]string{
	"document",
	"node",
	"compare",
	"group",
	"not",
	"evidence_node",
	"value",
	"evidence_kind",
	"evidence_state",
	"outcome",
	"remediation",
	"clause",
	"requirement",
	"template",
	"explanation",
}

// Valid reports whether t identifies a known table kind.
func (t TableKind) Valid() bool {
	return t >= TableDocument && t <= TableExplanation
}

// String returns the fixed name for t, or "invalid" for zero and out-of-range
// values.
func (t TableKind) String() string {
	i := int(t - TableDocument)
	if i < 0 || i >= len(tableNames) {
		return "invalid"
	}
	return tableNames[i]
}

// MemberKind identifies which member of a diagnostic's record the Code refers
// to. Values are append-only: new members extend the list and never reuse a
// numeric value. MemberInvalid is the zero value and means no specific member
// is identified.
type MemberKind uint8

const (
	MemberInvalid MemberKind = iota
	MemberID
	MemberName
	MemberRecordKind
	MemberPayload
	MemberSpan
	MemberField
	MemberOperation
	MemberValue
	MemberValues
	MemberChildren
	MemberChild
	MemberEvidenceKind
	MemberEvidenceState
	MemberAssertion
	MemberEvidence
	MemberRemediations
	MemberRemediation
	MemberApplicability
	MemberClauses
	MemberClause
	MemberOutcomeSatisfied
	MemberOutcomeFalse
	MemberOutcomeMissing
	MemberOutcomeStale
	MemberOutcomeUnclear
	MemberOutcomeUnverifiable
	MemberOutcomeConflict
	MemberMetadataName
	MemberMetadataVersion
	MemberContext
	MemberTemplate
	MemberRationale
	MemberUncertainty
	MemberAssumptions
	MemberExplanationSatisfied
	MemberExplanationFalse
	MemberExplanationMissing
	MemberExplanationStale
	MemberExplanationUnclear
	MemberExplanationUnverifiable
	MemberExplanationConflict
)

// memberNames is the fixed name table indexed by member-1. It is the single
// source of human-readable member text.
var memberNames = [...]string{
	"id",
	"name",
	"kind",
	"payload",
	"span",
	"field",
	"operation",
	"value",
	"values",
	"children",
	"child",
	"evidence_kind",
	"evidence_state",
	"assertion",
	"evidence",
	"remediations",
	"remediation",
	"applicability",
	"clauses",
	"clause",
	"outcome_satisfied",
	"outcome_false",
	"outcome_missing",
	"outcome_stale",
	"outcome_unclear",
	"outcome_unverifiable",
	"outcome_conflict",
	"metadata_name",
	"metadata_version",
	"context",
	"template",
	"rationale",
	"uncertainty",
	"assumptions",
	"explanation_satisfied",
	"explanation_false",
	"explanation_missing",
	"explanation_stale",
	"explanation_unclear",
	"explanation_unverifiable",
	"explanation_conflict",
}

// Valid reports whether m identifies a known member kind.
func (m MemberKind) Valid() bool {
	return m >= MemberID && m <= MemberExplanationConflict
}

// String returns the fixed name for m, or "invalid" for zero and out-of-range
// values.
func (m MemberKind) String() string {
	i := int(m - MemberID)
	if i < 0 || i >= len(memberNames) {
		return "invalid"
	}
	return memberNames[i]
}

// Diagnostic is a zero-allocation validation result. It carries a stable code,
// the parallel table and one-based row it refers to, the record member the
// code applies to, an exact source span when one is available, and strong-ID
// context for the affected node, clause, requirement, field, value, outcome,
// remediation, evidence kind, or evidence state. It stores no message, pointer,
// slice, map, or string. The field order is fixed for a compact 52-byte layout
// on 4-byte alignment; Member occupies former padding, so do not reorder or
// insert padding manually.
type Diagnostic struct {
	Code          DiagnosticCode
	Table         TableKind
	Member        MemberKind
	Row           uint32
	Span          ast.SourceSpan
	Node          schema.NodeID
	Clause        schema.ClauseID
	Requirement   schema.RequirementID
	Field         schema.FieldID
	Value         schema.ValueID
	Outcome       schema.OutcomeID
	Remediation   schema.RemediationID
	EvidenceKind  schema.EvidenceKindID
	EvidenceState schema.EvidenceStateID
}
