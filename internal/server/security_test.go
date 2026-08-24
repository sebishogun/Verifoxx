package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/buildinfo"
	"github.com/sebishogun/verifoxx/internal/config"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/observability"
	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/security"
	"github.com/sebishogun/verifoxx/internal/service"
	"github.com/sebishogun/verifoxx/internal/simdops"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

func TestEngineEnforcesConfiguredBatchLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*security.Limits)
	}{
		{name: "rows", mutate: func(value *security.Limits) { value.MaxBatchRows = 4 }},
		{name: "evidence", mutate: func(value *security.Limits) { value.MaxEvidenceRecords = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := security.DefaultLimits()
			test.mutate(&limits)
			engine := newSecurityTestEngine(t, limits)
			_, err := engine.EvaluateBatch(context.Background(), service.EvaluationRequest{
				Requests: []byte(fixtures.RequestsJSON()), Evidence: []byte(fixtures.EvidenceJSON()),
			}, nil)
			if !errors.Is(err, service.ErrInvalidRequest) {
				t.Fatalf("EvaluateBatch() error = %v, want %v", err, service.ErrInvalidRequest)
			}
		})
	}
}

func TestEngineEnforcesConfiguredPolicyAndOutputLimits(t *testing.T) {
	t.Parallel()

	policyLimits := security.DefaultLimits()
	policyLimits.MaxPolicyBytes = len(verifoxx.Source()) - 1
	policyEngine := newSecurityTestEngineWithoutPolicy(t, policyLimits)
	if _, err := policyEngine.CompilePolicy(context.Background(), []byte(verifoxx.Source())); !errors.Is(err, service.ErrInvalidPolicy) {
		t.Fatalf("CompilePolicy() error = %v, want %v", err, service.ErrInvalidPolicy)
	}

	outputLimits := security.DefaultLimits()
	outputLimits.MaxOutputBytes = 64
	outputEngine := newSecurityTestEngine(t, outputLimits)
	if _, err := outputEngine.EvaluateBatch(context.Background(), service.EvaluationRequest{
		Requests: []byte(fixtures.RequestsJSON()), Evidence: []byte(fixtures.EvidenceJSON()),
	}, nil); !errors.Is(err, service.ErrInvalidRequest) {
		t.Fatalf("EvaluateBatch() output error = %v, want %v", err, service.ErrInvalidRequest)
	}
}

func TestEngineEnforcesConfiguredPolicyStructureLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*security.Limits)
	}{
		{name: "AST depth", mutate: func(value *security.Limits) { value.MaxASTDepth = 1 }},
		{name: "AST nodes", mutate: func(value *security.Limits) { value.MaxASTNodes = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := security.DefaultLimits()
			test.mutate(&limits)
			engine := newSecurityTestEngineWithoutPolicy(t, limits)
			if _, err := engine.ValidatePolicy(context.Background(), []byte(verifoxx.Source())); !errors.Is(err, service.ErrInvalidPolicy) {
				t.Fatalf("ValidatePolicy() error = %v, want %v", err, service.ErrInvalidPolicy)
			}
			if _, err := engine.CompilePolicy(context.Background(), []byte(verifoxx.Source())); !errors.Is(err, service.ErrInvalidPolicy) {
				t.Fatalf("CompilePolicy() error = %v, want %v", err, service.ErrInvalidPolicy)
			}
		})
	}
}

