package diff

import (
	"bytes"
	"slices"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

// semanticIdentity reports exact equality of behavior-bearing Program slabs.
func semanticIdentity(oldProgram, newProgram *program.Program) bool {
	if oldProgram == nil || newProgram == nil || !semanticSymbolsEqual(oldProgram, newProgram) || !semanticValuesEqual(oldProgram, newProgram) {
		return false
	}
	oldOutcomes, newOutcomes := oldProgram.Outcomes, newProgram.Outcomes
	oldRemediations, newRemediations := oldProgram.Remediations, newProgram.Remediations
	oldResolutions, newResolutions := oldProgram.Resolutions, newProgram.Resolutions
	return slices.Equal(oldProgram.Opcodes, newProgram.Opcodes) &&
		slices.Equal(oldProgram.Fields, newProgram.Fields) &&
		slices.Equal(oldProgram.Values, newProgram.Values) &&
		slices.Equal(oldProgram.ListStarts, newProgram.ListStarts) &&
		slices.Equal(oldProgram.ListCounts, newProgram.ListCounts) &&
		slices.Equal(oldProgram.OperandStarts, newProgram.OperandStarts) &&
		slices.Equal(oldProgram.OperandCounts, newProgram.OperandCounts) &&
		slices.Equal(oldProgram.EvidenceKinds, newProgram.EvidenceKinds) &&
		slices.Equal(oldProgram.EvidenceStates, newProgram.EvidenceStates) &&
		slices.Equal(oldProgram.EvidenceSubjects, newProgram.EvidenceSubjects) &&
		slices.Equal(oldProgram.EvidenceScopes, newProgram.EvidenceScopes) &&
		slices.Equal(oldProgram.EvidenceTimings, newProgram.EvidenceTimings) &&
		slices.Equal(oldProgram.RootFlags, newProgram.RootFlags) &&
		slices.Equal(oldProgram.TruthSlots, newProgram.TruthSlots) &&
		slices.Equal(oldProgram.ReasonSlots, newProgram.ReasonSlots) &&
		slices.Equal(oldProgram.InstructionNodes, newProgram.InstructionNodes) &&
		slices.Equal(oldProgram.ListValues, newProgram.ListValues) &&
		slices.Equal(oldProgram.Operands, newProgram.Operands) &&
		slices.Equal(oldProgram.OpcodeRunOpcodes, newProgram.OpcodeRunOpcodes) &&
		slices.Equal(oldProgram.OpcodeRunStarts, newProgram.OpcodeRunStarts) &&
		slices.Equal(oldProgram.OpcodeRunCounts, newProgram.OpcodeRunCounts) &&
		bytes.Equal(oldProgram.TemplateBytes, newProgram.TemplateBytes) &&
		slices.Equal(oldProgram.TemplateOpStarts, newProgram.TemplateOpStarts) &&
		slices.Equal(oldProgram.TemplateOpCounts, newProgram.TemplateOpCounts) &&
		slices.Equal(oldProgram.TemplateLiteralStarts, newProgram.TemplateLiteralStarts) &&
		slices.Equal(oldProgram.TemplateMaxBytes, newProgram.TemplateMaxBytes) &&
		slices.Equal(oldProgram.TemplateOps, newProgram.TemplateOps) &&
		slices.Equal(oldProgram.TemplateArgs, newProgram.TemplateArgs) &&
		slices.Equal(oldProgram.ExplanationRationaleTemplateIDs, newProgram.ExplanationRationaleTemplateIDs) &&
		slices.Equal(oldProgram.ExplanationUncertaintyStarts, newProgram.ExplanationUncertaintyStarts) &&
		slices.Equal(oldProgram.ExplanationUncertaintyCounts, newProgram.ExplanationUncertaintyCounts) &&
		slices.Equal(oldProgram.ExplanationUncertaintyTemplateIDs, newProgram.ExplanationUncertaintyTemplateIDs) &&
		slices.Equal(oldProgram.AssumptionTemplateIDs, newProgram.AssumptionTemplateIDs) &&
		slices.Equal(oldProgram.EvidenceIssueNodeIDs, newProgram.EvidenceIssueNodeIDs) &&
		slices.Equal(oldProgram.EvidenceIssueTemplateIDs, newProgram.EvidenceIssueTemplateIDs) &&
		slices.Equal(oldProgram.FieldNames, newProgram.FieldNames) &&
		slices.Equal(oldProgram.FieldKinds, newProgram.FieldKinds) &&
		slices.Equal(oldProgram.FieldGroups, newProgram.FieldGroups) &&
		slices.Equal(oldProgram.EvidenceKindNames, newProgram.EvidenceKindNames) &&
		slices.Equal(oldProgram.EvidenceStateNames, newProgram.EvidenceStateNames) &&
		slices.Equal(oldProgram.RequirementIDs, newProgram.RequirementIDs) &&
		slices.Equal(oldProgram.RequirementRoots, newProgram.RequirementRoots) &&
		slices.Equal(oldProgram.RequirementSourceNodeIDs, newProgram.RequirementSourceNodeIDs) &&
		slices.Equal(oldProgram.RequirementClauseStarts, newProgram.RequirementClauseStarts) &&
		slices.Equal(oldProgram.RequirementClauseCounts, newProgram.RequirementClauseCounts) &&
		slices.Equal(oldProgram.RequirementClauseIDs, newProgram.RequirementClauseIDs) &&
		slices.Equal(oldProgram.ClauseAssertionRoots, newProgram.ClauseAssertionRoots) &&
		slices.Equal(oldProgram.ClauseAssertionSourceNodeIDs, newProgram.ClauseAssertionSourceNodeIDs) &&
		slices.Equal(oldProgram.ClauseEvidenceStarts, newProgram.ClauseEvidenceStarts) &&
		slices.Equal(oldProgram.ClauseEvidenceCounts, newProgram.ClauseEvidenceCounts) &&
		slices.Equal(oldProgram.ClauseEvidenceIDs, newProgram.ClauseEvidenceIDs) &&
		slices.Equal(oldProgram.ClauseEvidenceSourceNodeIDs, newProgram.ClauseEvidenceSourceNodeIDs) &&
		slices.Equal(oldProgram.ClauseOnSatisfied, newProgram.ClauseOnSatisfied) &&
		slices.Equal(oldProgram.ClauseOnFalse, newProgram.ClauseOnFalse) &&
		slices.Equal(oldProgram.ClauseExplanationIDs, newProgram.ClauseExplanationIDs) &&
		slices.Equal(oldProgram.ClauseRemediationStarts, newProgram.ClauseRemediationStarts) &&
		slices.Equal(oldProgram.ClauseRemediationCounts, newProgram.ClauseRemediationCounts) &&
		slices.Equal(oldProgram.ClauseRemediationIDs, newProgram.ClauseRemediationIDs) &&
		slices.Equal(oldOutcomes.Names, newOutcomes.Names) &&
		slices.Equal(oldOutcomes.Precedence, newOutcomes.Precedence) &&
		slices.Equal(oldOutcomes.Terminal, newOutcomes.Terminal) &&
		slices.Equal(oldRemediations.Kinds, newRemediations.Kinds) &&
		slices.Equal(oldRemediations.Fields, newRemediations.Fields) &&
		slices.Equal(oldRemediations.Values, newRemediations.Values) &&
		slices.Equal(oldRemediations.EvidenceKinds, newRemediations.EvidenceKinds) &&
		slices.Equal(oldResolutions.OutcomeIDs, newResolutions.OutcomeIDs) &&
		slices.Equal(oldResolutions.ExplanationIDs, newResolutions.ExplanationIDs) &&
		slices.Equal(oldResolutions.RemediationStarts, newResolutions.RemediationStarts) &&
		slices.Equal(oldResolutions.RemediationCounts, newResolutions.RemediationCounts) &&
		slices.Equal(oldResolutions.RemediationIDs, newResolutions.RemediationIDs) &&
		oldProgram.TruthSlotCount == newProgram.TruthSlotCount &&
		oldProgram.ReasonSlotCount == newProgram.ReasonSlotCount
}

func semanticSymbolsEqual(oldProgram, newProgram *program.Program) bool {
	pairsEqual := func(oldIDs, newIDs []schema.SymbolID) bool {
		if len(oldIDs) != len(newIDs) {
			return false
		}
		for row, oldID := range oldIDs {
			if oldID == 0 || newIDs[row] == 0 {
				if oldID != newIDs[row] {
					return false
				}
				continue
			}
			oldSymbol, oldOK := oldProgram.Symbol(oldID)
			newSymbol, newOK := newProgram.Symbol(newIDs[row])
			if !oldOK || !newOK || !bytes.Equal(oldSymbol, newSymbol) {
				return false
			}
		}
		return true
	}
	if !pairsEqual(oldProgram.FieldNames, newProgram.FieldNames) ||
		!pairsEqual(oldProgram.EvidenceKindNames, newProgram.EvidenceKindNames) ||
		!pairsEqual(oldProgram.EvidenceStateNames, newProgram.EvidenceStateNames) ||
		!pairsEqual(oldProgram.EvidenceSubjects, newProgram.EvidenceSubjects) ||
		!pairsEqual(oldProgram.EvidenceScopes, newProgram.EvidenceScopes) ||
		!pairsEqual(oldProgram.EvidenceTimings, newProgram.EvidenceTimings) ||
		!pairsEqual(oldProgram.Outcomes.Names, newProgram.Outcomes.Names) {
		return false
	}
	return true
}

func semanticValuesEqual(oldProgram, newProgram *program.Program) bool {
	if !slices.Equal(oldProgram.Values, newProgram.Values) ||
		!slices.Equal(oldProgram.ListValues, newProgram.ListValues) ||
		!slices.Equal(oldProgram.Remediations.Values, newProgram.Remediations.Values) {
		return false
	}
	for _, ids := range [][]schema.ValueID{oldProgram.Values, oldProgram.ListValues, oldProgram.Remediations.Values} {
		for _, id := range ids {
			if !semanticValueEqual(oldProgram, newProgram, id) {
				return false
			}
		}
	}
	return true
}

func semanticValueEqual(oldProgram, newProgram *program.Program, id schema.ValueID) bool {
	return semanticValuePairEqual(oldProgram, newProgram, id, id)
}

func semanticValuePairEqual(oldProgram, newProgram *program.Program, oldID, newID schema.ValueID) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldRow, newRow := int(oldID-1), int(newID-1)
	if oldRow < 0 || newRow < 0 || oldRow >= len(oldProgram.ValueKinds) || newRow >= len(newProgram.ValueKinds) ||
		oldRow >= len(oldProgram.ValueRefs) || newRow >= len(newProgram.ValueRefs) {
		return false
	}
	kind := oldProgram.ValueKinds[oldRow]
	if kind != newProgram.ValueKinds[newRow] {
		return false
	}
	oldRef, newRef := oldProgram.ValueRefs[oldRow], newProgram.ValueRefs[newRow]
	switch kind {
	case schema.ValueKindSymbol:
		oldSymbol, oldOK := oldProgram.Symbol(schema.SymbolID(oldRef))
		newSymbol, newOK := newProgram.Symbol(schema.SymbolID(newRef))
		return oldOK && newOK && bytes.Equal(oldSymbol, newSymbol)
	case schema.ValueKindInteger:
		return payloadEqual(oldProgram.IntegerValues, newProgram.IntegerValues, oldRef, newRef)
	case schema.ValueKindBoolean:
		return payloadEqual(oldProgram.BooleanValues, newProgram.BooleanValues, oldRef, newRef)
	case schema.ValueKindTimestamp:
		return payloadEqual(oldProgram.TimestampValues, newProgram.TimestampValues, oldRef, newRef)
	case schema.ValueKindPresence:
		return oldRef == newRef
	default:
		return false
	}
}

func symbolPairEqual(oldProgram, newProgram *program.Program, oldID, newID schema.SymbolID) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldSymbol, oldOK := oldProgram.Symbol(oldID)
	newSymbol, newOK := newProgram.Symbol(newID)
	return oldOK && newOK && bytes.Equal(oldSymbol, newSymbol)
}

func payloadEqual[T comparable](oldValues, newValues []T, oldRef, newRef uint32) bool {
	if oldRef == 0 || newRef == 0 || uint64(oldRef) > uint64(len(oldValues)) || uint64(newRef) > uint64(len(newValues)) {
		return false
	}
	return oldValues[oldRef-1] == newValues[newRef-1]
}
