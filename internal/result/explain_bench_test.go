package result

import (
	"math"
	"testing"
)

func BenchmarkExplainer(b *testing.B) {
	fixture := newExplainerFixture(b)
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		b.Fatalf("Bind: %v", err)
	}
	var dst Materialized
	if err := explainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32); err != nil {
		b.Fatalf("prime Materialize: %v", err)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(len(dst.Bytes)), "bytes")
	b.ResetTimer()
	for range b.N {
		if err := explainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32); err != nil {
			b.Fatal(err)
		}
	}
}
