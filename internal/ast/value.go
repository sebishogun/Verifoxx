package ast

import (
	"errors"
	"math"

	"github.com/sebishogun/nornrune/internal/schema"
)

var (
	ErrTooManyValues  = errors.New("ast: too many values")
	ErrSymbolTooLarge = errors.New("ast: symbol bytes exceed uint32 address space")
)

func (d *Document) valueIndex(id schema.ValueID) (int, bool) {
	if id == 0 {
		return 0, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(d.ValueKinds)) || i >= uint64(len(d.ValueRefs)) {
		return 0, false
	}
	return int(i), true
}

// ValueKind returns the bounded kind stored by id.
func (d *Document) ValueKind(id schema.ValueID) (schema.ValueKind, bool) {
	i, ok := d.valueIndex(id)
	if !ok {
		return schema.ValueKindInvalid, false
	}
	return d.ValueKinds[i], true
}

// SymbolValue returns a read-only view of a decoded symbol literal.
func (d *Document) SymbolValue(id schema.ValueID) ([]byte, bool) {
	i, ok := d.valueIndex(id)
	if !ok || d.ValueKinds[i] != schema.ValueKindSymbol {
		return nil, false
	}
	r := uint64(d.ValueRefs[i])
	if r >= uint64(len(d.SymbolStarts)) || r >= uint64(len(d.SymbolLengths)) {
		return nil, false
	}
	start := d.SymbolStarts[r]
	length := d.SymbolLengths[r]
	if uint64(start)+uint64(length) > uint64(len(d.SymbolBytes)) {
		return nil, false
	}
	return d.SymbolBytes[int(start):int(start+length)], true
}

// IntegerValue returns an integer literal.
func (d *Document) IntegerValue(id schema.ValueID) (int64, bool) {
	i, ok := d.valueIndex(id)
	if !ok || d.ValueKinds[i] != schema.ValueKindInteger {
		return 0, false
	}
	r := uint64(d.ValueRefs[i])
	if r >= uint64(len(d.IntegerValues)) {
		return 0, false
	}
	return d.IntegerValues[r], true
}

// BooleanValue returns a Boolean literal.
func (d *Document) BooleanValue(id schema.ValueID) (bool, bool) {
	i, ok := d.valueIndex(id)
	if !ok || d.ValueKinds[i] != schema.ValueKindBoolean {
		return false, false
	}
	r := uint64(d.ValueRefs[i])
	if r >= uint64(len(d.BooleanValues)) || d.BooleanValues[r] > 1 {
		return false, false
	}
	return d.BooleanValues[r] == 1, true
}

// TimestampValue returns a normalized timestamp literal.
func (d *Document) TimestampValue(id schema.ValueID) (int64, bool) {
	i, ok := d.valueIndex(id)
	if !ok || d.ValueKinds[i] != schema.ValueKindTimestamp {
		return 0, false
	}
	r := uint64(d.ValueRefs[i])
	if r >= uint64(len(d.TimestampValues)) {
		return 0, false
	}
	return d.TimestampValues[r], true
}

func (b *Builder) validateValue() error {
	if uint64(len(b.doc.ValueKinds)) >= uint64(math.MaxUint32) {
		return ErrTooManyValues
	}
	return nil
}

func (b *Builder) addValue(kind schema.ValueKind, ref uint32) schema.ValueID {
	b.doc.ValueKinds = append(b.doc.ValueKinds, kind)
	b.doc.ValueRefs = append(b.doc.ValueRefs, ref)
	return schema.ValueID(len(b.doc.ValueKinds))
}

// AddSymbolValue copies one decoded symbol literal into the document slab.
func (b *Builder) AddSymbolValue(value []byte) (schema.ValueID, error) {
	if err := b.validateValue(); err != nil {
		return 0, err
	}
	if uint64(len(b.doc.SymbolBytes))+uint64(len(value)) > uint64(math.MaxUint32) {
		return 0, ErrSymbolTooLarge
	}
	start := uint32(len(b.doc.SymbolBytes))
	ref := uint32(len(b.doc.SymbolStarts))
	b.doc.SymbolBytes = append(b.doc.SymbolBytes, value...)
	b.doc.SymbolStarts = append(b.doc.SymbolStarts, start)
	b.doc.SymbolLengths = append(b.doc.SymbolLengths, uint32(len(value)))
	return b.addValue(schema.ValueKindSymbol, ref), nil
}

// AddIntegerValue appends one signed integer literal.
func (b *Builder) AddIntegerValue(value int64) (schema.ValueID, error) {
	if err := b.validateValue(); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.IntegerValues))
	b.doc.IntegerValues = append(b.doc.IntegerValues, value)
	return b.addValue(schema.ValueKindInteger, ref), nil
}

// AddBooleanValue appends one Boolean literal as a bounded byte payload.
func (b *Builder) AddBooleanValue(value bool) (schema.ValueID, error) {
	if err := b.validateValue(); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.BooleanValues))
	encoded := uint8(0)
	if value {
		encoded = 1
	}
	b.doc.BooleanValues = append(b.doc.BooleanValues, encoded)
	return b.addValue(schema.ValueKindBoolean, ref), nil
}

// AddTimestampValue appends one normalized timestamp literal.
func (b *Builder) AddTimestampValue(value int64) (schema.ValueID, error) {
	if err := b.validateValue(); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.TimestampValues))
	b.doc.TimestampValues = append(b.doc.TimestampValues, value)
	return b.addValue(schema.ValueKindTimestamp, ref), nil
}
