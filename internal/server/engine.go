package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sebishogun/nornrune/internal/adapters/jsonbatch"
	"github.com/sebishogun/nornrune/internal/adapters/jsonresult"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/observability"
	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/scheduler"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/security"
	"github.com/sebishogun/nornrune/internal/service"
	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
)

const maxEngineWorkers = 256

var (
	approveOutcomeName  = []byte("Approve")
	rejectOutcomeName   = []byte("Reject")
	reviseOutcomeName   = []byte("Revise")
	escalateOutcomeName = []byte("Escalate")
)

func batchDecoderLimits(limits security.Limits) jsonbatch.Limits {
	return jsonbatch.Limits{
		MaxRequestBytes:       limits.MaxRequestBytes,
		MaxEvidenceBytes:      limits.MaxRequestBytes,
		MaxStringBytes:        1 << 20,
		MaxRequests:           limits.MaxBatchRows,
		MaxEvidence:           limits.MaxEvidenceRecords,
		MaxEvidenceRefs:       1 << 20,
		MaxFactsPerRequest:    256,
		MaxEvidenceAttributes: 64,
		MaxDepth:              limits.MaxASTDepth,
	}
}

type auditJournal interface {
	Submit(context.Context, *persistence.AuditBatch) error
}

// EngineConfig binds immutable publication dependencies and bounded workspaces.
type EngineConfig struct {
	Registry      *program.Registry
	Publisher     *persistence.Publisher
	Journal       auditJournal
	Metrics       *observability.Metrics
	Telemetry     *publictelemetry.Runtime
	Health        func(context.Context) error
	EngineVersion string
	AuditCapacity persistence.AuditCapacity
	AuditMode     persistence.AuditMode
	Limits        security.Limits
	Workers       int
	QueueDepth    int
}

type engineWorker struct {
	results result.Batch
	encoder jsonresult.Encoder
	builder eval.Builder
	audit   persistence.AuditBatch
	decoder jsonbatch.Decoder
}

// Engine implements the transport-independent PolicyAPI with a fixed worker
// set and immutable published Programs.
type Engine struct {
	telemetry      *publictelemetry.Runtime
	publisher      *persistence.Publisher
	registry       *program.Registry
	journal        auditJournal
	metrics        *observability.Metrics
	health         func(context.Context) error
	now            func() time.Time
	scheduler      *scheduler.Scheduler
	workers        chan *engineWorker
	versions       map[[32]byte]persistence.PolicyVersionID
	engineVersion  string
	batchLimits    jsonbatch.Limits
	limits         security.Limits
	versionMu      sync.RWMutex
	sequence       atomic.Uint64
	maxPolicyBytes int
	maxOutputBytes int
	auditMode      persistence.AuditMode
}

// NewEngine allocates all evaluator and audit workspaces before serving.
func NewEngine(config EngineConfig) (*Engine, error) {
	if config.Registry == nil || config.Publisher == nil || config.Journal == nil || config.Metrics == nil ||
		config.Health == nil || config.EngineVersion == "" || !config.AuditMode.Valid() ||
		config.Workers <= 0 || config.Workers > maxEngineWorkers || config.QueueDepth <= 0 || config.Limits.Validate() != nil {
		return nil, ErrInvalidRuntime
	}
	engine := &Engine{
		telemetry: config.Telemetry, publisher: config.Publisher, registry: config.Registry,
		journal: config.Journal, metrics: config.Metrics, health: config.Health, now: time.Now,
		workers:  make(chan *engineWorker, config.Workers),
		versions: make(map[[32]byte]persistence.PolicyVersionID), engineVersion: config.EngineVersion,
		batchLimits: batchDecoderLimits(config.Limits), limits: config.Limits, maxPolicyBytes: config.Limits.MaxPolicyBytes,
		maxOutputBytes: config.Limits.MaxOutputBytes, auditMode: config.AuditMode,
	}
	for range config.Workers {
		worker := &engineWorker{}
		if config.AuditMode != persistence.AuditOff {
			audit, err := persistence.NewAuditBatch(config.AuditCapacity)
			if err != nil {
				return nil, ErrInvalidRuntime
			}
			worker.audit = audit
		}
		engine.workers <- worker
	}
	queueDepth := min(config.QueueDepth, config.Workers)
	evaluationScheduler, err := scheduler.NewScheduler(scheduler.Config{
		Capacity:   scheduler.Capacity{Rows: config.Limits.MaxBatchRows},
		Workers:    config.Workers,
		QueueDepth: queueDepth,
	})
	if err != nil {
		return nil, ErrInvalidRuntime
	}
	engine.scheduler = evaluationScheduler
	return engine, nil
}

