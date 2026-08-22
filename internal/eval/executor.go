package eval

import (
	"math"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

const executorRootFlags = program.RootApplicability | program.RootAssertion | program.RootEvidence

// Executor owns mutable scratch for one serial evaluator worker. It is not safe
// for concurrent use; Programs and input batches remain borrowed and immutable.
type Executor struct {
	query           policyindex.Query
	program         *program.Program
	truthWords      []uint64
	reasonWords     []uint64
	candidateWords  []uint64
	selectorValues  []schema.SymbolID
	selectorPresent []uint8
	states          EvidenceStateIndex
}

func (e *Executor) prepare(p *program.Program, batch Batch) error {
	if e == nil || !validExecutionSchedule(p) || uint64(len(batch.RequestIDs)) != uint64(batch.Rows) {
		return ErrInvalidProgram
	}
	truthLen, ok := executorScratchLen(uint64(p.TruthSlotCount), 2, uint64(truth.WordCount(batch.Rows)))
	if !ok {
		return ErrBatchTooLarge
	}
	reasonLen, ok := executorScratchLen(uint64(p.ReasonSlotCount), truth.ReasonCount, uint64(truth.WordCount(batch.Rows)))
	if !ok {
		return ErrBatchTooLarge
	}
	if err := e.states.Bind(p); err != nil {
		return ErrInvalidProgram
	}
	for _, opcode := range p.Opcodes {
		if opcode == program.OpcodeEvidence {
			requireEvidenceBatch(batch, p, &e.states)
			break
		}
	}
	e.truthWords = resizeExecutorScratch(e.truthWords, truthLen)
	e.reasonWords = resizeExecutorScratch(e.reasonWords, reasonLen)
	e.program = p
	return nil
}

func executorScratchLen(a, b, c uint64) (int, bool) {
	if a != 0 && b > math.MaxUint64/a {
		return 0, false
	}
	n := a * b
	if n != 0 && c > math.MaxUint64/n {
		return 0, false
	}
	n *= c
	if n > uint64(math.MaxInt)/8 {
		return 0, false
	}
	return int(n), true
}

func resizeExecutorScratch[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	return dst[:n]
}

func validExecutionSchedule(p *program.Program) bool {
	if p == nil || len(p.Opcodes) == 0 || uint64(len(p.Opcodes)) > math.MaxUint32 {
		return false
	}
	n := len(p.Opcodes)
	if len(p.Fields) != n || len(p.Values) != n || len(p.ListStarts) != n || len(p.ListCounts) != n ||
		len(p.OperandStarts) != n || len(p.OperandCounts) != n || len(p.EvidenceKinds) != n ||
		len(p.EvidenceStates) != n || len(p.RootFlags) != n || len(p.TruthSlots) != n ||
		len(p.ReasonSlots) != n || len(p.InstructionNodes) != n ||
		len(p.InstructionSourceStarts) != n || len(p.InstructionSourceEnds) != n ||
		p.TruthSlotCount == 0 || p.ReasonSlotCount == 0 {
		return false
	}
	for row, opcode := range p.Opcodes {
		if !opcode.Valid() || p.RootFlags[row]&^executorRootFlags != 0 ||
			p.TruthSlots[row] == 0 || uint32(p.TruthSlots[row]) > p.TruthSlotCount ||
			p.ReasonSlots[row] == 0 || uint32(p.ReasonSlots[row]) > p.ReasonSlotCount ||
			p.InstructionNodes[row] == 0 || p.InstructionSourceStarts[row] > p.InstructionSourceEnds[row] {
			return false
		}
		start := uint64(p.OperandStarts[row])
		count := uint64(p.OperandCounts[row])
		if start+count < start || start+count > uint64(len(p.Operands)) {
			return false
		}
		switch opcode {
		case program.OpcodeAll, program.OpcodeAny:
			if count < 2 {
				return false
			}
		case program.OpcodeNot:
			if count != 1 {
				return false
			}
		default:
			if count != 0 {
				return false
			}
		}
		consumer := uint64(row) + 1
		for _, operand := range p.Operands[int(start):int(start+count)] {
			if operand == 0 || uint64(operand) >= consumer {
				return false
			}
		}
	}
	return true
}

func (e *Executor) truthSlot(slot schema.SlotID, rows uint32) truth.Planes {
	words := truth.WordCount(rows)
	if e == nil || slot == 0 || e.program == nil || uint32(slot) > e.program.TruthSlotCount {
		panic("eval: invalid truth slot")
	}
	start := int(uint64(slot-1) * 2 * uint64(words))
	middle := start + words
	end := middle + words
	if end > len(e.truthWords) {
		panic("eval: invalid truth scratch")
	}
	return truth.Planes{
		Positive: e.truthWords[start:middle:middle],
		Negative: e.truthWords[middle:end:end],
	}
}

func (e *Executor) reasonSlot(slot schema.SlotID, rows uint32) ReasonPlanes {
	words := truth.WordCount(rows)
	if e == nil || slot == 0 || e.program == nil || uint32(slot) > e.program.ReasonSlotCount {
		panic("eval: invalid reason slot")
	}
	start := int(uint64(slot-1) * truth.ReasonCount * uint64(words))
	end := start + truth.ReasonCount*words
	if end > len(e.reasonWords) {
		panic("eval: invalid reason scratch")
	}
	return ReasonPlanes{Words: e.reasonWords[start:end:end]}
}

func (e *Executor) executeSchedule(p *program.Program, batch Batch) {
	for row, opcode := range p.Opcodes {
		instruction := schema.InstructionID(row + 1)
		dstTruth := e.truthSlot(p.TruthSlots[row], batch.Rows)
		dstReasons := e.reasonSlot(p.ReasonSlots[row], batch.Rows)
		switch opcode {
		case program.OpcodeEqual, program.OpcodeNotEqual, program.OpcodeIn, program.OpcodeExists,
			program.OpcodeLess, program.OpcodeLessEqual, program.OpcodeGreater, program.OpcodeGreaterEqual:
			evalPredicate(dstTruth, dstReasons, batch, p, instruction)
		case program.OpcodeEvidence:
			evalEvidenceValidated(dstTruth, dstReasons, batch, p, &e.states, EvidencePredicate{
				Kind: p.EvidenceKinds[row], State: p.EvidenceStates[row],
			})
		case program.OpcodeAll, program.OpcodeAny:
			e.reduceTruthGroup(dstTruth, p, row, opcode, batch.Rows)
			e.reduceReasonGroup(dstReasons, p, row, batch.Rows)
		case program.OpcodeNot:
			operand := p.Operands[p.OperandStarts[row]]
			truth.Not(dstTruth, e.truthSlot(p.TruthSlots[operand-1], batch.Rows), batch.Rows)
			srcReasons := e.reasonSlot(p.ReasonSlots[operand-1], batch.Rows)
			if p.ReasonSlots[row] != p.ReasonSlots[operand-1] {
				copy(dstReasons.Words, srcReasons.Words)
			}
		default:
			panic("eval: invalid execution opcode")
		}
	}
}

func (e *Executor) reduceTruthGroup(dst truth.Planes, p *program.Program, row int, opcode program.Opcode, rows uint32) {
	start := int(p.OperandStarts[row])
	end := start + int(p.OperandCounts[row])
	operands := p.Operands[start:end]
	dstSlot := p.TruthSlots[row]
	driver := -1
	for i, operand := range operands {
		if p.TruthSlots[operand-1] == dstSlot {
			driver = i
			break
		}
	}
	if driver < 0 {
		driver = 0
		src := e.truthSlot(p.TruthSlots[operands[0]-1], rows)
		copy(dst.Positive, src.Positive)
		copy(dst.Negative, src.Negative)
	}
	for i, operand := range operands {
		if i == driver {
			continue
		}
		src := e.truthSlot(p.TruthSlots[operand-1], rows)
		if opcode == program.OpcodeAll {
			truth.And(dst, dst, src, rows)
		} else {
			truth.Or(dst, dst, src, rows)
		}
	}
}

func (e *Executor) reduceReasonGroup(dst ReasonPlanes, p *program.Program, row int, rows uint32) {
	start := int(p.OperandStarts[row])
	end := start + int(p.OperandCounts[row])
	operands := p.Operands[start:end]
	dstSlot := p.ReasonSlots[row]
	driver := -1
	for i, operand := range operands {
		if p.ReasonSlots[operand-1] == dstSlot {
			driver = i
			break
		}
	}
	if driver < 0 {
		driver = 0
		copy(dst.Words, e.reasonSlot(p.ReasonSlots[operands[0]-1], rows).Words)
	}
	for i, operand := range operands {
		if i == driver {
			continue
		}
		src := e.reasonSlot(p.ReasonSlots[operand-1], rows)
		for word := range dst.Words {
			dst.Words[word] |= src.Words[word]
		}
	}
}

type outcomeCandidate struct {
	remediations []schema.RemediationID
	outcome      schema.OutcomeID
	requirement  schema.RequirementID
	clause       schema.ClauseID
	node         schema.NodeID
	driverReason schema.ReasonID
	reasons      truth.ReasonMask
}

// Execute evaluates p over batch and replaces dst with one deterministic
// policy-owned outcome and compact numeric provenance per request row.
func (e *Executor) Execute(dst *result.Batch, p *program.Program, batch Batch) error {
	if e == nil || dst == nil || !validExecutionSemantics(p) {
		return ErrInvalidProgram
	}
	if err := e.query.Bind(&p.ApplicabilityIndex); err != nil {
		return ErrInvalidProgram
	}
	if err := e.prepare(p, batch); err != nil {
		return err
	}
	maxRequirements, ok := executorResultLen(uint64(batch.Rows), uint64(len(p.RequirementIDs)), 4)
	if !ok {
		return ErrBatchTooLarge
	}
	maxDrivers, ok := executorResultLen(uint64(batch.Rows), 1, 4)
	if !ok {
		return ErrBatchTooLarge
	}
	maxReasons, ok := executorResultLen(uint64(batch.Rows), truth.ReasonCount, 4)
	if !ok {
		return ErrBatchTooLarge
	}
	maxRemediations, ok := executorResultLen(uint64(batch.Rows), uint64(maxExecutionRemediations(p)), 4)
	if !ok {
		return ErrBatchTooLarge
	}
	if !validExecutionSelectors(p, batch) {
		return ErrInvalidProgram
	}

	e.candidateWords = resizeExecutorScratch(e.candidateWords, int(p.ApplicabilityIndex.WordCount))
	e.selectorValues = resizeExecutorScratch(e.selectorValues, len(p.ApplicabilityIndex.FieldIDs))
	e.selectorPresent = resizeExecutorScratch(e.selectorPresent, len(p.ApplicabilityIndex.FieldIDs))
	if err := dst.Reset(batch.Rows); err != nil {
		return ErrBatchTooLarge
	}
	dst.RequirementIDs = reserveResultEdges(dst.RequirementIDs, maxRequirements)
	dst.DriverRequirements = reserveResultEdges(dst.DriverRequirements, maxDrivers)
	dst.DriverClauses = reserveResultEdges(dst.DriverClauses, maxDrivers)
	dst.DriverNodes = reserveResultEdges(dst.DriverNodes, maxDrivers)
	dst.DriverReasons = reserveResultEdges(dst.DriverReasons, maxDrivers)
	dst.ReasonIDs = reserveResultEdges(dst.ReasonIDs, maxReasons)
	dst.RemediationIDs = reserveResultEdges(dst.RemediationIDs, maxRemediations)

	e.executeSchedule(p, batch)
	resolver := p.ResultResolver()
	for row := uint32(0); row < batch.Rows; row++ {
		e.selectRequirementCandidates(p, batch, row)
		var best outcomeCandidate
		for requirementRow, requirementID := range p.RequirementIDs {
			if e.candidateWords[requirementRow>>6]&(uint64(1)<<(uint(requirementRow)&63)) == 0 {
				continue
			}
			applicabilityRoot := p.RequirementRoots[requirementRow]
			positive, negative := e.instructionTruth(p, applicabilityRoot, row, batch.Rows)
			if !positive && negative {
				continue
			}
			dst.RequirementIDs = append(dst.RequirementIDs, requirementID)
			clauseStart := int(p.RequirementClauseStarts[requirementRow])
			clauseEnd := clauseStart + int(p.RequirementClauseCounts[requirementRow])
			for _, clauseID := range p.RequirementClauseIDs[clauseStart:clauseEnd] {
				var candidate outcomeCandidate
				if positive && !negative {
					candidate = e.resolveActiveClause(p, &resolver, requirementID, clauseID, row, batch.Rows)
				} else {
					candidate = e.resolveApplicability(p, &resolver, requirementID, clauseID, applicabilityRoot, row, batch.Rows)
				}
				preferExecutionCandidate(&best, candidate, &p.Outcomes)
			}
		}
		dst.RequirementOffsets[row+1] = uint32(len(dst.RequirementIDs))
		if best.outcome != 0 {
			dst.OutcomeIDs[row] = best.outcome
			dst.DriverRequirements = append(dst.DriverRequirements, best.requirement)
			dst.DriverClauses = append(dst.DriverClauses, best.clause)
			dst.DriverNodes = append(dst.DriverNodes, best.node)
			dst.DriverReasons = append(dst.DriverReasons, best.driverReason)
			for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
				if best.reasons.Has(reason) {
					dst.ReasonIDs = append(dst.ReasonIDs, reason)
				}
			}
			dst.RemediationIDs = append(dst.RemediationIDs, best.remediations...)
		}
		dst.DriverOffsets[row+1] = uint32(len(dst.DriverNodes))
		dst.EvidenceOffsets[row+1] = uint32(len(dst.EvidenceIDs))
		dst.ReasonOffsets[row+1] = uint32(len(dst.ReasonIDs))
		dst.RemediationOffsets[row+1] = uint32(len(dst.RemediationIDs))
	}
	return nil
}

