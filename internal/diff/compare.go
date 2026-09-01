package diff

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	resultbatch "github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

type comparisonScratch struct {
	oldResults   resultbatch.Batch
	newResults   resultbatch.Batch
	oldExecutor  eval.Executor
	newExecutor  eval.Executor
	batches      candidateBatches
	plan         searchPlan
	materializer candidateMaterializer
}

// Compare compiles and compares two native policies over an explicit finite domain.
func (analyzer *Analyzer) Compare(
	ctx context.Context,
	dst *Result,
	oldSource, newSource []byte,
	fields FieldSchema,
	domain Domain,
	matrix RiskMatrix,
	prover Prover,
) error {
	if analyzer == nil || ctx == nil || dst == nil {
		return ErrInvalidPolicy
	}
	if err := matrix.Validate(); err != nil {
		return err
	}
	oldProgram, newProgram, err := analyzer.compilePair(oldSource, newSource, fields)
	if errors.Is(err, ErrUnsupportedOutcomes) {
		*dst = Result{Outcome: Inconclusive, Uncertainty: "unsupported outcome catalog"}
		return nil
	}
	if err != nil {
		return err
	}
	validationDomain := domain
	validationDomain.MaxCandidates = math.MaxUint64
	_, complete, err := validationDomain.Validate()
	if err != nil {
		return err
	}
	if semanticIdentity(oldProgram, newProgram) {
		*dst = Result{Outcome: Equivalent, Complete: true}
		return nil
	}
	plan, err := buildSearchPlan(&analyzer.comparison.plan, oldProgram, newProgram, domain)
	if errors.Is(err, ErrCandidateBudget) {
		*dst = Result{Outcome: Inconclusive, Uncertainty: "candidate budget exhausted"}
		return nil
	}
	if err != nil {
		if errors.Is(err, ErrInvalidDomain) {
			*dst = Result{Outcome: Inconclusive, Uncertainty: "domain does not cover every policy dependency"}
			return nil
		}
		return err
	}
	if prover != nil {
		proof, proofErr := invokeProver(ctx, prover, ownProofRequest(oldSource, newSource, fields, domain, matrix))
		if proofErr != nil || !proof.Claim.Valid() || proof.Claim == ProofClaimInconclusive {
			*dst = Result{Outcome: Inconclusive, Uncertainty: "proof provider was inconclusive"}
			return nil
		}
		if proof.Claim == ProofClaimChanged && !analyzer.replayProofWitness(ctx, oldProgram, newProgram, plan, domain, proof.Witness) {
			*dst = Result{Outcome: Inconclusive, Uncertainty: "proof witness failed concrete replay"}
			return nil
		}
	}
	next := Result{Outcome: Equivalent, Complete: complete}
	for start := uint64(0); start < plan.cardinality; {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows := uint64(domain.BatchRows)
		if remaining := plan.cardinality - start; rows > remaining {
			rows = remaining
		}
		if err := analyzer.comparison.materializer.materializeContext(
			ctx,
			&analyzer.comparison.batches, oldProgram, newProgram, plan, domain, start, uint32(rows),
		); err != nil {
			return err
		}
		if err := analyzer.comparison.oldExecutor.Execute(
			&analyzer.comparison.oldResults, oldProgram, analyzer.comparison.batches.old,
		); err != nil {
			return err
		}
		if err := analyzer.comparison.newExecutor.Execute(
			&analyzer.comparison.newResults, newProgram, analyzer.comparison.batches.new,
		); err != nil {
			return err
		}
		if err := compareResultBatch(
			&next, oldProgram, newProgram, &analyzer.comparison.oldResults, &analyzer.comparison.newResults,
			plan, domain, matrix, start,
		); err != nil {
			if errors.Is(err, ErrUnsupportedOutcomes) {
				*dst = Result{Outcome: Inconclusive, Uncertainty: "candidate produced no supported decision", Candidates: start}
				return nil
			}
			return err
		}
		start += rows
		next.Candidates = start
	}
	if next.Outcome == Equivalent && !complete {
		next.Outcome = Inconclusive
		next.Uncertainty = "domain is not closed"
	}
	*dst = next
	return nil
}

