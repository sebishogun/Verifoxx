package compile

import (
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

const maxNamespacedUint32Bytes = 11

func runtimeTemplateOp(op ast.TemplateOp) (result.TemplateOp, bool) {
	switch op {
	case ast.TemplateOpLiteral:
		return result.TemplateOpLiteral, true
	case ast.TemplateOpPolicyName:
		return result.TemplateOpPolicyName, true
	case ast.TemplateOpPolicyVersion:
		return result.TemplateOpPolicyVersion, true
	case ast.TemplateOpRequestID:
		return result.TemplateOpRequestID, true
	case ast.TemplateOpOutcome:
		return result.TemplateOpOutcome, true
	case ast.TemplateOpRequirementID:
		return result.TemplateOpRequirementID, true
	case ast.TemplateOpClauseID:
		return result.TemplateOpClauseID, true
	case ast.TemplateOpNodeID:
		return result.TemplateOpNodeID, true
	case ast.TemplateOpReason:
		return result.TemplateOpReason, true
	case ast.TemplateOpEvidenceKind:
		return result.TemplateOpEvidenceKind, true
	case ast.TemplateOpEvidenceState:
		return result.TemplateOpEvidenceState, true
	case ast.TemplateOpRequiredEvidenceState:
		return result.TemplateOpRequiredEvidenceState, true
	case ast.TemplateOpEvidenceID:
		return result.TemplateOpEvidenceID, true
	default:
		return result.TemplateOpInvalid, false
	}
}

func programSymbolLength(dst *program.Program, id schema.SymbolID) (uint32, bool) {
	value, ok := dst.Symbol(id)
	if !ok || uint64(len(value)) > uint64(^uint32(0)) {
		return 0, false
	}
	return uint32(len(value)), true
}

func maxProgramSymbolLength(dst *program.Program, ids []schema.SymbolID) (uint32, bool) {
	var maximum uint32
	for _, id := range ids {
		length, ok := programSymbolLength(dst, id)
		if !ok {
			return 0, false
		}
		if length > maximum {
			maximum = length
		}
	}
	return maximum, len(ids) != 0
}

func templateOpMaximum(dst *program.Program, op result.TemplateOp, literal uint32) (uint32, bool) {
	switch op {
	case result.TemplateOpLiteral:
		return literal, literal != 0
	case result.TemplateOpPolicyName:
		return programSymbolLength(dst, dst.PolicyName)
	case result.TemplateOpPolicyVersion:
		return programSymbolLength(dst, dst.PolicyVersion)
	case result.TemplateOpRequestID, result.TemplateOpRequirementID, result.TemplateOpClauseID,
		result.TemplateOpNodeID, result.TemplateOpEvidenceID:
		return maxNamespacedUint32Bytes, true
	case result.TemplateOpOutcome:
		return maxProgramSymbolLength(dst, dst.Outcomes.Names)
	case result.TemplateOpReason:
		return uint32(len("wrong_subject")), true
	case result.TemplateOpEvidenceKind:
		return maxProgramSymbolLength(dst, dst.EvidenceKindNames)
	case result.TemplateOpEvidenceState, result.TemplateOpRequiredEvidenceState:
		return maxProgramSymbolLength(dst, dst.EvidenceStateNames)
	default:
		return 0, false
	}
}

func resetExplanationColumns(dst *program.Program) {
	dst.TemplateBytes = dst.TemplateBytes[:0]
	dst.TemplateOpStarts = dst.TemplateOpStarts[:0]
	dst.TemplateOpCounts = dst.TemplateOpCounts[:0]
	dst.TemplateLiteralStarts = dst.TemplateLiteralStarts[:0]
	dst.TemplateMaxBytes = dst.TemplateMaxBytes[:0]
	dst.TemplateOps = dst.TemplateOps[:0]
	dst.TemplateArgs = dst.TemplateArgs[:0]
	dst.ExplanationRationaleTemplateIDs = dst.ExplanationRationaleTemplateIDs[:0]
	dst.ExplanationUncertaintyStarts = dst.ExplanationUncertaintyStarts[:0]
	dst.ExplanationUncertaintyCounts = dst.ExplanationUncertaintyCounts[:0]
	dst.ExplanationUncertaintyTemplateIDs = dst.ExplanationUncertaintyTemplateIDs[:0]
	dst.AssumptionTemplateIDs = dst.AssumptionTemplateIDs[:0]
	dst.EvidenceIssueNodeIDs = dst.EvidenceIssueNodeIDs[:0]
	dst.EvidenceIssueTemplateIDs = dst.EvidenceIssueTemplateIDs[:0]
}

func (l *Lowerer) lowerExplanationTables(dst *program.Program, doc *ast.Document) error {
	rows := len(doc.TemplateOpStarts)
	if len(doc.TemplateOpCounts) != rows || len(doc.TemplateLiteralStarts) != rows ||
		len(doc.TemplateMaxBytes) != rows || len(doc.TemplateContexts) != rows ||
		len(doc.TemplateOps) != len(doc.TemplateArgs) {
		return ErrInvalidDocument
	}
	dst.TemplateBytes = resizeSlots(dst.TemplateBytes, len(doc.TemplateBytes))
	copy(dst.TemplateBytes, doc.TemplateBytes)
	dst.TemplateOpStarts = resizeSlots(dst.TemplateOpStarts, rows)
	copy(dst.TemplateOpStarts, doc.TemplateOpStarts)
	dst.TemplateOpCounts = resizeSlots(dst.TemplateOpCounts, rows)
	copy(dst.TemplateOpCounts, doc.TemplateOpCounts)
	dst.TemplateLiteralStarts = resizeSlots(dst.TemplateLiteralStarts, rows)
	copy(dst.TemplateLiteralStarts, doc.TemplateLiteralStarts)
	dst.TemplateMaxBytes = resizeSlots(dst.TemplateMaxBytes, rows)
	dst.TemplateOps = resizeSlots(dst.TemplateOps, len(doc.TemplateOps))
	dst.TemplateArgs = resizeSlots(dst.TemplateArgs, len(doc.TemplateArgs))
	copy(dst.TemplateArgs, doc.TemplateArgs)
	for i, op := range doc.TemplateOps {
		runtimeOp, ok := runtimeTemplateOp(op)
		if !ok {
			return ErrInvalidDocument
		}
		dst.TemplateOps[i] = runtimeOp
	}
	for row := 0; row < rows; row++ {
		start := uint64(dst.TemplateOpStarts[row])
		count := uint64(dst.TemplateOpCounts[row])
		end := start + count
		if count > result.MaxTemplateOperations || end > uint64(len(dst.TemplateOps)) {
			return ErrInvalidDocument
		}
		var maximum uint64
		for i := start; i < end; i++ {
			value, ok := templateOpMaximum(dst, dst.TemplateOps[int(i)], dst.TemplateArgs[int(i)])
			if !ok {
				return ErrInvalidGeneratedProgram
			}
			maximum += uint64(value)
			if maximum > result.MaxRenderedTemplateBytes {
				return ErrProgramTooLarge
			}
		}
		dst.TemplateMaxBytes[row] = uint32(maximum)
	}

	explanations := len(doc.ExplanationRationaleIDs)
	if len(doc.ExplanationUncertaintyStarts) != explanations || len(doc.ExplanationUncertaintyCounts) != explanations {
		return ErrInvalidDocument
	}
	dst.ExplanationRationaleTemplateIDs = resizeSlots(dst.ExplanationRationaleTemplateIDs, explanations)
	copy(dst.ExplanationRationaleTemplateIDs, doc.ExplanationRationaleIDs)
	dst.ExplanationUncertaintyStarts = resizeSlots(dst.ExplanationUncertaintyStarts, explanations)
	copy(dst.ExplanationUncertaintyStarts, doc.ExplanationUncertaintyStarts)
	dst.ExplanationUncertaintyCounts = resizeSlots(dst.ExplanationUncertaintyCounts, explanations)
	copy(dst.ExplanationUncertaintyCounts, doc.ExplanationUncertaintyCounts)
	dst.ExplanationUncertaintyTemplateIDs = resizeSlots(dst.ExplanationUncertaintyTemplateIDs, len(doc.ExplanationUncertaintyIDs))
	copy(dst.ExplanationUncertaintyTemplateIDs, doc.ExplanationUncertaintyIDs)
	dst.AssumptionTemplateIDs = resizeSlots(dst.AssumptionTemplateIDs, len(doc.AssumptionTemplateIDs))
	copy(dst.AssumptionTemplateIDs, doc.AssumptionTemplateIDs)

	evidenceRows := len(doc.EvidenceKinds)
	if uint64(evidenceRows)*result.EvidenceIssueTemplateCount != uint64(len(doc.EvidenceIssueTemplateIDs)) {
		return ErrInvalidDocument
	}
	dst.EvidenceIssueNodeIDs = resizeSlots(dst.EvidenceIssueNodeIDs, evidenceRows)
	dst.EvidenceIssueTemplateIDs = resizeSlots(dst.EvidenceIssueTemplateIDs, len(doc.EvidenceIssueTemplateIDs))
	filled := 0
	for nodeRow, kind := range doc.NodeKinds {
		if kind != ast.NodeKindEvidence {
			continue
		}
		if nodeRow >= len(doc.NodeRefs) || filled >= evidenceRows {
			return ErrInvalidDocument
		}
		payload := uint64(doc.NodeRefs[nodeRow])
		start := payload * result.EvidenceIssueTemplateCount
		end := start + result.EvidenceIssueTemplateCount
		if payload >= uint64(evidenceRows) || end > uint64(len(doc.EvidenceIssueTemplateIDs)) {
			return ErrInvalidDocument
		}
		dst.EvidenceIssueNodeIDs[filled] = schema.NodeID(nodeRow + 1)
		copy(dst.EvidenceIssueTemplateIDs[filled*result.EvidenceIssueTemplateCount:(filled+1)*result.EvidenceIssueTemplateCount],
			doc.EvidenceIssueTemplateIDs[int(start):int(end)])
		filled++
	}
	if filled != evidenceRows {
		return ErrInvalidDocument
	}
	return nil
}
