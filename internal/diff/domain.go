package diff

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// MaxBatchRows bounds one concrete comparison batch.
const MaxBatchRows uint32 = 4096

// FieldKind is one native fact-column kind.
type FieldKind uint8

const (
	FieldKindInvalid FieldKind = iota
	FieldKindString
	FieldKindInteger
	FieldKindBoolean
	FieldKindTimestamp
	FieldKindPresence
)

func (kind FieldKind) Valid() bool { return kind >= FieldKindString && kind <= FieldKindPresence }

// ValueState distinguishes an absent fact from a typed present value.
type ValueState uint8

const (
	ValueStateInvalid ValueState = iota
	ValueMissing
	ValuePresent
)

func (state ValueState) Valid() bool { return state == ValueMissing || state == ValuePresent }

// Value is one owned finite-domain fact option.
type Value struct {
	String  string
	Integer int64
	State   ValueState
	Kind    FieldKind
	Boolean bool
}

// FieldDomain is one named mixed-radix dimension.
type FieldDomain struct {
	Name   string
	Values []Value
	Kind   FieldKind
	Closed bool
}

// Evidence is one typed record in an evidence scenario.
type Evidence struct {
	Kind    string
	State   string
	Subject string
	Scope   string
	Timing  string
}

// EvidenceSet is one candidate evidence scenario. An empty set means no evidence.
type EvidenceSet struct {
	Records []Evidence
}

// Domain is the finite candidate declaration for one comparison.
type Domain struct {
	Fields       []FieldDomain
	EvidenceSets []EvidenceSet

	MaxCandidates uint64
	BatchRows     uint32
	// EvidenceClosed declares that EvidenceSets exhaust the relevant evidence scenarios.
	EvidenceClosed bool
}

// Validate checks shape, computes cardinality, and reports whether all declared
// field and evidence universes are caller-closed. Program-specific dependency
// coverage is checked later by Analyzer.
func (domain Domain) Validate() (uint64, bool, error) {
	return domain.validate(true)
}

func (domain Domain) validateShape() error {
	_, _, err := domain.validate(false)
	return err
}

func (domain Domain) validate(computeCardinality bool) (uint64, bool, error) {
	if domain.MaxCandidates == 0 || domain.BatchRows == 0 || domain.BatchRows > MaxBatchRows {
		return 0, false, ErrInvalidDomain
	}
	cardinality := uint64(1)
	complete := true
	// orderScratch keeps small domains allocation-free; larger value lists
	// spill to one bounded heap slice reused across the remaining fields.
	var orderScratch [16]uint32
	order := orderScratch[:0]
	for row := range domain.Fields {
		field := &domain.Fields[row]
		if !validDomainName(field.Name) || !field.Kind.Valid() || len(field.Values) == 0 {
			return 0, false, ErrInvalidDomain
		}
		for previous := 0; previous < row; previous++ {
			if domain.Fields[previous].Name == field.Name {
				return 0, false, ErrInvalidDomain
			}
		}
		missing := false
		order = order[:0]
		for valueRow := range field.Values {
			value := field.Values[valueRow]
			if value.Kind != field.Kind || !validDomainValue(value) {
				return 0, false, ErrInvalidDomain
			}
			order = append(order, uint32(valueRow))
			missing = missing || value.State == ValueMissing
		}
		if !missing {
			return 0, false, ErrInvalidDomain
		}
		slices.SortFunc(order, func(left, right uint32) int {
			return compareDomainValue(field.Values[left], field.Values[right])
		})
		for index := 1; index < len(order); index++ {
			if compareDomainValue(field.Values[order[index]], field.Values[order[index-1]]) == 0 {
				return 0, false, ErrInvalidDomain
			}
		}
		if computeCardinality {
			var ok bool
			cardinality, ok = checkedProduct(cardinality, uint64(len(field.Values)))
			if !ok {
				return math.MaxUint64, false, ErrCandidateBudget
			}
		}
		complete = complete && field.Closed
	}
	for setRow := range domain.EvidenceSets {
		for recordRow := range domain.EvidenceSets[setRow].Records {
			if !validEvidence(domain.EvidenceSets[setRow].Records[recordRow]) {
				return 0, false, ErrInvalidDomain
			}
		}
	}
	if len(domain.EvidenceSets) != 0 && computeCardinality {
		var ok bool
		cardinality, ok = checkedProduct(cardinality, uint64(len(domain.EvidenceSets)))
		if !ok {
			return math.MaxUint64, false, ErrCandidateBudget
		}
		complete = complete && domain.EvidenceClosed
	}
	if computeCardinality && cardinality > domain.MaxCandidates {
		return cardinality, complete, ErrCandidateBudget
	}
	return cardinality, complete, nil
}

