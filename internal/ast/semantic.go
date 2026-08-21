package ast

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	ErrInvalidRemediation    = errors.New("ast: invalid remediation ID")
	ErrInvalidClause         = errors.New("ast: invalid clause ID")
	ErrMetadataAlreadySet    = errors.New("ast: policy metadata already set")
	ErrTooManyEvidenceKinds  = errors.New("ast: too many evidence kinds")
	ErrTooManyEvidenceStates = errors.New("ast: too many evidence states")
	ErrTooManyOutcomes       = errors.New("ast: too many outcomes")
	ErrTooManyRemediations   = errors.New("ast: too many remediations")
	ErrTooManyClauses        = errors.New("ast: too many clauses")
	ErrTooManySemanticEdges  = errors.New("ast: too many semantic edges")
)

// Resolution maps each false or unresolved state to a policy-defined outcome.
// Zero IDs are retained so Task 7 can diagnose missing resolutions.
type Resolution struct {
	OnSatisfied    schema.OutcomeID
	OnFalse        schema.OutcomeID
	OnMissing      schema.OutcomeID
	OnStale        schema.OutcomeID
	OnUnclear      schema.OutcomeID
	OnUnverifiable schema.OutcomeID
	OnConflict     schema.OutcomeID
}

// PolicyMetadata is fixed-size provenance for one source document.
type PolicyMetadata struct {
	Name        schema.ValueID
	Version     schema.ValueID
	ContentHash [32]byte
	sourceSet   bool
}

// RemediationKind is a bounded corrective action understood by the compiler.
type RemediationKind uint8

const (
	RemediationKindInvalid RemediationKind = iota
	RemediationKindSetField
	RemediationKindAddEvidence
)

// Valid reports whether k is a supported bounded remediation action.
func (k RemediationKind) Valid() bool {
	return k >= RemediationKindSetField && k <= RemediationKindAddEvidence
}

func spanAt(starts, ends []uint32, index uint64, inputLen int) (SourceSpan, bool) {
	if index >= uint64(len(starts)) || index >= uint64(len(ends)) {
		return SourceSpan{}, false
	}
	span := SourceSpan{Start: starts[index], End: ends[index]}
	if !span.valid(inputLen) {
		return SourceSpan{}, false
	}
	return span, true
}

func rangeAt(starts []uint32, counts []uint16, index uint64, total int) (start, count uint32, ok bool) {
	if index >= uint64(len(starts)) || index >= uint64(len(counts)) {
		return 0, 0, false
	}
	start = starts[index]
	count = uint32(counts[index])
	if uint64(start)+uint64(count) > uint64(total) {
		return 0, 0, false
	}
	return start, count, true
}

func (b *Builder) symbolValue(id schema.ValueID) bool {
	kind, ok := b.doc.ValueKind(id)
	return ok && kind == schema.ValueKindSymbol
}

// SetMetadata binds symbol-valued policy identity to the current document.
func (b *Builder) SetMetadata(name, version schema.ValueID) error {
	if b.doc.Metadata.Name != 0 || b.doc.Metadata.Version != 0 {
		return ErrMetadataAlreadySet
	}
	if !b.symbolValue(name) || !b.symbolValue(version) {
		return ErrInvalidValue
	}
	b.doc.Metadata.Name = name
	b.doc.Metadata.Version = version
	return nil
}

// PolicyMetadata returns complete policy identity and source provenance.
func (d *Document) PolicyMetadata() (PolicyMetadata, bool) {
	if d.Metadata.Name == 0 || d.Metadata.Version == 0 || !d.Metadata.sourceSet {
		return PolicyMetadata{}, false
	}
	return d.Metadata, true
}

// AddEvidenceKind appends a symbol-named evidence kind catalog row.
func (b *Builder) AddEvidenceKind(name schema.ValueID, span SourceSpan) (schema.EvidenceKindID, error) {
	if !b.symbolValue(name) {
		return 0, ErrInvalidValue
	}
	if uint64(len(b.doc.EvidenceKindNames)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyEvidenceKinds
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return 0, ErrInvalidSourceSpan
	}
	b.doc.EvidenceKindNames = append(b.doc.EvidenceKindNames, name)
	b.doc.EvidenceKindSourceStarts = append(b.doc.EvidenceKindSourceStarts, span.Start)
	b.doc.EvidenceKindSourceEnds = append(b.doc.EvidenceKindSourceEnds, span.End)
	return schema.EvidenceKindID(len(b.doc.EvidenceKindNames)), nil
}

