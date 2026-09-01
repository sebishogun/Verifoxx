package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/observability"
	coreservice "github.com/sebishogun/nornrune/internal/service"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEvaluatePropagatesFixedRedactedTraceSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	runtime, err := publictelemetry.New(context.Background(), publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second,
		ExportQueueSize: 32, TraceSampleRatio: 1,
	}, publictelemetry.WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	gate, err := coreservice.New(4)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 }, SIMDTier: "test", Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
		return []byte(`{"schema_version":1,"results":[]}` + "\n"), nil
	}}
	server, err := New(api, gate, metrics, Config{MaxBodyBytes: 1 << 20, RequestTimeout: time.Second, Telemetry: runtime})
	if err != nil {
		t.Fatal(err)
	}
	body := evaluationEnvelope("", fixtures.RequestsJSON(), fixtures.EvidenceJSON())
	request := httptest.NewRequest(http.MethodPost, "/v1/evaluate", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	want := map[string]bool{
		"nornrune.admission": false, "nornrune.decode": false,
		"nornrune.evaluation": false, "nornrune.response_encode": false,
	}
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	for _, span := range spans {
		if _, ok := want[span.Name]; ok {
			want[span.Name] = true
		}
		if traceID := span.SpanContext.TraceID().String(); traceID != incomingTraceID {
			t.Errorf("span %q trace ID = %s, want incoming traceparent %s", span.Name, traceID, incomingTraceID)
		}
		encoded := span.Name + span.Status.Description
		for _, attribute := range span.Attributes {
			encoded += string(attribute.Key) + attribute.Value.Emit()
		}
		for _, protected := range []string{fixtures.RequestsJSON(), fixtures.EvidenceJSON(), body, "traceparent"} {
			if strings.Contains(encoded, protected) {
				t.Fatalf("span contains protected value %q: %s", protected, encoded)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing span %q; got %v", name, spans)
		}
	}
}