func executorResultLen(rows, perRow, elementBytes uint64) (int, bool) {
	if rows != 0 && perRow > math.MaxUint64/rows {
		return 0, false
	}
	n := rows * perRow
	if n > math.MaxUint32 || n > uint64(math.MaxInt)/elementBytes {
		return 0, false
	}
	return int(n), true
}

func reserveResultEdges[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, 0, n)
	}
	return dst[:0]
}

func maxExecutionRemediations(p *program.Program) uint16 {
	var maximum uint16
	for _, count := range p.ClauseRemediationCounts {
		maximum = max(maximum, count)
	}
	for _, count := range p.Resolutions.RemediationCounts {
		maximum = max(maximum, count)
	}
	return maximum
}

func validExecutionSemantics(p *program.Program) bool {
	if !validExecutionSchedule(p) {
		return false
	}
	requirements := len(p.RequirementIDs)
	clauses := len(p.ClauseAssertionRoots)
	if requirements == 0 || clauses == 0 || uint64(requirements) > math.MaxUint32 ||
		uint64(clauses) > math.MaxUint32 || p.ApplicabilityIndex.RequirementCount != uint32(requirements) ||
		len(p.RequirementRoots) != requirements || len(p.RequirementClauseStarts) != requirements ||
		len(p.RequirementClauseCounts) != requirements || len(p.ClauseEvidenceStarts) != clauses ||
		len(p.ClauseEvidenceCounts) != clauses || len(p.ClauseOnSatisfied) != clauses ||
		len(p.ClauseOnFalse) != clauses || len(p.ClauseRemediationStarts) != clauses ||
		len(p.ClauseRemediationCounts) != clauses {
		return false
	}
	for row, requirementID := range p.RequirementIDs {
		root := p.RequirementRoots[row]
		if requirementID == 0 || !validExecutionRoot(p, root, program.RootApplicability) {
			return false
		}
		start := uint64(p.RequirementClauseStarts[row])
		count := uint64(p.RequirementClauseCounts[row])
		if count == 0 || start+count < start || start+count > uint64(len(p.RequirementClauseIDs)) {
			return false
		}
		for _, clauseID := range p.RequirementClauseIDs[int(start):int(start+count)] {
			if clauseID == 0 || uint64(clauseID) > uint64(clauses) {
				return false
			}
		}
	}
	for row, assertion := range p.ClauseAssertionRoots {
		if !validExecutionRoot(p, assertion, program.RootAssertion) {
			return false
		}
		evidenceStart := uint64(p.ClauseEvidenceStarts[row])
		evidenceCount := uint64(p.ClauseEvidenceCounts[row])
		if evidenceStart+evidenceCount < evidenceStart || evidenceStart+evidenceCount > uint64(len(p.ClauseEvidenceIDs)) {
			return false
		}
		for _, evidence := range p.ClauseEvidenceIDs[int(evidenceStart):int(evidenceStart+evidenceCount)] {
			if !validExecutionRoot(p, evidence, program.RootEvidence) {
				return false
			}
		}
		if _, ok := p.Outcomes.Lookup(p.ClauseOnSatisfied[row]); !ok {
			return false
		}
		if _, ok := p.Outcomes.Lookup(p.ClauseOnFalse[row]); !ok {
			return false
		}
		remediationStart := uint64(p.ClauseRemediationStarts[row])
		remediationCount := uint64(p.ClauseRemediationCounts[row])
		if remediationStart+remediationCount < remediationStart || remediationStart+remediationCount > uint64(len(p.ClauseRemediationIDs)) {
			return false
		}
	}
	return true
}

