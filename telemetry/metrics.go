package telemetry

import (
	"context"
	"strconv"

	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type snapshotAttributes struct {
	decisions [internaltelemetry.DecisionCount]attribute.KeyValue
	reasons   [internaltelemetry.ReasonCount]attribute.KeyValue
	audits    [internaltelemetry.AuditOutcomeCount]attribute.KeyValue
	reloads   [internaltelemetry.ReloadOutcomeCount]attribute.KeyValue
	latency   [internaltelemetry.LatencyBucketCount]attribute.KeyValue
	queue     [internaltelemetry.QueueBucketCount]attribute.KeyValue
}

func newSnapshotAttributes() snapshotAttributes {
	var attributes snapshotAttributes
	for value := internaltelemetry.Decision(0); value < internaltelemetry.DecisionCount; value++ {
		name, _ := internaltelemetry.DecisionName(value)
		attributes.decisions[value] = attribute.String("outcome", name)
	}
	for value := internaltelemetry.Reason(0); value < internaltelemetry.ReasonCount; value++ {
		name, _ := internaltelemetry.ReasonName(value)
		attributes.reasons[value] = attribute.String("reason", name)
	}
	for value := internaltelemetry.AuditOutcome(0); value < internaltelemetry.AuditOutcomeCount; value++ {
		name, _ := internaltelemetry.AuditOutcomeName(value)
		attributes.audits[value] = attribute.String("outcome", name)
	}
	for value := internaltelemetry.ReloadOutcome(0); value < internaltelemetry.ReloadOutcomeCount; value++ {
		name, _ := internaltelemetry.ReloadOutcomeName(value)
		attributes.reloads[value] = attribute.String("outcome", name)
	}
	bounds := internaltelemetry.DurationBucketBounds()
	for row := range attributes.latency {
		if row < len(bounds) {
			le := strconv.FormatFloat(bounds[row].Seconds(), 'g', -1, 64)
			attributes.latency[row] = attribute.String("le", le)
			attributes.queue[row] = attribute.String("le", le)
		} else {
			attributes.latency[row] = attribute.String("le", "+Inf")
			attributes.queue[row] = attribute.String("le", "+Inf")
		}
	}
	return attributes
}

type snapshotInstruments struct {
	batches, failures, rows metric.Int64ObservableCounter
	decisions, escalations  metric.Int64ObservableCounter
	audits, reloads         metric.Int64ObservableCounter
	drops, shutdowns        metric.Int64ObservableCounter
	queueDepth              metric.Int64ObservableGauge
	active                  metric.Int64ObservableGauge
	latencySum, queueSum    metric.Int64ObservableCounter
	latency                 [internaltelemetry.LatencyBucketCount]metric.Int64ObservableCounter
	queue                   [internaltelemetry.QueueBucketCount]metric.Int64ObservableCounter
}

// registerMetrics exposes the complete bounded snapshot through one callback
// so every instrument observes the same immutable aggregate.
func registerMetrics(
	provider *sdkmetric.MeterProvider,
	counters *internaltelemetry.Counters,
	queueDepth func() uint64,
) error {
	meter := provider.Meter("github.com/sebishogun/nornrune/telemetry")
	var instruments snapshotInstruments
	var err error
	counter := func(name, description string) metric.Int64ObservableCounter {
		if err != nil {
			return nil
		}
		var value metric.Int64ObservableCounter
		value, err = meter.Int64ObservableCounter(name, metric.WithDescription(description))
		return value
	}
	instruments.batches = counter("nornrune.evaluation.batches", "Policy evaluation batches observed, including failures.")
	instruments.failures = counter("nornrune.evaluation.batch_failures", "Policy evaluation batches reported as failed.")
	instruments.rows = counter("nornrune.evaluation.rows", "Request rows presented to policy evaluation batches.")
	instruments.decisions = counter("nornrune.evaluation.outcomes", "Policy decisions by fixed outcome.")
	instruments.escalations = counter("nornrune.evaluation.escalations", "Escalation reasons by fixed reason.")
	instruments.audits = counter("nornrune.audit.outcomes", "Bounded audit persistence outcomes.")
	instruments.reloads = counter("nornrune.policy.reloads", "Bounded policy reload outcomes.")
	instruments.drops = counter("nornrune.telemetry.export_drops", "Optional telemetry exports dropped or failed.")
	instruments.shutdowns = counter("nornrune.shutdown.failures", "Telemetry shutdown flush failures.")
	instruments.latencySum = counter("nornrune.evaluation.duration_ns", "Cumulative policy evaluation batch duration.")
	instruments.queueSum = counter("nornrune.service.queue_wait_ns", "Cumulative service admission queue wait.")
	if err != nil {
		return err
	}
	if instruments.queueDepth, err = meter.Int64ObservableGauge(
		"nornrune.service.queue_depth", metric.WithDescription("Requests currently waiting for service admission."),
	); err != nil {
		return err
	}
	if instruments.active, err = meter.Int64ObservableGauge(
		"nornrune.service.active_admissions", metric.WithDescription("Currently admitted requests across all operations."),
	); err != nil {
		return err
	}
	for row := range instruments.latency {
		if instruments.latency[row], err = meter.Int64ObservableCounter(
			"nornrune.evaluation.duration_bucket",
			metric.WithDescription("Cumulative policy evaluation batch duration buckets."),
		); err != nil {
			return err
		}
		if instruments.queue[row], err = meter.Int64ObservableCounter(
			"nornrune.service.queue_wait_bucket",
			metric.WithDescription("Cumulative service admission queue wait buckets."),
		); err != nil {
			return err
		}
	}
	attributes := newSnapshotAttributes()
	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		snapshot := counters.Snapshot()
		observer.ObserveInt64(instruments.batches, clampMetric(snapshot.Batches))
		observer.ObserveInt64(instruments.failures, clampMetric(snapshot.Failures))
		observer.ObserveInt64(instruments.rows, clampMetric(snapshot.Rows))
		for value := internaltelemetry.Decision(0); value < internaltelemetry.DecisionCount; value++ {
			observer.ObserveInt64(instruments.decisions, clampMetric(snapshot.Decisions[value]),
				metric.WithAttributes(attributes.decisions[value]))
		}
		for value := internaltelemetry.Reason(0); value < internaltelemetry.ReasonCount; value++ {
			observer.ObserveInt64(instruments.escalations, clampMetric(snapshot.Reasons[value]),
				metric.WithAttributes(attributes.reasons[value]))
		}
		for value := internaltelemetry.AuditOutcome(0); value < internaltelemetry.AuditOutcomeCount; value++ {
			observer.ObserveInt64(instruments.audits, clampMetric(snapshot.Audits[value]),
				metric.WithAttributes(attributes.audits[value]))
		}
		for value := internaltelemetry.ReloadOutcome(0); value < internaltelemetry.ReloadOutcomeCount; value++ {
			observer.ObserveInt64(instruments.reloads, clampMetric(snapshot.Reloads[value]),
				metric.WithAttributes(attributes.reloads[value]))
		}
		observer.ObserveInt64(instruments.drops, clampMetric(snapshot.ExportDrops))
		observer.ObserveInt64(instruments.shutdowns, clampMetric(snapshot.ShutdownFailures))
		depth := int64(0)
		if queueDepth != nil {
			depth = int64(queueDepth())
		}
		observer.ObserveInt64(instruments.queueDepth, depth)
		observer.ObserveInt64(instruments.active, snapshot.ActiveAdmissions)
		observer.ObserveInt64(instruments.latencySum, clampMetric(snapshot.DurationNanoseconds))
		observer.ObserveInt64(instruments.queueSum, clampMetric(snapshot.QueueWaitNanoseconds))
		// Bucket counters are cumulative monotonic counts labeled with
		// numeric-second bounds, matching the Prometheus le convention so
		// histogram functions work on OTLP-to-Prometheus converted series.
		// The final +Inf bucket carries the total observation count.
		var latencyTotal, queueTotal uint64
		for row := range instruments.latency {
			latencyTotal += snapshot.LatencyBuckets[row]
			queueTotal += snapshot.QueueBuckets[row]
			observer.ObserveInt64(instruments.latency[row], clampMetric(latencyTotal),
				metric.WithAttributes(attributes.latency[row]))
			observer.ObserveInt64(instruments.queue[row], clampMetric(queueTotal),
				metric.WithAttributes(attributes.queue[row]))
		}
		return nil
	}, instruments.batches, instruments.failures, instruments.rows, instruments.decisions,
		instruments.escalations, instruments.audits, instruments.reloads, instruments.drops,
		instruments.shutdowns, instruments.queueDepth, instruments.active, instruments.latencySum,
		instruments.queueSum, instruments.latency[0], instruments.latency[1], instruments.latency[2],
		instruments.latency[3], instruments.latency[4], instruments.latency[5], instruments.latency[6],
		instruments.latency[7], instruments.queue[0], instruments.queue[1], instruments.queue[2],
		instruments.queue[3], instruments.queue[4], instruments.queue[5], instruments.queue[6], instruments.queue[7])
	return err
}