// ValidatePolicy validates source without publishing it.
func (engine *Engine) ValidatePolicy(ctx context.Context, source []byte) (service.Validation, error) {
	if !engine.valid() || ctx == nil || len(source) == 0 || len(source) > engine.maxPolicyBytes {
		return service.Validation{}, service.ErrInvalidPolicy
	}
	if err := ctx.Err(); err != nil {
		return service.Validation{}, err
	}
	diagnostics, err := validatePolicySourceWithLimits(source, engine.limits)
	if err != nil {
		return service.Validation{}, err
	}
	return service.Validation{Diagnostics: diagnostics}, nil
}

// CompilePolicy persists, publishes, and activates valid source.
func (engine *Engine) CompilePolicy(ctx context.Context, source []byte) (service.PolicyMetadata, error) {
	if !engine.valid() || ctx == nil || len(source) == 0 || len(source) > engine.maxPolicyBytes {
		return service.PolicyMetadata{}, service.ErrInvalidPolicy
	}
	compiled, version, err := engine.publisher.Publish(ctx, source)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPolicy) {
			engine.recordReload(publictelemetry.ReloadInvalid)
			return service.PolicyMetadata{}, service.ErrInvalidPolicy
		}
		engine.recordReload(publictelemetry.ReloadPersistenceFailure)
		return service.PolicyMetadata{}, fmt.Errorf("%w: publish policy: %v", service.ErrUnavailable, err)
	}
	engine.versionMu.Lock()
	engine.versions[version.ContentHash] = version.ID
	engine.versionMu.Unlock()
	engine.recordReload(publictelemetry.ReloadSuccess)
	return policyMetadata(compiled)
}

func (engine *Engine) recordReload(outcome publictelemetry.ReloadOutcome) {
	if engine == nil || engine.telemetry == nil {
		return
	}
	delta := publictelemetry.BatchDelta{}
	delta.Reloads[outcome] = 1
	_ = engine.telemetry.Record(delta)
}

// LookupPolicy returns one already-published immutable policy.
func (engine *Engine) LookupPolicy(ctx context.Context, hash [32]byte) (service.PolicyMetadata, error) {
	if !engine.valid() || ctx == nil || hash == [32]byte{} {
		return service.PolicyMetadata{}, service.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return service.PolicyMetadata{}, err
	}
	compiled, ok := engine.registry.Lookup(hash)
	if !ok {
		return service.PolicyMetadata{}, service.ErrPolicyNotFound
	}
	return policyMetadata(compiled)
}

