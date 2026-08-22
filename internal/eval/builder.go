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
	// ErrInvalidRow reports a request or evidence row outside the active batch.
	ErrInvalidRow = errors.New("eval: row outside active batch")
	// ErrInvalidField reports a zero or out-of-range FieldID.
	ErrInvalidField = errors.New("eval: invalid field")
	// ErrValueKind reports a typed setter used with a different field kind.
	ErrValueKind = errors.New("eval: field has incompatible value kind")
	// ErrInvalidValue reports a zero ID where the schema requires a valid ID.
	ErrInvalidValue = errors.New("eval: invalid zero value")
	// ErrInvalidEvidence reports an invalid evidence row or CSR relationship.
	ErrInvalidEvidence = errors.New("eval: invalid evidence")
	// ErrIncompleteBatch reports missing required IDs or an unfinished CSR.
	ErrIncompleteBatch = errors.New("eval: incomplete batch")
)

// Builder owns one reusable mutable Batch. It is not safe for concurrent use.
type Builder struct {
	program    *program.Program
	batch      Batch
	extension  schema.Interner
	fields     policyindex.Schema
	symbolBase uint32
	csrReady   bool
	active     bool
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

func validateProgramSymbols(p *program.Program) bool {
	count := uint64(p.ProgramSymbolCount)
	if count != uint64(len(p.SymbolStarts)) || count != uint64(len(p.SymbolLengths)) ||
		len(p.SymbolHashes) != len(p.SymbolIDs) {
		return false
	}
	slots := len(p.SymbolIDs)
	if slots == 0 {
		return count == 0 && len(p.SymbolBytes) == 0
	}
	if slots&(slots-1) != 0 || uint64(slots) < 2*count {
		return false
	}
	nonzero := uint64(0)
	for _, id := range p.SymbolIDs {
		if id == 0 {
			continue
		}
		if uint64(id) > count {
			return false
		}
		nonzero++
	}
	if nonzero != count {
		return false
	}
	for id := uint64(1); id <= count; id++ {
		value, ok := p.Symbol(schema.SymbolID(id))
		if !ok {
			return false
		}
		found, ok := p.LookupSymbol(value)
		if !ok || uint64(found) != id {
			return false
		}
	}
	return true
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
	if (p != b.program || p.ProgramSymbolCount != b.symbolBase) && !validateProgramSymbols(p) {
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
	b.extension.Reset()
	b.batch.Rows = rows
	b.fields = p.FieldIndex
	b.program = p
	b.symbolBase = p.ProgramSymbolCount
	b.csrReady = evidenceRefs == 0
	b.active = true
	return nil
}

// InternSymbol resolves value in the immutable Program first, then in the
// reusable batch-local extension namespace above ProgramSymbolCount.
func (b *Builder) InternSymbol(value []byte) (schema.SymbolID, error) {
	if b == nil || !b.active || b.program == nil {
		return 0, ErrInvalidBuilder
	}
	if id, ok := b.program.LookupSymbol(value); ok {
		if uint32(id) > b.symbolBase {
			return 0, ErrInvalidProgram
		}
		return id, nil
	}
	base := uint64(b.symbolBase)
	if local, ok := b.extension.Lookup(value); ok {
		id := base + uint64(local)
		if id > math.MaxUint32 {
			return 0, ErrBatchTooLarge
		}
		return schema.SymbolID(id), nil
	}
	next := uint64(b.extension.Len()) + 1
	if base+next > math.MaxUint32 {
		return 0, ErrBatchTooLarge
	}
	local, err := b.extension.Intern(value)
	if err != nil {
		return 0, ErrBatchTooLarge
	}
	return schema.SymbolID(base + uint64(local)), nil
}

// Symbol resolves a Program or batch-extension SymbolID. Extension bytes are
// invalidated by the next successful Begin or later extension InternSymbol.
func (b *Builder) Symbol(id schema.SymbolID) ([]byte, bool) {
	if b == nil || b.program == nil || id == 0 {
		return nil, false
	}
	if uint32(id) <= b.symbolBase {
		return b.program.Symbol(id)
	}
	return b.extension.Bytes(schema.SymbolID(uint32(id) - b.symbolBase))
}

// SetRequestID sets the required nonzero request ID for row.
func (b *Builder) SetRequestID(row uint32, id schema.RequestID) error {
	if b == nil || !b.active {
		return ErrInvalidBuilder
	}
	if row >= b.batch.Rows {
		return ErrInvalidRow
	}
	if id == 0 {
		return ErrInvalidValue
	}
	b.batch.RequestIDs[row] = id
	return nil
}

func (b *Builder) factColumn(row uint32, field schema.FieldID, want schema.ValueKind) (uint32, error) {
	if b == nil || !b.active {
		return 0, ErrInvalidBuilder
	}
	if row >= b.batch.Rows {
		return 0, ErrInvalidRow
	}
	kind, column, ok := b.fields.Lookup(field)
	if !ok {
		return 0, ErrInvalidField
	}
	if kind != want {
		return 0, ErrValueKind
	}
	return column, nil
}

func (b *Builder) setPresence(row uint32, field schema.FieldID) {
	words := uint64(b.batch.WordCount())
	word := uint64(field-1)*words + uint64(row>>6)
	b.batch.PresenceMasks[int(word)] |= uint64(1) << (row & 63)
}

// SetSymbol writes one nonzero symbol and marks the field present.
func (b *Builder) SetSymbol(row uint32, field schema.FieldID, value schema.SymbolID) error {
	column, err := b.factColumn(row, field, schema.ValueKindSymbol)
	if err != nil {
		return err
	}
	if value == 0 {
		return ErrInvalidValue
	}
	i := uint64(column)*uint64(b.batch.Rows) + uint64(row)
	b.batch.SymbolValues[int(i)] = value
	b.setPresence(row, field)
	return nil
}

// SetInteger writes one integer and marks the field present.
func (b *Builder) SetInteger(row uint32, field schema.FieldID, value int64) error {
	column, err := b.factColumn(row, field, schema.ValueKindInteger)
	if err != nil {
		return err
	}
	i := uint64(column)*uint64(b.batch.Rows) + uint64(row)
	b.batch.IntegerValues[int(i)] = value
	b.setPresence(row, field)
	return nil
}

// SetTimestamp writes one timestamp and marks the field present.
func (b *Builder) SetTimestamp(row uint32, field schema.FieldID, value int64) error {
	column, err := b.factColumn(row, field, schema.ValueKindTimestamp)
	if err != nil {
		return err
	}
	i := uint64(column)*uint64(b.batch.Rows) + uint64(row)
	b.batch.TimestampValues[int(i)] = value
	b.setPresence(row, field)
	return nil
}

// SetBoolean writes one bit-packed Boolean and marks the field present.
func (b *Builder) SetBoolean(row uint32, field schema.FieldID, value bool) error {
	column, err := b.factColumn(row, field, schema.ValueKindBoolean)
	if err != nil {
		return err
	}
	words := uint64(b.batch.WordCount())
	word := uint64(column)*words + uint64(row>>6)
	bit := uint64(1) << (row & 63)
	if value {
		b.batch.BooleanValues[int(word)] |= bit
	} else {
		b.batch.BooleanValues[int(word)] &^= bit
	}
	b.setPresence(row, field)
	return nil
}

// SetPresent marks one presence-only field present for row.
func (b *Builder) SetPresent(row uint32, field schema.FieldID) error {
	if _, err := b.factColumn(row, field, schema.ValueKindPresence); err != nil {
		return err
	}
	b.setPresence(row, field)
	return nil
}

// SetEvidence writes one evidence record into a zero-based evidence row.
func (b *Builder) SetEvidence(row uint32, record EvidenceRecord) error {
	if b == nil || !b.active {
		return ErrInvalidBuilder
	}
	if uint64(row) >= uint64(len(b.batch.Evidence.IDs)) {
		return ErrInvalidRow
	}
	if record.ID == 0 || record.Kind == 0 || record.State == 0 {
		return ErrInvalidEvidence
	}
	i := int(row)
	b.batch.Evidence.IDs[i] = record.ID
	b.batch.Evidence.Kinds[i] = record.Kind
	b.batch.Evidence.States[i] = record.State
	b.batch.Evidence.Subjects[i] = record.Subject
	b.batch.Evidence.Scopes[i] = record.Scope
	b.batch.Evidence.Reviewers[i] = record.Reviewer
	b.batch.Evidence.Timings[i] = record.Timing
	b.batch.Evidence.Timestamps[i] = record.Timestamp
	return nil
}

// SetEvidenceCSR replaces the request-to-evidence relation after validating
// the complete source. References are zero-based evidence-row indices.
func (b *Builder) SetEvidenceCSR(offsets, refs []uint32) error {
	if b == nil || !b.active {
		return ErrInvalidBuilder
	}
	if len(offsets) != len(b.batch.EvidenceOffsets) || len(refs) != len(b.batch.EvidenceRefs) ||
		len(offsets) == 0 || offsets[0] != 0 || uint64(offsets[len(offsets)-1]) != uint64(len(refs)) {
		return ErrInvalidEvidence
	}
	previous := uint32(0)
	for _, offset := range offsets[1:] {
		if offset < previous || uint64(offset) > uint64(len(refs)) {
			return ErrInvalidEvidence
		}
		previous = offset
	}
	evidenceRows := uint64(len(b.batch.Evidence.IDs))
	for _, ref := range refs {
		if uint64(ref) >= evidenceRows {
			return ErrInvalidEvidence
		}
	}
	copy(b.batch.EvidenceOffsets, offsets)
	copy(b.batch.EvidenceRefs, refs)
	b.csrReady = true
	return nil
}

// Finish validates required columns, seals the current build, and returns a
// borrowed view valid until the next successful Begin call.
func (b *Builder) Finish() (Batch, error) {
	if b == nil || !b.active {
		return Batch{}, ErrInvalidBuilder
	}
	for _, id := range b.batch.RequestIDs {
		if id == 0 {
			return Batch{}, ErrIncompleteBatch
		}
	}
	evidence := &b.batch.Evidence
	n := len(evidence.IDs)
	if len(evidence.Kinds) != n || len(evidence.States) != n || len(evidence.Subjects) != n ||
		len(evidence.Scopes) != n || len(evidence.Reviewers) != n || len(evidence.Timings) != n ||
		len(evidence.Timestamps) != n {
		return Batch{}, ErrIncompleteBatch
	}
	for i, id := range evidence.IDs {
		if id == 0 || evidence.Kinds[i] == 0 || evidence.States[i] == 0 {
			return Batch{}, ErrIncompleteBatch
		}
	}
	if !b.csrReady {
		return Batch{}, ErrIncompleteBatch
	}
	b.active = false
	return b.batch, nil
}
