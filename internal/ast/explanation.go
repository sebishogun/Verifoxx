package ast

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/schema"
)

const (
	MaxAssumptions = 8
	MaxUncertainty = 8
)

var (
	ErrAssumptionsAlreadySet    = errors.New("ast: policy assumptions already set")
	ErrEvidenceIssuesAlreadySet = errors.New("ast: evidence issue templates already set")
	ErrInvalidExplanation       = errors.New("ast: invalid explanation ID")
	ErrTooManyAssumptions       = errors.New("ast: too many assumptions")
	ErrTooManyExplanations      = errors.New("ast: too many explanations")
	ErrTooManyUncertainty       = errors.New("ast: too many uncertainty templates")
)

// EvidenceIssueReason defines the fixed storage order for evidence issue
// templates. The compiler maps these values to result.ReasonID explicitly.
type EvidenceIssueReason uint8

const (
	EvidenceIssueMissing EvidenceIssueReason = iota
	EvidenceIssueStale
	EvidenceIssueUnclear
	EvidenceIssueUnverifiable
	EvidenceIssueWrongScope
	EvidenceIssueWrongSubject
	EvidenceIssueWrongTiming
	EvidenceIssueInvalid
	EvidenceIssueConflict
)

const EvidenceIssueReasonCount = 9

const ResolutionBranchCount = 7

func (d *Document) templateContext(id schema.TemplateID) (TemplateContext, bool) {
	if id == 0 {
		return TemplateContextInvalid, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.TemplateContexts)) {
		return TemplateContextInvalid, false
	}
	return d.TemplateContexts[i], true
}

// SetAssumptions copies one complete source-ordered assumption list. An empty
// list is distinct from a list that has not been supplied.
func (b *Builder) SetAssumptions(ids []schema.TemplateID) error {
	d := &b.doc
	if len(d.AssumptionsSet) != 0 {
		return ErrAssumptionsAlreadySet
	}
	if len(ids) > MaxAssumptions {
		return ErrTooManyAssumptions
	}
	for _, id := range ids {
		context, ok := d.templateContext(id)
		if !ok {
			return ErrInvalidTemplate
		}
		if context != TemplateContextAssumption {
			return ErrInvalidTemplateContext
		}
	}
	d.AssumptionTemplateIDs = append(d.AssumptionTemplateIDs, ids...)
	d.AssumptionsSet = append(d.AssumptionsSet, 1)
	return nil
}

// Assumptions returns the borrowed source-ordered template IDs and whether the
// policy supplied the required list.
func (d *Document) Assumptions() ([]schema.TemplateID, bool) {
	if len(d.AssumptionsSet) != 1 || d.AssumptionsSet[0] != 1 {
		return nil, false
	}
	return d.AssumptionTemplateIDs, true
}

// AddExplanation appends one rationale and its source-ordered uncertainty
// templates. Every template in a row uses the same decision context.
func (b *Builder) AddExplanation(rationale schema.TemplateID, uncertainty []schema.TemplateID) (schema.ExplanationID, error) {
	d := &b.doc
	if len(uncertainty) > MaxUncertainty || uint64(len(d.ExplanationUncertaintyIDs))+uint64(len(uncertainty)) > uint64(math.MaxUint32) {
		return 0, ErrTooManyUncertainty
	}
	if uint64(len(d.ExplanationRationaleIDs)) >= uint64(math.MaxUint32) {
		return 0, ErrTooManyExplanations
	}
	context, ok := d.templateContext(rationale)
	if !ok {
		return 0, ErrInvalidExplanation
	}
	if context != TemplateContextDecision && context != TemplateContextUnresolved {
		return 0, ErrInvalidTemplateContext
	}
	for _, id := range uncertainty {
		uncertaintyContext, valid := d.templateContext(id)
		if !valid {
			return 0, ErrInvalidExplanation
		}
		if uncertaintyContext != context {
			return 0, ErrInvalidTemplateContext
		}
	}
	start := uint32(len(d.ExplanationUncertaintyIDs))
	d.ExplanationUncertaintyIDs = append(d.ExplanationUncertaintyIDs, uncertainty...)
	d.ExplanationRationaleIDs = append(d.ExplanationRationaleIDs, rationale)
	d.ExplanationUncertaintyStarts = append(d.ExplanationUncertaintyStarts, start)
	d.ExplanationUncertaintyCounts = append(d.ExplanationUncertaintyCounts, uint16(len(uncertainty)))
	return schema.ExplanationID(len(d.ExplanationRationaleIDs)), nil
}