// EvaluateBatch decodes, executes, encodes, and journals one admitted batch.
func (engine *Engine) EvaluateBatch(
	ctx context.Context,
	request service.EvaluationRequest,
	dst []byte,
) ([]byte, error) {
	if !engine.valid() || ctx == nil || len(request.Requests) == 0 || len(request.Evidence) == 0 {
		return nil, service.ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, lookupSpan := engine.telemetry.Start(ctx, publictelemetry.OperationPolicyLookup, publictelemetry.TransportService)
	compiled := engine.registry.Active()
	if request.ExplicitPolicy {
		var ok bool
		compiled, ok = engine.registry.Lookup(request.PolicyHash)
		if !ok {
			engine.telemetry.Finish(lookupSpan, publictelemetry.SpanStatusNotFound)
			return nil, service.ErrPolicyNotFound
		}
	}
	if compiled == nil {
		engine.telemetry.Finish(lookupSpan, publictelemetry.SpanStatusUnavailable)
		return nil, service.ErrUnavailable
	}
	engine.versionMu.RLock()
	versionID := engine.versions[compiled.ContentHash]
	engine.versionMu.RUnlock()
	if versionID <= 0 {
		engine.telemetry.Finish(lookupSpan, publictelemetry.SpanStatusUnavailable)
		return nil, service.ErrUnavailable
	}
	engine.telemetry.Finish(lookupSpan, publictelemetry.SpanStatusOK)

	worker, err := engine.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { engine.workers <- worker }()
	started := engine.now()
	batch, err := worker.decoder.Decode(&worker.builder, compiled, request.Requests, request.Evidence, engine.batchLimits)
	if err != nil {
		return nil, fmt.Errorf("%w: decode batch: %v", service.ErrInvalidRequest, err)
	}
	recorded := false
	defer func() {
		if !recorded {
			engine.recordEvaluationFailure(uint64(batch.Rows), nonNegativeElapsed(started, engine.now()))
		}
	}()
	if err := engine.scheduler.Execute(ctx, &worker.results, compiled, batch); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: evaluate batch: %v", service.ErrUnavailable, err)
	}
	if err := worker.encoder.Bind(compiled); err != nil {
		return nil, fmt.Errorf("%w: bind result encoder: %v", service.ErrUnavailable, err)
	}
	output := dst
	if engine.auditMode != persistence.AuditOff {
		output = nil
	}
	output, err = worker.encoder.Append(output, batch.RequestIDs, &worker.results, []byte(engine.engineVersion))
	if err != nil {
		return nil, fmt.Errorf("%w: encode results: %v", service.ErrUnavailable, err)
	}
	if len(output) > engine.maxOutputBytes {
		return nil, fmt.Errorf("%w: encoded output exceeds service limit", service.ErrInvalidRequest)
	}
	completed := engine.now()
	if engine.auditMode != persistence.AuditOff {
		_, auditSpan := engine.telemetry.Start(ctx, publictelemetry.OperationAuditAcknowledgment, publictelemetry.TransportService)
		auditStarted := started.UTC()
		if err := buildAuditBatch(&worker.audit, auditInput{
			policyVersionID: versionID, policyHash: compiled.ContentHash, engineVersion: engine.engineVersion,
			requests: request.Requests, evidence: request.Evidence, results: output,
			started: auditStarted, completed: auditStarted.Add(nonNegativeElapsed(started, completed)), sequence: engine.sequence.Add(1),
		}); err != nil {
			engine.telemetry.Finish(auditSpan, publictelemetry.SpanStatusInternal)
			return nil, fmt.Errorf("%w: materialize audit: %v", service.ErrAuditUnavailable, err)
		}
		if err := engine.journal.Submit(ctx, &worker.audit); err != nil {
			if engine.auditMode == persistence.AuditRequired {
				engine.telemetry.Finish(auditSpan, serviceSpanStatus(err))
				engine.recordAudit(publictelemetry.AuditRequiredFailure)
				return nil, fmt.Errorf("%w: persist audit: %v", service.ErrAuditUnavailable, err)
			}
			engine.telemetry.Finish(auditSpan, serviceSpanStatus(err))
			engine.recordAudit(publictelemetry.AuditOptionalDrop)
		} else if engine.auditMode == persistence.AuditRequired {
			engine.recordAudit(publictelemetry.AuditPersisted)
			engine.telemetry.Finish(auditSpan, publictelemetry.SpanStatusOK)
		} else {
			engine.telemetry.Finish(auditSpan, publictelemetry.SpanStatusOK)
		}
	}
	engine.recordEvaluation(ctx, compiled, &worker.results, nonNegativeElapsed(started, engine.now()))
	recorded = true
	return output, nil
}