func checkedProduct(left, right uint64) (uint64, bool) {
	if right == 0 || left > math.MaxUint64/right {
		return 0, false
	}
	return left * right, true
}

func validDomainName(name string) bool { return name != "" && utf8.ValidString(name) }

func validDomainValue(value Value) bool {
	if !value.Kind.Valid() || !value.State.Valid() || !utf8.ValidString(value.String) {
		return false
	}
	if value.State == ValueMissing {
		return value.String == "" && value.Integer == 0 && !value.Boolean
	}
	switch value.Kind {
	case FieldKindString:
		return value.Integer == 0 && !value.Boolean
	case FieldKindInteger, FieldKindTimestamp:
		return value.String == "" && !value.Boolean
	case FieldKindBoolean:
		return value.String == "" && value.Integer == 0
	case FieldKindPresence:
		return value.String == "" && value.Integer == 0 && !value.Boolean
	default:
		return false
	}
}

func validEvidence(record Evidence) bool {
	return record.Kind != "" && record.State != "" &&
		utf8.ValidString(record.Kind) && utf8.ValidString(record.State) &&
		utf8.ValidString(record.Subject) && utf8.ValidString(record.Scope) && utf8.ValidString(record.Timing)
}

// compareDomainValue totally orders values on the equality key used for
// duplicate rejection; Kind equality is already enforced per field.
func compareDomainValue(left, right Value) int {
	if left.State != right.State {
		if left.State < right.State {
			return -1
		}
		return 1
	}
	if comparison := strings.Compare(left.String, right.String); comparison != 0 {
		return comparison
	}
	if left.Integer != right.Integer {
		if left.Integer < right.Integer {
			return -1
		}
		return 1
	}
	if left.Boolean != right.Boolean {
		if !left.Boolean {
			return -1
		}
		return 1
	}
	return 0
}

// CloneDomain returns a deep owned domain copy.
func CloneDomain(source Domain) Domain {
	cloned := source
	cloned.Fields = make([]FieldDomain, len(source.Fields))
	for row := range source.Fields {
		cloned.Fields[row] = source.Fields[row]
		cloned.Fields[row].Name = strings.Clone(source.Fields[row].Name)
		cloned.Fields[row].Values = make([]Value, len(source.Fields[row].Values))
		for valueRow := range source.Fields[row].Values {
			cloned.Fields[row].Values[valueRow] = source.Fields[row].Values[valueRow]
			cloned.Fields[row].Values[valueRow].String = strings.Clone(source.Fields[row].Values[valueRow].String)
		}
	}
	cloned.EvidenceSets = make([]EvidenceSet, len(source.EvidenceSets))
	for setRow := range source.EvidenceSets {
		cloned.EvidenceSets[setRow].Records = make([]Evidence, len(source.EvidenceSets[setRow].Records))
		for recordRow := range source.EvidenceSets[setRow].Records {
			record := source.EvidenceSets[setRow].Records[recordRow]
			record.Kind = strings.Clone(record.Kind)
			record.State = strings.Clone(record.State)
			record.Subject = strings.Clone(record.Subject)
			record.Scope = strings.Clone(record.Scope)
			record.Timing = strings.Clone(record.Timing)
			cloned.EvidenceSets[setRow].Records[recordRow] = record
		}
	}
	return cloned
}
