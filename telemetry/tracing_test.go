package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRuntimeStartsFixedRedactedChildSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	runtime, err := New(context.Background(), validConfig(), WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	parent := runtime.Extract(context.Background(), carrier)
	ctx, span := runtime.Start(parent, OperationEvaluation)
	if !span.SpanContext().IsValid() {
		t.Fatal("Start returned invalid span context")
	}
	span.End()
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != "nornrune.evaluation" || got.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("span = name:%q parent:%s", got.Name, got.Parent.SpanID())
	}
	if len(got.Attributes) != 1 || string(got.Attributes[0].Key) != "nornrune.operation" || got.Attributes[0].Value.AsString() != "evaluation" {
		t.Fatalf("attributes = %v", got.Attributes)
	}
	if traceID := span.SpanContext().TraceID(); traceID != got.SpanContext.TraceID() || ctx == nil {
		t.Fatalf("span context mismatch: %s vs %s", traceID, got.SpanContext.TraceID())
	}
}

func TestOperationNamesAreFixed(t *testing.T) {
	want := []string{"admission", "decode", "policy_lookup", "evaluation", "audit_acknowledgment", "response_encode"}
	if int(OperationCount) != len(want) {
		t.Fatalf("OperationCount = %d, want %d", OperationCount, len(want))
	}
	for row, expected := range want {
		if got, ok := OperationName(Operation(row)); !ok || got != expected {
			t.Fatalf("OperationName(%d) = (%q, %t), want %q", row, got, ok, expected)
		}
	}
}
