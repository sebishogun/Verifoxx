package eval

import (
	"math"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/simdops"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func TestSIMDScheduleMatchesScalar(t *testing.T) {
	for _, rows := range simdTestRows() {
		t.Run(testRowsName(rows), func(t *testing.T) {
			fixture := simdScheduleTestProgram(t)
			p := fixture.program
			batch := simdPredicateBatch(t, p, rows)
			var leafExecutor Executor
			for _, instruction := range fixture.vectorLeaves {
				wantTruth, wantReasons := makeLeafOutputs(rows)
				gotTruth, gotReasons := makeLeafOutputs(rows)
				evalPredicate(wantTruth, wantReasons, batch, p, instruction)
				if !leafExecutor.evalPredicateSIMD(gotTruth, gotReasons, batch, p, instruction, executionSIMD) {
					t.Fatalf("instruction %d did not use SIMD", instruction)
				}
				if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
					!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
					!slices.Equal(gotReasons.Words, wantReasons.Words) {
					t.Fatalf("rows=%d instruction=%d SIMD leaf differs from scalar", rows, instruction)
				}
			}
			fallbackTruth, fallbackReasons := makeLeafOutputs(rows)
			if leafExecutor.evalPredicateSIMD(
				fallbackTruth, fallbackReasons, batch, p, fixture.scalarLeaf, executionSIMD,
			) {
				t.Fatal("In predicate unexpectedly used SIMD")
			}

			var scalar, vector Executor
			if err := scalar.prepare(p, batch); err != nil {
				t.Fatalf("scalar prepare: %v", err)
			}
			if err := vector.prepare(p, batch); err != nil {
				t.Fatalf("SIMD prepare: %v", err)
			}
			scalar.executeScheduleMode(p, batch, executionScalar)
			vector.executeScheduleMode(p, batch, executionSIMD)
			if !slices.Equal(vector.truthWords, scalar.truthWords) ||
				!slices.Equal(vector.reasonWords, scalar.reasonWords) {
				t.Fatalf("rows=%d SIMD schedule differs from scalar", rows)
			}
		})
	}

	fixture := simdScheduleTestProgram(t)
	p := fixture.program
	batch := simdPredicateBatch(t, p, 1024)
	var vector Executor
	if err := vector.prepare(p, batch); err != nil {
		t.Fatalf("allocation prepare: %v", err)
	}
	vector.executeScheduleMode(p, batch, executionSIMD)
	if allocs := testing.AllocsPerRun(100, func() {
		vector.executeScheduleMode(p, batch, executionSIMD)
	}); allocs != 0 {
		t.Fatalf("warm SIMD schedule allocates: %.2f allocs/run", allocs)
	}
	testSIMDTruthStates(t)
	testSIMDEndToEnd(t)
	testSIMDAutoSelection(t)
	testSIMDBooleanSelection(t)
	testSIMDRangeView(t)
	testSIMDRejectsMalformedPrograms(t)
}

func TestSIMDPredicateMasksDirtyPresenceTail(t *testing.T) {
	const rows = uint32(65)
	fixture := simdScheduleTestProgram(t)
	instruction := fixture.vectorLeaves[0]
	batch := simdPredicateBatch(t, fixture.program, rows)
	presence := batchWordColumn(batch, batch.PresenceMasks, uint32(fixture.program.Fields[instruction-1]-1))
	presence[len(presence)-1] = ^uint64(0)

	wantTruth, wantReasons := makeLeafOutputs(rows)
	gotTruth, gotReasons := makeLeafOutputs(rows)
	evalPredicate(wantTruth, wantReasons, batch, fixture.program, instruction)
	var executor Executor
	if !executor.evalPredicateSIMD(gotTruth, gotReasons, batch, fixture.program, instruction, executionSIMD) {
		t.Fatal("symbol predicate did not use SIMD")
	}
	if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
		!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
		!slices.Equal(gotReasons.Words, wantReasons.Words) {
		t.Fatalf("SIMD dirty-tail result = %+v/%v, want %+v/%v", gotTruth, gotReasons.Words, wantTruth, wantReasons.Words)
	}
}

