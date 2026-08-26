// Package result defines policy-owned outcome, remediation, and resolution
// tables used by the uncertainty resolver. Tables are non-owning
// structure-of-arrays views over numeric IDs; lookups and selection never
// allocate.
package result

import "github.com/sebishogun/nornrune/internal/schema"

// OutcomeTable maps one-based OutcomeIDs to policy-defined outcome records.
// All columns are parallel; IDs outside every column are invalid.
type OutcomeTable struct {
	Names      []schema.SymbolID
	Precedence []uint8
	Terminal   []bool
}

// Outcome is one policy outcome record.
type Outcome struct {
	Name       schema.SymbolID
	Precedence uint8
	Terminal   bool
}

// Lookup returns the one-based outcome record for id. IDs are valid only if
// they fall inside every column; zero and out-of-range IDs return false.
func (t *OutcomeTable) Lookup(id schema.OutcomeID) (Outcome, bool) {
	idx, ok := t.index(id)
	if !ok {
		return Outcome{}, false
	}
	return Outcome{
		Name:       t.Names[idx],
		Precedence: t.Precedence[idx],
		Terminal:   t.Terminal[idx],
	}, true
}

// Prefer selects between current and candidate outcomes. Zero means absence:
// absent-plus-absent returns zero, and absent plus a valid ID returns the
// other. Higher numeric precedence wins; an equal-precedence tie keeps the
// lower OutcomeID. Terminal never affects selection. Any invalid nonzero ID
// returns ok=false.
func (t *OutcomeTable) Prefer(current, candidate schema.OutcomeID) (schema.OutcomeID, bool) {
	if current == 0 {
		if candidate == 0 {
			return 0, true
		}
		if _, ok := t.index(candidate); !ok {
			return 0, false
		}
		return candidate, true
	}
	if _, ok := t.index(current); !ok {
		return 0, false
	}
	if candidate == 0 {
		return current, true
	}
	if _, ok := t.index(candidate); !ok {
		return 0, false
	}
	return t.preferKnown(current, candidate), true
}

// preferKnown selects between two already-validated IDs or zero without
// bounds checks, using the same precedence and lower-ID tie rules as Prefer.
func (t *OutcomeTable) preferKnown(current, candidate schema.OutcomeID) schema.OutcomeID {
	if current == 0 {
		return candidate
	}
	if candidate == 0 {
		return current
	}
	cur := t.Precedence[int(current-1)]
	cand := t.Precedence[int(candidate-1)]
	if cand > cur || (cand == cur && candidate < current) {
		return candidate
	}
	return current
}

// valid reports whether the columns are non-empty, equal in length, and hold
// nonzero name IDs. Precedence 0 and either terminal value are allowed.
func (t *OutcomeTable) valid() bool {
	n := len(t.Names)
	if n == 0 || len(t.Precedence) != n || len(t.Terminal) != n {
		return false
	}
	for _, name := range t.Names {
		if name == 0 {
			return false
		}
	}
	return true
}

// index returns the zero-based column offset for id, rejecting zero and any
// ID that falls outside a column. The sign guard keeps the uint32-to-int
// conversion safe on 32-bit platforms.
func (t *OutcomeTable) index(id schema.OutcomeID) (int, bool) {
	if id == 0 {
		return 0, false
	}
	i := int(id - 1)
	if i < 0 || i >= len(t.Names) || i >= len(t.Precedence) || i >= len(t.Terminal) {
		return 0, false
	}
	return i, true
}
