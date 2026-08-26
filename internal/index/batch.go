package index

import (
	"math"
	"slices"

	"github.com/sebishogun/nornrune/internal/schema"
)

// FactSpec is immutable compiler-selected metadata for reused symbol fields.
// Values are sorted and unique inside each field's CSR range.
type FactSpec struct {
	FieldIDs    []schema.FieldID
	Columns     []uint32
	ValueStarts []uint32
	ValueCounts []uint32
	UseCounts   []uint32
	Values      []schema.SymbolID
}

// SymbolColumns is a borrowed column-major symbol batch.
type SymbolColumns struct {
	Values             []schema.SymbolID
	Rows               uint32
	Count              uint32
	ProgramSymbolCount uint32
	RowOffset          uint32
	RowStride          uint32
}

// FactIndex stores one fixed-width row mask per queried symbol value.
// The specification is borrowed from an immutable Program.
type FactIndex struct {
	spec       *FactSpec
	ValueMasks []uint64
	Rows       uint32
	WordCount  uint32
}

// FactBuilder owns reusable construction scratch and is not safe for
// concurrent use. A zero value is usable.
type FactBuilder struct {
	valueRows []uint32
}

func factWordCount(rows uint32) uint64 {
	return (uint64(rows) + 63) >> 6
}

func factMaskLen(rows uint32, valueCount int) (int, bool) {
	if valueCount < 0 {
		return 0, false
	}
	words := factWordCount(rows)
	if words != 0 && uint64(valueCount) > math.MaxUint64/words {
		return 0, false
	}
	elements := uint64(valueCount) * words
	if elements > uint64(math.MaxInt)/8 {
		return 0, false
	}
	return int(elements), true
}

func factColumnLen(rows, columns uint32) (int, bool) {
	elements := uint64(rows) * uint64(columns)
	if elements > uint64(math.MaxInt)/4 {
		return 0, false
	}
	return int(elements), true
}

func validFactSpecShape(spec *FactSpec, symbolColumns, programSymbolCount uint32) bool {
	if spec == nil {
		return false
	}
	n := len(spec.FieldIDs)
	if len(spec.Columns) != n || len(spec.ValueStarts) != n || len(spec.ValueCounts) != n ||
		len(spec.UseCounts) != n || uint64(n) > math.MaxUint32 || uint64(len(spec.Values)) > math.MaxUint32 {
		return false
	}
	if n == 0 {
		return len(spec.Values) == 0
	}
	for fieldRow, field := range spec.FieldIDs {
		if field == 0 || spec.Columns[fieldRow] >= symbolColumns || spec.UseCounts[fieldRow] == 0 ||
			(fieldRow != 0 && (field <= spec.FieldIDs[fieldRow-1] || spec.Columns[fieldRow] <= spec.Columns[fieldRow-1])) {
			return false
		}
		start := uint64(spec.ValueStarts[fieldRow])
		count := uint64(spec.ValueCounts[fieldRow])
		if count == 0 || start+count < start || start+count > uint64(len(spec.Values)) ||
			(fieldRow == 0 && start != 0) ||
			(fieldRow != 0 && start != uint64(spec.ValueStarts[fieldRow-1])+uint64(spec.ValueCounts[fieldRow-1])) {
			return false
		}
		values := spec.Values[int(start):int(start+count)]
		for valueRow, value := range values {
			if value == 0 || uint32(value) > programSymbolCount ||
				(valueRow != 0 && value <= values[valueRow-1]) {
				return false
			}
		}
	}
	last := n - 1
	return uint64(spec.ValueStarts[last])+uint64(spec.ValueCounts[last]) == uint64(len(spec.Values))
}

// Valid reports whether the specification matches the compiled field schema
// and program symbol catalog.
func (spec FactSpec) Valid(fields Schema, programSymbolCount uint32) bool {
	if len(fields.Kinds) != len(fields.Columns) ||
		!validFactSpecShape(&spec, fields.Counts[schema.ValueKindSymbol], programSymbolCount) {
		return false
	}
	for row, field := range spec.FieldIDs {
		kind, column, ok := fields.Lookup(field)
		if !ok || kind != schema.ValueKindSymbol || column != spec.Columns[row] {
			return false
		}
	}
	return true
}

