package eval

import (
	"strconv"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

var evaluatorBenchmarkRows = [...]uint32{16, 32, 63, 64, 65, 128, 256, 320, 384, 448, 449, 512, 1024, 4096}

func BenchmarkEvaluateBackends(b *testing.B) {
	for _, rows := range evaluatorBenchmarkRows {
		name := "rows=" + strconv.FormatUint(uint64(rows), 10)
		for _, mode := range [...]executionMode{executionScalar, executionSIMD} {
			b.Run("predicate/"+name+"/"+mode.name(), func(b *testing.B) {
				benchmarkPredicateBackend(b, rows, mode, 2)
			})
			b.Run("boolean/"+name+"/"+mode.name(), func(b *testing.B) {
				benchmarkPredicateBackend(b, rows, mode, 14)
			})
			b.Run("truth-reasons/"+name+"/"+mode.name(), func(b *testing.B) {
				benchmarkTruthBackend(b, rows, mode)
			})
			b.Run("execute/"+name+"/"+mode.name(), func(b *testing.B) {
				benchmarkExecuteBackend(b, rows, mode)
			})
		}
	}
}

func (mode executionMode) name() string {
	switch mode {
	case executionAuto:
		return "auto"
	case executionScalar:
		return "scalar"
	case executionSIMD:
		return "simd"
	case executionIndex:
		return "index"
	default:
		return "invalid"
	}
}

func benchmarkPredicateBackend(b *testing.B, rows uint32, mode executionMode, leaf int) {
	fixture := simdScheduleTestProgram(b)
	batch := simdPredicateBatch(b, fixture.program, rows)
	instruction := fixture.vectorLeaves[leaf]
	dst, reasons := makeLeafOutputs(rows)
	var executor Executor
	if mode == executionScalar {
		evalPredicate(dst, reasons, batch, fixture.program, instruction)
	} else if !executor.evalPredicateSIMD(dst, reasons, batch, fixture.program, instruction, mode) {
		b.Fatal("predicate did not use SIMD")
	}
	b.ReportAllocs()
	b.ReportMetric(float64(rows), "rows")
	b.ResetTimer()
	if mode == executionScalar {
		for range b.N {
			evalPredicate(dst, reasons, batch, fixture.program, instruction)
		}
		return
	}
	for range b.N {
		executor.evalPredicateSIMD(dst, reasons, batch, fixture.program, instruction, mode)
	}
}

func benchmarkTruthBackend(b *testing.B, rows uint32, mode executionMode) {
	left := patternedTruth(rows, false)
	right := patternedTruth(rows, true)
	dst, _ := makeLeafOutputs(rows)
	words := truth.WordCount(rows)
	leftReasons := make([]uint64, truth.ReasonCount*words)
	rightReasons := make([]uint64, truth.ReasonCount*words)
	dstReasons := make([]uint64, truth.ReasonCount*words)
	for i := range leftReasons {
		leftReasons[i] = uint64(i)*0x9e3779b97f4a7c15 ^ 0xaaaaaaaaaaaaaaaa
		rightReasons[i] = uint64(i)*0xbf58476d1ce4e5b9 ^ 0x5555555555555555
	}
	b.ReportAllocs()
	b.ReportMetric(float64(rows), "rows")
	b.ResetTimer()
	if mode == executionScalar {
		for range b.N {
			truth.And(dst, left, right, rows)
			for i := range dstReasons {
				dstReasons[i] = leftReasons[i] | rightReasons[i]
			}
		}
		return
	}
	for range b.N {
		simdTruthAnd(dst, left, right, rows)
		simdReasonOr(dstReasons, leftReasons, rightReasons)
	}
}

func benchmarkExecuteBackend(b *testing.B, rows uint32, mode executionMode) {
	p := executorBenchmarkProgram(b)
	batch := executorBenchmarkBatch(b, p, rows)
	var executor Executor
	var dst result.Batch
	if err := executor.executeMode(&dst, p, batch, mode); err != nil {
		b.Fatalf("prime Execute: %v", err)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(rows), "rows")
	b.ResetTimer()
	for range b.N {
		if err := executor.executeMode(&dst, p, batch, mode); err != nil {
			b.Fatal(err)
		}
	}
}

func factIndexBenchmarkProgram(b testing.TB) *program.Program {
	b.Helper()
	p := indexedExecutionProgram(b)
	p.ListValues = p.ListValues[:0]
	for i := 0; i < 95; i++ {
		p.ListValues = append(p.ListValues, schema.ValueID(2+(i&1)))
	}
	for row, opcode := range p.Opcodes {
		if opcode == program.OpcodeIn {
			p.ListStarts[row] = 0
			p.ListCounts[row] = uint16(len(p.ListValues))
		}
	}
	p.FactIndexSpec = policyindex.FactSpec{
		FieldIDs:    []schema.FieldID{2},
		Columns:     []uint32{1},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{2},
		UseCounts:   []uint32{96},
		Values:      []schema.SymbolID{executionSymbolYes, executionSymbolNo},
	}
	return p
}

func BenchmarkEvaluateFactIndex(b *testing.B) {
	for _, distribution := range []string{"dense", "sparse"} {
		for _, rows := range []uint32{64, 256, 1024, 4096} {
			for _, backend := range []struct {
				name string
				mode executionMode
			}{
				{"direct", executionSIMD},
				{"indexed", executionIndex},
				{"auto", executionAuto},
			} {
				name := distribution + "/rows=" + strconv.FormatUint(uint64(rows), 10) + "/" + backend.name
				b.Run(name, func(b *testing.B) {
					p := factIndexBenchmarkProgram(b)
					batch := indexedExecutionBatch(b, p, rows)
					if distribution == "sparse" {
						column := int(rows)
						for row := uint32(0); row < rows; row++ {
							if batch.Present(2, row) {
								batch.SymbolValues[column+int(row)] = schema.SymbolID(p.ProgramSymbolCount + 1 + row%64)
							}
						}
					}
					var executor Executor
					var dst result.Batch
					if err := executor.executeMode(&dst, p, batch, backend.mode); err != nil {
						b.Fatalf("prime Execute: %v", err)
					}
					b.ReportAllocs()
					b.ReportMetric(float64(rows), "rows")
					b.ResetTimer()
					for range b.N {
						if err := executor.executeMode(&dst, p, batch, backend.mode); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