func testSIMDRangeView(t *testing.T) {
	t.Helper()
	fixture := simdScheduleTestProgram(t)
	batch := simdPredicateBatch(t, fixture.program, 129)
	batch.Rows = 64
	batch.rowBase = 64
	batch.rowStride = 129
	batch.RequestIDs = batch.RequestIDs[64:128:128]

	var executor Executor
	for _, instruction := range fixture.vectorLeaves {
		wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
		gotTruth, gotReasons := makeLeafOutputs(batch.Rows)
		evalPredicate(wantTruth, wantReasons, batch, fixture.program, instruction)
		if !executor.evalPredicateSIMD(gotTruth, gotReasons, batch, fixture.program, instruction, executionSIMD) {
			t.Fatalf("instruction %d did not use SIMD", instruction)
		}
		if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
			!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
			!slices.Equal(gotReasons.Words, wantReasons.Words) {
			t.Fatalf("instruction %d SIMD range differs from scalar", instruction)
		}
	}
	var scalar, indexed Executor
	if err := scalar.prepare(fixture.program, batch); err != nil {
		t.Fatalf("scalar prepare: %v", err)
	}
	if err := indexed.prepare(fixture.program, batch); err != nil {
		t.Fatalf("indexed prepare: %v", err)
	}
	scalar.executeScheduleMode(fixture.program, batch, executionScalar)
	indexed.executeScheduleMode(fixture.program, batch, executionIndex)
	if !slices.Equal(indexed.truthWords, scalar.truthWords) ||
		!slices.Equal(indexed.reasonWords, scalar.reasonWords) {
		t.Fatal("indexed range fallback differs from scalar")
	}
}

func testSIMDRejectsMalformedPrograms(t *testing.T) {
	t.Helper()
	tests := []struct {
		name   string
		mutate func(*program.Program, schema.InstructionID)
	}{
		{"unresolved field", func(p *program.Program, instruction schema.InstructionID) {
			p.Fields[instruction-1] = schema.FieldID(len(p.FieldIndex.Kinds) + 1)
		}},
		{"bad field column", func(p *program.Program, instruction schema.InstructionID) {
			p.FieldIndex.Columns[p.Fields[instruction-1]-1] = math.MaxUint32
		}},
		{"ordered symbol", func(p *program.Program, instruction schema.InstructionID) {
			p.Opcodes[instruction-1] = program.OpcodeLess
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := simdScheduleTestProgram(t)
			instruction := fixture.vectorLeaves[0]
			batch := simdPredicateBatch(t, fixture.program, simdComparisonMinRows)
			tc.mutate(fixture.program, instruction)
			dst := truth.Planes{Positive: []uint64{11}, Negative: []uint64{12}}
			reasons := ReasonPlanes{Words: slices.Repeat([]uint64{13}, truth.ReasonCount)}
			var executor Executor
			requirePanic(t, func() {
				executor.evalPredicateSIMD(dst, reasons, batch, fixture.program, instruction, executionSIMD)
			})
			if dst.Positive[0] != 11 || dst.Negative[0] != 12 ||
				!slices.Equal(reasons.Words, slices.Repeat([]uint64{13}, truth.ReasonCount)) {
				t.Fatal("malformed Program mutated output")
			}
		})
	}
}

func testSIMDBooleanSelection(t *testing.T) {
	t.Helper()
	active := !evaluatorSIMD.PureGo && evaluatorSIMD.Tier != "scalar"
	fixture := simdScheduleTestProgram(t)
	instruction := fixture.vectorLeaves[14]
	crossover := uint32((simdWordMinWords-1)*64 + 1)
	for _, rows := range [...]uint32{crossover - 1, crossover} {
		batch := simdPredicateBatch(t, fixture.program, rows)
		dst, reasons := makeLeafOutputs(rows)
		var executor Executor
		got := executor.evalPredicateSIMD(dst, reasons, batch, fixture.program, instruction, executionAuto)
		want := active && truth.WordCount(rows) >= simdWordMinWords
		if got != want {
			t.Fatalf("Boolean automatic SIMD at %d rows = %v want %v", rows, got, want)
		}
	}
}

func testSIMDAutoSelection(t *testing.T) {
	t.Helper()
	active := !evaluatorSIMD.PureGo && evaluatorSIMD.Tier != "scalar"
	if got := useSIMDRows(executionAuto, simdComparisonMinRows-1, simdComparisonMinRows); got {
		t.Fatal("automatic comparison selected SIMD below crossover")
	}
	if got := useSIMDRows(executionAuto, simdComparisonMinRows, simdComparisonMinRows); got != active {
		t.Fatalf("automatic comparison selection = %v want %v", got, active)
	}
	if got := useSIMDWords(executionAuto, 7); got {
		t.Fatal("automatic word reduction selected SIMD below measured crossover")
	}
	if got := useSIMDWords(executionAuto, 8); got != active {
		t.Fatalf("automatic word selection = %v want %v", got, active)
	}
}

