package observability

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	publictelemetry "github.com/sebishogun/nornrune/telemetry"
)

func TestCumulativeBucketsSaturateInsteadOfWrapping(t *testing.T) {
	var values [publictelemetry.LatencyBucketCount]uint64
	values[0] = math.MaxUint64
	values[1] = 1
	buckets := cumulativeBuckets(values)
	bounds := publictelemetry.DurationBucketBounds()
	if got := buckets[bounds[1].Seconds()]; got != math.MaxUint64 {
		t.Fatalf("second cumulative bucket = %d, want saturation", got)
	}
}

func TestMetricsExposeBatchAggregates(t *testing.T) {
	t.Parallel()

	var queueDepth atomic.Uint64
	var journalFailures atomic.Uint64
	metrics, err := NewMetrics(MetricsConfig{
		QueueDepth:      queueDepth.Load,
		JournalFailures: journalFailures.Load,
		SIMDTier:        "avx2",
		Workers:         4,
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	queueDepth.Store(3)
	journalFailures.Store(2)
	observation := BatchObservation{Rows: 10, Duration: 1500 * time.Microsecond}
	observation.Outcomes[OutcomeApprove] = 4
	observation.Outcomes[OutcomeReject] = 3
	observation.Outcomes[OutcomeRevise] = 2
	observation.Outcomes[OutcomeEscalate] = 1
	if err := metrics.ObserveBatch(observation); err != nil {
		t.Fatalf("ObserveBatch() error = %v", err)
	}
	failed := BatchObservation{Rows: 5, Failed: true, Duration: 500 * time.Microsecond}
	failed.Outcomes[OutcomeApprove] = 2
	failed.Outcomes[OutcomeReject] = 1
	if err := metrics.ObserveBatch(failed); err != nil {
		t.Fatalf("ObserveBatch(failed) error = %v", err)
	}
	events := publictelemetry.BatchDelta{}
	events.Reasons[publictelemetry.ReasonMissing] = 2
	events.Audits[publictelemetry.AuditPersisted] = 1
	events.Reloads[publictelemetry.ReloadSuccess] = 1
	if err := metrics.Record(events); err != nil {
		t.Fatalf("Record(events) error = %v", err)
	}

	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("metrics Content-Type = %q", contentType)
	}
	assertMetricContains(t, response.Body.String(),
		"nornrune_evaluation_batches_total 2",
		"nornrune_evaluation_batch_failures_total 1",
		"nornrune_evaluation_rows_total 15",
		"nornrune_evaluation_outcomes_total{outcome=\"approve\"} 6",
		"nornrune_evaluation_outcomes_total{outcome=\"reject\"} 4",
		"nornrune_evaluation_outcomes_total{outcome=\"revise\"} 2",
		"nornrune_evaluation_outcomes_total{outcome=\"escalate\"} 1",
		"nornrune_evaluation_escalations_total{reason=\"missing\"} 2",
		"nornrune_audit_outcomes_total{outcome=\"persisted\"} 1",
		"nornrune_policy_reloads_total{outcome=\"success\"} 1",
		"nornrune_telemetry_export_drops_total 0",
		"nornrune_shutdown_failures_total 0",
		"nornrune_evaluation_duration_seconds_count 2",
		"nornrune_evaluation_duration_seconds_sum 0.002",
		"nornrune_service_queue_depth 3",
		"nornrune_audit_journal_failures_total 2",
		"nornrune_evaluation_workers 4",
		"nornrune_simd_tier_info{tier=\"avx2\"} 1",
	)
}

func TestMetricsExposeOnlyFixedCardinalityLabels(t *testing.T) {
	metrics, err := NewMetrics(MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	exposition := response.Body.String()
	for prefix, count := range map[string]int{
		"nornrune_evaluation_outcomes_total{":    int(publictelemetry.DecisionCount),
		"nornrune_evaluation_escalations_total{": int(publictelemetry.ReasonCount),
		"nornrune_audit_outcomes_total{":         int(publictelemetry.AuditOutcomeCount),
		"nornrune_policy_reloads_total{":         int(publictelemetry.ReloadOutcomeCount),
	} {
		if got := strings.Count(exposition, prefix); got != count {
			t.Fatalf("%s series = %d, want %d\n%s", prefix, got, count, exposition)
		}
	}
	for _, forbidden := range []string{"request_id", "evidence", "policy_name", "policy_hash", "user", "url", "error"} {
		if strings.Contains(exposition, forbidden+"=") {
			t.Fatalf("metrics contain forbidden label %q\n%s", forbidden, exposition)
		}
	}
}

func TestMetricsCanCollectSharedTelemetryRuntime(t *testing.T) {
	runtime, err := publictelemetry.New(context.Background(), publictelemetry.Config{
		Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := NewMetrics(MetricsConfig{
		Runtime: runtime, QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	delta := publictelemetry.BatchDelta{Batches: 1, Rows: 1}
	delta.Decisions[publictelemetry.DecisionApprove] = 1
	if err := runtime.Record(delta); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertMetricContains(t, response.Body.String(), "nornrune_evaluation_rows_total 1")
}

func TestMetricsRejectInvalidConfigurationAndBatches(t *testing.T) {
	t.Parallel()

	valid := MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	}
	tests := []struct {
		name   string
		config MetricsConfig
	}{
		{name: "zero config"},
		{name: "missing queue source", config: MetricsConfig{
			JournalFailures: valid.JournalFailures, SIMDTier: valid.SIMDTier, Workers: valid.Workers,
		}},
		{name: "missing journal source", config: MetricsConfig{
			QueueDepth: valid.QueueDepth, SIMDTier: valid.SIMDTier, Workers: valid.Workers,
		}},
		{name: "missing SIMD tier", config: MetricsConfig{
			QueueDepth: valid.QueueDepth, JournalFailures: valid.JournalFailures, Workers: valid.Workers,
		}},
		{name: "invalid SIMD tier", config: MetricsConfig{
			QueueDepth: valid.QueueDepth, JournalFailures: valid.JournalFailures, SIMDTier: "\xff", Workers: valid.Workers,
		}},
		{name: "zero workers", config: MetricsConfig{
			QueueDepth: valid.QueueDepth, JournalFailures: valid.JournalFailures, SIMDTier: valid.SIMDTier,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics, err := NewMetrics(test.config)
			if err == nil || metrics != nil {
				t.Fatalf("NewMetrics() = (%p, %v), want nil error", metrics, err)
			}
		})
	}

	metrics, err := NewMetrics(valid)
	if err != nil {
		t.Fatalf("NewMetrics(valid) error = %v", err)
	}
	invalid := []BatchObservation{
		{},
		{Rows: 1, Duration: -time.Nanosecond, Outcomes: [OutcomeCount]uint64{1}},
		{Rows: 2, Outcomes: [OutcomeCount]uint64{1}},
		{Failed: true, Outcomes: [OutcomeCount]uint64{1}},
	}
	for _, observation := range invalid {
		if err := metrics.ObserveBatch(observation); err == nil {
			t.Fatalf("ObserveBatch(%+v) error = nil", observation)
		}
	}
	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assertMetricContains(t, response.Body.String(), "nornrune_evaluation_batches_total 0")
}

func BenchmarkMetricsObserveBatch(b *testing.B) {
	metrics, err := NewMetrics(MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		b.Fatalf("NewMetrics() error = %v", err)
	}
	observation := BatchObservation{Rows: 4, Duration: time.Millisecond}
	for index := range observation.Outcomes {
		observation.Outcomes[index] = 1
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := metrics.ObserveBatch(observation); err != nil {
			b.Fatalf("ObserveBatch() error = %v", err)
		}
	}
}

func assertMetricContains(t *testing.T, exposition string, expected ...string) {
	t.Helper()
	for _, value := range expected {
		if !strings.Contains(exposition, value+"\n") {
			t.Errorf("metrics exposition does not contain %q\n%s", value, exposition)
		}
	}
}

func BenchmarkMetricsScrape(b *testing.B) {
	metrics, err := NewMetrics(MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: "scalar", Workers: 1,
	})
	if err != nil {
		b.Fatalf("NewMetrics() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := discardResponse{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		metrics.ServeHTTP(response, request)
	}
}

type discardResponse struct {
	header http.Header
}

func (response discardResponse) Header() http.Header   { return response.header }
func (discardResponse) Write(body []byte) (int, error) { return len(body), nil }
func (discardResponse) WriteHeader(int)                {}
