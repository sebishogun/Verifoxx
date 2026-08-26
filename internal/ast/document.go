package ast

import "github.com/sebishogun/nornrune/internal/schema"

// Document owns a policy AST as parallel typed columns. NodeID n indexes the
// top-level columns at n-1. NodeRefs selects a row in the payload table named
// by NodeKinds. Boolean node refs are one-based ValueIDs; all other refs are
// zero-based payload rows. Relationships are integer IDs; group edges share one
// CSR edge column rather than allocating a child slice per node.
//
// A Document returned by Builder.Document remains mutable and is valid until
// that builder is reset or reused.
type Document struct {
	NodeKinds []NodeKind
	NodeRefs  []uint32

	TemplateBytes         []byte
	TemplateOpStarts      []uint32
	TemplateOpCounts      []uint16
	TemplateLiteralStarts []uint32
	TemplateMaxBytes      []uint32
	TemplateContexts      []TemplateContext
	TemplateOps           []TemplateOp
	TemplateArgs          []uint32
	AssumptionTemplateIDs []schema.TemplateID
	AssumptionsSet        []uint8

	ExplanationRationaleIDs      []schema.TemplateID
	ExplanationUncertaintyStarts []uint32
	ExplanationUncertaintyCounts []uint16
	ExplanationUncertaintyIDs    []schema.TemplateID

	CompareFields     []schema.FieldID
	CompareOps        []CompareOp
	CompareValues     []schema.ValueID
	CompareListStarts []uint32
	CompareListCounts []uint16
	ListValueIDs      []schema.ValueID

	GroupChildStarts []uint32
	GroupChildCounts []uint16
	ChildNodeIDs     []schema.NodeID

	NotChildren []schema.NodeID

	EvidenceKinds            []schema.EvidenceKindID
	EvidenceStates           []schema.EvidenceStateID
	EvidenceSubjects         []schema.ValueID
	EvidenceScopes           []schema.ValueID
	EvidenceTimings          []schema.ValueID
	EvidenceIssueTemplateIDs []schema.TemplateID

	SourceStarts []uint32
	SourceEnds   []uint32
	InputBytes   []byte

	ValueKinds      []schema.ValueKind
	ValueRefs       []uint32
	SymbolStarts    []uint32
	SymbolLengths   []uint32
	SymbolBytes     []byte
	IntegerValues   []int64
	BooleanValues   []uint8
	TimestampValues []int64

	EvidenceKindNames         []schema.ValueID
	EvidenceKindSourceStarts  []uint32
	EvidenceKindSourceEnds    []uint32
	EvidenceStateNames        []schema.ValueID
	EvidenceStateSourceStarts []uint32
	EvidenceStateSourceEnds   []uint32

	OutcomeNames        []schema.ValueID
	OutcomePrecedence   []uint8
	OutcomeTerminal     []bool
	OutcomeSourceStarts []uint32
	OutcomeSourceEnds   []uint32

	RemediationKinds         []RemediationKind
	RemediationFields        []schema.FieldID
	RemediationValues        []schema.ValueID
	RemediationEvidenceKinds []schema.EvidenceKindID
	RemediationSourceStarts  []uint32
	RemediationSourceEnds    []uint32

	ClauseAssertionRoots    []schema.NodeID
	ClauseEvidenceStarts    []uint32
	ClauseEvidenceCounts    []uint16
	ClauseEvidenceNodeIDs   []schema.NodeID
	ClauseRemediationStarts []uint32
	ClauseRemediationCounts []uint16
	ClauseRemediationIDs    []schema.RemediationID
	ClauseOnSatisfied       []schema.OutcomeID
	ClauseOnFalse           []schema.OutcomeID
	ClauseOnMissing         []schema.OutcomeID
	ClauseOnStale           []schema.OutcomeID
	ClauseOnUnclear         []schema.OutcomeID
	ClauseOnUnverifiable    []schema.OutcomeID
	ClauseOnConflict        []schema.OutcomeID
	ClauseExplanationIDs    []schema.ExplanationID
	ClauseSourceStarts      []uint32
	ClauseSourceEnds        []uint32

	RequirementIDs                []schema.RequirementID
	RequirementApplicabilityRoots []schema.NodeID
	RequirementClauseStarts       []uint32
	RequirementClauseCounts       []uint16
	RequirementClauseIDs          []schema.ClauseID
	RequirementSourceStarts       []uint32
	RequirementSourceEnds         []uint32

	Metadata PolicyMetadata
}

// Len returns the number of AST nodes.
func (d *Document) Len() int {
	return len(d.NodeKinds)
}

func (d *Document) nodeIndex(id schema.NodeID) (int, bool) {
	if id == 0 {
		return 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.NodeKinds)) || i >= uint64(len(d.NodeRefs)) {
		return 0, false
	}
	return int(i), true
}

// Kind returns the kind of id.
func (d *Document) Kind(id schema.NodeID) (NodeKind, bool) {
	i, ok := d.nodeIndex(id)
	if !ok {
		return NodeKindInvalid, false
	}
	return d.NodeKinds[i], true
}

// NodeRef returns the payload-table row selected by id.
func (d *Document) NodeRef(id schema.NodeID) (uint32, bool) {
	i, ok := d.nodeIndex(id)
	if !ok {
		return 0, false
	}
	return d.NodeRefs[i], true
}