func (analyzer *Analyzer) replayProofWitness(
	ctx context.Context,
	oldProgram, newProgram *program.Program,
	plan *searchPlan,
	domain Domain,
	witness Candidate,
) bool {
	index, ok := candidateIndex(plan, domain, witness)
	if !ok {
		return false
	}
	if err := analyzer.comparison.materializer.materializeContext(
		ctx,
		&analyzer.comparison.batches, oldProgram, newProgram, plan, domain, index, 1,
	); err != nil {
		return false
	}
	if err := analyzer.comparison.oldExecutor.Execute(
		&analyzer.comparison.oldResults, oldProgram, analyzer.comparison.batches.old,
	); err != nil {
		return false
	}
	if err := analyzer.comparison.newExecutor.Execute(
		&analyzer.comparison.newResults, newProgram, analyzer.comparison.batches.new,
	); err != nil {
		return false
	}
	oldDecision, oldOK := outcomeDecision(analyzer.comparison.oldResults.OutcomeIDs[0])
	newDecision, newOK := outcomeDecision(analyzer.comparison.newResults.OutcomeIDs[0])
	different := !resultRowEqual(oldProgram, newProgram, &analyzer.comparison.oldResults, &analyzer.comparison.newResults, 0)
	return oldOK && newOK && different && oldDecision == witness.OldDecision && newDecision == witness.NewDecision
}

func candidateIndex(plan *searchPlan, domain Domain, witness Candidate) (uint64, bool) {
	if len(witness.Fields) != len(plan.fieldRows) {
		return 0, false
	}
	index := uint64(0)
	for dimension, fieldRow := range plan.fieldRows {
		field := domain.Fields[fieldRow]
		witnessRow := -1
		for row := range witness.Fields {
			if witness.Fields[row].Name == field.Name {
				if witnessRow >= 0 {
					return 0, false
				}
				witnessRow = row
			}
		}
		if witnessRow < 0 {
			return 0, false
		}
		option := -1
		for row := range field.Values {
			if domainValueEqual(field.Values[row], witness.Fields[witnessRow].Value) {
				option = row
				break
			}
		}
		if option < 0 {
			return 0, false
		}
		index += uint64(option) * plan.strides[dimension]
	}
	evidenceStride := uint64(1)
	for _, count := range plan.optionCounts {
		evidenceStride *= uint64(count)
	}
	if len(domain.EvidenceSets) == 0 {
		if len(witness.Evidence) != 0 {
			return 0, false
		}
	} else {
		scenario := -1
		for row := range domain.EvidenceSets {
			if evidenceEqual(domain.EvidenceSets[row].Records, witness.Evidence) {
				scenario = row
				break
			}
		}
		if scenario < 0 {
			return 0, false
		}
		index += uint64(scenario) * evidenceStride
	}
	return index, index < plan.cardinality
}

func domainValueEqual(left, right Value) bool {
	return left.String == right.String && left.Integer == right.Integer && left.State == right.State && left.Kind == right.Kind && left.Boolean == right.Boolean
}

func evidenceEqual(left, right []Evidence) bool {
	if len(left) != len(right) {
		return false
	}
	for row := range left {
		if left[row] != right[row] {
			return false
		}
	}
	return true
}

func classifyEvaluation(matrix RiskMatrix, oldDecision, newDecision Decision, different bool) (Outcome, bool, bool, error) {
	if !different {
		return Equivalent, false, false, nil
	}
	if oldDecision == newDecision {
		return Changed, true, false, nil
	}
	transition, ok := matrix.Lookup(oldDecision, newDecision)
	if !ok {
		return OutcomeInvalid, false, false, ErrInvalidRiskMatrix
	}
	return transition.Class, true, !transition.Allowed, nil
}

