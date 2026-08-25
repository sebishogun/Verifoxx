package eval

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func executorBenchmarkProgram(t testing.TB) *program.Program {
	t.Helper()
	p := executionTestProgram(t, 4)
	assertion := p.ClauseAssertionRoots[0]
	evidence := p.ClauseEvidenceIDs[0]
	not := appendExecutorInstruction(p, program.OpcodeNot, 0, 0, []schema.InstructionID{assertion}, 0, 0)
	all := appendExecutorInstruction(p, program.OpcodeAll, 0, 0, []schema.InstructionID{assertion, evidence}, 0, 0)
	any := appendExecutorInstruction(p, program.OpcodeAny, 0, 0, []schema.InstructionID{assertion, not}, 0, 0)
	p.RootFlags[not-1] = program.RootAssertion
	p.RootFlags[all-1] = program.RootAssertion
	p.RootFlags[any-1] = program.RootAssertion
	p.TruthSlots = append(p.TruthSlots, 4, 5, 6)
	p.ReasonSlots = append(p.ReasonSlots, 4, 5, 6)
	p.TruthSlotCount = 6
	p.ReasonSlotCount = 6
	p.ClauseAssertionRoots = []schema.InstructionID{all, any, not, assertion}
	p.ClauseOnSatisfied = []schema.OutcomeID{1, 2, 3, 4}
	p.ClauseOnFalse = []schema.OutcomeID{2, 3, 4, 1}
	setExecutionResolutionRows(t, p)
	return p
}

func executorBenchmarkBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 3, rows); err != nil {
		t.Fatalf("Begin: %v", err)
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
		if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
			t.Fatal(err)
		}
		if row&3 != 2 {
			value := executionSymbolYes
			if row&3 == 1 {
				value = executionSymbolNo
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

func BenchmarkExecutorScalar(b *testing.B) {
	p := executorBenchmarkProgram(b)
	batch := executorBenchmarkBatch(b, p, 1024)
	var executor Executor
	var dst result.Batch
	if err := executor.executeMode(&dst, p, batch, executionScalar); err != nil {
		b.Fatalf("prime Execute: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := executor.executeMode(&dst, p, batch, executionScalar); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecutorBoolean(b *testing.B) {
	p, batch, executor := booleanExecutorFixture(b, true, 1024)
	executor.executeSchedule(p, batch)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		executor.executeSchedule(p, batch)
	}
}

func BenchmarkExecutorDefined(b *testing.B) {
	p, batch, executor := definedExecutorFixture(b, 1024)
	executor.executeSchedule(p, batch)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		executor.executeSchedule(p, batch)
	}
}
