package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
		"nornrune_evaluation_duration_seconds_count 2",
		"nornrune_evaluation_duration_seconds_sum 0.002",
		"nornrune_service_queue_depth 3",
		"nornrune_audit_journal_failures_total 2",
		"nornrune_evaluation_workers 4",
		"nornrune_simd_tier_info{tier=\"avx2\"} 1",
	)
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
