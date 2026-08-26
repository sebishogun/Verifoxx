package eval

import (
	"reflect"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func TestRetainedExecutorStepsAndMatchesScalar(t *testing.T) {
	t.Parallel()

	p := executionTestProgram(t, 1)
	batch := executionPathBatch(t, p)
	var scalar Executor
	var want result.Batch
	if err := scalar.executeMode(&want, p, batch, executionScalar); err != nil {
		t.Fatalf("scalar Execute() error = %v", err)
	}
	sourceTruthSlots := slices.Clone(p.TruthSlots)
	sourceReasonSlots := slices.Clone(p.ReasonSlots)
	sourceTruthSlotCount := p.TruthSlotCount
	sourceReasonSlotCount := p.ReasonSlotCount

	var retained RetainedExecutor
	if err := retained.Begin(p, batch); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if retained.Cursor() != 0 || retained.InstructionCount() != len(p.Opcodes) || retained.Complete() {
		t.Fatalf("initial retained state = cursor:%d instructions:%d complete:%v",
			retained.Cursor(), retained.InstructionCount(), retained.Complete())
	}
	words := truth.WordCount(batch.Rows)
	positive := make([]uint64, words)
	negative := make([]uint64, words)
	reasons := make([]uint64, truth.ReasonCount*words)
	var firstPositive []uint64
	for index := range p.Opcodes {
		instruction, done, err := retained.Step()
		if err != nil {
			t.Fatalf("Step(%d) error = %v", index, err)
		}
		if instruction != schema.InstructionID(index+1) || done != (index+1 == len(p.Opcodes)) {
			t.Fatalf("Step(%d) = (%d, %v), want (%d, %v)",
				index, instruction, done, index+1, index+1 == len(p.Opcodes))
		}
		if err := retained.CopyInstruction(instruction, positive, negative, reasons); err != nil {
			t.Fatalf("CopyInstruction(%d) error = %v", instruction, err)
		}
		if instruction == 1 {
			firstPositive = append(firstPositive, positive...)
		}
	}
	got, ok := retained.Result()
	if !ok || !reflect.DeepEqual(*got, want) {
		t.Fatalf("retained result differs\ngot:  %+v\nwant: %+v", got, want)
	}
	if err := retained.CopyInstruction(1, positive, negative, reasons); err != nil {
		t.Fatalf("CopyInstruction(1) after completion error = %v", err)
	}
	if !slices.Equal(positive, firstPositive) {
		t.Fatalf("instruction 1 state was overwritten: got %#x want %#x", positive, firstPositive)
	}
	if !slices.Equal(p.TruthSlots, sourceTruthSlots) || !slices.Equal(p.ReasonSlots, sourceReasonSlots) ||
		p.TruthSlotCount != sourceTruthSlotCount || p.ReasonSlotCount != sourceReasonSlotCount {
		t.Fatal("retained execution mutated the immutable source program")
	}
}

func TestRetainedExecutorRestartAndReplay(t *testing.T) {
	t.Parallel()

	p := executionTestProgram(t, 1)
	batch := executionPathBatch(t, p)
	var retained RetainedExecutor
	if err := retained.Begin(p, batch); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	for !retained.Complete() {
		if _, _, err := retained.Step(); err != nil {
			t.Fatalf("initial Step() error = %v", err)
		}
	}
	want, ok := retained.Result()
	if !ok {
		t.Fatal("Result() is not complete")
	}
	wantCopy := cloneResultBatch(*want)
	if err := retained.Rewind(uint32(retained.InstructionCount())); err != nil {
		t.Fatalf("Rewind(end) error = %v", err)
	}
	got, ok := retained.Result()
	if !ok || !reflect.DeepEqual(*got, wantCopy) {
		t.Fatalf("end-boundary result differs\ngot:  %+v\nwant: %+v", got, wantCopy)
	}

	if err := retained.Rewind(1); err != nil {
		t.Fatalf("Rewind(1) error = %v", err)
	}
	for !retained.Complete() {
		if _, _, err := retained.Step(); err != nil {
			t.Fatalf("replay Step() error = %v", err)
		}
	}
	got, ok = retained.Result()
	if !ok || !reflect.DeepEqual(*got, wantCopy) {
		t.Fatalf("replayed result differs\ngot:  %+v\nwant: %+v", got, wantCopy)
	}

	if err := retained.Restart(); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if retained.Cursor() != 0 || retained.Complete() {
		t.Fatalf("restarted state = cursor:%d complete:%v", retained.Cursor(), retained.Complete())
	}
	for !retained.Complete() {
		if _, _, err := retained.Step(); err != nil {
			t.Fatalf("restart Step() error = %v", err)
		}
	}
	got, ok = retained.Result()
	if !ok || !reflect.DeepEqual(*got, wantCopy) {
		t.Fatalf("restarted result differs\ngot:  %+v\nwant: %+v", got, wantCopy)
	}
}

func TestRetainedExecutorMatchesScalarAndSIMD(t *testing.T) {
	t.Parallel()

	p := executorBenchmarkProgram(t)
	batch := executorBenchmarkBatch(t, p, 1024)
	var retained RetainedExecutor
	if err := retained.Begin(p, batch); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	for !retained.Complete() {
		if _, _, err := retained.Step(); err != nil {
			t.Fatalf("Step() error = %v", err)
		}
	}
	retainedResult, ok := retained.Result()
	if !ok {
		t.Fatal("Result() is not complete")
	}

	for _, test := range []struct {
		name string
		mode executionMode
	}{
		{name: "scalar", mode: executionScalar},
		{name: "simd", mode: executionSIMD},
	} {
		t.Run(test.name, func(t *testing.T) {
			var executor Executor
			var got result.Batch
			if err := executor.executeMode(&got, p, batch, test.mode); err != nil {
				t.Fatalf("executeMode() error = %v", err)
			}
			if !reflect.DeepEqual(got, *retainedResult) {
				t.Fatalf("result differs from retained execution\ngot:  %+v\nwant: %+v", got, *retainedResult)
			}
			for row, flags := range p.RootFlags {
				if flags == 0 {
					continue
				}
				instruction := schema.InstructionID(row + 1)
				for requestRow := uint32(0); requestRow < batch.Rows; requestRow++ {
					wantPositive, wantNegative, ready := retained.InstructionTruth(instruction, requestRow)
					if !ready {
						t.Fatalf("retained instruction %d is not ready", instruction)
					}
					gotPositive, gotNegative := executor.instructionTruth(p, instruction, requestRow, batch.Rows)
					if gotPositive != wantPositive || gotNegative != wantNegative {
						t.Fatalf("instruction %d row %d truth = (%v,%v), want (%v,%v)",
							instruction, requestRow, gotPositive, gotNegative, wantPositive, wantNegative)
					}
					gotReasons := executor.instructionReasons(p, instruction, requestRow, batch.Rows)
					wantReasons, _ := retained.InstructionReasons(instruction, requestRow)
					if gotReasons != wantReasons {
						t.Fatalf("instruction %d row %d reasons = %#x, want %#x",
							instruction, requestRow, gotReasons, wantReasons)
					}
				}
			}
		})
	}
}

func TestRetainedExecutorRejectsInvalidOperations(t *testing.T) {
	t.Parallel()

	var retained RetainedExecutor
	if err := retained.Begin(nil, Batch{}); err == nil {
		t.Fatal("Begin(nil) error = nil")
	}
	if _, _, err := retained.Step(); err == nil {
		t.Fatal("Step() before Begin error = nil")
	}
	p := executionTestProgram(t, 1)
	batch := executionPathBatch(t, p)
	if err := retained.Begin(p, batch); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	words := truth.WordCount(batch.Rows)
	if err := retained.CopyInstruction(1, make([]uint64, words), make([]uint64, words),
		make([]uint64, truth.ReasonCount*words)); err == nil {
		t.Fatal("CopyInstruction() before execution error = nil")
	}
	if err := retained.Rewind(1); err == nil {
		t.Fatal("Rewind() beyond cursor error = nil")
	}
}

func BenchmarkRetainedExecutorRun(b *testing.B) {
	p := executionTestProgram(b, 1)
	batch := executionPathBatch(b, p)
	var retained RetainedExecutor
	if err := retained.Begin(p, batch); err != nil {
		b.Fatalf("Begin() error = %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for !retained.Complete() {
			if _, _, err := retained.Step(); err != nil {
				b.Fatalf("Step() error = %v", err)
			}
		}
		if err := retained.Restart(); err != nil {
			b.Fatalf("Restart() error = %v", err)
		}
	}
}
