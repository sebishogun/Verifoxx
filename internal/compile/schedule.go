package compile

import (
	"math"
	"math/bits"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

const scheduleOpcodeCount = int(program.OpcodeNot) + 1

func (l *Lowerer) setScheduleReady(opcode program.Opcode, oldID uint32, words int) error {
	if !opcode.Valid() || int(opcode) >= scheduleOpcodeCount {
		return ErrInvalidGeneratedProgram
	}
	word := oldID >> 6
	bit := uint64(1) << (oldID & 63)
	index := int(opcode)*words + int(word)
	if index < 0 || index >= len(l.scheduleReadyBits) || l.scheduleReadyBits[index]&bit != 0 {
		return ErrInvalidGeneratedProgram
	}
	l.scheduleReadyBits[index] |= bit
	l.scheduleReadyCount[opcode]++
	return nil
}

func (l *Lowerer) popScheduleReady(opcode program.Opcode, words int) (uint32, bool) {
	for word := l.scheduleReadyCursor[opcode]; word < uint32(words); word++ {
		index := int(opcode)*words + int(word)
		value := l.scheduleReadyBits[index]
		if value == 0 {
			l.scheduleReadyCursor[opcode] = word + 1
			continue
		}
		bit := uint32(bits.TrailingZeros64(value))
		l.scheduleReadyBits[index] &^= uint64(1) << bit
		l.scheduleReadyCount[opcode]--
		l.scheduleReadyCursor[opcode] = word
		return word*64 + bit, true
	}
	return 0, false
}

func validateScheduleColumns(p *program.Program) error {
	n := len(p.Opcodes)
	if len(p.Fields) != n || len(p.Values) != n || len(p.ListStarts) != n || len(p.ListCounts) != n ||
		len(p.OperandStarts) != n || len(p.OperandCounts) != n || len(p.EvidenceKinds) != n ||
		len(p.EvidenceStates) != n || len(p.RootFlags) != n || len(p.InstructionNodes) != n ||
		len(p.InstructionSourceStarts) != n || len(p.InstructionSourceEnds) != n {
		return ErrInvalidGeneratedProgram
	}
	for i, opcode := range p.Opcodes {
		if !opcode.Valid() || uint64(p.ListStarts[i])+uint64(p.ListCounts[i]) > uint64(len(p.ListValues)) ||
			uint64(p.OperandStarts[i])+uint64(p.OperandCounts[i]) > uint64(len(p.Operands)) {
			return ErrInvalidGeneratedProgram
		}
	}
	for _, id := range p.NodeInstructionIDs {
		if id == 0 || uint64(id) > uint64(n) {
			return ErrInvalidGeneratedProgram
		}
	}
	return nil
}

func (l *Lowerer) buildScheduleUsers(p *program.Program) (int, error) {
	n := len(p.Opcodes)
	l.scheduleIndegree = resizeSlots(l.scheduleIndegree, n)
	l.scheduleUserStarts = resizeSlots(l.scheduleUserStarts, n+1)
	edges := uint64(0)
	for consumer := 0; consumer < n; consumer++ {
		start := p.OperandStarts[consumer]
		count := p.OperandCounts[consumer]
		edges += uint64(count)
		if edges > uint64(math.MaxInt) || edges > math.MaxUint32 {
			return 0, ErrProgramTooLarge
		}
		l.scheduleIndegree[consumer] = uint32(count)
		for _, operand := range p.Operands[start : start+uint32(count)] {
			if operand == 0 || int(operand) > n {
				return 0, ErrInvalidGeneratedProgram
			}
			l.scheduleUserStarts[operand]++
		}
	}
	for i := 1; i < len(l.scheduleUserStarts); i++ {
		l.scheduleUserStarts[i] += l.scheduleUserStarts[i-1]
	}
	l.scheduleUsers = resizeSlots(l.scheduleUsers, int(edges))
	l.scheduleFill = resizeSlots(l.scheduleFill, n)
	copy(l.scheduleFill, l.scheduleUserStarts[:n])
	for consumer := 0; consumer < n; consumer++ {
		start := p.OperandStarts[consumer]
		count := p.OperandCounts[consumer]
		for _, operand := range p.Operands[start : start+uint32(count)] {
			dependency := int(operand) - 1
			position := l.scheduleFill[dependency]
			l.scheduleUsers[position] = uint32(consumer)
			l.scheduleFill[dependency]++
		}
	}
	return int(edges), nil
}

func (l *Lowerer) buildScheduleOrder(p *program.Program) error {
	n := len(p.Opcodes)
	words := (n + 63) / 64
	l.scheduleReadyBits = resizeSlots(l.scheduleReadyBits, scheduleOpcodeCount*words)
	l.scheduleOrder = resizeSlots(l.scheduleOrder, n)[:0]
	l.scheduleOldToNew = resizeSlots(l.scheduleOldToNew, n)
	l.scheduleReadyCount = [13]uint32{}
	l.scheduleReadyCursor = [13]uint32{}
	p.OpcodeRunOpcodes = p.OpcodeRunOpcodes[:0]
	p.OpcodeRunStarts = p.OpcodeRunStarts[:0]
	p.OpcodeRunCounts = p.OpcodeRunCounts[:0]
	for oldID, indegree := range l.scheduleIndegree {
		if indegree == 0 {
			if err := l.setScheduleReady(p.Opcodes[oldID], uint32(oldID), words); err != nil {
				return err
			}
		}
	}
	for len(l.scheduleOrder) < n {
		opcode := program.OpcodeInvalid
		for candidate := program.OpcodeEqual; candidate <= program.OpcodeNot; candidate++ {
			if l.scheduleReadyCount[candidate] != 0 {
				opcode = candidate
				break
			}
		}
		if opcode == program.OpcodeInvalid {
			return ErrInvalidDocument
		}
		runStart := len(l.scheduleOrder)
		l.scheduleReadyCursor[opcode] = 0
		for l.scheduleReadyCount[opcode] != 0 {
			oldID, ok := l.popScheduleReady(opcode, words)
			if !ok || int(oldID) >= n {
				return ErrInvalidGeneratedProgram
			}
			l.scheduleOrder = append(l.scheduleOrder, oldID)
			l.scheduleOldToNew[oldID] = schema.InstructionID(len(l.scheduleOrder))
			start, end := l.scheduleUserStarts[oldID], l.scheduleUserStarts[oldID+1]
			for _, user := range l.scheduleUsers[start:end] {
				if l.scheduleIndegree[user] == 0 {
					return ErrInvalidGeneratedProgram
				}
				l.scheduleIndegree[user]--
				if l.scheduleIndegree[user] == 0 {
					if err := l.setScheduleReady(p.Opcodes[user], user, words); err != nil {
						return err
					}
				}
			}
		}
		p.OpcodeRunOpcodes = append(p.OpcodeRunOpcodes, opcode)
		p.OpcodeRunStarts = append(p.OpcodeRunStarts, uint32(runStart))
		p.OpcodeRunCounts = append(p.OpcodeRunCounts, uint32(len(l.scheduleOrder)-runStart))
	}
	return nil
}

func (l *Lowerer) permuteScheduledInstructions(p *program.Program) error {
	n := len(p.Opcodes)
	l.candidateOpcodes = resizeSlots(l.candidateOpcodes, n)
	l.candidateFields = resizeSlots(l.candidateFields, n)
	l.candidateValues = resizeSlots(l.candidateValues, n)
	l.candidateListStarts = resizeSlots(l.candidateListStarts, n)
	l.candidateListCounts = resizeSlots(l.candidateListCounts, n)
	l.candidateOperandStarts = resizeSlots(l.candidateOperandStarts, n)
	l.candidateOperandCounts = resizeSlots(l.candidateOperandCounts, n)
	l.candidateEvidenceKinds = resizeSlots(l.candidateEvidenceKinds, n)
	l.candidateEvidenceState = resizeSlots(l.candidateEvidenceState, n)
	l.candidateRootFlags = resizeSlots(l.candidateRootFlags, n)
	l.candidateNodes = resizeSlots(l.candidateNodes, n)
	l.candidateSourceStarts = resizeSlots(l.candidateSourceStarts, n)
	l.candidateSourceEnds = resizeSlots(l.candidateSourceEnds, n)
	l.candidateListValues = resizeSlots(l.candidateListValues, len(p.ListValues))
	l.candidateOperands = resizeSlots(l.candidateOperands, len(p.Operands))
	listPosition, operandPosition := uint32(0), uint32(0)
	for newRow, oldRow32 := range l.scheduleOrder {
		oldRow := int(oldRow32)
		l.candidateOpcodes[newRow] = p.Opcodes[oldRow]
		l.candidateFields[newRow] = p.Fields[oldRow]
		l.candidateValues[newRow] = p.Values[oldRow]
		l.candidateEvidenceKinds[newRow] = p.EvidenceKinds[oldRow]
		l.candidateEvidenceState[newRow] = p.EvidenceStates[oldRow]
		l.candidateRootFlags[newRow] = p.RootFlags[oldRow]
		l.candidateNodes[newRow] = p.InstructionNodes[oldRow]
		l.candidateSourceStarts[newRow] = p.InstructionSourceStarts[oldRow]
		l.candidateSourceEnds[newRow] = p.InstructionSourceEnds[oldRow]
		listStart, listCount := p.ListStarts[oldRow], p.ListCounts[oldRow]
		l.candidateListCounts[newRow] = listCount
		if listCount != 0 {
			l.candidateListStarts[newRow] = listPosition
			copy(l.candidateListValues[listPosition:], p.ListValues[listStart:listStart+uint32(listCount)])
			listPosition += uint32(listCount)
		}
		operandStart, operandCount := p.OperandStarts[oldRow], p.OperandCounts[oldRow]
		l.candidateOperandCounts[newRow] = operandCount
		if operandCount != 0 {
			l.candidateOperandStarts[newRow] = operandPosition
		}
		for _, oldOperand := range p.Operands[operandStart : operandStart+uint32(operandCount)] {
			if oldOperand == 0 || uint64(oldOperand) > uint64(len(l.scheduleOldToNew)) {
				return ErrInvalidGeneratedProgram
			}
			newOperand := l.scheduleOldToNew[oldOperand-1]
			if newOperand == 0 || newOperand >= schema.InstructionID(newRow+1) {
				return ErrInvalidGeneratedProgram
			}
			l.candidateOperands[operandPosition] = newOperand
			operandPosition++
		}
	}
	copy(p.Opcodes, l.candidateOpcodes)
	copy(p.Fields, l.candidateFields)
	copy(p.Values, l.candidateValues)
	copy(p.ListStarts, l.candidateListStarts)
	copy(p.ListCounts, l.candidateListCounts)
	copy(p.OperandStarts, l.candidateOperandStarts)
	copy(p.OperandCounts, l.candidateOperandCounts)
	copy(p.EvidenceKinds, l.candidateEvidenceKinds)
	copy(p.EvidenceStates, l.candidateEvidenceState)
	copy(p.RootFlags, l.candidateRootFlags)
	copy(p.InstructionNodes, l.candidateNodes)
	copy(p.InstructionSourceStarts, l.candidateSourceStarts)
	copy(p.InstructionSourceEnds, l.candidateSourceEnds)
	copy(p.ListValues, l.candidateListValues)
	copy(p.Operands, l.candidateOperands)
	for i, old := range p.NodeInstructionIDs {
		if old == 0 || uint64(old) > uint64(len(l.scheduleOldToNew)) {
			return ErrInvalidGeneratedProgram
		}
		p.NodeInstructionIDs[i] = l.scheduleOldToNew[old-1]
	}
	return nil
}

func (l *Lowerer) scheduleInstructions(p *program.Program) error {
	if p == nil || uint64(len(p.Opcodes)) >= uint64(math.MaxUint32) {
		return ErrInvalidGeneratedProgram
	}
	if err := validateScheduleColumns(p); err != nil {
		return err
	}
	if len(p.Opcodes) == 0 {
		p.OpcodeRunOpcodes = p.OpcodeRunOpcodes[:0]
		p.OpcodeRunStarts = p.OpcodeRunStarts[:0]
		p.OpcodeRunCounts = p.OpcodeRunCounts[:0]
		return nil
	}
	if _, err := l.buildScheduleUsers(p); err != nil {
		return err
	}
	if err := l.buildScheduleOrder(p); err != nil {
		return err
	}
	return l.permuteScheduledInstructions(p)
}