func validExecutionRoot(p *program.Program, root schema.InstructionID, flag program.RootFlags) bool {
	return root != 0 && uint64(root) <= uint64(len(p.Opcodes)) && p.RootFlags[root-1].Has(flag)
}

func validExecutionSelectors(p *program.Program, batch Batch) bool {
	words := uint64(truth.WordCount(batch.Rows))
	if uint64(len(batch.PresenceMasks)) != uint64(len(p.FieldIndex.Kinds))*words ||
		uint64(len(batch.SymbolValues)) != uint64(p.FieldIndex.Counts[schema.ValueKindSymbol])*uint64(batch.Rows) {
		return false
	}
	for _, field := range p.ApplicabilityIndex.FieldIDs {
		kind, column, ok := p.FieldIndex.Lookup(field)
		if !ok || kind != schema.ValueKindSymbol || column >= p.FieldIndex.Counts[schema.ValueKindSymbol] {
			return false
		}
	}
	return true
}

func (e *Executor) selectRequirementCandidates(p *program.Program, batch Batch, row uint32) {
	words := uint64(truth.WordCount(batch.Rows))
	rows := uint64(batch.Rows)
	for selectorRow, field := range p.ApplicabilityIndex.FieldIDs {
		_, column, _ := p.FieldIndex.Lookup(field)
		presenceWord := uint64(field-1)*words + uint64(row>>6)
		present := batch.PresenceMasks[presenceWord]&(uint64(1)<<(row&63)) != 0
		if present {
			e.selectorPresent[selectorRow] = 1
			e.selectorValues[selectorRow] = batch.SymbolValues[uint64(column)*rows+uint64(row)]
		} else {
			e.selectorPresent[selectorRow] = 0
			e.selectorValues[selectorRow] = 0
		}
	}
	if err := e.query.Candidates(e.candidateWords, e.selectorValues, e.selectorPresent); err != nil {
		panic("eval: invalid bound applicability query")
	}
}

