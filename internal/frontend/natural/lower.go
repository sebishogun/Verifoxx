package natural

import (
	"fmt"

	public "github.com/sebishogun/nornrune/frontend/natural"
	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/security"
	nornrunepolicy "github.com/sebishogun/nornrune/policies/nornrune"
)

// Lowerer owns reusable review, decode, validation, and lowering scratch. It is
// not safe for concurrent use. Published Programs never borrow it.
type Lowerer struct {
	reviewer      Reviewer
	validator     compile.Validator
	diagnostics   []compile.Diagnostic
	semanticKinds []public.SemanticKind
	semanticIDs   []uint32
	semanticSlots []uint64
	builder       ast.Builder
	decoder       jsonpolicy.Decoder
	lowerer       compile.Lowerer
}

// Compile authenticates and compiles one reviewer-owned native policy draft.
// On any error or diagnostic, dst is unchanged.
func (lowerer *Lowerer) Compile(
	dst *program.Program,
	document *public.Document,
	proposal *public.Proposal,
	draft *public.ReviewedDraft,
	token public.ApprovalToken,
	verifier public.Verifier,
	nowUnix, maxClockSkew int64,
	limits public.Limits,
) ([]compile.Diagnostic, error) {
	if lowerer == nil || dst == nil || draft == nil {
		return nil, public.ErrInvalidDraft
	}
	if err := lowerer.reviewer.VerifyApproval(document, proposal, draft, token, verifier, nowUnix, maxClockSkew, limits); err != nil {
		return nil, err
	}

	fields, symbols, err := nornrunepolicy.NewSchema()
	if err != nil {
		return nil, fmt.Errorf("%w: schema", public.ErrInvalidDraft)
	}
	if err := lowerer.decoder.Decode(&lowerer.builder, draft.PolicySource, fields, symbols, naturalPolicyLimits(limits)); err != nil {
		return nil, fmt.Errorf("%w: decode", public.ErrInvalidDraft)
	}
	policy := lowerer.builder.Document()
	if !lowerer.matchesReviewedSemantics(policy, draft) {
		return nil, public.ErrInvalidDraft
	}
	lowerer.diagnostics = lowerer.validator.Validate(lowerer.diagnostics[:0], policy, fields)
	if len(lowerer.diagnostics) != 0 {
		return lowerer.diagnostics, nil
	}
	if err := lowerer.lowerer.Lower(dst, policy, fields, symbols); err != nil {
		return nil, fmt.Errorf("%w: lower", public.ErrInvalidDraft)
	}
	return lowerer.diagnostics, nil
}

func naturalPolicyLimits(limits public.Limits) jsonpolicy.Limits {
	maxSourceBytes := security.MaximumPolicyBytes
	if limits.MaxDraftBytes != 0 && uint64(limits.MaxDraftBytes) < uint64(maxSourceBytes) {
		maxSourceBytes = int(limits.MaxDraftBytes)
	}
	return jsonpolicy.Limits{
		MaxSourceBytes:   maxSourceBytes,
		MaxCatalogItems:  1024,
		MaxStringBytes:   1 << 20,
		MaxDepth:         security.MaximumASTDepth,
		MaxNodes:         security.MaximumASTNodes,
		MaxValues:        1 << 17,
		MaxArrayItems:    1 << 16,
		MaxSymbolBytes:   4 << 20,
		MaxRequirements:  1024,
		MaxClauses:       1 << 13,
		MaxTemplateBytes: 1 << 20,
		MaxAssumptions:   1024,
		MaxUncertainty:   1024,
	}
}

func (lowerer *Lowerer) matchesReviewedSemantics(document *ast.Document, draft *public.ReviewedDraft) bool {
	if lowerer == nil || document == nil || draft == nil {
		return false
	}
	lowerer.semanticKinds, lowerer.semanticIDs = appendPolicySemanticRows(
		lowerer.semanticKinds[:0], lowerer.semanticIDs[:0], document,
	)
	if len(lowerer.semanticKinds) != len(draft.SemanticKinds) || len(lowerer.semanticIDs) != len(draft.SemanticIDs) {
		return false
	}
	lowerer.semanticSlots = resizeUint64(lowerer.semanticSlots, reviewHashTableSize(len(lowerer.semanticKinds)))
	for row, kind := range lowerer.semanticKinds {
		if !kind.Valid() || lowerer.semanticIDs[row] == 0 ||
			insertSemanticKey(lowerer.semanticSlots, semanticKey(kind, lowerer.semanticIDs[row])) {
			return false
		}
	}
	clear(lowerer.semanticSlots)
	for row, kind := range draft.SemanticKinds {
		if !kind.Valid() || draft.SemanticIDs[row] == 0 ||
			insertSemanticKey(lowerer.semanticSlots, semanticKey(kind, draft.SemanticIDs[row])) {
			return false
		}
	}
	for row, kind := range lowerer.semanticKinds {
		if !containsSemanticKey(lowerer.semanticSlots, semanticKey(kind, lowerer.semanticIDs[row])) {
			return false
		}
	}
	return true
}

func appendPolicySemanticRows(
	kinds []public.SemanticKind,
	ids []uint32,
	document *ast.Document,
) ([]public.SemanticKind, []uint32) {
	for _, id := range document.RequirementIDs {
		kinds = append(kinds, public.SemanticKindRequirement)
		ids = append(ids, uint32(id))
	}
	for _, id := range document.RequirementIDs {
		kinds = append(kinds, public.SemanticKindApplicability)
		ids = append(ids, uint32(id))
	}
	for row := range document.ClauseAssertionRoots {
		kinds = append(kinds, public.SemanticKindClause)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.ClauseAssertionRoots {
		kinds = append(kinds, public.SemanticKindAssertion)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.ClauseEvidenceNodeIDs {
		kinds = append(kinds, public.SemanticKindEvidence)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.ClauseAssertionRoots {
		kinds = append(kinds, public.SemanticKindResolution)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.RemediationKinds {
		kinds = append(kinds, public.SemanticKindRemediation)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.ExplanationRationaleIDs {
		kinds = append(kinds, public.SemanticKindExplanation)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.OutcomeNames {
		kinds = append(kinds, public.SemanticKindOutcome)
		ids = append(ids, uint32(row+1))
	}
	for row := range document.AssumptionTemplateIDs {
		kinds = append(kinds, public.SemanticKindAssumption)
		ids = append(ids, uint32(row+1))
	}
	return kinds, ids
}

func containsSemanticKey(slots []uint64, key uint64) bool {
	mask := uint64(len(slots) - 1)
	slot := key * 11400714819323198485 & mask
	for slots[slot] != 0 {
		if slots[slot] == key {
			return true
		}
		slot = (slot + 1) & mask
	}
	return false
}