// Explanation returns one rationale and a borrowed uncertainty range.
func (d *Document) Explanation(id schema.ExplanationID) (schema.TemplateID, []schema.TemplateID, bool) {
	if id == 0 {
		return 0, nil, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.ExplanationRationaleIDs)) {
		return 0, nil, false
	}
	start, count, ok := rangeAt(d.ExplanationUncertaintyStarts, d.ExplanationUncertaintyCounts, i, len(d.ExplanationUncertaintyIDs))
	if !ok {
		return 0, nil, false
	}
	return d.ExplanationRationaleIDs[i], d.ExplanationUncertaintyIDs[int(start):int(start+count)], true
}

// SetEvidenceIssueTemplates copies one fixed reason-order row for an evidence
// source node. Missing must use a missing-safe context; all other rows may use
// either that fallback context or a non-missing override.
func (b *Builder) SetEvidenceIssueTemplates(node schema.NodeID, templates [EvidenceIssueReasonCount]schema.TemplateID) error {
	d := &b.doc
	i, ok := d.nodeIndex(node)
	if !ok || d.NodeKinds[i] != NodeKindEvidence {
		return ErrInvalidEvidence
	}
	row := uint64(d.NodeRefs[i])
	start := row * uint64(EvidenceIssueReasonCount)
	end := start + uint64(EvidenceIssueReasonCount)
	if end > uint64(len(d.EvidenceIssueTemplateIDs)) {
		return ErrInvalidEvidence
	}
	for _, id := range d.EvidenceIssueTemplateIDs[int(start):int(end)] {
		if id != 0 {
			return ErrEvidenceIssuesAlreadySet
		}
	}
	for reason, id := range templates {
		context, valid := d.templateContext(id)
		if !valid {
			return ErrInvalidTemplate
		}
		if reason == int(EvidenceIssueMissing) {
			if context != TemplateContextEvidenceMissing {
				return ErrInvalidTemplateContext
			}
			continue
		}
		if context != TemplateContextEvidenceMissing && context != TemplateContextEvidencePresent {
			return ErrInvalidTemplateContext
		}
	}
	copy(d.EvidenceIssueTemplateIDs[int(start):int(end)], templates[:])
	return nil
}

// EvidenceIssueTemplates returns a borrowed fixed reason-order row only after
// all entries have been supplied.
func (d *Document) EvidenceIssueTemplates(node schema.NodeID) ([]schema.TemplateID, bool) {
	i, ok := d.nodeIndex(node)
	if !ok || d.NodeKinds[i] != NodeKindEvidence {
		return nil, false
	}
	start := uint64(d.NodeRefs[i]) * uint64(EvidenceIssueReasonCount)
	end := start + uint64(EvidenceIssueReasonCount)
	if end > uint64(len(d.EvidenceIssueTemplateIDs)) {
		return nil, false
	}
	row := d.EvidenceIssueTemplateIDs[int(start):int(end)]
	for _, id := range row {
		if id == 0 {
			return nil, false
		}
	}
	return row, true
}

func (d *Document) explanationContext(id schema.ExplanationID) (TemplateContext, bool) {
	rationale, _, ok := d.Explanation(id)
	if !ok {
		return TemplateContextInvalid, false
	}
	return d.templateContext(rationale)
}

func (b *Builder) validateResolutionExplanations(resolution Resolution) error {
	decision := [...]schema.ExplanationID{resolution.OnSatisfiedExplanation, resolution.OnFalseExplanation}
	for _, id := range decision {
		if id == 0 {
			continue
		}
		context, ok := b.doc.explanationContext(id)
		if !ok {
			return ErrInvalidExplanation
		}
		if context != TemplateContextDecision {
			return ErrInvalidTemplateContext
		}
	}
	unresolved := [...]schema.ExplanationID{
		resolution.OnMissingExplanation,
		resolution.OnStaleExplanation,
		resolution.OnUnclearExplanation,
		resolution.OnUnverifiableExplanation,
		resolution.OnConflictExplanation,
	}
	for _, id := range unresolved {
		if id == 0 {
			continue
		}
		context, ok := b.doc.explanationContext(id)
		if !ok {
			return ErrInvalidExplanation
		}
		if context != TemplateContextUnresolved {
			return ErrInvalidTemplateContext
		}
	}
	return nil
}

func resolutionExplanationIDs(resolution Resolution) [ResolutionBranchCount]schema.ExplanationID {
	return [ResolutionBranchCount]schema.ExplanationID{
		resolution.OnSatisfiedExplanation,
		resolution.OnFalseExplanation,
		resolution.OnMissingExplanation,
		resolution.OnStaleExplanation,
		resolution.OnUnclearExplanation,
		resolution.OnUnverifiableExplanation,
		resolution.OnConflictExplanation,
	}
}
