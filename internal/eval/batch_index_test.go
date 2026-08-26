package eval

import (
	"reflect"
	"slices"
	"testing"

	policyindex "github.com/sebishogun/nornrune/internal/index"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func indexedExecutionProgram(t testing.TB) *program.Program {
	t.Helper()
	p := executionTestProgram(t, 1)
	p.ValueKinds = append(p.ValueKinds, schema.ValueKindSymbol)
	p.ValueRefs = append(p.ValueRefs, uint32(executionSymbolNo))

	notEqual := appendExecutorInstruction(p, program.OpcodeNotEqual, 2, 3, nil, 0, 0)
	listStart := uint32(len(p.ListValues))
	p.ListValues = append(p.ListValues, 2, 3, 2)
	in := appendExecutorInstruction(p, program.OpcodeIn, 2, 0, nil, 0, 0)
	p.ListStarts[in-1] = listStart
	p.ListCounts[in-1] = 3
	all := appendExecutorInstruction(p, program.OpcodeAll, 0, 0,
		[]schema.InstructionID{p.ClauseAssertionRoots[0], notEqual, in}, 0, 0)
	p.RootFlags[all-1] = program.RootAssertion
	p.TruthSlots = append(p.TruthSlots, 4, 5, 6)
	p.ReasonSlots = append(p.ReasonSlots, 4, 5, 6)
	p.TruthSlotCount = 6
	p.ReasonSlotCount = 6
	p.ClauseAssertionRoots[0] = all
	p.FactIndexSpec = policyindex.FactSpec{
		FieldIDs:    []schema.FieldID{2},
		Columns:     []uint32{1},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{2},
		UseCounts:   []uint32{96},
		Values: []schema.SymbolID{
			executionSymbolYes, executionSymbolNo,
		},
	}
	return p
}

func indexedExecutionBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 3, rows); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	extension, err := builder.InternSymbol([]byte("batch-extension"))
	if err != nil {
		t.Fatalf("InternSymbol: %v", err)
	}
	for row, record := range []EvidenceRecord{
		evidenceRecord(1, 1, 1),
		evidenceRecord(2, 1, 2),
		evidenceRecord(3, 1, 3),
	} {
		if err := builder.SetEvidence(uint32(row), record); err != nil {
			t.Fatal(err)
		}
	}
	offsets := make([]uint32, rows+1)
	refs := make([]uint32, rows)
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatal(err)
		}
		if row%7 != 0 {
			value := executionSymbolActive
			if row%11 == 0 {
				value = executionSymbolInactive
			}
			if err := builder.SetSymbol(row, 1, value); err != nil {
				t.Fatal(err)
			}
		}
		if row%5 != 0 {
			value := executionSymbolYes
			if row&1 != 0 {
				value = executionSymbolNo
			}
			if rows > 4 && row == rows-1 {
				value = extension
			}
			if err := builder.SetSymbol(row, 2, value); err != nil {
				t.Fatal(err)
			}
		}
		offsets[row+1] = row + 1
		refs[row] = row % 3
	}
	if err := builder.SetEvidenceCSR(offsets, refs); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func TestFactIndexExecutionMatchesDirect(t *testing.T) {
	if useFactIndex(executionAuto, factIndexMinRows-1) || !useFactIndex(executionAuto, factIndexMinRows) {
		t.Fatalf("automatic fact-index row crossover is not exact at %d", factIndexMinRows)
	}
	p := indexedExecutionProgram(t)
	for _, rows := range []uint32{factIndexMinRows - 1, factIndexMinRows, factIndexMinRows + 1} {
		t.Run(testRowsName(rows), func(t *testing.T) {
			batch := indexedExecutionBatch(t, p, rows)
			var scalar, automatic Executor
			var want, got result.Batch
			if err := scalar.executeMode(&want, p, batch, executionScalar); err != nil {
				t.Fatalf("scalar Execute: %v", err)
			}
			if err := automatic.executeMode(&got, p, batch, executionAuto); err != nil {
				t.Fatalf("automatic Execute: %v", err)
			}
			if !reflect.DeepEqual(got, want) ||
				!slices.Equal(automatic.truthWords, scalar.truthWords) ||
				!slices.Equal(automatic.reasonWords, scalar.reasonWords) {
				t.Fatal("automatic execution differs from scalar")
			}
			wantMasks := 0
			if rows >= factIndexMinRows {
				wantMasks = len(p.FactIndexSpec.Values) * int(batch.WordCount())
			}
			if len(automatic.factIndex.ValueMasks) != wantMasks {
				t.Fatalf("automatic fact masks = %d words, want %d", len(automatic.factIndex.ValueMasks), wantMasks)
			}
		})
	}
	batch := indexedExecutionBatch(t, p, 65)

	var scalar Executor
	var want result.Batch
	if err := scalar.executeMode(&want, p, batch, executionScalar); err != nil {
		t.Fatalf("scalar Execute: %v", err)
	}
	for _, mode := range []executionMode{executionSIMD, executionIndex, executionAuto} {
		t.Run(mode.name(), func(t *testing.T) {
			var executor Executor
			var got result.Batch
			if err := executor.executeMode(&got, p, batch, mode); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !reflect.DeepEqual(got, want) ||
				!slices.Equal(executor.truthWords, scalar.truthWords) ||
				!slices.Equal(executor.reasonWords, scalar.reasonWords) {
				t.Fatalf("%s execution differs from scalar", mode.name())
			}
			if mode != executionSIMD && len(executor.factIndex.ValueMasks) != len(p.FactIndexSpec.Values)*int(batch.WordCount()) {
				t.Fatalf("%s fact masks = %d words", mode.name(), len(executor.factIndex.ValueMasks))
			}
		})
	}

	var executor Executor
	var got result.Batch
	if err := executor.executeMode(&got, p, batch, executionIndex); err != nil {
		t.Fatalf("prime indexed Execute: %v", err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if err := executor.executeMode(&got, p, batch, executionIndex); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatalf("warm indexed Execute allocations = %g, want 0", allocs)
	}

	poisonCapacity(executor.factIndex.ValueMasks, ^uint64(0))
	poisonExecutor(&executor)
	poisonResult(&got)
	if err := executor.executeMode(&got, p, indexedExecutionBatch(t, p, 1), executionIndex); err != nil {
		t.Fatalf("small indexed Execute: %v", err)
	}
	poisonCapacity(executor.factIndex.ValueMasks, ^uint64(0))
	poisonExecutor(&executor)
	poisonResult(&got)
	if err := executor.executeMode(&got, p, batch, executionIndex); err != nil {
		t.Fatalf("reused indexed Execute: %v", err)
	}
	if !reflect.DeepEqual(got, want) ||
		!slices.Equal(executor.truthWords, scalar.truthWords) ||
		!slices.Equal(executor.reasonWords, scalar.reasonWords) {
		t.Fatal("poisoned large-small-large indexed execution differs from scalar")
	}

	other := executionTestProgram(t, 1)
	if err := executor.executeMode(&got, other, executionRowsBatch(t, other, 3), executionIndex); err != nil {
		t.Fatalf("other Program Execute: %v", err)
	}
	if err := executor.executeMode(&got, p, batch, executionIndex); err != nil {
		t.Fatalf("rebound indexed Execute: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Program A-B-A indexed result differs from scalar")
	}
}

func TestExecutorRejectsMalformedFactIndexAtomically(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*policyindex.FactSpec)
	}{
		{"bad column", func(spec *policyindex.FactSpec) { spec.Columns[0] = 2 }},
		{"omitted query value", func(spec *policyindex.FactSpec) {
			spec.ValueCounts[0] = 1
			spec.Values = []schema.SymbolID{
				executionSymbolNo,
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound := executionTestProgram(t, 1)
			boundBatch := executionRowsBatch(t, bound, 1)
			candidate := indexedExecutionProgram(t)
			test.mutate(&candidate.FactIndexSpec)
			candidateBatch := indexedExecutionBatch(t, candidate, 65)

			var executor Executor
			var dst result.Batch
			if err := executor.Execute(&dst, bound, boundBatch); err != nil {
				t.Fatal(err)
			}
			assertExecutorRejectedAtomically(t, &executor, &dst, bound, candidate, candidateBatch)
		})
	}
}