func compareResultBatch(
	dst *Result,
	oldProgram, newProgram *program.Program,
	oldResults, newResults *resultbatch.Batch,
	plan *searchPlan,
	domain Domain,
	matrix RiskMatrix,
	start uint64,
) error {
	if oldResults.Rows != newResults.Rows || len(oldResults.OutcomeIDs) != len(newResults.OutcomeIDs) {
		return ErrInvalidPolicy
	}
	for row := uint32(0); row < oldResults.Rows; row++ {
		oldDecision, ok := outcomeDecision(oldResults.OutcomeIDs[row])
		if !ok {
			return ErrUnsupportedOutcomes
		}
		newDecision, ok := outcomeDecision(newResults.OutcomeIDs[row])
		if !ok {
			return ErrUnsupportedOutcomes
		}
		different := !resultRowEqual(oldProgram, newProgram, oldResults, newResults, row)
		class, differing, forbidden, err := classifyEvaluation(matrix, oldDecision, newDecision, different)
		if err != nil {
			return err
		}
		index, _ := transitionIndex(oldDecision, newDecision)
		dst.Transitions[index]++
		if !differing {
			continue
		}
		dst.Outcome = mergeOutcome(dst.Outcome, class)
		dst.Forbidden = dst.Forbidden || forbidden
		if !dst.HasCounterexample {
			dst.Counterexample = ownCounterexample(
				oldProgram, newProgram, oldResults, newResults, plan, domain, row, start+uint64(row), oldDecision, newDecision,
			)
			dst.HasCounterexample = true
		}
	}
	return nil
}

func outcomeDecision(id schema.OutcomeID) (Decision, bool) {
	decision := Decision(id)
	return decision, decision.Valid()
}

func mergeOutcome(current, candidate Outcome) Outcome {
	if current == OutcomeInvalid || current == Equivalent {
		return candidate
	}
	if candidate == Equivalent || candidate == current {
		return current
	}
	return Changed
}

func resultRowEqual(oldProgram, newProgram *program.Program, oldResults, newResults *resultbatch.Batch, row uint32) bool {
	return oldResults.OutcomeIDs[row] == newResults.OutcomeIDs[row] &&
		outcomeRowsEqual(oldProgram, newProgram, oldResults.OutcomeIDs[row], newResults.OutcomeIDs[row]) &&
		rangeEqual(oldResults.RequirementOffsets, oldResults.RequirementIDs, newResults.RequirementOffsets, newResults.RequirementIDs, row) &&
		driverRangeEqual(oldProgram, newProgram, oldResults, newResults, row) &&
		rangeEqual(oldResults.EvidenceOffsets, oldResults.EvidenceIDs, newResults.EvidenceOffsets, newResults.EvidenceIDs, row) &&
		reasonRangeEqual(oldProgram, newProgram, oldResults, newResults, row) &&
		remediationRangeEqual(oldProgram, newProgram, oldResults, newResults, row)
}

func rangeEqual[T comparable](oldOffsets []uint32, oldValues []T, newOffsets []uint32, newValues []T, row uint32) bool {
	oldRange, oldOK := resultRange(oldOffsets, len(oldValues), row)
	newRange, newOK := resultRange(newOffsets, len(newValues), row)
	return oldOK && newOK && slices.Equal(oldValues[oldRange[0]:oldRange[1]], newValues[newRange[0]:newRange[1]])
}

func driverRangeEqual(oldProgram, newProgram *program.Program, oldResults, newResults *resultbatch.Batch, row uint32) bool {
	oldRange, oldOK := resultRange(oldResults.DriverOffsets, len(oldResults.DriverNodes), row)
	newRange, newOK := resultRange(newResults.DriverOffsets, len(newResults.DriverNodes), row)
	if !oldOK || !newOK {
		return false
	}
	o0, o1, n0, n1 := oldRange[0], oldRange[1], newRange[0], newRange[1]
	if !slices.Equal(oldResults.DriverRequirements[o0:o1], newResults.DriverRequirements[n0:n1]) ||
		!slices.Equal(oldResults.DriverClauses[o0:o1], newResults.DriverClauses[n0:n1]) ||
		!slices.Equal(oldResults.DriverNodes[o0:o1], newResults.DriverNodes[n0:n1]) ||
		!slices.Equal(oldResults.DriverReasons[o0:o1], newResults.DriverReasons[n0:n1]) ||
		!slices.Equal(oldResults.DriverExplanations[o0:o1], newResults.DriverExplanations[n0:n1]) {
		return false
	}
	for offset := 0; offset < o1-o0; offset++ {
		if !explanationRowsEqual(oldProgram, newProgram, oldResults.DriverExplanations[o0+offset], newResults.DriverExplanations[n0+offset]) {
			return false
		}
	}
	return assumptionsEqual(oldProgram, newProgram)
}

