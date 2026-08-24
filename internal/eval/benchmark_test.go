package eval

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/benchdata"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
)

type controlledBenchmarkShape struct {
	rows            uint32
	nodes           uint32
	evidencePercent uint32
	matchPercent    uint32
	seed            uint64
}

type controlledBenchmarkFixture struct {
	program *program.Program
	dataset benchdata.Dataset
	batch   Batch
}

func TestControlledBenchmarkFixture(t *testing.T) {
	shape := controlledBenchmarkShape{
		rows: 128, nodes: 96, evidencePercent: 25, matchPercent: 60, seed: 7,
	}
	fixture := newControlledBenchmarkFixture(t, shape)
	if fixture.batch.Rows != shape.rows || len(fixture.program.Opcodes) != int(shape.nodes) ||
		fixture.dataset.MatchRows != 76 || fixture.dataset.EvidenceRows != 32 ||
		len(fixture.batch.EvidenceRefs) != 32 {
		t.Fatalf("fixture = rows %d nodes %d match %d evidence %d refs %d",
			fixture.batch.Rows, len(fixture.program.Opcodes), fixture.dataset.MatchRows,
			fixture.dataset.EvidenceRows, len(fixture.batch.EvidenceRefs))
	}

	var scalarExecutor, simdExecutor, indexExecutor Executor
	var scalarResult, simdResult, indexResult result.Batch
	if err := scalarExecutor.executeMode(&scalarResult, fixture.program, fixture.batch, executionScalar); err != nil {
		t.Fatalf("scalar execute: %v", err)
	}
	if err := simdExecutor.executeMode(&simdResult, fixture.program, fixture.batch, executionSIMD); err != nil {
		t.Fatalf("SIMD execute: %v", err)
	}
	if err := indexExecutor.executeMode(&indexResult, fixture.program, fixture.batch, executionIndex); err != nil {
		t.Fatalf("index execute: %v", err)
	}
	if !reflect.DeepEqual(simdResult, scalarResult) || !reflect.DeepEqual(indexResult, scalarResult) {
		t.Fatal("forced execution modes produced different results")
	}
}

func newControlledBenchmarkFixture(t testing.TB, shape controlledBenchmarkShape) controlledBenchmarkFixture {
	t.Helper()
	p := indexedExecutionProgram(t)
	baseNodes := uint32(len(p.Opcodes))
	if shape.nodes <= baseNodes {
		t.Fatalf("controlled node count %d must exceed base count %d", shape.nodes, baseNodes)
	}
	dataset, err := benchdata.Generate(benchdata.Config{
		Rows: shape.rows, PolicyNodes: shape.nodes - baseNodes,
		EvidencePercent: shape.evidencePercent, MatchPercent: shape.matchPercent, Seed: shape.seed,
		TargetSymbol: executionSymbolYes, OtherSymbol: executionSymbolNo,
		TargetValue: 2, OtherValue: 3, EvidenceState: 1,
	})
	if err != nil {
		t.Fatalf("Generate benchmark data: %v", err)
	}
	for _, value := range dataset.PolicyValues {
		appendExecutorInstruction(p, program.OpcodeEqual, 2, value, nil, 0, 0)
		p.TruthSlots = append(p.TruthSlots, 7)
		p.ReasonSlots = append(p.ReasonSlots, 7)
	}
	p.TruthSlotCount = 7
	p.ReasonSlotCount = 7
	p.FactIndexSpec.UseCounts[0] = shape.nodes

	var builder Builder
	if err := builder.Begin(p, shape.rows, dataset.EvidenceRows, dataset.EvidenceRows); err != nil {
		t.Fatalf("Begin benchmark batch: %v", err)
	}
	for row := range dataset.EvidenceIDs {
		if err := builder.SetEvidence(uint32(row), EvidenceRecord{
			ID: dataset.EvidenceIDs[row], Kind: 1, State: dataset.EvidenceStates[row],
		}); err != nil {
			t.Fatalf("SetEvidence(%d): %v", row, err)
		}
	}
	for row := uint32(0); row < shape.rows; row++ {
		if err := builder.SetRequestID(row, dataset.RequestIDs[row]); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
			t.Fatalf("SetSymbol(active, %d): %v", row, err)
		}
		if err := builder.SetSymbol(row, 2, dataset.RequestValues[row]); err != nil {
			t.Fatalf("SetSymbol(value, %d): %v", row, err)
		}
	}
	if err := builder.SetEvidenceCSR(dataset.EvidenceOffsets, dataset.EvidenceRefs); err != nil {
		t.Fatalf("SetEvidenceCSR: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish benchmark batch: %v", err)
	}
	return controlledBenchmarkFixture{program: p, dataset: dataset, batch: batch}
}

func BenchmarkEvaluate(b *testing.B) {
	shapes := [...]controlledBenchmarkShape{
		{rows: 64, nodes: 96, evidencePercent: 0, matchPercent: 25, seed: 1},
		{rows: 1024, nodes: 96, evidencePercent: 25, matchPercent: 50, seed: 2},
		{rows: 4096, nodes: 256, evidencePercent: 75, matchPercent: 90, seed: 3},
	}
	modes := [...]executionMode{executionScalar, executionSIMD, executionIndex}
	tier := evaluatorSIMD.Tier
	if evaluatorSIMD.PureGo {
		tier += "-purego"
	}
	for _, shape := range shapes {
		fixture := newControlledBenchmarkFixture(b, shape)
		var referenceExecutor Executor
		var reference result.Batch
		if err := referenceExecutor.executeMode(&reference, fixture.program, fixture.batch, executionScalar); err != nil {
			b.Fatalf("prime scalar reference: %v", err)
		}
		for _, mode := range modes {
			name := fmt.Sprintf("tier=%s/rows=%d/nodes=%d/evidence=%d%%/match=%d%%/workers=1/mode=%s",
				tier, shape.rows, shape.nodes, shape.evidencePercent, shape.matchPercent, controlledModeName(mode))
			b.Run(name, func(b *testing.B) {
				benchmarkControlledMode(b, &fixture, &reference, mode, shape)
			})
		}
	}
}

func benchmarkControlledMode(
	b *testing.B,
	fixture *controlledBenchmarkFixture,
	reference *result.Batch,
	mode executionMode,
	shape controlledBenchmarkShape,
) {
	b.Helper()
	var executor Executor
	var destination result.Batch
	if err := executor.executeMode(&destination, fixture.program, fixture.batch, mode); err != nil {
		b.Fatalf("prime %s: %v", mode.name(), err)
	}
	if !reflect.DeepEqual(destination, *reference) {
		b.Fatalf("%s result differs from scalar", mode.name())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := executor.executeMode(&destination, fixture.program, fixture.batch, mode); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(shape.rows), "rows")
	b.ReportMetric(float64(shape.nodes), "nodes")
	b.ReportMetric(float64(shape.evidencePercent), "evidence_pct")
	b.ReportMetric(float64(shape.matchPercent), "match_pct")
	b.ReportMetric(1, "workers")
}

func controlledModeName(mode executionMode) string {
	switch mode {
	case executionScalar:
		return "scalar"
	case executionSIMD:
		return "simd"
	case executionIndex:
		return "index"
	default:
		return "unknown"
	}
}
