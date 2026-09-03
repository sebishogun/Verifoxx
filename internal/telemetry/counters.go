// Package telemetry stores bounded service telemetry outside evaluator kernels.
package telemetry

import (
	"errors"
	"math"
	"sync/atomic"
	"time"
)

var ErrInvalidDelta = errors.New("telemetry: invalid counter delta")

type Decision uint8

const (
	DecisionApprove Decision = iota
	DecisionReject
	DecisionRevise
	DecisionEscalate
	DecisionCount
)

type Reason uint8

const (
	ReasonMissing Reason = iota
	ReasonStale
	ReasonUnclear
	ReasonUnverifiable
	ReasonWrongScope
	ReasonWrongSubject
	ReasonWrongTiming
	ReasonInvalid
	ReasonConflict
	ReasonCount
)

type AuditOutcome uint8

const (
	AuditPersisted AuditOutcome = iota
	AuditOptionalDrop
	AuditRequiredFailure
	AuditOutcomeCount
)

type ReloadOutcome uint8

const (
	ReloadSuccess ReloadOutcome = iota
	ReloadInvalid
	ReloadPersistenceFailure
	ReloadOutcomeCount
)

const (
	LatencyBucketCount = 8
	QueueBucketCount   = LatencyBucketCount
)

var (
	decisionNames = [DecisionCount]string{"approve", "reject", "revise", "escalate"}
	reasonNames   = [ReasonCount]string{
		"missing", "stale", "unclear", "unverifiable", "wrong_scope", "wrong_subject", "wrong_timing", "invalid", "conflict",
	}
	auditOutcomeNames  = [AuditOutcomeCount]string{"persisted", "optional_drop", "required_failure"}
	reloadOutcomeNames = [ReloadOutcomeCount]string{"success", "invalid", "persistence_failure"}
	durationBounds     = [LatencyBucketCount - 1]time.Duration{
		10 * time.Microsecond,
		100 * time.Microsecond,
		time.Millisecond,
		10 * time.Millisecond,
		100 * time.Millisecond,
		time.Second,
		10 * time.Second,
	}
)

// DurationBucketBounds returns the fixed duration bucket upper bounds so
// every exporter labels histogram buckets from one source.
func DurationBucketBounds() [LatencyBucketCount - 1]time.Duration {
	return durationBounds
}

func DecisionName(value Decision) (string, bool) {
	if value >= DecisionCount {
		return "", false
	}
	return decisionNames[value], true
}

func ReasonName(value Reason) (string, bool) {
	if value >= ReasonCount {
		return "", false
	}
	return reasonNames[value], true
}

func AuditOutcomeName(value AuditOutcome) (string, bool) {
	if value >= AuditOutcomeCount {
		return "", false
	}
	return auditOutcomeNames[value], true
}

func ReloadOutcomeName(value ReloadOutcome) (string, bool) {
	if value >= ReloadOutcomeCount {
		return "", false
	}
	return reloadOutcomeNames[value], true
}

type BatchDelta struct {
	Decisions     [DecisionCount]uint64
	Reasons       [ReasonCount]uint64
	Audits        [AuditOutcomeCount]uint64
	Reloads       [ReloadOutcomeCount]uint64
	Duration      time.Duration
	QueueWait     time.Duration
	QueueObserved bool
	Batches       uint64
	Failures      uint64
	Rows          uint64
}

type Snapshot struct {
	Decisions            [DecisionCount]uint64
	Reasons              [ReasonCount]uint64
	Audits               [AuditOutcomeCount]uint64
	Reloads              [ReloadOutcomeCount]uint64
	LatencyBuckets       [LatencyBucketCount]uint64
	QueueBuckets         [QueueBucketCount]uint64
	Batches              uint64
	Failures             uint64
	Rows                 uint64
	DurationNanoseconds  uint64
	QueueWaitNanoseconds uint64
	QueueObservations    uint64
	ShutdownFailures     uint64
	ExportDrops          uint64
	ActiveAdmissions     int64
}

type decisionCounters struct {
	values [DecisionCount]atomic.Uint64
	_      [32]byte
}

type reasonCounters struct {
	values [ReasonCount]atomic.Uint64
	_      [56]byte
}

type outcomeCounters struct {
	audits  [AuditOutcomeCount]atomic.Uint64
	reloads [ReloadOutcomeCount]atomic.Uint64
	_       [16]byte
}

type durationCounters struct {
	latency [LatencyBucketCount]atomic.Uint64
	queue   [QueueBucketCount]atomic.Uint64
}

type totalCounters struct {
	failures             atomic.Uint64
	rows                 atomic.Uint64
	durationNanoseconds  atomic.Uint64
	queueWaitNanoseconds atomic.Uint64
	shutdownFailures     atomic.Uint64
	exportDrops          atomic.Uint64
	activeAdmissions     atomic.Int64
	_                    [24]byte
}

type Counters struct {
	decisions decisionCounters
	reasons   reasonCounters
	outcomes  outcomeCounters
	durations durationCounters
	totals    totalCounters
}

