package compile

import (
	"math"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

const (
	indexFieldUnseen uint8 = iota
	indexFieldSingle
	indexFieldAmbiguous
)

func validateApplicabilityProgram(p *program.Program) error {
	if err := validateSlotProgram(p); err != nil {
		return err
	}
	if len(p.RequirementRoots) != len(p.RequirementIDs) || uint64(len(p.RequirementRoots)) > math.MaxUint32 ||
		len(p.ValueKinds) != len(p.ValueRefs) || uint64(len(p.FieldKinds)) > math.MaxUint32 ||
		uint64(len(p.ValueKinds)) > math.MaxUint32 {
		return ErrInvalidGeneratedProgram
	}
	for _, root := range p.RequirementRoots {
		if root == 0 || uint64(root) > uint64(len(p.Opcodes)) || !p.RootFlags[root-1].Has(program.RootApplicability) {
			return ErrInvalidGeneratedProgram
		}
	}
	return nil
}

func selectorFieldKind(p *program.Program, field schema.FieldID) (schema.ValueKind, error) {
	if field == 0 || uint64(field) > uint64(len(p.FieldKinds)) {
		return 0, ErrInvalidGeneratedProgram
	}
	kind := p.FieldKinds[field-1]
	if !kind.Valid() {
		return 0, ErrInvalidGeneratedProgram
	}
	return kind, nil
}

func selectorSymbol(p *program.Program, value schema.ValueID) (schema.SymbolID, error) {
	if value == 0 || uint64(value) > uint64(len(p.ValueKinds)) || uint64(value) > uint64(len(p.ValueRefs)) ||
		p.ValueKinds[value-1] != schema.ValueKindSymbol {
		return 0, ErrInvalidGeneratedProgram
	}
	symbol := schema.SymbolID(p.ValueRefs[value-1])
	if symbol == 0 || uint64(symbol) > uint64(p.ProgramSymbolCount) {
		return 0, ErrInvalidGeneratedProgram
	}
	return symbol, nil
}

func (l *Lowerer) appendSelectorValues(p *program.Program, row int, field schema.FieldID) error {
	state := l.indexFieldState[field-1]
	if state == indexFieldSingle {
		l.indexFieldState[field-1] = indexFieldAmbiguous
	}
	appendValues := state == indexFieldUnseen
	if appendValues {
		if uint64(len(l.indexConstraintValue)) > math.MaxUint32 {
			return ErrProgramTooLarge
		}
		l.indexFieldState[field-1] = indexFieldSingle
		l.indexFieldValueStart[field-1] = uint32(len(l.indexConstraintValue))
	}
	startCount := len(l.indexConstraintValue)
	switch p.Opcodes[row] {
	case program.OpcodeEqual:
		symbol, err := selectorSymbol(p, p.Values[row])
		if err != nil {
			return err
		}
		if appendValues {
			l.indexConstraintValue = append(l.indexConstraintValue, symbol)
		}
	case program.OpcodeIn:
		start := int(p.ListStarts[row])
		end := start + int(p.ListCounts[row])
		for _, value := range p.ListValues[start:end] {
			symbol, err := selectorSymbol(p, value)
			if err != nil {
				return err
			}
			if appendValues {
				l.indexConstraintValue = append(l.indexConstraintValue, symbol)
			}
		}
	}
	if appendValues {
		count := len(l.indexConstraintValue) - startCount
		if count == 0 {
			return ErrInvalidGeneratedProgram
		}
		if uint64(count) > math.MaxUint32 || uint64(len(l.indexConstraintValue)) > math.MaxUint32 {
			return ErrProgramTooLarge
		}
		l.indexFieldValueCount[field-1] = uint32(count)
	}
	return nil
}

// extractApplicabilityConstraints returns a borrowed view over Lowerer-owned
// reusable scratch. Only positive symbolic Equal/In leaves reached through All
// are safe necessary conditions; every unsupported shape remains wildcard.
func (l *Lowerer) extractApplicabilityConstraints(p *program.Program) (policyindex.Constraints, error) {
	if l == nil {
		return policyindex.Constraints{}, ErrInvalidGeneratedProgram
	}
	if err := validateApplicabilityProgram(p); err != nil {
		return policyindex.Constraints{}, err
	}
	l.indexConstraintRows = l.indexConstraintRows[:0]
	l.indexConstraintField = l.indexConstraintField[:0]
	l.indexConstraintStart = l.indexConstraintStart[:0]
	l.indexConstraintCount = l.indexConstraintCount[:0]
	l.indexConstraintValue = l.indexConstraintValue[:0]
	n := len(p.Opcodes)
	fieldCount := len(p.FieldKinds)
	l.indexVisited = resizeSlots(l.indexVisited, n)
	l.indexFieldState = resizeSlots(l.indexFieldState, fieldCount)
	l.indexFieldValueStart = resizeSlots(l.indexFieldValueStart, fieldCount)
	l.indexFieldValueCount = resizeSlots(l.indexFieldValueCount, fieldCount)
	stackHint := len(p.Operands) + 1
	if stackHint <= 0 {
		return policyindex.Constraints{}, ErrProgramTooLarge
	}
	if cap(l.indexStack) < stackHint {
		l.indexStack = make([]schema.InstructionID, 0, stackHint)
	}

	for requirementRow, root := range p.RequirementRoots {
		clear(l.indexVisited)
		clear(l.indexFieldState)
		clear(l.indexFieldValueStart)
		clear(l.indexFieldValueCount)
		l.indexStack = l.indexStack[:0]
		l.indexStack = append(l.indexStack, root)
		valueBase := len(l.indexConstraintValue)

		for len(l.indexStack) != 0 {
			last := len(l.indexStack) - 1
			id := l.indexStack[last]
			l.indexStack = l.indexStack[:last]
			row := int(id - 1)
			if l.indexVisited[row] != 0 {
				continue
			}
			l.indexVisited[row] = 1
			switch p.Opcodes[row] {
			case program.OpcodeAll:
				start := int(p.OperandStarts[row])
				end := start + int(p.OperandCounts[row])
				for operand := end; operand > start; {
					operand--
					l.indexStack = append(l.indexStack, p.Operands[operand])
				}
			case program.OpcodeEqual, program.OpcodeIn:
				field := p.Fields[row]
				kind, err := selectorFieldKind(p, field)
				if err != nil {
					return policyindex.Constraints{}, err
				}
				if kind != schema.ValueKindSymbol {
					continue
				}
				if err := l.appendSelectorValues(p, row, field); err != nil {
					return policyindex.Constraints{}, err
				}
			}
		}

		valueOut := valueBase
		for fieldRow, state := range l.indexFieldState {
			if state != indexFieldSingle {
				continue
			}
			start := int(l.indexFieldValueStart[fieldRow])
			count := int(l.indexFieldValueCount[fieldRow])
			if uint64(len(l.indexConstraintRows)) >= math.MaxUint32 {
				return policyindex.Constraints{}, ErrProgramTooLarge
			}
			copy(l.indexConstraintValue[valueOut:], l.indexConstraintValue[start:start+count])
			l.indexConstraintRows = append(l.indexConstraintRows, uint32(requirementRow))
			l.indexConstraintField = append(l.indexConstraintField, schema.FieldID(fieldRow+1))
			l.indexConstraintStart = append(l.indexConstraintStart, uint32(valueOut))
			l.indexConstraintCount = append(l.indexConstraintCount, uint32(count))
			valueOut += count
		}
		l.indexConstraintValue = l.indexConstraintValue[:valueOut]
	}
	return policyindex.Constraints{
		Rows:        l.indexConstraintRows,
		Fields:      l.indexConstraintField,
		ValueStarts: l.indexConstraintStart,
		ValueCounts: l.indexConstraintCount,
		Values:      l.indexConstraintValue,
	}, nil
}
