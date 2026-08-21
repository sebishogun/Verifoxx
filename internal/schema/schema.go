package schema

import "errors"

var (
	// ErrDuplicateField reports registering the same field name symbol twice.
	ErrDuplicateField = errors.New("schema: duplicate field symbol")
	// ErrInvalidValueKind reports registering a field with an out-of-range kind.
	ErrInvalidValueKind = errors.New("schema: invalid value kind")
	// ErrInvalidFieldGroup reports registering a field with an invalid group.
	ErrInvalidFieldGroup = errors.New("schema: invalid field group")
	// ErrInvalidSymbol reports registering a field with the reserved zero symbol.
	ErrInvalidSymbol = errors.New("schema: invalid field symbol")
)

// Builder registers fields into parallel metadata columns. It rejects
// duplicates and invalid inputs without mutating state, so a failed
// registration leaves the builder exactly as it was. Reset clears logical
// content while retaining capacity for reuse.
type Builder struct {
	names  []SymbolID
	kinds  []ValueKind
	groups []FieldGroup
}

// NewBuilder returns an empty field-schema builder.
func NewBuilder() *Builder { return &Builder{} }

// Len returns the number of successfully registered fields.
func (b *Builder) Len() int { return len(b.names) }

// Lookup returns the FieldID for a name symbol registered in this builder,
// or 0 with ok=false. Allocation-free linear scan; used to prove rejected
// registrations do not disturb existing fields.
func (b *Builder) Lookup(name SymbolID) (FieldID, bool) {
	for i, n := range b.names {
		if n == name {
			return FieldID(i + 1), true
		}
	}
	return 0, false
}

// AddField registers a field with an interned name symbol, a value kind, and
// a logical group. It returns a new stable FieldID on success. An exact
// duplicate or incompatible redefinition of a name returns ErrDuplicateField.
// Invalid metadata or the reserved zero symbol also returns an error without
// modifying the builder.
func (b *Builder) AddField(name SymbolID, kind ValueKind, group FieldGroup) (FieldID, error) {
	if name == 0 {
		return 0, ErrInvalidSymbol
	}
	if !kind.Valid() {
		return 0, ErrInvalidValueKind
	}
	if !group.Valid() {
		return 0, ErrInvalidFieldGroup
	}
	for _, n := range b.names {
		if n == name {
			return 0, ErrDuplicateField
		}
	}
	id := FieldID(len(b.names) + 1)
	b.names = append(b.names, name)
	b.kinds = append(b.kinds, kind)
	b.groups = append(b.groups, group)
	return id, nil
}

// Reset clears all registered fields while retaining slice capacity. IDs
// assigned after Reset restart from 1.
func (b *Builder) Reset() {
	b.names = b.names[:0]
	b.kinds = b.kinds[:0]
	b.groups = b.groups[:0]
}

// Finish freezes the current fields into an immutable Schema. The schema is
// a copy: later builder additions and resets do not affect it.
func (b *Builder) Finish() *Schema {
	n := len(b.names)
	names := make([]SymbolID, n)
	kinds := make([]ValueKind, n)
	groups := make([]FieldGroup, n)
	copy(names, b.names)
	copy(kinds, b.kinds)
	copy(groups, b.groups)
	return &Schema{FieldTable: FieldTable{names: names, kinds: kinds, groups: groups}}
}

// Schema is the frozen field metadata for one policy pack. All lookups and
// queries are allocation-free; the parallel columns are directly usable for
// column mapping in later phases.
type Schema struct {
	FieldTable
}
