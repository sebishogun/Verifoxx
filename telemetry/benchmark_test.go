package telemetry

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func benchmarkDelta() BatchDelta {
	delta := BatchDelta{Batches: 1, Rows: 256, Duration: time.Millisecond}
	for row := range delta.Decisions {
		delta.Decisions[row] = 64
	}
	return delta
}

func BenchmarkTelemetryDisabledRecord(b *testing.B) {
	runtime, err := New(context.Background(), Config{})
	if err != nil {
		b.Fatal(err)
	}
	delta := benchmarkDelta()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := runtime.Record(delta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTelemetryCountersRecord(b *testing.B) {
	runtime, err := New(context.Background(), validConfig())
	if err != nil {
		b.Fatal(err)
	}
	delta := benchmarkDelta()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := runtime.Record(delta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTelemetrySnapshot(b *testing.B) {
	runtime, err := New(context.Background(), validConfig())
	if err != nil {
		b.Fatal(err)
	}
	_ = runtime.Record(benchmarkDelta())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if snapshot := runtime.Snapshot(); snapshot.Rows != 256 {
			b.Fatal("unexpected snapshot")
		}
	}
}

func benchmarkSpan(b *testing.B, ratio float64) {
	exporter := tracetest.NewInMemoryExporter()
	config := validConfig()
	config.TraceSampleRatio = ratio
	runtime, err := New(context.Background(), config, WithSpanExporter(exporter))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, span := runtime.Start(ctx, OperationEvaluation, TransportService)
		runtime.Finish(span, SpanStatusOK)
	}
}

func BenchmarkTelemetrySampledSpan(b *testing.B) {
	benchmarkSpan(b, 0.1)
}

func BenchmarkTelemetryForcedSpan(b *testing.B) {
	benchmarkSpan(b, 1.0)
}

func BenchmarkTelemetryNoopSpan(b *testing.B) {
	// Counters-only runtime: no endpoint and no span exporter, so Start must
	// return the cached no-op tracer without allocating.
	runtime, err := New(context.Background(), validConfig())
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, span := runtime.Start(ctx, OperationEvaluation, TransportService)
		runtime.Finish(span, SpanStatusOK)
	}
}
