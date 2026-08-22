package eval

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func BenchmarkBatchBuilder(b *testing.B) {
	p := batchTestProgramWithSymbol(b, "known",
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	)
	var builder Builder
	const rows = uint32(64)
	offsets := make([]uint32, rows+1)
	refs := make([]uint32, rows)
	for row := uint32(0); row < rows; row++ {
		offsets[row] = row
		refs[row] = row & 1
	}
	offsets[rows] = rows
	unknown := []byte("unknown")

	build := func() {
		if err := builder.Begin(p, rows, 2, rows); err != nil {
			b.Fatal(err)
		}
		symbol, err := builder.InternSymbol(unknown)
		if err != nil {
			b.Fatal(err)
		}
		for row := uint32(0); row < rows; row++ {
			if err = builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
				b.Fatal(err)
			}
			if err = builder.SetSymbol(row, 1, symbol); err != nil {
				b.Fatal(err)
			}
			if err = builder.SetInteger(row, 2, int64(row)); err != nil {
				b.Fatal(err)
			}
			if err = builder.SetBoolean(row, 3, row&1 != 0); err != nil {
				b.Fatal(err)
			}
			if err = builder.SetTimestamp(row, 4, int64(row)); err != nil {
				b.Fatal(err)
			}
			if err = builder.SetPresent(row, 5); err != nil {
				b.Fatal(err)
			}
		}
		if err = builder.SetEvidence(0, EvidenceRecord{ID: 1, Kind: 1, State: 1, Subject: symbol}); err != nil {
			b.Fatal(err)
		}
		if err = builder.SetEvidence(1, EvidenceRecord{ID: 2, Kind: 1, State: 1, Subject: symbol}); err != nil {
			b.Fatal(err)
		}
		if err = builder.SetEvidenceCSR(offsets, refs); err != nil {
			b.Fatal(err)
		}
		if _, err = builder.Finish(); err != nil {
			b.Fatal(err)
		}
	}

	build()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		build()
	}
}