// EvidenceKindName returns the symbol value naming an evidence kind.
func (d *Document) EvidenceKindName(id schema.EvidenceKindID) (schema.ValueID, bool) {
	if id == 0 {
		return 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.EvidenceKindNames)) {
		return 0, false
	}
	return d.EvidenceKindNames[i], true
}

// EvidenceKindSpan returns an evidence-kind definition's source range.
func (d *Document) EvidenceKindSpan(id schema.EvidenceKindID) (SourceSpan, bool) {
	if id == 0 {
		return SourceSpan{}, false
	}
	return spanAt(d.EvidenceKindSourceStarts, d.EvidenceKindSourceEnds, uint64(id-1), len(d.InputBytes))
}

// AddEvidenceState appends a symbol-named evidence state catalog row.
func (b *Builder) AddEvidenceState(name schema.ValueID, span SourceSpan) (schema.EvidenceStateID, error) {
	if !b.symbolValue(name) {
		return 0, ErrInvalidValue
	}
	if uint64(len(b.doc.EvidenceStateNames)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyEvidenceStates
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return 0, ErrInvalidSourceSpan
	}
	b.doc.EvidenceStateNames = append(b.doc.EvidenceStateNames, name)
	b.doc.EvidenceStateSourceStarts = append(b.doc.EvidenceStateSourceStarts, span.Start)
	b.doc.EvidenceStateSourceEnds = append(b.doc.EvidenceStateSourceEnds, span.End)
	return schema.EvidenceStateID(len(b.doc.EvidenceStateNames)), nil
}

// EvidenceStateName returns the symbol value naming an evidence state.
func (d *Document) EvidenceStateName(id schema.EvidenceStateID) (schema.ValueID, bool) {
	if id == 0 {
		return 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.EvidenceStateNames)) {
		return 0, false
	}
	return d.EvidenceStateNames[i], true
}

// EvidenceStateSpan returns an evidence-state definition's source range.
func (d *Document) EvidenceStateSpan(id schema.EvidenceStateID) (SourceSpan, bool) {
	if id == 0 {
		return SourceSpan{}, false
	}
	return spanAt(d.EvidenceStateSourceStarts, d.EvidenceStateSourceEnds, uint64(id-1), len(d.InputBytes))
}

// AddOutcome appends a policy-defined outcome catalog row.
func (b *Builder) AddOutcome(name schema.ValueID, precedence uint8, terminal bool, span SourceSpan) (schema.OutcomeID, error) {
	if !b.symbolValue(name) {
		return 0, ErrInvalidValue
	}
	if uint64(len(b.doc.OutcomeNames)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyOutcomes
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return 0, ErrInvalidSourceSpan
	}
	b.doc.OutcomeNames = append(b.doc.OutcomeNames, name)
	b.doc.OutcomePrecedence = append(b.doc.OutcomePrecedence, precedence)
	b.doc.OutcomeTerminal = append(b.doc.OutcomeTerminal, terminal)
	b.doc.OutcomeSourceStarts = append(b.doc.OutcomeSourceStarts, span.Start)
	b.doc.OutcomeSourceEnds = append(b.doc.OutcomeSourceEnds, span.End)
	return schema.OutcomeID(len(b.doc.OutcomeNames)), nil
}

// Outcome returns one policy-defined outcome catalog row.
func (d *Document) Outcome(id schema.OutcomeID) (schema.ValueID, uint8, bool, bool) {
	if id == 0 {
		return 0, 0, false, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.OutcomeNames)) || i >= uint64(len(d.OutcomePrecedence)) || i >= uint64(len(d.OutcomeTerminal)) {
		return 0, 0, false, false
	}
	return d.OutcomeNames[i], d.OutcomePrecedence[i], d.OutcomeTerminal[i], true
}

