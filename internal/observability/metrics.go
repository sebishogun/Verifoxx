// Package observability exposes bounded service telemetry outside evaluator kernels.
package observability

import (
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	ErrInvalidMetrics     = errors.New("observability: invalid metrics configuration")
	ErrInvalidObservation = errors.New("observability: invalid batch observation")
)

// Outcome identifies one bounded decision label.
type Outcome uint8

const (
	OutcomeApprove Outcome = iota
	OutcomeReject
	OutcomeRevise
	OutcomeEscalate
	OutcomeCount
)

var outcomeNames = [OutcomeCount]string{"approve", "reject", "revise", "escalate"}

// MetricsConfig binds concurrency-safe scrape-time sources and immutable
// runtime metadata. JournalFailures must be monotonic.
type MetricsConfig struct {
	QueueDepth      func() uint64
	JournalFailures func() uint64
	SIMDTier        string
	Workers         uint32
}

// BatchObservation carries one batch update, never a row or node update.
// Successful batches require one outcome per nonzero row. Failed batches may
// report zero rows or a subset of known outcomes.
type BatchObservation struct {
	Outcomes [OutcomeCount]uint64
	Rows     uint64
	Duration time.Duration
	Failed   bool
}

// Metrics owns a private Prometheus registry and fixed-label batch collectors.
type Metrics struct {
	handler  http.Handler
	batches  prometheus.Counter
	failures prometheus.Counter
	rows     prometheus.Counter
	duration prometheus.Histogram
	outcomes [OutcomeCount]prometheus.Counter
}

// NewMetrics allocates collectors once and rejects dynamic label sources.
func NewMetrics(config MetricsConfig) (*Metrics, error) {
	if config.QueueDepth == nil || config.JournalFailures == nil || config.Workers == 0 ||
		len(config.SIMDTier) == 0 || len(config.SIMDTier) > 128 || !utf8.ValidString(config.SIMDTier) {
		return nil, ErrInvalidMetrics
	}
	metrics := &Metrics{
		batches: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "verifoxx", Subsystem: "evaluation", Name: "batches_total",
			Help: "Policy evaluation batches observed, including failures.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "verifoxx", Subsystem: "evaluation", Name: "batch_failures_total",
			Help: "Policy evaluation batches reported as failed.",
		}),
		rows: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "verifoxx", Subsystem: "evaluation", Name: "rows_total",
			Help: "Request rows presented to completed or failed policy evaluation batches.",
		}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "verifoxx", Subsystem: "evaluation", Name: "duration_seconds",
			Help:    "End-to-end policy evaluation batch duration.",
			Buckets: []float64{0.00001, 0.0001, 0.001, 0.01, 0.1, 1, 10},
		}),
	}
	outcomes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "verifoxx", Subsystem: "evaluation", Name: "outcomes_total",
		Help: "Policy decisions completed in evaluation batches.",
	}, []string{"outcome"})
	for outcome, name := range outcomeNames {
		metrics.outcomes[outcome] = outcomes.WithLabelValues(name)
	}
	queueDepth := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "verifoxx", Subsystem: "service", Name: "queue_depth",
		Help: "Requests currently waiting for service admission.",
	}, func() float64 { return float64(config.QueueDepth()) })
	journalFailures := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "verifoxx", Subsystem: "audit", Name: "journal_failures_total",
		Help: "Audit journal batches that failed to persist.",
	}, func() float64 { return float64(config.JournalFailures()) })
	workers := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "verifoxx", Subsystem: "evaluation", Name: "workers",
		Help: "Configured evaluator worker count.",
	})
	workers.Set(float64(config.Workers))
	simdTier := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "verifoxx", Subsystem: "simd", Name: "tier_info",
		Help: "Selected SIMD runtime tier.",
	}, []string{"tier"})
	simdTier.WithLabelValues(config.SIMDTier).Set(1)

	registry := prometheus.NewRegistry()
	collectors := []prometheus.Collector{
		metrics.batches, metrics.failures, metrics.rows, metrics.duration, outcomes,
		queueDepth, journalFailures, workers, simdTier,
	}
	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			return nil, ErrInvalidMetrics
		}
	}
	metrics.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return metrics, nil
}

// ObserveBatch records one complete aggregate update.
func (metrics *Metrics) ObserveBatch(observation BatchObservation) error {
	if metrics == nil || metrics.handler == nil || observation.Duration < 0 {
		return ErrInvalidObservation
	}
	var outcomes uint64
	for _, count := range observation.Outcomes {
		next := outcomes + count
		if next < outcomes {
			return ErrInvalidObservation
		}
		outcomes = next
	}
	if (!observation.Failed && (observation.Rows == 0 || outcomes != observation.Rows)) ||
		(observation.Failed && outcomes > observation.Rows) {
		return ErrInvalidObservation
	}
	metrics.batches.Inc()
	if observation.Failed {
		metrics.failures.Inc()
	}
	if observation.Rows != 0 {
		metrics.rows.Add(float64(observation.Rows))
	}
	metrics.duration.Observe(observation.Duration.Seconds())
	for outcome, count := range observation.Outcomes {
		if count != 0 {
			metrics.outcomes[outcome].Add(float64(count))
		}
	}
	return nil
}

// ServeHTTP exposes this instance's private registry.
func (metrics *Metrics) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if metrics == nil || metrics.handler == nil {
		http.Error(response, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	metrics.handler.ServeHTTP(response, request)
}
