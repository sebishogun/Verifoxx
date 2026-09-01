package grpcapi

import (
	"context"
	"strings"
	"testing"
	"time"

	nornrunev1 "github.com/sebishogun/nornrune/api/gen/nornrune/v1"
	coreservice "github.com/sebishogun/nornrune/internal/service"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/metadata"
)

func TestGRPCPropagatesFixedRedactedTraceSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	runtime, err := publictelemetry.New(context.Background(), publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second,
		ExportQueueSize: 32, TraceSampleRatio: 1,
	}, publictelemetry.WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
		return []byte(`{"schema_version":1,"results":[]}`), nil
	}}
	harness := newGRPCTestHarness(t, api, nil, Config{
		MaxMessageBytes: 1 << 20, RequestTimeout: time.Second, Telemetry: runtime,
	})
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	))
	_, err = harness.client.EvaluateBatch(ctx, &nornrunev1.EvaluateBatchRequest{
		RequestsJson: []byte(`{"request":"protected-request-value"}`),
		EvidenceJson: []byte(`{"evidence":"protected-evidence-value"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"nornrune.admission": false, "nornrune.decode": false,
		"nornrune.evaluation": false, "nornrune.response_encode": false,
	}
	for _, span := range exporter.GetSpans() {
		if _, ok := want[span.Name]; ok {
			want[span.Name] = true
		}
		for _, attribute := range span.Attributes {
			value := attribute.Value.Emit()
			if value == "protected-request-value" || value == "protected-evidence-value" {
				t.Fatalf("span %q contains protected attribute %q", span.Name, value)
			}
		}
		if description := span.Status.Description; description != "" {
			for _, protected := range []string{"protected-request-value", "protected-evidence-value"} {
				if strings.Contains(description, protected) {
					t.Fatalf("span %q status description contains protected value %q", span.Name, protected)
				}
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing span %q", name)
		}
	}
}
