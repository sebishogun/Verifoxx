package compile

import (
	"math"

	"github.com/sebishogun/verifoxx/internal/program"
)

const knownRootFlags = program.RootApplicability | program.RootAssertion | program.RootEvidence

// validateSlotProgram checks the generated instruction graph before liveness
// scans slice its CSR edges. Slot planning requires final topological order, so
// every operand ID must be strictly less than its one-based consumer ID.
func validateSlotProgram(p *program.Program) error {
	if p == nil {
		return ErrInvalidGeneratedProgram
	}
	if uint64(len(p.Opcodes)) > math.MaxUint32 {
		return ErrProgramTooLarge
	}
	if err := validateScheduleColumns(p); err != nil {
		return err
	}
	for row, flags := range p.RootFlags {
		if flags&^knownRootFlags != 0 {
			return ErrInvalidGeneratedProgram
		}
		start := uint64(p.OperandStarts[row])
		end := start + uint64(p.OperandCounts[row])
		if end > uint64(len(p.Operands)) {
			return ErrInvalidGeneratedProgram
		}
		consumerID := uint64(row) + 1
		for _, operand := range p.Operands[int(start):int(end)] {
			if operand == 0 || uint64(operand) >= consumerID {
				return ErrInvalidGeneratedProgram
			}
		}
	}
	return nil
}

// computeLastUses returns zero-based final-consumer rows for every instruction.
// Semantic roots use the one-past-the-end sentinel and therefore remain live
// until evaluation has consumed all roots.
func (l *Lowerer) computeLastUses(p *program.Program) ([]uint32, error) {
	if err := validateSlotProgram(p); err != nil {
		return nil, err
	}
	n := len(p.Opcodes)
	l.slotLastUses = resizeSlots(l.slotLastUses, n)
	for row := range n {
		l.slotLastUses[row] = uint32(row)
	}
	for consumer := range n {
		start := int(p.OperandStarts[consumer])
		end := start + int(p.OperandCounts[consumer])
		for _, operand := range p.Operands[start:end] {
			l.slotLastUses[int(operand)-1] = uint32(consumer)
		}
	}
	for row, flags := range p.RootFlags {
		if flags != 0 {
			l.slotLastUses[row] = uint32(n)
		}
	}
	return l.slotLastUses, nil
}
