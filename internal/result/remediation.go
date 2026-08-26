package result

import "github.com/sebishogun/nornrune/internal/schema"

// RemediationKind enumerates the bounded remediation shapes a policy row may
// carry: set a field to an interned value, or require one additional evidence
// kind.
type RemediationKind uint8

const (
	RemediationInvalid RemediationKind = iota
	RemediationSetField
	RemediationAddEvidence
)

// Valid reports whether kind is a defined remediation shape.
func (k RemediationKind) Valid() bool {
	return k == RemediationSetField || k == RemediationAddEvidence
}

// RemediationTable maps one-based RemediationIDs to bounded remediation
// records. All columns are parallel and borrowed; lookups never allocate.
type RemediationTable struct {
	Kinds         []RemediationKind
	Fields        []schema.FieldID
	Values        []schema.ValueID
	EvidenceKinds []schema.EvidenceKindID
}

// Remediation is one policy remediation record.
type Remediation struct {
	Kind         RemediationKind
	Field        schema.FieldID
	Value        schema.ValueID
	EvidenceKind schema.EvidenceKindID
}

// Lookup returns the one-based remediation record for id. IDs are valid only
// if they fall inside every column; zero and out-of-range IDs return false.
// The returned record is a stack value.
func (t *RemediationTable) Lookup(id schema.RemediationID) (Remediation, bool) {
	idx, ok := t.index(id)
	if !ok {
		return Remediation{}, false
	}
	return Remediation{
		Kind:         t.Kinds[idx],
		Field:        t.Fields[idx],
		Value:        t.Values[idx],
		EvidenceKind: t.EvidenceKinds[idx],
	}, true
}

// valid reports whether the columns are equal in length and every row carries
// a defined kind with the exact payload that kind requires: SetField needs a
// field and value and no evidence kind; AddEvidence needs an evidence kind and
// no field or value. An aligned empty table is valid: a policy may define no
// remediations.
func (t *RemediationTable) valid() bool {
	n := len(t.Kinds)
	if len(t.Fields) != n || len(t.Values) != n || len(t.EvidenceKinds) != n {
		return false
	}
	for i := 0; i < n; i++ {
		switch t.Kinds[i] {
		case RemediationSetField:
			if t.Fields[i] == 0 || t.Values[i] == 0 || t.EvidenceKinds[i] != 0 {
				return false
			}
		case RemediationAddEvidence:
			if t.Fields[i] != 0 || t.Values[i] != 0 || t.EvidenceKinds[i] == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// index returns the zero-based column offset for id, rejecting zero and any
// ID that falls outside a column. The sign guard keeps the uint32-to-int
// conversion safe on 32-bit platforms.
func (t *RemediationTable) index(id schema.RemediationID) (int, bool) {
	if id == 0 {
		return 0, false
	}
	i := int(id - 1)
	if i < 0 || i >= len(t.Kinds) || i >= len(t.Fields) || i >= len(t.Values) || i >= len(t.EvidenceKinds) {
		return 0, false
	}
	return i, true
}
