// Package eval materializes request and evidence batches into typed,
// column-major storage and executes compiled policy programs over them.
package eval

import "github.com/sebishogun/verifoxx/internal/schema"

// Batch stores request facts and evidence in evaluator-ready struct-of-arrays
// form. A Batch returned by Builder.Finish is valid until that builder's next
// successful Begin call.
type Batch struct {
	SymbolValues    []schema.SymbolID
	IntegerValues   []int64
	TimestampValues []int64
	BooleanValues   []uint64
	PresenceMasks   []uint64
	RequestIDs      []schema.RequestID
	EvidenceOffsets []uint32
	EvidenceRefs    []uint32
	Evidence        EvidenceBatch
	Rows            uint32
	rowBase         uint32
	rowStride       uint32
}

func (b Batch) sourceRows() uint32 {
	if b.rowStride == 0 {
		return b.Rows
	}
	return b.rowStride
}

func (b Batch) sourceWords() uint32 {
	return uint32((uint64(b.sourceRows()) + 63) >> 6)
}

func (b Batch) wordBase() uint32 { return b.rowBase >> 6 }

func (b Batch) validPhysicalRange() bool {
	if b.rowBase&63 != 0 || (b.rowStride == 0 && b.rowBase != 0) {
		return false
	}
	return uint64(b.rowBase)+uint64(b.Rows) <= uint64(b.sourceRows())
}

func batchRowColumn[T any](b Batch, values []T, column uint32) []T {
	start := uint64(column)*uint64(b.sourceRows()) + uint64(b.rowBase)
	end := start + uint64(b.Rows)
	if end < start || end > uint64(len(values)) {
		panic("eval: invalid batch row column")
	}
	return values[int(start):int(end):int(end)]
}

func batchWordColumn(b Batch, values []uint64, column uint32) []uint64 {
	start := uint64(column)*uint64(b.sourceWords()) + uint64(b.wordBase())
	end := start + uint64(b.WordCount())
	if end < start || end > uint64(len(values)) {
		panic("eval: invalid batch word column")
	}
	return values[int(start):int(end):int(end)]
}

// WordCount returns the number of uint64 words in one row bitmap plane.
func (b Batch) WordCount() uint32 {
	return uint32((uint64(b.Rows) + 63) >> 6)
}

// Present reports whether one-based field is present for row. Malformed and
// out-of-range inputs return false rather than panicking.
func (b Batch) Present(field schema.FieldID, row uint32) bool {
	if field == 0 || row >= b.Rows || !b.validPhysicalRange() {
		return false
	}
	word := uint64(field-1)*uint64(b.sourceWords()) + uint64(b.wordBase()) + uint64(row>>6)
	if word >= uint64(len(b.PresenceMasks)) {
		return false
	}
	return b.PresenceMasks[int(word)]&(uint64(1)<<(row&63)) != 0
}

// Boolean reports the Boolean value in zero-based kind-local column for row.
// Malformed and out-of-range inputs return false rather than panicking.
func (b Batch) Boolean(column, row uint32) bool {
	if row >= b.Rows || !b.validPhysicalRange() {
		return false
	}
	word := uint64(column)*uint64(b.sourceWords()) + uint64(b.wordBase()) + uint64(row>>6)
	if word >= uint64(len(b.BooleanValues)) {
		return false
	}
	return b.BooleanValues[int(word)]&(uint64(1)<<(row&63)) != 0
}

// EvidenceRange returns the half-open range in EvidenceRefs for row.
func (b Batch) EvidenceRange(row uint32) (start, end uint32, ok bool) {
	if row >= b.Rows || uint64(row)+1 >= uint64(len(b.EvidenceOffsets)) {
		return 0, 0, false
	}
	start = b.EvidenceOffsets[row]
	end = b.EvidenceOffsets[row+1]
	if start > end || uint64(end) > uint64(len(b.EvidenceRefs)) {
		return 0, 0, false
	}
	return start, end, true
}
