// Package observability exposes bounded service telemetry outside evaluator kernels.
package observability

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
)

var (
	ErrInvalidMetrics     = errors.New("observability: invalid metrics configuration")
	ErrInvalidObservation = errors.New("observability: invalid batch observation")
)

type Outcome = publictelemetry.Decision

const (
	OutcomeApprove  = publictelemetry.DecisionApprove
	OutcomeReject   = publictelemetry.DecisionReject
	OutcomeRevise   = publictelemetry.DecisionRevise
	OutcomeEscalate = publictelemetry.DecisionEscalate
	OutcomeCount    = publictelemetry.DecisionCount
)

type MetricsConfig struct {
	Runtime         *publictelemetry.Runtime
	QueueDepth      func() uint64
	JournalFailures func() uint64
	SIMDTier        string
	Workers         uint32
}

type BatchObservation struct {
	Outcomes [OutcomeCount]uint64
	Rows     uint64
	Duration time.Duration
	Failed   bool
}

type Metrics struct {
	runtime *publictelemetry.Runtime
	handler http.Handler
}

func NewMetrics(config MetricsConfig) (*Metrics, error) {
	if config.QueueDepth == nil || config.JournalFailures == nil || config.Workers == 0 ||
		len(config.SIMDTier) == 0 || len(config.SIMDTier) > 128 || !utf8.ValidString(config.SIMDTier) {
		return nil, ErrInvalidMetrics
	}
	runtime := config.Runtime
	if runtime == nil {
		var err error
		runtime, err = publictelemetry.New(context.Background(), publictelemetry.Config{
			Enabled: true, ServiceVersion: "prometheus", BuildVersion: "prometheus",
			ExportInterval: time.Second, ExportQueueSize: 1,
		})
		if err != nil {
			return nil, ErrInvalidMetrics
		}
	}
	collector := newSnapshotCollector(runtime, config)
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		_ = runtime.Shutdown(context.Background())
		return nil, ErrInvalidMetrics
	}
	return &Metrics{runtime: runtime, handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{})}, nil
}