func testSIMDEndToEnd(t *testing.T) {
	t.Helper()
	p := executorBenchmarkProgram(t)
	batch := executorBenchmarkBatch(t, p, 1024)
	var scalar, vector Executor
	var scalarResult, vectorResult result.Batch
	if err := scalar.executeMode(&scalarResult, p, batch, executionScalar); err != nil {
		t.Fatalf("scalar Execute: %v", err)
	}
	if err := vector.executeMode(&vectorResult, p, batch, executionSIMD); err != nil {
		t.Fatalf("SIMD Execute: %v", err)
	}
	if !reflect.DeepEqual(vectorResult, scalarResult) {
		t.Fatal("SIMD end-to-end result differs from scalar")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if err := vector.executeMode(&vectorResult, p, batch, executionSIMD); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatalf("warm SIMD Execute allocates: %.2f allocs/run", allocs)
	}
}

func testSIMDTruthStates(t *testing.T) {
	t.Helper()
	const rows = uint32(65)
	left := patternedTruth(rows, false)
	right := patternedTruth(rows, true)

	wantAnd, _ := makeLeafOutputs(rows)
	truth.And(wantAnd, left, right, rows)
	gotAnd := truth.Planes{
		Positive: slices.Clone(left.Positive),
		Negative: slices.Clone(left.Negative),
	}
	simdTruthAnd(gotAnd, gotAnd, right, rows)
	if !slices.Equal(gotAnd.Positive, wantAnd.Positive) || !slices.Equal(gotAnd.Negative, wantAnd.Negative) {
		t.Fatal("SIMD truth And differs from scalar across four states")
	}

	wantOr, _ := makeLeafOutputs(rows)
	truth.Or(wantOr, left, right, rows)
	gotOr := truth.Planes{
		Positive: slices.Clone(right.Positive),
		Negative: slices.Clone(right.Negative),
	}
	simdTruthOr(gotOr, left, gotOr, rows)
	if !slices.Equal(gotOr.Positive, wantOr.Positive) || !slices.Equal(gotOr.Negative, wantOr.Negative) {
		t.Fatal("SIMD truth Or differs from scalar across four states")
	}

	words := truth.WordCount(rows)
	leftReasons := make([]uint64, truth.ReasonCount*words)
	rightReasons := make([]uint64, truth.ReasonCount*words)
	for reason := 0; reason < truth.ReasonCount; reason++ {
		for row := uint32(0); row < rows; row++ {
			bit := uint64(1) << (row & 63)
			word := reason*words + int(row>>6)
			if (int(row)+reason)%3 == 0 {
				leftReasons[word] |= bit
			}
			if (int(row)+reason)%5 == 0 {
				rightReasons[word] |= bit
			}
		}
	}
	wantReasons := make([]uint64, len(leftReasons))
	for i := range wantReasons {
		wantReasons[i] = leftReasons[i] | rightReasons[i]
	}
	gotReasons := slices.Clone(leftReasons)
	simdReasonOr(gotReasons, gotReasons, rightReasons)
	if !slices.Equal(gotReasons, wantReasons) {
		t.Fatal("SIMD reason union differs across reason planes")
	}
}

func patternedTruth(rows uint32, right bool) truth.Planes {
	planes, _ := makeLeafOutputs(rows)
	for row := uint32(0); row < rows; row++ {
		state := row & 3
		if right {
			state = row >> 2 & 3
		}
		bit := uint64(1) << (row & 63)
		word := row >> 6
		if state&1 != 0 {
			planes.Positive[word] |= bit
		}
		if state&2 != 0 {
			planes.Negative[word] |= bit
		}
	}
	if rows&63 != 0 {
		planes.Positive[len(planes.Positive)-1] |= ^(uint64(1)<<(rows&63) - 1)
		planes.Negative[len(planes.Negative)-1] |= ^(uint64(1)<<(rows&63) - 1)
	}
	return planes
}

