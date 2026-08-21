package schema

// FieldGroup is the logical request-fact group a field belongs to. Groups
// match the batch layout: subject, action, resource, output, and context.
type FieldGroup uint8

const (
	// FieldGroupInvalid is the zero value and is never a legal group.
	FieldGroupInvalid FieldGroup = iota
	// FieldGroupSubject covers the requester and trust dimension.
	FieldGroupSubject
	// FieldGroupAction covers the requested action.
	FieldGroupAction
	// FieldGroupResource covers the target dataset and output.
	FieldGroupResource
	// FieldGroupOutput covers the produced output shape.
	FieldGroupOutput
	// FieldGroupContext covers environment and usage context.
	FieldGroupContext
)

// Valid reports whether g is one of the five logical field groups.
func (g FieldGroup) Valid() bool {
	return g >= FieldGroupSubject && g <= FieldGroupContext
}

// FieldTable is the struct-of-arrays field metadata. FieldID f indexes all
// three columns at offset f-1; FieldID 0 is invalid. The parallel layout is
// ready for later column mapping: one contiguous column per attribute.
type FieldTable struct {
	names  []SymbolID
	kinds  []ValueKind
	groups []FieldGroup
}

// Len returns the number of registered fields.
func (t FieldTable) Len() int { return len(t.names) }

// Name returns the name symbol of field f and whether f is valid.
func (t FieldTable) Name(f FieldID) (SymbolID, bool) {
	i := int(f) - 1
	if i < 0 || i >= len(t.names) {
		return 0, false
	}
	return t.names[i], true
}

// Kind returns the value kind of field f and whether f is valid.
func (t FieldTable) Kind(f FieldID) (ValueKind, bool) {
	i := int(f) - 1
	if i < 0 || i >= len(t.kinds) {
		return 0, false
	}
	return t.kinds[i], true
}

// Group returns the logical group of field f and whether f is valid.
func (t FieldTable) Group(f FieldID) (FieldGroup, bool) {
	i := int(f) - 1
	if i < 0 || i >= len(t.groups) {
		return 0, false
	}
	return t.groups[i], true
}

// Lookup returns the FieldID for a name symbol, or 0 with ok=false when no
// field has that name. It is a cold-path linear scan over interned symbol
// IDs and performs no allocation.
func (t FieldTable) Lookup(name SymbolID) (FieldID, bool) {
	for i, n := range t.names {
		if n == name {
			return FieldID(i + 1), true
		}
	}
	return 0, false
}
