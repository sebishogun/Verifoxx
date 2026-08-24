package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonresult"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/observability"
	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/security"
	"github.com/sebishogun/verifoxx/internal/service"
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

// EngineConfig binds immutable publication dependencies and preallocated
// sequential evaluator workspaces.
type EngineConfig struct {
	Registry      *program.Registry
	Publisher     *persistence.Publisher
	Journal       auditJournal
	Metrics       *observability.Metrics
	Health        func(context.Context) error
	EngineVersion string
	AuditCapacity persistence.AuditCapacity
	AuditMode     persistence.AuditMode
	Limits        security.Limits
	Workers       int
}

type engineWorker struct {
	results  result.Batch
	executor eval.Executor
	encoder  jsonresult.Encoder
	builder  eval.Builder
	audit    persistence.AuditBatch
	decoder  jsonbatch.Decoder
}

// Engine implements the transport-independent PolicyAPI with a fixed worker
// set and immutable published Programs.
type Engine struct {
	publisher      *persistence.Publisher
	registry       *program.Registry
	journal        auditJournal
	metrics        *observability.Metrics
	health         func(context.Context) error
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
		config.Workers <= 0 || config.Workers > maxEngineWorkers || config.Limits.Validate() != nil {
		return nil, ErrInvalidRuntime
	}
	engine := &Engine{
		publisher: config.Publisher, registry: config.Registry, journal: config.Journal,
		metrics: config.Metrics, health: config.Health, workers: make(chan *engineWorker, config.Workers),
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
			return service.PolicyMetadata{}, service.ErrInvalidPolicy
		}
		return service.PolicyMetadata{}, fmt.Errorf("%w: publish policy: %v", service.ErrUnavailable, err)
	}
	engine.versionMu.Lock()
	engine.versions[version.ContentHash] = version.ID
	engine.versionMu.Unlock()
	return policyMetadata(compiled)
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
	compiled := engine.registry.Active()
	if request.ExplicitPolicy {
		var ok bool
		compiled, ok = engine.registry.Lookup(request.PolicyHash)
		if !ok {
			return nil, service.ErrPolicyNotFound
		}
	}
	if compiled == nil {
		return nil, service.ErrUnavailable
	}
	engine.versionMu.RLock()
	versionID := engine.versions[compiled.ContentHash]
	engine.versionMu.RUnlock()
	if versionID <= 0 {
		return nil, service.ErrUnavailable
	}

	worker, err := engine.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { engine.workers <- worker }()
	started := time.Now().UTC()
	batch, err := worker.decoder.Decode(&worker.builder, compiled, request.Requests, request.Evidence, engine.batchLimits)
	if err != nil {
		return nil, fmt.Errorf("%w: decode batch: %v", service.ErrInvalidRequest, err)
	}
	if err := worker.executor.Execute(&worker.results, compiled, batch); err != nil {
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
	completed := time.Now().UTC()
	if engine.auditMode != persistence.AuditOff {
		if err := buildAuditBatch(&worker.audit, auditInput{
			policyVersionID: versionID, policyHash: compiled.ContentHash, engineVersion: engine.engineVersion,
			requests: request.Requests, evidence: request.Evidence, results: output,
			started: started, completed: completed, sequence: engine.sequence.Add(1),
		}); err != nil {
			return nil, fmt.Errorf("%w: materialize audit: %v", service.ErrAuditUnavailable, err)
		}
		if err := engine.journal.Submit(ctx, &worker.audit); err != nil && engine.auditMode == persistence.AuditRequired {
			return nil, fmt.Errorf("%w: persist audit: %v", service.ErrAuditUnavailable, err)
		}
	}
	_ = engine.metrics.ObserveBatch(batchObservation(compiled, &worker.results, completed.Sub(started)))
	return output, nil
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

func (engine *Engine) valid() bool {
	return engine != nil && engine.publisher != nil && engine.registry != nil && engine.journal != nil &&
		engine.metrics != nil && engine.health != nil && engine.workers != nil && cap(engine.workers) > 0 &&
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
