package compile

import (
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func resetSemanticColumns(dst *program.Program) {
	dst.ClearResultResolver()
	resetExplanationColumns(dst)
	dst.OutcomeSourceStarts = dst.OutcomeSourceStarts[:0]
	dst.OutcomeSourceEnds = dst.OutcomeSourceEnds[:0]
	dst.RemediationSourceStarts = dst.RemediationSourceStarts[:0]
	dst.RemediationSourceEnds = dst.RemediationSourceEnds[:0]
	dst.RequirementIDs = dst.RequirementIDs[:0]
	dst.RequirementRoots = dst.RequirementRoots[:0]
	dst.RequirementSourceNodeIDs = dst.RequirementSourceNodeIDs[:0]
	dst.RequirementClauseStarts = dst.RequirementClauseStarts[:0]
	dst.RequirementClauseCounts = dst.RequirementClauseCounts[:0]
	dst.RequirementClauseIDs = dst.RequirementClauseIDs[:0]
	dst.RequirementSourceStarts = dst.RequirementSourceStarts[:0]
	dst.RequirementSourceEnds = dst.RequirementSourceEnds[:0]
	dst.ClauseAssertionRoots = dst.ClauseAssertionRoots[:0]
	dst.ClauseAssertionSourceNodeIDs = dst.ClauseAssertionSourceNodeIDs[:0]
	dst.ClauseEvidenceStarts = dst.ClauseEvidenceStarts[:0]
	dst.ClauseEvidenceCounts = dst.ClauseEvidenceCounts[:0]
	dst.ClauseEvidenceIDs = dst.ClauseEvidenceIDs[:0]
	dst.ClauseEvidenceSourceNodeIDs = dst.ClauseEvidenceSourceNodeIDs[:0]
	dst.ClauseOnSatisfied = dst.ClauseOnSatisfied[:0]
	dst.ClauseOnFalse = dst.ClauseOnFalse[:0]
	dst.ClauseExplanationIDs = dst.ClauseExplanationIDs[:0]
	dst.ClauseRemediationStarts = dst.ClauseRemediationStarts[:0]
	dst.ClauseRemediationCounts = dst.ClauseRemediationCounts[:0]
	dst.ClauseRemediationIDs = dst.ClauseRemediationIDs[:0]
	dst.ClauseSourceStarts = dst.ClauseSourceStarts[:0]
	dst.ClauseSourceEnds = dst.ClauseSourceEnds[:0]
	dst.Outcomes.Names = dst.Outcomes.Names[:0]
	dst.Outcomes.Precedence = dst.Outcomes.Precedence[:0]
	dst.Outcomes.Terminal = dst.Outcomes.Terminal[:0]
	dst.Remediations.Kinds = dst.Remediations.Kinds[:0]
	dst.Remediations.Fields = dst.Remediations.Fields[:0]
	dst.Remediations.Values = dst.Remediations.Values[:0]
	dst.Remediations.EvidenceKinds = dst.Remediations.EvidenceKinds[:0]
	dst.Resolutions.OutcomeIDs = dst.Resolutions.OutcomeIDs[:0]
	dst.Resolutions.ExplanationIDs = dst.Resolutions.ExplanationIDs[:0]
	dst.Resolutions.RemediationStarts = dst.Resolutions.RemediationStarts[:0]
	dst.Resolutions.RemediationCounts = dst.Resolutions.RemediationCounts[:0]
	dst.Resolutions.RemediationIDs = nil
}

func rootInstruction(dst *program.Program, node schema.NodeID, sourceNodeCount int) (schema.InstructionID, error) {
	if node == 0 {
		return 0, ErrInvalidDocument
	}
	index := uint64(node - 1)
	if index >= uint64(sourceNodeCount) {
		return 0, ErrInvalidDocument
	}
	if index >= uint64(len(dst.NodeInstructionStarts)) || index >= uint64(len(dst.NodeInstructionCounts)) {
		return 0, ErrInvalidGeneratedProgram
	}
	start := uint64(dst.NodeInstructionStarts[index])
	count := dst.NodeInstructionCounts[index]
	if count != 1 || start >= uint64(len(dst.NodeInstructionIDs)) {
		return 0, ErrInvalidGeneratedProgram
	}
	id := dst.NodeInstructionIDs[start]
	if id == 0 || uint64(id) > uint64(dst.InstructionCount()) {
		return 0, ErrInvalidGeneratedProgram
	}
	return id, nil
}

func (l *Lowerer) lowerOutcomes(dst *program.Program, doc *ast.Document) error {
	n := len(doc.OutcomeNames)
	if len(doc.OutcomePrecedence) != n || len(doc.OutcomeTerminal) != n ||
		len(doc.OutcomeSourceStarts) != n || len(doc.OutcomeSourceEnds) != n {
		return ErrInvalidDocument
	}
	dst.Outcomes.Names = resizeSlots(dst.Outcomes.Names, n)
	dst.Outcomes.Precedence = resizeSlots(dst.Outcomes.Precedence, n)
	dst.Outcomes.Terminal = resizeSlots(dst.Outcomes.Terminal, n)
	dst.OutcomeSourceStarts = resizeSlots(dst.OutcomeSourceStarts, n)
	dst.OutcomeSourceEnds = resizeSlots(dst.OutcomeSourceEnds, n)
	for i := 0; i < n; i++ {
		name, err := l.symbolForValue(dst, doc, doc.OutcomeNames[i])
		if err != nil {
			return err
		}
		dst.Outcomes.Names[i] = name
		dst.Outcomes.Precedence[i] = doc.OutcomePrecedence[i]
		dst.Outcomes.Terminal[i] = doc.OutcomeTerminal[i]
		dst.OutcomeSourceStarts[i] = doc.OutcomeSourceStarts[i]
		dst.OutcomeSourceEnds[i] = doc.OutcomeSourceEnds[i]
	}
	return nil
}

func runtimeRemediationKind(kind ast.RemediationKind) (result.RemediationKind, bool) {
	switch kind {
	case ast.RemediationKindSetField:
		return result.RemediationSetField, true
	case ast.RemediationKindAddEvidence:
		return result.RemediationAddEvidence, true
	default:
		return result.RemediationInvalid, false
	}
}

func (l *Lowerer) lowerRemediations(dst *program.Program, doc *ast.Document) error {
	n := len(doc.RemediationKinds)
	if len(doc.RemediationFields) != n || len(doc.RemediationValues) != n ||
		len(doc.RemediationEvidenceKinds) != n || len(doc.RemediationSourceStarts) != n ||
		len(doc.RemediationSourceEnds) != n {
		return ErrInvalidDocument
	}
	dst.Remediations.Kinds = resizeSlots(dst.Remediations.Kinds, n)
	dst.Remediations.Fields = resizeSlots(dst.Remediations.Fields, n)
	dst.Remediations.Values = resizeSlots(dst.Remediations.Values, n)
	dst.Remediations.EvidenceKinds = resizeSlots(dst.Remediations.EvidenceKinds, n)
	dst.RemediationSourceStarts = resizeSlots(dst.RemediationSourceStarts, n)
	dst.RemediationSourceEnds = resizeSlots(dst.RemediationSourceEnds, n)
	for i := 0; i < n; i++ {
		kind, ok := runtimeRemediationKind(doc.RemediationKinds[i])
		if !ok {
			return ErrInvalidDocument
		}
		dst.Remediations.Kinds[i] = kind
		dst.RemediationSourceStarts[i] = doc.RemediationSourceStarts[i]
		dst.RemediationSourceEnds[i] = doc.RemediationSourceEnds[i]
		switch kind {
		case result.RemediationSetField:
			value, err := l.canonicalValue(doc.RemediationValues[i])
			if err != nil || doc.RemediationFields[i] == 0 || doc.RemediationEvidenceKinds[i] != 0 {
				return ErrInvalidDocument
			}
			dst.Remediations.Fields[i] = doc.RemediationFields[i]
			dst.Remediations.Values[i] = value
		case result.RemediationAddEvidence:
			if doc.RemediationFields[i] != 0 || doc.RemediationValues[i] != 0 || doc.RemediationEvidenceKinds[i] == 0 {
				return ErrInvalidDocument
			}
			dst.Remediations.EvidenceKinds[i] = doc.RemediationEvidenceKinds[i]
		}
	}
	return nil
}

func (l *Lowerer) lowerRequirements(dst *program.Program, doc *ast.Document) error {
	n := len(doc.RequirementIDs)
	if len(doc.RequirementApplicabilityRoots) != n || len(doc.RequirementClauseStarts) != n ||
		len(doc.RequirementClauseCounts) != n || len(doc.RequirementSourceStarts) != n ||
		len(doc.RequirementSourceEnds) != n {
		return ErrInvalidDocument
	}
	dst.RequirementIDs = resizeSlots(dst.RequirementIDs, n)
	dst.RequirementRoots = resizeSlots(dst.RequirementRoots, n)
	dst.RequirementSourceNodeIDs = resizeSlots(dst.RequirementSourceNodeIDs, n)
	dst.RequirementClauseStarts = resizeSlots(dst.RequirementClauseStarts, n)
	dst.RequirementClauseCounts = resizeSlots(dst.RequirementClauseCounts, n)
	dst.RequirementSourceStarts = resizeSlots(dst.RequirementSourceStarts, n)
	dst.RequirementSourceEnds = resizeSlots(dst.RequirementSourceEnds, n)
	dst.RequirementClauseIDs = resizeSlots(dst.RequirementClauseIDs, len(doc.RequirementClauseIDs))
	clauseMax := uint64(len(doc.ClauseAssertionRoots))
	for i, id := range doc.RequirementClauseIDs {
		if uint64(id) == 0 || uint64(id) > clauseMax {
			return ErrInvalidDocument
		}
		dst.RequirementClauseIDs[i] = id
	}
	for i := 0; i < n; i++ {
		start, count := doc.RequirementClauseStarts[i], doc.RequirementClauseCounts[i]
		if uint64(start)+uint64(count) > uint64(len(doc.RequirementClauseIDs)) {
			return ErrInvalidDocument
		}
		root, err := rootInstruction(dst, doc.RequirementApplicabilityRoots[i], len(doc.NodeKinds))
		if err != nil {
			return err
		}
		dst.RequirementIDs[i] = doc.RequirementIDs[i]
		dst.RequirementRoots[i] = root
		dst.RequirementSourceNodeIDs[i] = doc.RequirementApplicabilityRoots[i]
		dst.RequirementClauseStarts[i] = start
		dst.RequirementClauseCounts[i] = count
		dst.RequirementSourceStarts[i] = doc.RequirementSourceStarts[i]
		dst.RequirementSourceEnds[i] = doc.RequirementSourceEnds[i]
	}
	return nil
}

func (l *Lowerer) lowerClauses(dst *program.Program, doc *ast.Document) error {
	n := len(doc.ClauseAssertionRoots)
	if len(doc.ClauseEvidenceStarts) != n || len(doc.ClauseEvidenceCounts) != n ||
		len(doc.ClauseRemediationStarts) != n || len(doc.ClauseRemediationCounts) != n ||
		len(doc.ClauseOnSatisfied) != n || len(doc.ClauseOnFalse) != n || len(doc.ClauseOnMissing) != n ||
		len(doc.ClauseOnStale) != n || len(doc.ClauseOnUnclear) != n ||
		len(doc.ClauseOnUnverifiable) != n || len(doc.ClauseOnConflict) != n ||
		uint64(len(doc.ClauseExplanationIDs)) != uint64(n)*uint64(ast.ResolutionBranchCount) ||
		len(doc.ClauseSourceStarts) != n || len(doc.ClauseSourceEnds) != n {
		return ErrInvalidDocument
	}
	dst.ClauseAssertionRoots = resizeSlots(dst.ClauseAssertionRoots, n)
	dst.ClauseAssertionSourceNodeIDs = resizeSlots(dst.ClauseAssertionSourceNodeIDs, n)
	dst.ClauseEvidenceStarts = resizeSlots(dst.ClauseEvidenceStarts, n)
	dst.ClauseEvidenceCounts = resizeSlots(dst.ClauseEvidenceCounts, n)
	dst.ClauseOnSatisfied = resizeSlots(dst.ClauseOnSatisfied, n)
	dst.ClauseOnFalse = resizeSlots(dst.ClauseOnFalse, n)
	dst.ClauseRemediationStarts = resizeSlots(dst.ClauseRemediationStarts, n)
	dst.ClauseRemediationCounts = resizeSlots(dst.ClauseRemediationCounts, n)
	dst.ClauseSourceStarts = resizeSlots(dst.ClauseSourceStarts, n)
	dst.ClauseSourceEnds = resizeSlots(dst.ClauseSourceEnds, n)
	dst.ClauseEvidenceIDs = resizeSlots(dst.ClauseEvidenceIDs, len(doc.ClauseEvidenceNodeIDs))
	dst.ClauseEvidenceSourceNodeIDs = resizeSlots(dst.ClauseEvidenceSourceNodeIDs, len(doc.ClauseEvidenceNodeIDs))
	dst.ClauseExplanationIDs = resizeSlots(dst.ClauseExplanationIDs, len(doc.ClauseExplanationIDs))
	copy(dst.ClauseExplanationIDs, doc.ClauseExplanationIDs)
	dst.ClauseRemediationIDs = resizeSlots(dst.ClauseRemediationIDs, len(doc.ClauseRemediationIDs))
	copy(dst.ClauseRemediationIDs, doc.ClauseRemediationIDs)
	for i := 0; i < n; i++ {
		assertion, err := rootInstruction(dst, doc.ClauseAssertionRoots[i], len(doc.NodeKinds))
		if err != nil {
			return err
		}
		evidenceStart, evidenceCount := doc.ClauseEvidenceStarts[i], doc.ClauseEvidenceCounts[i]
		if uint64(evidenceStart)+uint64(evidenceCount) > uint64(len(doc.ClauseEvidenceNodeIDs)) {
			return ErrInvalidDocument
		}
		for j := uint32(0); j < uint32(evidenceCount); j++ {
			sourceNode := doc.ClauseEvidenceNodeIDs[evidenceStart+j]
			root, err := rootInstruction(dst, sourceNode, len(doc.NodeKinds))
			if err != nil {
				return err
			}
			dst.ClauseEvidenceIDs[evidenceStart+j] = root
			dst.ClauseEvidenceSourceNodeIDs[evidenceStart+j] = sourceNode
		}
		remediationStart, remediationCount := doc.ClauseRemediationStarts[i], doc.ClauseRemediationCounts[i]
		if uint64(remediationStart)+uint64(remediationCount) > uint64(len(doc.ClauseRemediationIDs)) {
			return ErrInvalidDocument
		}
		dst.ClauseAssertionRoots[i] = assertion
		dst.ClauseAssertionSourceNodeIDs[i] = doc.ClauseAssertionRoots[i]
		dst.ClauseEvidenceStarts[i] = evidenceStart
		dst.ClauseEvidenceCounts[i] = evidenceCount
		dst.ClauseOnSatisfied[i] = doc.ClauseOnSatisfied[i]
		dst.ClauseOnFalse[i] = doc.ClauseOnFalse[i]
		dst.ClauseRemediationStarts[i] = remediationStart
		dst.ClauseRemediationCounts[i] = remediationCount
		dst.ClauseSourceStarts[i] = doc.ClauseSourceStarts[i]
		dst.ClauseSourceEnds[i] = doc.ClauseSourceEnds[i]
	}
	return nil
}

func (l *Lowerer) lowerResolutionRows(dst *program.Program, doc *ast.Document) error {
	clauses := len(doc.ClauseAssertionRoots)
	if clauses == 0 || clauses > math.MaxInt/truth.ReasonCount {
		return ErrInvalidDocument
	}
	rows := clauses * truth.ReasonCount
	dst.Resolutions.OutcomeIDs = resizeSlots(dst.Resolutions.OutcomeIDs, rows)
	dst.Resolutions.ExplanationIDs = resizeSlots(dst.Resolutions.ExplanationIDs, rows)
	dst.Resolutions.RemediationStarts = resizeSlots(dst.Resolutions.RemediationStarts, rows)
	dst.Resolutions.RemediationCounts = resizeSlots(dst.Resolutions.RemediationCounts, rows)
	for i := 0; i < clauses; i++ {
		_, resolution, ok := doc.Clause(schema.ClauseID(i + 1))
		if !ok {
			return ErrInvalidDocument
		}
		var outcomes [truth.ReasonCount]schema.OutcomeID
		var explanations [truth.ReasonCount]schema.ExplanationID
		outcomes[truth.ReasonMissing-1] = resolution.OnMissing
		outcomes[truth.ReasonStale-1] = resolution.OnStale
		outcomes[truth.ReasonUnclear-1] = resolution.OnUnclear
		outcomes[truth.ReasonUnverifiable-1] = resolution.OnUnverifiable
		outcomes[truth.ReasonWrongScope-1] = resolution.OnUnverifiable
		outcomes[truth.ReasonWrongSubject-1] = resolution.OnUnverifiable
		outcomes[truth.ReasonWrongTiming-1] = resolution.OnUnverifiable
		outcomes[truth.ReasonInvalid-1] = resolution.OnUnverifiable
		outcomes[truth.ReasonConflict-1] = resolution.OnConflict
		explanations[truth.ReasonMissing-1] = resolution.OnMissingExplanation
		explanations[truth.ReasonStale-1] = resolution.OnStaleExplanation
		explanations[truth.ReasonUnclear-1] = resolution.OnUnclearExplanation
		explanations[truth.ReasonUnverifiable-1] = resolution.OnUnverifiableExplanation
		explanations[truth.ReasonWrongScope-1] = resolution.OnUnverifiableExplanation
		explanations[truth.ReasonWrongSubject-1] = resolution.OnUnverifiableExplanation
		explanations[truth.ReasonWrongTiming-1] = resolution.OnUnverifiableExplanation
		explanations[truth.ReasonInvalid-1] = resolution.OnUnverifiableExplanation
		explanations[truth.ReasonConflict-1] = resolution.OnConflictExplanation
		remediationStart := dst.ClauseRemediationStarts[i]
		remediationCount := dst.ClauseRemediationCounts[i]
		base := i * truth.ReasonCount
		for reason, outcomeID := range outcomes {
			outcome, ok := dst.Outcomes.Lookup(outcomeID)
			if !ok {
				return ErrInvalidGeneratedProgram
			}
			row := base + reason
			dst.Resolutions.OutcomeIDs[row] = outcomeID
			dst.Resolutions.ExplanationIDs[row] = explanations[reason]
			if !outcome.Terminal {
				dst.Resolutions.RemediationStarts[row] = remediationStart
				dst.Resolutions.RemediationCounts[row] = remediationCount
			}
		}
	}
	dst.Resolutions.RemediationIDs = dst.ClauseRemediationIDs
	if err := dst.ValidateResultTables(); err != nil {
		return ErrInvalidGeneratedProgram
	}
	return nil
}

func (l *Lowerer) lowerSemantics(dst *program.Program, doc *ast.Document) error {
	if dst == nil || doc == nil {
		return ErrInvalidDocument
	}
	resetSemanticColumns(dst)
	if err := l.lowerOutcomes(dst, doc); err != nil {
		return err
	}
	if err := l.lowerRemediations(dst, doc); err != nil {
		return err
	}
	if err := l.lowerExplanationTables(dst, doc); err != nil {
		return err
	}
	if err := l.lowerRequirements(dst, doc); err != nil {
		return err
	}
	if err := l.lowerClauses(dst, doc); err != nil {
		return err
	}
	return l.lowerResolutionRows(dst, doc)
}