// OutcomeSpan returns an outcome definition's source range.
func (d *Document) OutcomeSpan(id schema.OutcomeID) (SourceSpan, bool) {
	if id == 0 {
		return SourceSpan{}, false
	}
	return spanAt(d.OutcomeSourceStarts, d.OutcomeSourceEnds, uint64(id-1), len(d.InputBytes))
}

func (b *Builder) addRemediation(kind RemediationKind, field schema.FieldID, value schema.ValueID, evidence schema.EvidenceKindID, span SourceSpan) (schema.RemediationID, error) {
	if uint64(len(b.doc.RemediationKinds)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyRemediations
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return 0, ErrInvalidSourceSpan
	}
	b.doc.RemediationKinds = append(b.doc.RemediationKinds, kind)
	b.doc.RemediationFields = append(b.doc.RemediationFields, field)
	b.doc.RemediationValues = append(b.doc.RemediationValues, value)
	b.doc.RemediationEvidenceKinds = append(b.doc.RemediationEvidenceKinds, evidence)
	b.doc.RemediationSourceStarts = append(b.doc.RemediationSourceStarts, span.Start)
	b.doc.RemediationSourceEnds = append(b.doc.RemediationSourceEnds, span.End)
	return schema.RemediationID(len(b.doc.RemediationKinds)), nil
}

// AddSetFieldRemediation appends a bounded field replacement action.
func (b *Builder) AddSetFieldRemediation(field schema.FieldID, value schema.ValueID, span SourceSpan) (schema.RemediationID, error) {
	if field == 0 {
		return 0, ErrInvalidField
	}
	if value == 0 {
		return 0, ErrInvalidValue
	}
	return b.addRemediation(RemediationKindSetField, field, value, 0, span)
}

// AddEvidenceRemediation appends a request for one allowed evidence kind.
func (b *Builder) AddEvidenceRemediation(kind schema.EvidenceKindID, span SourceSpan) (schema.RemediationID, error) {
	if kind == 0 {
		return 0, ErrInvalidEvidence
	}
	return b.addRemediation(RemediationKindAddEvidence, 0, 0, kind, span)
}

// Remediation returns one bounded corrective action row.
func (d *Document) Remediation(id schema.RemediationID) (RemediationKind, schema.FieldID, schema.ValueID, schema.EvidenceKindID, bool) {
	if id == 0 {
		return RemediationKindInvalid, 0, 0, 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.RemediationKinds)) || i >= uint64(len(d.RemediationFields)) || i >= uint64(len(d.RemediationValues)) || i >= uint64(len(d.RemediationEvidenceKinds)) {
		return RemediationKindInvalid, 0, 0, 0, false
	}
	return d.RemediationKinds[i], d.RemediationFields[i], d.RemediationValues[i], d.RemediationEvidenceKinds[i], true
}

// RemediationSpan returns a remediation definition's source range.
func (d *Document) RemediationSpan(id schema.RemediationID) (SourceSpan, bool) {
	if id == 0 {
		return SourceSpan{}, false
	}
	return spanAt(d.RemediationSourceStarts, d.RemediationSourceEnds, uint64(id-1), len(d.InputBytes))
}

