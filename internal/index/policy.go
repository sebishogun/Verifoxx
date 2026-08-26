package index

import (
	"math"
	"slices"

	"github.com/sebishogun/nornrune/internal/schema"
)

// Constraints is a borrowed SoA/CSR view of positive symbolic applicability
// restrictions. Rows are zero-based requirement-table offsets.
type Constraints struct {
	Rows        []uint32
	Fields      []schema.FieldID
	ValueStarts []uint32
	ValueCounts []uint32
	Values      []schema.SymbolID
}

// Policy stores sorted selector values and fixed-width candidate masks.
// Published columns are immutable by convention.
type Policy struct {
	FieldIDs         []schema.FieldID
	FieldValueStarts []uint32
	FieldValueCounts []uint32
	WildcardMasks    []uint64
	Values           []schema.SymbolID
	ValueMasks       []uint64
	AllMask          []uint64
	RequirementCount uint32
	WordCount        uint32
}

// PolicyBuilder owns reusable construction scratch. A zero value is usable
// and must not be shared concurrently.
type PolicyBuilder struct {
	fieldScratch []schema.FieldID
	valueScratch []schema.SymbolID
	rowMarks     []uint8
}

func validateConstraints(requirementCount uint32, constraints Constraints) error {
	n := len(constraints.Rows)
	if len(constraints.Fields) != n || len(constraints.ValueStarts) != n || len(constraints.ValueCounts) != n {
		return ErrInvalidPolicy
	}
	if uint64(n) > math.MaxUint32 || uint64(len(constraints.Values)) > math.MaxUint32 ||
		uint64(requirementCount) > uint64(math.MaxInt) {
		return ErrIndexTooLarge
	}
	for row := range n {
		if constraints.Rows[row] >= requirementCount || constraints.Fields[row] == 0 || constraints.ValueCounts[row] == 0 {
			return ErrInvalidPolicy
		}
		start := uint64(constraints.ValueStarts[row])
		end := start + uint64(constraints.ValueCounts[row])
		if end > uint64(len(constraints.Values)) {
			return ErrInvalidPolicy
		}
		for _, value := range constraints.Values[int(start):int(end)] {
			if value == 0 {
				return ErrInvalidPolicy
			}
		}
	}
	return nil
}

func compactFields(fields []schema.FieldID) []schema.FieldID {
	if len(fields) == 0 {
		return fields
	}
	out := 1
	for i := 1; i < len(fields); i++ {
		if fields[i] != fields[out-1] {
			fields[out] = fields[i]
			out++
		}
	}
	return fields[:out]
}

func compactSymbols(values []schema.SymbolID) []schema.SymbolID {
	if len(values) == 0 {
		return values
	}
	out := 1
	for i := 1; i < len(values); i++ {
		if values[i] != values[out-1] {
			values[out] = values[i]
			out++
		}
	}
	return values[:out]
}

func policyWordCount(requirementCount uint32) uint64 {
	return (uint64(requirementCount) + 63) >> 6
}