func (metrics *Metrics) ObserveBatch(observation BatchObservation) error {
	if metrics == nil || metrics.runtime == nil || observation.Duration < 0 {
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
	delta := publictelemetry.BatchDelta{
		Decisions: observation.Outcomes, Rows: observation.Rows, Duration: observation.Duration, Batches: 1,
	}
	if observation.Failed {
		delta.Failures = 1
	}
	if err := metrics.runtime.Record(delta); err != nil {
		return ErrInvalidObservation
	}
	return nil
}

func (metrics *Metrics) Record(delta publictelemetry.BatchDelta) error {
	if metrics == nil || metrics.runtime == nil || metrics.runtime.Record(delta) != nil {
		return ErrInvalidObservation
	}
	return nil
}

func (metrics *Metrics) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if metrics == nil || metrics.handler == nil {
		http.Error(response, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	metrics.handler.ServeHTTP(response, request)
}

type snapshotCollector struct {
	desc            snapshotDescriptors
	runtime         *publictelemetry.Runtime
	queueDepth      func() uint64
	journalFailures func() uint64
	simdTier        string
	workers         uint32
}

type snapshotDescriptors struct {
	batches, failures, rows, duration, queueWait, queued, active, journalFailures prometheus.Desc
	decisions, reasons, audits, reloads, exportDrops, shutdownFailures            prometheus.Desc
	workers, simdTier                                                             prometheus.Desc
}

func newSnapshotCollector(runtime *publictelemetry.Runtime, config MetricsConfig) *snapshotCollector {
	return &snapshotCollector{
		runtime: runtime, queueDepth: config.QueueDepth, journalFailures: config.JournalFailures,
		workers: config.Workers, simdTier: config.SIMDTier,
		desc: snapshotDescriptors{
			batches:          desc("nornrune_evaluation_batches_total", "Policy evaluation batches observed, including failures."),
			failures:         desc("nornrune_evaluation_batch_failures_total", "Policy evaluation batches reported as failed."),
			rows:             desc("nornrune_evaluation_rows_total", "Request rows presented to policy evaluation batches."),
			duration:         histogramDesc("nornrune_evaluation_duration_seconds", "End-to-end policy evaluation batch duration."),
			queueWait:        histogramDesc("nornrune_service_queue_wait_seconds", "Service admission queue wait duration."),
			queued:           desc("nornrune_service_queue_depth", "Requests currently waiting for service admission."),
			active:           desc("nornrune_service_active_admissions", "Currently active admitted requests."),
			journalFailures:  desc("nornrune_audit_journal_failures_total", "Audit journal batches that failed to persist."),
			decisions:        *prometheus.NewDesc("nornrune_evaluation_outcomes_total", "Policy decisions completed in evaluation batches.", []string{"outcome"}, nil),
			reasons:          *prometheus.NewDesc("nornrune_evaluation_escalations_total", "Escalation reasons observed in evaluation batches.", []string{"reason"}, nil),
			audits:           *prometheus.NewDesc("nornrune_audit_outcomes_total", "Bounded audit persistence outcomes.", []string{"outcome"}, nil),
			reloads:          *prometheus.NewDesc("nornrune_policy_reloads_total", "Bounded policy reload outcomes.", []string{"outcome"}, nil),
			exportDrops:      desc("nornrune_telemetry_export_drops_total", "Optional telemetry exports dropped or failed."),
			shutdownFailures: desc("nornrune_shutdown_failures_total", "Telemetry shutdown flush failures."),
			workers:          desc("nornrune_evaluation_workers", "Configured evaluator worker count."),
			simdTier:         *prometheus.NewDesc("nornrune_simd_tier_info", "Selected SIMD runtime tier.", []string{"tier"}, nil),
		},
	}
}

func desc(name, help string) prometheus.Desc { return *prometheus.NewDesc(name, help, nil, nil) }
func histogramDesc(name, help string) prometheus.Desc {
	return *prometheus.NewDesc(name, help, nil, nil)
}

func (collector *snapshotCollector) Describe(output chan<- *prometheus.Desc) {
	for _, descriptor := range []*prometheus.Desc{
		&collector.desc.batches, &collector.desc.failures, &collector.desc.rows, &collector.desc.duration,
		&collector.desc.queueWait, &collector.desc.queued, &collector.desc.active, &collector.desc.journalFailures, &collector.desc.decisions,
		&collector.desc.reasons, &collector.desc.audits, &collector.desc.reloads, &collector.desc.exportDrops,
		&collector.desc.shutdownFailures, &collector.desc.workers, &collector.desc.simdTier,
	} {
		output <- descriptor
	}
}

func (collector *snapshotCollector) Collect(output chan<- prometheus.Metric) {
	snapshot := collector.runtime.Snapshot()
	output <- prometheus.MustNewConstMetric(&collector.desc.batches, prometheus.CounterValue, float64(snapshot.Batches))
	output <- prometheus.MustNewConstMetric(&collector.desc.failures, prometheus.CounterValue, float64(snapshot.Failures))
	output <- prometheus.MustNewConstMetric(&collector.desc.rows, prometheus.CounterValue, float64(snapshot.Rows))
	output <- prometheus.MustNewConstHistogram(&collector.desc.duration, snapshot.Batches,
		float64(snapshot.DurationNanoseconds)/float64(time.Second), cumulativeBuckets(snapshot.LatencyBuckets))
	output <- prometheus.MustNewConstHistogram(&collector.desc.queueWait, snapshot.QueueObservations,
		float64(snapshot.QueueWaitNanoseconds)/float64(time.Second), cumulativeBuckets(snapshot.QueueBuckets))
	output <- prometheus.MustNewConstMetric(&collector.desc.queued, prometheus.GaugeValue, float64(collector.queueDepth()))
	output <- prometheus.MustNewConstMetric(&collector.desc.active, prometheus.GaugeValue, float64(snapshot.ActiveAdmissions))
	output <- prometheus.MustNewConstMetric(&collector.desc.journalFailures, prometheus.CounterValue, float64(collector.journalFailures()))
	for value := publictelemetry.Decision(0); value < publictelemetry.DecisionCount; value++ {
		name, _ := publictelemetry.DecisionName(value)
		output <- prometheus.MustNewConstMetric(&collector.desc.decisions, prometheus.CounterValue, float64(snapshot.Decisions[value]), name)
	}
	for value := publictelemetry.Reason(0); value < publictelemetry.ReasonCount; value++ {
		name, _ := publictelemetry.ReasonName(value)
		output <- prometheus.MustNewConstMetric(&collector.desc.reasons, prometheus.CounterValue, float64(snapshot.Reasons[value]), name)
	}
	for value := publictelemetry.AuditOutcome(0); value < publictelemetry.AuditOutcomeCount; value++ {
		name, _ := publictelemetry.AuditOutcomeName(value)
		output <- prometheus.MustNewConstMetric(&collector.desc.audits, prometheus.CounterValue, float64(snapshot.Audits[value]), name)
	}
	for value := publictelemetry.ReloadOutcome(0); value < publictelemetry.ReloadOutcomeCount; value++ {
		name, _ := publictelemetry.ReloadOutcomeName(value)
		output <- prometheus.MustNewConstMetric(&collector.desc.reloads, prometheus.CounterValue, float64(snapshot.Reloads[value]), name)
	}
	output <- prometheus.MustNewConstMetric(&collector.desc.exportDrops, prometheus.CounterValue, float64(snapshot.ExportDrops))
	output <- prometheus.MustNewConstMetric(&collector.desc.shutdownFailures, prometheus.CounterValue, float64(snapshot.ShutdownFailures))
	output <- prometheus.MustNewConstMetric(&collector.desc.workers, prometheus.GaugeValue, float64(collector.workers))
	output <- prometheus.MustNewConstMetric(&collector.desc.simdTier, prometheus.GaugeValue, 1, collector.simdTier)
}

func cumulativeBuckets(values [publictelemetry.LatencyBucketCount]uint64) map[float64]uint64 {
	bounds := publictelemetry.DurationBucketBounds()
	buckets := make(map[float64]uint64, len(bounds))
	var total uint64
	for row, bound := range bounds {
		total = internaltelemetry.SaturatingSum(total, values[row])
		buckets[bound.Seconds()] = total
	}
	return buckets
}
