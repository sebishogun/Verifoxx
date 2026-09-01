package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/buildinfo"
	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/observability"
	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/security"
	"github.com/sebishogun/nornrune/internal/service"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTelemetryTestEngine(t *testing.T, runtime *publictelemetry.Runtime) (*Engine, *captureJournal) {
	t.Helper()
	capacity := persistence.AuditCapacity{Bytes: 64 << 10, Requests: 16, Evidence: 16, Rows: 16, EvidenceLinks: 64}
	store := &memoryPolicyStore{}
	registry := &program.Registry{}
	publisher, err := persistence.NewPublisher(store, registry, compilePolicySource, buildinfo.Version())
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	journal := &captureJournal{capacity: capacity}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		Runtime:    runtime,
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	engine, err := NewEngine(EngineConfig{
		Registry: registry, Publisher: publisher, Journal: journal, Metrics: metrics, Telemetry: runtime,
		Health: func(context.Context) error { return nil }, EngineVersion: buildinfo.Version(),
		AuditMode: persistence.AuditRequired, AuditCapacity: capacity, Limits: security.DefaultLimits(), Workers: 1, QueueDepth: 1,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() { _ = engine.Close(context.Background()) })
	return engine, journal
}

func TestEngineRecordsTelemetryForReloadsEvaluationAndAudit(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	config := publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 8,
		TraceSampleRatio: 1,
	}
	runtime, err := publictelemetry.New(context.Background(), config, publictelemetry.WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	engine, journal := newTelemetryTestEngine(t, runtime)

	if _, err := engine.CompilePolicy(context.Background(), []byte(nornrune.Source())); err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	if _, err := engine.EvaluateBatch(context.Background(), service.EvaluationRequest{
		Requests: []byte(fixtures.RequestsJSON()), Evidence: []byte(fixtures.EvidenceJSON()),
	}, nil); err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	if _, err := engine.CompilePolicy(context.Background(), []byte(`{"schema_version":1,"policies":[}`)); err == nil {
		t.Fatal("CompilePolicy(invalid) error = nil")
	}

	snapshot := runtime.Snapshot()
	if snapshot.Reloads[publictelemetry.ReloadSuccess] != 1 || snapshot.Reloads[publictelemetry.ReloadInvalid] != 1 {
		t.Fatalf("reload outcomes = %+v", snapshot.Reloads)
	}
	if snapshot.Batches != 1 || snapshot.Rows != 5 {
		t.Fatalf("batch telemetry = batches:%d rows:%d", snapshot.Batches, snapshot.Rows)
	}
	var decisions uint64
	for _, count := range snapshot.Decisions {
		decisions += count
	}
	if decisions != 5 {
		t.Fatalf("decision total = %d, want 5", decisions)
	}
	escalations := snapshot.Decisions[publictelemetry.DecisionEscalate]
	if escalations == 0 {
		t.Fatalf("no escalations recorded: %+v", snapshot.Decisions)
	}
	var reasons uint64
	for _, count := range snapshot.Reasons {
		reasons += count
	}
	if reasons < escalations {
		t.Fatalf("escalation reasons = %d, want at least %d", reasons, escalations)
	}
	if snapshot.Audits[publictelemetry.AuditPersisted] != 1 || journal.calls != 1 {
		t.Fatalf("audit outcomes = %+v, journal calls = %d", snapshot.Audits, journal.calls)
	}
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := map[string]bool{}
	for _, span := range exporter.GetSpans() {
		spans[span.Name] = true
	}
	for _, name := range []string{"nornrune.policy_lookup", "nornrune.audit_acknowledgment"} {
		if !spans[name] {
			t.Errorf("engine did not emit span %q; got %v", name, spans)
		}
	}

	response := httptest.NewRecorder()
	metrics := observabilityMetricsForRuntime(t, runtime)
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		"nornrune_evaluation_rows_total 5", "nornrune_policy_reloads_total{outcome=\"success\"} 1",
		"nornrune_audit_outcomes_total{outcome=\"persisted\"} 1",
	} {
		if !containsLine(body, expected) {
			t.Fatalf("metrics exposition missing %q\n%s", expected, body)
		}
	}
}

func TestUnavailableExporterNeverBlocksEvaluationOrAudit(t *testing.T) {
	var requests atomic.Uint64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	runtime, err := publictelemetry.New(context.Background(), publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Hour, ExportQueueSize: 8,
		Endpoint: collector.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, journal := newTelemetryTestEngine(t, runtime)
	if _, err := engine.CompilePolicy(context.Background(), []byte(nornrune.Source())); err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	output, err := engine.EvaluateBatch(context.Background(), service.EvaluationRequest{
		Requests: []byte(fixtures.RequestsJSON()), Evidence: []byte(fixtures.EvidenceJSON()),
	}, nil)
	if err != nil {
		t.Fatalf("EvaluateBatch() with unavailable collector error = %v", err)
	}
	if len(output) == 0 {
		t.Fatal("EvaluateBatch() output is empty")
	}
	if time.Now().After(deadline) {
		t.Fatal("evaluation blocked on telemetry export")
	}
	if journal.calls != 1 {
		t.Fatalf("required audit submissions = %d, want 1", journal.calls)
	}
	if snapshot := runtime.Snapshot(); snapshot.Rows != 5 {
		t.Fatalf("telemetry rows = %d, want 5", snapshot.Rows)
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.ForceFlush(flushCtx); err == nil {
		t.Fatal("ForceFlush() against failing collector error = nil")
	}
	if got := runtime.Snapshot().ExportDrops; got == 0 {
		t.Fatal("failed export did not increment drop counter")
	}
}

func observabilityMetricsForRuntime(t *testing.T, runtime *publictelemetry.Runtime) *observability.Metrics {
	t.Helper()
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		Runtime:    runtime,
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	return metrics
}

func containsLine(body, expected string) bool {
	for len(body) > 0 {
		line := body
		if index := indexByte(body, '\n'); index >= 0 {
			line, body = body[:index], body[index+1:]
		} else {
			body = ""
		}
		if line == expected {
			return true
		}
	}
	return false
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
