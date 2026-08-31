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
	reviewer    Reviewer
	validator   compile.Validator
	diagnostics []compile.Diagnostic
	builder     ast.Builder
	decoder     jsonpolicy.Decoder
	lowerer     compile.Lowerer
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
	if !matchesReviewedRequirements(policy, draft) {
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

func matchesReviewedRequirements(document *ast.Document, draft *public.ReviewedDraft) bool {
	if document == nil || len(document.RequirementIDs) != len(draft.RequirementIDs) {
		return false
	}
	for _, requirement := range document.RequirementIDs {
		found := false
		for _, reviewed := range draft.RequirementIDs {
			if uint32(requirement) == reviewed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