func TestAuditRejectsProtectedDatasetRows(t *testing.T) {
	t.Parallel()

	requests := [][]byte{
		[]byte(`{"requests":[{"id":"R1","rows":[{"email":"private-row-value"}]}]}`),
		[]byte(`{"requests":[{"id":"R1","dataset_\u0072ows":[{"email":"private-row-value"}]}]}`),
	}
	for _, request := range requests {
		batch, err := persistence.NewAuditBatch(persistence.AuditCapacity{
			Bytes: 4096, Requests: 1, Evidence: 1, Rows: 1, EvidenceLinks: 1,
		})
		if err != nil {
			t.Fatalf("NewAuditBatch() error = %v", err)
		}
		started := time.Unix(1, 0).UTC()
		err = buildAuditBatch(&batch, auditInput{
			policyVersionID: 1,
			policyHash:      sha256.Sum256([]byte("policy")),
			engineVersion:   "test",
			requests:        request,
			evidence:        []byte(`{"evidence":[]}`),
			results:         []byte(`{"results":[{"request_id":"R1","decision":"Approve","rationale":"allowed","driver":{"requirement_id":"R1","clause_id":"C1","reason":"matched"},"requirements_applied":[],"evidence_used":[],"missing_or_conflicting_evidence":[],"assumptions":[],"unresolved_uncertainty":[],"remediation":[]}]}`),
			started:         started,
			completed:       started.Add(time.Millisecond),
			sequence:        1,
		})
		if !errors.Is(err, persistence.ErrInvalidAuditBatch) {
			t.Fatalf("buildAuditBatch(%s) error = %v, want %v", request, err, persistence.ErrInvalidAuditBatch)
		}
		if bytes.Contains(batch.Bytes, []byte("private-row-value")) {
			t.Fatal("rejected audit batch retained protected row bytes")
		}
	}
}

func TestRuntimeSecurityLimitsRejectRequestByteOverflow(t *testing.T) {
	t.Parallel()

	configuration := config.Default()
	configuration.MaxBodyBytes = 1<<32 + 1
	if _, err := runtimeSecurityLimits(configuration); !errors.Is(err, ErrInvalidRuntime) {
		t.Fatalf("runtimeSecurityLimits() error = %v, want %v", err, ErrInvalidRuntime)
	}
}

func TestRuntimeSecurityLimitsKeepPolicyAndTransportBoundsIndependent(t *testing.T) {
	t.Parallel()

	configuration := config.Default()
	configuration.MaxBodyBytes = 1024
	limits, err := runtimeSecurityLimits(configuration)
	if err != nil {
		t.Fatalf("runtimeSecurityLimits() error = %v", err)
	}
	if limits.MaxRequestBytes != 1024 || limits.MaxOutputBytes != 1024 ||
		limits.MaxPolicyBytes != security.MaximumPolicyBytes {
		t.Fatalf("runtimeSecurityLimits() = %+v", limits)
	}
}

func newSecurityTestEngine(t *testing.T, limits security.Limits) *Engine {
	return newSecurityTestEngineWithWorkers(t, limits, 1)
}

func newSecurityTestEngineWithWorkers(t *testing.T, limits security.Limits, workers int) *Engine {
	t.Helper()
	engine := newSecurityTestEngineWithoutPolicyAndWorkers(t, limits, workers)
	if _, err := engine.CompilePolicy(context.Background(), []byte(verifoxx.Source())); err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	return engine
}

func newSecurityTestEngineWithoutPolicy(t *testing.T, limits security.Limits) *Engine {
	return newSecurityTestEngineWithoutPolicyAndWorkers(t, limits, 1)
}

func newSecurityTestEngineWithoutPolicyAndWorkers(t *testing.T, limits security.Limits, workers int) *Engine {
	t.Helper()
	store := &memoryPolicyStore{}
	registry := &program.Registry{}
	publisher, err := persistence.NewPublisher(store, registry, func(source []byte) (*program.Program, error) {
		return compilePolicySourceWithLimits(source, limits)
	}, buildinfo.Version())
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: simdops.Runtime().Tier, Workers: uint32(workers),
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	engine, err := NewEngine(EngineConfig{
		Registry: registry, Publisher: publisher, Journal: &captureJournal{}, Metrics: metrics,
		Health: func(context.Context) error { return nil }, EngineVersion: buildinfo.Version(),
		AuditMode: persistence.AuditOff, Workers: workers, QueueDepth: workers, Limits: limits,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(context.Background()); err != nil {
			t.Errorf("Engine.Close() error = %v", err)
		}
	})
	return engine
}