func (e *Executor) instructionTruth(p *program.Program, instruction schema.InstructionID, row, rows uint32) (positive, negative bool) {
	planes := e.truthSlot(p.TruthSlots[instruction-1], rows)
	word := row >> 6
	bit := uint64(1) << (row & 63)
	return planes.Positive[word]&bit != 0, planes.Negative[word]&bit != 0
}

func (e *Executor) instructionReasons(p *program.Program, instruction schema.InstructionID, row, rows uint32) truth.ReasonMask {
	planes := e.reasonSlot(p.ReasonSlots[instruction-1], rows)
	word := row >> 6
	bit := uint64(1) << (row & 63)
	var mask truth.ReasonMask
	for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
		if planes.plane(reason, truth.WordCount(rows))[word]&bit != 0 {
			mask = mask.With(reason)
		}
	}
	return mask
}

func (e *Executor) resolveApplicability(
	p *program.Program,
	resolver *result.Resolver,
	requirementID schema.RequirementID,
	clauseID schema.ClauseID,
	root schema.InstructionID,
	row, rows uint32,
) outcomeCandidate {
	reasons := e.instructionReasons(p, root, row, rows)
	resolution, ok := resolver.Resolve(result.RuleSetID(clauseID), reasons)
	if !ok {
		panic("eval: unresolved applicability has no reason")
	}
	return outcomeCandidate{
		remediations: resolution.Remediations,
		outcome:      resolution.Outcome,
		requirement:  requirementID,
		clause:       clauseID,
		node:         p.InstructionNodes[root-1],
		driverReason: resolution.Reason,
		reasons:      reasons,
	}
}