// AddClause appends one assertion, its evidence requirements, resolution, and
// alternative bounded remediations. Empty edge ranges remain valid so Task 7
// can decide which semantic omissions are errors.
func (b *Builder) AddClause(assertion schema.NodeID, evidence []schema.NodeID, resolution Resolution, remediations []schema.RemediationID, span SourceSpan) (schema.ClauseID, error) {
	if assertion == 0 {
		return 0, ErrInvalidNode
	}
	if len(evidence) > math.MaxUint16 || len(remediations) > math.MaxUint16 {
		return 0, ErrTooManySemanticEdges
	}
	if uint64(len(b.doc.ClauseEvidenceNodeIDs))+uint64(len(evidence)) > uint64(math.MaxUint32) ||
		uint64(len(b.doc.ClauseRemediationIDs))+uint64(len(remediations)) > uint64(math.MaxUint32) {
		return 0, ErrTooManySemanticEdges
	}
	for _, id := range evidence {
		if id == 0 {
			return 0, ErrInvalidNode
		}
	}
	for _, id := range remediations {
		if id == 0 {
			return 0, ErrInvalidRemediation
		}
	}
	if uint64(len(b.doc.ClauseAssertionRoots)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyClauses
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return 0, ErrInvalidSourceSpan
	}
	evidenceStart := uint32(len(b.doc.ClauseEvidenceNodeIDs))
	remediationStart := uint32(len(b.doc.ClauseRemediationIDs))
	b.doc.ClauseEvidenceNodeIDs = append(b.doc.ClauseEvidenceNodeIDs, evidence...)
	b.doc.ClauseRemediationIDs = append(b.doc.ClauseRemediationIDs, remediations...)
	b.doc.ClauseAssertionRoots = append(b.doc.ClauseAssertionRoots, assertion)
	b.doc.ClauseEvidenceStarts = append(b.doc.ClauseEvidenceStarts, evidenceStart)
	b.doc.ClauseEvidenceCounts = append(b.doc.ClauseEvidenceCounts, uint16(len(evidence)))
	b.doc.ClauseRemediationStarts = append(b.doc.ClauseRemediationStarts, remediationStart)
	b.doc.ClauseRemediationCounts = append(b.doc.ClauseRemediationCounts, uint16(len(remediations)))
	b.doc.ClauseOnSatisfied = append(b.doc.ClauseOnSatisfied, resolution.OnSatisfied)
	b.doc.ClauseOnFalse = append(b.doc.ClauseOnFalse, resolution.OnFalse)
	b.doc.ClauseOnMissing = append(b.doc.ClauseOnMissing, resolution.OnMissing)
	b.doc.ClauseOnStale = append(b.doc.ClauseOnStale, resolution.OnStale)
	b.doc.ClauseOnUnclear = append(b.doc.ClauseOnUnclear, resolution.OnUnclear)
	b.doc.ClauseOnUnverifiable = append(b.doc.ClauseOnUnverifiable, resolution.OnUnverifiable)
	b.doc.ClauseOnConflict = append(b.doc.ClauseOnConflict, resolution.OnConflict)
	b.doc.ClauseSourceStarts = append(b.doc.ClauseSourceStarts, span.Start)
	b.doc.ClauseSourceEnds = append(b.doc.ClauseSourceEnds, span.End)
	return schema.ClauseID(len(b.doc.ClauseAssertionRoots)), nil
}

func (d *Document) clauseIndex(id schema.ClauseID) (uint64, bool) {
	if id == 0 {
		return 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.ClauseAssertionRoots)) {
		return 0, false
	}
	return i, true
}

// Clause returns an assertion root and its resolution table.
func (d *Document) Clause(id schema.ClauseID) (schema.NodeID, Resolution, bool) {
	i, ok := d.clauseIndex(id)
	if !ok || i >= uint64(len(d.ClauseOnSatisfied)) || i >= uint64(len(d.ClauseOnFalse)) || i >= uint64(len(d.ClauseOnMissing)) || i >= uint64(len(d.ClauseOnStale)) || i >= uint64(len(d.ClauseOnUnclear)) || i >= uint64(len(d.ClauseOnUnverifiable)) || i >= uint64(len(d.ClauseOnConflict)) {
		return 0, Resolution{}, false
	}
	return d.ClauseAssertionRoots[i], Resolution{
		OnSatisfied: d.ClauseOnSatisfied[i],
		OnFalse:     d.ClauseOnFalse[i], OnMissing: d.ClauseOnMissing[i],
		OnStale: d.ClauseOnStale[i], OnUnclear: d.ClauseOnUnclear[i],
		OnUnverifiable: d.ClauseOnUnverifiable[i], OnConflict: d.ClauseOnConflict[i],
	}, true
}

// ClauseEvidence returns a read-only view of a clause's evidence-node IDs.
func (d *Document) ClauseEvidence(id schema.ClauseID) ([]schema.NodeID, bool) {
	i, ok := d.clauseIndex(id)
	if !ok {
		return nil, false
	}
	start, count, ok := rangeAt(d.ClauseEvidenceStarts, d.ClauseEvidenceCounts, i, len(d.ClauseEvidenceNodeIDs))
	if !ok {
		return nil, false
	}
	return d.ClauseEvidenceNodeIDs[int(start):int(start+count)], true
}