func reasonRangeEqual(oldProgram, newProgram *program.Program, oldResults, newResults *resultbatch.Batch, row uint32) bool {
	oldRange, oldOK := resultRange(oldResults.ReasonOffsets, len(oldResults.ReasonIDs), row)
	newRange, newOK := resultRange(newResults.ReasonOffsets, len(newResults.ReasonIDs), row)
	if !oldOK || !newOK {
		return false
	}
	o0, o1, n0, n1 := oldRange[0], oldRange[1], newRange[0], newRange[1]
	if !slices.Equal(oldResults.ReasonIDs[o0:o1], newResults.ReasonIDs[n0:n1]) ||
		!slices.Equal(oldResults.ReasonNodes[o0:o1], newResults.ReasonNodes[n0:n1]) ||
		!slices.Equal(oldResults.ReasonEvidenceIDs[o0:o1], newResults.ReasonEvidenceIDs[n0:n1]) {
		return false
	}
	for offset := 0; offset < o1-o0; offset++ {
		if !evidenceStateRowsEqual(oldProgram, newProgram, oldResults.ReasonEvidenceStates[o0+offset], newResults.ReasonEvidenceStates[n0+offset]) {
			return false
		}
		if !evidenceIssueRowsEqual(
			oldProgram, newProgram,
			oldResults.ReasonNodes[o0+offset], newResults.ReasonNodes[n0+offset],
			oldResults.ReasonIDs[o0+offset], newResults.ReasonIDs[n0+offset],
		) {
			return false
		}
	}
	return true
}

func evidenceIssueRowsEqual(
	oldProgram, newProgram *program.Program,
	oldNode, newNode schema.NodeID,
	oldReason, newReason schema.ReasonID,
) bool {
	oldTemplate, oldFound, oldValid := evidenceIssueTemplate(oldProgram, oldNode, oldReason)
	newTemplate, newFound, newValid := evidenceIssueTemplate(newProgram, newNode, newReason)
	if !oldValid || !newValid || oldFound != newFound {
		return false
	}
	return !oldFound || templateRowsEqual(oldProgram, newProgram, oldTemplate, newTemplate)
}

func evidenceIssueTemplate(compiled *program.Program, node schema.NodeID, reason schema.ReasonID) (schema.TemplateID, bool, bool) {
	if reason == 0 || uint32(reason) > resultbatch.EvidenceIssueTemplateCount {
		return 0, false, false
	}
	for row, candidate := range compiled.EvidenceIssueNodeIDs {
		if candidate != node {
			continue
		}
		index := uint64(row)*resultbatch.EvidenceIssueTemplateCount + uint64(reason-1)
		if index >= uint64(len(compiled.EvidenceIssueTemplateIDs)) {
			return 0, false, false
		}
		return compiled.EvidenceIssueTemplateIDs[index], true, true
	}
	return 0, false, true
}

func outcomeRowsEqual(oldProgram, newProgram *program.Program, oldID, newID schema.OutcomeID) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldOutcome, oldOK := oldProgram.Outcomes.Lookup(oldID)
	newOutcome, newOK := newProgram.Outcomes.Lookup(newID)
	return oldOK && newOK && oldOutcome.Precedence == newOutcome.Precedence && oldOutcome.Terminal == newOutcome.Terminal &&
		symbolPairEqual(oldProgram, newProgram, oldOutcome.Name, newOutcome.Name)
}

func remediationRangeEqual(oldProgram, newProgram *program.Program, oldResults, newResults *resultbatch.Batch, row uint32) bool {
	oldRange, oldOK := resultRange(oldResults.RemediationOffsets, len(oldResults.RemediationIDs), row)
	newRange, newOK := resultRange(newResults.RemediationOffsets, len(newResults.RemediationIDs), row)
	if !oldOK || !newOK || oldRange[1]-oldRange[0] != newRange[1]-newRange[0] {
		return false
	}
	for offset := 0; offset < oldRange[1]-oldRange[0]; offset++ {
		oldID := oldResults.RemediationIDs[oldRange[0]+offset]
		newID := newResults.RemediationIDs[newRange[0]+offset]
		oldRemediation, oldOK := oldProgram.Remediations.Lookup(oldID)
		newRemediation, newOK := newProgram.Remediations.Lookup(newID)
		if !oldOK || !newOK || oldRemediation.Kind != newRemediation.Kind ||
			!fieldRowsEqual(oldProgram, newProgram, oldRemediation.Field, newRemediation.Field) ||
			!semanticValuePairEqual(oldProgram, newProgram, oldRemediation.Value, newRemediation.Value) ||
			!catalogRowsEqual(oldProgram, newProgram, oldProgram.EvidenceKindNames, newProgram.EvidenceKindNames, uint32(oldRemediation.EvidenceKind), uint32(newRemediation.EvidenceKind)) {
			return false
		}
	}
	return true
}