func (e *Executor) resolveActiveClause(
	p *program.Program,
	resolver *result.Resolver,
	requirementID schema.RequirementID,
	clauseID schema.ClauseID,
	row, rows uint32,
) outcomeCandidate {
	clauseRow := int(clauseID - 1)
	assertion := p.ClauseAssertionRoots[clauseRow]
	positive, negative := e.instructionTruth(p, assertion, row, rows)
	reasons := e.instructionReasons(p, assertion, row, rows)
	evidenceStart := int(p.ClauseEvidenceStarts[clauseRow])
	evidenceEnd := evidenceStart + int(p.ClauseEvidenceCounts[clauseRow])
	for _, evidence := range p.ClauseEvidenceIDs[evidenceStart:evidenceEnd] {
		evidencePositive, evidenceNegative := e.instructionTruth(p, evidence, row, rows)
		positive = positive && evidencePositive
		negative = negative || evidenceNegative
		reasons |= e.instructionReasons(p, evidence, row, rows)
	}

	candidate := outcomeCandidate{requirement: requirementID, clause: clauseID}
	switch {
	case positive && !negative:
		candidate.outcome = p.ClauseOnSatisfied[clauseRow]
		candidate.node = p.InstructionNodes[assertion-1]
		candidate.remediations = executionClauseRemediations(p, clauseRow, candidate.outcome)
	case !positive && negative:
		candidate.outcome = p.ClauseOnFalse[clauseRow]
		candidate.node = e.firstNegativeNode(p, clauseRow, row, rows)
		candidate.remediations = executionClauseRemediations(p, clauseRow, candidate.outcome)
	default:
		resolution, ok := resolver.Resolve(result.RuleSetID(clauseID), reasons)
		if !ok {
			panic("eval: unresolved clause has no reason")
		}
		candidate.outcome = resolution.Outcome
		candidate.driverReason = resolution.Reason
		candidate.node = e.firstReasonNode(p, clauseRow, row, rows, resolution.Reason)
		candidate.remediations = resolution.Remediations
		candidate.reasons = reasons
	}
	return candidate
}

