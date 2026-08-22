package jsonbatch

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
)

func BenchmarkDecodeBatch(b *testing.B) {
	p := fixtureDecoderProgram(b)
	requests := []byte(fixtures.RequestsJSON())
	evidence := []byte(fixtures.EvidenceJSON())
	var decoder Decoder
	var builder eval.Builder
	if _, err := decoder.Decode(&builder, p, requests, evidence, Limits{}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(requests) + len(evidence)))
	b.ResetTimer()
	for range b.N {
		if _, err := decoder.Decode(&builder, p, requests, evidence, Limits{}); err != nil {
			b.Fatal(err)
		}
	}
}
