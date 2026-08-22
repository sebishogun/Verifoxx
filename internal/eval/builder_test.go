package eval

import (
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
