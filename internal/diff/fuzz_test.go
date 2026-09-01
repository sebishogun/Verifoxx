package diff

import "testing"

func FuzzDomain(f *testing.F) {
	f.Add("field", uint8(2), true, uint64(2), uint32(2))
	f.Add("", uint8(0), false, uint64(0), uint32(0))
	f.Fuzz(func(t *testing.T, name string, options uint8, closed bool, budget uint64, batchRows uint32) {
		if options > 32 {
			options = 32
		}
		values := make([]Value, int(options))
		for row := range values {
			values[row] = Value{Kind: FieldKindBoolean, State: ValuePresent, Boolean: row&1 != 0}
		}
		if len(values) != 0 {
			values[0] = Value{Kind: FieldKindBoolean, State: ValueMissing}
		}
		domain := Domain{
			Fields:        []FieldDomain{{Name: name, Kind: FieldKindBoolean, Closed: closed, Values: values}},
			MaxCandidates: budget, BatchRows: batchRows,
		}
		cardinality, complete, err := domain.Validate()
		if err == nil {
			if cardinality != uint64(len(values)) || complete != closed || cardinality > budget {
				t.Fatalf("invalid successful validation: (%d,%v,%v)", cardinality, complete, err)
			}
		}
	})
}
