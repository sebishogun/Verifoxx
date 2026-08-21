package jsonpolicy

import "testing"

// BenchmarkDecodeFullPolicyFresh measures the package-level Decode entry
// point with a newly sized builder per iteration, so every allocation the
// decode path needs is counted. Fixture and schema setup happen before the
// timer.
func BenchmarkDecodeFullPolicyFresh(b *testing.B) {
	source := readPolicyFixture(b, "valid-full.json")
	fields := testSchema(b)
	symbols := testInterner(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder := policyBuilder(len(source))
		if err := Decode(builder, source, fields, symbols, Limits{}); err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkDecoderFullPolicyReuse measures the reusable Decoder on one
// generously hinted builder primed once outside the timer. Warm iterations
// must allocate 0 B/op with 0 allocs/op.
func BenchmarkDecoderFullPolicyReuse(b *testing.B) {
	source := readPolicyFixture(b, "valid-full.json")
	fields := testSchema(b)
	symbols := testInterner(b)
	builder := policyBuilder(len(source))
	var dec Decoder
	if err := dec.Decode(builder, source, fields, symbols, Limits{}); err != nil {
		b.Fatalf("prime decode: %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := dec.Decode(builder, source, fields, symbols, Limits{}); err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}