func fieldRowsEqual(oldProgram, newProgram *program.Program, oldID, newID schema.FieldID) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldRow, newRow := int(oldID-1), int(newID-1)
	if oldRow < 0 || newRow < 0 || oldRow >= len(oldProgram.FieldNames) || newRow >= len(newProgram.FieldNames) {
		return false
	}
	return symbolPairEqual(oldProgram, newProgram, oldProgram.FieldNames[oldRow], newProgram.FieldNames[newRow])
}

func evidenceStateRowsEqual(oldProgram, newProgram *program.Program, oldID, newID schema.EvidenceStateID) bool {
	return catalogRowsEqual(oldProgram, newProgram, oldProgram.EvidenceStateNames, newProgram.EvidenceStateNames, uint32(oldID), uint32(newID))
}

func catalogRowsEqual(oldProgram, newProgram *program.Program, oldNames, newNames []schema.SymbolID, oldID, newID uint32) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldRow, newRow := uint64(oldID-1), uint64(newID-1)
	if oldRow >= uint64(len(oldNames)) || newRow >= uint64(len(newNames)) {
		return false
	}
	return symbolPairEqual(oldProgram, newProgram, oldNames[oldRow], newNames[newRow])
}

func explanationRowsEqual(oldProgram, newProgram *program.Program, oldID, newID schema.ExplanationID) bool {
	if oldID == 0 || newID == 0 {
		return oldID == newID
	}
	oldExplanation, oldOK := oldProgram.Explanations.Lookup(oldID)
	newExplanation, newOK := newProgram.Explanations.Lookup(newID)
	if !oldOK || !newOK || !templateRowsEqual(oldProgram, newProgram, oldExplanation.Rationale, newExplanation.Rationale) ||
		len(oldExplanation.Uncertainty) != len(newExplanation.Uncertainty) {
		return false
	}
	for row := range oldExplanation.Uncertainty {
		if !templateRowsEqual(oldProgram, newProgram, oldExplanation.Uncertainty[row], newExplanation.Uncertainty[row]) {
			return false
		}
	}
	return true
}

func assumptionsEqual(oldProgram, newProgram *program.Program) bool {
	oldAssumptions := oldProgram.Explanations.Assumptions()
	newAssumptions := newProgram.Explanations.Assumptions()
	if len(oldAssumptions) != len(newAssumptions) {
		return false
	}
	for row := range oldAssumptions {
		if !templateRowsEqual(oldProgram, newProgram, oldAssumptions[row], newAssumptions[row]) {
			return false
		}
	}
	return true
}

func templateRowsEqual(oldProgram, newProgram *program.Program, oldID, newID schema.TemplateID) bool {
	oldTemplate, oldOK := oldProgram.Templates.Lookup(oldID)
	newTemplate, newOK := newProgram.Templates.Lookup(newID)
	return oldOK && newOK && oldTemplate.MaxBytes == newTemplate.MaxBytes && bytes.Equal(oldTemplate.LiteralBytes, newTemplate.LiteralBytes) &&
		slices.Equal(oldTemplate.Ops, newTemplate.Ops) && slices.Equal(oldTemplate.Args, newTemplate.Args)
}

func resultRange(offsets []uint32, values int, row uint32) ([2]int, bool) {
	if uint64(row)+1 >= uint64(len(offsets)) {
		return [2]int{}, false
	}
	start, end := uint64(offsets[row]), uint64(offsets[row+1])
	if start > end || end > uint64(values) {
		return [2]int{}, false
	}
	return [2]int{int(start), int(end)}, true
}