// Clone returns an exact-capacity copy suitable for immutable publication.
func (spec FactSpec) Clone() FactSpec {
	return FactSpec{
		FieldIDs:    cloneIndexExact(spec.FieldIDs),
		Columns:     cloneIndexExact(spec.Columns),
		ValueStarts: cloneIndexExact(spec.ValueStarts),
		ValueCounts: cloneIndexExact(spec.ValueCounts),
		UseCounts:   cloneIndexExact(spec.UseCounts),
		Values:      cloneIndexExact(spec.Values),
	}
}

// Reset removes the active binding while retaining bitmap capacity.
func (index *FactIndex) Reset() {
	if index == nil {
		return
	}
	index.spec = nil
	index.ValueMasks = index.ValueMasks[:0]
	index.Rows = 0
	index.WordCount = 0
}

// Build validates all shapes before replacing dst. It scans each selected
// symbol column once and retains capacity for reuse.
func (builder *FactBuilder) Build(dst *FactIndex, spec *FactSpec, columns SymbolColumns) error {
	if builder == nil || dst == nil {
		return ErrInvalidFactIndex
	}
	stride := columns.RowStride
	if stride == 0 {
		if columns.RowOffset != 0 {
			return ErrInvalidFactIndex
		}
		stride = columns.Rows
	}
	if uint64(columns.RowOffset)+uint64(columns.Rows) > uint64(stride) {
		return ErrInvalidFactIndex
	}
	columnLen, ok := factColumnLen(stride, columns.Count)
	if !ok {
		return ErrIndexTooLarge
	}
	if len(columns.Values) != columnLen ||
		!validFactSpecShape(spec, columns.Count, columns.ProgramSymbolCount) {
		return ErrInvalidFactIndex
	}
	maskLen, ok := factMaskLen(columns.Rows, len(spec.Values))
	if !ok || uint64(columns.ProgramSymbolCount)+1 > uint64(math.MaxInt)/4 {
		return ErrIndexTooLarge
	}

	valueRowLen := int(uint64(columns.ProgramSymbolCount) + 1)
	if cap(builder.valueRows) < valueRowLen {
		builder.valueRows = make([]uint32, valueRowLen)
	} else {
		builder.valueRows = builder.valueRows[:valueRowLen]
	}
	dst.ValueMasks = resizeIndex(dst.ValueMasks, maskLen)
	words := int(factWordCount(columns.Rows))
	rows := int(columns.Rows)
	physicalRows := int(stride)
	rowOffset := int(columns.RowOffset)
	for fieldRow, column := range spec.Columns {
		valueStart := int(spec.ValueStarts[fieldRow])
		valueEnd := valueStart + int(spec.ValueCounts[fieldRow])
		for valueRow := valueStart; valueRow < valueEnd; valueRow++ {
			builder.valueRows[spec.Values[valueRow]] = uint32(valueRow + 1)
		}
		columnStart := int(column)*physicalRows + rowOffset
		values := columns.Values[columnStart : columnStart+rows]
		for row, value := range values {
			if uint64(value) >= uint64(len(builder.valueRows)) {
				continue
			}
			valueRow := builder.valueRows[value]
			if valueRow != 0 {
				maskStart := int(valueRow-1) * words
				dst.ValueMasks[maskStart+(row>>6)] |= uint64(1) << (uint(row) & 63)
			}
		}
		for _, value := range spec.Values[valueStart:valueEnd] {
			builder.valueRows[value] = 0
		}
	}
	dst.spec = spec
	dst.Rows = columns.Rows
	dst.WordCount = uint32(words)
	return nil
}

// Lookup returns the row mask for value and whether field is indexed. A
// selected field with an unknown value has an all-zero mask represented by nil.
func (index FactIndex) Lookup(field schema.FieldID, value schema.SymbolID) ([]uint64, bool) {
	if index.spec == nil {
		return nil, false
	}
	fieldRow, found := slices.BinarySearch(index.spec.FieldIDs, field)
	if !found {
		return nil, false
	}
	valueStart := int(index.spec.ValueStarts[fieldRow])
	valueEnd := valueStart + int(index.spec.ValueCounts[fieldRow])
	relative, found := slices.BinarySearch(index.spec.Values[valueStart:valueEnd], value)
	if !found {
		return nil, true
	}
	valueRow := valueStart + relative
	words := int(index.WordCount)
	maskStart := valueRow * words
	return index.ValueMasks[maskStart : maskStart+words : maskStart+words], true
}
