package eval

import (
	"errors"
	"math"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	// ErrInvalidBuilder reports a nil builder or a call outside an active build.
	ErrInvalidBuilder = errors.New("eval: invalid batch builder state")
	// ErrInvalidProgram reports malformed compiled field or symbol metadata.
	ErrInvalidProgram = errors.New("eval: invalid compiled program")
	// ErrBatchTooLarge reports a fixed-width or host slice-index overflow.
	ErrBatchTooLarge = errors.New("eval: batch exceeds fixed-width limits")
)

// Builder owns one reusable mutable Batch. It is not safe for concurrent use.
type Builder struct {
	batch   Batch
	fields  policyindex.Schema
	program *program.Program
	active  bool
}

type batchShape struct {
	symbols    int
	integers   int
	timestamps int
	booleans   int
	presence   int
	rows       int
	offsets    int
	evidence   int
	refs       int
}

func checkedSliceLen(a, b uint64, elementBytes uint64) (int, bool) {
	n := a * b
	if n > uint64(math.MaxInt) || n > uint64(math.MaxInt)/elementBytes {
		return 0, false
	}
	return int(n), true
}

func validateFieldIndex(fields policyindex.Schema) bool {
	if len(fields.Kinds) != len(fields.Columns) || uint64(len(fields.Kinds)) > math.MaxUint32 {
		return false
	}
	var next [6]uint32
	for row, kind := range fields.Kinds {
		if !kind.Valid() || fields.Columns[row] != next[kind] || next[kind] == math.MaxUint32 {
			return false
		}
		next[kind]++
	}
	return next == fields.Counts
}

func makeBatchShape(fields policyindex.Schema, rows, evidenceRows, evidenceRefs uint32) (batchShape, bool) {
	if !validateFieldIndex(fields) || rows == math.MaxUint32 {
		return batchShape{}, false
	}
	words := (uint64(rows) + 63) >> 6
	fieldCount := uint64(len(fields.Kinds))
	var shape batchShape
	var ok bool
	if shape.symbols, ok = checkedSliceLen(uint64(fields.Counts[schema.ValueKindSymbol]), uint64(rows), 4); !ok {
		return batchShape{}, false
	}
	if shape.integers, ok = checkedSliceLen(uint64(fields.Counts[schema.ValueKindInteger]), uint64(rows), 8); !ok {
		return batchShape{}, false
	}
	if shape.timestamps, ok = checkedSliceLen(uint64(fields.Counts[schema.ValueKindTimestamp]), uint64(rows), 8); !ok {
		return batchShape{}, false
	}
	if shape.booleans, ok = checkedSliceLen(uint64(fields.Counts[schema.ValueKindBoolean]), words, 8); !ok {
		return batchShape{}, false
	}
	if shape.presence, ok = checkedSliceLen(fieldCount, words, 8); !ok {
		return batchShape{}, false
	}
	if shape.rows, ok = checkedSliceLen(uint64(rows), 1, 4); !ok {
		return batchShape{}, false
	}
	if shape.offsets, ok = checkedSliceLen(uint64(rows)+1, 1, 4); !ok {
		return batchShape{}, false
	}
	if shape.evidence, ok = checkedSliceLen(uint64(evidenceRows), 1, 8); !ok {
		return batchShape{}, false
	}
	if shape.refs, ok = checkedSliceLen(uint64(evidenceRefs), 1, 4); !ok {
		return batchShape{}, false
	}
	return shape, true
}

func resizeClear[T any](dst []T, length int) []T {
	if cap(dst) < length {
		return make([]T, length)
	}
	dst = dst[:length]
	clear(dst)
	return dst
}

// Begin starts a build for exact request, evidence-row, and evidence-reference
// counts. A failed call leaves the prior batch unchanged.
func (b *Builder) Begin(p *program.Program, rows, evidenceRows, evidenceRefs uint32) error {
	if b == nil {
		return ErrInvalidBuilder
	}
	if p == nil {
		return ErrInvalidProgram
	}
	shape, ok := makeBatchShape(p.FieldIndex, rows, evidenceRows, evidenceRefs)
	if !ok {
		if validateFieldIndex(p.FieldIndex) {
			return ErrBatchTooLarge
		}
		return ErrInvalidProgram
	}

	b.batch.SymbolValues = resizeClear(b.batch.SymbolValues, shape.symbols)
	b.batch.IntegerValues = resizeClear(b.batch.IntegerValues, shape.integers)
	b.batch.TimestampValues = resizeClear(b.batch.TimestampValues, shape.timestamps)
	b.batch.BooleanValues = resizeClear(b.batch.BooleanValues, shape.booleans)
	b.batch.PresenceMasks = resizeClear(b.batch.PresenceMasks, shape.presence)
	b.batch.RequestIDs = resizeClear(b.batch.RequestIDs, shape.rows)
	b.batch.EvidenceOffsets = resizeClear(b.batch.EvidenceOffsets, shape.offsets)
	b.batch.EvidenceRefs = resizeClear(b.batch.EvidenceRefs, shape.refs)
	b.batch.Evidence.resize(shape.evidence)
	b.batch.Rows = rows
	b.fields = p.FieldIndex
	b.program = p
	b.active = true
	return nil
}