func executionClauseRemediations(p *program.Program, clauseRow int, outcomeID schema.OutcomeID) []schema.RemediationID {
	outcome, ok := p.Outcomes.Lookup(outcomeID)
	if !ok {
		panic("eval: invalid clause outcome")
	}
	if outcome.Terminal {
		return nil
	}
	start := int(p.ClauseRemediationStarts[clauseRow])
	end := start + int(p.ClauseRemediationCounts[clauseRow])
	return p.ClauseRemediationIDs[start:end:end]
}

func (e *Executor) firstNegativeNode(p *program.Program, clauseRow int, row, rows uint32) schema.NodeID {
	assertion := p.ClauseAssertionRoots[clauseRow]
	if _, negative := e.instructionTruth(p, assertion, row, rows); negative {
		return p.InstructionNodes[assertion-1]
	}
	start := int(p.ClauseEvidenceStarts[clauseRow])
	end := start + int(p.ClauseEvidenceCounts[clauseRow])
	for _, evidence := range p.ClauseEvidenceIDs[start:end] {
		if _, negative := e.instructionTruth(p, evidence, row, rows); negative {
			return p.InstructionNodes[evidence-1]
		}
	}
	panic("eval: false clause has no negative driver")
}

func (e *Executor) firstReasonNode(p *program.Program, clauseRow int, row, rows uint32, reason schema.ReasonID) schema.NodeID {
	assertion := p.ClauseAssertionRoots[clauseRow]
	if e.instructionReasons(p, assertion, row, rows).Has(reason) {
		return p.InstructionNodes[assertion-1]
	}
	start := int(p.ClauseEvidenceStarts[clauseRow])
	end := start + int(p.ClauseEvidenceCounts[clauseRow])
	for _, evidence := range p.ClauseEvidenceIDs[start:end] {
		if e.instructionReasons(p, evidence, row, rows).Has(reason) {
			return p.InstructionNodes[evidence-1]
		}
	}
	panic("eval: unresolved clause has no reason driver")
}

func preferExecutionCandidate(best *outcomeCandidate, candidate outcomeCandidate, outcomes *result.OutcomeTable) {
	winner, ok := outcomes.Prefer(best.outcome, candidate.outcome)
	if !ok {
		panic("eval: invalid outcome candidate")
	}
	if best.outcome == 0 || winner != best.outcome {
		*best = candidate
	}
}