func ownCounterexample(
	oldProgram, newProgram *program.Program,
	oldResults, newResults *resultbatch.Batch,
	plan *searchPlan,
	domain Domain,
	row uint32,
	index uint64,
	oldDecision, newDecision Decision,
) Counterexample {
	counterexample := Counterexample{Index: index}
	counterexample.Fields = make([]CandidateField, len(plan.fieldRows))
	for dimension, fieldRow := range plan.fieldRows {
		option := uint32(index / plan.strides[dimension] % uint64(plan.optionCounts[dimension]))
		value := domain.Fields[fieldRow].Values[option]
		value.String = strings.Clone(value.String)
		counterexample.Fields[dimension] = CandidateField{Name: strings.Clone(domain.Fields[fieldRow].Name), Value: value}
	}
	if len(domain.EvidenceSets) != 0 {
		evidenceStride := uint64(1)
		for _, count := range plan.optionCounts {
			evidenceStride *= uint64(count)
		}
		scenario := uint32(index/evidenceStride) % plan.evidenceCount
		counterexample.Evidence = cloneEvidence(domain.EvidenceSets[scenario].Records)
	}
	counterexample.Old = ownEvaluation(oldProgram, oldResults, row, index, oldDecision)
	counterexample.New = ownEvaluation(newProgram, newResults, row, index, newDecision)
	return counterexample
}

func cloneEvidence(source []Evidence) []Evidence {
	cloned := make([]Evidence, len(source))
	for row := range source {
		cloned[row] = Evidence{
			Kind: strings.Clone(source[row].Kind), State: strings.Clone(source[row].State),
			Subject: strings.Clone(source[row].Subject), Scope: strings.Clone(source[row].Scope), Timing: strings.Clone(source[row].Timing),
		}
	}
	return cloned
}

func ownEvaluation(compiled *program.Program, batch *resultbatch.Batch, row uint32, index uint64, decision Decision) Evaluation {
	evaluation := Evaluation{Index: index, OutcomeID: uint32(batch.OutcomeIDs[row]), Decision: decision}
	evaluation.RequirementIDs = copyResultRange(batch.RequirementOffsets, batch.RequirementIDs, row)
	evaluation.EvidenceIDs = copyResultRange(batch.EvidenceOffsets, batch.EvidenceIDs, row)
	evaluation.RemediationIDs = copyResultRange(batch.RemediationOffsets, batch.RemediationIDs, row)
	driver, _ := resultRange(batch.DriverOffsets, len(batch.DriverNodes), row)
	evaluation.DriverRequirements = copyIDs(batch.DriverRequirements[driver[0]:driver[1]])
	evaluation.DriverClauses = copyIDs(batch.DriverClauses[driver[0]:driver[1]])
	evaluation.DriverNodes = copyIDs(batch.DriverNodes[driver[0]:driver[1]])
	evaluation.DriverReasons = copyIDs(batch.DriverReasons[driver[0]:driver[1]])
	evaluation.DriverExplanations = copyIDs(batch.DriverExplanations[driver[0]:driver[1]])
	reasons, _ := resultRange(batch.ReasonOffsets, len(batch.ReasonIDs), row)
	evaluation.ReasonIDs = copyIDs(batch.ReasonIDs[reasons[0]:reasons[1]])
	evaluation.ReasonNodes = copyIDs(batch.ReasonNodes[reasons[0]:reasons[1]])
	evaluation.ReasonEvidenceIDs = copyIDs(batch.ReasonEvidenceIDs[reasons[0]:reasons[1]])
	evaluation.ReasonEvidenceStates = copyIDs(batch.ReasonEvidenceStates[reasons[0]:reasons[1]])
	if len(batch.DriverNodes[driver[0]:driver[1]]) != 0 {
		evaluation.SourceStart, evaluation.SourceEnd = nodeSourceSpan(compiled, batch.DriverNodes[driver[0]])
	}
	return evaluation
}

func copyResultRange[T ~uint32](offsets []uint32, values []T, row uint32) []uint32 {
	span, _ := resultRange(offsets, len(values), row)
	return copyIDs(values[span[0]:span[1]])
}

func copyIDs[T ~uint32](source []T) []uint32 {
	cloned := make([]uint32, len(source))
	for row := range source {
		cloned[row] = uint32(source[row])
	}
	return cloned
}

func nodeSourceSpan(compiled *program.Program, node schema.NodeID) (uint32, uint32) {
	for row, candidate := range compiled.InstructionNodes {
		if candidate == node {
			return compiled.InstructionSourceStarts[row], compiled.InstructionSourceEnds[row]
		}
	}
	return 0, 0
}
