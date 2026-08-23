// Package index defines immutable field and applicability indexes used by
// compiled policy programs. Published indexes contain only pointerless typed
// columns and fixed-width bitmap slabs.
package index

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	ErrInvalidSchema    = errors.New("index: invalid field schema")
	ErrInvalidPolicy    = errors.New("index: invalid applicability constraints")
	ErrInvalidQuery     = errors.New("index: invalid candidate query")
	ErrInvalidFactIndex = errors.New("index: invalid fact bitmap index")
	ErrIndexTooLarge    = errors.New("index: fixed-width limit exceeded")
)

// Schema maps each one-based FieldID to its value kind and zero-based column
// within that kind's batch storage.
type Schema struct {
	Kinds   []schema.ValueKind
	Columns []uint32
	Counts  [6]uint32
}

func resizeIndex[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}

func cloneIndexExact[T any](src []T) []T {
	if len(src) == 0 {
		return nil
	}
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}

// BuildSchema validates kinds before replacing dst, then assigns stable
// kind-local columns in FieldID order. Existing capacity is reused.
func BuildSchema(dst *Schema, kinds []schema.ValueKind) error {
	if dst == nil {
		return ErrInvalidSchema
	}
	if uint64(len(kinds)) > math.MaxUint32 {
		return ErrIndexTooLarge
	}
	var counts [6]uint32
	for _, kind := range kinds {
		if !kind.Valid() {
			return ErrInvalidSchema
		}
		counts[kind]++
	}

	dst.Kinds = resizeIndex(dst.Kinds, len(kinds))
	dst.Columns = resizeIndex(dst.Columns, len(kinds))
	copy(dst.Kinds, kinds)
	var next [6]uint32
	for row, kind := range kinds {
		dst.Columns[row] = next[kind]
		next[kind]++
	}
	dst.Counts = counts
	return nil
}

// Lookup returns a field's kind and kind-local zero-based column.
func (s Schema) Lookup(field schema.FieldID) (schema.ValueKind, uint32, bool) {
	if field == 0 {
		return 0, 0, false
	}
	row := uint64(field - 1)
	if row >= uint64(len(s.Kinds)) || row >= uint64(len(s.Columns)) {
		return 0, 0, false
	}
	return s.Kinds[int(row)], s.Columns[int(row)], true
}

// ColumnCount returns the number of value columns assigned to kind.
func (s Schema) ColumnCount(kind schema.ValueKind) (uint32, bool) {
	if !kind.Valid() {
		return 0, false
	}
	return s.Counts[kind], true
}

// Clone returns an exact-capacity copy suitable for immutable publication.
func (s Schema) Clone() Schema {
	return Schema{
		Kinds:   cloneIndexExact(s.Kinds),
		Columns: cloneIndexExact(s.Columns),
		Counts:  s.Counts,
	}
}
