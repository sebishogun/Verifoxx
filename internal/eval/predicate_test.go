package eval

import (
	"slices"
	"strconv"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("call did not panic")
		}
	}()
	fn()
}

func TestReasonPlanesSelectsDenseReasonRanges(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65} {
		t.Run(strconv.FormatUint(uint64(rows), 10), func(t *testing.T) {
			words := truth.WordCount(rows)
			storage := make([]uint64, truth.ReasonCount*words)
			planes := ReasonPlanes{Words: storage}
			for reason := schema.ReasonID(1); reason <= truth.ReasonConflict; reason++ {
				plane := planes.Plane(reason, rows)
				if len(plane) != words || cap(plane) != words {
					t.Fatalf("reason %d shape = len %d cap %d, want %d", reason, len(plane), cap(plane), words)
				}
				if words != 0 {
					plane[0] = uint64(reason)
				}
			}
			for reason := schema.ReasonID(1); words != 0 && reason <= truth.ReasonConflict; reason++ {
				if got := storage[int(reason-1)*words]; got != uint64(reason) {
					t.Fatalf("reason %d starts with %d", reason, got)
				}
			}
		})
	}
}

func TestReasonPlanesRejectsInvalidShapes(t *testing.T) {
	rows := uint32(65)
	words := truth.WordCount(rows)
	valid := ReasonPlanes{Words: make([]uint64, truth.ReasonCount*words)}
	requirePanic(t, func() { valid.Plane(0, rows) })
	requirePanic(t, func() { valid.Plane(truth.ReasonConflict+1, rows) })
	requirePanic(t, func() {
		ReasonPlanes{Words: make([]uint64, truth.ReasonCount*words-1)}.Plane(truth.ReasonMissing, rows)
	})
}

func TestResetLeafOutputsClearsPoisonedStorage(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65} {
		words := truth.WordCount(rows)
		positive := slices.Repeat([]uint64{^uint64(0)}, words)
		negative := slices.Repeat([]uint64{^uint64(0)}, words)
		reasonWords := slices.Repeat([]uint64{^uint64(0)}, truth.ReasonCount*words)
		gotWords := resetLeafOutputs(
			truth.Planes{Positive: positive, Negative: negative},
			ReasonPlanes{Words: reasonWords},
			rows,
		)
		if gotWords != words {
			t.Fatalf("reset words = %d, want %d", gotWords, words)
		}
		for _, storage := range [][]uint64{positive, negative, reasonWords} {
			if slices.ContainsFunc(storage, func(word uint64) bool { return word != 0 }) {
				t.Fatalf("reset left dirty words: %#x", storage)
			}
		}
	}
}

func TestResetLeafOutputsValidatesBeforeMutation(t *testing.T) {
	positive := []uint64{1}
	negative := []uint64{2}
	reasonWords := slices.Repeat([]uint64{3}, truth.ReasonCount)
	requirePanic(t, func() {
		resetLeafOutputs(
			truth.Planes{Positive: positive, Negative: negative[:0]},
			ReasonPlanes{Words: reasonWords},
			1,
		)
	})
	if positive[0] != 1 || negative[0] != 2 || !slices.Equal(reasonWords, slices.Repeat([]uint64{3}, truth.ReasonCount)) {
		t.Fatal("shape failure mutated output")
	}
}

func predicateTestProgram(t testing.TB) *program.Program {
	t.Helper()
	p := batchTestProgramWithSymbol(t, "equal",
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	)
	p.ValueKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
	}
	p.ValueRefs = []uint32{1, 1, 1, 1}
	p.IntegerValues = []int64{7}
	p.BooleanValues = []uint64{1}
	p.TimestampValues = []int64{99}
	return p
}

func appendPredicateInstruction(p *program.Program, opcode program.Opcode, field schema.FieldID, value schema.ValueID) schema.InstructionID {
	p.Opcodes = append(p.Opcodes, opcode)
	p.Fields = append(p.Fields, field)
	p.Values = append(p.Values, value)
	p.ListStarts = append(p.ListStarts, 0)
	p.ListCounts = append(p.ListCounts, 0)
	return schema.InstructionID(len(p.Opcodes))
}