// Compare returns the compare payload for id.
func (d *Document) Compare(id schema.NodeID) (schema.FieldID, CompareOp, schema.ValueID, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindCompare {
		return 0, CompareOpInvalid, 0, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.CompareFields)) || r >= uint64(len(d.CompareOps)) || r >= uint64(len(d.CompareValues)) {
		return 0, CompareOpInvalid, 0, false
	}
	return d.CompareFields[r], d.CompareOps[r], d.CompareValues[r], true
}

// Boolean returns the Boolean ValueID referenced by a constant node.
func (d *Document) Boolean(id schema.NodeID) (schema.ValueID, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindBoolean {
		return 0, false
	}
	value := schema.ValueID(d.NodeRefs[i])
	if _, ok := d.BooleanValue(value); !ok {
		return 0, false
	}
	return value, true
}

// CompareListRange returns the compare row's half-open range in ListValueIDs.
// Scalar and Exists comparisons have a valid zero-length range.
func (d *Document) CompareListRange(id schema.NodeID) (start, count uint32, ok bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindCompare {
		return 0, 0, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.CompareListStarts)) || r >= uint64(len(d.CompareListCounts)) {
		return 0, 0, false
	}
	start = d.CompareListStarts[r]
	count = uint32(d.CompareListCounts[r])
	if uint64(start)+uint64(count) > uint64(len(d.ListValueIDs)) {
		return 0, 0, false
	}
	return start, count, true
}

// InValues returns a read-only view of an In comparison's values.
func (d *Document) InValues(id schema.NodeID) ([]schema.ValueID, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindCompare {
		return nil, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.CompareOps)) || d.CompareOps[r] != CompareOpIn {
		return nil, false
	}
	start, count, ok := d.CompareListRange(id)
	if !ok {
		return nil, false
	}
	return d.ListValueIDs[int(start):int(start+count)], true
}

// GroupRange returns id's half-open range in ChildNodeIDs.
func (d *Document) GroupRange(id schema.NodeID) (start, count uint32, ok bool) {
	i, ok := d.nodeIndex(id)
	if !ok || !d.NodeKinds[i].group() {
		return 0, 0, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.GroupChildStarts)) || r >= uint64(len(d.GroupChildCounts)) {
		return 0, 0, false
	}
	start = d.GroupChildStarts[r]
	count = uint32(d.GroupChildCounts[r])
	if uint64(start)+uint64(count) > uint64(len(d.ChildNodeIDs)) {
		return 0, 0, false
	}
	return start, count, true
}

// GroupChildren returns a read-only view of id's range in the shared edge
// column. The slice is invalidated by later builder mutation.
func (d *Document) GroupChildren(id schema.NodeID) ([]schema.NodeID, bool) {
	start, count, ok := d.GroupRange(id)
	if !ok {
		return nil, false
	}
	return d.ChildNodeIDs[int(start):int(start+count)], true
}

// NotChild returns the child referenced by a negation node.
func (d *Document) NotChild(id schema.NodeID) (schema.NodeID, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindNot {
		return 0, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.NotChildren)) {
		return 0, false
	}
	return d.NotChildren[r], true
}

// Evidence returns the evidence kind and required state stored by id.
func (d *Document) Evidence(id schema.NodeID) (schema.EvidenceKindID, schema.EvidenceStateID, bool) {
	kind, state, _, _, _, ok := d.EvidenceMatch(id)
	return kind, state, ok
}

// EvidenceMatch returns the required kind/state and optional symbol qualifiers.
func (d *Document) EvidenceMatch(id schema.NodeID) (schema.EvidenceKindID, schema.EvidenceStateID, schema.ValueID, schema.ValueID, schema.ValueID, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || d.NodeKinds[i] != NodeKindEvidence {
		return 0, 0, 0, 0, 0, false
	}
	r := uint64(d.NodeRefs[i])
	if r >= uint64(len(d.EvidenceKinds)) || r >= uint64(len(d.EvidenceStates)) {
		return 0, 0, 0, 0, 0, false
	}
	var subject, scope, timing schema.ValueID
	if len(d.EvidenceSubjects) != 0 {
		if r >= uint64(len(d.EvidenceSubjects)) {
			return 0, 0, 0, 0, 0, false
		}
		subject = d.EvidenceSubjects[r]
	}
	if len(d.EvidenceScopes) != 0 {
		if r >= uint64(len(d.EvidenceScopes)) {
			return 0, 0, 0, 0, 0, false
		}
		scope = d.EvidenceScopes[r]
	}
	if len(d.EvidenceTimings) != 0 {
		if r >= uint64(len(d.EvidenceTimings)) {
			return 0, 0, 0, 0, 0, false
		}
		timing = d.EvidenceTimings[r]
	}
	return d.EvidenceKinds[r], d.EvidenceStates[r], subject, scope, timing, true
}

// Span returns id's source range.
func (d *Document) Span(id schema.NodeID) (SourceSpan, bool) {
	i, ok := d.nodeIndex(id)
	if !ok || i >= len(d.SourceStarts) || i >= len(d.SourceEnds) {
		return SourceSpan{}, false
	}
	span := SourceSpan{Start: d.SourceStarts[i], End: d.SourceEnds[i]}
	if !span.valid(len(d.InputBytes)) {
		return SourceSpan{}, false
	}
	return span, true
}

// Source returns a read-only view of id's source bytes.
func (d *Document) Source(id schema.NodeID) ([]byte, bool) {
	span, ok := d.Span(id)
	if !ok {
		return nil, false
	}
	return d.InputBytes[int(span.Start):int(span.End)], true
}
