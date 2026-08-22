package eval

import (
	"errors"
	"slices"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func batchTestProgram(t *testing.T, kinds ...schema.ValueKind) *program.Program {
	t.Helper()
	var fields policyindex.Schema
	if err := policyindex.BuildSchema(&fields, kinds); err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	return &program.Program{FieldIndex: fields}
}

func TestBuilderSetTypedColumnsAndPresence(t *testing.T) {
	p := batchTestProgram(t,
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
		schema.ValueKindSymbol,
	)
	var b Builder
	if err := b.Begin(p, 3, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	setters := []struct {
		name string
		set  func() error
	}{
		{"request", func() error { return b.SetRequestID(1, 7) }},
		{"first symbol column", func() error { return b.SetSymbol(2, 1, 11) }},
		{"second symbol column", func() error { return b.SetSymbol(1, 6, 22) }},
		{"integer zero", func() error { return b.SetInteger(0, 2, 0) }},
		{"timestamp", func() error { return b.SetTimestamp(2, 4, 99) }},
		{"boolean true", func() error { return b.SetBoolean(1, 3, true) }},
		{"boolean false", func() error { return b.SetBoolean(2, 3, false) }},
		{"presence only", func() error { return b.SetPresent(0, 5) }},
	}
	for _, tc := range setters {
		if err := tc.set(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}

	if got := b.batch.RequestIDs; !slices.Equal(got, []schema.RequestID{0, 7, 0}) {
		t.Errorf("RequestIDs = %v, want [0 7 0]", got)
	}
	if got := b.batch.SymbolValues; !slices.Equal(got, []schema.SymbolID{0, 0, 11, 0, 22, 0}) {
		t.Errorf("SymbolValues = %v, want [0 0 11 0 22 0]", got)
	}
	if got := b.batch.IntegerValues; !slices.Equal(got, []int64{0, 0, 0}) {
		t.Errorf("IntegerValues = %v, want zeros", got)
	}
	if got := b.batch.TimestampValues; !slices.Equal(got, []int64{0, 0, 99}) {
		t.Errorf("TimestampValues = %v, want [0 0 99]", got)
	}
	if got := b.batch.BooleanValues; !slices.Equal(got, []uint64{1 << 1}) {
		t.Errorf("BooleanValues = %#x, want bit 1", got)
	}
	wantPresence := []uint64{1 << 2, 1 << 0, 1<<1 | 1<<2, 1 << 2, 1 << 0, 1 << 1}
	if got := b.batch.PresenceMasks; !slices.Equal(got, wantPresence) {
		t.Errorf("PresenceMasks = %#x, want %#x", got, wantPresence)
	}
	if !b.batch.Present(6, 1) || !b.batch.Boolean(0, 1) || b.batch.Boolean(0, 2) {
		t.Fatal("typed writes are not visible through Batch helpers")
	}
}

func TestBuilderRejectsInvalidTypedWritesWithoutMutation(t *testing.T) {
	p := batchTestProgram(t,
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	)
	var b Builder
	if err := b.Begin(p, 2, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := b.SetRequestID(0, 1); err != nil {
		t.Fatalf("prime request: %v", err)
	}
	if err := b.SetSymbol(0, 1, 9); err != nil {
		t.Fatalf("prime symbol: %v", err)
	}

	wantRequests := slices.Clone(b.batch.RequestIDs)
	wantSymbols := slices.Clone(b.batch.SymbolValues)
	wantIntegers := slices.Clone(b.batch.IntegerValues)
	wantTimestamps := slices.Clone(b.batch.TimestampValues)
	wantBooleans := slices.Clone(b.batch.BooleanValues)
	wantPresence := slices.Clone(b.batch.PresenceMasks)
	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"zero request ID", func() error { return b.SetRequestID(1, 0) }, ErrInvalidValue},
		{"request row", func() error { return b.SetRequestID(2, 2) }, ErrInvalidRow},
		{"symbol row", func() error { return b.SetSymbol(2, 1, 2) }, ErrInvalidRow},
		{"zero field", func() error { return b.SetSymbol(1, 0, 2) }, ErrInvalidField},
		{"large field", func() error { return b.SetSymbol(1, 99, 2) }, ErrInvalidField},
		{"zero symbol", func() error { return b.SetSymbol(1, 1, 0) }, ErrInvalidValue},
		{"integer kind", func() error { return b.SetInteger(1, 1, 2) }, ErrValueKind},
		{"timestamp kind", func() error { return b.SetTimestamp(1, 2, 2) }, ErrValueKind},
		{"boolean kind", func() error { return b.SetBoolean(1, 2, true) }, ErrValueKind},
		{"presence kind", func() error { return b.SetPresent(1, 1) }, ErrValueKind},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !slices.Equal(b.batch.RequestIDs, wantRequests) ||
				!slices.Equal(b.batch.SymbolValues, wantSymbols) ||
				!slices.Equal(b.batch.IntegerValues, wantIntegers) ||
				!slices.Equal(b.batch.TimestampValues, wantTimestamps) ||
				!slices.Equal(b.batch.BooleanValues, wantBooleans) ||
				!slices.Equal(b.batch.PresenceMasks, wantPresence) {
				t.Fatal("rejected setter mutated the batch")
			}
		})
	}

	b.active = false
	if err := b.SetInteger(1, 2, 3); !errors.Is(err, ErrInvalidBuilder) {
		t.Fatalf("inactive setter error = %v, want %v", err, ErrInvalidBuilder)
	}
	var nilBuilder *Builder
	if err := nilBuilder.SetRequestID(0, 1); !errors.Is(err, ErrInvalidBuilder) {
		t.Fatalf("nil setter error = %v, want %v", err, ErrInvalidBuilder)
	}
}