func predicateTestBatch(t testing.TB, p *program.Program) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, 3, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < 3; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
	}
	other, err := builder.InternSymbol([]byte("other"))
	if err != nil {
		t.Fatalf("InternSymbol: %v", err)
	}
	setters := []func() error{
		func() error { return builder.SetSymbol(0, 1, 1) },
		func() error { return builder.SetSymbol(1, 1, other) },
		func() error { return builder.SetInteger(0, 2, 7) },
		func() error { return builder.SetInteger(1, 2, 8) },
		func() error { return builder.SetBoolean(0, 3, true) },
		func() error { return builder.SetBoolean(1, 3, false) },
		func() error { return builder.SetTimestamp(0, 4, 99) },
		func() error { return builder.SetTimestamp(1, 4, 100) },
		func() error { return builder.SetPresent(0, 5) },
		func() error { return builder.SetPresent(1, 5) },
	}
	for i, set := range setters {
		if err := set(); err != nil {
			t.Fatalf("setter %d: %v", i, err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func makeLeafOutputs(rows uint32) (truth.Planes, ReasonPlanes) {
	words := truth.WordCount(rows)
	return truth.Planes{
		Positive: make([]uint64, words),
		Negative: make([]uint64, words),
	}, ReasonPlanes{Words: make([]uint64, truth.ReasonCount*words)}
}

func assertLeafWord(t testing.TB, dst truth.Planes, reasons ReasonPlanes, rows uint32, positive, negative, missing uint64) {
	t.Helper()
	if len(dst.Positive) != 1 || dst.Positive[0] != positive || dst.Negative[0] != negative {
		t.Fatalf("truth = (%#x,%#x), want (%#x,%#x)", dst.Positive, dst.Negative, positive, negative)
	}
	if got := reasons.Plane(truth.ReasonMissing, rows)[0]; got != missing {
		t.Fatalf("missing = %#x, want %#x", got, missing)
	}
	for reason := truth.ReasonStale; reason <= truth.ReasonConflict; reason++ {
		if got := reasons.Plane(reason, rows)[0]; got != 0 {
			t.Fatalf("reason %d = %#x, want zero", reason, got)
		}
	}
}

func TestEvalPredicateEqual(t *testing.T) {
	p := predicateTestProgram(t)
	batch := predicateTestBatch(t, p)
	tests := []struct {
		name  string
		field schema.FieldID
		value schema.ValueID
	}{
		{"symbol", 1, 1},
		{"integer", 2, 2},
		{"Boolean", 3, 3},
		{"timestamp", 4, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instruction := appendPredicateInstruction(p, program.OpcodeEqual, tc.field, tc.value)
			dst, reasons := makeLeafOutputs(batch.Rows)
			evalPredicate(dst, reasons, batch, p, instruction)
			assertLeafWord(t, dst, reasons, batch.Rows, 1<<0, 1<<1, 1<<2)
		})
	}
}

func TestEvalPredicateExists(t *testing.T) {
	p := predicateTestProgram(t)
	batch := predicateTestBatch(t, p)
	for field := schema.FieldID(1); field <= 5; field++ {
		instruction := appendPredicateInstruction(p, program.OpcodeExists, field, 0)
		dst, reasons := makeLeafOutputs(batch.Rows)
		evalPredicate(dst, reasons, batch, p, instruction)
		assertLeafWord(t, dst, reasons, batch.Rows, 1<<0|1<<1, 0, 1<<2)
	}
}

func TestEvalPredicateAcceptsEmptyBatch(t *testing.T) {
	p := predicateTestProgram(t)
	instruction := appendPredicateInstruction(p, program.OpcodeEqual, 2, 2)
	var builder Builder
	if err := builder.Begin(p, 0, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	dst, reasons := makeLeafOutputs(0)
	evalPredicate(dst, reasons, batch, p, instruction)
}
