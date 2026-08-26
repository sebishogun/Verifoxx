package index

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

func TestFactIndexBuildAndLookup(t *testing.T) {
	t.Run("exact masks and irrelevant values", func(t *testing.T) {
		for _, rows := range [...]uint32{0, 1, 63, 64, 65} {
			t.Run(factRowsName(rows), func(t *testing.T) {
				spec := factTestSpec()
				columns := factTestColumns(rows)
				var builder FactBuilder
				var got FactIndex
				if err := builder.Build(&got, &spec, columns); err != nil {
					t.Fatalf("Build: %v", err)
				}
				for _, query := range []struct {
					field schema.FieldID
					value schema.SymbolID
				}{
					{1, 1}, {1, 2}, {3, 3},
				} {
					mask, indexed := got.Lookup(query.field, query.value)
					if !indexed {
						t.Fatalf("Lookup(%d,%d) did not find indexed field", query.field, query.value)
					}
					column := spec.Columns[slices.Index(spec.FieldIDs, query.field)]
					want := factReferenceMask(columns, column, query.value)
					if !slices.Equal(mask, want) {
						t.Fatalf("Lookup(%d,%d) = %#x, want %#x", query.field, query.value, mask, want)
					}
					assertFactTailClean(t, mask, rows)
				}
				if mask, indexed := got.Lookup(1, 4); !indexed || mask != nil {
					t.Fatalf("extension Lookup = (%#x,%v), want (nil,true)", mask, indexed)
				}
				if mask, indexed := got.Lookup(2, 1); indexed || mask != nil {
					t.Fatalf("irrelevant Lookup = (%#x,%v), want (nil,false)", mask, indexed)
				}
			})
		}
	})

	t.Run("spec validation", func(t *testing.T) {
		var fields Schema
		if err := BuildSchema(&fields, []schema.ValueKind{
			schema.ValueKindSymbol,
			schema.ValueKindInteger,
			schema.ValueKindSymbol,
		}); err != nil {
			t.Fatal(err)
		}
		spec := factTestSpec()
		if !spec.Valid(fields, 3) {
			t.Fatal("valid spec rejected")
		}
		bad := spec.Clone()
		bad.Columns[1] = 0
		if bad.Valid(fields, 3) {
			t.Fatal("field-to-column mismatch accepted")
		}
	})

	t.Run("reuse clears poisoned words", func(t *testing.T) {
		spec := factTestSpec()
		large := factTestColumns(65)
		small := factTestColumns(3)
		var builder FactBuilder
		var got FactIndex
		if err := builder.Build(&got, &spec, large); err != nil {
			t.Fatal(err)
		}
		for i := range got.ValueMasks {
			got.ValueMasks[i] = math.MaxUint64
		}
		if err := builder.Build(&got, &spec, small); err != nil {
			t.Fatal(err)
		}
		for _, value := range [...]schema.SymbolID{1, 2} {
			mask, _ := got.Lookup(1, value)
			want := factReferenceMask(small, 0, value)
			if !slices.Equal(mask, want) {
				t.Fatalf("small value %d mask = %#x, want %#x", value, mask, want)
			}
			assertFactTailClean(t, mask, small.Rows)
		}
		if err := builder.Build(&got, &spec, large); err != nil {
			t.Fatal(err)
		}
		for _, value := range [...]schema.SymbolID{1, 2} {
			mask, _ := got.Lookup(1, value)
			want := factReferenceMask(large, 0, value)
			if !slices.Equal(mask, want) {
				t.Fatalf("rebuilt value %d mask = %#x, want %#x", value, mask, want)
			}
		}
	})

	t.Run("strided row view", func(t *testing.T) {
		spec := factTestSpec()
		columns := factTestColumns(129)
		wantValues := slices.Clone(columns.Values)
		wantData := &columns.Values[0]
		columns.Rows = 64
		columns.RowOffset = 64
		columns.RowStride = 129
		var builder FactBuilder
		var got FactIndex
		if err := builder.Build(&got, &spec, columns); err != nil {
			t.Fatalf("Build strided view: %v", err)
		}
		for _, query := range []struct {
			field schema.FieldID
			value schema.SymbolID
		}{{1, 1}, {1, 2}, {3, 3}} {
			mask, indexed := got.Lookup(query.field, query.value)
			if !indexed {
				t.Fatalf("Lookup(%d,%d) did not find indexed field", query.field, query.value)
			}
			column := spec.Columns[slices.Index(spec.FieldIDs, query.field)]
			want := factReferenceMask(columns, column, query.value)
			if !slices.Equal(mask, want) {
				t.Fatalf("Lookup(%d,%d) = %#x, want %#x", query.field, query.value, mask, want)
			}
		}
		if &columns.Values[0] != wantData || !slices.Equal(columns.Values, wantValues) {
			t.Fatal("strided Build mutated source storage")
		}
	})

	t.Run("invalid input is atomic", func(t *testing.T) {
		spec := factTestSpec()
		columns := factTestColumns(65)
		var builder FactBuilder
		var got FactIndex
		if err := builder.Build(&got, &spec, columns); err != nil {
			t.Fatal(err)
		}
		wantMasks := slices.Clone(got.ValueMasks)
		wantSpec, wantRows, wantWords := got.spec, got.Rows, got.WordCount

		tests := []struct {
			name    string
			mutate  func(*FactSpec)
			columns SymbolColumns
		}{
			{"misaligned fields", func(s *FactSpec) { s.Columns = s.Columns[:1] }, columns},
			{"unsorted fields", func(s *FactSpec) { s.FieldIDs[0], s.FieldIDs[1] = 3, 1 }, columns},
			{"duplicate fields", func(s *FactSpec) { s.FieldIDs[1] = 1 }, columns},
			{"wrong value start", func(s *FactSpec) { s.ValueStarts[1]++ }, columns},
			{"zero value count", func(s *FactSpec) { s.ValueCounts[0] = 0 }, columns},
			{"unsorted values", func(s *FactSpec) { s.Values[0], s.Values[1] = 2, 1 }, columns},
			{"duplicate values", func(s *FactSpec) { s.Values[1] = 1 }, columns},
			{"zero value", func(s *FactSpec) { s.Values[0] = 0 }, columns},
			{"symbol above catalog", func(s *FactSpec) { s.Values[2] = 4 }, columns},
			{"column out of range", func(s *FactSpec) { s.Columns[1] = 2 }, columns},
			{"short symbol slab", func(*FactSpec) {}, func() SymbolColumns {
				short := columns
				short.Values = short.Values[:len(short.Values)-1]
				return short
			}()},
			{"offset without stride", func(*FactSpec) {}, func() SymbolColumns {
				bad := columns
				bad.RowOffset = 1
				return bad
			}()},
			{"row range above stride", func(*FactSpec) {}, func() SymbolColumns {
				bad := columns
				bad.RowOffset = 64
				bad.RowStride = 65
				return bad
			}()},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				bad := spec.Clone()
				tc.mutate(&bad)
				if err := builder.Build(&got, &bad, tc.columns); !errors.Is(err, ErrInvalidFactIndex) {
					t.Fatalf("Build error = %v, want %v", err, ErrInvalidFactIndex)
				}
				if got.spec != wantSpec || got.Rows != wantRows || got.WordCount != wantWords ||
					!slices.Equal(got.ValueMasks, wantMasks) {
					t.Fatal("invalid Build mutated destination")
				}
			})
		}
	})

	t.Run("overflow helpers", func(t *testing.T) {
		if _, ok := factMaskLen(math.MaxUint32, math.MaxInt); ok {
			t.Fatal("oversized mask length accepted")
		}
		if _, ok := factColumnLen(math.MaxUint32, math.MaxUint32); ok {
			t.Fatal("oversized column length accepted")
		}
	})

	t.Run("warm build allocates zero", func(t *testing.T) {
		spec := factTestSpec()
		columns := factTestColumns(1024)
		var builder FactBuilder
		var got FactIndex
		if err := builder.Build(&got, &spec, columns); err != nil {
			t.Fatal(err)
		}
		var buildErr error
		if allocs := testing.AllocsPerRun(100, func() {
			buildErr = builder.Build(&got, &spec, columns)
		}); allocs != 0 {
			t.Fatalf("warm Build allocations = %g, want 0", allocs)
		}
		if buildErr != nil {
			t.Fatal(buildErr)
		}
	})
}

