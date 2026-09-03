package grpcapi

import (
	"context"
	"strings"
	"testing"
	"time"

	nornrunev1 "github.com/sebishogun/nornrune/api/gen/nornrune/v1"
	coreservice "github.com/sebishogun/nornrune/internal/service"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
		_, custom := want[span.Name]
		if custom {
			want[span.Name] = true
			operation, _ := strings.CutPrefix(span.Name, "nornrune.")
			attributes := make(map[string]string, len(span.Attributes))
			for _, value := range span.Attributes {
				attributes[string(value.Key)] = value.Value.AsString()
			}
			if len(attributes) != 3 || attributes["nornrune.operation"] != operation ||
				attributes["nornrune.transport"] != "grpc" || attributes["nornrune.status"] != "ok" {
				t.Fatalf("span %q attributes = %v", span.Name, attributes)
			}
			if span.Status.Code != otelcodes.Ok || span.Status.Description != "" {
				t.Fatalf("span %q status = %+v", span.Name, span.Status)
			}
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

func TestGRPCRedactsBackendStatusFromClientAndTrace(t *testing.T) {
	const protected = "protected backend detail"
	exporter := tracetest.NewInMemoryExporter()
	runtime, err := publictelemetry.New(context.Background(), publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second,
		ExportQueueSize: 32, TraceSampleRatio: 1,
	}, publictelemetry.WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
		return nil, status.Error(codes.Internal, protected)
	}}
	harness := newGRPCTestHarness(t, api, nil, Config{
		MaxMessageBytes: 1 << 20, RequestTimeout: time.Second, Telemetry: runtime,
	})
	_, err = harness.client.EvaluateBatch(context.Background(), validEvaluationRequest())
	clientStatus := status.Convert(err)
	if clientStatus.Code() != codes.Internal || clientStatus.Message() != "request failed" || strings.Contains(clientStatus.Message(), protected) {
		t.Fatalf("client status exposed backend detail: %v", clientStatus)
	}
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	foundFixedStatus := false
	foundEvaluationStatus := false
	for _, span := range exporter.GetSpans() {
		if strings.Contains(span.Status.Description, protected) {
			t.Fatalf("span %q status contains backend detail %q", span.Name, protected)
		}
		foundFixedStatus = foundFixedStatus || span.Status.Description == "request failed"
		if span.Name == "nornrune.evaluation" {
			attributes := make(map[string]string, len(span.Attributes))
			for _, value := range span.Attributes {
				attributes[string(value.Key)] = value.Value.AsString()
			}
			if len(attributes) != 3 || attributes["nornrune.operation"] != "evaluation" ||
				attributes["nornrune.transport"] != "grpc" || attributes["nornrune.status"] != "internal" ||
				span.Status.Code != otelcodes.Error || span.Status.Description != "internal" {
				t.Fatalf("evaluation failure span = attributes:%v status:%+v", attributes, span.Status)
			}
			foundEvaluationStatus = true
		}
	}
	if !foundFixedStatus {
		t.Fatal("no exported span carried the fixed error status")
	}
	if !foundEvaluationStatus {
		t.Fatal("no evaluation span carried the bounded internal status")
	}
}
