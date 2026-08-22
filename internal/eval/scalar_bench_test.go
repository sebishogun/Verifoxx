package eval

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func scalarPredicateFixture(t testing.TB, rows uint32) (*program.Program, Batch, schema.InstructionID) {
	t.Helper()
	p := predicateTestProgram(t)
	instruction := appendPredicateInstruction(p, program.OpcodeEqual, 2, 2)
	var builder Builder
	if err := builder.Begin(p, rows, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		if err := builder.SetInteger(row, 2, 7+int64(row&1)); err != nil {
			t.Fatalf("SetInteger(%d): %v", row, err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return p, batch, instruction
}

func scalarEvidenceBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	records := []EvidenceRecord{
		evidenceRecord(1, testEvidenceKindApproval, testEvidenceStateValid),
		evidenceRecord(2, testEvidenceKindApproval, testEvidenceStateStale),
	}
	offsets := make([]uint32, rows+1)
	refs := make([]uint32, 2*rows)
	for row := uint32(0); row < rows; row++ {
		offsets[row+1] = 2 * (row + 1)
		refs[2*row], refs[2*row+1] = 0, 1
	}
	return evidenceEvalBatch(t, p, rows, records, offsets, refs)
}

func TestLeafEvaluationReuse(t *testing.T) {
	p, large, instruction := scalarPredicateFixture(t, 65)
	_, small, _ := scalarPredicateFixture(t, 3)
	words := truth.WordCount(large.Rows)
	positive := make([]uint64, words)
	negative := make([]uint64, words)
	reasonWords := make([]uint64, truth.ReasonCount*words)
	evalPredicate(
		truth.Planes{Positive: positive, Negative: negative},
		ReasonPlanes{Words: reasonWords}, large, p, instruction,
	)
	for i := range positive {
		positive[i], negative[i] = math.MaxUint64, math.MaxUint64
	}
	for i := range reasonWords {
		reasonWords[i] = math.MaxUint64
	}
	evalPredicate(
		truth.Planes{Positive: positive[:1], Negative: negative[:1]},
		ReasonPlanes{Words: reasonWords[:truth.ReasonCount]}, small, p, instruction,
	)
	assertLeafWord(t,
		truth.Planes{Positive: positive[:1], Negative: negative[:1]},
		ReasonPlanes{Words: reasonWords[:truth.ReasonCount]}, small.Rows,
		1<<0|1<<2, 1<<1, 0,
	)

	evidenceProgram := evidenceEvalTestProgram()
	var states EvidenceStateIndex
	if err := states.Bind(evidenceProgram); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	largeEvidence := scalarEvidenceBatch(t, evidenceProgram, 65)
	smallEvidence := scalarEvidenceBatch(t, evidenceProgram, 3)
	for i := range positive {
		positive[i], negative[i] = math.MaxUint64, math.MaxUint64
	}
	for i := range reasonWords {
		reasonWords[i] = math.MaxUint64
	}
	predicate := EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid}
	evalEvidence(
		truth.Planes{Positive: positive, Negative: negative},
		ReasonPlanes{Words: reasonWords}, largeEvidence, evidenceProgram, &states, predicate,
	)
	for i := range positive {
		positive[i], negative[i] = math.MaxUint64, math.MaxUint64
	}
	for i := range reasonWords {
		reasonWords[i] = math.MaxUint64
	}
	dst := truth.Planes{Positive: positive[:1], Negative: negative[:1]}
	reasons := ReasonPlanes{Words: reasonWords[:truth.ReasonCount]}
	evalEvidence(dst, reasons, smallEvidence, evidenceProgram, &states, predicate)
	for row := uint32(0); row < smallEvidence.Rows; row++ {
		assertEvidenceRow(t, dst, reasons, smallEvidence.Rows, row, true, false, truth.ReasonBit(truth.ReasonStale))
	}
}

func TestLeafEvaluationAllocations(t *testing.T) {
	p, batch, instruction := scalarPredicateFixture(t, 1024)
	dst, reasons := makeLeafOutputs(batch.Rows)
	evalPredicate(dst, reasons, batch, p, instruction)
	if allocs := testing.AllocsPerRun(1000, func() {
		evalPredicate(dst, reasons, batch, p, instruction)
	}); allocs != 0 {
		t.Fatalf("evalPredicate allocations = %g, want 0", allocs)
	}

	evidenceProgram := evidenceEvalTestProgram()
	evidenceBatch := scalarEvidenceBatch(t, evidenceProgram, 1024)
	var states EvidenceStateIndex
	if err := states.Bind(evidenceProgram); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	predicate := EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid}
	dst, reasons = makeLeafOutputs(evidenceBatch.Rows)
	evalEvidence(dst, reasons, evidenceBatch, evidenceProgram, &states, predicate)
	if allocs := testing.AllocsPerRun(1000, func() {
		evalEvidence(dst, reasons, evidenceBatch, evidenceProgram, &states, predicate)
	}); allocs != 0 {
		t.Fatalf("evalEvidence allocations = %g, want 0", allocs)
	}
	requireEvidenceBatch(evidenceBatch, evidenceProgram, &states)
	evalEvidenceValidated(dst, reasons, evidenceBatch, evidenceProgram, &states, predicate)
	if allocs := testing.AllocsPerRun(1000, func() {
		evalEvidenceValidated(dst, reasons, evidenceBatch, evidenceProgram, &states, predicate)
	}); allocs != 0 {
		t.Fatalf("evalEvidenceValidated allocations = %g, want 0", allocs)
	}
}

func BenchmarkEvalPredicate(b *testing.B) {
	p, batch, instruction := scalarPredicateFixture(b, 1024)
	dst, reasons := makeLeafOutputs(batch.Rows)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evalPredicate(dst, reasons, batch, p, instruction)
	}
}

func BenchmarkEvalEvidence(b *testing.B) {
	p := evidenceEvalTestProgram()
	batch := scalarEvidenceBatch(b, p, 1024)
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		b.Fatalf("Bind: %v", err)
	}
	predicate := EvidencePredicate{Kind: testEvidenceKindApproval, State: testEvidenceStateValid}
	dst, reasons := makeLeafOutputs(batch.Rows)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		evalEvidence(dst, reasons, batch, p, &states, predicate)
	}
}
