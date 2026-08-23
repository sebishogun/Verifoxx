package jsonresult

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/buildinfo"
)

var benchmarkEncoded []byte

func BenchmarkEncoderAppend(b *testing.B) {
	fixture := loadEncodingFixture(b)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		b.Fatalf("Bind: %v", err)
	}
	dst := make([]byte, 0, len(fixture.golden))
	version := []byte(buildinfo.Version())
	if _, err := encoder.Append(dst, fixture.requestIDs, &fixture.batch, version); err != nil {
		b.Fatalf("prime Append: %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(fixture.golden)))
	b.ResetTimer()
	for b.Loop() {
		var err error
		benchmarkEncoded, err = encoder.Append(dst[:0], fixture.requestIDs, &fixture.batch, version)
		if err != nil {
			b.Fatal(err)
		}
	}
}
