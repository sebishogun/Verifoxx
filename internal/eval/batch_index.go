package eval

import (
	"errors"
	"slices"

	policyindex "github.com/sebishogun/nornrune/internal/index"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/simdops"
	"github.com/sebishogun/nornrune/internal/truth"
)

const factIndexMinRows uint32 = 64

func factIndexQuerySymbol(p *program.Program, id schema.ValueID) (schema.SymbolID, bool) {
	if id == 0 {
		return 0, false
	}
	row := uint64(id - 1)
	if row >= uint64(len(p.ValueKinds)) || row >= uint64(len(p.ValueRefs)) ||
		p.ValueKinds[row] != schema.ValueKindSymbol {
		return 0, false
	}
	symbol := schema.SymbolID(p.ValueRefs[row])
	if symbol == 0 || uint32(symbol) > p.ProgramSymbolCount {
		return 0, false
	}
	_, ok := p.Symbol(symbol)
	return symbol, ok
}

// validFactIndexQueries prevents stale or hand-built metadata from treating a
// queried value as absent merely because the compiler-owned spec omitted it.
func validFactIndexQueries(p *program.Program) bool {
	spec := &p.FactIndexSpec
	if len(spec.FieldIDs) == 0 {
		return true
	}
	for _, symbol := range spec.Values {
		if _, ok := p.Symbol(symbol); !ok {
			return false
		}
	}
	for row, opcode := range p.Opcodes {
		fieldRow, selected := slices.BinarySearch(spec.FieldIDs, p.Fields[row])
		if !selected {
			continue
		}
		start := int(spec.ValueStarts[fieldRow])
		end := start + int(spec.ValueCounts[fieldRow])
		values := spec.Values[start:end]
		contains := func(id schema.ValueID) bool {
			symbol, ok := factIndexQuerySymbol(p, id)
			if !ok {
				return false
			}
			_, ok = slices.BinarySearch(values, symbol)
			return ok
		}
		switch opcode {
		case program.OpcodeEqual, program.OpcodeNotEqual:
			if !contains(p.Values[row]) {
				return false
			}
		case program.OpcodeIn:
			if p.Values[row] != 0 {
				return false
			}
			listStart := int(p.ListStarts[row])
			listEnd := listStart + int(p.ListCounts[row])
			for _, id := range p.ListValues[listStart:listEnd] {
				if !contains(id) {
					return false
				}
			}
		}
	}
	return true
}

func useFactIndex(mode executionMode, rows uint32) bool {
	switch mode {
	case executionScalar, executionSIMD:
		return false
	case executionIndex:
		return true
	default:
		return rows >= factIndexMinRows
	}
}

func (e *Executor) buildFactIndex(p *program.Program, batch Batch, mode executionMode) error {
	if !useFactIndex(mode, batch.Rows) || len(p.FactIndexSpec.FieldIDs) == 0 {
		e.factIndex.Reset()
		return nil
	}
	err := e.factBuilder.Build(&e.factIndex, &p.FactIndexSpec, policyindex.SymbolColumns{
		Values:             batch.SymbolValues,
		Rows:               batch.Rows,
		Count:              p.FieldIndex.Counts[schema.ValueKindSymbol],
		ProgramSymbolCount: p.ProgramSymbolCount,
		RowOffset:          batch.rowBase,
		RowStride:          batch.sourceRows(),
	})
	if errors.Is(err, policyindex.ErrIndexTooLarge) {
		return ErrBatchTooLarge
	}
	if err != nil {
		return ErrInvalidProgram
	}
	return nil
}

func (e *Executor) evalPredicateIndex(
	dst truth.Planes,
	reasons ReasonPlanes,
	batch Batch,
	p *program.Program,
	instruction schema.InstructionID,
	mode executionMode,
) bool {
	if !useFactIndex(mode, batch.Rows) {
		return false
	}
	row := int(instruction - 1)
	opcode := p.Opcodes[row]
	if opcode != program.OpcodeEqual && opcode != program.OpcodeNotEqual && opcode != program.OpcodeIn {
		return false
	}
	field := p.Fields[row]
	kind, column, ok := p.FieldIndex.Lookup(field)
	if !ok || !kind.Valid() {
		panic("eval: invalid predicate field")
	}
	if column >= p.FieldIndex.Counts[kind] {
		panic("eval: invalid predicate column")
	}
	if kind != schema.ValueKindSymbol {
		return false
	}
	if _, indexed := e.factIndex.Lookup(field, 0); !indexed {
		return false
	}

	var equalMask []uint64
	var listStart, listCount uint32
	if opcode == program.OpcodeIn {
		if p.Values[row] != 0 {
			panic("eval: invalid in predicate")
		}
		listStart = p.ListStarts[row]
		listCount = uint32(p.ListCounts[row])
		if uint64(listStart)+uint64(listCount) > uint64(len(p.ListValues)) {
			panic("eval: invalid in predicate")
		}
		for i := uint32(0); i < listCount; i++ {
			programPredicateValue(p, p.ListValues[listStart+i], kind)
		}
	} else {
		value := programPredicateValue(p, p.Values[row], kind)
		equalMask, _ = e.factIndex.Lookup(field, value.symbol)
	}

	words := resetLeafOutputs(dst, reasons, batch.Rows)
	if opcode == program.OpcodeIn {
		for i := uint32(0); i < listCount; i++ {
			value := programPredicateValue(p, p.ListValues[listStart+i], kind)
			mask, _ := e.factIndex.Lookup(field, value.symbol)
			if len(mask) == 0 {
				continue
			}
			if useSIMDWords(mode, words) {
				simdops.OrWords(dst.Positive, dst.Positive, mask)
			} else {
				for word := range mask {
					dst.Positive[word] |= mask[word]
				}
			}
		}
	}
	presence := batchWordColumn(batch, batch.PresenceMasks, uint32(field-1))
	missing := reasons.plane(truth.ReasonMissing, words)
	for word := range words {
		valid := leafWordMask(word, words, batch.Rows)
		present := presence[word] & valid
		matches := dst.Positive[word]
		if opcode != program.OpcodeIn {
			matches = 0
			if equalMask != nil {
				matches = equalMask[word]
			}
			if opcode == program.OpcodeNotEqual {
				matches = ^matches
			}
		}
		missing[word] = valid &^ present
		dst.Positive[word] = present & matches
		dst.Negative[word] = present &^ matches
	}
	return true
}