// Build validates and canonicalizes constraints before replacing dst. Output
// slices retain capacity for reuse; immutable publication uses Policy.Clone.
func (b *PolicyBuilder) Build(dst *Policy, requirementCount uint32, constraints Constraints) error {
	if b == nil || dst == nil {
		return ErrInvalidPolicy
	}
	if err := validateConstraints(requirementCount, constraints); err != nil {
		return err
	}

	n := len(constraints.Fields)
	b.fieldScratch = resizeIndex(b.fieldScratch, n)
	copy(b.fieldScratch, constraints.Fields)
	slices.Sort(b.fieldScratch)
	fields := compactFields(b.fieldScratch)
	words := policyWordCount(requirementCount)
	if uint64(len(fields))*words > uint64(math.MaxInt) ||
		uint64(len(constraints.Values))*words > uint64(math.MaxInt) {
		return ErrIndexTooLarge
	}

	b.rowMarks = resizeIndex(b.rowMarks, int(requirementCount))
	for _, field := range fields {
		clear(b.rowMarks)
		for row := range n {
			if constraints.Fields[row] != field {
				continue
			}
			requirementRow := constraints.Rows[row]
			if b.rowMarks[requirementRow] != 0 {
				return ErrInvalidPolicy
			}
			b.rowMarks[requirementRow] = 1
		}
	}

	dst.FieldIDs = resizeIndex(dst.FieldIDs, len(fields))
	dst.FieldValueStarts = resizeIndex(dst.FieldValueStarts, len(fields))
	dst.FieldValueCounts = resizeIndex(dst.FieldValueCounts, len(fields))
	copy(dst.FieldIDs, fields)
	b.valueScratch = resizeIndex(b.valueScratch, len(constraints.Values))
	valueCount := 0
	for fieldRow, field := range fields {
		start := valueCount
		for constraintRow := range n {
			if constraints.Fields[constraintRow] != field {
				continue
			}
			valueStart := int(constraints.ValueStarts[constraintRow])
			valueEnd := valueStart + int(constraints.ValueCounts[constraintRow])
			valueCount += copy(b.valueScratch[valueCount:], constraints.Values[valueStart:valueEnd])
		}
		fieldValues := b.valueScratch[start:valueCount]
		slices.Sort(fieldValues)
		fieldValues = compactSymbols(fieldValues)
		valueCount = start + len(fieldValues)
		dst.FieldValueStarts[fieldRow] = uint32(start)
		dst.FieldValueCounts[fieldRow] = uint32(len(fieldValues))
	}
	dst.Values = resizeIndex(dst.Values, valueCount)
	copy(dst.Values, b.valueScratch[:valueCount])

	wordCount := int(words)
	dst.AllMask = resizeIndex(dst.AllMask, wordCount)
	for word := range dst.AllMask {
		dst.AllMask[word] = math.MaxUint64
	}
	if remainder := requirementCount & 63; remainder != 0 {
		dst.AllMask[wordCount-1] = uint64(1)<<remainder - 1
	}
	dst.WildcardMasks = resizeIndex(dst.WildcardMasks, len(fields)*wordCount)
	for fieldRow := range fields {
		copy(dst.WildcardMasks[fieldRow*wordCount:], dst.AllMask)
	}
	dst.ValueMasks = resizeIndex(dst.ValueMasks, valueCount*wordCount)

	for constraintRow := range n {
		fieldRow, found := slices.BinarySearch(dst.FieldIDs, constraints.Fields[constraintRow])
		if !found {
			panic("index: canonical field disappeared")
		}
		requirementRow := constraints.Rows[constraintRow]
		word := int(requirementRow >> 6)
		bit := uint64(1) << (requirementRow & 63)
		dst.WildcardMasks[fieldRow*wordCount+word] &^= bit

		fieldStart := int(dst.FieldValueStarts[fieldRow])
		fieldEnd := fieldStart + int(dst.FieldValueCounts[fieldRow])
		valueStart := int(constraints.ValueStarts[constraintRow])
		valueEnd := valueStart + int(constraints.ValueCounts[constraintRow])
		for _, value := range constraints.Values[valueStart:valueEnd] {
			relative, found := slices.BinarySearch(dst.Values[fieldStart:fieldEnd], value)
			if !found {
				panic("index: canonical value disappeared")
			}
			valueRow := fieldStart + relative
			dst.ValueMasks[valueRow*wordCount+word] |= bit
		}
	}
	for fieldRow := range fields {
		wildcard := dst.WildcardMasks[fieldRow*wordCount : (fieldRow+1)*wordCount]
		valueStart := int(dst.FieldValueStarts[fieldRow])
		valueEnd := valueStart + int(dst.FieldValueCounts[fieldRow])
		for valueRow := valueStart; valueRow < valueEnd; valueRow++ {
			mask := dst.ValueMasks[valueRow*wordCount : (valueRow+1)*wordCount]
			for word := range wordCount {
				mask[word] |= wildcard[word]
			}
		}
	}
	dst.RequirementCount = requirementCount
	dst.WordCount = uint32(words)
	return nil
}

// Clone returns an exact-capacity copy suitable for immutable publication.
func (p Policy) Clone() Policy {
	return Policy{
		FieldIDs:         cloneIndexExact(p.FieldIDs),
		FieldValueStarts: cloneIndexExact(p.FieldValueStarts),
		FieldValueCounts: cloneIndexExact(p.FieldValueCounts),
		WildcardMasks:    cloneIndexExact(p.WildcardMasks),
		Values:           cloneIndexExact(p.Values),
		ValueMasks:       cloneIndexExact(p.ValueMasks),
		AllMask:          cloneIndexExact(p.AllMask),
		RequirementCount: p.RequirementCount,
		WordCount:        p.WordCount,
	}
}

func (p Policy) validQueryShape(dst []uint64, fields []schema.FieldID, values []schema.SymbolID, present []uint8) bool {
	words := uint64(p.WordCount)
	if uint64(len(dst)) != words || len(fields) != len(values) || len(fields) != len(present) ||
		uint64(len(p.AllMask)) != words || p.WordCount != uint32(policyWordCount(p.RequirementCount)) ||
		len(p.FieldValueStarts) != len(p.FieldIDs) || len(p.FieldValueCounts) != len(p.FieldIDs) ||
		uint64(len(p.WildcardMasks)) != uint64(len(p.FieldIDs))*words ||
		uint64(len(p.ValueMasks)) != uint64(len(p.Values))*words {
		return false
	}
	for _, field := range fields {
		if field == 0 {
			return false
		}
	}
	for fieldRow := range p.FieldIDs {
		if p.FieldIDs[fieldRow] == 0 {
			return false
		}
		start := uint64(p.FieldValueStarts[fieldRow])
		end := start + uint64(p.FieldValueCounts[fieldRow])
		if end > uint64(len(p.Values)) {
			return false
		}
	}
	return true
}

// Candidates writes the conservative requirement-row mask for the supplied
// selector facts into dst. Missing selectors (present == 0) do not filter;
// present values absent from the index select only wildcard requirements.
func (p Policy) Candidates(dst []uint64, fields []schema.FieldID, values []schema.SymbolID, present []uint8) error {
	if !p.validQueryShape(dst, fields, values, present) {
		return ErrInvalidQuery
	}
	copy(dst, p.AllMask)
	words := int(p.WordCount)
	for selectorRow, field := range fields {
		if present[selectorRow] == 0 {
			continue
		}
		fieldRow, found := slices.BinarySearch(p.FieldIDs, field)
		if !found {
			continue
		}
		valueStart := int(p.FieldValueStarts[fieldRow])
		valueEnd := valueStart + int(p.FieldValueCounts[fieldRow])
		valueRow, valueFound := slices.BinarySearch(p.Values[valueStart:valueEnd], values[selectorRow])
		var mask []uint64
		if valueFound {
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
