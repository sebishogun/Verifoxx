package compile

import (
	"errors"
	"math"
	"slices"

	policyindex "github.com/sebishogun/nornrune/internal/index"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

const (
	indexFieldUnseen uint8 = iota
	indexFieldSingle
	indexFieldAmbiguous

	// Reused full-column symbol masks beat direct comparison conservatively at
	// 96 uses across measured 64-4096 row dense and sparse batches.
	factIndexMinUses uint32 = 96
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

func compileIndexError(err error) error {
	if errors.Is(err, policyindex.ErrIndexTooLarge) {
		return ErrProgramTooLarge
	}
	return ErrInvalidGeneratedProgram
}

// lowerIndexes builds reusable private index output. Public Lower freezes exact
// copies only after every compiler stage has succeeded.
func (l *Lowerer) lowerIndexes(p *program.Program) error {
	constraints, err := l.extractApplicabilityConstraints(p)
	if err != nil {
		return err
	}
	if err := policyindex.BuildSchema(&p.FieldIndex, p.FieldKinds); err != nil {
		return compileIndexError(err)
	}
	if err := l.indexBuilder.Build(&p.ApplicabilityIndex, uint32(len(p.RequirementIDs)), constraints); err != nil {
		return compileIndexError(err)
	}
	return l.lowerFactIndexSpec(p)
}

// lowerFactIndexSpec counts exact value comparisons in the final schedule,
// then emits sorted, unique symbol values for fields above the measured reuse
// threshold. The Program and Lowerer retain all capacities between calls.
func (l *Lowerer) lowerFactIndexSpec(p *program.Program) error {
	fieldCount := len(p.FieldKinds)
	l.factUseCounts = resizeSlots(l.factUseCounts, fieldCount)
	for row, opcode := range p.Opcodes {
		var uses uint32
		switch opcode {
		case program.OpcodeEqual, program.OpcodeNotEqual:
			uses = 1
		case program.OpcodeIn:
			uses = uint32(p.ListCounts[row])
		default:
			continue
		}

		field := p.Fields[row]
		if field == 0 || uint64(field) > uint64(fieldCount) {
			return ErrInvalidGeneratedProgram
		}
		if p.FieldKinds[field-1] != schema.ValueKindSymbol {
			continue
		}
		if opcode == program.OpcodeIn {
			start := int(p.ListStarts[row])
			end := start + int(p.ListCounts[row])
			for _, value := range p.ListValues[start:end] {
				if _, err := selectorSymbol(p, value); err != nil {
					return err
				}
			}
		} else if _, err := selectorSymbol(p, p.Values[row]); err != nil {
			return err
		}
		current := l.factUseCounts[field-1]
		if uses > math.MaxUint32-current {
			return ErrProgramTooLarge
		}
		l.factUseCounts[field-1] = current + uses
	}

	selectedFields := 0
	totalValues := uint64(0)
	for _, uses := range l.factUseCounts {
		if uses < factIndexMinUses {
			continue
		}
		selectedFields++
		totalValues += uint64(uses)
	}
	if totalValues > math.MaxUint32 || totalValues > uint64(math.MaxInt) {
		return ErrProgramTooLarge
	}

	spec := &p.FactIndexSpec
	spec.FieldIDs = resizeSlots(spec.FieldIDs, selectedFields)
	spec.Columns = resizeSlots(spec.Columns, selectedFields)
	spec.ValueStarts = resizeSlots(spec.ValueStarts, selectedFields)
	spec.ValueCounts = resizeSlots(spec.ValueCounts, selectedFields)
	spec.UseCounts = resizeSlots(spec.UseCounts, selectedFields)
	spec.Values = resizeSlots(spec.Values, int(totalValues))
	l.factValueFill = resizeSlots(l.factValueFill, fieldCount)

	fieldRow := 0
	valueStart := uint32(0)
	for fieldOffset, uses := range l.factUseCounts {
		if uses < factIndexMinUses {
			continue
		}
		field := schema.FieldID(fieldOffset + 1)
		kind, column, ok := p.FieldIndex.Lookup(field)
		if !ok || kind != schema.ValueKindSymbol {
			return ErrInvalidGeneratedProgram
		}
		spec.FieldIDs[fieldRow] = field
		spec.Columns[fieldRow] = column
		spec.ValueStarts[fieldRow] = valueStart
		spec.ValueCounts[fieldRow] = uses
		spec.UseCounts[fieldRow] = uses
		l.factValueFill[fieldOffset] = valueStart
		valueStart += uses
		fieldRow++
	}

	for row, opcode := range p.Opcodes {
		if opcode != program.OpcodeEqual && opcode != program.OpcodeNotEqual && opcode != program.OpcodeIn {
			continue
		}
		fieldOffset := int(p.Fields[row]) - 1
		if l.factUseCounts[fieldOffset] < factIndexMinUses {
			continue
		}
		if opcode == program.OpcodeIn {
			start := int(p.ListStarts[row])
			end := start + int(p.ListCounts[row])
			for _, value := range p.ListValues[start:end] {
				symbol, _ := selectorSymbol(p, value)
				fill := l.factValueFill[fieldOffset]
				spec.Values[fill] = symbol
				l.factValueFill[fieldOffset] = fill + 1
			}
			continue
		}
		symbol, _ := selectorSymbol(p, p.Values[row])
		fill := l.factValueFill[fieldOffset]
		spec.Values[fill] = symbol
		l.factValueFill[fieldOffset] = fill + 1
	}

	write := 0
	for row := range spec.FieldIDs {
		start := int(spec.ValueStarts[row])
		end := start + int(spec.ValueCounts[row])
		values := spec.Values[start:end]
		slices.Sort(values)
		spec.ValueStarts[row] = uint32(write)
		var previous schema.SymbolID
		for _, value := range values {
			if value == previous {
				continue
			}
			spec.Values[write] = value
			write++
			previous = value
		}
		spec.ValueCounts[row] = uint32(write) - spec.ValueStarts[row]
	}
	spec.Values = spec.Values[:write]
	if !spec.Valid(p.FieldIndex, p.ProgramSymbolCount) {
		return ErrInvalidGeneratedProgram
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
		if uint64(len(l.indexCandidateValue)) > math.MaxUint32 {
			return ErrProgramTooLarge
		}
		l.indexFieldState[field-1] = indexFieldSingle
		l.indexFieldValueStart[field-1] = uint32(len(l.indexCandidateValue))
	}
	startCount := len(l.indexCandidateValue)
	switch p.Opcodes[row] {
	case program.OpcodeEqual:
		symbol, err := selectorSymbol(p, p.Values[row])
		if err != nil {
			return err
		}
		if appendValues {
			l.indexCandidateValue = append(l.indexCandidateValue, symbol)
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
				l.indexCandidateValue = append(l.indexCandidateValue, symbol)
			}
		}
	}
	if appendValues {
		count := len(l.indexCandidateValue) - startCount
		if count == 0 {
			return ErrInvalidGeneratedProgram
		}
		if uint64(count) > math.MaxUint32 || uint64(len(l.indexCandidateValue)) > math.MaxUint32 {
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
		l.indexCandidateValue = l.indexCandidateValue[:0]
		l.indexStack = append(l.indexStack, root)

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

		for fieldRow, state := range l.indexFieldState {
			if state != indexFieldSingle {
				continue
			}
			start := int(l.indexFieldValueStart[fieldRow])
			count := int(l.indexFieldValueCount[fieldRow])
			if uint64(len(l.indexConstraintRows)) >= math.MaxUint32 {
				return policyindex.Constraints{}, ErrProgramTooLarge
			}
			valueStart := len(l.indexConstraintValue)
			if uint64(valueStart)+uint64(count) > math.MaxUint32 {
				return policyindex.Constraints{}, ErrProgramTooLarge
			}
			l.indexConstraintValue = append(l.indexConstraintValue, l.indexCandidateValue[start:start+count]...)
			l.indexConstraintRows = append(l.indexConstraintRows, uint32(requirementRow))
			l.indexConstraintField = append(l.indexConstraintField, schema.FieldID(fieldRow+1))
			l.indexConstraintStart = append(l.indexConstraintStart, uint32(valueStart))
			l.indexConstraintCount = append(l.indexConstraintCount, uint32(count))
		}
	}
	return policyindex.Constraints{
		Rows:        l.indexConstraintRows,
		Fields:      l.indexConstraintField,
		ValueStarts: l.indexConstraintStart,
		ValueCounts: l.indexConstraintCount,
		Values:      l.indexConstraintValue,
	}, nil
}
