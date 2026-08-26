package eval

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"unsafe"

	policyindex "github.com/sebishogun/nornrune/internal/index"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func executionRangeBatch(t testing.TB, p *program.Program, first, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 3, rows); err != nil {
		t.Fatalf("Begin(%d): %v", rows, err)
	}
	for row := uint32(0); row < rows; row++ {
		global := first + row
		if err := builder.SetRequestID(row, schema.RequestID(global+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		switch global % 7 {
		case 0:
			if err := builder.SetSymbol(row, 1, executionSymbolInactive); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(row, 2, executionSymbolYes); err != nil {
				t.Fatal(err)
			}
		case 1:
			if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(row, 2, executionSymbolYes); err != nil {
				t.Fatal(err)
			}
		case 2:
			if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(row, 2, executionSymbolNo); err != nil {
				t.Fatal(err)
			}
		case 3:
			if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
				t.Fatal(err)
			}
		case 4, 5:
			if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(row, 2, executionSymbolYes); err != nil {
				t.Fatal(err)
			}
		case 6:
			if err := builder.SetSymbol(row, 2, executionSymbolNo); err != nil {
				t.Fatal(err)
			}
		}
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
		offsets[row+1] = row + 1
		refs[row] = (first + row) % 7
		switch refs[row] {
		case 4:
			refs[row] = 1
		case 5:
			refs[row] = 2
		default:
			refs[row] = 0
		}
	}
	if err := builder.SetEvidenceCSR(offsets, refs); err != nil {
		t.Fatalf("SetEvidenceCSR: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func cloneRangeSource(batch Batch) Batch {
	clone := batch
	clone.SymbolValues = slices.Clone(batch.SymbolValues)
	clone.IntegerValues = slices.Clone(batch.IntegerValues)
	clone.TimestampValues = slices.Clone(batch.TimestampValues)
	clone.BooleanValues = slices.Clone(batch.BooleanValues)
	clone.PresenceMasks = slices.Clone(batch.PresenceMasks)
	clone.RequestIDs = slices.Clone(batch.RequestIDs)
	clone.EvidenceOffsets = slices.Clone(batch.EvidenceOffsets)
	clone.EvidenceRefs = slices.Clone(batch.EvidenceRefs)
	clone.Evidence.IDs = slices.Clone(batch.Evidence.IDs)
	clone.Evidence.Kinds = slices.Clone(batch.Evidence.Kinds)
	clone.Evidence.States = slices.Clone(batch.Evidence.States)
	clone.Evidence.Subjects = slices.Clone(batch.Evidence.Subjects)
	clone.Evidence.Scopes = slices.Clone(batch.Evidence.Scopes)
	clone.Evidence.Reviewers = slices.Clone(batch.Evidence.Reviewers)
	clone.Evidence.Timings = slices.Clone(batch.Evidence.Timings)
	clone.Evidence.Timestamps = slices.Clone(batch.Evidence.Timestamps)
	return clone
}

func TestExecuteRange(t *testing.T) {
	p := executionTestProgram(t, 1)
	p.FactIndexSpec = policyindex.FactSpec{
		FieldIDs:    []schema.FieldID{1},
		Columns:     []uint32{0},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{2},
		UseCounts:   []uint32{100},
		Values:      []schema.SymbolID{executionSymbolActive, executionSymbolInactive},
	}
	source := executionRangeBatch(t, p, 0, 129)
	wantSource := cloneRangeSource(source)
	wantPointers := rangeSourcePointers(source)

	for _, bounds := range [][2]uint32{{0, 0}, {0, 64}, {64, 128}, {128, 129}} {
		start, end := bounds[0], bounds[1]
		t.Run(fmt.Sprintf("rows=%d-%d", start, end), func(t *testing.T) {
			compact := executionRangeBatch(t, p, start, end-start)
			var wantExecutor Executor
			var want result.Batch
			if err := wantExecutor.executeMode(&want, p, compact, executionScalar); err != nil {
				t.Fatalf("compact Execute(%d,%d): %v", start, end, err)
			}

			for _, backend := range []struct {
				name string
				mode executionMode
			}{
				{"scalar", executionScalar},
				{"simd", executionSIMD},
				{"index", executionIndex},
				{"auto", executionAuto},
			} {
				t.Run(backend.name, func(t *testing.T) {
					scratch := make([]uint32, end-start+1)
					var gotExecutor Executor
					var got result.Batch
					if err := gotExecutor.executeRangeMode(&got, p, source, start, end, scratch, backend.mode); err != nil {
						t.Fatalf("ExecuteRange(%d,%d): %v", start, end, err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("ExecuteRange(%d,%d) result differs\ngot:  %+v\nwant: %+v", start, end, got, want)
					}
					for row, offset := range scratch {
						if offset != uint32(row) {
							t.Fatalf("ExecuteRange(%d,%d) scratch[%d] = %d, want %d", start, end, row, offset, row)
						}
					}
				})
			}
		})
	}
	if !reflect.DeepEqual(source, wantSource) {
		t.Fatal("range execution mutated source batch")
	}
	if got := rangeSourcePointers(source); !slices.Equal(got, wantPointers) {
		t.Fatal("range execution changed source backing storage")
	}

	t.Run("warm execution allocates zero", func(t *testing.T) {
		scratch := make([]uint32, 65)
		var executor Executor
		var dst result.Batch
		if err := executor.ExecuteRange(&dst, p, source, 64, 128, scratch); err != nil {
			t.Fatalf("warmup ExecuteRange: %v", err)
		}
		var executeErr error
		if allocs := testing.AllocsPerRun(100, func() {
			executeErr = executor.ExecuteRange(&dst, p, source, 64, 128, scratch)
		}); allocs != 0 {
			t.Fatalf("warm ExecuteRange allocations = %g, want 0", allocs)
		}
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	})

	poison := result.Batch{Rows: 7, OutcomeIDs: []schema.OutcomeID{4}}
	invalid := []struct {
		name       string
		executor   *Executor
		dst        *result.Batch
		program    *program.Program
		batch      Batch
		start, end uint32
		scratch    []uint32
	}{
		{"nil executor", nil, &poison, p, source, 0, 64, make([]uint32, 65)},
		{"nil destination", &Executor{}, nil, p, source, 0, 64, make([]uint32, 65)},
		{"nil program", &Executor{}, &poison, nil, source, 0, 64, make([]uint32, 65)},
		{"reversed", &Executor{}, &poison, p, source, 128, 64, make([]uint32, 1)},
		{"end past source", &Executor{}, &poison, p, source, 128, 130, make([]uint32, 3)},
		{"unaligned start", &Executor{}, &poison, p, source, 1, 64, make([]uint32, 64)},
		{"unaligned non-tail end", &Executor{}, &poison, p, source, 0, 63, make([]uint32, 64)},
		{"short scratch", &Executor{}, &poison, p, source, 64, 128, make([]uint32, 64)},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			before := poison
			if err := tc.executor.executeRangeMode(tc.dst, tc.program, tc.batch, tc.start, tc.end, tc.scratch, executionScalar); err == nil {
				t.Fatal("executeRangeMode error = nil")
			}
			if tc.dst != nil && !reflect.DeepEqual(*tc.dst, before) {
				t.Fatalf("failed range changed destination: got %+v want %+v", *tc.dst, before)
			}
		})
	}

	malformed := source
	malformed.EvidenceOffsets = slices.Clone(source.EvidenceOffsets)
	malformed.EvidenceOffsets[65] = malformed.EvidenceOffsets[64] - 1
	before := poison
	if err := new(Executor).executeRangeMode(&poison, p, malformed, 64, 128, make([]uint32, 65), executionScalar); err == nil {
		t.Fatal("executeRangeMode malformed evidence error = nil")
	}
	if !reflect.DeepEqual(poison, before) {
		t.Fatalf("malformed range changed destination: got %+v want %+v", poison, before)
	}
}

func rangeSourcePointers(batch Batch) []unsafe.Pointer {
	return []unsafe.Pointer{
		unsafe.Pointer(unsafe.SliceData(batch.SymbolValues)),
		unsafe.Pointer(unsafe.SliceData(batch.IntegerValues)),
		unsafe.Pointer(unsafe.SliceData(batch.TimestampValues)),
		unsafe.Pointer(unsafe.SliceData(batch.BooleanValues)),
		unsafe.Pointer(unsafe.SliceData(batch.PresenceMasks)),
		unsafe.Pointer(unsafe.SliceData(batch.RequestIDs)),
		unsafe.Pointer(unsafe.SliceData(batch.EvidenceOffsets)),
		unsafe.Pointer(unsafe.SliceData(batch.EvidenceRefs)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.IDs)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Kinds)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.States)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Subjects)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Scopes)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Reviewers)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Timings)),
		unsafe.Pointer(unsafe.SliceData(batch.Evidence.Timestamps)),
	}
}