func TestBuilderBeginSizesColumnMajorBatch(t *testing.T) {
	p := batchTestProgram(t,
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
		schema.ValueKindSymbol,
	)
	var b Builder
	if err := b.Begin(p, 3, 2, 4); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	batch := &b.batch
	if batch.Rows != 3 {
		t.Fatalf("Rows = %d, want 3", batch.Rows)
	}
	lengths := []struct {
		name string
		got  int
		want int
	}{
		{"symbols", len(batch.SymbolValues), 6},
		{"integers", len(batch.IntegerValues), 3},
		{"timestamps", len(batch.TimestampValues), 3},
		{"booleans", len(batch.BooleanValues), 1},
		{"presence", len(batch.PresenceMasks), 6},
		{"request IDs", len(batch.RequestIDs), 3},
		{"evidence offsets", len(batch.EvidenceOffsets), 4},
		{"evidence refs", len(batch.EvidenceRefs), 4},
		{"evidence IDs", len(batch.Evidence.IDs), 2},
		{"evidence kinds", len(batch.Evidence.Kinds), 2},
		{"evidence states", len(batch.Evidence.States), 2},
		{"evidence subjects", len(batch.Evidence.Subjects), 2},
		{"evidence scopes", len(batch.Evidence.Scopes), 2},
		{"evidence reviewers", len(batch.Evidence.Reviewers), 2},
		{"evidence timings", len(batch.Evidence.Timings), 2},
		{"evidence timestamps", len(batch.Evidence.Timestamps), 2},
	}
	for _, tc := range lengths {
		if tc.got != tc.want {
			t.Errorf("%s length = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	if batch.WordCount() != 1 {
		t.Errorf("WordCount = %d, want 1", batch.WordCount())
	}
}

func TestBuilderBeginEmptyBatch(t *testing.T) {
	p := batchTestProgram(t,
		schema.ValueKindSymbol,
		schema.ValueKindBoolean,
	)
	var b Builder
	if err := b.Begin(p, 0, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	batch := &b.batch
	if batch.Rows != 0 || batch.WordCount() != 0 {
		t.Fatalf("empty shape = (%d rows, %d words), want zero", batch.Rows, batch.WordCount())
	}
	if len(batch.SymbolValues) != 0 || len(batch.BooleanValues) != 0 ||
		len(batch.PresenceMasks) != 0 || len(batch.RequestIDs) != 0 {
		t.Fatalf("empty value slabs are not empty: %+v", *batch)
	}
	if len(batch.EvidenceOffsets) != 1 || batch.EvidenceOffsets[0] != 0 {
		t.Fatalf("empty EvidenceOffsets = %v, want [0]", batch.EvidenceOffsets)
	}
	if len(batch.EvidenceRefs) != 0 || batch.Evidence.Len() != 0 {
		t.Fatalf("empty evidence shape = (%d refs, %d rows), want zero", len(batch.EvidenceRefs), batch.Evidence.Len())
	}
}

func TestBatchReadHelpersRejectMalformedRanges(t *testing.T) {
	batch := Batch{
		BooleanValues:   []uint64{1 << 2},
		PresenceMasks:   []uint64{1 << 2},
		EvidenceOffsets: []uint32{0, 1},
		EvidenceRefs:    []uint32{0},
		Rows:            3,
	}
	if !batch.Present(1, 2) || batch.Present(0, 2) || batch.Present(1, 3) || batch.Present(2, 2) {
		t.Fatal("Present did not enforce one-based fields and row bounds")
	}
	if !batch.Boolean(0, 2) || batch.Boolean(1, 2) || batch.Boolean(0, 3) {
		t.Fatal("Boolean did not enforce zero-based columns and row bounds")
	}
	start, end, ok := batch.EvidenceRange(0)
	if !ok || start != 0 || end != 1 {
		t.Fatalf("EvidenceRange(0) = (%d, %d, %v), want (0, 1, true)", start, end, ok)
	}
	batch.EvidenceOffsets[1] = 2
	if _, _, ok := batch.EvidenceRange(0); ok {
		t.Fatal("EvidenceRange accepted an end beyond EvidenceRefs")
	}
}