func (counters *Counters) Add(delta BatchDelta) error {
	if counters == nil || !validDelta(delta) {
		return ErrInvalidDelta
	}
	for row, value := range delta.Decisions {
		saturatingAdd(&counters.decisions.values[row], value)
	}
	for row, value := range delta.Reasons {
		saturatingAdd(&counters.reasons.values[row], value)
	}
	for row, value := range delta.Audits {
		saturatingAdd(&counters.outcomes.audits[row], value)
	}
	for row, value := range delta.Reloads {
		saturatingAdd(&counters.outcomes.reloads[row], value)
	}
	if delta.Batches != 0 {
		saturatingAdd(&counters.durations.latency[durationBucket(delta.Duration)], delta.Batches)
	}
	if delta.QueueObserved {
		saturatingAdd(&counters.durations.queue[durationBucket(delta.QueueWait)], 1)
		saturatingAdd(&counters.totals.queueWaitNanoseconds, uint64(delta.QueueWait))
	}
	saturatingAdd(&counters.totals.failures, delta.Failures)
	saturatingAdd(&counters.totals.rows, delta.Rows)
	saturatingAdd(&counters.totals.durationNanoseconds, uint64(delta.Duration))
	return nil
}

func (counters *Counters) AddExportDrop(count uint64) {
	if counters != nil {
		saturatingAdd(&counters.totals.exportDrops, count)
	}
}

func (counters *Counters) AddShutdownFailure() {
	if counters != nil {
		saturatingAdd(&counters.totals.shutdownFailures, 1)
	}
}

func (counters *Counters) AdmissionStarted() {
	if counters != nil {
		counters.totals.activeAdmissions.Add(1)
	}
}

func (counters *Counters) ObserveAdmission(queueWait time.Duration) error {
	if counters == nil || queueWait < 0 {
		return ErrInvalidDelta
	}
	saturatingAdd(&counters.durations.queue[durationBucket(queueWait)], 1)
	saturatingAdd(&counters.totals.queueWaitNanoseconds, uint64(queueWait))
	counters.AdmissionStarted()
	return nil
}

func (counters *Counters) AdmissionFinished() {
	if counters == nil {
		return
	}
	for current := counters.totals.activeAdmissions.Load(); current > 0; current = counters.totals.activeAdmissions.Load() {
		if counters.totals.activeAdmissions.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func (counters *Counters) Snapshot() Snapshot {
	if counters == nil {
		return Snapshot{}
	}
	var snapshot Snapshot
	for row := range snapshot.Decisions {
		snapshot.Decisions[row] = counters.decisions.values[row].Load()
	}
	for row := range snapshot.Reasons {
		snapshot.Reasons[row] = counters.reasons.values[row].Load()
	}
	for row := range snapshot.Audits {
		snapshot.Audits[row] = counters.outcomes.audits[row].Load()
	}
	for row := range snapshot.Reloads {
		snapshot.Reloads[row] = counters.outcomes.reloads[row].Load()
	}
	var latencyTotal, queueTotal uint64
	for row := range snapshot.LatencyBuckets {
		snapshot.LatencyBuckets[row] = counters.durations.latency[row].Load()
		snapshot.QueueBuckets[row] = counters.durations.queue[row].Load()
		latencyTotal = SaturatingSum(latencyTotal, snapshot.LatencyBuckets[row])
		queueTotal = SaturatingSum(queueTotal, snapshot.QueueBuckets[row])
	}
	snapshot.Batches = latencyTotal
	snapshot.Failures = counters.totals.failures.Load()
	snapshot.Rows = counters.totals.rows.Load()
	snapshot.DurationNanoseconds = counters.totals.durationNanoseconds.Load()
	snapshot.QueueWaitNanoseconds = counters.totals.queueWaitNanoseconds.Load()
	snapshot.QueueObservations = queueTotal
	snapshot.ShutdownFailures = counters.totals.shutdownFailures.Load()
	snapshot.ExportDrops = counters.totals.exportDrops.Load()
	snapshot.ActiveAdmissions = counters.totals.activeAdmissions.Load()
	return snapshot
}

func validDelta(delta BatchDelta) bool {
	if delta.Duration < 0 || delta.QueueWait < 0 {
		return false
	}
	var decisions uint64
	for _, count := range delta.Decisions {
		if math.MaxUint64-decisions < count {
			return false
		}
		decisions += count
	}
	if delta.Batches == 0 {
		return delta.Failures == 0 && delta.Rows == 0 && decisions == 0
	}
	if delta.Failures > delta.Batches {
		return false
	}
	if delta.Failures != 0 {
		return decisions <= delta.Rows
	}
	return (delta.Rows == 0 && decisions == 0) || decisions == delta.Rows
}

func durationBucket(value time.Duration) int {
	for row, bound := range durationBounds {
		if value <= bound {
			return row
		}
	}
	return len(durationBounds)
}

func saturatingAdd(value *atomic.Uint64, delta uint64) {
	for delta != 0 {
		current := value.Load()
		next := current + delta
		if next < current {
			next = math.MaxUint64
		}
		if value.CompareAndSwap(current, next) {
			return
		}
	}
}

// SaturatingSum adds fixed telemetry counters without allowing wraparound.
func SaturatingSum(left, right uint64) uint64 {
	if math.MaxUint64-left < right {
		return math.MaxUint64
	}
	return left + right
}