func factTestSpec() FactSpec {
	return FactSpec{
		FieldIDs:    []schema.FieldID{1, 3},
		Columns:     []uint32{0, 1},
		ValueStarts: []uint32{0, 2},
		ValueCounts: []uint32{2, 1},
		UseCounts:   []uint32{4, 2},
		Values:      []schema.SymbolID{1, 2, 3},
	}
}

func factTestColumns(rows uint32) SymbolColumns {
	values := make([]schema.SymbolID, int(rows)*2)
	for row := uint32(0); row < rows; row++ {
		if row%5 != 0 {
			values[row] = 1
			if row%3 == 0 {
				values[row] = 2
			}
		}
		if row%7 != 0 {
			values[int(rows)+int(row)] = 3
		}
	}
	if rows > 7 {
		values[7] = 4
	}
	return SymbolColumns{
		Values:             values,
		Rows:               rows,
		Count:              2,
		ProgramSymbolCount: 3,
	}
}

func factReferenceMask(columns SymbolColumns, column uint32, value schema.SymbolID) []uint64 {
	words := int((uint64(columns.Rows) + 63) >> 6)
	mask := make([]uint64, words)
	stride := columns.RowStride
	if stride == 0 {
		stride = columns.Rows
	}
	start := uint64(column)*uint64(stride) + uint64(columns.RowOffset)
	for row, got := range columns.Values[start : start+uint64(columns.Rows)] {
		if got == value {
			mask[row>>6] |= uint64(1) << (uint(row) & 63)
		}
	}
	return mask
}

func assertFactTailClean(t *testing.T, mask []uint64, rows uint32) {
	t.Helper()
	if len(mask) == 0 || rows&63 == 0 {
		return
	}
	valid := uint64(1)<<(rows&63) - 1
	if mask[len(mask)-1]&^valid != 0 {
		t.Fatalf("dirty tail bits: %#x", mask[len(mask)-1])
	}
}

func factRowsName(rows uint32) string {
	if rows == 0 {
		return "rows=0"
	}
	buf := [32]byte{'r', 'o', 'w', 's', '='}
	i := len(buf)
	for value := rows; value != 0; value /= 10 {
		i--
		buf[i] = byte('0' + value%10)
	}
	return string(append(buf[:5], buf[i:]...))
}