func simdTestRows() []uint32 {
	values := []uint32{0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 65}
	thresholds := simdops.Runtime().Thresholds
	addThresholdRows := func(threshold int) {
		if threshold <= 0 {
			return
		}
		for _, delta := range [...]int{-1, 0, 1} {
			value := threshold + delta
			if value >= 0 {
				values = append(values, uint32(value))
			}
		}
	}
	addThresholdRows(thresholds.CompareU32)
	addThresholdRows(thresholds.CompareI64)
	addThresholdRows(thresholds.PackMask)
	addThresholdRows(thresholds.WordBitwise * 64)
	addThresholdRows(simdComparisonMinRows)
	addThresholdRows((simdWordMinWords-1)*64 + 1)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return slices.Compact(values)
}

func testRowsName(rows uint32) string {
	if rows == 0 {
		return "rows=0"
	}
	buf := [32]byte{'r', 'o', 'w', 's', '='}
	i := len(buf)
	for value := rows; value != 0; value /= 10 {
		i--
		buf[i] = byte('0' + value%10)
	}
	return string(append(buf[:5], buf[i:]...))
}

type simdScheduleFixture struct {
	program      *program.Program
	vectorLeaves []schema.InstructionID
	scalarLeaf   schema.InstructionID
}

func simdScheduleTestProgram(t testing.TB) simdScheduleFixture {
	t.Helper()
	p := predicateTestProgram(t)
	vectorLeaves := []schema.InstructionID{
		appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeNotEqual, 1, 1, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeNotEqual, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeLess, 2, 6, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeLessEqual, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeGreater, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeGreaterEqual, 2, 6, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 4, 4, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeNotEqual, 4, 4, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeLess, 4, 8, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeLessEqual, 4, 4, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeGreater, 4, 4, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeGreaterEqual, 4, 8, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 3, 3, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeNotEqual, 3, 3, nil, 0, 0),
	}
	exists := appendExecutorInstruction(p, program.OpcodeExists, 5, 0, nil, 0, 0)
	listStart := uint32(len(p.ListValues))
	p.ListValues = append(p.ListValues, 1, 5)
	in := appendExecutorInstruction(p, program.OpcodeIn, 1, 0, nil, 0, 0)
	p.ListStarts[in-1] = listStart
	p.ListCounts[in-1] = 2
	all := appendExecutorInstruction(p, program.OpcodeAll, 0, 0, []schema.InstructionID{
		vectorLeaves[0], vectorLeaves[2], vectorLeaves[14],
	}, 0, 0)
	any := appendExecutorInstruction(p, program.OpcodeAny, 0, 0, []schema.InstructionID{
		vectorLeaves[1], vectorLeaves[3], vectorLeaves[15],
	}, 0, 0)
	root := appendExecutorInstruction(p, program.OpcodeNot, 0, 0, []schema.InstructionID{all}, 0, 0)
	p.RootFlags[root-1] = program.RootAssertion
	_ = exists
	_ = any
	for instruction := range p.Opcodes {
		slot := schema.SlotID(instruction + 1)
		p.TruthSlots = append(p.TruthSlots, slot)
		p.ReasonSlots = append(p.ReasonSlots, slot)
	}
	p.TruthSlots[all-1] = p.TruthSlots[vectorLeaves[0]-1]
	p.ReasonSlots[all-1] = p.ReasonSlots[vectorLeaves[0]-1]
	p.TruthSlots[any-1] = p.TruthSlots[vectorLeaves[15]-1]
	p.ReasonSlots[any-1] = p.ReasonSlots[vectorLeaves[15]-1]
	p.TruthSlotCount = uint32(len(p.Opcodes))
	p.ReasonSlotCount = uint32(len(p.Opcodes))
	return simdScheduleFixture{program: p, vectorLeaves: vectorLeaves, scalarLeaf: in}
}

func simdPredicateBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		if row%5 != 0 {
			if err := builder.SetSymbol(row, 1, schema.SymbolID(row&1)+1); err != nil {
				t.Fatal(err)
			}
		}
		if row%7 != 0 {
			if err := builder.SetInteger(row, 2, int64(6+row%4)); err != nil {
				t.Fatal(err)
			}
		}
		if row%3 != 0 {
			if err := builder.SetBoolean(row, 3, row&1 == 0); err != nil {
				t.Fatal(err)
			}
		}
		if row%4 != 0 {
			if err := builder.SetTimestamp(row, 4, int64(98+row%4)); err != nil {
				t.Fatal(err)
			}
		}
		if row%6 != 0 {
			if err := builder.SetPresent(row, 5); err != nil {
				t.Fatal(err)
			}
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}
