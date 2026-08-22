package eval

import (
	"math"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
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