func nonNegativeElapsed(started, completed time.Time) time.Duration {
	elapsed := completed.Sub(started)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func (engine *Engine) recordAudit(outcome publictelemetry.AuditOutcome) {
	if engine == nil || engine.telemetry == nil {
		return
	}
	delta := publictelemetry.BatchDelta{}
	delta.Audits[outcome] = 1
	_ = engine.telemetry.Record(delta)
}

func (engine *Engine) recordEvaluation(ctx context.Context, compiled *program.Program, batch *result.Batch, elapsed time.Duration) {
	if ids, err := engineOutcomeIDs(compiled); err == nil && engine.telemetry != nil {
		if err := engine.telemetry.ObserveEvaluation(ctx, batch, ids, elapsed); err == nil {
			return
		}
	}
	_ = engine.metrics.ObserveBatch(batchObservation(compiled, batch, elapsed))
}

func serviceSpanStatus(err error) publictelemetry.SpanStatus {
	switch {
	case err == nil:
		return publictelemetry.SpanStatusOK
	case errors.Is(err, context.Canceled):
		return publictelemetry.SpanStatusCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return publictelemetry.SpanStatusDeadlineExceeded
	case errors.Is(err, service.ErrInvalidRequest), errors.Is(err, service.ErrInvalidPolicy):
		return publictelemetry.SpanStatusInvalidArgument
	case errors.Is(err, service.ErrPolicyNotFound):
		return publictelemetry.SpanStatusNotFound
	case errors.Is(err, service.ErrAuditUnavailable), errors.Is(err, service.ErrServiceBusy),
		errors.Is(err, service.ErrServiceStopping), errors.Is(err, service.ErrUnavailable):
		return publictelemetry.SpanStatusUnavailable
	default:
		return publictelemetry.SpanStatusInternal
	}
}

func (engine *Engine) recordEvaluationFailure(rows uint64, elapsed time.Duration) {
	_ = engine.metrics.ObserveBatch(observability.BatchObservation{Rows: rows, Duration: elapsed, Failed: true})
}

var errUnknownOutcomeIDs = errors.New("server: policy does not define the four fixed decisions")

func engineOutcomeIDs(compiled *program.Program) (internaltelemetry.OutcomeIDs, error) {
	var ids internaltelemetry.OutcomeIDs
	var found uint8
	for row := 1; row <= len(compiled.Outcomes.Names); row++ {
		outcome, ok := compiled.Outcomes.Lookup(schema.OutcomeID(row))
		if !ok {
			continue
		}
		name, ok := compiled.Symbol(outcome.Name)
		if !ok {
			continue
		}
		var id *schema.OutcomeID
		switch {
		case bytes.Equal(name, approveOutcomeName):
			id, found = &ids.Approve, found|1
		case bytes.Equal(name, rejectOutcomeName):
			id, found = &ids.Reject, found|2
		case bytes.Equal(name, reviseOutcomeName):
			id, found = &ids.Revise, found|4
		case bytes.Equal(name, escalateOutcomeName):
			id, found = &ids.Escalate, found|8
		default:
			continue
		}
		if *id != 0 {
			return internaltelemetry.OutcomeIDs{}, errUnknownOutcomeIDs
		}
		*id = schema.OutcomeID(row)
	}
	if found != 15 {
		return internaltelemetry.OutcomeIDs{}, errUnknownOutcomeIDs
	}
	return ids, nil
}

// Health checks the active policy and runtime dependency set.
func (engine *Engine) Health(ctx context.Context) error {
	if !engine.valid() || ctx == nil {
		return service.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if engine.registry.Active() == nil {
		return service.ErrUnavailable
	}
	if err := engine.health(ctx); err != nil {
		return fmt.Errorf("%w: %v", service.ErrUnavailable, err)
	}
	return nil
}

// Close rejects new evaluation work and joins the fixed scheduler workers.
func (engine *Engine) Close(ctx context.Context) error {
	if engine == nil || engine.scheduler == nil {
		return ErrInvalidRuntime
	}
	return engine.scheduler.CloseContext(ctx)
}

func (engine *Engine) valid() bool {
	return engine != nil && engine.publisher != nil && engine.registry != nil && engine.journal != nil &&
		engine.metrics != nil && engine.health != nil && engine.now != nil && engine.scheduler != nil && engine.workers != nil && cap(engine.workers) > 0 &&
		engine.engineVersion != "" && engine.maxPolicyBytes > 0 && engine.maxOutputBytes > 0 && engine.auditMode.Valid()
}

func (engine *Engine) acquire(ctx context.Context) (*engineWorker, error) {
	select {
	case worker := <-engine.workers:
		return worker, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func policyMetadata(compiled *program.Program) (service.PolicyMetadata, error) {
	if compiled == nil {
		return service.PolicyMetadata{}, service.ErrUnavailable
	}
	name, nameOK := compiled.Symbol(compiled.PolicyName)
	version, versionOK := compiled.Symbol(compiled.PolicyVersion)
	if !nameOK || !versionOK {
		return service.PolicyMetadata{}, service.ErrUnavailable
	}
	return service.PolicyMetadata{
		Name: name, Version: version, ContentHash: compiled.ContentHash,
		Instructions: uint32(len(compiled.Opcodes)), Requirements: uint32(len(compiled.RequirementIDs)),
		Clauses: uint32(len(compiled.ClauseAssertionRoots)),
	}, nil
}

func batchObservation(compiled *program.Program, batch *result.Batch, elapsed time.Duration) observability.BatchObservation {
	observation := observability.BatchObservation{Rows: uint64(batch.Rows), Duration: elapsed}
	for _, id := range batch.OutcomeIDs {
		outcome, ok := compiled.Outcomes.Lookup(id)
		if !ok {
			observation.Failed = true
			continue
		}
		name, ok := compiled.Symbol(outcome.Name)
		if !ok {
			observation.Failed = true
			continue
		}
		switch {
		case bytes.Equal(name, approveOutcomeName):
			observation.Outcomes[observability.OutcomeApprove]++
		case bytes.Equal(name, rejectOutcomeName):
			observation.Outcomes[observability.OutcomeReject]++
		case bytes.Equal(name, reviseOutcomeName):
			observation.Outcomes[observability.OutcomeRevise]++
		case bytes.Equal(name, escalateOutcomeName):
			observation.Outcomes[observability.OutcomeEscalate]++
		default:
			observation.Failed = true
		}
	}
	return observation
}
