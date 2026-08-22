package index

import (
	"slices"

	"github.com/sebishogun/verifoxx/internal/schema"
)

// Query binds one immutable Policy so its static columns are validated once.
// Candidate values and presence flags are supplied in Policy.FieldIDs order.
type Query struct {
	policy *Policy
}

// Bind validates p before replacing a previous usable binding.
func (q *Query) Bind(p *Policy) error {
	if q == nil || p == nil {
		return ErrInvalidPolicy
	}
	if q.policy == p {
		return nil
	}
	if !validBoundPolicy(p) {
		return ErrInvalidPolicy
	}
	q.policy = p
	return nil
}

// Candidates writes the conservative requirement mask for values into dst.
// Missing selectors do not filter; absent values select wildcard rows only.
func (q *Query) Candidates(dst []uint64, values []schema.SymbolID, present []uint8) error {
	if q == nil || q.policy == nil || len(dst) != int(q.policy.WordCount) ||
		len(values) != len(q.policy.FieldIDs) || len(present) != len(q.policy.FieldIDs) {
		return ErrInvalidQuery
	}
	p := q.policy
	copy(dst, p.AllMask)
	words := int(p.WordCount)
	for fieldRow := range p.FieldIDs {
		if present[fieldRow] == 0 {
			continue
		}
		valueStart := int(p.FieldValueStarts[fieldRow])
		valueEnd := valueStart + int(p.FieldValueCounts[fieldRow])
		valueRow, found := slices.BinarySearch(p.Values[valueStart:valueEnd], values[fieldRow])
		var mask []uint64
		if found {
			valueRow += valueStart
			mask = p.ValueMasks[valueRow*words : (valueRow+1)*words]
		} else {
			mask = p.WildcardMasks[fieldRow*words : (fieldRow+1)*words]
		}
		allZero := true
		for word := range words {
			dst[word] &= mask[word]
			allZero = allZero && dst[word] == 0
		}
		if allZero {
			return nil
		}
	}
	return nil
}

func validBoundPolicy(p *Policy) bool {
	if p == nil || p.WordCount != uint32(policyWordCount(p.RequirementCount)) ||
		len(p.AllMask) != int(p.WordCount) ||
		len(p.FieldValueStarts) != len(p.FieldIDs) || len(p.FieldValueCounts) != len(p.FieldIDs) {
		return false
	}
	words := uint64(p.WordCount)
	fieldCount := uint64(len(p.FieldIDs))
	valueCount := uint64(len(p.Values))
	if (fieldCount != 0 && words > ^uint64(0)/fieldCount) ||
		(valueCount != 0 && words > ^uint64(0)/valueCount) ||
		uint64(len(p.WildcardMasks)) != fieldCount*words ||
		uint64(len(p.ValueMasks)) != valueCount*words {
		return false
	}

	for word, got := range p.AllMask {
		want := ^uint64(0)
		if word+1 == len(p.AllMask) && p.RequirementCount&63 != 0 {
			want = uint64(1)<<(p.RequirementCount&63) - 1
		}
		if got != want {
			return false
		}
	}
	for i, field := range p.FieldIDs {
		if field == 0 || (i != 0 && field <= p.FieldIDs[i-1]) {
			return false
		}
		start := uint64(p.FieldValueStarts[i])
		count := uint64(p.FieldValueCounts[i])
		if count == 0 || start+count < start || start+count > valueCount ||
			(i == 0 && start != 0) || (i != 0 && start != uint64(p.FieldValueStarts[i-1])+uint64(p.FieldValueCounts[i-1])) {
			return false
		}
		values := p.Values[int(start):int(start+count)]
		for valueRow, value := range values {
			if value == 0 || (valueRow != 0 && value <= values[valueRow-1]) {
				return false
			}
		}
	}
	if len(p.FieldIDs) == 0 {
		if len(p.Values) != 0 {
			return false
		}
	} else {
		last := len(p.FieldIDs) - 1
		if uint64(p.FieldValueStarts[last])+uint64(p.FieldValueCounts[last]) != valueCount {
			return false
		}
	}
	for word, allowed := range p.AllMask {
		for fieldRow := range p.FieldIDs {
			if p.WildcardMasks[fieldRow*int(words)+word]&^allowed != 0 {
				return false
			}
		}
		for valueRow := range p.Values {
			if p.ValueMasks[valueRow*int(words)+word]&^allowed != 0 {
				return false
			}
		}
	}
	return true
}
