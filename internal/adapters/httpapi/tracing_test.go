package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/observability"
	coreservice "github.com/sebishogun/nornrune/internal/service"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
	"go.opentelemetry.io/otel/codes"
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
	const protectedBackendDetail = "protected backend detail"
	failEvaluation := false
	api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
		if failEvaluation {
			return nil, fmt.Errorf("%w: %s", coreservice.ErrUnavailable, protectedBackendDetail)
		}
		return []byte(`{"schema_version":1,"results":[]}` + "\n"), nil
	}}
	server, err := New(api, gate, metrics, Config{MaxBodyBytes: 1 << 20, RequestTimeout: time.Second, Telemetry: runtime})
	if err != nil {
		t.Fatal(err)
	}
	body := evaluationEnvelope("", fixtures.RequestsJSON(), fixtures.EvidenceJSON())
	request := httptest.NewRequest(http.MethodPost, "/v1/evaluate", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	request.Header.Set("traceparent", traceparent)
	request.Header.Set("tracestate", "vendor=value")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("traceparent"); got != traceparent {
		t.Fatalf("response traceparent = %q, want %q", got, traceparent)
	}
	if got := response.Header().Get("tracestate"); got != "vendor=value" {
		t.Fatalf("response tracestate = %q, want vendor=value", got)
	}

	earlyRequest := httptest.NewRequest(http.MethodGet, "/livez?invalid=true", nil)
	earlyRequest.Header.Set("traceparent", traceparent)
	earlyResponse := httptest.NewRecorder()
	server.ServeHTTP(earlyResponse, earlyRequest)
	if earlyResponse.Code != http.StatusBadRequest || earlyResponse.Header().Get("traceparent") != traceparent {
		t.Fatalf("early response = %d traceparent %q", earlyResponse.Code, earlyResponse.Header().Get("traceparent"))
	}

	malformedRequest := httptest.NewRequest(http.MethodGet, "/livez", nil)
	malformedRequest.Header.Set("traceparent", "malformed")
	malformedResponse := httptest.NewRecorder()
	server.ServeHTTP(malformedResponse, malformedRequest)
	if got := malformedResponse.Header().Get("traceparent"); got != "" {
		t.Fatalf("malformed traceparent was reflected as %q", got)
	}

	failEvaluation = true
	failureRequest := httptest.NewRequest(http.MethodPost, "/v1/evaluate", strings.NewReader(body))
	failureRequest.Header.Set("Content-Type", "application/json")
	failureRequest.Header.Set("traceparent", traceparent)
	failureResponse := httptest.NewRecorder()
	server.ServeHTTP(failureResponse, failureRequest)
	if failureResponse.Code != http.StatusServiceUnavailable || strings.Contains(failureResponse.Body.String(), protectedBackendDetail) {
		t.Fatalf("failure response = %d %s", failureResponse.Code, failureResponse.Body.String())
	}
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	want := map[string]bool{
		"nornrune.admission": false, "nornrune.decode": false,
		"nornrune.evaluation": false, "nornrune.response_encode": false,
	}
	foundBoundedFailure := false
	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	for _, span := range spans {
		operation, custom := strings.CutPrefix(span.Name, "nornrune.")
		if custom {
			if _, ok := want[span.Name]; ok {
				want[span.Name] = true
			}
			attributes := make(map[string]string, len(span.Attributes))
			for _, value := range span.Attributes {
				attributes[string(value.Key)] = value.Value.AsString()
			}
			if len(attributes) != 3 || attributes["nornrune.operation"] != operation || attributes["nornrune.transport"] != "http" {
				t.Fatalf("span %q attributes = %v", span.Name, attributes)
			}
			switch attributes["nornrune.status"] {
			case "ok":
				if span.Status.Code != codes.Ok || span.Status.Description != "" {
					t.Fatalf("span %q status = %+v", span.Name, span.Status)
				}
			case "unavailable":
				if span.Name != "nornrune.evaluation" || span.Status.Code != codes.Error || span.Status.Description != "unavailable" {
					t.Fatalf("span %q failure status = %+v", span.Name, span.Status)
				}
				foundBoundedFailure = true
			default:
				t.Fatalf("span %q bounded status = %q", span.Name, attributes["nornrune.status"])
			}
		}
		if traceID := span.SpanContext.TraceID().String(); traceID != incomingTraceID {
			t.Errorf("span %q trace ID = %s, want incoming traceparent %s", span.Name, traceID, incomingTraceID)
		}
		encoded := span.Name + span.Status.Description
		for _, attribute := range span.Attributes {
			encoded += string(attribute.Key) + attribute.Value.Emit()
		}
		for _, protected := range []string{fixtures.RequestsJSON(), fixtures.EvidenceJSON(), body, "traceparent", protectedBackendDetail} {
			if strings.Contains(encoded, protected) {
				t.Fatalf("span contains protected value %q: %s", protected, encoded)
			}
		}
	}
	if !foundBoundedFailure {
		t.Fatal("evaluation failure span did not carry bounded unavailable status")
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing span %q; got %v", name, spans)
		}
	}
}
