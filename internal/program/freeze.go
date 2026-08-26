package program

import (
	"github.com/sebishogun/nornrune/internal/result"
)

func cloneExact[T any](src []T) []T {
	if len(src) == 0 {
		return nil
	}
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}

// Freeze returns an exact-capacity, self-contained copy of src. Result-table
// headers and the Resolver are rebuilt over the copied Program-owned columns.
func Freeze(src *Program) (Program, error) {
	dst := Program{
		Opcodes:                           cloneExact(src.Opcodes),
		Fields:                            cloneExact(src.Fields),
		Values:                            cloneExact(src.Values),
		ListStarts:                        cloneExact(src.ListStarts),
		ListCounts:                        cloneExact(src.ListCounts),
		OperandStarts:                     cloneExact(src.OperandStarts),
		OperandCounts:                     cloneExact(src.OperandCounts),
		EvidenceKinds:                     cloneExact(src.EvidenceKinds),
		EvidenceStates:                    cloneExact(src.EvidenceStates),
		EvidenceSubjects:                  cloneExact(src.EvidenceSubjects),
		EvidenceScopes:                    cloneExact(src.EvidenceScopes),
		EvidenceTimings:                   cloneExact(src.EvidenceTimings),
		RootFlags:                         cloneExact(src.RootFlags),
		TruthSlots:                        cloneExact(src.TruthSlots),
		ReasonSlots:                       cloneExact(src.ReasonSlots),
		InstructionNodes:                  cloneExact(src.InstructionNodes),
		InstructionSourceStarts:           cloneExact(src.InstructionSourceStarts),
		InstructionSourceEnds:             cloneExact(src.InstructionSourceEnds),
		ListValues:                        cloneExact(src.ListValues),
		Operands:                          cloneExact(src.Operands),
		OpcodeRunOpcodes:                  cloneExact(src.OpcodeRunOpcodes),
		OpcodeRunStarts:                   cloneExact(src.OpcodeRunStarts),
		OpcodeRunCounts:                   cloneExact(src.OpcodeRunCounts),
		NodeInstructionStarts:             cloneExact(src.NodeInstructionStarts),
		NodeInstructionCounts:             cloneExact(src.NodeInstructionCounts),
		NodeInstructionIDs:                cloneExact(src.NodeInstructionIDs),
		SymbolBytes:                       cloneExact(src.SymbolBytes),
		SymbolStarts:                      cloneExact(src.SymbolStarts),
		SymbolLengths:                     cloneExact(src.SymbolLengths),
		SymbolHashes:                      cloneExact(src.SymbolHashes),
		SymbolIDs:                         cloneExact(src.SymbolIDs),
		ValueKinds:                        cloneExact(src.ValueKinds),
		ValueRefs:                         cloneExact(src.ValueRefs),
		IntegerValues:                     cloneExact(src.IntegerValues),
		BooleanValues:                     cloneExact(src.BooleanValues),
		TimestampValues:                   cloneExact(src.TimestampValues),
		TemplateBytes:                     cloneExact(src.TemplateBytes),
		TemplateOpStarts:                  cloneExact(src.TemplateOpStarts),
		TemplateOpCounts:                  cloneExact(src.TemplateOpCounts),
		TemplateLiteralStarts:             cloneExact(src.TemplateLiteralStarts),
		TemplateMaxBytes:                  cloneExact(src.TemplateMaxBytes),
		TemplateOps:                       cloneExact(src.TemplateOps),
		TemplateArgs:                      cloneExact(src.TemplateArgs),
		ExplanationRationaleTemplateIDs:   cloneExact(src.ExplanationRationaleTemplateIDs),
		ExplanationUncertaintyStarts:      cloneExact(src.ExplanationUncertaintyStarts),
		ExplanationUncertaintyCounts:      cloneExact(src.ExplanationUncertaintyCounts),
		ExplanationUncertaintyTemplateIDs: cloneExact(src.ExplanationUncertaintyTemplateIDs),
		AssumptionTemplateIDs:             cloneExact(src.AssumptionTemplateIDs),
		EvidenceIssueNodeIDs:              cloneExact(src.EvidenceIssueNodeIDs),
		EvidenceIssueTemplateIDs:          cloneExact(src.EvidenceIssueTemplateIDs),
		FieldNames:                        cloneExact(src.FieldNames),
		FieldKinds:                        cloneExact(src.FieldKinds),
		FieldGroups:                       cloneExact(src.FieldGroups),
		FieldIndex:                        src.FieldIndex.Clone(),
		ApplicabilityIndex:                src.ApplicabilityIndex.Clone(),
		FactIndexSpec:                     src.FactIndexSpec.Clone(),
		EvidenceKindNames:                 cloneExact(src.EvidenceKindNames),
		EvidenceStateNames:                cloneExact(src.EvidenceStateNames),
		EvidenceKindSourceStarts:          cloneExact(src.EvidenceKindSourceStarts),
		EvidenceKindSourceEnds:            cloneExact(src.EvidenceKindSourceEnds),
		EvidenceStateSourceStarts:         cloneExact(src.EvidenceStateSourceStarts),
		EvidenceStateSourceEnds:           cloneExact(src.EvidenceStateSourceEnds),
		OutcomeSourceStarts:               cloneExact(src.OutcomeSourceStarts),
		OutcomeSourceEnds:                 cloneExact(src.OutcomeSourceEnds),
		RemediationSourceStarts:           cloneExact(src.RemediationSourceStarts),
		RemediationSourceEnds:             cloneExact(src.RemediationSourceEnds),
		RequirementIDs:                    cloneExact(src.RequirementIDs),
		RequirementRoots:                  cloneExact(src.RequirementRoots),
		RequirementSourceNodeIDs:          cloneExact(src.RequirementSourceNodeIDs),
		RequirementClauseStarts:           cloneExact(src.RequirementClauseStarts),
		RequirementClauseCounts:           cloneExact(src.RequirementClauseCounts),
		RequirementClauseIDs:              cloneExact(src.RequirementClauseIDs),
		RequirementSourceStarts:           cloneExact(src.RequirementSourceStarts),
		RequirementSourceEnds:             cloneExact(src.RequirementSourceEnds),
		ClauseAssertionRoots:              cloneExact(src.ClauseAssertionRoots),
		ClauseAssertionSourceNodeIDs:      cloneExact(src.ClauseAssertionSourceNodeIDs),
		ClauseEvidenceStarts:              cloneExact(src.ClauseEvidenceStarts),
		ClauseEvidenceCounts:              cloneExact(src.ClauseEvidenceCounts),
		ClauseEvidenceIDs:                 cloneExact(src.ClauseEvidenceIDs),
		ClauseEvidenceSourceNodeIDs:       cloneExact(src.ClauseEvidenceSourceNodeIDs),
		ClauseOnSatisfied:                 cloneExact(src.ClauseOnSatisfied),
		ClauseOnFalse:                     cloneExact(src.ClauseOnFalse),
		ClauseExplanationIDs:              cloneExact(src.ClauseExplanationIDs),
		ClauseRemediationStarts:           cloneExact(src.ClauseRemediationStarts),
		ClauseRemediationCounts:           cloneExact(src.ClauseRemediationCounts),
		ClauseRemediationIDs:              cloneExact(src.ClauseRemediationIDs),
		ClauseSourceStarts:                cloneExact(src.ClauseSourceStarts),
		ClauseSourceEnds:                  cloneExact(src.ClauseSourceEnds),
		InputBytes:                        cloneExact(src.InputBytes),
		ContentHash:                       src.ContentHash,
		PolicyName:                        src.PolicyName,
		PolicyVersion:                     src.PolicyVersion,
		ProgramSymbolCount:                src.ProgramSymbolCount,
		TruthSlotCount:                    src.TruthSlotCount,
		ReasonSlotCount:                   src.ReasonSlotCount,
		Outcomes: result.OutcomeTable{
			Names:      cloneExact(src.Outcomes.Names),
			Precedence: cloneExact(src.Outcomes.Precedence),
			Terminal:   cloneExact(src.Outcomes.Terminal),
		},
		Remediations: result.RemediationTable{
			Kinds:         cloneExact(src.Remediations.Kinds),
			Fields:        cloneExact(src.Remediations.Fields),
			Values:        cloneExact(src.Remediations.Values),
			EvidenceKinds: cloneExact(src.Remediations.EvidenceKinds),
		},
		Resolutions: result.ResolutionTable{
			OutcomeIDs:        cloneExact(src.Resolutions.OutcomeIDs),
			ExplanationIDs:    cloneExact(src.Resolutions.ExplanationIDs),
			RemediationStarts: cloneExact(src.Resolutions.RemediationStarts),
			RemediationCounts: cloneExact(src.Resolutions.RemediationCounts),
		},
	}
	dst.Resolutions.RemediationIDs = dst.ClauseRemediationIDs
	if err := dst.ValidateResultTables(); err != nil {
		return Program{}, err
	}
	return dst, nil
}
