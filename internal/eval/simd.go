package eval

import (
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/simdops"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type executionMode uint8

const (
	executionAuto executionMode = iota
	executionScalar
	executionSIMD
	executionIndex
)

const (
	simdComparisonMinRows = 64
	simdWordMinWords      = 8
)

var evaluatorSIMD = simdops.Runtime()

func useSIMDRows(mode executionMode, rows uint32, threshold int) bool {
	switch mode {
	case executionSIMD:
		return true
	case executionScalar, executionIndex:
		return false
	default:
		return !evaluatorSIMD.PureGo && evaluatorSIMD.Tier != "scalar" && uint64(rows) >= uint64(threshold)
	}
}

func useSIMDWords(mode executionMode, words int) bool {
	threshold := max(simdWordMinWords, evaluatorSIMD.Thresholds.WordBitwise)
	return useSIMDRows(mode, uint32(words), threshold)
}

func simdComparison(opcode program.Opcode) (simdops.Comparison, bool) {
	switch opcode {
	case program.OpcodeEqual:
		return simdops.Equal, true
	case program.OpcodeNotEqual:
		return simdops.NotEqual, true
	case program.OpcodeLess:
		return simdops.Less, true
	case program.OpcodeLessEqual:
		return simdops.LessEqual, true
	case program.OpcodeGreater:
		return simdops.Greater, true
	case program.OpcodeGreaterEqual:
		return simdops.GreaterEqual, true
	default:
		return 0, false
	}
}

func (e *Executor) evalPredicateSIMD(
	dst truth.Planes,
	reasons ReasonPlanes,
	batch Batch,
	p *program.Program,
	instruction schema.InstructionID,
	mode executionMode,
) bool {
	row := int(instruction - 1)
	opcode := p.Opcodes[row]
	comparison, ok := simdComparison(opcode)
	if !ok {
		return false
	}
	kind, column, ok := p.FieldIndex.Lookup(p.Fields[row])
	if !ok || !kind.Valid() {
		panic("eval: invalid predicate field")
	}
	if column >= p.FieldIndex.Counts[kind] {
		panic("eval: invalid predicate column")
	}
	if orderedPredicate(opcode) && kind != schema.ValueKindInteger && kind != schema.ValueKindTimestamp {
		panic("eval: ordered predicate requires numeric field")
	}
	if kind != schema.ValueKindSymbol && kind != schema.ValueKindInteger &&
		kind != schema.ValueKindTimestamp && kind != schema.ValueKindBoolean {
		return false
	}
	if kind == schema.ValueKindBoolean {
		if !useSIMDWords(mode, truth.WordCount(batch.Rows)) {
			return false
		}
	} else if !useSIMDRows(mode, batch.Rows, simdComparisonMinRows) {
		return false
	}

	value := programPredicateValue(p, p.Values[row], kind)
	words := resetLeafOutputs(dst, reasons, batch.Rows)
	presence := batchWordColumn(batch, batch.PresenceMasks, uint32(p.Fields[row]-1))
	missing := reasons.plane(truth.ReasonMissing, words)
	for word := range words {
		valid := leafWordMask(word, words, batch.Rows)
		missing[word] = valid &^ presence[word]
	}
	if kind == schema.ValueKindBoolean {
		values := batchWordColumn(batch, batch.BooleanValues, column)
		matchesSetBits := value.boolean == (opcode == program.OpcodeEqual)
		if matchesSetBits {
			simdops.AndWords(dst.Positive, presence, values)
			simdops.AndNotWords(dst.Negative, presence, values)
		} else {
			simdops.AndNotWords(dst.Positive, presence, values)
			simdops.AndWords(dst.Negative, presence, values)
		}
		maskSIMDTruthTail(dst, batch.Rows)
		return true
	}

	e.compareMask = resizeExecutorScratch(e.compareMask, int(batch.Rows))
	switch kind {
	case schema.ValueKindSymbol:
		simdops.CompareU32(e.compareMask, batchRowColumn(batch, batch.SymbolValues, column), value.symbol, comparison)
	case schema.ValueKindInteger:
		simdops.CompareI64(e.compareMask, batchRowColumn(batch, batch.IntegerValues, column), value.integer, comparison)
	case schema.ValueKindTimestamp:
		simdops.CompareI64(e.compareMask, batchRowColumn(batch, batch.TimestampValues, column), value.timestamp, comparison)
	}
	simdops.PackMask(dst.Positive, e.compareMask)
	simdops.AndWords(dst.Positive, dst.Positive, presence)
	simdops.AndNotWords(dst.Negative, presence, dst.Positive)
	maskSIMDTruthTail(dst, batch.Rows)
	return true
}

func simdTruthAnd(dst, left, right truth.Planes, rows uint32) {
	simdops.AndWords(dst.Positive, left.Positive, right.Positive)
	simdops.OrWords(dst.Negative, left.Negative, right.Negative)
	maskSIMDTruthTail(dst, rows)
}

func simdTruthOr(dst, left, right truth.Planes, rows uint32) {
	simdops.OrWords(dst.Positive, left.Positive, right.Positive)
	simdops.AndWords(dst.Negative, left.Negative, right.Negative)
	maskSIMDTruthTail(dst, rows)
}

func simdReasonOr(dst, left, right []uint64) {
	simdops.OrWords(dst, left, right)
}

func maskSIMDTruthTail(dst truth.Planes, rows uint32) {
	remaining := rows & 63
	if remaining == 0 {
		return
	}
	mask := uint64(1)<<remaining - 1
	last := len(dst.Positive) - 1
	dst.Positive[last] &= mask
	dst.Negative[last] &= mask
}
