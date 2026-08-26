package index

import (
	"slices"
	"strconv"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/simdops"
)

var batchIndexBenchmarkSink uint64

func BenchmarkBatchIndex(b *testing.B) {
	for _, distribution := range [...]string{"dense", "sparse"} {
		for _, rows := range [...]uint32{1, 16, 32, 63, 64, 256, 1024, 4096} {
			for _, uses := range [...]int{1, 2, 4, 8, 16, 32, 64, 80, 95, 96, 97, 112, 128} {
				name := distribution + "/rows=" + strconv.FormatUint(uint64(rows), 10) +
					"/uses=" + strconv.Itoa(uses)
				columns, spec := batchIndexBenchmarkData(rows, uses, distribution == "dense")
				words := int(factWordCount(rows))
				outputs := make([]uint64, uses*words)
				compareMask := make([]bool, rows)

				b.Run(name+"/direct", func(b *testing.B) {
					for value := 1; value <= uses; value++ {
						dst := outputs[(value-1)*words : value*words]
						simdops.CompareU32(compareMask, columns.Values, schema.SymbolID(value), simdops.Equal)
						simdops.PackMask(dst, compareMask)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						for value := 1; value <= uses; value++ {
							dst := outputs[(value-1)*words : value*words]
							simdops.CompareU32(compareMask, columns.Values, schema.SymbolID(value), simdops.Equal)
							simdops.PackMask(dst, compareMask)
						}
					}
					batchIndexBenchmarkSink ^= outputs[len(outputs)-1]
				})

				b.Run(name+"/indexed", func(b *testing.B) {
					var builder FactBuilder
					var index FactIndex
					if err := builder.Build(&index, &spec, columns); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if err := builder.Build(&index, &spec, columns); err != nil {
							b.Fatal(err)
						}
						for value := 1; value <= uses; value++ {
							dst := outputs[(value-1)*words : value*words]
							mask, found := index.Lookup(1, schema.SymbolID(value))
							if !found {
								b.Fatal("indexed field disappeared")
							}
							copy(dst, mask)
						}
					}
					batchIndexBenchmarkSink ^= outputs[len(outputs)-1]
				})
			}
		}
	}
}

func batchIndexBenchmarkData(rows uint32, uses int, dense bool) (SymbolColumns, FactSpec) {
	cardinality := 64
	if dense {
		cardinality = 2
	}
	programSymbols := max(cardinality, uses)
	values := make([]schema.SymbolID, rows)
	for row := range values {
		values[row] = schema.SymbolID(row%cardinality + 1)
	}
	queried := make([]schema.SymbolID, uses)
	for row := range queried {
		queried[row] = schema.SymbolID(row + 1)
	}
	slices.Sort(queried)
	return SymbolColumns{
		Values:             values,
		Rows:               rows,
		Count:              1,
		ProgramSymbolCount: uint32(programSymbols),
	}, FactSpec{
		FieldIDs:    []schema.FieldID{1},
		Columns:     []uint32{0},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{uint32(uses)},
		UseCounts:   []uint32{uint32(uses)},
		Values:      queried,
	}
}