// ClauseRemediations returns a read-only view of remediation alternatives.
func (d *Document) ClauseRemediations(id schema.ClauseID) ([]schema.RemediationID, bool) {
	i, ok := d.clauseIndex(id)
	if !ok {
		return nil, false
	}
	start, count, ok := rangeAt(d.ClauseRemediationStarts, d.ClauseRemediationCounts, i, len(d.ClauseRemediationIDs))
	if !ok {
		return nil, false
	}
	return d.ClauseRemediationIDs[int(start):int(start+count)], true
}

// ClauseSpan returns a clause's source range.
func (d *Document) ClauseSpan(id schema.ClauseID) (SourceSpan, bool) {
	i, ok := d.clauseIndex(id)
	if !ok {
		return SourceSpan{}, false
	}
	return spanAt(d.ClauseSourceStarts, d.ClauseSourceEnds, i, len(d.InputBytes))
}

// AddRequirement appends a requirement's applicability root and clause CSR
// range. Empty clause ranges remain representable for Task 7 diagnostics.
func (b *Builder) AddRequirement(requirement schema.RequirementID, applicability schema.NodeID, clauses []schema.ClauseID, span SourceSpan) error {
	if requirement == 0 {
		return ErrInvalidRequirement
	}
	if applicability == 0 {
		return ErrInvalidNode
	}
	if len(clauses) > math.MaxUint16 || uint64(len(b.doc.RequirementClauseIDs))+uint64(len(clauses)) > uint64(math.MaxUint32) {
		return ErrTooManySemanticEdges
	}
	for _, id := range clauses {
		if id == 0 {
			return ErrInvalidClause
		}
	}
	if uint64(len(b.doc.RequirementIDs)) >= uint64(math.MaxUint32) {
		return ErrTooManyRequirements
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return ErrInvalidSourceSpan
	}
	start := uint32(len(b.doc.RequirementClauseIDs))
	b.doc.RequirementClauseIDs = append(b.doc.RequirementClauseIDs, clauses...)
	b.doc.RequirementIDs = append(b.doc.RequirementIDs, requirement)
	b.doc.RequirementApplicabilityRoots = append(b.doc.RequirementApplicabilityRoots, applicability)
	b.doc.RequirementClauseStarts = append(b.doc.RequirementClauseStarts, start)
	b.doc.RequirementClauseCounts = append(b.doc.RequirementClauseCounts, uint16(len(clauses)))
	b.doc.RequirementSourceStarts = append(b.doc.RequirementSourceStarts, span.Start)
	b.doc.RequirementSourceEnds = append(b.doc.RequirementSourceEnds, span.End)
	return nil
}

func (d *Document) requirementIndex(requirement schema.RequirementID) (uint64, bool) {
	for i, id := range d.RequirementIDs {
		if id == requirement {
			return uint64(i), true
		}
	}
	return 0, false
}

// RequirementRoot returns a requirement's applicability expression root.
func (d *Document) RequirementRoot(requirement schema.RequirementID) (schema.NodeID, bool) {
	i, ok := d.requirementIndex(requirement)
	if !ok || i >= uint64(len(d.RequirementApplicabilityRoots)) {
		return 0, false
	}
	return d.RequirementApplicabilityRoots[i], true
}

// RequirementClauses returns a read-only view of a requirement's ClauseIDs.
func (d *Document) RequirementClauses(requirement schema.RequirementID) ([]schema.ClauseID, bool) {
	i, ok := d.requirementIndex(requirement)
	if !ok {
		return nil, false
	}
	start, count, ok := rangeAt(d.RequirementClauseStarts, d.RequirementClauseCounts, i, len(d.RequirementClauseIDs))
	if !ok {
		return nil, false
	}
	return d.RequirementClauseIDs[int(start):int(start+count)], true
}

// RequirementSpan returns a requirement's source range.
func (d *Document) RequirementSpan(requirement schema.RequirementID) (SourceSpan, bool) {
	i, ok := d.requirementIndex(requirement)
	if !ok {
		return SourceSpan{}, false
	}
	return spanAt(d.RequirementSourceStarts, d.RequirementSourceEnds, i, len(d.InputBytes))
}
