package eval

import (
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type predicateValue struct {
	integer   int64
	timestamp int64
	symbol    schema.SymbolID
	kind      schema.ValueKind
	boolean   bool
}

func programPredicateValue(p *program.Program, id schema.ValueID, want schema.ValueKind) predicateValue {
	if id == 0 {
		panic("eval: invalid predicate value")
	}
	row := uint64(id - 1)
	if row >= uint64(len(p.ValueKinds)) || row >= uint64(len(p.ValueRefs)) || p.ValueKinds[row] != want {
		panic("eval: invalid predicate value")
	}
	ref := p.ValueRefs[row]
	if ref == 0 {
		panic("eval: invalid predicate value")
	}
	value := predicateValue{kind: want}
	switch want {
	case schema.ValueKindSymbol:
		value.symbol = schema.SymbolID(ref)
		if uint32(value.symbol) > p.ProgramSymbolCount {
			panic("eval: invalid predicate value")
		}
		if _, ok := p.Symbol(value.symbol); !ok {
			panic("eval: invalid predicate value")
		}
	case schema.ValueKindInteger:
		if uint64(ref-1) >= uint64(len(p.IntegerValues)) {
			panic("eval: invalid predicate value")
		}
		value.integer = p.IntegerValues[ref-1]
	case schema.ValueKindBoolean:
		if uint64(ref-1) >= uint64(len(p.BooleanValues)) || p.BooleanValues[ref-1] > 1 {
			panic("eval: invalid predicate value")
		}
		value.boolean = p.BooleanValues[ref-1] != 0
	case schema.ValueKindTimestamp:
		if uint64(ref-1) >= uint64(len(p.TimestampValues)) {
			panic("eval: invalid predicate value")
		}
		value.timestamp = p.TimestampValues[ref-1]
	default:
		panic("eval: invalid predicate value kind")
	}
	return value
}

func requireColumnLength(length int, columns, width uint32) {
	if uint64(length) != uint64(columns)*uint64(width) {
		panic("eval: invalid batch column")
	}
}

func orderedPredicate(opcode program.Opcode) bool {
	return opcode >= program.OpcodeLess && opcode <= program.OpcodeGreaterEqual
}

func compareInt64(opcode program.Opcode, left, right int64) bool {
	switch opcode {
	case program.OpcodeEqual:
		return left == right
	case program.OpcodeNotEqual:
		return left != right
	case program.OpcodeLess:
		return left < right
	case program.OpcodeLessEqual:
		return left <= right
	case program.OpcodeGreater:
		return left > right
	case program.OpcodeGreaterEqual:
		return left >= right
	default:
		panic("eval: invalid ordered predicate")
	}
}

func fillInMatches(dst []uint64, batch Batch, p *program.Program, kind schema.ValueKind, column uint32, start, count uint32) {
	rows := uint64(batch.Rows)
	words := uint64(len(dst))
	for i := uint32(0); i < count; i++ {
		value := programPredicateValue(p, p.ListValues[start+i], kind)
		switch kind {
		case schema.ValueKindSymbol:
			values := batch.SymbolValues[uint64(column)*rows : uint64(column+1)*rows]
			for request, got := range values {
				if got == value.symbol {
					dst[request>>6] |= uint64(1) << (uint(request) & 63)
				}
			}
		case schema.ValueKindInteger:
			values := batch.IntegerValues[uint64(column)*rows : uint64(column+1)*rows]
			for request, got := range values {
				if got == value.integer {
					dst[request>>6] |= uint64(1) << (uint(request) & 63)
				}
			}
		case schema.ValueKindBoolean:
			values := batch.BooleanValues[uint64(column)*words : uint64(column+1)*words]
			for word, got := range values {
				if value.boolean {
					dst[word] |= got
				} else {
					dst[word] |= ^got
				}
			}
		case schema.ValueKindTimestamp:
			values := batch.TimestampValues[uint64(column)*rows : uint64(column+1)*rows]
			for request, got := range values {
				if got == value.timestamp {
					dst[request>>6] |= uint64(1) << (uint(request) & 63)
				}
			}
		default:
			panic("eval: invalid in predicate kind")
		}
	}
}

func evalPredicate(dst truth.Planes, reasons ReasonPlanes, batch Batch, p *program.Program, instruction schema.InstructionID) {
	if p == nil || instruction == 0 {
		panic("eval: invalid predicate instruction")
	}
	row := uint64(instruction - 1)
	if row >= uint64(len(p.Opcodes)) || row >= uint64(len(p.Fields)) || row >= uint64(len(p.Values)) {
		panic("eval: invalid predicate instruction")
	}
	opcode := p.Opcodes[row]
	if opcode < program.OpcodeEqual || opcode > program.OpcodeGreaterEqual {
		panic("eval: unsupported predicate opcode")
	}
	field := p.Fields[row]
	kind, column, ok := p.FieldIndex.Lookup(field)
	if !ok || !kind.Valid() {
		panic("eval: invalid predicate field")
	}
	if column >= p.FieldIndex.Counts[kind] {
		panic("eval: invalid predicate column")
	}
	words := truth.WordCount(batch.Rows)
	requireColumnLength(len(batch.PresenceMasks), uint32(len(p.FieldIndex.Kinds)), uint32(words))
	presenceStart := uint64(field-1) * uint64(words)
	presenceEnd := presenceStart + uint64(words)
	if presenceEnd > uint64(len(batch.PresenceMasks)) {
		panic("eval: invalid batch presence")
	}

	var value predicateValue
	var listStart, listCount uint32
	switch opcode {
	case program.OpcodeExists:
		if p.Values[row] != 0 {
			panic("eval: invalid exists value")
		}
	case program.OpcodeIn:
		if p.Values[row] != 0 || row >= uint64(len(p.ListStarts)) || row >= uint64(len(p.ListCounts)) {
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
	default:
		value = programPredicateValue(p, p.Values[row], kind)
	}
	if kind == schema.ValueKindPresence && opcode != program.OpcodeExists {
		panic("eval: presence field requires exists")
	}
	if orderedPredicate(opcode) && kind != schema.ValueKindInteger && kind != schema.ValueKindTimestamp {
		panic("eval: ordered predicate requires numeric field")
	}

	switch kind {
	case schema.ValueKindSymbol:
		requireColumnLength(len(batch.SymbolValues), p.FieldIndex.Counts[kind], batch.Rows)
	case schema.ValueKindInteger:
		requireColumnLength(len(batch.IntegerValues), p.FieldIndex.Counts[kind], batch.Rows)
	case schema.ValueKindBoolean:
		requireColumnLength(len(batch.BooleanValues), p.FieldIndex.Counts[kind], uint32(words))
	case schema.ValueKindTimestamp:
		requireColumnLength(len(batch.TimestampValues), p.FieldIndex.Counts[kind], batch.Rows)
	}

	resetLeafOutputs(dst, reasons, batch.Rows)
	presence := batch.PresenceMasks[int(presenceStart):int(presenceEnd):int(presenceEnd)]
	missing := reasons.Plane(truth.ReasonMissing, batch.Rows)
	if opcode == program.OpcodeIn {
		fillInMatches(dst.Positive, batch, p, kind, column, listStart, listCount)
	}
	for word := 0; word < words; word++ {
		valid := leafWordMask(word, words, batch.Rows)
		present := presence[word] & valid
		missing[word] = valid &^ present
		if opcode == program.OpcodeExists {
			dst.Positive[word] = present
			continue
		}

		matches := dst.Positive[word]
		if opcode != program.OpcodeIn {
			matches = 0
			switch kind {
			case schema.ValueKindSymbol:
				values := batch.SymbolValues[uint64(column)*uint64(batch.Rows):]
				start := uint64(word) << 6
				end := min(start+64, uint64(batch.Rows))
				for request := start; request < end; request++ {
					equal := values[request] == value.symbol
					if equal == (opcode == program.OpcodeEqual) {
						matches |= uint64(1) << (request & 63)
					}
				}
			case schema.ValueKindInteger:
				values := batch.IntegerValues[uint64(column)*uint64(batch.Rows):]
				start := uint64(word) << 6
				end := min(start+64, uint64(batch.Rows))
				for request := start; request < end; request++ {
					if compareInt64(opcode, values[request], value.integer) {
						matches |= uint64(1) << (request & 63)
					}
				}
			case schema.ValueKindBoolean:
				values := batch.BooleanValues[uint64(column)*uint64(words):]
				matches = values[word]
				if !value.boolean {
					matches = ^matches
				}
				if opcode == program.OpcodeNotEqual {
					matches = ^matches
				}
			case schema.ValueKindTimestamp:
				values := batch.TimestampValues[uint64(column)*uint64(batch.Rows):]
				start := uint64(word) << 6
				end := min(start+64, uint64(batch.Rows))
				for request := start; request < end; request++ {
					if compareInt64(opcode, values[request], value.timestamp) {
						matches |= uint64(1) << (request & 63)
					}
				}
			}
		}
		dst.Positive[word] = present & matches
		dst.Negative[word] = present &^ matches
	}
}
