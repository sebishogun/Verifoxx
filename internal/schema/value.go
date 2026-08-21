package schema

// ValueKind is the bounded type of a field or literal value. Kinds map to
// the batch column layout: symbols, integers, booleans, timestamps, and the
// presence/missing column.
type ValueKind uint8

const (
	// ValueKindInvalid is the zero value and is never a legal kind.
	ValueKindInvalid ValueKind = iota
	// ValueKindSymbol is an interned string (field value, action, scope).
	ValueKindSymbol
	// ValueKindInteger is a signed 64-bit integer column.
	ValueKindInteger
	// ValueKindBoolean is a two-state Boolean column.
	ValueKindBoolean
	// ValueKindTimestamp is a 64-bit timestamp column.
	ValueKindTimestamp
	// ValueKindPresence covers presence/missing semantics for Exists-style
	// checks; it maps to the presence mask column.
	ValueKindPresence
)

// Valid reports whether k is one of the five bounded value kinds.
func (k ValueKind) Valid() bool {
	return k >= ValueKindSymbol && k <= ValueKindPresence
}

var valueKindNames = [...]string{
	"invalid", "symbol", "integer", "boolean", "timestamp", "presence",
}

// String returns the stable name of the kind, or "invalid" when out of range.
func (k ValueKind) String() string {
	if int(k) < len(valueKindNames) {
		return valueKindNames[k]
	}
	return "invalid"
}
